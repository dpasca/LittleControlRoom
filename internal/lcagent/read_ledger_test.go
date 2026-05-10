package lcagent

import (
	"strings"
	"testing"

	"lcroom/internal/lcagent/tools"
)

func TestReadLedgerMergesSuccessfulReadFileRanges(t *testing.T) {
	ledger := newReadLedger()
	for _, output := range []string{
		"file: a.go\ntotal_lines: 300\nhas_more: true\nnext_offset: 101\nlines: 1-100\n\n1 | package demo\n",
		"file: a.go\ntotal_lines: 300\nhas_more: true\nnext_offset: 201\nlines: 101-200\n\n101 | func A() {}\n",
		"file: b.go\ntotal_lines: 20\nhas_more: false\nlines: 10-20\n\n10 | func B() {}\n",
	} {
		if !ledger.ObserveReadResult(tools.ToolResult{Success: true, Output: output}) {
			t.Fatalf("ObserveReadResult(%q) = false, want true", output)
		}
	}

	files, ranges := ledger.Stats()
	if files != 2 || ranges != 2 {
		t.Fatalf("stats files=%d ranges=%d, want 2 files and 2 merged ranges", files, ranges)
	}
	text := ledger.Format(1000)
	for _, want := range []string{"- a.go: lines 1-200 of 300", "- b.go: lines 10-20 of 20"} {
		if !strings.Contains(text, want) {
			t.Fatalf("ledger missing %q:\n%s", want, text)
		}
	}
}

func TestReadLedgerIgnoresFailedOrUnparseableResults(t *testing.T) {
	ledger := newReadLedger()
	if ledger.ObserveReadResult(tools.ToolResult{Success: false, Output: "file: a.go\nlines: 1-2\n"}) {
		t.Fatal("failed result was recorded")
	}
	if ledger.ObserveReadResult(tools.ToolResult{Success: true, Output: "not a read_file result"}) {
		t.Fatal("unparseable result was recorded")
	}
	files, ranges := ledger.Stats()
	if files != 0 || ranges != 0 {
		t.Fatalf("stats files=%d ranges=%d, want empty ledger", files, ranges)
	}
}
