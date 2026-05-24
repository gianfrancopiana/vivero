package vivero

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

const (
	publicProviderCloudflare = "cloudflare"
	publicModeNamedTunnel    = "named-tunnel"
	defaultPublicRouterAddr  = "127.0.0.1:7777"
	defaultInactiveBehavior  = "410"
)

type PublicSetupRequest struct {
	Project          string
	BaseDomain       string
	Tunnel           string
	Zone             string
	Wildcard         string
	Hostname         string
	HostnameTemplate string
	RouterAddr       string
	InactiveBehavior string
}

type PublicSetupResult struct {
	OK                    bool     `json:"ok"`
	Project               string   `json:"project"`
	Path                  string   `json:"path"`
	Provider              string   `json:"provider"`
	Mode                  string   `json:"mode"`
	Tunnel                string   `json:"tunnel"`
	Zone                  string   `json:"zone,omitempty"`
	BaseDomain            string   `json:"baseDomain"`
	Wildcard              string   `json:"wildcard"`
	Hostname              string   `json:"hostname,omitempty"`
	HostnameTemplate      string   `json:"hostnameTemplate"`
	RouterAddr            string   `json:"routerAddr"`
	InactiveBehavior      string   `json:"inactiveBehavior"`
	StatePath             string   `json:"statePath"`
	CloudflaredConfigPath string   `json:"cloudflaredConfigPath"`
	Written               bool     `json:"written"`
	NextCommands          []string `json:"nextCommands"`
}

type PublicDoctorReport struct {
	OK                    bool                  `json:"ok"`
	Project               string                `json:"project"`
	Path                  string                `json:"path"`
	Provider              string                `json:"provider,omitempty"`
	Mode                  string                `json:"mode,omitempty"`
	Tunnel                string                `json:"tunnel,omitempty"`
	Zone                  string                `json:"zone,omitempty"`
	BaseDomain            string                `json:"baseDomain,omitempty"`
	Wildcard              string                `json:"wildcard,omitempty"`
	RouterAddr            string                `json:"routerAddr,omitempty"`
	StatePath             string                `json:"statePath,omitempty"`
	CloudflaredConfigPath string                `json:"cloudflaredConfigPath,omitempty"`
	Findings              []PublicDoctorFinding `json:"findings"`
	Errors                int                   `json:"errors"`
	Warnings              int                   `json:"warnings"`
}

type PublicDoctorFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

type PublicStartRequest struct {
	Project string
	DryRun  bool
}

type PublicStartResult struct {
	OK                    bool     `json:"ok"`
	Project               string   `json:"project"`
	Tunnel                string   `json:"tunnel"`
	BaseDomain            string   `json:"baseDomain"`
	Wildcard              string   `json:"wildcard"`
	RouterAddr            string   `json:"routerAddr"`
	StatePath             string   `json:"statePath"`
	CloudflaredConfigPath string   `json:"cloudflaredConfigPath"`
	RouterCommand         []string `json:"routerCommand"`
	CloudflaredCommand    []string `json:"cloudflaredCommand"`
	DryRun                bool     `json:"dryRun"`
	RouterPID             int      `json:"routerPid,omitempty"`
	CloudflaredPID        int      `json:"cloudflaredPid,omitempty"`
	RouterLogPath         string   `json:"routerLogPath,omitempty"`
	CloudflaredLogPath    string   `json:"cloudflaredLogPath,omitempty"`
}

func (a *App) runPublic(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	pos := positionalArgs(args)
	if len(pos) == 0 {
		return errOut(stderr, jsonOut, missingRequiredError("public", "subcommand", "vivero help public"))
	}
	switch pos[0] {
	case "setup":
		req, err := publicSetupRequestFromArgs(args[1:])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		result, err := a.PublicSetup(req)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"publicSetup": result}, publicSetupHuman(result))
		return 0
	case "doctor", "status":
		project := publicProjectPathFromArgs(args[1:])
		report, err := a.PublicDoctor(project)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		key := "publicDoctor"
		human := publicDoctorHuman(report)
		if pos[0] == "status" {
			key = "publicStatus"
			human = publicStatusHuman(report)
		}
		output(stdout, jsonOut, map[string]any{key: report}, human)
		if !report.OK {
			return 1
		}
		return 0
	case "start":
		req := PublicStartRequest{Project: publicProjectPathFromArgs(args[1:]), DryRun: hasArg(args[1:], "--dry-run")}
		result, err := a.PublicStart(req)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"publicStart": result}, publicStartHuman(result))
		return 0
	default:
		return errOut(stderr, jsonOut, unknownSubcommandError("public", pos[0]))
	}
}

func publicSetupRequestFromArgs(args []string) (PublicSetupRequest, error) {
	req := PublicSetupRequest{Project: publicProjectPathFromArgs(args)}
	req.BaseDomain, _ = flagValue(args, "--base-domain")
	req.Tunnel, _ = flagValue(args, "--tunnel")
	req.Zone, _ = flagValue(args, "--zone")
	req.Wildcard, _ = flagValue(args, "--wildcard")
	req.Hostname, _ = flagValue(args, "--hostname")
	req.HostnameTemplate, _ = flagValue(args, "--hostname-template")
	req.RouterAddr, _ = flagValue(args, "--router-addr")
	req.InactiveBehavior, _ = flagValue(args, "--inactive-behavior")
	return req, nil
}

func publicProjectPathFromArgs(args []string) string {
	if path, ok := flagValue(args, "--project"); ok && strings.TrimSpace(path) != "" {
		return path
	}
	pos := positionalArgs(args)
	if len(pos) == 0 {
		return "."
	}
	if (pos[0] == "setup" || pos[0] == "doctor" || pos[0] == "start") && len(pos) > 1 {
		return pos[1]
	}
	return pos[0]
}

func (a *App) PublicSetup(req PublicSetupRequest) (PublicSetupResult, error) {
	root, cfg, err := loadProjectConfig(req.Project)
	if err != nil {
		return PublicSetupResult{}, err
	}
	root, _ = filepath.Abs(root)
	public, err := normalizePublicSetupConfig(req, cfg.Project.Name)
	if err != nil {
		return PublicSetupResult{}, err
	}
	cfg.Public = public
	if err := writePublicConfigToProject(root, public); err != nil {
		return PublicSetupResult{}, err
	}
	if _, err := a.saveProject(root, cfg); err != nil {
		return PublicSetupResult{}, err
	}
	statePath := a.publicStatePath(cfg.Project.Name)
	configPath := a.publicCloudflaredConfigPath(cfg.Project.Name)
	if err := ensureDir(filepath.Dir(statePath)); err != nil {
		return PublicSetupResult{}, err
	}
	if err := os.WriteFile(configPath, []byte(renderCloudflaredNamedTunnelConfig(public)), 0o644); err != nil {
		return PublicSetupResult{}, err
	}
	result := PublicSetupResult{
		OK:                    true,
		Project:               cfg.Project.Name,
		Path:                  root,
		Provider:              public.Provider,
		Mode:                  public.Mode,
		Tunnel:                public.Tunnel,
		Zone:                  public.Zone,
		BaseDomain:            public.BaseDomain,
		Wildcard:              public.Wildcard,
		Hostname:              public.Hostname,
		HostnameTemplate:      public.HostnameTemplate,
		RouterAddr:            public.RouterAddr,
		InactiveBehavior:      public.InactiveBehavior,
		StatePath:             statePath,
		CloudflaredConfigPath: configPath,
		Written:               true,
	}
	result.NextCommands = []string{
		fmt.Sprintf("vivero public doctor --project %s --json --no-input", shellPathForSuggestion(root)),
		fmt.Sprintf("vivero public start --project %s --json --no-input", shellPathForSuggestion(root)),
	}
	if err := writePublicSetupState(statePath, result); err != nil {
		return PublicSetupResult{}, err
	}
	return result, nil
}

func normalizePublicSetupConfig(req PublicSetupRequest, projectName string) (PublicConfig, error) {
	baseDomain, err := normalizePublicHostname(req.BaseDomain, "")
	if err != nil {
		return PublicConfig{}, fmt.Errorf("--base-domain: %w", err)
	}
	if baseDomain == "" {
		return PublicConfig{}, fmt.Errorf("--base-domain is required")
	}
	wildcard, err := normalizePublicWildcard(req.Wildcard, baseDomain)
	if err != nil {
		return PublicConfig{}, err
	}
	tunnel := strings.TrimSpace(req.Tunnel)
	if tunnel == "" {
		tunnel = sanitizeDockerName(projectName + "-preview")
	}
	if strings.ContainsAny(tunnel, "\x00\n\r") {
		return PublicConfig{}, fmt.Errorf("--tunnel contains unsupported newline or NUL")
	}
	routerAddr := strings.TrimSpace(req.RouterAddr)
	if routerAddr == "" {
		routerAddr = defaultPublicRouterAddr
	}
	if err := validatePublicRouterAddr(routerAddr); err != nil {
		return PublicConfig{}, err
	}
	hostnameTemplate := strings.TrimSpace(req.HostnameTemplate)
	if hostnameTemplate == "" && strings.TrimSpace(req.Hostname) == "" {
		hostnameTemplate = "{{ .PreviewID }}.{{ .BaseDomain }}"
	}
	inactive := strings.TrimSpace(req.InactiveBehavior)
	if inactive == "" {
		inactive = defaultInactiveBehavior
	}
	if inactive != "410" && inactive != "404" {
		return PublicConfig{}, fmt.Errorf("--inactive-behavior must be 410 or 404")
	}
	hostname := strings.TrimSpace(req.Hostname)
	if hostname != "" {
		normalized, err := normalizePublicHostname(hostname, baseDomain)
		if err != nil {
			return PublicConfig{}, fmt.Errorf("--hostname: %w", err)
		}
		hostname = normalized
	}
	zone := strings.TrimSpace(req.Zone)
	if strings.ContainsAny(zone, "\x00\n\r") {
		return PublicConfig{}, fmt.Errorf("--zone contains unsupported newline or NUL")
	}
	return PublicConfig{
		Provider:         publicProviderCloudflare,
		Mode:             publicModeNamedTunnel,
		Tunnel:           tunnel,
		Zone:             zone,
		BaseDomain:       baseDomain,
		Wildcard:         wildcard,
		Hostname:         hostname,
		HostnameTemplate: hostnameTemplate,
		RouterAddr:       routerAddr,
		InactiveBehavior: inactive,
	}, nil
}

func normalizePublicWildcard(raw, baseDomain string) (string, error) {
	wildcard := strings.ToLower(strings.TrimSpace(strings.TrimRight(raw, ".")))
	if wildcard == "" {
		wildcard = "*." + baseDomain
	}
	if !strings.HasPrefix(wildcard, "*.") {
		return "", fmt.Errorf("--wildcard must start with *. for named tunnel routing")
	}
	host := strings.TrimPrefix(wildcard, "*.")
	if _, err := normalizePublicHostname(host, baseDomain); err != nil {
		return "", fmt.Errorf("--wildcard: %w", err)
	}
	return wildcard, nil
}

func validatePublicRouterAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || strings.TrimSpace(port) == "" {
		return fmt.Errorf("--router-addr must be host:port")
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("--router-addr must use a loopback host")
	}
	return nil
}

func writePublicConfigToProject(root string, public PublicConfig) error {
	_, configPath, err := resolveProjectConfigPath(root)
	if err != nil {
		return err
	}
	node, err := readProjectConfigNode(configPath)
	if err != nil {
		return err
	}
	setTopLevelYAMLMapping(&node, "public", publicConfigYAMLNode(public))
	var b bytes.Buffer
	encoder := yaml.NewEncoder(&b)
	encoder.SetIndent(2)
	if err := encoder.Encode(&node); err != nil {
		_ = encoder.Close()
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return os.WriteFile(configPath, b.Bytes(), 0o644)
}

func setTopLevelYAMLMapping(node *yaml.Node, key string, value yaml.Node) {
	if node.Kind == 0 {
		node.Kind = yaml.DocumentNode
	}
	if node.Kind != yaml.DocumentNode {
		original := *node
		*node = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&original}}
	}
	if len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		node.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	mapping := node.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = &value
			return
		}
	}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &value)
}

func publicConfigYAMLNode(public PublicConfig) yaml.Node {
	pairs := []struct{ key, value string }{
		{"provider", public.Provider},
		{"mode", public.Mode},
		{"tunnel", public.Tunnel},
		{"zone", public.Zone},
		{"baseDomain", public.BaseDomain},
		{"wildcard", public.Wildcard},
		{"hostname", public.Hostname},
		{"hostnameTemplate", public.HostnameTemplate},
		{"routerAddr", public.RouterAddr},
		{"inactiveBehavior", public.InactiveBehavior},
	}
	node := yaml.Node{Kind: yaml.MappingNode}
	for _, pair := range pairs {
		if strings.TrimSpace(pair.value) == "" {
			continue
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: pair.key},
			&yaml.Node{Kind: yaml.ScalarNode, Value: pair.value},
		)
	}
	return node
}

func writePublicSetupState(path string, result PublicSetupResult) error {
	body, err := json.MarshalIndent(map[string]any{
		"project":               result.Project,
		"provider":              result.Provider,
		"mode":                  result.Mode,
		"tunnel":                result.Tunnel,
		"zone":                  result.Zone,
		"baseDomain":            result.BaseDomain,
		"wildcard":              result.Wildcard,
		"hostname":              result.Hostname,
		"hostnameTemplate":      result.HostnameTemplate,
		"routerAddr":            result.RouterAddr,
		"inactiveBehavior":      result.InactiveBehavior,
		"cloudflaredConfigPath": result.CloudflaredConfigPath,
		"updatedAt":             nowUTC().Format("2006-01-02T15:04:05Z07:00"),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

func renderCloudflaredNamedTunnelConfig(public PublicConfig) string {
	var b strings.Builder
	b.WriteString("tunnel: " + yamlPlainScalar(public.Tunnel) + "\n")
	b.WriteString("ingress:\n")
	b.WriteString("  - hostname: " + yamlPlainScalar(public.Wildcard) + "\n")
	b.WriteString("    service: http://" + public.RouterAddr + "\n")
	b.WriteString("  - service: http_status:404\n")
	return b.String()
}

func (a *App) PublicDoctor(projectPath string) (PublicDoctorReport, error) {
	root, cfg, err := loadProjectConfig(projectPath)
	if err != nil {
		return PublicDoctorReport{}, err
	}
	root, _ = filepath.Abs(root)
	report := PublicDoctorReport{OK: true, Project: cfg.Project.Name, Path: root, Provider: cfg.Public.Provider, Mode: cfg.Public.Mode, Tunnel: cfg.Public.Tunnel, Zone: cfg.Public.Zone, BaseDomain: cfg.Public.BaseDomain, Wildcard: cfg.Public.Wildcard, RouterAddr: publicRouterAddr(cfg.Public), StatePath: a.publicStatePath(cfg.Project.Name), CloudflaredConfigPath: a.publicCloudflaredConfigPath(cfg.Project.Name)}
	addPublicConfigFindings(&report, cfg)
	if _, err := os.Stat(report.StatePath); err != nil {
		severity := "error"
		if os.IsNotExist(err) {
			report.addFinding(severity, "public-setup-state-missing", report.StatePath, "public setup state is missing", "Run `vivero public setup --project ...` before starting the named tunnel.")
		} else {
			report.addFinding(severity, "public-setup-state-unreadable", report.StatePath, err.Error(), "Check file permissions under VIVERO_HOME/public.")
		}
	} else {
		report.addFinding("info", "public-setup-state", report.StatePath, "public setup state exists", "")
	}
	if body, err := os.ReadFile(report.CloudflaredConfigPath); err != nil {
		if os.IsNotExist(err) {
			report.addFinding("error", "public-cloudflared-config-missing", report.CloudflaredConfigPath, "cloudflared config is missing", "Run `vivero public setup --project ...` to write the named tunnel config.")
		} else {
			report.addFinding("error", "public-cloudflared-config-unreadable", report.CloudflaredConfigPath, err.Error(), "Check file permissions under VIVERO_HOME/public.")
		}
	} else if !cloudflaredConfigMatches(body, cfg.Public) {
		report.addFinding("error", "public-cloudflared-config-drift", report.CloudflaredConfigPath, "cloudflared config does not match public config", "Re-run `vivero public setup --project ...`.")
	} else {
		report.addFinding("info", "public-cloudflared-config", report.CloudflaredConfigPath, "cloudflared named tunnel config matches public config", "")
	}
	if _, err := execLook("cloudflared"); err != nil {
		report.addFinding("warning", "public-cloudflared-binary", "cloudflared", "cloudflared is not on PATH", "Install cloudflared before running `vivero public start` without --dry-run.")
	} else {
		report.addFinding("info", "public-cloudflared-binary", "cloudflared", "cloudflared is available", "")
	}
	report.OK = report.Errors == 0
	return report, nil
}

func addPublicConfigFindings(report *PublicDoctorReport, cfg ProjectConfig) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Public.Provider))
	mode := strings.ToLower(strings.TrimSpace(cfg.Public.Mode))
	if provider != publicProviderCloudflare {
		report.addFinding("error", "public-provider-invalid", "public.provider", "public.provider must be cloudflare for durable named tunnels", "Run `vivero public setup --project ... --base-domain ...`.")
	}
	if mode != publicModeNamedTunnel {
		code := "public-mode-invalid"
		if mode == "quick-tunnel" || mode == "" {
			code = "public-mode-not-durable"
		}
		report.addFinding("error", code, "public.mode", "public.mode must be named-tunnel for durable preview URLs", "Quick tunnels are ephemeral; run `vivero public setup` to configure a Cloudflare named tunnel.")
	}
	if strings.TrimSpace(cfg.Public.Tunnel) == "" {
		report.addFinding("error", "public-tunnel-missing", "public.tunnel", "public.tunnel is required", "Pass --tunnel to `vivero public setup` or let setup derive a name.")
	}
	if _, err := normalizePublicHostname(cfg.Public.BaseDomain, ""); err != nil || strings.TrimSpace(cfg.Public.BaseDomain) == "" {
		report.addFinding("error", "public-base-domain-invalid", "public.baseDomain", "public.baseDomain must be a valid DNS name", "Pass --base-domain preview.example.com to `vivero public setup`.")
	}
	if _, err := normalizePublicWildcard(cfg.Public.Wildcard, cfg.Public.BaseDomain); err != nil {
		report.addFinding("error", "public-wildcard-invalid", "public.wildcard", err.Error(), "Use a wildcard under public.baseDomain, for example *.preview.example.com.")
	}
	if err := validatePublicRouterAddr(publicRouterAddr(cfg.Public)); err != nil {
		report.addFinding("error", "public-router-addr-invalid", "public.routerAddr", err.Error(), "Use a loopback host:port such as 127.0.0.1:7777.")
	}
	publicServices := 0
	for _, name := range sortedMapKeys(cfg.Services) {
		if cfg.Services[name].Public {
			publicServices++
		}
	}
	if publicServices == 0 {
		report.addFinding("warning", "public-services-empty", "services", "no services are marked public", "Set services.<name>.public: true or pass --public to preview up when needed.")
	}
	if report.Errors == 0 {
		report.addFinding("info", "public-config-valid", "public", "public named tunnel config is valid", "")
	}
}

func (r *PublicDoctorReport) addFinding(severity, code, path, message, hint string) {
	r.Findings = append(r.Findings, PublicDoctorFinding{Severity: severity, Code: code, Path: path, Message: message, Hint: hint})
	switch severity {
	case "error":
		r.Errors++
	case "warning":
		r.Warnings++
	}
}

func publicRouterAddr(public PublicConfig) string {
	if strings.TrimSpace(public.RouterAddr) == "" {
		return defaultPublicRouterAddr
	}
	return strings.TrimSpace(public.RouterAddr)
}

func cloudflaredConfigMatches(body []byte, public PublicConfig) bool {
	text := string(body)
	for _, want := range []string{"tunnel: " + public.Tunnel, "hostname: " + yamlPlainScalar(public.Wildcard), "service: http://" + publicRouterAddr(public), "http_status:404"} {
		if !strings.Contains(text, want) {
			return false
		}
	}
	return true
}

func (a *App) PublicStart(req PublicStartRequest) (PublicStartResult, error) {
	report, err := a.PublicDoctor(req.Project)
	if err != nil {
		return PublicStartResult{}, err
	}
	result := PublicStartResult{
		OK:                    report.OK,
		Project:               report.Project,
		Tunnel:                report.Tunnel,
		BaseDomain:            report.BaseDomain,
		Wildcard:              report.Wildcard,
		RouterAddr:            report.RouterAddr,
		StatePath:             report.StatePath,
		CloudflaredConfigPath: report.CloudflaredConfigPath,
		RouterCommand:         []string{"vivero", "serve", "--public-router", "--addr", report.RouterAddr},
		CloudflaredCommand:    []string{"cloudflared", "tunnel", "--config", report.CloudflaredConfigPath, "run", report.Tunnel},
		DryRun:                req.DryRun,
	}
	if !report.OK {
		return result, fmt.Errorf("public doctor failed for %s", report.Project)
	}
	if req.DryRun {
		return result, nil
	}
	if _, err := execLook("cloudflared"); err != nil {
		return result, fmt.Errorf("cloudflared not found: %w", err)
	}
	if err := ensureDir(a.publicProjectDir(report.Project)); err != nil {
		return result, err
	}
	result.RouterLogPath = filepath.Join(a.publicProjectDir(report.Project), "router.log")
	result.CloudflaredLogPath = filepath.Join(a.publicProjectDir(report.Project), "cloudflared.log")
	routerPID, err := startLoggedProcess(result.RouterLogPath, publicRuntimeCommand(a.Home, result.RouterCommand))
	if err != nil {
		return result, err
	}
	result.RouterPID = routerPID
	cloudflaredPID, err := startLoggedProcess(result.CloudflaredLogPath, result.CloudflaredCommand)
	if err != nil {
		_ = killProcessGroup(routerPID)
		return result, err
	}
	result.CloudflaredPID = cloudflaredPID
	result.OK = true
	return result, nil
}

func publicRuntimeCommand(home string, display []string) []string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return display
	}
	return append([]string{exe}, display[1:]...)
}

func startLoggedProcess(logPath string, command []string) (int, error) {
	if len(command) == 0 {
		return 0, fmt.Errorf("command is required")
	}
	if err := ensureDir(filepath.Dir(logPath)); err != nil {
		return 0, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = logFile.Close()
	return pid, nil
}

func (a *App) publicProjectDir(project string) string {
	return filepath.Join(a.Home, "public", safePathComponent(project, "project"))
}

func (a *App) publicStatePath(project string) string {
	return filepath.Join(a.publicProjectDir(project), "setup.json")
}

func (a *App) publicCloudflaredConfigPath(project string) string {
	return filepath.Join(a.publicProjectDir(project), "cloudflared.yml")
}

func publicSetupHuman(result PublicSetupResult) string {
	var b strings.Builder
	b.WriteString("public named tunnel configured for " + result.Project + "\n")
	b.WriteString("wildcard: " + result.Wildcard + "\n")
	b.WriteString("cloudflared config: " + result.CloudflaredConfigPath + "\n")
	for _, cmd := range result.NextCommands {
		b.WriteString("next: " + cmd + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func publicDoctorHuman(report PublicDoctorReport) string {
	var b strings.Builder
	status := "ok"
	if !report.OK {
		status = "failed"
	}
	b.WriteString("public doctor " + status + " for " + report.Project + "\n")
	for _, finding := range report.Findings {
		b.WriteString(fmt.Sprintf("%s %s %s\n", finding.Severity, finding.Code, finding.Message))
	}
	return strings.TrimRight(b.String(), "\n")
}

func publicStatusHuman(report PublicDoctorReport) string {
	status := "ready"
	if !report.OK {
		status = "needs attention"
	}
	return fmt.Sprintf("public status %s for %s: %d error(s), %d warning(s)", status, report.Project, report.Errors, report.Warnings)
}

func publicStartHuman(result PublicStartResult) string {
	var b strings.Builder
	if result.DryRun {
		b.WriteString("public start dry-run\n")
	} else {
		b.WriteString("public start launched\n")
	}
	b.WriteString("router: " + strings.Join(result.RouterCommand, " ") + "\n")
	b.WriteString("cloudflared: " + strings.Join(result.CloudflaredCommand, " "))
	return b.String()
}
