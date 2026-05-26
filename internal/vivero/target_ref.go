package vivero

import (
	"fmt"
	"strings"
)

func resolvePreviewTargetRef(raw string) (string, map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil, fmt.Errorf("preview target is required")
	}
	previewID := raw
	if kind, id, ok := strings.Cut(raw, ":"); ok {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "preview":
			previewID = strings.TrimSpace(id)
		default:
			return "", nil, newCLIError("unsupported_target", fmt.Sprintf("unsupported target ref %q; use preview:<id>", raw), "Use preview:<id> or a bare preview id.", map[string]any{"target": raw})
		}
	}
	if previewID == "" {
		return "", nil, fmt.Errorf("preview target is required")
	}
	return previewID, map[string]any{"kind": "preview", "id": previewID, "ref": "preview:" + previewID}, nil
}

func attachTargetRef(payload map[string]any, targetRef map[string]any) map[string]any {
	if payload == nil {
		return payload
	}
	payload["targetRef"] = targetRef
	return payload
}
