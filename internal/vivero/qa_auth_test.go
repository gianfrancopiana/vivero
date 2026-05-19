package vivero

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadProjectConfigParsesQAAuthSessions(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
agent:
  qa:
    auth:
      sessions:
        admin:
          storageState: .vivero/auth/admin.storage.json
          scopes: [authenticated]
          note: Use an operator-provided Playwright storage state file.
`)
	_, cfg, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	session := cfg.Agent.QA.Auth.Sessions["admin"]
	if session.StorageState != ".vivero/auth/admin.storage.json" {
		t.Fatalf("storage state = %q", session.StorageState)
	}
	if !reflect.DeepEqual(session.Scopes, []string{"authenticated"}) {
		t.Fatalf("scopes = %#v", session.Scopes)
	}
	if !strings.Contains(session.Note, "operator-provided") {
		t.Fatalf("note = %q", session.Note)
	}
}

func TestQAPlanIncludesApplicableAuthSessionAndEvidenceCommands(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	projectRoot := t.TempDir()
	storageState := writeProjectFile(t, projectRoot, ".vivero/auth/admin.storage.json", `{"cookies":[],"origins":[]}`)
	_, err = a.saveProject(projectRoot, ProjectConfig{
		Project:  ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{"web": {Port: 3000}},
		Agent: AgentConfig{
			DefaultPreviewService: "web",
			CommonPages: map[string]AgentPage{
				"dashboard": {Service: "web", Path: "/dashboard"},
			},
			QA: QAConfig{
				Auth: QAAuthConfig{Sessions: map[string]QAAuthSession{
					"admin": {StorageState: ".vivero/auth/admin.storage.json", Scopes: []string{"authenticated"}, Note: "operator provided"},
				}},
				Evidence: QAEvidenceConfig{
					Screenshots: QAScreenshotEvidenceConfig{ColorSchemes: []string{"light"}},
					Recordings:  QARecordingEvidenceConfig{ColorSchemes: []string{"dark"}},
				},
				Scopes: []QAScope{{Name: "authenticated", Pages: []string{"dashboard"}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "auth-pr", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("auth-pr", PreviewService{Name: "web", Status: "healthy", OriginURL: "http://127.0.0.1:3000"}); err != nil {
		t.Fatal(err)
	}

	plan, err := a.QAPlan("auth-pr", "authenticated")
	if err != nil {
		t.Fatal(err)
	}
	auth := plan["auth"].(map[string]any)
	sessions := auth["sessions"].(map[string]any)
	admin := sessions["admin"].(map[string]any)
	if admin["storageState"] != storageState || admin["exists"] != true {
		t.Fatalf("admin auth session = %#v, want storage state %s exists", admin, storageState)
	}
	scopes := plan["scopes"].([]map[string]any)
	if scopes[0]["authSession"] != "admin" {
		t.Fatalf("scope auth session = %#v", scopes[0])
	}

	evidence := plan["evidence"].(map[string]any)
	screenshots := evidence["screenshots"].(map[string]any)
	screenshotCommands := screenshots["commands"].([]map[string]any)
	shotArgv := screenshotCommands[0]["argv"].([]string)
	assertArgPair(t, shotArgv, "--storage-state", storageState)
	if screenshotCommands[0]["authSession"] != "admin" || screenshotCommands[0]["storageState"] != storageState {
		t.Fatalf("screenshot command missing auth context: %#v", screenshotCommands[0])
	}
	recordings := evidence["recordings"].(map[string]any)
	recordingCommands := recordings["commands"].([]map[string]any)
	recordArgv := recordingCommands[0]["argv"].([]string)
	assertArgPair(t, recordArgv, "--storage-state", storageState)
	if recordingCommands[0]["authSession"] != "admin" || recordingCommands[0]["storageState"] != storageState {
		t.Fatalf("recording command missing auth context: %#v", recordingCommands[0])
	}
}

func TestConfigDoctorWarnsOnMissingQAAuthStorageState(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
agent:
  qa:
    auth:
      sessions:
        admin:
          storageState: .vivero/auth/missing.storage.json
          scopes: [authenticated]
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ConfigDoctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Errors != 0 {
		t.Fatalf("missing storage state should be a warning-only report: %#v", report)
	}
	if !findingCode(report, "qa-auth-storage-state-missing") {
		t.Fatalf("missing qa auth storage state warning not found: %#v", report.Findings)
	}
}

func TestConfigDoctorRejectsEscapingQAAuthStorageStatePath(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
agent:
  qa:
    auth:
      sessions:
        admin:
          storageState: ../outside.storage.json
          scopes: [authenticated]
`)
	a := &App{Home: t.TempDir()}
	report, err := a.ConfigDoctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Errors == 0 {
		t.Fatalf("escaping auth storage state should be an error: %#v", report)
	}
	if !findingCode(report, "qa-auth-storage-state-invalid") {
		t.Fatalf("invalid qa auth storage state error not found: %#v", report.Findings)
	}
}

func writeProjectFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	full, err := resolveProjectPath(root, rel)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureDir(filepath.Dir(full)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func assertArgPair(t *testing.T, argv []string, flag, value string) {
	t.Helper()
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == value {
			return
		}
	}
	t.Fatalf("argv %#v does not contain %s %s", argv, flag, value)
}

func findingCode(report ConfigDoctorReport, code string) bool {
	for _, finding := range report.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
