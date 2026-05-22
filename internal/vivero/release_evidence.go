package vivero

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type ReleaseSmokeResult struct {
	OK       bool            `json:"ok"`
	Output   string          `json:"output,omitempty"`
	Error    string          `json:"error,omitempty"`
	Artifact *DeployArtifact `json:"artifact,omitempty"`
}

type ReleaseLogEntry struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (a *App) resolveReleaseTarget(raw, environment string) (ReleaseRecord, map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ReleaseRecord{}, nil, fmt.Errorf("release target is required")
	}
	if kind, id, ok := strings.Cut(raw, ":"); ok {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "release":
			id = strings.TrimSpace(id)
			if id == "" {
				return ReleaseRecord{}, nil, fmt.Errorf("release target is required")
			}
			release, err := a.loadRelease(id)
			if err != nil {
				return ReleaseRecord{}, nil, err
			}
			return release, releaseTargetRef(release), nil
		default:
			return ReleaseRecord{}, nil, fmt.Errorf("unsupported target ref %q; use release:<id> or project name", raw)
		}
	}
	if strings.TrimSpace(environment) == "" {
		environment = "production"
	}
	release, err := a.loadCurrentRelease(raw, environment)
	if err != nil {
		return ReleaseRecord{}, nil, err
	}
	return release, releaseTargetRef(release), nil
}

func releaseTargetRef(release ReleaseRecord) map[string]any {
	return map[string]any{"kind": "release", "id": release.ID, "ref": "release:" + release.ID, "project": release.Project, "environment": release.Environment}
}

func (a *App) runReleaseSmoke(plan DeployPlan, release *ReleaseRecord) (ReleaseSmokeResult, error) {
	command := strings.TrimSpace(plan.SmokeCommand)
	if command == "" {
		err := fmt.Errorf("release %s has no smoke command", release.ID)
		return ReleaseSmokeResult{OK: false, Error: err.Error()}, err
	}
	release.addAudit("smoke", "started", "running release smoke command")
	out, err := runDeployShellContext(a.deployContext(), plan, *release, command, deployCommandInvocation{Action: "smoke"})
	trimmed := strings.TrimSpace(string(out))
	result := ReleaseSmokeResult{OK: err == nil, Output: trimmed}
	if artifact, artifactErr := a.saveDeployArtifact(release.ID, "smoke", "command-output", string(out)); artifactErr == nil {
		release.Artifacts = append(release.Artifacts, artifact)
		result.Artifact = &artifact
	}
	release.Output = appendReleaseOutput(release.Output, trimmed)
	if err != nil {
		release.Status = "smoke_failed"
		release.addAudit("smoke", "failed", trimmed)
		result.Error = err.Error()
		return result, err
	}
	if release.Status == "smoke_failed" {
		release.Status = "smoke_ok"
	}
	release.addAudit("smoke", "succeeded", trimmed)
	return result, nil
}

func (a *App) SmokeRelease(release ReleaseRecord) (ReleaseRecord, ReleaseSmokeResult, error) {
	plan, err := a.loadDeployPlan(release.PlanID)
	if err != nil {
		return release, ReleaseSmokeResult{}, err
	}
	unlock, err := a.acquireDeployLock(release.Project, release.Environment)
	if err != nil {
		return release, ReleaseSmokeResult{}, err
	}
	defer unlock()
	smoke, smokeErr := a.runReleaseSmoke(plan, &release)
	if saveErr := a.saveReleaseEvidence(release); saveErr != nil {
		return release, smoke, saveErr
	}
	if smokeErr != nil {
		return release, smoke, nil
	}
	return release, smoke, nil
}

func (a *App) saveReleaseEvidence(release ReleaseRecord) error {
	if err := a.saveReleaseHistory(release); err != nil {
		return err
	}
	current, err := a.loadCurrentRelease(release.Project, release.Environment)
	if err != nil {
		if errors.Is(err, errNoCurrentRelease) {
			return nil
		}
		return err
	}
	if current.ID != release.ID {
		return nil
	}
	return writeIndentedJSONFile(a.currentReleasePath(release.Project, release.Environment), release, 0o644)
}

func releaseLogs(release ReleaseRecord) []ReleaseLogEntry {
	logs := []ReleaseLogEntry{}
	if strings.TrimSpace(release.Output) != "" {
		logs = append(logs, ReleaseLogEntry{Kind: "release-output", Name: "output", Content: release.Output})
	}
	for _, phase := range release.Phases {
		if strings.TrimSpace(phase.Output) == "" {
			continue
		}
		logs = append(logs, ReleaseLogEntry{Kind: "phase-output", Name: phase.Name, Content: phase.Output})
	}
	for _, artifact := range release.Artifacts {
		entry := ReleaseLogEntry{Kind: artifact.Kind, Name: artifact.Name, Path: artifact.Path}
		if b, err := os.ReadFile(artifact.Path); err == nil {
			entry.Content = string(b)
		} else {
			entry.Error = err.Error()
		}
		logs = append(logs, entry)
	}
	return logs
}

func appendReleaseOutput(existing, next string) string {
	existing = strings.TrimSpace(existing)
	next = strings.TrimSpace(next)
	if next == "" {
		return capDeployOutput(existing, deployCommandOutputLimit)
	}
	if existing == "" {
		return capDeployOutput(next, deployCommandOutputLimit)
	}
	return capDeployOutput(existing+"\n"+next, deployCommandOutputLimit)
}
