//! Lexical Vue template bindings: `v-for` aliases and implicit `$event`
//! locals (port of Go `vue_scope_bindings.go`).

use swlsp_parser_twig::ast::AstNode;

use crate::*;

/// All `v-for` declarations and implicit event locals in a template.
pub fn twig_vue_bindings(root: &SyntaxNode, content: &str) -> Vec<TwigVueBinding> {
    twig_vue_bindings_impl(
        root,
        content,
        TwigVueBindingScan {
            include_events: true,
            ..Default::default()
        },
    )
}

pub(crate) fn twig_vue_for_bindings(root: &SyntaxNode, content: &str) -> Vec<TwigVueBinding> {
    twig_vue_bindings_impl(root, content, TwigVueBindingScan::default())
}

pub(crate) fn twig_vue_bindings_for_target(
    root: &SyntaxNode,
    content: &str,
    target: &TwigVueBinding,
) -> Vec<TwigVueBinding> {
    let mut bindings = twig_vue_for_bindings(root, content);
    if target.kind == TwigVueBindingKind::Event {
        bindings.push(target.clone());
    }
    bindings
}

#[derive(Clone, Copy, Debug, Default)]
pub(crate) struct TwigVueBindingScan {
    pub include_events: bool,
    pub at_offset: bool,
    pub offset: u32,
}

fn outside(range: TextRange, offset: u32) -> bool {
    offset < u32::from(range.start()) || offset > u32::from(range.end())
}

pub(crate) fn twig_vue_bindings_impl(
    root: &SyntaxNode,
    content: &str,
    scan: TwigVueBindingScan,
) -> Vec<TwigVueBinding> {
    let mut result = Vec::new();
    for node in twigquery::iterate_nodes(root, &[twigsyntax::HTML_TAG]) {
        let Some(tag) = twigast::HtmlTag::cast(node.clone()) else {
            continue;
        };
        let Some(tag_name) = tag.name() else {
            continue;
        };
        let Some(starting_tag) = tag.starting_tag() else {
            continue;
        };
        for attribute in starting_tag.attributes() {
            let name = twigquery::html_attribute_name(attribute.syntax()).unwrap_or_default();
            let Some(inner) = attribute.value().and_then(|value| value.inner()) else {
                continue;
            };
            let inner = inner.syntax();
            let expression_range = inner.range_trimmed_trivia();
            if u32::from(expression_range.end()) as usize > content.len() {
                continue;
            }
            if name == "v-for" {
                let scope_range = node.range_trimmed_trivia();
                if scan.at_offset && outside(scope_range, scan.offset) {
                    continue;
                }
                result.extend(parse_twig_vue_for_bindings(
                    &inner.text().to_string(),
                    u32::from(expression_range.start()),
                    scope_range,
                    expression_range,
                ));
                continue;
            }
            if !scan.include_events || scan.at_offset && outside(expression_range, scan.offset) {
                continue;
            }
            let event_name = normalize_event_name(&name);
            if event_name.is_empty() {
                continue;
            }
            result.push(TwigVueBinding {
                name: "$event".to_owned(),
                kind: TwigVueBindingKind::Event,
                scope_range: expression_range,
                expression_range,
                component_name: tag_name.text().to_owned(),
                event_name,
                ..Default::default()
            });
        }
    }
    result
}

/// The effective lexical bindings at `offset`; innermost declarations
/// replace outer declarations of the same name. A `v-for` alias is not
/// visible in its own iterable expression.
pub fn twig_vue_bindings_at_offset(
    root: &SyntaxNode,
    content: &str,
    offset: u32,
) -> Vec<TwigVueBinding> {
    twig_vue_bindings_at_offset_impl(
        &twig_vue_bindings_impl(
            root,
            content,
            TwigVueBindingScan {
                include_events: true,
                at_offset: true,
                offset,
            },
        ),
        offset,
    )
}

pub(crate) fn twig_vue_bindings_at_offset_impl(
    bindings: &[TwigVueBinding],
    offset: u32,
) -> Vec<TwigVueBinding> {
    let mut visible: Vec<TwigVueBinding> = bindings
        .iter()
        .filter(|binding| {
            if outside(binding.scope_range, offset) {
                return false;
            }
            !(binding.kind == TwigVueBindingKind::For
                && !outside(binding.expression_range, offset)
                && !binding.is_declaration_offset(offset))
        })
        .cloned()
        .collect();
    // Outer scopes first, innermost last; stable for tuple order.
    visible.sort_by_key(|binding| std::cmp::Reverse(binding.scope_range.len()));
    let mut positions: HashMap<String, usize> = HashMap::with_capacity(visible.len());
    let mut result: Vec<TwigVueBinding> = Vec::with_capacity(visible.len());
    for binding in visible {
        if let Some(&index) = positions.get(&binding.name) {
            result[index] = binding;
            continue;
        }
        positions.insert(binding.name.clone(), result.len());
        result.push(binding);
    }
    result
}

/// The lexical Vue variable touching `offset`.
pub fn twig_vue_binding_at_offset(
    root: &SyntaxNode,
    content: &str,
    offset: u32,
) -> Option<TwigVueBinding> {
    let bindings = twig_vue_bindings_at_offset(root, content, offset);
    if let Some(binding) = bindings
        .iter()
        .find(|binding| binding.is_declaration_offset(offset))
    {
        return Some(binding.clone());
    }
    let (name, _) = twig_vue_expression_root_identifier_at_offset(root, content, offset)?;
    bindings.into_iter().find(|binding| binding.name == name)
}

/// Every lexical Vue binding of the document (`v-for` aliases and `$event`),
/// for callers resolving many offsets: pass it to
/// [`twig_vue_binding_at_offset_in`] instead of rescanning the document per
/// offset.
pub fn twig_vue_document_bindings(root: &SyntaxNode, content: &str) -> Vec<TwigVueBinding> {
    twig_vue_bindings_impl(
        root,
        content,
        TwigVueBindingScan {
            include_events: true,
            at_offset: false,
            offset: 0,
        },
    )
}

/// [`twig_vue_binding_at_offset`] over precomputed
/// [`twig_vue_document_bindings`]. The per-offset scan only drops bindings
/// whose scope excludes `offset`, which the visibility filter removes too.
pub fn twig_vue_binding_at_offset_in(
    bindings: &[TwigVueBinding],
    root: &SyntaxNode,
    content: &str,
    offset: u32,
) -> Option<TwigVueBinding> {
    let bindings = twig_vue_bindings_at_offset_impl(bindings, offset);
    if let Some(binding) = bindings
        .iter()
        .find(|binding| binding.is_declaration_offset(offset))
    {
        return Some(binding.clone());
    }
    let (name, _) = twig_vue_expression_root_identifier_at_offset(root, content, offset)?;
    bindings.into_iter().find(|binding| binding.name == name)
}

/// A declaration and all root-identifier usages resolving to the same
/// lexical binding.
pub fn twig_vue_binding_references(
    root: &SyntaxNode,
    content: &str,
    target: &TwigVueBinding,
) -> Vec<TextRange> {
    if target.name.is_empty() {
        return Vec::new();
    }
    let bindings = twig_vue_bindings_for_target(root, content, target);
    let mut result = Vec::new();
    let mut seen = HashSet::new();
    let mut add = |range: TextRange, result: &mut Vec<TextRange>| {
        if range.is_empty() || !seen.insert(range) {
            return;
        }
        result.push(range);
    };
    add(target.declaration_range, &mut result);
    let bytes = content.as_bytes();
    let needle = target.name.as_bytes();
    let mut start = 0;
    while start < bytes.len() {
        let Some(relative) = find_bytes(&bytes[start..], needle) else {
            break;
        };
        let position = start + relative;
        let end = position + needle.len();
        start = end;
        if position > 0 && is_slot_identifier_continue(bytes[position - 1])
            || end < bytes.len() && is_slot_identifier_continue(bytes[end])
        {
            continue;
        }
        let Some((name, range)) =
            twig_vue_expression_root_identifier_at_offset(root, content, position as u32)
        else {
            continue;
        };
        if name != target.name {
            continue;
        }
        let candidates = twig_vue_bindings_at_offset_impl(&bindings, position as u32);
        if candidates
            .iter()
            .any(|candidate| candidate.name == name && candidate.same_identity(target))
        {
            add(range, &mut result);
        }
    }
    result.sort_by_key(|range| range.start());
    result
}
