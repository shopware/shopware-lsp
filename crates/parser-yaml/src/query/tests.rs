use serde::Deserialize;

use super::*;
use crate::parse;

fn root_of(source: &str) -> SyntaxNode {
    let parse = parse(source);
    assert!(parse.errors.is_empty(), "{}", parse.debug_dump());
    root_value(&parse.root()).expect("root value")
}

#[test]
fn mapping_sequence_and_path_queries() {
    let root = root_of(
        "services:\n  App\\Service\\Example:\n    class: App\\Service\\Example\n    tags:\n      - name: kernel.event_subscriber\n      - { name: doctrine.listener }\n",
    );
    let services = property(&root, "services").unwrap();
    let service = property(&services, "App\\Service\\Example").unwrap();
    assert!(is_mapping(&service));
    assert_eq!(
        scalar_value(&property(&service, "class").unwrap()),
        "App\\Service\\Example"
    );

    let tags = property(&service, "tags").unwrap();
    let tag_items: Vec<_> = items(&tags).collect();
    assert_eq!(tag_items.len(), 2);
    let first = item_value(&tag_items[0]).unwrap();
    assert_eq!(
        scalar_value(&property(&first, "name").unwrap()),
        "kernel.event_subscriber"
    );
    let second = item_value(&tag_items[1]).unwrap();
    assert_eq!(second.kind(), YAML_FLOW_MAPPING);
    assert_eq!(
        scalar_value(&property(&second, "name").unwrap()),
        "doctrine.listener"
    );

    let class_pair = property_pair(&service, "class").unwrap();
    let class_value = pair_value(&class_pair).unwrap();
    assert_eq!(
        pair_path(&class_value),
        ["services", "App\\Service\\Example", "class"]
    );
    assert_eq!(ancestor_pair(&class_value), Some(class_pair.clone()));
    assert!(contains(&services, &class_value));
    assert!(!contains(&class_value, &services));
    assert_eq!(raw_text(&class_pair), "class: App\\Service\\Example");
    assert_eq!(nodes(&root, &[YAML_SEQUENCE_ITEM]).len(), 2);
    assert_eq!(pairs(&tags).count(), 0);
    assert_eq!(items(&service).count(), 0);
}

#[test]
fn decorated_collection_query() {
    let root =
        root_of("defaults: &defaults\n  enabled: true\nitems:\n  - !item\n    name: first\n");
    let defaults = property(&root, "defaults").unwrap();
    assert!(is_mapping(&defaults));
    assert_eq!(
        scalar_value(&property(&defaults, "enabled").unwrap()),
        "true"
    );
    let items: Vec<_> = items(&property(&root, "items").unwrap()).collect();
    let item = item_value(&items[0]).unwrap();
    assert_eq!(scalar_value(&property(&item, "name").unwrap()), "first");
}

#[test]
fn null_query_distinguishes_quoted_null() {
    let root = root_of("empty: ~\nword: null\nupper: NULL\nquoted: 'null'\nmissing:\n");
    for name in ["empty", "word", "upper", "missing"] {
        assert!(is_null(&property(&root, name).unwrap()), "{name}");
    }
    assert!(!is_null(&property(&root, "quoted").unwrap()));
}

#[test]
fn scalar_value_unquotes() {
    let root = root_of("a: 'x''y'\nb: \"c\\nd\"\nc: |\n  text\nd:   plain  \n");
    assert_eq!(scalar_value(&property(&root, "a").unwrap()), "x''y");
    assert_eq!(scalar_value(&property(&root, "b").unwrap()), "c\\nd");
    assert_eq!(scalar_value(&property(&root, "c").unwrap()), "|\n  text\n");
    assert_eq!(scalar_value(&property(&root, "d").unwrap()), "plain");
    let parse = parse("a: 'open\n");
    let root = root_value(&parse.root()).unwrap();
    assert_eq!(scalar_value(&property(&root, "a").unwrap()), "open\n");
}

fn mapping(entries: &[(&str, Value)]) -> Value {
    Value::Mapping(
        entries
            .iter()
            .map(|(key, value)| (Value::String((*key).to_owned()), value.clone()))
            .collect(),
    )
}

fn string(text: &str) -> Value {
    Value::String(text.to_owned())
}

#[test]
fn decode_native_cst() {
    let cases: Vec<(&str, Value)> = vec![
        (
            "value: 'it''s quoted'\n",
            mapping(&[("value", string("it's quoted"))]),
        ),
        (
            r#"value: "escaped\nline\tä""#,
            mapping(&[("value", string("escaped\nline\tä"))]),
        ),
        (
            "value: |\n  First line\n  Second line\n",
            mapping(&[("value", string("First line\nSecond line\n"))]),
        ),
        (
            "value: |-\n  First line\n  Second line\n",
            mapping(&[("value", string("First line\nSecond line"))]),
        ),
        (
            "value: >\n  First line\n  Second line\n",
            mapping(&[("value", string("First line Second line\n"))]),
        ),
        (
            "value: >\n  First paragraph\n\n  Second paragraph\n",
            mapping(&[("value", string("First paragraph\nSecond paragraph\n"))]),
        ),
        (
            "defaults: &defaults\n  enabled: true\nvalue:\n  <<: *defaults\n  name: example\n",
            mapping(&[
                ("defaults", mapping(&[("enabled", Value::Bool(true))])),
                (
                    "value",
                    mapping(&[("name", string("example")), ("enabled", Value::Bool(true))]),
                ),
            ]),
        ),
        (
            "value: [1, true, null, 'true', {key: value}]",
            mapping(&[(
                "value",
                Value::Sequence(vec![
                    Value::Int(1),
                    Value::Bool(true),
                    Value::Null,
                    string("true"),
                    mapping(&[("key", string("value"))]),
                ]),
            )]),
        ),
        (
            "flag: &enabled true\nvalue: *enabled\n",
            mapping(&[("flag", Value::Bool(true)), ("value", Value::Bool(true))]),
        ),
        ("value: !!str 123\n", mapping(&[("value", string("123"))])),
        (
            "value: |2\n    indented\n  normal\n",
            mapping(&[("value", string("  indented\nnormal\n"))]),
        ),
        (
            "value: |+\n  line\n\n",
            mapping(&[("value", string("line\n\n"))]),
        ),
        (
            "a: 0x1F\nb: 0o17\nc: 1_000\nd: 1.5e3\ne: .inf\nf: -12\ng: 18446744073709551615\nh: 1.2.3\ni: 010\n",
            mapping(&[
                ("a", Value::Int(31)),
                ("b", Value::Int(15)),
                ("c", Value::Int(1000)),
                ("d", Value::Float(1500.0)),
                ("e", Value::Float(f64::INFINITY)),
                ("f", Value::Int(-12)),
                ("g", Value::Uint(u64::MAX)),
                ("h", string("1.2.3")),
                ("i", Value::Int(8)),
            ]),
        ),
    ];
    for (source, expected) in cases {
        let parse = parse(source);
        assert_eq!(decode_value(&parse.root()).unwrap(), expected, "{source:?}");
    }
}

#[test]
fn decode_into_typed_target() {
    #[derive(Debug, Default, Deserialize, PartialEq)]
    #[serde(default)]
    struct Build {
        enabled: bool,
        name: String,
        paths: Vec<String>,
        locale: Option<String>,
    }
    #[derive(Debug, Default, Deserialize, PartialEq)]
    #[serde(default)]
    struct Config {
        build: Build,
        version: u32,
    }

    let source = parse("build:\n  enabled: yes\n  name: 42\n  paths: [a, 'b']\nversion: 3\n");
    let config: Config = decode(&source.root()).unwrap();
    assert_eq!(
        config,
        Config {
            build: Build {
                enabled: true,
                name: "42".into(),
                paths: vec!["a".into(), "b".into()],
                locale: None
            },
            version: 3,
        }
    );

    let invalid = parse("version: [1]\n");
    assert!(decode::<Config>(&invalid.root()).is_err());
    assert_eq!(decode_value(&parse("").root()).unwrap(), Value::Null);
}
