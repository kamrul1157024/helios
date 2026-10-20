package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

	// Reading a repo's status is what subscribes to it. See filewatch.go.
	s.files().Watch(root, WatchRepo)

	started := time.Now()
	status, err := statusCache.get(root)
	timingFrom(r.Context()).since("git", started)
	if err != nil {
		jsonError(w, "failed to get status", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, status)
}

// gitStatus is the status route's answer. The field names are the API.
type gitStatus struct {
	Root      string      `json:"root"`
	Branch    string      `json:"branch"`
	Dirty     bool        `json:"dirty"`
	Ahead     int         `json:"ahead"`
	Behind    int         `json:"behind"`
	Staged    []gitChange `json:"staged"`
	Unstaged  []gitChange `json:"unstaged"`
	Untracked []gitChange `json:"untracked"`
}

// gitStatusCache holds the last answer per repository with the index
// generation it was computed at. See docs/specs/65-fast-files.md, 1.6.
//
// The generation moves on every raw event under a live root, on .git index
// and HEAD changes, and on a write through PUT /api/file — everything that can
// change the answer. A root that is not live has no such counter, and its
// status is computed fresh every time, which is still one process, not six.
type gitStatusCache struct {
	mu      sync.Mutex
	entries map[string]cachedStatus
}

type cachedStatus struct {
	gen    uint64
	status *gitStatus
}

var statusCache = &gitStatusCache{entries: map[string]cachedStatus{}}

func (c *gitStatusCache) get(root string) (*gitStatus, error) {
	// The index watcher is what makes a cached answer safe, so a repository
	// someone asks the status of gets one, even if nobody has searched it.
	fileIndexes.ensure(root)
	gen, trusted := fileIndexes.generation(root)
	if trusted {
		c.mu.Lock()
		hit, ok := c.entries[root]
		c.mu.Unlock()
		if ok && hit.gen == gen {
			return hit.status, nil
		}
	}

	status, err := readGitStatus(root)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if trusted {
		// Stored under the generation read before git ran: a change during
		// the run moves the counter past it, so this answer is never served
		// for a state it did not see.
		c.entries[root] = cachedStatus{gen: gen, status: status}
	} else {
		delete(c.entries, root)
	}
	return status, nil
}

// readGitStatus asks git once for everything the route reports.
//
// --no-optional-locks stops status refreshing .git/index as a side effect.
// Without it the daemon's own read would move the index, the index watcher
// would bump the generation and rebuild the search list, and the next status
// request would miss the cache it just filled.
func readGitStatus(root string) (*gitStatus, error) {
	out, err := gitCmd(root, "--no-optional-locks", "status",
		"--porcelain=v2", "--branch", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	status := parsePorcelainV2(out)
	status.Root = root
	return status, nil
}

// parsePorcelainV2 reads `git status --porcelain=v2 --branch -z` into the
// shape the route has always returned.
//
// The old route ran rev-parse for the branch, rev-list for ahead and behind,
// two `diff --name-status` for staged and unstaged, and ls-files for
// untracked. Each translation below keeps that answer: the X column is what
// `diff --cached` reported, Y what `diff` reported, a rename reports its
// destination as "R", and a conflict reports "U" on both sides.
func parsePorcelainV2(out string) *gitStatus {
	status := &gitStatus{Staged: []gitChange{}, Unstaged: []gitChange{}, Untracked: []gitChange{}}
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		rec := records[i]
		switch {
		case strings.HasPrefix(rec, "# branch.head "):
			status.Branch = strings.TrimPrefix(rec, "# branch.head ")
			if status.Branch == "(detached)" {
				// What `rev-parse --abbrev-ref HEAD` says for a detached head.
				status.Branch = "HEAD"
			}
		case strings.HasPrefix(rec, "# branch.ab "):
			fmt.Sscanf(strings.TrimPrefix(rec, "# branch.ab "), "+%d -%d", &status.Ahead, &status.Behind)
		case strings.HasPrefix(rec, "1 "):
			addChange(status, rec, 8)
		case strings.HasPrefix(rec, "2 "):
			addChange(status, rec, 9)
			// The rename's original path is the next record; the old route
			// reported only the destination.
			i++
		case strings.HasPrefix(rec, "u "):
			if path := field(rec, 10); path != "" {
				status.Staged = append(status.Staged, gitChange{Path: path, Status: "U"})
				status.Unstaged = append(status.Unstaged, gitChange{Path: path, Status: "U"})
			}
		case strings.HasPrefix(rec, "? "):
			status.Untracked = append(status.Untracked, gitChange{Path: rec[2:], Status: "?"})
		}
	}
	status.Dirty = len(status.Staged) > 0 || len(status.Unstaged) > 0 || len(status.Untracked) > 0
	return status
}

// addChange records an ordinary (1) or renamed (2) entry. pathField is the
// number of space-separated fields before the path, which may itself hold
// spaces.
func addChange(status *gitStatus, rec string, pathField int) {
	path := field(rec, pathField)
	if len(rec) < 4 || path == "" {
		return
	}
	x, y := rec[2], rec[3]
	if x != '.' {
		status.Staged = append(status.Staged, gitChange{Path: path, Status: changeLetter(x)})
	}
	if y != '.' {
		status.Unstaged = append(status.Unstaged, gitChange{Path: path, Status: changeLetter(y)})
	}
}

func changeLetter(c byte) string {
	if c == 'C' {
		// diff --name-status never detected copies without -C, so the old
		// route reported a copy as the new file it is.
		return "A"
	}
	return string(c)
}

// field returns everything from the n-th space-separated field on — the path
// of a porcelain record, spaces and all.
func field(rec string, n int) string {
	for ; n > 0; n-- {
		cut := strings.IndexByte(rec, ' ')
		if cut < 0 {
			return ""
		}
		rec = rec[cut+1:]
	}
	return rec
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
		return gitCmd(root, "diff", "--no-color", "--find-renames", mergeBaseFrom(root, from, to, query), to, "--", file)
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
	Date     string `json:"date,omitempty"`
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

// handleCreateWorktree adds a worktree for a branch, creating the branch when it
// does not exist yet.
//
// The path is not the caller's to choose. One convention, applied here, is what
// lets a session started from a phone land somewhere the desktop can predict —
// and it is a sibling of the main worktree rather than a child, because a child
// would show up in git status and die to git clean -fdx.
func (s *PublicServer) handleCreateWorktree(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Repo   string `json:"repo"`
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	created, err := createWorktree(req.Repo, req.Branch)
	if err != nil {
		jsonError(w, err.Error(), StatusOf(err))
		return
	}

	jsonResponse(w, http.StatusCreated, map[string]interface{}{"worktree": created})
}

// createWorktree is the handler's body, minus the HTTP. The fork endpoint calls
// it directly: going back out through the API to reach the daemon's own routine
// would buy nothing but a second place for the convention to drift.
func createWorktree(repo, branch string) (*worktreeEntry, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil, statusError(http.StatusBadRequest, "branch name is required")
	}

	root, err := gitRepoRoot(repo)
	if err != nil {
		return nil, statusError(http.StatusBadRequest, "%s", err.Error())
	}
	if _, err := gitCmd(root, "check-ref-format", "--branch", branch); err != nil {
		return nil, statusError(http.StatusBadRequest, "invalid branch name: %s", branch)
	}

	out, err := gitCmd(root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, statusError(http.StatusInternalServerError, "failed to list worktrees")
	}
	existing := parseWorktreeList(out, root)
	for _, wt := range existing {
		if wt.Branch == branch {
			return nil, statusError(http.StatusConflict, "branch already checked out in %s", wt.Path)
		}
	}

	main := findMainWorktree(root)
	path := filepath.Join(
		filepath.Dir(main),
		filepath.Base(main)+"-worktrees",
		sanitizeBranch(branch),
	)

	// An existing branch is checked out as it is; a new one is cut from HEAD.
	// Passing -b for a branch that already exists is a hard failure, so this is
	// a question that has to be asked before the fact.
	args := []string{"worktree", "add", path, branch}
	if _, err := gitCmd(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		args = []string{"worktree", "add", "-b", branch, path}
	}
	if _, err := gitCmd(root, args...); err != nil {
		return nil, statusError(http.StatusInternalServerError, "%s", err.Error())
	}

	entry := worktreeEntry{Path: path, Branch: branch}
	if head, err := gitCmd(path, "rev-parse", "HEAD"); err == nil {
		entry.Head = shortSHA(strings.TrimSpace(head))
	}
	describeWorktree(&entry)
	return &entry, nil
}

// discardEmptyWorktree removes a worktree made for a fork that then failed to
// start, and the branch cut for it.
//
// Best effort, and deliberately timid. `git worktree remove` refuses a dirty
// tree and the branch delete is `-d` rather than `-D`, so anything actually
// written or committed survives. The caller is already reporting the failure
// that brought us here; a second error about the clean-up would only bury it.
func discardEmptyWorktree(repo, path string) {
	branch, err := gitCmd(path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return
	}
	if _, err := gitCmd(repo, "worktree", "remove", path); err != nil {
		log.Printf("fork: leaving worktree %s behind: %v", path, err)
		return
	}
	if name := strings.TrimSpace(branch); name != "" && name != "HEAD" {
		gitCmd(repo, "branch", "-d", name)
	}
}

// findMainWorktree returns the repository's main worktree, given any worktree
// of it.
//
// This has to be asked of git rather than read off parseWorktreeList, whose
// IsMain is computed against the path the caller happened to be standing in —
// ask it from a linked worktree and that worktree calls itself main. Hanging
// the -worktrees sibling off the answer would then nest each new worktree
// inside the last, which is exactly what forking a fork would do.
//
// --git-common-dir is the main repository's .git for every worktree of it, so
// its parent is the main worktree.
func findMainWorktree(dir string) string {
	out, err := gitCmd(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return dir
	}
	common := strings.TrimSpace(out)
	if common == "" {
		return dir
	}
	return filepath.Dir(common)
}

// deriveBranch invents a branch name from a session's label, and keeps trying
// until it finds one the repository does not already hold.
//
// A fork that demanded a text field before it happened is a fork nobody takes,
// so the common case has to need no typing. The suffix walks rather than using
// a timestamp because -2 is a name a person can read back and find.
func deriveBranch(repo, label string) string {
	base := sanitizeBranch(strings.ToLower(label))
	if len(base) > 40 {
		base = strings.Trim(base[:40], "-.")
	}
	// sanitizeBranch answers "worktree" when a name has nothing usable in it.
	// That is the right directory name and the wrong branch name — a session
	// with no title yet is common, and "fork" says what the branch is.
	if base == "" || base == "worktree" {
		base = "fork"
	}

	candidate := base
	for n := 2; n < 100; n++ {
		if _, err := gitCmd(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+candidate); err != nil {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
	return candidate
}

var branchSeparators = regexp.MustCompile(`[^A-Za-z0-9._-]+|-{2,}`)

// sanitizeBranch renders a branch name as one flat directory name, so
// feat/auth/v2 becomes feat-auth-v2 instead of three nested folders.
func sanitizeBranch(branch string) string {
	flat := branchSeparators.ReplaceAllString(branch, "-")
	flat = strings.Trim(flat, "-.")
	if flat == "" {
		return "worktree"
	}
	return flat
}

// describeWorktree fills in branch state. A worktree whose directory has been
// deleted but not pruned answers nothing, and is left as the porcelain had it.
func describeWorktree(entry *worktreeEntry) {
	if out, err := gitCmd(entry.Path, "log", "-1", "--format=%cI%n%s"); err == nil {
		date, subject, _ := strings.Cut(strings.TrimSpace(out), "\n")
		entry.Date = strings.TrimSpace(date)
		entry.Subject = strings.TrimSpace(subject)
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
