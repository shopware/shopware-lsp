use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicI64, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use crate::{
    BatchIndexer, CancelToken, DataIndexer, Error, Indexer, Mutation, ParsedFile, Prepared,
    PreparingIndexer, Result, SupplementalPathIndexer, TransactionalClearer, TransactionalRemover,
};

pub(crate) fn text(path: &Path) -> String {
    path.to_str().unwrap().to_owned()
}

pub(crate) fn write(path: &Path, content: &str) -> PathBuf {
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, content).unwrap();
    path.to_path_buf()
}

#[derive(Default)]
pub(crate) struct MockIndexer {
    pub indexed: Mutex<HashSet<String>>,
}

impl MockIndexer {
    pub fn has(&self, path: &Path) -> bool {
        self.indexed.lock().unwrap().contains(&text(path))
    }
}

impl Indexer for MockIndexer {
    fn id(&self) -> &str {
        "mock"
    }
    fn index(&self, file: &ParsedFile) -> Result<()> {
        self.indexed.lock().unwrap().insert(file.path().to_owned());
        Ok(())
    }
    fn removed_files(&self, paths: &[String]) -> Result<()> {
        let mut indexed = self.indexed.lock().unwrap();
        for path in paths {
            indexed.remove(path);
        }
        Ok(())
    }
    fn clear(&self) -> Result<()> {
        Ok(())
    }
}

/// Reopens `public/build` (and its parent) for a resource indexer.
pub(crate) struct SupplementalMockIndexer {
    pub mock: MockIndexer,
    pub public_build: String,
    pub syntax_prepared: AtomicBool,
}

impl SupplementalMockIndexer {
    pub fn new(public_build: &Path) -> Self {
        Self {
            mock: MockIndexer::default(),
            public_build: text(public_build),
            syntax_prepared: AtomicBool::new(false),
        }
    }
}

impl Indexer for SupplementalMockIndexer {
    fn id(&self) -> &str {
        "supplemental"
    }
    fn index(&self, file: &ParsedFile) -> Result<()> {
        if file.has_syntax_tree() {
            self.syntax_prepared.store(true, Ordering::SeqCst);
        }
        self.mock.index(file)
    }
    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.mock.removed_files(paths)
    }
    fn clear(&self) -> Result<()> {
        Ok(())
    }
    fn as_supplemental_path(&self) -> Option<&dyn SupplementalPathIndexer> {
        Some(self)
    }
}

impl SupplementalPathIndexer for SupplementalMockIndexer {
    fn should_enter_directory(&self, path: &str) -> bool {
        let path = crate::paths::clean(path);
        let root = crate::paths::clean(&self.public_build);
        let parent = Path::new(&root)
            .parent()
            .unwrap()
            .to_str()
            .unwrap()
            .to_owned();
        path == parent || path == root || path.starts_with(&format!("{root}/"))
    }
    fn should_index_path(&self, path: &str) -> bool {
        crate::paths::clean(path)
            .starts_with(&format!("{}/", crate::paths::clean(&self.public_build)))
    }
}

/// Interrupts a run once it has seen a given number of files.
pub(crate) struct CancellingIndexer {
    pub indexed: AtomicUsize,
    pub after: AtomicUsize,
    pub cancel: CancelToken,
}

impl Indexer for CancellingIndexer {
    fn id(&self) -> &str {
        "cancelling"
    }
    fn index(&self, _file: &ParsedFile) -> Result<()> {
        let indexed = self.indexed.fetch_add(1, Ordering::SeqCst) + 1;
        let after = self.after.load(Ordering::SeqCst);
        if after > 0 && indexed == after {
            self.cancel.cancel();
        }
        Ok(())
    }
    fn removed_files(&self, _paths: &[String]) -> Result<()> {
        Ok(())
    }
    fn clear(&self) -> Result<()> {
        Ok(())
    }
}

#[derive(Default)]
pub(crate) struct ControlledIndexer {
    pub error: Mutex<Option<String>>,
    pub calls: AtomicI64,
    pub closes: AtomicI64,
    pub active: AtomicI64,
    pub max_active: AtomicI64,
    pub gate: Option<(
        crossbeam_channel::Sender<()>,
        crossbeam_channel::Receiver<()>,
    )>,
    pub entered: Option<crossbeam_channel::Sender<()>>,
}

impl Indexer for ControlledIndexer {
    fn id(&self) -> &str {
        "controlled"
    }
    fn index(&self, _file: &ParsedFile) -> Result<()> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        let active = self.active.fetch_add(1, Ordering::SeqCst) + 1;
        self.max_active.fetch_max(active, Ordering::SeqCst);
        if let (Some(entered), Some((_, release))) = (&self.entered, &self.gate) {
            entered.send(()).unwrap();
            let _ = release.recv();
        }
        self.active.fetch_sub(1, Ordering::SeqCst);
        match self.error.lock().unwrap().as_ref() {
            Some(message) => Err(Error::msg(message.clone())),
            None => Ok(()),
        }
    }
    fn removed_files(&self, _paths: &[String]) -> Result<()> {
        Ok(())
    }
    fn close(&self) -> Result<()> {
        self.closes.fetch_add(1, Ordering::SeqCst);
        Ok(())
    }
    fn clear(&self) -> Result<()> {
        Ok(())
    }
}

#[derive(Default)]
pub(crate) struct BatchControlledIndexer {
    pub controlled: ControlledIndexer,
    pub begins: AtomicI64,
    pub candidates: AtomicI64,
    pub ends: AtomicI64,
}

impl Indexer for BatchControlledIndexer {
    fn id(&self) -> &str {
        "batch"
    }
    fn index(&self, file: &ParsedFile) -> Result<()> {
        self.controlled.index(file)
    }
    fn removed_files(&self, _paths: &[String]) -> Result<()> {
        Ok(())
    }
    fn clear(&self) -> Result<()> {
        Ok(())
    }
    fn as_batch(&self) -> Option<&dyn BatchIndexer> {
        Some(self)
    }
}

impl BatchIndexer for BatchControlledIndexer {
    fn begin_indexing_batch(&self, candidate_files: &[String]) {
        self.begins.fetch_add(1, Ordering::SeqCst);
        self.candidates
            .store(candidate_files.len() as i64, Ordering::SeqCst);
    }
    fn end_indexing_batch(&self) -> Result<()> {
        self.ends.fetch_add(1, Ordering::SeqCst);
        Ok(())
    }
}

#[derive(Default)]
pub(crate) struct PreparingControlledIndexer {
    pub prepare_error: Option<String>,
    pub prepare_calls: AtomicI64,
    pub index_calls: AtomicI64,
    pub prepare_had_mutation: AtomicBool,
    pub index_had_mutation: AtomicBool,
    pub indexed_value: Mutex<String>,
    pub syntax_available_during_index: AtomicBool,
}

impl Indexer for PreparingControlledIndexer {
    fn id(&self) -> &str {
        "preparing"
    }
    fn index(&self, _file: &ParsedFile) -> Result<()> {
        Err(Error::msg("unprepared index path used"))
    }
    fn removed_files(&self, _paths: &[String]) -> Result<()> {
        Ok(())
    }
    fn clear(&self) -> Result<()> {
        Ok(())
    }
    fn as_preparing(&self) -> Option<&dyn PreparingIndexer> {
        Some(self)
    }
}

impl PreparingIndexer for PreparingControlledIndexer {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        self.prepare_calls.fetch_add(1, Ordering::SeqCst);
        self.prepare_had_mutation
            .store(file.mutation().is_some(), Ordering::SeqCst);
        if let Some(message) = &self.prepare_error {
            return Err(Error::msg(message.clone()));
        }
        Ok(Some(Box::new(format!("{}:prepared", file.path()))))
    }
    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        self.index_calls.fetch_add(1, Ordering::SeqCst);
        self.index_had_mutation
            .store(file.mutation().is_some(), Ordering::SeqCst);
        *self.indexed_value.lock().unwrap() = prepared
            .and_then(|value| value.downcast_ref::<String>())
            .cloned()
            .unwrap_or_default();
        self.syntax_available_during_index
            .store(file.syntax_tree().is_some(), Ordering::SeqCst);
        Ok(())
    }
}

pub(crate) struct RepositoryIndexer {
    pub repository: Arc<DataIndexer<String>>,
    pub value: String,
    pub error: Mutex<Option<String>>,
    pub fail_path: String,
    pub remove_error: Mutex<Option<String>>,
    pub clear_error: Mutex<Option<String>>,
}

impl RepositoryIndexer {
    pub fn new(repository: Arc<DataIndexer<String>>, value: &str) -> Self {
        Self {
            repository,
            value: value.into(),
            error: Mutex::new(None),
            fail_path: String::new(),
            remove_error: Mutex::new(None),
            clear_error: Mutex::new(None),
        }
    }
}

fn configured(error: &Mutex<Option<String>>) -> Result<()> {
    match error.lock().unwrap().as_ref() {
        Some(message) => Err(Error::msg(message.clone())),
        None => Ok(()),
    }
}

impl Indexer for RepositoryIndexer {
    fn id(&self) -> &str {
        &self.value
    }
    fn index(&self, file: &ParsedFile) -> Result<()> {
        let items: HashMap<String, HashMap<String, String>> = HashMap::from([(
            file.path().to_owned(),
            HashMap::from([("value".to_owned(), self.value.clone())]),
        )]);
        self.repository
            .batch_save_items_in(file.mutation(), &items)?;
        if self.fail_path.is_empty() || self.fail_path == file.path() {
            configured(&self.error)
        } else {
            Ok(())
        }
    }
    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.repository.batch_delete_by_file_paths(paths)
    }
    fn clear(&self) -> Result<()> {
        self.repository.clear()
    }
    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }
    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

impl TransactionalRemover for RepositoryIndexer {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.repository
            .batch_delete_by_file_paths_in(Some(mutation), paths)?;
        configured(&self.remove_error)
    }
}

impl TransactionalClearer for RepositoryIndexer {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.repository.clear_in(Some(mutation))?;
        configured(&self.clear_error)
    }
}

#[derive(Default)]
pub(crate) struct PharRecordingIndexer {
    pub calls: AtomicUsize,
    pub sources: Mutex<HashMap<String, String>>,
}

impl PharRecordingIndexer {
    pub fn snapshot(&self) -> HashMap<String, String> {
        self.sources.lock().unwrap().clone()
    }
}

impl Indexer for PharRecordingIndexer {
    fn id(&self) -> &str {
        "phar-recording"
    }
    fn index(&self, file: &ParsedFile) -> Result<()> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        self.sources
            .lock()
            .unwrap()
            .insert(file.path().to_owned(), file.source().to_owned());
        Ok(())
    }
    fn removed_files(&self, paths: &[String]) -> Result<()> {
        let mut sources = self.sources.lock().unwrap();
        for path in paths {
            sources.remove(path);
        }
        Ok(())
    }
    fn clear(&self) -> Result<()> {
        Ok(())
    }
}

fn crc32(bytes: &[u8]) -> u32 {
    let mut crc = 0xffff_ffffu32;
    for byte in bytes {
        crc ^= u32::from(*byte);
        for _ in 0..8 {
            crc = if crc & 1 != 0 {
                (crc >> 1) ^ 0xedb8_8320
            } else {
                crc >> 1
            };
        }
    }
    !crc
}

/// Writes an uncompressed PHAR with the given entries (sorted by name).
pub(crate) fn write_phar(path: &Path, entries: &[(&str, &str)]) {
    let mut entries = entries.to_vec();
    entries.sort();
    let mut entry_manifest = Vec::new();
    let mut contents = Vec::new();
    for (name, content) in &entries {
        let mut push = |value: u32| entry_manifest.extend_from_slice(&value.to_le_bytes());
        push(name.len() as u32);
        entry_manifest.extend_from_slice(name.as_bytes());
        let mut push = |value: u32| entry_manifest.extend_from_slice(&value.to_le_bytes());
        push(content.len() as u32);
        push(1_700_000_000);
        push(content.len() as u32);
        push(crc32(content.as_bytes()));
        push(0o100644);
        push(0);
        contents.extend_from_slice(content.as_bytes());
    }
    let mut manifest = Vec::new();
    manifest.extend_from_slice(&(entries.len() as u32).to_le_bytes());
    manifest.extend_from_slice(&0x0011u16.to_le_bytes());
    for _ in 0..3 {
        manifest.extend_from_slice(&0u32.to_le_bytes());
    }
    manifest.extend_from_slice(&entry_manifest);
    let mut archive = b"<?php __HALT_COMPILER(); ?>\r\n".to_vec();
    archive.extend_from_slice(&(manifest.len() as u32).to_le_bytes());
    archive.extend_from_slice(&manifest);
    archive.extend_from_slice(&contents);
    std::fs::create_dir_all(path.parent().unwrap()).unwrap();
    std::fs::write(path, archive).unwrap();
}

pub(crate) fn set_mtime(path: &Path, seconds: i64, nanos: u32) {
    let time = filetime::FileTime::from_unix_time(seconds, nanos);
    filetime::set_file_times(path, time, time).unwrap();
}

/// Polls `condition` until it holds or `timeout` elapses.
pub(crate) fn eventually(
    timeout: std::time::Duration,
    mut condition: impl FnMut() -> bool,
) -> bool {
    let deadline = std::time::Instant::now() + timeout;
    while std::time::Instant::now() < deadline {
        if condition() {
            return true;
        }
        std::thread::sleep(std::time::Duration::from_millis(20));
    }
    condition()
}
