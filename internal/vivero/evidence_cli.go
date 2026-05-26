package vivero

import "io"

func (a *App) runEvidence(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	if len(args) == 0 {
		return errOut(stderr, jsonOut, missingArgError("evidence", "events|logs|smoke|screenshot|flow|qa"))
	}
	action := args[0]
	switch action {
	case "events", "logs", "smoke":
		pos := positionalArgs(args[1:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("evidence "+action, "preview target"))
		}
		return rerunTopLevel(append([]string{action}, args[1:]...), stdout, stderr)
	case "screenshot":
		pos := positionalArgs(args[1:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("evidence screenshot", "preview target"))
		}
		return rerunTopLevel(append([]string{"screenshot"}, args[1:]...), stdout, stderr)
	case "flow":
		pos := positionalArgs(args[1:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingRequiredError("evidence flow", "preview target", "vivero help evidence flow"))
		}
		previewID, targetRef, err := resolvePreviewTargetRef(pos[0])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		opts, err := evidenceFlowOptionsFromArgs(args[1:])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		v, err := a.EvidenceFlow(previewID, opts)
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		attachEvidenceShape(v, targetRef)
		output(stdout, jsonOut, v, evidenceFlowHuman(v))
		if !opts.DryRun && v["ok"] != true {
			return 1
		}
		return 0
	case "qa":
		if len(args) < 2 {
			return errOut(stderr, jsonOut, missingRequiredError("evidence qa", "subcommand and preview target", "vivero help evidence qa"))
		}
		pos := positionalArgs(args[2:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingRequiredError("evidence qa", "preview target", "vivero help evidence qa"))
		}
		return rerunTopLevel(append([]string{"qa"}, args[1:]...), stdout, stderr)
	default:
		return errOut(stderr, jsonOut, unknownSubcommandError("evidence", action))
	}
}

func rerunTopLevel(args []string, stdout, stderr io.Writer) int {
	return Run(args, stdout, stderr)
}

func evidenceFlowOptionsFromArgs(args []string) (EvidenceFlowOptions, error) {
	opts := EvidenceFlowOptions{Target: artifactTargetFromArgs(args)}
	if stepsFile, ok := flagValue(args, "--steps-file"); ok {
		opts.StepsFile = stepsFile
	}
	if outputDir, ok := flagValue(args, "--output-dir"); ok {
		opts.OutputDir = outputDir
	}
	if outputDir, ok := flagValue(args, "--out"); ok {
		opts.OutputDir = outputDir
	}
	if format, ok := flagValue(args, "--format"); ok {
		opts.Format = format
	}
	if colorScheme, ok := flagValue(args, "--color-scheme"); ok {
		opts.ColorScheme = colorScheme
		opts.ColorSchemeSet = true
	}
	if storageState, ok := flagValue(args, "--storage-state"); ok {
		opts.StorageState = storageState
		opts.StorageStateSet = true
	}
	if width, ok, err := positiveIntFlag(args, "--width"); err != nil {
		return opts, err
	} else if ok {
		opts.Width = width
		opts.WidthSet = true
	}
	if height, ok, err := positiveIntFlag(args, "--height"); err != nil {
		return opts, err
	} else if ok {
		opts.Height = height
		opts.HeightSet = true
	}
	if dsf, ok, err := positiveFloatFlag(args, "--device-scale-factor"); err != nil {
		return opts, err
	} else if ok {
		opts.DeviceScaleFactor = dsf
		opts.DeviceScaleFactorSet = true
	}
	if slowMo, ok, err := nonNegativeIntFlag(args, "--slow-mo-ms"); err != nil {
		return opts, err
	} else if ok {
		opts.SlowMoMS = slowMo
	}
	if wait, ok, err := nonNegativeIntFlag(args, "--wait-ms"); err != nil {
		return opts, err
	} else if ok {
		opts.WaitMS = wait
		opts.WaitMSSet = true
	}
	opts.DryRun = hasArg(args, "--dry-run")
	opts.PrintScript = hasArg(args, "--print-script")
	if hasArg(args, "--video") || hasArg(args, "--no-video") {
		opts.VideoSet = true
		opts.Video = hasArg(args, "--video") && !hasArg(args, "--no-video")
	}
	if hasArg(args, "--screenshots") || hasArg(args, "--no-screenshots") {
		opts.ScreenshotsSet = true
		opts.Screenshots = hasArg(args, "--screenshots") && !hasArg(args, "--no-screenshots")
	}
	if hasArg(args, "--console") || hasArg(args, "--no-console") {
		opts.ConsoleSet = true
		opts.Console = hasArg(args, "--console") && !hasArg(args, "--no-console")
	}
	if hasArg(args, "--network") || hasArg(args, "--no-network") {
		opts.NetworkSet = true
		opts.Network = hasArg(args, "--network") && !hasArg(args, "--no-network")
	}
	return opts, nil
}
