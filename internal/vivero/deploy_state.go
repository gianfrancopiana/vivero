package vivero

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var errNoCurrentRelease = errors.New("no current release")

func (a *App) saveDeployPlan(plan DeployPlan) error {
	if plan.StateVersion == 0 {
		plan.StateVersion = deployStateVersion
	}
	if err := ensureDir(a.deployPlanDir()); err != nil {
		return err
	}
	return writeIndentedJSONFile(filepath.Join(a.deployPlanDir(), statePathComponent(plan.ID)+".json"), plan, 0o644)
}

func (a *App) loadDeployPlan(id string) (DeployPlan, error) {
	var plan DeployPlan
	if strings.TrimSpace(id) == "" {
		return plan, fmt.Errorf("deploy plan id is required")
	}
	b, err := os.ReadFile(filepath.Join(a.deployPlanDir(), statePathComponent(id)+".json"))
	if err != nil {
		return plan, fmt.Errorf("deploy plan not found: %s", id)
	}
	if err := json.Unmarshal(b, &plan); err != nil {
		return plan, err
	}
	if plan.StateVersion == 0 {
		plan.StateVersion = deployStateVersion
	}
	if plan.StateVersion > deployStateVersion {
		return plan, fmt.Errorf("deploy plan %s uses unsupported state version %d", id, plan.StateVersion)
	}
	if plan.ID != id {
		return plan, fmt.Errorf("deploy plan state mismatch: requested %s, found %s", id, plan.ID)
	}
	return plan, nil
}

func (a *App) saveRelease(release ReleaseRecord) error {
	if release.StateVersion == 0 {
		release.StateVersion = deployStateVersion
	}
	if err := a.saveReleaseHistory(release); err != nil {
		return err
	}
	return writeIndentedJSONFile(a.currentReleasePath(release.Project, release.Environment), release, 0o644)
}

func (a *App) saveReleaseHistory(release ReleaseRecord) error {
	if release.StateVersion == 0 {
		release.StateVersion = deployStateVersion
	}
	if err := ensureDir(a.releaseDir()); err != nil {
		return err
	}
	return writeIndentedJSONFile(filepath.Join(a.releaseDir(), statePathComponent(release.ID)+".json"), release, 0o644)
}

func (a *App) loadRelease(id string) (ReleaseRecord, error) {
	var release ReleaseRecord
	if strings.TrimSpace(id) == "" {
		return release, fmt.Errorf("release id is required")
	}
	b, err := os.ReadFile(filepath.Join(a.releaseDir(), statePathComponent(id)+".json"))
	if err != nil {
		return release, fmt.Errorf("release not found: %s", id)
	}
	if err := json.Unmarshal(b, &release); err != nil {
		return release, err
	}
	if release.StateVersion == 0 {
		release.StateVersion = deployStateVersion
	}
	if release.StateVersion > deployStateVersion {
		return release, fmt.Errorf("release %s uses unsupported state version %d", id, release.StateVersion)
	}
	if release.ID != id {
		return release, fmt.Errorf("release state mismatch: requested %s, found %s", id, release.ID)
	}
	return release, nil
}

func (a *App) loadCurrentRelease(project, environment string) (ReleaseRecord, error) {
	var release ReleaseRecord
	if strings.TrimSpace(project) == "" {
		return release, fmt.Errorf("release status requires project")
	}
	b, err := os.ReadFile(a.currentReleasePath(project, environment))
	if err != nil {
		if os.IsNotExist(err) {
			return release, fmt.Errorf("%w for %s/%s", errNoCurrentRelease, project, environment)
		}
		return release, err
	}
	if err := json.Unmarshal(b, &release); err != nil {
		return release, err
	}
	if release.StateVersion == 0 {
		release.StateVersion = deployStateVersion
	}
	if release.StateVersion > deployStateVersion {
		return release, fmt.Errorf("current release for %s/%s uses unsupported state version %d", project, environment, release.StateVersion)
	}
	if release.Project != project || release.Environment != environment {
		return release, fmt.Errorf("current release state mismatch for %s/%s", project, environment)
	}
	return release, nil
}

func (a *App) deployPlanDir() string { return filepath.Join(a.Home, "deploy", "plans") }

func (a *App) releaseDir() string { return filepath.Join(a.Home, "deploy", "releases") }

func (a *App) deployArtifactDir() string { return filepath.Join(a.Home, "deploy", "artifacts") }

func (a *App) deployLockDir() string { return filepath.Join(a.Home, "deploy", "locks") }

func (a *App) deployLockPath(project, environment string) string {
	return filepath.Join(a.deployLockDir(), statePathComponent(project+"/"+environment)+".lock")
}

func (a *App) currentReleasePath(project, environment string) string {
	return filepath.Join(a.releaseDir(), "current-"+statePathComponent(project)+"-"+statePathComponent(environment)+".json")
}

func newDeployID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
}

func statePathComponent(s string) string {
	base := safeStateID(s)
	if len(base) > 80 {
		base = base[:80]
	}
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%s-%x", base, sum[:8])
}

func safeStateID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func (a *App) findSuccessfulReleaseForPlan(plan DeployPlan) (ReleaseRecord, bool, error) {
	release, err := a.loadCurrentRelease(plan.Project, plan.Environment)
	if err != nil {
		if errors.Is(err, errNoCurrentRelease) {
			return ReleaseRecord{}, false, nil
		}
		return ReleaseRecord{}, false, err
	}
	if release.PlanID != plan.ID {
		return ReleaseRecord{}, false, nil
	}
	if releaseStatusReapplySafe(release) {
		return release, true, nil
	}
	return release, false, fmt.Errorf("deploy plan %s already has current release %s in unsafe status %s; inspect release events/logs or roll back before applying again", plan.ID, release.ID, release.Status)
}

func releaseStatusReapplySafe(release ReleaseRecord) bool {
	if release.RollbackOf != "" {
		return false
	}
	status := strings.TrimSpace(release.Status)
	if status == "" || strings.HasSuffix(status, "_failed") {
		return false
	}
	switch status {
	case "failed", "rollback_failed", "applying", "promoting", "rolling_back", "rolled_back":
		return false
	default:
		return true
	}
}

func (a *App) findRollbackForRelease(project, environment, releaseID string) (ReleaseRecord, bool, error) {
	release, err := a.loadCurrentRelease(project, environment)
	if err != nil {
		if errors.Is(err, errNoCurrentRelease) {
			return ReleaseRecord{}, false, nil
		}
		return ReleaseRecord{}, false, err
	}
	if release.RollbackOf == releaseID && release.Status == "rolled_back" {
		return release, true, nil
	}
	return ReleaseRecord{}, false, nil
}

func (a *App) saveDeployArtifact(releaseID, name, kind, content string) (DeployArtifact, error) {
	artifact := DeployArtifact{Kind: kind, Name: name}
	dir := filepath.Join(a.deployArtifactDir(), statePathComponent(releaseID))
	if err := ensureDir(dir); err != nil {
		return artifact, err
	}
	path := filepath.Join(dir, newDeployID(statePathComponent(name))+".log")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return artifact, err
	}
	artifact.Path = path
	return artifact, nil
}

func (a *App) acquireDeployLock(project, environment string) (func(), error) {
	if err := ensureDir(a.deployLockDir()); err != nil {
		return nil, err
	}
	path := a.deployLockPath(project, environment)
	for {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			record := map[string]any{"project": project, "environment": environment, "pid": os.Getpid(), "createdAt": nowUTC()}
			_ = json.NewEncoder(file).Encode(record)
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if a.removeStaleDeployLock(path) {
			continue
		}
		return nil, fmt.Errorf("deploy lock already held for %s/%s", project, environment)
	}
}

func (a *App) removeStaleDeployLock(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var record struct {
		PID       int       `json:"pid"`
		CreatedAt time.Time `json:"createdAt"`
	}
	if err := json.Unmarshal(b, &record); err != nil {
		return false
	}
	if !record.CreatedAt.IsZero() && time.Since(record.CreatedAt) > 4*time.Hour {
		_ = os.Remove(path)
		return true
	}
	if record.PID > 0 && !processIsAlive(record.PID) {
		_ = os.Remove(path)
		return true
	}
	return false
}

func processIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
