package vivero

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFingerprintForPathsStableAndChangesWithContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package-lock.json"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "db", "migrate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "db", "migrate", "001.sql"), []byte("create table users"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := fingerprintForPaths(root, []string{"db/migrate", "package-lock.json"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fingerprintForPaths(root, []string{"package-lock.json", "db/migrate"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("fingerprint should be stable independent of path order: %q != %q", first, second)
	}
	if err := os.WriteFile(filepath.Join(root, "package-lock.json"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := fingerprintForPaths(root, []string{"db/migrate", "package-lock.json"})
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("fingerprint should change when selected file contents change")
	}
}

func TestFingerprintForPathsMissingIsStableAndUnsafeErrors(t *testing.T) {
	root := t.TempDir()
	first, err := fingerprintForPaths(root, []string{"missing.lock"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := fingerprintForPaths(root, []string{"missing.lock"})
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("missing paths should produce stable non-empty fingerprint: %q %q", first, second)
	}
	if _, err := fingerprintForPaths(root, []string{"../escape"}); err == nil {
		t.Fatal("unsafe fingerprint path should error")
	}
}

func TestRunSetupStepsOncePerFingerprintSkipsUntilFingerprintChanges(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "package-lock.json"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{
			"web": {
				Source:            "app",
				Image:             "alpine:latest",
				DependencyVolumes: []VolumeConfig{{Name: "node_modules", Target: "/node_modules", Lifetime: "project"}},
			},
		},
		Setup: SetupConfig{AfterSeeds: []SetupStep{{
			Service:     "web",
			Policy:      "once-per-fingerprint",
			Command:     RuntimeCommand{Shell: "printf x >> setup-count.txt"},
			Fingerprint: WarmFingerprintConfig{Paths: []string{"package-lock.json"}},
		}}},
	}
	sources := map[string]PreviewSource{"app": {Name: "app", Mode: "external", Path: source}}
	if err := a.runSetupSteps("first-pr", cfg.Setup.AfterSeeds, cfg, sources, warmRunState{}); err != nil {
		t.Fatal(err)
	}
	if err := a.runSetupSteps("second-pr", cfg.Setup.AfterSeeds, cfg, sources, warmRunState{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(source, "setup-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "x" {
		t.Fatalf("same fingerprint should skip setup, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(source, "package-lock.json"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.runSetupSteps("third-pr", cfg.Setup.AfterSeeds, cfg, sources, warmRunState{}); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(source, "setup-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "xx" {
		t.Fatalf("changed fingerprint should rerun setup, got %q", got)
	}
}

func TestRunSetupStepsOncePerFingerprintFallsBackToWarmPaths(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "Gemfile.lock"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Warm:    WarmConfig{Fingerprint: WarmFingerprintConfig{Paths: []string{"Gemfile.lock"}}},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "alpine:latest", DependencyVolumes: []VolumeConfig{{Name: "bundle", Target: "/bundle", Lifetime: "project"}}},
		},
		Setup: SetupConfig{AfterSeeds: []SetupStep{{Service: "web", Policy: "once-per-fingerprint", Command: RuntimeCommand{Shell: "printf x >> setup-count.txt"}}}},
	}
	sources := map[string]PreviewSource{"app": {Name: "app", Mode: "external", Path: source}}
	if err := a.runSetupSteps("first-pr", cfg.Setup.AfterSeeds, cfg, sources, warmRunState{}); err != nil {
		t.Fatal(err)
	}
	if err := a.runSetupSteps("second-pr", cfg.Setup.AfterSeeds, cfg, sources, warmRunState{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(source, "setup-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "x" {
		t.Fatalf("warm fingerprint fallback should skip setup, got %q", got)
	}
}

func TestRunSetupStepsOncePerFingerprintRequiresPersistentVolumeAndPaths(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "package-lock.json"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	sources := map[string]PreviewSource{"app": {Name: "app", Mode: "external", Path: source}}
	withoutVolume := ProjectConfig{
		Project:  ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{"web": {Source: "app", Image: "alpine:latest"}},
		Setup:    SetupConfig{AfterSeeds: []SetupStep{{Service: "web", Policy: "once-per-fingerprint", Command: RuntimeCommand{Shell: "printf x"}, Fingerprint: WarmFingerprintConfig{Paths: []string{"package-lock.json"}}}}},
	}
	if err := a.runSetupSteps("no-volume", withoutVolume.Setup.AfterSeeds, withoutVolume, sources, warmRunState{}); err == nil || !strings.Contains(err.Error(), "persistent dependency volume") {
		t.Fatalf("expected persistent volume error, got %v", err)
	}
	withoutPaths := ProjectConfig{
		Project:  ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{"web": {Source: "app", Image: "alpine:latest", DependencyVolumes: []VolumeConfig{{Name: "node_modules", Target: "/node_modules", Lifetime: "project"}}}},
		Setup:    SetupConfig{AfterSeeds: []SetupStep{{Service: "web", Policy: "once-per-fingerprint", Command: RuntimeCommand{Shell: "printf x"}}}},
	}
	if err := a.runSetupSteps("no-paths", withoutPaths.Setup.AfterSeeds, withoutPaths, sources, warmRunState{}); err == nil || !strings.Contains(err.Error(), "fingerprint paths") {
		t.Fatalf("expected fingerprint paths error, got %v", err)
	}
}

func TestConfigDoctorWarnsForUnsafeFingerprintSetupPolicy(t *testing.T) {
	root := t.TempDir()
	body := []byte(`project:
  name: demo
sources:
  app:
    path: .
services:
  web:
    source: app
    image: alpine:latest
setup:
  afterSeeds:
    - service: web
      policy: once-per-fingerprint
      command: npm install
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	report, err := a.ConfigDoctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !configDoctorHasFinding(report, "setup-fingerprint-paths-missing") {
		t.Fatalf("expected missing fingerprint paths warning: %#v", report.Findings)
	}
	if !configDoctorHasFinding(report, "setup-persistent-volume-missing") {
		t.Fatalf("expected missing persistent volume warning: %#v", report.Findings)
	}
}

func configDoctorHasFinding(report ConfigDoctorReport, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
