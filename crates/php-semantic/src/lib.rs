//! PHP symbols, scopes, references, and immutable document/workspace
//! generations (port of `internal/php/semantic`).
//!
//! # Representation
//!
//! The retained workspace graph is the memory-critical structure of the
//! language server, so it does not mirror Go's structs:
//!
//! - Published declarations and references store interned
//!   [`atom`](crate) IDs (4 bytes) for identifiers, in a generational table
//!   that the store collects when it is mostly garbage. Only published
//!   graphs intern; open-document overlays and binder output use
//!   reference-counted [`Str`]s, so editing does not grow the table.
//!   Materialized names point into the atom arena without allocating.
//! - A packed declaration is 72 bytes: four atoms, a bit-packed range triple,
//!   two type slots with per-field selectors, and two optional shared side
//!   records (hierarchy edges; signature and metadata). Rare values that do
//!   not fit (wide subranges, more than two distinct types) use exact sparse
//!   side tables.
//! - A packed reference is 16 bytes with per-document string/type tables.
//! - Snapshot indexes are keyless hash tables over 8-byte declaration
//!   references; names are hashed and compared case-insensitively from the
//!   stored declarations, so lookups never allocate.
//! - Materializing a [`Symbol`] clones `Arc`-shared side records instead of
//!   copying collections.

mod atom;
mod attribute;
mod call_contract;
mod canon;
mod codec;
mod document;
mod hierarchy;
mod packed;
mod references;
mod relations;
mod resolve;
mod snapshot;
mod stats;
mod store;
mod str;
mod symbol;
mod text;
mod view;
mod workspace;

#[cfg(test)]
mod tests;

pub use atom::{AtomGenerationStats, atom_generation_stats, atom_table_stats, empty_name};
pub use attribute::{Deprecation, attribute_named, deprecation_of};
pub use call_contract::{
    CallContract, CallMapEntry, CallReturnContract, CallReturnKind, CallTarget, CallTargetKind,
    CallValue, CallValueKind, ExpectedArgumentContract,
};
pub use codec::DecodeError;
pub use document::{
    CompactTypeFactTable, Confidence, Document, ImportTable, Issue, NameKind, NodeId, Reference,
    ReferenceTargets, Scope, ScopeId, ScopeKind, TypeFact, TypeFactProfileStats, TypeSource,
    node_identity, profile_type_facts,
};
pub use snapshot::{ReferenceLocation, Snapshot};
pub use stats::WorkspaceStorageStats;
pub use store::{Store, WorkspaceRestoreCapacity};
pub use str::Str;
pub use symbol::{
    Attribute, AttributeArgument, AttributeArrayItem, AttributeValue, AttributeValueKind,
    ConstantArrayItem, ConstantReturn, Flags, LiteralReturn, Parameter, Shared, SideRecord,
    SignatureExtras, Symbol, SymbolHierarchy, SymbolId, SymbolKind, SymbolMetadata,
    TemplateParameter, TraitAlias, TypeAssertion, Visibility, anonymous_class_name, new_symbol_id,
};
pub use view::SymbolView;
pub use workspace::{
    LoadError, WorkspaceGraph, WorkspaceGraphDecoder, WorkspaceGraphDetacher, WorkspaceGraphLoader,
};
