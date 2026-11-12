//! Symfony Validator constraint options and constraint/validator pairs.
//!
//! Request-time PHP analysis is passed explicitly as a [`PhpContext`].

use std::collections::HashSet;

use swlsp_parser_php::query;
use swlsp_parser_php::syntax::{PHP_NAMED_ARGUMENT, PHP_OBJECT_CREATION};
use swlsp_php::{PhpContext, PhpIndex};
use swlsp_php_resolver::MemberResolver;
use swlsp_php_semantic::{Snapshot, Symbol, SymbolKind, Visibility};
use swlsp_php_types::Type;
use swlsp_syntax::{SyntaxNode, SyntaxNodeExt};
use swlsp_textutil::{eq_ignore_case, lowercase};

pub const CONSTRAINT_CLASS: &str = "Symfony\\Component\\Validator\\Constraint";
pub const CONSTRAINT_VALIDATOR_CLASS: &str =
    "Symfony\\Component\\Validator\\ConstraintValidatorInterface";

/// A string key in the options array passed as the first argument of a
/// constraint instantiation (`new Length(['min' => 1])`).
#[derive(Clone, Debug)]
pub struct OptionReference {
    pub name: String,
    pub constraint: String,
    pub node: SyntaxNode,
    pub container: SyntaxNode,
}

pub fn option_reference_at(
    context: Option<&PhpContext>,
    root: &SyntaxNode,
    node: &SyntaxNode,
) -> Option<OptionReference> {
    let literal = query::string_at(node)?;
    let item = query::array_item_at(&literal)?;
    if query::array_item_key(&item).as_ref() != Some(&literal) {
        return None;
    }
    let array = query::array_at(&item)?;
    if item.parent().as_ref() != Some(&array) {
        return None;
    }
    let object = array.ancestor_or_self_of_kind(PHP_OBJECT_CREATION)?;
    if query::argument_expression(&object, 0).as_ref() != Some(&array) {
        return None;
    }
    // `new Collection(fields: [...])` names a constructor parameter; only
    // an unnamed array, or one named `options`, holds options.
    if let Some(argument) = array.parent()
        && argument.kind() == PHP_NAMED_ARGUMENT
        && !query::argument_name(&argument).eq_ignore_ascii_case("options")
    {
        return None;
    }
    let resolver = swlsp_php::name_resolver_for(context, root);
    let class_name = resolver.resolve(&query::object_class_name(&object));
    let class_name = class_name.strip_prefix('\\').unwrap_or(&class_name);
    if class_name.is_empty() || !is_constraint(context, class_name) {
        return None;
    }
    Some(OptionReference {
        name: query::string_value(&literal),
        constraint: class_name.to_owned(),
        node: literal,
        container: object,
    })
}

pub fn is_constraint(context: Option<&PhpContext>, class_name: &str) -> bool {
    let Some(context) = context else {
        return false;
    };
    context.snapshot.relations().is_subtype(
        &Type::named(
            class_name.strip_prefix('\\').unwrap_or(class_name),
            Vec::new(),
        ),
        &Type::named(CONSTRAINT_CLASS, Vec::new()),
    )
}

/// Public properties of a constraint (including inherited ones), unique and
/// sorted case-insensitively.
pub fn constraint_properties(index: &PhpIndex, class_name: &str) -> Vec<Symbol> {
    if class_name.is_empty() {
        return Vec::new();
    }
    properties_in(&index.semantic_snapshot(), class_name)
}

pub fn constraint_properties_in_context(
    context: Option<&PhpContext>,
    class_name: &str,
) -> Vec<Symbol> {
    context.map_or_else(Vec::new, |context| {
        properties_in(&context.snapshot, class_name)
    })
}

fn properties_in(snapshot: &Snapshot, class_name: &str) -> Vec<Symbol> {
    let members = MemberResolver {
        snapshot: Some(snapshot),
    }
    .all(&Type::named(
        class_name.strip_prefix('\\').unwrap_or(class_name),
        Vec::new(),
    ));
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for member in members {
        let symbol = member.symbol;
        if symbol.kind != SymbolKind::Property || symbol.visibility != Visibility::Public {
            continue;
        }
        if !seen.insert(lowercase(&symbol.name)) {
            continue;
        }
        result.push(symbol);
    }
    // Names equal under case folding keep their member order.
    result.sort_by_cached_key(|symbol| lowercase(&symbol.name));
    result
}

pub fn find_constraint_property<'a>(properties: &'a [Symbol], name: &str) -> Option<&'a Symbol> {
    properties
        .iter()
        .find(|property| eq_ignore_case(&property.name, name))
}

/// The validator of a constraint (`FooValidator`) or the constraint of a
/// validator.
pub fn counterpart_class(index: &PhpIndex, class: &Symbol) -> Option<Symbol> {
    if !class.is_class_like() {
        return None;
    }
    let snapshot = index.semantic_snapshot();
    let fully_qualified: &str = &class.fully_qualified;
    let class_type = Type::named(fully_qualified, Vec::new());
    let relations = snapshot.relations();
    if relations.is_subtype(&class_type, &Type::named(CONSTRAINT_CLASS, Vec::new())) {
        return index.find_class(&format!("{fully_qualified}Validator"));
    }
    if relations.is_subtype(
        &class_type,
        &Type::named(CONSTRAINT_VALIDATOR_CLASS, Vec::new()),
    ) && let Some(constraint) = fully_qualified.strip_suffix("Validator")
    {
        return index.find_class(constraint);
    }
    None
}

#[cfg(test)]
mod tests;
