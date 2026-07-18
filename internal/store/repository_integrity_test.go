package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestRepositoryRootPolicyRoundTripAndAcknowledgement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	rootPath := filepath.Join(t.TempDir(), "repo")
	policy := model.RepositoryRootPolicy{
		RootPath:             rootPath,
		ExpectedBranch:       "master",
		ExpectedBranchSource: "worktree_creation",
		Mode:                 model.RepositoryIntegrityModeWarn,
		UpdatedAt:            time.Unix(1_700_000_000, 0),
	}
	if err := st.UpsertRepositoryRootPolicy(ctx, policy); err != nil {
		t.Fatalf("UpsertRepositoryRootPolicy() error = %v", err)
	}
	if err := st.SetRepositoryRootAcknowledgedFingerprint(ctx, rootPath, "fingerprint-1"); err != nil {
		t.Fatalf("SetRepositoryRootAcknowledgedFingerprint() error = %v", err)
	}

	got, err := st.GetRepositoryRootPolicy(ctx, rootPath)
	if err != nil {
		t.Fatalf("GetRepositoryRootPolicy() error = %v", err)
	}
	if got.ExpectedBranch != "master" || got.ExpectedBranchSource != "worktree_creation" {
		t.Fatalf("policy branch metadata = (%q, %q), want (master, worktree_creation)", got.ExpectedBranch, got.ExpectedBranchSource)
	}
	if got.Mode != model.RepositoryIntegrityModeWarn {
		t.Fatalf("policy mode = %q, want warn", got.Mode)
	}
	if got.AcknowledgedFingerprint != "fingerprint-1" {
		t.Fatalf("acknowledged fingerprint = %q, want fingerprint-1", got.AcknowledgedFingerprint)
	}

	policies, err := st.ListRepositoryRootPolicies(ctx)
	if err != nil {
		t.Fatalf("ListRepositoryRootPolicies() error = %v", err)
	}
	if _, ok := policies[filepath.Clean(rootPath)]; !ok {
		t.Fatalf("policy map = %#v, want root %q", policies, rootPath)
	}
}

func TestListRecentEventsByTypeForProjectFiltersAndOrders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	for _, event := range []model.StoredEvent{
		{At: time.Unix(1, 0), ProjectPath: "/repo", Type: "workspace", Payload: "one"},
		{At: time.Unix(2, 0), ProjectPath: "/other", Type: "workspace", Payload: "other"},
		{At: time.Unix(3, 0), ProjectPath: "/repo", Type: "other", Payload: "wrong-type"},
		{At: time.Unix(4, 0), ProjectPath: "/repo", Type: "workspace", Payload: "two"},
	} {
		if err := st.AddEvent(ctx, event); err != nil {
			t.Fatalf("AddEvent(%q) error = %v", event.Payload, err)
		}
	}
	got, err := st.ListRecentEventsByTypeForProject(ctx, "workspace", "/repo", 10)
	if err != nil {
		t.Fatalf("ListRecentEventsByTypeForProject() error = %v", err)
	}
	if len(got) != 2 || got[0].Payload != "two" || got[1].Payload != "one" {
		t.Fatalf("events = %#v, want newest matching project events first", got)
	}
}
