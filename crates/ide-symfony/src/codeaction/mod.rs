//! Code actions of the Symfony domain.
//!
//! Every edit is a validated workspace edit built through `swlsp-rewrite`
//! or a protocol text edit derived from CST byte ranges; providers never
//! write files.

mod command_invoke_parameter;
mod event_listener;
mod invokable_command_migration;
mod property_service;
mod route;
mod service_tag;
mod symfony_generator;
mod xml_service_suggestion;

pub use command_invoke_parameter::CommandInvokeParameterCodeActionProvider;
pub use event_listener::{CREATE_EVENT_LISTENER_ACTION, EventListenerContextProvider};
pub use invokable_command_migration::InvokableCommandMigrationCodeActionProvider;
pub use property_service::PropertyServiceCodeActionProvider;
pub use route::{RouteActionParameterCodeActionProvider, RouteAttributeCodeActionProvider};
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
