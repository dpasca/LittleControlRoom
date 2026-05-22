package codexapp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

func forEachLCAgentJSONLEvent(reader io.Reader, fn func(map[string]json.RawMessage)) error {
	buffered := bufio.NewReader(reader)
	for {
		line, readErr := buffered.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var event map[string]json.RawMessage
			if err := json.Unmarshal(line, &event); err == nil {
				fn(event)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
