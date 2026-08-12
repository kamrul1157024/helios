package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveCWD turns a client-supplied working directory into one the daemon can
// hand to a terminal host.
//
// Clients send what the user typed, and "~/src/app" is what people type. Left
// alone it reaches exec as a literal directory name, which fails as
// "fork/exec /usr/local/bin/helios: no such file or directory" — exec blames
// the binary for a missing Dir — so the error points at everything except the
// path that is wrong.
func resolveCWD(raw string) (string, error) {
	cwd := strings.TrimSpace(raw)
	if cwd == "" {
		return "", fmt.Errorf("working directory is required")
	}

	if cwd == "~" || strings.HasPrefix(cwd, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot expand ~: %w", err)
		}
		cwd = filepath.Join(home, strings.TrimPrefix(cwd[1:], "/"))
	}

	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("working directory must be an absolute path: %s", raw)
	}

	cwd = filepath.Clean(cwd)
	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("working directory does not exist: %s", cwd)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory is not a directory: %s", cwd)
	}
	return cwd, nil
}
