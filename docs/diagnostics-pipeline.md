# Diagnostics and Quick-Fix Pipeline

This document describes how Shopware Language Server turns a syntax tree into
published diagnostics, and how a diagnostic carries enough information to
produce a validated quick fix later.

It assumes familiarity with the syntax layer described in
[`parser-architecture.md`](parser-architecture.md): every diagnostic range is a
`TextRange` of byte offsets, and every diagnostic is anchored to a
`SyntaxElement` in an immutable tree.

## Table of contents

- [Design](#design)
- [Pipeline overview](#pipeline-overview)
- [Inspections](#inspections)
- [Problems](#problems)
- [Writing an analyzer](#writing-an-analyzer)
- [Reporting: from Problem to protocol::Diagnostic](#reporting-from-problem-to-protocoldiagnostic)
- [The diagnostic envelope and element anchors](#the-diagnostic-envelope-and-element-anchors)
- [Quick fixes](#quick-fixes)
- [Configuration and gating](#configuration-and-gating)
- [Scheduling, debouncing, and caching](#scheduling-debouncing-and-caching)
- [Parser and language-level diagnostics](#parser-and-language-level-diagnostics)
- [Normalization](#normalization)
- [Registering a new inspection](#registering-a-new-inspection)
- [Debugging and testing](#debugging-and-testing)

## Design

The model is IntelliJ's, not a bag of independent providers:

- An **inspection** is a named unit that declares which languages it applies to
  and which diagnostic codes it can emit. Registration is validated up front,
  and a duplicate or undeclared code is a hard failure at composition time.
- A **problem** is the internal, byte-oriented finding. It carries a
  `TextRange`, optionally the `SyntaxElement` it belongs to, and the fixes
  bound to it *at report time*. Fixes are not rediscovered later by fanning out
  over providers.
- A **quick fix** is looked up from its owning inspection by ID and receives
  the problem's element anchor plus typed payloads.
- Every generated edit is **re-validated** before it reaches the editor. A fix
  that would introduce parser errors, touch a file outside the workspace, or
  apply to a changed document is disabled with a reason rather than shipped.

## Pipeline overview

```
 textDocument/didOpen | didChange | didSave
        │
        ▼
 Server::schedule_diagnostics(uri, version, delay)   debounced, cancels prior job
        │
        ▼
 Server::diagnostics_for_document(ctx, document)     cache: (document Arc, generation)
        │
        ▼
 Server::collect_diagnostics(ctx, document)
        │
        │  for each inspection registered for document.language
        │  ├─ client presentation profile gate
        │  ├─ domain gate            (configuration domain on/off)
        │  ├─ inspection gate        (diagnostics.inspections)
        │  └─ rule gate              (at least one non-off rule)
        │
        ▼
 Inspection::inspect(ctx, document, reporter)
        │      analyzers walk the document tree via <lang>::query
        │      and emit Vec<Problem>
        ▼
 InspectionProblemReporter::report(problem)
        ├─ validate the code is declared
        ├─ drop it when a suppression comment covers it
        ├─ apply configured severity / off
        ├─ validate the range
        ├─ anchor: ElementHandle::new(uri, version, language, element)
        ├─ encode problem + bound-fix payloads
        └─ protocol::Diagnostic { range: UTF-16, code, source, data: envelope }
        │
        ▼
 normalize_diagnostics → sort by position/code/message, drop duplicates
        │
        ▼
 textDocument/publishDiagnostics        (or textDocument/diagnostic pull)


 textDocument/codeAction (client sends the diagnostics back)
        │
        ▼
 decode envelope → resolve anchor against the current tree
        │
        ▼
 QuickFix::present(ctx, FixContext) → title, kind, eager|lazy
        │
        ▼
 RewriteQuickFix::build → WorkspacePlan     (or CommandQuickFix::build_command)
        │
        ▼
 Server::validate_workspace_plan   re-parse, compare error counts, check paths
        │
        ▼
 protocol::WorkspaceEdit    (or a disabled action with a reason)
```

## Inspections

```rust
// crates/lsp/src/inspection.rs
pub struct InspectionDefinition {
    pub id: String,
    pub domain: &'static str,       // configuration domain that gates it
    pub scope: InspectionScope,     // Framework, or Language (core PHP)
    pub languages: Vec<LanguageId>,
    pub problems: Vec<ProblemDefinition>,
}

pub struct ProblemDefinition {
    pub id: DiagnosticId,                              // the LSP diagnostic code
    pub source: String,                                // the LSP diagnostic source
    pub default_severity: protocol::DiagnosticSeverity,
    pub disabled_by_default: bool,
}

pub trait Inspection: Send + Sync {
    fn definition(&self) -> InspectionDefinition;
    fn inspect(&self, ctx: &Context, document: &Arc<TextDocument>,
               reporter: &mut dyn ProblemReporter) -> Result<(), BoxError>;
    fn quick_fixes(&self) -> Vec<Arc<dyn QuickFix>>;
}
```

`Server::register_inspection` panics on an invalid registration — this runs during
workspace composition, so a mistake fails at startup rather than producing
silently missing diagnostics. `InspectionRegistry::register` rejects:

- an empty or duplicate inspection ID;
- an inspection with no languages;
- a domain missing from `swlsp_projectconfig::DOMAIN_CATALOG` (the app's
  `Registrar` additionally requires every production inspection to declare
  one);
- an inspection with no diagnostics;
- a diagnostic with an empty ID or an empty `Source`;
- the same diagnostic code declared twice, or owned by two inspections;
- a quick fix with an empty ID, or declared twice.

The registry is only published after the whole definition validates, so a failed
registration cannot leave partial state behind. It indexes three ways: by
inspection ID, by diagnostic code (the owner lookup used during code-action
resolution), and by language (the dispatch list used per document).

Language dispatch is exact: `collect_diagnostics` iterates
`registry.inspections.for_language(document.language)`. An inspection that wants
to run on both PHP and Twig declares both.

### The BoundInspection adapter

Most inspections are not written as bare `Inspection` implementations. They use
the `BoundInspection` adapter in `crates/ide/src/inspections/inspection.rs`,
which separates three concerns:

```rust
// crates/ide/src/inspections/inspection.rs
pub struct BoundInspection {
    pub definition: InspectionDefinition,
    pub analyzers: Vec<Arc<dyn ProblemAnalyzer>>,  // run in order under one inspection
    pub fixes: Vec<Arc<dyn QuickFix>>,
    pub bind: Option<BindProblems>,                // code + payload → the fixes to offer
}
```

An analyzer implements only the `ProblemAnalyzer` trait
(`fn analyze(&self, ctx: &Context, document: &Arc<TextDocument>) -> Result<Vec<Problem>, BoxError>`)
— it does the tree work and knows nothing about the reporter, the envelope, or
the LSP protocol. `BoundInspection` then re-checks every returned problem against the declared
codes and attaches bound fixes through `bind`.

Analyzers live in the `diagnostics` module of their domain crate
(`crates/ide-<domain>`); the inspections that wire them up live in its
`inspections` module. The contracts both share are `swlsp_ide::diagnostics`
and `swlsp_ide::inspections`. That split is why a diagnostic can be unit
tested against a source string without a server.

### Declared analyzers

An analyzer that forms an inspection on its own, with warnings from one
source and the suggestion quick fix, declares that inspection beside its
implementation:

```rust
// crates/ide-symfony/src/diagnostics/symfony/console.rs
impl DeclaredAnalyzer for ConsoleAnalyzer {
    const INSPECTION: InspectionSpec = InspectionSpec {
        id: "symfony.console",
        domain: "symfony.console",
        scope: InspectionScope::Framework,
        languages: &[LanguageId::Php],
        source: "symfony",
        codes: &[
            MISSING_CONSOLE_ARGUMENT_CODE,
            MISSING_CONSOLE_COMMAND_CODE,
            MISSING_CONSOLE_OPTION_CODE,
        ],
    };
}
```

The composition root only supplies services:
`analyzer_inspection(ConsoleAnalyzer::new(console, php))`. Inspections with
their own quick fixes build an `InspectionDefinition` in
the domain's `inspections` module and state `domain` and `scope` the same way.

### One analyzer behind several inspections

A large analyzer whose rules share per-document work but belong to separate
inspections (the Administration checks, the Shopware migrations) runs once
per document through `SharedAnalysis`; each inspection holds a `CodeGroup`
reporting only its codes. Results are memoized per document snapshot and
input revision (so the analyzer must declare one), cancelled runs are not
kept, and syntax elements are stored by range and kind and resolved again in
the same document.

### Project analyses

A rule about the project as a whole ("this route name is also defined in
another file") implements `ProjectAnalysis` (`swlsp_ide::diagnostics`,
`project.rs`) in two phases, like PHPStan collectors:

- `summarize` derives a summary from index data. `Project` memoizes it per
  `revision()`, the combined revision of the indexes it reads
  (`DataIndexer::revision`, `PhpIndex::revision`), and derives it only when
  a document asks through its `SummaryHandle`, so documents the rule cannot
  apply to never pay for it. Concurrent documents wait for one derivation; a
  cancelled one is not memoized.
- `analyze` checks one document against the summary and anchors problems in
  the document's live tree. The document's own unsaved definitions win over
  what the index stored for it; ranges never come from index data.

`Project<P>` is a `DeclaredAnalyzer`, so registration is
`analyzer(Project::new(RouteRedefinedAnalysis::new(routes)))` and the rule
gets gating, suppression and configuration like any other inspection. Open
documents are re-diagnosed after every committed index change, which picks up
a changed summary. `symfony.route.redefined`
(`services/route_redefined.rs`) is the reference implementation.

## Problems

```rust
// crates/lsp/src/inspection.rs
pub struct Problem {
    pub id: DiagnosticId,
    pub range: TextRange,                          // byte offsets
    pub element: Option<SyntaxElement>,            // anchor; derived from range when None
    pub message: String,
    pub severity: Option<protocol::DiagnosticSeverity>,  // None → the declared default
    pub source: String,                            // "" → the declared source
    pub tags: Vec<protocol::DiagnosticTag>,
    pub related_information: Vec<protocol::DiagnosticRelatedInformation>,
    pub payload: Option<serde_json::Value>,        // carried in the envelope
    pub fixes: Vec<BoundFix>,
}

pub struct BoundFix { pub id: FixId, pub payload: Option<Value>, /* … */ }

pub fn bind_fix<T: Serialize>(id: impl Into<FixId>, payload: T) -> BoundFix
```

Three fields deserve attention.

**`range`** should almost always come from `node.range_trimmed_trivia()`
(`SyntaxNodeExt`), not `node.text_range()`. A parser marker frequently opens before the leading whitespace
is consumed, so the raw range can start on indentation and the editor squiggle
would begin in the margin. Narrowing further — to the name token rather than
the whole declaration — makes the diagnostic point at what the user needs to
change.

**`element`** is the anchor for quick fixes. Set it to the node the fix will
operate on. If left `None`, it is derived with
`root.descendant_for_range(problem.range)`, which is the smallest element fully
containing the range — usually right, but explicit is better when the fix needs
a specific ancestor.

**`payload`** is arbitrary JSON that survives the round trip to the client and
back. Use it to carry the facts the fix needs so the fix does not have to
re-derive them, and decode it with the typed helpers on `FixContext`:

```rust
let payload: ShopwareMigrationPayload = fix.problem_payload()?;
let bound: MyFixPayload = fix.fix_payload()?;
```

## Writing an analyzer

A complete, real analyzer — the Shopware 6.5 `AbstractMessageHandler`
migration:

```rust
// crates/ide-shopware/src/diagnostics/migration/message_handler.rs
pub(super) fn message_handler_migration_problems(
    &self,
    ctx: &Context,
    root: &SyntaxNode,
) -> Vec<Problem> {
    let resolver = scan::resolver(root);
    let mut result = Vec::new();
    for class in scan::classes(root) {
        if ctx.is_cancelled() {
            return result;
        }
        let Some(parent) = phpquery::direct_child(&class, PHP_EXTENDS_CLAUSE)
            .and_then(|extends| phpquery::direct_child(&extends, PHP_NAME))
        else {
            continue;
        };
        if !equal_fold(
            resolver.resolve(node_text(&parent).trim()).trim_matches('\\'),
            ABSTRACT_MESSAGE_HANDLER_CLASS,
        ) {
            continue;
        }
        let handle = php_own_method_for_migration(&class, "handle");
        let invoke = php_own_method_for_migration(&class, "__invoke");
        // Rector can still perform the structural migration when a handler
        // has neither method. Only an existing handle() and __invoke() pair
        // would make renaming handle() ambiguous.
        let safe = handle.is_none() || invoke.is_none();
        result.push(migration_problem(
            MESSAGE_HANDLER_SUBSCRIBER_CODE,
            class_name_range(&class),   // the name's trimmed range, else the class's
            &class,                     // the anchor element
            "Shopware 6.5: migrate AbstractMessageHandler to an attributed Messenger subscriber"
                .to_owned(),
            &ShopwareMigrationPayload {
                rule: "message-handler-subscriber".into(),
                kind: "class".into(),
                safe,
                ..ShopwareMigrationPayload::default()
            },
        ));
    }
    result
}
```

`migration_problem` builds the `Problem` with a warning severity, the
`shopware-rector` source, and the serialized payload.

The patterns to copy:

1. **Check `ctx.is_cancelled()` (or `ctx.check()?`) inside every loop.** Diagnostics run on a cancellable
   background job that is superseded by the next keystroke. An analyzer that
   ignores cancellation burns CPU on results nobody will read.
2. **Use the `query` helpers.** `phpquery::direct_child` and friends — never
   positional child indexing.
3. **Report on the narrowest sensible range**, and use
   `range_trimmed_trivia()`.
4. **Anchor on the node the fix edits** (`&class` here), which may be wider
   than the reported range.
5. **Put the fix's decision inputs in the payload** (`safe` here) so the fix
   does not re-analyze the tree.
6. **Guard on missing input.** Analyzers routinely start with
   `let Some(root) = document.root() else { return Ok(Vec::new()) };` — a file
   with an unregistered extension has no tree.

## Reporting: from Problem to protocol::Diagnostic

`InspectionProblemReporter::report` (`crates/lsp/src/diagnostics.rs`) is the
single funnel. In order, it:

1. **Rejects undeclared codes.** Reporting a code the inspection did not
   declare is an error, not a warning.
2. **Applies configuration.** If the effective policy sets the rule to `off`,
   the problem is dropped. If the rule is unconfigured and the declaration is
   `disabled_by_default`, it is dropped.
3. **Validates the range** against the document length — out-of-bounds ranges
   are an error.
4. **Builds the anchor** with `ElementHandle::new`, deriving the element
   from the range when the problem did not supply one.
5. **Encodes payloads** for the problem and every bound fix, verifying each
   bound fix ID is actually registered on this inspection.
6. **Resolves severity**: the problem's explicit severity, else the
   declaration's default, else — overriding both — the configured severity.
7. **Resolves source**: the problem's, else the declaration's.
8. **Converts the range to LSP coordinates** with
   `protocol_range(document.line_index(), problem.range)`, i.e.
   `LineIndex::position_utf16` on both ends.

The result:

```rust
protocol::Diagnostic {
    range: protocol_range(self.document.line_index(), problem.range),
    severity: Some(severity),
    code: Some(Value::String(problem.id.0)),
    source,
    message: problem.message,
    tags: problem.tags,
    related_information: problem.related_information,
    data: Some(data),   // the envelope, under "_shopwareLSP"
}
```

## The diagnostic envelope and element anchors

Everything a quick fix needs later travels in `Diagnostic.data`, under the
single key `_shopwareLSP`:

```rust
// crates/lsp/src/inspection.rs
pub(crate) const DIAGNOSTIC_ENVELOPE_KEY: &str = "_shopwareLSP";
pub(crate) const DIAGNOSTIC_ENVELOPE_SCHEMA: i64 = 1;

#[serde(rename_all = "camelCase")]
pub(crate) struct DiagnosticEnvelope {
    pub schema: i64,
    pub inspection: String,
    pub code: DiagnosticId,
    pub uri: String,
    pub document_version: i32,
    pub anchor: ElementHandle,
    pub payload: Option<Value>,          // omitted when None
    pub fixes: Vec<BoundFixEnvelope>,    // omitted when empty
}
```

The schema version is checked on decode, so a client replaying a diagnostic
from an older server version is rejected instead of misinterpreted.

### ElementHandle

A CST node pointer cannot be serialized, and by the time the client asks for
code actions the tree may have been replaced. `swlsp_rewrite::ElementHandle` is
the serializable stand-in:

```rust
// crates/rewrite/src/handle.rs
#[serde(rename_all = "camelCase")]
pub struct ElementHandle {
    pub uri: String,
    pub version: i32,
    pub language: LanguageId,
    pub kind: Kind,
    pub range: TextRange,
    pub text_hash: u64,   // FNV-1a of the element text, serialized as a string
}
```

`resolve` re-finds the element in a current tree and refuses anything
suspicious:

```rust
pub fn resolve(&self, uri: &str, version: i32, language: LanguageId, tree: &Tree)
    -> Result<SyntaxElement, HandleError>
{
    if self.uri != uri || self.version != version || self.language != language {
        return Err(HandleError::Stale);
    }
    let root = tree.root();
    if self.range.end() > root.text_range().end() {
        return Err(HandleError::Missing);
    }
    let mut candidate = Some(root.covering_element(self.range));
    while let Some(element) = candidate {
        if element.kind() == self.kind
            && element.text_range() == self.range
            && hash_text(tree.text(self.range)) == self.text_hash
        {
            return Ok(element);
        }
        candidate = element.parent().map(SyntaxElement::Node);
    }
    Err(HandleError::StaleElement { range: self.range, kind: self.kind.name().into_owned() })
}
```

Four things must agree — URI, document version, language, and then kind, exact
range, and text hash. The walk up the ancestor chain from
`covering_element` handles the case where the anchored element is not the
innermost element at that range. A mismatch is a stale `HandleError`, which the code
action layer turns into a disabled action reading "The document changed;
request code actions again" rather than an edit against the wrong text.

The `text_hash` is deliberately serialized as a JSON **string**: JavaScript
clients cannot represent every `u64` in a JSON number without rounding.

## Quick fixes

```rust
// crates/lsp/src/inspection.rs
pub trait QuickFix: Send + Sync {
    fn id(&self) -> FixId;
    /// Ok(None) when the fix is not available for this problem.
    fn present(&self, ctx: &Context, fix: &FixContext)
        -> Result<Option<FixPresentation>, BoxError>;
    fn as_rewrite(&self) -> Option<&dyn RewriteQuickFix> { None }
    fn as_command(&self) -> Option<&dyn CommandQuickFix> { None }
}

pub struct FixPresentation {
    pub title: String,
    pub kind: Option<protocol::CodeActionKind>,
    pub preferred: bool,
    pub resolution: FixResolution,   // Eager | Lazy
}

pub struct FixContext {
    pub document: Arc<TextDocument>,
    pub diagnostic: protocol::Diagnostic,
    pub anchor: ElementHandle,
    pub problem_payload: Option<Value>,
    pub fix_payload: Option<Value>,
    pub documents: Arc<dyn DocumentResolver>,
}
```

`present` is cheap and always runs — it decides the title, the kind, whether
the fix applies at all (`None` when it does not), and whether the edit is computed now or on
`codeAction/resolve`.

A `QuickFix` exposes one of two implementation traits through `as_rewrite` or
`as_command`:

```rust
/// A structural source rewrite.
pub trait RewriteQuickFix: Send + Sync {
    fn build(&self, ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError>;
}

/// An LSP command, for workflows needing user input or editor snippet semantics.
pub trait CommandQuickFix: Send + Sync {
    fn build_command(&self, ctx: &Context, fix: &FixContext)
        -> Result<Option<protocol::CommandAction>, BoxError>;
}
```

They are kept separate so a command-backed fix can participate in the same
exact diagnostic binding without manufacturing an empty edit plan.

### Resolution

`FixResolution::Lazy` defers `build` to `codeAction/resolve`, which matters when computing
the edit is expensive and the user may never invoke it. The server honors it
only when the client advertises resolve support:

```rust
if presentation.resolution == FixResolution::Eager || !resolve_support {
    self.populate_inspection_edit(ctx, &mut action, fix.as_ref(), &fix_context);
}
```

### Fix all in file

When a document has at least two problems of one code offering the same
rewrite fix, `inspection_code_actions` adds a *Fix all N '<code>' problems in
this file* action (resolved lazily when the client supports it). Resolving
it (`fix_all.rs`) applies the fix to one problem at a time on an in-memory
copy of the document and re-runs the inspection between steps, instead of
merging independently built edits: fixes share side effects (each occurrence
of a replaced type adding the same import, or seeing the others keep an old
import in use) that only sequential application gets right. A step that does
not reduce the number of matching problems ends the run. Fixes editing other
documents or creating files are not applied. The result is one validated
edit of the changed region.

### Plan validation

`Server::validate_workspace_plan` runs before any plan becomes a
`WorkspaceEdit`. For each planned document it re-resolves the current document,
rejects a source or version mismatch as stale, applies the edit, and re-parses
the result:

```rust
let updated = planned.apply()?;
if let Some((_, parse)) = parse_path(&planned.uri, &updated)
    && parse.errors.len() > current.document.parse_errors().len()
{
    return Err(format!(
        "rewrite introduces {} parser errors",
        parse.errors.len() - current.document.parse_errors().len()
    ).into());
}
```

The comparison is against the document's *existing* error count, not zero — a
fix must be allowed to apply inside an already-broken file, but it must not
make the syntax worse. Created files must be inside the workspace root, must
not already exist, and must parse cleanly. Deleted files must exist as regular
files and must match the recorded source and version.

When any of this fails the action is returned disabled with a human-readable
reason (`"The generated edit is no longer valid"`, `"The document changed;
request code actions again"`, …). The pipeline never ships an unvalidated edit.

## Suppression comments

Suppression is applied by the server, not by analyzers. Composition
registers one `SuppressionReader` per language
(`Server::register_suppressions`); `collect_diagnostics` reads a document's
comments once and `InspectionProblemReporter` drops every problem a comment
covers, whatever inspection reported it. PHP uses
`swlsp_lsp_phpanalysis::suppression_reader`, which recognizes PHPStan's
`@phpstan-ignore` forms and PhpStorm's `@noinspection`
(`swlsp-php-suppression`). Twig, YAML, XML, JavaScript and SCSS use
`swlsp_lsp::comment_suppression_reader` with their comment kinds: it finds
`@noinspection` in the syntax tree's comments and applies it to the
comment's own line when code shares it, or to the next line with code.
Analyzers therefore never parse suppression comments; the PHP semantic analyzer still checks them before expensive
per-reference work, which only saves time.

## Configuration and gating

Diagnostics as a whole can be disabled by the file's policy. Beyond that, four
independent gates run per inspection, per document, in `collect_diagnostics`;
the first three (`Server::inspection_active`) are re-checked before a quick fix
is offered:

1. **Client presentation profile.** Under the framework-only profile used by
   the PhpStorm integration, inspections declared with
   `InspectionScope::Language` (`php.semantic`, `php.imports`,
   `symfony.embedded_language`) are withheld so generic PHP presentation stays
   with the IDE. See [`phpstorm-integration.md`](phpstorm-integration.md).
2. **Domain.** The definition's `domain` (`php`, `shopware.migrations`,
   `symfony.services`, …) can be switched off wholesale.
3. **Inspection.** `diagnostics.inspections[<id>]` in project configuration can
   disable one inspection; an unconfigured inspection is enabled.
4. **Rules.** `inspection_has_enabled_rule` skips an inspection entirely when
   every one of its codes is either configured `off` or unconfigured and
   `disabled_by_default`. This is the gate that keeps a 21-code inspection from
   running when the user turned all of it off.

Severity resolution and rule enablement both come from
`swlsp_projectconfig::DiagnosticPolicy`, which is computed per file path:

```rust
let policy = self.diagnostic_policy(&document.uri);
```

`swlsp-projectconfig` resolves project configuration, editor settings, and
path-scoped overrides into that policy, so `.config/shopware/lsp.yaml` can turn
a rule to `off`, `hint`, `information`, `warning`, or `error` for a glob such as
`custom/plugins/*/src/Generated/**`. Nested configuration files may only
contain a `diagnostics` section. See
[`reference.md#project-configuration`](reference.md#project-configuration).

The precedence, from the reporter's perspective: a configured rule severity
wins over the problem's explicit severity, which wins over the declared
default. A configured `off` drops the problem entirely.

## Scheduling, debouncing, and caching

The server selects diagnostic delivery at initialization. Clients advertising
`textDocument.diagnostic` receive `diagnosticProvider` and use pull reports;
other clients receive `textDocument/publishDiagnostics` notifications. Pull
clients never receive push notifications, including close-document clears.
When a pull client supports `workspace.diagnostics.refreshSupport`, scheduled
updates request `workspace/diagnostic/refresh` so index, dependency, and
configuration changes prompt a new pull. Without refresh support, those changes
are reflected on the client's next diagnostic request.

`schedule_diagnostics(uri, version, delay)` is the only scheduling entry point. It:

- creates a cancellable job under the server lifecycle context;
- bumps a per-URI **generation** counter and invalidates that URI's cache
  entry;
- cancels any previously scheduled job for the same URI;
- starts a background job that waits `delay` and then publishes that document's
  diagnostics, or aborts on cancellation.

Interactive edits schedule with `DIAGNOSTICS_DEBOUNCE` (150 ms). Explicit
`publish_diagnostics` calls use no delay.

Publishing re-checks the document version before analyzing and again,
under the `diagnostics_publish` lock, before notifying — so a superseded job cannot
publish stale results over a newer run, and a canceled analysis cannot write
after the close-document clear.

The cache is keyed on both the document pointer and the generation:

```rust
// crates/lsp/src/diagnostics.rs
struct CacheEntry {
    document: Arc<TextDocument>,
    generation: u64,
    diagnostics: Vec<protocol::Diagnostic>,
}
```

Because `TextDocument` snapshots are immutable, `Arc::ptr_eq` identity is a
valid freshness test. `diagnostics_for_document` serves both the push path and the pull
path (`textDocument/diagnostic`), so repeated pulls reuse the analysis. Results
are cloned in and out of the cache. A canceled run is never cached.

Pull responses always return a full report with `"kind": "full"` and an `items`
array, including `"items": []` for empty or unavailable documents and cached
empty results. The server does not currently return unchanged reports.

`refresh_open_document_diagnostics(matches)` re-analyzes open documents affected by a
change elsewhere — the "open dependency changed" case — using the debounce so a
workspace overlay does not schedule one job per keystroke. It is skipped in CLI
mode.

### Reusing results across index refreshes

A committed index change does not re-diagnose a document whose result
cannot have changed. Each inspection may report an input revision
(`Inspection::input_revision`): the combined revision of everything it reads
besides the document. The server keeps each inspection's last diagnostics
per open document, and

- `collect_diagnostics` reuses them while the inspection's input revision,
  the document snapshot and the document's `DiagnosticPolicy` are unchanged;
- `refresh_after_index_change` (the scanner's update callback) does not
  schedule a document at all when every inspection on it declares inputs
  and none of their revisions changed.

Analyzers declare their inputs beside the fields they read, through
`ProblemAnalyzer::input_revision`; the adapters sum their analyzers' values
(`None` if any analyzer does not declare):

```rust
// crates/ide-symfony/src/diagnostics/symfony/console.rs
impl ProblemAnalyzer for ConsoleAnalyzer {
    fn input_revision(&self) -> Option<u64> {
        Some(crate::inputs(&self.index) + crate::inputs(&self.php_index))
    }
    // ...
}
// Document-only analyzers return Some(0).
```

Index types implement `swlsp_indexer::Revisioned`, as do `Option` and `Arc`
of them (an absent index is revision 0). Revisions only grow, so a sum is a
valid combined revision. Index revisions come from `DataIndexer::revision()`
plus any in-memory catalog the index publishes (compiled container and
route catalogs). `PhpIndex`'s includes `analysis_revision()`, which adds the
revisions of the registered type extensions (`Extension::revision`), so PHP
analyses follow the indexes extensions answer from (the service index for
`$container->get()`). Repositories advance their revision only when a write
changes rows, so a file without data for an index (every indexer sees every
file) leaves it, and the inspections declaring it, alone.

The declaration must be complete: an index, configuration or file read
from disk left out leaves stale diagnostics on open documents after it
changes. `crates/app/src/tests/inspection_inputs_test.rs` audits the
declarations with `Server::audit_inspections`: it edits one indexed file at
a time and fails when an inspection reports something different although
its declared revision did not change, naming the inspection and the file.
Extend its fixture when declaring a new input, with an edit that advances
that input alone.

The revision may depend on the document. An analyzer that ignores a
document reports `Some(0)` for it: the Administration analyzers outside
`Resources/app/administration`, the theme analyzer for templates without
`sw_icon`, versioning for templates that extend nothing. Files read from
disk outside the scanner are tracked by polling: the Assetic catalog and the
theme icon provider re-check their files at most every 500 ms when asked for
their revision and advance it when a fingerprint changes; versioning stamps
the `composer.json` files its version lookup walks for the document, and
entity snapshots stamp their plugin's snapshot directory. Indexes whose
readiness can flip back (the workspace symbol catalog behind SCSS variables
and entity snapshots) encode it as `2 × revision + ready`, which stays
distinct for every pair. Every production inspection declares its inputs;
the audit test fails on an active inspection without one. Configuration
reloads clear every reused result; closing a document drops its results.

### Time budget

Inspections run cheapest first, ordered by a moving average of their run
time. In push mode a document's diagnostics do not wait for slow
inspections: before starting one whose average exceeds `INSPECTION_BUDGET`
(200 ms), or once the document's run has used up that budget, the results so
far are published, then the complete set. Inspections still to run keep
their previous diagnostics in that partial set when the document snapshot is
unchanged (an index refresh), so nothing flickers; after an edit, their
results appear when they finish. An inspection exceeding the budget is
logged once per session as a warning naming it. Pull clients receive the
complete set.

### Workspace diagnostics

Project rules know problems in files nobody opened. The server advertises
`workspaceDiagnostics` and answers `workspace/diagnostic`
(`workspace_diagnostics.rs`): inspections name the files they may report on
(`Inspection::workspace_paths`; for project analyses
`ProjectAnalysis::affected_paths` from their summary), which are loaded from
disk and diagnosed by the whole pipeline, so the workspace view shows what
opening the file would. Open documents are left to document pulls. The
report is computed once per index and configuration state and served with
result IDs: files whose problems did not change are reported `unchanged`,
and files the client still lists that no longer have problems get an empty
report.

## Parser and language-level diagnostics

Syntax errors are not a special path. Because parsing is total, the tree always
exists, and `ParseError` values ride along on the document:

```rust
// crates/lsp/src/document.rs
pub struct TextDocument {
    // ...
    parse_errors: Vec<ParseError>,   // read through parse_errors()
}
```

The PHP semantic provider turns them into ordinary problems alongside its
semantic findings:

```rust
// crates/lsp-phpsemantic/src/diagnostics.rs
fn analyze(mut self) -> Vec<Problem> {
    self.add_parse_errors();
    self.add_language_level_problems();
    self.add_reference_problems();
    self.add_semantic_issues();
    self.add_duplicate_declarations();
    self.diagnostics
}

fn add_parse_errors(&mut self) {
    for parse_error in self.document.parse_errors() {
        if self.suppressed(parse_error.range, "php.parse") {
            continue;
        }
        self.diagnostics.push(problem(
            "php.parse",
            parse_error.range,
            DiagnosticSeverity::ERROR,
            parse_error.message(),
        ));
    }
}
```

Two consequences worth internalizing:

- A syntax error never suppresses semantic analysis. The tree still has `ERROR`
  nodes in place of the broken region, and every other construct in the file is
  still analyzed. This is why completion and hover keep working while you type.
- Parse-error diagnostics obey the same suppression comments and severity
  configuration as everything else (`php.parse`, `php.version`, …).

Language-level checks work the same way: `languagelevel::detect(root)` walks the
tree for version-gated syntax and reports `php.version` when the configured
PHP version does not support it.

### Embedded languages

String literals holding another language are diagnosed by parsing them on
demand. `embedded_language_strings` (`swlsp-php`) recognizes the signature that makes a
literal JSON, CSS, or XPath, and each sub-language runs its own frontend over
just that literal:

```rust
// crates/ide-symfony/src/diagnostics/symfony/embedded.rs
for literal in embedded_language_strings(
    php_index, &path, i64::from(document.version), document.source(), &root,
) {
    if ctx.is_cancelled() {
        return Ok(Vec::new());
    }
    result.extend(match literal.language {
        EmbeddedLanguage::Json => embedded_json_diagnostics(&literal.string),
        EmbeddedLanguage::Css => embedded_css_diagnostics(&literal.string),
        EmbeddedLanguage::XPath => embedded_xpath_diagnostics(&literal.string),
    });
}
```

The whole family — every supported injection signature — is validated in one
PHP CST scan and one semantic analysis, and ranges are translated back to
absolute offsets in the host file. Vue takes the other approach and embeds its
sections directly in one tree; see
[the embedding section](parser-architecture.md#embedded-and-multi-language-trees).

## Normalization

Independent inspections legitimately overlap. `normalize_diagnostics` gives the
editor a stable, deduplicated list:

- **Sort** (stable) by start line, start character, end line, end character,
  code, then message.
- **Deduplicate** adjacent entries that match on range, code, source, and
  message.

Deterministic ordering matters beyond aesthetics: CLI `check` output, snapshot
tests, and editor diagnostic ordering all depend on it.

Diagnostic collection can be traced with an environment variable:

```bash
SHOPWARE_LSP_TRACE_DIAGNOSTICS=1 shopware-lsp
```

This logs per-inspection duration and finding counts, plus the total request
time and normalization time — the first place to look when a document feels
slow.

## Registering a new inspection

1. **Write the analyzer** in `crates/ide-<domain>/src/diagnostics`, implementing
   `ProblemAnalyzer::analyze`. Guard on a missing syntax tree, check
   cancellation in loops, use the language's `query` module, and report
   trimmed ranges.
2. **Declare the inspection** with its ID, a description for users,
   configuration domain, scope, languages and every code it can emit. For a single analyzer with warnings
   and suggestion fixes, implement `DeclaredAnalyzer` beside it. Otherwise
   build an `InspectionDefinition` in `crates/ide-<domain>/src/inspections` with a
   `ProblemDefinition` per code — each with a `source` and a
   `default_severity`; set `disabled_by_default` for opt-in checks.
3. **Add quick fixes** implementing `RewriteQuickFix` or `CommandQuickFix`, and
   wire them in `bind` so each code offers the right fixes with the right
   payloads.
4. **Register it** in its domain crate
   (`crates/ide-<domain>/src/inspections/mod.rs`):
   `analyzer_inspection(Analyzer::new(…))` or the inspection's constructor. Composition fails on a missing
   description or a missing or unknown domain. Regenerate
   [`inspections.md`](inspections.md) with
   `SHOPWARE_LSP_UPDATE_REFERENCE=1 cargo test -p swlsp-app inspection_reference`;
   the test fails while it is out of date.
5. **Declare its inputs** in `ProblemAnalyzer::input_revision` when the
   analyzer reads only indexes with a revision (see
   [reusing results](#reusing-results-across-index-refreshes)).
6. **Test it** — see below.

## Debugging and testing

### Unit level

Analyzer and inspection tests run against a source string with no server
involved. Write the expected problems into the source as markup
(`swlsp_lsp::fixture`): `{|code:text|}` marks a problem with that code over
`text`, markers may nest, and `{|code:|}` marks an empty range.

```rust
// crates/ide-<domain>/src/diagnostics: one analyzer
let (_, problems) = check_problems(
    &AssetAnalyzer::new(Some(index), None),
    "file:///project/templates/page.html.twig",
    "{{ asset('{|symfony.asset.missing:build/ap.css|}') }}",
);
assert!(problems[0].message.contains("build/ap.css"));

// crates/ide-<domain>/src/inspections: a whole inspection and one of its fixes
check_diagnostics(inspection.as_ref(), uri, before);
check_fix(inspection.as_ref(), uri, before, FIX_ID, after);
```

The helpers strip the markup, run the analyzer or inspection on the plain
source, and compare the reported problems rendered back into markup with the
fixture. A failure prints the source with the differing lines marked `-`
(fixture) and `+` (reported), so a wrong range reads as moved markers rather
than offsets. Both return the problems in markup order for assertions on
messages, payloads, or tags. `check_fix` applies the fix to the one marked
problem that has it bound, ignoring other problems, so a test can reuse the
fixture of `check_diagnostics`; it returns the fix's presentation.

Tests that need the raw problems can still call
`analyzer.analyze(&Context::background(), &document)` on a
`TextDocument::new(uri, source, 1)`, which parses through the default
registry, so the tree, line index, and parse errors are all populated.

### Server level

`crates/lsp/src/tests/diagnostics_tests.rs` covers the scheduling, debounce, and
cache behavior end to end. Registry validation (duplicate codes, missing
sources) fails at composition time, so every test that builds the production
workspace in `swlsp-app` exercises it. The real-world
`crates/app/src/tests/real_world/diagnostics_audit.rs` harness runs the PHP
diagnostics over a real checkout and groups the recurring findings.

### Performance

`crates/bench/benches/diagnostics.rs` (CodSpeed, `cargo bench -p swlsp-bench
--bench diagnostics`) runs every registered inspection on a PHP controller
and a Twig template of an indexed plugin through the production session the
CLI uses, editing the document each iteration so nothing is served from a
cache. A regression there usually means an inspection rebuilt index-wide
data per document; `SHOPWARE_LSP_TRACE_DIAGNOSTICS=1` with `check` shows
which one.

### CLI

The `check` command runs the same inspections outside an editor, which is the
fastest way to see what a real workspace produces:

```bash
shopware-lsp --root /path/to/project check src tests
shopware-lsp --root /path/to/project check --fail-on error src/Controller.php
shopware-lsp --root /path/to/project --json check src
```

`lsp codeaction` exercises the fix path, previewing a unified diff unless `-w` is
passed:

```bash
shopware-lsp --root /path/to/project lsp codeaction --kind quickfix src/Controller.php:24:18
```

### Tracing

```bash
SHOPWARE_LSP_TRACE_DIAGNOSTICS=1 shopware-lsp
```

## Further reading

- [`architecture.md`](architecture.md) — the system this pipeline sits in
- [`parser-architecture.md`](parser-architecture.md) — trees, ranges, and the
  query packages every analyzer uses
- [`refactoring-engine.md`](refactoring-engine.md) — how a bound quick fix
  compiles and validates its edits
- [`lsp-server.md`](lsp-server.md) — dispatch, capabilities, and the client
  command filtering that affects which fixes are offered
- [`php-semantic-engine.md`](php-semantic-engine.md) — the semantic layer
  behind `php.semantic` diagnostics
- [`phpstorm-integration.md`](phpstorm-integration.md) — the presentation
  profile that gates which inspections reach the client
- [`reference.md#project-configuration`](reference.md#project-configuration) —
  the configuration keys behind `DiagnosticPolicy`
- [`shopware-rector-parity.md`](shopware-rector-parity.md) — the migration
  inspections and their Rector counterparts
