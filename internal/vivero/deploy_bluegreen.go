package vivero

import (
	"context"
	"fmt"
	"strings"
)

func (p *DeployPlan) configureBlueGreenDeploy(ctx context.Context, environment string, cfg BlueGreenDeployConfig) {
	base := "deploy.environments." + environment + ".blueGreen"
	slots := normalizeBlueGreenSlots(cfg.Slots)
	if len(slots) != 2 {
		p.addDiagnostic("error", "blue-green-slots-invalid", base+".slots", "blue/green deploys require exactly two distinct slots", "Set blueGreen.slots to two names, for example [blue, green].")
	}
	activeSlotCommand := strings.TrimSpace(cfg.ActiveSlotCommand)
	prepareCommand := strings.TrimSpace(cfg.PrepareCommand)
	smokeCommand := strings.TrimSpace(cfg.SmokeCommand)
	promoteCommand := strings.TrimSpace(cfg.PromoteCommand)
	p.StatusCommand = strings.TrimSpace(cfg.StatusCommand)
	p.SmokeCommand = smokeCommand
	p.RollbackCommand = strings.TrimSpace(cfg.RollbackCommand)

	if activeSlotCommand == "" {
		p.addDiagnostic("error", "blue-green-active-slot-missing", base+".activeSlotCommand", "blue/green deploy needs a read-only active slot command", "Set activeSlotCommand so Vivero can plan the inactive target slot before promotion.")
	}
	if prepareCommand == "" {
		p.addDiagnostic("error", "blue-green-prepare-missing", base+".prepareCommand", "blue/green deploy needs a prepare command", "Set prepareCommand to deploy the release to the inactive slot without switching traffic.")
	}
	if smokeCommand == "" {
		p.addDiagnostic("error", "blue-green-smoke-missing", base+".smokeCommand", "blue/green deploy needs a smoke gate before promotion", "Set smokeCommand to verify the inactive slot before promoteCommand switches traffic.")
	}
	if promoteCommand == "" {
		p.addDiagnostic("error", "blue-green-promote-missing", base+".promoteCommand", "blue/green deploy needs a promote command", "Set promoteCommand to switch production traffic to the verified target slot.")
	}
	if p.RollbackCommand == "" {
		p.addDiagnostic("error", "blue-green-rollback-missing", base+".rollbackCommand", "blue/green deploy needs a rollback command", "Set rollbackCommand to switch traffic back to the previous slot.")
	}

	activeSlot := ""
	if activeSlotCommand != "" {
		result, err := runDeployCommand(ctx, p.Path, map[string]string{
			"VIVERO_DEPLOY_PLAN_ID":   p.ID,
			"VIVERO_PROJECT":          p.Project,
			"VIVERO_ENVIRONMENT":      p.Environment,
			"VIVERO_BLUE_GREEN_SLOTS": strings.Join(slots, ","),
			"VIVERO_RELEASE_ACTION":   "blue_green_active_slot",
		}, activeSlotCommand, deployCommandOptionsForPlan(*p, "blue_green_active_slot"))
		activeSlot = strings.TrimSpace(result.Output)
		if err != nil {
			code := "blue-green-active-slot-failed"
			if result.TimedOut {
				code = "blue-green-active-slot-timeout"
			}
			message := strings.TrimSpace(result.Output)
			if message == "" {
				message = err.Error()
			}
			p.addDiagnostic("error", code, base+".activeSlotCommand", fmt.Sprintf("active slot command failed: %s", message), "Fix activeSlotCommand so it prints the current live slot and exits zero before statusTimeout.")
		}
	}
	targetSlot := ""
	if len(slots) == 2 {
		if activeSlot == "" {
			// Keep the plan inspectable even when another diagnostic already blocks it.
			targetSlot = slots[0]
		} else if activeSlot == slots[0] {
			targetSlot = slots[1]
		} else if activeSlot == slots[1] {
			targetSlot = slots[0]
		} else {
			p.addDiagnostic("error", "blue-green-active-slot-invalid", base+".activeSlotCommand", fmt.Sprintf("active slot %q is not one of %s", activeSlot, strings.Join(slots, ", ")), "Make activeSlotCommand print exactly one configured slot name.")
		}
	}
	p.BlueGreen = &BlueGreenDeployPlan{
		Slots:        slots,
		ActiveSlot:   activeSlot,
		TargetSlot:   targetSlot,
		PreviousSlot: activeSlot,
		Phases: []DeployPhasePlan{
			{Name: "prepare", Command: prepareCommand},
			{Name: "smoke", Command: smokeCommand},
			{Name: "promote", Command: promoteCommand},
		},
	}
}

func normalizeBlueGreenSlots(raw []string) []string {
	if len(raw) == 0 {
		raw = []string{"blue", "green"}
	}
	slots := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, slot := range raw {
		slot = strings.TrimSpace(slot)
		if slot == "" || seen[slot] {
			continue
		}
		seen[slot] = true
		slots = append(slots, slot)
	}
	return slots
}

func (a *App) applyBlueGreenDeployPlan(plan DeployPlan) (ReleaseRecord, error) {
	if plan.BlueGreen == nil {
		return ReleaseRecord{}, fmt.Errorf("deploy plan %s has no blue/green plan", plan.ID)
	}
	release := newReleaseRecord(plan, "promoting", "")
	release.PreviousSlot = plan.BlueGreen.ActiveSlot
	release.TargetSlot = plan.BlueGreen.TargetSlot
	release.ActiveSlot = plan.BlueGreen.ActiveSlot
	release.addAudit("blue_green_apply", "started", fmt.Sprintf("targeting inactive slot %s", release.TargetSlot))
	if err := a.saveReleaseHistory(release); err != nil {
		return ReleaseRecord{}, err
	}
	for _, phase := range plan.BlueGreen.Phases {
		record, err := a.runBlueGreenDeployPhase(plan, &release, phase)
		if record.Status != "skipped" {
			release.Phases = append(release.Phases, record)
		}
		if err != nil {
			_ = a.saveReleaseHistory(release)
			return release, fmt.Errorf("blue/green deploy %s_failed: %w: %s", phase.Name, err, record.Output)
		}
	}
	release.Status = "promoted"
	release.ActiveSlot = plan.BlueGreen.TargetSlot
	release.PreviousSlot = plan.BlueGreen.ActiveSlot
	release.addAudit("blue_green_apply", "succeeded", fmt.Sprintf("promoted %s", release.ActiveSlot))
	if err := a.saveRelease(release); err != nil {
		return ReleaseRecord{}, err
	}
	return release, nil
}

func (a *App) rollbackBlueGreenRelease(plan DeployPlan, release ReleaseRecord) (ReleaseRecord, error) {
	rollback := newReleaseRecord(plan, "rolling_back", release.ID)
	rollback.ActiveSlot = release.ActiveSlot
	rollback.PreviousSlot = release.ActiveSlot
	rollback.TargetSlot = release.PreviousSlot
	if rollback.TargetSlot == "" && plan.BlueGreen != nil {
		rollback.TargetSlot = plan.BlueGreen.ActiveSlot
	}
	rollback.addAudit("blue_green_rollback", "started", fmt.Sprintf("targeting previous slot %s", rollback.TargetSlot))
	if err := a.saveReleaseHistory(rollback); err != nil {
		return ReleaseRecord{}, err
	}
	if err := a.runRollbackCommand(plan, &rollback, plan.RollbackCommand, "blue_green_rollback", release.ID, fmt.Sprintf("active slot is now %s", rollback.TargetSlot)); err != nil {
		return rollback, err
	}
	rollback.ActiveSlot = rollback.TargetSlot
	rollback.PreviousSlot = release.ActiveSlot
	if err := a.saveRelease(rollback); err != nil {
		return ReleaseRecord{}, err
	}
	return rollback, nil
}
