package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// gitTimeout bounds every git call. A repository with a stale index.lock will
// otherwise hang the request until the client gives up.
const gitTimeout = 15 * time.Second

// maxWorktreeDetail caps how many worktrees are enriched with branch state:
// each one costs three more git calls.
const maxWorktreeDetail = 30

type gitChange struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// gitRepoRoot resolves a client-supplied path and finds the repository holding it.
func gitRepoRoot(path string) (string, error) {
	if path == "" {
		return "", errors.New("missing path")
	}
	clean, err := resolveSafePath(path)
	if err != nil {
		return "", err
	}
	out, err := gitCmd(clean, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errors.New("not a git repository")
	}
	return strings.TrimSpace(out), nil
}

// handleGitStatus returns branch, ahead/behind, and changed file lists.
func (s *PublicServer) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	root, err := gitRepoRoot(r.URL.Query().Get("path"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

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

// handleGitDiff returns the unified diff for a single file, in the working tree
// or at a revision.
func (s *PublicServer) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	file := r.URL.Query().Get("file")
	if file == "" {
		jsonError(w, "missing file", http.StatusBadRequest)
		return
	}
	root, err := gitRepoRoot(r.URL.Query().Get("path"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if (from != "" && !validRevision(from)) || (to != "" && !validRevision(to)) {
		jsonError(w, "invalid revision", http.StatusBadRequest)
		return
	}

	diff, err := fileDiff(root, file, from, to, r.URL.Query())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Infer language from extension.
	ext := ""
	if idx := strings.LastIndex(file, "."); idx >= 0 {
		ext = file[idx+1:]
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"file":     file,
		"language": ext,
		"diff":     diff,
		"stat":     statFromDiff(diff),
	})
}

// fileDiff picks the comparison the query asked for: an untracked file against
// nothing, a commit against its parent, one revision against another, or the
// working tree.
func fileDiff(root, file, from, to string, query map[string][]string) (string, error) {
	value := func(name string) string {
		if v := query[name]; len(v) > 0 {
			return v[0]
		}
		return ""
	}

	switch {
	case value("untracked") == "true":
		return diffNoIndex(root, file)
	case to != "" && from == "":
		return gitCmd(root, "show", "--format=", "--no-color", "--find-renames", to, "--", file)
	case to != "" && from != "":
		return gitCmd(root, "diff", "--no-color", "--find-renames", from, to, "--", file)
	case value("staged") == "true":
		return gitCmd(root, "diff", "--no-color", "--cached", "--", file)
	default:
		return gitCmd(root, "diff", "--no-color", "--", file)
	}
}

// diffNoIndex diffs a file git does not track yet. --no-index exits 1 when the
// files differ, which here is the expected outcome rather than a failure.
func diffNoIndex(root, file string) (string, error) {
	out, code, err := gitRun(root, "diff", "--no-color", "--no-index", "--", os.DevNull, file)
	if err != nil && code != 1 {
		return "", err
	}
	return out, nil
}

// statFromDiff counts the patch's own +/- lines, which is cheaper and more
// honest than parsing the prose of `git diff --stat`.
func statFromDiff(diff string) string {
	insertions, deletions := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			insertions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	if insertions == 0 && deletions == 0 {
		return ""
	}
	return fmt.Sprintf("+%d -%d", insertions, deletions)
}

type worktreeEntry struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	IsMain bool   `json:"is_main"`
	// The rest describe how far along the branch in that worktree is — enough
	// to tell several parallel agents apart at a glance.
	Head     string `json:"head,omitempty"`
	Subject  string `json:"subject,omitempty"`
	Detached bool   `json:"detached"`
	Locked   bool   `json:"locked"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Dirty    int    `json:"dirty"`
	// Base names what ahead and behind were measured against — the tracking
	// branch when there is one, otherwise the repository's base branch.
	Base string `json:"base,omitempty"`
}

// handleGitWorktrees lists all worktrees for the repo containing the given path.
func (s *PublicServer) handleGitWorktrees(w http.ResponseWriter, r *http.Request) {
	root, err := gitRepoRoot(r.URL.Query().Get("path"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	out, err := gitCmd(root, "worktree", "list", "--porcelain")
	if err != nil {
		jsonError(w, "failed to list worktrees", http.StatusInternalServerError)
		return
	}

	worktrees := parseWorktreeList(out, root)
	for i := range worktrees {
		if i >= maxWorktreeDetail {
			break
		}
		describeWorktree(&worktrees[i])
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"worktrees": worktrees,
	})
}

// describeWorktree fills in branch state. A worktree whose directory has been
// deleted but not pruned answers nothing, and is left as the porcelain had it.
func describeWorktree(entry *worktreeEntry) {
	if out, err := gitCmd(entry.Path, "log", "-1", "--format=%s"); err == nil {
		entry.Subject = strings.TrimSpace(out)
	}
	if out, err := gitCmd(entry.Path, "status", "--porcelain"); err == nil {
		entry.Dirty = countLines(out)
	}

	// A worktree cut for one feature usually has no upstream yet, and "0 ahead,
	// 0 behind" would say nothing about it. Fall back to the base branch, and
	// name whatever was compared against.
	entry.Base = "@{upstream}"
	ahead, behind, err := countAheadBehind(entry.Path, entry.Base)
	if err != nil {
		entry.Base = resolveBase(entry.Path)
		if entry.Base == "" {
			return
		}
		ahead, behind, err = countAheadBehind(entry.Path, entry.Base)
		if err != nil {
			entry.Base = ""
			return
		}
	}
	entry.Ahead, entry.Behind = ahead, behind
}

// countAheadBehind counts each side separately rather than asking for
// "HEAD...base": the two-call form keeps the revision its own argument instead
// of concatenating it into a range string.
func countAheadBehind(dir, base string) (ahead, behind int, err error) {
	ahead, err = countRevs(dir, "HEAD", base)
	if err != nil {
		return 0, 0, err
	}
	behind, err = countRevs(dir, base, "HEAD")
	if err != nil {
		return 0, 0, err
	}
	return ahead, behind, nil
}

// countRevs counts commits reachable from have but not from without.
func countRevs(dir, have, without string) (int, error) {
	out, err := gitCmd(dir, "rev-list", "--count", have, "--not", without)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parse rev-list count %q: %w", out, err)
	}
	return n, nil
}

func countLines(out string) int {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

// parseWorktreeList parses `git worktree list --porcelain` output.
func parseWorktreeList(out string, mainRoot string) []worktreeEntry {
	// Not nil: a nil slice marshals to JSON null, and clients expect a list.
	worktrees := []worktreeEntry{}
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
		switch {
		case strings.HasPrefix(line, "worktree "):
			current.Path = strings.TrimPrefix(line, "worktree ")
			current.IsMain = current.Path == mainRoot
		case strings.HasPrefix(line, "HEAD "):
			current.Head = shortSHA(strings.TrimPrefix(line, "HEAD "))
		case strings.HasPrefix(line, "branch "):
			// branch refs/heads/main -> main
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "detached":
			current.Branch = "(detached)"
			current.Detached = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			current.Locked = true
		}
	}
	// Flush last entry.
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}
	return worktrees
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// gitCmd runs a git command in the given directory and returns stdout.
func gitCmd(dir string, args ...string) (string, error) {
	out, _, err := gitRun(dir, args...)
	return out, err
}

// gitRun also reports the exit status, for the callers where a non-zero one is
// an answer rather than a failure.
func gitRun(dir string, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return string(out), 0, nil
	}

	code := -1
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	}
	// git says why on stderr; without it every failure reads the same.
	reason := strings.TrimSpace(stderr.String())
	if reason == "" {
		reason = err.Error()
	}
	return string(out), code, fmt.Errorf("git %s: %s", strings.Join(args, " "), firstLine(reason))
}

func firstLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
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
	// Not nil: a nil slice marshals to JSON null, and clients expect a list.
	changes := []gitChange{}
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
