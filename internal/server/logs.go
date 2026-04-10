package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LogsDir is set by daemon to avoid import cycle.
var LogsDir string

func logsDir() string {
	if LogsDir != "" {
		return LogsDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".helios", "logs")
}

type deviceLogEntry struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Context string `json:"context,omitempty"`
}

type deviceLogRequest struct {
	Logs []deviceLogEntry `json:"logs"`
}

// handleDeviceLogs receives log entries from a device (frontend) and appends to a per-device log file.
func (s *PublicServer) handleDeviceLogs(w http.ResponseWriter, r *http.Request) {
	kid, ok := r.Context().Value(deviceKIDKey).(string)
	if !ok {
		jsonError(w, "missing device context", http.StatusUnauthorized)
		return
	}

	var req deviceLogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Logs) == 0 {
		jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})
		return
	}

	dir := logsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		jsonError(w, "failed to create logs dir", http.StatusInternalServerError)
		return
	}

	// Sanitize kid for filename
	safeKID := strings.ReplaceAll(kid, "/", "_")
	safeKID = strings.ReplaceAll(safeKID, "..", "_")
	logPath := filepath.Join(dir, fmt.Sprintf("device-%s.log", safeKID))

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		jsonError(w, "failed to open log file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	for _, entry := range req.Logs {
		line := fmt.Sprintf("%s [%s] %s", now, entry.Level, entry.Message)
		if entry.Context != "" {
			line += " | " + entry.Context
		}
		f.WriteString(line + "\n")
	}

	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"written": len(req.Logs),
	})
}

// handleInternalLogs returns combined daemon + device logs for the CLI.
func (s *InternalServer) handleInternalLogs(w http.ResponseWriter, r *http.Request) {
	tail := 50
	if t := r.URL.Query().Get("tail"); t != "" {
		if n, err := fmt.Sscanf(t, "%d", &tail); n == 1 && err == nil && tail > 0 {
			// use parsed value
		} else {
			tail = 50
		}
	}

	source := r.URL.Query().Get("source") // "daemon", "device", or "" (all)

	dir := logsDir()
	result := map[string]interface{}{}

	// Daemon logs
	if source == "" || source == "daemon" {
		daemonPath := filepath.Join(dir, "daemon.log")
		lines, err := tailFile(daemonPath, tail)
		if err != nil {
			result["daemon"] = []string{}
		} else {
			result["daemon"] = lines
		}
	}

	// Device logs
	if source == "" || source == "device" {
		deviceLogs := map[string][]string{}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "device-") && strings.HasSuffix(e.Name(), ".log") {
				kid := strings.TrimPrefix(e.Name(), "device-")
				kid = strings.TrimSuffix(kid, ".log")
				lines, err := tailFile(filepath.Join(dir, e.Name()), tail)
				if err == nil {
					deviceLogs[kid] = lines
				}
			}
		}
		result["devices"] = deviceLogs
	}

	jsonResponse(w, http.StatusOK, result)
}

// tailFile reads the last n lines from a file.
func tailFile(path string, n int) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := strings.TrimRight(string(data), "\n")
	if content == "" {
		return []string{}, nil
	}

	lines := strings.Split(content, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
