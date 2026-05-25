package vivero

import "time"

type containerRuntime interface {
	BuildImage(spec dockerBuildSpec) error
	EnsureNetwork(previewID string) error
	StartService(home, projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string) (string, error)
	RunOneShot(home, projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string, command RuntimeCommand) ([]byte, error)
	PublishedPorts(containerID string, ports []ServicePort) ([]PreviewPort, error)
	WaitHealthCommand(containerID string, h HealthConfig, timeout time.Duration) error
	ContainerLogs(containerID string, limit int) ([]string, error)
	ContainerExists(containerID string) bool
	RemoveContainer(containerID string) (missing bool, output string, err error)
	RemoveComposeProject(previewID, service string) error
	RemoveContainersForPreview(previewID string) error
	RemoveNetwork(previewID string) error
	VolumeExists(name string) bool
	EnsureVolume(name string) error
	RemoveVolume(name string) error
	CopyVolume(src, dst string) error
	ImageExists(ref string) bool
	RemoveImage(ref string) error
}

type dockerContainerRuntime struct{}

func (a *App) containerRuntime() containerRuntime {
	if a.containers != nil {
		return a.containers
	}
	return dockerContainerRuntime{}
}

func (dockerContainerRuntime) BuildImage(spec dockerBuildSpec) error {
	return buildDockerImage(spec)
}

func (dockerContainerRuntime) EnsureNetwork(previewID string) error {
	return ensureDockerNetwork(previewID)
}

func (dockerContainerRuntime) StartService(home, projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string) (string, error) {
	return startDockerService(home, projectName, previewID, service, svc, sources, env)
}

func (dockerContainerRuntime) RunOneShot(home, projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string, command RuntimeCommand) ([]byte, error) {
	return runDockerOneShot(home, projectName, previewID, service, svc, sources, env, command)
}

func (dockerContainerRuntime) PublishedPorts(containerID string, ports []ServicePort) ([]PreviewPort, error) {
	return dockerPublishedPorts(containerID, ports)
}

func (dockerContainerRuntime) WaitHealthCommand(containerID string, h HealthConfig, timeout time.Duration) error {
	return waitDockerHealthCommand(containerID, h, timeout)
}

func (dockerContainerRuntime) ContainerLogs(containerID string, limit int) ([]string, error) {
	return dockerLogs(containerID, limit)
}

func (dockerContainerRuntime) ContainerExists(containerID string) bool {
	return dockerContainerExists(containerID)
}

func (dockerContainerRuntime) RemoveContainer(containerID string) (bool, string, error) {
	out, err := runCmd("", nil, "docker", "rm", "-f", containerID)
	if err == nil {
		return false, string(out), nil
	}
	return isDockerNoSuchContainer(string(out)), string(out), err
}

func (dockerContainerRuntime) RemoveComposeProject(previewID, service string) error {
	return removeDockerComposeProject(previewID, service)
}

func (dockerContainerRuntime) RemoveContainersForPreview(previewID string) error {
	return removeDockerContainersForPreview(previewID)
}

func (dockerContainerRuntime) RemoveNetwork(previewID string) error {
	return removeDockerNetwork(previewID)
}

func (dockerContainerRuntime) VolumeExists(name string) bool {
	return dockerVolumeExists(name)
}

func (dockerContainerRuntime) EnsureVolume(name string) error {
	return ensureDockerVolume(name)
}

func (dockerContainerRuntime) RemoveVolume(name string) error {
	return removeDockerVolume(name)
}

func (dockerContainerRuntime) CopyVolume(src, dst string) error {
	return copyDockerVolume(src, dst)
}

func (dockerContainerRuntime) ImageExists(ref string) bool {
	return dockerImageExists(ref)
}

func (dockerContainerRuntime) RemoveImage(ref string) error {
	return removeDockerImage(ref)
}
