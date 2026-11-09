//! Structural XML queries over parsed trees.
//!
//! Every function accepts any node inside a tree. Functions that operate on an
//! element normalize their argument first: a node inside an element resolves
//! to that element, and the document node resolves to its first element.

use std::collections::BTreeMap;

use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, SyntaxToken};

use crate::syntax::*;

/// All nodes (including `root`) whose kind is in `kinds`, in preorder.
pub fn nodes(root: &SyntaxNode, kinds: &[Kind]) -> Vec<SyntaxNode> {
    root.descendants()
        .filter(|node| kinds.contains(&node.kind()))
        .collect()
}

/// All elements (including `root`) in preorder. An empty `names` accepts
/// every element; otherwise only elements with one of the given names.
pub fn elements(root: &SyntaxNode, names: &[&str]) -> Vec<SyntaxNode> {
    root.descendants()
        .filter(|node| node.kind() == XML_ELEMENT && name_accepted(node, names))
        .collect()
}

/// The element containing `node` (or `node` itself when it is an element).
pub fn element_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, XML_ELEMENT)
}

/// The element enclosing the element at `node`.
pub fn parent_element(node: &SyntaxNode) -> Option<SyntaxNode> {
    element_at(node)?.ancestor_of_kind(XML_ELEMENT)
}

/// The name token of the (normalized) element's start tag.
pub fn element_name_token(node: &SyntaxNode) -> Option<SyntaxToken> {
    let element = normalize_element(node)?;
    start_tag(&element)?
        .child_node_of_kind(XML_NAME)?
        .child_token_of_kind(TK_NAME)
}

/// The (normalized) element's name, e.g. `x:item`.
pub fn element_name(node: &SyntaxNode) -> Option<String> {
    element_name_token(node).map(|token| token.text().to_owned())
}

/// Direct child elements of the (normalized) element. An empty `names`
/// accepts every element.
pub fn child_elements(node: &SyntaxNode, names: &[&str]) -> Vec<SyntaxNode> {
    let Some(content) =
        normalize_element(node).and_then(|element| element.child_node_of_kind(XML_CONTENT))
    else {
        return Vec::new();
    };
    content
        .children()
        .filter(|child| child.kind() == XML_ELEMENT && name_accepted(child, names))
        .collect()
}

/// The first direct child element matching `names`.
pub fn child_element(node: &SyntaxNode, names: &[&str]) -> Option<SyntaxNode> {
    let content = normalize_element(node)?.child_node_of_kind(XML_CONTENT)?;
    content
        .children()
        .find(|child| child.kind() == XML_ELEMENT && name_accepted(child, names))
}

/// Attribute nodes of the (normalized) element's start tag, in source order.
pub fn attributes(node: &SyntaxNode) -> Vec<SyntaxNode> {
    normalize_element(node)
        .and_then(|element| start_tag(&element))
        .map(|tag| {
            tag.children()
                .filter(|child| child.kind() == XML_ATTRIBUTE)
                .collect()
        })
        .unwrap_or_default()
}

/// The first attribute of the (normalized) element named `name`.
pub fn attribute(node: &SyntaxNode, name: &str) -> Option<SyntaxNode> {
    attributes(node)
        .into_iter()
        .find(|attribute| attribute_name_token(attribute).is_some_and(|token| token.text() == name))
}

/// The attribute containing `node` (or `node` itself).
pub fn attribute_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, XML_ATTRIBUTE)
}

/// The name token of the attribute containing `node`.
pub fn attribute_name_token(node: &SyntaxNode) -> Option<SyntaxToken> {
    attribute_at(node)?
        .child_node_of_kind(XML_NAME)?
        .child_token_of_kind(TK_NAME)
}

/// The name of the attribute containing `node`.
pub fn attribute_name(node: &SyntaxNode) -> Option<String> {
    attribute_name_token(node).map(|token| token.text().to_owned())
}

/// The raw value token of the attribute containing `node` (quotes included).
///
/// This is the first token of the value node, which is leading trivia when
/// whitespace separates `=` from the value.
pub fn attribute_value_token(node: &SyntaxNode) -> Option<SyntaxToken> {
    attribute_at(node)?
        .child_node_of_kind(XML_ATTRIBUTE_VALUE)?
        .first_token()
}

/// The unquoted value of the attribute containing `node`. Unterminated values
/// keep everything after the opening quote.
pub fn attribute_value(node: &SyntaxNode) -> Option<String> {
    attribute_value_token(node).map(|token| trim_attribute_quotes(token.text()).to_owned())
}

/// Name/value map of the (normalized) element's attributes. A repeated
/// attribute keeps its last value; attributes without a value map to "".
pub fn attribute_values(node: &SyntaxNode) -> BTreeMap<String, String> {
    attributes(node)
        .iter()
        .filter_map(|attribute| {
            let name = attribute_name(attribute)?;
            Some((name, attribute_value(attribute).unwrap_or_default()))
        })
        .collect()
}

/// The text node containing `node` (or `node` itself).
pub fn text_at(node: &SyntaxNode) -> Option<SyntaxNode> {
    ancestor_or_self(node, XML_TEXT)
}

/// The direct character content of the (normalized) element: text, raw entity
/// references and CDATA payloads. Nested child elements are intentionally
/// excluded, matching the value semantics of manifests, parameters and
/// system-config fields.
pub fn text_content(node: &SyntaxNode) -> String {
    let Some(content) =
        normalize_element(node).and_then(|element| element.child_node_of_kind(XML_CONTENT))
    else {
        return String::new();
    };
    let mut value = String::new();
    for child in content.children() {
        match child.kind() {
            XML_TEXT | XML_ENTITY_REFERENCE => value.push_str(&child.text().to_string()),
            XML_CDATA => {
                let text = child.text().to_string();
                let text = text.strip_prefix("<![CDATA[").unwrap_or(&text);
                let text = text.strip_suffix("]]>").unwrap_or(text);
                value.push_str(text);
            }
            _ => {}
        }
    }
    value
}

/// The value at `node`: the enclosing attribute's value, else the enclosing
/// text node, else the enclosing element's text content.
pub fn node_value(node: &SyntaxNode) -> String {
    if let Some(attribute) = attribute_at(node) {
        return attribute_value(&attribute).unwrap_or_default();
    }
    if let Some(text) = text_at(node) {
        return text.text().to_string();
    }
    if let Some(element) = element_at(node) {
        return text_content(&element);
    }
    String::new()
}

fn name_accepted(element: &SyntaxNode, names: &[&str]) -> bool {
    names.is_empty()
        || element_name_token(element).is_some_and(|token| names.contains(&token.text()))
}

fn normalize_element(node: &SyntaxNode) -> Option<SyntaxNode> {
    if let Some(element) = element_at(node) {
        return Some(element);
    }
    if node.kind() == XML_DOCUMENT {
        return node.descendants().find(|node| node.kind() == XML_ELEMENT);
    }
    None
}

fn start_tag(element: &SyntaxNode) -> Option<SyntaxNode> {
    element
        .child_node_of_kind(XML_START_TAG)
        .or_else(|| element.child_node_of_kind(XML_EMPTY_ELEMENT_TAG))
}

fn ancestor_or_self(node: &SyntaxNode, kind: Kind) -> Option<SyntaxNode> {
    node.ancestors().find(|node| node.kind() == kind)
}

fn trim_attribute_quotes(text: &str) -> &str {
    let Some(quote) = text.chars().next().filter(|c| matches!(c, '"' | '\'')) else {
        return text;
    };
    let text = &text[1..];
    text.strip_suffix(quote).unwrap_or(text)
}

#[cfg(test)]
mod tests {
    use swlsp_syntax::TextSize;

    use super::*;
    use crate::parse;

    #[test]
    fn element_and_attribute_queries() {
        let source = r#"<container><services><service id="App\Service" class='App\Service'><tag name="kernel.event_subscriber"/></service></services></container>"#;
        let root = parse(source).root();

        let services = elements(&root, &["service"]);
        assert_eq!(services.len(), 1);
        let id = attribute(&services[0], "id").unwrap();
        assert_eq!(attribute_value(&id).as_deref(), Some(r"App\Service"));
        assert_eq!(attribute_values(&services[0])["class"], r"App\Service");
        assert_eq!(child_elements(&services[0], &["tag"]).len(), 1);
        assert_eq!(elements(&root, &[]).len(), 4);
        assert_eq!(element_name(&root).as_deref(), Some("container"));

        let tag = child_element(&services[0], &["tag"]).unwrap();
        assert_eq!(
            element_name(&parent_element(&tag).unwrap()).as_deref(),
            Some("service")
        );
        assert_eq!(parent_element(&root), None);
        assert_eq!(attributes(&tag).len(), 1);
        assert_eq!(
            nodes(&root, &[XML_ATTRIBUTE, XML_EMPTY_ELEMENT_TAG]).len(),
            4
        );
    }

    #[test]
    fn text_and_cdata_queries() {
        let root = parse(r#"<meta><name> Test <![CDATA[App]]> </name><label lang="de">Deutsch</label><label>English</label></meta>"#).root();
        let meta = &elements(&root, &["meta"])[0];
        let name = child_element(meta, &["name"]).unwrap();
        assert_eq!(text_content(&name), " Test App ");
        assert_eq!(
            text_content(&child_elements(meta, &["label"])[1]),
            "English"
        );
        assert_eq!(node_value(&child_elements(meta, &["label"])[0]), "Deutsch");
    }

    #[test]
    fn incomplete_attribute_context() {
        let source = r#"<argument type="service" id="App\Service"#;
        let root = parse(source).root();
        let node = root
            .node_at(TextSize::from(source.len() as u32 - 1))
            .unwrap();

        let attribute = attribute_at(&node).unwrap();
        assert_eq!(attribute_name(&attribute).as_deref(), Some("id"));
        assert_eq!(attribute_value(&attribute).as_deref(), Some(r"App\Service"));
        assert_eq!(node_value(&node), r"App\Service");
        assert_eq!(element_at(&attribute).unwrap().kind(), XML_ELEMENT);
    }

    #[test]
    fn missing_values() {
        let root = parse("<a b c=/>").root();
        let values = attribute_values(&root);
        assert_eq!(values.get("b").map(String::as_str), Some(""));
        assert_eq!(values.get("c").map(String::as_str), Some(""));
        assert_eq!(text_content(&root), "");
        assert_eq!(trim_attribute_quotes("'x"), "x");
        assert_eq!(trim_attribute_quotes("x"), "x");
    }
}
