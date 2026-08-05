package indexer

import (
	"fmt"
	"os"
	"path"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

type TestItem struct {
	Name string `json:"name"`
}

func TestIndexer(t *testing.T) {
	tmpDir := t.TempDir()

	assert.NoError(t, os.MkdirAll(tmpDir, 0755))

	indexer, err := NewDataIndexer[TestItem](path.Join(tmpDir, "test.db"))

	assert.NoError(t, err)

	assert.NoError(t, indexer.SaveItem("core/test.twig", "test.twig", TestItem{Name: "test"}))
	assert.NoError(t, indexer.SaveItem("plugin/test.twig", "test.twig", TestItem{Name: "test2"}))

	keys, err := indexer.GetAllKeys()
	assert.NoError(t, err)
	assert.Equal(t, []string{"test.twig"}, keys)

	values, err := indexer.GetValues("test.twig")
	assert.NoError(t, err)
	assert.Equal(t, []TestItem{{Name: "test"}, {Name: "test2"}}, values)

	assert.NoError(t, indexer.DeleteByFilePath("core/test.twig"))

	values, err = indexer.GetValues("test.twig")
	assert.NoError(t, err)
	assert.Equal(t, []TestItem{{Name: "test2"}}, values)

	assert.NoError(t, indexer.DeleteByFilePath("plugin/test.twig"))

	values, err = indexer.GetValues("test.twig")

	assert.NoError(t, err)
	assert.Empty(t, values)
}

func TestDataIndexerHasAnyKeyExceptFold(t *testing.T) {
	tmpDir := t.TempDir()
	idx, err := NewDataIndexer[TestItem](path.Join(tmpDir, "existence.db"))
	assert.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, idx.Close()) })

	present, err := idx.HasAnyKeyExceptFold("current.twig")
	assert.NoError(t, err)
	assert.False(t, present)
	assert.NoError(t, idx.SaveItem(
		"current.twig", "current.twig", TestItem{Name: "current"},
	))
	present, err = idx.HasAnyKeyExceptFold("CURRENT.TWIG")
	assert.NoError(t, err)
	assert.False(t, present)
	assert.NoError(t, idx.SaveItem(
		"other.twig", "other.twig", TestItem{Name: "other"},
	))
	present, err = idx.HasAnyKeyExceptFold("Current.twig")
	assert.NoError(t, err)
	assert.True(t, present)

	// Exercise the cached-key path as well as the direct existence query.
	_, err = idx.GetAllKeys()
	assert.NoError(t, err)
	present, err = idx.HasAnyKeyExceptFold("current.twig", "OTHER.TWIG")
	assert.NoError(t, err)
	assert.False(t, present)
}

func TestBatchWrite(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir := t.TempDir()

	// Create the indexer
	indexer, err := NewDataIndexer[TestItem](path.Join(tmpDir, "batch_test.db"))
	assert.NoError(t, err)

	// Prepare batch data
	batchData := map[string]map[string]TestItem{
		"file1.twig": {
			"template": TestItem{Name: "template1"},
			"block":    TestItem{Name: "block1"},
		},
		"file2.twig": {
			"template": TestItem{Name: "template2"},
			"block":    TestItem{Name: "block2"},
		},
		"file3.twig": {
			"template": TestItem{Name: "template3"},
		},
	}

	// Perform batch write
	assert.NoError(t, indexer.BatchSaveItems(batchData))

	// Verify keys
	keys, err := indexer.GetAllKeys()
	assert.NoError(t, err)
	// Sort keys for consistent comparison
	sort.Strings(keys)
	assert.Equal(t, []string{"block", "template"}, keys)

	// Verify template values
	templateValues, err := indexer.GetValues("template")
	assert.NoError(t, err)
	assert.Len(t, templateValues, 3)

	// Verify block values
	blockValues, err := indexer.GetValues("block")
	assert.NoError(t, err)
	assert.Len(t, blockValues, 2)

	// Delete one file and verify remaining data
	assert.NoError(t, indexer.DeleteByFilePath("file1.twig"))

	// Check template values after deletion
	templateValues, err = indexer.GetValues("template")
	assert.NoError(t, err)
	assert.Len(t, templateValues, 2)

	// Check block values after deletion
	blockValues, err = indexer.GetValues("block")
	assert.NoError(t, err)
	assert.Len(t, blockValues, 1)

	// Clean up
	assert.NoError(t, indexer.Close())
}

func TestBatchDelete(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the indexer
	indexer, err := NewDataIndexer[TestItem](path.Join(tmpDir, "batch_delete_test.db"))
	assert.NoError(t, err)

	// Create test data with multiple files
	for i := 1; i <= 5; i++ {
		filePath := fmt.Sprintf("theme/file%d.twig", i)
		assert.NoError(t, indexer.SaveItem(filePath, "template", TestItem{Name: fmt.Sprintf("template%d", i)}))

		if i <= 3 {
			// Add block items to first 3 files only
			assert.NoError(t, indexer.SaveItem(filePath, "block", TestItem{Name: fmt.Sprintf("block%d", i)}))
		}
	}

	// Verify initial counts
	templateValues, err := indexer.GetValues("template")
	assert.NoError(t, err)
	assert.Len(t, templateValues, 5)

	blockValues, err := indexer.GetValues("block")
	assert.NoError(t, err)
	assert.Len(t, blockValues, 3)

	// Perform batch delete for files 1, 3, and 5
	filesToDelete := []string{
		"theme/file1.twig",
		"theme/file3.twig",
		"theme/file5.twig",
	}
	assert.NoError(t, indexer.BatchDeleteByFilePaths(filesToDelete))

	// Verify counts after batch deletion
	templateValues, err = indexer.GetValues("template")
	assert.NoError(t, err)
	assert.Len(t, templateValues, 2, "Should have 2 template items remaining")

	blockValues, err = indexer.GetValues("block")
	assert.NoError(t, err)
	assert.Len(t, blockValues, 1, "Should have 1 block item remaining")

	// Verify that only files 2 and 4 remain by trying to delete them
	assert.NoError(t, indexer.DeleteByFilePath("theme/file2.twig"))
	assert.NoError(t, indexer.DeleteByFilePath("theme/file4.twig"))

	// Verify all items are gone
	templateValues, err = indexer.GetValues("template")
	assert.NoError(t, err)
	assert.Empty(t, templateValues, "All template items should be gone")

	blockValues, err = indexer.GetValues("block")
	assert.NoError(t, err)
	assert.Empty(t, blockValues, "All block items should be gone")

	// Try batch deleting non-existent files (should not error)
	nonExistentFiles := []string{"theme/nonexistent1.twig", "theme/nonexistent2.twig"}
	assert.NoError(t, indexer.BatchDeleteByFilePaths(nonExistentFiles))

	// Clean up
	assert.NoError(t, indexer.Close())
}
