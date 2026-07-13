package vivero

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockingStartRuntime struct {
	*fakeContainerRuntime
	mu           sync.Mutex
	calls        int
	active       int
	maxActive    int
	firstEntered chan struct{}
	releaseFirst chan struct{}
}

func (r *blockingStartRuntime) StartService(home, projectName, previewID, service string, svc ServiceConfig, sources map[string]PreviewSource, env map[string]string) (string, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.mu.Unlock()
	if call == 1 {
		close(r.firstEntered)
		<-r.releaseFirst
	}
	container, err := r.fakeContainerRuntime.StartService(home, projectName, previewID, service, svc, sources, env)
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	return container, err
}

func (r *blockingStartRuntime) counts() (calls, maxActive int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.maxActive
}

func writeLifecycleTestProject(t *testing.T, root, envValue string, maxPreviews int) {
	t.Helper()
	resources := ""
	if maxPreviews > 0 {
		resources = "resources:\n  maxConcurrentPreviews: " + strconv.Itoa(maxPreviews) + "\n"
	}
	env := ""
	if envValue != "" {
		env = "    env:\n      FEATURE_FLAG: " + envValue + "\n"
	}
	body := "project:\n  name: lifecycle-test\n" + resources + "sources:\n  app:\n    path: .\nservices:\n  web:\n    runtime: docker\n    source: app\n    image: alpine:latest\n" + env
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestComposeProjectRuntimeStatusMatchesFullAndShortContainerIDs(t *testing.T) {
	fullID := "0a3a6fcd4bb8d62abe8d35cb0f1a14a5dc7151cce8d05b77f4913fb2cffde8cd"
	states := []runtimeContainerState{
		{ID: fullID[:12], Running: true},
		{ID: "123456789abc", Running: false, ExitCode: 0, ExpectedCompletion: true},
	}
	healthy, anyRunning, reason := composeProjectRuntimeStatus(states, fullID)
	if !healthy || !anyRunning || reason != "" {
		t.Fatalf("status = healthy:%t anyRunning:%t reason:%q", healthy, anyRunning, reason)
	}
	if sameContainerID("0a3a6fcd4bb", fullID) {
		t.Fatal("container IDs shorter than Docker's canonical short ID must not match")
	}
}

func TestReadReconciliationDemotesExitedContainerAndFreesPreviewCap(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{containers: map[string]bool{"exited": false}}
	a.containers = fake

	root := t.TempDir()
	writeLifecycleTestProject(t, root, "", 1)
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "stale", Project: "lifecycle-test", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveSource("stale", PreviewSource{Name: "app", Mode: "external", Path: root}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("stale", PreviewService{Name: "web", Runtime: "docker", ContainerID: "exited", Status: "healthy"}); err != nil {
		t.Fatal(err)
	}

	stale, err := a.getPreviewReconciled("stale")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Status != "unhealthy" || stale.Services["web"].Status != "dead" {
		t.Fatalf("stale preview was not demoted: %#v", stale)
	}

	started, err := a.Up(UpRequest{Project: "lifecycle-test", ID: "fresh"})
	if err != nil {
		t.Fatalf("dead preview must not consume maxConcurrentPreviews: %v", err)
	}
	if started.Status != "running" {
		t.Fatalf("fresh preview status = %q", started.Status)
	}
}

func TestConcurrentUpForSamePreviewIsSerialized(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	firstApp, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer firstApp.Close()
	secondApp, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer secondApp.Close()
	runtime := &blockingStartRuntime{
		fakeContainerRuntime: &fakeContainerRuntime{},
		firstEntered:         make(chan struct{}),
		releaseFirst:         make(chan struct{}),
	}
	firstApp.containers = runtime
	secondApp.containers = runtime
	root := t.TempDir()
	writeLifecycleTestProject(t, root, "", 0)
	if _, err := firstApp.SyncProject(root); err != nil {
		t.Fatal(err)
	}

	type result struct {
		preview PreviewRecord
		err     error
	}
	firstResult := make(chan result, 1)
	secondResult := make(chan result, 1)
	go func() {
		preview, err := firstApp.Up(UpRequest{Project: "lifecycle-test", ID: "same-id"})
		firstResult <- result{preview: preview, err: err}
	}()
	select {
	case <-runtime.firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first up did not enter service startup")
	}
	go func() {
		preview, err := secondApp.Up(UpRequest{Project: "lifecycle-test", ID: "same-id"})
		secondResult <- result{preview: preview, err: err}
	}()
	time.Sleep(100 * time.Millisecond)
	if calls, _ := runtime.counts(); calls != 1 {
		t.Fatalf("second up entered startup before first released lock; calls=%d", calls)
	}
	select {
	case result := <-secondResult:
		t.Fatalf("second up returned while first held lock: preview=%#v err=%v", result.preview, result.err)
	default:
	}
	close(runtime.releaseFirst)
	for name, ch := range map[string]<-chan result{"first": firstResult, "second": secondResult} {
		select {
		case result := <-ch:
			if result.err != nil {
				t.Fatalf("%s up failed: %v", name, result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s up did not finish", name)
		}
	}
	if calls, maxActive := runtime.counts(); calls != 2 || maxActive != 1 {
		t.Fatalf("same-id startups overlapped: calls=%d maxActive=%d", calls, maxActive)
	}
}

func TestReadReconciliationSkipsActiveStartupButDemotesAbandonedPending(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	root := t.TempDir()
	writeLifecycleTestProject(t, root, "", 0)
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "pending", Project: "lifecycle-test", Status: "pending"}); err != nil {
		t.Fatal(err)
	}

	lock, err := a.lockPreview("pending")
	if err != nil {
		t.Fatal(err)
	}
	active, err := a.getPreviewReconciled("pending")
	if err != nil {
		lock.unlock()
		t.Fatal(err)
	}
	if active.Status != "pending" {
		lock.unlock()
		t.Fatalf("active startup was reconciled: %q", active.Status)
	}
	lock.unlock()

	abandoned, err := a.getPreviewReconciled("pending")
	if err != nil {
		t.Fatal(err)
	}
	if abandoned.Status != "unhealthy" {
		t.Fatalf("abandoned pending preview status = %q", abandoned.Status)
	}
}

func TestReconciliationRecoversEndpointAndUnhealthyLivePreviewStillConsumesCap(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{containers: map[string]bool{"live": true}}
	a.containers = fake

	root := t.TempDir()
	config := `project:
  name: lifecycle-test
resources:
  maxConcurrentPreviews: 1
sources:
  app:
    path: .
services:
  web:
    runtime: docker
    source: app
    image: alpine:latest
    port: 8080
`
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}
	var healthy atomic.Bool
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer endpoint.Close()
	if err := a.upsertPreview(PreviewRecord{ID: "live-unhealthy", Project: "lifecycle-test", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveSource("live-unhealthy", PreviewSource{Name: "app", Mode: "external", Path: root}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("live-unhealthy", PreviewService{Name: "web", Runtime: "docker", ContainerID: "live", Status: "healthy", URL: endpoint.URL, OriginURL: endpoint.URL}); err != nil {
		t.Fatal(err)
	}

	preview, err := a.getPreviewReconciled("live-unhealthy")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != "unhealthy" {
		t.Fatalf("failing endpoint status = %q", preview.Status)
	}
	if _, err := a.Up(UpRequest{Project: "lifecycle-test", ID: "must-not-start"}); err == nil || !strings.Contains(err.Error(), "resource cap reached") {
		t.Fatalf("live unhealthy preview did not consume cap: %v", err)
	}

	healthy.Store(true)
	recovered, err := a.getPreviewReconciled("live-unhealthy")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "running" || recovered.Services["web"].Status != "healthy" {
		t.Fatalf("healthy endpoint did not recover preview: %#v", recovered)
	}
}

func TestComposeReconciliationCoversDependenciesAndCapacity(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	project := ProjectConfig{
		Project: ProjectMeta{Name: "compose-state"},
		Services: map[string]ServiceConfig{
			"web": {Runtime: "compose", Compose: ComposeConfig{File: "compose.yml", Service: "web"}},
		},
	}
	if _, err := a.saveProject(t.TempDir(), project); err != nil {
		t.Fatal(err)
	}
	fake := &fakeContainerRuntime{composeProjects: map[string]map[string]runtimeContainerState{
		"dependency-crash:web": {
			"target": {Running: true},
			"db":     {Running: false, ExitCode: 2},
		},
		"target-crash:web": {
			"target": {Running: false, ExitCode: 1},
			"db":     {Running: true},
		},
		"successful-init:web": {
			"target": {Running: true},
			"init":   {Running: false, ExitCode: 0, ExpectedCompletion: true},
		},
		"stopped-zero-daemon:web": {
			"target": {Running: true},
			"db":     {Running: false, ExitCode: 0},
		},
		"unpersisted:web": {
			"target": {Running: true},
		},
	}}
	a.containers = fake
	create := func(id string) {
		t.Helper()
		if err := a.upsertPreview(PreviewRecord{ID: id, Project: "compose-state", Status: "running"}); err != nil {
			t.Fatal(err)
		}
		if err := a.saveService(id, PreviewService{Name: "web", Runtime: "compose", ContainerID: "target", Status: "healthy"}); err != nil {
			t.Fatal(err)
		}
	}

	create("dependency-crash")
	dependencyCrash, err := a.getPreviewReconciled("dependency-crash")
	if err != nil {
		t.Fatal(err)
	}
	if dependencyCrash.Status != "unhealthy" || dependencyCrash.Services["web"].Status != "unhealthy" {
		t.Fatalf("crashed Compose dependency did not demote preview: %#v", dependencyCrash)
	}
	if !a.previewConsumesRuntimeCapacity(dependencyCrash) {
		t.Fatal("live target must consume capacity after a Compose dependency crashes")
	}

	create("target-crash")
	targetCrash, err := a.getPreviewReconciled("target-crash")
	if err != nil {
		t.Fatal(err)
	}
	if targetCrash.Status != "unhealthy" || targetCrash.Services["web"].Status != "unhealthy" {
		t.Fatalf("exited Compose target with live dependency did not demote preview: %#v", targetCrash)
	}
	if !a.previewConsumesRuntimeCapacity(targetCrash) {
		t.Fatal("live Compose dependency must keep consuming capacity after target exit")
	}

	create("successful-init")
	successfulInit, err := a.getPreviewReconciled("successful-init")
	if err != nil {
		t.Fatal(err)
	}
	if successfulInit.Status != "running" || successfulInit.Services["web"].Status != "healthy" {
		t.Fatalf("successful exited init dependency poisoned Compose status: %#v", successfulInit)
	}

	create("stopped-zero-daemon")
	stoppedDaemon, err := a.getPreviewReconciled("stopped-zero-daemon")
	if err != nil {
		t.Fatal(err)
	}
	if stoppedDaemon.Status != "unhealthy" || stoppedDaemon.Services["web"].Status != "unhealthy" {
		t.Fatalf("cleanly stopped daemon was mistaken for a completed init dependency: %#v", stoppedDaemon)
	}

	if err := a.upsertPreview(PreviewRecord{ID: "unpersisted", Project: "compose-state", Status: "unhealthy"}); err != nil {
		t.Fatal(err)
	}
	unpersisted, err := a.getPreviewRaw("unpersisted")
	if err != nil {
		t.Fatal(err)
	}
	if !a.previewConsumesRuntimeCapacity(unpersisted) {
		t.Fatal("unpersisted live Compose project must consume capacity")
	}
}

func TestReuseRestartsForEffectiveConfigSecretsAndExitedContainer(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{}
	a.containers = fake

	root := t.TempDir()
	writeLifecycleTestProject(t, root, "off", 0)
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}
	req := UpRequest{Project: "lifecycle-test", ID: "reuse", Reuse: true}
	if _, err := a.Up(req); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(req); err != nil {
		t.Fatal(err)
	}
	if len(fake.startedServices) != 1 {
		t.Fatalf("unchanged preview restarted: %#v", fake.startedServices)
	}

	writeLifecycleTestProject(t, root, "on", 0)
	if _, err := a.Up(req); err != nil {
		t.Fatal(err)
	}
	if len(fake.startedServices) != 2 {
		t.Fatalf("config change did not restart preview: %#v", fake.startedServices)
	}

	if err := writeEnvFile(a.secretFile("lifecycle-test"), map[string]string{"TOKEN": "rotated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Up(req); err != nil {
		t.Fatal(err)
	}
	if len(fake.startedServices) != 3 {
		t.Fatalf("secret change did not restart preview: %#v", fake.startedServices)
	}

	for container := range fake.containers {
		fake.containers[container] = false
	}
	if _, err := a.Up(req); err != nil {
		t.Fatal(err)
	}
	if len(fake.startedServices) != 4 {
		t.Fatalf("exited container was reused: %#v", fake.startedServices)
	}
}

func TestPreviewConfigHashIsNotSerialized(t *testing.T) {
	body, err := json.Marshal(PreviewRecord{ID: "preview", ConfigHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), strings.Repeat("a", 64)) || strings.Contains(string(body), "configHash") {
		t.Fatalf("config hash leaked into preview JSON: %s", body)
	}
}

func TestReusePublicRequestRejectsLocalOnlyURL(t *testing.T) {
	a := &App{containers: &fakeContainerRuntime{containers: map[string]bool{"live": true}}}
	cfg := ProjectConfig{Services: map[string]ServiceConfig{"web": {Runtime: "docker", Port: 8080}}}
	reason := a.reuseServiceMissReason(cfg, true, map[string]PreviewService{
		"web": {Name: "web", Runtime: "docker", ContainerID: "live", Status: "healthy", URL: "http://127.0.0.1:8080", OriginURL: "http://127.0.0.1:8080"},
	})
	if !strings.Contains(reason, "not started with a public URL") {
		t.Fatalf("local-only preview was accepted for public reuse: %q", reason)
	}
}

func TestUnidentifiedPIDDoesNotCountAsLiveOrReusable(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.containers = &fakeContainerRuntime{}
	service := PreviewService{Name: "worker", Runtime: "docker", Status: "running", PID: os.Getpid()}
	if trackedProcessRunning(service.PID, "") {
		t.Fatal("pid without a recorded identity must not be treated as owned")
	}
	preview := PreviewRecord{ID: "legacy-pid", Status: "unhealthy", Services: map[string]PreviewService{"worker": service}}
	if a.previewConsumesRuntimeCapacity(preview) {
		t.Fatal("unidentified pid must not consume preview capacity")
	}
	cfg := ProjectConfig{Services: map[string]ServiceConfig{"worker": {Runtime: "docker"}}}
	if reason := a.reuseServiceMissReason(cfg, false, preview.Services); !strings.Contains(reason, "pid is missing") {
		t.Fatalf("unidentified pid was accepted for reuse: %q", reason)
	}
}

func TestTeardownDoesNotSignalRecycledPID(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.upsertPreview(PreviewRecord{ID: "pid-test", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}

	state, err := a.stopPreviewServiceResources("pid-test", "worker", PreviewService{Name: "worker", PID: os.Getpid(), PIDIdentity: "different-start-token", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if state.PID != 0 || state.PIDIdentity != "" {
		t.Fatalf("recycled pid remained owned: %#v", state)
	}
	events, err := a.events("pid-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "service.pid_unowned" {
		t.Fatalf("missing recycled-pid warning: %#v", events)
	}
}

func TestComposeVolumesAreDiscardedOnlyOnExplicitDiscard(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{}
	a.containers = fake
	create := func(id string) {
		t.Helper()
		if err := a.upsertPreview(PreviewRecord{ID: id, Project: "missing-project", Status: "running"}); err != nil {
			t.Fatal(err)
		}
		if err := a.saveService(id, PreviewService{Name: "web", Runtime: "compose", Status: "running"}); err != nil {
			t.Fatal(err)
		}
	}
	create("safe")
	if _, err := a.Down("safe", "safe"); err != nil {
		t.Fatal(err)
	}
	create("discard")
	if _, err := a.Down("discard", "discard"); err != nil {
		t.Fatal(err)
	}
	want := []string{"safe:web:discard=false", "discard:web:discard=true"}
	if len(fake.removedComposeProjects) != len(want) {
		t.Fatalf("compose removals = %#v", fake.removedComposeProjects)
	}
	for i := range want {
		if fake.removedComposeProjects[i] != want[i] {
			t.Fatalf("compose removals = %#v; want %#v", fake.removedComposeProjects, want)
		}
	}

	project := ProjectConfig{
		Project: ProjectMeta{Name: "compose-crash"},
		Services: map[string]ServiceConfig{
			"web": {Runtime: "compose", Compose: ComposeConfig{File: "compose.yml", Service: "web"}},
		},
	}
	if _, err := a.saveProject(t.TempDir(), project); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "crash-window", Project: "compose-crash", Status: "unhealthy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Down("crash-window", "discard"); err != nil {
		t.Fatal(err)
	}
	if got := fake.removedComposeProjects[len(fake.removedComposeProjects)-1]; got != "crash-window:web:discard=true" {
		t.Fatalf("unpersisted compose project was not discarded: %#v", fake.removedComposeProjects)
	}
}

func TestProcessIdentityPersistsWithTrackedPID(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.upsertPreview(PreviewRecord{ID: "pid-state", Project: "demo", Status: "dead"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("pid-state", PreviewService{Name: "worker", PID: os.Getpid(), Status: "running", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	preview, err := a.getPreviewRaw("pid-state")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Services["worker"].PIDIdentity == "" {
		t.Fatal("tracked process identity was not persisted")
	}
}

func TestReplacingManagedSourceUnregistersWorktree(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.containers = &fakeContainerRuntime{}

	origin := t.TempDir()
	if out, err := runCmd(origin, nil, "git", "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	_, _ = runCmd(origin, nil, "git", "config", "user.email", "test@example.com")
	_, _ = runCmd(origin, nil, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(origin, nil, "git", "add", "."); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	if out, err := runCmd(origin, nil, "git", "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}

	sourceConfig := SourceConfig{Repo: origin, DefaultRef: "main"}
	first, err := a.resolveSource("demo", origin, "managed", "app", sourceConfig, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "managed", Project: "demo", Status: "dead"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveSource("managed", first); err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.cleanupExistingPreviewForUp("managed"); err != nil || !found {
		t.Fatalf("cleanup existing managed preview: found=%t err=%v", found, err)
	}
	repo := managedRepoPath(a.Home, "app")
	list, err := runCmd(repo, nil, "git", "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(list), first.Path) {
		t.Fatalf("managed worktree remained registered: %s", list)
	}
	if _, err := a.resolveSource("demo", origin, "managed", "app", sourceConfig, nil); err != nil {
		t.Fatalf("reusing managed preview path failed: %v", err)
	}
}
