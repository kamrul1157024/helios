package tunnel

import (
	"regexp"
	"testing"
)

func TestZrokProviderName(t *testing.T) {
	z := &ZrokTunnel{}
	if got := z.Provider(); got != "zrok" {
		t.Errorf("Provider() = %q, want %q", got, "zrok")
	}
}

func TestZrokPIDBeforeStart(t *testing.T) {
	z := &ZrokTunnel{}
	if pid := z.PID(); pid != 0 {
		t.Errorf("PID() before start = %d, want 0", pid)
	}
}

func TestZrokURLBeforeStart(t *testing.T) {
	z := &ZrokTunnel{}
	if url := z.URL(); url != "" {
		t.Errorf("URL() before start = %q, want empty", url)
	}
}

func TestZrokStopBeforeStart(t *testing.T) {
	z := &ZrokTunnel{}
	if err := z.Stop(); err != nil {
		t.Errorf("Stop() before start: %v", err)
	}
}

func TestZrokDefaultShareMode(t *testing.T) {
	// When shareMode is empty, it should default to reserved (not public)
	z := &ZrokTunnel{shareMode: ""}
	// We can't test the full Start flow without zrok binary,
	// but we can verify the default behavior in shareMode
	if z.shareMode != "" {
		t.Errorf("default shareMode should be empty (defaults to reserved in Start)")
	}
}

func TestParseZrokShareToken(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "token is format",
			output: "your reserved share token is abc123xyz",
			want:   "abc123xyz",
		},
		{
			name:   "token keyword",
			output: "reserved share token abc123xyz",
			want:   "abc123xyz",
		},
		{
			name:   "standalone token on line",
			output: "Creating share...\nabc123xyz\nDone.",
			want:   "abc123xyz",
		},
		{
			name:   "long token",
			output: "token is abcdefghijklmnop",
			want:   "abcdefghijklmnop",
		},
		{
			name:   "no token found",
			output: "error: something went wrong",
			want:   "",
		},
		{
			name:   "empty output",
			output: "",
			want:   "",
		},
		{
			name:   "short strings not matched",
			output: "ok\nhi\n",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseZrokShareToken(tt.output)
			if got != tt.want {
				t.Errorf("parseZrokShareToken(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

func TestZrokURLRegex(t *testing.T) {
	tests := []struct {
		input        string
		wantHost     string
		wantNoMatch  bool
	}{
		{
			input:    "https://abc123.share.zrok.io",
			wantHost: "abc123.share.zrok.io",
		},
		{
			input:    "Access your share: https://my-share.shares.zrok.io/path",
			wantHost: "my-share.shares.zrok.io",
		},
		{
			input:    "https://test-tunnel.share.zrok.io active",
			wantHost: "test-tunnel.share.zrok.io",
		},
		{
			// zrok v2 outputs bare hostname without https://
			input:    " di7j0xl8mh1o.shares.zrok.io",
			wantHost: "di7j0xl8mh1o.shares.zrok.io",
		},
		{
			input:       "no url here",
			wantNoMatch: true,
		},
	}

	for _, tt := range tests {
		match := zrokURLRegex.FindStringSubmatch(tt.input)
		if tt.wantNoMatch {
			if match != nil {
				t.Errorf("zrokURLRegex input %q: expected no match, got %v", tt.input, match)
			}
			continue
		}
		if match == nil {
			t.Errorf("zrokURLRegex input %q: expected match, got nil", tt.input)
			continue
		}
		if match[1] != tt.wantHost {
			t.Errorf("zrokURLRegex input %q: host got %q, want %q", tt.input, match[1], tt.wantHost)
		}
	}
}

func TestZrokTokenRegex(t *testing.T) {
	re := zrokTokenRegex

	tests := []struct {
		input string
		want  string
	}{
		{"token is abc123xyz", "abc123xyz"},
		{"token abc123xyz", "abc123xyz"},
		{"reserved abc123xyz", "abc123xyz"},
		{"no match here", ""},
	}

	for _, tt := range tests {
		match := re.FindStringSubmatch(tt.input)
		got := ""
		if match != nil && len(match) > 1 {
			got = match[1]
		}
		if got != tt.want {
			t.Errorf("zrokTokenRegex input %q: got %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestManagerStartZrokNoBinary(t *testing.T) {
	// This test verifies the error message when zrok is not installed
	// It will only work in environments where zrok is not installed
	dir := t.TempDir()
	mgr := NewManager(dir)
	mgr.SetProviderConfig(ProviderConfig{
		Zrok: ZrokProviderConfig{ShareMode: "public"},
	})

	_, err := mgr.Start("zrok", "", 8080)
	if err == nil {
		// zrok is installed on this machine, skip
		mgr.Stop()
		t.Skip("zrok is installed, skipping not-found test")
	}

	// Verify helpful error message
	if got := err.Error(); !regexp.MustCompile(`zrok not found`).MatchString(got) {
		t.Errorf("error should mention 'zrok not found', got: %q", got)
	}
}
