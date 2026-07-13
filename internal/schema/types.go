package schema

import "time"

type ProjectConfig struct {
	Project         ProjectMeta               `yaml:"project" json:"project"`
	Sources         map[string]SourceConfig   `yaml:"sources" json:"sources,omitempty"`
	Services        map[string]ServiceConfig  `yaml:"services" json:"services,omitempty"`
	BackingServices map[string]BackingConfig  `yaml:"backingServices" json:"backingServices,omitempty"`
	Seeds           map[string]SeedConfig     `yaml:"seeds" json:"seeds,omitempty"`
	Prebuild        map[string]PrebuildConfig `yaml:"prebuild" json:"prebuild,omitempty"`
	Public          PublicConfig              `yaml:"public" json:"public,omitempty"`
	Warm            WarmConfig                `yaml:"warm" json:"warm,omitempty"`
	Setup           SetupConfig               `yaml:"setup" json:"setup,omitempty"`
	Routes          map[string]string         `yaml:"routes" json:"routes,omitempty"`
	Agent           AgentConfig               `yaml:"agent" json:"agent,omitempty"`
	Profiles        map[string]ProfileConfig  `yaml:"profiles" json:"profiles,omitempty"`
	Resources       ResourceConfig            `yaml:"resources" json:"resources,omitempty"`
}

type ProjectMeta struct {
	Name string `yaml:"name" json:"name"`
}

type SourceConfig struct {
	Repo       string `yaml:"repo" json:"repo,omitempty"`
	DefaultRef string `yaml:"defaultRef" json:"defaultRef,omitempty"`
	Path       string `yaml:"path" json:"path,omitempty"`
	Mode       string `yaml:"mode" json:"mode,omitempty"`
}

type ServiceConfig struct {
	Source            string                `yaml:"source" json:"source,omitempty"`
	Runtime           string                `yaml:"runtime" json:"runtime,omitempty"`
	Image             string                `yaml:"image" json:"image,omitempty"`
	Build             ImageBuildConfig      `yaml:"build" json:"build,omitempty"`
	Compose           ComposeConfig         `yaml:"compose" json:"compose,omitempty"`
	Command           RuntimeCommand        `yaml:"command" json:"command,omitempty"`
	WorkingDir        string                `yaml:"workingDir" json:"workingDir,omitempty"`
	Port              int                   `yaml:"port" json:"port,omitempty"`
	Ports             map[string]PortConfig `yaml:"ports" json:"ports,omitempty"`
	PrimaryPort       string                `yaml:"primaryPort" json:"primaryPort,omitempty"`
	OriginHost        string                `yaml:"originHost" json:"originHost,omitempty"`
	ProxyListenHost   string                `yaml:"proxyListenHost" json:"proxyListenHost,omitempty"`
	TunnelHostHeader  string                `yaml:"tunnelHostHeader" json:"tunnelHostHeader,omitempty"`
	Public            bool                  `yaml:"public" json:"public,omitempty"`
	PublicRewrite     PublicRewriteConfig   `yaml:"publicRewrite" json:"publicRewrite,omitempty"`
	Health            HealthConfig          `yaml:"health" json:"health,omitempty"`
	Env               map[string]string     `yaml:"env" json:"env,omitempty"`
	DependencyVolumes []VolumeConfig        `yaml:"dependencyVolumes" json:"dependencyVolumes,omitempty"`
	ResourceLimits    ResourceLimits        `yaml:"resources" json:"resources,omitempty"`
}

type ComposeConfig struct {
	File    string   `yaml:"file" json:"file,omitempty"`
	Files   []string `yaml:"files" json:"files,omitempty"`
	Service string   `yaml:"service" json:"service,omitempty"`
}

type PortConfig struct {
	Container     int      `yaml:"container" json:"container,omitempty"`
	Host          int      `yaml:"host" json:"host,omitempty"`
	HostIP        string   `yaml:"hostIp" json:"hostIp,omitempty"`
	Protocol      string   `yaml:"protocol" json:"protocol,omitempty"`
	PublicPath    string   `yaml:"publicPath" json:"publicPath,omitempty"`
	PublicOrigins []string `yaml:"publicOrigins" json:"publicOrigins,omitempty"`
}

type ImageBuildConfig struct {
	Context    string                `yaml:"context" json:"context,omitempty"`
	Dockerfile string                `yaml:"dockerfile" json:"dockerfile,omitempty"`
	Tag        string                `yaml:"tag" json:"tag,omitempty"`
	Args       map[string]string     `yaml:"args" json:"args,omitempty"`
	Cache      ImageBuildCacheConfig `yaml:"cache" json:"cache,omitempty"`
}

type ImageBuildCacheConfig struct {
	Enabled *bool    `yaml:"enabled" json:"enabled,omitempty"`
	From    []string `yaml:"from" json:"from,omitempty"`
	To      []string `yaml:"to" json:"to,omitempty"`
}

type PublicRewriteConfig struct {
	Hosts        []string                `yaml:"hosts" json:"hosts,omitempty"`
	Origins      []string                `yaml:"origins" json:"origins,omitempty"`
	BasePaths    []string                `yaml:"basePaths" json:"basePaths,omitempty"`
	Replacements []PublicRewriteTemplate `yaml:"replacements" json:"replacements,omitempty"`
}

type PublicRewriteTemplate struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
}

type PublicConfig struct {
	Provider         string `yaml:"provider" json:"provider,omitempty"`
	Mode             string `yaml:"mode" json:"mode,omitempty"`
	Tunnel           string `yaml:"tunnel" json:"tunnel,omitempty"`
	Zone             string `yaml:"zone" json:"zone,omitempty"`
	BaseDomain       string `yaml:"baseDomain" json:"baseDomain,omitempty"`
	Wildcard         string `yaml:"wildcard" json:"wildcard,omitempty"`
	Hostname         string `yaml:"hostname" json:"hostname,omitempty"`
	HostnameTemplate string `yaml:"hostnameTemplate" json:"hostnameTemplate,omitempty"`
	RouterAddr       string `yaml:"routerAddr" json:"routerAddr,omitempty"`
	InactiveBehavior string `yaml:"inactiveBehavior" json:"inactiveBehavior,omitempty"`
}

type BackingConfig struct {
	Source            string            `yaml:"source" json:"source,omitempty"`
	Runtime           string            `yaml:"runtime" json:"runtime,omitempty"`
	Image             string            `yaml:"image" json:"image,omitempty"`
	Compose           ComposeConfig     `yaml:"compose" json:"compose,omitempty"`
	Command           RuntimeCommand    `yaml:"command" json:"command,omitempty"`
	Env               map[string]string `yaml:"env" json:"env,omitempty"`
	Health            HealthConfig      `yaml:"health" json:"health,omitempty"`
	DependencyVolumes []VolumeConfig    `yaml:"dependencyVolumes" json:"dependencyVolumes,omitempty"`
	ResourceLimits    ResourceLimits    `yaml:"resources" json:"resources,omitempty"`
}

type HealthConfig struct {
	Path         string         `yaml:"path" json:"path,omitempty"`
	Command      RuntimeCommand `yaml:"command" json:"command,omitempty"`
	ExpectStatus int            `yaml:"expectStatus" json:"expectStatus,omitempty"`
	Interval     string         `yaml:"interval" json:"interval,omitempty"`
	Timeout      string         `yaml:"timeout" json:"timeout,omitempty"`
}

type VolumeConfig struct {
	Name          string `yaml:"name" json:"name"`
	Target        string `yaml:"target" json:"target"`
	Lifetime      string `yaml:"lifetime" json:"lifetime,omitempty"`
	RuntimeSource string `yaml:"-" json:"-"`
}

type WarmConfig struct {
	BaselineRefs []string              `yaml:"baselineRefs" json:"baselineRefs,omitempty"`
	Fingerprint  WarmFingerprintConfig `yaml:"fingerprint" json:"fingerprint,omitempty"`
}

type WarmFingerprintConfig struct {
	Paths []string `yaml:"paths" json:"paths,omitempty"`
}

type SeedConfig struct {
	Path    string `yaml:"path" json:"path,omitempty"`
	Restore string `yaml:"restore" json:"restore,omitempty"`
}

type PrebuildConfig struct {
	Image string   `yaml:"image" json:"image,omitempty"`
	Steps []string `yaml:"steps" json:"steps,omitempty"`
}

type SetupConfig struct {
	AfterSeeds []SetupStep `yaml:"afterSeeds" json:"afterSeeds,omitempty"`
}

type SetupStep struct {
	Service     string                `yaml:"service" json:"service"`
	Command     RuntimeCommand        `yaml:"command" json:"command"`
	Policy      string                `yaml:"policy" json:"policy,omitempty"`
	Fingerprint WarmFingerprintConfig `yaml:"fingerprint" json:"fingerprint,omitempty"`
}

type AgentConfig struct {
	DefaultPreviewService string                 `yaml:"defaultPreviewService" json:"defaultPreviewService,omitempty"`
	CommonPages           map[string]AgentPage   `yaml:"commonPages" json:"commonPages,omitempty"`
	SmokeTests            []SmokeTest            `yaml:"smokeTests" json:"smokeTests,omitempty"`
	ScreenshotBreakpoints []ScreenshotBreakpoint `yaml:"screenshotBreakpoints" json:"screenshotBreakpoints,omitempty"`
	QA                    QAConfig               `yaml:"qa" json:"qa,omitempty"`
	Iteration             AgentIteration         `yaml:"iteration" json:"iteration,omitempty"`
}

type ScreenshotBreakpoint struct {
	Name   string `yaml:"name" json:"name,omitempty"`
	Width  int    `yaml:"width" json:"width"`
	Height int    `yaml:"height" json:"height"`
}

type ScreenshotOptions struct {
	Path                  string                 `json:"path,omitempty"`
	Target                string                 `json:"target,omitempty"`
	ColorScheme           string                 `json:"colorScheme,omitempty"`
	StorageState          string                 `json:"storageState,omitempty"`
	Width                 int                    `json:"width,omitempty"`
	Height                int                    `json:"height,omitempty"`
	DeviceScaleFactor     float64                `json:"deviceScaleFactor,omitempty"`
	Breakpoints           []ScreenshotBreakpoint `json:"breakpoints,omitempty"`
	UseProjectBreakpoints bool                   `json:"useProjectBreakpoints,omitempty"`
	FullPage              bool                   `json:"fullPage,omitempty"`
	Crop                  bool                   `json:"crop,omitempty"`
	WaitForSelector       string                 `json:"waitForSelector,omitempty"`
	WaitForTimeout        string                 `json:"waitForTimeout,omitempty"`
	OutputDir             string                 `json:"outputDir,omitempty"`
}

type QARecordOptions struct {
	Scope             string  `json:"scope,omitempty"`
	Target            string  `json:"target,omitempty"`
	ColorScheme       string  `json:"colorScheme,omitempty"`
	StorageState      string  `json:"storageState,omitempty"`
	Width             int     `json:"width,omitempty"`
	Height            int     `json:"height,omitempty"`
	DeviceScaleFactor float64 `json:"deviceScaleFactor,omitempty"`
	Format            string  `json:"format,omitempty"`
	OutputDir         string  `json:"outputDir,omitempty"`
	SlowMoMS          int     `json:"slowMoMs,omitempty"`
	WaitMS            int     `json:"waitMs,omitempty"`
	WaitMSSet         bool    `json:"-"`
}

type QAFinalOptions struct {
	Scope             string   `json:"scope,omitempty"`
	Target            string   `json:"target,omitempty"`
	SkipScreenshots   bool     `json:"skipScreenshots,omitempty"`
	SkipRecord        bool     `json:"skipRecord,omitempty"`
	ColorScheme       string   `json:"colorScheme,omitempty"`
	StorageState      string   `json:"storageState,omitempty"`
	Width             int      `json:"width,omitempty"`
	Height            int      `json:"height,omitempty"`
	DeviceScaleFactor float64  `json:"deviceScaleFactor,omitempty"`
	Format            string   `json:"format,omitempty"`
	SlowMoMS          int      `json:"slowMoMs,omitempty"`
	WaitMS            int      `json:"waitMs,omitempty"`
	WaitMSSet         bool     `json:"-"`
	IncludeEvidence   []string `json:"includeEvidence,omitempty"`
}

type AgentPage struct {
	Service string `yaml:"service" json:"service"`
	Path    string `yaml:"path" json:"path"`
}

type QAConfig struct {
	DefaultScope string           `yaml:"defaultScope" json:"defaultScope,omitempty"`
	ArtifactRoot string           `yaml:"artifactRoot" json:"artifactRoot,omitempty"`
	Driver       QADriverConfig   `yaml:"driver" json:"driver,omitempty"`
	Auth         QAAuthConfig     `yaml:"auth" json:"auth,omitempty"`
	Evidence     QAEvidenceConfig `yaml:"evidence" json:"evidence,omitempty"`
	Scopes       []QAScope        `yaml:"scopes" json:"scopes,omitempty"`
}

type QAAuthConfig struct {
	Sessions map[string]QAAuthSession `yaml:"sessions" json:"sessions,omitempty"`
}

type QAAuthSession struct {
	StorageState string   `yaml:"storageState" json:"storageState,omitempty"`
	Scopes       []string `yaml:"scopes" json:"scopes,omitempty"`
	Note         string   `yaml:"note" json:"note,omitempty"`
}

type QADriverConfig struct {
	Preferred   string   `yaml:"preferred" json:"preferred,omitempty"`
	Evidence    string   `yaml:"evidence" json:"evidence,omitempty"`
	Exploratory string   `yaml:"exploratory" json:"exploratory,omitempty"`
	Allowed     []string `yaml:"allowed" json:"allowed,omitempty"`
	Notes       string   `yaml:"notes" json:"notes,omitempty"`
}

type QAEvidenceConfig struct {
	Screenshots QAScreenshotEvidenceConfig `yaml:"screenshots" json:"screenshots,omitempty"`
	Recordings  QARecordingEvidenceConfig  `yaml:"recordings" json:"recordings,omitempty"`
}

type QAScreenshotEvidenceConfig struct {
	ColorSchemes []string `yaml:"colorSchemes" json:"colorSchemes,omitempty"`
}

type QARecordingEvidenceConfig struct {
	ColorSchemes []string `yaml:"colorSchemes" json:"colorSchemes,omitempty"`
}

type QAScope struct {
	Name        string    `yaml:"name" json:"name"`
	Description string    `yaml:"description" json:"description,omitempty"`
	AuthSession string    `yaml:"authSession" json:"authSession,omitempty"`
	Pages       []string  `yaml:"pages" json:"pages,omitempty"`
	Flows       []QAFlow  `yaml:"flows" json:"flows,omitempty"`
	Checks      []QACheck `yaml:"checks" json:"checks,omitempty"`
}

type QAFlow struct {
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"description,omitempty"`
	Service     string   `yaml:"service" json:"service,omitempty"`
	Start       string   `yaml:"start" json:"start,omitempty"`
	Steps       []QAStep `yaml:"steps" json:"steps,omitempty"`
}

type QAStep struct {
	Visit      string `yaml:"visit" json:"visit,omitempty"`
	Click      string `yaml:"click" json:"click,omitempty"`
	Fill       string `yaml:"fill" json:"fill,omitempty"`
	Value      string `yaml:"value" json:"value,omitempty"`
	Press      string `yaml:"press" json:"press,omitempty"`
	ExpectText string `yaml:"expectText" json:"expectText,omitempty"`
	ExpectURL  string `yaml:"expectUrl" json:"expectUrl,omitempty"`
	Screenshot string `yaml:"screenshot" json:"screenshot,omitempty"`
	Note       string `yaml:"note" json:"note,omitempty"`
}

type QACheck struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description,omitempty"`
	Category    string `yaml:"category" json:"category,omitempty"`
	Severity    string `yaml:"severity" json:"severity,omitempty"`
	Method      string `yaml:"method" json:"method,omitempty"`
}

type AgentIteration struct {
	RestartCommand           *SetupStep `yaml:"restartCommand" json:"restartCommand,omitempty"`
	DependencyChangedCommand *SetupStep `yaml:"dependencyChangedCommand" json:"dependencyChangedCommand,omitempty"`
}

type SmokeTest struct {
	Name         string `yaml:"name" json:"name"`
	Service      string `yaml:"service" json:"service,omitempty"`
	Path         string `yaml:"path" json:"path,omitempty"`
	ExpectStatus int    `yaml:"expectStatus" json:"expectStatus,omitempty"`
	Command      string `yaml:"command" json:"command,omitempty"`
}

type ResourceConfig struct {
	MaxConcurrentPreviews int `yaml:"maxConcurrentPreviews" json:"maxConcurrentPreviews,omitempty"`
	MaxStartupConcurrency int `yaml:"maxStartupConcurrency" json:"maxStartupConcurrency,omitempty"`
}

type ProfileConfig struct {
	Services        []string                     `yaml:"services" json:"services,omitempty"`
	BackingServices []string                     `yaml:"backingServices" json:"backingServices,omitempty"`
	SmokeTests      []string                     `yaml:"smokeTests" json:"smokeTests,omitempty"`
	ServiceEnv      map[string]map[string]string `yaml:"serviceEnv" json:"serviceEnv,omitempty"`
}

type ResourceLimits struct {
	CPUs   string `yaml:"cpus" json:"cpus,omitempty"`
	Memory string `yaml:"memory" json:"memory,omitempty"`
}

type ProjectRecord struct {
	Name     string        `json:"name"`
	Path     string        `json:"path"`
	Config   ProjectConfig `json:"config"`
	SyncedAt time.Time     `json:"syncedAt"`
}

type PreviewRecord struct {
	ID         string                    `json:"id"`
	Project    string                    `json:"project"`
	Profile    string                    `json:"profile,omitempty"`
	Status     string                    `json:"status"`
	ConfigHash string                    `json:"-"`
	Labels     map[string]string         `json:"labels,omitempty"`
	Metadata   map[string]string         `json:"metadata,omitempty"`
	Sources    map[string]PreviewSource  `json:"sources,omitempty"`
	Services   map[string]PreviewService `json:"services,omitempty"`
	CreatedAt  time.Time                 `json:"createdAt"`
	UpdatedAt  time.Time                 `json:"updatedAt"`
}

type PreviewSource struct {
	Name  string `json:"name"`
	Mode  string `json:"mode"`
	Ref   string `json:"ref,omitempty"`
	Path  string `json:"path"`
	Owned bool   `json:"owned"`
}

type PreviewService struct {
	Name              string                 `json:"name"`
	Source            string                 `json:"source,omitempty"`
	Runtime           string                 `json:"runtime,omitempty"`
	ContainerID       string                 `json:"containerId,omitempty"`
	Status            string                 `json:"status"`
	PID               int                    `json:"pid,omitempty"`
	PIDIdentity       string                 `json:"-"`
	ProxyPID          int                    `json:"proxyPid,omitempty"`
	ProxyPIDIdentity  string                 `json:"-"`
	TunnelPID         int                    `json:"tunnelPid,omitempty"`
	TunnelPIDIdentity string                 `json:"-"`
	Port              int                    `json:"port,omitempty"`
	Ports             map[string]PreviewPort `json:"ports,omitempty"`
	URL               string                 `json:"url,omitempty"`
	OriginURL         string                 `json:"originUrl,omitempty"`
	ProxyURL          string                 `json:"proxyUrl,omitempty"`
	LogPath           string                 `json:"logPath,omitempty"`
	TunnelLogPath     string                 `json:"tunnelLogPath,omitempty"`
	Command           string                 `json:"command,omitempty"`
	StartedAt         time.Time              `json:"startedAt,omitempty"`
	LastHealth        string                 `json:"lastHealth,omitempty"`
}

type PreviewPort struct {
	Name      string `json:"name"`
	Container int    `json:"container"`
	Host      int    `json:"host"`
	HostIP    string `json:"hostIp,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	URL       string `json:"url,omitempty"`
	Primary   bool   `json:"primary,omitempty"`
}

type Event struct {
	Seq       int64             `json:"seq"`
	PreviewID string            `json:"previewId"`
	Timestamp time.Time         `json:"timestamp"`
	Level     string            `json:"level"`
	Type      string            `json:"type"`
	Message   string            `json:"message"`
	Service   string            `json:"service,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type UpRequest struct {
	Project  string            `json:"project"`
	ID       string            `json:"id"`
	Profile  string            `json:"profile,omitempty"`
	Sources  map[string]string `json:"sources,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Wait     bool              `json:"wait"`
	Timeout  time.Duration     `json:"-"`
	Public   bool              `json:"public"`
	Reuse    bool              `json:"reuse,omitempty"`
}
