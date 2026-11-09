//! Immutable workspace-wide semantic generations.
//!
//! A published snapshot indexes [`PackedDocument`]s with compact keyless
//! hash tables: keys are re-derived from the stored declarations, so the
//! index retains only 8-byte declaration references. Document overlays (open
//! editor files) and fixed-point symbol overrides are small map-backed layers
//! over a shared base.

use std::borrow::Cow;
use std::sync::{Arc, OnceLock};

use hashbrown::HashTable;
use rustc_hash::{FxHashMap, FxHashSet};
use smallvec::SmallVec;
use swlsp_php_types::Type;

use crate::str::Str;

use crate::atom::{Atom, hash_str};
use crate::call_contract::{
    CallContract, CallTargetKind, clone_call_contracts, function_contract_key, method_contract_key,
};
use crate::document::Document;
use crate::hierarchy::ReverseHierarchy;
use crate::lazy_returns::{LazyReturns, ReturnInference};
use crate::packed::{PackedDocument, PackedSymbol};
use crate::references::PackedReferences;
use crate::resolve::{ReferenceQueryCache, ReverseReferences};
use crate::symbol::{Symbol, SymbolId, SymbolKind};
use crate::text::{cmp_lower, eq_lower, hash_exact, hash_lower, normalize_symbol_name, trim_name};
use crate::view::SymbolView;

/// One location referencing a declaration.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct ReferenceLocation {
    pub path: Str,
    pub range_start: u32,
    pub range_end: u32,
}

/// An immutable workspace-wide semantic generation.
pub struct Snapshot {
    pub revision: u64,
    pub(crate) layer: Arc<Layer>,
    pub(crate) overrides: Arc<FxHashMap<SymbolId, Symbol>>,
    /// On-demand inferred return types of the generation this snapshot
    /// derives from (see [`crate::ReturnInference`]).
    pub(crate) returns: Option<Arc<LazyReturns>>,
}

#[derive(Default)]
pub(crate) struct Layer {
    pub base: Option<Arc<Snapshot>>,
    pub overlay_path: Option<Str>,
    pub overlay_references: Option<PackedReferences>,
    pub reference_queries: Option<ReferenceQueryCache>,
    pub packed: PackedIndex,
    pub expanded: ExpandedIndex,
    pub function_contracts: FxHashMap<String, Vec<IndexedContract>>,
    pub method_contracts: FxHashMap<String, Vec<IndexedContract>>,
    pub reverse_references: OnceLock<ReverseReferences>,
    pub reverse_hierarchy: ReverseHierarchy,
}

pub(crate) struct IndexedContract {
    pub path: Str,
    pub contract: CallContract,
}

/// A declaration inside the packed base layer.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) struct SymRef {
    pub document: u32,
    pub index: u32,
}

#[derive(Clone, Copy)]
pub(crate) enum NameIndexKind {
    Class,
    Function,
    Constant,
}

#[derive(Clone, Copy)]
struct NameSlot {
    first: SymRef,
    alternates_start: u32,
    alternates_len: u32,
}

/// Keyless name index: keys are recomputed from the stored declarations.
#[derive(Default)]
pub(crate) struct NameTable {
    slots: HashTable<NameSlot>,
    alternates: Vec<SymRef>,
}

#[derive(Clone, Copy)]
struct ContainerSlot {
    container: Atom,
    groups_start: u32,
    groups_len: u32,
}

/// Members grouped by container and normalized name, in build order.
#[derive(Default)]
pub(crate) struct MemberTable {
    containers: HashTable<ContainerSlot>,
    /// `(entries start, entries len)` per name group.
    groups: Vec<(u32, u32)>,
    entries: Vec<SymRef>,
}

#[derive(Default)]
pub(crate) struct PackedIndex {
    pub documents: Vec<Arc<PackedDocument>>,
    paths: HashTable<u32>,
    symbols: HashTable<SymRef>,
    classes: NameTable,
    functions: NameTable,
    constants: NameTable,
    pub members: MemberTable,
    pub globals: Vec<SymRef>,
}

#[derive(Default)]
pub(crate) struct ExpandedIndex {
    pub symbols: Vec<Symbol>,
    pub ids: FxHashMap<SymbolId, u32>,
    pub classes: FxHashMap<String, Vec<SymbolId>>,
    pub functions: FxHashMap<String, Vec<SymbolId>>,
    pub constants: FxHashMap<String, Vec<SymbolId>>,
    pub members: FxHashMap<SymbolId, FxHashMap<String, Vec<SymbolId>>>,
    pub globals: Vec<SymbolId>,
}

/// Small deduplicating ID set for traversals.
#[derive(Default)]
pub(crate) struct IdSet<'a> {
    inline: SmallVec<[&'a str; 4]>,
    overflow: FxHashSet<&'a str>,
}

impl<'a> IdSet<'a> {
    pub fn insert(&mut self, id: &'a str) -> bool {
        if self.inline.contains(&id) || self.overflow.contains(id) {
            return false;
        }
        if self.inline.len() < 4 {
            self.inline.push(id);
        } else {
            self.overflow.insert(id);
        }
        true
    }
}

fn class_key(fully_qualified: &str) -> &str {
    trim_name(fully_qualified, false)
}

impl PackedIndex {
    pub fn document(&self, document: u32) -> &PackedDocument {
        &self.documents[document as usize]
    }

    pub fn view(&self, reference: SymRef) -> SymbolView<'_> {
        SymbolView::packed(self.document(reference.document), reference.index)
    }

    fn packed_symbol(&self, reference: SymRef) -> &PackedSymbol {
        self.document(reference.document).symbol(reference.index)
    }

    /// The interned ID of a declaration (derived IDs are interned on first
    /// use; index construction uses [`PackedIndex::same_id`] instead).
    fn symbol_id(&self, reference: SymRef) -> &'static str {
        self.packed_symbol(reference).id().as_str()
    }

    fn same_id(&self, left: SymRef, right: SymRef) -> bool {
        self.packed_symbol(left).same_id(self.packed_symbol(right))
    }

    pub fn find(&self, id: &str) -> Option<SymRef> {
        self.symbols
            .find(hash_str(id), |candidate| {
                self.packed_symbol(*candidate).id_eq(id)
            })
            .copied()
    }

    /// The declaration that owns `reference`'s ID.
    fn find_ref(&self, reference: SymRef) -> Option<SymRef> {
        let symbol = self.packed_symbol(reference);
        self.symbols
            .find(symbol.id_hash(), |candidate| {
                self.packed_symbol(*candidate).same_id(symbol)
            })
            .copied()
    }

    pub fn path_document(&self, path: &str) -> Option<&Arc<PackedDocument>> {
        self.paths
            .find(hash_str(path), |&document| {
                &*self.documents[document as usize].path == path
            })
            .map(|&document| &self.documents[document as usize])
    }

    pub fn symbol_refs(&self) -> impl Iterator<Item = SymRef> + '_ {
        self.symbols.iter().copied()
    }

    pub fn symbol_count(&self) -> usize {
        self.symbols.len()
    }

    fn table(&self, kind: NameIndexKind) -> &NameTable {
        match kind {
            NameIndexKind::Class => &self.classes,
            NameIndexKind::Function => &self.functions,
            NameIndexKind::Constant => &self.constants,
        }
    }

    fn name_key(&self, kind: NameIndexKind, reference: SymRef) -> &'static str {
        let fully_qualified = self
            .document(reference.document)
            .symbol(reference.index)
            .global_name();
        match kind {
            NameIndexKind::Constant => fully_qualified,
            _ => class_key(fully_qualified),
        }
    }

    /// Matching declarations for a query; `name` is trimmed but not
    /// lowercased.
    pub fn named(&self, kind: NameIndexKind, name: &str) -> impl Iterator<Item = SymRef> + '_ {
        let table = self.table(kind);
        let slot = match kind {
            NameIndexKind::Constant => table
                .slots
                .find(hash_exact(name), |slot| {
                    self.name_key(kind, slot.first) == name
                })
                .copied(),
            _ => table
                .slots
                .find(hash_lower(name), |slot| {
                    eq_lower(self.name_key(kind, slot.first), name)
                })
                .copied(),
        };
        slot.into_iter().flat_map(move |slot| {
            std::iter::once(slot.first).chain(
                table.alternates[slot.alternates_start as usize
                    ..(slot.alternates_start + slot.alternates_len) as usize]
                    .iter()
                    .copied(),
            )
        })
    }

    pub fn name_count(&self, kind: NameIndexKind) -> (usize, usize) {
        let table = self.table(kind);
        (
            table.slots.len(),
            table.slots.len() + table.alternates.len(),
        )
    }

    fn container_slot(&self, container: &str) -> Option<ContainerSlot> {
        self.members
            .containers
            .find(hash_str(container), |slot| {
                slot.container.as_str() == container
            })
            .copied()
    }

    fn member_name(&self, reference: SymRef) -> &'static str {
        trim_name(
            self.document(reference.document)
                .symbol(reference.index)
                .name
                .as_str(),
            true,
        )
    }

    /// Members of `container` named `name` (already `$`-trimmed).
    pub fn members(&self, container: &str, name: &str) -> &[SymRef] {
        let Some(slot) = self.container_slot(container) else {
            return &[];
        };
        let groups = &self.members.groups
            [slot.groups_start as usize..(slot.groups_start + slot.groups_len) as usize];
        let found = groups.binary_search_by(|&(start, _)| {
            cmp_lower(self.member_name(self.members.entries[start as usize]), name)
        });
        match found {
            Ok(position) => {
                let (start, len) = groups[position];
                &self.members.entries[start as usize..(start + len) as usize]
            }
            Err(_) => &[],
        }
    }

    pub fn members_of(&self, container: &str) -> &[SymRef] {
        let Some(slot) = self.container_slot(container) else {
            return &[];
        };
        if slot.groups_len == 0 {
            return &[];
        }
        let first = self.members.groups[slot.groups_start as usize];
        let last = self.members.groups[(slot.groups_start + slot.groups_len - 1) as usize];
        &self.members.entries[first.0 as usize..(last.0 + last.1) as usize]
    }

    pub fn member_stats(&self) -> (usize, usize, usize) {
        (
            self.members.containers.len(),
            self.members.groups.len(),
            self.members.entries.len(),
        )
    }

    pub fn member_container_sizes(&self) -> impl Iterator<Item = usize> + '_ {
        self.members.containers.iter().map(|slot| {
            if slot.groups_len == 0 {
                return 0;
            }
            let first = self.members.groups[slot.groups_start as usize];
            let last = self.members.groups[(slot.groups_start + slot.groups_len - 1) as usize];
            (last.0 + last.1 - first.0) as usize
        })
    }

    fn build(documents: Vec<Arc<PackedDocument>>) -> PackedIndex {
        let mut index = PackedIndex {
            documents,
            ..PackedIndex::default()
        };
        let symbol_count: usize = index
            .documents
            .iter()
            .map(|document| document.symbols.len())
            .sum();
        index.symbols.reserve(symbol_count, |_| 0);
        // Declaration IDs: the last declaration wins.
        for (document_index, document) in index.documents.iter().enumerate() {
            for symbol_index in 0..document.symbols.len() as u32 {
                let reference = SymRef {
                    document: document_index as u32,
                    index: symbol_index,
                };
                let symbol = document.symbol(symbol_index);
                let hash = symbol.id_hash();
                let documents = &index.documents;
                let lookup = |candidate: &SymRef| {
                    documents[candidate.document as usize]
                        .symbol(candidate.index)
                        .same_id(symbol)
                };
                match index.symbols.find_mut(hash, lookup) {
                    Some(existing) => *existing = reference,
                    None => {
                        index.symbols.insert_unique(hash, reference, |candidate| {
                            documents[candidate.document as usize]
                                .symbol(candidate.index)
                                .id_hash()
                        });
                    }
                }
            }
        }
        for (document_index, document) in index.documents.iter().enumerate() {
            let hash = hash_str(&document.path);
            let documents = &index.documents;
            match index.paths.find_mut(hash, |&candidate| {
                documents[candidate as usize].path == document.path
            }) {
                Some(existing) => *existing = document_index as u32,
                None => {
                    index
                        .paths
                        .insert_unique(hash, document_index as u32, |&candidate| {
                            hash_str(&documents[candidate as usize].path)
                        });
                }
            }
        }
        let winners: Vec<SymRef> = index
            .documents
            .iter()
            .enumerate()
            .flat_map(|(document_index, document)| {
                (0..document.symbols.len() as u32).map(move |symbol| SymRef {
                    document: document_index as u32,
                    index: symbol,
                })
            })
            .filter(|reference| index.find_ref(*reference) == Some(*reference))
            .collect();
        let mut classes = NameTableBuilder::default();
        let mut functions = NameTableBuilder::default();
        let mut constants = NameTableBuilder::default();
        let mut members = Vec::new();
        for &reference in &winners {
            let symbol = index.document(reference.document).symbol(reference.index);
            let kind = symbol.kind();
            if !symbol.container.is_empty() {
                if kind.is_member() {
                    members.push(reference);
                }
                continue;
            }
            let fully_qualified = symbol.global_name();
            let added = match kind {
                SymbolKind::Class
                | SymbolKind::Interface
                | SymbolKind::Trait
                | SymbolKind::Enum => classes.add(
                    &index,
                    NameIndexKind::Class,
                    class_key(fully_qualified),
                    reference,
                ),
                SymbolKind::Function => functions.add(
                    &index,
                    NameIndexKind::Function,
                    class_key(fully_qualified),
                    reference,
                ),
                SymbolKind::GlobalConstant => {
                    constants.add(&index, NameIndexKind::Constant, fully_qualified, reference)
                }
                _ => false,
            };
            if added {
                index.globals.push(reference);
            }
        }
        index.classes = classes.finish(&index, NameIndexKind::Class);
        index.functions = functions.finish(&index, NameIndexKind::Function);
        index.constants = constants.finish(&index, NameIndexKind::Constant);
        index.members = MemberTable::build(&index, members);
        index
    }
}

#[derive(Default)]
struct NameTableBuilder {
    groups: Vec<Vec<SymRef>>,
    slots: HashTable<u32>,
}

impl NameTableBuilder {
    fn hash(kind: NameIndexKind, key: &str) -> u64 {
        match kind {
            NameIndexKind::Constant => hash_exact(key),
            _ => hash_lower(key),
        }
    }

    fn matches(kind: NameIndexKind, left: &str, right: &str) -> bool {
        match kind {
            NameIndexKind::Constant => left == right,
            _ => eq_lower(left, right),
        }
    }

    /// Go `symbolNameIndex.add`: reports whether the ID was newly recorded.
    fn add(
        &mut self,
        index: &PackedIndex,
        kind: NameIndexKind,
        key: &str,
        reference: SymRef,
    ) -> bool {
        let hash = Self::hash(kind, key);
        let groups = &self.groups;
        let found = self
            .slots
            .find(hash, |&group| {
                Self::matches(kind, index.name_key(kind, groups[group as usize][0]), key)
            })
            .copied();
        match found {
            Some(group) => {
                let group = &mut self.groups[group as usize];
                if group
                    .iter()
                    .any(|existing| index.same_id(*existing, reference))
                {
                    return false;
                }
                group.push(reference);
                true
            }
            None => {
                self.groups.push(vec![reference]);
                let group = self.groups.len() as u32 - 1;
                let groups = &self.groups;
                self.slots.insert_unique(hash, group, |&group| {
                    Self::hash(kind, index.name_key(kind, groups[group as usize][0]))
                });
                true
            }
        }
    }

    fn finish(self, index: &PackedIndex, kind: NameIndexKind) -> NameTable {
        let mut table = NameTable::default();
        table.slots.reserve(self.groups.len(), |_| 0);
        for group in self.groups {
            let slot = NameSlot {
                first: group[0],
                alternates_start: table.alternates.len() as u32,
                alternates_len: group.len() as u32 - 1,
            };
            table.alternates.extend_from_slice(&group[1..]);
            let hash = Self::hash(kind, index.name_key(kind, slot.first));
            table.slots.insert_unique(hash, slot, |slot| {
                Self::hash(kind, index.name_key(kind, slot.first))
            });
        }
        table
    }
}

impl MemberTable {
    fn build(index: &PackedIndex, mut members: Vec<SymRef>) -> MemberTable {
        let container = |reference: &SymRef| {
            index
                .document(reference.document)
                .symbol(reference.index)
                .container
        };
        members.sort_by(|left, right| {
            container(left)
                .raw()
                .cmp(&container(right).raw())
                .then_with(|| cmp_lower(index.member_name(*left), index.member_name(*right)))
        });
        let mut table = MemberTable::default();
        let mut position = 0;
        while position < members.len() {
            let current_container = container(&members[position]);
            let groups_start = table.groups.len() as u32;
            while position < members.len() && container(&members[position]) == current_container {
                let name = index.member_name(members[position]);
                let entries_start = table.entries.len() as u32;
                while position < members.len()
                    && container(&members[position]) == current_container
                    && cmp_lower(index.member_name(members[position]), name).is_eq()
                {
                    let reference = members[position];
                    if !table.entries[entries_start as usize..]
                        .iter()
                        .any(|existing| index.same_id(*existing, reference))
                    {
                        table.entries.push(reference);
                    }
                    position += 1;
                }
                table
                    .groups
                    .push((entries_start, table.entries.len() as u32 - entries_start));
            }
            let slot = ContainerSlot {
                container: current_container,
                groups_start,
                groups_len: table.groups.len() as u32 - groups_start,
            };
            table
                .containers
                .insert_unique(hash_str(current_container.as_str()), slot, |slot| {
                    hash_str(slot.container.as_str())
                });
        }
        table
    }
}

fn add_unique(ids: &mut Vec<SymbolId>, id: &SymbolId) -> bool {
    if ids.contains(id) {
        return false;
    }
    ids.push(id.clone());
    true
}

impl ExpandedIndex {
    fn build(symbols: Vec<Symbol>) -> ExpandedIndex {
        let mut index = ExpandedIndex {
            symbols,
            ..ExpandedIndex::default()
        };
        for position in 0..index.symbols.len() {
            let symbol = &index.symbols[position];
            index.ids.insert(symbol.id.clone(), position as u32);
            let id = symbol.id.clone();
            if !symbol.container.is_empty() && symbol.kind.is_member() {
                let name = normalize_symbol_name(&symbol.name, true);
                let members = index.members.entry(symbol.container.clone()).or_default();
                add_unique(members.entry(name).or_default(), &id);
                continue;
            }
            if !symbol.container.is_empty() {
                continue;
            }
            let (table, key) = match symbol.kind {
                SymbolKind::Class
                | SymbolKind::Interface
                | SymbolKind::Trait
                | SymbolKind::Enum => (
                    &mut index.classes,
                    normalize_symbol_name(&symbol.fully_qualified, false),
                ),
                SymbolKind::Function => (
                    &mut index.functions,
                    normalize_symbol_name(&symbol.fully_qualified, false),
                ),
                SymbolKind::GlobalConstant => {
                    (&mut index.constants, symbol.fully_qualified.to_string())
                }
                _ => continue,
            };
            if add_unique(table.entry(key).or_default(), &id) {
                index.globals.push(id);
            }
        }
        index
    }

    fn table(&self, kind: NameIndexKind) -> &FxHashMap<String, Vec<SymbolId>> {
        match kind {
            NameIndexKind::Class => &self.classes,
            NameIndexKind::Function => &self.functions,
            NameIndexKind::Constant => &self.constants,
        }
    }
}

fn index_contracts(
    documents: &[(Str, &[CallContract])],
) -> [FxHashMap<String, Vec<IndexedContract>>; 2] {
    let mut functions: FxHashMap<String, Vec<IndexedContract>> = FxHashMap::default();
    let mut methods: FxHashMap<String, Vec<IndexedContract>> = FxHashMap::default();
    for (path, contracts) in documents {
        for contract in contracts.iter().filter(|contract| contract.valid()) {
            let (table, key) = match contract.target.kind {
                CallTargetKind::Function => {
                    (&mut functions, function_contract_key(&contract.target.name))
                }
                CallTargetKind::Method => {
                    (&mut methods, method_contract_key(&contract.target.name))
                }
            };
            table.entry(key).or_default().push(IndexedContract {
                path: path.clone(),
                contract: contract.clone(),
            });
        }
    }
    [functions, methods]
}

/// Lowercase a query name unless it is already lowercase.
fn lower_query(name: &str) -> Cow<'_, str> {
    if name
        .bytes()
        .all(|byte| byte.is_ascii() && !byte.is_ascii_uppercase())
    {
        Cow::Borrowed(name)
    } else {
        Cow::Owned(swlsp_php_types::go_to_lower(name))
    }
}

impl Snapshot {
    /// Builds a generation from public documents (cloned). Later documents
    /// win duplicate declaration IDs and paths.
    pub fn new(revision: u64, documents: &[Document]) -> Arc<Snapshot> {
        let packed = documents
            .iter()
            .map(|document| Arc::new(PackedDocument::pack(document, false)))
            .collect();
        Snapshot::from_packed(revision, packed)
    }

    pub(crate) fn from_packed(revision: u64, documents: Vec<Arc<PackedDocument>>) -> Arc<Snapshot> {
        Snapshot::from_packed_with(revision, documents, None)
    }

    /// A generation whose inferred return types `inference` recomputes on
    /// demand.
    pub(crate) fn from_packed_with(
        revision: u64,
        documents: Vec<Arc<PackedDocument>>,
        inference: Option<Arc<dyn ReturnInference>>,
    ) -> Arc<Snapshot> {
        // Member tables group by atom identity, which needs one generation.
        debug_assert!(
            documents
                .windows(2)
                .all(|pair| pair[0].generation.same(&pair[1].generation)),
            "snapshot documents span interner generations"
        );
        let contracts: Vec<(Str, &[CallContract])> = documents
            .iter()
            .map(|document| (document.path.clone(), &document.call_contracts[..]))
            .collect();
        let [function_contracts, method_contracts] = index_contracts(&contracts);
        let packed = PackedIndex::build(documents);
        let layer = Arc::new(Layer {
            packed,
            function_contracts,
            method_contracts,
            ..Layer::default()
        });
        Arc::new_cyclic(|generation| Snapshot {
            revision,
            layer,
            overrides: Arc::default(),
            returns: inference
                .map(|inference| Arc::new(LazyReturns::new(inference, generation.clone()))),
        })
    }

    pub(crate) fn base(&self) -> Option<&Snapshot> {
        self.layer.base.as_deref()
    }

    /// This generation followed by every base generation.
    /// A view of a packed declaration that materializes this generation's
    /// inferred return types.
    fn packed_view<'a>(&'a self, packed: &'a PackedIndex, reference: SymRef) -> SymbolView<'a> {
        packed.view(reference).with_returns(self.returns.as_deref())
    }

    pub(crate) fn chain(&self) -> impl Iterator<Item = &Snapshot> {
        std::iter::successors(Some(self), |current| current.base())
    }

    pub(crate) fn overlay_path(&self) -> Option<&str> {
        self.layer.overlay_path.as_deref()
    }

    /// Whether views can be returned straight from the packed layer.
    pub(crate) fn is_direct(&self) -> bool {
        self.layer.base.is_none()
            && self.layer.expanded.symbols.is_empty()
            && self.overrides.is_empty()
    }

    pub(crate) fn packed(&self) -> &PackedIndex {
        &self.layer.packed
    }

    /// The materialized declaration with `id`.
    pub fn symbol(&self, id: &str) -> Option<Symbol> {
        self.symbol_view(id).map(|view| view.materialize())
    }

    /// A lightweight view of the declaration with `id`.
    pub fn symbol_view(&self, id: &str) -> Option<SymbolView<'_>> {
        if let Some(symbol) = self.overrides.get(id) {
            return Some(SymbolView::expanded(symbol));
        }
        if let Some(&index) = self.layer.expanded.ids.get(id) {
            return Some(SymbolView::expanded(
                &self.layer.expanded.symbols[index as usize],
            ));
        }
        if let Some(reference) = self.layer.packed.find(id) {
            return Some(self.packed_view(&self.layer.packed, reference));
        }
        let view = self.base()?.symbol_view(id)?;
        if self.overlay_path() == Some(view.path()) {
            return None;
        }
        Some(view.with_returns(self.returns.as_deref()))
    }

    fn visit_named_ids<'a>(
        &'a self,
        kind: NameIndexKind,
        name: &str,
        seen: &mut IdSet<'a>,
        visit: &mut dyn FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        let direct = self.is_direct();
        let mut lowered: Option<Cow<'_, str>> = None;
        for current in self.chain() {
            for reference in current.layer.packed.named(kind, name) {
                let view = current.layer.packed.view(reference);
                if !self.visit_visible(direct, view, seen, visit) {
                    return false;
                }
            }
            let table = current.layer.expanded.table(kind);
            if table.is_empty() {
                continue;
            }
            let key = match kind {
                NameIndexKind::Constant => Cow::Borrowed(name),
                _ => lowered.get_or_insert_with(|| lower_query(name)).clone(),
            };
            if let Some(ids) = table.get(key.as_ref()) {
                for id in ids {
                    if !seen.insert(id_str(self, id)) {
                        continue;
                    }
                    if let Some(view) = self.symbol_view(id)
                        && !visit(view)
                    {
                        return false;
                    }
                }
            }
        }
        true
    }

    /// Visits a packed-layer candidate through this generation's visibility
    /// rules (overrides, overlays).
    fn visit_visible<'a>(
        &'a self,
        direct: bool,
        candidate: SymbolView<'a>,
        seen: &mut IdSet<'a>,
        visit: &mut dyn FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        let id = candidate.id();
        if !seen.insert(id) {
            return true;
        }
        if direct {
            return visit(candidate.with_returns(self.returns.as_deref()));
        }
        match self.symbol_view(id) {
            Some(view) => visit(view),
            None => true,
        }
    }

    fn named_views(&self, kind: NameIndexKind, name: &str) -> Vec<SymbolView<'_>> {
        let mut result = Vec::new();
        let mut seen = IdSet::default();
        self.visit_named_ids(kind, name, &mut seen, &mut |view| {
            result.push(view);
            true
        });
        result
    }

    pub fn classes(&self, name: &str) -> Vec<Symbol> {
        self.class_views(name)
            .iter()
            .map(SymbolView::materialize)
            .collect()
    }

    pub fn class_views(&self, name: &str) -> Vec<SymbolView<'_>> {
        self.named_views(NameIndexKind::Class, trim_name(name, false))
    }

    /// Visits matching classes without materializing a result. Returning
    /// `false` stops; the result reports whether every match was visited.
    pub fn visit_class_views<'a>(
        &'a self,
        name: &str,
        mut visit: impl FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        let mut seen = IdSet::default();
        self.visit_named_ids(
            NameIndexKind::Class,
            trim_name(name, false),
            &mut seen,
            &mut visit,
        )
    }

    pub fn functions(&self, name: &str) -> Vec<Symbol> {
        self.function_views(name)
            .iter()
            .map(SymbolView::materialize)
            .collect()
    }

    pub fn function_views(&self, name: &str) -> Vec<SymbolView<'_>> {
        self.named_views(NameIndexKind::Function, trim_name(name, false))
    }

    pub fn visit_function_views<'a>(
        &'a self,
        name: &str,
        mut visit: impl FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        let mut seen = IdSet::default();
        self.visit_named_ids(
            NameIndexKind::Function,
            trim_name(name, false),
            &mut seen,
            &mut visit,
        )
    }

    pub fn constants(&self, name: &str) -> Vec<Symbol> {
        self.constant_views(name)
            .iter()
            .map(SymbolView::materialize)
            .collect()
    }

    pub fn constant_views(&self, name: &str) -> Vec<SymbolView<'_>> {
        self.named_views(NameIndexKind::Constant, trim_name(name, false))
    }

    pub fn visit_constant_views<'a>(
        &'a self,
        name: &str,
        mut visit: impl FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        let mut seen = IdSet::default();
        self.visit_named_ids(
            NameIndexKind::Constant,
            trim_name(name, false),
            &mut seen,
            &mut visit,
        )
    }

    pub(crate) fn visit_named<'a>(
        &'a self,
        kind: NameIndexKind,
        name: &str,
        visit: &mut dyn FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        let mut seen = IdSet::default();
        self.visit_named_ids(kind, trim_name(name, false), &mut seen, visit)
    }

    /// Visits dynamic return contracts of a resolved function name. Document
    /// overlays shadow persisted contracts of the same path.
    pub fn visit_function_call_contracts(
        &self,
        name: &str,
        mut visit: impl FnMut(&CallContract) -> bool,
    ) -> bool {
        let key = function_contract_key(name);
        self.visit_call_contracts(|layer| layer.function_contracts.get(&key), &mut visit)
    }

    /// Visits contracts whose declaring class is a supertype of `receiver`.
    pub fn visit_method_call_contracts(
        &self,
        receiver: &Type,
        name: &str,
        mut visit: impl FnMut(&CallContract) -> bool,
    ) -> bool {
        if receiver.is_unknown() {
            return true;
        }
        let key = method_contract_key(name);
        let relations = self.relations();
        self.visit_call_contracts(|layer| layer.method_contracts.get(&key), &mut |contract| {
            if !relations.is_subtype(receiver, &Type::named(&contract.target.class, Vec::new())) {
                return true;
            }
            visit(contract)
        })
    }

    fn visit_call_contracts<'a>(
        &'a self,
        entries: impl Fn(&'a Layer) -> Option<&'a Vec<IndexedContract>>,
        visit: &mut dyn FnMut(&CallContract) -> bool,
    ) -> bool {
        let mut shadowed: SmallVec<[&str; 2]> = SmallVec::new();
        for current in self.chain() {
            for entry in entries(&current.layer).into_iter().flatten() {
                if shadowed.contains(&&*entry.path) {
                    continue;
                }
                if !visit(&entry.contract) {
                    return false;
                }
            }
            if let Some(path) = current.overlay_path()
                && !shadowed.contains(&path)
            {
                shadowed.push(path);
            }
        }
        true
    }

    pub fn members(&self, container: &str, name: &str) -> Vec<Symbol> {
        self.member_views(container, name)
            .iter()
            .map(SymbolView::materialize)
            .collect()
    }

    pub fn member_views(&self, container: &str, name: &str) -> Vec<SymbolView<'_>> {
        let mut result = Vec::new();
        self.visit_member_views(container, name, |view| {
            result.push(view);
            true
        });
        result
    }

    /// Visits named members without materializing a result.
    pub fn visit_member_views<'a>(
        &'a self,
        container: &str,
        name: &str,
        mut visit: impl FnMut(SymbolView<'a>) -> bool,
    ) -> bool {
        let name = trim_name(name, true);
        let mut seen = IdSet::default();
        let direct = self.is_direct();
        let mut lowered: Option<Cow<'_, str>> = None;
        for current in self.chain() {
            for &reference in current.layer.packed.members(container, name) {
                let view = current.layer.packed.view(reference);
                if !self.visit_visible(direct, view, &mut seen, &mut visit) {
                    return false;
                }
            }
            let Some(members) = current.layer.expanded.members.get(container) else {
                continue;
            };
            let key = lowered.get_or_insert_with(|| lower_query(name));
            if let Some(ids) = members.get(key.as_ref()) {
                for id in ids {
                    if !seen.insert(id_str(self, id)) {
                        continue;
                    }
                    if let Some(view) = self.symbol_view(id)
                        && !visit(view)
                    {
                        return false;
                    }
                }
            }
        }
        true
    }

    pub fn members_of(&self, container: &str) -> Vec<Symbol> {
        self.member_views_of(container)
            .iter()
            .map(SymbolView::materialize)
            .collect()
    }

    /// Every member of `container`, sorted by kind, name, path, and offset.
    pub fn member_views_of(&self, container: &str) -> Vec<SymbolView<'_>> {
        let mut result: Vec<SymbolView<'_>>;
        if self.is_direct() {
            result = self
                .layer
                .packed
                .members_of(container)
                .iter()
                .map(|&reference| self.packed_view(&self.layer.packed, reference))
                .collect();
        } else {
            result = Vec::new();
            let mut seen = IdSet::default();
            for current in self.chain() {
                for &reference in current.layer.packed.members_of(container) {
                    let id = current.layer.packed.view(reference).id();
                    if seen.insert(id)
                        && let Some(view) = self.symbol_view(id)
                    {
                        result.push(view);
                    }
                }
                if let Some(members) = current.layer.expanded.members.get(container) {
                    for ids in members.values() {
                        for id in ids {
                            if seen.insert(id_str(self, id))
                                && let Some(view) = self.symbol_view(id)
                            {
                                result.push(view);
                            }
                        }
                    }
                }
            }
        }
        sort_member_views(&mut result);
        result
    }

    /// Symbols declared in `path`.
    pub fn symbols_in(&self, path: &str) -> Vec<Symbol> {
        if self.overlay_path() == Some(path) {
            return self
                .layer
                .expanded
                .symbols
                .iter()
                .filter_map(|symbol| self.symbol(&symbol.id))
                .collect();
        }
        if let Some(document) = self.layer.packed.path_document(path) {
            return (0..document.symbols.len() as u32)
                .map(|index| document.materialize_symbol(index))
                .collect();
        }
        self.base()
            .map(|base| base.symbols_in(path))
            .unwrap_or_default()
    }

    /// Whether `path` contributes a document to this generation.
    pub fn has_document(&self, path: &str) -> bool {
        self.chain().any(|current| {
            current.overlay_path() == Some(path)
                || current.layer.packed.path_document(path).is_some()
        })
    }

    /// Every visible declaration, materialized with stored inferred return
    /// types (like [`Snapshot::global_symbols`]).
    pub fn all_symbols(&self) -> Vec<Symbol> {
        let stored = |id: &str| self.symbol_view(id).map(|view| view.materialize_stored());
        let mut result = Vec::new();
        let mut seen: FxHashSet<&str> = FxHashSet::default();
        for current in self.chain() {
            let packed = &current.layer.packed;
            for document in 0..packed.documents.len() as u32 {
                for index in 0..packed.document(document).symbols.len() as u32 {
                    let reference = SymRef { document, index };
                    let id = packed.symbol_id(reference);
                    if packed.find(id) == Some(reference)
                        && seen.insert(id)
                        && let Some(symbol) = stored(id)
                    {
                        result.push(symbol);
                    }
                }
            }
            for symbol in &current.layer.expanded.symbols {
                if seen.insert(&symbol.id)
                    && let Some(symbol) = stored(&symbol.id)
                {
                    result.push(symbol);
                }
            }
        }
        result
    }

    /// Every visible declaration keyed by ID.
    pub fn clone_symbols(&self) -> FxHashMap<SymbolId, Symbol> {
        let mut result = FxHashMap::default();
        if let Some(base) = self.base() {
            for (id, symbol) in base.clone_symbols() {
                if self.overlay_path() != Some(&*symbol.path) {
                    result.insert(id, symbol);
                }
            }
        }
        let packed = &self.layer.packed;
        for reference in packed.symbol_refs() {
            let symbol = packed
                .document(reference.document)
                .materialize_symbol(reference.index);
            result.insert(symbol.id.clone(), symbol);
        }
        for (id, &index) in &self.layer.expanded.ids {
            result.insert(
                id.clone(),
                self.layer.expanded.symbols[index as usize].clone(),
            );
        }
        for (id, symbol) in self.overrides.iter() {
            result.insert(id.clone(), symbol.clone());
        }
        result
    }

    /// Class-like, function, and constant declarations, materialized.
    ///
    /// Inferred return types are the stored ones: a workspace-wide listing
    /// must not analyze every file declaring such a function.
    pub fn global_symbols(&self) -> Vec<Symbol> {
        self.global_symbol_views()
            .iter()
            .map(SymbolView::materialize_stored)
            .collect()
    }

    /// Class-like, function, and constant declaration views.
    pub fn global_symbol_views(&self) -> Vec<SymbolView<'_>> {
        if self.is_direct() {
            return self
                .layer
                .packed
                .globals
                .iter()
                .map(|&reference| self.packed_view(&self.layer.packed, reference))
                .collect();
        }
        let mut result = Vec::new();
        let mut seen: FxHashSet<&str> = FxHashSet::default();
        for current in self.chain() {
            for &reference in &current.layer.packed.globals {
                let id = current.layer.packed.symbol_id(reference);
                if seen.insert(id)
                    && let Some(view) = self.symbol_view(id)
                {
                    result.push(view);
                }
            }
            for id in &current.layer.expanded.globals {
                if seen.insert(id_str(self, id))
                    && let Some(view) = self.symbol_view(id)
                {
                    result.push(view);
                }
            }
        }
        result
    }

    /// Class-like global declaration views.
    pub fn global_class_views(&self) -> Vec<SymbolView<'_>> {
        let mut views = self.global_symbol_views();
        views.retain(|view| view.kind().is_class_like());
        views
    }

    /// Overlays values for declarations already present in this generation.
    /// Index keys and reverse references stay shared because IDs, names, and
    /// ranges do not change during fixed-point analysis.
    pub fn with_updated_symbols(&self, document: &Document) -> Arc<Snapshot> {
        self.with_updated(document, |_| true)
    }

    /// Overlays function-like declarations during local fixed-point
    /// inference without copying unrelated declarations.
    pub fn with_updated_function_returns(&self, document: &Document) -> Arc<Snapshot> {
        self.with_updated(document, |symbol| symbol.kind.is_function_like())
    }

    /// Replaces the override of `symbol.id` with `symbol`, like
    /// [`Snapshot::with_updated_symbols`] for one declaration. The update is
    /// applied in place when `this` is the only handle, so fixed-point
    /// inference can publish one changed return type without copying the
    /// override table.
    pub fn override_symbol(this: &mut Arc<Snapshot>, symbol: &Symbol) {
        if this.symbol_view(&symbol.id).is_none() {
            return;
        }
        if Arc::get_mut(this).is_none() {
            *this = Arc::new(Snapshot {
                revision: this.revision,
                layer: this.layer.clone(),
                overrides: this.overrides.clone(),
                returns: this.returns.clone(),
            });
        }
        let snapshot = Arc::get_mut(this).expect("snapshot handle is unique");
        Arc::make_mut(&mut snapshot.overrides).insert(symbol.id.clone(), symbol.clone());
    }

    fn with_updated(
        &self,
        document: &Document,
        include: impl Fn(&Symbol) -> bool,
    ) -> Arc<Snapshot> {
        let mut overrides = FxHashMap::clone(&self.overrides);
        for symbol in &document.symbols {
            if include(symbol) && self.symbol_view(&symbol.id).is_some() {
                overrides.insert(symbol.id.clone(), symbol.clone());
            }
        }
        Arc::new(Snapshot {
            revision: self.revision,
            layer: self.layer.clone(),
            overrides: Arc::new(overrides),
            returns: self.returns.clone(),
        })
    }

    /// An overlay in which the document's declarations and references
    /// replace persisted ones from the same path.
    pub fn with_document(self: &Arc<Self>, document: &Document) -> Arc<Snapshot> {
        self.overlay(document, true)
    }

    /// An overlay with the document's declarations but not its reference
    /// graph; sufficient while linking and inferring one document.
    pub fn with_declarations(self: &Arc<Self>, document: &Document) -> Arc<Snapshot> {
        self.overlay(document, false)
    }

    fn overlay(self: &Arc<Self>, document: &Document, include_references: bool) -> Arc<Snapshot> {
        let [function_contracts, method_contracts] = if document.call_contracts.is_empty() {
            Default::default()
        } else {
            let contracts = clone_call_contracts(&document.call_contracts);
            index_contracts(&[(document.path.clone(), &contracts[..])])
        };
        let overlay_references = (include_references && !document.references.is_empty())
            .then(|| PackedReferences::pack(&document.references));
        Arc::new(Snapshot {
            revision: self.revision,
            layer: Arc::new(Layer {
                base: Some(self.clone()),
                overlay_path: Some(document.path.clone()),
                overlay_references,
                reference_queries: include_references.then(ReferenceQueryCache::default),
                expanded: ExpandedIndex::build(document.symbols.clone()),
                function_contracts,
                method_contracts,
                ..Layer::default()
            }),
            overrides: Arc::default(),
            returns: self.returns.clone(),
        })
    }

    /// This snapshot using the inferred return types stored at indexing
    /// time instead of recomputing them. Indexing analyzes against such a
    /// snapshot, so preparing a file never analyzes other files.
    pub fn without_return_inference(self: &Arc<Self>) -> Arc<Snapshot> {
        if self.returns.is_none() {
            return self.clone();
        }
        Arc::new(Snapshot {
            revision: self.revision,
            layer: self.layer.clone(),
            overrides: self.overrides.clone(),
            returns: None,
        })
    }

    /// Whether inferred return types are recomputed on demand.
    pub fn infers_returns(&self) -> bool {
        self.returns.is_some()
    }

    /// Number of files whose return types this generation inferred on
    /// demand so far.
    pub fn inferred_return_files(&self) -> usize {
        self.returns
            .as_ref()
            .map_or(0, |returns| returns.inferred_files())
    }

    /// Visits `(path, references)` of every document visible in this
    /// generation, overlays first.
    pub(crate) fn visit_path_references<'a>(
        &'a self,
        mut visitor: impl FnMut(&'a str, &'a PackedReferences),
    ) {
        let mut seen: FxHashSet<&str> = FxHashSet::default();
        for current in self.chain() {
            if let Some(path) = current.overlay_path()
                && seen.insert(path)
                && let Some(references) = &current.layer.overlay_references
            {
                visitor(path, references);
            }
            for document in &current.layer.packed.documents {
                let path: &str = &document.path;
                if current
                    .layer
                    .packed
                    .path_document(path)
                    .is_some_and(|winner| Arc::ptr_eq(winner, document))
                    && seen.insert(path)
                {
                    visitor(path, &document.references);
                }
            }
        }
    }
}

/// The ID string of a visible declaration, borrowed from the snapshot when
/// possible so traversal sets do not allocate.
fn id_str<'a>(_snapshot: &'a Snapshot, id: &'a SymbolId) -> &'a str {
    id
}

pub(crate) fn sort_member_views(views: &mut [SymbolView<'_>]) {
    views.sort_by(|left, right| {
        (left.kind() as u8)
            .cmp(&(right.kind() as u8))
            .then_with(|| cmp_lower(trim_name(left.name(), true), trim_name(right.name(), true)))
            .then_with(|| left.path().cmp(right.path()))
            .then_with(|| left.range().start().cmp(&right.range().start()))
    });
}

impl std::fmt::Debug for Snapshot {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Snapshot")
            .field("revision", &self.revision)
            .field("documents", &self.layer.packed.documents.len())
            .field("overlay", &self.overlay_path())
            .field("overrides", &self.overrides.len())
            .finish()
    }
}
