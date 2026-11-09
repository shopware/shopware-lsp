//! Shopware inspections with quick fixes beyond suggestions: PHP imports,
//! services.xml migration and app scripts.

mod app_script;
mod php_imports;
mod services_xml_migration;

#[cfg(test)]
mod tests;

pub use app_script::{ADD_APP_READ_PERMISSION_FIX_ID, new_app_script};
pub use php_imports::{UNUSED_IMPORT_FIX_ID, new_php_imports};
pub use services_xml_migration::{CONVERT_SERVICES_XML_FIX_ID, new_services_xml_migration};
