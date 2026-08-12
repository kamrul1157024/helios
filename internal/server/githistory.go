package server

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const (
	defaultLogLimit = 50
	maxLogLimit     = 200
	// maxChangedFiles bounds the file list for a commit or a range. A tree-wide
	// refactor can touch thousands of files, and no one reads that list.
	maxChangedFiles = 2000
)

// git will put these separators in a format string verbatim, and no commit
// subject or author name contains them.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

const logFormat = recordSep + "%H" + fieldSep + "%h" + fieldSep + "%an" + fieldSep + "%aI" + fieldSep + "%s"

// revPattern is deliberately narrow: these strings become git arguments, and a
// value beginning with "-" would be read as a flag rather than a revision.
var revPattern = regexp.MustCompile(`^[A-Za-z0-9._/^~@{}-]{1,255}$`)

func validRevision(rev string) bool {
	return !strings.HasPrefix(rev, "-") && revPattern.MatchString(rev)
}

// mergeBaseFrom answers "what has this branch changed", where the plain
// two-dot form answers "how do these two revisions differ".
//
// The two part company as soon as the base branch moves: commits landed on
// main after the branch was cut appear in a two-dot diff as changes the branch
// undid — somebody else's work, rendered backwards, in the middle of a review.
// Callers opt in with merge_base=true.
//
// Histories with no common ancestor keep the revision they were given: there
// is no better answer for unrelated trees, and failing would take the diff
// away entirely.
func mergeBaseFrom(root, from, to string, query map[string][]string) string {
	if from == "" || len(query["merge_base"]) == 0 || query["merge_base"][0] != "true" {
		return from
	}
	out, err := gitCmd(root, "merge-base", from, to)
	if err != nil {
		return from
	}
	if base := strings.TrimSpace(out); base != "" {
		return base
	}
	return from
}

type logEntry struct {
	SHA        string `json:"sha"`
	Short      string `json:"short"`
	Author     string `json:"author"`
	Date       string `json:"date"`
	Subject    string `json:"subject"`
	Files      int    `json:"files"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

type commitFile struct {
	Path string `json:"path"`
	// From is set on a rename or copy, so the UI can show "old → new".
	From       string `json:"from,omitempty"`
	Status     string `json:"status"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

// handleGitLog returns the commits on the current branch, newest first.
func (s *PublicServer) handleGitLog(w http.ResponseWriter, r *http.Request) {
	root, err := gitRepoRoot(r.URL.Query().Get("path"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	base := r.URL.Query().Get("base")
	if base != "" && !validRevision(base) {
		jsonError(w, "invalid base", http.StatusBadRequest)
		return
	}
	if base == "" {
		base = resolveBase(root)
	}

	limit := queryInt(r, "limit", defaultLogLimit, maxLogLimit)
	skip := queryInt(r, "skip", 0, 1<<20)
	scope := "branch"
	if r.URL.Query().Get("all") == "true" || base == "" {
		scope = "all"
	}

	commits, hasMore, err := readLog(root, base, scope, limit, skip)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// On a branch that is level with its base — main, fully pushed — the branch
	// range is empty, and an empty list is the least useful answer to "what
	// happened here". Fall back to the whole history and say so.
	if len(commits) == 0 && scope == "branch" && skip == 0 {
		scope = "all"
		commits, hasMore, err = readLog(root, base, scope, limit, skip)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	branch, _ := gitCmd(root, "rev-parse", "--abbrev-ref", "HEAD")
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"root":     root,
		"branch":   strings.TrimSpace(branch),
		"base":     base,
		"scope":    scope,
		"commits":  commits,
		"has_more": hasMore,
	})
}

// readLog asks for one commit more than the caller wants, which is how it knows
// whether there is another page.
func readLog(root, base, scope string, limit, skip int) ([]logEntry, bool, error) {
	args := []string{"log", "--no-color", "--format=" + logFormat, "--shortstat",
		"--max-count=" + strconv.Itoa(limit+1), "HEAD"}
	if skip > 0 {
		args = append(args, "--skip="+strconv.Itoa(skip))
	}
	// "--not base" rather than "base..HEAD": the revision stays its own argument
	// instead of being concatenated into a range string.
	if scope == "branch" && base != "" {
		args = append(args, "--not", base)
	}
	out, err := gitCmd(root, append(args, "--")...)
	if err != nil {
		return nil, false, err
	}

	commits := parseLog(out)
	hasMore := len(commits) > limit
	if hasMore {
		commits = commits[:limit]
	}
	return commits, hasMore, nil
}

func parseLog(out string) []logEntry {
	// Not nil: a nil slice marshals to JSON null, and clients expect a list.
	commits := []logEntry{}
	for _, record := range strings.Split(out, recordSep) {
		lines := strings.Split(strings.Trim(record, "\n"), "\n")
		fields := strings.Split(lines[0], fieldSep)
		if len(fields) < 5 {
			continue
		}
		entry := logEntry{SHA: fields[0], Short: fields[1], Author: fields[2], Date: fields[3], Subject: fields[4]}
		// A merge commit has no shortstat line, and neither does the record
		// separator's empty leading chunk.
		for _, line := range lines[1:] {
			if strings.Contains(line, "changed") {
				entry.Files, entry.Insertions, entry.Deletions = parseShortStat(line)
			}
		}
		commits = append(commits, entry)
	}
	return commits
}

// parseShortStat reads " 3 files changed, 10 insertions(+), 2 deletions(-)".
func parseShortStat(line string) (files, insertions, deletions int) {
	parts := strings.Fields(line)
	for i, part := range parts {
		if i == 0 {
			continue
		}
		n, err := strconv.Atoi(parts[i-1])
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(part, "file"):
			files = n
		case strings.HasPrefix(part, "insertion"):
			insertions = n
		case strings.HasPrefix(part, "deletion"):
			deletions = n
		}
	}
	return files, insertions, deletions
}

// resolveBase answers "where did this branch start" — the tracking branch, then
// the remote's default branch, then a local main or master.
func resolveBase(root string) string {
	if out, err := gitCmd(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		if base := strings.TrimSpace(out); base != "" {
			return base
		}
	}
	if out, err := gitCmd(root, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		if base := strings.TrimPrefix(strings.TrimSpace(out), "refs/remotes/"); base != "" {
			return base
		}
	}
	current := ""
	if out, err := gitCmd(root, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		current = strings.TrimSpace(out)
	}
	for _, name := range []string{"main", "master"} {
		if name == current {
			continue
		}
		if _, err := gitCmd(root, "rev-parse", "--verify", "--quiet", name+"^{commit}"); err == nil {
			return name
		}
	}
	return ""
}

// handleGitChanges returns the files touched by one commit, or between two.
func (s *PublicServer) handleGitChanges(w http.ResponseWriter, r *http.Request) {
	root, err := gitRepoRoot(r.URL.Query().Get("path"))
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	to := r.URL.Query().Get("to")
	from := r.URL.Query().Get("from")
	if to == "" || !validRevision(to) || (from != "" && !validRevision(from)) {
		jsonError(w, "missing or invalid revision", http.StatusBadRequest)
		return
	}

	files, err := changedFiles(root, mergeBaseFrom(root, from, to, r.URL.Query()), to)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	truncated := len(files) > maxChangedFiles
	if truncated {
		files = files[:maxChangedFiles]
	}

	insertions, deletions := 0, 0
	for _, file := range files {
		insertions += file.Insertions
		deletions += file.Deletions
	}

	body := map[string]interface{}{
		"from": from, "to": to, "single": from == "",
		"files": files, "insertions": insertions, "deletions": deletions,
		"truncated": truncated,
	}
	if from == "" {
		describeCommit(root, to, body)
	}
	jsonResponse(w, http.StatusOK, body)
}

// changedFiles zips `--name-status` (which knows what happened to a file) with
// `--numstat` (which knows how much). Both are asked for with -z so that a path
// containing a space, a quote or a rename arrow still parses exactly.
func changedFiles(root, from, to string) ([]commitFile, error) {
	var nameArgs, numArgs []string
	if from == "" {
		// `show` rather than `diff to^ to`: it is also right for a root commit.
		nameArgs = []string{"show", "--format=", "--name-status", "-z", "--find-renames", to}
		numArgs = []string{"show", "--format=", "--numstat", "-z", "--find-renames", to}
	} else {
		nameArgs = []string{"diff", "--name-status", "-z", "--find-renames", from, to}
		numArgs = []string{"diff", "--numstat", "-z", "--find-renames", from, to}
	}

	nameOut, err := gitCmd(root, append(nameArgs, "--")...)
	if err != nil {
		return nil, err
	}
	numOut, err := gitCmd(root, append(numArgs, "--")...)
	if err != nil {
		return nil, err
	}

	files := parseNameStatusZ(nameOut)
	counts := parseNumstatZ(numOut)
	for i := range files {
		if count, ok := counts[files[i].Path]; ok {
			files[i].Insertions, files[i].Deletions = count[0], count[1]
		}
	}
	return files, nil
}

func describeCommit(root, sha string, body map[string]interface{}) {
	format := "%s" + fieldSep + "%an" + fieldSep + "%aI" + fieldSep + "%P" + fieldSep + "%b"
	out, err := gitCmd(root, "show", "-s", "--format="+format, sha)
	if err != nil {
		return
	}
	fields := strings.SplitN(strings.TrimRight(out, "\n"), fieldSep, 5)
	if len(fields) < 5 {
		return
	}
	body["subject"] = fields[0]
	body["author"] = fields[1]
	body["date"] = fields[2]
	body["parents"] = strings.Fields(fields[3])
	body["body"] = strings.TrimSpace(fields[4])
}

// parseNameStatusZ reads NUL-separated records: a status, then one path, or two
// when the status is a rename or a copy.
func parseNameStatusZ(out string) []commitFile {
	fields := strings.Split(out, "\x00")
	files := []commitFile{}
	for i := 0; i < len(fields); i++ {
		status := strings.TrimSpace(fields[i])
		if status == "" {
			continue
		}
		renamed := strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C")
		if renamed && i+2 < len(fields) {
			files = append(files, commitFile{Status: status[:1], From: fields[i+1], Path: fields[i+2]})
			i += 2
			continue
		}
		if i+1 >= len(fields) {
			break
		}
		files = append(files, commitFile{Status: status[:1], Path: fields[i+1]})
		i++
	}
	return files
}

// parseNumstatZ reads "insertions\tdeletions\tpath" records. On a rename the
// path is empty and the old and new paths follow as two more records.
func parseNumstatZ(out string) map[string][2]int {
	counts := map[string][2]int{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.Split(fields[i], "\t")
		if len(parts) < 3 {
			continue
		}
		path := parts[2]
		if path == "" {
			if i+2 >= len(fields) {
				break
			}
			path = fields[i+2]
			i += 2
		}
		counts[path] = [2]int{countOrZero(parts[0]), countOrZero(parts[1])}
	}
	return counts
}

// countOrZero reads a numstat count, which is "-" for a binary file.
func countOrZero(field string) int {
	n, err := strconv.Atoi(field)
	if err != nil {
		return 0
	}
	return n
}

func queryInt(r *http.Request, name string, fallback, max int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}
