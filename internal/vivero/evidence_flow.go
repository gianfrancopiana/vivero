package vivero

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type EvidenceFlowOptions struct {
	StepsFile            string  `json:"stepsFile,omitempty"`
	Target               string  `json:"target,omitempty"`
	OutputDir            string  `json:"outputDir,omitempty"`
	Format               string  `json:"format,omitempty"`
	StorageState         string  `json:"storageState,omitempty"`
	Width                int     `json:"width,omitempty"`
	Height               int     `json:"height,omitempty"`
	DeviceScaleFactor    float64 `json:"deviceScaleFactor,omitempty"`
	ColorScheme          string  `json:"colorScheme,omitempty"`
	SlowMoMS             int     `json:"slowMoMs,omitempty"`
	WaitMS               int     `json:"waitMs,omitempty"`
	DryRun               bool    `json:"dryRun,omitempty"`
	PrintScript          bool    `json:"printScript,omitempty"`
	Video                bool    `json:"video,omitempty"`
	VideoSet             bool    `json:"-"`
	Screenshots          bool    `json:"screenshots,omitempty"`
	ScreenshotsSet       bool    `json:"-"`
	Console              bool    `json:"console,omitempty"`
	ConsoleSet           bool    `json:"-"`
	Network              bool    `json:"network,omitempty"`
	NetworkSet           bool    `json:"-"`
	WidthSet             bool    `json:"-"`
	HeightSet            bool    `json:"-"`
	DeviceScaleFactorSet bool    `json:"-"`
	ColorSchemeSet       bool    `json:"-"`
	StorageStateSet      bool    `json:"-"`
	WaitMSSet            bool    `json:"-"`
}

func normalizeEvidenceFlowOptions(opts EvidenceFlowOptions) EvidenceFlowOptions {
	opts.Target = normalizeArtifactTarget(opts.Target)
	opts.ColorScheme = normalizeColorScheme(opts.ColorScheme)
	if opts.StorageState != "" {
		opts.StorageState = expandPath(opts.StorageState)
	}
	if opts.Width == 0 {
		opts.Width = defaultScreenshotWidth
	}
	if opts.Height == 0 {
		opts.Height = defaultScreenshotHeight
	}
	if opts.DeviceScaleFactor == 0 {
		opts.DeviceScaleFactor = defaultDeviceScaleFactor
	}
	if opts.Format == "" {
		opts.Format = "mp4"
	} else {
		opts.Format = strings.ToLower(strings.TrimSpace(opts.Format))
	}
	if !opts.WaitMSSet && opts.WaitMS == 0 {
		opts.WaitMS = 350
	}
	return opts
}

func (a *App) EvidenceFlow(previewID string, opts EvidenceFlowOptions) (map[string]any, error) {
	opts = normalizeEvidenceFlowOptions(opts)
	if strings.TrimSpace(opts.StepsFile) == "" {
		return nil, fmt.Errorf("--steps-file is required")
	}
	if err := validateColorScheme(opts.ColorScheme); err != nil {
		return nil, err
	}
	if opts.Format != "mp4" && opts.Format != "webm" {
		return nil, fmt.Errorf("evidence flow format must be mp4 or webm")
	}

	spec, err := readEvidenceFlowSpec(opts.StepsFile)
	if err != nil {
		return nil, err
	}
	p, err := a.getPreview(previewID)
	if err != nil {
		return nil, err
	}
	agent := AgentConfig{}
	if rec, err := a.getProject(p.Project); err == nil {
		agent = rec.Config.Agent
	}
	plan, err := a.evidenceFlowPlan(p, agent, spec, opts)
	if err != nil {
		return nil, err
	}
	if opts.DryRun {
		result := map[string]any{
			"ok":          true,
			"dryRun":      true,
			"preview":     previewID,
			"target":      opts.Target,
			"stepsFile":   expandPath(opts.StepsFile),
			"outputDir":   stringValue(plan["outputDir"]),
			"flow":        plan["flow"],
			"variants":    plan["variants"],
			"record":      plan["record"],
			"plan":        plan,
			"wouldLaunch": false,
		}
		if opts.PrintScript {
			result["script"] = evidenceFlowPlaywrightScript
		}
		return result, nil
	}

	outputDir := stringValue(plan["outputDir"])
	if outputDir == "" {
		return nil, fmt.Errorf("evidence flow output dir was not resolved")
	}
	if err := ensureDir(outputDir); err != nil {
		return nil, err
	}
	inputArtifacts, err := writeEvidenceFlowInputArtifacts(outputDir, spec, plan)
	if err != nil {
		return nil, err
	}

	runner := a.recordRunner()
	if _, err := runner.LookPath("npm"); err != nil {
		return nil, fmt.Errorf("npm/playwright not available for evidence flow: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "vivero-evidence-flow-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	inputPath := filepath.Join(tmpDir, "input.json")
	scriptPath := filepath.Join(tmpDir, "flow.js")
	payload := map[string]any{"plan": plan}
	if err := writeIndentedJSONFile(inputPath, payload, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(scriptPath, []byte(evidenceFlowPlaywrightScript), 0o644); err != nil {
		return nil, err
	}

	stdout, stderr, err := runner.Run("npm", "exec", "--yes", "--package", playwrightPackage(), "--", "sh", "-lc", `NODE_PATH="$(dirname "$(dirname "$(command -v playwright)")")" exec node "$1" "$2"`, "vivero-playwright", scriptPath, inputPath)
	if err != nil {
		return nil, fmt.Errorf("playwright evidence flow failed: %w: %s", err, strings.TrimSpace(string(stderr)+"\n"+string(stdout)))
	}
	var result map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &result); err != nil {
		return nil, fmt.Errorf("parse evidence flow output: %w: %s", err, strings.TrimSpace(string(stdout)))
	}
	result["preview"] = previewID
	result["target"] = opts.Target
	result["stepsFile"] = expandPath(opts.StepsFile)
	result["outputDir"] = outputDir
	result["plan"] = plan
	result["format"] = opts.Format
	result["inputArtifacts"] = inputArtifacts
	attachEvidenceFlowInputArtifacts(result, inputArtifacts)
	if opts.PrintScript {
		result["script"] = evidenceFlowPlaywrightScript
	}

	if opts.Format == "mp4" {
		if err := convertEvidenceFlowVideosToMP4(runner, result); err != nil {
			result["ok"] = false
			result["conversionError"] = err.Error()
			return result, err
		}
	}
	resultPath := filepath.Join(outputDir, "result.json")
	result["resultPath"] = resultPath
	if reportPath, err := writeEvidenceFlowReport(outputDir, result); err != nil {
		result["reportArtifactError"] = err.Error()
	} else if reportPath != "" {
		result["reportPath"] = reportPath
	}
	if err := writeIndentedJSONFile(resultPath, result, 0o644); err != nil {
		result["ok"] = false
		result["resultArtifactError"] = err.Error()
	}
	return result, nil
}

func readEvidenceFlowSpec(path string) (map[string]any, error) {
	path = expandPath(path)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read evidence flow steps file: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err == nil {
		return normalizeYAMLMap(raw), nil
	}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse evidence flow steps file as JSON or YAML: %w", err)
	}
	return normalizeYAMLMap(raw), nil
}

func normalizeYAMLMap(v any) map[string]any {
	out, _ := normalizeYAMLValue(v).(map[string]any)
	if out == nil {
		return map[string]any{}
	}
	return out
}

func normalizeYAMLValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, v := range value {
			out[k] = normalizeYAMLValue(v)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(value))
		for k, v := range value {
			out[fmt.Sprint(k)] = normalizeYAMLValue(v)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, v := range value {
			out[i] = normalizeYAMLValue(v)
		}
		return out
	default:
		return v
	}
}

func (a *App) evidenceFlowPlan(p PreviewRecord, agent AgentConfig, spec map[string]any, opts EvidenceFlowOptions) (map[string]any, error) {
	flowName := strings.TrimSpace(stringValue(spec["name"]))
	if flowName == "" {
		flowName = "flow"
	}
	defaultService := evidenceFlowDefaultService(p, agent)
	start, err := evidenceFlowResolveEndpoint(p, agent, spec["start"], defaultService, opts.Target)
	if err != nil {
		return nil, fmt.Errorf("resolve flow start: %w", err)
	}
	actions, err := evidenceFlowActions(p, agent, spec, defaultService, opts.Target)
	if err != nil {
		return nil, err
	}
	variants, err := evidenceFlowVariants(spec, opts)
	if err != nil {
		return nil, err
	}
	record := evidenceFlowRecord(spec, opts)
	outputDir := expandPath(opts.OutputDir)
	if outputDir == "" {
		outputDir = filepath.Join(a.Home, "evidence", "flows", safePathComponent(p.ID, "preview"), safePathComponent(flowName, "flow")+"-"+time.Now().UTC().Format("20060102T150405Z"))
	}
	flow := map[string]any{
		"name":        flowName,
		"description": stringValue(spec["description"]),
		"start":       start,
		"actions":     actions,
	}
	return map[string]any{
		"preview":   p.ID,
		"project":   p.Project,
		"target":    opts.Target,
		"outputDir": outputDir,
		"format":    opts.Format,
		"flow":      flow,
		"variants":  variants,
		"record":    record,
		"options": map[string]any{
			"slowMoMs": opts.SlowMoMS,
			"waitMs":   opts.WaitMS,
		},
	}, nil
}

func evidenceFlowDefaultService(p PreviewRecord, agent AgentConfig) string {
	if agent.DefaultPreviewService != "" {
		return agent.DefaultPreviewService
	}
	if len(p.Services) == 0 {
		return ""
	}
	keys := make([]string, 0, len(p.Services))
	for name := range p.Services {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys[0]
}

func evidenceFlowResolveEndpoint(p PreviewRecord, agent AgentConfig, raw any, fallbackService, target string) (map[string]any, error) {
	endpoint := map[string]any{}
	service := fallbackService
	path := "/"
	name := ""
	resolvedURL := ""

	switch value := raw.(type) {
	case nil:
	case string:
		text := strings.TrimSpace(value)
		if text != "" {
			if evidenceFlowAbsoluteURL(text) {
				resolvedURL = text
			} else if page, ok := agent.CommonPages[text]; ok {
				name = text
				if page.Service != "" {
					service = page.Service
				}
				if page.Path != "" {
					path = page.Path
				}
			} else {
				path = text
			}
		}
	default:
		if m, ok := evidenceFlowMap(value); ok {
			name = stringValue(m["name"])
			if pageName := firstStringValue(m, "page", "commonPage"); pageName != "" {
				if page, ok := agent.CommonPages[pageName]; ok {
					name = pageName
					if page.Service != "" {
						service = page.Service
					}
					if page.Path != "" {
						path = page.Path
					}
				} else {
					return nil, fmt.Errorf("unknown common page %q", pageName)
				}
			}
			if s := stringValue(m["service"]); s != "" {
				service = s
			}
			if p := firstStringValue(m, "path", "visit", "goto"); p != "" {
				path = p
			}
			if u := stringValue(m["url"]); u != "" {
				resolvedURL = u
			}
		} else {
			return nil, fmt.Errorf("endpoint must be string or object")
		}
	}

	if resolvedURL == "" {
		if service == "" {
			return nil, fmt.Errorf("no service specified and preview has no default service")
		}
		if path == "" {
			path = "/"
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		url, err := qaURLForServicePathWithTarget(p, service, path, target)
		if err != nil {
			return nil, err
		}
		resolvedURL = url
	}
	endpoint["url"] = resolvedURL
	if service != "" {
		endpoint["service"] = service
	}
	if path != "" {
		endpoint["path"] = path
	}
	if name != "" {
		endpoint["name"] = name
	}
	return endpoint, nil
}

func evidenceFlowActions(p PreviewRecord, agent AgentConfig, spec map[string]any, fallbackService, target string) ([]any, error) {
	rawActions, ok := evidenceFlowSlice(spec["actions"])
	if !ok {
		rawActions, _ = evidenceFlowSlice(spec["steps"])
	}
	actions := make([]any, 0, len(rawActions))
	for i, raw := range rawActions {
		m, ok := evidenceFlowMap(raw)
		if !ok {
			return nil, fmt.Errorf("flow action %d must be an object", i+1)
		}
		action := evidenceFlowCopyMap(m)
		for _, key := range []string{"visit", "goto"} {
			if rawEndpoint, exists := action[key]; exists {
				endpoint, err := evidenceFlowResolveEndpoint(p, agent, rawEndpoint, fallbackService, target)
				if err != nil {
					return nil, fmt.Errorf("resolve action %d %s: %w", i+1, key, err)
				}
				action[key] = endpoint
			}
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func evidenceFlowVariants(spec map[string]any, opts EvidenceFlowOptions) ([]map[string]any, error) {
	rawVariants, _ := evidenceFlowSlice(spec["variants"])
	if len(rawVariants) == 0 {
		rawVariants = []any{map[string]any{"name": "default"}}
	}
	variants := make([]map[string]any, 0, len(rawVariants))
	for i, raw := range rawVariants {
		m, ok := evidenceFlowMap(raw)
		if !ok {
			return nil, fmt.Errorf("flow variant %d must be an object", i+1)
		}
		variant := evidenceFlowCopyMap(m)
		viewport := map[string]any{}
		if rawViewport, ok := evidenceFlowMap(variant["viewport"]); ok {
			viewport = evidenceFlowCopyMap(rawViewport)
		}
		width := evidenceFlowInt(firstAnyValue(viewport, "width"), 0)
		height := evidenceFlowInt(firstAnyValue(viewport, "height"), 0)
		if width == 0 {
			width = evidenceFlowInt(variant["width"], 0)
		}
		if height == 0 {
			height = evidenceFlowInt(variant["height"], 0)
		}
		if opts.WidthSet || width == 0 {
			width = opts.Width
		}
		if opts.HeightSet || height == 0 {
			height = opts.Height
		}
		if width <= 0 || height <= 0 {
			return nil, fmt.Errorf("flow variant %d has invalid viewport %dx%d", i+1, width, height)
		}
		viewport["width"] = width
		viewport["height"] = height
		variant["viewport"] = viewport
		delete(variant, "width")
		delete(variant, "height")

		dsf := evidenceFlowFloat(variant["deviceScaleFactor"], 0)
		if opts.DeviceScaleFactorSet || dsf == 0 {
			dsf = opts.DeviceScaleFactor
		}
		variant["deviceScaleFactor"] = dsf

		if opts.ColorSchemeSet {
			variant["colorScheme"] = opts.ColorScheme
		} else {
			variant["colorScheme"] = normalizeColorScheme(stringValue(variant["colorScheme"]))
		}
		if err := validateColorScheme(stringValue(variant["colorScheme"])); err != nil {
			return nil, fmt.Errorf("flow variant %d: %w", i+1, err)
		}
		if opts.StorageStateSet {
			variant["storageState"] = opts.StorageState
		} else if storageState := stringValue(variant["storageState"]); storageState != "" {
			variant["storageState"] = expandPath(storageState)
		}
		if _, ok := variant["isMobile"]; !ok {
			if _, ok := variant["mobile"]; ok {
				variant["isMobile"] = evidenceFlowBool(variant["mobile"], false)
			}
		}
		delete(variant, "mobile")
		if stringValue(variant["name"]) == "" {
			name := fmt.Sprintf("%dx%d", width, height)
			if color := stringValue(variant["colorScheme"]); color != "" {
				name += "-" + color
			}
			variant["name"] = name
		}
		variants = append(variants, variant)
	}
	return variants, nil
}

func evidenceFlowRecord(spec map[string]any, opts EvidenceFlowOptions) map[string]any {
	raw, _ := evidenceFlowMap(spec["record"])
	record := evidenceFlowCopyMap(raw)
	_, pointerSet := record["pointer"]
	video := evidenceFlowBool(record["video"], false)
	screenshots := evidenceFlowBool(record["screenshots"], true)
	console := evidenceFlowBool(record["console"], true)
	network := evidenceFlowBool(record["network"], false)
	pointer := evidenceFlowBool(record["pointer"], false)
	if opts.VideoSet {
		video = opts.Video
	}
	if opts.ScreenshotsSet {
		screenshots = opts.Screenshots
	}
	if opts.ConsoleSet {
		console = opts.Console
	}
	if opts.NetworkSet {
		network = opts.Network
	}
	if !pointerSet {
		pointer = video
	}
	record["video"] = video
	record["screenshots"] = screenshots
	record["console"] = console
	record["network"] = network
	record["pointer"] = pointer
	record["format"] = opts.Format
	return record
}

func writeEvidenceFlowInputArtifacts(outputDir string, spec, plan map[string]any) (map[string]any, error) {
	artifacts := map[string]any{
		"plan":   filepath.Join(outputDir, "plan.json"),
		"steps":  filepath.Join(outputDir, "steps.json"),
		"script": filepath.Join(outputDir, "playwright.js"),
	}
	if err := writeIndentedJSONFile(artifacts["plan"].(string), plan, 0o644); err != nil {
		return artifacts, fmt.Errorf("write evidence flow plan artifact: %w", err)
	}
	if err := writeIndentedJSONFile(artifacts["steps"].(string), spec, 0o644); err != nil {
		return artifacts, fmt.Errorf("write evidence flow steps artifact: %w", err)
	}
	if err := os.WriteFile(artifacts["script"].(string), []byte(evidenceFlowPlaywrightScript), 0o644); err != nil {
		return artifacts, fmt.Errorf("write evidence flow script artifact: %w", err)
	}
	return artifacts, nil
}

func attachEvidenceFlowInputArtifacts(result map[string]any, inputArtifacts map[string]any) {
	if len(inputArtifacts) == 0 {
		return
	}
	artifacts, ok := evidenceFlowMap(result["artifacts"])
	if !ok || artifacts == nil {
		artifacts = map[string]any{}
		result["artifacts"] = artifacts
	}
	artifacts["inputs"] = inputArtifacts
}

func convertEvidenceFlowVideosToMP4(runner qaRecordRunner, result map[string]any) error {
	videos := collectEvidenceFlowVideoMaps(result)
	if len(videos) == 0 {
		return nil
	}
	if _, err := runner.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not available for mp4 conversion: %w", err)
	}
	converted := map[string]string{}
	for _, video := range videos {
		webm := stringValue(video["path"])
		if webm == "" || strings.ToLower(filepath.Ext(webm)) != ".webm" {
			continue
		}
		mp4, ok := converted[webm]
		if !ok {
			mp4 = strings.TrimSuffix(webm, filepath.Ext(webm)) + ".mp4"
			b, err := runner.CombinedOutput("ffmpeg", "-y", "-i", webm, "-movflags", "+faststart", "-pix_fmt", "yuv420p", mp4)
			if err != nil {
				return fmt.Errorf("ffmpeg convert %s: %w: %s", webm, err, strings.TrimSpace(string(b)))
			}
			converted[webm] = mp4
		}
		video["sourcePath"] = webm
		video["path"] = mp4
		video["format"] = "mp4"
	}
	return nil
}

func collectEvidenceFlowVideoMaps(result map[string]any) []map[string]any {
	videos := []map[string]any{}
	add := func(m map[string]any) {
		if m != nil {
			videos = append(videos, m)
		}
	}
	if artifacts, ok := evidenceFlowMap(result["artifacts"]); ok {
		if rawVideos, ok := evidenceFlowSlice(artifacts["videos"]); ok {
			for _, raw := range rawVideos {
				if m, ok := evidenceFlowMap(raw); ok {
					add(m)
				}
			}
		}
	}
	if variants, ok := evidenceFlowSlice(result["variants"]); ok {
		for _, rawVariant := range variants {
			variant, ok := evidenceFlowMap(rawVariant)
			if !ok {
				continue
			}
			artifacts, _ := evidenceFlowMap(variant["artifacts"])
			if video, ok := evidenceFlowMap(artifacts["video"]); ok {
				add(video)
			}
		}
	}
	return videos
}

func writeEvidenceFlowReport(outputDir string, result map[string]any) (string, error) {
	if outputDir == "" {
		return "", nil
	}
	path := filepath.Join(outputDir, "report.md")
	var b strings.Builder
	status := "failed"
	if result["ok"] == true {
		status = "ok"
	}
	flow, _ := evidenceFlowMap(result["flow"])
	b.WriteString("# Vivero evidence flow\n\n")
	b.WriteString(fmt.Sprintf("- Status: %s\n", status))
	b.WriteString(fmt.Sprintf("- Preview: %s\n", stringValue(result["preview"])))
	b.WriteString(fmt.Sprintf("- Target: %s\n", stringValue(result["target"])))
	b.WriteString(fmt.Sprintf("- Flow: %s\n", stringValue(flow["name"])))
	b.WriteString(fmt.Sprintf("- Output: %s\n\n", outputDir))
	if inputs, ok := evidenceFlowMap(result["inputArtifacts"]); ok {
		b.WriteString("## Input artifacts\n\n")
		for _, key := range []string{"steps", "plan", "script"} {
			if path := stringValue(inputs[key]); path != "" {
				b.WriteString(fmt.Sprintf("- %s: `%s`\n", key, path))
			}
		}
		b.WriteString("\n")
	}
	if variants, ok := evidenceFlowSlice(result["variants"]); ok {
		b.WriteString("## Variants\n\n")
		for _, raw := range variants {
			variant, ok := evidenceFlowMap(raw)
			if !ok {
				continue
			}
			vstatus := "failed"
			if variant["ok"] == true {
				vstatus = "ok"
			}
			b.WriteString(fmt.Sprintf("- %s: %s\n", stringValue(variant["name"]), vstatus))
			if artifacts, ok := evidenceFlowMap(variant["artifacts"]); ok {
				if video, ok := evidenceFlowMap(artifacts["video"]); ok {
					if path := stringValue(video["path"]); path != "" {
						b.WriteString(fmt.Sprintf("  - video: `%s`\n", path))
					}
				}
				if screenshots, ok := evidenceFlowSlice(artifacts["screenshots"]); ok {
					for _, rawShot := range screenshots {
						if shot, ok := evidenceFlowMap(rawShot); ok {
							if path := stringValue(shot["path"]); path != "" {
								b.WriteString(fmt.Sprintf("  - screenshot: `%s`\n", path))
							}
						}
					}
				}
				if consolePath := stringValue(artifacts["console"]); consolePath != "" {
					b.WriteString(fmt.Sprintf("  - console: `%s`\n", consolePath))
				}
			}
		}
	}
	if err := atomicWriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func evidenceFlowHuman(v map[string]any) string {
	status := "failed"
	if v["ok"] == true {
		status = "ok"
	}
	lines := []string{"evidence flow " + status}
	if resultPath := stringValue(v["resultPath"]); resultPath != "" {
		lines = append(lines, "result: "+resultPath)
	}
	if reportPath := stringValue(v["reportPath"]); reportPath != "" {
		lines = append(lines, "report: "+reportPath)
	}
	if outputDir := stringValue(v["outputDir"]); outputDir != "" {
		lines = append(lines, "artifacts: "+outputDir)
	}
	return strings.Join(lines, "\n")
}

func evidenceFlowMap(v any) (map[string]any, bool) {
	switch value := v.(type) {
	case map[string]any:
		return value, true
	case map[any]any:
		return normalizeYAMLMap(value), true
	default:
		return nil, false
	}
}

func evidenceFlowSlice(v any) ([]any, bool) {
	switch value := v.(type) {
	case []any:
		return value, true
	case []map[string]any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func evidenceFlowCopyMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = normalizeYAMLValue(v)
	}
	return out
}

func firstStringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(m[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstAnyValue(m map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			return value
		}
	}
	return nil
}

func evidenceFlowBool(v any, fallback bool) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return fallback
}

func evidenceFlowInt(v any, fallback int) int {
	switch value := v.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	case json.Number:
		if n, err := value.Int64(); err == nil {
			return int(n)
		}
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &n); err == nil {
			return n
		}
	}
	return fallback
}

func evidenceFlowFloat(v any, fallback float64) float64 {
	switch value := v.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		if n, err := value.Float64(); err == nil {
			return n
		}
	case string:
		var n float64
		if _, err := fmt.Sscanf(strings.TrimSpace(value), "%f", &n); err == nil {
			return n
		}
	}
	return fallback
}

func evidenceFlowAbsoluteURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme != "" && u.Host != ""
}

const evidenceFlowPlaywrightScript = `
const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');

const input = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const plan = input.plan || {};
const flow = plan.flow || {};
const record = plan.record || {};
const outputDir = plan.outputDir;
const options = plan.options || {};

function safeName(value) {
  const text = String(value || 'unnamed').trim() || 'unnamed';
  return text.replace(/[^a-zA-Z0-9._-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 80) || 'unnamed';
}

function locatorFor(page, target) {
  if (typeof target === 'string') return page.locator(target).first();
  const exact = target.exact === true;
  if (target.selector) return page.locator(target.selector).first();
  if (target.role) {
    const opts = {};
    if (target.name) opts.name = target.name;
    if (target.exact !== undefined) opts.exact = exact;
    return page.getByRole(target.role, opts).first();
  }
  if (target.text) return page.getByText(target.text, { exact }).first();
  if (target.label) return page.getByLabel(target.label, { exact }).first();
  if (target.placeholder) return page.getByPlaceholder(target.placeholder, { exact }).first();
  if (target.testId) return page.getByTestId(target.testId).first();
  throw new Error('unsupported locator: ' + JSON.stringify(target));
}

function endpointURL(value) {
  if (!value) return '';
  if (typeof value === 'string') return value;
  return value.url || '';
}

function pointerEnabled() {
  return record.video === true && record.pointer !== false;
}

const pointerCSS = '#vivero-pointer{position:fixed;left:0;top:0;width:24px;height:24px;transform:translate(72px,72px);z-index:2147483647;pointer-events:none;transition:transform 180ms cubic-bezier(.2,.8,.2,1),opacity 120ms ease;opacity:.95}#vivero-pointer::before{content:"";position:absolute;left:0;top:0;width:0;height:0;border-left:14px solid #111827;border-top:0 solid transparent;border-bottom:20px solid transparent;filter:drop-shadow(0 0 1px white) drop-shadow(0 1px 2px rgba(0,0,0,.6))}#vivero-pointer::after{content:"";position:absolute;left:4px;top:3px;width:0;height:0;border-left:8px solid white;border-top:0 solid transparent;border-bottom:12px solid transparent}#vivero-pointer.vivero-click::before{border-left-color:#2563eb;filter:drop-shadow(0 0 1px white) drop-shadow(0 0 5px rgba(37,99,235,.7))}#vivero-pointer.vivero-click::after{border-left-color:white}';

async function ensurePointerOverlay(page) {
  if (!pointerEnabled()) return;
  await page.evaluate((css) => {
    if (!document.getElementById('vivero-pointer-style')) {
      const style = document.createElement('style');
      style.id = 'vivero-pointer-style';
      style.textContent = css;
      document.documentElement.appendChild(style);
    }
    if (!document.getElementById('vivero-pointer')) {
      const pointer = document.createElement('div');
      pointer.id = 'vivero-pointer';
      pointer.setAttribute('aria-hidden', 'true');
      document.documentElement.appendChild(pointer);
    }
  }, pointerCSS);
}

async function showPointerAt(page, x, y, pulse) {
  if (!pointerEnabled()) return;
  await ensurePointerOverlay(page);
  await page.evaluate(({ x, y, pulse }) => {
    const pointer = document.getElementById('vivero-pointer');
    if (!pointer) return;
    pointer.style.transform = 'translate(' + Math.round(x) + 'px,' + Math.round(y) + 'px)';
    pointer.classList.remove('vivero-click');
    if (pulse) {
      void pointer.offsetWidth;
      pointer.classList.add('vivero-click');
    }
  }, { x, y, pulse });
}

async function showPointerIdle(page) {
  if (!pointerEnabled()) return;
  const viewport = page.viewportSize() || { width: 1280, height: 800 };
  await showPointerAt(page, Math.min(96, viewport.width - 24), Math.min(96, viewport.height - 24), false);
}

async function captureScreenshot(page, options) {
  let previousVisibility = null;
  if (pointerEnabled()) {
    previousVisibility = await page.evaluate(() => {
      const pointer = document.getElementById('vivero-pointer');
      if (!pointer) return null;
      const visibility = pointer.style.visibility || '';
      pointer.style.visibility = 'hidden';
      return visibility;
    });
  }
  try {
    await page.screenshot(options);
  } finally {
    if (previousVisibility !== null) {
      await page.evaluate((previousVisibility) => {
        const pointer = document.getElementById('vivero-pointer');
        if (pointer) pointer.style.visibility = previousVisibility;
      }, previousVisibility);
    }
  }
}

async function locatorPoint(locator) {
  await locator.scrollIntoViewIfNeeded({ timeout: 10000 });
  const box = await locator.boundingBox();
  if (!box) return null;
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

function scrollDelta(value, viewport) {
  const base = viewport && viewport.height ? Math.max(200, Math.round(viewport.height * 0.8)) : 700;
  let dx = 0;
  let dy = base;
  const applyDirection = (direction, amount) => {
    const dir = String(direction || 'down').toLowerCase();
    const pixels = Number(amount || base);
    if (dir === 'up') { dx = 0; dy = -pixels; return; }
    if (dir === 'left') { dx = -pixels; dy = 0; return; }
    if (dir === 'right') { dx = pixels; dy = 0; return; }
    dx = 0; dy = pixels;
  };
  if (typeof value === 'number') {
    dy = value;
  } else if (typeof value === 'string') {
    applyDirection(value, base);
  } else if (value && typeof value === 'object') {
    if (value.x !== undefined || value.y !== undefined || value.dx !== undefined || value.dy !== undefined) {
      dx = Number(value.x !== undefined ? value.x : (value.dx || 0));
      dy = Number(value.y !== undefined ? value.y : (value.dy || 0));
    } else {
      applyDirection(value.direction || value.dir || 'down', value.pixels || value.amount || base);
    }
  }
  if (!Number.isFinite(dx)) dx = 0;
  if (!Number.isFinite(dy)) dy = base;
  return { dx, dy };
}

async function assertTextAbsent(page, text, timeoutMs) {
  const absentText = String(text);
  let timeout = Number(timeoutMs !== undefined ? timeoutMs : 10000);
  if (!Number.isFinite(timeout) || timeout < 0) timeout = 10000;
  const interval = 150;
  const deadline = Date.now() + timeout;
  while (true) {
    const count = await page.getByText(absentText).count();
    if (count > 0) throw new Error('expected text to stay absent: ' + absentText);
    const remaining = deadline - Date.now();
    if (remaining <= 0) return;
    await page.waitForTimeout(Math.min(interval, remaining));
  }
}

async function runAction(page, action, flowDir, steps, errors) {
  const stepOut = JSON.parse(JSON.stringify(action || {}));
  try {
    const visitURL = endpointURL(action.visit) || endpointURL(action.goto) || action.url || '';
    if (visitURL) {
      await page.goto(visitURL, { waitUntil: 'domcontentloaded' });
      await showPointerIdle(page);
      stepOut.action = action.goto ? 'goto' : 'visit';
      stepOut.currentUrl = page.url();
    }
    if (action.click) {
      const targetLocator = locatorFor(page, action.click);
      const point = await locatorPoint(targetLocator);
      if (point) {
        await showPointerAt(page, point.x, point.y, false);
        await page.waitForTimeout(Math.max(120, Math.min(320, options.slowMoMs || 160)));
      }
      await targetLocator.click({ timeout: action.timeoutMs || 10000 });
      if (point) {
        await showPointerAt(page, point.x, point.y, true);
        await page.waitForTimeout(420);
      }
      stepOut.action = 'click';
      stepOut.currentUrl = page.url();
    }
    if (action.fill) {
      const value = action.value !== undefined ? String(action.value) : String(action.fill.value || '');
      const target = typeof action.fill === 'object' ? action.fill : action.fill;
      const targetLocator = locatorFor(page, target);
      const point = await locatorPoint(targetLocator);
      if (point) await showPointerAt(page, point.x, point.y, false);
      await targetLocator.fill(value, { timeout: action.timeoutMs || 10000 });
      stepOut.action = 'fill';
    }
    if (action.press) {
      await page.keyboard.press(String(action.press));
      stepOut.action = 'press';
    }
    if (action.scroll !== undefined) {
      const delta = scrollDelta(action.scroll, page.viewportSize());
      const viewport = page.viewportSize() || { width: 1280, height: 800 };
      const pointerX = Math.max(32, viewport.width - 72);
      const pointerY = Math.max(32, Math.round(viewport.height * 0.62));
      await showPointerAt(page, pointerX, pointerY, false);
      await page.mouse.move(pointerX, pointerY);
      await page.waitForTimeout(Math.max(120, Math.min(320, options.slowMoMs || 160)));
      await page.mouse.wheel(delta.dx, delta.dy);
      stepOut.action = 'scroll';
      stepOut.scroll = { x: delta.dx, y: delta.dy };
      stepOut.scrollPosition = await page.evaluate(() => ({ x: window.scrollX, y: window.scrollY }));
    }
    if (action.waitForSelector) {
      await page.locator(action.waitForSelector).first().waitFor({ timeout: action.timeoutMs || 10000 });
      stepOut.waitForSelectorFound = true;
    }
    if (action.waitMs || action.wait) {
      const wait = Number(action.waitMs || action.wait || 0);
      if (wait > 0) await page.waitForTimeout(wait);
    }
    if (action.expectText) {
      await page.getByText(String(action.expectText)).first().waitFor({ timeout: action.timeoutMs || 10000 });
      stepOut.expectTextFound = true;
    }
    if (action.expectNoText) {
      const denied = String(action.expectNoText);
      await assertTextAbsent(page, denied, action.timeoutMs);
      stepOut.expectNoTextMatched = true;
    }
    if (action.expectSelector) {
      await page.locator(String(action.expectSelector)).first().waitFor({ timeout: action.timeoutMs || 10000 });
      stepOut.expectSelectorFound = true;
    }
    if (action.expectNoSelector) {
      await page.locator(String(action.expectNoSelector)).first().waitFor({ state: 'hidden', timeout: action.timeoutMs || 10000 });
      stepOut.expectNoSelectorMatched = true;
    }
    if (action.expectUrl) {
      const current = page.url();
      const expected = String(action.expectUrl);
      if (!current.includes(expected)) throw new Error('expected URL to contain ' + expected + ', got ' + current);
      stepOut.expectUrlMatched = true;
    }
    if (action.expectUrlNot) {
      const current = page.url();
      const denied = String(action.expectUrlNot);
      if (current.includes(denied)) throw new Error('expected URL not to contain ' + denied + ', got ' + current);
      stepOut.expectUrlNotMatched = true;
    }
    if (action.screenshot && record.screenshots !== false) {
      let shotName = action.screenshot;
      let fullPage = action.fullPage === true;
      if (typeof action.screenshot === 'object') {
        shotName = action.screenshot.name || action.screenshot.label || 'screenshot';
        fullPage = action.screenshot.fullPage === true;
      }
      const screenshotPath = path.join(flowDir, safeName(shotName) + '.png');
      await captureScreenshot(page, { path: screenshotPath, fullPage });
      stepOut.screenshotName = String(shotName || 'screenshot');
      stepOut.screenshotPath = screenshotPath;
    }
    if (options.waitMs) await page.waitForTimeout(options.waitMs);
    steps.push(stepOut);
  } catch (error) {
    stepOut.error = error && error.message ? error.message : String(error);
    stepOut.currentUrl = page.url();
    if (record.screenshots !== false) {
      const failureScreenshotPath = path.join(flowDir, safeName('failure-' + (steps.length + 1)) + '.png');
      try {
        await captureScreenshot(page, { path: failureScreenshotPath, fullPage: true });
        stepOut.failureScreenshotPath = failureScreenshotPath;
        stepOut.screenshotName = 'failure-' + (steps.length + 1);
        stepOut.screenshotPath = failureScreenshotPath;
      } catch (screenshotError) {
        stepOut.failureScreenshotError = screenshotError && screenshotError.message ? screenshotError.message : String(screenshotError);
      }
    }
    steps.push(stepOut);
    errors.push(stepOut.error);
  }
}

async function run() {
  fs.mkdirSync(outputDir, { recursive: true });
  const browser = await chromium.launch({ channel: 'chrome', headless: true, slowMo: options.slowMoMs || 0 });
  const variantsOut = [];
  const allScreenshots = [];
  const allVideos = [];
  const allConsole = [];
  const allNetwork = [];
  let ok = true;
  try {
    for (const variant of (plan.variants || [])) {
      const viewport = variant.viewport || { width: 1280, height: 800 };
      const variantName = safeName(variant.name || (viewport.width + 'x' + viewport.height));
      const flowDir = path.join(outputDir, variantName);
      fs.mkdirSync(flowDir, { recursive: true });
      const contextOptions = {
        viewport: { width: viewport.width || 1280, height: viewport.height || 800 },
        deviceScaleFactor: variant.deviceScaleFactor || 1,
        isMobile: variant.isMobile === true,
        colorScheme: variant.colorScheme || undefined,
        ignoreHTTPSErrors: true
      };
      if (variant.storageState) contextOptions.storageState = variant.storageState;
      if (record.video === true) {
        contextOptions.recordVideo = { dir: flowDir, size: contextOptions.viewport };
      }
      const context = await browser.newContext(contextOptions);
      const page = await context.newPage();
      const consoleMessages = [];
      const networkFailures = [];
      const pageErrors = [];
      page.on('pageerror', (error) => {
        const text = error && error.message ? error.message : String(error);
        pageErrors.push(text);
        if (record.console !== false) consoleMessages.push({ type: 'pageerror', text });
      });
      if (record.console !== false) {
        page.on('console', (msg) => consoleMessages.push({ type: msg.type(), text: msg.text(), location: msg.location() }));
      }
      if (record.network === true) {
        page.on('requestfailed', (request) => networkFailures.push({ url: request.url(), method: request.method(), failure: request.failure() }));
      }
      const steps = [];
      const errors = [];
      const startURL = endpointURL(flow.start);
      try {
        if (startURL) {
          await page.goto(startURL, { waitUntil: 'domcontentloaded' });
          await showPointerIdle(page);
          steps.push({ action: 'start', url: page.url() });
          if (options.waitMs) await page.waitForTimeout(options.waitMs);
        }
        for (const action of (flow.actions || [])) {
          await runAction(page, action, flowDir, steps, errors);
        }
      } catch (error) {
        errors.push(error && error.message ? error.message : String(error));
      }
      for (const pageError of pageErrors) {
        errors.push('uncaught page error: ' + pageError);
      }
      const video = page.video();
      await context.close();
      const artifacts = { dir: flowDir, screenshots: [] };
      for (const step of steps) {
        if (step.screenshotPath) {
          const shot = { name: step.screenshotName || step.action || 'screenshot', path: step.screenshotPath };
          artifacts.screenshots.push(shot);
          allScreenshots.push(shot);
        }
      }
      if (record.video === true && video) {
        const videoPath = await video.path();
        const videoArtifact = { path: videoPath, format: 'webm' };
        artifacts.video = videoArtifact;
        allVideos.push(videoArtifact);
      }
      if (record.console !== false) {
        const consolePath = path.join(flowDir, 'console.json');
        fs.writeFileSync(consolePath, JSON.stringify(consoleMessages, null, 2));
        artifacts.console = consolePath;
        allConsole.push(consolePath);
      }
      if (record.network === true) {
        const networkPath = path.join(flowDir, 'network.json');
        fs.writeFileSync(networkPath, JSON.stringify(networkFailures, null, 2));
        artifacts.network = networkPath;
        allNetwork.push(networkPath);
      }
      const variantOK = errors.length === 0;
      if (!variantOK) ok = false;
      variantsOut.push({
        name: variant.name || variantName,
        ok: variantOK,
        viewport: contextOptions.viewport,
        deviceScaleFactor: contextOptions.deviceScaleFactor,
        isMobile: contextOptions.isMobile,
        colorScheme: variant.colorScheme || '',
        storageState: variant.storageState || '',
        startUrl: startURL,
        artifacts,
        steps,
        errors
      });
    }
  } finally {
    await browser.close();
  }
  process.stdout.write(JSON.stringify({
    ok,
    preview: plan.preview,
    target: plan.target,
    outputDir,
    flow: { name: flow.name || 'flow', description: flow.description || '', start: flow.start || {} },
    variants: variantsOut,
    artifacts: { outputDir, screenshots: allScreenshots, videos: allVideos, console: allConsole, network: allNetwork }
  }, null, 2));
}

run().catch((error) => {
  console.error(error && error.stack ? error.stack : error);
  process.exit(1);
});
`
