use std::collections::HashMap;
use std::time::Duration;

use crate::{CancelToken, DataIndexer, Error, Store};

fn store() -> (tempfile::TempDir, Store) {
    let directory = tempfile::tempdir().unwrap();
    let store = Store::open(directory.path().join("indexes.db")).unwrap();
    (directory, store)
}

#[test]
fn isolates_repository_namespaces() {
    let (_directory, store) = store();
    let numbers = DataIndexer::<i64>::in_store(&store, "numbers").unwrap();
    let words = DataIndexer::<String>::in_store(&store, "words").unwrap();
    numbers.save_item("/a", "same-key", &42).unwrap();
    words
        .save_item("/b", "same-key", &"forty-two".to_owned())
        .unwrap();
    assert_eq!(numbers.get_values("same-key").unwrap(), [42]);
    assert_eq!(words.get_values("same-key").unwrap(), ["forty-two"]);
    // A repository in a shared store does not own the connection.
    numbers.close().unwrap();
    words
        .save_item("/c", "another-key", &"value".to_owned())
        .unwrap();
    store.close().unwrap();
}

#[test]
fn mutation_wait_honors_cancellation() {
    let (_directory, store) = store();
    let active = store.begin_mutation(&CancelToken::new()).unwrap();
    let cancel = CancelToken::new();
    let waiter = {
        let store = store.clone();
        let cancel = cancel.clone();
        std::thread::spawn(move || store.begin_mutation(&cancel).map(|_| ()))
    };
    std::thread::sleep(Duration::from_millis(30));
    cancel.cancel();
    assert!(matches!(waiter.join().unwrap(), Err(Error::Cancelled)));
    active.rollback().unwrap();
    store.close().unwrap();
}

#[test]
fn nested_mutation_on_the_same_thread_fails_instead_of_deadlocking() {
    let (_directory, store) = store();
    let active = store.begin_mutation(&CancelToken::new()).unwrap();
    let repository = DataIndexer::<String>::in_store(&store, "nested").unwrap();
    assert!(
        repository
            .save_item("/a", "k", &"v".to_owned())
            .unwrap_err()
            .to_string()
            .contains("already active")
    );
    active.rollback().unwrap();
    repository.save_item("/a", "k", &"v".to_owned()).unwrap();
    store.close().unwrap();
}

#[test]
fn allows_committed_repository_reads_during_mutation() {
    let (_directory, store) = store();
    let repository = DataIndexer::<String>::in_store(&store, "services").unwrap();
    repository
        .save_item("/old.php", "old", &"committed".to_owned())
        .unwrap();
    let mutation = store.begin_mutation(&CancelToken::new()).unwrap();
    repository
        .batch_save_items_in(
            Some(&mutation),
            HashMap::from([("/new.php", HashMap::from([("new", "pending".to_owned())]))]),
        )
        .unwrap();
    assert_eq!(repository.get_values("old").unwrap(), ["committed"]);
    assert!(repository.get_values("new").unwrap().is_empty());
    assert!(
        repository
            .has_file_path_in(Some(&mutation), "/new.php")
            .unwrap()
    );
    assert!(!repository.has_file_path_in(None, "/new.php").unwrap());
    mutation.commit().unwrap();
    assert_eq!(repository.get_values("new").unwrap(), ["pending"]);
    assert!(repository.has_file_path_in(None, "/new.php").unwrap());
    store.close().unwrap();
}

#[test]
fn mutation_shares_prepared_statements_across_repositories() {
    let (_directory, store) = store();
    let first = DataIndexer::<String>::in_store(&store, "first").unwrap();
    let second = DataIndexer::<String>::in_store(&store, "second").unwrap();
    let mutation = store.begin_mutation(&CancelToken::new()).unwrap();
    first
        .batch_save_items_in(Some(&mutation), [("/a.php", [("a", "first".to_owned())])])
        .unwrap();
    second
        .batch_save_items_in(Some(&mutation), [("/b.php", [("b", "second".to_owned())])])
        .unwrap();
    assert_eq!(mutation.prepared_statement_count(), 2);
    mutation.commit().unwrap();
    assert_eq!(mutation.prepared_statement_count(), 0);
    assert_eq!(first.get_values("a").unwrap(), ["first"]);
    assert_eq!(second.get_values("b").unwrap(), ["second"]);
    store.close().unwrap();
}

#[test]
fn delete_only_replacement_skips_insert_statement() {
    let (_directory, store) = store();
    let repository = DataIndexer::<String>::in_store(&store, "services").unwrap();
    repository
        .save_item("/removed.php", "service", &"value".to_owned())
        .unwrap();
    let mutation = store.begin_mutation(&CancelToken::new()).unwrap();
    repository
        .batch_save_items_in(
            Some(&mutation),
            [("/removed.php", Vec::<(&str, String)>::new())],
        )
        .unwrap();
    assert_eq!(mutation.prepared_statement_count(), 1);
    mutation.commit().unwrap();
    assert!(
        repository
            .get_values_by_path("/removed.php")
            .unwrap()
            .is_empty()
    );
    store.close().unwrap();
}

#[test]
fn mutation_batches_file_removals_across_repositories() {
    let (_directory, store) = store();
    let first = DataIndexer::<i64>::in_store(&store, "first").unwrap();
    let second = DataIndexer::<i64>::in_store(&store, "second").unwrap();
    let paths: Vec<String> = (0..257)
        .map(|index| format!("/file-{index:03}.php"))
        .collect();
    let items: Vec<(String, Vec<(&str, i64)>)> = paths
        .iter()
        .enumerate()
        .map(|(index, path)| (path.clone(), vec![("value", index as i64)]))
        .collect();
    first.batch_save_items(items.clone()).unwrap();
    second.batch_save_items(items).unwrap();
    assert_eq!(first.count_all_values().unwrap(), 257);

    let mutation = store.begin_mutation(&CancelToken::new()).unwrap();
    first
        .batch_delete_by_file_paths_in(Some(&mutation), &paths)
        .unwrap();
    second
        .batch_delete_by_file_paths_in(Some(&mutation), &paths)
        .unwrap();
    // The 128-path statement and the one-path tail are shared by both.
    assert_eq!(mutation.prepared_statement_count(), 2);
    mutation.commit().unwrap();
    assert!(first.get_all_values().unwrap().is_empty());
    assert!(second.get_all_values().unwrap().is_empty());
    store.close().unwrap();
}

#[test]
fn rolled_back_mutation_discards_writes_and_callbacks() {
    let (_directory, store) = store();
    let repository = DataIndexer::<String>::in_store(&store, "rollback").unwrap();
    let published = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
    {
        let mutation = store.begin_mutation(&CancelToken::new()).unwrap();
        repository
            .batch_save_items_in(Some(&mutation), [("/a.php", [("k", "v".to_owned())])])
            .unwrap();
        let published = published.clone();
        mutation
            .after_commit(move || published.store(true, std::sync::atomic::Ordering::SeqCst))
            .unwrap();
        // Dropped without commit.
    }
    assert!(!published.load(std::sync::atomic::Ordering::SeqCst));
    assert!(repository.get_all_values().unwrap().is_empty());

    let mutation = store.begin_mutation(&CancelToken::new()).unwrap();
    let flag = published.clone();
    mutation
        .after_commit(move || flag.store(true, std::sync::atomic::Ordering::SeqCst))
        .unwrap();
    mutation.commit().unwrap();
    assert!(published.load(std::sync::atomic::Ordering::SeqCst));
    assert!(mutation.commit().is_err());
    mutation.rollback().unwrap();
    store.close().unwrap();
}

#[test]
fn rebuilds_legacy_file_association_schema() {
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join("indexes.db");
    {
        let legacy = rusqlite::Connection::open(&path).unwrap();
        legacy
            .execute_batch(
                "CREATE TABLE data (id INTEGER PRIMARY KEY AUTOINCREMENT, namespace TEXT NOT NULL, key TEXT NOT NULL, value BLOB NOT NULL);
                 CREATE TABLE files (namespace TEXT NOT NULL, file_path TEXT NOT NULL, data_id INTEGER NOT NULL);
                 INSERT INTO data(namespace, key, value) VALUES ('legacy', 'key', X'01');
                 INSERT INTO files(namespace, file_path, data_id) VALUES ('legacy', '/old.php', 1);",
            )
            .unwrap();
    }
    let store = Store::open(&path).unwrap();
    let (tables, rows): (i64, i64) = store
        .read(|connection| {
            Ok((
                connection.query_row(
                    "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'files'",
                    [],
                    |row| row.get(0),
                )?,
                connection.query_row("SELECT COUNT(*) FROM data", [], |row| row.get(0))?,
            ))
        })
        .unwrap();
    assert_eq!((tables, rows), (0, 0));
    let repository = DataIndexer::<String>::in_store(&store, "current").unwrap();
    repository
        .save_item("/new.php", "key", &"value".to_owned())
        .unwrap();
    assert_eq!(
        repository.get_values_by_path("/new.php").unwrap(),
        ["value"]
    );
    store.close().unwrap();
    store.close().unwrap();
    assert!(store.begin_mutation(&CancelToken::new()).is_err());
}

/// Commits never checkpoint inline (the writer disables SQLite's automatic
/// checkpoint); the background checkpointer moves committed WAL frames into
/// the database file shortly after.
#[test]
fn background_checkpointer_moves_commits_into_the_database_file() {
    let (directory, store) = store();
    let path = directory.path().join("indexes.db");
    let repository = DataIndexer::<String>::in_store(&store, "blobs").unwrap();
    let before = std::fs::metadata(&path).unwrap().len();
    let payload = "x".repeat(2 * 1024 * 1024);
    repository.save_item("/big.php", "big", &payload).unwrap();
    let deadline = std::time::Instant::now() + Duration::from_secs(10);
    while std::fs::metadata(&path).unwrap().len() < before + payload.len() as u64 / 2 {
        assert!(
            std::time::Instant::now() < deadline,
            "committed frames were not checkpointed"
        );
        std::thread::sleep(Duration::from_millis(20));
    }
    assert_eq!(repository.get_values("big").unwrap(), [payload]);
    store.close().unwrap();
}
