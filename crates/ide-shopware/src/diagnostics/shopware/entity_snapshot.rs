//! Entity schema snapshot validation.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{
    BoxError, Context, DiagnosticId, InspectionScope, InspectionSpec, Problem, TextDocument,
};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_shopware_entityschema as entityschema;
use swlsp_syntax::{TextRange, TextSize};
use swlsp_uriutil::filepath;

use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

/// Validates committed entity schema snapshots, their history graph, referenced
/// migrations and schema drift.
#[derive(Clone, Default)]
pub struct EntitySnapshotAnalyzer {
    entities: Option<Arc<entityschema::IndexedCatalog>>,
}

impl EntitySnapshotAnalyzer {
    /// `None` scans plugin sources from disk (focused tests without a
    /// workspace index).
    pub fn new(catalog: Option<Arc<entityschema::IndexedCatalog>>) -> Self {
        Self { entities: catalog }
    }
}

impl DeclaredAnalyzer for EntitySnapshotAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "shopware.entity_snapshot",
        description: "Entity schema snapshots that no longer match the entity definitions or their migrations.",
        domain: "shopware.entitySchema",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Json],
        source: "shopware-lsp",
        codes: &[
            "shopware.entity_snapshot.invalid",
            "shopware.entity_snapshot.graph",
            "shopware.entity_snapshot.parent_missing",
            "shopware.entity_snapshot.reconcile",
            "shopware.entity_snapshot.migration_missing",
            "shopware.entity_snapshot.migration_changed",
            "shopware.entity_snapshot.schema_drift",
        ],
    };
}

impl ProblemAnalyzer for EntitySnapshotAnalyzer {
    fn input_revision(&self, document: &swlsp_lsp::TextDocument) -> Option<u64> {
        // Only entity snapshots are checked, against the entity catalog, the
        // other snapshots of the plugin (read from disk, stamped here) and
        // the referenced migrations (PHP files the catalog's PHP index sees
        // change).
        if !document.uri.to_lowercase().ends_with(".snapshot.json") {
            return Some(0);
        }
        // Without the indexed catalog the schema is scanned from disk.
        let entities = self.entities.as_ref()?;
        let path = swlsp_uriutil::path(&document.uri).ok()?;
        let plugin_root = snapshot_plugin_root(&path)?;
        Some(
            entities
                .revision()
                .wrapping_add(snapshot_directory_stamp(&plugin_root)),
        )
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        if !document.uri.to_lowercase().ends_with(".snapshot.json")
            || !filepath::to_slash(&document.uri).contains("/src/Resources/shopware-lsp/schema/")
        {
            return Ok(Vec::new());
        }
        let full_range = TextRange::new(TextSize::from(0), TextSize::of(document.source()));
        let problem = |id: &str, message: String, severity: DiagnosticSeverity| Problem {
            id: DiagnosticId::from(id),
            range: full_range,
            message,
            source: "shopware-lsp".to_owned(),
            severity: Some(severity),
            ..Problem::default()
        };
        let current = match entityschema::parse_snapshot(document.source().as_bytes()) {
            Ok(current) => current,
            Err(error) => {
                return Ok(vec![problem(
                    "shopware.entity_snapshot.invalid",
                    error.to_string(),
                    DiagnosticSeverity::ERROR,
                )]);
            }
        };
        let path = swlsp_uriutil::path(&document.uri)?;
        let Some(plugin_root) = snapshot_plugin_root(&path) else {
            return Ok(Vec::new());
        };
        let mut files = match entityschema::read_snapshots(&plugin_root) {
            Ok(files) => files,
            Err(error) => {
                return Ok(vec![problem(
                    "shopware.entity_snapshot.invalid",
                    error.to_string(),
                    DiagnosticSeverity::ERROR,
                )]);
            }
        };
        // The current editor buffer may be newer than disk. Substitute it
        // before graph validation so saves do not produce a transient
        // stale-ID report.
        let cleaned = filepath::clean(&path);
        for file in &mut files {
            if filepath::clean(&file.path) == cleaned {
                file.snapshot = current.clone();
            }
        }
        let graph = match entityschema::build_snapshot_graph(&files) {
            Ok(graph) => graph,
            Err(error) => {
                return Ok(vec![problem(
                    "shopware.entity_snapshot.graph",
                    error.to_string(),
                    DiagnosticSeverity::ERROR,
                )]);
            }
        };
        let mut problems = Vec::new();
        if let Some(missing) = graph.missing.get(&current.id)
            && !missing.is_empty()
        {
            problems.push(problem(
                "shopware.entity_snapshot.parent_missing",
                format!("Snapshot parents are missing: {}", missing.join(", ")),
                DiagnosticSeverity::ERROR,
            ));
        }
        if graph.leaves.len() > 1 {
            if current.id == graph.leaves[0].snapshot.id {
                problems.push(problem(
                    "shopware.entity_snapshot.reconcile",
                    format!(
                        "Snapshot history has {} leaves and must be reconciled",
                        graph.leaves.len()
                    ),
                    DiagnosticSeverity::ERROR,
                ));
            }
            return Ok(problems);
        }
        if graph.leaves.len() != 1 {
            return Ok(problems);
        }
        let leaf = &graph.leaves[0].snapshot;
        if current.id != leaf.id {
            return Ok(problems);
        }
        let mut checked = HashSet::new();
        for snapshot_file in graph.files.values() {
            for migration in &snapshot_file.snapshot.migrations {
                if !checked.insert(migration.path.clone()) {
                    continue;
                }
                let migration_path =
                    filepath::join(&[plugin_root.as_str(), &filepath::from_slash(&migration.path)]);
                match std::fs::read(&migration_path) {
                    Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                        problems.push(problem(
                            "shopware.entity_snapshot.migration_missing",
                            format!("Referenced migration is missing: {}", migration.path),
                            DiagnosticSeverity::ERROR,
                        ));
                    }
                    Err(error) => return Err(error.into()),
                    Ok(content) => {
                        if entityschema::file_sha256(&content) != migration.sha256 {
                            problems.push(problem(
                                "shopware.entity_snapshot.migration_changed",
                                format!(
                                    "Referenced migration changed after the snapshot was committed: {}",
                                    migration.path
                                ),
                                DiagnosticSeverity::ERROR,
                            ));
                        }
                    }
                }
            }
        }
        ctx.check()?;
        let scanned = match &self.entities {
            Some(entities) => entities.scan(&plugin_root, None),
            // Focused importer tests do not construct a production workspace
            // index.
            None => entityschema::scan_plugin_schema(&plugin_root),
        };
        let scanned = match scanned {
            Ok((scanned, _)) => scanned,
            Err(entityschema::Error::IndexNotReady) => return Ok(problems),
            Err(error) => return Err(error.into()),
        };
        let scanned = entityschema::restore_snapshot_only_indexes(&scanned, &leaf.schema);
        let left = serde_json::to_vec(&scanned.normalize())?;
        let right = serde_json::to_vec(&leaf.schema.normalize())?;
        if left != right {
            problems.push(problem(
                "shopware.entity_snapshot.schema_drift",
                "Entity definitions differ from the latest committed schema snapshot".to_owned(),
                DiagnosticSeverity::WARNING,
            ));
        }
        Ok(problems)
    }
}

/// The plugin root of `<root>/src/Resources/shopware-lsp/schema/<file>`.
fn snapshot_plugin_root(path: &str) -> Option<String> {
    let directory = filepath::dir(path);
    let shopware_lsp = filepath::dir(&directory);
    let resources = filepath::dir(&shopware_lsp);
    let src = filepath::dir(&resources);
    if filepath::base(&directory) != "schema"
        || filepath::base(&shopware_lsp) != "shopware-lsp"
        || filepath::base(&resources) != "Resources"
        || filepath::base(&src) != "src"
    {
        return None;
    }
    Some(filepath::dir(&src))
}

/// A hash of the plugin's snapshot files by name, size and modification time.
fn snapshot_directory_stamp(plugin_root: &str) -> u64 {
    use std::hash::{DefaultHasher, Hash, Hasher};
    let mut hasher = DefaultHasher::new();
    let directory = filepath::join(&[plugin_root, entityschema::SNAPSHOT_RELATIVE_DIRECTORY]);
    if let Ok(entries) = std::fs::read_dir(&directory) {
        let mut stamps: Vec<_> = entries
            .flatten()
            .filter_map(|entry| {
                let metadata = entry.metadata().ok()?;
                Some((entry.file_name(), metadata.len(), metadata.modified().ok()))
            })
            .collect();
        stamps.sort();
        stamps.hash(&mut hasher);
    }
    hasher.finish()
}
