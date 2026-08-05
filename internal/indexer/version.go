package indexer

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// IndexVersion is the current version of persisted index contents. Bump it
// whenever a schema change or extraction-semantics change requires existing
// workspace caches to be rebuilt.
const IndexVersion = 158

const versionFileName = "index_version"

// CacheVersionCurrent performs the read-only half of cache migration. It lets
// lightweight CLI commands reject stale catalogs without constructing a
// workspace (which will perform the actual migration when needed).
func CacheVersionCurrent(cacheDir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(cacheDir, versionFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	storedVersion, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, nil
	}
	return storedVersion == IndexVersion, nil
}

// CheckAndMigrateCache checks the cache version and clears it if outdated.
// Returns true if the cache was cleared and needs to be rebuilt.
func CheckAndMigrateCache(cacheDir string) (bool, error) {
	versionFile := filepath.Join(cacheDir, versionFileName)

	// Read current version from file
	data, err := os.ReadFile(versionFile)
	if err != nil {
		if os.IsNotExist(err) {
			// No version file exists - this is a fresh cache or old format
			// Clear everything and write new version
			if err := clearCacheDir(cacheDir); err != nil {
				return false, fmt.Errorf("failed to clear cache: %w", err)
			}
			if err := writeVersion(versionFile); err != nil {
				return false, fmt.Errorf("failed to write version: %w", err)
			}
			return true, nil
		}
		return false, fmt.Errorf("failed to read version file: %w", err)
	}

	// Parse version
	storedVersion, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		// Corrupted version file - clear and recreate
		if err := clearCacheDir(cacheDir); err != nil {
			return false, fmt.Errorf("failed to clear cache: %w", err)
		}
		if err := writeVersion(versionFile); err != nil {
			return false, fmt.Errorf("failed to write version: %w", err)
		}
		return true, nil
	}

	// Check if version matches
	if storedVersion != IndexVersion {
		// Version mismatch - clear cache and update version
		if err := clearCacheDir(cacheDir); err != nil {
			return false, fmt.Errorf("failed to clear cache: %w", err)
		}
		if err := writeVersion(versionFile); err != nil {
			return false, fmt.Errorf("failed to write version: %w", err)
		}
		return true, nil
	}

	// Version matches, no migration needed
	return false, nil
}

// clearCacheDir removes all files in the cache directory except the directory itself
func clearCacheDir(cacheDir string) error {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			// Directory doesn't exist, create it
			return os.MkdirAll(cacheDir, 0755)
		}
		return err
	}

	for _, entry := range entries {
		path := filepath.Join(cacheDir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}

	return nil
}

func writeVersion(versionFile string) error {
	return os.WriteFile(versionFile, []byte(strconv.Itoa(IndexVersion)), 0644)
}
