//! Opt-in storage profiling of a published generation.

use std::mem::size_of;

use crate::packed::{PackedParameter, PackedSymbol, SymbolDetail};
use crate::snapshot::{NameIndexKind, Snapshot, SymRef};

/// Cardinality and approximate retained size of the immutable declaration
/// and reference graph of the base generation. The fields describe the in-memory
/// layout.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct WorkspaceStorageStats {
    pub documents: usize,
    pub symbols: usize,
    pub references: usize,

    pub symbol_index_entries: usize,
    pub path_index_entries: usize,
    pub class_names: usize,
    pub class_ids: usize,
    pub function_names: usize,
    pub function_ids: usize,
    pub constant_names: usize,
    pub constant_ids: usize,
    pub member_containers: usize,
    pub member_names: usize,
    pub member_ids: usize,
    pub max_members_per_container: usize,
    pub global_ids: usize,

    pub symbol_range_extras: usize,
    pub symbol_type_extras: usize,
    pub hierarchies: usize,
    pub details: usize,
    pub lazy_details: usize,
    pub parameters: usize,
    pub parameter_extras: usize,
    pub signatures: usize,
    pub metadata: usize,

    pub reference_strings: usize,
    pub reference_types: usize,
    pub reference_values: usize,
    pub reference_full_records: usize,

    pub reverse_reference_targets: usize,
    pub reverse_reference_locations: usize,

    /// Approximate bytes owned by the packed documents and snapshot indexes,
    /// excluding shared type nodes and the atom table.
    pub estimated_graph_bytes: usize,
    pub atoms: usize,
    pub atom_bytes: usize,
    pub estimated_atom_table_bytes: usize,
}

impl Snapshot {
    /// Storage statistics of the base generation. Never loads lazy details.
    pub fn workspace_storage_stats(&self) -> WorkspaceStorageStats {
        let mut current = self;
        while let Some(base) = current.base() {
            current = base;
        }
        let packed = current.packed();
        let mut stats = WorkspaceStorageStats {
            documents: packed.documents.len(),
            ..Default::default()
        };
        let mut bytes = 0usize;
        for document in &packed.documents {
            stats.symbols += document.symbols.len();
            stats.references += document.references.len();
            stats.symbol_range_extras += document.range_extras.len();
            stats.symbol_type_extras += document.type_extras.len();
            bytes += document.symbols.len() * size_of::<PackedSymbol>()
                + document.range_extras.len() * size_of::<(u32, [swlsp_syntax::TextRange; 3])>()
                + document.type_extras.len() * size_of::<(u32, [swlsp_php_types::Type; 4])>()
                + document.path.len()
                + 128;
            for symbol in document.symbols.iter() {
                if let Some(hierarchy) = &symbol.hierarchy {
                    stats.hierarchies += 1;
                    bytes += 16
                        + size_of::<crate::symbol::SymbolHierarchy>()
                        + (hierarchy.extends.len()
                            + hierarchy.implements.len()
                            + hierarchy.traits.len())
                            * 16
                        + (hierarchy.extends_types.len()
                            + hierarchy.implements_types.len()
                            + hierarchy.trait_types.len())
                            * 8;
                }
                if symbol.lazy_detail() {
                    stats.lazy_details += 1;
                }
                if let Some(detail) = &symbol.detail {
                    stats.details += 1;
                    stats.parameters += detail.parameters.len();
                    bytes += 16
                        + size_of::<SymbolDetail>()
                        + detail.parameters.len() * size_of::<PackedParameter>();
                    for parameter in detail.parameters.iter() {
                        if parameter.extras.is_some() {
                            stats.parameter_extras += 1;
                            bytes += size_of::<crate::packed::ParameterExtras>();
                        }
                    }
                    if let Some(signature) = detail.signature.get() {
                        stats.signatures += 1;
                        bytes += 16
                            + size_of::<crate::symbol::SignatureExtras>()
                            + signature.templates.len()
                                * size_of::<crate::symbol::TemplateParameter>()
                            + signature.throws.len() * 8
                            + signature.assertions.len()
                                * size_of::<crate::symbol::TypeAssertion>()
                            + signature.literal_returns.len()
                                * size_of::<crate::symbol::LiteralReturn>()
                            + signature.constant_returns.len()
                                * size_of::<crate::symbol::ConstantReturn>();
                    }
                    if let Some(metadata) = detail.metadata.get() {
                        stats.metadata += 1;
                        bytes += 16
                            + size_of::<crate::symbol::SymbolMetadata>()
                            + metadata.attributes.len() * size_of::<crate::symbol::Attribute>()
                            + metadata.constant_array.len()
                                * size_of::<crate::symbol::ConstantArrayItem>()
                            + metadata.doc_summary.len();
                    }
                }
            }
            let references = &document.references;
            stats.reference_strings += references.strings().len();
            stats.reference_types += references.types().len();
            stats.reference_values += references.value_indexes().len();
            stats.reference_full_records += references.full_count();
            bytes += references.len() * 16
                + references.full_count() * 32
                + (references.strings().len() + 1) * 4
                + (references.types().len() + 1) * 8
                + references.value_indexes().len() * 4;
        }
        stats.symbol_index_entries = packed.symbol_count();
        stats.path_index_entries = packed.documents.len();
        (stats.class_names, stats.class_ids) = packed.name_count(NameIndexKind::Class);
        (stats.function_names, stats.function_ids) = packed.name_count(NameIndexKind::Function);
        (stats.constant_names, stats.constant_ids) = packed.name_count(NameIndexKind::Constant);
        (
            stats.member_containers,
            stats.member_names,
            stats.member_ids,
        ) = packed.member_stats();
        stats.max_members_per_container = packed.member_container_sizes().max().unwrap_or(0);
        stats.global_ids = packed.globals.len();
        bytes +=
            (stats.symbol_index_entries + stats.path_index_entries) * (size_of::<SymRef>() + 1) * 8
                / 7
                + (stats.class_names + stats.function_names + stats.constant_names) * 17 * 8 / 7
                + (stats.class_ids + stats.function_ids + stats.constant_ids) * size_of::<SymRef>()
                + stats.member_containers * 13 * 8 / 7
                + stats.member_names * 8
                + stats.member_ids * size_of::<SymRef>()
                + stats.global_ids * size_of::<SymRef>();
        if let Some((targets, locations)) = current.reverse_reference_stats() {
            stats.reverse_reference_targets = targets;
            stats.reverse_reference_locations = locations;
            bytes += targets * 25 * 8 / 7 + locations * 12;
        }
        stats.estimated_graph_bytes = bytes;
        (
            stats.atoms,
            stats.atom_bytes,
            stats.estimated_atom_table_bytes,
        ) = crate::atom::atom_table_stats();
        stats
    }
}
