package extension

import (
	"path/filepath"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigIndexPrecedenceAndFullPersistence(t *testing.T) {
	cache := t.TempDir()
	idx, err := NewConfigIndex(cache)
	require.NoError(t, err)
	root := t.TempDir()
	source := func(name string) string {
		return "build:\n  shopwareVersionConstraint: '" + name + "'\nchangelog:\n  variables: {ticket: 'ABC'}\nstore:\n  tags: {en: [one, two]}\nvalidation:\n  store_compliance: true\n"
	}
	for _, location := range ConfigLocations {
		path := filepath.Join(root, location)
		require.NoError(t, idx.Index(indexer.NewParsedFile(path, []byte(source(location)))))
	}
	check := func(location string) *Config {
		t.Helper()
		config, err := idx.GetConfig(root)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(root, location), config.StorageLocation)
		assert.Equal(t, location, config.Build.ShopwareVersionConstraint)
		assert.Equal(t, map[string]string{"ticket": "ABC"}, config.Changelog.Variables)
		require.NotNil(t, config.Store.Tags.English)
		assert.Equal(t, []string{"one", "two"}, *config.Store.Tags.English)
		return config
	}
	previous := check(ConfigLocations[0])
	// A lower-priority editor buffer does not displace the selected disk config.
	low := filepath.Join(root, ConfigLocations[2])
	idx.UpdateLiveDocument(low, yamlparser.Parse("validation: {store_compliance: false}").Tree.Root)
	assert.True(t, check(ConfigLocations[0]).Validation.StoreCompliance)
	idx.RemoveLiveDocument(low)
	require.NoError(t, idx.Close())
	idx, err = NewConfigIndex(cache)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	check(ConfigLocations[0])
	require.NoError(t, idx.RemovedFiles([]string{filepath.Join(root, ConfigLocations[0])}))
	check(ConfigLocations[1])
	require.NoError(t, idx.RemovedFiles([]string{filepath.Join(root, ConfigLocations[1])}))
	check(ConfigLocations[2])
	// An empty higher-priority file is still selected; files are never merged.
	high := filepath.Join(root, ConfigLocations[0])
	idx.UpdateLiveDocument(high, yamlparser.Parse("").Tree.Root)
	config, err := idx.GetConfig(root)
	require.NoError(t, err)
	assert.Equal(t, high, config.StorageLocation)
	assert.False(t, config.Validation.StoreCompliance)
	assert.Empty(t, config.Build.ShopwareVersionConstraint)
	idx.RemoveLiveDocument(high)
	check(ConfigLocations[2])
	assert.Equal(t, ConfigLocations[0], previous.Build.ShopwareVersionConstraint, "published snapshots must remain immutable")
}
