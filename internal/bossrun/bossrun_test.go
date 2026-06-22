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

func TestNormalizeGoalProposalDefaultsLCAgentTaskAuthority(t *testing.T) {
	t.Parallel()

	proposal, err := NormalizeGoalProposal(GoalProposal{
		Run: GoalRun{
			Kind:      GoalKindLCAgentTask,
			Title:     "Verify the release diff",
			Objective: "Use LCAgent to inspect the release diff and report whether tests cover it.",
		},
		Authority: AuthorityGrant{
			Resources: []control.ResourceRef{{Kind: control.ResourceProject, ProjectPath: "/tmp/app", Label: "app"}},
		},
	})
	if err != nil {
		t.Fatalf("NormalizeGoalProposal() error = %v", err)
	}
	if proposal.Authority.MaxRisk != control.RiskExternal {
		t.Fatalf("max risk = %q, want external", proposal.Authority.MaxRisk)
	}
	if len(proposal.Authority.AllowedCapabilities) != 1 || proposal.Authority.AllowedCapabilities[0] != control.CapabilityAgentTaskCreate {
		t.Fatalf("allowed capabilities = %#v, want agent_task.create", proposal.Authority.AllowedCapabilities)
	}
	if len(proposal.Plan.Steps) != 5 || proposal.Plan.Steps[0].ID != "create-lcagent-task" || proposal.Plan.Steps[2].Kind != PlanStepAwait {
		t.Fatalf("plan steps = %#v, want LCAgent task default plan", proposal.Plan.Steps)
	}
	if !strings.Contains(proposal.Preview, "Start a scoped LCAgent goal task?") ||
		!strings.Contains(proposal.Preview, "Verify the release diff") ||
		!strings.Contains(proposal.Preview, "Forbidden side effects") {
		t.Fatalf("preview = %q, want scoped LCAgent preview", proposal.Preview)
	}
}

func TestNormalizeGoalProposalRejectsLCAgentTaskWithoutExternalAuthority(t *testing.T) {
	t.Parallel()

	_, err := NormalizeGoalProposal(GoalProposal{
		Run: GoalRun{Kind: GoalKindLCAgentTask},
		Authority: AuthorityGrant{
			MaxRisk:             control.RiskWrite,
			AllowedCapabilities: []control.CapabilityName{control.CapabilityAgentTaskCreate},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "external model execution") {
		t.Fatalf("NormalizeGoalProposal() err = %v, want external-risk authority error", err)
	}
}

func TestNormalizeGoalProposalRejectsPlanCapabilityOutsideAuthority(t *testing.T) {
	t.Parallel()

	_, err := NormalizeGoalProposal(GoalProposal{
		Run: GoalRun{Kind: GoalKindAgentTaskCleanup},
		Authority: AuthorityGrant{
			AllowedCapabilities: []control.CapabilityName{control.CapabilityAgentTaskClose},
			Resources:           []control.ResourceRef{{Kind: control.ResourceAgentTask, ID: "agt_one"}},
			MaxRisk:             control.RiskWrite,
		},
		Plan: Plan{
			Version: 1,
			Steps: []PlanStep{{
				ID:         "delegate-extra-work",
				Kind:       PlanStepDelegate,
				Capability: control.CapabilityEngineerSendPrompt,
				Resources:  []control.ResourceRef{{Kind: control.ResourceAgentTask, ID: "agt_one"}},
			}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "capability outside authority") {
		t.Fatalf("NormalizeGoalProposal() err = %v, want capability authority error", err)
	}
}

func TestNormalizeGoalProposalRejectsPlanResourceOutsideAuthority(t *testing.T) {
	t.Parallel()

	_, err := NormalizeGoalProposal(GoalProposal{
		Run: GoalRun{Kind: GoalKindAgentTaskCleanup},
		Authority: AuthorityGrant{
			AllowedCapabilities: []control.CapabilityName{control.CapabilityAgentTaskClose},
			Resources:           []control.ResourceRef{{Kind: control.ResourceAgentTask, ID: "agt_one"}},
			MaxRisk:             control.RiskWrite,
		},
		Plan: Plan{
			Version: 1,
			Steps: []PlanStep{{
				ID:         "archive-unapproved-task",
				Kind:       PlanStepAct,
				Capability: control.CapabilityAgentTaskClose,
				Resources:  []control.ResourceRef{{Kind: control.ResourceAgentTask, ID: "agt_two"}},
			}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "resource outside authority") {
		t.Fatalf("NormalizeGoalProposal() err = %v, want resource authority error", err)
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

func TestFormatGoalResultSummarizesVerifiedLCAgentTask(t *testing.T) {
	t.Parallel()

	got := FormatGoalResult(GoalResult{
		Kind:                      GoalKindLCAgentTask,
		CreatedTaskIDs:            []string{"agt_lca"},
		Verified:                  true,
		LCAgentVerificationStatus: "reported",
		LCAgentTraceQuality:       "verification reported; actual checks: go test ./... passed; tokens: 150",
	})
	if !strings.Contains(got, "Started 1 LCAgent goal task") ||
		!strings.Contains(got, "completed with reported verification") ||
		!strings.Contains(got, "actual checks: go test ./... passed") {
		t.Fatalf("FormatGoalResult() = %q, want LCAgent outcome summary", got)
	}
}

func TestFormatGoalResultLimitsLongLCAgentTraceQuality(t *testing.T) {
	t.Parallel()

	command := longGoalResultCompileCommandForTest()
	got := FormatGoalResult(GoalResult{
		Kind:                      GoalKindLCAgentTask,
		CreatedTaskIDs:            []string{"agt_lca"},
		Verified:                  true,
		LCAgentVerificationStatus: "verified",
		LCAgentTraceQuality:       "verification verified; actual checks: " + command + " failed; " + command + " passed; tokens: 150",
	})
	if strings.Contains(got, command) {
		t.Fatalf("FormatGoalResult() contains full compile command:\n%s", got)
	}
	if !strings.Contains(got, "Trace:") || !strings.Contains(got, "...") {
		t.Fatalf("FormatGoalResult() = %q, want clipped trace quality", got)
	}
	max := 180 + lcagentGoalResultTraceMaxChars
	if len([]rune(got)) > max {
		t.Fatalf("FormatGoalResult() length = %d, want <= %d:\n%s", len([]rune(got)), max, got)
	}
}

func TestFormatGoalResultLimitsLongLCAgentActualChecks(t *testing.T) {
	t.Parallel()

	command := longGoalResultCompileCommandForTest()
	got := FormatGoalResult(GoalResult{
		Kind:                      GoalKindLCAgentTask,
		CreatedTaskIDs:            []string{"agt_lca"},
		Verified:                  true,
		LCAgentVerificationStatus: "verified",
		LCAgentActualChecks: []string{
			command + " in /Users/davide/LittleControlRoom/tasks/2026-06-20-new-task-13-05-06 failed",
			command + " in /Users/davide/LittleControlRoom/tasks/2026-06-20-new-task-13-05-06 passed",
		},
	})
	if strings.Contains(got, command) {
		t.Fatalf("FormatGoalResult() contains full compile command:\n%s", got)
	}
	if !strings.Contains(got, "actual checks:") || !strings.Contains(got, "...") {
		t.Fatalf("FormatGoalResult() = %q, want clipped actual checks", got)
	}
}

func longGoalResultCompileCommandForTest() string {
	return "clang++ -std=c++17 -O2 -Wall -Wno-deprecated-declarations -o skate main.cpp " +
		"-I/opt/homebrew/Cellar/glfw/3.4/include " +
		"-I/opt/homebrew/Cellar/glew/2.2.0_1/include " +
		"-I/opt/homebrew/Cellar/glm/1.0.2/include " +
		"-L/opt/homebrew/Cellar/glfw/3.4/lib " +
		"-L/opt/homebrew/Cellar/glew/2.2.0_1/lib " +
		"-lglfw -lGLEW -framework OpenGL -framework Cocoa -framework IOKit"
}
