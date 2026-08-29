package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kamrul1157024/helios/internal/provider"
)

func hookConfig(port int) map[string]interface{} {
	base := fmt.Sprintf("http://localhost:%d/hooks/claude", port)
	// Every hook that blocks on a human gets the same budget, derived from the
	// one the daemon itself waits: helios has to give up first, or the CLI walks
	// away from a prompt that is still on screen.
	blocking := HookTimeoutSeconds
	return map[string]interface{}{
		"hooks": map[string]interface{}{
			"PermissionRequest": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "http",
							"url":     base + "/permission",
							"timeout": blocking,
						},
					},
				},
			},
			"UserPromptSubmit": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type": "http",
							"url":  base + "/prompt/submit",
						},
					},
				},
			},
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "AskUserQuestion",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "http",
							"url":     base + "/question",
							"timeout": blocking,
						},
					},
				},
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type": "http",
							"url":  base + "/tool/pre",
						},
					},
				},
			},
			"PostToolUse": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type": "http",
							"url":  base + "/tool/post",
						},
					},
				},
			},
			"PostToolUseFailure": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type": "http",
							"url":  base + "/tool/post/failure",
						},
					},
				},
			},
			"Elicitation": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "http",
							"url":     base + "/elicitation",
							"timeout": blocking,
						},
					},
				},
			},
			"PreCompact": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type": "http",
							"url":  base + "/compact/pre",
						},
					},
				},
			},
			"PostCompact": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type": "http",
							"url":  base + "/compact/post",
						},
					},
				},
			},
			"Stop": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type": "http",
							"url":  base + "/stop",
						},
					},
				},
			},
			"StopFailure": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type": "http",
							"url":  base + "/stop/failure",
						},
					},
				},
			},
			"Notification": []interface{}{
				map[string]interface{}{
					"matcher": "permission_prompt|idle_prompt",
					"hooks": []interface{}{
						map[string]interface{}{
							"type": "http",
							"url":  base + "/notification",
						},
					},
				},
			},
			// SessionStart/End and SubagentStart/Stop use command hooks
			// because Claude Code v2.1.101 does not fire HTTP hooks for
			// these lifecycle events. The command hook pipes stdin (the
			// hook payload) through curl to the daemon endpoint.
			"SessionStart": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "cat | curl -s -X POST -H 'Content-Type: application/json' -d @- " + base + "/session/start",
						},
					},
				},
			},
			"SessionEnd": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "cat | curl -s -X POST -H 'Content-Type: application/json' -d @- " + base + "/session/end",
						},
					},
				},
			},
			"SubagentStart": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "cat | curl -s -X POST -H 'Content-Type: application/json' -d @- " + base + "/subagent/start",
						},
					},
				},
			},
			"SubagentStop": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "command",
							"command": "cat | curl -s -X POST -H 'Content-Type: application/json' -d @- " + base + "/subagent/stop",
						},
					},
				},
			},
		},
	}
}

func InstallHooks(port int, local bool) error {
	hooks := hookConfig(port)

	var settingsPath string
	if local {
		settingsPath = filepath.Join(".claude", "settings.local.json")
	} else {
		home, _ := os.UserHomeDir()
		settingsPath = filepath.Join(home, ".claude", "settings.json")
	}

	existing := make(map[string]interface{})
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		json.Unmarshal(data, &existing)
	}

	// Merge hooks into existing settings
	existing["hooks"] = hooks["hooks"]

	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	dir := filepath.Dir(settingsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	if err := os.WriteFile(settingsPath, out, 0644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	fmt.Printf("Hooks installed to %s\n", settingsPath)
	return nil
}

func InstallHooksIfMissing(port int) {
	home, _ := os.UserHomeDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		InstallHooks(port, false)
		return
	}
	if !json.Valid(data) {
		InstallHooks(port, false)
		return
	}
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if _, ok := m["hooks"]; !ok {
		InstallHooks(port, false)
	}
}

// HookConfigHash returns the SHA256 hash of the expected hook config JSON.
func HookConfigHash(port int) string {
	hooks := hookConfig(port)
	data, _ := json.Marshal(hooks["hooks"])
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HooksOutdated checks if the installed hooks differ from the expected config.
// Returns true when hooks are present but their content doesn't match the
// current hook config (e.g. after a helios upgrade that added new hooks).
func HooksOutdated(port int) bool {
	home, _ := os.UserHomeDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false // no file — not outdated, just missing
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false
	}

	installed, ok := settings["hooks"]
	if !ok {
		return false // no hooks key — not outdated, just missing
	}

	installedJSON, _ := json.Marshal(installed)
	installedSum := sha256.Sum256(installedJSON)

	return hex.EncodeToString(installedSum[:]) != HookConfigHash(port)
}

func ShowHooks(port int) {
	hooks := hookConfig(port)
	out, _ := json.MarshalIndent(hooks, "", "  ")
	fmt.Println(string(out))
}

func RemoveHooks() error {
	home, _ := os.UserHomeDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("no settings file found")
	}

	existing := make(map[string]interface{})
	if err := json.Unmarshal(data, &existing); err != nil {
		return fmt.Errorf("parse settings: %w", err)
	}

	delete(existing, "hooks")

	out, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(settingsPath, out, 0644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}

	fmt.Println("Hooks removed from", settingsPath)
	return nil
}

// ==================== provider.HookInstaller ====================

func (p *Provider) InstallHooks(scope provider.Scope) error {
	return InstallHooks(mcpPort, scope == provider.ScopeProject)
}

// HookHealth reports whether the hooks Claude would run match this build.
//
// Effective equals Current here. Claude runs whatever is in settings.json
// without a trust gate, so there is no third question to ask — unlike Codex,
// which reads a hook table and then silently declines to run it.
func (p *Provider) HookHealth() provider.HookHealth {
	installed, current := hookState(mcpPort)
	h := provider.HookHealth{Installed: installed, Current: current, Effective: current}
	switch {
	case !installed:
		h.Detail = "no hooks in ~/.claude/settings.json; run `helios hooks install`"
	case !current:
		h.Detail = "hooks are from an older helios; run `helios hooks install`"
	}
	return h
}

func (p *Provider) RemoveHooks() error { return RemoveHooks() }

// hookState reports whether a hook table is installed and whether it matches
// this build.
func hookState(port int) (installed, current bool) {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return false, false
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, false
	}
	raw, ok := settings["hooks"]
	if !ok {
		return false, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return true, false
	}
	sum := sha256.Sum256(encoded)
	return true, hex.EncodeToString(sum[:]) == HookConfigHash(port)
}
