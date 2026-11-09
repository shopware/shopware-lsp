//! Runs the production PHP
//! diagnostics provider over real source files and prints the recurring
//! findings. It is an investigation harness, not a zero-diagnostics
//! assertion.
//!
//! Environment controls: `SHOPWARE_LSP_DIAGNOSTICS_ROOT`,
//! `_FILE`, `_CODES`, `_TRACE_VARIABLE`, `_TRACE_CALL`, `_TRACE_CLASS`,
//! `_MAX_FILES`, `_MAX_GROUPS`, `_CACHE_DIR`.
//!
//! Trace mode keeps the class, variable (symbols and references with their
//! inferred types) and callable listings (document, indexed and function
//! candidates).

use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::time::Instant;

use swlsp_lsp::{Context, TextDocument};
use swlsp_lsp_phpsemantic::Provider;
use swlsp_syntax::SyntaxNodeExt;

use super::admin_markup_audit::walk_files;
use super::open_real_world_workspace_with_server;

struct Group {
    code: String,
    message: String,
    count: usize,
    samples: Vec<String>,
}

fn env(name: &str) -> String {
    std::env::var(name).unwrap_or_default()
}

#[test]
fn shopware_trunk_php_diagnostics_audit() {
    let root = crate::real_world_root_or_skip!();
    let temporary = tempfile::tempdir().unwrap();
    let cache_root = match env("SHOPWARE_LSP_DIAGNOSTICS_CACHE_DIR") {
        configured if !configured.is_empty() => PathBuf::from(configured),
        _ => temporary.path().to_path_buf(),
    };
    std::fs::create_dir_all(&cache_root).unwrap();

    let audit_root = match env("SHOPWARE_LSP_DIAGNOSTICS_ROOT") {
        configured if configured.is_empty() => Path::new(&root).join("src"),
        configured if Path::new(&configured).is_absolute() => PathBuf::from(configured),
        configured => Path::new(&root).join(configured),
    };
    let max_files = match env("SHOPWARE_LSP_DIAGNOSTICS_MAX_FILES") {
        configured if configured.is_empty() => 0,
        configured => {
            let value: usize = configured
                .parse()
                .expect("SHOPWARE_LSP_DIAGNOSTICS_MAX_FILES");
            assert!(value > 0);
            value
        }
    };
    let included_codes: HashSet<String> = env("SHOPWARE_LSP_DIAGNOSTICS_CODES")
        .split(',')
        .map(str::trim)
        .filter(|code| !code.is_empty())
        .map(str::to_owned)
        .collect();
    if !env("SHOPWARE_LSP_DIAGNOSTICS_CODES").is_empty() {
        assert!(!included_codes.is_empty());
    }

    let ctx = Context::background();
    let (workspace, php, _server) = open_real_world_workspace_with_server(&root, &cache_root);
    workspace
        .file_scanner()
        .index_all(&ctx.token().indexer_token())
        .unwrap();

    let mut paths: Vec<PathBuf> = walk_files(&audit_root, "php");
    paths.sort();
    let configured_file = env("SHOPWARE_LSP_DIAGNOSTICS_FILE");
    if !configured_file.is_empty() {
        let configured = swlsp_uriutil::filepath::clean(&configured_file);
        paths.retain(|path| {
            let relative = path.strip_prefix(&root).unwrap_or(path);
            swlsp_uriutil::filepath::clean(&relative.to_string_lossy()) == configured
                || path
                    .file_name()
                    .is_some_and(|name| name.to_string_lossy() == configured)
        });
        assert!(
            !paths.is_empty(),
            "SHOPWARE_LSP_DIAGNOSTICS_FILE did not match a PHP file"
        );
    }
    if max_files > 0 && paths.len() > max_files {
        paths.truncate(max_files);
    }

    let provider = Provider::new(Some(php.clone()));
    let mut groups: HashMap<(String, String), Group> = HashMap::new();
    let mut code_counts: HashMap<String, usize> = HashMap::new();
    let mut files_with_diagnostics = 0;
    let mut diagnostic_count = 0;
    let started = Instant::now();
    let trace_class = env("SHOPWARE_LSP_DIAGNOSTICS_TRACE_CLASS");
    if !trace_class.is_empty() {
        eprintln!(
            "class {trace_class} candidates={:?}",
            php.semantic_snapshot().classes(&trace_class)
        );
    }
    let variable = env("SHOPWARE_LSP_DIAGNOSTICS_TRACE_VARIABLE");
    let trace_call = env("SHOPWARE_LSP_DIAGNOSTICS_TRACE_CALL");
    for path in &paths {
        let path_text = path.to_string_lossy().into_owned();
        let source = String::from_utf8_lossy(&std::fs::read(path).unwrap()).into_owned();
        let document = TextDocument::new(swlsp_uriutil::file_uri(&path_text), &source, 1);
        let diagnostics = provider.analyze(&ctx, &document).unwrap();
        if !variable.is_empty() || !trace_call.is_empty() {
            trace_document(&php, &path_text, &document, &variable, &trace_call);
        }
        let relative = path
            .strip_prefix(&root)
            .map(|relative| {
                relative
                    .to_string_lossy()
                    .trim_start_matches('/')
                    .to_owned()
            })
            .unwrap_or_else(|_| path_text.clone());
        let mut file_has_included = false;
        for diagnostic in diagnostics {
            let code = diagnostic.id.as_str().to_owned();
            if !included_codes.is_empty() && !included_codes.contains(&code) {
                continue;
            }
            file_has_included = true;
            diagnostic_count += 1;
            *code_counts.entry(code.clone()).or_default() += 1;
            let (line, character) = document
                .line_index()
                .position_utf16(u32::from(diagnostic.range.start()));
            if paths.len() == 1 {
                eprintln!(
                    "{relative}:{}:{}  {}  {}",
                    line + 1,
                    character + 1,
                    diagnostic.id,
                    diagnostic.message
                );
            }
            let group = groups
                .entry((code.clone(), diagnostic.message.clone()))
                .or_insert_with(|| Group {
                    code,
                    message: diagnostic.message.clone(),
                    count: 0,
                    samples: Vec::new(),
                });
            group.count += 1;
            if group.samples.len() < 3 {
                group.samples.push(format!("{relative}:{}", line + 1));
            }
        }
        if file_has_included {
            files_with_diagnostics += 1;
        }
    }

    let mut ranked: Vec<Group> = groups.into_values().collect();
    ranked.sort_by(|left, right| {
        right
            .count
            .cmp(&left.count)
            .then_with(|| left.code.cmp(&right.code))
            .then_with(|| left.message.cmp(&right.message))
    });
    eprintln!(
        "PHP diagnostics audit: root={} files={} affected={files_with_diagnostics} diagnostics={diagnostic_count} groups={} elapsed={:.0?}",
        audit_root.display(),
        paths.len(),
        ranked.len(),
        started.elapsed()
    );
    let mut ranked_codes: Vec<(String, usize)> = code_counts.into_iter().collect();
    ranked_codes.sort_by(|left, right| right.1.cmp(&left.1).then_with(|| left.0.cmp(&right.0)));
    eprintln!(
        "diagnostics by code: {}",
        ranked_codes
            .iter()
            .map(|(code, count)| format!("{code}:{count}"))
            .collect::<Vec<_>>()
            .join(", ")
    );
    let max_groups = match env("SHOPWARE_LSP_DIAGNOSTICS_MAX_GROUPS") {
        configured if configured.is_empty() => 50,
        configured => {
            let value: usize = configured
                .parse()
                .expect("SHOPWARE_LSP_DIAGNOSTICS_MAX_GROUPS");
            assert!(value > 0);
            value
        }
    };
    for group in ranked.iter().take(max_groups) {
        eprintln!(
            "{:5}  {:<20}  {}  [{}]",
            group.count,
            group.code,
            group.message,
            group.samples.join(", ")
        );
    }
    workspace.close_workspace().unwrap();
}

fn trace_document(
    php: &swlsp_php::PhpIndex,
    path: &str,
    document: &TextDocument,
    variable: &str,
    trace_call: &str,
) {
    let Some(tree) = document.tree() else {
        return;
    };
    let root = document.root().expect("PHP syntax tree");
    let semantic = php.analyze_tree(path, i64::from(document.version), tree);
    for symbol in &semantic.symbols {
        if &*symbol.name == variable {
            eprintln!(
                "symbol {:?} kind={:?} scope-owner={:?} range={:?}",
                symbol.id, symbol.kind, symbol.container, symbol.range
            );
        }
    }
    for reference in &semantic.references {
        if &*reference.name == variable {
            let fact = root
                .node_at(reference.range.start())
                .map(|node| semantic.type_of(&node).ty.to_string())
                .unwrap_or_default();
            eprintln!(
                "reference {} type={fact} scope={:?} resolved={:?} candidates={:?} range={:?}",
                reference.name,
                reference.scope,
                reference.resolved,
                reference.candidate_ids(),
                reference.range
            );
        }
    }
    if trace_call.is_empty() {
        return;
    }
    for symbol in &semantic.symbols {
        if symbol.name.eq_ignore_ascii_case(trace_call) {
            eprintln!(
                "document callable {} parameters={:?} return={}",
                symbol.fully_qualified, symbol.parameters, symbol.return_type
            );
        }
    }
    let snapshot = php.semantic_snapshot();
    for symbol in snapshot.symbols_in(path) {
        if symbol.name.eq_ignore_ascii_case(trace_call) {
            eprintln!(
                "indexed callable {} parameters={:?} return={}",
                symbol.fully_qualified, symbol.parameters, symbol.return_type
            );
        }
    }
    for candidate in snapshot.functions(trace_call) {
        eprintln!(
            "function {} parameters={:?} return={}",
            candidate.fully_qualified, candidate.parameters, candidate.return_type
        );
    }
}
