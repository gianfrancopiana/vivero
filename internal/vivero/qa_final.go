package vivero

import (
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

	if diag, err := a.DiagnoseStartup(previewID); err != nil {
		result["diagnosisError"] = err.Error()
	} else {
		result["diagnosis"] = diag
	}

	if finalPath, err := writeQAFinalResult(artifacts); err != nil {
		result["ok"] = false
		result["finalArtifactError"] = err.Error()
	} else if finalPath != "" {
		result["finalPath"] = finalPath
		result["proof"] = qaFinalProof(result)
		if err := writeIndentedJSONFile(finalPath, result, 0o644); err != nil {
			result["ok"] = false
			result["finalArtifactError"] = err.Error()
			delete(result, "finalPath")
		}
	}
	result["proof"] = qaFinalProof(result)
	return result, nil
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
		"screenshots":   qaFinalScreenshotPaths(run["screenshots"]),
		"videos":        qaFinalVideoPaths(record["videos"]),
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

func qaFinalScreenshotPaths(raw any) []string {
	paths := []string{}
	seen := map[string]bool{}
	var collect func(any)
	collect = func(value any) {
		switch screenshots := value.(type) {
		case []map[string]any:
			for _, screenshot := range screenshots {
				collect(screenshot)
			}
		case []any:
			for _, item := range screenshots {
				collect(item)
			}
		case map[string]any:
			if path := stringValue(screenshots["path"]); path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
			if nested := screenshots["screenshots"]; nested != nil {
				collect(nested)
			}
		}
	}
	collect(raw)
	return paths
}

func qaFinalVideoPaths(raw any) []string {
	paths := []string{}
	if videos, ok := raw.([]any); ok {
		for _, item := range videos {
			if video, ok := item.(map[string]any); ok {
				if path := stringValue(video["path"]); path != "" {
					paths = append(paths, path)
				}
			}
		}
	}
	return paths
}

func writeQAFinalResult(artifacts map[string]any) (string, error) {
	finalPath := stringValue(artifacts["finalPath"])
	if finalPath == "" {
		return "", nil
	}
	finalPath = expandPath(finalPath)
	if !filepath.IsAbs(finalPath) {
		if dir := stringValue(artifacts["dir"]); dir != "" {
			finalPath = filepath.Join(dir, finalPath)
		}
	}
	finalPath = nextAvailableArtifactPath(finalPath)
	if err := ensureDir(filepath.Dir(finalPath)); err != nil {
		return "", err
	}
	return finalPath, nil
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
	runPath := stringValue(artifacts["runPath"])
	if runPath == "" {
		return "", nil
	}
	runPath = expandPath(runPath)
	if !filepath.IsAbs(runPath) {
		if dir := stringValue(artifacts["dir"]); dir != "" {
			runPath = filepath.Join(dir, runPath)
		}
	}
	runPath = nextAvailableArtifactPath(runPath)
	if err := ensureDir(filepath.Dir(runPath)); err != nil {
		return "", err
	}
	if err := writeIndentedJSONFile(runPath, result, 0o644); err != nil {
		return "", err
	}
	return runPath, nil
}
