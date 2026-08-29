package main

import (
	"slices"
	"strings"
	"testing"
)

// A wrap started from a terminal that is itself an agent session inherits the
// agent's own variables. The child would then take itself for a continuation
// of its parent: its hooks report the wrong session and the new one never gets
// a title.
func TestAgentEnv_DropsTheParentAgentsMarks(t *testing.T) {
	for _, v := range agentVars {
		t.Setenv(v, "inherited")
	}

	for _, kv := range agentEnv(nil) {
		if key, _, ok := strings.Cut(kv, "="); ok && slices.Contains(agentVars, key) {
			t.Errorf("env still carries %q", kv)
		}
	}
}

func TestAgentEnv_KeepsEverythingElse(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/cfg")

	env := agentEnv(nil)
	if !slices.Contains(env, "PATH=/usr/bin") {
		t.Error("PATH was dropped")
	}
	// Only the session markers go: config and credentials the agent needs are
	// not ours to strip.
	if !slices.Contains(env, "CLAUDE_CONFIG_DIR=/tmp/cfg") {
		t.Error("CLAUDE_CONFIG_DIR was dropped")
	}
}
