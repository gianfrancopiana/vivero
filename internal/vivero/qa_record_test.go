package vivero

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type qaRecordCommandCall struct {
	Name string
	Args []string
}

type fakeQARecordRunner struct {
	lookPathErrs map[string]error
	runFunc      func(name string, args ...string) ([]byte, []byte, error)
	combinedFunc func(name string, args ...string) ([]byte, error)
	lookups      []string
	runs         []qaRecordCommandCall
	combined     []qaRecordCommandCall
}

func (f *fakeQARecordRunner) LookPath(file string) (string, error) {
	f.lookups = append(f.lookups, file)
	if err := f.lookPathErrs[file]; err != nil {
		return "", err
	}
	return "/fake/bin/" + file, nil
}

func (f *fakeQARecordRunner) Run(name string, args ...string) ([]byte, []byte, error) {
	f.runs = append(f.runs, qaRecordCommandCall{Name: name, Args: append([]string(nil), args...)})
	if f.runFunc != nil {
		return f.runFunc(name, args...)
	}
	return []byte(`{"ok":true,"videos":[]}`), nil, nil
}

func (f *fakeQARecordRunner) CombinedOutput(name string, args ...string) ([]byte, error) {
	f.combined = append(f.combined, qaRecordCommandCall{Name: name, Args: append([]string(nil), args...)})
	if f.combinedFunc != nil {
		return f.combinedFunc(name, args...)
	}
	return nil, nil
}

func TestQARecordWebMUsesInjectableRunnerAndWritesArtifact(t *testing.T) {
	a, projectRoot := newQARecordTestApp(t)
	defer a.Close()

	runner := &fakeQARecordRunner{runFunc: func(name string, args ...string) ([]byte, []byte, error) {
		if name != "npm" {
			t.Fatalf("record runner command = %s; want npm", name)
		}
		if len(args) < 4 || !reflect.DeepEqual(args[:4], []string{"exec", "--yes", "--package", "playwright"}) {
			t.Fatalf("npm args start = %#v", args)
		}
		inputPath := args[len(args)-1]
		payload := readJSONFile[map[string]any](t, inputPath)
		outputDir := payload["outputDir"].(string)
		if !strings.Contains(outputDir, filepath.Join("qa-artifacts", "qa-pr", "smoke", "videos")) {
			t.Fatalf("outputDir = %s; want QA video artifact dir", outputDir)
		}
		options := payload["options"].(map[string]any)
		if options["format"] != "webm" || options["colorScheme"] != "dark" {
			t.Fatalf("record options not passed to runner: %#v", options)
		}
		videoPath := filepath.Join(outputDir, "smoke", "homepage.webm")
		return []byte(fmt.Sprintf(`{"ok":true,"videos":[{"path":%q,"format":"webm"}]}`, videoPath)), nil, nil
	}}
	a.qaRecordRunner = runner

	result, err := a.QARecord("qa-pr", QARecordOptions{Scope: "smoke", Format: "webm", ColorScheme: "dark"})
	if err != nil {
		t.Fatal(err)
	}
	if result["preview"] != "qa-pr" || result["scope"] != "smoke" || result["format"] != "webm" || result["colorScheme"] != "dark" {
		t.Fatalf("result metadata = %#v", result)
	}
	if len(runner.lookups) != 1 || runner.lookups[0] != "npm" {
		t.Fatalf("lookups = %#v; want npm", runner.lookups)
	}
	if stringSliceContains(runner.lookups, "ffmpeg") {
		t.Fatalf("webm recording should not require ffmpeg: %#v", runner.lookups)
	}
	if len(runner.runs) != 1 || len(runner.combined) != 0 {
		t.Fatalf("runner calls = runs %#v combined %#v", runner.runs, runner.combined)
	}
	recordPath := result["recordPath"].(string)
	if !strings.HasPrefix(recordPath, filepath.Join(projectRoot, "qa-artifacts")) {
		t.Fatalf("recordPath = %s; want project artifact root", recordPath)
	}
	written := readJSONFile[map[string]any](t, recordPath)
	if written["preview"] != "qa-pr" || written["format"] != "webm" {
		t.Fatalf("record artifact metadata = %#v", written)
	}
}

func TestQARecordMP4ConversionUsesInjectableRunner(t *testing.T) {
	a, _ := newQARecordTestApp(t)
	defer a.Close()
	webm := filepath.Join(t.TempDir(), "homepage.webm")

	runner := &fakeQARecordRunner{runFunc: func(name string, args ...string) ([]byte, []byte, error) {
		return []byte(fmt.Sprintf(`{"ok":true,"videos":[{"path":%q,"format":"webm"}]}`, webm)), nil, nil
	}}
	a.qaRecordRunner = runner

	result, err := a.QARecord("qa-pr", QARecordOptions{Scope: "smoke", Format: "mp4"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.lookups, []string{"npm", "ffmpeg"}) {
		t.Fatalf("lookups = %#v; want npm then ffmpeg", runner.lookups)
	}
	if len(runner.combined) != 1 || runner.combined[0].Name != "ffmpeg" {
		t.Fatalf("ffmpeg conversion call missing: %#v", runner.combined)
	}
	mp4 := strings.TrimSuffix(webm, filepath.Ext(webm)) + ".mp4"
	wantArgs := []string{"-y", "-i", webm, "-movflags", "+faststart", "-pix_fmt", "yuv420p", mp4}
	if !reflect.DeepEqual(runner.combined[0].Args, wantArgs) {
		t.Fatalf("ffmpeg args did not include source and mp4 target: %#v", runner.combined[0].Args)
	}
	videos := result["videos"].([]any)
	video := videos[0].(map[string]any)
	if video["sourcePath"] != webm || video["path"] != mp4 || video["format"] != "mp4" {
		t.Fatalf("converted video metadata = %#v", video)
	}
}

func TestQARecordMissingNPMReturnsActionableErrorWithoutRunningPlaywright(t *testing.T) {
	a, _ := newQARecordTestApp(t)
	defer a.Close()
	runner := &fakeQARecordRunner{lookPathErrs: map[string]error{"npm": errors.New("not found")}}
	a.qaRecordRunner = runner

	_, err := a.QARecord("qa-pr", QARecordOptions{Scope: "smoke", Format: "webm"})
	if err == nil || !strings.Contains(err.Error(), "npm/playwright not available") {
		t.Fatalf("error = %v; want npm/playwright not available", err)
	}
	if len(runner.runs) != 0 || len(runner.combined) != 0 {
		t.Fatalf("missing npm should not invoke commands: runs %#v combined %#v", runner.runs, runner.combined)
	}
}

func TestQARecordBadPlaywrightJSONIncludesRunnerOutput(t *testing.T) {
	a, _ := newQARecordTestApp(t)
	defer a.Close()
	runner := &fakeQARecordRunner{runFunc: func(name string, args ...string) ([]byte, []byte, error) {
		return []byte("not-json"), nil, nil
	}}
	a.qaRecordRunner = runner

	_, err := a.QARecord("qa-pr", QARecordOptions{Scope: "smoke", Format: "webm"})
	if err == nil || !strings.Contains(err.Error(), "parse qa record output") || !strings.Contains(err.Error(), "not-json") {
		t.Fatalf("error = %v; want parse error with runner output", err)
	}
}

func newQARecordTestApp(t *testing.T) (*App, string) {
	t.Helper()
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := t.TempDir()
	_, err = a.saveProject(projectRoot, ProjectConfig{
		Project:  ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{"web": {Port: 3000}},
		Agent: AgentConfig{
			DefaultPreviewService: "web",
			CommonPages: map[string]AgentPage{
				"home": {Service: "web", Path: "/"},
			},
			QA: QAConfig{
				DefaultScope: "smoke",
				ArtifactRoot: "qa-artifacts",
				Scopes:       []QAScope{{Name: "smoke", Pages: []string{"home"}}},
			},
		},
	})
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "qa-pr", Project: "demo", Status: "running"}); err != nil {
		a.Close()
		t.Fatal(err)
	}
	if err := a.saveService("qa-pr", PreviewService{Name: "web", Status: "healthy", OriginURL: "http://127.0.0.1:3000", ProxyURL: "http://127.0.0.1:4444"}); err != nil {
		a.Close()
		t.Fatal(err)
	}
	return a, projectRoot
}

func readJSONFile[T any](t *testing.T, path string) T {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("read %s: %v\n%s", path, err, b)
	}
	return out
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
