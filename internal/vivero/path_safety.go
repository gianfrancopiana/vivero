package vivero

import (
	"path/filepath"
	"strings"
)

const safePathComponentMaxLen = 120

func safePathComponent(value, fallback string) string {
	raw := strings.TrimSpace(value)
	if isSafePathComponent(raw) && len(raw) <= safePathComponentMaxLen {
		return raw
	}
	clean := strings.ReplaceAll(sanitizeDockerName(raw), ".", "-")
	if clean == "" {
		clean = strings.ReplaceAll(sanitizeDockerName(fallback), ".", "-")
	}
	if clean == "" {
		clean = "item"
	}
	hash := shortStableID(raw)
	maxClean := safePathComponentMaxLen - len(hash) - 1
	if maxClean < 1 {
		maxClean = 1
	}
	if len(clean) > maxClean {
		clean = strings.Trim(clean[:maxClean], "-._")
	}
	if clean == "" {
		clean = "item"
	}
	return clean + "-" + hash
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

func isSafePathComponent(value string) bool {
	if value == "" || value == "." || value == ".." || filepath.IsAbs(value) {
		return false
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}
