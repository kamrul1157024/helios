package codex

import (
	"encoding/json"
	"strings"

	"github.com/kamrul1157024/helios/internal/provider"
)

// patchMarker opens every apply_patch body, whichever field carries it.
const patchMarker = "*** Begin Patch"

// TouchedPaths names the files an apply_patch call wrote. Codex has no other
// tool that writes a file by name; a shell command that writes one shows up in
// git status instead.
//
// The patch is looked for rather than read from one field: the rollout keeps
// it as a custom tool call's input, the hook payload as "command", and older
// builds sent it inside a shell call's argv.
func (p *Provider) TouchedPaths(tool string, input map[string]any) []provider.Touch {
	patch := findPatch(input)
	if patch == "" {
		return nil
	}
	return parsePatchTouches(patch)
}

func findPatch(input map[string]any) string {
	for _, key := range []string{"input", "command", "patch", "arguments"} {
		switch v := input[key].(type) {
		case string:
			if strings.Contains(v, patchMarker) {
				// A function call's arguments are JSON holding the patch.
				var nested map[string]any
				if json.Unmarshal([]byte(v), &nested) == nil {
					if inner := findPatch(nested); inner != "" {
						return inner
					}
				}
				return v
			}
		case []any:
			for _, part := range v {
				if s, ok := part.(string); ok && strings.Contains(s, patchMarker) {
					return s
				}
			}
		}
	}
	return ""
}

// parsePatchTouches reads the file headers of an apply_patch body. A move
// counts as touching its destination, which is the file that exists after.
// For an update, the first added line is the anchor the daemon looks for on
// disk; an added file starts at line 1.
func parsePatchTouches(patch string) []provider.Touch {
	var touches []provider.Touch
	current := -1
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			touches = append(touches, provider.Touch{Path: strings.TrimSpace(line[len("*** Add File: "):]), Line: 1})
			current = len(touches) - 1
		case strings.HasPrefix(line, "*** Update File: "):
			touches = append(touches, provider.Touch{Path: strings.TrimSpace(line[len("*** Update File: "):])})
			current = len(touches) - 1
		case strings.HasPrefix(line, "*** Delete File: "):
			touches = append(touches, provider.Touch{Path: strings.TrimSpace(line[len("*** Delete File: "):]), Deleted: true})
			current = -1
		case strings.HasPrefix(line, "*** Move to: ") && current >= 0:
			touches[current].Path = strings.TrimSpace(line[len("*** Move to: "):])
		case strings.HasPrefix(line, "+") && current >= 0:
			t := &touches[current]
			if t.Line == 0 && t.Anchor == "" && strings.TrimSpace(line[1:]) != "" {
				t.Anchor = line[1:]
			}
		}
	}
	return touches
}
