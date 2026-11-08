use swlsp_indexer::{Indexer, ParsedFile};
use swlsp_php_project::Package;

use super::*;

fn slice(source: &str, range: TextRange) -> &str {
    &source[usize::from(range.start())..usize::from(range.end())]
}

#[test]
fn yaml_container_constant_references_cases() {
    let source = "parameters:
  modern: !php/const App\\Mode::ACTIVE
  legacy: !php/const:App\\Mode::LEGACY
  quoted_name: !php/const 'App\\Mode::QUOTED'
  global: !php/const APP_LIMIT
\tcase: !php/enum App\\State::ACTIVE
\tcase_value: !php/enum App\\State::ACTIVE->value
\tquoted_case_value: !php/enum 'App\\State::ACTIVE->value'
\tall_cases: !php/enum App\\State
  string: \"!php/const:App\\Mode::IGNORED\"
\tenum_string: \"!php/enum App\\State::IGNORED\"
  comment: value # !php/const App\\Mode::IGNORED
";
    let references = yaml_container_constant_references(source);
    let names: Vec<&str> = references
        .iter()
        .map(|reference| reference.name.as_str())
        .collect();
    assert_eq!(
        names,
        vec![
            "App\\Mode::ACTIVE",
            "App\\Mode::LEGACY",
            "App\\Mode::QUOTED",
            "APP_LIMIT",
            "App\\State::ACTIVE",
            "App\\State::ACTIVE",
            "App\\State::ACTIVE",
            "App\\State",
        ]
    );
    for reference in &references {
        assert_eq!(slice(source, reference.range), reference.name);
    }
    assert_eq!(references[3].kind, ContainerValueKind::Constant);
    for reference in &references[4..] {
        assert_eq!(reference.kind, ContainerValueKind::Enum);
    }
}

#[test]
fn yaml_container_constant_completion_at_trims_probe() {
    let source = "parameters:\n  mode: !php/const App\\Mo";
    let reference = yaml_container_constant_completion_at(source, source.len() as u32).unwrap();
    assert_eq!(reference.name, "App\\Mo");
    assert_eq!(slice(source, reference.range), "App\\Mo");
}

#[test]
fn yaml_container_enum_version_support() {
    let model = |version: &str| Model {
        dependencies: vec![Package {
            name: "symfony/yaml".to_owned(),
            version: version.to_owned(),
            ..Package::default()
        }],
        ..Model::default()
    };
    assert!(supports_yaml_container_enum(None));
    assert!(!supports_yaml_container_enum(Some(&model("v6.1.12"))));
    assert!(supports_yaml_container_enum(Some(&model("v6.2.0"))));
    assert!(!supports_yaml_container_enum_class(Some(&model("v7.0.9"))));
    assert!(supports_yaml_container_enum_class(Some(&model("v7.1.0"))));
}

fn php_index(source: &str) -> (tempfile::TempDir, PhpIndex) {
    let cache = tempfile::tempdir().unwrap();
    let index = PhpIndex::open(cache.path(), None).unwrap();
    index
        .index(&ParsedFile::new(
            "/project/src/Mode.php",
            source.as_bytes().to_vec(),
        ))
        .unwrap();
    (cache, index)
}

fn reference(name: &str) -> ContainerConstantReference {
    ContainerConstantReference {
        name: name.to_owned(),
        kind: ContainerValueKind::Enum,
        ..ContainerConstantReference::default()
    }
}

#[test]
fn resolve_container_enum() {
    let (_cache, index) = php_index(
        "<?php\nnamespace App;\nenum Mode {\n    public const ALIAS = self::ACTIVE;\n    case ACTIVE;\n}\nclass NotAnEnum { public const ACTIVE = 'active'; }\n",
    );
    assert_eq!(
        resolve_container_value(Some(&index), &reference("App\\Mode::ACTIVE"))[0].kind,
        SymbolKind::EnumCase
    );
    assert_eq!(
        resolve_container_value(Some(&index), &reference("App\\Mode"))[0].kind,
        SymbolKind::Enum
    );
    assert!(resolve_container_value(Some(&index), &reference("App\\Mode::ALIAS")).is_empty());
    assert!(resolve_container_value(Some(&index), &reference("App\\NotAnEnum")).is_empty());
    index.close().unwrap();
}

#[test]
fn xml_container_constant_references_cases() {
    let source = "<container><services><service id=\"app\">
<argument type=\"constant\">
  App\\Mode::ACTIVE
</argument>
<argument type=\"string\">App\\Mode::IGNORED</argument>
</service></services></container>";
    let parse = swlsp_parser_xml::parse(source);
    let references = xml_container_constant_references(&parse.root());
    assert_eq!(references.len(), 1);
    assert_eq!(references[0].name, "App\\Mode::ACTIVE");
    assert_eq!(slice(source, references[0].range), references[0].name);
}

#[test]
fn resolve_container_constant_cases() {
    let (_cache, index) = php_index(
        "<?php\nnamespace App;\nconst APP_LIMIT = 10;\nclass BaseMode { public const INHERITED = 1; }\nenum Mode {\n    public const ACTIVE = 'active';\n    case LEGACY;\n}\nclass ChildMode extends BaseMode {}\n",
    );
    let index = Some(&index);
    assert_eq!(
        resolve_container_constant(index, "App\\APP_LIMIT")[0].kind,
        SymbolKind::GlobalConstant
    );
    assert_eq!(
        resolve_container_constant(index, "App\\Mode::ACTIVE")[0].kind,
        SymbolKind::ClassConstant
    );
    assert_eq!(
        resolve_container_constant(index, "App\\Mode::LEGACY")[0].kind,
        SymbolKind::EnumCase
    );
    assert_eq!(
        resolve_container_constant(index, "App\\ChildMode::INHERITED")[0]
            .name
            .as_str(),
        "INHERITED"
    );
    assert!(resolve_container_constant(index, "App\\Mode::MISSING").is_empty());
}
