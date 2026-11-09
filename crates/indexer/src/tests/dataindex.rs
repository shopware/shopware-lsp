use std::collections::HashMap;
use std::sync::Arc;

use serde::{Deserialize, Serialize};

use crate::{DataIndexer, Error, decode_value};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
struct Sample {
    name: String,
    value: i64,
}

fn sample(name: &str, value: i64) -> Sample {
    Sample {
        name: name.into(),
        value,
    }
}

fn open() -> (tempfile::TempDir, DataIndexer<Sample>) {
    let directory = tempfile::tempdir().unwrap();
    let index = DataIndexer::open(directory.path().join("test.db")).unwrap();
    (directory, index)
}

fn sorted(mut values: Vec<Sample>) -> Vec<Sample> {
    values.sort_by(|left, right| left.name.cmp(&right.name));
    values
}

#[test]
fn saves_and_reads_items() {
    let (_directory, index) = open();
    index.save_item("/a.php", "key", &sample("a", 1)).unwrap();
    index.save_item("/b.php", "key", &sample("b", 2)).unwrap();
    assert_eq!(
        sorted(index.get_values("key").unwrap()),
        [sample("a", 1), sample("b", 2)]
    );
    assert!(index.get_values("missing").unwrap().is_empty());
    let mut keys = index.get_all_keys().unwrap();
    keys.sort();
    assert_eq!(keys, ["key"]);
    index.delete_by_file_path("/a.php").unwrap();
    assert_eq!(index.get_values("key").unwrap(), [sample("b", 2)]);
    index.clear().unwrap();
    assert!(index.get_all_values().unwrap().is_empty());
    index.close().unwrap();
    index.close().unwrap();
}

#[test]
fn has_any_key_except_fold() {
    let (_directory, index) = open();
    assert!(!index.has_any_key_except_fold(&[]).unwrap());
    index
        .save_item("/a.php", "Default", &sample("a", 1))
        .unwrap();
    assert!(index.has_any_key_except_fold(&[]).unwrap());
    assert!(!index.has_any_key_except_fold(&[" default ", ""]).unwrap());
    index
        .save_item("/b.php", "custom", &sample("b", 2))
        .unwrap();
    assert!(index.has_any_key_except_fold(&["DEFAULT"]).unwrap());
    // Cached keys answer the same question.
    index.get_all_keys().unwrap();
    assert!(index.has_any_key_except_fold(&["DEFAULT"]).unwrap());
    assert!(
        !index
            .has_any_key_except_fold(&["DEFAULT", "Custom"])
            .unwrap()
    );
}

#[test]
fn batch_write_replaces_paths_and_batch_delete_removes_them() {
    let (_directory, index) = open();
    index
        .batch_save_items(HashMap::from([
            (
                "/a.php".to_owned(),
                HashMap::from([
                    ("k1".to_owned(), sample("a1", 1)),
                    ("k2".to_owned(), sample("a2", 2)),
                ]),
            ),
            (
                "/b.php".to_owned(),
                HashMap::from([("k1".to_owned(), sample("b1", 3))]),
            ),
        ]))
        .unwrap();
    assert_eq!(index.count_all_values().unwrap(), 3);
    let mut keys = index.get_all_keys_by_path("/a.php").unwrap();
    keys.sort();
    assert_eq!(keys, ["k1", "k2"]);
    // Replacing a path removes its old entries.
    index
        .batch_save_items([("/a.php", [("k3", sample("a3", 4))])])
        .unwrap();
    assert_eq!(
        index.get_values_by_path("/a.php").unwrap(),
        [sample("a3", 4)]
    );
    assert_eq!(index.get_all_keys_by_path("/a.php").unwrap(), ["k3"]);
    assert_eq!(index.get_values("k1").unwrap(), [sample("b1", 3)]);
    let mut paths = index.get_all_file_paths().unwrap();
    paths.sort();
    assert_eq!(paths, ["/a.php", "/b.php"]);

    index
        .batch_delete_by_file_paths(&["/a.php", "/b.php"])
        .unwrap();
    assert!(index.get_all_values().unwrap().is_empty());
    index.batch_delete_by_file_paths::<&str>(&[]).unwrap();
}

#[test]
fn value_views_reuse_cached_catalogs() {
    let (_directory, index) = open();
    index
        .batch_save_items([("file.txt", [("key", sample("cached", 1))])])
        .unwrap();
    let all_first = index.get_all_values_view().unwrap();
    let all_second = index.get_all_values_view().unwrap();
    assert!(Arc::ptr_eq(&all_first, &all_second));
    let key_first = index.get_values_view("key").unwrap();
    assert!(Arc::ptr_eq(
        &key_first,
        &index.get_values_view("key").unwrap()
    ));
    let mut copied = index.get_all_values().unwrap();
    copied[0] = sample("changed", 0);
    assert_eq!(all_first[0].name, "cached");
    // A committed write invalidates the views.
    index
        .save_item("other.txt", "key", &sample("other", 2))
        .unwrap();
    assert_eq!(index.get_all_values_view().unwrap().len(), 2);
    assert_eq!(all_first.len(), 1);
}

#[test]
fn revision_and_path_view_follow_committed_changes() {
    let (_directory, index) = open();
    let initial = index.revision();
    let empty = index.get_all_file_paths_view().unwrap();
    assert!(empty.is_empty());
    assert!(Arc::ptr_eq(
        &empty,
        &index.get_all_file_paths_view().unwrap()
    ));

    index.save_item("/b.php", "key", &sample("b", 1)).unwrap();
    index.save_item("/a.php", "key", &sample("a", 1)).unwrap();
    let saved = index.revision();
    assert!(saved > initial);
    assert_eq!(
        *index.get_all_file_paths_view().unwrap(),
        ["/a.php", "/b.php"]
    );
    // Reads do not advance it.
    index.get_all_values_view().unwrap();
    assert_eq!(index.revision(), saved);

    index.delete_by_file_path("/a.php").unwrap();
    assert!(index.revision() > saved);
    assert_eq!(index.get_all_file_paths().unwrap(), ["/b.php"]);

    // Writes that change no row leave the revision and the views alone:
    // every indexer sees every file, most of which have no data here.
    let unchanged = index.revision();
    let view = index.get_all_values_view().unwrap();
    index.delete_by_file_path("/other.yaml").unwrap();
    index
        .batch_save_items([("/other.yaml", Vec::<(&str, Sample)>::new())])
        .unwrap();
    assert_eq!(index.revision(), unchanged);
    assert!(Arc::ptr_eq(&view, &index.get_all_values_view().unwrap()));
}

#[test]
fn visits_encoded_values() {
    let (_directory, index) = open();
    index
        .batch_save_items([
            ("/first", [("first", sample("first", 1))]),
            ("/second", [("second", sample("second", 2))]),
        ])
        .unwrap();
    let mut visited = Vec::new();
    index
        .visit_all_encoded_values(|encoded| {
            visited.push(decode_value::<Sample>(encoded)?);
            Ok(())
        })
        .unwrap();
    assert_eq!(sorted(visited), [sample("first", 1), sample("second", 2)]);

    let error = index
        .visit_all_encoded_values(|_| Err(Error::msg("stop visiting")))
        .unwrap_err();
    assert!(error.to_string().contains("stop visiting"));
    assert!(error.to_string().starts_with("visit encoded value /"));

    let mut by_path = Vec::new();
    index
        .visit_encoded_values_by_path("/first", |encoded| {
            by_path.push(decode_value::<Sample>(encoded)?);
            Ok(())
        })
        .unwrap();
    assert_eq!(by_path, [sample("first", 1)]);

    let mut decoded = Vec::new();
    index
        .visit_all_values(|value| {
            decoded.push(value);
            Ok(())
        })
        .unwrap();
    assert_eq!(sorted(decoded), [sample("first", 1), sample("second", 2)]);
}

#[test]
fn concurrent_access() {
    let (_directory, index) = open();
    let index = Arc::new(index);
    std::thread::scope(|scope| {
        for worker in 0..8 {
            let index = index.clone();
            scope.spawn(move || {
                for item in 0..20 {
                    let path = format!("/worker-{worker}/{item}.php");
                    index
                        .batch_save_items([(path.as_str(), [("shared", sample(&path, item))])])
                        .unwrap();
                    index.get_values("shared").unwrap();
                }
            });
        }
    });
    assert_eq!(index.get_values("shared").unwrap().len(), 160);
}

#[test]
fn optional_fields_round_trip_with_named_encoding() {
    #[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
    struct Versioned {
        name: String,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        alias: Option<String>,
    }
    let directory = tempfile::tempdir().unwrap();
    let index = DataIndexer::<Versioned>::open(directory.path().join("test.db")).unwrap();
    index
        .save_item(
            "/a",
            "k",
            &Versioned {
                name: "a".into(),
                alias: None,
            },
        )
        .unwrap();
    index
        .save_item(
            "/b",
            "k",
            &Versioned {
                name: "b".into(),
                alias: Some("c".into()),
            },
        )
        .unwrap();
    let mut values = index.get_values("k").unwrap();
    values.sort_by(|left, right| left.name.cmp(&right.name));
    assert_eq!(values[0].alias, None);
    assert_eq!(values[1].alias.as_deref(), Some("c"));
}

#[test]
fn standalone_repositories_share_one_store_per_directory() {
    let directory = tempfile::tempdir().unwrap();
    let first: DataIndexer<Sample> =
        DataIndexer::repository(directory.path().join("first.db"), "first", None).unwrap();
    let second: DataIndexer<Sample> =
        DataIndexer::repository(directory.path().join("second.db"), "second", None).unwrap();
    // One database for the directory, namespaces keep the data apart.
    assert!(first.store().same(second.store()));
    assert!(
        directory
            .path()
            .join(crate::dataindex::SHARED_REPOSITORY_STORE)
            .exists()
    );
    assert!(!directory.path().join("first.db").exists());
    first.save_item("/a.php", "key", &sample("a", 1)).unwrap();
    second.save_item("/a.php", "key", &sample("b", 2)).unwrap();
    assert_eq!(first.get_values("key").unwrap(), [sample("a", 1)]);
    assert_eq!(second.get_values("key").unwrap(), [sample("b", 2)]);

    // Closing one repository leaves the shared store open for the other.
    first.close().unwrap();
    second.save_item("/c.php", "key", &sample("c", 3)).unwrap();
    // The last close closes the store; data persists for the next open.
    second.close().unwrap();
    drop((first, second));
    let reopened: DataIndexer<Sample> =
        DataIndexer::repository(directory.path().join("second.db"), "second", None).unwrap();
    assert_eq!(
        sorted(reopened.get_values("key").unwrap()),
        [sample("b", 2), sample("c", 3)]
    );

    // A repository dropped without close releases the store as well.
    drop(reopened);
    let again: DataIndexer<Sample> =
        DataIndexer::repository(directory.path().join("first.db"), "first", None).unwrap();
    assert_eq!(again.get_values("key").unwrap(), [sample("a", 1)]);
}

#[test]
fn large_values_are_stored_compressed_and_read_back_through_every_api() {
    let directory = tempfile::tempdir().unwrap();
    let store = crate::Store::open(directory.path().join("indexes.db")).unwrap();
    let compressed = DataIndexer::<Vec<Sample>>::in_store(&store, "compressed").unwrap();
    let plain = DataIndexer::<Vec<Sample>>::in_store(&store, "plain")
        .unwrap()
        .without_value_compression();
    let items: Vec<Sample> = (0..64)
        .map(|index| sample("repeated name", index))
        .collect();
    for repository in [&compressed, &plain] {
        repository
            .batch_save_items([("/large.php", [("key", items.clone())])])
            .unwrap();
        assert_eq!(
            repository.get_values("key").unwrap(),
            std::slice::from_ref(&items)
        );
        assert_eq!(
            repository.get_all_values().unwrap(),
            std::slice::from_ref(&items)
        );
        assert_eq!(
            repository.get_values_by_path("/large.php").unwrap(),
            std::slice::from_ref(&items)
        );
        let mut visited = Vec::new();
        repository
            .visit_all_encoded_values(|encoded| {
                visited.push(decode_value::<Vec<Sample>>(encoded)?);
                Ok(())
            })
            .unwrap();
        repository
            .visit_encoded_values_by_path("/large.php", |encoded| {
                visited.push(rmp_serde::from_slice(encoded).unwrap());
                Ok(())
            })
            .unwrap();
        assert_eq!(visited, [items.clone(), items.clone()]);
    }
    let stored_sizes: Vec<i64> = store
        .read(|connection| {
            let mut statement =
                connection.prepare("SELECT length(value) FROM data ORDER BY namespace = ?")?;
            let rows =
                statement.query_map([crate::store::namespace_id("plain")], |row| row.get(0))?;
            Ok(rows.collect::<rusqlite::Result<Vec<i64>>>()?)
        })
        .unwrap();
    let encoded = crate::encode_value(&items).unwrap().len() as i64;
    assert!(
        stored_sizes[0] < encoded / 2,
        "{stored_sizes:?} vs {encoded}"
    );
    assert_eq!(stored_sizes[1], encoded);
    compressed.close().unwrap();
    plain.close().unwrap();
    store.close().unwrap();
}
