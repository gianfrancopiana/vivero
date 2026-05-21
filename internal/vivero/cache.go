package vivero

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	cacheKindBuild  = "build"
	cacheKindVolume = "volume"
	cacheKindImage  = "image"
	cacheKindAll    = "all"
)

type CacheInventory struct {
	Project        string             `json:"project"`
	OK             bool               `json:"ok"`
	BuildCaches    []BuildCacheEntry  `json:"buildCaches"`
	WarmVolumes    []VolumeCacheEntry `json:"warmVolumes"`
	ProjectVolumes []VolumeCacheEntry `json:"projectVolumes"`
	Images         []ImageCacheEntry  `json:"images"`
}

type BuildCacheEntry struct {
	Service      string   `json:"service"`
	Source       string   `json:"source,omitempty"`
	Context      string   `json:"context"`
	Dockerfile   string   `json:"dockerfile,omitempty"`
	Tag          string   `json:"tag"`
	Engine       string   `json:"engine"`
	CacheEnabled bool     `json:"cacheEnabled"`
	From         []string `json:"from"`
	To           []string `json:"to"`
	LocalDirs    []string `json:"localDirs"`
}

type VolumeCacheEntry struct {
	Service     string    `json:"service"`
	Name        string    `json:"name"`
	Target      string    `json:"target"`
	Lifetime    string    `json:"lifetime"`
	VolumeName  string    `json:"volumeName"`
	Exists      bool      `json:"exists"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
}

type ImageCacheEntry struct {
	Service       string `json:"service"`
	Tag           string `json:"tag,omitempty"`
	Reference     string `json:"reference,omitempty"`
	Exists        bool   `json:"exists"`
	ViveroManaged bool   `json:"viveroManaged"`
}

type CacheAction struct {
	Kind     string `json:"kind"`
	Service  string `json:"service,omitempty"`
	Name     string `json:"name,omitempty"`
	Resource string `json:"resource,omitempty"`
	Path     string `json:"path,omitempty"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

type CacheWarmOptions struct {
	Sources map[string]string
}

type CacheWarmResult struct {
	Project string        `json:"project"`
	OK      bool          `json:"ok"`
	Actions []CacheAction `json:"actions"`
}

type CachePruneOptions struct {
	Kind    string
	Yes     bool
	NoInput bool
}

type CachePruneResult struct {
	Project string        `json:"project"`
	OK      bool          `json:"ok"`
	Kind    string        `json:"kind"`
	Removed []CacheAction `json:"removed"`
}

func (a *App) CacheInspect(project string) (CacheInventory, error) {
	rec, err := a.getProject(project)
	if err != nil {
		return CacheInventory{}, err
	}
	inventory := CacheInventory{Project: rec.Name, OK: true}
	buildCaches, err := a.cacheBuildEntries(rec)
	if err != nil {
		return CacheInventory{}, err
	}
	inventory.BuildCaches = buildCaches
	inventory.WarmVolumes = a.cacheWarmVolumeEntries(rec)
	inventory.ProjectVolumes = a.cacheProjectVolumeEntries(rec)
	inventory.Images = a.cacheImageEntries(rec)
	return inventory, nil
}

func (a *App) CacheWarm(project string, opts CacheWarmOptions) (CacheWarmResult, error) {
	rec, err := a.getProject(project)
	if err != nil {
		return CacheWarmResult{}, err
	}
	previewID := cacheWarmPreviewID(rec.Name)
	sources, err := a.cacheWarmSources(rec, opts.Sources, previewID)
	if err != nil {
		return CacheWarmResult{}, err
	}
	metadata := map[string]string{"warm.ref": cacheWarmRef(rec.Config, opts.Sources)}
	if metadata["warm.ref"] == "" {
		metadata["warm.ref"] = "main"
	}
	cfg, warm, err := a.prepareSmartWarmVolumes(rec, UpRequest{Project: rec.Name, ID: previewID, Sources: opts.Sources, Metadata: metadata}, rec.Config, sources)
	if err != nil {
		return CacheWarmResult{Project: rec.Name, OK: false}, err
	}
	if warm.Active && warm.Mode != warmModeBaseline {
		return CacheWarmResult{Project: rec.Name, OK: false}, fmt.Errorf("cache warm requires a baseline ref; %q is not in warm.baselineRefs", warm.Ref)
	}
	result := CacheWarmResult{Project: rec.Name, OK: true}
	for _, binding := range warm.Volumes {
		result.Actions = append(result.Actions, CacheAction{Kind: cacheKindVolume, Service: binding.Service, Name: binding.Name, Resource: binding.BaselineName, Status: "warmed"})
	}
	if err := a.finalizeSmartWarmBaseline(previewID, warm); err != nil {
		return CacheWarmResult{Project: rec.Name, OK: false, Actions: result.Actions}, err
	}
	if len(rec.Config.Prebuild) > 0 {
		prebuild, err := a.Prebuild(rec.Name)
		action := CacheAction{Kind: "prebuild", Status: "completed"}
		if err != nil {
			action.Status = "failed"
			action.Error = err.Error()
			result.Actions = append(result.Actions, action)
			return CacheWarmResult{Project: rec.Name, OK: false, Actions: result.Actions}, err
		}
		if prebuild["ok"] == false {
			action.Status = "failed"
		}
		result.Actions = append(result.Actions, action)
	}
	buildActions, err := a.buildCacheEnabledImages(rec, previewID, sources, cfg)
	result.Actions = append(result.Actions, buildActions...)
	if err != nil {
		result.OK = false
		return result, err
	}
	return result, nil
}

func (a *App) CachePrune(project string, opts CachePruneOptions) (CachePruneResult, error) {
	rec, err := a.getProject(project)
	if err != nil {
		return CachePruneResult{}, err
	}
	kind := strings.ToLower(strings.TrimSpace(opts.Kind))
	if kind == "" {
		return CachePruneResult{}, fmt.Errorf("cache prune requires --kind build|volume|image|all")
	}
	if kind != cacheKindBuild && kind != cacheKindVolume && kind != cacheKindImage && kind != cacheKindAll {
		return CachePruneResult{}, fmt.Errorf("unsupported cache prune kind %q; use build, volume, image, or all", opts.Kind)
	}
	if !opts.Yes && !opts.NoInput {
		return CachePruneResult{}, fmt.Errorf("cache prune requires --yes, or --no-input with explicit project and --kind")
	}
	inventory, err := a.CacheInspect(rec.Name)
	if err != nil {
		return CachePruneResult{}, err
	}
	result := CachePruneResult{Project: rec.Name, OK: true, Kind: kind}
	if kind == cacheKindBuild || kind == cacheKindAll {
		for _, entry := range inventory.BuildCaches {
			for _, dir := range entry.LocalDirs {
				action := CacheAction{Kind: cacheKindBuild, Service: entry.Service, Path: dir, Status: "missing"}
				if pathExists(dir) {
					if err := os.RemoveAll(dir); err != nil {
						action.Status = "failed"
						action.Error = err.Error()
						result.OK = false
					} else {
						action.Status = "removed"
					}
				}
				result.Removed = append(result.Removed, action)
			}
		}
	}
	if kind == cacheKindVolume || kind == cacheKindAll {
		for _, volume := range append([]VolumeCacheEntry{}, append(inventory.WarmVolumes, inventory.ProjectVolumes...)...) {
			action := CacheAction{Kind: cacheKindVolume, Service: volume.Service, Name: volume.Name, Resource: volume.VolumeName, Status: "missing"}
			if volume.Exists {
				if err := a.containerRuntime().RemoveVolume(volume.VolumeName); err != nil {
					action.Status = "failed"
					action.Error = err.Error()
					result.OK = false
				} else {
					action.Status = "removed"
				}
			}
			result.Removed = append(result.Removed, action)
		}
	}
	if kind == cacheKindImage || kind == cacheKindAll {
		for _, image := range inventory.Images {
			ref := strings.TrimSpace(image.Tag)
			if ref == "" || strings.Contains(ref, "*") {
				continue
			}
			action := CacheAction{Kind: cacheKindImage, Service: image.Service, Resource: ref, Status: "missing"}
			if image.Exists {
				if err := a.containerRuntime().RemoveImage(ref); err != nil {
					action.Status = "failed"
					action.Error = err.Error()
					result.OK = false
				} else {
					action.Status = "removed"
				}
			}
			result.Removed = append(result.Removed, action)
		}
	}
	return result, nil
}

func (a *App) cacheBuildEntries(rec ProjectRecord) ([]BuildCacheEntry, error) {
	entries := []BuildCacheEntry{}
	for _, service := range sortedMapKeys(rec.Config.Services) {
		svc := rec.Config.Services[service]
		if !imageBuildConfigured(svc.Build) || !imageBuildCacheEnabled(svc.Build.Cache) {
			continue
		}
		basePath, err := a.cacheBuildBasePath(rec, svc, nil)
		if err != nil {
			return nil, err
		}
		spec, err := dockerBuildSpecForService(basePath, rec.Name, cacheWarmPreviewID(rec.Name), service, svc.Build)
		if err != nil {
			return nil, err
		}
		entries = append(entries, BuildCacheEntry{
			Service:      service,
			Source:       svc.Source,
			Context:      spec.Context,
			Dockerfile:   spec.Dockerfile,
			Tag:          spec.Tag,
			Engine:       dockerBuildEngine(spec),
			CacheEnabled: spec.CacheEnabled,
			From:         normalizedStringSlice(spec.CacheFrom),
			To:           normalizedStringSlice(spec.CacheTo),
			LocalDirs:    localDirsFromBuildCacheSpecs(spec.CacheFrom, spec.CacheTo),
		})
	}
	return entries, nil
}

func (a *App) cacheWarmVolumeEntries(rec ProjectRecord) []VolumeCacheEntry {
	entries := []VolumeCacheEntry{}
	collect := func(service string, volumes []VolumeConfig) {
		for _, vol := range volumes {
			lifetime, err := normalizeVolumeLifetime(vol.Lifetime)
			if err != nil || lifetime != "smart" || strings.TrimSpace(vol.Name) == "" || strings.TrimSpace(vol.Target) == "" {
				continue
			}
			volumeName := dockerSmartBaselineVolumeName(rec.Name, service, vol.Name)
			entry := VolumeCacheEntry{Service: service, Name: vol.Name, Target: vol.Target, Lifetime: lifetime, VolumeName: volumeName, Exists: a.containerRuntime().VolumeExists(volumeName)}
			if state, err := a.readWarmVolumeState(rec.Name, service, vol.Name); err == nil {
				entry.Fingerprint = state.Fingerprint
				entry.Ref = state.Ref
				entry.UpdatedAt = state.UpdatedAt
			}
			entries = append(entries, entry)
		}
	}
	for _, service := range sortedMapKeys(rec.Config.BackingServices) {
		collect(service, rec.Config.BackingServices[service].DependencyVolumes)
	}
	for _, service := range sortedMapKeys(rec.Config.Services) {
		collect(service, rec.Config.Services[service].DependencyVolumes)
	}
	return entries
}

func (a *App) cacheProjectVolumeEntries(rec ProjectRecord) []VolumeCacheEntry {
	entries := []VolumeCacheEntry{}
	collect := func(service string, volumes []VolumeConfig) {
		for _, vol := range volumes {
			lifetime, err := normalizeVolumeLifetime(vol.Lifetime)
			if err != nil || lifetime != "project" || strings.TrimSpace(vol.Name) == "" || strings.TrimSpace(vol.Target) == "" {
				continue
			}
			volumeName := dockerProjectVolumeName(rec.Name, service, vol.Name)
			entries = append(entries, VolumeCacheEntry{Service: service, Name: vol.Name, Target: vol.Target, Lifetime: lifetime, VolumeName: volumeName, Exists: a.containerRuntime().VolumeExists(volumeName)})
		}
	}
	for _, service := range sortedMapKeys(rec.Config.BackingServices) {
		collect(service, rec.Config.BackingServices[service].DependencyVolumes)
	}
	for _, service := range sortedMapKeys(rec.Config.Services) {
		collect(service, rec.Config.Services[service].DependencyVolumes)
	}
	return entries
}

func (a *App) cacheImageEntries(rec ProjectRecord) []ImageCacheEntry {
	entries := []ImageCacheEntry{}
	previewIDs := a.previewIDsForProject(rec.Name)
	for _, service := range sortedMapKeys(rec.Config.Services) {
		svc := rec.Config.Services[service]
		if !imageBuildConfigured(svc.Build) {
			continue
		}
		if tag := strings.TrimSpace(svc.Build.Tag); tag != "" {
			entries = append(entries, ImageCacheEntry{Service: service, Tag: tag, Reference: tag, Exists: a.containerRuntime().ImageExists(tag), ViveroManaged: strings.HasPrefix(tag, "vivero/")})
			continue
		}
		if len(previewIDs) == 0 {
			reference := defaultServiceImageRepository(rec.Name, service) + ":*"
			entries = append(entries, ImageCacheEntry{Service: service, Reference: reference, Exists: false, ViveroManaged: true})
			continue
		}
		for _, previewID := range previewIDs {
			tag := defaultServiceImageTag(rec.Name, previewID, service)
			entries = append(entries, ImageCacheEntry{Service: service, Tag: tag, Reference: tag, Exists: a.containerRuntime().ImageExists(tag), ViveroManaged: true})
		}
	}
	return entries
}

func (a *App) buildCacheEnabledImages(rec ProjectRecord, previewID string, sources map[string]PreviewSource, cfg ProjectConfig) ([]CacheAction, error) {
	actions := []CacheAction{}
	for _, service := range sortedMapKeys(cfg.Services) {
		svc := cfg.Services[service]
		if !imageBuildConfigured(svc.Build) || !imageBuildCacheEnabled(svc.Build.Cache) {
			continue
		}
		basePath, err := a.cacheBuildBasePath(rec, svc, sources)
		if err != nil {
			return actions, err
		}
		spec, err := dockerBuildSpecForService(basePath, rec.Name, previewID, service, svc.Build)
		if err != nil {
			return actions, err
		}
		metadata := dockerBuildEventMetadata(spec)
		a.recordEvent(previewID, "info", "cache.build.warming", "warming build cache for service image", service, metadata)
		if err := a.containerRuntime().BuildImage(spec); err != nil {
			a.recordEvent(previewID, "error", "cache.build_failed", err.Error(), service, metadata)
			actions = append(actions, CacheAction{Kind: cacheKindBuild, Service: service, Resource: spec.Tag, Status: "failed", Error: err.Error()})
			return actions, fmt.Errorf("warm build cache for service %s: %w", service, err)
		}
		a.recordEvent(previewID, "info", "cache.build.warmed", "build cache warmed for service image", service, metadata)
		actions = append(actions, CacheAction{Kind: cacheKindBuild, Service: service, Resource: spec.Tag, Status: "warmed"})
	}
	return actions, nil
}

func (a *App) cacheBuildBasePath(rec ProjectRecord, svc ServiceConfig, sources map[string]PreviewSource) (string, error) {
	if strings.TrimSpace(svc.Source) == "" {
		return rec.Path, nil
	}
	if sources != nil {
		if src, ok := sources[svc.Source]; ok && strings.TrimSpace(src.Path) != "" {
			return src.Path, nil
		}
	}
	srcCfg, ok := rec.Config.Sources[svc.Source]
	if !ok {
		return "", fmt.Errorf("service references unknown source %s", svc.Source)
	}
	if strings.TrimSpace(srcCfg.Path) != "" {
		return resolveSourcePath(rec.Path, srcCfg.Path)
	}
	repoPath := managedRepoPath(a.Home, svc.Source)
	if pathExists(repoPath) {
		return repoPath, nil
	}
	return rec.Path, nil
}

func (a *App) cacheWarmSources(rec ProjectRecord, overrides map[string]string, previewID string) (map[string]PreviewSource, error) {
	sources := map[string]PreviewSource{}
	for _, name := range sortedMapKeys(rec.Config.Sources) {
		source, err := a.resolveSource(rec.Name, rec.Path, previewID, name, rec.Config.Sources[name], overrides)
		if err != nil {
			return nil, err
		}
		sources[name] = source
	}
	return sources, nil
}

func cacheWarmRef(cfg ProjectConfig, overrides map[string]string) string {
	for _, key := range sortedMapKeys(overrides) {
		if strings.HasSuffix(key, ".ref") {
			if ref := normalizeWarmRef(overrides[key]); ref != "" {
				return ref
			}
		}
	}
	if len(cfg.Warm.BaselineRefs) > 0 {
		return normalizeWarmRef(cfg.Warm.BaselineRefs[0])
	}
	for _, name := range sortedMapKeys(cfg.Sources) {
		if ref := normalizeWarmRef(cfg.Sources[name].DefaultRef); ref != "" {
			return ref
		}
	}
	return "main"
}

func localDirsFromBuildCacheSpecs(groups ...[]string) []string {
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, raw := range group {
			spec, err := parseBuildCacheSpec(raw)
			if err != nil || !spec.isLocal() {
				continue
			}
			for _, key := range []string{"src", "dest"} {
				if value, ok := spec.option(key); ok && strings.TrimSpace(value) != "" {
					seen[filepath.Clean(value)] = struct{}{}
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for dir := range seen {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

func normalizedStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string{}, values...)
}

func (a *App) previewIDsForProject(project string) []string {
	rows, err := a.db.Query(`SELECT id FROM previews WHERE project=? ORDER BY id`, project)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil && strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func cacheWarmPreviewID(project string) string {
	return dockerResourceName("cache", "warm", project)
}

func defaultServiceImageRepository(projectName, service string) string {
	return "vivero/" + sanitizeDockerName(projectName+"-"+service)
}
