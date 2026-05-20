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

func TestCLIErrorDetailsAndCause(t *testing.T) {
	err := cliError{Code: "wrapped", Message: "outer", Cause: errors.New("inner")}
	if got := err.Error(); got != "outer: inner" {
		t.Fatalf("cliError.Error = %q", got)
	}

	details := normalizeDetails(struct{ Name string }{Name: "demo"})
	if details["value"] == nil {
		t.Fatalf("normalizeDetails should preserve non-map values: %#v", details)
	}
	copied := normalizeDetails(map[string]any{"path": "vivero.yml"})
	copied["path"] = "mutated"
	if copied["path"] != "mutated" {
		t.Fatalf("normalizeDetails copy not mutable as expected: %#v", copied)
	}

	missing := missingArgError("diagnose startup", "preview")
	ce, ok := asCLIError(missing)
	if !ok {
		t.Fatalf("missingArgError should produce cliError: %v", missing)
	}
	if ce.Code != "missing_required_argument" || ce.Details["required"] != "preview" || !strings.Contains(ce.Hint, "vivero help diagnose startup") {
		t.Fatalf("unexpected missing arg error: %#v", ce)
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
	if err := requireExplicitConfirmation(false, true, "", "prod"); err == nil || !strings.Contains(err.Error(), `confirmation "prod"`) {
		t.Fatalf("interactive dangerous command should require confirmation text, got %v", err)
	}
}
