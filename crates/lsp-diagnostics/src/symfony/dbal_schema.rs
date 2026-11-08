//! Go `dbal_schema.go`: physical tables described by Shopware DAL
//! definitions.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use swlsp_indexer::Result;
use swlsp_php_types::{equal_fold, go_to_lower};
use swlsp_shopware_dal::{Definition, Index};

#[derive(Default)]
struct DbalSchemaTable {
    dal_definitions: Vec<Definition>,
}

pub(crate) struct DbalSchemaCatalog {
    dal: Option<Arc<Index>>,
    tables: HashMap<String, DbalSchemaTable>,
    names: Vec<String>,
    dal_resolved: HashSet<String>,
    dal_loaded: bool,
}

impl DbalSchemaCatalog {
    pub(crate) fn new(dal: Option<Arc<Index>>) -> Self {
        Self {
            dal,
            tables: HashMap::new(),
            names: Vec::new(),
            dal_resolved: HashSet::new(),
            dal_loaded: false,
        }
    }

    fn add_table(&mut self, name: &str) -> Option<&mut DbalSchemaTable> {
        let name = name.trim();
        if name.is_empty() {
            return None;
        }
        let key = go_to_lower(name);
        if !self.tables.contains_key(&key) {
            self.names.push(name.to_owned());
        }
        Some(self.tables.entry(key).or_default())
    }

    fn add_dal_definition(&mut self, definition: Definition) {
        let Some(table) = self.add_table(&definition.name.clone()) else {
            return;
        };
        if table
            .dal_definitions
            .iter()
            .any(|current| current.file == definition.file && current.class == definition.class)
        {
            return;
        }
        table.dal_definitions.push(definition);
    }

    fn resolve_dal_table(&mut self, name: &str) -> Result<()> {
        let Some(dal) = self.dal.clone() else {
            return Ok(());
        };
        if name.is_empty() {
            return Ok(());
        }
        let key = go_to_lower(name);
        if self.dal_resolved.contains(&key) || self.dal_loaded {
            return Ok(());
        }
        self.dal_resolved.insert(key);
        let definitions = dal.definition(name)?;
        let found = !definitions.is_empty();
        for definition in definitions {
            self.add_dal_definition(definition);
        }
        if found {
            return Ok(());
        }
        // The repository key is case-sensitive while DBAL table comparison is
        // not. Load the complete DAL catalog only for an unresolved reference
        // or when typo suggestions are requested.
        self.load_all_dal_tables()
    }

    fn load_all_dal_tables(&mut self) -> Result<()> {
        let Some(dal) = self.dal.clone() else {
            return Ok(());
        };
        if self.dal_loaded {
            return Ok(());
        }
        for definition in dal.definitions()? {
            self.add_dal_definition(definition);
        }
        self.dal_loaded = true;
        Ok(())
    }

    pub(crate) fn has_table(&mut self, name: &str) -> Result<bool> {
        if name.is_empty() {
            return Ok(false);
        }
        let key = go_to_lower(name);
        if self.tables.contains_key(&key) {
            return Ok(true);
        }
        self.resolve_dal_table(name)?;
        Ok(self.tables.contains_key(&key))
    }

    pub(crate) fn table_names(&mut self) -> Result<Vec<String>> {
        self.load_all_dal_tables()?;
        let mut result = self.names.clone();
        result.sort_by_cached_key(|name| go_to_lower(name));
        Ok(result)
    }

    /// The column names of `table_name`, and whether the table exists.
    pub(crate) fn columns(&mut self, table_name: &str) -> Result<(Vec<String>, bool)> {
        if table_name.is_empty() {
            return Ok((Vec::new(), false));
        }
        self.resolve_dal_table(table_name)?;
        let Some(table) = self.tables.get(&go_to_lower(table_name)) else {
            return Ok((Vec::new(), false));
        };
        let mut seen = HashSet::new();
        let mut result = Vec::new();
        for definition in &table.dal_definitions {
            for field in &definition.fields {
                // Association fields describe object navigation; their
                // foreign-key storage is the corresponding scalar FkField.
                if field.association {
                    continue;
                }
                let name = if field.storage_name.is_empty() {
                    &field.name
                } else {
                    &field.storage_name
                };
                let name = name.trim();
                if name.is_empty() || !seen.insert(go_to_lower(name)) {
                    continue;
                }
                result.push(name.to_owned());
            }
        }
        result.sort_by_cached_key(|name| go_to_lower(name));
        Ok((result, true))
    }
}

pub(crate) fn has_dbal_schema_name(names: &[String], name: &str) -> bool {
    names.iter().any(|candidate| equal_fold(candidate, name))
}
