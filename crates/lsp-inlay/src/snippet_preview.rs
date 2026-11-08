//! The portable counterpart of the reference plugin's translation folding.
//! It keeps the source key visible and adds the preferred translation beside
//! it, with navigation to the JSON declaration.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, ProviderResult, TextDocument};
use swlsp_lsp_protocol::{self as protocol, InlayHintKind};
use swlsp_parser_js::query as jsquery;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;
use swlsp_php_types::go_to_lower;
use swlsp_snippet::{Snippet, SnippetIndexer};
use swlsp_syntax::SyntaxNodeExt;
use swlsp_uriutil::{file_uri, filepath};

use crate::{inlay_hint_byte_range, line_position, parts_label, position};

pub struct SnippetPreviewProvider {
    index: Arc<SnippetIndexer>,
}

impl SnippetPreviewProvider {
    pub fn new(index: Arc<SnippetIndexer>) -> Self {
        Self { index }
    }
}

struct Candidate {
    key: String,
    admin: bool,
    position: u32,
}

impl InlayHintProvider for SnippetPreviewProvider {
    fn get_inlay_hints(
        &self,
        ctx: &Context,
        request: &InlayHintRequest,
    ) -> ProviderResult<Vec<protocol::InlayHint>> {
        let document = &request.document;
        if ctx.is_cancelled() || document.root().is_none() {
            return Ok(Vec::new());
        }
        let (start, end) = inlay_hint_byte_range(document, request.params.range);
        let mut result = Vec::new();
        let mut seen = HashSet::new();
        for candidate in snippet_hint_candidates(document) {
            if candidate.key.is_empty() || candidate.position < start || candidate.position > end {
                continue;
            }
            if !seen.insert((candidate.position, candidate.admin, candidate.key.clone())) {
                continue;
            }
            let translations = if candidate.admin {
                self.index.admin_snippet(&candidate.key)?
            } else {
                self.index.frontend_snippet(&candidate.key)?
            };
            if translations.is_empty() {
                continue;
            }
            let mut translations = translations.to_vec();
            translations.sort_by(|left, right| {
                snippet_locale_rank(&left.file)
                    .cmp(&snippet_locale_rank(&right.file))
                    .then_with(|| left.file.cmp(&right.file))
            });
            let preferred = &translations[0];
            let mut part = protocol::InlayHintLabelPart {
                value: format!("→ {}", compact_snippet_preview(&preferred.text)),
                tooltip: format!("Open translation for {}", candidate.key),
                ..Default::default()
            };
            if !preferred.file.is_empty() && preferred.line > 0 {
                part.location = Some(protocol::Location {
                    uri: file_uri(&preferred.file),
                    range: protocol::Range {
                        start: line_position(preferred.line - 1),
                        end: line_position(preferred.line - 1),
                    },
                });
            }
            result.push(protocol::InlayHint {
                position: position(document, candidate.position),
                label: parts_label(vec![part]),
                kind: Some(InlayHintKind::TYPE),
                tooltip: snippet_preview_tooltip(&candidate.key, &translations),
                padding_left: true,
                ..Default::default()
            });
        }
        Ok(result)
    }
}

fn snippet_hint_candidates(document: &TextDocument) -> Vec<Candidate> {
    let Some(root) = document.root() else {
        return Vec::new();
    };
    let mut result = Vec::new();
    match document.language {
        Some(LanguageId::Twig) => {
            for reference in swlsp_snippet::admin_twig_references(&root) {
                result.push(Candidate {
                    key: reference.key,
                    admin: true,
                    position: u32::from(reference.range.end()) + 1,
                });
            }
            for literal in twigquery::iterate_nodes(&root, &[TWIG_LITERAL_STRING]) {
                if !twigquery::string_in_filter(&literal, &["trans"]) {
                    continue;
                }
                result.push(Candidate {
                    key: twigquery::string_value(&literal).unwrap_or_default(),
                    admin: false,
                    position: literal.range_trimmed_trivia().end().into(),
                });
            }
        }
        Some(LanguageId::JavaScript) => {
            for literal in swlsp_snippet::admin_javascript_string_references(&root) {
                result.push(Candidate {
                    key: jsquery::string_value(&literal),
                    admin: true,
                    position: literal.range_trimmed_trivia().end().into(),
                });
            }
        }
        Some(LanguageId::Php) => {
            for literal in phpquery::nodes(&root, &[PHP_STRING]) {
                if !phpquery::string_in_call(&literal, 0, &["trans"]) {
                    continue;
                }
                result.push(Candidate {
                    key: phpquery::string_value(&literal),
                    admin: false,
                    position: literal.range_trimmed_trivia().end().into(),
                });
            }
        }
        _ => {}
    }
    result
}

fn snippet_locale_rank(path: &str) -> u8 {
    let normalized = go_to_lower(&filepath::to_slash(path));
    if normalized.contains("en-gb")
        || normalized.contains("en_gb")
        || normalized.ends_with("/en.json")
    {
        0
    } else if normalized.contains("de-de") || normalized.contains("de_de") {
        1
    } else {
        2
    }
}

fn compact_snippet_preview(value: &str) -> String {
    let value = value.split_whitespace().collect::<Vec<_>>().join(" ");
    const LIMIT: usize = 72;
    if value.chars().count() > LIMIT {
        let mut result: String = value.chars().take(LIMIT - 1).collect();
        result.push('…');
        return result;
    }
    value
}

fn snippet_preview_tooltip(key: &str, translations: &[Snippet]) -> String {
    let mut lines = vec![format!("Snippet \"{key}\"")];
    for (index, value) in translations.iter().enumerate() {
        if index == 4 {
            lines.push(format!("… and {} more", translations.len() - index));
            break;
        }
        lines.push(format!(
            "{}: {}",
            filepath::base(&value.file),
            compact_snippet_preview(&value.text)
        ));
    }
    lines.join("\n")
}
