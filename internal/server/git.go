package server

import (
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
)

type gitChange struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// handleGitStatus returns branch, ahead/behind, and changed file lists.
func (s *PublicServer) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		jsonError(w, "missing path", http.StatusBadRequest)
		return
	}

	clean, err := resolveSafePath(path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Find repo root from the given path.
	root, err := gitCmd(clean, "rev-parse", "--show-toplevel")
	if err != nil {
		jsonError(w, "not a git repository", http.StatusBadRequest)
		return
	}
	root = strings.TrimSpace(root)

	// Branch name.
	branch, err := gitCmd(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		jsonError(w, "failed to get branch", http.StatusInternalServerError)
		return
	}
	branch = strings.TrimSpace(branch)

	// Ahead/behind upstream.
	ahead, behind := 0, 0
	if ab, err := gitCmd(root, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); err == nil {
		parts := strings.Fields(strings.TrimSpace(ab))
		if len(parts) == 2 {
			ahead, _ = strconv.Atoi(parts[0])
			behind, _ = strconv.Atoi(parts[1])
		}
	}

	// Staged changes.
	staged := parseNameStatus(root, "--cached")

	// Unstaged changes.
	unstaged := parseNameStatus(root)

	// Untracked files.
	untracked := []gitChange{}
	if out, err := gitCmd(root, "ls-files", "--others", "--exclude-standard"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			untracked = append(untracked, gitChange{Path: line, Status: "?"})
		}
	}

	dirty := len(staged) > 0 || len(unstaged) > 0 || len(untracked) > 0

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"root":      root,
		"branch":    branch,
		"dirty":     dirty,
		"ahead":     ahead,
		"behind":    behind,
		"staged":    staged,
		"unstaged":  unstaged,
		"untracked": untracked,
	})
}

// handleGitDiff returns the unified diff for a single file.
func (s *PublicServer) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	file := r.URL.Query().Get("file")
	if path == "" || file == "" {
		jsonError(w, "missing path or file", http.StatusBadRequest)
		return
	}

	clean, err := resolveSafePath(path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	root, err := gitCmd(clean, "rev-parse", "--show-toplevel")
	if err != nil {
		jsonError(w, "not a git repository", http.StatusBadRequest)
		return
	}
	root = strings.TrimSpace(root)

	staged := r.URL.Query().Get("staged") == "true"

	// Get the diff.
	var diff string
	if staged {
		diff, err = gitCmd(root, "diff", "--cached", "--", file)
	} else {
		diff, err = gitCmd(root, "diff", "--", file)
	}
	if err != nil {
		jsonError(w, "failed to get diff", http.StatusInternalServerError)
		return
	}

	// Get stat line.
	var stat string
	if staged {
		stat, _ = gitCmd(root, "diff", "--cached", "--stat", "--", file)
	} else {
		stat, _ = gitCmd(root, "diff", "--stat", "--", file)
	}
	// Parse stat to "+N -M" format.
	statLine := parseStatLine(stat)

	// Infer language from extension.
	ext := ""
	if idx := strings.LastIndex(file, "."); idx >= 0 {
		ext = file[idx+1:]
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"file":     file,
		"language": ext,
		"diff":     diff,
		"stat":     statLine,
	})
}

type worktreeEntry struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	IsMain bool   `json:"is_main"`
}

// handleGitWorktrees lists all worktrees for the repo containing the given path.
func (s *PublicServer) handleGitWorktrees(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		jsonError(w, "missing path", http.StatusBadRequest)
		return
	}

	clean, err := resolveSafePath(path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	root, err := gitCmd(clean, "rev-parse", "--show-toplevel")
	if err != nil {
		jsonError(w, "not a git repository", http.StatusBadRequest)
		return
	}
	root = strings.TrimSpace(root)

	out, err := gitCmd(root, "worktree", "list", "--porcelain")
	if err != nil {
		jsonError(w, "failed to list worktrees", http.StatusInternalServerError)
		return
	}

	worktrees := parseWorktreeList(out, root)

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"worktrees": worktrees,
	})
}

// parseWorktreeList parses `git worktree list --porcelain` output.
func parseWorktreeList(out string, mainRoot string) []worktreeEntry {
	var worktrees []worktreeEntry
	var current worktreeEntry

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = worktreeEntry{}
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			current.Path = strings.TrimPrefix(line, "worktree ")
			current.IsMain = current.Path == mainRoot
		} else if strings.HasPrefix(line, "branch ") {
			// branch refs/heads/main -> main
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		} else if line == "detached" {
			current.Branch = "(detached)"
		}
	}
	// Flush last entry.
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}
	return worktrees
}

// gitCmd runs a git command in the given directory and returns stdout.
func gitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// parseNameStatus parses `git diff --name-status` output into gitChange slices.
// Pass "--cached" for staged, or nothing for unstaged.
func parseNameStatus(root string, extraArgs ...string) []gitChange {
	args := []string{"diff", "--name-status"}
	args = append(args, extraArgs...)
	out, err := gitCmd(root, args...)
	if err != nil {
		return []gitChange{}
	}
	var changes []gitChange
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		status := parts[0]
		p := parts[len(parts)-1]
		// For renames (R100), use the destination path.
		if strings.HasPrefix(status, "R") {
			status = "R"
		}
		changes = append(changes, gitChange{Path: p, Status: status})
	}
	return changes
}

// parseStatLine extracts "+N -M" from git diff --stat output.
func parseStatLine(stat string) string {
	lines := strings.Split(strings.TrimSpace(stat), "\n")
	if len(lines) == 0 {
		return ""
	}
	// Last line looks like: " 1 file changed, 5 insertions(+), 2 deletions(-)"
	last := lines[len(lines)-1]
	var ins, del int
	for _, part := range strings.Fields(last) {
		if n, err := strconv.Atoi(part); err == nil {
			// First number after "changed" is insertions, second is deletions.
			if strings.Contains(last[strings.Index(last, part):], "insertion") || strings.Contains(last[strings.Index(last, part):], "deletion") {
				if ins == 0 && strings.Contains(last, "insertion") {
					ins = n
				} else {
					del = n
				}
			}
		}
	}
	// Simpler approach: just scan for numbers next to +/-.
	ins = 0
	del = 0
	parts := strings.Fields(last)
	for i, p := range parts {
		if strings.HasPrefix(p, "insertion") && i > 0 {
			ins, _ = strconv.Atoi(parts[i-1])
		}
		if strings.HasPrefix(p, "deletion") && i > 0 {
			del, _ = strconv.Atoi(parts[i-1])
		}
	}
	if ins == 0 && del == 0 {
		return ""
	}
	return fmt.Sprintf("+%d -%d", ins, del)
}

