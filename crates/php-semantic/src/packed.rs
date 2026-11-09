//! The compact retained declaration graph of one source file.
//!
//! Published workspace snapshots keep only [`PackedDocument`]s. Core
//! declaration strings are [`Atom`]s of the document's generation, ranges and modifiers are
//! bit-packed, and infrequent collections live in shared immutable side
//! records. Rare values that do not fit the compact encoding fall back to
//! exact sparse side tables, so packing is lossless.

use std::cell::RefCell;
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::{Arc, OnceLock};

use swlsp_php_types::Type;

use crate::str::Str;
use swlsp_syntax::TextRange;

use crate::atom::{Atom, Generation, empty_name, hash_str, target_generation, with_generation};
use crate::call_contract::{CallContract, clone_call_contracts};
use crate::document::{Document, NameKind, Reference};
use crate::references::PackedReferences;
use crate::symbol::{
    Attribute, AttributeValue, Flags, Parameter, Shared, SignatureExtras, Symbol, SymbolHierarchy,
    SymbolId, SymbolKind, SymbolMetadata, Visibility, write_lower_identifier,
    write_named_symbol_id,
};
use crate::workspace::WorkspaceGraphLoader;

/// Lazily loaded signature and metadata payload of a summary graph.
pub(crate) struct LazyDetails {
    pub loader: Arc<dyn WorkspaceGraphLoader>,
    pub pinned: OnceLock<Arc<crate::workspace::WorkspaceGraph>>,
}

pub(crate) struct PackedDocument {
    pub path: Str,
    pub version: i64,
    pub workspace_revision: u64,
    pub namespace: Str,
    pub symbols: Box<[PackedSymbol]>,
    /// Exact ranges of symbols whose ranges do not fit the compact encoding,
    /// sorted by symbol index.
    pub range_extras: Box<[(u32, [TextRange; 3])]>,
    /// Exact types of symbols with more than two distinct non-unknown types,
    /// sorted by symbol index.
    pub type_extras: Box<[(u32, [Type; 4])]>,
    pub references: PackedReferences,
    pub call_contracts: Box<[CallContract]>,
    pub lazy: Option<LazyDetails>,
    /// The interner generation of every atom in this document; keeps it
    /// alive while the document is reachable.
    pub generation: Arc<Generation>,
}

/// Signature and metadata side record of a function-like or annotated
/// declaration. Shared between the packed graph and decoded summaries.
#[derive(Debug, Default, PartialEq)]
pub(crate) struct SymbolDetail {
    pub parameters: Box<[PackedParameter]>,
    pub signature: Shared<SignatureExtras>,
    pub metadata: Shared<SymbolMetadata>,
}

/// 72 bytes; see the module documentation.
///
/// Declaration IDs that [`crate::new_symbol_id`] derives from the kind and
/// fully qualified name (almost all of them) are not stored: `id` caches
/// the atom of a derived ID once a caller needs it as a string, so IDs that
/// are only hashed and compared while indexing never enter the atom table.
/// Member names of the form `<owner>::<name>` (`::$<name>` for properties)
/// store only the owner, which is the class name or container ID that is
/// interned anyway.
pub(crate) struct PackedSymbol {
    /// The stored ID atom, or the cached atom of a derived ID (0 until
    /// first use).
    id: AtomicU32,
    pub name: Atom,
    /// The fully qualified name, or its owner prefix (`MEMBER_NAME`).
    fully_qualified: Atom,
    pub container: Atom,
    start: u32,
    end: u32,
    selection: u32,
    body: u32,
    types: [Type; 2],
    pub hierarchy: Option<Arc<SymbolHierarchy>>,
    pub detail: Option<Arc<SymbolDetail>>,
    /// flags(13) | kind(4) | visibility(2) | write visibility(2) |
    /// has-write-visibility(1) | lazy-detail(1) | range-extra(1) |
    /// type selectors(8).
    bits: u32,
    /// `DERIVED_ID` | `MEMBER_NAME` | `INFERRED_RETURN`; lives in what was
    /// padding.
    derived: u8,
}

const DERIVED_ID: u8 = 1;
const MEMBER_NAME: u8 = 2;
/// [`Flags::INFERRED_RETURN`], which does not fit the 13 flag bits.
const INFERRED_RETURN: u8 = 4;

const FLAG_BITS: u32 = 13;
const FLAG_MASK: u32 = (1 << FLAG_BITS) - 1;
const KIND_SHIFT: u32 = 13;
const VISIBILITY_SHIFT: u32 = 17;
const WRITE_VISIBILITY_SHIFT: u32 = 19;
const HAS_WRITE_VISIBILITY: u32 = 1 << 21;
const LAZY_DETAIL: u32 = 1 << 22;
const RANGE_EXTRA: u32 = 1 << 23;
const SELECTOR_SHIFT: u32 = 24;

/// Type field selectors: unknown, first slot, second slot, side table.
const TYPE_UNKNOWN: u32 = 0;
const TYPE_SLOT0: u32 = 1;
const TYPE_SLOT1: u32 = 2;
const TYPE_EXTRA: u32 = 3;

const EMPTY_SUBRANGE: u32 = u32::MAX;

fn range_parts(range: TextRange) -> (u32, u32) {
    (range.start().into(), range.end().into())
}

fn text_range(start: u32, end: u32) -> TextRange {
    if end < start {
        // TextRange cannot represent inverted ranges; the binder never
        // produces them for declarations.
        return TextRange::new(start.into(), start.into());
    }
    TextRange::new(start.into(), end.into())
}

fn compact_subrange(start: u32, range: TextRange) -> Option<u32> {
    if range == TextRange::default() {
        return Some(EMPTY_SUBRANGE);
    }
    let (sub_start, sub_end) = range_parts(range);
    let offset = sub_start.checked_sub(start)?;
    let length = sub_end - sub_start;
    if offset >= u32::from(u16::MAX) || length > u32::from(u16::MAX) {
        return None;
    }
    Some(offset << 16 | length)
}

fn expand_subrange(start: u32, packed: u32) -> TextRange {
    if packed == EMPTY_SUBRANGE {
        return TextRange::default();
    }
    let sub_start = start + (packed >> 16);
    text_range(sub_start, sub_start + (packed & 0xFFFF))
}

thread_local! {
    /// Reusable buffers for deriving IDs; a pool because comparisons nest.
    static ID_BUFFERS: RefCell<Vec<String>> = const { RefCell::new(Vec::new()) };
}

fn with_id_buffer<R>(write: impl FnOnce(&mut String), use_id: impl FnOnce(&str) -> R) -> R {
    let mut buffer = ID_BUFFERS
        .with(|buffers| buffers.borrow_mut().pop())
        .unwrap_or_default();
    buffer.clear();
    write(&mut buffer);
    let result = use_id(&buffer);
    ID_BUFFERS.with(|buffers| buffers.borrow_mut().push(buffer));
    result
}

/// How a packed declaration's ID is represented.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum PackedId {
    /// Derived from the kind and fully qualified name.
    Derived,
    Stored(Atom),
}

impl PackedId {
    /// `id` as stored for a declaration with `kind` and `fully_qualified`,
    /// interning it only when it cannot be derived.
    pub fn new(id: &str, kind: SymbolKind, fully_qualified: &str) -> PackedId {
        if PackedId::derivable(id, kind, fully_qualified) {
            PackedId::Derived
        } else {
            PackedId::Stored(Atom::intern(id))
        }
    }

    pub fn derivable(id: &str, kind: SymbolKind, fully_qualified: &str) -> bool {
        !fully_qualified.is_empty()
            && with_id_buffer(
                |buffer| write_named_symbol_id(buffer, kind, fully_qualified),
                |derived| derived == id,
            )
    }
}

/// How a packed declaration's fully qualified name is represented.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum PackedName {
    Stored(Atom),
    /// `<owner>::<name>`, or `<owner>::$<name>` for a property.
    Member(Atom),
}

impl PackedName {
    /// The representation of `fully_qualified` for a declaration; members
    /// whose name is derivable store only their owner prefix.
    pub fn new(fully_qualified: &str, kind: SymbolKind, name: &str, container: &str) -> PackedName {
        match PackedName::member_owner(fully_qualified, kind, name, container) {
            Some(owner) => PackedName::Member(Atom::intern(owner)),
            None => PackedName::Stored(Atom::intern(fully_qualified)),
        }
    }

    pub fn member_owner<'a>(
        fully_qualified: &'a str,
        kind: SymbolKind,
        name: &str,
        container: &str,
    ) -> Option<&'a str> {
        if container.is_empty() || !kind.is_member() || name.is_empty() {
            return None;
        }
        let owner = fully_qualified
            .strip_suffix(name)?
            .strip_suffix(member_separator(kind))?;
        (!owner.is_empty()).then_some(owner)
    }
}

fn member_separator(kind: SymbolKind) -> &'static str {
    if kind == SymbolKind::Property {
        "::$"
    } else {
        "::"
    }
}

impl PackedSymbol {
    fn has(&self, flag: u8) -> bool {
        self.derived & flag != 0
    }

    /// This symbol with its atoms in the pinned generation. Hierarchy and
    /// detail are re-interned by the document's canonicalization.
    fn rehomed(&self) -> PackedSymbol {
        let id = if self.has(DERIVED_ID) {
            // Re-derived (and re-interned) on demand.
            0
        } else {
            Atom::from_raw(self.id.load(Ordering::Relaxed))
                .in_target()
                .raw()
        };
        PackedSymbol {
            id: AtomicU32::new(id),
            name: self.name.in_target(),
            fully_qualified: self.fully_qualified.in_target(),
            container: self.container.in_target(),
            start: self.start,
            end: self.end,
            selection: self.selection,
            body: self.body,
            types: self.types.clone(),
            hierarchy: self.hierarchy.clone(),
            detail: self.detail.clone(),
            bits: self.bits,
            derived: self.derived,
        }
    }

    /// The fully qualified name. A derived member name is interned on each
    /// call that needs it; [`PackedSymbol::with_fully_qualified`] does not
    /// intern.
    pub fn fully_qualified(&self) -> Atom {
        if !self.has(MEMBER_NAME) {
            return self.fully_qualified;
        }
        self.with_fully_qualified(|name| self.fully_qualified.intern_like(name))
    }

    /// The stored name of a non-member declaration (name indexes hold only
    /// global declarations, whose names are never derived).
    pub fn global_name(&self) -> &'static str {
        debug_assert!(!self.has(MEMBER_NAME));
        self.fully_qualified.as_str()
    }

    pub fn with_fully_qualified<R>(&self, use_name: impl FnOnce(&str) -> R) -> R {
        if !self.has(MEMBER_NAME) {
            return use_name(self.fully_qualified.as_str());
        }
        with_id_buffer(
            |buffer| {
                buffer.push_str(self.fully_qualified.as_str());
                buffer.push_str(member_separator(self.kind()));
                buffer.push_str(self.name.as_str());
            },
            use_name,
        )
    }

    /// The fully qualified name as an owned string; allocates for a derived
    /// member name instead of growing the atom table.
    pub fn fully_qualified_value(&self) -> Str {
        if !self.has(MEMBER_NAME) {
            return self.fully_qualified.name();
        }
        self.with_fully_qualified(|name| Str::from(name))
    }

    /// The stored ID or the cached derived one, without deriving.
    fn known_id(&self) -> Option<Atom> {
        let raw = self.id.load(Ordering::Relaxed);
        if !self.has(DERIVED_ID) || raw != 0 {
            return Some(Atom::from_raw(raw));
        }
        None
    }

    /// The interned ID. A derived ID is interned on first use.
    pub fn id(&self) -> Atom {
        if let Some(atom) = self.known_id() {
            return atom;
        }
        // A derived ID implies a non-empty fully qualified name (or owner),
        // whose generation is this document's.
        let atom = self.with_id(|id| self.fully_qualified.intern_like(id));
        self.id.store(atom.raw(), Ordering::Relaxed);
        atom
    }

    /// Calls `use_id` with the ID text without interning a derived ID.
    pub fn with_id<R>(&self, use_id: impl FnOnce(&str) -> R) -> R {
        match self.known_id() {
            Some(atom) => use_id(atom.as_str()),
            None => self.with_fully_qualified(|fully_qualified| {
                with_id_buffer(
                    |buffer| write_named_symbol_id(buffer, self.kind(), fully_qualified),
                    use_id,
                )
            }),
        }
    }

    /// The ID as an owned string; allocates for a derived ID that has not
    /// been interned instead of growing the atom table.
    pub fn id_value(&self) -> Str {
        match self.known_id() {
            Some(atom) => atom.name(),
            None => self.with_id(|id| Str::from(id)),
        }
    }

    pub fn id_eq(&self, value: &str) -> bool {
        self.with_id(|id| id == value)
    }

    pub fn id_hash(&self) -> u64 {
        self.with_id(hash_str)
    }

    pub fn same_id(&self, other: &PackedSymbol) -> bool {
        match (self.known_id(), other.known_id()) {
            (Some(left), Some(right)) => left == right,
            _ => self.with_id(|id| other.id_eq(id)),
        }
    }

    pub fn kind(&self) -> SymbolKind {
        SymbolKind::from_u8(((self.bits >> KIND_SHIFT) & 0xF) as u8).unwrap_or_default()
    }

    pub fn flags(&self) -> Flags {
        let mut flags = Flags(self.bits & FLAG_MASK);
        if self.derived & INFERRED_RETURN != 0 {
            flags.insert(Flags::INFERRED_RETURN);
        }
        flags
    }

    pub fn visibility(&self) -> Visibility {
        Visibility::from_u8(((self.bits >> VISIBILITY_SHIFT) & 0x3) as u8).unwrap_or_default()
    }

    pub fn write_visibility(&self) -> Visibility {
        Visibility::from_u8(((self.bits >> WRITE_VISIBILITY_SHIFT) & 0x3) as u8).unwrap_or_default()
    }

    pub fn has_write_visibility(&self) -> bool {
        self.bits & HAS_WRITE_VISIBILITY != 0
    }

    pub fn lazy_detail(&self) -> bool {
        self.bits & LAZY_DETAIL != 0
    }

    fn selector(&self, field: u32) -> u32 {
        (self.bits >> (SELECTOR_SHIFT + 2 * field)) & 0x3
    }

    pub fn has_range_extra(&self) -> bool {
        self.bits & RANGE_EXTRA != 0
    }

    pub fn has_type_extra(&self) -> bool {
        (0..4).any(|field| self.selector(field) == TYPE_EXTRA)
    }

    pub fn compact_range(&self) -> TextRange {
        text_range(self.start, self.end)
    }
}

impl PackedDocument {
    pub fn symbol(&self, index: u32) -> &PackedSymbol {
        &self.symbols[index as usize]
    }

    /// `[range, selection range, body range]` of a symbol.
    pub fn ranges(&self, index: u32) -> [TextRange; 3] {
        let symbol = self.symbol(index);
        if symbol.has_range_extra()
            && let Ok(position) = self
                .range_extras
                .binary_search_by_key(&index, |(key, _)| *key)
        {
            return self.range_extras[position].1;
        }
        [
            symbol.compact_range(),
            expand_subrange(symbol.start, symbol.selection),
            expand_subrange(symbol.start, symbol.body),
        ]
    }

    pub fn range(&self, index: u32) -> TextRange {
        self.ranges(index)[0]
    }

    /// `[type, native type, doc type, return type]` of a symbol.
    pub fn types(&self, index: u32) -> [Type; 4] {
        let symbol = self.symbol(index);
        if symbol.has_type_extra()
            && let Ok(position) = self
                .type_extras
                .binary_search_by_key(&index, |(key, _)| *key)
        {
            return self.type_extras[position].1.clone();
        }
        std::array::from_fn(|field| match symbol.selector(field as u32) {
            TYPE_SLOT0 => symbol.types[0].clone(),
            TYPE_SLOT1 => symbol.types[1].clone(),
            _ => Type::unknown(),
        })
    }

    /// The signature/metadata record of a symbol, loading a lazy summary's
    /// payload on demand. Load failures degrade to no details.
    pub fn detail(&self, index: u32) -> Option<Arc<SymbolDetail>> {
        let symbol = self.symbol(index);
        if let Some(detail) = &symbol.detail {
            return Some(detail.clone());
        }
        if !symbol.lazy_detail() {
            return None;
        }
        let full = self.full_graph().ok()?;
        let full_document = full.packed();
        let candidate = full_document
            .symbols
            .get(index as usize)
            .filter(|candidate| candidate.same_id(symbol))
            .or_else(|| {
                full_document
                    .symbols
                    .iter()
                    .find(|candidate| candidate.same_id(symbol))
            })?;
        candidate.detail.clone()
    }

    pub(crate) fn full_graph(
        &self,
    ) -> Result<Arc<crate::workspace::WorkspaceGraph>, crate::workspace::LoadError> {
        let Some(lazy) = &self.lazy else {
            return Err(crate::workspace::LoadError::new(format!(
                "load workspace graph {}: not lazy",
                self.path
            )));
        };
        if let Some(pinned) = lazy.pinned.get() {
            return Ok(pinned.clone());
        }
        let graph = lazy.loader.load_workspace_graph(&self.path)?;
        if graph.path() != &*self.path {
            return Err(crate::workspace::LoadError::new(format!(
                "load workspace graph {}: got path {}",
                self.path,
                graph.path()
            )));
        }
        // A repository mutation may have pinned the old generation while this
        // lookup was in flight; prefer that immutable payload.
        if let Some(pinned) = lazy.pinned.get() {
            return Ok(pinned.clone());
        }
        Ok(graph)
    }

    /// Pins the full persisted payload so this generation keeps serving it
    /// after the repository row is replaced or removed.
    pub(crate) fn pin_details(&self) -> Result<(), crate::workspace::LoadError> {
        let Some(lazy) = &self.lazy else {
            return Ok(());
        };
        if lazy.pinned.get().is_some() {
            return Ok(());
        }
        let graph = self.full_graph()?;
        let _ = lazy.pinned.set(graph);
        Ok(())
    }

    pub fn materialize_symbol(&self, index: u32) -> Symbol {
        let symbol = self.symbol(index);
        let [range, selection_range, body_range] = self.ranges(index);
        let [ty, native_type, doc_type, return_type] = self.types(index);
        let mut result = Symbol {
            id: SymbolId::new(symbol.id_value()),
            kind: symbol.kind(),
            name: symbol.name.name(),
            fully_qualified: symbol.fully_qualified_value(),
            container: SymbolId::from(symbol.container.name()),
            path: self.path.clone(),
            range,
            selection_range,
            body_range,
            visibility: symbol.visibility(),
            write_visibility: symbol.write_visibility(),
            has_write_visibility: symbol.has_write_visibility(),
            flags: symbol.flags(),
            ty,
            native_type,
            doc_type,
            return_type,
            parameters: Vec::new(),
            signature: Shared::default(),
            hierarchy: Shared::from_arc(symbol.hierarchy.clone()),
            metadata: Shared::default(),
        };
        if let Some(detail) = self.detail(index) {
            result.parameters = detail
                .parameters
                .iter()
                .map(|parameter| parameter.materialize(&result.id))
                .collect();
            result.signature = detail.signature.clone();
            result.metadata = detail.metadata.clone();
        }
        result
    }

    /// The complete public document view of this graph.
    pub fn materialize(&self) -> Document {
        Document {
            path: self.path.clone(),
            version: self.version,
            workspace_revision: self.workspace_revision,
            namespace: self.namespace.clone(),
            symbols: (0..self.symbols.len() as u32)
                .map(|index| self.materialize_symbol(index))
                .collect(),
            references: self.references.materialize(),
            call_contracts: self.call_contracts.to_vec(),
            ..Document::default()
        }
    }

    /// Packs a document. `workspace_only` keeps only workspace declaration
    /// kinds and non-variable references (the retained projection).
    pub fn pack(document: &Document, workspace_only: bool) -> PackedDocument {
        let generation = target_generation();
        with_generation(&generation.clone(), || {
            PackedDocument::pack_in(document, workspace_only, generation)
        })
    }

    fn pack_in(
        document: &Document,
        workspace_only: bool,
        generation: Arc<Generation>,
    ) -> PackedDocument {
        let mut builder = PackedBuilder::default();
        for symbol in &document.symbols {
            if !workspace_only || symbol.kind.is_workspace() {
                builder.add(symbol);
            }
        }
        let references: Vec<&Reference> = document
            .references
            .iter()
            .filter(|reference| !workspace_only || reference.kind != NameKind::Variable)
            .collect();
        PackedDocument {
            path: if document.path.is_empty() {
                empty_name()
            } else {
                document.path.clone()
            },
            version: document.version,
            // The generation a document was analyzed against is not part of
            // its workspace projection: equal analyses project equal graphs.
            workspace_revision: if workspace_only {
                0
            } else {
                document.workspace_revision
            },
            namespace: document.namespace.clone(),
            symbols: builder.symbols.into_boxed_slice(),
            range_extras: builder.range_extras.into_boxed_slice(),
            type_extras: builder.type_extras.into_boxed_slice(),
            references: PackedReferences::pack(references),
            call_contracts: clone_call_contracts(&document.call_contracts).into_boxed_slice(),
            lazy: None,
            generation,
        }
    }

    /// Whether this document's atoms live in `generation`.
    pub(crate) fn is_in(&self, generation: &Generation) -> bool {
        self.generation.same(generation)
    }

    /// A copy of this document whose atoms and interned names all live in
    /// `generation`, so the old generation can be freed once no snapshot
    /// uses this document any more.
    pub(crate) fn rehome(&self, generation: &Arc<Generation>) -> PackedDocument {
        with_generation(generation, || {
            let mut document = PackedDocument {
                path: self.path.clone(),
                version: self.version,
                workspace_revision: self.workspace_revision,
                namespace: self.namespace.clone(),
                symbols: self.symbols.iter().map(PackedSymbol::rehomed).collect(),
                range_extras: self.range_extras.clone(),
                type_extras: self.type_extras.clone(),
                references: self.references.rehomed(),
                call_contracts: clone_call_contracts(&self.call_contracts).into_boxed_slice(),
                lazy: self.lazy.as_ref().map(|lazy| LazyDetails {
                    loader: lazy.loader.clone(),
                    pinned: match lazy.pinned.get() {
                        Some(graph) => OnceLock::from(Arc::new(crate::workspace::WorkspaceGraph {
                            document: graph.document.rehome(generation),
                        })),
                        None => OnceLock::new(),
                    },
                }),
                generation: generation.clone(),
            };
            // Re-interns path, namespace, hierarchy, detail and contract
            // identifiers into the pinned generation; types stay shared.
            document.canonicalize(&mut crate::canon::Canon::identifiers_only());
            document
        })
    }

    /// Visits every type and name retained by this document so a batch
    /// canonicalizer can share equal values across documents.
    pub(crate) fn canonicalize(&mut self, canon: &mut crate::canon::Canon) {
        self.path = canon.identifier(&self.path);
        self.namespace = canon.identifier(&self.namespace);
        for symbol in self.symbols.iter_mut() {
            for slot in &mut symbol.types {
                *slot = canon.ty(slot);
            }
            if let Some(hierarchy) = &mut symbol.hierarchy {
                canon.hierarchy(hierarchy);
            }
            if let Some(detail) = &mut symbol.detail {
                canon.detail(detail);
            }
        }
        for (_, types) in self.type_extras.iter_mut() {
            for value in types.iter_mut() {
                *value = canon.ty(value);
            }
        }
        self.references.canonicalize(canon);
        for contract in self.call_contracts.iter_mut() {
            canon.call_contract(contract);
        }
    }
}

#[derive(Default)]
pub(crate) struct PackedBuilder {
    pub symbols: Vec<PackedSymbol>,
    pub range_extras: Vec<(u32, [TextRange; 3])>,
    pub type_extras: Vec<(u32, [Type; 4])>,
}

impl PackedBuilder {
    pub fn add(&mut self, source: &Symbol) {
        let detail = (!source.parameters.is_empty()
            || source.signature.get().is_some()
            || source.metadata.get().is_some())
        .then(|| {
            Arc::new(SymbolDetail {
                parameters: source
                    .parameters
                    .iter()
                    .map(|parameter| PackedParameter::pack(parameter, &source.id))
                    .collect(),
                signature: source.signature.clone(),
                metadata: source.metadata.clone(),
            })
        });
        self.push(PackedCore {
            id: PackedId::new(&source.id, source.kind, &source.fully_qualified),
            name: Atom::intern(&source.name),
            fully_qualified: PackedName::new(
                &source.fully_qualified,
                source.kind,
                &source.name,
                &source.container,
            ),
            container: Atom::intern(&source.container),
            ranges: [source.range, source.selection_range, source.body_range],
            types: [
                source.ty.clone(),
                source.native_type.clone(),
                source.doc_type.clone(),
                source.return_type.clone(),
            ],
            kind: source.kind,
            flags: source.flags,
            visibility: source.visibility,
            write_visibility: source.write_visibility,
            has_write_visibility: source.has_write_visibility,
            hierarchy: source.hierarchy.arc().cloned(),
            detail,
            lazy_detail: false,
        });
    }

    pub fn push(&mut self, core: PackedCore) {
        let index = self.symbols.len() as u32;
        let [range, selection, body] = core.ranges;
        let (start, end) = range_parts(range);
        let mut bits = (core.flags.0 & FLAG_MASK)
            | (core.kind as u32) << KIND_SHIFT
            | (core.visibility as u32) << VISIBILITY_SHIFT
            | (core.write_visibility as u32) << WRITE_VISIBILITY_SHIFT;
        if core.has_write_visibility {
            bits |= HAS_WRITE_VISIBILITY;
        }
        if core.lazy_detail {
            bits |= LAZY_DETAIL;
        }
        debug_assert_eq!(
            core.flags.0 & !(FLAG_MASK | Flags::INFERRED_RETURN.0),
            0,
            "flags exceed packed range"
        );
        let (selection_bits, body_bits) = match (
            compact_subrange(start, selection),
            compact_subrange(start, body),
        ) {
            (Some(selection), Some(body)) => (selection, body),
            _ => {
                bits |= RANGE_EXTRA;
                self.range_extras.push((index, core.ranges));
                (EMPTY_SUBRANGE, EMPTY_SUBRANGE)
            }
        };
        let mut slots = [Type::unknown(), Type::unknown()];
        let mut used = 0usize;
        let mut overflow = false;
        let mut selectors = [TYPE_UNKNOWN; 4];
        for (field, value) in core.types.iter().enumerate() {
            if value.is_unknown() {
                continue;
            }
            if let Some(slot) = slots[..used].iter().position(|slot| slot == value) {
                selectors[field] = if slot == 0 { TYPE_SLOT0 } else { TYPE_SLOT1 };
            } else if used < 2 {
                slots[used] = value.clone();
                selectors[field] = if used == 0 { TYPE_SLOT0 } else { TYPE_SLOT1 };
                used += 1;
            } else {
                overflow = true;
            }
        }
        if overflow {
            selectors = [TYPE_EXTRA; 4];
            slots = [Type::unknown(), Type::unknown()];
            self.type_extras.push((index, core.types));
        }
        for (field, selector) in selectors.iter().enumerate() {
            bits |= selector << (SELECTOR_SHIFT + 2 * field as u32);
        }
        let (id, mut derived) = match core.id {
            PackedId::Derived => (0, DERIVED_ID),
            PackedId::Stored(atom) => (atom.raw(), 0),
        };
        let fully_qualified = match core.fully_qualified {
            PackedName::Stored(atom) => atom,
            PackedName::Member(owner) => {
                derived |= MEMBER_NAME;
                owner
            }
        };
        if core.flags.has(Flags::INFERRED_RETURN) {
            derived |= INFERRED_RETURN;
        }
        self.symbols.push(PackedSymbol {
            id: AtomicU32::new(id),
            name: core.name,
            fully_qualified,
            container: core.container,
            start,
            end,
            selection: selection_bits,
            body: body_bits,
            types: slots,
            hierarchy: core.hierarchy,
            detail: core.detail,
            bits,
            derived,
        });
    }
}

/// Unpacked core fields of one symbol, used by the packer and the decoder.
pub(crate) struct PackedCore {
    pub id: PackedId,
    pub name: Atom,
    pub fully_qualified: PackedName,
    pub container: Atom,
    pub ranges: [TextRange; 3],
    pub types: [Type; 4],
    pub kind: SymbolKind,
    pub flags: Flags,
    pub visibility: Visibility,
    pub write_visibility: Visibility,
    pub has_write_visibility: bool,
    pub hierarchy: Option<Arc<SymbolHierarchy>>,
    pub detail: Option<Arc<SymbolDetail>>,
    pub lazy_detail: bool,
}

/// Which declared type is the effective parameter type.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
#[repr(u8)]
pub(crate) enum ParameterTypeSource {
    #[default]
    Native = 0,
    Doc,
    Explicit,
}

#[derive(Debug, Default, PartialEq)]
pub(crate) struct PackedParameter {
    pub name: Atom,
    pub native_type: Type,
    pub doc_type: Type,
    start: u32,
    end: u32,
    /// Selection and default ranges relative to `start`; exact values live
    /// in `extras.ranges` when they do not fit.
    selection: u32,
    default: u32,
    pub extras: Option<Box<ParameterExtras>>,
    pub flags: u16,
    pub source: ParameterTypeSource,
    pub optional: bool,
}

#[derive(Debug, Default, PartialEq)]
pub(crate) struct ParameterExtras {
    /// An explicit ID that differs from the one derived from the owner.
    pub id: Option<SymbolId>,
    /// Exact `[range, selection, default]` when the compact form cannot
    /// represent them.
    pub ranges: Option<[TextRange; 3]>,
    pub ty: Type,
    pub assistant_tags: Vec<Str>,
    pub attributes: Vec<Attribute>,
    pub default_value: Option<Box<AttributeValue>>,
}

impl ParameterExtras {
    fn is_empty(&self) -> bool {
        self.id.is_none()
            && self.ranges.is_none()
            && self.ty.is_unknown()
            && self.assistant_tags.is_empty()
            && self.attributes.is_empty()
            && self.default_value.is_none()
    }
}

/// The parameter ID derived from its owner, matching what the binder
/// produces through [`crate::new_symbol_id`].
pub(crate) fn derived_parameter_id(owner: &str, name: &str) -> String {
    let mut id = String::with_capacity(3 + owner.len() + name.len());
    id.push_str("9:");
    write_lower_identifier(&mut id, owner);
    id.push(':');
    write_lower_identifier(&mut id, name);
    id
}

impl PackedParameter {
    pub fn pack(source: &Parameter, owner: &str) -> PackedParameter {
        let mut extras = ParameterExtras::default();
        let source_kind = if source.ty == source.native_type {
            ParameterTypeSource::Native
        } else if source.ty == source.doc_type {
            ParameterTypeSource::Doc
        } else {
            extras.ty = source.ty.clone();
            ParameterTypeSource::Explicit
        };
        // An empty ID is replaced by the derived one; only IDs
        // that differ from the derivation are stored.
        if !source.id.is_empty()
            && (owner.is_empty()
                || source.name.is_empty()
                || *source.id != derived_parameter_id(owner, &source.name))
        {
            extras.id = Some(source.id.clone());
        }
        extras.assistant_tags = source.assistant_tags.clone();
        extras.attributes = source.attributes.clone();
        extras.default_value = source.default_value.clone();
        PackedParameter::new(
            Atom::intern(&source.name),
            source.native_type.clone(),
            source.doc_type.clone(),
            [source.range, source.selection_range, source.default_range],
            extras,
            source.flags.0 as u16,
            source_kind,
            source.optional,
        )
    }

    /// Builds a parameter, moving ranges that do not fit the compact form
    /// into `extras`.
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        name: Atom,
        native_type: Type,
        doc_type: Type,
        ranges: [TextRange; 3],
        mut extras: ParameterExtras,
        flags: u16,
        source: ParameterTypeSource,
        optional: bool,
    ) -> PackedParameter {
        let (start, end) = range_parts(ranges[0]);
        let (selection, default) = match (
            compact_subrange(start, ranges[1]),
            compact_subrange(start, ranges[2]),
        ) {
            (Some(selection), Some(default)) => (selection, default),
            _ => {
                extras.ranges = Some(ranges);
                (EMPTY_SUBRANGE, EMPTY_SUBRANGE)
            }
        };
        PackedParameter {
            name,
            native_type,
            doc_type,
            start,
            end,
            selection,
            default,
            extras: (!extras.is_empty()).then(|| Box::new(extras)),
            flags,
            source,
            optional,
        }
    }

    /// `[range, selection range, default range]`.
    pub fn ranges(&self) -> [TextRange; 3] {
        if let Some(ranges) = self.extras.as_ref().and_then(|extras| extras.ranges) {
            return ranges;
        }
        [
            text_range(self.start, self.end),
            expand_subrange(self.start, self.selection),
            expand_subrange(self.start, self.default),
        ]
    }

    pub fn effective_type(&self) -> Type {
        match self.source {
            ParameterTypeSource::Native => self.native_type.clone(),
            ParameterTypeSource::Doc => self.doc_type.clone(),
            ParameterTypeSource::Explicit => self
                .extras
                .as_ref()
                .map(|extras| extras.ty.clone())
                .unwrap_or_default(),
        }
    }

    pub fn id(&self, owner: &str) -> SymbolId {
        if let Some(id) = self.extras.as_ref().and_then(|extras| extras.id.as_ref()) {
            return id.clone();
        }
        if owner.is_empty() || self.name.is_empty() {
            return SymbolId::default();
        }
        SymbolId::from(derived_parameter_id(owner, self.name.as_str()))
    }

    pub fn materialize(&self, owner: &str) -> Parameter {
        let extras = self.extras.as_deref();
        let [range, selection_range, default_range] = self.ranges();
        Parameter {
            id: self.id(owner),
            name: self.name.name(),
            ty: self.effective_type(),
            native_type: self.native_type.clone(),
            doc_type: self.doc_type.clone(),
            assistant_tags: extras
                .map(|extras| extras.assistant_tags.clone())
                .unwrap_or_default(),
            attributes: extras
                .map(|extras| extras.attributes.clone())
                .unwrap_or_default(),
            default_value: extras.and_then(|extras| extras.default_value.clone()),
            range,
            selection_range,
            default_range,
            flags: Flags(u32::from(self.flags)),
            optional: self.optional,
        }
    }
}
