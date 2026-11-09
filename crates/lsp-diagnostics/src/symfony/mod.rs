//! Symfony analyzers: Messenger, Console, Serializer, Validation, Doctrine,
//! assets, embedded languages and SCSS variables.

mod asset;
mod console;
mod dbal_schema;
mod doctrine;
mod embedded;
mod messenger;
mod scss_variable;
mod serializer;
mod validation;

#[cfg(test)]
mod tests;

pub use asset::{
    AssetAnalyzer, MISSING_ASSET_CODE, MISSING_ASSET_PACKAGE_CODE, MISSING_ENCORE_ENTRY_CODE,
    MISSING_IMPORTMAP_CODE, MISSING_VITE_ENTRY_CODE,
};
pub use console::{
    ConsoleAnalyzer, MISSING_CONSOLE_ARGUMENT_CODE, MISSING_CONSOLE_COMMAND_CODE,
    MISSING_CONSOLE_OPTION_CODE,
};
pub use doctrine::{
    DoctrineAnalyzer, INVALID_DOCTRINE_TYPE_CLASS_CODE, MISSING_DOCTRINE_COLUMN_CODE,
    MISSING_DOCTRINE_TABLE_CODE, MISSING_DOCTRINE_TYPE_CLASS_CODE,
};
pub use embedded::{
    EmbeddedCssAnalyzer, EmbeddedJsonAnalyzer, EmbeddedLanguageAnalyzer, EmbeddedXPathAnalyzer,
    INVALID_EMBEDDED_CSS_CODE, INVALID_EMBEDDED_JSON_CODE, INVALID_EMBEDDED_XPATH_CODE,
};
pub use messenger::{
    MISSING_MESSENGER_HANDLER_METHOD_CODE, MISSING_MESSENGER_MESSAGE_CODE, MessengerAnalyzer,
};
pub use scss_variable::{
    SCSS_VARIABLE_UNKNOWN_CODE, ScssVariableAnalyzer, ScssVariableDeclarationProvider,
    ScssVariableIndexReadiness,
};
pub use serializer::{MISSING_SERIALIZER_CLASS_CODE, SerializerAnalyzer};
pub use validation::{MISSING_CONSTRAINT_OPTION_CODE, ValidationAnalyzer};

use serde_json::{Value, json};
use swlsp_lsp::{DiagnosticId, Problem};
use swlsp_lsp_protocol::DiagnosticSeverity;
use swlsp_syntax::TextRange;
use swlsp_textutil::lowercase;
use swlsp_uriutil::filepath;

/// The lowercased extension of the last path element.
pub(crate) fn lower_ext(path: &str) -> String {
    lowercase(filepath::ext(path))
}

/// The `{"suggestions": [...]}` payload.
pub(crate) fn suggestions_payload(suggestions: Vec<String>) -> Value {
    json!({ "suggestions": suggestions })
}

/// A Symfony warning with a suggestion payload.
pub(crate) fn symfony_warning(
    id: &str,
    range: TextRange,
    message: String,
    suggestions: Vec<String>,
) -> Problem {
    Problem {
        id: DiagnosticId::from(id),
        range,
        message,
        severity: Some(DiagnosticSeverity::WARNING),
        source: "symfony".to_owned(),
        payload: Some(suggestions_payload(suggestions)),
        ..Problem::default()
    }
}
