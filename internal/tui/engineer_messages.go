package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	engineerMessageStoreTimeout  = 3 * time.Second
	engineerMessagePollEveryTick = int(time.Second / spinnerTickInterval)
	engineerMessagePollLimit     = 32
	engineerMessageRetryBase     = time.Second
	engineerMessageRetryMax      = 30 * time.Second
)

type engineerMessageQueuedMsg struct {
	message control.EngineerMessage
	err     error
}

type engineerMessagesLoadedMsg struct {
	messages       []control.EngineerMessage
	recovered      bool
	afterCreatedAt time.Time
	afterID        string
	err            error
}

type engineerMessageClaimedMsg struct {
	messageID string
	message   control.EngineerMessage
	claimed   bool
	err       error
}

type engineerMessageDeliveryRecordedMsg struct {
	message         control.EngineerMessage
	desiredState    control.EngineerMessageState
	targetSessionID string
	status          string
	stateErr        error
	recordErr       error
	retryAttempt    int
}

type engineerMessageReceiptRetryMsg struct {
	message         control.EngineerMessage
	desiredState    control.EngineerMessageState
	targetSessionID string
	status          string
	stateErr        error
	retryAttempt    int
}

func (m Model) createEngineerMessageCmd(inv control.Invocation, input control.EngineerSendPromptInput, project model.ProjectSummary, provider codexapp.Provider, prompt string) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	targetSessionID := strings.TrimSpace(input.TargetSessionID)
	if targetSessionID == "" && input.SessionMode == control.SessionModeResumeOrNew {
		targetSessionID = m.selectedProjectSessionID(project, provider)
	}
	message := control.EngineerMessage{
		EngineerModelSelection: input.EngineerModelSelection,
		OperationID:            strings.TrimSpace(inv.RequestID),
		ProjectPath:            strings.TrimSpace(project.Path),
		Provider:               controlProviderFromCodexProvider(provider),
		SessionMode:            input.SessionMode.Normalized(),
		TargetSessionID:        targetSessionID,
		Prompt:                 strings.TrimSpace(prompt),
		Reveal:                 input.Reveal,
		TodoID:                 input.TodoID,
		TodoLabel:              input.TodoLabel,
		TodoText:               input.TodoText,
		State:                  control.EngineerMessageQueued,
	}
	return func() tea.Msg {
		if svc == nil || svc.Store() == nil {
			return engineerMessageQueuedMsg{err: errors.New("service store unavailable for durable engineer messaging")}
		}
		ctx, cancel := context.WithTimeout(parent, engineerMessageStoreTimeout)
		defer cancel()
		created, err := svc.Store().CreateEngineerMessage(ctx, message)
		return engineerMessageQueuedMsg{message: created, err: err}
	}
}

func (m Model) loadEngineerMessagesCmd(recoverDeliveries bool) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	afterCreatedAt := m.engineerMessagesCursorAt
	afterID := m.engineerMessagesCursorID
	if recoverDeliveries {
		afterCreatedAt = time.Time{}
		afterID = ""
	}
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		if svc == nil || svc.Store() == nil {
			return engineerMessagesLoadedMsg{}
		}
		ctx, cancel := context.WithTimeout(parent, engineerMessageStoreTimeout)
		defer cancel()
		if recoverDeliveries {
			if err := svc.Store().RequeueDeliveringEngineerMessages(ctx); err != nil {
				return engineerMessagesLoadedMsg{err: err}
			}
		}
		messages, err := svc.Store().ListQueuedEngineerMessagesAfter(ctx, afterCreatedAt, afterID, engineerMessagePollLimit)
		return engineerMessagesLoadedMsg{
			messages:       messages,
			recovered:      recoverDeliveries,
			afterCreatedAt: afterCreatedAt,
			afterID:        afterID,
			err:            err,
		}
	}
}

func (m *Model) requestEngineerMessagesPollCmd() tea.Cmd {
	if m == nil || m.engineerMessagesPollInFlight || m.svc == nil || m.svc.Store() == nil {
		return nil
	}
	m.engineerMessagesPollInFlight = true
	return m.loadEngineerMessagesCmd(!m.engineerMessagesRecovered)
}

func (m Model) applyEngineerMessageQueued(msg engineerMessageQueuedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.appendBackgroundErrorLogEntry("Engineer message queue failed", msg.err, msg.message.ProjectPath)
		m.status = "Engineer message could not be queued: " + msg.err.Error()
		return m, nil
	}
	if m.engineerMessageDeliveries == nil {
		m.engineerMessageDeliveries = make(map[string]struct{})
	}
	m.status = engineerMessageStatus(msg.message)
	if msg.message.State.Terminal() {
		return m, nil
	}
	return m, m.requestEngineerMessagesPollCmd()
}

func engineerMessageStatus(message control.EngineerMessage) string {
	target := projectTitle(message.ProjectPath, "")
	provider := message.Provider.Label()
	if target == "" {
		target = message.ProjectPath
	}
	if provider == "" {
		provider = "engineer"
	}
	verb := "queued for"
	switch message.State {
	case control.EngineerMessageDelivered:
		verb = "delivered to"
	case control.EngineerMessageFailed:
		verb = "failed for"
	}
	status := fmt.Sprintf("Message %s %s the %s engineer on %s", shortID(message.ID), verb, provider, target)
	if message.TodoID > 0 {
		status += fmt.Sprintf(" for TODO #%d", message.TodoID)
		if label := strings.TrimSpace(firstNonEmptyTrimmed(message.TodoLabel, message.TodoText)); label != "" {
			status += " " + compactEngineerNoticeText(label, 48)
		}
	}
	return status + "."
}

type engineerMessageDisposition struct {
	project       model.ProjectSummary
	bindSessionID string
	deliver       bool
	wait          bool
	failure       error
}

func (m Model) engineerMessageDisposition(message control.EngineerMessage) engineerMessageDisposition {
	project, err := m.resolveControlProjectRef(message.ProjectPath, "")
	if err != nil {
		return engineerMessageDisposition{failure: err}
	}
	if !project.PresentOnDisk {
		return engineerMessageDisposition{project: project, failure: fmt.Errorf("%s delivery requires a folder present on disk", message.Provider.Label())}
	}
	provider := codexProviderFromControlProvider(message.Provider)
	if provider == "" {
		return engineerMessageDisposition{project: project, failure: errors.New("engineer message provider is unavailable")}
	}
	forceNew := message.SessionMode == control.SessionModeNew
	if block, blocked := m.embeddedLaunchBlock(project, provider, forceNew); blocked {
		return engineerMessageDisposition{project: project, wait: true, failure: errors.New(block.Message)}
	}
	if forceNew {
		if _, blocked := m.controlFreshSessionBlockedByActiveEngineerTurn(project, provider, "this message"); blocked {
			return engineerMessageDisposition{project: project, wait: true}
		}
		return engineerMessageDisposition{project: project, deliver: true}
	}
	targetSessionID := strings.TrimSpace(message.TargetSessionID)
	snapshot, live := m.liveEmbeddedSnapshotForProject(project.Path, provider)
	if !live {
		return engineerMessageDisposition{project: project, deliver: true, bindSessionID: targetSessionID}
	}
	liveSessionID := strings.TrimSpace(snapshot.ThreadID)
	if targetSessionID == "" {
		targetSessionID = liveSessionID
	}
	if targetSessionID != "" && liveSessionID != "" && targetSessionID != liveSessionID && !m.embeddedSessionMatchesResumeID(project.Path, targetSessionID) {
		return engineerMessageDisposition{
			project: project,
			failure: fmt.Errorf("%w: expected %s session %s, found %s", codexapp.ErrSessionChanged, provider.Label(), targetSessionID, liveSessionID),
		}
	}
	if embeddedSessionBlocksProviderSwitch(snapshot) {
		if message.Model == "" && provider == codexapp.ProviderCodex && controlPromptCanSteerActiveEmbeddedSession(snapshot) {
			return engineerMessageDisposition{project: project, deliver: true, bindSessionID: targetSessionID}
		}
		return engineerMessageDisposition{project: project, wait: true, bindSessionID: targetSessionID}
	}
	return engineerMessageDisposition{project: project, deliver: true, bindSessionID: targetSessionID}
}

func (m Model) embeddedSessionMatchesResumeID(projectPath, resumeID string) bool {
	if m.codexManager == nil || strings.TrimSpace(resumeID) == "" {
		return false
	}
	session, ok := m.codexManager.Session(projectPath)
	if !ok || session == nil {
		return false
	}
	matcher, ok := session.(interface{ MatchesResumeID(string) bool })
	return ok && matcher.MatchesResumeID(resumeID)
}

func (m Model) applyEngineerMessagesLoaded(msg engineerMessagesLoadedMsg) (tea.Model, tea.Cmd) {
	m.engineerMessagesPollInFlight = false
	if msg.recovered {
		m.engineerMessagesRecovered = true
	}
	if m.engineerMessageDeliveries == nil {
		m.engineerMessageDeliveries = make(map[string]struct{})
	}
	if msg.err != nil {
		m.appendBackgroundErrorLogEntry("Engineer message mailbox refresh failed", msg.err, "")
		return m, nil
	}
	if len(msg.messages) == 0 && (!msg.afterCreatedAt.IsZero() || strings.TrimSpace(msg.afterID) != "") {
		m.engineerMessagesCursorAt = time.Time{}
		m.engineerMessagesCursorID = ""
	}
	for _, message := range msg.messages {
		m.engineerMessagesCursorAt = message.CreatedAt
		m.engineerMessagesCursorID = message.ID
		if _, active := m.engineerMessageDeliveries[message.ID]; active {
			continue
		}
		disposition := m.engineerMessageDisposition(message)
		switch {
		case disposition.failure != nil && !disposition.wait:
			m.engineerMessageDeliveries[message.ID] = struct{}{}
			return m, m.recordEngineerMessageStateCmd(message, control.EngineerMessageFailed, disposition.bindSessionID, "Engineer message delivery failed: "+disposition.failure.Error(), disposition.failure)
		case disposition.wait:
			continue
		case disposition.deliver:
			m.engineerMessageDeliveries[message.ID] = struct{}{}
			return m, m.claimEngineerMessageCmd(message.ID, disposition.bindSessionID)
		}
	}
	return m, nil
}

func (m Model) claimEngineerMessageCmd(messageID, targetSessionID string) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		if svc == nil || svc.Store() == nil {
			return engineerMessageClaimedMsg{messageID: messageID, err: errors.New("service store unavailable")}
		}
		ctx, cancel := context.WithTimeout(parent, engineerMessageStoreTimeout)
		defer cancel()
		message, claimed, err := svc.Store().ClaimEngineerMessage(ctx, messageID, targetSessionID)
		return engineerMessageClaimedMsg{messageID: messageID, message: message, claimed: claimed, err: err}
	}
}

func (m Model) applyEngineerMessageClaimed(msg engineerMessageClaimedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil || !msg.claimed {
		messageID := firstNonEmptyTrimmed(msg.message.ID, msg.messageID)
		delete(m.engineerMessageDeliveries, messageID)
		if msg.err != nil {
			m.appendBackgroundErrorLogEntry("Engineer message claim failed", msg.err, msg.message.ProjectPath)
		}
		return m, m.requestEngineerMessagesPollCmd()
	}
	message := msg.message
	project, err := m.resolveControlProjectRef(message.ProjectPath, "")
	if err != nil {
		return m, m.recordEngineerMessageStateCmd(message, control.EngineerMessageFailed, message.TargetSessionID, "Engineer message delivery failed: "+err.Error(), err)
	}
	provider := codexProviderFromControlProvider(message.Provider)
	updated, cmd := m.launchEmbeddedForProjectWithOptions(project, provider, embeddedLaunchOptions{
		modelSelection:  message.EngineerModelSelection,
		forceNew:        message.SessionMode == control.SessionModeNew,
		prompt:          engineerMessageDeliveryPrompt(message),
		reveal:          message.Reveal,
		resumeID:        message.TargetSessionID,
		requireResumeID: strings.TrimSpace(message.TargetSessionID) != "",
	})
	m = normalizeUpdateModel(updated)
	if cmd == nil {
		status := strings.TrimSpace(m.status)
		if status == "" {
			status = "Engineer message delivery is waiting for its target session."
		}
		return m, m.recordEngineerMessageStateCmd(message, control.EngineerMessageQueued, message.TargetSessionID, status, nil)
	}
	cmd = m.todoEngineerLaunchTrackingCmd(message.ProjectPath, message.TodoID, cmd)
	return m, m.engineerMessageDeliveryCmd(message, cmd)
}

func engineerMessageDeliveryPrompt(message control.EngineerMessage) string {
	prompt := strings.TrimSpace(message.Prompt)
	messageID := strings.TrimSpace(message.ID)
	if messageID == "" {
		return prompt
	}
	return fmt.Sprintf("LCR engineer message %s (a host-restart retry may repeat this same message id).\n\n%s", messageID, prompt)
}

func (m Model) engineerMessageDeliveryCmd(message control.EngineerMessage, cmd tea.Cmd) tea.Cmd {
	svc := m.svc
	manager := m.codexManager
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return mapDeferredClaudeLaunchCommand(cmd, func(msg tea.Msg) tea.Msg {
		opened, ok := msg.(codexSessionOpenedMsg)
		if !ok {
			stateErr := errors.New("engineer message launch returned an unexpected result")
			status := "Engineer message delivery failed: " + stateErr.Error()
			recorded, recordErr := persistEngineerMessageState(parent, svc, message, control.EngineerMessageFailed, message.TargetSessionID, status, stateErr)
			if recordErr != nil {
				recorded = message
			}
			return engineerMessageDeliveryRecordedMsg{
				message:         recorded,
				desiredState:    control.EngineerMessageFailed,
				targetSessionID: message.TargetSessionID,
				status:          status,
				stateErr:        stateErr,
				recordErr:       recordErr,
			}
		}
		state := control.EngineerMessageDelivered
		stateErr := opened.err
		status := "Message delivered to the " + message.Provider.Label() + " engineer."
		targetSessionID := strings.TrimSpace(message.TargetSessionID)
		if targetSessionID == "" {
			targetSessionID = strings.TrimSpace(opened.snapshot.ThreadID)
		}
		if opened.snapshot.BusyExternal {
			state = control.EngineerMessageQueued
			stateErr = nil
			status = "The target session is active outside LCR; the message remains queued."
		} else if stateErr != nil && engineerMessageTargetIsTemporarilyBusy(manager, message, stateErr) {
			state = control.EngineerMessageQueued
			stateErr = nil
			status = "The target session became busy; the message remains queued."
		} else if stateErr != nil {
			state = control.EngineerMessageFailed
			status = "Engineer message delivery failed: " + stateErr.Error()
		}

		recorded, recordErr := persistEngineerMessageState(parent, svc, message, state, targetSessionID, status, stateErr)
		if recordErr != nil {
			recorded = message
		}
		return tea.BatchMsg{
			func() tea.Msg { return opened },
			func() tea.Msg {
				return engineerMessageDeliveryRecordedMsg{
					message:         recorded,
					desiredState:    state,
					targetSessionID: targetSessionID,
					status:          status,
					stateErr:        stateErr,
					recordErr:       recordErr,
				}
			},
		}
	})
}

func persistEngineerMessageState(
	parent context.Context,
	svc *service.Service,
	message control.EngineerMessage,
	state control.EngineerMessageState,
	targetSessionID string,
	status string,
	stateErr error,
) (control.EngineerMessage, error) {
	if svc == nil || svc.Store() == nil {
		return control.EngineerMessage{}, errors.New("service store unavailable while recording engineer message delivery")
	}
	ctx, cancel := context.WithTimeout(parent, engineerMessageStoreTimeout)
	defer cancel()
	return svc.Store().RecordEngineerMessageState(ctx, message.ID, state, targetSessionID, status, stateErr)
}

func engineerMessageTargetIsTemporarilyBusy(manager *codexapp.Manager, message control.EngineerMessage, deliveryErr error) bool {
	if manager == nil || errors.Is(deliveryErr, codexapp.ErrSessionChanged) {
		return false
	}
	session, ok := manager.Session(message.ProjectPath)
	if !ok || session == nil {
		return false
	}
	snapshot := session.Snapshot()
	if embeddedProvider(snapshot) != codexProviderFromControlProvider(message.Provider) {
		return false
	}
	return embeddedSessionBlocksProviderSwitch(snapshot)
}

func (m Model) recordEngineerMessageStateCmd(message control.EngineerMessage, state control.EngineerMessageState, targetSessionID, status string, stateErr error) tea.Cmd {
	return m.recordEngineerMessageStateAttemptCmd(message, state, targetSessionID, status, stateErr, 0)
}

func (m Model) recordEngineerMessageStateAttemptCmd(
	message control.EngineerMessage,
	state control.EngineerMessageState,
	targetSessionID string,
	status string,
	stateErr error,
	retryAttempt int,
) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		recorded, recordErr := persistEngineerMessageState(parent, svc, message, state, targetSessionID, status, stateErr)
		if recordErr != nil {
			recorded = message
		}
		return engineerMessageDeliveryRecordedMsg{
			message:         recorded,
			desiredState:    state,
			targetSessionID: targetSessionID,
			status:          status,
			stateErr:        stateErr,
			recordErr:       recordErr,
			retryAttempt:    retryAttempt,
		}
	}
}

func (m Model) applyEngineerMessageDeliveryRecorded(msg engineerMessageDeliveryRecordedMsg) (tea.Model, tea.Cmd) {
	if msg.recordErr != nil {
		if msg.retryAttempt == 0 {
			m.appendBackgroundErrorLogEntry("Engineer message delivery receipt failed", msg.recordErr, msg.message.ProjectPath)
		}
		m.status = fmt.Sprintf("Could not save the delivery receipt for message %s; retrying.", shortID(msg.message.ID))
		return m, m.retryEngineerMessageReceiptCmd(msg)
	}
	delete(m.engineerMessageDeliveries, msg.message.ID)
	if msg.message.State == control.EngineerMessageDelivered {
		m = m.trackDeliveredEngineerMessage(msg.message)
	}
	if msg.stateErr != nil || msg.message.State == control.EngineerMessageFailed {
		err := msg.stateErr
		if err == nil {
			err = errors.New(firstNonEmptyTrimmed(msg.message.LastError, msg.status, "engineer message delivery failed"))
		}
		m.appendBackgroundErrorLogEntry("Engineer message delivery failed", err, msg.message.ProjectPath)
	}
	if strings.TrimSpace(msg.status) != "" {
		m.status = msg.status
	}
	if msg.message.State == control.EngineerMessageQueued {
		return m, nil
	}
	return m, m.requestEngineerMessagesPollCmd()
}

func (m Model) retryEngineerMessageReceiptCmd(msg engineerMessageDeliveryRecordedMsg) tea.Cmd {
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	attempt := msg.retryAttempt + 1
	delay := engineerMessageRetryBase
	for i := 1; i < attempt && delay < engineerMessageRetryMax; i++ {
		delay *= 2
	}
	if delay > engineerMessageRetryMax {
		delay = engineerMessageRetryMax
	}
	return func() tea.Msg {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-parent.Done():
			return nil
		case <-timer.C:
			return engineerMessageReceiptRetryMsg{
				message:         msg.message,
				desiredState:    msg.desiredState,
				targetSessionID: msg.targetSessionID,
				status:          msg.status,
				stateErr:        msg.stateErr,
				retryAttempt:    attempt,
			}
		}
	}
}

func (m Model) applyEngineerMessageReceiptRetry(msg engineerMessageReceiptRetryMsg) (tea.Model, tea.Cmd) {
	return m, m.recordEngineerMessageStateAttemptCmd(
		msg.message,
		msg.desiredState,
		msg.targetSessionID,
		msg.status,
		msg.stateErr,
		msg.retryAttempt,
	)
}

func (m Model) trackDeliveredEngineerMessage(message control.EngineerMessage) Model {
	if message.TodoID <= 0 || m.codexManager == nil {
		return m
	}
	session, ok := m.codexManager.Session(message.ProjectPath)
	if !ok || session == nil {
		return m
	}
	snapshot := session.Snapshot()
	if embeddedProvider(snapshot) != codexProviderFromControlProvider(message.Provider) {
		return m
	}
	sessionID := strings.TrimSpace(snapshot.ThreadID)
	key := bossTrackedTodoKey(message.ProjectPath, modelSessionSourceFromCodexProvider(embeddedProvider(snapshot)), sessionID)
	if key == "" {
		return m
	}
	if m.bossTrackedTodos == nil {
		m.bossTrackedTodos = make(map[string]bossTrackedTodo)
	}
	startedAt := bossControlActivityStartedAt(snapshot)
	m.bossTrackedTodos[key] = bossTrackedTodo{
		ID:          message.TodoID,
		Label:       strings.TrimSpace(message.TodoLabel),
		Text:        strings.TrimSpace(message.TodoText),
		ProjectPath: strings.TrimSpace(message.ProjectPath),
		ProjectName: projectTitle(message.ProjectPath, ""),
		Provider:    modelSessionSourceFromCodexProvider(embeddedProvider(snapshot)),
		SessionID:   sessionID,
		StartedAt:   startedAt,
	}
	return m
}

func engineerMessageReceipt(message control.EngineerMessage, status string) *control.EngineerMessageReceipt {
	if strings.TrimSpace(message.ID) == "" {
		return nil
	}
	return &control.EngineerMessageReceipt{
		MessageID:       message.ID,
		State:           message.State,
		Provider:        message.Provider,
		ProjectPath:     message.ProjectPath,
		TargetSessionID: message.TargetSessionID,
		Status:          strings.TrimSpace(status),
	}
}

func engineerMessageControlResult(msg engineerMessageQueuedMsg) bossui.ControlInvocationResultMsg {
	status := engineerMessageStatus(msg.message)
	if msg.err != nil {
		status = "Engineer message could not be queued: " + msg.err.Error()
	}
	return bossui.ControlInvocationResultMsg{
		Status:           status,
		Err:              msg.err,
		OperationPending: msg.err == nil && !msg.message.State.Terminal(),
		Delivery:         engineerMessageReceipt(msg.message, status),
	}
}
