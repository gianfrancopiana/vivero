package vivero

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gianfrancopiana/vivero/internal/nameid"
)

type InitConfigRequest struct {
	Path         string
	Project      string
	Service      string
	Port         int
	Command      string
	BuildContext string
	Dockerfile   string
	DefaultRef   string
	HealthPath   string
	Force        bool
}

type InitConfigResult struct {
	OK           bool     `json:"ok"`
	Path         string   `json:"path"`
	Project      string   `json:"project"`
	Service      string   `json:"service"`
	Written      bool     `json:"written"`
	NextCommands []string `json:"nextCommands"`
}

func initConfigRequestFromArgs(args []string) (InitConfigRequest, error) {
	pos := positionalArgs(args)
	path := "."
	if len(pos) > 0 {
		path = pos[0]
	}
	project, _ := flagValue(args, "--name")
	service, _ := flagValue(args, "--service")
	command, _ := flagValue(args, "--command")
	buildContext, _ := flagValue(args, "--build-context")
	dockerfile, _ := flagValue(args, "--dockerfile")
	defaultRef, _ := flagValue(args, "--default-ref")
	healthPath, _ := flagValue(args, "--health-path")
	port, ok, err := positiveIntFlag(args, "--port")
	if err != nil {
		return InitConfigRequest{}, err
	}
	if !ok {
		port = 3000
	}
	if buildContext == "" {
		buildContext = "."
	}
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	if defaultRef == "" {
		defaultRef = "main"
	}
	if healthPath == "" {
		healthPath = "/"
	}
	return InitConfigRequest{
		Path:         path,
		Project:      project,
		Service:      service,
		Port:         port,
		Command:      command,
		BuildContext: buildContext,
		Dockerfile:   dockerfile,
		DefaultRef:   defaultRef,
		HealthPath:   healthPath,
		Force:        hasArg(args, "--force"),
	}, nil
}

func InitConfig(req InitConfigRequest) (InitConfigResult, error) {
	root, configPath, err := initTargetPaths(req.Path)
	if err != nil {
		return InitConfigResult{}, err
	}
	project := nameid.Docker(req.Project)
	if strings.TrimSpace(req.Project) == "" {
		project = nameid.Docker(filepath.Base(root))
	}
	service := nameid.Docker(req.Service)
	if strings.TrimSpace(req.Service) == "" {
		service = "web"
	}
	if req.Port <= 0 {
		req.Port = 3000
	}
	if req.BuildContext == "" {
		req.BuildContext = "."
	}
	if req.Dockerfile == "" {
		req.Dockerfile = "Dockerfile"
	}
	if req.DefaultRef == "" {
		req.DefaultRef = "main"
	}
	if req.HealthPath == "" {
		req.HealthPath = "/"
	}
	if _, err := os.Stat(configPath); err == nil && !req.Force {
		return InitConfigResult{}, newCLIError("config_exists", "vivero.yml already exists", "Run `vivero init --force` to overwrite the existing config", map[string]string{"command": "init", "path": configPath})
	} else if err != nil && !os.IsNotExist(err) {
		return InitConfigResult{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return InitConfigResult{}, err
	}
	body := renderInitConfig(project, service, req)
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		return InitConfigResult{}, err
	}
	result := InitConfigResult{OK: true, Path: configPath, Project: project, Service: service, Written: true}
	result.NextCommands = []string{
		fmt.Sprintf("vivero doctor config %s --json --no-input", shellPathForSuggestion(root)),
		fmt.Sprintf("vivero projects sync %s --json --no-input", shellPathForSuggestion(root)),
		fmt.Sprintf("vivero preview up %s --id %s-local --wait --json --no-input", project, project),
	}
	return result, nil
}

func initTargetPaths(path string) (string, string, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	path = expandPath(path)
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	if ext := strings.ToLower(filepath.Ext(abs)); ext == ".yml" || ext == ".yaml" {
		return filepath.Dir(abs), abs, nil
	}
	return abs, filepath.Join(abs, "vivero.yml"), nil
}

func renderInitConfig(project, service string, req InitConfigRequest) string {
	var b strings.Builder
	b.WriteString("project:\n")
	b.WriteString("  name: " + yamlPlainScalar(project) + "\n\n")
	b.WriteString("sources:\n")
	b.WriteString("  app:\n")
	b.WriteString("    mode: external\n")
	b.WriteString("    path: .\n")
	b.WriteString("    defaultRef: " + yamlPlainScalar(req.DefaultRef) + "\n\n")
	b.WriteString("services:\n")
	b.WriteString("  " + yamlPlainScalar(service) + ":\n")
	b.WriteString("    source: app\n")
	b.WriteString("    build:\n")
	b.WriteString("      context: " + yamlPlainScalar(req.BuildContext) + "\n")
	b.WriteString("      dockerfile: " + yamlPlainScalar(req.Dockerfile) + "\n")
	if strings.TrimSpace(req.Command) != "" {
		b.WriteString("    command: " + yamlPlainScalar(req.Command) + "\n")
	}
	b.WriteString(fmt.Sprintf("    port: %d\n", req.Port))
	b.WriteString("    health:\n")
	b.WriteString("      path: " + yamlPlainScalar(req.HealthPath) + "\n")
	b.WriteString("      expectStatus: 200\n\n")
	b.WriteString("agent:\n")
	b.WriteString("  defaultPreviewService: " + yamlPlainScalar(service) + "\n")
	b.WriteString("  smokeTests:\n")
	b.WriteString("    - name: homepage\n")
	b.WriteString("      service: " + yamlPlainScalar(service) + "\n")
	b.WriteString("      path: " + yamlPlainScalar(req.HealthPath) + "\n")
	b.WriteString("      expectStatus: 200\n")
	return b.String()
}

func yamlPlainScalar(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "''"
	}
	if yamlScalarNeedsQuotes(s) {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return s
}

func yamlScalarNeedsQuotes(s string) bool {
	if strings.TrimSpace(s) != s {
		return true
	}
	if strings.ContainsAny(s, "\n\r\t#{}[]&*!|>\"%@`") {
		return true
	}
	if strings.Contains(s, ": ") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "?") || strings.HasPrefix(s, ":") {
		return true
	}
	switch strings.ToLower(s) {
	case "true", "false", "null", "~", "yes", "no", "on", "off":
		return true
	}
	return false
}

func shellPathForSuggestion(path string) string {
	if strings.ContainsAny(path, " \t\n\r'\"") {
		return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
	}
	return path
}

func initHuman(result InitConfigResult) string {
	var b strings.Builder
	b.WriteString("initialized Vivero config: " + result.Path + "\n")
	for _, cmd := range result.NextCommands {
		b.WriteString("next: " + cmd + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
