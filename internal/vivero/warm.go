package vivero

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	warmModeNone     = ""
	warmModeBaseline = "baseline"
	warmModeDerived  = "derived"
)

type warmRunState struct {
	Active        bool
	Project       string
	PreviewID     string
	Mode          string
	Ref           string
	Fingerprint   string
	BaselineReady bool
	Volumes       []warmVolumeBinding
}

type warmVolumeBinding struct {
	Service      string `json:"service"`
	Name         string `json:"name"`
	BaselineName string `json:"baselineName"`
	ActiveName   string `json:"activeName"`
	Duration     string `json:"duration,omitempty"`
	DurationMs   int64  `json:"durationMs,omitempty"`
}

type warmVolumeState struct {
	Project     string    `json:"project"`
	Service     string    `json:"service"`
	Name        string    `json:"name"`
	VolumeName  string    `json:"volumeName"`
	Fingerprint string    `json:"fingerprint"`
	Ref         string    `json:"ref,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

var defaultSmartWarmFingerprintPaths = []string{
	"Gemfile.lock",
	"package-lock.json",
	"pnpm-lock.yaml",
	"yarn.lock",
	"go.sum",
	"db/migrate",
	"db/schema.rb",
	"db/seeds.rb",
	"migrations",
	"schema.sql",
}

func (a *App) prepareSmartWarmVolumes(project ProjectRecord, req UpRequest, cfg ProjectConfig, sources map[string]PreviewSource) (ProjectConfig, warmRunState, error) {
	state := warmRunState{Project: cfg.Project.Name, PreviewID: req.ID, Mode: warmModeNone}
	if !projectUsesSmartWarmVolumes(cfg) {
		return cfg, state, nil
	}
	fingerprint, err := computeSmartWarmFingerprint(project.Path, cfg, sources)
	if err != nil {
		return cfg, state, err
	}
	ref := detectWarmRef(project, req, sources)
	mode := warmModeDerived
	if warmRefIsBaseline(ref, cfg.Warm.BaselineRefs) {
		mode = warmModeBaseline
	}
	state.Active = true
	state.Mode = mode
	state.Ref = ref
	state.Fingerprint = fingerprint
	state.BaselineReady = a.smartWarmBaselineReady(cfg.Project.Name, cfg, fingerprint)

	bindServiceVolumes := func(service string, volumes []VolumeConfig) ([]VolumeConfig, error) {
		out := append([]VolumeConfig(nil), volumes...)
		for i, vol := range out {
			lifetime, err := normalizeVolumeLifetime(vol.Lifetime)
			if err != nil {
				return nil, err
			}
			if lifetime != "smart" || strings.TrimSpace(vol.Name) == "" || strings.TrimSpace(vol.Target) == "" {
				continue
			}
			baseline := dockerSmartBaselineVolumeName(cfg.Project.Name, service, vol.Name)
			active := baseline
			if mode == warmModeDerived {
				active = dockerSmartPreviewVolumeName(cfg.Project.Name, req.ID, service, vol.Name)
			}
			out[i].RuntimeSource = active
			state.Volumes = append(state.Volumes, warmVolumeBinding{Service: service, Name: vol.Name, BaselineName: baseline, ActiveName: active})
		}
		return out, nil
	}

	for _, service := range sortedMapKeys(cfg.BackingServices) {
		backing := cfg.BackingServices[service]
		volumes, err := bindServiceVolumes(service, backing.DependencyVolumes)
		if err != nil {
			return cfg, state, fmt.Errorf("prepare smart warm volumes for backing service %s: %w", service, err)
		}
		backing.DependencyVolumes = volumes
		cfg.BackingServices[service] = backing
	}
	for _, service := range sortedMapKeys(cfg.Services) {
		svc := cfg.Services[service]
		volumes, err := bindServiceVolumes(service, svc.DependencyVolumes)
		if err != nil {
			return cfg, state, fmt.Errorf("prepare smart warm volumes for service %s: %w", service, err)
		}
		svc.DependencyVolumes = volumes
		cfg.Services[service] = svc
	}
	if len(state.Volumes) > 0 {
		if mode == warmModeBaseline && !state.BaselineReady {
			if err := a.clearWarmSetupMarkers(cfg.Project.Name, fingerprint); err != nil {
				return cfg, state, err
			}
		}
		if mode == warmModeDerived {
			if err := a.clearPreviewWarmSetupMarkers(req.ID, fingerprint); err != nil {
				return cfg, state, err
			}
		}
	}

	for i := range state.Volumes {
		binding := &state.Volumes[i]
		timer := startOperationTimer()
		if mode == warmModeBaseline {
			if err := a.containerRuntime().EnsureVolume(binding.BaselineName); err != nil {
				return cfg, state, err
			}
			binding.DurationMs, binding.Duration = cacheDurationFromTimer(timer)
			a.recordEvent(req.ID, "info", "warm.baseline", "using canonical smart warm volume", binding.Service, timer.metadata(map[string]string{"volume": binding.BaselineName, "fingerprint": fingerprint, "ref": ref}))
			continue
		}
		if err := a.containerRuntime().RemoveVolume(binding.ActiveName); err != nil {
			return cfg, state, err
		}
		if a.containerRuntime().VolumeExists(binding.BaselineName) {
			if err := a.containerRuntime().CopyVolume(binding.BaselineName, binding.ActiveName); err != nil {
				return cfg, state, err
			}
			binding.DurationMs, binding.Duration = cacheDurationFromTimer(timer)
			a.recordEvent(req.ID, "info", "warm.derived", "created preview-local smart warm volume from baseline", binding.Service, timer.metadata(map[string]string{"baseline": binding.BaselineName, "volume": binding.ActiveName, "fingerprint": fingerprint, "baselineReady": fmt.Sprint(state.BaselineReady), "ref": ref}))
		} else {
			if err := a.containerRuntime().EnsureVolume(binding.ActiveName); err != nil {
				return cfg, state, err
			}
			binding.DurationMs, binding.Duration = cacheDurationFromTimer(timer)
			a.recordEvent(req.ID, "info", "warm.derived", "created empty preview-local smart warm volume; baseline is missing", binding.Service, timer.metadata(map[string]string{"baseline": binding.BaselineName, "volume": binding.ActiveName, "fingerprint": fingerprint, "ref": ref}))
		}
	}
	return cfg, state, nil
}

func projectUsesSmartWarmVolumes(cfg ProjectConfig) bool {
	for _, svc := range cfg.BackingServices {
		if volumesUseSmartWarm(svc.DependencyVolumes) {
			return true
		}
	}
	for _, svc := range cfg.Services {
		if volumesUseSmartWarm(svc.DependencyVolumes) {
			return true
		}
	}
	return false
}

func volumesUseSmartWarm(volumes []VolumeConfig) bool {
	for _, vol := range volumes {
		lifetime, err := normalizeVolumeLifetime(vol.Lifetime)
		if err == nil && lifetime == "smart" {
			return true
		}
	}
	return false
}

func (a *App) finalizeSmartWarmBaseline(previewID string, warm warmRunState) error {
	if !warm.Active || warm.Mode != warmModeBaseline {
		return nil
	}
	for _, binding := range warm.Volumes {
		state := warmVolumeState{
			Project:     warm.Project,
			Service:     binding.Service,
			Name:        binding.Name,
			VolumeName:  binding.BaselineName,
			Fingerprint: warm.Fingerprint,
			Ref:         warm.Ref,
			UpdatedAt:   nowUTC(),
		}
		if err := a.writeWarmVolumeState(state); err != nil {
			return err
		}
		a.recordEvent(previewID, "info", "warm.baseline.updated", "smart warm baseline fingerprint updated", binding.Service, map[string]string{"volume": binding.BaselineName, "fingerprint": warm.Fingerprint, "ref": warm.Ref})
	}
	return nil
}

func (a *App) smartWarmBaselineReady(projectName string, cfg ProjectConfig, fingerprint string) bool {
	if strings.TrimSpace(fingerprint) == "" {
		return false
	}
	checked := 0
	for _, binding := range smartWarmVolumeBindings(projectName, cfg) {
		state, err := a.readWarmVolumeState(projectName, binding.Service, binding.Name)
		if err != nil || state.Fingerprint != fingerprint || state.VolumeName != binding.BaselineName || !a.containerRuntime().VolumeExists(binding.BaselineName) {
			return false
		}
		checked++
	}
	return checked > 0
}

func smartWarmVolumeBindings(projectName string, cfg ProjectConfig) []warmVolumeBinding {
	var bindings []warmVolumeBinding
	collect := func(service string, volumes []VolumeConfig) {
		for _, vol := range volumes {
			lifetime, err := normalizeVolumeLifetime(vol.Lifetime)
			if err != nil || lifetime != "smart" || strings.TrimSpace(vol.Name) == "" || strings.TrimSpace(vol.Target) == "" {
				continue
			}
			baseline := dockerSmartBaselineVolumeName(projectName, service, vol.Name)
			bindings = append(bindings, warmVolumeBinding{Service: service, Name: vol.Name, BaselineName: baseline, ActiveName: baseline})
		}
	}
	for _, service := range sortedMapKeys(cfg.BackingServices) {
		collect(service, cfg.BackingServices[service].DependencyVolumes)
	}
	for _, service := range sortedMapKeys(cfg.Services) {
		collect(service, cfg.Services[service].DependencyVolumes)
	}
	return bindings
}

func (a *App) warmVolumeStatePath(project, service, name string) string {
	return filepath.Join(a.Home, "warm", sanitizeDockerName(project), sanitizeDockerName(service), sanitizeDockerName(name)+".json")
}

func (a *App) readWarmVolumeState(project, service, name string) (warmVolumeState, error) {
	path := a.warmVolumeStatePath(project, service, name)
	body, err := os.ReadFile(path)
	if err != nil {
		return warmVolumeState{}, err
	}
	var state warmVolumeState
	if err := json.Unmarshal(body, &state); err != nil {
		return warmVolumeState{}, err
	}
	return state, nil
}

func (a *App) writeWarmVolumeState(state warmVolumeState) error {
	path := a.warmVolumeStatePath(state.Project, state.Service, state.Name)
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	return writeIndentedJSONFile(path, state, 0o644)
}

func computeSmartWarmFingerprint(projectPath string, cfg ProjectConfig, sources map[string]PreviewSource) (string, error) {
	h := sha256.New()
	writeHashString(h, "vivero-smart-warm-v1\n")
	writeHashJSON(h, map[string]any{
		"warm":            cfg.Warm,
		"setupAfterSeeds": cfg.Setup.AfterSeeds,
		"volumes":         smartWarmVolumeBindings(cfg.Project.Name, cfg),
	})
	paths := cfg.Warm.Fingerprint.Paths
	if len(paths) == 0 {
		paths = defaultSmartWarmFingerprintPaths
	}
	roots := warmFingerprintRoots(projectPath, sources)
	for i, root := range roots {
		fingerprint, err := fingerprintForPaths(root, paths)
		if err != nil {
			return "", err
		}
		writeHashString(h, fmt.Sprintf("root\x00%d\x00%s\n", i, fingerprint))
	}
	sum := hex.EncodeToString(h.Sum(nil))
	return sum[:16], nil
}

func warmFingerprintRoots(projectPath string, sources map[string]PreviewSource) []string {
	seen := map[string]struct{}{}
	add := func(path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		if _, ok := seen[abs]; ok {
			return
		}
		seen[abs] = struct{}{}
	}
	add(projectPath)
	for _, name := range sortedMapKeys(sources) {
		add(sources[name].Path)
	}
	roots := make([]string, 0, len(seen))
	for root := range seen {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots
}

func fingerprintForPaths(root string, paths []string) (string, error) {
	normalized, err := normalizedFingerprintPaths(paths)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	writeHashString(h, "vivero-fingerprint-paths-v1\n")
	for _, rel := range normalized {
		if err := hashFingerprintPath(h, root, rel); err != nil {
			return "", err
		}
	}
	sum := hex.EncodeToString(h.Sum(nil))
	return sum[:16], nil
}

func validateFingerprintPaths(configPath, yamlPath string, paths []string) error {
	for i, path := range paths {
		if _, err := normalizeFingerprintPath(path); err != nil {
			return fmt.Errorf("%s %s[%d] must be a safe project-relative path: %w", configPath, yamlPath, i, err)
		}
	}
	return nil
}

func normalizedFingerprintPaths(paths []string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, path := range paths {
		normalized, err := normalizeFingerprintPath(path)
		if err != nil {
			return nil, err
		}
		seen[normalized] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for path := range seen {
		out = append(out, path)
	}
	sort.Strings(out)
	return out, nil
}

func normalizeFingerprintPath(path string) (string, error) {
	raw := strings.TrimSpace(path)
	if raw == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.ContainsAny(raw, "\x00\n\r") {
		return "", fmt.Errorf("path contains unsupported newline or NUL")
	}
	cleaned := filepath.Clean(raw)
	if filepath.IsAbs(raw) || filepath.IsAbs(cleaned) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the project root", path)
	}
	return filepath.ToSlash(cleaned), nil
}

func hashFingerprintPath(h interface{ Write([]byte) (int, error) }, root, rel string) error {
	full, err := resolveProjectPath(root, filepath.FromSlash(rel))
	if err != nil {
		return err
	}
	info, err := os.Stat(full)
	if os.IsNotExist(err) {
		writeHashString(h, "missing\x00"+rel+"\n")
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return hashFingerprintFile(h, root, full)
	}
	var files []string
	if err := filepath.WalkDir(full, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != full {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(files)
	for _, file := range files {
		if err := hashFingerprintFile(h, root, file); err != nil {
			return err
		}
	}
	return nil
}

func hashFingerprintFile(h interface{ Write([]byte) (int, error) }, root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	writeHashString(h, "file\x00"+filepath.ToSlash(rel)+"\x00")
	writeHashString(h, fmt.Sprint(len(body))+"\x00")
	_, _ = h.Write(body)
	writeHashString(h, "\n")
	return nil
}

func writeHashJSON(h interface{ Write([]byte) (int, error) }, v any) {
	body, _ := json.Marshal(v)
	_, _ = h.Write(body)
	writeHashString(h, "\n")
}

func writeHashString(h interface{ Write([]byte) (int, error) }, s string) {
	_, _ = h.Write([]byte(s))
}

func detectWarmRef(project ProjectRecord, req UpRequest, sources map[string]PreviewSource) string {
	for _, key := range []string{"warm.ref", "branch", "ref", "git.branch", "git.ref"} {
		if req.Metadata != nil {
			if ref := strings.TrimSpace(req.Metadata[key]); ref != "" {
				return normalizeWarmRef(ref)
			}
		}
	}
	if req.Sources != nil {
		for _, source := range sortedMapKeys(project.Config.Sources) {
			if ref := strings.TrimSpace(req.Sources[source+".ref"]); ref != "" {
				return normalizeWarmRef(ref)
			}
		}
	}
	for _, source := range sortedMapKeys(sources) {
		if ref := gitCurrentBranch(sources[source].Path); ref != "" {
			return normalizeWarmRef(ref)
		}
	}
	for _, source := range sortedMapKeys(project.Config.Sources) {
		if ref := strings.TrimSpace(project.Config.Sources[source].DefaultRef); ref != "" {
			return normalizeWarmRef(ref)
		}
	}
	if ref := gitCurrentBranch(project.Path); ref != "" {
		return normalizeWarmRef(ref)
	}
	return "main"
}

func gitCurrentBranch(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	out, err := runCmd(path, nil, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(string(out))
	if ref == "HEAD" {
		return ""
	}
	return ref
}

func normalizeWarmRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "origin/")
	return ref
}

func warmRefIsBaseline(ref string, baselineRefs []string) bool {
	ref = normalizeWarmRef(ref)
	if ref == "" {
		ref = "main"
	}
	if len(baselineRefs) == 0 {
		baselineRefs = []string{"main", "master"}
	}
	for _, baseline := range baselineRefs {
		if normalizeWarmRef(baseline) == ref {
			return true
		}
	}
	return false
}
