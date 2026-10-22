package model

import "testing"

func TestAgentTaskMatchesRemovedWorktree(t *testing.T) {
	for _, tc := range []struct {
		name string
		task AgentTask
		path string
		want bool
	}{
		{"own checkout", AgentTask{WorkspacePath: "/tasks/one"}, "/tasks/one", true},
		{"legacy checkout", AgentTask{WorkspacePath: "/tasks/one"}, "/tasks/one/repo", true},
		{"origin", AgentTask{WorkspacePath: "/tasks/one", OriginWorktreePath: "/repos/parent"}, "/repos/parent", true},
		{"contained workspace", AgentTask{WorkspacePath: "/tasks/one/child"}, "/tasks/one", true},
		{"isolated root", AgentTask{WorkspacePath: "/tasks/one", Repository: AgentTaskRepository{Isolated: true, Root: "/tasks/one/checkout"}}, "/tasks/one/checkout", true},
		{"shared lease", AgentTask{WorkspacePath: "/tasks/one", Repository: AgentTaskRepository{Root: "/repos/shared", Write: true}}, "/repos/shared", false},
		{"auxiliary repo", AgentTask{WorkspacePath: "/tasks/one", Repository: AgentTaskRepository{Isolated: true, Root: "/tasks/one"}}, "/tasks/one/repo", false},
		{"unrelated descendant", AgentTask{WorkspacePath: "/tasks/one"}, "/tasks/one/tools/repo", false},
		{"prefix sibling", AgentTask{WorkspacePath: "/tasks/one-more"}, "/tasks/one", false},
		{"empty", AgentTask{}, "", false},
		{"relative", AgentTask{WorkspacePath: "one"}, "one", false},
		{"filesystem root", AgentTask{WorkspacePath: "/tasks/one"}, "/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AgentTaskMatchesRemovedWorktree(tc.task, tc.path); got != tc.want {
				t.Fatalf("matches %s = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
