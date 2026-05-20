package vivero

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDeployPlanApplyStatusRollbackJSONContract(t *testing.T) {
	home := t.TempDir()
	projectDir := writeDeployFixture(t, true)

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var planPayload struct {
		Plan DeployPlan `json:"plan"`
	}
	if err := json.Unmarshal([]byte(stdout), &planPayload); err != nil {
		t.Fatalf("invalid deploy plan JSON: %v stdout=%s", err, stdout)
	}
	plan := planPayload.Plan
	if !plan.OK || plan.ID == "" || plan.Project != "demo" || plan.Environment != "production" || plan.ApplyCommand == "" || len(plan.Services) != 1 {
		t.Fatalf("unexpected deploy plan: %#v", plan)
	}
	if plan.Services[0].Image == "" || !strings.Contains(plan.Services[0].Image, "@sha256:") {
		t.Fatalf("plan should include immutable service image: %#v", plan.Services)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", plan.ID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy apply exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var applyPayload struct {
		Release ReleaseRecord `json:"release"`
	}
	if err := json.Unmarshal([]byte(stdout), &applyPayload); err != nil {
		t.Fatalf("invalid deploy apply JSON: %v stdout=%s", err, stdout)
	}
	if applyPayload.Release.ID == "" || applyPayload.Release.PlanID != plan.ID || applyPayload.Release.Status != "applied" {
		t.Fatalf("unexpected release after apply: %#v", applyPayload.Release)
	}
	appliedBytes, err := os.ReadFile(filepath.Join(projectDir, "deploy-applied.txt"))
	if err != nil {
		t.Fatalf("apply command should write proof file: %v", err)
	}
	if !strings.Contains(string(appliedBytes), plan.ID) || !strings.Contains(string(appliedBytes), applyPayload.Release.ID) {
		t.Fatalf("apply proof should include plan and release ids: %s", appliedBytes)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "release", "status", "demo", "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("release status exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var statusPayload struct {
		Release ReleaseRecord `json:"release"`
		Status  string        `json:"status"`
	}
	if err := json.Unmarshal([]byte(stdout), &statusPayload); err != nil {
		t.Fatalf("invalid release status JSON: %v stdout=%s", err, stdout)
	}
	if statusPayload.Release.ID != applyPayload.Release.ID || statusPayload.Status != "applied" {
		t.Fatalf("unexpected release status: %#v", statusPayload)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "release", "rollback", "demo", applyPayload.Release.ID, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("release rollback exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var rollbackPayload struct {
		Release ReleaseRecord `json:"release"`
	}
	if err := json.Unmarshal([]byte(stdout), &rollbackPayload); err != nil {
		t.Fatalf("invalid release rollback JSON: %v stdout=%s", err, stdout)
	}
	if rollbackPayload.Release.Status != "rolled_back" || rollbackPayload.Release.RollbackOf != applyPayload.Release.ID {
		t.Fatalf("unexpected rollback release: %#v", rollbackPayload.Release)
	}
	rollbackBytes, err := os.ReadFile(filepath.Join(projectDir, "deploy-rollback.txt"))
	if err != nil {
		t.Fatalf("rollback command should write proof file: %v", err)
	}
	if !strings.Contains(string(rollbackBytes), applyPayload.Release.ID) {
		t.Fatalf("rollback proof should include release id: %s", rollbackBytes)
	}
}

func TestRunDeployPlanBlocksMutablePreviewConfig(t *testing.T) {
	home := t.TempDir()
	projectDir := writeDeployFixture(t, false)

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code == 0 || stderr != "" {
		t.Fatalf("blocked deploy plan should write JSON stdout with non-zero exit, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var planPayload struct {
		Plan DeployPlan `json:"plan"`
	}
	if err := json.Unmarshal([]byte(stdout), &planPayload); err != nil {
		t.Fatalf("invalid blocked deploy plan JSON: %v stdout=%s", err, stdout)
	}
	if planPayload.Plan.OK || planPayload.Plan.ID == "" || len(planPayload.Plan.Diagnostics) == 0 {
		t.Fatalf("blocked plan should carry diagnostics and an id: %#v", planPayload.Plan)
	}
	codes := map[string]bool{}
	for _, diagnostic := range planPayload.Plan.Diagnostics {
		codes[diagnostic.Code] = true
	}
	if !codes["mutable-source"] || !codes["mutable-build"] {
		t.Fatalf("blocked plan should include production doctor diagnostics, got %#v", planPayload.Plan.Diagnostics)
	}
}

func TestDeployAndReleaseCommandsAreDiscoverable(t *testing.T) {
	code, stdout, stderr := runCLITestCommand(t, t.TempDir(), "help", "deploy", "plan")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy plan help exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "plan a production deploy") || !strings.Contains(stdout, "vivero deploy plan") || !strings.Contains(stdout, "--environment NAME") {
		t.Fatalf("deploy plan help is not discoverable/actionable:\n%s", stdout)
	}

	code, stdout, stderr = runCLITestCommand(t, t.TempDir(), "schema", "release", "status", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("release status schema exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var statusSchemaPayload struct {
		Command string `json:"command"`
		Schema  struct {
			AgentSafe                  bool `json:"agentSafe"`
			RunsAppOwnedCommand        bool `json:"runsAppOwnedCommand"`
			MayUpdateLocalReleaseState bool `json:"mayUpdateLocalReleaseState"`
		} `json:"schema"`
	}
	if err := json.Unmarshal([]byte(stdout), &statusSchemaPayload); err != nil {
		t.Fatalf("invalid release status schema JSON: %v stdout=%s", err, stdout)
	}
	if statusSchemaPayload.Command != "release status" || statusSchemaPayload.Schema.AgentSafe || !statusSchemaPayload.Schema.RunsAppOwnedCommand || !statusSchemaPayload.Schema.MayUpdateLocalReleaseState {
		t.Fatalf("release status schema must disclose app-owned command side effects: %#v", statusSchemaPayload)
	}

	code, stdout, stderr = runCLITestCommand(t, t.TempDir(), "schema", "release", "rollback", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("release rollback schema exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var schemaPayload struct {
		Command string `json:"command"`
		Schema  struct {
			Usage     string `json:"usage"`
			AgentSafe bool   `json:"agentSafe"`
		} `json:"schema"`
	}
	if err := json.Unmarshal([]byte(stdout), &schemaPayload); err != nil {
		t.Fatalf("invalid release rollback schema JSON: %v stdout=%s", err, stdout)
	}
	if schemaPayload.Command != "release rollback" || schemaPayload.Schema.AgentSafe || !strings.Contains(schemaPayload.Schema.Usage, "release rollback") {
		t.Fatalf("unexpected release rollback schema: %#v", schemaPayload)
	}
}

func TestReleaseStateUsesCollisionResistantCurrentKeys(t *testing.T) {
	a := &App{Home: t.TempDir()}
	if a.currentReleasePath("a-b", "c") == a.currentReleasePath("a", "b-c") {
		t.Fatalf("current release path should not collide for ambiguous project/environment pairs")
	}
	if len(statePathComponent(strings.Repeat("x", 300))) > 97 {
		t.Fatalf("state path component should cap readable prefix length: %q", statePathComponent(strings.Repeat("x", 300)))
	}

	dir1 := t.TempDir()
	plan1 := DeployPlan{ID: "plan-a-b-c", Project: "a-b", Environment: "c", Path: dir1, OK: true, StatusCommand: "printf first"}
	release1 := newReleaseRecord(plan1, "applied", "")
	if err := a.saveDeployPlan(plan1); err != nil {
		t.Fatal(err)
	}
	if err := a.saveRelease(release1); err != nil {
		t.Fatal(err)
	}

	dir2 := t.TempDir()
	plan2 := DeployPlan{ID: "plan-a-bc", Project: "a", Environment: "b-c", Path: dir2, OK: true, StatusCommand: "printf second"}
	release2 := newReleaseRecord(plan2, "applied", "")
	if err := a.saveDeployPlan(plan2); err != nil {
		t.Fatal(err)
	}
	if err := a.saveRelease(release2); err != nil {
		t.Fatal(err)
	}

	current1, err := a.CurrentRelease("a-b", "c")
	if err != nil {
		t.Fatal(err)
	}
	if current1.ID != release1.ID || current1.Status != "first" {
		t.Fatalf("expected first release without collision, got %#v", current1)
	}
	current2, err := a.CurrentRelease("a", "b-c")
	if err != nil {
		t.Fatal(err)
	}
	if current2.ID != release2.ID || current2.Status != "second" {
		t.Fatalf("expected second release without collision, got %#v", current2)
	}
}

func TestCurrentReleaseRejectsMismatchedStateBeforeStatusCommand(t *testing.T) {
	a := &App{Home: t.TempDir()}
	dir := t.TempDir()
	plan := DeployPlan{ID: "plan-tampered", Project: "evil", Environment: "production", Path: dir, OK: true, StatusCommand: "printf tampered > tampered.txt"}
	if err := a.saveDeployPlan(plan); err != nil {
		t.Fatal(err)
	}
	release := newReleaseRecord(plan, "applied", "")
	if err := ensureDir(a.releaseDir()); err != nil {
		t.Fatal(err)
	}
	if err := writeIndentedJSONFile(a.currentReleasePath("demo", "production"), release, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := a.CurrentRelease("demo", "production")
	if err == nil || !strings.Contains(err.Error(), "current release state mismatch") {
		t.Fatalf("expected current release state mismatch before status command, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "tampered.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("mismatched current release should not run status command, stat err=%v", statErr)
	}
}

func writeDeployFixture(t *testing.T, immutable bool) string {
	t.Helper()
	dir := t.TempDir()
	image := "registry.example.com/demo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	service := "    image: " + image + "\n"
	if !immutable {
		service = "    source: app\n    build:\n      context: .\n      dockerfile: Dockerfile\n"
	}
	config := "project:\n" +
		"  name: demo\n" +
		"sources:\n" +
		"  app:\n" +
		"    mode: external\n" +
		"    path: .\n" +
		"services:\n" +
		"  web:\n" +
		service +
		"    port: 3000\n" +
		"    health:\n" +
		"      path: /\n" +
		"      timeout: 30s\n" +
		"    resources:\n" +
		"      cpus: '1'\n" +
		"      memory: 512m\n" +
		"deploy:\n" +
		"  environments:\n" +
		"    production:\n" +
		"      applyCommand: 'printf applied:$VIVERO_DEPLOY_PLAN_ID:$VIVERO_RELEASE_ID > deploy-applied.txt'\n" +
		"      statusCommand: 'printf applied'\n" +
		"      rollbackCommand: 'printf rollback:$VIVERO_ROLLBACK_RELEASE_ID > deploy-rollback.txt'\n"
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
