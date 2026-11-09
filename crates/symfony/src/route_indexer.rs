//! The Symfony route index (Go `RouteIndexer`): routes from YAML, XML, and
//! PHP (attributes and `RoutingConfigurator`) plus routing imports, merged
//! with the compiled URL generator catalog.

use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{
    DataIndexer, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    TransactionalClearer, TransactionalRemover,
};
use swlsp_syntax::TextRange;

use crate::compiled_catalog_index::CompiledCatalogIndex;
use crate::compiled_route_catalog::{CompiledRouteCatalog, is_compiled_route_file_name};
use crate::model::Route;
use crate::php_routes::parse_php_routes_tree;
use crate::route_constant_names::{ClassConstantLookup, resolve_constant_route_names};
use crate::route_context::normalize_route_search_path;
use crate::route_resource::{RouteResourceImport, route_resource_references};
use crate::util::{base_name, contains_fold_ascii, go_trim_space};
use crate::workspace_symbols::add_route_workspace_symbols;
use crate::xml_routes::parse_xml_routes_tree;
use crate::yaml_routes::parse_yaml_routes_tree;

/// The first route whose controller is `name` (Go `RouteList.GetByController`).
pub fn route_by_controller<'a>(routes: &'a [Route], name: &str) -> Option<&'a Route> {
    routes.iter().find(|route| route.controller == name)
}

/// Indexes Symfony routes and routing imports.
pub struct RouteIndexer {
    routes: DataIndexer<Route>,
    resource_imports: DataIndexer<RouteResourceImport>,
    compiled_routes: Option<Arc<CompiledRouteCatalog>>,
    /// [`Self::get_routes`] memoized per stored catalog view and compiled
    /// catalog revision.
    routes_view: std::sync::Mutex<Option<RoutesView>>,
}

/// A merged route list and the inputs it was built from. Holding the stored
/// view keeps its allocation alive, so pointer identity cannot be reused.
struct RoutesView {
    stored: Arc<[Route]>,
    compiled_revision: u64,
    routes: Arc<[Route]>,
}

impl std::fmt::Debug for RouteIndexer {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("RouteIndexer").finish_non_exhaustive()
    }
}

impl RouteIndexer {
    /// Advanced after every change to the source routes, route resource
    /// imports or the compiled route catalog.
    pub fn revision(&self) -> u64 {
        self.routes.revision()
            + self.resource_imports.revision()
            + self
                .compiled_routes
                .as_ref()
                .map_or(0, |compiled| compiled.revision())
    }

    /// Opens the route repositories without a compiled route catalog.
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let config_dir = config_dir.as_ref();
        let routes = DataIndexer::repository(config_dir.join("route.db"), "symfony.routes", store)?;
        let resource_imports = match DataIndexer::repository(
            config_dir.join("route_resources.db"),
            "symfony.route_resources",
            store,
        ) {
            Ok(repository) => repository,
            Err(error) => {
                let _ = routes.close();
                return Err(error);
            }
        };
        Ok(Self {
            routes,
            resource_imports,
            compiled_routes: None,
            routes_view: std::sync::Mutex::new(None),
        })
    }

    /// Opens the route repositories with the project's compiled route catalog
    /// (Go `NewProjectRouteIndexer`).
    pub fn new_project(
        project_root: &str,
        config_dir: impl AsRef<Path>,
        store: Option<&Store>,
    ) -> Result<Self> {
        let mut index = Self::new(config_dir, store)?;
        index.compiled_routes = Some(Arc::new(CompiledRouteCatalog::new(project_root)));
        Ok(index)
    }

    /// The compiled route catalog, when the indexer was created for a project.
    pub fn compiled_route_catalog(&self) -> Option<&Arc<CompiledRouteCatalog>> {
        self.compiled_routes.as_ref()
    }

    /// The scanner-fed compiled route index publishing into this indexer's
    /// catalog (Go `NewCompiledRouteIndex`). An indexer without a project
    /// catalog gets a fresh, unshared one.
    pub fn compiled_route_index(
        &self,
        root: &str,
        cache: impl AsRef<Path>,
        store: Option<&Store>,
    ) -> Result<CompiledCatalogIndex> {
        let catalog = self
            .compiled_routes
            .clone()
            .unwrap_or_else(|| Arc::new(CompiledRouteCatalog::new(root)));
        CompiledCatalogIndex::new_routes(root, cache, catalog, store)
    }

    /// Every route with a literal name. Routes named by a class constant are
    /// left out; callers holding the PHP index use [`Self::resolved_routes`].
    pub fn get_routes(&self) -> Result<Vec<Route>> {
        self.get_routes_view().map(|routes| routes.to_vec())
    }

    /// The shared, immutable form of [`Self::get_routes`]: rebuilt only when
    /// the stored routes or the compiled catalog change.
    pub fn get_routes_view(&self) -> Result<Arc<[Route]>> {
        let stored = self.routes.get_all_values_view()?;
        let compiled_revision = self
            .compiled_routes
            .as_ref()
            .map_or(0, |compiled| compiled.revision());
        let mut cache = self
            .routes_view
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if let Some(view) = cache.as_ref()
            && Arc::ptr_eq(&view.stored, &stored)
            && view.compiled_revision == compiled_revision
        {
            return Ok(view.routes.clone());
        }
        let routes: Arc<[Route]> = self.merge_compiled(stored.to_vec(), None).into();
        *cache = Some(RoutesView {
            stored,
            compiled_revision,
            routes: routes.clone(),
        });
        Ok(routes)
    }

    /// Routes declared in indexed source files, without the compiled
    /// catalog and without resolving class-constant names.
    pub fn source_routes_view(&self) -> Result<Arc<[Route]>> {
        self.routes.get_all_values_view()
    }

    /// Advanced after every committed change to the source routes.
    pub fn source_revision(&self) -> u64 {
        self.routes.revision()
    }

    /// The first route whose controller is `name` (Go
    /// `GetRoutes().GetByController(name)`), without copying the catalog.
    pub fn route_by_controller(&self, name: &str) -> Result<Option<Route>> {
        Ok(route_by_controller(&self.get_routes_view()?, name).cloned())
    }

    /// [`Self::get_routes`] plus routes whose name is a class constant, with
    /// `lookup` supplying the literal behind each one.
    pub fn resolved_routes(&self, lookup: Option<&dyn ClassConstantLookup>) -> Result<Vec<Route>> {
        self.routes_with(lookup)
    }

    fn routes_with(&self, lookup: Option<&dyn ClassConstantLookup>) -> Result<Vec<Route>> {
        if lookup.is_none() {
            return self.get_routes();
        }
        let stored = self.routes.get_all_values()?;
        Ok(self.merge_compiled(stored, lookup))
    }

    fn merge_compiled(
        &self,
        stored: Vec<Route>,
        lookup: Option<&dyn ClassConstantLookup>,
    ) -> Vec<Route> {
        let mut routes = resolve_constant_route_names(stored, lookup);
        let Some(compiled) = self.compiled_routes.as_ref() else {
            return routes;
        };
        let mut seen: std::collections::HashSet<String> =
            routes.iter().map(|route| route.name.clone()).collect();
        for route in compiled.routes().0 {
            if seen.insert(route.name.clone()) {
                routes.push(route);
            }
        }
        routes
    }

    /// Source routes named `name`, falling back to the compiled catalog.
    pub fn get_route(&self, name: &str) -> Result<Vec<Route>> {
        let routes = self.routes.get_values(name)?;
        if !routes.is_empty() {
            return Ok(routes);
        }
        Ok(self
            .compiled_routes
            .as_ref()
            .and_then(|compiled| compiled.route(name))
            .into_iter()
            .collect())
    }

    pub fn get_route_resource_imports(&self) -> Result<Vec<RouteResourceImport>> {
        self.resource_imports.get_all_values()
    }

    /// The shared, immutable form of [`Self::get_route_resource_imports`].
    pub fn get_route_resource_imports_view(&self) -> Result<Arc<[RouteResourceImport]>> {
        self.resource_imports.get_all_values_view()
    }

    /// The currently loaded generated-route catalog without source precedence.
    pub fn get_compiled_routes(&self) -> Vec<Route> {
        self.compiled_routes
            .as_ref()
            .map(|compiled| compiled.routes().0)
            .unwrap_or_default()
    }

    /// Reparses the selected generated route catalog from disk. A missing
    /// catalog is not an error.
    pub fn reload_compiled_routes(&self) -> std::io::Result<()> {
        let Some(compiled) = self.compiled_routes.as_ref() else {
            return Ok(());
        };
        match compiled.refresh() {
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
            other => other,
        }
    }

    /// Routes whose path matches a concrete or partial request URL; exact
    /// matches first.
    pub fn find_routes_by_path(&self, value: &str) -> Result<Vec<Route>> {
        let routes = self.get_routes_view()?;
        let matcher = RoutePathSearchMatcher::new(value);
        if matcher.search_path().is_empty() {
            return Ok(Vec::new());
        }
        let mut exact = Vec::new();
        let mut partial = Vec::new();
        for route in routes.iter() {
            if !matcher.matches(&route.path) {
                continue;
            }
            if route_path_matches(&route.path, matcher.search_path()) {
                exact.push(route.clone());
            } else {
                partial.push(route.clone());
            }
        }
        sort_routes(&mut exact);
        sort_routes(&mut partial);
        exact.extend(partial);
        Ok(exact)
    }

    /// Routes rooted at a complete path segment of `prefix`, excluding the
    /// route whose path is `full_path`.
    pub fn find_routes_by_path_prefix(&self, prefix: &str, full_path: &str) -> Result<Vec<Route>> {
        let routes = self.get_routes_view()?;
        let prefix = normalized_route_path(prefix);
        let full_path = normalized_route_path(full_path);
        if prefix.is_empty() {
            return Ok(Vec::new());
        }
        let prefix_with_slash = swlsp_php_types::go_to_lower(&format!(
            "{}/",
            prefix.strip_suffix('/').unwrap_or(&prefix)
        ));
        let mut result = Vec::new();
        for route in routes.iter() {
            let path = normalized_route_path(&route.path);
            if path.is_empty() || swlsp_php_types::equal_fold(&path, &full_path) {
                continue;
            }
            let path_with_slash = swlsp_php_types::go_to_lower(&format!(
                "{}/",
                path.strip_suffix('/').unwrap_or(&path)
            ));
            if path_with_slash.starts_with(&prefix_with_slash) {
                result.push(route.clone());
            }
        }
        sort_routes(&mut result);
        Ok(result)
    }

    /// Parses the file's routes and routing imports without writing
    /// anything; `None` for files the route index ignores.
    fn extract(&self, file: &ParsedFile) -> Option<PreparedRoutes> {
        let (routes, key) = match file.extension() {
            ".yml" | ".yaml" => (
                parse_yaml_routes_tree(file.path(), file.syntax_tree(), file.line_index()),
                RouteKey::Name,
            ),
            ".xml" => (
                parse_xml_routes_tree(file.path(), file.syntax_tree(), file.line_index()),
                RouteKey::Name,
            ),
            ".php" => {
                let routes = if contains_fold_ascii(file.source().as_bytes(), "route") {
                    parse_php_routes_tree(file.path(), file.syntax_tree(), Some(file.line_index()))
                } else {
                    Vec::new()
                };
                (routes, RouteKey::NameOrConstant)
            }
            _ => return None,
        };
        Some(PreparedRoutes {
            routes,
            key,
            resources: extract_route_resources(file),
        })
    }

    /// Writes prepared routes and routing imports for `file`.
    fn persist(&self, file: &ParsedFile, prepared: Option<&PreparedRoutes>) -> Result<()> {
        let Some(prepared) = prepared else {
            return Ok(());
        };
        self.save_routes(file, &prepared.routes, |route| prepared.key.of(route))?;
        self.resource_imports.batch_save_items_in(
            file.mutation(),
            [(
                file.path(),
                prepared
                    .resources
                    .iter()
                    .map(|(key, import)| (key.as_str(), import)),
            )],
        )
    }

    fn save_routes<'r>(
        &self,
        file: &ParsedFile,
        routes: &'r [Route],
        key: impl Fn(&'r Route) -> &'r str,
    ) -> Result<()> {
        let path = file.path();
        // Go keys a path → key map; the last route of a key wins.
        let mut buckets: Vec<(String, Vec<(String, &Route)>)> = vec![(path.to_owned(), Vec::new())];
        for route in routes {
            let route_key = key(route);
            if route_key.is_empty() {
                continue;
            }
            let index = match buckets
                .iter()
                .position(|(existing, _)| *existing == route.file_path)
            {
                Some(index) => index,
                None => {
                    buckets.push((route.file_path.clone(), Vec::new()));
                    buckets.len() - 1
                }
            };
            let bucket = &mut buckets[index].1;
            match bucket
                .iter_mut()
                .find(|(existing, _)| existing == route_key)
            {
                Some(entry) => entry.1 = route,
                None => bucket.push((route_key.to_owned(), route)),
            }
        }
        add_route_workspace_symbols(file, routes);
        self.routes.batch_save_items_in(
            file.mutation(),
            buckets.iter().map(|(path, items)| {
                (
                    path.as_str(),
                    items.iter().map(|(key, route)| (key.as_str(), *route)),
                )
            }),
        )
    }

    fn remove(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        let routes = self.routes.batch_delete_by_file_paths_in(mutation, paths);
        let resources = self
            .resource_imports
            .batch_delete_by_file_paths_in(mutation, paths);
        routes.and(resources)
    }

    fn clear_all(&self, mutation: Option<&Mutation>) -> Result<()> {
        let routes = self.routes.clear_in(mutation);
        let resources = self.resource_imports.clear_in(mutation);
        routes.and(resources)
    }
}

/// Routes and routing imports parsed ahead of the write transaction.
struct PreparedRoutes {
    routes: Vec<Route>,
    key: RouteKey,
    /// Routing imports keyed by range; the last import of a key wins.
    resources: Vec<(String, RouteResourceImport)>,
}

/// The repository key of a route.
#[derive(Clone, Copy)]
enum RouteKey {
    Name,
    /// A constant-named route has no literal name yet; key it by the
    /// reference so it survives indexing.
    NameOrConstant,
}

impl RouteKey {
    fn of(self, route: &Route) -> &str {
        match self {
            Self::NameOrConstant if route.name.is_empty() => &route.name_constant,
            Self::Name | Self::NameOrConstant => &route.name,
        }
    }
}

fn extract_route_resources(file: &ParsedFile) -> Vec<(String, RouteResourceImport)> {
    let mut resources: Vec<(String, RouteResourceImport)> = Vec::new();
    if may_import_route_resources(file)
        && let Some(tree) = file.syntax_tree()
    {
        for reference in route_resource_references(&tree.root()) {
            let key = range_key(reference.range);
            let import = RouteResourceImport {
                path: reference.path,
                loader: reference.loader,
                namespace: reference.namespace,
                file_path: file.path().to_owned(),
                range: reference.range,
                nested: reference.nested,
            };
            match resources.iter_mut().find(|(existing, _)| *existing == key) {
                Some(entry) => entry.1 = import,
                None => resources.push((key, import)),
            }
        }
    }
    resources
}

/// A cheap superset of the files [`route_resource_references`] can match:
/// PHP imports need a parameter typed as the routing configurator, whose
/// short class name appears in any spelling of that type; YAML and XML
/// imports need a `resource` key or attribute. Walking every string of every
/// PHP file otherwise dominates the serialized index phase.
fn may_import_route_resources(file: &ParsedFile) -> bool {
    let source = file.source().as_bytes();
    match file.extension() {
        ".php" => contains_fold_ascii(source, "routingconfigurator"),
        _ => contains_fold_ascii(source, "resource"),
    }
}

/// Go `cst.TextRange.String`: `start..end`.
pub(crate) fn range_key(range: TextRange) -> String {
    format!("{}..{}", u32::from(range.start()), u32::from(range.end()))
}

impl Indexer for RouteIndexer {
    fn id(&self) -> &str {
        "symfony.route"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        self.persist(file, self.extract(file).as_ref())
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.remove(paths, None)
    }

    fn close(&self) -> Result<()> {
        let routes = self.routes.close();
        let resources = self.resource_imports.close();
        routes.and(resources)
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

impl PreparingIndexer for RouteIndexer {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        Ok(Some(Box::new(self.extract(file))))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        match prepared.and_then(|value| value.downcast_ref::<Option<PreparedRoutes>>()) {
            Some(prepared) => self.persist(file, prepared.as_ref()),
            None => self.index(file),
        }
    }
}

impl TransactionalRemover for RouteIndexer {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.remove(paths, Some(mutation))
    }
}

impl TransactionalClearer for RouteIndexer {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.clear_all(Some(mutation))
    }
}

/// Sorts source definitions before generated catalogs, then by path, name,
/// file, and line.
pub(crate) fn sort_routes(routes: &mut [Route]) {
    routes.sort_by(|left, right| {
        let left_compiled = is_compiled_route_file_name(base_name(&left.file_path));
        let right_compiled = is_compiled_route_file_name(base_name(&right.file_path));
        left_compiled
            .cmp(&right_compiled)
            .then_with(|| left.path.cmp(&right.path))
            .then_with(|| left.name.cmp(&right.name))
            .then_with(|| left.file_path.cmp(&right.file_path))
            .then_with(|| left.line.cmp(&right.line))
    });
}

fn normalized_route_path(value: &str) -> String {
    let value = go_trim_space(value);
    if value.is_empty() {
        return String::new();
    }
    format!("/{}", value.trim_matches('/'))
}

const ROUTE_PATH_PLACEHOLDER_MARKER: u8 = 0x1f;

/// Exact reverse matching of a concrete URL against Symfony placeholders
/// such as `/catalog/{id}`. Partial URLs never match.
pub fn route_path_matches(pattern: &str, value: &str) -> bool {
    let value = normalize_route_search_path(value);
    if pattern.is_empty() || value.is_empty() {
        return false;
    }
    let route_path = neutralize_route_placeholders(pattern);
    let width = value.len() + 1;
    let mut memo = vec![0u8; (route_path.len() + 1) * width];
    exact_route_path_match(&route_path, value.as_bytes(), 0, 0, width, &mut memo)
}

fn exact_route_path_match(
    route_path: &[u8],
    value: &[u8],
    route_index: usize,
    value_index: usize,
    width: usize,
    memo: &mut [u8],
) -> bool {
    if route_index == route_path.len() {
        return value_index == value.len();
    }
    if value_index == value.len() {
        return false;
    }
    let key = route_index * width + value_index;
    if memo[key] != 0 {
        return memo[key] == 1;
    }
    let mut matches = false;
    if route_path[route_index] == ROUTE_PATH_PLACEHOLDER_MARKER {
        for offset in value_index..value.len() {
            if !is_route_placeholder_path_byte(value[offset]) {
                break;
            }
            if exact_route_path_match(route_path, value, route_index + 1, offset + 1, width, memo) {
                matches = true;
                break;
            }
        }
    } else if route_path[route_index] == value[value_index] {
        matches = exact_route_path_match(
            route_path,
            value,
            route_index + 1,
            value_index + 1,
            width,
            memo,
        );
    }
    memo[key] = if matches { 1 } else { 2 };
    matches
}

/// Partial reverse-route matching that treats every Symfony placeholder as
/// one URL path segment. Construct once and call [`Self::matches`] for every
/// route of an index scan.
#[derive(Clone, Debug)]
pub struct RoutePathSearchMatcher {
    search_path: String,
    max_prefix_length: usize,
    prefix_table: Vec<usize>,
}

impl RoutePathSearchMatcher {
    pub fn new(search_path: &str) -> Self {
        let search_path = normalize_route_search_path(search_path);
        let max_prefix_length = search_path.len().saturating_sub(2);
        let prefix_table =
            build_route_search_prefix_table(search_path.as_bytes(), max_prefix_length);
        Self {
            search_path,
            max_prefix_length,
            prefix_table,
        }
    }

    pub fn search_path(&self) -> &str {
        &self.search_path
    }

    pub fn matches(&self, route_path: &str) -> bool {
        if route_path.is_empty() || self.search_path.is_empty() {
            return false;
        }
        if route_path.contains(self.search_path.as_str()) {
            return true;
        }
        if self.max_prefix_length == 0 || !route_path.contains('{') {
            return false;
        }
        let route_path = neutralize_route_placeholders(route_path);
        let Some((route_index, length)) = self.longest_search_prefix_match(&route_path) else {
            return false;
        };
        let width = self.search_path.len() + 1;
        let mut memo = vec![0u8; (route_path.len() + 1) * width];
        self.matches_route_prefix(&route_path, route_index + length, length, width, &mut memo)
    }

    fn longest_search_prefix_match(&self, route_path: &[u8]) -> Option<(usize, usize)> {
        let search = self.search_path.as_bytes();
        let mut matched = 0;
        let mut best: Option<(usize, usize)> = None;
        for (route_index, &value) in route_path.iter().enumerate() {
            if matched == self.max_prefix_length {
                break;
            }
            while matched > 0 && value != search[matched] {
                matched = self.prefix_table[matched - 1];
            }
            if value != search[matched] {
                continue;
            }
            matched += 1;
            if best.is_none_or(|(_, length)| matched > length) {
                best = Some((route_index + 1 - matched, matched));
                if matched == self.max_prefix_length {
                    break;
                }
            }
        }
        best
    }

    fn matches_route_prefix(
        &self,
        route_path: &[u8],
        route_index: usize,
        search_index: usize,
        width: usize,
        memo: &mut [u8],
    ) -> bool {
        let search = self.search_path.as_bytes();
        if search_index == search.len() {
            return true;
        }
        if route_index == route_path.len() {
            return false;
        }
        let key = route_index * width + search_index;
        if memo[key] != 0 {
            return memo[key] == 1;
        }
        let mut matches = false;
        if route_path[route_index] == ROUTE_PATH_PLACEHOLDER_MARKER {
            for (offset, &byte) in search.iter().enumerate().skip(search_index) {
                if !is_route_placeholder_path_byte(byte) {
                    break;
                }
                if self.matches_route_prefix(route_path, route_index + 1, offset + 1, width, memo) {
                    matches = true;
                    break;
                }
            }
        } else if route_path[route_index] == search[search_index] {
            matches = self.matches_route_prefix(
                route_path,
                route_index + 1,
                search_index + 1,
                width,
                memo,
            );
        }
        memo[key] = if matches { 1 } else { 2 };
        matches
    }
}

/// Convenience form of [`RoutePathSearchMatcher`] for one route.
pub fn route_path_search_matches(pattern: &str, search_path: &str) -> bool {
    RoutePathSearchMatcher::new(search_path).matches(pattern)
}

fn build_route_search_prefix_table(search_path: &[u8], max_prefix_length: usize) -> Vec<usize> {
    let mut prefix_table = vec![0; max_prefix_length];
    let mut matched = 0;
    for index in 1..max_prefix_length {
        let value = search_path[index];
        while matched > 0 && value != search_path[matched] {
            matched = prefix_table[matched - 1];
        }
        if value == search_path[matched] {
            matched += 1;
        }
        prefix_table[index] = matched;
    }
    prefix_table
}

fn neutralize_route_placeholders(route_path: &str) -> Vec<u8> {
    let bytes = route_path.as_bytes();
    let mut result = Vec::with_capacity(bytes.len());
    let mut offset = 0;
    while offset < bytes.len() {
        if bytes[offset] != b'{' {
            result.push(bytes[offset]);
            offset += 1;
            continue;
        }
        match bytes[offset + 1..].iter().position(|&byte| byte == b'}') {
            None => {
                result.push(bytes[offset]);
                offset += 1;
            }
            Some(end) => {
                result.push(ROUTE_PATH_PLACEHOLDER_MARKER);
                offset += end + 2;
            }
        }
    }
    result
}

fn is_route_placeholder_path_byte(value: u8) -> bool {
    value != b'/' && value != b'?' && value != b'#'
}

#[cfg(test)]
#[path = "tests_routes/route_indexer.rs"]
pub(crate) mod tests;

impl swlsp_indexer::Revisioned for RouteIndexer {
    fn revision(&self) -> u64 {
        RouteIndexer::revision(self)
    }
}
