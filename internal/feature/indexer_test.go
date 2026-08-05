package feature

import (
	"os"
	"path/filepath"
	"testing"

	indexerpkg "github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeatureIndexer_Index(t *testing.T) {
	// Create a temporary directory for the test database
	tempDir, err := os.MkdirTemp("", "feature-indexer-test")
	require.NoError(t, err, "Creating temp directory should not fail")
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create a new indexer
	indexer, err := NewFeatureIndexer(tempDir)
	require.NoError(t, err, "Creating indexer should not fail")
	defer func() { _ = indexer.Close() }()

	// Read the test file
	filePath := filepath.Join("testdata", "feature.yaml")
	content, err := os.ReadFile(filePath)
	require.NoError(t, err, "Reading test file should not fail")

	// Index the file
	err = indexer.Index(indexerpkg.NewParsedFile(filePath, content))
	require.NoError(t, err, "Indexing file should not fail")

	// Verify that all 8 features were indexed
	allFeatures, err := indexer.GetAllFeatures()
	require.NoError(t, err, "Getting all features should not fail")
	assert.Len(t, allFeatures, 8, "Should have indexed 8 features")

	// Verify specific feature details
	v650Feature, err := indexer.GetFeatureByName("v6.5.0.0")
	require.NoError(t, err, "Getting feature by name should not fail")
	require.Len(t, v650Feature, 1, "Should find exactly one v6.5.0.0 feature")

	feature := v650Feature[0]
	assert.Equal(t, "v6.5.0.0", feature.Name, "Feature name should match")
	assert.Equal(t, 4, feature.Line, "Line number should be 4") // Line number is 1-based

	// Test a feature from the middle of the file
	accessFeature, err := indexer.GetFeatureByName("ACCESSIBILITY_TWEAKS")
	require.NoError(t, err, "Getting feature by name should not fail")
	assert.Len(t, accessFeature, 1, "Should find exactly one ACCESSIBILITY_TWEAKS feature")

	// Test the last feature
	lastFeature, err := indexer.GetFeatureByName("FLOW_EXECUTION_AFTER_BUSINESS_PROCESS")
	require.NoError(t, err, "Getting feature by name should not fail")
	assert.Len(t, lastFeature, 1, "Should find exactly one FLOW_EXECUTION_AFTER_BUSINESS_PROCESS feature")
}

func TestFeatureIndexer_RemovedFiles(t *testing.T) {
	// Create a temporary directory for the test database
	tempDir, err := os.MkdirTemp("", "feature-indexer-test")
	require.NoError(t, err, "Creating temp directory should not fail")
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create a new indexer
	indexer, err := NewFeatureIndexer(tempDir)
	require.NoError(t, err, "Creating indexer should not fail")
	defer func() { _ = indexer.Close() }()

	// Read the test file
	filePath := filepath.Join("testdata", "feature.yaml")
	content, err := os.ReadFile(filePath)
	require.NoError(t, err, "Reading test file should not fail")

	// Index the file
	err = indexer.Index(indexerpkg.NewParsedFile(filePath, content))
	require.NoError(t, err, "Indexing file should not fail")

	// Verify features were indexed
	allFeatures, err := indexer.GetAllFeatures()
	require.NoError(t, err, "Getting all features should not fail")
	assert.NotEmpty(t, allFeatures, "Should have indexed at least one feature")

	// Remove the file
	err = indexer.RemovedFiles([]string{filePath})
	require.NoError(t, err, "Removing files should not fail")

	// Verify all features were removed
	allFeatures, err = indexer.GetAllFeatures()
	require.NoError(t, err, "Getting all features should not fail")
	assert.Empty(t, allFeatures, "All features should be removed after file deletion")
}
