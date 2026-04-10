package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func hookConfig(port int) map[string]interface{} {
	base := fmt.Sprintf("http://localhost:%d", port)
	return map[string]interface{}{
		"hooks": map[string]interface{}{
			"PermissionRequest": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type":    "http",
							"url":     base + "/hooks/permission",
							"timeout": 300,
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
							"url":  base + "/hooks/stop",
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
							"url":  base + "/hooks/stop-failure",
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
							"url":  base + "/hooks/notification",
						},
					},
				},
			},
			"SessionStart": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type": "http",
							"url":  base + "/hooks/session-start",
						},
					},
				},
			},
			"SessionEnd": []interface{}{
				map[string]interface{}{
					"matcher": "*",
					"hooks": []interface{}{
						map[string]interface{}{
							"type": "http",
							"url":  base + "/hooks/session-end",
						},
					},
				},
			},
		},
	}
}

func InstallHooks(local bool) error {
	cfg := DefaultConfig()
	hooks := hookConfig(cfg.Server.Port)

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

func ShowHooks() {
	cfg := DefaultConfig()
	hooks := hookConfig(cfg.Server.Port)
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
