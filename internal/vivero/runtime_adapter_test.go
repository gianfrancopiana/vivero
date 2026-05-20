package vivero

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func installFakeDocker(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake docker helper uses POSIX shell")
	}
	if testing.Short() {
		t.Skip("fake docker integration test")
	}
	binDir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "docker-state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -eu
state="${FAKE_DOCKER_STATE:?}"
mkdir -p "$state"
cmd="${1:-}"
if [ $# -gt 0 ]; then shift; fi
case "$cmd" in
  network)
    sub="${1:-}"
    if [ $# -gt 0 ]; then shift; fi
    name="${1:-}"
    case "$sub" in
      inspect)
        if [ -f "$state/network-$name" ]; then exit 0; fi
        echo "No such network: $name" >&2
        exit 1
        ;;
      create)
        touch "$state/network-$name"
        echo "$name"
        exit 0
        ;;
      rm)
        if [ "${FAKE_DOCKER_NETWORK_RM_FAIL:-}" != "" ]; then echo "$FAKE_DOCKER_NETWORK_RM_FAIL" >&2; exit 7; fi
        rm -f "$state/network-$name"
        exit 0
        ;;
    esac
    echo "unsupported docker network command $sub" >&2
    exit 2
    ;;
  volume)
    sub="${1:-}"
    if [ $# -gt 0 ]; then shift; fi
    name="${1:-}"
    case "$sub" in
      inspect)
        if [ -f "$state/volume-$name" ]; then echo "$name"; exit 0; fi
        echo "No such volume: $name" >&2
        exit 1
        ;;
      create)
        touch "$state/volume-$name"
        echo "$name"
        exit 0
        ;;
      rm)
        if [ "${FAKE_DOCKER_VOLUME_RM_FAIL:-}" != "" ]; then echo "$FAKE_DOCKER_VOLUME_RM_FAIL" >&2; exit 7; fi
        rm -f "$state/volume-$name"
        echo "$name"
        exit 0
        ;;
    esac
    echo "unsupported docker volume command $sub" >&2
    exit 2
    ;;
  build)
    tag=""
    dockerfile=""
    context=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --tag|-t) tag="$2"; shift 2 ;;
        --file|-f) dockerfile="$2"; shift 2 ;;
        --build-arg) shift 2 ;;
        --) shift; break ;;
        -*) echo "unsupported docker build flag $1" >&2; exit 2 ;;
        *) context="$1"; shift ;;
      esac
    done
    printf '%s|%s|%s\n' "${tag:-untagged}" "$dockerfile" "$context" >> "$state/builds"
    echo "built ${tag:-untagged}"
    exit 0
    ;;
  ps)
    preview_filter=""
    while [ $# -gt 0 ]; do
      case "$1" in
        -a|-q|-aq|-qa) shift ;;
        --filter)
          case "$2" in
            label=vivero.preview=*) preview_filter="${2#label=vivero.preview=}" ;;
          esac
          shift 2
          ;;
        *) shift ;;
      esac
    done
    for f in "$state"/*.pid; do
      [ -e "$f" ] || continue
      name="$(basename "$f" .pid)"
      if [ -n "$preview_filter" ]; then
        [ -f "$state/$name.preview" ] || continue
        [ "$(cat "$state/$name.preview")" = "$preview_filter" ] || continue
      fi
      echo "$name"
    done
    exit 0
    ;;
  rm)
    if [ "${FAKE_DOCKER_RM_FAIL:-}" != "" ]; then echo "$FAKE_DOCKER_RM_FAIL" >&2; exit 7; fi
    if [ "${1:-}" = "-f" ]; then shift; fi
    while [ $# -gt 0 ]; do
      name="${1:-}"
      if [ -n "$name" ] && [ -f "$state/$name.pid" ]; then
        kill "$(cat "$state/$name.pid")" 2>/dev/null || true
        rm -f "$state/$name.pid" "$state/$name.preview" "$state/$name.service" "$state/$name.cwd"
      fi
      shift || true
    done
    exit 0
    ;;
  logs)
    if [ "${1:-}" = "--tail" ]; then shift 2; fi
    name="${1:-}"
    if [ -f "$state/$name.log" ]; then cat "$state/$name.log"; fi
    exit 0
    ;;
  port)
    name="${1:-}"
    query="${2:-}"
    if [ -f "$state/$name.ports" ]; then
      awk -F'|' -v q="$query" '$1 == q { print $2; found=1; exit } END { exit found ? 0 : 1 }' "$state/$name.ports"
      exit $?
    fi
    exit 1
    ;;
  exec)
    name="${1:-}"
    shift || true
    cwd="$(cat "$state/$name.cwd" 2>/dev/null || pwd)"
    cd "$cwd"
    exec "$@"
    ;;
  run)
    if [ "${FAKE_DOCKER_REJECT_DOCKER_HOST:-}" != "" ] && [ "${DOCKER_HOST:-}" = "$FAKE_DOCKER_REJECT_DOCKER_HOST" ]; then
      echo "docker client env leaked DOCKER_HOST" >&2
      exit 9
    fi
    detach=0
    name=""
    volume=""
    workdir=""
    preview_label=""
    service_label=""
    published=""
    mount_count=0
    while [ $# -gt 0 ]; do
      case "$1" in
        --rm) shift ;;
        --detach|-d) detach=1; shift ;;
        --name) name="$2"; shift 2 ;;
        --volume|-v) volume="$2"; shift 2 ;;
        --workdir|-w) workdir="$2"; shift 2 ;;
        --label)
          case "$2" in
            vivero.preview=*) preview_label="${2#vivero.preview=}" ;;
            vivero.service=*) service_label="${2#vivero.service=}" ;;
          esac
          shift 2
          ;;
        --publish) published="$published
$2"; shift 2 ;;
        --mount) mount_count=$((mount_count + 1)); shift 2 ;;
        --cpus|--memory|--env|--env-file|--network|--network-alias) shift 2 ;;
        --) shift; break ;;
        -*) echo "unsupported docker flag $1" >&2; exit 2 ;;
        *) image="$1"; shift; break ;;
      esac
    done
    : "${image:=fake-image}"
    if [ -z "$name" ]; then name="fake-container"; fi
    hostwork="$workdir"
    case "$volume" in
      *:/app)
        hostroot="${volume%:/app}"
        case "$workdir" in
          /app*) hostwork="$hostroot${workdir#/app}" ;;
          "") hostwork="$hostroot" ;;
        esac
        ;;
    esac
    if [ -z "$hostwork" ]; then hostwork="$(pwd)"; fi
    mkdir -p "$hostwork"
    printf '%s' "$hostwork" > "$state/$name.cwd"
    [ -n "$preview_label" ] && printf '%s' "$preview_label" > "$state/$name.preview"
    [ -n "$service_label" ] && printf '%s' "$service_label" > "$state/$name.service"
    : > "$state/$name.ports"
    printf '%s\n' "$published" | while IFS= read -r publish; do
      [ -n "$publish" ] || continue
      container="${publish##*:}"
      hostpart="${publish%:*}"
      hostport="${hostpart##*:}"
      hostip="${hostpart%:*}"
      if [ "$hostip" = "$hostpart" ]; then hostip="127.0.0.1"; fi
      if [ -z "$hostport" ]; then hostport="${container%%/*}"; fi
      protocol="tcp"
      case "$container" in */*) protocol="${container#*/}"; container="${container%%/*}" ;; esac
      printf '%s/%s|%s:%s\n' "$container" "$protocol" "$hostip" "$hostport" >> "$state/$name.ports"
    done
    if [ "$detach" = "1" ]; then
      (
        cd "$hostwork"
        "$@" > "$state/$name.log" 2>&1 &
        echo $! > "$state/$name.pid"
      )
      if [ "${FAKE_DOCKER_WARN:-}" != "" ]; then echo "$FAKE_DOCKER_WARN" >&2; fi
      echo "$name"
      exit 0
    fi
    if [ "$name" = "fake-container" ] && [ "$mount_count" -gt 0 ]; then
      echo "copied volumes"
      exit 0
    fi
    cd "$hostwork"
    exec "$@"
    ;;
esac
echo "unsupported docker command $cmd" >&2
exit 2
`
	dockerPath := filepath.Join(binDir, "docker")
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_DOCKER_STATE", stateDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestLoadProjectConfigRejectsHostRuntime(t *testing.T) {
	root := t.TempDir()
	cfg := []byte(`project:
  name: naked-app
sources:
  app:
    path: .
services:
  web:
    source: app
    runtime: host
    command: npm run dev
    port: 3000
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := loadProjectConfig(root)
	if err == nil {
		t.Fatal("expected host runtime to be rejected")
	}
	if !strings.Contains(err.Error(), "containers") {
		t.Fatalf("error should explain the container-only invariant: %v", err)
	}
}

func TestLoadProjectConfigRequiresContainerImage(t *testing.T) {
	root := t.TempDir()
	cfg := []byte(`project:
  name: missing-image
sources:
  app:
    path: .
services:
  web:
    source: app
    command: npm run dev
    port: 3000
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := loadProjectConfig(root)
	if err == nil {
		t.Fatal("expected services without images to be rejected")
	}
	if !strings.Contains(err.Error(), "must declare image") {
		t.Fatalf("error should require a container image: %v", err)
	}
}

func TestLoadProjectConfigAllowsServiceBuildWithoutImage(t *testing.T) {
	root := t.TempDir()
	cfg := []byte(`project:
  name: built-app
sources:
  app:
    path: .
services:
  web:
    source: app
    build:
      context: .
      dockerfile: Dockerfile.runtime
      tag: vivero/built-app-web:test
    command: npm run dev
    port: 3000
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	_, loaded, err := loadProjectConfig(root)
	if err != nil {
		t.Fatalf("service build should satisfy the container image requirement: %v", err)
	}
	if loaded.Services["web"].Build.Tag != "vivero/built-app-web:test" {
		t.Fatalf("build config not loaded: %#v", loaded.Services["web"].Build)
	}
}

func TestLoadProjectConfigRejectsBackingServiceWithoutImage(t *testing.T) {
	root := t.TempDir()
	cfg := []byte(`project:
  name: bad-backing
services:
  web:
    image: python:3.12-alpine
backingServices:
  redis:
    health:
      command: redis-cli ping
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := loadProjectConfig(root)
	if err == nil {
		t.Fatal("expected backing service without image to be rejected")
	}
	if !strings.Contains(err.Error(), "backing service redis must declare image") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadProjectConfigRejectsServiceBackingNameCollision(t *testing.T) {
	root := t.TempDir()
	cfg := []byte(`project:
  name: name-collision
services:
  redis:
    image: redis:7
backingServices:
  redis:
    image: redis:7
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := loadProjectConfig(root)
	if err == nil {
		t.Fatal("expected app/backing service name collision to be rejected")
	}
	if !strings.Contains(err.Error(), "service name redis is declared in both services and backingServices") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerRunArgsMountSourceAndPublishPort(t *testing.T) {
	args, err := dockerRunArgs(dockerServiceSpec{
		PreviewID: "demo-pr-17",
		Service:   "web",
		Image:     "python:3.12-alpine",
		Command:   "python3 -m http.server 3310 --bind 0.0.0.0",
		Source:    "/tmp/demo-app",
		Workdir:   "frontend",
		Port:      3310,
		Env:       map[string]string{"RAILS_ENV": "development"},
		Network:   dockerNetworkName("demo-pr-17"),
		Alias:     "web",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run --detach",
		"--name " + dockerContainerName("demo-pr-17", "web"),
		"--label vivero.preview=demo-pr-17",
		"--label vivero.service=web",
		"--publish 127.0.0.1:3310:3310",
		"--network " + dockerNetworkName("demo-pr-17"),
		"--network-alias web",
		"--volume /tmp/demo-app:/app",
		"--workdir /app/frontend",
		"--env RAILS_ENV",
		"python:3.12-alpine",
		"/bin/sh -lc python3 -m http.server 3310 --bind 0.0.0.0",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker args missing %q: %v", want, args)
		}
	}
}

func TestDockerBuildArgsUsesContextDockerfileTagAndArgs(t *testing.T) {
	args, err := dockerBuildArgs(dockerBuildSpec{
		Tag:        "vivero/demo-web:test",
		Context:    "/tmp/vivero-example",
		Dockerfile: "/tmp/vivero-example/Dockerfile.runtime",
		Args:       map[string]string{"RUBY_VERSION": "3.4.3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"build",
		"--tag vivero/demo-web:test",
		"--file /tmp/vivero-example/Dockerfile.runtime",
		"--build-arg RUBY_VERSION=3.4.3",
		"/tmp/vivero-example",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker build args missing %q: %v", want, args)
		}
	}
}

func TestDockerBuildSpecForServiceResolvesAppOwnedDockerfile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	dockerfile := filepath.Join(root, "docker", "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := dockerBuildSpecForService(root, "app-owned", "preview-1", "web", ImageBuildConfig{
		Context:    ".",
		Dockerfile: "docker/Dockerfile",
		Tag:        "vivero/app-owned-web:test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Dockerfile != dockerfile {
		t.Fatalf("dockerfile should resolve from project path; got %q want %q", spec.Dockerfile, dockerfile)
	}
	if spec.Context != root {
		t.Fatalf("context should resolve from project path; got %q want %q", spec.Context, root)
	}
}

func TestDockerBuildSpecRejectsContextOutsideProjectRoot(t *testing.T) {
	root := t.TempDir()
	_, err := dockerBuildSpecForService(root, "escape-app", "preview-1", "web", ImageBuildConfig{Context: ".."})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected context escape error, got %v", err)
	}
	_, err = dockerBuildSpecForService(root, "escape-app", "preview-1", "web", ImageBuildConfig{Context: root})
	if err == nil || !strings.Contains(err.Error(), "must be relative") {
		t.Fatalf("expected absolute context error, got %v", err)
	}
}

func TestDockerBuildSpecRejectsDockerfileOutsideContextRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := dockerBuildSpecForService(root, "escape-app", "preview-1", "web", ImageBuildConfig{
		Context:    "app",
		Dockerfile: "../Dockerfile",
	})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected dockerfile escape error, got %v", err)
	}
}

func TestDockerRunOnceArgsUsesPreviewNetwork(t *testing.T) {
	args, err := dockerRunOnceArgs(dockerServiceSpec{
		PreviewID: "demo-pr-17",
		Service:   "web",
		Image:     "python:3.12-alpine",
		Source:    "/tmp/demo-app",
		Network:   dockerNetworkName("demo-pr-17"),
	}, "printf setup")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--network "+dockerNetworkName("demo-pr-17")) {
		t.Fatalf("one-shot args should use preview network: %v", args)
	}
}

func TestDockerRunArgsUsesProjectLifetimeVolumeNames(t *testing.T) {
	args, err := dockerRunArgs(dockerServiceSpec{
		Project:   "gumroad-main",
		PreviewID: "gumroad-pr-17",
		Service:   "gumroad-web",
		Image:     "alpine:latest",
		Volumes: []VolumeConfig{
			{Name: "bundle_path", Target: "/bundle_path", Lifetime: "project"},
			{Name: "tmp", Target: "/tmp/cache"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	projectVolume := dockerProjectVolumeName("gumroad-main", "gumroad-web", "bundle_path")
	previewVolume := dockerVolumeName("gumroad-pr-17", "gumroad-web", "tmp")
	for _, want := range []string{
		"source=" + projectVolume + ",target=/bundle_path",
		"source=" + previewVolume + ",target=/tmp/cache",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker args missing %q: %v", want, args)
		}
	}
	if strings.Contains(joined, dockerVolumeName("gumroad-pr-17", "gumroad-web", "bundle_path")) {
		t.Fatalf("project-lifetime volume should not be preview-scoped: %v", args)
	}
}

func TestNamedTunnelPublicURLUsesStableHostnameTemplate(t *testing.T) {
	url, err := publicURLForService(PublicConfig{
		Provider:         "cloudflare",
		Mode:             "named-tunnel",
		BaseDomain:       "preview.example.com",
		HostnameTemplate: "pr-{{ index .Metadata \"pr\" }}.{{ .BaseDomain }}",
	}, UpRequest{
		Project:  "demo",
		ID:       "demo-pr-17",
		Metadata: map[string]string{"pr": "17"},
	}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://pr-17.preview.example.com" {
		t.Fatalf("url = %s", url)
	}
}

func TestFixedPublicHostnameOverridesTemplate(t *testing.T) {
	url, err := publicURLForService(PublicConfig{
		Provider:         "cloudflare",
		Mode:             "named-tunnel",
		BaseDomain:       "preview.example.com",
		Hostname:         "staging.preview.example.com",
		HostnameTemplate: "{{ .PreviewID }}.{{ .BaseDomain }}",
	}, UpRequest{Project: "demo", ID: "demo-pr-17"}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://staging.preview.example.com" {
		t.Fatalf("url = %s", url)
	}
}

func TestPublicPreviewRouterReturnsGoneForDeadPreview(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.saveProject(t.TempDir(), ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Public:  PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com", InactiveBehavior: "410"},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "python:3.12-alpine", Public: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created := nowUTC()
	if err := a.upsertPreview(PreviewRecord{ID: "demo-pr-17", Project: "demo", Status: "dead", CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("demo-pr-17", PreviewService{Name: "web", Status: "dead", URL: "https://pr-17.preview.example.com", OriginURL: "http://127.0.0.1:1", StartedAt: created}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "http://pr-17.preview.example.com/products", nil)
	req.Host = "pr-17.preview.example.com"
	rec := httptest.NewRecorder()
	a.controlPlaneHandler().ServeHTTP(rec, req)

	if rec.Code != 410 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "inactive") {
		t.Fatalf("inactive response should be explicit: %s", rec.Body.String())
	}
}

func TestPublicPreviewRouterProxiesActiveNamedTunnelHost(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/products" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("active preview"))
	}))
	defer backend.Close()
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.saveProject(t.TempDir(), ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Public:  PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com"},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "python:3.12-alpine", Public: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created := nowUTC()
	if err := a.upsertPreview(PreviewRecord{ID: "demo-pr-17", Project: "demo", Status: "running", CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("demo-pr-17", PreviewService{Name: "web", Status: "healthy", URL: "https://pr-17.preview.example.com", OriginURL: backend.URL, StartedAt: created}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "http://pr-17.preview.example.com/products", nil)
	req.Host = "pr-17.preview.example.com"
	req.Header.Set("X-Forwarded-Host", "localhost")
	rec := httptest.NewRecorder()
	a.controlPlaneHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "active preview" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestDockerRunArgsPassEnvNamesWithoutValues(t *testing.T) {
	args, err := dockerRunArgs(dockerServiceSpec{
		PreviewID: "demo-pr-17",
		Service:   "web",
		Image:     "alpine:latest",
		Env:       map[string]string{"SECRET_TOKEN": "super-secret-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "super-secret-value") {
		t.Fatalf("docker args leaked env value: %v", args)
	}
	if !strings.Contains(joined, "--env SECRET_TOKEN") {
		t.Fatalf("docker args should pass env by name: %v", args)
	}
}

func TestStartDockerServiceParsesContainerIDFromStdoutOnly(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("FAKE_DOCKER_WARN", "Pulling alpine:latest...")
	source := t.TempDir()
	a := &App{Home: t.TempDir()}
	id, err := a.startDockerService("demo", "demo-pr-17", "web", ServiceConfig{Source: "app", Image: "alpine:latest", Command: "sleep 60"}, map[string]PreviewSource{"app": {Path: source}}, map[string]string{"SECRET_TOKEN": "super-secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	defer runCmd("", nil, "docker", "rm", "-f", id)
	if id != dockerContainerName("demo-pr-17", "web") {
		t.Fatalf("container id should come from stdout only, got %q", id)
	}
	if strings.Contains(id, "Pulling") || strings.Contains(id, "\n") {
		t.Fatalf("container id included stderr output: %q", id)
	}
}

func TestPublicURLRejectsHostnameOutsideBaseDomain(t *testing.T) {
	_, err := publicURLForService(PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com", Hostname: "evil.example.net"}, UpRequest{Project: "demo", ID: "demo-pr-17"}, "web")
	if err == nil {
		t.Fatal("expected hostname outside base domain to be rejected")
	}
	if !strings.Contains(err.Error(), "base domain") {
		t.Fatalf("error should mention base domain: %v", err)
	}
}

func TestPublicURLRejectsPathComponents(t *testing.T) {
	_, err := publicURLForService(PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com", Hostname: "https://pr-17.preview.example.com/path"}, UpRequest{Project: "demo", ID: "demo-pr-17"}, "web")
	if err == nil {
		t.Fatal("expected path-bearing hostname to be rejected")
	}
	if !strings.Contains(err.Error(), "host-only") {
		t.Fatalf("error should mention host-only DNS names: %v", err)
	}
}

func TestValidateNamedPublicRoutesRejectsDuplicateHosts(t *testing.T) {
	err := validateNamedPublicRoutes(UpRequest{Project: "demo", ID: "demo-pr-17", Public: true}, ProjectConfig{
		Public: PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com", Hostname: "pr-17.preview.example.com"},
		Services: map[string]ServiceConfig{
			"api": {Image: "alpine:latest", Port: 3001, Public: true},
			"web": {Image: "alpine:latest", Port: 3000, Public: true},
		},
	})
	if err == nil {
		t.Fatal("expected duplicate fixed hostname to be rejected")
	}
	if !strings.Contains(err.Error(), "used by both") {
		t.Fatalf("error should identify duplicate host: %v", err)
	}
}

func TestPublicPreviewRouterRejectsUnknownPublicHost(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	req := httptest.NewRequest("GET", "http://unknown.preview.example.com/projects", nil)
	req.Host = "unknown.preview.example.com"
	rec := httptest.NewRecorder()
	a.controlPlaneHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown public host should not fall through to control plane, status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestPublicOriginPrefersHostOverForwardedHost(t *testing.T) {
	req := httptest.NewRequest("GET", "https://preview.trycloudflare.com/path", nil)
	req.Host = "preview.trycloudflare.com"
	req.Header.Set("X-Forwarded-Host", "evil.example.com")
	if got := publicOriginForIncomingRequest(req); got != "https://preview.trycloudflare.com" {
		t.Fatalf("public origin = %s", got)
	}
}

func TestPublicPreviewRouterRejectsForwardedPublicHostWithLocalHost(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	req := httptest.NewRequest("GET", "http://localhost/projects", nil)
	req.Host = "localhost"
	req.Header.Set("X-Forwarded-Host", "unknown.preview.example.com")
	rec := httptest.NewRecorder()
	a.controlPlaneHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("forwarded public host should not reach control plane, status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestPreviewServiceForPublicHostIgnoresOriginOnlyService(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.upsertPreview(PreviewRecord{ID: "demo-pr-17", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("demo-pr-17", PreviewService{Name: "internal", Status: "healthy", URL: "https://internal.example.com", OriginURL: "https://internal.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := a.previewServiceForPublicHost("internal.example.com"); ok {
		t.Fatal("origin-only service should not be exposed through the public router")
	}
}

func TestValidateNamedPublicRouteConflictsRejectsExistingPreviewHost(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.upsertPreview(PreviewRecord{ID: "demo-pr-16", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("demo-pr-16", PreviewService{Name: "web", Status: "healthy", URL: "https://pr-17.preview.example.com", OriginURL: "http://127.0.0.1:3310"}); err != nil {
		t.Fatal(err)
	}
	err = a.validateNamedPublicRouteConflicts(UpRequest{Project: "demo", ID: "demo-pr-17", Public: true}, ProjectConfig{
		Public: PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com", Hostname: "pr-17.preview.example.com"},
		Services: map[string]ServiceConfig{
			"web": {Image: "alpine:latest", Port: 3000, Public: true},
		},
	})
	if err == nil {
		t.Fatal("expected existing public hostname conflict")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Fatalf("error should identify existing route owner: %v", err)
	}
}

func TestUpValidatesNamedPublicRouteBeforeStartingDockerNetwork(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.upsertPreview(PreviewRecord{ID: "demo-pr-16", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("demo-pr-16", PreviewService{Name: "web", Status: "healthy", URL: "https://pr-17.preview.example.com", OriginURL: "http://127.0.0.1:3310"}); err != nil {
		t.Fatal(err)
	}
	_, err = a.saveProject(t.TempDir(), ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Public:  PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com", Hostname: "pr-17.preview.example.com"},
		BackingServices: map[string]BackingConfig{
			"redis": {Image: "redis:7-alpine", Command: "redis-server"},
		},
		Services: map[string]ServiceConfig{
			"web": {
				Image:  "alpine:latest",
				Port:   3000,
				Public: true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Up(UpRequest{Project: "demo", ID: "demo-pr-17", Public: true})
	if err == nil {
		t.Fatal("expected public hostname conflict")
	}
	if _, statErr := os.Stat(filepath.Join(os.Getenv("FAKE_DOCKER_STATE"), "network-"+dockerNetworkName("demo-pr-17"))); !os.IsNotExist(statErr) {
		t.Fatalf("docker network should not be created before route validation, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(os.Getenv("FAKE_DOCKER_STATE"), "builds")); !os.IsNotExist(statErr) {
		t.Fatalf("docker build should not run before route validation, stat err=%v", statErr)
	}
}

func TestUpRejectsInvalidNamedPublicRouteBeforeStartingDockerNetwork(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	root := writeConfigDoctorFile(t, `project:
  name: demo
public:
  provider: cloudflare
  mode: named-tunnel
services:
  web:
    image: alpine:latest
    port: 3000
    public: true
`)
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}
	_, err = a.Up(UpRequest{Project: "demo", ID: "demo-pr-18", Public: true})
	if err == nil || !strings.Contains(err.Error(), "public.baseDomain") {
		t.Fatalf("expected invalid public route error before docker startup, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(os.Getenv("FAKE_DOCKER_STATE"), "network-"+dockerNetworkName("demo-pr-18"))); !os.IsNotExist(statErr) {
		t.Fatalf("docker network should not be created before invalid public route is rejected, stat err=%v", statErr)
	}
}

func TestDownSafeDirtySourceStillRemovesDockerNetwork(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := t.TempDir()
	if out, err := runCmd(source, nil, "git", "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	_, _ = runCmd(source, nil, "git", "config", "user.email", "test@example.com")
	_, _ = runCmd(source, nil, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("clean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(source, nil, "git", "add", "."); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	if out, err := runCmd(source, nil, "git", "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "dirty-pr", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveSource("dirty-pr", PreviewSource{Name: "app", Mode: "worktree", Path: source, Owned: true}); err != nil {
		t.Fatal(err)
	}
	if err := ensureDockerNetwork("dirty-pr"); err != nil {
		t.Fatal(err)
	}
	_, err = a.Down("dirty-pr", "safe")
	if err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("expected safe dirty-source error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(os.Getenv("FAKE_DOCKER_STATE"), "network-"+dockerNetworkName("dirty-pr"))); !os.IsNotExist(statErr) {
		t.Fatalf("docker network should be removed despite safe-mode dirty source, stat err=%v", statErr)
	}
}

func TestDownDiscardRemovesDependencyVolumes(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.saveProject(t.TempDir(), ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{
			"web": {Image: "alpine:latest", DependencyVolumes: []VolumeConfig{{Name: "bundle", Target: "/bundle"}}},
		},
		BackingServices: map[string]BackingConfig{
			"redis": {Image: "redis:7-alpine", DependencyVolumes: []VolumeConfig{{Name: "data", Target: "/data"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "volume-pr", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	state := os.Getenv("FAKE_DOCKER_STATE")
	volumes := []string{
		dockerVolumeName("volume-pr", "web", "bundle"),
		dockerVolumeName("volume-pr", "redis", "data"),
	}
	for _, name := range volumes {
		if err := os.WriteFile(filepath.Join(state, "volume-"+name), []byte("exists"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Down("volume-pr", "discard"); err != nil {
		t.Fatal(err)
	}
	for _, name := range volumes {
		if _, statErr := os.Stat(filepath.Join(state, "volume-"+name)); !os.IsNotExist(statErr) {
			t.Fatalf("dependency volume %s should be removed on discard, stat err=%v", name, statErr)
		}
	}
}

func TestDownDiscardPreservesProjectLifetimeDependencyVolumes(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.saveProject(t.TempDir(), ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{
			"web": {Image: "alpine:latest", DependencyVolumes: []VolumeConfig{
				{Name: "bundle", Target: "/bundle", Lifetime: "project"},
				{Name: "tmp", Target: "/tmp/cache"},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "volume-pr", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	state := os.Getenv("FAKE_DOCKER_STATE")
	projectVolume := dockerProjectVolumeName("demo", "web", "bundle")
	previewVolume := dockerVolumeName("volume-pr", "web", "tmp")
	for _, name := range []string{projectVolume, previewVolume} {
		if err := os.WriteFile(filepath.Join(state, "volume-"+name), []byte("exists"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Down("volume-pr", "discard"); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(state, "volume-"+projectVolume)); statErr != nil {
		t.Fatalf("project-lifetime dependency volume %s should survive discard: %v", projectVolume, statErr)
	}
	if _, statErr := os.Stat(filepath.Join(state, "volume-"+previewVolume)); !os.IsNotExist(statErr) {
		t.Fatalf("preview-lifetime dependency volume %s should be removed on discard, stat err=%v", previewVolume, statErr)
	}
}

func TestRunSetupStepsSkipsOncePerProjectAfterSuccessfulRun(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := t.TempDir()
	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "alpine:latest"},
		},
		Setup: SetupConfig{AfterSeeds: []SetupStep{
			{Service: "web", Policy: "once-per-project", Command: "printf x >> setup-count.txt"},
		}},
	}
	sources := map[string]PreviewSource{"app": {Name: "app", Mode: "external", Path: source}}
	if err := a.runSetupSteps("first-pr", cfg.Setup.AfterSeeds, cfg, sources, warmRunState{}); err != nil {
		t.Fatal(err)
	}
	if err := a.runSetupSteps("second-pr", cfg.Setup.AfterSeeds, cfg, sources, warmRunState{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(source, "setup-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "x" {
		t.Fatalf("once-per-project setup should run once, got %q", got)
	}
}

func TestWaitDockerHealthCommandUsesHealthDeadline(t *testing.T) {
	installFakeDocker(t)
	started := time.Now()
	err := waitDockerHealthCommand("fake-container", HealthConfig{Command: "sleep 5", Interval: "5ms"}, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected health command timeout")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("health command ignored deadline; elapsed=%s err=%v", elapsed, err)
	}
}

func TestBackingServiceEnvDoesNotIncludeProjectSecrets(t *testing.T) {
	a := &App{Home: t.TempDir()}
	if err := writeEnvFile(a.secretFile("demo"), map[string]string{"TOKEN": "super-secret"}); err != nil {
		t.Fatal(err)
	}
	withoutSecrets := a.envForService("demo", ServiceConfig{Env: map[string]string{"SERVICE": "ok"}}, false)
	if _, ok := withoutSecrets["TOKEN"]; ok {
		t.Fatalf("backing service env should not include project secrets: %#v", withoutSecrets)
	}
	if withoutSecrets["SERVICE"] != "ok" {
		t.Fatalf("service env missing: %#v", withoutSecrets)
	}
	withSecrets := a.envForService("demo", ServiceConfig{}, true)
	if withSecrets["TOKEN"] != "super-secret" {
		t.Fatalf("app service env should include project secrets: %#v", withSecrets)
	}
}

func TestStopPreviewServiceResourcesPreservesContainerIDOnDockerFailure(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	stopped, err := a.stopPreviewServiceResources("demo-pr-17", "web", PreviewService{Name: "web", Status: "running", ContainerID: "missing-container"})
	if err == nil {
		t.Fatal("expected docker rm failure")
	}
	if stopped.ContainerID != "missing-container" {
		t.Fatalf("container id should be preserved after failed cleanup, got %q", stopped.ContainerID)
	}
}

func TestDockerResourceNamesKeepStableHashWhenSanitizedOrTruncated(t *testing.T) {
	if dockerContainerName("a/b", "web") == dockerContainerName("a-b", "web") {
		t.Fatal("sanitized docker names should retain a hash to avoid collisions")
	}
	longA := "preview-" + strings.Repeat("a", 160) + "x"
	longB := "preview-" + strings.Repeat("a", 160) + "y"
	nameA := dockerContainerName(longA, "web")
	nameB := dockerContainerName(longB, "web")
	if nameA == nameB {
		t.Fatal("truncated docker names should retain a hash to avoid collisions")
	}
	if len(nameA) > 120 || len(nameB) > 120 {
		t.Fatalf("docker names should stay bounded: %d %d", len(nameA), len(nameB))
	}
}

func TestRemoveDockerNetworkIgnoresDockerNotFoundWording(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("FAKE_DOCKER_NETWORK_RM_FAIL", "Error response from daemon: network demo not found")
	if err := removeDockerNetwork("demo-pr-17"); err != nil {
		t.Fatalf("missing docker networks should be treated as already removed: %v", err)
	}
}

func TestDockerRunArgsRejectsUnsafeVolumeTarget(t *testing.T) {
	_, err := dockerRunArgs(dockerServiceSpec{
		PreviewID: "demo-pr-17",
		Service:   "web",
		Image:     "alpine:latest",
		Volumes:   []VolumeConfig{{Name: "bundle", Target: "/bundle,readonly"}},
	})
	if err == nil {
		t.Fatal("expected comma-bearing mount target to be rejected")
	}
	if !strings.Contains(err.Error(), "volume target") {
		t.Fatalf("error should mention volume target: %v", err)
	}
}

func TestDockerRunArgsRejectsInvalidEnvName(t *testing.T) {
	_, err := dockerRunArgs(dockerServiceSpec{
		PreviewID: "demo-pr-17",
		Service:   "web",
		Image:     "alpine:latest",
		Env:       map[string]string{"BAD=NAME": "value"},
	})
	if err == nil {
		t.Fatal("expected invalid env name to be rejected")
	}
	if !strings.Contains(err.Error(), "env name") {
		t.Fatalf("error should mention env name: %v", err)
	}
}

func TestDockerRunArgsAllowsDottedEnvName(t *testing.T) {
	_, err := dockerRunArgs(dockerServiceSpec{
		PreviewID: "demo-pr-17",
		Service:   "elasticsearch",
		Image:     "elasticsearch:7.9.3",
		Env:       map[string]string{"discovery.type": "single-node"},
	})
	if err != nil {
		t.Fatalf("docker env names should allow service-specific dotted keys: %v", err)
	}
}

func TestLoadProjectConfigRejectsInvalidRuntimeInputs(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "service env name",
			yaml: `project:
  name: invalid-runtime
services:
  web:
    image: alpine:latest
    env:
      BAD=NAME: value
`,
			want: "env name",
		},
		{
			name: "backing env value",
			yaml: "project:\n  name: invalid-runtime\nbackingServices:\n  redis:\n    image: redis:7-alpine\n    env:\n      TOKEN: \"bad\\nvalue\"\n",
			want: "env value",
		},
		{
			name: "volume target",
			yaml: `project:
  name: invalid-runtime
services:
  web:
    image: alpine:latest
    dependencyVolumes:
      - name: bundle
        target: bundle
`,
			want: "volume target",
		},
		{
			name: "setup service",
			yaml: `project:
  name: invalid-runtime
services:
  web:
    image: alpine:latest
setup:
  afterSeeds:
    - service: missing
      command: echo seed
`,
			want: "unknown service",
		},
		{
			name: "warm fingerprint path",
			yaml: `project:
  name: invalid-runtime
services:
  web:
    image: alpine:latest
warm:
  fingerprint:
    paths:
      - ../db/migrate
`,
			want: "warm.fingerprint.paths",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "vivero.yml"), []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := loadProjectConfig(root)
			if err == nil {
				t.Fatalf("expected %s to be rejected", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error should mention %q, got %v", tc.want, err)
			}
		})
	}
}

func TestExecRejectsLegacyHostRuntimeService(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.upsertPreview(PreviewRecord{ID: "legacy-pr-1", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("legacy-pr-1", PreviewService{Name: "web", Runtime: "", Status: "running", Source: "app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec("legacy-pr-1", "web", []string{"sh", "-c", "exit 0"}); err == nil {
		t.Fatal("expected legacy non-docker service exec to be rejected")
	} else if !strings.Contains(err.Error(), "containers only") {
		t.Fatalf("error should explain container-only exec: %v", err)
	}
}

func TestCleanupExistingPreviewForUpDeletesStaleServiceRows(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.upsertPreview(PreviewRecord{ID: "demo-pr-17", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("demo-pr-17", PreviewService{Name: "old", Runtime: "docker", Status: "running", ContainerID: dockerContainerName("demo-pr-17", "old")}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveSource("demo-pr-17", PreviewSource{Name: "old-source", Mode: "external", Path: t.TempDir(), Owned: false}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := a.cleanupExistingPreviewForUp("demo-pr-17"); err != nil {
		t.Fatal(err)
	} else if !found {
		t.Fatal("expected existing preview")
	}
	updated, err := a.getPreview("demo-pr-17")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Services) != 0 {
		t.Fatalf("stale services should be deleted before restart: %#v", updated.Services)
	}
	if len(updated.Sources) != 0 {
		t.Fatalf("stale sources should be deleted before restart: %#v", updated.Sources)
	}
}

func TestUpRefreshesProjectConfigFromDisk(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)

	root := t.TempDir()
	oldPort := freePort(t)
	newPort := freePort(t)
	writeConfig := func(port int) {
		t.Helper()
		cfg := []byte(`project:
  name: refresh-site
services:
  web:
    runtime: docker
    image: python:3.12-alpine
    command: ` + pythonPath + ` -m http.server ` + strconv.Itoa(port) + ` --bind 127.0.0.1
    port: ` + strconv.Itoa(port) + `
    originHost: localhost
    health:
      path: /
      expectStatus: 200
      timeout: 20s
`)
		if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeConfig(oldPort)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}
	writeConfig(newPort)

	preview, err := a.Up(UpRequest{Project: "refresh-site", ID: "refresh-pr", Wait: true, Timeout: 20 * time.Second})
	defer a.Down("refresh-pr", "discard")
	if err != nil {
		t.Fatal(err)
	}
	web := preview.Services["web"]
	wantOrigin := "http://localhost:" + strconv.Itoa(newPort)
	if web.OriginURL != wantOrigin || web.URL != wantOrigin {
		t.Fatalf("up should use refreshed config URL %s, got origin=%s url=%s", wantOrigin, web.OriginURL, web.URL)
	}
	if web.Port != newPort {
		t.Fatalf("up should use refreshed config port %d, got %d", newPort, web.Port)
	}
}

func TestUpPersistsFailedServiceResourcesWhenCleanupFails(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	home := t.TempDir()
	t.Setenv("VIVERO_HOME", home)
	installFakeDocker(t)
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	// Keep fake docker available and common POSIX tools on PATH, but hide cloudflared
	// so quick-tunnel startup fails immediately after the container is started.
	t.Setenv("PATH", filepath.Dir(dockerPath)+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")
	t.Setenv("FAKE_DOCKER_RM_FAIL", "rm blocked")

	root := t.TempDir()
	source := filepath.Join(root, "site")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("hello vivero"), 0o644); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	cfg := []byte(`project:
  name: cleanup-failure
sources:
  app:
    path: ` + source + `
services:
  web:
    source: app
    runtime: docker
    image: python:3.12-alpine
    command: ` + pythonPath + ` -m http.server ` + strconv.Itoa(port) + ` --bind 127.0.0.1
    port: ` + strconv.Itoa(port) + `
    originHost: localhost
    public: true
    health:
      path: /index.html
      expectStatus: 200
      timeout: 20s
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	defer func() {
		t.Setenv("FAKE_DOCKER_RM_FAIL", "")
		_, _ = a.Down("demo-pr-17", "discard")
	}()
	if _, err := a.SyncProject(root); err != nil {
		t.Fatal(err)
	}
	preview, err := a.Up(UpRequest{Project: "cleanup-failure", ID: "demo-pr-17", Public: true, Wait: false, Timeout: 20 * time.Second})
	if err == nil {
		t.Fatal("expected quick tunnel failure")
	}
	if !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("error should include cleanup failure: %v", err)
	}
	state, getErr := a.getPreview("demo-pr-17")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if preview.Services["web"].ContainerID == "" {
		t.Fatalf("returned preview should preserve failed service container id: %#v", preview.Services["web"])
	}
	if state.Services["web"].ContainerID == "" {
		t.Fatalf("stored preview should preserve failed service container id: %#v", state.Services["web"])
	}
	if state.Status != "unhealthy" {
		t.Fatalf("failed preview status = %s", state.Status)
	}
}

func TestDockerEnvFilePathStaysInsideRunDirectory(t *testing.T) {
	home := t.TempDir()
	a := &App{Home: home}
	path := filepath.Clean(a.dockerEnvFile("../escape", "../../web"))
	base := filepath.Join(home, "run", "docker")
	if !strings.HasPrefix(path, base+string(os.PathSeparator)) {
		t.Fatalf("env-file path escaped run directory: %s not under %s", path, base)
	}
	if strings.Contains(filepath.Base(path), "..") || strings.ContainsAny(filepath.Base(path), string(filepath.Separator)) {
		t.Fatalf("env-file basename should be sanitized: %s", filepath.Base(path))
	}
}

func TestStartDockerServiceDoesNotMergeServiceEnvIntoDockerClientEnv(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("FAKE_DOCKER_REJECT_DOCKER_HOST", "tcp://evil.example:2375")
	source := t.TempDir()
	a := &App{Home: t.TempDir()}
	id, err := a.startDockerService("demo", "demo-pr-17", "web", ServiceConfig{Source: "app", Image: "alpine:latest", Command: "sleep 60"}, map[string]PreviewSource{"app": {Path: source}}, map[string]string{"DOCKER_HOST": "tcp://evil.example:2375", "SECRET_TOKEN": "super-secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	defer runCmd("", nil, "docker", "rm", "-f", id)
	if _, err := os.Stat(a.dockerEnvFile("demo-pr-17", "web")); !os.IsNotExist(err) {
		t.Fatalf("temporary service env-file should be removed after docker run, stat err=%v", err)
	}
}

func TestDockerRunOnceArgsApplyResourceLimits(t *testing.T) {
	args, err := dockerRunOnceArgs(dockerServiceSpec{
		PreviewID: "demo-pr-17",
		Service:   "web",
		Image:     "alpine:latest",
		Resources: ResourceLimits{CPUs: "0.5", Memory: "256m"},
	}, "echo setup")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--cpus 0.5", "--memory 256m"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("one-shot docker args missing %s: %v", want, args)
		}
	}
}

func TestPublicServiceRejectsNonLoopbackOriginHost(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.startService(UpRequest{Project: "demo", ID: "demo-pr-17", Public: true}, "web", ServiceConfig{Runtime: "docker", Image: "alpine:latest", Port: 3000, OriginHost: "10.0.0.5", Public: true}, nil, ProjectConfig{}, true, true)
	if err == nil {
		t.Fatal("expected non-loopback public originHost to be rejected")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error should mention loopback: %v", err)
	}
}

func TestPublicPreviewRouterRejectsNonLoopbackUpstream(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.saveProject(t.TempDir(), ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Public:  PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com"},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "python:3.12-alpine", Public: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created := nowUTC()
	if err := a.upsertPreview(PreviewRecord{ID: "demo-pr-17", Project: "demo", Status: "running", CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("demo-pr-17", PreviewService{Name: "web", Status: "healthy", URL: "https://pr-17.preview.example.com", OriginURL: "http://10.0.0.5:3000", StartedAt: created}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "http://pr-17.preview.example.com/products", nil)
	req.Host = "pr-17.preview.example.com"
	rec := httptest.NewRecorder()
	a.controlPlaneHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "loopback") {
		t.Fatalf("error should mention loopback: %s", rec.Body.String())
	}
}

func TestStopPreviewServiceResourcesTreatsMissingContainerAsStopped(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	t.Setenv("FAKE_DOCKER_RM_FAIL", "Error: No such container: missing-container")
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	stopped, err := a.stopPreviewServiceResources("demo-pr-17", "web", PreviewService{Name: "web", Runtime: "docker", Status: "running", ContainerID: "missing-container"})
	if err != nil {
		t.Fatal(err)
	}
	if stopped.ContainerID != "" {
		t.Fatalf("missing container should be treated as stopped, got %#v", stopped)
	}
}

func TestDownRemovesPreviewLabeledStrayContainers(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.saveProject(t.TempDir(), ProjectConfig{Project: ProjectMeta{Name: "demo"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "demo-pr-17", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	state := os.Getenv("FAKE_DOCKER_STATE")
	stray := dockerOneShotContainerName("demo-pr-17", "web", "bundle install")
	for suffix, body := range map[string]string{
		".pid":     "999999",
		".preview": "demo-pr-17",
		".service": "web",
	} {
		if err := os.WriteFile(filepath.Join(state, stray+suffix), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Down("demo-pr-17", "discard"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, stray+".pid")); !os.IsNotExist(err) {
		t.Fatalf("stray preview-labeled container should be removed, stat err=%v", err)
	}
}

func TestSmartWarmFingerprintChangesWhenMigrationChanges(t *testing.T) {
	source := t.TempDir()
	migrations := filepath.Join(source, "db", "migrate")
	if err := os.MkdirAll(migrations, 0o755); err != nil {
		t.Fatal(err)
	}
	migration := filepath.Join(migrations, "001_create_users.sql")
	if err := os.WriteFile(migration, []byte("create table users(id integer);"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Warm:    WarmConfig{Fingerprint: WarmFingerprintConfig{Paths: []string{"db/migrate"}}},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "alpine:latest", DependencyVolumes: []VolumeConfig{{Name: "db", Target: "/db", Lifetime: "smart"}}},
		},
	}
	sources := map[string]PreviewSource{"app": {Name: "app", Mode: "external", Path: source}}
	first, err := computeSmartWarmFingerprint(t.TempDir(), cfg, sources)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(migration, []byte("create table users(id integer); alter table users add column email text;"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := computeSmartWarmFingerprint(t.TempDir(), cfg, sources)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("fingerprint should change when tracked migrations change: %s", first)
	}
}

func TestPrepareSmartWarmVolumesUsesBaselineOnMainAndDerivedOnBranch(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "db", "migrate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "db", "migrate", "001.sql"), []byte("create table users(id integer);"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Warm: WarmConfig{
			BaselineRefs: []string{"main"},
			Fingerprint:  WarmFingerprintConfig{Paths: []string{"db/migrate"}},
		},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "alpine:latest", DependencyVolumes: []VolumeConfig{{Name: "db", Target: "/db", Lifetime: "smart"}}},
		},
	}
	project := ProjectRecord{Name: "demo", Path: t.TempDir(), Config: cfg}
	sources := map[string]PreviewSource{"app": {Name: "app", Mode: "external", Path: source}}
	mainCfg, mainWarm, err := a.prepareSmartWarmVolumes(project, UpRequest{Project: "demo", ID: "main-preview", Metadata: map[string]string{"branch": "main"}}, cfg, sources)
	if err != nil {
		t.Fatal(err)
	}
	wantBaseline := dockerSmartBaselineVolumeName("demo", "web", "db")
	if mainWarm.Mode != warmModeBaseline {
		t.Fatalf("main preview should use baseline warm mode, got %q", mainWarm.Mode)
	}
	if got := mainCfg.Services["web"].DependencyVolumes[0].RuntimeSource; got != wantBaseline {
		t.Fatalf("main preview volume source = %q; want baseline %q", got, wantBaseline)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("FAKE_DOCKER_STATE"), "volume-"+wantBaseline)); err != nil {
		t.Fatalf("baseline volume should be created: %v", err)
	}
	branchCfg, branchWarm, err := a.prepareSmartWarmVolumes(project, UpRequest{Project: "demo", ID: "feature-preview", Metadata: map[string]string{"branch": "feature/new-migration"}}, cfg, sources)
	if err != nil {
		t.Fatal(err)
	}
	wantDerived := dockerSmartPreviewVolumeName("demo", "feature-preview", "web", "db")
	if branchWarm.Mode != warmModeDerived {
		t.Fatalf("feature preview should use derived warm mode, got %q", branchWarm.Mode)
	}
	if got := branchCfg.Services["web"].DependencyVolumes[0].RuntimeSource; got != wantDerived {
		t.Fatalf("feature preview volume source = %q; want derived %q", got, wantDerived)
	}
	if got := branchCfg.Services["web"].DependencyVolumes[0].RuntimeSource; got == wantBaseline {
		t.Fatalf("feature preview must not mount canonical baseline volume")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("FAKE_DOCKER_STATE"), "volume-"+wantDerived)); err != nil {
		t.Fatalf("derived volume should be created: %v", err)
	}
}

func TestRunSetupStepsSmartWarmMarkersProtectBranchDerivedVolumes(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := t.TempDir()
	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "alpine:latest"},
		},
		Setup: SetupConfig{AfterSeeds: []SetupStep{{Service: "web", Policy: "once-per-project", Command: "printf x >> setup-count.txt"}}},
	}
	sources := map[string]PreviewSource{"app": {Name: "app", Mode: "external", Path: source}}
	if err := a.runSetupSteps("main-preview", cfg.Setup.AfterSeeds, cfg, sources, warmRunState{Active: true, Project: "demo", PreviewID: "main-preview", Mode: warmModeBaseline, Fingerprint: "fp-main"}); err != nil {
		t.Fatal(err)
	}
	if err := a.runSetupSteps("branch-same", cfg.Setup.AfterSeeds, cfg, sources, warmRunState{Active: true, Project: "demo", PreviewID: "branch-same", Mode: warmModeDerived, Fingerprint: "fp-main", BaselineReady: true}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(source, "setup-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "x" {
		t.Fatalf("derived volume copied from matching baseline should skip setup, got %q", got)
	}
	if err := a.runSetupSteps("branch-changed", cfg.Setup.AfterSeeds, cfg, sources, warmRunState{Active: true, Project: "demo", PreviewID: "branch-changed", Mode: warmModeDerived, Fingerprint: "fp-branch", BaselineReady: false}); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(source, "setup-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "xx" {
		t.Fatalf("derived volume with changed fingerprint should run setup locally, got %q", got)
	}
}

func TestPrepareSmartWarmVolumesClearsStaleDerivedPreviewMarkers(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := t.TempDir()
	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Warm: WarmConfig{
			BaselineRefs: []string{"main"},
			Fingerprint:  WarmFingerprintConfig{Paths: []string{"Gemfile.lock"}},
		},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "alpine:latest", DependencyVolumes: []VolumeConfig{{Name: "db", Target: "/db", Lifetime: "smart"}}},
		},
		Setup: SetupConfig{AfterSeeds: []SetupStep{{Service: "web", Policy: "once-per-project", Command: "printf x >> setup-count.txt"}}},
	}
	project := ProjectRecord{Name: "demo", Path: t.TempDir(), Config: cfg}
	sources := map[string]PreviewSource{"app": {Name: "app", Mode: "external", Path: source}}
	warmCfg, warmState, err := a.prepareSmartWarmVolumes(project, UpRequest{Project: "demo", ID: "feature-preview", Metadata: map[string]string{"branch": "feature/new-migration"}}, cfg, sources)
	if err != nil {
		t.Fatal(err)
	}
	if warmState.BaselineReady {
		t.Fatal("test requires a missing/stale baseline so derived setup uses a preview-local marker")
	}
	if err := a.runSetupSteps("feature-preview", warmCfg.Setup.AfterSeeds, warmCfg, sources, warmState); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(source, "setup-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "x" {
		t.Fatalf("first derived setup should run once, got %q", got)
	}
	warmCfg, warmState, err = a.prepareSmartWarmVolumes(project, UpRequest{Project: "demo", ID: "feature-preview", Metadata: map[string]string{"branch": "feature/new-migration"}}, cfg, sources)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.runSetupSteps("feature-preview", warmCfg.Setup.AfterSeeds, warmCfg, sources, warmState); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(source, "setup-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "xx" {
		t.Fatalf("recreated derived volume must rerun setup instead of trusting a stale preview marker, got %q", got)
	}
}

func TestPrepareSmartWarmVolumesClearsStaleBaselineMarkers(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := t.TempDir()
	cfg := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Warm: WarmConfig{
			BaselineRefs: []string{"main"},
			Fingerprint:  WarmFingerprintConfig{Paths: []string{"Gemfile.lock"}},
		},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "alpine:latest", DependencyVolumes: []VolumeConfig{{Name: "db", Target: "/db", Lifetime: "smart"}}},
		},
		Setup: SetupConfig{AfterSeeds: []SetupStep{{Service: "web", Policy: "once-per-project", Command: "printf x >> setup-count.txt"}}},
	}
	project := ProjectRecord{Name: "demo", Path: t.TempDir(), Config: cfg}
	sources := map[string]PreviewSource{"app": {Name: "app", Mode: "external", Path: source}}
	warmCfg, warmState, err := a.prepareSmartWarmVolumes(project, UpRequest{Project: "demo", ID: "main-preview", Metadata: map[string]string{"branch": "main"}}, cfg, sources)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.runSetupSteps("main-preview", warmCfg.Setup.AfterSeeds, warmCfg, sources, warmState); err != nil {
		t.Fatal(err)
	}
	if err := a.finalizeSmartWarmBaseline("main-preview", warmState); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(source, "setup-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "x" {
		t.Fatalf("first baseline setup should run once, got %q", got)
	}
	baseline := dockerSmartBaselineVolumeName("demo", "web", "db")
	if err := os.Remove(filepath.Join(os.Getenv("FAKE_DOCKER_STATE"), "volume-"+baseline)); err != nil {
		t.Fatal(err)
	}
	warmCfg, warmState, err = a.prepareSmartWarmVolumes(project, UpRequest{Project: "demo", ID: "main-preview-2", Metadata: map[string]string{"branch": "main"}}, cfg, sources)
	if err != nil {
		t.Fatal(err)
	}
	if warmState.BaselineReady {
		t.Fatal("baseline should not be ready after its Docker volume was removed")
	}
	if err := a.runSetupSteps("main-preview-2", warmCfg.Setup.AfterSeeds, warmCfg, sources, warmState); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(source, "setup-count.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "xx" {
		t.Fatalf("recreated baseline volume must rerun setup instead of trusting a stale warm marker, got %q", got)
	}
}

func TestDownDiscardRemovesSmartDerivedVolumesButPreservesBaseline(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.saveProject(t.TempDir(), ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Services: map[string]ServiceConfig{
			"web": {Image: "alpine:latest", DependencyVolumes: []VolumeConfig{{Name: "db", Target: "/db", Lifetime: "smart"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "feature-preview", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	state := os.Getenv("FAKE_DOCKER_STATE")
	baseline := dockerSmartBaselineVolumeName("demo", "web", "db")
	derived := dockerSmartPreviewVolumeName("demo", "feature-preview", "web", "db")
	for _, name := range []string{baseline, derived} {
		if err := os.WriteFile(filepath.Join(state, "volume-"+name), []byte("exists"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Down("feature-preview", "discard"); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(state, "volume-"+baseline)); statErr != nil {
		t.Fatalf("smart baseline volume should survive discard: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(state, "volume-"+derived)); !os.IsNotExist(statErr) {
		t.Fatalf("smart derived preview volume should be removed on discard, stat err=%v", statErr)
	}
}
