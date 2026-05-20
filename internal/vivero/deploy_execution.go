package vivero

import (
	"strings"
)

func newReleaseRecord(plan DeployPlan, status, rollbackOf string) ReleaseRecord {
	now := nowUTC()
	return ReleaseRecord{ID: newDeployID("rel"), PlanID: plan.ID, Project: plan.Project, Environment: plan.Environment, Strategy: plan.Strategy, Status: status, RollbackOf: rollbackOf, CreatedAt: now, UpdatedAt: now}
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
	for k, v := range extra {
		env[k] = v
	}
	return runCmd(plan.Path, env, "/bin/sh", "-lc", command)
}
