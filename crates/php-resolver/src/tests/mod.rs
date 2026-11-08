//! Ports of the Go `internal/php/resolver` tests.

mod members;
mod names;
mod signatures;

use std::collections::HashMap;

use swlsp_php_semantic::{
    Document, Parameter, Str, Symbol, SymbolKind, TemplateParameter, TraitAlias,
};
use swlsp_php_types::{Hierarchy, Type};

pub(crate) fn sym(id: &str, kind: SymbolKind, name: &str, fully_qualified: &str) -> Symbol {
    Symbol {
        id: id.into(),
        kind,
        name: name.into(),
        fully_qualified: fully_qualified.into(),
        ..Symbol::default()
    }
}

pub(crate) fn member(
    id: &str,
    kind: SymbolKind,
    name: &str,
    fully_qualified: &str,
    container: &str,
) -> Symbol {
    Symbol {
        container: container.into(),
        ..sym(id, kind, name, fully_qualified)
    }
}

pub(crate) fn with_templates(mut symbol: Symbol, templates: Vec<TemplateParameter>) -> Symbol {
    symbol.set_templates(templates);
    symbol
}

pub(crate) fn template(name: &str) -> TemplateParameter {
    TemplateParameter {
        name: name.into(),
        ..TemplateParameter::default()
    }
}

pub(crate) fn template_with(name: &str, bound: Type, default: Type) -> TemplateParameter {
    TemplateParameter {
        name: name.into(),
        bound,
        default,
        ..TemplateParameter::default()
    }
}

pub(crate) fn strs(values: &[&str]) -> Vec<Str> {
    values.iter().map(|value| Str::from(*value)).collect()
}

#[allow(clippy::too_many_arguments)]
pub(crate) fn with_hierarchy(
    mut symbol: Symbol,
    extends: &[&str],
    implements: &[&str],
    traits: &[&str],
    extends_types: Vec<Type>,
    aliases: Vec<TraitAlias>,
) -> Symbol {
    symbol.set_hierarchy(
        strs(extends),
        strs(implements),
        strs(traits),
        extends_types,
        Vec::new(),
        Vec::new(),
        aliases,
    );
    symbol
}

pub(crate) fn param(name: &str, ty: Type) -> Parameter {
    Parameter {
        name: name.into(),
        ty,
        ..Parameter::default()
    }
}

pub(crate) fn document(path: &str, symbols: Vec<Symbol>) -> Document {
    Document {
        path: path.into(),
        symbols: symbols
            .into_iter()
            .map(|mut symbol| {
                if symbol.path.is_empty() {
                    symbol.path = path.into();
                }
                symbol
            })
            .collect(),
        ..Document::default()
    }
}

pub(crate) fn named(name: &str) -> Type {
    Type::named(name, Vec::new())
}

pub(crate) fn generic(name: &str, arguments: Vec<Type>) -> Type {
    Type::named(name, arguments)
}

/// Parent links by class name.
pub(crate) struct TestHierarchy(pub HashMap<&'static str, &'static str>);

impl Hierarchy for TestHierarchy {
    fn is_subtype_of(&self, candidate: &str, target: &str) -> bool {
        let mut candidate = candidate;
        while !candidate.is_empty() {
            if candidate == target {
                return true;
            }
            candidate = self.0.get(candidate).copied().unwrap_or("");
        }
        false
    }
}
