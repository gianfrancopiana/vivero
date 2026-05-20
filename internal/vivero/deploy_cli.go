package vivero

import (
	"fmt"
	"io"
	"strings"
)

func (a *App) runDeploy(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	if len(args) == 0 {
		return errOut(stderr, jsonOut, missingArgError("deploy", "plan|apply"))
	}
	switch args[0] {
	case "plan":
		cmdArgs := args[1:]
		projectPath, _ := flagValue(cmdArgs, "--project")
		if projectPath == "" {
			pos := positionalArgs(cmdArgs)
			if len(pos) > 0 {
				projectPath = pos[0]
			}
		}
		if strings.TrimSpace(projectPath) == "" {
			return errOut(stderr, jsonOut, missingRequiredError("deploy plan", "path or --project PATH", "vivero help deploy plan"))
		}
		environment, _ := flagValue(cmdArgs, "--environment")
		plan, err := a.DeployPlan(projectPath, environment)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"plan": plan}, deployPlanHuman(plan))
		if !plan.OK {
			return 1
		}
		return 0
	case "apply":
		pos := positionalArgs(args[1:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("deploy apply", "plan-id"))
		}
		release, err := a.ApplyDeployPlan(pos[0])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"release": release}, releaseHuman(release))
		if release.Status != "applied" && release.Status != "promoted" {
			return 1
		}
		return 0
	default:
		return errOut(stderr, jsonOut, unknownSubcommandError("deploy", args[0]))
	}
}

func (a *App) runRelease(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	if len(args) == 0 {
		return errOut(stderr, jsonOut, missingArgError("release", "status|rollback"))
	}
	switch args[0] {
	case "status":
		pos := positionalArgs(args[1:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("release status", "project"))
		}
		environment, _ := flagValue(args[1:], "--environment")
		release, err := a.CurrentRelease(pos[0], environment)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"release": release, "status": release.Status}, releaseHuman(release))
		return 0
	case "rollback":
		pos := positionalArgs(args[1:])
		if len(pos) < 2 {
			return errOut(stderr, jsonOut, missingRequiredError("release rollback", "project and release-id", "vivero help release rollback"))
		}
		environment, _ := flagValue(args[1:], "--environment")
		release, err := a.RollbackRelease(pos[0], pos[1], environment)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"release": release}, releaseHuman(release))
		if release.Status != "rolled_back" {
			return 1
		}
		return 0
	default:
		return errOut(stderr, jsonOut, unknownSubcommandError("release", args[0]))
	}
}

func deployPlanHuman(plan DeployPlan) string {
	var b strings.Builder
	strategy := plan.Strategy
	if strategy == "" {
		strategy = "command"
	}
	b.WriteString(fmt.Sprintf("deploy plan %s %s/%s %s strategy=%s\n", plan.ID, plan.Project, plan.Environment, plan.Verdict, strategy))
	if plan.BlueGreen != nil {
		b.WriteString(fmt.Sprintf("blue-green active=%s target=%s\n", plan.BlueGreen.ActiveSlot, plan.BlueGreen.TargetSlot))
	}
	for _, diag := range plan.Diagnostics {
		if diag.Level == "info" {
			continue
		}
		b.WriteString(fmt.Sprintf("%s %s: %s\n", diag.Level, diag.Code, diag.Message))
	}
	return strings.TrimRight(b.String(), "\n")
}

func releaseHuman(release ReleaseRecord) string {
	if release.Strategy == "blue-green" && release.ActiveSlot != "" {
		if release.RollbackOf != "" {
			return fmt.Sprintf("release %s %s rollbackOf=%s active=%s previous=%s", release.ID, release.Status, release.RollbackOf, release.ActiveSlot, release.PreviousSlot)
		}
		return fmt.Sprintf("release %s %s active=%s previous=%s", release.ID, release.Status, release.ActiveSlot, release.PreviousSlot)
	}
	if release.RollbackOf != "" {
		return fmt.Sprintf("release %s %s rollbackOf=%s", release.ID, release.Status, release.RollbackOf)
	}
	return fmt.Sprintf("release %s %s", release.ID, release.Status)
}
