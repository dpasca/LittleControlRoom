package codexapp

import "sync/atomic"

// TurnAdmission coordinates host-owned repository writers. It is called before
// taking a session mutex and releases only after submission has made activity
// visible. Inspection, cancellation and approval of an existing turn stay usable.
type TurnAdmission func(projectPath, controlSessionKey string) (func(), error)

func turnAdmissionForLaunch(req LaunchRequest) func() (func(), error) {
	return func() (func(), error) {
		if req.TurnAdmission == nil {
			return func() {}, nil
		}
		return req.TurnAdmission(req.ProjectPath, req.TodoCaptureSessionKey)
	}
}

func beginManagedTurn(admission func() (func(), error)) (func(), error) {
	if admission == nil {
		return func() {}, nil
	}
	return admission()
}

// SetTurnAdmission binds host ownership checks for current and future sessions.
func (m *Manager) SetTurnAdmission(admission TurnAdmission) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turnAdmission = admission
}

func (m *Manager) enrichTurnAdmission(req LaunchRequest) LaunchRequest {
	if req.TurnAdmission == nil {
		// Hosted registries can contain inspection sessions before the TUI binds
		// its ownership service. Existing sessions must see that later binding.
		req.TurnAdmission = func(path, key string) (func(), error) {
			m.mu.Lock()
			admission := m.turnAdmission
			m.mu.Unlock()
			if admission == nil {
				return func() {}, nil
			}
			return admission(path, key)
		}
	}
	return req
}

// Hold admission until the new session is registered. Otherwise an initial
// provider submission could start writing before the host can see its snapshot.
func admitManagedLaunch(req LaunchRequest) (LaunchRequest, func(), func(), error) {
	if req.TurnAdmission == nil || launchRequestInitialInput(req).Empty() {
		return req, func() {}, func() {}, nil
	}
	admission := req.TurnAdmission
	unlock, err := admission(req.ProjectPath, req.TodoCaptureSessionKey)
	if err != nil {
		return req, nil, nil, err
	}
	var initial atomic.Bool
	initial.Store(true)
	req.TurnAdmission = func(path, key string) (func(), error) {
		if initial.CompareAndSwap(true, false) {
			return func() {}, nil
		}
		return admission(path, key)
	}
	return req, func() { initial.Store(false) }, unlock, nil
}

func beginManagedSubmission(admission func() (func(), error), input Submission) (func(), error) {
	unlock, err := beginManagedTurn(admission)
	if err != nil {
		return nil, err
	}
	if input.BeforeStart != nil {
		if err := input.BeforeStart(); err != nil {
			unlock()
			return nil, err
		}
	}
	return unlock, nil
}
