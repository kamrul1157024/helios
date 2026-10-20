package desktop

import (
	"embed"
	"io/fs"
)

// web is the page the phone's WebView loads (D8): the client, its styles, and
// the vendored guacamole-common-js. Embedded so the daemon serves exactly the
// client that matches its tunnel, with nothing to install beside it.
//
//go:embed web
var web embed.FS

// WebFS is the page's files, rooted at the web folder.
func WebFS() fs.FS {
	sub, err := fs.Sub(web, "web")
	if err != nil {
		// fs.Sub fails only on an invalid path, and "web" is a constant.
		panic(err)
	}
	return sub
}
