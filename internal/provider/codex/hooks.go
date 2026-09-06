package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kamrul1157024/helios/internal/backend"
	"github.com/kamrul1157024/helios/internal/hitl"
	"github.com/kamrul1157024/helios/internal/notifications"
	"github.com/kamrul1157024/helios/internal/provider"
	"github.com/kamrul1157024/helios/internal/store"
)

var errNoTerminal = errors.New("codex: session has no live terminal")

// hookInput is the payload Codex sends. Field names measured against
// codex-cli 0.150.1; see docs/specs/46-codex-provider.md for the full capture.
type hookInput struct {
	SessionID      string          `json:"session_id"`
	TurnID         string          `json:"turn_id,omitempty"`
	TranscriptPath string          `json:"transcript_path,omitempty"`
	CWD            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name,omitempty"`
	Model          string          `json:"model,omitempty"`
	PermissionMode string          `json:"permission_mode,omitempty"`
	Source         string          `json:"source,omitempty"` // SessionStart: startup|resume|clear|compact
	Reason         string          `json:"reason,omitempty"` // SessionEnd
	Prompt         string          `json:"prompt,omitempty"` // UserPromptSubmit
	ToolName       string          `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
	ToolUseID      string          `json:"tool_use_id,omitempty"`
	// ToolResponse is raw because its shape is the tool's, not Codex's: shell
	// returns a string, web_search an object. Decoding it as a string made
	// every PostToolUse after a search a failed hook — curl -f saw the 400 and
	// exited 22 — for a field nothing reads.
	ToolResponse json.RawMessage `json:"tool_response,omitempty"`
	Trigger      string          `json:"trigger,omitempty"` // PreCompact/PostCompact
	AgentID      string          `json:"agent_id,omitempty"`
	AgentType    string          `json:"agent_type,omitempty"`
	// Stop
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
	StopHookActive       bool   `json:"stop_hook_active,omitempty"`
}

// heliosSessionHeader carries the id Helios minted.
//
// Codex mints its own session id and Helios has never seen it, so without this
// header a hook cannot be matched to a session row. The launch environment
// puts HELIOS_SESSION in the agent's process and the hook table's curl sends
// it back. See HeliosSessionEnv.
const heliosSessionHeader = "X-Helios-Session"

// sessionKey is the row a hook belongs to.
//
// Three answers, in order. The header, when Helios launched the session. Then
// a row already bound to this Codex id, which covers a hook that lost the
// environment — a subagent, a re-exec, a shell that scrubbed it — and which
// without this lookup would mint a second, ghost row for a session already
// being tracked. Then the Codex id itself, which is a session the user started
// by hand and is how those become visible for free.
func sessionKey(ctx *provider.HookContext, r *http.Request, in *hookInput) string {
	if id := r.Header.Get(heliosSessionHeader); id != "" {
		return id
	}
	if sess, err := ctx.DB.SessionByResumeID(in.SessionID); err == nil && sess != nil {
		return sess.SessionID
	}
	return in.SessionID
}

// decisionTimeout is how long a blocking hook waits for a human on some
// surface.
const decisionTimeout = 5 * time.Minute

// HookTimeoutSeconds is what Codex is told to wait. It clears decisionTimeout
// by a margin on purpose: Helios has to give up first, or the CLI abandons a
// hook that is still holding a prompt on screen and the two race to decide
// what happened.
const HookTimeoutSeconds = int((decisionTimeout + 30*time.Second) / time.Second)

func (p *Provider) HookRoutes() map[string]provider.HookHandler {
	return map[string]provider.HookHandler{
		"session/start":  handleSessionStart,
		"session/end":    handleSessionEnd,
		"prompt/submit":  handlePromptSubmit,
		"tool/pre":       handleToolPre,
		"tool/post":      handleToolPost,
		"permission":     handlePermission,
		"stop":           handleStop,
		"compact/pre":    handlePreCompact,
		"compact/post":   handlePostCompact,
		"subagent/start": handleSubagentStart,
		"subagent/stop":  handleSubagentStop,
	}
}

// decode reads a hook body, or writes the error and reports failure.
//
// It is also where a one-shot run is turned away. Hooks are configured for the
// whole of Codex, so `codex exec` fires every one of them, and Helios runs an
// exec each time it names a session. Turning away only the session-start hook
// left the rest arriving for a session that does not exist: a "Session
// completed" notification for the titler's own reply, and — because the stop
// hook titles the session it just heard from — another exec, which stops,
// which titles.
func decode(w http.ResponseWriter, raw json.RawMessage) (*hookInput, bool) {
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return nil, false
	}
	if in.TranscriptPath != "" && oneShot(in.TranscriptPath) {
		ack(w)
		return nil, false
	}
	return &in, true
}

// oneShot is IsOneShot, memoised. Hooks fire several times a turn and a
// rollout's origin never changes, so it is read from disk once.
var oneShotCache sync.Map

func oneShot(rolloutPath string) bool {
	if v, ok := oneShotCache.Load(rolloutPath); ok {
		return v.(bool)
	}
	v := IsOneShot(rolloutPath)
	oneShotCache.Store(rolloutPath, v)
	return v
}

// ack writes the empty object Codex expects.
//
// Stop in particular requires valid JSON on exit 0, and reads
// {"decision":"block"} as "keep going" — so an empty object is not merely
// polite here, it is the difference between ending a turn and restarting it.
func ack(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{}`)
}

// ==================== Lifecycle ====================

func handleSessionStart(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	in, ok := decode(w, raw)
	if !ok {
		return
	}
	key := sessionKey(ctx, r, in)

	var transcriptPath *string
	if in.TranscriptPath != "" {
		transcriptPath = &in.TranscriptPath
	}
	var model *string
	if in.Model != "" {
		model = &in.Model
	}

	handle := ""
	if ctx.Terminal != nil {
		handle, _ = ctx.Terminal.Handle(key)
	}
	if ctx.SessionStarted != nil {
		ctx.SessionStarted(key)
	}

	ctx.DB.UpsertSession(&store.Session{
		SessionID:      key,
		Source:         "codex",
		CWD:            in.CWD,
		TranscriptPath: transcriptPath,
		Model:          model,
		Status:         "idle",
		LastEvent:      strPtr("SessionStart"),
	})

	// The id Codex minted, which is the only thing `codex resume` accepts.
	// Recorded on every start, not only the first: a resumed session reports
	// the same id, and a cleared one reports a new id under the same row.
	if in.SessionID != "" {
		if err := ctx.DB.UpdateSessionResumeID(key, in.SessionID); err != nil {
			log.Printf("codex: record resume id for %s: %v", key, err)
		}
	}

	renameSessionWindow(ctx, key, "idle", in.CWD)

	data := map[string]interface{}{
		"session_id": key, "cwd": in.CWD, "status": "idle", "model": in.Model,
	}
	if handle != "" {
		data["terminal"] = handle
	}
	ctx.Notify("session_status", data)
	report(ctx, provider.ReportEvent{Type: "session_start", SessionID: key, CWD: in.CWD})
	ack(w)
}

func handleSessionEnd(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	in, ok := decode(w, raw)
	if !ok {
		return
	}
	key := sessionKey(ctx, r, in)

	if ctx.Terminal != nil {
		ctx.Terminal.Forget(key)
	}
	// Unless helios stopped the agent itself to reclaim memory. The exit is
	// then ours, not the user's, and the session is cold rather than over.
	// See docs/specs/42-cold-sessions.md.
	if evicter, ok := ctx.Terminal.(backend.Evicter); ok && evicter.EvictedRecently(key) {
		ctx.Notify("session_updated", map[string]interface{}{"session_id": key})
		ack(w)
		return
	}

	ctx.DB.UpdateSessionStatus(key, "terminated", "SessionEnd")
	ctx.Notify("session_status", map[string]interface{}{"session_id": key, "status": "terminated"})
	report(ctx, provider.ReportEvent{Type: "session_end", SessionID: key, CWD: in.CWD})
	ack(w)
}

func handlePromptSubmit(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	in, ok := decode(w, raw)
	if !ok {
		return
	}
	key := sessionKey(ctx, r, in)

	ctx.DB.UpdateSessionStatus(key, "active", "UserPromptSubmit")
	updateTranscript(ctx, key, in)
	updateMode(ctx, key, in)
	renameSessionWindow(ctx, key, "active", in.CWD)

	if in.Prompt != "" {
		ctx.DB.UpdateSessionLastUserMessage(key, in.Prompt)
	}
	ctx.Notify("session_status", map[string]interface{}{
		"session_id": key, "status": "active", "last_user_message": in.Prompt,
	})
	report(ctx, provider.ReportEvent{Type: "prompt_submit", SessionID: key, CWD: in.CWD, Message: in.Prompt})

	// Last, so a caller woken by this has the status and the message already
	// written rather than racing them.
	if ctx.PromptSubmitted != nil {
		ctx.PromptSubmitted(key)
	}
	ack(w)
}

func handleStop(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	in, ok := decode(w, raw)
	if !ok {
		return
	}
	key := sessionKey(ctx, r, in)

	ctx.DB.UpdateSessionStatus(key, "idle", "Stop")
	updateTranscript(ctx, key, in)
	renameSessionWindow(ctx, key, "idle", in.CWD)

	if _, err := ctx.Mgr.ResolveSession(key, "resolved", "codex"); err != nil {
		log.Printf("codex: resolve notifications for %s: %v", key, err)
	}

	sess, _ := ctx.DB.GetSession(key)
	title := "Session completed"
	detail := sessionContext(in.CWD, sess)
	if in.LastAssistantMessage != "" {
		detail = truncate(in.LastAssistantMessage, 200)
	}
	notif := &store.Notification{
		ID:            notifications.GenerateNotificationID(),
		Source:        "codex",
		SourceSession: key,
		CWD:           in.CWD,
		Type:          "codex.done",
		Status:        "dismissed",
		Title:         &title,
		Detail:        &detail,
	}
	ctx.Mgr.CreateNotification(notif)
	ctx.Notify("notification", notif)
	ctx.Notify("session_status", map[string]interface{}{
		"session_id": key, "cwd": in.CWD, "status": "idle",
	})
	report(ctx, provider.ReportEvent{Type: "stop", SessionID: key, CWD: in.CWD, Detail: in.LastAssistantMessage})

	if t := provider.TitlerFor("codex"); t != nil {
		t.AutoTitle(ctx, key, in.CWD, in.TranscriptPath, ctx.Notify)
	}
	ack(w)
}

// ==================== Tools ====================

func handleToolPre(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	in, ok := decode(w, raw)
	if !ok {
		return
	}
	key := sessionKey(ctx, r, in)
	ctx.DB.UpdateSessionStatus(key, "active", "PreToolUse:"+in.ToolName)
	updateTranscript(ctx, key, in)
	ctx.Notify("session_status", map[string]interface{}{
		"session_id": key, "status": "active", "last_event": "PreToolUse:" + in.ToolName,
	})
	report(ctx, provider.ReportEvent{
		Type: "tool_pre", SessionID: key, CWD: in.CWD,
		ToolName: in.ToolName, ToolInput: summarize(in.ToolInput),
	})
	ack(w)
}

func handleToolPost(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	in, ok := decode(w, raw)
	if !ok {
		return
	}
	key := sessionKey(ctx, r, in)
	ctx.DB.UpdateSessionStatus(key, "active", "PostToolUse:"+in.ToolName)
	updateTranscript(ctx, key, in)
	// A tool ran, so the paths clients are watching may have moved. No path is
	// passed: the daemon works out what changed from its own digests, which is
	// why apply_patch needs no parsing here. See spec 54.
	if ctx.FilesTouched != nil {
		ctx.FilesTouched()
	}
	// The tool ran, so any permission card for it is settled whichever surface
	// answered. Matched by tool name because PermissionRequest carries no
	// tool_use_id to match on — measured, see spec 46.
	resolveToolPermissions(ctx, key, in.ToolName)
	report(ctx, provider.ReportEvent{
		Type: "tool_post", SessionID: key, CWD: in.CWD,
		ToolName: in.ToolName, ToolInput: summarize(in.ToolInput),
	})
	ack(w)
}

// ==================== Permission ====================

// permResponse is the PermissionRequest hook's reply.
//
// Only allow and deny work. updatedInput, updatedPermissions and interrupt are
// reserved and fail closed in 0.150.1, which is why Codex has no
// "allow, and don't ask again".
type permResponse struct {
	HookSpecificOutput struct {
		HookEventName string `json:"hookEventName"`
		Decision      struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message,omitempty"`
		} `json:"decision"`
	} `json:"hookSpecificOutput"`
}

func writePermResponse(w http.ResponseWriter, behavior, message string) {
	var resp permResponse
	resp.HookSpecificOutput.HookEventName = "PermissionRequest"
	resp.HookSpecificOutput.Decision.Behavior = behavior
	resp.HookSpecificOutput.Decision.Message = message
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("codex: encode permission response: %v", err)
	}
}

const (
	allowOnce  = "Allow once"
	denyChoice = "Deny"

	// Codex's own approval dialog offers "No, and tell Codex what to do
	// differently". Helios paints over that dialog, so it has to offer the row
	// too or the only refusal it leaves the user is a bare no.
	feedbackLabel = "Tell Codex what to do differently"
)

// What Codex reads in place of the tool's result when it is refused.
//
// A denied call comes back to the model as an error, so bare text there reads
// as a malfunction rather than as a person talking. Each of these says who is
// speaking. Codex honours a deny message; see docs/specs/46-codex-provider.md.
const (
	deniedReason   = "Denied via helios"
	deniedFeedback = "The user denied this in helios and said:"
)

// permissionAnswer is what a surface sends back beyond approve or deny. Only
// the words matter here: Codex cannot write a permission rule and cannot take
// an edited input, so there is nothing else to carry.
type permissionAnswer struct {
	Feedback string `json:"feedback,omitempty"`
}

// deniedWithFeedback refuses the tool in the user's own words.
func deniedWithFeedback(text string) notifications.Decision {
	response, err := json.Marshal(permissionAnswer{Feedback: text})
	if err != nil {
		log.Printf("codex: encode denial feedback: %v", err)
		return notifications.Decision{Status: "denied"}
	}
	return notifications.Decision{Status: "denied", Response: response}
}

// denyMessage is the sentence Codex reads for a refusal, whichever surface
// refused. Unreadable feedback degrades to the bare reason rather than to a
// tool that hangs.
func denyMessage(sessionID string, decision *notifications.Decision) string {
	if len(decision.Response) == 0 {
		return deniedReason
	}
	var answer permissionAnswer
	if err := json.Unmarshal(decision.Response, &answer); err != nil {
		log.Printf("codex: read denial for %s: %v", sessionID, err)
		return deniedReason
	}
	if text := strings.TrimSpace(answer.Feedback); text != "" {
		return deniedFeedback + "\n" + text
	}
	return deniedReason
}

// terminalDecision reads an answer off the overlay. Anything it does not
// recognise — escape, a row that is gone, a prompt that changed under it —
// denies, because the safe reading of an unclear answer is no.
func terminalDecision(choices []string, a hitl.Answer) notifications.Decision {
	// Text first: a typed answer carries Index -1, so indexing choices with it
	// would read off the front of the list.
	if text := strings.TrimSpace(a.Text); text != "" {
		return deniedWithFeedback(text)
	}
	if a.Cancelled() || a.Index < 0 || a.Index >= len(choices) {
		return notifications.Decision{Status: "denied"}
	}
	if choices[a.Index] == allowOnce {
		return notifications.Decision{Status: "approved"}
	}
	return notifications.Decision{Status: "denied"}
}

func handlePermission(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	in, ok := decode(w, raw)
	if !ok {
		return
	}
	key := sessionKey(ctx, r, in)

	ctx.DB.UpdateSessionStatus(key, "waiting_permission", "PermissionRequest")
	updateTranscript(ctx, key, in)
	updateMode(ctx, key, in)
	renameSessionWindow(ctx, key, "waiting_permission", in.CWD)

	notifID := notifications.GenerateNotificationID()
	detail := fmt.Sprintf("%s: %s", in.ToolName, summarize(in.ToolInput))
	payload, _ := json.Marshal(map[string]interface{}{
		"tool_name":  in.ToolName,
		"tool_input": json.RawMessage(in.ToolInput),
	})
	payloadStr := string(payload)

	notif := &store.Notification{
		ID:            notifID,
		Source:        "codex",
		SourceSession: key,
		CWD:           in.CWD,
		Type:          "codex.permission",
		Status:        "pending",
		Title:         &in.ToolName,
		Detail:        &detail,
		Payload:       &payloadStr,
	}
	if err := ctx.Mgr.CreateNotification(notif); err != nil {
		http.Error(w, "failed to create notification", http.StatusInternalServerError)
		return
	}
	// Reserve the decision slot before publishing, so a client that answers
	// immediately cannot beat this handler to WaitForDecision.
	ctx.Mgr.Register(notifID)
	ctx.Notify("notification", notif)
	report(ctx, provider.ReportEvent{
		Type: "permission", SessionID: key, CWD: in.CWD,
		ToolName: in.ToolName, ToolInput: summarize(in.ToolInput),
	})

	// Painted after publishing, so the phone and the terminal race from the
	// same starting line, and taken down once the decision is settled no
	// matter which of them settled it.
	//
	// Only two choices: Codex cannot write a permission rule, so there is
	// nothing for "don't ask again" to apply. The third row is not a choice —
	// it is the text field, and the words it takes become the deny message.
	choices := []string{allowOnce, denyChoice}
	defer showPrompt(ctx, key, hitl.Prompt{
		Title:     in.ToolName,
		Body:      []string{summarize(in.ToolInput)},
		Choices:   choices,
		AllowText: true,
		TextLabel: feedbackLabel,
	}, func(a hitl.Answer) {
		if err := ctx.Mgr.Resolve(notifID, terminalDecision(choices, a), "terminal"); err != nil &&
			!errors.Is(err, store.ErrAlreadyResolved) {
			log.Printf("codex: resolve permission %s from the terminal: %v", notifID, err)
		}
	})()

	decision := waitForDecision(ctx, notifID, r)
	if decision == nil {
		return
	}

	ctx.DB.UpdateSessionStatus(key, "active", "PermissionResolved")
	renameSessionWindow(ctx, key, "active", in.CWD)

	if decision.Status == "approved" {
		writePermResponse(w, "allow", "")
		return
	}
	writePermResponse(w, "deny", denyMessage(key, decision))
}

// ==================== Compaction and subagents ====================

func handlePreCompact(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	compactStatus(ctx, w, r, raw, "compacting", "PreCompact", "compact_pre")
}

func handlePostCompact(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	compactStatus(ctx, w, r, raw, "active", "PostCompact", "compact_post")
}

func compactStatus(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request,
	raw json.RawMessage, status, event, reportType string) {
	in, ok := decode(w, raw)
	if !ok {
		return
	}
	key := sessionKey(ctx, r, in)
	ctx.DB.UpdateSessionStatus(key, status, event)
	updateTranscript(ctx, key, in)
	renameSessionWindow(ctx, key, status, in.CWD)
	ctx.Notify("session_status", map[string]interface{}{"session_id": key, "status": status})
	report(ctx, provider.ReportEvent{Type: reportType, SessionID: key, CWD: in.CWD})
	ack(w)
}

func handleSubagentStart(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	in, ok := decode(w, raw)
	if !ok {
		return
	}
	key := sessionKey(ctx, r, in)
	sub := &store.Subagent{AgentID: in.AgentID, ParentSessionID: key, Status: "active"}
	if in.AgentType != "" {
		sub.AgentType = &in.AgentType
	}
	ctx.DB.CreateSubagent(sub)
	ctx.Notify("subagent_status", map[string]interface{}{
		"agent_id": in.AgentID, "parent_session_id": key,
		"agent_type": in.AgentType, "status": "active",
	})
	report(ctx, provider.ReportEvent{Type: "subagent_start", SessionID: key, CWD: in.CWD, AgentType: in.AgentType})
	ack(w)
}

func handleSubagentStop(ctx *provider.HookContext, w http.ResponseWriter, r *http.Request, raw json.RawMessage) {
	in, ok := decode(w, raw)
	if !ok {
		return
	}
	key := sessionKey(ctx, r, in)
	ctx.DB.UpdateSubagentStatus(in.AgentID, "completed")
	ctx.Notify("subagent_status", map[string]interface{}{
		"agent_id": in.AgentID, "parent_session_id": key, "status": "completed",
	})
	report(ctx, provider.ReportEvent{Type: "subagent_stop", SessionID: key, CWD: in.CWD})
	ack(w)
}

// ==================== Helpers ====================

func waitForDecision(ctx *provider.HookContext, notifID string, r *http.Request) *notifications.Decision {
	timer := time.NewTimer(decisionTimeout)
	defer timer.Stop()

	ch := make(chan notifications.Decision, 1)
	go func() {
		decision, err := ctx.Mgr.WaitForDecision(notifID)
		if err != nil {
			ch <- notifications.Decision{Status: "denied"}
			return
		}
		ch <- decision
	}()

	select {
	case decision := <-ch:
		return &decision
	case <-timer.C:
		ctx.Mgr.CancelPending(notifID)
		denied := notifications.Decision{Status: "denied"}
		return &denied
	case <-r.Context().Done():
		ctx.Mgr.CancelPendingFromClaude(notifID)
		return nil
	}
}

// showPrompt paints a helios modal over a session's terminal and returns the
// function that takes it down.
//
// A session with no terminal is not a failure: the notification is already on
// the phone. Every caller defers the returned function, so it is always safe
// to call.
func showPrompt(ctx *provider.HookContext, sessionID string, p hitl.Prompt, onAnswer func(hitl.Answer)) func() {
	release, err := ctx.HITL.Ask(sessionID, p, onAnswer)
	if err != nil {
		if !errors.Is(err, hitl.ErrNoTerminal) {
			log.Printf("codex: show %q prompt for %s: %v", p.Title, sessionID, err)
		}
		return func() {}
	}
	return release
}

// resolveToolPermissions retracts the pending card for a tool that has run.
func resolveToolPermissions(ctx *provider.HookContext, sessionID, toolName string) {
	if toolName == "" {
		return
	}
	pending, err := ctx.Mgr.ListNotifications("codex", "pending", "codex.permission")
	if err != nil {
		log.Printf("codex: list pending permissions for %s: %v", sessionID, err)
		return
	}
	for i := range pending {
		n := &pending[i]
		if n.SourceSession != sessionID || permissionToolName(n) != toolName {
			continue
		}
		ctx.Mgr.CancelPendingFromClaude(n.ID)
	}
}

func permissionToolName(n *store.Notification) string {
	if n.Payload == nil {
		return ""
	}
	var payload struct {
		ToolName string `json:"tool_name"`
	}
	if err := json.Unmarshal([]byte(*n.Payload), &payload); err != nil {
		return ""
	}
	return payload.ToolName
}

func updateTranscript(ctx *provider.HookContext, key string, in *hookInput) {
	if in.TranscriptPath != "" {
		ctx.DB.UpdateSessionTranscriptPath(key, in.TranscriptPath)
	}
}

// updateMode records the mode the agent says it is in.
//
// Codex reports Claude's vocabulary here — default, bypassPermissions and so
// on — which is not the vocabulary Helios launches with. Only record a value
// Helios could replay; otherwise a wake would try to pass "default" as a
// sandbox flag.
func updateMode(ctx *provider.HookContext, key string, in *hookInput) {
	if !slices2Contains(permissionModes, in.PermissionMode) {
		return
	}
	if err := ctx.DB.UpdateSessionPermissionMode(key, in.PermissionMode); err != nil {
		log.Printf("codex: record permission mode for %s: %v", key, err)
	}
}

func slices2Contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func renameSessionWindow(ctx *provider.HookContext, sessionID, status, cwd string) {
	if ctx.Terminal == nil || !ctx.Terminal.Alive(sessionID) {
		return
	}
	sess, _ := ctx.DB.GetSession(sessionID)
	label := ""
	if sess != nil {
		label = sess.Label(30)
	}
	if err := ctx.Terminal.Rename(sessionID, backend.DisplayName(status, cwd, label)); err != nil {
		log.Printf("codex: rename terminal for %s: %v", sessionID, err)
	}
}

func report(ctx *provider.HookContext, e provider.ReportEvent) {
	if ctx.Report != nil {
		ctx.Report(e)
	}
}

func summarize(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return truncate(string(raw), 100)
	}
	// Bash and apply_patch both put the interesting part in "command".
	if cmd, ok := m["command"].(string); ok {
		return truncate(cmd, 100)
	}
	return truncate(string(raw), 100)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func sessionContext(cwd string, sess *store.Session) string {
	project := filepath.Base(cwd)
	if sess != nil {
		if label := sess.Label(80); label != "" {
			return fmt.Sprintf("%s: %s", project, label)
		}
	}
	return project
}

func strPtr(s string) *string { return &s }
