//go:build !linux

package server

import (
	"errors"

	"github.com/fsnotify/fsnotify"
)

// liveWatchSupported is false off Linux. fsnotify's macOS backend is kqueue,
// which opens a descriptor for every file in a watched directory, and FSEvents
// needs cgo. Every root polls instead: git stat, the FilesTouched hook, and the
// TTL. See docs/specs/65-fast-files.md, open question 1.
const liveWatchSupported = false

var errNoWatchBackend = errors.New("no directory watcher on this platform")

func openDirWatcher() (*fsnotify.Watcher, error) {
	return nil, errNoWatchBackend
}

func maxUserWatches() int { return 0 }
