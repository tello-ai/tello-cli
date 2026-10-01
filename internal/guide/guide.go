// Package guide embeds the coding-agent operating guide and the skill file
// that `tello init` installs (docs/design.md section 11).
package guide

import (
	"embed"
	"errors"
	"fmt"
)

//go:embed content/*.md
var content embed.FS

var (
	ErrUnknownTopic  = errors.New("unknown guide topic")
	ErrUnknownTarget = errors.New("unknown skill target")
)

const topicCodingAgent = "coding-agent"

// skillPaths maps each init target to its skill file, relative to the
// project directory and slash-separated.
var skillPaths = map[string]string{
	"claude": ".claude/skills/tello/SKILL.md",
	"codex":  ".codex/skills/tello/SKILL.md",
}

// Topics lists the guide topics.
func Topics() []string { return []string{topicCodingAgent} }

// Text returns a guide topic; brief selects the compact version.
func Text(topic string, brief bool) (string, error) {
	if topic != topicCodingAgent {
		return "", fmt.Errorf("%w %q", ErrUnknownTopic, topic)
	}
	name := "content/coding-agent.md"
	if brief {
		name = "content/coding-agent.brief.md"
	}
	data, err := content.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SkillTargets lists the coding agents `tello init` writes skill files for.
func SkillTargets() []string { return []string{"claude", "codex"} }

// SkillFile returns the skill file for target: its slash-separated path
// relative to the project directory and its content. The content does not
// carry the CLI version, so it only changes when the template does.
func SkillFile(target string) (string, []byte, error) {
	path, ok := skillPaths[target]
	if !ok {
		return "", nil, fmt.Errorf("%w %q", ErrUnknownTarget, target)
	}
	data, err := content.ReadFile("content/SKILL.md")
	if err != nil {
		return "", nil, err
	}
	return path, data, nil
}
