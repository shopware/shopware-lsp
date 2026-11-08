//! Template usage helpers (port of Go `usage_twig_helpers.go`).

use crate::*;

use twigast::AstNode;

/// Administration symbol occurrences of a live template CST, without
/// touching the persistent index.
pub fn collect_twig_usages(
    root: &SyntaxNode,
    file_path: &str,
    line_index: &LineIndex,
) -> Vec<AdminUsageSet> {
    parse_admin_twig_usages(root, file_path, line_index)
}

pub(crate) fn static_component_contract_names(start_tag: Option<&SyntaxNode>) -> Vec<String> {
    let Some(start_tag) = start_tag else {
        return Vec::new();
    };
    let name = twigquery::html_tag_name(start_tag).unwrap_or_default();
    if is_component_tag(&name) {
        return vec![name];
    }
    let Some(selector) = twig_dynamic_component_selector(start_tag) else {
        return Vec::new();
    };
    if !selector.complete {
        return Vec::new();
    }
    selector
        .names()
        .into_iter()
        .filter(|name| is_component_tag(name))
        .collect()
}

pub(crate) fn twig_vue_root_identifier_is_local_impl(
    root: &SyntaxNode,
    content: &str,
    identifier: &TwigVueMember,
) -> bool {
    twig_vue_root_identifier_is_local_in(
        root,
        &twig_vue_for_bindings(root, content),
        &twig_scoped_slots(root),
        identifier,
    )
}

/// Whether `identifier` resolves to a lexical v-for, event, or scoped-slot
/// binding instead of the owning component instance.
pub fn twig_vue_root_identifier_is_local(
    root: &SyntaxNode,
    content: &str,
    identifier: &TwigVueMember,
) -> bool {
    twig_vue_root_identifier_is_local_impl(root, content, identifier)
}

/// The ranges of all identifiers resolving to lexical bindings, computed
/// with one shared scope scan.
pub fn twig_vue_local_root_identifier_ranges(
    root: &SyntaxNode,
    content: &str,
    identifiers: &[TwigVueMember],
) -> HashSet<TextRange> {
    let mut result = HashSet::new();
    if identifiers.is_empty() {
        return result;
    }
    let bindings = twig_vue_for_bindings(root, content);
    let scopes = twig_scoped_slots(root);
    for identifier in identifiers {
        if twig_vue_root_identifier_is_local_in(root, &bindings, &scopes, identifier) {
            result.insert(identifier.range);
        }
    }
    result
}

pub(crate) fn twig_vue_root_identifier_is_local_in(
    root: &SyntaxNode,
    bindings: &[TwigVueBinding],
    scopes: &[TwigScopedSlot],
    identifier: &TwigVueMember,
) -> bool {
    if twig_vue_event_identifier_is_local(root, identifier)
        || twig_vue_binding_identifier_is_local(bindings, identifier)
    {
        return true;
    }
    twig_scoped_slots_at_offset_impl(scopes, u32::from(identifier.range.start()))
        .iter()
        .any(|scope| {
            scope
                .bindings
                .iter()
                .any(|binding| binding.local_name == identifier.name)
        })
}

fn twig_vue_event_identifier_is_local(root: &SyntaxNode, identifier: &TwigVueMember) -> bool {
    if identifier.name != "$event" {
        return false;
    }
    root.node_at(identifier.range.start())
        .and_then(|node| twigquery::html_attribute_at(&node))
        .is_some_and(|attribute| {
            !normalize_event_name(&twigquery::html_attribute_name(&attribute).unwrap_or_default())
                .is_empty()
        })
}

fn twig_vue_binding_identifier_is_local(
    bindings: &[TwigVueBinding],
    identifier: &TwigVueMember,
) -> bool {
    let offset = identifier.range.start();
    for binding in bindings {
        if binding.name != identifier.name {
            continue;
        }
        if binding.is_declaration_offset(u32::from(offset)) {
            return true;
        }
        if offset < binding.scope_range.start() || offset > binding.scope_range.end() {
            continue;
        }
        if binding.kind == TwigVueBindingKind::For
            && offset >= binding.expression_range.start()
            && offset <= binding.expression_range.end()
        {
            continue;
        }
        return true;
    }
    false
}

pub(crate) fn collect_slot_declaration(
    tag: &twigast::HtmlStartingTag,
    file_path: &str,
    collector: &mut AdminUsageCollector<'_>,
) {
    for attribute in tag.attributes() {
        if twigquery::html_attribute_name(attribute.syntax()).as_deref() != Some("name") {
            continue;
        }
        let Some(inner) = attribute.value().and_then(|value| value.inner()) else {
            return;
        };
        let text = inner.syntax().text().to_string();
        let name = text.trim();
        if name.is_empty() || name.contains(['{', '}', '%']) {
            return;
        }
        collector.add_range(
            AdminSymbolKind::ComponentSlot,
            file_path,
            name,
            inner.syntax().range_trimmed_trivia(),
            true,
            false,
        );
        return;
    }
}

pub(crate) fn must_vue_attribute_argument_range(
    range: TextRange,
    attribute_name: &str,
) -> TextRange {
    vue_attribute_argument_range(attribute_name, range).unwrap_or(range)
}

/// The nearest enclosing statically named component tag.
pub(crate) fn parent_component_name(node: &SyntaxNode) -> String {
    for current in node.ancestors().skip(1) {
        let Some(tag) = twigast::HtmlTag::cast(current) else {
            continue;
        };
        if tag.name().is_none() {
            continue;
        }
        let Some(starting) = tag.starting_tag() else {
            continue;
        };
        if let Some(name) = static_component_name_for_tag(starting.syntax()) {
            return name;
        }
    }
    String::new()
}
