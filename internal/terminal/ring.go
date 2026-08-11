package terminal

import "sync"

// DefaultRingSize is the per-session raw output buffer. Claude Code redraws
// heavily, so this holds seconds of output rather than minutes; viewers that
// fall further behind are resynced from a snapshot instead.
const DefaultRingSize = 1 << 20 // 1 MiB

// Ring is a fixed-capacity byte buffer with monotonic sequence numbers. Every
// byte ever written has a sequence number; only the most recent cap bytes are
// retained. A reader that asks for an evicted sequence is told so explicitly
// rather than being handed a corrupt stream.
type Ring struct {
	mu  sync.RWMutex
	buf []byte
	// start is the sequence number of the oldest retained byte.
	start uint64
	// end is the sequence number one past the newest byte, i.e. total written.
	end uint64
}

// NewRing returns a Ring holding at most cap bytes.
func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		capacity = DefaultRingSize
	}
	return &Ring{buf: make([]byte, 0, capacity)}
}

// Write appends p, evicting the oldest bytes if necessary. It never fails.
func (r *Ring) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	capacity := cap(r.buf)
	// A single write larger than the ring keeps only its tail.
	if len(p) >= capacity {
		tail := p[len(p)-capacity:]
		r.buf = append(r.buf[:0], tail...)
		r.end += uint64(len(p))
		r.start = r.end - uint64(len(tail))
		return len(p), nil
	}

	if len(r.buf)+len(p) > capacity {
		drop := len(r.buf) + len(p) - capacity
		r.buf = append(r.buf[:0], r.buf[drop:]...)
		r.start += uint64(drop)
	}
	r.buf = append(r.buf, p...)
	r.end += uint64(len(p))
	return len(p), nil
}

// Since returns retained bytes from sequence number seq onward, plus the
// sequence one past the last returned byte. ok is false when seq has been
// evicted, meaning the caller must resync from a snapshot.
func (r *Ring) Since(seq uint64) (data []byte, next uint64, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if seq > r.end {
		// Caller is ahead of us; treat as caught up rather than an error.
		return nil, r.end, true
	}
	if seq < r.start {
		return nil, r.end, false
	}
	off := int(seq - r.start)
	out := make([]byte, len(r.buf)-off)
	copy(out, r.buf[off:])
	return out, r.end, true
}

// Seq returns the sequence number one past the newest byte written.
func (r *Ring) Seq() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.end
}

// Start returns the sequence number of the oldest retained byte.
func (r *Ring) Start() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.start
}

// Len returns the number of bytes currently retained.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.buf)
}
