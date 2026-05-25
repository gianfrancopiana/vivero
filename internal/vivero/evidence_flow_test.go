package vivero

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEvidenceFlowRunWritesResultReportAndConvertsVideos(t *testing.T) {
	t.Setenv("VIVERO_PLAYWRIGHT_PACKAGE", "")
	a, _ := newQARecordTestApp(t)
	defer a.Close()

	stepsFile := filepath.Join(t.TempDir(), "flow.yaml")
	if err := os.WriteFile(stepsFile, []byte(`name: checkout-review
start: home
variants:
  - name: desktop-dark
    viewport:
      width: 1280
      height: 900
    colorScheme: dark
record:
  video: true
  screenshots: true
  console: true
actions:
  - screenshot:
      name: loaded
      fullPage: true
  - expectText: Demo
`), 0o644); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(t.TempDir(), "flow-artifacts")
	webmPath := filepath.Join(outputDir, "desktop-dark", "walkthrough.webm")
	screenshotPath := filepath.Join(outputDir, "desktop-dark", "loaded.png")
	consolePath := filepath.Join(outputDir, "desktop-dark", "console.json")

	runner := &fakeQARecordRunner{runFunc: func(name string, args ...string) ([]byte, []byte, error) {
		if name != "npm" {
			t.Fatalf("evidence flow runner command = %s; want npm", name)
		}
		if len(args) < 4 || !reflect.DeepEqual(args[:4], []string{"exec", "--yes", "--package", "playwright"}) {
			t.Fatalf("npm args start = %#v", args)
		}
		inputPath := args[len(args)-1]
		payload := readJSONFile[map[string]any](t, inputPath)
		plan := payload["plan"].(map[string]any)
		if plan["outputDir"] != outputDir || plan["target"] != "local" || plan["format"] != "mp4" {
			t.Fatalf("unexpected flow plan: %#v", plan)
		}
		flow := plan["flow"].(map[string]any)
		start := flow["start"].(map[string]any)
		if start["url"] != "http://127.0.0.1:4444/" || start["service"] != "web" {
			t.Fatalf("flow plan should resolve start page against local target: %#v", start)
		}
		variants := plan["variants"].([]any)
		variant := variants[0].(map[string]any)
		if variant["name"] != "desktop-dark" || variant["colorScheme"] != "dark" {
			t.Fatalf("variant contract not passed to runner: %#v", variant)
		}
		if err := os.MkdirAll(filepath.Dir(webmPath), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{webmPath, screenshotPath, consolePath} {
			if err := os.WriteFile(path, []byte("artifact"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return []byte(fmt.Sprintf(`{
  "ok": true,
  "flow": {"name":"checkout-review"},
  "variants": [{
    "name":"desktop-dark",
    "ok": true,
    "artifacts": {
      "video": {"path":%q,"format":"webm"},
      "screenshots": [{"name":"loaded","path":%q}],
      "console": %q
    }
  }],
  "artifacts": {
    "videos": [{"path":%q,"format":"webm"}],
    "screenshots": [{"name":"loaded","path":%q}],
    "console": [%q]
  }
}`, webmPath, screenshotPath, consolePath, webmPath, screenshotPath, consolePath)), nil, nil
	}}
	a.qaRecordRunner = runner

	result, err := a.EvidenceFlow("qa-pr", EvidenceFlowOptions{StepsFile: stepsFile, OutputDir: outputDir, Target: "local", VideoSet: true, Video: true})
	if err != nil {
		t.Fatalf("EvidenceFlow failed: %v", err)
	}
	if result["ok"] != true || result["outputDir"] != outputDir || result["format"] != "mp4" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(runner.runs) != 1 || len(runner.combined) != 1 {
		t.Fatalf("expected one playwright run and one ffmpeg conversion, runs=%#v combined=%#v", runner.runs, runner.combined)
	}
	mp4Path := strings.TrimSuffix(webmPath, filepath.Ext(webmPath)) + ".mp4"
	artifacts := result["artifacts"].(map[string]any)
	videos := artifacts["videos"].([]any)
	video := videos[0].(map[string]any)
	if video["path"] != mp4Path || video["sourcePath"] != webmPath || video["format"] != "mp4" {
		t.Fatalf("video artifact should be converted in response: %#v", video)
	}
	resultPath := result["resultPath"].(string)
	reportPath := result["reportPath"].(string)
	if _, err := os.Stat(resultPath); err != nil {
		t.Fatalf("result artifact should exist: %v", err)
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("report artifact should exist: %v", err)
	}
	persisted := readJSONFile[map[string]any](t, resultPath)
	if persisted["resultPath"] != resultPath || persisted["reportPath"] != reportPath {
		b, _ := json.MarshalIndent(persisted, "", "  ")
		t.Fatalf("result artifact should include its artifact paths, got %s", b)
	}
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"checkout-review", mp4Path, screenshotPath, consolePath} {
		if !strings.Contains(string(report), want) {
			t.Fatalf("report should include %q:\n%s", want, report)
		}
	}
}

func TestEvidenceFlowCommandReturnsNonZeroWithFailurePayload(t *testing.T) {
	t.Setenv("VIVERO_PLAYWRIGHT_PACKAGE", "")
	a, _ := newQARecordTestApp(t)
	defer a.Close()

	stepsFile := filepath.Join(t.TempDir(), "flow.json")
	if err := os.WriteFile(stepsFile, []byte(`{"name":"bad-flow","actions":[{"expectText":"Never shown"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(t.TempDir(), "flow-artifacts")
	a.qaRecordRunner = &fakeQARecordRunner{runFunc: func(name string, args ...string) ([]byte, []byte, error) {
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			t.Fatal(err)
		}
		return []byte(`{"ok":false,"flow":{"name":"bad-flow"},"variants":[{"name":"default","ok":false,"errors":["expected text not found"],"artifacts":{"screenshots":[]}}],"artifacts":{"screenshots":[]}}`), nil, nil
	}}

	var stdout, stderr bytes.Buffer
	code := a.runEvidence([]string{"flow", "preview:qa-pr", "--steps-file", stepsFile, "--target", "local", "--out", outputDir, "--json", "--no-input"}, &stdout, &stderr, true)
	if code == 0 || stderr.Len() != 0 {
		t.Fatalf("failed evidence flow should return non-zero with JSON on stdout, code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	payload := decodeJSONMap(t, stdout.String())
	assertEvidenceShape(t, payload)
	if payload["ok"] != false || payload["resultPath"] == "" {
		t.Fatalf("failure payload should be preserved on stdout with artifact paths: %#v", payload)
	}
	if _, err := os.Stat(payload["resultPath"].(string)); err != nil {
		t.Fatalf("failure result artifact should be written: %v", err)
	}
}

func TestEvidenceFlowPlanCoversEndpointActionsDefaultsAndTypedValues(t *testing.T) {
	a := &App{Home: t.TempDir()}
	preview := PreviewRecord{
		ID:      "pr-typed",
		Project: "demo",
		Services: map[string]PreviewService{
			"api": {Name: "api", ProxyURL: "http://127.0.0.1:9001", URL: "https://preview.example.com/api"},
			"web": {Name: "web", ProxyURL: "http://127.0.0.1:9000", URL: "https://preview.example.com"},
		},
	}
	agent := AgentConfig{CommonPages: map[string]AgentPage{
		"home": {Service: "web", Path: "/"},
		"blog": {Service: "web", Path: "blog"},
	}}
	spec := map[string]any{
		"start": "home",
		"steps": []any{
			map[string]any{"visit": "blog"},
			map[string]any{"goto": map[string]any{"service": "api", "path": "health"}},
			map[string]any{"visit": "https://external.example.com/path"},
		},
		"variants": []map[string]any{{
			"width":             "390",
			"height":            float32(844),
			"mobile":            "on",
			"deviceScaleFactor": json.Number("3"),
			"colorScheme":       "light",
			"storageState":      "~/state.json",
		}},
		"record": map[string]any{
			"video":       "yes",
			"screenshots": "off",
			"console":     "false",
			"network":     "on",
		},
	}
	plan, err := a.evidenceFlowPlan(preview, agent, spec, EvidenceFlowOptions{Target: "local", Width: 1440, Height: 1000, DeviceScaleFactor: 1, Format: "mp4"})
	if err != nil {
		t.Fatalf("evidenceFlowPlan failed: %v", err)
	}
	flow := plan["flow"].(map[string]any)
	start := flow["start"].(map[string]any)
	if start["url"] != "http://127.0.0.1:9000/" || start["name"] != "home" || start["service"] != "web" {
		t.Fatalf("start did not resolve common page: %#v", start)
	}
	actions := flow["actions"].([]any)
	if len(actions) != 3 {
		t.Fatalf("expected 3 actions, got %#v", actions)
	}
	visitBlog := actions[0].(map[string]any)["visit"].(map[string]any)
	if visitBlog["url"] != "http://127.0.0.1:9000/blog" || visitBlog["path"] != "/blog" {
		t.Fatalf("visit common page not resolved: %#v", visitBlog)
	}
	gotoAPI := actions[1].(map[string]any)["goto"].(map[string]any)
	if gotoAPI["url"] != "http://127.0.0.1:9001/health" || gotoAPI["service"] != "api" {
		t.Fatalf("goto service/path not resolved: %#v", gotoAPI)
	}
	visitExternal := actions[2].(map[string]any)["visit"].(map[string]any)
	if visitExternal["url"] != "https://external.example.com/path" {
		t.Fatalf("absolute visit not preserved: %#v", visitExternal)
	}
	variants := plan["variants"].([]map[string]any)
	variant := variants[0]
	viewport := variant["viewport"].(map[string]any)
	if variant["name"] != "390x844-light" || viewport["width"] != 390 || viewport["height"] != 844 || variant["isMobile"] != true || variant["deviceScaleFactor"] != float64(3) {
		t.Fatalf("typed variant values not normalized: %#v", variant)
	}
	if got := stringValue(variant["storageState"]); !strings.HasSuffix(got, "/state.json") || strings.Contains(got, "~") {
		t.Fatalf("storage state should be expanded, got %q", got)
	}
	record := plan["record"].(map[string]any)
	if record["video"] != true || record["screenshots"] != false || record["console"] != false || record["network"] != true {
		t.Fatalf("record booleans not normalized: %#v", record)
	}
}

func TestEvidenceFlowHelpersCoverFallbacksAndYAMLShapes(t *testing.T) {
	m, ok := evidenceFlowMap(map[any]any{"one": []any{map[any]any{"two": "2"}}})
	if !ok || m["one"].([]any)[0].(map[string]any)["two"] != "2" {
		t.Fatalf("map[any]any should normalize recursively: %#v", m)
	}
	slice, ok := evidenceFlowSlice([]map[string]any{{"name": "a"}, {"name": "b"}})
	if !ok || len(slice) != 2 {
		t.Fatalf("[]map[string]any should be accepted: %#v", slice)
	}
	if got := firstAnyValue(map[string]any{"a": nil, "b": "bee"}, "missing", "b"); got != "bee" {
		t.Fatalf("firstAnyValue returned %#v", got)
	}
	if evidenceFlowBool("nope", true) != true || evidenceFlowBool("0", true) != false || evidenceFlowBool("YES", false) != true {
		t.Fatalf("boolean parser fallback/string cases failed")
	}
	if evidenceFlowInt(json.Number("12"), 0) != 12 || evidenceFlowInt("bad", 7) != 7 || evidenceFlowInt(int64(4), 0) != 4 {
		t.Fatalf("integer parser cases failed")
	}
	if evidenceFlowFloat(json.Number("1.5"), 0) != 1.5 || evidenceFlowFloat("bad", 2.5) != 2.5 || evidenceFlowFloat(int64(3), 0) != 3 {
		t.Fatalf("float parser cases failed")
	}
	if evidenceFlowAbsoluteURL("/local") || !evidenceFlowAbsoluteURL("https://example.com") {
		t.Fatalf("absolute URL parser cases failed")
	}
}
