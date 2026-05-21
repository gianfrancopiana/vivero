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
		"safe app-operations control plane",
		"Preview lane",
		"Deploy/release lane",
		"Evidence/debug lane",
		"the app owns how it runs and deploys; Vivero owns orchestration, safety gates, local state, command contracts, and evidence",
		"Golden path: preview, prove, deploy",
		"vivero preview up",
		"vivero deploy plan",
		"vivero release events",
		"docs/certified-examples.md",
		"## Limits",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("README should frame preview/deploy/evidence contract with %q", want)
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
		"certify:\n\t$(MAKE) audit",
		"\t$(MAKE) example-e2e",
		"\t$(MAKE) integration-fixtures",
		"\t$(MAKE) nasty-integration-fixtures",
		"\t$(MAKE) dogfood-configs",
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

func TestBundledSkillStatesThinRuntimeBoundary(t *testing.T) {
	skill, err := os.ReadFile("../../skills/vivero/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(skill)
	for _, want := range []string{
		"Keep `vivero.yml` as thin orchestration metadata",
		"do not copy Dockerfiles, compose files, env contracts, or setup scripts into YAML when the app repo already owns them",
		"Reference app-owned images, Dockerfiles, or prebuild commands instead",
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
		"Gumroad owns the",
		"instead of copying",
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
	build := cfg.Services["gumroad-web"].Build
	if build.Dockerfile != "docker/web/Dockerfile" {
		t.Fatalf("bundled gumroad example should reference Gumroad's app-owned Dockerfile, got %q", build.Dockerfile)
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
