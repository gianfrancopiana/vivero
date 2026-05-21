package safepath

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestComponentPreservesSafeNames(t *testing.T) {
	if got := Component("demo-pr-17", "preview"); got != "demo-pr-17" {
		t.Fatalf("safe component changed: %q", got)
	}
}

func TestComponentSanitizesEscapes(t *testing.T) {
	got := Component("../outside", "preview")
	if strings.Contains(got, "..") || strings.ContainsAny(got, `/\\`) || got == "" {
		t.Fatalf("unsafe component: %q", got)
	}
	if !strings.HasSuffix(got, "-62ca1d92") {
		t.Fatalf("component should keep stable hash suffix, got %q", got)
	}
}

func TestWithinRoot(t *testing.T) {
	root := filepath.Clean("/tmp/vivero/root")
	inside := filepath.Join(root, "child")
	outside := filepath.Clean(filepath.Join(root, "..", "outside"))
	if !WithinRoot(root, root) || !WithinRoot(root, inside) {
		t.Fatalf("root/inside should be allowed")
	}
	if WithinRoot(root, outside) {
		t.Fatalf("outside path should be rejected")
	}
}
