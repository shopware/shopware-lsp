//! Symfony route catalog (port of `route_catalog.go`).

use std::collections::{BTreeMap, HashSet};
use std::sync::Arc;

use serde::Serialize;
use serde_json::Value;
use swlsp_lsp::{CommandFunc, CommandProvider, Context};
use swlsp_php::PhpIndex;
use swlsp_symfony::{
    Route, RouteIndexer, RoutePathSearchMatcher, ServiceIndex, parse_controller_reference,
    resolve_controller_reference,
};
use swlsp_twig::TwigIndexer;
use swlsp_uriutil::filepath;

use crate::request::{Field, decode, present};
use crate::{
    Result, RouteTemplateResolver, SourceLineResolver, ant_path_match, check, errorf, is_zero,
    lower, range_len, range_start, relative_inside, trim_space,
};

pub const LIST_ROUTES_COMMAND: &str = "shopware/symfony/analytics/routes";

#[derive(Clone)]
pub struct RouteCatalogProvider {
    root: String,
    routes: Option<Arc<RouteIndexer>>,
    services: Option<Arc<ServiceIndex>>,
    php: Option<PhpIndex>,
    twig: Option<Arc<TwigIndexer>>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct RouteCatalogRequest {
    pub route_name: String,
    pub controller: String,
    pub url_path: String,
    pub file_glob: String,
}

impl RouteCatalogRequest {
    fn decode(value: &Value) -> Result<Self, String> {
        let members = decode(
            value,
            "RouteCatalogRequest",
            &[
                ("routeName", Field::String),
                ("controller", Field::String),
                ("urlPath", Field::String),
                ("fileGlob", Field::String),
            ],
        )?;
        Ok(Self {
            route_name: members.string("routeName"),
            controller: members.string("controller"),
            url_path: members.string("urlPath"),
            file_glob: members.string("fileGlob"),
        })
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RouteCatalogEntry {
    pub name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub path: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub methods: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub controller: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub resolved_controller: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub source_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub source_line: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub controller_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub controller_line: i64,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub templates: Vec<String>,
}

impl RouteCatalogProvider {
    pub fn new(
        root: &str,
        routes: Option<Arc<RouteIndexer>>,
        services: Option<Arc<ServiceIndex>>,
        php: Option<PhpIndex>,
        twig: Option<Arc<TwigIndexer>>,
    ) -> Self {
        Self {
            root: filepath::clean(root),
            routes,
            services,
            php,
            twig,
        }
    }

    fn list(&self, ctx: &Context, args: Option<&Value>) -> Result<Value> {
        let request = match present(args) {
            Some(value) => RouteCatalogRequest::decode(value)
                .map_err(|error| errorf!("invalid route catalog request: {error}"))?,
            None => RouteCatalogRequest::default(),
        };
        Ok(serde_json::to_value(self.catalog(ctx, &request)?)?)
    }

    pub fn catalog(
        &self,
        ctx: &Context,
        request: &RouteCatalogRequest,
    ) -> Result<Vec<RouteCatalogEntry>> {
        let (Some(routes), Some(php)) = (&self.routes, &self.php) else {
            return Err(errorf!("symfony route catalog is unavailable"));
        };
        check(ctx)?;
        let routes = routes.get_routes_view()?;
        let name_filter = lower(trim_space(&request.route_name));
        let controller_filter = lower(trim_space(&request.controller));
        let file_glob = filepath::to_slash(trim_space(&request.file_glob));
        let path_matcher = RoutePathSearchMatcher::new(&request.url_path);
        let filter_path = !path_matcher.search_path().is_empty();
        let mut templates = RouteTemplateResolver::new(Some(php), self.twig.as_deref());
        let mut lines = SourceLineResolver::default();
        let mut seen = HashSet::with_capacity(routes.len());
        let mut result = Vec::with_capacity(routes.len());
        for route in routes.iter() {
            check(ctx)?;
            if !name_filter.is_empty() && !lower(&route.name).contains(&name_filter) {
                continue;
            }
            if filter_path && !path_matcher.matches(&route.path) {
                continue;
            }
            let entry = self.route_entry(php, route, &mut templates, &mut lines)?;
            if !controller_filter.is_empty()
                && !lower(&entry.controller).contains(&controller_filter)
                && !lower(&entry.resolved_controller).contains(&controller_filter)
            {
                continue;
            }
            if !file_glob.is_empty() {
                let relative = self.relative_controller_path(&entry.controller_uri);
                if relative.is_empty() || !ant_path_match(&file_glob, &relative) {
                    continue;
                }
            }
            let key = [
                entry.name.as_str(),
                entry.path.as_str(),
                entry.controller.as_str(),
                entry.source_uri.as_str(),
                &entry.source_line.to_string(),
            ]
            .join("\x00");
            if !seen.insert(key) {
                continue;
            }
            result.push(entry);
        }
        result.sort_by(|left, right| {
            if left.name != right.name {
                return lower(&left.name).cmp(&lower(&right.name));
            }
            left.source_uri
                .cmp(&right.source_uri)
                .then(left.source_line.cmp(&right.source_line))
        });
        Ok(result)
    }

    fn route_entry(
        &self,
        php: &PhpIndex,
        route: &Route,
        templates: &mut RouteTemplateResolver<'_>,
        lines: &mut SourceLineResolver,
    ) -> Result<RouteCatalogEntry> {
        let mut entry = RouteCatalogEntry {
            name: route.name.clone(),
            path: route.path.clone(),
            methods: normalized_route_methods(&route.methods),
            controller: route.controller.clone(),
            source_line: i64::from(route.line),
            ..RouteCatalogEntry::default()
        };
        if !route.file_path.is_empty() {
            entry.source_uri = swlsp_uriutil::file_uri(&route.file_path);
        }
        let Some(reference) = parse_controller_reference(&route.controller) else {
            return Ok(entry);
        };
        let resolution =
            resolve_controller_reference(&reference, self.services.as_deref(), Some(php))?;
        let class = resolution.class.clone().unwrap_or_default();
        let target = if resolution.method_found {
            let method = resolution.method.clone().unwrap_or_default();
            entry.resolved_controller = format!("{}::{}", class.fully_qualified, method.name);
            method
        } else if resolution.class_found {
            entry.resolved_controller = class.fully_qualified.to_string();
            class
        } else {
            return Ok(entry);
        };
        if !target.path.is_empty() {
            entry.controller_uri = swlsp_uriutil::file_uri(&target.path);
            let range = if range_len(target.selection_range) == 0 {
                target.range
            } else {
                target.selection_range
            };
            entry.controller_line = lines.line(&target.path, range_start(range));
        }
        if resolution.method_found {
            entry.templates = templates.for_method(&target)?;
        }
        Ok(entry)
    }

    fn relative_controller_path(&self, uri: &str) -> String {
        if uri.is_empty() {
            return String::new();
        }
        let Ok(path) = swlsp_uriutil::path(uri) else {
            return String::new();
        };
        relative_inside(&self.root, &path).unwrap_or_default()
    }
}

impl CommandProvider for RouteCatalogProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let provider = self.clone();
        let command: CommandFunc = Arc::new(move |ctx, args| provider.list(ctx, args));
        BTreeMap::from([(LIST_ROUTES_COMMAND.to_owned(), command)])
    }
}

/// Go `strings.ToUpper` (simple case mapping).
fn go_to_upper(value: &str) -> String {
    value
        .chars()
        .map(|character| {
            let mut upper = character.to_uppercase();
            match (upper.next(), upper.next()) {
                (Some(single), None) => single,
                _ => character,
            }
        })
        .collect()
}

pub(crate) fn normalized_route_methods(methods: &[String]) -> Vec<String> {
    let mut result = Vec::with_capacity(methods.len());
    let mut seen = HashSet::with_capacity(methods.len());
    for method in methods {
        let method = go_to_upper(trim_space(method));
        if method.is_empty() || !seen.insert(method.clone()) {
            continue;
        }
        result.push(method);
    }
    result
}
