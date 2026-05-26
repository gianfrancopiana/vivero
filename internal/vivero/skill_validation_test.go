package vivero

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBundledSkillFrontmatterIsValid(t *testing.T) {
	skill := mustEmbeddedSkillForValidationTest(t)
	frontmatter, err := parseSkillFrontmatter(skill)
	if err != nil {
		t.Fatalf("frontmatter should parse: %v", err)
	}
	if frontmatter.Name != "vivero" {
		t.Fatalf("frontmatter name = %q, want vivero", frontmatter.Name)
	}
	if frontmatter.Version == "" || frontmatter.Version == "unknown" {
		t.Fatalf("frontmatter should include a concrete version, got %q", frontmatter.Version)
	}
	if frontmatter.ViveroCLI == "" {
		t.Fatal("frontmatter should include the supported vivero_cli version")
	}
	if frontmatter.Schema != 1 {
		t.Fatalf("frontmatter schema = %d, want 1", frontmatter.Schema)
	}
	if frontmatter.License != "MIT" {
		t.Fatalf("frontmatter license = %q, want MIT", frontmatter.License)
	}
	description := strings.TrimSpace(frontmatter.Description)
	if description == "" {
		t.Fatal("frontmatter description should not be empty")
	}
	if len(description) > maxSkillDescriptionLength {
		t.Fatalf("frontmatter description length = %d, want <= %d", len(description), maxSkillDescriptionLength)
	}
}

func TestBundledSkillHasRequiredOperatingSections(t *testing.T) {
	report, err := validateEmbeddedSkill(mustEmbeddedSkillForValidationTest(t))
	if err != nil {
		t.Fatalf("validate embedded skill: %v", err)
	}
	if !report.OK {
		t.Fatalf("embedded skill validation failed: %#v", report.Errors)
	}

	seen := map[string]skillSectionCheck{}
	for _, section := range report.RequiredSections {
		seen[section.Heading] = section
	}
	for _, heading := range []string{
		"Mental model",
		"First checks",
		"Choose the lane",
		"Preview flow",
		"Evidence/QA flow",
		"Cache/speed flow",
		"Failure playbooks",
		"Teardown and safety",
		"Secrets rules",
		"Verification gates",
	} {
		section, ok := seen[heading]
		if !ok {
			t.Fatalf("validation should report required section %q", heading)
		}
		if !section.OK {
			t.Fatalf("required section %q should be present: %#v", heading, section)
		}
	}
}

func TestBundledSkillCommandSnippetsResolve(t *testing.T) {
	report, err := validateEmbeddedSkill(mustEmbeddedSkillForValidationTest(t))
	if err != nil {
		t.Fatalf("validate embedded skill: %v", err)
	}
	if len(report.CommandSnippets) < 20 {
		t.Fatalf("expected many command snippets to be validated, got %d", len(report.CommandSnippets))
	}
	var unresolved []string
	for _, snippet := range report.CommandSnippets {
		if !snippet.OK {
			unresolved = append(unresolved, fmt.Sprintf("line %d: %s (%s)", snippet.Line, snippet.Command, snippet.Reason))
			continue
		}
		if snippet.Kind == "vivero" && snippet.ManifestPath == "" {
			unresolved = append(unresolved, fmt.Sprintf("line %d: %s did not resolve to a manifest path", snippet.Line, snippet.Command))
		}
	}
	if len(unresolved) > 0 {
		t.Fatalf("all bundled skill command snippets should resolve or be explicitly allowed:\n%s", strings.Join(unresolved, "\n"))
	}
}

func TestBundledSkillDescribesAgentLaneDecisionFlow(t *testing.T) {
	skill := string(mustEmbeddedSkillForValidationTest(t))
	for _, required := range []string{
		"Preview lane",
		"Evidence/QA lane",
		"Support lane",
		"preview:<id>",
		"exact artifact paths",
		"cache inspect",
		"Trust the installed CLI contract",
		"URL means healthy",
	} {
		if !strings.Contains(skill, required) {
			t.Fatalf("bundled skill should include %q in the agent operating flow", required)
		}
	}
}

func TestSkillValidationRejectsFrontmatterDrift(t *testing.T) {
	skill := string(mustEmbeddedSkillForValidationTest(t))
	skill = strings.Replace(skill, "schema: 1", "schema: 2", 1)
	skill = strings.Replace(skill, "license: MIT", "license: Proprietary", 1)
	report, err := validateEmbeddedSkill([]byte(skill))
	if err != nil {
		t.Fatalf("validate embedded skill: %v", err)
	}
	if report.OK {
		t.Fatalf("frontmatter schema/license drift should fail validation: %#v", report)
	}
	if !strings.Contains(strings.Join(report.Errors, "\n"), "frontmatter.schema") || !strings.Contains(strings.Join(report.Errors, "\n"), "frontmatter.license") {
		t.Fatalf("frontmatter drift errors should name schema and license: %#v", report.Errors)
	}
}

func TestSkillCommandSnippetValidationRejectsStaleSubcommands(t *testing.T) {
	for _, command := range []string{
		"vivero doctor prod --json --no-input",
		"vivero release status my-app unexpected --json --no-input",
	} {
		snippet := validateSkillCommandSnippet(skillCommandSnippet{Line: 1, Command: command})
		if snippet.OK {
			t.Fatalf("%q should not pass command manifest validation: %#v", command, snippet)
		}
	}

	for _, command := range []string{
		"vivero doctor config . --json --no-input",
		"vivero schema evidence flow --json --no-input",
		"vivero help preview qa final",
		"vivero cache inspect my-app --json --no-input",
	} {
		snippet := validateSkillCommandSnippet(skillCommandSnippet{Line: 1, Command: command})
		if !snippet.OK {
			t.Fatalf("%q should resolve to the command manifest: %#v", command, snippet)
		}
	}
}

func TestSkillCommandSnippetValidationRejectsUnknownFlagsAndTypos(t *testing.T) {
	for _, command := range []string{
		"vivero up webapp --stale-flag --json --no-input",
		"vivro up webapp --json --no-input",
	} {
		snippet := validateSkillCommandSnippet(skillCommandSnippet{Line: 1, Command: command})
		if snippet.OK {
			t.Fatalf("%q should not pass command validation: %#v", command, snippet)
		}
	}

	for _, command := range []string{
		"vivero evidence screenshot preview:webapp-local web / --breakpoints --json --no-input --quiet",
		"vivero evidence qa record preview:webapp-local --scope public --storage-state auth.json --json --no-input --quiet",
	} {
		snippet := validateSkillCommandSnippet(skillCommandSnippet{Line: 1, Command: command})
		if !snippet.OK {
			t.Fatalf("%q should pass known CLI flag validation: %#v", command, snippet)
		}
	}
}

func TestSkillSectionsIgnoreFencedHeadings(t *testing.T) {
	skill := []byte("---\nname: vivero\nversion: 0.1.0\nvivero_cli: 0.1.0\nschema: 1\nlicense: MIT\ndescription: valid\n---\n\n```md\n## Mental model\n## First checks\n## Choose the lane\n## Preview flow\n## Evidence/QA flow\n## Cache/speed flow\n## Failure playbooks\n## Teardown and safety\n## Secrets rules\n## Verification gates\n```\n")
	report, err := validateEmbeddedSkill(skill)
	if err != nil {
		t.Fatalf("validate embedded skill: %v", err)
	}
	if report.OK {
		t.Fatalf("headings inside fenced code should not satisfy required sections: %#v", report.RequiredSections)
	}
}

func TestSkillDoctorReportsEmbeddedValidation(t *testing.T) {
	code, stdout, stderr := runCLITestCommand(t, t.TempDir(), "skill", "doctor", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("skill doctor exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		EmbeddedVersion    string `json:"embeddedVersion"`
		EmbeddedSHA256     string `json:"embeddedSha256"`
		EmbeddedValidation struct {
			OK                bool                  `json:"ok"`
			Version           string                `json:"version"`
			DescriptionLength int                   `json:"descriptionLength"`
			RequiredSections  []skillSectionCheck   `json:"requiredSections"`
			CommandSnippets   []skillCommandSnippet `json:"commandSnippets"`
			Errors            []string              `json:"errors"`
		} `json:"embeddedValidation"`
		Targets []struct {
			Path    string `json:"path"`
			Exists  bool   `json:"exists"`
			Current bool   `json:"current"`
		} `json:"targets"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid skill doctor JSON: %v stdout=%s", err, stdout)
	}
	if payload.EmbeddedVersion == "" || payload.EmbeddedSHA256 == "" || len(payload.Targets) == 0 {
		t.Fatalf("skill doctor should keep existing installed-target freshness fields: %#v", payload)
	}
	if !payload.EmbeddedValidation.OK {
		t.Fatalf("skill doctor should report passing embedded validation: %#v", payload.EmbeddedValidation.Errors)
	}
	if payload.EmbeddedValidation.Version != payload.EmbeddedVersion {
		t.Fatalf("embedded validation version = %q, want doctor version %q", payload.EmbeddedValidation.Version, payload.EmbeddedVersion)
	}
	if payload.EmbeddedValidation.DescriptionLength == 0 || len(payload.EmbeddedValidation.RequiredSections) == 0 || len(payload.EmbeddedValidation.CommandSnippets) == 0 {
		t.Fatalf("embedded validation should include frontmatter, section, and command snippet details: %#v", payload.EmbeddedValidation)
	}
}

func TestSkillPrintIncludesVersionAndHash(t *testing.T) {
	code, stdout, stderr := runCLITestCommand(t, t.TempDir(), "skill", "print", "--json", "--no-input")
	if code != 0 || stderr != "" {
		t.Fatalf("skill print exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		Content string `json:"content"`
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid skill print JSON: %v stdout=%s", err, stdout)
	}
	if payload.Content == "" {
		t.Fatal("skill print should include content")
	}
	if payload.Version == "" || payload.Version == "unknown" {
		t.Fatalf("skill print should include concrete version, got %q", payload.Version)
	}
	if len(payload.SHA256) != 64 {
		t.Fatalf("skill print sha256 length = %d, want 64", len(payload.SHA256))
	}
	if got := hashBytes([]byte(payload.Content)); got != payload.SHA256 {
		t.Fatalf("skill print sha256 = %s, want content hash %s", payload.SHA256, got)
	}
	if !strings.Contains(payload.Content, "version: "+payload.Version) {
		t.Fatalf("skill print version %q should match embedded frontmatter", payload.Version)
	}
}

func mustEmbeddedSkillForValidationTest(t *testing.T) []byte {
	t.Helper()
	skill, err := embeddedSkill()
	if err != nil {
		t.Fatal(err)
	}
	return skill
}
