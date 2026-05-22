package vivero

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeployApplyCommandTimeoutPersistsFailedEvidenceAndReleasesLock(t *testing.T) {
	home := t.TempDir()
	projectDir := writeTimeoutDeployFixture(t)

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
	if planPayload.Plan.CommandTimeout != "50ms" {
		t.Fatalf("plan should preserve configured command timeout: %#v", planPayload.Plan)
	}

	start := time.Now()
	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planPayload.Plan.ID, "--json", "--no-input")
	elapsed := time.Since(start)
	if code == 0 {
		t.Fatalf("deploy apply should fail on timeout, stdout=%s stderr=%s", stdout, stderr)
	}
	if elapsed > 750*time.Millisecond {
		t.Fatalf("deploy apply should respect configured timeout quickly, took %s", elapsed)
	}
	if stderr != "" {
		t.Fatalf("deploy apply failure with release evidence should be stdout JSON, stderr=%s", stderr)
	}
	if !strings.Contains(stdout, "timed out") {
		t.Fatalf("timeout failure should be visible in JSON output, stdout=%s", stdout)
	}
	var failure struct {
		Release ReleaseRecord `json:"release"`
		Error   any           `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &failure); err != nil {
		t.Fatalf("invalid timeout failure JSON: %v stdout=%s", err, stdout)
	}
	if failure.Release.Status != "failed" {
		t.Fatalf("timed-out apply should persist failed release, got %#v", failure.Release)
	}
	if len(failure.Release.Phases) != 1 || failure.Release.Phases[0].Status != "failed" {
		t.Fatalf("timed-out apply should persist failed phase, got %#v", failure.Release.Phases)
	}

	projectDir2 := writeFastDeployFixtureForProject(t, "demo-timeout")
	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "plan", projectDir2, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("second deploy plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var planPayload2 struct {
		Plan DeployPlan `json:"plan"`
	}
	if err := json.Unmarshal([]byte(stdout), &planPayload2); err != nil {
		t.Fatalf("invalid second plan JSON: %v stdout=%s", err, stdout)
	}
	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planPayload2.Plan.ID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy lock should be released after timeout; second apply exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestDeployCommandOutputIsCappedAndRedactedInJSONLogsAndArtifacts(t *testing.T) {
	home := t.TempDir()
	secret := "vivero-super-secret-token-12345"
	t.Setenv("VIVERO_TEST_SECRET_TOKEN", secret)
	projectDir := writeSensitiveOutputDeployFixture(t)

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

	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planPayload.Plan.ID, "--json", "--no-input")
	if code == 0 || stderr != "" {
		t.Fatalf("sensitive deploy should fail with stdout JSON only, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, secret) {
		t.Fatalf("deploy failure JSON leaked secret: %s", stdout)
	}
	if !strings.Contains(stdout, "[REDACTED]") || !strings.Contains(stdout, "output truncated") {
		t.Fatalf("deploy failure JSON should show redaction and truncation markers, stdout=%s", stdout)
	}
	var failure struct {
		Release ReleaseRecord `json:"release"`
	}
	if err := json.Unmarshal([]byte(stdout), &failure); err != nil {
		t.Fatalf("invalid deploy failure JSON: %v stdout=%s", err, stdout)
	}
	if len(failure.Release.Output) > deployCommandOutputLimit+512 {
		t.Fatalf("release output should be capped, length=%d", len(failure.Release.Output))
	}
	if len(failure.Release.Artifacts) == 0 {
		t.Fatalf("expected capped/redacted command output artifact: %#v", failure.Release)
	}
	artifactBytes, err := os.ReadFile(failure.Release.Artifacts[0].Path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	artifact := string(artifactBytes)
	if strings.Contains(artifact, secret) {
		t.Fatalf("artifact leaked secret")
	}
	if len(artifact) > deployCommandOutputLimit+512 || !strings.Contains(artifact, "output truncated") {
		t.Fatalf("artifact should be capped with truncation marker, length=%d content suffix=%q", len(artifact), tailString(artifact, 128))
	}

	code, stdout, stderr = runCLITestCommand(t, home, "release", "logs", "release:"+failure.Release.ID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("release logs exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, secret) {
		t.Fatalf("release logs leaked secret: %s", stdout)
	}
	if !strings.Contains(stdout, "[REDACTED]") || !strings.Contains(stdout, "output truncated") {
		t.Fatalf("release logs should include redacted/capped output, stdout=%s", stdout)
	}
}

func TestReleaseStatusRejectsUnsafeOutputAndDoesNotPersistRawStatus(t *testing.T) {
	home := t.TempDir()
	projectDir := writeUnsafeStatusDeployFixture(t)

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var planPayload struct {
		Plan DeployPlan `json:"plan"`
	}
	if err := json.Unmarshal([]byte(stdout), &planPayload); err != nil {
		t.Fatalf("invalid plan JSON: %v stdout=%s", err, stdout)
	}
	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planPayload.Plan.ID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy apply exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runCLITestCommand(t, home, "release", "status", "demo-unsafe-status", "--environment", "production", "--json", "--no-input")
	if code == 0 || stderr != "" {
		t.Fatalf("release status should fail with release evidence on unsafe output, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "invalid release status output") {
		t.Fatalf("unsafe status error should be explicit, stdout=%s", stdout)
	}
	var failure struct {
		Release ReleaseRecord `json:"release"`
	}
	if err := json.Unmarshal([]byte(stdout), &failure); err != nil {
		t.Fatalf("invalid status failure JSON: %v stdout=%s", err, stdout)
	}
	if failure.Release.Status != "status_failed" {
		t.Fatalf("unsafe status should persist status_failed, got %#v", failure.Release)
	}
	if strings.Contains(failure.Release.Status, "raw-status") || strings.Contains(failure.Release.Output, "raw-status\nsecond-line") {
		t.Fatalf("unsafe raw status should not become release status/output: %#v", failure.Release)
	}
}

func TestReleaseStatusHonorsDeployLockBeforeRunningAppOwnedStatusCommand(t *testing.T) {
	home := t.TempDir()
	projectDir := writeLockedStatusDeployFixture(t)

	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy plan exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var planPayload struct {
		Plan DeployPlan `json:"plan"`
	}
	if err := json.Unmarshal([]byte(stdout), &planPayload); err != nil {
		t.Fatalf("invalid plan JSON: %v stdout=%s", err, stdout)
	}
	code, stdout, stderr = runCLITestCommand(t, home, "deploy", "apply", planPayload.Plan.ID, "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("deploy apply exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	a := &App{Home: home}
	unlock, err := a.acquireDeployLock("demo-status-lock", "production")
	if err != nil {
		t.Fatalf("manual lock: %v", err)
	}
	defer unlock()
	code, stdout, stderr = runCLITestCommand(t, home, "release", "status", "demo-status-lock", "--environment", "production", "--json", "--no-input")
	if code == 0 {
		t.Fatalf("release status should respect held deploy lock, stdout=%s stderr=%s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "deploy lock already held") {
		t.Fatalf("lock error should be visible, stdout=%s stderr=%s", stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "status-ran.txt")); !os.IsNotExist(err) {
		t.Fatalf("status command should not run while deploy lock is held, stat err=%v", err)
	}
}

func TestBlueGreenActiveSlotCommandTimeoutBlocksPlanQuickly(t *testing.T) {
	home := t.TempDir()
	projectDir := writeBlueGreenTimeoutFixture(t)

	start := time.Now()
	code, stdout, stderr := runCLITestCommand(t, home, "deploy", "plan", projectDir, "--environment", "production", "--json", "--no-input")
	elapsed := time.Since(start)
	if code == 0 || stderr != "" {
		t.Fatalf("blue/green plan should be blocked by active-slot timeout, exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if elapsed > 750*time.Millisecond {
		t.Fatalf("active-slot command should respect status timeout quickly, took %s", elapsed)
	}
	if !strings.Contains(stdout, "blue-green-active-slot-timeout") || !strings.Contains(stdout, "timed out") {
		t.Fatalf("plan should include timeout diagnostic, stdout=%s", stdout)
	}
}

func TestAtomicReleaseWriteKeepsPreviousCurrentOnInjectedWriteFailure(t *testing.T) {
	a := &App{Home: t.TempDir()}
	projectDir := t.TempDir()
	plan := DeployPlan{ID: "plan-atomic", Project: "demo-atomic", Environment: "production", Path: projectDir, OK: true, StatusCommand: "printf applied"}
	if err := a.saveDeployPlan(plan); err != nil {
		t.Fatal(err)
	}
	release := newReleaseRecord(plan, "applied", "")
	if err := a.saveRelease(release); err != nil {
		t.Fatal(err)
	}
	updated := release
	updated.Status = "new-status"
	currentPath := a.currentReleasePath(plan.Project, plan.Environment)
	injected := errors.New("injected write failure")
	atomicWriteBeforeRenameHook = func(path string, body []byte) error {
		if path == currentPath {
			return injected
		}
		return nil
	}
	defer func() { atomicWriteBeforeRenameHook = nil }()

	if err := a.saveRelease(updated); !errors.Is(err, injected) {
		t.Fatalf("expected injected write failure, got %v", err)
	}
	current, err := a.loadCurrentRelease(plan.Project, plan.Environment)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "applied" {
		t.Fatalf("atomic write failure should leave previous current release intact, got %#v", current)
	}
}

func TestMalformedStaleDeployLockCanBeReclaimedButFreshMalformedLockBlocks(t *testing.T) {
	a := &App{Home: t.TempDir()}
	path := a.deployLockPath("demo-lock", "production")
	if err := ensureDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	unlock, err := a.acquireDeployLock("demo-lock", "production")
	if err != nil {
		t.Fatalf("stale malformed lock should be reclaimed: %v", err)
	}
	unlock()

	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.acquireDeployLock("demo-lock", "production"); err == nil || !strings.Contains(err.Error(), "deploy lock already held") {
		t.Fatalf("fresh malformed lock should block, got %v", err)
	}
}

func TestRunDeployCommandCancellationKillsChildProcessGroup(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		result deployCommandResult
		err    error
	}, 1)
	go func() {
		result, err := runDeployCommand(ctx, dir, nil, "sh -c 'sleep 2; printf child > child-ran.txt' & wait", deployCommandOptions{Timeout: 5 * time.Second, OutputLimit: 1024})
		done <- struct {
			result deployCommandResult
			err    error
		}{result: result, err: err}
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("expected cancellation error, result=%#v", got.result)
		}
		if !got.result.Canceled {
			t.Fatalf("expected canceled result, got %#v err=%v", got.result, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deploy command did not exit after cancellation")
	}
	time.Sleep(2300 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "child-ran.txt")); !os.IsNotExist(err) {
		t.Fatalf("canceled deploy command should kill child process group, stat err=%v", err)
	}
}

func TestAppDeployCommandsUseAppContextCancellation(t *testing.T) {
	a := &App{Home: t.TempDir()}
	projectDir := writeAppContextCancellationDeployFixture(t)
	plan, err := a.DeployPlan(projectDir, "production")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.Context = ctx
	cancel()

	start := time.Now()
	release, err := a.ApplyDeployPlan(plan.ID)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected deploy apply to honor canceled app context, release=%#v err=%v", release, err)
	}
	if time.Since(start) > 750*time.Millisecond {
		t.Fatalf("deploy apply ignored canceled app context")
	}
	if _, statErr := os.Stat(filepath.Join(projectDir, "app-context-ran.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("canceled app context should stop app-owned command before side effects, stat err=%v", statErr)
	}
}

func TestRunDeployCommandRedactsPartialSecretAtOutputLimit(t *testing.T) {
	dir := t.TempDir()
	secret := "vivero-partial-secret-token-abcdefghijklmnopqrstuvwxyz"
	result, err := runDeployCommand(context.Background(), dir, map[string]string{"VIVERO_PARTIAL_SECRET_TOKEN": secret}, `printf "$VIVERO_PARTIAL_SECRET_TOKEN"`, deployCommandOptions{Timeout: time.Second, OutputLimit: 12})
	if err != nil {
		t.Fatalf("run deploy command: %v", err)
	}
	if !result.Truncated {
		t.Fatalf("expected truncated result: %#v", result)
	}
	if strings.Contains(result.Output, secret[:12]) || strings.Contains(result.Output, secret[:8]) {
		t.Fatalf("partial secret leaked in output: %q", result.Output)
	}
	if !strings.Contains(result.Output, "[REDACTED]") || !strings.Contains(result.Output, "output truncated") {
		t.Fatalf("expected redaction and truncation markers, got %q", result.Output)
	}
}

func TestAppendReleaseOutputCapsAggregateOutput(t *testing.T) {
	got := appendReleaseOutput(strings.Repeat("a", deployCommandOutputLimit), "extra-output")
	if len(got) > deployCommandOutputLimit+512 {
		t.Fatalf("aggregate release output should stay capped, len=%d", len(got))
	}
	if !strings.Contains(got, "output truncated") {
		t.Fatalf("aggregate cap should include truncation marker")
	}
}

func writeTimeoutDeployFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config := `project:
  name: demo-timeout
services:
  web:
    image: registry.example.com/demo-timeout@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
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
      commandTimeout: 50ms
      applyCommand: 'sleep 1; printf should-not-complete'
      statusCommand: 'printf applied'
      rollbackCommand: 'printf rollback:$VIVERO_ROLLBACK_RELEASE_ID > deploy-rollback.txt'
`
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFastDeployFixtureForProject(t *testing.T, project string) string {
	t.Helper()
	dir := t.TempDir()
	config := `project:
  name: ` + project + `
services:
  web:
    image: registry.example.com/` + project + `@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
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
      applyCommand: 'printf applied > deploy-applied.txt'
      statusCommand: 'printf applied'
      rollbackCommand: 'printf rollback:$VIVERO_ROLLBACK_RELEASE_ID > deploy-rollback.txt'
`
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeSensitiveOutputDeployFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config := `project:
  name: demo-sensitive-output
services:
  web:
    image: registry.example.com/demo-sensitive-output@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
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
      applyCommand: 'printf "$VIVERO_TEST_SECRET_TOKEN"; dd if=/dev/zero bs=1024 count=80 2>/dev/null | tr "\000" x; exit 42'
      statusCommand: 'printf applied'
      rollbackCommand: 'printf rollback:$VIVERO_ROLLBACK_RELEASE_ID > deploy-rollback.txt'
`
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeUnsafeStatusDeployFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config := `project:
  name: demo-unsafe-status
services:
  web:
    image: registry.example.com/demo-unsafe-status@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd
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
      applyCommand: 'printf applied > deploy-applied.txt'
      statusCommand: 'printf "raw-status\nsecond-line"'
      rollbackCommand: 'printf rollback:$VIVERO_ROLLBACK_RELEASE_ID > deploy-rollback.txt'
`
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeLockedStatusDeployFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config := `project:
  name: demo-status-lock
services:
  web:
    image: registry.example.com/demo-status-lock@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee
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
      applyCommand: 'printf applied > deploy-applied.txt'
      statusCommand: 'printf ran > status-ran.txt; printf applied'
      rollbackCommand: 'printf rollback:$VIVERO_ROLLBACK_RELEASE_ID > deploy-rollback.txt'
`
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeBlueGreenTimeoutFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config := `project:
  name: demo-bg-timeout
services:
  web:
    image: registry.example.com/demo-bg-timeout@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
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
      strategy: blue-green
      statusTimeout: 50ms
      blueGreen:
        slots: [blue, green]
        activeSlotCommand: 'sleep 1; printf blue'
        prepareCommand: 'printf prepare'
        smokeCommand: 'printf smoke'
        promoteCommand: 'printf promote'
        rollbackCommand: 'printf rollback'
`
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeAppContextCancellationDeployFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config := `project:
  name: demo-app-context-cancel
services:
  web:
    image: registry.example.com/demo-app-context-cancel@sha256:1111111111111111111111111111111111111111111111111111111111111111
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
      commandTimeout: 5s
      applyCommand: 'sleep 1; printf ran > app-context-ran.txt'
      statusCommand: 'printf applied'
      rollbackCommand: 'printf rollback:$VIVERO_ROLLBACK_RELEASE_ID > deploy-rollback.txt'
`
	if err := os.WriteFile(filepath.Join(dir, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
