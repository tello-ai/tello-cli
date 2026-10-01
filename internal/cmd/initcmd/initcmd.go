// Package initcmd implements `tello init`, which installs the coding-agent
// skill files (docs/design.md section 11).
package initcmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tello-ai/tello-cli/internal/app"
	"github.com/tello-ai/tello-cli/internal/guide"
)

type fileResult struct {
	// Path is relative to the target directory, slash-separated.
	Path string `json:"path"`
	// Status is "created", "updated" or "unchanged".
	Status string `json:"status"`
}

// New returns the `init` command.
func New(a *app.App) *cobra.Command {
	var (
		targets []string
		dir     string
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create or refresh the coding-agent skill files",
		Args:  app.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			selected, err := validTargets(targets)
			if err != nil {
				return err
			}
			root, err := resolveDir(a, dir)
			if err != nil {
				return err
			}
			files := make([]fileResult, 0, len(selected))
			for _, target := range selected {
				res, err := install(root, target)
				if err != nil {
					return err
				}
				files = append(files, res)
				if !a.JSON {
					a.Printf("%-9s %s\n", res.Status, res.Path)
				}
			}
			return a.WriteResult(map[string]any{"files": files})
		},
	}
	cmd.Flags().StringSliceVar(&targets, "target", guide.SkillTargets(), "coding agents to install skill files for ("+strings.Join(guide.SkillTargets(), ", ")+")")
	cmd.Flags().StringVar(&dir, "dir", "", "project directory (default: current directory)")
	return cmd
}

// validTargets rejects unknown targets and drops duplicates, keeping order.
func validTargets(targets []string) ([]string, error) {
	known := guide.SkillTargets()
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if !slices.Contains(known, target) {
			return nil, app.Usagef("unknown target %q (available: %s)", target, strings.Join(known, ", "))
		}
		if !slices.Contains(out, target) {
			out = append(out, target)
		}
	}
	if len(out) == 0 {
		return nil, app.Usagef("no target given (available: %s)", strings.Join(known, ", "))
	}
	return out, nil
}

// resolveDir returns the absolute project directory, which must exist.
func resolveDir(a *app.App, dir string) (string, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		wd, err := a.Getwd()
		if err != nil {
			return "", fmt.Errorf("get current directory: %w", err)
		}
		dir = filepath.Join(wd, dir)
	}
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", app.Usagef("directory %s does not exist", dir)
	}
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", app.Usagef("%s is not a directory", dir)
	}
	return dir, nil
}

// install writes target's skill file under root unless it already holds
// the current template.
func install(root, target string) (fileResult, error) {
	rel, content, err := guide.SkillFile(target)
	if err != nil {
		return fileResult{}, err
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	status := "updated"
	existing, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		status = "created"
	case err != nil:
		return fileResult{}, fmt.Errorf("read %s: %w", rel, err)
	case bytes.Equal(existing, content):
		return fileResult{Path: rel, Status: "unchanged"}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fileResult{}, fmt.Errorf("create %s: %w", filepath.Dir(rel), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fileResult{}, fmt.Errorf("write %s: %w", rel, err)
	}
	return fileResult{Path: rel, Status: status}, nil
}
