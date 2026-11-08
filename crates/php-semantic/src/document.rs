//! File-local semantic documents: scopes, references, and type facts.

use std::borrow::Cow;
use std::collections::HashMap;

use rustc_hash::FxHashMap;
use swlsp_php_types::Type;

use crate::str::Str;
use swlsp_syntax::{Kind, SyntaxNode, SyntaxNodeExt, TextRange};

use crate::call_contract::{CallContract, clone_call_contracts};
use crate::symbol::{Symbol, SymbolId, SymbolKind};
use crate::text::lower_equals;

pub type ScopeId = u32;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
#[repr(u8)]
pub enum ScopeKind {
    #[default]
    File = 0,
    Namespace,
    Class,
    Function,
    Closure,
    Block,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
#[repr(u8)]
pub enum NameKind {
    #[default]
    Class = 0,
    Function,
    Constant,
    Member,
    Variable,
}

impl NameKind {
    pub fn from_u8(value: u8) -> Option<NameKind> {
        [
            NameKind::Class,
            NameKind::Function,
            NameKind::Constant,
            NameKind::Member,
            NameKind::Variable,
        ]
        .get(usize::from(value))
        .copied()
    }
}

/// Imported names keyed by alias.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct ImportTable {
    pub classes: HashMap<Str, Str>,
    pub functions: HashMap<Str, Str>,
    pub constants: HashMap<Str, Str>,
}

/// A lexical scope. Declarations are document-local symbol indexes in
/// insertion order; duplicates are allowed.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Scope {
    pub id: ScopeId,
    pub kind: ScopeKind,
    pub parent: ScopeId,
    pub range: TextRange,
    pub owner: SymbolId,
    pub namespace: Str,
    pub imports: ImportTable,
    /// Document-local symbol indexes in insertion order.
    pub symbols: Vec<u32>,
}

impl Scope {
    /// Records a declaration's document-local symbol index.
    pub fn add_symbol(&mut self, symbol_index: u32) {
        self.symbols.push(symbol_index);
    }

    /// Declarations matching one normalized lookup name, in insertion order.
    pub fn symbol_ids<'a>(
        &'a self,
        symbols: &'a [Symbol],
        name: &'a str,
    ) -> impl Iterator<Item = &'a SymbolId> + 'a {
        self.symbols_matching(symbols, name)
            .map(|symbol| &symbol.id)
    }

    /// Every declaration in insertion order.
    pub fn all_symbol_ids<'a>(
        &'a self,
        symbols: &'a [Symbol],
    ) -> impl Iterator<Item = &'a SymbolId> + 'a {
        self.symbols
            .iter()
            .filter_map(|&index| symbols.get(index as usize))
            .map(|symbol| &symbol.id)
    }

    pub fn has_symbol(&self, symbols: &[Symbol], name: &str) -> bool {
        self.symbols_matching(symbols, name).next().is_some()
    }

    /// Appends declarations matching `name` to `target` in insertion order.
    pub fn append_symbol_ids(&self, symbols: &[Symbol], target: &mut Vec<SymbolId>, name: &str) {
        target.extend(self.symbol_ids(symbols, name).cloned());
    }

    /// Raw document-local symbol indexes.
    pub fn symbol_indexes(&self) -> &[u32] {
        &self.symbols
    }

    fn symbols_matching<'a>(
        &'a self,
        symbols: &'a [Symbol],
        name: &'a str,
    ) -> impl Iterator<Item = &'a Symbol> + 'a {
        self.symbols
            .iter()
            .filter_map(|&index| symbols.get(index as usize))
            .filter(move |symbol| scope_symbol_matches(symbol, name))
    }
}

/// `normalized_name` is the exact name for variables and constants and the
/// lowercase name otherwise.
fn scope_symbol_matches(symbol: &Symbol, normalized_name: &str) -> bool {
    match symbol.kind {
        SymbolKind::Parameter
        | SymbolKind::Local
        | SymbolKind::GlobalConstant
        | SymbolKind::ClassConstant
        | SymbolKind::EnumCase => &*symbol.name == normalized_name,
        _ => {
            let name = symbol.name.strip_prefix('$').unwrap_or(&symbol.name);
            lower_equals(name, normalized_name)
        }
    }
}

/// A syntactic name use, resolved or unresolved.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Reference {
    pub name: Str,
    pub receiver: Type,
    pub resolved: SymbolId,
    /// Optional qualified-name and candidate side record; use the accessors.
    pub targets: Option<Box<ReferenceTargets>>,
    pub range: TextRange,
    pub scope: ScopeId,
    pub kind: NameKind,
    pub target_kind: SymbolKind,
    pub is_static: bool,
    pub write: bool,
}

/// Collections kept off the common variable-reference record.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct ReferenceTargets {
    qualified: Vec<Str>,
    candidates: Vec<SymbolId>,
}

impl Reference {
    /// Ordered, resolver-normalized lookup candidates.
    pub fn qualified_names(&self) -> &[Str] {
        self.targets
            .as_deref()
            .map_or(&[], |targets| &targets.qualified)
    }

    pub fn qualified_name_count(&self) -> usize {
        self.qualified_names().len()
    }

    pub fn set_qualified_name(&mut self, name: impl Into<Str>) {
        self.set_qualified_names(vec![name.into()]);
    }

    pub fn set_qualified_names(&mut self, names: Vec<Str>) {
        self.targets.get_or_insert_with(Box::default).qualified = names;
        self.release_empty_targets();
    }

    /// Every ambiguous target in resolution order.
    pub fn candidate_ids(&self) -> &[SymbolId] {
        self.targets
            .as_deref()
            .map_or(&[], |targets| &targets.candidates)
    }

    pub fn set_candidate_ids(&mut self, candidates: Vec<SymbolId>) {
        self.targets.get_or_insert_with(Box::default).candidates = candidates;
        self.release_empty_targets();
    }

    pub fn clear_candidate_ids(&mut self) {
        self.set_candidate_ids(Vec::new());
    }

    /// Stores a unique target in `resolved`; a second target promotes both to
    /// candidates and clears `resolved`.
    pub fn add_candidate(&mut self, id: SymbolId) {
        if id.is_empty() {
            return;
        }
        if self.resolved.is_empty() && self.candidate_ids().is_empty() {
            self.resolved = id;
            return;
        }
        let mut candidates = self.candidate_ids().to_vec();
        if !self.resolved.is_empty() {
            candidates.push(std::mem::take(&mut self.resolved));
        }
        candidates.push(id);
        self.set_candidate_ids(candidates);
    }

    #[cfg(test)]
    pub(crate) fn has_targets_record(&self) -> bool {
        self.targets.is_some()
    }

    fn release_empty_targets(&mut self) {
        if self
            .targets
            .as_ref()
            .is_some_and(|targets| targets.qualified.is_empty() && targets.candidates.is_empty())
        {
            self.targets = None;
        }
    }
}

/// A syntax node identity: kind plus byte range.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct NodeId {
    pub kind: Kind,
    pub start: u32,
    pub end: u32,
}

impl Default for NodeId {
    fn default() -> Self {
        NodeId {
            kind: Kind(0),
            start: 0,
            end: 0,
        }
    }
}

pub fn node_identity(node: &SyntaxNode) -> NodeId {
    let range = node.text_range();
    NodeId {
        kind: node.kind(),
        start: range.start().into(),
        end: range.end().into(),
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
#[repr(u8)]
pub enum Confidence {
    #[default]
    Unknown = 0,
    Inferred,
    Documented,
    Declared,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
#[repr(u8)]
pub enum TypeSource {
    #[default]
    Unknown = 0,
    Literal,
    Assignment,
    Native,
    PhpDoc,
    Signature,
    Flow,
    Framework,
}

/// An inferred type for one syntax node.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct TypeFact {
    pub ty: Type,
    pub confidence: Confidence,
    pub source: TypeSource,
    pub origin: TextRange,
    pub reason: Cow<'static, str>,
}

const COMPACT_REASONS: [&str; 23] = [
    "",
    "assignment",
    "foreach value",
    "instanceof",
    "null comparison",
    "flow expression",
    "conditional predicate",
    "by-reference argument",
    "logical condition",
    "truthiness",
    "is_string",
    "is_int",
    "is_integer",
    "is_long",
    "is_float",
    "is_double",
    "is_real",
    "is_bool",
    "is_array",
    "is_callable",
    "is_iterable",
    "is_object",
    "is_null",
];

fn compact_reason(reason: &str) -> Option<u8> {
    COMPACT_REASONS
        .iter()
        .position(|candidate| *candidate == reason)
        .map(|index| index as u8)
}

/// A fact without origin whose reason is one of the built-in inference
/// reasons. 16 bytes.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct CompactTypeFact {
    ty: Type,
    confidence: Confidence,
    source: TypeSource,
    reason: u8,
}

/// Common facts keyed by a lossless 64-bit identity: start(32), length(16),
/// kind(16). Facts spanning more than 65,535 bytes, or with inverted ranges,
/// keep their full identity in `overflow`. Scalar literal types are derived
/// from syntax and need no entry.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct CompactTypeFactTable {
    packed: FxHashMap<u64, CompactTypeFact>,
    overflow: FxHashMap<NodeId, CompactTypeFact>,
}

fn pack_identity(identity: NodeId) -> Option<u64> {
    if identity.end < identity.start {
        return None;
    }
    let length = identity.end - identity.start;
    if length > u32::from(u16::MAX) {
        return None;
    }
    Some(u64::from(identity.start) << 32 | u64::from(length) << 16 | u64::from(identity.kind.0))
}

impl CompactTypeFactTable {
    fn set(&mut self, identity: NodeId, fact: CompactTypeFact) {
        match pack_identity(identity) {
            Some(key) => {
                self.packed.insert(key, fact);
            }
            None => {
                self.overflow.insert(identity, fact);
            }
        }
    }

    fn get(&self, identity: NodeId) -> Option<&CompactTypeFact> {
        match pack_identity(identity) {
            Some(key) => self.packed.get(&key),
            None => self.overflow.get(&identity),
        }
    }

    fn remove(&mut self, identity: NodeId) {
        match pack_identity(identity) {
            Some(key) => {
                self.packed.remove(&key);
            }
            None => {
                self.overflow.remove(&identity);
            }
        }
    }

    fn len(&self) -> usize {
        self.packed.len() + self.overflow.len()
    }
}

/// A diagnostic produced while analyzing a document.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Issue {
    pub range: TextRange,
    pub code: Str,
    pub message: Str,
}

/// An immutable semantic snapshot of one source version.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Document {
    pub path: Str,
    pub version: i64,
    pub workspace_revision: u64,
    pub namespace: Str,
    pub symbols: Vec<Symbol>,
    pub scopes: Vec<Scope>,
    pub references: Vec<Reference>,
    pub call_contracts: Vec<CallContract>,
    /// Detailed facts whose reason text is not one of the built-in reasons.
    pub type_facts: HashMap<NodeId, TypeFact>,
    /// Compact storage of built-in-reason facts; use the accessors.
    pub compact_type_facts: CompactTypeFactTable,
    pub type_aliases: HashMap<Str, Type>,
    pub issues: Vec<Issue>,
}

impl Document {
    /// The declaration/reference view retained by a workspace snapshot.
    /// File-local scopes, locals, parameters, closures, type facts, aliases,
    /// diagnostics, and variable references are omitted; reference scope IDs
    /// are cleared because they address the omitted scope table.
    pub fn workspace_graph(&self) -> Document {
        Document {
            path: self.path.clone(),
            version: self.version,
            workspace_revision: self.workspace_revision,
            namespace: self.namespace.clone(),
            symbols: self
                .symbols
                .iter()
                .filter(|symbol| symbol.kind.is_workspace())
                .cloned()
                .collect(),
            references: self
                .references
                .iter()
                .filter(|reference| reference.kind != NameKind::Variable)
                .map(|reference| Reference {
                    scope: 0,
                    ..reference.clone()
                })
                .collect(),
            call_contracts: clone_call_contracts(&self.call_contracts),
            ..Document::default()
        }
    }

    /// The type of `node`: a stored fact, an intrinsic literal type, or
    /// unknown.
    pub fn type_of(&self, node: &SyntaxNode) -> TypeFact {
        if let Some(mut fact) = self.type_fact(node_identity(node)) {
            if fact.origin == TextRange::default() {
                fact.origin = node.range_trimmed_trivia();
            }
            return fact;
        }
        if let Some(value) = swlsp_php_literal::type_of(node) {
            return TypeFact {
                ty: value,
                confidence: Confidence::Inferred,
                source: TypeSource::Literal,
                origin: node.range_trimmed_trivia(),
                reason: Cow::Borrowed(""),
            };
        }
        TypeFact::default()
    }

    /// The stored fact for a syntax identity.
    pub fn type_fact(&self, identity: NodeId) -> Option<TypeFact> {
        if let Some(fact) = self.type_facts.get(&identity) {
            return Some(fact.clone());
        }
        self.compact_type_facts.get(identity).map(|fact| TypeFact {
            ty: fact.ty.clone(),
            confidence: fact.confidence,
            source: fact.source,
            origin: TextRange::default(),
            reason: Cow::Borrowed(COMPACT_REASONS[usize::from(fact.reason)]),
        })
    }

    /// Prepares compact storage for an estimated fact count.
    pub fn reserve_type_facts(&mut self, capacity: usize) {
        self.compact_type_facts.packed.reserve(capacity);
    }

    /// Stores built-in-reason facts compactly (dropping `origin`, which
    /// [`Document::type_of`] re-derives) and other facts in `type_facts`.
    pub fn set_type_fact(&mut self, identity: NodeId, fact: TypeFact) {
        if let Some(reason) = compact_reason(&fact.reason) {
            self.compact_type_facts.set(
                identity,
                CompactTypeFact {
                    ty: fact.ty,
                    confidence: fact.confidence,
                    source: fact.source,
                    reason,
                },
            );
            self.type_facts.remove(&identity);
            return;
        }
        self.type_facts.insert(identity, fact);
        self.compact_type_facts.remove(identity);
    }

    pub fn delete_type_fact(&mut self, identity: NodeId) {
        self.type_facts.remove(&identity);
        self.compact_type_facts.remove(identity);
    }

    pub fn type_fact_count(&self) -> usize {
        self.type_facts.len() + self.compact_type_facts.len()
    }

    /// Visits every stored fact (detailed facts first, then compact facts in
    /// unspecified order).
    pub fn visit_type_facts(&self, mut visit: impl FnMut(Confidence, TypeSource, &str)) {
        for fact in self.type_facts.values() {
            visit(fact.confidence, fact.source, &fact.reason);
        }
        for fact in self
            .compact_type_facts
            .packed
            .values()
            .chain(self.compact_type_facts.overflow.values())
        {
            visit(
                fact.confidence,
                fact.source,
                COMPACT_REASONS[usize::from(fact.reason)],
            );
        }
    }

    /// The innermost scope containing `offset` (inclusive bounds).
    pub fn scope_at(&self, offset: u32) -> Option<&Scope> {
        let mut best: Option<(u32, &Scope)> = None;
        for scope in &self.scopes {
            let (start, end): (u32, u32) = (scope.range.start().into(), scope.range.end().into());
            if offset < start || offset > end {
                continue;
            }
            let width = end - start;
            if best.is_none_or(|(best_width, _)| width < best_width) {
                best = Some((width, scope));
            }
        }
        best.map(|(_, scope)| scope)
    }
}

/// Profiling counts of a document's type facts.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct TypeFactProfileStats {
    pub total: usize,
    pub detailed: usize,
    pub common: usize,
    pub declared: usize,
    pub inferred_assignment: usize,
    pub inferred_literal: usize,
    pub inferred_signature: usize,
    pub inferred_flow: usize,
    pub other: usize,
}

/// Classifies the detailed and compact fact tables.
pub fn profile_type_facts(document: &Document) -> TypeFactProfileStats {
    let mut stats = TypeFactProfileStats {
        detailed: document.type_facts.len(),
        ..Default::default()
    };
    document.visit_type_facts(|confidence, source, reason| {
        stats.total += 1;
        if confidence == Confidence::Inferred
            && source == TypeSource::Assignment
            && reason.is_empty()
        {
            stats.common += 1;
        }
        match (confidence, source) {
            (Confidence::Declared, _) => stats.declared += 1,
            (Confidence::Inferred, TypeSource::Assignment) => stats.inferred_assignment += 1,
            (Confidence::Inferred, TypeSource::Literal) => stats.inferred_literal += 1,
            (Confidence::Inferred, TypeSource::Signature) => stats.inferred_signature += 1,
            (Confidence::Inferred, TypeSource::Flow) => stats.inferred_flow += 1,
            _ => stats.other += 1,
        }
    });
    stats
}
