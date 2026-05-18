package skills

import "embed"

//go:embed vivero
var content embed.FS

func SkillMarkdown() ([]byte, error) {
	return content.ReadFile("vivero/SKILL.md")
}
