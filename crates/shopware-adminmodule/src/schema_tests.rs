//! The schema against the format the crate reads, and what it suggests.

use std::collections::BTreeSet;

use serde_json::{Map, Value, json};

use super::{Names, SCHEMA, Schema};
use crate::config::{ConfigError, parse};

/// A key the format does not know.
const UNKNOWN: &str = "zz";

fn schema() -> Value {
    serde_json::from_str(SCHEMA).unwrap()
}

fn target<'a>(root: &'a Value, node: &'a Value) -> &'a Value {
    match node.get("$ref").and_then(Value::as_str) {
        Some(reference) => root.pointer(reference.strip_prefix('#').unwrap()).unwrap(),
        None => node,
    }
}

/// A value the schema `node` accepts.
fn sample(root: &Value, node: &Value) -> Value {
    let node = target(root, node);
    if let Some(value) = node.get("const") {
        return value.clone();
    }
    if let Some(values) = node.get("enum").and_then(Value::as_array) {
        return values[0].clone();
    }
    if let Some(tag) = node.pointer("/oneOf/0/properties/type/const") {
        return json!({ "type": tag });
    }
    match node.get("type") {
        Some(Value::String(name)) if name == "boolean" => json!(true),
        Some(Value::String(name)) if name == "integer" => json!(1),
        Some(Value::String(name)) if name == "string" => json!("text"),
        _ => json!({}),
    }
}

/// `value` as block YAML, its lines indented by `depth` steps.
fn block(value: &Value, depth: usize, out: &mut String) {
    let Some(mapping) = value.as_object().filter(|mapping| !mapping.is_empty()) else {
        out.push_str(&format!(" {value}\n"));
        return;
    };
    out.push('\n');
    for (key, inner) in mapping {
        out.push_str(&format!("{}{key}:", "  ".repeat(depth)));
        block(inner, depth + 1, out);
    }
}

/// A mapping key on the way to a place, with what the mapping that holds
/// it needs beside it.
type Step = (String, Map<String, Value>);

/// `value` below the mapping keys of `path`, as a description.
fn document(path: &[Step], value: Value) -> String {
    let nested = path.iter().rev().fold(value, |inner, (key, beside)| {
        let mut mapping = beside.clone();
        mapping.insert(key.clone(), inner);
        Value::Object(mapping)
    });
    let mut out = String::new();
    block(&nested, 0, &mut out);
    out
}

/// The fields the format expects where it met [`UNKNOWN`].
fn expected(error: &ConfigError) -> BTreeSet<String> {
    let ConfigError::Decode(message) = error else {
        panic!("{error}");
    };
    assert!(message.contains(UNKNOWN), "{message}");
    let list = message.split("expected").nth(1).unwrap_or_default();
    list.split('`')
        .skip(1)
        .step_by(2)
        .map(str::to_owned)
        .collect()
}

/// Checks the mapping `object` at `path`: every property is read, and the
/// format knows no other. `tag` is what selects `object` among alternatives.
fn check(root: &Value, object: &Value, path: &[Step], tag: Option<&Value>, seen: &mut usize) {
    let object = target(root, object);
    if let Some(alternatives) = object.get("oneOf").and_then(Value::as_array) {
        for alternative in alternatives {
            let tag = alternative.pointer("/properties/type/const").unwrap();
            check(root, alternative, path, Some(tag), seen);
        }
        return;
    }
    // What the mapping needs whatever else it holds.
    let mut beside = Map::new();
    if let Some(tag) = tag {
        beside.insert("type".to_owned(), tag.clone());
    }
    let required = object.get("required").and_then(Value::as_array);
    for name in required.into_iter().flatten() {
        let name = name.as_str().unwrap();
        beside.insert(name.to_owned(), sample(root, &object["properties"][name]));
    }
    let with = |key: &str, value: Value| {
        let mut mapping = beside.clone();
        mapping.insert(key.to_owned(), value);
        document(path, Value::Object(mapping))
    };
    let below = |key: &str| {
        let mut below = path.to_vec();
        below.push((key.to_owned(), beside.clone()));
        below
    };
    if let Some(properties) = object.get("properties").and_then(Value::as_object) {
        *seen += 1;
        for (name, child) in properties {
            let source = with(name, sample(root, child));
            assert!(parse(&source).is_ok(), "{source}: {:?}", parse(&source));
            check(root, child, &below(name), None, seen);
        }
        if object.get("additionalProperties") == Some(&json!(false)) {
            let source = with(UNKNOWN, json!(1));
            let known: BTreeSet<String> = properties
                .keys()
                .filter(|name| !(tag.is_some() && *name == "type"))
                .cloned()
                .collect();
            let error = parse(&source).expect_err(&source);
            if !known.is_empty() {
                assert_eq!(expected(&error), known, "{source}");
            }
        }
    }
    let named = object.get("additionalProperties");
    if let Some(child) = named.filter(|node| node.is_object()) {
        check(root, child, &below("acme-name"), None, seen);
    }
}

#[test]
fn the_schema_describes_what_the_format_reads() {
    let root = schema();
    let mut seen = 0;
    check(&root, &root, &[], None, &mut seen);
    // Every mapping of the format was reached.
    assert!(seen > 20, "{seen}");
}

fn path(keys: &[&str]) -> Vec<String> {
    keys.iter().map(|key| (*key).to_owned()).collect()
}

fn labels(suggestions: &super::Suggestions) -> Vec<&str> {
    suggestions
        .items
        .iter()
        .map(|item| item.label.as_str())
        .collect()
}

#[test]
fn keys_and_values_come_from_the_schema() {
    let schema = Schema::bundled();
    let nothing = |_: &[String]| None;
    assert_eq!(labels(&schema.keys(&[], &nothing)), ["modules", "version"]);
    let module = schema.keys(&path(&["modules", "acme-review"]), &nothing);
    assert!(labels(&module).contains(&"labelField"));
    let listing = module.items.iter().find(|item| item.label == "listing");
    assert_eq!(listing.map(|item| item.nested), Some(true));
    assert_eq!(
        listing.map(|item| item.description.as_str()),
        Some("The list page.")
    );

    let column = ["modules", "acme-review", "listing", "columns"];
    assert_eq!(
        schema.keys(&path(&column), &nothing).names,
        Some(Names::Fields)
    );
    let align = [&column[..], &["title", "align"]].concat();
    assert_eq!(
        labels(&schema.values(&path(&align), &nothing)),
        ["left", "center", "right"]
    );
    let enable = [&column[..], &["title", "enable"]].concat();
    assert_eq!(
        labels(&schema.values(&path(&enable), &nothing)),
        ["true", "false"]
    );
    let entity = schema.values(&path(&["modules", "acme-review", "entity"]), &nothing);
    assert_eq!(entity.names, Some(Names::Entities));
    let title = schema.keys(&path(&["modules", "acme-review", "title"]), &nothing);
    assert_eq!(title.names, Some(Names::Locales));
}

#[test]
fn a_component_offers_the_keys_of_its_type() {
    let schema = Schema::bundled();
    let component = path(&[
        "modules",
        "acme-review",
        "detail",
        "cards",
        "general",
        "fields",
        "rating",
        "component",
    ]);
    let nothing = |_: &[String]| None;
    assert_eq!(labels(&schema.keys(&component, &nothing)), ["type"]);
    let number = |_: &[String]| Some("number".to_owned());
    assert_eq!(
        labels(&schema.keys(&component, &number)),
        ["numberType", "props", "type"]
    );
    let tag = [&component[..], &["type".to_owned()]].concat();
    let types = schema.values(&tag, &nothing);
    assert_eq!(types.items.len(), 10);
    assert_eq!(types.items[0].label, "text");
    assert_eq!(types.items[0].description, "A text field.");
    let number_type = [&component[..], &["numberType".to_owned()]].concat();
    assert_eq!(
        labels(&schema.values(&number_type, &number)),
        ["int", "float"]
    );
}
