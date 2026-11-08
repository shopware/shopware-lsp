//! Differential oracle against the Go indexer (run manually):
//!
//! ```sh
//! ADMIN_ORACLE_FILES=files.txt ADMIN_ORACLE_CACHE=cache ADMIN_ORACLE_OUT=rust.tsv \
//!   cargo test -p swlsp-admin --release oracle_dump -- --ignored
//! ```
//!
//! Writes `namespace\tpath\tjson` lines comparable with the Go dump after
//! key normalization.

use std::io::Write;

use swlsp_indexer::Indexer;

use crate::*;

fn dump<T: Serialize + serde::de::DeserializeOwned + Send + Sync + 'static>(
    out: &mut impl Write,
    repository: &DataIndexer<T>,
) {
    let mut paths = repository.get_all_file_paths().expect("paths");
    paths.sort();
    for path in paths {
        let mut lines: Vec<String> = repository
            .get_values_by_path_view(&path)
            .expect("values")
            .iter()
            .map(|value| serde_json::to_string(value).expect("json"))
            .collect();
        lines.sort();
        for line in lines {
            writeln!(out, "{}\t{}\t{}", repository.namespace(), path, line).expect("write");
        }
    }
}

#[test]
#[ignore = "differential oracle; needs ADMIN_ORACLE_* environment"]
fn oracle_dump() {
    let files = std::env::var("ADMIN_ORACLE_FILES").expect("ADMIN_ORACLE_FILES");
    let cache = std::env::var("ADMIN_ORACLE_CACHE").expect("ADMIN_ORACLE_CACHE");
    let output = std::env::var("ADMIN_ORACLE_OUT").expect("ADMIN_ORACLE_OUT");
    let _ = std::fs::remove_dir_all(&cache);
    std::fs::create_dir_all(&cache).expect("cache");
    let idx = AdminComponentIndexer::new(&cache, None).expect("indexer");
    let list = std::fs::read_to_string(files).expect("file list");
    let started = std::time::Instant::now();
    for path in list.lines().filter(|line| !line.is_empty()) {
        let content = std::fs::read(path).expect("source");
        if let Err(error) = idx.index(&ParsedFile::new(path, content)) {
            eprintln!("{path}: {error}");
        }
    }
    eprintln!("indexed in {:?}", started.elapsed());
    let file = std::fs::File::create(output).expect("output");
    let mut out = std::io::BufWriter::new(file);
    dump(&mut out, &idx.component_index);
    dump(&mut out, &idx.definition_index);
    dump(&mut out, &idx.mixin_index);
    dump(&mut out, &idx.directive_index);
    dump(&mut out, &idx.filter_index);
    dump(&mut out, &idx.cms_index);
    dump(&mut out, &idx.module_index);
    dump(&mut out, &idx.service_index);
    dump(&mut out, &idx.store_index);
    dump(&mut out, &idx.store_factory_index);
    dump(&mut out, &idx.privilege_index);
    dump(&mut out, &idx.usage_index);
    dump(&mut out, &idx.type_index);
}

/// Request-latency probe on an oracle index (run manually):
/// `ADMIN_ORACLE_FILES=… ADMIN_BENCH_FILES=… ADMIN_ORACLE_CACHE=… cargo test
/// -p swlsp-admin --release oracle_bench -- --ignored --nocapture`.
#[test]
#[ignore = "benchmark; needs ADMIN_ORACLE_* environment"]
fn oracle_bench() {
    let files = std::env::var("ADMIN_ORACLE_FILES").expect("ADMIN_ORACLE_FILES");
    let bench = std::env::var("ADMIN_BENCH_FILES").expect("ADMIN_BENCH_FILES");
    let cache = std::env::var("ADMIN_ORACLE_CACHE").expect("ADMIN_ORACLE_CACHE");
    let _ = std::fs::remove_dir_all(&cache);
    std::fs::create_dir_all(&cache).expect("cache");
    let idx = AdminComponentIndexer::new(&cache, None).expect("indexer");
    for path in std::fs::read_to_string(files).expect("list").lines() {
        let content = std::fs::read(path).expect("source");
        let _ = idx.index(&ParsedFile::new(path, content));
    }
    let mut timing: BTreeMap<&str, std::time::Duration> = BTreeMap::new();
    for path in std::fs::read_to_string(bench).expect("bench list").lines() {
        let parsed = ParsedFile::new(path, std::fs::read(path).expect("source"));
        let root = parsed.syntax_tree().map(|tree| tree.root());
        for _ in 0..5 {
            let started = std::time::Instant::now();
            let _ = idx.get_component_for_document(
                path,
                root.as_ref(),
                parsed.source(),
                Some(parsed.line_index()),
            );
            *timing.entry("for_document").or_default() += started.elapsed();
            if path.ends_with(".twig")
                && let Some(root) = root.as_ref()
            {
                let started = std::time::Instant::now();
                let component = idx.get_component_by_template_path(path).ok().flatten();
                *timing.entry("twig.lookup").or_default() += started.elapsed();
                let started = std::time::Instant::now();
                let mut template = parse_template_tree(root, parsed.source(), parsed.line_index());
                *timing.entry("twig.parse_template").or_default() += started.elapsed();
                if let Some(component) = component {
                    let started = std::time::Instant::now();
                    set_template_source_paths(&mut template, path);
                    inherit_live_template_block_contracts(&mut template, &component, path);
                    let mut result = component.clone();
                    strip_component_template_source(&mut result, path);
                    *timing.entry("twig.strip").or_default() += started.elapsed();
                    let started = std::time::Instant::now();
                    let live = VueComponent {
                        name: result.name.clone(),
                        template_path: path.to_owned(),
                        slots: template.slots,
                        blocks: template.blocks,
                        ..Default::default()
                    };
                    let _ = overlay_components(&result, &live);
                    *timing.entry("twig.overlay").or_default() += started.elapsed();
                }
            }
            let started = std::time::Instant::now();
            let _ = idx.get_components_by_definition_path(path);
            *timing.entry("by_definition_path").or_default() += started.elapsed();
            let started = std::time::Instant::now();
            let _ = idx.get_component_by_template_path(path);
            *timing.entry("by_template_path").or_default() += started.elapsed();
            let started = std::time::Instant::now();
            let _ = idx.resolve_shopware_utils("format", path);
            *timing.entry("utils").or_default() += started.elapsed();
        }
    }
    for (name, duration) in timing {
        eprintln!("bench {name} {:.3}s", duration.as_secs_f64());
    }
}
