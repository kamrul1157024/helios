// Command apiprobe creates one session and dumps what its terminal is showing,
// so a session stuck in "starting" can be diagnosed. Scratch tooling.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/kamrul1157024/helios/internal/terminal"
)

var (
	baseURL = flag.String("base", "http://localhost:7655", "public API base URL")
	keyPath = flag.String("key", "/tmp/helios-apitest-key.json", "cached device key")
	cwd     = flag.String("cwd", "", "session cwd (default: a fresh temp dir)")
	prompt  = flag.String("prompt", "Reply with exactly PROBEOK and nothing else.", "initial prompt")
	watch   = flag.Duration("watch", 25*time.Second, "how long to let it run before dumping the screen")
	keep    = flag.Bool("keep", false, "leave the session running")
	attach  = flag.String("attach", "", "attach to an existing session id instead of creating one")
	mode    = flag.String("mode", "", "permission mode for a new session (default: the launch default)")
)

type device struct {
	KID  string `json:"kid"`
	Seed string `json:"seed"`
	priv ed25519.PrivateKey
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func load() (*device, error) {
	raw, err := os.ReadFile(*keyPath)
	if err != nil {
		return nil, err
	}
	var d device
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	seed, err := base64.RawURLEncoding.DecodeString(d.Seed)
	if err != nil {
		return nil, err
	}
	d.priv = ed25519.NewKeyFromSeed(seed)
	return &d, nil
}

func (d *device) token() string {
	header, _ := json.Marshal(map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": d.KID})
	now := time.Now().Unix()
	payload, _ := json.Marshal(map[string]any{"iat": now, "exp": now + 3600, "sub": "helios-client"})
	input := b64(header) + "." + b64(payload)
	return input + "." + b64(ed25519.Sign(d.priv, []byte(input)))
}

func (d *device) do(method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, *baseURL+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+d.token())
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	return resp.StatusCode, out, err
}

// pending returns the notifications still awaiting an answer for sessionID.
func (d *device) pending(sessionID string) []map[string]any {
	_, body, err := d.do("GET", "/api/notifications", nil)
	if err != nil {
		return nil
	}
	var parsed struct {
		Notifications []map[string]any `json:"notifications"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	var out []map[string]any
	for _, n := range parsed.Notifications {
		if n["source_session"] == sessionID && n["status"] == "pending" {
			out = append(out, n)
		}
	}
	return out
}

func main() {
	flag.Parse()
	dev, err := load()
	if err != nil {
		fmt.Println("load device key:", err, "— run apitest once first")
		os.Exit(1)
	}

	id := *attach
	if id == "" {
		dir := *cwd
		if dir == "" {
			dir, err = os.MkdirTemp("", "helios-probe-")
			if err != nil {
				panic(err)
			}
			if err := os.WriteFile(dir+"/hello.txt", []byte("helios probe\n"), 0o644); err != nil {
				panic(err)
			}
		}
		req := map[string]any{"provider": "claude", "cwd": dir, "prompt": *prompt}
		if *mode != "" {
			req["permission_mode"] = *mode
		}
		code, body, err := dev.do("POST", "/api/sessions", req)
		if err != nil || code != 200 {
			fmt.Printf("create failed: %d %s %v\n", code, body, err)
			os.Exit(1)
		}
		var created map[string]any
		_ = json.Unmarshal(body, &created)
		id, _ = created["session_id"].(string)
		fmt.Printf("created %s in %s\n", id, dir)
	}

	deadline := time.Now().Add(*watch)
	for time.Now().Before(deadline) {
		code, body, err := dev.do("GET", "/api/sessions/"+id, nil)
		if err == nil {
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			if inner, ok := m["session"].(map[string]any); ok {
				m = inner
			}
			fmt.Printf("  t+%02.0fs http=%d status=%v last_event=%v mode=%v\n",
				time.Until(deadline).Seconds(), code, m["status"], m["last_event"],
				m["permission_mode"])
		}
		for _, n := range dev.pending(id) {
			fmt.Printf("        notif %v type=%v title=%v\n", n["id"], n["type"], n["title"])
			// A new directory is untrusted and the agent is blocked until someone
			// says so; answer it the way a client would rather than hang here.
			if n["type"] == "claude.trust" {
				c, b, _ := dev.do("POST", "/api/notifications/"+fmt.Sprint(n["id"])+"/action",
					map[string]any{"action": "trust"})
				fmt.Printf("        → trusted (%d %s)\n", c, b)
			}
		}
		time.Sleep(5 * time.Second)
	}

	fmt.Println("\n=== notifications ===")
	_, notifs, _ := dev.do("GET", "/api/notifications", nil)
	var parsed struct {
		Notifications []map[string]any `json:"notifications"`
	}
	_ = json.Unmarshal(notifs, &parsed)
	for _, n := range parsed.Notifications {
		if n["source_session"] == id {
			fmt.Printf("  %v type=%v status=%v title=%v\n", n["id"], n["type"], n["status"], n["title"])
		}
	}

	fmt.Println("\n=== terminal screen ===")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	wsURL := strings.Replace(strings.Replace(*baseURL, "https://", "wss://", 1), "http://", "ws://", 1)
	conn, _, err := websocket.Dial(ctx, wsURL+"/api/sessions/"+id+"/terminal",
		&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + dev.token()}}})
	if err != nil {
		fmt.Println("attach failed:", err)
	} else {
		conn.SetReadLimit(-1)
		readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
		stream := websocket.NetConn(readCtx, conn, websocket.MessageBinary)

		// The WebSocket handler is a byte relay, not a translator, so a viewer
		// speaks the ptyhost's frame protocol over it exactly as the desktop
		// does over the unix socket.
		if err := terminal.WriteJSONFrame(stream, terminal.FrameHello, terminal.Hello{
			Role: terminal.RoleObserver, Cols: 120, Rows: 40, Name: "apiprobe",
		}); err != nil {
			fmt.Println("hello:", err)
		}

		var screen bytes.Buffer
		for i := 0; i < 8; i++ {
			f, err := terminal.ReadFrame(stream)
			if err != nil {
				fmt.Printf("(stream ended after %d bytes: %v)\n", screen.Len(), err)
				break
			}
			switch f.Type {
			case terminal.FrameSnapshot:
				seq, ansi, err := terminal.DecodeSnapshot(f.Payload)
				if err != nil {
					fmt.Println("bad snapshot:", err)
					continue
				}
				fmt.Printf("(snapshot seq=%d, %d bytes)\n", seq, len(ansi))
				screen.Write(ansi)
			case terminal.FrameOutput:
				screen.Write(f.Payload)
			case terminal.FrameStatus:
				fmt.Printf("(status %s)\n", f.Payload)
			default:
				fmt.Printf("(frame %s, %d bytes)\n", f.Type, len(f.Payload))
			}
			if screen.Len() > 0 && f.Type == terminal.FrameStatus {
				i = 8 // snapshot and status both seen; that is the handshake
			}
		}
		readCancel()
		conn.Close(websocket.StatusNormalClosure, "")
		fmt.Println(visible(screen.String()))
	}

	if !*keep && *attach == "" {
		if _, _, err := dev.do("POST", "/api/sessions/"+id+"/terminate", nil); err != nil {
			fmt.Println("terminate:", err)
		}
		if _, _, err := dev.do("DELETE", "/api/sessions/"+id, nil); err != nil {
			fmt.Println("delete:", err)
		}
		fmt.Println("\ncleaned up", id)
	} else {
		fmt.Println("\nleft running:", id)
	}
}

// visible strips escape sequences so the screen is readable in a log.
func visible(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			// Skip CSI / OSC until a terminator.
			j := i + 1
			if j < len(s) && (s[j] == '[' || s[j] == ']' || s[j] == '(') {
				for j < len(s) && !strings.ContainsRune("@ABCDEFGHJKSTfmnsulhpqrt\a\\", rune(s[j])) {
					j++
				}
			}
			i = j
			continue
		}
		if c == '\r' {
			continue
		}
		if c >= 0x20 || c == '\n' || c == '\t' {
			out.WriteByte(c)
		}
	}
	// Collapse the runs of blank lines a TUI redraw leaves behind.
	lines := strings.Split(out.String(), "\n")
	var kept []string
	blank := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		kept = append(kept, strings.TrimRight(l, " "))
	}
	return strings.Join(kept, "\n")
}
