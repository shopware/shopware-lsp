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
the `BoundInspection` adapter in `crates/lsp-inspections/src/inspection.rs`,
which separates three concerns:

```rust
// crates/lsp-inspections/src/inspection.rs
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

Analyzers live in `swlsp-lsp-diagnostics`; the inspections that wire them up
live in `swlsp-lsp-inspections`. That split is why a diagnostic can be unit
tested against a source string without a server.

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
// crates/lsp-diagnostics/src/migration/message_handler.rs
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

## Configuration and gating

Diagnostics as a whole can be disabled by the file's policy. Beyond that, four
independent gates run per inspection, per document, in `collect_diagnostics`:

1. **Client presentation profile.** `inspection_presented_to_client` suppresses
   inspections the host IDE already provides. Under the framework-only profile
   used by the PhpStorm integration, `php.semantic` and
   `symfony.embedded_language` are withheld so generic PHP presentation stays
   with the IDE. See [`phpstorm-integration.md`](phpstorm-integration.md).
2. **Domain.** `inspection_domain(id)` (`crates/lsp/src/configuration.rs`) maps an inspection to a configuration
   domain (`php.semantic` → `php`, `shopware.migration` →
   `shopware.migrations`, `shopware.snippet` → `shopware.snippets`, …), and the
   domain can be switched off wholesale.
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
// crates/lsp-diagnostics/src/symfony/embedded.rs
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

1. **Write the analyzer** in `swlsp-lsp-diagnostics`, implementing
   `ProblemAnalyzer::analyze`. Guard on a missing syntax tree, check
   cancellation in loops, use the language's `query` module, and report
   trimmed ranges.
2. **Declare the inspection** in `swlsp-lsp-inspections`, listing its
   languages and every `ProblemDefinition` it can emit — each with a `source`
   and a `default_severity`. Set `disabled_by_default` for opt-in checks.
3. **Add quick fixes** implementing `RewriteQuickFix` or `CommandQuickFix`, and
   wire them in `bind` so each code offers the right fixes with the right
   payloads.
4. **Register it** from the application composition root
   (`crates/app/src/inspections.rs`) with `Server::register_inspection`.
5. **Map a configuration domain** in `inspection_domain` if the inspection
   belongs to a toggleable feature domain.
6. **Test it** — see below.

## Debugging and testing

### Unit level

Analyzer tests construct a `TextDocument` from a source string and assert on
the returned problems, with no server involved:

```rust
let document = Arc::new(TextDocument::new("file:///src/Handler.php", source, 1));
let problems = analyzer.analyze(&Context::background(), &document).unwrap();
```

`TextDocument::new` parses through the default registry, so the tree, line
index, and parse errors are all populated. Assert on `TextRange` values rather than
line/column where possible — they are stable against reformatting of the
fixture.

### Server level

`crates/lsp/src/tests/diagnostics_tests.rs` covers the scheduling, debounce, and
cache behavior end to end. Registry validation (duplicate codes, missing
sources) fails at composition time, so every test that builds the production
workspace in `swlsp-app` exercises it. The real-world
`crates/app/src/tests/real_world/diagnostics_audit.rs` harness runs the PHP
diagnostics over a real checkout and groups the recurring findings.

### CLI

The `check` command runs the same inspections outside an editor, which is the
fastest way to see what a real workspace produces:

```bash
shopware-lsp --root /path/to/project check src tests
shopware-lsp --root /path/to/project check --fail-on error src/Controller.php
shopware-lsp --root /path/to/project --json check src
```

`codeaction` exercises the fix path, previewing a unified diff unless `-w` is
passed:

```bash
shopware-lsp --root /path/to/project codeaction --kind quickfix src/Controller.php:24:18
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
