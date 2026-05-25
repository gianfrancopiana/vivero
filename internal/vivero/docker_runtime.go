package vivero

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type dockerServiceSpec struct {
	Project   string
	PreviewID string
	Service   string
	Image     string
	Command   RuntimeCommand
	Source    string
	Workdir   string
	Port      int
	Ports     []ServicePort
	Env       map[string]string
	EnvFile   string
	Volumes   []VolumeConfig
	Resources ResourceLimits
	Network   string
	Alias     string
}

type dockerBuildSpec struct {
	Tag          string
	Context      string
	Dockerfile   string
	Args         map[string]string
	Engine       string
	CacheEnabled bool
	CacheFrom    []string
	CacheTo      []string
}

func dockerRunArgs(spec dockerServiceSpec) ([]string, error) {
	if strings.TrimSpace(spec.Image) == "" {
		return nil, fmt.Errorf("service %s must declare image", spec.Service)
	}
	args := []string{"run", "--detach", "--name", dockerContainerName(spec.PreviewID, spec.Service)}
	args = append(args, "--label", "vivero.preview="+spec.PreviewID, "--label", "vivero.service="+spec.Service)
	for _, port := range dockerPortsForSpec(spec) {
		publish := fmt.Sprintf("127.0.0.1::%d", port.Container)
		if port.Host > 0 {
			publish = fmt.Sprintf("127.0.0.1:%d:%d", port.Host, port.Container)
		}
		if port.Protocol != "" && port.Protocol != "tcp" {
			publish += "/" + port.Protocol
		}
		args = append(args, "--publish", publish)
	}
	args = appendDockerNetworkArgs(args, spec)
	if spec.Source != "" {
		args = append(args, "--volume", spec.Source+":/app")
		workdir := "/app"
		if strings.TrimSpace(spec.Workdir) != "" {
			workdir = filepath.ToSlash(filepath.Join(workdir, spec.Workdir))
		}
		args = append(args, "--workdir", workdir)
	} else if strings.TrimSpace(spec.Workdir) != "" {
		args = append(args, "--workdir", spec.Workdir)
	}
	for _, vol := range spec.Volumes {
		mount, ok, err := dockerVolumeMountArg(spec.Project, spec.PreviewID, spec.Service, vol)
		if err != nil {
			return nil, err
		}
		if ok {
			args = append(args, "--mount", mount)
		}
	}
	if spec.Resources.CPUs != "" {
		args = append(args, "--cpus", spec.Resources.CPUs)
	}
	if spec.Resources.Memory != "" {
		args = append(args, "--memory", spec.Resources.Memory)
	}
	var err error
	args, err = appendDockerEnvArgs(args, spec)
	if err != nil {
		return nil, err
	}
	args = append(args, spec.Image)
	if !spec.Command.IsZero() {
		args = append(args, spec.Command.RuntimeArgs()...)
	}
	return args, nil
}

func dockerPortsForSpec(spec dockerServiceSpec) []ServicePort {
	if len(spec.Ports) > 0 {
		return spec.Ports
	}
	if spec.Port > 0 {
		return []ServicePort{{Name: defaultPrimaryPortName, Container: spec.Port, Host: spec.Port, Protocol: "tcp", Primary: true, Legacy: true}}
	}
	return nil
}

func dockerRunOnceArgs(spec dockerServiceSpec, command RuntimeCommand) ([]string, error) {
	if strings.TrimSpace(spec.Image) == "" {
		return nil, fmt.Errorf("service %s must declare image", spec.Service)
	}
	args := []string{"run", "--rm", "--name", dockerOneShotContainerName(spec.PreviewID, spec.Service, command)}
	args = append(args, "--label", "vivero.preview="+spec.PreviewID, "--label", "vivero.service="+spec.Service)
	args = appendDockerNetworkArgs(args, spec)
	if spec.Source != "" {
		args = append(args, "--volume", spec.Source+":/app")
		workdir := "/app"
		if strings.TrimSpace(spec.Workdir) != "" {
			workdir = filepath.ToSlash(filepath.Join(workdir, spec.Workdir))
		}
		args = append(args, "--workdir", workdir)
	}
	for _, vol := range spec.Volumes {
		mount, ok, err := dockerVolumeMountArg(spec.Project, spec.PreviewID, spec.Service, vol)
		if err != nil {
			return nil, err
		}
		if ok {
			args = append(args, "--mount", mount)
		}
	}
	if spec.Resources.CPUs != "" {
		args = append(args, "--cpus", spec.Resources.CPUs)
	}
	if spec.Resources.Memory != "" {
		args = append(args, "--memory", spec.Resources.Memory)
	}
	var err error
	args, err = appendDockerEnvArgs(args, spec)
	if err != nil {
		return nil, err
	}
	args = append(args, spec.Image)
	args = append(args, command.RuntimeArgs()...)
	return args, nil
}

func appendDockerNetworkArgs(args []string, spec dockerServiceSpec) []string {
	if strings.TrimSpace(spec.Network) != "" {
		args = append(args, "--network", spec.Network)
	}
	if strings.TrimSpace(spec.Alias) != "" {
		args = append(args, "--network-alias", spec.Alias)
	}
	return args
}

func dockerBuildArgs(spec dockerBuildSpec) ([]string, error) {
	if strings.TrimSpace(spec.Tag) == "" {
		return nil, fmt.Errorf("docker build tag is required")
	}
	if strings.TrimSpace(spec.Context) == "" {
		return nil, fmt.Errorf("docker build context is required")
	}
	args := []string{"build", "--tag", spec.Tag}
	if dockerBuildEngine(spec) == dockerBuildEngineBuildx {
		args = []string{"buildx", "build", "--load", "--tag", spec.Tag}
	}
	if strings.TrimSpace(spec.Dockerfile) != "" {
		args = append(args, "--file", spec.Dockerfile)
	}
	for _, k := range sortedMapKeys(spec.Args) {
		args = append(args, "--build-arg", k+"="+spec.Args[k])
	}
	if dockerBuildEngine(spec) == dockerBuildEngineBuildx {
		for _, cacheFrom := range spec.CacheFrom {
			args = append(args, "--cache-from", cacheFrom)
		}
		for _, cacheTo := range spec.CacheTo {
			args = append(args, "--cache-to", cacheTo)
		}
	}
	args = append(args, spec.Context)
	return args, nil
}

func buildDockerImage(spec dockerBuildSpec) error {
	args, err := dockerBuildArgs(spec)
	if err != nil {
		return err
	}
	if dockerBuildEngine(spec) == dockerBuildEngineBuildx {
		if err := ensureDockerBuildxAvailable(); err != nil {
			return err
		}
	}
	stdout, stderr, err := runDocker(args)
	if err != nil {
		return fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr+"\n"+stdout))
	}
	return nil
}

func ensureDockerBuildxAvailable() error {
	stdout, stderr, err := runDocker([]string{"buildx", "version"})
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(stderr + "\n" + stdout)
	if detail != "" {
		return fmt.Errorf("docker buildx is required for build cache but is not available: %w: %s", err, detail)
	}
	return fmt.Errorf("docker buildx is required for build cache but is not available: %w", err)
}

func (a *App) startDockerService(projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string) (string, error) {
	return a.containerRuntime().StartService(a.Home, projectName, previewID, service, svc, sources, env)
}

func startDockerService(home, projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string) (string, error) {
	if serviceRuntime(svc) == "compose" {
		return startDockerComposeService(home, previewID, service, svc, sources, env)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("docker CLI not found; install Docker or OrbStack so Vivero can run containers: %w", err)
	}
	spec, err := dockerSpecForService(projectName, previewID, service, svc, sources, env)
	if err != nil {
		return "", err
	}
	if err := spec.writeEnvFile(dockerEnvFilePath(home, previewID, service)); err != nil {
		return "", err
	}
	if spec.EnvFile != "" {
		defer os.Remove(spec.EnvFile)
	}
	containerName := dockerContainerName(previewID, service)
	_, _ = runCmd("", nil, "docker", "rm", "-f", containerName)
	args, err := dockerRunArgs(spec)
	if err != nil {
		return "", err
	}
	stdout, stderr, err := runDocker(args)
	if err != nil {
		return "", fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr+"\n"+stdout))
	}
	containerID := firstNonEmptyLine(stdout)
	if containerID == "" {
		return "", fmt.Errorf("docker %s did not return a container id: %s", strings.Join(args, " "), strings.TrimSpace(stderr))
	}
	return containerID, nil
}

func (a *App) runDockerOneShot(projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string, command RuntimeCommand) ([]byte, error) {
	return a.containerRuntime().RunOneShot(a.Home, projectName, previewID, service, svc, sources, env, command)
}

func runDockerOneShot(home, projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string, command RuntimeCommand) ([]byte, error) {
	if serviceRuntime(svc) == "compose" {
		return nil, fmt.Errorf("setup.afterSeeds does not support runtime compose for service %s; keep setup in the app-owned Compose entrypoint or scripts", service)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker CLI not found; install Docker or OrbStack so Vivero can run containers: %w", err)
	}
	spec, err := dockerSpecForService(projectName, previewID, service, svc, sources, env)
	if err != nil {
		return nil, err
	}
	if err := spec.writeEnvFile(dockerEnvFilePath(home, previewID, service)); err != nil {
		return nil, err
	}
	if spec.EnvFile != "" {
		defer os.Remove(spec.EnvFile)
	}
	args, err := dockerRunOnceArgs(spec, command)
	if err != nil {
		return nil, err
	}
	stdout, stderr, err := runDocker(args)
	combined := []byte(stdout + stderr)
	if err != nil {
		return combined, fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr+"\n"+stdout))
	}
	return combined, nil
}

func runDocker(args []string) (stdout, stderr string, err error) {
	cmd := exec.Command("docker", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	return out.String(), errBuf.String(), runErr
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func dockerPublishedPorts(containerID string, ports []ServicePort) ([]PreviewPort, error) {
	if strings.TrimSpace(containerID) == "" {
		return nil, fmt.Errorf("container id is required")
	}
	out := make([]PreviewPort, 0, len(ports))
	for _, port := range ports {
		protocol := port.Protocol
		if protocol == "" {
			protocol = "tcp"
		}
		hostPort := port.Host
		hostIP := "127.0.0.1"
		if hostPort <= 0 {
			query := fmt.Sprintf("%d/%s", port.Container, protocol)
			body, err := runCmd("", nil, "docker", "port", containerID, query)
			if err != nil {
				return nil, fmt.Errorf("docker port %s %s: %w: %s", containerID, query, err, strings.TrimSpace(string(body)))
			}
			line := firstNonEmptyLine(string(body))
			var parseErr error
			hostPort, hostIP, parseErr = parseDockerPublishedPort(line)
			if parseErr != nil {
				return nil, parseErr
			}
		}
		out = append(out, PreviewPort{Name: port.Name, Container: port.Container, Host: hostPort, HostIP: hostIP, Protocol: protocol, Primary: port.Primary})
	}
	return out, nil
}

func dockerSpecForService(projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string) (dockerServiceSpec, error) {
	ports, err := servicePortPlan(svc)
	if err != nil {
		return dockerServiceSpec{}, err
	}
	spec := dockerServiceSpec{
		Project:   projectName,
		PreviewID: previewID,
		Service:   service,
		Image:     svc.Image,
		Command:   svc.Command,
		Workdir:   svc.WorkingDir,
		Port:      svc.Port,
		Ports:     ports,
		Env:       env,
		Volumes:   svc.DependencyVolumes,
		Resources: svc.ResourceLimits,
		Network:   dockerNetworkName(previewID),
		Alias:     service,
	}
	if svc.Source != "" {
		src, ok := sources[svc.Source]
		if !ok {
			return spec, fmt.Errorf("service %s references unknown source %s", service, svc.Source)
		}
		spec.Source = src.Path
	}
	return spec, nil
}

func dockerEnvFilePath(home, previewID, service string) string {
	name := strings.ReplaceAll(dockerResourceName("env", previewID, service), ".", "-") + ".env"
	return filepath.Join(home, "run", "docker", name)
}

func (spec *dockerServiceSpec) writeEnvFile(path string) error {
	if len(spec.Env) == 0 {
		spec.EnvFile = ""
		return nil
	}
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	keys := sortedMapKeys(spec.Env)
	var b strings.Builder
	for _, k := range keys {
		if err := validateDockerEnvName(k); err != nil {
			return err
		}
		v := spec.Env[k]
		if strings.ContainsAny(v, "\x00\n\r") {
			return fmt.Errorf("docker env value for %s contains unsupported newline or NUL", k)
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(v)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	spec.EnvFile = path
	return nil
}

func appendDockerEnvArgs(args []string, spec dockerServiceSpec) ([]string, error) {
	keys := sortedMapKeys(spec.Env)
	for _, k := range keys {
		if err := validateDockerEnvName(k); err != nil {
			return nil, err
		}
	}
	if spec.EnvFile != "" {
		return append(args, "--env-file", spec.EnvFile), nil
	}
	for _, k := range keys {
		args = append(args, "--env", k)
	}
	return args, nil
}

func dockerExec(containerID string, cmdArgs []string) (stdout, stderr string, exit int, err error) {
	return dockerExecWithTimeout(containerID, cmdArgs, 30*time.Minute)
}

func dockerExecWithTimeout(containerID string, cmdArgs []string, timeout time.Duration) (stdout, stderr string, exit int, err error) {
	if strings.TrimSpace(containerID) == "" {
		return "", "", 0, fmt.Errorf("container id is required")
	}
	if timeout <= 0 {
		return "", "", 0, context.DeadlineExceeded
	}
	args := append([]string{"exec", containerID}, cmdArgs...)
	cmd := exec.Command("docker", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return out.String(), errBuf.String(), 0, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var runErr error
	select {
	case runErr = <-done:
	case <-timer.C:
		if cmd.Process != nil {
			_ = killProcessGroup(cmd.Process.Pid)
			_ = cmd.Process.Kill()
		}
		<-done
		return out.String(), errBuf.String(), 0, context.DeadlineExceeded
	}
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			return out.String(), errBuf.String(), ee.ExitCode(), nil
		}
		return out.String(), errBuf.String(), 0, runErr
	}
	return out.String(), errBuf.String(), 0, nil
}

func dockerLogs(containerID string, limit int) ([]string, error) {
	if strings.TrimSpace(containerID) == "" {
		return nil, fmt.Errorf("container id is required")
	}
	args := []string{"logs"}
	if limit > 0 {
		args = append(args, "--tail", fmt.Sprint(limit))
	}
	args = append(args, containerID)
	out, err := runCmd("", nil, "docker", args...)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(out), "\n"), nil
}

func dockerContainerName(previewID, service string) string {
	return dockerResourceName("vivero", previewID, service)
}

func dockerNetworkName(previewID string) string {
	return dockerResourceName("vivero", previewID, "network")
}

func ensureDockerNetwork(previewID string) error {
	name := dockerNetworkName(previewID)
	if _, err := runCmd("", nil, "docker", "network", "inspect", name); err == nil {
		return nil
	}
	out, err := runCmd("", nil, "docker", "network", "create", name)
	if err != nil {
		return fmt.Errorf("docker network create %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func removeDockerNetwork(previewID string) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	name := dockerNetworkName(previewID)
	out, err := runCmd("", nil, "docker", "network", "rm", name)
	if err != nil && !isDockerNoSuchContainer(string(out)) && !strings.Contains(strings.ToLower(string(out)), "no such network") && !strings.Contains(strings.ToLower(string(out)), "not found") {
		return fmt.Errorf("docker network rm %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func dockerContainerExists(containerID string) bool {
	if strings.TrimSpace(containerID) == "" {
		return false
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	_, err := runCmd("", nil, "docker", "container", "inspect", containerID)
	return err == nil
}

func removeDockerContainersForPreview(previewID string) error {
	if strings.TrimSpace(previewID) == "" {
		return nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	out, err := runCmd("", nil, "docker", "ps", "-aq", "--filter", "label=vivero.preview="+previewID)
	if err != nil {
		return fmt.Errorf("docker ps for preview %s: %w: %s", previewID, err, strings.TrimSpace(string(out)))
	}
	ids := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		id := strings.TrimSpace(line)
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	args := append([]string{"rm", "-f"}, ids...)
	body, err := runCmd("", nil, "docker", args...)
	if err != nil && !isDockerNoSuchContainer(string(body)) {
		return fmt.Errorf("docker rm preview containers %s: %w: %s", previewID, err, strings.TrimSpace(string(body)))
	}
	return nil
}

func removeDockerVolume(name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	out, err := runCmd("", nil, "docker", "volume", "rm", name)
	if err != nil && !isDockerNoSuchContainer(string(out)) && !strings.Contains(strings.ToLower(string(out)), "no such volume") && !strings.Contains(strings.ToLower(string(out)), "not found") {
		return fmt.Errorf("docker volume rm %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func dockerImageExists(ref string) bool {
	if strings.TrimSpace(ref) == "" || strings.Contains(ref, "*") {
		return false
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	_, err := runCmd("", nil, "docker", "image", "inspect", ref)
	return err == nil
}

func removeDockerImage(ref string) error {
	if strings.TrimSpace(ref) == "" || strings.Contains(ref, "*") {
		return nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	out, err := runCmd("", nil, "docker", "image", "rm", ref)
	if err != nil && !strings.Contains(strings.ToLower(string(out)), "no such image") && !strings.Contains(strings.ToLower(string(out)), "not found") {
		return fmt.Errorf("docker image rm %s: %w: %s", ref, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func dockerVolumeExists(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	_, err := runCmd("", nil, "docker", "volume", "inspect", name)
	return err == nil
}

func ensureDockerVolume(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("docker volume name is required")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker CLI not found; install Docker or OrbStack so Vivero can prepare warm volumes: %w", err)
	}
	if dockerVolumeExists(name) {
		return nil
	}
	out, err := runCmd("", nil, "docker", "volume", "create", name)
	if err != nil {
		return fmt.Errorf("docker volume create %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func copyDockerVolume(src, dst string) error {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" {
		return fmt.Errorf("docker volume copy requires source and destination")
	}
	if !dockerVolumeExists(src) {
		return ensureDockerVolume(dst)
	}
	if err := ensureDockerVolume(dst); err != nil {
		return err
	}
	args := []string{
		"run", "--rm",
		"--mount", fmt.Sprintf("type=volume,source=%s,target=/from,readonly", src),
		"--mount", fmt.Sprintf("type=volume,source=%s,target=/to", dst),
		"alpine:3.20", "/bin/sh", "-lc", "cd /from && tar cf - . | tar xf - -C /to",
	}
	stdout, stderr, err := runDocker(args)
	if err != nil {
		return fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr+"\n"+stdout))
	}
	return nil
}

func dockerOneShotContainerName(previewID, service string, command RuntimeCommand) string {
	return dockerResourceName("vivero", previewID, service, "oneshot", shortStableID(command.Key()))
}

func dockerVolumeName(previewID, service, name string) string {
	return dockerResourceName("vivero", previewID, service, name)
}

func dockerProjectVolumeName(project, service, name string) string {
	return dockerResourceName("vivero", "project", project, service, name)
}

func dockerSmartBaselineVolumeName(project, service, name string) string {
	return dockerResourceName("vivero", "warm", "baseline", project, service, name)
}

func dockerSmartPreviewVolumeName(project, previewID, service, name string) string {
	return dockerResourceName("vivero", "warm", project, previewID, service, name)
}

func dockerResourceName(parts ...string) string {
	raw := strings.Join(parts, "-")
	clean := sanitizeDockerName(raw)
	hash := shortStableID(raw)
	maxClean := 120 - len(hash) - 1
	if maxClean < 1 {
		maxClean = 1
	}
	if len(clean) > maxClean {
		clean = strings.Trim(clean[:maxClean], "-._")
	}
	if clean == "" {
		clean = "vivero"
	}
	return clean + "-" + hash
}

func dockerVolumeMountArg(project, previewID, service string, vol VolumeConfig) (string, bool, error) {
	if strings.TrimSpace(vol.Name) == "" || strings.TrimSpace(vol.Target) == "" {
		return "", false, nil
	}
	target, err := validateDockerMountTarget(vol.Target)
	if err != nil {
		return "", false, err
	}
	lifetime, err := normalizeVolumeLifetime(vol.Lifetime)
	if err != nil {
		return "", false, err
	}
	source := strings.TrimSpace(vol.RuntimeSource)
	if source == "" {
		source = dockerVolumeName(previewID, service, vol.Name)
		if lifetime == "project" {
			if strings.TrimSpace(project) == "" {
				return "", false, fmt.Errorf("project-lifetime volume %s for service %s requires project name", vol.Name, service)
			}
			source = dockerProjectVolumeName(project, service, vol.Name)
		}
		if lifetime == "smart" {
			if strings.TrimSpace(project) == "" {
				return "", false, fmt.Errorf("smart warm volume %s for service %s requires project name", vol.Name, service)
			}
			source = dockerSmartPreviewVolumeName(project, previewID, service, vol.Name)
		}
	}
	return fmt.Sprintf("type=volume,source=%s,target=%s", source, target), true, nil
}

func normalizeVolumeLifetime(lifetime string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(lifetime)) {
	case "", "preview", "per-preview", "ephemeral":
		return "preview", nil
	case "project", "persistent", "shared", "warm":
		return "project", nil
	case "smart", "smart-warm", "smart_warm", "branch", "branch-safe", "copy-on-write", "cow":
		return "smart", nil
	default:
		return "", fmt.Errorf("unsupported dependency volume lifetime %q; use preview, project, or smart", lifetime)
	}
}

func validateDockerMountTarget(target string) (string, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" || trimmed != target {
		return "", fmt.Errorf("docker volume target must be a non-empty absolute path: %q", target)
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("docker volume target must be absolute: %q", target)
	}
	if strings.ContainsAny(trimmed, ",\x00\n\r") {
		return "", fmt.Errorf("docker volume target contains unsupported mount option delimiter: %q", target)
	}
	return path.Clean(trimmed), nil
}

func validateDockerEnvName(name string) error {
	if name == "" {
		return fmt.Errorf("docker env name is empty")
	}
	for i, r := range name {
		if i == 0 {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' {
				continue
			}
			return fmt.Errorf("docker env name must start with a letter or underscore: %q", name)
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("docker env name contains invalid character: %q", name)
	}
	return nil
}
