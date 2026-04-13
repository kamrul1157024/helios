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
	"strconv"
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

	if settingBool(settings, "desktop.notify.suppress_focused", false) && isUserFocusedOnPane(notif.TmuxPane) {
		notifLog.Printf("skipped: user focused on pane %s", notif.TmuxPane)
		return
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
		args = append(args, "-execute", focusTerminalCmd(paneID))
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
			notifLog.Printf("linux click: focusing pane %s", paneID)
			focusPane(paneID)
		}
		return
	}

	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		notifLog.Printf("notify-send error: %v output: %s", err, out)
	}
}

// paneClient holds information about the tmux client watching a pane.
type paneClient struct {
	tty string // e.g. /dev/ttys000
	pid int    // tmux client pid
	app string // host app name (e.g. "kitty", "Code")
}

// findPaneClient returns the tmux client currently focused on paneID, or nil
// if no client is watching it.
func findPaneClient(paneID string) *paneClient {
	if paneID == "" {
		return nil
	}
	out, err := exec.Command("tmux", "list-clients", "-F", "#{pane_id} #{client_tty} #{client_pid}").Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != paneID {
			continue
		}
		pid, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		app := appFromPID(pid)
		notifLog.Printf("findPaneClient: pane=%s tty=%s pid=%d app=%s", paneID, fields[1], pid, app)
		return &paneClient{tty: fields[1], pid: pid, app: app}
	}
	return nil
}

// appFromPID walks up the process tree from a tmux client PID to find the
// hosting application name (e.g. "kitty", "Code").
func appFromPID(pid int) string {
	// Walk up one level — tmux client's parent is the terminal/editor process.
	ppid := parentPID(pid)
	if ppid <= 1 {
		return ""
	}
	comm := processComm(ppid)
	if comm == "" {
		return ""
	}
	// Extract the innermost .app bundle name from the path on macOS.
	if runtime.GOOS == "darwin" {
		// e.g. ".../kitty.app/Contents/MacOS/kitty" → "kitty"
		// e.g. ".../Visual Studio Code 2.app/Contents/..." → "Visual Studio Code 2"
		parts := strings.Split(comm, "/")
		for _, part := range parts {
			if strings.HasSuffix(part, ".app") {
				return strings.TrimSuffix(part, ".app")
			}
		}
	}
	// On Linux or fallback: use the executable basename.
	return filepath.Base(comm)
}

// parentPID returns the PPID of the given pid, or 0 on error.
func parentPID(pid int) int {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
	if err != nil {
		return 0
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return ppid
}

// processComm returns the full executable path of a pid.
func processComm(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// isUserFocusedOnPane returns true if a tmux client is focused on paneID and
// the app hosting that client is the frontmost window.
func isUserFocusedOnPane(paneID string) bool {
	client := findPaneClient(paneID)
	if client == nil {
		return false
	}
	return isAppFrontmost(client.app)
}

// isAppFrontmost returns true if the named app is the frontmost window.
// On macOS uses osascript; on Linux uses xdotool (X11) with a best-effort fallback.
func isAppFrontmost(app string) bool {
	if app == "" {
		return false
	}
	switch runtime.GOOS {
	case "darwin":
		return darwinIsAppFrontmost(app)
	case "linux":
		return linuxIsAppFrontmost(app)
	}
	return false
}

func darwinIsAppFrontmost(app string) bool {
	// Get the bundle identifier of the frontmost app.
	frontmost, err := exec.Command("osascript", "-e",
		`tell application "System Events" to get bundle identifier of first application process whose frontmost is true`,
	).Output()
	if err != nil {
		return false
	}
	frontmostID := strings.TrimSpace(string(frontmost))

	// Get the bundle identifier of the target app by name.
	targetID, err := exec.Command("osascript", "-e",
		fmt.Sprintf(`id of app "%s"`, app),
	).Output()
	if err != nil {
		// Fallback: compare app name directly against frontmost process name.
		name, err2 := exec.Command("osascript", "-e",
			`tell application "System Events" to get name of first application process whose frontmost is true`,
		).Output()
		if err2 != nil {
			return false
		}
		return strings.EqualFold(strings.TrimSpace(string(name)), app)
	}

	return strings.TrimSpace(string(targetID)) == frontmostID
}

func linuxIsAppFrontmost(app string) bool {
	// Try xdotool (X11).
	out, err := exec.Command("xdotool", "getactivewindow", "getwindowname").Output()
	if err == nil {
		return strings.Contains(strings.ToLower(string(out)), strings.ToLower(app))
	}
	// Try xprop (X11 fallback).
	activeWin, err := exec.Command("sh", "-c",
		`xprop -root _NET_ACTIVE_WINDOW | awk '{print $NF}'`,
	).Output()
	if err == nil {
		wmClass, err := exec.Command("xprop", "-id", strings.TrimSpace(string(activeWin)), "WM_CLASS").Output()
		if err == nil {
			return strings.Contains(strings.ToLower(string(wmClass)), strings.ToLower(app))
		}
	}
	// Wayland: no universal method, skip app check — pane match is enough.
	return true
}

// focusPane switches the tmux client watching paneID to that pane and brings
// the hosting app to the foreground.
func focusPane(paneID string) {
	client := findPaneClient(paneID)

	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		tmuxBin = "tmux"
	}

	if client != nil {
		// Target the specific client that owns this pane.
		exec.Command(tmuxBin, "switch-client", "-c", client.tty, "-t", paneID).Run()
	} else {
		// No client currently watching the pane — switch any available client.
		exec.Command(tmuxBin, "select-window", "-t", paneID).Run()
		exec.Command(tmuxBin, "select-pane", "-t", paneID).Run()
	}

	activateApp(client)
}

// focusTerminalCmd returns a shell command string that focuses the pane.
// Used as the -execute argument for terminal-notifier on macOS.
func focusTerminalCmd(paneID string) string {
	// We resolve the client at notification-fire time so the command embedded
	// in the notification banner is as accurate as possible.
	client := findPaneClient(paneID)

	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		tmuxBin = "tmux"
	}

	var switchCmd string
	if client != nil {
		switchCmd = fmt.Sprintf("%s switch-client -c '%s' -t '%s'", tmuxBin, client.tty, paneID)
	} else {
		switchCmd = fmt.Sprintf("%s select-window -t '%s' && %s select-pane -t '%s'", tmuxBin, paneID, tmuxBin, paneID)
	}

	app := ""
	if client != nil {
		app = client.app
	}

	switch runtime.GOOS {
	case "darwin":
		if app != "" {
			return fmt.Sprintf("%s && open -a '%s'", switchCmd, app)
		}
	case "linux":
		if app != "" {
			if _, err := exec.LookPath("wmctrl"); err == nil {
				return fmt.Sprintf("%s && wmctrl -a '%s'", switchCmd, app)
			}
			if _, err := exec.LookPath("xdotool"); err == nil {
				return fmt.Sprintf("%s && xdotool search --name '%s' windowactivate", switchCmd, app)
			}
		}
	}
	return switchCmd
}

// activateApp brings the app hosting the given client to the foreground.
func activateApp(client *paneClient) {
	if client == nil || client.app == "" {
		return
	}
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", "-a", client.app).Run()
	case "linux":
		if _, err := exec.LookPath("wmctrl"); err == nil {
			exec.Command("wmctrl", "-a", client.app).Run()
		} else if _, err := exec.LookPath("xdotool"); err == nil {
			exec.Command("xdotool", "search", "--name", client.app, "windowactivate").Run()
		}
	}
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
