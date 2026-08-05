package indexer

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

type testStruct struct {
	Name  string
	Value int
}

func TestMessagePackEncoderReusesBoundedBuffers(t *testing.T) {
	clearMessagePackBuffers()
	t.Cleanup(clearMessagePackBuffers)

	encoder := acquireMessagePackEncoder()
	first := testStruct{Name: "first", Value: 1}
	encoded, err := encoder.encode(first)
	require.NoError(t, err)
	expected, err := msgpack.Marshal(first)
	require.NoError(t, err)
	require.Equal(t, expected, encoded)
	firstStorage := unsafe.SliceData(encoded)

	second := testStruct{Name: "next", Value: 2}
	encoded, err = encoder.encode(second)
	require.NoError(t, err)
	expected, err = msgpack.Marshal(second)
	require.NoError(t, err)
	require.Equal(t, expected, encoded)
	require.Equal(t, firstStorage, unsafe.SliceData(encoded))
	encoder.release()
	require.Len(t, messagePackEncoders, 1)

	oversized := acquireMessagePackEncoder()
	require.Same(t, encoder, oversized)
	_, err = oversized.encode(strings.Repeat(
		"x",
		maxPooledMessagePackBuffer+1,
	))
	require.NoError(t, err)
	oversized.release()
	require.Empty(t, messagePackEncoders)
}

// Helper function to set up a temporary database for testing
func setupTestDB[T any](t *testing.T) (*DataIndexer[T], func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	indexer, err := NewDataIndexer[T](dbPath)
	require.NoError(t, err, "Failed to create new data indexer")

	cleanup := func() {
		err := indexer.Close()
		if err != nil {
			// Log or handle the error during cleanup if necessary, but don't fail the test here
			t.Logf("Warning: error closing test database: %v", err)
		}
		// os.RemoveAll(tempDir) // t.TempDir() handles cleanup
	}

	return indexer, cleanup
}

func TestDataIndexer_GetAllValues_Empty(t *testing.T) {
	indexer, cleanup := setupTestDB[testStruct](t)
	defer cleanup()

	values, err := indexer.GetAllValues()
	require.NoError(t, err, "GetAllValues failed on empty DB")
	assert.Empty(t, values, "Expected empty slice for empty DB")
}

func TestDataIndexer_GetAllValues_WithData(t *testing.T) {
	indexer, cleanup := setupTestDB[testStruct](t)
	defer cleanup()

	// Prepare test data
	item1 := testStruct{Name: "ItemA", Value: 10}
	item2 := testStruct{Name: "ItemB", Value: 20}
	item3 := testStruct{Name: "ItemC", Value: 30} // Different key, same file
	item4 := testStruct{Name: "ItemD", Value: 40} // Different file

	itemsToSave := map[string]map[string]testStruct{
		"file1.txt": {
			"keyA": item1,
			"keyB": item2,
		},
		"file2.txt": {
			"keyC": item3,
		},
		"file3.txt": {
			"keyA": item4, // Same key as item1, but different file/value
		},
	}

	// Save items
	err := indexer.BatchSaveItems(itemsToSave)
	require.NoError(t, err, "BatchSaveItems failed")

	// Retrieve all values
	values, err := indexer.GetAllValues()
	require.NoError(t, err, "GetAllValues failed")

	// Assertions
	expectedValues := []testStruct{item1, item2, item3, item4}
	assert.Len(t, values, len(expectedValues), "Incorrect number of values returned")

	// Check that all expected values are present (order doesn't matter)
	assert.ElementsMatch(t, expectedValues, values, "Returned values do not match expected values")
}

func TestDataIndexerValueViewsReuseCachedCatalogs(t *testing.T) {
	index, cleanup := setupTestDB[testStruct](t)
	defer cleanup()
	require.NoError(t, index.BatchSaveItems(
		map[string]map[string]testStruct{
			"file.txt": {"key": {Name: "cached", Value: 1}},
		},
	))

	allFirst, err := index.GetAllValuesView()
	require.NoError(t, err)
	allSecond, err := index.GetAllValuesView()
	require.NoError(t, err)
	require.Len(t, allFirst, 1)
	require.Equal(t, unsafe.SliceData(allFirst), unsafe.SliceData(allSecond))

	keyFirst, err := index.GetValuesView("key")
	require.NoError(t, err)
	keySecond, err := index.GetValuesView("key")
	require.NoError(t, err)
	require.Len(t, keyFirst, 1)
	require.Equal(t, unsafe.SliceData(keyFirst), unsafe.SliceData(keySecond))

	copyValues, err := index.GetAllValues()
	require.NoError(t, err)
	copyValues[0] = testStruct{Name: "changed"}
	require.Equal(t, "cached", allFirst[0].Name)
}

func TestDataIndexerVisitAllEncodedValues(t *testing.T) {
	index, cleanup := setupTestDB[testStruct](t)
	defer cleanup()

	expected := []testStruct{
		{Name: "first", Value: 1},
		{Name: "second", Value: 2},
	}
	require.NoError(t, index.BatchSaveItems(
		map[string]map[string]testStruct{
			"/first":  {"first": expected[0]},
			"/second": {"second": expected[1]},
		},
	))
	count, err := index.CountAllValues()
	require.NoError(t, err)
	require.Equal(t, len(expected), count)

	var visited []testStruct
	require.NoError(t, index.VisitAllEncodedValues(func(encoded []byte) error {
		var value testStruct
		if err := msgpack.Unmarshal(encoded, &value); err != nil {
			return err
		}
		visited = append(visited, value)
		return nil
	}))
	require.ElementsMatch(t, expected, visited)

	sentinel := errors.New("stop visiting")
	require.ErrorIs(t, index.VisitAllEncodedValues(
		func([]byte) error { return sentinel },
	), sentinel)
	require.NoError(t, index.VisitAllEncodedValues(nil))
}

func TestDataIndexer_GetAllValues_SpecificKeyRetrievalStillWorks(t *testing.T) {
	// This test ensures GetValues (key-specific) still works after modifying GetAllValues
	indexer, cleanup := setupTestDB[testStruct](t)
	defer cleanup()

	item1 := testStruct{Name: "ItemA1", Value: 10}
	item2 := testStruct{Name: "ItemA2", Value: 20}
	item3 := testStruct{Name: "ItemB1", Value: 30}

	itemsToSave := map[string]map[string]testStruct{
		"file1.txt": {
			"keyA": item1,
		},
		"file2.txt": {
			"keyA": item2,
			"keyB": item3,
		},
	}

	err := indexer.BatchSaveItems(itemsToSave)
	require.NoError(t, err, "BatchSaveItems failed")

	// Retrieve values for "keyA"
	keyAValues, err := indexer.GetValues("keyA")
	require.NoError(t, err, "GetValues(keyA) failed")
	assert.Len(t, keyAValues, 2, "Incorrect number of values for keyA")
	assert.ElementsMatch(t, []testStruct{item1, item2}, keyAValues, "Incorrect values returned for keyA")

	// Retrieve values for "keyB"
	keyBValues, err := indexer.GetValues("keyB")
	require.NoError(t, err, "GetValues(keyB) failed")
	assert.Len(t, keyBValues, 1, "Incorrect number of values for keyB")
	assert.ElementsMatch(t, []testStruct{item3}, keyBValues, "Incorrect values returned for keyB")
}

func TestDataIndexer_GetAllKeysByPath(t *testing.T) {
	indexer, cleanup := setupTestDB[testStruct](t)
	defer cleanup()

	// Prepare test data with different keys and files
	item1 := testStruct{Name: "Item1", Value: 10}
	item2 := testStruct{Name: "Item2", Value: 20}
	item3 := testStruct{Name: "Item3", Value: 30}
	item4 := testStruct{Name: "Item4", Value: 40}
	item5 := testStruct{Name: "Item5", Value: 50}

	// Structure: map[filePath]map[key]value
	itemsToSave := map[string]map[string]testStruct{
		"file1.txt": {
			"keyA": item1,
			"keyB": item2,
			"keyC": item3,
		},
		"file2.txt": {
			"keyA": item4,
			"keyD": item5,
		},
	}

	// Save items
	err := indexer.BatchSaveItems(itemsToSave)
	require.NoError(t, err, "BatchSaveItems failed")

	// Test GetAllKeysByPath for file1.txt
	file1Keys, err := indexer.GetAllKeysByPath("file1.txt")
	require.NoError(t, err, "GetAllKeysByPath failed for file1.txt")
	assert.Len(t, file1Keys, 3, "Incorrect number of keys for file1.txt")
	assert.ElementsMatch(t, []string{"keyA", "keyB", "keyC"}, file1Keys, "Incorrect keys returned for file1.txt")

	// Test GetAllKeysByPath for file2.txt
	file2Keys, err := indexer.GetAllKeysByPath("file2.txt")
	require.NoError(t, err, "GetAllKeysByPath failed for file2.txt")
	assert.Len(t, file2Keys, 2, "Incorrect number of keys for file2.txt")
	assert.ElementsMatch(t, []string{"keyA", "keyD"}, file2Keys, "Incorrect keys returned for file2.txt")

	// Test GetAllKeysByPath for non-existent file
	nonExistentKeys, err := indexer.GetAllKeysByPath("non-existent.txt")
	require.NoError(t, err, "GetAllKeysByPath should not error for non-existent file")
	assert.Empty(t, nonExistentKeys, "Expected empty keys for non-existent file")
}

func TestDataIndexer_BatchSaveItems_DeletesOldEntries(t *testing.T) {
	indexer, cleanup := setupTestDB[testStruct](t)
	defer cleanup()

	// First save
	item1 := testStruct{Name: "ItemA", Value: 10}
	err := indexer.BatchSaveItems(map[string]map[string]testStruct{
		"file1.txt": {
			"keyA": item1,
		},
	})
	require.NoError(t, err, "First BatchSaveItems failed")

	// Verify first save
	values, err := indexer.GetValues("keyA")
	require.NoError(t, err)
	assert.Len(t, values, 1, "Expected 1 value after first save")

	// Second save to the same file path - should replace, not add
	item2 := testStruct{Name: "ItemB", Value: 20}
	err = indexer.BatchSaveItems(map[string]map[string]testStruct{
		"file1.txt": {
			"keyA": item2,
		},
	})
	require.NoError(t, err, "Second BatchSaveItems failed")

	// Verify second save replaced the first - should still be 1 value, not 2
	values, err = indexer.GetValues("keyA")
	require.NoError(t, err)
	assert.Len(t, values, 1, "Expected 1 value after second save (should replace, not duplicate)")
	assert.Equal(t, "ItemB", values[0].Name, "Should have the updated value")

	// Third save with different key to same file - should replace all entries for that file
	item3 := testStruct{Name: "ItemC", Value: 30}
	err = indexer.BatchSaveItems(map[string]map[string]testStruct{
		"file1.txt": {
			"keyB": item3,
		},
	})
	require.NoError(t, err, "Third BatchSaveItems failed")

	// keyA should now be empty since file1.txt entries were deleted
	valuesA, err := indexer.GetValues("keyA")
	require.NoError(t, err)
	assert.Len(t, valuesA, 0, "keyA should be empty after file1.txt was re-saved with different key")

	// keyB should have the new value
	valuesB, err := indexer.GetValues("keyB")
	require.NoError(t, err)
	assert.Len(t, valuesB, 1, "Expected 1 value for keyB")
	assert.Equal(t, "ItemC", valuesB[0].Name)
}

func TestDataIndexer_ConcurrentAccess(t *testing.T) {
	// This test verifies that multiple connections to the same SQLite database
	// can work concurrently without blocking/hanging (the original BBolt issue)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "concurrent_test.db")

	// Create first indexer
	indexer1, err := NewDataIndexer[testStruct](dbPath)
	require.NoError(t, err, "Failed to create first indexer")
	defer func() { _ = indexer1.Close() }()

	// Create second indexer to the same database (simulating second LSP instance)
	indexer2, err := NewDataIndexer[testStruct](dbPath)
	require.NoError(t, err, "Failed to create second indexer - this would fail with BBolt")
	defer func() { _ = indexer2.Close() }()

	// Use WaitGroup to coordinate concurrent operations
	var wg sync.WaitGroup
	errChan := make(chan error, 4)

	// Indexer 1 writes
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			err := indexer1.SaveItem("file1.txt", "key1", testStruct{Name: "from-indexer1", Value: i})
			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	// Indexer 2 writes
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			err := indexer2.SaveItem("file2.txt", "key2", testStruct{Name: "from-indexer2", Value: i})
			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	// Indexer 1 reads
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			_, err := indexer1.GetAllValues()
			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	// Indexer 2 reads
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			_, err := indexer2.GetAllKeys()
			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		t.Errorf("Concurrent operation failed: %v", err)
	}

	// Verify data integrity - should have data from both indexers
	values, err := indexer1.GetAllValues()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(values), 10, "Expected at least 10 values from concurrent writes")
}
