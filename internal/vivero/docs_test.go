package vivero

import (
	"os"
	"strings"
	"testing"
)

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
