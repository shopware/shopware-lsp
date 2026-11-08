//! Materializes the PHP sources of workspace PHAR archives into the cache so
//! they can be indexed like ordinary files.

use std::collections::{HashMap, HashSet};
use std::fs;
use std::io::{ErrorKind, Write};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use swlsp_phar::Archive;

use crate::error::{CancelToken, Error, Result, ResultExt};
use crate::paths::{self, path_within};
use crate::scanner::{FileMeta, Inner};

const PHAR_CACHE_FORMAT_VERSION: i32 = 1;
const MAX_PHAR_PHP_FILES: usize = 100_000;
const MAX_PHAR_PHP_FILE_SIZE: i64 = 128 << 20;
const MAX_PHAR_PHP_SOURCE_SIZE: i64 = 1 << 30;
const PHAR_CACHE_STATE_FILE: &str = "state.json";

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct PharCacheState {
    version: i32,
    archive_path: String,
    archive_size: i64,
    #[serde(rename = "archiveMTime")]
    archive_mtime: i64,
    php_file_count: usize,
    php_source_size: i64,
}

pub(crate) fn is_phar_archive_path(path: &str) -> bool {
    paths::has_ext_fold(path, ".phar")
}

impl Inner {
    pub(crate) fn discover_phar_sources(
        &self,
        cancel: &CancelToken,
        mut archives: Vec<String>,
    ) -> Result<Vec<String>> {
        if archives.is_empty() {
            return Ok(Vec::new());
        }
        archives.sort();
        let mut seen = HashSet::with_capacity(archives.len());
        let mut sources = Vec::new();
        for archive in archives {
            cancel.check()?;
            let canonical =
                canonical_phar_path(&archive).with_context(|| format!("resolve PHAR {archive}"))?;
            if !seen.insert(canonical.clone()) {
                continue;
            }
            match self.materialize_phar_sources(cancel, &canonical) {
                Ok(archive_sources) => sources.extend(archive_sources),
                // Composer bin proxies can use a .phar suffix while being
                // ordinary PHP scripts that load a different archive.
                Err(error) if is_not_archive(&error) => {}
                Err(error) => return Err(error.context(format!("materialize PHAR {canonical}"))),
            }
        }
        Ok(sources)
    }

    fn materialize_phar_sources(
        &self,
        cancel: &CancelToken,
        archive_path: &str,
    ) -> Result<Vec<String>> {
        let metadata = fs::metadata(archive_path)?;
        if !metadata.is_file() {
            return Err(Error::other(swlsp_phar::PharError::NotArchive(
                "archive is not a regular file".into(),
            )));
        }
        let meta = FileMeta::of(&metadata);
        let cache_key = Sha256::digest(archive_path.as_bytes());
        let cache_dir = PathBuf::from(&self.phar_cache).join(hex(&cache_key));
        let mut expected = PharCacheState {
            version: PHAR_CACHE_FORMAT_VERSION,
            archive_path: archive_path.to_owned(),
            archive_size: meta.size,
            archive_mtime: meta.mtime,
            ..PharCacheState::default()
        };
        if let Ok(state) = read_phar_cache_state(&cache_dir)
            && state.version == expected.version
            && state.archive_path == expected.archive_path
            && state.archive_size == expected.archive_size
            && state.archive_mtime == expected.archive_mtime
            && let Ok((files, source_size)) = collect_phar_cache_files(&cache_dir)
            && files.len() == state.php_file_count
            && source_size == state.php_source_size
        {
            return Ok(files);
        }

        let mut archive = Archive::open(archive_path)?;
        let mut entries = Vec::new();
        let mut collisions: HashMap<String, String> = HashMap::new();
        let mut source_size: i64 = 0;
        for entry in archive.entries() {
            cancel.check()?;
            if !paths::ext(&entry.name).eq_ignore_ascii_case(".php") {
                continue;
            }
            if entries.len() >= MAX_PHAR_PHP_FILES {
                return Err(Error::msg(format!(
                    "archive exceeds the PHP source limit of {MAX_PHAR_PHP_FILES} files"
                )));
            }
            if entry.uncompressed_size > MAX_PHAR_PHP_FILE_SIZE {
                return Err(Error::msg(format!(
                    "entry {:?} exceeds the PHP source limit of {MAX_PHAR_PHP_FILE_SIZE} bytes",
                    entry.name
                )));
            }
            if entry.uncompressed_size > MAX_PHAR_PHP_SOURCE_SIZE - source_size {
                return Err(Error::msg(format!(
                    "archive exceeds the PHP source limit of {MAX_PHAR_PHP_SOURCE_SIZE} bytes"
                )));
            }
            let relative = safe_phar_entry_path(&entry.name)?;
            if let Some(previous) = collisions.insert(relative.to_lowercase(), entry.name.clone()) {
                return Err(Error::msg(format!(
                    "entries {previous:?} and {:?} map to the same source path",
                    entry.name
                )));
            }
            source_size += entry.uncompressed_size;
            entries.push((entry.clone(), relative));
        }

        fs::create_dir_all(&self.phar_cache).context("create PHAR source cache")?;
        let temporary = TemporaryDirectory::create(Path::new(&self.phar_cache), ".extract-")
            .context("create temporary PHAR source cache")?;
        let files_root = temporary.path.join("files");
        fs::create_dir_all(&files_root).context("create PHAR files cache")?;
        let files_root_text = files_root.to_string_lossy().into_owned();
        let archive_time = filetime::FileTime::from_last_modification_time(&metadata);
        let mut relatives = Vec::with_capacity(entries.len());
        for (entry, relative) in &entries {
            cancel.check()?;
            let destination = files_root.join(relative);
            if !path_within(&files_root_text, &destination.to_string_lossy()) {
                return Err(Error::msg(format!(
                    "unsafe PHAR entry path {:?}",
                    entry.name
                )));
            }
            if let Some(parent) = destination.parent() {
                fs::create_dir_all(parent)
                    .with_context(|| format!("create parent for {:?}", entry.name))?;
            }
            let mut file = fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(&destination)
                .with_context(|| format!("create source for {:?}", entry.name))?;
            archive.extract(entry, &mut file)?;
            file.flush()?;
            drop(file);
            filetime::set_file_times(&destination, archive_time, archive_time)
                .with_context(|| format!("timestamp source for {:?}", entry.name))?;
            relatives.push(relative.clone());
        }
        archive.close();

        expected.php_file_count = entries.len();
        expected.php_source_size = source_size;
        let state = serde_json::to_vec(&expected)
            .map_err(Error::other)
            .context("encode PHAR cache state")?;
        fs::write(temporary.path.join(PHAR_CACHE_STATE_FILE), state)
            .context("write PHAR cache state")?;
        temporary.publish(&cache_dir)?;

        let final_root = cache_dir.join("files");
        let mut extracted: Vec<String> = relatives
            .iter()
            .map(|relative| final_root.join(relative).to_string_lossy().into_owned())
            .collect();
        extracted.sort();
        Ok(extracted)
    }
}

fn is_not_archive(error: &Error) -> bool {
    error.any(&|error| match error {
        Error::Other(inner) => inner
            .downcast_ref::<swlsp_phar::PharError>()
            .is_some_and(swlsp_phar::PharError::is_not_archive),
        _ => false,
    })
}

fn canonical_phar_path(path: &str) -> Result<String> {
    let absolute = std::path::absolute(path)?;
    let resolved = fs::canonicalize(absolute)?;
    let resolved = resolved
        .to_str()
        .ok_or_else(|| Error::msg("PHAR path is not valid UTF-8"))?;
    Ok(paths::clean(resolved))
}

pub(crate) fn safe_phar_entry_path(name: &str) -> Result<String> {
    let unsafe_path = || Error::msg(format!("unsafe PHAR entry path {name:?}"));
    if name.is_empty() || name.contains('\\') || name.contains(':') {
        return Err(unsafe_path());
    }
    let cleaned = clean_slash(name);
    if cleaned != name
        || cleaned == "."
        || cleaned == ".."
        || cleaned.starts_with('/')
        || cleaned.starts_with("../")
    {
        return Err(unsafe_path());
    }
    Ok(cleaned)
}

/// Go `path.Clean` (always slash-separated).
fn clean_slash(path: &str) -> String {
    let rooted = path.starts_with('/');
    let mut parts: Vec<&str> = Vec::new();
    for part in path.split('/') {
        match part {
            "" | "." => {}
            ".." => {
                if parts.last().is_some_and(|last| *last != "..") {
                    parts.pop();
                } else if !rooted {
                    parts.push("..");
                }
            }
            other => parts.push(other),
        }
    }
    let joined = parts.join("/");
    match (rooted, joined.is_empty()) {
        (true, _) => format!("/{joined}"),
        (false, true) => ".".to_owned(),
        (false, false) => joined,
    }
}

fn read_phar_cache_state(cache_dir: &Path) -> Result<PharCacheState> {
    let content = fs::read(cache_dir.join(PHAR_CACHE_STATE_FILE))?;
    serde_json::from_slice(&content).map_err(Error::other)
}

/// The cached PHP sources (sorted) and their total size. Anything but
/// regular `.php` files means the cache was tampered with.
fn collect_phar_cache_files(cache_dir: &Path) -> Result<(Vec<String>, i64)> {
    let mut files = Vec::new();
    let mut source_size = 0;
    let mut pending = vec![cache_dir.join("files")];
    while let Some(directory) = pending.pop() {
        for entry in fs::read_dir(&directory)? {
            let entry = entry?;
            let file_type = entry.file_type()?;
            let path = entry.path();
            if file_type.is_dir() {
                pending.push(path);
                continue;
            }
            let text = path
                .to_str()
                .ok_or_else(|| Error::msg("PHAR cache path is not valid UTF-8"))?
                .to_owned();
            if !file_type.is_file() || !paths::has_ext_fold(&text, ".php") {
                return Err(Error::msg(format!(
                    "unexpected file in PHAR source cache: {text}"
                )));
            }
            source_size += i64::try_from(entry.metadata()?.len()).unwrap_or(i64::MAX);
            files.push(text);
        }
    }
    files.sort();
    Ok((files, source_size))
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

/// A uniquely named directory removed on drop unless published.
struct TemporaryDirectory {
    path: PathBuf,
    published: bool,
}

static TEMPORARY_COUNTER: AtomicU64 = AtomicU64::new(0);

fn unique_suffix() -> String {
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|duration| duration.subsec_nanos())
        .unwrap_or_default();
    format!(
        "{}-{}-{nanos}",
        std::process::id(),
        TEMPORARY_COUNTER.fetch_add(1, Ordering::Relaxed)
    )
}

impl TemporaryDirectory {
    fn create(parent: &Path, prefix: &str) -> std::io::Result<Self> {
        loop {
            let path = parent.join(format!("{prefix}{}", unique_suffix()));
            match fs::create_dir(&path) {
                Ok(()) => {
                    return Ok(Self {
                        path,
                        published: false,
                    });
                }
                Err(error) if error.kind() == ErrorKind::AlreadyExists => continue,
                Err(error) => return Err(error),
            }
        }
    }

    /// Atomically replaces `destination` with this directory, keeping the
    /// previous cache until the new one is in place.
    fn publish(mut self, destination: &Path) -> Result<()> {
        let parent = destination.parent().unwrap_or(Path::new("."));
        let mut previous = None;
        match fs::metadata(destination) {
            Ok(_) => {
                let placeholder = parent.join(format!(".previous-{}", unique_suffix()));
                fs::rename(destination, &placeholder).context("preserve previous PHAR cache")?;
                previous = Some(placeholder);
            }
            Err(error) if error.kind() == ErrorKind::NotFound => {}
            Err(error) => return Err(Error::from(error).context("inspect existing PHAR cache")),
        }
        if let Err(error) = fs::rename(&self.path, destination) {
            if let Some(previous) = &previous {
                let _ = fs::rename(previous, destination);
            }
            return Err(Error::from(error).context("publish PHAR source cache"));
        }
        self.published = true;
        if let Some(previous) = previous {
            fs::remove_dir_all(previous).context("remove previous PHAR source cache")?;
        }
        Ok(())
    }
}

impl Drop for TemporaryDirectory {
    fn drop(&mut self) {
        if !self.published {
            let _ = fs::remove_dir_all(&self.path);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn safe_phar_entry_paths() {
        for valid in ["file.php", "src/Nested/File.php"] {
            assert_eq!(safe_phar_entry_path(valid).unwrap(), valid);
        }
        for invalid in [
            "",
            ".",
            "../outside.php",
            "src/../../outside.php",
            "/absolute.php",
            r"src\windows.php",
            "C:/windows.php",
            "src/./file.php",
        ] {
            assert!(safe_phar_entry_path(invalid).is_err(), "{invalid}");
        }
    }
}
