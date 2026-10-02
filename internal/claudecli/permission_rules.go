package claudecli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/tailscale/hujson"
)

var permissionFileMu sync.Mutex

func readPermissionDocument(path string) ([]byte, *hujson.Value, map[string][]string, error) {
	raw := []byte("{}\n")
	file, err := os.Open(path)
	if err == nil {
		defer file.Close()
		raw, err = io.ReadAll(io.LimitReader(file, (1<<20)+1))
	} else if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if len(raw) > 1<<20 {
		return nil, nil, nil, fmt.Errorf("settings file exceeds 1 MiB")
	}
	doc, err := hujson.Parse(bytes.Clone(raw))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid settings JSON: %w", err)
	}
	if _, ok := doc.Value.(*hujson.Object); !ok {
		return nil, nil, nil, fmt.Errorf("settings must be an object")
	}
	rules := map[string][]string{}
	if node := doc.Find("/permissions"); node != nil {
		if _, ok := node.Value.(*hujson.Object); !ok {
			return nil, nil, nil, fmt.Errorf("permissions must be an object")
		}
		for _, action := range []string{"allow", "ask", "deny"} {
			if value := doc.Find("/permissions/" + action); value != nil {
				clone := value.Clone()
				clone.Standardize()
				var values []any
				if _, ok := value.Value.(*hujson.Array); !ok {
					return nil, nil, nil, fmt.Errorf("permissions.%s must be an array of strings", action)
				}
				if err := json.Unmarshal(clone.Pack(), &values); err != nil {
					return nil, nil, nil, fmt.Errorf("permissions.%s: %w", action, err)
				}
				for _, value := range values {
					rule, ok := value.(string)
					if !ok {
						return nil, nil, nil, fmt.Errorf("permissions.%s must contain only strings", action)
					}
					rules[action] = append(rules[action], rule)
				}
			}
		}
	}
	return raw, &doc, rules, nil
}

// ReadPermissionRules reports saved rules, not the running CLI's effective policy.
func ReadPermissionRules(path string) (map[string][]string, error) {
	_, _, rules, err := readPermissionDocument(path)
	return rules, err
}

// ValidatePermissionRule checks only the native Tool or Tool(specifier) shape.
// Claude Code remains responsible for interpreting tool-specific specifiers.
func ValidatePermissionRule(rule string) error {
	tool := rule
	if i := strings.IndexByte(rule, '('); i >= 0 {
		if !strings.HasSuffix(rule, ")") || i == len(rule)-2 {
			return fmt.Errorf("use Tool or Tool(specifier), for example Bash(huggingface-cli upload:*)")
		}
		tool = rule[:i]
	}
	if tool == "" || strings.ContainsAny(rule, "\r\n\x00") {
		return fmt.Errorf("permission rule required")
	}
	for _, c := range tool {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && !strings.ContainsRune("_:-.*", c) {
			return fmt.Errorf("invalid permission tool name %q", tool)
		}
	}
	return nil
}

// EditPermissionRule updates one list, preserving unrelated keys and comments.
// Refuse symlinks so a worktree-local edit cannot mutate another scope.
func EditPermissionRule(project, action, rule string, remove bool) error {
	if strings.TrimSpace(project) == "" {
		return fmt.Errorf("Claude Code project path required")
	}
	if action != "allow" && action != "ask" && action != "deny" {
		return fmt.Errorf("permission action must be allow, ask, or deny")
	}
	rule = strings.TrimSpace(rule)
	if err := ValidatePermissionRule(rule); err != nil {
		return err
	}
	permissionFileMu.Lock()
	defer permissionFileMu.Unlock()
	dir := filepath.Join(project, ".claude")
	path := filepath.Join(dir, "settings.local.json")
	for _, p := range []string{dir, path} {
		info, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to edit symlink %s", p)
		}
	}
	original, doc, rules, err := readPermissionDocument(path)
	if err != nil {
		return err
	}
	list := rules[action]
	next := make([]string, 0, len(list)+1)
	found := false
	for _, existing := range list {
		if existing == rule {
			found = true
			if remove {
				continue
			}
		}
		next = append(next, existing)
	}
	if remove && !found {
		return fmt.Errorf("rule is not in this worktree's %s list", action)
	}
	if !remove && found {
		return nil
	}
	if !remove {
		next = append(next, rule)
	}
	if doc.Find("/permissions") == nil {
		if err := doc.Patch([]byte(`[{"op":"add","path":"/permissions","value":{}}]`)); err != nil {
			return err
		}
	}
	patch, err := json.Marshal([]map[string]any{{"op": "add", "path": "/permissions/" + action, "value": next}})
	if err != nil {
		return err
	}
	if err := doc.Patch(patch); err != nil {
		return err
	}
	doc.Format()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".permissions-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(doc.Pack()); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	current, _, _, err := readPermissionDocument(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(original, current) {
		return fmt.Errorf("Claude settings changed while saving; retry the command")
	}
	return os.Rename(temp.Name(), path)
}
