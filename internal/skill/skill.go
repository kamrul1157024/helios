// The Helios skill: the manual an agent reads to drive the CLI.
//
// Installed rather than documented, because the agent that needs it is the one
// Helios itself starts — the desktop's "describe it and let an agent build it"
// button opens a session and expects `helios schedule add` to be already
// understood. A manual nobody installed is a manual nobody reads.
package skill

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// The skill's source of truth lives in the repo, where it can be reviewed like
// anything else, and is carried in the binary so `helios setup` needs no
// checkout.
//
//go:embed SKILL.md
var content string

// Name is the directory the skill installs into, and the name agents call it by.
const Name = "helios"

// Dir is where a Claude skill lives for the user.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "skills", Name), nil
}

// Path is the file itself.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "SKILL.md"), nil
}

// Install writes the skill, replacing an older copy.
//
// Overwritten rather than merged: this file is ours, it changes when the CLI
// changes, and a half-updated manual is worse than none.
func Install() (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create skill directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write skill: %w", err)
	}
	return path, nil
}

// Installed reports whether the skill on disk is the one this build carries.
//
// Both questions in one: an older copy is as good as missing, because the flags
// it documents may no longer exist.
func Installed() (present bool, current bool) {
	path, err := Path()
	if err != nil {
		return false, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	return true, string(data) == content
}

// Remove takes it away again, for `helios setup skill --remove`.
func Remove() error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove skill: %w", err)
	}
	return nil
}
