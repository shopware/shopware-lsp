//! Revisions for files analyzers read from disk: a stamp changes when the
//! file appears, disappears or is rewritten.

use std::hash::{DefaultHasher, Hash, Hasher};

/// A hash of the file's size and modification time, or of its absence.
pub(crate) fn file_stamp(path: &str) -> u64 {
    let mut hasher = DefaultHasher::new();
    path.hash(&mut hasher);
    std::fs::metadata(path)
        .ok()
        .map(|metadata| (metadata.len(), metadata.modified().ok()))
        .hash(&mut hasher);
    hasher.finish()
}

/// A hash of the directory's `.json` files by name, size and modification
/// time.
pub(crate) fn json_directory_stamp(directory: &str) -> u64 {
    let mut hasher = DefaultHasher::new();
    if let Ok(entries) = std::fs::read_dir(directory) {
        let mut stamps: Vec<_> = entries
            .flatten()
            .filter(|entry| entry.file_name().to_string_lossy().ends_with(".json"))
            .filter_map(|entry| {
                let metadata = entry.metadata().ok()?;
                Some((entry.file_name(), metadata.len(), metadata.modified().ok()))
            })
            .collect();
        stamps.sort();
        stamps.hash(&mut hasher);
    }
    hasher.finish()
}
