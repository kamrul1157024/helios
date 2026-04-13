package notify

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/kamrul1157024/helios/internal/store"
	"github.com/kamrul1157024/helios/internal/tmux"
)

// ServiceStatus describes the readiness of the desktop notification service.
type ServiceStatus struct {
	Available   bool   // binary found
	Binary      string // resolved path
	Platform    string // "darwin" or "linux"
	InstallHint string // e.g. "brew install terminal-notifier"
}

// Service sends desktop notifications via native OS binaries.
// It is stateless — no goroutines, no lifecycle management.
// Each Send call is a fire-and-forget exec.Command.
type Service struct {
	db             *store.Store
	tmux           *tmux.Client
	binary         string
	platform       string
	senderBundleID string // macOS only
	available      bool
}

// New creates a new Service. Detects platform, resolves binary, detects parent terminal.
func New(db *store.Store, tc *tmux.Client) *Service {
	s := &Service{
		db:       db,
		tmux:     tc,
		platform: runtime.GOOS,
	}

	switch s.platform {
	case "darwin":
		if path, err := exec.LookPath("terminal-notifier"); err == nil {
			s.binary = path
			s.available = true
		}
		s.senderBundleID = detectTerminalBundleID()
	case "linux":
		if path, err := exec.LookPath("notify-send"); err == nil {
			s.binary = path
			s.available = true
		}
	}

	if s.available {
		log.Printf("notify: using %s (sender=%s)", s.binary, s.senderBundleID)
	} else {
		log.Printf("notify: binary not found — %s", CheckStatus().InstallHint)
	}

	return s
}

// Available returns true if the notification binary is found.
func (s *Service) Available() bool { return s.available }

// Status returns the current readiness status.
func (s *Service) Status() ServiceStatus { return checkStatusFor(s.platform) }

// CheckStatus performs a static platform check without a service instance.
func CheckStatus() ServiceStatus { return checkStatusFor(runtime.GOOS) }

func checkStatusFor(platform string) ServiceStatus {
	st := ServiceStatus{Platform: platform}
	switch platform {
	case "darwin":
		if path, err := exec.LookPath("terminal-notifier"); err == nil {
			st.Binary = path
			st.Available = true
		} else {
			st.InstallHint = "brew install terminal-notifier"
		}
	case "linux":
		if path, err := exec.LookPath("notify-send"); err == nil {
			st.Binary = path
			st.Available = true
		} else {
			st.InstallHint = "sudo apt install libnotify-bin"
		}
	default:
		st.InstallHint = "desktop notifications not supported on " + platform
	}
	return st
}

// Send delivers a desktop notification if enabled and the type is alert-enabled.
// Checks settings from DB before sending. Fire-and-forget — call in a goroutine.
func (s *Service) Send(id, notifType, title, body, sessionID, paneID string) {
	if !s.available {
		return
	}
	if !s.isEnabled() {
		return
	}
	if !s.isAlertEnabled(notifType) {
		return
	}
	sound := s.isSoundEnabled()

	switch s.platform {
	case "darwin":
		s.sendDarwin(id, notifType, body, sound, paneID)
	case "linux":
		s.sendLinux(notifType, body)
	}
}

func (s *Service) sendDarwin(id, notifType, body string, sound bool, paneID string) {
	args := []string{
		"-title", "Helios",
		"-subtitle", notifTypeLabel(notifType),
		"-message", body,
		"-group", id,
	}
	if s.senderBundleID != "" {
		args = append(args, "-sender", s.senderBundleID)
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
	if err := exec.Command(s.binary, args...).Run(); err != nil {
		log.Printf("notify: terminal-notifier error: %v", err)
	}
}

func (s *Service) sendLinux(notifType, body string) {
	urgency := "normal"
	if isBlockingType(notifType) {
		urgency = "critical"
	}
	title := fmt.Sprintf("Helios — %s", notifTypeLabel(notifType))
	if err := exec.Command(s.binary,
		title,
		body,
		"--urgency="+urgency,
		"--app-name=Helios",
	).Run(); err != nil {
		log.Printf("notify: notify-send error: %v", err)
	}
}

// ==================== Settings helpers ====================

func (s *Service) getBool(key string, defaultVal bool) bool {
	val, err := s.db.GetSetting(key)
	if err != nil || val == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return defaultVal
	}
	return b
}

func (s *Service) isEnabled() bool {
	return s.getBool("desktop.notify.enabled", true)
}

func (s *Service) isSoundEnabled() bool {
	return s.getBool("desktop.notify.sound", true)
}

func (s *Service) isAlertEnabled(notifType string) bool {
	key := alertKey(notifType)
	if key == "" {
		return true
	}
	return s.getBool(key, true)
}

func alertKey(notifType string) string {
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

// ==================== Platform helpers ====================

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

func isBlockingType(notifType string) bool {
	return notifType == "claude.permission" ||
		notifType == "claude.question" ||
		strings.HasPrefix(notifType, "claude.elicitation")
}

// detectTerminalBundleID walks the process tree to find the parent terminal
// and returns its macOS bundle ID. Falls back to com.apple.Terminal.
func detectTerminalBundleID() string {
	terminalBundleIDs := map[string]string{
		"Terminal":    "com.apple.Terminal",
		"iTerm2":      "com.googlecode.iterm2",
		"iTerm":       "com.googlecode.iterm2",
		"WezTerm":     "com.github.wez.wezterm",
		"wezterm":     "com.github.wez.wezterm",
		"Alacritty":   "org.alacritty",
		"alacritty":   "org.alacritty",
		"kitty":       "net.kovidgoyal.kitty",
		"Kitty":       "net.kovidgoyal.kitty",
		"Hyper":       "co.zeit.hyper",
		"ghostty":     "com.mitchellh.ghostty",
		"Ghostty":     "com.mitchellh.ghostty",
	}

	pid := os.Getpid()
	for i := 0; i < 10; i++ {
		out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=,comm=").Output()
		if err != nil {
			break
		}
		parts := strings.Fields(strings.TrimSpace(string(out)))
		if len(parts) < 2 {
			break
		}
		ppid, err := strconv.Atoi(parts[0])
		if err != nil {
			break
		}
		comm := parts[1]
		// Strip path component
		if idx := strings.LastIndex(comm, "/"); idx >= 0 {
			comm = comm[idx+1:]
		}
		if bundleID, ok := terminalBundleIDs[comm]; ok {
			return bundleID
		}
		pid = ppid
		if pid <= 1 {
			break
		}
	}
	return "com.apple.Terminal"
}
