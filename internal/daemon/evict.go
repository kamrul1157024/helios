package daemon

import (
	"sort"
	"time"

	"github.com/kamrul1157024/helios/internal/store"
)

// minIdleBeforeEvict keeps a session that was just woken from being taken
// straight back. Without it, waking a session while over budget can evict it
// again on the next pass, and the user sees it flap.
const minIdleBeforeEvict = 5 * time.Minute

// evictableStatuses are the states in which nothing is lost by going cold.
//
// Everything absent from this set means something is in flight. "active",
// "starting" and "compacting" mean the agent is mid-turn. "waiting_permission"
// and "waiting_input" mean the *human* is the blocker, and taking the terminal
// would discard the question being asked.
var evictableStatuses = map[string]bool{"idle": true}

// candidate is one warm session considered for eviction.
type candidate struct {
	SessionID string
	RSS       int64
	// Unread is how long since a human last looked. Not how long since the
	// agent last ran: an agent working alone keeps last_event_at fresh while
	// nobody is watching, which is the opposite of what eviction should protect.
	Unread time.Duration
}

// evictionCandidates picks the warm sessions that could go cold.
//
// usage carries only live terminals, so a session missing from it is already
// cold and is not a candidate.
func evictionCandidates(sessions []store.Session, usage map[string]int64, now time.Time) []candidate {
	out := make([]candidate, 0, len(usage))
	for _, sess := range sessions {
		rss, warm := usage[sess.SessionID]
		if !warm || rss <= 0 {
			continue
		}
		if !evictableStatuses[sess.Status] || sess.Archived {
			continue
		}
		// Pinning already means "I am coming back to this".
		if sess.Pinned {
			continue
		}
		unread := unreadFor(sess, now)
		if unread < minIdleBeforeEvict {
			continue
		}
		out = append(out, candidate{SessionID: sess.SessionID, RSS: rss, Unread: unread})
	}
	return out
}

// unreadFor is how long since a human looked at this session.
//
// A session no client has ever shown has no last_interacted_at, and falls back
// to agent activity. That makes it a strong candidate, which is right: nobody
// has opened it.
func unreadFor(sess store.Session, now time.Time) time.Duration {
	for _, stamp := range []*string{sess.LastInteractedAt, sess.LastEventAt} {
		if stamp == nil || *stamp == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, *stamp)
		if err != nil {
			continue
		}
		return now.Sub(at)
	}
	// No timestamps at all. Treat it as long unread rather than brand new, so a
	// session with a broken record is reclaimable instead of pinned forever.
	return now.Sub(time.Time{})
}

// chooseEvictions returns the sessions to take, in order, until the pool is
// under budget.
//
// The score multiplies size by how long the session has gone unread, so the
// best victim is both expensive to hold and unlikely to be wanted. Plain
// recency would take a small session nobody has opened in a week and leave a
// huge one; plain size would take the biggest even if it was being read a
// minute ago.
func chooseEvictions(candidates []candidate, warmTotal, budget int64) []candidate {
	if budget <= 0 || warmTotal <= budget {
		return nil
	}

	ranked := make([]candidate, len(candidates))
	copy(ranked, candidates)
	sort.SliceStable(ranked, func(i, j int) bool {
		return score(ranked[i]) > score(ranked[j])
	})

	var taken []candidate
	freed := int64(0)
	for _, c := range ranked {
		if warmTotal-freed <= budget {
			break
		}
		taken = append(taken, c)
		freed += c.RSS
	}
	return taken
}

// score is bytes multiplied by minutes unread. Higher is evicted first.
//
// Note the multiplication. Dividing by the idle time — which an earlier draft
// of this did, and the spec described — ranks a large session read a minute ago
// above one nobody has opened all day, which is exactly backwards.
func score(c candidate) float64 {
	minutes := c.Unread.Minutes()
	if minutes < 1 {
		minutes = 1
	}
	return float64(c.RSS) * minutes
}

// budgetBytes resolves the configured share of physical memory.
//
// A fraction rather than a byte count: the same install runs on a 16 GB laptop
// and a 64 GB desktop. A fraction at or below zero means no limit, which is how
// eviction is turned off without a second flag being consulted here.
func budgetBytes(fraction float64, memoryTotal uint64) int64 {
	if fraction <= 0 || memoryTotal == 0 {
		return 0
	}
	return int64(float64(memoryTotal) * fraction)
}
