package vivero

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func Run(args []string, stdout, stderr io.Writer) int {
	jsonOut := hasArg(args, "--json")
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(stdout, usage())
		return 0
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
	case "commands":
		output(stdout, jsonOut, map[string]any{"commands": commandCatalog()}, commandsHuman())
		return 0
	case "schema":
		pos := positionalArgs(rest)
		command := ""
		if len(pos) > 0 {
			command = pos[0]
		}
		output(stdout, jsonOut, schemaFor(command), "schema: "+command)
		return 0
	case "doctor":
		v, err := a.Doctor()
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, "doctor ok")
		return 0
	case "projects":
		if len(rest) > 0 && rest[0] == "sync" {
			pos := positionalArgs(rest[1:])
			if len(pos) == 0 {
				return errOut(stderr, jsonOut, fmt.Errorf("projects sync requires a path"))
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
		if len(rest) < 2 {
			return errOut(stderr, jsonOut, fmt.Errorf("usage: vivero project inspect <project>"))
		}
		name := rest[1]
		rec, err := a.getProject(name)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"project": rec}, "project "+rec.Name)
		return 0
	case "up":
		pos := positionalArgs(rest)
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, fmt.Errorf("up requires project"))
		}
		id, _ := flagValue(rest, "--id")
		if id == "" {
			return errOut(stderr, jsonOut, fmt.Errorf("up requires --id"))
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
			return errOut(stderr, jsonOut, fmt.Errorf("wait requires preview id"))
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
			return errOut(stderr, jsonOut, fmt.Errorf("down requires preview id"))
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
			return errOut(stderr, jsonOut, fmt.Errorf("inspect requires preview id"))
		}
		p, err := a.getPreview(pos[0])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"preview": p}, previewHuman(p))
		return 0
	case "events":
		pos := positionalArgs(rest)
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, fmt.Errorf("events requires preview id"))
		}
		limit := 0
		if hasArg(rest, "--tail") {
			limit = 50
		}
		ev, err := a.events(pos[0], limit)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"events": ev}, eventsHuman(ev))
		return 0
	case "sync":
		pos := positionalArgs(rest)
		if len(pos) < 3 {
			return errOut(stderr, jsonOut, fmt.Errorf("sync requires <preview> <source> <path>"))
		}
		from, ok := flagValue(rest, "--from")
		if !ok {
			return errOut(stderr, jsonOut, fmt.Errorf("sync requires --from"))
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
			return errOut(stderr, jsonOut, fmt.Errorf("rm requires <preview> <source> <path>"))
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
			return errOut(stderr, jsonOut, fmt.Errorf("diff requires <preview> <source>"))
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
			return errOut(stderr, jsonOut, fmt.Errorf("exec requires <preview> <service> -- <command>"))
		}
		cmdArgs := splitAfterDoubleDash(rest)
		if len(cmdArgs) == 0 {
			return errOut(stderr, jsonOut, fmt.Errorf("exec requires command after --"))
		}
		v, err := a.Exec(pos[0], pos[1], cmdArgs)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, fmt.Sprint(v["stdout"])+fmt.Sprint(v["stderr"]))
		if v["exitCode"].(int) != 0 {
			return v["exitCode"].(int)
		}
		return 0
	case "logs":
		pos := positionalArgs(rest)
		if len(pos) < 2 {
			return errOut(stderr, jsonOut, fmt.Errorf("logs requires <preview> <service>"))
		}
		v, err := a.Logs(pos[0], pos[1], 200)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, strings.Join(v["lines"].([]string), "\n"))
		return 0
	case "smoke":
		pos := positionalArgs(rest)
		if len(pos) < 1 {
			return errOut(stderr, jsonOut, fmt.Errorf("smoke requires <preview>"))
		}
		name := ""
		if len(pos) > 1 {
			name = pos[1]
		}
		v, err := a.Smoke(pos[0], name)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, fmt.Sprintf("smoke ok=%v", v["ok"]))
		if v["ok"] != true {
			return 1
		}
		return 0
	case "screenshot":
		pos := positionalArgs(rest)
		if len(pos) < 2 {
			return errOut(stderr, jsonOut, fmt.Errorf("screenshot requires <preview> <service> [path]"))
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
		for _, spec := range flagValues(rest, "--breakpoint") {
			bp, err := parseScreenshotBreakpoint(spec)
			if err != nil {
				return errOut(stderr, jsonOut, err)
			}
			opts.Breakpoints = append(opts.Breakpoints, bp)
		}
		v, err := a.ScreenshotWithOptions(pos[0], pos[1], opts)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, screenshotsHuman(v))
		return 0
	case "qa":
		return a.runQA(rest, stdout, stderr, jsonOut)
	case "prebuild":
		pos := positionalArgs(rest)
		if len(pos) < 1 {
			return errOut(stderr, jsonOut, fmt.Errorf("prebuild requires project"))
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
		return errOut(stderr, jsonOut, fmt.Errorf("unknown command: %s", cmd))
	}
}

func (a *App) runQA(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	if len(args) < 2 {
		return errOut(stderr, jsonOut, fmt.Errorf("usage: vivero qa <plan|context|run|record|report> <preview>"))
	}
	action := args[0]
	previewID := args[1]
	actionArgs := args[2:]
	scope, _ := flagValue(actionArgs, "--scope")
	switch action {
	case "plan", "context":
		target := artifactTargetFromArgs(actionArgs)
		v, err := a.QAPlanWithTarget(previewID, scope, target)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, v, qaPlanHuman(v))
		return 0
	case "run":
		target := artifactTargetFromArgs(actionArgs)
		v, err := a.QARunWithTarget(previewID, scope, target, !hasArg(actionArgs, "--no-screenshots"))
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
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
		output(stdout, jsonOut, v, qaRecordHuman(v))
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
		output(stdout, jsonOut, v, fmt.Sprintf("qa report: %s", v["path"]))
		return 0
	default:
		return errOut(stderr, jsonOut, fmt.Errorf("unknown qa action: %s", action))
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
	if len(args) < 2 {
		return errOut(stderr, jsonOut, fmt.Errorf("usage: vivero secrets <set|list|unset> <project>"))
	}
	action, project := args[0], args[1]
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
				return errOut(stderr, jsonOut, fmt.Errorf("secret must be KEY=value"))
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
		return errOut(stderr, jsonOut, fmt.Errorf("unknown secrets action: %s", action))
	}
}

func (a *App) runSkill(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	if len(args) == 0 {
		return errOut(stderr, jsonOut, fmt.Errorf("usage: vivero skill <install|print|path|doctor>"))
	}
	switch args[0] {
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
		return errOut(stderr, jsonOut, fmt.Errorf("unknown skill action: %s", args[0]))
	}
}

func (a *App) Doctor() (map[string]any, error) {
	checks := map[string]any{"home": a.Home, "database": filepath.Join(a.Home, "state.db")}
	for _, bin := range []string{"git", "cloudflared", "docker"} {
		_, err := os.Stat("/usr/bin/" + bin)
		if bin == "cloudflared" {
			_, err = execLook(bin)
		} else {
			_, err = execLook(bin)
		}
		checks[bin] = err == nil
	}
	projects, _ := a.listProjects()
	previews, _ := a.listPreviews()
	checks["projects"] = len(projects)
	checks["previews"] = len(previews)
	return map[string]any{"ok": true, "version": Version, "checks": checks}, nil
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
		path := expandPath(srcCfg.Path)
		if path == "" {
			path = rec.Path
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

func usage() string {
	return `vivero - local-first preview runtime

Common commands:
  vivero capabilities --json
  vivero projects sync <path> --json
  vivero up <project> --id <preview-id> --profile <name> --source app.path=/repo --wait --json --no-input
  vivero inspect <preview-id> --json
  vivero smoke <preview-id> --json
  vivero qa plan <preview-id> --json --no-input
  vivero qa run <preview-id> --scope <scope> --json --no-input
  vivero down <preview-id> --discard --json --no-input
`
}

func commandsHuman() string {
	var b strings.Builder
	for _, c := range commandCatalog() {
		b.WriteString(c["name"].(string))
		b.WriteByte('\n')
	}
	return b.String()
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
