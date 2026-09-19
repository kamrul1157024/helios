package claude

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/kamrul1157024/helios/internal/provider"
)

// PrepareFork puts the parent's transcript where the forked agent will look for
// it.
//
// `claude --resume <id>` does not search: it looks in the one project directory
// named by the process's working directory. A fork given a worktree of its own
// therefore cannot see its parent, answers "No conversation found with session
// ID", and exits — before any hook fires, so the daemon records a session that
// reads as idle and holds nothing. Copying the transcript across is what makes
// the resume resolve.
//
// A copy rather than a move or a link. The parent keeps its own conversation and
// stays resumable; a symlink would be rewritten by the first compaction, and
// Claude writes the fork under its new id, so the copy is read once and then
// left alone.
func (p *Provider) PrepareFork(parentCWD, forkCWD, parentSessionID, parentResumeID string) error {
	// Claude takes the id Helios minted, so the parent's conversation is named
	// by its session id. parentResumeID is nil for every Claude session.
	_ = parentResumeID

	source := findTranscript(parentSessionID)
	if source == "" {
		return provider.ErrNoConversation
	}

	target := projectDir(forkCWD)
	if target == "" {
		return fmt.Errorf("claude: no project directory for %s", forkCWD)
	}
	destination := filepath.Join(target, parentSessionID+".jsonl")

	// Already there — the fork shares its parent's directory, or a previous
	// attempt got this far.
	if same, err := sameFile(source, destination); err != nil {
		return err
	} else if same {
		return nil
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("claude: make project directory: %w", err)
	}
	return copyFile(source, destination)
}

// findTranscript locates a session's transcript in whichever project holds it.
//
// Scanned rather than derived from the session's recorded cwd: Claude renames a
// transcript's directory when a session moves, so the path recorded at
// SessionStart is not where the file is now.
func findTranscript(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	projects := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(projects)
	if err != nil {
		return ""
	}

	best := ""
	var newest int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(projects, entry.Name(), sessionID+".jsonl")
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		// The newest wins: a session that has moved leaves the old copy behind,
		// and the one still being written to is the real conversation.
		if best == "" || info.ModTime().UnixNano() > newest {
			best, newest = candidate, info.ModTime().UnixNano()
		}
	}
	return best
}

var notNameSafe = regexp.MustCompile(`[^A-Za-z0-9]`)

// projectDir is the directory Claude keeps a working directory's transcripts
// in: the absolute path with every character that is not a letter or a digit
// replaced by a dash.
//
// Symlinks are resolved first because Claude records the real path — on macOS a
// session started in /tmp is filed under -private-tmp, and a copy written to
// the unresolved name would sit in a directory the agent never reads.
func projectDir(cwd string) string {
	if cwd == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		// A directory that does not exist yet still has a name, and the caller
		// is about to create it.
		resolved = cwd
	}
	if !filepath.IsAbs(resolved) {
		if abs, err := filepath.Abs(resolved); err == nil {
			resolved = abs
		}
	}
	return filepath.Join(home, ".claude", "projects", notNameSafe.ReplaceAllString(resolved, "-"))
}

// sameFile reports whether two paths are the same file on disk, so a fork that
// shares its parent's directory copies nothing over itself.
func sameFile(a, b string) (bool, error) {
	infoA, err := os.Stat(a)
	if err != nil {
		return false, fmt.Errorf("claude: read transcript: %w", err)
	}
	infoB, err := os.Stat(b)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("claude: read destination: %w", err)
	}
	return os.SameFile(infoA, infoB), nil
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("claude: open transcript: %w", err)
	}
	defer in.Close()

	// Written beside the destination and renamed into place: a fork that read a
	// half-copied transcript would resume a conversation that stops mid-sentence.
	temp, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".*")
	if err != nil {
		return fmt.Errorf("claude: stage transcript: %w", err)
	}
	staged := temp.Name()
	defer os.Remove(staged)

	if _, err := io.Copy(temp, in); err != nil {
		temp.Close()
		return fmt.Errorf("claude: copy transcript: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("claude: close transcript: %w", err)
	}
	if err := os.Chmod(staged, 0o600); err != nil {
		return fmt.Errorf("claude: set transcript mode: %w", err)
	}
	if err := os.Rename(staged, destination); err != nil {
		return fmt.Errorf("claude: place transcript: %w", err)
	}
	return nil
}
