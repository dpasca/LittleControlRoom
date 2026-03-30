package todoworktree

import (
	"encoding/json"
	"testing"
)

func TestResultUnmarshalDecodesSchemaFieldNames(t *testing.T) {
	t.Parallel()

	var result Result
	err := json.Unmarshal([]byte(`{
		"branch_name": "feat/todo-worktree-launch",
		"worktree_suffix": "feat-todo-worktree-launch",
		"kind": "feature",
		"reason": "Implements the worktree launcher.",
		"confidence": 0.91
	}`), &result)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if result.BranchName != "feat/todo-worktree-launch" {
		t.Fatalf("BranchName = %q, want feat/todo-worktree-launch", result.BranchName)
	}
	if result.WorktreeSuffix != "feat-todo-worktree-launch" {
		t.Fatalf("WorktreeSuffix = %q, want feat-todo-worktree-launch", result.WorktreeSuffix)
	}
	if err := validateResult(&result); err != nil {
		t.Fatalf("validateResult() error = %v", err)
	}
}
