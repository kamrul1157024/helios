package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo builds a small repository to read history from: three commits, a
// rename, and an untracked file left in the working tree.
func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Tester", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Tester", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}

	run("init", "--initial-branch=main")
	write(t, filepath.Join(root, "one.txt"), "alpha\n")
	run("add", ".")
	run("commit", "-m", "first: add one")

	write(t, filepath.Join(root, "two.txt"), "beta\ngamma\n")
	run("add", ".")
	run("commit", "-m", "second: add two")

	run("mv", "one.txt", "renamed.txt")
	write(t, filepath.Join(root, "two.txt"), "beta\ngamma\ndelta\n")
	run("add", ".")
	run("commit", "-m", "third: rename and extend")

	// A branch to serve as a base, pointing at the first commit.
	run("branch", "base-branch", "HEAD~2")
	write(t, filepath.Join(root, "loose.txt"), "untracked\n")
	return root
}

func gitSHAs(t *testing.T, root string) []string {
	t.Helper()
	out, err := gitCmd(root, "log", "--format=%H")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	return strings.Fields(out)
}

func TestGitLogListsBranchCommits(t *testing.T) {
	root := gitRepo(t)
	body := gitJSON(t, gitServer(t), "/api/git/log?path="+root+"&base=base-branch")

	if body["scope"] != "branch" {
		t.Fatalf("scope = %v, want branch", body["scope"])
	}
	commits, _ := body["commits"].([]interface{})
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want the 2 since base-branch", len(commits))
	}
	first, _ := commits[0].(map[string]interface{})
	if subject, _ := first["subject"].(string); !strings.HasPrefix(subject, "third:") {
		t.Fatalf("newest commit = %q, want the third one", subject)
	}
	if files, _ := first["files"].(float64); files == 0 {
		t.Fatal("shortstat was not parsed: files = 0")
	}
}

func TestGitLogFallsBackToFullHistory(t *testing.T) {
	root := gitRepo(t)
	// HEAD is its own base, so the branch range is empty.
	body := gitJSON(t, gitServer(t), "/api/git/log?path="+root+"&base=main")

	if body["scope"] != "all" {
		t.Fatalf("scope = %v, want all after an empty branch range", body["scope"])
	}
	if commits, _ := body["commits"].([]interface{}); len(commits) != 3 {
		t.Fatalf("got %d commits, want all 3", len(commits))
	}
}

func TestGitLogPages(t *testing.T) {
	root := gitRepo(t)
	server := gitServer(t)

	first := gitJSON(t, server, "/api/git/log?path="+root+"&all=true&limit=2")
	if has, _ := first["has_more"].(bool); !has {
		t.Fatal("has_more = false on the first of two pages")
	}
	if commits, _ := first["commits"].([]interface{}); len(commits) != 2 {
		t.Fatalf("page one has %d commits, want 2", len(commits))
	}

	second := gitJSON(t, server, "/api/git/log?path="+root+"&all=true&limit=2&skip=2")
	if has, _ := second["has_more"].(bool); has {
		t.Fatal("has_more = true on the last page")
	}
	if commits, _ := second["commits"].([]interface{}); len(commits) != 1 {
		t.Fatalf("page two has %d commits, want 1", len(commits))
	}
}

func TestGitChangesForOneCommit(t *testing.T) {
	root := gitRepo(t)
	head := gitSHAs(t, root)[0]
	body := gitJSON(t, gitServer(t), "/api/git/changes?path="+root+"&to="+head)

	if single, _ := body["single"].(bool); !single {
		t.Fatal("single = false for a lone revision")
	}
	if subject, _ := body["subject"].(string); !strings.HasPrefix(subject, "third:") {
		t.Fatalf("subject = %q", subject)
	}

	files, _ := body["files"].([]interface{})
	byPath := map[string]map[string]interface{}{}
	for _, entry := range files {
		file, _ := entry.(map[string]interface{})
		path, _ := file["path"].(string)
		byPath[path] = file
	}
	renamed, ok := byPath["renamed.txt"]
	if !ok {
		t.Fatalf("renamed.txt missing from %v", byPath)
	}
	if renamed["status"] != "R" || renamed["from"] != "one.txt" {
		t.Fatalf("rename reported as %v", renamed)
	}
	if edited, ok := byPath["two.txt"]; !ok || edited["insertions"].(float64) != 1 {
		t.Fatalf("two.txt counts wrong: %v", edited)
	}
}

func TestGitChangesBetweenTwoCommits(t *testing.T) {
	root := gitRepo(t)
	shas := gitSHAs(t, root)
	body := gitJSON(t, gitServer(t), "/api/git/changes?path="+root+"&from="+shas[2]+"&to="+shas[0])

	if single, _ := body["single"].(bool); single {
		t.Fatal("single = true for a range")
	}
	if files, _ := body["files"].([]interface{}); len(files) != 2 {
		t.Fatalf("got %d files across the range, want 2", len(files))
	}
	if insertions, _ := body["insertions"].(float64); insertions != 3 {
		t.Fatalf("insertions = %v, want 3", insertions)
	}
}

func TestGitChangesRejectsFlagLikeRevision(t *testing.T) {
	root := gitRepo(t)
	res := gitGet(t, gitServer(t), "/api/git/changes?path="+root+"&to=--output=/tmp/pwned")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a revision that starts with a dash", res.Code)
	}
}

func TestGitDiffAtRevision(t *testing.T) {
	root := gitRepo(t)
	head := gitSHAs(t, root)[0]
	body := gitJSON(t, gitServer(t), "/api/git/diff?path="+root+"&file=two.txt&to="+head)

	diff, _ := body["diff"].(string)
	if !strings.Contains(diff, "+delta") {
		t.Fatalf("diff does not show the change: %q", diff)
	}
	if body["stat"] != "+1 -0" {
		t.Fatalf("stat = %v, want +1 -0", body["stat"])
	}
}

func TestGitDiffShowsUntrackedFile(t *testing.T) {
	root := gitRepo(t)
	body := gitJSON(t, gitServer(t), "/api/git/diff?path="+root+"&file=loose.txt&untracked=true")

	if diff, _ := body["diff"].(string); !strings.Contains(diff, "+untracked") {
		t.Fatalf("untracked file diffed to %q, want its contents", diff)
	}
}

func TestGitWorktreesReportBranchState(t *testing.T) {
	root := gitRepo(t)
	body := gitJSON(t, gitServer(t), "/api/git/worktrees?path="+root)

	worktrees, _ := body["worktrees"].([]interface{})
	if len(worktrees) != 1 {
		t.Fatalf("got %d worktrees, want 1", len(worktrees))
	}
	main, _ := worktrees[0].(map[string]interface{})
	if main["branch"] != "main" || main["is_main"] != true {
		t.Fatalf("main worktree = %v", main)
	}
	if subject, _ := main["subject"].(string); !strings.HasPrefix(subject, "third:") {
		t.Fatalf("subject = %q, want the head commit", subject)
	}
	// One untracked file is left in the tree by gitRepo.
	if dirty, _ := main["dirty"].(float64); dirty != 1 {
		t.Fatalf("dirty = %v, want 1", dirty)
	}
}

func TestResolveBasePrefersUpstream(t *testing.T) {
	root := gitRepo(t)
	// No upstream and no origin/HEAD: main is the last rung of the chain, and is
	// skipped when it is the branch you are on.
	if base := resolveBase(root); base != "" {
		t.Fatalf("base = %q, want empty when only main exists and HEAD is main", base)
	}

	if out, err := gitCmd(root, "checkout", "-q", "base-branch"); err != nil {
		t.Fatalf("checkout: %v %s", err, out)
	}
	if base := resolveBase(root); base != "main" {
		t.Fatalf("base = %q, want main", base)
	}
}

func TestParseShortStatReadsEachCount(t *testing.T) {
	files, insertions, deletions := parseShortStat(" 3 files changed, 10 insertions(+), 2 deletions(-)")
	if files != 3 || insertions != 10 || deletions != 2 {
		t.Fatalf("got %d/%d/%d, want 3/10/2", files, insertions, deletions)
	}
	// A commit that only adds has no deletion clause.
	if _, insertions, deletions = parseShortStat(" 1 file changed, 4 insertions(+)"); insertions != 4 || deletions != 0 {
		t.Fatalf("got %d insertions and %d deletions, want 4 and 0", insertions, deletions)
	}
}

func TestStatFromDiffIgnoresHeaders(t *testing.T) {
	diff := "--- a/x\n+++ b/x\n@@ -1 +1,2 @@\n context\n+added\n-removed\n"
	if got := statFromDiff(diff); got != "+1 -1" {
		t.Fatalf("stat = %q, want +1 -1", got)
	}
	if got := statFromDiff(""); got != "" {
		t.Fatalf("stat = %q, want empty for an empty diff", got)
	}
}

// gitServer wires just the git routes, the way the daemon does.
func gitServer(t *testing.T) http.Handler {
	t.Helper()
	server := &PublicServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/git/status", server.handleGitStatus)
	mux.HandleFunc("GET /api/git/diff", server.handleGitDiff)
	mux.HandleFunc("GET /api/git/log", server.handleGitLog)
	mux.HandleFunc("GET /api/git/changes", server.handleGitChanges)
	mux.HandleFunc("GET /api/git/worktrees", server.handleGitWorktrees)
	return mux
}

func gitGet(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, target, nil))
	return res
}

func gitJSON(t *testing.T, handler http.Handler, target string) map[string]interface{} {
	t.Helper()
	res := gitGet(t, handler, target)
	if res.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", target, res.Code, res.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", target, err)
	}
	return body
}
