//! Route names defined in more than one project file.
//!
//! Symfony keeps one route per name: whichever definition is loaded last
//! silently replaces the others. The per-file `symfony.duplicate` rule sees
//! repeats within one file; this project rule compares every indexed route
//! definition. Definitions below `vendor/` neither count nor get reported:
//! redefining a bundle's route by name is the documented way to override it.
//! Test directories are left out the same way: their fixtures redefine
//! production routes on purpose and are not loaded by the application.

use std::collections::HashMap;
use std::sync::Arc;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_lsp_protocol::{self as protocol, DiagnosticSeverity};
use swlsp_symfony::RouteIndexer;

use super::duplicate::definitions;
use super::problem;
use crate::diagnostics::common::value_node_text_range;
use crate::diagnostics::{ProjectAnalysis, SummaryHandle};

pub const ROUTE_REDEFINED_CODE: &str = "symfony.route.redefined";

/// Where a route name is defined: source path and one-based line.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RouteLocation {
    pub path: String,
    pub line: u32,
}

/// Every project route name with the files defining it.
#[derive(Debug, Default)]
pub struct RouteDefinitions {
    pub by_name: HashMap<String, Vec<RouteLocation>>,
}

/// The `symfony.route.redefined` project analysis.
pub struct RouteRedefinedAnalysis {
    routes: Option<Arc<RouteIndexer>>,
}

impl RouteRedefinedAnalysis {
    pub fn new(routes: Option<Arc<RouteIndexer>>) -> Self {
        RouteRedefinedAnalysis { routes }
    }
}

/// Whether definitions in `path` are left out: dependencies and tests.
fn is_excluded(path: &str) -> bool {
    path.split('/')
        .any(|segment| matches!(segment, "vendor" | "tests" | "Tests" | "Test" | "test"))
}

impl ProjectAnalysis for RouteRedefinedAnalysis {
    type Summary = RouteDefinitions;

    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.route.redefined",
        description: "Route names defined in more than one project file; Symfony keeps only the definition loaded last.",
        domain: "symfony",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php, LanguageId::Xml, LanguageId::Yaml],
        source: "symfony",
        codes: &[ROUTE_REDEFINED_CODE],
    };

    fn revision(&self) -> u64 {
        self.routes
            .as_ref()
            .map_or(0, |routes| routes.source_revision())
    }

    fn summarize(&self, ctx: &Context) -> Result<RouteDefinitions, BoxError> {
        let mut summary = RouteDefinitions::default();
        let Some(routes) = &self.routes else {
            return Ok(summary);
        };
        for route in routes.source_routes_view()?.iter() {
            if ctx.is_cancelled() {
                return Err(swlsp_lsp::Cancelled.into());
            }
            if route.name.is_empty() || is_excluded(&route.file_path) {
                continue;
            }
            let locations = summary.by_name.entry(route.name.clone()).or_default();
            let location = RouteLocation {
                path: route.file_path.clone(),
                line: route.line,
            };
            if !locations.contains(&location) {
                locations.push(location);
            }
        }
        for locations in summary.by_name.values_mut() {
            locations.sort_by(|a, b| a.path.cmp(&b.path).then(a.line.cmp(&b.line)));
        }
        Ok(summary)
    }

    fn affected_paths(&self, summary: &RouteDefinitions) -> Vec<String> {
        let mut paths: Vec<String> = summary
            .by_name
            .values()
            .filter(|locations| {
                locations
                    .iter()
                    .any(|location| location.path != locations[0].path)
            })
            .flat_map(|locations| locations.iter().map(|location| location.path.clone()))
            .collect();
        paths.sort();
        paths.dedup();
        paths
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        summary: &SummaryHandle<'_, Self>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Ok(path) = swlsp_uriutil::path(&document.uri) else {
            return Ok(Vec::new());
        };
        if self.routes.is_none() || is_excluded(&path) {
            return Ok(Vec::new());
        }
        let routes: Vec<_> = definitions(document)
            .into_iter()
            .filter(|definition| definition.kind == "route")
            .collect();
        if routes.is_empty() {
            return Ok(Vec::new());
        }
        let summary = summary.get()?;
        let mut result = Vec::new();
        for route in routes {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            // This document's own definitions come from its live tree; the
            // index only says where else the name is defined.
            let elsewhere: Vec<&RouteLocation> = summary
                .by_name
                .get(&route.name)
                .into_iter()
                .flatten()
                .filter(|location| location.path != path)
                .collect();
            let Some(first) = elsewhere.first() else {
                continue;
            };
            let place = format!("{}:{}", base_name(&first.path), first.line);
            let message = match elsewhere.len() {
                1 => format!(
                    "Route name '{}' is also defined in {place}; the definition loaded last replaces the other",
                    route.name
                ),
                2 => format!(
                    "Route name '{}' is also defined in {place} and 1 other file; the definition loaded last replaces the others",
                    route.name
                ),
                count => format!(
                    "Route name '{}' is also defined in {place} and {} other files; the definition loaded last replaces the others",
                    route.name,
                    count - 1
                ),
            };
            let mut problem = problem(
                ROUTE_REDEFINED_CODE,
                value_node_text_range(Some(&route.node), &route.name),
                message,
                DiagnosticSeverity::WARNING,
            );
            problem.related_information = elsewhere
                .iter()
                .map(|location| related(location, &route.name))
                .collect();
            problem.payload = Some(json!({
                "routeName": route.name,
                "definitions": elsewhere
                    .iter()
                    .map(|location| json!({"path": location.path, "line": location.line}))
                    .collect::<Vec<_>>(),
            }));
            result.push(problem);
        }
        Ok(result)
    }
}

fn base_name(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}

fn related(location: &RouteLocation, name: &str) -> protocol::DiagnosticRelatedInformation {
    let line = location.line.saturating_sub(1);
    let position = protocol::Position { line, character: 0 };
    protocol::DiagnosticRelatedInformation {
        location: protocol::Location {
            uri: swlsp_uriutil::file_uri(&location.path),
            range: protocol::Range {
                start: position,
                end: position,
            },
        },
        message: format!("Route '{name}' defined here"),
    }
}
