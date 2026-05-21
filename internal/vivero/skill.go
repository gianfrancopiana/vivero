package vivero

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	vskills "github.com/gianfrancopiana/vivero/skills"
	"gopkg.in/yaml.v3"
)

const maxSkillDescriptionLength = 800

var requiredSkillSections = []string{
	"First checks",
	"Production deploy strategy notes",
	"Repo quality gates",
	"Agent invariants",
	"Common flow: run a preview",
	"Verification",
	"QA flow",
	"Teardown",
	"Secrets",
	"Failure checklist",
}

type skillFrontmatter struct {
	Name        string `yaml:"name" json:"name"`
	Version     string `yaml:"version" json:"version"`
	ViveroCLI   string `yaml:"vivero_cli" json:"viveroCli"`
	Schema      int    `yaml:"schema" json:"schema"`
	License     string `yaml:"license" json:"license"`
	Description string `yaml:"description" json:"description"`
}

type skillValidationReport struct {
	OK                bool                   `json:"ok"`
	Name              string                 `json:"name,omitempty"`
	Version           string                 `json:"version,omitempty"`
	ViveroCLI         string                 `json:"viveroCli,omitempty"`
	Schema            int                    `json:"schema,omitempty"`
	License           string                 `json:"license,omitempty"`
	DescriptionLength int                    `json:"descriptionLength,omitempty"`
	Checks            []skillValidationCheck `json:"checks"`
	RequiredSections  []skillSectionCheck    `json:"requiredSections"`
	CommandSnippets   []skillCommandSnippet  `json:"commandSnippets"`
	Errors            []string               `json:"errors,omitempty"`
	Warnings          []string               `json:"warnings,omitempty"`
}

type skillValidationCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type skillSectionCheck struct {
	Heading string `json:"heading"`
	OK      bool   `json:"ok"`
	Reason  string `json:"reason,omitempty"`
}

type skillCommandSnippet struct {
	Line         int    `json:"line"`
	Command      string `json:"command"`
	Kind         string `json:"kind"`
	ManifestPath string `json:"manifestPath,omitempty"`
	OK           bool   `json:"ok"`
	Reason       string `json:"reason,omitempty"`
}

func embeddedSkill() ([]byte, error) { return vskills.SkillMarkdown() }

func defaultSkillTargets() []string {
	cwd, _ := os.Getwd()
	h, _ := os.UserHomeDir()
	return []string{
		filepath.Join(h, ".agents", "skills", "vivero", "SKILL.md"),
		filepath.Join(h, ".claude", "skills", "vivero", "SKILL.md"),
		filepath.Join(h, ".codex", "skills", "vivero", "SKILL.md"),
		filepath.Join(h, ".opencode", "skills", "vivero", "SKILL.md"),
		filepath.Join(cwd, ".agents", "skills", "vivero", "SKILL.md"),
		filepath.Join(cwd, ".claude", "skills", "vivero", "SKILL.md"),
	}
}

func skillVersion(b []byte) string {
	if fm, err := parseSkillFrontmatter(b); err == nil && fm.Version != "" {
		return fm.Version
	}
	re := regexp.MustCompile(`(?m)^version:\s*([^\s]+)\s*$`)
	m := re.FindSubmatch(b)
	if len(m) == 2 {
		return string(m[1])
	}
	return "unknown"
}

func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func parseSkillFrontmatter(b []byte) (skillFrontmatter, error) {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return skillFrontmatter{}, fmt.Errorf("skill markdown missing YAML frontmatter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return skillFrontmatter{}, fmt.Errorf("skill markdown frontmatter is not closed")
	}
	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
		return skillFrontmatter{}, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	return fm, nil
}

func validateEmbeddedSkill(b []byte) (skillValidationReport, error) {
	report := skillValidationReport{OK: true}
	addCheck := func(name string, ok bool, message string) {
		check := skillValidationCheck{Name: name, OK: ok}
		if !ok {
			check.Message = message
			report.Errors = append(report.Errors, fmt.Sprintf("%s: %s", name, message))
		}
		report.Checks = append(report.Checks, check)
	}

	fm, err := parseSkillFrontmatter(b)
	if err != nil {
		addCheck("frontmatter", false, err.Error())
	} else {
		description := strings.TrimSpace(fm.Description)
		report.Name = fm.Name
		report.Version = fm.Version
		report.ViveroCLI = fm.ViveroCLI
		report.Schema = fm.Schema
		report.License = fm.License
		report.DescriptionLength = len(description)
		addCheck("frontmatter", true, "")
		addCheck("frontmatter.name", fm.Name == "vivero", "name must be vivero")
		addCheck("frontmatter.version", fm.Version != "" && fm.Version != "unknown", "version must be present")
		addCheck("frontmatter.vivero_cli", fm.ViveroCLI != "", "vivero_cli must be present")
		addCheck("frontmatter.schema", fm.Schema == 1, "schema must be 1")
		addCheck("frontmatter.license", fm.License == "MIT", "license must be MIT")
		addCheck("frontmatter.description", description != "" && len(description) <= maxSkillDescriptionLength, fmt.Sprintf("description must be non-empty and <= %d chars", maxSkillDescriptionLength))
	}

	report.RequiredSections = validateSkillSections(b)
	sectionsOK := true
	for _, section := range report.RequiredSections {
		if !section.OK {
			sectionsOK = false
			report.Errors = append(report.Errors, fmt.Sprintf("required section missing: %s", section.Heading))
		}
	}
	addCheck("requiredSections", sectionsOK, "all required operating sections must be present")

	report.CommandSnippets = validateSkillCommandSnippets(b)
	commandsOK := true
	for _, snippet := range report.CommandSnippets {
		if !snippet.OK {
			commandsOK = false
			report.Errors = append(report.Errors, fmt.Sprintf("command snippet line %d: %s: %s", snippet.Line, snippet.Command, snippet.Reason))
		} else if snippet.Kind == "example" {
			report.Warnings = append(report.Warnings, fmt.Sprintf("command snippet line %d not checked against manifest: %s", snippet.Line, snippet.Command))
		}
	}
	addCheck("commandSnippets", commandsOK, "vivero command snippets must resolve to command manifest paths")

	report.OK = len(report.Errors) == 0
	return report, nil
}

func validateSkillSections(b []byte) []skillSectionCheck {
	headings := skillHeadings(b)
	checks := make([]skillSectionCheck, 0, len(requiredSkillSections))
	for _, required := range requiredSkillSections {
		_, ok := headings[required]
		check := skillSectionCheck{Heading: required, OK: ok}
		if !ok {
			check.Reason = "missing required operating section"
		}
		checks = append(checks, check)
	}
	return checks
}

func skillHeadings(b []byte) map[string]bool {
	headings := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(trimmed, "## ") {
			continue
		}
		heading := strings.TrimSpace(strings.Trim(trimmed[3:], "#"))
		if heading != "" {
			headings[heading] = true
		}
	}
	return headings
}

func validateSkillCommandSnippets(b []byte) []skillCommandSnippet {
	snippets := extractSkillCommandSnippets(b)
	for i := range snippets {
		snippets[i] = validateSkillCommandSnippet(snippets[i])
	}
	return snippets
}

func extractSkillCommandSnippets(b []byte) []skillCommandSnippet {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	snippets := extractShellFencedCommands(s)
	snippets = append(snippets, extractInlineViveroCommands(s)...)
	return snippets
}

func extractShellFencedCommands(s string) []skillCommandSnippet {
	lines := strings.Split(s, "\n")
	snippets := []skillCommandSnippet{}
	inFence := false
	inShellFence := false
	skipUntil := ""
	buffer := ""
	bufferLine := 0

	flush := func() {
		command := strings.TrimSpace(buffer)
		if command != "" {
			snippets = append(snippets, skillCommandSnippet{Line: bufferLine, Command: command})
		}
		buffer = ""
		bufferLine = 0
	}

	for i, raw := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				if inShellFence {
					flush()
				}
				inFence = false
				inShellFence = false
				skipUntil = ""
				continue
			}
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			inFence = true
			inShellFence = isShellFenceLanguage(lang)
			continue
		}
		if !inFence || !inShellFence {
			continue
		}
		if skipUntil != "" {
			if trimmed == skipUntil {
				skipUntil = ""
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "$ "))
		continued := strings.HasSuffix(strings.TrimRight(trimmed, " \t"), "\\")
		if continued {
			trimmed = strings.TrimSpace(strings.TrimSuffix(strings.TrimRight(trimmed, " \t"), "\\"))
		}
		if buffer == "" {
			bufferLine = lineNo
			buffer = trimmed
		} else if trimmed != "" {
			buffer += " " + trimmed
		}
		if continued {
			continue
		}
		command := strings.TrimSpace(buffer)
		flush()
		if delimiter := heredocDelimiter(command); delimiter != "" {
			skipUntil = delimiter
		}
	}
	if inFence && inShellFence {
		flush()
	}
	return snippets
}

func isShellFenceLanguage(lang string) bool {
	fields := strings.Fields(lang)
	if len(fields) == 0 {
		return false
	}
	lang = strings.ToLower(fields[0])
	switch lang {
	case "sh", "shell", "bash", "zsh", "console", "terminal":
		return true
	default:
		return false
	}
}

func heredocDelimiter(command string) string {
	re := regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)
	m := re.FindStringSubmatch(command)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func extractInlineViveroCommands(s string) []skillCommandSnippet {
	re := regexp.MustCompile("`([^`\n]+)`")
	matches := re.FindAllStringSubmatchIndex(s, -1)
	snippets := []skillCommandSnippet{}
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		command := strings.TrimSpace(s[match[2]:match[3]])
		fields := normalizedCommandFields(command)
		if len(fields) <= 1 || fields[0] != "vivero" {
			continue
		}
		line := strings.Count(s[:match[2]], "\n") + 1
		snippets = append(snippets, skillCommandSnippet{Line: line, Command: command})
	}
	return snippets
}

func validateSkillCommandSnippet(snippet skillCommandSnippet) skillCommandSnippet {
	fields := normalizedCommandFields(snippet.Command)
	if len(fields) == 0 {
		snippet.Kind = "empty"
		snippet.OK = false
		snippet.Reason = "empty command snippet"
		return snippet
	}
	if fields[0] == "vivero" {
		snippet.Kind = "vivero"
		if path, ok := resolveViveroCommandPath(fields[1:]); ok {
			snippet.OK = true
			snippet.ManifestPath = path
			return snippet
		}
		snippet.OK = false
		snippet.Reason = "unknown vivero command path"
		return snippet
	}
	if allowedSkillShellCommand(fields[0]) {
		snippet.Kind = "shell"
		snippet.OK = true
		snippet.Reason = "allowed shell command"
		return snippet
	}
	snippet.Kind = "shell"
	snippet.OK = false
	snippet.Reason = "shell command is not allowlisted for skill validation"
	return snippet
}

func normalizedCommandFields(command string) []string {
	command = strings.TrimSpace(command)
	command = strings.TrimPrefix(command, "$ ")
	command = strings.TrimSuffix(command, ";")
	fields := strings.Fields(command)
	cleaned := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, " \t\r\n;")
		if field != "" {
			cleaned = append(cleaned, field)
		}
	}
	for len(cleaned) > 0 {
		switch {
		case cleaned[0] == "env", cleaned[0] == "sudo":
			cleaned = cleaned[1:]
		case isShellEnvAssignment(cleaned[0]):
			cleaned = cleaned[1:]
		default:
			if cleaned[0] == "./bin/vivero" {
				cleaned[0] = "vivero"
			}
			return cleaned
		}
	}
	return cleaned
}

func isShellEnvAssignment(s string) bool {
	idx := strings.Index(s, "=")
	if idx <= 0 {
		return false
	}
	for _, r := range s[:idx] {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func resolveViveroCommandPath(args []string) (string, bool) {
	known := map[string]CommandManifest{}
	for _, manifest := range commandManifests() {
		known[strings.Join(manifest.Path, " ")] = manifest
	}
	for length := len(args); length >= 1; length-- {
		candidate := strings.Join(args[:length], " ")
		manifest, ok := known[candidate]
		if !ok {
			continue
		}
		if skillCommandRemainderValid(manifest, args[length:]) {
			return candidate, true
		}
	}
	return "", false
}

func skillCommandRemainderValid(manifest CommandManifest, fields []string) bool {
	positionals := skillCommandPositionals(manifest, fields)
	if positionals == nil {
		return false
	}
	name := manifest.Name()
	if name == "help" || name == "schema" {
		return len(positionals) == 0 || positionalsResolveCommandPath(positionals)
	}
	if len(positionals) == 0 {
		return true
	}
	return len(positionals) <= len(manifest.Args)
}

func skillCommandPositionals(manifest CommandManifest, fields []string) []string {
	positionals := []string{}
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == "--" {
			if i+1 < len(fields) {
				positionals = append(positionals, strings.Join(fields[i+1:], " "))
			}
			break
		}
		if strings.HasPrefix(field, "-") {
			known, takesValue := skillFlagSpec(manifest, field)
			if !known {
				return nil
			}
			if takesValue && !strings.Contains(field, "=") {
				if i+1 >= len(fields) || strings.HasPrefix(fields[i+1], "-") {
					return nil
				}
				i++
			}
			continue
		}
		positionals = append(positionals, field)
	}
	return positionals
}

func skillFlagSpec(manifest CommandManifest, flag string) (bool, bool) {
	name := strings.SplitN(flag, "=", 2)[0]
	for _, commandFlag := range manifest.Flags {
		if commandFlag.Name == name {
			return true, commandFlag.ValueName != ""
		}
	}
	if takesValue, ok := knownSkillCLIFlagValueRequirement()[name]; ok {
		return true, takesValue
	}
	return false, false
}

func knownSkillCLIFlagValueRequirement() map[string]bool {
	return map[string]bool{
		"--archive-patch":  false,
		"--breakpoint":     true,
		"--breakpoints":    false,
		"--color-scheme":   true,
		"--crop":           false,
		"--discard":        false,
		"--force":          false,
		"--full-page":      false,
		"--height":         true,
		"--keep-worktree":  false,
		"--no-record":      false,
		"--no-screenshots": false,
		"--out":            true,
		"--since":          true,
		"--storage-state":  true,
		"--target":         true,
		"--width":          true,
	}
}

func positionalsResolveCommandPath(positionals []string) bool {
	candidate := strings.Join(positionals, " ")
	for _, manifest := range commandManifests() {
		if strings.Join(manifest.Path, " ") == candidate {
			return true
		}
	}
	return false
}

func allowedSkillShellCommand(name string) bool {
	switch name {
	case "make", "git", "docker", "go", "mkdir", "cp", "mv", "rm", "python", "python3", "node", "npm", "pnpm", "yarn", "curl":
		return true
	default:
		return false
	}
}

func (a *App) SkillPrint() (map[string]any, error) {
	b, err := embeddedSkill()
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": string(b), "version": skillVersion(b), "sha256": hashBytes(b)}, nil
}

func (a *App) SkillInstall(target string, force bool) (map[string]any, error) {
	b, err := embeddedSkill()
	if err != nil {
		return nil, err
	}
	targets := []string{}
	if target != "" {
		targets = append(targets, expandPath(target))
	} else {
		targets = defaultSkillTargets()
	}
	installed := []map[string]any{}
	for _, t := range targets {
		if !strings.HasSuffix(t, "SKILL.md") {
			t = filepath.Join(t, "SKILL.md")
		}
		if _, err := os.Stat(t); err == nil && !force {
			installed = append(installed, map[string]any{"path": t, "installed": false, "reason": "exists; pass --force to overwrite"})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(t), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(t, b, 0o644); err != nil {
			return nil, err
		}
		installed = append(installed, map[string]any{"path": t, "installed": true})
	}
	return map[string]any{"version": skillVersion(b), "sha256": hashBytes(b), "targets": installed}, nil
}

func (a *App) SkillPath() (map[string]any, error) {
	return map[string]any{"defaultTargets": defaultSkillTargets()}, nil
}

func (a *App) SkillDoctor() (map[string]any, error) {
	b, err := embeddedSkill()
	if err != nil {
		return nil, err
	}
	embeddedHash := hashBytes(b)
	validation, err := validateEmbeddedSkill(b)
	if err != nil {
		return nil, err
	}
	checks := []map[string]any{}
	for _, t := range defaultSkillTargets() {
		check := map[string]any{"path": t, "exists": false, "current": false}
		if ib, err := os.ReadFile(t); err == nil {
			check["exists"] = true
			check["version"] = skillVersion(ib)
			check["sha256"] = hashBytes(ib)
			check["current"] = hashBytes(ib) == embeddedHash
		}
		checks = append(checks, check)
	}
	return map[string]any{"embeddedVersion": skillVersion(b), "embeddedSha256": embeddedHash, "embeddedValidation": validation, "targets": checks}, nil
}
