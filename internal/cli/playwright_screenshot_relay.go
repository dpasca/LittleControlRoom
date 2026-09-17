package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// The relay preserves MCP messages byte-for-byte, including image payloads and
// server-initiated requests. Only the exact screenshot tool gets a visibility
// lease, released before its response reaches the agent.
type playwrightScreenshotRelay struct {
	output   io.Writer
	log      io.Writer
	begin    func() (func() error, error)
	mu       sync.Mutex
	pending  map[string]func() error
	closed   bool
	outputMu sync.Mutex
	buffer   bytes.Buffer // written only by the child stdout copier
	scanFrom int
}

type playwrightRelayMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name string `json:"name"`
	} `json:"params"`
}

func playwrightRelayID(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return "string:" + text
	}
	return "number:" + string(raw)
}

func (r *playwrightScreenshotRelay) forwardInput(input io.Reader, child io.WriteCloser) error {
	defer child.Close()
	reader := bufio.NewReader(input)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var message playwrightRelayMessage
			if json.Unmarshal(line, &message) == nil && message.Method == "tools/call" &&
				message.Params.Name == "browser_take_screenshot" && len(message.ID) > 0 {
				finish, err := r.begin()
				if err != nil {
					response, _ := json.Marshal(map[string]any{
						"jsonrpc": "2.0", "id": message.ID,
						"result": map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": err.Error()}}},
					})
					if err := r.writeOutput(append(response, '\n')); err != nil {
						return err
					}
					continue
				}
				r.mu.Lock()
				if r.closed {
					r.mu.Unlock()
					r.release(finish)
					return io.ErrClosedPipe
				}
				if r.pending == nil {
					r.pending = make(map[string]func() error)
				}
				id := playwrightRelayID(message.ID)
				previous := r.pending[id]
				r.pending[id] = finish
				r.mu.Unlock()
				if previous != nil {
					r.release(previous)
				}
			}
			if _, err := child.Write(line); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}

func (r *playwrightScreenshotRelay) Write(data []byte) (int, error) {
	_, _ = r.buffer.Write(data)
	for {
		index := bytes.IndexByte(r.buffer.Bytes()[r.scanFrom:], '\n')
		if index < 0 {
			r.scanFrom = r.buffer.Len()
			break
		}
		index += r.scanFrom
		line := r.buffer.Next(index + 1)
		r.scanFrom = 0
		var message playwrightRelayMessage
		if json.Unmarshal(line, &message) == nil && message.Method == "" && len(message.ID) > 0 {
			r.mu.Lock()
			id := playwrightRelayID(message.ID)
			finish := r.pending[id]
			delete(r.pending, id)
			r.mu.Unlock()
			if finish != nil {
				r.release(finish)
			}
		}
		if err := r.writeOutput(line); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (r *playwrightScreenshotRelay) writeOutput(data []byte) error {
	r.outputMu.Lock()
	defer r.outputMu.Unlock()
	_, err := r.output.Write(data)
	return err
}

func (r *playwrightScreenshotRelay) release(finish func() error) {
	if err := finish(); err != nil {
		fmt.Fprintf(r.log, "playwright-mcp screenshot restore: %v\n", err)
	}
}

func (r *playwrightScreenshotRelay) close() {
	r.mu.Lock()
	r.closed = true
	pending := r.pending
	r.pending = nil
	r.mu.Unlock()
	for _, finish := range pending {
		r.release(finish)
	}
}
