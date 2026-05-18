package vivero

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (a *App) QAPlan(previewID, scopeName string) (map[string]any, error) {
	return a.QAPlanWithTarget(previewID, scopeName, defaultArtifactTarget)
}

func (a *App) QAPlanWithTarget(previewID, scopeName, target string) (map[string]any, error) {
	target = normalizeArtifactTarget(target)
	p, err := a.getPreview(previewID)
	if err != nil {
		return nil, err
	}
	project, err := a.getProject(p.Project)
	if err != nil {
		return nil, err
	}
	cfg, err := projectConfigForPreview(project, p)
	if err != nil {
		return nil, err
	}
	agent := cfg.Agent
	scopes, selectedScope, err := selectedQAScopes(agent, scopeName)
	if err != nil {
		return nil, err
	}
	artifactDir := qaArtifactDir(a.Home, project.Path, previewID, selectedScope, agent.QA.ArtifactRoot)
	driver := qaDriver(agent.QA.Driver)

	scopePlans := []map[string]any{}
	for _, scope := range scopes {
		pages, err := qaPagesForScope(p, agent, scope, target)
		if err != nil {
			return nil, err
		}
		flows, err := qaFlowsForScope(p, agent, scope, target)
		if err != nil {
			return nil, err
		}
		scopePlans = append(scopePlans, map[string]any{
			"name":        qaScopeName(scope),
			"description": scope.Description,
			"pages":       pages,
			"flows":       flows,
			"checks":      qaChecksForScope(agent, scope),
		})
	}
	evidence, err := qaEvidencePlan(p.ID, selectedScope, p, agent, scopes, target, artifactDir)
	if err != nil {
		return nil, err
	}

	previewInfo := map[string]any{
		"id":      p.ID,
		"project": p.Project,
		"status":  p.Status,
	}
	if p.Profile != "" {
		previewInfo["profile"] = p.Profile
	}
	return map[string]any{
		"version": 1,
		"preview": previewInfo,
		"target":  target,
		"driver":  driver,
		"artifacts": map[string]any{
			"dir":        artifactDir,
			"runPath":    filepath.Join(artifactDir, "run.json"),
			"recordPath": filepath.Join(artifactDir, "record.json"),
			"videoDir":   filepath.Join(artifactDir, "videos"),
			"reportPath": filepath.Join(artifactDir, "report.md"),
		},
		"services":              qaServiceMapForTarget(p, target),
		"defaultPreviewService": defaultPreviewService(agent, p),
		"screenshotBreakpoints": agent.ScreenshotBreakpoints,
		"evidence":              evidence,
		"smokeTests":            agent.SmokeTests,
		"scopes":                scopePlans,
		"commands": map[string]any{
			"smoke":       fmt.Sprintf("vivero smoke %s --json --no-input --quiet", p.ID),
			"events":      fmt.Sprintf("vivero events %s --tail --json --no-input", p.ID),
			"report":      fmt.Sprintf("vivero qa report %s --scope %s --json --no-input", p.ID, selectedScope),
			"record":      fmt.Sprintf("vivero qa record %s --scope %s --json --no-input --quiet", p.ID, selectedScope),
			"screenshots": qaCommandWithTarget(fmt.Sprintf("vivero screenshot %s %s <path> --breakpoints --json --no-input --quiet", p.ID, defaultPreviewService(agent, p)), target),
		},
		"agentInstructions": []string{
			"Use this plan as the source of truth for preview URLs, services, scopes, and artifact paths.",
			"Screenshots and QA reports default to local preview URLs; pass --public only for explicit public-tunnel validation.",
			"Browser recordings use the local/proxy preview URL.",
			"Use evidence.screenshots.commands and evidence.recordings.commands for YAML-backed screenshot and video evidence instead of hardcoding project-specific matrices.",
			"Use Playwright for reproducible screenshots, recordings, traces, and CI-safe E2E evidence; use Chrome MCP or another browser driver only for exploratory/debug sessions.",
			"Run project smoke tests before or during QA, then drive the listed pages and flows in a real browser.",
			"Save screenshots, traces, and notes under artifacts.dir; generate the final markdown scaffold with `vivero qa report`.",
			"Do not hardcode project-specific URLs or selectors in the generic agent skill; put them in vivero.yml under agent.qa.",
		},
	}, nil
}

func (a *App) QAReport(previewID, scopeName, outPath string) (map[string]any, error) {
	return a.QAReportWithTarget(previewID, scopeName, defaultArtifactTarget, outPath)
}

func (a *App) QAReportWithTarget(previewID, scopeName, target, outPath string) (map[string]any, error) {
	plan, err := a.QAPlanWithTarget(previewID, scopeName, target)
	if err != nil {
		return nil, err
	}
	artifacts, _ := plan["artifacts"].(map[string]any)
	if outPath == "" {
		outPath, _ = artifacts["reportPath"].(string)
	}
	outPath = expandPath(outPath)
	if !filepath.IsAbs(outPath) {
		if dir, ok := artifacts["dir"].(string); ok && dir != "" {
			outPath = filepath.Join(dir, outPath)
		}
	}
	if err := ensureDir(filepath.Dir(outPath)); err != nil {
		return nil, err
	}
	content := renderQAReport(plan)
	if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "preview": previewID, "scope": scopeNameFromPlan(plan), "path": outPath, "bytes": len(content)}, nil
}

func (a *App) QARun(previewID, scopeName string, screenshots bool) (map[string]any, error) {
	return a.QARunWithTarget(previewID, scopeName, defaultArtifactTarget, screenshots)
}

func (a *App) QARunWithTarget(previewID, scopeName, target string, screenshots bool) (map[string]any, error) {
	plan, err := a.QAPlanWithTarget(previewID, scopeName, target)
	if err != nil {
		return nil, err
	}
	artifacts, _ := plan["artifacts"].(map[string]any)
	artifactDir, _ := artifacts["dir"].(string)
	if artifactDir != "" {
		if err := ensureDir(artifactDir); err != nil {
			return nil, err
		}
	}
	result := map[string]any{"ok": true, "plan": plan, "artifacts": artifacts}
	if smoke, err := a.Smoke(previewID, ""); err != nil {
		if strings.Contains(err.Error(), "no smoke tests matched") {
			result["smokeSkipped"] = true
			result["smokeSkipReason"] = err.Error()
		} else {
			result["ok"] = false
			result["smokeError"] = err.Error()
		}
	} else {
		result["smoke"] = smoke
		if smoke["ok"] != true {
			result["ok"] = false
		}
	}
	if screenshots {
		shots, err := a.captureQAPageScreenshots(previewID, plan)
		if err != nil {
			result["ok"] = false
			result["screenshotError"] = err.Error()
		} else {
			result["screenshots"] = shots
		}
	}
	report, err := a.QAReportWithTarget(previewID, scopeName, target, "")
	if err != nil {
		result["ok"] = false
		result["reportError"] = err.Error()
	} else {
		result["report"] = report
	}
	if runPath, err := writeQARunResult(artifacts, result); err != nil {
		result["ok"] = false
		result["runArtifactError"] = err.Error()
	} else if runPath != "" {
		result["runPath"] = runPath
	}
	return result, nil
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
			key := service + "\x00" + path
			if seen[key] {
				continue
			}
			seen[key] = true
			for _, colorScheme := range colorSchemes {
				shot, err := a.ScreenshotWithOptions(previewID, service, ScreenshotOptions{Path: path, Target: target, ColorScheme: colorScheme, UseProjectBreakpoints: useProjectBreakpoints, OutputDir: outputDir})
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
	if err := ensureDir(filepath.Dir(runPath)); err != nil {
		return "", err
	}
	if err := writeIndentedJSONFile(runPath, result, 0o644); err != nil {
		return "", err
	}
	return runPath, nil
}

func selectedQAScopes(agent AgentConfig, scopeName string) ([]QAScope, string, error) {
	scopes := agent.QA.Scopes
	if len(scopes) == 0 {
		scope := defaultQAScope(agent)
		return []QAScope{scope}, scope.Name, nil
	}
	desired := strings.TrimSpace(scopeName)
	if desired == "" {
		desired = strings.TrimSpace(agent.QA.DefaultScope)
	}
	if desired == "" {
		desired = qaScopeName(scopes[0])
	}
	if desired == "all" {
		return scopes, "all", nil
	}
	for _, scope := range scopes {
		if qaScopeName(scope) == desired {
			return []QAScope{scope}, desired, nil
		}
	}
	return nil, "", fmt.Errorf("qa scope not found: %s (available: %s)", desired, strings.Join(qaScopeNames(scopes), ", "))
}

func defaultQAScope(agent AgentConfig) QAScope {
	pages := sortedAgentPageNames(agent.CommonPages)
	checks := []QACheck{}
	for _, smoke := range agent.SmokeTests {
		method := "http"
		desc := smoke.Path
		if smoke.Command != "" {
			method = "command"
			desc = smoke.Command
		}
		checks = append(checks, QACheck{Name: smoke.Name, Description: desc, Method: method, Category: "smoke"})
	}
	return QAScope{
		Name:        "default",
		Description: "Generated from agent.commonPages and agent.smokeTests.",
		Pages:       pages,
		Checks:      checks,
	}
}

func qaScopeNames(scopes []QAScope) []string {
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		out = append(out, qaScopeName(scope))
	}
	sort.Strings(out)
	return out
}

func qaScopeName(scope QAScope) string {
	if strings.TrimSpace(scope.Name) == "" {
		return "default"
	}
	return strings.TrimSpace(scope.Name)
}

func qaDriver(cfg QADriverConfig) map[string]any {
	evidence := strings.TrimSpace(cfg.Evidence)
	if evidence == "" {
		evidence = strings.TrimSpace(cfg.Preferred)
	}
	if evidence == "" {
		evidence = "playwright"
	}
	exploratory := strings.TrimSpace(cfg.Exploratory)
	if exploratory == "" {
		exploratory = "chrome-mcp"
	}
	preferred := strings.TrimSpace(cfg.Preferred)
	if preferred == "" {
		preferred = evidence
	}
	allowed := cfg.Allowed
	if len(allowed) == 0 {
		allowed = []string{"playwright", "browser", "chrome-mcp"}
	}
	return map[string]any{"preferred": preferred, "evidence": evidence, "exploratory": exploratory, "allowed": allowed, "notes": cfg.Notes}
}

func qaArtifactDir(home, projectPath, previewID, scope, root string) string {
	base := strings.TrimSpace(root)
	if base == "" {
		base = filepath.Join(home, "qa")
	} else {
		base = expandPath(base)
		if !filepath.IsAbs(base) {
			base = filepath.Join(projectPath, base)
		}
	}
	dir := filepath.Join(base, previewID)
	if scope != "" && scope != "default" && scope != "all" {
		dir = filepath.Join(dir, sanitizeScreenshotName(scope))
	}
	return dir
}

func qaEvidencePlan(previewID, scopeName string, p PreviewRecord, agent AgentConfig, scopes []QAScope, target, artifactDir string) (map[string]any, error) {
	screenshotColorSchemes := normalizeColorSchemes(agent.QA.Evidence.Screenshots.ColorSchemes)
	if err := validateColorSchemes(screenshotColorSchemes); err != nil {
		return nil, err
	}
	recordingColorSchemes := normalizeColorSchemes(agent.QA.Evidence.Recordings.ColorSchemes)
	if err := validateColorSchemes(recordingColorSchemes); err != nil {
		return nil, err
	}

	pages := []map[string]any{}
	seenPages := map[string]bool{}
	for _, scope := range scopes {
		resolved, err := qaPagesForScope(p, agent, scope, target)
		if err != nil {
			return nil, err
		}
		for _, page := range resolved {
			service := stringValue(page["service"])
			path := stringValue(page["path"])
			key := service + "\x00" + path
			if seenPages[key] {
				continue
			}
			seenPages[key] = true
			pages = append(pages, page)
		}
	}

	screenshotCommands := []map[string]any{}
	for _, page := range pages {
		service := stringValue(page["service"])
		path := stringValue(page["path"])
		for _, colorScheme := range screenshotColorSchemes {
			argv := []string{"vivero", "screenshot", previewID, service, path, "--breakpoints", "--json", "--no-input", "--quiet"}
			argv = appendArtifactTargetArgs(argv, target)
			if colorScheme != "" {
				argv = append(argv, "--color-scheme", colorScheme)
			}
			entry := map[string]any{
				"service":     service,
				"path":        path,
				"url":         stringValue(page["url"]),
				"breakpoints": agent.ScreenshotBreakpoints,
				"argv":        argv,
				"command":     shellJoin(argv),
			}
			if colorScheme != "" {
				entry["colorScheme"] = colorScheme
			}
			screenshotCommands = append(screenshotCommands, entry)
		}
	}

	recordingCommands := []map[string]any{}
	for _, colorScheme := range recordingColorSchemes {
		argv := []string{"vivero", "qa", "record", previewID, "--scope", scopeName, "--json", "--no-input", "--quiet"}
		if colorScheme != "" {
			argv = append(argv, "--color-scheme", colorScheme)
		}
		entry := map[string]any{"scope": scopeName, "argv": argv, "command": shellJoin(argv)}
		if colorScheme != "" {
			entry["colorScheme"] = colorScheme
		}
		recordingCommands = append(recordingCommands, entry)
	}

	return map[string]any{
		"screenshots": map[string]any{
			"colorSchemes": explicitColorSchemesForPlan(screenshotColorSchemes),
			"breakpoints":  agent.ScreenshotBreakpoints,
			"outputDir":    filepath.Join(artifactDir, "screenshots"),
			"commands":     screenshotCommands,
		},
		"recordings": map[string]any{
			"colorSchemes": explicitColorSchemesForPlan(recordingColorSchemes),
			"outputDir":    filepath.Join(artifactDir, "videos"),
			"commands":     recordingCommands,
		},
	}, nil
}

func explicitColorSchemesForPlan(colorSchemes []string) []string {
	out := []string{}
	for _, colorScheme := range normalizeColorSchemes(colorSchemes) {
		if colorScheme != "" {
			out = append(out, colorScheme)
		}
	}
	return out
}

func appendArtifactTargetArgs(argv []string, target string) []string {
	switch normalizeArtifactTarget(target) {
	case artifactTargetPublic:
		return append(argv, "--public")
	case artifactTargetOrigin:
		return append(argv, "--target", artifactTargetOrigin)
	default:
		return argv
	}
}

func shellJoin(args []string) string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, shellQuote(arg))
	}
	return strings.Join(out, " ")
}

func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	if !strings.ContainsAny(arg, " \t\n'\"\\$`!*?[]{}()&;|<>") {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

func qaServiceMap(p PreviewRecord) map[string]any {
	return qaServiceMapForTarget(p, defaultArtifactTarget)
}

func qaServiceMapForTarget(p PreviewRecord, target string) map[string]any {
	target = normalizeArtifactTarget(target)
	out := map[string]any{}
	for _, name := range sortedMapKeys(p.Services) {
		svc := p.Services[name]
		out[name] = map[string]any{
			"status":    svc.Status,
			"url":       serviceBaseURLForTarget(svc, target),
			"localUrl":  serviceBaseURLForTarget(svc, defaultArtifactTarget),
			"publicUrl": serviceBaseURLForTarget(svc, artifactTargetPublic),
			"proxyUrl":  svc.ProxyURL,
			"originUrl": svc.OriginURL,
			"ports":     svc.Ports,
			"source":    svc.Source,
		}
	}
	return out
}

func qaCommandWithTarget(command, target string) string {
	switch normalizeArtifactTarget(target) {
	case artifactTargetPublic:
		return command + " --public"
	case artifactTargetOrigin:
		return command + " --target origin"
	default:
		return command
	}
}

func defaultPreviewService(agent AgentConfig, p PreviewRecord) string {
	if agent.DefaultPreviewService != "" {
		return agent.DefaultPreviewService
	}
	keys := sortedMapKeys(p.Services)
	for _, name := range keys {
		if serviceBaseURLForTarget(p.Services[name], defaultArtifactTarget) != "" {
			return name
		}
	}
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

func sortedAgentPageNames(pages map[string]AgentPage) []string {
	return sortedMapKeys(pages)
}

func qaPagesForScope(p PreviewRecord, agent AgentConfig, scope QAScope, target string) ([]map[string]any, error) {
	pageRefs := scope.Pages
	if len(pageRefs) == 0 {
		pageRefs = sortedAgentPageNames(agent.CommonPages)
	}
	out := []map[string]any{}
	for _, ref := range pageRefs {
		page, err := resolveQAPageForTarget(p, agent, ref, defaultPreviewService(agent, p), target)
		if err != nil {
			return nil, err
		}
		out = append(out, page)
	}
	return out, nil
}

func qaFlowsForScope(p PreviewRecord, agent AgentConfig, scope QAScope, target string) ([]map[string]any, error) {
	out := []map[string]any{}
	for _, flow := range scope.Flows {
		service := flow.Service
		if service == "" {
			service = defaultPreviewService(agent, p)
		}
		flowMap := map[string]any{
			"name":        flow.Name,
			"description": flow.Description,
			"service":     service,
			"steps":       qaStepMaps(p, agent, service, flow.Steps, target),
		}
		if flow.Start != "" {
			page, err := resolveQAPageForTarget(p, agent, flow.Start, service, target)
			if err != nil {
				return nil, err
			}
			flowMap["start"] = page
		}
		out = append(out, flowMap)
	}
	return out, nil
}

func qaStepMaps(p PreviewRecord, agent AgentConfig, fallbackService string, steps []QAStep, target string) []map[string]any {
	out := []map[string]any{}
	for _, step := range steps {
		m := map[string]any{}
		if step.Visit != "" {
			m["visit"] = step.Visit
			if page, err := resolveQAPageForTarget(p, agent, step.Visit, fallbackService, target); err == nil {
				m["url"] = page["url"]
				m["service"] = page["service"]
				m["path"] = page["path"]
			}
		}
		if step.Click != "" {
			m["click"] = step.Click
		}
		if step.Fill != "" {
			m["fill"] = step.Fill
			m["value"] = step.Value
		}
		if step.Press != "" {
			m["press"] = step.Press
		}
		if step.ExpectText != "" {
			m["expectText"] = step.ExpectText
		}
		if step.ExpectURL != "" {
			m["expectUrl"] = step.ExpectURL
		}
		if step.Screenshot != "" {
			m["screenshot"] = step.Screenshot
		}
		if step.Note != "" {
			m["note"] = step.Note
		}
		out = append(out, m)
	}
	return out
}

func qaChecksForScope(_ AgentConfig, scope QAScope) []map[string]any {
	checks := []QACheck{}
	checks = append(checks, scope.Checks...)
	out := []map[string]any{}
	for _, check := range checks {
		m := map[string]any{"name": check.Name}
		if check.Description != "" {
			m["description"] = check.Description
		}
		if check.Category != "" {
			m["category"] = check.Category
		}
		if check.Severity != "" {
			m["severity"] = check.Severity
		}
		if check.Method != "" {
			m["method"] = check.Method
		}
		out = append(out, m)
	}
	return out
}

func resolveQAPage(p PreviewRecord, agent AgentConfig, ref, fallbackService string) (map[string]any, error) {
	return resolveQAPageForTarget(p, agent, ref, fallbackService, defaultArtifactTarget)
}

func resolveQAPageForTarget(p PreviewRecord, agent AgentConfig, ref, fallbackService, target string) (map[string]any, error) {
	name := strings.TrimSpace(ref)
	service := fallbackService
	path := name
	if page, ok := agent.CommonPages[name]; ok {
		if page.Service != "" {
			service = page.Service
		}
		path = page.Path
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
	return map[string]any{"name": name, "service": service, "path": path, "url": url}, nil
}

func qaURLForServicePath(p PreviewRecord, service, path string) (string, error) {
	return qaURLForServicePathWithTarget(p, service, path, defaultArtifactTarget)
}

func qaURLForServicePathWithTarget(p PreviewRecord, service, path, target string) (string, error) {
	if service == "" {
		return "", fmt.Errorf("qa page %s has no service and preview has no default service", path)
	}
	svc, ok := p.Services[service]
	if !ok {
		return "", fmt.Errorf("qa page references unknown service %s", service)
	}
	base := serviceBaseURLForTarget(svc, target)
	if base == "" {
		return "", fmt.Errorf("service %s has no %s URL", service, normalizeArtifactTarget(target))
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/"), nil
}

func serviceBaseURLForTarget(svc PreviewService, target string) string {
	origin := serviceOriginURL(svc)
	switch normalizeArtifactTarget(target) {
	case artifactTargetPublic:
		if svc.URL != "" {
			return svc.URL
		}
		if svc.ProxyURL != "" {
			return svc.ProxyURL
		}
		return origin
	case artifactTargetOrigin:
		if origin != "" {
			return origin
		}
		if svc.ProxyURL != "" {
			return svc.ProxyURL
		}
		return svc.URL
	default:
		if svc.ProxyURL != "" {
			return svc.ProxyURL
		}
		if origin != "" {
			return origin
		}
		return svc.URL
	}
}

func serviceOriginURL(svc PreviewService) string {
	if svc.OriginURL != "" {
		return svc.OriginURL
	}
	if primary, ok := primaryPreviewPort(svc.Ports); ok {
		return primary.URL
	}
	return ""
}

func scopeNameFromPlan(plan map[string]any) string {
	scopes, _ := plan["scopes"].([]map[string]any)
	if len(scopes) == 1 {
		if name, ok := scopes[0]["name"].(string); ok {
			return name
		}
	}
	return "all"
}

func renderQAReport(plan map[string]any) string {
	var b strings.Builder
	preview, _ := plan["preview"].(map[string]any)
	artifacts, _ := plan["artifacts"].(map[string]any)
	driver, _ := plan["driver"].(map[string]any)
	previewID, _ := preview["id"].(string)
	project, _ := preview["project"].(string)
	status, _ := preview["status"].(string)
	artifactDir, _ := artifacts["dir"].(string)
	target := stringValue(plan["target"])
	preferredDriver, _ := driver["preferred"].(string)

	fmt.Fprintf(&b, "# QA Report: %s\n\n", previewID)
	fmt.Fprintf(&b, "- Project: `%s`\n", project)
	fmt.Fprintf(&b, "- Preview status: `%s`\n", status)
	if target != "" {
		fmt.Fprintf(&b, "- Evidence target: `%s`\n", target)
	}
	fmt.Fprintf(&b, "- Preferred driver: `%s`\n", preferredDriver)
	fmt.Fprintf(&b, "- Artifact directory: `%s`\n\n", artifactDir)

	b.WriteString("## Summary\n\n")
	b.WriteString("- Status: pending\n")
	b.WriteString("- Critical issues: 0\n")
	b.WriteString("- High issues: 0\n")
	b.WriteString("- Medium issues: 0\n")
	b.WriteString("- Low issues: 0\n\n")

	scopes, _ := plan["scopes"].([]map[string]any)
	for _, scope := range scopes {
		name, _ := scope["name"].(string)
		description, _ := scope["description"].(string)
		fmt.Fprintf(&b, "## Scope: %s\n\n", name)
		if description != "" {
			fmt.Fprintf(&b, "%s\n\n", description)
		}
		if pages, ok := scope["pages"].([]map[string]any); ok && len(pages) > 0 {
			b.WriteString("### Pages\n\n")
			for _, page := range pages {
				fmt.Fprintf(&b, "- [ ] `%s` %s\n", stringValue(page["service"]), stringValue(page["url"]))
			}
			b.WriteByte('\n')
		}
		if flows, ok := scope["flows"].([]map[string]any); ok && len(flows) > 0 {
			b.WriteString("### Flows\n\n")
			for _, flow := range flows {
				fmt.Fprintf(&b, "- [ ] %s", stringValue(flow["name"]))
				if desc := stringValue(flow["description"]); desc != "" {
					fmt.Fprintf(&b, " — %s", desc)
				}
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
		}
		if checks, ok := scope["checks"].([]map[string]any); ok && len(checks) > 0 {
			b.WriteString("### Checks\n\n")
			for _, check := range checks {
				fmt.Fprintf(&b, "- [ ] %s", stringValue(check["name"]))
				if method := stringValue(check["method"]); method != "" {
					fmt.Fprintf(&b, " (`%s`)", method)
				}
				if desc := stringValue(check["description"]); desc != "" {
					fmt.Fprintf(&b, " — %s", desc)
				}
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
		}
	}

	b.WriteString("## Findings\n\n")
	b.WriteString("Add issues here with severity, URL, repro steps, expected/actual behavior, console/network evidence, and screenshot paths.\n\n")
	b.WriteString("## Evidence\n\n")
	b.WriteString("Store screenshots, recordings, traces, and notes in the artifact directory above.\n")
	return b.String()
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func qaRunHuman(v map[string]any) string {
	status := "failed"
	if v["ok"] == true {
		status = "ok"
	}
	reportPath := ""
	if report, ok := v["report"].(map[string]any); ok {
		reportPath = stringValue(report["path"])
	}
	if reportPath == "" {
		return "qa run " + status
	}
	return "qa run " + status + "\nreport: " + reportPath
}

func qaRecordHuman(v map[string]any) string {
	status := "failed"
	if v["ok"] == true {
		status = "ok"
	}
	recordPath := stringValue(v["recordPath"])
	if recordPath == "" {
		return "qa record " + status
	}
	return "qa record " + status + "\nrecord: " + recordPath
}

func qaPlanHuman(v map[string]any) string {
	preview, _ := v["preview"].(map[string]any)
	artifacts, _ := v["artifacts"].(map[string]any)
	scopes, _ := v["scopes"].([]map[string]any)
	pageCount := 0
	flowCount := 0
	checkCount := 0
	for _, scope := range scopes {
		if pages, ok := scope["pages"].([]map[string]any); ok {
			pageCount += len(pages)
		}
		if flows, ok := scope["flows"].([]map[string]any); ok {
			flowCount += len(flows)
		}
		if checks, ok := scope["checks"].([]map[string]any); ok {
			checkCount += len(checks)
		}
	}
	return fmt.Sprintf("qa plan %s: %d pages, %d flows, %d checks\nartifacts: %s\nnext: capture reproducible evidence with Playwright; use Chrome MCP/browser only for exploratory debugging, then run `vivero qa report %s`", stringValue(preview["id"]), pageCount, flowCount, checkCount, stringValue(artifacts["dir"]), stringValue(preview["id"]))
}
