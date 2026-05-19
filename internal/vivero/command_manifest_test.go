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
		if _, ok := seen[name]; ok {
			t.Fatalf("duplicate manifest for %s", name)
		}
		seen[name] = cmd
	}
	for _, name := range []string{"up", "down", "commands", "schema", "doctor", "doctor config", "doctor production", "qa run", "qa record", "skill doctor"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing public manifest for %s", name)
		}
	}
}

func TestCommandCatalogUsesManifestMetadata(t *testing.T) {
	catalog := commandCatalog()
	if len(catalog) != len(commandManifests()) {
		t.Fatalf("catalog length = %d, manifests = %d", len(catalog), len(commandManifests()))
	}
	for _, cmd := range catalog {
		if cmd.Summary == "" || cmd.JSONStability == "" {
			t.Fatalf("catalog command lacks manifest metadata: %#v", cmd)
		}
		if cmd.Name() == "up" {
			if !cmd.WritesLocal || !cmd.RequiresNet || !cmd.AgentSafe {
				t.Fatalf("up metadata should describe side effects and agent safety: %#v", cmd)
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
}

func TestCapabilitiesAdvertiseCLIContract(t *testing.T) {
	a := &App{Home: t.TempDir()}
	caps := a.capabilities()
	features := stringSet(caps["features"].([]string))
	for _, want := range []string{"cli-manifest", "clig-compatible-help", "cli-coverage-ratchet", "config-doctor", "production-readiness-doctor", "bounded-parallel-startup", "authenticated-qa", "preview-runtime"} {
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
