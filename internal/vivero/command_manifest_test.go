package vivero

import (
	"encoding/json"
	"strings"
	"testing"
)

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
	for _, name := range []string{"init", "up", "down", "commands", "schema", "doctor", "doctor config", "cache inspect", "cache warm", "cache prune", "preview up", "preview inspect", "preview down", "preview events", "preview logs", "preview smoke", "preview screenshot", "preview qa run", "preview qa final", "preview diagnose startup", "evidence events", "evidence logs", "evidence screenshot", "evidence flow", "evidence qa", "qa run", "qa record", "skill doctor"} {
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
		{name: "init", category: "projects", visibility: CommandVisibilityCommon, lane: CommandLaneProject},
		{name: "up", category: "runtime", visibility: CommandVisibilityCommon, lane: CommandLanePreview},
		{name: "preview up", category: "runtime", visibility: CommandVisibilityCommon, lane: CommandLanePreview},
		{name: "preview events", category: "runtime", visibility: CommandVisibilityCommon, lane: CommandLaneEvidence},
		{name: "preview logs", category: "runtime", visibility: CommandVisibilityAdvanced, lane: CommandLaneEvidence},
		{name: "preview qa run", category: "qa", visibility: CommandVisibilityCommon, lane: CommandLaneEvidence},
		{name: "evidence events", category: "qa", visibility: CommandVisibilityCommon, lane: CommandLaneEvidence},
		{name: "evidence logs", category: "qa", visibility: CommandVisibilityCommon, lane: CommandLaneEvidence},
		{name: "evidence screenshot", category: "qa", visibility: CommandVisibilityCommon, lane: CommandLaneEvidence},
		{name: "evidence flow", category: "qa", visibility: CommandVisibilityCommon, lane: CommandLaneEvidence},
		{name: "evidence qa", category: "qa", visibility: CommandVisibilityCommon, lane: CommandLaneEvidence},
		{name: "qa run", category: "qa", visibility: CommandVisibilityCommon, lane: CommandLaneEvidence},
		{name: "cache inspect", category: "cache", visibility: CommandVisibilityCommon, lane: CommandLaneSupport},
		{name: "cache warm", category: "cache", visibility: CommandVisibilityCommon, lane: CommandLaneSupport},
		{name: "cache prune", category: "cache", visibility: CommandVisibilityCommon, lane: CommandLaneSupport},
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

func TestCommandManifestOmitsDeployReleaseProductionCommands(t *testing.T) {
	for _, cmd := range commandManifests() {
		name := cmd.Name()
		if strings.HasPrefix(name, "deploy") || strings.HasPrefix(name, "release") || name == "doctor production" {
			t.Fatalf("preview-only core should not expose %s", name)
		}
		if cmd.Lane == "deploy" || cmd.Category == "release" {
			t.Fatalf("preview-only core should not expose deploy/release metadata: %#v", cmd)
		}
	}
}

func TestCommandManifestExposesAgentSafetyMetadataForEveryCommand(t *testing.T) {
	requiredKeys := []string{
		"readsLocal",
		"writesLocal",
		"readsRemote",
		"writesRemote",
		"requiresAuth",
		"requiresNetwork",
		"dangerous",
		"agentSafe",
	}
	for _, cmd := range commandManifests() {
		encoded, err := json.Marshal(cmd)
		if err != nil {
			t.Fatalf("marshal %s: %v", cmd.Name(), err)
		}
		var raw map[string]any
		if err := json.Unmarshal(encoded, &raw); err != nil {
			t.Fatalf("unmarshal %s: %v", cmd.Name(), err)
		}
		for _, key := range requiredKeys {
			if _, ok := raw[key]; !ok {
				t.Fatalf("%s command manifest should explicitly expose %s for agents: %s", cmd.Name(), key, string(encoded))
			}
		}
	}
}

func TestCommandManifestPromotesTargetRefsAndApprovalToTopLevel(t *testing.T) {
	commands := commandManifests()
	for _, name := range []string{"inspect", "events", "logs", "smoke", "screenshot", "qa plan", "qa run", "qa final", "evidence events", "evidence logs", "evidence smoke", "evidence screenshot", "evidence flow", "evidence qa"} {
		cmd := mustManifestForTest(t, commands, name)
		raw := manifestJSONForTest(t, cmd)
		if _, ok := raw["targetRefs"]; !ok {
			t.Fatalf("%s should expose targetRefs as first-class manifest metadata: %#v", name, raw)
		}
	}
}

func TestQAContextManifestIncludesPlanSelectionFlags(t *testing.T) {
	commands := commandManifests()
	for _, name := range []string{"qa plan", "qa context"} {
		cmd := mustManifestForTest(t, commands, name)
		flags := map[string]bool{}
		for _, flag := range cmd.Flags {
			flags[flag.Name] = true
		}
		for _, want := range []string{"--scope", "--target"} {
			if !flags[want] {
				t.Fatalf("%s manifest should expose %s for agents: %#v", name, want, cmd.Flags)
			}
		}
	}
}

func TestQAMediaProofManifestFlags(t *testing.T) {
	commands := commandManifests()
	for name, wants := range map[string][]string{
		"qa record": {"--output-dir", "--wait-ms", "--slow-mo-ms", "--format"},
		"qa final":  {"--include-evidence", "--wait-ms", "--slow-mo-ms", "--format"},
	} {
		cmd := mustManifestForTest(t, commands, name)
		flags := map[string]bool{}
		for _, flag := range cmd.Flags {
			flags[flag.Name] = flag.ValueName != ""
		}
		for _, want := range wants {
			if !flags[want] {
				t.Fatalf("%s manifest should expose value flag %s for evidence artifacts: %#v", name, want, cmd.Flags)
			}
		}
	}
}

func TestCommandManifestExamplesResolveToManifestCommands(t *testing.T) {
	commands := commandManifests()
	for _, cmd := range commands {
		for _, example := range cmd.Examples {
			invocation := strings.Join(example.Command, " ")
			if !manifestMatchesInvocation(commands, invocation) {
				t.Fatalf("%s example %q should resolve to a command manifest", cmd.Name(), invocation)
			}
			if cmd.AgentSafe && cmd.JSONStability == "stable" && commandSupportsJSONForAgents(cmd) {
				if !commandFieldsContain(example.Command, "--json") || !commandFieldsContain(example.Command, "--no-input") {
					t.Fatalf("agent-safe stable example for %s should include --json --no-input: %q", cmd.Name(), invocation)
				}
			}
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
	for _, want := range []string{"cli-manifest", "manifest-visibility", "clig-compatible-help", "cli-coverage-ratchet", "release-checksums", "build-provenance", "thin-config-init", "local-state-doctor", "config-doctor", "evidence-namespace", "evidence-flow", "bounded-parallel-startup", "authenticated-qa", "preview-runtime"} {
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

func mustManifestForTest(t *testing.T, commands []CommandManifest, name string) CommandManifest {
	t.Helper()
	for _, cmd := range commands {
		if cmd.Name() == name {
			return cmd
		}
	}
	t.Fatalf("missing manifest for %s", name)
	return CommandManifest{}
}

func manifestJSONForTest(t *testing.T, cmd CommandManifest) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal %s: %v", cmd.Name(), err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatalf("unmarshal %s: %v", cmd.Name(), err)
	}
	return raw
}

func commandSupportsJSONForAgents(cmd CommandManifest) bool {
	switch cmd.Name() {
	case "help", "serve":
		return false
	default:
		return true
	}
}

func commandFieldsContain(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}
