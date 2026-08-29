package daemon

import (
	"sync"

	"github.com/kamrul1157024/helios/internal/featureflag"
	"github.com/kamrul1157024/helios/internal/provider"
	claude "github.com/kamrul1157024/helios/internal/provider/claude"
	codex "github.com/kamrul1157024/helios/internal/provider/codex"
)

var registerOnce sync.Once

// RegisterProviders fills the registry with the compiled-in providers.
//
// Every process that asks the registry anything must call this, not only the
// daemon. `helios hooks install` and the TUI's health check both run in their
// own process: with an empty registry they iterate nothing, report nothing
// wrong, and exit successfully having done nothing at all.
//
// Idempotent, because the daemon and a command that runs before it can both
// reach here.
func RegisterProviders(internalPort int) {
	registerOnce.Do(func() {
		// Two different uses of one number, and only one is behind the MCP
		// feature flag. A zero port tells the Claude provider to leave
		// --mcp-config off the argv, which is what the flag being off means.
		// Codex uses the port to reach the daemon's hook route, which is not
		// optional: gating it would leave every codex hook curling
		// http://localhost:0 and the daemon hearing nothing.
		mcpPort := 0
		if featureflag.MCP() {
			mcpPort = internalPort
		}
		provider.MustRegister(claude.New(mcpPort))
		provider.MustRegister(codex.New(internalPort))
	})
}

// RegisterDefaultProviders registers against the configured internal port, for
// callers that have not loaded config themselves.
func RegisterDefaultProviders() {
	RegisterProviders(DefaultConfig().Server.InternalPort)
}
