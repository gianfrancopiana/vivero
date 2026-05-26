package vivero

import (
	"fmt"
	"io"
	"strings"
)

func (a *App) runCache(args []string, stdout, stderr io.Writer, jsonOut bool) int {
	if len(args) == 0 {
		return errOut(stderr, jsonOut, missingRequiredError("cache", "subcommand", "vivero help cache"))
	}
	switch args[0] {
	case "inspect":
		pos := positionalArgs(args[1:])
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("cache inspect", "project"))
		}
		inventory, err := a.CacheInspect(pos[0])
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"cache": inventory}, cacheInspectHuman(inventory))
		return 0
	case "warm":
		cmdArgs := args[1:]
		pos := positionalArgs(cmdArgs)
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("cache warm", "project"))
		}
		sources, err := collectKV(cmdArgs, "--source")
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		result, err := a.CacheWarm(pos[0], CacheWarmOptions{Sources: sources})
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"cacheWarm": result}, cacheActionsHuman(result.Actions))
		if !result.OK {
			return 1
		}
		return 0
	case "prune":
		cmdArgs := args[1:]
		pos := positionalArgs(cmdArgs)
		if len(pos) == 0 {
			return errOut(stderr, jsonOut, missingArgError("cache prune", "project"))
		}
		kind, _ := flagValue(cmdArgs, "--kind")
		result, err := a.CachePrune(pos[0], CachePruneOptions{Kind: kind, Yes: hasArg(cmdArgs, "--yes"), NoInput: hasArg(cmdArgs, "--no-input")})
		if err != nil {
			return errOut(stderr, jsonOut, err)
		}
		output(stdout, jsonOut, map[string]any{"cachePrune": result}, cacheActionsHuman(result.Removed))
		if !result.OK {
			return 1
		}
		return 0
	default:
		return errOut(stderr, jsonOut, unknownSubcommandError("cache", args[0]))
	}
}

func cacheInspectHuman(inventory CacheInventory) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s\tbuild=%d\twarmVolumes=%d\tprojectVolumes=%d\timages=%d\n", inventory.Project, len(inventory.BuildCaches), len(inventory.WarmVolumes), len(inventory.ProjectVolumes), len(inventory.Images)))
	for _, cache := range inventory.BuildCaches {
		b.WriteString(fmt.Sprintf("build\t%s\t%d dirs\n", cache.Service, len(cache.LocalDirs)))
	}
	for _, volume := range inventory.WarmVolumes {
		b.WriteString(fmt.Sprintf("warm-volume\t%s/%s\t%s\texists=%t\n", volume.Service, volume.Name, volume.VolumeName, volume.Exists))
	}
	for _, volume := range inventory.ProjectVolumes {
		b.WriteString(fmt.Sprintf("project-volume\t%s/%s\t%s\texists=%t\n", volume.Service, volume.Name, volume.VolumeName, volume.Exists))
	}
	for _, image := range inventory.Images {
		ref := image.Tag
		if ref == "" {
			ref = image.Reference
		}
		b.WriteString(fmt.Sprintf("image\t%s\t%s\texists=%t\n", image.Service, ref, image.Exists))
	}
	return strings.TrimRight(b.String(), "\n")
}

func cacheActionsHuman(actions []CacheAction) string {
	var b strings.Builder
	for _, action := range actions {
		resource := action.Resource
		if resource == "" {
			resource = action.Path
		}
		duration := ""
		if action.Duration != "" {
			duration = "\tduration=" + action.Duration
		}
		if action.Service != "" {
			b.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s%s\n", action.Kind, action.Service, resource, action.Status, duration))
		} else {
			b.WriteString(fmt.Sprintf("%s\t%s\t%s%s\n", action.Kind, resource, action.Status, duration))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
