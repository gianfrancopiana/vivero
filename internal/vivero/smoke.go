package vivero

import (
	"fmt"
	"strings"
	"time"
)

func (a *App) Smoke(previewID, name string) (map[string]any, error) {
	p, err := a.getPreview(previewID)
	if err != nil {
		return nil, err
	}
	proj, err := a.getProject(p.Project)
	if err != nil {
		return nil, err
	}
	cfg, err := projectConfigForPreview(proj, p)
	if err != nil {
		return nil, err
	}
	defaultService := defaultPreviewService(cfg.Agent, p)
	var tests []SmokeTest
	for _, t := range cfg.Agent.SmokeTests {
		if name == "" || t.Name == name {
			tests = append(tests, t)
		}
	}
	if len(tests) == 0 {
		return nil, fmt.Errorf("no smoke tests matched")
	}
	results := []map[string]any{}
	okAll := true
	for _, t := range tests {
		res := map[string]any{"name": t.Name, "ok": false}
		service := strings.TrimSpace(t.Service)
		if service == "" {
			service = defaultService
		}
		if t.Command != "" {
			parts := []string{"/bin/sh", "-lc", t.Command}
			ex, _ := a.Exec(previewID, service, parts)
			res["exec"] = ex
			if ex != nil && ex["exitCode"].(int) == 0 {
				res["ok"] = true
			}
		} else {
			svc, exists := p.Services[service]
			if !exists {
				res["error"] = "service not found"
			} else {
				h := HealthConfig{Path: t.Path, ExpectStatus: t.ExpectStatus}
				if err := waitHTTP(svc.OriginURL, h, 15*time.Second); err != nil {
					res["error"] = err.Error()
				} else {
					res["ok"] = true
				}
			}
		}
		if res["ok"] != true {
			okAll = false
		}
		results = append(results, res)
	}
	return map[string]any{"preview": previewID, "ok": okAll, "results": results}, nil
}
