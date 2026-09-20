package tui

import (
	"encoding/json"
	"fmt"

	"lcroom/internal/control"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) executeTaskResultControl(inv control.Invocation) controlInvocationOutcome {
	return controlInvocationOutcome{model: m, cmd: func() tea.Msg {
		ctx, cancel := m.actionContext(tuiProjectActionTimeout)
		defer cancel()
		msg := bossAgentTaskClosedMsg{}
		if m.svc == nil {
			msg.err = fmt.Errorf("service unavailable")
			return msg
		}
		actor := model.AgentTaskActor{Operator: true}
		if control.IsExternalOperationID(inv.RequestID) {
			op, err := m.svc.Store().GetControlOperation(ctx, inv.RequestID)
			if err != nil {
				msg.err = err
				return msg
			}
			actor = model.AgentTaskActor{ProjectPath: op.ProjectPath, Provider: model.NormalizeSessionSource(model.SessionSource(op.Provider)), SessionKey: op.SessionKey}
		}
		if inv.Capability == control.CapabilityAgentTaskSubmitResult {
			var input control.AgentTaskSubmitResultInput
			json.Unmarshal(inv.Args, &input)
			msg.task, msg.err = m.svc.Store().SubmitAgentTaskResult(ctx, actor, input)
		} else {
			var input control.AgentTaskReviewResultInput
			json.Unmarshal(inv.Args, &input)
			msg.task, msg.err = m.svc.Store().ReviewAgentTaskResult(ctx, actor, input)
		}
		if msg.err != nil {
			msg.status = msg.err.Error()
		} else {
			msg.status = "Task result: " + msg.task.Workflow.Phase
		}
		return msg
	}}
}
