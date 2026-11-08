//! Storefront and Administration snippet hover.

use std::fmt::Write as _;
use std::sync::Arc;

use swlsp_lsp::{Context, HoverProvider, HoverRequest, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_parser_js::query as jsquery;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_twig::query as twigquery;
use swlsp_snippet::{Snippet, SnippetIndexer};
use swlsp_syntax::SyntaxNode;

use swlsp_syntax::TextRange;

use crate::{
    js_string_content_range, lower_ext, markdown_hover, rel_or, request_byte_range, request_offset,
    request_uri, twig_string_content_range,
};

pub struct SnippetHoverProvider {
    snippet_indexer: Arc<SnippetIndexer>,
    project_root: String,
}

impl SnippetHoverProvider {
    pub fn new(project_root: impl Into<String>, snippet_indexer: Arc<SnippetIndexer>) -> Self {
        Self {
            snippet_indexer,
            project_root: project_root.into(),
        }
    }

    fn twig_hover(&self, request: &HoverRequest, node: &SyntaxNode) -> Option<protocol::Hover> {
        if let (Some(root), Some(offset)) = (request.root.as_ref(), request_offset(request))
            && let Some(reference) = swlsp_snippet::admin_twig_reference_at_offset(root, offset)
            && !reference.key.is_empty()
        {
            return self.create_hover_for_snippet(
                &reference.key,
                Some(reference.range),
                request,
                true,
            );
        }
        if twigquery::string_in_filter(node, &["trans"]) {
            let (key, range) = twig_string_key(node);
            return self.create_hover_for_snippet(&key, range, request, false);
        }
        if twigquery::string_in_function(node, &["$tc", "$t"]) {
            let (key, range) = twig_string_key(node);
            return self.create_hover_for_snippet(&key, range, request, true);
        }
        None
    }

    fn create_hover_for_snippet(
        &self,
        snippet_key: &str,
        key_range: Option<TextRange>,
        request: &HoverRequest,
        is_admin: bool,
    ) -> Option<protocol::Hover> {
        let snippets = if is_admin {
            self.snippet_indexer.admin_snippet(snippet_key)
        } else {
            self.snippet_indexer.frontend_snippet(snippet_key)
        };
        let mut snippets: Vec<Snippet> = snippets.ok()?.to_vec();
        if snippets.is_empty() {
            return None;
        }
        swlsp_gosort::sort_slice(&mut snippets, |left, right| left.file < right.file);
        let mut markdown = format!("**Snippet**: `{snippet_key}`\n\n");
        markdown.push_str("**Translations**:\n\n");
        for snippet in &snippets {
            let locale = extract_locale_from_path(&snippet.file);
            let display = rel_or(&self.project_root, &snippet.file);
            let _ = writeln!(markdown, "- **{locale}**: `{}`", snippet.text);
            let _ = write!(markdown, "  <small>{display}:{}</small>\n\n", snippet.line);
        }
        Some(markdown_hover(
            markdown,
            request_byte_range(request, key_range),
        ))
    }
}

/// The unquoted key of the Twig string literal at `node` and its byte range.
fn twig_string_key(node: &SyntaxNode) -> (String, Option<TextRange>) {
    let Some(literal) = twigquery::literal_string_at(node) else {
        return (String::new(), None);
    };
    (
        twigquery::string_value(&literal).unwrap_or_default(),
        twig_string_content_range(&literal),
    )
}

impl HoverProvider for SnippetHoverProvider {
    fn get_hover(
        &self,
        _ctx: &Context,
        request: &HoverRequest,
    ) -> ProviderResult<Option<protocol::Hover>> {
        let Some(node) = request.node.as_ref() else {
            return Ok(None);
        };
        Ok(match lower_ext(request_uri(request)).as_str() {
            ".twig" => self.twig_hover(request, node),
            ".php" => {
                if phpquery::string_in_call(node, 0, &["trans"]) {
                    let key = phpquery::string_value(node);
                    let range = phpquery::string_at(node)
                        .map(|string| phpquery::string_content_range(&string));
                    self.create_hover_for_snippet(&key, range, request, false)
                } else {
                    None
                }
            }
            ".js" | ".ts" => {
                if swlsp_snippet::admin_javascript_string_reference(node) {
                    let key = jsquery::string_value(node);
                    let range = js_string_content_range(node);
                    self.create_hover_for_snippet(&key, range, request, true)
                } else {
                    None
                }
            }
            _ => None,
        })
    }
}

/// The locale of a snippet file, from its file name first and its directory
/// structure second, e.g. `de-DE` for `snippet/de_DE/storefront.de-DE.json`.
pub fn extract_locale_from_path(path: &str) -> String {
    let normalized = path.replace('\\', "/");
    let parts: Vec<&str> = normalized.split('/').collect();
    if let Some(filename) = parts.last()
        && filename.contains('.')
    {
        for part in filename.split('.') {
            if is_locale_pattern(part) {
                return normalize_locale(part);
            }
        }
    }
    for (index, part) in parts.iter().enumerate() {
        if is_locale_pattern(part) {
            return normalize_locale(part);
        }
        if *part == "snippet"
            && let Some(next) = parts.get(index + 1)
            && is_locale_pattern(next)
        {
            return normalize_locale(next);
        }
    }
    "unknown".to_owned()
}

fn is_locale_pattern(value: &str) -> bool {
    let bytes = value.as_bytes();
    (bytes.len() == 5 && (bytes[2] == b'-' || bytes[2] == b'_')) || bytes.len() == 2
}

fn normalize_locale(locale: &str) -> String {
    locale.replace('_', "-")
}

#[cfg(test)]
mod tests {
    use super::{is_locale_pattern, normalize_locale};

    #[test]
    fn is_locale_pattern_cases() {
        for (input, expected) in [
            ("de-DE", true),
            ("en_GB", true),
            ("de", true),
            ("FR", true),
            ("deutsch", false),
            ("d", false),
            ("d-eDE", false),
            ("deDE", false),
            ("de-D", false),
            ("", false),
            ("12-34", true),
            ("De-dE", true),
        ] {
            assert_eq!(is_locale_pattern(input), expected, "{input}");
        }
    }

    #[test]
    fn normalize_locale_cases() {
        for (input, expected) in [
            ("de_DE", "de-DE"),
            ("en-GB", "en-GB"),
            ("de_DE_formal", "de-DE-formal"),
            ("de", "de"),
            ("", ""),
            ("de_DE-CH", "de-DE-CH"),
        ] {
            assert_eq!(normalize_locale(input), expected);
        }
    }
}
