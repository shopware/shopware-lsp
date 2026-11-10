//! Symfony analytics catalogs:
//! routes, services, Twig extensions, template usages and variables, Twig
//! components, and the local Symfony profiler request index.
//!
//! Every catalog is exposed as a custom LSP command with a JSON response.

mod twig_component_catalog;
mod twig_extension_catalog;
mod twig_template_usage_catalog;
mod twig_template_variable_catalog;

pub use twig_component_catalog::{
    LIST_TWIG_COMPONENTS_COMMAND, TwigComponentBlockEntry, TwigComponentCatalogEntry,
    TwigComponentCatalogProvider, TwigComponentCatalogRequest, TwigComponentDeclarationEntry,
    TwigComponentPropEntry, TwigComponentSyntaxEntry, TwigComponentTemplateEntry,
    TwigComponentUsageEntry,
};
pub use twig_extension_catalog::{
    LIST_TWIG_EXTENSIONS_COMMAND, TwigExtensionCatalogEntry, TwigExtensionCatalogParameter,
    TwigExtensionCatalogProvider, TwigExtensionCatalogRequest,
};
pub use twig_template_usage_catalog::{
    LIST_TWIG_TEMPLATE_USAGES_COMMAND, TwigTemplateComponentUsage, TwigTemplateControllerUsage,
    TwigTemplateReferenceUsage, TwigTemplateRouteEntry, TwigTemplateSourceLocation,
    TwigTemplateUsageCatalogEntry, TwigTemplateUsageCatalogProvider,
    TwigTemplateUsageCatalogRequest,
};
pub use twig_template_variable_catalog::{
    LIST_TWIG_TEMPLATE_VARIABLES_COMMAND, TwigTemplateVariableCatalogEntry,
    TwigTemplateVariableCatalogProvider, TwigTemplateVariableCatalogRequest,
    TwigTemplateVariableEntry, TwigTemplateVariableProperty, TwigTemplateVariableSource,
};
// What the analytics providers of all domains share.
#[allow(unused_imports)]
pub(crate) use swlsp_ide::analytics::*;

#[cfg(test)]
mod tests;
