package codexapp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const managedWorkspaceContextSource = "little-control-room/workspace-boundary"

type workspaceContractUpdater interface {
	SetWorkspaceContract(WorkspaceContract, WorkspaceExcursionHandler)
}

func (s *appServerSession) SetWorkspaceContract(contract WorkspaceContract, handler WorkspaceExcursionHandler) {
	if s == nil {
		return
	}
	contract = normalizeWorkspaceContract(contract)
	s.mu.Lock()
	changed := contract != s.workspaceContract
	s.workspaceContract = contract
	s.workspaceExcursionHandler = handler
	if changed {
		s.workspaceExcursionItems = make(map[string]struct{})
		s.workspaceWarningShown = false
	}
	s.mu.Unlock()
}

func (s *appServerSession) managedTurnContext() map[string]additionalContextEntry {
	contextEntries := s.managedBrowserTurnContext()
	s.mu.Lock()
	contract := normalizeWorkspaceContract(s.workspaceContract)
	s.mu.Unlock()
	if contract.AssignedPath == "" || contract.RepositoryRootPath == "" {
		return contextEntries
	}
	if contextEntries == nil {
		contextEntries = make(map[string]additionalContextEntry, 1)
	}
	contextEntries[managedWorkspaceContextSource] = additionalContextEntry{
		Kind:  applicationContextKind,
		Value: managedWorkspaceTurnContextText(contract),
	}
	return contextEntries
}

func managedWorkspaceTurnContextText(contract WorkspaceContract) string {
	contract = normalizeWorkspaceContract(contract)
	lines := []string{
		"Little Control Room workspace contract for this turn (warn-only):",
		"- Your assigned workspace is " + contract.AssignedPath + ".",
		"- The canonical repository root is " + contract.RepositoryRootPath + ".",
	}
	if contract.ExpectedRootBranch != "" {
		lines = append(lines, "- The canonical root is expected to remain on branch "+contract.ExpectedRootBranch+".")
	}
	if sameWorkspacePath(contract.AssignedPath, contract.RepositoryRootPath) {
		lines = append(lines,
			"- Do not switch the canonical root onto a feature/task branch for isolated work. Propose a linked worktree and ask the user before changing the root branch.",
		)
	} else {
		lines = append(lines,
			"- Keep commands, edits, and generated files inside the assigned workspace. Do not work from the canonical root or another linked worktree.",
			"- If the task genuinely requires a different checkout, explain why and ask the user before crossing that workspace boundary.",
		)
	}
	lines = append(lines, "- LCR is warning, not enforcing: you remain responsible for checking cwd and Git state before mutations.")
	return strings.Join(lines, "\n")
}

func normalizeWorkspaceContract(contract WorkspaceContract) WorkspaceContract {
	contract.AssignedPath = cleanWorkspacePath(contract.AssignedPath)
	contract.RepositoryRootPath = cleanWorkspacePath(contract.RepositoryRootPath)
	contract.ExpectedRootBranch = strings.TrimSpace(contract.ExpectedRootBranch)
	return contract
}

func (s *appServerSession) captureWorkspaceExcursionLocked(item map[string]json.RawMessage) (WorkspaceExcursion, WorkspaceExcursionHandler, bool) {
	contract := normalizeWorkspaceContract(s.workspaceContract)
	if contract.AssignedPath == "" || contract.RepositoryRootPath == "" || sameWorkspacePath(contract.AssignedPath, contract.RepositoryRootPath) {
		return WorkspaceExcursion{}, nil, false
	}
	cwd := cleanWorkspacePath(decodeRawString(item["cwd"]))
	if cwd == "" || workspacePathContains(contract.AssignedPath, cwd) || !workspacePathContains(contract.RepositoryRootPath, cwd) {
		return WorkspaceExcursion{}, nil, false
	}
	itemID := strings.TrimSpace(decodeRawString(item["id"]))
	if itemID != "" {
		if s.workspaceExcursionItems == nil {
			s.workspaceExcursionItems = make(map[string]struct{})
		}
		if _, seen := s.workspaceExcursionItems[itemID]; seen {
			return WorkspaceExcursion{}, nil, false
		}
		s.workspaceExcursionItems[itemID] = struct{}{}
	}
	command := strings.TrimSpace(decodeRawString(item["command"]))
	excursion := WorkspaceExcursion{
		At:                 time.Now(),
		ProjectPath:        contract.AssignedPath,
		RepositoryRootPath: contract.RepositoryRootPath,
		ExpectedRootBranch: contract.ExpectedRootBranch,
		Provider:           ProviderCodex,
		SessionID:          strings.TrimSpace(s.threadID),
		ItemID:             itemID,
		Command:            command,
		CWD:                cwd,
	}
	if !s.workspaceWarningShown {
		notice := fmt.Sprintf("Workspace boundary warning: this session is assigned to %s, but a command ran from the canonical root at %s. LCR did not block it; return to the assigned worktree before making changes.", contract.AssignedPath, cwd)
		noticeID := "lcr-workspace-warning"
		if itemID != "" {
			noticeID += "-" + itemID
		}
		s.appendEntryLocked(noticeID, TranscriptSystem, notice)
		s.lastSystemNotice = notice
		s.status = "Workspace boundary warning"
		s.workspaceWarningShown = true
	}
	return excursion, s.workspaceExcursionHandler, true
}

func dispatchWorkspaceExcursion(handler WorkspaceExcursionHandler, excursion WorkspaceExcursion) {
	if handler == nil {
		return
	}
	go handler(excursion)
}

func workspacePathContains(parent, child string) bool {
	parent = cleanWorkspacePath(parent)
	child = cleanWorkspacePath(child)
	if parent == "" || child == "" {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func sameWorkspacePath(left, right string) bool {
	left = cleanWorkspacePath(left)
	right = cleanWorkspacePath(right)
	return left != "" && left == right
}

func cleanWorkspacePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if path == "." {
		return ""
	}
	return path
}
