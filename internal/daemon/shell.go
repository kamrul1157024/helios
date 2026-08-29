package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kamrul1157024/helios/internal/provider"
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

// wrappedCommands returns the command every registered provider is started by.
//
// Read from the registry rather than written out here. The wrapper named only
// claude, so a codex session started in a terminal ran outside Helios: no host
// to attach to, and waking it later meant a second `codex resume` against a
// rollout the live process still holds, which Codex refuses outright.
func wrappedCommands() []string {
	var out []string
	for _, info := range provider.Infos() {
		if info.Command != "" {
			out = append(out, info.Command)
		}
	}
	return out
}

// ShellWrapperSnippet returns the shell wrapper code for the given shell syntax.
//
// The wrapper delegates unconditionally, including inside a helios terminal: a
// session started from in there is a session of its own and needs to be
// registered like any other. It cannot recurse — a host executes its agent
// directly, so the rc file this lives in is never read again inside one.
func ShellWrapperSnippet(syntax string) string {
	var body strings.Builder
	for _, cmd := range wrappedCommands() {
		switch syntax {
		case "posix":
			fmt.Fprintf(&body, "%s() {\n  helios wrap -- %s \"$@\"\n}\n", cmd, cmd)
		case "fish":
			fmt.Fprintf(&body, "function %s\n  helios wrap -- %s $argv\nend\n", cmd, cmd)
		default:
			return ""
		}
	}
	if body.Len() == 0 {
		return ""
	}
	return fmt.Sprintf("%s\n%s%s", shellMarkerStart, body.String(), shellMarkerEnd)
}

// ShellWrapperInstalled reports whether the wrapper is live in the rc file.
//
// The marker has to be the whole line. It is itself a comment, so a block the
// user commented out still contains it — "# # >>> helios claude wrapper >>>" —
// and a substring test called that installed. Helios then reported a green
// tick for a wrapper the shell never defines, which is how a machine came to
// run every agent unwrapped while setup insisted it was done.
func ShellWrapperInstalled(info ShellInfo) bool {
	if info.RCPath == "" {
		return false
	}
	data, err := os.ReadFile(info.RCPath)
	if err != nil {
		return false
	}
	return markerLine(string(data), shellMarkerStart) >= 0
}

// markerLine returns the offset of the marker where it stands as a line of its
// own, or -1.
func markerLine(content, marker string) int {
	offset := 0
	for _, line := range strings.SplitAfter(content, "\n") {
		if strings.TrimRight(line, " \t\r\n") == marker {
			return offset
		}
		offset += len(line)
	}
	return -1
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

	// Whole lines, for the same reason the install check uses them: a block the
	// user commented out holds both markers too, and a substring search finds
	// that one first — removing the dead copy and leaving the live wrapper.
	content := string(data)
	startIdx := markerLine(content, shellMarkerStart)
	if startIdx < 0 {
		return nil // not installed
	}

	endIdx := markerLine(content, shellMarkerEnd)
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
		b.WriteString("  Add a wrapper function for your shell that runs:\n")
		for _, cmd := range wrappedCommands() {
			b.WriteString(fmt.Sprintf("    helios wrap -- %s <args>\n", cmd))
		}
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
