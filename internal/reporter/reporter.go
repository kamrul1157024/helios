package reporter

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
)

// Event represents a hook event worth narrating.
type Event struct {
	Type      string `json:"type"`                 // "tool_pre", "tool_post", "tool_post_failure", "prompt_submit", "stop", "stop_failure", "permission", "question", "session_start", "session_end", "compact_pre", "compact_post", "subagent_start", "subagent_stop", "notification"
	SessionID string `json:"session_id"`           //nolint
	CWD       string `json:"cwd,omitempty"`        //nolint
	ToolName  string `json:"tool_name,omitempty"`   //nolint
	ToolInput string `json:"tool_input,omitempty"`  // summarized tool input (file path, command, etc.)
	Message   string `json:"message,omitempty"`     // user message, question text, notification message
	Status    string `json:"status,omitempty"`      // "idle", "error", etc.
	AgentType string `json:"agent_type,omitempty"`  //nolint
	Detail    string `json:"detail,omitempty"`      // additional context (last session detail, subagent description)
}

// Narration is the SSE payload pushed to connected clients.
type Narration struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

// Listener is an SSE client connected to /api/reporter.
// SessionFilter == "" means all sessions (global voice mode).
// SessionFilter == "<id>" means only that session's narrations.
type Listener struct {
	Ch            chan Narration
	SessionFilter string
	done          chan struct{}
}

// Reporter collects hook events, batches them per session, calls Haiku,
// and pushes narration text to connected SSE clients.
type Reporter struct {
	mu             sync.Mutex
	pendingEvents  map[string][]Event
	debounceTimers map[string]*time.Timer
	listeners      map[*Listener]bool
	listenersMu    sync.RWMutex
	providerID     string
	db             *store.Store
}

// New creates a Reporter.
func New(providerID string, db *store.Store) *Reporter {
	return &Reporter{
		pendingEvents:  make(map[string][]Event),
		debounceTimers: make(map[string]*time.Timer),
		listeners:      make(map[*Listener]bool),
		providerID:     providerID,
		db:             db,
	}
}

// debounceWindow returns the configured debounce duration from settings.
func (r *Reporter) debounceWindow() time.Duration {
	val, _ := r.db.GetSetting("reporter.debounce_seconds")
	if val != "" {
		if secs, err := strconv.Atoi(val); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 10 * time.Second
}

// systemPrompt returns the active system prompt based on persona setting.
func (r *Reporter) systemPrompt() string {
	personaID, _ := r.db.GetSetting("reporter.persona")
	if personaID == "" {
		personaID = "default"
	}

	if personaID == "custom" {
		customPrompt, _ := r.db.GetSetting("reporter.custom_prompt")
		if customPrompt != "" {
			return customPrompt
		}
		personaID = "default"
	}

	if p := GetPersona(personaID); p != nil {
		return p.Prompt
	}
	return GetPersona("default").Prompt
}

// HasListeners returns true if any SSE clients are connected.
func (r *Reporter) HasListeners() bool {
	r.listenersMu.RLock()
	defer r.listenersMu.RUnlock()
	return len(r.listeners) > 0
}

// AddEvent queues a hook event for narration.
// Events are buffered per session with a configurable debounce window.
func (r *Reporter) AddEvent(event Event) {
	if !r.HasListeners() {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	sid := event.SessionID
	r.pendingEvents[sid] = append(r.pendingEvents[sid], event)

	if t, ok := r.debounceTimers[sid]; ok {
		t.Stop()
	}
	r.debounceTimers[sid] = time.AfterFunc(r.debounceWindow(), func() {
		r.flush(sid)
	})
}

// defaultGlobalFilter is the set of event types narrated in global mode by default.
var defaultGlobalFilter = map[string]bool{
	"stop":         true,
	"stop_failure": true,
	"question":     true,
	"permission":   true,
}

// eventFilter loads the allowed event types for a mode ("global" or "session")
// from settings. Returns nil if all events are allowed (session default).
func (r *Reporter) eventFilter(mode string) map[string]bool {
	key := "reporter.filter." + mode
	val, _ := r.db.GetSetting(key)
	if val == "" {
		return defaultFilter(mode)
	}

	var types []string
	if err := json.Unmarshal([]byte(val), &types); err != nil {
		return defaultFilter(mode)
	}

	allowed := make(map[string]bool, len(types))
	for _, t := range types {
		allowed[t] = true
	}
	return allowed
}

// defaultFilter returns the hardcoded default filter for a mode.
// Global: only actionable events. Session: nil (all allowed).
func defaultFilter(mode string) map[string]bool {
	if mode == "global" {
		return defaultGlobalFilter
	}
	return nil // session: all events allowed
}

// filterEvents returns only events whose Type is in the allowed set.
// If allowed is nil, all events pass through.
func filterEvents(events []Event, allowed map[string]bool) []Event {
	if allowed == nil {
		return events
	}
	filtered := make([]Event, 0, len(events))
	for _, e := range events {
		if allowed[e.Type] {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// flush collects pending events for a session, calls Haiku, and pushes
// the narration to all matching listeners.
func (r *Reporter) flush(sessionID string) {
	r.mu.Lock()
	events := r.pendingEvents[sessionID]
	delete(r.pendingEvents, sessionID)
	delete(r.debounceTimers, sessionID)
	r.mu.Unlock()

	if len(events) == 0 {
		return
	}

	// Classify listeners into global vs session and collect matching ones.
	r.listenersMu.RLock()
	hasGlobal := false
	hasSession := false
	for l := range r.listeners {
		if l.SessionFilter == "" {
			hasGlobal = true
		} else if l.SessionFilter == sessionID {
			hasSession = true
		}
	}
	r.listenersMu.RUnlock()

	if !hasGlobal && !hasSession {
		return
	}

	// Determine which mode is active and apply event filter.
	// Only one mode is active at a time (mobile enforces mutual exclusion).
	mode := "session"
	if hasGlobal {
		mode = "global"
	}

	filtered := filterEvents(events, r.eventFilter(mode))
	if len(filtered) == 0 {
		return
	}

	// Include session context for global listeners so the narrator
	// can identify which session the events belong to.
	var sessionCtx *SessionContext
	if hasGlobal {
		if sess, err := r.db.GetSession(sessionID); err == nil && sess != nil {
			sessionCtx = &SessionContext{
				CWD:             sess.CWD,
				Title:           sess.Title,
				LastUserMessage: sess.LastUserMessage,
			}
		}
	}

	prompt := buildPrompt(filtered, sessionCtx)
	if prompt == "" {
		return
	}

	system := r.systemPrompt()

	caller := provider.GetSmallModelCaller(r.providerID)
	if caller == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	text, err := caller(ctx, system, prompt)
	if err != nil || text == "" {
		return
	}

	narration := Narration{
		SessionID: sessionID,
		Text:      text,
	}

	r.listenersMu.RLock()
	for listener := range r.listeners {
		if listener.SessionFilter == "" || listener.SessionFilter == sessionID {
			select {
			case listener.Ch <- narration:
			default:
			}
		}
	}
	r.listenersMu.RUnlock()
}

// Subscribe adds a listener. Returns it so the caller can read from Ch.
func (r *Reporter) Subscribe(sessionFilter string) *Listener {
	l := &Listener{
		Ch:            make(chan Narration, 16),
		SessionFilter: sessionFilter,
		done:          make(chan struct{}),
	}
	r.listenersMu.Lock()
	r.listeners[l] = true
	r.listenersMu.Unlock()
	return l
}

// Unsubscribe removes a listener and closes its done channel.
func (r *Reporter) Unsubscribe(l *Listener) {
	r.listenersMu.Lock()
	delete(r.listeners, l)
	r.listenersMu.Unlock()
	close(l.done)
}

// Clear removes all pending events.
func (r *Reporter) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.debounceTimers {
		t.Stop()
	}
	r.pendingEvents = make(map[string][]Event)
	r.debounceTimers = make(map[string]*time.Timer)
}
