package vivero

import (
	"fmt"
	"strings"
)

func rootHelp() string {
	var b strings.Builder
	b.WriteString("vivero - local-first preview runtime\n\n")
	b.WriteString("Examples:\n")
	for _, line := range []string{
		"  vivero projects sync . --json --no-input",
		"  vivero up my-app --id my-app-local --wait --timeout 5m --json --no-input",
		"  vivero events my-app-local --tail --json --no-input",
		"  vivero qa run my-app-local --scope all --target local --json --no-input",
		"  vivero down my-app-local --discard --json --no-input",
	} {
		b.WriteString(line + "\n")
	}
	b.WriteString("\nCommon commands:\n")
	for _, cmd := range commandCatalog() {
		if len(cmd.Path) > 2 {
			continue
		}
		if cmd.Name() == "serve" {
			continue
		}
		b.WriteString(fmt.Sprintf("  %-22s %s\n", "vivero "+cmd.Name(), cmd.Summary))
	}
	b.WriteString("\nGlobal flags:\n")
	b.WriteString("  --json       stable machine-readable output\n")
	b.WriteString("  --no-input   never prompt; fail fast with an actionable error\n")
	b.WriteString("  --quiet      suppress progress output\n")
	b.WriteString("  --verbose    include more progress detail\n")
	b.WriteString("  --debug      include debugging context\n")
	b.WriteString("\nMore help:\n")
	b.WriteString("  vivero help <command>\n")
	b.WriteString("  vivero commands --json --no-input\n")
	b.WriteString("  vivero schema <command> --json --no-input\n")
	return b.String()
}

func commandHelp(path []string) (string, bool) {
	name := strings.Join(path, " ")
	for _, cmd := range commandCatalog() {
		if cmd.Name() == name || strings.Join(cmd.Path, " ") == name {
			return renderCommandHelp(cmd), true
		}
	}
	return "", false
}

func renderCommandHelp(cmd CommandManifest) string {
	var b strings.Builder
	b.WriteString("vivero " + cmd.Name() + " - " + cmd.Summary + "\n\n")
	if cmd.Description != "" && cmd.Description != cmd.Summary {
		b.WriteString(cmd.Description + "\n\n")
	}
	if cmd.Usage != "" {
		b.WriteString("Usage:\n  " + cmd.Usage + "\n\n")
	}
	if len(cmd.Examples) > 0 {
		b.WriteString("Examples:\n")
		for _, ex := range cmd.Examples {
			b.WriteString("  # " + ex.Description + "\n")
			b.WriteString("  " + strings.Join(ex.Command, " ") + "\n")
		}
		b.WriteByte('\n')
	}
	if len(cmd.Args) > 0 {
		b.WriteString("Arguments:\n")
		for _, arg := range cmd.Args {
			required := ""
			if arg.Required {
				required = " required"
			}
			b.WriteString(fmt.Sprintf("  %-14s %s%s\n", arg.Name, arg.Description, required))
		}
		b.WriteByte('\n')
	}
	if len(cmd.Flags) > 0 {
		b.WriteString("Flags:\n")
		for _, flag := range cmd.Flags {
			name := flag.Name
			if flag.ValueName != "" {
				name += " " + flag.ValueName
			}
			b.WriteString(fmt.Sprintf("  %-22s %s\n", name, flag.Description))
		}
		b.WriteByte('\n')
	}
	b.WriteString(fmt.Sprintf("JSON stability: %s\n", cmd.JSONStability))
	if cmd.RequiresNet {
		b.WriteString("Network: may use Docker/network resources.\n")
	}
	if cmd.WritesLocal {
		b.WriteString("Side effects: writes local preview/runtime state.\n")
	}
	return b.String()
}

func usage() string { return rootHelp() }

func helpPathFromArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	if args[0] == "help" {
		return positionalArgs(args[1:])
	}
	path := []string{}
	for _, arg := range positionalArgs(args) {
		if arg == "help" || arg == "--help" || arg == "-h" {
			continue
		}
		path = append(path, arg)
	}
	return path
}

func commandsHuman() string {
	var b strings.Builder
	for _, c := range commandCatalog() {
		b.WriteString(c.Name())
		b.WriteByte('\n')
	}
	return b.String()
}
