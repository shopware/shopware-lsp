package extension

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStoreCompliance(t *testing.T) {
	for _, test := range []struct {
		source  string
		enabled bool
	}{
		{"validation:\n  store_compliance: true\n", true},
		{"validation: {store_compliance: true}", true},
		{"validation:\n  store_compliance: true # Store checks\n", true},
		{"validation:\n  store_compliance: false\n", false},
		{"validation:\n  store_compliance: \"true\"\n", false},
		{"validation:\n  store_compliance: \n", false},
		{"validation:\n  other: true\n", false},
		{"store_compliance: true\n", false},
		{"", false},
	} {
		t.Run(test.source, func(t *testing.T) {
			result := yamlparser.Parse(test.source)
			assert.Equal(t, test.enabled, ParseConfigTree(".shopware-extension.yml", result.Tree.Root).Validation.StoreCompliance)
		})
	}
}

func TestStoreCompliancePersistenceAndOverlay(t *testing.T) {
	cache := t.TempDir()
	idx, err := NewConfigIndex(cache)
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "Plugin")
	path := filepath.Join(dir, ".shopware-extension.yml")
	check := func(want bool) {
		t.Helper()
		enabled, err := idx.StoreComplianceEnabled(dir)
		require.NoError(t, err)
		assert.Equal(t, want, enabled)
	}
	check(false)
	changes := 0
	idx.SetOnChange(func(changed string) { changes++; assert.Equal(t, path, changed) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte("validation:\n  store_compliance: true\n"))))
	check(true)
	idx.UpdateLiveDocument(path, yamlparser.Parse("validation:\n  store_compliance: false\n").Tree.Root)
	check(false)
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte("validation:\n  store_compliance: true\n"))))
	check(false)
	idx.RemoveLiveDocument(path)
	check(true)
	assert.Equal(t, 4, changes)
	require.NoError(t, idx.Close())
	idx, err = NewConfigIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	check(true)
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte("validation: {}\n"))))
	check(false)
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte("validation: {store_compliance: true}"))))
	check(true)
	require.NoError(t, idx.RemovedFiles([]string{path}))
	check(false)
}

func TestStoreComplianceRemovalPublishesAfterCommit(t *testing.T) {
	store, err := indexer.NewStore(filepath.Join(t.TempDir(), "indexes.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	idx, err := NewConfigIndex(t.TempDir(), store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	dir := t.TempDir()
	path := filepath.Join(dir, ".shopware-extension.yml")
	require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte("validation: {store_compliance: true}"))))
	changes := 0
	idx.SetOnChange(func(string) { changes++ })
	mutation, err := store.BeginMutation(context.Background())
	require.NoError(t, err)
	require.NoError(t, idx.RemovedFilesIn([]string{path}, mutation))
	assert.Zero(t, changes)
	require.NoError(t, mutation.Rollback())
	enabled, err := idx.StoreComplianceEnabled(dir)
	require.NoError(t, err)
	assert.True(t, enabled)
	mutation, err = store.BeginMutation(context.Background())
	require.NoError(t, err)
	require.NoError(t, idx.RemovedFilesIn([]string{path}, mutation))
	require.NoError(t, mutation.Commit())
	assert.Equal(t, 1, changes)
	enabled, err = idx.StoreComplianceEnabled(dir)
	require.NoError(t, err)
	assert.False(t, enabled)
}
