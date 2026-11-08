//! Lazily derived reverse hierarchy indexes.

use std::collections::HashSet;
use std::sync::OnceLock;

use rustc_hash::FxHashMap;

use crate::snapshot::Snapshot;
use crate::str::Str;
use crate::symbol::{Flags, Symbol, SymbolId, SymbolKind, Visibility};
use crate::text::normalize_symbol_name;
use crate::view::SymbolView;

/// Type edges and method-implementation edges use separate guards so a
/// type-hierarchy query does not pay to inspect every workspace method.
#[derive(Default)]
pub(crate) struct ReverseHierarchy {
    types: OnceLock<TypeEdges>,
    methods: OnceLock<FxHashMap<SymbolId, Vec<SymbolId>>>,
}

#[derive(Default)]
struct TypeEdges {
    subtypes: FxHashMap<Str, Vec<SymbolId>>,
    trait_consumers: FxHashMap<Str, Vec<SymbolId>>,
    class_aliases: FxHashMap<Str, Vec<SymbolId>>,
}

fn view_id(view: &SymbolView<'_>) -> SymbolId {
    view.symbol_id()
}

/// Normalized class-name keys are interned: the set of class names in a
/// workspace is finite and the keys then cost no allocation per edge.
fn class_key(name: &str) -> Str {
    let lower = normalize_symbol_name(name, false);
    crate::atom::intern_str(&lower)
}

fn push_unique(ids: &mut Vec<SymbolId>, id: SymbolId) {
    if !ids.contains(&id) {
        ids.push(id);
    }
}

impl Snapshot {
    /// Classes and interfaces directly extending or implementing `name`.
    pub fn direct_subtypes(&self, name: &str) -> Vec<Symbol> {
        self.type_edges_lookup(name, |edges| &edges.subtypes)
    }

    /// Classes directly using the named trait.
    pub fn trait_consumers(&self, name: &str) -> Vec<Symbol> {
        self.type_edges_lookup(name, |edges| &edges.trait_consumers)
    }

    /// Synthetic `class_alias` declarations targeting `name`.
    pub fn class_aliases(&self, name: &str) -> Vec<Symbol> {
        self.type_edges_lookup(name, |edges| &edges.class_aliases)
    }

    fn type_edges_lookup(
        &self,
        name: &str,
        table: impl Fn(&TypeEdges) -> &FxHashMap<Str, Vec<SymbolId>>,
    ) -> Vec<Symbol> {
        if name.is_empty() {
            return Vec::new();
        }
        let edges = self
            .layer
            .reverse_hierarchy
            .types
            .get_or_init(|| self.build_type_edges());
        table(edges)
            .get(normalize_symbol_name(name, false).as_str())
            .map(|ids| ids.iter().filter_map(|id| self.symbol(id)).collect())
            .unwrap_or_default()
    }

    /// The target of a synthetic `class_alias` declaration.
    pub fn class_alias_target(&self, id: &str) -> Option<Symbol> {
        let view = self.symbol_view(id)?;
        if !view.flags().has(Flags::CLASS_ALIAS) {
            return None;
        }
        let (_, extends, _) = view.hierarchy_names();
        if extends.len() != 1 {
            return None;
        }
        self.classes(&extends[0]).into_iter().next()
    }

    /// Workspace methods implementing or overriding the selected method,
    /// including through intermediate classes that do not redeclare it.
    pub fn method_overrides(&self, id: &str) -> Vec<Symbol> {
        if id.is_empty() {
            return Vec::new();
        }
        let methods = self
            .layer
            .reverse_hierarchy
            .methods
            .get_or_init(|| self.build_method_edges());
        methods
            .get(id)
            .map(|ids| ids.iter().filter_map(|id| self.symbol(id)).collect())
            .unwrap_or_default()
    }

    /// Declarations named directly by a class-like declaration's extends and
    /// implements clauses. Trait use is composition, not subtyping.
    pub fn direct_supertypes(&self, id: &str) -> Vec<Symbol> {
        let Some(view) = self.symbol_view(id) else {
            return Vec::new();
        };
        if !view.kind().is_class_like() {
            return Vec::new();
        }
        let (_, extends, implements) = view.hierarchy_names();
        let mut result: Vec<Symbol> = Vec::new();
        let mut seen = HashSet::new();
        for name in extends.iter().chain(implements) {
            self.visit_class_views(name, |class| {
                if seen.insert(class.id().to_owned()) {
                    result.push(class.materialize());
                }
                true
            });
        }
        result.sort_by(|left, right| {
            left.path
                .cmp(&right.path)
                .then_with(|| {
                    left.selection_range
                        .start()
                        .cmp(&right.selection_range.start())
                })
                .then_with(|| left.fully_qualified.cmp(&right.fully_qualified))
        });
        result
    }

    fn build_type_edges(&self) -> TypeEdges {
        let mut edges = TypeEdges::default();
        for class in self.global_class_views() {
            let (traits, extends, implements) = class.hierarchy_names();
            if class.flags().has(Flags::CLASS_ALIAS) && extends.len() == 1 {
                push_unique(
                    edges
                        .class_aliases
                        .entry(class_key(&extends[0]))
                        .or_default(),
                    view_id(&class),
                );
                continue;
            }
            for parent in extends.iter().chain(implements) {
                push_unique(
                    edges.subtypes.entry(class_key(parent)).or_default(),
                    view_id(&class),
                );
            }
            for trait_name in traits {
                push_unique(
                    edges
                        .trait_consumers
                        .entry(class_key(trait_name))
                        .or_default(),
                    view_id(&class),
                );
            }
        }
        for ids in edges
            .subtypes
            .values_mut()
            .chain(edges.trait_consumers.values_mut())
            .chain(edges.class_aliases.values_mut())
        {
            self.sort_hierarchy_ids(ids);
        }
        edges
    }

    fn build_method_edges(&self) -> FxHashMap<SymbolId, Vec<SymbolId>> {
        let mut overrides: FxHashMap<SymbolId, Vec<SymbolId>> = FxHashMap::default();
        for class in self.global_class_views() {
            for member in self.member_views_of(class.id()) {
                if member.kind() != SymbolKind::Method || member.visibility() == Visibility::Private
                {
                    continue;
                }
                self.index_ancestor_method_overrides(&mut overrides, &class, &member);
            }
        }
        for ids in overrides.values_mut() {
            self.sort_hierarchy_ids(ids);
        }
        overrides
    }

    fn index_ancestor_method_overrides(
        &self,
        overrides: &mut FxHashMap<SymbolId, Vec<SymbolId>>,
        class: &SymbolView<'_>,
        method: &SymbolView<'_>,
    ) {
        let mut visited = HashSet::new();
        let (traits, extends, implements) = class.hierarchy_names();
        for parent in traits.iter().chain(extends).chain(implements) {
            self.visit_ancestor(overrides, parent, method, &mut visited);
        }
    }

    fn visit_ancestor(
        &self,
        overrides: &mut FxHashMap<SymbolId, Vec<SymbolId>>,
        name: &str,
        method: &SymbolView<'_>,
        visited: &mut HashSet<String>,
    ) {
        let key = normalize_symbol_name(name, false);
        if key.is_empty() || !visited.insert(key) {
            return;
        }
        let mut parents = Vec::new();
        self.visit_class_views(name, |parent| {
            parents.push(parent);
            true
        });
        for parent in parents {
            self.visit_member_views(parent.id(), method.name(), |candidate| {
                if candidate.kind() == SymbolKind::Method
                    && candidate.visibility() != Visibility::Private
                {
                    push_unique(
                        overrides.entry(view_id(&candidate)).or_default(),
                        view_id(method),
                    );
                }
                true
            });
            let (traits, extends, implements) = parent.hierarchy_names();
            for ancestor in traits.iter().chain(extends).chain(implements) {
                self.visit_ancestor(overrides, ancestor, method, visited);
            }
        }
    }

    fn sort_hierarchy_ids(&self, ids: &mut [SymbolId]) {
        ids.sort_by(
            |left, right| match (self.symbol_view(left), self.symbol_view(right)) {
                (Some(left), Some(right)) => left
                    .path()
                    .cmp(right.path())
                    .then_with(|| left.range().start().cmp(&right.range().start()))
                    .then_with(|| left.fully_qualified().cmp(right.fully_qualified())),
                _ => left.as_str().cmp(right.as_str()),
            },
        );
    }
}
