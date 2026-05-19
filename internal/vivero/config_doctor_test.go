package vivero

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeConfigDoctorFile(t *testing.T, yaml string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestConfigDoctorValidConfig(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
sources:
  app:
    path: .
services:
  web:
    source: app
    image: nginx:alpine
    port: 8080
agent:
  defaultPreviewService: web
  commonPages:
    home:
      service: web
      path: /
  smokeTests:
    - name: homepage
      service: web
      path: /
  qa:
    defaultScope: smoke
    scopes:
      - name: smoke
        pages: [home]
        flows:
          - name: homepage
            start: home
            steps:
              - visit: home
        checks:
          - name: no-console-errors
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ConfigDoctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Errors != 0 || len(report.Findings) != 0 {
		t.Fatalf("expected clean report: %#v", report)
	}
	if report.Project != "demo" || report.Path == "" {
		t.Fatalf("missing project/path: %#v", report)
	}
}

func TestConfigDoctorReportsCrossReferenceFindings(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
sources:
  app:
    path: .
services:
  web:
    source: missing
    image: nginx:alpine
    port: 8080
  api:
    image: alpine:latest
    port: 8081
agent:
  defaultPreviewService: ghost
  commonPages:
    home:
      service: nope
      path: home
  smokeTests:
    - name: smoke
      service: nope
      path: health
  qa:
    defaultScope: regression
    scopes:
      - name: smoke
        pages: [missingPage]
        flows:
          - name: flow
            start: missingPage
            service: nope
            steps:
              - visit: missingPage
        checks:
          - method: browser-console
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ConfigDoctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Errors == 0 {
		t.Fatalf("expected failing report: %#v", report)
	}
	codes := map[string]bool{}
	for _, finding := range report.Findings {
		codes[finding.Code] = true
	}
	for _, want := range []string{"unknown-source", "unknown-service", "unknown-page", "unknown-qa-scope", "qa-check-name-missing"} {
		if !codes[want] {
			t.Fatalf("missing finding %s in %#v", want, report.Findings)
		}
	}
}

func TestConfigDoctorReturnsLoadErrorAsReport(t *testing.T) {
	root := writeConfigDoctorFile(t, `project: {}`)
	a := &App{Home: t.TempDir()}
	report, err := a.ConfigDoctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Errors != 1 || report.Findings[0].Code != "config-load" {
		t.Fatalf("expected config-load report: %#v", report)
	}
}

func TestRunDoctorConfigJSONExitCode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	root := writeConfigDoctorFile(t, `project: {}`)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor", "config", root, "--json", "--no-input"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 for invalid config, got %d stderr=%s", code, stderr.String())
	}
	var payload struct {
		Report ConfigDoctorReport `json:"configDoctor"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v stdout=%s", err, stdout.String())
	}
	if payload.Report.OK || payload.Report.Errors != 1 {
		t.Fatalf("unexpected report: %#v", payload.Report)
	}
}
