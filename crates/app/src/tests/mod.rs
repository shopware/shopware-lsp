mod support;

mod cache_test;
mod client_profile_test;
mod doctrine_dbal_test;
mod php_editor_test;
mod php_extract_method_test;
mod php_extract_variable_test;
mod php_imports_test;
mod providers_context_test;
mod providers_document_lifecycle_test;
mod providers_store_compliance_test;
mod smoke_test;
mod workspace_asset_snapshot_test;
mod workspace_test;

#[cfg(feature = "integration")]
mod real_world;
