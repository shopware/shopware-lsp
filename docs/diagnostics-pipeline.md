# Diagnostics and Quick-Fix Pipeline

This document describes how Shopware Language Server turns a syntax tree into
published diagnostics, and how a diagnostic carries enough information to
produce a validated quick fix later.

It assumes familiarity with the syntax layer described in
[`parser-architecture.md`](parser-architecture.md): every diagnostic range is a
`cst.TextRange` of byte offsets, and every diagnostic is anchored to a
`cst.Element` in an immutable tree.

## Table of contents

- [Design](#design)
- [Pipeline overview](#pipeline-overview)
- [Inspections](#inspections)
- [Problems](#problems)
- [Writing an analyzer](#writing-an-analyzer)
- [Reporting: from Problem to protocol.Diagnostic](#reporting-from-problem-to-protocoldiagnostic)
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
  `cst.TextRange`, optionally the `cst.Element` it belongs to, and the fixes
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
 Server.scheduleDiagnostics(uri, version, delay)     debounced, cancels prior job
        │
        ▼
 Server.diagnosticsForDocument(ctx, document)        cache: (document ptr, generation)
        │
        ▼
 Server.collectDiagnostics(ctx, document)
        │
        │  for each inspection registered for document.SyntaxLanguage
        │  ├─ client presentation profile gate
        │  ├─ domain gate            (configuration domain on/off)
        │  ├─ inspection gate        (diagnostics.inspections)
        │  └─ rule gate              (at least one non-off rule)
        │
        ▼
 Inspection.Inspect(ctx, document, reporter)
        │      analyzers walk document.SyntaxTree via <lang>/query
        │      and emit []lsp.Problem
        ▼
 inspectionProblemReporter.Report(problem)
        ├─ validate the code is declared
        ├─ apply configured severity / off
        ├─ validate the range
        ├─ anchor: rewrite.NewElementHandle(uri, version, language, element)
        ├─ encode problem + bound-fix payloads
        └─ protocol.Diagnostic{ Range: UTF-16, Code, Source, Data: envelope }
        │
        ▼
 normalizeDiagnostics  → sort by position/code/message, drop duplicates
        │
        ▼
 textDocument/publishDiagnostics        (or textDocument/diagnostic pull)


 textDocument/codeAction (client sends the diagnostics back)
        │
        ▼
 decode envelope → resolve anchor against the current tree
        │
        ▼
 QuickFix.Present(ctx, FixContext) → title, kind, eager|lazy
        │
        ▼
 RewriteQuickFix.Build → rewrite.WorkspacePlan     (or CommandQuickFix.BuildCommand)
        │
        ▼
 Server.validateWorkspacePlan   re-parse, compare error counts, check paths
        │
        ▼
 protocol.WorkspaceEdit    (or a disabled action with a reason)
```

## Inspections

```go
// internal/lsp/inspection_types.go
type InspectionDefinition struct {
    ID        string
    Languages []language.ID
    Problems  []ProblemDefinition
}

type ProblemDefinition struct {
    ID                DiagnosticID                // the LSP diagnostic code
    Source            string                      // the LSP diagnostic source
    DefaultSeverity   protocol.DiagnosticSeverity
    DisabledByDefault bool
}

type Inspection interface {
    Definition() InspectionDefinition
    Inspect(context.Context, *TextDocument, ProblemReporter) error
    QuickFixes() []QuickFix
}
```

`Server.RegisterInspection` panics on an invalid registration — this runs during
workspace composition, so a mistake fails at startup rather than producing
silently missing diagnostics. `inspectionRegistry.register` rejects:

- an empty or duplicate inspection ID;
- an inspection with no languages, or an empty language ID;
- an inspection with no diagnostics;
- a diagnostic with an empty ID or an empty `Source`;
- the same diagnostic code declared twice, or owned by two inspections;
- a quick fix with an empty ID, or declared twice.

The registry is only published after the whole definition validates, so a failed
registration cannot leave partial state behind. It indexes three ways: by
inspection ID, by diagnostic code (the owner lookup used during code-action
resolution), and by language (the dispatch list used per document).

Language dispatch is exact: `collectDiagnostics` iterates
`s.inspections.inspections(document.SyntaxLanguage)`. An inspection that wants
to run on both PHP and Twig declares both.

### The boundInspection adapter

Most inspections are not written as bare `Inspection` implementations. They use
the `boundInspection` adapter in `internal/lsp/inspections`, which separates
three concerns:

```go
// internal/lsp/inspections/inspection.go
type boundInspection struct {
    definition lsp.InspectionDefinition
    analyzer   ProblemAnalyzer      // one analyzer, or…
    analyzers  []ProblemAnalyzer    // …several composed under one inspection
    fixes      []lsp.QuickFix
    bind       bindProblems         // code + payload → the fixes to offer
}
```

An analyzer implements only
`Analyze(ctx, *lsp.TextDocument) ([]lsp.Problem, error)` — it does the tree
work and knows nothing about the reporter, the envelope, or the LSP protocol.
`boundInspection` then re-checks every returned problem against the declared
codes and attaches bound fixes through `bind`.

Analyzers live in `internal/lsp/diagnostics`; the inspections that wire them up
live in `internal/lsp/inspections`. That split is why a diagnostic can be unit
tested against a source string without a server.

## Problems

```go
type Problem struct {
    ID                 DiagnosticID
    Range              cst.TextRange   // byte offsets
    Element            cst.Element     // anchor; derived from Range when nil
    Message            string
    Severity           protocol.DiagnosticSeverity  // 0 → the declared default
    Source             string                        // "" → the declared source
    Tags               []protocol.DiagnosticTag
    RelatedInformation []protocol.DiagnosticRelatedInformation
    Payload            any             // JSON-encoded into the envelope
    Fixes              []BoundFix
}

type BoundFix struct {
    ID      FixID
    Payload any
}

func BindFix[T any](id FixID, payload T) BoundFix
```

Three fields deserve attention.

**`Range`** should almost always come from `Node.RangeTrimmedTrivia()`, not
`Node.Range()`. A parser marker frequently opens before the leading whitespace
is consumed, so the raw range can start on indentation and the editor squiggle
would begin in the margin. Narrowing further — to the name token rather than
the whole declaration — makes the diagnostic point at what the user needs to
change.

**`Element`** is the anchor for quick fixes. Set it to the node the fix will
operate on. If left nil, the reporter derives it with
`root.DescendantForRange(problem.Range)`, which is the smallest element fully
containing the range — usually right, but explicit is better when the fix needs
a specific ancestor.

**`Payload`** is arbitrary JSON that survives the round trip to the client and
back. Use it to carry the facts the fix needs so the fix does not have to
re-derive them, and decode it with the typed helper:

```go
payload, err := lsp.DecodeProblemPayload[ShopwareMigrationPayload](fixContext)
boundPayload, err := lsp.DecodeBoundFixPayload[MyFixPayload](fixContext)
```

## Writing an analyzer

A complete, real analyzer — the Shopware 6.5 `AbstractMessageHandler`
migration:

```go
// internal/lsp/diagnostics/shopware_migration_message_handler.go
func (p *ShopwareMigrationAnalyzer) messageHandlerMigrationProblems(
    ctx context.Context,
    root *phpsyntax.Node,
) []lsp.Problem {
    resolver := php.NewNameResolver(root)
    var result []lsp.Problem
    for _, class := range phpquery.Classes(root) {
        if ctx.Err() != nil {
            return result
        }
        extends := phpquery.DirectChild(class, phpsyntax.PhpExtendsClause)
        parent := phpquery.DirectChild(extends, phpsyntax.PhpName)
        if parent == nil || !strings.EqualFold(
            strings.Trim(resolver.Resolve(strings.TrimSpace(parent.Text())), "\\"),
            abstractMessageHandlerClass,
        ) {
            continue
        }
        handle := phpOwnMethodForMigration(class, "handle")
        invoke := phpOwnMethodForMigration(class, "__invoke")
        // Rector can still perform the structural migration when a handler has
        // neither method. Only an existing handle()/__invoke() pair would make
        // renaming handle() ambiguous.
        safe := handle == nil || invoke == nil

        name := phpquery.DirectChild(class, phpsyntax.PhpName)
        rng := class.RangeTrimmedTrivia()
        if name != nil {
            rng = name.RangeTrimmedTrivia()
        }
        result = append(result, lsp.Problem{
            ID:       messageHandlerSubscriberCode,
            Range:    rng,
            Element:  class,
            Message:  "Shopware 6.5: migrate AbstractMessageHandler to an attributed Messenger subscriber",
            Severity: protocol.DiagnosticSeverityWarning,
            Source:   "shopware-rector",
            Payload: ShopwareMigrationPayload{
                Rule: "message-handler-subscriber",
                Kind: "class",
                Safe: safe,
            },
        })
    }
    return result
}
```

The patterns to copy:

1. **Check `ctx.Err()` inside every loop.** Diagnostics run on a cancellable
   background job that is superseded by the next keystroke. An analyzer that
   ignores cancellation burns CPU on results nobody will read.
2. **Go through `query`.** `phpquery.Classes`, `phpquery.DirectChild` — never
   positional child indexing.
3. **Report on the narrowest sensible range**, and use
   `RangeTrimmedTrivia()`.
4. **Anchor on the node the fix edits** (`Element: class`), which may be wider
   than the reported range.
5. **Put the fix's decision inputs in the payload** (`safe` here) so the fix
   does not re-analyze the tree.
6. **Guard on missing input.** Analyzers routinely start with
   `document.SyntaxTree == nil || document.SyntaxTree.Root == nil` checks — a
   file with an unregistered extension has no tree.

## Reporting: from Problem to protocol.Diagnostic

`inspectionProblemReporter.Report` is the single funnel. In order, it:

1. **Rejects undeclared codes.** Reporting a code the inspection did not
   declare is an error, not a warning.
2. **Applies configuration.** If the effective policy sets the rule to `off`,
   the problem is dropped. If the rule is unconfigured and the declaration is
   `DisabledByDefault`, it is dropped.
3. **Validates the range** against the document length — inverted or
   out-of-bounds ranges are an error.
4. **Builds the anchor** with `rewrite.NewElementHandle`, deriving the element
   from the range when the problem did not supply one.
5. **Encodes payloads** for the problem and every bound fix, verifying each
   bound fix ID is actually registered on this inspection.
6. **Resolves severity**: the problem's explicit severity, else the
   declaration's default, else — overriding both — the configured severity.
7. **Resolves source**: the problem's, else the declaration's.
8. **Converts the range to LSP coordinates** with
   `protocolRangeFromText(document.LineIndex, problem.Range)`, i.e.
   `LineIndex.PositionUTF16` on both ends.

The result:

```go
protocol.Diagnostic{
    Range:              protocolRangeFromText(document.LineIndex, problem.Range),
    Severity:           severity,
    Code:               string(problem.ID),
    Source:             source,
    Message:            problem.Message,
    Tags:               problem.Tags,
    RelatedInformation: problem.RelatedInformation,
    Data:               data,   // the envelope, under "_shopwareLSP"
}
```

## The diagnostic envelope and element anchors

Everything a quick fix needs later travels in `Diagnostic.Data`, under the
single key `_shopwareLSP`:

```go
// internal/lsp/inspection_registry.go
const diagnosticEnvelopeKey = "_shopwareLSP"
const diagnosticEnvelopeSchema = 1

type diagnosticEnvelope struct {
    Schema          int                   `json:"schema"`
    Inspection      string                `json:"inspection"`
    Code            DiagnosticID          `json:"code"`
    URI             string                `json:"uri"`
    DocumentVersion int                   `json:"documentVersion"`
    Anchor          rewrite.ElementHandle `json:"anchor"`
    Payload         json.RawMessage       `json:"payload,omitempty"`
    Fixes           []boundFixEnvelope    `json:"fixes,omitempty"`
}
```

The schema version is checked on decode, so a client replaying a diagnostic
from an older server version is rejected instead of misinterpreted.

### ElementHandle

A CST node pointer cannot be serialized, and by the time the client asks for
code actions the tree may have been replaced. `rewrite.ElementHandle` is the
serializable stand-in:

```go
// internal/rewrite/handle.go
type ElementHandle struct {
    URI      string
    Version  int
    Language language.ID
    Kind     cst.Kind
    Range    cst.TextRange
    TextHash uint64 `json:"textHash,string"`  // FNV-1a of the element text
}
```

`Resolve` re-finds the element in a current tree and refuses anything
suspicious:

```go
func (h ElementHandle) Resolve(uri string, version int, languageID language.ID, tree *cst.Tree) (cst.Element, error) {
    if h.URI != uri || h.Version != version || h.Language != languageID {
        return nil, ErrStaleHandle
    }
    if tree == nil || tree.Root == nil || h.Range.Start > h.Range.End ||
        h.Range.End > tree.Root.Range().End {
        return nil, ErrMissingHandle
    }
    candidate := tree.Root.DescendantForRange(h.Range)
    for candidate != nil {
        if candidate.Kind() == h.Kind && candidate.Range() == h.Range &&
            hashText(candidate.Text()) == h.TextHash {
            return candidate, nil
        }
        candidate = candidate.Parent()
    }
    return nil, fmt.Errorf("%w: %s kind %s", ErrStaleHandle, h.Range, h.Kind)
}
```

Four things must agree — URI, document version, language, and then kind, exact
range, and text hash. The walk up the ancestor chain from
`DescendantForRange` handles the case where the anchored element is not the
innermost element at that range. A mismatch is `ErrStaleHandle`, which the code
action layer turns into a disabled action reading "The document changed;
request code actions again" rather than an edit against the wrong text.

The `TextHash` is deliberately serialized as a JSON **string**: JavaScript
clients cannot represent every `uint64` in a JSON number without rounding.

## Quick fixes

```go
// internal/lsp/inspection_types.go
type QuickFix interface {
    ID() FixID
    Present(context.Context, FixContext) (FixPresentation, bool, error)
}

type FixPresentation struct {
    Title      string
    Kind       protocol.CodeActionKind
    Preferred  bool
    Resolution FixResolution   // FixEager | FixLazy
}

type FixContext struct {
    Document       *TextDocument
    Diagnostic     protocol.Diagnostic
    Anchor         rewrite.ElementHandle
    ProblemPayload json.RawMessage
    FixPayload     json.RawMessage
    Documents      DocumentResolver
}
```

`Present` is cheap and always runs — it decides the title, the kind, whether
the fix applies at all (the `bool`), and whether the edit is computed now or on
`codeAction/resolve`.

Two implementations extend `QuickFix`:

```go
// A structural source rewrite.
type RewriteQuickFix interface {
    QuickFix
    Build(context.Context, FixContext) (rewrite.WorkspacePlan, error)
}

// An LSP command, for workflows needing user input or editor snippet semantics.
type CommandQuickFix interface {
    QuickFix
    BuildCommand(context.Context, FixContext) (*protocol.CommandAction, error)
}
```

They are kept separate so a command-backed fix can participate in the same
exact diagnostic binding without manufacturing an empty edit plan.

### Resolution

`FixLazy` defers `Build` to `codeAction/resolve`, which matters when computing
the edit is expensive and the user may never invoke it. The server honors it
only when the client advertises resolve support:

```go
if presentation.Resolution == FixEager || !s.codeActionResolveSupport {
    s.populateInspectionEdit(ctx, &action, fix, fixContext)
}
```

### Plan validation

`Server.validateWorkspacePlan` runs before any plan becomes a
`WorkspaceEdit`. For each planned document it re-resolves the current document,
rejects a source or version mismatch as `ErrStaleHandle`, applies the edit, and
re-parses the result:

```go
_, result, parsed := s.documentManager.languages.ParsePath(planned.URI, updated)
if parsed && len(result.Errors) > len(current.Document.ParseErrors) {
    return fmt.Errorf("rewrite introduces %d parser errors", len(result.Errors)-len(current.Document.ParseErrors))
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

Four independent gates run per inspection, per document, in
`collectDiagnostics`:

1. **Client presentation profile.** `inspectionPresentedToClient` suppresses
   inspections the host IDE already provides. Under the framework-only profile
   used by the PhpStorm integration, `php.semantic` and
   `symfony.embedded_language` are withheld so generic PHP presentation stays
   with the IDE. See [`phpstorm-integration.md`](phpstorm-integration.md).
2. **Domain.** `inspectionDomain(id)` maps an inspection to a configuration
   domain (`php.semantic` → `php`, `shopware.migration` →
   `shopware.migrations`, `shopware.snippet` → `shopware.snippets`, …), and the
   domain can be switched off wholesale.
3. **Inspection.** `diagnostics.inspections[<id>]` in project configuration can
   disable one inspection; an unconfigured inspection is enabled.
4. **Rules.** `inspectionHasEnabledRule` skips an inspection entirely when
   every one of its codes is either configured `off` or unconfigured and
   `DisabledByDefault`. This is the gate that keeps a 21-code inspection from
   running when the user turned all of it off.

Severity resolution and rule enablement both come from
`projectconfig.DiagnosticPolicy`, which is computed per file path:

```go
policy := s.diagnosticPolicy(document.URI)
```

`projectconfig` resolves project configuration, editor settings, and
path-scoped overrides into that policy, so `.config/shopware/lsp.yaml` can turn
a rule to `off`, `hint`, `information`, `warning`, or `error` for a glob such as
`custom/plugins/*/src/Generated/**`. Nested configuration files may only
contain a `diagnostics` section. See
[`reference.md#project-configuration`](reference.md#project-configuration).

The precedence, from the reporter's perspective: a configured rule severity
wins over the problem's explicit severity, which wins over the declared
default. A configured `off` drops the problem entirely.

## Scheduling, debouncing, and caching

`scheduleDiagnostics(uri, version, delay)` is the only entry point. It:

- creates a cancellable job under the server lifecycle context;
- bumps a per-URI **generation** counter and invalidates that URI's cache
  entry;
- cancels any previously scheduled job for the same URI;
- starts a background timer that runs `publishDiagnostics` after `delay`, or
  aborts on cancellation.

Interactive edits schedule with `diagnosticsDebounce` (150 ms). Explicit
`PublishDiagnostics` calls use no delay.

`publishDiagnostics` re-checks the document version before analyzing and again,
under `diagnosticsPublishMu`, before notifying — so a superseded job cannot
publish stale results over a newer run, and a canceled analysis cannot write
after the close-document clear.

The cache is keyed on both the document pointer and the generation:

```go
type diagnosticsCacheEntry struct {
    document    *TextDocument
    generation  uint64
    diagnostics []protocol.Diagnostic
}
```

Because `TextDocument` snapshots are immutable, pointer identity is a valid
freshness test. `diagnosticsForDocument` serves both the push path and the pull
path (`textDocument/diagnostic`), so a pull request right after a push costs
nothing. Results are copied in and out of the cache so a caller cannot mutate
the shared slice. A canceled run is never cached.

`RefreshOpenDocumentDiagnostics(match)` re-analyzes open documents affected by a
change elsewhere — the "open dependency changed" case — using the debounce so a
workspace overlay does not schedule one job per keystroke. It is skipped in CLI
mode.

## Parser and language-level diagnostics

Syntax errors are not a special path. Because `Parse` is total, the tree always
exists, and `parsekit.Error` values ride along on the document:

```go
// internal/lsp/document.go
type TextDocument struct {
    // ...
    ParseErrors []parsekit.Error
}
```

The PHP semantic provider turns them into ordinary problems alongside its
semantic findings:

```go
// internal/lsp/phpsemantic/diagnostics_run.go
func (r *phpDiagnosticRun) analyze() []lsp.Problem {
    r.addParseErrors()
    r.addLanguageLevelProblems()
    r.addReferenceProblems()
    r.addSemanticIssues()
    r.addDuplicateDeclarations()
    return r.diagnostics
}

func (r *phpDiagnosticRun) addParseErrors() {
    for errorIndex := range r.document.ParseErrors {
        parseError := &r.document.ParseErrors[errorIndex]
        if r.suppressions.Suppresses(parseError.Range.Start, "php.parse") {
            continue
        }
        r.diagnostics = append(r.diagnostics, lsp.Problem{
            Range:    parseError.Range,
            Severity: protocol.DiagnosticSeverityError,
            ID:       "php.parse",
            Source:   "shopware-php",
            Message:  parseError.Message(),
        })
    }
}
```

Two consequences worth internalizing:

- A syntax error never suppresses semantic analysis. The tree still has `ERROR`
  nodes in place of the broken region, and every other construct in the file is
  still analyzed. This is why completion and hover keep working while you type.
- Parse-error diagnostics obey the same suppression comments and severity
  configuration as everything else (`php.parse`, `php.version`, …).

Language-level checks work the same way: `languagelevel.Detect(root)` walks the
tree for version-gated syntax and reports `php.version` when the configured
PHP version does not support it.

### Embedded languages

String literals holding another language are diagnosed by parsing them on
demand. `php.EmbeddedLanguageStrings` recognizes the signature that makes a
literal JSON, CSS, or XPath, and each sub-language runs its own frontend over
just that literal:

```go
// internal/lsp/diagnostics/embedded_language_diagnostics.go
embedded := php.EmbeddedLanguageStrings(
    p.phpIndex, path, document.Version, string(document.Text), document.SyntaxTree.Root,
)
for _, literal := range embedded {
    switch literal.Language {
    case php.EmbeddedLanguageJSON:
        result = append(result, embeddedJSONDiagnostics(literal.EmbeddedPHPString, document.LineIndex)...)
    case php.EmbeddedLanguageCSS:
        result = append(result, embeddedCSSDiagnostics(literal.EmbeddedPHPString, document.LineIndex)...)
    case php.EmbeddedLanguageXPath:
        result = append(result, embeddedXPathDiagnostics(literal.EmbeddedPHPString, document.LineIndex)...)
    }
}
```

The whole family — every supported injection signature — is validated in one
PHP CST scan and one semantic analysis, and ranges are translated back to
absolute offsets in the host file. Vue takes the other approach and embeds its
sections directly in one tree; see
[the embedding section](parser-architecture.md#embedded-and-multi-language-trees).

## Normalization

Independent inspections legitimately overlap. `normalizeDiagnostics` gives the
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

1. **Write the analyzer** in `internal/lsp/diagnostics`, implementing
   `Analyze(ctx, *lsp.TextDocument) ([]lsp.Problem, error)`. Guard on a nil
   syntax tree, check `ctx.Err()` in loops, use the language's `query` package,
   and report trimmed ranges.
2. **Declare the inspection** in `internal/lsp/inspections`, listing its
   languages and every `ProblemDefinition` it can emit — each with a `Source`
   and a `DefaultSeverity`. Set `DisabledByDefault` for opt-in checks.
3. **Add quick fixes** implementing `RewriteQuickFix` or `CommandQuickFix`, and
   wire them in `bind` so each code offers the right fixes with the right
   payloads.
4. **Register it** from the application composition root with
   `Server.RegisterInspection`.
5. **Map a configuration domain** in `inspectionDomain` if the inspection
   belongs to a toggleable feature domain.
6. **Test it** — see below.

## Debugging and testing

### Unit level

Analyzer tests construct a `TextDocument` from a source string and assert on
the returned problems, with no server involved:

```go
document := lsp.NewTextDocument("file:///src/Handler.php", source, 1)
problems, err := analyzer.Analyze(context.Background(), document)
require.NoError(t, err)
```

`NewTextDocument` parses through the default registry, so the tree, line index,
and parse errors are all populated. Assert on `cst.TextRange` values rather than
line/column where possible — they are stable against reformatting of the
fixture.

### Server level

`internal/lsp/server_diagnostics_test.go` covers the scheduling, debounce, and
cache behavior end to end. `internal/app/diagnostics_audit_test.go` audits the
registered inspection set as a whole — the guard against a code being declared
twice, losing its source, or drifting from its configuration domain.

### CLI

The `check` command runs the same inspections outside an editor, which is the
fastest way to see what a real workspace produces:

```bash
shopware-lsp -root /path/to/project check src tests
shopware-lsp -root /path/to/project check -fail-on error src/Controller.php
shopware-lsp -root /path/to/project -json check src
```

`codeaction` exercises the fix path, previewing a unified diff unless `-w` is
passed:

```bash
shopware-lsp -root /path/to/project codeaction -kind quickfix src/Controller.php:24:18
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
