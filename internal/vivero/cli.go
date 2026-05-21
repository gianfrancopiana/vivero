package vivero

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func Run(args []string, stdout, stderr io.Writer) int {
	jsonOut := hasArg(args, "--json")
	if len(args) == 0 {
		fmt.Fprint(stdout, usage())
		return 0
	}
	if hasArg(args, "--version") {
		output(stdout, jsonOut, buildVersionInfo(), Version)
		return 0
	}
	if args[0] == "help" || hasArg(args, "--help") || hasArg(args, "-h") {
		path := helpPathFromArgs(args)
		if len(path) == 0 {
			fmt.Fprint(stdout, usage())
			return 0
		}
		if help, ok := commandHelp(path); ok {
			fmt.Fprint(stdout, help)
			return 0
		}
		return errOut(stderr, jsonOut, unknownCommandError(strings.Join(path, " ")))
	}
	if args[0] == "_proxy" {
		listen, _ := flagValue(args[1:], "--listen")
		target, _ := flagValue(args[1:], "--target")
		host, _ := flagValue(args[1:], "--host")
		rewriteJSON, _ := flagValue(args[1:], "--rewrite-json")
		publicRewrite, err := fromJSONString[PublicRewriteConfig](rewriteJSON)
		if err != nil {
			return errOut(stderr, jsonOut, fmt.Errorf("invalid --rewrite-json: %w", err))
		}
		if err := runHeaderRewriteProxy(listen, target, host, publicRewrite); err != nil {
			return errOut(stderr, jsonOut, err)
		}
		return 0
	}
	if !knownTopLevelCommand(args[0]) {
		return errOut(stderr, jsonOut, unknownCommandError(args[0]))
	}
	if group, action, ok := stateFreeUnknownSubcommand(args); ok {
		return errOut(stderr, jsonOut, unknownSubcommandError(group, action))
	}
	switch args[0] {
	case "commands":
		output(stdout, jsonOut, map[string]any{"commands": commandCatalog()}, commandsHuman())
		return 0
	case "schema":
		rest := args[1:]
		pos := positionalArgs(rest)
		command := ""
		if len(pos) > 0 {
			command = strings.Join(pos, " ")
		}
		schema := schemaFor(command)
		if command != "" {
			if body, ok := schema["schema"].(map[string]any); ok && body["unknown"] == true {
				return errOut(stderr, jsonOut, unknownCommandError(command))
			}
		}
		output(stdout, jsonOut, schema, "schema: "+command)
		return 0
	}
	if args[0] == "preview" {
		if len(args) < 2 {
			return errOut(stderr, jsonOut, missingRequiredError("preview", "subcommand", "vivero help preview"))
		}
		return Run(args[1:], stdout, stderr)
	}
	a, err := NewApp()
	if err != nil {
		return errOut(stderr, jsonOut, err)
	}
	defer a.Close()
	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "serve":
		addr, _ := flagValue(rest, "--addr")
		if addr == "" {
			addr = "127.0.0.1:7777"
		}
		if err := a.Serve(addr); err != nil {
			return errOut(stderr, jsonOut, err)
		}
		return 0
	case "capabilities":
		output(stdout, jsonOut, a.capabilities(), "vivero "+Version)
		return 0
	case "version":
		output(stdout, jsonOut, buildVersionInfo(), "vivero "+Version)
		return 0
	case "doctor":
		if len(rest) > 0 && rest[0] == "config" {
			pos := positionalArgs(rest[1:])
			if len(pos) == 0 {
				return errOut(stderr, jsonOut, missingArgError("doctor config", "path"))
			}
			report, err := a.ConfigDoctor(pos[0])
			if err != nil {
				return errOut(stderr, jsonOut, err)
			}
			output(stdout, jsonOut, map[string]any{"configDoctor": report}, configDoctorHuman(report))
			if !report.OK {
				return 1
			}
			return 0
		}
		if len(rest) > 0 && rest[0] == "production" {
			path := doctorProjectPath(rest[1:])
			report, err := a.ProductionDoctor(path)
			if err != nil {
				return errOut(stderr, jsonOut, err)
			}
			output(stdout, jsonOut, map[string]any{"productionDoctor": report}, productionDoctorHuman(report))
			if !report.OK {
				return 1
			}
			return 0
		}
		if hasArg(rest, "--production") {
			path := doctorProjectPath(rest)
			report, err := a.ProductionDoctor(path)
			if err != nil {
				return errOut(stderr, jsonOut, err)
			}
			output(stdout, jsonOut, map[string]any{"productionDoctor": report}, productionDoctorHuman(report))
			if !report.OK {
				return 1
			}
			return 0
		}
		if path, ok := flagValue(rest, "--project"); ok {
			report, err := a.ConfigDoctor(path)
			if err != nil {
				return errOut(stderr, jsonOut, err)
			}
			output(stdout, jsonOut, map[string]any{"configDoctor": report}, configDoctorHuman(report))
			if !report.OK {
				return 1
			}
			return 0
		}
		v, err := a.Doctor()
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, doctorHuman(v))
		if ok, _ := v["ok"].(bool); !ok {
			return 1
		}
		return 0
	case "deploy":
		return a.runDeploy(rest, stdout, stderr, jsonOut)
	case "release":
		return a.runRelease(rest, stdout, stderr, jsonOut)
	case "cache":
		return a.runCache(rest, stdout, stderr, jsonOut)
	case "projects":
		if len(rest) > 0 && rest[0] == "sync" {
			pos := positionalArgs(rest[1:])
			if len(pos) == 0 {
				return errOut(stderr, jsonOut, missingArgError("projects sync", "path"))
			}
			rec, err := a.SyncProject(pos[0])
			if err != nil {
				return errOut(stderr, jsonOut, err)
			}
			output(stdout, jsonOut, map[string]any{"project": rec}, "synced project "+rec.Name)
			return 0
		}
		list, err := a.listProjects()
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"projects": list}, projectsHuman(list))
		return 0
	case "project":
		pos := positionalArgs(rest)
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("project inspect", "project"))
		}
		if pos[0] != "inspect" {
			return errOut(stderr, jsonOut, unknownSubcommandError("project", pos[0]))
		}
		if len(pos) < 2 {
			return errOut(stderr, jsonOut, missingArgError("project inspect", "project"))
		}
		name := pos[1]
		rec, err := a.getProject(name)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"project": rec}, "project "+rec.Name)
		return 0
	case "up":
		pos := positionalArgs(rest)
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("up", "project"))
		}
		id, _ := flagValue(rest, "--id")
		if id == "" {
			return errOut(stderr, jsonOut, missingRequiredError("up", "--id", "vivero help up"))
		}
		sources, err := collectKV(rest, "--source")
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		labels, err := collectKV(rest, "--label")
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		metadata, err := collectKV(rest, "--metadata")
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		timeout, err := durationFlag(rest, "--timeout", 5*time.Minute)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		profile, _ := flagValue(rest, "--profile")
		p, err := a.Up(UpRequest{Project: pos[0], ID: id, Profile: profile, Sources: sources, Labels: labels, Metadata: metadata, Wait: hasArg(rest, "--wait"), Timeout: timeout, Public: hasArg(rest, "--public")})
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"preview": p}, previewHuman(p))
		return 0
	case "wait":
		pos := positionalArgs(rest)
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("wait", "preview"))
		}
		timeout, err := durationFlag(rest, "--timeout", 5*time.Minute)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		if err := a.Wait(pos[0], timeout); err != nil {
			return errOut(stderr, jsonOut, err)
		}
		p, _ := a.getPreview(pos[0])
		output(stdout, jsonOut, map[string]any{"preview": p}, "ready "+pos[0])
		return 0
	case "down":
		pos := positionalArgs(rest)
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("down", "preview"))
		}
		mode := ""
		if hasArg(rest, "--discard") {
			mode = "discard"
		}
		if hasArg(rest, "--archive-patch") {
			mode = "archive-patch"
		}
		if hasArg(rest, "--keep-worktree") {
			mode = "keep-worktree"
		}
		p, err := a.Down(pos[0], mode)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"preview": p}, "down "+pos[0])
		return 0
	case "list":
		ps, err := a.listPreviews()
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"previews": ps}, previewsHuman(ps))
		return 0
	case "inspect":
		pos := positionalArgs(rest)
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("inspect", "preview"))
		}
		previewID, targetRef, err := resolvePreviewTargetRef(pos[0])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		p, err := a.getPreview(previewID)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, attachEvidenceShape(map[string]any{"preview": p}, targetRef), previewHuman(p))
		return 0
	case "events":
		pos := positionalArgs(rest)
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("events", "preview"))
		}
		limit := 0
		if hasArg(rest, "--tail") {
			limit = 50
		}
		previewID, targetRef, err := resolvePreviewTargetRef(pos[0])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		ev, err := a.events(previewID, limit)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, attachEvidenceShape(map[string]any{"events": ev}, targetRef), eventsHuman(ev))
		return 0
	case "sync":
		pos := positionalArgs(rest)
		if len(pos) < 3 {
			return errOut(stderr, jsonOut, missingRequiredError("sync", "preview, source, and path", "vivero help sync"))
		}
		from, ok := flagValue(rest, "--from")
		if !ok {
			return errOut(stderr, jsonOut, missingRequiredError("sync", "--from", "vivero help sync"))
		}
		v, err := a.SyncFile(pos[0], pos[1], pos[2], from)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, "synced "+pos[2])
		return 0
	case "rm":
		pos := positionalArgs(rest)
		if len(pos) < 3 {
			return errOut(stderr, jsonOut, missingRequiredError("rm", "preview, source, and path", "vivero help rm"))
		}
		v, err := a.RemoveFile(pos[0], pos[1], pos[2])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, "removed "+pos[2])
		return 0
	case "diff":
		pos := positionalArgs(rest)
		if len(pos) < 2 {
			return errOut(stderr, jsonOut, missingRequiredError("diff", "preview and source", "vivero help diff"))
		}
		v, err := a.Diff(pos[0], pos[1])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, fmt.Sprint(v["status"])+fmt.Sprint(v["diff"]))
		return 0
	case "exec":
		pos := positionalArgs(rest)
		if len(pos) < 2 {
			return errOut(stderr, jsonOut, missingRequiredError("exec", "preview, service, and command", "vivero help exec"))
		}
		cmdArgs := splitAfterDoubleDash(rest)
		if len(cmdArgs) == 0 {
			return errOut(stderr, jsonOut, missingRequiredError("exec", "command after --", "vivero help exec"))
		}
		previewID, targetRef, err := resolvePreviewTargetRef(pos[0])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		v, err := a.Exec(previewID, pos[1], cmdArgs)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachTargetRef(v, targetRef)
		output(stdout, jsonOut, v, fmt.Sprint(v["stdout"])+fmt.Sprint(v["stderr"]))
		if v["exitCode"].(int) != 0 {
			return v["exitCode"].(int)
		}
		return 0
	case "logs":
		pos := positionalArgs(rest)
		if len(pos) < 2 {
			return errOut(stderr, jsonOut, missingRequiredError("logs", "preview and service", "vivero help logs"))
		}
		previewID, targetRef, err := resolvePreviewTargetRef(pos[0])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		v, err := a.Logs(previewID, pos[1], 200)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, strings.Join(v["lines"].([]string), "\n"))
		return 0
	case "smoke":
		pos := positionalArgs(rest)
		if len(pos) < 1 {
			return errOut(stderr, jsonOut, missingArgError("smoke", "preview"))
		}
		previewID, targetRef, err := resolvePreviewTargetRef(pos[0])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		name := ""
		if len(pos) > 1 {
			name = pos[1]
		}
		v, err := a.Smoke(previewID, name)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, fmt.Sprintf("smoke ok=%v", v["ok"]))
		if v["ok"] != true {
			return 1
		}
		return 0
	case "screenshot":
		pos := positionalArgs(rest)
		if len(pos) < 2 {
			return errOut(stderr, jsonOut, missingRequiredError("screenshot", "preview and service", "vivero help screenshot"))
		}
		previewID, targetRef, err := resolvePreviewTargetRef(pos[0])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		path := "/"
		if flagPath, ok := flagValue(rest, "--path"); ok {
			path = flagPath
		}
		if len(pos) > 2 {
			path = pos[2]
		}
		target := artifactTargetFromArgs(rest)
		opts := ScreenshotOptions{Path: path, Target: target, Crop: hasArg(rest, "--crop"), FullPage: hasArg(rest, "--full-page"), UseProjectBreakpoints: hasArg(rest, "--breakpoints")}
		if width, ok, err := positiveIntFlag(rest, "--width"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.Width = width
		}
		if height, ok, err := positiveIntFlag(rest, "--height"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.Height = height
		}
		if dsf, ok, err := positiveFloatFlag(rest, "--device-scale-factor"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.DeviceScaleFactor = dsf
		}
		if selector, ok := flagValue(rest, "--wait-for-selector"); ok {
			opts.WaitForSelector = selector
		}
		if wait, ok := flagValue(rest, "--wait-for-timeout"); ok {
			opts.WaitForTimeout = wait
		}
		if outputDir, ok := flagValue(rest, "--output-dir"); ok {
			opts.OutputDir = outputDir
		}
		if colorScheme, ok := flagValue(rest, "--color-scheme"); ok {
			opts.ColorScheme = colorScheme
		}
		if storageState, ok := flagValue(rest, "--storage-state"); ok {
			opts.StorageState = expandPath(storageState)
		}
		for _, spec := range flagValues(rest, "--breakpoint") {
			bp, err := parseScreenshotBreakpoint(spec)
			if err != nil {
				return errOut(stderr, jsonOut, err)
			}
			opts.Breakpoints = append(opts.Breakpoints, bp)
		}
		v, err := a.ScreenshotWithOptions(previewID, pos[1], opts)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, screenshotsHuman(v))
		return 0
	case "qa":
		return a.runQA(rest, stdout, stderr, jsonOut)
	case "diagnose":
		return a.runDiagnose(rest, stdout, stderr, jsonOut)
	case "prebuild":
		pos := positionalArgs(rest)
		if len(pos) < 1 {
			return errOut(stderr, jsonOut, missingArgError("prebuild", "project"))
		}
		v, err := a.Prebuild(pos[0])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, "prebuild done")
		return 0
	case "secrets":
		return a.runSecrets(rest, stdout, stderr, jsonOut)
	case "skill":
		return a.runSkill(rest, stdout, stderr, jsonOut)
	default:
		return errOut(stderr, jsonOut, unknownCommandError(cmd))
	}
}

func knownTopLevelCommand(command string) bool {
	for _, manifest := range commandCatalog() {
		if len(manifest.Path) > 0 && manifest.Path[0] == command {
			return true
		}
	}
	return false
}

func stateFreeUnknownSubcommand(args []string) (string, string, bool) {
	if len(args) < 2 {
		return "", "", false
	}
	group := args[0]
	switch group {
	case "cache", "deploy", "diagnose", "preview", "project", "qa", "release", "secrets", "skill":
	default:
		return "", "", false
	}
	action := args[1]
	if action == "" || strings.HasPrefix(action, "-") {
		return "", "", false
	}
	if commandPathPrefixExists([]string{group, action}) {
		return "", "", false
	}
	return group, action, true
}

func commandPathPrefixExists(path []string) bool {
	for _, manifest := range commandCatalog() {
		if len(manifest.Path) < len(path) {
			continue
		}
		matches := true
		for i, part := range path {
			if manifest.Path[i] != part {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func (a *App) runQA(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	pos := positionalArgs(args)
	if len(pos) < 2 {
		return errOut(stderr, jsonOut, missingRequiredError("qa", "subcommand and preview", "vivero help qa"))
	}
	action := pos[0]
	previewID, targetRef, err := resolvePreviewTargetRef(pos[1])
	if err != nil {
		return errOut(stderr, jsonOut, err)
	}
	actionArgs := args[2:]
	scope, _ := flagValue(actionArgs, "--scope")
	switch action {
	case "plan", "context":
		target := artifactTargetFromArgs(actionArgs)
		v, err := a.QAPlanWithTarget(previewID, scope, target)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, qaPlanHuman(v))
		return 0
	case "run":
		target := artifactTargetFromArgs(actionArgs)
		v, err := a.QARunWithTarget(previewID, scope, target, !hasArg(actionArgs, "--no-screenshots"))
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, qaRunHuman(v))
		if v["ok"] != true {
			return 1
		}
		return 0
	case "record":
		opts := QARecordOptions{Scope: scope}
		if width, ok, err := positiveIntFlag(actionArgs, "--width"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.Width = width
		}
		if height, ok, err := positiveIntFlag(actionArgs, "--height"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.Height = height
		}
		if dsf, ok, err := positiveFloatFlag(actionArgs, "--device-scale-factor"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.DeviceScaleFactor = dsf
		}
		if format, ok := flagValue(actionArgs, "--format"); ok {
			opts.Format = format
		}
		if outputDir, ok := flagValue(actionArgs, "--output-dir"); ok {
			opts.OutputDir = outputDir
		}
		if colorScheme, ok := flagValue(actionArgs, "--color-scheme"); ok {
			opts.ColorScheme = colorScheme
		}
		if storageState, ok := flagValue(actionArgs, "--storage-state"); ok {
			opts.StorageState = expandPath(storageState)
		}
		if ms, ok, err := nonNegativeIntFlag(actionArgs, "--slow-mo-ms"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.SlowMoMS = ms
		}
		if ms, ok, err := nonNegativeIntFlag(actionArgs, "--wait-ms"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.WaitMS = ms
		}
		v, err := a.QARecord(previewID, opts)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, qaRecordHuman(v))
		if v["ok"] != true {
			return 1
		}
		return 0
	case "final":
		opts := QAFinalOptions{Scope: scope, Target: artifactTargetFromArgs(actionArgs), SkipScreenshots: hasArg(actionArgs, "--no-screenshots"), SkipRecord: hasArg(actionArgs, "--no-record")}
		if width, ok, err := positiveIntFlag(actionArgs, "--width"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.Width = width
		}
		if height, ok, err := positiveIntFlag(actionArgs, "--height"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.Height = height
		}
		if dsf, ok, err := positiveFloatFlag(actionArgs, "--device-scale-factor"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.DeviceScaleFactor = dsf
		}
		if format, ok := flagValue(actionArgs, "--format"); ok {
			opts.Format = format
		}
		if colorScheme, ok := flagValue(actionArgs, "--color-scheme"); ok {
			opts.ColorScheme = colorScheme
		}
		if storageState, ok := flagValue(actionArgs, "--storage-state"); ok {
			opts.StorageState = expandPath(storageState)
		}
		if ms, ok, err := nonNegativeIntFlag(actionArgs, "--slow-mo-ms"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.SlowMoMS = ms
		}
		if ms, ok, err := nonNegativeIntFlag(actionArgs, "--wait-ms"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.WaitMS = ms
		}
		v, err := a.QAFinal(previewID, opts)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, qaFinalHuman(v))
		if v["ok"] != true {
			return 1
		}
		return 0
	case "report":
		target := artifactTargetFromArgs(actionArgs)
		out, _ := flagValue(actionArgs, "--out")
		v, err := a.QAReportWithTarget(previewID, scope, target, out)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, fmt.Sprintf("qa report: %s", v["path"]))
		return 0
	default:
		return errOut(stderr, jsonOut, unknownSubcommandError("qa", action))
	}
}

func artifactTargetFromArgs(args []string) string {
	target, _ := flagValue(args, "--target")
	if hasArg(args, "--public") {
		target = artifactTargetPublic
	}
	if hasArg(args, "--origin") {
		target = artifactTargetOrigin
	}
	return target
}

func (a *App) runSecrets(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	pos := positionalArgs(args)
	if len(pos) < 2 {
		return errOut(stderr, jsonOut, missingRequiredError("secrets", "subcommand and project", "vivero help secrets"))
	}
	action, project := pos[0], pos[1]
	path := a.secretFile(project)
	switch action {
	case "set":
		m, err := readEnvFile(path)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		for _, kv := range positionalArgs(args[2:]) {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return errOut(stderr, jsonOut, newCLIError("invalid_argument", "secret must be KEY=value", "Run: vivero help secrets set", map[string]string{"command": "secrets set", "argument": "KEY=value"}))
			}
			m[k] = v
		}
		if err := writeEnvFile(path, m); err != nil {
			return errOut(stderr, jsonOut, err)
		}
		keys := keysOf(m)
		output(stdout, jsonOut, map[string]any{"project": project, "keys": keys}, "set secrets: "+strings.Join(keys, ", "))
		return 0
	case "list":
		m, err := readEnvFile(path)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		keys := keysOf(m)
		output(stdout, jsonOut, map[string]any{"project": project, "keys": keys}, strings.Join(keys, "\n"))
		return 0
	case "unset":
		m, err := readEnvFile(path)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		for _, k := range positionalArgs(args[2:]) {
			delete(m, k)
		}
		if err := writeEnvFile(path, m); err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"project": project, "keys": keysOf(m)}, "unset")
		return 0
	default:
		return errOut(stderr, jsonOut, unknownSubcommandError("secrets", action))
	}
}

func (a *App) runSkill(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	pos := positionalArgs(args)
	if len(pos) == 0 {
		return errOut(stderr, jsonOut, missingRequiredError("skill", "subcommand", "vivero help skill"))
	}
	switch pos[0] {
	case "print":
		v, err := a.SkillPrint()
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		if jsonOut {
			writeJSON(stdout, v)
		} else {
			fmt.Fprint(stdout, v["content"])
		}
		return 0
	case "install":
		target, _ := flagValue(args[1:], "--target")
		v, err := a.SkillInstall(target, hasArg(args[1:], "--force"))
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, "skill installed")
		return 0
	case "path":
		v, err := a.SkillPath()
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, strings.Join(v["defaultTargets"].([]string), "\n"))
		return 0
	case "doctor":
		v, err := a.SkillDoctor()
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, "skill doctor complete")
		return 0
	default:
		return errOut(stderr, jsonOut, unknownSubcommandError("skill", pos[0]))
	}
}

func (a *App) Doctor() (map[string]any, error) {
	checks := map[string]any{"home": a.Home, "database": filepath.Join(a.Home, "state.db")}
	for _, bin := range []string{"git", "cloudflared", "docker"} {
		_, err := execLook(bin)
		checks[bin] = err == nil
	}
	localState, err := a.LocalStateDoctor()
	if err != nil {
		return nil, err
	}
	checks["projects"] = localState.Projects
	checks["previews"] = localState.Previews
	return map[string]any{"ok": localState.OK, "version": Version, "checks": checks, "localState": localState}, nil
}

func execLook(bin string) (string, error) { return exec.LookPath(bin) }

func (a *App) Prebuild(project string) (map[string]any, error) {
	rec, err := a.getProject(project)
	if err != nil {
		return nil, err
	}
	results := []map[string]any{}
	for source, pb := range rec.Config.Prebuild {
		srcCfg, ok := rec.Config.Sources[source]
		if !ok {
			continue
		}
		path := rec.Path
		if strings.TrimSpace(srcCfg.Path) != "" {
			resolved, err := resolveSourcePath(rec.Path, srcCfg.Path)
			if err != nil {
				return nil, err
			}
			path = resolved
		}
		for _, step := range pb.Steps {
			out, err := runCmd(path, nil, "/bin/sh", "-lc", step)
			r := map[string]any{"source": source, "step": step, "output": string(out), "ok": err == nil}
			if err != nil {
				r["error"] = err.Error()
				results = append(results, r)
				return map[string]any{"project": project, "ok": false, "results": results}, err
			}
			results = append(results, r)
		}
	}
	return map[string]any{"project": project, "ok": true, "results": results}, nil
}

func keysOf(m map[string]string) []string {
	return sortedMapKeys(m)
}

func projectsHuman(ps []ProjectRecord) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.Name + "\t" + p.Path + "\n")
	}
	return b.String()
}
func previewsHuman(ps []PreviewRecord) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString(p.ID + "\t" + p.Status + "\n")
	}
	return b.String()
}
func previewHuman(p PreviewRecord) string {
	var b strings.Builder
	if p.Profile != "" {
		b.WriteString(p.ID + " " + p.Status + " profile=" + p.Profile + "\n")
	} else {
		b.WriteString(p.ID + " " + p.Status + "\n")
	}
	for _, k := range sortedMapKeys(p.Services) {
		s := p.Services[k]
		b.WriteString(fmt.Sprintf("%s\t%s\t%s\n", s.Name, s.Status, s.URL))
	}
	return b.String()
}
func screenshotsHuman(v map[string]any) string {
	shots, ok := v["screenshots"].([]map[string]any)
	if !ok {
		if path, ok := v["path"].(string); ok {
			return path
		}
		return ""
	}
	var b strings.Builder
	for _, shot := range shots {
		if path, ok := shot["path"].(string); ok {
			b.WriteString(path)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
func eventsHuman(ev []Event) string {
	var b strings.Builder
	for _, e := range ev {
		b.WriteString(fmt.Sprintf("%d %s %s %s\n", e.Seq, e.Level, e.Type, e.Message))
	}
	return b.String()
}
