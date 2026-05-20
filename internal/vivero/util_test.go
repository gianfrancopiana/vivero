package vivero

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTypedFlagHelpersValidateInput(t *testing.T) {
	timeout, err := durationFlag([]string{"--timeout", "2s"}, "--timeout", time.Minute)
	if err != nil || timeout != 2*time.Second {
		t.Fatalf("durationFlag = %v, %v", timeout, err)
	}
	if _, err := durationFlag([]string{"--timeout", "0s"}, "--timeout", time.Minute); err == nil || !strings.Contains(err.Error(), "positive duration") {
		t.Fatalf("expected positive duration error, got %v", err)
	}

	width, ok, err := positiveIntFlag([]string{"--width", "1280"}, "--width")
	if err != nil || !ok || width != 1280 {
		t.Fatalf("positiveIntFlag = %d, %v, %v", width, ok, err)
	}
	if _, _, err := positiveIntFlag([]string{"--width", "0"}, "--width"); err == nil || !strings.Contains(err.Error(), "positive integer") {
		t.Fatalf("expected positive integer error, got %v", err)
	}

	wait, ok, err := nonNegativeIntFlag([]string{"--wait-ms", "0"}, "--wait-ms")
	if err != nil || !ok || wait != 0 {
		t.Fatalf("nonNegativeIntFlag = %d, %v, %v", wait, ok, err)
	}
	if _, _, err := nonNegativeIntFlag([]string{"--wait-ms", "-1"}, "--wait-ms"); err == nil || !strings.Contains(err.Error(), "non-negative integer") {
		t.Fatalf("expected non-negative integer error, got %v", err)
	}
}

func TestFlagParsingHelpersHandleRepeatedEqualsAndPositionals(t *testing.T) {
	args := []string{
		"--label", "tier=web",
		"--label=team=infra",
		"--metadata", "branch=main",
		"--timeout=5s",
		"preview",
		"--",
		"npm", "test",
	}
	if got := flagValues(args, "--label"); !reflect.DeepEqual(got, []string{"tier=web", "team=infra"}) {
		t.Fatalf("flagValues = %#v", got)
	}
	labels, err := collectKV(args, "--label")
	if err != nil {
		t.Fatal(err)
	}
	if labels["tier"] != "web" || labels["team"] != "infra" {
		t.Fatalf("collectKV labels = %#v", labels)
	}
	metadata, err := collectKV(args, "--metadata")
	if err != nil {
		t.Fatal(err)
	}
	if metadata["branch"] != "main" {
		t.Fatalf("collectKV metadata = %#v", metadata)
	}
	if _, err := collectKV([]string{"--label"}, "--label"); err == nil || !strings.Contains(err.Error(), "requires value") {
		t.Fatalf("expected missing value error, got %v", err)
	}
	if _, err := collectKV([]string{"--label", "broken"}, "--label"); err == nil || !strings.Contains(err.Error(), "key=value") {
		t.Fatalf("expected key=value error, got %v", err)
	}
	if got := firstPositional(args); got != "preview" {
		t.Fatalf("firstPositional = %q", got)
	}
	if got := positionalArgs(args); !reflect.DeepEqual(got, []string{"preview", "npm", "test"}) {
		t.Fatalf("positionalArgs = %#v", got)
	}
	if got := splitAfterDoubleDash(args); !reflect.DeepEqual(got, []string{"npm", "test"}) {
		t.Fatalf("splitAfterDoubleDash = %#v", got)
	}
}

func TestFloatAndPathHelpersValidateEdges(t *testing.T) {
	value, ok, err := positiveFloatFlag([]string{"--device-scale-factor=1.5"}, "--device-scale-factor")
	if err != nil || !ok || value != 1.5 {
		t.Fatalf("positiveFloatFlag = %v, %v, %v", value, ok, err)
	}
	if _, ok, err := positiveFloatFlag(nil, "--device-scale-factor"); err != nil || ok {
		t.Fatalf("missing positiveFloatFlag = ok %v err %v", ok, err)
	}
	if _, _, err := positiveFloatFlag([]string{"--device-scale-factor", "-1"}, "--device-scale-factor"); err == nil || !strings.Contains(err.Error(), "positive number") {
		t.Fatalf("expected positive number error, got %v", err)
	}

	if got, err := cleanRelPath("assets/../app/main.go"); err != nil || got != "app/main.go" {
		t.Fatalf("cleanRelPath = %q, %v", got, err)
	}
	for _, path := range []string{"", "/tmp/file", "../secret", "a/../../secret"} {
		if _, err := cleanRelPath(path); err == nil {
			t.Fatalf("cleanRelPath(%q) should fail", path)
		}
	}
}

func TestSortedMapKeysIsDeterministic(t *testing.T) {
	got := sortedMapKeys(map[string]int{"b": 2, "a": 1, "c": 3})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedMapKeys = %#v, want %#v", got, want)
	}
}
