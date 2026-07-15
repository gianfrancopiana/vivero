package vivero

import (
	"fmt"
	"os"
	"os/exec"
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
	Severity   string `json:"severity"`
	Code       string `json:"code"`
	Path       string `json:"path,omitempty"`
	Line       int    `json:"line,omitempty"`
	Column     int    `json:"column,omitempty"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
	Docs       string `json:"docs,omitempty"`
}

func (a *App) ConfigDoctor(path string) (ConfigDoctorReport, error) {
	if strings.TrimSpace(path) == "" {
		return ConfigDoctorReport{}, fmt.Errorf("doctor config requires <path>")
	}
	root, configPath, err := resolveProjectConfigPath(path)
	report := ConfigDoctorReport{OK: true, Path: root, Findings: []ConfigDoctorFinding{}}
	if root == "" {
		report.Path = expandPath(path)
	}
	if err != nil {
		report.addFinding("error", "config-load", "", err.Error())
		report.finish()
		return report, nil
	}
	node, err := readProjectConfigNode(configPath)
	if err != nil {
		report.addFinding("error", "config-load", "", err.Error())
		report.finish()
		return report, nil
	}
	report.Findings = append(report.Findings, configSchemaFindings(&node)...)
	cfg, err := decodeProjectConfigNode(configPath, &node)
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
	configDoctorCheckCompose(&report, root, cfg)
	configDoctorCheckBuildCache(&report, cfg)
	configDoctorCheckSetup(&report, root, cfg)
	configDoctorCheckAgent(&report, root, cfg)
	configDoctorCheckPublicRoutes(&report, cfg)
	report.finish()
	return report, nil
}

func configDoctorCheckCompose(report *ConfigDoctorReport, root string, cfg ProjectConfig) {
	type composeTarget struct {
		kind    string
		name    string
		source  string
		compose ComposeConfig
		health  HealthConfig
	}
	targets := []composeTarget{}
	for _, name := range sortedMapKeys(cfg.Services) {
		svc := cfg.Services[name]
		if serviceRuntime(svc) == "compose" {
			targets = append(targets, composeTarget{kind: "services", name: name, source: svc.Source, compose: svc.Compose, health: svc.Health})
		}
	}
	for _, name := range sortedMapKeys(cfg.BackingServices) {
		svc := cfg.BackingServices[name]
		if backingRuntime(svc) == "compose" {
			targets = append(targets, composeTarget{kind: "backingServices", name: name, source: svc.Source, compose: svc.Compose, health: svc.Health})
		}
	}
	for _, target := range targets {
		base := target.kind + "." + target.name
		if strings.TrimSpace(target.health.Timeout) == "" {
			report.addFinding("warning", "compose-health-timeout-missing", base+".health.timeout", fmt.Sprintf("compose service %s has no explicit health timeout; large application stacks often need longer than the default", target.name))
		}
		source, ok := cfg.Sources[target.source]
		if !ok {
			continue
		}
		if strings.TrimSpace(source.Path) == "" {
			report.addFinding("warning", "compose-file-unverified", base+".compose", fmt.Sprintf("compose files for %s cannot be verified until managed source %s is checked out", target.name, target.source))
			continue
		}
		sourcePath, err := resolveSourcePath(root, source.Path)
		if err != nil {
			report.addFinding("warning", "compose-file-unverified", base+".compose", fmt.Sprintf("compose files for %s could not be resolved: %v", target.name, err))
			continue
		}
		info, err := os.Stat(sourcePath)
		if err != nil {
			if os.IsNotExist(err) {
				report.addFinding("warning", "compose-file-unverified", base+".compose", fmt.Sprintf("compose files for %s cannot be verified because source root %s is unavailable", target.name, sourcePath))
			} else {
				report.addFinding("warning", "compose-file-unverified", base+".compose", fmt.Sprintf("compose files for %s cannot be verified: %v", target.name, err))
			}
			continue
		}
		if !info.IsDir() {
			report.addFinding("error", "compose-source-invalid", "sources."+target.source+".path", fmt.Sprintf("compose source root is not a directory: %s", sourcePath))
			continue
		}
		files, err := resolveComposeFiles(sourcePath, target.compose)
		if err != nil {
			report.addFinding("error", "compose-file-invalid", base+".compose", err.Error())
			continue
		}
		missing := false
		for i, file := range files {
			stat, statErr := os.Stat(file)
			if statErr != nil {
				missing = true
				path := base + ".compose.file"
				if len(files) > 1 {
					path = fmt.Sprintf("%s.compose.files[%d]", base, i)
				}
				report.addFinding("error", "compose-file-missing", path, fmt.Sprintf("compose file does not exist: %s", file))
				continue
			}
			if stat.IsDir() {
				missing = true
				report.addFinding("error", "compose-file-invalid", base+".compose", fmt.Sprintf("compose file is a directory: %s", file))
			}
		}
		if missing {
			continue
		}
		if _, err := exec.LookPath("docker"); err != nil {
			report.addFinding("warning", "compose-inspection-unavailable", base+".compose", fmt.Sprintf("cannot inspect the Compose dependency closure for %s because Docker is unavailable", target.name))
			continue
		}
		spec := dockerComposeServiceSpec{
			Project:        cfg.Project.Name,
			PreviewID:      "doctor",
			Service:        target.name,
			ComposeProject: dockerComposeProjectName("doctor", target.name),
			ComposeService: composeServiceName(target.compose, target.name),
			Source:         sourcePath,
			Files:          files,
		}
		model, err := dockerComposeConfig(spec)
		if err != nil {
			report.addFinding("error", "compose-config-invalid", base+".compose", err.Error())
			continue
		}
		if _, ok := model.Services[spec.ComposeService]; !ok {
			report.addFinding("error", "compose-service-missing", base+".compose.service", fmt.Sprintf("compose service %s is not defined in %s", spec.ComposeService, strings.Join(files, ", ")))
			continue
		}
		if omitted := dockerComposeOmittedServices(model, spec.ComposeService); len(omitted) > 0 {
			report.addFinding("info", "compose-services-omitted", base+".compose.service", fmt.Sprintf("target %s will not start Compose services outside its depends_on closure: %s", spec.ComposeService, strings.Join(omitted, ", ")))
		}
		closure := dockerComposeTargetClosure(model, spec.ComposeService)
		for _, service := range sortedMapKeys(model.Services) {
			if service == spec.ComposeService || !closure[service] {
				continue
			}
			if ports := dockerComposePublishedPorts(model.Services[service]); len(ports) > 0 {
				report.addFinding("warning", "compose-host-ports-stripped", base+".compose", fmt.Sprintf("dependency service %s publishes host port(s) %s; Vivero will strip them to prevent concurrent-preview collisions", service, strings.Join(ports, ", ")))
			}
		}
		if target.kind == "services" {
			if ports, err := servicePortPlan(cfg.Services[target.name]); err == nil {
				for _, port := range ports {
					owner := strings.TrimSpace(port.ComposeService)
					if owner == "" || owner == spec.ComposeService {
						continue
					}
					path := base + ".ports." + port.Name + ".composeService"
					if _, ok := model.Services[owner]; !ok {
						report.addFinding("error", "compose-port-service-missing", path, fmt.Sprintf("named port %s composeService %s is not defined in the Compose project", port.Name, owner))
						continue
					}
					if !closure[owner] {
						report.addFinding("error", "compose-port-service-outside-closure", path, fmt.Sprintf("named port %s composeService %s is outside target %s depends_on closure", port.Name, owner, spec.ComposeService))
					}
				}
			}
		}
	}
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
		location := configDoctorFindingLocation(finding)
		if location != "" {
			b.WriteString(fmt.Sprintf("%s %s %s: %s\n", finding.Severity, finding.Code, location, finding.Message))
		} else {
			b.WriteString(fmt.Sprintf("%s %s: %s\n", finding.Severity, finding.Code, finding.Message))
		}
		if finding.Suggestion != "" {
			b.WriteString(fmt.Sprintf("  suggestion: %s\n", finding.Suggestion))
		}
	}
	return b.String()
}

func configDoctorFindingLocation(finding ConfigDoctorFinding) string {
	location := finding.Path
	if finding.Line > 0 {
		if location == "" {
			location = fmt.Sprintf("line %d", finding.Line)
		} else {
			location = fmt.Sprintf("%s:%d", location, finding.Line)
		}
		if finding.Column > 0 {
			location = fmt.Sprintf("%s:%d", location, finding.Column)
		}
	}
	return location
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

func configDoctorCheckBuildCache(report *ConfigDoctorReport, cfg ProjectConfig) {
	for _, name := range sortedMapKeys(cfg.Services) {
		svc := cfg.Services[name]
		if !imageBuildCacheEnabled(svc.Build.Cache) {
			continue
		}
		if err := ensureDockerBuildxAvailable(); err != nil {
			report.addFinding("error", "build-cache-buildx-unavailable", "services."+name+".build.cache", fmt.Sprintf("service %s build cache requires Docker buildx: %v", name, err))
		}
	}
}

func configDoctorCheckSetup(report *ConfigDoctorReport, root string, cfg ProjectConfig) {
	check := func(kind string, steps []SetupStep) {
		for i, step := range steps {
			if strings.TrimSpace(step.Stdin) != "" && step.Command.IsZero() {
				report.addFinding("error", "setup-stdin-without-command", fmt.Sprintf("%s[%d].stdin", kind, i), "stdin requires command")
			}
			policy, err := normalizeSetupPolicy(step.Policy)
			if err != nil {
				report.addFinding("error", "setup-policy-invalid", fmt.Sprintf("%s[%d].policy", kind, i), err.Error())
				continue
			}
			if policy != "once-per-project" && policy != "once-per-fingerprint" {
				continue
			}
			base := fmt.Sprintf("%s[%d]", kind, i)
			svc, ok := cfg.Services[step.Service]
			if !ok || strings.TrimSpace(step.Service) == "" {
				continue
			}
			if !serviceHasPersistentDependencyVolume(svc) {
				report.addFinding("warning", "setup-persistent-volume-missing", base+".service", fmt.Sprintf("setup step for service %s uses %s but the service has no dependencyVolume with lifetime project or smart", step.Service, policy))
			}
			if policy != "once-per-fingerprint" {
				continue
			}
			paths := step.Fingerprint.Paths
			pathSource := base + ".fingerprint.paths"
			if len(paths) == 0 {
				paths = cfg.Warm.Fingerprint.Paths
				pathSource = "warm.fingerprint.paths"
			}
			if len(paths) == 0 {
				report.addFinding("warning", "setup-fingerprint-paths-missing", base+".fingerprint.paths", "once-per-fingerprint setup needs fingerprint.paths or warm.fingerprint.paths")
				continue
			}
			fingerprintRoot := root
			if strings.TrimSpace(svc.Source) != "" {
				if src, ok := cfg.Sources[svc.Source]; ok && strings.TrimSpace(src.Path) != "" {
					if resolved, err := resolveSourcePath(root, src.Path); err == nil {
						fingerprintRoot = resolved
					}
				}
			}
			for pathIndex, raw := range paths {
				rel, err := normalizeFingerprintPath(raw)
				if err != nil {
					continue
				}
				full, err := resolveProjectPath(fingerprintRoot, filepath.FromSlash(rel))
				if err != nil {
					continue
				}
				if _, err := os.Stat(full); err != nil {
					report.addFinding("warning", "setup-fingerprint-path-missing", fmt.Sprintf("%s[%d]", pathSource, pathIndex), fmt.Sprintf("fingerprint path does not exist yet: %s", full))
				}
			}
		}
	}
	check("setup.afterSeeds", cfg.Setup.AfterSeeds)
	check("setup.everyBoot", cfg.Setup.EveryBoot)
}

func configDoctorCheckAgent(report *ConfigDoctorReport, root string, cfg ProjectConfig) {
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
	configDoctorCheckQAAuth(report, root, cfg)
	configDoctorCheckQAScopes(report, cfg)
}

func configDoctorCheckPublicRoutes(report *ConfigDoctorReport, cfg ProjectConfig) {
	project := strings.TrimSpace(cfg.Project.Name)
	previewID := project
	if previewID == "" {
		previewID = "preview"
	}
	_, err := plannedNamedPublicHosts(UpRequest{Project: project, ID: previewID, Public: true}, cfg)
	if err != nil {
		report.addFinding("error", "public-route", "public", err.Error())
	}
}

func configDoctorCheckQAAuth(report *ConfigDoctorReport, root string, cfg ProjectConfig) {
	scopeNames := map[string]bool{}
	for _, scope := range cfg.Agent.QA.Scopes {
		scopeNames[qaScopeName(scope)] = true
	}
	for _, name := range sortedMapKeys(cfg.Agent.QA.Auth.Sessions) {
		session := cfg.Agent.QA.Auth.Sessions[name]
		base := "agent.qa.auth.sessions." + name
		if strings.TrimSpace(name) == "" {
			report.addFinding("error", "qa-auth-session-name-missing", base, "QA auth session name is required")
		}
		if strings.TrimSpace(session.StorageState) == "" {
			report.addFinding("warning", "qa-auth-storage-state-missing", base+".storageState", fmt.Sprintf("QA auth session %s has no Playwright storage state path", name))
		} else if full, err := resolveProjectPath(root, session.StorageState); err != nil {
			report.addFinding("error", "qa-auth-storage-state-invalid", base+".storageState", err.Error())
		} else if _, err := os.Stat(full); os.IsNotExist(err) {
			report.addFinding("warning", "qa-auth-storage-state-missing", base+".storageState", fmt.Sprintf("storage state %s does not exist yet", session.StorageState))
		} else if err != nil {
			report.addFinding("error", "qa-auth-storage-state-invalid", base+".storageState", err.Error())
		}
		for i, scope := range session.Scopes {
			scope = strings.TrimSpace(scope)
			if scope == "" || scope == "all" || scope == "*" {
				continue
			}
			if len(scopeNames) > 0 && !scopeNames[scope] {
				report.addFinding("error", "unknown-qa-scope", fmt.Sprintf("%s.scopes[%d]", base, i), fmt.Sprintf("QA auth session %s references unknown scope %s", name, scope))
			}
		}
	}
}

func configDoctorCheckQAScopes(report *ConfigDoctorReport, cfg ProjectConfig) {
	scopes := cfg.Agent.QA.Scopes
	if len(scopes) == 0 {
		return
	}
	scopeNames := map[string]bool{}
	sessionNames := map[string]bool{}
	for name := range cfg.Agent.QA.Auth.Sessions {
		sessionNames[strings.TrimSpace(name)] = true
	}
	for i, scope := range scopes {
		name := qaScopeName(scope)
		base := fmt.Sprintf("agent.qa.scopes[%d]", i)
		if scopeNames[name] {
			report.addFinding("error", "qa-scope-duplicate", base+".name", fmt.Sprintf("duplicate QA scope %s", name))
		}
		if authSession := strings.TrimSpace(scope.AuthSession); authSession != "" && !sessionNames[authSession] {
			report.addFinding("error", "unknown-qa-auth-session", base+".authSession", fmt.Sprintf("QA scope %s references unknown auth session %s", name, authSession))
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
