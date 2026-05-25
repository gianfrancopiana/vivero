package vivero

import (
	"os"
	"strings"
	"testing"
)

func TestReadmeFramesPreviewDeployEvidenceContract(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(readme)
	for _, want := range []string{
		"preview-first app-ops runtime",
		"Preview lane",
		"Experimental deploy/release lane",
		"Evidence/cache lane",
		"the app owns how it runs and deploys; Vivero owns orchestration, safety gates, local state, command contracts, and evidence",
		"Golden path: preview, prove, then guarded deploy-readiness",
		"vivero preview up",
		"vivero deploy plan",
		"vivero release events",
		"## Fast paths",
		"Docker build cache",
		"deploy prepare/cache hints",
		"docs/certified-examples.md",
		"## Limits",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("README should frame preview/deploy/evidence contract with %q", want)
		}
	}
}

func TestDocsExplainPreviewDeployCacheFastPaths(t *testing.T) {
	files := map[string]string{
		"readme":            "../../README.md",
		"skill":             "../../skills/vivero/SKILL.md",
		"certifiedExamples": "../../docs/certified-examples.md",
		"releasing":         "../../docs/releasing.md",
	}
	bodies := map[string]string{}
	for name, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		bodies[name] = string(body)
	}

	for _, want := range []string{"Preview lane", "Experimental deploy/release lane", "Evidence/cache lane"} {
		if !strings.Contains(bodies["readme"], want) {
			t.Fatalf("README should teach lane %q", want)
		}
	}
	for _, want := range []string{"## Speed model", "stable preview IDs", "--metadata branch=", "build cache config", "cache inspect", "deploy prepare/cache evidence"} {
		if !strings.Contains(bodies["skill"], want) {
			t.Fatalf("bundled skill should document speed model with %q", want)
		}
	}
	for _, want := range []string{"Fast-path signals", "image build duration", "cache enabled/disabled", "warm baseline/derived events", "deploy phase durations", "artifact paths"} {
		if !strings.Contains(bodies["certifiedExamples"], want) {
			t.Fatalf("certified examples docs should document fast-path signal %q", want)
		}
	}
	for _, want := range []string{"preview/deploy lane contract", "build cache config", "cache commands", "deploy prepare/cache evidence", "--example-e2e"} {
		if !strings.Contains(bodies["releasing"], want) {
			t.Fatalf("releasing docs should mention release-note surface %q", want)
		}
	}
}

func TestReleaseCertificationAndInstallTrustDocsStayAligned(t *testing.T) {
	files := map[string]string{
		"makefile":          "../../Makefile",
		"readme":            "../../README.md",
		"certifiedExamples": "../../docs/certified-examples.md",
		"releaseWorkflow":   "../../.github/workflows/release.yml",
		"releasing":         "../../docs/releasing.md",
		"skill":             "../../skills/vivero/SKILL.md",
	}
	bodies := map[string]string{}
	for name, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		bodies[name] = string(body)
	}
	for _, want := range []string{
		"RELEASE_POSTFLIGHT_FLAGS ?=",
		"certify:\n\t$(MAKE) audit",
		"\t$(MAKE) example-e2e",
		"\t$(MAKE) integration-fixtures",
		"\t$(MAKE) nasty-integration-fixtures",
		"\t$(MAKE) example-configs",
		"\t$(MAKE) deploy-fixtures",
		"\t$(MAKE) release-smoke",
	} {
		if !strings.Contains(bodies["makefile"], want) {
			t.Fatalf("Makefile certify target should include %q", want)
		}
	}
	for _, doc := range []string{"readme", "certifiedExamples", "releaseWorkflow", "releasing", "skill"} {
		if !strings.Contains(bodies[doc], "make certify") {
			t.Fatalf("%s should document make certify", doc)
		}
	}
	for _, want := range []string{"Verify tag points at current main", "install-only: true", "release tags must point at current origin/main"} {
		if !strings.Contains(bodies["releaseWorkflow"], want) {
			t.Fatalf("release workflow should enforce certification preconditions with %q", want)
		}
	}
	for _, doc := range []string{"readme", "releasing", "skill"} {
		for _, want := range []string{"release-postflight", "checksums", "attestations", "installer", "Homebrew"} {
			if !strings.Contains(bodies[doc], want) {
				t.Fatalf("%s should document install trust postflight with %q", doc, want)
			}
		}
	}
	for _, doc := range []string{"readme", "releasing", "skill"} {
		for _, want := range []string{"RELEASE_POSTFLIGHT_FLAGS", "--example-e2e", "checksum-installed release binary"} {
			if !strings.Contains(bodies[doc], want) {
				t.Fatalf("%s should document released-binary preview E2E with %q", doc, want)
			}
		}
	}
}

func TestCertifiedExamplesAreDocumentedAndLoad(t *testing.T) {
	docBytes, err := os.ReadFile("../../docs/certified-examples.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(docBytes)
	examples := map[string]string{
		"../../examples/agent-demo":        "agent-demo",
		"../../examples/integration-stack": "integration-stack",
		"../../examples/nasty-integration": "nasty-integration",
		"../../examples/deploy-command":    "deploy-ready",
		"../../examples/deploy-blue-green": "deploy-blue-green",
	}
	for path, wantName := range examples {
		t.Run(wantName, func(t *testing.T) {
			if !strings.Contains(doc, strings.TrimPrefix(path, "../../")) {
				t.Fatalf("certified examples doc should mention %s", path)
			}
			_, cfg, err := loadProjectConfig(path)
			if err != nil {
				t.Fatalf("load %s: %v", path, err)
			}
			if cfg.Project.Name != wantName {
				t.Fatalf("project name = %q, want %q", cfg.Project.Name, wantName)
			}
		})
	}
	for _, want := range []string{"static-only", "web app", "app + database", "monorepo app-owned Dockerfile", "command deploy", "blue/green deploy", "make example-e2e", "make integration-fixtures", "make nasty-integration-fixtures", "make deploy-fixtures"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("certified examples doc should include %q", want)
		}
	}
}

func TestCertifiedDeployExamplesUseAppOwnedScripts(t *testing.T) {
	cases := []struct {
		path     string
		strategy string
		command  string
	}{
		{path: "../../examples/deploy-command", strategy: "command", command: "scripts/deploy-command.sh"},
		{path: "../../examples/deploy-blue-green", strategy: "blue-green", command: "scripts/blue-green.sh"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			bodyBytes, err := os.ReadFile(tc.path + "/vivero.yml")
			if err != nil {
				t.Fatal(err)
			}
			body := string(bodyBytes)
			if !strings.Contains(body, tc.command) {
				t.Fatalf("deploy example should delegate to app-owned script %q", tc.command)
			}
			_, cfg, err := loadProjectConfig(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			env, ok := cfg.Deploy.Environments["production"]
			if !ok {
				t.Fatal("deploy example should configure production environment")
			}
			if normalizeDeployStrategy(env.Strategy) != tc.strategy {
				t.Fatalf("strategy = %q, want %q", normalizeDeployStrategy(env.Strategy), tc.strategy)
			}
		})
	}
}

func TestGumroadExampleStaysThinAgainstAppOwnedCompose(t *testing.T) {
	bodyBytes, err := os.ReadFile("../../examples/gumroad/vivero.yml")
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	for _, forbidden := range []string{
		"backingServices:",
		"setup:",
		"DATABASE_HOST:",
		"ELASTICSEARCH_HOST:",
		"bundle install",
		"npm install",
		"mysql:",
		"redis:",
		"memcached:",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("gumroad example should not duplicate app runtime internals %q", forbidden)
		}
	}
	_, cfg, err := loadProjectConfig("../../examples/gumroad")
	if err != nil {
		t.Fatal(err)
	}
	svc := cfg.Services["gumroad-web"]
	if serviceRuntime(svc) != "compose" || svc.Compose.File != "docker-compose.yml" || svc.Compose.Service != "web" {
		t.Fatalf("gumroad example should delegate runtime to app-owned compose stack: %#v", svc)
	}
	if svc.Image != "" || imageBuildConfigured(svc.Build) || !svc.Command.IsZero() || len(svc.DependencyVolumes) > 0 {
		t.Fatalf("gumroad example should only keep preview-layer fields in Vivero YAML: %#v", svc)
	}
}

func TestCertifiedFastPathExamplesExposeCacheContracts(t *testing.T) {
	_, previewCfg, err := loadProjectConfig("../../examples/nasty-integration")
	if err != nil {
		t.Fatal(err)
	}
	monorepo := previewCfg.Services["monorepo-web"]
	if len(monorepo.Build.Cache.From) == 0 || len(monorepo.Build.Cache.To) == 0 {
		t.Fatalf("nasty integration monorepo preview should expose explicit build cache config: %#v", monorepo.Build.Cache)
	}
	if len(monorepo.DependencyVolumes) == 0 {
		t.Fatalf("nasty integration monorepo preview should keep runtime dependency volumes alongside build cache: %#v", monorepo.DependencyVolumes)
	}

	_, deployCfg, err := loadProjectConfig("../../examples/deploy-command")
	if err != nil {
		t.Fatal(err)
	}
	env := deployCfg.Deploy.Environments["production"]
	if env.PrepareCommand == "" {
		t.Fatal("deploy-command example should include a prepareCommand fast path")
	}
	if env.Cache.Dir == "" || len(env.Cache.Build.From) == 0 || len(env.Cache.Build.To) == 0 {
		t.Fatalf("deploy-command example should expose cache dir and build cache hints: %#v", env.Cache)
	}

	for path, wants := range map[string][]string{
		"../../docs/certified-examples.md":        {"prepareCommand", "build cache specs", "cache inspect"},
		"../../examples/deploy-command/README.md": {"prepareCommand", "VIVERO_BUILD_CACHE_FROM", "VIVERO_CACHE_DIR"},
	} {
		bodyBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(bodyBytes)
		for _, want := range wants {
			if !strings.Contains(body, want) {
				t.Fatalf("%s should document fast-path contract %q", path, want)
			}
		}
	}
}

func TestDocsFrameTinyInvariantMatrixAndFrontierAgentRecipes(t *testing.T) {
	files := map[string]string{
		"readme":            "../../README.md",
		"certifiedExamples": "../../docs/certified-examples.md",
		"skill":             "../../skills/vivero/SKILL.md",
	}
	bodies := map[string]string{}
	for name, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		bodies[name] = string(body)
	}

	for _, doc := range []string{"readme", "certifiedExamples", "skill"} {
		for _, want := range []string{"Tiny invariant fixture matrix", "Preview invariants", "Deploy/release invariants", "Evidence invariants"} {
			if !strings.Contains(bodies[doc], want) {
				t.Fatalf("%s should frame the tiny invariant matrix with %q", doc, want)
			}
		}
	}
	for _, want := range []string{"Frontier-agent recipes", "Discover the live contract", "Start from a thin config", "Collect target-aware evidence", "Plan before applying deploys", "Leave a handoff"} {
		if !strings.Contains(bodies["skill"], want) {
			t.Fatalf("bundled skill should include frontier-agent recipe %q", want)
		}
	}
	for _, want := range []string{"make example-e2e", "make nasty-integration-fixtures", "make deploy-fixtures", "vivero evidence logs", "vivero evidence qa"} {
		if !strings.Contains(bodies["readme"], want) || !strings.Contains(bodies["skill"], want) {
			t.Fatalf("README and skill should ground matrix/recipes in %q", want)
		}
	}
}

func TestBundledSkillStatesThinRuntimeBoundary(t *testing.T) {
	skill, err := os.ReadFile("../../skills/vivero/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(skill)
	for _, want := range []string{
		"Keep `vivero.yml` as thin orchestration metadata",
		"do not copy Dockerfiles, compose files, dependency services, env contracts, volumes, or setup scripts into YAML when the app repo already owns them",
		"app-owned Compose stack (`runtime: compose` + `compose.file`/`compose.service`)",
		"Inline Dockerfiles are intentionally unsupported",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("bundled skill should include thin runtime boundary guidance %q", want)
		}
	}
}

func TestBundledExamplesLoad(t *testing.T) {
	examples := map[string]string{
		"../../examples/gumroad":              "gumroad-main",
		"../../examples/helper-host-products": "helper-host-products",
		"../../examples/nasty-integration":    "nasty-integration",
	}
	for path, wantName := range examples {
		t.Run(wantName, func(t *testing.T) {
			_, cfg, err := loadProjectConfig(path)
			if err != nil {
				t.Fatalf("load %s: %v", path, err)
			}
			if cfg.Project.Name != wantName {
				t.Fatalf("project name = %q, want %q", cfg.Project.Name, wantName)
			}
			if len(cfg.Services) == 0 {
				t.Fatalf("%s should declare app services", path)
			}
		})
	}
}

func TestGumroadExampleReferencesAppOwnedRuntimeAssets(t *testing.T) {
	path := "../../examples/gumroad"
	bodyBytes, err := os.ReadFile(path + "/vivero.yml")
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	for _, want := range []string{
		"Vivero owns preview orchestration",
		"Gumroad owns runtime assets",
		"rather than copying Gumroad runtime internals",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("gumroad example should document thin runtime boundary with %q", want)
		}
	}
	if strings.Contains(body, "Vivero owns the app container") {
		t.Fatal("gumroad example should not claim Vivero owns the app runtime source of truth")
	}

	_, cfg, err := loadProjectConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	svc := cfg.Services["gumroad-web"]
	if serviceRuntime(svc) != "compose" || svc.Compose.File != "docker-compose.yml" || svc.Compose.Service != "web" {
		t.Fatalf("bundled gumroad example should reference Gumroad's app-owned Compose stack, got %#v", svc.Compose)
	}
	if strings.Contains(body, "dockerfileInline") {
		t.Fatal("bundled gumroad example should not inline Dockerfiles")
	}
}

func TestConfigRejectsInlineDockerfiles(t *testing.T) {
	dir := t.TempDir()
	config := `project:
  name: inline
services:
  web:
    build:
      context: .
      dockerfileInline: |
        FROM scratch
`
	if err := os.WriteFile(dir+"/vivero.yml", []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := loadProjectConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "unsupported dockerfileInline") {
		t.Fatalf("expected dockerfileInline rejection, got %v", err)
	}
}

func TestHelperHostProductsExampleUsesExplicitHostProfiles(t *testing.T) {
	_, cfg, err := loadProjectConfig("../../examples/helper-host-products")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"default", "gumroad", "flexile"} {
		if _, ok := cfg.Profiles[name]; !ok {
			t.Fatalf("helper-host-products example should declare %s profile", name)
		}
	}
	defaultCfg, active, err := projectConfigForRequestedProfile(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if active != "default" {
		t.Fatalf("omitted profile should select default, got %q", active)
	}
	if _, ok := defaultCfg.Services["helper-web"]; !ok || len(defaultCfg.Services) != 1 {
		t.Fatalf("default profile should only run helper-web: %#v", defaultCfg.Services)
	}

	gumroadCfg, _, err := projectConfigForRequestedProfile(cfg, "gumroad")
	if err != nil {
		t.Fatal(err)
	}
	if gumroadCfg.Services["helper-web"].Env["HOST_PRODUCT"] != "gumroad" || gumroadCfg.Services["helper-web"].Env["GUMROAD_URL"] != "http://gumroad-web:3310" {
		t.Fatalf("gumroad profile should point Helper at gumroad-web: %#v", gumroadCfg.Services["helper-web"].Env)
	}

	flexileCfg, _, err := projectConfigForRequestedProfile(cfg, "flexile")
	if err != nil {
		t.Fatal(err)
	}
	if flexileCfg.Services["helper-web"].Env["HOST_PRODUCT"] != "flexile" || flexileCfg.Services["helper-web"].Env["FLEXILE_URL"] != "http://flexile-web:3000" {
		t.Fatalf("flexile profile should point Helper at flexile-web: %#v", flexileCfg.Services["helper-web"].Env)
	}
}

func TestNastyIntegrationExampleCoversMessyProjectShapes(t *testing.T) {
	_, cfg, err := loadProjectConfig("../../examples/nasty-integration")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"static-only", "app-with-db", "monorepo", "full"} {
		if _, ok := cfg.Profiles[profile]; !ok {
			t.Fatalf("nasty integration example should declare %s profile", profile)
		}
	}
	if cfg.Public.Mode != "named-tunnel" || cfg.Public.BaseDomain == "" {
		t.Fatalf("nasty integration example should cover named public tunnel planning: %#v", cfg.Public)
	}
	if _, ok := cfg.BackingServices["db"]; !ok {
		t.Fatal("nasty integration example should cover app+database previews")
	}
	if cfg.Services["monorepo-web"].Build.Dockerfile != "apps/web/Dockerfile" {
		t.Fatalf("monorepo service should reference app-owned Dockerfile, got %#v", cfg.Services["monorepo-web"].Build)
	}
	if len(cfg.Services["monorepo-web"].DependencyVolumes) < 2 {
		t.Fatalf("monorepo service should cover warm/dependency volumes: %#v", cfg.Services["monorepo-web"].DependencyVolumes)
	}
}

func TestLicenseIsMIT(t *testing.T) {
	license, err := os.ReadFile("../../LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	body := string(license)
	for _, want := range []string{"MIT License", "Copyright (c) 2026 Gianfranco Piana", "Permission is hereby granted"} {
		if !strings.Contains(body, want) {
			t.Fatalf("LICENSE should include %q", want)
		}
	}
}
