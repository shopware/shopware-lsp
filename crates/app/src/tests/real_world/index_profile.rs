//! A small opt-in resource harness for
//! the production workspace composition (construction, cold indexing, warm
//! no-op scan, retained memory and on-disk cache size).
//!
//! Memory is reported as the process's resident set (`/proc/self/status`
//! VmRSS/VmHWM); allocation counts are not reported.

use std::path::Path;
use std::time::Instant;

use swlsp_lsp::{Context, Server};

use crate::Workspace;

fn status_kib(field: &str) -> u64 {
    std::fs::read_to_string("/proc/self/status")
        .ok()
        .and_then(|status| {
            status.lines().find_map(|line| {
                let value = line.strip_prefix(field)?.strip_prefix(':')?;
                value.trim().trim_end_matches("kB").trim().parse().ok()
            })
        })
        .unwrap_or(0)
}

/// RSS after returning freed heap pages to the OS, so allocator retention
/// does not hide the live set (glibc only).
fn trimmed_rss() -> u64 {
    #[cfg(all(target_os = "linux", target_env = "gnu"))]
    {
        unsafe extern "C" {
            fn malloc_trim(pad: usize) -> i32;
        }
        // SAFETY: glibc's malloc_trim has no preconditions.
        unsafe { malloc_trim(0) };
    }
    status_kib("VmRSS") * 1024
}

fn format_bytes(value: u64) -> String {
    const KIB: f64 = 1024.0;
    const MIB: f64 = 1024.0 * KIB;
    const GIB: f64 = 1024.0 * MIB;
    let value = value as f64;
    if value >= GIB {
        format!("{:.2} GiB", value / GIB)
    } else if value >= MIB {
        format!("{:.2} MiB", value / MIB)
    } else if value >= KIB {
        format!("{:.2} KiB", value / KIB)
    } else {
        format!("{value} B")
    }
}

fn directory_entries(root: &Path) -> Vec<(String, u64)> {
    let mut entries = Vec::new();
    let mut stack = vec![root.to_path_buf()];
    while let Some(directory) = stack.pop() {
        let Ok(read) = std::fs::read_dir(&directory) else {
            continue;
        };
        for entry in read.flatten() {
            let path = entry.path();
            let Ok(metadata) = entry.metadata() else {
                continue;
            };
            if metadata.is_dir() {
                stack.push(path);
            } else {
                let relative = path.strip_prefix(root).unwrap_or(&path);
                entries.push((relative.to_string_lossy().into_owned(), metadata.len()));
            }
        }
    }
    entries.sort_by_key(|entry| std::cmp::Reverse(entry.1));
    entries
}

#[test]
fn shopware_trunk_indexing_profile() {
    let root = crate::real_world_root_or_skip!();
    let temporary = tempfile::tempdir().unwrap();
    let cache_root = match std::env::var_os("SHOPWARE_LSP_PROFILE_CACHE_DIR") {
        Some(directory) if !directory.is_empty() => {
            std::fs::create_dir_all(&directory).unwrap();
            std::path::PathBuf::from(directory)
        }
        _ => temporary.path().to_path_buf(),
    };
    let ctx = Context::background();
    let before_rss = status_kib("VmRSS") * 1024;

    let constructed_at = Instant::now();
    let server = Server::new(None, &root, "index-profile");
    let workspace = Workspace::new_in(&ctx, &root, &server, &cache_root).unwrap();
    let construction = constructed_at.elapsed();
    if let Ok(configured) = std::env::var("SHOPWARE_LSP_PROFILE_WORKERS")
        && !configured.is_empty()
    {
        let workers: usize = configured.parse().expect("SHOPWARE_LSP_PROFILE_WORKERS");
        assert!(workers > 0);
        workspace.file_scanner().set_worker_count(workers);
    }
    let php = workspace.services().php.clone();

    let cold_started = Instant::now();
    workspace
        .file_scanner()
        .index_all(&ctx.token().indexer_token())
        .unwrap();
    let cold = cold_started.elapsed();
    let cold_rss = status_kib("VmRSS") * 1024;
    let cold_hwm = status_kib("VmHWM") * 1024;
    if std::env::var_os("SHOPWARE_LSP_PROFILE_SEMANTIC_STORAGE").is_some() {
        eprintln!(
            "semantic storage: {:?}",
            php.semantic_snapshot().workspace_storage_stats()
        );
    }

    let warm_started = Instant::now();
    workspace
        .file_scanner()
        .index_all(&ctx.token().indexer_token())
        .unwrap();
    let warm = warm_started.elapsed();

    if std::env::var_os("SHOPWARE_LSP_PROFILE_REVERSE_REFERENCES").is_some() {
        let before = status_kib("VmRSS") * 1024;
        let started = Instant::now();
        // Any lookup initializes the complete immutable reverse index. An
        // empty ID keeps this measurement independent of checkout contents.
        let _ = php.semantic_snapshot().references_to("");
        let elapsed = started.elapsed();
        let after = status_kib("VmRSS") * 1024;
        eprintln!(
            "reverse-reference profile: first_lookup={elapsed:.0?} rss_growth={}",
            format_bytes(after.saturating_sub(before)),
        );
    }

    let entries = directory_entries(&cache_root);
    let cache_bytes: u64 = entries.iter().map(|(_, size)| size).sum();
    eprintln!(
        "index profile: construction={construction:.0?} cold={cold:.0?} warm={warm:.0?} classes={} num_cpu={} \
         rss_before={} rss_cold={} rss_peak={} rss_retained={} cache={} cache_files={}",
        php.class_symbols().len(),
        std::thread::available_parallelism().map_or(0, |count| count.get()),
        format_bytes(before_rss),
        format_bytes(cold_rss),
        format_bytes(cold_hwm),
        format_bytes(status_kib("VmRSS") * 1024),
        format_bytes(cache_bytes),
        entries.len(),
    );
    eprintln!(
        "cache files: {}",
        entries
            .iter()
            .map(|(path, size)| format!("{path}={}", format_bytes(*size)))
            .collect::<Vec<_>>()
            .join(", ")
    );

    // Interner growth under churn: every forced full re-index republishes
    // every graph. Atoms are only freed with the process, so the table must
    // stay at the distinct-identifier set instead of growing per republish.
    if let Ok(rounds) = std::env::var("SHOPWARE_LSP_PROFILE_ATOM_CHURN")
        && !rounds.is_empty()
    {
        let rounds: usize = rounds.parse().expect("SHOPWARE_LSP_PROFILE_ATOM_CHURN");
        let atoms = || {
            let stats = php.semantic_snapshot().workspace_storage_stats();
            (stats.atoms, stats.estimated_atom_table_bytes)
        };
        let before = atoms();
        let collect = std::env::var_os("SHOPWARE_LSP_PROFILE_ATOM_COLLECT").is_some();
        let classes = php.class_symbols().len();
        for round in 1..=rounds {
            workspace.file_scanner().clear_hashes().unwrap();
            workspace
                .file_scanner()
                .index_all(&ctx.token().indexer_token())
                .unwrap();
            if collect {
                let started = Instant::now();
                assert!(php.collect_semantic_atoms(), "collection should start");
                let generations = swlsp_php_semantic::atom_generation_stats();
                eprintln!(
                    "atom collection: round={round} elapsed={:.0?} live_generations={} collections={}",
                    started.elapsed(),
                    generations.live,
                    generations.collections,
                );
                assert_eq!(php.class_symbols().len(), classes);
            }
            let after = atoms();
            eprintln!(
                "atom churn: round={round} atoms={} ({:+}) table={} rss={} rss_trimmed={}",
                after.0,
                after.0 as i64 - before.0 as i64,
                format_bytes(after.1 as u64),
                format_bytes(status_kib("VmRSS") * 1024),
                format_bytes(trimmed_rss()),
            );
            if collect {
                // Live atoms after a collection are this round's baseline.
                assert_eq!(swlsp_php_semantic::atom_generation_stats().live, 1);
            }
        }
        if !collect {
            assert_eq!(
                atoms(),
                before,
                "re-indexing unchanged files grew the interner"
            );
        }
    }

    if let Ok(document_path) = std::env::var("SHOPWARE_LSP_PROFILE_SEMANTIC_DOCUMENT")
        && !document_path.is_empty()
    {
        let started = Instant::now();
        let document = php.semantic_document(&document_path);
        let elapsed = started.elapsed();
        let document =
            document.unwrap_or_else(|| panic!("profile semantic document {document_path}"));
        eprintln!(
            "semantic document profile: path={document_path} elapsed={elapsed:?} symbols={} references={}",
            document.symbols.len(),
            document.references.len(),
        );
    }
    workspace.close_workspace().unwrap();
}
