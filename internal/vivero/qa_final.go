package vivero

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (a *App) QAFinal(previewID string, opts QAFinalOptions) (map[string]any, error) {
	target := normalizeArtifactTarget(opts.Target)
	plan, err := a.QAPlanWithTarget(previewID, opts.Scope, target)
	if err != nil {
		return nil, err
	}
	artifacts, _ := plan["artifacts"].(map[string]any)
	result := map[string]any{
		"ok":        true,
		"preview":   previewID,
		"scope":     scopeNameFromPlan(plan),
		"target":    stringValue(plan["target"]),
		"plan":      plan,
		"artifacts": artifacts,
	}

	run, err := a.QARunWithTarget(previewID, opts.Scope, target, !opts.SkipScreenshots)
	if err != nil {
		result["ok"] = false
		result["runError"] = err.Error()
	} else {
		result["run"] = run
		if run["ok"] != true {
			result["ok"] = false
		}
	}

	if opts.SkipRecord {
		result["recordSkipped"] = true
	} else {
		record, err := a.QARecord(previewID, qaFinalRecordOptionsFromPlan(plan, opts))
		if err != nil {
			result["ok"] = false
			result["recordError"] = err.Error()
		} else {
			result["record"] = record
			if record["ok"] != true {
				result["ok"] = false
			}
		}
	}

	if included, includeErrors := loadQAFinalIncludedEvidence(opts.IncludeEvidence); len(included) > 0 || len(includeErrors) > 0 {
		if len(included) > 0 {
			result["includedEvidence"] = included
		}
		if len(includeErrors) > 0 {
			result["ok"] = false
			result["includedEvidenceErrors"] = includeErrors
		}
	}

	if diag, err := a.DiagnoseStartup(previewID); err != nil {
		result["diagnosisError"] = err.Error()
	} else {
		result["diagnosis"] = diag
	}

	finalPath := ""
	if path, err := writeQAFinalResult(artifacts); err != nil {
		result["ok"] = false
		result["finalArtifactError"] = err.Error()
	} else if path != "" {
		finalPath = path
		result["finalPath"] = finalPath
	}
	setQAFinalProof(result)
	if finalPath != "" {
		if err := writeIndentedJSONFile(finalPath, result, 0o644); err != nil {
			result["ok"] = false
			result["finalArtifactError"] = err.Error()
			delete(result, "finalPath")
			setQAFinalProof(result)
		}
	}
	return result, nil
}

func loadQAFinalIncludedEvidence(paths []string) ([]map[string]any, []string) {
	included := []map[string]any{}
	errors := []string{}
	for _, rawPath := range paths {
		path := strings.TrimSpace(rawPath)
		if path == "" {
			continue
		}
		path = expandPath(path)
		b, err := os.ReadFile(path)
		if err != nil {
			errors = append(errors, path+": "+err.Error())
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(b, &payload); err != nil {
			errors = append(errors, path+": "+err.Error())
			continue
		}
		payload["sourcePath"] = path
		included = append(included, payload)
	}
	return included, errors
}

func setQAFinalProof(result map[string]any) {
	proof := qaFinalProof(result)
	if proof["mediaOk"] == false {
		result["ok"] = false
		proof["ok"] = false
		result["mediaError"] = "one or more media artifacts are missing or empty"
	}
	result["proof"] = proof
}

func qaFinalRecordOptionsFromPlan(plan map[string]any, opts QAFinalOptions) QARecordOptions {
	rec := QARecordOptions{
		Scope:             opts.Scope,
		Target:            opts.Target,
		ColorScheme:       opts.ColorScheme,
		StorageState:      opts.StorageState,
		Width:             opts.Width,
		Height:            opts.Height,
		DeviceScaleFactor: opts.DeviceScaleFactor,
		Format:            opts.Format,
		SlowMoMS:          opts.SlowMoMS,
		WaitMS:            opts.WaitMS,
		WaitMSSet:         opts.WaitMSSet,
	}
	if rec.Scope == "" {
		rec.Scope = scopeNameFromPlan(plan)
	}
	if command := firstQARecordingCommand(plan); command != nil {
		if rec.ColorScheme == "" {
			rec.ColorScheme = stringValue(command["colorScheme"])
		}
		if rec.StorageState == "" {
			rec.StorageState = stringValue(command["storageState"])
		}
	}
	return rec
}

func firstQARecordingCommand(plan map[string]any) map[string]any {
	evidence, _ := plan["evidence"].(map[string]any)
	recordings, _ := evidence["recordings"].(map[string]any)
	if commands, ok := recordings["commands"].([]map[string]any); ok && len(commands) > 0 {
		return commands[0]
	}
	if commands, ok := recordings["commands"].([]any); ok && len(commands) > 0 {
		if command, ok := commands[0].(map[string]any); ok {
			return command
		}
	}
	return nil
}

func qaFinalProof(result map[string]any) map[string]any {
	plan, _ := result["plan"].(map[string]any)
	artifacts, _ := result["artifacts"].(map[string]any)
	run, _ := result["run"].(map[string]any)
	record, _ := result["record"].(map[string]any)
	screenshots, videos := artifactMediaPaths(run["screenshots"], record["videos"], result["includedEvidence"])
	media := qaFinalMediaEntries(screenshots, videos)
	proof := map[string]any{
		"preview":       stringValue(result["preview"]),
		"scope":         stringValue(result["scope"]),
		"target":        stringValue(result["target"]),
		"url":           qaFinalPrimaryURL(plan),
		"reportPath":    qaFinalReportPath(run, artifacts),
		"runPath":       stringValue(run["runPath"]),
		"recordPath":    qaFinalRecordPath(record, artifacts),
		"finalPath":     stringValue(result["finalPath"]),
		"videoDir":      stringValue(artifacts["videoDir"]),
		"screenshots":   screenshots,
		"videos":        videos,
		"media":         media,
		"mediaOk":       qaFinalMediaOK(media),
		"recordSkipped": result["recordSkipped"] == true,
		"ok":            result["ok"] == true,
	}
	if smoke, ok := run["smoke"].(map[string]any); ok {
		proof["smoke"] = smoke["ok"]
	} else if skipped, ok := run["smokeSkipped"].(bool); ok {
		proof["smokeSkipped"] = skipped
	}
	return proof
}

func qaFinalPrimaryURL(plan map[string]any) string {
	defaultService := stringValue(plan["defaultPreviewService"])
	services, _ := plan["services"].(map[string]any)
	if svc, ok := services[defaultService].(map[string]any); ok {
		return stringValue(svc["url"])
	}
	for _, name := range sortedMapKeysAny(services) {
		if svc, ok := services[name].(map[string]any); ok {
			if url := stringValue(svc["url"]); url != "" {
				return url
			}
		}
	}
	return ""
}

func sortedMapKeysAny(in map[string]any) []string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func qaFinalReportPath(run, artifacts map[string]any) string {
	if report, ok := run["report"].(map[string]any); ok {
		if path := stringValue(report["path"]); path != "" {
			return path
		}
	}
	return stringValue(artifacts["reportPath"])
}

func qaFinalRecordPath(record, artifacts map[string]any) string {
	if path := stringValue(record["recordPath"]); path != "" {
		return path
	}
	return stringValue(artifacts["recordPath"])
}

func qaFinalMediaEntries(screenshots, videos []string) []map[string]any {
	entries := []map[string]any{}
	appendEntry := func(kind, path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		entry := map[string]any{"kind": kind, "path": path, "exists": false, "bytes": int64(0), "deliverable": false}
		info, err := os.Stat(expandPath(path))
		if err != nil {
			entry["error"] = err.Error()
		} else if info.IsDir() {
			entry["error"] = "path is a directory"
		} else {
			entry["exists"] = true
			entry["bytes"] = info.Size()
			entry["deliverable"] = info.Size() > 0
		}
		entries = append(entries, entry)
	}
	for _, path := range screenshots {
		appendEntry("screenshot", path)
	}
	for _, path := range videos {
		appendEntry("video", path)
	}
	return entries
}

func qaFinalMediaOK(media []map[string]any) bool {
	for _, entry := range media {
		if entry["deliverable"] != true {
			return false
		}
	}
	return true
}

func writeQAFinalResult(artifacts map[string]any) (string, error) {
	return qaArtifactResultPath(artifacts, "finalPath")
}

func qaFinalHuman(v map[string]any) string {
	status := "failed"
	if v["ok"] == true {
		status = "ok"
	}
	proof, _ := v["proof"].(map[string]any)
	parts := []string{"qa final " + status}
	if url := stringValue(proof["url"]); url != "" {
		parts = append(parts, "url: "+url)
	}
	if reportPath := stringValue(proof["reportPath"]); reportPath != "" {
		parts = append(parts, "report: "+reportPath)
	}
	if finalPath := stringValue(v["finalPath"]); finalPath != "" {
		parts = append(parts, "final: "+finalPath)
	}
	if screenshots, _ := proof["screenshots"].([]string); len(screenshots) > 0 {
		for _, path := range screenshots {
			parts = append(parts, "screenshot: "+path)
		}
	}
	if videos, _ := proof["videos"].([]string); len(videos) > 0 {
		for _, path := range videos {
			parts = append(parts, "video: "+path)
		}
	}
	if proof["mediaOk"] == false {
		parts = append(parts, "media: missing or empty artifact")
	}
	return strings.Join(parts, "\n")
}

func (a *App) captureQAPageScreenshots(previewID string, plan map[string]any) ([]map[string]any, error) {
	artifacts, _ := plan["artifacts"].(map[string]any)
	artifactDir, _ := artifacts["dir"].(string)
	outputDir := ""
	if artifactDir != "" {
		outputDir = filepath.Join(artifactDir, "screenshots")
	}
	target := stringValue(plan["target"])
	if target == "" {
		target = defaultArtifactTarget
	}
	breakpoints, _ := plan["screenshotBreakpoints"].([]ScreenshotBreakpoint)
	useProjectBreakpoints := len(breakpoints) > 0
	colorSchemes := qaScreenshotColorSchemesFromPlan(plan)
	scopes, _ := plan["scopes"].([]map[string]any)
	seen := map[string]bool{}
	out := []map[string]any{}
	for _, scope := range scopes {
		pages, _ := scope["pages"].([]map[string]any)
		for _, page := range pages {
			service := stringValue(page["service"])
			path := stringValue(page["path"])
			if service == "" || path == "" {
				continue
			}
			key := service + "\x00" + path + "\x00" + stringValue(page["storageState"])
			if seen[key] {
				continue
			}
			seen[key] = true
			for _, colorScheme := range colorSchemes {
				shot, err := a.ScreenshotWithOptions(previewID, service, ScreenshotOptions{Path: path, Target: target, ColorScheme: colorScheme, StorageState: stringValue(page["storageState"]), UseProjectBreakpoints: useProjectBreakpoints, OutputDir: outputDir})
				if err != nil {
					return out, err
				}
				out = append(out, shot)
			}
		}
	}
	return out, nil
}

func qaScreenshotColorSchemesFromPlan(plan map[string]any) []string {
	evidence, _ := plan["evidence"].(map[string]any)
	screenshots, _ := evidence["screenshots"].(map[string]any)
	if colorSchemes, ok := screenshots["colorSchemes"].([]string); ok {
		return normalizeColorSchemes(colorSchemes)
	}
	if raw, ok := screenshots["colorSchemes"].([]any); ok {
		values := make([]string, 0, len(raw))
		for _, value := range raw {
			if s, ok := value.(string); ok {
				values = append(values, s)
			}
		}
		return normalizeColorSchemes(values)
	}
	return []string{""}
}

func writeQARunResult(artifacts map[string]any, result map[string]any) (string, error) {
	runPath, err := qaArtifactResultPath(artifacts, "runPath")
	if err != nil || runPath == "" {
		return runPath, err
	}
	if err := writeIndentedJSONFile(runPath, result, 0o644); err != nil {
		return "", err
	}
	return runPath, nil
}

func qaArtifactResultPath(artifacts map[string]any, key string) (string, error) {
	path := expandPath(stringValue(artifacts[key]))
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		if dir := stringValue(artifacts["dir"]); dir != "" {
			path = filepath.Join(dir, path)
		}
	}
	path = nextAvailableArtifactPath(path)
	return path, ensureDir(filepath.Dir(path))
}
