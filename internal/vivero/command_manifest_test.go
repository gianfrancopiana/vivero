package vivero

import "testing"

func TestCommandManifestCoversPublicCommands(t *testing.T) {
	manifests := commandManifests()
	if len(manifests) == 0 {
		t.Fatal("expected command manifests")
	}
	seen := map[string]CommandManifest{}
	for _, cmd := range manifests {
		name := cmd.Name()
		if len(cmd.Path) == 0 {
			t.Fatal("manifest path is required")
		}
		if cmd.Summary == "" {
			t.Fatalf("%s summary is required", name)
		}
		if len(cmd.Examples) == 0 {
			t.Fatalf("%s examples are required", name)
		}
		if cmd.JSONStability == "" {
			t.Fatalf("%s json stability is required", name)
		}
		if cmd.Category == "" {
			t.Fatalf("%s category is required", name)
		}
		if cmd.Lane == "" {
			t.Fatalf("%s lane is required", name)
		}
		if !validCommandLane(cmd.Lane) {
			t.Fatalf("%s lane is invalid: %q", name, cmd.Lane)
		}
		if !validCommandVisibility(cmd.Visibility) {
			t.Fatalf("%s visibility is invalid: %q", name, cmd.Visibility)
		}
		if _, ok := seen[name]; ok {
			t.Fatalf("duplicate manifest for %s", name)
		}
		seen[name] = cmd
	}
	for _, name := range []string{"up", "down", "commands", "schema", "doctor", "doctor config", "doctor production", "preview up", "preview inspect", "preview down", "preview events", "deploy plan", "deploy apply", "release status", "release rollback", "qa run", "qa record", "skill doctor"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing manifest for %s", name)
		}
	}
	for _, tc := range []struct {
		name       string
		category   string
		visibility string
		lane       string
	}{
		{name: "up", category: "runtime", visibility: CommandVisibilityCommon, lane: CommandLanePreview},
		{name: "preview up", category: "runtime", visibility: CommandVisibilityCommon, lane: CommandLanePreview},
		{name: "preview events", category: "runtime", visibility: CommandVisibilityCommon, lane: CommandLaneEvidence},
		{name: "qa run", category: "qa", visibility: CommandVisibilityCommon, lane: CommandLaneEvidence},
		{name: "deploy apply", category: "release", visibility: CommandVisibilityAdvanced, lane: CommandLaneDeploy},
		{name: "release rollback", category: "release", visibility: CommandVisibilityAdvanced, lane: CommandLaneDeploy},
		{name: "serve", category: "control-plane", visibility: CommandVisibilityInternal, lane: CommandLaneSupport},
	} {
		cmd, ok := seen[tc.name]
		if !ok {
			t.Fatalf("missing manifest for %s", tc.name)
		}
		if cmd.Category != tc.category || cmd.Visibility != tc.visibility || cmd.Lane != tc.lane {
			t.Fatalf("%s category/visibility/lane = %s/%s/%s, want %s/%s/%s", cmd.Name(), cmd.Category, cmd.Visibility, cmd.Lane, tc.category, tc.visibility, tc.lane)
		}
	}
}

func TestCommandCatalogUsesManifestMetadata(t *testing.T) {
	catalog := commandCatalog()
	if len(catalog) != len(commandManifests()) {
		t.Fatalf("catalog length = %d, manifests = %d", len(catalog), len(commandManifests()))
	}
	for _, cmd := range catalog {
		if cmd.Summary == "" || cmd.JSONStability == "" || cmd.Category == "" || cmd.Lane == "" || !validCommandLane(cmd.Lane) || !validCommandVisibility(cmd.Visibility) {
			t.Fatalf("catalog command lacks manifest metadata: %#v", cmd)
		}
		if cmd.Name() == "up" {
			if !cmd.WritesLocal || !cmd.RequiresNet || !cmd.AgentSafe {
				t.Fatalf("up metadata should describe side effects and agent safety: %#v", cmd)
			}
		}
		if cmd.Name() == "release status" {
			if cmd.AgentSafe || !cmd.WritesLocal || !cmd.RequiresNet || cmd.Schema["runsAppOwnedCommand"] != true || cmd.Schema["mayUpdateLocalReleaseState"] != true {
				t.Fatalf("release status metadata should disclose app-owned status command side effects: %#v", cmd)
			}
		}
	}
}

func TestSchemaForComesFromManifest(t *testing.T) {
	schema := schemaFor("up")
	if schema["command"] != "up" {
		t.Fatalf("schema command = %v", schema["command"])
	}
	body := schema["schema"].(map[string]any)
	if body["usage"] == "" {
		t.Fatalf("schema usage is required: %#v", body)
	}
	if body["jsonStability"] != "stable" {
		t.Fatalf("schema jsonStability = %v", body["jsonStability"])
	}
	if body["category"] != "runtime" || body["visibility"] != CommandVisibilityCommon || body["lane"] != CommandLanePreview {
		t.Fatalf("schema category/visibility/lane = %v/%v/%v", body["category"], body["visibility"], body["lane"])
	}
}

func TestCapabilitiesAdvertiseCLIContract(t *testing.T) {
	a := &App{Home: t.TempDir()}
	caps := a.capabilities()
	if build, ok := caps["build"].(VersionInfo); !ok || build.Version != Version || build.Commit == "" || build.Date == "" {
		t.Fatalf("capabilities should expose build provenance: %#v", caps["build"])
	}
	features := stringSet(caps["features"].([]string))
	for _, want := range []string{"cli-manifest", "manifest-visibility", "clig-compatible-help", "cli-coverage-ratchet", "release-checksums", "build-provenance", "local-state-doctor", "config-doctor", "production-readiness-doctor", "app-owned-deploy-surface", "release-status", "release-rollback", "bounded-parallel-startup", "authenticated-qa", "preview-runtime"} {
		if !features[want] {
			t.Fatalf("capabilities missing %s: %#v", want, caps["features"])
		}
	}
	invariants := stringSet(caps["invariants"].([]string))
	for _, want := range []string{"stable-json-errors", "no-required-prompts"} {
		if !invariants[want] {
			t.Fatalf("capabilities missing invariant %s: %#v", want, caps["invariants"])
		}
	}
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}
