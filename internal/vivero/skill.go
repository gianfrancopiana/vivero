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
)

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
	re := regexp.MustCompile(`(?m)^version:\s*([^\s]+)\s*$`)
	m := re.FindSubmatch(b)
	if len(m) == 2 {
		return string(m[1])
	}
	return "unknown"
}

func hashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

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
	return map[string]any{"embeddedVersion": skillVersion(b), "embeddedSha256": embeddedHash, "targets": checks}, nil
}

func skillHuman(m map[string]any) string { return fmt.Sprint(m) }
