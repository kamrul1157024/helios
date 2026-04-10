package helios

import (
	"embed"
	"io/fs"
)

//go:embed all:frontend/dist
var frontendEmbed embed.FS

// FrontendFS returns the embedded frontend filesystem.
func FrontendFS() fs.FS {
	sub, err := fs.Sub(frontendEmbed, "frontend/dist")
	if err != nil {
		return nil
	}
	return sub
}
