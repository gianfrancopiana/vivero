package skills

import (
	"strings"
	"testing"
)

func TestSkillMarkdownEmbedsBundledViveroSkill(t *testing.T) {
	body, err := SkillMarkdown()
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"name: vivero", "vivero preview up", "vivero evidence flow"} {
		if !strings.Contains(text, want) {
			t.Fatalf("embedded Vivero skill should contain %q", want)
		}
	}
}
