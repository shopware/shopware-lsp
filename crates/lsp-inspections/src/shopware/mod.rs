//! Shopware inspections: local and semantic PHP rules, PHP imports, DAL
//! entity names, services.xml migration and app scripts.

mod app_script;
mod php_imports;
mod services_xml_migration;
mod simple;

#[cfg(test)]
mod tests;

pub use app_script::{ADD_APP_READ_PERMISSION_FIX_ID, new_app_script};
pub use php_imports::{UNUSED_IMPORT_FIX_ID, new_php_imports};
pub use services_xml_migration::{CONVERT_SERVICES_XML_FIX_ID, new_services_xml_migration};
pub use simple::{new_dal_entity, new_shopware_php_local, new_shopware_php_semantic};
