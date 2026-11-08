use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_syntax::TextSize;

use super::*;

#[test]
fn constraint_option_reference_and_inherited_properties() {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/project/vendor/Constraint.php",
            br"<?php
namespace Symfony\Component\Validator;
class Constraint { public string $message; public array $groups; }
"
            .to_vec(),
        ))
        .unwrap();
    let source = r"<?php
namespace App\Validation;
class UniqueName extends \Symfony\Component\Validator\Constraint {
    public string $repositoryMethod;
    protected string $internal;
}
function validate(): void {
    new UniqueName(['repositoryMethod' => 'find', 'message' => 'key']);
}
";
    let path = "/project/src/UniqueName.php";
    let parsed = ParsedFile::new(path, source.as_bytes().to_vec());
    index.index(&parsed).unwrap();
    let root = parsed.syntax_tree().unwrap().root();
    let offset = source.find("repositoryMethod'").unwrap() + 2;
    let node = root.node_at(TextSize::from(offset as u32)).unwrap();
    let context = index.document_context(path, 1, Some(&node), &root, source);

    let reference = option_reference_at(Some(&context), &root, &node).unwrap();
    assert_eq!(reference.name, "repositoryMethod");
    assert_eq!(reference.constraint, "App\\Validation\\UniqueName");

    let properties = constraint_properties_in_context(Some(&context), &reference.constraint);
    let mut names: Vec<&str> = properties.iter().map(|property| &*property.name).collect();
    names.sort_unstable();
    assert_eq!(names, ["groups", "message", "repositoryMethod"]);
    assert!(find_constraint_property(&properties, "REPOSITORYMETHOD").is_some());

    assert!(option_reference_at(None, &root, &node).is_none());
    index.close().unwrap();
}

#[test]
fn counterpart_class_links_constraints_and_validators() {
    let dir = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(dir.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/project/vendor/Validator.php",
            br"<?php
namespace Symfony\Component\Validator;
class Constraint {}
interface ConstraintValidatorInterface {}
"
            .to_vec(),
        ))
        .unwrap();
    index
        .index(&ParsedFile::new(
            "/project/src/Unique.php",
            br"<?php
namespace App;
class Unique extends \Symfony\Component\Validator\Constraint {}
class UniqueValidator implements \Symfony\Component\Validator\ConstraintValidatorInterface {}
class Unrelated {}
"
            .to_vec(),
        ))
        .unwrap();
    let constraint = index.find_class("App\\Unique").unwrap();
    let validator = index.find_class("App\\UniqueValidator").unwrap();
    let unrelated = index.find_class("App\\Unrelated").unwrap();
    assert_eq!(
        &*counterpart_class(&index, &constraint)
            .unwrap()
            .fully_qualified,
        "App\\UniqueValidator"
    );
    assert_eq!(
        &*counterpart_class(&index, &validator)
            .unwrap()
            .fully_qualified,
        "App\\Unique"
    );
    assert!(counterpart_class(&index, &unrelated).is_none());
    index.close().unwrap();
}
