package vivero

import (
	"fmt"
	"time"
)

func (a *App) Up(req UpRequest) (PreviewRecord, error) {
	if req.Project == "" {
		return PreviewRecord{}, fmt.Errorf("project is required")
	}
	if req.ID == "" {
		return PreviewRecord{}, fmt.Errorf("--id is required")
	}
	lock, err := a.lockPreview(req.ID)
	if err != nil {
		return PreviewRecord{}, err
	}
	defer lock.unlock()
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
	ensureCanonicalPreviewMetadata(&req, runtimeConfig)
	configHash, err := a.previewConfigHash(runtimeConfig)
	if err != nil {
		return PreviewRecord{}, fmt.Errorf("fingerprint preview config: %w", err)
	}
	if req.Timeout == 0 {
		req.Timeout = 5 * time.Minute
	}
	if req.Reuse {
		if existing, reused, err := a.reusablePreviewForUp(req, project, runtimeConfig); err != nil {
			return existing, err
		} else if reused {
			return existing, nil
		}
	}
	if existing, found, err := a.cleanupExistingPreviewForUp(req.ID); err != nil {
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return existing, err
	} else if found {
		a.recordEvent(req.ID, "info", "preview.replacing", "existing preview resources pruned before restart", "", nil)
	}
	capacityLock, err := a.lockRuntimeCapacity()
	if err != nil {
		return PreviewRecord{}, err
	}
	defer capacityLock.unlock()
	if project.Config.Resources.MaxConcurrentPreviews > 0 {
		previews, _ := a.listPreviewsReconciled()
		running := 0
		for _, p := range previews {
			if p.ID == req.ID {
				continue
			}
			if a.previewConsumesRuntimeCapacity(p) {
				running++
			}
		}
		if running >= project.Config.Resources.MaxConcurrentPreviews {
			return PreviewRecord{}, fmt.Errorf("resource cap reached: %d previews running", running)
		}
	}
	p := PreviewRecord{ID: req.ID, Project: req.Project, Profile: activeProfile, Status: "pending", ConfigHash: configHash, Labels: req.Labels, Metadata: req.Metadata, Sources: map[string]PreviewSource{}, Services: map[string]PreviewService{}, CreatedAt: nowUTC()}
	if err := a.upsertPreview(p); err != nil {
		return p, err
	}
	capacityLock.unlock()
	capacityLock = nil
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
	var warmState warmRunState
	runtimeConfig, warmState, err = a.prepareSmartWarmVolumes(project, req, runtimeConfig, p.Sources)
	if err != nil {
		a.recordEvent(req.ID, "error", "warm.failed", err.Error(), "", nil)
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	if err := a.writeComposeManifest(req.ID, runtimeConfig, p.Sources); err != nil {
		return p, err
	}
	if err := a.containerRuntime().EnsureNetwork(req.ID); err != nil {
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	cleanupFailedStartup := func(primary error) error {
		a.snapshotPreviewServiceLogs(req.ID, p.Services, runtimeConfig)
		if cleanupErr := a.cleanupPreviewServices(req.ID, p.Services); cleanupErr != nil {
			return fmt.Errorf("%w; cleanup failed: %v", primary, cleanupErr)
		}
		return primary
	}
	if err := a.startBackingServices(req, runtimeConfig, p.Sources, p.Services); err != nil {
		a.recordEvent(req.ID, "error", "backing.failed", err.Error(), "", nil)
		err = cleanupFailedStartup(err)
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	setupTimer := startOperationTimer()
	if err := a.runSetupSteps(req.ID, runtimeConfig.Setup.AfterSeeds, runtimeConfig, p.Sources, warmState, project.Path); err != nil {
		a.recordEvent(req.ID, "error", "setup.failed", err.Error(), "", setupTimer.metadata(nil))
		err = cleanupFailedStartup(err)
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	if err := a.finalizeSmartWarmBaseline(req.ID, warmState); err != nil {
		a.recordEvent(req.ID, "error", "warm.finalize_failed", err.Error(), "", nil)
		err = cleanupFailedStartup(err)
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	if err := a.setPreviewStatus(req.ID, "starting_apps"); err != nil {
		return p, cleanupFailedStartup(err)
	}
	deferReadiness := len(runtimeConfig.Setup.EveryBoot) > 0
	if err := a.startAppServices(req, runtimeConfig, p.Sources, p.Services, deferReadiness); err != nil {
		err = cleanupFailedStartup(err)
		_ = a.setPreviewStatus(req.ID, "unhealthy")
		return p, err
	}
	if deferReadiness {
		everyBootTimer := startOperationTimer()
		if err := a.runSetupStepsNamed("setup.everyBoot", req.ID, runtimeConfig.Setup.EveryBoot, runtimeConfig, p.Sources, warmState, project.Path); err != nil {
			a.recordEvent(req.ID, "error", "setup.failed", err.Error(), "", everyBootTimer.metadata(nil))
			err = cleanupFailedStartup(err)
			_ = a.setPreviewStatus(req.ID, "unhealthy")
			return p, err
		}
		if err := a.finalizeAppServicesReadiness(req, runtimeConfig, p.Services); err != nil {
			err = cleanupFailedStartup(err)
			_ = a.setPreviewStatus(req.ID, "unhealthy")
			return p, err
		}
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
