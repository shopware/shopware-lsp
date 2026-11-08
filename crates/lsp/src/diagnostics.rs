//! Diagnostics scheduling, caching, push/pull delivery and the inspection
//! problem reporter.

use std::collections::HashMap;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use serde_json::Value;
use swlsp_lsp_protocol as protocol;
use swlsp_projectconfig::{DiagnosticPolicy, Severity};
use swlsp_rewrite::ElementHandle;
use swlsp_syntax::{LineIndex, TextRange, TextSize};

use crate::configuration::{
    diagnostic_rule_severity, inspection_domain, inspection_enabled_by_policy,
    protocol_diagnostic_severity,
};
use crate::context::{BoxError, CancellationToken, Context, is_cancellation, lock};
use crate::document::{TextDocument, diagnostic_element};
use crate::inspection::{
    BoundFixEnvelope, DIAGNOSTIC_ENVELOPE_SCHEMA, DiagnosticEnvelope, Problem, ProblemReporter,
    RegisteredInspection, code_string, encode_diagnostic_data,
};
use crate::server::{Server, read, trace_enabled};

/// Delay between a document change and its push diagnostics.
pub const DIAGNOSTICS_DEBOUNCE: Duration = Duration::from_millis(150);

#[derive(Clone)]
pub(crate) struct DiagnosticsJob {
    pub cancel: CancellationToken,
}

struct CacheEntry {
    document: Arc<TextDocument>,
    generation: u64,
    diagnostics: Vec<protocol::Diagnostic>,
}

#[derive(Default)]
pub(crate) struct DiagnosticsState {
    pub jobs: HashMap<String, DiagnosticsJob>,
    generations: HashMap<String, u64>,
    cache: HashMap<String, CacheEntry>,
    suspensions: u32,
}

/// One diagnostics suspension held by a full index rebuild. It resumes at
/// most once: explicitly as soon as the rebuilt indexes are published (before
/// `shopware/indexingCompleted` reaches the client), or on drop when the
/// rebuild fails, is cancelled, or never starts.
pub(crate) struct DiagnosticsSuspension {
    server: Option<Server>,
    active: AtomicBool,
}

impl DiagnosticsSuspension {
    /// Suspends diagnostics when `enabled`; otherwise returns an inert guard.
    pub(crate) fn new(server: &Server, enabled: bool) -> Self {
        if enabled {
            server.suspend_diagnostics();
        }
        Self {
            server: enabled.then(|| server.clone()),
            active: AtomicBool::new(enabled),
        }
    }

    pub(crate) fn resume(&self) {
        if self.active.swap(false, Ordering::AcqRel)
            && let Some(server) = &self.server
        {
            server.resume_diagnostics();
        }
    }
}

impl Drop for DiagnosticsSuspension {
    fn drop(&mut self) {
        self.resume();
    }
}

pub(crate) fn diagnostic_performance_trace_enabled() -> bool {
    trace_enabled("SHOPWARE_LSP_TRACE_DIAGNOSTICS")
}

impl Server {
    /// Schedules diagnostics for the given URIs, or for every open document
    /// when `files` is `None`.
    pub fn publish_diagnostics(&self, _ctx: &Context, files: Option<&[String]>) {
        let documents: Vec<(String, i32)> = match files {
            None => self
                .documents
                .documents()
                .into_iter()
                .map(|document| (document.uri.clone(), document.version))
                .collect(),
            Some(files) => files
                .iter()
                .map(|uri| {
                    let version = self
                        .documents
                        .get_document(uri)
                        .map_or(0, |document| document.version);
                    (uri.clone(), version)
                })
                .collect(),
        };
        for (uri, version) in documents {
            self.schedule_diagnostics(&uri, version, Duration::ZERO);
        }
    }

    /// Schedules a debounced re-analysis for every open document accepted by
    /// `matches`; workspace overlays use it when an open dependency changes.
    pub fn refresh_open_document_diagnostics(&self, matches: impl Fn(&TextDocument) -> bool) {
        if self.cli_mode() {
            return;
        }
        for document in self.documents.documents() {
            if matches(&document) {
                self.schedule_diagnostics(&document.uri, document.version, DIAGNOSTICS_DEBOUNCE);
            }
        }
    }

    pub(crate) fn schedule_diagnostics(&self, uri: &str, version: i32, delay: Duration) {
        let job = DiagnosticsJob {
            cancel: self.lifecycle.child(),
        };
        {
            let mut state = lock(&self.diagnostics);
            *state.generations.entry(uri.to_owned()).or_default() += 1;
            state.cache.remove(uri);
            if let Some(previous) = state.jobs.remove(uri) {
                previous.cancel.cancel();
            }
            if state.suspensions > 0 {
                drop(state);
                job.cancel.cancel();
                return;
            }
            state.jobs.insert(uri.to_owned(), job.clone());
        }
        let server = self.clone();
        let uri_owned = uri.to_owned();
        let token = job.cancel.clone();
        let started = self.start_background(move |_| {
            let ctx = Context::with_token(token.clone());
            if !token.wait_timeout(delay) {
                server.publish_document_diagnostics(&ctx, &uri_owned, version);
            }
            server.remove_job(&uri_owned, &token);
        });
        if !started {
            job.cancel.cancel();
            self.remove_job(uri, &job.cancel);
        }
    }

    fn remove_job(&self, uri: &str, token: &CancellationToken) {
        let mut state = lock(&self.diagnostics);
        if state
            .jobs
            .get(uri)
            .is_some_and(|job| job.cancel.same(token))
        {
            state.jobs.remove(uri);
        }
    }

    /// Prevents diagnostics from observing a workspace while a full index
    /// rebuild publishes its repositories. Nested rebuilds share the
    /// suspension; the last resume republishes against the newest documents.
    pub(crate) fn suspend_diagnostics(&self) {
        let _publish = lock(&self.diagnostics_publish);
        let mut state = lock(&self.diagnostics);
        state.suspensions += 1;
        if state.suspensions == 1 {
            for (_, job) in state.jobs.drain() {
                job.cancel.cancel();
            }
            state.cache.clear();
            state.generations.clear();
        }
    }

    pub(crate) fn resume_diagnostics(&self) {
        let resumed = {
            let mut state = lock(&self.diagnostics);
            if state.suspensions == 0 {
                return;
            }
            state.suspensions -= 1;
            state.suspensions == 0
        };
        if resumed && !self.cli_mode() {
            let ctx = Context::with_token(self.lifecycle.clone());
            self.publish_diagnostics(&ctx, None);
        }
    }

    pub(crate) fn cancel_diagnostics(&self, uri: &str) {
        let mut state = lock(&self.diagnostics);
        if let Some(job) = state.jobs.remove(uri) {
            job.cancel.cancel();
        }
        state.cache.remove(uri);
        state.generations.remove(uri);
    }

    pub(crate) fn cancel_all_diagnostics(&self) {
        let mut state = lock(&self.diagnostics);
        for (_, job) in state.jobs.drain() {
            job.cancel.cancel();
        }
        state.cache.clear();
        state.generations.clear();
    }

    fn publish_document_diagnostics(&self, ctx: &Context, uri: &str, version: i32) {
        let Some(conn) = self.connection() else {
            return;
        };
        let Some(document) = self.documents.get_document(uri) else {
            return;
        };
        if document.version != version {
            return;
        }
        let (pull, refresh) = {
            let session = read(&self.session);
            (session.pull_diagnostics, session.diagnostic_refresh_support)
        };
        if pull {
            // Invalidated through schedule_diagnostics; let the client pull
            // the latest snapshot. Never publish a second set.
            if refresh
                && !ctx.is_cancelled()
                && let Err(error) = conn.call(
                    "workspace/diagnostic/refresh",
                    Value::Null,
                    ctx.token(),
                    None,
                )
                && !ctx.is_cancelled()
            {
                log::warn!("Error refreshing diagnostics: {error}");
            }
            return;
        }
        let diagnostics = self.diagnostics_for_document(ctx, &document);
        let _publish = lock(&self.diagnostics_publish);
        let current = self.documents.get_document(uri);
        if ctx.is_cancelled() || !current.is_some_and(|current| Arc::ptr_eq(&current, &document)) {
            return;
        }
        let params = protocol::PublishDiagnosticsParams {
            uri: uri.to_owned(),
            version,
            diagnostics,
        };
        if let Err(error) = conn.notify("textDocument/publishDiagnostics", params) {
            log::warn!("Error publishing diagnostics: {error}");
        }
    }

    /// Clears pushed diagnostics when an editor closes a document. Shares
    /// the publish lock so a cancelled analysis cannot publish afterwards.
    pub(crate) fn clear_published_diagnostics(&self, uri: &str) {
        if read(&self.session).pull_diagnostics {
            return;
        }
        let Some(conn) = self.connection() else {
            return;
        };
        let _publish = lock(&self.diagnostics_publish);
        let params = protocol::PublishDiagnosticsParams {
            uri: uri.to_owned(),
            version: 0,
            diagnostics: Vec::new(),
        };
        if let Err(error) = conn.notify("textDocument/publishDiagnostics", params) {
            log::warn!("Error clearing diagnostics: {error}");
        }
    }

    /// `textDocument/diagnostic`: always a full report.
    pub(crate) fn diagnostic(
        &self,
        ctx: &Context,
        params: &protocol::DiagnosticParams,
    ) -> protocol::DiagnosticResult {
        let mut result = protocol::DiagnosticResult {
            kind: "full".into(),
            ..protocol::DiagnosticResult::default()
        };
        if let Some(document) = self.documents.get_document(&params.text_document.uri) {
            result.items = self.diagnostics_for_document(ctx, &document);
        }
        result
    }

    pub(crate) fn diagnostics_for_document(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Vec<protocol::Diagnostic> {
        if !self.diagnostic_policy(&document.uri).enabled {
            return Vec::new();
        }
        let generation = {
            let state = lock(&self.diagnostics);
            if state.suspensions > 0 {
                return Vec::new();
            }
            let generation = state.generations.get(&document.uri).copied().unwrap_or(0);
            if let Some(cached) = state.cache.get(&document.uri)
                && Arc::ptr_eq(&cached.document, document)
                && cached.generation == generation
            {
                return cached.diagnostics.clone();
            }
            generation
        };
        let diagnostics = self.collect_diagnostics(ctx, document);
        if ctx.is_cancelled() {
            return diagnostics;
        }
        let mut state = lock(&self.diagnostics);
        if state.suspensions > 0 {
            return Vec::new();
        }
        if state.generations.get(&document.uri).copied().unwrap_or(0) == generation {
            state.cache.insert(
                document.uri.clone(),
                CacheEntry {
                    document: document.clone(),
                    generation,
                    diagnostics: diagnostics.clone(),
                },
            );
        }
        diagnostics
    }

    /// Runs every applicable inspection and normalizes the result.
    pub(crate) fn collect_diagnostics(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Vec<protocol::Diagnostic> {
        let mut all = Vec::new();
        let policy = self.diagnostic_policy(&document.uri);
        if !policy.enabled {
            return all;
        }
        let trace = diagnostic_performance_trace_enabled();
        let request_started = Instant::now();
        let registry = self.registry();
        for inspection in registry.inspections.for_language(document.language) {
            if ctx.is_cancelled() {
                break;
            }
            let id = inspection.definition.id.as_str();
            if !self.inspection_presented_to_client(id) {
                continue;
            }
            let domain = inspection_domain(id);
            if (!domain.is_empty() && !self.domain_enabled(domain))
                || !inspection_enabled_by_policy(&policy, id)
                || !inspection_has_enabled_rule(inspection, &policy)
            {
                continue;
            }
            let inspection_started = Instant::now();
            let mut reporter = InspectionProblemReporter {
                document,
                inspection,
                policy: &policy,
                diagnostics: Vec::new(),
            };
            if let Err(error) = inspection.inspection.inspect(ctx, document, &mut reporter) {
                if ctx.is_cancelled() || is_cancellation(error.as_ref()) {
                    break;
                }
                log::warn!("Error getting diagnostics from inspection {id}: {error}");
                continue;
            }
            if trace {
                log::info!(
                    "Diagnostic inspection {id} took {:?} ({} findings)",
                    inspection_started.elapsed(),
                    reporter.diagnostics.len()
                );
            }
            all.extend(reporter.diagnostics);
        }
        let normalize_started = Instant::now();
        let result = normalize_diagnostics(all);
        if trace {
            log::info!(
                "Diagnostic request {} took {:?} ({} findings, normalization {:?})",
                document.uri,
                request_started.elapsed(),
                result.len(),
                normalize_started.elapsed()
            );
        }
        result
    }
}

pub(crate) fn inspection_has_enabled_rule(
    inspection: &RegisteredInspection,
    policy: &DiagnosticPolicy,
) -> bool {
    inspection.problems.iter().any(
        |(id, definition)| match diagnostic_rule_severity(policy, id) {
            Some(severity) => *severity != Severity::Off,
            None => !definition.disabled_by_default,
        },
    )
}

struct InspectionProblemReporter<'a> {
    document: &'a Arc<TextDocument>,
    inspection: &'a RegisteredInspection,
    policy: &'a DiagnosticPolicy,
    diagnostics: Vec<protocol::Diagnostic>,
}

impl ProblemReporter for InspectionProblemReporter<'_> {
    fn report(&mut self, problem: Problem) -> Result<(), BoxError> {
        let inspection_id = &self.inspection.definition.id;
        let Some(definition) = self.inspection.problems.get(&problem.id) else {
            return Err(format!(
                "inspection {:?} reported undeclared diagnostic {:?}",
                inspection_id, problem.id.0
            )
            .into());
        };
        let configured = diagnostic_rule_severity(self.policy, &problem.id);
        match configured {
            Some(Severity::Off) => return Ok(()),
            None if definition.disabled_by_default => return Ok(()),
            _ => {}
        }
        let source_len = TextSize::of(self.document.source());
        if problem.range.end() > source_len {
            return Err(format!(
                "diagnostic {:?} has invalid range {:?}",
                problem.id.0, problem.range
            )
            .into());
        }
        let element = problem.element.clone().or_else(|| {
            self.document
                .root()
                .and_then(|root| diagnostic_element(&root, problem.range))
        });
        let (Some(element), Some(language)) = (element, self.document.language) else {
            return Err(format!(
                "diagnostic {:?} anchor: syntax element handle cannot be resolved",
                problem.id.0
            )
            .into());
        };
        let anchor = ElementHandle::new(
            self.document.uri.clone(),
            self.document.version,
            language,
            element,
        );
        let mut fixes = Vec::with_capacity(problem.fixes.len());
        for bound in &problem.fixes {
            if !self.inspection.fixes.contains_key(&bound.id) {
                return Err(format!(
                    "diagnostic {:?} bound unregistered fix {:?}",
                    problem.id.0, bound.id.0
                )
                .into());
            }
            if let Some(error) = bound.encode_error() {
                return Err(format!(
                    "diagnostic {:?} fix {:?} payload: {error}",
                    problem.id.0, bound.id.0
                )
                .into());
            }
            fixes.push(BoundFixEnvelope {
                id: bound.id.clone(),
                payload: bound.payload.clone(),
            });
        }
        let envelope = DiagnosticEnvelope {
            schema: DIAGNOSTIC_ENVELOPE_SCHEMA,
            inspection: inspection_id.clone(),
            code: problem.id.clone(),
            uri: self.document.uri.clone(),
            document_version: self.document.version,
            anchor,
            payload: problem.payload,
            fixes,
        };
        let data = encode_diagnostic_data(&envelope)
            .map_err(|error| format!("diagnostic {:?} data: {error}", problem.id.0))?;
        let severity = match configured {
            Some(configured) => protocol_diagnostic_severity(configured),
            None => problem.severity.unwrap_or(definition.default_severity),
        };
        let source = if problem.source.is_empty() {
            definition.source.clone()
        } else {
            problem.source
        };
        self.diagnostics.push(protocol::Diagnostic {
            range: protocol_range(self.document.line_index(), problem.range),
            severity: Some(severity),
            code: Some(Value::String(problem.id.0)),
            source,
            message: problem.message,
            tags: problem.tags,
            related_information: problem.related_information,
            data: Some(data),
        });
        Ok(())
    }
}

/// Converts a byte range to an LSP UTF-16 range.
pub use swlsp_rewrite::protocol_range;

/// Converts an LSP UTF-16 range to a byte range (clamping like the line
/// index).
pub fn text_range(line_index: &LineIndex, range: protocol::Range) -> TextRange {
    let start = line_index.offset_utf16(range.start.line, range.start.character);
    let end = line_index.offset_utf16(range.end.line, range.end.character);
    TextRange::new(TextSize::from(start), TextSize::from(end.max(start)))
}

/// Sorts by position, code and message and drops exact duplicates;
/// overlapping inspections are expected.
pub(crate) fn normalize_diagnostics(
    mut values: Vec<protocol::Diagnostic>,
) -> Vec<protocol::Diagnostic> {
    values.sort_by(|left, right| {
        position_key(left)
            .cmp(&position_key(right))
            .then_with(|| code_string(left.code.as_ref()).cmp(&code_string(right.code.as_ref())))
            .then_with(|| left.message.cmp(&right.message))
    });
    values.dedup_by(|current, previous| {
        previous.range == current.range
            && code_string(previous.code.as_ref()) == code_string(current.code.as_ref())
            && previous.source == current.source
            && previous.message == current.message
    });
    values
}

fn position_key(diagnostic: &protocol::Diagnostic) -> (u32, u32, u32, u32) {
    let range = diagnostic.range;
    (
        range.start.line,
        range.start.character,
        range.end.line,
        range.end.character,
    )
}
