//! Go `admin_diagnostics_registry.go`: registry and module-route lookups.

use std::collections::HashSet;

use swlsp_admin::{
    AdminCMSRegistrationKind, AdminSymbolKind, JavaScriptDocumentAnalysis,
    JavaScriptRegistryReference, is_javascript_module_route_reference,
    javascript_cms_component_reference_at, javascript_cms_reference_at,
    javascript_registry_reference_at,
};
use swlsp_lsp::{BoxError, Problem, TextDocument};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_parser_js::{query as jsquery, syntax as jssyntax};
use swlsp_suggestion::similar;
use swlsp_syntax::TextRange;

use super::analyzer::AdminAnalyzer;
use super::twig::AdminTwigDiagnosticDocument;
use super::{go_string_list, payload, text};
use crate::common::{admin_nearby_suggestions, javascript_string_content_range};

/// Distinct non-empty names in first-seen order (Go `uniqueAdmin*Names`).
fn unique_names<'a>(names: impl Iterator<Item = &'a str>) -> Vec<String> {
    let mut seen = HashSet::new();
    let mut result = Vec::new();
    for name in names {
        if name.is_empty() || !seen.insert(name) {
            continue;
        }
        result.push(name.to_owned());
    }
    result
}

fn string_set(values: &[String]) -> HashSet<&str> {
    values.iter().map(String::as_str).collect()
}

impl AdminAnalyzer {
    pub(super) fn javascript_module_route_diagnostics(
        &self,
        document: &TextDocument,
        analysis: &JavaScriptDocumentAnalysis,
    ) -> Result<Vec<Problem>, BoxError> {
        let (known, names) = self.module_route_catalog()?;
        if known.is_empty() {
            return Ok(Vec::new());
        }
        let mut diagnostics = Vec::new();
        for literal in analysis.nodes(&[jssyntax::JS_STRING]) {
            if !is_javascript_module_route_reference(&literal) {
                continue;
            }
            let name = jsquery::string_value(&literal);
            if name.is_empty() || known.contains(&name) {
                continue;
            }
            diagnostics.push(module_route_problem(
                &name,
                javascript_string_content_range(&literal, document.source()),
                &names,
            ));
        }
        Ok(diagnostics)
    }

    pub(super) fn javascript_registry_diagnostics(
        &self,
        document: &TextDocument,
        analysis: &JavaScriptDocumentAnalysis,
    ) -> Result<Vec<Problem>, BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok(Vec::new());
        };
        let components = indexer.get_all_components_view()?;
        let modules = indexer.get_all_modules()?;
        let directives = indexer.get_all_directives()?;
        let filters = indexer.get_all_filters()?;
        let cms_elements =
            indexer.get_all_cms_registrations_by_kind(AdminCMSRegistrationKind::Element)?;
        let cms_blocks =
            indexer.get_all_cms_registrations_by_kind(AdminCMSRegistrationKind::Block)?;
        let component_names = unique_names(components.iter().map(|value| value.name.as_str()));
        let module_names = unique_names(modules.iter().map(|value| value.name.as_str()));
        let directive_names = unique_names(directives.iter().map(|value| value.name.as_str()));
        let filter_names = unique_names(filters.iter().map(|value| value.name.as_str()));
        let known_components = string_set(&component_names);
        let known_modules = string_set(&module_names);
        let known_directives = string_set(&directive_names);
        let known_filters = string_set(&filter_names);
        let cms_element_names = unique_names(cms_elements.iter().map(|value| value.name.as_str()));
        let cms_block_names = unique_names(cms_blocks.iter().map(|value| value.name.as_str()));
        let known_cms_elements = string_set(&cms_element_names);
        let known_cms_blocks = string_set(&cms_block_names);

        let mut diagnostics = Vec::new();
        let literals = analysis.nodes(&[jssyntax::JS_STRING]);
        for literal in &literals {
            let reference = javascript_registry_reference_at(literal).or_else(|| {
                javascript_cms_component_reference_at(literal).map(|target| {
                    JavaScriptRegistryReference {
                        target,
                        operation: "cms-component".to_owned(),
                    }
                })
            });
            let Some(reference) = reference else {
                continue;
            };
            let kind = reference.target.kind;
            if reference.operation != "get"
                && reference.operation != "cms-component"
                && (kind != AdminSymbolKind::Directive && kind != AdminSymbolKind::Filter
                    || reference.operation != "getByName")
            {
                // A has() lookup is commonly an intentional existence guard.
                continue;
            }
            let (known, names, label, code, payload_key) = match kind {
                AdminSymbolKind::Component => (
                    &known_components,
                    &component_names,
                    "Administration component",
                    "admin.component.registry-not-found",
                    "componentName",
                ),
                AdminSymbolKind::Module => (
                    &known_modules,
                    &module_names,
                    "Administration module",
                    "admin.module.not-found",
                    "moduleName",
                ),
                AdminSymbolKind::Directive => (
                    &known_directives,
                    &directive_names,
                    "Administration Vue directive",
                    "admin.directive.not-found",
                    "directiveName",
                ),
                AdminSymbolKind::Filter => (
                    &known_filters,
                    &filter_names,
                    "Administration filter",
                    "admin.filter.not-found",
                    "filterName",
                ),
                _ => continue,
            };
            // An empty catalog means indexing has not produced enough
            // information to make a reliable missing-symbol claim yet.
            let name = &reference.target.name;
            if known.is_empty() || known.contains(name.as_str()) {
                continue;
            }
            let suggestions = if kind == AdminSymbolKind::Directive {
                let suggestions = admin_nearby_suggestions(name, names);
                if suggestions.is_empty() {
                    continue;
                }
                suggestions
            } else {
                similar(name, names)
            };
            diagnostics.push(Problem {
                range: javascript_string_content_range(literal, document.source()),
                message: format!("{label} '{name}' is not registered"),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: code.into(),
                payload: Some(payload(vec![
                    (payload_key, text(name)),
                    ("suggestions", go_string_list(suggestions)),
                ])),
                ..Problem::default()
            });
        }
        // Keep CMS registry diagnostics in the same literal pass so
        // block-slot references and explicit get-by-name lookups share exact
        // source ranges.
        for literal in &literals {
            let Some(reference) = javascript_cms_reference_at(literal) else {
                continue;
            };
            if reference.operation == "register" {
                continue;
            }
            let (known, names, label, code, payload_key) = match reference.target.kind {
                AdminSymbolKind::CmsElement => (
                    &known_cms_elements,
                    &cms_element_names,
                    "Shopware CMS element",
                    "admin.cms-element.not-found",
                    "cmsElementName",
                ),
                AdminSymbolKind::CmsBlock => (
                    &known_cms_blocks,
                    &cms_block_names,
                    "Shopware CMS block",
                    "admin.cms-block.not-found",
                    "cmsBlockName",
                ),
                _ => continue,
            };
            let name = &reference.target.name;
            if known.is_empty() || known.contains(name.as_str()) {
                continue;
            }
            diagnostics.push(Problem {
                range: javascript_string_content_range(literal, document.source()),
                message: format!("{label} '{name}' is not registered"),
                source: "shopware".into(),
                severity: Some(DiagnosticSeverity::WARNING),
                id: code.into(),
                payload: Some(payload(vec![
                    (payload_key, text(name)),
                    ("suggestions", go_string_list(similar(name, names))),
                ])),
                ..Problem::default()
            });
        }
        Ok(diagnostics)
    }

    pub(super) fn twig_module_route_diagnostics(
        &self,
        analysis: &AdminTwigDiagnosticDocument,
    ) -> Result<Vec<Problem>, BoxError> {
        let references: Vec<_> = analysis
            .registry_references
            .iter()
            .filter(|reference| {
                reference.kind == AdminSymbolKind::ModuleRoute && !reference.name.is_empty()
            })
            .collect();
        if references.is_empty() {
            return Ok(Vec::new());
        }
        let (known, names) = self.module_route_catalog()?;
        if known.is_empty() {
            return Ok(Vec::new());
        }
        let mut diagnostics = Vec::new();
        for reference in references {
            if known.contains(&reference.name) {
                continue;
            }
            diagnostics.push(module_route_problem(
                &reference.name,
                reference.range,
                &names,
            ));
        }
        Ok(diagnostics)
    }

    fn module_route_catalog(&self) -> Result<(HashSet<String>, Vec<String>), BoxError> {
        let Some(indexer) = self.indexer() else {
            return Ok((HashSet::new(), Vec::new()));
        };
        let routes = indexer.get_all_module_routes()?;
        let mut known = HashSet::new();
        let mut names = Vec::new();
        for route in routes {
            if route.name.is_empty() || !known.insert(route.name.clone()) {
                continue;
            }
            names.push(route.name);
        }
        Ok((known, names))
    }
}

fn module_route_problem(name: &str, range: TextRange, names: &[String]) -> Problem {
    Problem {
        range,
        message: format!("Administration module route '{name}' is not registered"),
        source: "shopware".into(),
        severity: Some(DiagnosticSeverity::WARNING),
        id: "admin.module-route.not-found".into(),
        payload: Some(payload(vec![
            ("routeName", text(name)),
            ("suggestions", go_string_list(similar(name, names))),
        ])),
        ..Problem::default()
    }
}
