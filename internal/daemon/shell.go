package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const shellMarkerStart = "# >>> helios claude wrapper >>>"
const shellMarkerEnd = "# <<< helios claude wrapper <<<"

// ShellInfo describes the user's shell and where to install the wrapper.
type ShellInfo struct {
	Name   string // zsh, bash, fish
	RCPath string // absolute path to the rc file
	Syntax string // posix or fish
}

// DetectShell returns info about the user's login shell.
func DetectShell() ShellInfo {
	shell := os.Getenv("SHELL")
	home, _ := os.UserHomeDir()

	switch filepath.Base(shell) {
	case "zsh":
		return ShellInfo{Name: "zsh", RCPath: filepath.Join(home, ".zshrc"), Syntax: "posix"}
	case "bash":
		rcPath := filepath.Join(home, ".bashrc")
		if runtime.GOOS == "darwin" {
			// macOS bash reads .bash_profile for login shells
			if _, err := os.Stat(filepath.Join(home, ".bash_profile")); err == nil {
				rcPath = filepath.Join(home, ".bash_profile")
			}
		}
		return ShellInfo{Name: "bash", RCPath: rcPath, Syntax: "posix"}
	case "fish":
		return ShellInfo{Name: "fish", RCPath: filepath.Join(home, ".config", "fish", "config.fish"), Syntax: "fish"}
	default:
		return ShellInfo{Name: filepath.Base(shell), Syntax: "unknown"}
	}
}

// ShellWrapperSnippet returns the shell wrapper code for the given shell syntax.
func ShellWrapperSnippet(syntax string) string {
	switch syntax {
	case "posix":
		return fmt.Sprintf(`%s
claude() {
  if [ -n "$TMUX" ]; then
    command claude "$@"
    return
  fi
  helios wrap -- claude "$@"
}
%s`, shellMarkerStart, shellMarkerEnd)
	case "fish":
		return fmt.Sprintf(`%s
function claude
  if set -q TMUX
    command claude $argv
    return
  end
  helios wrap -- claude $argv
end
%s`, shellMarkerStart, shellMarkerEnd)
	default:
		return ""
	}
}

// ShellWrapperInstalled checks if the wrapper is already in the rc file.
func ShellWrapperInstalled(info ShellInfo) bool {
	if info.RCPath == "" {
		return false
	}
	data, err := os.ReadFile(info.RCPath)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), shellMarkerStart)
}

// InstallShellWrapper appends the wrapper to the user's shell rc file.
// Returns an error message suitable for showing manual instructions.
func InstallShellWrapper(info ShellInfo) error {
	if info.RCPath == "" {
		return fmt.Errorf("unsupported shell: %s", info.Name)
	}
	if info.Syntax == "unknown" {
		return fmt.Errorf("unsupported shell syntax: %s", info.Name)
	}

	snippet := ShellWrapperSnippet(info.Syntax)
	if snippet == "" {
		return fmt.Errorf("no wrapper snippet for shell: %s", info.Name)
	}

	// Check if already installed
	if ShellWrapperInstalled(info) {
		return nil
	}

	// Ensure parent directory exists (for fish)
	dir := filepath.Dir(info.RCPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	f, err := os.OpenFile(info.RCPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open %s: %w", info.RCPath, err)
	}
	defer f.Close()

	if _, err := f.WriteString("\n" + snippet + "\n"); err != nil {
		return fmt.Errorf("write to %s: %w", info.RCPath, err)
	}

	return nil
}

// RemoveShellWrapper removes the helios wrapper from the rc file.
func RemoveShellWrapper(info ShellInfo) error {
	if info.RCPath == "" {
		return fmt.Errorf("unsupported shell: %s", info.Name)
	}

	data, err := os.ReadFile(info.RCPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", info.RCPath, err)
	}

	content := string(data)
	startIdx := strings.Index(content, shellMarkerStart)
	if startIdx < 0 {
		return nil // not installed
	}

	endIdx := strings.Index(content, shellMarkerEnd)
	if endIdx < 0 {
		return fmt.Errorf("found start marker but no end marker in %s — edit manually", info.RCPath)
	}

	// Remove from start marker to end marker (inclusive) plus surrounding newlines
	before := strings.TrimRight(content[:startIdx], "\n")
	after := content[endIdx+len(shellMarkerEnd):]
	after = strings.TrimLeft(after, "\n")

	newContent := before
	if after != "" {
		newContent += "\n" + after
	}

	if err := os.WriteFile(info.RCPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("write %s: %w", info.RCPath, err)
	}

	return nil
}

// ManualShellInstructions returns human-readable instructions for manual installation.
func ManualShellInstructions(info ShellInfo, err error) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("  Could not auto-configure %s: %v\n\n", info.Name, err))

	if info.Syntax == "unknown" {
		b.WriteString("  Add a wrapper function for your shell that:\n")
		b.WriteString("    1. If inside tmux ($TMUX is set), run: command claude <args>\n")
		b.WriteString("    2. Otherwise, run: helios wrap -- claude <args>\n")
		return b.String()
	}

	snippet := ShellWrapperSnippet(info.Syntax)
	rcPath := info.RCPath
	if rcPath == "" {
		rcPath = "your shell rc file"
	}

	b.WriteString(fmt.Sprintf("  Add this to %s:\n\n", rcPath))
	for _, line := range strings.Split(snippet, "\n") {
		b.WriteString("    " + line + "\n")
	}

	return b.String()
}
