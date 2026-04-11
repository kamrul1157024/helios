package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func hookConfig(port int) map[string]interface{} {
	base := fmt.Sprintf("http://localhost:%d/hooks/claude", port)
	return map[string]interface{}{
		"hooks": map[string]interface{}{
			"PermissionRequest": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "http",
							"url":     base + "/permission",
							"timeout": 300,
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
							"timeout": 300,
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
							"timeout": 300,
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

func InstallHooks(local bool) error {
	cfg := DefaultConfig()
	hooks := hookConfig(cfg.Server.InternalPort)

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

func InstallHooksIfMissing() {
	home, _ := os.UserHomeDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		InstallHooks(false)
		return
	}
	if !json.Valid(data) {
		InstallHooks(false)
		return
	}
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if _, ok := m["hooks"]; !ok {
		InstallHooks(false)
	}
}

func ShowHooks() {
	cfg := DefaultConfig()
	hooks := hookConfig(cfg.Server.InternalPort)
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
