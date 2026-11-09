//! Member reference linking after inference.

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::{MemberResolver, NameContext};
use swlsp_php_semantic::{Document, NameKind, Reference, Snapshot, Str, Symbol, SymbolKind};
use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::lowercase;

use crate::syntax::{direct_nodes, direct_operator, first_node, has_direct_token, start};
use crate::types_util::NamedTypeCache;

/// Resolves member-name references once expression inference has
/// established receiver types, on a copy of `document`.
pub fn link_members(document: &Document, snapshot: &Snapshot, root: &SyntaxNode) -> Document {
    link_members_owned(document.clone(), snapshot, root)
}

/// Appends resolved member references to a document owned by the caller.
pub fn link_members_owned(
    mut document: Document,
    snapshot: &Snapshot,
    root: &SyntaxNode,
) -> Document {
    let resolver = MemberResolver::new(snapshot);
    let mut static_receiver_types = NamedTypeCache::default();
    let mut references = Vec::new();
    query::visit(
        root,
        &[
            PHP_MEMBER_CALL,
            PHP_SCOPED_CALL,
            PHP_MEMBER_ACCESS,
            PHP_SCOPED_ACCESS,
        ],
        |node| {
            let nodes = direct_nodes(node);
            if nodes.len() < 2 {
                return true;
            }
            let is_static = node.kind() == PHP_SCOPED_CALL
                || node.kind() == PHP_SCOPED_ACCESS
                || has_direct_token(node, TK_SCOPE_RESOLUTION);
            let receiver_node = &nodes[0];
            let mut receiver = document.type_of(receiver_node).ty;
            if receiver.is_unknown() && receiver_node.kind() == PHP_VARIABLE {
                receiver = declared_variable_type(&document, snapshot, receiver_node);
            }
            if receiver.is_unknown() && is_static && receiver_node.kind() == PHP_NAME {
                receiver = static_receiver_type(
                    &document,
                    snapshot,
                    &query::name_value(receiver_node),
                    start(receiver_node),
                    &mut static_receiver_types,
                );
            }
            let name_node = &nodes[1];
            // A variable member name is resolved at runtime.
            if name_node.kind() == PHP_VARIABLE
                || (!is_static && name_node.text().to_string().trim().starts_with('$'))
            {
                return true;
            }
            let name = query::name_value(name_node);
            if name.is_empty() || (is_static && swlsp_textutil::eq_ignore_case(&name, "class")) {
                return true;
            }
            let dynamic_class_property = node.kind() == PHP_SCOPED_CALL
                && receiver_node.kind() == PHP_OBJECT_CREATION
                && name.starts_with('$');
            let target_kind = match node.kind() {
                PHP_MEMBER_CALL | PHP_SCOPED_CALL if !dynamic_class_property => SymbolKind::Method,
                PHP_MEMBER_CALL | PHP_SCOPED_CALL => SymbolKind::Property,
                _ if is_static && !name.starts_with('$') => SymbolKind::ClassConstant,
                _ => SymbolKind::Property,
            };
            let mut reference = Reference {
                name: Str::from(name.as_str()),
                kind: NameKind::Member,
                receiver: receiver.clone(),
                target_kind,
                is_static,
                write: member_is_write(node),
                range: name_node.range_trimmed_trivia(),
                ..Reference::default()
            };
            let mut visit = |view: swlsp_php_semantic::SymbolView<'_>| {
                reference.add_candidate(view.symbol_id());
                true
            };
            if dynamic_class_property {
                resolver.visit_property_ids(&receiver, &name, &mut visit);
            } else if matches!(node.kind(), PHP_MEMBER_CALL | PHP_SCOPED_CALL) {
                resolver.visit_method_ids(&receiver, &name, &mut visit);
            } else if is_static && !name.starts_with('$') {
                resolver.visit_constant_ids(&receiver, &name, &mut visit);
            } else {
                resolver.visit_property_ids(&receiver, &name, &mut visit);
            }
            if let Some(scope) = document.scope_at(start(name_node)) {
                reference.scope = scope.id;
            }
            references.push(reference);
            true
        },
    );
    document.references.extend(references);
    document
}

fn declared_variable_type(document: &Document, snapshot: &Snapshot, node: &SyntaxNode) -> Type {
    let name = query::variable_key(node);
    let Some(mut scope) = document.scope_at(start(node)) else {
        return Type::UNKNOWN;
    };
    loop {
        for id in scope.symbol_ids(&document.symbols, &name) {
            // Local types are inferred into the analyzed document, not the
            // snapshot. Prefer the document's current declaration for IDs the
            // snapshot knows.
            if snapshot.symbol_view(id).is_none() {
                continue;
            }
            let ty = match document.symbols.iter().find(|symbol| symbol.id == *id) {
                Some(symbol) => symbol.ty.clone(),
                None => snapshot
                    .symbol(id)
                    .map(|symbol| symbol.ty)
                    .unwrap_or_default(),
            };
            if !ty.is_unknown() {
                return ty;
            }
        }
        let parent = scope.parent as usize;
        if scope.id as usize == parent || parent >= document.scopes.len() {
            return Type::UNKNOWN;
        }
        scope = &document.scopes[parent];
    }
}

fn member_is_write(node: &SyntaxNode) -> bool {
    let Some(parent) = node.parent() else {
        return false;
    };
    match parent.kind() {
        PHP_ASSIGNMENT_EXPRESSION => {
            first_node(&parent).is_some_and(|left| left.text_range() == node.text_range())
        }
        PHP_UNARY_EXPRESSION => {
            let operator = direct_operator(&parent);
            operator == "++" || operator == "--"
        }
        _ => false,
    }
}

fn name_context(document: &Document, offset: u32) -> NameContext {
    match document.scope_at(offset) {
        Some(scope) => NameContext::with_imports(scope.namespace.clone(), &scope.imports),
        None => NameContext::new(&document.namespace),
    }
}

fn static_receiver_type(
    document: &Document,
    snapshot: &Snapshot,
    name: &str,
    offset: u32,
    named_types: &mut NamedTypeCache,
) -> Type {
    match lowercase(name).as_str() {
        "self" | "static" => {
            if let Some(class) = enclosing_class_at(document, snapshot, offset) {
                return named_types.type_for(&class.fully_qualified);
            }
        }
        "parent" => {
            if let Some(class) = enclosing_class_at(document, snapshot, offset)
                && let Some(parent) = class.extends().first()
            {
                return named_types.type_for(parent);
            }
        }
        _ => {
            return named_types.type_for(&name_context(document, offset).resolve_class(name));
        }
    }
    Type::UNKNOWN
}

fn enclosing_class_at(document: &Document, snapshot: &Snapshot, offset: u32) -> Option<Symbol> {
    let mut scope = document.scope_at(offset)?;
    loop {
        if !scope.owner.is_empty()
            && let Some(symbol) = snapshot.symbol(&scope.owner)
        {
            if symbol.is_class_like() {
                return Some(symbol);
            }
            if !symbol.container.is_empty()
                && let Some(container) = snapshot.symbol(&symbol.container)
                && container.is_class_like()
            {
                return Some(container);
            }
        }
        let parent = scope.parent as usize;
        if scope.id as usize == parent || parent >= document.scopes.len() {
            return None;
        }
        scope = &document.scopes[parent];
    }
}
