package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lcroom/internal/selfupdate"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type fakeSelfUpdateManager struct {
	checkResult   selfupdate.CheckResult
	checkErr      error
	installResult selfupdate.InstallResult
	installErr    error
	checkCalls    int
	installCalls  int
}

func (f *fakeSelfUpdateManager) Check(context.Context, bool) (selfupdate.CheckResult, error) {
	f.checkCalls++
	return f.checkResult, f.checkErr
}

func (f *fakeSelfUpdateManager) Install(context.Context, selfupdate.Release) (selfupdate.InstallResult, error) {
	f.installCalls++
	return f.installResult, f.installErr
}

func TestSelfUpdateCheckShowsPersistentBadgeWithoutOpeningDialog(t *testing.T) {
	release := testSelfUpdateRelease()
	m := Model{width: 140, availableSelfUpdate: nil}
	updated, _ := m.Update(selfUpdateCheckMsg{result: selfupdate.CheckResult{
		Supported:      true,
		CurrentVersion: "v1.0.0",
		Release:        &release,
	}})
	got := normalizeUpdateModel(updated)
	if got.selfUpdateDialog != nil {
		t.Fatal("automatic update check should not interrupt with a dialog")
	}
	if got.availableSelfUpdate == nil || got.availableSelfUpdate.Version != "v1.1.0" {
		t.Fatalf("available update = %#v, want v1.1.0", got.availableSelfUpdate)
	}
	rendered := ansi.Strip(got.renderTopStatusLine(140))
	if !strings.Contains(rendered, "/update v1.1.0") {
		t.Fatalf("top status = %q, want compact update command and version", rendered)
	}
}

func TestSelfUpdateRequiresHighlightedConfirmationThenRestarts(t *testing.T) {
	release := testSelfUpdateRelease()
	fake := &fakeSelfUpdateManager{installResult: selfupdate.InstallResult{
		Version:    release.Version,
		InstallDir: "/tmp/bin",
	}}
	m := Model{
		ctx:                 context.Background(),
		selfUpdater:         fake,
		selfUpdateStatus:    selfupdate.CheckResult{Supported: true, CurrentVersion: "v1.0.0", InstallPath: "/tmp/bin/lcroom"},
		availableSelfUpdate: &release,
	}
	opened, cmd := m.openSelfUpdateDialog()
	if cmd != nil {
		t.Fatal("cached release should open without another check")
	}
	got := normalizeUpdateModel(opened)
	if got.selfUpdateDialog == nil || got.selfUpdateDialog.Selected != selfUpdateDialogFocusLater {
		t.Fatalf("dialog = %#v, want Later selected by default", got.selfUpdateDialog)
	}

	dismissed, dismissCmd := got.updateSelfUpdateDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	if dismissCmd != nil || normalizeUpdateModel(dismissed).selfUpdateDialog != nil || fake.installCalls != 0 {
		t.Fatal("Enter on the default Later action should close without installing")
	}

	reopened, _ := m.openSelfUpdateDialog()
	got = normalizeUpdateModel(reopened)
	gotModel, _ := got.updateSelfUpdateDialogMode(tea.KeyMsg{Type: tea.KeyTab})
	got = normalizeUpdateModel(gotModel)
	installing, installCmd := got.updateSelfUpdateDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = normalizeUpdateModel(installing)
	if installCmd == nil || got.selfUpdateDialog.Phase != selfUpdateDialogInstalling || !got.selfUpdateInstallInFlight {
		t.Fatalf("confirmed dialog = %#v, cmd=%v", got.selfUpdateDialog, installCmd)
	}
	if fake.installCalls != 0 {
		t.Fatal("installation must run in tea.Cmd, not on the Update path")
	}

	installMsg := installCmd()
	if fake.installCalls != 1 {
		t.Fatalf("install calls = %d, want 1", fake.installCalls)
	}
	installed, shutdownCmd := got.Update(installMsg)
	got = normalizeUpdateModel(installed)
	if shutdownCmd == nil || !got.relaunchAfterUpdate || got.installedUpdate != "v1.1.0" || !got.gracefulQuitInFlight {
		t.Fatalf("installed model = relaunch:%t version:%q quitting:%t cmd:%v", got.relaunchAfterUpdate, got.installedUpdate, got.gracefulQuitInFlight, shutdownCmd)
	}
	if got.selfUpdateDialog != nil {
		t.Fatal("successful install should close the dialog before graceful shutdown")
	}
}

func TestSelfUpdateInstallErrorStaysOpenAndCanRetry(t *testing.T) {
	release := testSelfUpdateRelease()
	m := Model{selfUpdateInstallInFlight: true, selfUpdateDialog: &selfUpdateDialogState{
		Phase:   selfUpdateDialogInstalling,
		Release: &release,
	}}
	updated, _ := m.Update(selfUpdateInstallMsg{release: release, err: errors.New("target directory is read-only")})
	got := normalizeUpdateModel(updated)
	if got.selfUpdateDialog == nil || got.selfUpdateDialog.Phase != selfUpdateDialogError || !got.selfUpdateDialog.RetryInstall {
		t.Fatalf("error dialog = %#v, want retryable install error", got.selfUpdateDialog)
	}
	if got.selfUpdateInstallInFlight || got.relaunchAfterUpdate {
		t.Fatalf("failed install state = in-flight:%t relaunch:%t", got.selfUpdateInstallInFlight, got.relaunchAfterUpdate)
	}
}

func TestSelfUpdateDialogSanitizesRemoteReleaseNotes(t *testing.T) {
	release := testSelfUpdateRelease()
	release.Notes = "## Changes\n- safe\n\x1b]52;c;dGVzdA==\aunsafe control"
	m := Model{
		width:  100,
		height: 28,
		selfUpdateDialog: &selfUpdateDialogState{
			Phase:    selfUpdateDialogAvailable,
			Selected: selfUpdateDialogFocusLater,
			Result: selfupdate.CheckResult{
				CurrentVersion: "v1.0.0",
				InstallPath:    "/tmp/bin/lcroom",
			},
			Release: &release,
		},
	}
	rendered := m.renderSelfUpdateDialogOverlay("", 100, 26)
	visible := ansi.Strip(rendered)
	if strings.Contains(rendered, "\x1b]52") || strings.Contains(rendered, "\a") {
		t.Fatalf("dialog retained remote control sequence: %q", rendered)
	}
	for _, want := range []string{"Little Control Room Update", "v1.0.0", "v1.1.0", "Update & restart", "Later", "safe"} {
		if !strings.Contains(visible, want) {
			t.Fatalf("dialog missing %q:\n%s", want, visible)
		}
	}
}

func testSelfUpdateRelease() selfupdate.Release {
	return selfupdate.Release{
		Tag:         "v1.1.0",
		Version:     "v1.1.0",
		Notes:       "- Faster startup\n- Better project refresh",
		PublishedAt: time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC),
		Archive: selfupdate.Asset{
			Name: "lcroom_Linux_x86_64.tar.gz",
			Size: 15 << 20,
		},
	}
}
