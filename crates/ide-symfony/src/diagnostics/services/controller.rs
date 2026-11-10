//! Missing and deprecated controller targets of routes and Twig `controller()`
//! calls.

use std::sync::Arc;

use serde_json::json;
use swlsp_lsp::{BoxError, Context, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_xml::syntax::{XML_ATTRIBUTE, XML_TEXT};
use swlsp_parser_yaml::query as yamlquery;
use swlsp_parser_yaml::syntax::YAML_SCALAR;
use swlsp_php::PhpIndex;
use swlsp_symfony::{ControllerReference, Route, ServiceIndex};
use swlsp_syntax::{SyntaxNode, TextRange};

use super::{deprecated_tags, lower_ext, problem};
use crate::diagnostics::ProblemAnalyzer;
use crate::diagnostics::common::value_node_text_range;

pub(crate) const MISSING_CONTROLLER_TARGET_CODE: &str = "symfony.controller.target.missing";
pub(crate) const MISSING_CONTROLLER_METHOD_CODE: &str = "symfony.controller.method.missing";
pub(crate) const DEPRECATED_CONTROLLER_CODE: &str = "symfony.controller.deprecated";

pub struct ControllerAnalyzer {
    service_index: Option<Arc<ServiceIndex>>,
    php_index: Option<PhpIndex>,
}

impl ControllerAnalyzer {
    pub fn new(service_index: Option<Arc<ServiceIndex>>, php_index: Option<PhpIndex>) -> Self {
        Self {
            service_index,
            php_index,
        }
    }
}

struct ReferenceNode {
    node: SyntaxNode,
    reference: ControllerReference,
    route_name: String,
    value_range: TextRange,
}

impl ProblemAnalyzer for ControllerAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(
            crate::diagnostics::inputs(&self.service_index)
                + crate::diagnostics::inputs(&self.php_index),
        )
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(php) = &self.php_index else {
            return Ok(Vec::new());
        };
        let Some(root) = document.root() else {
            return Ok(Vec::new());
        };
        let mut references = Vec::new();
        match lower_ext(&document.uri).as_str() {
            ".yaml" | ".yml" => {
                for scalar in yamlquery::nodes(&root, &[YAML_SCALAR]) {
                    if let Some((reference, route_name)) =
                        swlsp_symfony::yaml_controller_reference(&scalar)
                    {
                        references.push(ReferenceNode {
                            node: scalar,
                            reference,
                            route_name,
                            value_range: TextRange::default(),
                        });
                    }
                }
            }
            ".xml" => {
                for node in xmlquery::nodes(&root, &[XML_ATTRIBUTE, XML_TEXT]) {
                    if let Some((reference, route_name)) =
                        swlsp_symfony::xml_controller_reference(&node)
                    {
                        references.push(ReferenceNode {
                            node,
                            reference,
                            route_name,
                            value_range: TextRange::default(),
                        });
                    }
                }
            }
            ".twig" => {
                for reference in swlsp_symfony::twig_controller_references(&root) {
                    references.push(ReferenceNode {
                        node: reference.node,
                        reference: reference.reference,
                        route_name: String::new(),
                        value_range: reference.range,
                    });
                }
            }
            _ => return Ok(Vec::new()),
        }

        let mut result = Vec::new();
        for item in &references {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            let resolution = swlsp_symfony::resolve_controller_reference(
                &item.reference,
                self.service_index.as_deref(),
                Some(php),
            )?;
            if !resolution.target_exists {
                let mut problem = problem(
                    MISSING_CONTROLLER_TARGET_CODE,
                    reference_text_range(item, &item.reference.target),
                    format!("Controller target '{}' not found", item.reference.target),
                    DiagnosticSeverity::WARNING,
                );
                problem.payload = Some(json!({
                    "controller": item.reference.value,
                    "routeName": item.route_name,
                }));
                result.push(problem);
            } else if resolution.class_found && !resolution.method_declared {
                let class_name = resolution
                    .class
                    .as_ref()
                    .map(|class| class.fully_qualified.as_str().to_owned())
                    .unwrap_or_default();
                let mut problem = problem(
                    MISSING_CONTROLLER_METHOD_CODE,
                    reference_text_range(item, &item.reference.method),
                    format!(
                        "Controller method '{class_name}::{}' not found",
                        item.reference.method
                    ),
                    DiagnosticSeverity::WARNING,
                );
                problem.payload = Some(json!({
                    "controller": item.reference.value,
                    "className": class_name,
                    "methodName": item.reference.method,
                    "routeName": item.route_name,
                    "routeParameters": local_route_parameters(document, &item.route_name),
                }));
                result.push(problem);
            } else if resolution.deprecated() {
                let mut problem = problem(
                    DEPRECATED_CONTROLLER_CODE,
                    reference_text_range(item, &item.reference.value),
                    format!("Controller action '{}' is deprecated", item.reference.value),
                    DiagnosticSeverity::HINT,
                );
                problem.tags = deprecated_tags();
                result.push(problem);
            }
        }
        Ok(result)
    }
}

fn reference_text_range(item: &ReferenceNode, value: &str) -> TextRange {
    if item.node.text().to_string().contains(value) || item.value_range.is_empty() {
        return value_node_text_range(Some(&item.node), value);
    }
    item.value_range
}

fn local_route_parameters(document: &TextDocument, route_name: &str) -> Vec<String> {
    if route_name.is_empty() {
        return Vec::new();
    }
    let routes: Vec<Route> = match lower_ext(&document.uri).as_str() {
        ".yaml" | ".yml" => {
            swlsp_symfony::parse_yaml_routes_tree("", document.tree(), document.line_index())
        }
        ".xml" => swlsp_symfony::parse_xml_routes_tree("", document.tree(), document.line_index()),
        _ => Vec::new(),
    };
    routes
        .iter()
        .find(|route| route.name == route_name)
        .map(Route::parameters)
        .unwrap_or_default()
}
