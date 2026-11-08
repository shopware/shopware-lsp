//! Shopware system-config XML fields (port of Go `internal/systemconfig`).

mod indexer;
mod namespace;

pub use indexer::SystemConfigIndexer;
pub use namespace::{
    SystemConfigEntry, index_system_config_file, index_system_config_tree, namespace_from_path,
};

use swlsp_parser_xml::query;
use swlsp_parser_xml::syntax::XML_DOCUMENT;
use swlsp_syntax::{LineIndex, SyntaxNode};

/// The schema marker identifying a system-config XML document.
pub const SYSTEM_CONFIG_SCHEMA: &str = "SystemConfig/Schema/config.xsd";

/// One `input-field` or `component` of a system-config document.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SystemConfigField {
    pub name: String,
    pub label: String,
    pub r#type: String,
    pub component: String,
    pub file_path: String,
    /// 1-based line of the element.
    pub line: u32,
}

pub fn is_system_config_xml(content: &str) -> bool {
    content.contains(SYSTEM_CONFIG_SCHEMA)
}

/// The field's `<name>` (or legacy `<n>`) text.
pub fn system_config_field_name(node: &SyntaxNode) -> String {
    field_element(node)
        .and_then(|element| query::child_element(&element, &["n", "name"]))
        .map(|name| query::text_content(&name).trim().to_owned())
        .unwrap_or_default()
}

/// The untranslated (default) label: the first `<label>` without `lang`.
pub fn system_config_field_label(node: &SyntaxNode) -> String {
    let Some(element) = field_element(node) else {
        return String::new();
    };
    query::child_elements(&element, &["label"])
        .into_iter()
        .find(|label| query::attribute(label, "lang").is_none())
        .map(|label| query::text_content(&label).trim().to_owned())
        .unwrap_or_default()
}

/// The `type` attribute of an `input-field`.
pub fn system_config_field_type(node: &SyntaxNode) -> String {
    element_attribute(node, "input-field", "type")
}

/// The `name` attribute of a `component`.
pub fn system_config_component(node: &SyntaxNode) -> String {
    element_attribute(node, "component", "name")
}

fn element_attribute(node: &SyntaxNode, element_name: &str, attribute: &str) -> String {
    field_element(node)
        .filter(|element| query::element_name(element).as_deref() == Some(element_name))
        .and_then(|element| query::attribute(&element, attribute))
        .and_then(|attribute| query::attribute_value(&attribute))
        .unwrap_or_default()
}

/// Parses one field element (or the element containing `node`).
pub fn parse_system_config_field(
    node: &SyntaxNode,
    line_index: &LineIndex,
    file_path: &str,
) -> SystemConfigField {
    let mut field = SystemConfigField {
        file_path: file_path.to_owned(),
        ..SystemConfigField::default()
    };
    let Some(element) = field_element(node) else {
        return field;
    };
    let (line, _) = line_index.position(element.text_range().start().into());
    field.line = line + 1;
    field.name = system_config_field_name(&element);
    field.label = system_config_field_label(&element);
    match query::element_name(&element).as_deref() {
        Some("input-field") => field.r#type = system_config_field_type(&element),
        Some("component") => field.component = system_config_component(&element),
        _ => {}
    }
    field
}

/// Every named `input-field` and `component` element, in source order.
pub fn find_all_system_config_fields(
    root: &SyntaxNode,
    line_index: &LineIndex,
    file_path: &str,
) -> Vec<SystemConfigField> {
    query::elements(root, &["input-field", "component"])
        .iter()
        .map(|node| parse_system_config_field(node, line_index, file_path))
        .filter(|field| !field.name.is_empty())
        .collect()
}

fn field_element(node: &SyntaxNode) -> Option<SyntaxNode> {
    if let Some(element) = query::element_at(node) {
        return Some(element);
    }
    if node.kind() == XML_DOCUMENT {
        return query::elements(node, &[]).into_iter().next();
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    fn parse(content: &str) -> (SyntaxNode, LineIndex) {
        let parse = swlsp_parser_xml::parse(content);
        (parse.tree.root(), LineIndex::new(content))
    }

    #[test]
    fn detects_system_config_xml() {
        assert!(is_system_config_xml(
            r#"<?xml version="1.0" encoding="UTF-8" ?><config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/master/src/Core/System/SystemConfig/Schema/config.xsd"></config>"#
        ));
        assert!(!is_system_config_xml(
            r#"<?xml version="1.0" encoding="UTF-8" ?><config></config>"#
        ));
    }

    #[test]
    fn element_query_counts_fields() {
        let cases = [
            (
                "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<config>\n  <card>\n    <input-field type=\"text\">\n      <n>fieldName</n>\n      <label>Field Label</label>\n    </input-field>\n    <component name=\"custom-component\">\n      <n>componentName</n>\n      <label>Component Label</label>\n    </component>\n  </card>\n</config>",
                2,
            ),
            (
                "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<config>\n  <card>\n    <input-field type=\"text\">\n      <n>fieldName</n>\n      <label>Field Label</label>\n    </input-field>\n  </card>\n</config>",
                1,
            ),
            (
                "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<config>\n  <card>\n    <title>Test Card</title>\n  </card>\n</config>",
                0,
            ),
        ];
        for (content, expected) in cases {
            let (root, _) = parse(content);
            assert_eq!(
                query::elements(&root, &["input-field", "component"]).len(),
                expected
            );
        }
    }

    const INPUT: &str = "<input-field type=\"text\">\n  <n>fieldName</n>\n  <label>Field Label</label>\n</input-field>";
    const COMPONENT: &str = "<component name=\"custom-component\">\n  <n>componentName</n>\n  <label>Component Label</label>\n</component>";

    #[test]
    fn field_accessors() {
        let (root, _) = parse(INPUT);
        assert_eq!(system_config_field_name(&root), "fieldName");
        assert_eq!(system_config_field_label(&root), "Field Label");
        assert_eq!(system_config_field_type(&root), "text");
        let (root, _) = parse(COMPONENT);
        assert_eq!(system_config_component(&root), "custom-component");
        let (root, _) = parse(
            "<input-field type=\"text\">\n  <n>fieldName</n>\n  <label lang=\"de-DE\">Deutsches Label</label>\n  <label>English Label</label>\n</input-field>",
        );
        assert_eq!(system_config_field_label(&root), "English Label");
    }

    #[test]
    fn parses_fields() {
        let (root, index) = parse(INPUT);
        assert_eq!(
            parse_system_config_field(&root, &index, "test-file.xml"),
            SystemConfigField {
                name: "fieldName".into(),
                label: "Field Label".into(),
                r#type: "text".into(),
                file_path: "test-file.xml".into(),
                line: 1,
                ..SystemConfigField::default()
            }
        );
        let (root, index) = parse(COMPONENT);
        assert_eq!(
            parse_system_config_field(&root, &index, "test-file.xml"),
            SystemConfigField {
                name: "componentName".into(),
                label: "Component Label".into(),
                component: "custom-component".into(),
                file_path: "test-file.xml".into(),
                line: 1,
                ..SystemConfigField::default()
            }
        );
    }

    #[test]
    fn finds_all_fields() {
        let content = "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<config xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xsi:noNamespaceSchemaLocation=\"https://raw.githubusercontent.com/shopware/shopware/master/src/Core/System/SystemConfig/Schema/config.xsd\">\n  <card>\n    <input-field type=\"text\">\n      <n>textField</n>\n      <label>Text Field</label>\n    </input-field>\n    <input-field type=\"bool\">\n      <n>boolField</n>\n      <label>Bool Field</label>\n    </input-field>\n    <component name=\"custom-component\">\n      <n>customComponent</n>\n      <label>Custom Component</label>\n    </component>\n  </card>\n</config>";
        let (root, index) = parse(content);
        let fields = find_all_system_config_fields(&root, &index, "/tmp/test-config.xml");
        let summary: Vec<(&str, &str, &str, &str, u32)> = fields
            .iter()
            .map(|field| {
                (
                    field.name.as_str(),
                    field.label.as_str(),
                    field.r#type.as_str(),
                    field.component.as_str(),
                    field.line,
                )
            })
            .collect();
        assert_eq!(
            summary,
            [
                ("textField", "Text Field", "text", "", 4),
                ("boolField", "Bool Field", "bool", "", 8),
                (
                    "customComponent",
                    "Custom Component",
                    "",
                    "custom-component",
                    12
                )
            ]
        );
    }

    #[test]
    fn real_system_config_file() {
        let path = concat!(env!("CARGO_MANIFEST_DIR"), "/testdata/common.xml");
        let content = std::fs::read_to_string(path).unwrap();
        assert!(is_system_config_xml(&content));
        let (root, index) = parse(&content);
        let fields = find_all_system_config_fields(&root, &index, "testdata/common.xml");
        assert!(!fields.is_empty());
        let cash = fields
            .iter()
            .find(|field| field.name == "cashOnDeliveryPaymentMethodIds")
            .unwrap();
        assert_eq!(cash.label, "Cash on delivery payment methods:");
        assert_eq!(
            cash.component,
            "pw-shipping-entity-multi-select-by-id-field"
        );
        let first_name = fields
            .iter()
            .find(|field| field.name == "senderAddressFirstName")
            .unwrap();
        assert_eq!(first_name.label, "First name");
        assert_eq!(first_name.r#type, "text");
    }
}
