package vivero

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type dockerComposeServiceSpec struct {
	PreviewID      string
	Service        string
	ComposeProject string
	ComposeService string
	Source         string
	Files          []string
	OverrideFile   string
	Network        string
	NetworkAliases []string
	Ports          []ServicePort
	Env            map[string]string
}

func composeFiles(cfg ComposeConfig) []string {
	out := []string{}
	if file := strings.TrimSpace(cfg.File); file != "" {
		out = append(out, file)
	}
	for _, file := range cfg.Files {
		if trimmed := strings.TrimSpace(file); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func composeServiceName(cfg ComposeConfig, service string) string {
	if name := strings.TrimSpace(cfg.Service); name != "" {
		return name
	}
	return service
}

func composeNetworkAliases(service, composeService string) []string {
	aliases := []string{}
	seen := map[string]bool{}
	for _, value := range []string{service, composeService} {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		aliases = append(aliases, trimmed)
		seen[trimmed] = true
	}
	return aliases
}

func dockerComposeProjectName(previewID, service string) string {
	return strings.ReplaceAll(dockerResourceName("vivero", "compose", previewID, service), ".", "-")
}

func startDockerComposeService(home, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string) (string, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("docker CLI not found; install Docker or OrbStack so Vivero can run Compose previews: %w", err)
	}
	if strings.TrimSpace(svc.Source) == "" {
		return "", fmt.Errorf("compose service %s must declare source", service)
	}
	src, ok := sources[svc.Source]
	if !ok {
		return "", fmt.Errorf("service %s references unknown source %s", service, svc.Source)
	}
	if strings.TrimSpace(src.Path) == "" {
		return "", fmt.Errorf("source %s has no path for compose service %s", svc.Source, service)
	}
	resolved, err := resolveComposeFiles(src.Path, svc.Compose)
	if err != nil {
		return "", fmt.Errorf("service %s compose files: %w", service, err)
	}
	ports, err := servicePortPlan(svc)
	if err != nil {
		return "", err
	}
	spec := dockerComposeServiceSpec{
		PreviewID:      previewID,
		Service:        service,
		ComposeProject: dockerComposeProjectName(previewID, service),
		ComposeService: composeServiceName(svc.Compose, service),
		Source:         src.Path,
		Files:          resolved,
		OverrideFile:   dockerComposeOverridePath(home, previewID, service),
		Network:        dockerNetworkName(previewID),
		NetworkAliases: composeNetworkAliases(service, composeServiceName(svc.Compose, service)),
		Ports:          ports,
		Env:            env,
	}
	services, err := dockerComposeConfigServices(spec)
	if err != nil {
		return "", err
	}
	if !stringInSlice(spec.ComposeService, services) {
		return "", fmt.Errorf("compose service %s not found in %s", spec.ComposeService, strings.Join(spec.Files, ", "))
	}
	if err := writeDockerComposeOverride(spec, services); err != nil {
		return "", err
	}
	args := dockerComposeBaseArgs(spec)
	args = append(args, "up", "--detach", "--remove-orphans", spec.ComposeService)
	out, err := runCmd(spec.Source, env, "docker", args...)
	if err != nil {
		return "", fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	args = dockerComposeBaseArgs(spec)
	args = append(args, "ps", "-q", spec.ComposeService)
	out, err = runCmd(spec.Source, env, "docker", args...)
	if err != nil {
		return "", fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	containerID := firstNonEmptyLine(string(out))
	if containerID == "" {
		return "", fmt.Errorf("docker %s did not return a container id", strings.Join(args, " "))
	}
	return containerID, nil
}

func resolveComposeFiles(sourcePath string, cfg ComposeConfig) ([]string, error) {
	files := composeFiles(cfg)
	if len(files) == 0 {
		return nil, fmt.Errorf("compose.file or compose.files is required")
	}
	resolved := make([]string, 0, len(files))
	for _, file := range files {
		path, err := resolveProjectPath(sourcePath, file)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, path)
	}
	return resolved, nil
}

func dockerComposeBaseArgs(spec dockerComposeServiceSpec) []string {
	args := []string{"compose"}
	for _, file := range spec.Files {
		args = append(args, "-f", file)
	}
	if spec.OverrideFile != "" {
		args = append(args, "-f", spec.OverrideFile)
	}
	args = append(args, "-p", spec.ComposeProject)
	return args
}

func dockerComposeConfigServices(spec dockerComposeServiceSpec) ([]string, error) {
	args := []string{"compose"}
	for _, file := range spec.Files {
		args = append(args, "-f", file)
	}
	args = append(args, "-p", spec.ComposeProject, "config", "--services")
	out, err := runCmd(spec.Source, spec.Env, "docker", args...)
	if err != nil {
		return nil, fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	services := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			services = append(services, trimmed)
		}
	}
	if len(services) == 0 {
		return nil, fmt.Errorf("docker %s returned no services", strings.Join(args, " "))
	}
	return services, nil
}

func dockerComposeOverridePath(home, previewID, service string) string {
	name := strings.ReplaceAll(dockerResourceName("compose", previewID, service), ".", "-") + ".override.yml"
	return filepath.Join(home, "run", "compose", name)
}

func writeDockerComposeOverride(spec dockerComposeServiceSpec, services []string) error {
	if err := ensureDir(filepath.Dir(spec.OverrideFile)); err != nil {
		return err
	}
	overrideServices := map[string]any{}
	for _, service := range services {
		labels := map[string]string{"vivero.preview": spec.PreviewID}
		entry := map[string]any{"labels": labels}
		if service == spec.ComposeService {
			labels["vivero.service"] = spec.Service
			if len(spec.NetworkAliases) > 0 {
				entry["networks"] = map[string]any{"default": map[string]any{"aliases": spec.NetworkAliases}}
			}
			if len(spec.Ports) > 0 {
				entry["ports"] = dockerComposePortBindings(spec.Ports)
			}
			if len(spec.Env) > 0 {
				entry["environment"] = spec.Env
			}
		}
		overrideServices[service] = entry
	}
	doc := map[string]any{
		"services": overrideServices,
		"networks": map[string]any{
			"default": map[string]any{
				"name":     spec.Network,
				"external": true,
			},
		},
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(spec.OverrideFile, body, 0o600)
}

func dockerComposePortBindings(ports []ServicePort) []string {
	bindings := make([]string, 0, len(ports))
	for _, port := range ports {
		protocol := strings.TrimSpace(port.Protocol)
		if protocol == "" {
			protocol = "tcp"
		}
		container := fmt.Sprint(port.Container)
		if protocol != "tcp" {
			container += "/" + protocol
		}
		if port.Host > 0 {
			bindings = append(bindings, fmt.Sprintf("127.0.0.1:%d:%s", port.Host, container))
			continue
		}
		bindings = append(bindings, fmt.Sprintf("127.0.0.1::%s", container))
	}
	return bindings
}

func stringInSlice(needle string, haystack []string) bool {
	for _, value := range haystack {
		if value == needle {
			return true
		}
	}
	return false
}

func removeDockerComposeProject(previewID, service string) error {
	if strings.TrimSpace(previewID) == "" || strings.TrimSpace(service) == "" {
		return nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	project := dockerComposeProjectName(previewID, service)
	var errs []string
	if err := removeDockerComposeContainers(project); err != nil {
		errs = append(errs, err.Error())
	}
	if err := removeDockerComposeVolumes(project); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func removeDockerComposeContainers(project string) error {
	out, err := runCmd("", nil, "docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+project)
	if err != nil {
		return fmt.Errorf("docker ps for compose project %s: %w: %s", project, err, strings.TrimSpace(string(out)))
	}
	ids := nonEmptyLines(string(out))
	if len(ids) == 0 {
		return nil
	}
	args := append([]string{"rm", "-f"}, ids...)
	body, err := runCmd("", nil, "docker", args...)
	if err != nil && !isDockerNoSuchContainer(string(body)) {
		return fmt.Errorf("docker rm compose project %s: %w: %s", project, err, strings.TrimSpace(string(body)))
	}
	return nil
}

func removeDockerComposeVolumes(project string) error {
	out, err := runCmd("", nil, "docker", "volume", "ls", "-q", "--filter", "label=com.docker.compose.project="+project)
	if err != nil {
		return fmt.Errorf("docker volume ls for compose project %s: %w: %s", project, err, strings.TrimSpace(string(out)))
	}
	volumes := nonEmptyLines(string(out))
	for _, volume := range volumes {
		if err := removeDockerVolume(volume); err != nil {
			return err
		}
	}
	return nil
}

func nonEmptyLines(value string) []string {
	lines := []string{}
	for _, line := range strings.Split(value, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
