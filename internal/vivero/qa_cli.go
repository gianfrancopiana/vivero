package vivero

import (
	"fmt"
	"io"
)

func (a *App) runQA(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	pos := positionalArgs(args)
	if len(pos) < 2 {
		return errOut(stderr, jsonOut, missingRequiredError("qa", "subcommand and preview", "vivero help qa"))
	}
	action := pos[0]
	previewID, targetRef, err := resolvePreviewTargetRef(pos[1])
	if err != nil {
		return errOut(stderr, jsonOut, err)
	}
	actionArgs := args[2:]
	scope, _ := flagValue(actionArgs, "--scope")
	switch action {
	case "plan", "context":
		target := artifactTargetFromArgs(actionArgs)
		v, err := a.QAPlanWithTarget(previewID, scope, target)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, qaPlanHuman(v))
		return 0
	case "run":
		target := artifactTargetFromArgs(actionArgs)
		v, err := a.QARunWithTarget(previewID, scope, target, !hasArg(actionArgs, "--no-screenshots"))
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, qaRunHuman(v))
		if v["ok"] != true {
			return 1
		}
		return 0
	case "record":
		opts := QARecordOptions{Scope: scope}
		if width, ok, err := positiveIntFlag(actionArgs, "--width"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.Width = width
		}
		if height, ok, err := positiveIntFlag(actionArgs, "--height"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.Height = height
		}
		if dsf, ok, err := positiveFloatFlag(actionArgs, "--device-scale-factor"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.DeviceScaleFactor = dsf
		}
		if format, ok := flagValue(actionArgs, "--format"); ok {
			opts.Format = format
		}
		if outputDir, ok := flagValue(actionArgs, "--output-dir"); ok {
			opts.OutputDir = outputDir
		}
		if colorScheme, ok := flagValue(actionArgs, "--color-scheme"); ok {
			opts.ColorScheme = colorScheme
		}
		if storageState, ok := flagValue(actionArgs, "--storage-state"); ok {
			opts.StorageState = expandPath(storageState)
		}
		if ms, ok, err := nonNegativeIntFlag(actionArgs, "--slow-mo-ms"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.SlowMoMS = ms
		}
		if ms, ok, err := nonNegativeIntFlag(actionArgs, "--wait-ms"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.WaitMS = ms
			opts.WaitMSSet = true
		}
		v, err := a.QARecord(previewID, opts)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, qaRecordHuman(v))
		if v["ok"] != true {
			return 1
		}
		return 0
	case "final":
		opts := QAFinalOptions{Scope: scope, Target: artifactTargetFromArgs(actionArgs), SkipScreenshots: hasArg(actionArgs, "--no-screenshots"), SkipRecord: hasArg(actionArgs, "--no-record")}
		if width, ok, err := positiveIntFlag(actionArgs, "--width"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.Width = width
		}
		if height, ok, err := positiveIntFlag(actionArgs, "--height"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.Height = height
		}
		if dsf, ok, err := positiveFloatFlag(actionArgs, "--device-scale-factor"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.DeviceScaleFactor = dsf
		}
		if format, ok := flagValue(actionArgs, "--format"); ok {
			opts.Format = format
		}
		if colorScheme, ok := flagValue(actionArgs, "--color-scheme"); ok {
			opts.ColorScheme = colorScheme
		}
		if storageState, ok := flagValue(actionArgs, "--storage-state"); ok {
			opts.StorageState = expandPath(storageState)
		}
		if ms, ok, err := nonNegativeIntFlag(actionArgs, "--slow-mo-ms"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.SlowMoMS = ms
		}
		if ms, ok, err := nonNegativeIntFlag(actionArgs, "--wait-ms"); err != nil {
			return errOut(stderr, jsonOut, err)
		} else if ok {
			opts.WaitMS = ms
			opts.WaitMSSet = true
		}
		v, err := a.QAFinal(previewID, opts)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, qaFinalHuman(v))
		if v["ok"] != true {
			return 1
		}
		return 0
	case "report":
		target := artifactTargetFromArgs(actionArgs)
		out, _ := flagValue(actionArgs, "--out")
		v, err := a.QAReportWithTarget(previewID, scope, target, out)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, fmt.Sprintf("qa report: %s", v["path"]))
		return 0
	default:
		return errOut(stderr, jsonOut, unknownSubcommandError("qa", action))
	}
}
