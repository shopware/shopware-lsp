//! Port of Go `entity_snapshot.go`.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_lsp::{BoxError, Context, DiagnosticId, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_shopware_entityschema as entityschema;
use swlsp_syntax::{TextRange, TextSize};
use swlsp_uriutil::filepath;

use super::util::go_dir;
use crate::ProblemAnalyzer;

/// Go `EntitySnapshotAnalyzer`: validates committed entity schema
/// snapshots, their history graph, referenced migrations and schema drift.
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

impl ProblemAnalyzer for EntitySnapshotAnalyzer {
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
            Err(error) if error.0 == entityschema::ERR_INDEX_NOT_READY => return Ok(problems),
            Err(error) => return Err(error.0.into()),
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

/// Go `snapshotPluginRoot`: the plugin root of
/// `<root>/src/Resources/shopware-lsp/schema/<file>`.
fn snapshot_plugin_root(path: &str) -> Option<String> {
    let directory = go_dir(path);
    let shopware_lsp = go_dir(&directory);
    let resources = go_dir(&shopware_lsp);
    let src = go_dir(&resources);
    if filepath::base(&directory) != "schema"
        || filepath::base(&shopware_lsp) != "shopware-lsp"
        || filepath::base(&resources) != "Resources"
        || filepath::base(&src) != "src"
    {
        return None;
    }
    Some(go_dir(&src))
}
