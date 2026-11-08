//! Stable editor-integration catalog shared by LSP clients and MCP. It
//! describes UI responsibilities without duplicating any generator or analysis
//! implementation.

use serde::Serialize;
use serde_json::Value;

pub const PROTOCOL_VERSION: i32 = 1;

#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Field {
    pub name: &'static str,
    pub label: &'static str,
    #[serde(rename = "type")]
    pub field_type: &'static str,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub required: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub default: Option<Value>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub choices: Vec<&'static str>,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub source_command: &'static str,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ClientCommand {
    pub id: &'static str,
    pub description: &'static str,
    pub surfaces: Vec<&'static str>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub arguments: Vec<Field>,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ScaffoldDefinition {
    pub family: &'static str,
    pub kind: &'static str,
    pub label: &'static str,
    pub description: &'static str,
    pub workflow: &'static str,
    #[serde(skip_serializing_if = "str::is_empty")]
    pub name_placeholder: &'static str,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub options: Vec<Field>,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Catalog {
    pub protocol_version: i32,
    pub client_commands: Vec<ClientCommand>,
    pub scaffolds: Vec<ScaffoldDefinition>,
}

pub fn current_catalog() -> Catalog {
    Catalog {
        protocol_version: PROTOCOL_VERSION,
        client_commands: client_commands(),
        scaffolds: scaffolds(),
    }
}

macro_rules! fields {
    ($($field:expr),* $(,)?) => { vec![$($field),*] };
}

pub fn client_commands() -> Vec<ClientCommand> {
    vec![
        command(
            "shopware.openReferences",
            "Open a references result list",
            "codeLens",
            fields!(field("references", "References", "string[]", true),),
        ),
        command(
            "shopware.symfony.runConsoleCommand",
            "Confirm and run a Symfony console command",
            "codeLens",
            fields!(
                field("command", "Command", "string", true),
                field("fileUri", "Source file", "uri", true),
            ),
        ),
        command(
            "shopware.symfony.twigVariables",
            "Browse typed Twig variables",
            "inlayHint",
            fields!(
                field("fileUri", "Twig file", "uri", true),
                field("variables", "Variable names", "string[]", true),
            ),
        ),
        command(
            "shopware.insertSnippet",
            "Insert an existing snippet through an editor picker",
            "codeAction",
            vec![],
        ),
        command(
            "shopware.createSnippet",
            "Create a storefront snippet",
            "quickFix",
            fields!(
                field("snippetKey", "Snippet key", "string", true),
                field("fileUri", "Source file", "uri", true),
            ),
        ),
        command(
            "shopware.createAdminSnippet",
            "Create an Administration snippet",
            "quickFix",
            fields!(
                field("snippetKey", "Snippet key", "string", true),
                field("fileUri", "Source file", "uri", true),
            ),
        ),
        command(
            "shopware.createSnippetFromSelection",
            "Create a storefront snippet from selected text",
            "codeAction",
            selection_arguments(),
        ),
        command(
            "shopware.createAdminSnippetFromSelection",
            "Create an Administration snippet from selected text",
            "codeAction",
            selection_arguments(),
        ),
        command(
            "shopware.copySnippetUsage",
            "Copy the selected snippet usage",
            "codeAction",
            fields!(field("snippetKey", "Snippet key", "string", true),),
        ),
        command(
            "shopware.editor.insertSnippetAtPosition",
            "Insert a server-provided snippet at an exact editor position",
            "quickFix",
            fields!(
                field("fileUri", "Target file", "uri", true),
                field("line", "Line", "integer", true),
                field("character", "Character", "integer", true),
                field("snippetText", "Snippet", "string", true),
            ),
        ),
        command(
            "shopware.twig.extendBlock",
            "Choose an extension and create a storefront block override",
            "codeAction",
            fields!(
                field("fileUri", "Twig file", "uri", true),
                field("blockName", "Block", "string", true),
            ),
        ),
        command(
            "shopware.twig.showBlockDiff",
            "Show the current and upstream Twig block difference",
            "codeAction",
            fields!(
                field("fileUri", "Twig file", "uri", true),
                field("blockName", "Block", "string", true),
            ),
        ),
        command(
            "shopware.admin.overrideTwigBlock",
            "Choose a plugin and generate an Administration Twig override",
            "codeAction",
            fields!(
                field("fileUri", "Twig file", "uri", true),
                field("blockName", "Block", "string", true),
            ),
        ),
        command(
            "shopware.admin.extendComponent",
            "Choose a plugin and extend or override an Administration component",
            "codeAction",
            fields!(
                field("component", "Component", "string", true),
                field("fileUri", "Source file", "uri", true),
            ),
        ),
        command(
            "shopware.admin.overrideMethod",
            "Generate an Administration method override",
            "codeAction",
            fields!(
                field("component", "Component", "string", true),
                field("method", "Method", "string", true),
                field("methodGroup", "Method group", "string", false),
                field("parameters", "Parameters", "string", false),
                field("fileUri", "Source file", "uri", true),
            ),
        ),
        command(
            "shopware.createEventListener",
            "Generate a listener for the selected event",
            "codeAction",
            fields!(
                field("event", "Event class", "string", true),
                field("suggestedName", "Suggested listener name", "string", true),
                field("fileUri", "Source file", "uri", true),
            ),
        ),
        command(
            "shopware.symfony.generateService",
            "Generate a Symfony service definition",
            "codeAction",
            class_arguments(),
        ),
        command(
            "shopware.symfony.createCompilerPass",
            "Generate and register a compiler pass",
            "codeAction",
            fields!(
                field("bundleUri", "Bundle file", "uri", true),
                field("bundleClass", "Bundle class", "string", true),
            ),
        ),
        command(
            "shopware.symfony.generateTwigExtends",
            "Choose and insert a Twig parent template",
            "codeAction",
            uri_arguments(),
        ),
        command(
            "shopware.symfony.generateTwigBlocks",
            "Choose and insert parent Twig blocks",
            "codeAction",
            uri_arguments(),
        ),
        command(
            "shopware.symfony.extractTwigTranslation",
            "Extract selected Twig text into translations",
            "codeAction",
            fields!(
                field("fileUri", "Twig file", "uri", true),
                field("range", "Selected range", "range", true),
            ),
        ),
    ]
}

pub fn scaffolds() -> Vec<ScaffoldDefinition> {
    vec![
        scaffold(
            "shopware",
            "entity-definition",
            "DAL Entity / Mapping / Extensions",
            "Visual entity, mapping, EntityExtension, or BulkEntityExtension migration, service, and snapshot workflow",
            "entity-schema",
            "product_note",
        ),
        scaffold_with_options(
            "shopware",
            "plugin",
            "Plugin Skeleton",
            "Shopware plugin package, class, and YAML service configuration",
            "workspace-edit",
            "AcmeExample",
            vec![
                field("namespace", "PHP namespace", "string", false),
                field("description", "Description", "string", false),
                default_field("author", "Author", "string", "Acme"),
                default_field("license", "License", "string", "MIT"),
                field("package", "Composer package", "string", false),
            ],
        ),
        scaffold(
            "shopware",
            "system-config",
            "System Configuration",
            "Shopware system configuration XML",
            "workspace-edit",
            "configuration",
        ),
        scaffold_with_options(
            "shopware",
            "scheduled-task",
            "Scheduled Task",
            "Scheduled task and handler classes",
            "workspace-edit",
            "Cleanup",
            vec![
                field("namespace", "PHP namespace", "string", false),
                default_field("interval", "Interval in seconds", "integer", 300),
                field("taskName", "Task name", "string", false),
            ],
        ),
        scaffold_with_options(
            "shopware",
            "migration",
            "Migration",
            "Timestamped Shopware migration",
            "workspace-edit",
            "AddProductIndex",
            vec![
                field("namespace", "PHP namespace", "string", false),
                field("timestamp", "Unix timestamp", "string", false),
            ],
        ),
        scaffold_with_options(
            "shopware",
            "event-listener",
            "Event Listener",
            "Attribute-based Shopware event listener",
            "workspace-edit",
            "ProductWrittenListener",
            vec![
                field("namespace", "PHP namespace", "string", false),
                field("event", "Event class", "string", true),
            ],
        ),
        scaffold_with_options(
            "shopware",
            "admin-component",
            "Administration Component",
            "Administration component, Twig, and SCSS files",
            "workspace-edit",
            "sw-example-card",
            vec![
                choice_field(
                    "mode",
                    "Mode",
                    "register",
                    &["register", "extend", "override"],
                ),
                field("target", "Existing component", "string", false),
                field("generateTwig", "Generate Twig", "boolean", false),
                field("generateScss", "Generate SCSS", "boolean", false),
                field("method", "Method", "string", false),
                field("methodGroup", "Method group", "string", false),
                field("parameters", "Parameters", "string", false),
            ],
        ),
        scaffold_with_options(
            "shopware",
            "admin-module",
            "Administration Module",
            "Administration module and snippets",
            "workspace-edit",
            "sw-example",
            vec![
                default_field("type", "Extension type", "string", "plugin"),
                default_field("color", "Module color", "string", "#189EFF"),
                default_field("icon", "Module icon", "string", "regular-cube"),
            ],
        ),
        scaffold_with_options(
            "shopware",
            "cms-block",
            "CMS Block",
            "Administration CMS block and preview",
            "workspace-edit",
            "example-text",
            vec![default_field("category", "Category", "string", "text")],
        ),
        scaffold(
            "shopware",
            "cms-element",
            "CMS Element",
            "Administration CMS element",
            "workspace-edit",
            "example-media",
        ),
        scaffold_with_options(
            "shopware",
            "app",
            "App Manifest",
            "Shopware app manifest",
            "workspace-edit",
            "acme-example",
            vec![
                field("label", "Label", "string", false),
                default_field("author", "Author", "string", "Acme"),
                default_field("license", "License", "string", "MIT"),
            ],
        ),
        scaffold(
            "shopware",
            "app-custom-entities",
            "App Custom Entities",
            "Shopware app custom entities XML",
            "workspace-edit",
            "catalog-entry",
        ),
        scaffold(
            "shopware",
            "app-cms",
            "App CMS Configuration",
            "Shopware app CMS XML",
            "workspace-edit",
            "cms",
        ),
        scaffold_with_options(
            "shopware",
            "app-script",
            "App Script",
            "Shopware app script hook",
            "workspace-edit",
            "product-page-loaded",
            vec![field("hook", "Hook", "string", false)],
        ),
        scaffold(
            "symfony",
            "command",
            "Command",
            "Symfony Console command",
            "workspace-edit",
            "CacheWarm",
        ),
        scaffold(
            "symfony",
            "controller",
            "Controller",
            "Symfony controller with route",
            "workspace-edit",
            "Product",
        ),
        scaffold(
            "symfony",
            "twig-extension",
            "Twig Extension",
            "Twig functions and filters",
            "workspace-edit",
            "PriceExtension",
        ),
        scaffold(
            "symfony",
            "compiler-pass",
            "Compiler Pass",
            "Dependency-injection compiler pass",
            "workspace-edit",
            "CollectServicesPass",
        ),
        scaffold(
            "symfony",
            "kernel-test",
            "Kernel Test",
            "KernelTestCase integration test",
            "workspace-edit",
            "Container",
        ),
        scaffold(
            "symfony",
            "web-test",
            "Web Test",
            "WebTestCase functional test",
            "workspace-edit",
            "Storefront",
        ),
        scaffold(
            "symfony",
            "services-yaml",
            "YAML Service Configuration",
            "Autowiring service prototype",
            "workspace-edit",
            "services",
        ),
        scaffold(
            "symfony",
            "services-xml",
            "XML Service Configuration",
            "Autowiring service prototype",
            "workspace-edit",
            "services",
        ),
        scaffold(
            "symfony",
            "services-php",
            "PHP Service Configuration",
            "Fluent service configurator",
            "workspace-edit",
            "services",
        ),
    ]
}

fn command(
    id: &'static str,
    description: &'static str,
    surface: &'static str,
    arguments: Vec<Field>,
) -> ClientCommand {
    ClientCommand {
        id,
        description,
        surfaces: vec![surface],
        arguments,
    }
}

fn field(
    name: &'static str,
    label: &'static str,
    field_type: &'static str,
    required: bool,
) -> Field {
    Field {
        name,
        label,
        field_type,
        required,
        default: None,
        choices: Vec::new(),
        source_command: "",
    }
}

fn default_field(
    name: &'static str,
    label: &'static str,
    field_type: &'static str,
    value: impl Into<Value>,
) -> Field {
    Field {
        default: Some(value.into()),
        ..field(name, label, field_type, false)
    }
}

fn choice_field(
    name: &'static str,
    label: &'static str,
    default: &'static str,
    choices: &[&'static str],
) -> Field {
    Field {
        default: Some(default.into()),
        choices: choices.to_vec(),
        ..field(name, label, "enum", false)
    }
}

fn uri_arguments() -> Vec<Field> {
    vec![field("fileUri", "Source file", "uri", true)]
}

fn class_arguments() -> Vec<Field> {
    vec![
        field("fileUri", "Source file", "uri", true),
        field("className", "Class name", "string", true),
    ]
}

fn selection_arguments() -> Vec<Field> {
    vec![
        field("fileUri", "Source file", "uri", true),
        field("selectedText", "Selected text", "string", true),
    ]
}

fn scaffold(
    family: &'static str,
    kind: &'static str,
    label: &'static str,
    description: &'static str,
    workflow: &'static str,
    placeholder: &'static str,
) -> ScaffoldDefinition {
    scaffold_with_options(
        family,
        kind,
        label,
        description,
        workflow,
        placeholder,
        Vec::new(),
    )
}

fn scaffold_with_options(
    family: &'static str,
    kind: &'static str,
    label: &'static str,
    description: &'static str,
    workflow: &'static str,
    placeholder: &'static str,
    options: Vec<Field>,
) -> ScaffoldDefinition {
    ScaffoldDefinition {
        family,
        kind,
        label,
        description,
        workflow,
        name_placeholder: placeholder,
        options,
    }
}

#[cfg(test)]
mod tests {
    use std::collections::HashSet;

    use super::*;

    #[test]
    fn catalog_is_stable_and_unique() {
        let catalog = current_catalog();
        assert_eq!(catalog.protocol_version, PROTOCOL_VERSION);
        let mut commands = HashSet::new();
        for command in &catalog.client_commands {
            assert!(!command.id.is_empty());
            assert!(commands.insert(command.id), "duplicate {}", command.id);
        }
        assert!(commands.contains("shopware.admin.extendComponent"));
        assert!(commands.contains("shopware.twig.extendBlock"));
        let mut scaffolds = HashSet::new();
        for scaffold in &catalog.scaffolds {
            assert!(scaffolds.insert(format!("{}:{}", scaffold.family, scaffold.kind)));
        }
        for key in [
            "shopware:entity-definition",
            "shopware:plugin",
            "symfony:controller",
        ] {
            assert!(scaffolds.contains(key), "{key}");
        }
    }
}
