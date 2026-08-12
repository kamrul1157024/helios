package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTranscript writes lines to a .jsonl file and returns its path.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// apiErrorLine builds the shape Claude writes for a failed turn.
func apiErrorLine(text string) string {
	line, err := json.Marshal(map[string]interface{}{
		"type":              "assistant",
		"isApiErrorMessage": true,
		"message": map[string]interface{}{
			"role":    "assistant",
			"content": []map[string]string{{"type": "text", "text": text}},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(line)
}

func assistantLine(text string) string {
	line, err := json.Marshal(map[string]interface{}{
		"type": "assistant",
		"message": map[string]interface{}{
			"role":    "assistant",
			"content": []map[string]string{{"type": "text", "text": text}},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(line)
}

// ==================== lastAPIError ====================

func TestLastAPIError_ReturnsFinalErrorText(t *testing.T) {
	const want = "API Error: Response stalled mid-stream. The response above may be incomplete."
	path := writeTranscript(t,
		assistantLine("working on it"),
		apiErrorLine(want),
	)

	if got := lastAPIError(path); got != want {
		t.Errorf("lastAPIError = %q, want %q", got, want)
	}
}

func TestLastAPIError_PrefersTheMostRecentError(t *testing.T) {
	path := writeTranscript(t,
		apiErrorLine("older error"),
		assistantLine("recovered"),
		apiErrorLine("newer error"),
	)

	if got := lastAPIError(path); got != "newer error" {
		t.Errorf("lastAPIError = %q, want the newer error", got)
	}
}

func TestLastAPIError_NoErrorEntry(t *testing.T) {
	path := writeTranscript(t, assistantLine("all good"))

	if got := lastAPIError(path); got != "" {
		t.Errorf("lastAPIError = %q, want empty", got)
	}
}

// A hook that panics on a rotated or deleted transcript is worse than one that
// posts a notification with no detail.
func TestLastAPIError_MissingFile(t *testing.T) {
	if got := lastAPIError(filepath.Join(t.TempDir(), "nope.jsonl")); got != "" {
		t.Errorf("lastAPIError = %q, want empty", got)
	}
	if got := lastAPIError(""); got != "" {
		t.Errorf("lastAPIError(\"\") = %q, want empty", got)
	}
}

func TestLastAPIError_SkipsUnparseableLines(t *testing.T) {
	path := writeTranscript(t,
		apiErrorLine("the error"),
		"{not json at all",
		"",
	)

	if got := lastAPIError(path); got != "the error" {
		t.Errorf("lastAPIError = %q, want %q", got, "the error")
	}
}

func TestLastAPIError_StopsAtScanLimit(t *testing.T) {
	lines := []string{apiErrorLine("too far back")}
	for i := 0; i < apiErrorScanLimit; i++ {
		lines = append(lines, assistantLine(fmt.Sprintf("filler %d", i)))
	}
	path := writeTranscript(t, lines...)

	if got := lastAPIError(path); got != "" {
		t.Errorf("lastAPIError = %q, want empty beyond the scan limit", got)
	}
}

// ==================== classifyAPIError ====================

func TestClassifyAPIError_UsageLimitWithEpoch(t *testing.T) {
	info := classifyAPIError("Claude AI usage limit reached|1754899200")

	if !info.IsRateLimit {
		t.Fatal("IsRateLimit = false, want true")
	}
	if info.ResetAt == nil {
		t.Fatal("ResetAt = nil, want a parsed time")
	}
	if got := info.ResetAt.Unix(); got != 1754899200 {
		t.Errorf("ResetAt = %d, want 1754899200", got)
	}
}

func TestClassifyAPIError_UsageLimitWithoutEpoch(t *testing.T) {
	for _, text := range []string{
		"Claude AI usage limit reached",
		"Claude AI usage limit reached|",
		"Claude AI usage limit reached|not-a-number",
	} {
		info := classifyAPIError(text)
		if !info.IsRateLimit {
			t.Errorf("%q: IsRateLimit = false, want true", text)
		}
		// An unknown window must not become a fabricated one.
		if info.ResetAt != nil {
			t.Errorf("%q: ResetAt = %v, want nil", text, info.ResetAt)
		}
	}
}

func TestClassifyAPIError_PlainRateLimitWording(t *testing.T) {
	info := classifyAPIError("Error: rate limit exceeded, please slow down")

	if !info.IsRateLimit {
		t.Error("IsRateLimit = false, want true")
	}
	if info.ResetAt != nil {
		t.Errorf("ResetAt = %v, want nil", info.ResetAt)
	}
}

func TestClassifyAPIError_TransientErrorsAreNotRateLimits(t *testing.T) {
	for _, text := range []string{
		"API Error: Response stalled mid-stream. The response above may be incomplete.",
		"API Error: Stream idle timeout - no chunks received",
		"API Error: Connection to the API was lost (ECONNRESET). This is usually temporary — try again.",
		"",
	} {
		if info := classifyAPIError(text); info.IsRateLimit {
			t.Errorf("%q: IsRateLimit = true, want false", text)
		}
	}
}

func TestClassifyAPIError_ResetIsUTC(t *testing.T) {
	info := classifyAPIError("Claude AI usage limit reached|1754899200")
	if info.ResetAt == nil {
		t.Fatal("ResetAt = nil")
	}
	if _, offset := info.ResetAt.Zone(); offset != 0 {
		t.Errorf("ResetAt zone offset = %d, want UTC", offset)
	}
	// The card parses this back out of the payload.
	if _, err := time.Parse(time.RFC3339, info.ResetAt.Format(time.RFC3339)); err != nil {
		t.Errorf("ResetAt does not round-trip through RFC3339: %v", err)
	}
}
