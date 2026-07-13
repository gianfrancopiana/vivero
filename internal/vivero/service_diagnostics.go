package vivero

import (
	"fmt"
	"os"
	"strings"
)

const serviceFailureLogTail = 100

type serviceFailureDiagnostic struct {
	Service       string
	Runtime       string
	Image         string
	ContainerID   string
	Command       string
	HealthCommand string
	LogPath       string
	LastLogs      []string
	Cause         error
}

func (d serviceFailureDiagnostic) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "service %s failed: %v", d.Service, d.Cause)
	if d.Runtime != "" {
		fmt.Fprintf(&b, "\nruntime=%s", d.Runtime)
	}
	if d.Image != "" {
		fmt.Fprintf(&b, "\nimage=%s", d.Image)
	}
	if d.ContainerID != "" {
		fmt.Fprintf(&b, "\ncontainer=%s", d.ContainerID)
	}
	if d.Command != "" {
		fmt.Fprintf(&b, "\ncommand=%s", d.Command)
	}
	if d.HealthCommand != "" {
		fmt.Fprintf(&b, "\nhealthCommand=%s", d.HealthCommand)
	}
	if d.LogPath != "" {
		fmt.Fprintf(&b, "\nlogPath=%s", d.LogPath)
	}
	if len(d.LastLogs) > 0 {
		b.WriteString("\nlastLogs:")
		for _, line := range d.LastLogs {
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				continue
			}
			b.WriteString("\n  ")
			b.WriteString(line)
		}
	}
	return b.String()
}

func (d serviceFailureDiagnostic) Unwrap() error { return d.Cause }

func (a *App) serviceFailureError(previewID, name string, svc ServiceConfig, ps PreviewService, env map[string]string, cause error) error {
	if cause == nil {
		return nil
	}
	diagnostic := serviceFailureDiagnostic{
		Service:       name,
		Runtime:       serviceRuntime(svc),
		Image:         svc.Image,
		ContainerID:   ps.ContainerID,
		Command:       svc.Command.Display(),
		HealthCommand: svc.Health.Command.Display(),
		LogPath:       ps.LogPath,
		Cause:         cause,
	}
	if ps.ContainerID != "" || serviceRuntime(svc) == "compose" {
		var lines []string
		var err error
		if serviceRuntime(svc) == "compose" {
			lines, err = dockerComposeProjectLogs(previewID, name, serviceFailureLogTail)
		} else {
			lines, err = a.containerRuntime().ContainerLogs(ps.ContainerID, serviceFailureLogTail)
		}
		if len(lines) > 0 {
			diagnostic.LastLogs = redactRuntimeLogLines(lines, env)
			appendServiceDiagnosticLogs(ps.LogPath, diagnostic.LastLogs)
		}
		if err != nil {
			a.recordEvent(previewID, "warning", "service.logs_snapshot_failed", err.Error(), name, nil)
		}
	}
	return diagnostic
}

// snapshotPreviewServiceLogs persists every tracked container's logs before a
// cleanup can destroy the only evidence for a sibling-caused startup failure.
// Compose snapshots include every container in that Compose project, not only
// the target container represented in PreviewService.
func (a *App) snapshotPreviewServiceLogs(previewID string, services map[string]PreviewService, cfg ProjectConfig) {
	for _, name := range sortedMapKeys(services) {
		state := services[name]
		if state.ContainerID == "" || strings.TrimSpace(state.LogPath) == "" {
			continue
		}
		svcCfg, includeSecrets, ok := configForLogSnapshot(name, cfg)
		if !ok {
			continue
		}
		var lines []string
		var err error
		if serviceRuntime(svcCfg) == "compose" {
			lines, err = dockerComposeProjectLogs(previewID, name, serviceFailureLogTail)
		} else {
			lines, err = a.containerRuntime().ContainerLogs(state.ContainerID, serviceFailureLogTail)
		}
		if err != nil {
			a.recordEvent(previewID, "warning", "service.logs_snapshot_failed", err.Error(), name, nil)
		}
		if len(lines) == 0 {
			continue
		}
		lines = redactRuntimeLogLines(lines, a.envForService(cfg.Project.Name, svcCfg, includeSecrets))
		appendSnapshotLogs(state.LogPath, "runtime logs before cleanup", lines)
		a.recordEvent(previewID, "info", "service.logs_snapshotted", "runtime logs persisted before cleanup", name, map[string]string{"logPath": state.LogPath})
	}
}

func configForLogSnapshot(name string, cfg ProjectConfig) (ServiceConfig, bool, bool) {
	if service, ok := cfg.Services[name]; ok {
		return service, true, true
	}
	if backing, ok := cfg.BackingServices[name]; ok {
		return serviceConfigForBacking(backing), false, true
	}
	return ServiceConfig{}, false, false
}

func appendSnapshotLogs(path, label string, lines []string) {
	if strings.TrimSpace(path) == "" || len(lines) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("\n--- ")
	b.WriteString(label)
	b.WriteString(" ---\n")
	for _, line := range lines {
		b.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			b.WriteByte('\n')
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(b.String())
}

func appendServiceDiagnosticLogs(path string, lines []string) {
	if strings.TrimSpace(path) == "" || len(lines) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("\n--- docker logs tail after failure ---\n")
	for _, line := range lines {
		b.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			b.WriteByte('\n')
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(b.String())
}

func redactRuntimeLogLines(lines []string, env map[string]string) []string {
	if len(lines) == 0 {
		return nil
	}
	redacted := make([]string, 0, len(lines))
	for _, line := range lines {
		redacted = append(redacted, redactRuntimeLogLine(line, env))
	}
	return redacted
}

func redactRuntimeLogLine(line string, env map[string]string) string {
	out := line
	for key, value := range env {
		if value == "" || len(value) < 4 {
			continue
		}
		if diagnosticMetadataLooksSensitive(key, value) {
			out = strings.ReplaceAll(out, value, "[redacted]")
		}
	}
	return out
}
