//! Diagnostics scheduling, caching, push/pull delivery and the inspection
//! problem reporter.

use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Weak};
use std::time::{Duration, Instant};

use serde_json::Value;
use swlsp_lsp_protocol as protocol;
use swlsp_projectconfig::{DiagnosticPolicy, Severity};
use swlsp_rewrite::ElementHandle;
use swlsp_syntax::{LineIndex, TextRange, TextSize};

use crate::configuration::{
    diagnostic_rule_severity, inspection_enabled_by_policy, protocol_diagnostic_severity,
};
use crate::context::{BoxError, CancellationToken, Context, is_cancellation, lock};
use crate::document::{TextDocument, diagnostic_element};
use crate::inspection::{
    BoundFixEnvelope, DIAGNOSTIC_ENVELOPE_SCHEMA, DiagnosticEnvelope, InspectionDefinition,
    Problem, ProblemReporter, RegisteredInspection, Suppressions, code_string,
    encode_diagnostic_data,
};
use crate::server::{Server, read, trace_enabled};

/// Delay between a document change and its push diagnostics.
pub const DIAGNOSTICS_DEBOUNCE: Duration = Duration::from_millis(150);

/// How long one document's diagnostics may wait for slow inspections before
/// the results so far are published (push mode), and how long one
/// inspection may take before it is reported as slow.
pub const INSPECTION_BUDGET: Duration = Duration::from_millis(200);

/// Weight of the newest run in an inspection's average cost.
const COST_WEIGHT: f64 = 0.25;

#[derive(Clone)]
pub(crate) struct DiagnosticsJob {
    pub cancel: CancellationToken,
}

struct CacheEntry {
    document: Arc<TextDocument>,
    generation: u64,
    diagnostics: Vec<protocol::Diagnostic>,
}

/// The last diagnostics of each inspection that declares its inputs, for one
/// open document and diagnostic policy. A refresh after an index change
/// reuses them while the inspection's input revision is unchanged; see
/// [`crate::Inspection::input_revision`].
pub(crate) struct InspectionResults {
    document: Weak<TextDocument>,
    policy: DiagnosticPolicy,
    /// Whether every inspection that ran declared its inputs, so the
    /// document's diagnostics change only with those revisions.
    fully_declared: bool,
    /// Inspection ID to (input revision, diagnostics).
    results: HashMap<String, (u64, Vec<protocol::Diagnostic>)>,
}

impl InspectionResults {
    fn new(document: &Arc<TextDocument>, policy: &DiagnosticPolicy) -> Self {
        InspectionResults {
            document: Arc::downgrade(document),
            policy: policy.clone(),
            fully_declared: true,
            results: HashMap::new(),
        }
    }

    fn matches(&self, document: &Arc<TextDocument>, policy: &DiagnosticPolicy) -> bool {
        Weak::ptr_eq(&self.document, &Arc::downgrade(document)) && self.policy == *policy
    }
}

#[derive(Default)]
pub(crate) struct DiagnosticsState {
    pub jobs: HashMap<String, DiagnosticsJob>,
    generations: HashMap<String, u64>,
    cache: HashMap<String, CacheEntry>,
    /// Per-inspection results; unlike `cache`, they survive a refresh.
    pub(crate) inspection_results: HashMap<String, InspectionResults>,
    /// Average run time per inspection ID, in seconds; inspections run
    /// cheapest first.
    pub(crate) costs: HashMap<String, f64>,
    /// Advanced by index and configuration changes; the workspace report is
    /// recomputed when it moves.
    pub(crate) workspace_generation: u64,
    pub(crate) workspace_report: Option<Arc<crate::workspace_diagnostics::WorkspaceReport>>,
    /// Inspections already reported for exceeding [`INSPECTION_BUDGET`].
    pub(crate) slow_reported: std::collections::HashSet<String>,
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

    /// Schedules diagnostics after committed index changes: every open
    /// document except those whose inspections all declare their inputs and
    /// whose input revisions are unchanged, which would report the same.
    pub(crate) fn refresh_after_index_change(&self) {
        lock(&self.diagnostics).workspace_generation += 1;
        let registry = self.registry();
        for document in self.documents.documents() {
            if self.inputs_unchanged(&registry, &document) {
                continue;
            }
            self.schedule_diagnostics(&document.uri, document.version, Duration::ZERO);
        }
    }

    fn inputs_unchanged(
        &self,
        registry: &crate::server::Registry,
        document: &Arc<TextDocument>,
    ) -> bool {
        let policy = self.diagnostic_policy(&document.uri);
        let state = lock(&self.diagnostics);
        let Some(results) = state.inspection_results.get(&document.uri) else {
            return false;
        };
        if !results.fully_declared || !results.matches(document, &policy) {
            return false;
        }
        registry
            .inspections
            .for_language(document.language)
            .iter()
            .filter(|inspection| {
                self.inspection_active(&inspection.definition, &policy)
                    && inspection_has_enabled_rule(inspection, &policy)
            })
            .all(|inspection| {
                let revision = inspection.inspection.input_revision(document);
                revision.is_some()
                    && results
                        .results
                        .get(&inspection.definition.id)
                        .is_some_and(|(cached, _)| Some(*cached) == revision)
            })
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
        state.workspace_generation += 1;
        if state.suspensions == 1 {
            for (_, job) in state.jobs.drain() {
                job.cancel.cancel();
            }
            state.cache.clear();
            state.inspection_results.clear();
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
        state.inspection_results.remove(uri);
        state.generations.remove(uri);
    }

    pub(crate) fn cancel_all_diagnostics(&self) {
        let mut state = lock(&self.diagnostics);
        state.workspace_generation += 1;
        for (_, job) in state.jobs.drain() {
            job.cancel.cancel();
        }
        state.cache.clear();
        state.inspection_results.clear();
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
        let publish = |diagnostics: Vec<protocol::Diagnostic>| {
            let _publish = lock(&self.diagnostics_publish);
            let current = self.documents.get_document(uri);
            if ctx.is_cancelled()
                || !current.is_some_and(|current| Arc::ptr_eq(&current, &document))
            {
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
        };
        // Slow inspections do not hold back the others: their partial
        // results are published first, then the complete set.
        let diagnostics =
            self.diagnostics_for_document_with_progress(ctx, &document, Some(&publish));
        publish(diagnostics);
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
        self.diagnostics_for_document_with_progress(ctx, document, None)
    }

    /// [`Self::diagnostics_for_document`] with partial results reported
    /// through `progress` while slow inspections run.
    fn diagnostics_for_document_with_progress(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        progress: Option<&dyn Fn(Vec<protocol::Diagnostic>)>,
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
        let diagnostics = self.collect_diagnostics_with_progress(ctx, document, progress);
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
    #[cfg(test)]
    pub(crate) fn collect_diagnostics(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
    ) -> Vec<protocol::Diagnostic> {
        self.collect_diagnostics_with_progress(ctx, document, None)
    }

    /// [`Self::collect_diagnostics`], calling `progress` at most once with
    /// the diagnostics so far when slow inspections remain: before starting
    /// one whose average cost exceeds [`INSPECTION_BUDGET`], or once the
    /// document has used up the budget. Inspections still to run contribute
    /// their previous diagnostics of the same document snapshot, if any.
    pub(crate) fn collect_diagnostics_with_progress(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        progress: Option<&dyn Fn(Vec<protocol::Diagnostic>)>,
    ) -> Vec<protocol::Diagnostic> {
        let mut all = Vec::new();
        let policy = self.diagnostic_policy(&document.uri);
        if !policy.enabled {
            return all;
        }
        let trace = diagnostic_performance_trace_enabled();
        let request_started = Instant::now();
        let registry = self.registry();
        let suppressions = document
            .language
            .and_then(|language| registry.suppressions.get(&language))
            .map(|read| read(document));
        let mut previous = {
            let mut state = lock(&self.diagnostics);
            state
                .inspection_results
                .remove(&document.uri)
                .filter(|results| results.matches(document, &policy))
        };
        let mut current = InspectionResults::new(document, &policy);
        let mut complete = true;
        let costs = lock(&self.diagnostics).costs.clone();
        let cost = |id: &str| costs.get(id).copied().unwrap_or(0.0);
        let mut inspections: Vec<&Arc<RegisteredInspection>> = registry
            .inspections
            .for_language(document.language)
            .iter()
            .filter(|inspection| {
                self.inspection_active(&inspection.definition, &policy)
                    && inspection_has_enabled_rule(inspection, &policy)
            })
            .collect();
        // Cheapest first, so a partial publication carries as much as
        // possible; ties keep registration order.
        inspections.sort_by(|left, right| {
            cost(&left.definition.id).total_cmp(&cost(&right.definition.id))
        });
        let mut measured: Vec<(String, f64)> = Vec::new();
        let mut progress = progress;
        for (position, inspection) in inspections.iter().enumerate() {
            if ctx.is_cancelled() {
                break;
            }
            let id = inspection.definition.id.as_str();
            if let Some(report) = progress
                && (request_started.elapsed() >= INSPECTION_BUDGET
                    || cost(id) >= INSPECTION_BUDGET.as_secs_f64())
            {
                let mut partial = all.clone();
                if let Some(previous) = &previous {
                    for pending in &inspections[position..] {
                        if let Some((_, diagnostics)) = previous.results.get(&pending.definition.id)
                        {
                            partial.extend(diagnostics.iter().cloned());
                        }
                    }
                }
                report(normalize_diagnostics(partial));
                progress = None;
            }
            // Read before running, so a change committed meanwhile is not
            // stored under the old revision.
            let revision = inspection.inspection.input_revision(document);
            current.fully_declared &= revision.is_some();
            if let Some(revision) = revision
                && let Some((cached, diagnostics)) = previous
                    .as_mut()
                    .and_then(|previous| previous.results.remove(id))
                && cached == revision
            {
                if trace {
                    log::info!(
                        "Diagnostic inspection {id} reused ({} findings)",
                        diagnostics.len()
                    );
                }
                all.extend(diagnostics.iter().cloned());
                current
                    .results
                    .insert(id.to_owned(), (revision, diagnostics));
                continue;
            }
            let inspection_started = Instant::now();
            let mut reporter = InspectionProblemReporter {
                document,
                inspection,
                policy: &policy,
                suppressions: suppressions.as_deref(),
                diagnostics: Vec::new(),
            };
            if let Err(error) = inspection.inspection.inspect(ctx, document, &mut reporter) {
                if ctx.is_cancelled() || is_cancellation(error.as_ref()) {
                    complete = false;
                    break;
                }
                log::warn!("Error getting diagnostics from inspection {id}: {error}");
                continue;
            }
            if let Some(revision) = revision
                && !ctx.is_cancelled()
            {
                current
                    .results
                    .insert(id.to_owned(), (revision, reporter.diagnostics.clone()));
            }
            let elapsed = inspection_started.elapsed();
            measured.push((id.to_owned(), elapsed.as_secs_f64()));
            if elapsed >= INSPECTION_BUDGET
                && lock(&self.diagnostics).slow_reported.insert(id.to_owned())
            {
                log::warn!(
                    "Diagnostic inspection {id} took {elapsed:?} on {}, over the {INSPECTION_BUDGET:?} budget; \
                     set SHOPWARE_LSP_TRACE_DIAGNOSTICS=1 for per-inspection timings",
                    document.uri
                );
            }
            if trace {
                log::info!(
                    "Diagnostic inspection {id} took {elapsed:?} ({} findings)",
                    reporter.diagnostics.len()
                );
            }
            all.extend(reporter.diagnostics);
        }
        if !measured.is_empty() {
            let mut state = lock(&self.diagnostics);
            for (id, seconds) in measured {
                let average = state.costs.entry(id).or_insert(seconds);
                *average += (seconds - *average) * COST_WEIGHT;
            }
        }
        // Only for the open version: a newer one may have been diagnosed
        // meanwhile, and closed documents must not keep results.
        if complete
            && !ctx.is_cancelled()
            && (!current.results.is_empty() || current.fully_declared)
            && self
                .documents
                .get_document(&document.uri)
                .is_some_and(|open| Arc::ptr_eq(&open, document))
        {
            lock(&self.diagnostics)
                .inspection_results
                .insert(document.uri.clone(), current);
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

/// One inspection's uncached result, from [`Server::audit_inspections`].
#[derive(Debug, Clone, PartialEq)]
pub struct InspectionAudit {
    pub id: String,
    /// The declared input revision; `None` when inputs are not declared.
    pub input_revision: Option<u64>,
    pub diagnostics: Vec<protocol::Diagnostic>,
}

impl Server {
    /// Runs every active inspection on the open document `uri` without
    /// reusing results, recording each one's declared input revision. Tests
    /// use it to check input declarations: between two audits, an
    /// inspection whose revision is unchanged must report the same
    /// diagnostics. Empty when `uri` is not open.
    pub fn audit_inspections(&self, ctx: &Context, uri: &str) -> Vec<InspectionAudit> {
        let Some(document) = self.documents.get_document(uri) else {
            return Vec::new();
        };
        let document = &document;
        let policy = self.diagnostic_policy(&document.uri);
        let registry = self.registry();
        let suppressions = document
            .language
            .and_then(|language| registry.suppressions.get(&language))
            .map(|read| read(document));
        let mut result = Vec::new();
        for inspection in registry.inspections.for_language(document.language) {
            if !self.inspection_active(&inspection.definition, &policy)
                || !inspection_has_enabled_rule(inspection, &policy)
            {
                continue;
            }
            let input_revision = inspection.inspection.input_revision(document);
            let mut reporter = InspectionProblemReporter {
                document,
                inspection,
                policy: &policy,
                suppressions: suppressions.as_deref(),
                diagnostics: Vec::new(),
            };
            if inspection
                .inspection
                .inspect(ctx, document, &mut reporter)
                .is_err()
            {
                continue;
            }
            result.push(InspectionAudit {
                id: inspection.definition.id.clone(),
                input_revision,
                diagnostics: reporter.diagnostics,
            });
        }
        result
    }
}

impl Server {
    /// Whether `definition` runs for a document with `policy`: presented
    /// to this client, its configuration domain enabled, and not disabled
    /// by the diagnostics configuration.
    pub(crate) fn inspection_active(
        &self,
        definition: &InspectionDefinition,
        policy: &DiagnosticPolicy,
    ) -> bool {
        self.inspection_presented_to_client(definition)
            && (definition.domain.is_empty() || self.domain_enabled(definition.domain))
            && inspection_enabled_by_policy(policy, &definition.id)
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
    suppressions: Option<&'a dyn Suppressions>,
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
        if self
            .suppressions
            .is_some_and(|suppressions| suppressions.suppresses(problem.range.start(), &problem.id))
        {
            return Ok(());
        }
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
