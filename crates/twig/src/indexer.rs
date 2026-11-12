//! The persistent Twig index.
//!
//! Extraction happens in [`PreparingIndexer::prepare`], ahead of
//! the workspace write transaction; `index_prepared` only persists.

use std::collections::{BTreeMap, HashSet};
use std::path::Path;
use std::sync::{Arc, RwLock};

use serde::Serialize;
use serde::de::DeserializeOwned;
use swlsp_indexer::{
    DataIndexer, Error, Indexer, Mutation, ParsedFile, Prepared, PreparingIndexer, Result, Store,
    SupplementalPathIndexer, TransactionalClearer, TransactionalRemover,
    WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK, WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
    WorkspaceSymbol, WorkspaceSymbolKind, WorkspaceSymbolRange,
};
use swlsp_php::PhpIndex;
use swlsp_symfony::ServiceIndex;
use swlsp_syntax::SyntaxNode;

use crate::compare_fold;
use crate::extension_parser::{
    TwigFilter, TwigFunction, TwigOperator, TwigTest, is_twig_callable_definition_candidate,
    parse_twig_extension_tree, parse_twig_operators,
};
use crate::extension_usage::{
    ExtensionUsage, ExtensionUsageCatalog, ExtensionUsageKind, extension_usage_key,
    extension_usages_in_document, unique_extension_usages,
};
use crate::global::{
    Global, GlobalCatalog, GlobalSource, globals_in_php_extension, globals_in_yaml, unique_globals,
};
use crate::macros::{
    Macro, MacroCatalog, MacroReferenceRole, MacroUsage, MacroUsageRecord,
    macro_references_in_document, macro_usage_key, macros_in_document, normalize_macro_template,
    sort_macro_usages, sort_macros,
};
use crate::parser::{TwigBlock, TwigBlockHash, TwigFile, new_twig_file, parse_twig_tree};
use crate::path::{
    clean, convert_to_relative_path, ext, is_excluded_twig_path, is_storefront_dist_directory,
    is_template_asset_path, storefront_dist_relative, template_names, to_slash,
};
use crate::php_access::PhpAccessResolver;
use crate::php_usage::{
    ConstantReference, ConstantUsageCatalog, PhpUsageCatalog, PhpUsageReference,
    constant_reference_key, constant_references_in_document, php_usage_reference_key,
    php_usage_references_in_document, unique_constant_references, unique_php_usage_references,
};
use crate::reference::{
    TemplateReference, TemplateReferenceCatalog, normalize_template_reference,
    twig_template_lookup_name, twig_template_references, unique_template_references,
};
use crate::template_variable::{
    TemplateVariable, TemplateVariableCatalog, template_dependencies_in_document,
    template_input_variables_in_document,
};
use crate::token_parser::{TwigTag, is_twig_token_parser_candidate, parse_twig_token_parsers};

type Records<T> = BTreeMap<String, T>;

#[derive(Default)]
struct Dependencies {
    php: Option<PhpIndex>,
    services: Option<Arc<ServiceIndex>>,
    routes: Option<Arc<swlsp_symfony::RouteIndexer>>,
}

/// Twig templates, blocks, macros, references and extension callables of
/// one workspace.
pub struct TwigIndexer {
    files: DataIndexer<TwigFile>,
    blocks: DataIndexer<TwigBlock>,
    block_hashes: DataIndexer<TwigBlockHash>,
    functions: DataIndexer<TwigFunction>,
    filters: DataIndexer<TwigFilter>,
    tests: DataIndexer<TwigTest>,
    operators: DataIndexer<TwigOperator>,
    tags: DataIndexer<TwigTag>,
    macros: DataIndexer<MacroCatalog>,
    macro_usages: DataIndexer<MacroUsageRecord>,
    template_variables: DataIndexer<TemplateVariableCatalog>,
    globals: DataIndexer<GlobalCatalog>,
    template_references: DataIndexer<TemplateReferenceCatalog>,
    constant_usages: DataIndexer<ConstantUsageCatalog>,
    php_usages: DataIndexer<PhpUsageCatalog>,
    extension_usages: DataIndexer<ExtensionUsageCatalog>,
    dependencies: RwLock<Dependencies>,
    global_paths: Arc<RwLock<HashSet<String>>>,
}

impl std::fmt::Debug for TwigIndexer {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("TwigIndexer").finish_non_exhaustive()
    }
}

/// Records extracted from one Twig template.
struct PreparedTemplate {
    file: TwigFile,
    blocks: Records<TwigBlock>,
    block_hashes: Records<TwigBlockHash>,
    macros: Records<MacroCatalog>,
    variables: Records<TemplateVariableCatalog>,
    macro_usages: Records<MacroUsageRecord>,
    references: Records<TemplateReferenceCatalog>,
    constants: Records<ConstantUsageCatalog>,
    extension_usages: Records<ExtensionUsageCatalog>,
    php_usages: Records<PhpUsageCatalog>,
    symbols: Vec<WorkspaceSymbol>,
}

/// Records extracted from one PHP file.
struct PreparedPhp {
    functions: Records<TwigFunction>,
    filters: Records<TwigFilter>,
    tests: Records<TwigTest>,
    operators: Records<TwigOperator>,
    tags: Records<TwigTag>,
    /// `None` when the file cannot declare globals.
    globals: Option<Vec<Global>>,
    references: Records<TemplateReferenceCatalog>,
    symbols: Vec<WorkspaceSymbol>,
}

enum PreparedFile {
    ExcludedTemplate,
    Template(Box<PreparedTemplate>),
    Php(Box<PreparedPhp>),
    /// `None` when the file can declare neither globals nor access control.
    Yaml(Option<YamlConfiguration>),
    Asset(Records<TwigFile>),
}

/// The key of the includes whose template name is built at runtime.
const DYNAMIC_INCLUDE_KEY: &str = "*dynamic-include*";

/// What a YAML configuration file contributes.
struct YamlConfiguration {
    globals: Vec<Global>,
    access_control: Vec<crate::security::AccessRule>,
}

fn repository<T>(
    dir: &Path,
    file: &str,
    namespace: &str,
    store: Option<&Store>,
) -> Result<DataIndexer<T>>
where
    T: Serialize + DeserializeOwned + Send + Sync + 'static,
{
    DataIndexer::repository(dir.join(file), namespace, store)
}

fn join_errors(results: impl IntoIterator<Item = Result<()>>) -> Result<()> {
    match Error::join(results.into_iter().filter_map(Result::err)) {
        Some(error) => Err(error),
        None => Ok(()),
    }
}

impl TwigIndexer {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let dir = config_dir.as_ref();
        let indexer = TwigIndexer {
            files: repository(dir, "twig_file.index", "twig.files", store)?,
            blocks: repository(dir, "twig_block.index", "twig.blocks", store)?,
            block_hashes: repository(dir, "twig_block_hash.index", "twig.block_hashes", store)?,
            functions: repository(dir, "twig_function.index", "twig.functions", store)?,
            filters: repository(dir, "twig_filter.index", "twig.filters", store)?,
            tests: repository(dir, "twig_test.index", "twig.tests", store)?,
            operators: repository(dir, "twig_operator.index", "twig.operators", store)?,
            tags: repository(dir, "twig_tag.index", "twig.tags", store)?,
            macros: repository(dir, "twig_macro.index", "twig.macros", store)?,
            macro_usages: repository(dir, "twig_macro.index", "twig.macro_usages", store)?,
            template_variables: repository(
                dir,
                "twig_template_variable.index",
                "twig.template_variables",
                store,
            )?,
            globals: repository(dir, "twig_global.index", "twig.globals", store)?,
            template_references: repository(
                dir,
                "twig_template_reference.index",
                "twig.template_references",
                store,
            )?,
            constant_usages: repository(
                dir,
                "twig_constant_usage.index",
                "twig.constant_usages",
                store,
            )?,
            php_usages: repository(dir, "twig_php_usage.index", "twig.php_usages", store)?,
            extension_usages: repository(
                dir,
                "twig_extension_usage.index",
                "twig.extension_usages",
                store,
            )?,
            dependencies: RwLock::default(),
            global_paths: Arc::default(),
        };
        let paths = indexer.globals.get_all_file_paths()?;
        *write(&indexer.global_paths) = paths.into_iter().collect();
        Ok(indexer)
    }

    /// Wires the PHP and service indexes used for globals, constant and
    /// PHP usage resolution.
    pub fn set_dependencies(&self, php: Option<PhpIndex>, services: Option<Arc<ServiceIndex>>) {
        let mut dependencies = write(&self.dependencies);
        dependencies.php = php;
        dependencies.services = services;
    }

    /// Wires the route index `app.user` is typed through: a route whose
    /// access control requires a user renders its templates with one.
    pub fn set_route_index(&self, routes: Option<Arc<swlsp_symfony::RouteIndexer>>) {
        write(&self.dependencies).routes = routes;
    }

    /// Whether every route of the controller method `controller`
    /// (`Class::method`) requires a user by `security.access_control`.
    /// `false` for a method without routes.
    pub fn routes_require_user(&self, controller: &str) -> bool {
        let Some(routes) = read(&self.dependencies).routes.clone() else {
            return false;
        };
        let Ok(routes) = routes.get_routes_view() else {
            return false;
        };
        let rules = self.access_control();
        if rules.is_empty() {
            return false;
        }
        let controller = controller.trim_start_matches('\\');
        let mut found = false;
        for route in routes.iter() {
            if !swlsp_textutil::eq_ignore_case(
                route.controller.trim_start_matches('\\'),
                controller,
            ) {
                continue;
            }
            found = true;
            if !crate::security::route_requires_user(&rules, &route.name, &route.path) {
                return false;
            }
        }
        found
    }

    /// The `security.access_control` rules of the workspace, in file and
    /// declaration order.
    pub fn access_control(&self) -> Vec<crate::security::AccessRule> {
        let mut catalogs = self
            .globals
            .get_values("access_control")
            .unwrap_or_default();
        catalogs.sort_by(|left, right| left.file.cmp(&right.file));
        catalogs
            .into_iter()
            .flat_map(|catalog| catalog.access_control)
            .collect()
    }

    pub(crate) fn php_index(&self) -> Option<PhpIndex> {
        read(&self.dependencies).php.clone()
    }

    fn service_index(&self) -> Option<Arc<ServiceIndex>> {
        read(&self.dependencies).services.clone()
    }

    /// Advanced after every committed change to the indexed templates,
    /// blocks, callables, references and usages, and after every change of
    /// the PHP and service indexes globals and PHP usages resolve through;
    /// inspections declare it as an input (`ProblemAnalyzer::input_revision`).
    pub fn revision(&self) -> u64 {
        let own = [
            self.files.revision(),
            self.blocks.revision(),
            self.block_hashes.revision(),
            self.functions.revision(),
            self.filters.revision(),
            self.tests.revision(),
            self.operators.revision(),
            self.tags.revision(),
            self.macros.revision(),
            self.macro_usages.revision(),
            self.template_variables.revision(),
            self.globals.revision(),
            self.template_references.revision(),
            self.constant_usages.revision(),
            self.php_usages.revision(),
            self.extension_usages.revision(),
        ]
        .iter()
        .sum::<u64>();
        let dependencies = read(&self.dependencies);
        own + dependencies
            .php
            .as_ref()
            .map_or(0, |php| php.revision() + php.analysis_revision())
            + dependencies
                .services
                .as_ref()
                .map_or(0, |services| services.revision())
            + dependencies
                .routes
                .as_ref()
                .map_or(0, |routes| routes.revision())
    }

    // --- preparation ---

    fn prepare_file(&self, file: &ParsedFile) -> Option<PreparedFile> {
        match file.extension() {
            ".twig" => Some(self.prepare_twig(file)),
            ".php" => Some(PreparedFile::Php(Box::new(self.prepare_php(file)))),
            ".yaml" | ".yml" => Some(PreparedFile::Yaml(prepare_yaml_configuration(file))),
            _ => prepare_template_asset(file).map(PreparedFile::Asset),
        }
    }

    fn prepare_twig(&self, parsed: &ParsedFile) -> PreparedFile {
        let path = parsed.path();
        if is_excluded_twig_path(path) {
            return PreparedFile::ExcludedTemplate;
        }
        let tree = parsed.syntax_tree();
        let file = parse_twig_tree(path, tree, parsed.line_index());
        let names = template_names(path);
        let mut prepared = PreparedTemplate {
            file,
            blocks: Records::new(),
            block_hashes: Records::new(),
            macros: Records::new(),
            variables: Records::new(),
            macro_usages: Records::new(),
            references: Records::new(),
            constants: Records::new(),
            extension_usages: Records::new(),
            php_usages: Records::new(),
            symbols: Vec::new(),
        };
        let versioned = to_slash(path).contains("/Resources/views/");
        let relative = convert_to_relative_path(path);
        for block in prepared.file.blocks.values() {
            prepared
                .blocks
                .entry(block.name.clone())
                .or_insert_with(|| block.clone());
            if versioned {
                prepared.block_hashes.insert(
                    block.name.clone(),
                    TwigBlockHash {
                        name: block.name.clone(),
                        relative_path: relative.clone(),
                        absolute_path: path.to_owned(),
                        bundle_name: prepared.file.bundle_name.clone(),
                        hash: block.hash.clone(),
                        text: block.text.clone(),
                        line: block.line,
                        deprecation: block.deprecation.clone(),
                        has_versioning_comment: block.has_versioning_comment,
                    },
                );
            }
        }
        let Some(tree) = tree else {
            prepared.symbols = template_workspace_symbols(parsed, &prepared.file, &[]);
            return PreparedFile::Template(Box::new(prepared));
        };
        let root = tree.root();

        let macros = macros_in_document(path, &root);
        if !macros.is_empty() {
            for template in &names {
                prepared.macros.insert(
                    normalize_macro_template(template),
                    MacroCatalog {
                        template: template.clone(),
                        file_path: path.to_owned(),
                        macros: macros.clone(),
                    },
                );
            }
        }

        let variables = template_input_variables_in_document(path, &root);
        let dependencies = template_dependencies_in_document(&root);
        for template in &names {
            prepared.variables.insert(
                normalize_macro_template(template),
                TemplateVariableCatalog {
                    template: template.clone(),
                    file_path: path.to_owned(),
                    variables: variables.clone(),
                    dependencies: dependencies.clone(),
                },
            );
        }

        for reference in macro_references_in_document(path, &root) {
            if reference.role != MacroReferenceRole::Usage {
                continue;
            }
            for template in &reference.templates {
                let record = prepared
                    .macro_usages
                    .entry(macro_usage_key(template, &reference.name))
                    .or_insert_with(|| MacroUsageRecord {
                        template: template.clone(),
                        name: reference.name.clone(),
                        file_path: path.to_owned(),
                        usages: Vec::new(),
                    });
                record.usages.push(MacroUsage {
                    template: template.clone(),
                    name: reference.name.clone(),
                    file_path: path.to_owned(),
                    range: reference.range,
                });
            }
        }

        prepared.references =
            template_reference_records(path, twig_template_references(path, &root));
        let dynamic = crate::context::dynamic_include_prefixes(&root);
        if !dynamic.is_empty() {
            prepared.references.insert(
                DYNAMIC_INCLUDE_KEY.to_owned(),
                TemplateReferenceCatalog {
                    file_path: path.to_owned(),
                    references: dynamic
                        .into_iter()
                        .map(|(prefix, range)| TemplateReference {
                            template: format!("{prefix}*"),
                            file_path: path.to_owned(),
                            range,
                            kind: crate::TemplateReferenceKind::Include,
                        })
                        .collect(),
                },
            );
        }
        let php = self.php_index();
        let resolver = PhpAccessResolver::new(php.as_ref(), Some(self)).for_document(&root);
        for reference in constant_references_in_document(path, &root, &resolver) {
            let key = constant_reference_key(&reference);
            if key.is_empty() {
                continue;
            }
            prepared
                .constants
                .entry(key.clone())
                .or_insert_with(|| ConstantUsageCatalog {
                    key,
                    references: Vec::new(),
                })
                .references
                .push(reference);
        }
        for usage in extension_usages_in_document(path, &root) {
            let key = extension_usage_key(usage.kind, &usage.name);
            if key.is_empty() {
                continue;
            }
            prepared
                .extension_usages
                .entry(key.clone())
                .or_insert_with(|| ExtensionUsageCatalog {
                    key,
                    usages: Vec::new(),
                })
                .usages
                .push(usage);
        }
        for reference in php_usage_references_in_document(path, &root, &resolver) {
            let key = php_usage_reference_key(&reference);
            if key.is_empty() {
                continue;
            }
            prepared
                .php_usages
                .entry(key.clone())
                .or_insert_with(|| PhpUsageCatalog {
                    key,
                    references: Vec::new(),
                })
                .references
                .push(reference);
        }
        prepared.symbols = template_workspace_symbols(parsed, &prepared.file, &macros);
        PreparedFile::Template(Box::new(prepared))
    }

    fn prepare_php(&self, file: &ParsedFile) -> PreparedPhp {
        let path = file.path();
        let content = file.source();
        let tree = file.syntax_tree();
        let root = tree.map(swlsp_syntax::Tree::root);
        let mut prepared = PreparedPhp {
            functions: Records::new(),
            filters: Records::new(),
            tests: Records::new(),
            operators: Records::new(),
            tags: Records::new(),
            globals: None,
            references: Records::new(),
            symbols: Vec::new(),
        };
        let Some(root) = root else {
            return prepared;
        };
        if is_twig_callable_definition_candidate(content) {
            let callables = parse_twig_extension_tree(path, &root, content, file.line_index());
            prepared.symbols =
                extension_workspace_symbols(&callables.functions, &callables.filters);
            for function in callables.functions {
                prepared.functions.insert(function.name.clone(), function);
            }
            for filter in callables.filters {
                prepared.filters.insert(filter.name.clone(), filter);
            }
            for test in callables.tests {
                prepared.tests.insert(test.name.clone(), test);
            }
        }
        for operator in parse_twig_operators(path, &root, content) {
            prepared.operators.insert(operator.name.clone(), operator);
        }
        if is_twig_token_parser_candidate(content) {
            for tag in parse_twig_token_parsers(path, &root, content, file.line_index()) {
                prepared.tags.insert(tag.name.clone(), tag);
            }
        }
        let render_parameters = crate::global::may_set_render_parameters(content);
        if content.contains("getGlobals") || render_parameters {
            let document = self.php_index().map(|php| php.analyze_parsed_file(file));
            let mut globals = Vec::new();
            if content.contains("getGlobals") {
                globals.extend(globals_in_php_extension(path, &root, document.as_deref()));
            }
            if render_parameters {
                globals.extend(crate::global::globals_in_render_subscriber(
                    path,
                    &root,
                    document.as_deref(),
                ));
            }
            prepared.globals = Some(globals);
        }
        prepared.references = template_reference_records(
            path,
            crate::reference::php_template_references_uncached(path, &root),
        );
        prepared
    }

    // --- persistence ---

    fn persist(&self, file: &ParsedFile, prepared: Option<&PreparedFile>) -> Result<()> {
        let path = file.path();
        let mutation = file.mutation();
        let Some(prepared) = prepared else {
            return Ok(());
        };
        match prepared {
            PreparedFile::ExcludedTemplate => join_errors([
                save(&self.files, mutation, path, &Records::new()),
                save(&self.blocks, mutation, path, &Records::new()),
                save(&self.block_hashes, mutation, path, &Records::new()),
                save(&self.macros, mutation, path, &Records::new()),
                save(&self.macro_usages, mutation, path, &Records::new()),
                save(&self.template_variables, mutation, path, &Records::new()),
                save(&self.template_references, mutation, path, &Records::new()),
                save(&self.constant_usages, mutation, path, &Records::new()),
                save(&self.php_usages, mutation, path, &Records::new()),
                save(&self.extension_usages, mutation, path, &Records::new()),
            ]),
            PreparedFile::Template(template) => {
                let files: Records<TwigFile> = template_names(path)
                    .into_iter()
                    .map(|name| (name, template.file.clone()))
                    .collect();
                save(&self.files, mutation, path, &files)?;
                save(&self.blocks, mutation, path, &template.blocks)?;
                save(&self.block_hashes, mutation, path, &template.block_hashes)?;
                save(&self.macros, mutation, path, &template.macros)?;
                save(
                    &self.template_variables,
                    mutation,
                    path,
                    &template.variables,
                )?;
                save(&self.macro_usages, mutation, path, &template.macro_usages)?;
                save(
                    &self.template_references,
                    mutation,
                    path,
                    &template.references,
                )?;
                save(&self.constant_usages, mutation, path, &template.constants)?;
                save(
                    &self.extension_usages,
                    mutation,
                    path,
                    &template.extension_usages,
                )?;
                save(&self.php_usages, mutation, path, &template.php_usages)?;
                file.add_workspace_symbols(template.symbols.iter().cloned());
                Ok(())
            }
            PreparedFile::Php(php) => {
                save(&self.functions, mutation, path, &php.functions)?;
                save(&self.filters, mutation, path, &php.filters)?;
                save(&self.tests, mutation, path, &php.tests)?;
                save(&self.operators, mutation, path, &php.operators)?;
                save(&self.tags, mutation, path, &php.tags)?;
                file.add_workspace_symbols(php.symbols.iter().cloned());
                self.save_globals_or_clear(file, php.globals.as_deref(), &[])?;
                save(&self.template_references, mutation, path, &php.references)
            }
            PreparedFile::Yaml(configuration) => match configuration {
                Some(configuration) => self.save_globals_or_clear(
                    file,
                    Some(&configuration.globals),
                    &configuration.access_control,
                ),
                None => self.save_globals_or_clear(file, None, &[]),
            },
            PreparedFile::Asset(names) => save(&self.files, mutation, path, names),
        }
    }

    fn save_globals_or_clear(
        &self,
        file: &ParsedFile,
        globals: Option<&[Global]>,
        access_control: &[crate::security::AccessRule],
    ) -> Result<()> {
        match globals {
            Some(globals) => self.save_globals(file, globals, access_control),
            None if self.has_global_path(file.path()) => self.save_globals(file, &[], &[]),
            None => Ok(()),
        }
    }

    fn save_globals(
        &self,
        file: &ParsedFile,
        globals: &[Global],
        access_control: &[crate::security::AccessRule],
    ) -> Result<()> {
        let path = file.path();
        let mut records = Records::new();
        if !access_control.is_empty() {
            records.insert(
                "access_control".to_owned(),
                GlobalCatalog {
                    file: path.to_owned(),
                    globals: Vec::new(),
                    access_control: access_control.to_vec(),
                },
            );
        }
        if !globals.is_empty() {
            records.insert(
                "globals".to_owned(),
                GlobalCatalog {
                    file: path.to_owned(),
                    globals: globals.to_vec(),
                    access_control: Vec::new(),
                },
            );
        }
        save(&self.globals, file.mutation(), path, &records)?;
        let present = !globals.is_empty() || !access_control.is_empty();
        let paths = self.global_paths.clone();
        let owned = path.to_owned();
        let publish = move || {
            let mut paths = write(&paths);
            if present {
                paths.insert(owned);
            } else {
                paths.remove(&owned);
            }
        };
        match file.mutation() {
            Some(mutation) => mutation.after_commit(publish),
            None => {
                publish();
                Ok(())
            }
        }
    }

    fn has_global_path(&self, path: &str) -> bool {
        read(&self.global_paths).contains(path)
    }

    fn remove(&self, paths: &[String], mutation: Option<&Mutation>) -> Result<()> {
        join_errors([
            self.files.batch_delete_by_file_paths_in(mutation, paths),
            self.blocks.batch_delete_by_file_paths_in(mutation, paths),
            self.block_hashes
                .batch_delete_by_file_paths_in(mutation, paths),
            self.functions
                .batch_delete_by_file_paths_in(mutation, paths),
            self.filters.batch_delete_by_file_paths_in(mutation, paths),
            self.tests.batch_delete_by_file_paths_in(mutation, paths),
            self.operators
                .batch_delete_by_file_paths_in(mutation, paths),
            self.tags.batch_delete_by_file_paths_in(mutation, paths),
            self.macros.batch_delete_by_file_paths_in(mutation, paths),
            self.macro_usages
                .batch_delete_by_file_paths_in(mutation, paths),
            self.template_variables
                .batch_delete_by_file_paths_in(mutation, paths),
            self.globals.batch_delete_by_file_paths_in(mutation, paths),
            self.template_references
                .batch_delete_by_file_paths_in(mutation, paths),
            self.constant_usages
                .batch_delete_by_file_paths_in(mutation, paths),
            self.php_usages
                .batch_delete_by_file_paths_in(mutation, paths),
            self.extension_usages
                .batch_delete_by_file_paths_in(mutation, paths),
        ])?;
        let global_paths = self.global_paths.clone();
        let removed = paths.to_vec();
        let publish = move || {
            let mut global_paths = write(&global_paths);
            for path in &removed {
                global_paths.remove(path);
            }
        };
        match mutation {
            Some(mutation) => mutation.after_commit(publish),
            None => {
                publish();
                Ok(())
            }
        }
    }

    fn clear_all(&self, mutation: Option<&Mutation>) -> Result<()> {
        join_errors([
            self.blocks.clear_in(mutation),
            self.block_hashes.clear_in(mutation),
            self.files.clear_in(mutation),
            self.functions.clear_in(mutation),
            self.filters.clear_in(mutation),
            self.tests.clear_in(mutation),
            self.operators.clear_in(mutation),
            self.tags.clear_in(mutation),
            self.macros.clear_in(mutation),
            self.macro_usages.clear_in(mutation),
            self.template_variables.clear_in(mutation),
            self.globals.clear_in(mutation),
            self.template_references.clear_in(mutation),
            self.constant_usages.clear_in(mutation),
            self.php_usages.clear_in(mutation),
            self.extension_usages.clear_in(mutation),
        ])?;
        let global_paths = self.global_paths.clone();
        let publish = move || write(&global_paths).clear();
        match mutation {
            Some(mutation) => mutation.after_commit(publish),
            None => {
                publish();
                Ok(())
            }
        }
    }

    // --- queries ---

    pub fn get_constant_references(
        &self,
        target: &ConstantReference,
    ) -> Result<Vec<ConstantReference>> {
        let key = constant_reference_key(target);
        if key.is_empty() {
            return Ok(Vec::new());
        }
        let references = self
            .constant_usages
            .get_values(&key)?
            .into_iter()
            .flat_map(|catalog| catalog.references)
            .collect();
        Ok(unique_constant_references(references))
    }

    pub fn get_php_usage_references(
        &self,
        target: &PhpUsageReference,
    ) -> Result<Vec<PhpUsageReference>> {
        let key = php_usage_reference_key(target);
        if key.is_empty() {
            return Ok(Vec::new());
        }
        let references = self
            .php_usages
            .get_values(&key)?
            .into_iter()
            .flat_map(|catalog| catalog.references)
            .collect();
        Ok(unique_php_usage_references(references))
    }

    pub fn get_extension_usages(
        &self,
        kind: ExtensionUsageKind,
        name: &str,
    ) -> Result<Vec<ExtensionUsage>> {
        let key = extension_usage_key(kind, name);
        if key.is_empty() {
            return Ok(Vec::new());
        }
        let usages = self
            .extension_usages
            .get_values(&key)?
            .into_iter()
            .flat_map(|catalog| catalog.usages)
            .collect();
        Ok(unique_extension_usages(usages))
    }

    /// Registered Twig callbacks resolved at query time. This complements
    /// the persisted usage catalog when a template was indexed before its
    /// extension declaration.
    pub fn get_extension_usages_for_php_symbol(
        &self,
        php: &PhpIndex,
        symbol: &swlsp_php_semantic::Symbol,
    ) -> Result<Vec<ExtensionUsage>> {
        if symbol.kind != swlsp_php_semantic::SymbolKind::Method {
            return Ok(Vec::new());
        }
        let resolver = PhpAccessResolver::new(Some(php), Some(self));
        let mut callbacks: Vec<(ExtensionUsageKind, String, String, String)> = Vec::new();
        for function in self.get_all_twig_functions()? {
            callbacks.push((
                ExtensionUsageKind::Function,
                function.name,
                function.file_path,
                function.method,
            ));
        }
        for filter in self.get_all_twig_filters()? {
            callbacks.push((
                ExtensionUsageKind::Filter,
                filter.name,
                filter.file_path,
                filter.method,
            ));
        }
        for test in self.get_all_twig_tests()? {
            callbacks.push((
                ExtensionUsageKind::Test,
                test.name,
                test.file_path,
                test.method,
            ));
        }
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for (kind, name, path, method) in callbacks {
            let key = extension_usage_key(kind, &name);
            if key.is_empty() || method.is_empty() {
                continue;
            }
            if !resolver
                .callback_method_symbols(&path, &method)
                .iter()
                .any(|candidate| candidate.id == symbol.id)
            {
                continue;
            }
            if !seen.insert(key) {
                continue;
            }
            result.extend(self.get_extension_usages(kind, &name)?);
        }
        Ok(unique_extension_usages(result))
    }

    pub fn get_template_references<S: AsRef<str>>(
        &self,
        templates: &[S],
    ) -> Result<Vec<TemplateReference>> {
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for template in templates {
            for catalog in self
                .template_references
                .get_values(&normalize_template_reference(template.as_ref()))?
            {
                for reference in catalog.references {
                    if seen.insert((
                        reference.file_path.clone(),
                        reference.range,
                        reference.template.clone(),
                    )) {
                        result.push(reference);
                    }
                }
            }
        }
        result.sort_by(|left, right| {
            left.file_path
                .cmp(&right.file_path)
                .then(left.range.start().cmp(&right.range.start()))
        });
        Ok(result)
    }

    /// The literal starts of the template names built at runtime that some
    /// template includes: `'@Storefront/element/cms-element-' ~ type`.
    pub fn dynamic_include_prefixes(&self) -> Vec<String> {
        let mut prefixes: Vec<String> = self
            .template_references
            .get_values(DYNAMIC_INCLUDE_KEY)
            .unwrap_or_default()
            .into_iter()
            .flat_map(|catalog| catalog.references)
            .map(|reference| reference.template.trim_end_matches('*').to_owned())
            .collect();
        prefixes.sort();
        prefixes.dedup();
        prefixes
    }

    pub fn get_template_references_by_path(
        &self,
        file_path: &str,
    ) -> Result<Vec<TemplateReference>> {
        let references = self
            .template_references
            .get_values_by_path(file_path)?
            .into_iter()
            .flat_map(|catalog| catalog.references)
            // An include of a name built at runtime names no template.
            .filter(|reference| !reference.template.ends_with('*'))
            .collect();
        Ok(unique_template_references(references))
    }

    pub fn get_all_template_files(&self) -> Result<Vec<String>> {
        self.files.get_all_keys()
    }

    /// The cheap availability question used while presenting an extends
    /// action; full candidate materialization stays deferred.
    pub fn has_other_template_file(&self, current_path: &str) -> Result<bool> {
        let names = template_names(current_path);
        let names: Vec<&str> = names.iter().map(String::as_str).collect();
        self.files.has_any_key_except_fold(&names)
    }

    pub fn get_all_twig_functions(&self) -> Result<Vec<TwigFunction>> {
        self.functions.get_all_values()
    }

    pub fn get_twig_function(&self, name: &str) -> Result<Vec<TwigFunction>> {
        self.functions.get_values(name)
    }

    pub fn twig_function_deprecation(&self, name: &str) -> Result<Option<String>> {
        Ok(all_deprecated(self.get_twig_function(name)?.iter().map(
            |function| (function.deprecated, &function.deprecation),
        )))
    }

    pub fn get_twig_filter(&self, name: &str) -> Result<Vec<TwigFilter>> {
        self.filters.get_values(name)
    }

    pub fn twig_filter_deprecation(&self, name: &str) -> Result<Option<String>> {
        Ok(all_deprecated(
            self.get_twig_filter(name)?
                .iter()
                .map(|filter| (filter.deprecated, &filter.deprecation)),
        ))
    }

    /// Every filter plus Twig's built-in `raw`.
    pub fn get_all_twig_filters(&self) -> Result<Vec<TwigFilter>> {
        let mut values = self.filters.get_all_values()?;
        values.push(TwigFilter {
            name: "raw".to_owned(),
            usage: "raw()".to_owned(),
            ..TwigFilter::default()
        });
        Ok(values)
    }

    pub fn get_twig_test(&self, name: &str) -> Result<Vec<TwigTest>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        self.tests.get_values(name)
    }

    pub fn get_all_twig_tests(&self) -> Result<Vec<TwigTest>> {
        self.tests.get_all_values()
    }

    pub fn twig_test_deprecation(&self, name: &str) -> Result<Option<String>> {
        Ok(all_deprecated(
            self.get_twig_test(name)?
                .iter()
                .map(|test| (test.deprecated, &test.deprecation)),
        ))
    }

    pub fn get_twig_operator(&self, name: &str) -> Result<Vec<TwigOperator>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        self.operators.get_values(name)
    }

    pub fn get_all_twig_operators(&self) -> Result<Vec<TwigOperator>> {
        self.operators.get_all_values()
    }

    pub fn get_twig_tag(&self, name: &str) -> Result<Vec<TwigTag>> {
        if name.is_empty() {
            return Ok(Vec::new());
        }
        self.tags.get_values(name)
    }

    pub fn get_all_twig_tags(&self) -> Result<Vec<TwigTag>> {
        self.tags.get_all_values()
    }

    /// Resolves an opening tag or its conventional `end`-prefixed closing
    /// form. Exact declarations win over end-tag normalization. Returns the
    /// tags and the name they were found under.
    pub fn resolve_twig_tag(&self, name: &str) -> Result<(Vec<TwigTag>, String)> {
        let tags = self.get_twig_tag(name)?;
        if !tags.is_empty() {
            return Ok((tags, name.to_owned()));
        }
        let bytes = name.as_bytes();
        if bytes.len() <= 3 || !bytes[..3].eq_ignore_ascii_case(b"end") {
            return Ok((Vec::new(), name.to_owned()));
        }
        let base = &name[3..];
        Ok((self.get_twig_tag(base)?, base.to_owned()))
    }

    pub fn twig_tag_deprecation(&self, name: &str) -> Result<Option<String>> {
        let (tags, _) = self.resolve_twig_tag(name)?;
        Ok(all_deprecated(
            tags.iter().map(|tag| (tag.deprecated, &tag.deprecation)),
        ))
    }

    /// Every global from configuration, extensions and the compiled
    /// container, with service-backed types resolved.
    pub fn get_all_globals(&self) -> Result<Vec<Global>> {
        let mut result: Vec<Global> = self
            .globals
            .get_all_values()?
            .into_iter()
            .flat_map(|catalog| catalog.globals)
            .collect();
        let services = self.service_index();
        if let Some(services) = &services {
            for value in services.get_twig_globals() {
                let mut global = Global {
                    name: value.name,
                    value: value.value,
                    service_id: value.service_id,
                    file: value.path,
                    range: value.range,
                    source: GlobalSource::Container,
                    ..Global::default()
                };
                if global.service_id.is_empty() && !global.value.is_empty() {
                    global.ty = "string".to_owned();
                }
                result.push(global);
            }
        }
        for global in &mut result {
            resolve_global(global, services.as_deref());
        }
        let mut result = unique_globals(result);
        result.sort_by(|left, right| {
            compare_fold(&left.name, &right.name)
                .then(left.source.cmp(&right.source))
                .then_with(|| left.file.cmp(&right.file))
                .then(left.range.start().cmp(&right.range.start()))
                .then_with(|| left.name.cmp(&right.name))
        });
        Ok(result)
    }

    pub fn get_globals(&self, name: &str) -> Result<Vec<Global>> {
        Ok(self
            .get_all_globals()?
            .into_iter()
            .filter(|global| global.name == name)
            .collect())
    }

    pub fn get_macros(&self, template: &str) -> Result<Vec<Macro>> {
        let mut result: Vec<Macro> = self
            .macros
            .get_values(&normalize_macro_template(template))?
            .into_iter()
            .flat_map(|catalog| catalog.macros)
            .collect();
        sort_macros(&mut result);
        Ok(result)
    }

    pub fn find_macro(&self, template: &str, name: &str) -> Result<Vec<Macro>> {
        Ok(self
            .get_macros(template)?
            .into_iter()
            .filter(|definition| swlsp_textutil::eq_ignore_case(&definition.name, name))
            .collect())
    }

    pub fn get_macro_usages(&self, template: &str, name: &str) -> Result<Vec<MacroUsage>> {
        let mut result: Vec<MacroUsage> = self
            .macro_usages
            .get_values(&macro_usage_key(template, name))?
            .into_iter()
            .flat_map(|record| record.usages)
            .collect();
        sort_macro_usages(&mut result);
        Ok(result)
    }

    pub fn get_all_macros(&self) -> Result<Vec<Macro>> {
        let mut seen = HashSet::new();
        let mut result: Vec<Macro> = self
            .macros
            .get_all_values()?
            .into_iter()
            .flat_map(|catalog| catalog.macros)
            .filter(|definition| seen.insert((definition.file_path.clone(), definition.name_range)))
            .collect();
        sort_macros(&mut result);
        Ok(result)
    }

    /// The effective input contract of one or more template names, followed
    /// through extends and context-preserving include/embed edges, with
    /// explicitly supplied child parameters removed.
    pub fn get_template_variables<S: AsRef<str>>(
        &self,
        templates: &[S],
    ) -> Result<Vec<TemplateVariable>> {
        struct Walk<'a> {
            index: &'a TwigIndexer,
            result: Vec<TemplateVariable>,
            seen: HashSet<(String, rowan::TextRange, String)>,
            visiting: HashSet<String>,
            visited: HashSet<String>,
        }
        impl Walk<'_> {
            fn visit(
                &mut self,
                template: &str,
                omitted: &HashSet<String>,
                depth: usize,
            ) -> Result<()> {
                if depth > 32 {
                    return Ok(());
                }
                let template = normalize_macro_template(template);
                if template.is_empty() {
                    return Ok(());
                }
                let mut omitted_names: Vec<&str> = omitted.iter().map(String::as_str).collect();
                omitted_names.sort_unstable();
                let state = format!("{template}\0{}", omitted_names.join("\0"));
                if self.visited.contains(&state) || !self.visiting.insert(state.clone()) {
                    return Ok(());
                }
                let outcome = self.visit_catalogs(&template, omitted, depth);
                self.visiting.remove(&state);
                outcome?;
                self.visited.insert(state);
                Ok(())
            }

            fn visit_catalogs(
                &mut self,
                template: &str,
                omitted: &HashSet<String>,
                depth: usize,
            ) -> Result<()> {
                for catalog in self.index.template_variables.get_values(template)? {
                    for variable in catalog.variables {
                        if omitted.contains(&variable.name) {
                            continue;
                        }
                        if self.seen.insert((
                            variable.file_path.clone(),
                            variable.range,
                            variable.name.clone(),
                        )) {
                            self.result.push(variable);
                        }
                    }
                    for dependency in catalog.dependencies {
                        if !dependency.propagate {
                            continue;
                        }
                        let mut child = omitted.clone();
                        child.extend(dependency.provided.iter().cloned());
                        self.visit(&dependency.template, &child, depth + 1)?;
                    }
                }
                Ok(())
            }
        }
        let mut walk = Walk {
            index: self,
            result: Vec::new(),
            seen: HashSet::new(),
            visiting: HashSet::new(),
            visited: HashSet::new(),
        };
        for template in templates {
            walk.visit(template.as_ref(), &HashSet::new(), 0)?;
        }
        let mut result = walk.result;
        result.sort_by(|left, right| {
            if left.name != right.name {
                return compare_fold(&left.name, &right.name)
                    .then_with(|| left.name.cmp(&right.name));
            }
            left.file_path
                .cmp(&right.file_path)
                .then(left.range.start().cmp(&right.range.start()))
        });
        Ok(result)
    }

    pub fn find_template_variable<S: AsRef<str>>(
        &self,
        name: &str,
        templates: &[S],
    ) -> Result<Vec<TemplateVariable>> {
        Ok(self
            .get_template_variables(templates)?
            .into_iter()
            .filter(|variable| variable.name == name)
            .collect())
    }

    pub fn get_twig_files_by_rel_path(&self, rel_path: &str) -> Result<Vec<TwigFile>> {
        self.files.get_values(rel_path)
    }

    /// Resolves a Twig template using the enclosing tag's lookup semantics,
    /// including TemplateFinder's fallback to the original name.
    pub fn get_twig_files_for_reference(
        &self,
        node: Option<&SyntaxNode>,
        template: &str,
    ) -> Result<Vec<TwigFile>> {
        let name = twig_template_lookup_name(node, template);
        if name != template {
            let files = self.get_twig_files_by_rel_path(&name)?;
            if !files.is_empty() {
                return Ok(files);
            }
        }
        self.get_twig_files_by_rel_path(template)
    }

    pub fn get_twig_file_by_path(&self, path: &str) -> Result<Option<TwigFile>> {
        if path.is_empty() {
            return Ok(None);
        }
        let clean_path = clean(path);
        Ok(self
            .files
            .get_values_by_path(path)?
            .into_iter()
            .find(|file| clean(&file.path) == clean_path))
    }

    pub fn get_twig_block_hashes(&self, block_name: &str) -> Result<Vec<TwigBlockHash>> {
        self.block_hashes.get_values(block_name)
    }

    pub fn get_twig_block_hash_by_path(
        &self,
        block_name: &str,
        relative_path: &str,
    ) -> Result<Option<TwigBlockHash>> {
        Ok(self
            .get_twig_block_hashes(block_name)?
            .into_iter()
            .find(|hash| hash.relative_path == relative_path))
    }

    pub fn get_all_twig_block_hashes(&self) -> Result<Vec<TwigBlockHash>> {
        self.block_hashes.get_all_values()
    }

    pub(crate) fn get_all_twig_files(&self) -> Result<Vec<TwigFile>> {
        self.files.get_all_values()
    }
}

/// `Some(message)` only when declarations exist and every one is
/// deprecated; the message is the first non-empty one.
fn all_deprecated<'a>(values: impl Iterator<Item = (bool, &'a String)>) -> Option<String> {
    let mut any = false;
    let mut message = String::new();
    for (deprecated, deprecation) in values {
        any = true;
        if !deprecated {
            return None;
        }
        if message.is_empty() {
            message.clone_from(deprecation);
        }
    }
    any.then_some(message)
}

fn resolve_global(global: &mut Global, services: Option<&ServiceIndex>) {
    if !global.ty.is_empty() || global.service_id.is_empty() {
        return;
    }
    let Some(services) = services else {
        return;
    };
    let mut current = global.service_id.clone();
    let mut visited = HashSet::new();
    for _ in 0..16 {
        if current.is_empty() || !visited.insert(swlsp_textutil::lowercase(&current)) {
            break;
        }
        let Ok(Some(service)) = services.get_service_by_id(&current) else {
            break;
        };
        if !service.class.is_empty() {
            global.ty = service
                .class
                .strip_prefix('\\')
                .unwrap_or(&service.class)
                .to_owned();
            break;
        }
        current = service.alias_target;
    }
}

fn save<T>(
    repository: &DataIndexer<T>,
    mutation: Option<&Mutation>,
    path: &str,
    records: &Records<T>,
) -> Result<()>
where
    T: Serialize + DeserializeOwned + Send + Sync + 'static,
{
    repository.batch_save_items_in(mutation, [(path, records.iter())])
}

fn template_reference_records(
    path: &str,
    references: Vec<TemplateReference>,
) -> Records<TemplateReferenceCatalog> {
    let mut records = Records::new();
    for reference in references {
        let key = normalize_template_reference(&reference.template);
        if key.is_empty() {
            continue;
        }
        records
            .entry(key)
            .or_insert_with(|| TemplateReferenceCatalog {
                file_path: path.to_owned(),
                references: Vec::new(),
            })
            .references
            .push(reference);
    }
    records
}

fn prepare_yaml_configuration(file: &ParsedFile) -> Option<YamlConfiguration> {
    let content = file.source();
    let globals = content.contains("twig") && content.contains("globals");
    let access_control = content.contains("security") && content.contains("access_control");
    if !globals && !access_control {
        return None;
    }
    let root = file.syntax_tree()?.root();
    Some(YamlConfiguration {
        globals: if globals {
            globals_in_yaml(file.path(), &root)
        } else {
            Vec::new()
        },
        access_control: if access_control {
            crate::security::access_rules_in_yaml(&root)
        } else {
            Vec::new()
        },
    })
}

/// The names a non-Twig template can be addressed by. Nothing is parsed:
/// the file is data to whatever template includes it.
fn prepare_template_asset(file: &ParsedFile) -> Option<Records<TwigFile>> {
    if !is_template_asset_path(file.path()) {
        return None;
    }
    let twig_file = new_twig_file(file.path());
    Some(
        template_names(file.path())
            .into_iter()
            .map(|name| (name, twig_file.clone()))
            .collect(),
    )
}

fn template_workspace_symbols(
    parsed: &ParsedFile,
    file: &TwigFile,
    macros: &[Macro],
) -> Vec<WorkspaceSymbol> {
    let mut result = Vec::new();
    for name in template_names(&file.path) {
        result.push(WorkspaceSymbol {
            name,
            container_name: "Twig template".to_owned(),
            path: file.path.clone(),
            domain: "twig.template".to_owned(),
            kind: WorkspaceSymbolKind::FILE,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            ..WorkspaceSymbol::default()
        });
    }
    for block in file.blocks.values() {
        result.push(WorkspaceSymbol {
            name: block.name.clone(),
            container_name: "Twig block".to_owned(),
            path: file.path.clone(),
            domain: "twig.block".to_owned(),
            kind: WorkspaceSymbolKind::FIELD,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK_MEMBER,
            range: WorkspaceSymbolRange::from_text(parsed, block.name_range),
            ..WorkspaceSymbol::default()
        });
    }
    for definition in macros {
        result.push(WorkspaceSymbol {
            name: definition.name.clone(),
            container_name: format!("Twig macro · {}", definition.signature()),
            path: definition.file_path.clone(),
            domain: "twig.macro".to_owned(),
            kind: WorkspaceSymbolKind::FUNCTION,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: WorkspaceSymbolRange::from_text(parsed, definition.name_range),
            ..WorkspaceSymbol::default()
        });
    }
    result
}

fn extension_workspace_symbols(
    functions: &[TwigFunction],
    filters: &[TwigFilter],
) -> Vec<WorkspaceSymbol> {
    let callable = |name: &str, kind: &str, method: &str, path: &str, line: i64, domain: &str| {
        WorkspaceSymbol {
            name: name.to_owned(),
            container_name: kind.to_owned(),
            aliases: vec![method.to_owned()],
            path: path.to_owned(),
            domain: domain.to_owned(),
            kind: WorkspaceSymbolKind::FUNCTION,
            priority: WORKSPACE_SYMBOL_PRIORITY_FRAMEWORK,
            range: WorkspaceSymbolRange::at_line(line as i32),
        }
    };
    functions
        .iter()
        .map(|function| {
            callable(
                &function.name,
                "Twig function",
                &function.method,
                &function.file_path,
                function.line,
                "twig.function",
            )
        })
        .chain(filters.iter().map(|filter| {
            callable(
                &filter.name,
                "Twig filter",
                &filter.method,
                &filter.file_path,
                filter.line,
                "twig.filter",
            )
        }))
        .collect()
}

fn read<T>(lock: &RwLock<T>) -> std::sync::RwLockReadGuard<'_, T> {
    lock.read()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

fn write<T>(lock: &RwLock<T>) -> std::sync::RwLockWriteGuard<'_, T> {
    lock.write()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

impl Indexer for TwigIndexer {
    fn id(&self) -> &str {
        "twig.indexer"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let prepared = self.prepare_file(file);
        self.persist(file, prepared.as_ref())
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.remove(paths, None)
    }

    fn close(&self) -> Result<()> {
        join_errors([
            self.blocks.close(),
            self.block_hashes.close(),
            self.files.close(),
            self.functions.close(),
            self.filters.close(),
            self.tests.close(),
            self.operators.close(),
            self.tags.close(),
            self.macros.close(),
            self.macro_usages.close(),
            self.template_variables.close(),
            self.globals.close(),
            self.template_references.close(),
            self.constant_usages.close(),
            self.php_usages.close(),
            self.extension_usages.close(),
        ])
    }

    fn clear(&self) -> Result<()> {
        self.clear_all(None)
    }

    fn as_preparing(&self) -> Option<&dyn PreparingIndexer> {
        Some(self)
    }

    fn as_supplemental_path(&self) -> Option<&dyn SupplementalPathIndexer> {
        Some(self)
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

impl PreparingIndexer for TwigIndexer {
    fn prepare(&self, file: &ParsedFile) -> Result<Option<Prepared>> {
        Ok(self
            .prepare_file(file)
            .map(|prepared| Box::new(prepared) as Prepared))
    }

    fn index_prepared(&self, file: &ParsedFile, prepared: Option<&Prepared>) -> Result<()> {
        self.persist(
            file,
            prepared.and_then(|value| value.downcast_ref::<PreparedFile>()),
        )
    }
}

impl SupplementalPathIndexer for TwigIndexer {
    /// Reopens only storefront dist roots; every other template root is
    /// part of a normal scan.
    fn should_enter_directory(&self, path: &str) -> bool {
        is_storefront_dist_directory(path)
    }

    /// Template files the language registry does not claim (such as the
    /// `.svg` views Symfony's profiler includes), plus Twig templates below
    /// a dist root, which the scanner skips.
    fn should_index_path(&self, path: &str) -> bool {
        if storefront_dist_relative(&to_slash(path)).is_some()
            && ext(path).eq_ignore_ascii_case(".twig")
        {
            return !is_excluded_twig_path(path);
        }
        !swlsp_indexer::is_scanned_path(path) && is_template_asset_path(path)
    }
}

impl TransactionalRemover for TwigIndexer {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.remove(paths, Some(mutation))
    }
}

impl TransactionalClearer for TwigIndexer {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.clear_all(Some(mutation))
    }
}

impl swlsp_indexer::Revisioned for TwigIndexer {
    fn revision(&self) -> u64 {
        TwigIndexer::revision(self)
    }
}
