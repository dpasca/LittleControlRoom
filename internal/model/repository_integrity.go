package model

import (
	"strings"
	"time"
)

type RepositoryIntegrityMode string

const (
	RepositoryIntegrityModeOff  RepositoryIntegrityMode = "off"
	RepositoryIntegrityModeWarn RepositoryIntegrityMode = "warn"
)

type RepositoryRootPolicy struct {
	RootPath                string
	ExpectedBranch          string
	ExpectedBranchSource    string
	Mode                    RepositoryIntegrityMode
	AcknowledgedFingerprint string
	UpdatedAt               time.Time
}

type RepositoryIntegrityMember struct {
	Path         string
	Name         string
	Branch       string
	ParentBranch string
	WorktreeKind WorktreeKind
	Dirty        bool
	Conflict     bool
	Present      bool
}

type RepositoryWorkspaceExcursion struct {
	At             time.Time `json:"at"`
	ProjectPath    string    `json:"project_path"`
	RootPath       string    `json:"root_path"`
	ExpectedBranch string    `json:"expected_branch"`
	Provider       string    `json:"provider"`
	SessionID      string    `json:"session_id"`
	ItemID         string    `json:"item_id"`
	Command        string    `json:"command"`
	CWD            string    `json:"cwd"`
}

type RepositoryIntegrityState struct {
	RootPath              string
	RootName              string
	ExpectedBranch        string
	ExpectedBranchSource  string
	ActualBranch          string
	Mode                  RepositoryIntegrityMode
	Displaced             bool
	Acknowledged          bool
	Fingerprint           string
	RootDirty             bool
	RootConflict          bool
	SuggestedWorktreePath string
	CanRepair             bool
	RepairBlockReason     string
	Members               []RepositoryIntegrityMember
	RecentExcursions      []RepositoryWorkspaceExcursion
}

func NormalizeRepositoryIntegrityMode(mode RepositoryIntegrityMode) RepositoryIntegrityMode {
	switch mode {
	case RepositoryIntegrityModeOff:
		return mode
	default:
		return RepositoryIntegrityModeWarn
	}
}

func (s RepositoryIntegrityState) NeedsAttention() bool {
	return s.Displaced && !s.Acknowledged && NormalizeRepositoryIntegrityMode(s.Mode) != RepositoryIntegrityModeOff
}

func (s RepositoryIntegrityState) ContainsProject(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if strings.TrimSpace(s.RootPath) == path {
		return true
	}
	for _, member := range s.Members {
		if strings.TrimSpace(member.Path) == path {
			return true
		}
	}
	return false
}
