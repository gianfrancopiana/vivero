package vivero

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServicePortPlanSupportsNamedDynamicPortsAndLegacyFixedPort(t *testing.T) {
	named, err := servicePortPlan(ServiceConfig{
		Ports: map[string]PortConfig{
			"http":    {Container: 3310},
			"metrics": {Container: 9394, Host: 19394},
		},
		PrimaryPort: "http",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(named) != 2 {
		t.Fatalf("expected two named ports, got %#v", named)
	}
	primary, ok := primaryServicePort(named)
	if !ok || primary.Name != "http" || primary.Container != 3310 || primary.Host != 0 {
		t.Fatalf("named primary should keep host dynamic and container fixed, got %#v", primary)
	}
	if named[1].Name != "metrics" || named[1].Container != 9394 || named[1].Host != 19394 {
		t.Fatalf("named ports should be sorted and preserve explicit host port: %#v", named)
	}

	legacy, err := servicePortPlan(ServiceConfig{Port: 3000})
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy) != 1 || legacy[0].Name != "http" || legacy[0].Container != 3000 || legacy[0].Host != 3000 || !legacy[0].Legacy {
		t.Fatalf("legacy port should remain fixed-host compatibility shorthand, got %#v", legacy)
	}
}

func TestDockerRunArgsPublishesNamedPortsWithDynamicHostPort(t *testing.T) {
	args, err := dockerRunArgs(dockerServiceSpec{
		PreviewID: "demo-pr-17",
		Service:   "web",
		Image:     "python:3.12-alpine",
		Command:   "python3 -m http.server 3310 --bind 0.0.0.0",
		Ports: []ServicePort{
			{Name: "http", Container: 3310, Host: 0, Protocol: "tcp", Primary: true},
			{Name: "metrics", Container: 9394, Host: 19394, Protocol: "tcp"},
		},
		Network: dockerNetworkName("demo-pr-17"),
		Alias:   "web",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--publish 127.0.0.1::3310",
		"--publish 127.0.0.1:19394:9394",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker args missing %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "127.0.0.1:3310:3310") {
		t.Fatalf("dynamic named port should not pin host port: %v", args)
	}
}

func TestDockerPublishedPortParsingSupportsIPv4IPv6AndZeroHostFallback(t *testing.T) {
	port, host, err := parseDockerPublishedPort("127.0.0.1:49153")
	if err != nil {
		t.Fatal(err)
	}
	if port != 49153 || host != "127.0.0.1" {
		t.Fatalf("unexpected IPv4 mapping: host=%s port=%d", host, port)
	}
	port, host, err = parseDockerPublishedPort("[::1]:49154")
	if err != nil {
		t.Fatal(err)
	}
	if port != 49154 || host != "::1" {
		t.Fatalf("unexpected IPv6 mapping: host=%s port=%d", host, port)
	}
}

func TestCleanupPreviewServicesRemovesStrayDockerContainersByPreviewLabel(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	installFakeDocker(t)
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	stray := dockerOneShotContainerName("demo-pr-17", "web", "bundle install")
	state := os.Getenv("FAKE_DOCKER_STATE")
	if err := os.WriteFile(filepath.Join(state, stray+".pid"), []byte("999999"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, stray+".preview"), []byte("demo-pr-17"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.cleanupPreviewServices("demo-pr-17", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, stray+".pid")); !os.IsNotExist(err) {
		t.Fatalf("stray labeled container should be removed before network cleanup, stat err=%v", err)
	}
}
