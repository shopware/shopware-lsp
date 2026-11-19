//! Shopware inspections with quick fixes beyond suggestions: PHP imports,
//! services.xml migration and app scripts.

mod admin_api_contract;
mod app_script;
mod migration_step;
pub(crate) mod routes;
mod services_xml_migration;
mod storefront_js;

#[cfg(test)]
mod tests;

pub use admin_api_contract::new_admin_api_contract;
pub use app_script::{ADD_APP_READ_PERMISSION_FIX_ID, new_app_script};
pub use migration_step::new_shopware_migration_step;
pub use routes::new_shopware_routes;
pub use services_xml_migration::{
    CONVERT_IMPORTED_SERVICES_XML_FIX_ID, CONVERT_SERVICES_XML_FIX_ID, new_services_xml_migration,
};
pub use storefront_js::new_storefront_javascript;
