//! The portable counterpart of the reference plugin's inline route-name
//! folding. It leaves source text visible and adds the resolved path as a
//! clickable inlay instead.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, ProviderResult, TextDocument};
use swlsp_lsp_protocol::{self as protocol, InlayHintKind};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::PHP_STRING;
use swlsp_parser_twig::query as twigquery;
use swlsp_parser_twig::syntax::TWIG_LITERAL_STRING;
use swlsp_php::PhpIndex;
use swlsp_symfony::{Route, RouteIndexer};
use swlsp_syntax::SyntaxNodeExt;
use swlsp_textutil::lowercase;
use swlsp_uriutil::file_uri;

use crate::{inlay_hint_byte_range, line_position, position};

pub struct RoutePathProvider {
    routes: Arc<RouteIndexer>,
    php: Option<PhpIndex>,
}

impl RoutePathProvider {
    pub fn new(routes: Arc<RouteIndexer>, php: Option<PhpIndex>) -> Self {
        Self { routes, php }
    }
}

struct Candidate {
    name: String,
    position: u32,
}

impl InlayHintProvider for RoutePathProvider {
    fn get_inlay_hints(
        &self,
        ctx: &Context,
        request: &InlayHintRequest,
    ) -> ProviderResult<Vec<protocol::InlayHint>> {
        let document = &request.document;
        if ctx.is_cancelled() || document.root().is_none() {
            return Ok(Vec::new());
        }
        let candidates = route_path_hint_candidates(document, self.php.as_ref());
        if candidates.is_empty() {
            return Ok(Vec::new());
        }
        let (range_start, range_end) = inlay_hint_byte_range(document, request.params.range);
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for candidate in candidates {
            if ctx.is_cancelled() {
                return Ok(result);
            }
            if candidate.position < range_start || candidate.position > range_end {
                continue;
            }
            if !seen.insert((candidate.position, candidate.name.clone())) {
                continue;
            }
            let routes = route_path_hint_targets(self.routes.get_route(&candidate.name)?);
            let Some(primary) = routes.first() else {
                continue;
            };
            let mut part = protocol::InlayHintLabelPart {
                value: route_path_hint_label(&routes),
                tooltip: format!("Open route {}", candidate.name),
                ..Default::default()
            };
            if !primary.file_path.is_empty() && primary.line > 0 {
                part.location = Some(protocol::Location {
                    uri: file_uri(&primary.file_path),
                    range: protocol::Range {
                        start: line_position(primary.line - 1),
                        end: line_position(primary.line - 1),
                    },
                });
            }
            result.push(protocol::InlayHint {
                position: position(document, candidate.position),
                label: protocol::InlayHintLabel::Parts(vec![part]),
                kind: Some(InlayHintKind::TYPE),
                tooltip: route_path_hint_tooltip(&candidate.name, &routes),
                padding_left: true,
                ..Default::default()
            });
        }
        Ok(result)
    }
}

fn route_path_hint_candidates(document: &TextDocument, php: Option<&PhpIndex>) -> Vec<Candidate> {
    let Some(root) = document.root() else {
        return Vec::new();
    };
    let mut result = Vec::new();
    match document.language {
        Some(LanguageId::Php) => {
            let context = php.map(|php| {
                if let Ok(Some(context)) =
                    swlsp_lsp_phpanalysis::context_for_document(php, document)
                {
                    return context;
                }
                let path = swlsp_uriutil::path(&document.uri).unwrap_or_default();
                php.document_context(
                    &path,
                    i64::from(document.version),
                    Some(&root),
                    &root,
                    document.source(),
                )
            });
            for literal in phpquery::nodes(&root, &[PHP_STRING]) {
                if !swlsp_symfony::is_php_route_name_in_context(context.as_ref(), &literal) {
                    continue;
                }
                let name = phpquery::string_value(&literal);
                if !swlsp_symfony::is_static_route_name(&name) {
                    continue;
                }
                result.push(Candidate {
                    name,
                    position: literal.range_trimmed_trivia().end().into(),
                });
            }
        }
        Some(LanguageId::Twig) => {
            for literal in twigquery::iterate_nodes(&root, &[TWIG_LITERAL_STRING]) {
                if !swlsp_symfony::is_twig_route_name(&literal)
                    || !twigquery::string_is_static(&literal)
                {
                    continue;
                }
                let name = twigquery::string_value(&literal).unwrap_or_default();
                if !swlsp_symfony::is_static_route_name(&name) {
                    continue;
                }
                result.push(Candidate {
                    name,
                    position: literal.range_trimmed_trivia().end().into(),
                });
            }
        }
        _ => {}
    }
    result
}

fn route_path_hint_targets(routes: Vec<Route>) -> Vec<Route> {
    let mut seen = HashSet::new();
    let mut result: Vec<Route> = routes
        .into_iter()
        .filter(|route| !route.path.is_empty() && seen.insert(lowercase(&route.path)))
        .collect();
    result.sort_by(|left, right| {
        left.path
            .cmp(&right.path)
            .then_with(|| left.file_path.cmp(&right.file_path))
            .then(left.line.cmp(&right.line))
    });
    result
}

fn route_path_hint_label(routes: &[Route]) -> String {
    let mut label = format!("→ {}", routes[0].path);
    if routes.len() > 1 {
        label.push_str(&format!(" (+{})", routes.len() - 1));
    }
    label
}

fn route_path_hint_tooltip(name: &str, routes: &[Route]) -> String {
    let mut lines = vec![format!("Route {name:?}")];
    for route in routes {
        let mut detail = route.path.clone();
        if !route.methods.is_empty() {
            detail = format!("{} {detail}", route.methods.join("|"));
        }
        if !route.controller.is_empty() {
            detail.push_str(" → ");
            detail.push_str(&route.controller);
        }
        lines.push(detail);
    }
    lines.join("\n")
}
