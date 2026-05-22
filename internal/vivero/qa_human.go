package vivero

import (
	"fmt"
	"strings"
)

func renderQAReport(plan map[string]any) string {
	return renderQAReportWithStatus(plan, "pending")
}

func renderQAReportWithStatus(plan map[string]any, statusText string) string {
	if statusText == "" {
		statusText = "pending"
	}
	var b strings.Builder
	preview, _ := plan["preview"].(map[string]any)
	artifacts, _ := plan["artifacts"].(map[string]any)
	driver, _ := plan["driver"].(map[string]any)
	previewID, _ := preview["id"].(string)
	project, _ := preview["project"].(string)
	status, _ := preview["status"].(string)
	artifactDir, _ := artifacts["dir"].(string)
	target := stringValue(plan["target"])
	preferredDriver, _ := driver["preferred"].(string)

	fmt.Fprintf(&b, "# QA Report: %s\n\n", previewID)
	fmt.Fprintf(&b, "- Project: `%s`\n", project)
	fmt.Fprintf(&b, "- Preview status: `%s`\n", status)
	if target != "" {
		fmt.Fprintf(&b, "- Evidence target: `%s`\n", target)
	}
	fmt.Fprintf(&b, "- Preferred driver: `%s`\n", preferredDriver)
	fmt.Fprintf(&b, "- Artifact directory: `%s`\n\n", artifactDir)

	b.WriteString("## Summary\n\n")
	fmt.Fprintf(&b, "- Status: %s\n", statusText)
	b.WriteString("- Critical issues: 0\n")
	b.WriteString("- High issues: 0\n")
	b.WriteString("- Medium issues: 0\n")
	b.WriteString("- Low issues: 0\n\n")

	scopes, _ := plan["scopes"].([]map[string]any)
	for _, scope := range scopes {
		name, _ := scope["name"].(string)
		description, _ := scope["description"].(string)
		fmt.Fprintf(&b, "## Scope: %s\n\n", name)
		if description != "" {
			fmt.Fprintf(&b, "%s\n\n", description)
		}
		if pages, ok := scope["pages"].([]map[string]any); ok && len(pages) > 0 {
			b.WriteString("### Pages\n\n")
			for _, page := range pages {
				fmt.Fprintf(&b, "- [ ] `%s` %s\n", stringValue(page["service"]), stringValue(page["url"]))
			}
			b.WriteByte('\n')
		}
		if flows, ok := scope["flows"].([]map[string]any); ok && len(flows) > 0 {
			b.WriteString("### Flows\n\n")
			for _, flow := range flows {
				fmt.Fprintf(&b, "- [ ] %s", stringValue(flow["name"]))
				if desc := stringValue(flow["description"]); desc != "" {
					fmt.Fprintf(&b, " — %s", desc)
				}
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
		}
		if checks, ok := scope["checks"].([]map[string]any); ok && len(checks) > 0 {
			b.WriteString("### Checks\n\n")
			for _, check := range checks {
				fmt.Fprintf(&b, "- [ ] %s", stringValue(check["name"]))
				if method := stringValue(check["method"]); method != "" {
					fmt.Fprintf(&b, " (`%s`)", method)
				}
				if desc := stringValue(check["description"]); desc != "" {
					fmt.Fprintf(&b, " — %s", desc)
				}
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
		}
	}

	b.WriteString("## Findings\n\n")
	b.WriteString("Add issues here with severity, URL, repro steps, expected/actual behavior, console/network evidence, and screenshot paths.\n\n")
	b.WriteString("## Evidence\n\n")
	b.WriteString("Store screenshots, recordings, traces, and notes in the artifact directory above.\n")
	return b.String()
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func qaRunHuman(v map[string]any) string {
	status := "failed"
	if v["ok"] == true {
		status = "ok"
	}
	reportPath := ""
	if report, ok := v["report"].(map[string]any); ok {
		reportPath = stringValue(report["path"])
	}
	if reportPath == "" {
		return "qa run " + status
	}
	return "qa run " + status + "\nreport: " + reportPath
}

func qaRecordHuman(v map[string]any) string {
	status := "failed"
	if v["ok"] == true {
		status = "ok"
	}
	recordPath := stringValue(v["recordPath"])
	if recordPath == "" {
		return "qa record " + status
	}
	return "qa record " + status + "\nrecord: " + recordPath
}

func qaPlanHuman(v map[string]any) string {
	preview, _ := v["preview"].(map[string]any)
	artifacts, _ := v["artifacts"].(map[string]any)
	scopes, _ := v["scopes"].([]map[string]any)
	pageCount := 0
	flowCount := 0
	checkCount := 0
	for _, scope := range scopes {
		if pages, ok := scope["pages"].([]map[string]any); ok {
			pageCount += len(pages)
		}
		if flows, ok := scope["flows"].([]map[string]any); ok {
			flowCount += len(flows)
		}
		if checks, ok := scope["checks"].([]map[string]any); ok {
			checkCount += len(checks)
		}
	}
	return fmt.Sprintf("qa plan %s: %d pages, %d flows, %d checks\nartifacts: %s\nnext: capture reproducible evidence with Playwright; use Chrome MCP/browser only for exploratory debugging, then run `vivero preview qa report preview:%s`", stringValue(preview["id"]), pageCount, flowCount, checkCount, stringValue(artifacts["dir"]), stringValue(preview["id"]))
}
