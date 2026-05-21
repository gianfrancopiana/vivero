package safepath

import (
	"path/filepath"
	"strings"

	"github.com/gianfrancopiana/vivero/internal/nameid"
)

const componentMaxLen = 120

// Component returns a single filesystem path component that cannot escape its parent.
func Component(value, fallback string) string {
	raw := strings.TrimSpace(value)
	if IsComponent(raw) && len(raw) <= componentMaxLen {
		return raw
	}
	clean := strings.ReplaceAll(nameid.Docker(raw), ".", "-")
	if clean == "" {
		clean = strings.ReplaceAll(nameid.Docker(fallback), ".", "-")
	}
	if clean == "" {
		clean = "item"
	}
	hash := nameid.ShortStable(raw)
	maxClean := componentMaxLen - len(hash) - 1
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

// IsComponent reports whether value is already a safe single path component.
func IsComponent(value string) bool {
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

// WithinRoot reports whether path is root itself or contained below root.
func WithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
