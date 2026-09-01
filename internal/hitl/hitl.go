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
	"strings"
	"sync"

	"github.com/kamrul1157024/helios/internal/terminal"
)

// ErrNoTerminal is returned when a session has no terminal to paint on. The
// caller falls back to the phone, which is what every prompt did before this
// package existed.
var ErrNoTerminal = errors.New("hitl: session has no terminal")

// The key hints under a prompt. Short on purpose: they are drawn on terminals
// as narrow as the box allows.
const (
	footer      = "↑↓ select · enter confirm · esc cancel"
	footerMulti = "space toggle · ↑↓ move · enter confirm · esc cancel"
	// footerTyping says what the first Escape does, which is the one thing a
	// user cannot guess: it closes the field and leaves the question standing.
	footerTyping = "enter send · esc back to the list"
)

// defaultTextLabel names the row that opens the answer field.
const defaultTextLabel = "Other…"

// richOverlayProtocol is the first host build that draws checkboxes and the
// answer field. See terminal.HostProtocol.
const richOverlayProtocol = 2

// Prompt is one question helios is waiting on a human to answer.
type Prompt struct {
	// Title labels the box — the tool name, or who is asking.
	Title string
	// Body is the detail above the choices, one entry per paragraph.
	Body []string
	// Choices are the answers, in the order they are shown.
	Choices []string
	// Details are descriptions of the choices, index-aligned with them. A short
	// slice or a blank entry means that choice has none.
	Details []string
	// Multi lets the user tick more than one choice and answer with the set.
	Multi bool
	// AllowText adds a row that opens a field for an answer none of the choices
	// carry.
	AllowText bool
	// TextLabel names that row. Empty means "Other…".
	TextLabel string
}

// Answer is how a terminal user resolved a prompt.
type Answer struct {
	// Index selects Prompt.Choices for a single-choice prompt, and is negative
	// when the user chose nothing.
	Index int
	// Indexes selects them for a Multi prompt.
	Indexes []int
	// Text is what the user typed instead of choosing.
	Text string
}

// Cancelled reports whether the user dismissed the prompt instead of answering.
// A typed answer carries no index, so an index alone cannot decide this.
func (a Answer) Cancelled() bool {
	return a.Index < 0 && len(a.Indexes) == 0 && a.Text == ""
}

// Overlays is the part of the terminal backend a Controller drives.
type Overlays interface {
	SetOverlay(sessionID string, o terminal.Overlay) error
	ClearOverlay(sessionID string) error
	// OverlayProtocol reports what the host behind a session can draw. A
	// session with no host reports 0.
	OverlayProtocol(sessionID string) int
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

	// mu guards the answer being assembled. Input arrives on the mirror's read
	// goroutine while the hook's goroutine may be releasing the prompt, and
	// these are the fields both can touch.
	mu       sync.Mutex
	selected int
	checked  []bool
	text     string
	editing  bool

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
	if len(p.Choices) == 0 && !p.AllowText {
		return func() {}, errors.New("hitl: prompt has no choices")
	}
	// An older host ignores the fields it has never heard of. A description is
	// no loss that way, but the other two are: checkboxes would draw a
	// multi-select question as a single-select list, and the answer field would
	// hide the line the user is typing into.
	//
	// So the answer field is dropped rather than mourned — the choices are still
	// answerable, and anything the user wants to write is still on the phone.
	// Checkboxes have no such half measure, and neither does a question whose
	// only row was the field, so those go to the phone whole.
	if c.terms.OverlayProtocol(sessionID) < richOverlayProtocol {
		if p.Multi || len(p.Choices) == 0 {
			return func() {}, fmt.Errorf("hitl: host cannot draw this prompt: %w", ErrNoTerminal)
		}
		p.AllowText = false
	}

	l := &live{prompt: p, onAnswer: onAnswer}
	if p.Multi {
		l.checked = make([]bool, len(p.Choices))
	}
	// A question with no choices is asking to be typed into. Opening the field
	// saves an Enter whose only purpose would be to open it.
	l.editing = len(p.Choices) == 0

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

	// The mode is read once for the whole frame. A frame that both opens the
	// answer field and types into it would be decoded under the old mode, which
	// no terminal produces: it sends what the user pressed, as they press it.
	moved := false
	for _, ev := range decodeKeys(keys, l.isEditing()) {
		switch ev.kind {
		case keyPrev:
			moved = l.move(-1) || moved
		case keyNext:
			moved = l.move(1) || moved
		case keySelect:
			if ev.n < len(l.prompt.Choices) {
				moved = l.jump(ev.n) || moved
			}
		case keyToggle:
			moved = l.toggle() || moved
		case keyText:
			moved = l.insert(ev.s) || moved
		case keyErase, keyEraseWord, keyEraseLine:
			moved = l.erase(ev.kind) || moved
		case keyLeaveField:
			moved = l.leaveField() || moved
		case keyConfirm:
			if a, done := l.confirm(); done {
				l.answer(a)
				return
			}
			moved = true
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

// rows is how many places the highlight can land: the choices, and the row that
// opens the answer field.
func (l *live) rows() int {
	n := len(l.prompt.Choices)
	if l.prompt.AllowText {
		n++
	}
	return n
}

// move shifts the highlight and reports whether it actually went anywhere.
// Movement stops at the ends rather than wrapping, so holding an arrow key
// cannot roll past "Deny" onto "Allow". Moving also closes the answer field:
// the arrow was aimed at the list.
func (l *live) move(delta int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	next := l.selected + delta
	if next < 0 || next >= l.rows() {
		return false
	}
	if next == l.selected && !l.editing {
		return false
	}
	l.selected, l.editing = next, false
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

// toggle ticks the highlighted choice on a multi-select prompt. The row that
// opens the answer field has nothing to tick.
func (l *live) toggle() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.prompt.Multi || l.selected < 0 || l.selected >= len(l.checked) {
		return false
	}
	l.checked[l.selected] = !l.checked[l.selected]
	return true
}

// insert adds typed or pasted characters to the answer. Newlines become spaces:
// the field is one line, and Enter is how it is sent.
func (l *live) insert(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.editing || s == "" {
		return false
	}
	l.text += strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, s)
	return true
}

// erase removes the rune, the word or the line behind the caret.
func (l *live) erase(k key) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.editing || l.text == "" {
		return false
	}
	switch k {
	case keyEraseLine:
		l.text = ""
	case keyEraseWord:
		trimmed := strings.TrimRight(l.text, " ")
		if i := strings.LastIndex(trimmed, " "); i >= 0 {
			l.text = trimmed[:i+1]
		} else {
			l.text = ""
		}
	default:
		r := []rune(l.text)
		l.text = string(r[:len(r)-1])
	}
	return true
}

// leaveField closes the answer field, keeping what was typed. The prompt stays
// on screen: only a second Escape takes it down.
func (l *live) leaveField() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.editing {
		return false
	}
	l.editing = false
	return true
}

// confirm decides what Enter meant. It reports the answer, and whether the
// prompt is over: on the answer-field row Enter opens the field instead, and an
// empty field closes it rather than sending nothing.
func (l *live) confirm() (Answer, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.editing {
		if text := strings.TrimSpace(l.text); text != "" {
			return Answer{Index: -1, Text: text}, true
		}
		l.editing = false
		return Answer{}, false
	}
	if l.prompt.AllowText && l.selected == len(l.prompt.Choices) {
		l.editing = true
		return Answer{}, false
	}
	if l.prompt.Multi {
		var picked []int
		for i, on := range l.checked {
			if on {
				picked = append(picked, i)
			}
		}
		return Answer{Index: -1, Indexes: picked}, true
	}
	return Answer{Index: l.selected}, true
}

func (l *live) isEditing() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.editing
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
	l.mu.Lock()
	defer l.mu.Unlock()

	o := terminal.Overlay{
		Title:    l.prompt.Title,
		Body:     l.prompt.Body,
		Options:  l.prompt.Choices,
		Details:  l.prompt.Details,
		Selected: l.selected,
		Footer:   footer,
	}
	if l.prompt.Multi {
		o.Checked = append([]bool(nil), l.checked...)
		o.Footer = footerMulti
	}
	if l.prompt.AllowText {
		label := l.prompt.TextLabel
		if label == "" {
			label = defaultTextLabel
		}
		o.Input = &terminal.OverlayInput{Label: label, Value: l.text, Active: l.editing}
	}
	if l.editing {
		o.Footer = footerTyping
	}
	return o
}
