package vivero

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCacheInspectReportsBuildVolumeAndImageInventory(t *testing.T) {
	a, fake, projectDir := newCacheTestApp(t)
	baselineVolume := dockerSmartBaselineVolumeName("demo", "db", "pgdata")
	projectVolume := dockerProjectVolumeName("demo", "web", "uploads")
	fake.volumes = map[string]bool{baselineVolume: true, projectVolume: true}
	fake.images = map[string]bool{"registry.example/demo-web:cache": true}
	if err := a.writeWarmVolumeState(warmVolumeState{Project: "demo", Service: "db", Name: "pgdata", VolumeName: baselineVolume, Fingerprint: "fp-main", Ref: "main", UpdatedAt: nowUTC()}); err != nil {
		t.Fatal(err)
	}

	inventory, err := a.CacheInspect("demo")
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.OK || inventory.Project != "demo" {
		t.Fatalf("unexpected inventory header: %#v", inventory)
	}
	if len(inventory.BuildCaches) != 1 {
		t.Fatalf("expected one build cache entry: %#v", inventory.BuildCaches)
	}
	build := inventory.BuildCaches[0]
	wantCacheDir := filepath.Join(projectDir, ".vivero", "cache", "web")
	if build.Service != "web" || build.Engine != dockerBuildEngineBuildx || !build.CacheEnabled || !reflect.DeepEqual(build.LocalDirs, []string{wantCacheDir}) {
		t.Fatalf("unexpected build cache entry: %#v", build)
	}
	if len(inventory.WarmVolumes) != 1 || !inventory.WarmVolumes[0].Exists || inventory.WarmVolumes[0].VolumeName != baselineVolume || inventory.WarmVolumes[0].Fingerprint != "fp-main" || inventory.WarmVolumes[0].Ref != "main" {
		t.Fatalf("unexpected warm volumes: %#v", inventory.WarmVolumes)
	}
	if len(inventory.ProjectVolumes) != 1 || !inventory.ProjectVolumes[0].Exists || inventory.ProjectVolumes[0].VolumeName != projectVolume {
		t.Fatalf("unexpected project volumes: %#v", inventory.ProjectVolumes)
	}
	if len(inventory.Images) != 1 || !inventory.Images[0].Exists || inventory.Images[0].Tag != "registry.example/demo-web:cache" {
		t.Fatalf("unexpected images: %#v", inventory.Images)
	}
}

func TestCacheWarmCreatesBaselineVolumesAndBuildsCacheEnabledImages(t *testing.T) {
	a, fake, _ := newCacheTestApp(t)

	result, err := a.CacheWarm("demo", CacheWarmOptions{})
	if err != nil {
		t.Fatal(err)
	}
	baselineVolume := dockerSmartBaselineVolumeName("demo", "db", "pgdata")
	if !result.OK {
		t.Fatalf("cache warm should succeed: %#v", result)
	}
	if !reflect.DeepEqual(fake.ensuredVolumes, []string{baselineVolume}) {
		t.Fatalf("cache warm should create only baseline warm volume, got %#v", fake.ensuredVolumes)
	}
	if len(fake.built) != 1 {
		t.Fatalf("cache warm should build the cache-enabled image once, got %#v", fake.built)
	}
	if fake.built[0].Tag != "registry.example/demo-web:cache" || !fake.built[0].CacheEnabled || len(fake.built[0].CacheTo) != 1 {
		t.Fatalf("unexpected warmed build spec: %#v", fake.built[0])
	}
	state, err := a.readWarmVolumeState("demo", "db", "pgdata")
	if err != nil {
		t.Fatal(err)
	}
	if state.VolumeName != baselineVolume || state.Ref != "main" || state.Fingerprint == "" {
		t.Fatalf("cache warm should persist baseline state: %#v", state)
	}
	if got := cacheActionStatuses(result.Actions); !reflect.DeepEqual(got, []string{"volume:db:warmed", "build:web:warmed"}) {
		t.Fatalf("unexpected cache warm actions: %#v", result.Actions)
	}
	if result.Duration == "" {
		t.Fatalf("cache warm result should report total duration: %#v", result)
	}
	for _, action := range result.Actions {
		if action.Duration == "" {
			t.Fatalf("cache warm action should report duration for speed evidence: %#v", result.Actions)
		}
	}
	if human := cacheActionsHuman(result.Actions); !strings.Contains(human, "duration=") {
		t.Fatalf("cache warm human output should include durations, got %q", human)
	}
}

func TestCachePruneRequiresExplicitScopeAndRemovesSelectedResources(t *testing.T) {
	a, fake, projectDir := newCacheTestApp(t)
	baselineVolume := dockerSmartBaselineVolumeName("demo", "db", "pgdata")
	projectVolume := dockerProjectVolumeName("demo", "web", "uploads")
	cacheDir := filepath.Join(projectDir, ".vivero", "cache", "web")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fake.volumes = map[string]bool{baselineVolume: true, projectVolume: true}
	fake.images = map[string]bool{"registry.example/demo-web:cache": true}

	if _, err := a.CachePrune("demo", CachePruneOptions{Kind: "build", NoInput: true}); err != nil {
		t.Fatal(err)
	}
	if pathExists(cacheDir) {
		t.Fatalf("build cache dir should be removed: %s", cacheDir)
	}
	if len(fake.removedVolumes) != 0 || len(fake.removedImages) != 0 {
		t.Fatalf("build prune should not remove volumes/images: volumes=%#v images=%#v", fake.removedVolumes, fake.removedImages)
	}

	if _, err := a.CachePrune("demo", CachePruneOptions{Kind: "volume", NoInput: true}); err != nil {
		t.Fatal(err)
	}
	if got := fake.removedVolumes; !reflect.DeepEqual(got, []string{baselineVolume, projectVolume}) {
		t.Fatalf("volume prune removed %#v, want baseline then project", got)
	}
	if len(fake.removedImages) != 0 {
		t.Fatalf("volume prune should not remove images: %#v", fake.removedImages)
	}

	if _, err := a.CachePrune("demo", CachePruneOptions{Kind: "image", Yes: true}); err != nil {
		t.Fatal(err)
	}
	if got := fake.removedImages; !reflect.DeepEqual(got, []string{"registry.example/demo-web:cache"}) {
		t.Fatalf("image prune removed %#v", got)
	}
}

func TestCachePruneRejectsAmbiguousOrUnconfirmedDeletes(t *testing.T) {
	a, _, _ := newCacheTestApp(t)
	for _, tc := range []struct {
		name string
		opts CachePruneOptions
		want string
	}{
		{name: "missing kind", opts: CachePruneOptions{NoInput: true}, want: "requires --kind"},
		{name: "bad kind", opts: CachePruneOptions{Kind: "everything", NoInput: true}, want: "unsupported cache prune kind"},
		{name: "unconfirmed", opts: CachePruneOptions{Kind: "build"}, want: "requires --yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.CachePrune("demo", tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CachePrune(%#v) err=%v, want %q", tc.opts, err, tc.want)
			}
		})
	}
}

func TestRunCacheCommandsRenderHumanOutput(t *testing.T) {
	a, fake, _ := newCacheTestApp(t)
	baselineVolume := dockerSmartBaselineVolumeName("demo", "db", "pgdata")
	projectVolume := dockerProjectVolumeName("demo", "web", "uploads")
	fake.volumes = map[string]bool{baselineVolume: true, projectVolume: true}
	fake.images = map[string]bool{"registry.example/demo-web:cache": true}

	var stdout, stderr bytes.Buffer
	if code := a.runCache([]string{"inspect", "demo"}, &stdout, &stderr, false); code != 0 {
		t.Fatalf("cache inspect code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "demo\tbuild=1") || !strings.Contains(got, "image\tweb\tregistry.example/demo-web:cache") {
		t.Fatalf("unexpected cache inspect output: %q", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := a.runCache([]string{"warm", "demo"}, &stdout, &stderr, false); code != 0 {
		t.Fatalf("cache warm code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "volume\tdb") || !strings.Contains(got, "build\tweb") || !strings.Contains(got, "duration=") {
		t.Fatalf("unexpected cache warm output: %q", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := a.runCache([]string{"prune", "demo", "--kind", "image", "--yes"}, &stdout, &stderr, false); code != 0 {
		t.Fatalf("cache prune code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "image\tweb\tregistry.example/demo-web:cache\tremoved") {
		t.Fatalf("unexpected cache prune output: %q", got)
	}
}

func TestRunCacheRejectsMissingAndUnknownSubcommands(t *testing.T) {
	a, _, _ := newCacheTestApp(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing subcommand", args: nil, want: "cache requires subcommand"},
		{name: "missing inspect project", args: []string{"inspect"}, want: "cache inspect requires project"},
		{name: "missing warm project", args: []string{"warm"}, want: "cache warm requires project"},
		{name: "missing prune project", args: []string{"prune"}, want: "cache prune requires project"},
		{name: "unknown subcommand", args: []string{"frobnicate"}, want: "unknown command: cache frobnicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := a.runCache(tc.args, &stdout, &stderr, false); code == 0 {
				t.Fatalf("runCache(%#v) code=0 stdout=%q stderr=%q", tc.args, stdout.String(), stderr.String())
			}
			if got := stderr.String(); !strings.Contains(got, tc.want) {
				t.Fatalf("runCache(%#v) stderr=%q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestPrebuildRunsConfiguredStepsAndReportsFailures(t *testing.T) {
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := cacheTestProjectConfig()
	cfg.Sources["app"] = SourceConfig{Path: "app"}
	cfg.Prebuild = map[string]PrebuildConfig{"app": {Steps: []string{"printf ok > prebuild.txt"}}}
	if _, err := a.saveProject(projectDir, cfg); err != nil {
		t.Fatal(err)
	}

	result, err := a.Prebuild("demo")
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true {
		t.Fatalf("prebuild should succeed: %#v", result)
	}
	if got, err := os.ReadFile(filepath.Join(projectDir, "app", "prebuild.txt")); err != nil || string(got) != "ok" {
		t.Fatalf("prebuild did not run in source dir: got=%q err=%v", got, err)
	}

	cfg.Prebuild = map[string]PrebuildConfig{
		"app":     {Steps: []string{"false"}},
		"missing": {Steps: []string{"exit 99"}},
	}
	if _, err := a.saveProject(projectDir, cfg); err != nil {
		t.Fatal(err)
	}
	result, err = a.Prebuild("demo")
	if err == nil || result["ok"] != false {
		t.Fatalf("prebuild should report failing step: result=%#v err=%v", result, err)
	}
}

func newCacheTestApp(t *testing.T) (*App, *fakeContainerRuntime, string) {
	t.Helper()
	t.Setenv("VIVERO_HOME", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	fake := &fakeContainerRuntime{}
	a.containers = fake
	projectDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectDir, ".vivero", "cache", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := a.saveProject(projectDir, cacheTestProjectConfig()); err != nil {
		t.Fatal(err)
	}
	return a, fake, projectDir
}

func cacheTestProjectConfig() ProjectConfig {
	return ProjectConfig{
		Project: ProjectMeta{Name: "demo"},
		Sources: map[string]SourceConfig{
			"app": {Path: "."},
		},
		Warm: WarmConfig{BaselineRefs: []string{"main"}, Fingerprint: WarmFingerprintConfig{Paths: []string{"vivero.yml"}}},
		Services: map[string]ServiceConfig{
			"web": {
				Source: "app",
				Build: ImageBuildConfig{
					Context: ".",
					Tag:     "registry.example/demo-web:cache",
					Cache: ImageBuildCacheConfig{
						From: []string{"type=local,src=.vivero/cache/web"},
						To:   []string{"type=local,dest=.vivero/cache/web,mode=max"},
					},
				},
				DependencyVolumes: []VolumeConfig{{Name: "uploads", Target: "/app/uploads", Lifetime: "project"}},
			},
		},
		BackingServices: map[string]BackingConfig{
			"db": {Image: "postgres:16", DependencyVolumes: []VolumeConfig{{Name: "pgdata", Target: "/var/lib/postgresql/data", Lifetime: "smart"}}},
		},
	}
}

func cacheActionStatuses(actions []CacheAction) []string {
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		out = append(out, action.Kind+":"+action.Service+":"+action.Status)
	}
	return out
}
