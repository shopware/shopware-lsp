//! Workspace construction (port of Go `internal/app/workspace.go`): opens the
//! shared store, constructs every domain index with its dependencies,
//! registers the domain-enabled indexers with the file scanner and hands the
//! indexes to provider registration.

use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use swlsp_indexer::{FileScanner, Indexer, Store, WorkspaceSymbolCatalog};
use swlsp_lsp::{BoxError, Context, Server, WorkspaceFactory, WorkspaceRuntime};
use swlsp_php::PhpIndex;
use swlsp_shopware::ResolvedVersion;

use crate::cache;
use crate::registrar::{Registrar, Registration};

/// Every domain index and service the providers are built from (Go
/// `workspaceServices`). Indexes of disabled structural domains (`scss`,
/// `administration`) are `None`.
#[derive(Clone)]
pub struct Services {
    pub root: String,
    pub paths: Arc<FileScanner>,
    pub symbols: Arc<WorkspaceSymbolCatalog>,
    pub php: PhpIndex,
    pub services: Arc<swlsp_symfony::ServiceIndex>,
    pub routes: Arc<swlsp_symfony::RouteIndexer>,
    pub route_usage: Arc<swlsp_symfony::RouteUsageIndexer>,
    pub console: Arc<swlsp_console::Index>,
    pub events: Arc<swlsp_event::Index>,
    pub messenger: Arc<swlsp_messenger::Index>,
    pub environment: Arc<swlsp_environment::Index>,
    pub configuration: Arc<swlsp_symfonyconfig::Index>,
    pub serializer: Arc<swlsp_serializer::Index>,
    pub doctrine: Arc<swlsp_doctrine::Index>,
    pub assets: Arc<swlsp_asset::Index>,
    pub styles: Option<Arc<swlsp_style::Index>>,
    pub twig: Arc<swlsp_twig::TwigIndexer>,
    pub twig_versioning: Arc<swlsp_twig::VersioningService>,
    pub twig_components: Arc<swlsp_twigcomponent::Index>,
    pub snippets: Arc<swlsp_snippet::SnippetIndexer>,
    pub translations: Arc<swlsp_translation::Index>,
    pub features: Arc<swlsp_feature::FeatureIndexer>,
    pub system_config: Arc<swlsp_systemconfig::SystemConfigIndexer>,
    pub theme: Arc<swlsp_theme::ThemeConfigIndexer>,
    pub extensions: Arc<swlsp_extension::ExtensionIndexer>,
    pub extension_config: Arc<swlsp_extension::ConfigIndex>,
    pub admin: Option<Arc<swlsp_admin::AdminComponentIndexer>>,
    pub dal: Arc<swlsp_shopware_dal::Index>,
    pub entity_schema_sources: Arc<swlsp_shopware_entityschema::SourceIndex>,
    pub app_scripts: Arc<swlsp_appscript::Index>,
    pub shopware_version: ResolvedVersion,
}

/// The resources of one initialized workspace root.
pub struct Workspace {
    root: String,
    cache_dir: PathBuf,
    scanner: Arc<FileScanner>,
    store: Store,
    indexers: Vec<Arc<dyn Indexer>>,
    services: Services,
    registrations: Vec<Registration>,
    initial_force: bool,
    close_result: Mutex<Option<Result<(), String>>>,
}

/// Resources created so far; closed in reverse order when construction
/// fails half way.
#[derive(Default)]
struct Partial {
    store: Option<Store>,
    scanner: Option<FileScanner>,
    indexers: Vec<Arc<dyn Indexer>>,
}

impl Partial {
    fn push<T: Indexer + 'static>(&mut self, index: T) -> Arc<T> {
        let index = Arc::new(index);
        self.indexers.push(index.clone());
        index
    }

    fn close(self) {
        if let Some(scanner) = &self.scanner {
            let _ = scanner.close();
        }
        close_indexers(&self.indexers);
        if let Some(store) = &self.store {
            let _ = store.close();
        }
    }
}

fn close_indexers(indexers: &[Arc<dyn Indexer>]) -> Vec<String> {
    let mut errors = Vec::new();
    for index in indexers.iter().rev() {
        if let Err(error) = index.close() {
            errors.push(format!("{}: {error}", index.id()));
        }
    }
    errors
}

fn context<E: std::fmt::Display>(message: &str) -> impl FnOnce(E) -> String + '_ {
    move |error| format!("{message}: {error}")
}

impl Workspace {
    /// Builds the workspace for `root` with the cache below the user cache
    /// directory (or `SHOPWARE_LSP_CACHE_DIR`).
    pub fn new(ctx: &Context, root: &str, server: &Server) -> Result<Workspace, String> {
        let cache_base = cache::user_cache_dir().map_err(context("resolve cache directory"))?;
        Self::new_in(ctx, root, server, &cache_base)
    }

    /// [`Workspace::new`] with an explicit cache base directory; tests use it
    /// instead of mutating the process environment.
    pub fn new_in(
        _ctx: &Context,
        root: &str,
        server: &Server,
        cache_base: &Path,
    ) -> Result<Workspace, String> {
        let cache_dir = cache::project_cache_folder_in(cache_base, root)
            .map_err(context("resolve cache directory"))?;
        let mut partial = Partial::default();
        match build(root, &cache_dir, server, &mut partial) {
            Ok(built) => Ok(built),
            Err(error) => {
                partial.close();
                Err(error)
            }
        }
    }

    pub fn root(&self) -> &str {
        &self.root
    }

    pub fn cache_dir(&self) -> &Path {
        &self.cache_dir
    }

    pub fn file_scanner(&self) -> &Arc<FileScanner> {
        &self.scanner
    }

    pub fn store(&self) -> &Store {
        &self.store
    }

    /// The constructed domain indexes.
    pub fn services(&self) -> &Services {
        &self.services
    }

    /// Every constructed indexer in construction order (also those whose
    /// domain is disabled and which the scanner therefore never feeds).
    pub fn indexers(&self) -> &[Arc<dyn Indexer>] {
        &self.indexers
    }

    /// Every provider, inspection, enricher and observer registration in
    /// registration order.
    pub fn registrations(&self) -> &[Registration] {
        &self.registrations
    }

    /// Closes the scanner, the indexers in reverse construction order and
    /// the store. Idempotent; later calls return the first result.
    pub fn close_workspace(&self) -> Result<(), String> {
        let mut result = self
            .close_result
            .lock()
            .unwrap_or_else(|error| error.into_inner());
        if let Some(result) = result.as_ref() {
            return result.clone();
        }
        let mut errors = Vec::new();
        if let Err(error) = self.scanner.close() {
            errors.push(error.to_string());
        }
        errors.extend(close_indexers(&self.indexers));
        if let Err(error) = self.store.close() {
            errors.push(format!("index store: {error}"));
        }
        let closed = if errors.is_empty() {
            Ok(())
        } else {
            Err(errors.join("\n"))
        };
        *result = Some(closed.clone());
        closed
    }
}

impl WorkspaceRuntime for Workspace {
    fn root(&self) -> &str {
        &self.root
    }

    fn scanner(&self) -> Option<Arc<FileScanner>> {
        Some(self.scanner.clone())
    }

    fn initial_force_reindex(&self) -> bool {
        self.initial_force
    }

    fn close(&self) -> Result<(), BoxError> {
        self.close_workspace().map_err(BoxError::from)
    }
}

/// The production workspace factory for [`Server::set_workspace_factory`].
pub fn workspace_factory() -> WorkspaceFactory {
    Arc::new(|ctx: &Context, root: &str, server: &Server| {
        Workspace::new(ctx, root, server)
            .map(|workspace| Box::new(workspace) as Box<dyn WorkspaceRuntime>)
            .map_err(BoxError::from)
    })
}

/// [`workspace_factory`] with an explicit cache base directory.
pub fn workspace_factory_in(cache_base: PathBuf) -> WorkspaceFactory {
    Arc::new(move |ctx: &Context, root: &str, server: &Server| {
        Workspace::new_in(ctx, root, server, &cache_base)
            .map(|workspace| Box::new(workspace) as Box<dyn WorkspaceRuntime>)
            .map_err(BoxError::from)
    })
}

fn build(
    root: &str,
    cache_dir: &Path,
    server: &Server,
    partial: &mut Partial,
) -> Result<Workspace, String> {
    let mut cache_cleared = swlsp_indexer::check_and_migrate_cache(cache_dir)
        .map_err(context("migrate index cache"))?;
    let configuration = server.effective_configuration();
    let configuration_cleared = swlsp_indexer::check_and_migrate_configuration(
        cache_dir,
        &configuration.structural_fingerprint(),
    )
    .map_err(context("migrate structurally configured cache"))?;
    cache_cleared = cache_cleared || configuration_cleared;

    let store = Store::open(cache_dir.join("indexes.db")).map_err(context("create index store"))?;
    partial.store = Some(store.clone());
    let symbols = Arc::new(
        WorkspaceSymbolCatalog::new(&store).map_err(context("create workspace symbol catalog"))?,
    );

    partial.scanner = Some(
        FileScanner::new(root, cache_dir.join("file_scanner.db"), Some(store.clone()))
            .map_err(context("create file scanner"))?,
    );
    {
        let scanner = partial.scanner.as_mut().expect("scanner was just created");
        scanner
            .set_excluded_paths(&configuration.indexing.exclude)
            .map_err(|error| error.to_string())?;
        scanner.set_max_file_size_bytes(configuration.indexing.max_file_size_mib << 20);
        scanner.set_workspace_symbol_catalog(symbols.clone());
    }

    let store_ref = Some(&store);
    let options = server.initialization_options();
    let php = PhpIndex::open_project(
        cache_dir,
        store_ref,
        Path::new(root),
        &options.php_extensions,
        &options.disabled_php_extensions,
    )
    .map_err(context("create PHP index"))?;
    partial.indexers.push(Arc::new(php.clone()));
    php.register_type_extension(Arc::new(swlsp_php_inference::FakerTypes));
    php.register_type_extension(Arc::new(swlsp_shopware::PhpTypeExtension::new()));
    php.register_type_extension(Arc::new(swlsp_event::PhpTypeExtension::new()));
    let project = php.project();
    let shopware_version = swlsp_shopware::VersionResolver::new(
        root,
        project.as_deref(),
        &options.shopware_target_version,
    )
    .resolve()
    .map_err(context("configure Shopware target version"))?;

    let services = partial.push(
        swlsp_symfony::ServiceIndex::new(root, cache_dir, store_ref)
            .map_err(context("create service index"))?,
    );
    services.set_php_index(Some(php.clone()));
    php.register_type_extension(Arc::new(swlsp_symfony::PhpTypeExtension::new(Some(
        services.clone(),
    ))));
    let routes = partial.push(
        swlsp_symfony::RouteIndexer::new_project(root, cache_dir, store_ref)
            .map_err(context("create route index"))?,
    );
    let route_usage = partial.push(
        swlsp_symfony::RouteUsageIndexer::new(cache_dir, store_ref)
            .map_err(context("create route usage index"))?,
    );
    route_usage.set_php_index(Some(php.clone()));
    let console = partial.push(
        swlsp_console::Index::new(cache_dir, store_ref).map_err(context("create Console index"))?,
    );
    let events = partial.push(
        swlsp_event::Index::new(cache_dir, store_ref).map_err(context("create event index"))?,
    );
    events.set_php_index(php.clone());
    let messenger = partial.push(
        swlsp_messenger::Index::new(cache_dir, store_ref)
            .map_err(context("create Messenger index"))?,
    );
    messenger.set_php_index(php.clone());
    let environment = partial.push(
        swlsp_environment::Index::new(cache_dir, store_ref)
            .map_err(context("create environment index"))?,
    );
    let symfony_configuration = partial.push(
        swlsp_symfonyconfig::Index::new(cache_dir, store_ref)
            .map_err(context("create Symfony configuration index"))?,
    );
    let serializer = partial.push(
        swlsp_serializer::Index::new(cache_dir, store_ref)
            .map_err(context("create Serializer index"))?,
    );
    let doctrine = partial.push(
        swlsp_doctrine::Index::new(cache_dir, store_ref)
            .map_err(context("create Doctrine index"))?,
    );
    let assets = partial.push(
        swlsp_asset::Index::new(root, cache_dir, store_ref)
            .map_err(context("create asset index"))?,
    );
    let styles = if configuration.domain_enabled("scss") {
        Some(partial.push(
            swlsp_style::Index::new(cache_dir, store_ref).map_err(context("create style index"))?,
        ))
    } else {
        None
    };
    let twig = partial.push(
        swlsp_twig::TwigIndexer::new(cache_dir, store_ref).map_err(context("create Twig index"))?,
    );
    twig.set_dependencies(Some(php.clone()), Some(services.clone()));
    let twig_components = partial.push(
        swlsp_twigcomponent::Index::new(cache_dir, store_ref)
            .map_err(context("create Twig component index"))?,
    );
    twig_components.set_dependencies(
        Some(php.clone()),
        Some(services.clone()),
        Some(twig.clone()),
    );
    let snippets = partial.push(
        swlsp_snippet::SnippetIndexer::new(cache_dir, store_ref)
            .map_err(context("create snippet index"))?,
    );
    let translations = partial.push(
        swlsp_translation::Index::new(cache_dir, store_ref)
            .map_err(context("create translation index"))?,
    );
    let features = partial.push(
        swlsp_feature::FeatureIndexer::new(cache_dir, store_ref)
            .map_err(context("create feature index"))?,
    );
    let system_config = partial.push(
        swlsp_systemconfig::SystemConfigIndexer::new(cache_dir, store_ref)
            .map_err(context("create system config index"))?,
    );
    let theme = partial.push(
        swlsp_theme::ThemeConfigIndexer::new(cache_dir, store_ref)
            .map_err(context("create theme index"))?,
    );
    let extensions = partial.push(
        swlsp_extension::ExtensionIndexer::new(cache_dir, store_ref)
            .map_err(context("create extension index"))?,
    );
    extensions.set_php_index(Some(php.clone()));
    let extension_config = partial.push(
        swlsp_extension::ConfigIndex::new(cache_dir, store_ref)
            .map_err(context("create extension configuration index"))?,
    );
    let admin = if configuration.domain_enabled("administration") {
        let cache = cache_dir.to_string_lossy();
        Some(
            partial.push(
                swlsp_admin::AdminComponentIndexer::new(&cache, store_ref)
                    .map_err(context("create administration index"))?,
            ),
        )
    } else {
        None
    };
    let dal = partial.push(
        swlsp_shopware_dal::Index::new(cache_dir, store_ref)
            .map_err(context("create Shopware DAL index"))?,
    );
    doctrine.set_dal_index(dal.clone());
    let entity_schema_sources = partial.push(
        swlsp_shopware_entityschema::SourceIndex::new(cache_dir, store_ref)
            .map_err(context("create entity schema source index"))?,
    );
    let app_scripts = partial.push(
        swlsp_appscript::Index::new(cache_dir, store_ref)
            .map_err(context("create App script index"))?,
    );
    let shopware_version_text = if shopware_version.known {
        shopware_version.version.to_string()
    } else {
        String::new()
    };
    let twig_versioning = Arc::new(swlsp_twig::VersioningService::new(
        root,
        Some(twig.clone()),
        &shopware_version_text,
    ));
    if configuration.domain_enabled("symfony.services") {
        partial.push(
            services
                .compiled_container_index(cache_dir, store_ref)
                .map_err(context("create compiled container index"))?,
        );
    }
    if configuration.domain_enabled("symfony.routes") {
        partial.push(
            routes
                .compiled_route_index(root, cache_dir, store_ref)
                .map_err(context("create compiled route index"))?,
        );
    }

    let mut scanner = partial.scanner.take().expect("scanner was created");
    for index in &partial.indexers {
        if configuration.domain_enabled(domain_for_indexer(index.id())) {
            scanner.add_indexer(index.clone());
        }
    }
    let scanner = Arc::new(scanner);

    let services = Services {
        root: root.to_owned(),
        paths: scanner.clone(),
        symbols,
        php,
        services,
        routes,
        route_usage,
        console,
        events,
        messenger,
        environment,
        configuration: symfony_configuration,
        serializer,
        doctrine,
        assets,
        styles,
        twig,
        twig_versioning,
        twig_components,
        snippets,
        translations,
        features,
        system_config,
        theme,
        extensions,
        extension_config,
        admin,
        dal,
        entity_schema_sources,
        app_scripts,
        shopware_version,
    };
    // Registration panics only on invalid inspection definitions (a
    // programming error), which Go's composition also treats as fatal.
    let registrar = Registrar::new(server);
    crate::providers::register_features(&registrar, &services);
    let registrations = registrar.into_log();

    let store = partial.store.take().expect("store was created");
    let indexers = std::mem::take(&mut partial.indexers);
    Ok(Workspace {
        root: root.to_owned(),
        cache_dir: cache_dir.to_path_buf(),
        scanner,
        store,
        indexers,
        services,
        registrations,
        initial_force: cache_cleared,
        close_result: Mutex::new(None),
    })
}

/// The structural domain that gates an indexer (Go `domainForIndexer`).
pub fn domain_for_indexer(id: &str) -> &'static str {
    match id {
        "extension.config" => "shopware",
        "php.index" => "php",
        "symfony.service" | "symfony.compiled_container" => "symfony.services",
        "symfony.route" | "symfony.route_usage" | "symfony.compiled_routes" => "symfony.routes",
        "symfony.console" => "symfony.console",
        "symfony.doctrine" => "symfony.doctrine",
        "symfony.assets" => "symfony.assets",
        "symfony.event" => "symfony.events",
        "symfony.messenger" => "symfony.messenger",
        "symfony.environment" => "symfony.environment",
        "symfony.configuration" => "symfony.configuration",
        "symfony.serializer" => "symfony.serializer",
        "style.classes" => "scss",
        "twig.indexer" => "twig",
        "symfony.twig_components" => "symfony.twigComponents",
        "snippet.indexer" => "shopware.snippets",
        "translation.indexer" => "shopware.translations",
        "feature.indexer" => "shopware.featureFlags",
        "systemconfig.indexer" => "shopware.systemConfig",
        "theme.indexer" => "shopware.theme",
        "extension.indexer" => "shopware.extensions",
        "admin.component.indexer" => "administration",
        "shopware.dal" | "shopware.entity_schema.sources" => "shopware.dal",
        "shopware.app_script" => "shopware.appScripts",
        _ => "shopware",
    }
}
