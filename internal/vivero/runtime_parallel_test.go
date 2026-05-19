package vivero

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type startOutcome struct {
	services map[string]PreviewService
	err      error
}

func TestStartupConcurrencyDefaultsAndCaps(t *testing.T) {
	tests := []struct {
		name       string
		configured int
		services   int
		want       int
	}{
		{name: "none", configured: 0, services: 0, want: 0},
		{name: "default", configured: 0, services: 8, want: 4},
		{name: "default capped by service count", configured: 0, services: 2, want: 2},
		{name: "explicit sequential", configured: 1, services: 5, want: 1},
		{name: "explicit parallel", configured: 3, services: 5, want: 3},
		{name: "explicit capped", configured: 10, services: 5, want: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := startupConcurrency(ResourceConfig{MaxStartupConcurrency: tt.configured}, tt.services)
			if got != tt.want {
				t.Fatalf("startupConcurrency(%d, %d) = %d, want %d", tt.configured, tt.services, got, tt.want)
			}
		})
	}
}

func TestStartServicesBoundedHonorsConcurrencyLimit(t *testing.T) {
	var mu sync.Mutex
	active := 0
	maxActive := 0
	services, err := startServicesBounded([]string{"a", "b", "c", "d"}, 2, func(name string) (PreviewService, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()

		time.Sleep(25 * time.Millisecond)

		mu.Lock()
		active--
		mu.Unlock()
		return PreviewService{Name: name, Status: "running"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 4 {
		t.Fatalf("started services = %d, want 4", len(services))
	}
	if maxActive != 2 {
		t.Fatalf("max active services = %d, want 2", maxActive)
	}
}

func TestStartServicesBoundedWaitsForInFlightAndStopsQueuingAfterFirstError(t *testing.T) {
	started := make(chan string, 3)
	releaseA := make(chan struct{})
	done := make(chan startOutcome, 1)

	go func() {
		services, err := startServicesBounded([]string{"c", "b", "a"}, 2, func(name string) (PreviewService, error) {
			started <- name
			switch name {
			case "a":
				<-releaseA
				return PreviewService{Name: name, Status: "running"}, nil
			case "b":
				return PreviewService{Name: name, Status: "starting"}, errors.New("boom")
			default:
				return PreviewService{Name: name, Status: "running"}, nil
			}
		})
		done <- startOutcome{services: services, err: err}
	}()

	first := waitForStartedService(t, started)
	second := waitForStartedService(t, started)
	seen := map[string]bool{first: true, second: true}
	if !seen["a"] || !seen["b"] || seen["c"] {
		t.Fatalf("expected only sorted first batch a,b to start, got %q and %q", first, second)
	}

	select {
	case name := <-started:
		t.Fatalf("service %s should not start after first error while an earlier service is still in-flight", name)
	case out := <-done:
		t.Fatalf("startServicesBounded returned before in-flight service finished: err=%v services=%v", out.err, out.services)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseA)
	out := waitForStartOutcome(t, done)
	if out.err == nil || !strings.Contains(out.err.Error(), "boom") {
		t.Fatalf("expected first error to be returned, got %v", out.err)
	}
	if _, ok := out.services["a"]; !ok {
		t.Fatalf("finished in-flight service a should be included in results: %#v", out.services)
	}
	if _, ok := out.services["b"]; !ok {
		t.Fatalf("failed service b should be included in results for cleanup: %#v", out.services)
	}
	if _, ok := out.services["c"]; ok {
		t.Fatalf("queued service c should not have started after first error: %#v", out.services)
	}
}

func TestLoadProjectConfigParsesMaxStartupConcurrency(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
resources:
  maxStartupConcurrency: 2
services:
  web:
    image: alpine:latest
`)

	_, cfg, err := loadProjectConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Resources.MaxStartupConcurrency; got != 2 {
		t.Fatalf("maxStartupConcurrency = %d, want 2", got)
	}
}

func TestLoadProjectConfigRejectsNegativeMaxStartupConcurrency(t *testing.T) {
	root := writeConfigDoctorFile(t, `project:
  name: demo
resources:
  maxStartupConcurrency: -1
services:
  web:
    image: alpine:latest
`)

	_, _, err := loadProjectConfig(root)
	if err == nil {
		t.Fatal("expected negative maxStartupConcurrency to be rejected")
	}
	if !strings.Contains(err.Error(), "maxStartupConcurrency") {
		t.Fatalf("error should mention maxStartupConcurrency: %v", err)
	}
}

func waitForStartedService(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case name := <-ch:
		return name
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for service start")
		return ""
	}
}

func waitForStartOutcome(t *testing.T, ch <-chan startOutcome) startOutcome {
	t.Helper()
	select {
	case out := <-ch:
		return out
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for start outcome")
		return startOutcome{}
	}
}
