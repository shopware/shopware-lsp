use std::path::Path;
use std::sync::Arc;

use swlsp_indexer::{
    DataIndexer, Error, Indexer, Mutation, ParsedFile, Result, Store, TransactionalClearer,
    TransactionalRemover,
};

use crate::{SystemConfigEntry, index_system_config_tree, is_system_config_xml};

/// Indexes system-config XML documents by fully qualified field key.
pub struct SystemConfigIndexer {
    entries: DataIndexer<SystemConfigEntry>,
}

impl SystemConfigIndexer {
    pub fn new(config_dir: impl AsRef<Path>, store: Option<&Store>) -> Result<Self> {
        let entries = DataIndexer::repository(
            config_dir.as_ref().join("system_config.db"),
            "system_config",
            store,
        )?;
        Ok(Self { entries })
    }

    /// Every indexed `namespace.field` key.
    pub fn system_config_keys(&self) -> Result<Arc<[String]>> {
        self.entries.get_all_keys_view()
    }

    pub fn system_config_entry(&self, key: &str) -> Result<Arc<[SystemConfigEntry]>> {
        self.entries.get_values_view(key)
    }

    pub fn all_system_config_entries(&self) -> Result<Arc<[SystemConfigEntry]>> {
        self.entries.get_all_values_view()
    }
}

impl Indexer for SystemConfigIndexer {
    fn id(&self) -> &str {
        "systemconfig.indexer"
    }

    fn index(&self, file: &ParsedFile) -> Result<()> {
        let path = file.path();
        if !path.ends_with(".xml") || path.contains("/_fixtures/") || path.contains("/_fixture/") {
            return Ok(());
        }
        // Replacing with an empty set clears entries when a file stops being
        // a system-config document but keeps the same path.
        if !is_system_config_xml(file.source()) {
            return self.entries.batch_save_items_in(
                file.mutation(),
                [(path, std::iter::empty::<(&str, &SystemConfigEntry)>())],
            );
        }
        let tree = file
            .syntax_tree()
            .ok_or_else(|| Error::msg("not a system config file"))?;
        let entries = index_system_config_tree(path, tree, file.line_index())?;
        // A repeated key keeps its last entry.
        let mut items: Vec<&SystemConfigEntry> = Vec::with_capacity(entries.len());
        for entry in &entries {
            match items
                .iter_mut()
                .find(|existing| existing.name == entry.name)
            {
                Some(existing) => *existing = entry,
                None => items.push(entry),
            }
        }
        self.entries.batch_save_items_in(
            file.mutation(),
            [(
                path,
                items.into_iter().map(|entry| (entry.name.as_str(), entry)),
            )],
        )
    }

    fn removed_files(&self, paths: &[String]) -> Result<()> {
        self.entries.batch_delete_by_file_paths(paths)
    }

    fn close(&self) -> Result<()> {
        self.entries.close()
    }

    fn clear(&self) -> Result<()> {
        self.entries.clear()
    }

    fn as_transactional_remover(&self) -> Option<&dyn TransactionalRemover> {
        Some(self)
    }

    fn as_transactional_clearer(&self) -> Option<&dyn TransactionalClearer> {
        Some(self)
    }
}

impl TransactionalRemover for SystemConfigIndexer {
    fn removed_files_in(&self, paths: &[String], mutation: &Mutation) -> Result<()> {
        self.entries
            .batch_delete_by_file_paths_in(Some(mutation), paths)
    }
}

impl TransactionalClearer for SystemConfigIndexer {
    fn clear_in(&self, mutation: &Mutation) -> Result<()> {
        self.entries.clear_in(Some(mutation))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const XML: &str = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<config xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\"\n        xsi:noNamespaceSchemaLocation=\"https://raw.githubusercontent.com/shopware/platform/trunk/src/Core/System/SystemConfig/Schema/config.xsd\">\n    <card>\n        <title>Basic Configuration</title>\n        <input-field type=\"text\">\n            <name>testField</name>\n            <label>Test Field</label>\n        </input-field>\n        <component name=\"sw-entity-single-select\">\n            <name>testComponent</name>\n            <label>Test Component</label>\n        </component>\n    </card>\n</config>";

    #[test]
    fn stores_queries_and_removes_entries() {
        let dir = tempfile::tempdir().unwrap();
        let indexer = SystemConfigIndexer::new(dir.path(), None).unwrap();
        let config_path = dir.path().join("TestPlugin/Resources/config/config.xml");
        let config_path = config_path.to_str().unwrap();

        let field = SystemConfigEntry {
            namespace: "TestPlugin\\TestPlugin".into(),
            name: "testField".into(),
            label: "Test Field".into(),
            r#type: "text".into(),
            file_path: config_path.into(),
            line: 1,
            ..SystemConfigEntry::default()
        };
        let component = SystemConfigEntry {
            namespace: "TestPlugin\\TestPlugin".into(),
            name: "testComponent".into(),
            label: "Test Component".into(),
            component: "sw-entity-single-select".into(),
            file_path: config_path.into(),
            line: 1,
            ..SystemConfigEntry::default()
        };
        let field_key = format!("{}.{}", field.namespace, field.name);
        let component_key = format!("{}.{}", component.namespace, component.name);
        indexer
            .entries
            .batch_save_items([(
                config_path,
                [
                    (field_key.as_str(), &field),
                    (component_key.as_str(), &component),
                ],
            )])
            .unwrap();

        assert_eq!(indexer.system_config_keys().unwrap().len(), 2);
        let fields = indexer.system_config_entry(&field_key).unwrap();
        assert_eq!(fields.len(), 1);
        assert_eq!(fields[0], field);
        let components = indexer.system_config_entry(&component_key).unwrap();
        assert_eq!(components.len(), 1);
        assert_eq!(components[0], component);
        assert_eq!(indexer.all_system_config_entries().unwrap().len(), 2);

        indexer.removed_files(&[config_path.to_owned()]).unwrap();
        assert!(indexer.system_config_keys().unwrap().is_empty());
        indexer.close().unwrap();
    }

    #[test]
    fn indexes_and_clears_documents() {
        let dir = tempfile::tempdir().unwrap();
        let plugin = dir.path().join("TestPlugin");
        std::fs::create_dir_all(plugin.join("Resources/config")).unwrap();
        std::fs::write(
            plugin.join("composer.json"),
            r#"{"name": "test/plugin", "extra": {"shopware-plugin-class": "TestPlugin\\TestPlugin"}}"#,
        )
        .unwrap();
        let config = plugin.join("Resources/config/config.xml");
        let config = config.to_str().unwrap();
        let indexer = SystemConfigIndexer::new(dir.path(), None).unwrap();
        indexer
            .index(&ParsedFile::new(config, XML.as_bytes().to_vec()))
            .unwrap();
        let mut keys = indexer.system_config_keys().unwrap().to_vec();
        keys.sort();
        assert_eq!(
            keys,
            [
                "TestPlugin.config.testComponent",
                "TestPlugin.config.testField"
            ]
        );
        assert_eq!(
            indexer
                .system_config_entry("TestPlugin.config.testField")
                .unwrap()[0]
                .line,
            6
        );

        indexer
            .index(&ParsedFile::new(config, b"<config/>".to_vec()))
            .unwrap();
        assert!(indexer.system_config_keys().unwrap().is_empty());

        let fixture = dir.path().join("_fixtures/config.xml");
        indexer
            .index(&ParsedFile::new(
                fixture.to_str().unwrap(),
                XML.as_bytes().to_vec(),
            ))
            .unwrap();
        assert!(indexer.system_config_keys().unwrap().is_empty());
        indexer.close().unwrap();
    }
}
