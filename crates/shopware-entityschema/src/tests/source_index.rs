use swlsp_indexer::{Indexer, ParsedFile};

use crate::*;

#[test]
fn source_index_update_removal_and_persistence() {
    let cache = tempfile::tempdir().unwrap();
    let workspace = tempfile::tempdir().unwrap();
    let path = workspace
        .path()
        .join("src")
        .join("Odd.php")
        .to_string_lossy()
        .into_owned();
    let candidate = "<?php
namespace Acme;
use Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition;
final class Odd extends EntityDefinition {
    public const ENTITY_NAME = 'odd';
    protected function defineFields(): FieldCollection { return new FieldCollection([]); }
}";
    let index = SourceIndex::new(cache.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            path.clone(),
            candidate.as_bytes().to_vec(),
        ))
        .unwrap();
    assert_eq!(index.source(&path).unwrap().as_deref(), Some(candidate));

    index
        .index(&ParsedFile::new(
            path.clone(),
            b"<?php final class Odd {}".to_vec(),
        ))
        .unwrap();
    assert_eq!(index.source(&path).unwrap(), None);

    index
        .index(&ParsedFile::new(
            path.clone(),
            candidate.as_bytes().to_vec(),
        ))
        .unwrap();
    index.close().unwrap();
    drop(index);
    let index = SourceIndex::new(cache.path(), None).unwrap();
    assert_eq!(index.source(&path).unwrap().as_deref(), Some(candidate));

    index.removed_files(std::slice::from_ref(&path)).unwrap();
    assert_eq!(index.source(&path).unwrap(), None);
    index.close().unwrap();
}
