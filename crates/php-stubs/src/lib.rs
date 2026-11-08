//! Version-aware semantic declarations for PHP runtime symbols that do not
//! exist in project source (port of `internal/php/stubs`).
//!
//! Declarations come from the catalog generated from JetBrains'
//! phpstorm-stubs by the Go generator (`cmd/generate_phpstorm_stubs`, which
//! needs the not-yet-ported binder). Rust embeds the same
//! `phpstorm-stubs.msgpack` artifact and overlays a small hand-written set
//! of refined declarations, exactly like Go.

pub mod catalog;
mod extensions;
mod overlay;

#[cfg(test)]
mod tests;

use std::collections::HashMap;
use std::sync::LazyLock;

use swlsp_php_project::Version;
use swlsp_php_semantic::{
    Document, Flags, Parameter, Str, Symbol, SymbolId, SymbolKind, TemplateParameter, Visibility,
    new_symbol_id,
};
use swlsp_php_types::{Type, equal_fold, go_to_lower};

pub use extensions::selected_extensions;

use catalog::Catalog;

/// The embedded generator output. Keep this path in sync with the Go
/// `go:embed`; move the artifact into this crate before deleting the Go
/// tree.
static CATALOG_DATA: &[u8] = include_bytes!("../data/phpstorm-stubs.msgpack");

static CATALOG: LazyLock<Catalog> = LazyLock::new(|| {
    Catalog::decode_static(CATALOG_DATA).unwrap_or_else(|error| panic!("{error}"))
});

/// Lowercased global symbol name → normalized owning extension.
static EXTENSION_SYMBOLS: LazyLock<HashMap<String, String>> = LazyLock::new(|| {
    let symbols = &generated_catalog().extension_symbols;
    let mut result = HashMap::with_capacity(symbols.len());
    for symbol in symbols {
        if symbol.extension.is_empty() || symbol.name.is_empty() {
            continue;
        }
        result.insert(
            go_to_lower(&symbol.name),
            swlsp_php_project::normalize_extension(&symbol.extension),
        );
    }
    result
});

/// The decoded embedded catalog header. Extension bundles stay encoded
/// until materialized.
pub fn generated_catalog() -> &'static Catalog {
    &CATALOG
}

/// The phpstorm-stubs repository and commit the catalog was generated from.
pub fn generated_source() -> (&'static str, &'static str) {
    let catalog = generated_catalog();
    (catalog.repository.as_str(), catalog.commit.as_str())
}

/// Every generated dynamic call contract.
pub fn generated_contracts() -> Vec<swlsp_php_semantic::CallContract> {
    generated_catalog().materialize_contracts()
}

/// The generated runtime bundle owning a global or class-member symbol, even
/// when that bundle is not materialized for the current project.
pub fn extension_for_symbol(name: &str) -> Option<&'static str> {
    let key = go_to_lower(name.strip_prefix('\\').unwrap_or(name));
    let extensions = &*EXTENSION_SYMBOLS;
    if let Some(extension) = extensions.get(&key) {
        return Some(extension.as_str());
    }
    let separator = key.find("::")?;
    extensions.get(&key[..separator]).map(String::as_str)
}

/// The runtime declarations of every extension for `version`.
pub fn document(version: Version) -> Document {
    build_document(version, None)
}

/// The runtime declarations of the always-loaded bundles plus the requested
/// extensions and their dependencies, minus disabled ones.
pub fn document_for_extensions<S: AsRef<str>>(
    version: Version,
    extensions: &[S],
    disabled: &[&[S]],
) -> Document {
    let selected = selected_extensions(extensions, disabled);
    build_document(version, Some(&selected))
}

fn build_document(version: Version, extensions: Option<&[String]>) -> Document {
    let catalog = generated_catalog();
    let (path, generated) = match extensions {
        None => {
            let path = format!("phpstub://{version}/core");
            let symbols = catalog.materialize(version, &path);
            (path, symbols)
        }
        Some(extensions) => {
            let path = format!("phpstub://{version}/selected");
            let symbols = catalog.materialize_for_extensions(version, &path, Some(extensions));
            (path, symbols)
        }
    };
    let mut builder = StubBuilder::new(&path, generated);
    overlay::apply(&mut builder, version);
    let symbols = match extensions {
        None => builder.symbols,
        Some(extensions) => extensions::filter_stub_symbols(builder.symbols, extensions),
    };
    Document {
        path: path.into(),
        symbols,
        call_contracts: catalog.materialize_contracts_for_extensions(extensions),
        ..Document::default()
    }
}

/// Merges hand-written declarations into the generated ones. A declaration
/// with the same kind and case-insensitive name replaces the generated one
/// in place.
pub(crate) struct StubBuilder {
    path: Str,
    symbols: Vec<Symbol>,
    index: HashMap<(SymbolKind, String), usize>,
    /// Positions of every symbol carrying an ID, in order.
    by_id: HashMap<SymbolId, Vec<usize>>,
}

fn stub_symbol_key(kind: SymbolKind, fully_qualified: &str) -> (SymbolKind, String) {
    (
        kind,
        go_to_lower(
            fully_qualified
                .strip_prefix('\\')
                .unwrap_or(fully_qualified),
        ),
    )
}

impl StubBuilder {
    fn new(path: &str, symbols: Vec<Symbol>) -> Self {
        let mut index = HashMap::with_capacity(symbols.len());
        let mut by_id: HashMap<SymbolId, Vec<usize>> = HashMap::with_capacity(symbols.len());
        for (position, symbol) in symbols.iter().enumerate() {
            index.insert(
                stub_symbol_key(symbol.kind, &symbol.fully_qualified),
                position,
            );
            by_id.entry(symbol.id.clone()).or_default().push(position);
        }
        StubBuilder {
            path: path.into(),
            symbols,
            index,
            by_id,
        }
    }

    fn positions(&self, id: &SymbolId) -> Vec<usize> {
        self.by_id.get(id).cloned().unwrap_or_default()
    }

    pub(crate) fn class(&mut self, name: &str, kind: SymbolKind) -> SymbolId {
        if let Some(&position) = self.index.get(&stub_symbol_key(kind, name)) {
            return self.symbols[position].id.clone();
        }
        let id = new_symbol_id(kind, name, &self.path, 0);
        self.put(Symbol {
            id: id.clone(),
            kind,
            name: name.into(),
            fully_qualified: name.into(),
            path: self.path.clone(),
            flags: Flags::INTERNAL,
            ..Symbol::default()
        });
        id
    }

    pub(crate) fn class_extends(&mut self, name: &str, kind: SymbolKind, parent: &str) -> SymbolId {
        let id = self.class(name, kind);
        for position in self.positions(&id) {
            if kind == SymbolKind::Interface {
                self.symbols[position].set_implements(vec![parent.into()]);
            } else {
                self.symbols[position].set_extends(vec![parent.into()]);
            }
        }
        id
    }

    pub(crate) fn add_implements(&mut self, id: &SymbolId, names: &[&str]) {
        for position in self.positions(id) {
            let symbol = &mut self.symbols[position];
            let mut implemented = symbol.implements().to_vec();
            for name in names {
                if !implemented.iter().any(|current| equal_fold(current, name)) {
                    implemented.push((*name).into());
                }
            }
            symbol.set_implements(implemented);
        }
    }

    pub(crate) fn set_templates(&mut self, id: &SymbolId, templates: Vec<TemplateParameter>) {
        for position in self.positions(id) {
            self.symbols[position].set_templates(templates.clone());
        }
    }

    fn symbol_name(&self, id: &SymbolId) -> Str {
        self.by_id
            .get(id)
            .and_then(|positions| positions.first())
            .map_or_else(Str::default, |&position| {
                self.symbols[position].fully_qualified.clone()
            })
    }

    fn member_position(&self, container: &SymbolId, name: &str) -> Option<usize> {
        let fqn = format!("{}::{name}", self.symbol_name(container));
        self.index
            .get(&stub_symbol_key(SymbolKind::Method, &fqn))
            .copied()
    }

    pub(crate) fn method(
        &mut self,
        container: &SymbolId,
        name: &str,
        result: Type,
        parameters: Vec<Parameter>,
    ) {
        let fqn = format!("{}::{name}", self.symbol_name(container));
        self.put(Symbol {
            id: new_symbol_id(SymbolKind::Method, &fqn, &self.path, 0),
            kind: SymbolKind::Method,
            name: name.into(),
            fully_qualified: fqn.into(),
            container: container.clone(),
            path: self.path.clone(),
            visibility: Visibility::Public,
            flags: Flags::INTERNAL,
            return_type: result.clone(),
            native_type: result,
            parameters,
            ..Symbol::default()
        });
    }

    pub(crate) fn method_with_templates(
        &mut self,
        container: &SymbolId,
        name: &str,
        result: Type,
        templates: Vec<TemplateParameter>,
        parameters: Vec<Parameter>,
    ) {
        self.method(container, name, result, parameters);
        if let Some(position) = self.member_position(container, name) {
            self.symbols[position].set_templates(templates);
        }
    }

    pub(crate) fn static_method(
        &mut self,
        container: &SymbolId,
        name: &str,
        result: Type,
        parameters: Vec<Parameter>,
    ) {
        self.method(container, name, result, parameters);
        if let Some(position) = self.member_position(container, name) {
            self.symbols[position].flags |= Flags::STATIC;
        }
    }

    pub(crate) fn static_method_with_templates(
        &mut self,
        container: &SymbolId,
        name: &str,
        result: Type,
        templates: Vec<TemplateParameter>,
        parameters: Vec<Parameter>,
    ) {
        self.method_with_templates(container, name, result, templates, parameters);
        if let Some(position) = self.member_position(container, name) {
            self.symbols[position].flags |= Flags::STATIC;
        }
    }

    pub(crate) fn class_constant(&mut self, container: &SymbolId, name: &str, value: Type) {
        let fqn = format!("{}::{name}", self.symbol_name(container));
        self.put(Symbol {
            id: new_symbol_id(SymbolKind::ClassConstant, &fqn, &self.path, 0),
            kind: SymbolKind::ClassConstant,
            name: name.into(),
            fully_qualified: fqn.into(),
            container: container.clone(),
            path: self.path.clone(),
            visibility: Visibility::Public,
            flags: Flags::INTERNAL,
            ty: value.clone(),
            native_type: value,
            ..Symbol::default()
        });
    }

    pub(crate) fn property(&mut self, container: &SymbolId, name: &str, value: Type) {
        self.property_visibility(container, name, value, Visibility::Public);
    }

    pub(crate) fn property_visibility(
        &mut self,
        container: &SymbolId,
        name: &str,
        value: Type,
        visibility: Visibility,
    ) {
        let fqn = format!("{}::${name}", self.symbol_name(container));
        self.put(Symbol {
            id: new_symbol_id(SymbolKind::Property, &fqn, &self.path, 0),
            kind: SymbolKind::Property,
            name: name.into(),
            fully_qualified: fqn.into(),
            container: container.clone(),
            path: self.path.clone(),
            visibility,
            flags: Flags::INTERNAL,
            ty: value.clone(),
            native_type: value,
            ..Symbol::default()
        });
    }

    pub(crate) fn function(&mut self, name: &str, result: Type, parameters: Vec<Parameter>) {
        self.put(Symbol {
            id: new_symbol_id(SymbolKind::Function, name, &self.path, 0),
            kind: SymbolKind::Function,
            name: name.into(),
            fully_qualified: name.into(),
            path: self.path.clone(),
            flags: Flags::INTERNAL,
            return_type: result.clone(),
            native_type: result,
            parameters,
            ..Symbol::default()
        });
    }

    fn put(&mut self, symbol: Symbol) -> usize {
        let key = stub_symbol_key(symbol.kind, &symbol.fully_qualified);
        if let Some(&position) = self.index.get(&key) {
            let previous = std::mem::replace(&mut self.symbols[position], symbol);
            if previous.id != self.symbols[position].id {
                self.forget_id(&previous.id, position);
                let id = self.symbols[position].id.clone();
                self.remember_id(id, position);
            }
            return position;
        }
        let position = self.symbols.len();
        let id = symbol.id.clone();
        self.symbols.push(symbol);
        self.index.insert(key, position);
        self.remember_id(id, position);
        position
    }

    fn remember_id(&mut self, id: SymbolId, position: usize) {
        let positions = self.by_id.entry(id).or_default();
        let at = positions.partition_point(|&current| current < position);
        positions.insert(at, position);
    }

    fn forget_id(&mut self, id: &SymbolId, position: usize) {
        if let Some(positions) = self.by_id.get_mut(id) {
            positions.retain(|&current| current != position);
        }
    }
}

pub(crate) fn parameter(name: &str, value: Type, optional: bool) -> Parameter {
    Parameter {
        name: name.into(),
        ty: value.clone(),
        native_type: value,
        optional,
        ..Parameter::default()
    }
}

pub(crate) fn variadic(name: &str, value: Type) -> Parameter {
    Parameter {
        name: name.into(),
        ty: value.clone(),
        native_type: value,
        flags: Flags::VARIADIC,
        ..Parameter::default()
    }
}

pub(crate) fn by_reference(name: &str, value: Type, optional: bool) -> Parameter {
    Parameter {
        name: name.into(),
        ty: value.clone(),
        native_type: value,
        flags: Flags::BY_REFERENCE,
        optional,
        ..Parameter::default()
    }
}

pub(crate) fn template(name: &str, bound: Type, default: Type) -> TemplateParameter {
    TemplateParameter {
        name: name.into(),
        bound,
        default,
        ..TemplateParameter::default()
    }
}
