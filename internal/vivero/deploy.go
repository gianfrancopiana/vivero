package vivero

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
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

func (p *DeployPlan) configureBlueGreenDeploy(environment string, cfg BlueGreenDeployConfig) {
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
		out, err := runCmd(p.Path, map[string]string{
			"VIVERO_DEPLOY_PLAN_ID":   p.ID,
			"VIVERO_PROJECT":          p.Project,
			"VIVERO_ENVIRONMENT":      p.Environment,
			"VIVERO_BLUE_GREEN_SLOTS": strings.Join(slots, ","),
			"VIVERO_RELEASE_ACTION":   "blue_green_active_slot",
		}, "/bin/sh", "-lc", activeSlotCommand)
		activeSlot = strings.TrimSpace(string(out))
		if err != nil {
			p.addDiagnostic("error", "blue-green-active-slot-failed", base+".activeSlotCommand", fmt.Sprintf("active slot command failed: %s", strings.TrimSpace(string(out))), "Fix activeSlotCommand so it prints the current live slot and exits zero.")
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

func (a *App) applyBlueGreenDeployPlan(plan DeployPlan) (ReleaseRecord, error) {
	if plan.BlueGreen == nil {
		return ReleaseRecord{}, fmt.Errorf("deploy plan %s has no blue/green plan", plan.ID)
	}
	release := newReleaseRecord(plan, "promoting", "")
	release.PreviousSlot = plan.BlueGreen.ActiveSlot
	release.TargetSlot = plan.BlueGreen.TargetSlot
	release.ActiveSlot = plan.BlueGreen.ActiveSlot
	var outputs []string
	for _, phase := range plan.BlueGreen.Phases {
		if strings.TrimSpace(phase.Command) == "" {
			continue
		}
		out, err := runDeployShell(plan, release, phase.Command, map[string]string{"VIVERO_RELEASE_ACTION": "blue_green_" + phase.Name})
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			outputs = append(outputs, phase.Name+": "+trimmed)
		}
		record := DeployPhaseRecord{Name: phase.Name, Status: "succeeded", Output: trimmed}
		if err != nil {
			record.Status = "failed"
			release.Phases = append(release.Phases, record)
			release.Status = phase.Name + "_failed"
			release.Output = strings.Join(outputs, "\n")
			release.UpdatedAt = nowUTC()
			_ = a.saveReleaseHistory(release)
			return release, fmt.Errorf("blue/green deploy %s_failed: %w: %s", phase.Name, err, trimmed)
		}
		release.Phases = append(release.Phases, record)
	}
	release.Status = "promoted"
	release.ActiveSlot = plan.BlueGreen.TargetSlot
	release.PreviousSlot = plan.BlueGreen.ActiveSlot
	release.Output = strings.Join(outputs, "\n")
	release.UpdatedAt = nowUTC()
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

func (a *App) rollbackBlueGreenRelease(plan DeployPlan, release ReleaseRecord) (ReleaseRecord, error) {
	rollback := newReleaseRecord(plan, "rolled_back", release.ID)
	rollback.ActiveSlot = release.ActiveSlot
	rollback.PreviousSlot = release.ActiveSlot
	rollback.TargetSlot = release.PreviousSlot
	if rollback.TargetSlot == "" && plan.BlueGreen != nil {
		rollback.TargetSlot = plan.BlueGreen.ActiveSlot
	}
	out, err := runDeployShell(plan, rollback, plan.RollbackCommand, map[string]string{"VIVERO_RELEASE_ACTION": "blue_green_rollback", "VIVERO_ROLLBACK_RELEASE_ID": release.ID})
	rollback.Output = strings.TrimSpace(string(out))
	if err != nil {
		rollback.Status = "rollback_failed"
		rollback.UpdatedAt = nowUTC()
		_ = a.saveReleaseHistory(rollback)
		return rollback, fmt.Errorf("release rollback failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	rollback.ActiveSlot = rollback.TargetSlot
	rollback.PreviousSlot = release.ActiveSlot
	rollback.UpdatedAt = nowUTC()
	if err := a.saveRelease(rollback); err != nil {
		return ReleaseRecord{}, err
	}
	return rollback, nil
}

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
func (a *App) releaseDir() string    { return filepath.Join(a.Home, "deploy", "releases") }
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
