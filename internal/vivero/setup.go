package vivero

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) runSetupSteps(previewID string, steps []SetupStep, cfg ProjectConfig, sources map[string]PreviewSource, warm warmRunState, projectPath ...string) error {
	return a.runSetupStepsNamed("setup.afterSeeds", previewID, steps, cfg, sources, warm, projectPath...)
}

func (a *App) runSetupStepsNamed(eventKind string, previewID string, steps []SetupStep, cfg ProjectConfig, sources map[string]PreviewSource, warm warmRunState, projectPath ...string) error {
	if eventKind == "" {
		eventKind = "setup.afterSeeds"
	}
	root := ""
	if len(projectPath) > 0 {
		root = projectPath[0]
	}
	for i, step := range steps {
		if step.Command.IsZero() {
			if strings.TrimSpace(step.Stdin) != "" {
				return fmt.Errorf("%s[%d]: stdin requires command", eventKind, i)
			}
			continue
		}
		policy, err := normalizeSetupPolicy(step.Policy)
		if err != nil {
			return fmt.Errorf("%s[%d]: %w", eventKind, i, err)
		}
		timer := startOperationTimer()
		metadata := map[string]string{"command": step.Command.Display(), "index": fmt.Sprint(i), "policy": policy}
		if step.Service != "" {
			metadata["service"] = step.Service
		}
		if name := strings.TrimSpace(step.Name); name != "" {
			metadata["name"] = name
		}
		if strings.TrimSpace(step.Stdin) != "" {
			metadata["stdin"] = "true"
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
							a.recordEvent(previewID, "info", eventKind+".skipped", "setup command skipped because derived warm volume matches baseline", step.Service, timer.metadata(metadata))
							continue
						} else if !os.IsNotExist(err) {
							return fmt.Errorf("%s[%d] marker check failed: %w", eventKind, i, err)
						}
					}
					markerPath = a.setupStepPreviewWarmMarkerPath(previewID, warm.Fingerprint, i, step)
				}
			}
		}
		if policy == "once-per-fingerprint" {
			svc, ok := cfg.Services[step.Service]
			if !ok {
				return fmt.Errorf("%s[%d] references unknown service %s", eventKind, i, step.Service)
			}
			if !serviceHasPersistentDependencyVolume(svc) {
				return fmt.Errorf("%s[%d] once-per-fingerprint requires service %s to declare a persistent dependency volume with lifetime project or smart", eventKind, i, step.Service)
			}
			paths, err := setupStepFingerprintPaths(step, cfg)
			if err != nil {
				return fmt.Errorf("%s[%d] once-per-fingerprint: %w", eventKind, i, err)
			}
			fingerprintRoot, err := setupStepFingerprintRoot(i, step, cfg, sources, root)
			if err != nil {
				return err
			}
			fingerprint, err := fingerprintForPaths(fingerprintRoot, paths)
			if err != nil {
				return fmt.Errorf("%s[%d] fingerprint: %w", eventKind, i, err)
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
				a.recordEvent(previewID, "info", eventKind+".skipped", skipMessage, step.Service, timer.metadata(metadata))
				continue
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("%s[%d] marker check failed: %w", eventKind, i, err)
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
			if serviceRuntime(svc) == "compose" {
				out, err = runDockerComposeOneShotWithStdin(a.Home, cfg.Project.Name, previewID, step.Service, svc, sources, env, step.Command, step.Stdin)
			} else {
				out, err = a.runDockerOneShotWithStdin(cfg.Project.Name, previewID, step.Service, svc, sources, env, step.Command, step.Stdin)
			}
		} else {
			return fmt.Errorf("%s[%d] must target a containerized service", eventKind, i)
		}
		if err != nil {
			a.recordEvent(previewID, "error", eventKind+".failed", err.Error(), step.Service, timer.metadata(metadata))
			return fmt.Errorf("%s[%d] failed: %w: %s", eventKind, i, err, string(out))
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
		a.recordEvent(previewID, "info", eventKind, "setup command completed", step.Service, timer.metadata(metadata))
	}
	return nil
}

func normalizeSetupPolicy(policy string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", "per-preview", "preview", "always", "every-boot", "every_boot":
		return "per-preview", nil
	case "once-per-project", "once_per_project", "project", "once":
		return "once-per-project", nil
	case "once-per-fingerprint", "once_per_fingerprint", "fingerprint":
		return "once-per-fingerprint", nil
	default:
		return "", fmt.Errorf("unsupported setup policy %q; use per-preview, once-per-project, once-per-fingerprint, or every-boot", policy)
	}
}

func setupStepFingerprintPaths(step SetupStep, cfg ProjectConfig) ([]string, error) {
	paths := step.Fingerprint.Paths
	if len(paths) == 0 {
		paths = cfg.Warm.Fingerprint.Paths
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("fingerprint paths are required; set setup step fingerprint.paths or warm.fingerprint.paths")
	}
	return normalizedFingerprintPaths(paths)
}

func setupStepFingerprintRoot(index int, step SetupStep, cfg ProjectConfig, sources map[string]PreviewSource, projectPath string) (string, error) {
	svc, ok := cfg.Services[step.Service]
	if !ok {
		return "", fmt.Errorf("setup step[%d] references unknown service %s", index, step.Service)
	}
	if strings.TrimSpace(svc.Source) != "" {
		src, ok := sources[svc.Source]
		if !ok {
			return "", fmt.Errorf("service %s references unknown source %s", step.Service, svc.Source)
		}
		if strings.TrimSpace(src.Path) == "" {
			return "", fmt.Errorf("source %s has no path for setup step[%d] fingerprint", svc.Source, index)
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
	return "", fmt.Errorf("setup step[%d] once-per-fingerprint requires a service source or project path for fingerprint paths", index)
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
	id := shortStableID(setupStepIdentity(fmt.Sprintf("%s\x00%d\x00%s\x00%s", projectName, index, step.Service, step.Command.Key()), step))
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
	base := fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s\x00%s", projectName, fingerprint, index, step.Service, step.Command.Key(), strings.Join(paths, "\x00"))
	id := shortStableID(setupStepIdentity(base, step))
	return filepath.Join(a.Home, "setup", projectName, "fingerprints", fingerprint, id+".done")
}

func (a *App) setupStepWarmMarkerPath(projectName, fingerprint string, index int, step SetupStep) string {
	dir, projectName, fingerprint := a.warmMarkerDir(projectName, fingerprint)
	id := shortStableID(setupStepIdentity(fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s", projectName, fingerprint, index, step.Service, step.Command.Key()), step))
	return filepath.Join(dir, id+".done")
}

func setupStepIdentity(base string, step SetupStep) string {
	if name := strings.TrimSpace(step.Name); name != "" {
		base += "\x00" + name
	}
	if step.Stdin != "" {
		// Hash stdin so marker paths stay short and update when the inline program changes.
		base += "\x00stdin:" + shortStableID(step.Stdin)
	}
	return base
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
	id := shortStableID(setupStepIdentity(fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s", previewID, fingerprint, index, step.Service, step.Command.Key()), step))
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
