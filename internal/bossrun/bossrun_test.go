package bossrun

import (
	"strings"
	"testing"

	"lcroom/internal/control"
)

func TestNormalizeGoalProposalDefaultsAgentTaskCleanupAuthority(t *testing.T) {
	t.Parallel()

	proposal, err := NormalizeGoalProposal(GoalProposal{
		Run: GoalRun{
			Kind:      GoalKindAgentTaskCleanup,
			Title:     "Clear stale agents",
			Objective: "Remove stale delegated agents from the active record.",
		},
		ArchiveResources: []control.ResourceRef{
			{Kind: control.ResourceAgentTask, ID: "agt_one", Label: "old check"},
			{Kind: control.ResourceAgentTask, ID: "agt_two", Label: "old follow-up"},
		},
	})
	if err != nil {
		t.Fatalf("NormalizeGoalProposal() error = %v", err)
	}
	if proposal.Authority.MaxRisk != control.RiskWrite {
		t.Fatalf("max risk = %q, want write", proposal.Authority.MaxRisk)
	}
	if len(proposal.Authority.AllowedCapabilities) != 1 || proposal.Authority.AllowedCapabilities[0] != control.CapabilityAgentTaskClose {
		t.Fatalf("allowed capabilities = %#v, want agent_task.close", proposal.Authority.AllowedCapabilities)
	}
	if ids := AgentTaskResourceIDs(proposal.Authority.Resources); len(ids) != 2 || ids[0] != "agt_one" || ids[1] != "agt_two" {
		t.Fatalf("resource ids = %#v, want selected tasks", ids)
	}
	if len(proposal.Plan.Steps) < 3 {
		t.Fatalf("plan steps = %#v, want default executable plan", proposal.Plan.Steps)
	}
	if !strings.Contains(proposal.Preview, "Archive 2 delegated agent task records?") ||
		!strings.Contains(proposal.Preview, "delete files or workspaces") {
		t.Fatalf("preview = %q, want scoped cleanup preview", proposal.Preview)
	}
}

func TestNormalizeGoalProposalRejectsCleanupWithoutAgentTaskResources(t *testing.T) {
	t.Parallel()

	_, err := NormalizeGoalProposal(GoalProposal{
		Run: GoalRun{Kind: GoalKindAgentTaskCleanup},
		Authority: AuthorityGrant{
			Resources: []control.ResourceRef{{Kind: control.ResourceProject, ProjectPath: "/tmp/app"}},
		},
	})
	if err == nil {
		t.Fatalf("NormalizeGoalProposal() err = nil, want missing agent task resource error")
	}
}

func TestNormalizeGoalProposalRejectsReadOnlyCleanupAuthority(t *testing.T) {
	t.Parallel()

	_, err := NormalizeGoalProposal(GoalProposal{
		Run: GoalRun{Kind: GoalKindAgentTaskCleanup},
		Authority: AuthorityGrant{
			MaxRisk:   control.RiskRead,
			Resources: []control.ResourceRef{{Kind: control.ResourceAgentTask, ID: "agt_one"}},
		},
	})
	if err == nil {
		t.Fatalf("NormalizeGoalProposal() err = nil, want write-risk authority error")
	}
}

func TestFormatGoalResultSummarizesVerifiedArchive(t *testing.T) {
	t.Parallel()

	got := FormatGoalResult(GoalResult{
		ArchivedTaskIDs: []string{"agt_one", "agt_two"},
		Verified:        true,
	})
	if !strings.Contains(got, "Archived 2 delegated agent task records") ||
		!strings.Contains(got, "verified") {
		t.Fatalf("FormatGoalResult() = %q, want archive and verification summary", got)
	}
}
