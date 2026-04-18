package browserctl

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type SessionRef struct {
	Provider    string
	ProjectPath string
	SessionID   string
}

func (r SessionRef) Normalize() SessionRef {
	projectPath := strings.TrimSpace(r.ProjectPath)
	if projectPath != "" {
		projectPath = filepath.Clean(projectPath)
	}
	return SessionRef{
		Provider:    strings.TrimSpace(strings.ToLower(r.Provider)),
		ProjectPath: projectPath,
		SessionID:   strings.TrimSpace(r.SessionID),
	}
}

func (r SessionRef) Valid() bool {
	normalized := r.Normalize()
	return normalized.Provider != "" && normalized.ProjectPath != "" && normalized.SessionID != ""
}

func (r SessionRef) key() string {
	normalized := r.Normalize()
	if !normalized.Valid() {
		return ""
	}
	return normalized.Provider + "\x00" + normalized.ProjectPath + "\x00" + normalized.SessionID
}

type InteractiveLeaseState string

const (
	InteractiveLeaseStateWaiting     InteractiveLeaseState = "waiting"
	InteractiveLeaseStateInteractive InteractiveLeaseState = "interactive"
)

type InteractiveLease struct {
	Ref         SessionRef
	Policy      Policy
	State       InteractiveLeaseState
	LoginURL    string
	SourceLabel string
	UpdatedAt   time.Time
}

func (l InteractiveLease) Normalize() InteractiveLease {
	normalized := l
	normalized.Ref = normalized.Ref.Normalize()
	normalized.Policy = normalized.Policy.Normalize()
	normalized.LoginURL = strings.TrimSpace(normalized.LoginURL)
	normalized.SourceLabel = strings.TrimSpace(normalized.SourceLabel)
	switch normalized.State {
	case InteractiveLeaseStateInteractive:
	default:
		normalized.State = InteractiveLeaseStateWaiting
	}
	return normalized
}

type Observation struct {
	Ref       SessionRef
	Policy    Policy
	Activity  SessionActivity
	LoginURL  string
	UpdatedAt time.Time
}

type ControllerSnapshot struct {
	Interactive *InteractiveLease
	Waiting     []InteractiveLease
}

type InteractiveAcquireResult struct {
	Granted  bool
	Lease    InteractiveLease
	Owner    *InteractiveLease
	Snapshot ControllerSnapshot
}

type Controller struct {
	mu             sync.Mutex
	leases         map[string]InteractiveLease
	interactiveKey string
}

func NewController() *Controller {
	return &Controller{leases: make(map[string]InteractiveLease)}
}

func (c *Controller) Snapshot() ControllerSnapshot {
	if c == nil {
		return ControllerSnapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *Controller) Observe(observation Observation) ControllerSnapshot {
	if c == nil {
		return ControllerSnapshot{}
	}
	ref := observation.Ref.Normalize()
	if !ref.Valid() {
		return c.Snapshot()
	}
	key := ref.key()

	c.mu.Lock()
	defer c.mu.Unlock()

	if !observationTracksManagedLogin(ref, observation.Policy, observation.Activity, observation.LoginURL) {
		delete(c.leases, key)
		if c.interactiveKey == key {
			c.interactiveKey = ""
		}
		return c.snapshotLocked()
	}

	lease := InteractiveLease{
		Ref:         ref,
		Policy:      observation.Policy.Normalize(),
		State:       InteractiveLeaseStateWaiting,
		LoginURL:    strings.TrimSpace(observation.LoginURL),
		SourceLabel: observation.Activity.Normalize().SourceLabel(),
		UpdatedAt:   observation.UpdatedAt,
	}.Normalize()
	if lease.UpdatedAt.IsZero() {
		lease.UpdatedAt = observation.Activity.Normalize().LastEventAt
	}
	if lease.UpdatedAt.IsZero() {
		lease.UpdatedAt = time.Now()
	}
	if c.interactiveKey == key {
		lease.State = InteractiveLeaseStateInteractive
	}
	c.leases[key] = lease
	return c.snapshotLocked()
}

func (c *Controller) Remove(ref SessionRef) ControllerSnapshot {
	if c == nil {
		return ControllerSnapshot{}
	}
	normalized := ref.Normalize()
	if !normalized.Valid() {
		return c.Snapshot()
	}
	key := normalized.key()
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.leases, key)
	if c.interactiveKey == key {
		c.interactiveKey = ""
	}
	return c.snapshotLocked()
}

func (c *Controller) AcquireInteractive(ref SessionRef) InteractiveAcquireResult {
	if c == nil {
		return InteractiveAcquireResult{}
	}
	normalized := ref.Normalize()
	if !normalized.Valid() {
		return InteractiveAcquireResult{}
	}
	key := normalized.key()

	c.mu.Lock()
	defer c.mu.Unlock()

	lease, ok := c.leases[key]
	if !ok {
		return InteractiveAcquireResult{Snapshot: c.snapshotLocked()}
	}
	if c.interactiveKey == "" || c.interactiveKey == key {
		c.interactiveKey = key
		lease.State = InteractiveLeaseStateInteractive
		lease.UpdatedAt = time.Now()
		c.leases[key] = lease
		return InteractiveAcquireResult{
			Granted:  true,
			Lease:    lease.Normalize(),
			Snapshot: c.snapshotLocked(),
		}
	}

	owner := c.leases[c.interactiveKey].Normalize()
	return InteractiveAcquireResult{
		Granted:  false,
		Lease:    lease.Normalize(),
		Owner:    &owner,
		Snapshot: c.snapshotLocked(),
	}
}

func (c *Controller) ReleaseInteractive(ref SessionRef) ControllerSnapshot {
	if c == nil {
		return ControllerSnapshot{}
	}
	normalized := ref.Normalize()
	if !normalized.Valid() {
		return c.Snapshot()
	}
	key := normalized.key()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.interactiveKey == key {
		c.interactiveKey = ""
	}
	if lease, ok := c.leases[key]; ok {
		lease.State = InteractiveLeaseStateWaiting
		c.leases[key] = lease.Normalize()
	}
	return c.snapshotLocked()
}

func observationTracksManagedLogin(ref SessionRef, policy Policy, activity SessionActivity, loginURL string) bool {
	if !ref.Valid() {
		return false
	}
	normalizedPolicy := policy.Normalize()
	normalizedActivity := activity.Normalize()
	if normalizedPolicy.ManagementMode != ManagementModeManaged || normalizedPolicy.LoginMode != LoginModePromote {
		return false
	}
	if normalizedActivity.State != SessionActivityStateWaitingForUser {
		return false
	}
	if strings.TrimSpace(loginURL) == "" {
		return false
	}
	return true
}

func (c *Controller) snapshotLocked() ControllerSnapshot {
	if c.interactiveKey != "" {
		if _, ok := c.leases[c.interactiveKey]; !ok {
			c.interactiveKey = ""
		}
	}

	var snapshot ControllerSnapshot
	for key, stored := range c.leases {
		lease := stored.Normalize()
		if key == c.interactiveKey {
			lease.State = InteractiveLeaseStateInteractive
			owner := lease
			snapshot.Interactive = &owner
			continue
		}
		lease.State = InteractiveLeaseStateWaiting
		snapshot.Waiting = append(snapshot.Waiting, lease)
	}
	sort.Slice(snapshot.Waiting, func(i, j int) bool {
		if snapshot.Waiting[i].UpdatedAt.Equal(snapshot.Waiting[j].UpdatedAt) {
			if snapshot.Waiting[i].Ref.Provider != snapshot.Waiting[j].Ref.Provider {
				return snapshot.Waiting[i].Ref.Provider < snapshot.Waiting[j].Ref.Provider
			}
			return snapshot.Waiting[i].Ref.ProjectPath < snapshot.Waiting[j].Ref.ProjectPath
		}
		return snapshot.Waiting[i].UpdatedAt.After(snapshot.Waiting[j].UpdatedAt)
	})
	return snapshot
}
