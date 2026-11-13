//! The workspace's domain indexes, as the domains see them.

use std::sync::Arc;

use swlsp_indexer::{FileScanner, WorkspaceSymbolCatalog};
use swlsp_lsp_phpsemantic::Provider as PhpFeatures;
use swlsp_php::PhpIndex;
use swlsp_shopware::ResolvedVersion;

/// Every domain index and service the providers are built from. Indexes of
/// disabled structural domains (`scss`, `administration`) are `None`.
#[derive(Clone)]
pub struct Services {
    pub root: String,
    pub paths: Arc<FileScanner>,
    pub symbols: Arc<WorkspaceSymbolCatalog>,
    pub php: PhpIndex,
    /// The PHP language features, shared by every request kind and by the
    /// providers of other domains that build on them.
    pub php_features: Arc<PhpFeatures>,
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
    /// The entities a repository search may load in full.
    pub small_entities: Arc<std::collections::BTreeSet<String>>,
}

impl Services {
    /// The PHP index, as the optional dependency most providers take.
    pub fn php(&self) -> Option<PhpIndex> {
        Some(self.php.clone())
    }

    /// The indexed entity-schema catalog shared by scaffolding and snapshot
    /// diagnostics.
    pub fn entity_catalog(&self) -> swlsp_shopware_entityschema::IndexedCatalog {
        swlsp_shopware_entityschema::IndexedCatalog::new(
            self.php.clone(),
            Some(self.entity_schema_sources.clone()),
            Some(self.symbols.clone()),
        )
    }
}
