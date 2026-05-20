package vivero

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

type DeployPlan struct {
	ID              string                       `json:"id"`
	Project         string                       `json:"project"`
	Path            string                       `json:"path"`
	Environment     string                       `json:"environment"`
	Strategy        string                       `json:"strategy,omitempty"`
	OK              bool                         `json:"ok"`
	Verdict         string                       `json:"verdict"`
	Diagnostics     []ProductionDoctorDiagnostic `json:"diagnostics"`
	Services        []DeployServiceArtifact      `json:"services,omitempty"`
	ApplyCommand    string                       `json:"applyCommand,omitempty"`
	StatusCommand   string                       `json:"statusCommand,omitempty"`
	RollbackCommand string                       `json:"rollbackCommand,omitempty"`
	BlueGreen       *BlueGreenDeployPlan         `json:"blueGreen,omitempty"`
	CreatedAt       time.Time                    `json:"createdAt"`
}

type BlueGreenDeployPlan struct {
	Slots        []string          `json:"slots"`
	ActiveSlot   string            `json:"activeSlot,omitempty"`
	TargetSlot   string            `json:"targetSlot"`
	PreviousSlot string            `json:"previousSlot,omitempty"`
	Phases       []DeployPhasePlan `json:"phases"`
}

type DeployPhasePlan struct {
	Name    string `json:"name"`
	Command string `json:"command,omitempty"`
}

type DeployServiceArtifact struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

type ReleaseRecord struct {
	ID           string              `json:"id"`
	PlanID       string              `json:"planId"`
	Project      string              `json:"project"`
	Environment  string              `json:"environment"`
	Strategy     string              `json:"strategy,omitempty"`
	Status       string              `json:"status"`
	RollbackOf   string              `json:"rollbackOf,omitempty"`
	ActiveSlot   string              `json:"activeSlot,omitempty"`
	PreviousSlot string              `json:"previousSlot,omitempty"`
	TargetSlot   string              `json:"targetSlot,omitempty"`
	Phases       []DeployPhaseRecord `json:"phases,omitempty"`
	Output       string              `json:"output,omitempty"`
	CreatedAt    time.Time           `json:"createdAt"`
	UpdatedAt    time.Time           `json:"updatedAt"`
}

type DeployPhaseRecord struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
}

func (a *App) DeployPlan(path, environment string) (DeployPlan, error) {
	if strings.TrimSpace(environment) == "" {
		environment = "production"
	}
	root, cfg, err := loadProjectConfig(path)
	if err != nil {
		return DeployPlan{}, err
	}
	if abs, absErr := filepath.Abs(root); absErr == nil {
		root = abs
	}
	doctor, err := a.ProductionDoctor(root)
	if err != nil {
		return DeployPlan{}, err
	}
	now := nowUTC()
	plan := DeployPlan{
		ID:          newDeployID("plan"),
		Project:     cfg.Project.Name,
		Path:        root,
		Environment: environment,
		OK:          doctor.OK,
		Verdict:     doctor.Verdict,
		Diagnostics: append([]ProductionDoctorDiagnostic{}, doctor.Diagnostics...),
		CreatedAt:   now,
	}
	for _, name := range sortedMapKeys(cfg.Services) {
		image := strings.TrimSpace(cfg.Services[name].Image)
		if image == "" {
			continue
		}
		plan.Services = append(plan.Services, DeployServiceArtifact{Name: name, Image: image})
	}
	deployEnv, ok := cfg.Deploy.Environments[environment]
	if !ok {
		plan.addDiagnostic("error", "deploy-environment-missing", "deploy.environments."+environment, fmt.Sprintf("deploy environment %s is not configured", environment), "Add deploy.environments.<name> with app-owned apply/status/rollback commands.")
	} else {
		strategy := normalizeDeployStrategy(deployEnv.Strategy)
		plan.Strategy = strategy
		switch strategy {
		case "command":
			plan.ApplyCommand = strings.TrimSpace(deployEnv.ApplyCommand)
			plan.StatusCommand = strings.TrimSpace(deployEnv.StatusCommand)
			plan.RollbackCommand = strings.TrimSpace(deployEnv.RollbackCommand)
			if plan.ApplyCommand == "" {
				plan.addDiagnostic("error", "deploy-apply-missing", "deploy.environments."+environment+".applyCommand", "deploy apply command is not configured", "Set an app-owned applyCommand for this environment.")
			}
			if plan.RollbackCommand == "" {
				plan.addDiagnostic("error", "deploy-rollback-missing", "deploy.environments."+environment+".rollbackCommand", "deploy rollback command is not configured", "Set an app-owned rollbackCommand for this environment.")
			}
		case "blue-green":
			plan.configureBlueGreenDeploy(environment, deployEnv.BlueGreen)
		default:
			plan.addDiagnostic("error", "deploy-strategy-unsupported", "deploy.environments."+environment+".strategy", fmt.Sprintf("deploy strategy %q is not supported", deployEnv.Strategy), "Use strategy: blue-green or omit strategy for app-owned command deploys.")
		}
	}
	plan.finish()
	if err := a.saveDeployPlan(plan); err != nil {
		return DeployPlan{}, err
	}
	return plan, nil
}

func (p *DeployPlan) addDiagnostic(level, code, path, message, suggestion string) {
	p.Diagnostics = append(p.Diagnostics, ProductionDoctorDiagnostic{Level: level, Code: code, Path: path, Message: message, Suggestion: suggestion})
}

func (p *DeployPlan) finish() {
	errors := 0
	warnings := 0
	for _, diag := range p.Diagnostics {
		switch diag.Level {
		case "error":
			errors++
		case "warning":
			warnings++
		}
	}
	p.OK = errors == 0
	switch {
	case errors > 0:
		p.Verdict = "blocked"
	case warnings > 0:
		p.Verdict = "candidate"
	default:
		p.Verdict = "ready"
	}
}

func normalizeDeployStrategy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "command", "app-owned", "app-owned-command":
		return "command"
	case "blue-green", "bluegreen":
		return "blue-green"
	default:
		return strings.TrimSpace(raw)
	}
}

func (a *App) ApplyDeployPlan(planID string) (ReleaseRecord, error) {
	plan, err := a.loadDeployPlan(planID)
	if err != nil {
		return ReleaseRecord{}, err
	}
	if !plan.OK {
		return ReleaseRecord{}, fmt.Errorf("deploy plan %s is blocked", planID)
	}
	if plan.Strategy == "blue-green" {
		return a.applyBlueGreenDeployPlan(plan)
	}
	if strings.TrimSpace(plan.ApplyCommand) == "" {
		return ReleaseRecord{}, fmt.Errorf("deploy plan %s has no apply command", planID)
	}
	release := newReleaseRecord(plan, "applied", "")
	out, err := runDeployShell(plan, release, plan.ApplyCommand, map[string]string{"VIVERO_RELEASE_ACTION": "apply"})
	release.Output = strings.TrimSpace(string(out))
	if err != nil {
		release.Status = "failed"
		release.UpdatedAt = nowUTC()
		_ = a.saveRelease(release)
		return release, fmt.Errorf("deploy apply failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := a.saveRelease(release); err != nil {
		return ReleaseRecord{}, err
	}
	return release, nil
}

func (a *App) CurrentRelease(project, environment string) (ReleaseRecord, error) {
	if strings.TrimSpace(environment) == "" {
		environment = "production"
	}
	release, err := a.loadCurrentRelease(project, environment)
	if err != nil {
		return ReleaseRecord{}, err
	}
	plan, err := a.loadDeployPlan(release.PlanID)
	if err != nil {
		return ReleaseRecord{}, err
	}
	if strings.TrimSpace(plan.StatusCommand) != "" {
		out, runErr := runDeployShell(plan, release, plan.StatusCommand, map[string]string{"VIVERO_RELEASE_ACTION": "status"})
		if runErr != nil {
			return release, fmt.Errorf("release status failed: %w: %s", runErr, strings.TrimSpace(string(out)))
		}
		if status := strings.TrimSpace(string(out)); status != "" {
			release.Status = status
			release.Output = status
			release.UpdatedAt = nowUTC()
			if err := a.saveRelease(release); err != nil {
				return ReleaseRecord{}, err
			}
		}
	}
	return release, nil
}

func (a *App) RollbackRelease(project, releaseID, environment string) (ReleaseRecord, error) {
	if strings.TrimSpace(environment) == "" {
		environment = "production"
	}
	if strings.TrimSpace(project) == "" || strings.TrimSpace(releaseID) == "" {
		return ReleaseRecord{}, fmt.Errorf("release rollback requires project and release id")
	}
	release, err := a.loadRelease(releaseID)
	if err != nil {
		return ReleaseRecord{}, err
	}
	if release.Project != project || release.Environment != environment {
		return ReleaseRecord{}, fmt.Errorf("release %s belongs to %s/%s, not %s/%s", releaseID, release.Project, release.Environment, project, environment)
	}
	plan, err := a.loadDeployPlan(release.PlanID)
	if err != nil {
		return ReleaseRecord{}, err
	}
	if strings.TrimSpace(plan.RollbackCommand) == "" {
		return ReleaseRecord{}, fmt.Errorf("release %s has no rollback command", releaseID)
	}
	if plan.Strategy == "blue-green" {
		return a.rollbackBlueGreenRelease(plan, release)
	}
	rollback := newReleaseRecord(plan, "rolled_back", releaseID)
	out, err := runDeployShell(plan, rollback, plan.RollbackCommand, map[string]string{"VIVERO_RELEASE_ACTION": "rollback", "VIVERO_ROLLBACK_RELEASE_ID": releaseID})
	rollback.Output = strings.TrimSpace(string(out))
	if err != nil {
		rollback.Status = "rollback_failed"
		rollback.UpdatedAt = nowUTC()
		_ = a.saveRelease(rollback)
		return rollback, fmt.Errorf("release rollback failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := a.saveRelease(rollback); err != nil {
		return ReleaseRecord{}, err
	}
	return rollback, nil
}
