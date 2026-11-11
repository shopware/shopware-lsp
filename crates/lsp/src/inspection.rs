//! Typed inspections: definitions, problems, bound quick fixes and the
//! registry that validates them.
//!
//! Fixes are bound while a problem is reported (IntelliJ's ProblemsHolder
//! model) and travel with the diagnostic in its `data` envelope, so code
//! actions never rediscover the owning fix through provider fan-out.

use std::collections::{BTreeMap, HashMap};
use std::fmt;
use std::sync::Arc;

use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use swlsp_language::LanguageId;
use swlsp_lsp_protocol as protocol;
use swlsp_rewrite::{ElementHandle, WorkspacePlan};
use swlsp_syntax::{SyntaxElement, TextRange, TextSize};

use crate::context::{BoxError, Context};
use crate::document::TextDocument;

/// Stable diagnostic rule ID; part of the configuration contract.
#[derive(Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord, Serialize, Deserialize, Default)]
#[serde(transparent)]
pub struct DiagnosticId(pub String);

impl DiagnosticId {
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl From<&str> for DiagnosticId {
    fn from(value: &str) -> Self {
        Self(value.to_owned())
    }
}

impl fmt::Display for DiagnosticId {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

/// Stable quick-fix ID, unique within its inspection.
#[derive(Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord, Serialize, Deserialize, Default)]
#[serde(transparent)]
pub struct FixId(pub String);

impl FixId {
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl From<&str> for FixId {
    fn from(value: &str) -> Self {
        Self(value.to_owned())
    }
}

impl fmt::Display for FixId {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

#[derive(Debug, Clone, PartialEq)]
pub struct ProblemDefinition {
    pub id: DiagnosticId,
    pub source: String,
    pub default_severity: protocol::DiagnosticSeverity,
    pub disabled_by_default: bool,
}

/// What to do about one diagnostic code, for people and agents who meet it:
/// the part of a rule its one-line message cannot carry. Declared next to
/// the rule and registered with [`crate::Server::register_rule_help`].
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct RuleHelp {
    pub code: &'static str,
    /// How to resolve a finding.
    pub fix: &'static str,
    /// What the rule deliberately does not report, so a similar-looking
    /// place without a finding is not a gap; empty when there is nothing
    /// to say.
    pub exceptions: &'static str,
    /// A way to tell the rule that a finding is intended, beyond switching
    /// the code off or suppressing the line; empty when there is none.
    pub opt_out: &'static str,
}

/// Which clients an inspection is presented to.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub enum InspectionScope {
    /// Framework diagnostics, shown to every client.
    #[default]
    Framework,
    /// Core-language diagnostics (syntax, types, imports), hidden from
    /// clients with a framework presentation profile because their IDE
    /// already reports them.
    Language,
}

/// Everything the server needs to know about an inspection, declared next
/// to it: no other table routes, gates or presents it.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct InspectionDefinition {
    pub id: String,
    /// What the inspection reports, one or two sentences for users: shown
    /// in the configuration catalog and the generated inspection reference.
    pub description: &'static str,
    /// The configuration domain that must be enabled for the inspection to
    /// run (`swlsp_projectconfig::DOMAIN_CATALOG`); empty for none.
    pub domain: &'static str,
    pub scope: InspectionScope,
    pub languages: Vec<LanguageId>,
    pub problems: Vec<ProblemDefinition>,
}

/// The static declaration of an inspection whose diagnostics share one
/// source and default to warnings, kept next to the analyzer that reports
/// them.
#[derive(Debug, Clone, Copy, Default)]
pub struct InspectionSpec {
    pub id: &'static str,
    /// See [`InspectionDefinition::description`].
    pub description: &'static str,
    /// See [`InspectionDefinition::domain`].
    pub domain: &'static str,
    pub scope: InspectionScope,
    pub languages: &'static [LanguageId],
    pub source: &'static str,
    pub codes: &'static [&'static str],
}

impl InspectionSpec {
    pub fn definition(&self) -> InspectionDefinition {
        InspectionDefinition {
            id: self.id.to_owned(),
            description: self.description,
            domain: self.domain,
            scope: self.scope,
            languages: self.languages.to_vec(),
            problems: self
                .codes
                .iter()
                .map(|code| ProblemDefinition {
                    id: DiagnosticId::from(*code),
                    source: self.source.to_owned(),
                    default_severity: protocol::DiagnosticSeverity::WARNING,
                    disabled_by_default: false,
                })
                .collect(),
        }
    }
}

/// The source-level suppression comments of one document
/// (`@phpstan-ignore`, `@noinspection`, …).
pub trait Suppressions: Send + Sync {
    /// Whether a comment suppresses diagnostic `code` reported at `offset`.
    fn suppresses(&self, offset: TextSize, code: &DiagnosticId) -> bool;
}

/// Reads the suppression comments of a document in one language. The
/// server applies them to every inspection's problems, so analyzers do not
/// need to know about suppression.
pub type SuppressionReader = Arc<dyn Fn(&TextDocument) -> Box<dyn Suppressions> + Send + Sync>;

/// The byte-oriented representation of one diagnostic.
#[derive(Debug, Clone, Default)]
pub struct Problem {
    pub id: DiagnosticId,
    pub range: TextRange,
    /// The anchor element; defaults to the smallest element covering
    /// `range`.
    pub element: Option<SyntaxElement>,
    pub message: String,
    /// Overrides the definition's default severity.
    pub severity: Option<protocol::DiagnosticSeverity>,
    /// Overrides the definition's source.
    pub source: String,
    pub tags: Vec<protocol::DiagnosticTag>,
    pub related_information: Vec<protocol::DiagnosticRelatedInformation>,
    /// Serializable context for quick fixes.
    pub payload: Option<Value>,
    pub fixes: Vec<BoundFix>,
}

impl Problem {
    /// Serializes `payload` into the problem.
    pub fn with_payload<T: Serialize>(mut self, payload: &T) -> Result<Self, BoxError> {
        self.payload = Some(serde_json::to_value(payload)?);
        Ok(self)
    }
}

/// A quick fix bound to one reported problem.
#[derive(Debug, Clone, PartialEq)]
pub struct BoundFix {
    pub id: FixId,
    pub payload: Option<Value>,
    encode_error: Option<String>,
}

/// Binds the fix `id` with a serializable payload . A payload
/// that fails to serialize is reported when the problem is reported.
pub fn bind_fix<T: Serialize>(id: impl Into<FixId>, payload: T) -> BoundFix {
    match serde_json::to_value(payload) {
        Ok(value) => BoundFix {
            id: id.into(),
            payload: Some(value),
            encode_error: None,
        },
        Err(error) => BoundFix {
            id: id.into(),
            payload: None,
            encode_error: Some(error.to_string()),
        },
    }
}

impl BoundFix {
    /// A binding without payload.
    pub fn new(id: impl Into<FixId>) -> Self {
        Self {
            id: id.into(),
            payload: None,
            encode_error: None,
        }
    }

    pub(crate) fn encode_error(&self) -> Option<&str> {
        self.encode_error.as_deref()
    }
}

pub trait ProblemReporter {
    fn report(&mut self, problem: Problem) -> Result<(), BoxError>;
}

pub trait Inspection: Send + Sync {
    fn definition(&self) -> InspectionDefinition;
    fn inspect(
        &self,
        ctx: &Context,
        document: &Arc<TextDocument>,
        reporter: &mut dyn ProblemReporter,
    ) -> Result<(), BoxError>;
    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>>;
    /// The combined revision of everything besides `document` that the
    /// inspection reads for it; it may depend on the document (an inspection
    /// that ignores a file can report `Some(0)` for it). While it and the document are unchanged, the server
    /// reuses the inspection's previous diagnostics instead of running it
    /// again. `None`, the default, means the inputs are not declared: the
    /// inspection runs on every refresh. Analyzer-backed inspections derive
    /// it from `ProblemAnalyzer::input_revision` (`swlsp_ide::diagnostics`).
    fn input_revision(&self, _document: &TextDocument) -> Option<u64> {
        None
    }

    /// Files this inspection may report problems in although they are not
    /// open, for workspace diagnostics (`workspace/diagnostic`): rules about
    /// the project as a whole know them from index data. Empty by default.
    fn workspace_paths(&self, _ctx: &Context) -> Vec<String> {
        Vec::new()
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum FixResolution {
    /// Edits are computed while listing code actions.
    #[default]
    Eager,
    /// Edits are computed in `codeAction/resolve` when the client supports
    /// it.
    Lazy,
}

#[derive(Debug, Clone, PartialEq)]
pub struct FixPresentation {
    pub title: String,
    pub kind: Option<protocol::CodeActionKind>,
    pub preferred: bool,
    pub resolution: FixResolution,
}

/// A quick fix. It is implemented as a structural rewrite
/// ([`RewriteQuickFix`]) or, when editor interaction is genuinely required,
/// as a command ([`CommandQuickFix`]).
pub trait QuickFix: Send + Sync {
    fn id(&self) -> FixId;
    /// `Ok(None)` when the fix is not available for this problem.
    fn present(&self, ctx: &Context, fix: &FixContext)
    -> Result<Option<FixPresentation>, BoxError>;
    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> {
        None
    }
    fn as_command(&self) -> Option<&dyn CommandQuickFix> {
        None
    }
}

/// Compiles a validated structural source rewrite.
pub trait RewriteQuickFix: Send + Sync {
    fn build(&self, ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError>;
}

/// Resolves to an LSP command for workflows that need user input or editor
/// snippet semantics.
pub trait CommandQuickFix: Send + Sync {
    fn build_command(
        &self,
        ctx: &Context,
        fix: &FixContext,
    ) -> Result<Option<protocol::CommandAction>, BoxError>;
}

/// A resolved document: the open snapshot (with its version) or the disk
/// content (without one).
#[derive(Debug, Clone)]
pub struct DocumentSnapshot {
    pub document: Arc<TextDocument>,
    pub version: Option<i32>,
}

pub trait DocumentResolver: Send + Sync {
    fn resolve_document(&self, ctx: &Context, uri: &str) -> Result<DocumentSnapshot, BoxError>;
}

/// Everything a quick fix receives.
#[derive(Clone)]
pub struct FixContext {
    pub document: Arc<TextDocument>,
    pub diagnostic: protocol::Diagnostic,
    pub anchor: ElementHandle,
    pub problem_payload: Option<Value>,
    pub fix_payload: Option<Value>,
    pub documents: Arc<dyn DocumentResolver>,
}

impl FixContext {
    /// The problem payload; absent or `null` payloads decode to `T::default()`.
    pub fn problem_payload<T: DeserializeOwned + Default>(&self) -> Result<T, BoxError> {
        decode_fix_value(self.problem_payload.as_ref(), "problem payload")
    }

    /// The bound fix payload; absent or `null` payloads decode to
    /// `T::default()`.
    pub fn fix_payload<T: DeserializeOwned + Default>(&self) -> Result<T, BoxError> {
        decode_fix_value(self.fix_payload.as_ref(), "fix payload")
    }
}

fn decode_fix_value<T: DeserializeOwned + Default>(
    value: Option<&Value>,
    label: &str,
) -> Result<T, BoxError> {
    match value {
        None | Some(Value::Null) => Ok(T::default()),
        Some(value) => {
            T::deserialize(value).map_err(|error| format!("decode {label}: {error}").into())
        }
    }
}

// ---------------------------------------------------------------------------
// Registry and envelopes

pub(crate) const DIAGNOSTIC_ENVELOPE_KEY: &str = "_shopwareLSP";
pub(crate) const DIAGNOSTIC_ENVELOPE_SCHEMA: i64 = 1;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub(crate) struct BoundFixEnvelope {
    pub id: FixId,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub payload: Option<Value>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct DiagnosticEnvelope {
    pub schema: i64,
    pub inspection: String,
    pub code: DiagnosticId,
    pub uri: String,
    pub document_version: i32,
    pub anchor: ElementHandle,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub payload: Option<Value>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub fixes: Vec<BoundFixEnvelope>,
}

pub(crate) struct RegisteredInspection {
    pub inspection: Arc<dyn Inspection>,
    pub definition: InspectionDefinition,
    pub problems: HashMap<DiagnosticId, ProblemDefinition>,
    pub fixes: HashMap<FixId, Arc<dyn QuickFix>>,
}

#[derive(Clone, Default)]
pub(crate) struct InspectionRegistry {
    pub by_id: BTreeMap<String, Arc<RegisteredInspection>>,
    pub by_code: HashMap<DiagnosticId, Arc<RegisteredInspection>>,
    pub by_language: HashMap<LanguageId, Vec<Arc<RegisteredInspection>>>,
}

impl InspectionRegistry {
    pub fn register(&mut self, inspection: Arc<dyn Inspection>) -> Result<(), String> {
        let definition = inspection.definition();
        if definition.id.is_empty() {
            return Err("inspection ID must not be empty".into());
        }
        if self.by_id.contains_key(&definition.id) {
            return Err(format!(
                "inspection {:?} is registered more than once",
                definition.id
            ));
        }
        if definition.languages.is_empty() {
            return Err(format!("inspection {:?} has no languages", definition.id));
        }
        if definition.description.trim().is_empty() {
            return Err(format!("inspection {:?} has no description", definition.id));
        }
        if !definition.domain.is_empty()
            && !swlsp_projectconfig::DOMAIN_CATALOG
                .iter()
                .any(|domain| domain.id == definition.domain)
        {
            return Err(format!(
                "inspection {:?} belongs to unknown domain {:?}",
                definition.id, definition.domain
            ));
        }
        let mut problems = HashMap::new();
        for problem in &definition.problems {
            if problem.id.0.is_empty() {
                return Err(format!(
                    "inspection {:?} has an empty diagnostic ID",
                    definition.id
                ));
            }
            if problem.source.is_empty() {
                return Err(format!("diagnostic {:?} has no source", problem.id.0));
            }
            if problems.contains_key(&problem.id) {
                return Err(format!(
                    "inspection {:?} declares diagnostic {:?} twice",
                    definition.id, problem.id.0
                ));
            }
            if let Some(owner) = self.by_code.get(&problem.id) {
                return Err(format!(
                    "diagnostic {:?} belongs to both {:?} and {:?}",
                    problem.id.0, owner.definition.id, definition.id
                ));
            }
            problems.insert(problem.id.clone(), problem.clone());
        }
        if problems.is_empty() {
            return Err(format!("inspection {:?} has no diagnostics", definition.id));
        }
        let mut fixes: HashMap<FixId, Arc<dyn QuickFix>> = HashMap::new();
        for fix in inspection.quick_fixes() {
            let id = fix.id();
            if id.0.is_empty() {
                return Err(format!(
                    "inspection {:?} has an invalid quick fix",
                    definition.id
                ));
            }
            if fixes.contains_key(&id) {
                return Err(format!(
                    "inspection {:?} declares quick fix {:?} twice",
                    definition.id, id.0
                ));
            }
            fixes.insert(id, fix);
        }
        let mut languages: Vec<LanguageId> = Vec::with_capacity(definition.languages.len());
        for language in &definition.languages {
            if !languages.contains(language) {
                languages.push(*language);
            }
        }
        // Publish only after the complete definition was validated.
        let registered = Arc::new(RegisteredInspection {
            inspection,
            definition,
            problems,
            fixes,
        });
        self.by_id
            .insert(registered.definition.id.clone(), registered.clone());
        for code in registered.problems.keys() {
            self.by_code.insert(code.clone(), registered.clone());
        }
        for language in languages {
            self.by_language
                .entry(language)
                .or_default()
                .push(registered.clone());
        }
        Ok(())
    }

    pub fn inspection(&self, id: &str) -> Option<&Arc<RegisteredInspection>> {
        self.by_id.get(id)
    }

    pub fn for_language(&self, language: Option<LanguageId>) -> &[Arc<RegisteredInspection>] {
        language
            .and_then(|language| self.by_language.get(&language))
            .map_or(&[], Vec::as_slice)
    }

    pub fn is_empty(&self) -> bool {
        self.by_id.is_empty()
    }
}

pub(crate) fn encode_diagnostic_data(
    envelope: &DiagnosticEnvelope,
) -> Result<Value, serde_json::Error> {
    let metadata = serde_json::to_value(envelope)?;
    let mut data = serde_json::Map::new();
    data.insert(DIAGNOSTIC_ENVELOPE_KEY.to_owned(), metadata);
    Ok(Value::Object(data))
}

pub(crate) fn decode_diagnostic_envelope(
    value: Option<&Value>,
) -> Result<DiagnosticEnvelope, String> {
    let Some(Value::Object(data)) = value else {
        return Err("diagnostic has no Shopware LSP envelope".into());
    };
    let Some(metadata) = data.get(DIAGNOSTIC_ENVELOPE_KEY) else {
        return Err("diagnostic has no Shopware LSP envelope".into());
    };
    let envelope = DiagnosticEnvelope::deserialize(metadata).map_err(|error| error.to_string())?;
    if envelope.schema != DIAGNOSTIC_ENVELOPE_SCHEMA {
        return Err(format!(
            "unsupported diagnostic envelope schema {}",
            envelope.schema
        ));
    }
    Ok(envelope)
}

/// The text of a JSON-decoded diagnostic code (numbers in their JSON
/// form); `None` when the diagnostic has no code.
pub(crate) fn code_string(code: Option<&Value>) -> Option<String> {
    match code {
        None | Some(Value::Null) => None,
        Some(Value::String(value)) => Some(value.clone()),
        Some(other) => Some(other.to_string()),
    }
}

pub(crate) fn supports_language(
    definition: &InspectionDefinition,
    language: Option<LanguageId>,
) -> bool {
    language.is_some_and(|language| definition.languages.contains(&language))
}
