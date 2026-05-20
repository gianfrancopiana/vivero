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
	if cfg.Deploy.Environments == nil {
		cfg.Deploy.Environments = map[string]DeployEnvironmentConfig{}
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
	if cfg.Resources.MaxConcurrentPreviews < 0 {
		return fmt.Errorf("%s resources.maxConcurrentPreviews must be >= 0", configPath)
	}
	if cfg.Resources.MaxStartupConcurrency < 0 {
		return fmt.Errorf("%s resources.maxStartupConcurrency must be >= 0", configPath)
	}
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
		if err := validateFingerprintPaths(configPath, fmt.Sprintf("setup.afterSeeds[%d].fingerprint.paths", i), step.Fingerprint.Paths); err != nil {
			return err
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
	if err := validateDeployConfig(configPath, cfg.Deploy); err != nil {
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
	return validateFingerprintPaths(configPath, "warm.fingerprint.paths", warm.Fingerprint.Paths)
}

func validateDeployConfig(configPath string, deploy DeployConfig) error {
	for _, name := range sortedMapKeys(deploy.Environments) {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%s deploy.environments has an empty environment name", configPath)
		}
		env := deploy.Environments[name]
		for field, value := range map[string]string{"strategy": env.Strategy, "applyCommand": env.ApplyCommand, "statusCommand": env.StatusCommand, "rollbackCommand": env.RollbackCommand} {
			if strings.ContainsAny(value, "\x00") {
				return fmt.Errorf("%s deploy.environments.%s.%s contains unsupported NUL", configPath, name, field)
			}
		}
		for field, value := range map[string]string{"activeSlotCommand": env.BlueGreen.ActiveSlotCommand, "prepareCommand": env.BlueGreen.PrepareCommand, "smokeCommand": env.BlueGreen.SmokeCommand, "promoteCommand": env.BlueGreen.PromoteCommand, "statusCommand": env.BlueGreen.StatusCommand, "rollbackCommand": env.BlueGreen.RollbackCommand} {
			if strings.ContainsAny(value, "\x00") {
				return fmt.Errorf("%s deploy.environments.%s.blueGreen.%s contains unsupported NUL", configPath, name, field)
			}
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
		"build":                 buildVersionInfo(),
		"home":                  a.Home,
		"localOnlyControlPlane": true,
		"sourceModes":           []string{"managed", "external"},
		"runtimes":              []string{"docker"},
		"features":              []string{"preview-runtime", "projects", "worktrees", "health-gated-up", "events", "startup-diagnostics", "config-doctor", "production-readiness-doctor", "app-owned-deploy-surface", "blue-green-deploy", "release-status", "release-rollback", "secrets", "sync", "diff", "exec", "logs", "smoke", "screenshots", "screenshot-breakpoints", "color-scheme-evidence", "qa-plan", "qa-run", "qa-record", "qa-report", "authenticated-qa", "local-default-evidence", "cloudflared-quick-tunnel", "cloudflare-named-tunnel", "fixed-public-hostnames", "profiles", "profile-service-env", "project-lifetime-volumes", "smart-warm-volumes", "setup-once-per-project", "setup-once-per-fingerprint", "bounded-parallel-startup", "bundled-skill", "cli-manifest", "clig-compatible-help", "cli-coverage-ratchet", "release-checksums", "build-provenance"},
		"invariants":            []string{"json-first", "stable-json-errors", "no-required-prompts", "no-github-auth-in-core", "control-plane-local-only", "url-after-health", "containerized-apps-only"},
	}
}
