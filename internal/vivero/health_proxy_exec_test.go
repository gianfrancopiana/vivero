package vivero

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestHTTPClientResolvesLocalhostSubdomainsLocallyAndBypassesProxy(t *testing.T) {
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequests.Add(1)
		http.Error(w, "must not use proxy", http.StatusBadGateway)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "local")
	}))
	defer target.Close()
	parsed, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = net.JoinHostPort("app.localhost", parsed.Port())
	resp, err := httpClientForURL(parsed.String()).Get(parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "local" {
		t.Fatalf("body = %q", body)
	}
	if got := proxyRequests.Load(); got != 0 {
		t.Fatalf("localhost request escaped through HTTP proxy (%d requests)", got)
	}
}

func TestWaitHTTPRequestCannotExceedOverallHealthDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	started := time.Now()
	err := waitHTTP(server.URL, HealthConfig{Interval: "5s"}, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected hanging health request to time out")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("100ms health timeout took %s: %v", elapsed, err)
	}
}

func TestWaitHTTPForContainerFailsImmediatelyWhenContainerExited(t *testing.T) {
	a := &App{containers: &fakeContainerRuntime{containers: map[string]bool{"dead": false}}}
	started := time.Now()
	err := a.waitHTTPForContainer("http://127.0.0.1:1", "dead", HealthConfig{Interval: "2s"}, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("dead container detection took %s", elapsed)
	}
}

func TestHealthIntervalResourceChecksAreCappedAtOncePerSecond(t *testing.T) {
	checks := 0
	started := time.Now()
	if err := sleepHealthInterval(1050*time.Millisecond, func() error {
		checks++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if checks > 3 {
		t.Fatalf("resource check ran %d times in %s", checks, time.Since(started))
	}
}

func TestReconcileServiceEndpointRespawnsDeadProxyAndProbesReportedURL(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	reported := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ready")
	}))
	defer reported.Close()
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.upsertPreview(PreviewRecord{ID: "proxy-restart", Project: "demo", Status: "dead", CreatedAt: nowUTC()}); err != nil {
		t.Fatal(err)
	}
	state := PreviewService{Name: "web", Runtime: "docker", ContainerID: "container-1", Status: "healthy", OriginURL: "http://127.0.0.1:3310", URL: "http://127.0.0.1:59999", ProxyURL: "http://127.0.0.1:59999", ProxyPID: 99999999}
	if err := a.saveService("proxy-restart", state); err != nil {
		t.Fatal(err)
	}
	cfg := ServiceConfig{TunnelHostHeader: "localhost", Health: HealthConfig{ExpectStatus: 200}}
	called := false
	reconciled, err := a.reconcileServiceEndpointWithStarter("proxy-restart", "web", state, cfg, func(previewID, service, runtime, containerID, originURL, hostHeader string, rewrite PublicRewriteConfig, routes []publicProxyRoute, h HealthConfig, listenHost, preferredURL string, maxWait time.Duration) (string, int, error) {
		called = true
		if preferredURL != state.ProxyURL || originURL != state.OriginURL {
			t.Fatalf("restart args preferred=%q origin=%q", preferredURL, originURL)
		}
		if maxWait != 5*time.Second {
			t.Fatalf("reconciliation proxy wait cap = %s", maxWait)
		}
		return reported.URL, os.Getpid(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || reconciled.URL != reported.URL || reconciled.ProxyURL != reported.URL || reconciled.ProxyPID != os.Getpid() || reconciled.LastHealth != "ok" {
		t.Fatalf("reconciled = %#v", reconciled)
	}
	persisted, err := a.getPreviewRaw("proxy-restart")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Services["web"].ProxyPID != os.Getpid() {
		t.Fatalf("restarted proxy was not persisted: %#v", persisted.Services["web"])
	}
}

func TestWaitProbesReportedURLInsteadOfHealthyOrigin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "origin healthy") }))
	defer origin.Close()
	reported := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "proxy down", http.StatusBadGateway) }))
	defer reported.Close()
	projectRoot := t.TempDir()
	config := `project:
  name: wait-reported
services:
  web:
    runtime: docker
    image: example.test/web
    port: 3000
    health:
      expectStatus: 200
      interval: 5ms
`
	if err := os.WriteFile(filepath.Join(projectRoot, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.SyncProject(projectRoot); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "wait-reported", Project: "wait-reported", Status: "running", CreatedAt: nowUTC()}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeContainerRuntime{containers: map[string]bool{"container-1": true}}
	a.containers = fake
	if err := a.saveService("wait-reported", PreviewService{Name: "web", Runtime: "docker", ContainerID: "container-1", Status: "healthy", OriginURL: origin.URL, URL: reported.URL}); err != nil {
		t.Fatal(err)
	}
	err = a.Wait("wait-reported", 40*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), reported.URL) {
		t.Fatalf("Wait error = %v; expected reported URL failure", err)
	}
}

func TestWaitRejectsStoppedComposeDependency(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "healthy") }))
	defer server.Close()
	projectRoot := t.TempDir()
	config := `project:
  name: wait-compose-dependency
sources:
  app:
    path: .
services:
  web:
    source: app
    runtime: compose
    compose:
      file: compose.yml
      service: web
    port: 3000
`
	if err := os.WriteFile(filepath.Join(projectRoot, "vivero.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "compose.yml"), []byte("services:\n  web:\n    image: example.test/web\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.SyncProject(projectRoot); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "wait-compose", Project: "wait-compose-dependency", Status: "running", CreatedAt: nowUTC()}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeContainerRuntime{
		containers: map[string]bool{"web-container": true, "db-container": false},
		composeProjects: map[string]map[string]runtimeContainerState{
			"wait-compose:web": {
				"web-container": {Running: true},
				"db-container":  {Running: false, ExitCode: 1},
			},
		},
	}
	a.containers = fake
	if err := a.saveService("wait-compose", PreviewService{Name: "web", Runtime: "compose", ContainerID: "web-container", Status: "healthy", OriginURL: server.URL, URL: server.URL}); err != nil {
		t.Fatal(err)
	}
	err = a.Wait("wait-compose", time.Second)
	if err == nil || !strings.Contains(err.Error(), "db-container") {
		t.Fatalf("Wait error = %v", err)
	}
}

func TestStartServiceRejectsFailedComposeDependencyDespiteHealthyTargetURL(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("VIVERO_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "healthy web") }))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	_, portText, _ := net.SplitHostPort(parsed.Host)
	hostPort, _ := strconv.Atoi(portText)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{
		containerID: "web-container",
		published:   []PreviewPort{{Name: "http", Container: 3000, Host: hostPort, HostIP: "127.0.0.1", Primary: true}},
		composeProjects: map[string]map[string]runtimeContainerState{
			"compose-start:web": {
				"web-container": {Running: true},
				"db-container":  {Running: false, ExitCode: 1},
			},
		},
	}
	a.containers = fake
	source := t.TempDir()
	svc := ServiceConfig{Source: "app", Runtime: "compose", Compose: ComposeConfig{File: "compose.yml", Service: "web"}, Ports: map[string]PortConfig{"http": {Container: 3000}}, Health: HealthConfig{ExpectStatus: 200}}
	cfg := ProjectConfig{Project: ProjectMeta{Name: "compose-start"}, Services: map[string]ServiceConfig{"web": svc}}
	_, err = a.startService(UpRequest{ID: "compose-start"}, "web", svc, map[string]PreviewSource{"app": {Name: "app", Path: source}}, cfg, false, true)
	if err == nil || !strings.Contains(err.Error(), "db-container") {
		t.Fatalf("startService error = %v", err)
	}
}

func TestStartServiceRejectsFailedComposeDependencyWithoutHealthOrURL(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{
		containerID: "worker-container",
		composeProjects: map[string]map[string]runtimeContainerState{
			"compose-worker:worker": {
				"worker-container": {Running: true},
				"db-container":     {Running: false, ExitCode: 1},
			},
		},
	}
	a.containers = fake
	svc := ServiceConfig{Source: "app", Runtime: "compose", Compose: ComposeConfig{File: "compose.yml", Service: "worker"}}
	cfg := ProjectConfig{Project: ProjectMeta{Name: "compose-worker"}, Services: map[string]ServiceConfig{"worker": svc}}
	_, err = a.startService(UpRequest{ID: "compose-worker"}, "worker", svc, map[string]PreviewSource{"app": {Name: "app", Path: t.TempDir()}}, cfg, false, true)
	if err == nil || !strings.Contains(err.Error(), "db-container") {
		t.Fatalf("commandless startService error = %v", err)
	}
}

func TestStartServiceRechecksComposeDependenciesAfterHealthCommand(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	projectStates := map[string]runtimeContainerState{
		"worker-container": {Running: true},
		"db-container":     {Running: true},
	}
	fake := &fakeContainerRuntime{
		containerID: "worker-container",
		composeProjects: map[string]map[string]runtimeContainerState{
			"compose-health-command:worker": projectStates,
		},
	}
	fake.healthHook = func() {
		projectStates["db-container"] = runtimeContainerState{Running: false, ExitCode: 1}
	}
	a.containers = fake
	svc := ServiceConfig{Source: "app", Runtime: "compose", Compose: ComposeConfig{File: "compose.yml", Service: "worker"}, Health: HealthConfig{Command: RuntimeCommand{Shell: "bin/health"}}}
	cfg := ProjectConfig{Project: ProjectMeta{Name: "compose-health-command"}, Services: map[string]ServiceConfig{"worker": svc}}
	_, err = a.startService(UpRequest{ID: "compose-health-command"}, "worker", svc, map[string]PreviewSource{"app": {Name: "app", Path: t.TempDir()}}, cfg, false, true)
	if err == nil || !strings.Contains(err.Error(), "db-container") {
		t.Fatalf("health-command startService error = %v", err)
	}
}

func TestHeaderRewriteProxyForcesIdentityEncoding(t *testing.T) {
	target, _ := url.Parse("http://127.0.0.1:3310")
	proxy := newHeaderRewriteProxy(target, "localhost", PublicRewriteConfig{})
	req := httptest.NewRequest("GET", "https://preview.example.test/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	proxy.Director(req)
	if got := req.Header.Get("Accept-Encoding"); got != "identity" {
		t.Fatalf("Accept-Encoding = %q", got)
	}
}

func TestRoutedHeaderRewriteProxyRoutesWebSocketOnPathBoundary(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "primary")
	}))
	defer primary.Close()
	cable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "cable.localhost:8080" {
			t.Errorf("cable Host = %q", r.Host)
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("upstream response does not support hijacking")
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nX-Upstream: cable\r\n\r\n")
		_ = rw.Flush()
	}))
	defer cable.Close()
	primaryURL, _ := url.Parse(primary.URL)
	handler := newRoutedHeaderRewriteProxy(primaryURL, "localhost", PublicRewriteConfig{}, []publicProxyRoute{{Path: "/cable", Target: cable.URL, HostHeader: "cable.localhost:8080"}})
	server := httptest.NewServer(handler)
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn, err := net.Dial("tcp", serverURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(conn, "GET /cable HTTP/1.1\r\nHost: preview.example.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	_ = conn.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("X-Upstream") != "cable" {
		t.Fatalf("websocket response = %d %#v", resp.StatusCode, resp.Header)
	}

	boundaryResp, err := http.Get(server.URL + "/cable-admin")
	if err != nil {
		t.Fatal(err)
	}
	defer boundaryResp.Body.Close()
	body, _ := io.ReadAll(boundaryResp.Body)
	if string(body) != "primary" {
		t.Fatalf("boundary request routed to %q", body)
	}
}

func TestRoutedOriginRewritesToPublicWebSocketPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<script>new WebSocket("ws://cable.localhost:8080/cable")</script>`)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	handler := newRoutedHeaderRewriteProxy(target, "localhost", PublicRewriteConfig{}, []publicProxyRoute{{Path: "/cable", Target: upstream.URL, Origins: []string{"ws://cable.localhost:8080/cable"}}})
	req := httptest.NewRequest("GET", "https://preview.example.test/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, `wss://preview.example.test/cable`) || strings.Contains(body, `cable.localhost`) {
		t.Fatalf("rewritten body = %s", body)
	}
}

func TestServicePortPlanValidatesPublicRoutesAndHostIP(t *testing.T) {
	valid, err := servicePortPlan(ServiceConfig{Ports: map[string]PortConfig{
		"http":  {Container: 3000},
		"cable": {Container: 8080, HostIP: "100.64.0.4", PublicPath: "/cable", PublicOrigins: []string{"ws://cable.localhost:8080/cable"}},
	}, PrimaryPort: "http"})
	if err != nil || len(valid) != 2 || valid[0].HostIP != "100.64.0.4" {
		t.Fatalf("valid plan = %#v, %v", valid, err)
	}
	for _, svc := range []ServiceConfig{
		{Ports: map[string]PortConfig{"http": {Container: 3000}, "cable": {Container: 8080, PublicPath: "cable"}}, PrimaryPort: "http"},
		{Ports: map[string]PortConfig{"http": {Container: 3000}, "a": {Container: 1, PublicPath: "/cable"}, "b": {Container: 2, PublicPath: "/cable"}}, PrimaryPort: "http"},
		{Ports: map[string]PortConfig{"http": {Container: 3000}, "cable": {Container: 8080, HostIP: "not-an-ip"}}, PrimaryPort: "http"},
	} {
		if _, err := servicePortPlan(svc); err == nil {
			t.Fatalf("expected invalid port plan for %#v", svc)
		}
	}
}

func TestPublicPathStartsProxyWithoutTunnelHostHeader(t *testing.T) {
	ps := PreviewService{Name: "web", URL: "http://127.0.0.1:3000", OriginURL: "http://127.0.0.1:3000", Ports: map[string]PreviewPort{
		"http":  {Name: "http", URL: "http://127.0.0.1:3000", Primary: true},
		"cable": {Name: "cable", URL: "http://127.0.0.1:8080"},
	}}
	svc := ServiceConfig{Ports: map[string]PortConfig{
		"http":  {Container: 3000},
		"cable": {Container: 8080, PublicPath: "/cable", PublicOrigins: []string{"ws://cable.localhost:8080/cable"}},
	}, PrimaryPort: "http"}
	got, _, err := exposeServiceThroughHeaderRewriteProxy("route-only", "web", ps, svc, false, func(previewID, service, runtime, containerID, originURL, hostHeader string, rewrite PublicRewriteConfig, routes []publicProxyRoute, h HealthConfig, listenHost string) (string, int, error) {
		if hostHeader != "" || len(routes) != 1 || routes[0].Path != "/cable" {
			t.Fatalf("proxy args host=%q routes=%#v", hostHeader, routes)
		}
		return "http://127.0.0.1:4000", os.Getpid(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ProxyURL != "http://127.0.0.1:4000" {
		t.Fatalf("route-only proxy was not started: %#v", got)
	}
}

func TestLogsFallsBackToSnapshotAfterContainerCleanup(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.upsertPreview(PreviewRecord{ID: "postmortem", Project: "demo", Status: "dead", CreatedAt: nowUTC()}); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(a.Home, "logs", "postmortem", "web.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("mysql crashed\nweb exited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("postmortem", PreviewService{Name: "web", Runtime: "docker", Status: "dead", LogPath: logPath}); err != nil {
		t.Fatal(err)
	}
	result, err := a.Logs("postmortem", "web", 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(result["lines"].([]string), "\n"); !strings.Contains(got, "mysql crashed") {
		t.Fatalf("snapshot logs = %q", got)
	}
}

func TestComposeUpFailureWithoutTargetIDSnapshotsProjectLogs(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("VIVERO_HOME", t.TempDir())
	t.Setenv("FAKE_DOCKER_COMPOSE_UP_FAIL", "mysql dependency crashed")
	source := t.TempDir()
	compose := "services:\n  web:\n    image: example.test/web\n"
	if err := os.WriteFile(filepath.Join(source, "compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	svc := ServiceConfig{Source: "app", Runtime: "compose", Compose: ComposeConfig{File: "compose.yml", Service: "web"}}
	cfg := ProjectConfig{Project: ProjectMeta{Name: "compose-failure"}, Services: map[string]ServiceConfig{"web": svc}}
	state, err := a.startService(UpRequest{ID: "compose-up-failure"}, "web", svc, map[string]PreviewSource{"app": {Name: "app", Path: source}}, cfg, false, true)
	if err == nil || !strings.Contains(err.Error(), "mysql dependency crashed") {
		t.Fatalf("start error = %v", err)
	}
	if state.ContainerID != "" {
		t.Fatalf("failed compose target unexpectedly returned id: %#v", state)
	}
	body, readErr := os.ReadFile(state.LogPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(body), "mysql dependency crashed") {
		t.Fatalf("compose failure log snapshot = %s", body)
	}
	_ = removeDockerComposeProject("compose-up-failure", "web")
}

func TestComposeSetupFailureReturnsDependencyLogsBeforeCleanup(t *testing.T) {
	installFakeDocker(t)
	stateDir := os.Getenv("FAKE_DOCKER_STATE")
	previewID := "compose-setup-failure"
	project := dockerComposeProjectName(previewID, "web")
	for path, body := range map[string]string{
		"compose-db.pid":             "99999999",
		"compose-db.compose-project": project,
		"compose-db.log":             "mysql setup dependency crashed\n",
	} {
		if err := os.WriteFile(filepath.Join(stateDir, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "compose.yml"), []byte("services:\n  web:\n    image: example.test/web\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := ServiceConfig{Source: "app", Runtime: "compose", Compose: ComposeConfig{File: "compose.yml", Service: "web"}}
	out, err := runDockerComposeOneShot(t.TempDir(), "compose-failure", previewID, "web", svc, map[string]PreviewSource{"app": {Name: "app", Path: source}}, nil, RuntimeCommand{Shell: "echo setup failed; exit 7"})
	if err == nil {
		t.Fatal("expected compose setup failure")
	}
	if !strings.Contains(string(out), "mysql setup dependency crashed") {
		t.Fatalf("setup output did not preserve dependency logs: %s\nerror: %v", out, err)
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, "compose-db.compose-project")); !os.IsNotExist(statErr) {
		t.Fatalf("compose setup project was not cleaned after snapshot: %v", statErr)
	}
}

func TestExecTimeoutReturnsPartialOutputAndTimedOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	bin := t.TempDir()
	dockerPath := filepath.Join(bin, "docker")
	if err := os.WriteFile(dockerPath, []byte("#!/bin/sh\ncase \"$*\" in *'kill -TERM'*) exit 0;; esac\necho partial-stdout\necho partial-stderr >&2\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.upsertPreview(PreviewRecord{ID: "exec-timeout", Project: "demo", Status: "running", CreatedAt: nowUTC()}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("exec-timeout", PreviewService{Name: "web", Runtime: "compose", ContainerID: "container-1", Status: "healthy"}); err != nil {
		t.Fatal(err)
	}
	result, err := a.Exec("exec-timeout", "web", []string{"long-command"}, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if result["timedOut"] != true || result["exitCode"] != 124 {
		t.Fatalf("timeout result = %#v", result)
	}
	if !strings.Contains(result["stdout"].(string), "partial-stdout") || !strings.Contains(result["stderr"].(string), "partial-stderr") {
		t.Fatalf("partial output missing: %#v", result)
	}
}

func TestExecCLILeavesCommandTimeoutAfterDoubleDashUntouched(t *testing.T) {
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	bin := t.TempDir()
	dockerPath := filepath.Join(bin, "docker")
	if err := os.WriteFile(dockerPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "exec-cli", Project: "demo", Status: "dead", CreatedAt: nowUTC()}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("exec-cli", PreviewService{Name: "web", Runtime: "docker", ContainerID: "container-1", Status: "healthy"}); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"exec", "exec-cli", "web", "--timeout", "1s", "--json", "--no-input", "--", "pytest", "--timeout", "9s"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	if got := fmt.Sprint(result["stdout"]); !strings.Contains(got, "pytest --timeout 9s") {
		t.Fatalf("command timeout flag was consumed: %q", got)
	}
}

func TestDockerExecFallsBackForContainersWithoutShell(t *testing.T) {
	bin := t.TempDir()
	dockerPath := filepath.Join(bin, "docker")
	script := `#!/bin/sh
shift
shift
if [ "${1:-}" = "sh" ]; then
  echo 'OCI runtime exec failed: exec: "sh": executable file not found in $PATH' >&2
  exit 126
fi
printf '%s\n' "$*"
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	stdout, stderr, exit, err := dockerExecWithTimeout("shell-free", []string{"app-binary", "status"}, time.Second)
	if err != nil || exit != 0 || strings.TrimSpace(stderr) != "" || !strings.Contains(stdout, "app-binary status") {
		t.Fatalf("fallback stdout=%q stderr=%q exit=%d err=%v", stdout, stderr, exit, err)
	}
}

func TestDockerExecTimeoutKillsTrackedInContainerProcess(t *testing.T) {
	bin := t.TempDir()
	dockerPath := filepath.Join(bin, "docker")
	if err := os.WriteFile(dockerPath, []byte("#!/bin/sh\nshift\nshift\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	pidPath := filepath.Join(t.TempDir(), "command.pid")
	command := fmt.Sprintf("echo $$ > %q; exec sleep 30", pidPath)
	stdout, stderr, _, err := dockerExecWithTimeout("local-fake", []string{"/bin/sh", "-c", command}, 500*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	body, readErr := os.ReadFile(pidPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(body)))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	deadline := time.Now().Add(time.Second)
	for processExists(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(pid) {
		t.Fatalf("timed-out in-container process %d is still running", pid)
	}
}
