package projectbrief

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/store"
)

type Reader interface {
	ListProjectBriefs(context.Context, []string) ([]store.ProjectBriefRow, error)
	HasNewerProjectBriefSession(context.Context, string, time.Time, model.SessionSource, string) (bool, error)
}

type savedBrief struct {
	Text      model.ProjectBriefText
	Provider  model.SessionSource
	SessionID string
	Family    string
	Git       *gitState
	Answered  *bool
}

func familyPath(project model.ProjectSummary) string {
	if project.WorktreeRootPath != "" && project.WorktreeKind == model.WorktreeKindLinked {
		return project.WorktreeRootPath
	}
	return project.Path
}

var briefReadWarning, briefPayloadWarning sync.Once

// LogReadError warns once per process so polling an unavailable brief store
// does not flood the log. Briefs are optional on every read surface.
func LogReadError(err error) {
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		briefReadWarning.Do(func() { log.Printf("WARN project briefs: %v", err) })
	}
}

// Resolve prefers a project's own brief, then the newest eligible family brief.
// The caller filters both recipients and authors before any briefs are read.
func Resolve(ctx context.Context, reader Reader, eligible []model.ProjectSummary, requested []string) map[string]*model.ProjectBrief {
	projects := make(map[string]model.ProjectSummary, len(eligible))
	for _, project := range eligible {
		projects[project.Path] = project
	}
	families := make(map[string]bool, len(requested))
	for _, path := range requested {
		if project, allowed := projects[path]; allowed {
			families[familyPath(project)] = true
		}
	}
	var candidates []string
	for _, project := range eligible {
		if families[familyPath(project)] {
			candidates = append(candidates, project.Path)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	rows, err := reader.ListProjectBriefs(ctx, candidates)
	if err != nil {
		LogReadError(fmt.Errorf("list briefs: %w", err))
		return nil
	}
	type selectedBrief struct {
		row   store.ProjectBriefRow
		saved savedBrief
	}
	byCheckout := make(map[string]selectedBrief)
	newestByFamily := make(map[string]string)
	for _, row := range rows {
		family := familyPath(projects[row.CheckoutPath])
		var saved savedBrief
		err := json.Unmarshal(row.Payload, &saved)
		if err == nil {
			err = validateText(&saved.Text)
		}
		if err != nil {
			briefPayloadWarning.Do(func() { log.Printf("WARN project briefs: skipping an unreadable payload: %v", err) })
			continue
		}
		if saved.Family != family {
			continue
		}
		byCheckout[row.CheckoutPath] = selectedBrief{row: row, saved: saved}
		if newestByFamily[family] == "" {
			newestByFamily[family] = row.CheckoutPath
		}
	}

	gitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	byPath := make(map[string]*model.ProjectBrief)
	verified := make(map[string]*model.ProjectBrief)
	for _, path := range requested {
		project, allowed := projects[path]
		if !allowed {
			continue
		}
		entry, found := byCheckout[path]
		if !found {
			entry, found = byCheckout[newestByFamily[familyPath(project)]]
		}
		if !found {
			continue
		}
		author := entry.row.CheckoutPath
		brief, checked := verified[author]
		if !checked {
			brief, err = projectBrief(ctx, gitCtx, reader, entry.row, entry.saved, projects)
			if err != nil {
				LogReadError(fmt.Errorf("check session freshness: %w", err))
			}
			verified[author] = brief
		}
		if brief != nil {
			byPath[path] = brief
		}
	}
	return byPath
}

func projectBrief(ctx, gitCtx context.Context, reader Reader, row store.ProjectBriefRow, saved savedBrief, eligible map[string]model.ProjectSummary) (*model.ProjectBrief, error) {
	brief := model.ProjectBrief{
		ProjectBriefText: saved.Text, CheckoutPath: row.CheckoutPath, Provider: saved.Provider,
		RecordedAt: row.RecordedAt, NeedsStatus: "none",
	}
	newer, err := reader.HasNewerProjectBriefSession(ctx, row.CheckoutPath, row.RecordedAt, saved.Provider, saved.SessionID)
	if err != nil {
		return nil, err
	}
	brief.NewerSession = newer
	if brief.Needs != nil {
		brief.NeedsStatus = "open"
		if brief.Needs.Check != nil {
			brief.NeedsStatus = "unverified"
		}
	}
	if saved.Git == nil || gitCtx.Err() != nil {
		return &brief, nil
	}

	checkoutCtx, cancel := context.WithTimeout(gitCtx, gitCheckTimeout)
	defer cancel()
	checkout := row.CheckoutPath
	current := readGitState(checkoutCtx, checkout, saved.Family)
	if current != nil {
		changed := *current != *saved.Git
		brief.GitChanged = &changed
	} else {
		// If the author checkout disappeared, its local refs can still be
		// verified from the family root if that root is visible to this reader.
		if _, err := os.Stat(checkout); errors.Is(err, os.ErrNotExist) {
			changed := true
			brief.GitChanged = &changed
		}
		checkout = saved.Family
		if _, allowed := eligible[checkout]; !allowed || !inGitFamily(checkoutCtx, checkout, saved.Family) {
			return &brief, nil
		}
	}
	if brief.Needs != nil && brief.Needs.Check != nil {
		answer, err := checkDecision(checkoutCtx, checkout, *saved.Git, *brief.Needs.Check)
		if err == nil {
			answered := answer != ""
			if saved.Answered != nil && *saved.Answered != answered {
				changed := true
				brief.GitChanged = &changed
			}
			brief.NeedsStatus = "open"
			if answered {
				brief.NeedsStatus, brief.Answer, brief.Needs = "answered", answer, nil
			}
		}
	}
	return &brief, nil
}
