package vivero

import (
	"encoding/json"
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
	scriptPath := filepath.Join("testdata", "fake-docker.sh")
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
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

func TestLoadProjectConfigAllowsComposeRuntimeWithoutImage(t *testing.T) {
	root := t.TempDir()
	cfg := []byte(`project:
  name: compose-app
sources:
  app:
    path: .
services:
  web:
    source: app
    runtime: compose
    compose:
      file: docker-compose.yml
      service: rails
    ports:
      http:
        container: 3000
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	_, loaded, err := loadProjectConfig(root)
	if err != nil {
		t.Fatalf("compose runtime should satisfy the container runtime requirement without duplicating image/build: %v", err)
	}
	if serviceRuntime(loaded.Services["web"]) != "compose" {
		t.Fatalf("compose runtime not normalized: %#v", loaded.Services["web"])
	}
	if loaded.Services["web"].Compose.Service != "rails" {
		t.Fatalf("compose service config not loaded: %#v", loaded.Services["web"].Compose)
	}
}

func TestLoadProjectConfigAllowsComposeRuntimeBackingService(t *testing.T) {
	root := t.TempDir()
	cfg := []byte(`project:
  name: compose-backing
sources:
  gumroad:
    path: .
backingServices:
  db:
    source: gumroad
    runtime: compose
    compose:
      file: docker/docker-compose-test-and-ci.yml
      service: db_test
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	_, loaded, err := loadProjectConfig(root)
	if err != nil {
		t.Fatalf("compose runtime backing should be accepted without duplicating image/command/volumes: %v", err)
	}
	backing := loaded.BackingServices["db"]
	if backingRuntime(backing) != "compose" || backing.Compose.Service != "db_test" {
		t.Fatalf("compose backing not loaded: %#v", backing)
	}
}

func TestLoadProjectConfigRejectsComposeRuntimeDuplication(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "image", body: "    image: app/web:latest\n", want: "image/build"},
		{name: "build", body: "    build:\n      context: .\n", want: "image/build"},
		{name: "command", body: "    command: bundle exec rails s\n", want: "service command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := []byte(`project:
  name: compose-duplication
sources:
  app:
    path: .
services:
  web:
    source: app
    runtime: compose
    compose:
      file: docker-compose.yml
      service: web
    ports:
      http:
        container: 3000
` + tt.body)
			if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := loadProjectConfig(root)
			if err == nil {
				t.Fatal("expected compose runtime duplication to be rejected")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error should mention %q: %v", tt.want, err)
			}
		})
	}
}

func TestLoadProjectConfigRejectsComposeBackingDuplication(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "image", body: "    image: mysql:8\n", want: "image definitions"},
		{name: "command", body: "    command: mysqld\n", want: "service command"},
		{name: "env", body: "    env:\n      MYSQL_ROOT_PASSWORD: password\n", want: "env contracts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := []byte(`project:
  name: compose-backing-duplication
sources:
  gumroad:
    path: .
backingServices:
  db:
    source: gumroad
    runtime: compose
    compose:
      file: docker/docker-compose-test-and-ci.yml
      service: db_test
` + tt.body)
			if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := loadProjectConfig(root)
			if err == nil {
				t.Fatal("expected compose backing duplication to be rejected")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error should mention %q: %v", tt.want, err)
			}
		})
	}
}

func TestLoadProjectConfigAllowsComposeRuntimeVolumesAndSetupSteps(t *testing.T) {
	root := t.TempDir()
	cfg := []byte(`project:
  name: compose-setup
sources:
  app:
    path: .
services:
  web:
    source: app
    runtime: compose
    compose:
      file: docker-compose.yml
      service: web
    ports:
      http:
        container: 3000
    dependencyVolumes:
      - name: bundle
        target: /bundle
        lifetime: project
setup:
  afterSeeds:
    - service: web
      command: bin/setup
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	_, loaded, err := loadProjectConfig(root)
	if err != nil {
		t.Fatalf("compose dependency volumes and setup should be accepted: %v", err)
	}
	if len(loaded.Services["web"].DependencyVolumes) != 1 || len(loaded.Setup.AfterSeeds) != 1 {
		t.Fatalf("compose volume/setup config not preserved: %#v", loaded)
	}
}

func TestLoadProjectConfigRejectsInvalidBuildCacheSpecs(t *testing.T) {
	tests := []struct {
		name string
		spec string
		want string
	}{
		{name: "empty", spec: `""`, want: "must not be empty"},
		{name: "absolute local source", spec: `type=local,src=/tmp/vivero-build-cache`, want: "must be relative"},
		{name: "escaping local destination", spec: `type=local,dest=../cache`, want: "escapes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := []byte(`project:
  name: cached-app
services:
  web:
    image: alpine:latest
    build:
      cache:
        from:
          - ` + tt.spec + `
`)
			if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := loadProjectConfig(root)
			if err == nil {
				t.Fatal("expected invalid build cache spec to be rejected")
			}
			if !strings.Contains(err.Error(), "services.web.build.cache.from[0]") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("invalid cache spec error should include config path and reason %q: %v", tt.want, err)
			}
		})
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

func TestDockerComposeOverrideAddsOnlyPreviewLayer(t *testing.T) {
	home := t.TempDir()
	spec := dockerComposeServiceSpec{
		Project:        "compose-app",
		PreviewID:      "preview-pr-9",
		Service:        "web-preview",
		ComposeService: "web",
		OverrideFile:   dockerComposeOverridePath(home, "preview-pr-9", "web-preview"),
		Network:        dockerNetworkName("preview-pr-9"),
		NetworkAliases: []string{"web-preview", "web"},
		Ports:          []ServicePort{{Name: "http", Container: 3000, Protocol: "tcp"}},
		Env:            map[string]string{"VIVERO_PUBLIC_URL": "https://preview.example"},
		Volumes:        []VolumeConfig{{Name: "bundle", Target: "/bundle", Lifetime: "project"}},
	}
	if err := writeDockerComposeOverride(spec, []string{"web", "db"}, map[string]bool{"db": true}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(spec.OverrideFile)
	if err != nil {
		t.Fatal(err)
	}
	yml := string(body)
	for _, want := range []string{
		"vivero.preview: preview-pr-9",
		composeExpectedCompletionLabel + ": \"true\"",
		composeExpectedCompletionLabel + ": \"false\"",
		"vivero.service: web-preview",
		"127.0.0.1::3000",
		"aliases:",
		"- web-preview",
		"- web",
		"environment:",
		"- VIVERO_PUBLIC_URL",
		"ports: !override",
		"ports: !reset []",
		"vivero_dependency_0",
		"target: /bundle",
		"external: true",
		"name: " + dockerNetworkName("preview-pr-9"),
	} {
		if !strings.Contains(yml, want) {
			t.Fatalf("compose override missing %q:\n%s", want, yml)
		}
	}
	if strings.Contains(yml, "https://preview.example") {
		t.Fatalf("compose override must not persist environment values:\n%s", yml)
	}
	for _, forbidden := range []string{"image:", "build:", "command:"} {
		if strings.Contains(yml, forbidden) {
			t.Fatalf("compose override should not duplicate app runtime field %q:\n%s", forbidden, yml)
		}
	}
}

func TestDockerComposeExpectedCompletionServicesUsesDependencyConditions(t *testing.T) {
	model := dockerComposeConfigModel{Services: map[string]dockerComposeConfigService{
		"web": {DependsOn: map[string]json.RawMessage{
			"db":   json.RawMessage(`{"condition":"service_started"}`),
			"init": json.RawMessage(`{"condition":"service_completed_successfully"}`),
		}},
		"db": {},
		"init": {DependsOn: map[string]json.RawMessage{
			"nested-init": json.RawMessage(`{"condition":"service_completed_successfully"}`),
		}},
		"nested-init": {},
	}}
	got := dockerComposeExpectedCompletionServices(model, "web")
	if !got["init"] || !got["nested-init"] || got["db"] || got["web"] {
		t.Fatalf("expected-completion services = %#v", got)
	}
}

func TestStartDockerServiceSupportsComposeRuntime(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("FAKE_DOCKER_COMPOSE_SERVICES", "web db")
	home := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "docker-compose.yml"), []byte("services:\n  web:\n    image: app/web\n  db:\n    image: postgres\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := ServiceConfig{
		Source:  "app",
		Runtime: "compose",
		Compose: ComposeConfig{File: "docker-compose.yml", Service: "web"},
		Ports:   map[string]PortConfig{"http": {Container: 3310}},
	}
	containerID, err := startDockerService(home, "compose-app", "preview-compose", "web", svc, map[string]PreviewSource{"app": {Path: source}}, map[string]string{"VIVERO_PUBLIC_URL": "https://preview.example"})
	if err != nil {
		t.Fatalf("start compose service: %v", err)
	}
	if !strings.Contains(containerID, "preview-compose") || !strings.Contains(containerID, "web") {
		t.Fatalf("unexpected compose container id: %s", containerID)
	}
	ports, err := dockerContainerRuntime{}.PublishedPorts(containerID, []ServicePort{{Name: "http", Container: 3310, Protocol: "tcp"}})
	if err != nil {
		t.Fatalf("published ports: %v", err)
	}
	if len(ports) != 1 || ports[0].Name != "http" || ports[0].HostIP != "127.0.0.1" || ports[0].Host != 3310 {
		t.Fatalf("unexpected published port: %#v", ports)
	}
	if _, err := os.Stat(dockerComposeOverridePath(home, "preview-compose", "web")); !os.IsNotExist(err) {
		t.Fatalf("compose override should be deleted after startup, stat err=%v", err)
	}
}

func TestComposeRuntimeSupportsBackingServiceAlias(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("FAKE_DOCKER_COMPOSE_SERVICES", "db_test redis")
	home := t.TempDir()
	source := t.TempDir()
	composePath := filepath.Join(source, "docker-compose-test-and-ci.yml")
	if err := os.WriteFile(composePath, []byte("services:\n  db_test:\n    image: mysql\n  redis:\n    image: redis\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backing := BackingConfig{
		Source:  "gumroad",
		Runtime: "compose",
		Compose: ComposeConfig{File: "docker-compose-test-and-ci.yml", Service: "db_test"},
	}
	containerID, err := startDockerService(home, "gumroad-main", "preview-compose-db", "db", serviceConfigForBacking(backing), map[string]PreviewSource{"gumroad": {Path: source}}, nil)
	if err != nil {
		t.Fatalf("start compose backing: %v", err)
	}
	if !strings.Contains(containerID, "preview-compose-db") || !strings.Contains(containerID, "db_test") {
		t.Fatalf("unexpected compose backing container id: %s", containerID)
	}
	if _, err := os.Stat(dockerComposeOverridePath(home, "preview-compose-db", "db")); !os.IsNotExist(err) {
		t.Fatalf("compose backing override should be deleted after startup, stat err=%v", err)
	}
}

func TestComposeRuntimeInjectsDependencyVolumes(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("FAKE_DOCKER_COMPOSE_SERVICES", "web db")
	home := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "docker-compose.yml"), []byte("services:\n  web:\n    image: app/web\n  db:\n    image: postgres\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := ServiceConfig{
		Source:            "app",
		Runtime:           "compose",
		Compose:           ComposeConfig{File: "docker-compose.yml", Service: "web"},
		Ports:             map[string]PortConfig{"http": {Container: 3310}},
		DependencyVolumes: []VolumeConfig{{Name: "bundle", Target: "/bundle", Lifetime: "project"}},
	}
	if _, err := startDockerService(home, "compose-app", "preview-compose-volume", "web", svc, map[string]PreviewSource{"app": {Path: source}}, nil); err != nil {
		t.Fatalf("start compose service: %v", err)
	}
	volume := dockerProjectVolumeName("compose-app", "web", "bundle")
	if _, err := os.Stat(filepath.Join(os.Getenv("FAKE_DOCKER_STATE"), "volume-"+volume)); err != nil {
		t.Fatalf("compose dependency volume %s was not created: %v", volume, err)
	}
}

func TestRunDockerComposeOneShotUsesRunThenExecAndCleansOverride(t *testing.T) {
	installFakeDocker(t)
	t.Setenv("FAKE_DOCKER_COMPOSE_SERVICES", "web db")
	home := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "docker-compose.yml"), []byte("services:\n  web:\n    image: app/web\n  db:\n    image: postgres\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := ServiceConfig{
		Source:  "app",
		Runtime: "compose",
		Compose: ComposeConfig{File: "docker-compose.yml", Service: "web"},
		Ports:   map[string]PortConfig{"http": {Container: 3310}},
	}
	sources := map[string]PreviewSource{"app": {Path: source}}
	if _, err := runDockerComposeOneShot(home, "compose-app", "preview-compose-setup", "web", svc, sources, nil, RuntimeCommand{Shell: "printf seeded > setup-run.txt"}); err != nil {
		t.Fatalf("compose run setup: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(source, "setup-run.txt")); err != nil || string(body) != "seeded" {
		t.Fatalf("compose run did not execute setup command: body=%q err=%v", body, err)
	}
	if _, err := startDockerService(home, "compose-app", "preview-compose-setup", "web", svc, sources, nil); err != nil {
		t.Fatalf("start compose service: %v", err)
	}
	if _, err := runDockerComposeOneShot(home, "compose-app", "preview-compose-setup", "web", svc, sources, nil, RuntimeCommand{Shell: "printf seeded > setup-exec.txt"}); err != nil {
		t.Fatalf("compose exec setup: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(source, "setup-exec.txt")); err != nil || string(body) != "seeded" {
		t.Fatalf("compose exec did not execute setup command: body=%q err=%v", body, err)
	}
	modePath := filepath.Join(os.Getenv("FAKE_DOCKER_STATE"), "compose-"+dockerComposeProjectName("preview-compose-setup", "web")+"-web.setup-mode")
	if body, err := os.ReadFile(modePath); err != nil || string(body) != "exec" {
		t.Fatalf("expected running compose target to use exec: mode=%q err=%v", body, err)
	}
	if _, err := os.Stat(dockerComposeOverridePath(home, "preview-compose-setup", "web")); !os.IsNotExist(err) {
		t.Fatalf("compose setup override should be deleted, stat err=%v", err)
	}
}

func TestRemoveDockerComposeProjectRetainsVolumesUnlessDiscarded(t *testing.T) {
	installFakeDocker(t)
	state := os.Getenv("FAKE_DOCKER_STATE")
	project := dockerComposeProjectName("preview-compose-retain", "web")
	volume := "app-db-data"
	if err := os.WriteFile(filepath.Join(state, "compose-volume-"+volume), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, volume+".compose-project"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeDockerComposeProject("preview-compose-retain", "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "compose-volume-"+volume)); err != nil {
		t.Fatalf("compose volume should survive normal cleanup: %v", err)
	}
	if err := removeDockerComposeProjectWithOptions("preview-compose-retain", "web", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "compose-volume-"+volume)); !os.IsNotExist(err) {
		t.Fatalf("compose volume should be removed on explicit discard, stat err=%v", err)
	}
}

func TestRemoveDockerComposeProjectRemovesOnlyProjectNetworks(t *testing.T) {
	installFakeDocker(t)
	state := os.Getenv("FAKE_DOCKER_STATE")
	project := dockerComposeProjectName("preview-compose-network", "web")
	for _, network := range []string{"compose-custom", dockerNetworkName("preview-compose-network")} {
		if err := os.WriteFile(filepath.Join(state, "network-"+network), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(state, "compose-custom.compose-project"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeDockerComposeProject("preview-compose-network", "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "network-compose-custom")); !os.IsNotExist(err) {
		t.Fatalf("project-labeled compose network survived cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state, "network-"+dockerNetworkName("preview-compose-network"))); err != nil {
		t.Fatalf("external Vivero network should not be removed by compose cleanup: %v", err)
	}
}

func TestDockerComposeProjectLogsIncludesEveryProjectContainer(t *testing.T) {
	installFakeDocker(t)
	state := os.Getenv("FAKE_DOCKER_STATE")
	project := dockerComposeProjectName("preview-compose-logs", "web")
	for _, fixture := range []struct {
		name string
		log  string
	}{{name: "compose-web", log: "web boot failed\n"}, {name: "compose-db", log: "mysql crashed\n"}} {
		if err := os.WriteFile(filepath.Join(state, fixture.name+".pid"), []byte("999999"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, fixture.name+".compose-project"), []byte(project), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, fixture.name+".log"), []byte(fixture.log), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lines, err := dockerComposeProjectLogs("preview-compose-logs", "web", 100)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"compose-web", "web boot failed", "compose-db", "mysql crashed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("compose project logs missing %q: %s", want, joined)
		}
	}
}

func TestDockerComposeProjectContainersIncludesRunningAndExitedStates(t *testing.T) {
	installFakeDocker(t)
	state := os.Getenv("FAKE_DOCKER_STATE")
	previewID := "preview-compose-state"
	project := dockerComposeProjectName(previewID, "web")
	targetID := strings.Repeat("a", 64)
	for _, fixture := range []struct {
		name     string
		exited   bool
		exitCode int
	}{
		{name: targetID},
		{name: "compose-init", exited: true, exitCode: 0},
		{name: "compose-db", exited: true, exitCode: 7},
	} {
		if err := os.WriteFile(filepath.Join(state, fixture.name+".pid"), []byte("999999"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, fixture.name+".compose-project"), []byte(project), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, fixture.name+".preview"), []byte(previewID), 0o644); err != nil {
			t.Fatal(err)
		}
		if fixture.exited {
			if err := os.WriteFile(filepath.Join(state, fixture.name+".exited"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(state, fixture.name+".exit-code"), []byte(strconv.Itoa(fixture.exitCode)), 0o644); err != nil {
				t.Fatal(err)
			}
			if fixture.name == "compose-init" {
				if err := os.WriteFile(filepath.Join(state, fixture.name+".expected-completion"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	states, err := dockerComposeProjectContainers(previewID, "web")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]runtimeContainerState{}
	for _, container := range states {
		got[container.ID] = container
	}
	if !got[targetID].Running || got[targetID].ExitCode != 0 {
		t.Fatalf("full target state = %#v", got[targetID])
	}
	if _, _, reason := composeProjectRuntimeStatus(states, targetID); strings.Contains(reason, "target container is missing") {
		t.Fatalf("full persisted target id did not match project state: %s", reason)
	}
	if got["compose-init"].Running || got["compose-init"].ExitCode != 0 || !got["compose-init"].ExpectedCompletion {
		t.Fatalf("init state = %#v", got["compose-init"])
	}
	if got["compose-db"].Running || got["compose-db"].ExitCode != 7 {
		t.Fatalf("db state = %#v", got["compose-db"])
	}
}

func TestDockerRunArgsMountSourceAndPublishPort(t *testing.T) {
	args, err := dockerRunArgs(dockerServiceSpec{
		PreviewID: "demo-pr-17",
		Service:   "web",
		Image:     "python:3.12-alpine",
		Command:   RuntimeCommand{Shell: "python3 -m http.server 3310 --bind 0.0.0.0"},
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
	want := []string{
		"build",
		"--tag", "vivero/demo-web:test",
		"--file", "/tmp/vivero-example/Dockerfile.runtime",
		"--build-arg", "RUBY_VERSION=3.4.3",
		"/tmp/vivero-example",
	}
	if got, want := strings.Join(args, "\n"), strings.Join(want, "\n"); got != want {
		t.Fatalf("docker build args changed\ngot:  %v\nwant: %v", args, want)
	}
}

func TestDockerBuildArgsUsesBuildxForConfiguredCache(t *testing.T) {
	args, err := dockerBuildArgs(dockerBuildSpec{
		Tag:          "vivero/demo-web:test",
		Context:      "/tmp/vivero-example",
		Dockerfile:   "/tmp/vivero-example/Dockerfile.runtime",
		CacheEnabled: true,
		CacheFrom:    []string{"type=local,src=/tmp/vivero-example/.vivero/cache/build/web"},
		CacheTo:      []string{"type=local,dest=/tmp/vivero-example/.vivero/cache/build/web,mode=max"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"buildx", "build", "--load",
		"--tag", "vivero/demo-web:test",
		"--file", "/tmp/vivero-example/Dockerfile.runtime",
		"--cache-from", "type=local,src=/tmp/vivero-example/.vivero/cache/build/web",
		"--cache-to", "type=local,dest=/tmp/vivero-example/.vivero/cache/build/web,mode=max",
		"/tmp/vivero-example",
	}
	if got, want := strings.Join(args, "\n"), strings.Join(want, "\n"); got != want {
		t.Fatalf("buildx cache args mismatch\ngot:  %v\nwant: %v", args, want)
	}
}

func TestDockerBuildSpecForServiceResolvesLocalCachePathsUnderContext(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app", "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	cacheEnabled := true
	spec, err := dockerBuildSpecForService(root, "demo", "preview-1", "web", ImageBuildConfig{
		Context:    "app",
		Dockerfile: "docker/Dockerfile",
		Cache: ImageBuildCacheConfig{
			Enabled: &cacheEnabled,
			From:    []string{"type=local,src=.vivero/cache/build/web"},
			To:      []string{"type=local,dest=.vivero/cache/build/web,mode=max"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCachePath := filepath.Join(root, "app", ".vivero", "cache", "build", "web")
	if spec.Engine != dockerBuildEngineBuildx || !spec.CacheEnabled {
		t.Fatalf("cache-enabled spec should use buildx: %#v", spec)
	}
	if got, want := spec.CacheFrom, []string{"type=local,src=" + wantCachePath}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("cache-from mismatch: got %#v want %#v", got, want)
	}
	if got, want := spec.CacheTo, []string{"type=local,dest=" + wantCachePath + ",mode=max"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("cache-to mismatch: got %#v want %#v", got, want)
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
	}, RuntimeCommand{Shell: "printf setup"})
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
		Project:   "demo-main",
		PreviewID: "demo-pr-17",
		Service:   "demo-web",
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
	projectVolume := dockerProjectVolumeName("demo-main", "demo-web", "bundle_path")
	previewVolume := dockerVolumeName("demo-pr-17", "demo-web", "tmp")
	for _, want := range []string{
		"source=" + projectVolume + ",target=/bundle_path",
		"source=" + previewVolume + ",target=/tmp/cache",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker args missing %q: %v", want, args)
		}
	}
	if strings.Contains(joined, dockerVolumeName("demo-pr-17", "demo-web", "bundle_path")) {
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

func TestNamedTunnelPublicURLSupportsBranchProjectHostname(t *testing.T) {
	url, err := publicURLForService(PublicConfig{
		Provider:         "cloudflare",
		Mode:             "named-tunnel",
		BaseDomain:       "pocketmake.com",
		HostnameTemplate: "{{ .BranchSlug }}-{{ .ProjectSlug }}.{{ .BaseDomain }}",
	}, UpRequest{
		Project:  "Gumroad Preview",
		ID:       "ignored-preview-id",
		Metadata: map[string]string{"branch": "feature/Checkout Flow"},
	}, "web")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://feature-checkout-flow-gumroad-preview.pocketmake.com" {
		t.Fatalf("url = %s", url)
	}
}

func TestEnsureCanonicalPreviewMetadataSetsBranchFromSourceRefOrDefaultRef(t *testing.T) {
	t.Run("source ref override wins", func(t *testing.T) {
		req := UpRequest{Sources: map[string]string{"app.ref": "feature/Checkout Flow"}}
		ensureCanonicalPreviewMetadata(&req, ProjectConfig{Sources: map[string]SourceConfig{"app": {DefaultRef: "main"}}})
		if got := req.Metadata["branch"]; got != "feature/Checkout Flow" {
			t.Fatalf("branch metadata = %q", got)
		}
	})

	t.Run("default ref fills missing branch", func(t *testing.T) {
		req := UpRequest{}
		ensureCanonicalPreviewMetadata(&req, ProjectConfig{Sources: map[string]SourceConfig{"app": {DefaultRef: "main"}}})
		if got := req.Metadata["branch"]; got != "main" {
			t.Fatalf("branch metadata = %q", got)
		}
	})

	t.Run("explicit branch is preserved", func(t *testing.T) {
		req := UpRequest{Metadata: map[string]string{"branch": "release"}, Sources: map[string]string{"app.ref": "feature"}}
		ensureCanonicalPreviewMetadata(&req, ProjectConfig{Sources: map[string]SourceConfig{"app": {DefaultRef: "main"}}})
		if got := req.Metadata["branch"]; got != "release" {
			t.Fatalf("branch metadata = %q", got)
		}
	})
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

	for _, tc := range []struct {
		name          string
		host          string
		forwardedHost string
	}{
		{name: "preserved host", host: "pr-17.preview.example.com", forwardedHost: "localhost"},
		{name: "cloudflared loopback origin host", host: "127.0.0.1:7777", forwardedHost: "pr-17.preview.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tc.host+"/products", nil)
			req.Host = tc.host
			req.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			rec := httptest.NewRecorder()
			a.controlPlaneHandler().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if rec.Body.String() != "active preview" {
				t.Fatalf("body = %q", rec.Body.String())
			}
		})
	}
}

func TestPublicPreviewRouterRewritesOriginRedirectsToHTTPSPublicURL(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/legacy" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		http.Redirect(w, r, "http://"+r.Host+"/products", http.StatusMovedPermanently)
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
			"web": {Source: "app", Image: "python:3.12-alpine", Public: true, OriginHost: "app.localhost"},
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

	for _, tc := range []struct {
		name          string
		host          string
		forwardedHost string
	}{
		{name: "public host over local router", host: "pr-17.preview.example.com"},
		{name: "forwarded public host over loopback router", host: "127.0.0.1:7777", forwardedHost: "pr-17.preview.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tc.host+"/legacy", nil)
			req.Host = tc.host
			if tc.forwardedHost != "" {
				req.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			}
			rec := httptest.NewRecorder()

			a.controlPlaneHandler().ServeHTTP(rec, req)

			if rec.Code != http.StatusMovedPermanently {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if got, want := rec.Header().Get("Location"), "https://pr-17.preview.example.com/products"; got != want {
				t.Fatalf("Location = %q; want %q", got, want)
			}
		})
	}
}

func TestPublicPreviewRouterPreservesOriginSubdomainRedirects(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/legacy":
			http.Redirect(w, r, "http://seller."+r.Host+"/products", http.StatusMovedPermanently)
		case "/products":
			if r.Host != "seller.app.localhost" {
				http.Error(w, "wrong upstream host: "+r.Host, http.StatusMisdirectedRequest)
				return
			}
			_, _ = w.Write([]byte("seller products"))
		case "/base":
			if r.Host != "seller.app.localhost" {
				http.Error(w, "wrong upstream host: "+r.Host, http.StatusMisdirectedRequest)
				return
			}
			http.Redirect(w, r, "http://app.localhost/home", http.StatusMovedPermanently)
		case "/admin":
			if r.Host != "seller.app.localhost" {
				http.Error(w, "wrong upstream host: "+r.Host, http.StatusMisdirectedRequest)
				return
			}
			http.Redirect(w, r, "http://admin.app.localhost/dashboard", http.StatusMovedPermanently)
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
	defer backend.Close()
	staleProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://pr-17.preview.example.com/products", http.StatusMovedPermanently)
	}))
	defer staleProxy.Close()
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.saveProject(t.TempDir(), ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Public:  PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com"},
		Services: map[string]ServiceConfig{
			"web": {Source: "app", Image: "python:3.12-alpine", Public: true, OriginHost: "app.localhost"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created := nowUTC()
	if err := a.upsertPreview(PreviewRecord{ID: "demo-pr-17", Project: "demo", Status: "running", CreatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("demo-pr-17", PreviewService{Name: "web", Status: "healthy", URL: "https://pr-17.preview.example.com", OriginURL: backend.URL, ProxyURL: staleProxy.URL, StartedAt: created}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "http://pr-17.preview.example.com/legacy", nil)
	req.Host = "pr-17.preview.example.com"
	rec := httptest.NewRecorder()
	a.controlPlaneHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "https://seller-pr-17.preview.example.com/products"; got != want {
		t.Fatalf("Location = %q; want %q", got, want)
	}

	req = httptest.NewRequest("GET", "http://seller-pr-17.preview.example.com/products", nil)
	req.Host = "seller-pr-17.preview.example.com"
	rec = httptest.NewRecorder()
	a.controlPlaneHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("prefixed host status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "seller products" {
		t.Fatalf("body = %q", rec.Body.String())
	}

	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "/base", want: "https://pr-17.preview.example.com/home"},
		{path: "/admin", want: "https://admin-pr-17.preview.example.com/dashboard"},
	} {
		req = httptest.NewRequest("GET", "http://seller-pr-17.preview.example.com"+tc.path, nil)
		req.Host = "seller-pr-17.preview.example.com"
		rec = httptest.NewRecorder()
		a.controlPlaneHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("%s status = %d, body = %s", tc.path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Location"); got != tc.want {
			t.Fatalf("%s Location = %q; want %q", tc.path, got, tc.want)
		}
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
	id, err := a.startDockerService("demo", "demo-pr-17", "web", ServiceConfig{Source: "app", Image: "alpine:latest", Command: RuntimeCommand{Shell: "sleep 60"}}, map[string]PreviewSource{"app": {Path: source}}, map[string]string{"SECRET_TOKEN": "super-secret-value"})
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
	_, err := plannedNamedPublicHosts(UpRequest{Project: "demo", ID: "demo-pr-17", Public: true}, ProjectConfig{
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
	fake := &fakeContainerRuntime{containers: map[string]bool{"live-container": true}}
	a.containers = fake
	projectConfig := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Public:  PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com", Hostname: "pr-17.preview.example.com"},
		Services: map[string]ServiceConfig{
			"web": {Image: "alpine:latest", Port: 3000, Public: true},
		},
	}
	if _, err := a.saveProject(t.TempDir(), projectConfig); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "demo-pr-16", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("demo-pr-16", PreviewService{Name: "web", Runtime: "docker", ContainerID: "live-container", Status: "healthy", URL: "https://pr-17.preview.example.com", OriginURL: "http://127.0.0.1:3310"}); err != nil {
		t.Fatal(err)
	}
	lock, err := a.lockPreview("demo-pr-16")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.unlock()
	err = a.validateNamedPublicRouteConflicts(UpRequest{Project: "demo", ID: "demo-pr-17", Public: true}, projectConfig)
	if err == nil {
		t.Fatal("expected existing public hostname conflict")
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Fatalf("error should identify existing route owner: %v", err)
	}
}

func TestValidateNamedPublicRouteConflictsIgnoresDeadPreviewOwner(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.containers = &fakeContainerRuntime{containers: map[string]bool{"dead-container": false}}
	projectConfig := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Public:  PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com", Hostname: "pr-17.preview.example.com"},
		Services: map[string]ServiceConfig{
			"web": {Image: "alpine:latest", Port: 3000, Public: true},
		},
	}
	if _, err := a.saveProject(t.TempDir(), projectConfig); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "ghost", Project: "demo", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("ghost", PreviewService{Name: "web", Runtime: "docker", ContainerID: "dead-container", Status: "healthy", URL: "https://pr-17.preview.example.com", OriginURL: "http://127.0.0.1:3310"}); err != nil {
		t.Fatal(err)
	}
	if err := a.validateNamedPublicRouteConflicts(UpRequest{Project: "demo", ID: "replacement", Public: true}, projectConfig); err != nil {
		t.Fatalf("dead preview must not retain public hostname ownership: %v", err)
	}
}

func TestValidateNamedPublicRouteConflictsRetainsLiveUnhealthyOwner(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.containers = &fakeContainerRuntime{containers: map[string]bool{"live-container": true}}
	projectConfig := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Public:  PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com", Hostname: "pr-17.preview.example.com"},
		Services: map[string]ServiceConfig{
			"web": {Image: "alpine:latest", Port: 3000, Public: true},
		},
	}
	if _, err := a.saveProject(t.TempDir(), projectConfig); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "unhealthy-live", Project: "demo", Status: "unhealthy"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("unhealthy-live", PreviewService{Name: "web", Runtime: "docker", ContainerID: "live-container", Status: "unhealthy", URL: "https://pr-17.preview.example.com", OriginURL: "http://127.0.0.1:3310"}); err != nil {
		t.Fatal(err)
	}
	lock, err := a.lockPreview("unhealthy-live")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.unlock()
	if err := a.validateNamedPublicRouteConflicts(UpRequest{Project: "demo", ID: "replacement", Public: true}, projectConfig); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("live unhealthy preview must retain public hostname ownership: %v", err)
	}
}

func TestValidateNamedPublicRouteConflictsRequiresLiveComposeTarget(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	projectStates := map[string]runtimeContainerState{
		"target": {Running: false, ExitCode: 1},
		"db":     {Running: true},
	}
	a.containers = &fakeContainerRuntime{composeProjects: map[string]map[string]runtimeContainerState{"compose-owner:web": projectStates}}
	projectConfig := ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Public:  PublicConfig{Provider: "cloudflare", Mode: "named-tunnel", BaseDomain: "preview.example.com", Hostname: "pr-17.preview.example.com"},
		Services: map[string]ServiceConfig{
			"web": {Runtime: "compose", Compose: ComposeConfig{File: "compose.yml", Service: "web"}, Port: 3000, Public: true},
		},
	}
	if _, err := a.saveProject(t.TempDir(), projectConfig); err != nil {
		t.Fatal(err)
	}
	if err := a.upsertPreview(PreviewRecord{ID: "compose-owner", Project: "demo", Status: "unhealthy"}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveService("compose-owner", PreviewService{Name: "web", Runtime: "compose", ContainerID: "target", Status: "unhealthy", URL: "https://pr-17.preview.example.com", OriginURL: "http://127.0.0.1:3310"}); err != nil {
		t.Fatal(err)
	}
	lock, err := a.lockPreview("compose-owner")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.unlock()
	request := UpRequest{Project: "demo", ID: "replacement", Public: true}
	if err := a.validateNamedPublicRouteConflicts(request, projectConfig); err != nil {
		t.Fatalf("dead Compose target must not retain public hostname ownership: %v", err)
	}
	projectStates["target"] = runtimeContainerState{Running: true}
	projectStates["db"] = runtimeContainerState{Running: false, ExitCode: 2}
	if err := a.validateNamedPublicRouteConflicts(request, projectConfig); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("live unhealthy Compose target must retain public hostname ownership: %v", err)
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
			"redis": {Image: "redis:7-alpine", Command: RuntimeCommand{Shell: "redis-server"}},
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
			{Service: "web", Policy: "once-per-project", Command: RuntimeCommand{Shell: "printf x >> setup-count.txt"}},
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
	err := waitDockerHealthCommand("fake-container", HealthConfig{Command: RuntimeCommand{Shell: "sleep 5"}, Interval: "5ms"}, 100*time.Millisecond)
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
	path := filepath.Clean(dockerEnvFilePath(home, "../escape", "../../web"))
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
	id, err := a.startDockerService("demo", "demo-pr-17", "web", ServiceConfig{Source: "app", Image: "alpine:latest", Command: RuntimeCommand{Shell: "sleep 60"}}, map[string]PreviewSource{"app": {Path: source}}, map[string]string{"DOCKER_HOST": "tcp://evil.example:2375", "SECRET_TOKEN": "super-secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	defer runCmd("", nil, "docker", "rm", "-f", id)
	if _, err := os.Stat(dockerEnvFilePath(a.Home, "demo-pr-17", "web")); !os.IsNotExist(err) {
		t.Fatalf("temporary service env-file should be removed after docker run, stat err=%v", err)
	}
}

func TestDockerRunOnceArgsApplyResourceLimits(t *testing.T) {
	args, err := dockerRunOnceArgs(dockerServiceSpec{
		PreviewID: "demo-pr-17",
		Service:   "web",
		Image:     "alpine:latest",
		Resources: ResourceLimits{CPUs: "0.5", Memory: "256m"},
	}, RuntimeCommand{Shell: "echo setup"})
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
	stray := dockerOneShotContainerName("demo-pr-17", "web", RuntimeCommand{Shell: "bundle install"})
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
		Setup: SetupConfig{AfterSeeds: []SetupStep{{Service: "web", Policy: "once-per-project", Command: RuntimeCommand{Shell: "printf x >> setup-count.txt"}}}},
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

func TestPrepareSmartWarmVolumesReusesSameFingerprintAndRebuildsChangedFingerprint(t *testing.T) {
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
		Setup: SetupConfig{AfterSeeds: []SetupStep{{Service: "web", Policy: "once-per-project", Command: RuntimeCommand{Shell: "printf x >> setup-count.txt"}}}},
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
	if string(got) != "x" {
		t.Fatalf("same-ID retry with the same fingerprint should retain the derived volume and setup marker, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(source, "Gemfile.lock"), []byte("changed dependency graph\n"), 0o644); err != nil {
		t.Fatal(err)
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
		t.Fatalf("changed fingerprint should rebuild the derived volume and rerun setup, got %q", got)
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
		Setup: SetupConfig{AfterSeeds: []SetupStep{{Service: "web", Policy: "once-per-project", Command: RuntimeCommand{Shell: "printf x >> setup-count.txt"}}}},
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
