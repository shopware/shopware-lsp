//! Shopware analyzers: syntax-local and semantic PHP rules, decoration,
//! Store composer metadata, entity snapshots, DAL entity names and
//! definitions, services.xml deprecation, app scripts and theme icons.
//!
//! The semantic PHP rules share one analyzer and one tree walk
//! (`php_semantic`); `php_conventions`, `php_http` and `php_filesystem` hold
//! its rules by subject.

mod app_script;
mod dal_definition;
mod dal_entity;
mod decoration;
mod entity_snapshot;
mod php_conventions;
mod php_filesystem;
mod php_http;
mod php_local;
mod php_preflight;
mod php_semantic;
mod services_xml;
mod store_composer;
mod theme;
mod util;

#[cfg(test)]
mod tests;

pub use app_script::AppScriptAnalyzer;
pub use dal_definition::DalDefinitionAnalyzer;
pub use dal_entity::DalEntityAnalyzer;
pub use decoration::ShopwareDecorationAnalyzer;
pub use entity_snapshot::EntitySnapshotAnalyzer;
pub use php_local::*;
pub(crate) use php_semantic::same_php_namespace_package;
pub use php_semantic::{
    SHOPWARE_PHP_CONCRETE_DECORATOR_EXTENSION_CODE, SHOPWARE_PHP_DEFAULT_CONTEXT_CODE,
    SHOPWARE_PHP_EMPTY_RESPONSE_CODE, SHOPWARE_PHP_INTERNAL_CLASS_EXTENSION_CODE,
    SHOPWARE_PHP_INTERNAL_FUNCTION_CALL_CODE, SHOPWARE_PHP_INTERNAL_METHOD_CALL_CODE,
    SHOPWARE_PHP_LOCAL_WRITE_CODE, SHOPWARE_PHP_REPOSITORY_IN_LOOP_CODE,
    SHOPWARE_PHP_ROUTE_LISTENER_CODE, SHOPWARE_PHP_SCHEDULED_TASK_INTERVAL_CODE,
    SHOPWARE_PHP_SESSION_CONSTRUCTOR_CODE, SHOPWARE_PHP_SESSION_PAYMENT_HANDLER_CODE,
    SHOPWARE_PHP_SESSION_STORE_API_CODE, SHOPWARE_PHP_SYMFONY_COOKIE_CODE,
    SHOPWARE_PHP_SYSTEM_CONFIG_SALES_CHANNEL_CODE, SHOPWARE_PHP_USER_STORE_TOKEN_CODE,
    ShopwarePhpSemanticAnalyzer,
};
pub(crate) use services_xml::is_local_xml_resource;
pub use services_xml::{
    ROUTES_XML_DEPRECATED_CODE, SERVICES_XML_DEPRECATED_CODE, ServicesXmlDeprecationAnalyzer,
    ServicesXmlDeprecationPayload, ServicesXmlImportAnalyzer, ServicesXmlImportPayload,
};
pub use store_composer::{ShopwareStoreComposerAnalyzer, StoreComplianceResolver};
pub use theme::{IconProvider, ThemeAnalyzer};
