//! Reference target resolution and reverse-reference indexes.

use std::collections::HashMap;
use std::sync::{Arc, Mutex, OnceLock};

use rustc_hash::FxHashMap;
use swlsp_php_types::{Kind as TypeKind, Type};
use swlsp_textutil::eq_ignore_case;

use crate::str::Str;

use crate::document::NameKind;
use crate::references::{PackedReferences, ReferenceRecord, bloom_bits, reference_bloom_hash};
use crate::snapshot::{IdSet, NameIndexKind, ReferenceLocation, Snapshot};
use crate::symbol::SymbolKind;
use crate::text::{eq_lower, trim_name};
use crate::view::SymbolView;

/// The lazily built reverse-reference index of a published generation.
#[derive(Default)]
pub(crate) struct ReverseReferences {
    paths: Vec<Str>,
    /// Target ID → `(start, count)` span of `locations`.
    spans: FxHashMap<&'static str, (u32, u32)>,
    /// `(path index, range start, range end)`.
    locations: Vec<(u32, u32, u32)>,
}

impl ReverseReferences {
    pub fn target_count(&self) -> usize {
        self.spans.len()
    }

    pub fn location_count(&self) -> usize {
        self.locations.len()
    }
}

/// Reference targets requested from a document overlay. Building a complete
/// reverse index per open document would multiply retained memory, while
/// rescanning on every repeated request is linear in workspace size.
#[derive(Default)]
pub(crate) struct ReferenceQueryCache {
    entries: Mutex<HashMap<String, Arc<OnceLock<Vec<ReferenceLocation>>>>>,
}

impl ReferenceQueryCache {
    fn load_or_compute(
        &self,
        id: &str,
        compute: impl FnOnce() -> Vec<ReferenceLocation>,
    ) -> Vec<ReferenceLocation> {
        let entry = {
            let mut entries = self
                .entries
                .lock()
                .unwrap_or_else(|poison| poison.into_inner());
            entries.entry(id.to_owned()).or_default().clone()
        };
        entry.get_or_init(compute).clone()
    }

    #[cfg(test)]
    pub(crate) fn len(&self) -> usize {
        self.entries.lock().unwrap().len()
    }
}

/// Rejects documents whose references cannot target a declaration using a
/// derived Bloom filter over every retained reference string.
struct TargetFilter {
    masks: Vec<[u64; 2]>,
}

impl TargetFilter {
    fn new(target: &SymbolView<'_>) -> TargetFilter {
        let kind = target.kind();
        let mut filter = TargetFilter {
            masks: Vec::with_capacity(2),
        };
        filter.add(target.id());
        let name = trim_name(target.name(), true);
        let fully_qualified = trim_name(target.fully_qualified(), false);
        if kind.is_class_like()
            || kind == SymbolKind::Function
            || kind == SymbolKind::GlobalConstant
        {
            filter.add(fully_qualified);
            if fully_qualified.is_empty() {
                filter.add(name);
            }
        } else if kind.is_member() {
            filter.add(name);
        }
        filter
    }

    fn add(&mut self, value: &str) {
        let hash = reference_bloom_hash(value);
        if hash == 0 {
            return;
        }
        let mask = bloom_bits(hash);
        if !self.masks.contains(&mask) {
            self.masks.push(mask);
        }
    }

    fn matches(&self, references: &PackedReferences) -> bool {
        if references.is_empty() {
            return false;
        }
        let bloom = references.bloom;
        self.masks
            .iter()
            .any(|mask| bloom[0] & mask[0] != 0 && bloom[1] & mask[1] != 0)
    }
}

fn member_kind_matches(reference_kind: SymbolKind, target_kind: SymbolKind) -> bool {
    reference_kind == target_kind
        || (reference_kind == SymbolKind::ClassConstant && target_kind == SymbolKind::EnumCase)
}

fn records_target(references: &PackedReferences, record: &ReferenceRecord, target: &str) -> bool {
    if target.is_empty() {
        return false;
    }
    references.string(record.resolved) == target
        || record
            .candidate_values()
            .any(|value| references.value(value) == target)
}

fn may_resolve_global(
    references: &PackedReferences,
    record: &ReferenceRecord,
    target: &SymbolView<'_>,
    case_insensitive: bool,
) -> bool {
    if records_target(references, record, target.id()) {
        return true;
    }
    let target_name = trim_name(target.fully_qualified(), false);
    if target_name.is_empty() {
        return false;
    }
    record.qualified_values().any(|value| {
        let candidate = trim_name(references.value(value), false);
        candidate == target_name || (case_insensitive && eq_ignore_case(candidate, target_name))
    })
}

/// Cheaply rejects references that cannot resolve to `target` before the
/// inheritance-aware resolution runs.
pub(crate) fn reference_may_target(
    references: &PackedReferences,
    record: &ReferenceRecord,
    target: &SymbolView<'_>,
) -> bool {
    match record.kind {
        NameKind::Class => {
            target.kind().is_class_like() && may_resolve_global(references, record, target, true)
        }
        NameKind::Function => {
            target.kind() == SymbolKind::Function
                && may_resolve_global(references, record, target, true)
        }
        NameKind::Constant => {
            target.kind() == SymbolKind::GlobalConstant
                && may_resolve_global(references, record, target, false)
        }
        NameKind::Member => {
            member_kind_matches(record.target_kind, target.kind())
                && eq_lower(
                    trim_name(references.string(record.name), true),
                    trim_name(target.name(), true),
                )
        }
        NameKind::Variable => records_target(references, record, target.id()),
    }
}

/// Resolves packed references against one snapshot, reusing traversal
/// storage across references.
pub(crate) struct TargetResolver<'a> {
    snapshot: &'a Snapshot,
    seen: IdSet<'a>,
    visited: IdSet<'a>,
    pub targets: Vec<&'a str>,
    scratch: Vec<&'a str>,
}

impl<'a> TargetResolver<'a> {
    pub fn new(snapshot: &'a Snapshot) -> Self {
        TargetResolver {
            snapshot,
            seen: IdSet::default(),
            visited: IdSet::default(),
            targets: Vec::new(),
            scratch: Vec::new(),
        }
    }

    pub fn resolve(
        &mut self,
        references: &'a PackedReferences,
        record: &ReferenceRecord,
    ) -> &[&'a str] {
        self.targets.clear();
        self.seen = IdSet::default();
        self.visited = IdSet::default();
        let resolved = references.string(record.resolved);
        match record.kind {
            NameKind::Class => {
                for value in record.qualified_values() {
                    self.append_named(references.value(value), NameIndexKind::Class);
                }
            }
            NameKind::Function => {
                for value in record.qualified_values() {
                    if self.append_named(references.value(value), NameIndexKind::Function) {
                        break;
                    }
                }
            }
            NameKind::Constant => {
                for value in record.qualified_values() {
                    if self.append_named(references.value(value), NameIndexKind::Constant) {
                        break;
                    }
                }
            }
            NameKind::Member => {
                self.append_members(
                    references.ty(record.receiver),
                    references.string(record.name),
                    record.target_kind,
                );
                return &self.targets;
            }
            NameKind::Variable => {
                if self.append_existing(resolved) {
                    return &self.targets;
                }
            }
        }
        if self.targets.is_empty() {
            self.append_existing(resolved);
        }
        if self.targets.is_empty() {
            for value in record.candidate_values() {
                self.append_existing(references.value(value));
            }
        }
        &self.targets
    }

    fn append(&mut self, id: &'a str) -> bool {
        if id.is_empty() || !self.seen.insert(id) {
            return false;
        }
        self.targets.push(id);
        true
    }

    fn append_existing(&mut self, id: &'static str) -> bool {
        if id.is_empty() {
            return false;
        }
        match self.snapshot.symbol_view(id) {
            Some(view) => self.append(view.id()),
            None => false,
        }
    }

    fn append_named(&mut self, name: &str, kind: NameIndexKind) -> bool {
        let mut ids = std::mem::take(&mut self.scratch);
        ids.clear();
        self.snapshot.visit_named(kind, name, &mut |view| {
            ids.push(view.id());
            true
        });
        let found = !ids.is_empty();
        for &id in &ids {
            self.append(id);
        }
        self.scratch = ids;
        found
    }

    fn append_members(&mut self, receiver: &Type, name: &str, target_kind: SymbolKind) {
        match receiver.kind() {
            TypeKind::Union | TypeKind::Intersection => {
                for argument in receiver.arguments() {
                    self.append_members(argument, name, target_kind);
                }
            }
            TypeKind::Object => self.collect_for_class_name(receiver.name(), name, target_kind),
            _ => {}
        }
    }

    fn collect_for_class_name(&mut self, class_name: &str, member: &str, target_kind: SymbolKind) {
        if class_name.is_empty() {
            return;
        }
        let snapshot = self.snapshot;
        let mut classes = Vec::new();
        snapshot.visit_class_views(class_name, |class| {
            classes.push(class);
            true
        });
        for class in classes {
            self.collect_members(class, member, target_kind);
        }
    }

    fn collect_members(&mut self, class: SymbolView<'a>, name: &str, target_kind: SymbolKind) {
        if !self.visited.insert(class.id()) {
            return;
        }
        let snapshot = self.snapshot;
        let mut matches = Vec::new();
        snapshot.visit_member_views(class.id(), name, |member| {
            if member_kind_matches(target_kind, member.kind()) {
                matches.push(member.id());
            }
            true
        });
        for id in matches {
            self.append(id);
        }
        let (traits, extends, implements) = class.hierarchy_names();
        for related in traits.iter().chain(extends).chain(implements) {
            self.collect_for_class_name(related, name, target_kind);
        }
    }
}

impl Snapshot {
    /// Locations of references resolving to `id`, sorted by path and range
    /// for overlays and in path/source order for published generations.
    pub fn references_to(&self, id: &str) -> Vec<ReferenceLocation> {
        if self.base().is_some() {
            return self.overlay_references_to(id);
        }
        let index = self
            .layer
            .reverse_references
            .get_or_init(|| self.build_reverse_references());
        let Some(&(start, count)) = index.spans.get(id) else {
            return Vec::new();
        };
        index.locations[start as usize..(start + count) as usize]
            .iter()
            .map(|&(path, range_start, range_end)| ReferenceLocation {
                path: index.paths[path as usize].clone(),
                range_start,
                range_end,
            })
            .collect()
    }

    pub(crate) fn reverse_reference_stats(&self) -> Option<(usize, usize)> {
        self.layer
            .reverse_references
            .get()
            .map(|index| (index.target_count(), index.location_count()))
    }

    fn build_reverse_references(&self) -> ReverseReferences {
        let packed = self.packed();
        let mut documents: Vec<_> = packed
            .documents
            .iter()
            .filter(|document| {
                packed
                    .path_document(&document.path)
                    .is_some_and(|winner| Arc::ptr_eq(winner, document))
            })
            .collect();
        documents.sort_by(|left, right| left.path.cmp(&right.path));
        let mut buckets: FxHashMap<&'static str, Vec<(u32, u32, u32)>> = FxHashMap::default();
        let mut order: Vec<&'static str> = Vec::new();
        let mut resolver = TargetResolver::new(self);
        for (path_index, document) in documents.iter().enumerate() {
            let references = &document.references;
            for record in references.records() {
                let (start, end): (u32, u32) =
                    (record.range.start().into(), record.range.end().into());
                for &target in resolver.resolve(references, &record) {
                    // Base-generation targets are packed declarations whose IDs
                    // are interned in their document's generation, which this
                    // layer keeps alive (it owns the documents).
                    let Some(target) = self.packed().find(target).map(|reference| {
                        packed
                            .document(reference.document)
                            .symbol(reference.index)
                            .id()
                            .as_str()
                    }) else {
                        continue;
                    };
                    let bucket = buckets.entry(target).or_insert_with(|| {
                        order.push(target);
                        Vec::new()
                    });
                    bucket.push((path_index as u32, start, end));
                }
            }
        }
        let mut index = ReverseReferences {
            paths: documents
                .iter()
                .map(|document| document.path.clone())
                .collect(),
            spans: FxHashMap::default(),
            locations: Vec::new(),
        };
        index.spans.reserve(order.len());
        for target in order {
            let bucket = &buckets[target];
            let start = index.locations.len() as u32;
            index.locations.extend_from_slice(bucket);
            index.spans.insert(target, (start, bucket.len() as u32));
        }
        index.locations.shrink_to_fit();
        index
    }

    fn overlay_references_to(&self, id: &str) -> Vec<ReferenceLocation> {
        let compute = || {
            let Some(target) = self.symbol_view(id) else {
                return Vec::new();
            };
            let filter = TargetFilter::new(&target);
            let mut result = Vec::new();
            let mut resolver = TargetResolver::new(self);
            self.visit_path_references(|path, references| {
                if !filter.matches(references) {
                    return;
                }
                for record in references.records() {
                    if !reference_may_target(references, &record, &target) {
                        continue;
                    }
                    if resolver.resolve(references, &record).contains(&id) {
                        result.push(ReferenceLocation {
                            path: Str::from(path),
                            range_start: record.range.start().into(),
                            range_end: record.range.end().into(),
                        });
                    }
                }
            });
            result.sort();
            result
        };
        match &self.layer.reference_queries {
            Some(cache) => cache.load_or_compute(id, compute),
            None => compute(),
        }
    }
}
