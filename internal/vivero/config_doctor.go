package vivero

import (
	"fmt"
	"path/filepath"
	"strings"
)

type ConfigDoctorReport struct {
	OK       bool                  `json:"ok"`
	Path     string                `json:"path"`
	Project  string                `json:"project,omitempty"`
	Errors   int                   `json:"errors"`
	Warnings int                   `json:"warnings"`
	Findings []ConfigDoctorFinding `json:"findings"`
}

type ConfigDoctorFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
}

func (a *App) ConfigDoctor(path string) (ConfigDoctorReport, error) {
	if strings.TrimSpace(path) == "" {
		return ConfigDoctorReport{}, fmt.Errorf("doctor config requires <path>")
	}
	root, cfg, err := loadProjectConfig(path)
	report := ConfigDoctorReport{OK: true, Path: root, Findings: []ConfigDoctorFinding{}}
	if root == "" {
		report.Path = expandPath(path)
	}
	if err != nil {
		report.addFinding("error", "config-load", "", err.Error())
		report.finish()
		return report, nil
	}
	report.Project = cfg.Project.Name
	if abs, absErr := filepath.Abs(root); absErr == nil {
		report.Path = abs
	}
	configDoctorCheckSources(&report, cfg)
	configDoctorCheckAgent(&report, cfg)
	report.finish()
	return report, nil
}

func (r *ConfigDoctorReport) addFinding(severity, code, path, message string) {
	r.Findings = append(r.Findings, ConfigDoctorFinding{Severity: severity, Code: code, Path: path, Message: message})
}

func (r *ConfigDoctorReport) finish() {
	r.Errors = 0
	r.Warnings = 0
	for _, finding := range r.Findings {
		switch finding.Severity {
		case "error":
			r.Errors++
		case "warning":
			r.Warnings++
		}
	}
	r.OK = r.Errors == 0
}

func configDoctorHuman(report ConfigDoctorReport) string {
	var b strings.Builder
	status := "ok"
	if !report.OK {
		status = "failed"
	}
	b.WriteString(fmt.Sprintf("config doctor %s: %s\n", status, report.Path))
	for _, finding := range report.Findings {
		if finding.Path != "" {
			b.WriteString(fmt.Sprintf("%s %s %s: %s\n", finding.Severity, finding.Code, finding.Path, finding.Message))
		} else {
			b.WriteString(fmt.Sprintf("%s %s: %s\n", finding.Severity, finding.Code, finding.Message))
		}
	}
	return b.String()
}

func configDoctorCheckSources(report *ConfigDoctorReport, cfg ProjectConfig) {
	for _, name := range sortedMapKeys(cfg.Services) {
		svc := cfg.Services[name]
		source := strings.TrimSpace(svc.Source)
		if source == "" {
			continue
		}
		if _, ok := cfg.Sources[source]; !ok {
			report.addFinding("error", "unknown-source", "services."+name+".source", fmt.Sprintf("service %s references unknown source %s", name, source))
		}
	}
}

func configDoctorCheckAgent(report *ConfigDoctorReport, cfg ProjectConfig) {
	if cfg.Agent.DefaultPreviewService != "" {
		if _, ok := cfg.Services[cfg.Agent.DefaultPreviewService]; !ok {
			report.addFinding("error", "unknown-service", "agent.defaultPreviewService", fmt.Sprintf("agent.defaultPreviewService references unknown service %s", cfg.Agent.DefaultPreviewService))
		}
	} else if len(cfg.Services) > 1 {
		report.addFinding("warning", "default-preview-service-missing", "agent.defaultPreviewService", "multiple services configured; set agent.defaultPreviewService so agents choose the intended preview URL")
	}
	for _, name := range sortedMapKeys(cfg.Agent.CommonPages) {
		page := cfg.Agent.CommonPages[name]
		if strings.TrimSpace(page.Path) == "" {
			report.addFinding("warning", "page-path-missing", "agent.commonPages."+name+".path", fmt.Sprintf("common page %s has no path", name))
		} else if !strings.HasPrefix(page.Path, "/") {
			report.addFinding("warning", "page-path-relative", "agent.commonPages."+name+".path", fmt.Sprintf("common page %s path should start with /", name))
		}
		if page.Service != "" {
			if _, ok := cfg.Services[page.Service]; !ok {
				report.addFinding("error", "unknown-service", "agent.commonPages."+name+".service", fmt.Sprintf("common page %s references unknown service %s", name, page.Service))
			}
		}
	}
	for i, smoke := range cfg.Agent.SmokeTests {
		base := fmt.Sprintf("agent.smokeTests[%d]", i)
		if strings.TrimSpace(smoke.Name) == "" {
			report.addFinding("error", "smoke-name-missing", base+".name", "smoke test name is required")
		}
		if smoke.Service != "" {
			if _, ok := cfg.Services[smoke.Service]; !ok {
				report.addFinding("error", "unknown-service", base+".service", fmt.Sprintf("smoke test %s references unknown service %s", smoke.Name, smoke.Service))
			}
		}
		if smoke.Path != "" && !strings.HasPrefix(smoke.Path, "/") {
			report.addFinding("warning", "smoke-path-relative", base+".path", fmt.Sprintf("smoke test %s path should start with /", smoke.Name))
		}
	}
	configDoctorCheckQAScopes(report, cfg)
}

func configDoctorCheckQAScopes(report *ConfigDoctorReport, cfg ProjectConfig) {
	scopes := cfg.Agent.QA.Scopes
	if len(scopes) == 0 {
		return
	}
	scopeNames := map[string]bool{}
	for i, scope := range scopes {
		name := qaScopeName(scope)
		base := fmt.Sprintf("agent.qa.scopes[%d]", i)
		if scopeNames[name] {
			report.addFinding("error", "qa-scope-duplicate", base+".name", fmt.Sprintf("duplicate QA scope %s", name))
		}
		scopeNames[name] = true
		for _, page := range scope.Pages {
			if _, ok := cfg.Agent.CommonPages[page]; !ok {
				report.addFinding("error", "unknown-page", base+".pages", fmt.Sprintf("QA scope %s references unknown common page %s", name, page))
			}
		}
		for flowIndex, flow := range scope.Flows {
			flowBase := fmt.Sprintf("%s.flows[%d]", base, flowIndex)
			if strings.TrimSpace(flow.Name) == "" {
				report.addFinding("error", "qa-flow-name-missing", flowBase+".name", fmt.Sprintf("QA scope %s has a flow without a name", name))
			}
			if flow.Service != "" {
				if _, ok := cfg.Services[flow.Service]; !ok {
					report.addFinding("error", "unknown-service", flowBase+".service", fmt.Sprintf("QA flow %s references unknown service %s", flow.Name, flow.Service))
				}
			}
			if flow.Start != "" {
				configDoctorCheckPageRef(report, cfg, flowBase+".start", name, flow.Start)
			}
			for stepIndex, step := range flow.Steps {
				if step.Visit != "" {
					configDoctorCheckPageRef(report, cfg, fmt.Sprintf("%s.steps[%d].visit", flowBase, stepIndex), name, step.Visit)
				}
			}
		}
		for checkIndex, check := range scope.Checks {
			if strings.TrimSpace(check.Name) == "" {
				report.addFinding("error", "qa-check-name-missing", fmt.Sprintf("%s.checks[%d].name", base, checkIndex), fmt.Sprintf("QA scope %s has a check without a name", name))
			}
		}
	}
	defaultScope := strings.TrimSpace(cfg.Agent.QA.DefaultScope)
	if defaultScope != "" && defaultScope != "all" && !scopeNames[defaultScope] {
		report.addFinding("error", "unknown-qa-scope", "agent.qa.defaultScope", fmt.Sprintf("agent.qa.defaultScope references unknown scope %s", defaultScope))
	}
}

func configDoctorCheckPageRef(report *ConfigDoctorReport, cfg ProjectConfig, path, scope, ref string) {
	if strings.HasPrefix(ref, "/") {
		return
	}
	if _, ok := cfg.Agent.CommonPages[ref]; !ok {
		report.addFinding("error", "unknown-page", path, fmt.Sprintf("QA scope %s references unknown common page %s", scope, ref))
	}
}
