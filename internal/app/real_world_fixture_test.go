//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

// realWorldWorkspaceFixture owns the expensive indexed workspace shared by all
// feature checks in TestShopwareTrunkIndexing. Keeping setup state in one value
// makes future domain-focused subtests independent of setup implementation.
type realWorldWorkspaceFixture struct {
	root        string
	ctx         context.Context
	workspace   *Workspace
	phpIndex    *php.PHPIndex
	server      *lsp.Server
	coldElapsed time.Duration
}

func newRealWorldWorkspaceFixture(t *testing.T) *realWorldWorkspaceFixture {
	t.Helper()
	root := realWorldProjectRoot(t)
	t.Setenv("SHOPWARE_LSP_CACHE_DIR", t.TempDir())
	ctx := context.Background()
	workspace, phpIndex, server := openRealWorldWorkspaceWithServer(t, ctx, root)
	started := time.Now()
	require.NoError(t, workspace.Scanner().IndexAll(ctx))
	return &realWorldWorkspaceFixture{
		root:        root,
		ctx:         ctx,
		workspace:   workspace,
		phpIndex:    phpIndex,
		server:      server,
		coldElapsed: time.Since(started),
	}
}
