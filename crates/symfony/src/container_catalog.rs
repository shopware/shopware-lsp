//! The selected compiled container snapshot. The file scanner owns updates
//! through [`crate::CompiledCatalogIndex`].

use std::collections::HashMap;
use std::path::Path;
use std::sync::{PoisonError, RwLock, RwLockReadGuard, RwLockWriteGuard};
use std::time::SystemTime;

use swlsp_syntax::LineIndex;

use crate::model::{ContainerTwigComponent, ContainerTwigGlobal, Parameter, Service};
use crate::twig_components::parse_xml_twig_components_tree;
use crate::twig_globals::parse_xml_twig_globals_tree;
use crate::xml_services::parse_xml_services_tree;

#[derive(Default)]
pub(crate) struct ContainerState {
    pub(crate) container_path: String,
    pub(crate) services: HashMap<String, Service>,
    pub(crate) parameters: HashMap<String, Parameter>,
    pub(crate) twig_globals: Vec<ContainerTwigGlobal>,
    pub(crate) twig_components: Vec<ContainerTwigComponent>,
    pub(crate) revision: u64,
    pub(crate) last_updated: Option<SystemTime>,
    pub(crate) container_exists: bool,
}

/// Holds the selected compiled container snapshot.
pub struct ContainerCatalog {
    project_root: String,
    state: RwLock<ContainerState>,
}

impl ContainerCatalog {
    /// Creates an empty compiled container catalog.
    pub fn new(project_root: impl Into<String>) -> Self {
        Self {
            project_root: project_root.into(),
            state: RwLock::new(ContainerState::default()),
        }
    }

    pub(crate) fn read(&self) -> RwLockReadGuard<'_, ContainerState> {
        self.state.read().unwrap_or_else(PoisonError::into_inner)
    }

    pub(crate) fn write(&self) -> RwLockWriteGuard<'_, ContainerState> {
        self.state.write().unwrap_or_else(PoisonError::into_inner)
    }

    /// Locates and loads `var/cache/dev*/Shopware_Core_KernelDevDebugContainer.xml`.
    pub(crate) fn find_and_load_container(&self) -> std::io::Result<()> {
        match self.find_container_file() {
            Ok(path) => {
                {
                    let mut state = self.write();
                    state.container_path = path.clone();
                    state.container_exists = true;
                }
                self.load_container(&path)
            }
            Err(error) => {
                let mut state = self.write();
                state.container_exists = false;
                state.services.clear();
                state.parameters.clear();
                state.twig_globals.clear();
                state.twig_components.clear();
                state.revision += 1;
                Err(error)
            }
        }
    }

    fn find_container_file(&self) -> std::io::Result<String> {
        let cache_dir = Path::new(&self.project_root).join("var").join("cache");
        let mut matches: Vec<String> = Vec::new();
        for entry in std::fs::read_dir(&cache_dir)? {
            let Ok(entry) = entry else { continue };
            let name = entry.file_name().to_string_lossy().into_owned();
            if !name.starts_with("dev") {
                continue;
            }
            let candidate = entry
                .path()
                .join("Shopware_Core_KernelDevDebugContainer.xml");
            if std::fs::symlink_metadata(&candidate).is_ok() {
                matches.push(candidate.to_string_lossy().into_owned());
            }
        }
        // filepath.Glob returns matches in lexical order.
        matches.sort();
        matches
            .into_iter()
            .next()
            .ok_or_else(|| std::io::Error::from(std::io::ErrorKind::NotFound))
    }

    fn load_container(&self, path: &str) -> std::io::Result<()> {
        let content = std::fs::read(path)?;
        let source = String::from_utf8_lossy(&content);
        let parse = swlsp_parser_xml::parse(&source);
        let line_index = LineIndex::new(parse.tree.source_arc().clone());
        let (services, parameters) = parse_xml_services_tree(path, Some(&parse.tree), &line_index);
        let globals = parse_xml_twig_globals_tree(path, Some(&parse.tree));
        let components = parse_xml_twig_components_tree(path, Some(&parse.tree));
        let mut state = self.write();
        state.services = services
            .into_iter()
            .map(|service| (service.id.clone(), service))
            .collect();
        state.parameters = parameters
            .into_iter()
            .map(|parameter| (parameter.name.clone(), parameter))
            .collect();
        state.twig_globals = globals;
        state.twig_components = components;
        state.revision += 1;
        state.last_updated = Some(SystemTime::now());
        log::info!(
            "Loaded {} services and {} parameters from container XML",
            state.services.len(),
            state.parameters.len()
        );
        Ok(())
    }

    pub fn get_service_by_id(&self, id: &str) -> Option<Service> {
        self.read().services.get(id).cloned()
    }

    pub fn get_parameter_by_name(&self, name: &str) -> Option<Parameter> {
        self.read().parameters.get(name).cloned()
    }

    /// All service IDs in unspecified order.
    pub fn get_all_services(&self) -> Vec<String> {
        self.read().services.keys().cloned().collect()
    }

    /// All service definitions in unspecified order.
    pub fn get_all_service_definitions(&self) -> Vec<Service> {
        self.read().services.values().cloned().collect()
    }

    /// The catalog's mutation counter for cache invalidation.
    pub fn revision(&self) -> u64 {
        self.read().revision
    }

    pub fn get_all_parameters(&self) -> Vec<Parameter> {
        self.read().parameters.values().cloned().collect()
    }

    pub fn get_twig_components(&self) -> Vec<ContainerTwigComponent> {
        self.get_twig_components_state().0
    }

    pub fn get_twig_components_state(&self) -> (Vec<ContainerTwigComponent>, u64) {
        let state = self.read();
        (state.twig_components.clone(), state.revision)
    }

    pub fn get_twig_globals(&self) -> Vec<ContainerTwigGlobal> {
        self.read().twig_globals.clone()
    }

    pub fn container_exists(&self) -> bool {
        self.read().container_exists
    }

    pub fn last_updated(&self) -> Option<SystemTime> {
        self.read().last_updated
    }
}
