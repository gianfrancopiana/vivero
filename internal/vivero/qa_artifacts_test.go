package vivero

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestQAURLsDefaultToLocalProxyForFastEvidence(t *testing.T) {
	p := PreviewRecord{Services: map[string]PreviewService{
		"web": {
			URL:       "https://public.example.trycloudflare.com",
			OriginURL: "http://127.0.0.1:3000",
			ProxyURL:  "http://127.0.0.1:4444",
		},
	}}

	got, err := qaURLForServicePath(p, "web", "/login")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:4444/login" {
		t.Fatalf("default QA URL = %s; want local proxy URL", got)
	}

	public, err := qaURLForServicePathWithTarget(p, "web", "/login", "public")
	if err != nil {
		t.Fatal(err)
	}
	if public != "https://public.example.trycloudflare.com/login" {
		t.Fatalf("public QA URL = %s", public)
	}
}

func TestNormalizeArtifactRecordingDefaults(t *testing.T) {
	shot := normalizeScreenshotOptions(ScreenshotOptions{})
	if shot.Target != "local" {
		t.Fatalf("screenshot target = %q; want local", shot.Target)
	}
	if shot.Width != 1280 || shot.Height != 800 {
		t.Fatalf("screenshot viewport = %dx%d; want 1280x800", shot.Width, shot.Height)
	}
	if shot.DeviceScaleFactor != 1 {
		t.Fatalf("device scale factor = %v; want 1", shot.DeviceScaleFactor)
	}

	rec := normalizeQARecordOptions(QARecordOptions{})
	if rec.Width != 1280 || rec.Height != 800 {
		t.Fatalf("record viewport = %dx%d; want 1280x800", rec.Width, rec.Height)
	}
	if rec.Format != "mp4" {
		t.Fatalf("record format = %q; want mp4", rec.Format)
	}
}

func TestEvidenceColorSchemeIsNormalizedAndNamed(t *testing.T) {
	shot := normalizeScreenshotOptions(ScreenshotOptions{ColorScheme: "Dark"})
	if shot.ColorScheme != "dark" {
		t.Fatalf("screenshot color scheme = %q; want dark", shot.ColorScheme)
	}
	if err := validateColorScheme("sepia"); err == nil {
		t.Fatal("expected invalid color scheme to fail")
	}
	if got := strings.Join(normalizeColorSchemes([]string{"Light", "", "light", "dark"}), ","); got != "light,dark" {
		t.Fatalf("normalized color schemes = %s; want light,dark", got)
	}

	path := screenshotOutputPath("/home", "/tmp/out", "preview", "web", "/", ScreenshotBreakpoint{Name: "desktop", Width: 1440, Height: 900}, true, "dark")
	if !strings.Contains(path, "web-_-desktop-dark.png") {
		t.Fatalf("screenshot path should include breakpoint and color scheme: %s", path)
	}
}

func TestQAEvidencePlanExposesYAMLBackedConcreteCommands(t *testing.T) {
	p := PreviewRecord{Services: map[string]PreviewService{
		"web": {
			OriginURL: "http://127.0.0.1:3000",
		},
	}}
	agent := AgentConfig{
		DefaultPreviewService: "web",
		CommonPages: map[string]AgentPage{
			"home": {Service: "web", Path: "/"},
		},
		ScreenshotBreakpoints: []ScreenshotBreakpoint{
			{Name: "desktop", Width: 1440, Height: 900},
			{Name: "mobile", Width: 390, Height: 844},
		},
		QA: QAConfig{
			Evidence: QAEvidenceConfig{
				Screenshots: QAScreenshotEvidenceConfig{ColorSchemes: []string{"light", "dark"}},
				Recordings:  QARecordingEvidenceConfig{ColorSchemes: []string{"light"}},
			},
			Scopes: []QAScope{{Name: "core", Pages: []string{"home"}}},
		},
	}

	evidence, err := qaEvidencePlan("preview", "core", p, agent, agent.QA.Scopes, nil, "", "/tmp/qa")
	if err != nil {
		t.Fatal(err)
	}
	screenshots := evidence["screenshots"].(map[string]any)
	if strings.Join(screenshots["colorSchemes"].([]string), ",") != "light,dark" {
		t.Fatalf("screenshot color schemes = %#v", screenshots["colorSchemes"])
	}
	screenshotCommands := screenshots["commands"].([]map[string]any)
	if len(screenshotCommands) != 2 {
		t.Fatalf("screenshot commands = %d; want light and dark", len(screenshotCommands))
	}
	firstScreenshotCommand := screenshotCommands[0]["command"].(string)
	if !strings.Contains(firstScreenshotCommand, "--breakpoints") || !strings.Contains(firstScreenshotCommand, "--color-scheme light") {
		t.Fatalf("screenshot command should be concrete and YAML-backed: %s", firstScreenshotCommand)
	}
	recordings := evidence["recordings"].(map[string]any)
	recordingCommands := recordings["commands"].([]map[string]any)
	if len(recordingCommands) != 1 {
		t.Fatalf("recording commands = %d; want configured light recording", len(recordingCommands))
	}
	wantRecordArgs := []string{"vivero", "qa", "record", "preview", "--scope", "core", "--json", "--no-input", "--quiet", "--color-scheme", "light"}
	if got := recordingCommands[0]["argv"].([]string); !reflect.DeepEqual(got, wantRecordArgs) {
		t.Fatalf("recording argv = %#v; want %#v", got, wantRecordArgs)
	}
}

func TestQAEvidencePlanRecordCommandsIgnorePublicPlanTarget(t *testing.T) {
	p := PreviewRecord{Services: map[string]PreviewService{
		"web": {
			URL:       "https://public.example.trycloudflare.com",
			OriginURL: "http://127.0.0.1:3000",
			ProxyURL:  "http://127.0.0.1:4444",
		},
	}}
	agent := AgentConfig{
		DefaultPreviewService: "web",
		CommonPages: map[string]AgentPage{
			"home": {Service: "web", Path: "/"},
		},
		QA: QAConfig{
			Evidence: QAEvidenceConfig{
				Recordings: QARecordingEvidenceConfig{ColorSchemes: []string{"light"}},
			},
			Scopes: []QAScope{{Name: "public", Pages: []string{"home"}}},
		},
	}

	evidence, err := qaEvidencePlan("preview", "public", p, agent, agent.QA.Scopes, nil, "public", "/tmp/qa")
	if err != nil {
		t.Fatal(err)
	}
	recordings := evidence["recordings"].(map[string]any)
	recordingCommands := recordings["commands"].([]map[string]any)
	wantRecordArgs := []string{"vivero", "qa", "record", "preview", "--scope", "public", "--json", "--no-input", "--quiet", "--color-scheme", "light"}
	if got := recordingCommands[0]["argv"].([]string); !reflect.DeepEqual(got, wantRecordArgs) {
		t.Fatalf("recording argv = %#v; want %#v", got, wantRecordArgs)
	}
}

func TestQAServiceMapUsesLocalURLAsDefaultEvidenceTarget(t *testing.T) {
	p := PreviewRecord{Services: map[string]PreviewService{
		"web": {
			URL:       "https://public.example.trycloudflare.com",
			OriginURL: "http://127.0.0.1:3000",
			ProxyURL:  "http://127.0.0.1:4444",
		},
	}}

	local := qaServiceMapForTarget(p, "")
	localWeb := local["web"].(map[string]any)
	if localWeb["url"] != "http://127.0.0.1:4444" {
		t.Fatalf("default service url = %v; want local proxy", localWeb["url"])
	}
	if localWeb["publicUrl"] != "https://public.example.trycloudflare.com" {
		t.Fatalf("publicUrl = %v", localWeb["publicUrl"])
	}

	public := qaServiceMapForTarget(p, "public")
	publicWeb := public["web"].(map[string]any)
	if publicWeb["url"] != "https://public.example.trycloudflare.com" {
		t.Fatalf("public service url = %v", publicWeb["url"])
	}
}

func TestDefaultPreviewServiceSkipsURLLessBackingServices(t *testing.T) {
	p := PreviewRecord{Services: map[string]PreviewService{
		"db":  {Name: "db", Status: "healthy"},
		"web": {Name: "web", Status: "healthy", ProxyURL: "http://127.0.0.1:4444"},
	}}
	if got := defaultPreviewService(AgentConfig{}, p); got != "web" {
		t.Fatalf("default preview service = %q; want web", got)
	}
}

func TestDiscoverabilityDocumentsQARecordOptions(t *testing.T) {
	qa := schemaFor("qa")["schema"].(map[string]any)
	usage := qa["usage"].(string)
	_, recordUsage, hasRecordUsage := strings.Cut(usage, ";")
	if !hasRecordUsage || !strings.Contains(recordUsage, "vivero qa record") {
		t.Fatalf("qa schema usage should advertise record action: %s", usage)
	}
	if !strings.Contains(usage, "--public") {
		t.Fatalf("qa schema usage should advertise explicit public target for non-record actions: %s", usage)
	}
	if strings.Contains(recordUsage, "--public") || strings.Contains(recordUsage, "--origin") || strings.Contains(recordUsage, "--target") {
		t.Fatalf("qa record usage should not include target flags: %s", recordUsage)
	}
	if strings.Contains(usage, "--color-scheme") {
		t.Fatalf("qa schema should keep color schemes in agent.qa.evidence, not as a broad qa flag: %s", usage)
	}
	if qa["config"] != "agent.qa, agent.qa.auth, and agent.qa.evidence in vivero.yml" {
		t.Fatalf("qa schema config = %v", qa["config"])
	}
	recordOptions := qa["recordOptions"].(map[string]any)
	if _, ok := recordOptions["target"]; ok {
		t.Fatalf("qa schema should not expose record target options: %v", recordOptions)
	}
	if !strings.Contains(recordOptions["colorScheme"].(string), "generated evidence.recordings.commands") {
		t.Fatalf("qa schema should document record color scheme as generated evidence primitive: %v", recordOptions)
	}
	if !strings.Contains(recordOptions["storageState"].(string), "agent.qa.auth.sessions") {
		t.Fatalf("qa schema should document storage-state auth primitive: %v", recordOptions)
	}

	shot := schemaFor("screenshot")["schema"].(map[string]any)
	defaults := shot["defaults"].(map[string]any)
	if defaults["target"] != "local" {
		t.Fatalf("screenshot schema target default = %v", defaults["target"])
	}
	shotUsage := shot["usage"].(string)
	if !strings.Contains(shotUsage, "--color-scheme") {
		t.Fatalf("screenshot schema usage should advertise color scheme evidence: %s", shotUsage)
	}
	if !strings.Contains(fmt.Sprint(shot["flags"]), "--storage-state") {
		t.Fatalf("screenshot schema should advertise authenticated evidence storage state: %v", shot["flags"])
	}
}
