//! Persisted cache versioning.

use std::fs;
use std::io::ErrorKind;
use std::path::Path;

use crate::error::{Error, Result, ResultExt};

/// The current version of persisted index contents. Bump it whenever a schema
/// change or extraction-semantics change requires existing workspace caches
/// to be rebuilt.
pub const INDEX_VERSION: u32 = 188;

const VERSION_FILE_NAME: &str = "index_version";
const CONFIGURATION_FINGERPRINT_FILE_NAME: &str = "configuration_fingerprint";

/// The exact contents of a current version file.
pub fn index_version_marker() -> String {
    INDEX_VERSION.to_string()
}

/// The read-only half of cache migration. Lets lightweight CLI commands
/// reject stale catalogs without constructing a workspace.
pub fn cache_version_current(cache_dir: impl AsRef<Path>) -> Result<bool> {
    match fs::read_to_string(cache_dir.as_ref().join(VERSION_FILE_NAME)) {
        Ok(data) => Ok(data.trim() == index_version_marker()),
        Err(error) if error.kind() == ErrorKind::NotFound => Ok(false),
        Err(error) => Err(error.into()),
    }
}

/// Checks the cache version and clears the cache if it is missing, corrupted
/// or outdated. Returns `true` when the cache was cleared and must be rebuilt.
pub fn check_and_migrate_cache(cache_dir: impl AsRef<Path>) -> Result<bool> {
    let cache_dir = cache_dir.as_ref();
    let version_file = cache_dir.join(VERSION_FILE_NAME);
    match fs::read_to_string(&version_file) {
        Ok(data) if data.trim() == index_version_marker() => return Ok(false),
        Ok(_) => {}
        Err(error) if error.kind() == ErrorKind::NotFound => {}
        Err(error) => return Err(Error::from(error).context("failed to read version file")),
    }
    clear_cache_dir(cache_dir).context("failed to clear cache")?;
    write_version(&version_file).context("failed to write version")?;
    Ok(true)
}

/// Invalidates the complete workspace cache when structural configuration
/// changes. File hashes are shared by all indexers; without this, re-enabling
/// an indexer could incorrectly skip files which changed while it was
/// disabled.
pub fn check_and_migrate_configuration(
    cache_dir: impl AsRef<Path>,
    fingerprint: &str,
) -> Result<bool> {
    if fingerprint.trim().is_empty() {
        return Err(Error::msg("configuration fingerprint must not be empty"));
    }
    let cache_dir = cache_dir.as_ref();
    let path = cache_dir.join(CONFIGURATION_FINGERPRINT_FILE_NAME);
    match fs::read_to_string(&path) {
        Ok(data) if data.trim() == fingerprint => return Ok(false),
        Ok(_) => {}
        Err(error) if error.kind() == ErrorKind::NotFound => {}
        Err(error) => return Err(Error::from(error).context("read configuration fingerprint")),
    }
    clear_cache_dir(cache_dir).context("clear structurally stale cache")?;
    write_version(&cache_dir.join(VERSION_FILE_NAME)).context("restore index version")?;
    fs::write(&path, format!("{fingerprint}\n")).context("write configuration fingerprint")?;
    Ok(true)
}

/// Removes everything inside the cache directory, creating it when missing.
fn clear_cache_dir(cache_dir: &Path) -> Result<()> {
    let entries = match fs::read_dir(cache_dir) {
        Ok(entries) => entries,
        Err(error) if error.kind() == ErrorKind::NotFound => {
            fs::create_dir_all(cache_dir)?;
            return Ok(());
        }
        Err(error) => return Err(error.into()),
    };
    for entry in entries {
        let entry = entry?;
        let path = entry.path();
        let result = if entry.file_type()?.is_dir() {
            fs::remove_dir_all(&path)
        } else {
            fs::remove_file(&path)
        };
        result.with_context(|| format!("failed to remove {}", path.display()))?;
    }
    Ok(())
}

fn write_version(version_file: &Path) -> std::io::Result<()> {
    fs::write(version_file, index_version_marker())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn index_version_marker_is_current() {
        // Update together with every INDEX_VERSION bump.
        assert_eq!(INDEX_VERSION, 188);
        assert_eq!(index_version_marker(), "188");
    }

    #[test]
    fn fresh_cache_is_cleared_and_versioned() {
        let cache = tempfile::tempdir().unwrap();
        std::fs::write(cache.path().join("stale.db"), "old").unwrap();
        assert!(check_and_migrate_cache(cache.path()).unwrap());
        assert!(!cache.path().join("stale.db").exists());
        assert_eq!(
            std::fs::read_to_string(cache.path().join(VERSION_FILE_NAME)).unwrap(),
            index_version_marker()
        );
    }

    #[test]
    fn missing_cache_directory_is_created() {
        let cache = tempfile::tempdir().unwrap();
        let nested = cache.path().join("nested");
        assert!(check_and_migrate_cache(&nested).unwrap());
        assert!(cache_version_current(&nested).unwrap());
    }

    #[test]
    fn matching_version_keeps_cache() {
        let cache = tempfile::tempdir().unwrap();
        std::fs::write(cache.path().join(VERSION_FILE_NAME), index_version_marker()).unwrap();
        std::fs::write(cache.path().join("indexes.db"), "data").unwrap();
        assert!(!check_and_migrate_cache(cache.path()).unwrap());
        assert!(cache.path().join("indexes.db").exists());
        assert!(cache_version_current(cache.path()).unwrap());
    }

    #[test]
    fn older_versions_are_rebuilt() {
        for stored in ["1", "183", "181-rs"] {
            let cache = tempfile::tempdir().unwrap();
            std::fs::write(cache.path().join(VERSION_FILE_NAME), stored).unwrap();
            std::fs::write(cache.path().join("indexes.db"), "data").unwrap();
            assert!(!cache_version_current(cache.path()).unwrap());
            assert!(check_and_migrate_cache(cache.path()).unwrap());
            assert!(!cache.path().join("indexes.db").exists());
            assert_eq!(
                std::fs::read_to_string(cache.path().join(VERSION_FILE_NAME)).unwrap(),
                index_version_marker()
            );
        }
    }

    #[test]
    fn corrupted_version_is_rebuilt() {
        let cache = tempfile::tempdir().unwrap();
        std::fs::write(cache.path().join(VERSION_FILE_NAME), "not-a-number").unwrap();
        assert!(check_and_migrate_cache(cache.path()).unwrap());
        assert_eq!(
            std::fs::read_to_string(cache.path().join(VERSION_FILE_NAME)).unwrap(),
            index_version_marker()
        );
    }

    #[test]
    fn clears_subdirectories() {
        let cache = tempfile::tempdir().unwrap();
        let sub = cache.path().join("subdir");
        std::fs::create_dir_all(&sub).unwrap();
        std::fs::write(sub.join("nested.db"), "nested").unwrap();
        assert!(check_and_migrate_cache(cache.path()).unwrap());
        assert!(!sub.exists());
    }

    #[test]
    fn configuration_invalidates_only_on_fingerprint_change() {
        let cache = tempfile::tempdir().unwrap();
        assert!(check_and_migrate_cache(cache.path()).unwrap());
        assert!(check_and_migrate_configuration(cache.path(), "first").unwrap());
        let marker = cache.path().join("indexes.db");
        std::fs::write(&marker, "cache").unwrap();
        assert!(!check_and_migrate_configuration(cache.path(), "first").unwrap());
        assert!(marker.exists());
        assert!(check_and_migrate_configuration(cache.path(), "second").unwrap());
        assert!(!marker.exists());
        assert!(cache_version_current(cache.path()).unwrap());
        assert!(check_and_migrate_configuration(cache.path(), "  ").is_err());
    }
}
