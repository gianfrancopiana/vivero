package vivero

import (
	"path/filepath"

	"github.com/gianfrancopiana/vivero/internal/nameid"
	"github.com/gianfrancopiana/vivero/internal/safepath"
)

func safePathComponent(value, fallback string) string {
	return safepath.Component(value, fallback)
}

func isSafePathComponent(value string) bool {
	return safepath.IsComponent(value)
}

func pathWithinRoot(root, path string) bool {
	return safepath.WithinRoot(root, path)
}

func sanitizeDockerName(s string) string {
	return nameid.Docker(s)
}

func shortStableID(input string) string {
	return nameid.ShortStable(input)
}

func managedRepoPath(home, source string) string {
	return filepath.Join(home, "repos", safePathComponent(source, "source"))
}

func previewRunFilePath(home, previewID, file string) string {
	return filepath.Join(home, "run", safePathComponent(previewID, "preview"), safePathComponent(file, "file"))
}

func serviceLogPath(home, previewID, service, suffix string) string {
	return filepath.Join(home, "logs", safePathComponent(previewID, "preview"), safePathComponent(service, "service")+suffix)
}

func qaVideoFallbackDir(home, previewID string) string {
	return filepath.Join(home, "qa", safePathComponent(previewID, "preview"), "videos")
}

func projectSecretFilePath(home, project string) string {
	return filepath.Join(home, "secrets", safePathComponent(project, "project")+".env")
}
