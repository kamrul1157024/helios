package tunnel

import (
	"testing"
)

func TestLocalhostRunProviderName(t *testing.T) {
	lhr := &LocalhostRunTunnel{}
	if got := lhr.Provider(); got != "localhostrun" {
		t.Errorf("Provider() = %q, want %q", got, "localhostrun")
	}
}

func TestLocalhostRunPIDBeforeStart(t *testing.T) {
	lhr := &LocalhostRunTunnel{}
	if pid := lhr.PID(); pid != 0 {
		t.Errorf("PID() before start = %d, want 0", pid)
	}
}

func TestLocalhostRunURLBeforeStart(t *testing.T) {
	lhr := &LocalhostRunTunnel{}
	if url := lhr.URL(); url != "" {
		t.Errorf("URL() before start = %q, want empty", url)
	}
}

func TestLocalhostRunStopBeforeStart(t *testing.T) {
	lhr := &LocalhostRunTunnel{}
	if err := lhr.Stop(); err != nil {
		t.Errorf("Stop() before start: %v", err)
	}
}

func TestLocalhostRunBuildRemoteSpec(t *testing.T) {
	tests := []struct {
		name         string
		tunnel       *LocalhostRunTunnel
		port         int
		wantSpec     string
	}{
		{
			name:     "free tier",
			tunnel:   &LocalhostRunTunnel{},
			port:     7655,
			wantSpec: "80:localhost:7655",
		},
		{
			name:     "custom domain",
			tunnel:   &LocalhostRunTunnel{customDomain: "myapp.lhr.rocks"},
			port:     8080,
			wantSpec: "myapp.lhr.rocks:80:localhost:8080",
		},
		{
			name:     "own domain",
			tunnel:   &LocalhostRunTunnel{customDomain: "tunnel.example.com"},
			port:     3000,
			wantSpec: "tunnel.example.com:80:localhost:3000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tunnel.buildRemoteSpec(tt.port)
			if got != tt.wantSpec {
				t.Errorf("buildRemoteSpec(%d) = %q, want %q", tt.port, got, tt.wantSpec)
			}
		})
	}
}

func TestLocalhostRunBuildSSHArgs(t *testing.T) {
	tests := []struct {
		name     string
		tunnel   *LocalhostRunTunnel
		port     int
		wantLast string // last arg should be localhost.run or user@localhost.run
		wantR    string // the -R value
	}{
		{
			name:     "default (anonymous)",
			tunnel:   &LocalhostRunTunnel{},
			port:     7655,
			wantLast: "localhost.run",
			wantR:    "80:localhost:7655",
		},
		{
			name:     "explicit nokey user",
			tunnel:   &LocalhostRunTunnel{sshUser: "nokey"},
			port:     8080,
			wantLast: "nokey@localhost.run",
			wantR:    "80:localhost:8080",
		},
		{
			name:     "plan user with custom domain",
			tunnel:   &LocalhostRunTunnel{sshUser: "plan", customDomain: "myapp.lhr.rocks"},
			port:     7655,
			wantLast: "plan@localhost.run",
			wantR:    "myapp.lhr.rocks:80:localhost:7655",
		},
		{
			name:     "custom keepalive",
			tunnel:   &LocalhostRunTunnel{keepalive: 120},
			port:     8080,
			wantLast: "localhost.run",
			wantR:    "80:localhost:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.tunnel.buildSSHArgs(tt.port)

			// Last arg should be user@localhost.run
			last := args[len(args)-1]
			if last != tt.wantLast {
				t.Errorf("last arg: got %q, want %q", last, tt.wantLast)
			}

			// Find -R value
			for i, arg := range args {
				if arg == "-R" && i+1 < len(args) {
					if args[i+1] != tt.wantR {
						t.Errorf("-R value: got %q, want %q", args[i+1], tt.wantR)
					}
					break
				}
			}

			// Should contain StrictHostKeyChecking
			found := false
			for _, arg := range args {
				if arg == "StrictHostKeyChecking=accept-new" {
					found = true
					break
				}
			}
			if !found {
				t.Error("args should contain StrictHostKeyChecking=accept-new")
			}

			// Should contain ExitOnForwardFailure
			found = false
			for _, arg := range args {
				if arg == "ExitOnForwardFailure=yes" {
					found = true
					break
				}
			}
			if !found {
				t.Error("args should contain ExitOnForwardFailure=yes")
			}
		})
	}
}

func TestLocalhostRunBuildSSHArgsKeepalive(t *testing.T) {
	// Default keepalive
	lhr := &LocalhostRunTunnel{}
	args := lhr.buildSSHArgs(8080)
	found := false
	for _, arg := range args {
		if arg == "ServerAliveInterval=60" {
			found = true
			break
		}
	}
	if !found {
		t.Error("default keepalive should be 60")
	}

	// Custom keepalive
	lhr2 := &LocalhostRunTunnel{keepalive: 120}
	args2 := lhr2.buildSSHArgs(8080)
	found = false
	for _, arg := range args2 {
		if arg == "ServerAliveInterval=120" {
			found = true
			break
		}
	}
	if !found {
		t.Error("custom keepalive 120 should be in args")
	}
}

func TestLocalhostRunBuildAutosshArgs(t *testing.T) {
	lhr := &LocalhostRunTunnel{useAutossh: true}
	args := lhr.buildAutosshArgs(8080)

	// First two args should be -M 0
	if len(args) < 2 || args[0] != "-M" || args[1] != "0" {
		t.Errorf("autossh args should start with [-M 0], got %v", args[:2])
	}

	// Last arg should be localhost.run (no user by default)
	last := args[len(args)-1]
	if last != "localhost.run" {
		t.Errorf("last arg: got %q, want %q", last, "localhost.run")
	}
}

func TestLocalhostRunBuildCommand(t *testing.T) {
	// Without autossh
	lhr := &LocalhostRunTunnel{}
	binary, _ := lhr.buildCommand(8080)
	if binary != "ssh" {
		t.Errorf("without autossh: binary = %q, want %q", binary, "ssh")
	}

	// With autossh
	lhr2 := &LocalhostRunTunnel{useAutossh: true}
	binary2, args2 := lhr2.buildCommand(8080)
	if binary2 != "autossh" {
		t.Errorf("with autossh: binary = %q, want %q", binary2, "autossh")
	}
	if args2[0] != "-M" {
		t.Errorf("autossh args should start with -M, got %q", args2[0])
	}
}

func TestLocalhostRunURLRegex(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "986a09d8f6c02a.lhr.life tunneled with tls termination, https://986a09d8f6c02a.lhr.life",
			expected: "https://986a09d8f6c02a.lhr.life",
		},
		{
			input:    "abc123.lhr.rocks tunneled with tls termination, https://abc123.lhr.rocks",
			expected: "https://abc123.lhr.rocks",
		},
		{
			input:    "myapp.localhost.run tunneled with tls termination, https://myapp.localhost.run",
			expected: "https://myapp.localhost.run",
		},
		{
			// Banner URL — should NOT match because no "tunneled" keyword
			input:    "To set up and manage custom domains go to https://admin.localhost.run/",
			expected: "",
		},
		{
			input:    "Connection established. ** your connection id is abc123 **",
			expected: "",
		},
		{
			input:    "http://not-https.localhost.run",
			expected: "",
		},
	}

	for _, tt := range tests {
		// Simulate the same logic as scanForURL: only match lines with "tunneled"
		got := ""
		if localhostRunTunnelLine.MatchString(tt.input) {
			got = localhostRunURLRegex.FindString(tt.input)
		}
		if got != tt.expected {
			t.Errorf("input %q: got %q, want %q", tt.input, got, tt.expected)
		}
	}
}
