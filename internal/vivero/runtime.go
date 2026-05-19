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
