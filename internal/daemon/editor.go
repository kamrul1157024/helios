package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EditorInfo describes a detected code editor installation.
type EditorInfo struct {
	Name         string // e.g. "VS Code", "Cursor"
	SettingsPath string // absolute path to settings.json
	Configured   bool   // true if tmux profile is already set up
}

// EditorSetupResult reports the outcome of configuring an editor.
type EditorSetupResult struct {
	Editor  EditorInfo
	Success bool
	Error   error
}

// DetectEditors scans known editor locations and returns those that exist.
func DetectEditors() []EditorInfo {
	home, _ := os.UserHomeDir()

	type candidate struct {
		name string
		dir  string
	}

	var candidates []candidate

	switch runtime.GOOS {
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support")
		candidates = []candidate{
			{"VS Code", filepath.Join(base, "Code", "User")},
			{"VS Code Insiders", filepath.Join(base, "Code - Insiders", "User")},
			{"Cursor", filepath.Join(base, "Cursor", "User")},
			{"VSCodium", filepath.Join(base, "VSCodium", "User")},
		}
	case "linux":
		base := filepath.Join(home, ".config")
		candidates = []candidate{
			{"VS Code", filepath.Join(base, "Code", "User")},
			{"VS Code Insiders", filepath.Join(base, "Code - Insiders", "User")},
			{"Cursor", filepath.Join(base, "Cursor", "User")},
			{"VSCodium", filepath.Join(base, "VSCodium", "User")},
		}
	default:
		return nil
	}

	var found []EditorInfo
	for _, c := range candidates {
		settingsPath := filepath.Join(c.dir, "settings.json")
		if _, err := os.Stat(c.dir); err == nil {
			info := EditorInfo{
				Name:         c.name,
				SettingsPath: settingsPath,
				Configured:   editorHasTmuxProfile(settingsPath),
			}
			found = append(found, info)
		}
	}

	return found
}

// editorSettingsKeys returns the correct VS Code settings keys for the current OS.
func editorSettingsKeys() (defaultProfileKey, profilesKey string) {
	switch runtime.GOOS {
	case "darwin":
		return "terminal.integrated.defaultProfile.osx", "terminal.integrated.profiles.osx"
	case "linux":
		return "terminal.integrated.defaultProfile.linux", "terminal.integrated.profiles.linux"
	default:
		return "", ""
	}
}

// editorHasTmuxProfile checks if a VS Code settings file already has the tmux profile configured.
func editorHasTmuxProfile(settingsPath string) bool {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}
	// Quick string check — avoids needing to parse JSONC
	return strings.Contains(string(data), `"tmux"`) &&
		strings.Contains(string(data), "new-session")
}

// ConfigureEditor patches a VS Code settings.json to add the tmux terminal profile.
// Returns an error if the file can't be parsed (e.g. has JSONC comments).
func ConfigureEditor(editor EditorInfo, tmuxPath string) error {
	if editor.Configured {
		return nil
	}

	defaultKey, profilesKey := editorSettingsKeys()
	if defaultKey == "" {
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	// Read existing settings
	var settings map[string]interface{}
	data, err := os.ReadFile(editor.SettingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No settings file yet — create one
			settings = make(map[string]interface{})
		} else {
			return fmt.Errorf("read %s: %w", editor.SettingsPath, err)
		}
	} else {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse %s (may contain comments — edit manually): %w", editor.SettingsPath, err)
		}
	}

	// Set default profile to tmux
	settings[defaultKey] = "tmux"

	// Get or create the profiles object
	profiles, _ := settings[profilesKey].(map[string]interface{})
	if profiles == nil {
		profiles = make(map[string]interface{})
	}

	// Add tmux profile
	profiles["tmux"] = map[string]interface{}{
		"path": tmuxPath,
		"args": []string{"new-session"},
	}
	settings[profilesKey] = profiles

	// Write back
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(editor.SettingsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(editor.SettingsPath, append(out, '\n'), 0644); err != nil {
		return fmt.Errorf("write %s: %w", editor.SettingsPath, err)
	}

	return nil
}

// ConfigureAllEditors attempts to configure all detected editors.
// Returns results for each editor (success or failure with instructions).
func ConfigureAllEditors(tmuxPath string) []EditorSetupResult {
	editors := DetectEditors()
	results := make([]EditorSetupResult, len(editors))

	for i, editor := range editors {
		results[i] = EditorSetupResult{Editor: editor}
		if editor.Configured {
			results[i].Success = true
			continue
		}
		err := ConfigureEditor(editor, tmuxPath)
		results[i].Success = err == nil
		results[i].Error = err
	}

	return results
}

// ManualEditorInstructions returns human-readable instructions for manual configuration.
func ManualEditorInstructions(editor EditorInfo, tmuxPath string, err error) string {
	var b strings.Builder

	defaultKey, profilesKey := editorSettingsKeys()

	b.WriteString(fmt.Sprintf("  Manual fix for %s:\n", editor.Name))
	b.WriteString(fmt.Sprintf("  Edit: %s\n\n", editor.SettingsPath))
	b.WriteString(fmt.Sprintf("    \"%s\": \"tmux\",\n", defaultKey))
	b.WriteString(fmt.Sprintf("    \"%s\": {\n", profilesKey))
	b.WriteString("      \"tmux\": {\n")
	b.WriteString(fmt.Sprintf("        \"path\": \"%s\",\n", tmuxPath))
	b.WriteString("        \"args\": [\"new-session\"]\n")
	b.WriteString("      }\n")
	b.WriteString("    }\n")

	return b.String()
}
