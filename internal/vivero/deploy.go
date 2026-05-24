package vivero

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const deployStateVersion = 1

type DeployPlan struct {
	StateVersion    int                          `json:"stateVersion"`
	ID              string                       `json:"id"`
	Project         string                       `json:"project"`
	Path            string                       `json:"path"`
	Environment     string                       `json:"environment"`
	Strategy        string                       `json:"strategy,omitempty"`
	OK              bool                         `json:"ok"`
	Verdict         string                       `json:"verdict"`
	Diagnostics     []ProductionDoctorDiagnostic `json:"diagnostics"`
	Changes         []DeployChange               `json:"changes,omitempty"`
	Services        []DeployServiceArtifact      `json:"services,omitempty"`
	PrepareCommand  string                       `json:"prepareCommand,omitempty"`
	ApplyCommand    string                       `json:"applyCommand,omitempty"`
	StatusCommand   string                       `json:"statusCommand,omitempty"`
	SmokeCommand    string                       `json:"smokeCommand,omitempty"`
	RollbackCommand string                       `json:"rollbackCommand,omitempty"`
	CommandTimeout  string                       `json:"commandTimeout,omitempty"`
	StatusTimeout   string                       `json:"statusTimeout,omitempty"`
	Phases          []DeployPhasePlan            `json:"phases,omitempty"`
	Cache           *DeployCachePlan             `json:"cache,omitempty"`
	BlueGreen       *BlueGreenDeployPlan         `json:"blueGreen,omitempty"`
	CreatedAt       time.Time                    `json:"createdAt"`
}

type DeployCachePlan struct {
	Dir   string                `json:"dir,omitempty"`
	Build ImageBuildCacheConfig `json:"build,omitempty"`
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

type DeployChange struct {
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Summary string `json:"summary"`
}

type ReleaseRecord struct {
	StateVersion int                 `json:"stateVersion"`
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
	Audit        []DeployAuditEvent  `json:"audit,omitempty"`
	Artifacts    []DeployArtifact    `json:"artifacts,omitempty"`
	Output       string              `json:"output,omitempty"`
	CreatedAt    time.Time           `json:"createdAt"`
	UpdatedAt    time.Time           `json:"updatedAt"`
}

type DeployAuditEvent struct {
	At      time.Time `json:"at"`
	Action  string    `json:"action"`
	Status  string    `json:"status"`
	Message string    `json:"message,omitempty"`
}

type DeployArtifact struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type DeployPhaseRecord struct {
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	Output     string          `json:"output,omitempty"`
	DurationMS int64           `json:"durationMs"`
	Artifact   *DeployArtifact `json:"artifact,omitempty"`
}

func (a *App) DeployPlan(path, environment string) (DeployPlan, error) {
	if strings.TrimSpace(environment) == "" {
		environment = "production"
	}
	root, cfg, err := loadProjectConfig(path)
	if err != nil {
		return DeployPlan{}, err
	}
	if profiled, _, profileErr := projectConfigForEnvironment(cfg, environment); profileErr != nil {
		return DeployPlan{}, profileErr
	} else {
		cfg = profiled
	}
	if abs, absErr := filepath.Abs(root); absErr == nil {
		root = abs
	}
	doctor, err := a.ProductionDoctorForEnvironment(root, environment)
	if err != nil {
		return DeployPlan{}, err
	}
	now := nowUTC()
	plan := DeployPlan{
		StateVersion: deployStateVersion,
		ID:           newDeployID("plan"),
		Project:      cfg.Project.Name,
		Path:         root,
		Environment:  environment,
		OK:           doctor.OK,
		Verdict:      doctor.Verdict,
		Diagnostics:  append([]ProductionDoctorDiagnostic{}, doctor.Diagnostics...),
		CreatedAt:    now,
	}
	for _, name := range sortedMapKeys(cfg.Services) {
		image := strings.TrimSpace(cfg.Services[name].Image)
		if image == "" {
			continue
		}
		plan.Services = append(plan.Services, DeployServiceArtifact{Name: name, Image: image})
		plan.Changes = append(plan.Changes, DeployChange{Kind: "service-image", Name: name, To: image, Summary: fmt.Sprintf("deploy %s image %s", name, image)})
	}
	deployEnv, ok := cfg.Deploy.Environments[environment]
	if !ok {
		plan.addDiagnostic("error", "deploy-environment-missing", "deploy.environments."+environment, fmt.Sprintf("deploy environment %s is not configured", environment), "Add deploy.environments.<name> with app-owned apply/status/rollback commands.")
	} else {
		plan.CommandTimeout = strings.TrimSpace(deployEnv.CommandTimeout)
		plan.StatusTimeout = strings.TrimSpace(deployEnv.StatusTimeout)
		validateDeployTimeout := func(raw, path string) {
			if strings.TrimSpace(raw) == "" {
				return
			}
			if _, err := durationValue(raw, path, 0); err != nil {
				plan.addDiagnostic("error", "deploy-timeout-invalid", path, err.Error(), "Use a positive Go duration such as 30s, 5m, or 1h.")
			}
		}
		validateDeployTimeout(plan.CommandTimeout, "deploy.environments."+environment+".commandTimeout")
		validateDeployTimeout(plan.StatusTimeout, "deploy.environments."+environment+".statusTimeout")
		strategy := normalizeDeployStrategy(deployEnv.Strategy)
		plan.Strategy = strategy
		plan.Changes = append(plan.Changes, DeployChange{Kind: "deploy-strategy", To: strategy, Summary: fmt.Sprintf("apply %s strategy in %s", strategy, environment)})
		switch strategy {
		case "command":
			plan.PrepareCommand = strings.TrimSpace(deployEnv.PrepareCommand)
			plan.ApplyCommand = strings.TrimSpace(deployEnv.ApplyCommand)
			plan.StatusCommand = strings.TrimSpace(deployEnv.StatusCommand)
			plan.SmokeCommand = strings.TrimSpace(deployEnv.SmokeCommand)
			plan.RollbackCommand = strings.TrimSpace(deployEnv.RollbackCommand)
			plan.Phases = commandDeployPhasePlan(plan)
			if cache, cacheErr := resolveDeployCachePlan(root, deployEnv.Cache); cacheErr != nil {
				plan.addDiagnostic("error", "deploy-cache-invalid", "deploy.environments."+environment+".cache", cacheErr.Error(), "Keep deploy cache paths relative to the project root and build cache specs valid.")
			} else {
				plan.Cache = cache
			}
			if plan.ApplyCommand == "" {
				plan.addDiagnostic("error", "deploy-apply-missing", "deploy.environments."+environment+".applyCommand", "deploy apply command is not configured", "Set an app-owned applyCommand for this environment.")
			}
			if plan.RollbackCommand == "" {
				plan.addDiagnostic("error", "deploy-rollback-missing", "deploy.environments."+environment+".rollbackCommand", "deploy rollback command is not configured", "Set an app-owned rollbackCommand for this environment.")
			}
		case "blue-green":
			plan.configureBlueGreenDeploy(a.deployContext(), environment, deployEnv.BlueGreen)
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

func commandDeployPhasePlan(plan DeployPlan) []DeployPhasePlan {
	phases := []DeployPhasePlan{}
	for _, phase := range []DeployPhasePlan{
		{Name: "prepare", Command: plan.PrepareCommand},
		{Name: "apply", Command: plan.ApplyCommand},
		{Name: "smoke", Command: plan.SmokeCommand},
	} {
		if strings.TrimSpace(phase.Command) == "" {
			continue
		}
		phases = append(phases, phase)
	}
	if strings.TrimSpace(plan.PrepareCommand) != "" && strings.TrimSpace(plan.StatusCommand) != "" {
		phases = append(phases, DeployPhasePlan{Name: "status", Command: plan.StatusCommand})
	}
	return phases
}

func resolveDeployCachePlan(projectRoot string, cfg DeployCacheConfig) (*DeployCachePlan, error) {
	dir := strings.TrimSpace(cfg.Dir)
	buildConfigured := imageBuildCacheEnabled(cfg.Build)
	if dir == "" && !buildConfigured {
		return nil, nil
	}
	plan := &DeployCachePlan{}
	if dir != "" {
		resolved, err := resolveProjectPath(projectRoot, dir)
		if err != nil {
			return nil, fmt.Errorf("cache dir: %w", err)
		}
		plan.Dir = resolved
	}
	if buildConfigured {
		build := ImageBuildCacheConfig{}
		enabled := true
		build.Enabled = &enabled
		from, err := resolveBuildCacheSpecs(projectRoot, "deploy.cache.build.from", cfg.Build.From)
		if err != nil {
			return nil, err
		}
		to, err := resolveBuildCacheSpecs(projectRoot, "deploy.cache.build.to", cfg.Build.To)
		if err != nil {
			return nil, err
		}
		build.From = from
		build.To = to
		plan.Build = build
	}
	return plan, nil
}

func (r *ReleaseRecord) addAudit(action, status, message string) {
	r.Audit = append(r.Audit, DeployAuditEvent{At: nowUTC(), Action: action, Status: status, Message: message})
	r.UpdatedAt = nowUTC()
}

func (a *App) ApplyDeployPlan(planID string) (ReleaseRecord, error) {
	plan, err := a.loadDeployPlan(planID)
	if err != nil {
		return ReleaseRecord{}, err
	}
	if !plan.OK {
		return ReleaseRecord{}, fmt.Errorf("deploy plan %s is blocked", planID)
	}
	if existing, ok, err := a.findSuccessfulReleaseForPlan(plan); err != nil {
		return ReleaseRecord{}, err
	} else if ok {
		return existing, nil
	}
	unlock, err := a.acquireDeployLock(plan.Project, plan.Environment)
	if err != nil {
		return ReleaseRecord{}, err
	}
	defer unlock()
	if existing, ok, err := a.findSuccessfulReleaseForPlan(plan); err != nil {
		return ReleaseRecord{}, err
	} else if ok {
		return existing, nil
	}
	if plan.Strategy == "blue-green" {
		return a.applyBlueGreenDeployPlan(plan)
	}
	if strings.TrimSpace(plan.ApplyCommand) == "" {
		return ReleaseRecord{}, fmt.Errorf("deploy plan %s has no apply command", planID)
	}
	if len(plan.Phases) == 0 {
		plan.Phases = commandDeployPhasePlan(plan)
	}
	release := newReleaseRecord(plan, "applying", "")
	if err := a.saveReleaseHistory(release); err != nil {
		return ReleaseRecord{}, err
	}
	for _, phase := range plan.Phases {
		record, phaseErr := a.runCommandDeployPhase(plan, &release, phase)
		if record.Status != "skipped" {
			release.Phases = append(release.Phases, record)
		}
		if phaseErr != nil {
			_ = a.saveReleaseHistory(release)
			switch phase.Name {
			case "smoke":
				return release, fmt.Errorf("deploy smoke failed: %w: %s", phaseErr, record.Output)
			case "apply":
				return release, fmt.Errorf("deploy apply failed: %w: %s", phaseErr, record.Output)
			default:
				return release, fmt.Errorf("deploy %s failed: %w: %s", phase.Name, phaseErr, record.Output)
			}
		}
	}
	release.Status = "applied"
	release.addAudit("apply", "succeeded", "app-owned deploy phases completed")
	if err := a.saveRelease(release); err != nil {
		return ReleaseRecord{}, err
	}
	return release, nil
}

func (a *App) CurrentRelease(project, environment string) (ReleaseRecord, error) {
	if strings.TrimSpace(environment) == "" {
		environment = "production"
	}
	release, plan, err := a.loadCurrentReleaseWithPlan(project, environment)
	if err != nil {
		return ReleaseRecord{}, err
	}
	if strings.TrimSpace(plan.StatusCommand) == "" {
		return release, nil
	}
	return a.withDeployLock(project, environment, func() (ReleaseRecord, error) {
		release, plan, err := a.loadCurrentReleaseWithPlan(project, environment)
		if err != nil {
			return ReleaseRecord{}, err
		}
		if strings.TrimSpace(plan.StatusCommand) == "" {
			return release, nil
		}
		result, runErr := runDeployShellResultContext(a.deployContext(), plan, release, plan.StatusCommand, deployCommandInvocation{Action: "status"})
		trimmed := strings.TrimSpace(result.Output)
		if runErr != nil {
			a.markReleaseStatusFailed(&release, result, trimmed, trimmed)
			return release, fmt.Errorf("release status failed: %w: %s", runErr, trimmed)
		}
		if trimmed == "" {
			return release, nil
		}
		status, statusErr := normalizeReleaseStatusOutput(result.Output)
		if statusErr != nil {
			a.markReleaseStatusFailed(&release, result, "invalid release status output", statusErr.Error())
			return release, statusErr
		}
		release.Status = status
		release.Output = status
		if artifact, artifactErr := a.saveDeployArtifact(release.ID, "status", "command-output", result.Output); artifactErr == nil {
			release.Artifacts = append(release.Artifacts, artifact)
		}
		release.addAudit("status", "succeeded", status)
		if err := a.saveRelease(release); err != nil {
			return ReleaseRecord{}, err
		}
		return release, nil
	})
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
	if existing, ok, err := a.findRollbackForRelease(project, environment, releaseID); err != nil {
		return ReleaseRecord{}, err
	} else if ok {
		return existing, nil
	}
	unlock, err := a.acquireDeployLock(project, environment)
	if err != nil {
		return ReleaseRecord{}, err
	}
	defer unlock()
	if existing, ok, err := a.findRollbackForRelease(project, environment, releaseID); err != nil {
		return ReleaseRecord{}, err
	} else if ok {
		return existing, nil
	}
	if plan.Strategy == "blue-green" {
		return a.rollbackBlueGreenRelease(plan, release)
	}
	rollback := newReleaseRecord(plan, "rolling_back", releaseID)
	rollback.addAudit("rollback", "started", "running app-owned rollback command")
	if err := a.saveReleaseHistory(rollback); err != nil {
		return ReleaseRecord{}, err
	}
	if err := a.runRollbackCommand(plan, &rollback, plan.RollbackCommand, "rollback", releaseID, "app-owned rollback command completed"); err != nil {
		return rollback, err
	}
	if err := a.saveRelease(rollback); err != nil {
		return ReleaseRecord{}, err
	}
	return rollback, nil
}
