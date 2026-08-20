package indexer

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckAndMigrateCache_FreshCache(t *testing.T) {
	assert.Equal(t, 167, IndexVersion)
	cacheDir := t.TempDir()

	// Fresh cache should be marked as cleared (needs rebuild)
	cleared, err := CheckAndMigrateCache(cacheDir)
	require.NoError(t, err)
	assert.True(t, cleared, "Fresh cache should report as cleared")

	// Version file should exist now
	versionFile := filepath.Join(cacheDir, versionFileName)
	_, err = os.Stat(versionFile)
	require.NoError(t, err, "Version file should exist after migration")
}

func TestCheckAndMigrateCache_MatchingVersion(t *testing.T) {
	cacheDir := t.TempDir()

	// Write current version
	versionFile := filepath.Join(cacheDir, versionFileName)
	err := os.WriteFile(versionFile, []byte(strconv.Itoa(IndexVersion)), 0644)
	require.NoError(t, err)

	// Create a dummy file to verify it's not deleted
	dummyFile := filepath.Join(cacheDir, "test.db")
	err = os.WriteFile(dummyFile, []byte("test data"), 0644)
	require.NoError(t, err)

	// Matching version should not clear cache
	cleared, err := CheckAndMigrateCache(cacheDir)
	require.NoError(t, err)
	assert.False(t, cleared, "Matching version should not clear cache")
	current, err := CacheVersionCurrent(cacheDir)
	require.NoError(t, err)
	assert.True(t, current)

	// Dummy file should still exist
	_, err = os.Stat(dummyFile)
	require.NoError(t, err, "Existing files should not be deleted when version matches")
}

func TestCheckAndMigrateCache_OldVersion(t *testing.T) {
	cacheDir := t.TempDir()

	// Write old version (0)
	versionFile := filepath.Join(cacheDir, versionFileName)
	err := os.WriteFile(versionFile, []byte("0"), 0644)
	require.NoError(t, err)

	// Create a dummy file to verify it gets deleted
	dummyFile := filepath.Join(cacheDir, "test.db")
	err = os.WriteFile(dummyFile, []byte("test data"), 0644)
	require.NoError(t, err)
	current, err := CacheVersionCurrent(cacheDir)
	require.NoError(t, err)
	assert.False(t, current)

	// Old version should trigger cache clear
	cleared, err := CheckAndMigrateCache(cacheDir)
	require.NoError(t, err)
	assert.True(t, cleared, "Old version should clear cache")

	// Dummy file should be deleted
	_, err = os.Stat(dummyFile)
	assert.True(t, os.IsNotExist(err), "Old files should be deleted on version mismatch")

	// Version file should be updated
	data, err := os.ReadFile(versionFile)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(IndexVersion), string(data), "Version file should be updated to current version")
}

func TestCheckAndMigrateCache_CorruptedVersion(t *testing.T) {
	cacheDir := t.TempDir()

	// Write corrupted version
	versionFile := filepath.Join(cacheDir, versionFileName)
	err := os.WriteFile(versionFile, []byte("not-a-number"), 0644)
	require.NoError(t, err)

	// Corrupted version should trigger cache clear
	cleared, err := CheckAndMigrateCache(cacheDir)
	require.NoError(t, err)
	assert.True(t, cleared, "Corrupted version should clear cache")

	// Version file should be fixed
	data, err := os.ReadFile(versionFile)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(IndexVersion), string(data), "Version file should be fixed")
}

func TestCheckAndMigrateCache_ClearsSubdirectories(t *testing.T) {
	cacheDir := t.TempDir()

	// Create a subdirectory with files
	subDir := filepath.Join(cacheDir, "subdir")
	err := os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	subFile := filepath.Join(subDir, "nested.db")
	err = os.WriteFile(subFile, []byte("nested data"), 0644)
	require.NoError(t, err)

	// Fresh cache should clear everything including subdirs
	cleared, err := CheckAndMigrateCache(cacheDir)
	require.NoError(t, err)
	assert.True(t, cleared)

	// Subdirectory should be deleted
	_, err = os.Stat(subDir)
	assert.True(t, os.IsNotExist(err), "Subdirectories should be deleted")
}

func TestCheckAndMigrateConfigurationInvalidatesOnlyOnFingerprintChange(t *testing.T) {
	cacheDir := t.TempDir()
	rebuilt, err := CheckAndMigrateCache(cacheDir)
	require.NoError(t, err)
	require.True(t, rebuilt)

	rebuilt, err = CheckAndMigrateConfiguration(cacheDir, "first")
	require.NoError(t, err)
	require.True(t, rebuilt)
	marker := filepath.Join(cacheDir, "indexes.db")
	require.NoError(t, os.WriteFile(marker, []byte("cache"), 0o644))

	rebuilt, err = CheckAndMigrateConfiguration(cacheDir, "first")
	require.NoError(t, err)
	require.False(t, rebuilt)
	_, err = os.Stat(marker)
	require.NoError(t, err)

	rebuilt, err = CheckAndMigrateConfiguration(cacheDir, "second")
	require.NoError(t, err)
	require.True(t, rebuilt)
	_, err = os.Stat(marker)
	require.True(t, os.IsNotExist(err))
	current, err := CacheVersionCurrent(cacheDir)
	require.NoError(t, err)
	require.True(t, current)
}
