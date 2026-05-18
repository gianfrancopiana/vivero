package vivero

import "testing"

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
