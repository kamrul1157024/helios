//go:build linux

package server

import (
	"os"
	"strconv"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// liveWatchSupported is true where the index can watch directories: inotify,
// one descriptor per directory and none per file.
const liveWatchSupported = true

// openDirWatcher opens an inotify watcher with room for a burst. The buffer is
// what a checkout fills while the loop is still reading the first events.
func openDirWatcher() (*fsnotify.Watcher, error) {
	return fsnotify.NewBufferedWatcher(4096)
}

// maxUserWatches reads the kernel's per-user inotify limit. Kernels from 5.11
// scale it with memory, so most machines have plenty; 8192 is the old default
// and the answer when the file cannot be read.
func maxUserWatches() int {
	raw, err := os.ReadFile("/proc/sys/fs/inotify/max_user_watches")
	if err != nil {
		return 8192
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n <= 0 {
		return 8192
	}
	return n
}
