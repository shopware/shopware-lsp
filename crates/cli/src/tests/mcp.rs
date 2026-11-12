//! The MCP server: tool listing, argument validation and tool results.

use serde_json::{Value, json};
use swlsp_lsp_protocol::{DocumentChange, WorkspaceEdit};

use super::common::{ENV_LOCK, McpClient, action_titles, path, temp_dir, test_options, write};
use crate::Runner;
use crate::mcp::McpRuntime;

fn output_schema_properties(tool: &Value) -> Vec<String> {
    let properties = tool["outputSchema"]["properties"]
        .as_object()
        .unwrap_or_else(|| panic!("{} has no output schema properties", tool["name"]));
    assert!(!properties.is_empty());
    properties.keys().cloned().collect()
}

#[test]
fn mcp_server_advertises_analysis_and_write_tools() {
    let root = temp_dir();
    let mut client = McpClient::connect(root.path());
    let instructions = client.initialize["instructions"]
        .as_str()
        .unwrap()
        .to_owned();
    for needle in [
        "always run shopware_entity_schema_bootstrap",
        r#"spec.definitionKind="mapping""#,
        r#"spec.definitionKind="extension""#,
        r#"spec.definitionKind="bulk-extension""#,
        "one bulkExtensions entry per indexed target",
        "bootstrap.definitionKinds",
        "collectMethodRaw",
        "spec.definitionBehavior",
        "spec.definitionMetadata",
        "*MethodRaw",
        r#"kind="hierarchy""#,
        "spec.inheritanceAware=true",
        "reverseInheritedProperty",
        "shopware_entity_schema_preview and shopware_entity_schema_apply",
        "always use shopware_scaffold",
    ] {
        assert!(
            instructions.contains(needle),
            "instructions lack {needle:?}"
        );
    }
    let leading: String = instructions.chars().take(512).collect();
    assert!(leading.contains("never handwrite entity PHP"));
    assert!(leading.contains(r#"kind="plugin""#));

    let tools = client.list_tools();
    let by_name = |name: &str| -> Value {
        tools
            .iter()
            .find(|tool| tool["name"] == name)
            .cloned()
            .unwrap_or_else(|| panic!("missing tool {name}"))
    };
    let mut registered: Vec<String> = tools
        .iter()
        .map(|tool| tool["name"].as_str().unwrap().to_owned())
        .collect();
    let mut catalog: Vec<String> = swlsp_projectconfig::MCP_TOOL_CATALOG
        .iter()
        .map(|tool| tool.id.to_owned())
        .collect();
    registered.sort();
    catalog.sort();
    assert_eq!(catalog, registered);
    for name in [
        "shopware_diagnostics",
        "shopware_code_actions",
        "shopware_apply_code_action",
        "shopware_hover",
        "shopware_definition",
        "shopware_references",
        "shopware_workspace_symbols",
        "shopware_explain_diagnostic",
        "shopware_scaffold_catalog",
        "shopware_scaffold",
        "shopware_entity_schema_bootstrap",
        "shopware_entity_schema_field_types",
        "shopware_entity_schema_search",
        "shopware_entity_schema_load",
        "shopware_entity_schema_preview",
        "shopware_entity_schema_apply",
        "shopware_entity_schema_reconcile",
    ] {
        assert!(
            by_name(name)["outputSchema"].is_object(),
            "{name} has no output schema"
        );
    }
    for (tool, property) in [
        ("shopware_diagnostics", "diagnostics"),
        ("shopware_code_actions", "actions"),
        ("shopware_apply_code_action", "diff"),
        ("shopware_hover", "hover"),
        ("shopware_definition", "locations"),
        ("shopware_references", "locations"),
        ("shopware_workspace_symbols", "symbols"),
        ("shopware_explain_diagnostic", "rules"),
        ("shopware_scaffold_catalog", "scaffolds"),
        ("shopware_scaffold", "diff"),
        ("shopware_entity_schema_bootstrap", "spec"),
        ("shopware_entity_schema_field_types", "fieldTypes"),
        ("shopware_entity_schema_preview", "revision"),
        ("shopware_entity_schema_apply", "diff"),
    ] {
        assert!(
            output_schema_properties(&by_name(tool)).contains(&property.to_owned()),
            "{tool} lacks {property}"
        );
    }
    assert!(
        !output_schema_properties(&by_name("shopware_entity_schema_bootstrap"))
            .contains(&"existing".to_owned())
    );
    let text = |name: &str, field: &str| by_name(name)[field].as_str().unwrap().to_owned();
    assert!(text("shopware_scaffold", "title").contains("plugin"));
    assert!(text("shopware_scaffold", "description").contains("Always use"));
    assert!(text("shopware_scaffold", "description").contains("kind=plugin"));
    for needle in [
        "Always call this first",
        "MappingEntityDefinition",
        "EntityExtension",
        "BulkEntityExtension",
        "kind=hierarchy",
        "inheritanceAware=true",
    ] {
        assert!(text("shopware_entity_schema_bootstrap", "description").contains(needle));
    }
    assert!(text("shopware_entity_schema_bootstrap", "title").contains("creating or editing"));
    assert!(text("shopware_entity_schema_field_types", "description").contains("content.json"));
    assert!(
        text("shopware_entity_schema_search", "description")
            .contains("Use after entity-schema bootstrap")
    );
    assert!(
        text("shopware_entity_schema_load", "description").contains("edit an existing DAL entity")
    );
    assert!(
        text("shopware_entity_schema_preview", "description")
            .contains("Required after bootstrap/load")
    );
    assert!(
        text("shopware_entity_schema_apply", "description").contains("never reproduce the preview")
    );
    assert!(text("shopware_entity_schema_apply", "description").contains("without refreshing"));
    let preview_input = by_name("shopware_entity_schema_preview")["inputSchema"].to_string();
    for key in [
        "inheritanceAware",
        "associationInherited",
        "associationAutoload",
        "writeProtected",
        "writeProtectedScopes",
        "associationWriteProtected",
        "translationWriteProtected",
        "translationInherited",
        "reverseInheritedProperty",
        "jsonPropertyMappingExpression",
        "apiAwareSources",
        "conditionalAssociation",
        "minimumAdditionalArguments",
        "bulkExtensions",
        "definitionBehavior",
        "definitionMetadata",
        "parentDefinitionClass",
        "overrideDefaultFields",
        "overrideBaseFields",
        "hydratorClass",
    ] {
        assert!(
            preview_input.contains(&format!("\"{key}\"")),
            "preview schema lacks {key}"
        );
    }
    assert!(
        by_name("shopware_entity_schema_load")["inputSchema"]
            .to_string()
            .contains("\"definitionKind\"")
    );
    assert_eq!(
        by_name("shopware_diagnostics")["annotations"]["readOnlyHint"],
        true
    );
    assert_eq!(
        by_name("shopware_apply_code_action")["annotations"]["readOnlyHint"],
        false
    );
    assert_eq!(
        by_name("shopware_apply_code_action")["annotations"]["destructiveHint"],
        true
    );
}

#[test]
fn mcp_configuration_disables_exact_tools() {
    let root = temp_dir();
    write(
        &path(root.path(), ".config/shopware/lsp.yaml"),
        r#"{"version": 1, "mcp": {"tools": {"shopware_hover": false, "shopware_scaffold": false}}}"#,
    );
    let names = McpClient::connect(root.path()).tool_names();
    assert!(!names.contains(&"shopware_hover".to_owned()));
    assert!(!names.contains(&"shopware_scaffold".to_owned()));
    assert!(names.contains(&"shopware_diagnostics".to_owned()));
    assert!(names.contains(&"shopware_scaffold_catalog".to_owned()));
}

/// Connects with `SHOPWARE_LSP_EDITOR_CONFIGURATION` set for the startup.
fn connect_with_editor_configuration(root: &std::path::Path, configuration: &str) -> McpClient {
    let _guard = ENV_LOCK.write().unwrap_or_else(|p| p.into_inner());
    // SAFETY: every other run and MCP startup is excluded by the lock.
    unsafe { std::env::set_var("SHOPWARE_LSP_EDITOR_CONFIGURATION", configuration) };
    let client = std::panic::catch_unwind(|| McpClient::connect_unlocked(root));
    unsafe { std::env::remove_var("SHOPWARE_LSP_EDITOR_CONFIGURATION") };
    client.unwrap_or_else(|panic| std::panic::resume_unwind(panic))
}

#[test]
fn mcp_configuration_uses_project_and_editor_layers() {
    let root = temp_dir();
    write(
        &swlsp_projectconfig::path(root.path().to_str().unwrap()),
        r#"{
        "version": 1,
        "mcp": {"tools": {"shopware_scaffold": false}}
    }"#,
    );
    let mut client = connect_with_editor_configuration(
        root.path(),
        r#"{"mcp": {"tools": {"shopware_scaffold": true, "shopware_hover": false}}}"#,
    );
    let names = client.tool_names();
    assert!(names.contains(&"shopware_scaffold".to_owned()));
    assert!(!names.contains(&"shopware_hover".to_owned()));
}

#[test]
fn mcp_editor_configuration_reads_diagnostic_overrides() {
    // The decoded overrides are observed through the server: the tool switch and a diagnostics
    // override that disables a file that otherwise reports a finding.
    let root = temp_dir();
    let relative = "custom/plugins/Test/src/Resources/app/administration/src/component.js";
    write(
        &path(root.path(), relative),
        "this.$tc('translation.key');\n",
    );
    let mut client = connect_with_editor_configuration(
        root.path(),
        r#"{
        "mcp": {"tools": {"shopware_scaffold": false}},
        "diagnostics": {
            "overrides": [{"files":["custom/plugins/Test/**"],"enabled":false}]
        }
    }"#,
    );
    assert!(
        !client
            .tool_names()
            .contains(&"shopware_scaffold".to_owned())
    );
    let diagnostics = client.call_ok("shopware_diagnostics", json!({"path": relative}));
    assert_eq!(diagnostics["total"], 0, "{diagnostics}");
}

#[test]
fn mcp_scaffold_catalog_and_shopware_preview_write() {
    let root = temp_dir();
    let mut client = McpClient::connect(root.path());
    let catalog = client.call_ok("shopware_scaffold_catalog", json!({}));
    assert_eq!(catalog["entitySchema"], true);
    let kinds: Vec<String> = catalog["scaffolds"]
        .as_array()
        .unwrap()
        .iter()
        .map(|item| {
            format!(
                "{}:{}",
                item["family"].as_str().unwrap(),
                item["kind"].as_str().unwrap()
            )
        })
        .collect();
    assert!(kinds.contains(&"shopware:app-cms".to_owned()));
    assert!(kinds.contains(&"symfony:controller".to_owned()));

    let mut arguments =
        json!({"family": "shopware", "kind": "app-cms", "directory": ".", "name": "cms"});
    let preview = client.call_ok("shopware_scaffold", arguments.clone());
    assert_eq!(preview["applied"], false);
    assert!(
        preview["diff"]
            .as_str()
            .unwrap()
            .contains("Resources/cms.xml")
    );
    assert!(!root.path().join("Resources/cms.xml").exists());

    arguments["write"] = json!(true);
    let applied = client.call_ok("shopware_scaffold", arguments);
    assert_eq!(applied["applied"], true);
    assert_eq!(applied["primaryFile"], "Resources/cms.xml");
    assert!(root.path().join("Resources/cms.xml").exists());
}

#[test]
fn mcp_symfony_scaffold_writes_through_production_command() {
    let root = temp_dir();
    std::fs::create_dir_all(root.path().join("src/Controller")).unwrap();
    write(
        &path(root.path(), "composer.json"),
        r#"{
        "name": "acme/example",
        "autoload": {"psr-4": {"App\\": "src/"}}
    }"#,
    );
    let mut client = McpClient::connect(root.path());
    let output = client.call_ok(
        "shopware_scaffold",
        json!({"family": "symfony", "kind": "controller", "directory": "src/Controller", "name": "Product", "write": true}),
    );
    assert_eq!(output["applied"], true);
    assert_eq!(
        output["primaryFile"],
        "src/Controller/ProductController.php"
    );
    let content =
        std::fs::read_to_string(root.path().join("src/Controller/ProductController.php")).unwrap();
    assert!(content.contains("namespace App\\Controller;"));
}

fn write_plugin_composer(plugin: &std::path::Path, name: &str, namespace: &str, class: &str) {
    write(
        plugin.join("composer.json").to_str().unwrap(),
        &format!(
            r#"{{
        "name": "{name}",
        "type": "shopware-platform-plugin",
        "require": {{"shopware/core": "~6.7"}},
        "autoload": {{"psr-4": {{"{namespace}\\": "src/"}}}},
        "extra": {{"shopware-plugin-class": "{class}"}}
    }}"#
        ),
    );
}

/// Round-trips a field through the typed `FieldSpec`, so every member that is
/// always serialized is present in the expectation.
fn field(value: Value) -> Value {
    let mut field = value;
    field["editable"] = json!(true);
    let typed: swlsp_shopware_entityschema::FieldSpec = serde_json::from_value(field).unwrap();
    serde_json::to_value(typed).unwrap()
}

fn typed_spec(spec: Value) -> Value {
    let typed: swlsp_shopware_entityschema::EntitySpec = serde_json::from_value(spec).unwrap();
    serde_json::to_value(typed).unwrap()
}

#[test]
fn mcp_entity_schema_bootstrap_preview_and_apply() {
    let root = temp_dir();
    let plugin = root.path().join("custom/plugins/Example");
    let directory = plugin.join("src/Content/Example");
    std::fs::create_dir_all(&directory).unwrap();
    write_plugin_composer(
        &plugin,
        "acme/example",
        "Acme\\\\Example",
        "Acme\\\\Example\\\\Example",
    );
    let mut client = McpClient::connect(root.path());
    let dir = "custom/plugins/Example/src/Content/Example";

    let bootstrap = client.call_ok(
        "shopware_entity_schema_bootstrap",
        json!({"directory": dir}),
    );
    assert_eq!(bootstrap["plugin"]["rootUri"], "custom/plugins/Example");
    assert_eq!(bootstrap["spec"]["directoryUri"], dir);
    let field_types = bootstrap["fieldTypes"].as_array().unwrap();
    assert!(!field_types.is_empty());
    assert!(field_types.iter().any(|item| item["kind"] == "enum"));
    assert!(
        bootstrap["nextAction"]
            .as_str()
            .unwrap()
            .contains("Do not inspect content.json")
    );
    let bootstrap_json = bootstrap.to_string();
    assert!(
        bootstrap_json.len() < 12_000,
        "bootstrap is {} bytes",
        bootstrap_json.len()
    );
    assert!(!bootstrap_json.contains("\"existing\""));
    assert!(!bootstrap_json.contains("\"template\""));

    let list = client.call_ok(
        "shopware_entity_schema_field_types",
        json!({"directory": dir, "definitionKind": "entity", "query": "custom fields"}),
    );
    let list_types = list["fieldTypes"].as_array().unwrap();
    assert_eq!(list_types.len(), 1);
    assert_eq!(list_types[0]["id"], "specialized:CustomFields");
    assert!(list_types[0].get("template").is_none());
    assert!(list["nextAction"].as_str().unwrap().contains("exact id"));

    let detail = client.call_ok(
        "shopware_entity_schema_field_types",
        json!({"directory": dir, "id": "specialized:CustomFields"}),
    );
    let detail_types = detail["fieldTypes"].as_array().unwrap();
    assert_eq!(detail_types.len(), 1);
    let template = &detail_types[0]["template"];
    assert_eq!(template["id"], "replace-me");
    assert_eq!(
        template["implementation"]["class"],
        "Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\CustomFields"
    );
    assert!(
        detail["nextAction"]
            .as_str()
            .unwrap()
            .contains("Copy template")
    );

    let scalar = client.call_ok(
        "shopware_entity_schema_field_types",
        json!({"directory": dir, "id": "string"}),
    );
    let scalar_types = scalar["fieldTypes"].as_array().unwrap();
    assert_eq!(scalar_types.len(), 1);
    assert_eq!(scalar_types[0]["template"]["kind"], "string");
    assert_eq!(scalar_types[0]["template"]["id"], "replace-me");
    assert!(scalar_types[0]["template"].get("translated").is_none());
    assert!(
        scalar_types[0]["usage"]
            .as_str()
            .unwrap()
            .contains("translated=true")
    );

    let unknown = client.call_tool(
        "shopware_entity_schema_field_types",
        json!({"directory": dir, "id": "not-a-field"}),
    );
    assert!(unknown.is_error);
    assert!(
        unknown
            .text
            .contains("field type \"not-a-field\" is unavailable"),
        "{}",
        unknown.text
    );

    let mut spec = bootstrap["spec"].clone();
    spec["fields"] = json!([
        field(json!({"id": "id", "kind": "id"})),
        field(json!({"id": "version", "kind": "version"})),
        field(json!({"id": "hierarchy", "kind": "hierarchy", "propertyName": "children"})),
        field(
            json!({"id": "name", "kind": "string", "propertyName": "name", "storageName": "name", "inherited": true})
        ),
    ]);
    spec["inheritanceAware"] = json!(true);
    spec["definitionBehavior"] = json!({
        "parentDefinitionClass": "Shopware\\Core\\Content\\Product\\ProductDefinition",
        "versionAware": true,
        "restrictDeleteMetaProperties": ["id"],
    });
    spec["definitionMetadata"] = json!({
        "since": "6.7.1.0",
        "defaults": [{"propertyName": "name", "valueExpression": "'default'"}],
        "childDefaults": [{"propertyName": "name", "valueExpression": "'child'"}],
    });

    let preview = client.call_ok("shopware_entity_schema_preview", json!({"spec": spec}));
    let revision = preview["revision"].as_str().unwrap().to_owned();
    assert!(!revision.is_empty());
    assert!(preview.get("issues").is_none(), "{preview}");
    assert!(!preview["files"].as_array().unwrap().is_empty());
    assert!(preview["migrationTimestamp"].as_i64().unwrap() != 0);
    assert!(revision.contains(':'));
    assert_eq!(preview["status"], "ready");
    assert_eq!(preview["readyToApply"], true);
    assert!(
        preview["nextAction"]
            .as_str()
            .unwrap()
            .contains("Do not preview again")
    );
    let preview_json = preview.to_string();
    assert!(preview_json.len() < 8_000);
    assert!(!preview_json.contains("\"after\""));

    let applied = client.call_ok(
        "shopware_entity_schema_apply",
        json!({"spec": spec, "revision": revision}),
    );
    assert_eq!(applied["applied"], true);
    assert!(!applied["snapshotId"].as_str().unwrap().is_empty());
    assert!(applied["files"].as_array().unwrap().contains(&json!(
        "custom/plugins/Example/src/Content/Example/ExampleDefinition.php"
    )));
    let definition = std::fs::read_to_string(directory.join("ExampleDefinition.php")).unwrap();
    for needle in [
        "new ParentFkField(ExampleDefinition::class)",
        "new ReferenceVersionField(ExampleDefinition::class, 'parent_version_id')",
        "new ParentAssociationField(ExampleDefinition::class, 'id')",
        "new ChildrenAssociationField(ExampleDefinition::class)",
        "public function isInheritanceAware(): bool",
        "protected function getParentDefinitionClass(): ?string",
        "public function isVersionAware(): bool",
        "public function since(): ?string",
        "public function getDefaults(): array",
        "public function getChildDefaults(): array",
        "public function getRestrictDeleteMetaFields(): FieldCollection",
        "new StringField('name', 'name'))->addFlags(new Inherited())",
    ] {
        assert!(definition.contains(needle), "definition lacks {needle}");
    }

    let mut loaded = client.call_ok(
        "shopware_entity_schema_load",
        json!({"path": "custom/plugins/Example/src/Content/Example/ExampleDefinition.php"}),
    );
    assert_eq!(loaded["mode"], "edit");
    assert_eq!(
        loaded["definitionBehavior"]["parentDefinitionClass"],
        spec["definitionBehavior"]["parentDefinitionClass"]
    );
    assert_eq!(
        loaded["definitionMetadata"]["since"],
        spec["definitionMetadata"]["since"]
    );
    assert_eq!(
        loaded["definitionMetadata"]["defaults"],
        spec["definitionMetadata"]["defaults"]
    );
    assert_eq!(
        loaded["definitionMetadata"]["childDefaults"],
        spec["definitionMetadata"]["childDefaults"]
    );

    let round_trip = client.call_ok("shopware_entity_schema_preview", json!({"spec": loaded}));
    assert_eq!(round_trip["drift"], false, "{}", round_trip["driftMessage"]);
    assert!(round_trip.get("issues").is_none(), "{round_trip}");

    loaded["definitionKind"] = json!("mapping");
    loaded.as_object_mut().unwrap().remove("translation");
    loaded["inheritanceAware"] = json!(false);
    loaded["fields"] = json!([field(json!({
        "id": "id", "kind": "binary-id", "propertyName": "id", "storageName": "id",
        "required": true, "primary": true,
    }))]);
    let transition = client.call_ok("shopware_entity_schema_preview", json!({"spec": loaded}));
    assert!(transition.get("issues").is_none(), "{transition}");
    assert_eq!(transition["status"], "requires-destructive-confirmation");
    assert_eq!(transition["destructive"], true);
    client.call_ok(
        "shopware_entity_schema_apply",
        json!({"spec": loaded, "revision": transition["revision"], "allowDestructive": true}),
    );
    assert!(!directory.join("ExampleEntity.php").exists());
    assert!(!directory.join("ExampleCollection.php").exists());
    let definition = std::fs::read_to_string(directory.join("ExampleDefinition.php")).unwrap();
    assert!(definition.contains("extends MappingEntityDefinition"));
}

#[test]
fn mcp_entity_schema_creates_mapping_definition() {
    let root = temp_dir();
    let plugin = root.path().join("custom/plugins/Example");
    let directory = plugin.join("src/Content/ProductTag");
    std::fs::create_dir_all(&directory).unwrap();
    write_plugin_composer(
        &plugin,
        "acme/example",
        "Acme\\\\Example",
        "Acme\\\\Example\\\\Example",
    );
    let mut client = McpClient::connect(root.path());
    let bootstrap = client.call_ok(
        "shopware_entity_schema_bootstrap",
        json!({"directory": "custom/plugins/Example/src/Content/ProductTag"}),
    );
    let mut spec = bootstrap["spec"].clone();
    spec["definitionKind"] = json!("mapping");
    spec["fields"] = json!([
        field(
            json!({"id": "product", "kind": "foreign-key", "propertyName": "productId", "storageName": "product_id", "targetDefinitionClass": "Shopware\\Core\\Content\\Product\\ProductDefinition", "targetEntityName": "product", "required": true, "primary": true})
        ),
        field(
            json!({"id": "tag", "kind": "foreign-key", "propertyName": "tagId", "storageName": "tag_id", "targetDefinitionClass": "Shopware\\Core\\System\\Tag\\TagDefinition", "targetEntityName": "tag", "required": true, "primary": true})
        ),
    ]);
    let preview = client.call_ok("shopware_entity_schema_preview", json!({"spec": spec}));
    assert_eq!(preview["status"], "ready", "{preview}");
    assert!(preview.get("issues").is_none());
    assert_eq!(preview["files"].as_array().unwrap().len(), 5);
    client.call_ok(
        "shopware_entity_schema_apply",
        json!({"spec": spec, "revision": preview["revision"]}),
    );
    let content = std::fs::read_to_string(directory.join("ProductTagDefinition.php")).unwrap();
    assert!(content.contains("extends MappingEntityDefinition"));
    assert!(!content.contains("getEntityClass"));
    assert!(!directory.join("ProductTagEntity.php").exists());
    assert!(!directory.join("ProductTagCollection.php").exists());
}

fn write_definition(directory: &std::path::Path, namespace: &str, class: &str, entity: &str) {
    write(
        directory.join(format!("{class}.php")).to_str().unwrap(),
        &format!(
            "<?php declare(strict_types=1);\nnamespace {namespace};\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityDefinition;\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\Field\\IdField;\nuse Shopware\\Core\\Framework\\DataAbstractionLayer\\FieldCollection;\nclass {class} extends EntityDefinition {{\n    public const ENTITY_NAME = '{entity}';\n    protected function defineFields(): FieldCollection {{ return new FieldCollection([new IdField('id', 'id')]); }}\n}}"
        ),
    );
}

#[test]
fn mcp_entity_schema_creates_entity_extension() {
    let root = temp_dir();
    let plugin = root.path().join("custom/plugins/Example");
    let extension_directory = plugin.join("src/Extension/Product");
    std::fs::create_dir_all(&extension_directory).unwrap();
    write_plugin_composer(
        &plugin,
        "acme/example",
        "Acme\\\\Example",
        "Acme\\\\Example\\\\Example",
    );
    write_definition(
        &plugin.join("src/Content/Product"),
        "Acme\\Example\\Content\\Product",
        "ProductDefinition",
        "acme_product",
    );
    let mut client = McpClient::connect(root.path());
    let bootstrap = client.call_ok(
        "shopware_entity_schema_bootstrap",
        json!({"directory": "custom/plugins/Example/src/Extension/Product"}),
    );
    let mut spec = bootstrap["spec"].clone();
    spec["definitionKind"] = json!("extension");
    spec["className"] = json!("ProductCustom");
    spec["definitionClass"] = json!("");
    spec["entityName"] = json!("acme_product");
    spec["extendedDefinitionClass"] = json!("Acme\\Example\\Content\\Product\\ProductDefinition");
    spec["fields"] = json!([field(json!({
        "id": "parent", "kind": "many-to-one", "propertyName": "parent", "foreignKeyPropertyName": "parentId",
        "storageName": "acme_parent_id", "targetDefinitionClass": "Acme\\Example\\Content\\Product\\ProductDefinition",
        "targetEntityClass": "Acme\\Example\\Content\\Product\\ProductEntity", "targetCollectionClass": "Acme\\Example\\Content\\Product\\ProductCollection",
        "targetEntityName": "acme_product", "referenceField": "id", "referenceStorageName": "id", "deleteBehavior": "set-null",
    }))]);
    spec["indexes"] = json!([{"name": "uniq.acme_product.acme_parent_id_id", "kind": "unique", "columns": ["acme_parent_id", "id"]}]);
    let preview = client.call_ok("shopware_entity_schema_preview", json!({"spec": spec}));
    assert_eq!(preview["status"], "ready", "{preview}");
    assert!(preview.get("issues").is_none());
    client.call_ok(
        "shopware_entity_schema_apply",
        json!({"spec": spec, "revision": preview["revision"]}),
    );
    let source =
        std::fs::read_to_string(extension_directory.join("ProductCustomExtension.php")).unwrap();
    assert!(source.contains("extends EntityExtension"));
    assert!(source.contains("return ProductDefinition::ENTITY_NAME"));
    let migrations: Vec<_> = std::fs::read_dir(plugin.join("src/Migration"))
        .unwrap()
        .filter_map(Result::ok)
        .filter(|entry| {
            entry
                .path()
                .extension()
                .is_some_and(|extension| extension == "php")
        })
        .collect();
    assert_eq!(migrations.len(), 1);
    let migration = std::fs::read_to_string(migrations[0].path()).unwrap();
    assert!(migration.contains(
        "ADD UNIQUE INDEX `uniq.acme_product.acme_parent_id_id` (`acme_parent_id`, `id`)"
    ));
    assert!(!extension_directory.join("ProductCustomEntity.php").exists());
    assert!(
        !extension_directory
            .join("ProductCustomCollection.php")
            .exists()
    );
}

#[test]
fn mcp_entity_schema_creates_bulk_entity_extension() {
    let root = temp_dir();
    let plugin = root.path().join("custom/plugins/BulkExample");
    let extension_directory = plugin.join("src/Extension/Catalog");
    std::fs::create_dir_all(&extension_directory).unwrap();
    write_plugin_composer(
        &plugin,
        "acme/bulk-example",
        "Acme\\\\BulkExample",
        "Acme\\\\BulkExample\\\\BulkExample",
    );
    write_definition(
        &plugin.join("src/Content/Product"),
        "Acme\\BulkExample\\Content\\Product",
        "ProductDefinition",
        "acme_product",
    );
    write_definition(
        &plugin.join("src/Content/Category"),
        "Acme\\BulkExample\\Content\\Category",
        "CategoryDefinition",
        "acme_category",
    );
    let mut client = McpClient::connect(root.path());
    let bootstrap = client.call_ok(
        "shopware_entity_schema_bootstrap",
        json!({"directory": "custom/plugins/BulkExample/src/Extension/Catalog"}),
    );
    let product = "Acme\\BulkExample\\Content\\Product\\ProductDefinition";
    let category = "Acme\\BulkExample\\Content\\Category\\CategoryDefinition";
    let to_one = |id: &str, property: &str, storage: &str, target: &str, entity: &str| {
        let base = target.trim_end_matches("Definition");
        field(json!({
            "id": id, "kind": "many-to-one", "propertyName": property,
            "foreignKeyPropertyName": format!("{property}Id"), "storageName": storage,
            "targetDefinitionClass": target, "targetEntityClass": format!("{base}Entity"),
            "targetCollectionClass": format!("{base}Collection"), "targetEntityName": entity,
            "referenceField": "id", "referenceStorageName": "id", "deleteBehavior": "set-null",
        }))
    };
    let mut spec = bootstrap["spec"].clone();
    spec["definitionKind"] = json!("bulk-extension");
    spec["className"] = json!("Catalog");
    spec["definitionClass"] = json!("");
    spec["entityName"] = json!("");
    spec["fields"] = json!([]);
    spec["indexes"] = json!([]);
    spec["bulkExtensions"] = json!([
        {"id": "product", "entityName": "acme_product", "extendedDefinitionClass": product,
         "fields": [to_one("category", "category", "acme_category_id", category, "acme_category")]},
        {"id": "category", "entityName": "acme_category", "extendedDefinitionClass": category,
         "fields": [to_one("product", "product", "acme_product_id", product, "acme_product")]},
    ]);
    let spec = typed_spec(spec);
    let preview = client.call_ok("shopware_entity_schema_preview", json!({"spec": spec}));
    assert_eq!(preview["status"], "ready", "{preview}");
    assert!(preview.get("issues").is_none());
    let mut added: Vec<String> = preview["diff"]["addedColumns"]
        .as_array()
        .unwrap()
        .iter()
        .map(|column| column.as_str().unwrap().to_owned())
        .collect();
    added.sort();
    assert_eq!(
        added,
        vec![
            "acme_category.acme_product_id",
            "acme_product.acme_category_id"
        ]
    );
    client.call_ok(
        "shopware_entity_schema_apply",
        json!({"spec": spec, "revision": preview["revision"]}),
    );
    let source =
        std::fs::read_to_string(extension_directory.join("CatalogBulkEntityExtension.php"))
            .unwrap();
    assert!(source.contains("extends BulkEntityExtension"));
    assert!(source.contains("yield ProductDefinition::ENTITY_NAME"));
    assert!(source.contains("yield CategoryDefinition::ENTITY_NAME"));
    let service =
        std::fs::read_to_string(plugin.join("src/Resources/config/services.yaml")).unwrap();
    assert!(service.contains("shopware.bulk.entity.extension"));
    assert!(!extension_directory.join("CatalogEntity.php").exists());
    assert!(!extension_directory.join("CatalogCollection.php").exists());
    let loaded = client.call_ok(
        "shopware_entity_schema_load",
        json!({"path": "custom/plugins/BulkExample/src/Extension/Catalog/CatalogBulkEntityExtension.php"}),
    );
    assert_eq!(loaded["definitionKind"], "bulk-extension");
    assert_eq!(loaded["bulkExtensions"].as_array().unwrap().len(), 2);
}

#[test]
fn mcp_diagnostics_and_code_action_use_production_lsp() {
    let root = temp_dir();
    let relative = "custom/plugins/Test/src/Resources/app/administration/src/component.js";
    let file = path(root.path(), relative);
    write(&file, "this.$tc('translation.key');\n");
    let mut client = McpClient::connect(root.path());
    let diagnostics = client.call_ok("shopware_diagnostics", json!({"path": relative}));
    assert!(diagnostics["total"].as_i64().unwrap() >= 1);
    let found = diagnostics["diagnostics"]
        .as_array()
        .unwrap()
        .iter()
        .find(|item| item["code"] == "admin.vue-i18n.tc-deprecated");
    let found =
        found.unwrap_or_else(|| panic!("expected admin.vue-i18n.tc-deprecated in {diagnostics}"));
    assert_eq!(found["range"]["start"]["line"], 1);
    assert_eq!(found["range"]["start"]["column"], 6);

    let args = json!({"path": relative, "line": 1, "column": 6, "kind": "quickfix"});
    let actions = client.call_ok("shopware_code_actions", args.clone());
    let title = "Replace deprecated $tc() with $t()";
    assert!(
        action_titles(&actions).contains(&title.to_owned()),
        "{actions}"
    );
    let mut apply_args = args;
    apply_args["title"] = json!(title);
    let applied = client.call_ok("shopware_apply_code_action", apply_args);
    assert_eq!(applied["applied"], true);
    assert!(
        applied["diff"]
            .as_str()
            .unwrap()
            .contains("+this.$t('translation.key');")
    );
    assert_eq!(
        std::fs::read_to_string(&file).unwrap(),
        "this.$t('translation.key');\n"
    );
}

#[test]
fn mcp_twig_versioning_quick_fix_uses_production_index() {
    let root = temp_dir();
    write(
        &path(
            root.path(),
            "src/Storefront/Resources/views/storefront/page/example.html.twig",
        ),
        "{% block content %}upstream{% endblock %}\n",
    );
    let relative = "custom/plugins/Test/src/Resources/views/storefront/page/example.html.twig";
    let override_path = path(root.path(), relative);
    write(
        &override_path,
        "{% sw_extends '@Storefront/storefront/page/example.html.twig' %}\n{# shopware-block: deadbeef@6.6.0.0 #}\n{% block content %}local{% endblock %}\n",
    );
    let mut client = McpClient::connect(root.path());
    let diagnostics = client.call_ok("shopware_diagnostics", json!({"path": relative}));
    let items = diagnostics["diagnostics"].as_array().unwrap();
    assert!(
        items
            .iter()
            .any(|item| item["code"] == "twig.versioning.outdated"),
        "{diagnostics}"
    );
    assert!(
        !items
            .iter()
            .any(|item| item["code"] == "twig.versioning.comment_missing")
    );

    let args = json!({"path": relative, "line": 3, "column": 10, "kind": "quickfix"});
    let actions = client.call_ok("shopware_code_actions", args.clone());
    let title = "Shopware: Update Twig block version comment";
    assert!(
        action_titles(&actions).contains(&title.to_owned()),
        "{actions}"
    );
    let mut apply_args = args;
    apply_args["title"] = json!(title);
    let applied = client.call_ok("shopware_apply_code_action", apply_args);
    assert_eq!(applied["applied"], true);
    assert!(
        applied["diff"]
            .as_str()
            .unwrap()
            .contains("-{# shopware-block: deadbeef@6.6.0.0 #}")
    );
    let content = std::fs::read_to_string(&override_path).unwrap();
    assert!(!content.contains("deadbeef"));
    assert!(content.contains("{# shopware-block: "));
}

#[test]
fn mcp_explains_diagnostic_codes_and_inspections() {
    let root = temp_dir();
    let mut client = McpClient::connect(root.path());

    let explained = client.call_ok(
        "shopware_explain_diagnostic",
        json!({"code": "shopware.php.route.acl_missing"}),
    );
    let [rule] = explained["rules"].as_array().unwrap().as_slice() else {
        panic!("expected one rule: {explained}");
    };
    assert_eq!(rule["code"], "shopware.php.route.acl_missing");
    assert_eq!(rule["inspection"], "shopware.php.semantic");
    assert_eq!(rule["defaultSeverity"], "warning");
    assert_eq!(rule["enabledByDefault"], true);
    assert!(rule["fix"].as_str().unwrap().contains("_acl"), "{rule}");
    assert!(
        rule["exceptions"].as_str().unwrap().contains("GET-only"),
        "{rule}"
    );
    assert!(
        rule["optOut"].as_str().unwrap().contains("'_acl' => []"),
        "{rule}"
    );
    assert_eq!(
        explained["configuration"]["file"],
        ".config/shopware/lsp.yaml"
    );

    // An inspection ID explains all of its codes.
    let inspection = client.call_ok(
        "shopware_explain_diagnostic",
        json!({"code": "twig.markup"}),
    );
    let codes: Vec<&str> = inspection["rules"]
        .as_array()
        .unwrap()
        .iter()
        .map(|rule| rule["code"].as_str().unwrap())
        .collect();
    assert!(codes.contains(&"twig.html.button_type"), "{codes:?}");
    assert!(codes.len() > 1);
    for rule in inspection["rules"].as_array().unwrap() {
        assert!(!rule["fix"].as_str().unwrap().is_empty(), "{rule}");
    }

    let unknown = client.call_tool(
        "shopware_explain_diagnostic",
        json!({"code": "shopware.php.route.acl"}),
    );
    assert!(unknown.is_error);
    assert!(
        unknown.text.contains("shopware.php.route.acl_missing"),
        "{}",
        unknown.text
    );
}

/// A client starts the server for every project it opens. Outside a
/// Shopware or Symfony project it must still answer, with no tools and the
/// reason why.
#[test]
fn mcp_starts_inactive_outside_a_supported_project() {
    let root = temp_dir();
    write(&path(root.path(), "README.md"), "not a shop");
    let mut client = McpClient::connect_guarded(root.path());
    let instructions = client.initialize["instructions"]
        .as_str()
        .unwrap()
        .to_owned();
    assert!(
        instructions.starts_with("The Shopware MCP server is inactive"),
        "{instructions}"
    );
    assert!(
        instructions.contains("no Shopware or Symfony project markers found"),
        "{instructions}"
    );
    assert!(client.initialize["capabilities"]["tools"].is_null());
    assert_eq!(client.tool_names(), Vec::<String>::new());
    let response = client.request(
        "tools/call",
        json!({"name": "shopware_diagnostics", "arguments": {"path": "README.md"}}),
    );
    assert!(
        response["error"]["message"]
            .as_str()
            .unwrap()
            .contains("unknown tool"),
        "{response}"
    );
}

#[test]
fn mcp_is_active_in_a_supported_project_without_the_override() {
    let root = temp_dir();
    write(
        &path(root.path(), ".config/shopware/lsp.yaml"),
        "version: 1\n",
    );
    let mut client = McpClient::connect_guarded(root.path());
    assert!(
        client
            .tool_names()
            .contains(&"shopware_diagnostics".to_owned())
    );
    assert!(
        !client.initialize["instructions"]
            .as_str()
            .unwrap()
            .contains("inactive")
    );
}

/// A configuration that cannot be read is the project's problem to fix, not
/// a reason for the client to show a failed server.
#[test]
fn mcp_starts_inactive_with_an_invalid_configuration() {
    let root = temp_dir();
    write(
        &path(root.path(), ".config/shopware/lsp.yaml"),
        "version: 1\nmcp:\n  tools:\n    shopware_no_such_tool: true\n",
    );
    let mut client = McpClient::connect_guarded(root.path());
    let instructions = client.initialize["instructions"]
        .as_str()
        .unwrap()
        .to_owned();
    assert!(
        instructions.contains("invalid project configuration"),
        "{instructions}"
    );
    assert_eq!(client.tool_names(), Vec::<String>::new());

    let root = temp_dir();
    write(
        &path(root.path(), ".config/shopware/lsp.yaml"),
        "version: 1\n",
    );
    let mut client = {
        let _guard = ENV_LOCK.write().unwrap_or_else(|p| p.into_inner());
        // SAFETY: every other run and MCP startup is excluded by the lock.
        unsafe { std::env::set_var("SHOPWARE_LSP_EDITOR_CONFIGURATION", "[]") };
        let client = std::panic::catch_unwind(|| McpClient::connect_unlocked(root.path()));
        unsafe { std::env::remove_var("SHOPWARE_LSP_EDITOR_CONFIGURATION") };
        client.unwrap_or_else(|panic| std::panic::resume_unwind(panic))
    };
    let instructions = client.initialize["instructions"]
        .as_str()
        .unwrap()
        .to_owned();
    assert!(
        instructions.contains("decode MCP editor configuration"),
        "{instructions}"
    );
    assert_eq!(client.tool_names(), Vec::<String>::new());
}

#[test]
fn mcp_rejects_paths_outside_workspace_before_indexing() {
    let root = temp_dir();
    let mut client = McpClient::connect(root.path());
    let outside = root.path().parent().unwrap().join("outside.php");
    let result = client.call_tool(
        "shopware_diagnostics",
        json!({"path": outside.to_str().unwrap()}),
    );
    assert!(result.is_error);
    assert!(
        result.text.contains("outside workspace root"),
        "{}",
        result.text
    );
}

#[test]
fn mcp_rejects_unsafe_workspace_edits() {
    let root = temp_dir();
    let root_path = root.path().to_str().unwrap().to_owned();
    let runner = Runner::new(test_options());
    let effective = swlsp_projectconfig::resolve(&Default::default(), &Default::default());
    let runtime = McpRuntime::new(&runner, root_path, None, effective, None);
    let outside = swlsp_uriutil::file_uri(
        root.path()
            .parent()
            .unwrap()
            .join("outside.php")
            .to_str()
            .unwrap(),
    );

    let mut edit = WorkspaceEdit::default();
    edit.changes.insert(outside.clone(), Vec::new());
    let error = runtime.validate_workspace_edit(&edit).unwrap_err();
    assert!(error.message.contains("outside workspace root"), "{error}");

    let change = |kind: &str| WorkspaceEdit {
        document_changes: vec![DocumentChange {
            kind: kind.into(),
            uri: outside.clone(),
            ..DocumentChange::default()
        }],
        ..WorkspaceEdit::default()
    };
    let error = runtime
        .validate_workspace_edit(&change("delete"))
        .unwrap_err();
    assert!(error.message.contains("outside workspace root"), "{error}");
    let error = runtime
        .validate_workspace_edit(&change("rename"))
        .unwrap_err();
    assert!(
        error
            .message
            .contains("unsupported workspace resource operation"),
        "{error}"
    );
}

#[test]
fn compact_entity_schema_diff_keeps_table_rename_questions() {
    use swlsp_shopware_entityschema::{EntityRenameQuestion, RenameCandidate, SchemaDiff};
    let questions = vec![EntityRenameQuestion {
        added: "acme_new".into(),
        candidates: vec![RenameCandidate {
            from: "acme_old".into(),
            score: 100,
        }],
    }];
    let summary = crate::mcp::scaffold::compact_diff(&SchemaDiff {
        entity_rename_questions: questions.clone(),
        ..SchemaDiff::default()
    });
    assert_eq!(summary.entity_rename_questions, questions);
}
