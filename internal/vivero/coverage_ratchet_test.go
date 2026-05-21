package vivero

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSkillInstallPathAndExistingTargetBranches(t *testing.T) {
	a := &App{}
	targetDir := filepath.Join(t.TempDir(), "skills", "vivero")
	targetFile := filepath.Join(targetDir, "SKILL.md")

	first, err := a.SkillInstall(targetDir, false)
	if err != nil {
		t.Fatal(err)
	}
	assertSingleSkillInstallTarget(t, first, targetFile, true, "")
	if b, err := os.ReadFile(targetFile); err != nil || !strings.Contains(string(b), "name: vivero") {
		t.Fatalf("skill install should write bundled skill, err=%v content=%q", err, b)
	}

	second, err := a.SkillInstall(targetFile, false)
	if err != nil {
		t.Fatal(err)
	}
	assertSingleSkillInstallTarget(t, second, targetFile, false, "exists; pass --force to overwrite")

	forced, err := a.SkillInstall(targetFile, true)
	if err != nil {
		t.Fatal(err)
	}
	assertSingleSkillInstallTarget(t, forced, targetFile, true, "")

	paths, err := a.SkillPath()
	if err != nil {
		t.Fatal(err)
	}
	defaults, ok := paths["defaultTargets"].([]string)
	if !ok || len(defaults) == 0 {
		t.Fatalf("skill path should report default targets: %#v", paths)
	}
	for _, path := range defaults {
		if !strings.HasSuffix(path, filepath.Join("vivero", "SKILL.md")) {
			t.Fatalf("default skill target should end in vivero/SKILL.md: %s", path)
		}
	}
}

func TestSkillVersionFallsBackToRegexAndUnknown(t *testing.T) {
	if got := skillVersion([]byte("---\nname: vivero\nversion: 1.2.3\n---\n")); got != "1.2.3" {
		t.Fatalf("frontmatter skill version = %q", got)
	}
	if got := skillVersion([]byte("name: vivero\nversion: 9.8.7\n")); got != "9.8.7" {
		t.Fatalf("fallback skill version = %q", got)
	}
	if got := skillVersion([]byte("name: vivero\n")); got != "unknown" {
		t.Fatalf("missing skill version = %q", got)
	}
}

func TestDeployStateLoadersRejectCorruptDriftedAndFutureState(t *testing.T) {
	a := &App{Home: t.TempDir()}

	assertErrContainsRatchet(t, func() error { _, err := a.loadDeployPlan(""); return err }, "deploy plan id is required")
	assertErrContainsRatchet(t, func() error { _, err := a.loadDeployPlan("missing-plan"); return err }, "deploy plan not found")

	if err := ensureDir(a.deployPlanDir()); err != nil {
		t.Fatal(err)
	}
	writeDeployStateFile(t, filepath.Join(a.deployPlanDir(), statePathComponent("corrupt-plan")+".json"), `not-json`)
	assertErrContainsRatchet(t, func() error { _, err := a.loadDeployPlan("corrupt-plan"); return err }, "invalid character")
	writeDeployStateFile(t, filepath.Join(a.deployPlanDir(), statePathComponent("requested-plan")+".json"), `{"id":"other-plan","stateVersion":1}`)
	assertErrContainsRatchet(t, func() error { _, err := a.loadDeployPlan("requested-plan"); return err }, "state mismatch")
	writeDeployStateFile(t, filepath.Join(a.deployPlanDir(), statePathComponent("future-plan")+".json"), `{"id":"future-plan","stateVersion":2}`)
	assertErrContainsRatchet(t, func() error { _, err := a.loadDeployPlan("future-plan"); return err }, "unsupported state version")

	assertErrContainsRatchet(t, func() error { _, err := a.loadRelease(""); return err }, "release id is required")
	assertErrContainsRatchet(t, func() error { _, err := a.loadRelease("missing-release"); return err }, "release not found")

	if err := ensureDir(a.releaseDir()); err != nil {
		t.Fatal(err)
	}
	writeDeployStateFile(t, filepath.Join(a.releaseDir(), statePathComponent("corrupt-release")+".json"), `not-json`)
	assertErrContainsRatchet(t, func() error { _, err := a.loadRelease("corrupt-release"); return err }, "invalid character")
	writeDeployStateFile(t, filepath.Join(a.releaseDir(), statePathComponent("requested-release")+".json"), `{"id":"other-release","stateVersion":1}`)
	assertErrContainsRatchet(t, func() error { _, err := a.loadRelease("requested-release"); return err }, "state mismatch")
	writeDeployStateFile(t, filepath.Join(a.releaseDir(), statePathComponent("future-release")+".json"), `{"id":"future-release","stateVersion":2}`)
	assertErrContainsRatchet(t, func() error { _, err := a.loadRelease("future-release"); return err }, "unsupported state version")

	_, err := a.loadCurrentRelease("demo", "production")
	if !errors.Is(err, errNoCurrentRelease) {
		t.Fatalf("missing current release should wrap errNoCurrentRelease, got %v", err)
	}
	assertErrContainsRatchet(t, func() error { _, err := a.loadCurrentRelease("", "production"); return err }, "release status requires project")
	writeDeployStateFile(t, a.currentReleasePath("demo", "production"), `{"id":"rel-1","project":"other","environment":"production","stateVersion":1}`)
	assertErrContainsRatchet(t, func() error { _, err := a.loadCurrentRelease("demo", "production"); return err }, "current release state mismatch")
	writeDeployStateFile(t, a.currentReleasePath("demo", "production"), `{"id":"rel-1","project":"demo","environment":"production","stateVersion":2}`)
	assertErrContainsRatchet(t, func() error { _, err := a.loadCurrentRelease("demo", "production"); return err }, "unsupported state version")
}

func TestDeployStateFindsSafeReapplyAndExistingRollback(t *testing.T) {
	a := &App{Home: t.TempDir()}
	plan := DeployPlan{ID: "plan-1", Project: "demo", Environment: "production"}

	if release, found, err := a.findSuccessfulReleaseForPlan(plan); err != nil || found || release.ID != "" {
		t.Fatalf("no current release should be a cache miss, release=%#v found=%v err=%v", release, found, err)
	}

	applied := ReleaseRecord{ID: "rel-1", PlanID: plan.ID, Project: plan.Project, Environment: plan.Environment, Status: "applied"}
	if err := a.saveRelease(applied); err != nil {
		t.Fatal(err)
	}
	if release, found, err := a.findSuccessfulReleaseForPlan(plan); err != nil || !found || release.ID != applied.ID {
		t.Fatalf("safe applied current release should be reused, release=%#v found=%v err=%v", release, found, err)
	}

	unsafe := applied
	unsafe.Status = "smoke_failed"
	if err := a.saveRelease(unsafe); err != nil {
		t.Fatal(err)
	}
	if release, found, err := a.findSuccessfulReleaseForPlan(plan); err == nil || found || release.ID != unsafe.ID || !strings.Contains(err.Error(), "unsafe status smoke_failed") {
		t.Fatalf("unsafe current release should block reapply, release=%#v found=%v err=%v", release, found, err)
	}

	otherPlan := plan
	otherPlan.ID = "plan-2"
	if release, found, err := a.findSuccessfulReleaseForPlan(otherPlan); err != nil || found || release.ID != "" {
		t.Fatalf("current release for another plan should be ignored, release=%#v found=%v err=%v", release, found, err)
	}

	rollback := ReleaseRecord{ID: "rollback-1", Project: plan.Project, Environment: plan.Environment, Status: "rolled_back", RollbackOf: applied.ID}
	if err := a.saveRelease(rollback); err != nil {
		t.Fatal(err)
	}
	if release, found, err := a.findRollbackForRelease(plan.Project, plan.Environment, applied.ID); err != nil || !found || release.ID != rollback.ID {
		t.Fatalf("existing rollback should be reused, release=%#v found=%v err=%v", release, found, err)
	}
	if release, found, err := a.findRollbackForRelease(plan.Project, plan.Environment, "rel-other"); err != nil || found || release.ID != "" {
		t.Fatalf("rollback for another release should be ignored, release=%#v found=%v err=%v", release, found, err)
	}
}

func TestDeployLocksRemoveStaleRecordsAndBlockLiveHolders(t *testing.T) {
	a := &App{Home: t.TempDir()}
	if err := ensureDir(a.deployLockDir()); err != nil {
		t.Fatal(err)
	}
	lockPath := a.deployLockPath("demo", "production")
	if err := writeIndentedJSONFile(lockPath, map[string]any{"pid": 0, "createdAt": nowUTC().Add(-5 * time.Hour)}, 0o644); err != nil {
		t.Fatal(err)
	}
	unlock, err := a.acquireDeployLock("demo", "production")
	if err != nil {
		t.Fatalf("stale lock should be removed before acquiring: %v", err)
	}
	unlock()

	if a.removeStaleDeployLock(filepath.Join(a.deployLockDir(), "missing.lock")) {
		t.Fatal("missing lock should not be treated as stale")
	}
	writeDeployStateFile(t, lockPath, `not-json`)
	if a.removeStaleDeployLock(lockPath) {
		t.Fatal("corrupt lock should not be removed automatically")
	}
	if err := writeIndentedJSONFile(lockPath, map[string]any{"pid": os.Getpid(), "createdAt": nowUTC()}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.acquireDeployLock("demo", "production"); err == nil || !strings.Contains(err.Error(), "deploy lock already held") {
		t.Fatalf("live lock should block acquisition, got %v", err)
	}
}

func TestSourceSyncRemoveLogsAndExecErrorBranches(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	sourceDir := t.TempDir()
	removePath := filepath.Join(sourceDir, "remove-me.txt")
	if err := os.WriteFile(removePath, []byte("delete this"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(sourceDir, "web.log")
	if err := os.WriteFile(logPath, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "sync-pr", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveSource("sync-pr", PreviewSource{Name: "app", Mode: "external", Path: sourceDir}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("sync-pr", PreviewService{Name: "web", Source: "app", Runtime: "process", Status: "running", LogPath: logPath}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("sync-pr", PreviewService{Name: "bad-log", Source: "app", Runtime: "process", Status: "running", LogPath: filepath.Join(sourceDir, "missing.log")}); err != nil {
		t.Fatal(err)
	}

	removed, err := a.RemoveFile("sync-pr", "app", "remove-me.txt")
	if err != nil {
		t.Fatal(err)
	}
	if removed["path"] != "remove-me.txt" || removed["source"] != "app" {
		t.Fatalf("unexpected remove response: %#v", removed)
	}
	if _, err := os.Stat(removePath); !os.IsNotExist(err) {
		t.Fatalf("removed source file should be gone, stat err=%v", err)
	}
	assertErrContainsRatchet(t, func() error { _, err := a.RemoveFile("sync-pr", "app", "remove-me.txt"); return err }, "no such file")
	assertErrContainsRatchet(t, func() error { _, err := a.RemoveFile("sync-pr", "missing", "x.txt"); return err }, "source not found")
	assertErrContainsRatchet(t, func() error { _, err := a.RemoveFile("sync-pr", "app", "../escape.txt"); return err }, "unsafe path")

	logs, err := a.Logs("sync-pr", "web", 2)
	if err != nil {
		t.Fatal(err)
	}
	lines := logs["lines"].([]string)
	if len(lines) != 2 || lines[0] != "three" || lines[1] != "" {
		t.Fatalf("log limit should keep the last two split lines, got %#v", lines)
	}
	assertErrContainsRatchet(t, func() error { _, err := a.Logs("sync-pr", "missing", 1); return err }, "service not found")
	assertErrContainsRatchet(t, func() error { _, err := a.Logs("sync-pr", "bad-log", 1); return err }, "no such file")
	assertErrContainsRatchet(t, func() error { _, err := a.Exec("sync-pr", "missing", []string{"true"}); return err }, "service not found")
	assertErrContainsRatchet(t, func() error { _, err := a.Exec("sync-pr", "web", nil); return err }, "command required")
	assertErrContainsRatchet(t, func() error { _, err := a.Exec("sync-pr", "web", []string{"true"}); return err }, "containers only")
}

func TestResolveSourceOverrideConfigAndRepoErrorBranches(t *testing.T) {
	a := &App{Home: t.TempDir()}
	projectRoot := t.TempDir()
	overrideDir := t.TempDir()
	configuredDir := filepath.Join(projectRoot, "app")
	if err := os.MkdirAll(configuredDir, 0o755); err != nil {
		t.Fatal(err)
	}

	overridden, err := a.resolveSource("demo", projectRoot, "pr-1", "app", SourceConfig{Repo: "unused"}, map[string]string{"app.path": overrideDir})
	if err != nil {
		t.Fatal(err)
	}
	if overridden.Mode != "external" || overridden.Path != overrideDir || overridden.Owned {
		t.Fatalf("path override should produce external source, got %#v", overridden)
	}
	assertErrContainsRatchet(t, func() error {
		_, err := a.resolveSource("demo", projectRoot, "pr-1", "app", SourceConfig{}, map[string]string{"app.path": filepath.Join(projectRoot, "missing")})
		return err
	}, "external source path is not a directory")

	configured, err := a.resolveSource("demo", projectRoot, "pr-1", "app", SourceConfig{Path: "app"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if configured.Path != configuredDir || configured.Mode != "external" || configured.Owned {
		t.Fatalf("configured source path should produce external source, got %#v", configured)
	}
	assertErrContainsRatchet(t, func() error {
		_, err := a.resolveSource("demo", projectRoot, "pr-1", "app", SourceConfig{Path: "missing"}, nil)
		return err
	}, "configured source path is not a directory")
	assertErrContainsRatchet(t, func() error {
		_, err := a.resolveSource("demo", projectRoot, "pr-1", "app", SourceConfig{}, nil)
		return err
	}, "source app has no repo/path")
}

func TestDetectWarmRefPriorityAndGitFallbacks(t *testing.T) {
	projectRoot := t.TempDir()
	project := ProjectRecord{Path: projectRoot, Config: ProjectConfig{Sources: map[string]SourceConfig{"app": {DefaultRef: "origin/main"}}}}

	if got := detectWarmRef(project, UpRequest{Metadata: map[string]string{"branch": "refs/heads/feature"}}, nil); got != "feature" {
		t.Fatalf("metadata warm ref = %q", got)
	}
	if got := detectWarmRef(project, UpRequest{Sources: map[string]string{"app.ref": "origin/topic"}}, nil); got != "topic" {
		t.Fatalf("source override warm ref = %q", got)
	}

	sourceDir := t.TempDir()
	initCommittedGitBranch(t, sourceDir, "dev")
	if got := detectWarmRef(ProjectRecord{Path: projectRoot, Config: ProjectConfig{Sources: map[string]SourceConfig{"app": {}}}}, UpRequest{}, map[string]PreviewSource{"app": {Path: sourceDir}}); got != "dev" {
		t.Fatalf("source git branch warm ref = %q", got)
	}

	if got := detectWarmRef(project, UpRequest{}, nil); got != "main" {
		t.Fatalf("default source ref warm ref = %q", got)
	}
	initCommittedGitBranch(t, projectRoot, "trunk")
	if got := detectWarmRef(ProjectRecord{Path: projectRoot, Config: ProjectConfig{}}, UpRequest{}, nil); got != "trunk" {
		t.Fatalf("project git branch warm ref = %q", got)
	}
	if got := detectWarmRef(ProjectRecord{Path: t.TempDir(), Config: ProjectConfig{}}, UpRequest{}, nil); got != "main" {
		t.Fatalf("fallback warm ref = %q", got)
	}
}

func assertSingleSkillInstallTarget(t *testing.T, payload map[string]any, wantPath string, wantInstalled bool, wantReason string) {
	t.Helper()
	targets, ok := payload["targets"].([]map[string]any)
	if !ok || len(targets) != 1 {
		t.Fatalf("skill install should return one target: %#v", payload)
	}
	if targets[0]["path"] != wantPath || targets[0]["installed"] != wantInstalled {
		t.Fatalf("unexpected skill install target: got %#v want path=%s installed=%v", targets[0], wantPath, wantInstalled)
	}
	if wantReason != "" && targets[0]["reason"] != wantReason {
		t.Fatalf("skill install reason = %#v, want %q", targets[0]["reason"], wantReason)
	}
}

func initCommittedGitBranch(t *testing.T, dir, branch string) {
	t.Helper()
	if out, err := runCmd(dir, nil, "git", "init", "-b", branch); err != nil {
		t.Fatalf("git init %s: %v %s", dir, err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(dir, nil, "git", "add", "README.md"); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	if out, err := runCmd(dir, nil, "git", "-c", "user.name=Vivero Tests", "-c", "user.email=vivero-tests@example.com", "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
}

func writeDeployStateFile(t *testing.T, path, content string) {
	t.Helper()
	if err := ensureDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertErrContainsRatchet(t *testing.T, run func() error, want string) {
	t.Helper()
	err := run()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}
