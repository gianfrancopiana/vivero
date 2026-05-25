package vivero

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeContainerRuntime struct {
	containerID string
	published   []PreviewPort

	built             []dockerBuildSpec
	ensuredNetworks   []string
	startedServices   []string
	runOnceCommands   []string
	publishedRequests []string
	healthCommands    []string
	removedContainers []string
	removedPreviews   []string
	removedNetworks   []string
	ensuredVolumes    []string
	removedVolumes    []string
	removedImages     []string
	copiedVolumes     []string
	containers        map[string]bool
	volumes           map[string]bool
	images            map[string]bool
	logs              map[string][]string

	startErr               error
	publishedErr           error
	healthErr              error
	removeContainerErr     error
	removeContainerOutput  string
	removeContainerMissing bool
	removePreviewErr       error
	removeNetworkErr       error
}

func (f *fakeContainerRuntime) BuildImage(spec dockerBuildSpec) error {
	f.built = append(f.built, spec)
	return nil
}

func (f *fakeContainerRuntime) EnsureNetwork(previewID string) error {
	f.ensuredNetworks = append(f.ensuredNetworks, previewID)
	return nil
}

func (f *fakeContainerRuntime) StartService(home, projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string) (string, error) {
	f.startedServices = append(f.startedServices, fmt.Sprintf("%s/%s/%s/%s", home, projectName, previewID, service))
	if f.startErr != nil {
		return "", f.startErr
	}
	if f.containerID == "" {
		if f.containers == nil {
			f.containers = map[string]bool{}
		}
		f.containers["fake-container"] = true
		return "fake-container", nil
	}
	if f.containers == nil {
		f.containers = map[string]bool{}
	}
	f.containers[f.containerID] = true
	return f.containerID, nil
}

func (f *fakeContainerRuntime) RunOneShot(home, projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string, command RuntimeCommand) ([]byte, error) {
	f.runOnceCommands = append(f.runOnceCommands, fmt.Sprintf("%s/%s/%s/%s:%s", home, projectName, previewID, service, command.Display()))
	return []byte("ok"), nil
}

func (f *fakeContainerRuntime) PublishedPorts(containerID string, ports []ServicePort) ([]PreviewPort, error) {
	f.publishedRequests = append(f.publishedRequests, containerID)
	if f.publishedErr != nil {
		return nil, f.publishedErr
	}
	return f.published, nil
}

func (f *fakeContainerRuntime) WaitHealthCommand(containerID string, h HealthConfig, timeout time.Duration) error {
	f.healthCommands = append(f.healthCommands, fmt.Sprintf("%s:%s:%s", containerID, h.Command.Display(), timeout))
	return f.healthErr
}

func (f *fakeContainerRuntime) ContainerLogs(containerID string, limit int) ([]string, error) {
	lines := append([]string(nil), f.logs[containerID]...)
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines, nil
}

func (f *fakeContainerRuntime) ContainerExists(containerID string) bool {
	return f.containers != nil && f.containers[containerID]
}

func (f *fakeContainerRuntime) RemoveContainer(containerID string) (bool, string, error) {
	f.removedContainers = append(f.removedContainers, containerID)
	if f.containers != nil {
		delete(f.containers, containerID)
	}
	return f.removeContainerMissing, f.removeContainerOutput, f.removeContainerErr
}

func (f *fakeContainerRuntime) RemoveContainersForPreview(previewID string) error {
	f.removedPreviews = append(f.removedPreviews, previewID)
	return f.removePreviewErr
}

func (f *fakeContainerRuntime) RemoveNetwork(previewID string) error {
	f.removedNetworks = append(f.removedNetworks, previewID)
	return f.removeNetworkErr
}

func (f *fakeContainerRuntime) VolumeExists(name string) bool {
	return f.volumes != nil && f.volumes[name]
}

func (f *fakeContainerRuntime) EnsureVolume(name string) error {
	f.ensuredVolumes = append(f.ensuredVolumes, name)
	if f.volumes == nil {
		f.volumes = map[string]bool{}
	}
	f.volumes[name] = true
	return nil
}

func (f *fakeContainerRuntime) RemoveVolume(name string) error {
	f.removedVolumes = append(f.removedVolumes, name)
	if f.volumes != nil {
		delete(f.volumes, name)
	}
	return nil
}

func (f *fakeContainerRuntime) CopyVolume(src, dst string) error {
	f.copiedVolumes = append(f.copiedVolumes, src+":"+dst)
	if f.volumes == nil {
		f.volumes = map[string]bool{}
	}
	f.volumes[dst] = true
	return nil
}

func (f *fakeContainerRuntime) ImageExists(ref string) bool {
	return f.images != nil && f.images[ref]
}

func (f *fakeContainerRuntime) RemoveImage(ref string) error {
	f.removedImages = append(f.removedImages, ref)
	if f.images != nil {
		delete(f.images, ref)
	}
	return nil
}

func TestUpUsesInjectedContainerRuntime(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &httptest.Server{
		Listener: ln,
		Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})},
	}
	server.Start()
	defer server.Close()
	_, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	hostPort, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{
		containerID: "container-123",
		published:   []PreviewPort{{Name: defaultPrimaryPortName, Container: 8080, Host: hostPort, HostIP: "127.0.0.1", Protocol: "tcp", Primary: true}},
	}
	a.containers = fake

	root := t.TempDir()
	cfg := []byte(`project:
  name: injected-runtime
services:
  web:
    runtime: docker
    build:
      context: .
      dockerfile: Dockerfile
    port: 8080
    health:
      command: ./bin/ready
      timeout: 1s
      interval: 10ms
setup:
  afterSeeds:
    - service: web
      command: ./bin/setup-db
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}

	preview, err := a.Up(UpRequest{Project: "injected-runtime", ID: "demo-pr-17"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := preview.Status, "running"; got != want {
		t.Fatalf("preview status = %q; want %q", got, want)
	}
	web := preview.Services["web"]
	if got, want := web.ContainerID, "container-123"; got != want {
		t.Fatalf("container id = %q; want %q", got, want)
	}
	wantURL := "http://127.0.0.1:" + portText
	if web.URL != wantURL || web.OriginURL != wantURL {
		t.Fatalf("service URLs = url:%q origin:%q; want %q", web.URL, web.OriginURL, wantURL)
	}
	if got, want := web.Status, "healthy"; got != want {
		t.Fatalf("service status = %q; want %q", got, want)
	}
	if len(fake.built) != 1 {
		t.Fatalf("built images = %#v", fake.built)
	}
	if got, want := fake.built[0].Tag, "vivero/injected-runtime-web:"+shortStableID("demo-pr-17:web"); got != want {
		t.Fatalf("built image tag = %q; want %q", got, want)
	}
	if !reflect.DeepEqual(fake.ensuredNetworks, []string{"demo-pr-17"}) {
		t.Fatalf("ensured networks = %#v", fake.ensuredNetworks)
	}
	if len(fake.startedServices) != 1 || !strings.Contains(fake.startedServices[0], "/injected-runtime/demo-pr-17/web") {
		t.Fatalf("started services = %#v", fake.startedServices)
	}
	if !reflect.DeepEqual(fake.runOnceCommands, []string{a.Home + "/injected-runtime/demo-pr-17/web:./bin/setup-db"}) {
		t.Fatalf("one-shot commands = %#v", fake.runOnceCommands)
	}
	if !reflect.DeepEqual(fake.publishedRequests, []string{"container-123"}) {
		t.Fatalf("published port requests = %#v", fake.publishedRequests)
	}
	if len(fake.healthCommands) != 1 || !strings.Contains(fake.healthCommands[0], "container-123:./bin/ready:") {
		t.Fatalf("health commands = %#v", fake.healthCommands)
	}
}

func TestUpReuseReturnsHealthyExistingPreviewWithoutRestart(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{containers: map[string]bool{"container-123": true}}
	a.containers = fake

	root := t.TempDir()
	cfg := []byte(`project:
  name: reusable-runtime
sources:
  app:
    path: .
services:
  web:
    runtime: docker
    source: app
    image: alpine:latest
    port: 8080
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}
	existing := PreviewRecord{
		ID:       "demo-pr-17",
		Project:  "reusable-runtime",
		Status:   "running",
		Metadata: map[string]string{"branch": "feature/demo"},
		Sources:  map[string]PreviewSource{},
		Services: map[string]PreviewService{},
	}
	if err := a.upsertPreview(existing); err != nil {
		t.Fatal(err)
	}
	if err := a.saveSource("demo-pr-17", PreviewSource{Name: "app", Mode: "external", Path: root, Owned: false}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("demo-pr-17", PreviewService{Name: "web", Runtime: "docker", Source: "app", ContainerID: "container-123", Status: "healthy", URL: "http://127.0.0.1:3310", OriginURL: "http://127.0.0.1:3310"}); err != nil {
		t.Fatal(err)
	}

	preview, err := a.Up(UpRequest{Project: "reusable-runtime", ID: "demo-pr-17", Sources: map[string]string{"app.path": root}, Metadata: map[string]string{"branch": "feature/demo"}, Reuse: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := preview.Services["web"].ContainerID; got != "container-123" {
		t.Fatalf("container id = %q; want existing container", got)
	}
	if len(fake.startedServices) != 0 || len(fake.removedContainers) != 0 || len(fake.removedPreviews) != 0 || len(fake.ensuredNetworks) != 0 {
		t.Fatalf("reuse should not restart or clean resources; started=%#v removed=%#v removedPreviews=%#v networks=%#v", fake.startedServices, fake.removedContainers, fake.removedPreviews, fake.ensuredNetworks)
	}
	events, err := a.events("demo-pr-17", 0)
	if err != nil {
		t.Fatal(err)
	}
	foundReuseEvent := false
	for _, event := range events {
		if event.Type == "preview.reused" {
			foundReuseEvent = true
			break
		}
	}
	if !foundReuseEvent {
		t.Fatalf("missing preview.reused event: %#v", events)
	}
}

func TestBuildServiceImagesRecordsBuildCacheMetadata(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{}
	a.containers = fake

	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	cacheEnabled := true
	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{
			"web": {
				Build: ImageBuildConfig{
					Context: "app",
					Cache: ImageBuildCacheConfig{
						Enabled: &cacheEnabled,
						From:    []string{"type=local,src=.vivero/cache/build/web"},
						To:      []string{"type=local,dest=.vivero/cache/build/web,mode=max"},
					},
				},
			},
		},
	}
	if err := a.buildServiceImages(ProjectRecord{Name: "demo", Path: projectRoot, Config: cfg}, "demo-pr-17", nil, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(fake.built) != 1 || fake.built[0].Engine != dockerBuildEngineBuildx || !fake.built[0].CacheEnabled {
		t.Fatalf("expected cache-enabled buildx image spec, got %#v", fake.built)
	}
	events, err := a.events("demo-pr-17", 0)
	if err != nil {
		t.Fatal(err)
	}
	var built Event
	for _, event := range events {
		if event.Type == "image.built" {
			built = event
			break
		}
	}
	if built.Type == "" {
		t.Fatalf("missing image.built event: %#v", events)
	}
	wantCachePath := filepath.Join(projectRoot, "app", ".vivero", "cache", "build", "web")
	if built.Metadata["engine"] != dockerBuildEngineBuildx || built.Metadata["cacheEnabled"] != "true" || !strings.Contains(built.Metadata["cacheFrom"], wantCachePath) || !strings.Contains(built.Metadata["cacheTo"], wantCachePath) || built.Metadata["durationMs"] == "" {
		t.Fatalf("build cache metadata missing: %#v", built.Metadata)
	}
}

func TestCleanupPreviewServicesUsesInjectedContainerRuntime(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{
		removeContainerMissing: true,
		removeContainerOutput:  "Error: No such container: missing-container",
		removeContainerErr:     errors.New("exit status 1"),
	}
	a.containers = fake

	services := map[string]PreviewService{
		"web": {Name: "web", Runtime: "docker", Status: "running", ContainerID: "missing-container"},
	}
	if err := a.cleanupPreviewServices("demo-pr-17", services); err != nil {
		t.Fatal(err)
	}
	if got := services["web"].ContainerID; got != "" {
		t.Fatalf("container id should be cleared after missing-container cleanup, got %q", got)
	}
	if got, want := services["web"].Status, "dead"; got != want {
		t.Fatalf("service status = %q; want %q", got, want)
	}
	if !reflect.DeepEqual(fake.removedContainers, []string{"missing-container"}) {
		t.Fatalf("removed containers = %#v", fake.removedContainers)
	}
	if !reflect.DeepEqual(fake.removedPreviews, []string{"demo-pr-17"}) {
		t.Fatalf("removed preview containers = %#v", fake.removedPreviews)
	}
	if !reflect.DeepEqual(fake.removedNetworks, []string{"demo-pr-17"}) {
		t.Fatalf("removed networks = %#v", fake.removedNetworks)
	}
}

func TestRemovePreviewDependencyVolumesUsesInjectedContainerRuntime(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{}
	a.containers = fake

	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{
			"web": {
				DependencyVolumes: []VolumeConfig{
					{Name: "cache", Target: "/cache"},
					{Name: "db", Target: "/db", Lifetime: "smart"},
					{Name: "project-cache", Target: "/project-cache", Lifetime: "project"},
				},
			},
		},
	}
	if err := a.removePreviewDependencyVolumes("demo-pr-17", cfg); err != nil {
		t.Fatal(err)
	}
	want := []string{
		dockerVolumeName("demo-pr-17", "web", "cache"),
		dockerSmartPreviewVolumeName("demo", "demo-pr-17", "web", "db"),
	}
	if !reflect.DeepEqual(fake.removedVolumes, want) {
		t.Fatalf("removed volumes = %#v; want %#v", fake.removedVolumes, want)
	}
}

func TestPrepareSmartWarmVolumesUsesInjectedContainerRuntime(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "Gemfile.lock"), []byte("gem lock"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseline := dockerSmartBaselineVolumeName("demo", "web", "db")
	fake := &fakeContainerRuntime{volumes: map[string]bool{baseline: true}}
	a.containers = fake

	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Warm:    WarmConfig{BaselineRefs: []string{"main"}, Fingerprint: WarmFingerprintConfig{Paths: []string{"Gemfile.lock"}}},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "alpine:latest", DependencyVolumes: []VolumeConfig{{Name: "db", Target: "/db", Lifetime: "smart"}}},
		},
	}
	project := ProjectRecord{Name: "demo", Path: t.TempDir(), Config: cfg}
	sources := map[string]PreviewSource{"app": {Name: "app", Mode: "external", Path: source}}
	warmCfg, warmState, err := a.prepareSmartWarmVolumes(project, UpRequest{Project: "demo", ID: "feature-preview", Metadata: map[string]string{"branch": "feature/demo"}}, cfg, sources)
	if err != nil {
		t.Fatal(err)
	}
	active := dockerSmartPreviewVolumeName("demo", "feature-preview", "web", "db")
	if got := warmCfg.Services["web"].DependencyVolumes[0].RuntimeSource; got != active {
		t.Fatalf("runtime volume source = %q; want %q", got, active)
	}
	if warmState.Mode != warmModeDerived {
		t.Fatalf("warm mode = %q; want %q", warmState.Mode, warmModeDerived)
	}
	if !reflect.DeepEqual(fake.removedVolumes, []string{active}) {
		t.Fatalf("removed volumes = %#v", fake.removedVolumes)
	}
	if !reflect.DeepEqual(fake.copiedVolumes, []string{baseline + ":" + active}) {
		t.Fatalf("copied volumes = %#v", fake.copiedVolumes)
	}
}
