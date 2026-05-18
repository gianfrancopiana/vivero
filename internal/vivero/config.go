package vivero

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func loadProjectConfig(path string) (string, ProjectConfig, error) {
	path = expandPath(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", ProjectConfig{}, err
	}
	configPath := path
	root := filepath.Dir(path)
	if info.IsDir() {
		root = path
		configPath = filepath.Join(path, "vivero.yml")
	}
	b, err := os.ReadFile(configPath)
	if err != nil {
		return "", ProjectConfig{}, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(b, &node); err != nil {
		return "", ProjectConfig{}, err
	}
	if err := rejectConfigKey(configPath, &node, "dockerfileInline"); err != nil {
		return "", ProjectConfig{}, err
	}
	var cfg ProjectConfig
	if err := node.Decode(&cfg); err != nil {
		return "", ProjectConfig{}, err
	}
	if cfg.Project.Name == "" {
		return "", ProjectConfig{}, fmt.Errorf("%s missing project.name", configPath)
	}
	if cfg.Sources == nil {
		cfg.Sources = map[string]SourceConfig{}
	}
	if cfg.Services == nil {
		cfg.Services = map[string]ServiceConfig{}
	}
	if cfg.BackingServices == nil {
		cfg.BackingServices = map[string]BackingConfig{}
	}
	if cfg.Routes == nil {
		cfg.Routes = map[string]string{}
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]ProfileConfig{}
	}
	if err := validateProjectConfig(configPath, cfg); err != nil {
		return "", ProjectConfig{}, err
	}
	return root, cfg, nil
}

func rejectConfigKey(configPath string, node *yaml.Node, key string) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i]
			v := node.Content[i+1]
			if k.Value == key {
				return fmt.Errorf("%s uses unsupported %s; keep Dockerfiles in the app repo and reference build.dockerfile or prebuild/image instead", configPath, key)
			}
			if err := rejectConfigKey(configPath, v, key); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range node.Content {
		if err := rejectConfigKey(configPath, child, key); err != nil {
			return err
		}
	}
	return nil
}

func validateProjectConfig(configPath string, cfg ProjectConfig) error {
	for name, svc := range cfg.Services {
		if _, err := servicePortPlan(svc); err != nil {
			return fmt.Errorf("%s service %s has invalid port configuration: %w", configPath, name, err)
		}
		if err := validateRuntimeEnv(configPath, "service", name, svc.Env); err != nil {
			return err
		}
		if err := validateDependencyVolumes(configPath, "service", name, svc.DependencyVolumes); err != nil {
			return err
		}
		runtime := serviceRuntime(svc)
		switch runtime {
		case "docker":
			if strings.TrimSpace(svc.Image) == "" && !imageBuildConfigured(svc.Build) {
				return fmt.Errorf("%s service %s must declare image or build; Vivero app services run in containers only", configPath, name)
			}
		case "host":
			return fmt.Errorf("%s service %s uses runtime host; Vivero runs app services in containers only", configPath, name)
		default:
			return fmt.Errorf("%s service %s has unsupported runtime %q; use docker", configPath, name, runtime)
		}
	}
	for name, backing := range cfg.BackingServices {
		if _, exists := cfg.Services[name]; exists {
			return fmt.Errorf("%s service name %s is declared in both services and backingServices", configPath, name)
		}
		if strings.TrimSpace(backing.Image) == "" {
			return fmt.Errorf("%s backing service %s must declare image", configPath, name)
		}
		if err := validateRuntimeEnv(configPath, "backing service", name, backing.Env); err != nil {
			return err
		}
		if err := validateDependencyVolumes(configPath, "backing service", name, backing.DependencyVolumes); err != nil {
			return err
		}
	}
	for i, step := range cfg.Setup.AfterSeeds {
		if _, err := normalizeSetupPolicy(step.Policy); err != nil {
			return fmt.Errorf("%s setup.afterSeeds[%d]: %w", configPath, i, err)
		}
		if strings.TrimSpace(step.Command) == "" {
			continue
		}
		service := strings.TrimSpace(step.Service)
		if service == "" {
			return fmt.Errorf("%s setup.afterSeeds[%d] must target a service", configPath, i)
		}
		if _, ok := cfg.Services[service]; !ok {
			return fmt.Errorf("%s setup.afterSeeds[%d] references unknown service %s", configPath, i, service)
		}
	}
	if err := validateWarmConfig(configPath, cfg.Warm); err != nil {
		return err
	}
	if err := validateProfilesConfig(configPath, cfg); err != nil {
		return err
	}
	return nil
}

func validateRuntimeEnv(configPath, kind, name string, env map[string]string) error {
	for _, key := range sortedMapKeys(env) {
		if err := validateDockerEnvName(key); err != nil {
			return fmt.Errorf("%s %s %s env name %q is invalid: %w", configPath, kind, name, key, err)
		}
		if strings.ContainsAny(env[key], "\x00\n\r") {
			return fmt.Errorf("%s %s %s env value for %s contains unsupported newline or NUL", configPath, kind, name, key)
		}
	}
	return nil
}

func validateDependencyVolumes(configPath, kind, name string, volumes []VolumeConfig) error {
	for i, vol := range volumes {
		if strings.TrimSpace(vol.Name) == "" {
			return fmt.Errorf("%s %s %s dependencyVolumes[%d] volume name is required", configPath, kind, name, i)
		}
		if _, err := validateDockerMountTarget(vol.Target); err != nil {
			return fmt.Errorf("%s %s %s dependencyVolumes[%d] volume target: %w", configPath, kind, name, i, err)
		}
		if _, err := normalizeVolumeLifetime(vol.Lifetime); err != nil {
			return fmt.Errorf("%s %s %s dependencyVolumes[%d]: %w", configPath, kind, name, i, err)
		}
	}
	return nil
}

func validateWarmConfig(configPath string, warm WarmConfig) error {
	for i, ref := range warm.BaselineRefs {
		if strings.TrimSpace(ref) == "" {
			return fmt.Errorf("%s warm.baselineRefs[%d] must not be empty", configPath, i)
		}
		if strings.ContainsAny(ref, "\x00\n\r") {
			return fmt.Errorf("%s warm.baselineRefs[%d] contains unsupported newline or NUL", configPath, i)
		}
	}
	for i, path := range warm.Fingerprint.Paths {
		raw := strings.TrimSpace(path)
		cleaned := filepath.Clean(raw)
		if raw == "" || cleaned == "." || filepath.IsAbs(raw) || filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || strings.ContainsAny(raw, "\x00\n\r") {
			return fmt.Errorf("%s warm.fingerprint.paths[%d] must be a safe project-relative path", configPath, i)
		}
	}
	return nil
}

func imageBuildConfigured(build ImageBuildConfig) bool {
	return strings.TrimSpace(build.Context) != "" || strings.TrimSpace(build.Dockerfile) != "" || strings.TrimSpace(build.Tag) != "" || len(build.Args) > 0
}

func (a *App) SyncProject(path string) (ProjectRecord, error) {
	root, cfg, err := loadProjectConfig(path)
	if err != nil {
		return ProjectRecord{}, err
	}
	abs, _ := filepath.Abs(root)
	return a.saveProject(abs, cfg)
}

func (a *App) refreshProjectConfig(project ProjectRecord) (ProjectRecord, error) {
	root, cfg, err := loadProjectConfig(project.Path)
	if err != nil {
		return project, fmt.Errorf("refresh project config %s: %w", project.Path, err)
	}
	if cfg.Project.Name != project.Name {
		return project, fmt.Errorf("refresh project config %s: project.name is %q, want %q", project.Path, cfg.Project.Name, project.Name)
	}
	abs, _ := filepath.Abs(root)
	return a.saveProject(abs, cfg)
}

func serviceRuntime(s ServiceConfig) string {
	runtime := strings.ToLower(strings.TrimSpace(s.Runtime))
	switch runtime {
	case "", "container", "containers", "docker", "docker-cli", "orbstack":
		return "docker"
	default:
		return runtime
	}
}

func (a *App) capabilities() map[string]any {
	return map[string]any{
		"name":                  "vivero",
		"version":               Version,
		"home":                  a.Home,
		"localOnlyControlPlane": true,
		"sourceModes":           []string{"managed", "external"},
		"runtimes":              []string{"docker"},
		"features":              []string{"projects", "worktrees", "health-gated-up", "events", "secrets", "sync", "diff", "exec", "logs", "smoke", "screenshots", "screenshot-breakpoints", "color-scheme-evidence", "qa-plan", "qa-run", "qa-record", "qa-report", "local-default-evidence", "cloudflared-quick-tunnel", "cloudflare-named-tunnel", "fixed-public-hostnames", "profiles", "profile-service-env", "project-lifetime-volumes", "smart-warm-volumes", "setup-once-per-project", "bundled-skill"},
		"invariants":            []string{"json-first", "no-github-auth-in-core", "control-plane-local-only", "url-after-health", "containerized-apps-only"},
	}
}

func commandCatalog() []map[string]any {
	return []map[string]any{
		{"name": "serve", "agentSafe": false, "description": "start local HTTP control plane"},
		{"name": "capabilities", "agentSafe": true},
		{"name": "commands", "agentSafe": true},
		{"name": "schema", "agentSafe": true},
		{"name": "doctor", "agentSafe": true},
		{"name": "projects sync", "agentSafe": true},
		{"name": "projects", "agentSafe": true},
		{"name": "project inspect", "agentSafe": true},
		{"name": "up", "agentSafe": true, "requires": []string{"--id", "--profile when selecting a non-default profile", "--source when overriding refs/paths"}},
		{"name": "wait", "agentSafe": true},
		{"name": "down", "agentSafe": true},
		{"name": "list", "agentSafe": true},
		{"name": "inspect", "agentSafe": true},
		{"name": "events", "agentSafe": true},
		{"name": "sync", "agentSafe": true},
		{"name": "rm", "agentSafe": true},
		{"name": "diff", "agentSafe": true},
		{"name": "exec", "agentSafe": true},
		{"name": "logs", "agentSafe": true},
		{"name": "screenshot", "agentSafe": true, "defaultTarget": "local", "publicTargetFlag": "--public", "colorSchemeFlag": "--color-scheme"},
		{"name": "smoke", "agentSafe": true},
		{"name": "qa plan/context/run/record/report", "agentSafe": true, "defaultTarget": "local", "publicTargetFlag": "--public for plan/run/report only", "evidenceConfig": "agent.qa.evidence"},
		{"name": "prebuild", "agentSafe": true},
		{"name": "secrets set/list/unset", "agentSafe": true, "secretValuesWriteOnly": true},
		{"name": "skill install/print/path/doctor", "agentSafe": true},
	}
}

func schemaFor(command string) map[string]any {
	schemas := map[string]any{
		"up":         map[string]any{"usage": "vivero up <project> --id <preview-id> [--profile <name>] --source name.path=/repo --wait --timeout 5m --json --no-input", "returns": "preview with health-gated services and URLs", "profile": "selects a config profile from project.profiles; if omitted, profile 'default' is used when present"},
		"down":       map[string]any{"usage": "vivero down <preview-id> [--discard|--archive-patch|--keep-worktree] --json --no-input"},
		"sync":       map[string]any{"usage": "vivero sync <preview-id> <source> <relative-path> --from <local-path> --json --no-input"},
		"exec":       map[string]any{"usage": "vivero exec <preview-id> <service> --json --no-input -- <command...>"},
		"qa":         map[string]any{"usage": "vivero qa <plan|context|run|report> <preview-id> [--scope <name|all>] [--public|--target local|public|origin] --json --no-input; vivero qa record <preview-id> [--scope <name|all>] --json --no-input", "defaults": map[string]any{"target": "local", "recordFormat": "mp4", "width": 1280, "height": 800}, "planReturns": "driver-agnostic QA context with local-by-default services, pages, flows, checks, artifact paths, and concrete evidence commands derived from agent.qa.evidence", "run": "runs deterministic smoke checks, captures declared page screenshots from the YAML-backed evidence matrix unless --no-screenshots is passed, and writes a report scaffold", "record": "records declared QA flows through the local/proxy preview URL; use the qa plan evidence.recordings commands for configured color schemes", "recordOptions": map[string]any{"colorScheme": "optional light|dark primitive used by generated evidence.recordings.commands"}, "config": "agent.qa and agent.qa.evidence in vivero.yml"},
		"screenshot": map[string]any{"usage": "vivero screenshot <preview-id> <service> [path] [--public|--target local|public|origin] [--color-scheme light|dark] --width 1280 --height 800 --breakpoint desktop=1440x900 --breakpoint mobile=390x844 --output-dir <dir> --json --no-input", "defaults": map[string]any{"width": 1280, "height": 800, "target": "local", "crop": false}, "projectBreakpoints": "agent.screenshotBreakpoints plus --breakpoints"},
		"secrets":    map[string]any{"usage": "vivero secrets set <project> KEY=value --json --no-input", "listReturns": "keys only"},
		"project":    map[string]any{"configFile": "vivero.yml", "required": "project.name", "profiles": "optional profiles.<name> may select services, backingServices, smokeTests, and serviceEnv; omitted --profile uses default when present", "dependencyVolumeLifetimes": []string{"preview", "project", "smart"}, "setupPolicies": []string{"per-preview", "once-per-project"}, "warm": map[string]any{"baselineRefs": "refs that update canonical smart warm volumes; defaults to main/master", "fingerprint.paths": "project-relative paths that invalidate smart warm baselines"}},
	}
	if command != "" {
		if s, ok := schemas[command]; ok {
			return map[string]any{"command": command, "schema": s}
		}
		return map[string]any{"command": command, "schema": map[string]any{"usage": "see vivero commands --json"}}
	}
	return map[string]any{"version": 1, "commands": schemas}
}
