//! Legacy `slot` / `slot-scope` attributes.

use std::sync::{Arc, LazyLock};

use regex::Regex;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, Problem, TextDocument, effective_syntax_language};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_syntax::{SyntaxNodeExt, TextRange, TextSize};

use super::{payload, text};
use crate::diagnostics::ProblemAnalyzer;

// `\b` and `\s` are meant as ASCII classes; spell them out so Unicode classes
// do not widen the matches.
static LEGACY_TEMPLATE_TAG: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(?is)<template(?-u:\b)[^>]*>").expect("template pattern"));
static LEGACY_SLOT: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?i)[\t\n\f\r ]+slot[\t\n\f\r ]*=[\t\n\f\r ]*["']([^"']+)["']"#)
        .expect("slot pattern")
});
static LEGACY_SLOT_SCOPE: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(?i)[\t\n\f\r ]+slot-scope[\t\n\f\r ]*=[\t\n\f\r ]*["']([^"']*)["']"#)
        .expect("slot-scope pattern")
});

#[derive(Clone, Copy, Debug, Default)]
pub struct AdminSlotMigrationAnalyzer;

impl AdminSlotMigrationAnalyzer {
    pub fn new() -> Self {
        Self
    }
}

impl ProblemAnalyzer for AdminSlotMigrationAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(0)
    }

    fn analyze(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        if !document.uri.contains("Resources/app/administration") {
            return Ok(Vec::new());
        }
        let lower_uri = document.uri.to_lowercase();
        if !lower_uri.ends_with(".twig") && !lower_uri.ends_with(".vue") {
            return Ok(Vec::new());
        }
        let source = document.source();
        let root = document.root();
        let mut result = Vec::new();
        for location in LEGACY_TEMPLATE_TAG.find_iter(source) {
            if document.language == Some(LanguageId::Vue) {
                let node = root
                    .as_ref()
                    .and_then(|root| root.node_at(TextSize::from(location.start() as u32)));
                if root.is_none()
                    || effective_syntax_language(Some(LanguageId::Vue), node.as_ref())
                        != Some(LanguageId::Twig)
                {
                    continue;
                }
            }
            let tag = location.as_str();
            let Some(slot) = LEGACY_SLOT.captures(tag) else {
                continue;
            };
            let scope = LEGACY_SLOT_SCOPE
                .captures(tag)
                .map(|captures| captures[1].to_owned())
                .unwrap_or_default();
            let replacement = LEGACY_SLOT.replace_all(tag, "");
            let replacement = LEGACY_SLOT_SCOPE.replace_all(&replacement, "").into_owned();
            let mut directive = format!(" #{}", &slot[1]);
            if !scope.is_empty() {
                directive.push_str(&format!("=\"{scope}\""));
            }
            let split = "<template".len();
            let replacement = format!(
                "{}{directive}{}",
                &replacement[..split],
                &replacement[split..]
            );
            result.push(Problem {
                id: "admin.slot-syntax-deprecated".into(),
                range: TextRange::new(
                    TextSize::from(location.start() as u32),
                    TextSize::from(location.end() as u32),
                ),
                message: "Use Vue v-slot shorthand instead of slot and slot-scope".to_owned(),
                severity: Some(DiagnosticSeverity::WARNING),
                source: "shopware-lsp".into(),
                payload: Some(payload(vec![("replacement", text(&replacement))])),
                ..Problem::default()
            });
        }
        Ok(result)
    }
}
