package vivero

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestJSONErrorShape(t *testing.T) {
	var stderr bytes.Buffer
	code := errOut(&stderr, true, newCLIError("missing_required_flag", "up requires --id", "Run: vivero help up", map[string]string{"flag": "--id"}))
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	var body struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Hint    string            `json:"hint"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, stderr.String())
	}
	if body.OK || body.Error.Code != "missing_required_flag" || body.Error.Details["flag"] != "--id" {
		t.Fatalf("unexpected error body: %#v", body)
	}
}

func TestPlainErrorShape(t *testing.T) {
	var stderr bytes.Buffer
	_ = errOut(&stderr, false, errors.New("plain failure"))
	if !strings.Contains(stderr.String(), "error: plain failure") {
		t.Fatalf("unexpected plain error: %q", stderr.String())
	}
}

func TestNoInputConfirmationPolicy(t *testing.T) {
	if err := requireExplicitConfirmation(true, true, "", "prod"); err == nil || !strings.Contains(err.Error(), "--confirm prod") {
		t.Fatalf("expected no-input confirmation error, got %v", err)
	}
	if err := requireExplicitConfirmation(true, true, "prod", "prod"); err != nil {
		t.Fatalf("expected matching confirmation, got %v", err)
	}
	if err := requireExplicitConfirmation(true, false, "", "prod"); err != nil {
		t.Fatalf("non-dangerous command should not require confirmation: %v", err)
	}
}
