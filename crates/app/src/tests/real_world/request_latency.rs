//! Port of Go `request_latency_integration_test.go`: the production request
//! census over JSON-RPC with p95 budgets.

use std::collections::HashSet;
use std::time::{Duration, Instant};

use serde_json::{Value, json};
use swlsp_lsp::{Server, TextDocument};

use super::RealWorldWorkspaceFixture;
use crate::tests::support::TestClient;

const REAL_WORLD_REQUEST_SAMPLES: usize = 21;

#[derive(Debug, Default, Clone, Copy)]
struct RequestLatency {
    first: Duration,
    median: Duration,
    p95: Duration,
    max: Duration,
}

#[test]
fn shopware_trunk_request_latency() {
    let Some(fixture) = RealWorldWorkspaceFixture::new() else {
        return;
    };
    measure_real_world_lsp_request_latency(&fixture.server, &fixture.root, false);
}

fn sample_count() -> usize {
    std::env::var("SHOPWARE_LSP_LATENCY_SAMPLES")
        .ok()
        .and_then(|value| value.trim().parse::<usize>().ok())
        .filter(|value| *value >= 2)
        .unwrap_or(REAL_WORLD_REQUEST_SAMPLES)
}

fn open_document(client: &TestClient, path: &str) -> TextDocument {
    let source = std::fs::read_to_string(path).unwrap_or_else(|error| panic!("{path}: {error}"));
    let document = TextDocument::new(swlsp_uriutil::file_uri(path), &source, 1);
    assert!(document.tree().is_some(), "{path} has no syntax tree");
    let language = std::path::Path::new(path)
        .extension()
        .map(|extension| extension.to_string_lossy().to_lowercase())
        .unwrap_or_default();
    client.notify(
        "textDocument/didOpen",
        json!({"textDocument": {
            "uri": document.uri, "version": document.version,
            "languageId": language, "text": document.source(),
        }}),
    );
    document
}

fn wait_for_diagnostics(client: &TestClient, uris: &[&str]) {
    let mut pending: HashSet<&str> = uris.iter().copied().collect();
    let deadline = Instant::now() + Duration::from_secs(10);
    while !pending.is_empty() {
        let remaining = deadline.saturating_duration_since(Instant::now());
        let params = client
            .published
            .recv_timeout(remaining)
            .unwrap_or_else(|_| panic!("timed out waiting for diagnostics for {pending:?}"));
        if let Some(uri) = params["uri"].as_str() {
            pending.remove(uri);
        }
    }
}

fn position(document: &TextDocument, needle: &str, delta: usize) -> Value {
    let offset = document
        .source()
        .find(needle)
        .unwrap_or_else(|| panic!("{needle} not found in {}", document.uri))
        + delta;
    let (line, character) = document.line_index().position_utf16(offset as u32);
    json!({"line": line, "character": character})
}

fn code_action_params(
    uri: &str,
    position: &Value,
    diagnostics: Option<Vec<Value>>,
    only: Option<Vec<&str>>,
) -> Value {
    let mut context = json!({"diagnostics": diagnostics.unwrap_or_default()});
    if let Some(only) = only {
        context["only"] = json!(only);
    }
    json!({
        "textDocument": {"uri": uri},
        "range": {"start": position, "end": position},
        "context": context,
    })
}

fn measure(
    client: &TestClient,
    name: &str,
    method: &str,
    params: &Value,
    p95_budget: Duration,
) -> (Value, RequestLatency) {
    let samples = sample_count();
    let mut durations = Vec::with_capacity(samples - 1);
    let mut latest = Value::Null;
    let mut first = Duration::ZERO;
    for sample in 0..samples {
        let started = Instant::now();
        let response = client.request(method, params.clone());
        let elapsed = started.elapsed();
        if let Some(error) = response.get("error") {
            panic!("{name} sample {sample}: {error}");
        }
        latest = response.get("result").cloned().unwrap_or(Value::Null);
        if sample == 0 {
            first = elapsed;
            continue;
        }
        durations.push(elapsed);
    }
    durations.sort();
    let latency = RequestLatency {
        first,
        median: durations[durations.len() / 2],
        p95: durations[(durations.len() as f64 * 0.95).ceil() as usize - 1],
        max: durations[durations.len() - 1],
    };
    eprintln!(
        "{name} first/p50/p95/max: {:?}/{:?}/{:?}/{:?}",
        latency.first, latency.median, latency.p95, latency.max
    );
    assert!(
        latency.p95 <= p95_budget,
        "{name} p95 latency {:?} exceeded budget {p95_budget:?} (median={:?} max={:?})",
        latency.p95,
        latency.median,
        latency.max,
    );
    (latest, latency)
}

fn array_len(value: &Value) -> usize {
    value.as_array().map_or(0, Vec::len)
}

pub(crate) fn measure_real_world_lsp_request_latency(
    server: &Server,
    root: &str,
    strict_fixtures: bool,
) {
    let client = TestClient::start(server);
    client.call(
        "initialize",
        json!({
            "processId": std::process::id(),
            "rootUri": swlsp_uriutil::file_uri(root),
            "capabilities": {"textDocument": {"codeAction": {
                "dataSupport": true,
                "resolveSupport": {"properties": ["edit"]},
            }}},
        }),
    );
    client.notify("initialized", json!({}));

    let php_document = open_document(
        &client,
        &format!("{root}/src/Core/Framework/Demodata/DemodataContext.php"),
    );
    let admin_template = open_document(
        &client,
        &format!(
            "{root}/src/Administration/Resources/app/administration/src/app/component/base/sw-card/sw-card.html.twig"
        ),
    );
    let admin_definition = open_document(
        &client,
        &format!(
            "{root}/src/Administration/Resources/app/administration/src/app/component/base/sw-card/index.ts"
        ),
    );
    let linked_template = open_document(
        &client,
        &format!("{root}/src/Administration/Resources/views/administration/index.html.twig"),
    );
    let diagnostic_template = open_document(
        &client,
        &format!(
            "{root}/src/Administration/Resources/app/administration/src/module/sw-settings-number-range/page/sw-settings-number-range-detail/sw-settings-number-range-detail.html.twig"
        ),
    );
    wait_for_diagnostics(
        &client,
        &[
            &php_document.uri,
            &admin_template.uri,
            &admin_definition.uri,
            &linked_template.uri,
            &diagnostic_template.uri,
        ],
    );

    let php_position = position(&php_document, "DemodataContext", 3);
    let admin_position = position(&admin_template, "getSlots", 3);
    let at =
        |uri: &str, position: &Value| json!({"textDocument": {"uri": uri}, "position": position});
    let ms = Duration::from_millis;

    let (php_hover, php_hover_latency) = measure(
        &client,
        "hover/php",
        "textDocument/hover",
        &at(&php_document.uri, &php_position),
        ms(25),
    );
    assert!(!php_hover.is_null());
    let (admin_hover, admin_hover_latency) = measure(
        &client,
        "hover/admin-twig",
        "textDocument/hover",
        &at(&admin_template.uri, &admin_position),
        ms(25),
    );
    assert!(!admin_hover.is_null());

    let (php_definitions, php_definition_latency) = measure(
        &client,
        "definition/php",
        "textDocument/definition",
        &at(&php_document.uri, &php_position),
        ms(25),
    );
    assert!(array_len(&php_definitions) > 0);
    let (admin_definitions, admin_definition_latency) = measure(
        &client,
        "definition/admin-twig",
        "textDocument/definition",
        &at(&admin_template.uri, &admin_position),
        ms(25),
    );
    assert!(array_len(&admin_definitions) > 0);

    let references = |uri: &str, position: &Value| {
        let mut params = at(uri, position);
        params["context"] = json!({"includeDeclaration": true});
        params
    };
    let (php_references, php_references_latency) = measure(
        &client,
        "references/php",
        "textDocument/references",
        &references(&php_document.uri, &php_position),
        ms(25),
    );
    assert!(array_len(&php_references) > 1);
    let (admin_references, admin_references_latency) = measure(
        &client,
        "references/admin-twig",
        "textDocument/references",
        &references(&admin_template.uri, &admin_position),
        ms(25),
    );
    assert!(array_len(&admin_references) > 1);

    let (_, php_actions_latency) = measure(
        &client,
        "codeAction/php/all",
        "textDocument/codeAction",
        &code_action_params(&php_document.uri, &php_position, None, None),
        ms(50),
    );
    let (_, php_quick_fix_latency) = measure(
        &client,
        "codeAction/php/quickfix",
        "textDocument/codeAction",
        &code_action_params(
            &php_document.uri,
            &php_position,
            None,
            Some(vec!["quickfix"]),
        ),
        ms(10),
    );
    let (admin_actions, admin_actions_latency) = measure(
        &client,
        "codeAction/admin-twig/all",
        "textDocument/codeAction",
        &code_action_params(&admin_template.uri, &admin_position, None, None),
        ms(50),
    );
    assert!(array_len(&admin_actions) > 0);

    let (diagnostic_result, diagnostics_latency) = measure(
        &client,
        "diagnostic/admin-twig",
        "textDocument/diagnostic",
        &json!({"textDocument": {"uri": diagnostic_template.uri}}),
        ms(10),
    );
    let unknown_event = diagnostic_result["items"]
        .as_array()
        .cloned()
        .unwrap_or_default()
        .into_iter()
        .find(|diagnostic| diagnostic["code"] == json!("admin.component.unknown-event"));
    let mut diagnostic_actions_latency = RequestLatency::default();
    let mut resolve_latency = RequestLatency::default();
    if let Some(unknown_event) = unknown_event {
        let start = unknown_event["range"]["start"].clone();
        let (diagnostic_actions, measured) = measure(
            &client,
            "codeAction/admin-diagnostic",
            "textDocument/codeAction",
            &code_action_params(
                &diagnostic_template.uri,
                &start,
                Some(vec![unknown_event.clone()]),
                Some(vec!["quickfix"]),
            ),
            ms(25),
        );
        diagnostic_actions_latency = measured;
        assert!(array_len(&diagnostic_actions) > 0);
        let (resolved, measured) = measure(
            &client,
            "codeAction/resolve",
            "codeAction/resolve",
            &diagnostic_actions[0],
            ms(25),
        );
        resolve_latency = measured;
        assert!(!resolved["edit"].is_null(), "{resolved}");
    } else if strict_fixtures {
        panic!("admin.component.unknown-event fixture is missing");
    } else {
        eprintln!(
            "administration diagnostic quick-fix fixture is unavailable on this Shopware revision"
        );
    }

    let (links, document_link_latency) = measure(
        &client,
        "documentLink/twig",
        "textDocument/documentLink",
        &json!({"textDocument": {"uri": linked_template.uri}}),
        ms(25),
    );
    assert!(array_len(&links) > 0);

    let (definition_symbols, definition_symbol_latency) = measure(
        &client,
        "documentSymbol/admin-ts",
        "textDocument/documentSymbol",
        &json!({"textDocument": {"uri": admin_definition.uri}}),
        ms(25),
    );
    assert!(array_len(&definition_symbols) > 0);
    let (template_symbols, template_symbol_latency) = measure(
        &client,
        "documentSymbol/admin-twig",
        "textDocument/documentSymbol",
        &json!({"textDocument": {"uri": admin_template.uri}}),
        ms(25),
    );
    assert!(array_len(&template_symbols) > 0);

    let pair = |latency: RequestLatency| format!("{:?}/{:?}", latency.median, latency.p95);
    eprintln!(
        "interactive LSP p50/p95 (JSON-RPC): hover_php={}, hover_admin={}, definition_php={}, definition_admin={}, references_php={}, references_admin={}, code_action_php={}, code_action_php_quickfix={}, code_action_admin={}, diagnostics_admin={}, code_action_diagnostic={}, code_action_resolve={}, document_link={}, document_symbol_ts={}, document_symbol_twig={}",
        pair(php_hover_latency),
        pair(admin_hover_latency),
        pair(php_definition_latency),
        pair(admin_definition_latency),
        pair(php_references_latency),
        pair(admin_references_latency),
        pair(php_actions_latency),
        pair(php_quick_fix_latency),
        pair(admin_actions_latency),
        pair(diagnostics_latency),
        pair(diagnostic_actions_latency),
        pair(resolve_latency),
        pair(document_link_latency),
        pair(definition_symbol_latency),
        pair(template_symbol_latency),
    );

    client.call("shutdown", json!(null));
    client.notify("exit", json!(null));
    client.shutdown().unwrap();
}
