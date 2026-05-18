package vivero

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestContainerPreviewLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	installFakeDocker(t)
	root := t.TempDir()
	source := filepath.Join(root, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("hello vivero"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(source, nil, "git", "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	_, _ = runCmd(source, nil, "git", "config", "user.email", "test@example.com")
	_, _ = runCmd(source, nil, "git", "config", "user.name", "Test")
	if out, err := runCmd(source, nil, "git", "add", "."); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	if out, err := runCmd(source, nil, "git", "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	port := freePort(t)
	cfg := []byte(`project:
  name: static-site
sources:
  app:
    path: ` + source + `
services:
  web:
    source: app
    runtime: docker
    image: python:3.12-alpine
    command: python3 -m http.server ` + strconv.Itoa(port) + ` --bind 0.0.0.0
    port: ` + strconv.Itoa(port) + `
    originHost: localhost
    health:
      path: /index.html
      expectStatus: 200
      timeout: 20s
setup:
  afterSeeds:
    - service: web
      command: printf setup > setup.txt
agent:
  defaultPreviewService: web
  screenshotBreakpoints:
    - name: desktop
      width: 1280
      height: 720
  commonPages:
    home:
      service: web
      path: /index.html
  smokeTests:
    - name: homepage
      service: web
      path: /index.html
      expectStatus: 200
  qa:
    defaultScope: smoke
    artifactRoot: qa-artifacts
    driver:
      preferred: playwright
      evidence: playwright
      exploratory: chrome-mcp
      allowed: [playwright, browser, chrome-mcp]
    scopes:
      - name: smoke
        description: Verify the static homepage behaves like a running preview.
        pages: [home]
        checks:
          - name: no-console-errors
            category: browser
            severity: high
            method: browser-console
        flows:
          - name: homepage-loads
            start: home
            steps:
              - visit: home
              - expectText: hello vivero
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}
	preview, err := a.Up(UpRequest{Project: "static-site", ID: "test-static", Wait: true, Timeout: 20 * time.Second})
	defer a.Down("test-static", "discard")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != "running" {
		t.Fatalf("status = %s", preview.Status)
	}
	if preview.Services["web"].URL == "" {
		t.Fatal("expected URL")
	}
	if preview.Services["web"].Runtime != "docker" || preview.Services["web"].ContainerID == "" {
		t.Fatalf("expected docker service with container id: %#v", preview.Services["web"])
	}
	if preview.Services["web"].OriginURL != fmt.Sprintf("http://localhost:%d", port) {
		t.Fatalf("origin URL = %s", preview.Services["web"].OriginURL)
	}
	if got, err := os.ReadFile(filepath.Join(source, "setup.txt")); err != nil || string(got) != "setup" {
		t.Fatalf("setup afterSeeds did not run in service workdir: %q %v", got, err)
	}
	smoke, err := a.Smoke("test-static", "homepage")
	if err != nil {
		t.Fatal(err)
	}
	if smoke["ok"] != true {
		t.Fatalf("smoke failed: %#v", smoke)
	}
	qaPlan, err := a.QAPlan("test-static", "")
	if err != nil {
		t.Fatal(err)
	}
	scopes := qaPlan["scopes"].([]map[string]any)
	if len(scopes) != 1 || scopes[0]["name"] != "smoke" {
		t.Fatalf("unexpected qa scopes: %#v", scopes)
	}
	pages := scopes[0]["pages"].([]map[string]any)
	if len(pages) != 1 || pages[0]["url"] == "" || pages[0]["service"] != "web" {
		t.Fatalf("unexpected qa pages: %#v", pages)
	}
	if artifacts := qaPlan["artifacts"].(map[string]any); !strings.Contains(artifacts["dir"].(string), filepath.Join(root, "qa-artifacts", "test-static", "smoke")) {
		t.Fatalf("unexpected qa artifact dir: %#v", artifacts)
	}
	if driver := qaPlan["driver"].(map[string]any); driver["evidence"] != "playwright" || driver["exploratory"] != "chrome-mcp" {
		t.Fatalf("unexpected qa driver split: %#v", driver)
	}
	qaReport, err := a.QAReport("test-static", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(qaReport["path"].(string)); err != nil {
		t.Fatalf("qa report not written: %v", err)
	}
	controlPlane := httptest.NewServer(a.controlPlaneHandler())
	defer controlPlane.Close()
	resp, err := http.Get(controlPlane.URL + "/previews/test-static/qa?scope=smoke")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("qa plan HTTP status = %d", resp.StatusCode)
	}
	var httpPlan map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&httpPlan); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if previewMap := httpPlan["preview"].(map[string]any); previewMap["id"] != "test-static" {
		t.Fatalf("unexpected qa HTTP preview: %#v", previewMap)
	}
	runResp, err := http.Post(controlPlane.URL+"/previews/test-static/qa/run", "application/json", strings.NewReader(`{"scope":"smoke","noScreenshots":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if runResp.StatusCode != http.StatusOK {
		t.Fatalf("qa run HTTP status = %d", runResp.StatusCode)
	}
	var httpRun map[string]any
	if err := json.NewDecoder(runResp.Body).Decode(&httpRun); err != nil {
		t.Fatal(err)
	}
	_ = runResp.Body.Close()
	if httpRun["ok"] != true || httpRun["runPath"] == "" {
		t.Fatalf("unexpected qa run HTTP response: %#v", httpRun)
	}
	newFile := filepath.Join(root, "new.html")
	if err := os.WriteFile(newFile, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncFile("test-static", "app", "index.html", newFile); err != nil {
		t.Fatal(err)
	}
	diff, err := a.Diff("test-static", "app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff["diff"].(string), "changed") {
		t.Fatalf("diff missing change: %#v", diff)
	}
	logs, err := a.Logs("test-static", "web", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs["lines"].([]string)) == 0 {
		t.Fatal("expected logs")
	}
}

func TestContainerPreviewProfilesSelectServicesBackingSourcesAndSmoke(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	installFakeDocker(t)
	root := t.TempDir()
	helper := filepath.Join(root, "helper")
	if err := os.MkdirAll(helper, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("hello app"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(helper, "index.html"), []byte("hello helper"), 0o644); err != nil {
		t.Fatal(err)
	}
	appPort := freePort(t)
	helperPort := freePort(t)
	cfg := []byte(`project:
  name: profiled-site
sources:
  app:
    path: .
  helper:
    path: helper
backingServices:
  redis:
    image: busybox:latest
    command: sleep 60
  mailhog:
    image: busybox:latest
    command: sleep 60
services:
  web:
    source: app
    runtime: docker
    image: python:3.12-alpine
    command: python3 -m http.server ` + strconv.Itoa(appPort) + ` --bind 0.0.0.0
    port: ` + strconv.Itoa(appPort) + `
    health:
      path: /index.html
      expectStatus: 200
      timeout: 20s
  helper-web:
    source: helper
    runtime: docker
    image: python:3.12-alpine
    command: python3 -m http.server ` + strconv.Itoa(helperPort) + ` --bind 0.0.0.0
    port: ` + strconv.Itoa(helperPort) + `
    health:
      path: /index.html
      expectStatus: 200
      timeout: 20s
setup:
  afterSeeds:
    - service: web
      command: printf web > web-setup.txt
    - service: helper-web
      command: printf helper > helper-setup.txt
agent:
  defaultPreviewService: web
  commonPages:
    home:
      service: web
      path: /index.html
    helper:
      service: helper-web
      path: /index.html
  smokeTests:
    - name: homepage
      service: web
      path: /index.html
      expectStatus: 200
    - name: helper-homepage
      service: helper-web
      path: /index.html
      expectStatus: 200
profiles:
  default:
    services: [web]
    backingServices: [redis]
    smokeTests: [homepage]
  helper:
    services: [web, helper-web]
    backingServices: [redis, mailhog]
    smokeTests: [homepage, helper-homepage]
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}
	preview, err := a.Up(UpRequest{Project: "profiled-site", ID: "profile-default", Wait: true, Timeout: 20 * time.Second})
	defer a.Down("profile-default", "discard")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Profile != "default" {
		t.Fatalf("default profile = %q", preview.Profile)
	}
	if _, ok := preview.Services["web"]; !ok {
		t.Fatal("default profile should start web")
	}
	if _, ok := preview.Services["redis"]; !ok {
		t.Fatal("default profile should start redis")
	}
	for _, absent := range []string{"helper-web", "mailhog"} {
		if _, ok := preview.Services[absent]; ok {
			t.Fatalf("default profile should not start %s", absent)
		}
	}
	if _, ok := preview.Sources["helper"]; ok {
		t.Fatal("default profile should not resolve inactive helper source")
	}
	if _, err := os.Stat(filepath.Join(root, "helper", "helper-setup.txt")); !os.IsNotExist(err) {
		t.Fatalf("default profile should not run helper setup, stat err=%v", err)
	}
	smoke, err := a.Smoke("profile-default", "")
	if err != nil {
		t.Fatal(err)
	}
	results := smoke["results"].([]map[string]any)
	if len(results) != 1 || results[0]["name"] != "homepage" || results[0]["ok"] != true {
		t.Fatalf("default profile smoke should only run homepage: %#v", results)
	}
	qaPlan, err := a.QAPlan("profile-default", "")
	if err != nil {
		t.Fatal(err)
	}
	if tests := qaPlan["smokeTests"].([]SmokeTest); len(tests) != 1 || tests[0].Name != "homepage" {
		t.Fatalf("default profile QA should only include homepage smoke: %#v", tests)
	}
	if _, err := a.Down("profile-default", "discard"); err != nil {
		t.Fatal(err)
	}

	helperPreview, err := a.Up(UpRequest{Project: "profiled-site", ID: "profile-helper", Profile: "helper", Wait: true, Timeout: 20 * time.Second})
	defer a.Down("profile-helper", "discard")
	if err != nil {
		t.Fatal(err)
	}
	if helperPreview.Profile != "helper" {
		t.Fatalf("helper profile = %q", helperPreview.Profile)
	}
	for _, present := range []string{"web", "helper-web", "redis", "mailhog"} {
		if _, ok := helperPreview.Services[present]; !ok {
			t.Fatalf("helper profile should start %s", present)
		}
	}
	if _, ok := helperPreview.Sources["helper"]; !ok {
		t.Fatal("helper profile should resolve helper source")
	}
	helperSmoke, err := a.Smoke("profile-helper", "")
	if err != nil {
		t.Fatal(err)
	}
	helperResults := helperSmoke["results"].([]map[string]any)
	if len(helperResults) != 2 {
		t.Fatalf("helper profile smoke should run both tests: %#v", helperResults)
	}
}

func TestContainerPreviewStartsBackingServicesBeforeSetupAndApps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	installFakeDocker(t)
	root := t.TempDir()
	source := filepath.Join(root, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("hello backing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(source, nil, "git", "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	_, _ = runCmd(source, nil, "git", "config", "user.email", "test@example.com")
	_, _ = runCmd(source, nil, "git", "config", "user.name", "Test")
	if out, err := runCmd(source, nil, "git", "add", "."); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	if out, err := runCmd(source, nil, "git", "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	port := freePort(t)
	cfg := []byte(`project:
  name: backing-site
sources:
  app:
    path: ` + source + `
backingServices:
  redis:
    image: python:3.12-alpine
    command: python3 -c "import time; time.sleep(60)"
    health:
      command: python3 -c "print('ok')"
      timeout: 5s
services:
  web:
    source: app
    runtime: docker
    image: python:3.12-alpine
    command: python3 -m http.server ` + strconv.Itoa(port) + ` --bind 0.0.0.0
    port: ` + strconv.Itoa(port) + `
    originHost: localhost
    health:
      path: /index.html
      expectStatus: 200
      timeout: 20s
setup:
  afterSeeds:
    - service: web
      command: printf setup > setup.txt
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}
	preview, err := a.Up(UpRequest{Project: "backing-site", ID: "test-backing", Wait: true, Timeout: 20 * time.Second})
	defer a.Down("test-backing", "discard")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Services["redis"].ContainerID == "" || preview.Services["redis"].Status != "healthy" {
		t.Fatalf("backing service not healthy: %#v", preview.Services["redis"])
	}
	if preview.Services["web"].ContainerID == "" {
		t.Fatalf("web service missing container: %#v", preview.Services["web"])
	}
	if got, err := os.ReadFile(filepath.Join(source, "setup.txt")); err != nil || string(got) != "setup" {
		t.Fatalf("setup afterSeeds did not run in service workdir: %q %v", got, err)
	}
}

func TestBuildServiceImagesUsesServiceSourceAsBuildContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	projectRoot := t.TempDir()
	sourceRoot := filepath.Join(t.TempDir(), "gumroad")
	if err := os.MkdirAll(filepath.Join(sourceRoot, "docker", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "docker", "web", "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "gumroad-main"},
		Services: map[string]ServiceConfig{
			"gumroad-web": {
				Source: "gumroad",
				Build:  ImageBuildConfig{Context: ".", Dockerfile: "docker/web/Dockerfile", Tag: "vivero/gumroad-web:test"},
			},
		},
	}
	if err := a.buildServiceImages(ProjectRecord{Name: "gumroad-main", Path: projectRoot, Config: cfg}, "gumroad-pr-1", map[string]PreviewSource{
		"gumroad": {Name: "gumroad", Path: sourceRoot},
	}, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services["gumroad-web"].Image; got != "vivero/gumroad-web:test" {
		t.Fatalf("built image not written back to service config: %q", got)
	}
	builds, err := os.ReadFile(filepath.Join(os.Getenv("FAKE_DOCKER_STATE"), "builds"))
	if err != nil {
		t.Fatal(err)
	}
	want := "vivero/gumroad-web:test|" + filepath.Join(sourceRoot, "docker", "web", "Dockerfile") + "|" + sourceRoot
	if !strings.Contains(string(builds), want) {
		t.Fatalf("build should be rooted at the resolved service source; want %q in %q", want, builds)
	}
}

func TestSecretsAreWriteOnlyByList(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := writeEnvFile(a.secretFile("demo"), map[string]string{"TOKEN": "super-secret"}); err != nil {
		t.Fatal(err)
	}
	m, err := readEnvFile(a.secretFile("demo"))
	if err != nil {
		t.Fatal(err)
	}
	if m["TOKEN"] != "super-secret" {
		t.Fatal("secret round-trip failed")
	}
	keys := keysOf(m)
	if len(keys) != 1 || keys[0] != "TOKEN" {
		t.Fatalf("bad keys: %#v", keys)
	}
}

func TestServiceHealthTimeoutUsesFallbackAsMinimum(t *testing.T) {
	if got := serviceHealthTimeout(HealthConfig{}, 3*time.Minute); got != 3*time.Minute {
		t.Fatalf("empty timeout = %s", got)
	}
	if got := serviceHealthTimeout(HealthConfig{Timeout: "2m"}, 3*time.Minute); got != 3*time.Minute {
		t.Fatalf("short timeout = %s", got)
	}
	if got := serviceHealthTimeout(HealthConfig{Timeout: "5m"}, 3*time.Minute); got != 5*time.Minute {
		t.Fatalf("long timeout = %s", got)
	}
	if got := serviceHealthTimeout(HealthConfig{Timeout: "not-a-duration"}, 3*time.Minute); got != 3*time.Minute {
		t.Fatalf("invalid timeout = %s", got)
	}
}

func TestQuickTunnelArgsIncludesHostHeader(t *testing.T) {
	args := quickTunnelArgs("http://localhost:3310", "localhost")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--http-host-header localhost") {
		t.Fatalf("missing host header arg: %v", args)
	}
}

func TestQuickTunnelLogPollingIgnoresOldURLs(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "cloudflared.log")
	old := "old https://old-url.trycloudflare.com\n"
	if err := os.WriteFile(logPath, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.WriteString("new https://new-url.trycloudflare.com\n")
	}()
	got, err := waitForQuickTunnelURL(logPath, int64(len(old)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://new-url.trycloudflare.com" {
		t.Fatalf("url = %s", got)
	}
}

func TestHeaderRewriteProxySendsHostHeader(t *testing.T) {
	target, err := url.Parse("http://localhost:3310")
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHeaderRewriteProxy(target, "localhost", PublicRewriteConfig{})
	req := httptest.NewRequest("GET", "https://preview.trycloudflare.com/path", nil)
	req.Header.Set("X-Forwarded-Host", "preview.trycloudflare.com")
	proxy.Director(req)
	if req.Host != "localhost" {
		t.Fatalf("Host = %s", req.Host)
	}
	if got := req.Header.Get("X-Forwarded-Host"); got != "localhost" {
		t.Fatalf("X-Forwarded-Host = %s", got)
	}
	if req.URL.Host != "localhost:3310" {
		t.Fatalf("target host = %s", req.URL.Host)
	}
}

func TestPublicPreviewRewriterRewritesURLEncodedOrigins(t *testing.T) {
	target, err := url.Parse("http://localhost:3310")
	if err != nil {
		t.Fatal(err)
	}
	rewriter := newPublicPreviewRewriter(target, "localhost", PublicRewriteConfig{})
	body := `<a href="/login?next=https%3A%2F%2Flocalhost%2F">Log in</a>`
	got := rewriter.rewrite(body, "https://preview.trycloudflare.com")
	if strings.Contains(got, "localhost") || strings.Contains(strings.ToLower(got), "localhost") {
		t.Fatalf("body still leaks localhost: %s", got)
	}
	if !strings.Contains(got, "next=https%3A%2F%2Fpreview.trycloudflare.com%2F") {
		t.Fatalf("encoded next URL not rewritten: %s", got)
	}
}

func TestPublicPreviewRewriterRewritesProtocolRelativeDevHosts(t *testing.T) {
	target, err := url.Parse("http://localhost:3310")
	if err != nil {
		t.Fatal(err)
	}
	rewriter := newPublicPreviewRewriter(target, "localhost", PublicRewriteConfig{Hosts: []string{"app.localhost:3000"}})
	body := `<link rel="dns-prefetch" href="//app.localhost"><link rel="dns-prefetch" href="//localhost">`
	got := rewriter.rewrite(body, "https://preview.trycloudflare.com")
	if strings.Contains(got, "localhost") {
		t.Fatalf("body still leaks localhost: %s", got)
	}
	if strings.Count(got, "//preview.trycloudflare.com") != 2 {
		t.Fatalf("protocol-relative hosts not rewritten: %s", got)
	}
}

func TestHeaderRewriteProxyRewritesRequestOriginForMaskedHost(t *testing.T) {
	target, err := url.Parse("http://localhost:3310")
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHeaderRewriteProxy(target, "localhost", PublicRewriteConfig{})
	req := httptest.NewRequest("POST", "https://preview.trycloudflare.com/signup", nil)
	req.Host = "preview.trycloudflare.com"
	req.Header.Set("X-Forwarded-Host", "preview.trycloudflare.com")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://preview.trycloudflare.com")
	req.Header.Set("Referer", "https://preview.trycloudflare.com/signup")

	proxy.Director(req)

	if got := req.Header.Get("Origin"); got != "https://localhost" {
		t.Fatalf("Origin = %s", got)
	}
	if got := req.Header.Get("Referer"); got != "https://localhost/signup" {
		t.Fatalf("Referer = %s", got)
	}
	if req.Host != "localhost" {
		t.Fatalf("Host = %s", req.Host)
	}
	if got := req.Header.Get("X-Forwarded-Host"); got != "localhost" {
		t.Fatalf("X-Forwarded-Host = %s", got)
	}
}

func TestHeaderRewriteProxyTreatsTryCloudflareAsHTTPSWhenForwardedProtoIsHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Origin"); got != "https://localhost" {
			t.Fatalf("upstream Origin = %s", got)
		}
		if got := r.Header.Get("Referer"); got != "https://localhost/dashboard" {
			t.Fatalf("upstream Referer = %s", got)
		}
		if got := r.Header.Get("X-Forwarded-Proto"); got != "https" {
			t.Fatalf("upstream X-Forwarded-Proto = %s", got)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`data-page="{&quot;domain_settings&quot;:{&quot;scheme&quot;:&quot;http&quot;,&quot;app_domain&quot;:&quot;localhost:3000&quot;},&quot;navigation&quot;:{&quot;products_url&quot;:&quot;http:\/\/localhost:3000\/products&quot;}}"`))
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHeaderRewriteProxy(target, "localhost", PublicRewriteConfig{
		Hosts: []string{"localhost:3000"},
		Replacements: []PublicRewriteTemplate{
			{From: `&quot;scheme&quot;:&quot;http&quot;`, To: `&quot;scheme&quot;:&quot;{publicScheme}&quot;`},
		},
	})
	req := httptest.NewRequest("GET", "http://preview.trycloudflare.com/dashboard", nil)
	req.Host = "preview.trycloudflare.com"
	req.Header.Set("X-Forwarded-Host", "preview.trycloudflare.com")
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("Origin", "https://preview.trycloudflare.com")
	req.Header.Set("Referer", "https://preview.trycloudflare.com/dashboard")
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, disallowed := range []string{`&quot;scheme&quot;:&quot;http&quot;`, `http:\/\/preview.trycloudflare.com`, "http://preview.trycloudflare.com"} {
		if strings.Contains(body, disallowed) {
			t.Fatalf("body still contains %q: %s", disallowed, body)
		}
	}
	for _, expected := range []string{`&quot;scheme&quot;:&quot;https&quot;`, `https:\/\/preview.trycloudflare.com\/products`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("body missing %q: %s", expected, body)
		}
	}
}

func TestHeaderRewriteProxyRewritesDevOriginsToPublicOrigin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "localhost" {
			t.Fatalf("upstream Host = %s", r.Host)
		}
		if got := r.Header.Get("X-Forwarded-Host"); got != "localhost" {
			t.Fatalf("upstream X-Forwarded-Host = %s", got)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Link", `<http://app.localhost:3000/packs/css/design.css>; rel=preload; as=style`)
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' app.localhost:3000")
		_, _ = w.Write([]byte(`<link rel="stylesheet" href="http://app.localhost:3000/packs/css/design.css"><a href="http://localhost/products">Products</a>`))
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHeaderRewriteProxy(target, "localhost", PublicRewriteConfig{})
	req := httptest.NewRequest("GET", "http://preview.trycloudflare.com/", nil)
	req.Host = "preview.trycloudflare.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, disallowed := range []string{"http://app.localhost:3000", "http://localhost"} {
		if strings.Contains(body, disallowed) {
			t.Fatalf("body still contains %q: %s", disallowed, body)
		}
	}
	if !strings.Contains(body, `href="https://preview.trycloudflare.com/packs/css/design.css"`) {
		t.Fatalf("body was not rewritten to public origin: %s", body)
	}
	if got := rec.Header().Get("Link"); !strings.Contains(got, "https://preview.trycloudflare.com/packs/css/design.css") {
		t.Fatalf("Link header was not rewritten: %s", got)
	}
}

func TestHeaderRewriteProxyRewritesDevOriginsToLocalProxyOrigin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "localhost" {
			t.Fatalf("upstream Host = %s", r.Host)
		}
		if got := r.Header.Get("X-Forwarded-Host"); got != "localhost" {
			t.Fatalf("upstream X-Forwarded-Host = %s", got)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Link", `<http://app.localhost:3000/packs/css/design.css>; rel=preload; as=style`)
		_, _ = w.Write([]byte(`<link rel="stylesheet" href="http://app.localhost:3000/packs/css/design.css"><a href="http://localhost/products">Products</a>`))
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHeaderRewriteProxy(target, "localhost", PublicRewriteConfig{})
	req := httptest.NewRequest("GET", "http://127.0.0.1:64874/", nil)
	req.Host = "127.0.0.1:64874"
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, disallowed := range []string{"http://app.localhost:3000", "http://localhost"} {
		if strings.Contains(body, disallowed) {
			t.Fatalf("body still contains %q: %s", disallowed, body)
		}
	}
	if !strings.Contains(body, `href="http://127.0.0.1:64874/packs/css/design.css"`) {
		t.Fatalf("body was not rewritten to local proxy origin: %s", body)
	}
	if got := rec.Header().Get("Link"); !strings.Contains(got, "http://127.0.0.1:64874/packs/css/design.css") {
		t.Fatalf("Link header was not rewritten to local proxy origin: %s", got)
	}
}

func TestHeaderRewriteProxyAppliesConfiguredPublicRewrites(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`data-page="{&quot;domain_settings&quot;:{&quot;scheme&quot;:&quot;http&quot;,&quot;app_domain&quot;:&quot;localhost:3000&quot;,&quot;api_domain&quot;:&quot;api.localhost:3000&quot;},&quot;navigation&quot;:{&quot;products_url&quot;:&quot;http:\/\/app.localhost:3000\/products&quot;}}"`))
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHeaderRewriteProxy(target, "localhost", PublicRewriteConfig{
		Hosts: []string{"localhost:3000", "app.localhost:3000", "api.localhost:3000"},
		Replacements: []PublicRewriteTemplate{
			{From: `&quot;scheme&quot;:&quot;http&quot;`, To: `&quot;scheme&quot;:&quot;{publicScheme}&quot;`},
		},
	})
	req := httptest.NewRequest("GET", "http://preview.trycloudflare.com/dashboard", nil)
	req.Host = "preview.trycloudflare.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, disallowed := range []string{"localhost:3000", `&quot;scheme&quot;:&quot;http&quot;`, `http:\/\/preview.trycloudflare.com`} {
		if strings.Contains(body, disallowed) {
			t.Fatalf("body still contains %q: %s", disallowed, body)
		}
	}
	for _, expected := range []string{`&quot;scheme&quot;:&quot;https&quot;`, `&quot;app_domain&quot;:&quot;preview.trycloudflare.com&quot;`, `&quot;api_domain&quot;:&quot;preview.trycloudflare.com&quot;`, `https:\/\/preview.trycloudflare.com\/products`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("body missing %q: %s", expected, body)
		}
	}
}

func TestHeaderRewriteProxyRewritesDevSubdomainOriginsBeforeBareHostnames(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<input value="http://seller.localhost:3000/affiliates"><a href="http://seller.localhost:3000/l/demo">Demo</a>`))
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHeaderRewriteProxy(target, "localhost", PublicRewriteConfig{Hosts: []string{"localhost:3000"}})
	req := httptest.NewRequest("GET", "http://preview.trycloudflare.com/affiliates", nil)
	req.Host = "preview.trycloudflare.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, disallowed := range []string{"seller.localhost:3000", "seller.preview.trycloudflare.com", "http://preview.trycloudflare.com"} {
		if strings.Contains(body, disallowed) {
			t.Fatalf("body still contains %q: %s", disallowed, body)
		}
	}
	for _, expected := range []string{`value="https://preview.trycloudflare.com/affiliates"`, `href="https://preview.trycloudflare.com/l/demo"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("body missing %q: %s", expected, body)
		}
	}
}

func TestNormalizePublicHostSchemeHandlesEscapedURLs(t *testing.T) {
	input := strings.Join([]string{
		`http://preview.trycloudflare.com/products`,
		`http:\/\/preview.trycloudflare.com\/products`,
		`http:\\/\\/preview.trycloudflare.com\\/products`,
		`http:\\\\/\\\\/preview.trycloudflare.com\\\\/products`,
	}, "\n")

	got := normalizePublicHostScheme(input, "preview.trycloudflare.com", "https")

	for _, disallowed := range []string{
		`http://preview.trycloudflare.com`,
		`http:\/\/preview.trycloudflare.com`,
		`http:\\/\\/preview.trycloudflare.com`,
		`http:\\\\/\\\\/preview.trycloudflare.com`,
	} {
		if strings.Contains(got, disallowed) {
			t.Fatalf("output still contains %q: %s", disallowed, got)
		}
	}
	for _, expected := range []string{
		`https://preview.trycloudflare.com/products`,
		`https:\/\/preview.trycloudflare.com\/products`,
		`https:\\/\\/preview.trycloudflare.com\\/products`,
		`https:\\\\/\\\\/preview.trycloudflare.com\\\\/products`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("output missing %q: %s", expected, got)
		}
	}
}

func TestHeaderRewriteProxyInjectsRuntimeForClientGeneratedPublicLinks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Preview</title></head><body><script>document.body.innerHTML = '<a href="http://' + location.host + '/products">Products</a>';</script></body></html>`))
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHeaderRewriteProxy(target, "localhost", PublicRewriteConfig{})
	req := httptest.NewRequest("GET", "http://preview.trycloudflare.com/dashboard", nil)
	req.Host = "preview.trycloudflare.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "data-vivero-public-preview-runtime") {
		t.Fatalf("runtime normalizer was not injected: %s", body)
	}
	if !strings.Contains(body, `const publicOrigin="https://preview.trycloudflare.com"`) {
		t.Fatalf("runtime normalizer did not include the public origin: %s", body)
	}
	for _, expected := range []string{"window.fetch=function", "XMLHttpRequest.prototype.open=function", "navigator.sendBeacon", "input,textarea", "endsWith(\".\"+publicHost)", "el.value=fixedValue"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("runtime normalizer missing client request rewrite %q: %s", expected, body)
		}
	}
	if strings.Index(body, "data-vivero-public-preview-runtime") > strings.Index(body, "document.body.innerHTML") {
		t.Fatalf("runtime normalizer must load before client-side DOM mutations: %s", body)
	}
}

func TestHeaderRewriteProxyInjectsRuntimeWithCSPNonce(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-test-nonce-123'")
		_, _ = w.Write([]byte(`<html><head><title>Preview</title></head><body></body></html>`))
	}))
	defer upstream.Close()

	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHeaderRewriteProxy(target, "localhost", PublicRewriteConfig{})
	req := httptest.NewRequest("GET", "https://preview.trycloudflare.com/dashboard", nil)
	req.Host = "preview.trycloudflare.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `data-vivero-public-preview-runtime nonce="test-nonce-123"`) {
		t.Fatalf("runtime normalizer did not include CSP nonce: %s", body)
	}
}
