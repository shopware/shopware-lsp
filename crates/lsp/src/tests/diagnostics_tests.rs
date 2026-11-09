//! Port of `server_diagnostics_test.go`.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::time::Duration;

use crossbeam_channel::{Receiver, Sender, bounded, unbounded};
use serde_json::{Value, json};
use swlsp_indexer::{BatchIndexer, FileScanner, Indexer, ParsedFile};
use swlsp_language::LanguageId;
use swlsp_lsp_protocol as protocol;
use swlsp_syntax::TextSize;

use super::support::*;
use crate::context::lock;
use crate::diagnostics::diagnostic_performance_trace_enabled;
use crate::*;

/// Blocks version 2 until its diagnostics job is cancelled.
struct LifecycleInspection {
    version_two_started: Sender<()>,
}

impl Inspection for LifecycleInspection {
    fn definition(&self) -> InspectionDefinition {
        TestInspection.definition()
    }

    fn inspect(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        if document.version == 2 {
            let _ = self.version_two_started.try_send(());
            ctx.token().wait();
            return Ok(());
        }
        TestInspection.inspect(ctx, document, reporter)
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        TestInspection.quick_fixes()
    }
}

#[derive(Default)]
struct CountingInspection {
    calls: AtomicUsize,
}

impl Inspection for CountingInspection {
    fn definition(&self) -> InspectionDefinition {
        TestInspection.definition()
    }

    fn inspect(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        TestInspection.inspect(ctx, document, reporter)
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        TestInspection.quick_fixes()
    }
}

#[test]
fn pull_diagnostics_full_report_over_json_rpc() {
    for (name, open, inspection, want) in [
        ("unopened document", false, false, 0),
        ("empty document report", true, false, 0),
        ("document with diagnostics", true, true, 1),
    ] {
        let dir = temp_dir();
        let server = Server::new(None, &root_string(&dir), "test");
        if inspection {
            server.register_inspection(Arc::new(TestInspection));
        }
        let client = start_client(&server, |_, _| {});
        let uri = file_uri(&temp_dir().path().join("test.yaml"));
        if open {
            did_open(&client, &uri, 1, "value: bad\n");
        }
        // Repeat to cover cached reports, including empty ones.
        for _ in 0..2 {
            let report = client
                .call(
                    "textDocument/diagnostic",
                    json!({"textDocument": {"uri": uri}}),
                )
                .unwrap();
            assert_eq!(report["kind"], "full", "{name}");
            let items = report["items"]
                .as_array()
                .expect("full reports serialize items as an array");
            assert_eq!(items.len(), want, "{name}");
        }
        client.shutdown().unwrap();
    }
}

#[test]
fn document_diagnostic_lifecycle_over_json_rpc() {
    let dir = temp_dir();
    let server = Server::new(None, &root_string(&dir), "test");
    let (started_sender, version_two_started) = bounded(1);
    server.register_inspection(Arc::new(LifecycleInspection {
        version_two_started: started_sender,
    }));
    let client = start_client(&server, |_, _| {});
    const URI: &str = "file:///project/test.yaml";
    did_open(&client, URI, 1, "value: bad\n");
    let opened = client.wait_published();
    assert_eq!(opened.uri, URI);
    assert_eq!(opened.version, 1);
    assert_eq!(opened.diagnostics.len(), 1);

    did_change(&client, URI, 2, "value: bad\n");
    recv(&version_two_started, "version 2 diagnostics did not start");
    did_change(&client, URI, 3, "value: bad\n");
    let changed = client.wait_published();
    assert_eq!(changed.version, 3);
    assert_eq!(changed.diagnostics.len(), 1);

    client.notify(
        "textDocument/didClose",
        json!({"textDocument": {"uri": URI}}),
    );
    let closed = client.wait_published();
    assert_eq!(closed.uri, URI);
    assert_eq!(closed.version, 0);
    assert!(closed.diagnostics.is_empty());
    assert!(server.documents.get_document(URI).is_none());
    assert!(
        client
            .published
            .recv_timeout(DIAGNOSTICS_DEBOUNCE * 2)
            .is_err(),
        "received diagnostics after close"
    );
    client.shutdown().unwrap();
}

struct DiagnosticsBatchIndexer {
    ready: AtomicBool,
    index_started: Sender<()>,
    release_index: Receiver<()>,
    finish_started: Sender<()>,
    release_finish: Receiver<()>,
}

impl Indexer for DiagnosticsBatchIndexer {
    fn id(&self) -> &str {
        "diagnostics-batch"
    }

    fn index(&self, _file: &ParsedFile) -> swlsp_indexer::Result<()> {
        let _ = self.index_started.try_send(());
        let _ = self.release_index.recv();
        Ok(())
    }

    fn removed_files(&self, _paths: &[String]) -> swlsp_indexer::Result<()> {
        Ok(())
    }

    fn clear(&self) -> swlsp_indexer::Result<()> {
        Ok(())
    }

    fn as_batch(&self) -> Option<&dyn BatchIndexer> {
        Some(self)
    }
}

impl BatchIndexer for DiagnosticsBatchIndexer {
    fn begin_indexing_batch(&self, _candidate_files: &[String]) {}

    fn end_indexing_batch(&self) -> swlsp_indexer::Result<()> {
        let _ = self.finish_started.try_send(());
        let _ = self.release_finish.recv();
        self.ready.store(true, Ordering::SeqCst);
        Ok(())
    }
}

struct IndexedStateInspection {
    index: Arc<DiagnosticsBatchIndexer>,
}

impl Inspection for IndexedStateInspection {
    fn definition(&self) -> InspectionDefinition {
        TestInspection.definition()
    }

    fn inspect(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        if self.index.ready.load(Ordering::SeqCst) {
            return Ok(());
        }
        TestInspection.inspect(ctx, document, reporter)
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        TestInspection.quick_fixes()
    }
}

#[test]
fn initial_index_refresh_waits_for_batch_publication() {
    let dir = temp_dir();
    let root = root_string(&dir);
    let path = dir.path().join("test.yaml");
    const SOURCE: &str = "value: bad\n";
    std::fs::write(&path, SOURCE).unwrap();
    let (index_started_sender, index_started) = bounded(1);
    let (release_index_sender, release_index) = bounded::<()>(1);
    let (finish_started_sender, finish_started) = bounded(1);
    let (release_finish_sender, release_finish) = bounded::<()>(1);
    let batch = Arc::new(DiagnosticsBatchIndexer {
        ready: AtomicBool::new(false),
        index_started: index_started_sender,
        release_index,
        finish_started: finish_started_sender,
        release_finish,
    });
    let state_dir = temp_dir();
    let mut scanner = FileScanner::new(&root, state_dir.path().join("scanner.db"), None).unwrap();
    scanner.add_indexer(batch.clone());
    let server = Server::new(Some(scanner), &root, "test");
    server.register_inspection(Arc::new(IndexedStateInspection {
        index: batch.clone(),
    }));
    let client = start_client(&server, |_, _| {});

    client
        .call("initialize", json!({"rootUri": file_uri(dir.path())}))
        .unwrap();
    client.notify("initialized", json!({}));
    recv(&index_started, "initial indexing did not start");

    let uri = file_uri(&path);
    did_open(&client, &uri, 1, SOURCE);
    release_index_sender.send(()).unwrap();
    recv(&finish_started, "batch publication did not start");
    let pulled = server.diagnostic(
        &Context::background(),
        &protocol::DiagnosticParams {
            text_document: protocol::TextDocumentIdentifier::new(uri.clone()),
            ..Default::default()
        },
    );
    assert!(pulled.items.is_empty());
    assert!(
        client
            .published
            .recv_timeout(DIAGNOSTICS_DEBOUNCE * 2)
            .is_err(),
        "diagnostics refreshed before batch publication"
    );
    release_finish_sender.send(()).unwrap();
    let published = client.wait_published();
    assert_eq!(published.uri, uri);
    assert!(published.diagnostics.is_empty());
    client.shutdown().unwrap();
}

#[test]
fn schedule_diagnostics_cancels_superseded_job() {
    let server = Server::new(None, "", "test");
    const URI: &str = "file:///project/template.twig";
    server.schedule_diagnostics(URI, 1, Duration::from_secs(3600));
    let first = lock(&server.diagnostics).jobs.get(URI).cloned().unwrap();
    server.schedule_diagnostics(URI, 2, Duration::from_secs(3600));
    let second = lock(&server.diagnostics).jobs.get(URI).cloned().unwrap();
    assert!(!first.cancel.same(&second.cancel));
    assert!(
        first.cancel.wait_timeout(Duration::from_secs(1)),
        "superseded job was not cancelled"
    );
    assert!(
        lock(&server.diagnostics).jobs[URI]
            .cancel
            .same(&second.cancel)
    );
    server.cancel_diagnostics(URI);
    assert!(
        second.cancel.wait_timeout(Duration::from_secs(1)),
        "active job was not cancelled"
    );
    assert!(!lock(&server.diagnostics).jobs.contains_key(URI));
    server.close_all().unwrap();
}

#[test]
fn refresh_open_document_diagnostics_filters_dependents() {
    let server = Server::new(None, "", "test");
    const ADMIN: &str = "file:///project/Resources/app/administration/src/card.html.twig";
    const PHP: &str = "file:///project/src/Controller.php";
    server.documents.open_document(ADMIN, "<sw-card />", 3);
    server.documents.open_document(PHP, "<?php", 4);
    server.refresh_open_document_diagnostics(|document| document.uri == ADMIN);
    {
        let state = lock(&server.diagnostics);
        assert!(state.jobs.contains_key(ADMIN));
        assert!(!state.jobs.contains_key(PHP));
    }
    server.close_all().unwrap();
}

#[test]
fn diagnostics_cache_reuses_and_invalidates_document_analysis() {
    let dir = temp_dir();
    let uri = file_uri(&dir.path().join("test.yaml"));
    let server = Server::new(None, &root_string(&dir), "test");
    let inspection = Arc::new(CountingInspection::default());
    server.register_inspection(inspection.clone());
    server.documents.open_document(&uri, "value: bad\n", 3);
    let document = server.documents.get_document(&uri).unwrap();
    let ctx = Context::background();
    let first = server.diagnostics_for_document(&ctx, &document);
    let second = server.diagnostics_for_document(&ctx, &document);
    assert_eq!(first, second);
    assert_eq!(inspection.calls.load(Ordering::SeqCst), 1);
    server.schedule_diagnostics(&uri, document.version, Duration::from_secs(3600));
    let third = server.diagnostics_for_document(&ctx, &document);
    assert_eq!(first, third);
    assert_eq!(inspection.calls.load(Ordering::SeqCst), 2);
    server.cancel_diagnostics(&uri);
    server.close_all().unwrap();
}

/// Counts runs and declares one input whose revision tests advance.
#[derive(Default)]
struct DeclaredInspection {
    counting: CountingInspection,
    revision: std::sync::atomic::AtomicU64,
}

impl Inspection for DeclaredInspection {
    fn definition(&self) -> InspectionDefinition {
        self.counting.definition()
    }

    fn inspect(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        self.counting.inspect(ctx, document, reporter)
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        self.counting.quick_fixes()
    }

    fn input_revision(&self, _document: &TextDocument) -> Option<u64> {
        Some(self.revision.load(Ordering::SeqCst))
    }
}

#[test]
fn refresh_reuses_inspections_whose_declared_inputs_are_unchanged() {
    let dir = temp_dir();
    let uri = file_uri(&dir.path().join("test.yaml"));
    let server = Server::new(None, &root_string(&dir), "test");
    let inspection = Arc::new(DeclaredInspection::default());
    server.register_inspection(inspection.clone());
    let revision = &inspection.revision;
    let calls = || inspection.counting.calls.load(Ordering::SeqCst);
    let ctx = Context::background();
    let diagnose = || {
        let document = server.documents.get_document(&uri).unwrap();
        // An index refresh: schedules every open document again.
        server.schedule_diagnostics(&uri, document.version, Duration::from_secs(3600));
        server.diagnostics_for_document(&ctx, &document)
    };
    server.documents.open_document(&uri, "value: bad\n", 3);
    let first = diagnose();
    assert_eq!(first.len(), 1);
    assert_eq!(calls(), 1);

    // Unchanged inputs and document: the previous result is reused.
    assert_eq!(diagnose(), first);
    assert_eq!(calls(), 1);

    // A changed input runs the inspection again.
    revision.store(1, Ordering::SeqCst);
    assert_eq!(diagnose(), first);
    assert_eq!(calls(), 2);

    // So does a changed document.
    server.documents.open_document(&uri, "other: bad\n", 4);
    diagnose();
    assert_eq!(calls(), 3);

    // Configuration changes drop every reused result.
    server.cancel_all_diagnostics();
    diagnose();
    assert_eq!(calls(), 4);

    // Closing the document drops its results.
    server.cancel_diagnostics(&uri);
    assert!(lock(&server.diagnostics).inspection_results.is_empty());
    server.close_all().unwrap();
}

#[test]
fn index_refresh_skips_documents_whose_declared_inputs_are_unchanged() {
    let dir = temp_dir();
    let uri = file_uri(&dir.path().join("test.yaml"));
    let server = Server::new(None, &root_string(&dir), "test");
    let inspection = Arc::new(DeclaredInspection::default());
    server.register_inspection(inspection.clone());
    server.documents.open_document(&uri, "value: bad\n", 3);
    let document = server.documents.get_document(&uri).unwrap();
    let scheduled = || lock(&server.diagnostics).jobs.contains_key(&uri);

    // Never diagnosed: nothing is known about its inputs.
    server.refresh_after_index_change();
    assert!(scheduled());
    server.cancel_diagnostics(&uri);

    server.diagnostics_for_document(&Context::background(), &document);
    server.refresh_after_index_change();
    assert!(!scheduled(), "unchanged inputs");

    inspection.revision.store(1, Ordering::SeqCst);
    server.refresh_after_index_change();
    assert!(scheduled(), "changed input");
    server.close_all().unwrap();
}

#[test]
fn index_refresh_keeps_documents_with_undeclared_inspections() {
    let dir = temp_dir();
    let uri = file_uri(&dir.path().join("test.yaml"));
    let server = Server::new(None, &root_string(&dir), "test");
    server.register_inspection(Arc::new(CountingInspection::default()));
    server.documents.open_document(&uri, "value: bad\n", 3);
    let document = server.documents.get_document(&uri).unwrap();
    server.diagnostics_for_document(&Context::background(), &document);
    server.refresh_after_index_change();
    assert!(lock(&server.diagnostics).jobs.contains_key(&uri));
    server.close_all().unwrap();
}

/// Reports `test.slow` on the whole document after 300 ms.
struct SlowInspection;

impl Inspection for SlowInspection {
    fn definition(&self) -> InspectionDefinition {
        test_definition("test.slow", false)
    }

    fn inspect(
        &self,
        _ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError> {
        std::thread::sleep(Duration::from_millis(300));
        reporter.report(Problem {
            id: "test.slow".into(),
            range: swlsp_syntax::TextRange::new(0.into(), TextSize::of(document.source())),
            message: "slow".to_owned(),
            ..Problem::default()
        })
    }

    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>> {
        Vec::new()
    }
}

#[test]
fn slow_inspections_do_not_hold_back_the_others() {
    let dir = temp_dir();
    let server = Server::new(None, &root_string(&dir), "test");
    // Registered first; the first run has no cost data to reorder by.
    server.register_inspection(Arc::new(SlowInspection));
    server.register_inspection(Arc::new(TestInspection));
    let client = start_client(&server, |_, _| {});
    const URI: &str = "file:///project/test.yaml";
    let codes = |published: &protocol::PublishDiagnosticsParams| -> Vec<String> {
        published
            .diagnostics
            .iter()
            .map(|diagnostic| crate::inspection::code_string(diagnostic.code.as_ref()))
            .collect()
    };
    did_open(&client, URI, 1, "value: bad\n");
    // Unknown costs: registration order. The slow inspection uses up the
    // budget, so its result is published before the rest runs.
    assert_eq!(codes(&client.wait_published()), ["test.slow"]);
    assert_eq!(
        codes(&client.wait_published()),
        ["test.slow", "test.invalid-value"]
    );

    did_change(&client, URI, 2, "value: bad\n");
    // The slow inspection is known now: the fast one is published first.
    let partial = client.wait_published();
    assert_eq!(partial.version, 2);
    assert_eq!(codes(&partial), ["test.invalid-value"]);
    let complete = client.wait_published();
    assert_eq!(complete.version, 2);
    assert_eq!(codes(&complete), ["test.slow", "test.invalid-value"]);
    {
        let state = lock(&server.diagnostics);
        assert!(state.costs["test.slow"] >= 0.25);
        // Reported once as over budget; the fast inspection is not.
        assert!(state.slow_reported.contains("test.slow"));
        assert!(!state.slow_reported.contains("test.invalid-value"));
    }
    client.shutdown().unwrap();
}

#[test]
fn diagnostic_performance_trace_defaults_off() {
    if std::env::var_os("SHOPWARE_LSP_TRACE_DIAGNOSTICS").is_none() {
        assert!(!diagnostic_performance_trace_enabled());
    }
}

#[test]
fn diagnostic_delivery_negotiation_over_json_rpc() {
    for (name, pull, refresh) in [
        ("legacy push", false, false),
        ("pull and push capabilities", true, false),
        ("pull with refresh", true, true),
    ] {
        let dir = temp_dir();
        let server = Server::new(None, &root_string(&dir), "test");
        let inspection = Arc::new(CountingInspection::default());
        server.register_inspection(inspection.clone());
        let (refreshed_sender, refreshed) = unbounded();
        let client = start_client(&server, move |method: &str, _: &Value| {
            if method == "workspace/diagnostic/refresh" {
                let _ = refreshed_sender.send(());
            }
        });
        let mut text = json!({"publishDiagnostics": {}});
        if pull {
            text["diagnostic"] = json!({});
        }
        let initialized = client
            .call(
                "initialize",
                json!({
                    "rootUri": file_uri(temp_dir().path()),
                    "capabilities": {
                        "textDocument": text,
                        "workspace": {"diagnostics": {"refreshSupport": refresh}},
                    },
                }),
            )
            .unwrap();
        assert_eq!(
            initialized["capabilities"]
                .get("diagnosticProvider")
                .is_some(),
            pull,
            "{name}"
        );
        const URI: &str = "file:///project/test.yaml";
        did_open(&client, URI, 1, "value: bad\n");
        let pull_report = |want: usize| {
            let report = client
                .call(
                    "textDocument/diagnostic",
                    json!({"textDocument": {"uri": URI}}),
                )
                .unwrap();
            assert_eq!(report["kind"], "full", "{name}");
            assert_eq!(report["items"].as_array().unwrap().len(), want, "{name}");
        };
        if pull {
            if refresh {
                recv(&refreshed, "missing open refresh");
            }
            pull_report(1);
            pull_report(1);
            assert_eq!(inspection.calls.load(Ordering::SeqCst), 1, "{name}");
            server.publish_diagnostics(&Context::background(), None);
            if refresh {
                recv(&refreshed, "missing workspace refresh");
            }
            pull_report(1);
            assert_eq!(
                inspection.calls.load(Ordering::SeqCst),
                2,
                "{name}: workspace updates must invalidate cached reports"
            );
        } else {
            assert_eq!(client.wait_published().diagnostics.len(), 1, "{name}");
        }
        did_change(&client, URI, 2, "value: bad\n");
        if pull {
            if refresh {
                recv(&refreshed, "missing change refresh");
            }
            pull_report(1);
        } else {
            assert_eq!(client.wait_published().version, 2, "{name}");
        }
        client.notify(
            "textDocument/didClose",
            json!({"textDocument": {"uri": URI}}),
        );
        pull_report(0);
        if !pull {
            assert!(client.wait_published().diagnostics.is_empty(), "{name}");
        }
        if let Ok(unexpected) = client.published.recv_timeout(DIAGNOSTICS_DEBOUNCE * 2) {
            panic!("{name}: unexpected push: {unexpected:?}");
        }
        if !refresh {
            assert!(refreshed.try_recv().is_err(), "{name}");
        }
        client.shutdown().unwrap();
    }
}

/// Clients wait for `shopware/indexingCompleted` and then pull. The initial
/// index suspension must lift before that notification, not after the file
/// watcher starts, or the first pull after it returns an empty report.
#[test]
fn initial_index_resumes_diagnostics_before_indexing_completed() {
    let dir = temp_dir();
    let root = root_string(&dir);
    let path = dir.path().join("test.yaml");
    const SOURCE: &str = "value: bad\n";
    std::fs::write(&path, SOURCE).unwrap();
    // Directories make the watcher registration that follows the initial
    // index slow enough to expose a resume that waits for it.
    for index in 0..200 {
        std::fs::create_dir_all(dir.path().join(format!("dir{index}/nested"))).unwrap();
    }
    let (index_started_sender, index_started) = bounded(1);
    let (release_index_sender, release_index) = bounded::<()>(1);
    let (finish_started_sender, _finish_started) = bounded(1);
    let (release_finish_sender, release_finish) = bounded::<()>(1);
    let batch = Arc::new(DiagnosticsBatchIndexer {
        ready: AtomicBool::new(false),
        index_started: index_started_sender,
        release_index,
        finish_started: finish_started_sender,
        release_finish,
    });
    let state_dir = temp_dir();
    let mut scanner = FileScanner::new(&root, state_dir.path().join("scanner.db"), None).unwrap();
    scanner.add_indexer(batch);
    let server = Server::new(Some(scanner), &root, "test");
    server.register_inspection(Arc::new(TestInspection));
    let uri = file_uri(&path);
    let (completed_sender, completed) = unbounded();
    let observing = server.clone();
    let observed_uri = uri.clone();
    let client = start_client(&server, move |method: &str, _: &Value| {
        if method != "shopware/indexingCompleted" {
            return;
        }
        // Observe the server the moment the client sees the notification,
        // before it can send anything else.
        let report = observing.diagnostic(
            &Context::background(),
            &protocol::DiagnosticParams {
                text_document: protocol::TextDocumentIdentifier::new(observed_uri.clone()),
                ..Default::default()
            },
        );
        let _ = completed_sender.send(report.items.len());
    });

    client
        .call(
            "initialize",
            json!({
                "rootUri": file_uri(dir.path()),
                "capabilities": {"textDocument": {"diagnostic": {}}},
            }),
        )
        .unwrap();
    client.notify("initialized", json!({}));
    recv(&index_started, "initial indexing did not start");
    did_open(&client, &uri, 1, SOURCE);
    // A pull during the initial index keeps Go's suspended (empty) report.
    let during = client
        .call(
            "textDocument/diagnostic",
            json!({"textDocument": {"uri": uri}}),
        )
        .unwrap();
    assert_eq!(during["items"], json!([]));
    release_index_sender.send(()).unwrap();
    release_finish_sender.send(()).unwrap();

    assert_eq!(
        recv(&completed, "indexing did not complete"),
        1,
        "diagnostics were still suspended when indexingCompleted was sent"
    );
    let report = client
        .call(
            "textDocument/diagnostic",
            json!({"textDocument": {"uri": uri}}),
        )
        .unwrap();
    assert_eq!(report["items"].as_array().map(Vec::len), Some(1));
    client.call("shutdown", json!({})).unwrap();
    client.notify("exit", json!({}));
    client.shutdown().unwrap();
}

/// Suppresses one code reported at one offset.
struct SuppressAt(u32, &'static str);

impl Suppressions for SuppressAt {
    fn suppresses(&self, offset: TextSize, code: &DiagnosticId) -> bool {
        offset == TextSize::from(self.0) && code.as_str() == self.1
    }
}

#[test]
fn registered_suppressions_apply_to_every_inspection() {
    // `TestInspection` reports `test.invalid-value` at byte 7.
    for (offset, code, count) in [
        (7, "test.invalid-value", 0),
        (7, "test.other", 1),
        (0, "test.invalid-value", 1),
    ] {
        let server = Server::new(None, "", "test");
        server.register_inspection(Arc::new(TestInspection));
        server.register_suppressions(
            LanguageId::Yaml,
            Arc::new(move |_: &TextDocument| {
                Box::new(SuppressAt(offset, code)) as Box<dyn Suppressions>
            }),
        );
        let document = Arc::new(TextDocument::new("file:///fixture.yaml", "value: bad\n", 1));
        assert_eq!(
            server
                .diagnostics_for_document(&Context::background(), &document)
                .len(),
            count,
            "{code} at {offset}"
        );
        // Other languages are unaffected.
        let json = Arc::new(TextDocument::new("file:///fixture.json", "{}", 1));
        assert!(
            server
                .diagnostics_for_document(&Context::background(), &json)
                .is_empty()
        );
    }
}
