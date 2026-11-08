//! The selected compiled URL generator route table.

use std::collections::HashMap;
use std::path::Path;
use std::sync::{PoisonError, RwLock};
use std::time::SystemTime;

use crate::model::Route;

#[derive(Default)]
struct RouteState {
    path: String,
    routes: HashMap<String, Route>,
    revision: u64,
}

pub struct CompiledRouteCatalog {
    project_root: String,
    state: RwLock<RouteState>,
}

impl CompiledRouteCatalog {
    pub fn new(project_root: impl Into<String>) -> Self {
        Self {
            project_root: project_root.into(),
            state: RwLock::new(RouteState::default()),
        }
    }

    /// Re-reads the preferred compiled route file from disk.
    pub fn refresh(&self) -> std::io::Result<()> {
        let path = match self.find_route_file() {
            Ok(path) => path,
            Err(error) => {
                if error.kind() == std::io::ErrorKind::NotFound {
                    self.publish("", Vec::new());
                }
                return Err(error);
            }
        };
        let content = std::fs::read(&path)?;
        let routes = parse_compiled_routes(&path, &String::from_utf8_lossy(&content));
        self.publish(&path, routes);
        Ok(())
    }

    /// Routes sorted by name and file, plus the catalog revision.
    pub fn routes(&self) -> (Vec<Route>, u64) {
        let state = self.state.read().unwrap_or_else(PoisonError::into_inner);
        let mut result: Vec<Route> = state.routes.values().cloned().collect();
        result.sort_by(|left, right| {
            left.name
                .cmp(&right.name)
                .then_with(|| left.file_path.cmp(&right.file_path))
        });
        (result, state.revision)
    }

    pub fn route(&self, name: &str) -> Option<Route> {
        if name.is_empty() {
            return None;
        }
        let state = self.state.read().unwrap_or_else(PoisonError::into_inner);
        state.routes.get(name).cloned()
    }

    pub fn path(&self) -> String {
        self.state
            .read()
            .unwrap_or_else(PoisonError::into_inner)
            .path
            .clone()
    }

    pub fn revision(&self) -> u64 {
        self.state
            .read()
            .unwrap_or_else(PoisonError::into_inner)
            .revision
    }

    pub(crate) fn publish(&self, path: &str, routes: Vec<Route>) {
        let mut next = HashMap::with_capacity(routes.len());
        for route in routes {
            if !route.name.is_empty() {
                next.insert(route.name.clone(), route);
            }
        }
        let mut state = self.state.write().unwrap_or_else(PoisonError::into_inner);
        state.path = path.to_owned();
        state.routes = next;
        state.revision += 1;
    }

    fn cache_directories(&self) -> [std::path::PathBuf; 2] {
        let root = Path::new(&self.project_root);
        [
            root.join("var").join("cache"),
            root.join("app").join("cache"),
        ]
    }

    fn find_route_file(&self) -> std::io::Result<String> {
        struct Candidate {
            path: String,
            environment_dev: bool,
            modern: bool,
            modified: Option<SystemTime>,
        }
        let mut candidates: Vec<Candidate> = Vec::new();
        for cache_dir in self.cache_directories() {
            let Ok(environments) = std::fs::read_dir(&cache_dir) else {
                continue;
            };
            for environment in environments.flatten() {
                if !environment.file_type().is_ok_and(|kind| kind.is_dir()) {
                    continue;
                }
                let environment_name = environment.file_name().to_string_lossy().into_owned();
                let Ok(files) = std::fs::read_dir(environment.path()) else {
                    continue;
                };
                for file in files.flatten() {
                    let name = file.file_name().to_string_lossy().into_owned();
                    if file.file_type().is_ok_and(|kind| kind.is_dir())
                        || !is_compiled_route_file_name(&name)
                    {
                        continue;
                    }
                    let Ok(metadata) = file.metadata() else {
                        continue;
                    };
                    candidates.push(Candidate {
                        path: environment
                            .path()
                            .join(&name)
                            .to_string_lossy()
                            .into_owned(),
                        environment_dev: environment_name.to_lowercase().starts_with("dev"),
                        modern: name == "url_generating_routes.php",
                        modified: metadata.modified().ok(),
                    });
                }
            }
        }
        candidates.sort_by(|left, right| {
            right
                .environment_dev
                .cmp(&left.environment_dev)
                .then_with(|| right.modern.cmp(&left.modern))
                .then_with(|| right.modified.cmp(&left.modified))
                .then_with(|| left.path.cmp(&right.path))
        });
        candidates
            .into_iter()
            .next()
            .map(|candidate| candidate.path)
            .ok_or_else(|| std::io::Error::from(std::io::ErrorKind::NotFound))
    }
}

pub(crate) fn is_compiled_route_file_name(name: &str) -> bool {
    name == "url_generating_routes.php"
        || name == "UrlGenerator.php"
        || name.ends_with("UrlGenerator.php")
}

/// Parses a compiled route file.
pub fn parse_compiled_routes(path: &str, source: &str) -> Vec<Route> {
    let parse = swlsp_parser_php::parse(source);
    parse_compiled_routes_tree(path, Some(&parse.tree))
}

/// Parses a compiled route file tree.
pub fn parse_compiled_routes_tree(path: &str, tree: Option<&swlsp_syntax::Tree>) -> Vec<Route> {
    crate::compiled_routes::parse_compiled_routes_tree(path, tree)
}
