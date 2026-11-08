//! The LSP equivalent of the reference plugin's route-controller gutter
//! target. It makes service and invokable controller indirection visible
//! while retaining ordinary go-to-definition on the source.

use std::collections::HashSet;
use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::{Context, InlayHintProvider, InlayHintRequest, ProviderResult, TextDocument};
use swlsp_lsp_protocol::{self as protocol, InlayHintKind};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_xml::syntax::{XML_ATTRIBUTE, XML_TEXT};
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::Symbol;
use swlsp_php_types::go_quote;
use swlsp_symfony::{ControllerReference, ServiceIndex};
use swlsp_syntax::{LineIndex, SyntaxNodeExt};
use swlsp_uriutil::file_uri;

use crate::{inlay_hint_byte_range, parts_label, position};

pub struct RouteControllerProvider {
    services: Option<Arc<ServiceIndex>>,
    php: Option<PhpIndex>,
}

impl RouteControllerProvider {
    pub fn new(services: Option<Arc<ServiceIndex>>, php: Option<PhpIndex>) -> Self {
        Self { services, php }
    }
}

struct Candidate {
    reference: ControllerReference,
    route_name: String,
    position: u32,
}

impl InlayHintProvider for RouteControllerProvider {
    fn get_inlay_hints(
        &self,
        ctx: &Context,
        request: &InlayHintRequest,
    ) -> ProviderResult<Vec<protocol::InlayHint>> {
        let document = &request.document;
        if ctx.is_cancelled() || self.php.is_none() || document.root().is_none() {
            return Ok(Vec::new());
        }
        let candidates = route_controller_hint_candidates(document);
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
            let key = (
                candidate.position,
                swlsp_symfony::controller_reference_key(&candidate.reference),
            );
            if !seen.insert(key) {
                continue;
            }
            let resolution = swlsp_symfony::resolve_controller_reference(
                &candidate.reference,
                self.services.as_deref(),
                self.php.as_ref(),
            )?;
            if !resolution.method_found {
                continue;
            }
            let Some(method) = resolution.method.as_ref() else {
                continue;
            };
            let class_name = resolution
                .class
                .as_ref()
                .map(|class| class.fully_qualified.trim_start_matches('\\').to_string())
                .unwrap_or_default();
            let method_name = method.name.to_string();
            let label = format!("→ {}::{method_name}", short_controller_class(&class_name));
            result.push(protocol::InlayHint {
                position: position(document, candidate.position),
                label: parts_label(vec![protocol::InlayHintLabelPart {
                    value: label,
                    tooltip: format!("Open {class_name}::{method_name}"),
                    location: Some(route_controller_method_location(method)),
                    command: None,
                }]),
                kind: Some(InlayHintKind::TYPE),
                tooltip: route_controller_hint_tooltip(&candidate, &class_name, &method_name),
                padding_left: true,
                ..Default::default()
            });
        }
        Ok(result)
    }
}

fn route_controller_hint_candidates(document: &TextDocument) -> Vec<Candidate> {
    let Some(root) = document.root() else {
        return Vec::new();
    };
    match document.language {
        Some(LanguageId::Yaml) => yamlquery::nodes(&root, &[YAML_SCALAR])
            .into_iter()
            .filter_map(|scalar| {
                let (reference, route_name) = swlsp_symfony::yaml_controller_reference(&scalar)?;
                Some(Candidate {
                    reference,
                    route_name,
                    position: scalar.range_trimmed_trivia().end().into(),
                })
            })
            .collect(),
        Some(LanguageId::Xml) => xmlquery::nodes(&root, &[XML_ATTRIBUTE, XML_TEXT])
            .into_iter()
            .filter_map(|node| {
                let (reference, route_name) = swlsp_symfony::xml_controller_reference(&node)?;
                Some(Candidate {
                    reference,
                    route_name,
                    position: node.range_trimmed_trivia().end().into(),
                })
            })
            .collect(),
        Some(LanguageId::Php) => {
            let Ok(path) = swlsp_uriutil::path(&document.uri) else {
                return Vec::new();
            };
            let lines = document.line_index();
            swlsp_symfony::parse_php_routes_tree(&path, document.tree(), Some(lines))
                .into_iter()
                .filter(|route| route.line > 0 && !route.controller.is_empty())
                .filter_map(|route| {
                    let reference = swlsp_symfony::parse_controller_reference(&route.controller)?;
                    Some(Candidate {
                        reference,
                        route_name: route.name,
                        position: lines.line_end(route.line - 1),
                    })
                })
                .collect()
        }
        _ => Vec::new(),
    }
}

fn short_controller_class(class_name: &str) -> &str {
    match class_name.rfind('\\') {
        Some(separator) => &class_name[separator + 1..],
        None => class_name,
    }
}

fn route_controller_method_location(method: &Symbol) -> protocol::Location {
    let mut location = protocol::Location {
        uri: file_uri(&method.path),
        range: protocol::Range::default(),
    };
    let Ok(source) = std::fs::read(&*method.path) else {
        return location;
    };
    let lines = LineIndex::new(String::from_utf8_lossy(&source).into_owned());
    let range = if method.selection_range.is_empty() {
        method.range
    } else {
        method.selection_range
    };
    let (start_line, start_character) = lines.position_utf16(range.start().into());
    let (end_line, end_character) = lines.position_utf16(range.end().into());
    location.range = protocol::Range {
        start: protocol::Position {
            line: start_line,
            character: start_character,
        },
        end: protocol::Position {
            line: end_line,
            character: end_character,
        },
    };
    location
}

fn route_controller_hint_tooltip(
    candidate: &Candidate,
    class_name: &str,
    method_name: &str,
) -> String {
    let resolved = format!("{class_name}::{method_name}");
    let mut detail = candidate.reference.value.clone();
    if detail != resolved {
        detail.push_str(" → ");
        detail.push_str(&resolved);
    }
    if candidate.route_name.is_empty() {
        return detail;
    }
    format!("Route {}\n{detail}", go_quote(&candidate.route_name))
}
