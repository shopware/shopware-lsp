use std::path::Path;

use swlsp_php_project::Version as ProjectVersion;
use swlsp_php_semantic::{Flags, Symbol as SemanticSymbol, SymbolKind};
use swlsp_php_stubs::catalog::Catalog;

use super::*;

const CORE_STUB: &str = r#"<?php
/** @since 8.1 */
function added(): string {}
/** @removed 8.2 */
function removed(): bool {}
/** @return string|false */
#[LanguageLevelTypeAware(['8.2' => 'string'], default: 'string|false')]
function aware() {}
#[Deprecated(since: '8.2')]
function laterDeprecated(): void {}
/** @deprecated use replacement() */
#[Deprecated(since: '8.2')]
function documentedDeprecated(): void {}
#[\JetBrains\PhpStorm\ArrayShape(['id' => 'int'])]
function shaped(): array {}
function expected(
    #[\JetBrains\PhpStorm\ExpectedValues(values: ['auto', 7])]
    string|int $mode
): void {}
#[PhpStormStubsElementAvailable(from: '8.1')]
interface Feature {
    public function value(
        #[PhpStormStubsElementAvailable(from: '8.2')]
        #[LanguageLevelTypeAware(['8.2' => 'int'], default: '')]
        $mode = 0
    ): string;
}
"#;

const CORE_META: &str = r#"<?php
namespace PHPSTORM_META;
override(\aware(0), type(0));
override(\Feature::value(0), elementType(0));
"#;

fn fixture() -> (tempfile::TempDir, Lock) {
    let root = tempfile::tempdir().unwrap();
    std::fs::create_dir(root.path().join("Core")).unwrap();
    std::fs::write(root.path().join("Core/Core.php"), CORE_STUB).unwrap();
    std::fs::write(root.path().join("Core/.phpstorm.meta.php"), CORE_META).unwrap();
    let lock = Lock {
        repository: "https://example.com/phpstorm-stubs.git".to_owned(),
        commit: "0123456789012345678901234567890123456789".to_owned(),
        versions: vec!["8.0".to_owned(), "8.1".to_owned(), "8.2".to_owned()],
        directories: vec!["Core".to_owned()],
    };
    (root, lock)
}

fn find<'a>(
    symbols: &'a [SemanticSymbol],
    kind: SymbolKind,
    fully_qualified: &str,
) -> Vec<&'a SemanticSymbol> {
    symbols
        .iter()
        .filter(|symbol| symbol.kind == kind && symbol.fully_qualified == fully_qualified)
        .collect()
}

#[test]
fn build_versioned_catalog() {
    let (root, lock) = fixture();
    let (generated, stats) = build(root.path(), &lock, EmptyListPolicy::Committed).unwrap();
    assert_eq!(stats.files, 1);
    assert_eq!(stats.parser_errors, 0);
    assert_eq!(generated.materialize_contracts().len(), 2);
    assert_eq!(generated.bundles.len(), 1);
    assert_eq!(generated.bundles[0].extension, "core");
    assert!(!generated.extension_symbols.is_empty());

    // The encoded artifact must decode with the runtime catalog reader.
    let encoded = encode(&generated, EmptyListPolicy::Committed);
    let generated = Catalog::decode(&encoded).unwrap();

    let v80 = generated.materialize(ProjectVersion::new(8, 0, 0), "phpstub://8.0/core");
    assert!(find(&v80, SymbolKind::Function, "added").is_empty());
    assert!(!find(&v80, SymbolKind::Function, "removed").is_empty());
    assert_eq!(
        find(&v80, SymbolKind::Function, "aware")[0]
            .return_type
            .to_string(),
        "false|string"
    );
    assert!(
        !find(&v80, SymbolKind::Function, "laterDeprecated")[0]
            .flags
            .has(Flags::DEPRECATED)
    );
    assert!(
        find(&v80, SymbolKind::Function, "documentedDeprecated")[0]
            .flags
            .has(Flags::DEPRECATED)
    );
    assert_eq!(
        find(&v80, SymbolKind::Function, "shaped")[0]
            .return_type
            .to_string(),
        "array{id:int}"
    );
    let expected = find(&v80, SymbolKind::Function, "expected")[0];
    assert_eq!(expected.parameters[0].attributes.len(), 1);
    assert_eq!(expected.parameters[0].attributes[0].arguments.len(), 1);
    assert_eq!(
        expected.parameters[0].attributes[0].arguments[0]
            .value
            .items[0]
            .value
            .value,
        "auto"
    );
    assert!(find(&v80, SymbolKind::Interface, "Feature").is_empty());

    let v81 = generated.materialize(ProjectVersion::new(8, 1, 0), "phpstub://8.1/core");
    assert!(!find(&v81, SymbolKind::Function, "added").is_empty());
    assert!(!find(&v81, SymbolKind::Function, "removed").is_empty());
    let methods = find(&v81, SymbolKind::Method, "Feature::value");
    assert_eq!(methods.len(), 1);
    assert!(methods[0].parameters.is_empty());

    let v82 = generated.materialize(ProjectVersion::new(8, 2, 0), "phpstub://8.2/core");
    assert!(find(&v82, SymbolKind::Function, "removed").is_empty());
    assert_eq!(
        find(&v82, SymbolKind::Function, "aware")[0]
            .return_type
            .to_string(),
        "string"
    );
    assert!(
        find(&v82, SymbolKind::Function, "laterDeprecated")[0]
            .flags
            .has(Flags::DEPRECATED)
    );
    let methods = find(&v82, SymbolKind::Method, "Feature::value");
    assert_eq!(methods.len(), 1);
    assert_eq!(methods[0].parameters.len(), 1);
    assert_eq!(methods[0].parameters[0].ty.to_string(), "int");

    let (repeated, _) = build(root.path(), &lock, EmptyListPolicy::Committed).unwrap();
    assert_eq!(encoded, encode(&repeated, EmptyListPolicy::Committed));
}

#[test]
fn empty_list_policies_differ_only_in_hierarchy_and_throws() {
    let (root, lock) = fixture();
    let (committed, _) = build(root.path(), &lock, EmptyListPolicy::Committed).unwrap();
    let (head, _) = build(root.path(), &lock, EmptyListPolicy::GoHead).unwrap();
    let committed = encode(&committed, EmptyListPolicy::Committed);
    let head = encode(&head, EmptyListPolicy::GoHead);
    assert_ne!(committed, head);
    // Both decode to the same catalog content.
    let left = Catalog::decode(&committed).unwrap();
    let right = Catalog::decode(&head).unwrap();
    let version = ProjectVersion::new(8, 2, 0);
    assert_eq!(
        left.materialize(version, "phpstub://core"),
        right.materialize(version, "phpstub://core")
    );
}

#[test]
fn attribute_argument_parser_handles_nested_values() {
    let arguments = attribute_arguments(
        "#[LanguageLevelTypeAware(['8.0' => 'array<int, string>'], default: 'array|false')]",
        "LanguageLevelTypeAware",
    )
    .unwrap();
    assert!(arguments.contains("array<int, string>"));
    assert_eq!(attribute_arguments("Deprecated", "Deprecated"), None);
    assert_eq!(
        attribute_arguments("#[Deprecated ('a)', 1)]", "Deprecated"),
        Some("'a)', 1")
    );
}

#[test]
fn lock_validation() {
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join("lock.json");
    std::fs::write(
        &path,
        r#"{"repository":"r","commit":"short","versions":["8.0"],"directories":["Core"]}"#,
    )
    .unwrap();
    assert!(
        load_lock(&path)
            .unwrap_err()
            .to_string()
            .contains("incomplete lock file")
    );
    let lock = load_lock(Path::new(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../php-stubs/data/phpstorm-stubs.lock.json"
    )))
    .unwrap();
    assert_eq!(lock.commit.len(), 40);
}

#[test]
fn rejects_non_increasing_versions() {
    let (root, mut lock) = fixture();
    lock.versions = vec!["8.1".to_owned(), "8.0".to_owned()];
    assert!(
        build(root.path(), &lock, EmptyListPolicy::Committed)
            .unwrap_err()
            .to_string()
            .contains("strictly increasing")
    );
}

#[test]
fn version_helpers_match_go() {
    assert_eq!(parse_version("8.1").unwrap(), ProjectVersion::new(8, 1, 0));
    assert_eq!(
        parse_version("8.1.2").unwrap(),
        ProjectVersion::new(8, 1, 2)
    );
    assert!(parse_version("8").is_err());
    assert!(parse_version("8.-1").is_err());
    assert_eq!(namespace_of("Dom\\Node::append"), "Dom");
    assert_eq!(namespace_of("strlen"), "");
    assert_eq!(stub_extension("standard/basic.php"), "core");
    assert_eq!(stub_extension("SQLite/sqlite.php"), "pdo_sqlite");
    assert_eq!(stub_extension("mbstring/mbstring.php"), "mbstring");
    assert_eq!(
        leading_doc_comment("/** @since 8.1 */\nfunction f() {}", 18),
        "/** @since 8.1 */"
    );
}

/// Regenerates the committed artifact from a locked phpstorm-stubs checkout
/// named by `PHPSTORM_STUBS_SOURCE` and compares the bytes.
#[test]
#[ignore = "needs a phpstorm-stubs checkout in PHPSTORM_STUBS_SOURCE"]
fn regenerates_committed_artifact() {
    let source = std::env::var("PHPSTORM_STUBS_SOURCE").expect("PHPSTORM_STUBS_SOURCE");
    let repository = Path::new(env!("CARGO_MANIFEST_DIR")).join("../..");
    let lock =
        load_lock(&repository.join("crates/php-stubs/data/phpstorm-stubs.lock.json")).unwrap();
    verify_source(Path::new(&source), &lock).unwrap();
    let (generated, _) = build(Path::new(&source), &lock, EmptyListPolicy::Committed).unwrap();
    let committed =
        std::fs::read(repository.join("crates/php-stubs/data/phpstorm-stubs.msgpack")).unwrap();
    assert!(encode(&generated, EmptyListPolicy::Committed) == committed);
}
