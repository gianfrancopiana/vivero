package vivero

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) reusablePreviewForUp(req UpRequest, project ProjectRecord, cfg ProjectConfig) (PreviewRecord, bool, error) {
	existing, err := a.getPreview(req.ID)
	if err != nil {
		if strings.Contains(err.Error(), "preview not found") {
			return PreviewRecord{}, false, nil
		}
		return existing, false, err
	}
	if reason := a.previewReuseMissReason(existing, req, project, cfg); reason != "" {
		a.recordEvent(req.ID, "info", "preview.reuse_miss", "existing preview not reusable: "+reason, "", map[string]string{"reason": reason})
		return existing, false, nil
	}
	if req.Wait {
		if err := a.Wait(req.ID, req.Timeout); err != nil {
			a.recordEvent(req.ID, "info", "preview.reuse_miss", "existing preview failed wait before reuse", "", map[string]string{"reason": err.Error()})
			return existing, false, nil
		}
		if refreshed, err := a.getPreview(req.ID); err == nil {
			existing = refreshed
		}
	}
	_ = a.upsertPreview(existing)
	a.recordEvent(req.ID, "info", "preview.reused", "existing healthy preview reused without restart", "", map[string]string{"policy": "reuse"})
	if refreshed, err := a.getPreview(req.ID); err == nil {
		existing = refreshed
	}
	return existing, true, nil
}

func (a *App) previewReuseMissReason(existing PreviewRecord, req UpRequest, project ProjectRecord, cfg ProjectConfig) string {
	if existing.Project != req.Project {
		return fmt.Sprintf("project changed from %s to %s", existing.Project, req.Project)
	}
	if strings.TrimSpace(existing.Profile) != strings.TrimSpace(req.Profile) {
		return "profile changed"
	}
	if existing.Status != "running" {
		return "preview status is " + existing.Status
	}
	if oldBranch, newBranch := canonicalBranchFromMetadata(existing.Metadata), canonicalBranchFromMetadata(req.Metadata); oldBranch != "" && newBranch != "" && oldBranch != newBranch {
		return "branch metadata changed"
	}
	if reason, err := reuseSourceMissReason(project.Path, cfg.Sources, req.Sources, existing.Sources); err != nil {
		return err.Error()
	} else if reason != "" {
		return reason
	}
	if reason := a.reuseServiceMissReason(cfg, req.Public, existing.Services); reason != "" {
		return reason
	}
	return ""
}

func reuseSourceMissReason(projectPath string, configs map[string]SourceConfig, overrides map[string]string, existing map[string]PreviewSource) (string, error) {
	if len(existing) != len(configs) {
		return "source set changed", nil
	}
	for _, name := range sortedMapKeys(configs) {
		current, ok := existing[name]
		if !ok {
			return "source " + name + " missing", nil
		}
		identity, err := expectedReuseSourceIdentity(projectPath, name, configs[name], overrides)
		if err != nil {
			return "", err
		}
		if identity.mode == "external" {
			if current.Mode != "external" || cleanPath(current.Path) != identity.path {
				return "source " + name + " path changed", nil
			}
			continue
		}
		if current.Mode != "managed" {
			return "source " + name + " mode changed", nil
		}
		if identity.ref != "" && current.Ref != identity.ref {
			return "source " + name + " ref changed", nil
		}
	}
	return "", nil
}

type reuseSourceIdentity struct {
	mode string
	path string
	ref  string
}

func expectedReuseSourceIdentity(projectPath, name string, src SourceConfig, overrides map[string]string) (reuseSourceIdentity, error) {
	if overrides == nil {
		overrides = map[string]string{}
	}
	if p := strings.TrimSpace(overrides[name+".path"]); p != "" {
		abs, err := filepath.Abs(expandPath(p))
		if err != nil {
			return reuseSourceIdentity{}, err
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return reuseSourceIdentity{}, fmt.Errorf("external source path is not a directory: %s", abs)
		}
		return reuseSourceIdentity{mode: "external", path: cleanPath(abs)}, nil
	}
	if src.Path != "" {
		abs, err := resolveSourcePath(projectPath, src.Path)
		if err != nil {
			return reuseSourceIdentity{}, err
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return reuseSourceIdentity{}, fmt.Errorf("configured source path is not a directory: %s", abs)
		}
		return reuseSourceIdentity{mode: "external", path: cleanPath(abs)}, nil
	}
	ref := strings.TrimSpace(src.DefaultRef)
	if v := strings.TrimSpace(overrides[name+".ref"]); v != "" {
		ref = v
	}
	if ref == "" {
		ref = "main"
	}
	return reuseSourceIdentity{mode: "managed", ref: ref}, nil
}

func cleanPath(path string) string {
	abs, err := filepath.Abs(expandPath(path))
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func (a *App) reuseServiceMissReason(cfg ProjectConfig, forcePublic bool, existing map[string]PreviewService) string {
	required := map[string]ServiceConfig{}
	for _, name := range sortedMapKeys(cfg.BackingServices) {
		required[name] = serviceConfigForBacking(cfg.BackingServices[name])
	}
	for _, name := range sortedMapKeys(cfg.Services) {
		required[name] = cfg.Services[name]
	}
	if len(existing) != len(required) {
		return "service set changed"
	}
	for _, name := range sortedMapKeys(required) {
		svc, ok := existing[name]
		if !ok {
			return "service " + name + " missing"
		}
		cfgSvc := required[name]
		if runtime := serviceRuntime(cfgSvc); svc.Runtime != "" && svc.Runtime != runtime {
			return "service " + name + " runtime changed"
		}
		if !reusableServiceStatus(svc.Status) {
			return "service " + name + " status is " + svc.Status
		}
		if serviceResourcesStopped(svc) {
			return "service " + name + " has no running resources"
		}
		if svc.ContainerID != "" && !a.containerRuntime().ContainerExists(svc.ContainerID) {
			return "service " + name + " container is missing"
		}
		for _, pid := range []struct {
			name string
			pid  int
		}{
			{name: "process", pid: svc.PID},
			{name: "proxy", pid: svc.ProxyPID},
			{name: "tunnel", pid: svc.TunnelPID},
		} {
			if pid.pid > 0 && !processExists(pid.pid) {
				return fmt.Sprintf("service %s %s pid is missing", name, pid.name)
			}
		}
		ports, err := servicePortPlan(cfgSvc)
		if err != nil {
			return err.Error()
		}
		if len(ports) > 0 && svc.OriginURL == "" {
			return "service " + name + " origin URL is missing"
		}
		if serviceIsPublic(cfgSvc, forcePublic) && len(ports) > 0 && svc.URL == "" {
			return "service " + name + " public URL is missing"
		}
	}
	return ""
}

func reusableServiceStatus(status string) bool {
	switch status {
	case "healthy", "running":
		return true
	default:
		return false
	}
}
