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
	"runtime"
	"strings"
	"time"
)

// subscribeDesktopNotifications connects to the internal SSE endpoint and fires
// terminal-notifier (macOS) or notify-send (Linux) for each notification event.
// Reconnects automatically on disconnect. Exits when ctx is cancelled.
func subscribeDesktopNotifications(ctx context.Context, internalPort int) {
	bin, ok := findNotifyBinary()
	if !ok {
		return
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", internalPort)

	for {
		if ctx.Err() != nil {
			return
		}
		if err := listenSSE(ctx, baseURL, bin); err != nil {
			log.Printf("desktop-notif: SSE disconnected (%v), reconnecting in 3s", err)
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
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Load settings once per connection.
	settings := loadSettings(baseURL)

	scanner := bufio.NewScanner(resp.Body)
	var eventType, dataLine string

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataLine = strings.TrimPrefix(line, "data: ")
		case line == "":
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
		return
	}

	if !settingBool(settings, "desktop.notify.enabled", true) {
		return
	}
	if !settingBool(settings, notifAlertKey(notif.Type), true) {
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

	switch runtime.GOOS {
	case "darwin":
		sendDarwin(bin, notif.ID, notif.Type, body, notif.TmuxPane, sound)
	case "linux":
		sendLinux(bin, notif.Type, body)
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
			args = append(args, "-execute", fmt.Sprintf("%s attach %s", exe, paneID))
		}
	}
	if err := exec.Command(bin, args...).Run(); err != nil {
		log.Printf("desktop-notif: terminal-notifier: %v", err)
	}
}

func sendLinux(bin, notifType, body string) {
	urgency := "normal"
	if isBlockingNotifType(notifType) {
		urgency = "critical"
	}
	title := fmt.Sprintf("Helios — %s", notifTypeLabel(notifType))
	if err := exec.Command(bin, title, body, "--urgency="+urgency, "--app-name=Helios").Run(); err != nil {
		log.Printf("desktop-notif: notify-send: %v", err)
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
