//! `cache`: the workspace caches of this machine, and clearing them. No
//! workspace is opened, so it also works when a cache no longer does.

use std::fmt::Write as _;

use swlsp_app::{CacheEntry, CacheState, ClearScope, caches_dir, clear_caches, list_caches};
use swlsp_textutil::number::grouped;

use crate::args::{Cache, CacheClear, CacheCommand};
use crate::workspace_commands::format_bytes;
use crate::{CliError, Runner};

pub(crate) fn run_cache(runner: &Runner, flags: &Cache) -> Result<(), CliError> {
    match &flags.command {
        None => run_list(runner),
        Some(CacheCommand::Clear(clear)) => run_clear(runner, clear),
    }
}

fn cache_base() -> Result<std::path::PathBuf, CliError> {
    swlsp_app::user_cache_dir().map_err(|error| CliError::fail(error.to_string()))
}

fn run_list(runner: &Runner) -> Result<(), CliError> {
    let base = cache_base()?;
    let caches =
        list_caches(&base).map_err(|error| CliError::fail(format!("list caches: {error}")))?;
    if runner.json {
        return runner.write_json(&serde_json::json!({
            "directory": caches_dir(&base),
            "caches": caches,
        }));
    }
    let directory = caches_dir(&base);
    if caches.is_empty() {
        return runner.write_text(&format!("No caches in {}\n", directory.display()));
    }
    let mut text = String::new();
    for cache in &caches {
        let _ = writeln!(
            text,
            "{:>10}  {:<12}  {:<14}  {}{}",
            format_bytes(cache.bytes as i64),
            state_label(cache),
            idle_label(cache.idle_seconds),
            project_label(cache),
            if cache.stale { "  (stale)" } else { "" },
        );
    }
    let total: u64 = caches.iter().map(|cache| cache.bytes).sum();
    let stale = caches.iter().filter(|cache| cache.stale).count();
    let noun = if caches.len() == 1 { "cache" } else { "caches" };
    let _ = writeln!(
        text,
        "\n{} {noun}, {} in {}",
        grouped(caches.len()),
        format_bytes(total as i64),
        directory.display()
    );
    if stale > 0 {
        let _ = writeln!(
            text,
            "{} stale (project gone, or outdated and unused for a week): \
             `shopware-lsp cache clear --stale`",
            grouped(stale)
        );
    }
    runner.write_text(&text)
}

fn run_clear(runner: &Runner, flags: &CacheClear) -> Result<(), CliError> {
    let base = cache_base()?;
    let root;
    let scope = if flags.all {
        ClearScope::All
    } else if flags.stale {
        ClearScope::Stale
    } else {
        root = runner.workspace_root()?;
        ClearScope::Project(&root)
    };
    let report = clear_caches(&base, scope, flags.dry_run)
        .map_err(|error| CliError::fail(error.to_string()))?;
    if runner.json {
        return runner.write_json(&report);
    }
    let mut text = String::new();
    let verb = if report.dry_run {
        "Would clear"
    } else {
        "Cleared"
    };
    for cache in &report.cleared {
        let _ = writeln!(
            text,
            "{verb} {} ({})",
            project_label(cache),
            format_bytes(cache.bytes as i64)
        );
    }
    for cache in &report.in_use {
        let _ = writeln!(
            text,
            "Kept {}: a running server or command has it open. Stop it first, or rebuild \
             the index in place with `shopware-lsp index --force`.",
            project_label(cache)
        );
    }
    if report.cleared.is_empty() && report.in_use.is_empty() {
        text.push_str(match scope {
            ClearScope::Project(_) => "This project has no cache.\n",
            ClearScope::Stale => "No stale caches.\n",
            ClearScope::All => "No caches.\n",
        });
    } else if report.cleared.len() > 1 {
        let freed = if report.dry_run {
            "would be freed"
        } else {
            "freed"
        };
        let _ = writeln!(
            text,
            "{} caches, {} {freed}",
            grouped(report.cleared.len()),
            format_bytes(report.freed_bytes as i64)
        );
    }
    runner.write_text(&text)
}

fn state_label(cache: &CacheEntry) -> &'static str {
    if cache.in_use {
        return "in use";
    }
    match cache.state {
        CacheState::Current => "current",
        CacheState::Outdated => "outdated",
        CacheState::ProjectGone => "project gone",
        CacheState::Unknown => "unknown",
    }
}

fn project_label(cache: &CacheEntry) -> String {
    match &cache.root {
        Some(root) => root.clone(),
        None => {
            let name = cache
                .path
                .file_name()
                .map(|name| name.to_string_lossy().into_owned())
                .unwrap_or_default();
            // Enough of the hash to find the directory.
            let short: String = name.chars().take(12).collect();
            format!("(no project recorded) {short}")
        }
    }
}

/// How long ago the cache was written, in the largest fitting unit.
fn idle_label(seconds: Option<u64>) -> String {
    let Some(seconds) = seconds else {
        return "never used".to_owned();
    };
    let (count, unit) = match seconds {
        0..60 => return "just now".to_owned(),
        60..3_600 => (seconds / 60, "minute"),
        3_600..86_400 => (seconds / 3_600, "hour"),
        86_400..2_592_000 => (seconds / 86_400, "day"),
        _ => (seconds / 2_592_000, "month"),
    };
    let plural = if count == 1 { "" } else { "s" };
    format!("{count} {unit}{plural} ago")
}
