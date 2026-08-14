// Package integration owns the stable editor-integration catalog shared by
// LSP clients and MCP. It describes UI responsibilities without duplicating
// any generator or analysis implementation.
package integration

const ProtocolVersion = 1

type Field struct {
	Name          string   `json:"name"`
	Label         string   `json:"label"`
	Type          string   `json:"type"`
	Required      bool     `json:"required,omitempty"`
	Default       any      `json:"default,omitempty"`
	Choices       []string `json:"choices,omitempty"`
	SourceCommand string   `json:"sourceCommand,omitempty"`
}

type ClientCommand struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Surfaces    []string `json:"surfaces"`
	Arguments   []Field  `json:"arguments,omitempty"`
}

type ScaffoldDefinition struct {
	Family          string  `json:"family"`
	Kind            string  `json:"kind"`
	Label           string  `json:"label"`
	Description     string  `json:"description"`
	Workflow        string  `json:"workflow"`
	NamePlaceholder string  `json:"namePlaceholder,omitempty"`
	Options         []Field `json:"options,omitempty"`
}

type Catalog struct {
	ProtocolVersion int                  `json:"protocolVersion"`
	ClientCommands  []ClientCommand      `json:"clientCommands"`
	Scaffolds       []ScaffoldDefinition `json:"scaffolds"`
}

func CurrentCatalog() Catalog {
	return Catalog{
		ProtocolVersion: ProtocolVersion,
		ClientCommands:  ClientCommands(),
		Scaffolds:       Scaffolds(),
	}
}

func ClientCommands() []ClientCommand {
	return []ClientCommand{
		command("shopware.openReferences", "Open a references result list", "codeLens", fields(
			field("references", "References", "string[]", true),
		)),
		command("shopware.symfony.runConsoleCommand", "Confirm and run a Symfony console command", "codeLens", fields(
			field("command", "Command", "string", true),
			field("fileUri", "Source file", "uri", true),
		)),
		command("shopware.symfony.twigVariables", "Browse typed Twig variables", "inlayHint", fields(
			field("fileUri", "Twig file", "uri", true),
			field("variables", "Variable names", "string[]", true),
		)),
		command("shopware.insertSnippet", "Insert an existing snippet through an editor picker", "codeAction", nil),
		command("shopware.createSnippet", "Create a storefront snippet", "quickFix", fields(
			field("snippetKey", "Snippet key", "string", true),
			field("fileUri", "Source file", "uri", true),
		)),
		command("shopware.createAdminSnippet", "Create an Administration snippet", "quickFix", fields(
			field("snippetKey", "Snippet key", "string", true),
			field("fileUri", "Source file", "uri", true),
		)),
		command("shopware.createSnippetFromSelection", "Create a storefront snippet from selected text", "codeAction", selectionArguments()),
		command("shopware.createAdminSnippetFromSelection", "Create an Administration snippet from selected text", "codeAction", selectionArguments()),
		command("shopware.copySnippetUsage", "Copy the selected snippet usage", "codeAction", fields(
			field("snippetKey", "Snippet key", "string", true),
		)),
		command("shopware.editor.insertSnippetAtPosition", "Insert a server-provided snippet at an exact editor position", "quickFix", fields(
			field("fileUri", "Target file", "uri", true),
			field("line", "Line", "integer", true),
			field("character", "Character", "integer", true),
			field("snippetText", "Snippet", "string", true),
		)),
		command("shopware.twig.extendBlock", "Choose an extension and create a storefront block override", "codeAction", fields(
			field("fileUri", "Twig file", "uri", true),
			field("blockName", "Block", "string", true),
		)),
		command("shopware.twig.showBlockDiff", "Show the current and upstream Twig block difference", "codeAction", fields(
			field("fileUri", "Twig file", "uri", true),
			field("blockName", "Block", "string", true),
		)),
		command("shopware.admin.overrideTwigBlock", "Choose a plugin and generate an Administration Twig override", "codeAction", fields(
			field("fileUri", "Twig file", "uri", true),
			field("blockName", "Block", "string", true),
		)),
		command("shopware.admin.extendComponent", "Choose a plugin and extend or override an Administration component", "codeAction", fields(
			field("component", "Component", "string", true),
			field("fileUri", "Source file", "uri", true),
		)),
		command("shopware.admin.overrideMethod", "Generate an Administration method override", "codeAction", fields(
			field("component", "Component", "string", true),
			field("method", "Method", "string", true),
			field("methodGroup", "Method group", "string", false),
			field("parameters", "Parameters", "string", false),
			field("fileUri", "Source file", "uri", true),
		)),
		command("shopware.createEventListener", "Generate a listener for the selected event", "codeAction", fields(
			field("event", "Event class", "string", true),
			field("suggestedName", "Suggested listener name", "string", true),
			field("fileUri", "Source file", "uri", true),
		)),
		command("shopware.symfony.generateService", "Generate a Symfony service definition", "codeAction", classArguments()),
		command("shopware.symfony.createCompilerPass", "Generate and register a compiler pass", "codeAction", fields(
			field("bundleUri", "Bundle file", "uri", true),
			field("bundleClass", "Bundle class", "string", true),
		)),
		command("shopware.symfony.generateFormFields", "Generate form fields from an inferred data class", "codeAction", classArguments()),
		command("shopware.symfony.generateTwigFormFields", "Generate Twig form rows", "codeAction", uriArguments()),
		command("shopware.symfony.generateTwigExtends", "Choose and insert a Twig parent template", "codeAction", uriArguments()),
		command("shopware.symfony.generateTwigBlocks", "Choose and insert parent Twig blocks", "codeAction", uriArguments()),
		command("shopware.symfony.extractTwigTranslation", "Extract selected Twig text into translations", "codeAction", fields(
			field("fileUri", "Twig file", "uri", true),
			field("range", "Selected range", "range", true),
		)),
	}
}

func Scaffolds() []ScaffoldDefinition {
	return []ScaffoldDefinition{
		scaffold("shopware", "entity-definition", "DAL Entity / Mapping / Extensions", "Visual entity, mapping, EntityExtension, or BulkEntityExtension migration, service, and snapshot workflow", "entity-schema", "product_note"),
		scaffoldWithOptions("shopware", "plugin", "Plugin Skeleton", "Shopware plugin package, class, and YAML service configuration", "workspace-edit", "AcmeExample", []Field{
			field("namespace", "PHP namespace", "string", false),
			field("description", "Description", "string", false),
			defaultField("author", "Author", "string", "Acme"),
			defaultField("license", "License", "string", "MIT"),
			field("package", "Composer package", "string", false),
		}),
		scaffold("shopware", "system-config", "System Configuration", "Shopware system configuration XML", "workspace-edit", "configuration"),
		scaffoldWithOptions("shopware", "scheduled-task", "Scheduled Task", "Scheduled task and handler classes", "workspace-edit", "Cleanup", []Field{
			field("namespace", "PHP namespace", "string", false),
			defaultField("interval", "Interval in seconds", "integer", 300),
			field("taskName", "Task name", "string", false),
		}),
		scaffoldWithOptions("shopware", "migration", "Migration", "Timestamped Shopware migration", "workspace-edit", "AddProductIndex", []Field{
			field("namespace", "PHP namespace", "string", false),
			field("timestamp", "Unix timestamp", "string", false),
		}),
		scaffoldWithOptions("shopware", "event-listener", "Event Listener", "Attribute-based Shopware event listener", "workspace-edit", "ProductWrittenListener", []Field{
			field("namespace", "PHP namespace", "string", false),
			field("event", "Event class", "string", true),
		}),
		scaffoldWithOptions("shopware", "admin-component", "Administration Component", "Administration component, Twig, and SCSS files", "workspace-edit", "sw-example-card", []Field{
			choiceField("mode", "Mode", "register", "register", "extend", "override"),
			field("target", "Existing component", "string", false),
			field("generateTwig", "Generate Twig", "boolean", false),
			field("generateScss", "Generate SCSS", "boolean", false),
			field("method", "Method", "string", false),
			field("methodGroup", "Method group", "string", false),
			field("parameters", "Parameters", "string", false),
		}),
		scaffoldWithOptions("shopware", "admin-module", "Administration Module", "Administration module and snippets", "workspace-edit", "sw-example", []Field{
			defaultField("type", "Extension type", "string", "plugin"),
			defaultField("color", "Module color", "string", "#189EFF"),
			defaultField("icon", "Module icon", "string", "regular-cube"),
		}),
		scaffoldWithOptions("shopware", "cms-block", "CMS Block", "Administration CMS block and preview", "workspace-edit", "example-text", []Field{
			defaultField("category", "Category", "string", "text"),
		}),
		scaffold("shopware", "cms-element", "CMS Element", "Administration CMS element", "workspace-edit", "example-media"),
		scaffoldWithOptions("shopware", "app", "App Manifest", "Shopware app manifest", "workspace-edit", "acme-example", []Field{
			field("label", "Label", "string", false),
			defaultField("author", "Author", "string", "Acme"),
			defaultField("license", "License", "string", "MIT"),
		}),
		scaffold("shopware", "app-custom-entities", "App Custom Entities", "Shopware app custom entities XML", "workspace-edit", "catalog-entry"),
		scaffold("shopware", "app-cms", "App CMS Configuration", "Shopware app CMS XML", "workspace-edit", "cms"),
		scaffoldWithOptions("shopware", "app-script", "App Script", "Shopware app script hook", "workspace-edit", "product-page-loaded", []Field{
			field("hook", "Hook", "string", false),
		}),
		scaffold("symfony", "command", "Command", "Symfony Console command", "workspace-edit", "CacheWarm"),
		scaffold("symfony", "controller", "Controller", "Symfony controller with route", "workspace-edit", "Product"),
		scaffold("symfony", "form", "Form Type", "Symfony form type", "workspace-edit", "ProductType"),
		scaffold("symfony", "twig-extension", "Twig Extension", "Twig functions and filters", "workspace-edit", "PriceExtension"),
		scaffold("symfony", "compiler-pass", "Compiler Pass", "Dependency-injection compiler pass", "workspace-edit", "CollectServicesPass"),
		scaffold("symfony", "kernel-test", "Kernel Test", "KernelTestCase integration test", "workspace-edit", "Container"),
		scaffold("symfony", "web-test", "Web Test", "WebTestCase functional test", "workspace-edit", "Storefront"),
		scaffold("symfony", "services-yaml", "YAML Service Configuration", "Autowiring service prototype", "workspace-edit", "services"),
		scaffold("symfony", "services-xml", "XML Service Configuration", "Autowiring service prototype", "workspace-edit", "services"),
		scaffold("symfony", "services-php", "PHP Service Configuration", "Fluent service configurator", "workspace-edit", "services"),
	}
}

func command(id, description, surface string, arguments []Field) ClientCommand {
	return ClientCommand{ID: id, Description: description, Surfaces: []string{surface}, Arguments: arguments}
}

func field(name, label, fieldType string, required bool) Field {
	return Field{Name: name, Label: label, Type: fieldType, Required: required}
}

func defaultField(name, label, fieldType string, value any) Field {
	return Field{Name: name, Label: label, Type: fieldType, Default: value}
}

func choiceField(name, label string, defaultValue string, choices ...string) Field {
	return Field{Name: name, Label: label, Type: "enum", Default: defaultValue, Choices: choices}
}

func fields(values ...Field) []Field { return values }

func uriArguments() []Field {
	return fields(field("fileUri", "Source file", "uri", true))
}

func classArguments() []Field {
	return fields(
		field("fileUri", "Source file", "uri", true),
		field("className", "Class name", "string", true),
	)
}

func selectionArguments() []Field {
	return fields(
		field("fileUri", "Source file", "uri", true),
		field("selectedText", "Selected text", "string", true),
	)
}

func scaffold(
	family, kind, label, description, workflow, placeholder string,
) ScaffoldDefinition {
	return scaffoldWithOptions(
		family, kind, label, description, workflow, placeholder, nil,
	)
}

func scaffoldWithOptions(
	family, kind, label, description, workflow, placeholder string,
	options []Field,
) ScaffoldDefinition {
	return ScaffoldDefinition{
		Family: family, Kind: kind, Label: label, Description: description,
		Workflow: workflow, NamePlaceholder: placeholder, Options: options,
	}
}
