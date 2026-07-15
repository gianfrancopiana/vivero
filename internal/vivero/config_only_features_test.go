package vivero

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServePublicPreviewHonorsPublicPathRoutes(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "primary:"+r.URL.Path)
	}))
	defer primary.Close()
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "storage:"+r.URL.Path)
	}))
	defer storage.Close()

	projectCfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{
			"web": {
				Ports: map[string]PortConfig{
					"http":  {Container: 3000},
					"minio": {Container: 9000, PublicPath: "/gumroad-dev-public-storage", PublicOrigins: []string{"http://minio:9000"}},
				},
				PrimaryPort: "http",
			},
		},
	}
	if _, err := a.saveProject(t.TempDir(), projectCfg); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "demo-public", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("demo-public", PreviewService{
		Name:      "web",
		Status:    "healthy",
		URL:       "https://demo-public.previews.example.com",
		OriginURL: primary.URL,
		Ports: map[string]PreviewPort{
			"http":  {Name: "http", Host: 3000, URL: primary.URL, Primary: true},
			"minio": {Name: "minio", Host: 9000, URL: storage.URL},
		},
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://demo-public.previews.example.com/gumroad-dev-public-storage/object.png", nil)
	req.Host = "demo-public.previews.example.com"
	rec := httptest.NewRecorder()
	if !a.servePublicPreview(rec, req) {
		t.Fatal("expected public preview to handle request")
	}
	if body := rec.Body.String(); !strings.Contains(body, "storage:") {
		t.Fatalf("publicPath route should hit storage origin, body=%q status=%d", body, rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "https://demo-public.previews.example.com/products", nil)
	req.Host = "demo-public.previews.example.com"
	rec = httptest.NewRecorder()
	if !a.servePublicPreview(rec, req) {
		t.Fatal("expected public preview to handle request")
	}
	if body := rec.Body.String(); !strings.Contains(body, "primary:") {
		t.Fatalf("non-matching path should hit primary origin, body=%q status=%d", body, rec.Code)
	}
}

func TestDockerComposeOverridePublishesSiblingComposeServicePorts(t *testing.T) {
	home := t.TempDir()
	spec := dockerComposeServiceSpec{
		Project:        "compose-app",
		PreviewID:      "preview-sibling",
		Service:        "web",
		ComposeService: "web",
		OverrideFile:   dockerComposeOverridePath(home, "preview-sibling", "web"),
		Network:        dockerNetworkName("preview-sibling"),
		Ports: []ServicePort{
			{Name: "http", Container: 3000, Protocol: "tcp", Primary: true},
			{Name: "minio-public", Container: 9000, Protocol: "tcp", ComposeService: "minio", PublicPath: "/gumroad-dev-public-storage"},
		},
	}
	if err := writeDockerComposeOverride(spec, []string{"web", "minio", "db"}, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(spec.OverrideFile)
	if err != nil {
		t.Fatal(err)
	}
	yml := string(body)
	for _, want := range []string{
		"minio:",
		"127.0.0.1::9000",
		"127.0.0.1::3000",
	} {
		if !strings.Contains(yml, want) {
			t.Fatalf("override missing %q:\n%s", want, yml)
		}
	}
	// Target should not own the sibling container port.
	webSection := yml[strings.Index(yml, "web:"):]
	if idx := strings.Index(webSection, "\n  minio:"); idx > 0 {
		webSection = webSection[:idx]
	}
	if strings.Contains(webSection, "9000") {
		t.Fatalf("web service should not publish minio port:\n%s", webSection)
	}
}

func TestStartDockerServiceResolvesSiblingComposeServicePorts(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("FAKE_DOCKER_COMPOSE_SERVICES", "web minio")
	t.Setenv("FAKE_DOCKER_COMPOSE_CONFIG_JSON", `{"services":{"web":{"depends_on":{"minio":{"condition":"service_started"}}},"minio":{}}}`)
	home := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "docker-compose.yml"), []byte("services:\n  web:\n    image: app/web\n    depends_on: [minio]\n  minio:\n    image: minio/minio\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := ServiceConfig{
		Source:  "app",
		Runtime: "compose",
		Compose: ComposeConfig{File: "docker-compose.yml", Service: "web"},
		Ports: map[string]PortConfig{
			"http":         {Container: 3310},
			"minio-public": {Container: 9000, ComposeService: "minio", PublicPath: "/gumroad-dev-public-storage"},
		},
		PrimaryPort: "http",
	}
	containerID, err := startDockerService(home, "compose-app", "preview-sibling-ports", "web", svc, map[string]PreviewSource{"app": {Path: source}}, nil)
	if err != nil {
		t.Fatalf("start compose service: %v", err)
	}
	ports, err := servicePortPlan(svc)
	if err != nil {
		t.Fatal(err)
	}
	published, err := dockerContainerRuntime{}.PublishedPorts(containerID, ports)
	if err != nil {
		t.Fatalf("published ports: %v", err)
	}
	byName := map[string]PreviewPort{}
	for _, port := range published {
		byName[port.Name] = port
	}
	if byName["http"].Host != 3310 {
		t.Fatalf("http published = %#v", byName["http"])
	}
	if byName["minio-public"].Host != 9000 || byName["minio-public"].HostIP != "127.0.0.1" {
		t.Fatalf("minio published = %#v", byName["minio-public"])
	}
}

func TestRunDockerComposeOneShotPipesStdin(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("FAKE_DOCKER_COMPOSE_SERVICES", "web")
	home := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "docker-compose.yml"), []byte("services:\n  web:\n    image: app/web\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := ServiceConfig{
		Source:  "app",
		Runtime: "compose",
		Compose: ComposeConfig{File: "docker-compose.yml", Service: "web"},
		Ports:   map[string]PortConfig{"http": {Container: 3310}},
	}
	sources := map[string]PreviewSource{"app": {Path: source}}
	if _, err := runDockerComposeOneShotWithStdin(home, "compose-app", "preview-stdin", "web", svc, sources, nil, RuntimeCommand{Shell: "cat > /tmp/marker && cp /tmp/marker stdin-out.txt"}, "hello-from-stdin"); err != nil {
		// The fake docker writes to host cwd; use a host-writable command.
		if _, err2 := runDockerComposeOneShotWithStdin(home, "compose-app", "preview-stdin", "web", svc, sources, nil, RuntimeCommand{Shell: "cat > stdin-out.txt"}, "hello-from-stdin"); err2 != nil {
			t.Fatalf("compose stdin setup: first=%v second=%v", err, err2)
		}
	}
	body, err := os.ReadFile(filepath.Join(source, "stdin-out.txt"))
	if err != nil || string(body) != "hello-from-stdin" {
		// fallback path above writes into source cwd
		body, err = os.ReadFile(filepath.Join(source, "stdin-out.txt"))
		if err != nil || string(body) != "hello-from-stdin" {
			t.Fatalf("stdin not forwarded: body=%q err=%v", body, err)
		}
	}
	state := os.Getenv("FAKE_DOCKER_STATE")
	project := dockerComposeProjectName("preview-stdin", "web")
	if saved, err := os.ReadFile(filepath.Join(state, "compose-"+project+"-web.stdin")); err != nil || string(saved) != "hello-from-stdin" {
		t.Fatalf("fake docker did not capture stdin: %q err=%v", saved, err)
	}
}

func TestValidateSetupStdinRequiresCommand(t *testing.T) {
	root := t.TempDir()
	cfg := []byte(`
project:
  name: stdin-cfg
sources:
  app:
    mode: external
    path: .
services:
  web:
    source: app
    image: alpine
    ports:
      http:
        container: 3000
setup:
  everyBoot:
    - service: web
      stdin: |
        puts "hi"
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := loadProjectConfig(root)
	if err == nil || !strings.Contains(err.Error(), "stdin requires command") {
		t.Fatalf("expected stdin validation error, got %v", err)
	}
}

func TestRunSetupEveryBootWritesMarkerEachTime(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "everyboot"},
		Sources: map[string]SourceConfig{"app": {Mode: "external", Path: source}},
		Services: map[string]ServiceConfig{
			"web": {
				Source: "app",
				Image:  "alpine",
				Ports:  map[string]PortConfig{"http": {Container: 3000}},
				Health: HealthConfig{Path: "/", ExpectStatus: 200, Timeout: "1s"},
			},
		},
		Setup: SetupConfig{
			EveryBoot: []SetupStep{{
				Name:    "marker",
				Service: "web",
				Command: RuntimeCommand{Shell: "printf booted >> every-boot.txt"},
			}},
		},
	}
	sources := map[string]PreviewSource{"app": {Name: "app", Path: source}}
	if err := a.runSetupStepsNamed("setup.everyBoot", "preview-every", cfg.Setup.EveryBoot, cfg, sources, warmRunState{}); err != nil {
		t.Fatal(err)
	}
	if err := a.runSetupStepsNamed("setup.everyBoot", "preview-every", cfg.Setup.EveryBoot, cfg, sources, warmRunState{}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(source, "every-boot.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "bootedbooted" {
		t.Fatalf("everyBoot should run twice, got %q", body)
	}
}
