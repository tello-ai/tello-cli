package guide

import (
	"errors"
	"strings"
	"testing"
)

func TestTextKnownAndUnknownTopics(t *testing.T) {
	for _, topic := range Topics() {
		for _, brief := range []bool{false, true} {
			text, err := Text(topic, brief)
			if err != nil || strings.TrimSpace(text) == "" {
				t.Fatalf("Text(%q, %v) = %q, %v", topic, brief, text, err)
			}
		}
	}
	if _, err := Text("nope", false); !errors.Is(err, ErrUnknownTopic) {
		t.Fatalf("Text(nope) error = %v, want ErrUnknownTopic", err)
	}
}

func TestSkillFiles(t *testing.T) {
	want := map[string]string{
		"claude": ".claude/skills/tello/SKILL.md",
		"codex":  ".codex/skills/tello/SKILL.md",
	}
	targets := SkillTargets()
	if len(targets) != len(want) {
		t.Fatalf("SkillTargets = %v", targets)
	}
	for _, target := range targets {
		path, content, err := SkillFile(target)
		if err != nil {
			t.Fatalf("SkillFile(%q): %v", target, err)
		}
		if path != want[target] {
			t.Fatalf("SkillFile(%q) path = %q, want %q", target, path, want[target])
		}
		fm := frontmatter(t, string(content))
		if fm["name"] != "tello" || fm["description"] == "" {
			t.Fatalf("SkillFile(%q) frontmatter = %v, want name tello and a description", target, fm)
		}
	}
	if _, _, err := SkillFile("vim"); !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf("SkillFile(vim) error = %v, want ErrUnknownTarget", err)
	}
}

// frontmatter parses the leading "---" block of flat "key: value" lines that
// skill loaders read.
func frontmatter(t *testing.T, content string) map[string]string {
	t.Helper()
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		t.Fatalf("skill file does not start with frontmatter:\n%s", content)
	}
	block, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		t.Fatalf("unterminated frontmatter:\n%s", content)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok || strings.Contains(value, ": ") || strings.Contains(value, " #") {
			t.Fatalf("frontmatter line %q is not a plain YAML scalar", line)
		}
		fields[key] = value
	}
	return fields
}
