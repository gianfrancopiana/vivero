package vivero

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicSetupWritesNamedTunnelConfigAndState(t *testing.T) {
	home := t.TempDir()
	projectDir := writePublicCLITestProject(t)

	code, stdout, stderr := runCLITestCommand(t, home,
		"public", "setup",
		"--project", projectDir,
		"--base-domain", "previews.example.com",
		"--tunnel", "vivero-preview",
		"--zone", "example.com",
		"--router-addr", "127.0.0.1:7777",
		"--json", "--no-input",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("public setup exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		PublicSetup PublicSetupResult `json:"publicSetup"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("public setup should return JSON: %v stdout=%s", err, stdout)
	}
	setup := payload.PublicSetup
	if !setup.OK || !setup.Written || setup.Project != "demo" || setup.BaseDomain != "previews.example.com" {
		t.Fatalf("unexpected setup payload: %#v", setup)
	}
	if setup.Tunnel != "vivero-preview" || setup.Zone != "example.com" || setup.Wildcard != "*.previews.example.com" || setup.RouterAddr != "127.0.0.1:7777" {
		t.Fatalf("setup should normalize durable public fields: %#v", setup)
	}
	if setup.StatePath == "" || setup.CloudflaredConfigPath == "" || len(setup.NextCommands) == 0 {
		t.Fatalf("setup should return state/config paths and next commands: %#v", setup)
	}

	_, cfg, err := loadProjectConfig(projectDir)
	if err != nil {
		t.Fatalf("updated config should load: %v", err)
	}
	if cfg.Public.Provider != "cloudflare" || cfg.Public.Mode != "named-tunnel" || cfg.Public.BaseDomain != "previews.example.com" {
		t.Fatalf("public config not written for named tunnel: %#v", cfg.Public)
	}
	if cfg.Public.Tunnel != "vivero-preview" || cfg.Public.Zone != "example.com" || cfg.Public.Wildcard != "*.previews.example.com" || cfg.Public.RouterAddr != "127.0.0.1:7777" {
		t.Fatalf("public config missing setup fields: %#v", cfg.Public)
	}
	if cfg.Public.HostnameTemplate != "{{ .PreviewID }}.{{ .BaseDomain }}" || cfg.Public.InactiveBehavior != "410" {
		t.Fatalf("public config should use durable defaults: %#v", cfg.Public)
	}

	for _, path := range []string{setup.StatePath, setup.CloudflaredConfigPath} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected setup artifact %s: %v", path, err)
		}
		if strings.Contains(strings.ToLower(string(body)), "token") || strings.Contains(strings.ToLower(string(body)), "password") {
			t.Fatalf("setup artifact should not store secrets: %s\n%s", path, string(body))
		}
	}
}

func TestPublicDoctorValidatesNamedTunnelSetup(t *testing.T) {
	home := t.TempDir()
	projectDir := writePublicCLITestProject(t)
	code, stdout, stderr := runCLITestCommand(t, home,
		"public", "setup",
		"--project", projectDir,
		"--base-domain", "previews.example.com",
		"--tunnel", "vivero-preview",
		"--zone", "example.com",
		"--json", "--no-input",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("public setup exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "public", "doctor", "--project", projectDir, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("public doctor exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		PublicDoctor PublicDoctorReport `json:"publicDoctor"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("public doctor should return JSON: %v stdout=%s", err, stdout)
	}
	report := payload.PublicDoctor
	if !report.OK || report.Project != "demo" || report.BaseDomain != "previews.example.com" || report.Tunnel != "vivero-preview" {
		t.Fatalf("unexpected public doctor report: %#v", report)
	}
	if report.Errors != 0 {
		t.Fatalf("public doctor should have no errors: %#v", report.Findings)
	}
	assertPublicFinding(t, report.Findings, "public-config-valid")
	assertPublicFinding(t, report.Findings, "public-setup-state")
	assertPublicFinding(t, report.Findings, "public-cloudflared-config")
}

func TestPublicStatusReportsNamedTunnelState(t *testing.T) {
	home := t.TempDir()
	projectDir := writePublicCLITestProject(t)
	code, stdout, stderr := runCLITestCommand(t, home,
		"public", "setup",
		"--project", projectDir,
		"--base-domain", "previews.example.com",
		"--tunnel", "vivero-preview",
		"--json", "--no-input",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("public setup exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "public", "status", "--project", projectDir, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("public status exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		PublicStatus PublicDoctorReport `json:"publicStatus"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("public status should return JSON: %v stdout=%s", err, stdout)
	}
	if !payload.PublicStatus.OK || payload.PublicStatus.Tunnel != "vivero-preview" || payload.PublicStatus.Wildcard != "*.previews.example.com" {
		t.Fatalf("unexpected public status report: %#v", payload.PublicStatus)
	}
}

func TestPublicStartDryRunReturnsRouterAndCloudflaredCommands(t *testing.T) {
	home := t.TempDir()
	projectDir := writePublicCLITestProject(t)
	code, stdout, stderr := runCLITestCommand(t, home,
		"public", "setup",
		"--project", projectDir,
		"--base-domain", "previews.example.com",
		"--tunnel", "vivero-preview",
		"--router-addr", "127.0.0.1:9999",
		"--json", "--no-input",
	)
	if code != 0 || stderr != "" {
		t.Fatalf("public setup exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "public", "start", "--project", projectDir, "--dry-run", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("public start --dry-run exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		PublicStart PublicStartResult `json:"publicStart"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("public start should return JSON: %v stdout=%s", err, stdout)
	}
	start := payload.PublicStart
	if !start.OK || !start.DryRun || start.Tunnel != "vivero-preview" || start.RouterAddr != "127.0.0.1:9999" {
		t.Fatalf("unexpected public start payload: %#v", start)
	}
	if got := strings.Join(start.RouterCommand, " "); got != "vivero serve --public-router --addr 127.0.0.1:9999" {
		t.Fatalf("router command = %q", got)
	}
	cloudflared := strings.Join(start.CloudflaredCommand, " ")
	if !strings.Contains(cloudflared, "cloudflared tunnel") || !strings.Contains(cloudflared, "run vivero-preview") || !strings.Contains(cloudflared, start.CloudflaredConfigPath) {
		t.Fatalf("cloudflared command should run named tunnel with generated config: %#v", start.CloudflaredCommand)
	}
	body, err := os.ReadFile(start.CloudflaredConfigPath)
	if err != nil {
		t.Fatalf("cloudflared config should exist: %v", err)
	}
	for _, want := range []string{"hostname: '*.previews.example.com'", "service: http://127.0.0.1:9999", "http_status:404"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("cloudflared config missing %q:\n%s", want, string(body))
		}
	}
}

func TestPublicDoctorRejectsEphemeralQuickTunnelConfig(t *testing.T) {
	home := t.TempDir()
	projectDir := writePublicCLITestProject(t)
	appendPublicConfig(t, projectDir, "provider: cloudflare\n  mode: quick-tunnel\n")

	code, stdout, stderr := runCLITestCommand(t, home, "public", "doctor", "--project", projectDir, "--json", "--no-input")
	if code == 0 || stderr != "" {
		t.Fatalf("public doctor should fail on quick-tunnel mode with JSON stdout, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		PublicDoctor PublicDoctorReport `json:"publicDoctor"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("public doctor stdout should be JSON: %v stdout=%s", err, stdout)
	}
	if payload.PublicDoctor.OK || payload.PublicDoctor.Errors == 0 {
		t.Fatalf("quick tunnel should not pass durable public doctor: %#v", payload.PublicDoctor)
	}
	assertPublicFinding(t, payload.PublicDoctor.Findings, "public-mode-not-durable")
}

func writePublicCLITestProject(t *testing.T) string {
	t.Helper()
	projectDir := t.TempDir()
	body := `project:
  name: demo

sources:
  app:
    mode: external
    path: .

services:
  web:
    source: app
    image: nginx:alpine
    port: 3000
    public: true
    health:
      path: /
      expectStatus: 200

agent:
  defaultPreviewService: web
`
	if err := os.WriteFile(filepath.Join(projectDir, "vivero.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return projectDir
}

func appendPublicConfig(t *testing.T, projectDir, publicBody string) {
	t.Helper()
	path := filepath.Join(projectDir, "vivero.yml")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("\npublic:\n  " + publicBody); err != nil {
		t.Fatal(err)
	}
}

func assertPublicFinding(t *testing.T, findings []PublicDoctorFinding, code string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Code == code {
			return
		}
	}
	t.Fatalf("missing public doctor finding %q in %#v", code, findings)
}
