package vivero

import (
	"strings"
	"time"
)

func newReleaseRecord(plan DeployPlan, status, rollbackOf string) ReleaseRecord {
	now := nowUTC()
	return ReleaseRecord{StateVersion: deployStateVersion, ID: newDeployID("rel"), PlanID: plan.ID, Project: plan.Project, Environment: plan.Environment, Strategy: plan.Strategy, Status: status, RollbackOf: rollbackOf, CreatedAt: now, UpdatedAt: now}
}

func runDeployShell(plan DeployPlan, release ReleaseRecord, command string, extra map[string]string) ([]byte, error) {
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
	return runCmd(plan.Path, env, "/bin/sh", "-lc", command)
}

func (a *App) runCommandDeployPhase(plan DeployPlan, release *ReleaseRecord, phase DeployPhasePlan) (DeployPhaseRecord, error) {
	name := strings.TrimSpace(phase.Name)
	command := strings.TrimSpace(phase.Command)
	record := DeployPhaseRecord{Name: name, Status: "skipped"}
	if name == "" || command == "" {
		return record, nil
	}
	release.addAudit(name, "started", "running app-owned "+name+" command")
	started := time.Now()
	out, err := runDeployShell(plan, *release, command, map[string]string{"VIVERO_RELEASE_ACTION": name})
	record.DurationMS = time.Since(started).Milliseconds()
	record.Output = strings.TrimSpace(string(out))
	record.Status = "succeeded"
	if record.Output != "" || err != nil {
		if artifact, artifactErr := a.saveDeployArtifact(release.ID, name, "command-output", string(out)); artifactErr == nil {
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
