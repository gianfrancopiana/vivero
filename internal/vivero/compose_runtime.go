package vivero

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type dockerComposeServiceSpec struct {
	Project        string
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
	Volumes        []VolumeConfig
}

type dockerComposeConfigModel struct {
	Services map[string]dockerComposeConfigService `json:"services"`
}

type dockerComposeConfigService struct {
	DependsOn map[string]json.RawMessage `json:"depends_on"`
	Ports     []map[string]any           `json:"ports"`
}

type dockerComposeVolumeBinding struct {
	Key    string
	Source string
	Target string
}

const composeExpectedCompletionLabel = "vivero.compose.expected-completion"

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

func startDockerComposeService(home, projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string) (string, error) {
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
		Project:        projectName,
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
		Volumes:        svc.DependencyVolumes,
	}
	services, err := dockerComposeConfigServices(spec)
	if err != nil {
		return "", err
	}
	if !stringInSlice(spec.ComposeService, services) {
		return "", fmt.Errorf("compose service %s not found in %s", spec.ComposeService, strings.Join(spec.Files, ", "))
	}
	model, err := dockerComposeConfig(spec)
	if err != nil {
		return "", err
	}
	if err := ensureDockerComposeDependencyVolumes(spec); err != nil {
		return "", err
	}
	if err := writeDockerComposeOverride(spec, services, dockerComposeExpectedCompletionServices(model, spec.ComposeService)); err != nil {
		return "", err
	}
	defer os.Remove(spec.OverrideFile)
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

func runDockerComposeOneShot(home, projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string, command RuntimeCommand) ([]byte, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker CLI not found; install Docker or OrbStack so Vivero can run Compose setup steps: %w", err)
	}
	if strings.TrimSpace(svc.Source) == "" {
		return nil, fmt.Errorf("compose service %s must declare source", service)
	}
	src, ok := sources[svc.Source]
	if !ok {
		return nil, fmt.Errorf("service %s references unknown source %s", service, svc.Source)
	}
	resolved, err := resolveComposeFiles(src.Path, svc.Compose)
	if err != nil {
		return nil, fmt.Errorf("service %s compose files: %w", service, err)
	}
	ports, err := servicePortPlan(svc)
	if err != nil {
		return nil, err
	}
	spec := dockerComposeServiceSpec{
		Project:        projectName,
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
		Volumes:        svc.DependencyVolumes,
	}
	services, err := dockerComposeConfigServices(spec)
	if err != nil {
		return nil, err
	}
	if !stringInSlice(spec.ComposeService, services) {
		return nil, fmt.Errorf("compose service %s not found in %s", spec.ComposeService, strings.Join(spec.Files, ", "))
	}
	model, err := dockerComposeConfig(spec)
	if err != nil {
		return nil, err
	}
	if err := ensureDockerComposeDependencyVolumes(spec); err != nil {
		return nil, err
	}
	if err := writeDockerComposeOverride(spec, services, dockerComposeExpectedCompletionServices(model, spec.ComposeService)); err != nil {
		return nil, err
	}
	defer os.Remove(spec.OverrideFile)

	args := dockerComposeBaseArgs(spec)
	args = append(args, "ps", "-q", spec.ComposeService)
	psOut, psErr := runCmd(spec.Source, env, "docker", args...)
	running := psErr == nil && firstNonEmptyLine(string(psOut)) != ""
	args = dockerComposeBaseArgs(spec)
	if running {
		args = append(args, "exec", "-T", spec.ComposeService)
	} else {
		args = append(args, "run", "--rm", "-T", spec.ComposeService)
	}
	args = append(args, command.RuntimeArgs()...)
	out, err := runCmd(spec.Source, env, "docker", args...)
	if !running {
		if err != nil {
			lines, logsErr := dockerComposeProjectLogs(previewID, service, serviceFailureLogTail)
			if len(lines) > 0 {
				out = append(out, []byte("\n--- compose dependency logs before setup cleanup ---\n")...)
				out = append(out, []byte(strings.Join(redactRuntimeLogLines(lines, env), "\n"))...)
				out = append(out, '\n')
			}
			if logsErr != nil {
				out = append(out, []byte("compose log snapshot failed: "+logsErr.Error()+"\n")...)
			}
		}
		cleanupErr := removeDockerComposeProject(previewID, service)
		if cleanupErr != nil {
			if err != nil {
				return out, fmt.Errorf("docker %s failed: %w: %s; compose setup cleanup failed: %v", strings.Join(args, " "), err, strings.TrimSpace(string(out)), cleanupErr)
			}
			return out, fmt.Errorf("compose setup cleanup failed: %w", cleanupErr)
		}
	}
	if err != nil {
		return out, fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
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

func dockerComposeConfig(spec dockerComposeServiceSpec) (dockerComposeConfigModel, error) {
	args := []string{"compose"}
	for _, file := range spec.Files {
		args = append(args, "-f", file)
	}
	args = append(args, "-p", spec.ComposeProject, "config", "--format", "json")
	out, err := runCmd(spec.Source, spec.Env, "docker", args...)
	if err != nil {
		return dockerComposeConfigModel{}, fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	var model dockerComposeConfigModel
	if err := json.Unmarshal(out, &model); err != nil {
		return dockerComposeConfigModel{}, fmt.Errorf("decode docker compose config: %w", err)
	}
	if len(model.Services) == 0 {
		return dockerComposeConfigModel{}, fmt.Errorf("docker %s returned no services", strings.Join(args, " "))
	}
	return model, nil
}

func dockerComposeTargetClosure(model dockerComposeConfigModel, target string) map[string]bool {
	closure := map[string]bool{}
	var visit func(string)
	visit = func(service string) {
		if closure[service] {
			return
		}
		closure[service] = true
		for dependency := range model.Services[service].DependsOn {
			visit(dependency)
		}
	}
	visit(target)
	return closure
}

func dockerComposeExpectedCompletionServices(model dockerComposeConfigModel, target string) map[string]bool {
	completed := map[string]bool{}
	requiresRunning := map[string]bool{target: true}
	visited := map[string]bool{}
	var visit func(string)
	visit = func(service string) {
		if visited[service] {
			return
		}
		visited[service] = true
		for dependency, raw := range model.Services[service].DependsOn {
			condition := ""
			var decoded struct {
				Condition string `json:"condition"`
			}
			if json.Unmarshal(raw, &decoded) == nil {
				condition = strings.ToLower(strings.TrimSpace(decoded.Condition))
			}
			if condition == "service_completed_successfully" {
				completed[dependency] = true
			} else {
				requiresRunning[dependency] = true
			}
			visit(dependency)
		}
	}
	visit(target)
	for service := range requiresRunning {
		delete(completed, service)
	}
	return completed
}

func dockerComposeOmittedServices(model dockerComposeConfigModel, target string) []string {
	closure := dockerComposeTargetClosure(model, target)
	omitted := []string{}
	for _, service := range sortedMapKeys(model.Services) {
		if !closure[service] {
			omitted = append(omitted, service)
		}
	}
	return omitted
}

func dockerComposePublishedPorts(service dockerComposeConfigService) []string {
	ports := []string{}
	for _, port := range service.Ports {
		published, ok := port["published"]
		if !ok || published == nil {
			continue
		}
		value := strings.TrimSpace(fmt.Sprint(published))
		if value != "" && value != "0" {
			ports = append(ports, value)
		}
	}
	return ports
}

func dockerComposeOverridePath(home, previewID, service string) string {
	name := strings.ReplaceAll(dockerResourceName("compose", previewID, service), ".", "-") + ".override.yml"
	return filepath.Join(home, "run", "compose", name)
}

func writeDockerComposeOverride(spec dockerComposeServiceSpec, services []string, expectedCompletion map[string]bool) error {
	if err := ensureDir(filepath.Dir(spec.OverrideFile)); err != nil {
		return err
	}
	overrideServices := map[string]any{}
	for _, service := range services {
		labels := map[string]string{
			"vivero.preview":               spec.PreviewID,
			composeExpectedCompletionLabel: "false",
		}
		if expectedCompletion[service] {
			labels[composeExpectedCompletionLabel] = "true"
		}
		entry := map[string]any{"labels": labels, "ports": dockerComposeTaggedSequence("!reset", nil)}
		if service == spec.ComposeService {
			labels["vivero.service"] = spec.Service
			if len(spec.NetworkAliases) > 0 {
				entry["networks"] = map[string]any{"default": map[string]any{"aliases": spec.NetworkAliases}}
			}
			if len(spec.Ports) > 0 {
				entry["ports"] = dockerComposeTaggedSequence("!override", dockerComposePortBindings(spec.Ports))
			}
			if len(spec.Env) > 0 {
				entry["environment"] = sortedMapKeys(spec.Env)
			}
			bindings, err := dockerComposeDependencyVolumeBindings(spec)
			if err != nil {
				return err
			}
			if len(bindings) > 0 {
				mounts := make([]map[string]string, 0, len(bindings))
				for _, binding := range bindings {
					mounts = append(mounts, map[string]string{"type": "volume", "source": binding.Key, "target": binding.Target})
				}
				entry["volumes"] = mounts
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
	bindings, err := dockerComposeDependencyVolumeBindings(spec)
	if err != nil {
		return err
	}
	if len(bindings) > 0 {
		volumes := map[string]any{}
		for _, binding := range bindings {
			volumes[binding.Key] = map[string]any{"name": binding.Source, "external": true}
		}
		doc["volumes"] = volumes
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return atomicWriteFile(spec.OverrideFile, body, 0o600)
}

func dockerComposeTaggedSequence(tag string, values []string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: tag, Style: yaml.FlowStyle}
	for _, value := range values {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
	return node
}

func dockerComposeDependencyVolumeBindings(spec dockerComposeServiceSpec) ([]dockerComposeVolumeBinding, error) {
	bindings := []dockerComposeVolumeBinding{}
	for i, volume := range spec.Volumes {
		mount, ok, err := dockerVolumeMountArg(spec.Project, spec.PreviewID, spec.Service, volume)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		var source, target string
		for _, field := range strings.Split(mount, ",") {
			if value, found := strings.CutPrefix(field, "source="); found {
				source = value
			}
			if value, found := strings.CutPrefix(field, "target="); found {
				target = value
			}
		}
		if source == "" || target == "" {
			return nil, fmt.Errorf("invalid dependency volume mount %q", mount)
		}
		bindings = append(bindings, dockerComposeVolumeBinding{Key: "vivero_dependency_" + strconv.Itoa(i), Source: source, Target: target})
	}
	return bindings, nil
}

func ensureDockerComposeDependencyVolumes(spec dockerComposeServiceSpec) error {
	bindings, err := dockerComposeDependencyVolumeBindings(spec)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if err := ensureDockerVolume(binding.Source); err != nil {
			return fmt.Errorf("ensure compose dependency volume %s: %w", binding.Source, err)
		}
	}
	return nil
}

func dockerComposePortBindings(ports []ServicePort) []string {
	bindings := make([]string, 0, len(ports))
	for _, port := range ports {
		hostIP := strings.TrimSpace(port.HostIP)
		if hostIP == "" {
			hostIP = "127.0.0.1"
		}
		protocol := strings.TrimSpace(port.Protocol)
		if protocol == "" {
			protocol = "tcp"
		}
		container := fmt.Sprint(port.Container)
		if protocol != "tcp" {
			container += "/" + protocol
		}
		if port.Host > 0 {
			bindings = append(bindings, fmt.Sprintf("%s:%d:%s", hostIP, port.Host, container))
			continue
		}
		bindings = append(bindings, fmt.Sprintf("%s::%s", hostIP, container))
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
	return removeDockerComposeProjectWithOptions(previewID, service, false)
}

func dockerComposeProjectContainers(previewID, service string) ([]runtimeContainerState, error) {
	if strings.TrimSpace(previewID) == "" || strings.TrimSpace(service) == "" {
		return nil, nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker is required: %w", err)
	}
	project := dockerComposeProjectName(previewID, service)
	out, err := runCmd("", nil, "docker", "ps", "-aq", "--no-trunc",
		"--filter", "label=com.docker.compose.project="+project,
		"--filter", "label=vivero.preview="+previewID)
	if err != nil {
		return nil, fmt.Errorf("docker ps for compose project %s: %w: %s", project, err, strings.TrimSpace(string(out)))
	}
	ids := nonEmptyLines(string(out))
	states := make([]runtimeContainerState, 0, len(ids))
	for _, containerID := range ids {
		format := `{{.State.Running}}|{{.State.ExitCode}}|{{index .Config.Labels "` + composeExpectedCompletionLabel + `"}}`
		out, stateErr := runCmd("", nil, "docker", "container", "inspect", "--format", format, containerID)
		if stateErr != nil {
			return nil, fmt.Errorf("inspect compose project %s container %s: %w: %s", project, containerID, stateErr, strings.TrimSpace(string(out)))
		}
		fields := strings.Split(strings.TrimSpace(string(out)), "|")
		if len(fields) != 3 || (fields[0] != "true" && fields[0] != "false") {
			return nil, fmt.Errorf("inspect compose project %s container %s returned invalid state %q", project, containerID, strings.TrimSpace(string(out)))
		}
		exitCode, parseErr := strconv.Atoi(fields[1])
		if parseErr != nil {
			return nil, fmt.Errorf("inspect compose project %s container %s returned invalid exit code %q", project, containerID, fields[1])
		}
		states = append(states, runtimeContainerState{ID: containerID, Running: fields[0] == "true", ExitCode: exitCode, ExpectedCompletion: fields[2] == "true"})
	}
	return states, nil
}

func removeDockerComposeProjectWithOptions(previewID, service string, discardVolumes bool) error {
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
	if err := removeDockerComposeNetworks(project); err != nil {
		errs = append(errs, err.Error())
	}
	if discardVolumes {
		if err := removeDockerComposeVolumes(project); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func removeDockerComposeNetworks(project string) error {
	out, err := runCmd("", nil, "docker", "network", "ls", "-q", "--filter", "label=com.docker.compose.project="+project)
	if err != nil {
		return fmt.Errorf("docker network ls for compose project %s: %w: %s", project, err, strings.TrimSpace(string(out)))
	}
	var errs []string
	for _, network := range nonEmptyLines(string(out)) {
		body, removeErr := runCmd("", nil, "docker", "network", "rm", network)
		if removeErr != nil && !strings.Contains(strings.ToLower(string(body)), "no such network") && !strings.Contains(strings.ToLower(string(body)), "not found") {
			errs = append(errs, fmt.Sprintf("docker network rm %s: %v: %s", network, removeErr, strings.TrimSpace(string(body))))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("compose project %s networks: %s", project, strings.Join(errs, "; "))
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

func dockerComposeProjectLogs(previewID, service string, limit int) ([]string, error) {
	if strings.TrimSpace(previewID) == "" || strings.TrimSpace(service) == "" {
		return nil, nil
	}
	project := dockerComposeProjectName(previewID, service)
	out, err := runCmd("", nil, "docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+project)
	if err != nil {
		return nil, fmt.Errorf("docker ps for compose project %s: %w: %s", project, err, strings.TrimSpace(string(out)))
	}
	lines := []string{}
	errors := []string{}
	for _, containerID := range nonEmptyLines(string(out)) {
		containerLines, logErr := dockerLogs(containerID, limit)
		if logErr != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", containerID, logErr))
			continue
		}
		lines = append(lines, fmt.Sprintf("--- compose container %s ---", containerID))
		lines = append(lines, containerLines...)
	}
	if len(errors) > 0 {
		return lines, fmt.Errorf("compose project %s logs: %s", project, strings.Join(errors, "; "))
	}
	return lines, nil
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
