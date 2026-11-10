//! Per-workspace cache directories.

use std::path::{Path, PathBuf};

use sha2::{Digest, Sha256};
use swlsp_uriutil::filepath;

/// Environment variable relocating every workspace cache.
pub const CACHE_DIR_ENV: &str = "SHOPWARE_LSP_CACHE_DIR";

/// Why a workspace cache directory could not be resolved or created.
#[derive(Debug, thiserror::Error)]
pub enum CacheDirError {
    #[error("normalize project root: {0}")]
    NormalizeRoot(#[source] std::io::Error),
    #[error("create project cache directory: {0}")]
    Create(#[source] std::io::Error),
    #[error("resolve user cache directory: {0}; home directory is not defined")]
    Unresolved(#[source] PlatformCacheDirError),
}

/// Why the platform's user cache directory is unavailable.
#[derive(Debug, thiserror::Error)]
pub enum PlatformCacheDirError {
    #[error("{0} is not defined")]
    Undefined(&'static str),
    #[error("path in $XDG_CACHE_HOME is relative")]
    RelativeXdgCacheHome,
    #[error("neither $XDG_CACHE_HOME nor $HOME is defined")]
    NoXdgCacheHomeOrHome,
}

/// The persistent cache directory for a workspace, created on demand. CLI
/// diagnostics and statistics use the same location as the LSP runtime.
pub fn project_cache_folder(project_root: &str) -> Result<PathBuf, CacheDirError> {
    project_cache_folder_in(&user_cache_dir()?, project_root)
}

/// [`project_cache_folder`] below an explicit cache base directory instead
/// of the user cache directory.
pub fn project_cache_folder_in(
    cache_base: &Path,
    project_root: &str,
) -> Result<PathBuf, CacheDirError> {
    let root = filepath::abs(project_root).map_err(CacheDirError::NormalizeRoot)?;
    // Keep the complete root identity: separator substitution aliases
    // distinct workspaces, and long readable paths can exceed filesystem
    // name limits.
    let digest = Sha256::digest(filepath::clean(&root).as_bytes());
    let expected = cache_base.join("shopware-lsp").join(format!("{digest:x}"));
    std::fs::create_dir_all(&expected).map_err(CacheDirError::Create)?;
    disable_copy_on_write(&expected);
    Ok(expected)
}

/// Marks `directory` no-copy-on-write, which the files created in it
/// inherit. On a copy-on-write filesystem (btrfs) every page SQLite rewrites
/// is otherwise copied and checksummed, and compressed where compression is
/// on. The cache can be rebuilt, so it does not need that protection. Other
/// filesystems reject the flag, which is fine.
#[cfg(target_os = "linux")]
fn disable_copy_on_write(directory: &Path) {
    use rustix::fs::{IFlags, ioctl_getflags, ioctl_setflags};

    let Ok(handle) = std::fs::File::open(directory) else {
        return;
    };
    if let Ok(flags) = ioctl_getflags(&handle)
        && !flags.contains(IFlags::NOCOW)
    {
        let _ = ioctl_setflags(&handle, flags | IFlags::NOCOW);
    }
}

#[cfg(not(target_os = "linux"))]
fn disable_copy_on_write(_directory: &Path) {}

/// The cache base: `SHOPWARE_LSP_CACHE_DIR`, the platform user cache
/// directory, or `~/.config` as a last resort.
pub fn user_cache_dir() -> Result<PathBuf, CacheDirError> {
    if let Some(value) = std::env::var_os(CACHE_DIR_ENV)
        && !value.is_empty()
    {
        return Ok(PathBuf::from(value));
    }
    match platform_cache_dir() {
        Ok(directory) => Ok(directory),
        Err(cache_error) => match home_dir() {
            Some(home) => Ok(home.join(".config")),
            None => Err(CacheDirError::Unresolved(cache_error)),
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
fn platform_cache_dir() -> Result<PathBuf, PlatformCacheDirError> {
    std::env::var_os("LocalAppData")
        .filter(|value| !value.is_empty())
        .map(PathBuf::from)
        .ok_or(PlatformCacheDirError::Undefined("%LocalAppData%"))
}

#[cfg(target_os = "macos")]
fn platform_cache_dir() -> Result<PathBuf, PlatformCacheDirError> {
    home_dir()
        .map(|home| home.join("Library").join("Caches"))
        .ok_or(PlatformCacheDirError::Undefined("$HOME"))
}

#[cfg(not(any(target_os = "windows", target_os = "macos")))]
fn platform_cache_dir() -> Result<PathBuf, PlatformCacheDirError> {
    if let Some(value) = std::env::var_os("XDG_CACHE_HOME").filter(|value| !value.is_empty()) {
        let path = PathBuf::from(value);
        if !path.is_absolute() {
            return Err(PlatformCacheDirError::RelativeXdgCacheHome);
        }
        return Ok(path);
    }
    home_dir()
        .map(|home| home.join(".cache"))
        .ok_or(PlatformCacheDirError::NoXdgCacheHomeOrHome)
}
