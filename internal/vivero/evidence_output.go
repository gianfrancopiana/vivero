package vivero

import "fmt"

func attachEvidenceShape(payload map[string]any, targetRef map[string]any) map[string]any {
	if payload == nil {
		return payload
	}
	attachTargetRef(payload, targetRef)
	if _, ok := payload["ok"]; !ok {
		payload["ok"] = evidencePayloadOK(payload)
	}
	discoveredArtifacts := evidenceArtifacts(payload)
	if existingArtifacts, ok := payload["artifacts"].(map[string]any); ok {
		for key, value := range discoveredArtifacts {
			existingArtifacts[key] = value
		}
		payload["artifacts"] = existingArtifacts
	} else {
		payload["artifacts"] = discoveredArtifacts
	}
	if ok, _ := payload["ok"].(bool); !ok {
		if _, exists := payload["nextSuggestedCommands"]; !exists {
			payload["nextSuggestedCommands"] = evidenceNextSuggestedCommands(targetRef)
		}
	}
	return payload
}

func evidencePayloadOK(payload map[string]any) bool {
	if _, hasError := payload["error"]; hasError {
		return false
	}
	for _, key := range []string{"smoke", "run", "record", "diagnosis"} {
		if okValue, exists := evidenceOKValue(payload[key]); exists {
			return okValue
		}
	}
	return true
}

func evidenceOKValue(v any) (bool, bool) {
	switch value := v.(type) {
	case ReleaseSmokeResult:
		return value.OK, true
	case *ReleaseSmokeResult:
		if value == nil {
			return false, false
		}
		return value.OK, true
	case map[string]any:
		okValue, exists := value["ok"].(bool)
		return okValue, exists
	default:
		return false, false
	}
}

func evidenceArtifacts(payload map[string]any) map[string]any {
	artifacts := map[string]any{}
	for _, key := range []string{"logPath", "path", "runPath", "recordPath", "finalPath", "resultPath", "reportPath", "outputDir"} {
		if value := stringValue(payload[key]); value != "" {
			artifacts[key] = value
		}
	}
	if paths := screenshotArtifactPaths(payload["screenshots"]); len(paths) > 0 {
		artifacts["screenshots"] = paths
	}
	if releaseArtifacts, ok := releaseArtifactValue(payload["release"]); ok {
		artifacts["release"] = releaseArtifacts
	}
	if smokeArtifact, ok := smokeArtifactValue(payload["smoke"]); ok {
		artifacts["smoke"] = smokeArtifact
	}
	if report, ok := mapValue(payload["report"]); ok {
		if path := stringValue(report["path"]); path != "" {
			artifacts["reportPath"] = path
		}
	}
	if run, ok := mapValue(payload["run"]); ok {
		if path := stringValue(run["runPath"]); path != "" {
			artifacts["runPath"] = path
		}
		if report, ok := mapValue(run["report"]); ok {
			if path := stringValue(report["path"]); path != "" {
				artifacts["reportPath"] = path
			}
		}
	}
	if record, ok := mapValue(payload["record"]); ok {
		if path := stringValue(record["recordPath"]); path != "" {
			artifacts["recordPath"] = path
		}
	}
	return artifacts
}

func evidenceNextSuggestedCommands(targetRef map[string]any) []string {
	kind := stringValue(targetRef["kind"])
	id := stringValue(targetRef["id"])
	if id == "" {
		return nil
	}
	switch kind {
	case "preview":
		ref := "preview:" + id
		return []string{
			fmt.Sprintf("vivero preview inspect %s --json --no-input", ref),
			fmt.Sprintf("vivero preview events %s --tail --json --no-input", ref),
			fmt.Sprintf("vivero preview diagnose startup %s --json --no-input", ref),
		}
	case "release":
		ref := "release:" + id
		return []string{
			fmt.Sprintf("vivero release events %s --json --no-input", ref),
			fmt.Sprintf("vivero release logs %s --json --no-input", ref),
		}
	default:
		return nil
	}
}

func mapValue(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func releaseArtifactValue(v any) (any, bool) {
	switch release := v.(type) {
	case ReleaseRecord:
		if len(release.Artifacts) == 0 {
			return nil, false
		}
		return release.Artifacts, true
	case *ReleaseRecord:
		if release == nil || len(release.Artifacts) == 0 {
			return nil, false
		}
		return release.Artifacts, true
	case map[string]any:
		artifacts, ok := release["artifacts"]
		return artifacts, ok
	default:
		return nil, false
	}
}

func smokeArtifactValue(v any) (any, bool) {
	switch smoke := v.(type) {
	case ReleaseSmokeResult:
		if smoke.Artifact == nil {
			return nil, false
		}
		return *smoke.Artifact, true
	case *ReleaseSmokeResult:
		if smoke == nil || smoke.Artifact == nil {
			return nil, false
		}
		return *smoke.Artifact, true
	case map[string]any:
		artifact, ok := smoke["artifact"]
		return artifact, ok
	default:
		return nil, false
	}
}

func screenshotArtifactPaths(v any) []string {
	paths := []string{}
	switch screenshots := v.(type) {
	case []map[string]any:
		for _, screenshot := range screenshots {
			if path := stringValue(screenshot["path"]); path != "" {
				paths = append(paths, path)
			}
		}
	case []any:
		for _, raw := range screenshots {
			if screenshot, ok := raw.(map[string]any); ok {
				if path := stringValue(screenshot["path"]); path != "" {
					paths = append(paths, path)
				}
			}
		}
	}
	return paths
}
