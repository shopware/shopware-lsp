//! Shopware analyzers: syntax-local and semantic PHP rules, decoration,
//! Store composer metadata, entity snapshots, DAL entity names, services.xml
//! deprecation, app scripts and theme icons.

mod app_script;
mod dal_entity;
mod decoration;
mod entity_snapshot;
mod php_local;
mod php_semantic;
mod services_xml;
mod store_composer;
mod theme;
mod util;

#[cfg(test)]
mod tests;

pub use app_script::AppScriptAnalyzer;
pub use dal_entity::DalEntityAnalyzer;
pub use decoration::ShopwareDecorationAnalyzer;
pub use entity_snapshot::EntitySnapshotAnalyzer;
pub use php_local::*;
pub use php_semantic::{
    SHOPWARE_PHP_CONCRETE_DECORATOR_EXTENSION_CODE, SHOPWARE_PHP_INTERNAL_CLASS_EXTENSION_CODE,
    SHOPWARE_PHP_INTERNAL_FUNCTION_CALL_CODE, SHOPWARE_PHP_INTERNAL_METHOD_CALL_CODE,
    SHOPWARE_PHP_REPOSITORY_IN_LOOP_CODE, SHOPWARE_PHP_SCHEDULED_TASK_INTERVAL_CODE,
    SHOPWARE_PHP_SESSION_CONSTRUCTOR_CODE, SHOPWARE_PHP_SESSION_PAYMENT_HANDLER_CODE,
    SHOPWARE_PHP_SESSION_STORE_API_CODE, SHOPWARE_PHP_USER_STORE_TOKEN_CODE,
    ShopwarePhpSemanticAnalyzer,
};
pub use services_xml::{
    SERVICES_XML_DEPRECATED_CODE, ServicesXmlDeprecationAnalyzer, ServicesXmlDeprecationPayload,
};
pub use store_composer::{ShopwareStoreComposerAnalyzer, StoreComplianceResolver};
pub use theme::{IconProvider, ThemeAnalyzer};
