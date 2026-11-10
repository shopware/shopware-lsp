//! Deprecated and missing members accessed in Twig expressions.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_ACCESSOR;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Flags, Snapshot, Symbol, SymbolKind};
use swlsp_syntax::SyntaxNodeExt;
use swlsp_textutil::lowercase;
use swlsp_twig::{PhpAccessResolver, TwigIndexer};

use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

const DEPRECATED_TWIG_MEMBER_CODE: &str = "twig.member.deprecated";
const MISSING_TWIG_MEMBER_CODE: &str = "twig.member.missing";

pub struct TwigMemberDeprecationAnalyzer {
    twig_index: Option<Arc<TwigIndexer>>,
    php_index: Option<PhpIndex>,
}

impl TwigMemberDeprecationAnalyzer {
    pub fn new(twig_index: Option<Arc<TwigIndexer>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            twig_index,
            php_index,
        }
    }
}

impl DeclaredAnalyzer for TwigMemberDeprecationAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "twig.member.deprecation",
        description: "Twig member access to deprecated properties and methods.",
        domain: "twig",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Twig],
        source: "twig",
        codes: &[DEPRECATED_TWIG_MEMBER_CODE],
    };
}

impl ProblemAnalyzer for TwigMemberDeprecationAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(
            crate::diagnostics::inputs(&self.twig_index)
                + crate::diagnostics::inputs(&self.php_index),
        )
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        if !lowercase(&document.uri).ends_with(".twig") || is_vue_template(&document.uri) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let template_path = swlsp_uriutil::path(&document.uri)?;
        let resolver =
            PhpAccessResolver::new(Some(php_index), self.twig_index.as_deref()).for_document(&root);
        let snapshot = php_index.semantic_snapshot();
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for accessor in twigquery::iterate_nodes(&root, &[TWIG_ACCESSOR]) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let Some(resolution) = resolver.resolve_accessor(&template_path, &root, &accessor)
            else {
                continue;
            };
            let range = resolution.name_node.range_trimmed_trivia();
            for member in &resolution.members {
                let symbol = &member.symbol;
                if !symbol.flags.has(Flags::DEPRECATED) {
                    continue;
                }
                let key = format!("{}:{:?}", symbol.id.as_str(), range);
                if !seen.insert(key) {
                    continue;
                }
                result.push(Problem {
                    id: DEPRECATED_TWIG_MEMBER_CODE.into(),
                    range,
                    message: deprecated_twig_member_message(&snapshot, symbol),
                    source: "twig".into(),
                    severity: Some(protocol::DiagnosticSeverity::HINT),
                    tags: vec![protocol::DiagnosticTag::DEPRECATED],
                    ..Problem::default()
                });
            }
        }
        Ok(result)
    }
}

fn deprecated_twig_member_message(snapshot: &Snapshot, symbol: &Symbol) -> String {
    let mut class_name = snapshot
        .symbol(symbol.container.as_str())
        .map(|container| container.name.to_string())
        .unwrap_or_default();
    if class_name.is_empty() {
        let fully_qualified: &str = &symbol.fully_qualified;
        let first = fully_qualified.split("::").next().unwrap_or_default();
        let first = first.strip_prefix('\\').unwrap_or(first);
        class_name = match first.rfind('\\') {
            Some(separator) => first[separator + 1..].to_owned(),
            None => first.to_owned(),
        };
    }
    let name: &str = &symbol.name;
    match symbol.kind {
        SymbolKind::Method => format!("Method '{class_name}::{name}' is deprecated"),
        SymbolKind::Property => format!(
            "Field '{class_name}::${}' is deprecated",
            name.strip_prefix('$').unwrap_or(name)
        ),
        _ => format!("Element '{class_name}::{name}' is deprecated"),
    }
}

pub struct TwigMemberMissingAnalyzer {
    twig_index: Option<Arc<TwigIndexer>>,
    php_index: Option<PhpIndex>,
}

impl TwigMemberMissingAnalyzer {
    pub fn new(twig_index: Option<Arc<TwigIndexer>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            twig_index,
            php_index,
        }
    }
}

impl DeclaredAnalyzer for TwigMemberMissingAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "twig.member.missing",
        description: "Twig member access to properties and methods the variable's type does not have.",
        domain: "twig",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Twig],
        source: "twig",
        codes: &[MISSING_TWIG_MEMBER_CODE],
    };
}

impl ProblemAnalyzer for TwigMemberMissingAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(
            crate::diagnostics::inputs(&self.twig_index)
                + crate::diagnostics::inputs(&self.php_index),
        )
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php_index) = &self.php_index else {
            return Ok(Vec::new());
        };
        if !lowercase(&document.uri).ends_with(".twig") || is_vue_template(&document.uri) {
            return Ok(Vec::new());
        }
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let template_path = swlsp_uriutil::path(&document.uri)?;
        let resolver =
            PhpAccessResolver::new(Some(php_index), self.twig_index.as_deref()).for_document(&root);
        let mut result = Vec::new();
        for accessor in twigquery::iterate_nodes(&root, &[TWIG_ACCESSOR]) {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let Some(resolution) = resolver.inspect_accessor(&template_path, &root, &accessor)
            else {
                continue;
            };
            if !resolution.members.is_empty() {
                continue;
            }
            result.push(Problem {
                id: MISSING_TWIG_MEMBER_CODE.into(),
                range: resolution.name_node.range_trimmed_trivia(),
                message: format!(
                    "Field or method '{}' not found on {}",
                    resolution.name, resolution.receiver
                ),
                source: "twig".into(),
                severity: Some(protocol::DiagnosticSeverity::WARNING),
                ..Problem::default()
            });
        }
        Ok(result)
    }
}

/// Administration templates are Vue templates in Twig block syntax: their
/// `{{ app.name }}` is a Vue expression over component state, not an access
/// to a Twig variable (such as the `app` global) with a PHP type.
fn is_vue_template(uri: &str) -> bool {
    uri.contains("/Resources/app/administration/")
}
