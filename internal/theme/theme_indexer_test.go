package theme

import (
	"os"
	"testing"

	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThemeConfigIndexer(t *testing.T) {
	tempDir := t.TempDir()

	// Create a new indexer
	indexer, err := NewThemeConfigIndexer(tempDir)
	require.NoError(t, err)
	defer func() { _ = indexer.Close() }()

	// Load test theme.json file
	bytes, err := os.ReadFile("testdata/theme.json")
	require.NoError(t, err)

	// Index the file
	filePath := "testdata/theme.json"
	err = indexer.Index(indexerpkg.NewParsedFile(filePath, bytes))
	require.NoError(t, err)

	// Test GetThemeConfigFields
	keys, err := indexer.GetThemeConfigFields()
	require.NoError(t, err)
	assert.NotEmpty(t, keys)

	// Test GetThemeConfigField for a specific key
	fields, err := indexer.GetThemeConfigField("sw-color-brand-primary")
	require.NoError(t, err)
	assert.NotEmpty(t, fields)
	assert.Equal(t, "Primary colour", fields[0].Label["en-GB"])
	assert.Equal(t, "color", fields[0].Type)

	// Test GetAllThemeConfigFields
	allFields, err := indexer.GetAllThemeConfigFields()
	require.NoError(t, err)
	assert.NotEmpty(t, allFields)

	// Test removing a file
	err = indexer.RemovedFiles([]string{filePath})
	require.NoError(t, err)

	// Verify the file was removed
	emptyKeys, err := indexer.GetThemeConfigFields()
	require.NoError(t, err)
	assert.Empty(t, emptyKeys)
}
