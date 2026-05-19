package vivero

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) sourceFor(previewID, source string) (PreviewSource, error) {
	p, err := a.getPreview(previewID)
	if err != nil {
		return PreviewSource{}, err
	}
	src, ok := p.Sources[source]
	if !ok {
		return PreviewSource{}, fmt.Errorf("source not found: %s", source)
	}
	return src, nil
}

func (a *App) SyncFile(previewID, source, rel, from string) (map[string]any, error) {
	src, err := a.sourceFor(previewID, source)
	if err != nil {
		return nil, err
	}
	rel, err = cleanRelPath(rel)
	if err != nil {
		return nil, err
	}
	from = expandPath(from)
	b, err := os.ReadFile(from)
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(src.Path, rel)
	if err := ensureDir(filepath.Dir(dest)); err != nil {
		return nil, err
	}
	if err := os.WriteFile(dest, b, 0o644); err != nil {
		return nil, err
	}
	h, _ := fileHash(dest)
	a.recordEvent(previewID, "info", "source.synced", "file synced", source, map[string]string{"path": rel, "sha256": h})
	return map[string]any{"ok": true, "preview": previewID, "source": source, "path": rel, "bytes": len(b), "sha256": h}, nil
}

func (a *App) RemoveFile(previewID, source, rel string) (map[string]any, error) {
	src, err := a.sourceFor(previewID, source)
	if err != nil {
		return nil, err
	}
	rel, err = cleanRelPath(rel)
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(src.Path, rel)
	if err := os.Remove(dest); err != nil {
		return nil, err
	}
	a.recordEvent(previewID, "info", "source.removed", "file removed", source, map[string]string{"path": rel})
	return map[string]any{"ok": true, "preview": previewID, "source": source, "path": rel}, nil
}

func (a *App) Diff(previewID, source string) (map[string]any, error) {
	src, err := a.sourceFor(previewID, source)
	if err != nil {
		return nil, err
	}
	status, _ := runCmd(src.Path, nil, "git", "status", "--short")
	diff, _ := runCmd(src.Path, nil, "git", "diff")
	return map[string]any{"preview": previewID, "source": source, "path": src.Path, "status": string(status), "diff": string(diff)}, nil
}

func (a *App) Exec(previewID, service string, cmdArgs []string) (map[string]any, error) {
	p, err := a.getPreview(previewID)
	if err != nil {
		return nil, err
	}
	svc, ok := p.Services[service]
	if !ok {
		return nil, fmt.Errorf("service not found: %s", service)
	}
	if len(cmdArgs) == 0 {
		return nil, fmt.Errorf("command required after --")
	}
	if svc.Runtime != "docker" {
		return nil, fmt.Errorf("service %s cannot exec command: runtime %q is not supported; Vivero runs app services in containers only", service, svc.Runtime)
	}
	stdout, stderr, exit, err := dockerExec(svc.ContainerID, cmdArgs)
	if err != nil {
		return nil, err
	}
	a.recordEvent(previewID, "info", "service.exec", "command executed in container", service, map[string]string{"command": strings.Join(cmdArgs, " "), "exit": fmt.Sprint(exit), "container": svc.ContainerID})
	return map[string]any{"preview": previewID, "service": service, "containerId": svc.ContainerID, "command": cmdArgs, "exitCode": exit, "stdout": stdout, "stderr": stderr}, nil
}

func (a *App) Logs(previewID, service string, limit int) (map[string]any, error) {
	p, err := a.getPreview(previewID)
	if err != nil {
		return nil, err
	}
	svc, ok := p.Services[service]
	if !ok {
		return nil, fmt.Errorf("service not found: %s", service)
	}
	if svc.Runtime == "docker" || svc.ContainerID != "" {
		lines, err := dockerLogs(svc.ContainerID, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"preview": previewID, "service": service, "containerId": svc.ContainerID, "logPath": svc.LogPath, "lines": lines}, nil
	}
	b, err := os.ReadFile(svc.LogPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(b), "\n")
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return map[string]any{"preview": previewID, "service": service, "logPath": svc.LogPath, "lines": lines}, nil
}
