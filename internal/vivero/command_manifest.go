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
	Command          string           `json:"name"`
	Path             []string         `json:"path"`
	Summary          string           `json:"summary"`
	Description      string           `json:"description,omitempty"`
	Usage            string           `json:"usage,omitempty"`
	Examples         []CommandExample `json:"examples,omitempty"`
	Flags            []CommandFlag    `json:"flags,omitempty"`
	Args             []CommandArg     `json:"args,omitempty"`
	Category         string           `json:"category"`
	Lane             string           `json:"lane"`
	Visibility       string           `json:"visibility"`
	JSONStability    string           `json:"jsonStability"`
	ReadsLocal       bool             `json:"readsLocal"`
	WritesLocal      bool             `json:"writesLocal"`
	ReadsRemote      bool             `json:"readsRemote"`
	WritesRemote     bool             `json:"writesRemote"`
	RequiresAuth     bool             `json:"requiresAuth"`
	RequiresNet      bool             `json:"requiresNetwork"`
	Dangerous        bool             `json:"dangerous"`
	AgentSafe        bool             `json:"agentSafe"`
	TargetRefs       []string         `json:"targetRefs,omitempty"`
	ApprovalRequired string           `json:"approvalRequired,omitempty"`
	Schema           map[string]any   `json:"schema,omitempty"`
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
	previewTargetArg := CommandArg{Name: "preview", Description: "preview id or preview:<id> target ref", Required: true}
	evidenceTargetArg := CommandArg{Name: "target", Description: "preview:<id> or release:<id> target ref", Required: true}
	global := []CommandFlag{
		{Name: "--json", Description: "write stable JSON to stdout", Global: true},
		{Name: "--no-input", Description: "fail instead of prompting", Global: true},
		{Name: "--quiet", Description: "suppress progress output", Global: true},
		{Name: "--verbose", Description: "show more progress detail", Global: true},
		{Name: "--debug", Description: "show debugging context for unexpected failures", Global: true},
		{Name: "--version", Description: "print Vivero version", Global: true},
	}
	confirmProduction := CommandFlag{Name: "--confirm-production", Description: "confirm explicit operator approval for production mutation; not implied by --no-input"}
	commands := []CommandManifest{
		manifest([]string{"capabilities"}, "print runtime capabilities", "vivero capabilities --json --no-input", true, "stable", global, nil, map[string]any{"returns": "version, build provenance, home, features, and invariants"}),
		manifest([]string{"version"}, "print Vivero version", "vivero version --json --no-input", true, "stable", global, nil, map[string]any{"returns": "version, commit, and build date"}),
		manifest([]string{"help"}, "show examples-first help", "vivero help <command>", true, "stable", []CommandFlag{{Name: "<command>", Description: "optional command or command group"}}, nil, map[string]any{"returns": "human-readable help on stdout"}),
		manifest([]string{"commands"}, "list public commands", "vivero commands --json --no-input", true, "stable", global, nil, map[string]any{"returns": "typed command manifest for public commands"}),
		manifest([]string{"schema"}, "print command schema", "vivero schema up --json --no-input", true, "stable", append(global, CommandFlag{Name: "<command>", Description: "optional command name"}), nil, map[string]any{"returns": "schema for one command or all commands"}),
		withSideEffects(manifest([]string{"init"}, "write a minimal vivero.yml", "vivero init . --name my-app --service web --port 3000 --json --no-input", true, "stable", append(global, CommandFlag{Name: "--name", ValueName: "NAME", Description: "project name; defaults to the directory name"}, CommandFlag{Name: "--service", ValueName: "NAME", Description: "preview service name", Default: "web"}, CommandFlag{Name: "--port", ValueName: "PORT", Description: "service port", Default: "3000"}, CommandFlag{Name: "--command", ValueName: "COMMAND", Description: "app-owned container command"}, CommandFlag{Name: "--build-context", ValueName: "PATH", Description: "Docker build context", Default: "."}, CommandFlag{Name: "--dockerfile", ValueName: "PATH", Description: "app-owned Dockerfile", Default: "Dockerfile"}, CommandFlag{Name: "--default-ref", ValueName: "REF", Description: "baseline source ref", Default: "main"}, CommandFlag{Name: "--health-path", ValueName: "PATH", Description: "HTTP health path", Default: "/"}, CommandFlag{Name: "--force", Description: "overwrite existing vivero.yml"}), []CommandArg{{Name: "path", Description: "project directory or config file path", Required: false}}, map[string]any{"returns": "written config path, project/service names, and next commands", "writes": "vivero.yml", "thinConfig": true}), true, false, true),
		manifest([]string{"doctor"}, "check local Vivero environment", "vivero doctor --json --no-input", true, "stable", append(global, CommandFlag{Name: "--project", ValueName: "PATH", Description: "validate project config via ConfigDoctor instead of local environment checks"}), nil, map[string]any{"returns": "environment checks plus localState diagnostics, or configDoctor when --project is passed"}),
		manifest([]string{"doctor", "config"}, "validate and lint vivero.yml", "vivero doctor config . --json --no-input", true, "stable", global, []CommandArg{{Name: "path", Description: "project directory or vivero.yml", Required: true}}, configDoctorSchema()),
		manifest([]string{"doctor", "production"}, "experimental read-only production readiness assessment", "vivero doctor production --project . --environment production --json --no-input", true, "stable", append(global, CommandFlag{Name: "--project", ValueName: "PATH", Description: "project directory or vivero.yml", Default: "."}, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "deploy environment; uses a matching config profile when present", Default: "production"}), nil, productionSchema(map[string]any{"returns": "production readiness verdict and diagnostics", "readOnly": true, "doesNotDeploy": true})),
		withSideEffects(manifest([]string{"deploy", "plan"}, "plan an experimental production deploy", "vivero deploy plan . --environment production --json --no-input", true, "stable", append(global, CommandFlag{Name: "--project", ValueName: "PATH", Description: "project directory or vivero.yml"}, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "deploy environment", Default: "production"}), []CommandArg{{Name: "path", Description: "project directory or vivero.yml", Required: true}}, productionSchema(map[string]any{"returns": "deploy plan gated by production doctor diagnostics", "doesNotDeploy": true, "strategies": []string{"command", "blue-green"}, "blueGreen": "strategy blue-green plans active and target slots before apply"})), true, false, false),
		withApproval(withRemoteEffects(withSideEffects(manifest([]string{"deploy", "apply"}, "experimentally apply an approved deploy plan", "vivero deploy apply <plan-id> --confirm-production --json --no-input", false, "stable", append(global, confirmProduction), []CommandArg{{Name: "plan-id", Description: "deploy plan id", Required: true}}, guardedProductionSchema(map[string]any{"returns": "release record", "runsAppOwnedCommand": true, "blueGreen": "runs prepare, smoke, then promote; stops before promote when smoke fails"})), true, true, true), true, true), "human"),
		withRemoteEffects(withSideEffects(manifest([]string{"release", "status"}, "inspect experimental release status", "vivero release status my-app --environment production --json --no-input", false, "stable", append(global, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "deploy environment", Default: "production"}), []CommandArg{{Name: "project", Description: "project name", Required: true}}, productionSchema(map[string]any{"returns": "current release record and status", "runsAppOwnedCommand": true, "mayUpdateLocalReleaseState": true, "blueGreen": "passes the active slot to statusCommand"})), true, true, false), true, false),
		manifest([]string{"release", "events"}, "show release audit events", "vivero release events release:<release-id> --json --no-input", true, "stable", append(global, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "deploy environment when targeting a project", Default: "production"}), []CommandArg{{Name: "target", Description: "project name or release:<id> target ref", Required: true}}, map[string]any{"returns": "release record, audit events, and targetRef", "targetRefs": []string{"release:<id>"}}),
		manifest([]string{"release", "logs"}, "show release command logs", "vivero release logs release:<release-id> --json --no-input", true, "stable", append(global, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "deploy environment when targeting a project", Default: "production"}), []CommandArg{{Name: "target", Description: "project name or release:<id> target ref", Required: true}}, map[string]any{"returns": "release output, phase output, and command-output artifacts", "targetRefs": []string{"release:<id>"}}),
		withApproval(withRemoteEffects(withSideEffects(manifest([]string{"release", "smoke"}, "run the current experimental release smoke gate", "vivero release smoke my-app --environment production --json --no-input", false, "stable", append(global, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "deploy environment", Default: "production"}), []CommandArg{{Name: "target", Description: "project name or release:<id> target ref", Required: true}}, productionSchema(map[string]any{"returns": "smoke result plus updated release evidence", "runsAppOwnedCommand": true, "targetRefs": []string{"release:<id>"}})), true, true, true), true, false), "human"),
		withApproval(withRemoteEffects(withSideEffects(manifest([]string{"release", "rollback"}, "experimentally run release rollback", "vivero release rollback my-app <release-id> --environment production --confirm-production --json --no-input", false, "stable", append(global, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "deploy environment", Default: "production"}, confirmProduction), []CommandArg{{Name: "project", Description: "project name", Required: true}, {Name: "release-id", Description: "release to roll back", Required: true}}, guardedProductionSchema(map[string]any{"returns": "rollback release record", "runsAppOwnedCommand": true, "blueGreen": "switches traffic back to the previous active slot"})), true, true, true), true, true), "human"),
		manifest([]string{"evidence", "events"}, "show events for a preview or release target", "vivero evidence events preview:my-preview --tail --json --no-input", true, "stable", append(global, CommandFlag{Name: "--tail", Description: "limit preview events to latest entries"}, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "release environment when targeting a release project", Default: "production"}), []CommandArg{evidenceTargetArg}, map[string]any{"returns": "targetRef plus preview events or release audit events", "targetRefs": []string{"preview:<id>", "release:<id>"}}),
		manifest([]string{"evidence", "logs"}, "show logs for a preview service or release", "vivero evidence logs preview:my-preview web --json --no-input", true, "stable", append(global, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "release environment when targeting a release project", Default: "production"}), []CommandArg{evidenceTargetArg, {Name: "service", Description: "preview service name; ignored for release targets"}}, map[string]any{"returns": "targetRef plus preview service logs or release command logs", "targetRefs": []string{"preview:<id>", "release:<id>"}}),
		withApproval(manifest([]string{"evidence", "smoke"}, "run smoke evidence for a preview or release", "vivero evidence smoke preview:my-preview --json --no-input", true, "stable", append(global, CommandFlag{Name: "--environment", ValueName: "NAME", Description: "release environment when targeting a release project", Default: "production"}), []CommandArg{evidenceTargetArg, {Name: "check", Description: "optional preview smoke check name"}}, map[string]any{"returns": "targetRef plus preview or release smoke result", "targetRefs": []string{"preview:<id>", "release:<id>"}}), "human-for-release-targets"),
		manifest([]string{"evidence", "screenshot"}, "capture screenshot evidence for a preview", "vivero evidence screenshot preview:my-preview web / --target local --json --no-input", true, "stable", append(global, CommandFlag{Name: "--target", ValueName: "local|public|origin", Description: "evidence target", Default: "local"}, CommandFlag{Name: "--color-scheme", ValueName: "light|dark", Description: "browser color scheme"}, CommandFlag{Name: "--storage-state", ValueName: "PATH", Description: "Playwright storage state for authenticated evidence"}, CommandFlag{Name: "--breakpoint", ValueName: "NAME=WxH", Description: "extra screenshot size"}), []CommandArg{previewTargetArg, {Name: "service", Required: true}, {Name: "path"}}, map[string]any{"returns": "screenshot artifact paths with targetRef", "targetRefs": []string{"preview:<id>"}}),
		withSideEffects(manifest([]string{"evidence", "flow"}, "run an ad-hoc browser evidence flow", "vivero evidence flow preview:my-preview --steps-file flow.json --target local --json --no-input", true, "stable", append(global, CommandFlag{Name: "--steps-file", ValueName: "PATH", Description: "JSON/YAML evidence-flow steps file"}, CommandFlag{Name: "--target", ValueName: "local|public|origin", Description: "evidence target", Default: "local"}, CommandFlag{Name: "--out", ValueName: "DIR", Description: "artifact output directory"}, CommandFlag{Name: "--video", Description: "record per-variant video"}, CommandFlag{Name: "--no-video", Description: "disable per-variant video"}, CommandFlag{Name: "--screenshots", Description: "enable named screenshot actions"}, CommandFlag{Name: "--no-screenshots", Description: "skip named screenshot actions"}, CommandFlag{Name: "--console", Description: "capture console/pageerror logs"}, CommandFlag{Name: "--no-console", Description: "skip console/pageerror logs"}, CommandFlag{Name: "--network", Description: "capture failed network requests"}, CommandFlag{Name: "--no-network", Description: "skip failed-network artifacts"}, CommandFlag{Name: "--color-scheme", ValueName: "light|dark", Description: "override all variant color schemes"}, CommandFlag{Name: "--width", ValueName: "PX", Description: "override all variant viewport widths"}, CommandFlag{Name: "--height", ValueName: "PX", Description: "override all variant viewport heights"}, CommandFlag{Name: "--device-scale-factor", ValueName: "N", Description: "override all variant device scale factors"}, CommandFlag{Name: "--storage-state", ValueName: "PATH", Description: "override all variant Playwright storage state"}, CommandFlag{Name: "--wait-ms", ValueName: "MS", Description: "wait after each action before continuing"}, CommandFlag{Name: "--slow-mo-ms", ValueName: "MS", Description: "slow Playwright actions for readable recordings"}, CommandFlag{Name: "--format", ValueName: "mp4|webm", Description: "video artifact format", Default: "mp4"}, CommandFlag{Name: "--dry-run", Description: "validate and print the execution plan without launching a browser"}, CommandFlag{Name: "--print-script", Description: "include generated Playwright script in JSON output"}), []CommandArg{previewTargetArg}, evidenceFlowSchema()), true, true, false),
		manifest([]string{"evidence", "qa"}, "run preview QA evidence commands", "vivero evidence qa run preview:my-preview --scope all --target local --json --no-input", true, "stable", append(global, CommandFlag{Name: "--scope", ValueName: "NAME|all", Description: "QA scope"}, CommandFlag{Name: "--target", ValueName: "local|public|origin", Description: "QA target", Default: "local"}, CommandFlag{Name: "--no-screenshots", Description: "skip screenshot capture for run/final"}, CommandFlag{Name: "--no-record", Description: "skip walkthrough recording for final"}), []CommandArg{{Name: "subcommand", Description: "plan|context|run|record|final|report", Required: true}, previewTargetArg}, withTargetRefs(qaSchema())),
		manifest([]string{"serve"}, "start the local HTTP control plane and public preview router", "vivero serve --public-router --addr 127.0.0.1:7777", false, "none", []CommandFlag{{Name: "--public-router", Description: "accept durable public preview host routing on this server"}, {Name: "--addr", ValueName: "HOST:PORT", Description: "listen address", Default: "127.0.0.1:7777"}}, nil, map[string]any{"sideEffects": "starts a local server"}),
		withSideEffects(manifest([]string{"public", "setup"}, "configure durable Cloudflare named-tunnel preview URLs", "vivero public setup --project . --base-domain previews.example.com --tunnel vivero-preview --json --no-input", true, "stable", append(global, CommandFlag{Name: "--project", ValueName: "PATH", Description: "project directory or vivero.yml", Default: "."}, CommandFlag{Name: "--base-domain", ValueName: "HOST", Description: "owned preview base domain, for example previews.example.com"}, CommandFlag{Name: "--tunnel", ValueName: "NAME", Description: "Cloudflare named tunnel name"}, CommandFlag{Name: "--zone", ValueName: "ZONE", Description: "Cloudflare zone name for operator reference"}, CommandFlag{Name: "--wildcard", ValueName: "HOST", Description: "wildcard hostname routed to the named tunnel", Default: "*.baseDomain"}, CommandFlag{Name: "--hostname-template", ValueName: "TEMPLATE", Description: "stable preview hostname template", Default: "{{ .PreviewID }}.{{ .BaseDomain }}"}, CommandFlag{Name: "--router-addr", ValueName: "HOST:PORT", Description: "local router address for cloudflared ingress", Default: "127.0.0.1:7777"}), nil, publicSchema(map[string]any{"returns": "updated public config plus local setup/cloudflared artifact paths", "secrets": "does not store API tokens or tunnel credentials"})), true, false, false),
		manifest([]string{"public", "doctor"}, "validate durable public preview named-tunnel setup", "vivero public doctor --project . --json --no-input", true, "stable", append(global, CommandFlag{Name: "--project", ValueName: "PATH", Description: "project directory or vivero.yml", Default: "."}), nil, publicSchema(map[string]any{"returns": "public setup diagnostics and finding list", "quickTunnel": "reported as non-durable"})),
		manifest([]string{"public", "status"}, "summarize durable public preview named-tunnel state", "vivero public status --project . --json --no-input", true, "stable", append(global, CommandFlag{Name: "--project", ValueName: "PATH", Description: "project directory or vivero.yml", Default: "."}), nil, publicSchema(map[string]any{"returns": "public setup status and finding counts"})),
		withSideEffects(manifest([]string{"public", "start"}, "start the durable public preview router and named tunnel", "vivero public start --project . --dry-run --json --no-input", true, "stable", append(global, CommandFlag{Name: "--project", ValueName: "PATH", Description: "project directory or vivero.yml", Default: "."}, CommandFlag{Name: "--dry-run", Description: "return router/cloudflared commands without starting processes"}), nil, publicSchema(map[string]any{"returns": "router and cloudflared commands plus launched process IDs when not dry-run"})), true, true, false),
		manifest([]string{"projects", "sync"}, "sync a project from vivero.yml", "vivero projects sync . --json --no-input", true, "stable", global, []CommandArg{{Name: "path", Description: "project directory or vivero.yml", Required: true}}, map[string]any{"returns": "project record"}),
		manifest([]string{"projects"}, "list synced projects", "vivero projects --json --no-input", true, "stable", global, nil, map[string]any{"returns": "project records"}),
		manifest([]string{"project", "inspect"}, "inspect one project", "vivero project inspect my-app --json --no-input", true, "stable", global, []CommandArg{{Name: "project", Description: "project name", Required: true}}, map[string]any{"returns": "project record and config"}),
		manifest([]string{"cache", "inspect"}, "inspect project cache inventory", "vivero cache inspect my-app --json --no-input", true, "stable", global, []CommandArg{{Name: "project", Description: "synced project name", Required: true}}, map[string]any{"returns": "project build cache directories, smart warm volumes, project volumes, and Vivero-tagged images", "readOnly": true}),
		withSideEffects(manifest([]string{"cache", "warm"}, "warm configured project caches", "vivero cache warm my-app --source app.ref=main --json --no-input", true, "stable", append(global, CommandFlag{Name: "--source", ValueName: "NAME.KEY=VALUE", Description: "source override such as app.ref=main"}), []CommandArg{{Name: "project", Description: "synced project name", Required: true}}, map[string]any{"returns": "cache warm actions for smart warm volumes, prebuild steps, and cache-enabled image builds", "runsAppOwnedCommand": true}), true, true, false),
		withSideEffects(manifest([]string{"cache", "prune"}, "prune project-scoped caches", "vivero cache prune my-app --kind build --yes --json --no-input", true, "stable", append(global, CommandFlag{Name: "--kind", ValueName: "build|volume|image|all", Description: "cache resources to prune"}, CommandFlag{Name: "--yes", Description: "confirm project-scoped cache deletion"}), []CommandArg{{Name: "project", Description: "synced project name", Required: true}}, map[string]any{"returns": "removed or missing project-scoped cache resources", "scope": "only configured build cache dirs, smart/project volumes, and known Vivero image tags"}), true, false, true),
		withSideEffects(manifest([]string{"up"}, "start a health-gated preview", "vivero up my-app --id my-app-local --wait --timeout 5m --json --no-input", true, "stable", append(global, CommandFlag{Name: "--id", ValueName: "PREVIEW", Description: "required preview id"}, CommandFlag{Name: "--profile", ValueName: "NAME", Description: "config profile"}, CommandFlag{Name: "--source", ValueName: "NAME.KEY=VALUE", Description: "source override"}, CommandFlag{Name: "--label", ValueName: "KEY=VALUE", Description: "preview label for callers and cleanup"}, CommandFlag{Name: "--metadata", ValueName: "KEY=VALUE", Description: "runtime metadata such as branch or ref"}, CommandFlag{Name: "--wait", Description: "wait for health before returning"}, CommandFlag{Name: "--timeout", ValueName: "DURATION", Description: "wait timeout", Default: "5m"}, CommandFlag{Name: "--public", Description: "request public tunnel/hostname"}, CommandFlag{Name: "--reuse", Description: "reuse an existing healthy preview when project, profile, sources, and services still match"}), []CommandArg{{Name: "project", Description: "synced project name", Required: true}}, map[string]any{"returns": "preview with health-gated services and URLs", "profile": "selects project.profiles; default profile applies when present", "metadata": "branch/ref metadata influences smart warm baseline selection and is persisted with the preview"}), true, true, false),
		manifest([]string{"wait"}, "wait for a preview to become healthy", "vivero wait my-preview --timeout 5m --json --no-input", true, "stable", append(global, CommandFlag{Name: "--timeout", ValueName: "DURATION", Description: "wait timeout", Default: "5m"}), []CommandArg{{Name: "preview", Description: "preview id", Required: true}}, map[string]any{"returns": "preview record"}),
		withSideEffects(manifest([]string{"down"}, "stop and clean up a preview", "vivero down my-preview --discard --json --no-input", true, "stable", append(global, CommandFlag{Name: "--discard", Description: "discard preview changes"}, CommandFlag{Name: "--archive-patch", Description: "archive a patch before cleanup"}, CommandFlag{Name: "--keep-worktree", Description: "preserve the preview worktree"}), []CommandArg{{Name: "preview", Description: "preview id", Required: true}}, map[string]any{"returns": "updated preview record"}), true, false, true),
		manifest([]string{"list"}, "list previews", "vivero list --json --no-input", true, "stable", global, nil, map[string]any{"returns": "preview records"}),
		manifest([]string{"inspect"}, "inspect one preview", "vivero inspect preview:my-preview --json --no-input", true, "stable", global, []CommandArg{previewTargetArg}, map[string]any{"returns": "preview record", "targetRefs": []string{"preview:<id>"}}),
		manifest([]string{"events"}, "show preview events", "vivero events preview:my-preview --tail --json --no-input", true, "stable", append(global, CommandFlag{Name: "--tail", Description: "limit to latest events"}), []CommandArg{previewTargetArg}, map[string]any{"returns": "event list", "targetRefs": []string{"preview:<id>"}}),
		withSideEffects(manifest([]string{"sync"}, "copy a local file into a preview source", "vivero sync my-preview app path/to/file --from ./file --json --no-input", true, "stable", append(global, CommandFlag{Name: "--from", ValueName: "PATH", Description: "local file to copy"}), []CommandArg{{Name: "preview", Required: true}, {Name: "source", Required: true}, {Name: "path", Required: true}}, map[string]any{"returns": "sync result"}), true, false, false),
		withSideEffects(manifest([]string{"rm"}, "remove a file from a preview source", "vivero rm my-preview app path/to/file --json --no-input", true, "stable", global, []CommandArg{{Name: "preview", Required: true}, {Name: "source", Required: true}, {Name: "path", Required: true}}, map[string]any{"returns": "remove result"}), true, false, true),
		manifest([]string{"diff"}, "show preview source diff", "vivero diff my-preview app --json --no-input", true, "stable", global, []CommandArg{{Name: "preview", Required: true}, {Name: "source", Required: true}}, map[string]any{"returns": "diff text and status"}),
		manifest([]string{"exec"}, "run a command in a preview service", "vivero exec preview:my-preview web --json --no-input -- npm test", true, "stable", global, []CommandArg{previewTargetArg, {Name: "service", Required: true}, {Name: "command", Required: true}}, map[string]any{"returns": "stdout, stderr, and exitCode", "targetRefs": []string{"preview:<id>"}}),
		manifest([]string{"logs"}, "show service logs", "vivero logs preview:my-preview web --json --no-input", true, "stable", global, []CommandArg{previewTargetArg, {Name: "service", Required: true}}, map[string]any{"returns": "latest log lines", "targetRefs": []string{"preview:<id>"}}),
		manifest([]string{"smoke"}, "run smoke checks", "vivero smoke preview:my-preview --json --no-input", true, "stable", global, []CommandArg{previewTargetArg, {Name: "check", Description: "optional smoke check name"}}, map[string]any{"returns": "smoke result", "targetRefs": []string{"preview:<id>"}}),
		manifest([]string{"screenshot"}, "capture screenshot evidence", "vivero screenshot preview:my-preview web / --target local --color-scheme dark --json --no-input", true, "stable", append(global, CommandFlag{Name: "--target", ValueName: "local|public|origin", Description: "evidence target", Default: "local"}, CommandFlag{Name: "--color-scheme", ValueName: "light|dark", Description: "browser color scheme"}, CommandFlag{Name: "--storage-state", ValueName: "PATH", Description: "Playwright storage state for authenticated evidence"}, CommandFlag{Name: "--breakpoint", ValueName: "NAME=WxH", Description: "extra screenshot size"}), []CommandArg{previewTargetArg, {Name: "service", Required: true}, {Name: "path"}}, map[string]any{"defaults": map[string]any{"width": 1280, "height": 800, "target": "local", "crop": false}, "projectBreakpoints": "agent.screenshotBreakpoints plus --breakpoints", "targetRefs": []string{"preview:<id>"}}),
		manifest([]string{"qa", "plan"}, "print QA plan/context", "vivero qa plan preview:my-preview --scope all --target local --json --no-input", true, "stable", append(global, CommandFlag{Name: "--scope", ValueName: "NAME|all", Description: "QA scope"}, CommandFlag{Name: "--target", ValueName: "local|public|origin", Description: "QA target", Default: "local"}), []CommandArg{previewTargetArg}, withTargetRefs(qaSchema())),
		manifest([]string{"qa", "context"}, "alias for QA plan", "vivero qa context preview:my-preview --json --no-input", true, "stable", global, []CommandArg{previewTargetArg}, withTargetRefs(qaSchema())),
		manifest([]string{"qa", "run"}, "run deterministic QA checks", "vivero qa run preview:my-preview --scope smoke --target local --json --no-input", true, "stable", append(global, CommandFlag{Name: "--scope", ValueName: "NAME|all", Description: "QA scope"}, CommandFlag{Name: "--target", ValueName: "local|public|origin", Description: "QA target", Default: "local"}, CommandFlag{Name: "--no-screenshots", Description: "skip screenshot capture"}), []CommandArg{previewTargetArg}, withTargetRefs(qaSchema())),
		manifest([]string{"qa", "record"}, "record QA walkthrough video", "vivero qa record preview:my-preview --scope smoke --color-scheme dark --json --no-input", true, "stable", append(global, CommandFlag{Name: "--scope", ValueName: "NAME|all", Description: "QA scope"}, CommandFlag{Name: "--color-scheme", ValueName: "light|dark", Description: "recording color scheme"}, CommandFlag{Name: "--storage-state", ValueName: "PATH", Description: "Playwright storage state for authenticated evidence"}), []CommandArg{previewTargetArg}, withTargetRefs(qaSchema())),
		withSideEffects(manifest([]string{"qa", "final"}, "run final QA proof and write handoff artifact", "vivero qa final preview:my-preview --scope smoke --json --no-input", true, "stable", append(global, CommandFlag{Name: "--scope", ValueName: "NAME|all", Description: "QA scope"}, CommandFlag{Name: "--target", ValueName: "local|public|origin", Description: "QA target", Default: "local"}, CommandFlag{Name: "--no-screenshots", Description: "skip screenshot capture"}, CommandFlag{Name: "--no-record", Description: "skip walkthrough video recording"}), []CommandArg{previewTargetArg}, withTargetRefs(qaSchema())), true, false, false),
		manifest([]string{"qa", "report"}, "write QA report artifact", "vivero qa report preview:my-preview --out qa/report.md --json --no-input", true, "stable", append(global, CommandFlag{Name: "--out", ValueName: "PATH", Description: "report path"}), []CommandArg{previewTargetArg}, withTargetRefs(qaSchema())),
		manifest([]string{"prebuild"}, "run app-owned prebuild steps", "vivero prebuild my-app --json --no-input", true, "stable", global, []CommandArg{{Name: "project", Required: true}}, map[string]any{"returns": "prebuild step results"}),
		withSideEffects(manifest([]string{"secrets", "set"}, "set local project secrets", "vivero secrets set my-app KEY=value --json --no-input", true, "stable", global, []CommandArg{{Name: "project", Required: true}, {Name: "KEY=value", Required: true}}, map[string]any{"secretValuesWriteOnly": true}), true, false, false),
		manifest([]string{"secrets", "list"}, "list local secret keys", "vivero secrets list my-app --json --no-input", true, "stable", global, []CommandArg{{Name: "project", Required: true}}, map[string]any{"returns": "keys only"}),
		withSideEffects(manifest([]string{"secrets", "unset"}, "remove local secret keys", "vivero secrets unset my-app KEY --json --no-input", true, "stable", global, []CommandArg{{Name: "project", Required: true}, {Name: "KEY", Required: true}}, map[string]any{"returns": "remaining keys"}), true, false, true),
		manifest([]string{"skill", "install"}, "install bundled Vivero skill", "vivero skill install --json --no-input", true, "stable", append(global, CommandFlag{Name: "--target", ValueName: "PATH", Description: "skill target"}, CommandFlag{Name: "--force", Description: "overwrite existing skill"}), nil, map[string]any{"returns": "install result"}),
		manifest([]string{"skill", "print"}, "print bundled Vivero skill", "vivero skill print --json --no-input", true, "stable", global, nil, map[string]any{"returns": "skill content"}),
		manifest([]string{"skill", "path"}, "show bundled skill paths", "vivero skill path --json --no-input", true, "stable", global, nil, map[string]any{"returns": "default skill targets"}),
		manifest([]string{"skill", "doctor"}, "check bundled skill installation", "vivero skill doctor --json --no-input", true, "stable", global, nil, map[string]any{"returns": "skill doctor result"}),
		manifest([]string{"diagnose", "startup"}, "diagnose startup timing from preview events", "vivero diagnose startup preview:my-preview --json --no-input", true, "experimental", global, []CommandArg{previewTargetArg}, map[string]any{"returns": "startup timing diagnosis once diagnostics are enabled", "targetRefs": []string{"preview:<id>"}}),
	}
	return append(commands, previewNamespaceAliases(commands)...)
}

func previewNamespaceAliases(commands []CommandManifest) []CommandManifest {
	aliasNames := map[string]bool{
		"up":               true,
		"wait":             true,
		"down":             true,
		"list":             true,
		"inspect":          true,
		"events":           true,
		"sync":             true,
		"rm":               true,
		"diff":             true,
		"exec":             true,
		"logs":             true,
		"smoke":            true,
		"screenshot":       true,
		"qa plan":          true,
		"qa context":       true,
		"qa run":           true,
		"qa record":        true,
		"qa final":         true,
		"qa report":        true,
		"diagnose startup": true,
	}
	aliases := []CommandManifest{}
	for _, cmd := range commands {
		if !aliasNames[cmd.Name()] {
			continue
		}
		alias := cmd
		alias.Path = append([]string{"preview"}, cmd.Path...)
		alias.Command = strings.Join(alias.Path, " ")
		alias.Usage = previewAliasUsage(cmd.Usage)
		alias.Examples = previewAliasExamples(cmd.Examples)
		aliases = append(aliases, alias)
	}
	return aliases
}

func previewAliasUsage(usage string) string {
	if strings.HasPrefix(usage, "vivero ") {
		return "vivero preview " + strings.TrimPrefix(usage, "vivero ")
	}
	return usage
}

func previewAliasExamples(examples []CommandExample) []CommandExample {
	if len(examples) == 0 {
		return nil
	}
	out := make([]CommandExample, 0, len(examples))
	for _, ex := range examples {
		copyEx := ex
		if len(ex.Command) > 1 && ex.Command[0] == "vivero" {
			copyEx.Command = append([]string{"vivero", "preview"}, ex.Command[1:]...)
		} else {
			copyEx.Command = append([]string{}, ex.Command...)
		}
		out = append(out, copyEx)
	}
	return out
}

func productionSchema(schema map[string]any) map[string]any {
	out := map[string]any{"featureStatus": "experimental", "featureLane": "production"}
	for k, v := range schema {
		out[k] = v
	}
	return out
}

func publicSchema(schema map[string]any) map[string]any {
	out := map[string]any{"featureStatus": "experimental", "featureLane": "public-preview", "provider": "cloudflare", "mode": "named-tunnel"}
	for k, v := range schema {
		out[k] = v
	}
	return out
}

func guardedProductionSchema(schema map[string]any) map[string]any {
	out := productionSchema(schema)
	out["requiresConfirmProduction"] = true
	return out
}

func manifest(path []string, summary, usage string, agentSafe bool, stability string, flags []CommandFlag, args []CommandArg, schema map[string]any) CommandManifest {
	visibility := commandVisibility(path)
	if !validCommandVisibility(visibility) {
		visibility = CommandVisibilityAdvanced
	}
	lane := commandLane(path)
	if !validCommandLane(lane) {
		lane = CommandLaneSupport
	}
	m := CommandManifest{Command: strings.Join(path, " "), Path: path, Summary: summary, Description: summary, Usage: usage, Examples: []CommandExample{{Description: summary, Command: strings.Fields(usage)}}, Flags: flags, Args: args, Category: commandCategory(path), Lane: lane, Visibility: visibility, JSONStability: stability, ReadsLocal: true, AgentSafe: agentSafe, TargetRefs: commandTargetRefs(schema), Schema: schema}
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
	case "projects", "project", "init":
		return "projects"
	case "cache":
		return "cache"
	case "public":
		return "public"
	case "up", "wait", "down", "list", "inspect", "events", "logs", "smoke":
		return "runtime"
	case "sync", "rm", "diff", "exec":
		return "source"
	case "screenshot", "qa", "evidence":
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
	case "projects", "project", "init":
		return CommandLaneProject
	case "deploy", "release":
		return CommandLaneDeploy
	case "doctor":
		if name == "doctor production" {
			return CommandLaneDeploy
		}
		return CommandLaneSupport
	case "events", "logs", "smoke", "screenshot", "qa", "evidence", "diagnose":
		return CommandLaneEvidence
	case "public", "up", "wait", "down", "list", "inspect", "sync", "rm", "diff", "exec":
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

func withRemoteEffects(m CommandManifest, readsRemote, writesRemote bool) CommandManifest {
	m.ReadsRemote = readsRemote
	m.WritesRemote = writesRemote
	if readsRemote || writesRemote {
		m.RequiresNet = true
	}
	return m
}

func withApproval(m CommandManifest, approvalRequired string) CommandManifest {
	m.ApprovalRequired = approvalRequired
	return m
}

func commandTargetRefs(schema map[string]any) []string {
	if schema == nil {
		return nil
	}
	raw, ok := schema["targetRefs"]
	if !ok {
		return nil
	}
	refs, ok := raw.([]string)
	if !ok || len(refs) == 0 {
		return nil
	}
	return append([]string{}, refs...)
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
	if len(cmd.TargetRefs) > 0 {
		body["targetRefs"] = cmd.TargetRefs
	}
	if cmd.ApprovalRequired != "" {
		body["approvalRequired"] = cmd.ApprovalRequired
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

func evidenceFlowSchema() map[string]any {
	return map[string]any{
		"usage":      "vivero evidence flow preview:<id> --steps-file <flow.json|flow.yaml> [--target local|public|origin] [--video] [--dry-run] --json --no-input",
		"returns":    "stable JSON with preview target, execution plan, per-variant artifact paths, screenshots, videos, console logs, optional network failures, resultPath, and reportPath",
		"targetRefs": []string{"preview:<id>"},
		"defaults":   map[string]any{"target": "local", "format": "mp4", "width": 1280, "height": 800, "deviceScaleFactor": 1, "screenshots": true, "console": true, "video": false, "network": false},
		"stepsFile": map[string]any{
			"format":         "JSON or YAML",
			"topLevelFields": []string{"name", "description", "start", "variants", "record", "actions"},
			"start":          "string path/common-page name/absolute URL, or object with service+path/url/page",
			"actions":        []string{"visit/goto endpoint", "click locator", "fill locator+value", "press key", "waitForSelector", "expectText", "expectUrl", "screenshot"},
			"variants":       "first-class viewport, colorScheme, deviceScaleFactor, isMobile, storageState definitions",
		},
		"dryRun":      "validates target refs, step file, URL/service/path resolution, variants, and artifact directories without launching a browser or writing artifacts",
		"printScript": "includes the generated Playwright script in JSON output for inspection",
		"appAgnostic": true,
	}
}

func qaSchema() map[string]any {
	return map[string]any{
		"usage":       "vivero preview qa <plan|context|run|final|report> <preview-id|preview:<id>> [--scope <name|all>] [--public|--target local|public|origin] --json --no-input; vivero preview qa record <preview-id|preview:<id>> [--scope <name|all>] [--storage-state <path>] --json --no-input",
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

func withTargetRefs(schema map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range schema {
		out[k] = v
	}
	out["targetRefs"] = []string{"preview:<id>"}
	return out
}
