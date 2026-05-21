package vivero

import (
	"fmt"
	"path/filepath"
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

func TestQAExplicitTargetsDoNotFallbackToLocalURLs(t *testing.T) {
	localOnly := PreviewRecord{Services: map[string]PreviewService{
		"web": {ProxyURL: "http://127.0.0.1:4444"},
	}}
	if _, err := qaURLForServicePathWithTarget(localOnly, "web", "/login", "public"); err == nil || !strings.Contains(err.Error(), "no public URL") {
		t.Fatalf("explicit public target should require a public URL, got %v", err)
	}

	runtimeLocal := PreviewRecord{Services: map[string]PreviewService{
		"web": {URL: "http://127.0.0.1:4444", OriginURL: "http://127.0.0.1:3000", ProxyURL: "http://127.0.0.1:4444"},
	}}
	if _, err := qaURLForServicePathWithTarget(runtimeLocal, "web", "/login", "public"); err == nil || !strings.Contains(err.Error(), "no public URL") {
		t.Fatalf("explicit public target should not treat runtime local/proxy URL as public, got %v", err)
	}

	proxiedOnly := PreviewRecord{Services: map[string]PreviewService{
		"web": {URL: "https://public.example.trycloudflare.com", ProxyURL: "http://127.0.0.1:4444"},
	}}
	if _, err := qaURLForServicePathWithTarget(proxiedOnly, "web", "/login", "origin"); err == nil || !strings.Contains(err.Error(), "no origin URL") {
		t.Fatalf("explicit origin target should require an origin URL, got %v", err)
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

func TestQAArtifactDirKeepsPreviewAndScopeUnderArtifactRoot(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()
	dir, err := qaArtifactDir(home, projectRoot, "../outside", "auth/../admin", "")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "qa")
	if !pathWithinRoot(root, dir) {
		t.Fatalf("artifact dir escaped root: dir=%s root=%s", dir, root)
	}
	if strings.Contains(dir, "..") {
		t.Fatalf("artifact dir should not preserve path traversal elements: %s", dir)
	}
}

func TestQAArtifactDirRejectsProjectRelativeRootEscape(t *testing.T) {
	_, err := qaArtifactDir(t.TempDir(), t.TempDir(), "preview", "smoke", "../outside")
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected project-relative artifact root escape error, got %v", err)
	}
}

func TestQAFinalUsesRecordingPlanForDefaultProof(t *testing.T) {
	plan := map[string]any{
		"scopes":                []map[string]any{{"name": "auth"}},
		"defaultPreviewService": "web",
		"services": map[string]any{
			"web": map[string]any{"url": "http://127.0.0.1:4444"},
		},
		"evidence": map[string]any{
			"recordings": map[string]any{
				"commands": []map[string]any{{"colorScheme": "dark", "storageState": "/tmp/state.json"}},
			},
		},
		"artifacts": map[string]any{"reportPath": "/tmp/report.md", "recordPath": "/tmp/record.json", "videoDir": "/tmp/videos"},
	}

	recordOpts := qaFinalRecordOptionsFromPlan(plan, QAFinalOptions{})
	if recordOpts.Scope != "auth" || recordOpts.ColorScheme != "dark" || recordOpts.StorageState != "/tmp/state.json" {
		t.Fatalf("qa final default record opts should come from evidence plan: %#v", recordOpts)
	}

	proof := qaFinalProof(map[string]any{
		"ok":        true,
		"preview":   "preview",
		"scope":     "auth",
		"target":    "local",
		"plan":      plan,
		"artifacts": plan["artifacts"],
		"run":       map[string]any{"runPath": "/tmp/run.json", "smoke": map[string]any{"ok": true}},
		"record":    map[string]any{"recordPath": "/tmp/record.json", "videos": []any{map[string]any{"path": "/tmp/videos/auth/home.mp4"}}},
		"finalPath": "/tmp/final.json",
	})
	if proof["url"] != "http://127.0.0.1:4444" || proof["recordPath"] != "/tmp/record.json" || proof["finalPath"] != "/tmp/final.json" || proof["smoke"] != true {
		t.Fatalf("qa final proof should summarize recorded evidence: %#v", proof)
	}
	videos := proof["videos"].([]string)
	if len(videos) != 1 || videos[0] != "/tmp/videos/auth/home.mp4" {
		t.Fatalf("qa final proof videos = %#v", videos)
	}
}

func TestQAPureHelperFallbacks(t *testing.T) {
	p := PreviewRecord{Services: map[string]PreviewService{
		"api": {Name: "api", OriginURL: "http://127.0.0.1:3001"},
		"web": {Name: "web", OriginURL: "http://127.0.0.1:3000", URL: "https://web.example.test"},
	}}
	agent := AgentConfig{
		CommonPages: map[string]AgentPage{
			"login": {Service: "web", Path: "login"},
		},
		SmokeTests: []SmokeTest{{Name: "health", Path: "/health"}},
	}

	page, err := resolveQAPage(p, agent, "login", "api")
	if err != nil {
		t.Fatal(err)
	}
	if page["path"] != "/login" || page["url"] != "http://127.0.0.1:3000/login" {
		t.Fatalf("resolveQAPage should normalize paths and local URL: %#v", page)
	}
	if _, err := resolveQAPage(PreviewRecord{}, AgentConfig{}, "/missing", ""); err == nil || !strings.Contains(err.Error(), "no service") {
		t.Fatalf("expected no-service page error, got %v", err)
	}

	if got := qaCommandWithTarget("vivero screenshot pr web /", "origin"); got != "vivero screenshot pr web / --target origin" {
		t.Fatalf("qaCommandWithTarget origin = %q", got)
	}
	if got := qaServiceMap(p)["api"].(map[string]any)["url"]; got != "http://127.0.0.1:3001" {
		t.Fatalf("qaServiceMap default url = %v", got)
	}

	plan := map[string]any{
		"evidence": map[string]any{
			"screenshots": map[string]any{"colorSchemes": []any{"Light", "dark", "light", 7}},
			"recordings":  map[string]any{"commands": []any{map[string]any{"colorScheme": "dark"}}},
		},
		"services": map[string]any{
			"api": map[string]any{"url": ""},
			"web": map[string]any{"url": "http://127.0.0.1:3000"},
		},
		"defaultPreviewService": "missing",
	}
	if got := qaScreenshotColorSchemesFromPlan(plan); !reflect.DeepEqual(got, []string{"light", "dark"}) {
		t.Fatalf("qaScreenshotColorSchemesFromPlan = %#v", got)
	}
	if got := firstQARecordingCommand(plan); got == nil || got["colorScheme"] != "dark" {
		t.Fatalf("firstQARecordingCommand = %#v", got)
	}
	if got := qaFinalPrimaryURL(plan); got != "http://127.0.0.1:3000" {
		t.Fatalf("qaFinalPrimaryURL fallback = %q", got)
	}

	scopes, selected, err := selectedQAScopes(agent, "")
	if err != nil {
		t.Fatal(err)
	}
	if selected != "default" || len(scopes) != 1 || !reflect.DeepEqual(scopes[0].Pages, []string{"login"}) || len(scopes[0].Checks) != 1 {
		t.Fatalf("default selected QA scope = selected %q scopes %#v", selected, scopes)
	}
	if got := qaScopeNames([]QAScope{{Name: "z"}, {}, {Name: "a"}}); !reflect.DeepEqual(got, []string{"a", "default", "z"}) {
		t.Fatalf("qaScopeNames = %#v", got)
	}
	if got := qaRecordHuman(map[string]any{"ok": true, "recordPath": "/tmp/record.json"}); got != "qa record ok\nrecord: /tmp/record.json" {
		t.Fatalf("qaRecordHuman = %q", got)
	}
	if got := qaFinalScreenshotPaths([]map[string]any{{"path": "/tmp/a.png"}, {"path": ""}}); !reflect.DeepEqual(got, []string{"/tmp/a.png"}) {
		t.Fatalf("qaFinalScreenshotPaths map slice = %#v", got)
	}
	if got := qaFinalScreenshotPaths([]any{map[string]any{"path": "/tmp/b.png"}, "skip"}); !reflect.DeepEqual(got, []string{"/tmp/b.png"}) {
		t.Fatalf("qaFinalScreenshotPaths any slice = %#v", got)
	}
	grouped := []any{map[string]any{"screenshots": []any{map[string]any{"path": "/tmp/grouped-desktop.png"}, map[string]any{"path": "/tmp/grouped-mobile.png"}}}}
	if got := qaFinalScreenshotPaths(grouped); !reflect.DeepEqual(got, []string{"/tmp/grouped-desktop.png", "/tmp/grouped-mobile.png"}) {
		t.Fatalf("qaFinalScreenshotPaths grouped results = %#v", got)
	}
	duplicated := []any{map[string]any{"path": "/tmp/single.png", "screenshots": []any{map[string]any{"path": "/tmp/single.png"}}}}
	if got := qaFinalScreenshotPaths(duplicated); !reflect.DeepEqual(got, []string{"/tmp/single.png"}) {
		t.Fatalf("qaFinalScreenshotPaths should dedupe flattened+grouped results = %#v", got)
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
