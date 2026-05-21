package vivero

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	applyMap := decodeJSONMap(t, stdout)
	assertEvidenceShape(t, applyMap)
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
	statusMap := decodeJSONMap(t, stdout)
	assertEvidenceShape(t, statusMap)
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
	rollbackMap := decodeJSONMap(t, stdout)
	assertEvidenceShape(t, rollbackMap)
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

	code, stdout, stderr = runCLITestCommand(t, home, "release", "rollback", "demo", applyPayload.Release.ID, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("repeat release rollback exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var repeatRollbackPayload struct {
		Release ReleaseRecord `json:"release"`
	}
	if err := json.Unmarshal([]byte(stdout), &repeatRollbackPayload); err != nil {
		t.Fatalf("invalid repeat release rollback JSON: %v stdout=%s", err, stdout)
	}
	if repeatRollbackPayload.Release.ID != rollbackPayload.Release.ID {
		t.Fatalf("repeat rollback should return existing rollback release, got %#v want %#v", repeatRollbackPayload.Release, rollbackPayload.Release)
	}
}

func TestRunReleaseEvidenceCommandsExposeEventsLogsAndSmoke(t *testing.T) {
	home := t.TempDir()
	projectDir := writeReleaseEvidenceDeployFixture(t, false)

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	plan := decodeJSONMap(t, stdout)["plan"].(map[string]any)
	if plan["smokeCommand"] == "" {
		t.Fatalf("deploy plan should expose command-strategy smoke gate: %#v", plan)
	}

	planID := plan["id"].(string)
	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy apply exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	applyRelease := decodeJSONMap(t, stdout)["release"].(map[string]any)
	releaseID := applyRelease["id"].(string)
	if applyRelease["status"] != "applied" || !strings.Contains(stdout, "smoke-output") {
		t.Fatalf("deploy apply should run smoke before marking release applied: %#v stdout=%s", applyRelease, stdout)
	}
	if smokeBytes, err := os.ReadFile(filepath.Join(projectDir, "deploy-smoke.txt")); err != nil || !strings.Contains(string(smokeBytes), releaseID) {
		t.Fatalf("deploy apply should write smoke proof, err=%v proof=%s", err, smokeBytes)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "release", "events", "release:"+releaseID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("release events exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	eventsPayload := decodeJSONMap(t, stdout)
	assertEvidenceShape(t, eventsPayload)
	if eventsPayload["targetRef"].(map[string]any)["ref"] != "release:"+releaseID || !strings.Contains(stdout, "\"action\": \"apply\"") || !strings.Contains(stdout, "\"action\": \"smoke\"") {
		t.Fatalf("release events should expose typed release target and audit trail: %s", stdout)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "release", "logs", "release:"+releaseID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("release logs exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	assertEvidenceShape(t, decodeJSONMap(t, stdout))
	if !strings.Contains(stdout, "apply-output") || !strings.Contains(stdout, "smoke-output") {
		t.Fatalf("release logs should include apply and smoke command output: %s", stdout)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "release", "smoke", "demo-evidence", "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("release smoke exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	smokePayload := decodeJSONMap(t, stdout)
	assertEvidenceShape(t, smokePayload)
	if smokePayload["targetRef"].(map[string]any)["ref"] != "release:"+releaseID || smokePayload["smoke"].(map[string]any)["ok"] != true || !strings.Contains(stdout, "smoke-output") {
		t.Fatalf("release smoke should rerun the configured smoke gate against current release: %s", stdout)
	}
}

func TestCommandDeploySmokeFailureDoesNotBecomeCurrentRelease(t *testing.T) {
	a := &App{Home: t.TempDir()}
	projectDir := writeReleaseEvidenceDeployFixture(t, true)

	plan, err := a.DeployPlan(projectDir, "production")
	if err != nil {
		t.Fatal(err)
	}
	release, err := a.ApplyDeployPlan(plan.ID)
	if err == nil || !strings.Contains(err.Error(), "deploy smoke failed") {
		t.Fatalf("expected smoke-gated apply failure, release=%#v err=%v", release, err)
	}
	if release.Status != "smoke_failed" || !hasAudit(release.Audit, "smoke", "failed") {
		t.Fatalf("failed smoke should be audited and mark the candidate release failed: %#v", release)
	}
	if len(release.Artifacts) == 0 {
		t.Fatalf("failed smoke should store command output artifacts: %#v", release)
	}
	_, err = a.CurrentRelease("demo-evidence", "production")
	if err == nil || !strings.Contains(err.Error(), "no current release") {
		t.Fatalf("smoke-failed deploy must not become current release, got %v", err)
	}
}

func TestRunReleaseSmokeFailureReturnsEvidenceJSON(t *testing.T) {
	home := t.TempDir()
	a := &App{Home: home}
	projectDir := writeReleaseEvidenceDeployFixture(t, false)

	plan, err := a.DeployPlan(projectDir, "production")
	if err != nil {
		t.Fatal(err)
	}
	release, err := a.ApplyDeployPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan.SmokeCommand = "printf smoke-failed; exit 12"
	if err := a.saveDeployPlan(plan); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLITestCommand(t, home, "release", "smoke", "demo-evidence", "--environment", "production", "--json", "--no-input")
	if code != 1 || stderr != "" {
		t.Fatalf("release smoke failure should return evidence JSON on stdout with exit 1, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	payload := decodeJSONMap(t, stdout)
	assertEvidenceShape(t, payload)
	if payload["targetRef"].(map[string]any)["ref"] != "release:"+release.ID {
		t.Fatalf("unexpected release target ref: %s", stdout)
	}
	if payload["ok"] != false || len(payload["nextSuggestedCommands"].([]any)) == 0 {
		t.Fatalf("failed release smoke should expose ok=false and next suggested commands: %s", stdout)
	}
	smoke := payload["smoke"].(map[string]any)
	if smoke["ok"] != false || !strings.Contains(smoke["output"].(string), "smoke-failed") || smoke["error"] == "" {
		t.Fatalf("release smoke failure should include output and error evidence: %s", stdout)
	}
	updated := payload["release"].(map[string]any)
	if updated["status"] != "smoke_failed" || !strings.Contains(stdout, "\"action\": \"smoke\"") {
		t.Fatalf("release smoke failure should update release evidence: %s", stdout)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", plan.ID, "--json", "--no-input")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "unsafe status smoke_failed") {
		t.Fatalf("reapplying a plan with unsafe current release should be blocked, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	countBytes, err := os.ReadFile(filepath.Join(projectDir, "deploy-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(countBytes)) != "1" {
		t.Fatalf("blocked reapply should not rerun apply command, count=%s", countBytes)
	}

	failedSmokePaths := artifactPathsByName(updated, "smoke")
	plan.SmokeCommand = "printf smoke-recovered"
	if err := a.saveDeployPlan(plan); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runCLITestCommand(t, home, "release", "smoke", "demo-evidence", "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("release smoke recovery exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	payload = decodeJSONMap(t, stdout)
	recovered := payload["release"].(map[string]any)
	if recovered["status"] != "smoke_ok" || payload["smoke"].(map[string]any)["ok"] != true || !strings.Contains(stdout, "smoke-recovered") {
		t.Fatalf("successful smoke rerun should recover smoke_failed current release: %s", stdout)
	}
	recoveredSmokePaths := artifactPathsByName(recovered, "smoke")
	if len(recoveredSmokePaths) <= len(failedSmokePaths) {
		t.Fatalf("smoke rerun should append a new artifact path, before=%v after=%v", failedSmokePaths, recoveredSmokePaths)
	}
	seenPaths := map[string]bool{}
	for _, path := range recoveredSmokePaths {
		if seenPaths[path] {
			t.Fatalf("smoke artifact paths must be unique across reruns: %v", recoveredSmokePaths)
		}
		seenPaths[path] = true
	}

	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", plan.ID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("reapplying recovered release should return existing release, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	reapplied := decodeJSONMap(t, stdout)["release"].(map[string]any)
	if reapplied["id"] != release.ID {
		t.Fatalf("reapply should return existing release after smoke recovery: %s", stdout)
	}
	countBytes, err = os.ReadFile(filepath.Join(projectDir, "deploy-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(countBytes)) != "1" {
		t.Fatalf("reapplied should not rerun apply command, count=%s", countBytes)
	}
}

func TestRunReleaseSmokeMissingCommandReturnsActionableJSON(t *testing.T) {
	home := t.TempDir()
	projectDir := writeDeployFixture(t, true)

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	planID := decodeJSONMap(t, stdout)["plan"].(map[string]any)["id"].(string)
	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy apply exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	releaseID := decodeJSONMap(t, stdout)["release"].(map[string]any)["id"].(string)

	code, stdout, stderr = runCLITestCommand(t, home, "release", "smoke", "demo", "--environment", "production", "--json", "--no-input")
	if code != 1 || stderr != "" {
		t.Fatalf("missing release smoke command should return JSON stdout and exit 1, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	payload := decodeJSONMap(t, stdout)
	assertEvidenceShape(t, payload)
	if payload["targetRef"].(map[string]any)["ref"] != "release:"+releaseID {
		t.Fatalf("unexpected release target ref: %s", stdout)
	}
	if payload["ok"] != false || len(payload["nextSuggestedCommands"].([]any)) == 0 {
		t.Fatalf("missing release smoke command should expose ok=false and next suggested commands: %s", stdout)
	}
	smoke := payload["smoke"].(map[string]any)
	if smoke["ok"] != false || !strings.Contains(smoke["error"].(string), "no smoke command") {
		t.Fatalf("missing release smoke command should expose actionable smoke error: %s", stdout)
	}
}

func TestCommandDeployPrepareCachePhasesAndEvidence(t *testing.T) {
	home := t.TempDir()
	projectDir := writeFastDeployFixture(t)

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	planMap := decodeJSONMap(t, stdout)["plan"].(map[string]any)
	if planMap["prepareCommand"] == "" || planMap["applyCommand"] == "" {
		t.Fatalf("command deploy plan should expose prepare/apply commands: %#v", planMap)
	}
	if got := phaseNames(planMap["phases"].([]any)); strings.Join(got, ",") != "prepare,apply,smoke,status" {
		t.Fatalf("command deploy plan should expose phase order, got %v", got)
	}
	cache := planMap["cache"].(map[string]any)
	if cache["dir"] != filepath.Join(projectDir, ".vivero", "cache", "deploy") {
		t.Fatalf("deploy cache dir should be resolved relative to project root: %#v", cache)
	}
	buildCache := cache["build"].(map[string]any)
	if buildCache["enabled"] != true || len(buildCache["from"].([]any)) != 1 || len(buildCache["to"].([]any)) != 1 {
		t.Fatalf("deploy plan should expose build cache hints: %#v", cache)
	}

	planID := planMap["id"].(string)
	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy apply exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	applyPayload := decodeJSONMap(t, stdout)
	assertEvidenceShape(t, applyPayload)
	release := applyPayload["release"].(map[string]any)
	if release["status"] != "applied" {
		t.Fatalf("successful command deploy should finish applied, got %#v", release)
	}
	if got := phaseRecordNames(release["phases"].([]any)); strings.Join(got, ",") != "prepare:succeeded,apply:succeeded,smoke:succeeded,status:succeeded" {
		t.Fatalf("unexpected command deploy phase records: %v", got)
	}
	for _, raw := range release["phases"].([]any) {
		phase := raw.(map[string]any)
		if _, ok := phase["durationMs"].(float64); !ok {
			t.Fatalf("phase missing durationMs evidence: %#v", phase)
		}
		artifact, ok := phase["artifact"].(map[string]any)
		if !ok || artifact["path"] == "" {
			t.Fatalf("phase missing command-output artifact path: %#v", phase)
		}
	}
	orderBytes, err := os.ReadFile(filepath.Join(projectDir, "deploy-order.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(orderBytes)); got != "prepare:prepare\napply:apply\nsmoke:smoke\nstatus:status" {
		t.Fatalf("unexpected command phase order/actions:\n%s", got)
	}
	cacheEnvBytes, err := os.ReadFile(filepath.Join(projectDir, "deploy-cache-env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cacheEnv := string(cacheEnvBytes)
	for _, want := range []string{"cache-dir=" + filepath.Join(projectDir, ".vivero", "cache", "deploy"), "VIVERO_BUILD_CACHE_FROM", "type=local,src=", "VIVERO_BUILD_CACHE_TO", "type=local,dest="} {
		if !strings.Contains(cacheEnv, want) {
			t.Fatalf("deploy cache env proof missing %q:\n%s", want, cacheEnv)
		}
	}

	code, stdout, stderr = runCLITestCommand(t, home, "release", "logs", "release:"+release["id"].(string), "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("release logs exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	logs := decodeJSONMap(t, stdout)["logs"].([]any)
	content := stdout
	for _, want := range []string{"prepare-output", "apply-output", "smoke-output", "live-status", "\"path\":"} {
		if !strings.Contains(content, want) {
			t.Fatalf("release logs should include phase output and artifact paths, missing %q: %#v", want, logs)
		}
	}
}

func TestRunDeployBlueGreenPlanApplyStatusRollbackJSONContract(t *testing.T) {
	home := t.TempDir()
	projectDir := writeBlueGreenDeployFixture(t, false)

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("blue/green deploy plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	plan := decodeJSONMap(t, stdout)["plan"].(map[string]any)
	if plan["strategy"] != "blue-green" || plan["ok"] != true || plan["verdict"] != "ready" {
		t.Fatalf("unexpected blue/green plan summary: %#v", plan)
	}
	blueGreen := plan["blueGreen"].(map[string]any)
	if blueGreen["activeSlot"] != "blue" || blueGreen["targetSlot"] != "green" {
		t.Fatalf("blue/green plan should target inactive slot: %#v", blueGreen)
	}
	phases := blueGreen["phases"].([]any)
	if got := phaseNames(phases); strings.Join(got, ",") != "prepare,smoke,promote" {
		t.Fatalf("blue/green plan should expose phase order, got %v", got)
	}

	planID := plan["id"].(string)
	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("blue/green deploy apply exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	applyRelease := decodeJSONMap(t, stdout)["release"].(map[string]any)
	if applyRelease["status"] != "promoted" || applyRelease["strategy"] != "blue-green" || applyRelease["activeSlot"] != "green" || applyRelease["previousSlot"] != "blue" {
		t.Fatalf("unexpected blue/green release after apply: %#v", applyRelease)
	}
	if got := phaseRecordNames(applyRelease["phases"].([]any)); strings.Join(got, ",") != "prepare:succeeded,smoke:succeeded,promote:succeeded" {
		t.Fatalf("unexpected blue/green apply phase records: %v", got)
	}
	logBytes, err := os.ReadFile(filepath.Join(projectDir, "blue-green.log"))
	if err != nil {
		t.Fatalf("blue/green phases should write proof log: %v", err)
	}
	log := string(logBytes)
	for _, want := range []string{"prepare:blue:green", "smoke:blue:green", "promote:blue:green"} {
		if !strings.Contains(log, want) {
			t.Fatalf("blue/green proof log missing %q:\n%s", want, log)
		}
	}

	code, stdout, stderr = runCLITestCommand(t, home, "release", "status", "demo-bg", "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("blue/green release status exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	statusPayload := decodeJSONMap(t, stdout)
	statusRelease := statusPayload["release"].(map[string]any)
	if statusPayload["status"] != "live-green" || statusRelease["activeSlot"] != "green" {
		t.Fatalf("unexpected blue/green release status: %#v", statusPayload)
	}
	statusBytes, err := os.ReadFile(filepath.Join(projectDir, "blue-green-status.txt"))
	if err != nil || !strings.Contains(string(statusBytes), "status:green") {
		t.Fatalf("blue/green status command should receive active slot, err=%v proof=%s", err, statusBytes)
	}

	releaseID := applyRelease["id"].(string)
	code, stdout, stderr = runCLITestCommand(t, home, "release", "rollback", "demo-bg", releaseID, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("blue/green rollback exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	rollbackRelease := decodeJSONMap(t, stdout)["release"].(map[string]any)
	if rollbackRelease["status"] != "rolled_back" || rollbackRelease["rollbackOf"] != releaseID || rollbackRelease["activeSlot"] != "blue" || rollbackRelease["previousSlot"] != "green" {
		t.Fatalf("unexpected blue/green rollback release: %#v", rollbackRelease)
	}
	rollbackBytes, err := os.ReadFile(filepath.Join(projectDir, "blue-green-rollback.txt"))
	if err != nil || !strings.Contains(string(rollbackBytes), "rollback:green:blue") {
		t.Fatalf("blue/green rollback should switch back to previous slot, err=%v proof=%s", err, rollbackBytes)
	}
}

func TestRunDeployBlueGreenPlanRequiresPromotionGate(t *testing.T) {
	home := t.TempDir()
	projectDir := writeBlueGreenDeployFixture(t, true)

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code == 0 || stderr != "" {
		t.Fatalf("blue/green plan without smoke gate should fail with JSON stdout, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	plan := decodeJSONMap(t, stdout)["plan"].(map[string]any)
	if plan["ok"] != false || plan["verdict"] != "blocked" {
		t.Fatalf("blue/green plan missing smoke gate should be blocked: %#v", plan)
	}
	codes := map[string]bool{}
	for _, raw := range plan["diagnostics"].([]any) {
		diag := raw.(map[string]any)
		codes[diag["code"].(string)] = true
	}
	if !codes["blue-green-smoke-missing"] {
		t.Fatalf("blue/green blocked plan should explain missing smoke gate, got %#v", plan["diagnostics"])
	}
}

func TestRunDeployBlueGreenApplyStopsBeforePromoteWhenSmokeFails(t *testing.T) {
	home := t.TempDir()
	projectDir := writeBlueGreenDeployFixture(t, false)
	configPath := filepath.Join(projectDir, "vivero.yml")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config := strings.Replace(string(configBytes), "printf ''smoke:%s:%s\\n'' \"$VIVERO_BLUE_GREEN_ACTIVE_SLOT\" \"$VIVERO_BLUE_GREEN_TARGET_SLOT\" >> blue-green.log", "printf ''smoke:%s:%s\\n'' \"$VIVERO_BLUE_GREEN_ACTIVE_SLOT\" \"$VIVERO_BLUE_GREEN_TARGET_SLOT\" >> blue-green.log; exit 7", 1)
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("blue/green deploy plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	planID := decodeJSONMap(t, stdout)["plan"].(map[string]any)["id"].(string)

	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planID, "--json", "--no-input")
	if code != 1 || stderr != "" {
		t.Fatalf("blue/green apply with failing smoke should return release evidence JSON before promote, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	failedRelease := decodeJSONMap(t, stdout)["release"].(map[string]any)
	if failedRelease["status"] != "smoke_failed" {
		t.Fatalf("blue/green failing smoke should expose smoke_failed release evidence: %s", stdout)
	}
	logBytes, err := os.ReadFile(filepath.Join(projectDir, "blue-green.log"))
	if err != nil {
		t.Fatalf("blue/green failing smoke should still write proof log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "prepare:blue:green") || !strings.Contains(log, "smoke:blue:green") || strings.Contains(log, "promote:blue:green") {
		t.Fatalf("blue/green failing smoke should stop before promote:\n%s", log)
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

func TestApplyDeployPlanIsIdempotentAuditedAndVersioned(t *testing.T) {
	a := &App{Home: t.TempDir()}
	projectDir := writeCountingDeployFixture(t)

	plan, err := a.DeployPlan(projectDir, "production")
	if err != nil {
		t.Fatal(err)
	}
	if plan.StateVersion != deployStateVersion {
		t.Fatalf("deploy plan should carry state version %d, got %#v", deployStateVersion, plan)
	}
	changes := map[string]bool{}
	for _, change := range plan.Changes {
		changes[change.Kind] = change.Summary != ""
	}
	if !changes["service-image"] || !changes["deploy-strategy"] {
		t.Fatalf("deploy plan should summarize service and strategy changes, got %#v", plan.Changes)
	}

	first, err := a.ApplyDeployPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.StateVersion != deployStateVersion || first.Status != "applied" {
		t.Fatalf("unexpected first release: %#v", first)
	}
	if !hasAudit(first.Audit, "apply", "started") || !hasAudit(first.Audit, "apply", "succeeded") {
		t.Fatalf("applied release should include apply audit trail: %#v", first.Audit)
	}

	second, err := a.ApplyDeployPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("applying the same plan twice should return existing release, first=%s second=%s", first.ID, second.ID)
	}
	countBytes, err := os.ReadFile(filepath.Join(projectDir, "deploy-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(countBytes)) != "1" {
		t.Fatalf("idempotent apply should run app-owned command once, count=%s", countBytes)
	}
	current, err := a.CurrentRelease("demo-counting", "production")
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != first.ID || current.Status != "applied" {
		t.Fatalf("current release should remain the applied release: %#v", current)
	}
}

func TestFailedDeployApplyKeepsCurrentReleaseCleanAndStoresArtifact(t *testing.T) {
	a := &App{Home: t.TempDir()}
	projectDir := writeFailingDeployFixture(t)

	plan, err := a.DeployPlan(projectDir, "production")
	if err != nil {
		t.Fatal(err)
	}
	release, err := a.ApplyDeployPlan(plan.ID)
	if err == nil || !strings.Contains(err.Error(), "deploy apply failed") {
		t.Fatalf("expected deploy apply failure, release=%#v err=%v", release, err)
	}
	if release.Status != "failed" || !hasAudit(release.Audit, "apply", "failed") {
		t.Fatalf("failed release should keep failed status and audit trail: %#v", release)
	}
	if len(release.Artifacts) != 1 {
		t.Fatalf("failed release should store command output artifact: %#v", release.Artifacts)
	}
	artifactBytes, err := os.ReadFile(release.Artifacts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(artifactBytes), "boom-output") {
		t.Fatalf("artifact should contain failed command output, got %q", artifactBytes)
	}
	_, err = a.CurrentRelease("demo-failing", "production")
	if err == nil || !strings.Contains(err.Error(), "no current release") {
		t.Fatalf("failed apply should not become current release, got %v", err)
	}
}

func TestRunDeployApplyFailureReturnsReleaseEvidenceJSON(t *testing.T) {
	home := t.TempDir()
	projectDir := writeFailingDeployFixture(t)

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	planID := decodeJSONMap(t, stdout)["plan"].(map[string]any)["id"].(string)

	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planID, "--json", "--no-input")
	if code != 1 || stderr != "" {
		t.Fatalf("failed deploy apply should return release evidence JSON on stdout, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	payload := decodeJSONMap(t, stdout)
	release := payload["release"].(map[string]any)
	if release["status"] != "failed" || !strings.Contains(payload["error"].(map[string]any)["message"].(string), "deploy apply failed") {
		t.Fatalf("failed deploy apply should expose failed release and error: %s", stdout)
	}
	if len(artifactPathsByName(release, "apply")) != 1 || !strings.Contains(stdout, "boom-output") {
		t.Fatalf("failed deploy apply should expose command output artifact: %s", stdout)
	}
}

func TestRunDeployBlueGreenPromoteFailureReturnsEvidenceJSON(t *testing.T) {
	home := t.TempDir()
	projectDir := writeBlueGreenDeployFixture(t, false)
	configPath := filepath.Join(projectDir, "vivero.yml")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config := strings.Replace(string(configBytes), "printf ''promote:%s:%s\\n'' \"$VIVERO_BLUE_GREEN_ACTIVE_SLOT\" \"$VIVERO_BLUE_GREEN_TARGET_SLOT\" >> blue-green.log", "printf ''promote:%s:%s\\n'' \"$VIVERO_BLUE_GREEN_ACTIVE_SLOT\" \"$VIVERO_BLUE_GREEN_TARGET_SLOT\" >> blue-green.log; exit 9", 1)
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("blue/green deploy plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	planID := decodeJSONMap(t, stdout)["plan"].(map[string]any)["id"].(string)

	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planID, "--json", "--no-input")
	if code != 1 || stderr != "" {
		t.Fatalf("blue/green promote failure should return release evidence JSON on stdout, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	payload := decodeJSONMap(t, stdout)
	release := payload["release"].(map[string]any)
	if release["status"] != "promote_failed" || !strings.Contains(payload["error"].(map[string]any)["message"].(string), "promote_failed") {
		t.Fatalf("promote failure should expose failed release and error: %s", stdout)
	}
	if got := phaseRecordNames(release["phases"].([]any)); strings.Join(got, ",") != "prepare:succeeded,smoke:succeeded,promote:failed" {
		t.Fatalf("promote failure should persist completed and failed phases: %v", got)
	}
	if len(artifactPathsByName(release, "promote")) != 1 {
		t.Fatalf("promote failure should store phase output artifact: %s", stdout)
	}
	_, err = (&App{Home: home}).CurrentRelease("demo-bg", "production")
	if err == nil || !strings.Contains(err.Error(), "no current release") {
		t.Fatalf("promote-failed deploy must not become current release, got %v", err)
	}
}

func TestReleaseStatusFailurePersistsEvidenceJSON(t *testing.T) {
	home := t.TempDir()
	a := &App{Home: home}
	projectDir := writeReleaseEvidenceDeployFixture(t, false)

	plan, err := a.DeployPlan(projectDir, "production")
	if err != nil {
		t.Fatal(err)
	}
	release, err := a.ApplyDeployPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan.StatusCommand = "printf status-boom; exit 42"
	if err := a.saveDeployPlan(plan); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLITestCommand(t, home, "release", "status", "demo-evidence", "--environment", "production", "--json", "--no-input")
	if code != 1 || stderr != "" {
		t.Fatalf("failed release status should return release evidence JSON on stdout, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	payload := decodeJSONMap(t, stdout)
	updated := payload["release"].(map[string]any)
	if updated["id"] != release.ID || updated["status"] != "status_failed" || !strings.Contains(payload["error"].(map[string]any)["message"].(string), "release status failed") {
		t.Fatalf("status failure should expose status_failed release and error: %s", stdout)
	}
	if len(artifactPathsByName(updated, "status")) != 1 || !strings.Contains(stdout, "status-boom") {
		t.Fatalf("status failure should expose command output artifact: %s", stdout)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "release", "events", "release:"+release.ID, "--json", "--no-input")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "\"action\": \"status\"") || !strings.Contains(stdout, "\"status\": \"failed\"") {
		t.Fatalf("status failure should be inspectable through release events, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLITestCommand(t, home, "release", "logs", "release:"+release.ID, "--json", "--no-input")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "status-boom") {
		t.Fatalf("status failure should be inspectable through release logs, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunReleaseRollbackFailureReturnsEvidenceJSONAndKeepsCurrent(t *testing.T) {
	home := t.TempDir()
	a := &App{Home: home}
	projectDir := writeReleaseEvidenceDeployFixture(t, false)

	plan, err := a.DeployPlan(projectDir, "production")
	if err != nil {
		t.Fatal(err)
	}
	release, err := a.ApplyDeployPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan.RollbackCommand = "printf rollback-boom; exit 44"
	if err := a.saveDeployPlan(plan); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLITestCommand(t, home, "release", "rollback", "demo-evidence", release.ID, "--environment", "production", "--json", "--no-input")
	if code != 1 || stderr != "" {
		t.Fatalf("failed rollback should return release evidence JSON on stdout, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	payload := decodeJSONMap(t, stdout)
	rollback := payload["release"].(map[string]any)
	if rollback["status"] != "rollback_failed" || rollback["rollbackOf"] != release.ID || !strings.Contains(payload["error"].(map[string]any)["message"].(string), "release rollback failed") {
		t.Fatalf("rollback failure should expose failed rollback release and error: %s", stdout)
	}
	if len(artifactPathsByName(rollback, "rollback")) != 1 || !strings.Contains(stdout, "rollback-boom") {
		t.Fatalf("rollback failure should expose command output artifact: %s", stdout)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "release", "status", "demo-evidence", "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("rollback failure should leave original release current, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	current := decodeJSONMap(t, stdout)["release"].(map[string]any)
	if current["id"] != release.ID {
		t.Fatalf("rollback failure should leave original release current: %s", stdout)
	}
}

func TestDeployLockRejectsConcurrentMutation(t *testing.T) {
	a := &App{Home: t.TempDir()}
	lockPath := a.deployLockPath("demo/prod", "blue/green")
	if lockPath != (&App{Home: a.Home}).deployLockPath("demo/prod", "blue/green") {
		t.Fatalf("deploy lock path should be deterministic")
	}
	if a.deployLockPath("demo-prod", "blue/green") == a.deployLockPath("demo", "prod-blue/green") {
		t.Fatalf("deploy lock path should avoid ambiguous project/environment collisions")
	}

	unlock, err := a.acquireDeployLock("demo", "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.acquireDeployLock("demo", "production"); err == nil || !strings.Contains(err.Error(), "deploy lock already held") {
		t.Fatalf("expected concurrent deploy lock rejection, got %v", err)
	}
	unlock()

	unlockAgain, err := a.acquireDeployLock("demo", "production")
	if err != nil {
		t.Fatalf("lock should be reusable after unlock: %v", err)
	}
	unlockAgain()
}

func TestDeployLockRemovesStaleRecordBeforeAcquiring(t *testing.T) {
	a := &App{Home: t.TempDir()}
	if err := ensureDir(a.deployLockDir()); err != nil {
		t.Fatal(err)
	}
	stale := map[string]any{"project": "demo", "environment": "production", "pid": os.Getpid(), "createdAt": nowUTC().Add(-5 * time.Hour)}
	if err := writeIndentedJSONFile(a.deployLockPath("demo", "production"), stale, 0o644); err != nil {
		t.Fatal(err)
	}

	unlock, err := a.acquireDeployLock("demo", "production")
	if err != nil {
		t.Fatalf("stale lock should be removed before acquiring: %v", err)
	}
	unlock()
}

func TestDeployApplyLockContentionReturnsStableJSONError(t *testing.T) {
	home := t.TempDir()
	a := &App{Home: home}
	projectDir := writeCountingDeployFixture(t)
	plan, err := a.DeployPlan(projectDir, "production")
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := a.acquireDeployLock(plan.Project, plan.Environment)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "apply", plan.ID, "--json", "--no-input")
	if code != 1 || stdout != "" {
		t.Fatalf("lock contention should return a JSON error on stderr only, code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	payload := decodeJSONMap(t, stderr)
	if !strings.Contains(payload["error"].(map[string]any)["message"].(string), "deploy lock already held for demo-counting/production") {
		t.Fatalf("lock contention should include stable project/environment message: %s", stderr)
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

	code, stdout, stderr = runCLITestCommand(t, t.TempDir(), "schema", "deploy", "plan", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy plan schema exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "blue-green") || !strings.Contains(stdout, "blueGreen") {
		t.Fatalf("deploy plan schema should disclose blue/green strategy support:\n%s", stdout)
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

func writeCountingDeployFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config := "project:\n" +
		"  name: demo-counting\n" +
		"services:\n" +
		"  web:\n" +
		"    image: registry.example.com/demo-counting@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\n" +
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
		"      applyCommand: 'count=0; test -f deploy-count.txt && count=$(cat deploy-count.txt); count=$((count+1)); printf \"%s\" \"$count\" > deploy-count.txt; printf applied:$VIVERO_DEPLOY_PLAN_ID:$VIVERO_RELEASE_ID > deploy-applied.txt'\n" +
		"      statusCommand: 'printf applied'\n" +
		"      rollbackCommand: 'printf rollback:$VIVERO_ROLLBACK_RELEASE_ID > deploy-rollback.txt'\n"
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFailingDeployFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config := "project:\n" +
		"  name: demo-failing\n" +
		"services:\n" +
		"  web:\n" +
		"    image: registry.example.com/demo-failing@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd\n" +
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
		"      applyCommand: 'printf boom-output; exit 23'\n" +
		"      statusCommand: 'printf should-not-run'\n" +
		"      rollbackCommand: 'printf rollback:$VIVERO_ROLLBACK_RELEASE_ID > deploy-rollback.txt'\n"
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func hasAudit(events []DeployAuditEvent, action, status string) bool {
	for _, event := range events {
		if event.Action == action && event.Status == status {
			return true
		}
	}
	return false
}

func writeReleaseEvidenceDeployFixture(t *testing.T, failingSmoke bool) string {
	t.Helper()
	dir := t.TempDir()
	smokeCommand := "printf smoke-output:$VIVERO_RELEASE_ID; printf smoke:$VIVERO_RELEASE_ID > deploy-smoke.txt"
	if failingSmoke {
		smokeCommand = "printf smoke-output:$VIVERO_RELEASE_ID; printf smoke:$VIVERO_RELEASE_ID > deploy-smoke.txt; exit 12"
	}
	config := "project:\n" +
		"  name: demo-evidence\n" +
		"services:\n" +
		"  web:\n" +
		"    image: registry.example.com/demo-evidence@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\n" +
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
		"      applyCommand: 'count=0; test -f deploy-count.txt && count=$(cat deploy-count.txt); count=$((count+1)); printf \"%s\" \"$count\" > deploy-count.txt; printf apply-output; printf applied:$VIVERO_DEPLOY_PLAN_ID:$VIVERO_RELEASE_ID > deploy-applied.txt'\n" +
		"      statusCommand: 'printf applied'\n" +
		"      smokeCommand: '" + smokeCommand + "'\n" +
		"      rollbackCommand: 'printf rollback:$VIVERO_ROLLBACK_RELEASE_ID > deploy-rollback.txt'\n"
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFastDeployFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/env sh
set -eu
action="${1:-}"
case "$action" in
  prepare)
    mkdir -p "$VIVERO_CACHE_DIR"
    printf 'prepare:%s\n' "$VIVERO_RELEASE_ACTION" >> deploy-order.txt
    {
      printf 'cache-dir=%s\n' "$VIVERO_CACHE_DIR"
      printf 'VIVERO_BUILD_CACHE_FROM=%s\n' "${VIVERO_BUILD_CACHE_FROM:-}"
      printf 'VIVERO_BUILD_CACHE_TO=%s\n' "${VIVERO_BUILD_CACHE_TO:-}"
    } > deploy-cache-env.txt
    printf 'prepare-output'
    ;;
  apply)
    printf 'apply:%s\n' "$VIVERO_RELEASE_ACTION" >> deploy-order.txt
    printf 'apply-output'
    ;;
  smoke)
    printf 'smoke:%s\n' "$VIVERO_RELEASE_ACTION" >> deploy-order.txt
    printf 'smoke-output'
    ;;
  status)
    printf 'status:%s\n' "$VIVERO_RELEASE_ACTION" >> deploy-order.txt
    printf 'live-status'
    ;;
  rollback)
    printf 'rollback:%s\n' "$VIVERO_ROLLBACK_RELEASE_ID" > deploy-rollback.txt
    ;;
  *)
    exit 64
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "scripts", "deploy.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	config := `project:
  name: demo-fast-deploy
services:
  web:
    image: registry.example.com/demo-fast-deploy@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
    port: 3000
    health:
      path: /
      timeout: 30s
    resources:
      cpus: '1'
      memory: 512m
deploy:
  environments:
    production:
      prepareCommand: 'sh scripts/deploy.sh prepare'
      applyCommand: 'sh scripts/deploy.sh apply'
      smokeCommand: 'sh scripts/deploy.sh smoke'
      statusCommand: 'sh scripts/deploy.sh status'
      rollbackCommand: 'sh scripts/deploy.sh rollback'
      cache:
        dir: .vivero/cache/deploy
        build:
          from:
            - type=local,src=.vivero/cache/build/web
          to:
            - type=local,dest=.vivero/cache/build/web,mode=max
`
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
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

func writeBlueGreenDeployFixture(t *testing.T, omitSmoke bool) string {
	t.Helper()
	dir := t.TempDir()
	smoke := "        smokeCommand: 'printf ''smoke:%s:%s\\n'' \"$VIVERO_BLUE_GREEN_ACTIVE_SLOT\" \"$VIVERO_BLUE_GREEN_TARGET_SLOT\" >> blue-green.log'\n"
	if omitSmoke {
		smoke = ""
	}
	config := "project:\n" +
		"  name: demo-bg\n" +
		"services:\n" +
		"  web:\n" +
		"    image: registry.example.com/demo-bg@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n" +
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
		"      strategy: blue-green\n" +
		"      blueGreen:\n" +
		"        slots: [blue, green]\n" +
		"        activeSlotCommand: 'test \"$VIVERO_BLUE_GREEN_SLOTS\" = \"blue,green\" && printf blue'\n" +
		"        prepareCommand: 'printf ''prepare:%s:%s\\n'' \"$VIVERO_BLUE_GREEN_ACTIVE_SLOT\" \"$VIVERO_BLUE_GREEN_TARGET_SLOT\" >> blue-green.log'\n" +
		smoke +
		"        promoteCommand: 'printf ''promote:%s:%s\\n'' \"$VIVERO_BLUE_GREEN_ACTIVE_SLOT\" \"$VIVERO_BLUE_GREEN_TARGET_SLOT\" >> blue-green.log'\n" +
		"        statusCommand: 'printf ''status:%s\\n'' \"$VIVERO_BLUE_GREEN_ACTIVE_SLOT\" > blue-green-status.txt; printf live-green'\n" +
		"        rollbackCommand: 'printf ''rollback:%s:%s\\n'' \"$VIVERO_BLUE_GREEN_ACTIVE_SLOT\" \"$VIVERO_BLUE_GREEN_TARGET_SLOT\" > blue-green-rollback.txt'\n"
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func decodeJSONMap(t *testing.T, payload string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		t.Fatalf("invalid JSON: %v payload=%s", err, payload)
	}
	return out
}

func phaseNames(phases []any) []string {
	names := make([]string, 0, len(phases))
	for _, raw := range phases {
		phase := raw.(map[string]any)
		names = append(names, phase["name"].(string))
	}
	return names
}

func phaseRecordNames(phases []any) []string {
	names := make([]string, 0, len(phases))
	for _, raw := range phases {
		phase := raw.(map[string]any)
		names = append(names, phase["name"].(string)+":"+phase["status"].(string))
	}
	return names
}

func artifactPathsByName(release map[string]any, name string) []string {
	artifacts, _ := release["artifacts"].([]any)
	paths := make([]string, 0, len(artifacts))
	for _, raw := range artifacts {
		artifact := raw.(map[string]any)
		if artifact["name"] != name {
			continue
		}
		path, _ := artifact["path"].(string)
		paths = append(paths, path)
	}
	return paths
}
