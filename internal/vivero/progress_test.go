package vivero

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgressReporterSuppressesJSONAndQuietOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts ProgressOptions
	}{
		{name: "json", opts: ProgressOptions{JSON: true}},
		{name: "quiet", opts: ProgressOptions{Quiet: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			reporter := NewProgressReporter(&stderr, tc.opts)
			reporter.Step("startup", "building image")
			if stderr.Len() != 0 {
				t.Fatalf("expected no progress, got %q", stderr.String())
			}
		})
	}
}

func TestProgressReporterWritesToStderr(t *testing.T) {
	var stderr bytes.Buffer
	reporter := NewProgressReporter(&stderr, ProgressOptions{})
	reporter.Step("startup", "building image")
	if !strings.Contains(stderr.String(), "startup: building image") {
		t.Fatalf("unexpected progress output: %q", stderr.String())
	}
}
