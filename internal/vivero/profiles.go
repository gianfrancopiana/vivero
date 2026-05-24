package vivero

import (
	"fmt"
	"strings"
)

func projectConfigForRequestedProfile(cfg ProjectConfig, profile string) (ProjectConfig, string, error) {
	name := strings.TrimSpace(profile)
	if name == "" {
		if _, ok := cfg.Profiles["default"]; ok {
			name = "default"
		}
	}
	return projectConfigForProfile(cfg, name)
}

func projectConfigForEnvironment(cfg ProjectConfig, environment string) (ProjectConfig, string, error) {
	profile := strings.TrimSpace(environment)
	if profile == "" {
		return cfg, "", nil
	}
	if _, ok := cfg.Profiles[profile]; !ok {
		return cfg, "", nil
	}
	return projectConfigForProfile(cfg, profile)
}

func projectConfigForPreview(project ProjectRecord, preview PreviewRecord) (ProjectConfig, error) {
	cfg, _, err := projectConfigForProfile(project.Config, preview.Profile)
	return cfg, err
}

func projectConfigForProfile(cfg ProjectConfig, profile string) (ProjectConfig, string, error) {
	name := strings.TrimSpace(profile)
	if name == "" {
		return cfg, "", nil
	}
	prof, ok := cfg.Profiles[name]
	if !ok {
		return ProjectConfig{}, "", fmt.Errorf("profile not found: %s", name)
	}
	out := cfg
	activeServices, err := selectServices(cfg.Services, prof.Services, "profile "+name)
	if err != nil {
		return ProjectConfig{}, "", err
	}
	activeServices, err = applyProfileServiceEnv(activeServices, prof.ServiceEnv, "profile "+name)
	if err != nil {
		return ProjectConfig{}, "", err
	}
	out.Services = activeServices
	activeBacking, err := selectBackingServices(cfg.BackingServices, prof.BackingServices, "profile "+name)
	if err != nil {
		return ProjectConfig{}, "", err
	}
	out.BackingServices = activeBacking
	out.Sources, err = selectSourcesForServices(cfg.Sources, out.Services, "profile "+name)
	if err != nil {
		return ProjectConfig{}, "", err
	}
	out.Setup.AfterSeeds = filterSetupStepsForServices(cfg.Setup.AfterSeeds, out.Services)
	out.Agent = filterAgentForProfile(cfg.Agent, out.Services, prof, name)
	return out, name, nil
}

func selectServices(all map[string]ServiceConfig, names []string, context string) (map[string]ServiceConfig, error) {
	if names == nil {
		selected := make(map[string]ServiceConfig, len(all))
		for name, svc := range all {
			selected[name] = svc
		}
		return selected, nil
	}
	selected := make(map[string]ServiceConfig, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("%s services contains an empty service name", context)
		}
		svc, ok := all[name]
		if !ok {
			return nil, fmt.Errorf("%s references unknown service %s", context, name)
		}
		selected[name] = svc
	}
	return selected, nil
}

func selectBackingServices(all map[string]BackingConfig, names []string, context string) (map[string]BackingConfig, error) {
	if names == nil {
		selected := make(map[string]BackingConfig, len(all))
		for name, svc := range all {
			selected[name] = svc
		}
		return selected, nil
	}
	selected := make(map[string]BackingConfig, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("%s backingServices contains an empty service name", context)
		}
		svc, ok := all[name]
		if !ok {
			return nil, fmt.Errorf("%s references unknown backing service %s", context, name)
		}
		selected[name] = svc
	}
	return selected, nil
}

func applyProfileServiceEnv(services map[string]ServiceConfig, overlays map[string]map[string]string, context string) (map[string]ServiceConfig, error) {
	if len(overlays) == 0 {
		return services, nil
	}
	selected := make(map[string]ServiceConfig, len(services))
	for name, svc := range services {
		selected[name] = svc
	}
	for rawService, env := range overlays {
		service := strings.TrimSpace(rawService)
		if service == "" || service != rawService {
			return nil, fmt.Errorf("%s serviceEnv service name %q must be non-empty and trimmed", context, rawService)
		}
		svc, ok := selected[service]
		if !ok {
			return nil, fmt.Errorf("%s serviceEnv references inactive or unknown service %s", context, service)
		}
		merged := make(map[string]string, len(svc.Env)+len(env))
		for key, value := range svc.Env {
			merged[key] = value
		}
		for _, key := range sortedMapKeys(env) {
			merged[key] = env[key]
		}
		svc.Env = merged
		selected[service] = svc
	}
	return selected, nil
}

func selectSourcesForServices(all map[string]SourceConfig, services map[string]ServiceConfig, context string) (map[string]SourceConfig, error) {
	used := map[string]struct{}{}
	for _, svc := range services {
		name := strings.TrimSpace(svc.Source)
		if name != "" {
			used[name] = struct{}{}
		}
	}
	if len(used) == 0 {
		return map[string]SourceConfig{}, nil
	}
	selected := make(map[string]SourceConfig, len(used))
	for _, name := range sortedStringSetKeys(used) {
		src, ok := all[name]
		if !ok {
			return nil, fmt.Errorf("%s selected service references unknown source %s", context, name)
		}
		selected[name] = src
	}
	return selected, nil
}

func filterSetupStepsForServices(steps []SetupStep, services map[string]ServiceConfig) []SetupStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]SetupStep, 0, len(steps))
	for _, step := range steps {
		if serviceSelected(step.Service, services, "") {
			out = append(out, step)
		}
	}
	return out
}

func filterAgentForProfile(agent AgentConfig, services map[string]ServiceConfig, prof ProfileConfig, profileName string) AgentConfig {
	out := agent
	out.DefaultPreviewService = activeDefaultPreviewService(agent.DefaultPreviewService, services)
	out.CommonPages = filterCommonPagesForServices(agent.CommonPages, services, out.DefaultPreviewService)
	out.SmokeTests = filterSmokeTestsForProfile(agent.SmokeTests, services, out.DefaultPreviewService, prof.SmokeTests)
	out.QA.Scopes = filterQAScopesForServices(agent.QA.Scopes, out.CommonPages, services, out.DefaultPreviewService)
	return out
}

func activeDefaultPreviewService(defaultService string, services map[string]ServiceConfig) string {
	if serviceSelected(defaultService, services, "") {
		return defaultService
	}
	keys := sortedMapKeys(services)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func filterCommonPagesForServices(pages map[string]AgentPage, services map[string]ServiceConfig, defaultService string) map[string]AgentPage {
	if len(pages) == 0 {
		return pages
	}
	out := map[string]AgentPage{}
	for name, page := range pages {
		if serviceSelected(page.Service, services, defaultService) {
			out[name] = page
		}
	}
	return out
}

func filterSmokeTestsForProfile(tests []SmokeTest, services map[string]ServiceConfig, defaultService string, names []string) []SmokeTest {
	if len(tests) == 0 {
		return nil
	}
	nameSet := map[string]struct{}{}
	if len(names) > 0 {
		for _, raw := range names {
			name := strings.TrimSpace(raw)
			if name != "" {
				nameSet[name] = struct{}{}
			}
		}
	}
	out := make([]SmokeTest, 0, len(tests))
	for _, test := range tests {
		if len(nameSet) > 0 {
			if _, ok := nameSet[test.Name]; !ok {
				continue
			}
		}
		if serviceSelected(test.Service, services, defaultService) {
			out = append(out, test)
		}
	}
	return out
}

func filterQAScopesForServices(scopes []QAScope, pages map[string]AgentPage, services map[string]ServiceConfig, defaultService string) []QAScope {
	if len(scopes) == 0 {
		return scopes
	}
	out := make([]QAScope, 0, len(scopes))
	for _, scope := range scopes {
		filtered := scope
		filtered.Pages = filterQAPageRefs(scope.Pages, pages)
		filtered.Flows = filterQAFlowsForServices(scope.Flows, services, defaultService)
		out = append(out, filtered)
	}
	return out
}

func filterQAPageRefs(refs []string, pages map[string]AgentPage) []string {
	if len(refs) == 0 {
		return refs
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if _, ok := pages[ref]; ok {
			out = append(out, ref)
		}
	}
	return out
}

func filterQAFlowsForServices(flows []QAFlow, services map[string]ServiceConfig, defaultService string) []QAFlow {
	if len(flows) == 0 {
		return flows
	}
	out := make([]QAFlow, 0, len(flows))
	for _, flow := range flows {
		if serviceSelected(flow.Service, services, defaultService) {
			out = append(out, flow)
		}
	}
	return out
}

func serviceSelected(name string, services map[string]ServiceConfig, defaultService string) bool {
	service := strings.TrimSpace(name)
	if service == "" {
		service = strings.TrimSpace(defaultService)
	}
	if service == "" {
		return len(services) > 0
	}
	_, ok := services[service]
	return ok
}

func sortedStringSetKeys(values map[string]struct{}) []string {
	asMap := make(map[string]any, len(values))
	for key := range values {
		asMap[key] = true
	}
	return sortedMapKeys(asMap)
}

func validateProfilesConfig(configPath string, cfg ProjectConfig) error {
	for rawName, prof := range cfg.Profiles {
		name := strings.TrimSpace(rawName)
		if name == "" || name != rawName {
			return fmt.Errorf("%s profile name %q must be non-empty and trimmed", configPath, rawName)
		}
		activeServices, err := selectServices(cfg.Services, prof.Services, "profile "+name)
		if err != nil {
			return fmt.Errorf("%s %w", configPath, err)
		}
		if err := validateProfileServiceEnv(configPath, name, prof.ServiceEnv, activeServices); err != nil {
			return err
		}
		if _, err := selectBackingServices(cfg.BackingServices, prof.BackingServices, "profile "+name); err != nil {
			return fmt.Errorf("%s %w", configPath, err)
		}
		if err := validateProfileSmokeTests(configPath, name, prof.SmokeTests, cfg.Agent.SmokeTests); err != nil {
			return err
		}
	}
	return nil
}

func validateProfileServiceEnv(configPath, profile string, overlays map[string]map[string]string, services map[string]ServiceConfig) error {
	for rawService, env := range overlays {
		service := strings.TrimSpace(rawService)
		if service == "" || service != rawService {
			return fmt.Errorf("%s profile %s serviceEnv service name %q must be non-empty and trimmed", configPath, profile, rawService)
		}
		if _, ok := services[service]; !ok {
			return fmt.Errorf("%s profile %s serviceEnv references inactive or unknown service %s", configPath, profile, service)
		}
		if err := validateRuntimeEnv(configPath, "profile "+profile+" serviceEnv", service, env); err != nil {
			return err
		}
	}
	return nil
}

func validateProfileSmokeTests(configPath, profile string, names []string, tests []SmokeTest) error {
	if len(names) == 0 {
		return nil
	}
	known := map[string]struct{}{}
	for _, test := range tests {
		known[test.Name] = struct{}{}
	}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return fmt.Errorf("%s profile %s smokeTests contains an empty smoke test name", configPath, profile)
		}
		if _, ok := known[name]; !ok {
			return fmt.Errorf("%s profile %s references unknown smoke test %s", configPath, profile, name)
		}
	}
	return nil
}
