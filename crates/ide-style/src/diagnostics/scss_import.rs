//! `@import "~scss/…​.scss"` in the Administration: the shared SCSS
//! resources are imported without their file extension (Shopware's
//! `admin-stylelint-rules/wrong-scss-import`).

use std::sync::Arc;

use serde_json::{Map, Value};
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_scss::query as scssquery;
use swlsp_parser_scss::syntax::{SCSS_AT_RULE, SCSS_STRING, TK_AT_KEYWORD};
use swlsp_syntax::SyntaxElement;

use crate::diagnostics::ProblemAnalyzer;

pub const SCSS_IMPORT_EXTENSION_CODE: &str = "scss.import.extension";

#[derive(Debug, Clone, Copy, Default)]
pub struct ScssImportExtensionAnalyzer;

impl ProblemAnalyzer for ScssImportExtensionAnalyzer {
    fn input_revision(&self, _document: &TextDocument) -> Option<u64> {
        Some(0)
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        if !document.uri.contains("Resources/app/administration") {
            return Ok(Vec::new());
        }
        let mut result = Vec::new();
        for rule in scssquery::nodes(&root, &[SCSS_AT_RULE]) {
            ctx.check()?;
            let is_import = rule
                .children_with_tokens()
                .filter_map(|element| element.into_token())
                .find(|token| token.kind() == TK_AT_KEYWORD)
                .is_some_and(|keyword| keyword.text() == "@import");
            if !is_import {
                continue;
            }
            for string in rule.children().filter(|child| child.kind() == SCSS_STRING) {
                let (Some(token), Some(path)) = (
                    scssquery::string_token(&string),
                    scssquery::string_value(&string),
                ) else {
                    continue;
                };
                let Some(stem) = path.strip_suffix(".scss") else {
                    continue;
                };
                if !path.starts_with("~scss") {
                    continue;
                }
                let quoted = token.text();
                // The closing quote stays where it is.
                let replacement = quoted.replacen(&path, stem, 1);
                let mut payload = Map::new();
                payload.insert(
                    "title".into(),
                    Value::String("Remove the .scss extension".into()),
                );
                payload.insert("expected".into(), Value::String(quoted.to_owned()));
                payload.insert("replacement".into(), Value::String(replacement));
                payload.insert("equivalent".into(), Value::Bool(true));
                result.push(Problem {
                    id: SCSS_IMPORT_EXTENSION_CODE.into(),
                    range: token.text_range(),
                    element: Some(SyntaxElement::Token(token)),
                    message: format!(
                        "Import the shared SCSS resource without its extension: \"{stem}\""
                    ),
                    severity: Some(DiagnosticSeverity::WARNING),
                    source: "scss".into(),
                    payload: Some(Value::Object(payload)),
                    ..Problem::default()
                });
            }
        }
        Ok(result)
    }
}
