//! Snapshot and diagnostic assertion helpers of the real-world suite.

#[allow(unused_imports)]
use super::indexing_main::record_failure as soft;

/// Records a soft failure and
/// continues with the zero value.
#[allow(dead_code)]
fn soft_default<T: Default>(message: String) -> T {
    soft(message);
    T::default()
}

macro_rules! soft_assert {
    ($cond:expr, $($arg:tt)+) => {
        if !$cond {
            soft(format!($($arg)+));
        }
    };
}

use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;

use serde_json::Value;
use swlsp_lsp::*;
use swlsp_lsp_protocol as protocol;
use swlsp_php::PhpIndex;
use swlsp_syntax::LineIndex;

use super::helpers_request::*;
use super::helpers_workspace::*;
use crate::Workspace;

fn ctx() -> Context {
    Context::background()
}

fn read(path: &str) -> String {
    std::fs::read_to_string(path).unwrap_or_else(|error| panic!("read {path}: {error}"))
}

fn join(root: &str, parts: &[&str]) -> String {
    let mut path = root.to_owned();
    for part in parts {
        path.push('/');
        path.push_str(part);
    }
    path
}

fn file_uri_with_fragment(path: &str, fragment: &str) -> String {
    swlsp_uriutil::file_uri_with_fragment(path, fragment)
}

/// The first diagnostic with `code`; checks that it covers `value` and
/// suggests `suggestion`.
pub fn require_real_world_suggestion_diagnostic(
    document: &TextDocument,
    diagnostics: &[Problem],
    code: &str,
    value: &str,
    suggestion: &str,
) -> Problem {
    for diagnostic in diagnostics {
        if diagnostic.id.to_string() != code {
            continue;
        }
        let payload = diagnostic.payload.as_ref().expect("problem payload");
        let suggestions = payload
            .get("suggestions")
            .and_then(Value::as_array)
            .unwrap_or_else(|| {
                panic!("diagnostic {code:?} has unsupported suggestions {payload:?}")
            });
        soft_assert!(
            suggestions
                .iter()
                .any(|item| item.as_str() == Some(suggestion)),
            "{suggestion:?} not in {suggestions:?}"
        );
        let start: usize = diagnostic.range.start().into();
        let end: usize = diagnostic.range.end().into();
        assert_eq!(value, &document.source()[start..end]);
        return diagnostic.clone();
    }
    soft_default(format!("diagnostic {code:?} not found in {diagnostics:#?}"))
}

pub fn real_world_symbol_snapshot(
    workspace: &Workspace,
) -> BTreeMap<String, Vec<protocol::SymbolInformation>> {
    let s = workspace.services();
    let provider = swlsp_ide_symfony::symbol::SymfonyWorkspaceSymbolProvider::new(
        None,
        Some(s.routes.clone()),
        Some(s.console.clone()),
        Some(s.twig.clone()),
        Some(s.twig_components.clone()),
        Some(s.translations.clone()),
        Some(s.php.clone()),
    );
    let queries = [
        (
            "frontend.home.page",
            "Symfony route · GET / · NavigationController:home",
        ),
        (
            "Shopware\\Storefront\\Controller\\NavigationController::home",
            "Symfony controller · Shopware\\Storefront\\Controller\\NavigationController",
        ),
        ("system:config:get", "Symfony command"),
        ("@Storefront/storefront/base.html.twig", "Twig template"),
        ("base_body", "Twig block"),
        ("shopware.installer.header_title", "Translation"),
    ];
    let mut result = BTreeMap::new();
    for (query, container) in queries {
        let symbols = provider
            .workspace_symbols(&ctx(), query)
            .expect("workspace symbols");
        require_workspace_symbol(&symbols, query, container);
        result.insert(query.to_owned(), symbols);
    }
    result
}

pub fn real_world_route_endpoint_snapshot(
    root: &str,
    workspace: &Workspace,
    php: &PhpIndex,
) -> Vec<protocol::CodeLens> {
    let controller = join(
        root,
        &[
            "src",
            "Storefront",
            "Controller",
            "NavigationController.php",
        ],
    );
    let lenses = real_world_code_lenses(
        &swlsp_ide_symfony::codelens::RouteEndpointCodeLensProvider::new(
            Some(workspace.services().services.clone()),
            Some(php.clone()),
        ),
        &controller,
    );
    require_related_lens_target(
        &lenses,
        "GET / · frontend.home.page",
        &file_uri_with_fragment(&controller, "56"),
    );
    lenses
}

pub fn real_world_console_command_lens_snapshot(root: &str) -> Vec<protocol::CodeLens> {
    let command = join(
        root,
        &[
            "src",
            "Core",
            "System",
            "SystemConfig",
            "Command",
            "ConfigGet.php",
        ],
    );
    let lenses = real_world_code_lenses(
        &swlsp_ide_symfony::codelens::ConsoleCommandCodeLensProvider::new(root),
        &command,
    );
    require_code_lens_command(
        &lenses,
        "Run system:config:get",
        "shopware.symfony.runConsoleCommand",
        &[
            Value::from("system:config:get"),
            Value::from(file_uri(&command)),
        ],
    );
    lenses
}

#[derive(Debug, Clone, PartialEq)]
pub struct RelatedNavigationSnapshot {
    pub controller: Vec<protocol::CodeLens>,
    pub template: Vec<protocol::CodeLens>,
}

pub fn real_world_related_navigation_snapshot(
    root: &str,
    workspace: &Workspace,
    php: &PhpIndex,
) -> RelatedNavigationSnapshot {
    let controller = join(
        root,
        &[
            "src",
            "Storefront",
            "Controller",
            "NavigationController.php",
        ],
    );
    let template = join(
        root,
        &[
            "src",
            "Storefront",
            "Resources",
            "views",
            "storefront",
            "page",
            "content",
            "index.html.twig",
        ],
    );
    let s = workspace.services();
    let provider = swlsp_ide_symfony::codelens::RelatedNavigationCodeLensProvider::new(
        Some(s.twig.clone()),
        Some(php.clone()),
        Some(s.routes.clone()),
        Some(s.services.clone()),
    );
    let result = RelatedNavigationSnapshot {
        controller: real_world_code_lenses(&provider, &controller),
        template: real_world_code_lenses(&provider, &template),
    };
    require_related_lens_target(
        &result.controller,
        "related template",
        &file_uri_with_fragment(&template, "1"),
    );
    require_related_lens_target(
        &result.controller,
        "route definition",
        &file_uri_with_fragment(&controller, "49"),
    );
    require_related_lens_target(
        &result.template,
        "rendering PHP location",
        &file_uri_with_fragment(&controller, "62"),
    );
    require_related_lens_target(
        &result.template,
        "related route",
        &file_uri_with_fragment(&controller, "49"),
    );
    result
}

pub fn real_world_code_lenses(
    provider: &dyn CodeLensProvider,
    path: &str,
) -> Vec<protocol::CodeLens> {
    let document = new_document(file_uri(path), &read(path), 1);
    let mut params = protocol::CodeLensParams::default();
    params.text_document.uri = document.uri.clone();
    let lenses = provider
        .get_code_lenses(&ctx(), &CodeLensRequest { params, document })
        .expect("code lenses");
    assert!(!lenses.is_empty(), "no code lenses for {path}");
    lenses
}

pub fn real_world_document_symbols(
    provider: &dyn DocumentSymbolProvider,
    path: &str,
) -> Vec<protocol::DocumentSymbol> {
    let document = new_document(file_uri(path), &read(path), 1);
    let mut params = protocol::DocumentSymbolParams::default();
    params.text_document.uri = document.uri.clone();
    provider
        .get_document_symbols(&ctx(), &DocumentSymbolRequest { params, document })
        .expect("document symbols")
}

fn needle_document(path: &str, needle: &str) -> (Arc<TextDocument>, u32) {
    let document = new_document(file_uri(path), &read(path), 1);
    let offset = document
        .source()
        .find(needle)
        .unwrap_or_else(|| soft_default(format!("{needle:?} not in {path}")))
        as u32
        + 1;
    (document, offset)
}

fn implementation_params(document: &TextDocument, offset: u32) -> protocol::ImplementationParams {
    let mut params = protocol::ImplementationParams::default();
    params.text_document.uri = document.uri.clone();
    params.position = position(document, offset);
    params
}

pub fn real_world_document_highlights(
    provider: &dyn DocumentHighlightProvider,
    path: &str,
    needle: &str,
) -> Vec<protocol::DocumentHighlight> {
    let (document, offset) = needle_document(path, needle);
    provider
        .get_document_highlights(
            &ctx(),
            &DocumentHighlightRequest {
                params: implementation_params(&document, offset),
                syntax: syntax(&document, node_at(&document, offset), None),
            },
        )
        .expect("document highlights")
}

pub fn range_offsets(document: &TextDocument, value: &protocol::Range) -> (usize, usize) {
    let index = document.line_index();
    (
        index.offset_utf16(value.start.line, value.start.character) as usize,
        index.offset_utf16(value.end.line, value.end.character) as usize,
    )
}

pub fn real_world_linked_editing_ranges(
    provider: &dyn LinkedEditingRangeProvider,
    path: &str,
    needle: &str,
) -> Option<protocol::LinkedEditingRanges> {
    let (document, offset) = needle_document(path, needle);
    let ranges = provider
        .get_linked_editing_ranges(
            &ctx(),
            &LinkedEditingRangeRequest {
                params: implementation_params(&document, offset),
                syntax: syntax(&document, node_at(&document, offset), None),
            },
        )
        .expect("linked editing ranges");
    if let Some(ranges) = &ranges {
        for value in &ranges.ranges {
            let (start, end) = range_offsets(&document, value);
            assert_eq!(needle, &document.source()[start..end]);
        }
    }
    ranges
}

pub fn real_world_folding_ranges(
    provider: &dyn FoldingRangeProvider,
    path: &str,
) -> Vec<protocol::FoldingRange> {
    let document = new_document(file_uri(path), &read(path), 1);
    let mut params = protocol::FoldingRangeParams::default();
    params.text_document.uri = document.uri.clone();
    provider
        .get_folding_ranges(&ctx(), &FoldingRangeRequest { params, document })
        .expect("folding ranges")
}

pub fn real_world_selection_texts(
    provider: &dyn SelectionRangeProvider,
    path: &str,
    needle: &str,
) -> Vec<String> {
    let (document, offset) = needle_document(path, needle);
    let mut params = protocol::SelectionRangeParams::default();
    params.text_document.uri = document.uri.clone();
    params.positions = vec![position(&document, offset)];
    let ranges = provider
        .get_selection_ranges(
            &ctx(),
            &SelectionRangeRequest {
                params,
                document: document.clone(),
            },
        )
        .expect("selection ranges");
    assert_eq!(1, ranges.len());
    let mut result = Vec::new();
    let mut current = Some(&ranges[0]);
    while let Some(value) = current {
        let (start, end) = range_offsets(&document, &value.range);
        result.push(document.source()[start..end].to_owned());
        current = value.parent.as_deref();
    }
    result
}

pub fn real_world_document_colors(
    provider: &dyn DocumentColorProvider,
    path: &str,
) -> (Vec<protocol::ColorInformation>, Arc<TextDocument>) {
    let document = new_document(file_uri(path), &read(path), 1);
    let mut params = protocol::DocumentColorParams::default();
    params.text_document.uri = document.uri.clone();
    let colors = provider
        .get_document_colors(
            &ctx(),
            &DocumentColorRequest {
                params,
                document: document.clone(),
            },
        )
        .expect("document colors");
    (colors, document)
}

pub fn real_world_range_text(document: &TextDocument, value: &protocol::Range) -> String {
    let (start, end) = range_offsets(document, value);
    document.source()[start..end].to_owned()
}

pub fn admin_workspace_edit_count(edit: Option<&protocol::WorkspaceEdit>) -> usize {
    edit.map(|edit| edit.changes.values().map(Vec::len).sum())
        .unwrap_or_default()
}

pub fn apply_real_world_text_edits(source: &str, edits: &[protocol::TextEdit]) -> String {
    let line_index = LineIndex::new(source);
    let mut result = source.to_owned();
    for edit in edits {
        let start =
            line_index.offset_utf16(edit.range.start.line, edit.range.start.character) as usize;
        let end = line_index.offset_utf16(edit.range.end.line, edit.range.end.character) as usize;
        assert!(start <= end);
        assert!(end <= result.len());
        result = format!("{}{}{}", &result[..start], edit.new_text, &result[end..]);
    }
    result
}

pub fn require_real_world_selection_containing(values: &[String], needle: &str) {
    soft_assert!(
        values.iter().any(|value| value.contains(needle)),
        "missing selection ancestor: expected {needle:?} in {values:#?}"
    );
}

pub fn require_real_world_folding_range(
    ranges: &[protocol::FoldingRange],
    start_line: u32,
    end_line: u32,
    kind: &str,
) {
    soft_assert!(
        ranges.iter().any(|value| value.start_line == start_line
            && value.end_line == end_line
            && value.kind == kind),
        "missing real-world folding range: expected {start_line}..{end_line} kind {kind:?} in {ranges:#?}"
    );
}

pub fn real_world_call_hierarchy_items(
    provider: &dyn CallHierarchyProvider,
    path: &str,
    needle: &str,
) -> Vec<protocol::CallHierarchyItem> {
    let (document, offset) = needle_document(path, needle);
    provider
        .prepare_call_hierarchy(
            &ctx(),
            &CallHierarchyPrepareRequest {
                params: implementation_params(&document, offset),
                syntax: syntax(&document, node_at(&document, offset), None),
            },
        )
        .expect("call hierarchy")
}

pub fn require_document_symbol_named(
    symbols: &[protocol::DocumentSymbol],
    name: &str,
) -> protocol::DocumentSymbol {
    symbols
        .iter()
        .find(|symbol| symbol.name == name)
        .cloned()
        .unwrap_or_else(|| panic!("document symbol {name:?} not found in {symbols:#?}"))
}

pub fn require_document_symbol_named_recursive(
    symbols: &[protocol::DocumentSymbol],
    name: &str,
) -> protocol::DocumentSymbol {
    find_document_symbol_named(symbols, name)
        .unwrap_or_else(|| panic!("document symbol {name:?} not found recursively in {symbols:#?}"))
}

pub fn find_document_symbol_named(
    symbols: &[protocol::DocumentSymbol],
    name: &str,
) -> Option<protocol::DocumentSymbol> {
    for symbol in symbols {
        if symbol.name == name {
            return Some(symbol.clone());
        }
        if let Some(child) = find_document_symbol_named(&symbol.children, name) {
            return Some(child);
        }
    }
    None
}

#[derive(Debug, Clone)]
pub struct ControllerUsageSnapshot {
    pub usages: Vec<swlsp_symfony::ControllerUsage>,
    pub controller: Vec<protocol::CodeLens>,
    pub template: Vec<protocol::CodeLens>,
}

pub fn real_world_controller_usage_snapshot(
    root: &str,
    workspace: &Workspace,
    php: &PhpIndex,
) -> ControllerUsageSnapshot {
    let controller = join(
        root,
        &[
            "src",
            "Core",
            "Profiling",
            "Controller",
            "ProfilerController.php",
        ],
    );
    let template = join(
        root,
        &[
            "src",
            "Core",
            "Profiling",
            "Resources",
            "views",
            "Collector",
            "db.html.twig",
        ],
    );
    let reference = swlsp_symfony::parse_controller_reference(
        "Shopware\\Core\\Profiling\\Controller\\ProfilerController::explainAction",
    )
    .expect("controller reference");
    let s = workspace.services();
    let usages = s
        .route_usage
        .get_controller_usages(&reference)
        .expect("controller usages");
    assert!(!usages.is_empty());
    require_controller_usage_path(&usages, &template);
    let provider = swlsp_ide_symfony::codelens::ControllerRelatedCodeLensProvider::new(
        Some(s.route_usage.clone()),
        Some(s.services.clone()),
        Some(php.clone()),
    );
    let result = ControllerUsageSnapshot {
        usages,
        controller: real_world_code_lenses(&provider, &controller),
        template: real_world_code_lenses(&provider, &template),
    };
    require_related_lens_target(
        &result.controller,
        "Twig controller usage",
        &file_uri_with_fragment(&template, "80"),
    );
    require_related_lens_target(
        &result.template,
        "controller method",
        &file_uri_with_fragment(&controller, "29"),
    );
    result
}

pub fn require_controller_usage_path(usages: &[swlsp_symfony::ControllerUsage], path: &str) {
    let path = swlsp_uriutil::filepath::clean(path);
    soft_assert!(
        usages
            .iter()
            .any(|usage| swlsp_uriutil::filepath::clean(&usage.file) == path),
        "controller usage {path:?} not found in {usages:#?}"
    );
}

#[derive(Debug, Clone, PartialEq)]
pub struct ServiceNavigationSnapshot {
    pub decorators: Vec<protocol::CodeLens>,
    pub target: Vec<protocol::CodeLens>,
}

pub fn real_world_service_navigation_snapshot(
    root: &str,
    workspace: &Workspace,
    php: &PhpIndex,
) -> ServiceNavigationSnapshot {
    let decorator = join(
        root,
        &[
            "src",
            "Elasticsearch",
            "DependencyInjection",
            "services.php",
        ],
    );
    let target = join(
        root,
        &[
            "src",
            "Core",
            "Framework",
            "DependencyInjection",
            "data-abstraction-layer.php",
        ],
    );
    let provider = swlsp_ide_symfony::codelens::ServiceRelatedCodeLensProvider::new(
        Some(workspace.services().services.clone()),
        Some(php.clone()),
    );
    let result = ServiceNavigationSnapshot {
        decorators: real_world_code_lenses(&provider, &decorator),
        target: real_world_code_lenses(&provider, &target),
    };
    require_related_lens_file_targets(
        &result.decorators,
        "decorated service",
        &file_uri(&target),
        1,
    );
    require_related_lens_file_targets(
        &result.target,
        "decorating services",
        &file_uri(&decorator),
        2,
    );
    result
}

fn lens_targets<'a>(lens: &'a protocol::CodeLens, title: &str) -> Vec<&'a str> {
    let Some(command) = &lens.command else {
        return Vec::new();
    };
    if !command.title.contains(title) || command.arguments.len() != 1 {
        return Vec::new();
    }
    command.arguments[0]
        .as_array()
        .map(|values| values.iter().filter_map(Value::as_str).collect())
        .unwrap_or_default()
}

pub fn require_related_lens_file_targets(
    lenses: &[protocol::CodeLens],
    title: &str,
    file_uri: &str,
    minimum: usize,
) {
    let prefix = format!("{file_uri}#");
    let targets: BTreeSet<&str> = lenses
        .iter()
        .flat_map(|lens| lens_targets(lens, title))
        .filter(|current| *current == file_uri || current.starts_with(&prefix))
        .collect();
    soft_assert!(
        targets.len() >= minimum,
        "related code lens {title:?} targets in {file_uri:?}: {targets:?} (want >= {minimum})"
    );
}

pub fn require_related_lens_target(lenses: &[protocol::CodeLens], title: &str, target: &str) {
    soft_assert!(
        lenses
            .iter()
            .any(|lens| lens_targets(lens, title).contains(&target)),
        "related code lens {title:?} target {target:?} not found in {lenses:#?}"
    );
}
