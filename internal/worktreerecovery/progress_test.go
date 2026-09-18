package worktreerecovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProgressIsThrottledAndReportsFileBytes(t *testing.T) {
	var updates []Progress
	ctx := WithProgress(context.Background(), func(p Progress) { updates = append(updates, p) })
	for n := 0; n < 1000; n++ {
		reportProgress(ctx, "Reading and verifying files", "file", 1, 0)
	}
	if len(updates) != 1 {
		t.Fatalf("flooded UI: %d", len(updates))
	}
	p := ctx.Value(progressKey{}).(*progressReporter)
	p.last = time.Now().Add(-time.Second)
	reader := progressReader{ctx: ctx, reader: strings.NewReader("contents"), stage: "Reading and verifying files", path: "file"}
	if _, err := reader.Read(make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 || updates[1].Bytes != 8 || updates[1].Entries != 1000 {
		t.Fatalf("missing counters: %#v", updates)
	}
	reportProgress(ctx, "Checking Git: fsck", "repo", 1, 0)
	if len(updates) != 2 {
		t.Fatal("rapid phase changes bypassed throttling")
	}
	p.last = time.Now().Add(-time.Second)
	reportProgress(ctx, "Checking Git: fsck", "repo", 1, 0)
	if len(updates) != 3 || updates[2].Bytes != 0 || updates[2].Stage != "Checking Git: fsck" {
		t.Fatal("phase transition lost its counters")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	reader.ctx = canceled
	if _, err := reader.Read(make([]byte, 32)); !errors.Is(err, context.Canceled) {
		t.Fatal("read ignored cancellation")
	}
}
