package vivero

import (
	"io"
	"sort"
	"strings"
	"time"
)

type StartupDiagnosis struct {
	PreviewID       string            `json:"previewId"`
	Project         string            `json:"project,omitempty"`
	Status          string            `json:"status,omitempty"`
	TotalMs         int64             `json:"totalMs,omitempty"`
	SlowestPhase    DiagnosticPhase   `json:"slowestPhase,omitempty"`
	Failure         *DiagnosticPhase  `json:"failure,omitempty"`
	Phases          []DiagnosticPhase `json:"phases"`
	Recommendations []string          `json:"recommendations,omitempty"`
}

type DiagnosticPhase struct {
	Type       string            `json:"type"`
	Service    string            `json:"service,omitempty"`
	Message    string            `json:"message,omitempty"`
	DurationMs int64             `json:"durationMs,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

func (a *App) DiagnoseStartup(previewID string) (StartupDiagnosis, error) {
	preview, err := a.getPreview(previewID)
	if err != nil {
		return StartupDiagnosis{}, err
	}
	events, err := a.events(previewID, 0)
	if err != nil {
		return StartupDiagnosis{}, err
	}
	diag := StartupDiagnosis{
		PreviewID: preview.ID,
		Project:   preview.Project,
		Status:    preview.Status,
		Phases:    []DiagnosticPhase{},
	}
	var slowest DiagnosticPhase
	for _, event := range events {
		phase := diagnosticPhaseFromEvent(event)
		diag.Phases = append(diag.Phases, phase)
		if phase.DurationMs > 0 {
			diag.TotalMs += phase.DurationMs
			if phase.DurationMs > slowest.DurationMs {
				slowest = phase
			}
		}
		if diag.Failure == nil && eventIsFailure(event) {
			failure := phase
			diag.Failure = &failure
		}
	}
	sort.SliceStable(diag.Phases, func(i, j int) bool {
		return diag.Phases[i].Timestamp.Before(diag.Phases[j].Timestamp)
	})
	if slowest.Type == "" && len(diag.Phases) > 1 {
		first := diag.Phases[0].Timestamp
		last := diag.Phases[len(diag.Phases)-1].Timestamp
		if last.After(first) {
			diag.TotalMs = last.Sub(first).Milliseconds()
		}
	}
	if slowest.Type != "" {
		diag.SlowestPhase = slowest
		diag.Recommendations = appendRecommendation(diag.Recommendations, recommendationForPhase(slowest.Type))
	}
	if diag.Failure != nil {
		diag.Recommendations = appendRecommendation(diag.Recommendations, recommendationForFailure(diag.Failure.Type))
	}
	return diag, nil
}

func diagnosticPhaseFromEvent(event Event) DiagnosticPhase {
	metadata := redactDiagnosticMetadata(copyStringMap(event.Metadata))
	durationMs, _ := durationMsFromMetadata(metadata)
	return DiagnosticPhase{
		Type:       event.Type,
		Service:    event.Service,
		Message:    event.Message,
		DurationMs: durationMs,
		Timestamp:  event.Timestamp,
		Metadata:   metadata,
	}
}

func eventIsFailure(event Event) bool {
	if strings.EqualFold(event.Level, "error") {
		return true
	}
	return strings.Contains(event.Type, ".failed") || strings.Contains(event.Type, "_failed")
}

func appendRecommendation(recs []string, rec string) []string {
	if rec == "" {
		return recs
	}
	for _, existing := range recs {
		if existing == rec {
			return recs
		}
	}
	return append(recs, rec)
}

func recommendationForFailure(typ string) string {
	switch {
	case strings.Contains(typ, "setup"):
		return "setup failed; inspect the setup command, service logs, and dependency volume policy"
	case strings.Contains(typ, "source"):
		return "source preparation failed; verify the configured source path, repo, and ref"
	case strings.Contains(typ, "image"):
		return "image build failed; inspect the app-owned Dockerfile and build context"
	case strings.Contains(typ, "service"):
		return "service startup failed; inspect service logs, health checks, and container command"
	case strings.Contains(typ, "tunnel"):
		return "public tunnel failed; use local target for routine QA and verify public tunnel configuration"
	default:
		return "inspect the failure event and surrounding preview logs"
	}
}

func recommendationForPhase(typ string) string {
	switch typ {
	case "image.built", "image.building":
		return "image build is the bottleneck; consider app-owned Dockerfile layer caching or prebuild, not Vivero registry behavior"
	case "setup.afterSeeds":
		return "split dependency install from build and use fingerprinted setup policy where outputs persist in dependency volumes"
	case "service.healthy":
		return "inspect health path/command and service logs"
	case "tunnel.ready":
		return "public tunnel is bottleneck; use local target for routine QA and public target only when validating public URLs"
	case "source.ready":
		return "source resolution is slow; inspect source path, ref, and fetch/worktree behavior"
	default:
		if strings.Contains(typ, "setup") {
			return "split dependency install from build and use fingerprinted setup policy where outputs persist in dependency volumes"
		}
		if strings.Contains(typ, "service") {
			return "inspect service startup, health path/command, and service logs"
		}
		return "inspect the slowest phase and surrounding events"
	}
}

func startupDiagnosisHuman(diag StartupDiagnosis) string {
	var b strings.Builder
	b.WriteString("startup diagnosis for " + diag.PreviewID + "\n")
	if diag.Status != "" {
		b.WriteString("status: " + diag.Status + "\n")
	}
	if diag.TotalMs > 0 {
		b.WriteString("total: " + humanDurationMs(diag.TotalMs) + "\n")
	}
	if diag.SlowestPhase.Type != "" {
		b.WriteString("slowest: " + phaseSummary(diag.SlowestPhase) + "\n")
	}
	if diag.Failure != nil {
		b.WriteString("failure: " + phaseSummary(*diag.Failure) + "\n")
	}
	if len(diag.Recommendations) > 0 {
		b.WriteString("next: " + diag.Recommendations[0] + "\n")
	}
	return b.String()
}

func phaseSummary(phase DiagnosticPhase) string {
	parts := []string{phase.Type}
	if phase.Service != "" {
		parts = append(parts, phase.Service)
	}
	if phase.DurationMs > 0 {
		parts = append(parts, humanDurationMs(phase.DurationMs))
	}
	return strings.Join(parts, " ")
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func redactDiagnosticMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	for k, v := range in {
		if diagnosticMetadataLooksSensitive(k, v) {
			in[k] = "[redacted]"
		}
	}
	return in
}

func diagnosticMetadataLooksSensitive(key, value string) bool {
	needle := strings.ToLower(key + "=" + value)
	for _, sensitive := range []string{"secret", "token", "password", "passwd", "api_key", "apikey", "access_key", "private_key"} {
		if strings.Contains(needle, sensitive) {
			return true
		}
	}
	return false
}

func (a *App) runDiagnose(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	if len(args) == 0 {
		return errOut(stderr, jsonOut, missingArgError("diagnose startup", "preview"))
	}
	if args[0] != "startup" {
		return errOut(stderr, jsonOut, unknownSubcommandError("diagnose", args[0]))
	}
	if len(args) < 2 {
		return errOut(stderr, jsonOut, missingArgError("diagnose startup", "preview"))
	}
	previewID, targetRef, err := resolvePreviewTargetRef(args[1])
	if err != nil {
		return errOut(stderr, jsonOut, err)
	}
	diag, err := a.DiagnoseStartup(previewID)
	if err != nil {
		return errOut(stderr, jsonOut, err)
	}
	output(stdout, jsonOut, map[string]any{"diagnosis": diag, "targetRef": targetRef}, startupDiagnosisHuman(diag))
	return 0
}
