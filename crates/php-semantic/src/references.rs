//! Compact retained reference tables.
//!
//! Each reference is 16 bytes: a start offset plus three words of packed
//! per-document table indexes, counts, and metadata. References that do not
//! fit (more than 65,535 table entries, ranges of 4,095 bytes or more, or
//! more than 15 qualified/candidate values) use an exact fallback record.

use rustc_hash::FxHashMap;
use swlsp_php_types::Type;

use crate::text::go_lower_char;
use swlsp_syntax::TextRange;

use crate::atom::Atom;
use crate::document::{NameKind, Reference};
use crate::symbol::{SymbolId, SymbolKind};

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
struct PackedRef {
    start: u32,
    name_resolved: u32,
    value_receiver: u32,
    meta: u32,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
struct FullRef {
    start: u32,
    end: u32,
    value_start: u32,
    name: u32,
    resolved: u32,
    receiver: u32,
    qualified: u8,
    candidates: u8,
}

const INDEX_MASK: u32 = 0xFFFF;
const RANGE_BITS: u32 = 12;
const RANGE_MASK: u32 = (1 << RANGE_BITS) - 1;
const FULL_SENTINEL: u32 = RANGE_MASK;
const QUALIFIED_SHIFT: u32 = RANGE_BITS;
const CANDIDATE_SHIFT: u32 = QUALIFIED_SHIFT + 4;
const COUNT_MASK: u32 = 0xF;
const KIND_SHIFT: u32 = CANDIDATE_SHIFT + 4;
const TARGET_SHIFT: u32 = KIND_SHIFT + 3;
const FLAGS_SHIFT: u32 = TARGET_SHIFT + 4;
pub(crate) const STATIC_FLAG: u32 = 1;
pub(crate) const WRITE_FLAG: u32 = 2;

/// Unpacked indexes of one reference.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) struct ReferenceRecord {
    pub range: TextRange,
    pub name: u32,
    pub resolved: u32,
    pub receiver: u32,
    pub value_start: u32,
    pub qualified: u32,
    pub candidates: u32,
    pub kind: NameKind,
    pub target_kind: SymbolKind,
    pub flags: u32,
}

impl ReferenceRecord {
    pub fn qualified_values(&self) -> std::ops::Range<usize> {
        self.value_start as usize..(self.value_start + self.qualified) as usize
    }

    pub fn candidate_values(&self) -> std::ops::Range<usize> {
        let start = self.value_start + self.qualified;
        start as usize..(start + self.candidates) as usize
    }
}

#[derive(Debug, Default)]
pub(crate) struct PackedReferences {
    refs: Box<[PackedRef]>,
    full: Box<[FullRef]>,
    /// Index 0 is the empty string.
    strings: Box<[Atom]>,
    /// Index 0 is the unknown type.
    types: Box<[Type]>,
    values: Box<[u32]>,
    pub bloom: [u64; 2],
}

/// FNV-1a over the lowercase runes of `value` after one leading `\` or `$`.
pub(crate) fn reference_bloom_hash(value: &str) -> u64 {
    if value.is_empty() {
        return 0;
    }
    let value = value.strip_prefix(['\\', '$']).unwrap_or(value);
    let mut hash: u64 = 14_695_981_039_346_656_037;
    for character in value.chars() {
        hash ^= u64::from(u32::from(go_lower_char(character)));
        hash = hash.wrapping_mul(1_099_511_628_211);
    }
    hash
}

pub(crate) fn bloom_bits(hash: u64) -> [u64; 2] {
    [1u64 << (hash & 63), 1u64 << ((hash >> 17) & 63)]
}

/// Collects tables while packing or decoding references.
#[derive(Default)]
pub(crate) struct ReferenceTableBuilder {
    refs: Vec<PackedRef>,
    full: Vec<FullRef>,
    strings: Vec<Atom>,
    string_index: FxHashMap<Atom, u32>,
    types: Vec<Type>,
    type_index: FxHashMap<Type, u32>,
    values: Vec<u32>,
}

impl ReferenceTableBuilder {
    pub fn with_capacity(references: usize) -> Self {
        ReferenceTableBuilder {
            refs: Vec::with_capacity(references),
            strings: vec![Atom::EMPTY],
            types: vec![Type::unknown()],
            ..ReferenceTableBuilder::default()
        }
    }

    pub fn string(&mut self, value: Atom) -> u32 {
        if value.is_empty() {
            return 0;
        }
        if let Some(index) = self.string_index.get(&value) {
            return *index;
        }
        let index = self.strings.len() as u32;
        self.strings.push(value);
        self.string_index.insert(value, index);
        index
    }

    pub fn ty(&mut self, value: &Type) -> u32 {
        if value.is_unknown() {
            return 0;
        }
        if let Some(index) = self.type_index.get(value) {
            return *index;
        }
        let index = self.types.len() as u32;
        self.types.push(value.clone());
        self.type_index.insert(value.clone(), index);
        index
    }

    pub fn value(&mut self, value: Atom) {
        let index = self.string(value);
        self.values.push(index);
    }

    pub fn value_count(&self) -> usize {
        self.values.len()
    }

    #[allow(clippy::too_many_arguments)]
    pub fn push(
        &mut self,
        range: TextRange,
        name: u32,
        resolved: u32,
        receiver: u32,
        value_start: u32,
        qualified: u8,
        candidates: u8,
        kind: NameKind,
        target_kind: SymbolKind,
        flags: u32,
    ) {
        let start: u32 = range.start().into();
        let end: u32 = range.end().into();
        let metadata = (kind as u32) << KIND_SHIFT
            | (target_kind as u32) << TARGET_SHIFT
            | (flags & 0xF) << FLAGS_SHIFT;
        let length = end - start;
        let compact = name <= INDEX_MASK
            && resolved <= INDEX_MASK
            && receiver <= INDEX_MASK
            && value_start <= INDEX_MASK
            && u32::from(qualified) <= COUNT_MASK
            && u32::from(candidates) <= COUNT_MASK
            && length < FULL_SENTINEL;
        if compact {
            self.refs.push(PackedRef {
                start,
                name_resolved: name | resolved << 16,
                value_receiver: value_start | receiver << 16,
                meta: length
                    | u32::from(qualified) << QUALIFIED_SHIFT
                    | u32::from(candidates) << CANDIDATE_SHIFT
                    | metadata,
            });
        } else {
            self.full.push(FullRef {
                start,
                end,
                value_start,
                name,
                resolved,
                receiver,
                qualified,
                candidates,
            });
            self.refs.push(PackedRef {
                start,
                name_resolved: 0,
                value_receiver: self.full.len() as u32 - 1,
                meta: FULL_SENTINEL | metadata,
            });
        }
    }

    pub fn finish(self) -> PackedReferences {
        let mut result = PackedReferences {
            refs: self.refs.into_boxed_slice(),
            full: self.full.into_boxed_slice(),
            strings: self.strings.into_boxed_slice(),
            types: self.types.into_boxed_slice(),
            values: self.values.into_boxed_slice(),
            bloom: [0, 0],
        };
        if result.refs.is_empty() {
            return PackedReferences::default();
        }
        result.rebuild_bloom();
        result
    }
}

impl PackedReferences {
    pub fn pack<'a>(references: impl IntoIterator<Item = &'a Reference>) -> PackedReferences {
        let references: Vec<&Reference> = references.into_iter().collect();
        if references.is_empty() {
            return PackedReferences::default();
        }
        let mut builder = ReferenceTableBuilder::with_capacity(references.len());
        for reference in references {
            let value_start = builder.value_count() as u32;
            let qualified =
                &reference.qualified_names()[..reference.qualified_name_count().min(255)];
            for name in qualified {
                builder.value(Atom::intern(name));
            }
            let candidates = &reference.candidate_ids()[..reference.candidate_ids().len().min(255)];
            for candidate in candidates {
                builder.value(Atom::intern(candidate));
            }
            let name = builder.string(Atom::intern(&reference.name));
            let resolved = builder.string(Atom::intern(&reference.resolved));
            let receiver = builder.ty(&reference.receiver);
            let mut flags = 0;
            if reference.is_static {
                flags |= STATIC_FLAG;
            }
            if reference.write {
                flags |= WRITE_FLAG;
            }
            builder.push(
                reference.range,
                name,
                resolved,
                receiver,
                value_start,
                qualified.len() as u8,
                candidates.len() as u8,
                reference.kind,
                reference.target_kind,
                flags,
            );
        }
        builder.finish()
    }

    pub fn len(&self) -> usize {
        self.refs.len()
    }

    pub fn is_empty(&self) -> bool {
        self.refs.is_empty()
    }

    pub fn record(&self, index: usize) -> ReferenceRecord {
        let packed = self.refs[index];
        let meta = packed.meta;
        let kind = NameKind::from_u8(((meta >> KIND_SHIFT) & 0x7) as u8).unwrap_or_default();
        let target_kind =
            SymbolKind::from_u8(((meta >> TARGET_SHIFT) & 0xF) as u8).unwrap_or_default();
        let flags = (meta >> FLAGS_SHIFT) & 0xF;
        if meta & RANGE_MASK == FULL_SENTINEL {
            let full = self.full[packed.value_receiver as usize];
            return ReferenceRecord {
                range: TextRange::new(full.start.into(), full.end.max(full.start).into()),
                name: full.name,
                resolved: full.resolved,
                receiver: full.receiver,
                value_start: full.value_start,
                qualified: u32::from(full.qualified),
                candidates: u32::from(full.candidates),
                kind,
                target_kind,
                flags,
            };
        }
        ReferenceRecord {
            range: TextRange::new(
                packed.start.into(),
                (packed.start + (meta & RANGE_MASK)).into(),
            ),
            name: packed.name_resolved & INDEX_MASK,
            resolved: packed.name_resolved >> 16,
            receiver: packed.value_receiver >> 16,
            value_start: packed.value_receiver & INDEX_MASK,
            qualified: (meta >> QUALIFIED_SHIFT) & COUNT_MASK,
            candidates: (meta >> CANDIDATE_SHIFT) & COUNT_MASK,
            kind,
            target_kind,
            flags,
        }
    }

    pub fn string(&self, index: u32) -> &'static str {
        self.strings
            .get(index as usize)
            .map_or("", |atom| atom.as_str())
    }

    pub fn string_atom(&self, index: u32) -> Atom {
        self.strings
            .get(index as usize)
            .copied()
            .unwrap_or(Atom::EMPTY)
    }

    pub fn ty(&self, index: u32) -> &Type {
        static UNKNOWN: Type = Type::UNKNOWN;
        self.types.get(index as usize).unwrap_or(&UNKNOWN)
    }

    pub fn value(&self, index: usize) -> &'static str {
        self.values
            .get(index)
            .map_or("", |&string| self.string(string))
    }

    /// Strings excluding the implicit empty entry.
    pub fn strings(&self) -> &[Atom] {
        self.strings.get(1..).unwrap_or(&[])
    }

    /// Types excluding the implicit unknown entry.
    pub fn types(&self) -> &[Type] {
        self.types.get(1..).unwrap_or(&[])
    }

    pub fn value_indexes(&self) -> &[u32] {
        &self.values
    }

    pub fn full_count(&self) -> usize {
        self.full.len()
    }

    pub fn rebuild_bloom(&mut self) {
        let mut bloom = [0u64; 2];
        for atom in self.strings() {
            let hash = reference_bloom_hash(atom.as_str());
            if hash == 0 {
                continue;
            }
            let bits = bloom_bits(hash);
            bloom[0] |= bits[0];
            bloom[1] |= bits[1];
        }
        self.bloom = bloom;
    }

    /// Whether every table index of every record is in bounds.
    pub fn validate(&self) -> bool {
        (0..self.refs.len()).all(|index| {
            let packed = self.refs[index];
            if packed.meta & RANGE_MASK == FULL_SENTINEL
                && packed.value_receiver as usize >= self.full.len()
            {
                return false;
            }
            let record = self.record(index);
            (record.name as usize) < self.strings.len()
                && (record.resolved as usize) < self.strings.len()
                && (record.receiver as usize) < self.types.len()
                && record.candidate_values().end <= self.values.len()
        }) && self
            .values
            .iter()
            .all(|&value| (value as usize) < self.strings.len())
    }

    pub fn reference(&self, index: usize) -> Reference {
        let record = self.record(index);
        let mut reference = Reference {
            name: self.string_atom(record.name).name(),
            receiver: self.ty(record.receiver).clone(),
            resolved: SymbolId::from(self.string_atom(record.resolved).name()),
            range: record.range,
            scope: 0,
            kind: record.kind,
            target_kind: record.target_kind,
            is_static: record.flags & STATIC_FLAG != 0,
            write: record.flags & WRITE_FLAG != 0,
            ..Reference::default()
        };
        let qualified: Vec<_> = record
            .qualified_values()
            .map(|value| self.string_atom(self.values[value]).name())
            .collect();
        let candidates: Vec<_> = record
            .candidate_values()
            .map(|value| SymbolId::from(self.string_atom(self.values[value]).name()))
            .collect();
        if !qualified.is_empty() {
            reference.set_qualified_names(qualified);
        }
        if !candidates.is_empty() {
            reference.set_candidate_ids(candidates);
        }
        reference
    }

    pub fn materialize(&self) -> Vec<Reference> {
        (0..self.refs.len())
            .map(|index| self.reference(index))
            .collect()
    }

    /// A copy with every string atom in the pinned generation. Records refer
    /// to strings by index, so only the string table changes.
    pub(crate) fn rehomed(&self) -> PackedReferences {
        PackedReferences {
            refs: self.refs.clone(),
            full: self.full.clone(),
            strings: self.strings.iter().map(|atom| atom.in_target()).collect(),
            types: self.types.clone(),
            values: self.values.clone(),
            bloom: self.bloom,
        }
    }

    pub(crate) fn canonicalize(&mut self, canon: &mut crate::canon::Canon) {
        for value in self.types.iter_mut() {
            *value = canon.ty(value);
        }
    }

    /// Raw encoding view: `(start, end, name, resolved, receiver,
    /// value_start, qualified, candidates, kind, target_kind, flags)`.
    pub(crate) fn records(&self) -> impl Iterator<Item = ReferenceRecord> + '_ {
        (0..self.refs.len()).map(|index| self.record(index))
    }

    /// Builds tables from decoded parts without re-interning.
    pub(crate) fn from_parts(
        records: Vec<ReferenceRecord>,
        strings: Vec<Atom>,
        types: Vec<Type>,
        values: Vec<u32>,
    ) -> Option<PackedReferences> {
        if records.is_empty() {
            return Some(PackedReferences::default());
        }
        let mut builder = ReferenceTableBuilder {
            refs: Vec::with_capacity(records.len()),
            strings: std::iter::once(Atom::EMPTY).chain(strings).collect(),
            types: std::iter::once(Type::unknown()).chain(types).collect(),
            values,
            ..ReferenceTableBuilder::default()
        };
        for record in records {
            if record.qualified > 255 || record.candidates > 255 {
                return None;
            }
            builder.push(
                record.range,
                record.name,
                record.resolved,
                record.receiver,
                record.value_start,
                record.qualified as u8,
                record.candidates as u8,
                record.kind,
                record.target_kind,
                record.flags,
            );
        }
        let result = builder.finish();
        result.validate().then_some(result)
    }
}
