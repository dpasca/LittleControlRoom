// Package todocontrol validates TODO ownership independently of checkout routing.
// All entry points do disk/DB/Git work and must run outside the UI thread.
package todocontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/projectowner"
	"lcroom/internal/store"
)

type Plan struct {
	OwnerPath       string
	DestinationPath string
	Todo            model.TodoItem
	Project         model.ProjectSummary
	Replay          bool
}

func Applies(name control.CapabilityName) bool {
	return name == control.CapabilityTodoAdd || name == control.CapabilityTodoCreateWorktreeAndStartEngineer || name == control.CapabilityTodoRepairOwner || name == control.CapabilityEngineerSendPrompt
}

// Inspect binds resolved expectations into the typed invocation. The original
// project_path remains provenance and permission scope; ownership is separate.
func Inspect(ctx context.Context, st *store.Store, inv control.Invocation) (control.Invocation, Plan, error) {
	var plan Plan
	var err error
	switch inv.Capability {
	case control.CapabilityTodoAdd:
		var input control.TodoAddInput
		if err = json.Unmarshal(inv.Args, &input); err == nil {
			plan, err = InspectAdd(ctx, st, input)
			if err == nil {
				input.TodoOwnerPath = plan.OwnerPath
				inv.Args, err = json.Marshal(input)
			}
		}
	case control.CapabilityTodoCreateWorktreeAndStartEngineer:
		var input control.TodoCreateWorktreeAndStartEngineerInput
		if err = json.Unmarshal(inv.Args, &input); err == nil {
			plan, err = InspectLaunch(ctx, st, input)
			if err == nil {
				input.TodoOwnerPath = plan.OwnerPath
				if input.TodoID > 0 && input.TodoLabel == "" {
					input.TodoLabel = plan.Todo.Text
				}
				inv.Args, err = json.Marshal(input)
			}
		}
	case control.CapabilityEngineerSendPrompt:
		var input control.EngineerSendPromptInput
		if err = json.Unmarshal(inv.Args, &input); err == nil {
			if input.TodoID <= 0 {
				return inv, plan, nil
			}
			plan, err = InspectSend(ctx, st, input)
			if err == nil {
				input.TodoOwnerPath, input.DestinationPath = plan.OwnerPath, plan.DestinationPath
				inv.Args, err = json.Marshal(input)
			}
		}
	case control.CapabilityTodoRepairOwner:
		var input control.TodoRepairOwnerInput
		if err = json.Unmarshal(inv.Args, &input); err == nil {
			input, plan, err = InspectRepair(ctx, st, input)
			if err == nil {
				inv.Args, err = json.Marshal(input)
			}
		}
	}
	return inv, plan, err
}

func Project(ctx context.Context, st *store.Store, path, name string) (model.ProjectSummary, error) {
	if strings.TrimSpace(path) != "" {
		return st.GetProjectSummary(ctx, filepath.Clean(path), true)
	}
	projects, err := st.GetProjectSummaryMap(ctx)
	if err != nil {
		return model.ProjectSummary{}, err
	}
	var match model.ProjectSummary
	for _, p := range projects {
		if strings.EqualFold(p.Name, name) || strings.EqualFold(filepath.Base(p.Path), name) {
			if match.Path != "" && match.Path != p.Path {
				return model.ProjectSummary{}, fmt.Errorf("project name is ambiguous: %s", name)
			}
			match = p
		}
	}
	if match.Path == "" {
		return match, fmt.Errorf("project is not loaded: %s", name)
	}
	return st.GetProjectSummary(ctx, match.Path, true)
}

func expectPath(expected, actual, field string) error {
	if expected != "" && filepath.Clean(expected) != filepath.Clean(actual) {
		return fmt.Errorf("%s changed from %s to %s; prepare a new proposal", field, expected, actual)
	}
	return nil
}

func InspectAdd(ctx context.Context, st *store.Store, input control.TodoAddInput) (Plan, error) {
	project, err := Project(ctx, st, input.ProjectPath, input.ProjectName)
	if err != nil {
		return Plan{}, err
	}
	scope, err := projectowner.New(st).ResolveTodoOwner(ctx, project.Path)
	if err != nil {
		return Plan{}, err
	}
	if err := expectPath(input.TodoOwnerPath, scope.ProjectPath, "repository TODO owner"); err != nil {
		return Plan{}, err
	}
	owner, err := st.GetProjectSummary(ctx, scope.ProjectPath, true)
	return Plan{OwnerPath: scope.ProjectPath, Project: owner}, err
}

func InspectLaunch(ctx context.Context, st *store.Store, input control.TodoCreateWorktreeAndStartEngineerInput) (Plan, error) {
	if strings.TrimSpace(input.RequestID) == "" {
		return Plan{}, fmt.Errorf("launch request id is required before confirmation")
	}
	project, err := Project(ctx, st, input.ProjectPath, input.ProjectName)
	if err != nil {
		return Plan{}, err
	}
	scope, err := projectowner.New(st).ResolveRepository(ctx, project.Path)
	if err != nil {
		return Plan{}, err
	}
	if err := expectPath(input.TodoOwnerPath, scope.ProjectPath, "repository TODO owner"); err != nil {
		return Plan{}, err
	}
	root, err := st.GetProjectSummary(ctx, scope.ProjectPath, true)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{OwnerPath: scope.ProjectPath, Project: root}
	saved, replay, err := st.InspectTodoLaunchRequest(ctx, input)
	if err != nil {
		return Plan{}, err
	}
	if input.TodoID > 0 || replay {
		todo := saved
		if !replay {
			todo, err = st.GetTodo(ctx, input.TodoID)
		}
		if err != nil {
			return Plan{}, err
		}
		if todo.ProjectPath != scope.ProjectPath {
			return Plan{}, ownershipError(todo, project.Path)
		}
		if todo.LaunchRequestID != input.RequestID {
			origin, err := st.TodoHasWorktreeOrigin(ctx, todo.ID)
			if err != nil {
				return Plan{}, err
			}
			if origin || todo.Done || todo.LaunchRequestID != "" || todo.WorkProjectPath != "" || todo.WorkSessionID != "" || todo.LaunchWorktreePath != "" || !todo.WorkClaimedAt.IsZero() || todo.WorkState != "" {
				return Plan{}, fmt.Errorf("TODO #%d already has work or is complete; use engineer.send_prompt for its existing session, or inspect its saved launch", todo.ID)
			}
		}
		plan.Todo = todo
		plan.DestinationPath = todo.LaunchWorktreePath
	}
	if input.WorktreePath != "" && input.WorktreePath != plan.DestinationPath {
		return Plan{}, fmt.Errorf("worktree_path is a host recovery field; omit it for a new launch")
	}
	return plan, nil
}

func InspectSend(ctx context.Context, st *store.Store, input control.EngineerSendPromptInput) (Plan, error) {
	project, err := Project(ctx, st, input.ProjectPath, input.ProjectName)
	if err != nil {
		return Plan{}, err
	}
	if info, err := os.Stat(project.Path); err != nil || !info.IsDir() {
		return Plan{}, fmt.Errorf("engineer destination %s is unavailable", project.Path)
	}
	plan := Plan{Project: project, DestinationPath: project.Path}
	if input.TodoID > 0 {
		scope, err := projectowner.New(st).ResolveTodoOwner(ctx, project.Path)
		if err != nil {
			return Plan{}, err
		}
		todo, err := st.GetTodo(ctx, input.TodoID)
		if err != nil {
			return Plan{}, err
		}
		if todo.ProjectPath != scope.ProjectPath {
			return Plan{}, ownershipError(todo, project.Path)
		}
		if todo.Done {
			return Plan{}, fmt.Errorf("TODO #%d is already complete", todo.ID)
		}
		if scope.FromWorktree && todo.WorkProjectPath == "" && project.WorktreeOriginTodoID != todo.ID {
			return Plan{}, fmt.Errorf("TODO #%d has no work assignment to %s; omit todo_id or use repository root path %s", todo.ID, project.Path, scope.ProjectPath)
		}
		plan.OwnerPath, plan.Todo = scope.ProjectPath, todo
		if todo.WorkProjectPath != "" {
			if scope.FromWorktree && !projectowner.SamePath(project.Path, todo.WorkProjectPath) {
				return Plan{}, fmt.Errorf("TODO #%d works in %s, not %s; omit todo_id to message this checkout without redirecting", todo.ID, todo.WorkProjectPath, project.Path)
			}
			worker, err := Project(ctx, st, todo.WorkProjectPath, "")
			if err != nil {
				return Plan{}, err
			}
			if worker.Path != scope.ProjectPath {
				workerScope, err := projectowner.New(st).ResolveRepository(ctx, worker.Path)
				if err != nil || workerScope.ProjectPath != scope.ProjectPath {
					return Plan{}, fmt.Errorf("TODO #%d worker %s no longer has trusted membership in %s", todo.ID, worker.Path, scope.ProjectPath)
				}
			}
			plan.Project, plan.DestinationPath = worker, worker.Path
		}
		if input.TargetSessionID != "" && todo.WorkSessionID != "" {
			source, canonical, _ := model.NormalizeSessionIdentity(model.SessionSource(input.Provider), "", input.TargetSessionID, "")
			_, recorded, _ := model.NormalizeSessionIdentity(todo.WorkProvider, "", todo.WorkSessionID, "")
			if source != todo.WorkProvider || canonical != recorded {
				return Plan{}, fmt.Errorf("TODO #%d is linked to %s session %s; omit todo_id to message another exact session", todo.ID, todo.WorkProvider, todo.WorkSessionID)
			}
		}
	}
	if err := expectPath(input.TodoOwnerPath, plan.OwnerPath, "repository TODO owner"); err != nil {
		return Plan{}, err
	}
	if err := expectPath(input.DestinationPath, plan.DestinationPath, "worker destination"); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func ownershipError(todo model.TodoItem, path string) error {
	return fmt.Errorf("TODO #%d belongs to repository %s; %s does not resolve to that owner. Omit todo_id to send without TODO tracking, or use the repository root path %s (incorrect stored ownership requires todo.repair_owner)", todo.ID, todo.ProjectPath, path, todo.ProjectPath)
}

func InspectRepair(ctx context.Context, st *store.Store, input control.TodoRepairOwnerInput) (control.TodoRepairOwnerInput, Plan, error) {
	if strings.TrimSpace(input.RequestID) == "" {
		return input, Plan{}, fmt.Errorf("repair request id is required before confirmation")
	}
	reviewRevision, err := st.TodoOwnerRevision(ctx, input.TodoID)
	if err != nil {
		return input, Plan{}, err
	}
	resolver := projectowner.New(st)
	source, err := resolver.ResolveRepository(ctx, input.ExpectedOwnerPath)
	if err != nil {
		return input, Plan{}, err
	}
	if source.ProjectPath != input.RepositoryPath || !source.FromWorktree {
		return input, Plan{}, fmt.Errorf("expected owner must be a verified linked checkout of repository %s", input.RepositoryPath)
	}
	todo, err := st.GetTodo(ctx, input.TodoID)
	if err != nil {
		return input, Plan{}, err
	}
	replay, err := st.TodoOwnerRepairReplay(ctx, input)
	if err != nil {
		return input, Plan{}, err
	}
	expectedOwner := input.ExpectedOwnerPath
	if replay {
		expectedOwner = input.RepositoryPath
		if input.ExpectedRevision == "" {
			prior, err := st.TodoOwnerRepairInput(ctx, input.RequestID)
			if err != nil {
				return input, Plan{}, err
			}
			input.ExpectedRevision = prior.ExpectedRevision
		}
	}
	if todo.ProjectPath != expectedOwner {
		return input, Plan{}, fmt.Errorf("TODO #%d expected owner %s, found %s; refresh before retrying", todo.ID, expectedOwner, todo.ProjectPath)
	}
	if todo.WorkProjectPath != "" {
		worker, err := resolver.ResolveRepository(ctx, todo.WorkProjectPath)
		if err != nil || worker.ProjectPath != input.RepositoryPath {
			return input, Plan{}, fmt.Errorf("TODO #%d worker is not a trusted member of %s", todo.ID, input.RepositoryPath)
		}
	}
	if plan, err := st.TodoWorktreePlan(ctx, todo.ID); err == nil {
		if !projectowner.SamePath(plan.RootPath, input.RepositoryPath) || (todo.WorkProjectPath != "" && !projectowner.SamePath(plan.WorktreePath, todo.WorkProjectPath)) {
			return input, Plan{}, fmt.Errorf("TODO #%d launch plan contradicts its repository or worker", todo.ID)
		}
		worker, err := resolver.ResolveRepository(ctx, plan.WorktreePath)
		if err != nil || worker.ProjectPath != input.RepositoryPath {
			return input, Plan{}, fmt.Errorf("TODO #%d planned worker is missing or not a trusted repository member", todo.ID)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return input, Plan{}, err
	}
	if !replay {
		revision, err := st.TodoOwnerRevision(ctx, todo.ID)
		if err != nil {
			return input, Plan{}, err
		}
		if revision != reviewRevision {
			return input, Plan{}, fmt.Errorf("TODO #%d changed during repair inspection; review again", todo.ID)
		}
		if input.ExpectedRevision != "" && input.ExpectedRevision != revision {
			return input, Plan{}, fmt.Errorf("TODO #%d changed since review; prepare a new repair", todo.ID)
		}
		input.ExpectedRevision = revision
	}
	return input, Plan{OwnerPath: input.RepositoryPath, DestinationPath: todo.WorkProjectPath, Todo: todo, Replay: replay}, nil
}

func (p Plan) Preview(name control.CapabilityName) string {
	if name == control.CapabilityTodoAdd {
		return "Repository TODO owner: " + p.OwnerPath + "\nCreate one TODO in this backlog; no worktree or engineer session is started."
	}
	destination := p.DestinationPath
	if destination == "" {
		destination = "not yet fixed; a separate worker checkout is allocated after confirmation"
		if name == control.CapabilityTodoRepairOwner {
			destination = "none recorded; repair retains the current work links"
		}
	}
	effects := "Deliver to the worker; retain exact-session delivery guards."
	if name == control.CapabilityTodoCreateWorktreeAndStartEngineer {
		effects = "Create one repository TODO, create a separate worktree, start one engineer."
		if p.Todo.ID > 0 {
			effects = fmt.Sprintf("Use existing TODO #%d, create a separate worktree, start one engineer; no new TODO.", p.Todo.ID)
		}
	} else if name == control.CapabilityTodoRepairOwner {
		effects = fmt.Sprintf("Repair only TODO #%d owner and append its ordering; preserve metadata, attachments, checkout, provider, exact session, origin TODO and launch provenance.", p.Todo.ID)
	}
	return "Repository TODO owner: " + p.OwnerPath + "\nWorker destination: " + destination + "\n" + effects
}
