//! Code actions of the PHP domain.
//!
//! Every edit is a validated workspace edit built through `swlsp-rewrite`
//! or a protocol text edit derived from CST byte ranges; providers never
//! write files.

mod php_extract_method;
mod php_extract_variable;
mod php_imports;

pub use php_extract_method::PhpExtractMethodProvider;
pub use php_extract_variable::PhpExtractVariableProvider;
pub use php_imports::PhpImportsProvider;

#[cfg(test)]
#[path = "tests/php.rs"]
mod tests;
