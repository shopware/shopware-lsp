//! Route and Twig `controller()` usages from Twig and PHP files.

use std::path::Path;
use std::sync::{LazyLock, PoisonError, RwLock};

use serde::{Deserialize, Serialize};
use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    TransactionalClearer, TransactionalRemover,
};
use swlsp_parser_php::query as phpquery;
use swlsp_parser_php::syntax as phpsyntax;
use swlsp_parser_twig::query as twigquery;
use swlsp_php::PhpIndex;
use swlsp_syntax::{SyntaxNodeExt, TextRange};
use swlsp_textutil::FoldAsciiMatcher;

use crate::controller::{ControllerReference, resolve_controller_reference};
use crate::route_context::{
    is_php_route_name_in_context, route_url_path, twig_html_route_references,
    twig_route_comparison_references,
};
use crate::service_index::ServiceIndex;
use crate::twig_controller::{controller_reference_key, twig_controller_references};

static ROUTE_USAGE_PHP_CANDIDATE_MATCHER: LazyLock<FoldAsciiMatcher> =
    LazyLock::new(|| FoldAsciiMatcher::new(&["generate", "redirecttoroute"]));

/// One route reference (by name, or by static HTML URL path) in a file.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct RouteUsage {
    pub name: String,
    pub path: String,
    pub file: String,
    pub line: u32,
    /// Every occurrence of one route key in a file. `line` is the first.
    pub lines: Vec<u32>,
}

/// One static Twig `controller()` call.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct ControllerUsage {
    pub controller: String,
    pub target: String,
    pub method: String,
    pub file: String,
    pub range: TextRange,
}

/// Every equal controller reference of one file under one queryable key.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct ControllerUsageGroup {
    pub controller: String,
    pub usages: Vec<ControllerUsage>,
}

/// Keyed records in insertion order; queries sort, so the order does not
/// reach callers.
type Keyed<T> = Vec<(String, T)>;

struct PreparedRouteUsages {
    routes: Keyed<RouteUsage>,
    controllers: Option<Keyed<ControllerUsageGroup>>,
}

pub struct RouteUsageIndexer {
    routes: DataIndexer<RouteUsage>,
    controllers: DataIndexer<ControllerUsageGroup>,
    php_index: RwLock<Option<PhpIndex>>,
}

impl RouteUsageIndexer {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let config_dir = config_dir.as_ref();
        let routes = DataIndexer::repository(
            config_dir.join("route_usage.db"),
            "symfony.route_usage",
            store,
        )?;
        let controllers = match DataIndexer::repository(
            config_dir.join("twig_controller_usage.db"),
            "symfony.twig_controller_usage",
            store,
        ) {
            Ok(controllers) => controllers,
            Err(error) => {
                let _ = routes.close();
                return Err(error);
            }
        };
        Ok(Self {
            routes,
            controllers,
            php_index: RwLock::new(None),
        })
    }

    /// Enables typed route-generator validation for PHP files.
    pub fn set_php_index(&self, php_index: Option<PhpIndex>) {
        *self
            .php_index
            .write()
            .unwrap_or_else(PoisonError::into_inner) = php_index;
    }

    fn prepare_usages(&self, file: &ParsedFile) -> Option<PreparedRouteUsages> {
        match file.extension() {
            ".twig" => {
                let (routes, controllers) = prepare_twig(file);
                Some(PreparedRouteUsages {
                    routes,
                    controllers: Some(controllers),
                })
            }
            ".php" => Some(PreparedRouteUsages {
                routes: self.prepare_php(file),
                controllers: None,
            }),
            _ => None,
        }
    }

    fn prepare_php(&self, file: &ParsedFile) -> Keyed<RouteUsage> {
        let path = file.path();
        let mut usages: Keyed<RouteUsage> = Vec::new();
        if !ROUTE_USAGE_PHP_CANDIDATE_MATCHER.contains_str(file.source()) {
            return usages;
        }
        let Some(tree) = file.syntax_tree() else {
            return usages;
        };
        let root = tree.root();
        let line_index = file.line_index();
        let php_index = self
            .php_index
            .read()
            .unwrap_or_else(PoisonError::into_inner)
            .clone();
        let context = php_index.map(|php| php.parsed_file_context(file, Some(&root)));
        phpquery::visit(
            &root,
            &[
                phpsyntax::PHP_MEMBER_CALL,
                phpsyntax::PHP_SCOPED_CALL,
                phpsyntax::PHP_FUNCTION_CALL,
            ],
            |call| {
                let Some(literal) = phpquery::string_argument(call, 0) else {
                    return true;
                };
                if !is_php_route_name_in_context(context.as_ref(), &literal) {
                    return true;
                }
                let name = phpquery::string_value(&literal);
                if name.is_empty() {
                    return true;
                }
                let line = line_index
                    .position(literal.range_trimmed_trivia().start().into())
                    .0
                    + 1;
                add_route_usage(
                    &mut usages,
                    &name,
                    RouteUsage {
                        name: name.clone(),
                        file: path.to_owned(),
                        line,
                        ..RouteUsage::default()
                    },
                );
                true
            },
        );
        usages
    }

    /// Usages of the route `name`, one per occurrence, sorted.
    pub fn get_route(&self, name: &str) -> Result<Vec<RouteUsage>> {
        Ok(expand_route_usage_lines(
            self.routes.get_values_view(name)?.iter(),
        ))
    }

    /// Static HTML URL usages, one per occurrence, sorted.
    pub fn get_html_route_usages(&self) -> Result<Vec<RouteUsage>> {
        Ok(
            expand_route_usage_lines(self.routes.get_all_values_view()?.iter())
                .into_iter()
                .filter(|usage| !usage.path.is_empty())
                .collect(),
        )
    }

    /// Usages of one controller reference, sorted by file and range.
    pub fn get_controller_usages(
        &self,
        reference: &ControllerReference,
    ) -> Result<Vec<ControllerUsage>> {
        let groups = self
            .controllers
            .get_values_view(&controller_reference_key(reference))?;
        let mut result: Vec<ControllerUsage> = groups
            .iter()
            .flat_map(|group| group.usages.clone())
            .collect();
        sort_controller_usages(&mut result);
        Ok(result)
    }

    /// Every controller usage, sorted by file and range.
    pub fn get_all_controller_usages(&self) -> Result<Vec<ControllerUsage>> {
        let groups = self.controllers.get_all_values_view()?;
        let mut result: Vec<ControllerUsage> = groups
            .iter()
            .flat_map(|group| group.usages.clone())
            .collect();
        sort_controller_usages(&mut result);
        Ok(result)
    }

    /// Resolves aliases and service IDs so PHP-side references find every
    /// equivalent Twig spelling of `class_name::method_name`.
    pub fn controller_usages_for_method(
        &self,
        class_name: &str,
        method_name: &str,
        services: Option<&ServiceIndex>,
        php_index: Option<&PhpIndex>,
    ) -> Result<Vec<ControllerUsage>> {
        if class_name.is_empty() || method_name.is_empty() || php_index.is_none() {
            return Ok(Vec::new());
        }
        let class_name = class_name.trim_start_matches('\\');
        let mut result = Vec::new();
        let mut seen = std::collections::HashSet::new();
        for usage in self.get_all_controller_usages()? {
            let reference = ControllerReference {
                value: usage.controller.clone(),
                target: usage.target.clone(),
                method: usage.method.clone(),
            };
            let resolution = resolve_controller_reference(&reference, services, php_index)?;
            let class_matches = resolution.class.as_ref().is_some_and(|class| {
                swlsp_textutil::eq_ignore_case(
                    class.fully_qualified.as_str().trim_start_matches('\\'),
                    class_name,
                )
            });
            let method_matches = resolution.method.as_ref().is_some_and(|method| {
                swlsp_textutil::eq_ignore_case(method.name.as_str(), method_name)
            });
            if !resolution.method_declared || !class_matches || !method_matches {
                continue;
            }
            let key = format!(
                "{}:{}:{}",
                usage.file,
                u32::from(usage.range.start()),
                u32::from(usage.range.end())
            );
            if seen.insert(key) {
                result.push(usage);
            }
        }
        Ok(result)
    }

    fn remove(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        let routes = self.routes.batch_delete_by_file_paths_in(mutation, paths);
        let controllers = self
            .controllers
            .batch_delete_by_file_paths_in(mutation, paths);
        routes.and(controllers)
    }

    fn clear_all(&self, mutation: Option<&Mutation>) -> Result<()> {
        let routes = self.routes.clear_in(mutation);
        let controllers = self.controllers.clear_in(mutation);
        routes.and(controllers)
    }
}

fn prepare_twig(file: &ParsedFile) -> (Keyed<RouteUsage>, Keyed<ControllerUsageGroup>) {
    let path = file.path();
    let mut usages: Keyed<RouteUsage> = Vec::new();
    let mut groups: Keyed<ControllerUsageGroup> = Vec::new();
    let Some(tree) = file.syntax_tree() else {
        return (usages, groups);
    };
    let root = tree.root();
    let line_index = file.line_index();
    let line_of = |offset: swlsp_syntax::TextSize| line_index.position(offset.into()).0 + 1;

    for literal in twigquery::string_arguments_in_functions(&root, &["seoUrl", "url", "path"]) {
        let name = twigquery::string_value(&literal).unwrap_or_default();
        if name.is_empty() {
            continue;
        }
        let line = line_of(literal.range_trimmed_trivia().start());
        add_route_usage(
            &mut usages,
            &name,
            RouteUsage {
                name: name.clone(),
                file: path.to_owned(),
                line,
                ..RouteUsage::default()
            },
        );
    }
    for reference in twig_route_comparison_references(&root) {
        if reference.value.is_empty() {
            continue;
        }
        let line = line_of(reference.range.start());
        add_route_usage(
            &mut usages,
            &reference.value,
            RouteUsage {
                name: reference.value.clone(),
                file: path.to_owned(),
                line,
                ..RouteUsage::default()
            },
        );
    }
    for reference in twig_html_route_references(&root) {
        let route_path = route_url_path(&reference.value);
        if route_path.is_empty() {
            continue;
        }
        let line = line_of(reference.node.range_trimmed_trivia().start());
        add_route_usage(
            &mut usages,
            &format!("@url:{route_path}"),
            RouteUsage {
                path: route_path,
                file: path.to_owned(),
                line,
                ..RouteUsage::default()
            },
        );
    }
    for reference in twig_controller_references(&root) {
        let key = controller_reference_key(&reference.reference);
        let usage = ControllerUsage {
            controller: reference.reference.value.clone(),
            target: reference.reference.target.clone(),
            method: reference.reference.method.clone(),
            file: path.to_owned(),
            range: reference.range,
        };
        match groups.iter_mut().find(|(existing, _)| *existing == key) {
            Some((_, group)) => group.usages.push(usage),
            None => groups.push((
                key.clone(),
                ControllerUsageGroup {
                    controller: key,
                    usages: vec![usage],
                },
            )),
        }
    }
    (usages, groups)
}

fn add_route_usage(usages: &mut Keyed<RouteUsage>, key: &str, mut usage: RouteUsage) {
    let Some((_, existing)) = usages.iter_mut().find(|(existing, _)| existing == key) else {
        usage.lines = vec![usage.line];
        usages.push((key.to_owned(), usage));
        return;
    };
    if existing.name.is_empty() {
        existing.name = usage.name;
    }
    if existing.path.is_empty() {
        existing.path = usage.path;
    }
    if existing.file.is_empty() {
        existing.file = usage.file;
    }
    if existing.line == 0 {
        existing.line = usage.line;
    }
    if existing.lines.is_empty() && existing.line > 0 {
        existing.lines.push(existing.line);
    }
    if !existing.lines.contains(&usage.line) {
        existing.lines.push(usage.line);
    }
}

fn expand_route_usage_lines<'a>(values: impl Iterator<Item = &'a RouteUsage>) -> Vec<RouteUsage> {
    let mut result = Vec::new();
    for usage in values {
        let lines = if usage.lines.is_empty() && usage.line > 0 {
            vec![usage.line]
        } else {
            usage.lines.clone()
        };
        for line in lines {
            result.push(RouteUsage {
                line,
                lines: Vec::new(),
                ..usage.clone()
            });
        }
    }
    result.sort_by(|left, right| {
        left.file
            .cmp(&right.file)
            .then(left.line.cmp(&right.line))
            .then_with(|| left.name.cmp(&right.name))
            .then_with(|| left.path.cmp(&right.path))
    });
    result
}

fn sort_controller_usages(usages: &mut [ControllerUsage]) {
    usages.sort_by(|left, right| {
        left.file
            .cmp(&right.file)
            .then(left.range.start().cmp(&right.range.start()))
            .then(left.range.end().cmp(&right.range.end()))
    });
}

impl Indexer for RouteUsageIndexer {
    fn id(&self) -> &str {
        "symfony.route_usage"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let prepared = self.prepare(file)?;
        self.index_prepared(file, prepared.as_ref())
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.remove(paths, None)
    }

    fn close(&self) -> Result<()> {
        let routes = self.routes.close();
        let controllers = self.controllers.close();
        routes.and(controllers)
    }

    fn clear(&self) -> Result<()> {
        self.clear_all(None)
    }

    fn as_preparing(&self) -> Option<&dyn PreparingIndexer> {
        Some(self)
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

fn save_items<T: Serialize + serde::de::DeserializeOwned + Send + Sync + Clone + 'static>(
    repository: &DataIndexer<T>,
    mutation: Option<&Mutation>,
    path: &str,
    items: &Keyed<T>,
) -> Result<()> {
    repository.batch_save_items_in(
        mutation,
        [(path, items.iter().map(|(key, value)| (key.as_str(), value)))],
    )
}

impl PreparingIndexer for RouteUsageIndexer {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        Ok(self
            .prepare_usages(file)
            .map(|prepared| Box::new(prepared) as Prepared))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        let Some(prepared) = prepared.and_then(|value| value.downcast_ref::<PreparedRouteUsages>())
        else {
            return Ok(());
        };
        save_items(&self.routes, file.mutation(), file.path(), &prepared.routes)?;
        if let Some(controllers) = &prepared.controllers {
            save_items(&self.controllers, file.mutation(), file.path(), controllers)?;
        }
        Ok(())
    }
}

impl TransactionalRemover for RouteUsageIndexer {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.remove(paths, Some(mutation))
    }
}

impl TransactionalClearer for RouteUsageIndexer {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.clear_all(Some(mutation))
    }
}

#[cfg(test)]
#[path = "tests_routectx/route_usage_indexer.rs"]
mod tests;
