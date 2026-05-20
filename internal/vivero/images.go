package vivero

import (
	"fmt"
	"strings"
)

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
		if err := a.containerRuntime().BuildImage(spec); err != nil {
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

func defaultServiceImageTag(projectName, previewID, service string) string {
	name := sanitizeDockerName(projectName + "-" + service)
	return "vivero/" + name + ":" + shortStableID(previewID+":"+service)
}
