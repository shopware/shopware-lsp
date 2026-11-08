use swlsp_parser_json::syntax::{JSON_OBJECT, JSON_STRING};
use swlsp_syntax::{LineIndex, SyntaxNodeExt, Tree};

use crate::json::{
    boolean_value, integer_value, pair_key, pair_value, pairs, property, root_value, string_value,
};
use crate::types::ThemeConfigField;

/// Parses the `config.fields` section of a `theme.json` document.
pub fn parse_theme_config(
    document: &[u8],
    file_path: &str,
) -> Result<Vec<ThemeConfigField>, String> {
    let parse = swlsp_parser_json::parse(&String::from_utf8_lossy(document));
    let lines = LineIndex::new(parse.tree.source_arc().clone());
    parse_theme_config_tree(&parse.tree, &lines, file_path)
}

/// Fields in source order; an error when the root is not an object.
pub fn parse_theme_config_tree(
    tree: &Tree,
    lines: &LineIndex,
    file_path: &str,
) -> Result<Vec<ThemeConfigField>, String> {
    let root = root_value(&tree.root())
        .filter(|root| root.kind() == JSON_OBJECT)
        .ok_or_else(|| "JSON root is not an object".to_owned())?;
    let Some(fields) = property(&root, "config")
        .filter(|config| config.kind() == JSON_OBJECT)
        .and_then(|config| property(&config, "fields"))
        .filter(|fields| fields.kind() == JSON_OBJECT)
    else {
        return Ok(Vec::new());
    };

    let mut result = Vec::new();
    for pair in pairs(&fields) {
        let (Some(key), Some(value)) = (pair_key(&pair), pair_value(&pair)) else {
            continue;
        };
        if value.kind() != JSON_OBJECT {
            continue;
        }
        let (line, _) = lines.position(key.range_trimmed_trivia().start().into());
        let mut field = ThemeConfigField {
            key: string_value(&key),
            scss: true,
            path: file_path.to_owned(),
            line: i64::from(line) + 1,
            ..ThemeConfigField::default()
        };
        if let Some(labels) =
            property(&value, "label").filter(|labels| labels.kind() == JSON_OBJECT)
        {
            for label in pairs(&labels) {
                if let (Some(locale), Some(text)) = (pair_key(&label), pair_value(&label))
                    && text.kind() == JSON_STRING
                {
                    field
                        .label
                        .insert(string_value(&locale), string_value(&text));
                }
            }
        }
        if let Some(property) = property(&value, "type") {
            field.kind = string_value(&property);
        }
        if let Some(property) = property(&value, "value") {
            field.value = string_value(&property);
        }
        if let Some(editable) = property(&value, "editable").and_then(|node| boolean_value(&node)) {
            field.editable = editable;
        }
        if let Some(scss) = property(&value, "scss").and_then(|node| boolean_value(&node)) {
            field.scss = scss;
        }
        if let Some(order) = property(&value, "order").and_then(|node| integer_value(&node)) {
            field.order = order;
        }
        if let Some(property) = property(&value, "block") {
            field.block = string_value(&property);
        }
        result.push(field);
    }
    Ok(result)
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use super::*;

    #[test]
    fn parse_theme_config_fields() {
        let bytes =
            std::fs::read(concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/theme.json")).unwrap();
        let file_path = "testdata/theme.json";
        let fields = parse_theme_config(&bytes, file_path).unwrap();
        assert!(!fields.is_empty());
        let by_key: HashMap<&str, &ThemeConfigField> = fields
            .iter()
            .map(|field| (field.key.as_str(), field))
            .collect();
        for key in [
            "sw-color-brand-primary",
            "sw-color-success",
            "sw-logo-desktop",
        ] {
            assert!(by_key.contains_key(key), "{key}");
        }
        let primary = by_key["sw-color-brand-primary"];
        assert_eq!(primary.label["en-GB"], "Primary colour");
        assert_eq!(primary.kind, "color");
        assert_eq!(primary.value, "#0042a0");
        assert!(primary.editable);
        assert_eq!(primary.block, "themeColors");
        assert_eq!(primary.order, 100);
        assert_eq!(primary.path, file_path);
        assert!(primary.line > 0);
        assert_eq!(fields.len(), 20);
    }

    #[test]
    fn non_object_root_is_an_error() {
        assert!(parse_theme_config(b"[]", "theme.json").is_err());
        assert!(parse_theme_config(b"", "theme.json").is_err());
        assert_eq!(
            parse_theme_config(b"{\"config\": []}", "theme.json").unwrap(),
            Vec::new()
        );
    }
}
