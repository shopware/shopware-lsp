//! Go `scss_variable_diagnostics.go`.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use serde_json::json;
use swlsp_lsp::{BoxError, Context, DiagnosticId, Problem, TextDocument};
use swlsp_php_types::equal_fold;
use swlsp_style::{analyze_variables, normalize_variable_name};
use swlsp_syntax::TextRange;

use super::go_ext;
use crate::ProblemAnalyzer;

pub const SCSS_VARIABLE_UNKNOWN_CODE: &str = "scss.variable.unknown";

/// Go `SCSSVariableDeclarationProvider`.
pub trait ScssVariableDeclarationProvider: Send + Sync {
    fn has_variable_declaration(&self, name: &str, excluded_path: &str) -> Result<bool, BoxError>;
}

/// Go `SCSSVariableIndexReadiness`.
pub trait ScssVariableIndexReadiness: Send + Sync {
    fn ready(&self, ctx: &Context) -> Result<bool, BoxError>;
}

impl ScssVariableDeclarationProvider for swlsp_style::Index {
    fn has_variable_declaration(&self, name: &str, excluded_path: &str) -> Result<bool, BoxError> {
        Ok(swlsp_style::Index::has_variable_declaration(
            self,
            name,
            excluded_path,
        )?)
    }
}

impl ScssVariableIndexReadiness for swlsp_indexer::WorkspaceSymbolCatalog {
    fn ready(&self, _ctx: &Context) -> Result<bool, BoxError> {
        Ok(swlsp_indexer::WorkspaceSymbolCatalog::ready(self)?)
    }
}

pub struct ScssVariableAnalyzer {
    variables: Option<Arc<dyn ScssVariableDeclarationProvider>>,
    readiness: Option<Arc<dyn ScssVariableIndexReadiness>>,
}

impl ScssVariableAnalyzer {
    pub fn new(
        variables: Option<Arc<dyn ScssVariableDeclarationProvider>>,
        readiness: Option<Arc<dyn ScssVariableIndexReadiness>>,
    ) -> Self {
        Self {
            variables,
            readiness,
        }
    }
}

impl ProblemAnalyzer for ScssVariableAnalyzer {
    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let (Some(variables), Some(root)) = (&self.variables, document.root()) else {
            return Ok(Vec::new());
        };
        if !equal_fold(go_ext(&document.uri), ".scss") {
            return Ok(Vec::new());
        }
        if let Some(readiness) = &self.readiness {
            let ready = readiness
                .ready(ctx)
                .map_err(|error| format!("query SCSS index readiness: {error}"))?;
            if !ready {
                return Ok(Vec::new());
            }
        }
        let path = swlsp_uriutil::path(&document.uri)
            .map_err(|error| format!("resolve SCSS document path: {error}"))?;
        let analysis = analyze_variables(&path, &root);
        let known: HashSet<String> = analysis
            .bindings
            .iter()
            .map(|binding| normalize_variable_name(&binding.name))
            .collect();
        let mut indexed: HashMap<String, bool> = HashMap::new();
        let mut problems = Vec::new();
        for reference in &analysis.references {
            ctx.check()?;
            let name = normalize_variable_name(&reference.name);
            if known.contains(&name) {
                continue;
            }
            let exists = match indexed.get(&name) {
                Some(exists) => *exists,
                None => {
                    let exists =
                        variables
                            .has_variable_declaration(&name, &path)
                            .map_err(|error| {
                                format!("resolve SCSS variable ${}: {error}", reference.name)
                            })?;
                    indexed.insert(name, exists);
                    exists
                }
            };
            if exists {
                continue;
            }
            problems.push(Problem {
                id: DiagnosticId::from(SCSS_VARIABLE_UNKNOWN_CODE),
                range: TextRange::from(reference.range),
                message: format!("SCSS variable '${}' is not defined", reference.name),
                payload: Some(json!({ "variable": reference.name })),
                ..Problem::default()
            });
        }
        Ok(problems)
    }
}
