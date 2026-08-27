package agentquery

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"lcroom/internal/demorecord"
)

type DemoRecordingPathGrantBasis string

const (
	DemoRecordingGrantExplicitAttachment   DemoRecordingPathGrantBasis = "explicit_recording_attachment"
	DemoRecordingGrantOperatorConfirmation DemoRecordingPathGrantBasis = "operator_confirmation"
)

// DemoRecordingPathGrant is host-provided authority. Query arguments cannot
// create a grant: an adapter must bind an explicit attachment or a completed
// operator-confirmation decision while constructing the executor.
type DemoRecordingPathGrant struct {
	RecordingID string
	PackagePath string
	Basis       DemoRecordingPathGrantBasis
}

func normalizeDemoRecordingPathGrants(grants []DemoRecordingPathGrant) ([]DemoRecordingPathGrant, error) {
	out := make([]DemoRecordingPathGrant, 0, len(grants))
	for _, grant := range grants {
		grant.RecordingID = strings.TrimSpace(grant.RecordingID)
		grant.PackagePath = strings.TrimSpace(grant.PackagePath)
		if grant.PackagePath != "" {
			path, err := demorecord.NormalizeRecordingPath(grant.PackagePath)
			if err != nil {
				return nil, err
			}
			grant.PackagePath = path
		}
		if grant.RecordingID == "" && grant.PackagePath == "" {
			return nil, errors.New("demo recording path grant requires a recording id or package path")
		}
		switch grant.Basis {
		case DemoRecordingGrantExplicitAttachment, DemoRecordingGrantOperatorConfirmation:
		default:
			return nil, errors.New("demo recording path grant requires explicit attachment or operator-confirmation authority")
		}
		out = append(out, grant)
	}
	return out, nil
}

func (e *Executor) demoRecordingLatest(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args struct{}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	result := map[string]any{
		"found":               false,
		"discovery_available": e.demoRecordings != nil,
		"path_disclosure": map[string]any{
			"authorized":  false,
			"basis":       "withheld",
			"requirement": "Package path requires portfolio scope, an explicit recording attachment, or operator confirmation.",
		},
	}
	if e.demoRecordings == nil {
		result["message"] = "Demo recording discovery is not connected to this LCR query host."
		return result, nil
	}
	recording, found, err := e.demoRecordings.Latest(ctx)
	if err != nil {
		return nil, err
	}
	if !found {
		result["message"] = "No active or finalized LCR demo recording was found."
		return result, nil
	}

	authorized, basis := e.demoRecordingPathAuthorized(recording)
	resource := map[string]any{
		"id":             recording.ID,
		"resource_uri":   "lcr://demo-recordings/" + recording.ID,
		"resource_kind":  "lcr_demo_recording_package",
		"status":         recording.Status,
		"format_version": recording.FormatVersion,
		"started_at":     formatTime(recording.StartedAt),
		"duration_ms":    recording.DurationMS,
		"frame_count":    recording.FrameCount,
		"dropped_frames": recording.DroppedFrames,
		"updated_at":     formatTime(recording.UpdatedAt),
	}
	if !recording.CompletedAt.IsZero() {
		resource["completed_at"] = formatTime(recording.CompletedAt)
	}
	if authorized {
		resource["package_path"] = recording.PackagePath
		result["path_disclosure"] = map[string]any{
			"authorized": true,
			"basis":      basis,
		}
	}
	if association, ok := e.visibleDemoRecordingAssociation(ctx, recording.Association, authorized); ok {
		resource["association"] = association
	}
	result["found"] = true
	result["recording"] = resource
	return result, nil
}

func (e *Executor) demoRecordingPathAuthorized(recording demorecord.Resource) (bool, string) {
	if ScopeAllows(e.scope, ScopePortfolio) {
		return true, "portfolio_scope"
	}
	for _, grant := range e.recordingGrants {
		idMatches := grant.RecordingID == "" || grant.RecordingID == recording.ID
		pathMatches := grant.PackagePath == "" || filepath.Clean(grant.PackagePath) == filepath.Clean(recording.PackagePath)
		if idMatches && pathMatches {
			return true, string(grant.Basis)
		}
	}
	return false, "withheld"
}

func (e *Executor) visibleDemoRecordingAssociation(ctx context.Context, association demorecord.Association, pathAuthorized bool) (map[string]any, bool) {
	association = association.Normalize()
	if association.ProjectPath == "" {
		if !pathAuthorized || (association.Provider == "" && association.SessionID == "") {
			return nil, false
		}
		return demoRecordingAssociationRecord(association, ""), true
	}
	if e.scope == ScopeProject && cleanPath(association.ProjectPath) != e.originProjectPath {
		return nil, false
	}
	project, err := e.reader.GetProjectSummary(ctx, association.ProjectPath, true)
	if err != nil || !e.projectVisible(project) {
		return nil, false
	}
	return demoRecordingAssociationRecord(association, project.Name), true
}

func demoRecordingAssociationRecord(association demorecord.Association, projectName string) map[string]any {
	record := map[string]any{}
	if association.ProjectPath != "" {
		record["project_path"] = association.ProjectPath
	}
	if strings.TrimSpace(projectName) != "" {
		record["project_name"] = strings.TrimSpace(projectName)
	}
	if association.Provider != "" {
		record["provider"] = association.Provider
	}
	if association.SessionID != "" {
		record["session_id"] = association.SessionID
	}
	return record
}
