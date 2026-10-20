package server

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"io/fs"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// scanResult is one full scan of a root.
type scanResult struct {
	// entries are sorted by rel.
	entries []indexEntry
	// dirs is every directory the watcher should watch, root-relative, with
	// "" for the root. For a git root that is each directory holding a path
	// git lists; for any other root it is each directory the walk entered.
	dirs    map[string]bool
	git     bool
	partial bool
}

// partialSink receives the list as a cold scan grows it. The slice shares its
// array with the scan, which only ever appends past what it has handed out.
type partialSink func([]indexEntry)

// scanRoot lists a root, asking git first so that .gitignore is honoured, and
// walking when the root is not a repository.
func scanRoot(ctx context.Context, root string, publish partialSink) scanResult {
	if res, ok := scanGit(ctx, root, publish); ok {
		return res
	}
	return scanWalk(ctx, root, publish)
}

// collector accumulates entries and hands out partial lists on a clock.
type collector struct {
	entries []indexEntry
	publish partialSink
	last    time.Time
}

func (c *collector) add(rel string) {
	c.entries = append(c.entries, newIndexEntry(rel))
	if c.publish != nil && time.Since(c.last) >= publishEvery {
		c.last = time.Now()
		c.publish(c.entries[:len(c.entries):len(c.entries)])
	}
}

// scanGit streams `git ls-files` so a cold query can rank the first part of a
// big repository while git is still listing the rest.
//
// A tracked file deleted from the working tree is still in git's index, and
// so still in --cached. It is taken back out with a second, concurrent
// `ls-files --deleted`: the panel should not offer a file that is not there,
// and a patched list — which reads the disk — must equal a fresh one.
func scanGit(ctx context.Context, root string, publish partialSink) (scanResult, bool) {
	deleted := make(chan map[string]bool, 1)
	go func() { deleted <- gitDeleted(ctx, root) }()

	cmd := exec.CommandContext(ctx, "git", "-C", root,
		"ls-files", "--cached", "--others", "--exclude-standard", "-z")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return scanResult{}, false
	}
	if err := cmd.Start(); err != nil {
		return scanResult{}, false
	}

	col := &collector{publish: publish, last: time.Now()}
	partial := readNulSeparated(out, func(rel string) bool {
		col.add(rel)
		return len(col.entries) < maxCandidates
	})
	if partial {
		// Stop git rather than read the rest of a list nobody will use.
		_ = cmd.Process.Kill()
	}
	if err := cmd.Wait(); err != nil && !partial {
		return scanResult{}, false
	}

	gone := <-deleted
	entries := sortEntries(col.entries)
	if len(gone) > 0 {
		kept := entries[:0]
		for _, e := range entries {
			if !gone[e.rel] {
				kept = append(kept, e)
			}
		}
		entries = kept
	}
	return scanResult{entries: entries, dirs: dirsOf(entries), git: true, partial: partial}, true
}

// readNulSeparated calls take for each name until it returns false, and
// reports whether it stopped early.
func readNulSeparated(src io.Reader, take func(string) bool) bool {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, 0); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for scanner.Scan() {
		if name := scanner.Text(); name != "" && !take(name) {
			return true
		}
	}
	return false
}

func gitDeleted(ctx context.Context, root string) map[string]bool {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--deleted", "-z").Output()
	if err != nil {
		return nil
	}
	gone := map[string]bool{}
	readNulSeparated(bytes.NewReader(out), func(rel string) bool {
		gone[rel] = true
		return true
	})
	return gone
}

// dirsOf is every directory holding an entry, and every directory above
// those, up to the root.
func dirsOf(entries []indexEntry) map[string]bool {
	dirs := map[string]bool{"": true}
	for _, e := range entries {
		for dir := path.Dir(e.rel); dir != "." && !dirs[dir]; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	return dirs
}

// scanWalk lists a root that is not a repository, minus skipDirs.
func scanWalk(ctx context.Context, root string, publish partialSink) scanResult {
	col := &collector{publish: publish, last: time.Now()}
	dirs := map[string]bool{"": true}
	partial := false
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable corner of the tree is not worth failing the search.
			return nil
		}
		if ctx.Err() != nil {
			partial = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			dirs[relTo(root, p)] = true
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		col.add(relTo(root, p))
		if len(col.entries) >= maxCandidates {
			partial = true
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		partial = true
	}
	return scanResult{entries: sortEntries(col.entries), dirs: dirs, partial: partial}
}

// checkIgnored asks git which of rels it ignores, in one process. A directory
// is named with a trailing slash. A failure ignores nothing, which errs on the
// side of showing a path.
func checkIgnored(ctx context.Context, root string, rels []string) map[string]bool {
	if len(rels) == 0 {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(rels, "\x00") + "\x00")
	// Exit 1 means none of them is ignored, which is an answer.
	out, _ := cmd.Output()
	ignored := map[string]bool{}
	readNulSeparated(bytes.NewReader(out), func(rel string) bool {
		ignored[strings.TrimSuffix(rel, "/")] = true
		return true
	})
	return ignored
}
