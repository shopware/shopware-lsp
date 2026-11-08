//! Per-workspace cache directories (port of Go `internal/app/cache.go`).

use std::path::{Path, PathBuf};

use sha2::{Digest, Sha256};
use swlsp_uriutil::filepath;

/// Environment variable relocating every workspace cache.
pub const CACHE_DIR_ENV: &str = "SHOPWARE_LSP_CACHE_DIR";

/// The persistent cache directory for a workspace, created on demand. CLI
/// diagnostics and statistics use the same location as the LSP runtime.
pub fn project_cache_folder(project_root: &str) -> Result<PathBuf, String> {
    project_cache_folder_in(&user_cache_dir()?, project_root)
}

/// [`project_cache_folder`] below an explicit cache base directory instead
/// of the user cache directory.
pub fn project_cache_folder_in(cache_base: &Path, project_root: &str) -> Result<PathBuf, String> {
    let root =
        filepath::abs(project_root).map_err(|error| format!("normalize project root: {error}"))?;
    // Keep the complete root identity: separator substitution aliases
    // distinct workspaces, and long readable paths can exceed filesystem
    // name limits.
    let digest = Sha256::digest(filepath::clean(&root).as_bytes());
    let expected = cache_base.join("shopware-lsp").join(format!("{digest:x}"));
    std::fs::create_dir_all(&expected)
        .map_err(|error| format!("create project cache directory: {error}"))?;
    Ok(expected)
}

/// The cache base: `SHOPWARE_LSP_CACHE_DIR`, the platform user cache
/// directory (Go `os.UserCacheDir`), or `~/.config` as a last resort.
pub fn user_cache_dir() -> Result<PathBuf, String> {
    if let Some(value) = std::env::var_os(CACHE_DIR_ENV)
        && !value.is_empty()
    {
        return Ok(PathBuf::from(value));
    }
    match platform_cache_dir() {
        Ok(directory) => Ok(directory),
        Err(cache_error) => match home_dir() {
            Some(home) => Ok(home.join(".config")),
            None => Err(format!(
                "resolve user cache directory: {cache_error}\n$HOME is not defined"
            )),
        },
    }
}

fn home_dir() -> Option<PathBuf> {
    let variable = if cfg!(windows) { "USERPROFILE" } else { "HOME" };
    std::env::var_os(variable)
        .filter(|value| !value.is_empty())
        .map(PathBuf::from)
}

#[cfg(target_os = "windows")]
fn platform_cache_dir() -> Result<PathBuf, String> {
    std::env::var_os("LocalAppData")
        .filter(|value| !value.is_empty())
        .map(PathBuf::from)
        .ok_or_else(|| "%LocalAppData% is not defined".to_owned())
}

#[cfg(target_os = "macos")]
fn platform_cache_dir() -> Result<PathBuf, String> {
    home_dir()
        .map(|home| home.join("Library").join("Caches"))
        .ok_or_else(|| "$HOME is not defined".to_owned())
}

#[cfg(not(any(target_os = "windows", target_os = "macos")))]
fn platform_cache_dir() -> Result<PathBuf, String> {
    if let Some(value) = std::env::var_os("XDG_CACHE_HOME").filter(|value| !value.is_empty()) {
        let path = PathBuf::from(value);
        if !path.is_absolute() {
            return Err("path in $XDG_CACHE_HOME is relative".to_owned());
        }
        return Ok(path);
    }
    home_dir()
        .map(|home| home.join(".cache"))
        .ok_or_else(|| "neither $XDG_CACHE_HOME nor $HOME are defined".to_owned())
}
