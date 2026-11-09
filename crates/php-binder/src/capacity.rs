//! Up-front capacity estimates derived from one census of the syntax tree.

use swlsp_parser_php::syntax::*;
use swlsp_syntax::rowan::{GreenNodeData, NodeOrToken};
use swlsp_syntax::{Kind, SyntaxNode};
use swlsp_textutil::eq_ignore_case;

const MAX_ESTIMATED_REFERENCES: usize = 4096;

pub(crate) fn estimated_symbol_capacity(source_bytes: u32) -> usize {
    if source_bytes == 0 {
        return 0;
    }
    // Reserve for declarations plus parameters and function-local symbols.
    // The hard cap protects large generated source files.
    const ESTIMATED_BYTES_PER_SYMBOL: usize = 1536;
    const MAX_ESTIMATED_SYMBOLS: usize = 512;
    (source_bytes as usize / ESTIMATED_BYTES_PER_SYMBOL + 1).min(MAX_ESTIMATED_SYMBOLS)
}

pub(crate) fn estimated_non_literal_type_fact_capacity(
    source_bytes: u32,
    literal_capacity: usize,
) -> usize {
    let capacity = estimated_type_fact_capacity(source_bytes);
    if capacity == 0 {
        return 0;
    }
    let general = capacity as isize - literal_capacity as isize;
    let minimum = capacity / 3 + 1;
    if general < minimum as isize {
        return minimum;
    }
    general as usize
}

#[cfg(test)]
pub(crate) fn estimated_document_symbol_capacity(root: &SyntaxNode) -> usize {
    estimated_document_capacities(root).symbols
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) struct Capacities {
    pub symbols: usize,
    pub references: usize,
    pub literals: usize,
    pub scopes: usize,
}

pub(crate) fn estimated_document_capacities(root: &SyntaxNode) -> Capacities {
    let mut capacity = estimated_symbol_capacity(u32::from(root.text_range().len()));
    let counts = structural_document_counts(root);
    let mut structural = counts.symbols;
    if structural > 0 {
        // PHPDoc-synthesized members are not represented by a one-to-one
        // declaration node. Local declaration candidates are counted below,
        // so only leave one slot of headroom.
        structural += 1;
    }
    const MAX_STRUCTURAL_SYMBOLS: usize = 4096;
    structural = structural.min(MAX_STRUCTURAL_SYMBOLS);
    capacity = capacity.max(structural);
    Capacities {
        symbols: capacity,
        references: estimated_document_reference_capacity(counts.references, counts.members),
        literals: counts.literals,
        scopes: estimated_scope_capacity(counts.scopes),
    }
}

#[cfg(test)]
pub(crate) fn structural_symbol_count(node: &SyntaxNode) -> usize {
    structural_document_counts(node).symbols
}

pub(crate) fn estimated_scope_capacity(scope_nodes: usize) -> usize {
    const MAX_ESTIMATED_SCOPES: usize = 4096;
    scope_nodes.saturating_add(1).min(MAX_ESTIMATED_SCOPES)
}

#[derive(Default)]
struct Counts {
    symbols: usize,
    references: usize,
    members: usize,
    literals: usize,
    scopes: usize,
}

fn structural_document_counts(root: &SyntaxNode) -> Counts {
    let mut counts = Counts::default();
    count_green(&root.green(), &mut counts);
    counts
}

type Green = GreenNodeData;

fn kind_of(green: &Green) -> Kind {
    Kind(green.kind().0)
}

fn child_nodes(green: &Green) -> impl Iterator<Item = &Green> {
    green.children().filter_map(NodeOrToken::into_node)
}

/// Census over the green tree: no red nodes are materialized.
fn count_green(node: &Green, counts: &mut Counts) {
    match kind_of(node) {
        PHP_NAME | PHP_VARIABLE => counts.references += 1,
        PHP_STRING | PHP_NUMBER | PHP_BOOLEAN | PHP_NULL => counts.literals += 1,
        // The binder deliberately leaves these targets for the
        // post-inference member linker. Reserve their final slots during
        // this census.
        PHP_MEMBER_CALL | PHP_SCOPED_CALL | PHP_MEMBER_ACCESS | PHP_SCOPED_ACCESS => {
            counts.members += 1;
        }
        _ => {}
    }
    match kind_of(node) {
        PHP_CLASS_DECLARATION
        | PHP_ANONYMOUS_CLASS
        | PHP_INTERFACE_DECLARATION
        | PHP_TRAIT_DECLARATION
        | PHP_ENUM_DECLARATION
        | PHP_METHOD_DECLARATION
        | PHP_FUNCTION_DECLARATION
        | PHP_CLOSURE
        | PHP_ARROW_FUNCTION => {
            counts.symbols += 1;
            counts.scopes += 1;
        }
        PHP_ENUM_CASE_DECLARATION => counts.symbols += 1,
        PHP_NAMESPACE => counts.scopes += 1,
        PHP_PARAMETER => {
            counts.symbols += 1;
            if parameter_is_promoted(node) {
                counts.symbols += 1;
            }
        }
        PHP_PROPERTY_DECLARATION => {
            counts.symbols += child_nodes(node)
                .filter(|child| kind_of(child) == PHP_VARIABLE)
                .count();
        }
        PHP_CONST_DECLARATION | PHP_CLASS_CONST_DECLARATION => {
            counts.symbols += direct_constant_count(node);
        }
        PHP_ASSIGNMENT_EXPRESSION => {
            if let Some(left) = child_nodes(node).next() {
                match kind_of(left) {
                    PHP_VARIABLE => counts.symbols += 1,
                    PHP_ARRAY => counts.symbols += descendant_kind_count(left, PHP_VARIABLE),
                    _ => {}
                }
            }
        }
        PHP_FOREACH_STATEMENT => counts.symbols += foreach_local_count(node),
        PHP_CATCH_CLAUSE => counts.symbols += 1,
        PHP_GLOBAL_STATEMENT | PHP_STATIC_STATEMENT => {
            counts.symbols += descendant_kind_count(node, PHP_VARIABLE);
        }
        _ => {}
    }
    for child in child_nodes(node) {
        count_green(child, counts);
    }
}

/// `declaration_flags(node).has(PROMOTED)` for a parameter.
fn parameter_is_promoted(node: &Green) -> bool {
    node.children()
        .filter_map(NodeOrToken::into_token)
        .any(|token| {
            Kind(token.kind().0) == TK_KEYWORD
                && ["public", "protected", "private"]
                    .iter()
                    .any(|visibility| eq_ignore_case(token.text(), visibility))
        })
}

/// Strict descendants of `kind`.
fn descendant_kind_count(node: &Green, kind: Kind) -> usize {
    child_nodes(node)
        .map(|child| usize::from(kind_of(child) == kind) + descendant_kind_count(child, kind))
        .sum()
}

fn direct_constant_count(node: &Green) -> usize {
    let mut count = 0;
    let mut expect_name = false;
    for child in node.children() {
        match child {
            NodeOrToken::Token(token) => {
                if eq_ignore_case(token.text(), "const") || Kind(token.kind().0) == TK_COMMA {
                    expect_name = true;
                } else if Kind(token.kind().0) == TK_EQUALS {
                    expect_name = false;
                }
            }
            NodeOrToken::Node(child) => {
                if expect_name && kind_of(child) == PHP_NAME {
                    count += 1;
                    expect_name = false;
                }
            }
        }
    }
    count
}

fn foreach_local_count(node: &Green) -> usize {
    let mut count = 0;
    let mut in_targets = false;
    for child in node.children() {
        match child {
            NodeOrToken::Token(token) => {
                if eq_ignore_case(token.text(), "as") {
                    in_targets = true;
                } else if in_targets && Kind(token.kind().0) == TK_CLOSE_PAREN {
                    return count;
                }
            }
            NodeOrToken::Node(child) => {
                if in_targets {
                    count += usize::from(kind_of(child) == PHP_VARIABLE)
                        + descendant_kind_count(child, PHP_VARIABLE);
                }
            }
        }
    }
    count
}

pub(crate) fn estimated_reference_capacity(reference_nodes: usize) -> usize {
    // Declaration names and member targets are not references themselves. A
    // three-quarter density keeps the estimate correlated with syntax shape
    // without reserving space for every name-like node.
    (reference_nodes.saturating_mul(3).saturating_add(3) / 4).min(MAX_ESTIMATED_REFERENCES)
}

pub(crate) fn estimated_linked_reference_capacity(
    reference_nodes: usize,
    member_nodes: usize,
) -> usize {
    (estimated_reference_capacity(reference_nodes) + member_nodes).min(MAX_ESTIMATED_REFERENCES)
}

pub(crate) fn estimated_document_reference_capacity(
    reference_nodes: usize,
    member_nodes: usize,
) -> usize {
    const NUMERATOR: usize = 9;
    const DENOMINATOR: usize = 10;
    let capacity = estimated_linked_reference_capacity(reference_nodes, member_nodes);
    if capacity == MAX_ESTIMATED_REFERENCES {
        return capacity;
    }
    (capacity * NUMERATOR).div_ceil(DENOMINATOR)
}

pub(crate) fn estimated_type_fact_capacity(source_bytes: u32) -> usize {
    estimated_type_fact_capacity_for_bytes(source_bytes, 40)
}

fn estimated_type_fact_capacity_for_bytes(source_bytes: u32, bytes_per_fact: usize) -> usize {
    const MAX_ESTIMATED_TYPE_FACTS: usize = 4096;
    if bytes_per_fact == 0 {
        return 0;
    }
    (source_bytes as usize / bytes_per_fact).min(MAX_ESTIMATED_TYPE_FACTS)
}
