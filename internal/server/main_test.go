package server

import (
	"os"
	"testing"

	"github.com/kamrul1157024/helios/internal/provider"
	claude "github.com/kamrul1157024/helios/internal/provider/claude"
	codex "github.com/kamrul1157024/helios/internal/provider/codex"
)

// TestMain registers the providers the daemon registers.
//
// The server asks the registry what a provider can do — its permission modes,
// its transcript locator, what a screen means — so an empty registry makes
// every one of those look like "not supported" and the tests fail for a reason
// that has nothing to do with what they check.
func TestMain(m *testing.M) {
	provider.MustRegister(claude.New(0, 0))
	provider.MustRegister(codex.New(0))
	os.Exit(m.Run())
}
