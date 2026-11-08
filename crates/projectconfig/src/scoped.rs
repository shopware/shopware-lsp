//! Nested, diagnostics-only extension configurations (port of
//! `internal/projectconfig/scoped.go`).

use std::collections::HashSet;
use std::io;

use serde::{Deserialize, Serialize};
use swlsp_uriutil::filepath;

use crate::config::{ConfigError, Partial, decode, path};

/// One nested diagnostics-only project configuration. `root` is the
/// directory containing `.config` and therefore owns every file below it.
#[derive(Debug, Clone, PartialEq, Eq, Default, Serialize, Deserialize)]
pub struct Scope {
    pub root: String,
    pub path: String,
    #[serde(default)]
    pub configuration: Partial,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
}

const SCOPED_DISCOVERY_SKIP_DIRECTORIES: &[&str] = &[
    ".git",
    ".idea",
    ".vscode",
    ".github",
    ".gitlab",
    ".run",
    "node_modules",
    "vendor",
    "vendor-bin",
    "var",
    "cache",
    "dist",
    "build",
    "public",
    "tests",
    "test",
    "fixtures",
    "_fixtures",
    "coverage",
    "out",
    ".tmp",
    ".direnv",
    ".devenv",
];

/// Go `filepath.EvalSymlinks` for an existing path.
fn eval_symlinks(path: &str) -> io::Result<String> {
    Ok(std::fs::canonicalize(path)?.to_string_lossy().into_owned())
}

struct Discovery {
    workspace_root: String,
    seen: HashSet<String>,
    visited_directories: HashSet<String>,
    result: Vec<Scope>,
}

impl Discovery {
    fn walk(&mut self, directory: &str) -> io::Result<()> {
        let mut entries = std::fs::read_dir(directory)?.collect::<io::Result<Vec<_>>>()?;
        // Go os.ReadDir returns entries sorted by file name.
        entries.sort_by_key(|entry| entry.file_name());
        for entry in entries {
            let name = entry.file_name().to_string_lossy().into_owned();
            if name == ".config" {
                let config_path = path(directory);
                match std::fs::metadata(&config_path) {
                    Ok(_) => {
                        if directory != self.workspace_root
                            && self.seen.insert(directory.to_owned())
                        {
                            self.result.push(load_scope(directory, &config_path));
                        }
                    }
                    Err(error) if error.kind() == io::ErrorKind::NotFound => {}
                    Err(error) => return Err(error),
                }
                continue;
            }
            if SCOPED_DISCOVERY_SKIP_DIRECTORIES.contains(&name.as_str()) {
                continue;
            }
            let child = filepath::join(&[directory, &name]);
            let file_type = entry.file_type()?;
            let is_symlink = file_type.is_symlink();
            let mut is_directory = file_type.is_dir();
            if !is_directory && is_symlink {
                match std::fs::metadata(&child) {
                    Ok(metadata) => is_directory = metadata.is_dir(),
                    Err(error) if error.kind() == io::ErrorKind::NotFound => {}
                    Err(error) => return Err(error),
                }
            }
            if !is_directory {
                continue;
            }
            if is_symlink {
                let real_child = filepath::clean(&eval_symlinks(&child)?);
                if !self.visited_directories.insert(real_child) {
                    continue;
                }
            }
            self.walk(&child)?;
        }
        Ok(())
    }
}

/// Discovers diagnostics-only configurations below `workspace_root`. The
/// workspace-root configuration itself remains owned by [`crate::load`]
/// (Go `LoadScopes`).
pub fn load_scopes(workspace_root: &str) -> Result<Vec<Scope>, ConfigError> {
    let workspace_root = filepath::abs(workspace_root)
        .map_err(|error| ConfigError(format!("resolve configuration workspace: {error}")))?;
    let workspace_root = filepath::clean(&workspace_root);
    let mut discovery = Discovery {
        workspace_root: workspace_root.clone(),
        seen: HashSet::new(),
        visited_directories: HashSet::new(),
        result: Vec::new(),
    };
    if let Ok(real_root) = eval_symlinks(&workspace_root) {
        discovery
            .visited_directories
            .insert(filepath::clean(&real_root));
    }
    discovery.walk(&workspace_root).map_err(|error| {
        ConfigError(format!(
            "discover nested Shopware LSP configurations: {error}"
        ))
    })?;
    let mut result = discovery.result;
    result.sort_by(|left, right| {
        path_depth(&left.root)
            .cmp(&path_depth(&right.root))
            .then_with(|| left.root.cmp(&right.root))
    });
    Ok(result)
}

fn load_scope(root: &str, config_path: &str) -> Scope {
    let mut result = Scope {
        root: root.to_owned(),
        path: config_path.to_owned(),
        ..Scope::default()
    };
    let outcome = std::fs::read(config_path)
        .map_err(|error| ConfigError(error.to_string()))
        .and_then(|data| decode(&data))
        .and_then(|configuration| {
            validate_nested(&configuration)?;
            Ok(configuration)
        });
    match outcome {
        Ok(configuration) => result.configuration = configuration,
        Err(error) => result.error = format!("{config_path}: {error}"),
    }
    result
}

/// Rejects settings whose behavior cannot be scoped safely to a nested
/// extension repository (Go `ValidateNested`).
pub fn validate_nested(value: &Partial) -> Result<(), ConfigError> {
    if value.php.is_some()
        || value.shopware.is_some()
        || !value.features.is_empty()
        || value.indexing.is_some()
        || !value.domains.is_empty()
        || value.check.is_some()
    {
        return Err(ConfigError::new(
            "nested configuration may only contain diagnostics",
        ));
    }
    Ok(())
}

/// Joins every invalid nested configuration for strict CLI startup (Go
/// `ScopeErrors`; `Ok` stands for a nil joined error).
pub fn scope_errors(values: &[Scope]) -> Result<(), ConfigError> {
    let errors: Vec<&str> = values
        .iter()
        .filter(|value| !value.error.is_empty())
        .map(|value| value.error.as_str())
        .collect();
    if errors.is_empty() {
        return Ok(());
    }
    Err(ConfigError(errors.join("\n")))
}

/// Reports whether `candidate` belongs to `root` without resolving symlinks,
/// preserving the workspace-visible path used by editor URIs (Go
/// `Contains`).
pub fn contains(root: &str, candidate: &str) -> bool {
    let Ok(relative) = filepath::rel(&filepath::clean(root), &filepath::clean(candidate)) else {
        return false;
    };
    let parent_prefix = format!("..{}", filepath::SEPARATOR);
    relative != ".." && !relative.starts_with(&parent_prefix)
}

fn path_depth(path: &str) -> usize {
    filepath::to_slash(&filepath::clean(path))
        .matches('/')
        .count()
}
