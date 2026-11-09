//! Controller ↔ template ↔ route navigation lenses.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_lsp::{CodeLensProvider, CodeLensRequest, Context, ProviderResult};
use swlsp_lsp_protocol::{self as protocol, Range};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::SymbolKind;
use swlsp_symfony::{RouteIndexer, ServiceIndex};
use swlsp_textutil::lowercase;
use swlsp_twig::{TemplateReference, TemplateReferenceKind, TwigIndexer};
use swlsp_uriutil::filepath;

use crate::{
    by_line_title, cancelled, lower_ext, related_lens, related_protocol_range, related_source_line,
    related_target, unique_related_targets,
};

pub struct RelatedNavigationCodeLensProvider {
    twig: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
    routes: Option<Arc<RouteIndexer>>,
    services: Option<Arc<ServiceIndex>>,
}

impl RelatedNavigationCodeLensProvider {
    pub fn new(
        twig: Option<Arc<TwigIndexer>>,
        php: Option<PhpIndex>,
        routes: Option<Arc<RouteIndexer>>,
        services: Option<Arc<ServiceIndex>>,
    ) -> Self {
        Self {
            twig,
            php,
            routes,
            services,
        }
    }
}

impl CodeLensProvider for RelatedNavigationCodeLensProvider {
    fn get_code_lenses(
        &self,
        ctx: &Context,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let path = swlsp_uriutil::path(&request.params.text_document.uri)?;
        let (Some(twig), Some(php), Some(routes)) = (&self.twig, &self.php, &self.routes) else {
            return Ok(Vec::new());
        };
        match lower_ext(&path).as_str() {
            ".php" => self.php_code_lenses(ctx, twig, php, routes, &path, request),
            ".twig" => self.twig_code_lenses(ctx, twig, php, routes, &path),
            _ => Ok(Vec::new()),
        }
    }

    fn resolve_code_lens(
        &self,
        _ctx: &Context,
        lens: &protocol::CodeLens,
    ) -> ProviderResult<Option<protocol::CodeLens>> {
        Ok(Some(lens.clone()))
    }
}

impl RelatedNavigationCodeLensProvider {
    fn php_code_lenses(
        &self,
        ctx: &Context,
        twig: &TwigIndexer,
        php: &PhpIndex,
        routes: &RouteIndexer,
        path: &str,
        request: &CodeLensRequest,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let Some(root) = request.document.root() else {
            return Ok(Vec::new());
        };
        let lines = request.document.line_index();
        let mut result = Vec::new();
        for reference in swlsp_twig::php_template_references(path, &root) {
            cancelled(ctx)?;
            let targets = unique_related_targets(
                twig.get_twig_files_by_rel_path(&reference.template)?
                    .iter()
                    .map(|file| related_target(&file.path, 1))
                    .collect(),
            );
            if targets.is_empty() {
                continue;
            }
            let title = if targets.len() > 1 {
                format!("Open {} related templates", targets.len())
            } else {
                "Open related template".to_owned()
            };
            result.push(related_lens(
                related_protocol_range(reference.range, lines),
                title,
                targets,
            ));
        }
        let snapshot = php.semantic_snapshot();
        let methods: Vec<_> = snapshot
            .symbols_in(path)
            .into_iter()
            .filter(|symbol| symbol.kind == SymbolKind::Method)
            .collect();
        let wanted: HashSet<String> = methods
            .iter()
            .map(|symbol| lowercase(&symbol.fully_qualified))
            .collect();
        let route_targets = self.route_targets_by_method(ctx, php, routes, &wanted)?;
        for symbol in methods {
            let Some(targets) = route_targets.get(&lowercase(&symbol.fully_qualified)) else {
                continue;
            };
            if targets.is_empty() {
                continue;
            }
            let title = if targets.len() > 1 {
                format!("Open {} route definitions", targets.len())
            } else {
                "Open route definition".to_owned()
            };
            result.push(related_lens(
                related_protocol_range(symbol.selection_range, lines),
                title,
                targets.clone(),
            ));
        }
        result.sort_by(by_line_title);
        Ok(result)
    }

    fn twig_code_lenses(
        &self,
        ctx: &Context,
        twig: &TwigIndexer,
        php: &PhpIndex,
        routes: &RouteIndexer,
        path: &str,
    ) -> ProviderResult<Vec<protocol::CodeLens>> {
        let references = twig.get_template_references(&swlsp_twig::template_names(path))?;
        let mut php_references: Vec<TemplateReference> = Vec::new();
        let mut controller_targets = Vec::new();
        for reference in references {
            cancelled(ctx)?;
            if !filepath::ext(&reference.file_path).eq_ignore_ascii_case(".php") {
                continue;
            }
            if !matches!(
                reference.kind,
                TemplateReferenceKind::Render | TemplateReferenceKind::Attribute
            ) {
                continue;
            }
            controller_targets.push(related_target(
                &reference.file_path,
                related_source_line(&reference.file_path, reference.range.start().into()),
            ));
            php_references.push(reference);
        }
        let controller_targets = unique_related_targets(controller_targets);
        let snapshot = php.semantic_snapshot();
        // Methods rendering this template, in reference order.
        let mut rendering_methods = Vec::new();
        for reference in &php_references {
            for symbol in snapshot.symbols_in(&reference.file_path) {
                if symbol.kind == SymbolKind::Method
                    && symbol.body_range.contains(reference.range.start())
                {
                    rendering_methods.push(lowercase(&symbol.fully_qualified));
                }
            }
        }
        let wanted: HashSet<String> = rendering_methods.iter().cloned().collect();
        let route_targets_by_method = self.route_targets_by_method(ctx, php, routes, &wanted)?;
        let mut route_targets = Vec::new();
        for method in &rendering_methods {
            if let Some(targets) = route_targets_by_method.get(method) {
                route_targets.extend(targets.iter().cloned());
            }
        }
        let route_targets = unique_related_targets(route_targets);
        let mut result = Vec::new();
        if !controller_targets.is_empty() {
            let title = if controller_targets.len() > 1 {
                format!("Open {} rendering PHP locations", controller_targets.len())
            } else {
                "Open rendering PHP location".to_owned()
            };
            result.push(related_lens(Range::default(), title, controller_targets));
        }
        if !route_targets.is_empty() {
            let title = if route_targets.len() > 1 {
                format!("Open {} related routes", route_targets.len())
            } else {
                "Open related route".to_owned()
            };
            result.push(related_lens(Range::default(), title, route_targets));
        }
        Ok(result)
    }

    fn route_targets_by_method(
        &self,
        ctx: &Context,
        php: &PhpIndex,
        routes: &RouteIndexer,
        wanted: &HashSet<String>,
    ) -> ProviderResult<HashMap<String, Vec<String>>> {
        let mut result: HashMap<String, Vec<String>> = HashMap::new();
        // Only the targets of `wanted` methods are built; most documents need
        // none, and building one per route dominated the request.
        if wanted.is_empty() {
            return Ok(result);
        }
        for route in routes.get_routes_view()?.iter() {
            cancelled(ctx)?;
            let Some(reference) = swlsp_symfony::parse_controller_reference(&route.controller)
            else {
                continue;
            };
            let mut method = format!(
                "{}::{}",
                reference
                    .target
                    .strip_prefix('\\')
                    .unwrap_or(&reference.target),
                reference.method
            );
            if !reference.target.contains('\\')
                && let Some(services) = &self.services
            {
                let resolution = swlsp_symfony::resolve_controller_reference(
                    &reference,
                    Some(services),
                    Some(php),
                )?;
                if resolution.method_found
                    && let Some(resolved) = &resolution.method
                {
                    method = resolved.fully_qualified.to_string();
                }
            }
            let key = lowercase(method.strip_prefix('\\').unwrap_or(&method));
            if !wanted.contains(&key) {
                continue;
            }
            result
                .entry(key)
                .or_default()
                .push(related_target(&route.file_path, i64::from(route.line)));
        }
        for targets in result.values_mut() {
            *targets = unique_related_targets(std::mem::take(targets));
        }
        Ok(result)
    }
}
