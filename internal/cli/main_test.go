package cli

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) (code int) {
	cache, err := os.MkdirTemp("", "shopware-lsp-cli-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		if err := os.RemoveAll(cache); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}()
	// Production cache paths are keyed by workspace root. Each test owns a
	// unique t.TempDir root, so parallel sessions still have separate databases.
	// Set the parent once: t.Setenv in a session helper prevents t.Parallel.
	if err := os.Setenv("SHOPWARE_LSP_CACHE_DIR", cache); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}
