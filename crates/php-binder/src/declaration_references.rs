//! Class-name references in declarations (clauses, types, attributes).

use crate::fast::Fast;
use swlsp_parser_php::syntax::*;
use swlsp_php_resolver::NameContext;
use swlsp_php_semantic::{NameKind, Reference, ScopeId, SymbolId};
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::eq_ignore_case;

use crate::builder::Builder;
use crate::modifiers::compact_name;

impl Builder<'_> {
    pub(crate) fn bind_class_clauses(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
    ) {
        for clause_kind in [PHP_EXTENDS_CLAUSE, PHP_IMPLEMENTS_CLAUSE] {
            let Some(clause) = node.child_node_of_kind(clause_kind) else {
                continue;
            };
            for child in clause.children().filter(|child| child.kind() == PHP_NAME) {
                self.add_resolved_class_reference(&child, scope, context, &SymbolId::default());
            }
        }
    }

    pub(crate) fn bind_direct_type_references(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
    ) {
        for child in node.children().filter(|child| is_type_node(child.kind())) {
            self.bind_type_references(&child, scope, context, owner);
        }
    }

    fn bind_type_references(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
    ) {
        if node.kind() == PHP_TYPE
            && let Some(name_node) = node.child_node_of_kind(PHP_NAME)
        {
            self.add_resolved_class_reference(&name_node, scope, context, owner);
        }
        for child in node.children().filter(|child| is_type_node(child.kind())) {
            self.bind_type_references(&child, scope, context, owner);
        }
    }

    pub(crate) fn bind_attribute_references(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
    ) {
        for group in node
            .children()
            .filter(|child| child.kind() == PHP_ATTRIBUTE_GROUP)
        {
            for attribute in group
                .children()
                .filter(|child| child.kind() == PHP_ATTRIBUTE)
            {
                if let Some(name_node) = attribute.child_node_of_kind(PHP_NAME) {
                    self.add_resolved_class_reference(
                        &name_node,
                        scope,
                        context,
                        &SymbolId::default(),
                    );
                }
                // `#[Route(defaults: ['_acl' => [Privileges::READ]])]`
                if let Some(arguments) = attribute.child_node_of_kind(PHP_ARGUMENT_LIST) {
                    self.bind_body(&arguments, scope, context, &SymbolId::default());
                }
            }
        }
    }

    /// Binds the references in the values a declaration assigns with `=`:
    /// constant values, property and parameter defaults, enum case values
    /// (`const TYPES = [Field::class => Types::JSON]`).
    pub(crate) fn bind_value_references(
        &mut self,
        node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
    ) {
        let mut value = false;
        for child in node.children_with_tokens() {
            if child.kind() == TK_EQUALS {
                value = true;
            } else if let Some(child) = child.as_node()
                && std::mem::take(&mut value)
            {
                self.bind_body_node(child, scope, context, owner);
            }
        }
    }

    pub(crate) fn add_resolved_class_reference(
        &mut self,
        name_node: &SyntaxNode,
        scope: ScopeId,
        context: &NameContext,
        owner: &SymbolId,
    ) {
        let name = crate::fast::name_value(name_node, self.source);
        if name.is_empty() || non_class_native_type_name(&name) {
            return;
        }
        let trimmed = name.strip_prefix('\\').unwrap_or(&name);
        let resolved = if eq_ignore_case(trimmed, "self") || eq_ignore_case(trimmed, "static") {
            match self.enclosing_class(owner) {
                Some(index) => self.doc.symbols[index].fully_qualified.to_string(),
                None => String::new(),
            }
        } else if eq_ignore_case(trimmed, "parent") {
            match self.enclosing_class(owner) {
                Some(index) => self.doc.symbols[index]
                    .extends()
                    .first()
                    .map(ToString::to_string)
                    .unwrap_or_default(),
                None => String::new(),
            }
        } else {
            context.resolve_class(&name)
        };
        if resolved.is_empty() {
            return;
        }
        self.add_single_reference(name_node, NameKind::Class, scope, &resolved);
    }

    pub(crate) fn add_reference(
        &mut self,
        node: &SyntaxNode,
        kind: NameKind,
        scope: ScopeId,
        qualified: Vec<String>,
    ) {
        let mut reference = Reference {
            name: compact_name(&self.source[node.trimmed()]).into(),
            kind,
            range: node.trimmed(),
            scope,
            ..Reference::default()
        };
        reference.set_qualified_names(qualified.into_iter().map(Into::into).collect());
        self.doc.references.push(reference);
    }

    pub(crate) fn add_single_reference(
        &mut self,
        node: &SyntaxNode,
        kind: NameKind,
        scope: ScopeId,
        qualified: &str,
    ) {
        let mut reference = Reference {
            name: compact_name(&self.source[node.trimmed()]).into(),
            kind,
            range: node.trimmed(),
            scope,
            ..Reference::default()
        };
        reference.set_qualified_name(qualified);
        self.doc.references.push(reference);
    }

    /// Index of the innermost class-like declaration owning `owner`.
    pub(crate) fn enclosing_class(&self, owner: &SymbolId) -> Option<usize> {
        let mut owner = owner.clone();
        while !owner.is_empty() {
            let index = self.symbol_index(&owner)? as usize;
            let symbol = &self.doc.symbols[index];
            if symbol.is_class_like() {
                return Some(index);
            }
            owner = symbol.container.clone();
        }
        None
    }
}

/// Mirrors the scalar/composite names accepted by the shared type parser,
/// except the ones that are ordinary class names in PHP (`Closure`,
/// `Error`, `Number`, `Unknown`): a native type or a `::` receiver with
/// such a name refers to that class and uses its import.
/// Native CST references only need to distinguish these builtins from class
/// names.
pub(crate) fn non_class_native_type_name(name: &str) -> bool {
    let name = name.strip_prefix('\\').unwrap_or(name);
    let candidates: &[&str] = match name.len() {
        3 => &["int"],
        4 => &["void", "null", "bool", "true", "list"],
        5 => &["never", "mixed", "false", "float", "array"],
        6 => &["double", "string", "object"],
        7 => &["boolean", "integer"],
        8 => &["resource", "iterable", "callable"],
        9 => &["array-key"],
        11 => &["enum-string"],
        12 => &[
            "positive-int",
            "negative-int",
            "class-string",
            "trait-string",
        ],
        14 => &["numeric-string", "literal-string", "non-empty-list"],
        15 => &["non-empty-array"],
        16 => &["non-negative-int", "non-empty-string", "interface-string"],
        _ => return false,
    };
    candidates
        .iter()
        .any(|candidate| eq_ignore_case(name, candidate))
}

pub(crate) fn is_type_node(kind: Kind) -> bool {
    matches!(
        kind,
        PHP_TYPE | PHP_NULLABLE_TYPE | PHP_UNION_TYPE | PHP_INTERSECTION_TYPE
    )
}
