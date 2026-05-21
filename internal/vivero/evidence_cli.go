package vivero

import (
	"io"
	"strings"
)

func (a *App) runEvidence(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	if len(args) == 0 {
		return errOut(stderr, jsonOut, missingArgError("evidence", "events|logs|smoke|screenshot|qa"))
	}
	action := args[0]
	switch action {
	case "events", "logs", "smoke":
		pos := positionalArgs(args[1:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("evidence "+action, "target"))
		}
		if evidenceTargetKind(pos[0]) == "release" {
			return rerunTopLevel(append([]string{"release", action}, args[1:]...), stdout, stderr)
		}
		return rerunTopLevel(append([]string{action}, args[1:]...), stdout, stderr)
	case "screenshot":
		pos := positionalArgs(args[1:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("evidence screenshot", "preview target"))
		}
		if evidenceTargetKind(pos[0]) == "release" {
			return errOut(stderr, jsonOut, newCLIError("unsupported_target", "release targets do not support screenshots", "Use `vivero evidence events release:<id>` or `vivero evidence logs release:<id>`", map[string]string{"command": "evidence screenshot", "target": pos[0]}))
		}
		return rerunTopLevel(append([]string{"screenshot"}, args[1:]...), stdout, stderr)
	case "qa":
		if len(args) < 2 {
			return errOut(stderr, jsonOut, missingRequiredError("evidence qa", "subcommand and preview target", "vivero help evidence qa"))
		}
		pos := positionalArgs(args[2:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingRequiredError("evidence qa", "preview target", "vivero help evidence qa"))
		}
		if evidenceTargetKind(pos[0]) == "release" {
			return errOut(stderr, jsonOut, newCLIError("unsupported_target", "release targets do not support QA commands", "Use `vivero evidence smoke release:<id>` for release smoke evidence", map[string]string{"command": "evidence qa", "target": pos[0]}))
		}
		return rerunTopLevel(append([]string{"qa"}, args[1:]...), stdout, stderr)
	default:
		return errOut(stderr, jsonOut, unknownSubcommandError("evidence", action))
	}
}

func rerunTopLevel(args []string, stdout, stderr io.Writer) int {
	return Run(args, stdout, stderr)
}

func evidenceTargetKind(target string) string {
	kind, _, ok := strings.Cut(strings.TrimSpace(target), ":")
	if !ok {
		return "preview"
	}
	switch strings.ToLower(kind) {
	case "release":
		return "release"
	default:
		return "preview"
	}
}
