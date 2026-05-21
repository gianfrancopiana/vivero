package vivero

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type LocalStateDoctorReport struct {
	OK             bool                `json:"ok"`
	Projects       int                 `json:"projects"`
	Previews       int                 `json:"previews"`
	ActivePreviews int                 `json:"activePreviews"`
	Errors         int                 `json:"errors"`
	Warnings       int                 `json:"warnings"`
	Findings       []LocalStateFinding `json:"findings,omitempty"`
}

type LocalStateFinding struct {
	Severity    string `json:"severity"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	Suggestion  string `json:"suggestion,omitempty"`
	Project     string `json:"project,omitempty"`
	PreviewID   string `json:"previewId,omitempty"`
	Service     string `json:"service,omitempty"`
	Source      string `json:"source,omitempty"`
	Path        string `json:"path,omitempty"`
	PID         int    `json:"pid,omitempty"`
	ContainerID string `json:"containerId,omitempty"`
}

func (r *LocalStateDoctorReport) add(f LocalStateFinding) {
	if f.Severity == "" {
		f.Severity = "warning"
	}
	switch f.Severity {
	case "error":
		r.Errors++
	default:
		f.Severity = "warning"
		r.Warnings++
	}
	r.Findings = append(r.Findings, f)
	r.OK = r.Errors == 0
}

func (a *App) LocalStateDoctor() (LocalStateDoctorReport, error) {
	report := LocalStateDoctorReport{OK: true}
	projects, err := a.listProjects()
	if err != nil {
		return report, err
	}
	previews, err := a.listPreviews()
	if err != nil {
		return report, err
	}
	report.Projects = len(projects)
	report.Previews = len(previews)

	projectByName := map[string]ProjectRecord{}
	for _, project := range projects {
		projectByName[project.Name] = project
		if strings.TrimSpace(project.Path) == "" || !pathExists(project.Path) {
			report.add(LocalStateFinding{
				Severity:   "error",
				Code:       "project-path-missing",
				Project:    project.Name,
				Path:       project.Path,
				Message:    fmt.Sprintf("synced project %s points at a missing path", project.Name),
				Suggestion: "run `vivero projects sync <project-path> --json --no-input` with the current project path, or remove stale previews for this project",
			})
		}
	}

	runtime := a.containerRuntime()
	for _, preview := range previews {
		activePreview := !previewIsDead(preview.Status)
		if activePreview {
			report.ActivePreviews++
		}
		if _, ok := projectByName[preview.Project]; !ok {
			report.add(LocalStateFinding{
				Severity:   "error",
				Code:       "preview-project-missing",
				PreviewID:  preview.ID,
				Project:    preview.Project,
				Message:    fmt.Sprintf("preview %s references missing project %s", preview.ID, preview.Project),
				Suggestion: recoverySuggestion(preview.ID),
			})
		}
		if activePreview && len(preview.Services) == 0 {
			report.add(LocalStateFinding{
				Severity:   "warning",
				Code:       "active-preview-empty",
				PreviewID:  preview.ID,
				Project:    preview.Project,
				Message:    fmt.Sprintf("preview %s is %s but has no service rows", preview.ID, preview.Status),
				Suggestion: recoverySuggestion(preview.ID),
			})
		}
		if activePreview {
			for _, sourceName := range sortedMapKeys(preview.Sources) {
				source := preview.Sources[sourceName]
				if strings.TrimSpace(source.Path) == "" || !pathExists(source.Path) {
					report.add(LocalStateFinding{
						Severity:   "error",
						Code:       "source-path-missing",
						PreviewID:  preview.ID,
						Project:    preview.Project,
						Source:     source.Name,
						Path:       source.Path,
						Message:    fmt.Sprintf("active preview %s source %s points at a missing path", preview.ID, source.Name),
						Suggestion: recoverySuggestion(preview.ID),
					})
				}
			}
		}
		for _, serviceName := range sortedMapKeys(preview.Services) {
			service := preview.Services[serviceName]
			deadService := previewIsDead(preview.Status) || serviceIsDead(service.Status)
			if activePreview && !deadService && serviceResourcesStopped(service) {
				report.add(LocalStateFinding{
					Severity:   "error",
					Code:       "service-no-resources",
					PreviewID:  preview.ID,
					Project:    preview.Project,
					Service:    service.Name,
					Message:    fmt.Sprintf("service %s/%s is %s but has no tracked process, tunnel, proxy, or container", preview.ID, service.Name, service.Status),
					Suggestion: recoverySuggestion(preview.ID),
				})
			}
			if service.ContainerID != "" {
				exists := runtime.ContainerExists(service.ContainerID)
				if !exists && !deadService {
					report.add(LocalStateFinding{
						Severity:    "error",
						Code:        "container-missing",
						PreviewID:   preview.ID,
						Project:     preview.Project,
						Service:     service.Name,
						ContainerID: service.ContainerID,
						Message:     fmt.Sprintf("service %s/%s is %s but container %s is missing", preview.ID, service.Name, service.Status, service.ContainerID),
						Suggestion:  recoverySuggestion(preview.ID),
					})
				}
				if exists && deadService {
					report.add(LocalStateFinding{
						Severity:    "warning",
						Code:        "dead-service-container-leftover",
						PreviewID:   preview.ID,
						Project:     preview.Project,
						Service:     service.Name,
						ContainerID: service.ContainerID,
						Message:     fmt.Sprintf("dead service %s/%s still has container %s", preview.ID, service.Name, service.ContainerID),
						Suggestion:  recoverySuggestion(preview.ID),
					})
				}
			}
			checkServicePID(&report, preview, service, "pid", service.PID, deadService)
			checkServicePID(&report, preview, service, "proxy-pid", service.ProxyPID, deadService)
			checkServicePID(&report, preview, service, "tunnel-pid", service.TunnelPID, deadService)
		}
	}
	report.OK = report.Errors == 0
	return report, nil
}

func doctorHuman(v map[string]any) string {
	state, _ := v["localState"].(LocalStateDoctorReport)
	if state.OK {
		return fmt.Sprintf("doctor ok\nprojects: %d\npreviews: %d\n", state.Projects, state.Previews)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "doctor found %d errors and %d warnings\n", state.Errors, state.Warnings)
	for _, finding := range state.Findings {
		fmt.Fprintf(&b, "%s %s: %s\n", finding.Severity, finding.Code, finding.Message)
		if finding.Suggestion != "" {
			fmt.Fprintf(&b, "  fix: %s\n", finding.Suggestion)
		}
	}
	return b.String()
}

func checkServicePID(report *LocalStateDoctorReport, preview PreviewRecord, service PreviewService, code string, pid int, deadService bool) {
	if pid <= 0 {
		return
	}
	missingCode := code + "-missing"
	leftoverCode := "dead-service-" + code + "-leftover"
	alive := processExists(pid)
	if !alive && !deadService {
		report.add(LocalStateFinding{
			Severity:   "error",
			Code:       missingCode,
			PreviewID:  preview.ID,
			Project:    preview.Project,
			Service:    service.Name,
			PID:        pid,
			Message:    fmt.Sprintf("service %s/%s tracks missing %s %d", preview.ID, service.Name, code, pid),
			Suggestion: recoverySuggestion(preview.ID),
		})
		return
	}
	if alive && deadService {
		report.add(LocalStateFinding{
			Severity:   "warning",
			Code:       leftoverCode,
			PreviewID:  preview.ID,
			Project:    preview.Project,
			Service:    service.Name,
			PID:        pid,
			Message:    fmt.Sprintf("dead service %s/%s still has live %s %d", preview.ID, service.Name, code, pid),
			Suggestion: recoverySuggestion(preview.ID),
		})
	}
}

func previewIsDead(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "dead")
}

func serviceIsDead(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "dead")
}

func recoverySuggestion(previewID string) string {
	if strings.TrimSpace(previewID) == "" {
		return "inspect local Vivero state and rerun the command after cleanup"
	}
	return fmt.Sprintf("run `vivero down %s --discard --json --no-input` to reconcile tracked resources, then rerun `vivero up` if needed", previewID)
}

func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(filepath.Clean(path))
	return err == nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}
