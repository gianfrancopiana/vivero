package vivero

import (
	"fmt"
	"strings"
	"time"
)

func previewStatusNeedsReconciliation(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "unhealthy", "pending", "preparing_source", "starting_apps":
		return true
	default:
		return false
	}
}

func reconciliationServiceConfigs(project ProjectRecord, profile string) map[string]ServiceConfig {
	cfg, _, err := projectConfigForRequestedProfile(project.Config, profile)
	if err != nil {
		return nil
	}
	services := make(map[string]ServiceConfig, len(cfg.Services)+len(cfg.BackingServices))
	for name, service := range cfg.Services {
		services[name] = service
	}
	for name, service := range cfg.BackingServices {
		services[name] = serviceConfigForBacking(service)
	}
	return services
}

func sameContainerID(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	const shortestDockerID = 12
	return len(left) >= shortestDockerID && len(right) >= shortestDockerID &&
		(strings.HasPrefix(left, right) || strings.HasPrefix(right, left))
}

func composeProjectRuntimeStatus(states []runtimeContainerState, targetContainerID string) (healthy, anyRunning bool, reason string) {
	if len(states) == 0 {
		return false, false, "compose project has no containers"
	}
	targetFound := false
	failed := make([]string, 0)
	for _, container := range states {
		isTarget := sameContainerID(container.ID, targetContainerID)
		if isTarget {
			targetFound = true
		}
		if container.Running {
			anyRunning = true
		} else if isTarget || !container.ExpectedCompletion || container.ExitCode != 0 {
			failed = append(failed, fmt.Sprintf("%s (exit %d)", container.ID, container.ExitCode))
		}
	}
	if targetContainerID == "" {
		return false, anyRunning, "compose target container is not tracked"
	}
	if !targetFound {
		return false, anyRunning, "compose target container is missing"
	}
	if len(failed) > 0 {
		return false, anyRunning, "compose containers failed: " + strings.Join(failed, ", ")
	}
	return true, anyRunning, ""
}

// reconcilePreviewRuntime makes persisted preview state subordinate to the
// resources and endpoint that exist now. Callers hold the per-preview lock, so
// intermediate startup states are only reconciled after their owning Up exits.
func (a *App) reconcilePreviewRuntime(preview PreviewRecord) PreviewRecord {
	if !previewStatusNeedsReconciliation(preview.Status) {
		return preview
	}

	serviceConfigs := map[string]ServiceConfig(nil)
	if project, err := a.getProject(preview.Project); err == nil {
		serviceConfigs = reconciliationServiceConfigs(project, preview.Profile)
	}
	unhealthy := len(preview.Services) == 0 || len(serviceConfigs) == 0 || len(preview.Services) != len(serviceConfigs)
	reasons := make([]string, 0)
	if len(preview.Services) == 0 {
		reasons = append(reasons, "preview has no tracked services")
	}
	if len(serviceConfigs) == 0 {
		reasons = append(reasons, "effective service configuration is unavailable")
	} else if len(preview.Services) != len(serviceConfigs) {
		reasons = append(reasons, "tracked service set is incomplete")
	}
	for name := range serviceConfigs {
		if _, ok := preview.Services[name]; !ok {
			unhealthy = true
			reasons = append(reasons, fmt.Sprintf("%s service is not tracked", name))
		}
	}

	for _, name := range sortedMapKeys(preview.Services) {
		state := preview.Services[name]
		cfg, configured := serviceConfigs[name]
		expectedRuntime := state.Runtime
		if configured {
			expectedRuntime = serviceRuntime(cfg)
		}
		if expectedRuntime == "compose" {
			containers, err := a.containerRuntime().ComposeProjectContainers(preview.ID, name)
			if err != nil {
				state.Status = "unhealthy"
				state.LastHealth = "compose project state unavailable: " + err.Error()
				unhealthy = true
				reasons = append(reasons, fmt.Sprintf("%s compose project state unavailable", name))
				_ = a.saveService(preview.ID, state)
				preview.Services[name] = state
				continue
			}
			healthy, anyRunning, reason := composeProjectRuntimeStatus(containers, state.ContainerID)
			if !healthy {
				if anyRunning {
					state.Status = "unhealthy"
				} else {
					state.Status = "dead"
				}
				state.LastHealth = reason
				unhealthy = true
				reasons = append(reasons, fmt.Sprintf("%s %s", name, reason))
				_ = a.saveService(preview.ID, state)
				preview.Services[name] = state
				continue
			}
		} else if state.ContainerID == "" {
			if expectedRuntime == "docker" || expectedRuntime == "compose" {
				state.Status = "dead"
				state.LastHealth = "container is not tracked"
				unhealthy = true
				reasons = append(reasons, fmt.Sprintf("%s container is not tracked", name))
				_ = a.saveService(preview.ID, state)
				preview.Services[name] = state
			}
			continue
		}

		if expectedRuntime != "compose" {
			running, err := a.containerRuntime().ContainerRunning(state.ContainerID)
			if err != nil {
				state.Status = "unhealthy"
				state.LastHealth = "container state unavailable: " + err.Error()
				unhealthy = true
				reasons = append(reasons, fmt.Sprintf("%s container state unavailable", name))
				_ = a.saveService(preview.ID, state)
				preview.Services[name] = state
				continue
			}
			if !running {
				state.Status = "dead"
				state.LastHealth = "container is not running"
				unhealthy = true
				reasons = append(reasons, fmt.Sprintf("%s container is not running", name))
				_ = a.saveService(preview.ID, state)
				preview.Services[name] = state
				continue
			}
		}

		if !configured {
			unhealthy = true
			reasons = append(reasons, fmt.Sprintf("%s service is no longer configured", name))
			continue
		}
		if !cfg.Health.Command.IsZero() {
			if err := a.containerRuntime().WaitHealthCommand(state.ContainerID, cfg.Health, 5*time.Second); err != nil {
				state.Status = "unhealthy"
				state.LastHealth = err.Error()
				unhealthy = true
				reasons = append(reasons, fmt.Sprintf("%s health command failed", name))
				_ = a.saveService(preview.ID, state)
				preview.Services[name] = state
				continue
			}
		}
		if strings.TrimSpace(state.URL) == "" {
			ports, portErr := servicePortPlan(cfg)
			if portErr != nil || len(ports) > 0 {
				state.Status = "unhealthy"
				state.LastHealth = "reported URL is missing"
				unhealthy = true
				reasons = append(reasons, fmt.Sprintf("%s reported URL is missing", name))
				_ = a.saveService(preview.ID, state)
				preview.Services[name] = state
				continue
			}
			if state.Status == "dead" || state.Status == "unhealthy" {
				state.Status = "running"
				state.LastHealth = "container is running"
				_ = a.saveService(preview.ID, state)
				preview.Services[name] = state
			}
			continue
		}
		reconciled, endpointErr := a.reconcileServiceEndpoint(preview.ID, name, state, cfg)
		if endpointErr != nil {
			reconciled.Status = "unhealthy"
			reconciled.LastHealth = endpointErr.Error()
			unhealthy = true
			reasons = append(reasons, fmt.Sprintf("%s endpoint is unhealthy", name))
		} else {
			reconciled.Status = "healthy"
			reconciled.LastHealth = "ok"
		}
		_ = a.saveService(preview.ID, reconciled)
		preview.Services[name] = reconciled
	}

	previousStatus := preview.Status
	statusChanged := false
	if unhealthy {
		preview.Status = "unhealthy"
		if previousStatus != preview.Status {
			statusChanged = true
			_ = a.setPreviewStatus(preview.ID, preview.Status)
			a.recordEvent(preview.ID, "warning", "preview.reconciled", "persisted state did not match live resources", "", map[string]string{"reason": strings.Join(reasons, "; ")})
		}
	} else {
		preview.Status = "running"
		if previousStatus != preview.Status {
			statusChanged = true
			_ = a.setPreviewStatus(preview.ID, preview.Status)
			a.recordEvent(preview.ID, "info", "preview.recovered", "live resources and reported endpoints are healthy", "", nil)
		}
	}
	if statusChanged {
		if refreshed, err := a.getPreviewRaw(preview.ID); err == nil {
			return refreshed
		}
	}
	return preview
}

// previewConsumesRuntimeCapacity counts resources, not health. An unhealthy
// endpoint still owns its containers; a reconciled dead row does not.
func (a *App) previewConsumesRuntimeCapacity(preview PreviewRecord) bool {
	switch strings.ToLower(strings.TrimSpace(preview.Status)) {
	case "pending", "preparing_source", "starting_apps":
		return true
	}
	serviceConfigs := map[string]ServiceConfig(nil)
	if project, err := a.getProject(preview.Project); err == nil {
		serviceConfigs = reconciliationServiceConfigs(project, preview.Profile)
	}
	checkedComposeProjects := map[string]bool{}
	for name, cfg := range serviceConfigs {
		if serviceRuntime(cfg) != "compose" {
			continue
		}
		checkedComposeProjects[name] = true
		containers, err := a.containerRuntime().ComposeProjectContainers(preview.ID, name)
		if err != nil {
			return true
		}
		for _, container := range containers {
			if container.Running {
				return true
			}
		}
	}
	for name, service := range preview.Services {
		runtime := service.Runtime
		if cfg, ok := serviceConfigs[name]; ok {
			runtime = serviceRuntime(cfg)
		}
		if runtime == "compose" {
			if checkedComposeProjects[name] {
				continue
			}
			containers, err := a.containerRuntime().ComposeProjectContainers(preview.ID, name)
			if err != nil {
				return true
			}
			for _, container := range containers {
				if container.Running {
					return true
				}
			}
		} else if service.ContainerID != "" {
			running, err := a.containerRuntime().ContainerRunning(service.ContainerID)
			if err != nil || running {
				return true
			}
		}
		if trackedProcessRunning(service.PID, service.PIDIdentity) ||
			trackedProcessRunning(service.ProxyPID, service.ProxyPIDIdentity) ||
			trackedProcessRunning(service.TunnelPID, service.TunnelPIDIdentity) {
			return true
		}
	}
	return false
}
