//! Ports of the Go `internal/php/semantic` tests plus Rust layout tests.

mod document;
mod hierarchy;
mod lazy_returns;
mod references;
mod snapshot;
mod store;
mod symbols;
mod workspace;

use swlsp_php_types::Type;

pub(crate) use crate::str::Str;
use swlsp_syntax::TextRange;

use crate::*;

pub(crate) fn range(start: u32, end: u32) -> TextRange {
    TextRange::new(start.into(), end.into())
}

pub(crate) fn names(values: &[&str]) -> Vec<Str> {
    values.iter().map(|value| Str::from(*value)).collect()
}

pub(crate) fn symbol(
    id: &str,
    kind: SymbolKind,
    name: &str,
    fully_qualified: &str,
    path: &str,
) -> Symbol {
    Symbol {
        id: id.into(),
        kind,
        name: name.into(),
        fully_qualified: fully_qualified.into(),
        path: path.into(),
        ..Symbol::default()
    }
}

pub(crate) fn member(
    id: &str,
    kind: SymbolKind,
    name: &str,
    container: &str,
    path: &str,
) -> Symbol {
    Symbol {
        container: container.into(),
        ..symbol(id, kind, name, "", path)
    }
}

pub(crate) fn document(path: &str, symbols: Vec<Symbol>) -> Document {
    Document {
        path: path.into(),
        symbols,
        ..Document::default()
    }
}

pub(crate) fn reference_with_targets(
    mut reference: Reference,
    qualified: &[&str],
    candidates: &[&str],
) -> Reference {
    reference.set_qualified_names(names(qualified));
    reference.set_candidate_ids(candidates.iter().map(|id| SymbolId::from(*id)).collect());
    reference
}

pub(crate) fn named(name: &str) -> Type {
    Type::named(name, Vec::new())
}

pub(crate) fn ids(symbols: &[Symbol]) -> Vec<String> {
    symbols.iter().map(|symbol| symbol.id.to_string()).collect()
}

pub(crate) fn fully_qualified(symbols: &[Symbol]) -> Vec<String> {
    symbols
        .iter()
        .map(|symbol| symbol.fully_qualified.to_string())
        .collect()
}
