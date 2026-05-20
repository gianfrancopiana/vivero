package vivero

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSourcePathIsProjectRelative(t *testing.T) {
	projectRoot := t.TempDir()
	appDir := filepath.Join(projectRoot, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := (&App{}).resolveSource("demo", projectRoot, "preview-1", "app", SourceConfig{Path: "app"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != appDir {
		t.Fatalf("path = %s, want %s", got.Path, appDir)
	}
	if got.Mode != "external" || got.Owned {
		t.Fatalf("source metadata = %#v", got)
	}
}

func TestResolveSourcePathRejectsProjectRelativeEscape(t *testing.T) {
	projectRoot := t.TempDir()
	_, err := (&App{}).resolveSource("demo", projectRoot, "preview-1", "app", SourceConfig{Path: ".."}, nil)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected project-relative escape error, got %v", err)
	}
}

func TestManagedWorktreePathKeepsDynamicNamesUnderWorktreeRoot(t *testing.T) {
	home := t.TempDir()
	path := managedWorktreePath(home, "../project", "../preview", "../source")
	root := filepath.Join(home, "worktrees")
	if !pathWithinRoot(root, path) {
		t.Fatalf("managed worktree path escaped root: path=%s root=%s", path, root)
	}
	if strings.Contains(path, "..") {
		t.Fatalf("managed worktree path should not preserve traversal elements: %s", path)
	}
}
