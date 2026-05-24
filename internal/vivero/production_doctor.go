package vivero

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ProductionDoctorResult struct {
	OK          bool                         `json:"ok"`
	Path        string                       `json:"path"`
	Project     string                       `json:"project,omitempty"`
	Profile     string                       `json:"profile,omitempty"`
	Verdict     string                       `json:"verdict"`
	Diagnostics []ProductionDoctorDiagnostic `json:"diagnostics"`
}

type ProductionDoctorDiagnostic struct {
	Level      string `json:"level"`
	Code       string `json:"code"`
	Path       string `json:"path,omitempty"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

func doctorProjectPath(args []string) string {
	if path, ok := flagValue(args, "--project"); ok && strings.TrimSpace(path) != "" {
		return path
	}
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--production" {
			continue
		}
		filtered = append(filtered, arg)
	}
	pos := positionalArgs(filtered)
	if len(pos) > 0 {
		return pos[0]
	}
	return "."
}

func (a *App) ProductionDoctorForEnvironment(path, environment string) (ProductionDoctorResult, error) {
	if strings.TrimSpace(environment) == "" {
		environment = "production"
	}
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	root, cfg, err := loadProjectConfig(path)
	report := ProductionDoctorResult{OK: true, Path: expandPath(path), Verdict: "candidate", Diagnostics: []ProductionDoctorDiagnostic{}}
	if root != "" {
		report.Path = root
		if abs, absErr := filepath.Abs(root); absErr == nil {
			report.Path = abs
		}
	}
	if err != nil {
		report.addDiagnostic("error", "config-load", "", err.Error(), "Fix vivero.yml first, then rerun the production readiness doctor.")
		report.finish()
		return report, nil
	}
	if profiled, profile, profileErr := projectConfigForEnvironment(cfg, environment); profileErr != nil {
		report.Project = cfg.Project.Name
		report.addDiagnostic("error", "profile-load", "profiles", profileErr.Error(), "Fix the environment profile in vivero.yml before rerunning the production readiness doctor.")
		report.finish()
		return report, nil
	} else {
		cfg = profiled
		report.Profile = profile
	}
	report.Project = cfg.Project.Name
	report.addDiagnostic("info", "deploy-surface", "", "Vivero production deploys are app-owned commands gated by this read-only readiness check.", "Use deploy plan first; apply only plans whose production doctor diagnostics are not blocked.")
	productionDoctorCheckControlPlane(&report)
	productionDoctorCheckServices(&report, cfg)
	productionDoctorCheckBackingServices(&report, cfg)
	report.finish()
	return report, nil
}

func (r *ProductionDoctorResult) addDiagnostic(level, code, path, message, suggestion string) {
	r.Diagnostics = append(r.Diagnostics, ProductionDoctorDiagnostic{Level: level, Code: code, Path: path, Message: message, Suggestion: suggestion})
}

func (r *ProductionDoctorResult) finish() {
	errors := 0
	warnings := 0
	for _, diag := range r.Diagnostics {
		switch diag.Level {
		case "error":
			errors++
		case "warning":
			warnings++
		}
	}
	r.OK = errors == 0
	switch {
	case errors > 0:
		r.Verdict = "blocked"
	case warnings > 0:
		r.Verdict = "candidate"
	default:
		r.Verdict = "candidate"
	}
}

func productionDoctorHuman(report ProductionDoctorResult) string {
	var b strings.Builder
	status := report.Verdict
	if !report.OK {
		status = "blocked"
	}
	b.WriteString(fmt.Sprintf("production readiness %s: %s\n", status, report.Path))
	for _, diag := range report.Diagnostics {
		if diag.Path != "" {
			b.WriteString(fmt.Sprintf("%s %s %s: %s\n", diag.Level, diag.Code, diag.Path, diag.Message))
		} else {
			b.WriteString(fmt.Sprintf("%s %s: %s\n", diag.Level, diag.Code, diag.Message))
		}
		if diag.Suggestion != "" {
			b.WriteString(fmt.Sprintf("  suggestion: %s\n", diag.Suggestion))
		}
	}
	return b.String()
}

func productionDoctorCheckControlPlane(report *ProductionDoctorResult) {
	if os.Getenv("VIVERO_ALLOW_REMOTE_CONTROL") == "1" {
		report.addDiagnostic("error", "remote-control-plane", "VIVERO_ALLOW_REMOTE_CONTROL", "remote control-plane access is enabled without a production authentication model", "Keep Vivero local-only for previews; a production control plane needs explicit authentication and authorization before non-loopback binds are allowed.")
	}
}

func productionDoctorCheckServices(report *ProductionDoctorResult, cfg ProjectConfig) {
	productionDoctorCheckPublicRoutes(report, cfg)
	for _, name := range sortedMapKeys(cfg.Services) {
		svc := cfg.Services[name]
		base := "services." + name
		if strings.TrimSpace(svc.Source) != "" {
			report.addDiagnostic("error", "mutable-source", base+".source", fmt.Sprintf("service %s depends on a mutable preview source", name), "Production deploys need immutable release artifacts; build and pin an image digest instead of mounting a worktree/source path.")
		}
		if imageBuildConfigured(svc.Build) {
			report.addDiagnostic("error", "mutable-build", base+".build", fmt.Sprintf("service %s builds from project context at runtime", name), "Move build output to an immutable image artifact and reference it by digest for production.")
		}
		productionDoctorCheckImage(report, base+".image", svc.Image)
		productionDoctorCheckResources(report, base+".resources", svc.ResourceLimits)
		productionDoctorCheckHealth(report, base+".health", svc.Health)
		productionDoctorCheckEnv(report, base+".env", svc.Env)
		productionDoctorCheckVolumes(report, base+".dependencyVolumes", svc.DependencyVolumes)
	}
}

func productionDoctorCheckBackingServices(report *ProductionDoctorResult, cfg ProjectConfig) {
	for _, name := range sortedMapKeys(cfg.BackingServices) {
		backing := cfg.BackingServices[name]
		base := "backingServices." + name
		productionDoctorCheckImage(report, base+".image", backing.Image)
		productionDoctorCheckResources(report, base+".resources", backing.ResourceLimits)
		productionDoctorCheckHealth(report, base+".health", backing.Health)
		productionDoctorCheckEnv(report, base+".env", backing.Env)
		productionDoctorCheckVolumes(report, base+".dependencyVolumes", backing.DependencyVolumes)
	}
}

func productionDoctorCheckPublicRoutes(report *ProductionDoctorResult, cfg ProjectConfig) {
	var publicServices []string
	for _, name := range sortedMapKeys(cfg.Services) {
		if cfg.Services[name].Public {
			publicServices = append(publicServices, name)
		}
	}
	if len(publicServices) == 0 {
		return
	}
	if !isNamedPublicTunnel(cfg.Public) {
		for _, service := range publicServices {
			report.addDiagnostic("error", "quick-tunnel-production", "public", fmt.Sprintf("public service %s would rely on an ephemeral quick tunnel", service), "Production ingress needs explicit DNS/TLS ownership, not Cloudflare quick tunnels.")
		}
		return
	}
	_, err := plannedNamedPublicHosts(UpRequest{Project: cfg.Project.Name, ID: cfg.Project.Name, Labels: map[string]string{}, Metadata: map[string]string{}}, cfg)
	if err != nil {
		report.addDiagnostic("error", "public-route-invalid", "public", fmt.Sprintf("stable public route plan is invalid: %v", err), "Set public.baseDomain, public.hostname, or public.hostnameTemplate to unique valid stable DNS hosts before production.")
	}
}

func productionDoctorCheckImage(report *ProductionDoctorResult, path, image string) {
	image = strings.TrimSpace(image)
	if image == "" {
		return
	}
	if !strings.Contains(image, "@sha256:") {
		report.addDiagnostic("warning", "image-not-immutable", path, "image reference is not pinned to a digest", "Use an immutable image reference such as registry.example.com/app@sha256:<digest> for production candidates.")
	}
}

func productionDoctorCheckResources(report *ProductionDoctorResult, path string, limits ResourceLimits) {
	if strings.TrimSpace(limits.CPUs) == "" && strings.TrimSpace(limits.Memory) == "" {
		report.addDiagnostic("warning", "resource-limits-missing", path, "no CPU or memory limits are declared", "Declare resource limits before treating the service as production-deployable.")
	}
}

func productionDoctorCheckHealth(report *ProductionDoctorResult, path string, health HealthConfig) {
	if strings.TrimSpace(health.Timeout) == "" {
		report.addDiagnostic("warning", "health-timeout-missing", path+".timeout", "no explicit health timeout policy is declared", "Set health.timeout so startup/readiness failures are bounded and diagnosable.")
	}
}

func productionDoctorCheckEnv(report *ProductionDoctorResult, path string, env map[string]string) {
	for _, name := range sortedMapKeys(env) {
		value := strings.TrimSpace(env[name])
		if value == "" || !looksSensitiveEnvName(name) || looksSecretBackendReference(value) {
			continue
		}
		report.addDiagnostic("warning", "inline-secret", path+"."+name, fmt.Sprintf("environment variable %s looks sensitive and is set inline", name), "Use a secret backend reference instead of storing secret values in vivero.yml.")
	}
}

func productionDoctorCheckVolumes(report *ProductionDoctorResult, path string, volumes []VolumeConfig) {
	for i, volume := range volumes {
		lifetime, err := normalizeVolumeLifetime(volume.Lifetime)
		if err != nil || lifetime == "preview" {
			continue
		}
		report.addDiagnostic("warning", "backup-policy-missing", fmt.Sprintf("%s[%d]", path, i), fmt.Sprintf("persistent volume %s has no backup/restore policy", volume.Name), "Production state needs explicit backup and restore checks before deployment.")
	}
}

func looksSensitiveEnvName(name string) bool {
	upper := strings.ToUpper(name)
	for _, token := range []string{"SECRET", "PASSWORD", "PASSWD", "TOKEN", "API_KEY", "PRIVATE_KEY", "CREDENTIAL"} {
		if strings.Contains(upper, token) {
			return true
		}
	}
	return false
}

func looksSecretBackendReference(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "op://") || strings.HasPrefix(trimmed, "vault://") || strings.HasPrefix(trimmed, "aws-sm://") || strings.HasPrefix(trimmed, "gcp-sm://") || strings.HasPrefix(trimmed, "${") || strings.HasPrefix(trimmed, "$")
}
