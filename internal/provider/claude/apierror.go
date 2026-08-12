package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"
)

// apiErrorScanLimit caps how far back from the end of a transcript lastAPIError
// looks. The failure is written milliseconds before the hook fires, so it is
// always within the last few entries; the limit keeps a large transcript from
// being walked twice for nothing.
const apiErrorScanLimit = 20

// usageLimitMarker is how Claude reports a usage limit. It is followed by
// "|<unix epoch>" when a reset time is known.
const usageLimitMarker = "Claude AI usage limit reached"

// apiErrorEntry is the subset of a transcript line that identifies an error.
type apiErrorEntry struct {
	IsAPIErrorMessage bool `json:"isApiErrorMessage"`
	Message           struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// lastAPIError returns the error text Claude recorded for the turn that just
// failed, or "" when the transcript has none.
//
// The StopFailure payload does not carry the reason, so the transcript is the
// only place it exists. Entries are scanned from the end because the failure is
// always the last thing written. A missing or unreadable file yields "": an
// error notification with no detail beats a failed hook.
func lastAPIError(transcriptPath string) string {
	if transcriptPath == "" {
		return ""
	}
	f, err := os.Open(transcriptPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	// Keep only the tail in memory — transcripts run to megabytes.
	tail := make([]string, 0, apiErrorScanLimit)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if len(tail) == apiErrorScanLimit {
			tail = append(tail[1:], line)
			continue
		}
		tail = append(tail, line)
	}
	if scanner.Err() != nil {
		return ""
	}

	for i := len(tail) - 1; i >= 0; i-- {
		var entry apiErrorEntry
		if err := json.Unmarshal([]byte(tail[i]), &entry); err != nil {
			continue
		}
		if !entry.IsAPIErrorMessage {
			continue
		}
		var parts []string
		for _, block := range entry.Message.Content {
			if block.Type == "text" && block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

// RateLimitInfo describes a usage-limit error and when it lifts.
type RateLimitInfo struct {
	IsRateLimit bool
	// ResetAt is nil when the error carries no reset time. An unknown window is
	// not the same as no window, and callers must not treat it as "retry now".
	ResetAt *time.Time
}

// classifyAPIError reports whether an error is a usage limit and when it
// clears. Claude formats it as "Claude AI usage limit reached|<unix epoch>".
func classifyAPIError(text string) RateLimitInfo {
	if text == "" {
		return RateLimitInfo{}
	}

	idx := strings.Index(text, usageLimitMarker)
	if idx < 0 {
		// Older or differently-worded limits still say so in plain words.
		lower := strings.ToLower(text)
		if strings.Contains(lower, "rate limit") || strings.Contains(lower, "usage limit") {
			return RateLimitInfo{IsRateLimit: true}
		}
		return RateLimitInfo{}
	}

	rest := text[idx+len(usageLimitMarker):]
	if !strings.HasPrefix(rest, "|") {
		return RateLimitInfo{IsRateLimit: true}
	}
	epoch := strings.TrimPrefix(rest, "|")
	if cut := strings.IndexAny(epoch, " \t\n\r"); cut >= 0 {
		epoch = epoch[:cut]
	}
	secs, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil || secs <= 0 {
		return RateLimitInfo{IsRateLimit: true}
	}
	reset := time.Unix(secs, 0).UTC()
	return RateLimitInfo{IsRateLimit: true, ResetAt: &reset}
}
