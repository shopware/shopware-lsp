use std::path::Path;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::time::Duration;

use super::support::*;
use crate::scanner::{
    FileMeta, FileState, SKIPPED_CONFIGURED_LIMIT, SKIPPED_PARSER_RANGE, StoredFileState,
    default_index_worker_count,
};
use crate::scanner_index::{
    MAX_PREPARATION_BATCH_BYTES, MAX_PREPARATION_BATCH_FILES, preparation_batch_ready,
    preparation_batch_would_overflow,
};
use crate::{
    CancelToken, DataIndexer, Error, FileScanner, FileSizeStats, MAXIMUM_SOURCE_FILE_BYTES,
    SkippedFileStats, Store, should_skip_relative_path,
};

fn scanner(root: &Path, db: &Path, store: Option<Store>) -> FileScanner {
    FileScanner::new(text(root), db, store).unwrap()
}

fn tracked(scanner: &FileScanner, path: &Path) -> i64 {
    scanner
        .with_db(|connection| {
            Ok(connection.query_row(
                "SELECT COUNT(*) FROM file_hashes WHERE path = ?",
                [text(path)],
                |row| row.get(0),
            )?)
        })
        .unwrap()
}

fn none() -> CancelToken {
    CancelToken::new()
}

#[test]
fn default_worker_count_is_lightweight_and_cpu_aware() {
    assert_eq!(default_index_worker_count(0), 1);
    assert_eq!(default_index_worker_count(1), 1);
    assert_eq!(default_index_worker_count(2), 1);
    assert_eq!(default_index_worker_count(4), 3);
    assert_eq!(default_index_worker_count(8), 6);
    assert_eq!(default_index_worker_count(12), 8);
    assert_eq!(default_index_worker_count(64), 8);
}

#[test]
fn preparation_batches_bound_files_and_source_weight() {
    assert!(!preparation_batch_ready(0, 0));
    assert!(!preparation_batch_ready(
        MAX_PREPARATION_BATCH_FILES - 1,
        MAX_PREPARATION_BATCH_BYTES - 1
    ));
    assert!(preparation_batch_ready(MAX_PREPARATION_BATCH_FILES, 0));
    assert!(preparation_batch_ready(1, MAX_PREPARATION_BATCH_BYTES));
    assert!(!preparation_batch_would_overflow(
        0,
        0,
        MAX_PREPARATION_BATCH_BYTES + 1
    ));
    assert!(!preparation_batch_would_overflow(
        MAX_PREPARATION_BATCH_FILES - 1,
        MAX_PREPARATION_BATCH_BYTES - 1,
        1
    ));
    assert!(preparation_batch_would_overflow(
        MAX_PREPARATION_BATCH_FILES,
        0,
        1
    ));
    assert!(preparation_batch_would_overflow(
        1,
        MAX_PREPARATION_BATCH_BYTES - 1,
        2
    ));
}

#[test]
fn source_size_rejection() {
    let directory = tempfile::tempdir().unwrap();
    let scanner = scanner(directory.path(), &directory.path().join("scanner.db"), None);
    scanner.set_max_file_size_bytes(8 << 20);
    assert_eq!(scanner.inner.source_size_rejection(8 << 20), None);
    assert_eq!(
        scanner.inner.source_size_rejection((8 << 20) + 1),
        Some((SKIPPED_CONFIGURED_LIMIT, 8 << 20))
    );
    scanner.set_max_file_size_bytes(0);
    assert_eq!(
        scanner
            .inner
            .source_size_rejection(MAXIMUM_SOURCE_FILE_BYTES + 1),
        Some((SKIPPED_PARSER_RANGE, MAXIMUM_SOURCE_FILE_BYTES))
    );
}

#[test]
fn skips_oversized_files_and_reports_largest_files() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let small = write(&root.path().join("small.php"), "<?php");
    let large = write(&root.path().join("large.php"), "<?php generated source");
    let indexer = Arc::new(MockIndexer::default());
    let mut scanner = scanner(root.path(), &cache.path().join("scanner.db"), None);
    scanner.set_max_file_size_bytes(8);
    scanner.add_indexer(indexer.clone());
    scanner.index_all(&none()).unwrap();
    assert!(indexer.has(&small));
    assert!(!indexer.has(&large));
    let stats = scanner.stats().unwrap();
    assert_eq!(stats.tracked_files, 1);
    assert_eq!(stats.tracked_bytes, 5);
    assert_eq!(stats.max_file_size_bytes, 8);
    assert_eq!(
        stats.largest_indexed_files,
        [FileSizeStats {
            path: text(&small),
            bytes: 5
        }]
    );
    assert_eq!(stats.skipped_oversized_count, 1);
    assert_eq!(stats.skipped_oversized_bytes, 22);
    assert_eq!(
        stats.largest_skipped_files,
        [SkippedFileStats {
            path: text(&large),
            bytes: 22,
            limit_bytes: 8,
            reason: SKIPPED_CONFIGURED_LIMIT.into()
        }]
    );
    let json = serde_json::to_value(&stats).unwrap();
    assert_eq!(json["skippedOversizedCount"], 1);
    assert_eq!(json["largestSkippedFiles"][0]["limitBytes"], 8);
    scanner.close().unwrap();
}

#[test]
fn removes_file_that_grows_beyond_limit() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let path = write(&root.path().join("generated.php"), "<?php");
    let indexer = Arc::new(MockIndexer::default());
    let mut scanner = scanner(root.path(), &cache.path().join("scanner.db"), None);
    scanner.set_max_file_size_bytes(8);
    scanner.add_indexer(indexer.clone());
    scanner.index_files(&none(), &[text(&path)]).unwrap();
    assert!(indexer.has(&path));
    std::fs::write(&path, "<?php generated source").unwrap();
    scanner.index_files(&none(), &[text(&path)]).unwrap();
    assert!(!indexer.has(&path));
    let stats = scanner.stats().unwrap();
    assert_eq!(stats.tracked_files, 0);
    assert_eq!(stats.skipped_oversized_count, 1);

    scanner.set_max_file_size_bytes(64);
    scanner.index_files(&none(), &[text(&path)]).unwrap();
    assert!(indexer.has(&path));
    let stats = scanner.stats().unwrap();
    assert_eq!(stats.tracked_files, 1);
    assert_eq!(stats.skipped_oversized_count, 0);
}

#[test]
fn should_skip_relative_path_checks_components() {
    assert!(!should_skip_relative_path(""));
    assert!(!should_skip_relative_path("."));
    assert!(!should_skip_relative_path("src/variant"));
    assert!(should_skip_relative_path("src/node_modules/package"));
    assert!(should_skip_relative_path("public/build"));
    assert!(should_skip_relative_path(
        "Resources/app/administration/.tmp/vite"
    ));
    // Composer packages named like cache directories stay visible.
    assert!(should_skip_relative_path("var/cache/file.php"));
    assert!(should_skip_relative_path("nested/cache/file.php"));
    assert!(!should_skip_relative_path(
        "vendor/symfony/cache/Adapter/AdapterInterface.php"
    ));
    assert!(should_skip_relative_path(
        "vendor/package/node_modules/generated.php"
    ));
    for directory in [
        ".claude",
        ".codex",
        ".continue",
        ".cursor",
        ".delta",
        ".ddev",
        ".devbox",
        ".devenv",
        ".direnv",
        ".fleet",
        ".windsurf",
        ".zed",
    ] {
        assert!(should_skip_relative_path(&format!(
            "{directory}/config.yaml"
        )));
        assert!(should_skip_relative_path(&format!(
            "nested/{directory}/config.yaml"
        )));
    }
}

#[test]
fn does_not_commit_state_after_indexer_failure() {
    let root = tempfile::tempdir().unwrap();
    let path = write(&root.path().join("broken.php"), "<?php");
    let failing = Arc::new(ControlledIndexer {
        error: Some("index failed".to_owned()).into(),
        ..Default::default()
    });
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(failing.clone());
    let error = scanner.index_files(&none(), &[text(&path)]).unwrap_err();
    assert!(error.to_string().contains("index failed"), "{error}");
    assert_eq!(tracked(&scanner, &path), 0);
    *failing.error.lock().unwrap() = None;
    scanner.index_files(&none(), &[text(&path)]).unwrap();
    assert_eq!(tracked(&scanner, &path), 1);
}

#[test]
fn updates_file_states_in_batches() {
    let root = tempfile::tempdir().unwrap();
    let scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    let states: Vec<FileState> = (0..257)
        .map(|index| FileState {
            path: format!("/state-{index}.php"),
            meta: FileMeta {
                size: 5,
                mtime: 1_234,
            },
            hash: 0,
        })
        .collect();
    scanner.inner.update_file_states(&states).unwrap();
    assert_eq!(scanner.stats().unwrap().tracked_files, 257);
    for index in [0, 127, 128, 256] {
        let (size, mtime): (i64, i64) = scanner
            .with_db(|connection| {
                Ok(connection.query_row(
                    "SELECT size, mtime FROM file_hashes WHERE path = ?",
                    [&states[index].path],
                    |row| Ok((row.get(0)?, row.get(1)?)),
                )?)
            })
            .unwrap();
        assert_eq!((size, mtime), (5, 1_234));
    }
}

#[test]
fn keeps_progress_when_indexing_is_interrupted() {
    let root = tempfile::tempdir().unwrap();
    const FILE_COUNT: usize = 120;
    for index in 0..FILE_COUNT {
        write(
            &root.path().join(format!("source{index:03}.php")),
            "<?php\n",
        );
    }
    let store = Store::open(root.path().join("indexes.db")).unwrap();
    let cancel = CancelToken::new();
    let indexer = Arc::new(CancellingIndexer {
        indexed: AtomicUsize::new(0),
        after: AtomicUsize::new(60),
        cancel: cancel.clone(),
    });
    let mut scanner = scanner(
        root.path(),
        &root.path().join("scanner.db"),
        Some(store.clone()),
    );
    scanner.set_worker_count(1);
    scanner.add_indexer(indexer.clone());

    assert!(scanner.index_all(&cancel).is_err());
    let interrupted = scanner.stats().unwrap().tracked_files;
    assert!(
        interrupted > 0,
        "batches whose mutation committed must stay tracked"
    );
    assert!(interrupted < FILE_COUNT);

    indexer.after.store(0, Ordering::SeqCst);
    let before = indexer.indexed.load(Ordering::SeqCst);
    scanner.index_all(&none()).unwrap();
    assert_eq!(scanner.stats().unwrap().tracked_files, FILE_COUNT);
    assert_eq!(
        indexer.indexed.load(Ordering::SeqCst) - before,
        FILE_COUNT - interrupted
    );
    scanner.close().unwrap();
    store.close().unwrap();
}

fn repositories(store: &Store) -> (Arc<DataIndexer<String>>, Arc<DataIndexer<String>>) {
    (
        Arc::new(DataIndexer::in_store(store, "first").unwrap()),
        Arc::new(DataIndexer::in_store(store, "second").unwrap()),
    )
}

#[test]
fn rolls_back_workspace_repositories_together() {
    let root = tempfile::tempdir().unwrap();
    let path = text(&write(&root.path().join("atomic.php"), "<?php"));
    let store = Store::open(root.path().join("indexes.db")).unwrap();
    let (first_repository, second_repository) = repositories(&store);
    first_repository
        .batch_save_items([(&path, [("value", "old-first".to_owned())])])
        .unwrap();
    second_repository
        .batch_save_items([(&path, [("value", "old-second".to_owned())])])
        .unwrap();
    let first = Arc::new(RepositoryIndexer::new(
        first_repository.clone(),
        "new-first",
    ));
    let second = Arc::new(RepositoryIndexer::new(
        second_repository.clone(),
        "new-second",
    ));
    *second.error.lock().unwrap() = Some("second index failed".into());
    let mut scanner = scanner(
        root.path(),
        &root.path().join("scanner.db"),
        Some(store.clone()),
    );
    scanner.add_indexer(first);
    scanner.add_indexer(second.clone());

    let error = scanner.index_files(&none(), &[&path]).unwrap_err();
    assert!(error.to_string().contains("second index failed"));
    assert_eq!(first_repository.get_values("value").unwrap(), ["old-first"]);
    assert_eq!(
        second_repository.get_values("value").unwrap(),
        ["old-second"]
    );

    *second.error.lock().unwrap() = None;
    scanner.index_files(&none(), &[&path]).unwrap();
    assert_eq!(first_repository.get_values("value").unwrap(), ["new-first"]);
    assert_eq!(
        second_repository.get_values("value").unwrap(),
        ["new-second"]
    );
    scanner.close().unwrap();
    store.close().unwrap();
}

#[test]
fn batch_failure_retries_files_individually() {
    let root = tempfile::tempdir().unwrap();
    let good = write(&root.path().join("good.php"), "<?php");
    let bad = write(&root.path().join("bad.php"), "<?php");
    let store = Store::open(root.path().join("indexes.db")).unwrap();
    let repository = Arc::new(DataIndexer::<String>::in_store(&store, "batch-retry").unwrap());
    let mut indexer = RepositoryIndexer::new(repository.clone(), "indexed");
    *indexer.error.lock().unwrap() = Some("bad file".into());
    indexer.fail_path = text(&bad);
    let mut scanner = scanner(
        root.path(),
        &root.path().join("scanner.db"),
        Some(store.clone()),
    );
    scanner.set_worker_count(1);
    scanner.add_indexer(Arc::new(indexer));

    let error = scanner
        .index_files(&none(), &[text(&good), text(&bad)])
        .unwrap_err();
    assert!(error.to_string().contains("bad file"));
    assert_eq!(repository.get_values("value").unwrap(), ["indexed"]);
    assert_eq!(tracked(&scanner, &good), 1);
    assert_eq!(tracked(&scanner, &bad), 0);
    scanner.close().unwrap();
    store.close().unwrap();
}

#[test]
fn rolls_back_repository_removal_and_clear() {
    let root = tempfile::tempdir().unwrap();
    let path = text(&write(&root.path().join("atomic.php"), "<?php"));
    let store = Store::open(root.path().join("indexes.db")).unwrap();
    let (first_repository, second_repository) = repositories(&store);
    let seed = || {
        first_repository
            .batch_save_items([(&path, [("value", "first".to_owned())])])
            .unwrap();
        second_repository
            .batch_save_items([(&path, [("value", "second".to_owned())])])
            .unwrap();
    };
    seed();
    let second = Arc::new(RepositoryIndexer::new(second_repository.clone(), "second"));
    *second.remove_error.lock().unwrap() = Some("remove failed".into());
    let mut scanner = scanner(
        root.path(),
        &root.path().join("scanner.db"),
        Some(store.clone()),
    );
    scanner.add_indexer(Arc::new(RepositoryIndexer::new(
        first_repository.clone(),
        "first",
    )));
    scanner.add_indexer(second.clone());

    let error = scanner.remove_files(&none(), &[&path]).unwrap_err();
    assert!(error.to_string().contains("remove failed"));
    assert_eq!(first_repository.get_values("value").unwrap(), ["first"]);
    assert_eq!(second_repository.get_values("value").unwrap(), ["second"]);

    *second.remove_error.lock().unwrap() = None;
    scanner.remove_files(&none(), &[&path]).unwrap();
    assert!(first_repository.get_values("value").unwrap().is_empty());
    assert!(second_repository.get_values("value").unwrap().is_empty());

    seed();
    *second.clear_error.lock().unwrap() = Some("clear failed".into());
    assert!(
        scanner
            .clear_hashes()
            .unwrap_err()
            .to_string()
            .contains("clear failed")
    );
    assert_eq!(first_repository.get_values("value").unwrap(), ["first"]);
    assert_eq!(second_repository.get_values("value").unwrap(), ["second"]);
    *second.clear_error.lock().unwrap() = None;
    scanner.clear_hashes().unwrap();
    assert!(first_repository.get_values("value").unwrap().is_empty());
    scanner.close().unwrap();
    store.close().unwrap();
}

#[test]
fn remove_files_batches_state_deletion() {
    let root = tempfile::tempdir().unwrap();
    let scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    let paths: Vec<String> = (0..257)
        .map(|index| format!("/removed-{index:03}.php"))
        .collect();
    let mut states: Vec<FileState> = paths
        .iter()
        .map(|path| FileState {
            path: path.clone(),
            meta: FileMeta { size: 1, mtime: 1 },
            hash: 0,
        })
        .collect();
    states.push(FileState {
        path: "/survivor.php".into(),
        meta: FileMeta { size: 1, mtime: 1 },
        hash: 0,
    });
    scanner.inner.update_file_states(&states).unwrap();
    scanner.remove_files(&none(), &paths).unwrap();
    assert_eq!(scanner.stats().unwrap().tracked_files, 1);
    assert_eq!(tracked(&scanner, Path::new("/survivor.php")), 1);
}

#[test]
fn honors_cancelled_token() {
    let root = tempfile::tempdir().unwrap();
    let path = write(&root.path().join("canceled.php"), "<?php");
    let indexer = Arc::new(ControlledIndexer::default());
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    let cancel = CancelToken::new();
    cancel.cancel();
    assert!(
        scanner
            .index_files(&cancel, &[text(&path)])
            .unwrap_err()
            .is_cancelled()
    );
    assert_eq!(indexer.calls.load(Ordering::SeqCst), 0);
}

#[test]
fn serializes_concurrent_runs() {
    let root = tempfile::tempdir().unwrap();
    let first = text(&write(&root.path().join("first.php"), "<?php"));
    let second = text(&write(&root.path().join("second.php"), "<?php"));
    let (entered_sender, entered) = crossbeam_channel::unbounded();
    let (release, released) = crossbeam_channel::unbounded();
    let indexer = Arc::new(ControlledIndexer {
        entered: Some(entered_sender),
        gate: Some((release.clone(), released)),
        ..Default::default()
    });
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    std::thread::scope(|scope| {
        let first_run = scope.spawn(|| scanner.index_files(&none(), &[&first]));
        entered.recv().unwrap();
        let second_run = scope.spawn(|| scanner.index_files(&none(), &[&second]));
        assert!(entered.recv_timeout(Duration::from_millis(50)).is_err());
        release.send(()).unwrap();
        release.send(()).unwrap();
        first_run.join().unwrap().unwrap();
        second_run.join().unwrap().unwrap();
    });
    assert_eq!(indexer.max_active.load(Ordering::SeqCst), 1);
}

#[test]
fn does_not_own_indexers() {
    let root = tempfile::tempdir().unwrap();
    let indexer = Arc::new(ControlledIndexer::default());
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    scanner.close().unwrap();
    scanner.close().unwrap();
    assert_eq!(indexer.closes.load(Ordering::SeqCst), 0);
    assert!(scanner.stats().is_err());
}

#[test]
fn watcher_can_restart() {
    let root = tempfile::tempdir().unwrap();
    let scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.start_watcher().unwrap();
    assert!(
        scanner
            .start_watcher()
            .unwrap_err()
            .to_string()
            .contains("already running")
    );
    scanner.stop_watcher();
    scanner.start_watcher().unwrap();
    scanner.stop_watcher();
    scanner.close().unwrap();
}

fn create_test_files(base: &Path) {
    for directory in [
        "regular",
        ".devenv",
        "node_modules",
        "vendor-bin",
        "tests",
        "nested/node_modules",
    ] {
        write(
            &base.join(directory).join("file.php"),
            "<?php\n// Test file\n",
        );
    }
}

#[test]
fn index_files_skips_excluded_directories() {
    let root = tempfile::tempdir().unwrap();
    create_test_files(root.path());
    let indexer = Arc::new(MockIndexer::default());
    let mut scanner = scanner(root.path(), &root.path().join("test.db"), None);
    scanner.add_indexer(indexer.clone());
    let files: Vec<String> = [
        "regular",
        ".devenv",
        "node_modules",
        "vendor-bin",
        "tests",
        "nested/node_modules",
    ]
    .iter()
    .map(|directory| text(&root.path().join(directory).join("file.php")))
    .collect();
    scanner.index_files(&none(), &files).unwrap();
    assert_eq!(indexer.indexed.lock().unwrap().len(), 1);
    assert!(indexer.has(&root.path().join("regular/file.php")));
}

#[test]
fn index_all_skips_directories_at_any_depth() {
    let root = tempfile::tempdir().unwrap();
    create_test_files(root.path());
    let indexer = Arc::new(MockIndexer::default());
    let mut scanner = scanner(root.path(), &root.path().join("test.db"), None);
    scanner.add_indexer(indexer.clone());
    scanner.index_all(&none()).unwrap();
    assert!(indexer.has(&root.path().join("regular/file.php")));
    assert!(!indexer.has(&root.path().join(".devenv/file.php")));
    assert!(!indexer.has(&root.path().join("nested/node_modules/file.php")));
}

#[test]
fn supplemental_indexer_selectively_reopens_skipped_directory() {
    let root = tempfile::tempdir().unwrap();
    let build = root.path().join("public/build");
    let build_file = write(&build.join("app.css"), "body{}");
    let media = write(&root.path().join("public/media/upload.jpg"), "image");
    let indexer = Arc::new(SupplementalMockIndexer::new(&build));
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    scanner.index_all(&none()).unwrap();
    assert!(indexer.mock.has(&build_file));
    assert!(!indexer.mock.has(&media));
}

#[test]
fn configured_exclusions_veto_normal_and_supplemental_paths() {
    let root = tempfile::tempdir().unwrap();
    let generated = write(&root.path().join("src/generated/Model.php"), "fixture");
    let regular = write(&root.path().join("src/Service.php"), "fixture");
    let build = root.path().join("public/build");
    let public = write(&build.join("app.js"), "fixture");
    let indexer = Arc::new(SupplementalMockIndexer::new(&build));
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner
        .set_excluded_paths(&["**/generated/**", "public/build/**"])
        .unwrap();
    scanner.add_indexer(indexer.clone());
    scanner.index_all(&none()).unwrap();
    assert!(indexer.mock.has(&regular));
    assert!(!indexer.mock.has(&generated));
    assert!(!indexer.mock.has(&public));
    scanner
        .index_files(&none(), &[text(&generated), text(&public)])
        .unwrap();
    assert!(!indexer.mock.has(&generated));
    assert!(!indexer.mock.has(&public));
    assert!(scanner.set_excluded_paths(&["[invalid"]).is_err());
}

#[test]
fn configured_exclusions_remove_warm_tracked_files() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let generated = write(&root.path().join("src/generated/Model.php"), "<?php");
    let indexer = Arc::new(MockIndexer::default());
    let db = cache.path().join("scanner.db");
    let mut first = scanner(root.path(), &db, None);
    first.add_indexer(indexer.clone());
    first.index_all(&none()).unwrap();
    assert!(indexer.has(&generated));
    first.close().unwrap();

    let mut second = scanner(root.path(), &db, None);
    second.set_excluded_paths(&["**/generated/**"]).unwrap();
    second.add_indexer(indexer.clone());
    second.index_all(&none()).unwrap();
    assert!(!indexer.has(&generated));
}

#[test]
fn does_not_preparse_supplemental_resource_files() {
    let root = tempfile::tempdir().unwrap();
    let build = root.path().join("public/build");
    let resource = write(&build.join("app.js"), "const broken = '");
    let indexer = Arc::new(SupplementalMockIndexer::new(&build));
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    scanner.index_all(&none()).unwrap();
    assert!(indexer.mock.has(&resource));
    assert!(!indexer.syntax_prepared.load(Ordering::SeqCst));
}

#[test]
fn index_all_removes_files_deleted_while_stopped() {
    let root = tempfile::tempdir().unwrap();
    let path = write(&root.path().join("stale.php"), "<?php");
    let indexer = Arc::new(MockIndexer::default());
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    scanner.index_all(&none()).unwrap();
    assert!(indexer.has(&path));
    std::fs::remove_file(&path).unwrap();
    scanner.index_all(&none()).unwrap();
    assert!(!indexer.has(&path));
    assert_eq!(tracked(&scanner, &path), 0);
}

fn seed_states(scanner: &FileScanner, states: &[(&str, i64, i64)]) {
    let states: Vec<FileState> = states
        .iter()
        .map(|(path, size, mtime)| FileState {
            path: (*path).into(),
            meta: FileMeta {
                size: *size,
                mtime: *mtime,
            },
            hash: 0,
        })
        .collect();
    scanner.inner.update_file_states(&states).unwrap();
}

fn found(size: i64, mtime: i64) -> StoredFileState {
    StoredFileState {
        meta: FileMeta { size, mtime },
        hash: 0,
        found: true,
    }
}

#[test]
fn scan_file_states_aligns_and_finds_stale_paths() {
    let root = tempfile::tempdir().unwrap();
    let scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    seed_states(
        &scanner,
        &[
            ("/a.php", 10, 20),
            ("/b.php", 11, 21),
            ("/d.php", 12, 22),
            ("/f.php", 13, 23),
        ],
    );
    let files = ["/b.php", "/c.php", "/e.php"].map(String::from);
    let (states, stale) = scanner.inner.scan_file_states(&files, true).unwrap();
    assert_eq!(stale, ["/a.php", "/d.php", "/f.php"]);
    assert_eq!(
        states,
        [
            found(11, 21),
            StoredFileState::default(),
            StoredFileState::default()
        ]
    );
}

#[test]
fn load_file_states_aligns_with_sorted_paths() {
    let root = tempfile::tempdir().unwrap();
    let scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    seed_states(&scanner, &[("/a.php", 10, 20), ("/c.php", 11, 21)]);
    let files = ["/a.php", "/b.php", "/c.php"].map(String::from);
    let states = scanner.inner.load_file_states(&files).unwrap();
    assert_eq!(
        states,
        [found(10, 20), StoredFileState::default(), found(11, 21)]
    );
}

#[test]
fn notifies_batch_indexers_on_failure() {
    let root = tempfile::tempdir().unwrap();
    let path = text(&write(&root.path().join("broken.php"), "<?php"));
    let indexer = Arc::new(BatchControlledIndexer {
        controlled: ControlledIndexer {
            error: Some("index failed".to_owned()).into(),
            ..Default::default()
        },
        ..Default::default()
    });
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    let error = scanner.index_files(&none(), &[&path, &path]).unwrap_err();
    assert!(error.to_string().contains("index failed"));
    assert_eq!(
        indexer.controlled.calls.load(Ordering::SeqCst),
        1,
        "duplicate paths must only be indexed once"
    );
    assert_eq!(indexer.begins.load(Ordering::SeqCst), 1);
    assert_eq!(indexer.candidates.load(Ordering::SeqCst), 1);
    assert_eq!(indexer.ends.load(Ordering::SeqCst), 1);
    assert_eq!(
        indexer.settles.load(Ordering::SeqCst),
        0,
        "failed runs are not settled"
    );
}

#[test]
fn notifies_update_after_batch_indexers_publish() {
    let root = tempfile::tempdir().unwrap();
    let path = text(&write(&root.path().join("published.php"), "<?php"));
    let indexer = Arc::new(BatchControlledIndexer::default());
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    let (sender, published) = crossbeam_channel::unbounded();
    let observed = indexer.clone();
    scanner.set_on_update(move || {
        let ends = observed.ends.load(Ordering::SeqCst);
        let settles = observed.settles.load(Ordering::SeqCst);
        sender.send((ends, settles)).unwrap();
    });
    scanner.index_files(&none(), &[&path]).unwrap();
    // Consumers only see a run once it ended and settled, in that order.
    assert_eq!(published.try_recv().unwrap(), (1, 1));
    assert_eq!(indexer.ends_before_settle.load(Ordering::SeqCst), 1);
}

#[test]
fn a_full_scan_reports_its_progress() {
    let root = tempfile::tempdir().unwrap();
    for name in ["a.php", "b.php", "c.php"] {
        write(&root.path().join(name), "<?php");
    }
    let scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    let (sender, reports) = crossbeam_channel::unbounded();
    scanner.set_on_progress(Some(Arc::new(move |progress| {
        sender.send(progress).unwrap();
    })));
    scanner.index_all(&none()).unwrap();
    // Reports are throttled, but the last file is always reported.
    let last = reports.try_iter().last().unwrap();
    assert_eq!((last.done, last.total), (3, 3));

    // Unchanged files count as well: a warm scan still looks at each.
    scanner.index_all(&none()).unwrap();
    let last = reports.try_iter().last().unwrap();
    assert_eq!((last.done, last.total), (3, 3));

    // Only full scans report: a watcher batch is not a workspace scan.
    let path = text(&write(&root.path().join("d.php"), "<?php"));
    scanner.index_files(&none(), &[&path]).unwrap();
    assert!(reports.try_iter().next().is_none());

    scanner.set_on_progress(None);
    scanner.index_all(&none()).unwrap();
    assert!(reports.try_iter().next().is_none());
}

#[test]
fn prepares_before_workspace_mutation() {
    let root = tempfile::tempdir().unwrap();
    let path = text(&write(&root.path().join("prepared.php"), "<?php"));
    let store = Store::open(root.path().join("indexes.db")).unwrap();
    let indexer = Arc::new(PreparingControlledIndexer::default());
    let mut scanner = scanner(
        root.path(),
        &root.path().join("scanner.db"),
        Some(store.clone()),
    );
    scanner.add_indexer(indexer.clone());
    scanner.index_files(&none(), &[&path]).unwrap();
    assert_eq!(indexer.prepare_calls.load(Ordering::SeqCst), 1);
    assert_eq!(indexer.index_calls.load(Ordering::SeqCst), 1);
    assert!(!indexer.prepare_had_mutation.load(Ordering::SeqCst));
    assert!(indexer.index_had_mutation.load(Ordering::SeqCst));
    assert_eq!(
        *indexer.indexed_value.lock().unwrap(),
        format!("{path}:prepared")
    );
    assert!(indexer.syntax_available_during_index.load(Ordering::SeqCst));
    scanner.close().unwrap();
    store.close().unwrap();
}

#[test]
fn does_not_index_after_preparation_failure() {
    let root = tempfile::tempdir().unwrap();
    let path = write(&root.path().join("broken.php"), "<?php");
    let indexer = Arc::new(PreparingControlledIndexer {
        prepare_error: Some("prepare failed".into()),
        ..Default::default()
    });
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    let error = scanner.index_files(&none(), &[text(&path)]).unwrap_err();
    assert!(error.to_string().contains("prepare failed"));
    assert_eq!(indexer.prepare_calls.load(Ordering::SeqCst), 1);
    assert_eq!(indexer.index_calls.load(Ordering::SeqCst), 0);
    assert_eq!(tracked(&scanner, &path), 0);
}

#[test]
fn index_files_parses_every_registered_language() {
    let root = tempfile::tempdir().unwrap();
    let paths = [
        write(
            &root
                .path()
                .join("Resources/views/storefront/page.html.twig"),
            "{% block page %}content{% endblock %}",
        ),
        write(
            &root.path().join("Resources/snippet/en-GB.json"),
            r#"{"page":{"title":"Hello"}}"#,
        ),
        write(
            &root.path().join("Resources/app/storefront/theme.scss"),
            ".button { color: $sw-color-brand-primary; }",
        ),
        write(
            &root.path().join("Resources/config/services.xml"),
            "<container><services/></container>",
        ),
        write(
            &root.path().join("Resources/config/services.yaml"),
            "services:\n  App\\\\Service: ~\n",
        ),
    ];
    let indexer = Arc::new(MockIndexer::default());
    let mut scanner = scanner(root.path(), &root.path().join("test.db"), None);
    scanner.add_indexer(indexer.clone());
    let files: Vec<String> = paths.iter().map(|path| text(path)).collect();
    scanner.index_files(&none(), &files).unwrap();
    for path in &paths {
        assert!(indexer.has(path), "{}", path.display());
    }
}

#[test]
fn skips_nested_vendor_copy_of_workspace_package() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    write(
        &root.path().join("composer.json"),
        r#"{"name":"shopware/platform","replace":{"shopware/core":"self.version"}}"#,
    );
    let workspace = write(&root.path().join("src/Core/Product.php"), "<?php");
    let vendored = write(
        &root
            .path()
            .join("custom/plugins/Swag/vendor/shopware/core/Product.php"),
        "<?php",
    );
    let extension_only = write(
        &root
            .path()
            .join("custom/plugins/Swag/vendor/acme/pdf/Writer.php"),
        "<?php",
    );
    let indexer = Arc::new(MockIndexer::default());
    let mut scanner = scanner(root.path(), &cache.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    scanner.index_all(&none()).unwrap();
    assert!(indexer.has(&workspace));
    assert!(!indexer.has(&vendored));
    assert!(indexer.has(&extension_only));
}

#[test]
fn keeps_root_vendor_package() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    write(
        &root.path().join("composer.json"),
        r#"{"name":"acme/shop"}"#,
    );
    let root_vendor = write(
        &root.path().join("vendor/shopware/core/Product.php"),
        "<?php",
    );
    let vendored = write(
        &root
            .path()
            .join("custom/plugins/Swag/vendor/shopware/core/Product.php"),
        "<?php",
    );
    let indexer = Arc::new(MockIndexer::default());
    let mut scanner = scanner(root.path(), &cache.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    scanner.index_all(&none()).unwrap();
    assert!(indexer.has(&root_vendor));
    assert!(!indexer.has(&vendored));
}

#[test]
fn resource_paths_use_persisted_index() {
    let root = tempfile::tempdir().unwrap();
    let scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    seed_states(
        &scanner,
        &[
            ("/w/public/a.css", 1, 1),
            ("/w/public/b/c.js", 1, 1),
            ("/w/public-other/x.css", 1, 1),
            ("/w/publicity.css", 1, 1),
        ],
    );
    assert_eq!(
        scanner.resource_paths("/w/public", 10).unwrap(),
        ["/w/public/a.css", "/w/public/b/c.js"]
    );
    assert_eq!(
        scanner.resource_paths("/w/public/", 1).unwrap(),
        ["/w/public/a.css"]
    );
    assert!(scanner.resource_paths("/w/public", 0).unwrap().is_empty());
}

#[test]
fn indexes_and_refreshes_phar_sources() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let archive = root.path().join("vendor/analysis/analysis.phar");
    write_phar(
        &archive,
        &[
            ("src/Library.php", "<?php class First {}"),
            ("README.md", "not source"),
        ],
    );
    set_mtime(&archive, 1_700_000_000, 123);
    // Composer-generated launchers can end in .phar without being archives.
    write(
        &root.path().join("vendor/bin/analysis.phar"),
        "<?php require __DIR__.'/../analysis/analysis.phar';",
    );

    let recorder = Arc::new(PharRecordingIndexer::default());
    let mut scanner = scanner(root.path(), &cache.path().join("scanner.db"), None);
    scanner.add_indexer(recorder.clone());
    scanner.index_all(&none()).unwrap();
    assert_eq!(recorder.calls.load(Ordering::SeqCst), 1);
    let sources = recorder.snapshot();
    assert_eq!(sources.len(), 1);
    let (source_path, content) = sources.into_iter().next().unwrap();
    assert_eq!(content, "<?php class First {}");
    assert!(crate::paths::path_within(
        &scanner.inner.phar_cache,
        &source_path
    ));

    // An unchanged warm scan neither re-extracts nor re-indexes.
    scanner.index_all(&none()).unwrap();
    assert_eq!(recorder.calls.load(Ordering::SeqCst), 1);

    write_phar(&archive, &[("src/Library.php", "<?php class Other {}")]);
    set_mtime(&archive, 1_700_000_001, 123);
    scanner.index_all(&none()).unwrap();
    assert_eq!(recorder.calls.load(Ordering::SeqCst), 2);
    assert_eq!(recorder.snapshot()[&source_path], "<?php class Other {}");
}

#[test]
fn rebuilds_incomplete_phar_cache() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    write_phar(
        &root.path().join("library.phar"),
        &[
            ("src/One.php", "<?php class One {}"),
            ("src/Two.php", "<?php class Two {}"),
        ],
    );
    let scanner = scanner(root.path(), &cache.path().join("scanner.db"), None);
    let first = scanner.inner.discover_files(&none()).unwrap();
    assert_eq!(first.len(), 2);
    std::fs::remove_file(&first[0]).unwrap();
    let second = scanner.inner.discover_files(&none()).unwrap();
    assert_eq!(first, second);
    assert!(second.iter().all(|path| Path::new(path).exists()));
}

#[test]
fn only_accepts_workspace_and_phar_cache_paths() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let scanner = scanner(root.path(), &cache.path().join("scanner.db"), None);
    assert!(scanner.should_index_path(&text(&root.path().join("src/File.php"))));
    assert!(
        !scanner.should_index_path(&text(&root.path().parent().unwrap().join("unrelated.php")))
    );
    let cached = crate::paths::join(&[
        &scanner.inner.phar_cache,
        "archive",
        "files",
        "tests",
        "File.php",
    ]);
    assert!(scanner.should_index_path(&cached));
    assert!(scanner.inner.should_preparse_path(&cached));
}

#[test]
fn watcher_indexes_changes_and_removals() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let existing = write(&root.path().join("src/Existing.php"), "<?php");
    let indexer = Arc::new(MockIndexer::default());
    let updates = Arc::new(AtomicUsize::new(0));
    let mut scanner = scanner(root.path(), &cache.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    let counter = updates.clone();
    scanner.set_on_update(move || {
        counter.fetch_add(1, Ordering::SeqCst);
    });
    scanner.index_all(&none()).unwrap();
    scanner.start_watcher().unwrap();

    let created = write(
        &root.path().join("src/Created.php"),
        "<?php class Created {}",
    );
    let nested = write(&root.path().join("src/New/Deep/Nested.php"), "<?php");
    write(&root.path().join("node_modules/pkg/Ignored.php"), "<?php");
    assert!(eventually(Duration::from_secs(5), || indexer.has(&created)
        && indexer.has(&nested)));
    assert!(!indexer.has(&root.path().join("node_modules/pkg/Ignored.php")));

    std::fs::remove_file(&existing).unwrap();
    std::fs::remove_dir_all(root.path().join("src/New")).unwrap();
    assert!(eventually(Duration::from_secs(5), || !indexer
        .has(&existing)
        && !indexer.has(&nested)));
    assert!(updates.load(Ordering::SeqCst) > 0);
    scanner.close().unwrap();
}

#[test]
fn watcher_refreshes_phar_sources() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let archive = root.path().join("library.phar");
    write_phar(&archive, &[("src/Library.php", "<?php class Before {}")]);
    set_mtime(&archive, 1_700_000_000, 0);
    let recorder = Arc::new(PharRecordingIndexer::default());
    let mut scanner = scanner(root.path(), &cache.path().join("scanner.db"), None);
    scanner.add_indexer(recorder.clone());
    scanner.index_all(&none()).unwrap();
    scanner.start_watcher().unwrap();
    write_phar(&archive, &[("src/Library.php", "<?php class After {}")]);
    set_mtime(&archive, 1_700_000_001, 0);
    assert!(eventually(Duration::from_secs(5), || {
        recorder.calls.load(Ordering::SeqCst) >= 2
            && recorder
                .snapshot()
                .values()
                .any(|content| content == "<?php class After {}")
    }));
    scanner.close().unwrap();
}

#[test]
fn errors_wrap_indexer_context() {
    let root = tempfile::tempdir().unwrap();
    let path = write(&root.path().join("broken.php"), "<?php");
    let failing = Arc::new(ControlledIndexer {
        error: Some("boom".to_owned()).into(),
        ..Default::default()
    });
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(failing);
    let error = scanner.index_files(&none(), &[text(&path)]).unwrap_err();
    assert_eq!(
        error.to_string(),
        format!("controlled index {}: boom", text(&path))
    );
    assert!(!matches!(error, Error::Cancelled));
    let _ = AtomicBool::new(false);
}

#[test]
fn indexed_content_matches_only_for_the_scanned_disk_state() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let path = write(&root.path().join("component.php"), "<?php one");
    let indexer = Arc::new(MockIndexer::default());
    let mut scanner = scanner(root.path(), &cache.path().join("scanner.db"), None);
    scanner.add_indexer(indexer);
    let file = text(&path);
    // Not scanned yet: the index cannot reflect the file.
    assert!(!scanner.indexed_content_matches(&file, b"<?php one"));
    scanner
        .index_files(&none(), std::slice::from_ref(&file))
        .unwrap();
    assert!(scanner.indexed_content_matches(&file, b"<?php one"));
    // Unsaved edits differ from disk.
    assert!(!scanner.indexed_content_matches(&file, b"<?php two"));
    assert!(!scanner.indexed_content_matches(&file, b"<?php on!"));
    // Saved but not rescanned yet: the stored state is stale.
    std::fs::write(&path, "<?php changed").unwrap();
    let later = std::time::SystemTime::now() + std::time::Duration::from_secs(5);
    std::fs::File::options()
        .write(true)
        .open(&path)
        .unwrap()
        .set_modified(later)
        .unwrap();
    assert!(!scanner.indexed_content_matches(&file, b"<?php changed"));
    scanner
        .index_files(&none(), std::slice::from_ref(&file))
        .unwrap();
    assert!(scanner.indexed_content_matches(&file, b"<?php changed"));
    assert!(!scanner.indexed_content_matches(&text(&root.path().join("missing.php")), b""));
}

fn stored_mtime(scanner: &FileScanner, path: &str) -> i64 {
    scanner
        .with_db(|connection| {
            Ok(connection.query_row(
                "SELECT mtime FROM file_hashes WHERE path = ?",
                [path],
                |row| row.get(0),
            )?)
        })
        .unwrap()
}

#[test]
fn touched_files_with_unchanged_content_are_not_reindexed() {
    let root = tempfile::tempdir().unwrap();
    let file = write(&root.path().join("touched.php"), "<?php echo 1;");
    let path = text(&file);
    let indexer = Arc::new(MockIndexer::default());
    let mut scanner = scanner(root.path(), &root.path().join("scanner.db"), None);
    scanner.add_indexer(indexer.clone());
    set_mtime(&file, 1_000, 0);
    scanner.index_files(&none(), &[&path]).unwrap();
    assert!(indexer.has(&file));

    // Same content, new modification time (git checkout, touch): the
    // tracked state follows the file, the indexer does not run.
    indexer.indexed.lock().unwrap().clear();
    set_mtime(&file, 2_000, 0);
    scanner.index_files(&none(), &[&path]).unwrap();
    assert!(!indexer.has(&file));
    assert_eq!(stored_mtime(&scanner, &path), 2_000_000_000_000);

    // Same size, different content: reindexed.
    std::fs::write(&file, "<?php echo 2;").unwrap();
    set_mtime(&file, 3_000, 0);
    scanner.index_files(&none(), &[&path]).unwrap();
    assert!(indexer.has(&file));
    assert_eq!(stored_mtime(&scanner, &path), 3_000_000_000_000);
}
