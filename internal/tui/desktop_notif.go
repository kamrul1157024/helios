package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var notifLog *log.Logger

func init() {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".helios", "logs")
	os.MkdirAll(dir, 0755)
	f, err := os.OpenFile(filepath.Join(dir, "desktop-notif.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		notifLog = log.New(os.Stderr, "desktop-notif: ", log.LstdFlags)
		return
	}
	notifLog = log.New(f, "", log.LstdFlags)
}

// subscribeDesktopNotifications connects to the internal SSE endpoint and fires
// terminal-notifier (macOS) or notify-send (Linux) for each notification event.
// Reconnects automatically on disconnect. Exits when ctx is cancelled.
func subscribeDesktopNotifications(ctx context.Context, internalPort int) {
	bin, ok := findNotifyBinary()
	if !ok {
		notifLog.Printf("no notification binary found (tried terminal-notifier / notify-send)")
		return
	}
	notifLog.Printf("starting: bin=%s port=%d", bin, internalPort)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", internalPort)

	for {
		if ctx.Err() != nil {
			return
		}
		notifLog.Printf("connecting to %s/internal/events", baseURL)
		if err := listenSSE(ctx, baseURL, bin); err != nil {
			notifLog.Printf("SSE disconnected: %v — reconnecting in 3s", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func listenSSE(ctx context.Context, baseURL, bin string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/internal/events", nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer resp.Body.Close()
	notifLog.Printf("connected (status %d)", resp.StatusCode)

	// Load settings once per connection.
	settings := loadSettings(baseURL)
	notifLog.Printf("settings loaded: %v", settings)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1 MB buffer for large payloads
	var eventType, dataLine string

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataLine = strings.TrimPrefix(line, "data: ")
		case line == "":
			notifLog.Printf("SSE event: type=%q data=%q", eventType, truncate(dataLine, 200))
			if eventType == "notification" && dataLine != "" {
				go handleNotificationEvent(bin, dataLine, settings)
			}
			eventType = ""
			dataLine = ""
		}
	}
	return scanner.Err()
}

// loadSettings fetches notification settings from the internal API.
func loadSettings(baseURL string) map[string]string {
	resp, err := http.Get(baseURL + "/internal/settings")
	if err != nil {
		notifLog.Printf("loadSettings error: %v", err)
		return map[string]string{}
	}
	defer resp.Body.Close()
	var result struct {
		Settings map[string]string `json:"settings"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if result.Settings == nil {
		return map[string]string{}
	}
	return result.Settings
}

type sseNotification struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Title    *string `json:"title"`
	Detail   *string `json:"detail"`
	TmuxPane string  `json:"tmux_pane"` // enriched by daemon
}

func handleNotificationEvent(bin, data string, settings map[string]string) {
	var notif sseNotification
	if err := json.Unmarshal([]byte(data), &notif); err != nil {
		notifLog.Printf("unmarshal error: %v  data=%q", err, truncate(data, 200))
		return
	}
	notifLog.Printf("notification: id=%s type=%s tmux_pane=%s", notif.ID, notif.Type, notif.TmuxPane)

	if !settingBool(settings, "desktop.notify.enabled", true) {
		notifLog.Printf("skipped: desktop.notify.enabled=false")
		return
	}
	alertKey := notifAlertKey(notif.Type)
	if alertKey != "" && !settingBool(settings, alertKey, true) {
		notifLog.Printf("skipped: %s=false", alertKey)
		return
	}

	body := ""
	if notif.Detail != nil {
		body = *notif.Detail
	}
	if body == "" && notif.Title != nil {
		body = *notif.Title
	}

	sound := settingBool(settings, "desktop.notify.sound", true)
	notifLog.Printf("firing: os=%s bin=%s body=%q sound=%v pane=%s", runtime.GOOS, bin, truncate(body, 80), sound, notif.TmuxPane)

	switch runtime.GOOS {
	case "darwin":
		sendDarwin(bin, notif.ID, notif.Type, body, notif.TmuxPane, sound)
	case "linux":
		sendLinux(bin, notif.Type, body, notif.TmuxPane)
	}
}

func sendDarwin(bin, id, notifType, body, paneID string, sound bool) {
	args := []string{
		"-title", "Helios",
		"-subtitle", notifTypeLabel(notifType),
		"-message", body,
		"-group", id,
	}
	if sound {
		args = append(args, "-sound", "default")
	}
	if paneID != "" {
		exe, err := os.Executable()
		if err == nil {
			args = append(args, "-execute", openTerminalCmd(exe, paneID))
		}
	}
	notifLog.Printf("terminal-notifier args: %v", args)
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		notifLog.Printf("terminal-notifier error: %v output: %s", err, out)
	} else {
		notifLog.Printf("terminal-notifier ok output: %s", out)
	}
}

func sendLinux(bin, notifType, body, paneID string) {
	urgency := "normal"
	if isBlockingNotifType(notifType) {
		urgency = "critical"
	}
	title := fmt.Sprintf("Helios — %s", notifTypeLabel(notifType))

	args := []string{title, body, "--urgency=" + urgency, "--app-name=Helios"}

	if paneID != "" {
		// --action requires libnotify ≥0.8; if unsupported it is ignored.
		args = append(args, "--action=default=Open")
		cmd := exec.Command(bin, args...)
		out, err := cmd.Output()
		if err != nil {
			notifLog.Printf("notify-send error: %v", err)
			return
		}
		// If user clicked, output contains the action key ("default").
		if strings.TrimSpace(string(out)) == "default" {
			heliosBin, _ := os.Executable()
			termCmd := openTerminalCmd(heliosBin, paneID)
			notifLog.Printf("linux click: running %s", termCmd)
			exec.Command("sh", "-c", termCmd).Start()
		}
		return
	}

	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		notifLog.Printf("notify-send error: %v output: %s", err, out)
	}
}

// openTerminalCmd returns a shell command string that opens a new terminal
// window and runs `helios attach <paneID>`. It detects the current terminal
// from environment variables and falls back to common ones.
func openTerminalCmd(heliosBin, paneID string) string {
	attachCmd := fmt.Sprintf("%s attach %s", heliosBin, paneID)

	// select-window runs before attach, so tmux already has the right window
	// active. We just need to focus the terminal app that's running tmux.
	// Detect terminal via env vars set by the terminal itself.
	switch {
	case os.Getenv("KITTY_PID") != "":
		// Focus existing Kitty window; tmux select-window handles the rest.
		return fmt.Sprintf("%s && open -a kitty", attachCmd)
	case os.Getenv("ITERM_SESSION_ID") != "":
		return fmt.Sprintf("%s && open -a iTerm", attachCmd)
	case os.Getenv("WARP_SESSION_ID") != "":
		return fmt.Sprintf("%s && open -a Warp", attachCmd)
	}

	// Fallback: try common terminal emulators in PATH order.
	switch runtime.GOOS {
	case "darwin":
		for _, app := range []string{"kitty", "iTerm", "Warp", "WezTerm", "Alacritty"} {
			if appExists(app) {
				return fmt.Sprintf("%s && open -a '%s'", attachCmd, app)
			}
		}
		// Last resort: Terminal.app.
		return fmt.Sprintf("%s && open -a Terminal", attachCmd)

	case "linux":
		for _, t := range []struct{ bin, flag string }{
			{"kitty", ""},
			{"wezterm", "start --"},
			{"alacritty", "-e"},
			{"gnome-terminal", "--"},
			{"xterm", "-e"},
		} {
			if p, err := exec.LookPath(t.bin); err == nil {
				if t.flag != "" {
					return fmt.Sprintf("%s %s %s", p, t.flag, attachCmd)
				}
				return fmt.Sprintf("%s %s", p, attachCmd)
			}
		}
	}

	return attachCmd
}

// appExists checks if a macOS .app bundle is present in /Applications or ~/Applications.
func appExists(name string) bool {
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
		if _, err := os.Stat(filepath.Join(dir, name+".app")); err == nil {
			return true
		}
	}
	return false
}

func findNotifyBinary() (string, bool) {
	switch runtime.GOOS {
	case "darwin":
		if p, err := exec.LookPath("terminal-notifier"); err == nil {
			return p, true
		}
	case "linux":
		if p, err := exec.LookPath("notify-send"); err == nil {
			return p, true
		}
	}
	return "", false
}

func settingBool(settings map[string]string, key string, def bool) bool {
	val, ok := settings[key]
	if !ok || val == "" {
		return def
	}
	return val == "true" || val == "1"
}

func notifAlertKey(notifType string) string {
	switch {
	case notifType == "claude.permission":
		return "desktop.notify.alert.permission"
	case notifType == "claude.question":
		return "desktop.notify.alert.question"
	case strings.HasPrefix(notifType, "claude.elicitation"):
		return "desktop.notify.alert.elicitation"
	case notifType == "claude.done":
		return "desktop.notify.alert.done"
	case notifType == "claude.error":
		return "desktop.notify.alert.error"
	default:
		return ""
	}
}

func notifTypeLabel(notifType string) string {
	switch notifType {
	case "claude.permission":
		return "Permission Request"
	case "claude.question":
		return "Question"
	case "claude.elicitation.form":
		return "Input Requested"
	case "claude.elicitation.url":
		return "Authentication Required"
	case "claude.done":
		return "Session Completed"
	case "claude.error":
		return "Session Error"
	default:
		return "Notification"
	}
}

func isBlockingNotifType(notifType string) bool {
	return notifType == "claude.permission" ||
		notifType == "claude.question" ||
		strings.HasPrefix(notifType, "claude.elicitation")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
