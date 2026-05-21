package vivero

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalStateDoctorFindsStaleActivePreviewState(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.containers = &fakeContainerRuntime{}

	projectPath := filepath.Join(t.TempDir(), "missing-project")
	if _, err := a.saveProject(projectPath, ProjectConfig{Project: ProjectMeta{Name: "demo"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "stale-pr", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveSource("stale-pr", PreviewSource{Name: "app", Mode: "external", Path: filepath.Join(t.TempDir(), "missing-source"), Owned: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("stale-pr", PreviewService{Name: "web", Runtime: "docker", Status: "healthy", ContainerID: "missing-container"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("stale-pr", PreviewService{Name: "worker", Status: "running", PID: 99999999}); err != nil {
		t.Fatal(err)
	}

	report, err := a.LocalStateDoctor()
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Errors == 0 {
		t.Fatalf("stale active state should fail doctor: %#v", report)
	}
	codes := map[string]bool{}
	for _, finding := range report.Findings {
		codes[finding.Code] = true
	}
	for _, want := range []string{"project-path-missing", "source-path-missing", "container-missing", "pid-missing"} {
		if !codes[want] {
			t.Fatalf("missing finding code %s in %#v", want, report.Findings)
		}
	}
}

func TestLocalStateDoctorIgnoresDeadPreviewWithCleanedWorktree(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	projectPath := t.TempDir()
	if _, err := a.saveProject(projectPath, ProjectConfig{Project: ProjectMeta{Name: "demo"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "old-pr", Project: "demo", Status: "dead"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveSource("old-pr", PreviewSource{Name: "app", Mode: "worktree", Path: filepath.Join(t.TempDir(), "removed-worktree"), Owned: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("old-pr", PreviewService{Name: "web", Status: "dead"}); err != nil {
		t.Fatal(err)
	}

	report, err := a.LocalStateDoctor()
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Findings) != 0 {
		t.Fatalf("dead preview history should not be treated as stale active state: %#v", report)
	}
}

func TestDownDiscardToleratesMissingProjectDuringRecovery(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.containers = &fakeContainerRuntime{}
	if err := a.upsertPreview(PreviewRecord{ID: "orphan-pr", Project: "missing-project", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("orphan-pr", PreviewService{Name: "web", Runtime: "docker", Status: "running", ContainerID: "missing-container"}); err != nil {
		t.Fatal(err)
	}

	preview, err := a.Down("orphan-pr", "discard")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != "dead" || preview.Services["web"].Status != "dead" {
		t.Fatalf("orphan preview should be recoverable via down --discard: %#v", preview)
	}
}

func TestRunDoctorJSONReportsLocalStateFailures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveProject(t.TempDir(), ProjectConfig{Project: ProjectMeta{Name: "demo"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "stale-pr", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("stale-pr", PreviewService{Name: "worker", Status: "running", PID: 99999999}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLITestCommand(t, home, "doctor", "--json", "--no-input")
	if code == 0 || stderr != "" {
		t.Fatalf("doctor should fail via JSON stdout for stale local state, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		OK         bool                   `json:"ok"`
		LocalState LocalStateDoctorReport `json:"localState"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid doctor JSON: %v stdout=%s", err, stdout)
	}
	if payload.OK || payload.LocalState.OK || payload.LocalState.Errors == 0 {
		t.Fatalf("doctor JSON should expose localState failures: %#v", payload)
	}
	if !strings.Contains(stdout, "vivero down stale-pr --discard") {
		t.Fatalf("doctor should suggest a recovery command, stdout=%s", stdout)
	}
}
