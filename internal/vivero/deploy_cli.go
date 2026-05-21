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
			if jsonOut && strings.TrimSpace(release.ID) != "" {
				output(stdout, jsonOut, releaseFailurePayload(release, err), releaseHuman(release))
				return 1
			}
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, attachEvidenceShape(map[string]any{"release": release}, releaseTargetRef(release)), releaseHuman(release))
		if !releaseStatusReapplySafe(release) {
			return 1
		}
		return 0
	default:
		return errOut(stderr, jsonOut, unknownSubcommandError("deploy", args[0]))
	}
}

func (a *App) runRelease(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	if len(args) == 0 {
		return errOut(stderr, jsonOut, missingArgError("release", "status|events|logs|smoke|rollback"))
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
			if jsonOut && strings.TrimSpace(release.ID) != "" {
				output(stdout, jsonOut, releaseFailurePayload(release, err), releaseHuman(release))
				return 1
			}
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, attachEvidenceShape(map[string]any{"release": release, "status": release.Status}, releaseTargetRef(release)), releaseHuman(release))
		return 0
	case "events":
		pos := positionalArgs(args[1:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("release events", "project or release:<id>"))
		}
		environment, _ := flagValue(args[1:], "--environment")
		release, targetRef, err := a.resolveReleaseTarget(pos[0], environment)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, attachEvidenceShape(map[string]any{"release": release, "events": release.Audit}, targetRef), releaseEventsHuman(release))
		return 0
	case "logs":
		pos := positionalArgs(args[1:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("release logs", "project or release:<id>"))
		}
		environment, _ := flagValue(args[1:], "--environment")
		release, targetRef, err := a.resolveReleaseTarget(pos[0], environment)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		logs := releaseLogs(release)
		output(stdout, jsonOut, attachEvidenceShape(map[string]any{"release": release, "logs": logs}, targetRef), releaseLogsHuman(release, logs))
		return 0
	case "smoke":
		pos := positionalArgs(args[1:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("release smoke", "project or release:<id>"))
		}
		environment, _ := flagValue(args[1:], "--environment")
		release, targetRef, err := a.resolveReleaseTarget(pos[0], environment)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		release, smoke, err := a.SmokeRelease(release)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, attachEvidenceShape(map[string]any{"release": release, "smoke": smoke}, targetRef), releaseSmokeHuman(release, smoke))
		if !smoke.OK {
			return 1
		}
		return 0
	case "rollback":
		pos := positionalArgs(args[1:])
		if len(pos) < 2 {
			return errOut(stderr, jsonOut, missingRequiredError("release rollback", "project and release-id", "vivero help release rollback"))
		}
		environment, _ := flagValue(args[1:], "--environment")
		release, err := a.RollbackRelease(pos[0], pos[1], environment)
		if err != nil {
			if jsonOut && strings.TrimSpace(release.ID) != "" {
				output(stdout, jsonOut, releaseFailurePayload(release, err), releaseHuman(release))
				return 1
			}
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, attachEvidenceShape(map[string]any{"release": release}, releaseTargetRef(release)), releaseHuman(release))
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

func releaseEventsHuman(release ReleaseRecord) string {
	return fmt.Sprintf("release %s events=%d", release.ID, len(release.Audit))
}

func releaseLogsHuman(release ReleaseRecord, logs []ReleaseLogEntry) string {
	return fmt.Sprintf("release %s logs=%d", release.ID, len(logs))
}

func releaseSmokeHuman(release ReleaseRecord, smoke ReleaseSmokeResult) string {
	status := "failed"
	if smoke.OK {
		status = "ok"
	}
	return fmt.Sprintf("release %s smoke %s", release.ID, status)
}

func releaseFailurePayload(release ReleaseRecord, err error) map[string]any {
	return attachEvidenceShape(map[string]any{"release": release, "error": cliErrorPayload(err).Error}, releaseTargetRef(release))
}
