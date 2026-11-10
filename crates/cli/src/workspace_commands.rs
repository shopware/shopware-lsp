//! `index`, `stats` and `check`.

use std::collections::{BTreeMap, HashSet};
use std::fmt::Write as _;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, Instant};

use serde::{Deserialize, Serialize};
use serde_json::json;
use swlsp_indexer::{PathExclusions, VendorPackages, is_scanned_path, should_skip_relative_path};
use swlsp_uriutil::filepath;

use crate::args::Check;
use crate::report::{DisplayPaths, Format, Report};
use crate::session::{DiagnosticOutput, diagnostic_severity_value};
use crate::terminal::{DIM, GREEN, short_duration};
use crate::{Cancel, CliError, INDEXING, Runner};
use swlsp_textutil::number::grouped;

/// Mirror of the indexer's `FileScannerStats` wire shape.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct ScannerStats {
    pub tracked_files: i64,
    pub tracked_bytes: i64,
    pub indexers: i64,
    pub workers: i64,
    pub max_file_size_bytes: i64,
    pub skipped_oversized_count: i64,
    pub skipped_oversized_bytes: i64,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub largest_indexed_files: Vec<FileSize>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub largest_skipped_files: Vec<SkippedFile>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct FileSize {
    pub path: String,
    pub bytes: i64,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct SkippedFile {
    pub path: String,
    pub bytes: i64,
    pub limit_bytes: i64,
    pub reason: String,
}

/// A duration in seconds rounded to milliseconds, as `1s 234ms`.
pub(crate) fn seconds_duration(seconds: f64) -> String {
    let millis = (seconds * 1e3).round();
    let millis = if millis.is_finite() && millis > 0.0 {
        millis as u64
    } else {
        0
    };
    humantime::format_duration(std::time::Duration::from_millis(millis)).to_string()
}

pub(crate) fn run_index(runner: &Runner, force: bool) -> Result<(), CliError> {
    let session = runner.connect()?;
    let mut result = session.initial_index;
    if force {
        session.call_raw("shopware/forceReindex", json!({}))?;
        let status = runner.status(INDEXING);
        let reindexed =
            session.wait_for_index_with(|files| status.set_message(format!("{INDEXING} {files}")));
        drop(status);
        result = reindexed?;
    }
    let scanner: ScannerStats = session
        .call::<Option<ScannerStats>>("shopware/index/stats", json!({}))?
        .unwrap_or_default();
    let root = session.root.clone();
    let _ = session.close();
    if runner.json {
        #[derive(Serialize)]
        struct Output<'a> {
            forced: bool,
            index: &'a ScannerStats,
            root: &'a str,
            #[serde(rename = "timeInSeconds")]
            time_in_seconds: f64,
        }
        return runner.write_json(&Output {
            forced: force,
            index: &scanner,
            root: &root,
            time_in_seconds: result.time_in_seconds,
        });
    }
    let style = runner.color.style(&runner.out);
    let mark = if runner.out.is_terminal() {
        format!("{} ", style.paint(GREEN, "✓"))
    } else {
        String::new()
    };
    let mut formatted = format!(
        "{mark}Indexed {} files ({}) with {} indexers in {}\n",
        grouped(scanner.tracked_files.max(0) as usize),
        format_bytes(scanner.tracked_bytes),
        scanner.indexers,
        short_duration(Duration::from_secs_f64(result.time_in_seconds.max(0.0))),
    );
    if scanner.skipped_oversized_count > 0 {
        let _ = writeln!(
            formatted,
            "Skipped {} oversized files ({}); run stats for details",
            scanner.skipped_oversized_count,
            format_bytes(scanner.skipped_oversized_bytes)
        );
    }
    runner.write_text(&formatted)
}

#[derive(Debug, Clone, Default, Serialize)]
struct CacheStats {
    path: String,
    files: i64,
    bytes: i64,
}

/// Process memory figures. There is no garbage-collected heap to report, so
/// the fields come from `/proc/self/status`
/// (resident set for the heap figures, peak virtual size as system bytes)
/// and `collections` is always zero.
#[derive(Debug, Clone, Default, Serialize)]
#[serde(rename_all = "camelCase")]
struct MemoryStats {
    alloc_bytes: u64,
    heap_alloc_bytes: u64,
    heap_in_use_bytes: u64,
    heap_objects: u64,
    system_bytes: u64,
    total_alloc_bytes: u64,
    collections: u32,
}

fn memory_stats() -> MemoryStats {
    let status = std::fs::read_to_string("/proc/self/status").unwrap_or_default();
    let field = |name: &str| -> u64 {
        status
            .lines()
            .find_map(|line| line.strip_prefix(name))
            .and_then(|rest| {
                rest.trim()
                    .trim_end_matches("kB")
                    .trim()
                    .parse::<u64>()
                    .ok()
            })
            .unwrap_or(0)
            * 1024
    };
    let resident = field("VmRSS:");
    MemoryStats {
        alloc_bytes: resident,
        heap_alloc_bytes: resident,
        heap_in_use_bytes: resident,
        heap_objects: 0,
        system_bytes: field("VmPeak:"),
        total_alloc_bytes: field("VmHWM:"),
        collections: 0,
    }
}

pub(crate) fn run_stats(runner: &Runner) -> Result<(), CliError> {
    let session = runner.connect()?;
    let scanner: ScannerStats = session
        .call::<Option<ScannerStats>>("shopware/index/stats", json!({}))?
        .unwrap_or_default();
    let cache_dir = swlsp_app::project_cache_folder(&session.root)
        .map_err(|error| CliError::fail(error.to_string()))?;
    let cache = inspect_cache(&cache_dir.to_string_lossy())?;
    let memory = memory_stats();
    let root = session.root.clone();
    let initial = session.initial_index;
    let _ = session.close();
    if runner.json {
        #[derive(Serialize)]
        #[serde(rename_all = "camelCase")]
        struct Output<'a> {
            cache: &'a CacheStats,
            index: &'a ScannerStats,
            initial_index_in_seconds: f64,
            memory: &'a MemoryStats,
            root: &'a str,
            version: &'a str,
        }
        return runner.write_json(&Output {
            cache: &cache,
            index: &scanner,
            initial_index_in_seconds: initial.time_in_seconds,
            memory: &memory,
            root: &root,
            version: runner.version(),
        });
    }
    let mut formatted = String::new();
    let _ = write!(
        formatted,
        "Workspace:       {}\nIndex duration:  {}\nTracked files:   {} ({})\nMax file size:   {}\nSkipped files:   {} ({})\nIndexers:        {}\nIndex workers:   {}\nCache:           {} in {} files\nHeap allocated:  {} ({} objects)\nHeap in use:     {}\nRuntime system:  {}\nTotal allocated: {}\nGC collections:  {}\n",
        root,
        seconds_duration(initial.time_in_seconds),
        scanner.tracked_files,
        format_bytes(scanner.tracked_bytes),
        format_file_size_limit(scanner.max_file_size_bytes),
        scanner.skipped_oversized_count,
        format_bytes(scanner.skipped_oversized_bytes),
        scanner.indexers,
        scanner.workers,
        format_bytes(cache.bytes),
        cache.files,
        format_bytes(memory.heap_alloc_bytes as i64),
        memory.heap_objects,
        format_bytes(memory.heap_in_use_bytes as i64),
        format_bytes(memory.system_bytes as i64),
        format_bytes(memory.total_alloc_bytes as i64),
        memory.collections,
    );
    if !scanner.largest_indexed_files.is_empty() {
        formatted.push_str("Largest indexed files:\n");
        for file in &scanner.largest_indexed_files {
            let _ = writeln!(
                formatted,
                "  {:>9}  {}",
                format_bytes(file.bytes),
                display_workspace_path(&root, &file.path)
            );
        }
    }
    if !scanner.largest_skipped_files.is_empty() {
        formatted.push_str("Largest skipped files:\n");
        for file in &scanner.largest_skipped_files {
            let _ = writeln!(
                formatted,
                "  {:>9}  {} ({})",
                format_bytes(file.bytes),
                display_workspace_path(&root, &file.path),
                file.reason
            );
        }
    }
    runner.write_text(&formatted)
}

fn inspect_cache(path: &str) -> Result<CacheStats, CliError> {
    let mut result = CacheStats {
        path: path.to_owned(),
        ..CacheStats::default()
    };
    fn walk(path: &std::path::Path, result: &mut CacheStats) -> Result<(), CliError> {
        let metadata = std::fs::symlink_metadata(path)
            .map_err(|error| CliError::fail(format!("{}: {error}", path.display())))?;
        if metadata.is_dir() {
            let mut entries: Vec<_> = std::fs::read_dir(path)
                .map_err(|error| CliError::fail(format!("{}: {error}", path.display())))?
                .filter_map(Result::ok)
                .map(|entry| entry.path())
                .collect();
            entries.sort();
            for entry in entries {
                walk(&entry, result)?;
            }
        } else {
            result.files += 1;
            result.bytes += metadata.len() as i64;
        }
        Ok(())
    }
    walk(std::path::Path::new(path), &mut result)?;
    Ok(result)
}

/// A byte count in binary units with one decimal (`1.5 MiB`).
pub fn format_bytes(bytes: i64) -> String {
    const UNIT: i64 = 1024;
    if bytes < UNIT {
        return format!("{bytes} B");
    }
    let mut value = bytes as f64;
    let units = ["KiB", "MiB", "GiB", "TiB"];
    for (index, suffix) in units.iter().enumerate() {
        value /= UNIT as f64;
        if value < UNIT as f64 || index == units.len() - 1 {
            return format!("{value:.1} {suffix}");
        }
    }
    format!("{bytes} B")
}

fn format_file_size_limit(bytes: i64) -> String {
    if bytes == 0 {
        return "disabled (32-bit parser limit remains)".into();
    }
    format_bytes(bytes)
}

fn display_workspace_path(root: &str, path: &str) -> String {
    match filepath::rel(root, path) {
        Ok(relative) if relative != ".." && !relative.starts_with("../") => {
            filepath::to_slash(&relative)
        }
        _ => path.to_owned(),
    }
}

pub(crate) fn run_check(runner: &Runner, flags: &Check) -> Result<(), CliError> {
    let parallelism = std::thread::available_parallelism()
        .map(|count| count.get())
        .unwrap_or(1);
    let workers = flags.workers.unwrap_or(parallelism.min(4) as i64);
    if workers < 1 {
        return Err(CliError::usage("check workers must be at least 1"));
    }
    let session = runner.connect()?;
    let paths = resolve_check_files_with_exclusions(
        &runner.cancel,
        &session.root,
        &flags.paths,
        &session.configuration.exclude,
    )?;
    let mut effective_severity = flags.severity.clone().unwrap_or_default();
    if effective_severity.is_empty() {
        effective_severity = session.configuration.severity.clone();
    }
    let cutoff = diagnostic_severity(&effective_severity)?;
    let mut effective_fail_on = flags.fail_on.clone().unwrap_or_default();
    if effective_fail_on.is_empty() {
        effective_fail_on = session.configuration.fail_on.clone();
    }
    let mut fail_on = 0;
    if !effective_fail_on.is_empty() && !effective_fail_on.eq_ignore_ascii_case("off") {
        fail_on = diagnostic_severity(&effective_fail_on)?;
    }
    let format = Format::parse(flags.format.as_deref(), runner.out.is_terminal())?;
    let mut report = Report::new(
        format,
        runner.color.style(&runner.out),
        DisplayPaths::new(&session.root),
    );
    let err_style = runner.color.style(&runner.err_out);
    let mut status = runner.status("Checking…");
    if status.is_live() {
        // The live line replaced the one `connect` showed while indexing.
        let files = session
            .call::<Option<ScannerStats>>("shopware/index/stats", json!({}))
            .ok()
            .flatten()
            .map(|stats| format!(" ({} files)", grouped(stats.tracked_files.max(0) as usize)))
            .unwrap_or_default();
        let ready = format!(
            "{} Workspace ready{files} in {}\n",
            err_style.paint(GREEN, "✓"),
            err_style.paint(
                DIM,
                &short_duration(Duration::from_secs_f64(
                    session.initial_index.time_in_seconds.max(0.0)
                ))
            ),
        );
        status.suspend(|| runner.write_err(&ready))?;
    }
    let started = Instant::now();
    let total = paths.len();
    let progress = |done: usize| format!("Checking {}/{} files", grouped(done), grouped(total));
    status.set_message(progress(0));

    let next = AtomicUsize::new(0);
    let worker_count = (workers as usize).min(total);
    let mut findings = Vec::new();
    let mut failed = false;
    let mut errors = Vec::new();
    let mut write_error = None;
    std::thread::scope(|scope| {
        let (sender, results) = crossbeam_channel::unbounded();
        for _ in 0..worker_count {
            let sender = sender.clone();
            let (session, paths, next) = (&session, &paths, &next);
            scope.spawn(move || {
                loop {
                    let index = next.fetch_add(1, Ordering::Relaxed);
                    if index >= paths.len() {
                        break;
                    }
                    let result = session.check_document(&paths[index], cutoff);
                    if sender.send((index, result)).is_err() {
                        break;
                    }
                }
            });
        }
        drop(sender);
        // Files finish out of order; each is reported once every file
        // before it is, so the report reads in path order while it streams.
        let mut finished = BTreeMap::new();
        let mut reported = 0;
        for (done, (index, result)) in results.iter().enumerate() {
            status.set_message(progress(done + 1));
            match result {
                Ok(items) => {
                    finished.insert(index, items);
                }
                Err(error) => {
                    errors.push((index, error.message));
                    finished.insert(index, Vec::new());
                }
            }
            while let Some(items) = finished.remove(&reported) {
                reported += 1;
                failed |= findings_at_severity(&items, fail_on);
                if runner.json {
                    findings.extend(items);
                    continue;
                }
                let text = report.file(&items);
                if text.is_empty() || write_error.is_some() {
                    continue;
                }
                if let Err(error) = status.suspend(|| runner.write_text(&text)) {
                    // Nobody reads the report any more: stop checking.
                    write_error = Some(error);
                    next.store(paths.len(), Ordering::Relaxed);
                }
            }
        }
    });
    status.finish();
    let _ = session.close();
    if let Some(error) = write_error {
        return Err(error);
    }
    if !errors.is_empty() {
        errors.sort();
        let messages: Vec<String> = errors.into_iter().map(|(_, message)| message).collect();
        return Err(CliError::fail(messages.join("\n")));
    }
    if runner.json {
        runner.write_json(&findings)?;
        if failed {
            return Err(CliError::exit(
                1,
                format!("diagnostics matched fail-on {effective_fail_on}"),
            ));
        }
        return Ok(());
    }
    // The summary goes to stderr: stdout stays one finding per line for
    // whatever reads the concise report.
    runner.write_err(&report.summary(err_style, total, started.elapsed()))?;
    if failed {
        // The summary already says what was found.
        return Err(CliError::exit(1, ""));
    }
    Ok(())
}

/// Expands targets to scanned files, honoring exclusions below the
/// workspace root.
pub fn resolve_check_files_with_exclusions(
    cancel: &Cancel,
    workspace_root: &str,
    targets: &[String],
    excluded_patterns: &[String],
) -> Result<Vec<String>, CliError> {
    let vendors = VendorPackages::new(workspace_root);
    let exclusions = PathExclusions::new(excluded_patterns)
        .map_err(|error| CliError::fail(format!("configure excluded check paths: {error}")))?;
    let mut seen = HashSet::new();
    let mut files = Vec::new();
    let mut add = |path: String| {
        let path = filepath::clean(&path);
        if seen.insert(path.clone()) {
            files.push(path);
        }
    };
    for target in targets {
        if cancel.is_cancelled() {
            return Err(CliError::cancelled());
        }
        let mut path = target.clone();
        if path.starts_with("file://") {
            path = swlsp_uriutil::path(&path).map_err(|error| {
                CliError::fail(format!("resolve check target {:?}: {error}", target))
            })?;
        }
        let absolute = filepath::abs(&path).map_err(|error| {
            CliError::fail(format!("resolve check target {:?}: {error}", target))
        })?;
        let metadata = std::fs::metadata(&absolute).map_err(|error| {
            CliError::fail(format!(
                "inspect check target {:?}: {absolute}: {error}",
                target
            ))
        })?;
        if !metadata.is_dir() {
            add(absolute);
            continue;
        }
        let mut walk_error = None;
        walk_check_directory(
            cancel,
            &absolute,
            &absolute,
            workspace_root,
            &exclusions,
            &vendors,
            &mut add,
            &mut walk_error,
        );
        if let Some(error) = walk_error {
            return Err(CliError::fail(format!(
                "walk check target {:?}: {error}",
                target
            )));
        }
    }
    files.sort();
    Ok(files)
}

#[allow(clippy::too_many_arguments)]
fn walk_check_directory(
    cancel: &Cancel,
    base: &str,
    directory: &str,
    workspace_root: &str,
    exclusions: &PathExclusions,
    vendors: &VendorPackages,
    add: &mut dyn FnMut(String),
    error: &mut Option<String>,
) {
    let entries = match std::fs::read_dir(directory) {
        Ok(entries) => entries,
        Err(cause) => {
            *error = Some(format!("{directory}: {cause}"));
            return;
        }
    };
    let mut entries: Vec<(String, bool)> = entries
        .filter_map(Result::ok)
        .map(|entry| {
            let is_dir = entry.file_type().map(|kind| kind.is_dir()).unwrap_or(false);
            (entry.file_name().to_string_lossy().into_owned(), is_dir)
        })
        .collect();
    entries.sort();
    for (name, is_dir) in entries {
        if error.is_some() {
            return;
        }
        if cancel.is_cancelled() {
            *error = Some("cancelled".into());
            return;
        }
        let path = filepath::join(&[directory, name.as_str()]);
        let relative = filepath::rel(base, &path).unwrap_or_default();
        let workspace_relative = relative_check_path(workspace_root, &path);
        if is_dir {
            let skip = should_skip_relative_path(&relative)
                || workspace_relative
                    .as_ref()
                    .is_some_and(|relative| exclusions.excludes_directory(relative))
                || workspace_relative
                    .as_ref()
                    .is_some_and(|relative| vendors.duplicates_root_package(relative));
            if !skip {
                walk_check_directory(
                    cancel,
                    base,
                    &path,
                    workspace_root,
                    exclusions,
                    vendors,
                    add,
                    error,
                );
            }
            continue;
        }
        if should_skip_relative_path(&relative)
            || workspace_relative
                .as_ref()
                .is_some_and(|relative| exclusions.excludes(relative))
            || workspace_relative
                .as_ref()
                .is_some_and(|relative| vendors.duplicates_root_package(relative))
            || !is_scanned_path(&path)
        {
            continue;
        }
        add(path);
    }
}

fn relative_check_path(root: &str, candidate: &str) -> Option<String> {
    if root.is_empty() {
        return None;
    }
    match filepath::rel(&filepath::clean(root), &filepath::clean(candidate)) {
        Ok(relative) if relative != ".." && !relative.starts_with("../") => Some(relative),
        _ => None,
    }
}

fn findings_at_severity(findings: &[DiagnosticOutput], cutoff: i32) -> bool {
    if cutoff == 0 {
        return false;
    }
    findings
        .iter()
        .any(|finding| diagnostic_severity_value(&finding.diagnostic) <= cutoff)
}

/// Parses a `--severity` / `--fail-on` name into an LSP severity number.
pub(crate) fn diagnostic_severity(value: &str) -> Result<i32, CliError> {
    match value.trim().to_lowercase().as_str() {
        "hint" => Ok(4),
        "info" | "information" => Ok(3),
        "warning" | "warn" => Ok(2),
        "error" => Ok(1),
        _ => Err(CliError::fail(format!(
            "unknown diagnostic severity {:?}",
            value
        ))),
    }
}

/// The LSP name of a severity number; unknown values read as a warning.
pub(crate) fn severity_label(severity: i32) -> &'static str {
    match severity {
        1 => "Error",
        3 => "Information",
        4 => "Hint",
        _ => "Warning",
    }
}

/// A diagnostic code as text: strings unquoted, numbers as written.
pub(crate) fn code_text(value: &serde_json::Value) -> String {
    match value {
        serde_json::Value::String(value) => value.clone(),
        value => value.to_string(),
    }
}
