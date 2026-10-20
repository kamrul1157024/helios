package desktop

import (
	"strings"
)

// xrdp keeps its settings in INI files that the package also edits on
// upgrade, and that people edit by hand. So Helios changes only the keys it
// owns, in place, and leaves every other byte — comments, order, spacing —
// as it found it.

// setINIKey sets key=value inside [section]. An existing key keeps its line
// and only its value changes; a missing key is added after the last setting
// of the section; a missing section is added at the end. Commented-out keys
// (# or ;) are left alone, which keeps the package's documentation intact.
func setINIKey(content, section, key, value string) string {
	lines := strings.Split(content, "\n")
	start, end := sectionBounds(lines, section)
	if start < 0 {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + "[" + section + "]\n" + key + "=" + value + "\n"
	}
	for i := start + 1; i < end; i++ {
		if k, _, ok := iniPair(lines[i]); ok && k == key {
			lines[i] = key + "=" + value
			return strings.Join(lines, "\n")
		}
	}
	insert := start + 1
	for i := start + 1; i < end; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			insert = i + 1
		}
	}
	lines = append(lines[:insert], append([]string{key + "=" + value}, lines[insert:]...)...)
	return strings.Join(lines, "\n")
}

// iniValue reads key from [section].
func iniValue(content, section, key string) (string, bool) {
	lines := strings.Split(content, "\n")
	start, end := sectionBounds(lines, section)
	if start < 0 {
		return "", false
	}
	for i := start + 1; i < end; i++ {
		if k, v, ok := iniPair(lines[i]); ok && k == key {
			return v, true
		}
	}
	return "", false
}

// removeINIKey deletes key from [section], for the uninstall of a key that
// the file did not have before Helios added it.
func removeINIKey(content, section, key string) string {
	lines := strings.Split(content, "\n")
	start, end := sectionBounds(lines, section)
	if start < 0 {
		return content
	}
	for i := start + 1; i < end; i++ {
		if k, _, ok := iniPair(lines[i]); ok && k == key {
			return strings.Join(append(lines[:i], lines[i+1:]...), "\n")
		}
	}
	return content
}

// sectionBounds finds the header line of [section] and the line that ends
// it: the next header, or the end of the file. It returns -1 when the section
// is absent. Section names compare exactly, as xrdp compares them.
func sectionBounds(lines []string, section string) (int, int) {
	start := -1
	for i, line := range lines {
		name, ok := iniHeader(line)
		if !ok {
			continue
		}
		if start >= 0 {
			return start, i
		}
		if name == section {
			start = i
		}
	}
	return start, len(lines)
}

func iniHeader(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if len(t) < 2 || t[0] != '[' || t[len(t)-1] != ']' {
		return "", false
	}
	return strings.TrimSpace(t[1 : len(t)-1]), true
}

func iniPair(line string) (string, string, bool) {
	t := strings.TrimSpace(line)
	if t == "" || t[0] == '#' || t[0] == ';' {
		return "", "", false
	}
	k, v, ok := strings.Cut(t, "=")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(k), strings.TrimSpace(v), true
}
