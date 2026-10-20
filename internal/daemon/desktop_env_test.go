package daemon

import "testing"

func TestWithDesktopEnv(t *testing.T) {
	if got := withDesktopEnv(map[string]string{"A": "1"}, nil); len(got) != 1 || got["A"] != "1" {
		t.Fatalf("no desktop changed the env: %v", got)
	}
	got := withDesktopEnv(map[string]string{"HELIOS_SESSION": "s1", "DISPLAY": ":0"},
		map[string]string{"DISPLAY": ":10", "XAUTHORITY": "/home/dev/.Xauthority"})
	if got["DISPLAY"] != ":0" || got["XAUTHORITY"] != "/home/dev/.Xauthority" || got["HELIOS_SESSION"] != "s1" {
		t.Fatalf("merged env = %v; the launch's own DISPLAY must win", got)
	}
	if got := withDesktopEnv(nil, map[string]string{"DISPLAY": ":10"}); got["DISPLAY"] != ":10" {
		t.Fatalf("nil env = %v", got)
	}
}
