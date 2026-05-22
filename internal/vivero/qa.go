package vivero

import (
	"fmt"
	"os"
	"path/filepath"
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
	artifactDir, err := qaArtifactDir(a.Home, project.Path, previewID, selectedScope, agent.QA.ArtifactRoot)
	if err != nil {
		return nil, err
	}
	driver := qaDriver(agent.QA.Driver)
	authPlan, authSessions, err := qaAuthPlan(project.Path, agent.QA.Auth)
	if err != nil {
		return nil, err
	}

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
		scopePlan := map[string]any{
			"name":        qaScopeName(scope),
			"description": scope.Description,
			"pages":       pages,
			"flows":       flows,
			"checks":      qaChecksForScope(agent, scope),
		}
		if session, ok := qaAuthSessionForScope(scope, authSessions); ok {
			scopePlan["authSession"] = session.Name
			scopePlan["storageState"] = session.StorageState
			scopePlan["storageStateExists"] = session.Exists
		}
		scopePlans = append(scopePlans, scopePlan)
	}
	evidence, err := qaEvidencePlan(p.ID, selectedScope, p, agent, scopes, authSessions, target, artifactDir)
	if err != nil {
		return nil, err
	}
	previewRef := "preview:" + p.ID

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
		"auth":    authPlan,
		"artifacts": map[string]any{
			"dir":        artifactDir,
			"runPath":    filepath.Join(artifactDir, "run.json"),
			"recordPath": filepath.Join(artifactDir, "record.json"),
			"finalPath":  filepath.Join(artifactDir, "final.json"),
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
			"smoke":       fmt.Sprintf("vivero preview smoke %s --json --no-input --quiet", previewRef),
			"events":      fmt.Sprintf("vivero preview events %s --tail --json --no-input", previewRef),
			"report":      fmt.Sprintf("vivero preview qa report %s --scope %s --json --no-input", previewRef, selectedScope),
			"record":      fmt.Sprintf("vivero preview qa record %s --scope %s --json --no-input --quiet", previewRef, selectedScope),
			"screenshots": qaCommandWithTarget(fmt.Sprintf("vivero preview screenshot %s %s <path> --breakpoints --json --no-input --quiet", previewRef, defaultPreviewService(agent, p)), target),
		},
		"agentInstructions": []string{
			"Use this plan as the source of truth for preview URLs, services, scopes, and artifact paths.",
			"Screenshots and QA reports default to local preview URLs; pass --public only for explicit public-tunnel validation.",
			"Browser recordings use the local/proxy preview URL.",
			"Use evidence.screenshots.commands and evidence.recordings.commands for YAML-backed screenshot and video evidence instead of hardcoding project-specific matrices.",
			"Use Playwright for reproducible screenshots, recordings, traces, and CI-safe E2E evidence; use Chrome MCP or another browser driver only for exploratory/debug sessions.",
			"Run project smoke tests before or during QA, then drive the listed pages and flows in a real browser.",
			"Save screenshots, traces, and notes under artifacts.dir; generate the final markdown scaffold with `vivero preview qa report`.",
			"Do not hardcode project-specific URLs or selectors in the generic agent skill; put them in vivero.yml under agent.qa.",
		},
	}, nil
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
	outPath = nextAvailableArtifactPath(outPath)
	if err := ensureDir(filepath.Dir(outPath)); err != nil {
		return nil, err
	}
	content := renderQAReport(plan)
	if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "preview": previewID, "scope": scopeNameFromPlan(plan), "path": outPath, "bytes": len(content)}, nil
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
