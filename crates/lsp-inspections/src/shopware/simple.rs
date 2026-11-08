//! Ports of Go `shopware_php_local.go`, `shopware_php_semantic.go` and
//! `dal_entity.go`: analyzer inspections with the suggestion fix.

use std::sync::Arc;

use swlsp_language::LanguageId;
use swlsp_lsp::Inspection;
use swlsp_lsp_diagnostics::{
    DalEntityAnalyzer, SHOPWARE_PHP_CONCRETE_DECORATOR_EXTENSION_CODE,
    SHOPWARE_PHP_DISALLOWED_FUNCTION_CODE, SHOPWARE_PHP_FOREIGN_KEY_CHECKS_CODE,
    SHOPWARE_PHP_GLOB_BRACE_CODE, SHOPWARE_PHP_INSECURE_COOKIE_CODE,
    SHOPWARE_PHP_INTERNAL_CLASS_EXTENSION_CODE, SHOPWARE_PHP_INTERNAL_FUNCTION_CALL_CODE,
    SHOPWARE_PHP_INTERNAL_METHOD_CALL_CODE, SHOPWARE_PHP_PREDICTABLE_SALT_CODE,
    SHOPWARE_PHP_REPOSITORY_IN_LOOP_CODE, SHOPWARE_PHP_SCHEDULED_TASK_INTERVAL_CODE,
    SHOPWARE_PHP_SESSION_CONSTRUCTOR_CODE, SHOPWARE_PHP_SESSION_FUNCTION_CODE,
    SHOPWARE_PHP_SESSION_PAYMENT_HANDLER_CODE, SHOPWARE_PHP_SESSION_STORE_API_CODE,
    SHOPWARE_PHP_SUPERGLOBAL_CODE, SHOPWARE_PHP_TLS_VERIFICATION_CODE,
    SHOPWARE_PHP_USER_STORE_TOKEN_CODE, SHOPWARE_PHP_WEAK_KEY_CODE, ShopwarePhpLocalAnalyzer,
    ShopwarePhpSemanticAnalyzer,
};
use swlsp_php::PhpIndex;
use swlsp_shopware_dal::Index as DalIndex;

use crate::new_analyzer_inspection;

/// Go `NewShopwarePHPLocal`.
pub fn new_shopware_php_local() -> Arc<dyn Inspection> {
    new_analyzer_inspection(
        "shopware.php.local",
        &[LanguageId::Php],
        "shopware-lsp",
        &[
            SHOPWARE_PHP_SUPERGLOBAL_CODE,
            SHOPWARE_PHP_DISALLOWED_FUNCTION_CODE,
            SHOPWARE_PHP_SESSION_FUNCTION_CODE,
            SHOPWARE_PHP_GLOB_BRACE_CODE,
            SHOPWARE_PHP_TLS_VERIFICATION_CODE,
            SHOPWARE_PHP_PREDICTABLE_SALT_CODE,
            SHOPWARE_PHP_WEAK_KEY_CODE,
            SHOPWARE_PHP_INSECURE_COOKIE_CODE,
            SHOPWARE_PHP_FOREIGN_KEY_CHECKS_CODE,
        ],
        vec![Arc::new(ShopwarePhpLocalAnalyzer::new())],
    )
}

/// Go `NewShopwarePHPSemantic`.
pub fn new_shopware_php_semantic(php_index: Option<PhpIndex>) -> Arc<dyn Inspection> {
    new_analyzer_inspection(
        "shopware.php.semantic",
        &[LanguageId::Php],
        "shopware-lsp",
        &[
            SHOPWARE_PHP_REPOSITORY_IN_LOOP_CODE,
            SHOPWARE_PHP_INTERNAL_CLASS_EXTENSION_CODE,
            SHOPWARE_PHP_INTERNAL_FUNCTION_CALL_CODE,
            SHOPWARE_PHP_INTERNAL_METHOD_CALL_CODE,
            SHOPWARE_PHP_SESSION_CONSTRUCTOR_CODE,
            SHOPWARE_PHP_SESSION_PAYMENT_HANDLER_CODE,
            SHOPWARE_PHP_SESSION_STORE_API_CODE,
            SHOPWARE_PHP_SCHEDULED_TASK_INTERVAL_CODE,
            SHOPWARE_PHP_USER_STORE_TOKEN_CODE,
            SHOPWARE_PHP_CONCRETE_DECORATOR_EXTENSION_CODE,
        ],
        vec![Arc::new(ShopwarePhpSemanticAnalyzer::new(php_index))],
    )
}

/// Go `NewDALEntity`.
pub fn new_dal_entity(index: Option<Arc<DalIndex>>) -> Arc<dyn Inspection> {
    new_analyzer_inspection(
        "shopware.dal.entity",
        &[LanguageId::JavaScript, LanguageId::Vue],
        "shopware-lsp",
        &["shopware.dal.entity-not-found"],
        vec![Arc::new(DalEntityAnalyzer::new(index))],
    )
}
