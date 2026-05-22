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

func TestAttachEvidenceShapeSuggestsPreviewAndReleaseDebugCommandsOnFailure(t *testing.T) {
	previewPayload := map[string]any{"smoke": map[string]any{"ok": false}}
	attachEvidenceShape(previewPayload, map[string]any{"kind": "preview", "id": "pr-1", "ref": "preview:pr-1"})
	previewSuggestions := stringSliceFromAny(t, previewPayload["nextSuggestedCommands"])
	for _, want := range []string{
		"vivero preview inspect preview:pr-1 --json --no-input",
		"vivero preview events preview:pr-1 --tail --json --no-input",
		"vivero preview diagnose startup preview:pr-1 --json --no-input",
	} {
		if !containsString(previewSuggestions, want) {
			t.Fatalf("preview failure suggestions missing %q: %#v", want, previewSuggestions)
		}
	}

	releasePayload := map[string]any{"smoke": ReleaseSmokeResult{OK: false, Error: "smoke failed"}}
	attachEvidenceShape(releasePayload, map[string]any{"kind": "release", "id": "rel-1", "ref": "release:rel-1", "project": "demo", "environment": "production"})
	releaseSuggestions := stringSliceFromAny(t, releasePayload["nextSuggestedCommands"])
	for _, want := range []string{
		"vivero release events release:rel-1 --json --no-input",
		"vivero release logs release:rel-1 --json --no-input",
	} {
		if !containsString(releaseSuggestions, want) {
			t.Fatalf("release failure suggestions missing %q: %#v", want, releaseSuggestions)
		}
	}
}

func stringSliceFromAny(t *testing.T, value any) []string {
	t.Helper()
	raw, ok := value.([]string)
	if ok {
		return raw
	}
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("expected string slice, got %#v", value)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("expected string item, got %#v", item)
		}
		out = append(out, s)
	}
	return out
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
