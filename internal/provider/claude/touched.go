package claude

import "github.com/kamrul1157024/helios/internal/provider"

// TouchedPaths names the file a Claude tool call wrote. Only the tools that
// write a file by name count; a Bash command that writes one is invisible
// here, and shows up in git status instead.
func (p *Provider) TouchedPaths(tool string, input map[string]any) []provider.Touch {
	str := func(key string) string {
		v, _ := input[key].(string)
		return v
	}
	switch tool {
	case "Write":
		if path := str("file_path"); path != "" {
			return []provider.Touch{{Path: path, Line: 1}}
		}
	case "Edit":
		if path := str("file_path"); path != "" {
			return []provider.Touch{{Path: path, Anchor: str("new_string")}}
		}
	case "MultiEdit":
		if path := str("file_path"); path != "" {
			return []provider.Touch{{Path: path, Anchor: firstEditText(input["edits"])}}
		}
	case "NotebookEdit":
		if path := str("notebook_path"); path != "" {
			return []provider.Touch{{Path: path}}
		}
	}
	return nil
}

// firstEditText is the new_string of a MultiEdit's first edit, which is where
// the call's change starts as far as anyone can tell without applying it.
func firstEditText(edits any) string {
	list, _ := edits.([]any)
	if len(list) == 0 {
		return ""
	}
	first, _ := list[0].(map[string]any)
	text, _ := first["new_string"].(string)
	return text
}
