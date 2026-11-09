//! Twig symbol entry points.

use crate::*;

pub fn twig_symbol_at(node: &SyntaxNode) -> Option<AdminSymbolTarget> {
    let root = node.ancestors().last().unwrap_or_else(|| node.clone());
    twig_symbol_at_offset(&root, u32::from(node.text_range().start()))
}

/// Every custom directive attribute of one template; Vue built-ins are
/// excluded.
pub fn twig_directive_references(root: &SyntaxNode) -> Vec<VueDirectiveReference> {
    let mut result = Vec::new();
    for attribute_node in twigquery::iterate_nodes(root, &[twigsyntax::HTML_ATTRIBUTE]) {
        let Some(attribute) =
            <twigast::HtmlAttribute as twigast::AstNode>::cast(attribute_node.clone())
        else {
            continue;
        };
        let Some(name) = attribute.name() else {
            continue;
        };
        if let Some(reference) = vue_directive_reference_for_attribute(
            &twigquery::html_attribute_name(&attribute_node).unwrap_or_default(),
            name.text_range(),
        ) {
            result.push(reference);
        }
    }
    result
}

pub fn twig_directive_at_offset(root: &SyntaxNode, offset: u32) -> Option<VueDirectiveReference> {
    twig_directive_references(root)
        .into_iter()
        .find(|reference| range_contains(reference.range, offset))
}

pub(crate) fn string_target(kind: AdminSymbolKind, node: &SyntaxNode) -> Option<AdminSymbolTarget> {
    let name = jsquery::string_value(node);
    if name.is_empty() {
        return None;
    }
    Some(AdminSymbolTarget {
        kind,
        owner: String::new(),
        name,
    })
}
