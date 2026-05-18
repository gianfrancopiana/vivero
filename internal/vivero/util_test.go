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

func TestSortedMapKeysIsDeterministic(t *testing.T) {
	got := sortedMapKeys(map[string]int{"b": 2, "a": 1, "c": 3})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedMapKeys = %#v, want %#v", got, want)
	}
}
