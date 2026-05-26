package vivero

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func assertErrContainsRatchet(t *testing.T, run func() error, want string) {
	t.Helper()
	err := run()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}
