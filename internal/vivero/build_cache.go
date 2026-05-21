package vivero

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	dockerBuildEngineDocker = "docker"
	dockerBuildEngineBuildx = "buildx"
)

type buildCacheSpecOption struct {
	Raw      string
	Key      string
	Value    string
	HasValue bool
}

type buildCacheSpec struct {
	Raw     string
	Options []buildCacheSpecOption
}

func imageBuildCacheConfigured(cache ImageBuildCacheConfig) bool {
	return imageBuildCacheEnabled(cache)
}

func imageBuildCacheEnabled(cache ImageBuildCacheConfig) bool {
	return (cache.Enabled != nil && *cache.Enabled) || len(cache.From) > 0 || len(cache.To) > 0
}

func validateImageBuildCacheConfig(configPath, service string, cache ImageBuildCacheConfig) error {
	if err := validateImageBuildCacheSpecs(configPath, fmt.Sprintf("services.%s.build.cache.from", service), cache.From); err != nil {
		return err
	}
	if err := validateImageBuildCacheSpecs(configPath, fmt.Sprintf("services.%s.build.cache.to", service), cache.To); err != nil {
		return err
	}
	return nil
}

func validateImageBuildCacheSpecs(configPath, yamlPath string, specs []string) error {
	for i, raw := range specs {
		path := fmt.Sprintf("%s[%d]", yamlPath, i)
		spec := strings.TrimSpace(raw)
		if spec == "" {
			return fmt.Errorf("%s %s must not be empty", configPath, path)
		}
		if strings.ContainsAny(spec, "\x00\n\r") {
			return fmt.Errorf("%s %s contains unsupported newline or NUL", configPath, path)
		}
		if err := validateBuildCacheSpec(spec); err != nil {
			return fmt.Errorf("%s %s %w", configPath, path, err)
		}
	}
	return nil
}

func validateBuildCacheSpec(raw string) error {
	spec, err := parseBuildCacheSpec(raw)
	if err != nil {
		return err
	}
	if !spec.isLocal() {
		return nil
	}
	for _, key := range []string{"src", "dest"} {
		value, ok := spec.option(key)
		if !ok {
			continue
		}
		if err := validateRelativeBuildCacheLocalPath(key, value); err != nil {
			return err
		}
	}
	return nil
}

func validateRelativeBuildCacheLocalPath(key, value string) error {
	path := strings.TrimSpace(value)
	if path == "" {
		return fmt.Errorf("local cache %s path must not be empty", key)
	}
	expanded := expandPath(path)
	if filepath.IsAbs(expanded) || filepath.IsAbs(path) {
		return fmt.Errorf("local cache %s path %q must be relative to the build context", key, path)
	}
	cleaned := filepath.Clean(path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("local cache %s path %q escapes the build context", key, path)
	}
	return nil
}

func resolveBuildCacheSpecs(contextRoot, yamlPath string, specs []string) ([]string, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	resolved := make([]string, 0, len(specs))
	for i, raw := range specs {
		spec, err := resolveBuildCacheSpec(contextRoot, strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", yamlPath, i, err)
		}
		resolved = append(resolved, spec)
	}
	return resolved, nil
}

func resolveBuildCacheSpec(contextRoot, raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("cache spec must not be empty")
	}
	if strings.ContainsAny(raw, "\x00\n\r") {
		return "", fmt.Errorf("cache spec contains unsupported newline or NUL")
	}
	spec, err := parseBuildCacheSpec(raw)
	if err != nil {
		return "", err
	}
	if !spec.isLocal() {
		return raw, nil
	}
	parts := make([]string, 0, len(spec.Options))
	for _, option := range spec.Options {
		if option.HasValue && (strings.EqualFold(option.Key, "src") || strings.EqualFold(option.Key, "dest")) {
			if err := validateRelativeBuildCacheLocalPath(option.Key, option.Value); err != nil {
				return "", err
			}
			resolved, err := resolveProjectPath(contextRoot, option.Value)
			if err != nil {
				return "", fmt.Errorf("local cache %s path: %w", option.Key, err)
			}
			parts = append(parts, option.Key+"="+resolved)
			continue
		}
		parts = append(parts, option.Raw)
	}
	return strings.Join(parts, ","), nil
}

func parseBuildCacheSpec(raw string) (buildCacheSpec, error) {
	spec := buildCacheSpec{Raw: raw}
	parts := strings.Split(raw, ",")
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			return buildCacheSpec{}, fmt.Errorf("cache spec contains an empty option")
		}
		key, value, hasValue := strings.Cut(trimmed, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return buildCacheSpec{}, fmt.Errorf("cache spec contains an empty option key")
		}
		if hasValue {
			value = strings.TrimSpace(value)
		}
		spec.Options = append(spec.Options, buildCacheSpecOption{Raw: trimmed, Key: key, Value: value, HasValue: hasValue})
	}
	return spec, nil
}

func (s buildCacheSpec) option(key string) (string, bool) {
	for _, option := range s.Options {
		if option.HasValue && strings.EqualFold(option.Key, key) {
			return option.Value, true
		}
	}
	return "", false
}

func (s buildCacheSpec) isLocal() bool {
	value, ok := s.option("type")
	return ok && strings.EqualFold(value, "local")
}

func dockerBuildSpecUsesBuildx(spec dockerBuildSpec) bool {
	return spec.Engine == dockerBuildEngineBuildx || spec.CacheEnabled || len(spec.CacheFrom) > 0 || len(spec.CacheTo) > 0
}

func dockerBuildEngine(spec dockerBuildSpec) string {
	if dockerBuildSpecUsesBuildx(spec) {
		return dockerBuildEngineBuildx
	}
	return dockerBuildEngineDocker
}

func dockerBuildCacheSpecsJSON(values []string) string {
	if values == nil {
		values = []string{}
	}
	return jsonString(values)
}
