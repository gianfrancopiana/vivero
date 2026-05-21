package vivero

import "strings"

const (
	CommandVisibilityCommon   = "common"
	CommandVisibilityAdvanced = "advanced"
	CommandVisibilityInternal = "internal"
)

const (
	CommandLaneDiscovery = "discovery"
	CommandLaneProject   = "project"
	CommandLanePreview   = "preview"
	CommandLaneDeploy    = "deploy"
	CommandLaneEvidence  = "evidence"
	CommandLaneSupport   = "support"
)

type CommandManifest struct {
	Command       string           `json:"name"`
	Path          []string         `json:"path"`
	Summary       string           `json:"summary"`
	Description   string           `json:"description,omitempty"`
	Usage         string           `json:"usage,omitempty"`
	Examples      []CommandExample `json:"examples,omitempty"`
	Flags         []CommandFlag    `json:"flags,omitempty"`
	Args          []CommandArg     `json:"args,omitempty"`
	Category      string           `json:"category"`
	Lane          string           `json:"lane"`
	Visibility    string           `json:"visibility"`
	JSONStability string           `json:"jsonStability"`
	ReadsLocal    bool             `json:"readsLocal,omitempty"`
	WritesLocal   bool             `json:"writesLocal,omitempty"`
	ReadsRemote   bool             `json:"readsRemote,omitempty"`
	WritesRemote  bool             `json:"writesRemote,omitempty"`
	RequiresAuth  bool             `json:"requiresAuth,omitempty"`
	RequiresNet   bool             `json:"requiresNetwork,omitempty"`
	Dangerous     bool             `json:"dangerous,omitempty"`
	AgentSafe     bool             `json:"agentSafe"`
	Schema        map[string]any   `json:"schema,omitempty"`
}

type CommandExample struct {
	Description string   `json:"description"`
	Command     []string `json:"command"`
}

type CommandFlag struct {
	Name        string `json:"name"`
	ValueName   string `json:"valueName,omitempty"`
	Description string `json:"description"`
	Default     string `json:"default,omitempty"`
	Global      bool   `json:"global,omitempty"`
}

type CommandArg struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required,omitempty"`
}

func (c CommandManifest) Name() string {
	if c.Command != "" {
		return c.Command
	}
	return strings.Join(c.Path, " ")
}

func commandManifests() []CommandManifest {
	global := []CommandFlag{
		{Name: "--json", Description: "write stable JSON to stdout", Global: true},
		{Name: "--no-input", Description: "fail instead of prompting", Global: true},
		{Name: "--quiet", Description: "suppress progress output", Global: true},
		{Name: "--verbose", Description: "show more progress detail", Global: true},
		{Name: "--debug", Description: "show debugging context for unexpected failures", Global: true},
		{Name: "--version", Description: "print Vivero version", Global: true},
	}
	return []CommandManifest{
		manifest([]string{"capabilities"}, "print runtime capabilities", "vivero capabilities --json --no-input", true, "stable", global, nil, map[string]any{"returns": "version, build provenance, home, features, and invariants"}),
		manifest([]string{"version"}, "print Vivero version", "vivero version --json --no-input", true, "stable", global, nil, map[string]any{"returns": "version, commit, and build date"}),
		manifest([]string{"help"}, "show examples-first help", "vivero help <command>", true, "stable", []CommandFlag{{Name: "<command>", Description: "optional command or command group"}}, nil, map[string]any{"returns": "human-readable help on stdout"}),
		manifest([]string{"commands"}, "list public commands", "vivero commands --json --no-input", true, "stable", global, nil, map[string]any{"returns": "typed command manifest for public commands"}),
		manifest([]string{"schema"}, "print command schema", "vivero schema up --json --no-input", true, "stable", append(global, CommandFlag{Name: "<command>", Description: "optional command name"}), nil, map[string]any{"returns": "schema for one command or all commands"}),
		manifest([]string{"doctor"}, "check local Vivero environment", "vivero doctor --json --no-input", true, "stable", append(global, CommandFlag{Name: "--project", ValueName: "PATH", Description: "validate project config via ConfigDoctor instead of local environment checks"}), nil, map[string]any{"returns": "environment checks plus localState diagnostics, or configDoctor when --project is passed"}),
		manifest([]string{"doctor", "config"}, "validate and lint vivero.yml", "vivero doctor config . --json --no-input", true, "stable", global, []CommandArg{{Name: "path", Description: "project directory or vivero.yml", Required: true}}, configDoctorSchema()),
		manifest([]string{"doctor", "production"}, "read-only production readiness assessment", "vivero doctor production --project . --json --no-input", true, "stable", append(global, CommandFlag{Name: "--project", ValueName: "PATH", Description: "project directory or vivero.yml", Default: "."}), nil, map[string]any{"returns": "production readiness verdict and diagnostics", "readOnly": true, "doesNotDeploy": true}),
		withSideEffects(manifest([]string{"deploy", "plan"}, "plan a production deploy", "vivero deploy plan . --environment production --json --no-input", true, "stable", append(global, CommandFlag{Name: "--project", ValueName: "PATH", Description: "project directory or vivero.yml"}, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "deploy environment", Default: "production"}), []CommandArg{{Name: "path", Description: "project directory or vivero.yml", Required: true}}, map[string]any{"returns": "deploy plan gated by production doctor diagnostics", "doesNotDeploy": true, "strategies": []string{"command", "blue-green"}, "blueGreen": "strategy blue-green plans active and target slots before apply"}), true, false, false),
		withSideEffects(manifest([]string{"deploy", "apply"}, "apply an approved deploy plan", "vivero deploy apply <plan-id> --json --no-input", false, "stable", global, []CommandArg{{Name: "plan-id", Description: "deploy plan id", Required: true}}, map[string]any{"returns": "release record", "runsAppOwnedCommand": true, "blueGreen": "runs prepare, smoke, then promote; stops before promote when smoke fails"}), true, true, true),
		withSideEffects(manifest([]string{"release", "status"}, "inspect current release status", "vivero release status my-app --environment production --json --no-input", false, "stable", append(global, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "deploy environment", Default: "production"}), []CommandArg{{Name: "project", Description: "project name", Required: true}}, map[string]any{"returns": "current release record and status", "runsAppOwnedCommand": true, "mayUpdateLocalReleaseState": true, "blueGreen": "passes the active slot to statusCommand"}), true, true, false),
		withSideEffects(manifest([]string{"release", "rollback"}, "run release rollback", "vivero release rollback my-app <release-id> --environment production --json --no-input", false, "stable", append(global, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "deploy environment", Default: "production"}), []CommandArg{{Name: "project", Description: "project name", Required: true}, {Name: "release-id", Description: "release to roll back", Required: true}}, map[string]any{"returns": "rollback release record", "runsAppOwnedCommand": true, "blueGreen": "switches traffic back to the previous active slot"}), true, true, true),
		manifest([]string{"serve"}, "start the local HTTP control plane", "vivero serve --addr 127.0.0.1:7777", false, "none", []CommandFlag{{Name: "--addr", ValueName: "HOST:PORT", Description: "listen address", Default: "127.0.0.1:7777"}}, nil, map[string]any{"sideEffects": "starts a local server"}),
		manifest([]string{"projects", "sync"}, "sync a project from vivero.yml", "vivero projects sync . --json --no-input", true, "stable", global, []CommandArg{{Name: "path", Description: "project directory or vivero.yml", Required: true}}, map[string]any{"returns": "project record"}),
		manifest([]string{"projects"}, "list synced projects", "vivero projects --json --no-input", true, "stable", global, nil, map[string]any{"returns": "project records"}),
		manifest([]string{"project", "inspect"}, "inspect one project", "vivero project inspect my-app --json --no-input", true, "stable", global, []CommandArg{{Name: "project", Description: "project name", Required: true}}, map[string]any{"returns": "project record and config"}),
		withSideEffects(manifest([]string{"up"}, "start a health-gated preview", "vivero up my-app --id my-app-local --wait --timeout 5m --json --no-input", true, "stable", append(global, CommandFlag{Name: "--id", ValueName: "PREVIEW", Description: "required preview id"}, CommandFlag{Name: "--profile", ValueName: "NAME", Description: "config profile"}, CommandFlag{Name: "--source", ValueName: "NAME.KEY=VALUE", Description: "source override"}, CommandFlag{Name: "--label", ValueName: "KEY=VALUE", Description: "preview label for callers and cleanup"}, CommandFlag{Name: "--metadata", ValueName: "KEY=VALUE", Description: "runtime metadata such as branch or ref"}, CommandFlag{Name: "--wait", Description: "wait for health before returning"}, CommandFlag{Name: "--timeout", ValueName: "DURATION", Description: "wait timeout", Default: "5m"}, CommandFlag{Name: "--public", Description: "request public tunnel/hostname"}), []CommandArg{{Name: "project", Description: "synced project name", Required: true}}, map[string]any{"returns": "preview with health-gated services and URLs", "profile": "selects project.profiles; default profile applies when present", "metadata": "branch/ref metadata influences smart warm baseline selection and is persisted with the preview"}), true, true, false),
		manifest([]string{"wait"}, "wait for a preview to become healthy", "vivero wait my-preview --timeout 5m --json --no-input", true, "stable", append(global, CommandFlag{Name: "--timeout", ValueName: "DURATION", Description: "wait timeout", Default: "5m"}), []CommandArg{{Name: "preview", Description: "preview id", Required: true}}, map[string]any{"returns": "preview record"}),
		withSideEffects(manifest([]string{"down"}, "stop and clean up a preview", "vivero down my-preview --discard --json --no-input", true, "stable", append(global, CommandFlag{Name: "--discard", Description: "discard preview changes"}, CommandFlag{Name: "--archive-patch", Description: "archive a patch before cleanup"}, CommandFlag{Name: "--keep-worktree", Description: "preserve the preview worktree"}), []CommandArg{{Name: "preview", Description: "preview id", Required: true}}, map[string]any{"returns": "updated preview record"}), true, false, true),
		manifest([]string{"list"}, "list previews", "vivero list --json --no-input", true, "stable", global, nil, map[string]any{"returns": "preview records"}),
		manifest([]string{"inspect"}, "inspect one preview", "vivero inspect my-preview --json --no-input", true, "stable", global, []CommandArg{{Name: "preview", Description: "preview id", Required: true}}, map[string]any{"returns": "preview record"}),
		manifest([]string{"events"}, "show preview events", "vivero events my-preview --tail --json --no-input", true, "stable", append(global, CommandFlag{Name: "--tail", Description: "limit to latest events"}), []CommandArg{{Name: "preview", Description: "preview id", Required: true}}, map[string]any{"returns": "event list"}),
		withSideEffects(manifest([]string{"sync"}, "copy a local file into a preview source", "vivero sync my-preview app path/to/file --from ./file --json --no-input", true, "stable", append(global, CommandFlag{Name: "--from", ValueName: "PATH", Description: "local file to copy"}), []CommandArg{{Name: "preview", Required: true}, {Name: "source", Required: true}, {Name: "path", Required: true}}, map[string]any{"returns": "sync result"}), true, false, false),
		withSideEffects(manifest([]string{"rm"}, "remove a file from a preview source", "vivero rm my-preview app path/to/file --json --no-input", true, "stable", global, []CommandArg{{Name: "preview", Required: true}, {Name: "source", Required: true}, {Name: "path", Required: true}}, map[string]any{"returns": "remove result"}), true, false, true),
		manifest([]string{"diff"}, "show preview source diff", "vivero diff my-preview app --json --no-input", true, "stable", global, []CommandArg{{Name: "preview", Required: true}, {Name: "source", Required: true}}, map[string]any{"returns": "diff text and status"}),
		manifest([]string{"exec"}, "run a command in a preview service", "vivero exec my-preview web --json --no-input -- npm test", true, "stable", global, []CommandArg{{Name: "preview", Required: true}, {Name: "service", Required: true}, {Name: "command", Required: true}}, map[string]any{"returns": "stdout, stderr, and exitCode"}),
		manifest([]string{"logs"}, "show service logs", "vivero logs my-preview web --json --no-input", true, "stable", global, []CommandArg{{Name: "preview", Required: true}, {Name: "service", Required: true}}, map[string]any{"returns": "latest log lines"}),
		manifest([]string{"smoke"}, "run smoke checks", "vivero smoke my-preview --json --no-input", true, "stable", global, []CommandArg{{Name: "preview", Required: true}, {Name: "check", Description: "optional smoke check name"}}, map[string]any{"returns": "smoke result"}),
		manifest([]string{"screenshot"}, "capture screenshot evidence", "vivero screenshot my-preview web / --target local --color-scheme dark --json --no-input", true, "stable", append(global, CommandFlag{Name: "--target", ValueName: "local|public|origin", Description: "evidence target", Default: "local"}, CommandFlag{Name: "--color-scheme", ValueName: "light|dark", Description: "browser color scheme"}, CommandFlag{Name: "--storage-state", ValueName: "PATH", Description: "Playwright storage state for authenticated evidence"}, CommandFlag{Name: "--breakpoint", ValueName: "NAME=WxH", Description: "extra screenshot size"}), []CommandArg{{Name: "preview", Required: true}, {Name: "service", Required: true}, {Name: "path"}}, map[string]any{"defaults": map[string]any{"width": 1280, "height": 800, "target": "local", "crop": false}, "projectBreakpoints": "agent.screenshotBreakpoints plus --breakpoints"}),
		manifest([]string{"qa", "plan"}, "print QA plan/context", "vivero qa plan my-preview --scope all --target local --json --no-input", true, "stable", append(global, CommandFlag{Name: "--scope", ValueName: "NAME|all", Description: "QA scope"}, CommandFlag{Name: "--target", ValueName: "local|public|origin", Description: "QA target", Default: "local"}), []CommandArg{{Name: "preview", Required: true}}, qaSchema()),
		manifest([]string{"qa", "context"}, "alias for QA plan", "vivero qa context my-preview --json --no-input", true, "stable", global, []CommandArg{{Name: "preview", Required: true}}, qaSchema()),
		manifest([]string{"qa", "run"}, "run deterministic QA checks", "vivero qa run my-preview --scope smoke --target local --json --no-input", true, "stable", append(global, CommandFlag{Name: "--scope", ValueName: "NAME|all", Description: "QA scope"}, CommandFlag{Name: "--target", ValueName: "local|public|origin", Description: "QA target", Default: "local"}, CommandFlag{Name: "--no-screenshots", Description: "skip screenshot capture"}), []CommandArg{{Name: "preview", Required: true}}, qaSchema()),
		manifest([]string{"qa", "record"}, "record QA walkthrough video", "vivero qa record my-preview --scope smoke --color-scheme dark --json --no-input", true, "stable", append(global, CommandFlag{Name: "--scope", ValueName: "NAME|all", Description: "QA scope"}, CommandFlag{Name: "--color-scheme", ValueName: "light|dark", Description: "recording color scheme"}, CommandFlag{Name: "--storage-state", ValueName: "PATH", Description: "Playwright storage state for authenticated evidence"}), []CommandArg{{Name: "preview", Required: true}}, qaSchema()),
		withSideEffects(manifest([]string{"qa", "final"}, "run final QA proof and write handoff artifact", "vivero qa final my-preview --scope smoke --json --no-input", true, "stable", append(global, CommandFlag{Name: "--scope", ValueName: "NAME|all", Description: "QA scope"}, CommandFlag{Name: "--target", ValueName: "local|public|origin", Description: "QA target", Default: "local"}, CommandFlag{Name: "--no-screenshots", Description: "skip screenshot capture"}, CommandFlag{Name: "--no-record", Description: "skip walkthrough video recording"}), []CommandArg{{Name: "preview", Required: true}}, qaSchema()), true, false, false),
		manifest([]string{"qa", "report"}, "write QA report artifact", "vivero qa report my-preview --out qa/report.md --json --no-input", true, "stable", append(global, CommandFlag{Name: "--out", ValueName: "PATH", Description: "report path"}), []CommandArg{{Name: "preview", Required: true}}, qaSchema()),
		manifest([]string{"prebuild"}, "run app-owned prebuild steps", "vivero prebuild my-app --json --no-input", true, "stable", global, []CommandArg{{Name: "project", Required: true}}, map[string]any{"returns": "prebuild step results"}),
		withSideEffects(manifest([]string{"secrets", "set"}, "set local project secrets", "vivero secrets set my-app KEY=value --json --no-input", true, "stable", global, []CommandArg{{Name: "project", Required: true}, {Name: "KEY=value", Required: true}}, map[string]any{"secretValuesWriteOnly": true}), true, false, false),
		manifest([]string{"secrets", "list"}, "list local secret keys", "vivero secrets list my-app --json --no-input", true, "stable", global, []CommandArg{{Name: "project", Required: true}}, map[string]any{"returns": "keys only"}),
		withSideEffects(manifest([]string{"secrets", "unset"}, "remove local secret keys", "vivero secrets unset my-app KEY --json --no-input", true, "stable", global, []CommandArg{{Name: "project", Required: true}, {Name: "KEY", Required: true}}, map[string]any{"returns": "remaining keys"}), true, false, true),
		manifest([]string{"skill", "install"}, "install bundled Vivero skill", "vivero skill install --json --no-input", true, "stable", append(global, CommandFlag{Name: "--target", ValueName: "PATH", Description: "skill target"}, CommandFlag{Name: "--force", Description: "overwrite existing skill"}), nil, map[string]any{"returns": "install result"}),
		manifest([]string{"skill", "print"}, "print bundled Vivero skill", "vivero skill print --json --no-input", true, "stable", global, nil, map[string]any{"returns": "skill content"}),
		manifest([]string{"skill", "path"}, "show bundled skill paths", "vivero skill path --json --no-input", true, "stable", global, nil, map[string]any{"returns": "default skill targets"}),
		manifest([]string{"skill", "doctor"}, "check bundled skill installation", "vivero skill doctor --json --no-input", true, "stable", global, nil, map[string]any{"returns": "skill doctor result"}),
		manifest([]string{"diagnose", "startup"}, "diagnose startup timing from preview events", "vivero diagnose startup my-preview --json --no-input", true, "experimental", global, []CommandArg{{Name: "preview", Required: true}}, map[string]any{"returns": "startup timing diagnosis once diagnostics are enabled"}),
	}
}

func manifest(path []string, summary, usage string, agentSafe bool, stability string, flags []CommandFlag, args []CommandArg, schema map[string]any) CommandManifest {
	visibility := commandVisibility(path)
	if !validCommandVisibility(visibility) {
		visibility = CommandVisibilityAdvanced
	}
	m := CommandManifest{Command: strings.Join(path, " "), Path: path, Summary: summary, Description: summary, Usage: usage, Examples: []CommandExample{{Description: summary, Command: strings.Fields(usage)}}, Flags: flags, Args: args, Category: commandCategory(path), Lane: commandLane(path), Visibility: visibility, JSONStability: stability, ReadsLocal: true, AgentSafe: agentSafe, Schema: schema}
	return m
}

func commandVisibility(path []string) string {
	name := strings.Join(path, " ")
	switch name {
	case "deploy plan", "deploy apply", "release status", "release rollback", "sync", "rm", "diff", "exec", "logs", "screenshot", "qa context", "qa record", "qa report", "prebuild", "secrets set", "secrets list", "secrets unset", "skill install", "skill print", "skill path", "skill doctor", "diagnose startup":
		return CommandVisibilityAdvanced
	case "serve":
		return CommandVisibilityInternal
	default:
		return CommandVisibilityCommon
	}
}

func commandCategory(path []string) string {
	if len(path) == 0 {
		return "unknown"
	}
	switch path[0] {
	case "capabilities", "version", "help", "commands", "schema":
		return "discovery"
	case "doctor":
		return "diagnostics"
	case "deploy", "release":
		return "release"
	case "projects", "project":
		return "projects"
	case "up", "wait", "down", "list", "inspect", "events", "logs", "smoke":
		return "runtime"
	case "sync", "rm", "diff", "exec":
		return "source"
	case "screenshot", "qa":
		return "qa"
	case "prebuild":
		return "build"
	case "secrets":
		return "secrets"
	case "skill":
		return "skills"
	case "diagnose":
		return "diagnostics"
	case "serve":
		return "control-plane"
	default:
		return "other"
	}
}

func commandLane(path []string) string {
	if len(path) == 0 {
		return CommandLaneSupport
	}
	name := strings.Join(path, " ")
	switch path[0] {
	case "capabilities", "version", "help", "commands", "schema":
		return CommandLaneDiscovery
	case "projects", "project":
		return CommandLaneProject
	case "deploy", "release":
		return CommandLaneDeploy
	case "doctor":
		if name == "doctor production" {
			return CommandLaneDeploy
		}
		return CommandLaneSupport
	case "events", "logs", "smoke", "screenshot", "qa", "diagnose":
		return CommandLaneEvidence
	case "up", "wait", "down", "list", "inspect", "sync", "rm", "diff", "exec":
		return CommandLanePreview
	default:
		return CommandLaneSupport
	}
}

func validCommandLane(lane string) bool {
	switch lane {
	case CommandLaneDiscovery, CommandLaneProject, CommandLanePreview, CommandLaneDeploy, CommandLaneEvidence, CommandLaneSupport:
		return true
	default:
		return false
	}
}

func validCommandVisibility(visibility string) bool {
	switch visibility {
	case CommandVisibilityCommon, CommandVisibilityAdvanced, CommandVisibilityInternal:
		return true
	default:
		return false
	}
}

func withSideEffects(m CommandManifest, writesLocal, requiresNet, dangerous bool) CommandManifest {
	m.WritesLocal = writesLocal
	m.RequiresNet = requiresNet
	m.Dangerous = dangerous
	return m
}

func commandCatalog() []CommandManifest {
	return commandManifests()
}

func schemaFor(command string) map[string]any {
	manifests := commandManifests()
	if command != "" {
		for _, cmd := range manifests {
			if cmd.Name() == command || strings.Join(cmd.Path, " ") == command {
				return map[string]any{"command": cmd.Name(), "schema": schemaBody(cmd)}
			}
		}
		for _, cmd := range manifests {
			if len(cmd.Path) > 1 && cmd.Path[0] == command {
				return map[string]any{"command": cmd.Name(), "schema": schemaBody(cmd)}
			}
		}
		return map[string]any{"command": command, "schema": map[string]any{"usage": "see vivero commands --json", "jsonStability": "none", "unknown": true}}
	}
	out := map[string]any{}
	for _, cmd := range manifests {
		out[cmd.Name()] = schemaBody(cmd)
	}
	return map[string]any{"version": 1, "commands": out}
}

func schemaBody(cmd CommandManifest) map[string]any {
	body := map[string]any{
		"usage":           cmd.Usage,
		"category":        cmd.Category,
		"lane":            cmd.Lane,
		"visibility":      cmd.Visibility,
		"jsonStability":   cmd.JSONStability,
		"agentSafe":       cmd.AgentSafe,
		"readsLocal":      cmd.ReadsLocal,
		"writesLocal":     cmd.WritesLocal,
		"readsRemote":     cmd.ReadsRemote,
		"writesRemote":    cmd.WritesRemote,
		"requiresAuth":    cmd.RequiresAuth,
		"requiresNetwork": cmd.RequiresNet,
		"dangerous":       cmd.Dangerous,
		"flags":           cmd.Flags,
		"args":            cmd.Args,
	}
	for k, v := range cmd.Schema {
		body[k] = v
	}
	return body
}

func configDoctorSchema() map[string]any {
	return map[string]any{
		"returns":       "config validation findings with schema, deprecation, and cross-reference checks",
		"findingFields": []string{"severity", "code", "path", "line", "column", "message", "suggestion", "docs"},
		"findingCodes": []string{
			"config-load",
			"unsupported-config-key",
			"unknown-config-key",
			"unknown-source",
			"unknown-service",
			"unknown-page",
			"unknown-qa-scope",
			"unknown-qa-auth-session",
			"setup-policy-invalid",
			"setup-persistent-volume-missing",
			"setup-fingerprint-paths-missing",
			"setup-fingerprint-path-missing",
		},
		"unknownKeys": "reported as warnings because YAML decoding ignores them; unsupported/retired keys are errors",
	}
}

func qaSchema() map[string]any {
	return map[string]any{
		"usage":       "vivero qa <plan|context|run|final|report> <preview-id> [--scope <name|all>] [--public|--target local|public|origin] --json --no-input; vivero qa record <preview-id> [--scope <name|all>] [--storage-state <path>] --json --no-input",
		"defaults":    map[string]any{"target": "local", "recordFormat": "mp4", "width": 1280, "height": 800},
		"planReturns": "driver-agnostic QA context with local-by-default services, pages, flows, checks, authenticated storage-state context, artifact paths, and concrete evidence commands derived from agent.qa.evidence",
		"run":         "runs deterministic smoke checks, captures declared page screenshots from the YAML-backed evidence matrix unless --no-screenshots is passed, and writes a report scaffold",
		"record":      "records declared QA flows through the local/proxy preview URL; use the qa plan evidence.recordings commands for configured color schemes",
		"final":       "runs qa run plus a walkthrough recording, startup diagnosis, and writes final.json with URL, smoke status, screenshots, videos, report path, and artifact paths",
		"recordOptions": map[string]any{
			"colorScheme":  "optional light|dark primitive used by generated evidence.recordings.commands",
			"storageState": "optional Playwright storage state path; normally generated by agent.qa.auth.sessions in qa plan evidence.recordings.commands",
		},
		"config": "agent.qa, agent.qa.auth, and agent.qa.evidence in vivero.yml",
	}
}
