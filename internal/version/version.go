// Package version carries the release this binary belongs to.
//
// Its own package rather than a constant in main: the daemon reports the number
// over /api/health so a client can say which machines are behind, and importing
// package main is not a thing a server can do.
package version

// Current is stamped at build time with -ldflags "-X …/internal/version.Current=x.y.z".
// A var, not a const, for that reason — and "dev" by default, because a
// checkout built by hand belongs to no release and should not claim one.
var Current = "dev"
