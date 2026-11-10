//! Duplicate service, parameter and route names.

use std::collections::HashMap;
use std::sync::Arc;

use serde_json::json;
use swlsp_language::LanguageId;
use swlsp_lsp::{BoxError, Context, InspectionScope, InspectionSpec, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax::{PHP_ATTRIBUTE, PHP_STRING};
use swlsp_parser_xml::query as xmlquery;
use swlsp_parser_yaml::query as yamlquery;
use swlsp_syntax::SyntaxNode;

use super::{lower_ext, problem};
use crate::diagnostics::common::value_node_text_range;
use crate::diagnostics::{DeclaredAnalyzer, ProblemAnalyzer};

const DUPLICATE_ROUTE_CODE: &str = "symfony.route.duplicate";
const DUPLICATE_SERVICE_CODE: &str = "symfony.service.duplicate";
const DUPLICATE_PARAMETER_CODE: &str = "symfony.parameter.duplicate";

#[derive(Debug, Default)]
pub struct DuplicateAnalyzer;

impl DuplicateAnalyzer {
    pub fn new() -> Self {
        Self
    }
}

/// A service, parameter or route name defined in a document.
pub(crate) struct Definition {
    pub kind: &'static str,
    pub name: String,
    pub node: SyntaxNode,
}

impl DeclaredAnalyzer for DuplicateAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.duplicate",
        description: "Service, parameter or route names defined twice in the same file.",
        domain: "symfony.services",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php, LanguageId::Xml, LanguageId::Yaml],
        source: "symfony",
        codes: &[
            DUPLICATE_PARAMETER_CODE,
            DUPLICATE_ROUTE_CODE,
            DUPLICATE_SERVICE_CODE,
        ],
    };
}

impl ProblemAnalyzer for DuplicateAnalyzer {
    fn input_revision(&self, _document: &swlsp_lsp::TextDocument) -> Option<u64> {
        Some(0)
    }

    fn analyze(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Result<Vec<Problem>, BoxError> {
        let entries = definitions(document);
        if entries.is_empty() {
            return Ok(Vec::new());
        }
        let mut counts: HashMap<(&str, &str), usize> = HashMap::new();
        for entry in &entries {
            *counts.entry((entry.kind, &entry.name)).or_default() += 1;
        }
        let mut result = Vec::new();
        for entry in &entries {
            if ctx.is_cancelled() {
                return Ok(Vec::new());
            }
            if counts[&(entry.kind, entry.name.as_str())] < 2 {
                continue;
            }
            let (code, label) = duplicate_code_and_label(entry.kind);
            let mut problem = problem(
                code,
                value_node_text_range(Some(&entry.node), &entry.name),
                format!("Duplicate {label} name '{}'", entry.name),
                DiagnosticSeverity::WARNING,
            );
            problem.payload = Some(json!({
                "definitionKind": entry.kind,
                "definitionName": entry.name,
            }));
            result.push(problem);
        }
        Ok(result)
    }
}

/// The service, parameter and route names `document` defines, in source
/// order.
pub(crate) fn definitions(document: &TextDocument) -> Vec<Definition> {
    let Some(root) = document.root() else {
        return Vec::new();
    };
    match lower_ext(&document.uri).as_str() {
        ".yaml" | ".yml" => yaml_duplicate_definitions(&root),
        ".xml" => xml_duplicate_definitions(&root),
        ".php" => php_duplicate_route_definitions(&root),
        _ => Vec::new(),
    }
}

fn yaml_duplicate_definitions(root: &SyntaxNode) -> Vec<Definition> {
    let Some(mapping) = yamlquery::root_value(root).filter(yamlquery::is_mapping) else {
        return Vec::new();
    };
    let mut result = Vec::new();
    result.extend(yaml_mapping_definitions(
        yamlquery::property(&mapping, "services"),
        "service",
        true,
    ));
    result.extend(yaml_mapping_definitions(
        yamlquery::property(&mapping, "parameters"),
        "parameter",
        false,
    ));
    let routing = yamlquery::pairs(&mapping).any(|pair| {
        yamlquery::pair_value(&pair)
            .filter(yamlquery::is_mapping)
            .is_some_and(|value| {
                yamlquery::property(&value, "path").is_some()
                    || yamlquery::property(&value, "resource").is_some()
                    || yamlquery::property(&value, "controller").is_some()
            })
    });
    if routing {
        for pair in yamlquery::pairs(&mapping) {
            let Some(key) = yamlquery::pair_key(&pair) else {
                continue;
            };
            let name = yamlquery::scalar_value(&key);
            if name.is_empty()
                || name.starts_with('_')
                || !yamlquery::pair_value(&pair).is_some_and(|value| yamlquery::is_mapping(&value))
            {
                continue;
            }
            result.push(Definition {
                kind: "route",
                name,
                node: key,
            });
        }
    }
    result
}

fn yaml_mapping_definitions(
    mapping: Option<SyntaxNode>,
    kind: &'static str,
    skip_defaults: bool,
) -> Vec<Definition> {
    let Some(mapping) = mapping.filter(yamlquery::is_mapping) else {
        return Vec::new();
    };
    let mut result = Vec::new();
    for pair in yamlquery::pairs(&mapping) {
        let Some(key) = yamlquery::pair_key(&pair) else {
            continue;
        };
        let name = yamlquery::scalar_value(&key);
        if name.is_empty() || skip_defaults && name.starts_with('_') {
            continue;
        }
        result.push(Definition {
            kind,
            name,
            node: key,
        });
    }
    result
}

fn xml_duplicate_definitions(root: &SyntaxNode) -> Vec<Definition> {
    let mut result = Vec::new();
    let mut collect = |containers: &str, children: &[&str], attribute: &str, kind: &'static str| {
        for container in xmlquery::elements(root, &[containers]) {
            for child in xmlquery::child_elements(&container, children) {
                let Some(node) = xmlquery::attribute(&child, attribute) else {
                    continue;
                };
                let name = xmlquery::attribute_value(&node).unwrap_or_default();
                if !name.is_empty() {
                    result.push(Definition { kind, name, node });
                }
            }
        }
    };
    collect("services", &["service", "alias"], "id", "service");
    collect("parameters", &["parameter"], "key", "parameter");
    collect("routes", &["route"], "id", "route");
    result
}

fn php_duplicate_route_definitions(root: &SyntaxNode) -> Vec<Definition> {
    let mut result = Vec::new();
    for attribute in phpquery::nodes(root, &[PHP_ATTRIBUTE]) {
        if phpquery::class_at(&attribute).is_some() && phpquery::method_at(&attribute).is_none() {
            // A class-level route name is a prefix, not a concrete route name.
            continue;
        }
        let name = phpquery::attribute_name(&attribute);
        let name = name.strip_prefix('\\').unwrap_or(&name);
        let name = match name.rfind('\\') {
            Some(index) => &name[index + 1..],
            None => name,
        };
        if name != "Route" {
            continue;
        }
        let mut positional = 0;
        for argument in phpquery::arguments(&attribute) {
            let argument_name = phpquery::argument_name(&argument);
            let literal = phpquery::nodes(&argument, &[PHP_STRING]).into_iter().next();
            if let Some(literal) = literal
                && (argument_name == "name" || argument_name.is_empty() && positional == 1)
            {
                let value = phpquery::string_value(&literal);
                if !value.is_empty() {
                    result.push(Definition {
                        kind: "route",
                        name: value,
                        node: literal,
                    });
                }
            }
            if argument_name.is_empty() {
                positional += 1;
            }
        }
    }
    result
}

fn duplicate_code_and_label(kind: &str) -> (&'static str, &'static str) {
    match kind {
        "service" => (DUPLICATE_SERVICE_CODE, "service"),
        "parameter" => (DUPLICATE_PARAMETER_CODE, "parameter"),
        _ => (DUPLICATE_ROUTE_CODE, "route"),
    }
}
