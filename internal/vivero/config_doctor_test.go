package vivero

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestConfigDoctorReportsInvalidBuildCacheSpecAsConfigLoad(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
services:
  web:
    image: alpine:latest
    build:
      cache:
        from:
          - type=local,src=/tmp/vivero-build-cache
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ConfigDoctor(root)
	if err != nil {
		t.Fatal(err)
	}
	finding, ok := configDoctorFinding(report, "config-load")
	if !ok || report.OK {
		t.Fatalf("expected config-load finding for invalid cache spec: %#v", report)
	}
	if !strings.Contains(finding.Message, "services.web.build.cache.from[0]") || !strings.Contains(finding.Message, "must be relative") {
		t.Fatalf("cache spec finding should include config path and reason: %#v", finding)
	}
}

func TestConfigDoctorReportsUnavailableBuildxForCacheConfig(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("FAKE_DOCKER_BUILDX_FAIL", "buildx plugin missing")
	root := writeConfigDoctorFile(t, `project:
  name: demo
services:
  web:
    image: alpine:latest
    build:
      cache:
        enabled: true
        from:
          - type=local,src=.vivero/cache/build/web
        to:
          - type=local,dest=.vivero/cache/build/web,mode=max
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ConfigDoctor(root)
	if err != nil {
		t.Fatal(err)
	}
	finding, ok := configDoctorFinding(report, "build-cache-buildx-unavailable")
	if !ok || report.OK {
		t.Fatalf("expected unavailable buildx finding for cache config: %#v", report)
	}
	if finding.Path != "services.web.build.cache" || !strings.Contains(finding.Message, "buildx plugin missing") {
		t.Fatalf("unexpected buildx finding: %#v", finding)
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

func TestConfigDoctorReportsUnsupportedConfigKeyWithLocation(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
services:
  web:
    image: nginx:alpine
    dockerfileInline: |
      FROM nginx:alpine
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ConfigDoctor(root)
	if err != nil {
		t.Fatal(err)
	}
	finding, ok := configDoctorFinding(report, "unsupported-config-key")
	if !ok {
		t.Fatalf("missing unsupported-config-key finding: %#v", report.Findings)
	}
	if finding.Severity != "error" || finding.Path != "services.web.dockerfileInline" || finding.Line == 0 || finding.Column == 0 {
		t.Fatalf("unsupported key should include severity/path/location: %#v", finding)
	}
	if !strings.Contains(finding.Suggestion, "build.dockerfile") {
		t.Fatalf("unsupported key should point at replacement, got %#v", finding)
	}
}

func TestConfigDoctorWarnsOnUnknownSchemaKeyWithLocation(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
services:
  web:
    image: nginx:alpine
    port: 8080
    portsTypo:
      http:
        container: 8080
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ConfigDoctor(root)
	if err != nil {
		t.Fatal(err)
	}
	finding, ok := configDoctorFinding(report, "unknown-config-key")
	if !ok {
		t.Fatalf("missing unknown-config-key finding: %#v", report.Findings)
	}
	if finding.Severity != "warning" || finding.Path != "services.web.portsTypo" || finding.Line == 0 || finding.Column == 0 {
		t.Fatalf("unknown key should include severity/path/location: %#v", finding)
	}
	if !strings.Contains(finding.Suggestion, "ports") {
		t.Fatalf("unknown key should suggest nearby schema key, got %#v", finding)
	}
}

func TestSchemaDoctorConfigDescribesFindingContract(t *testing.T) {
	code, stdout, stderr := runCLITestCommand(t, t.TempDir(), "schema", "doctor", "config", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("schema doctor config exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		Schema struct {
			FindingFields []string `json:"findingFields"`
			FindingCodes  []string `json:"findingCodes"`
		} `json:"schema"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid schema JSON: %v stdout=%s", err, stdout)
	}
	for _, want := range []string{"severity", "code", "path", "line", "column", "message", "suggestion", "docs"} {
		if !stringSliceContains(payload.Schema.FindingFields, want) {
			t.Fatalf("doctor config schema missing finding field %q: %#v", want, payload.Schema.FindingFields)
		}
	}
	for _, want := range []string{"unsupported-config-key", "unknown-config-key", "config-load"} {
		if !stringSliceContains(payload.Schema.FindingCodes, want) {
			t.Fatalf("doctor config schema missing finding code %q: %#v", want, payload.Schema.FindingCodes)
		}
	}
}

func configDoctorFinding(report ConfigDoctorReport, code string) (ConfigDoctorFinding, bool) {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return finding, true
		}
	}
	return ConfigDoctorFinding{}, false
}
