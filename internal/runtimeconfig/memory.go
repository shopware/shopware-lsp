// Package runtimeconfig applies process-wide runtime defaults used by the
// language-server executable.
package runtimeconfig

import (
	"os"
	"runtime/debug"
)

// DefaultGCPercent keeps the language server's idle and peak RSS below the Go
// runtime default without materially changing cold-index latency on the
// reference Shopware workspace.
const DefaultGCPercent = 75

// MemoryPolicy describes whether the process selected the balanced default.
// Explicit Go runtime environment settings always take precedence.
type MemoryPolicy struct {
	GCPercent int
	Applied   bool
}

// ApplyMemoryPolicy selects the balanced GC default unless the caller already
// configured GOGC or GOMEMLIMIT. The latter is also respected because combining
// an explicit soft limit with an implicit lower GC target can add substantially
// more collection work than the requested limit alone.
func ApplyMemoryPolicy() MemoryPolicy {
	return applyMemoryPolicy(os.LookupEnv, debug.SetGCPercent)
}

func applyMemoryPolicy(
	lookupEnv func(string) (string, bool),
	setGCPercent func(int) int,
) MemoryPolicy {
	if _, configured := lookupEnv("GOGC"); configured {
		return MemoryPolicy{}
	}
	if _, configured := lookupEnv("GOMEMLIMIT"); configured {
		return MemoryPolicy{}
	}

	setGCPercent(DefaultGCPercent)
	return MemoryPolicy{
		GCPercent: DefaultGCPercent,
		Applied:   true,
	}
}
