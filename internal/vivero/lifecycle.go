package vivero

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func (a *App) cleanupExistingPreviewForUp(previewID string) (PreviewRecord, bool, error) {
	existing, err := a.getPreviewRaw(previewID)
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
	for _, name := range sortedMapKeys(existing.Sources) {
		source := existing.Sources[name]
		if !source.Owned {
			continue
		}
		if err := runGitWorktreeRemove(source.Path); err != nil {
			return existing, true, fmt.Errorf("cleanup managed source %s: %w", name, err)
		}
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
		if err := a.containerRuntime().RemoveContainersForPreview(previewID); err != nil {
			errs = append(errs, err.Error())
		}
		if err := a.containerRuntime().RemoveNetwork(previewID); err != nil {
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
	if err := a.containerRuntime().RemoveContainersForPreview(previewID); err != nil {
		errs = append(errs, err.Error())
	}
	if networkErr := a.containerRuntime().RemoveNetwork(previewID); networkErr != nil {
		errs = append(errs, networkErr.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (a *App) stopPreviewServiceResources(previewID, name string, svc PreviewService) (PreviewService, error) {
	return a.stopPreviewServiceResourcesWithOptions(previewID, name, svc, false)
}

func (a *App) stopPreviewServiceResourcesWithOptions(previewID, name string, svc PreviewService, discardVolumes bool) (PreviewService, error) {
	var errs []string
	if svc.TunnelPID > 0 {
		pid := svc.TunnelPID
		if err := killTrackedProcessGroup(pid, svc.TunnelPIDIdentity); err != nil {
			if errors.Is(err, errProcessIdentityMissing) || errors.Is(err, errProcessIdentityChanged) {
				svc.TunnelPID = 0
				svc.TunnelPIDIdentity = ""
				a.recordEvent(previewID, "warning", "tunnel.pid_unowned", err.Error(), name, map[string]string{"pid": fmt.Sprint(pid)})
			} else {
				errs = append(errs, fmt.Sprintf("stop tunnel pid %d: %v", pid, err))
			}
		} else {
			svc.TunnelPID = 0
			svc.TunnelPIDIdentity = ""
			a.recordEvent(previewID, "info", "tunnel.stopped", "tunnel process stopped", name, map[string]string{"pid": fmt.Sprint(pid)})
		}
	}
	if svc.ProxyPID > 0 {
		pid := svc.ProxyPID
		if err := killTrackedProcessGroup(pid, svc.ProxyPIDIdentity); err != nil {
			if errors.Is(err, errProcessIdentityMissing) || errors.Is(err, errProcessIdentityChanged) {
				svc.ProxyPID = 0
				svc.ProxyPIDIdentity = ""
				a.recordEvent(previewID, "warning", "proxy.pid_unowned", err.Error(), name, map[string]string{"pid": fmt.Sprint(pid)})
			} else {
				errs = append(errs, fmt.Sprintf("stop proxy pid %d: %v", pid, err))
			}
		} else {
			svc.ProxyPID = 0
			svc.ProxyPIDIdentity = ""
			a.recordEvent(previewID, "info", "proxy.stopped", "header rewrite proxy stopped", name, map[string]string{"pid": fmt.Sprint(pid)})
		}
	}
	if svc.PID > 0 {
		pid := svc.PID
		if err := killTrackedProcessGroup(pid, svc.PIDIdentity); err != nil {
			if errors.Is(err, errProcessIdentityMissing) || errors.Is(err, errProcessIdentityChanged) {
				svc.PID = 0
				svc.PIDIdentity = ""
				a.recordEvent(previewID, "warning", "service.pid_unowned", err.Error(), name, map[string]string{"pid": fmt.Sprint(pid)})
			} else {
				errs = append(errs, fmt.Sprintf("stop service pid %d: %v", pid, err))
			}
		} else {
			svc.PID = 0
			svc.PIDIdentity = ""
			a.recordEvent(previewID, "info", "service.stopped", "service process stopped", name, map[string]string{"pid": fmt.Sprint(pid)})
		}
	}
	if svc.Runtime == "compose" {
		if err := a.containerRuntime().RemoveComposeProject(previewID, name, discardVolumes); err != nil {
			errs = append(errs, fmt.Sprintf("compose cleanup %s/%s: %v", previewID, name, err))
		} else {
			a.recordEvent(previewID, "info", "service.stopped", "compose project stopped", name, map[string]string{"project": dockerComposeProjectName(previewID, name)})
		}
	}
	if svc.ContainerID != "" {
		containerID := svc.ContainerID
		missing, out, err := a.containerRuntime().RemoveContainer(containerID)
		if err != nil && !missing {
			errs = append(errs, fmt.Sprintf("docker rm -f %s: %v: %s", containerID, err, strings.TrimSpace(out)))
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

func (a *App) removePreviewDependencyVolumes(previewID string, cfg ProjectConfig) error {
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
		if err := a.containerRuntime().RemoveVolume(name); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func (a *App) Down(id, mode string) (PreviewRecord, error) {
	lock, err := a.lockPreview(id)
	if err != nil {
		return PreviewRecord{}, err
	}
	defer lock.unlock()
	p, err := a.getPreviewRaw(id)
	if err != nil {
		return p, err
	}
	if mode == "" {
		mode = "safe"
	}
	var cleanupErrs []string
	var safeDirtyErr error
	cleanedComposeServices := map[string]bool{}
	for name, svc := range p.Services {
		if svc.Runtime == "compose" {
			cleanedComposeServices[name] = true
		}
		stopped, stopErr := a.stopPreviewServiceResourcesWithOptions(id, name, svc, mode == "discard")
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
	if mode == "discard" {
		project, projectErr := a.getProject(p.Project)
		if projectErr != nil {
			if !strings.HasPrefix(projectErr.Error(), "project not found:") {
				cleanupErrs = append(cleanupErrs, fmt.Sprintf("load project %s for compose cleanup: %v", p.Project, projectErr))
			}
		} else {
			effective, _, profileErr := projectConfigForRequestedProfile(project.Config, p.Profile)
			if profileErr != nil {
				cleanupErrs = append(cleanupErrs, fmt.Sprintf("resolve project %s profile for compose cleanup: %v", p.Project, profileErr))
			} else {
				composeServices := map[string]bool{}
				for name, service := range effective.Services {
					if serviceRuntime(service) == "compose" {
						composeServices[name] = true
					}
				}
				for name, backing := range effective.BackingServices {
					if serviceRuntime(serviceConfigForBacking(backing)) == "compose" {
						composeServices[name] = true
					}
				}
				for _, name := range sortedMapKeys(composeServices) {
					if cleanedComposeServices[name] {
						continue
					}
					if err := a.containerRuntime().RemoveComposeProject(id, name, true); err != nil {
						cleanupErrs = append(cleanupErrs, fmt.Sprintf("compose cleanup %s/%s: %v", id, name, err))
					} else {
						a.recordEvent(id, "info", "service.stopped", "unpersisted compose project discarded", name, map[string]string{"project": dockerComposeProjectName(id, name)})
					}
				}
			}
		}
	}
	if err := a.containerRuntime().RemoveContainersForPreview(id); err != nil {
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
			if strings.HasPrefix(projectErr.Error(), "project not found:") {
				a.recordEvent(id, "warning", "dependency_volumes.skipped", "project record missing; skipped dependency volume cleanup", "", map[string]string{"project": p.Project})
			} else {
				cleanupErrs = append(cleanupErrs, fmt.Sprintf("load project %s for volume cleanup: %v", p.Project, projectErr))
			}
		} else if volumeErr := a.removePreviewDependencyVolumes(id, project.Config); volumeErr != nil {
			cleanupErrs = append(cleanupErrs, volumeErr.Error())
		}
	}
	if networkErr := a.containerRuntime().RemoveNetwork(id); networkErr != nil {
		cleanupErrs = append(cleanupErrs, networkErr.Error())
	}
	if safeDirtyErr != nil {
		_ = a.setPreviewStatus(id, "unhealthy")
		updated, _ := a.getPreviewRaw(id)
		if len(cleanupErrs) > 0 {
			return updated, fmt.Errorf("%w; cleanup failed: %s", safeDirtyErr, strings.Join(cleanupErrs, "; "))
		}
		return updated, safeDirtyErr
	}
	if len(cleanupErrs) > 0 {
		_ = a.setPreviewStatus(id, "unhealthy")
		updated, _ := a.getPreviewRaw(id)
		return updated, fmt.Errorf("%s", strings.Join(cleanupErrs, "; "))
	}
	_ = a.setPreviewStatus(id, "dead")
	a.recordEvent(id, "info", "preview.dead", "preview torn down", "", map[string]string{"mode": mode})
	return a.getPreviewRaw(id)
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
