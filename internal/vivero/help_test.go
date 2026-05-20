package vivero

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHelpIsExamplesFirst(t *testing.T) {
	out := rootHelp()
	for _, want := range []string{"vivero up", "vivero events", "vivero qa run", "vivero down", "--version", "docs:", "issues:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("root help missing %q:\n%s", want, out)
		}
	}
	examples := strings.Index(out, "Examples:")
	commands := strings.Index(out, "Common commands:")
	if examples == -1 || commands == -1 || examples > commands {
		t.Fatalf("root help should put examples before command list:\n%s", out)
	}
}

func TestCommandHelpFromManifest(t *testing.T) {
	out, ok := commandHelp([]string{"up"})
	if !ok {
		t.Fatal("expected up help")
	}
	for _, want := range []string{"Examples:", "vivero up", "--json", "--no-input", "--metadata", "--label", "--timeout", "default: 5m", "Agent safety:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("up help missing %q:\n%s", want, out)
		}
	}
	qa, ok := commandHelp([]string{"qa", "run"})
	if !ok {
		t.Fatal("expected qa run help")
	}
	for _, want := range []string{"local", "public", "vivero qa run", "default: local"} {
		if !strings.Contains(qa, want) {
			t.Fatalf("qa run help missing %q:\n%s", want, qa)
		}
	}
}

func TestCommandGroupHelp(t *testing.T) {
	out, ok := commandHelp([]string{"qa"})
	if !ok {
		t.Fatal("expected qa group help")
	}
	for _, want := range []string{"vivero qa - command group", "Examples:", "Subcommands:", "vivero qa plan", "vivero qa run", "vivero qa final"} {
		if !strings.Contains(out, want) {
			t.Fatalf("qa group help missing %q:\n%s", want, out)
		}
	}
}

func TestRunHelpEntrypoints(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help", "up"}, {"up", "--help"}, {"help", "qa"}, {"help", "qa", "run"}, {"qa", "run", "--help"}} {
		var stdout, stderr bytes.Buffer
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("Run(%v) exit = %d stderr=%s", args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Examples:") {
			t.Fatalf("Run(%v) did not print help: %s", args, stdout.String())
		}
	}
}

func TestUnknownCommandSuggestsCloseMatch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"capabilties", "--json", "--no-input"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("unknown command should fail")
	}
	if stdout.Len() != 0 {
		t.Fatalf("json errors should be on stderr, stdout=%q", stdout.String())
	}
	body := stderr.String()
	for _, want := range []string{`"ok": false`, `"code": "unknown_command"`, "capabilities"} {
		if !strings.Contains(body, want) {
			t.Fatalf("json error missing %q: %s", want, body)
		}
	}
}

func TestNestedUnknownCommandSuggestsCloseMatch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"qa", "rn", "preview", "--json", "--no-input"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("unknown nested command should fail")
	}
	if stdout.Len() != 0 {
		t.Fatalf("json errors should be on stderr, stdout=%q", stdout.String())
	}
	body := stderr.String()
	for _, want := range []string{`"code": "unknown_command"`, `"command": "qa rn"`, "vivero qa run"} {
		if !strings.Contains(body, want) {
			t.Fatalf("json error missing %q: %s", want, body)
		}
	}
}
