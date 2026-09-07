package server

import (
	"testing"
	"time"
)

func TestHeartbeatInterval(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"absent falls back to the in-app rate", "", 30 * time.Second},
		{"garbage falls back rather than failing the stream", "soon", 30 * time.Second},
		{"a background client gets the slow rate it asked for", "240", 240 * time.Second},
		{"too fast is clamped up", "1", 5 * time.Second},
		{"too slow is clamped down", "86400", 10 * time.Minute},
		{"negative is clamped up", "-30", 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := heartbeatInterval(tt.raw); got != tt.want {
				t.Errorf("heartbeatInterval(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
