package vivero

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

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

func qaArtifactDir(home, projectPath, previewID, scope, root string) (string, error) {
	base := strings.TrimSpace(root)
	if base == "" {
		var err error
		base, err = filepath.Abs(filepath.Join(home, "qa"))
		if err != nil {
			return "", err
		}
	} else {
		expanded := expandPath(base)
		if filepath.IsAbs(expanded) {
			abs, err := filepath.Abs(expanded)
			if err != nil {
				return "", err
			}
			base = abs
		} else {
			resolved, err := resolveProjectPath(projectPath, expanded)
			if err != nil {
				return "", err
			}
			base = resolved
		}
	}
	dir := filepath.Join(base, safePathComponent(previewID, "preview"))
	if scope != "" && scope != "default" && scope != "all" {
		dir = filepath.Join(dir, safePathComponent(scope, "scope"))
	}
	return dir, nil
}

func qaEvidencePlan(previewID, scopeName string, p PreviewRecord, agent AgentConfig, scopes []QAScope, authSessions map[string]resolvedQAAuthSession, target, artifactDir string) (map[string]any, error) {
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
		if session, ok := qaAuthSessionForScope(scope, authSessions); ok {
			for _, page := range resolved {
				page["authSession"] = session.Name
				page["storageState"] = session.StorageState
				page["storageStateExists"] = session.Exists
			}
		}
		for _, page := range resolved {
			service := stringValue(page["service"])
			path := stringValue(page["path"])
			key := service + "\x00" + path + "\x00" + stringValue(page["authSession"])
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
			argv := []string{"vivero", "preview", "screenshot", "preview:" + previewID, service, path, "--breakpoints", "--json", "--no-input", "--quiet"}
			argv = appendArtifactTargetArgs(argv, target)
			storageState := stringValue(page["storageState"])
			if storageState != "" {
				argv = append(argv, "--storage-state", storageState)
			}
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
			if authSession := stringValue(page["authSession"]); authSession != "" {
				entry["authSession"] = authSession
				entry["storageState"] = stringValue(page["storageState"])
				entry["storageStateExists"] = page["storageStateExists"]
			}
			if colorScheme != "" {
				entry["colorScheme"] = colorScheme
			}
			screenshotCommands = append(screenshotCommands, entry)
		}
	}

	recordingCommands := []map[string]any{}
	recordingSession := resolvedQAAuthSession{}
	hasRecordingAuth := false
	if len(scopes) == 1 {
		recordingSession, hasRecordingAuth = qaAuthSessionForScope(scopes[0], authSessions)
	}
	for _, colorScheme := range recordingColorSchemes {
		argv := []string{"vivero", "preview", "qa", "record", "preview:" + previewID, "--scope", scopeName, "--json", "--no-input", "--quiet"}
		if hasRecordingAuth && recordingSession.StorageState != "" {
			argv = append(argv, "--storage-state", recordingSession.StorageState)
		}
		if colorScheme != "" {
			argv = append(argv, "--color-scheme", colorScheme)
		}
		entry := map[string]any{"scope": scopeName, "argv": argv, "command": shellJoin(argv)}
		if hasRecordingAuth {
			entry["authSession"] = recordingSession.Name
			entry["storageState"] = recordingSession.StorageState
			entry["storageStateExists"] = recordingSession.Exists
		}
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
		return append(argv, "--target", artifactTargetPublic)
	case artifactTargetOrigin:
		return append(argv, "--target", artifactTargetOrigin)
	default:
		return append(argv, "--target", defaultArtifactTarget)
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
		return command + " --target public"
	case artifactTargetOrigin:
		return command + " --target origin"
	default:
		return command + " --target local"
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
		if previewServiceHasPublicURL(svc) {
			return svc.URL
		}
		return ""
	case artifactTargetOrigin:
		return origin
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
