package vivero

import (
	"fmt"
	"path/filepath"
	"strings"
)

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
	value, ok := v.(map[string]any)
	if !ok {
		return false, false
	}
	okValue, exists := value["ok"].(bool)
	return okValue, exists
}

func evidenceArtifacts(payload map[string]any) map[string]any {
	artifacts := map[string]any{}
	for _, key := range []string{"logPath", "path", "runPath", "recordPath", "finalPath", "resultPath", "reportPath", "outputDir"} {
		if value := stringValue(payload[key]); value != "" {
			artifacts[key] = value
		}
	}
	screenshots, videos := artifactMediaPaths(payload)
	if len(screenshots) > 0 {
		artifacts["screenshots"] = screenshots
	}
	if len(videos) > 0 {
		artifacts["videos"] = videos
	}
	if len(screenshots)+len(videos) > 0 {
		media := append([]string{}, screenshots...)
		media = append(media, videos...)
		artifacts["media"] = media
	}
	if paths := screenshotArtifactPaths(payload["screenshots"]); len(paths) > 0 && len(screenshots) == 0 {
		artifacts["screenshots"] = paths
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
	id := stringValue(targetRef["id"])
	if id == "" || stringValue(targetRef["kind"]) != "preview" {
		return nil
	}
	ref := "preview:" + id
	return []string{
		fmt.Sprintf("vivero preview inspect %s --json --no-input", ref),
		fmt.Sprintf("vivero preview events %s --tail --json --no-input", ref),
		fmt.Sprintf("vivero preview diagnose startup %s --json --no-input", ref),
	}
}

func mapValue(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func smokeArtifactValue(v any) (any, bool) {
	smoke, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	artifact, ok := smoke["artifact"]
	return artifact, ok
}

func screenshotArtifactPaths(v any) []string {
	screenshots, _ := artifactMediaPaths(v)
	return screenshots
}

func artifactMediaPaths(values ...any) ([]string, []string) {
	screenshots := []string{}
	videos := []string{}
	seenScreenshots := map[string]bool{}
	seenVideos := map[string]bool{}
	add := func(kind, path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		switch kind {
		case "screenshot":
			if !seenScreenshots[path] {
				seenScreenshots[path] = true
				screenshots = append(screenshots, path)
			}
		case "video":
			if !seenVideos[path] {
				seenVideos[path] = true
				videos = append(videos, path)
			}
		}
	}
	var collect func(any)
	collect = func(value any) {
		switch v := value.(type) {
		case nil:
			return
		case string:
			add(mediaKindForPath(v, ""), v)
		case []string:
			for _, item := range v {
				collect(item)
			}
		case []map[string]any:
			for _, item := range v {
				collect(item)
			}
		case []any:
			for _, item := range v {
				collect(item)
			}
		case map[string]any:
			if path := stringValue(v["screenshotPath"]); path != "" {
				add("screenshot", path)
			}
			if path := stringValue(v["videoPath"]); path != "" {
				add("video", path)
			}
			if path := stringValue(v["path"]); path != "" {
				add(mediaKindForPath(path, stringValue(v["format"])), path)
			}
			for _, key := range []string{"screenshots", "videos", "media", "proof", "run", "record", "artifacts", "steps", "flows", "results"} {
				collect(v[key])
			}
		}
	}
	for _, value := range values {
		collect(value)
	}
	return screenshots, videos
}

func mediaKindForPath(path, format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "png", "jpg", "jpeg", "webp", "screenshot", "image":
		return "screenshot"
	case "mp4", "webm", "mov", "video":
		return "video"
	}
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".png", ".jpg", ".jpeg", ".webp":
		return "screenshot"
	case ".mp4", ".webm", ".mov":
		return "video"
	}
	return ""
}
