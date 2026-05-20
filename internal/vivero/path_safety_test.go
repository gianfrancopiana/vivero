package vivero

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeStatePathsKeepDynamicNamesUnderRoots(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		name string
		root string
		path string
	}{
		{
			name: "managed repo",
			root: filepath.Join(home, "repos"),
			path: managedRepoPath(home, "../source"),
		},
		{
			name: "run file",
			root: filepath.Join(home, "run"),
			path: previewRunFilePath(home, "../preview", "compose.yml"),
		},
		{
			name: "service log",
			root: filepath.Join(home, "logs"),
			path: serviceLogPath(home, "../preview", "web/../../api", ".log"),
		},
		{
			name: "qa video fallback",
			root: filepath.Join(home, "qa"),
			path: qaVideoFallbackDir(home, "../preview"),
		},
		{
			name: "project secret",
			root: filepath.Join(home, "secrets"),
			path: projectSecretFilePath(home, "../project"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !pathWithinRoot(tc.root, tc.path) {
				t.Fatalf("path escaped root: path=%s root=%s", tc.path, tc.root)
			}
			if strings.Contains(tc.path, "..") {
				t.Fatalf("path should not preserve traversal elements: %s", tc.path)
			}
		})
	}
}

func TestSafePathComponentPreservesNormalNames(t *testing.T) {
	if got := safePathComponent("demo-pr-17", "preview"); got != "demo-pr-17" {
		t.Fatalf("normal preview id changed: %q", got)
	}
	if got := safePathComponent("web", "service"); got != "web" {
		t.Fatalf("normal service changed: %q", got)
	}
}
