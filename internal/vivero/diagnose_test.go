package vivero

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func insertStartupEvent(t *testing.T, a *App, previewID string, ts time.Time, level, typ, msg, service string, meta map[string]string) {
	t.Helper()
	_, err := a.db.Exec(`INSERT INTO preview_events(preview_id,timestamp,level,type,message,service_name,metadata_json) VALUES(?,?,?,?,?,?,?)`, previewID, ts.Format(time.RFC3339Nano), level, typ, msg, service, jsonString(meta))
	if err != nil {
		t.Fatal(err)
	}
}

func TestDiagnoseStartupSortsPhasesAndFindsSlowest(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	created := nowUTC().Add(-3 * time.Minute)
	if err := a.upsertPreview(PreviewRecord{ID: "demo-pr-17", Project: "demo", Status: "running", CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	insertStartupEvent(t, a, "demo-pr-17", created.Add(2*time.Second), "info", "image.built", "service image built", "web", map[string]string{"durationMs": "90000", "tag": "vivero/demo"})
	insertStartupEvent(t, a, "demo-pr-17", created.Add(1*time.Second), "info", "source.ready", "source ready", "app", map[string]string{"durationMs": "50", "path": "/tmp/app"})
	insertStartupEvent(t, a, "demo-pr-17", created.Add(3*time.Second), "info", "setup.afterSeeds", "setup command completed", "web", map[string]string{"durationMs": "45000", "command": "bin/setup"})

	diag, err := a.DiagnoseStartup("demo-pr-17")
	if err != nil {
		t.Fatal(err)
	}
	if diag.PreviewID != "demo-pr-17" || diag.Project != "demo" || diag.Status != "running" {
		t.Fatalf("unexpected diagnosis identity: %#v", diag)
	}
	if len(diag.Phases) != 3 {
		t.Fatalf("expected 3 phases, got %#v", diag.Phases)
	}
	if diag.Phases[0].Type != "source.ready" || diag.Phases[1].Type != "image.built" || diag.Phases[2].Type != "setup.afterSeeds" {
		t.Fatalf("phases not sorted by timestamp: %#v", diag.Phases)
	}
	if diag.SlowestPhase.Type != "image.built" || diag.SlowestPhase.DurationMs != 90000 {
		t.Fatalf("wrong slowest phase: %#v", diag.SlowestPhase)
	}
	if diag.TotalMs != 135050 {
		t.Fatalf("totalMs = %d", diag.TotalMs)
	}
	if len(diag.Recommendations) == 0 || !strings.Contains(diag.Recommendations[0], "image build") {
		t.Fatalf("missing image build recommendation: %#v", diag.Recommendations)
	}
}

func TestDiagnoseStartupHandlesLegacyAndFailureEvents(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	created := nowUTC().Add(-time.Minute)
	if err := a.upsertPreview(PreviewRecord{ID: "legacy-pr", Project: "demo", Status: "unhealthy", CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	insertStartupEvent(t, a, "legacy-pr", created.Add(time.Second), "info", "service.started", "container started", "web", nil)
	insertStartupEvent(t, a, "legacy-pr", created.Add(2*time.Second), "error", "setup.failed", "setup.afterSeeds[0] failed", "web", map[string]string{"durationMs": "2200", "command": "SECRET_TOKEN=abc bin/setup"})

	diag, err := a.DiagnoseStartup("legacy-pr")
	if err != nil {
		t.Fatal(err)
	}
	if len(diag.Phases) != 2 || diag.Phases[0].DurationMs != 0 {
		t.Fatalf("legacy event not preserved with zero duration: %#v", diag.Phases)
	}
	if diag.Failure == nil || diag.Failure.Type != "setup.failed" || diag.Failure.DurationMs != 2200 {
		t.Fatalf("failure not detected: %#v", diag.Failure)
	}
	if diag.Failure.Metadata["command"] != "[redacted]" {
		t.Fatalf("failure metadata should redact sensitive command: %#v", diag.Failure.Metadata)
	}
	human := startupDiagnosisHuman(diag)
	if strings.Contains(human, "SECRET_TOKEN") || strings.Contains(human, "abc") {
		t.Fatalf("human diagnosis leaked event metadata: %s", human)
	}
	if !strings.Contains(human, "failure: setup.failed web 2.2s") {
		t.Fatalf("human diagnosis should summarize failure: %s", human)
	}
}

func TestDiagnosticRecommendationsCoverKnownFailuresAndPhases(t *testing.T) {
	failures := map[string]string{
		"source.clone.failed": "source preparation failed",
		"image.build.failed":  "image build failed",
		"service.failed":      "service startup failed",
		"tunnel.failed":       "public tunnel failed",
		"other.failed":        "inspect the failure event",
	}
	for typ, want := range failures {
		if got := recommendationForFailure(typ); !strings.Contains(got, want) {
			t.Fatalf("recommendationForFailure(%q) = %q, want %q", typ, got, want)
		}
	}

	phases := map[string]string{
		"setup.before":  "split dependency install",
		"service.start": "inspect service startup",
		"tunnel.ready":  "public tunnel is bottleneck",
		"source.ready":  "source resolution is slow",
		"unknown.phase": "inspect the slowest phase",
	}
	for typ, want := range phases {
		if got := recommendationForPhase(typ); !strings.Contains(got, want) {
			t.Fatalf("recommendationForPhase(%q) = %q, want %q", typ, got, want)
		}
	}

	recs := appendRecommendation(nil, "inspect logs")
	recs = appendRecommendation(recs, "")
	recs = appendRecommendation(recs, "inspect logs")
	if len(recs) != 1 || recs[0] != "inspect logs" {
		t.Fatalf("appendRecommendation should skip empty and duplicate values: %#v", recs)
	}
}

func TestRunDiagnoseStartupJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	created := nowUTC().Add(-time.Minute)
	if err := a.upsertPreview(PreviewRecord{ID: "cli-pr", Project: "demo", Status: "running", CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	insertStartupEvent(t, a, "cli-pr", created.Add(time.Second), "info", "service.healthy", "health check passed", "web", map[string]string{"durationMs": "1200"})
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"diagnose", "startup", "cli-pr", "--json", "--no-input"}, &stdout, &stderr); code != 0 {
		t.Fatalf("Run diagnose exit = %d stderr=%s", code, stderr.String())
	}
	var payload struct {
		Diagnosis StartupDiagnosis `json:"diagnosis"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v stdout=%s", err, stdout.String())
	}
	if payload.Diagnosis.PreviewID != "cli-pr" || payload.Diagnosis.SlowestPhase.Type != "service.healthy" {
		t.Fatalf("unexpected JSON diagnosis: %#v", payload.Diagnosis)
	}
}
