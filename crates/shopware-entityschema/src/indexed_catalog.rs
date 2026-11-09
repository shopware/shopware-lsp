//! Class-based schema discovery from the PHP semantic generation. It owns no
//! watcher; it only memoizes the DAL subtype selection of the current immutable
//! semantic generation and the import results of unchanged sources.

use std::collections::HashMap;
use std::sync::{Arc, Weak};

use std::sync::Mutex;
use swlsp_indexer::WorkspaceSymbolCatalog;
use swlsp_php::PhpIndex;

use swlsp_php_semantic::{Flags, Snapshot, Symbol, SymbolKind};

use crate::context::{
    ImportMemo, IndexedClassDeclaration, ScannedDefinition,
    scan_indexed_plugin_schema_with_enricher,
};
use crate::model::*;
use crate::source_index::SourceIndex;
use swlsp_uriutil::filepath;

pub struct IndexedCatalog {
    php: PhpIndex,
    sources: Option<Arc<SourceIndex>>,
    readiness: Option<Arc<WorkspaceSymbolCatalog>>,
    /// DAL subtypes of the last semantic generation. Subtype checks dominate
    /// a scan, and generations are immutable, so they are computed once per
    /// generation (identified by the published `Arc`s; held weakly so an old
    /// generation is never kept alive) for every root.
    selection: Mutex<Option<Selection>>,
    /// Import results reused by later scans while their source and the
    /// answers of their relation lookups are unchanged.
    imports: ImportMemo,
}

struct Selection {
    symbols: Weak<[Symbol]>,
    snapshot: Weak<Snapshot>,
    candidates: Arc<[Candidate]>,
}

struct Candidate {
    path: String,
    class: String,
    parents: Vec<String>,
    abstract_class: bool,
}

const DAL_BASES: [&str; 5] = [
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityTranslationDefinition",
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\MappingEntityDefinition",
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition",
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\BulkEntityExtension",
    "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityExtension",
];

impl IndexedCatalog {
    pub fn new(
        php: PhpIndex,
        sources: Option<Arc<SourceIndex>>,
        readiness: Option<Arc<WorkspaceSymbolCatalog>>,
    ) -> Self {
        IndexedCatalog {
            php,
            sources,
            readiness,
            selection: Mutex::new(None),
            imports: ImportMemo::default(),
        }
    }

    /// Every class-like DAL subtype with a path, in symbol order.
    fn candidates(&self) -> Arc<[Candidate]> {
        let symbols = self.php.class_symbols_view();
        let snapshot = self.php.semantic_snapshot();
        let mut selection = self
            .selection
            .lock()
            .unwrap_or_else(|poison| poison.into_inner());
        if let Some(cached) = selection.as_ref()
            && Weak::ptr_eq(&cached.symbols, &Arc::downgrade(&symbols))
            && Weak::ptr_eq(&cached.snapshot, &Arc::downgrade(&snapshot))
        {
            return cached.candidates.clone();
        }
        let candidates: Arc<[Candidate]> = symbols
            .iter()
            .filter(|symbol| {
                symbol.is_class_like()
                    && !symbol.path.is_empty()
                    && !symbol.fully_qualified.is_empty()
                    && DAL_BASES
                        .iter()
                        .any(|base| snapshot.is_subtype_of(&symbol.fully_qualified, base))
            })
            .map(|symbol| Candidate {
                path: symbol.path.to_string(),
                class: symbol.fully_qualified.to_string(),
                parents: symbol.extends().iter().map(ToString::to_string).collect(),
                abstract_class: symbol.flags.has(Flags::ABSTRACT),
            })
            .collect();
        *selection = Some(Selection {
            symbols: Arc::downgrade(&symbols),
            snapshot: Arc::downgrade(&snapshot),
            candidates: candidates.clone(),
        });
        candidates
    }

    /// Advances whenever [`Self::scan`] may answer differently: the PHP index
    /// (with its type extensions), the entity sources and readiness, which
    /// can flip back (doubling keeps the pairs distinct).
    pub fn revision(&self) -> u64 {
        use swlsp_indexer::Revisioned;
        let ready = match &self.readiness {
            Some(readiness) => readiness.ready().unwrap_or(false),
            None => true,
        };
        (self.php.revision() + self.sources.revision()) * 2 + u64::from(ready)
    }

    pub fn scan(
        &self,
        plugin_root: &str,
        external: Option<RelationLookup>,
    ) -> Result<(Schema, Vec<ScannedDefinition>)> {
        if let Some(readiness) = &self.readiness
            && !readiness.ready()?
        {
            return Err(Error::IndexNotReady);
        }
        let root = filepath::clean(plugin_root);
        let candidates = self.candidates();
        let mut declarations = Vec::new();
        let mut source_by_path: HashMap<String, String> = HashMap::new();
        for candidate in candidates.iter() {
            if !crate::context::within_directory(&root, &candidate.path) {
                continue;
            }
            let mut declaration = IndexedClassDeclaration {
                path: candidate.path.clone(),
                class: candidate.class.clone(),
                parents: candidate.parents.clone(),
                abstract_class: candidate.abstract_class,
                source: String::new(),
            };
            if let Some(sources) = &self.sources
                && !declaration.abstract_class
            {
                if let Some(source) = source_by_path.get(&declaration.path) {
                    declaration.source = source.clone();
                } else if let Some(source) = sources.source(&declaration.path)? {
                    source_by_path.insert(declaration.path.clone(), source.clone());
                    declaration.source = source;
                }
            }
            declarations.push(declaration);
        }
        let enrich = |spec: &mut EntitySpec| self.enrich_spec(spec);
        scan_indexed_plugin_schema_with_enricher(
            &root,
            &declarations,
            external,
            self.sources.is_none(),
            Some(&enrich),
            Some(&self.imports),
        )
    }

    /// Resolves semantic information a field constructor does not carry, such
    /// as the database backing type of an EnumField's enum.
    pub fn enrich_spec(&self, spec: &mut EntitySpec) {
        let enrich = |fields: &mut [FieldSpec]| {
            for field in fields {
                self.enrich_enum_field(field);
            }
        };
        enrich(&mut spec.fields);
        if let Some(behavior) = spec.definition_behavior.as_mut() {
            enrich(&mut behavior.default_fields);
            enrich(&mut behavior.base_fields);
        }
        if let Some(behavior) = spec
            .translation
            .as_mut()
            .and_then(|translation| translation.definition_behavior.as_mut())
        {
            enrich(&mut behavior.default_fields);
            enrich(&mut behavior.base_fields);
        }
        for target in &mut spec.bulk_extensions {
            enrich(&mut target.fields);
        }
    }

    fn enrich_enum_field(&self, field: &mut FieldSpec) {
        if field.kind != FIELD_ENUM
            || !field.enum_backing_type.is_empty()
            || field.enum_class.is_empty()
        {
            return;
        }
        let Some(enumeration) = self.php.find_class(&field.enum_class) else {
            return;
        };
        if enumeration.kind != SymbolKind::Enum {
            return;
        }
        for member in self
            .php
            .semantic_snapshot()
            .members(enumeration.id.as_str(), "value")
        {
            if member.kind != SymbolKind::Property {
                continue;
            }
            match member.ty.to_string().as_str() {
                "string" => field.enum_backing_type = "string".to_owned(),
                "int" => field.enum_backing_type = "int".to_owned(),
                _ => {}
            }
            if !field.enum_backing_type.is_empty() {
                return;
            }
        }
    }
}
