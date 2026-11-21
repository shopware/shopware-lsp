//! The snippet file of a module in one locale.

use std::collections::BTreeMap;

use serde_json::Value;

use super::Names;
use crate::resolve::{Input, MAIN_LOCALE};

/// The texts no description names, in the locales the generator knows.
fn built_in(locale: &str, key: &str) -> &'static str {
    let german = locale.starts_with("de");
    match (key, german) {
        ("buttonCreate", false) => "Add",
        ("buttonCreate", true) => "Hinzufügen",
        ("titleNew", false) => "New entry",
        ("titleNew", true) => "Neuer Eintrag",
        ("saveError", false) => "The entry could not be saved.",
        (_, true) => "Der Eintrag konnte nicht gespeichert werden.",
        _ => "",
    }
}

/// Sets `text` at `path` below `value`.
fn set(value: &mut Value, path: &[&str], text: &str) {
    let Some((key, rest)) = path.split_first() else {
        *value = Value::String(text.to_owned());
        return;
    };
    if !value.is_object() {
        *value = Value::Object(serde_json::Map::new());
    }
    if let Value::Object(map) = value {
        set(
            map.entry((*key).to_owned()).or_insert(Value::Null),
            rest,
            text,
        );
    }
}

fn sorted(value: &Value, indent: usize, out: &mut String) {
    let Value::Object(map) = value else {
        out.push_str(&value.to_string());
        return;
    };
    let mut keys: Vec<&String> = map.keys().collect();
    keys.sort();
    out.push_str("{\n");
    for (index, key) in keys.iter().enumerate() {
        out.push_str(&"    ".repeat(indent + 1));
        out.push_str(&Value::String((*key).clone()).to_string());
        out.push_str(": ");
        sorted(&map[*key], indent + 1, out);
        out.push_str(if index + 1 < keys.len() { ",\n" } else { "\n" });
    }
    out.push_str(&"    ".repeat(indent));
    out.push('}');
}

pub(super) fn snippets(names: &Names, locale: &str) -> String {
    let module = names.module;
    let name = module.name.as_str();
    let mut root = Value::Null;
    let pick = |labels: &BTreeMap<String, String>| {
        labels
            .get(locale)
            .or_else(|| labels.get(MAIN_LOCALE))
            .cloned()
            .unwrap_or_default()
    };
    set(&mut root, &[name, "general", "title"], &pick(&module.title));
    set(
        &mut root,
        &[name, "general", "description"],
        &pick(&module.description),
    );
    set(
        &mut root,
        &[name, "list", "buttonCreate"],
        built_in(locale, "buttonCreate"),
    );
    for column in &module.listing.columns {
        set(
            &mut root,
            &[name, "list", "columns", &column.property],
            &pick(&column.label),
        );
    }
    set(
        &mut root,
        &[name, "detail", "titleNew"],
        built_in(locale, "titleNew"),
    );
    set(
        &mut root,
        &[name, "detail", "notificationSaveError"],
        built_in(locale, "saveError"),
    );
    for card in &module.detail.cards {
        set(
            &mut root,
            &[name, "detail", "cards", &card.name, "title"],
            &pick(&card.title),
        );
        for field in &card.fields {
            let path = [name, "detail", "fields", field.name.as_str()];
            set(
                &mut root,
                &[&path[..], &["label"]].concat(),
                &pick(&field.label),
            );
            if !field.help_text.is_empty() {
                set(
                    &mut root,
                    &[&path[..], &["helpText"]].concat(),
                    &pick(&field.help_text),
                );
            }
            if !field.placeholder.is_empty() {
                set(
                    &mut root,
                    &[&path[..], &["placeholder"]].concat(),
                    &pick(&field.placeholder),
                );
            }
            if let Input::Select(choices) = &field.input {
                for choice in choices {
                    set(
                        &mut root,
                        &[&path[..], &["options", choice.value.as_str()]].concat(),
                        &pick(&choice.label),
                    );
                }
            }
        }
    }
    if module.acl.is_some() {
        set(
            &mut root,
            &["sw-privileges", "permissions", &module.entity, "label"],
            &pick(&module.title),
        );
    }
    let mut out = String::new();
    sorted(&root, 0, &mut out);
    out + "\n"
}
