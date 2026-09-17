package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type relayWriteCloser struct{ bytes.Buffer }

func (*relayWriteCloser) Close() error { return nil }

func TestScreenshotRelayPreservesProtocolAndRestoresBeforeResponse(t *testing.T) {
	var output, logs bytes.Buffer
	child := &relayWriteCloser{}
	active := 0
	r := &playwrightScreenshotRelay{output: &output, log: &logs, begin: func() (func() error, error) {
		active++
		return func() error {
			if output.Len() != 0 {
				t.Fatal("response forwarded before restoring browser")
			}
			active--
			return nil
		}, nil
	}}
	defer r.close()
	input := "{\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"browser_snapshot\"}}\n" +
		"{\"id\":\"shot\",\"method\":\"tools/call\",\"params\":{\"name\":\"browser_take_screenshot\",\"arguments\":{\"filename\":\"shot.png\"}}}\n"
	if err := r.forwardInput(strings.NewReader(input), child); err != nil {
		t.Fatal(err)
	}
	if child.String() != input || active != 1 {
		t.Fatal("input changed or wrong tools acquired leases")
	}
	// A large image response, fragmented across writes, must not hit Scanner's
	// 64 KiB limit or be mistaken for a server request sharing a client ID.
	response := "{\"id\":\"shot\",\"result\":{\"content\":[{\"type\":\"image\",\"data\":\"" + strings.Repeat("A", 200000) + "\"}]}}\n"
	for start := 0; start < len(response); start += 137 {
		if _, err := r.Write([]byte(response[start:min(start+137, len(response))])); err != nil {
			t.Fatal(err)
		}
	}
	if output.String() != response || active != 0 {
		t.Fatal("response changed or lease leaked")
	}
}

func TestScreenshotRelayCleanupOnErrorAndExit(t *testing.T) {
	for _, response := range []string{"", "{\"id\":2,\"error\":{\"code\":-32603,\"message\":\"failed\"}}\n", "{\"id\":2,\"result\":{\"isError\":true}}\n"} {
		t.Run(response, func(t *testing.T) {
			releases := 0
			r := &playwrightScreenshotRelay{output: io.Discard, log: io.Discard, begin: func() (func() error, error) {
				return func() error { releases++; return nil }, nil
			}}
			if err := r.forwardInput(strings.NewReader("{\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"browser_take_screenshot\"}}\n"), &relayWriteCloser{}); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Write([]byte(response)); err != nil {
				t.Fatal(err)
			}
			r.close()
			r.close()
			if releases != 1 {
				t.Fatalf("releases = %d", releases)
			}
		})
	}
}

func TestScreenshotRelayPreparationFailureIsToolError(t *testing.T) {
	var output bytes.Buffer
	child := &relayWriteCloser{}
	r := &playwrightScreenshotRelay{output: &output, log: io.Discard, begin: func() (func() error, error) { return nil, errors.New("cannot unhide browser") }}
	if err := r.forwardInput(strings.NewReader("{\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"browser_take_screenshot\"}}\n"), child); err != nil {
		t.Fatal(err)
	}
	if child.Len() != 0 || !strings.Contains(output.String(), "cannot unhide browser") || !strings.Contains(output.String(), "\"isError\":true") {
		t.Fatalf("preparation error lost: %s", output.String())
	}
}

func TestScreenshotRelayDoesNotReleaseOnServerRequest(t *testing.T) {
	releases := 0
	r := &playwrightScreenshotRelay{output: io.Discard, log: io.Discard, pending: map[string]func() error{"number:4": func() error { releases++; return nil }}}
	if _, err := r.Write([]byte("{\"id\":4,\"method\":\"sampling/createMessage\",\"params\":{}}\n")); err != nil {
		t.Fatal(err)
	}
	if releases != 0 {
		t.Fatal("server request consumed client capture")
	}
	r.close()
}

func TestScreenshotRelayMatchesEscapedStringID(t *testing.T) {
	releases := 0
	r := &playwrightScreenshotRelay{output: io.Discard, log: io.Discard, begin: func() (func() error, error) {
		return func() error { releases++; return nil }, nil
	}}
	defer r.close()
	input := `{"id":"\u0073hot","method":"tools/call","params":{"name":"browser_take_screenshot"}}` + "\n"
	if err := r.forwardInput(strings.NewReader(input), &relayWriteCloser{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]byte("{\"id\":\"shot\",\"result\":{}}\n")); err != nil {
		t.Fatal(err)
	}
	if releases != 1 {
		t.Fatal("escaped string ID leaked capture")
	}
}
