//! Shopware extension scaffolds producing atomic create-file edits (port of
//! Go `shopware_scaffold.go`).

use std::collections::{BTreeMap, HashSet};

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};
use swlsp_language::LanguageId;
use swlsp_lsp::Context;
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::{Builder, CreateFilePlan, DocumentPlan, WorkspacePlan};
use swlsp_shopware_entityschema::gojson;
use swlsp_uriutil::{file_uri, go_quote};

use crate::paths::{self, Stat};
use crate::symfony::{ensure_scaffold_target_available, namespace_for_directory};
use crate::{Provider, Result, Sorted, errorf, pascal_name};

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct ShopwareRequest {
    pub kind: String,
    #[serde(rename = "directoryUri")]
    pub directory_uri: String,
    pub name: String,
    #[serde(skip_serializing_if = "Map::is_empty")]
    pub options: Map<String, Value>,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ShopwareResponse {
    pub edit: Option<protocol::WorkspaceEdit>,
    #[serde(rename = "primaryFileUri")]
    pub primary_file_uri: String,
    #[serde(
        rename = "shopwareVersion",
        default,
        skip_serializing_if = "String::is_empty"
    )]
    pub shopware_version: String,
}

struct GeneratedFile {
    path: String,
    content: String,
}

fn generated(path: String, content: impl Into<String>) -> GeneratedFile {
    GeneratedFile {
        path,
        content: content.into(),
    }
}

/// `^[A-Za-z][A-Za-z0-9_-]*$`
fn valid_scaffold_name(name: &str) -> bool {
    let mut bytes = name.bytes();
    bytes
        .next()
        .is_some_and(|first| first.is_ascii_alphabetic())
        && bytes.all(|byte| byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'-')
}

/// `^[A-Za-z_$][A-Za-z0-9_$]*$`
fn valid_js_identifier(name: &str) -> bool {
    let mut bytes = name.bytes();
    bytes
        .next()
        .is_some_and(|first| first.is_ascii_alphabetic() || first == b'_' || first == b'$')
        && bytes.all(|byte| byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'$')
}

impl Provider {
    pub fn create_shopware(&self, ctx: &Context, args: Option<&Value>) -> Result<ShopwareResponse> {
        ctx.check()?;
        let mut request: ShopwareRequest = crate::decode_args(
            args,
            "missing Shopware scaffold request",
            "invalid Shopware scaffold request",
        )?;
        let directory = swlsp_uriutil::path(&request.directory_uri)
            .map_err(|error| errorf!("resolve scaffold directory: {error}"))?;
        let directory = self.validated_directory(&directory)?;
        request.kind = request.kind.trim().to_lowercase();
        request.name = request.name.trim().to_owned();
        if !valid_scaffold_name(&request.name) {
            return Err(errorf!(
                "invalid Shopware scaffold name {}",
                go_quote(&request.name)
            ));
        }
        let (files, primary) = self.shopware_files(&directory, &request)?;
        if files.is_empty() {
            return Err(errorf!(
                "shopware scaffold {} produced no files",
                go_quote(&request.kind)
            ));
        }
        let mut seen: HashSet<String> = HashSet::with_capacity(files.len());
        let mut plan = WorkspacePlan::default();
        for file in files {
            let path = paths::clean(&file.path);
            if !seen.insert(path.clone()) {
                return Err(errorf!("duplicate scaffold target {path}"));
            }
            ensure_scaffold_target_available(&path)?;
            validate_generated_file(&path, &file.content)?;
            plan.creates.push(CreateFilePlan {
                uri: file_uri(&path),
                content: file.content,
            });
        }
        if request.kind == "admin-component" || request.kind == "admin-module" {
            let main_path = paths::join(&[&directory, "main.js"]);
            match paths::stat(&main_path) {
                Stat::Missing => {
                    let content = main_js_import_content(&directory, &primary)?;
                    if seen.contains(&main_path) {
                        return Err(errorf!("duplicate scaffold target {main_path}"));
                    }
                    ensure_scaffold_target_available(&main_path)?;
                    plan.creates.push(CreateFilePlan {
                        uri: file_uri(&main_path),
                        content,
                    });
                }
                Stat::Failed(error) => {
                    return Err(errorf!("{}", paths::path_error("stat", &main_path, &error)));
                }
                Stat::Exists(_) => {
                    if let Some(document) = main_js_import_plan(&directory, &primary)? {
                        plan.documents.push(document);
                    }
                }
            }
        }
        let edit = plan.workspace_edit()?;
        Ok(ShopwareResponse {
            edit: Some(edit),
            primary_file_uri: file_uri(&primary),
            shopware_version: self.shopware_version(),
        })
    }

    fn shopware_files(
        &self,
        directory: &str,
        request: &ShopwareRequest,
    ) -> Result<(Vec<GeneratedFile>, String)> {
        let options = &request.options;
        let mut name = request.name.clone();
        let class_name = pascal_name(&name);
        let mut namespace_fallback = class_name.clone();
        if request.kind != "plugin"
            && let Some(php) = &self.php
            && let Ok(inferred) = namespace_for_directory(php.project().as_deref(), directory)
            && !inferred.is_empty()
        {
            namespace_fallback = inferred;
        }
        let namespace = option_string(options, "namespace", &namespace_fallback);
        let mut version = self.shopware_version();
        if version.is_empty() {
            version = "~6.7".to_owned();
        }
        let join = |parts: &[&str]| {
            let mut all = vec![directory];
            all.extend_from_slice(parts);
            paths::join(&all)
        };
        match request.kind.as_str() {
            "plugin" => {
                let root = join(&[&class_name]);
                let description = option_string(
                    options,
                    "description",
                    &format!("Shopware extension {class_name}"),
                );
                let author = option_string(options, "author", "Acme");
                let license = option_string(options, "license", "MIT");
                let lower = class_name.to_lowercase();
                let package_name = option_string(options, "package", &format!("{lower}/{lower}"));
                let both = |value: &str| json!({"de-DE": value, "en-GB": value});
                let composer = json!({
                    "name": package_name,
                    "description": description,
                    "type": "shopware-platform-plugin",
                    "license": license,
                    "authors": [{"name": author}],
                    "require": {"shopware/core": version},
                    "autoload": {"psr-4": {format!("{namespace}\\"): "src/"}},
                    "extra": {
                        "shopware-plugin-class": format!("{namespace}\\{class_name}"),
                        "label": both(&class_name),
                        "description": both(&description),
                        "manufacturerLink": both("https://example.com"),
                        "supportLink": both("https://example.com"),
                    },
                });
                let primary = paths::join(&[&root, "src", &format!("{class_name}.php")]);
                Ok((
                    vec![
                        generated(
                            paths::join(&[&root, "composer.json"]),
                            go_indent_json(&composer) + "\n",
                        ),
                        generated(primary.clone(), plugin_php(&namespace, &class_name)),
                        generated(
                            paths::join(&[&root, "src", "Resources", "config", "services.yaml"]),
                            plugin_services_yaml(&namespace, &class_name),
                        ),
                    ],
                    primary,
                ))
            }
            "system-config" => {
                let primary = join(&["Resources", "config", "config.xml"]);
                Ok((vec![generated(primary.clone(), SYSTEM_CONFIG_XML)], primary))
            }
            "scheduled-task" => {
                let interval = option_int(options, "interval", 300);
                let task_name =
                    option_string(options, "taskName", &name.replace('_', ".").to_lowercase());
                let root = join(&["ScheduledTask"]);
                let primary = paths::join(&[&root, &format!("{class_name}Task.php")]);
                Ok((
                    vec![
                        generated(
                            primary.clone(),
                            scheduled_task_php(&namespace, &class_name, &task_name, interval),
                        ),
                        generated(
                            paths::join(&[&root, &format!("{class_name}TaskHandler.php")]),
                            scheduled_task_handler_php(&namespace, &class_name),
                        ),
                    ],
                    primary,
                ))
            }
            "migration" => {
                let now = std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .map(|duration| duration.as_secs())
                    .unwrap_or_default()
                    .to_string();
                let timestamp = option_string(options, "timestamp", &now);
                let migration_class = format!("Migration{timestamp}{class_name}");
                let primary = join(&["Migration", &format!("{migration_class}.php")]);
                Ok((
                    vec![generated(
                        primary.clone(),
                        migration_php(&namespace, &migration_class, &timestamp),
                    )],
                    primary,
                ))
            }
            "event-listener" => {
                let event_class = option_string(options, "event", "")
                    .trim_matches(|c| c == '\\' || c == ' ')
                    .to_owned();
                if event_class.is_empty() || !event_class.contains('\\') {
                    return Err(errorf!("event-listener scaffold requires an event class"));
                }
                let event_short = event_class
                    .rsplit_once('\\')
                    .map_or(event_class.as_str(), |(_, short)| short);
                let listener_class = pascal_name(&name);
                let primary = join(&[&format!("{listener_class}.php")]);
                Ok((
                    vec![generated(
                        primary.clone(),
                        event_listener_php(&namespace, &listener_class, &event_class, event_short),
                    )],
                    primary,
                ))
            }
            "app" => {
                let root = join(&[&name]);
                let primary = paths::join(&[&root, "manifest.xml"]);
                Ok((
                    vec![generated(
                        primary.clone(),
                        app_manifest_xml(
                            &name,
                            &option_string(options, "label", &name),
                            &option_string(options, "author", "Acme"),
                            &option_string(options, "license", "MIT"),
                        ),
                    )],
                    primary,
                ))
            }
            "app-custom-entities" => {
                let primary = join(&["Resources", "entities.xml"]);
                Ok((
                    vec![generated(primary.clone(), app_entities_xml(&name))],
                    primary,
                ))
            }
            "app-cms" => {
                let primary = join(&["Resources", "cms.xml"]);
                Ok((vec![generated(primary.clone(), APP_CMS_XML)], primary))
            }
            "app-script" => {
                let hook = option_string(options, "hook", &name);
                let primary = join(&["Resources", "scripts", &hook, &format!("{name}.twig")]);
                Ok((
                    vec![generated(primary.clone(), app_script_twig(&hook))],
                    primary,
                ))
            }
            "admin-component" => {
                let mode = option_string(options, "mode", "register").to_lowercase();
                if mode != "register" && mode != "extend" && mode != "override" {
                    return Err(errorf!(
                        "unsupported Administration component mode {}",
                        go_quote(&mode)
                    ));
                }
                let target = option_string(options, "target", "");
                if mode != "register" && !valid_scaffold_name(&target) {
                    return Err(errorf!(
                        "invalid Administration target component {}",
                        go_quote(&target)
                    ));
                }
                let mut root_kind = "component";
                if mode == "override" {
                    root_kind = "override";
                    name = target.clone();
                }
                let root = join(&[root_kind, &name]);
                let primary = paths::join(&[&root, "index.js"]);
                let generate_assets = mode == "register";
                let generate_twig = option_bool(options, "generateTwig", generate_assets);
                let generate_scss = option_bool(options, "generateScss", generate_assets);
                let mut files = vec![generated(
                    primary.clone(),
                    admin_component_js(
                        &name,
                        &target,
                        &mode,
                        generate_twig,
                        generate_scss,
                        options,
                    ),
                )];
                if generate_twig {
                    files.push(generated(
                        paths::join(&[&root, &format!("{name}.html.twig")]),
                        admin_component_twig(&name),
                    ));
                }
                if generate_scss {
                    files.push(generated(
                        paths::join(&[&root, &format!("{name}.scss")]),
                        format!(".{name} {{\n}}\n"),
                    ));
                }
                Ok((files, primary))
            }
            "admin-module" => {
                let root = join(&["module", &name]);
                let primary = paths::join(&[&root, "index.js"]);
                Ok((
                    vec![
                        generated(primary.clone(), admin_module_js(&name, options)),
                        generated(
                            paths::join(&[&root, "snippet", "en-GB.json"]),
                            admin_module_snippet(&name),
                        ),
                        generated(
                            paths::join(&[&root, "snippet", "de-DE.json"]),
                            admin_module_snippet(&name),
                        ),
                    ],
                    primary,
                ))
            }
            "cms-block" => Ok(cms_block_files(
                directory,
                &name,
                &option_string(options, "category", "text"),
            )),
            "cms-element" => Ok(cms_element_files(directory, &name)),
            _ => Err(errorf!(
                "unsupported Shopware scaffold kind {}",
                go_quote(&request.kind)
            )),
        }
    }

    pub(crate) fn shopware_version(&self) -> String {
        #[derive(Default, Deserialize)]
        #[serde(default)]
        struct Composer {
            require: BTreeMap<String, String>,
        }
        let Ok(data) = std::fs::read(paths::join(&[&self.root, "composer.json"])) else {
            return String::new();
        };
        gojson::from_slice::<Composer>(&data)
            .ok()
            .and_then(|mut composer| composer.require.remove("shopware/core"))
            .unwrap_or_default()
    }
}

/// Go `json.MarshalIndent(value, "", "  ")` of a map literal (sorted keys,
/// HTML-safe escaping).
fn go_indent_json(value: &Value) -> String {
    String::from_utf8(gojson::to_vec_indent(&Sorted(value)).unwrap_or_default()).unwrap_or_default()
}

fn validate_generated_file(path: &str, source: &str) -> Result<()> {
    let Some(parse) = LanguageId::from_path(path).and_then(|language| language.parse(source))
    else {
        return Ok(());
    };
    if let Some(error) = parse.errors.first() {
        return Err(errorf!(
            "generated {} scaffold is invalid: {}",
            paths::ext(path),
            error.message()
        ));
    }
    Ok(())
}

fn main_js_import_plan(directory: &str, primary: &str) -> Result<Option<DocumentPlan>> {
    let main_path = paths::join(&[directory, "main.js"]);
    let Some(source) = paths::read_file(&main_path).map_err(|error| errorf!("{error}"))? else {
        return Ok(None);
    };
    let relative =
        paths::rel(directory, &paths::dir(primary)).map_err(|error| errorf!("{error}"))?;
    let import_path = format!("./{}", paths::to_slash(&relative));
    if source.contains(&format!("'{import_path}'"))
        || source.contains(&format!("\"{import_path}\""))
    {
        return Ok(None);
    }
    let prefix = if !source.is_empty() && !source.ends_with('\n') {
        "\n"
    } else {
        ""
    };
    let mut builder = Builder::new(&source);
    builder.insert(
        source.len() as u32,
        format!("{prefix}import '{import_path}';\n"),
    )?;
    let edits = builder.finish()?;
    Ok(Some(DocumentPlan::new(
        file_uri(&main_path),
        None,
        source,
        edits,
    )))
}

fn main_js_import_content(directory: &str, primary: &str) -> Result<String> {
    let relative =
        paths::rel(directory, &paths::dir(primary)).map_err(|error| errorf!("{error}"))?;
    Ok(format!("import './{}';\n", paths::to_slash(&relative)))
}

fn option_string(options: &Map<String, Value>, key: &str, fallback: &str) -> String {
    match options.get(key) {
        Some(Value::String(value)) if !value.trim().is_empty() => value.trim().to_owned(),
        _ => fallback.to_owned(),
    }
}

fn option_bool(options: &Map<String, Value>, key: &str, fallback: bool) -> bool {
    match options.get(key) {
        Some(Value::Bool(value)) => *value,
        _ => fallback,
    }
}

fn option_int(options: &Map<String, Value>, key: &str, fallback: i64) -> i64 {
    match options.get(key) {
        Some(Value::Number(number)) => number
            .as_i64()
            .unwrap_or_else(|| number.as_f64().map_or(fallback, |value| value as i64)),
        _ => fallback,
    }
}

fn normalize_twig_name(name: &str) -> String {
    name.replace('-', "_")
}

fn plugin_php(namespace: &str, class: &str) -> String {
    format!(
        "<?php declare(strict_types=1);\n\nnamespace {namespace};\n\nuse Shopware\\Core\\Framework\\Plugin;\n\nclass {class} extends Plugin\n{{\n}}\n"
    )
}

fn plugin_services_yaml(namespace: &str, class: &str) -> String {
    format!(
        "services:
  _defaults:
    autowire: true
    autoconfigure: true

  {namespace}\\:
    resource: '../../'
    exclude:
      - '../../Resources/'
      - '../../Migration/'
      - '../../{class}.php'
"
    )
}

const SYSTEM_CONFIG_XML: &str = r#"<?xml version="1.0" encoding="UTF-8"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
        xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/System/SystemConfig/Schema/config.xsd">
    <card>
        <title>Configuration</title>
        <input-field type="text">
            <name>example</name>
            <label>Example</label>
        </input-field>
    </card>
</config>
"#;

fn scheduled_task_php(namespace: &str, name: &str, task_name: &str, interval: i64) -> String {
    format!(
        "<?php declare(strict_types=1);\n\nnamespace {namespace}\\ScheduledTask;\n\nuse Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask\\ScheduledTask;\n\nclass {name}Task extends ScheduledTask\n{{\n    public static function getTaskName(): string\n    {{\n        return '{task_name}';\n    }}\n\n    public static function getDefaultInterval(): int\n    {{\n        return {interval};\n    }}\n}}\n"
    )
}

fn scheduled_task_handler_php(namespace: &str, name: &str) -> String {
    format!(
        "<?php declare(strict_types=1);\n\nnamespace {namespace}\\ScheduledTask;\n\nuse Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask\\ScheduledTaskHandler;\n\nclass {name}TaskHandler extends ScheduledTaskHandler\n{{\n    public static function getHandledMessages(): iterable\n    {{\n        return [{name}Task::class];\n    }}\n\n    public function run(): void\n    {{\n    }}\n}}\n"
    )
}

fn migration_php(namespace: &str, class: &str, timestamp: &str) -> String {
    format!(
        "<?php declare(strict_types=1);\n\nnamespace {namespace}\\Migration;\n\nuse Doctrine\\DBAL\\Connection;\nuse Shopware\\Core\\Framework\\Migration\\MigrationStep;\n\nclass {class} extends MigrationStep\n{{\n    public function getCreationTimestamp(): int\n    {{\n        return {timestamp};\n    }}\n\n    public function update(Connection $connection): void\n    {{\n    }}\n\n    public function updateDestructive(Connection $connection): void\n    {{\n    }}\n}}\n"
    )
}

fn event_listener_php(
    namespace: &str,
    class: &str,
    event_class: &str,
    event_short: &str,
) -> String {
    format!(
        "<?php declare(strict_types=1);\n\nnamespace {namespace};\n\nuse {event_class};\nuse Symfony\\Component\\EventDispatcher\\Attribute\\AsEventListener;\n\n#[AsEventListener]\nclass {class}\n{{\n    public function __invoke({event_short} $event): void\n    {{\n    }}\n}}\n"
    )
}

fn app_manifest_xml(name: &str, label: &str, author: &str, license: &str) -> String {
    format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<manifest xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/Framework/App/Manifest/Schema/manifest-3.0.xsd">
    <meta>
        <name>{name}</name>
        <label>{label}</label>
        <label lang="de-DE">{label}</label>
        <description>{label}</description>
        <description lang="de-DE">{label}</description>
        <author>{author}</author>
        <version>1.0.0</version>
        <license>{license}</license>
    </meta>
</manifest>
"#
    )
}

fn app_entities_xml(name: &str) -> String {
    format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<entities xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/System/CustomEntity/Xml/entity-1.0.xsd">
    <entity name="custom_entity_{}">
        <fields/>
    </entity>
</entities>
"#,
        name.replace('-', "_").to_lowercase()
    )
}

const APP_CMS_XML: &str = r#"<?xml version="1.0" encoding="UTF-8"?>
<cms xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
     xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/Framework/App/Cms/Schema/cms-1.0.xsd">
    <blocks/>
</cms>
"#;

fn app_script_twig(hook: &str) -> String {
    format!(
        "{{# Script hook: {hook} #}}\n{{# @var services \\Shopware\\Core\\Framework\\Script\\ServiceStubs #}}\n\n{{# Your script code #}}\n"
    )
}

fn admin_component_js(
    name: &str,
    target: &str,
    mode: &str,
    twig: bool,
    scss: bool,
    options: &Map<String, Value>,
) -> String {
    let mut imports = String::new();
    let mut fields = String::new();
    if twig {
        imports.push_str(&format!("import template from './{name}.html.twig';\n"));
        fields.push_str("    template,\n");
    }
    if scss {
        imports.push_str(&format!("import './{name}.scss';\n"));
    }
    let method = option_string(options, "method", "");
    if !method.is_empty() && valid_js_identifier(&method) {
        fields.push_str(&admin_override_method_js(
            &method,
            &option_string(options, "methodGroup", ""),
            &option_string(options, "parameters", ""),
        ));
    }
    let call = match mode {
        "extend" => format!("Component.extend('{name}', '{target}', {{"),
        "override" => format!("Component.override('{target}', {{"),
        _ => format!("Component.register('{name}', {{"),
    };
    format!("{imports}\nconst {{ Component }} = Shopware;\n\n{call}\n{fields}}});\n")
}

fn admin_override_method_js(method: &str, group: &str, parameters: &str) -> String {
    let parameter_names: Vec<&str> = parameters
        .split(',')
        .map(str::trim)
        .filter(|parameter| !parameter.is_empty() && valid_js_identifier(parameter))
        .collect();
    let parameters = parameter_names.join(", ");
    let forward = if parameters.is_empty() {
        String::new()
    } else {
        format!(", {parameters}")
    };
    let method_body = format!(
        "{method}({parameters}) {{\n            return this.$super('{method}'{forward});\n        }},\n"
    );
    if !group.is_empty() && valid_js_identifier(group) {
        return format!("    {group}: {{\n        {method_body}    }},\n");
    }
    format!("    {}", method_body.replace("\n        ", "\n    "))
}

fn admin_component_twig(name: &str) -> String {
    format!(
        "{{% block {} %}}\n<div class=\"{name}\">\n</div>\n{{% endblock %}}\n",
        normalize_twig_name(name)
    )
}

fn admin_module_js(name: &str, options: &Map<String, Value>) -> String {
    let type_name = option_string(options, "type", "plugin");
    let color = option_string(options, "color", "#189EFF");
    let icon = option_string(options, "icon", "regular-cube");
    let prefix = name.replace('-', ".");
    format!(
        "const {{ Module }} = Shopware;\n\nModule.register('{name}', {{\n    type: '{type_name}',\n    title: '{name}.general.title',\n    description: '{name}.general.description',\n    color: '{color}',\n    icon: '{icon}',\n    routes: {{\n        index: {{ component: '{name}-index', path: 'index' }},\n    }},\n}});\n\n// Route name: {prefix}.index\n"
    )
}

fn admin_module_snippet(name: &str) -> String {
    let mut root = Map::new();
    root.insert(
        name.to_owned(),
        json!({"general": {"title": name, "description": name}}),
    );
    go_indent_json(&Value::Object(root)) + "\n"
}

fn cms_block_files(directory: &str, name: &str, category: &str) -> (Vec<GeneratedFile>, String) {
    let root = paths::join(&[directory, "module", "sw-cms", "blocks", name]);
    let primary = paths::join(&[&root, "index.js"]);
    let normalized = normalize_twig_name(name);
    (
        vec![
            generated(
                primary.clone(),
                format!(
                    "import './component';\nimport './preview';\n\nShopware.Service('cmsService').registerCmsBlock({{ name: '{name}', label: 'sw-cms.blocks.{category}.{name}.label', category: '{category}', component: 'sw-cms-block-{name}', previewComponent: 'sw-cms-preview-{name}', slots: {{ content: 'text' }} }});\n"
                ),
            ),
            generated(
                paths::join(&[&root, "component", "index.js"]),
                format!(
                    "import template from './sw-cms-block-{name}.html.twig';\nShopware.Component.register('sw-cms-block-{name}', {{ template }});\n"
                ),
            ),
            generated(
                paths::join(&[
                    &root,
                    "component",
                    &format!("sw-cms-block-{name}.html.twig"),
                ]),
                format!(
                    "{{% block sw_cms_block_{normalized} %}}\n<div class=\"sw-cms-block-{name}\">{name}</div>\n{{% endblock %}}\n"
                ),
            ),
            generated(
                paths::join(&[&root, "preview", "index.js"]),
                format!(
                    "import template from './sw-cms-preview-{name}.html.twig';\nShopware.Component.register('sw-cms-preview-{name}', {{ template }});\n"
                ),
            ),
            generated(
                paths::join(&[
                    &root,
                    "preview",
                    &format!("sw-cms-preview-{name}.html.twig"),
                ]),
                format!("<div class=\"sw-cms-preview-{name}\">{name}</div>\n"),
            ),
        ],
        primary,
    )
}

fn cms_element_files(directory: &str, name: &str) -> (Vec<GeneratedFile>, String) {
    let root = paths::join(&[directory, "module", "sw-cms", "elements", name]);
    let primary = paths::join(&[&root, "index.js"]);
    let normalized = normalize_twig_name(name);
    (
        vec![
            generated(
                primary.clone(),
                format!(
                    "import './component';\nimport './config';\nimport './preview';\n\nShopware.Service('cmsService').registerCmsElement({{ name: '{name}', label: 'sw-cms.elements.{name}.label', component: 'sw-cms-el-component-{name}', configComponent: 'sw-cms-el-config-{name}', previewComponent: 'sw-cms-el-preview-{name}', defaultConfig: {{}}, defaultData: {{}} }});\n"
                ),
            ),
            generated(
                paths::join(&[&root, "component", "index.js"]),
                format!(
                    "import template from './sw-cms-el-component-{name}.html.twig';\nShopware.Component.register('sw-cms-el-component-{name}', {{ template }});\n"
                ),
            ),
            generated(
                paths::join(&[
                    &root,
                    "component",
                    &format!("sw-cms-el-component-{name}.html.twig"),
                ]),
                format!(
                    "{{% block sw_cms_el_component_{normalized} %}}<div>{name}</div>{{% endblock %}}\n"
                ),
            ),
            generated(
                paths::join(&[&root, "config", "index.js"]),
                format!("Shopware.Component.register('sw-cms-el-config-{name}', {{}});\n"),
            ),
            generated(
                paths::join(&[&root, "preview", "index.js"]),
                format!("Shopware.Component.register('sw-cms-el-preview-{name}', {{}});\n"),
            ),
        ],
        primary,
    )
}
