package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kamrul1157024/helios/internal/store"
)

// Each schedule keeps its own log.
//
// The daemon log is one line per decision, which is the wrong grain for "what
// has this monitor been seeing all week": a five-minute check writes 288 blocks
// a day and would bury everything else. So a schedule's checks and fires go to
// a file of its own, with the whole of the output rather than the 4 KB the row
// keeps for the UI.
const (
	scheduleLogMax = 5 << 20 // rotated once past this, so a monitor cannot fill a disk
	scheduleLogDir = "schedules"
)

// ScheduleLogDir is where a schedule's own log lives, under the daemon's log
// directory. Injected rather than computed so the daemon owns the path and the
// tests can point it somewhere harmless.
var ScheduleLogDir = ""

func scheduleLogPath(id string) string {
	if ScheduleLogDir == "" {
		return ""
	}
	return filepath.Join(ScheduleLogDir, id+".log")
}

// logf appends one timestamped block to a schedule's log.
func (s *Scheduler) logf(sc *store.Schedule, format string, args ...interface{}) {
	AppendScheduleLog(sc.ID, fmt.Sprintf(format, args...))
}

// AppendScheduleLog writes a line, stamped, and rotates the file when it has
// grown past its cap.
func AppendScheduleLog(id, line string) {
	path := scheduleLogPath(id)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Size() > scheduleLogMax {
		// One generation kept. Two would be a retention policy, and nobody
		// reads a check log from a fortnight ago.
		_ = os.Rename(path, path+".1")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("15:04:05"), line)
}

// TailScheduleLog returns the last n lines of a schedule's log.
func TailScheduleLog(id string, n int) ([]string, error) {
	path := scheduleLogPath(id)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	content := strings.TrimRight(string(data), "\n")
	if content == "" {
		return []string{}, nil
	}
	lines := strings.Split(content, "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// RemoveScheduleLog takes the log with the schedule it belonged to.
func RemoveScheduleLog(id string) {
	if path := scheduleLogPath(id); path != "" {
		_ = os.Remove(path)
		_ = os.Remove(path + ".1")
	}
}
