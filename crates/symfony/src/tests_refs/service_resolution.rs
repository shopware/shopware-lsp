use swlsp_indexer::{Indexer, ParsedFile};

use crate::ServiceIndex;

#[test]
fn resolve_service_class_name_follows_aliases() {
    let root = tempfile::tempdir().unwrap();
    let cache = tempfile::tempdir().unwrap();
    let index = ServiceIndex::new(root.path().to_str().unwrap(), cache.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/project/services.yaml",
            b"services:\n  app.alias:\n    alias: app.concrete\n  app.concrete:\n    class: App\\Concrete\n  app.cycle_a:\n    alias: app.cycle_b\n  app.cycle_b:\n    alias: app.cycle_a\n".to_vec(),
        ))
        .unwrap();
    assert_eq!(
        index
            .resolve_service_class_name("app.alias")
            .unwrap()
            .as_deref(),
        Some(r"App\Concrete")
    );
    assert_eq!(
        index.resolve_service_class_name("app.cycle_a").unwrap(),
        None
    );
    index.close().unwrap();
}
