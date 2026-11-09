use std::path::Path;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_indexer::{Error, Result, ResultExt};
use swlsp_parser_xml::query;
use swlsp_syntax::{LineIndex, Tree};

use crate::{SYSTEM_CONFIG_SCHEMA, find_all_system_config_fields};

/// A system-config field with its namespace (`<Plugin>.config` or
/// `core.<file>`). `name` is the fully qualified `namespace.field` key.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct SystemConfigEntry {
    pub namespace: String,
    pub name: String,
    pub label: String,
    pub r#type: String,
    pub component: String,
    pub file_path: String,
    pub line: u32,
}

/// The configuration namespace of `file_path`, found through the nearest
/// enclosing `composer.json` (plugin class) or `manifest.xml` (app name);
/// `core.<file stem>` without either.
pub fn namespace_from_path(file_path: &str) -> Result<String> {
    let path = Path::new(file_path);
    let file_name = path
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or_default();
    let stem = match file_name.rfind('.') {
        Some(dot) => &file_name[..dot],
        None => file_name,
    };
    let start = path.parent().unwrap_or(Path::new(""));
    for dir in start.ancestors() {
        let composer = dir.join("composer.json");
        if composer.metadata().is_ok() {
            return namespace_from_composer_json(&composer, stem);
        }
        let manifest = dir.join("manifest.xml");
        if manifest.metadata().is_ok() {
            return namespace_from_manifest_xml(&manifest);
        }
    }
    Ok(format!("core.{stem}"))
}

fn namespace_from_composer_json(composer_path: &Path, stem: &str) -> Result<String> {
    let data = std::fs::read(composer_path)?;
    let composer: Value = serde_json::from_slice(&data).map_err(Error::other)?;
    let plugin_class = composer_plugin_class(&composer)
        .with_context(|| format!("invalid composer.json {}", composer_path.display()))?;
    if plugin_class.is_empty() {
        return Ok(format!("core.{stem}"));
    }
    let plugin_name = plugin_class.rsplit('\\').next().unwrap_or(plugin_class);
    Ok(format!("{plugin_name}.config"))
}

/// Validates the composer.json shape: `name` and `extra.shopware-plugin-class` must
/// be strings (or null), `extra` an object (or null).
fn composer_plugin_class(composer: &Value) -> Result<&str> {
    let Value::Object(composer) = composer else {
        return Err(Error::msg("composer.json is not an object"));
    };
    if !matches!(
        composer.get("name"),
        None | Some(Value::Null | Value::String(_))
    ) {
        return Err(Error::msg("composer.json name is not a string"));
    }
    match composer.get("extra") {
        None | Some(Value::Null) => Ok(""),
        Some(Value::Object(extra)) => match extra.get("shopware-plugin-class") {
            None | Some(Value::Null) => Ok(""),
            Some(Value::String(class)) => Ok(class),
            Some(_) => Err(Error::msg(
                "composer.json shopware-plugin-class is not a string",
            )),
        },
        Some(_) => Err(Error::msg("composer.json extra is not an object")),
    }
}

fn namespace_from_manifest_xml(manifest_path: &Path) -> Result<String> {
    let data = std::fs::read(manifest_path)?;
    let source = String::from_utf8_lossy(&data);
    let root = swlsp_parser_xml::parse(&source).tree.root();
    Ok(query::elements(&root, &["name"])
        .first()
        .map(|name| query::text_content(name).trim().to_owned())
        .unwrap_or_default())
}

/// Parses and indexes one system-config document.
pub fn index_system_config_file(file_path: &str, data: &str) -> Result<Vec<SystemConfigEntry>> {
    if !crate::is_system_config_xml(data) {
        return Err(Error::msg("not a system config file"));
    }
    let tree = swlsp_parser_xml::parse(data).tree;
    let line_index = LineIndex::new(tree.source_arc().clone());
    index_system_config_tree(file_path, &tree, &line_index)
}

pub fn index_system_config_tree(
    file_path: &str,
    tree: &Tree,
    line_index: &LineIndex,
) -> Result<Vec<SystemConfigEntry>> {
    if !tree.source().contains(SYSTEM_CONFIG_SCHEMA) {
        return Err(Error::msg("not a system config file"));
    }
    let namespace = namespace_from_path(file_path)?;
    Ok(
        find_all_system_config_fields(&tree.root(), line_index, file_path)
            .into_iter()
            .map(|field| SystemConfigEntry {
                name: format!("{namespace}.{}", field.name),
                namespace: namespace.clone(),
                label: field.label,
                r#type: field.r#type,
                component: field.component,
                file_path: file_path.to_owned(),
                line: field.line,
            })
            .collect(),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn write(path: &Path, content: &str) {
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(path, content).unwrap();
    }

    #[test]
    fn namespace_from_composer_manifest_and_fallback() {
        let dir = tempfile::tempdir().unwrap();
        let root = dir.path();

        write(
            &root.join("plugin/composer.json"),
            r#"{"name": "shopware/plugin-name", "extra": {"shopware-plugin-class": "Shopware\\PluginName\\PluginName"}}"#,
        );
        let config = root.join("plugin/Resources/config/config.xml");
        write(&config, "<config></config>");
        assert_eq!(
            namespace_from_path(config.to_str().unwrap()).unwrap(),
            "PluginName.config"
        );

        write(
            &root.join("plugin2/manifest.xml"),
            "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<manifest>\n  <meta>\n    <name>MyApp</name>\n  </meta>\n</manifest>",
        );
        let config = root.join("plugin2/Resources/config/config.xml");
        write(&config, "<config></config>");
        assert_eq!(
            namespace_from_path(config.to_str().unwrap()).unwrap(),
            "MyApp"
        );

        let config = root.join("standalone/custom.xml");
        write(&config, "<config></config>");
        assert_eq!(
            namespace_from_path(config.to_str().unwrap()).unwrap(),
            "core.custom"
        );

        // A composer.json without a plugin class falls back to the file stem.
        write(
            &root.join("lib/composer.json"),
            r#"{"name": "x/y", "extra": null}"#,
        );
        let config = root.join("lib/config/settings.xml");
        write(&config, "<config/>");
        assert_eq!(
            namespace_from_path(config.to_str().unwrap()).unwrap(),
            "core.settings"
        );

        write(&root.join("broken/composer.json"), r#"{"extra": []}"#);
        let config = root.join("broken/config.xml");
        write(&config, "<config/>");
        assert!(namespace_from_path(config.to_str().unwrap()).is_err());
    }

    #[test]
    fn indexes_system_config_file() {
        let dir = tempfile::tempdir().unwrap();
        let plugin = dir.path().join("plugin");
        write(
            &plugin.join("composer.json"),
            r#"{"name": "shopware/test-plugin", "extra": {"shopware-plugin-class": "Shopware\\TestPlugin\\TestPlugin"}}"#,
        );
        let content = "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<config xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xsi:noNamespaceSchemaLocation=\"https://raw.githubusercontent.com/shopware/shopware/master/src/Core/System/SystemConfig/Schema/config.xsd\">\n\t<card>\n\t\t<input-field type=\"text\">\n\t\t\t<name>textField</name>\n\t\t\t<label>Text Field</label>\n\t\t</input-field>\n\t\t<component name=\"custom-component\">\n\t\t\t<name>customComponent</name>\n\t\t\t<label>Custom Component</label>\n\t\t</component>\n\t</card>\n</config>";
        let config = plugin.join("Resources/config/config.xml");
        write(&config, content);
        let config = config.to_str().unwrap();

        let entries = index_system_config_file(config, content).unwrap();
        assert_eq!(entries.len(), 2);
        assert_eq!(entries[0].namespace, "TestPlugin.config");
        assert_eq!(entries[0].name, "TestPlugin.config.textField");
        assert_eq!(entries[0].label, "Text Field");
        assert_eq!(entries[0].r#type, "text");
        assert_eq!(entries[0].component, "");
        assert_eq!(entries[0].file_path, config);
        assert_eq!(entries[1].namespace, "TestPlugin.config");
        assert_eq!(entries[1].name, "TestPlugin.config.customComponent");
        assert_eq!(entries[1].label, "Custom Component");
        assert_eq!(entries[1].r#type, "");
        assert_eq!(entries[1].component, "custom-component");
        assert_eq!(entries[1].file_path, config);

        assert!(index_system_config_file(config, "<config/>").is_err());
    }
}
