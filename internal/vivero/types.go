package vivero

import "github.com/gianfrancopiana/vivero/internal/schema"

var (
	Version   = "0.1.0"
	Commit    = "unknown"
	BuildDate = "unknown"
)

type VersionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

func buildVersionInfo() VersionInfo {
	return VersionInfo{Version: Version, Commit: Commit, Date: BuildDate}
}

type ProjectConfig = schema.ProjectConfig
type ProjectMeta = schema.ProjectMeta
type SourceConfig = schema.SourceConfig
type RuntimeCommand = schema.RuntimeCommand
type ServiceConfig = schema.ServiceConfig
type ComposeConfig = schema.ComposeConfig
type PortConfig = schema.PortConfig
type ImageBuildConfig = schema.ImageBuildConfig
type ImageBuildCacheConfig = schema.ImageBuildCacheConfig
type PublicRewriteConfig = schema.PublicRewriteConfig
type PublicRewriteTemplate = schema.PublicRewriteTemplate
type PublicConfig = schema.PublicConfig
type BackingConfig = schema.BackingConfig
type HealthConfig = schema.HealthConfig
type VolumeConfig = schema.VolumeConfig
type WarmConfig = schema.WarmConfig
type WarmFingerprintConfig = schema.WarmFingerprintConfig
type SeedConfig = schema.SeedConfig
type PrebuildConfig = schema.PrebuildConfig
type SetupConfig = schema.SetupConfig
type SetupStep = schema.SetupStep
type AgentConfig = schema.AgentConfig
type ScreenshotBreakpoint = schema.ScreenshotBreakpoint
type ScreenshotOptions = schema.ScreenshotOptions
type QARecordOptions = schema.QARecordOptions
type QAFinalOptions = schema.QAFinalOptions
type AgentPage = schema.AgentPage
type QAConfig = schema.QAConfig
type QAAuthConfig = schema.QAAuthConfig
type QAAuthSession = schema.QAAuthSession
type QADriverConfig = schema.QADriverConfig
type QAEvidenceConfig = schema.QAEvidenceConfig
type QAScreenshotEvidenceConfig = schema.QAScreenshotEvidenceConfig
type QARecordingEvidenceConfig = schema.QARecordingEvidenceConfig
type QAScope = schema.QAScope
type QAFlow = schema.QAFlow
type QAStep = schema.QAStep
type QACheck = schema.QACheck
type AgentIteration = schema.AgentIteration
type SmokeTest = schema.SmokeTest
type ResourceConfig = schema.ResourceConfig
type ProfileConfig = schema.ProfileConfig
type ResourceLimits = schema.ResourceLimits
type ProjectRecord = schema.ProjectRecord
type PreviewRecord = schema.PreviewRecord
type PreviewSource = schema.PreviewSource
type PreviewService = schema.PreviewService
type PreviewPort = schema.PreviewPort
type Event = schema.Event
type UpRequest = schema.UpRequest
