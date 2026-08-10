package phpanalysis

import (
	"sync"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/stretchr/testify/require"
)

func TestForDocumentCachesPerWorkspaceRevision(t *testing.T) {
	index, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	document := lsp.NewTextDocument(
		"file:///project/src/Consumer.php",
		"<?php class Consumer {}",
		1,
	)

	first, err := ForDocument(index, document)
	require.NoError(t, err)
	second, err := ForDocument(index, document)
	require.NoError(t, err)
	require.Same(t, first, second)

	require.NoError(t, index.Index(indexer.NewParsedFile(
		"/project/src/Dependency.php",
		[]byte("<?php class Dependency {}"),
	)))
	third, err := ForDocument(index, document)
	require.NoError(t, err)
	require.NotSame(t, first, third)
	require.Greater(t, third.Snapshot.Revision, first.Snapshot.Revision)
}

func TestForDocumentSharesConcurrentAnalysis(t *testing.T) {
	index, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, index.Close()) })
	document := lsp.NewTextDocument(
		"file:///project/src/Concurrent.php",
		"<?php class Concurrent {}",
		1,
	)

	const workers = 8
	states := make([]*State, workers)
	errors := make([]error, workers)
	var group sync.WaitGroup
	for worker := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			states[worker], errors[worker] = ForDocument(index, document)
		}()
	}
	group.Wait()
	for worker := range workers {
		require.NoError(t, errors[worker])
		require.Same(t, states[0], states[worker])
	}
}
