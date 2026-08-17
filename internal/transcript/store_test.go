package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// entryLine is one assistant message as Claude writes it.
func entryLine(text string) string {
	return fmt.Sprintf(
		`{"type":"assistant","timestamp":"2026-08-12T10:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`,
		text,
	) + "\n"
}

// writeTranscript creates a transcript holding the named messages.
func writeTranscript(t *testing.T, texts ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	var b strings.Builder
	for _, text := range texts {
		b.WriteString(entryLine(text))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func appendTo(t *testing.T, path string, texts ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	defer f.Close()
	for _, text := range texts {
		if _, err := f.WriteString(entryLine(text)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
}

func contents(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func TestStoreMatchesFullParse(t *testing.T) {
	path := writeTranscript(t, "one", "two", "three")

	got, err := NewStore().Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	want, err := ParseClaudeTranscript(path, 10, 0)
	if err != nil {
		t.Fatalf("ParseClaudeTranscript: %v", err)
	}

	if fmt.Sprint(contents(got.Messages)) != fmt.Sprint(contents(want.Messages)) {
		t.Fatalf("store = %v, full parse = %v", contents(got.Messages), contents(want.Messages))
	}
	if got.Total != want.Total {
		t.Fatalf("Total = %d, want %d", got.Total, want.Total)
	}
	if got.Epoch == "" {
		t.Fatal("no epoch on a page")
	}
}

func TestStoreServesAppendedMessages(t *testing.T) {
	path := writeTranscript(t, "one", "two")
	s := NewStore()

	first, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}

	appendTo(t, path, "three")

	second, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page after append: %v", err)
	}
	if got := contents(second.Messages); fmt.Sprint(got) != "[one two three]" {
		t.Fatalf("messages = %v, want [one two three]", got)
	}
	// An append extends what was already parsed, so the caller's seq numbers
	// stay good and it can keep what it holds.
	if second.Epoch != first.Epoch {
		t.Fatal("epoch changed on a plain append")
	}
}

func TestStoreLeavesPartialLineForNextRead(t *testing.T) {
	path := writeTranscript(t, "one")
	s := NewStore()

	if _, err := s.Page(path, 10, 0); err != nil {
		t.Fatalf("Page: %v", err)
	}

	// Half an entry, as seen when a read races the agent writing.
	half := entryLine("two")
	half = half[:len(half)/2]
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString(half); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	partial, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page with a partial line: %v", err)
	}
	if got := contents(partial.Messages); fmt.Sprint(got) != "[one]" {
		t.Fatalf("messages = %v, want [one]: a half-written line must not be served", got)
	}

	// Finish the line the way the writer would.
	rest := entryLine("two")[len(half):]
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("reopen for append: %v", err)
	}
	if _, err := f.WriteString(rest); err != nil {
		t.Fatalf("append rest: %v", err)
	}
	f.Close()

	complete, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page after completing the line: %v", err)
	}
	if got := contents(complete.Messages); fmt.Sprint(got) != "[one two]" {
		t.Fatalf("messages = %v, want [one two]: the completed line must appear exactly once", got)
	}
}

func TestStoreRebuildsOnTruncation(t *testing.T) {
	path := writeTranscript(t, "one", "two", "three")
	s := NewStore()

	first, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}

	if err := os.WriteFile(path, []byte(entryLine("only")), 0o600); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	second, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page after truncation: %v", err)
	}
	if got := contents(second.Messages); fmt.Sprint(got) != "[only]" {
		t.Fatalf("messages = %v, want [only]", got)
	}
	if second.Epoch == first.Epoch {
		t.Fatal("epoch survived a truncation; callers would keep stale messages")
	}
}

func TestStoreRebuildsWhenRewrittenToTheSameLength(t *testing.T) {
	path := writeTranscript(t, "one", "two")
	s := NewStore()

	first, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}

	// Same byte count, different content: size alone cannot catch this.
	if err := os.WriteFile(path, []byte(entryLine("one")+entryLine("XXX")), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	second, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page after rewrite: %v", err)
	}
	if got := contents(second.Messages); fmt.Sprint(got) != "[one XXX]" {
		t.Fatalf("messages = %v, want [one XXX]", got)
	}
	if second.Epoch == first.Epoch {
		t.Fatal("epoch survived a same-length rewrite")
	}
}

func TestStoreRebuildsWhenTheFileIsReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte(entryLine("one")+entryLine("two")), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := NewStore()

	first, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}

	// A fork lands a different file at the path, longer than the original —
	// growth that is not an append.
	replacement := filepath.Join(dir, "fork.jsonl")
	if err := os.WriteFile(replacement, []byte(entryLine("one")+entryLine("two")+entryLine("forked")), 0o600); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("replace: %v", err)
	}

	second, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page after replacement: %v", err)
	}
	if got := contents(second.Messages); fmt.Sprint(got) != "[one two forked]" {
		t.Fatalf("messages = %v, want [one two forked]", got)
	}
	if second.Epoch == first.Epoch {
		t.Fatal("epoch survived the file being replaced")
	}
}

func TestStoreDeltaReturnsOnlyWhatIsNew(t *testing.T) {
	path := writeTranscript(t, "one", "two")
	s := NewStore()

	first, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	newest := first.Messages[len(first.Messages)-1].Seq

	appendTo(t, path, "three", "four")

	delta, err := s.Delta(path, first.Epoch, newest, 0)
	if err != nil {
		t.Fatalf("Delta: %v", err)
	}
	if got := contents(delta.Messages); fmt.Sprint(got) != "[three four]" {
		t.Fatalf("delta = %v, want [three four]", got)
	}
	if delta.EpochChanged {
		t.Fatal("delta reported an epoch change after a plain append")
	}
	if delta.Total != 4 {
		t.Fatalf("Total = %d, want 4", delta.Total)
	}
}

func TestStoreDeltaOnAStaleEpochResets(t *testing.T) {
	path := writeTranscript(t, "one", "two")
	s := NewStore()

	first, err := s.Page(path, 10, 0)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}

	if err := os.WriteFile(path, []byte(entryLine("fresh")), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	delta, err := s.Delta(path, first.Epoch, 1, 10)
	if err != nil {
		t.Fatalf("Delta: %v", err)
	}
	if !delta.EpochChanged {
		t.Fatal("stale epoch not reported; the caller would append to a transcript it no longer has")
	}
	if got := contents(delta.Messages); fmt.Sprint(got) != "[fresh]" {
		t.Fatalf("messages = %v, want the newest page [fresh]", got)
	}
}

func TestStoreSeqSurvivesPaging(t *testing.T) {
	path := writeTranscript(t, "one", "two", "three", "four")
	s := NewStore()

	newest, err := s.Page(path, 2, 0)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	older, err := s.Page(path, 2, 2)
	if err != nil {
		t.Fatalf("Page(offset=2): %v", err)
	}

	if got := contents(newest.Messages); fmt.Sprint(got) != "[three four]" {
		t.Fatalf("newest page = %v", got)
	}
	if got := contents(older.Messages); fmt.Sprint(got) != "[one two]" {
		t.Fatalf("older page = %v", got)
	}
	// seq numbers the whole transcript, not the page, or a client could not
	// stitch pages together or ask for a delta after the newest one it holds.
	if older.Messages[0].Seq != 0 || newest.Messages[1].Seq != 3 {
		t.Fatalf("seqs = %d…%d, want 0…3", older.Messages[0].Seq, newest.Messages[1].Seq)
	}
	if !newest.HasMore {
		t.Fatal("HasMore false with two older messages on the file")
	}
}

func TestStoreConcurrentReadersSeeOneTranscript(t *testing.T) {
	path := writeTranscript(t, "one", "two")
	s := NewStore()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := s.Page(path, 10, 0); err != nil {
					t.Errorf("Page: %v", err)
					return
				}
				if i == 0 {
					appendTo(t, path, fmt.Sprintf("more-%d", j))
				}
			}
		}(i)
	}
	wg.Wait()

	final, err := s.Page(path, 100, 0)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if final.Total != 22 {
		t.Fatalf("Total = %d, want 22", final.Total)
	}
	for i, m := range final.Messages {
		if m.Seq != i {
			t.Fatalf("message %d has seq %d: seqs must stay dense and ordered", i, m.Seq)
		}
	}
}

func TestStoreMissingFile(t *testing.T) {
	if _, err := NewStore().Page(filepath.Join(t.TempDir(), "absent.jsonl"), 10, 0); err == nil {
		t.Fatal("no error for a transcript that is not there")
	}
}

func BenchmarkStoreWarmPage(b *testing.B) {
	path := filepath.Join(b.TempDir(), "transcript.jsonl")
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		sb.WriteString(entryLine(fmt.Sprintf("message %d with a little padding to look real", i)))
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
		b.Fatalf("write: %v", err)
	}

	s := NewStore()
	if _, err := s.Page(path, 50, 0); err != nil {
		b.Fatalf("warm: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Page(path, 50, 0); err != nil {
			b.Fatalf("Page: %v", err)
		}
	}
}
