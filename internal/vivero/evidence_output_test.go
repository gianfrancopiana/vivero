package vivero

import "testing"

func TestAttachEvidenceShapeMergesDiscoveredArtifacts(t *testing.T) {
	payload := map[string]any{
		"ok":        true,
		"runPath":   "/tmp/qa/run-2.json",
		"finalPath": "/tmp/qa/final-2.json",
		"artifacts": map[string]any{"dir": "/tmp/qa", "runPath": "/tmp/qa/run.json"},
	}

	attachEvidenceShape(payload, map[string]any{"kind": "preview", "id": "pr-1", "ref": "preview:pr-1"})
	artifacts := payload["artifacts"].(map[string]any)
	if artifacts["dir"] != "/tmp/qa" {
		t.Fatalf("existing artifact dir should be preserved: %#v", artifacts)
	}
	if artifacts["runPath"] != "/tmp/qa/run-2.json" || artifacts["finalPath"] != "/tmp/qa/final-2.json" {
		t.Fatalf("discovered artifact paths should override stale plan defaults: %#v", artifacts)
	}
}
