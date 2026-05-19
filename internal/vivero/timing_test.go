package vivero

import (
	"testing"
	"time"
)

func TestDurationMsFromMetadata(t *testing.T) {
	if _, ok := durationMsFromMetadata(nil); ok {
		t.Fatal("nil metadata should not have a duration")
	}
	if _, ok := durationMsFromMetadata(map[string]string{"durationMs": "not-a-number"}); ok {
		t.Fatal("invalid metadata should not have a duration")
	}
	got, ok := durationMsFromMetadata(map[string]string{"durationMs": "1234"})
	if !ok || got != 1234 {
		t.Fatalf("durationMsFromMetadata = %d, %v", got, ok)
	}
}

func TestHumanDurationMs(t *testing.T) {
	cases := map[int64]string{
		42:   "42ms",
		1200: "1.2s",
		3250: "3.3s",
	}
	for ms, want := range cases {
		if got := humanDurationMs(ms); got != want {
			t.Fatalf("humanDurationMs(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestOperationTimerMetadataCopiesExtraAndAddsDuration(t *testing.T) {
	timer := operationTimer{started: nowUTC().Add(-25 * time.Millisecond)}
	extra := map[string]string{"service": "web"}
	meta := timer.metadata(extra)
	if meta["service"] != "web" {
		t.Fatalf("metadata did not copy extra fields: %#v", meta)
	}
	if _, ok := durationMsFromMetadata(meta); !ok {
		t.Fatalf("metadata missing parseable duration: %#v", meta)
	}
	extra["service"] = "changed"
	if meta["service"] != "web" {
		t.Fatalf("metadata aliases caller map: %#v", meta)
	}
}
