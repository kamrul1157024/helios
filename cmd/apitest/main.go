// Command apitest drives the daemon's public API the way a remote client does:
// pair a local device, sign an EdDSA JWT per request, then walk the whole
// session lifecycle over REST, SSE and the terminal WebSocket.
//
// It exists to exercise the paths the desktop and mobile apps take against a
// running daemon, including over the tunnel URL, where TLS and a proxy sit
// between the client and the handler. Scratch tooling; not part of the build.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/kamrul1157024/helios/internal/terminal"
)

var (
	baseURL     = flag.String("base", "http://localhost:7655", "public API base URL")
	internalURL = flag.String("internal", "http://localhost:7654", "internal API base URL (pairing only)")
	keyPath     = flag.String("key", "/tmp/helios-apitest-key.json", "where to cache the paired device key")
	skipAgent   = flag.Bool("skip-agent", false, "skip tests that launch a real agent session")
)

// ─── Device ────────────────────────────────────────────────────────────────

type device struct {
	KID  string `json:"kid"`
	Seed string `json:"seed"` // base64url ed25519 seed
	priv ed25519.PrivateKey
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// loadOrPair reuses a cached device so a remote run does not need the internal
// port, which is loopback-only and unreachable over a tunnel.
func loadOrPair() (*device, error) {
	if raw, err := os.ReadFile(*keyPath); err == nil {
		var d device
		if json.Unmarshal(raw, &d) == nil && d.KID != "" {
			seed, err := base64.RawURLEncoding.DecodeString(d.Seed)
			if err == nil && len(seed) == ed25519.SeedSize {
				d.priv = ed25519.NewKeyFromSeed(seed)
				return &d, nil
			}
		}
	}
	d, err := pair()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(*keyPath, raw, 0o600); err != nil {
		return nil, err
	}
	return d, nil
}

// pair reproduces HostRegistry.pairLocal from desktop/src/main/hosts.ts.
func pair() (*device, error) {
	var created struct {
		Token string `json:"token"`
	}
	if err := postJSON(*internalURL+"/internal/device/create", map[string]any{}, &created); err != nil {
		return nil, fmt.Errorf("device create: %w", err)
	}
	if created.Token == "" {
		return nil, fmt.Errorf("daemon issued no pairing token")
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	kid := fmt.Sprintf("apitest-%d", time.Now().UnixNano())

	var paired map[string]any
	if err := postJSON(*baseURL+"/api/auth/pair", map[string]any{
		"token": created.Token, "kid": kid, "public_key": b64(pub),
	}, &paired); err != nil {
		return nil, fmt.Errorf("pair: %w", err)
	}
	if e, ok := paired["error"]; ok {
		return nil, fmt.Errorf("pairing rejected: %v", e)
	}
	if err := postJSON(*internalURL+"/internal/device/activate", map[string]any{"kid": kid}, nil); err != nil {
		return nil, fmt.Errorf("activate: %w", err)
	}
	return &device{KID: kid, Seed: b64(priv.Seed()), priv: priv}, nil
}

// token mirrors signJWT in desktop/src/main/keys.ts.
func (d *device) token() string {
	header, _ := json.Marshal(map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": d.KID})
	now := time.Now().Unix()
	payload, _ := json.Marshal(map[string]any{"iat": now, "exp": now + 3600, "sub": "helios-client"})
	input := b64(header) + "." + b64(payload)
	return input + "." + b64(ed25519.Sign(d.priv, []byte(input)))
}

func (d *device) req(method, path string, body any) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, *baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+d.token())
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

type reply struct {
	code int
	body []byte
}

func (r reply) json() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.body, &m)
	return m
}

func (r reply) String() string { return fmt.Sprintf("HTTP %d: %s", r.code, trunc(r.body, 300)) }

func (d *device) do(method, path string, body any) (reply, error) {
	req, err := d.req(method, path, body)
	if err != nil {
		return reply{}, err
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return reply{}, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	return reply{resp.StatusCode, out}, err
}

func postJSON(url string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ─── SSE ───────────────────────────────────────────────────────────────────

type sseEvent struct {
	Type string
	Data map[string]any
}

// sseTap holds an open /api/events stream, the way every client keeps one.
type sseTap struct {
	mu     sync.Mutex
	events []sseEvent
	cancel context.CancelFunc
	opened chan struct{}
	err    error
}

func (d *device) tapEvents() *sseTap {
	ctx, cancel := context.WithCancel(context.Background())
	tap := &sseTap{cancel: cancel, opened: make(chan struct{})}
	go func() {
		req, err := d.req("GET", "/api/events", nil)
		if err != nil {
			tap.fail(err)
			return
		}
		req = req.WithContext(ctx)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			tap.fail(err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			tap.fail(fmt.Errorf("events stream returned HTTP %d", resp.StatusCode))
			return
		}
		close(tap.opened)

		var evType string
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				evType = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				var data map[string]any
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data)
				tap.mu.Lock()
				tap.events = append(tap.events, sseEvent{evType, data})
				tap.mu.Unlock()
			}
		}
	}()
	return tap
}

func (t *sseTap) fail(err error) {
	t.mu.Lock()
	t.err = err
	t.mu.Unlock()
	select {
	case <-t.opened:
	default:
		close(t.opened)
	}
}

func (t *sseTap) wait(d time.Duration) error {
	select {
	case <-t.opened:
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.err
	case <-time.After(d):
		return fmt.Errorf("events stream did not open within %s", d)
	}
}

// seek waits for an event matching pred, so a test does not race the stream.
func (t *sseTap) seek(timeout time.Duration, pred func(sseEvent) bool) (sseEvent, bool) {
	deadline := time.Now().Add(timeout)
	for {
		t.mu.Lock()
		for _, e := range t.events {
			if pred(e) {
				t.mu.Unlock()
				return e, true
			}
		}
		t.mu.Unlock()
		if time.Now().After(deadline) {
			return sseEvent{}, false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (t *sseTap) types() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := map[string]int{}
	var out []string
	for _, e := range t.events {
		if seen[e.Type] == 0 {
			out = append(out, e.Type)
		}
		seen[e.Type]++
	}
	for i, s := range out {
		out[i] = fmt.Sprintf("%s×%d", s, seen[s])
	}
	return out
}

// ─── Reporting ─────────────────────────────────────────────────────────────

var (
	passes   int
	failures []string
)

func check(name string, ok bool, detail string) bool {
	if ok {
		passes++
		fmt.Printf("  \033[32mPASS\033[0m  %s\n", name)
		return true
	}
	failures = append(failures, name+" — "+detail)
	fmt.Printf("  \033[31mFAIL\033[0m  %s\n        %s\n", name, detail)
	return false
}

func step(name string) { fmt.Printf("\n\033[1m%s\033[0m\n", name) }

func infof(format string, args ...any) {
	fmt.Printf("  ....  "+format+"\n", args...)
}

func trunc(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ─── Session helpers ───────────────────────────────────────────────────────

func (d *device) session(id string) (map[string]any, error) {
	r, err := d.do("GET", "/api/sessions/"+id, nil)
	if err != nil {
		return nil, err
	}
	if r.code != 200 {
		return nil, fmt.Errorf("%s", r)
	}
	m := r.json()
	if inner, ok := m["session"].(map[string]any); ok {
		return inner, nil
	}
	return m, nil
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// awaitStatus polls until the session reaches one of want, mirroring what a
// client does between SSE events.
func (d *device) awaitStatus(id string, timeout time.Duration, want ...string) (string, error) {
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		sess, err := d.session(id)
		if err != nil {
			return last, err
		}
		status := str(sess, "status")
		if status != last {
			infof("status → %s", status)
			last = status
		}
		for _, w := range want {
			if status == w {
				return status, nil
			}
		}
		time.Sleep(time.Second)
	}
	return last, fmt.Errorf("still %q after %s, wanted one of %v", last, timeout, want)
}

func (d *device) transcriptText(id string) (string, int, error) {
	r, err := d.do("GET", "/api/sessions/"+id+"/transcript?limit=200", nil)
	if err != nil {
		return "", 0, err
	}
	if r.code != 200 {
		return "", 0, fmt.Errorf("%s", r)
	}
	var parsed struct {
		Messages []map[string]any `json:"messages"`
		Total    int              `json:"total"`
	}
	if err := json.Unmarshal(r.body, &parsed); err != nil {
		return "", 0, err
	}
	var sb strings.Builder
	for _, m := range parsed.Messages {
		raw, _ := json.Marshal(m)
		sb.Write(raw)
	}
	return sb.String(), parsed.Total, nil
}

func (d *device) pendingNotifications() ([]map[string]any, error) {
	r, err := d.do("GET", "/api/notifications?status=pending", nil)
	if err != nil {
		return nil, err
	}
	if r.code != 200 {
		return nil, fmt.Errorf("%s", r)
	}
	var parsed struct {
		Notifications []map[string]any `json:"notifications"`
	}
	if err := json.Unmarshal(r.body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Notifications, nil
}

// belongsTo reports whether a notification is for sessionID.
//
// source_session is the column every server-side lifecycle sweep keys on, but
// some producers only record the session inside the payload, so both are
// checked here — a client has no other way to route the tap.
func belongsTo(n map[string]any, sessionID string) bool {
	if str(n, "source_session") == sessionID {
		return true
	}
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if raw := str(n, "payload"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &payload)
	}
	return payload.SessionID == sessionID
}

// awaitNotification waits for a pending notification belonging to sessionID.
func (d *device) awaitNotification(sessionID string, timeout time.Duration, ofType ...string) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		notifs, err := d.pendingNotifications()
		if err != nil {
			return nil, err
		}
		for _, n := range notifs {
			if !belongsTo(n, sessionID) {
				continue
			}
			if len(ofType) == 0 {
				return n, nil
			}
			for _, t := range ofType {
				if str(n, "type") == t {
					return n, nil
				}
			}
		}
		time.Sleep(time.Second)
	}
	return nil, fmt.Errorf("no pending %v notification for %s within %s", ofType, sessionID, timeout)
}

// ─── Suite ─────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()
	fmt.Printf("\033[1mHelios API suite\033[0m  base=%s\n", *baseURL)

	dev, err := loadOrPair()
	if err != nil {
		fmt.Println("pairing failed:", err)
		os.Exit(1)
	}
	infof("device %s", dev.KID)

	sessionID := ""
	defer func() {
		if sessionID != "" {
			cleanup(dev, sessionID)
		}
		summarize()
	}()

	// ── Unauthenticated surface ──
	step("Health and auth")
	resp, err := http.Get(*baseURL + "/api/health")
	if err == nil {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		check("GET /api/health is reachable without a token", resp.StatusCode == 200,
			fmt.Sprintf("HTTP %d: %s", resp.StatusCode, trunc(body, 200)))
		infof("health %s", trunc(body, 200))
	} else {
		check("GET /api/health is reachable without a token", false, err.Error())
	}

	noAuth, err := http.Get(*baseURL + "/api/sessions")
	if err == nil {
		noAuth.Body.Close()
		check("GET /api/sessions rejects an unauthenticated caller", noAuth.StatusCode == 401,
			fmt.Sprintf("HTTP %d, expected 401", noAuth.StatusCode))
	} else {
		check("GET /api/sessions rejects an unauthenticated caller", false, err.Error())
	}

	bad, err := http.NewRequest("GET", *baseURL+"/api/sessions", nil)
	if err == nil {
		bad.Header.Set("Authorization", "Bearer not.a.jwt")
		if r, err := (&http.Client{Timeout: 10 * time.Second}).Do(bad); err == nil {
			r.Body.Close()
			check("a malformed bearer token is rejected", r.StatusCode == 401,
				fmt.Sprintf("HTTP %d, expected 401", r.StatusCode))
		}
	}

	// ── Read-only surface ──
	step("Read-only endpoints")
	for _, path := range []string{
		"/api/sessions", "/api/sessions/directories", "/api/providers",
		"/api/commands", "/api/settings", "/api/notifications", "/api/auth/devices",
	} {
		r, err := dev.do("GET", path, nil)
		if err != nil {
			check("GET "+path, false, err.Error())
			continue
		}
		check("GET "+path, r.code == 200, r.String())
	}

	// ── SSE ──
	step("Event stream")
	tap := dev.tapEvents()
	if err := tap.wait(10 * time.Second); err != nil {
		check("GET /api/events opens", false, err.Error())
	} else {
		check("GET /api/events opens", true, "")
	}
	defer tap.cancel()

	if *skipAgent {
		infof("skipping agent tests (-skip-agent)")
		return
	}

	// ── Session lifecycle ──
	step("Create a session")
	cwd, err := os.MkdirTemp("", "helios-e2e-")
	if err != nil {
		check("scratch directory", false, err.Error())
		return
	}
	defer os.RemoveAll(cwd)
	if err := os.WriteFile(filepath.Join(cwd, "hello.txt"), []byte("helios e2e\n"), 0o644); err != nil {
		check("scratch directory", false, err.Error())
		return
	}
	infof("cwd %s", cwd)

	const marker = "HELIOSOK"
	create, err := dev.do("POST", "/api/sessions", map[string]any{
		"provider": "claude",
		"cwd":      cwd,
		// "manual" rather than the launch default: "auto" waves through the
		// safe tools, so an approval would never be raised to test.
		"permission_mode": "manual",
		"prompt":          "Reply with exactly " + marker + " and nothing else. Do not use any tools.",
	})
	if err != nil {
		check("POST /api/sessions", false, err.Error())
		return
	}
	if !check("POST /api/sessions returns 200", create.code == 200, create.String()) {
		return
	}
	created := create.json()
	sessionID = str(created, "session_id")
	if !check("the response carries a session_id", sessionID != "", create.String()) {
		return
	}
	infof("session %s", sessionID)
	check("the response echoes the requested cwd", str(created, "cwd") == cwd,
		fmt.Sprintf("got %q, want %q", str(created, "cwd"), cwd))

	step("The new session appears everywhere a client looks")
	if sess, err := dev.session(sessionID); err != nil {
		check("GET /api/sessions/{id} finds it", false, err.Error())
	} else {
		check("GET /api/sessions/{id} finds it", true, "")
		check("it is marked managed", sess["managed"] == true, fmt.Sprintf("managed=%v", sess["managed"]))
		check("its cwd is recorded", str(sess, "cwd") == cwd,
			fmt.Sprintf("got %q, want %q", str(sess, "cwd"), cwd))
	}
	if list, err := dev.do("GET", "/api/sessions", nil); err == nil {
		check("it is in the session list", strings.Contains(string(list.body), sessionID),
			"the list did not mention the new session id")
	}

	// A fresh directory is untrusted, so Claude opens its trust dialog before it
	// will run anything. Answering it is part of creating a session from a
	// client — the agent is blocked until someone does.
	step("The workspace-trust dialog is answerable from a client")
	trust, err := dev.awaitNotification(sessionID, 60*time.Second, "claude.trust")
	if !check("a claude.trust notification is raised for a new directory", err == nil, fmt.Sprintf("%v", err)) {
		return
	}
	trustID := str(trust, "id")
	infof("trust notification %s", trustID)
	check("the trust notification names its session in source_session",
		str(trust, "source_session") == sessionID,
		fmt.Sprintf("source_session=%q, want %q — clients cannot route it, and no "+
			"session-scoped sweep will ever resolve it",
			str(trust, "source_session"), sessionID))
	if r, err := dev.do("POST", "/api/notifications/"+trustID+"/action",
		map[string]any{"action": "trust"}); err == nil {
		check("trusting the workspace is accepted", r.code == 200, r.String())
	}

	step("The agent starts and answers")
	if _, err := dev.awaitStatus(sessionID, 90*time.Second, "active", "idle", "waiting_permission"); err != nil {
		check("the session leaves \"starting\"", false, err.Error())
	} else {
		check("the session leaves \"starting\"", true, "")
	}
	if _, err := dev.awaitStatus(sessionID, 180*time.Second, "idle"); err != nil {
		check("the agent finishes the first prompt", false, err.Error())
	} else {
		check("the agent finishes the first prompt", true, "")
	}

	text, total, err := dev.transcriptText(sessionID)
	if err != nil {
		check("GET transcript", false, err.Error())
	} else {
		infof("transcript has %d messages", total)
		check("the transcript is populated", total > 0, "transcript is empty")
		check("the agent's reply is in the transcript", strings.Contains(text, marker),
			"no "+marker+" in the transcript")
	}

	if _, ok := tap.seek(5*time.Second, func(e sseEvent) bool {
		return e.Type == "session_status" && str(e.Data, "session_id") == sessionID
	}); ok {
		check("clients were told the status changed over SSE", true, "")
	} else {
		check("clients were told the status changed over SSE", false,
			"no session_status event for this session; saw "+strings.Join(tap.types(), ", "))
	}

	step("Send a follow-up, the way the desktop composer does")
	const marker2 = "SECONDOK"
	send, err := dev.do("POST", "/api/sessions/"+sessionID+"/send", map[string]any{
		"message": "Reply with exactly " + marker2 + " and nothing else. Do not use any tools.",
	})
	if err != nil {
		check("POST /send", false, err.Error())
	} else if check("POST /send is accepted", send.code == 200, send.String()) {
		if _, err := dev.awaitStatus(sessionID, 180*time.Second, "idle"); err != nil {
			check("the follow-up completes", false, err.Error())
		} else {
			check("the follow-up completes", true, "")
			text, _, err := dev.transcriptText(sessionID)
			if err != nil {
				check("the follow-up reply reaches the transcript", false, err.Error())
			} else {
				check("the follow-up reply reaches the transcript", strings.Contains(text, marker2),
					"no "+marker2+" in the transcript")
			}
		}
	}

	step("Empty and malformed sends are refused")
	if r, err := dev.do("POST", "/api/sessions/"+sessionID+"/send", map[string]any{"message": ""}); err == nil {
		check("an empty message is a 400", r.code == 400, r.String())
	}
	if r, err := dev.do("POST", "/api/sessions/deadbeef-not-a-session/send",
		map[string]any{"message": "hi"}); err == nil {
		check("sending to an unknown session is a 404", r.code == 404, r.String())
	}

	step("A tool request raises an approval, and answering it releases the agent")
	// A command that writes, not one that reads: the CLI classifies read-only
	// shell commands as safe and runs them without asking even in manual mode,
	// so `cat` would never produce the approval this step exists to answer.
	perm, err := dev.do("POST", "/api/sessions/"+sessionID+"/send", map[string]any{
		"message": "Use the Bash tool to run exactly: cp hello.txt copied.txt",
	})
	if err != nil || perm.code != 200 {
		check("POST /send for the tool prompt", false, fmt.Sprintf("%v %v", err, perm))
	} else {
		notif, err := dev.awaitNotification(sessionID, 120*time.Second)
		if !check("a pending notification is raised", err == nil, fmt.Sprintf("%v", err)) {
			goto teardown
		}
		notifID := str(notif, "id")
		infof("notification %s type=%s", notifID, str(notif, "type"))
		check("it is pending", str(notif, "status") == "pending",
			fmt.Sprintf("status=%q", str(notif, "status")))

		if _, ok := tap.seek(10*time.Second, func(e sseEvent) bool {
			return e.Type == "notification" && str(e.Data, "id") == notifID
		}); ok {
			check("the notification arrived over SSE", true, "")
		} else {
			check("the notification arrived over SSE", false,
				"no notification event carrying "+notifID)
		}

		act, err := dev.do("POST", "/api/notifications/"+notifID+"/action",
			map[string]any{"action": "approve"})
		if err != nil {
			check("approving it succeeds", false, err.Error())
		} else {
			check("approving it succeeds", act.code == 200, act.String())
		}

		if ev, ok := tap.seek(10*time.Second, func(e sseEvent) bool {
			return e.Type == "notification_resolved" && str(e.Data, "id") == notifID
		}); ok {
			check("the daemon announces the resolution to every client", true, "")
			check("the announcement names the device that answered",
				strings.HasPrefix(str(ev.Data, "source"), "device:"),
				fmt.Sprintf("source=%q", str(ev.Data, "source")))
			check("the announcement carries the action", str(ev.Data, "action") == "approved",
				fmt.Sprintf("action=%q", str(ev.Data, "action")))
		} else {
			check("the daemon announces the resolution to every client", false,
				"no notification_resolved for "+notifID)
		}

		// The second phone to tap Approve must be told it lost the race rather
		// than getting a silent success.
		if again, err := dev.do("POST", "/api/notifications/"+notifID+"/action",
			map[string]any{"action": "approve"}); err == nil {
			check("approving twice reports already_resolved", again.code == 410, again.String())
		}

		if notifs, err := dev.pendingNotifications(); err == nil {
			still := false
			for _, n := range notifs {
				if str(n, "id") == notifID {
					still = true
				}
			}
			check("it is gone from the pending list", !still, "still listed as pending")
		}

		if _, err := dev.awaitStatus(sessionID, 180*time.Second, "idle"); err != nil {
			check("the agent runs the tool and returns to idle", false, err.Error())
		} else {
			check("the agent runs the tool and returns to idle", true, "")
			// On disk rather than in the transcript: this is the whole point of
			// answering an approval remotely — the tool the phone released has
			// to have actually run.
			_, statErr := os.Stat(filepath.Join(cwd, "copied.txt"))
			check("the approved tool actually ran", statErr == nil,
				"copied.txt was never created in the session's cwd")
		}
	}

	// One question, one card. The PermissionRequest hook fires for
	// AskUserQuestion alongside the question hook, and raising an approval from
	// each put two cards on the phone for a single question.
	step("AskUserQuestion raises exactly one card")
	if r, err := dev.do("POST", "/api/sessions/"+sessionID+"/send", map[string]any{
		"message": "Use the AskUserQuestion tool to ask me whether to proceed, " +
			"with the options Yes and No. Ask only one question.",
	}); err != nil || r.code != 200 {
		check("POST /send for the question prompt", false, fmt.Sprintf("%v %v", err, r))
	} else {
		question, err := dev.awaitNotification(sessionID, 120*time.Second, "claude.question")
		if check("a claude.question notification is raised", err == nil, fmt.Sprintf("%v", err)) {
			questionID := str(question, "id")
			infof("question notification %s", questionID)

			// Give the permission hook every chance to raise its own card.
			time.Sleep(3 * time.Second)
			notifs, err := dev.pendingNotifications()
			if err != nil {
				check("only one card is pending for the question", false, err.Error())
			} else {
				var pending []string
				for _, n := range notifs {
					if belongsTo(n, sessionID) {
						pending = append(pending, fmt.Sprintf("%s/%s", str(n, "type"), str(n, "id")))
					}
				}
				check("only one card is pending for the question", len(pending) == 1,
					fmt.Sprintf("%d pending: %s", len(pending), strings.Join(pending, ", ")))
			}

			answer, err := dev.do("POST", "/api/notifications/"+questionID+"/action", map[string]any{
				"action":     "answer",
				"selections": []map[string]int{{"question_index": 0, "option_index": 0}},
			})
			if err != nil {
				check("answering the question is accepted", false, err.Error())
			} else if check("answering the question is accepted", answer.code == 200, answer.String()) {
				if _, ok := tap.seek(10*time.Second, func(e sseEvent) bool {
					return e.Type == "notification_resolved" && str(e.Data, "id") == questionID
				}); ok {
					check("the answered question is retracted everywhere", true, "")
				} else {
					check("the answered question is retracted everywhere", false,
						"no notification_resolved for "+questionID)
				}
				if _, err := dev.awaitStatus(sessionID, 120*time.Second, "idle"); err != nil {
					check("the agent continues after the answer", false, err.Error())
				} else {
					check("the agent continues after the answer", true, "")
				}
			}
		}
	}

	// ── Terminal WebSocket ──
	step("Attach to the terminal, the way the session screen does")
	{
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		wsURL := strings.Replace(strings.Replace(*baseURL, "https://", "wss://", 1), "http://", "ws://", 1)
		conn, _, err := websocket.Dial(ctx, wsURL+"/api/sessions/"+sessionID+"/terminal",
			&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + dev.token()}}})
		if err != nil {
			check("the terminal WebSocket connects", false, err.Error())
		} else {
			check("the terminal WebSocket connects", true, "")
			conn.SetReadLimit(-1)
			readCtx, readCancel := context.WithTimeout(ctx, 15*time.Second)
			// The handler is a byte relay, not a translator, so a viewer speaks
			// the ptyhost's frame protocol over the socket exactly as the
			// desktop does over the unix one.
			stream := websocket.NetConn(readCtx, conn, websocket.MessageBinary)
			if err := terminal.WriteJSONFrame(stream, terminal.FrameHello, terminal.Hello{
				Role: terminal.RoleObserver, Cols: 120, Rows: 40, Name: "apitest",
			}); err != nil {
				check("the viewer handshake is accepted", false, err.Error())
			} else {
				check("the viewer handshake is accepted", true, "")
			}
			f, err := terminal.ReadFrame(stream)
			if err != nil {
				check("it replays the session's screen", false, err.Error())
			} else if f.Type != terminal.FrameSnapshot {
				check("it replays the session's screen", false,
					fmt.Sprintf("first frame is %s, want snapshot", f.Type))
			} else {
				_, ansi, err := terminal.DecodeSnapshot(f.Payload)
				check("it replays the session's screen", err == nil && len(ansi) > 0,
					fmt.Sprintf("snapshot decode err=%v, %d bytes", err, len(ansi)))
				infof("snapshot %d bytes", len(ansi))
			}
			readCancel()
			conn.Close(websocket.StatusNormalClosure, "")
		}
		cancel()
	}
	{
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		wsURL := strings.Replace(strings.Replace(*baseURL, "https://", "wss://", 1), "http://", "ws://", 1)
		conn, resp, err := websocket.Dial(ctx, wsURL+"/api/sessions/"+sessionID+"/terminal", nil)
		if conn != nil {
			conn.Close(websocket.StatusNormalClosure, "")
		}
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		check("an unauthenticated terminal attach is refused", err != nil && code == 401,
			fmt.Sprintf("err=%v status=%d", err, code))
		cancel()
	}

	// ── Mutations ──
	step("Rename, permission mode, and stop")
	if r, err := dev.do("PATCH", "/api/sessions/"+sessionID,
		map[string]any{"title": "e2e probe"}); err == nil {
		if check("PATCH renames the session", r.code == 200, r.String()) {
			if sess, err := dev.session(sessionID); err == nil {
				check("the new title is persisted", str(sess, "title") == "e2e probe",
					fmt.Sprintf("title=%q", str(sess, "title")))
			}
		}
	}
	if r, err := dev.do("POST", "/api/sessions/"+sessionID+"/permission-mode",
		map[string]any{"mode": "acceptEdits"}); err == nil {
		if check("the permission mode can be changed", r.code == 200, r.String()) {
			if sess, err := dev.session(sessionID); err == nil {
				check("the new mode is persisted", str(sess, "permission_mode") == "acceptEdits",
					fmt.Sprintf("permission_mode=%q", str(sess, "permission_mode")))
			}
		}
	}
	// Stop is an interrupt, so there has to be something to interrupt: on an idle
	// session the daemon deliberately answers 409 rather than pretending. Give it
	// a running turn to cut short, which is the case a client's Stop button is for.
	if r, err := dev.do("POST", "/api/sessions/"+sessionID+"/stop", nil); err == nil {
		check("stopping an idle session is refused, not silently accepted", r.code == 409, r.String())
	}
	// Changing the mode restarts the session, so wait for it to come back before
	// prompting it — otherwise the send races the relaunch.
	if _, err := dev.awaitStatus(sessionID, 90*time.Second, "idle", "active"); err != nil {
		check("the session comes back after a permission-mode change", false, err.Error())
	} else {
		check("the session comes back after a permission-mode change", true, "")
	}
	{
		send, err := dev.do("POST", "/api/sessions/"+sessionID+"/send", map[string]any{
			"message": "Count slowly from 1 to 500, one number per line.",
		})
		if check("POST /send for the long prompt", err == nil && send.code == 200,
			fmt.Sprintf("%v %v", err, send)) {
			if _, err := dev.awaitStatus(sessionID, 60*time.Second, "active"); err != nil {
				check("the long prompt starts running", false, err.Error())
			} else if r, err := dev.do("POST", "/api/sessions/"+sessionID+"/stop", nil); err != nil {
				check("POST /stop is accepted while the agent is working", false, err.Error())
			} else if check("POST /stop is accepted while the agent is working", r.code == 200, r.String()) {
				if _, err := dev.awaitStatus(sessionID, 60*time.Second, "idle"); err != nil {
					check("the interrupted session settles back to idle", false, err.Error())
				} else {
					check("the interrupted session settles back to idle", true, "")
				}
			}
		}
	}

teardown:
	step("Terminate and delete")
	if r, err := dev.do("POST", "/api/sessions/"+sessionID+"/terminate", nil); err == nil {
		check("POST /terminate is accepted", r.code == 200, r.String())
	}
	// Nothing may outlive the session it belongs to: a pending row here is an
	// approval every tray and phone keeps counting for a session that is gone,
	// with no surface left that could ever answer it.
	time.Sleep(2 * time.Second)
	if notifs, err := dev.pendingNotifications(); err == nil {
		var leaked []string
		for _, n := range notifs {
			if belongsTo(n, sessionID) {
				leaked = append(leaked, fmt.Sprintf("%s (%s)", str(n, "id"), str(n, "type")))
			}
		}
		check("terminating the session leaves nothing pending", len(leaked) == 0,
			"still pending: "+strings.Join(leaked, ", "))
	}
	if _, err := dev.awaitStatus(sessionID, 30*time.Second, "terminated"); err != nil {
		check("the session reports terminated", false, err.Error())
	} else {
		check("the session reports terminated", true, "")
	}
	if r, err := dev.do("DELETE", "/api/sessions/"+sessionID, nil); err == nil {
		if check("DELETE removes the session", r.code == 200, r.String()) {
			if r, err := dev.do("GET", "/api/sessions/"+sessionID, nil); err == nil {
				check("it is a 404 afterwards", r.code == 404, r.String())
			}
			sessionID = ""
		}
	}

	infof("events seen: %s", strings.Join(tap.types(), ", "))
}

// cleanup makes a best effort not to leave a live agent behind on failure.
func cleanup(dev *device, sessionID string) {
	step("Cleanup")
	if _, err := dev.do("POST", "/api/sessions/"+sessionID+"/terminate", nil); err != nil {
		infof("terminate %s: %v", sessionID, err)
	}
	if _, err := dev.do("DELETE", "/api/sessions/"+sessionID, nil); err != nil {
		infof("delete %s: %v", sessionID, err)
	}
	infof("removed leftover session %s", sessionID)
}

func summarize() {
	fmt.Printf("\n\033[1m%d passed, %d failed\033[0m\n", passes, len(failures))
	for _, f := range failures {
		fmt.Println("  ✗", f)
	}
	if len(failures) > 0 {
		os.Exit(1)
	}
}
