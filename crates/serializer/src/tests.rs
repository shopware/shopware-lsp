use swlsp_indexer::{Indexer, ParsedFile};

use super::*;

#[test]
fn index_collects_serializer_deserialize_targets() {
    let dir = tempfile::tempdir().unwrap();
    let index = Index::new(dir.path(), None).unwrap();
    let source = r"<?php
namespace App;
final class Handler {
    public function run($serializer): void {
        $serializer->deserialize('one', Foobar::class, 'json');
        $serializer->deserialize('two', 'App\Foobar2', 'json');
        $serializer->deserialize('three', '\App\Foobar3[]', 'json');
        $serializer->deserialize('four', Foobar4::class . '[]', 'json');
        $serializer->deserialize('five', Foobar5::class . 'Suffix', 'json');
    }
}
";
    let path = "/project/src/Handler.php";
    index
        .index(&ParsedFile::new(path, source.as_bytes().to_vec()))
        .unwrap();
    let classes = index.classes().unwrap();
    assert_eq!(
        classes,
        [
            "App\\Foobar",
            "App\\Foobar2",
            "App\\Foobar3",
            "App\\Foobar4"
        ]
    );
    for class_name in &classes {
        let usages = index.usages(class_name).unwrap();
        assert_eq!(usages.len(), 1);
        assert_eq!(usages[0].file, path);
    }
    let usages = index.usages("\\APP\\FOOBAR4[]").unwrap();
    assert_eq!(usages.len(), 1);
    assert_eq!(usages[0].kind, TargetKind::ClassConstant);
    assert_eq!(&source[usages[0].range], "Foobar4::class");
    let usages = index.usages("App\\Foobar3").unwrap();
    assert_eq!(usages[0].kind, TargetKind::String);
    assert_eq!(&source[usages[0].range], "\\App\\Foobar3[]");
    index.close().unwrap();

    let restored = Index::new(dir.path(), None).unwrap();
    assert_eq!(restored.classes().unwrap(), classes);
    restored.close().unwrap();
}

#[test]
fn index_removes_stale_serializer_targets() {
    let dir = tempfile::tempdir().unwrap();
    let index = Index::new(dir.path(), None).unwrap();
    let path = "/project/src/Handler.php";
    index
        .index(&ParsedFile::new(
            path,
            br"<?php $serializer->deserialize($data, 'App\Model', 'json');".to_vec(),
        ))
        .unwrap();
    assert_eq!(index.usages("App\\Model").unwrap().len(), 1);

    index
        .index(&ParsedFile::new(path, b"<?php echo 'unchanged';".to_vec()))
        .unwrap();
    assert!(index.usages("App\\Model").unwrap().is_empty());
    index.close().unwrap();
}

#[test]
fn usage_at_includes_the_range_end() {
    let source = "<?php $s->deserialize($d, Model::class, 'json');";
    let root = swlsp_parser_php::parse(source).tree.root();
    let end = (source.find("Model::class").unwrap() + "Model::class".len()) as u32;
    assert_eq!(usage_at(&root, end).unwrap().class, "Model");
    assert!(usage_at(&root, end + 1).is_none());
}
