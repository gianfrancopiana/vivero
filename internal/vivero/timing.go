package vivero

import (
	"fmt"
	"strconv"
	"time"
)

type operationTimer struct {
	started time.Time
}

func startOperationTimer() operationTimer {
	return operationTimer{started: nowUTC()}
}

func (t operationTimer) metadata(extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range extra {
		out[k] = v
	}
	d := nowUTC().Sub(t.started)
	if d < 0 {
		d = 0
	}
	out["durationMs"] = strconv.FormatInt(d.Milliseconds(), 10)
	out["duration"] = d.Round(time.Millisecond).String()
	return out
}

func durationMsFromMetadata(meta map[string]string) (int64, bool) {
	if meta == nil || meta["durationMs"] == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(meta["durationMs"], 10, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

func humanDurationMs(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return (time.Duration(ms) * time.Millisecond).Round(100 * time.Millisecond).String()
}
