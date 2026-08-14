package entityschema

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/require"
)

func TestSourceIndexUpdateRemovalAndPersistence(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(t.TempDir(), "src", "Odd.php")
	candidate := []byte(`<?php
namespace Acme;
use Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition;
final class Odd extends EntityDefinition {
    public const ENTITY_NAME = 'odd';
    protected function defineFields(): FieldCollection { return new FieldCollection([]); }
}`)
	index, err := NewSourceIndex(cache)
	require.NoError(t, err)
	require.NoError(t, index.Index(indexer.NewParsedFile(path, candidate)))
	source, found, err := index.Source(path)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, string(candidate), source)

	require.NoError(t, index.Index(indexer.NewParsedFile(path, []byte(`<?php final class Odd {}`))))
	_, found, err = index.Source(path)
	require.NoError(t, err)
	require.False(t, found)

	require.NoError(t, index.Index(indexer.NewParsedFile(path, candidate)))
	require.NoError(t, index.Close())
	index, err = NewSourceIndex(cache)
	require.NoError(t, err)
	source, found, err = index.Source(path)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, string(candidate), source)

	require.NoError(t, index.RemovedFiles([]string{path}))
	_, found, err = index.Source(path)
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, index.Close())
}
