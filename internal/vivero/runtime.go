package vivero

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

func (a *App) Up(req UpRequest) (PreviewRecord, error) {
	if req.Project == "" {
		return PreviewRecord{}, fmt.Errorf("project is required")
	}
	if req.ID == "" {
		return PreviewRecord{}, fmt.Errorf("--id is required")
	}
	project, err := a.getProject(req.Project)
	if err != nil {
		return PreviewRecord{}, err
	}
	project, err = a.refreshProjectConfig(project)
	if err != nil {
		return PreviewRecord{}, err
	}
	runtimeConfig, activeProfile, err := projectConfigForRequestedProfile(project.Config, req.Profile)
	if err != nil {
		return PreviewRecord{}, err
	}
	req.Profile = activeProfile
	if req.Timeout == 0 {
		req.Timeout = 5 * time.Minute
	}
	if existing, found, err := a.cleanupExistingPreviewForUp(req.ID); err != nil {
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return existing, err
	} else if found {
		if err := removePreviewDependencyVolumes(req.ID, project.Config); err != nil {
			_ = a.setPreviewStatus(req.ID, "unhealthy")
			return existing, fmt.Errorf("cleanup existing preview dependency volumes %s: %w", req.ID, err)
		}
		a.recordEvent(req.ID, "info", "preview.replacing", "existing preview resources pruned before restart", "", nil)
	}
	if project.Config.Resources.MaxConcurrentPreviews > 0 {
		previews, _ := a.listPreviews()
		running := 0
		for _, p := range previews {
			if p.ID == req.ID {
				continue
			}
			if p.Status == "running" || p.Status == "pending" || p.Status == "starting_apps" {
				running++
			}
		}
		if running >= project.Config.Resources.MaxConcurrentPreviews {
			return PreviewRecord{}, fmt.Errorf("resource cap reached: %d previews running", running)
		}
	}
	p := PreviewRecord{ID: req.ID, Project: req.Project, Profile: activeProfile, Status: "pending", Labels: req.Labels, Metadata: req.Metadata, Sources: map[string]PreviewSource{}, Services: map[string]PreviewService{}, CreatedAt: nowUTC()}
	if err := a.upsertPreview(p); err != nil {
		return p, err
	}
	eventMetadata := map[string]string{}
	if activeProfile != "" {
		eventMetadata["profile"] = activeProfile
	}
	a.recordEvent(req.ID, "info", "preview.created", "preview requested", "", eventMetadata)
	if err := a.setPreviewStatus(req.ID, "preparing_source"); err != nil {
		return p, err
	}
	for name, src := range runtimeConfig.Sources {
		timer := startOperationTimer()
		resolved, err := a.resolveSource(runtimeConfig.Project.Name, project.Path, req.ID, name, src, req.Sources)
		if err != nil {
			a.recordEvent(req.ID, "error", "source.failed", err.Error(), name, timer.metadata(nil))
			_ = a.setPreviewStatus(req.ID, "unhealthy")
			return p, err
		}
		p.Sources[name] = resolved
		if err := a.saveSource(req.ID, resolved); err != nil {
			return p, err
		}
		a.recordEvent(req.ID, "info", "source.ready", "source ready", name, timer.metadata(map[string]string{"path": resolved.Path, "mode": resolved.Mode, "ref": resolved.Ref}))
	}
	if err := a.validateNamedPublicRouteConflicts(req, runtimeConfig); err != nil {
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	if err := a.buildServiceImages(project, req.ID, p.Sources, &runtimeConfig); err != nil {
		a.recordEvent(req.ID, "error", "image.build_failed", err.Error(), "", nil)
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	warmState := warmRunState{Project: runtimeConfig.Project.Name, PreviewID: req.ID, Mode: warmModeNone}
	runtimeConfig, warmState, err = a.prepareSmartWarmVolumes(project, req, runtimeConfig, p.Sources)
	if err != nil {
		a.recordEvent(req.ID, "error", "warm.failed", err.Error(), "", nil)
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	if err := a.writeComposeManifest(req.ID, runtimeConfig, p.Sources); err != nil {
		return p, err
	}
	if err := ensureDockerNetwork(req.ID); err != nil {
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	if err := a.startBackingServices(req, runtimeConfig, p.Sources, p.Services); err != nil {
		a.recordEvent(req.ID, "error", "backing.failed", err.Error(), "", nil)
		if cleanupErr := a.cleanupPreviewServices(req.ID, p.Services); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
		}
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	setupTimer := startOperationTimer()
	if err := a.runSetupSteps(req.ID, runtimeConfig.Setup.AfterSeeds, runtimeConfig, p.Sources, warmState, project.Path); err != nil {
		a.recordEvent(req.ID, "error", "setup.failed", err.Error(), "", setupTimer.metadata(nil))
		if cleanupErr := a.cleanupPreviewServices(req.ID, p.Services); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
		}
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	if err := a.finalizeSmartWarmBaseline(req.ID, warmState); err != nil {
		a.recordEvent(req.ID, "error", "warm.finalize_failed", err.Error(), "", nil)
		if cleanupErr := a.cleanupPreviewServices(req.ID, p.Services); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
		}
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	if err := a.setPreviewStatus(req.ID, "starting_apps"); err != nil {
		if cleanupErr := a.cleanupPreviewServices(req.ID, p.Services); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
		}
		return p, err
	}
	if err := a.startAppServices(req, runtimeConfig, p.Sources, p.Services); err != nil {
		if cleanupErr := a.cleanupPreviewServices(req.ID, p.Services); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
		}
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	if req.Wait {
		if err := a.Wait(req.ID, req.Timeout); err != nil {
			_ = a.setPreviewStatus(req.ID, "unhealthy")
			p, _ = a.getPreview(req.ID)
			return p, err
		}
	}
	_ = a.setPreviewStatus(req.ID, "running")
	a.recordEvent(req.ID, "info", "preview.running", "all requested health checks passed", "", nil)
	return a.getPreview(req.ID)
}

func (a *App) resolveSource(project, projectPath, previewID, name string, src SourceConfig, overrides map[string]string) (PreviewSource, error) {
	if overrides == nil {
		overrides = map[string]string{}
	}
	if p, ok := overrides[name+".path"]; ok {
		abs, err := filepath.Abs(expandPath(p))
		if err != nil {
			return PreviewSource{}, err
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return PreviewSource{}, fmt.Errorf("external source path is not a directory: %s", abs)
		}
		return PreviewSource{Name: name, Mode: "external", Path: abs, Owned: false}, nil
	}
	if src.Path != "" {
		abs, err := resolveSourcePath(projectPath, src.Path)
		if err != nil {
			return PreviewSource{}, err
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return PreviewSource{}, fmt.Errorf("configured source path is not a directory: %s", abs)
		}
		return PreviewSource{Name: name, Mode: "external", Path: abs, Owned: false}, nil
	}
	ref := src.DefaultRef
	if v, ok := overrides[name+".ref"]; ok {
		ref = v
	}
	if ref == "" {
		ref = "main"
	}
	if src.Repo == "" {
		return PreviewSource{}, fmt.Errorf("source %s has no repo/path and no %s.path override", name, name)
	}
	repoPath := filepath.Join(a.Home, "repos", name)
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); os.IsNotExist(err) {
		if out, err := runCmd("", nil, "git", "clone", src.Repo, repoPath); err != nil {
			return PreviewSource{}, fmt.Errorf("git clone %s: %w: %s", src.Repo, err, string(out))
		}
	}
	if out, err := runCmd(repoPath, nil, "git", "fetch", "--all", "--prune"); err != nil {
		return PreviewSource{}, fmt.Errorf("git fetch: %w: %s", err, string(out))
	}
	wt := filepath.Join(a.Home, "worktrees", project, previewID, name)
	_ = os.RemoveAll(wt)
	if err := ensureDir(filepath.Dir(wt)); err != nil {
		return PreviewSource{}, err
	}
	if out, err := runCmd(repoPath, nil, "git", "worktree", "add", "--detach", wt, ref); err != nil {
		return PreviewSource{}, fmt.Errorf("git worktree add %s: %w: %s", ref, err, string(out))
	}
	sha := ref
	if out, err := runCmd(wt, nil, "git", "rev-parse", "--short", "HEAD"); err == nil {
		sha = strings.TrimSpace(string(out))
	}
	return PreviewSource{Name: name, Mode: "managed", Ref: sha, Path: wt, Owned: true}, nil
}

func (a *App) buildServiceImages(project ProjectRecord, previewID string, sources map[string]PreviewSource, cfg *ProjectConfig) error {
	if len(cfg.Services) == 0 {
		return nil
	}
	services := make(map[string]ServiceConfig, len(cfg.Services))
	for name, svc := range cfg.Services {
		services[name] = svc
	}
	for _, name := range sortedMapKeys(services) {
		svc := services[name]
		if !imageBuildConfigured(svc.Build) {
			continue
		}
		basePath := project.Path
		if svc.Source != "" {
			src, ok := sources[svc.Source]
			if !ok {
				return fmt.Errorf("service %s references unknown source %s", name, svc.Source)
			}
			basePath = src.Path
		}
		spec, err := dockerBuildSpecForService(basePath, cfg.Project.Name, previewID, name, svc.Build)
		if err != nil {
			return err
		}
		timer := startOperationTimer()
		a.recordEvent(previewID, "info", "image.building", "building service image", name, map[string]string{"tag": spec.Tag, "context": spec.Context, "dockerfile": spec.Dockerfile})
		if err := buildDockerImage(spec); err != nil {
			a.recordEvent(previewID, "error", "image.build_failed", err.Error(), name, timer.metadata(map[string]string{"tag": spec.Tag, "context": spec.Context, "dockerfile": spec.Dockerfile}))
			return fmt.Errorf("build image for service %s: %w", name, err)
		}
		svc.Image = spec.Tag
		services[name] = svc
		a.recordEvent(previewID, "info", "image.built", "service image built", name, timer.metadata(map[string]string{"tag": spec.Tag, "context": spec.Context, "dockerfile": spec.Dockerfile}))
	}
	cfg.Services = services
	return nil
}

func dockerBuildSpecForService(projectPath, projectName, previewID, service string, build ImageBuildConfig) (dockerBuildSpec, error) {
	contextPath := strings.TrimSpace(build.Context)
	if contextPath == "" {
		contextPath = "."
	}
	resolvedContext, err := resolveProjectPath(projectPath, contextPath)
	if err != nil {
		return dockerBuildSpec{}, fmt.Errorf("resolve build context for service %s: %w", service, err)
	}
	dockerfile := strings.TrimSpace(build.Dockerfile)
	if dockerfile != "" {
		resolvedDockerfile, err := resolveProjectPath(resolvedContext, dockerfile)
		if err != nil {
			return dockerBuildSpec{}, fmt.Errorf("resolve build dockerfile for service %s: %w", service, err)
		}
		dockerfile = resolvedDockerfile
	}
	tag := strings.TrimSpace(build.Tag)
	if tag == "" {
		tag = defaultServiceImageTag(projectName, previewID, service)
	}
	return dockerBuildSpec{Tag: tag, Context: resolvedContext, Dockerfile: dockerfile, Args: build.Args}, nil
}

func resolveSourcePath(projectPath, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("source path is required")
	}
	expanded := expandPath(value)
	if filepath.IsAbs(expanded) {
		return filepath.Abs(expanded)
	}
	return resolveProjectPath(projectPath, expanded)
}

func resolveProjectPath(projectPath, value string) (string, error) {
	root, err := filepath.Abs(expandPath(projectPath))
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return root, nil
	}
	expanded := expandPath(value)
	if filepath.IsAbs(expanded) {
		return "", fmt.Errorf("path %q must be relative to %s", value, root)
	}
	resolved, err := filepath.Abs(filepath.Join(root, expanded))
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(root, resolved) {
		return "", fmt.Errorf("path %q escapes %s", value, root)
	}
	if realResolved, err := filepath.EvalSymlinks(resolved); err == nil {
		realRoot := root
		if evaluatedRoot, rootErr := filepath.EvalSymlinks(root); rootErr == nil {
			realRoot = evaluatedRoot
		}
		if !pathWithinRoot(realRoot, realResolved) {
			return "", fmt.Errorf("path %q resolves outside %s", value, root)
		}
	}
	return resolved, nil
}

func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func defaultServiceImageTag(projectName, previewID, service string) string {
	name := sanitizeDockerName(projectName + "-" + service)
	return "vivero/" + name + ":" + shortStableID(previewID+":"+service)
}

func serviceConfigForBacking(backing BackingConfig) ServiceConfig {
	return ServiceConfig{
		Runtime:           "docker",
		Image:             backing.Image,
		Command:           backing.Command,
		Env:               backing.Env,
		Health:            backing.Health,
		DependencyVolumes: backing.DependencyVolumes,
		ResourceLimits:    backing.ResourceLimits,
	}
}

func (a *App) writeComposeManifest(previewID string, cfg ProjectConfig, sources map[string]PreviewSource) error {
	var b strings.Builder
	b.WriteString("# Generated by Vivero for inspection/debugging. Vivero launches app services through the Docker-compatible runtime adapter.\n")
	b.WriteString("services:\n")
	for _, name := range sortedMapKeys(cfg.Services) {
		svc := cfg.Services[name]
		b.WriteString("  " + name + ":\n")
		if svc.Image != "" {
			b.WriteString("    image: " + svc.Image + "\n")
		} else {
			b.WriteString("    image: scratch\n")
		}
		if svc.Command != "" {
			b.WriteString("    command: " + quoteYAML(svc.Command) + "\n")
		}
		ports, err := servicePortPlan(svc)
		if err != nil {
			return err
		}
		if len(ports) > 0 {
			b.WriteString("    ports:\n")
			for _, port := range ports {
				if port.Host > 0 {
					b.WriteString(fmt.Sprintf("      - \"127.0.0.1:%d:%d\"\n", port.Host, port.Container))
				} else {
					b.WriteString(fmt.Sprintf("      - \"127.0.0.1::%d\"\n", port.Container))
				}
			}
		}
		if svc.Source != "" {
			if src, ok := sources[svc.Source]; ok {
				b.WriteString("    volumes:\n      - " + src.Path + ":/app\n")
			}
		}
	}
	path := filepath.Join(a.Home, "run", previewID, "compose.yml")
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func quoteYAML(s string) string { return fmt.Sprintf("%q", s) }

func (a *App) runSetupSteps(previewID string, steps []SetupStep, cfg ProjectConfig, sources map[string]PreviewSource, warm warmRunState, projectPath ...string) error {
	root := ""
	if len(projectPath) > 0 {
		root = projectPath[0]
	}
	for i, step := range steps {
		if strings.TrimSpace(step.Command) == "" {
			continue
		}
		policy, err := normalizeSetupPolicy(step.Policy)
		if err != nil {
			return fmt.Errorf("setup.afterSeeds[%d]: %w", i, err)
		}
		timer := startOperationTimer()
		metadata := map[string]string{"command": step.Command, "index": fmt.Sprint(i), "policy": policy}
		if step.Service != "" {
			metadata["service"] = step.Service
		}
		markerPath := ""
		skipMessage := "setup command skipped by " + policy + " policy"
		if policy == "once-per-project" {
			markerPath = a.setupStepMarkerPath(cfg.Project.Name, i, step)
			if warm.Active {
				metadata["fingerprint"] = warm.Fingerprint
				canonicalMarker := a.setupStepWarmMarkerPath(cfg.Project.Name, warm.Fingerprint, i, step)
				if warm.Mode == warmModeBaseline {
					markerPath = canonicalMarker
				} else {
					if warm.BaselineReady {
						if _, err := os.Stat(canonicalMarker); err == nil {
							metadata["marker"] = canonicalMarker
							metadata["reason"] = "warm-baseline-match"
							a.recordEvent(previewID, "info", "setup.afterSeeds.skipped", "setup command skipped because derived warm volume matches baseline", step.Service, timer.metadata(metadata))
							continue
						} else if !os.IsNotExist(err) {
							return fmt.Errorf("setup.afterSeeds[%d] marker check failed: %w", i, err)
						}
					}
					markerPath = a.setupStepPreviewWarmMarkerPath(previewID, warm.Fingerprint, i, step)
				}
			}
		}
		if policy == "once-per-fingerprint" {
			svc, ok := cfg.Services[step.Service]
			if !ok {
				return fmt.Errorf("setup.afterSeeds[%d] references unknown service %s", i, step.Service)
			}
			if !serviceHasPersistentDependencyVolume(svc) {
				return fmt.Errorf("setup.afterSeeds[%d] once-per-fingerprint requires service %s to declare a persistent dependency volume with lifetime project or smart", i, step.Service)
			}
			paths, err := setupStepFingerprintPaths(step, cfg)
			if err != nil {
				return fmt.Errorf("setup.afterSeeds[%d] once-per-fingerprint: %w", i, err)
			}
			fingerprintRoot, err := setupStepFingerprintRoot(i, step, cfg, sources, root)
			if err != nil {
				return err
			}
			fingerprint, err := fingerprintForPaths(fingerprintRoot, paths)
			if err != nil {
				return fmt.Errorf("setup.afterSeeds[%d] fingerprint: %w", i, err)
			}
			metadata["fingerprint"] = fingerprint
			metadata["fingerprintPaths"] = strings.Join(paths, ",")
			metadata["fingerprintRoot"] = fingerprintRoot
			markerPath = a.setupStepFingerprintMarkerPath(cfg.Project.Name, fingerprint, paths, i, step)
		}
		if markerPath != "" {
			if _, err := os.Stat(markerPath); err == nil {
				metadata["marker"] = markerPath
				metadata["reason"] = "marker-exists"
				a.recordEvent(previewID, "info", "setup.afterSeeds.skipped", skipMessage, step.Service, timer.metadata(metadata))
				continue
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("setup.afterSeeds[%d] marker check failed: %w", i, err)
			}
		}
		workdir, env, err := a.setupStepContext(step, cfg, sources)
		if err != nil {
			return err
		}
		_ = workdir
		var out []byte
		if step.Service != "" {
			svc := cfg.Services[step.Service]
			out, err = a.runDockerOneShot(cfg.Project.Name, previewID, step.Service, svc, sources, env, step.Command)
		} else {
			return fmt.Errorf("setup.afterSeeds[%d] must target a containerized service", i)
		}
		if err != nil {
			a.recordEvent(previewID, "error", "setup.afterSeeds.failed", err.Error(), step.Service, timer.metadata(metadata))
			return fmt.Errorf("setup.afterSeeds[%d] failed: %w: %s", i, err, string(out))
		}
		if markerPath != "" {
			if err := ensureDir(filepath.Dir(markerPath)); err != nil {
				return err
			}
			if err := os.WriteFile(markerPath, []byte(nowUTC().Format(time.RFC3339Nano)+"\n"), 0o644); err != nil {
				return err
			}
			metadata["marker"] = markerPath
		}
		a.recordEvent(previewID, "info", "setup.afterSeeds", "setup command completed", step.Service, timer.metadata(metadata))
	}
	return nil
}

func normalizeSetupPolicy(policy string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", "per-preview", "preview", "always":
		return "per-preview", nil
	case "once-per-project", "once_per_project", "project", "once":
		return "once-per-project", nil
	case "once-per-fingerprint", "once_per_fingerprint", "fingerprint":
		return "once-per-fingerprint", nil
	default:
		return "", fmt.Errorf("unsupported setup policy %q; use per-preview, once-per-project, or once-per-fingerprint", policy)
	}
}

func setupStepFingerprintPaths(step SetupStep, cfg ProjectConfig) ([]string, error) {
	paths := step.Fingerprint.Paths
	if len(paths) == 0 {
		paths = cfg.Warm.Fingerprint.Paths
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("fingerprint paths are required; set setup.afterSeeds[].fingerprint.paths or warm.fingerprint.paths")
	}
	return normalizedFingerprintPaths(paths)
}

func setupStepFingerprintRoot(index int, step SetupStep, cfg ProjectConfig, sources map[string]PreviewSource, projectPath string) (string, error) {
	svc, ok := cfg.Services[step.Service]
	if !ok {
		return "", fmt.Errorf("setup.afterSeeds[%d] references unknown service %s", index, step.Service)
	}
	if strings.TrimSpace(svc.Source) != "" {
		src, ok := sources[svc.Source]
		if !ok {
			return "", fmt.Errorf("service %s references unknown source %s", step.Service, svc.Source)
		}
		if strings.TrimSpace(src.Path) == "" {
			return "", fmt.Errorf("source %s has no path for setup.afterSeeds[%d] fingerprint", svc.Source, index)
		}
		return src.Path, nil
	}
	if strings.TrimSpace(projectPath) != "" {
		return projectPath, nil
	}
	if len(sources) == 1 {
		for _, src := range sources {
			if strings.TrimSpace(src.Path) != "" {
				return src.Path, nil
			}
		}
	}
	return "", fmt.Errorf("setup.afterSeeds[%d] once-per-fingerprint requires a service source or project path for fingerprint paths", index)
}

func serviceHasPersistentDependencyVolume(svc ServiceConfig) bool {
	for _, vol := range svc.DependencyVolumes {
		lifetime, err := normalizeVolumeLifetime(vol.Lifetime)
		if err == nil && (lifetime == "project" || lifetime == "smart") {
			return true
		}
	}
	return false
}

func (a *App) setupStepMarkerPath(projectName string, index int, step SetupStep) string {
	projectName = sanitizeDockerName(projectName)
	if projectName == "" {
		projectName = "project"
	}
	id := shortStableID(fmt.Sprintf("%s\x00%d\x00%s\x00%s", projectName, index, step.Service, step.Command))
	return filepath.Join(a.Home, "setup", projectName, id+".done")
}

func (a *App) setupStepFingerprintMarkerPath(projectName, fingerprint string, paths []string, index int, step SetupStep) string {
	projectName = sanitizeDockerName(projectName)
	if projectName == "" {
		projectName = "project"
	}
	fingerprint = sanitizeDockerName(fingerprint)
	if fingerprint == "" {
		fingerprint = "unknown"
	}
	id := shortStableID(fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s\x00%s", projectName, fingerprint, index, step.Service, step.Command, strings.Join(paths, "\x00")))
	return filepath.Join(a.Home, "setup", projectName, "fingerprints", fingerprint, id+".done")
}

func (a *App) setupStepWarmMarkerPath(projectName, fingerprint string, index int, step SetupStep) string {
	dir, projectName, fingerprint := a.warmMarkerDir(projectName, fingerprint)
	id := shortStableID(fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s", projectName, fingerprint, index, step.Service, step.Command))
	return filepath.Join(dir, id+".done")
}

func (a *App) warmMarkerDir(projectName, fingerprint string) (string, string, string) {
	projectName = sanitizeDockerName(projectName)
	if projectName == "" {
		projectName = "project"
	}
	fingerprint = sanitizeDockerName(fingerprint)
	if fingerprint == "" {
		fingerprint = "unknown"
	}
	return filepath.Join(a.Home, "setup", projectName, "warm", fingerprint), projectName, fingerprint
}

func (a *App) clearWarmSetupMarkers(projectName, fingerprint string) error {
	dir, _, _ := a.warmMarkerDir(projectName, fingerprint)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear warm setup markers: %w", err)
	}
	return nil
}

func (a *App) setupStepPreviewWarmMarkerPath(previewID, fingerprint string, index int, step SetupStep) string {
	dir, previewID, fingerprint := a.previewWarmMarkerDir(previewID, fingerprint)
	id := shortStableID(fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s", previewID, fingerprint, index, step.Service, step.Command))
	return filepath.Join(dir, id+".done")
}

func (a *App) previewWarmMarkerDir(previewID, fingerprint string) (string, string, string) {
	previewID = sanitizeDockerName(previewID)
	if previewID == "" {
		previewID = "preview"
	}
	fingerprint = sanitizeDockerName(fingerprint)
	if fingerprint == "" {
		fingerprint = "unknown"
	}
	return filepath.Join(a.Home, "setup", "previews", previewID, "warm", fingerprint), previewID, fingerprint
}

func (a *App) clearPreviewWarmSetupMarkers(previewID, fingerprint string) error {
	dir, _, _ := a.previewWarmMarkerDir(previewID, fingerprint)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear preview warm setup markers: %w", err)
	}
	return nil
}

func (a *App) setupStepContext(step SetupStep, cfg ProjectConfig, sources map[string]PreviewSource) (string, map[string]string, error) {
	if step.Service == "" {
		return "", map[string]string{}, nil
	}
	svc, ok := cfg.Services[step.Service]
	if !ok {
		return "", nil, fmt.Errorf("setup step references unknown service %s", step.Service)
	}
	workdir := ""
	if svc.Source != "" {
		src, ok := sources[svc.Source]
		if !ok {
			return "", nil, fmt.Errorf("service %s references unknown source %s", step.Service, svc.Source)
		}
		workdir = src.Path
		if svc.WorkingDir != "" {
			workdir = filepath.Join(workdir, svc.WorkingDir)
		}
	}
	env := a.envForService(cfg.Project.Name, svc, true)
	return workdir, env, nil
}

func (a *App) envForService(projectName string, svc ServiceConfig, includeSecrets bool) map[string]string {
	env := map[string]string{}
	if includeSecrets {
		secrets, _ := readEnvFile(a.secretFile(projectName))
		for k, v := range secrets {
			env[k] = v
		}
	}
	for k, v := range svc.Env {
		env[k] = v
	}
	return env
}

type headerRewriteProxyStarter func(previewID, service, originURL, hostHeader string, publicRewrite PublicRewriteConfig, h HealthConfig) (string, int, error)

func serviceIsPublic(svc ServiceConfig, forcePublic bool) bool {
	return forcePublic || svc.Public
}

func exposeServiceThroughHeaderRewriteProxy(previewID, name string, ps PreviewService, svc ServiceConfig, forcePublic bool, start headerRewriteProxyStarter) (PreviewService, string, error) {
	tunnelOriginURL := ps.OriginURL
	if tunnelOriginURL == "" {
		tunnelOriginURL = ps.URL
	}
	hostHeader := strings.TrimSpace(svc.TunnelHostHeader)
	if hostHeader == "" || tunnelOriginURL == "" {
		return ps, tunnelOriginURL, nil
	}
	proxyURL, proxyPID, err := start(previewID, name, tunnelOriginURL, hostHeader, svc.PublicRewrite, svc.Health)
	if err != nil {
		return ps, tunnelOriginURL, err
	}
	if proxyURL == "" {
		return ps, tunnelOriginURL, fmt.Errorf("header rewrite proxy returned an empty URL")
	}
	ps.ProxyPID = proxyPID
	ps.ProxyURL = proxyURL
	tunnelOriginURL = proxyURL
	if !serviceIsPublic(svc, forcePublic) {
		ps.URL = proxyURL
	}
	return ps, tunnelOriginURL, nil
}

func (a *App) startService(req UpRequest, name string, svc ServiceConfig, sources map[string]PreviewSource, cfg ProjectConfig, forcePublic bool, includeSecrets bool) (PreviewService, error) {
	previewID := req.ID
	serviceTimer := startOperationTimer()
	runtime := serviceRuntime(svc)
	ports, err := servicePortPlan(svc)
	if err != nil {
		return PreviewService{Name: name, Source: svc.Source, Runtime: runtime, Status: "starting", Command: svc.Command, StartedAt: nowUTC()}, err
	}
	ps := PreviewService{Name: name, Source: svc.Source, Runtime: runtime, Status: "starting", Port: svc.Port, Command: svc.Command, StartedAt: nowUTC()}
	originURL := ""
	originHost := originHostForService(svc)
	if len(ports) > 0 && (forcePublic || svc.Public) && !isLoopbackHost(originHost) {
		return ps, fmt.Errorf("public service %s originHost must be loopback", name)
	}
	workdir := ""
	if svc.Source != "" {
		src, ok := sources[svc.Source]
		if !ok {
			return ps, fmt.Errorf("service %s references unknown source %s", name, svc.Source)
		}
		workdir = src.Path
		if svc.WorkingDir != "" {
			workdir = filepath.Join(workdir, svc.WorkingDir)
		}
	}
	env := a.envForService(cfg.Project.Name, svc, includeSecrets)
	logPath := filepath.Join(a.Home, "logs", previewID, name+".log")
	ps.LogPath = logPath
	if err := ensureDir(filepath.Dir(logPath)); err != nil {
		return ps, err
	}
	if runtime != "docker" {
		return ps, fmt.Errorf("service %s has unsupported runtime %q; Vivero runs app services in containers only", name, runtime)
	}
	containerID, err := a.startDockerService(cfg.Project.Name, previewID, name, svc, sources, env)
	if err != nil {
		return ps, err
	}
	ps.ContainerID = containerID
	cleanupStarted := func() error {
		var cleanupErr error
		ps, cleanupErr = a.stopPreviewServiceResources(previewID, name, ps)
		return cleanupErr
	}
	if len(ports) > 0 {
		published, err := dockerPublishedPorts(containerID, ports)
		if err != nil {
			if cleanupErr := cleanupStarted(); cleanupErr != nil {
				err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
			}
			return ps, err
		}
		portMap, err := previewPortsFromPublished(ports, published, originHost)
		if err != nil {
			if cleanupErr := cleanupStarted(); cleanupErr != nil {
				err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
			}
			return ps, err
		}
		ps.Ports = portMap
		if primary, ok := primaryPreviewPort(portMap); ok {
			ps.Port = primary.Host
			originURL = primary.URL
			ps.OriginURL = originURL
			ps.URL = originURL
		}
	}
	_ = os.WriteFile(logPath, []byte("container "+containerID+" started via docker\n"), 0o644)
	a.recordEvent(previewID, "info", "service.started", "container started", name, serviceTimer.metadata(map[string]string{"container": containerID, "image": svc.Image, "command": svc.Command}))
	if workdir != "" {
		a.recordEvent(previewID, "info", "service.workdir", "container source mounted", name, map[string]string{"hostPath": workdir})
	}
	if strings.TrimSpace(svc.Health.Command) != "" {
		timeout := serviceHealthTimeout(svc.Health, 30*time.Second)
		healthTimer := startOperationTimer()
		if err := waitDockerHealthCommand(containerID, svc.Health, timeout); err != nil {
			ps.Status = "unhealthy"
			ps.LastHealth = err.Error()
			a.recordEvent(previewID, "error", "service.health_failed", err.Error(), name, healthTimer.metadata(map[string]string{"container": containerID}))
			if cleanupErr := cleanupStarted(); cleanupErr != nil {
				err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
			}
			_ = a.saveService(previewID, ps)
			return ps, err
		}
		ps.Status = "healthy"
		ps.LastHealth = "ok"
		a.recordEvent(previewID, "info", "service.healthy", "health command passed", name, healthTimer.metadata(map[string]string{"container": containerID}))
	}
	if originURL != "" {
		timeout := serviceHealthTimeout(svc.Health, 30*time.Second)
		healthTimer := startOperationTimer()
		if err := waitHTTP(originURL, svc.Health, timeout); err != nil {
			ps.Status = "unhealthy"
			ps.LastHealth = err.Error()
			a.recordEvent(previewID, "error", "service.health_failed", err.Error(), name, healthTimer.metadata(map[string]string{"url": originURL}))
			if cleanupErr := cleanupStarted(); cleanupErr != nil {
				err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
			}
			_ = a.saveService(previewID, ps)
			return ps, err
		}
		ps.Status = "healthy"
		ps.LastHealth = "ok"
		a.recordEvent(previewID, "info", "service.healthy", "health check passed", name, healthTimer.metadata(map[string]string{"url": originURL}))
	}
	if originURL != "" {
		var tunnelOriginURL string
		var proxyErr error
		ps, tunnelOriginURL, proxyErr = exposeServiceThroughHeaderRewriteProxy(previewID, name, ps, svc, forcePublic, a.startHeaderRewriteProxy)
		if proxyErr != nil {
			if cleanupErr := cleanupStarted(); cleanupErr != nil {
				proxyErr = fmt.Errorf("%w; cleanup failed: %v", proxyErr, cleanupErr)
			}
			return ps, proxyErr
		}
		if serviceIsPublic(svc, forcePublic) {
			if isNamedPublicTunnel(cfg.Public) {
				url, err := publicURLForService(cfg.Public, req, name)
				if err != nil {
					if cleanupErr := cleanupStarted(); cleanupErr != nil {
						err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
					}
					return ps, err
				}
				ps.URL = url
				if err := a.saveService(previewID, ps); err != nil {
					if cleanupErr := cleanupStarted(); cleanupErr != nil {
						err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
					}
					return ps, err
				}
				tunnelTimer := startOperationTimer()
				if err := waitHTTP(url, svc.Health, serviceHealthTimeout(svc.Health, 3*time.Minute)); err != nil {
					ps.Status = "unhealthy"
					ps.LastHealth = err.Error()
					a.recordEvent(previewID, "error", "tunnel.failed", err.Error(), name, tunnelTimer.metadata(map[string]string{"url": url, "origin": tunnelOriginURL}))
					err = fmt.Errorf("public named tunnel health failed for %s: %w", name, err)
					if cleanupErr := cleanupStarted(); cleanupErr != nil {
						err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
					}
					_ = a.saveService(previewID, ps)
					return ps, err
				}
				a.recordEvent(previewID, "info", "tunnel.ready", "stable public URL health check passed", name, tunnelTimer.metadata(map[string]string{"url": url, "origin": tunnelOriginURL}))
				return ps, nil
			}
			tunnelTimer := startOperationTimer()
			url, pid, tlog, err := a.startQuickTunnel(previewID, name, tunnelOriginURL, "")
			if err != nil {
				a.recordEvent(previewID, "error", "tunnel.failed", err.Error(), name, tunnelTimer.metadata(map[string]string{"origin": tunnelOriginURL}))
				if cleanupErr := cleanupStarted(); cleanupErr != nil {
					err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
				}
				return ps, err
			}
			ps.TunnelPID = pid
			ps.TunnelLogPath = tlog
			ps.URL = url
			// Verify the public URL before returning it. Vivero's core guarantee is URL = works.
			if err := waitHTTP(url, svc.Health, serviceHealthTimeout(svc.Health, 3*time.Minute)); err != nil {
				ps.Status = "unhealthy"
				ps.LastHealth = err.Error()
				a.recordEvent(previewID, "error", "tunnel.failed", err.Error(), name, tunnelTimer.metadata(map[string]string{"url": url, "origin": tunnelOriginURL}))
				err = fmt.Errorf("public tunnel health failed for %s: %w", name, err)
				if cleanupErr := cleanupStarted(); cleanupErr != nil {
					err = fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
				}
				_ = a.saveService(previewID, ps)
				return ps, err
			}
			a.recordEvent(previewID, "info", "tunnel.ready", "public URL health check passed", name, tunnelTimer.metadata(map[string]string{"url": url, "origin": tunnelOriginURL}))
		}
	}
	if ps.Status == "starting" {
		ps.Status = "running"
	}
	return ps, nil
}

func serviceHealthTimeout(h HealthConfig, fallback time.Duration) time.Duration {
	d := positiveDurationOrDefault(h.Timeout, fallback)
	if d < fallback {
		return fallback
	}
	return d
}

func healthCheckInterval(h HealthConfig) time.Duration {
	return positiveDurationOrDefault(h.Interval, time.Second)
}

func waitHTTP(baseURL string, h HealthConfig, timeout time.Duration) error {
	path := h.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	url := strings.TrimRight(baseURL, "/") + path
	interval := healthCheckInterval(h)
	deadline := time.Now().Add(timeout)
	client := httpClientForURL(url)
	var last string
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			expected := h.ExpectStatus
			if expected == 0 && resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return nil
			}
			if expected != 0 && resp.StatusCode == expected {
				return nil
			}
			last = fmt.Sprintf("%s returned %d", url, resp.StatusCode)
		} else {
			last = err.Error()
		}
		time.Sleep(interval)
	}
	if last == "" {
		last = "timeout"
	}
	return fmt.Errorf("health check failed for %s: %s", url, last)
}

func waitDockerHealthCommand(containerID string, h HealthConfig, timeout time.Duration) error {
	if strings.TrimSpace(h.Command) == "" {
		return nil
	}
	interval := healthCheckInterval(h)
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		stdout, stderr, exit, err := dockerExecWithTimeout(containerID, []string{"/bin/sh", "-lc", h.Command}, remaining)
		combined := strings.TrimSpace(stderr + "\n" + stdout)
		if err == nil && exit == 0 {
			return nil
		}
		if err != nil {
			last = err.Error()
		} else {
			last = fmt.Sprintf("exit %d", exit)
		}
		if combined != "" {
			last += ": " + combined
		}
		sleep := interval
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		if sleep > 0 {
			time.Sleep(sleep)
		}
	}
	if last == "" {
		last = "timeout"
	}
	return fmt.Errorf("health command failed for %s: %s", containerID, last)
}

func httpClientForURL(raw string) *http.Client {
	defaultClient := &http.Client{Timeout: 5 * time.Second}
	parsed, err := url.Parse(raw)
	if err != nil {
		return defaultClient
	}
	host := parsed.Hostname()
	if host == "" || host == "localhost" || net.ParseIP(host) != nil {
		return defaultClient
	}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", "1.1.1.1:53")
		},
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, Resolver: resolver}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DialContext:       dialer.DialContext,
			ForceAttemptHTTP2: true,
		},
	}
}

func quickTunnelArgs(originURL, hostHeader string) []string {
	args := []string{"tunnel", "--url", originURL, "--no-autoupdate"}
	if hostHeader != "" {
		args = append(args, "--http-host-header", hostHeader)
	}
	return args
}

func (a *App) startQuickTunnel(previewID, service, originURL, hostHeader string) (string, int, string, error) {
	if _, err := exec.LookPath("cloudflared"); err != nil {
		return "", 0, "", fmt.Errorf("cloudflared not found: %w", err)
	}
	logPath := filepath.Join(a.Home, "logs", previewID, service+".cloudflared.log")
	if err := ensureDir(filepath.Dir(logPath)); err != nil {
		return "", 0, "", err
	}
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", 0, "", err
	}
	startOffset, err := lf.Seek(0, io.SeekEnd)
	if err != nil {
		lf.Close()
		return "", 0, "", err
	}
	cmd := exec.Command("cloudflared", quickTunnelArgs(originURL, hostHeader)...)
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		lf.Close()
		return "", 0, "", err
	}
	pid := cmd.Process.Pid
	_ = lf.Close()
	url, err := waitForQuickTunnelURL(logPath, startOffset, 45*time.Second)
	if err != nil {
		_ = killProcessGroup(pid)
		return "", pid, logPath, err
	}
	a.recordEvent(previewID, "info", "tunnel.started", "cloudflared quick tunnel started", service, map[string]string{"pid": fmt.Sprint(pid), "url": url})
	return url, pid, logPath, nil
}

func waitForQuickTunnelURL(logPath string, offset int64, timeout time.Duration) (string, error) {
	re := regexp.MustCompile(`https://[-a-zA-Z0-9.]+trycloudflare\.com`)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(logPath)
		if err == nil {
			if offset < 0 || offset > int64(len(body)) {
				offset = 0
			}
			if m := re.Find(body[offset:]); m != nil {
				return string(m), nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return "", fmt.Errorf("timed out waiting for cloudflared URL; see %s", logPath)
}

func (a *App) Wait(id string, timeout time.Duration) error {
	p, err := a.getPreview(id)
	if err != nil {
		return err
	}
	project, err := a.getProject(p.Project)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for name, svcState := range p.Services {
		svcCfg, ok := project.Config.Services[name]
		if !ok || svcState.OriginURL == "" {
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timeout waiting for %s", name)
		}
		if err := waitHTTP(svcState.OriginURL, svcCfg.Health, remaining); err != nil {
			return err
		}
		svcState.Status = "healthy"
		svcState.LastHealth = "ok"
		_ = a.saveService(id, svcState)
	}
	return nil
}

func (a *App) cleanupExistingPreviewForUp(previewID string) (PreviewRecord, bool, error) {
	existing, err := a.getPreview(previewID)
	if err != nil {
		if strings.Contains(err.Error(), "preview not found") {
			return PreviewRecord{}, false, nil
		}
		return existing, false, err
	}
	if err := a.cleanupPreviewServices(previewID, existing.Services); err != nil {
		return existing, true, fmt.Errorf("cleanup existing preview %s: %w", previewID, err)
	}
	if err := a.deletePreviewServices(previewID); err != nil {
		return existing, true, err
	}
	if err := a.deletePreviewSources(previewID); err != nil {
		return existing, true, err
	}
	return existing, true, nil
}

func (a *App) deletePreviewServices(previewID string) error {
	_, err := a.db.Exec(`DELETE FROM preview_services WHERE preview_id=?`, previewID)
	return err
}

func (a *App) deletePreviewSources(previewID string) error {
	_, err := a.db.Exec(`DELETE FROM preview_sources WHERE preview_id=?`, previewID)
	return err
}

func (a *App) cleanupPreviewServices(previewID string, services map[string]PreviewService) error {
	if len(services) == 0 {
		var errs []string
		if err := removeDockerContainersForPreview(previewID); err != nil {
			errs = append(errs, err.Error())
		}
		if err := removeDockerNetwork(previewID); err != nil {
			errs = append(errs, err.Error())
		}
		if len(errs) > 0 {
			return fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		return nil
	}
	var errs []string
	for _, name := range sortedMapKeys(services) {
		svc, err := a.stopPreviewServiceResources(previewID, name, services[name])
		if err != nil {
			errs = append(errs, err.Error())
		}
		if serviceResourcesStopped(svc) {
			svc.Status = "dead"
		}
		services[name] = svc
		if saveErr := a.saveService(previewID, svc); saveErr != nil {
			errs = append(errs, fmt.Sprintf("save service %s: %v", name, saveErr))
		}
	}
	if err := removeDockerContainersForPreview(previewID); err != nil {
		errs = append(errs, err.Error())
	}
	if networkErr := removeDockerNetwork(previewID); networkErr != nil {
		errs = append(errs, networkErr.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (a *App) stopPreviewServiceResources(previewID, name string, svc PreviewService) (PreviewService, error) {
	var errs []string
	if svc.TunnelPID > 0 {
		pid := svc.TunnelPID
		if err := killProcessGroup(pid); err != nil {
			errs = append(errs, fmt.Sprintf("stop tunnel pid %d: %v", pid, err))
		} else {
			svc.TunnelPID = 0
			a.recordEvent(previewID, "info", "tunnel.stopped", "tunnel process stopped", name, map[string]string{"pid": fmt.Sprint(pid)})
		}
	}
	if svc.ProxyPID > 0 {
		pid := svc.ProxyPID
		if err := killProcessGroup(pid); err != nil {
			errs = append(errs, fmt.Sprintf("stop proxy pid %d: %v", pid, err))
		} else {
			svc.ProxyPID = 0
			a.recordEvent(previewID, "info", "proxy.stopped", "header rewrite proxy stopped", name, map[string]string{"pid": fmt.Sprint(pid)})
		}
	}
	if svc.PID > 0 {
		pid := svc.PID
		if err := killProcessGroup(pid); err != nil {
			errs = append(errs, fmt.Sprintf("stop service pid %d: %v", pid, err))
		} else {
			svc.PID = 0
			a.recordEvent(previewID, "info", "service.stopped", "service process stopped", name, map[string]string{"pid": fmt.Sprint(pid)})
		}
	}
	if svc.ContainerID != "" {
		containerID := svc.ContainerID
		out, err := runCmd("", nil, "docker", "rm", "-f", containerID)
		if err != nil && !isDockerNoSuchContainer(string(out)) {
			errs = append(errs, fmt.Sprintf("docker rm -f %s: %v: %s", containerID, err, strings.TrimSpace(string(out))))
		} else {
			svc.ContainerID = ""
			a.recordEvent(previewID, "info", "service.stopped", "container stopped", name, map[string]string{"container": containerID})
		}
	}
	if len(errs) > 0 {
		return svc, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return svc, nil
}

func isDockerNoSuchContainer(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "no such container") || strings.Contains(lower, "no such object")
}

func serviceResourcesStopped(svc PreviewService) bool {
	return svc.ContainerID == "" && svc.PID == 0 && svc.ProxyPID == 0 && svc.TunnelPID == 0
}

func removePreviewDependencyVolumes(previewID string, cfg ProjectConfig) error {
	volumeNames := map[string]struct{}{}
	collect := func(service string, volumes []VolumeConfig) error {
		for _, vol := range volumes {
			if strings.TrimSpace(vol.Name) == "" || strings.TrimSpace(vol.Target) == "" {
				continue
			}
			lifetime, err := normalizeVolumeLifetime(vol.Lifetime)
			if err != nil {
				return err
			}
			if lifetime == "project" {
				continue
			}
			volumeName := dockerVolumeName(previewID, service, vol.Name)
			if lifetime == "smart" {
				volumeName = dockerSmartPreviewVolumeName(cfg.Project.Name, previewID, service, vol.Name)
			}
			volumeNames[volumeName] = struct{}{}
		}
		return nil
	}
	for service, svc := range cfg.BackingServices {
		if err := collect(service, svc.DependencyVolumes); err != nil {
			return err
		}
	}
	for service, svc := range cfg.Services {
		if err := collect(service, svc.DependencyVolumes); err != nil {
			return err
		}
	}
	if len(volumeNames) == 0 {
		return nil
	}
	var errs []string
	for _, name := range sortedMapKeys(volumeNames) {
		if err := removeDockerVolume(name); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (a *App) Down(id, mode string) (PreviewRecord, error) {
	p, err := a.getPreview(id)
	if err != nil {
		return p, err
	}
	if mode == "" {
		mode = "safe"
	}
	var cleanupErrs []string
	var safeDirtyErr error
	for name, svc := range p.Services {
		stopped, stopErr := a.stopPreviewServiceResources(id, name, svc)
		if stopErr != nil {
			cleanupErrs = append(cleanupErrs, stopErr.Error())
		}
		if serviceResourcesStopped(stopped) {
			stopped.Status = "dead"
		}
		p.Services[name] = stopped
		if saveErr := a.saveService(id, stopped); saveErr != nil {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("save service %s: %v", name, saveErr))
		}
	}
	if err := removeDockerContainersForPreview(id); err != nil {
		cleanupErrs = append(cleanupErrs, err.Error())
	}
	for _, src := range p.Sources {
		if safeDirtyErr != nil {
			break
		}
		if !src.Owned {
			continue
		}
		dirty, patch, err := gitDirtyPatch(src.Path)
		if err == nil && dirty && mode == "safe" {
			safeDirtyErr = fmt.Errorf("managed source %s is dirty; use --archive-patch, --keep-worktree, or --discard", src.Name)
			break
		}
		if dirty && mode == "archive-patch" {
			patchPath := filepath.Join(a.Home, "patches", id+"-"+src.Name+".patch")
			_ = os.WriteFile(patchPath, patch, 0o644)
			a.recordEvent(id, "info", "source.patch_archived", "dirty worktree patch archived", src.Name, map[string]string{"path": patchPath})
		}
		if mode != "keep-worktree" {
			_ = runGitWorktreeRemove(src.Path)
		}
	}
	if mode == "discard" && safeDirtyErr == nil {
		project, projectErr := a.getProject(p.Project)
		if projectErr != nil {
			cleanupErrs = append(cleanupErrs, fmt.Sprintf("load project %s for volume cleanup: %v", p.Project, projectErr))
		} else if volumeErr := removePreviewDependencyVolumes(id, project.Config); volumeErr != nil {
			cleanupErrs = append(cleanupErrs, volumeErr.Error())
		}
	}
	if networkErr := removeDockerNetwork(id); networkErr != nil {
		cleanupErrs = append(cleanupErrs, networkErr.Error())
	}
	if safeDirtyErr != nil {
		_ = a.setPreviewStatus(id, "unhealthy")
		updated, _ := a.getPreview(id)
		if len(cleanupErrs) > 0 {
			return updated, fmt.Errorf("%w; cleanup failed: %s", safeDirtyErr, strings.Join(cleanupErrs, "; "))
		}
		return updated, safeDirtyErr
	}
	if len(cleanupErrs) > 0 {
		_ = a.setPreviewStatus(id, "unhealthy")
		updated, _ := a.getPreview(id)
		return updated, fmt.Errorf("%s", strings.Join(cleanupErrs, "; "))
	}
	_ = a.setPreviewStatus(id, "dead")
	a.recordEvent(id, "info", "preview.dead", "preview torn down", "", map[string]string{"mode": mode})
	return a.getPreview(id)
}

func killProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	for i := 0; i < 20; i++ {
		if err := syscall.Kill(pid, syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	return nil
}

func gitDirtyPatch(path string) (bool, []byte, error) {
	out, err := runCmd(path, nil, "git", "status", "--porcelain")
	if err != nil {
		return false, nil, err
	}
	dirty := strings.TrimSpace(string(out)) != ""
	if !dirty {
		return false, nil, nil
	}
	patch, _ := runCmd(path, nil, "git", "diff")
	return true, patch, nil
}

func runGitWorktreeRemove(path string) error {
	gitDir, err := runCmd(path, nil, "git", "rev-parse", "--git-common-dir")
	if err != nil {
		_ = os.RemoveAll(path)
		return nil
	}
	common := strings.TrimSpace(string(gitDir))
	if !filepath.IsAbs(common) {
		common = filepath.Join(path, common)
	}
	repo := filepath.Dir(common)
	out, err := runCmd(repo, nil, "git", "worktree", "remove", "--force", path)
	if err != nil {
		_ = os.RemoveAll(path)
		return fmt.Errorf("git worktree remove: %w: %s", err, string(out))
	}
	return nil
}

func (a *App) sourceFor(previewID, source string) (PreviewSource, error) {
	p, err := a.getPreview(previewID)
	if err != nil {
		return PreviewSource{}, err
	}
	src, ok := p.Sources[source]
	if !ok {
		return PreviewSource{}, fmt.Errorf("source not found: %s", source)
	}
	return src, nil
}

func (a *App) SyncFile(previewID, source, rel, from string) (map[string]any, error) {
	src, err := a.sourceFor(previewID, source)
	if err != nil {
		return nil, err
	}
	rel, err = cleanRelPath(rel)
	if err != nil {
		return nil, err
	}
	from = expandPath(from)
	b, err := os.ReadFile(from)
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(src.Path, rel)
	if err := ensureDir(filepath.Dir(dest)); err != nil {
		return nil, err
	}
	if err := os.WriteFile(dest, b, 0o644); err != nil {
		return nil, err
	}
	h, _ := fileHash(dest)
	a.recordEvent(previewID, "info", "source.synced", "file synced", source, map[string]string{"path": rel, "sha256": h})
	return map[string]any{"ok": true, "preview": previewID, "source": source, "path": rel, "bytes": len(b), "sha256": h}, nil
}

func (a *App) RemoveFile(previewID, source, rel string) (map[string]any, error) {
	src, err := a.sourceFor(previewID, source)
	if err != nil {
		return nil, err
	}
	rel, err = cleanRelPath(rel)
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(src.Path, rel)
	if err := os.Remove(dest); err != nil {
		return nil, err
	}
	a.recordEvent(previewID, "info", "source.removed", "file removed", source, map[string]string{"path": rel})
	return map[string]any{"ok": true, "preview": previewID, "source": source, "path": rel}, nil
}

func (a *App) Diff(previewID, source string) (map[string]any, error) {
	src, err := a.sourceFor(previewID, source)
	if err != nil {
		return nil, err
	}
	status, _ := runCmd(src.Path, nil, "git", "status", "--short")
	diff, _ := runCmd(src.Path, nil, "git", "diff")
	return map[string]any{"preview": previewID, "source": source, "path": src.Path, "status": string(status), "diff": string(diff)}, nil
}

func (a *App) Exec(previewID, service string, cmdArgs []string) (map[string]any, error) {
	p, err := a.getPreview(previewID)
	if err != nil {
		return nil, err
	}
	svc, ok := p.Services[service]
	if !ok {
		return nil, fmt.Errorf("service not found: %s", service)
	}
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("command required after --")
	}
	if svc.Runtime != "docker" {
		return nil, fmt.Errorf("service %s cannot exec command: runtime %q is not supported; Vivero runs app services in containers only", service, svc.Runtime)
	}
	stdout, stderr, exit, err := dockerExec(svc.ContainerID, cmdArgs)
	if err != nil {
		return nil, err
	}
	a.recordEvent(previewID, "info", "service.exec", "command executed in container", service, map[string]string{"command": strings.Join(cmdArgs, " "), "exit": fmt.Sprint(exit), "container": svc.ContainerID})
	return map[string]any{"preview": previewID, "service": service, "containerId": svc.ContainerID, "command": cmdArgs, "exitCode": exit, "stdout": stdout, "stderr": stderr}, nil
}

func (a *App) Logs(previewID, service string, limit int) (map[string]any, error) {
	p, err := a.getPreview(previewID)
	if err != nil {
		return nil, err
	}
	svc, ok := p.Services[service]
	if !ok {
		return nil, fmt.Errorf("service not found: %s", service)
	}
	if svc.Runtime == "docker" || svc.ContainerID != "" {
		lines, err := dockerLogs(svc.ContainerID, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"preview": previewID, "service": service, "containerId": svc.ContainerID, "logPath": svc.LogPath, "lines": lines}, nil
	}
	b, err := os.ReadFile(svc.LogPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(b), "\n")
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return map[string]any{"preview": previewID, "service": service, "logPath": svc.LogPath, "lines": lines}, nil
}

func (a *App) Smoke(previewID, name string) (map[string]any, error) {
	p, err := a.getPreview(previewID)
	if err != nil {
		return nil, err
	}
	proj, err := a.getProject(p.Project)
	if err != nil {
		return nil, err
	}
	cfg, err := projectConfigForPreview(proj, p)
	if err != nil {
		return nil, err
	}
	defaultService := defaultPreviewService(cfg.Agent, p)
	var tests []SmokeTest
	for _, t := range cfg.Agent.SmokeTests {
		if name == "" || t.Name == name {
			tests = append(tests, t)
		}
	}
	if len(tests) == 0 {
		return nil, fmt.Errorf("no smoke tests matched")
	}
	results := []map[string]any{}
	okAll := true
	for _, t := range tests {
		res := map[string]any{"name": t.Name, "ok": false}
		service := strings.TrimSpace(t.Service)
		if service == "" {
			service = defaultService
		}
		if t.Command != "" {
			parts := []string{"/bin/sh", "-lc", t.Command}
			ex, _ := a.Exec(previewID, service, parts)
			res["exec"] = ex
			if ex != nil && ex["exitCode"].(int) == 0 {
				res["ok"] = true
			}
		} else {
			svc, exists := p.Services[service]
			if !exists {
				res["error"] = "service not found"
			} else {
				h := HealthConfig{Path: t.Path, ExpectStatus: t.ExpectStatus}
				if err := waitHTTP(svc.OriginURL, h, 15*time.Second); err != nil {
					res["error"] = err.Error()
				} else {
					res["ok"] = true
				}
			}
		}
		if res["ok"] != true {
			okAll = false
		}
		results = append(results, res)
	}
	return map[string]any{"preview": previewID, "ok": okAll, "results": results}, nil
}

func (a *App) Screenshot(previewID, service, path string) (map[string]any, error) {
	return a.ScreenshotWithOptions(previewID, service, ScreenshotOptions{Path: path})
}

func (a *App) ScreenshotWithOptions(previewID, service string, opts ScreenshotOptions) (map[string]any, error) {
	opts = normalizeScreenshotOptions(opts)
	if err := validateColorScheme(opts.ColorScheme); err != nil {
		return nil, err
	}
	p, err := a.getPreview(previewID)
	if err != nil {
		return nil, err
	}
	svc, ok := p.Services[service]
	if !ok {
		return nil, fmt.Errorf("service not found: %s", service)
	}
	projectBreakpoints := []ScreenshotBreakpoint{}
	if opts.UseProjectBreakpoints {
		if rec, err := a.getProject(p.Project); err == nil {
			projectBreakpoints = rec.Config.Agent.ScreenshotBreakpoints
		}
	}
	breakpoints := screenshotBreakpoints(opts, projectBreakpoints)
	if len(breakpoints) == 0 {
		breakpoints = []ScreenshotBreakpoint{{Width: opts.Width, Height: opts.Height}}
	}
	if _, err := exec.LookPath("npx"); err != nil {
		return nil, fmt.Errorf("npx/playwright not available for screenshots: %w", err)
	}
	baseURL := serviceBaseURLForTarget(svc, opts.Target)
	if baseURL == "" {
		return nil, fmt.Errorf("service %s has no %s URL", service, opts.Target)
	}
	url := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(opts.Path, "/")
	screenshots := []map[string]any{}
	for _, bp := range breakpoints {
		if bp.Width <= 0 || bp.Height <= 0 {
			return nil, fmt.Errorf("invalid screenshot breakpoint %q: %dx%d", bp.Name, bp.Width, bp.Height)
		}
		out := screenshotOutputPath(a.Home, opts.OutputDir, previewID, service, opts.Path, bp, len(breakpoints) > 1, opts.ColorScheme)
		if err := ensureDir(filepath.Dir(out)); err != nil {
			return nil, err
		}
		args := []string{"--yes", "playwright", "screenshot", "--viewport-size", fmt.Sprintf("%d,%d", bp.Width, bp.Height)}
		args = append(args, "--channel", "chrome")
		if opts.FullPage {
			args = append(args, "--full-page")
		}
		if opts.WaitForSelector != "" {
			args = append(args, "--wait-for-selector", opts.WaitForSelector)
		}
		if opts.WaitForTimeout != "" {
			args = append(args, "--wait-for-timeout", opts.WaitForTimeout)
		}
		if opts.ColorScheme != "" {
			args = append(args, "--color-scheme", opts.ColorScheme)
		}
		args = append(args, url, out)
		cmd := exec.Command("npx", args...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("playwright screenshot failed: %w: %s", err, string(b))
		}
		width, height, err := screenshotDimensions(out)
		if err != nil {
			return nil, fmt.Errorf("read screenshot dimensions: %w", err)
		}
		cropped := false
		originalWidth, originalHeight := width, height
		if opts.Crop {
			crop, err := cropScreenshotOuterWhitespace(out)
			if err != nil {
				return nil, fmt.Errorf("crop screenshot whitespace: %w", err)
			}
			cropped = crop.Cropped
			width = crop.Width
			height = crop.Height
			originalWidth = crop.OriginalWidth
			originalHeight = crop.OriginalHeight
		}
		screenshots = append(screenshots, map[string]any{
			"preview":        previewID,
			"service":        service,
			"target":         opts.Target,
			"colorScheme":    opts.ColorScheme,
			"url":            url,
			"path":           out,
			"breakpoint":     bp.Name,
			"viewportWidth":  bp.Width,
			"viewportHeight": bp.Height,
			"cropped":        cropped,
			"width":          width,
			"height":         height,
			"originalWidth":  originalWidth,
			"originalHeight": originalHeight,
		})
	}
	result := map[string]any{"preview": previewID, "service": service, "target": opts.Target, "url": url, "screenshots": screenshots}
	if len(screenshots) == 1 {
		for k, v := range screenshots[0] {
			result[k] = v
		}
	}
	return result, nil
}
