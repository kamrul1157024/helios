package transcript

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// A transcript is read once and kept. Re-reading one is what this package
// exists to avoid: the file grows by a few KB per hook while a whole parse of a
// large one costs ~200ms and ~75MB, and the answer is nearly always the same
// messages as last time.
//
// Keeping them costs little. The parsed form is about a tenth of the file, some
// 680 bytes a message, because the large things in a transcript — tool result
// bodies, attachments, file snapshots — are exactly what the parser discards.
// Every transcript one machine has ever written fits in a few tens of MB.
const (
	// maxEntries bounds the store by session count rather than bytes: a
	// dropped entry costs one reparse, and a parse of the largest transcript
	// seen in the wild is ~200ms.
	//
	// Set well past the number of sessions anyone watches at once, because the
	// cost of being wrong is asymmetric: holding a transcript nobody reads
	// costs a megabyte, while evicting one that is being read costs that parse
	// on the next request. Every transcript on a heavily used machine — 146 of
	// them — came to 12MB.
	maxEntries = 256
	// idleTTL drops sessions nobody is watching. A transcript nobody has asked
	// about for an hour is unlikely to be asked about at all.
	idleTTL = time.Hour
	// tailWindow is how much of the already-read tail is re-checked to confirm
	// the file is the same one, extended, rather than a different file that
	// happens to be the same length or longer.
	tailWindow = 4096
)

// Store holds parsed transcripts and brings them up to date by reading only
// what was appended since the last read.
type Store struct {
	mu      sync.Mutex
	entries map[string]*entry
}

// entry is one transcript's parsed state.
type entry struct {
	mu sync.Mutex

	messages []Message
	// parsedBytes is the offset just past the last complete line consumed.
	parsedBytes int64
	// tailHash covers the bytes just before parsedBytes, and is what tells an
	// append apart from a rewrite that left the file the same size or longer.
	tailHash uint64
	// info identifies the file the messages were read from, so a new file at
	// the same path is not mistaken for its predecessor.
	info os.FileInfo
	// epoch changes whenever the messages stop being an extension of what was
	// served before, which is a caller's signal to discard what it holds.
	epoch    string
	lastUsed time.Time
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{entries: make(map[string]*entry)}
}

// shared is the store the daemon serves from. A transcript cache has no
// configuration and no lifecycle — it is a memo of files on disk — so the
// readers scattered across the daemon share one rather than being handed one.
var shared = NewStore()

// Page returns a window of a transcript, counted from the end: offset=0 is the
// newest `limit` messages.
func Page(parse LineParser, path string, limit, offset int) (*TranscriptResult, error) {
	return shared.Page(parse, path, limit, offset)
}

// Delta returns the messages appended after seq, or a fresh newest page if the
// epoch no longer holds.
func Delta(parse LineParser, path, epoch string, afterSeq, limit int) (*TranscriptResult, error) {
	return shared.Delta(parse, path, epoch, afterSeq, limit)
}

// Page returns a window of a transcript, counted from the end: offset=0 is the
// newest `limit` messages.
func (s *Store) Page(parse LineParser, path string, limit, offset int) (*TranscriptResult, error) {
	e, err := s.current(parse, path)
	if err != nil {
		return nil, err
	}
	defer e.mu.Unlock()

	result := page(e.messages, limit, offset)
	result.Epoch = e.epoch
	return result, nil
}

// Delta returns the messages appended after seq.
//
// A stale epoch means the transcript is no longer the one those seq numbers
// were counted against — it was forked, replaced, or truncated. Answering with
// a delta then would append messages to a conversation the caller no longer
// has, so the answer is a newest page and a flag saying to start over.
func (s *Store) Delta(parse LineParser, path, epoch string, afterSeq, limit int) (*TranscriptResult, error) {
	e, err := s.current(parse, path)
	if err != nil {
		return nil, err
	}
	defer e.mu.Unlock()

	if epoch != e.epoch {
		result := page(e.messages, limit, 0)
		result.Epoch = e.epoch
		result.EpochChanged = true
		return result, nil
	}

	// Searched for, not indexed by: a sequence number is only its own index
	// while a format numbers its messages densely from zero. Codex numbers a
	// message by the record's ordinal in the rollout, which counts records it
	// does not render, so afterSeq+1 there is far past the end of the slice —
	// and every delta came back empty, leaving a live session frozen on the
	// page it was opened with.
	start := sort.Search(len(e.messages), func(i int) bool {
		return e.messages[i].Seq > afterSeq
	})
	fresh := e.messages[start:]
	if limit > 0 && len(fresh) > limit {
		fresh = fresh[len(fresh)-limit:]
	}
	if fresh == nil {
		fresh = []Message{}
	}

	return &TranscriptResult{
		Messages: fresh,
		Total:    len(e.messages),
		Returned: len(fresh),
		HasMore:  start > 0,
		Epoch:    e.epoch,
	}, nil
}

// current returns the entry for path, up to date, with its mutex held. The
// caller unlocks.
func (s *Store) current(parse LineParser, path string) (*entry, error) {
	s.mu.Lock()
	e := s.entries[path]
	if e == nil {
		e = &entry{}
		s.entries[path] = e
	}
	e.lastUsed = time.Now()
	s.evictLocked()
	s.mu.Unlock()

	e.mu.Lock()
	if err := e.refresh(parse, path); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	return e, nil
}

// evictLocked keeps the store bounded. Callers hold s.mu.
func (s *Store) evictLocked() {
	cutoff := time.Now().Add(-idleTTL)
	for path, e := range s.entries {
		if e.lastUsed.Before(cutoff) {
			delete(s.entries, path)
		}
	}
	for len(s.entries) > maxEntries {
		var oldest string
		var oldestAt time.Time
		for path, e := range s.entries {
			if oldest == "" || e.lastUsed.Before(oldestAt) {
				oldest, oldestAt = path, e.lastUsed
			}
		}
		delete(s.entries, oldest)
	}
}

// refresh brings the entry level with the file on disk. Callers hold e.mu.
//
// Reading is the only thing that keeps the entry current, which is what makes
// this safe: an entry that only ever advances when it is read cannot be stale
// at the moment it is served. A stat is ~2µs, so checking costs nothing.
func (e *entry) refresh(parse LineParser, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat transcript: %w", err)
	}
	// Same file, same length, same moment of writing: nothing has happened
	// since the last read. This is the common case while a session sits idle,
	// and it costs one stat.
	//
	// The modification time carries the check that length alone would miss —
	// a file rewritten to the same length is a different transcript.
	if e.info != nil && os.SameFile(e.info, info) &&
		info.Size() == e.parsedBytes && info.ModTime().Equal(e.info.ModTime()) {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	if e.extends(f, info) {
		return e.appendFrom(parse, f, info)
	}
	return e.rebuild(parse, f, info)
}

// extends reports whether the file is the one already parsed, with more written
// on the end.
//
// Size alone would not answer it: a file replaced by a longer one also grows.
// Claude Code only ever appends — a rewind adds a branch rather than cutting
// one — so a failure here means something else rewrote the file, and the safe
// answer is to parse it again.
func (e *entry) extends(f *os.File, info os.FileInfo) bool {
	if e.info == nil || !os.SameFile(e.info, info) || info.Size() < e.parsedBytes {
		return false
	}
	sum, err := hashTail(f, e.parsedBytes)
	return err == nil && sum == e.tailHash
}

// rebuild parses the whole file. Callers hold e.mu.
func (e *entry) rebuild(parse LineParser, f *os.File, info os.FileInfo) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek transcript: %w", err)
	}

	msgs, consumed, err := parseSegment(f, maxLineBytes, 0, parse)
	if err != nil {
		return err
	}

	e.messages = msgs
	e.parsedBytes = consumed
	e.info = info
	e.epoch = uuid.NewString()
	return e.rehashTail(f)
}

// appendFrom parses the bytes written since the last read. Callers hold e.mu.
func (e *entry) appendFrom(parse LineParser, f *os.File, info os.FileInfo) error {
	if _, err := f.Seek(e.parsedBytes, io.SeekStart); err != nil {
		return fmt.Errorf("seek transcript: %w", err)
	}

	msgs, consumed, err := parseSegment(f, maxLineBytes, len(e.messages), parse)
	if err != nil {
		return err
	}

	e.messages = append(e.messages, msgs...)
	e.parsedBytes += consumed
	e.info = info
	return e.rehashTail(f)
}

func (e *entry) rehashTail(f *os.File) error {
	sum, err := hashTail(f, e.parsedBytes)
	if err != nil {
		return err
	}
	e.tailHash = sum
	return nil
}

// hashTail hashes the window of bytes ending at end.
func hashTail(f *os.File, end int64) (uint64, error) {
	if end == 0 {
		return 0, nil
	}
	start := end - tailWindow
	if start < 0 {
		start = 0
	}
	buf := make([]byte, end-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return 0, fmt.Errorf("read transcript tail: %w", err)
	}
	h := fnv.New64a()
	h.Write(buf)
	return h.Sum64(), nil
}
