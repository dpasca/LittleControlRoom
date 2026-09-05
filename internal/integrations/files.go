package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
	"github.com/tailscale/hujson"
)

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds size limit")
	}
	return data, nil
}

// edit changes a single native configuration subtree and retains an exact
// backup. Revalidate the source immediately before the atomic replacement.
func (s *scan) edit(path string, keys []string, value any, remove bool) (string, error) {
	doc := s.readDocument(path)
	if doc.err != nil {
		return "", doc.err
	}
	var updated []byte
	var err error
	if strings.HasSuffix(path, ".toml") {
		updated, err = patchTOML(doc.raw, keys, value, remove)
	} else {
		updated, err = patchJSON(doc.raw, keys, value, remove)
	}
	if err != nil {
		return "", err
	}
	return replaceFile(path, doc.raw, updated)
}

func patchJSON(raw []byte, keys []string, value any, remove bool) ([]byte, error) {
	raw = append([]byte(nil), raw...)
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}\n")
	}
	root, err := hujson.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid JSON configuration")
	}
	pointer := ""
	for i, key := range keys {
		pointer += "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
		if i < len(keys)-1 && root.Find(pointer) != nil {
			continue
		}
		op := "add"
		v := value
		if i < len(keys)-1 {
			v = map[string]any{}
		} else if remove {
			if root.Find(pointer) == nil {
				return raw, nil
			}
			op = "remove"
		}
		patch, _ := json.Marshal([]map[string]any{{"op": op, "path": pointer, "value": v}})
		if err := root.Patch(patch); err != nil {
			return nil, fmt.Errorf("cannot edit JSON configuration path")
		}
	}
	root.Format()
	return root.Pack(), nil
}

// TOML's parser, not textual matching, identifies the top-level group being
// edited. Other groups retain their original bytes and formatting. Verify the
// complete resulting value tree before writing, including unusual dotted keys.
func patchTOML(raw []byte, keys []string, value any, remove bool) ([]byte, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("configuration key required")
	}
	expected := map[string]any{}
	if err := toml.Unmarshal(raw, &expected); err != nil {
		return nil, fmt.Errorf("invalid TOML configuration")
	}
	node := expected
	for _, key := range keys[:len(keys)-1] {
		child, ok := node[key].(map[string]any)
		if !ok {
			if node[key] != nil {
				return nil, fmt.Errorf("configuration key is not a table")
			}
			child = map[string]any{}
			node[key] = child
		}
		node = child
	}
	if remove {
		delete(node, keys[len(keys)-1])
	} else {
		node[keys[len(keys)-1]] = value
	}
	type expression struct {
		start int
		group string
	}
	expressions := []expression{}
	parser := unstable.Parser{}
	parser.Reset(raw)
	group := ""
	for parser.NextExpression() {
		n := parser.Expression()
		if n.Kind != unstable.Table && n.Kind != unstable.ArrayTable && n.Kind != unstable.KeyValue {
			continue
		}
		it := n.Key()
		if !it.Next() {
			continue
		}
		key := it.Node()
		start := int(key.Raw.Offset)
		for start > 0 && raw[start-1] != '\n' {
			start--
		}
		if n.Kind == unstable.Table || n.Kind == unstable.ArrayTable {
			group = string(key.Data)
		}
		exprGroup := group
		if exprGroup == "" {
			exprGroup = string(key.Data)
		}
		expressions = append(expressions, expression{start, exprGroup})
	}
	if parser.Error() != nil {
		return nil, fmt.Errorf("cannot parse TOML configuration")
	}
	var out bytes.Buffer
	if len(expressions) > 0 {
		out.Write(raw[:expressions[0].start])
	} else {
		out.Write(raw)
	}
	for i, expr := range expressions {
		end := len(raw)
		if i+1 < len(expressions) {
			end = expressions[i+1].start
		}
		if expr.group != keys[0] {
			out.Write(raw[expr.start:end])
		}
	}
	part, err := toml.Marshal(map[string]any{keys[0]: expected[keys[0]]})
	if err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	out.Write(part)
	actual := map[string]any{}
	if err := toml.Unmarshal(out.Bytes(), &actual); err != nil {
		return nil, fmt.Errorf("this TOML layout needs native editing; original configuration was preserved")
	}
	// Normalize caller values (e.g. []string) through TOML before comparison.
	canonical, _ := toml.Marshal(expected)
	normalized := map[string]any{}
	_ = toml.Unmarshal(canonical, &normalized)
	if !reflect.DeepEqual(actual, normalized) {
		return nil, fmt.Errorf("TOML edit would affect unrelated settings; original configuration was preserved")
	}
	return out.Bytes(), nil
}

func replaceFile(path string, original, updated []byte) (string, error) {
	if bytes.Equal(original, updated) {
		return "", nil
	}
	// Preserve symlinks, including the per-launch CODEX_HOME overlay.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	} else {
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("configuration symlink target is missing or inaccessible; repair it before editing")
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
	current, err := readConfigBytes(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if !bytes.Equal(current, original) {
		return "", fmt.Errorf("configuration changed while preparing the edit; refresh and retry")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm() & 0o600
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".lcr-integration-*")
	if err != nil {
		return "", err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(updated)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	backup := ""
	if len(original) > 0 {
		// A fresh exclusive file cannot overwrite an existing recovery copy or
		// follow an attacker-controlled backup symlink.
		copy, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".lcr-*.bak")
		if err != nil {
			return "", err
		}
		backup = copy.Name()
		_, writeErr := copy.Write(original)
		if writeErr == nil {
			writeErr = copy.Sync()
		}
		closeErr := copy.Close()
		if writeErr != nil {
			return "", writeErr
		}
		if closeErr != nil {
			return "", closeErr
		}
	}
	current, err = readConfigBytes(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if !bytes.Equal(current, original) {
		return "", fmt.Errorf("configuration changed before saving; original file was preserved")
	}
	if err := os.Rename(temp, path); err != nil {
		return "", err
	}
	return backup, nil
}

func readConfigBytes(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readBounded(file, 4<<20)
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
