//! Code actions and generator commands (port of Go
//! `internal/lsp/codeaction`): PHP refactorings (organize imports, extract
//! variable/method), Symfony/Shopware generators and migrations, Twig and
//! Administration helpers, and snippet actions.
//!
//! Every edit is a validated workspace edit built through `swlsp-rewrite`
//! or a protocol text edit derived from CST byte ranges; providers never
//! write files.

mod shared;

mod php_extract_method;
mod php_extract_variable;
mod php_imports;
mod route;
mod shopware_context;
mod snippet;
mod twig;

pub use php_extract_method::PhpExtractMethodProvider;
pub use php_extract_variable::PhpExtractVariableProvider;
pub use php_imports::PhpImportsProvider;
pub use route::{RouteActionParameterCodeActionProvider, RouteAttributeCodeActionProvider};
pub use shopware_context::{
    AdminContextProvider, CREATE_EVENT_LISTENER_ACTION, EXTEND_ADMIN_COMPONENT_ACTION,
    EventListenerContextProvider, OVERRIDE_ADMIN_METHOD_ACTION,
};
pub use snippet::{
    COPY_SNIPPET_USAGE_ACTION, SnippetCodeActionProvider, SnippetCopyCodeActionProvider,
};
pub use twig::TwigCodeActionProvider;

// --- symfony ---
mod service_tag;
mod symfony_generator;
mod xml_service_suggestion;
pub use service_tag::ServiceTagCodeActionProvider;
pub use symfony_generator::{
    COLLECT_SYMFONY_SERVICES_COMMAND, CREATE_COMPILER_PASS_ACTION, CREATE_COMPILER_PASS_COMMAND,
    CompilerPassCreationRequest, CompilerPassCreationResponse, GENERATE_SYMFONY_SERVICE_ACTION,
    GENERATE_SYMFONY_SERVICE_COMMAND, SymfonyGeneratorProvider,
    SymfonyServiceDefinitionCollectionEntry, SymfonyServiceDefinitionCollectionRequest,
    SymfonyServiceDefinitionCollectionResponse, SymfonyServiceGenerationRequest,
    SymfonyServiceGenerationResponse, SymfonyServiceParameterSuggestion,
};
pub use xml_service_suggestion::XmlServiceSuggestionCodeActionProvider;

// --- command ---
mod command_invoke_parameter;
mod invokable_command_migration;
mod property_service;
pub use command_invoke_parameter::CommandInvokeParameterCodeActionProvider;
pub use invokable_command_migration::InvokableCommandMigrationCodeActionProvider;
pub use property_service::PropertyServiceCodeActionProvider;

// --- twig ---
mod admin_twig_override;
mod twig_extension_attribute;
mod twig_template_generator;
mod twig_translation_extract;
pub use admin_twig_override::{
    ADMIN_TWIG_OVERRIDE_ACTION, ADMIN_TWIG_OVERRIDE_COMMAND, AdminTwigOverrideProvider,
};
pub use twig_extension_attribute::TwigExtensionAttributeCodeActionProvider;
pub use twig_template_generator::{
    GENERATE_TWIG_BLOCKS_ACTION, GENERATE_TWIG_BLOCKS_COMMAND, GENERATE_TWIG_EXTENDS_ACTION,
    GENERATE_TWIG_EXTENDS_COMMAND, TWIG_BLOCK_CANDIDATES_COMMAND, TWIG_EXTENDS_CANDIDATES_COMMAND,
    TwigTemplateGeneratorProvider,
};
pub use twig_translation_extract::{
    EXTRACT_TWIG_TRANSLATION_ACTION, GENERATE_TWIG_TRANSLATION_EXTRACTION_COMMAND,
    PREPARE_TWIG_TRANSLATION_EXTRACTION_COMMAND, TwigTranslationExtractProvider,
};

#[cfg(test)]
mod test_support;
#[cfg(test)]
mod tests;
