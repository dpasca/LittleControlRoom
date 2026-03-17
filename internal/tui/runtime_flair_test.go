package tui

import "testing"

func TestRuntimeFlairOperatorSceneStateWalksBetweenPanelAndDesk(t *testing.T) {
	left := runtimeFlairOperatorSceneState(41, 14, 22, 0, true)
	leftHold := runtimeFlairOperatorSceneState(41, 14, 22, 1, true)
	leftLateHold := runtimeFlairOperatorSceneState(41, 14, 22, 5, true)
	stepOne := runtimeFlairOperatorSceneState(41, 14, 22, 6, true)
	stepTwo := runtimeFlairOperatorSceneState(41, 14, 22, 8, true)
	stepThree := runtimeFlairOperatorSceneState(41, 14, 22, 10, true)
	desk := runtimeFlairOperatorSceneState(41, 14, 22, 12, true)
	deskHold := runtimeFlairOperatorSceneState(41, 14, 22, 16, true)
	deskLateHold := runtimeFlairOperatorSceneState(41, 14, 22, 21, true)
	returnStepThree := runtimeFlairOperatorSceneState(41, 14, 22, 22, true)
	returnStepTwo := runtimeFlairOperatorSceneState(41, 14, 22, 24, true)
	returnStepOne := runtimeFlairOperatorSceneState(41, 14, 22, 26, true)
	backAtPanel := runtimeFlairOperatorSceneState(41, 14, 22, 31, true)

	if left.pose != runtimeFlairOperatorInspect || left.facing != -1 {
		t.Fatalf("phase 0 = %+v, want inspect pose facing left", left)
	}
	if desk.pose != runtimeFlairOperatorTypeA || desk.facing != 1 {
		t.Fatalf("phase 12 = %+v, want typing pose facing right", desk)
	}
	if left.x != leftHold.x || left.x != leftLateHold.x {
		t.Fatalf("the operator should linger at the left terminal before walking: %d vs %d vs %d", left.x, leftHold.x, leftLateHold.x)
	}
	if desk.x != deskHold.x || desk.x != deskLateHold.x {
		t.Fatalf("the operator should linger at the desk terminal before returning: %d vs %d vs %d", desk.x, deskHold.x, deskLateHold.x)
	}
	if desk.y != deskHold.y || desk.y != deskLateHold.y || desk.pose != deskHold.pose || desk.pose != deskLateHold.pose {
		t.Fatalf("the operator should pause at the desk without bobbing: desk=%+v hold=%+v late=%+v", desk, deskHold, deskLateHold)
	}
	if !(left.x < stepOne.x && stepOne.x < stepTwo.x && stepTwo.x < stepThree.x && stepThree.x <= desk.x) {
		t.Fatalf("walk-right positions should progress toward the desk: left=%d step1=%d step2=%d step3=%d desk=%d", left.x, stepOne.x, stepTwo.x, stepThree.x, desk.x)
	}
	if !(returnStepThree.x < desk.x && returnStepTwo.x < returnStepThree.x && returnStepOne.x < returnStepTwo.x && backAtPanel.x <= returnStepOne.x) {
		t.Fatalf("walk-left positions should progress back toward the panel: desk=%d step3=%d step2=%d step1=%d panel=%d", desk.x, returnStepThree.x, returnStepTwo.x, returnStepOne.x, backAtPanel.x)
	}
}

func TestRuntimeFlairOperatorSceneStateStaysAtDeskWithoutPanel(t *testing.T) {
	typingA := runtimeFlairOperatorSceneState(30, 14, 18, 0, false)
	typingAHold := runtimeFlairOperatorSceneState(30, 14, 18, 6, false)
	typingB := runtimeFlairOperatorSceneState(30, 14, 18, 16, false)

	if typingA.x != typingAHold.x || typingA.x != typingB.x {
		t.Fatalf("narrow scenes without the side panel should keep the operator at the desk: %d vs %d vs %d", typingA.x, typingAHold.x, typingB.x)
	}
	if typingA.pose != runtimeFlairOperatorTypeA {
		t.Fatalf("phase 0 without a panel = %+v, want typing pose A", typingA)
	}
	if typingB.pose != runtimeFlairOperatorTypeB {
		t.Fatalf("phase 16 without a panel = %+v, want typing pose B", typingB)
	}
}
