package vivero

import (
	"path/filepath"
	"strings"

	"github.com/gianfrancopiana/vivero/internal/nameid"
	"github.com/gianfrancopiana/vivero/internal/safepath"
)

func safePathComponent(value, fallback string) string {
	return safepath.Component(value, fallback)
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

func publicDNSLabelSlug(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = strings.Trim(publicDNSLabelSlug(fallback, "vivero"), "-")
	}
	if len(out) <= 63 {
		return out
	}
	suffix := "-" + shortStableID(out)
	prefixLen := 63 - len(suffix)
	if prefixLen < 1 {
		return strings.TrimPrefix(suffix, "-")
	}
	prefix := strings.TrimRight(out[:prefixLen], "-")
	if prefix == "" {
		return strings.TrimPrefix(suffix, "-")
	}
	return prefix + suffix
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
