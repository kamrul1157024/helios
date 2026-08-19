// Package sysinfo reads what the machine as a whole is doing, so a client can
// put a session's cost next to the room the machine has left.
package sysinfo

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// How long a reading is trusted. Every field here comes from a shell-out or a
// file read, and clients ask on every poll.
const ttl = 15 * time.Second

// Stats is the machine's own load, as opposed to any one session's.
type Stats struct {
	// Load is the one-minute load average over the core count, as a fraction:
	// 1 means the machine is saturated. Load rather than an instantaneous CPU
	// percentage because sampling for the latter costs a second of waiting.
	Load float64 `json:"load"`
	// MemoryUsed is physical memory in use, and MemoryTotal what the machine
	// has. Both are 0 where they cannot be read.
	MemoryUsed  int64 `json:"memory_used"`
	MemoryTotal int64 `json:"memory_total"`
}

var (
	mu     sync.Mutex
	cached Stats
	read   time.Time
)

// Read returns the machine's load and memory, cached for ttl.
func Read() Stats {
	mu.Lock()
	defer mu.Unlock()
	if !read.IsZero() && time.Since(read) < ttl {
		return cached
	}

	total := MemoryTotal()
	cached = Stats{
		Load:        loadAverage() / float64(runtime.NumCPU()),
		MemoryUsed:  total - memoryAvailable(),
		MemoryTotal: total,
	}
	if cached.MemoryUsed < 0 || total == 0 {
		cached.MemoryUsed = 0
	}
	read = time.Now()
	return cached
}

// MemoryTotal returns physical memory in bytes, or 0 where it cannot be read.
// Linux is read from /proc/meminfo rather than sysctl, which reports something
// unrelated there.
func MemoryTotal() int64 {
	if runtime.GOOS == "linux" {
		return meminfoField("MemTotal:")
	}
	return sysctlInt("hw.memsize")
}

// memoryAvailable returns the bytes a new process could claim without paging.
func memoryAvailable() int64 {
	if runtime.GOOS == "linux" {
		return meminfoField("MemAvailable:")
	}
	// Free pages plus the ones the kernel would hand over on demand:
	// speculative reads and the inactive list are both reclaimable.
	out, err := run("vm_stat")
	if err != nil {
		return 0
	}
	pageSize := int64(4096)
	if i := strings.Index(out, "page size of "); i >= 0 {
		if n, err := strconv.ParseInt(strings.Fields(out[i+len("page size of "):])[0], 10, 64); err == nil {
			pageSize = n
		}
	}
	var pages int64
	for _, line := range strings.Split(out, "\n") {
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(name) {
		case "Pages free", "Pages inactive", "Pages speculative", "Pages purgeable":
			n, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(value), "."), 10, 64)
			if err == nil {
				pages += n
			}
		}
	}
	return pages * pageSize
}

// loadAverage returns the one-minute load average, or 0 if it cannot be read.
func loadAverage() float64 {
	if runtime.GOOS == "linux" {
		data, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return 0
		}
		fields := strings.Fields(string(data))
		if len(fields) == 0 {
			return 0
		}
		load, _ := strconv.ParseFloat(fields[0], 64)
		return load
	}
	// Darwin reports it as "{ 2.20 2.33 2.65 }".
	out, err := run("sysctl", "-n", "vm.loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(strings.Trim(strings.TrimSpace(out), "{}"))
	if len(fields) == 0 {
		return 0
	}
	load, _ := strconv.ParseFloat(fields[0], 64)
	return load
}

func meminfoField(key string) int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, key) {
			continue
		}
		fields := strings.Fields(line)
		// key, value, unit — the kernel always reports kB here.
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || kb <= 0 {
			return 0
		}
		return kb * 1024
	}
	return 0
}

func sysctlInt(name string) int64 {
	out, err := run("sysctl", "-n", name)
	if err != nil {
		return 0
	}
	value, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
