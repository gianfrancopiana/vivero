package vivero

import (
	"os"
	"strings"
	"testing"
)

func TestREADMEIsConciseAndAgentFocused(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(readme)
	for _, want := range []string{
		"Vivero is Spanish for “nursery”",
		"For coding agents, Vivero is a nursery for app changes.",
		"## What Vivero handles",
		"Docker-compatible engine, such as Docker Desktop or OrbStack",
		"Project routes, selectors, QA flows, and restart commands belong in `vivero.yml`.",
		"## Basic use",
		"public:` config",
		"## License\n\n[MIT](LICENSE)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("README should include %q", want)
		}
	}

	skill := strings.Index(body, "## Bundled skill")
	yml := strings.Index(body, "## `vivero.yml`")
	if skill == -1 || yml == -1 || skill > yml {
		t.Fatal("README should put bundled skill support before the vivero.yml example")
	}

	for _, avoid := range []string{"```mermaid", "## Flow", "## Core idea", "## Agent workflow", "## Repository layout", "agents and humans", "Human or agent", "Docker preview", "health-gated", "control plane", "Nursery, place where", "Leaves product decisions"} {
		if strings.Contains(body, avoid) {
			t.Fatalf("README should avoid jargon, noisy sections, or mixed framing %q", avoid)
		}
	}
}

func TestBundledExamplesLoad(t *testing.T) {
	examples := map[string]string{
		"../../examples/gumroad":              "gumroad-main",
		"../../examples/helper-host-products": "helper-host-products",
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
