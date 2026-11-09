//! Symfony analytics catalogs:
//! routes, services, Twig extensions, template usages and variables, Twig
//! components, and the local Symfony profiler request index.
//!
//! Every catalog is exposed as a custom LSP command with a JSON response.

use std::collections::HashMap;

use swlsp_lsp::{BoxError, Context};
use swlsp_php::PhpIndex;
use swlsp_php_semantic::{Symbol, SymbolKind};
use swlsp_syntax::TextRange;
use swlsp_twig::{TemplateReferenceKind, TwigIndexer};

mod profiler;
mod profiler_serialized;
mod request;
mod route_catalog;
mod service_locator;
mod twig_component_catalog;
mod twig_extension_catalog;
mod twig_template_usage_catalog;
mod twig_template_variable_catalog;

pub use profiler::{
    LIST_PROFILER_REQUESTS_COMMAND, ProfilerCatalogProvider, ProfilerMailMessage,
    ProfilerRequestCatalogEntry, ProfilerRequestCatalogRequest, ProfilerRuntimeTwigComponent,
};
pub use route_catalog::{
    LIST_ROUTES_COMMAND, RouteCatalogEntry, RouteCatalogProvider, RouteCatalogRequest,
};
pub use service_locator::{
    LOCATE_SERVICES_COMMAND, ServiceDefinitionSourceEntry, ServiceLocatorEntry,
    ServiceLocatorProvider, ServiceLocatorRequest,
};
pub use twig_component_catalog::{
    LIST_TWIG_COMPONENTS_COMMAND, TwigComponentBlockEntry, TwigComponentCatalogEntry,
    TwigComponentCatalogProvider, TwigComponentCatalogRequest, TwigComponentDeclarationEntry,
    TwigComponentPropEntry, TwigComponentSyntaxEntry, TwigComponentTemplateEntry,
    TwigComponentUsageEntry,
};
pub use twig_extension_catalog::{
    LIST_TWIG_EXTENSIONS_COMMAND, TwigExtensionCatalogEntry, TwigExtensionCatalogParameter,
    TwigExtensionCatalogProvider, TwigExtensionCatalogRequest,
};
pub use twig_template_usage_catalog::{
    LIST_TWIG_TEMPLATE_USAGES_COMMAND, TwigTemplateComponentUsage, TwigTemplateControllerUsage,
    TwigTemplateReferenceUsage, TwigTemplateRouteEntry, TwigTemplateSourceLocation,
    TwigTemplateUsageCatalogEntry, TwigTemplateUsageCatalogProvider,
    TwigTemplateUsageCatalogRequest,
};
pub use twig_template_variable_catalog::{
    LIST_TWIG_TEMPLATE_VARIABLES_COMMAND, TwigTemplateVariableCatalogEntry,
    TwigTemplateVariableCatalogProvider, TwigTemplateVariableCatalogRequest,
    TwigTemplateVariableEntry, TwigTemplateVariableProperty, TwigTemplateVariableSource,
};

#[cfg(test)]
mod tests;

pub type Result<T, E = BoxError> = std::result::Result<T, E>;

/// Builds a [`BoxError`] from a formatted message.
macro_rules! errorf {
    ($($arg:tt)*) => { $crate::BoxError::from(format!($($arg)*)) };
}
pub(crate) use errorf;

/// Fails with the cancellation error once the request is cancelled.
pub(crate) fn check(ctx: &Context) -> Result<()> {
    ctx.check()
        .map_err(|cancelled| BoxError::from(cancelled.to_string()))
}

pub(crate) fn is_zero<T: Default + PartialEq>(value: &T) -> bool {
    *value == T::default()
}

/// The `file://` URI of `path`, or an empty string for an empty path.
pub(crate) fn optional_file_uri(path: &str) -> String {
    if path.is_empty() {
        return String::new();
    }
    swlsp_uriutil::file_uri(path)
}

pub(crate) fn range_len(range: TextRange) -> u32 {
    u32::from(range.len())
}

pub(crate) fn range_start(range: TextRange) -> u32 {
    u32::from(range.start())
}

fn range_contains(range: TextRange, offset: u32) -> bool {
    offset >= u32::from(range.start()) && offset < u32::from(range.end())
}

/// `path` relative to `root` with slash separators, or `None` when `path`
/// lies outside `root`.
pub(crate) fn relative_inside(root: &str, path: &str) -> Option<String> {
    let relative = swlsp_uriutil::filepath::rel(root, path).ok()?;
    if relative == ".." || relative.starts_with("../") {
        return None;
    }
    Some(swlsp_uriutil::filepath::to_slash(&relative))
}

/// Whether `candidate` matches the Ant-style `pattern` (`*`, `**`, `?`).
pub fn ant_path_match(pattern: &str, candidate: &str) -> bool {
    swlsp_pathmatch::ant(pattern, candidate)
}

/// One-based source lines for byte offsets, reading each file once per
/// request. Unreadable files yield line 0.
#[derive(Default)]
pub(crate) struct SourceLineResolver {
    indexes: HashMap<String, Option<(Vec<u32>, u32)>>,
}

impl SourceLineResolver {
    pub(crate) fn line(&mut self, path: &str, offset: u32) -> i64 {
        if !self.indexes.contains_key(path) {
            let loaded = std::fs::read(path).ok().map(|source| {
                let newlines = source
                    .iter()
                    .enumerate()
                    .filter(|(_, byte)| **byte == b'\n')
                    .map(|(index, _)| index as u32)
                    .collect();
                (newlines, source.len() as u32)
            });
            self.indexes.insert(path.to_owned(), loaded);
        }
        let Some((newlines, len)) = &self.indexes[path] else {
            return 0;
        };
        let offset = offset.min(*len);
        newlines.partition_point(|&position| position < offset) as i64 + 1
    }
}

/// The innermost method whose body (or declaration) contains `range`.
pub(crate) fn enclosing_template_method(symbols: &[Symbol], range: TextRange) -> Option<Symbol> {
    let start = range_start(range);
    let mut best_width = u32::MAX;
    let mut best: Option<&Symbol> = None;
    for symbol in symbols {
        if symbol.kind != SymbolKind::Method {
            continue;
        }
        let container = if range_contains(symbol.body_range, start) {
            symbol.body_range
        } else if range_contains(symbol.range, start) {
            symbol.range
        } else {
            continue;
        };
        if range_len(container) >= best_width {
            continue;
        }
        best = Some(symbol);
        best_width = range_len(container);
    }
    best.cloned()
}

/// Statically rendered templates per controller method, loaded per PHP file.
pub(crate) struct RouteTemplateResolver<'a> {
    php: Option<&'a PhpIndex>,
    twig: Option<&'a TwigIndexer>,
    by_path: HashMap<String, HashMap<String, Vec<String>>>,
}

impl<'a> RouteTemplateResolver<'a> {
    pub(crate) fn new(php: Option<&'a PhpIndex>, twig: Option<&'a TwigIndexer>) -> Self {
        Self {
            php,
            twig,
            by_path: HashMap::new(),
        }
    }

    pub(crate) fn for_method(&mut self, method: &Symbol) -> Result<Vec<String>> {
        let (Some(php), Some(twig)) = (self.php, self.twig) else {
            return Ok(Vec::new());
        };
        if method.path.is_empty() {
            return Ok(Vec::new());
        }
        if !self.by_path.contains_key(method.path.as_str()) {
            let loaded = load_route_templates(php, twig, method.path.as_str())?;
            self.by_path.insert(method.path.to_string(), loaded);
        }
        Ok(self.by_path[method.path.as_str()]
            .get(&swlsp_textutil::lowercase(&method.fully_qualified))
            .cloned()
            .unwrap_or_default())
    }
}

fn load_route_templates(
    php: &PhpIndex,
    twig: &TwigIndexer,
    path: &str,
) -> Result<HashMap<String, Vec<String>>> {
    let references = twig.get_template_references_by_path(path)?;
    let symbols = php.semantic_snapshot().symbols_in(path);
    let mut values: HashMap<String, std::collections::BTreeSet<String>> = HashMap::new();
    for reference in references {
        if !matches!(
            reference.kind,
            TemplateReferenceKind::Render | TemplateReferenceKind::Attribute
        ) {
            continue;
        }
        let Some(method) = enclosing_template_method(&symbols, reference.range) else {
            continue;
        };
        values
            .entry(swlsp_textutil::lowercase(&method.fully_qualified))
            .or_default()
            .insert(reference.template);
    }
    Ok(values
        .into_iter()
        .map(|(method, templates)| (method, templates.into_iter().collect()))
        .collect())
}
