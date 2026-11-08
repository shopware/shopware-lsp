//! Twig template usage catalog (port of `twig_template_usage_catalog.go`).

use std::collections::{BTreeMap, HashMap, HashSet};
use std::sync::Arc;

use serde::Serialize;
use serde_json::Value;
use swlsp_lsp::{CommandFunc, CommandProvider, Context};
use swlsp_php::PhpIndex;
use swlsp_symfony::{
    RouteIndexer, ServiceIndex, parse_controller_reference, resolve_controller_reference,
};
use swlsp_twig::{TemplateReference, TemplateReferenceKind, TwigFile, TwigIndexer};
use swlsp_twigcomponent::Index as ComponentIndex;
use swlsp_uriutil::filepath;

use crate::request::{Field, decode, present};
use crate::route_catalog::normalized_route_methods;
use crate::{
    Result, SourceLineResolver, ant_path_match, check, enclosing_template_method, errorf, is_zero,
    lower, range_len, range_start, relative_inside, trim_space,
};

pub const LIST_TWIG_TEMPLATE_USAGES_COMMAND: &str =
    "shopware/symfony/analytics/twig/templateUsages";

const TEMPLATE_REQUIRED: &str = "at least one of template or fileGlob is required";

#[derive(Clone)]
pub struct TwigTemplateUsageCatalogProvider {
    root: String,
    twig: Option<Arc<TwigIndexer>>,
    php: Option<PhpIndex>,
    routes: Option<Arc<RouteIndexer>>,
    services: Option<Arc<ServiceIndex>>,
    components: Option<Arc<ComponentIndex>>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct TwigTemplateUsageCatalogRequest {
    pub template: String,
    pub file_glob: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigTemplateSourceLocation {
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub line: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct TwigTemplateRouteEntry {
    pub name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub path: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub methods: Vec<String>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigTemplateControllerUsage {
    pub controller: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub line: i64,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub routes: Vec<TwigTemplateRouteEntry>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigTemplateReferenceUsage {
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub line: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigTemplateComponentUsage {
    pub component: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub syntax: String,
    pub file_uri: String,
    #[serde(skip_serializing_if = "is_zero")]
    pub line: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TwigTemplateUsageCatalogEntry {
    pub template: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub files: Vec<TwigTemplateSourceLocation>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub controllers: Vec<TwigTemplateControllerUsage>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub includes: Vec<TwigTemplateReferenceUsage>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub embeds: Vec<TwigTemplateReferenceUsage>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub extends: Vec<TwigTemplateReferenceUsage>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub imports: Vec<TwigTemplateReferenceUsage>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub uses: Vec<TwigTemplateReferenceUsage>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub form_themes: Vec<TwigTemplateReferenceUsage>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub components: Vec<TwigTemplateComponentUsage>,
}

type RouteMap = HashMap<String, Vec<TwigTemplateRouteEntry>>;

impl TwigTemplateUsageCatalogProvider {
    pub fn new(
        root: &str,
        twig: Option<Arc<TwigIndexer>>,
        php: Option<PhpIndex>,
        routes: Option<Arc<RouteIndexer>>,
        services: Option<Arc<ServiceIndex>>,
        components: Option<Arc<ComponentIndex>>,
    ) -> Self {
        Self {
            root: filepath::clean(root),
            twig,
            php,
            routes,
            services,
            components,
        }
    }

    fn list(&self, ctx: &Context, args: Option<&Value>) -> Result<Value> {
        let Some(value) = present(args) else {
            return Err(errorf!("{TEMPLATE_REQUIRED}"));
        };
        let members = decode(
            value,
            "TwigTemplateUsageCatalogRequest",
            &[("template", Field::String), ("fileGlob", Field::String)],
        )
        .map_err(|error| errorf!("invalid twig template usage request: {error}"))?;
        let request = TwigTemplateUsageCatalogRequest {
            template: members.string("template"),
            file_glob: members.string("fileGlob"),
        };
        Ok(serde_json::to_value(self.catalog(ctx, &request)?)?)
    }

    pub fn catalog(
        &self,
        ctx: &Context,
        request: &TwigTemplateUsageCatalogRequest,
    ) -> Result<Vec<TwigTemplateUsageCatalogEntry>> {
        let (Some(twig), Some(php)) = (&self.twig, &self.php) else {
            return Err(errorf!("twig template usage catalog is unavailable"));
        };
        check(ctx)?;
        let template_filter = lower(trim_space(&request.template));
        let file_glob = filepath::to_slash(trim_space(&request.file_glob));
        if template_filter.is_empty() && file_glob.is_empty() {
            return Err(errorf!("{TEMPLATE_REQUIRED}"));
        }
        let mut matching: HashSet<String> = twig
            .get_all_template_files()?
            .into_iter()
            .filter(|name| template_filter.is_empty() || lower(name).contains(&template_filter))
            .collect();
        if !request.template.is_empty() {
            matching.extend(template_names_for_path_input(
                &self.root,
                twig,
                &request.template,
            ));
        }
        let route_map = self.controller_routes(php)?;
        let mut lines = SourceLineResolver::default();
        let mut result = Vec::with_capacity(matching.len());
        for name in matching {
            check(ctx)?;
            let files = unique_twig_files(twig.get_twig_files_by_rel_path(&name)?);
            if !file_glob.is_empty() && !template_files_match_glob(&self.root, &files, &file_glob) {
                continue;
            }
            result.push(self.catalog_entry(twig, php, &name, &files, &route_map, &mut lines)?);
        }
        result.sort_by_cached_key(|entry| lower(&entry.template));
        Ok(result)
    }

    fn catalog_entry(
        &self,
        twig: &TwigIndexer,
        php: &PhpIndex,
        template: &str,
        files: &[TwigFile],
        routes: &RouteMap,
        lines: &mut SourceLineResolver,
    ) -> Result<TwigTemplateUsageCatalogEntry> {
        let mut entry = TwigTemplateUsageCatalogEntry {
            template: template.to_owned(),
            ..TwigTemplateUsageCatalogEntry::default()
        };
        for file in files {
            if file.path.is_empty() {
                continue;
            }
            entry.files.push(TwigTemplateSourceLocation {
                file_uri: swlsp_uriutil::file_uri(&file.path),
                line: 1,
            });
        }
        for reference in twig.get_template_references(&[template])? {
            let location = TwigTemplateReferenceUsage {
                file_uri: swlsp_uriutil::file_uri(&reference.file_path),
                line: lines.line(&reference.file_path, range_start(reference.range)),
            };
            match reference.kind {
                TemplateReferenceKind::Include => entry.includes.push(location),
                TemplateReferenceKind::Embed => entry.embeds.push(location),
                TemplateReferenceKind::Extends => entry.extends.push(location),
                TemplateReferenceKind::Import => entry.imports.push(location),
                TemplateReferenceKind::Use => entry.uses.push(location),
                TemplateReferenceKind::FormTheme => entry.form_themes.push(location),
                TemplateReferenceKind::Render | TemplateReferenceKind::Attribute => {
                    if let Some(controller) = controller_usage(php, &reference, routes, lines) {
                        entry.controllers.push(controller);
                    }
                }
                _ => {}
            }
        }
        entry.controllers = unique_template_controllers(std::mem::take(&mut entry.controllers));
        for list in [
            &mut entry.includes,
            &mut entry.embeds,
            &mut entry.extends,
            &mut entry.imports,
            &mut entry.uses,
            &mut entry.form_themes,
        ] {
            *list = unique_template_reference_usages(std::mem::take(list));
        }
        entry.components = self.component_usages(files, lines)?;
        Ok(entry)
    }

    fn controller_routes(&self, php: &PhpIndex) -> Result<RouteMap> {
        let mut result: RouteMap = HashMap::new();
        let Some(routes) = &self.routes else {
            return Ok(result);
        };
        for route in routes.get_routes_view()?.iter() {
            let Some(reference) = parse_controller_reference(&route.controller) else {
                continue;
            };
            let resolution =
                resolve_controller_reference(&reference, self.services.as_deref(), Some(php))?;
            if !resolution.method_found {
                continue;
            }
            let method = resolution.method.unwrap_or_default();
            result
                .entry(lower(&method.fully_qualified))
                .or_default()
                .push(TwigTemplateRouteEntry {
                    name: route.name.clone(),
                    path: route.path.clone(),
                    methods: normalized_route_methods(&route.methods),
                });
        }
        for entries in result.values_mut() {
            entries.sort_by_cached_key(|entry| lower(&entry.name));
        }
        Ok(result)
    }

    fn component_usages(
        &self,
        files: &[TwigFile],
        lines: &mut SourceLineResolver,
    ) -> Result<Vec<TwigTemplateComponentUsage>> {
        let Some(components) = &self.components else {
            return Ok(Vec::new());
        };
        let mut result = Vec::new();
        for file in files {
            for component in components.components_for_template(&file.path)? {
                for usage in components.usages(&component.name)? {
                    if usage.file == file.path {
                        continue;
                    }
                    result.push(TwigTemplateComponentUsage {
                        component: component.name.clone(),
                        syntax: usage.kind.to_string(),
                        file_uri: swlsp_uriutil::file_uri(&usage.file),
                        line: lines.line(&usage.file, range_start(usage.range)),
                    });
                }
            }
        }
        let mut seen = HashSet::with_capacity(result.len());
        result.retain(|usage| {
            seen.insert(format!(
                "{}\x00{}\x00{}",
                usage.component, usage.file_uri, usage.line
            ))
        });
        result.sort_by(|left, right| {
            left.file_uri
                .cmp(&right.file_uri)
                .then(left.line.cmp(&right.line))
                .then_with(|| left.component.cmp(&right.component))
        });
        Ok(result)
    }
}

impl CommandProvider for TwigTemplateUsageCatalogProvider {
    fn commands(&self, _ctx: &Context) -> BTreeMap<String, CommandFunc> {
        let provider = self.clone();
        let command: CommandFunc = Arc::new(move |ctx, args| provider.list(ctx, args));
        BTreeMap::from([(LIST_TWIG_TEMPLATE_USAGES_COMMAND.to_owned(), command)])
    }
}

fn controller_usage(
    php: &PhpIndex,
    reference: &TemplateReference,
    routes: &RouteMap,
    lines: &mut SourceLineResolver,
) -> Option<TwigTemplateControllerUsage> {
    let symbols = php.semantic_snapshot().symbols_in(&reference.file_path);
    let method = enclosing_template_method(&symbols, reference.range)?;
    let line_range = if range_len(method.selection_range) == 0 {
        method.range
    } else {
        method.selection_range
    };
    Some(TwigTemplateControllerUsage {
        controller: method.fully_qualified.to_string(),
        file_uri: swlsp_uriutil::file_uri(&method.path),
        line: lines.line(&method.path, range_start(line_range)),
        routes: routes
            .get(&lower(&method.fully_qualified))
            .cloned()
            .unwrap_or_default(),
    })
}

/// Template names of a workspace-relative template file path.
pub(crate) fn template_names_for_path_input(
    root: &str,
    twig: &TwigIndexer,
    input: &str,
) -> Vec<String> {
    let input = trim_space(input);
    if input.is_empty() || filepath::is_abs(input) {
        return Vec::new();
    }
    let path = filepath::clean(&filepath::join(&[root, &filepath::from_slash(input)]));
    if relative_inside(root, &path).is_none() {
        return Vec::new();
    }
    match twig.get_twig_file_by_path(&path) {
        Ok(Some(file)) => swlsp_twig::template_names(&file.path),
        _ => Vec::new(),
    }
}

pub(crate) fn template_files_match_glob(root: &str, files: &[TwigFile], glob: &str) -> bool {
    files.iter().any(|file| {
        relative_inside(root, &file.path)
            .is_some_and(|relative| !relative.is_empty() && ant_path_match(glob, &relative))
    })
}

pub(crate) fn unique_twig_files(files: Vec<TwigFile>) -> Vec<TwigFile> {
    let mut seen = HashSet::with_capacity(files.len());
    let mut result: Vec<TwigFile> = files
        .into_iter()
        .filter(|file| !file.path.is_empty() && seen.insert(file.path.clone()))
        .collect();
    result.sort_by(|left, right| left.path.cmp(&right.path));
    result
}

fn unique_template_controllers(
    controllers: Vec<TwigTemplateControllerUsage>,
) -> Vec<TwigTemplateControllerUsage> {
    let mut seen = HashSet::with_capacity(controllers.len());
    let mut result: Vec<_> = controllers
        .into_iter()
        .filter(|controller| {
            seen.insert(format!(
                "{}\x00{}\x00{}",
                lower(&controller.controller),
                controller.file_uri,
                controller.line
            ))
        })
        .collect();
    result.sort_by_cached_key(|controller| lower(&controller.controller));
    result
}

fn unique_template_reference_usages(
    usages: Vec<TwigTemplateReferenceUsage>,
) -> Vec<TwigTemplateReferenceUsage> {
    let mut seen = HashSet::with_capacity(usages.len());
    let mut result: Vec<_> = usages
        .into_iter()
        .filter(|usage| seen.insert(format!("{}\x00{}", usage.file_uri, usage.line)))
        .collect();
    result.sort_by(|left, right| {
        left.file_uri
            .cmp(&right.file_uri)
            .then(left.line.cmp(&right.line))
    });
    result
}
