use std::sync::Mutex;
use std::sync::atomic::{AtomicUsize, Ordering};

use swlsp_indexer::CancelToken;

use super::*;

fn yaml(source: &str) -> SyntaxNode {
    swlsp_parser_yaml::parse(source).root()
}

fn file(path: &str, source: &str) -> ParsedFile {
    ParsedFile::new(path, source.as_bytes().to_vec())
}

fn path_str(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

#[test]
fn config_index_precedence_and_full_persistence() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let root = path_str(project.path());
    let source = |name: &str| {
        format!(
            "build:\n  shopwareVersionConstraint: '{name}'\nchangelog:\n  variables: {{ticket: 'ABC'}}\nstore:\n  tags: {{en: [one, two]}}\nvalidation:\n  store_compliance: true\n"
        )
    };
    let mut index = ConfigIndex::new(cache.path(), None).unwrap();
    for location in CONFIG_LOCATIONS {
        let path = format!("{root}/{location}");
        index.index(&file(&path, &source(location))).unwrap();
    }
    let check = |index: &ConfigIndex, location: &str| -> Arc<Config> {
        let config = index.get_config(&root).unwrap();
        assert_eq!(config.storage_location, format!("{root}/{location}"));
        assert_eq!(config.build.shopware_version_constraint, location);
        assert_eq!(
            config.changelog.variables,
            [("ticket".to_owned(), "ABC".to_owned())].into()
        );
        assert_eq!(
            config.store.tags.english.as_deref(),
            Some(&["one".to_owned(), "two".to_owned()][..])
        );
        config
    };
    let previous = check(&index, CONFIG_LOCATIONS[0]);
    // A lower-priority editor buffer does not displace the selected disk config.
    let low = format!("{root}/{}", CONFIG_LOCATIONS[2]);
    index.update_live_document(&low, &yaml("validation: {store_compliance: false}"));
    assert!(
        check(&index, CONFIG_LOCATIONS[0])
            .validation
            .store_compliance
    );
    index.remove_live_document(&low);
    index.close().unwrap();

    index = ConfigIndex::new(cache.path(), None).unwrap();
    check(&index, CONFIG_LOCATIONS[0]);
    index
        .removed_files(&[format!("{root}/{}", CONFIG_LOCATIONS[0])])
        .unwrap();
    check(&index, CONFIG_LOCATIONS[1]);
    index
        .removed_files(&[format!("{root}/{}", CONFIG_LOCATIONS[1])])
        .unwrap();
    check(&index, CONFIG_LOCATIONS[2]);
    // An empty higher-priority file is still selected; files are never merged.
    let high = format!("{root}/{}", CONFIG_LOCATIONS[0]);
    index.update_live_document(&high, &yaml(""));
    let config = index.get_config(&root).unwrap();
    assert_eq!(config.storage_location, high);
    assert!(!config.validation.store_compliance);
    assert!(config.build.shopware_version_constraint.is_empty());
    index.remove_live_document(&high);
    check(&index, CONFIG_LOCATIONS[2]);
    assert_eq!(
        previous.build.shopware_version_constraint, CONFIG_LOCATIONS[0],
        "published snapshots must remain immutable"
    );
    index.close().unwrap();
}

#[test]
fn store_compliance_persistence_and_overlay() {
    let cache = tempfile::tempdir().unwrap();
    let project = tempfile::tempdir().unwrap();
    let dir = format!("{}/Plugin", path_str(project.path()));
    let path = format!("{dir}/.shopware-extension.yml");
    let check = |index: &ConfigIndex, want: bool| {
        assert_eq!(index.store_compliance_enabled(&dir).unwrap(), want);
    };
    let mut index = ConfigIndex::new(cache.path(), None).unwrap();
    check(&index, false);
    let changes = Arc::new(AtomicUsize::new(0));
    let seen = Arc::new(Mutex::new(Vec::new()));
    {
        let changes = changes.clone();
        let seen = seen.clone();
        index.set_on_change(Some(Arc::new(move |changed: &str| {
            changes.fetch_add(1, Ordering::SeqCst);
            seen.lock().unwrap().push(changed.to_owned());
        })));
    }
    index
        .index(&file(&path, "validation:\n  store_compliance: true\n"))
        .unwrap();
    check(&index, true);
    index.update_live_document(&path, &yaml("validation:\n  store_compliance: false\n"));
    check(&index, false);
    index
        .index(&file(&path, "validation:\n  store_compliance: true\n"))
        .unwrap();
    check(&index, false);
    index.remove_live_document(&path);
    check(&index, true);
    assert_eq!(changes.load(Ordering::SeqCst), 4);
    assert!(seen.lock().unwrap().iter().all(|changed| *changed == path));
    index.close().unwrap();

    index = ConfigIndex::new(cache.path(), None).unwrap();
    check(&index, true);
    index.index(&file(&path, "validation: {}\n")).unwrap();
    check(&index, false);
    index
        .index(&file(&path, "validation: {store_compliance: true}"))
        .unwrap();
    check(&index, true);
    index.removed_files(std::slice::from_ref(&path)).unwrap();
    check(&index, false);
    index.close().unwrap();
}

#[test]
fn store_compliance_removal_publishes_after_commit() {
    let cache = tempfile::tempdir().unwrap();
    let store = Store::open(cache.path().join("indexes.db")).unwrap();
    let index = ConfigIndex::new(cache.path(), Some(&store)).unwrap();
    let project = tempfile::tempdir().unwrap();
    let dir = path_str(project.path());
    let path = format!("{dir}/.shopware-extension.yml");
    index
        .index(&file(&path, "validation: {store_compliance: true}"))
        .unwrap();
    let changes = Arc::new(AtomicUsize::new(0));
    {
        let changes = changes.clone();
        index.set_on_change(Some(Arc::new(move |_: &str| {
            changes.fetch_add(1, Ordering::SeqCst);
        })));
    }
    let mutation = store.begin_mutation(&CancelToken::new()).unwrap();
    index
        .removed_files_in(std::slice::from_ref(&path), &mutation)
        .unwrap();
    assert_eq!(changes.load(Ordering::SeqCst), 0);
    mutation.rollback().unwrap();
    assert!(index.store_compliance_enabled(&dir).unwrap());

    let mutation = store.begin_mutation(&CancelToken::new()).unwrap();
    index
        .removed_files_in(std::slice::from_ref(&path), &mutation)
        .unwrap();
    mutation.commit().unwrap();
    assert_eq!(changes.load(Ordering::SeqCst), 1);
    assert!(!index.store_compliance_enabled(&dir).unwrap());
    index.close().unwrap();
    store.close().unwrap();
}
