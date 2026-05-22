package vivero

import (
	"context"
	"strings"
)

func newReleaseRecord(plan DeployPlan, status, rollbackOf string) ReleaseRecord {
	now := nowUTC()
	return ReleaseRecord{StateVersion: deployStateVersion, ID: newDeployID("rel"), PlanID: plan.ID, Project: plan.Project, Environment: plan.Environment, Strategy: plan.Strategy, Status: status, RollbackOf: rollbackOf, CreatedAt: now, UpdatedAt: now}
}

func runDeployShellResultContext(ctx context.Context, plan DeployPlan, release ReleaseRecord, command string, extra map[string]string) (deployCommandResult, error) {
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
	for k, v := range extra {
		env[k] = v
	}
	action := extra["VIVERO_RELEASE_ACTION"]
	return runDeployCommand(ctx, plan.Path, env, command, deployCommandOptionsForPlan(plan, action))
}

func runDeployShellContext(ctx context.Context, plan DeployPlan, release ReleaseRecord, command string, extra map[string]string) ([]byte, error) {
	result, err := runDeployShellResultContext(ctx, plan, release, command, extra)
	return []byte(result.Output), err
}

func (a *App) deployContext() context.Context {
	if a != nil && a.Context != nil {
		return a.Context
	}
	return context.Background()
}

func (a *App) runCommandDeployPhase(plan DeployPlan, release *ReleaseRecord, phase DeployPhasePlan) (DeployPhaseRecord, error) {
	name := strings.TrimSpace(phase.Name)
	command := strings.TrimSpace(phase.Command)
	record := DeployPhaseRecord{Name: name, Status: "skipped"}
	if name == "" || command == "" {
		return record, nil
	}
	release.addAudit(name, "started", "running app-owned "+name+" command")
	result, err := runDeployShellResultContext(a.deployContext(), plan, *release, command, map[string]string{"VIVERO_RELEASE_ACTION": name})
	record.DurationMS = result.Duration.Milliseconds()
	record.Output = strings.TrimSpace(result.Output)
	record.Status = "succeeded"
	if record.Output != "" || err != nil {
		if artifact, artifactErr := a.saveDeployArtifact(release.ID, name, "command-output", result.Output); artifactErr == nil {
			release.Artifacts = append(release.Artifacts, artifact)
			record.Artifact = &artifact
		}
	}
	release.Output = appendReleaseOutput(release.Output, record.Output)
	if err != nil {
		record.Status = "failed"
		release.Status = commandDeployPhaseFailureStatus(name)
		release.addAudit(name, "failed", record.Output)
		return record, err
	}
	release.addAudit(name, "succeeded", record.Output)
	return record, nil
}

func commandDeployPhaseFailureStatus(name string) string {
	if name == "apply" {
		return "failed"
	}
	return name + "_failed"
}
