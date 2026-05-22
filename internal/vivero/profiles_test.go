package vivero

import (
	"reflect"
	"testing"
)

func TestProjectConfigProfileAppliesServiceEnvOverlay(t *testing.T) {
	cfg := ProjectConfig{
		Services: map[string]ServiceConfig{
			"helper-web": {
				Env: map[string]string{
					"NODE_ENV":     "development",
					"HOST_PRODUCT": "standalone",
				},
			},
			"gumroad-web": {},
		},
		Profiles: map[string]ProfileConfig{
			"gumroad": {
				Services: []string{"helper-web", "gumroad-web"},
				ServiceEnv: map[string]map[string]string{
					"helper-web": {
						"HOST_PRODUCT": "gumroad",
						"GUMROAD_URL":  "http://gumroad-web:3310",
					},
				},
			},
		},
	}

	profiled, active, err := projectConfigForRequestedProfile(cfg, "gumroad")
	if err != nil {
		t.Fatal(err)
	}
	if active != "gumroad" {
		t.Fatalf("active profile = %q", active)
	}
	helperEnv := profiled.Services["helper-web"].Env
	if helperEnv["NODE_ENV"] != "development" {
		t.Fatalf("base env missing from profile overlay: %#v", helperEnv)
	}
	if helperEnv["HOST_PRODUCT"] != "gumroad" {
		t.Fatalf("profile env should override service env: %#v", helperEnv)
	}
	if helperEnv["GUMROAD_URL"] != "http://gumroad-web:3310" {
		t.Fatalf("profile env should add host service URL: %#v", helperEnv)
	}
	if cfg.Services["helper-web"].Env["HOST_PRODUCT"] != "standalone" {
		t.Fatalf("profile env overlay mutated base config: %#v", cfg.Services["helper-web"].Env)
	}
}

func TestProfileFiltersAgentQAScopesAndDefaultService(t *testing.T) {
	cfg := ProjectConfig{
		Sources: map[string]SourceConfig{
			"api-src": {Path: "./api"},
			"web-src": {Path: "./web"},
		},
		Services: map[string]ServiceConfig{
			"api": {Source: "api-src"},
			"web": {Source: "web-src"},
		},
		Setup: SetupConfig{AfterSeeds: []SetupStep{
			{Command: RuntimeCommand{Shell: "api setup"}, Service: "api"},
			{Command: RuntimeCommand{Shell: "web setup"}, Service: "web"},
			{Command: RuntimeCommand{Shell: "global setup"}},
		}},
		Agent: AgentConfig{
			DefaultPreviewService: "web",
			CommonPages: map[string]AgentPage{
				"api":      {Service: "api", Path: "/api"},
				"home":     {Service: "web", Path: "/"},
				"implicit": {Path: "/status"},
			},
			SmokeTests: []SmokeTest{
				{Name: "api smoke", Service: "api"},
				{Name: "web smoke", Service: "web"},
			},
			QA: QAConfig{Scopes: []QAScope{{
				Name:  "core",
				Pages: []string{"home", "api", "implicit", "missing"},
				Flows: []QAFlow{
					{Name: "api flow", Service: "api"},
					{Name: "default flow"},
					{Name: "web flow", Service: "web"},
				},
			}}},
		},
		Profiles: map[string]ProfileConfig{
			"api": {
				Services:   []string{"api"},
				SmokeTests: []string{"api smoke"},
			},
		},
	}

	profiled, active, err := projectConfigForProfile(cfg, "api")
	if err != nil {
		t.Fatal(err)
	}
	if active != "api" {
		t.Fatalf("active profile = %q", active)
	}
	if _, ok := profiled.Services["web"]; ok {
		t.Fatalf("web service should be filtered out: %#v", profiled.Services)
	}
	if _, ok := profiled.Sources["web-src"]; ok {
		t.Fatalf("unused web source should be filtered out: %#v", profiled.Sources)
	}
	if profiled.Agent.DefaultPreviewService != "api" {
		t.Fatalf("default preview service = %q, want api", profiled.Agent.DefaultPreviewService)
	}
	if _, ok := profiled.Agent.CommonPages["home"]; ok {
		t.Fatalf("web page should be filtered out: %#v", profiled.Agent.CommonPages)
	}
	if got := profiled.Agent.QA.Scopes[0].Pages; !reflect.DeepEqual(got, []string{"api", "implicit"}) {
		t.Fatalf("filtered QA page refs = %#v", got)
	}
	if got := profiled.Agent.QA.Scopes[0].Flows; len(got) != 2 || got[0].Name != "api flow" || got[1].Name != "default flow" {
		t.Fatalf("filtered QA flows = %#v", got)
	}
	if got := profiled.Agent.SmokeTests; len(got) != 1 || got[0].Name != "api smoke" {
		t.Fatalf("filtered smoke tests = %#v", got)
	}
	if got := profiled.Setup.AfterSeeds; len(got) != 2 || got[0].Command.Display() != "api setup" || got[1].Command.Display() != "global setup" {
		t.Fatalf("filtered setup steps = %#v", got)
	}
}
