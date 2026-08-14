// Package hitl owns helios's own human-in-the-loop prompts.
//
// A prompt is one decision an agent is blocked on. It is painted as an overlay
// over the session's terminal, so every viewer of that session sees it and any
// of them can answer, and it is answered with the keys the user presses rather
// than by typing into the agent. The same decision is published to the phone as
// a notification; whichever surface answers first wins, and that race is settled
// by the notification manager, not here.
//
// See docs/specs/36-helios-owned-hitl.md.
package hitl

import (
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/kamrul1157024/helios/internal/terminal"
)

// ErrNoTerminal is returned when a session has no terminal to paint on. The
// caller falls back to the phone, which is what every prompt did before this
// package existed.
var ErrNoTerminal = errors.New("hitl: session has no terminal")

// footer is the key hint under every prompt. Short on purpose: it is drawn on
// terminals as narrow as the box allows.
const footer = "↑↓ select · enter confirm · esc cancel"

// Prompt is one question helios is waiting on a human to answer.
type Prompt struct {
	// Title labels the box — the tool name, or who is asking.
	Title string
	// Body is the detail above the choices, one entry per paragraph.
	Body []string
	// Choices are the answers, in the order they are shown.
	Choices []string
}

// Answer is how a terminal user resolved a prompt.
type Answer struct {
	// Index selects Prompt.Choices, or is negative when the user cancelled.
	Index int
}

// Cancelled reports whether the user dismissed the prompt instead of choosing.
func (a Answer) Cancelled() bool { return a.Index < 0 }

// Overlays is the part of the terminal backend a Controller drives.
type Overlays interface {
	SetOverlay(sessionID string, o terminal.Overlay) error
	ClearOverlay(sessionID string) error
}

// Controller holds the prompt each session is waiting on and turns keystrokes
// into answers. One per daemon.
type Controller struct {
	terms Overlays

	mu      sync.Mutex
	pending map[string]*live
}

// live is a prompt currently on screen.
type live struct {
	prompt   Prompt
	onAnswer func(Answer)

	// mu guards selected alone. Input arrives on the mirror's read goroutine
	// while the hook's goroutine may be releasing the prompt, and the highlight
	// is the one field both can touch.
	mu       sync.Mutex
	selected int

	// answered guards the callback: two Enters, or an Enter racing an Escape,
	// must still produce exactly one answer.
	answered sync.Once
}

// NewController returns a controller painting on terms. A nil terms — a backend
// that cannot draw over a session — is allowed: every prompt then reports
// ErrNoTerminal and lives on the phone alone.
func NewController(terms Overlays) *Controller {
	return &Controller{terms: terms, pending: make(map[string]*live)}
}

// Ask paints p over the session's terminal and calls onAnswer at most once,
// when someone answers there. It does not block: the caller is already waiting
// on the notification manager, which is where the phone answers too.
//
// The returned release takes the overlay down and must be called once the
// decision is settled, whichever surface settled it. It is safe to call more
// than once, and safe to call after an error.
func (c *Controller) Ask(sessionID string, p Prompt, onAnswer func(Answer)) (release func(), err error) {
	if c == nil || c.terms == nil {
		return func() {}, ErrNoTerminal
	}
	if len(p.Choices) == 0 {
		return func() {}, errors.New("hitl: prompt has no choices")
	}

	l := &live{prompt: p, onAnswer: onAnswer}

	c.mu.Lock()
	// Last prompt wins. Two blocking hooks for one session at once is not a
	// shape Claude Code produces, but if it ever does, the newer question is the
	// one the user is looking at; the older stays answerable from the phone.
	previous := c.pending[sessionID]
	c.pending[sessionID] = l
	c.mu.Unlock()
	if previous != nil {
		log.Printf("hitl: session %s already had a prompt on screen; replacing it", sessionID)
	}

	release = func() { c.release(sessionID, l) }

	if err := c.terms.SetOverlay(sessionID, l.overlay()); err != nil {
		release()
		return func() {}, fmt.Errorf("show prompt on %s: %w", sessionID, err)
	}
	return release, nil
}

// release forgets a prompt and clears its overlay, if it is still the one on
// screen. A prompt that was already replaced leaves the newer one alone.
func (c *Controller) release(sessionID string, l *live) {
	c.mu.Lock()
	current, ok := c.pending[sessionID]
	if ok && current == l {
		delete(c.pending, sessionID)
	}
	c.mu.Unlock()

	if !ok || current != l {
		return
	}
	if err := c.terms.ClearOverlay(sessionID); err != nil {
		log.Printf("hitl: clear prompt on %s: %v", sessionID, err)
	}
}

// HandleInput feeds keystrokes an overlay captured into that session's pending
// prompt. It is wired to the terminal backend once, at startup.
//
// It runs on the mirror's read goroutine, so it must not block: the answer
// callback goes to its own goroutine because resolving a notification writes to
// the database and fans out over SSE.
func (c *Controller) HandleInput(sessionID string, keys []byte) {
	c.mu.Lock()
	l := c.pending[sessionID]
	c.mu.Unlock()
	if l == nil {
		return
	}

	moved := false
	for _, ev := range decodeKeys(keys) {
		switch ev.kind {
		case keyPrev:
			moved = l.move(-1) || moved
		case keyNext:
			moved = l.move(1) || moved
		case keySelect:
			if ev.n < len(l.prompt.Choices) {
				moved = l.jump(ev.n) || moved
			}
		case keyConfirm:
			l.answer(Answer{Index: l.at()})
			return
		case keyCancel:
			l.answer(Answer{Index: -1})
			return
		}
	}

	if moved {
		if err := c.terms.SetOverlay(sessionID, l.overlay()); err != nil {
			log.Printf("hitl: redraw prompt on %s: %v", sessionID, err)
		}
	}
}

// Pending reports whether a session has a prompt on screen.
func (c *Controller) Pending(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending[sessionID] != nil
}

// move shifts the highlight and reports whether it actually went anywhere.
// Movement stops at the ends rather than wrapping, so holding an arrow key
// cannot roll past "Deny" onto "Allow".
func (l *live) move(delta int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	next := l.selected + delta
	if next < 0 || next >= len(l.prompt.Choices) || next == l.selected {
		return false
	}
	l.selected = next
	return true
}

func (l *live) jump(to int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if to == l.selected {
		return false
	}
	l.selected = to
	return true
}

func (l *live) at() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.selected
}

// answer delivers the user's choice exactly once, off the read goroutine.
func (l *live) answer(a Answer) {
	l.answered.Do(func() {
		if l.onAnswer != nil {
			go l.onAnswer(a)
		}
	})
}

func (l *live) overlay() terminal.Overlay {
	return terminal.Overlay{
		Title:    l.prompt.Title,
		Body:     l.prompt.Body,
		Options:  l.prompt.Choices,
		Selected: l.at(),
		Footer:   footer,
	}
}
