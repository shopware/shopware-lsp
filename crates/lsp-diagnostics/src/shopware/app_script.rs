//! Port of Go `app_script.go`.

use std::collections::HashSet;
use std::sync::{Arc, LazyLock};

use regex::Regex;
use serde_json::json;
use swlsp_appscript::Index as AppScriptIndex;
use swlsp_extension::ExtensionIndexer;
use swlsp_lsp::{BoxError, Context, DiagnosticId, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_twig::syntax::{HTML_COMMENT, TWIG_COMMENT};
use swlsp_syntax::{SyntaxNodeExt, TextRange, TextSize};
use swlsp_uriutil::filepath;

use crate::ProblemAnalyzer;

/// RE2 `\b` and `\s` are ASCII-only.
static SERVICE_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"(?-u:\b)services[\t\n\x0C\r ]*\.[\t\n\x0C\r ]*([A-Za-z_][A-Za-z0-9_]*)")
        .expect("service pattern")
});
static REPOSITORY_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(
        r#"(?-u:\b)services[\t\n\x0C\r ]*\.[\t\n\x0C\r ]*repository[\t\n\x0C\r ]*\.[\t\n\x0C\r ]*(?:search|searchIds|aggregate)[\t\n\x0C\r ]*\([\t\n\x0C\r ]*['"]([^'"]+)['"]"#,
    )
    .expect("repository pattern")
});

/// Go `AppScriptAnalyzer`: unavailable hook services and missing manifest
/// read permissions in app scripts.
#[derive(Clone)]
pub struct AppScriptAnalyzer {
    index: Option<Arc<AppScriptIndex>>,
    extensions: Option<Arc<ExtensionIndexer>>,
}

impl AppScriptAnalyzer {
    pub fn new(
        index: Option<Arc<AppScriptIndex>>,
        extensions: Option<Arc<ExtensionIndexer>>,
    ) -> Self {
        Self { index, extensions }
    }
}

impl ProblemAnalyzer for AppScriptAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(crate::inputs(&self.index) + crate::inputs(&self.extensions))
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(index) = &self.index else {
            return Ok(Vec::new());
        };
        if !document.uri.to_lowercase().ends_with(".twig")
            || !filepath::to_slash(&document.uri).contains("/Resources/scripts/")
        {
            return Ok(Vec::new());
        }
        let path = swlsp_uriutil::path(&document.uri)?;
        let hook_name = script_hook_name(&path);
        let available = index.services_for_hook(&hook_name)?;
        let source = document.source();
        let mut problems = Vec::new();
        if available.found {
            for captures in SERVICE_PATTERN.captures_iter(source) {
                ctx.check()?;
                let Some(service) = captures.get(1) else {
                    continue;
                };
                if match_in_comment(document, service.start()) {
                    continue;
                }
                if available.services.contains_key(service.as_str()) {
                    continue;
                }
                problems.push(Problem {
                    id: DiagnosticId::from("app_script.service-unavailable"),
                    range: byte_range(service.start(), service.end()),
                    message: format!(
                        "Service '{}' is not available in hook '{hook_name}'",
                        service.as_str()
                    ),
                    severity: Some(DiagnosticSeverity::ERROR),
                    source: "shopware-lsp".to_owned(),
                    payload: Some(json!({"hook": hook_name, "service": service.as_str()})),
                    ..Problem::default()
                });
            }
        }
        let Some(extensions) = &self.extensions else {
            return Ok(problems);
        };
        let Some(app) = extensions.find_app_for_file(&path)? else {
            return Ok(problems);
        };
        let read_permissions: HashSet<&str> = app
            .permissions
            .iter()
            .filter(|permission| permission.operation == "read")
            .map(|permission| permission.entity.as_str())
            .collect();
        for captures in REPOSITORY_PATTERN.captures_iter(source) {
            let Some(entity) = captures.get(1) else {
                continue;
            };
            if match_in_comment(document, entity.start())
                || read_permissions.contains(entity.as_str())
            {
                continue;
            }
            problems.push(Problem {
                id: DiagnosticId::from("app_script.permission-missing"),
                range: byte_range(entity.start(), entity.end()),
                message: format!(
                    "App manifest needs read permission for entity '{}'",
                    entity.as_str()
                ),
                severity: Some(DiagnosticSeverity::ERROR),
                source: "shopware-lsp".to_owned(),
                payload: Some(json!({
                    "entity": entity.as_str(),
                    "manifest": filepath::join(&[app.path.as_str(), "manifest.xml"]),
                })),
                ..Problem::default()
            });
        }
        Ok(problems)
    }
}

fn byte_range(start: usize, end: usize) -> TextRange {
    TextRange::new(TextSize::from(start as u32), TextSize::from(end as u32))
}

/// Go `scriptHookName`: the directory (or file stem) below
/// `/Resources/scripts/`.
fn script_hook_name(path: &str) -> String {
    let path = filepath::to_slash(path);
    const MARKER: &str = "/Resources/scripts/";
    let Some(index) = path.find(MARKER) else {
        return String::new();
    };
    let relative = &path[index + MARKER.len()..];
    if let Some(separator) = relative.find('/') {
        return relative[..separator].to_owned();
    }
    let extension = filepath::ext(relative);
    relative[..relative.len() - extension.len()].to_owned()
}

fn match_in_comment(document: &TextDocument, offset: usize) -> bool {
    let Some(root) = document.root() else {
        return false;
    };
    let Some(node) = root.node_at(TextSize::from(offset as u32)) else {
        return false;
    };
    node.ancestors()
        .any(|current| current.kind() == TWIG_COMMENT || current.kind() == HTML_COMMENT)
}
