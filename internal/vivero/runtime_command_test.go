package vivero

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeCommandConfigAcceptsShellAndExecForms(t *testing.T) {
	root := t.TempDir()
	cfg := []byte(`project:
  name: command-forms
services:
  web:
    image: node:22
    command: npm run dev -- --host 0.0.0.0
  worker:
    image: ruby:3.4
    command: ["bundle", "exec", "sidekiq", "-q", "default jobs"]
  api:
    image: ruby:3.4
    health:
      command: ["bin/rails", "runner", "puts :ok"]
backingServices:
  db:
    image: postgres:16
    command: ["postgres", "-c", "max_connections=200"]
setup:
  afterSeeds:
    - service: api
      command: ["bin/rails", "db:prepare"]
`)
	if err := os.WriteFile(filepath.Join(root, "vivero.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	_, loaded, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Services["web"].Command.Shell; got != "npm run dev -- --host 0.0.0.0" {
		t.Fatalf("shell command = %q", got)
	}
	if got := loaded.Services["worker"].Command.Args; strings.Join(got, "\x00") != "bundle\x00exec\x00sidekiq\x00-q\x00default jobs" {
		t.Fatalf("worker exec args = %#v", got)
	}
	if got := loaded.BackingServices["db"].Command.Args; strings.Join(got, "\x00") != "postgres\x00-c\x00max_connections=200" {
		t.Fatalf("db exec args = %#v", got)
	}
	if got := loaded.Services["api"].Health.Command.Args; strings.Join(got, "\x00") != "bin/rails\x00runner\x00puts :ok" {
		t.Fatalf("health exec args = %#v", got)
	}
	if got := loaded.Setup.AfterSeeds[0].Command.Args; strings.Join(got, "\x00") != "bin/rails\x00db:prepare" {
		t.Fatalf("setup exec args = %#v", got)
	}
}

func TestDockerRunArgsUsesExecCommandArrayWithoutShell(t *testing.T) {
	args, err := dockerRunArgs(dockerServiceSpec{
		PreviewID: "demo-pr-17",
		Service:   "db",
		Image:     "postgres:16",
		Command:   RuntimeCommand{Args: []string{"postgres", "-c", "max_connections=200"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\x00")
	if strings.Contains(joined, "/bin/sh\x00-lc") {
		t.Fatalf("exec-form command must not be shell wrapped: %#v", args)
	}
	wantSuffix := "postgres:16\x00postgres\x00-c\x00max_connections=200"
	if !strings.HasSuffix(joined, wantSuffix) {
		t.Fatalf("docker args suffix = %#v; want %q", args, wantSuffix)
	}
}

func TestRuntimeCommandYAMLPreservesExecForm(t *testing.T) {
	got := runtimeCommandYAML(RuntimeCommand{Args: []string{"postgres", "-c", "max_connections=200"}})
	if got != `["postgres","-c","max_connections=200"]` {
		t.Fatalf("exec command YAML = %s", got)
	}
}

func TestStartServiceHealthFailureIncludesGenericDiagnostics(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	fake := &fakeContainerRuntime{
		containerID: "container-123",
		healthErr:   errors.New("container is not running"),
		logs:        map[string][]string{"container-123": {"database files are incompatible", "fatal: startup aborted"}},
	}
	a.containers = fake

	ps, err := a.startService(UpRequest{Project: "demo", ID: "demo-pr-17"}, "db", ServiceConfig{
		Runtime: "docker",
		Image:   "postgres:16",
		Command: RuntimeCommand{Args: []string{"postgres", "-c", "max_connections=200"}},
		Health:  HealthConfig{Command: RuntimeCommand{Args: []string{"pg_isready", "-U", "postgres"}}, Timeout: "1ms", Interval: "1ms"},
	}, nil, ProjectConfig{Project: ProjectMeta{Name: "demo"}}, false, false)
	if err == nil {
		t.Fatal("expected health failure")
	}
	msg := err.Error()
	for _, want := range []string{
		"service db failed",
		"image=postgres:16",
		"container=container-123",
		"command=postgres -c max_connections=200",
		"healthCommand=pg_isready -U postgres",
		"logPath=",
		"database files are incompatible",
		"fatal: startup aborted",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("diagnostic missing %q:\n%s", want, msg)
		}
	}
	if ps.LastHealth == "" || !strings.Contains(ps.LastHealth, "container is not running") {
		t.Fatalf("last health should keep root failure, got %#v", ps)
	}
}
