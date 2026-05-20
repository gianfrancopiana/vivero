package vivero

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) saveDeployPlan(plan DeployPlan) error {
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
	if plan.ID != id {
		return plan, fmt.Errorf("deploy plan state mismatch: requested %s, found %s", id, plan.ID)
	}
	return plan, nil
}

func (a *App) saveRelease(release ReleaseRecord) error {
	if err := a.saveReleaseHistory(release); err != nil {
		return err
	}
	return writeIndentedJSONFile(a.currentReleasePath(release.Project, release.Environment), release, 0o644)
}

func (a *App) saveReleaseHistory(release ReleaseRecord) error {
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
		return release, fmt.Errorf("no current release for %s/%s", project, environment)
	}
	if err := json.Unmarshal(b, &release); err != nil {
		return release, err
	}
	if release.Project != project || release.Environment != environment {
		return release, fmt.Errorf("current release state mismatch for %s/%s", project, environment)
	}
	return release, nil
}

func (a *App) deployPlanDir() string { return filepath.Join(a.Home, "deploy", "plans") }

func (a *App) releaseDir() string { return filepath.Join(a.Home, "deploy", "releases") }

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
