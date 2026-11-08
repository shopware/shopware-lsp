//! Implementations and type hierarchy (port of `hierarchy.go`).

use std::collections::{HashSet, VecDeque};

use serde_json::json;
use swlsp_lsp::{ImplementationRequest, TypeHierarchyPrepareRequest};
use swlsp_lsp_protocol::{Location, SymbolKind, TypeHierarchyItem};
use swlsp_php_semantic::{Snapshot, Symbol, SymbolKind as PhpSymbolKind};
use swlsp_php_types::go_to_lower;

use crate::Provider;
use crate::completion::request_offset;
use crate::helpers::{LocationCache, is_php, locations_for_symbols};

impl Provider {
    pub(crate) fn implementation(&self, request: &ImplementationRequest) -> Vec<Location> {
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        if !is_php(&request.params.text_document.uri) {
            return Vec::new();
        }
        let Some(context) = self.php_context(request) else {
            return Vec::new();
        };
        let offset = request_offset(line_index, request.params.position);
        let Some(symbol) = swlsp_php::symbol_at(&context.document, &context.snapshot, offset)
        else {
            return Vec::new();
        };
        let implementations = implementation_symbols(&context.snapshot, symbol);
        locations_for_symbols(&implementations, request.document.as_deref())
    }

    pub(crate) fn prepare_hierarchy(
        &self,
        request: &TypeHierarchyPrepareRequest,
    ) -> Vec<TypeHierarchyItem> {
        let Some(line_index) = request.line_index() else {
            return Vec::new();
        };
        if !is_php(&request.params.text_document.uri) {
            return Vec::new();
        }
        let Some(context) = self.php_context(request) else {
            return Vec::new();
        };
        let offset = request_offset(line_index, request.params.position);
        let Some(symbol) = swlsp_php::symbol_at(&context.document, &context.snapshot, offset)
        else {
            return Vec::new();
        };
        if !symbol.is_class_like() {
            return Vec::new();
        }
        vec![type_hierarchy_item(
            &mut LocationCache::new(request.document.as_deref()),
            &symbol,
        )]
    }

    pub(crate) fn hierarchy_supertypes(&self, item: &TypeHierarchyItem) -> Vec<TypeHierarchyItem> {
        let Some(index) = &self.index else {
            return Vec::new();
        };
        let snapshot = index.semantic_snapshot();
        let Some(symbol) = hierarchy_item_symbol(&snapshot, item) else {
            return Vec::new();
        };
        type_hierarchy_items(
            &mut LocationCache::new(None),
            &snapshot.direct_supertypes(symbol.id.as_str()),
        )
    }

    pub(crate) fn hierarchy_subtypes(&self, item: &TypeHierarchyItem) -> Vec<TypeHierarchyItem> {
        let Some(index) = &self.index else {
            return Vec::new();
        };
        let snapshot = index.semantic_snapshot();
        let Some(symbol) = hierarchy_item_symbol(&snapshot, item) else {
            return Vec::new();
        };
        let symbols = if symbol.kind == PhpSymbolKind::Trait {
            snapshot.trait_consumers(&symbol.fully_qualified)
        } else {
            snapshot.direct_subtypes(&symbol.fully_qualified)
        };
        type_hierarchy_items(&mut LocationCache::new(None), &symbols)
    }
}

fn implementation_symbols(snapshot: &Snapshot, symbol: Symbol) -> Vec<Symbol> {
    if symbol.kind == PhpSymbolKind::Method {
        return snapshot.method_overrides(symbol.id.as_str());
    }
    if !symbol.is_class_like() {
        return Vec::new();
    }
    let symbol = snapshot
        .class_alias_target(symbol.id.as_str())
        .unwrap_or(symbol);
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    let mut append = |candidate: Symbol| {
        if candidate.id == symbol.id || !seen.insert(candidate.id.clone()) {
            return;
        }
        result.push(candidate);
    };
    if symbol.kind == PhpSymbolKind::Trait {
        for consumer in snapshot.trait_consumers(&symbol.fully_qualified) {
            append(consumer);
        }
        return result;
    }
    let mut queue = VecDeque::from([symbol.fully_qualified.to_string()]);
    let mut visited_names = HashSet::new();
    while let Some(name) = queue.pop_front() {
        if !visited_names.insert(go_to_lower(name.strip_prefix('\\').unwrap_or(&name))) {
            continue;
        }
        for subtype in snapshot.direct_subtypes(&name) {
            queue.push_back(subtype.fully_qualified.to_string());
            append(subtype);
        }
        for alias in snapshot.class_aliases(&name) {
            append(alias);
        }
    }
    result
}

fn type_hierarchy_items(
    cache: &mut LocationCache<'_>,
    symbols: &[Symbol],
) -> Vec<TypeHierarchyItem> {
    symbols
        .iter()
        .map(|symbol| type_hierarchy_item(cache, symbol))
        .collect()
}

fn type_hierarchy_item(cache: &mut LocationCache<'_>, symbol: &Symbol) -> TypeHierarchyItem {
    let full = cache.text_range(&symbol.path, symbol.range);
    let selection = cache.text_range(&symbol.path, symbol.selection_range);
    TypeHierarchyItem {
        name: symbol.name.to_string(),
        kind: hierarchy_symbol_kind(symbol.kind),
        detail: symbol.fully_qualified.to_string(),
        uri: full.uri,
        range: full.range,
        selection_range: selection.range,
        data: Some(json!({
            "symbol": symbol.id.as_str(),
            "name": &*symbol.fully_qualified,
        })),
    }
}

fn hierarchy_item_symbol(snapshot: &Snapshot, item: &TypeHierarchyItem) -> Option<Symbol> {
    let data = item.data.as_ref().and_then(|data| data.as_object());
    if let Some(id) = data
        .and_then(|data| data.get("symbol"))
        .and_then(|value| value.as_str())
        .filter(|id| !id.is_empty())
        && let Some(symbol) = snapshot.symbol(id)
    {
        return Some(symbol);
    }
    let name = data
        .and_then(|data| data.get("name"))
        .and_then(|value| value.as_str())
        .filter(|name| !name.is_empty())
        .unwrap_or(&item.detail);
    snapshot.classes(name).into_iter().next()
}

fn hierarchy_symbol_kind(kind: PhpSymbolKind) -> SymbolKind {
    match kind {
        PhpSymbolKind::Interface => SymbolKind::INTERFACE,
        PhpSymbolKind::Enum => SymbolKind::ENUM,
        PhpSymbolKind::Trait => SymbolKind::STRUCT,
        _ => SymbolKind::CLASS,
    }
}
