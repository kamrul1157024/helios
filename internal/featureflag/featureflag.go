// Package featureflag reads the environment switches that turn unfinished
// Helios features on. Each flag is read fresh, so a process picks up whatever
// its own environment held at startup.
package featureflag

import "os"

// MCP reports whether the experimental MCP tools are served.
//
// Off by default. A tool call can only name the session it came from when
// Helios launched the agent and injected the session header; an agent started
// by hand reaches the same server anonymously, and every tool that needs an
// identity refuses it. Until that is solved the tools are opt-in.
func MCP() bool { return os.Getenv("HELIOS_EXPERIMENTAL_MCP") == "1" }
