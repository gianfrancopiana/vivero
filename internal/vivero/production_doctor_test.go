package vivero

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProductionDoctorBlocksPreviewOnlyConfig(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
sources:
  app:
    path: .
public:
  provider: cloudflare
  mode: quick-tunnel
services:
  web:
    source: app
    image: demo:latest
    public: true
    port: 3000
    health:
      path: /
    env:
      API_TOKEN: supersecret
    dependencyVolumes:
      - name: uploads
        target: /data/uploads
        lifetime: project
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ProductionDoctorForEnvironment(root, "production")
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Verdict != "blocked" {
		t.Fatalf("expected blocked report: %#v", report)
	}
	codes := productionDiagnosticCodes(report.Diagnostics)
	for _, want := range []string{"deploy-surface", "mutable-source", "quick-tunnel-production", "image-not-immutable", "resource-limits-missing", "health-timeout-missing", "inline-secret", "backup-policy-missing"} {
		if !codes[want] {
			t.Fatalf("missing diagnostic %s in %#v", want, report.Diagnostics)
		}
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "supersecret") {
		t.Fatalf("diagnostics leaked an inline secret: %s", string(encoded))
	}
}

func TestProductionDoctorCandidateKeepsProductionHostingCapabilityOff(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
public:
  provider: cloudflare
  mode: named-tunnel
  baseDomain: preview.example.com
services:
  web:
    image: registry.example.com/demo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    public: true
    port: 3000
    resources:
      cpus: "1"
      memory: 512m
    health:
      path: /
      timeout: 30s
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ProductionDoctorForEnvironment(root, "production")
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Verdict != "candidate" {
		t.Fatalf("expected production candidate report: %#v", report)
	}
	codes := productionDiagnosticCodes(report.Diagnostics)
	if !codes["deploy-surface"] {
		t.Fatalf("expected deploy-surface guardrail diagnostic: %#v", report.Diagnostics)
	}
	for _, blocked := range []string{"mutable-source", "quick-tunnel-production", "image-not-immutable", "resource-limits-missing", "health-timeout-missing"} {
		if codes[blocked] {
			t.Fatalf("unexpected blocking/warning diagnostic %s in %#v", blocked, report.Diagnostics)
		}
	}
	features := stringSet(a.capabilities()["features"].([]string))
	if !features["preview-runtime"] || !features["production-readiness-doctor"] || !features["app-owned-deploy-surface"] {
		t.Fatalf("capabilities missing production surface features: %#v", features)
	}
	if features["production-hosting"] {
		t.Fatalf("capabilities must not claim production hosting: %#v", features)
	}
}

func TestProductionDoctorBlocksRemoteControlOverride(t *testing.T) {
	t.Setenv("VIVERO_ALLOW_REMOTE_CONTROL", "1")
	root := writeConfigDoctorFile(t, `project:
  name: demo
services:
  web:
    image: registry.example.com/demo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    port: 3000
    resources:
      cpus: "1"
      memory: 512m
    health:
      path: /
      timeout: 30s
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ProductionDoctorForEnvironment(root, "production")
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Verdict != "blocked" {
		t.Fatalf("expected remote control-plane override to block production readiness: %#v", report)
	}
	if !productionDiagnosticCodes(report.Diagnostics)["remote-control-plane"] {
		t.Fatalf("missing remote-control-plane diagnostic: %#v", report.Diagnostics)
	}
}

func TestProductionDoctorBlocksInvalidNamedPublicRoute(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
public:
  provider: cloudflare
  mode: named-tunnel
services:
  web:
    image: registry.example.com/demo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    public: true
    port: 3000
    resources:
      cpus: "1"
      memory: 512m
    health:
      path: /
      timeout: 30s
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ProductionDoctorForEnvironment(root, "production")
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Verdict != "blocked" {
		t.Fatalf("expected invalid named public route to block production readiness: %#v", report)
	}
	if !productionDiagnosticCodes(report.Diagnostics)["public-route-invalid"] {
		t.Fatalf("missing public-route-invalid diagnostic: %#v", report.Diagnostics)
	}
}

func TestProductionDoctorBlocksDuplicateNamedPublicRoutes(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
public:
  provider: cloudflare
  mode: named-tunnel
  baseDomain: preview.example.com
  hostname: staging.preview.example.com
services:
  web:
    image: registry.example.com/web@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    public: true
    port: 3000
    resources:
      cpus: "1"
      memory: 512m
    health:
      path: /
      timeout: 30s
  api:
    image: registry.example.com/api@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    public: true
    port: 3001
    resources:
      cpus: "1"
      memory: 512m
    health:
      path: /
      timeout: 30s
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ProductionDoctorForEnvironment(root, "production")
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Verdict != "blocked" {
		t.Fatalf("expected duplicate named public routes to block production readiness: %#v", report)
	}
	if !productionDiagnosticCodes(report.Diagnostics)["public-route-invalid"] {
		t.Fatalf("missing public-route-invalid diagnostic: %#v", report.Diagnostics)
	}
}

func TestProductionDoctorFlagsBackingServiceReadiness(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
services:
  web:
    image: registry.example.com/web@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    port: 3000
    resources:
      cpus: "1"
      memory: 512m
    health:
      path: /
      timeout: 30s
backingServices:
  db:
    image: postgres:16
    env:
      POSTGRES_PASSWORD: plain-password-value
      VAULT_TOKEN: vault://secret/db/token
    dependencyVolumes:
      - name: pgdata
        target: /var/lib/postgresql/data
        lifetime: project
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ProductionDoctorForEnvironment(root, "production")
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Verdict != "candidate" {
		t.Fatalf("backing warnings should keep report a candidate: %#v", report)
	}
	codes := productionDiagnosticCodes(report.Diagnostics)
	for _, want := range []string{"image-not-immutable", "resource-limits-missing", "health-timeout-missing", "inline-secret", "backup-policy-missing"} {
		if !codes[want] {
			t.Fatalf("missing backing diagnostic %s in %#v", want, report.Diagnostics)
		}
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "plain-password-value") || strings.Contains(string(encoded), "vault://secret") {
		t.Fatalf("diagnostics leaked backing secret values: %s", string(encoded))
	}
}

func TestDoctorProjectPathParsesFlagsAndPositionals(t *testing.T) {
	if got := doctorProjectPath([]string{"--production", "--project", "/tmp/app"}); got != "/tmp/app" {
		t.Fatalf("doctorProjectPath --project = %q", got)
	}
	if got := doctorProjectPath([]string{"--production", "./app"}); got != "./app" {
		t.Fatalf("doctorProjectPath positional = %q", got)
	}
	if got := doctorProjectPath(nil); got != "." {
		t.Fatalf("doctorProjectPath default = %q", got)
	}
}

func TestRunDoctorProductionJSONExitCode(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	root := writeConfigDoctorFile(t, `project:
  name: demo
sources:
  app:
    path: .
services:
  web:
    source: app
    image: demo:latest
    port: 3000
`)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor", "production", "--project", root, "--json", "--no-input"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 for blocked production report, got %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var payload struct {
		Report ProductionDoctorResult `json:"productionDoctor"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v stdout=%s", err, stdout.String())
	}
	if payload.Report.OK || payload.Report.Verdict != "blocked" {
		t.Fatalf("unexpected report: %#v", payload.Report)
	}
}

func productionDiagnosticCodes(diags []ProductionDoctorDiagnostic) map[string]bool {
	codes := map[string]bool{}
	for _, diag := range diags {
		codes[diag.Code] = true
	}
	return codes
}
