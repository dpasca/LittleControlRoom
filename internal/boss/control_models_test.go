package boss

import (
	"encoding/json"
	"lcroom/internal/control"
	"strings"
	"testing"
)

func TestTaskModelChoicesSurviveChatProposalAndConfirmation(t *testing.T) {
	for _, capability := range []control.CapabilityName{control.CapabilityAgentTaskCreate, control.CapabilityAgentTaskContinue} {
		action := bossAction{ControlCapability: string(capability), TaskTitle: "Worker", TaskKind: "agent", TaskID: "agt_worker", EngineerProvider: "lcagent", SessionMode: "resume_or_new", Prompt: "work", EngineerModelSelection: control.EngineerModelSelection{Model: "cheap", ModelProvider: "zai", ReasoningEffort: "low"}}
		inv, _, err := controlProposalFromBossAction(action)
		if err != nil {
			t.Fatal(err)
		}
		var selection control.EngineerModelSelection
		if err := json.Unmarshal(inv.Args, &selection); err != nil {
			t.Fatal(err)
		}
		if selection != action.EngineerModelSelection {
			t.Fatalf("Chat lost model choice: %#v", selection)
		}
		preview, err := controlConfirmationContent(inv)
		if err != nil || !strings.Contains(preview, "zai/cheap") || !strings.Contains(preview, "low") {
			t.Fatalf("confirmation hides model choice: %s / %v", preview, err)
		}
	}
}
