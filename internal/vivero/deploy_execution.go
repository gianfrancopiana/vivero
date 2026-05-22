package vivero

import (
	"context"
	"fmt"
	"strings"
)

type deployCommandInvocation struct {
	Action   string
	ExtraEnv map[string]string
}

type deployPhaseBehavior struct {
	ActionPrefix          string
	AuditPrefix           string
	StartedMessage        string
	ArtifactKind          string
	SaveArtifactOnSuccess bool
	LabelOutput           bool
	FailureStatus         func(string) string
}

func newReleaseRecord(plan DeployPlan, status, rollbackOf string) ReleaseRecord {
	now := nowUTC()
	return ReleaseRecord{StateVersion: deployStateVersion, ID: newDeployID("rel"), PlanID: plan.ID, Project: plan.Project, Environment: plan.Environment, Strategy: plan.Strategy, Status: status, RollbackOf: rollbackOf, CreatedAt: now, UpdatedAt: now}
}

func runDeployShellResultContext(ctx context.Context, plan DeployPlan, release ReleaseRecord, command string, invocation deployCommandInvocation) (deployCommandResult, error) {
	env := map[string]string{
		"VIVERO_DEPLOY_PLAN_ID": plan.ID,
		"VIVERO_RELEASE_ID":     release.ID,
		"VIVERO_PROJECT":        plan.Project,
		"VIVERO_ENVIRONMENT":    plan.Environment,
	}
	if release.ActiveSlot != "" {
		env["VIVERO_BLUE_GREEN_ACTIVE_SLOT"] = release.ActiveSlot
	}
	if release.TargetSlot != "" {
		env["VIVERO_BLUE_GREEN_TARGET_SLOT"] = release.TargetSlot
	}
	if release.PreviousSlot != "" {
		env["VIVERO_BLUE_GREEN_PREVIOUS_SLOT"] = release.PreviousSlot
	}
	if plan.BlueGreen != nil {
		env["VIVERO_BLUE_GREEN_SLOTS"] = strings.Join(plan.BlueGreen.Slots, ",")
	}
	if plan.Cache != nil {
		if plan.Cache.Dir != "" {
			env["VIVERO_CACHE_DIR"] = plan.Cache.Dir
		}
		if len(plan.Cache.Build.From) > 0 {
			env["VIVERO_BUILD_CACHE_FROM"] = dockerBuildCacheSpecsJSON(plan.Cache.Build.From)
		}
		if len(plan.Cache.Build.To) > 0 {
			env["VIVERO_BUILD_CACHE_TO"] = dockerBuildCacheSpecsJSON(plan.Cache.Build.To)
		}
	}
	if invocation.Action != "" {
		env["VIVERO_RELEASE_ACTION"] = invocation.Action
	}
	for k, v := range invocation.ExtraEnv {
		env[k] = v
	}
	return runDeployCommand(ctx, plan.Path, env, command, deployCommandOptionsForPlan(plan, invocation.Action))
}

func runDeployShellContext(ctx context.Context, plan DeployPlan, release ReleaseRecord, command string, invocation deployCommandInvocation) ([]byte, error) {
	result, err := runDeployShellResultContext(ctx, plan, release, command, invocation)
	return []byte(result.Output), err
}

func (a *App) deployContext() context.Context {
	if a != nil && a.Context != nil {
		return a.Context
	}
	return context.Background()
}

func (a *App) runCommandDeployPhase(plan DeployPlan, release *ReleaseRecord, phase DeployPhasePlan) (DeployPhaseRecord, error) {
	return a.runDeployPhase(plan, release, phase, deployPhaseBehavior{
		StartedMessage:        "running app-owned %s command",
		ArtifactKind:          "command-output",
		SaveArtifactOnSuccess: true,
		FailureStatus:         commandDeployPhaseFailureStatus,
	})
}

func (a *App) runBlueGreenDeployPhase(plan DeployPlan, release *ReleaseRecord, phase DeployPhasePlan) (DeployPhaseRecord, error) {
	return a.runDeployPhase(plan, release, phase, deployPhaseBehavior{
		ActionPrefix:  "blue_green_",
		AuditPrefix:   "blue_green_",
		ArtifactKind:  "phase-output",
		FailureStatus: func(name string) string { return name + "_failed" },
		LabelOutput:   true,
	})
}

func (a *App) runDeployPhase(plan DeployPlan, release *ReleaseRecord, phase DeployPhasePlan, behavior deployPhaseBehavior) (DeployPhaseRecord, error) {
	name := strings.TrimSpace(phase.Name)
	command := strings.TrimSpace(phase.Command)
	record := DeployPhaseRecord{Name: name, Status: "skipped"}
	if name == "" || command == "" {
		return record, nil
	}
	action := behavior.ActionPrefix + name
	auditAction := behavior.AuditPrefix + name
	if behavior.StartedMessage != "" {
		release.addAudit(auditAction, "started", fmt.Sprintf(behavior.StartedMessage, name))
	}
	result, err := runDeployShellResultContext(a.deployContext(), plan, *release, command, deployCommandInvocation{Action: action})
	record.DurationMS = result.Duration.Milliseconds()
	record.Output = strings.TrimSpace(result.Output)
	record.Status = "succeeded"
	shouldSaveArtifact := err != nil || (behavior.SaveArtifactOnSuccess && record.Output != "")
	if shouldSaveArtifact {
		artifactKind := behavior.ArtifactKind
		if artifactKind == "" {
			artifactKind = "command-output"
		}
		if artifact, artifactErr := a.saveDeployArtifact(release.ID, name, artifactKind, result.Output); artifactErr == nil {
			release.Artifacts = append(release.Artifacts, artifact)
			record.Artifact = &artifact
		}
	}
	output := record.Output
	if behavior.LabelOutput && output != "" {
		output = name + ": " + output
	}
	release.Output = appendReleaseOutput(release.Output, output)
	if err != nil {
		record.Status = "failed"
		if behavior.FailureStatus != nil {
			release.Status = behavior.FailureStatus(name)
		} else {
			release.Status = name + "_failed"
		}
		release.addAudit(auditAction, "failed", record.Output)
		return record, err
	}
	release.addAudit(auditAction, "succeeded", record.Output)
	return record, nil
}

func commandDeployPhaseFailureStatus(name string) string {
	if name == "apply" {
		return "failed"
	}
	return name + "_failed"
}

func (a *App) runRollbackCommand(plan DeployPlan, rollback *ReleaseRecord, command, action, releaseID, successMessage string) error {
	out, err := runDeployShellContext(a.deployContext(), plan, *rollback, command, deployCommandInvocation{Action: action, ExtraEnv: map[string]string{"VIVERO_ROLLBACK_RELEASE_ID": releaseID}})
	rollback.Output = strings.TrimSpace(string(out))
	if err != nil {
		rollback.Status = "rollback_failed"
		rollback.addAudit(action, "failed", rollback.Output)
		if artifact, artifactErr := a.saveDeployArtifact(rollback.ID, "rollback", "command-output", string(out)); artifactErr == nil {
			rollback.Artifacts = append(rollback.Artifacts, artifact)
		}
		_ = a.saveReleaseHistory(*rollback)
		return fmt.Errorf("release rollback failed: %w: %s", err, rollback.Output)
	}
	rollback.Status = "rolled_back"
	rollback.addAudit(action, "succeeded", successMessage)
	return nil
}

func (a *App) markReleaseStatusFailed(release *ReleaseRecord, result deployCommandResult, output, auditDetail string) {
	release.Status = "status_failed"
	release.Output = appendReleaseOutput(release.Output, output)
	release.addAudit("status", "failed", auditDetail)
	if artifact, artifactErr := a.saveDeployArtifact(release.ID, "status", "command-output", result.Output); artifactErr == nil {
		release.Artifacts = append(release.Artifacts, artifact)
	}
	_ = a.saveRelease(*release)
}

func (a *App) withDeployLock(project, environment string, fn func() (ReleaseRecord, error)) (ReleaseRecord, error) {
	unlock, err := a.acquireDeployLock(project, environment)
	if err != nil {
		return ReleaseRecord{}, err
	}
	defer unlock()
	return fn()
}

func (a *App) loadCurrentReleaseWithPlan(project, environment string) (ReleaseRecord, DeployPlan, error) {
	release, err := a.loadCurrentRelease(project, environment)
	if err != nil {
		return ReleaseRecord{}, DeployPlan{}, err
	}
	plan, err := a.loadDeployPlan(release.PlanID)
	if err != nil {
		return ReleaseRecord{}, DeployPlan{}, err
	}
	return release, plan, nil
}
