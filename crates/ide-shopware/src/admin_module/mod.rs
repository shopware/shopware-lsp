//! Administration modules generated from the description of a plugin
//! (`src/Resources/shopware-lsp/admin-modules.yaml`).
//!
//! [`Generator`] reads the entities of the plugin and renders the files of
//! every described module; the inspection reports a description that does
//! not fit its entity and files that differ from what it describes, and its
//! fix writes them as one workspace edit.

mod action;
mod completion;
mod completion_flow;
mod icons;
mod inspection;
mod plan;

use std::collections::BTreeMap;
use std::sync::Arc;

use swlsp_shopware::SupportedVersions;
use swlsp_shopware_adminmodule::{
    self as adminmodule, CONFIG_RELATIVE_PATH, Entity, GENERATED_DIRECTORY, Problem, Style,
};
use swlsp_shopware_dal::Index as DalIndex;
use swlsp_shopware_entityschema::{self as entityschema, IndexedCatalog};
use swlsp_uriutil::filepath;

pub use action::AdminModuleActionProvider;
pub use completion::AdminModuleCompletionProvider;
pub use icons::AdminIcons;
pub(crate) use inspection::new_admin_module;

/// The sources of the Administration of a plugin, below its root.
const ADMIN_SOURCE: &str = "src/Resources/app/administration/src";

/// Why nothing can be generated right now.
#[derive(Debug, thiserror::Error)]
pub(crate) enum GenerateError {
    /// The workspace is still being indexed.
    #[error("the workspace index is not ready")]
    NotReady,
    #[error("{0}")]
    Config(#[from] adminmodule::ConfigError),
    #[error("read the entities of the plugin: {0}")]
    Entities(String),
}

/// A file of a module with its absolute path.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct Target {
    pub(crate) path: String,
    pub(crate) content: String,
    /// Written when missing and left alone afterwards.
    pub(crate) once: bool,
}

/// What the description of a plugin generates.
#[derive(Debug, Default)]
pub(crate) struct Generation {
    /// The directory of the Administration sources.
    pub(crate) admin: String,
    /// Every module the description names, generated or not.
    pub(crate) described: Vec<String>,
    /// The files by the name of their module.
    pub(crate) modules: BTreeMap<String, Vec<Target>>,
    pub(crate) problems: Vec<Problem>,
}

impl Generation {
    pub(crate) fn module_directory(&self, name: &str) -> String {
        filepath::join(&[self.admin.as_str(), "module", name])
    }

    pub(crate) fn generated_directory(&self, name: &str) -> String {
        filepath::join(&[self.module_directory(name).as_str(), GENERATED_DIRECTORY])
    }
}

#[derive(Clone)]
pub struct Generator {
    entities: Arc<IndexedCatalog>,
    dal: Arc<DalIndex>,
    versions: SupportedVersions,
}

/// The root of the plugin a description belongs to, `None` for any other
/// file.
pub(crate) fn plugin_root(config_path: &str) -> Option<String> {
    let path = filepath::to_slash(config_path);
    let root = path.strip_suffix(CONFIG_RELATIVE_PATH)?.strip_suffix('/')?;
    (!root.is_empty()).then(|| root.to_owned())
}

impl Generator {
    pub fn new(
        entities: Arc<IndexedCatalog>,
        dal: Arc<DalIndex>,
        versions: SupportedVersions,
    ) -> Self {
        Self {
            entities,
            dal,
            versions,
        }
    }

    /// The property that names a record of `entity`.
    fn label_property(&self, entity: &str) -> String {
        let definitions = self.dal.definition(entity).unwrap_or_default();
        let has = |name: &str| {
            definitions
                .iter()
                .flat_map(|definition| &definition.fields)
                .any(|field| field.name == name)
        };
        ["name", "title", "label", "technicalName"]
            .into_iter()
            .find(|name| has(name))
            .unwrap_or("id")
            .to_owned()
    }

    /// The entities the plugin at `plugin_root` defines; none while the
    /// workspace is being indexed or when they cannot be read.
    pub(crate) fn entities(&self, plugin_root: &str) -> Vec<Entity> {
        let Ok((_, definitions)) = self.entities.scan(plugin_root, None) else {
            return Vec::new();
        };
        definitions
            .iter()
            .map(|definition| {
                Entity::from_spec(&definition.spec, &|target| self.label_property(target))
            })
            .collect()
    }

    /// Renders every module `source`, the description of the plugin at
    /// `plugin_root`, describes.
    pub(crate) fn generate(
        &self,
        plugin_root: &str,
        source: &str,
    ) -> Result<Generation, GenerateError> {
        let config = adminmodule::parse(source)?;
        let mut generation = Generation {
            admin: filepath::join(&[plugin_root, ADMIN_SOURCE]),
            described: config.modules.keys().cloned().collect(),
            ..Generation::default()
        };
        let (_, definitions) = match self.entities.scan(plugin_root, None) {
            Ok(scanned) => scanned,
            Err(entityschema::Error::IndexNotReady) => return Err(GenerateError::NotReady),
            Err(error) => return Err(GenerateError::Entities(error.to_string())),
        };
        let style = match self.versions.minimum_for(plugin_root) {
            version if version.known && !version.at_least(6, 7, 0) => Style::Classic,
            _ => Style::Meteor,
        };
        for (name, module) in &config.modules {
            let Some(spec) = definitions
                .iter()
                .map(|definition| &definition.spec)
                .find(|spec| spec.entity_name == module.entity)
            else {
                let message = if module.entity.is_empty() {
                    "The module names no entity".to_owned()
                } else {
                    format!("Entity \"{}\" is not defined in this plugin", module.entity)
                };
                generation.problems.push(Problem {
                    path: vec!["modules".to_owned(), name.clone(), "entity".to_owned()],
                    message,
                });
                continue;
            };
            let entity = Entity::from_spec(spec, &|target| self.label_property(target));
            let (resolved, problems) = adminmodule::resolve(name, module, &entity);
            if !problems.is_empty() {
                generation.problems.extend(problems);
                continue;
            }
            let directory = generation.module_directory(name);
            let targets = adminmodule::render(&resolved, style)
                .into_iter()
                .map(|file| Target {
                    path: filepath::join(&[directory.as_str(), file.path.as_str()]),
                    content: file.content,
                    once: file.once,
                })
                .collect();
            generation.modules.insert(name.clone(), targets);
        }
        Ok(generation)
    }
}

#[cfg(test)]
pub(crate) mod tests;
