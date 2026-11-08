//! Ports the reference plugin's TwigControllerUsageControllerRelatedGotoCollector
//! in both directions.

use std::collections::HashMap;
use std::sync::Arc;

use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Symbol, SymbolKind, Visibility};
use swlsp_php_types::go_to_lower;
use swlsp_symfony::{ControllerReference, RouteUsageIndexer, ServiceIndex};
use swlsp_syntax::SyntaxNode;

use crate::{
    by_line, cancelled, lower_ext, related_lens, related_protocol_range, related_source_line,
    related_target, unique_related_targets,
};

pub struct ControllerRelatedCodeLensProvider {
    usages: Option<Arc<RouteUsageIndexer>>,
    services: Option<Arc<ServiceIndex>>,
    php: Option<PhpIndex>,
}

impl ControllerRelatedCodeLensProvider {
    pub fn new(
        usages: Option<Arc<RouteUsageIndexer>>,
        services: Option<Arc<ServiceIndex>>,
        php: Option<PhpIndex>,
    ) -> Self {
        Self {
            usages,
            services,
            php,
        }
    }
}

impl CodeLensProvider for ControllerRelatedCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let (Some(usages), Some(php)) = (&self.usages, &self.php) else {
            return Ok(Vec::new());
        };
        let Some(root) = request.document.root() else {
            return Ok(Vec::new());
        };
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        match lower_ext(&path).as_str() {
            ".php" => self.php_code_lenses(ctx, usages, php, &path, request),
            ".twig" => self.twig_code_lenses(ctx, php, &root, request),
            _ => Ok(Vec::new()),
        }
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        _lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(None)
    }
}

impl ControllerRelatedCodeLensProvider {
    fn php_code_lenses(
        &self,
        ctx: &Context,
        usages: &RouteUsageIndexer,
        php: &PhpIndex,
        path: &str,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let targets_by_method = self.twig_targets_by_method(ctx, usages, php)?;
        let snapshot = php.semantic_snapshot();
        let lines = request.document.line_index();
        let mut result = Vec::new();
        for method in snapshot.symbols_in(path) {
            if method.kind != SymbolKind::Method || method.visibility != Visibility::Public {
                continue;
            }
            let Some(class) = snapshot.symbol(method.container.as_str()) else {
                continue;
            };
            let key = controller_method_key(&class.fully_qualified, &method.name);
            let targets =
                unique_related_targets(targets_by_method.get(&key).cloned().unwrap_or_default());
            if targets.is_empty() {
                continue;
            }
            let title = if targets.len() > 1 {
                format!("Open {} Twig controller usages", targets.len())
            } else {
                "Open Twig controller usage".to_owned()
            };
            result.push(related_lens(
                related_protocol_range(method.selection_range, lines),
                title,
                targets,
            ));
        }
        result.sort_by(by_line);
        Ok(result)
    }

    fn twig_code_lenses(
        &self,
        ctx: &Context,
        php: &PhpIndex,
        root: &SyntaxNode,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let lines = request.document.line_index();
        let mut result = Vec::new();
        for reference in swlsp_symfony::twig_controller_references(root) {
            cancelled(ctx)?;
            let resolution = swlsp_symfony::resolve_controller_reference(
                &reference.reference,
                self.services.as_deref(),
                Some(php),
            )?;
            let symbol: &Symbol = if resolution.method_declared {
                match resolution.method.as_ref() {
                    Some(method) => method,
                    None => continue,
                }
            } else if resolution.class_found {
                match resolution.class.as_ref() {
                    Some(class) => class,
                    None => continue,
                }
            } else {
                continue;
            };
            let range = if symbol.selection_range.is_empty() {
                symbol.range
            } else {
                symbol.selection_range
            };
            let title = if symbol.kind == SymbolKind::Method {
                "Open controller method"
            } else {
                "Open controller class"
            };
            result.push(related_lens(
                related_protocol_range(reference.range, lines),
                title,
                vec![related_target(
                    &symbol.path,
                    related_source_line(&symbol.path, range.start().into()),
                )],
            ));
        }
        Ok(result)
    }

    fn twig_targets_by_method(
        &self,
        ctx: &Context,
        usages: &RouteUsageIndexer,
        php: &PhpIndex,
    ) -> ProviderResult<HashMap<String, Vec<String>>> {
        let mut result: HashMap<String, Vec<String>> = HashMap::new();
        for usage in usages.get_all_controller_usages()? {
            cancelled(ctx)?;
            let reference = ControllerReference {
                value: usage.controller.clone(),
                target: usage.target.clone(),
                method: usage.method.clone(),
            };
            let resolution = swlsp_symfony::resolve_controller_reference(
                &reference,
                self.services.as_deref(),
                Some(php),
            )?;
            if !resolution.method_declared {
                continue;
            }
            let class_name = resolution
                .class
                .as_ref()
                .map(|class| class.fully_qualified.to_string())
                .unwrap_or_default();
            let method_name = resolution
                .method
                .as_ref()
                .map(|method| method.name.to_string())
                .unwrap_or_default();
            result
                .entry(controller_method_key(&class_name, &method_name))
                .or_default()
                .push(related_target(
                    &usage.file,
                    related_source_line(&usage.file, usage.range.start().into()),
                ));
        }
        Ok(result)
    }
}

fn controller_method_key(class_name: &str, method_name: &str) -> String {
    go_to_lower(&format!(
        "{}::{method_name}",
        class_name.trim_start_matches('\\')
    ))
}
