# Refactoring Engine: Edits, Plans, and Validation

This document explains how Shopware Language Server changes source code. Every
edit — a one-token quick fix, a cross-file rename, a scaffolded new class —
flows through the same three stages: resolve an element in an immutable
snapshot, compile byte edits, validate the result before the editor sees it.

The core is [`internal/rewrite`](../internal/rewrite), with language-aware
helpers in [`internal/php/rewrite`](../internal/php/rewrite) and callers in
`internal/lsp/{inspections,codeaction,refactor,scaffold}`.

## Table of contents

- [The central rule](#the-central-rule)
- [Edits](#edits)
- [Builder and conflict rules](#builder-and-conflict-rules)
- [Applying edits](#applying-edits)
- [Document and workspace plans](#document-and-workspace-plans)
- [Element handles](#element-handles)
- [Validation](#validation)
- [Worked example: a quick fix](#worked-example-a-quick-fix)
- [Language-aware rewrites: the PHP editor](#language-aware-rewrites-the-php-editor)
- [Rename](#rename)
- [File rename](#file-rename)
- [Scaffolding](#scaffolding)
- [Commands instead of edits](#commands-instead-of-edits)
- [Design rules](#design-rules)
- [Testing](#testing)

## The central rule

> **Nothing mutates a syntax tree.**

A CST is a lossless view of one exact source snapshot. Changing source means:

1. resolve the element you want to change **in that snapshot**;
2. compile the change to byte edits **against that snapshot**;
3. hand the edits to the client as a workspace edit.

The package doc states it plainly:

```go
// Package rewrite builds validated, lossless source edits from immutable CST
// elements. It deliberately does not mutate syntax trees: callers resolve an
// element in one document snapshot and compile the requested change to byte
// edits for that same snapshot.
```

Two consequences follow, and both are load-bearing:

- **A rewrite is only valid for the snapshot it was built against.** If the
  document changed, the rewrite is stale and must be rejected, not adapted. That
  is what [`ElementHandle`](#element-handles) enforces.
- **The server never writes files.** It returns a `protocol.WorkspaceEdit`. The
  editor, the CLI, and MCP each apply it through their own path, and all three
  get the same validation.

## Edits

```go
// internal/rewrite/rewrite.go
type Edit struct {
    Range   cst.TextRange   // byte offsets into the snapshot
    NewText string
    order   int             // declaration order, for insertion coalescing
}
```

An `Edit` is a byte-range replacement. Insertion is a zero-width range;
deletion is an empty `NewText`. There is no "insert node" or "wrap in
parentheses" operation — everything reduces to replacing bytes, which is what
makes losslessness trivially preserved: bytes you did not touch are unchanged,
including formatting and comments.

## Builder and conflict rules

```go
builder := rewrite.NewBuilder(document.Source)

builder.Replace(element, "newText")     // element.Range() ← text
builder.Delete(element)                 // element.Range() ← ""
builder.InsertBefore(element, "text")   // at element.Range().Start
builder.InsertAfter(element, "text")    // at element.Range().End
builder.Insert(offset, "text")          // zero-width at offset
builder.ReplaceRange(rng, "text")       // explicit range

edits, err := builder.Finish()
```

Every method funnels into `ReplaceRange`, which validates the range against the
snapshot length and returns `ErrInvalidRange` for an inverted or out-of-bounds
range. Passing a nil element is also `ErrInvalidRange` rather than a panic.

`Finish()` does three things:

**1. Sorts** by start, then end, then declaration order — stably.

**2. Coalesces insertions at the same offset** in declaration order:

```go
if previous.Range.Start == previous.Range.End &&
    edit.Range.Start == edit.Range.End &&
    previous.Range.Start == edit.Range.Start {
    previous.NewText += edit.NewText
    continue
}
```

So two `InsertBefore` calls on the same element produce the text in the order
you wrote them, not an overlap error. This is what makes composing independent
rewrites work — adding a `use` declaration and adding an attribute at the same
offset simply concatenate.

**3. Rejects overlaps** with `ErrOverlap`. `editsConflict` treats a zero-width
insertion as conflicting only when it lands *strictly inside* another edit's
range — touching a boundary is fine:

```go
func editsConflict(left, right Edit) bool {
    if left.Range.Start == left.Range.End {
        return left.Range.Start > right.Range.Start && left.Range.Start < right.Range.End
    }
    if right.Range.Start == right.Range.End {
        return right.Range.Start > left.Range.Start && right.Range.Start < left.Range.End
    }
    return left.Range.Start < right.Range.End && right.Range.Start < left.Range.End
}
```

An insertion at the start or end of a replaced range is therefore legal, while
an insertion in the middle of text that is being replaced is an error — the
result would be undefined.

## Applying edits

```go
func Apply(source string, edits []Edit) (string, error)
```

`Apply` re-validates through a fresh `Builder` (so it cannot be handed
unvalidated edits) and then applies them **back to front**:

```go
for index := len(validated) - 1; index >= 0; index-- {
    edit := validated[index]
    result = result[:edit.Range.Start] + edit.NewText + result[edit.Range.End:]
}
```

Descending order means earlier offsets stay valid as later ones are rewritten —
no offset bookkeeping, no shift accumulation. The input snapshot is never
mutated.

## Document and workspace plans

```go
type DocumentPlan struct {
    URI       string
    Version   *int          // nil = versionless (a freshly created file)
    Source    string        // the snapshot the edits were built against
    LineIndex *cst.LineIndex
    Edits     []Edit
}

func NewDocumentPlan(uri string, version *int, source string, edits []Edit) DocumentPlan
func (p DocumentPlan) Apply() (string, error)
```

A `DocumentPlan` carries the snapshot alongside the edits. That is what lets the
server verify, at apply time, that the document has not changed — and what lets
tests apply a plan without a server.

```go
type WorkspacePlan struct {
    Documents []DocumentPlan
    Creates   []CreateFilePlan
    Deletes   []DeleteFilePlan
}

type CreateFilePlan struct { URI, Content string }
type DeleteFilePlan struct { URI string; Version *int; Source string }
```

`WorkspaceEdit()` compiles the plan into `protocol.DocumentChanges` in a fixed
order — **creates, then document edits, then deletes** — because a document edit
may target a file the same plan creates. A create becomes a
`CreateFileOperation` with `Overwrite: false, IgnoreIfExists: false` (so an
unexpected existing file is an error, not a silent clobber), followed by a
versionless text edit inserting the content.

A URI may appear only once across all three sections; a duplicate is an error
rather than an ambiguous merge.

Byte ranges become UTF-16 protocol ranges here, using the plan's line index.
This is the only place in the rewrite pipeline that knows about UTF-16.

## Element handles

A quick fix is presented, sent to the client, and possibly resolved and applied
many keystrokes later. A CST pointer cannot survive that, and a byte range alone
is not enough — the document may have changed underneath it.

```go
// internal/rewrite/handle.go
type ElementHandle struct {
    URI      string
    Version  int
    Language language.ID
    Kind     cst.Kind
    Range    cst.TextRange
    TextHash uint64 `json:"textHash,string"`   // FNV-1a of the element text
}
```

`Resolve` re-finds the element and refuses anything that does not match exactly:

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

Six checks: URI, version, language, kind, exact range, text hash. The walk up
from `DescendantForRange` handles the case where the anchored element is not the
innermost element at that range.

`TextHash` is serialized as a JSON **string** deliberately: JavaScript clients
cannot represent every `uint64` in a JSON number without rounding, and a
silently rounded hash would defeat the check.

Callers turn the two error types into user-facing reasons:

| Error | Meaning | Typical message |
| --- | --- | --- |
| `ErrStaleHandle` | The document changed | "The document changed; request code actions again" |
| `ErrMissingHandle` | No element to anchor to | "The quick fix could not be prepared" |

## Validation

`Server.validateWorkspacePlan` runs before **any** plan becomes a
`WorkspaceEdit`. Four checks, per section.

**Created files** must be inside the workspace root, must not already exist, and
must parse without errors:

```go
if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
    return fmt.Errorf("created document %q already exists or cannot be checked", created.URI)
}
if _, result, parsed := s.documentManager.languages.ParsePath(created.URI, created.Content); parsed && len(result.Errors) != 0 {
    return fmt.Errorf("created document has %d parser errors", len(result.Errors))
}
```

**Edited documents** must still match the snapshot, and applying the edit must
not make the syntax worse:

```go
if current.Document.Source != planned.Source || !sameOptionalVersion(current.Version, planned.Version) {
    return rewrite.ErrStaleHandle
}
updated, err := planned.Apply()
_, result, parsed := s.documentManager.languages.ParsePath(planned.URI, updated)
if parsed && len(result.Errors) > len(current.Document.ParseErrors) {
    return fmt.Errorf("rewrite introduces %d parser errors", len(result.Errors)-len(current.Document.ParseErrors))
}
```

The comparison is against the document's **existing** error count, not zero. A
fix must be able to apply inside an already-broken file — that is often exactly
when you need it — but it must not add new syntax errors. This one check catches
most malformed-rewrite bugs before a user ever sees them.

**Deleted files** must exist as regular files and must match the recorded
snapshot and version.

**Cancellation** is checked between every item.

When validation fails the code action is returned **disabled with a reason**
rather than dropped or shipped:

```go
action.Disabled = &protocol.CodeActionDisabled{Reason: "The generated edit is no longer valid"}
action.Edit = nil
```

A disabled action with a reason is good UX; a broken edit is not.

## Worked example: a quick fix

Replacing a deprecated Vue I18n `$tc` call with `$t`, complete:

```go
// internal/lsp/inspections/admin_i18n.go
func (adminI18nTCFix) Build(
    _ context.Context,
    fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
    // 1. decode the payload the analyzer bound when it reported the problem
    payload, err := lsp.DecodeBoundFixPayload[adminI18nTCReplacement](fixContext)
    if err != nil {
        return rewrite.WorkspacePlan{}, err
    }
    if payload.Replacement != "$t" {
        return rewrite.WorkspacePlan{}, fmt.Errorf("invalid Vue I18n replacement")
    }

    // 2. resolve the anchor against the CURRENT tree
    element, err := fixContext.Anchor.Resolve(
        fixContext.Document.URI,
        fixContext.Document.Version,
        fixContext.Document.SyntaxLanguage,
        fixContext.Document.SyntaxTree,
    )
    if err != nil {
        return rewrite.WorkspacePlan{}, err
    }

    // 3. re-verify the assumption the fix depends on
    if element.Text() != "$tc" {
        return rewrite.WorkspacePlan{}, rewrite.ErrStaleHandle
    }

    // 4. compile edits against this snapshot
    builder := rewrite.NewBuilder(fixContext.Document.Source)
    if err := builder.Replace(element, payload.Replacement); err != nil {
        return rewrite.WorkspacePlan{}, err
    }
    edits, err := builder.Finish()
    if err != nil {
        return rewrite.WorkspacePlan{}, err
    }

    // 5. return a versioned plan
    version := fixContext.Document.Version
    return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
        rewrite.NewDocumentPlan(
            fixContext.Document.URI, &version, fixContext.Document.Source, edits,
        ),
    }}, nil
}
```

Step 3 is the one people skip. `ElementHandle.Resolve` proves the *element* is
unchanged, but a fix usually also depends on a *semantic* assumption. Re-check
it and return `ErrStaleHandle` if it no longer holds — that turns a wrong edit
into a graceful "request code actions again".

Also note: **always pass the version.** A versioned document edit lets the
editor reject the change if the buffer moved between response and application.
Omit it only for files the same plan creates.

## Language-aware rewrites: the PHP editor

Byte edits are the right primitive, but "add an interface to this class" should
not be written by hand at every call site. `internal/php/rewrite` provides a
composable editor over the PHP CST:

```go
// internal/php/rewrite/editor.go
editor := phprewrite.NewEditor(source, root)
// ... schedule several related changes ...
edits, err := editor.Finish()
```

Available operations:

| Area | Methods |
| --- | --- |
| Imports and references | `ClassReference(className)` |
| Class shape | `SetExtends`, `RemoveExtends`, `AddImplements`, `RemoveImplements`, `AddAttribute` |
| Members | `InsertClassMember`, `RemoveClassMember` |
| Lists | `InsertArgument`, `AppendArgument`, `RemoveArgument`, `InsertParameter`, `AppendParameter`, `RemoveParameter` |
| Types | `SetParameterType`, `SetPropertyType`, `SetReturnType` |
| PHPDoc | `FindPHPDocAnnotation` and related helpers |
| Raw | `ReplaceRange`, `Insert`, `Delete` |

Two of these deserve highlighting because they encode judgment you would
otherwise re-derive:

**`ClassReference(className)`** returns the shortest unambiguous reference to a
class *and* schedules a `use` declaration when one is useful. If an existing
alias would conflict, it falls back to a fully-qualified reference rather than
rewriting someone's imports:

```go
// ClassReference returns the shortest unambiguous reference to className and
// schedules a use declaration when one is useful. Conflicting aliases fall
// back to a fully-qualified reference rather than changing existing imports.
```

**`InsertClassMember`** inserts before the closing brace and re-indents every
non-empty line to match the surrounding class body, so generated members do not
arrive with the generator's indentation.

Both compose: schedule an import, an attribute, and a member, then `Finish()`
once. The insertion-coalescing rule in `Builder` is what makes several
independent operations at the same offset behave.

When you need a Twig, XML, or YAML rewrite, use `rewrite.Builder` directly with
ranges from that language's `query` package. Only PHP currently has enough
recurring rewrite shapes to justify a dedicated editor.

## Rename

`textDocument/rename` uses **first-match** dispatch, not fan-out:

```go
// internal/lsp/rename.go
for _, provider := range s.renameProviders {
    edit, err := provider.Rename(ctx, request)
    if err != nil {
        return nil, fmt.Errorf("rename provider %T: %w", provider, err)
    }
    if edit != nil {
        return edit, nil          // first provider that answers wins
    }
}
return nil, nil
```

That differs from completion and definition deliberately: concatenating two
providers' rename edits would produce a conflicting or double-applied refactor.
A rename provider must therefore be confident about ownership — return `nil`
unless the symbol under the cursor is clearly yours.

Rename providers live in `internal/lsp/refactor`:

- `php_twig_rename.go` — a PHP symbol whose name also appears in Twig
- `twig_template_rename.go` — template paths and block names
- `admin_rename.go` — Administration components and their references

Cross-language rename is exactly why rewrite plans are workspace-scoped:
renaming a Twig block touches every template that extends or overrides it, and
all of those edits must land as one workspace edit or none.

## File rename

`workspace/willRenameFiles` lets the server update references *before* the
editor moves a file:

```go
// internal/lsp/file_rename.go
type FileRenameRequest struct {
    *protocol.RenameFilesParams
    Documents []*TextDocument      // every open document
}

type FileRenameProvider interface {
    WillRenameFiles(context.Context, *FileRenameRequest) (*protocol.WorkspaceEdit, error)
}
```

Unlike symbol rename this **is** a fan-out: every provider contributes, and
their `Changes` maps are merged per URI. Different providers own different
reference kinds — a moved Twig template affects `extends`/`include` paths, a
moved PHP class affects imports — and those are independent.

The request carries all open documents because unsaved buffers must be updated
too, not just indexed files.

## Scaffolding

`internal/lsp/scaffold` generates new code — entities, plugins, DAL
definitions, Administration components. It uses the same machinery:
`CreateFilePlan` for new files, `DocumentPlan` for registration edits in
existing files, one `WorkspacePlan` for the whole operation.

The validation rules apply unchanged: created files must be inside the root,
must not exist, and must parse. A scaffold that generates syntactically invalid
code is caught before the editor applies it.

## Commands instead of edits

Some workflows genuinely cannot be an edit — they need user input, editor
snippet semantics, or a multi-step interaction. Those implement
`CommandQuickFix` instead:

```go
type CommandQuickFix interface {
    QuickFix
    BuildCommand(context.Context, FixContext) (*protocol.CommandAction, error)
}
```

**Prefer edits.** A workspace edit works in every client. A command only works
in a client that implements it, and the server filters out actions whose
commands the client did not advertise — MCP in particular can apply workspace
edits but cannot execute editor-only commands. See
[`lsp-server.md`](lsp-server.md#gating-features-domains-client-profiles).

Also: **code actions must never write files directly.** Returning a workspace
edit is what lets the editor, the CLI (`codeaction -exec`), and MCP validate and
apply the change consistently — and what makes `codeaction` able to preview a
unified diff without touching the disk.

## Design rules

1. **Never mutate a tree.** Resolve, compile, return.
2. **One snapshot per plan.** Every edit in a `DocumentPlan` must be built
   against that plan's `Source`.
3. **Always carry the version** for edits to existing documents.
4. **Resolve the anchor, then re-verify the semantic assumption.** A matching
   element is not proof that the fix still applies.
5. **Prefer `RewriteQuickFix` over `CommandQuickFix`.**
6. **Prefer lazy resolution** (`FixLazy`) for non-trivial or cross-file
   rewrites, so element handles get a chance to reject a stale document instead
   of computing an edit nobody asked for.
7. **Compose through one builder or editor.** Do not concatenate strings and
   hope the offsets line up; let `Finish()` catch the conflict.
8. **Return a reason, not a broken edit.** If the rewrite cannot be produced
   safely, fail — the caller turns that into a disabled action with an
   explanation.
9. **Reuse analyzers and rewrite helpers across migrations.** For
   version-migration inspections in particular, prefer a shared analyzer over
   copying one Rector rule verbatim into each inspection.

## Testing

Test a rewrite at three levels.

**The edit itself** — apply the plan and compare source:

```go
plan, err := fix.Build(context.Background(), fixContext)
require.NoError(t, err)
require.Len(t, plan.Documents, 1)

updated, err := plan.Documents[0].Apply()
require.NoError(t, err)
require.Equal(t, expectedSource, updated)
```

**Staleness** — build the fix against one document version, then resolve against
a newer one and assert `ErrStaleHandle`. This is the regression that matters
most, because it only manifests as a corrupted file in production.

**Protocol shape** — a server-level test that the action is offered, that
`codeAction/resolve` produces the edit for a lazy fix, and that the ranges are
correct UTF-16. Use a real `TextDocument` so the conversion is actually
exercised; a hand-built range skips the most error-prone step.

Also assert the **validation** path: a fix that would introduce parser errors
must be rejected. `internal/rewrite/rewrite_test.go` covers builder conflicts,
coalescing, and `Apply`; `internal/php/rewrite/*_test.go` covers the PHP editor
operations; `internal/lsp/codeaction` and `internal/lsp/inspections` cover
presentation and end-to-end fix behavior.

```bash
go test ./internal/rewrite ./internal/php/rewrite
go test ./internal/lsp/inspections ./internal/lsp/codeaction ./internal/lsp/refactor
shopware-lsp -root /path/to/project codeaction -kind quickfix src/Controller.php:24:18
shopware-lsp -root /path/to/project rename -d src/Controller.php:24:18 NewName
```

The last two print a unified diff without writing, which is the fastest way to
eyeball a rewrite against a real project.

## Further reading

- [`diagnostics-pipeline.md`](diagnostics-pipeline.md) — where quick fixes are
  bound and resolved
- [`parser-architecture.md`](parser-architecture.md) — elements, ranges, and
  `RangeTrimmedTrivia`
- [`lsp-server.md`](lsp-server.md) — command filtering and client capabilities
- [`php-semantic-engine.md`](php-semantic-engine.md) — the semantic information
  PHP rewrites depend on
- [`shopware-rector-parity.md`](shopware-rector-parity.md) — the migration
  rewrites and their Rector counterparts
