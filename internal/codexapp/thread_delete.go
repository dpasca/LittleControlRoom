package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"lcroom/internal/codexcli"
	"lcroom/internal/codexstate"
)

const threadAdminScannerBuffer = 4 * 1024 * 1024
const threadAdminStderrLimit = 16 * 1024

var newThreadAdminCommand = func() *exec.Cmd {
	return exec.Command("codex", "app-server")
}

// DeleteThreads permanently removes persisted Codex root threads through the
// supported app-server thread/delete API. The returned ids completed before an
// error, which lets callers verify and report partial progress accurately.
func DeleteThreads(ctx context.Context, codexHome string, threadIDs []string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	threadIDs = uniqueNonEmptyStrings(threadIDs)
	if len(threadIDs) == 0 {
		return nil, nil
	}

	client, err := startThreadAdminClient(ctx, codexHome)
	if err != nil {
		return nil, err
	}
	defer client.close()

	deleted := make([]string, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		if _, err := client.call(ctx, "thread/delete", map[string]any{"threadId": threadID}); err != nil {
			return deleted, fmt.Errorf("delete Codex thread %s: %w", shortID(threadID), err)
		}
		deleted = append(deleted, threadID)
	}
	return deleted, nil
}

type threadAdminClient struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	messages chan rpcEnvelope
	exited   chan error
	done     chan struct{}

	writeMu   sync.Mutex
	closeOnce sync.Once
	nextID    int64

	stderrMu sync.Mutex
	stderr   strings.Builder
}

func startThreadAdminClient(ctx context.Context, codexHome string) (*threadAdminClient, error) {
	codexHome = filepath.Clean(strings.TrimSpace(codexstate.ResolveHomeRoot(codexHome)))
	if codexHome == "" || codexHome == "." {
		return nil, fmt.Errorf("Codex home is unavailable")
	}
	if info, err := os.Stat(codexHome); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		return nil, fmt.Errorf("inspect Codex home %s: %w", codexHome, err)
	}

	cmd := newThreadAdminCommand()
	if cmd == nil {
		return nil, fmt.Errorf("Codex app-server command is unavailable")
	}
	cmd.Dir = codexHome
	baseEnv := cmd.Env
	if len(baseEnv) == 0 {
		baseEnv = os.Environ()
	}
	cmd.Env = withEnvOverride(baseEnv, "CODEX_HOME", codexHome)
	configureAppServerCommand(cmd)
	codexcli.ApplyCodeModeHostCompatibility(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex app-server stdin: %w", err)
	}
	client := &threadAdminClient{
		cmd:      cmd,
		stdin:    stdin,
		messages: make(chan rpcEnvelope, 32),
		exited:   make(chan error, 1),
		done:     make(chan struct{}),
	}
	stdout, stderr, err := startWithOwnedOutputPipes(cmd)
	if err != nil {
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}
	var captured sync.WaitGroup
	captureProcessOutput(&captured, stdout, func(stream *os.File) { client.readMessages(stream) })
	captureProcessOutput(&captured, stderr, func(stream *os.File) { client.readStderr(stream) })
	go func() {
		err := cmd.Wait()
		// Report the exit only once stderr is drained, so withStderr can still
		// explain why the app-server died instead of returning a bare error.
		waitForOutputDrain(&captured, appServerOutputDrainTimeout)
		client.exited <- err
	}()

	if _, err := client.call(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "little_control_room",
			"title":   "Little Control Room",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}); err != nil {
		client.close()
		return nil, fmt.Errorf("initialize Codex app-server cleanup client: %w", err)
	}
	if err := client.send(rpcNotification{Method: "initialized", Params: map[string]any{}}); err != nil {
		client.close()
		return nil, fmt.Errorf("acknowledge Codex app-server initialization: %w", err)
	}
	return client, nil
}

func (c *threadAdminClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c == nil {
		return nil, fmt.Errorf("Codex app-server cleanup client is unavailable")
	}
	c.nextID++
	id := c.nextID
	if err := c.send(rpcRequest{Method: method, ID: id, Params: params}); err != nil {
		return nil, err
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-c.exited:
			if err == nil {
				err = io.EOF
			}
			return nil, c.withStderr(fmt.Errorf("Codex app-server exited: %w", err))
		case envelope, ok := <-c.messages:
			if !ok {
				return nil, c.withStderr(io.EOF)
			}
			if len(envelope.ID) == 0 {
				continue
			}
			if envelope.Method != "" {
				_ = c.send(rpcErrorResponse{
					ID: decodeRequestID(idKey(envelope.ID)),
					Error: rpcError{
						Code:    -32601,
						Message: "Little Control Room cleanup does not support server requests",
					},
				})
				continue
			}
			responseID, err := strconv.ParseInt(strings.TrimSpace(string(envelope.ID)), 10, 64)
			if err != nil || responseID != id {
				continue
			}
			if envelope.Error != nil {
				return nil, c.withStderr(errors.New(envelope.Error.Message))
			}
			return envelope.Result, nil
		}
	}
}

func (c *threadAdminClient) send(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.stdin == nil {
		return fmt.Errorf("Codex app-server stdin is unavailable")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(append(payload, '\n')); err != nil {
		return c.withStderr(err)
	}
	return nil
}

func (c *threadAdminClient) readMessages(reader io.Reader) {
	defer close(c.messages)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), threadAdminScannerBuffer)
	for scanner.Scan() {
		var envelope rpcEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			continue
		}
		select {
		case c.messages <- envelope:
		case <-c.done:
			return
		}
	}
}

func (c *threadAdminClient) readStderr(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		c.stderrMu.Lock()
		remaining := threadAdminStderrLimit - c.stderr.Len()
		if remaining > 0 {
			if c.stderr.Len() > 0 {
				c.stderr.WriteByte('\n')
				remaining--
			}
			if len(line) > remaining {
				line = line[:remaining]
			}
			c.stderr.WriteString(line)
		}
		c.stderrMu.Unlock()
	}
}

func (c *threadAdminClient) withStderr(err error) error {
	if err == nil || c == nil {
		return err
	}
	c.stderrMu.Lock()
	detail := strings.TrimSpace(c.stderr.String())
	c.stderrMu.Unlock()
	if detail == "" {
		return err
	}
	return fmt.Errorf("%w (%s)", err, detail)
}

func (c *threadAdminClient) close() {
	if c == nil || c.cmd == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.done)
		c.writeMu.Lock()
		stdin := c.stdin
		c.stdin = nil
		c.writeMu.Unlock()
		if stdin != nil {
			_ = stdin.Close()
		}
		_ = terminateAppServerCommand(c.cmd)
	})
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
