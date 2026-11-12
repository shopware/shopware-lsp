//! Planning of a services.xml or routes.xml conversion to YAML, including
//! the local XML files it imports.

use std::collections::BTreeMap;

use swlsp_indexer::CancelToken;

use crate::routes_yaml_convert::{Routes, convert_routes_to_yaml, parse_routes_xml};
use crate::services_yaml_convert::{ConvertError, convert_container_to_yaml};
use crate::services_yaml_model::{Container, XmlImports, parse_services_xml};
use crate::util::{clean_path, dir_path, extension, join_path};

/// Why a services.xml conversion could not be planned.
#[derive(Debug, thiserror::Error)]
pub enum PlanError {
    #[error("cancelled")]
    Cancelled,
    #[error("read {path}: {source}")]
    Read {
        path: String,
        #[source]
        source: std::io::Error,
    },
    #[error("{path}: {source}")]
    Convert {
        path: String,
        #[source]
        source: ConvertError,
    },
    #[error("check conversion target {target}: {source}")]
    CheckTarget {
        target: String,
        #[source]
        source: std::io::Error,
    },
    #[error("cannot convert {path}: {target} exists already")]
    TargetExists { path: String, target: String },
}

/// One lossless XML-to-YAML file replacement. Planning is side-effect free
/// so callers can validate and apply every file as one workspace edit.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ServicesXmlConversion {
    pub source_path: String,
    pub target_path: String,
    pub content: String,
}

/// Converts a services.xml file and its local XML imports without touching
/// the filesystem. `read` supplies the current document snapshots,
/// including unsaved editor buffers; `target_exists` protects existing YAML
/// configuration from being overwritten.
pub fn plan_services_xml_conversion(
    cancel: &CancelToken,
    path: &str,
    read: &mut dyn FnMut(&str) -> std::io::Result<String>,
    target_exists: &mut dyn FnMut(&str) -> std::io::Result<bool>,
) -> Result<Vec<ServicesXmlConversion>, PlanError> {
    plan_conversion(XmlConfigKind::Services, cancel, path, read, target_exists)
}

/// [`plan_services_xml_conversion`] for a routes.xml file and the route
/// files it imports.
pub fn plan_routes_xml_conversion(
    cancel: &CancelToken,
    path: &str,
    read: &mut dyn FnMut(&str) -> std::io::Result<String>,
    target_exists: &mut dyn FnMut(&str) -> std::io::Result<bool>,
) -> Result<Vec<ServicesXmlConversion>, PlanError> {
    plan_conversion(XmlConfigKind::Routes, cancel, path, read, target_exists)
}

/// Which Symfony XML configuration format a file and its imports have.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum XmlConfigKind {
    Services,
    Routes,
}

/// A parsed configuration file of either format.
enum XmlConfig {
    Services(Container),
    Routes(Routes),
}

impl XmlConfig {
    fn parse(kind: XmlConfigKind, content: &str) -> Result<Self, ConvertError> {
        Ok(match kind {
            XmlConfigKind::Services => XmlConfig::Services(parse_services_xml(content)?),
            XmlConfigKind::Routes => XmlConfig::Routes(parse_routes_xml(content)?),
        })
    }

    /// The resource and loader type of every import, to point them at
    /// converted files.
    fn imports(&mut self) -> Vec<(&mut String, Option<&mut String>)> {
        match self {
            XmlConfig::Services(container) => import_lists(container)
                .into_iter()
                .flat_map(|imports| imports.imports.iter_mut())
                .map(|import| (&mut import.resource, Some(&mut import.kind)))
                .collect(),
            XmlConfig::Routes(routes) => routes
                .import_references()
                .into_iter()
                .map(|import| (import.resource, import.kind))
                .collect(),
        }
    }

    fn to_yaml(&self) -> Result<String, ConvertError> {
        match self {
            XmlConfig::Services(container) => convert_container_to_yaml(container),
            XmlConfig::Routes(routes) => convert_routes_to_yaml(routes),
        }
    }
}

fn plan_conversion(
    kind: XmlConfigKind,
    cancel: &CancelToken,
    path: &str,
    read: &mut dyn FnMut(&str) -> std::io::Result<String>,
    target_exists: &mut dyn FnMut(&str) -> std::io::Result<bool>,
) -> Result<Vec<ServicesXmlConversion>, PlanError> {
    let mut planner = Planner {
        kind,
        cancel,
        read,
        target_exists,
        planned: BTreeMap::new(),
        contents: BTreeMap::new(),
    };
    planner.plan(&clean_path(path), None)?;
    let Planner {
        planned,
        mut contents,
        ..
    } = planner;
    Ok(planned
        .into_iter()
        .map(|(source, target)| ServicesXmlConversion {
            content: contents.remove(&source).unwrap_or_default(),
            source_path: source,
            target_path: target,
        })
        .collect())
}

struct Planner<'a> {
    kind: XmlConfigKind,
    cancel: &'a CancelToken,
    read: &'a mut dyn FnMut(&str) -> std::io::Result<String>,
    target_exists: &'a mut dyn FnMut(&str) -> std::io::Result<bool>,
    planned: BTreeMap<String, String>,
    contents: BTreeMap<String, String>,
}

fn import_lists(container: &mut Container) -> Vec<&mut XmlImports> {
    let mut lists: Vec<&mut XmlImports> = Vec::new();
    if let Some(imports) = container.imports.as_mut() {
        lists.push(imports);
    }
    for when in &mut container.when {
        if let Some(imports) = when.imports.as_mut() {
            lists.push(imports);
        }
    }
    lists
}

impl Planner<'_> {
    fn check_cancelled(&self) -> Result<(), PlanError> {
        if self.cancel.is_cancelled() {
            return Err(PlanError::Cancelled);
        }
        Ok(())
    }

    fn plan(&mut self, path: &str, prefetched: Option<String>) -> Result<(), PlanError> {
        self.check_cancelled()?;
        if self.planned.contains_key(path) {
            return Ok(());
        }
        let content = match prefetched {
            Some(content) => content,
            None => (self.read)(path).map_err(|source| PlanError::Read {
                path: path.to_owned(),
                source,
            })?,
        };
        let mut config =
            XmlConfig::parse(self.kind, &content).map_err(|source| PlanError::Convert {
                path: path.to_owned(),
                source,
            })?;
        let without_extension = &path[..path.len() - extension(path).len()];
        let target = format!("{without_extension}.yaml");
        for candidate in [target.clone(), format!("{without_extension}.yml")] {
            let exists =
                (self.target_exists)(&candidate).map_err(|source| PlanError::CheckTarget {
                    target: candidate.clone(),
                    source,
                })?;
            if exists {
                return Err(PlanError::TargetExists {
                    path: path.to_owned(),
                    target: candidate,
                });
            }
        }
        // Mark the file before descending so circular local imports end.
        self.planned.insert(path.to_owned(), target);
        let directory = dir_path(path);
        for (resource_slot, kind_slot) in config.imports() {
            self.check_cancelled()?;
            let resource = resource_slot.clone();
            if !resource.to_lowercase().ends_with(".xml")
                || resource.contains(['*', '?', '[', '{'])
                || resource.starts_with('/')
            {
                continue;
            }
            let import_path = join_path(&[&directory, &resource]);
            // Like Symfony's loader, unresolved optional/local imports
            // remain XML references.
            let Ok(import_content) = (self.read)(&import_path) else {
                continue;
            };
            self.plan(&import_path, Some(import_content))?;
            *resource_slot = format!("{}.yaml", &resource[..resource.len() - ".xml".len()]);
            if let Some(kind) = kind_slot
                && kind == "xml"
            {
                *kind = "yaml".to_owned();
            }
        }
        let converted = config.to_yaml().map_err(|source| PlanError::Convert {
            path: path.to_owned(),
            source,
        })?;
        self.contents.insert(path.to_owned(), converted);
        Ok(())
    }
}

#[cfg(test)]
#[path = "tests_yamlconv/services_yaml_plan.rs"]
mod tests;
