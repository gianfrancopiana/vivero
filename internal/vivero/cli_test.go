package vivero

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestRunVersionEntrypoints(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"up", "--version"}} {
		code, stdout, stderr := runCLITestCommand(t, t.TempDir(), args...)
		if code != 0 || stderr != "" || strings.TrimSpace(stdout) != Version {
			t.Fatalf("Run(%v) exit=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}

	for _, args := range [][]string{{"--version", "--json", "--no-input"}, {"up", "demo", "--version", "--json", "--no-input"}, {"version", "--json", "--no-input"}} {
		code, stdout, stderr := runCLITestCommand(t, t.TempDir(), args...)
		if code != 0 || stderr != "" {
			t.Fatalf("Run(%v) exit=%d stdout=%s stderr=%s", args, code, stdout, stderr)
		}
		var payload struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			Date    string `json:"date"`
		}
		if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
			t.Fatalf("invalid version JSON for %v: %v stdout=%s", args, err, stdout)
		}
		if payload.Version != Version || payload.Commit == "" || payload.Date == "" {
			t.Fatalf("version payload for %v = %#v", args, payload)
		}
	}
}

func TestRunHelpAndSubcommandHelpAreExamplesFirst(t *testing.T) {
	home := t.TempDir()
	code, stdout, stderr := runCLITestCommand(t, home, "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("help exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{"local-first app operations for agents", "Preview:", "Deploy/release:", "Evidence/debug:", "vivero preview up", "vivero deploy plan", "vivero commands --json --no-input"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("root help missing %q:\n%s", want, stdout)
		}
	}
	for _, hidden := range []string{"vivero serve", "vivero diagnose startup"} {
		if strings.Contains(stdout, hidden) {
			t.Fatalf("root help should not show %q because manifest visibility is not common:\n%s", hidden, stdout)
		}
	}

	code, stdout, stderr = runCLITestCommand(t, home, "help", "preview")
	if code != 0 || stderr != "" {
		t.Fatalf("preview help exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, want := range []string{"vivero preview - command group", "vivero preview up", "vivero preview inspect", "vivero preview down"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("preview help missing %q:\n%s", want, stdout)
		}
	}

	code, stdout, stderr = runCLITestCommand(t, home, "help", "qa", "run")
	if code != 0 || stderr != "" {
		t.Fatalf("qa run help exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	examples := strings.Index(stdout, "Examples:")
	flags := strings.Index(stdout, "Flags:")
	if examples == -1 || flags == -1 || examples > flags {
		t.Fatalf("subcommand help should be examples-first:\n%s", stdout)
	}
	for _, want := range []string{"Usage:", "vivero qa run", "Category: qa", "Visibility: common", "JSON stability: stable"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("qa run help missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunInitWritesThinConfigAndDoctorPasses(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(t.TempDir(), "My App")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLITestCommand(t, home, "init", projectDir, "--name", "My App", "--service", "web", "--port", "4173", "--command", "npm run dev -- --host 0.0.0.0", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("init exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		Init struct {
			OK           bool     `json:"ok"`
			Path         string   `json:"path"`
			Project      string   `json:"project"`
			Service      string   `json:"service"`
			NextCommands []string `json:"nextCommands"`
		} `json:"init"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("init stdout should be JSON: %v stdout=%s", err, stdout)
	}
	configPath := filepath.Join(projectDir, "vivero.yml")
	if !payload.Init.OK || payload.Init.Path != configPath || payload.Init.Project != "my-app" || payload.Init.Service != "web" {
		t.Fatalf("unexpected init payload: %#v", payload.Init)
	}
	if len(payload.Init.NextCommands) == 0 || !strings.Contains(strings.Join(payload.Init.NextCommands, "\n"), "vivero doctor config") {
		t.Fatalf("init should return next commands for agents: %#v", payload.Init.NextCommands)
	}
	bodyBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	for _, want := range []string{"project:", "name: my-app", "sources:", "mode: external", "services:", "dockerfile: Dockerfile", "command: npm run dev -- --host 0.0.0.0", "port: 4173", "agent:", "defaultPreviewService: web", "smokeTests:"} {
		if !strings.Contains(body, want) {
			t.Fatalf("generated config missing %q:\n%s", want, body)
		}
	}
	_, cfg, err := loadProjectConfig(projectDir)
	if err != nil {
		t.Fatalf("generated config should load: %v\n%s", err, body)
	}
	if cfg.Project.Name != "my-app" || cfg.Agent.DefaultPreviewService != "web" || cfg.Services["web"].Port != 4173 {
		t.Fatalf("unexpected generated config: %#v", cfg)
	}
	report, err := (&App{}).ConfigDoctor(projectDir)
	if err != nil || !report.OK {
		t.Fatalf("doctor config should pass err=%v report=%#v\n%s", err, report, body)
	}
}

func TestRunInitRefusesToOverwriteWithoutForce(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(projectDir, "vivero.yml")
	if err := os.WriteFile(configPath, []byte("project:\n  name: existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLITestCommand(t, t.TempDir(), "init", projectDir, "--json", "--no-input")
	if code == 0 || stdout != "" {
		t.Fatalf("init should fail without --force, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload cliErrorResponse
	if err := json.Unmarshal([]byte(stderr), &payload); err != nil {
		t.Fatalf("stderr should be JSON error: %v stderr=%s", err, stderr)
	}
	if payload.Error.Code != "config_exists" || !strings.Contains(payload.Error.Hint, "--force") {
		t.Fatalf("unexpected overwrite error: %#v", payload.Error)
	}
}

func TestPreviewNamespaceAliasesRootPreviewCommands(t *testing.T) {
	home := t.TempDir()
	setupCLIQAPreview(t, home)

	for _, tc := range []struct {
		name  string
		root  []string
		alias []string
	}{
		{name: "list", root: []string{"list", "--json", "--no-input"}, alias: []string{"preview", "list", "--json", "--no-input"}},
		{name: "inspect", root: []string{"inspect", "cli-pr", "--json", "--no-input"}, alias: []string{"preview", "inspect", "cli-pr", "--json", "--no-input"}},
		{name: "events", root: []string{"events", "cli-pr", "--tail", "--json", "--no-input"}, alias: []string{"preview", "events", "cli-pr", "--tail", "--json", "--no-input"}},
		{name: "logs", root: []string{"logs", "cli-pr", "web", "--json", "--no-input"}, alias: []string{"preview", "logs", "cli-pr", "web", "--json", "--no-input"}},
		{name: "qa plan", root: []string{"qa", "plan", "cli-pr", "--scope", "auth", "--json", "--no-input"}, alias: []string{"preview", "qa", "plan", "cli-pr", "--scope", "auth", "--json", "--no-input"}},
		{name: "diagnose startup", root: []string{"diagnose", "startup", "cli-pr", "--json", "--no-input"}, alias: []string{"preview", "diagnose", "startup", "cli-pr", "--json", "--no-input"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rootCode, rootStdout, rootStderr := runCLITestCommand(t, home, tc.root...)
			aliasCode, aliasStdout, aliasStderr := runCLITestCommand(t, home, tc.alias...)
			if rootCode != 0 || rootStderr != "" {
				t.Fatalf("root command failed exit=%d stdout=%s stderr=%s", rootCode, rootStdout, rootStderr)
			}
			if aliasCode != 0 || aliasStderr != "" {
				t.Fatalf("preview alias failed exit=%d stdout=%s stderr=%s", aliasCode, aliasStdout, aliasStderr)
			}
			if aliasStdout != rootStdout {
				t.Fatalf("preview alias should match root output\nroot=%s\nalias=%s", rootStdout, aliasStdout)
			}
		})
	}
}

func TestRunDiscoveryCommandsAreStateFree(t *testing.T) {
	blockedHome := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedHome, []byte("state should not be opened"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIVERO_HOME", blockedHome)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "commands json", args: []string{"commands", "--json", "--no-input"}, want: `"commands"`},
		{name: "schema json", args: []string{"schema", "--json", "--no-input"}, want: `"commands"`},
		{name: "schema command json", args: []string{"schema", "qa", "run", "--json", "--no-input"}, want: `"command": "qa run"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tc.args, &stdout, &stderr)
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("Run(%v) exit=%d stdout=%s stderr=%s", tc.args, code, stdout.String(), stderr.String())
			}
			if !json.Valid(stdout.Bytes()) {
				t.Fatalf("Run(%v) stdout should be JSON: %s", tc.args, stdout.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("Run(%v) stdout missing %q: %s", tc.args, tc.want, stdout.String())
			}
		})
	}
}

func TestRunUnknownCommandsAreStateFree(t *testing.T) {
	blockedHome := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedHome, []byte("state should not be opened"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIVERO_HOME", blockedHome)

	for _, tc := range []struct {
		name       string
		args       []string
		wantDetail string
		wantHint   string
	}{
		{name: "top level", args: []string{"capabilties", "--json", "--no-input"}, wantDetail: `"command": "capabilties"`, wantHint: "capabilities"},
		{name: "nested", args: []string{"qa", "rn", "preview", "--json", "--no-input"}, wantDetail: `"command": "qa rn"`, wantHint: "vivero qa run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tc.args, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("Run(%v) should fail", tc.args)
			}
			if stdout.Len() != 0 {
				t.Fatalf("Run(%v) should keep JSON errors on stderr, stdout=%s", tc.args, stdout.String())
			}
			body := stderr.String()
			for _, want := range []string{`"ok": false`, `"code": "unknown_command"`, tc.wantDetail, tc.wantHint} {
				if !strings.Contains(body, want) {
					t.Fatalf("Run(%v) JSON error missing %q: %s", tc.args, want, body)
				}
			}
			if strings.Contains(body, "database") || strings.Contains(body, "not-a-directory") {
				t.Fatalf("Run(%v) should not expose state initialization errors: %s", tc.args, body)
			}
		})
	}
}

func TestRunUnknownCommandReturnsJSONErrorShape(t *testing.T) {
	_, payload := runCLITestJSONError(t, "prevue")
	if payload.Error.Code != "unknown_command" || !strings.Contains(payload.Error.Message, "prevue") || payload.Error.Hint == "" || payload.Error.Docs == "" {
		t.Fatalf("unexpected error payload: %#v", payload)
	}
}

func TestRunJSONErrorStreamContract(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		code    string
		docs    string
		details map[string]string
	}{
		{name: "unknown command with suggestion", args: []string{"capabilties"}, code: "unknown_command", docs: "vivero help capabilities", details: map[string]string{"command": "capabilties", "suggestion": "capabilities"}},
		{name: "unknown nested command with suggestion", args: []string{"qa", "rn", "preview"}, code: "unknown_command", docs: "vivero help qa run", details: map[string]string{"command": "qa rn", "suggestion": "qa run"}},
		{name: "missing required flag", args: []string{"up", "demo"}, code: "missing_required_argument", docs: "vivero help up", details: map[string]string{"command": "up", "required": "--id"}},
		{name: "missing group action", args: []string{"qa"}, code: "missing_required_argument", docs: "vivero help qa", details: map[string]string{"command": "qa", "required": "subcommand and preview"}},
		{name: "missing multi-arg command", args: []string{"sync", "preview", "app"}, code: "missing_required_argument", docs: "vivero help sync", details: map[string]string{"command": "sync", "required": "preview, source, and path"}},
		{name: "invalid argument", args: []string{"secrets", "set", "demo", "NOT_A_SECRET"}, code: "invalid_argument", docs: "vivero help secrets set", details: map[string]string{"command": "secrets set", "argument": "KEY=value"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, payload := runCLITestJSONError(t, tc.args...)
			if payload.Error.Code != tc.code || payload.Error.Docs != tc.docs {
				t.Fatalf("unexpected code/docs: %#v", payload.Error)
			}
			if payload.Error.Message == "" || payload.Error.Hint == "" {
				t.Fatalf("JSON error should be actionable: %#v", payload.Error)
			}
			for key, want := range tc.details {
				got, _ := payload.Error.Details[key].(string)
				if got != want {
					t.Fatalf("detail %s = %q, want %q in %#v", key, got, want, payload.Error.Details)
				}
			}
		})
	}
}

func TestRunMissingRequiredArgumentErrorsAreActionableJSON(t *testing.T) {
	code, stdout, stderr := runCLITestCommand(t, t.TempDir(), "up", "demo", "--json", "--no-input")
	if code == 0 || stdout != "" {
		t.Fatalf("missing --id should fail on stderr only, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Hint    string            `json:"hint"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stderr), &payload); err != nil {
		t.Fatalf("stderr should be JSON error: %v stderr=%s", err, stderr)
	}
	if payload.OK || payload.Error.Code != "missing_required_argument" || payload.Error.Details["required"] != "--id" || !strings.Contains(payload.Error.Hint, "vivero help up") {
		t.Fatalf("unexpected missing arg payload: %#v", payload)
	}
}

func TestRunSchemaDiagnoseReturnsCommandSchema(t *testing.T) {
	code, stdout, stderr := runCLITestCommand(t, t.TempDir(), "schema", "diagnose", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("schema diagnose exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		Command string `json:"command"`
		Schema  struct {
			Usage         string `json:"usage"`
			JSONStability string `json:"jsonStability"`
			AgentSafe     bool   `json:"agentSafe"`
		} `json:"schema"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid schema JSON: %v stdout=%s", err, stdout)
	}
	if payload.Command != "diagnose startup" || payload.Schema.JSONStability != "experimental" || !payload.Schema.AgentSafe || !strings.Contains(payload.Schema.Usage, "diagnose startup") {
		t.Fatalf("unexpected diagnose schema: %#v", payload)
	}
}

func TestRunSchemaUnknownCommandFailsActionably(t *testing.T) {
	code, stdout, stderr := runCLITestCommand(t, t.TempDir(), "schema", "qa", "rn", "--json", "--no-input")
	if code == 0 || stdout != "" {
		t.Fatalf("schema unknown command should fail on stderr only, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
			Hint string `json:"hint"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stderr), &payload); err != nil {
		t.Fatalf("stderr should be JSON error: %v stderr=%s", err, stderr)
	}
	if payload.OK || payload.Error.Code != "unknown_command" || !strings.Contains(payload.Error.Hint, "vivero qa run") {
		t.Fatalf("unexpected schema error payload: %#v", payload)
	}
}

func TestEveryPublicCommandHasHelpAndSchemaContract(t *testing.T) {
	for _, cmd := range commandCatalog() {
		helpArgs := append([]string{"help"}, cmd.Path...)
		code, stdout, stderr := runCLITestCommand(t, t.TempDir(), helpArgs...)
		if code != 0 || stderr != "" {
			t.Fatalf("help %s exit=%d stdout=%s stderr=%s", cmd.Name(), code, stdout, stderr)
		}
		for _, want := range []string{"Examples:", "JSON stability:"} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("help %s missing %q:\n%s", cmd.Name(), want, stdout)
			}
		}

		schemaArgs := append([]string{"schema"}, cmd.Path...)
		schemaArgs = append(schemaArgs, "--json", "--no-input")
		code, stdout, stderr = runCLITestCommand(t, t.TempDir(), schemaArgs...)
		if code != 0 || stderr != "" {
			t.Fatalf("schema %s exit=%d stdout=%s stderr=%s", cmd.Name(), code, stdout, stderr)
		}
		var payload struct {
			Command string `json:"command"`
			Schema  struct {
				Usage         string `json:"usage"`
				JSONStability string `json:"jsonStability"`
			} `json:"schema"`
		}
		if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
			t.Fatalf("invalid schema JSON for %s: %v stdout=%s", cmd.Name(), err, stdout)
		}
		if payload.Command != cmd.Name() || payload.Schema.Usage == "" || payload.Schema.JSONStability != cmd.JSONStability {
			t.Fatalf("schema drift for %s: %#v", cmd.Name(), payload)
		}
	}
}

func TestRunCommandsJSONCoversREADMEInvocations(t *testing.T) {
	code, stdout, stderr := runCLITestCommand(t, t.TempDir(), "commands", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("commands exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		Commands []CommandManifest `json:"commands"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid commands JSON: %v stdout=%s", err, stdout)
	}
	if len(payload.Commands) == 0 {
		t.Fatal("commands JSON should include command manifests")
	}
	for _, cmd := range payload.Commands {
		if cmd.Category == "" || !validCommandVisibility(cmd.Visibility) || !validCommandLane(cmd.Lane) {
			t.Fatalf("commands JSON missing category/visibility/lane metadata: %#v", cmd)
		}
	}
	readmeBytes, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, invocation := range readmeViveroInvocations(string(readmeBytes)) {
		if !manifestMatchesInvocation(payload.Commands, invocation) {
			t.Fatalf("README invocation %q is not covered by command manifest", invocation)
		}
	}
}

func TestRunDoctorJSONContracts(t *testing.T) {
	home := t.TempDir()
	code, stdout, stderr := runCLITestCommand(t, home, "doctor", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("doctor exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var doctor struct {
		OK      bool           `json:"ok"`
		Version string         `json:"version"`
		Checks  map[string]any `json:"checks"`
	}
	if err := json.Unmarshal([]byte(stdout), &doctor); err != nil {
		t.Fatalf("invalid doctor JSON: %v stdout=%s", err, stdout)
	}
	if !doctor.OK || doctor.Version == "" || doctor.Checks["database"] == "" {
		t.Fatalf("unexpected doctor payload: %#v", doctor)
	}

	badConfigDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(badConfigDir, "vivero.yml"), []byte("project:\n  name: [unterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runCLITestCommand(t, home, "doctor", "--project", badConfigDir, "--json", "--no-input")
	if code == 0 || stderr != "" {
		t.Fatalf("doctor --project bad config should fail via JSON stdout, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var configPayload struct {
		ConfigDoctor ConfigDoctorReport `json:"configDoctor"`
	}
	if err := json.Unmarshal([]byte(stdout), &configPayload); err != nil {
		t.Fatalf("invalid config doctor JSON: %v stdout=%s", err, stdout)
	}
	if configPayload.ConfigDoctor.OK || configPayload.ConfigDoctor.Errors == 0 || len(configPayload.ConfigDoctor.Findings) == 0 || configPayload.ConfigDoctor.Findings[0].Code != "config-load" {
		t.Fatalf("unexpected config doctor payload: %#v", configPayload.ConfigDoctor)
	}
}

func TestRunQASubcommandsJSONContract(t *testing.T) {
	home := t.TempDir()
	setupCLIQAPreview(t, home)

	code, stdout, stderr := runCLITestCommand(t, home, "qa", "plan", "cli-pr", "--scope", "auth", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("qa plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var plan map[string]any
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatalf("invalid qa plan JSON: %v stdout=%s", err, stdout)
	}
	if plan["version"].(float64) != 1 || plan["target"] != "local" || plan["driver"] == nil || plan["auth"] == nil || plan["evidence"] == nil {
		t.Fatalf("qa plan missing stable contract fields: %#v", plan)
	}
	if !strings.Contains(stdout, "--storage-state") {
		t.Fatalf("authenticated qa plan should include generated storage-state commands: %s", stdout)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "qa", "run", "cli-pr", "--scope", "auth", "--no-screenshots", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("qa run exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var run map[string]any
	if err := json.Unmarshal([]byte(stdout), &run); err != nil {
		t.Fatalf("invalid qa run JSON: %v stdout=%s", err, stdout)
	}
	if run["ok"] != true || run["plan"] == nil || run["report"] == nil || run["runPath"] == "" {
		t.Fatalf("qa run missing stable contract fields: %#v", run)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "qa", "final", "cli-pr", "--scope", "auth", "--no-screenshots", "--no-record", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("qa final exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var final map[string]any
	if err := json.Unmarshal([]byte(stdout), &final); err != nil {
		t.Fatalf("invalid qa final JSON: %v stdout=%s", err, stdout)
	}
	proof := final["proof"].(map[string]any)
	if final["ok"] != true || final["plan"] == nil || final["run"] == nil || final["diagnosis"] == nil || final["finalPath"] == "" {
		t.Fatalf("qa final missing stable contract fields: %#v", final)
	}
	if proof["url"] != "http://127.0.0.1:7777" || proof["reportPath"] == "" || proof["runPath"] == "" || proof["recordSkipped"] != true {
		t.Fatalf("qa final proof should summarize handoff evidence: %#v", proof)
	}
	if _, err := os.Stat(final["finalPath"].(string)); err != nil {
		t.Fatalf("qa final should write final proof JSON: %v", err)
	}
}

func TestEvidenceCommandsAcceptPreviewTargetRefs(t *testing.T) {
	home := t.TempDir()
	setupCLIQAPreview(t, home)

	code, stdout, stderr := runCLITestCommand(t, home, "logs", "preview:cli-pr", "web", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("logs target ref exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var logs map[string]any
	if err := json.Unmarshal([]byte(stdout), &logs); err != nil {
		t.Fatalf("invalid logs JSON: %v stdout=%s", err, stdout)
	}
	assertPreviewTargetRef(t, logs, "cli-pr")
	assertEvidenceShape(t, logs)
	if _, ok := logs["logPath"].(string); !ok {
		t.Fatalf("logs evidence should expose logPath: %#v", logs)
	}
	if logs["preview"] != "cli-pr" || logs["service"] != "web" || !strings.Contains(stdout, "fixture log line") {
		t.Fatalf("logs should resolve preview target ref without changing payload: %#v", logs)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "events", "preview:cli-pr", "--tail", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("events target ref exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var events map[string]any
	if err := json.Unmarshal([]byte(stdout), &events); err != nil {
		t.Fatalf("invalid events JSON: %v stdout=%s", err, stdout)
	}
	assertPreviewTargetRef(t, events, "cli-pr")
	assertEvidenceShape(t, events)
	if got := len(events["events"].([]any)); got == 0 {
		t.Fatalf("events should resolve preview target ref and return recorded startup event: %#v", events)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "inspect", "preview:cli-pr", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("inspect target ref exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var inspect map[string]any
	if err := json.Unmarshal([]byte(stdout), &inspect); err != nil {
		t.Fatalf("invalid inspect JSON: %v stdout=%s", err, stdout)
	}
	assertPreviewTargetRef(t, inspect, "cli-pr")
	assertEvidenceShape(t, inspect)
	previewRecord := inspect["preview"].(map[string]any)
	if previewRecord["id"] != "cli-pr" {
		t.Fatalf("inspect should resolve preview target ref: %#v", inspect)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "qa", "plan", "preview:cli-pr", "--scope", "auth", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("qa plan target ref exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var plan map[string]any
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatalf("invalid qa plan JSON: %v stdout=%s", err, stdout)
	}
	assertPreviewTargetRef(t, plan, "cli-pr")
	assertEvidenceShape(t, plan)
	preview, _ := plan["preview"].(map[string]any)
	if preview["id"] != "cli-pr" || plan["target"] != "local" {
		t.Fatalf("qa plan should keep preview id and artifact target separate: %#v", plan)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "diagnose", "startup", "preview:cli-pr", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("diagnose startup target ref exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var diagnosis map[string]any
	if err := json.Unmarshal([]byte(stdout), &diagnosis); err != nil {
		t.Fatalf("invalid diagnose JSON: %v stdout=%s", err, stdout)
	}
	assertPreviewTargetRef(t, diagnosis, "cli-pr")
	assertEvidenceShape(t, diagnosis)
}

func TestRunSecretsAndSkillJSONContracts(t *testing.T) {
	home := t.TempDir()
	code, stdout, stderr := runCLITestCommand(t, home, "secrets", "set", "demo", "TOKEN=secret-value", "OTHER=1", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("secrets set exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "secret-value") {
		t.Fatalf("secrets JSON must not echo values: %s", stdout)
	}
	var secrets struct {
		Project string   `json:"project"`
		Keys    []string `json:"keys"`
	}
	if err := json.Unmarshal([]byte(stdout), &secrets); err != nil {
		t.Fatalf("invalid secrets JSON: %v stdout=%s", err, stdout)
	}
	if secrets.Project != "demo" || strings.Join(secrets.Keys, ",") != "OTHER,TOKEN" {
		t.Fatalf("unexpected secrets payload: %#v", secrets)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "secrets", "unset", "demo", "TOKEN", "--json", "--no-input")
	if code != 0 || stderr != "" || strings.Contains(stdout, "TOKEN") {
		t.Fatalf("secrets unset should remove TOKEN exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	target := filepath.Join(t.TempDir(), "vivero-skill")
	code, stdout, stderr = runCLITestCommand(t, home, "skill", "install", "--target", target, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("skill install exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var install struct {
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
		Targets []struct {
			Path      string `json:"path"`
			Installed bool   `json:"installed"`
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(stdout), &install); err != nil {
		t.Fatalf("invalid skill install JSON: %v stdout=%s", err, stdout)
	}
	if install.Version == "" || install.SHA256 == "" || len(install.Targets) != 1 || !install.Targets[0].Installed {
		t.Fatalf("unexpected skill install payload: %#v", install)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("skill install should write SKILL.md: %v", err)
	}
}

func TestFlagParsingErrorsStayJSON(t *testing.T) {
	code, stdout, stderr := runCLITestCommand(t, t.TempDir(), "qa", "record", "missing-preview", "--width", "0", "--json", "--no-input")
	if code == 0 || stdout != "" {
		t.Fatalf("invalid width should fail on stderr only, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stderr), &payload); err != nil {
		t.Fatalf("stderr should be JSON error: %v stderr=%s", err, stderr)
	}
	if payload.OK || payload.Error.Code != "error" || !strings.Contains(payload.Error.Message, "--width must be a positive integer") {
		t.Fatalf("unexpected flag error payload: %#v", payload)
	}
}

func TestCLIHumanFormatHelpers(t *testing.T) {
	projectText := projectsHuman([]ProjectRecord{{Name: "demo", Path: "/tmp/demo"}})
	if !strings.Contains(projectText, "demo\t/tmp/demo") {
		t.Fatalf("projectsHuman output = %q", projectText)
	}
	preview := PreviewRecord{ID: "cli-pr", Status: "running", Profile: "full", Services: map[string]PreviewService{"web": {Name: "web", Status: "healthy", URL: "https://example.test"}}}
	previewText := previewHuman(preview)
	if !strings.Contains(previewText, "cli-pr running profile=full") || !strings.Contains(previewText, "web\thealthy\thttps://example.test") {
		t.Fatalf("previewHuman output = %q", previewText)
	}
	if got := screenshotsHuman(map[string]any{"screenshots": []map[string]any{{"path": "/tmp/a.png"}, {"path": "/tmp/b.png"}}}); got != "/tmp/a.png\n/tmp/b.png" {
		t.Fatalf("screenshotsHuman output = %q", got)
	}
	if got := eventsHuman([]Event{{Seq: 1, Level: "info", Type: "service.ready", Message: "ready"}}); !strings.Contains(got, "1 info service.ready ready") {
		t.Fatalf("eventsHuman output = %q", got)
	}
	if got := qaRunHuman(map[string]any{"ok": true, "report": map[string]any{"path": "/tmp/report.md"}}); got != "qa run ok\nreport: /tmp/report.md" {
		t.Fatalf("qaRunHuman output = %q", got)
	}
}

func runCLITestCommand(t *testing.T, home string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("VIVERO_HOME", home)
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func assertPreviewTargetRef(t *testing.T, payload map[string]any, previewID string) {
	t.Helper()
	targetRef, ok := payload["targetRef"].(map[string]any)
	if !ok {
		t.Fatalf("payload missing targetRef: %#v", payload)
	}
	if targetRef["kind"] != "preview" || targetRef["id"] != previewID || targetRef["ref"] != "preview:"+previewID {
		t.Fatalf("unexpected targetRef: %#v", targetRef)
	}
}

func assertEvidenceShape(t *testing.T, payload map[string]any) {
	t.Helper()
	if _, ok := payload["targetRef"].(map[string]any); !ok {
		t.Fatalf("evidence payload missing targetRef: %#v", payload)
	}
	if _, ok := payload["ok"].(bool); !ok {
		t.Fatalf("evidence payload missing ok boolean: %#v", payload)
	}
	if _, ok := payload["artifacts"].(map[string]any); !ok {
		t.Fatalf("evidence payload missing artifacts object: %#v", payload)
	}
}

func runCLITestJSONError(t *testing.T, args ...string) (int, cliErrorResponse) {
	t.Helper()
	jsonArgs := append([]string{}, args...)
	jsonArgs = append(jsonArgs, "--json", "--no-input")
	code, stdout, stderr := runCLITestCommand(t, t.TempDir(), jsonArgs...)
	if code == 0 {
		t.Fatalf("Run(%v) should fail, stdout=%s stderr=%s", jsonArgs, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("Run(%v) JSON error should write stdout empty, got %s", jsonArgs, stdout)
	}
	if !json.Valid([]byte(stderr)) {
		t.Fatalf("Run(%v) stderr should be valid JSON, got %s", jsonArgs, stderr)
	}
	if strings.Contains(stderr, "cause") || strings.Contains(stderr, "Cause") {
		t.Fatalf("Run(%v) JSON error should not leak wrapped causes: %s", jsonArgs, stderr)
	}
	var payload cliErrorResponse
	if err := json.Unmarshal([]byte(stderr), &payload); err != nil {
		t.Fatalf("Run(%v) stderr should be JSON error: %v stderr=%s", jsonArgs, err, stderr)
	}
	if payload.OK || payload.Error.Code == "" || payload.Error.Message == "" {
		t.Fatalf("Run(%v) invalid JSON error payload: %#v", jsonArgs, payload)
	}
	return code, payload
}

func setupCLIQAPreview(t *testing.T, home string) {
	t.Helper()
	t.Setenv("VIVERO_HOME", home)
	projectDir := t.TempDir()
	storageState := filepath.Join(projectDir, ".vivero", "auth", "admin.storage.json")
	if err := os.MkdirAll(filepath.Dir(storageState), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storageState, []byte(`{"cookies":[],"origins":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(projectDir, ".vivero", "logs", "web.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("fixture log line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cfg := ProjectConfig{
		Project:  ProjectMeta{Name: "demo"},
		Sources:  map[string]SourceConfig{"app": {Mode: "external", Path: projectDir}},
		Services: map[string]ServiceConfig{"web": {Source: "app", Port: 3000}},
		Agent: AgentConfig{
			DefaultPreviewService: "web",
			CommonPages: map[string]AgentPage{
				"home": {Service: "web", Path: "/"},
			},
			QA: QAConfig{
				DefaultScope: "auth",
				ArtifactRoot: ".vivero/qa",
				Auth: QAAuthConfig{Sessions: map[string]QAAuthSession{
					"admin": {StorageState: ".vivero/auth/admin.storage.json", Scopes: []string{"auth"}},
				}},
				Evidence: QAEvidenceConfig{
					Screenshots: QAScreenshotEvidenceConfig{ColorSchemes: []string{"light", "dark"}},
					Recordings:  QARecordingEvidenceConfig{ColorSchemes: []string{"dark"}},
				},
				Scopes: []QAScope{{
					Name:        "auth",
					AuthSession: "admin",
					Pages:       []string{"home"},
					Flows:       []QAFlow{{Name: "home", Start: "home", Steps: []QAStep{{Visit: "home"}, {Screenshot: "home"}}}},
					Checks:      []QACheck{{Name: "review-authenticated-home", Category: "ui", Severity: "normal", Method: "manual"}},
				}},
			},
		},
	}
	if _, err := a.saveProject(projectDir, cfg); err != nil {
		t.Fatal(err)
	}
	created := nowUTC().Add(-time.Minute)
	if err := a.upsertPreview(PreviewRecord{ID: "cli-pr", Project: "demo", Status: "running", CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("cli-pr", PreviewService{Name: "web", Source: "app", Status: "healthy", OriginURL: "http://127.0.0.1:3000", ProxyURL: "http://127.0.0.1:7777", LogPath: logPath}); err != nil {
		t.Fatal(err)
	}
	insertStartupEvent(t, a, "cli-pr", created.Add(time.Second), "info", "service.healthy", "health check passed", "web", map[string]string{"durationMs": "1200"})
}

func readmeViveroInvocations(readme string) []string {
	re := regexp.MustCompile(`vivero(?:\s+[a-z][a-z0-9_-]*)+`)
	seen := map[string]bool{}
	out := []string{}
	for _, match := range re.FindAllString(readme, -1) {
		fields := strings.Fields(match)
		if len(fields) < 2 || seen[match] {
			continue
		}
		seen[match] = true
		out = append(out, match)
	}
	return out
}

func manifestMatchesInvocation(commands []CommandManifest, invocation string) bool {
	words := strings.Fields(invocation)
	if len(words) < 2 || words[0] != "vivero" {
		return false
	}
	invocationPath := words[1:]
	for _, command := range commands {
		if len(command.Path) > len(invocationPath) {
			continue
		}
		matched := true
		for i, part := range command.Path {
			if invocationPath[i] != part {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
