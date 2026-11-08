# Refactoring Engine: Edits, Plans, and Validation

This document explains how Shopware Language Server changes source code. Every
edit — a one-token quick fix, a cross-file rename, a scaffolded new class —
flows through the same three stages: resolve an element in an immutable
snapshot, compile byte edits, validate the result before the editor sees it.

The core is [`swlsp-rewrite`](../crates/rewrite), with language-aware
helpers in [`swlsp-php-rewrite`](../crates/php-rewrite) and callers in
`crates/lsp-{inspections,codeaction,refactor,scaffold}`.

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

The crate doc states it plainly:

```rust
//! Validated, lossless source edits compiled from immutable syntax trees.
//!
//! Trees are never mutated: callers resolve an element in one document
//! snapshot and compile the requested change to byte edits for that same
//! snapshot.
```

Two consequences follow, and both are load-bearing:

- **A rewrite is only valid for the snapshot it was built against.** If the
  document changed, the rewrite is stale and must be rejected, not adapted. That
  is what [`ElementHandle`](#element-handles) enforces.
- **The server never writes files.** It returns a `protocol::WorkspaceEdit`. The
  editor, the CLI, and MCP each apply it through their own path, and all three
  get the same validation.

## Edits

```rust
// crates/rewrite/src/lib.rs
pub struct Edit {
    pub range: TextRange,   // byte offsets into the snapshot
    pub new_text: String,
    order: usize,           // declaration order, for insertion coalescing
}
```

An `Edit` is a byte-range replacement. Insertion is a zero-width range;
deletion is an empty `new_text`. There is no "insert node" or "wrap in
parentheses" operation — everything reduces to replacing bytes, which is what
makes losslessness trivially preserved: bytes you did not touch are unchanged,
including formatting and comments.

## Builder and conflict rules

```rust
let mut builder = Builder::new(document.source());

builder.replace(&element, "newText")?;     // element range ← text
builder.delete(&element)?;                 // element range ← ""
builder.insert_before(&element, "text")?;  // at the element's start
builder.insert_after(&element, "text")?;   // at the element's end
builder.insert(offset, "text")?;           // zero-width at offset
builder.replace_range(range, "text")?;     // explicit range

let edits = builder.finish()?;
```

Elements are anything implementing `SyntaxRange` (nodes, tokens, elements, or
a plain `TextRange`). Every method funnels into `replace_range`, which
validates the range against the snapshot length and returns
`RewriteError::InvalidRange` for an out-of-bounds range.

`finish()` does three things:

**1. Sorts** by start, then end, then declaration order — stably.

**2. Coalesces insertions at the same offset** in declaration order:

```rust
if previous.range.is_empty()
    && edit.range.is_empty()
    && previous.range.start() == edit.range.start()
{
    previous.new_text.push_str(&edit.new_text);
    continue;
}
```

So two `insert_before` calls on the same element produce the text in the order
you wrote them, not an overlap error. This is what makes composing independent
rewrites work — adding a `use` declaration and adding an attribute at the same
offset simply concatenate.

**3. Rejects overlaps** with `RewriteError::Overlap`. `edits_conflict` treats a zero-width
insertion as conflicting only when it lands *strictly inside* another edit's
range — touching a boundary is fine:

```rust
fn edits_conflict(left: &Edit, right: &Edit) -> bool {
    let (left, right) = (left.range, right.range);
    if left.is_empty() {
        return left.start() > right.start() && left.start() < right.end();
    }
    if right.is_empty() {
        return right.start() > left.start() && right.start() < left.end();
    }
    left.start() < right.end() && right.start() < left.end()
}
```

An insertion at the start or end of a replaced range is therefore legal, while
an insertion in the middle of text that is being replaced is an error — the
result would be undefined.

## Applying edits

```rust
pub fn apply(source: &str, edits: &[Edit]) -> Result<String>
```

`apply` re-validates through a fresh `Builder` (so it cannot be handed
unvalidated edits), rejects ranges that would split a UTF-8 character, and then
copies the source into a new string in one ascending pass:

```rust
let mut cursor = 0usize;
for edit in &validated {
    let start = usize::from(edit.range.start());
    result.push_str(&source[cursor..start]);
    result.push_str(&edit.new_text);
    cursor = usize::from(edit.range.end());
}
result.push_str(&source[cursor..]);
```

Because the edits are sorted and non-overlapping, every range refers to the
original snapshot — no offset bookkeeping, no shift accumulation. The input
snapshot is never mutated.

## Document and workspace plans

```rust
pub struct DocumentPlan {
    pub uri: String,
    pub version: Option<i32>,               // None = versionless (a freshly created file)
    pub source: Arc<str>,                   // the snapshot the edits were built against
    pub line_index: Option<Arc<LineIndex>>, // built on demand when absent
    pub edits: Vec<Edit>,
}

impl DocumentPlan {
    pub fn new(uri, version: Option<i32>, source, edits: Vec<Edit>) -> Self;
    pub fn with_line_index(self, line_index: Arc<LineIndex>) -> Self;
    pub fn apply(&self) -> Result<String>;
}
```

A `DocumentPlan` carries the snapshot alongside the edits. That is what lets the
server verify, at apply time, that the document has not changed — and what lets
tests apply a plan without a server.

```rust
pub struct WorkspacePlan {
    pub documents: Vec<DocumentPlan>,
    pub creates: Vec<CreateFilePlan>,
    pub deletes: Vec<DeleteFilePlan>,
}

pub struct CreateFilePlan { pub uri: String, pub content: String }
pub struct DeleteFilePlan { pub uri: String, pub version: Option<i32>, pub source: String }
```

`workspace_edit()` compiles the plan into ordered `documentChanges` in a fixed
order — **creates, then document edits, then deletes** — because a document edit
may target a file the same plan creates. A create becomes a
`create` resource operation with default options (neither `overwrite` nor
`ignoreIfExists`, so an unexpected existing file is an error, not a silent
clobber), followed by a versionless text edit inserting non-empty content.

A URI may appear only once across all three sections; a duplicate is an error
rather than an ambiguous merge.

Byte ranges become UTF-16 protocol ranges here, using the plan's line index.
This is the only place in the rewrite pipeline that knows about UTF-16.

## Element handles

A quick fix is presented, sent to the client, and possibly resolved and applied
many keystrokes later. A CST pointer cannot survive that, and a byte range alone
is not enough — the document may have changed underneath it.

```rust
// crates/rewrite/src/handle.rs
#[serde(rename_all = "camelCase")]
pub struct ElementHandle {
    pub uri: String,
    pub version: i32,
    pub language: LanguageId,
    pub kind: Kind,
    pub range: TextRange,
    #[serde(with = "hash_serde")]   // a decimal string
    pub text_hash: u64,             // FNV-1a of the element text
}
```

`resolve` re-finds the element and refuses anything that does not match exactly:

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

Six checks: URI, version, language, kind, exact range, text hash. The walk up
from `covering_element` handles the case where the anchored element is not the
innermost element at that range.

`text_hash` is serialized as a JSON **string** deliberately: JavaScript clients
cannot represent every `uint64` in a JSON number without rounding, and a
silently rounded hash would defeat the check.

Callers turn the two error types into user-facing reasons:

| Error | Meaning | Typical message |
| --- | --- | --- |
| `HandleError::Stale` / `StaleElement` | The document changed | "The document changed; request code actions again" |
| `HandleError::Missing` | No element to anchor to | "The quick fix could not be prepared" |

## Validation

`Server::validate_workspace_plan` (`crates/lsp/src/code_action.rs`) runs
before **any** plan becomes a `WorkspaceEdit`. Four checks, per section.

**Created files** must be inside the workspace root, must not be open or
already exist, and must parse without errors:

```rust
match std::fs::metadata(&path) {
    Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
    _ => return Err(format!("created document {} already exists or cannot be checked", ...).into()),
}
if let Some((_, parse)) = parse_path(&created.uri, &created.content)
    && !parse.errors.is_empty()
{
    return Err(format!("created document has {} parser errors", parse.errors.len()).into());
}
```

**Edited documents** must still match the snapshot, and applying the edit must
not make the syntax worse:

```rust
let current = self.resolve_snapshot(ctx, &planned.uri)?;
if current.document.source() != &*planned.source
    || !same_optional_version(current.version, planned.version)
{
    return Err(stale());
}
let updated = planned.apply()?;
if let Some((_, parse)) = parse_path(&planned.uri, &updated)
    && parse.errors.len() > current.document.parse_errors().len()
{
    return Err(format!("rewrite introduces {} parser errors", ...).into());
}
```

The comparison is against the document's **existing** error count, not zero. A
fix must be able to apply inside an already-broken file — that is often exactly
when you need it — but it must not add new syntax errors. This one check catches
most malformed-rewrite bugs before a user ever sees them.

**Deleted files** must be inside the workspace root, exist as regular files, and must match the recorded
snapshot and version.

**Cancellation** is checked between every item.

When validation fails the code action is returned **disabled with a reason**
rather than dropped or shipped:

```rust
action.disabled = Some(protocol::CodeActionDisabled {
    reason: "The generated edit is no longer valid".to_owned(),
});
action.edit = None;
```

A disabled action with a reason is good UX; a broken edit is not.

## Worked example: a quick fix

Replacing a deprecated Vue I18n `$tc` call with `$t`, complete:

```rust
// crates/lsp-inspections/src/admin/fixes.rs
impl RewriteQuickFix for AdminI18nTcFix {
    fn build(&self, _ctx: &Context, fix: &FixContext) -> Result<WorkspacePlan, BoxError> {
        // 1. decode the payload the analyzer bound when it reported the problem
        let payload: AdminI18nTcReplacement = fix.fix_payload()?;
        if payload.replacement != "$t" {
            return Err("invalid Vue I18n replacement".into());
        }

        // 2. resolve the anchor against the CURRENT tree
        let element = resolve_anchor(fix)?;

        // 3. re-verify the assumption the fix depends on
        let text = match &element {
            SyntaxElement::Node(node) => node.text().to_string(),
            SyntaxElement::Token(token) => token.text().to_owned(),
        };
        if text != "$tc" {
            return Err(Box::new(HandleError::Stale));
        }

        // 4. compile edits against this snapshot
        let mut builder = Builder::new(fix.document.source());
        builder.replace(&element, payload.replacement)?;
        let edits = builder.finish()?;

        // 5. return a versioned plan for the fix document
        Ok(single_document_plan(fix, edits))
    }
}
```

`resolve_anchor` (`crates/lsp-inspections/src/inspection.rs`) calls
`ElementHandle::resolve` with the fix document's URI, version, language, and
tree. `single_document_plan` builds one `DocumentPlan` with
`Some(document.version)` and reuses the document's line index.

Step 3 is the one people skip. `ElementHandle::resolve` proves the *element* is
unchanged, but a fix usually also depends on a *semantic* assumption. Re-check
it and return `HandleError::Stale` if it no longer holds — that turns a wrong edit
into a graceful "request code actions again".

Also note: **always pass the version.** A versioned document edit lets the
editor reject the change if the buffer moved between response and application.
Omit it only for files the same plan creates.

## Language-aware rewrites: the PHP editor

Byte edits are the right primitive, but "add an interface to this class" should
not be written by hand at every call site. `swlsp-php-rewrite` provides a
composable editor over the PHP CST:

```rust
// crates/php-rewrite/src/editor.rs
let mut editor = Editor::new(source, Some(&root));
// ... schedule several related changes ...
let edits = editor.finish()?;
```

Available operations:

| Area | Methods |
| --- | --- |
| Imports and references | `class_reference(class_name)` |
| Class shape | `set_extends`, `remove_extends`, `add_implements`, `remove_implements`, `add_attribute` |
| Members | `insert_class_member`, `remove_class_member` |
| Lists | `insert_argument`, `append_argument`, `remove_argument`, `insert_parameter`, `append_parameter`, `remove_parameter` |
| Types | `set_parameter_type`, `set_property_type`, `set_return_type` |
| PHPDoc | `find_phpdoc_annotation`, `replace_phpdoc_annotation`, `remove_phpdoc_annotation` |
| Raw | `replace_range`, `insert`, `delete` |

Two of these deserve highlighting because they encode judgment you would
otherwise re-derive:

**`class_reference(class_name)`** returns the shortest unambiguous reference to a
class *and* schedules a `use` declaration when one is useful. If an existing
alias would conflict, it falls back to a fully-qualified reference rather than
rewriting someone's imports:

```rust
/// The shortest unambiguous reference to `class_name`, scheduling a use
/// declaration when one is useful. Conflicting aliases fall back to a
/// fully qualified reference rather than changing existing imports.
pub fn class_reference(&mut self, class_name: &str) -> Result<String>
```

**`insert_class_member`** inserts before the closing brace and re-indents every
non-empty line to match the surrounding class body, so generated members do not
arrive with the generator's indentation.

Both compose: schedule an import, an attribute, and a member, then `finish()`
once. The insertion-coalescing rule in `Builder` is what makes several
independent operations at the same offset behave.

When you need a Twig, XML, or YAML rewrite, use `swlsp_rewrite::Builder` directly with
ranges from that language's `query` module. Only PHP currently has enough
recurring rewrite shapes to justify a dedicated editor.

## Rename

`textDocument/rename` uses **first-match** dispatch, not fan-out:

```rust
// crates/lsp/src/features.rs
for provider in &registry.rename {
    let edit = provider
        .rename(&ctx, &request)
        .map_err(|error| provider_error("rename", provider.provider_name(), error))?;
    if edit.is_some() {
        return Ok(edit);          // first provider that answers wins
    }
}
Ok(None)
```

That differs from completion and definition deliberately: concatenating two
providers' rename edits would produce a conflicting or double-applied refactor.
A rename provider must therefore be confident about ownership — return `None`
unless the symbol under the cursor is clearly yours.

Rename providers live in `crates/lsp-refactor/src`:

- `php_twig_rename.rs` — a PHP symbol whose name also appears in Twig
- `twig_template_rename.rs` — template paths and block names
- `admin_rename.rs` — Administration components and their references

Cross-language rename is exactly why rewrite plans are workspace-scoped:
renaming a Twig block touches every template that extends or overrides it, and
all of those edits must land as one workspace edit or none.

## File rename

`workspace/willRenameFiles` lets the server update references *before* the
editor moves a file:

```rust
// crates/lsp/src/providers.rs
pub struct FileRenameRequest {
    pub params: protocol::RenameFilesParams,
    pub documents: Vec<Arc<TextDocument>>,   // every open document
}

pub trait FileRenameProvider: Send + Sync {
    fn will_rename_files(&self, ctx: &Context, request: &FileRenameRequest)
        -> ProviderResult<Option<protocol::WorkspaceEdit>>;
}
```

Unlike symbol rename this **is** a fan-out: every provider contributes, and
their `changes` maps are merged per URI. Different providers own different
reference kinds — a moved Twig template affects `extends`/`include` paths, a
moved PHP class affects imports — and those are independent.

The request carries all open documents because unsaved buffers must be updated
too, not just indexed files.

## Scaffolding

`swlsp-lsp-scaffold` generates new code — entities, plugins, DAL
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

```rust
// crates/lsp/src/inspection.rs
pub trait CommandQuickFix: Send + Sync {
    fn build_command(&self, ctx: &Context, fix: &FixContext)
        -> Result<Option<protocol::CommandAction>, BoxError>;
}
```

A `QuickFix` exposes it through `as_command()`, just as a rewrite fix exposes
`RewriteQuickFix` through `as_rewrite()`.

**Prefer edits.** A workspace edit works in every client. A command only works
in a client that implements it, and the server filters out actions whose
commands the client did not advertise — MCP in particular can apply workspace
edits but cannot execute editor-only commands. See
[`lsp-server.md`](lsp-server.md#gating-features-domains-client-profiles).

Also: **code actions must never write files directly.** Returning a workspace
edit is what lets the editor, the CLI (`codeaction --exec`), and MCP validate and
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
6. **Prefer lazy resolution** (`FixResolution::Lazy`) for non-trivial or cross-file
   rewrites, so element handles get a chance to reject a stale document instead
   of computing an edit nobody asked for.
7. **Compose through one builder or editor.** Do not concatenate strings and
   hope the offsets line up; let `finish()` catch the conflict.
8. **Return a reason, not a broken edit.** If the rewrite cannot be produced
   safely, fail — the caller turns that into a disabled action with an
   explanation.
9. **Reuse analyzers and rewrite helpers across migrations.** For
   version-migration inspections in particular, prefer a shared analyzer over
   copying one Rector rule verbatim into each inspection.

## Testing

Test a rewrite at three levels.

**The edit itself** — apply the plan and compare source:

```rust
let plan = fix.build(&Context::background(), &fix_context).unwrap();
assert_eq!(plan.documents.len(), 1);

let updated = plan.documents[0].apply().unwrap();
assert_eq!(updated, expected_source);
```

**Staleness** — build the fix against one document version, then resolve against
a newer one and assert a stale `HandleError` (`is_stale()`). This is the regression that matters
most, because it only manifests as a corrupted file in production.

**Protocol shape** — a server-level test that the action is offered, that
`codeAction/resolve` produces the edit for a lazy fix, and that the ranges are
correct UTF-16. Use a real `TextDocument` so the conversion is actually
exercised; a hand-built range skips the most error-prone step.

Also assert the **validation** path: a fix that would introduce parser errors
must be rejected. `crates/rewrite/src/tests.rs` covers builder conflicts,
coalescing, and `apply`; `crates/php-rewrite/src/tests` covers the PHP editor
operations; `swlsp-lsp-codeaction` and `swlsp-lsp-inspections` cover
presentation and end-to-end fix behavior.

```bash
cargo test -p swlsp-rewrite -p swlsp-php-rewrite
cargo test -p swlsp-lsp-inspections -p swlsp-lsp-codeaction -p swlsp-lsp-refactor
shopware-lsp --root /path/to/project codeaction --kind quickfix src/Controller.php:24:18
shopware-lsp --root /path/to/project rename -d src/Controller.php:24:18 NewName
```

The last two print a unified diff without writing, which is the fastest way to
eyeball a rewrite against a real project.

## Further reading

- [`diagnostics-pipeline.md`](diagnostics-pipeline.md) — where quick fixes are
  bound and resolved
- [`parser-architecture.md`](parser-architecture.md) — elements, ranges, and
  `range_trimmed_trivia`
- [`lsp-server.md`](lsp-server.md) — command filtering and client capabilities
- [`php-semantic-engine.md`](php-semantic-engine.md) — the semantic information
  PHP rewrites depend on
- [`shopware-rector-parity.md`](shopware-rector-parity.md) — the migration
  rewrites and their Rector counterparts

## Interactive Twig and snippet commands

The Storefront Twig block extension and snippet creation commands return an
`edit` containing a validated workspace edit. They resolve open target documents
before disk, retain document versions, and build all changes before returning.
Snippet discovery only suggests paths; it never creates files. The VS Code
client checks target versions again immediately before applying the returned edit
and retains compatibility with older custom
servers that apply the command themselves and return no edit.

Command adapters live in `swlsp-lsp-commands`; domain indexes expose plain Rust
APIs. The same server snapshot resolver and plan validator serve these commands
and rewrite quick fixes.

### Snapshot validation for action adapters

Inspection actions recheck the effective diagnostic policy when listed and when
resolved. Disabling a rule, inspection, domain, or client presentation removes
its cached fixes as well. Document resolution checks workspace containment
before consulting open buffers.

Legacy text-edit providers pass through the same workspace-plan validator. The
server adds open-document versions and source fingerprints; resolving an action
rejects changed targets, including closed files changed on disk. New resource
operations belong in typed rewrite fixes or command plans.

Administration Twig overrides return one validated workspace edit for the
component script, template, and entry point. Translation extraction lists locale
targets first, then builds the final plan after selection, using the unchanged
source document and the latest target snapshots. Missing-translation diagnostics
bind indexed locale metadata without reading or parsing resource files; only the
selected fix computes an insertion. Existing keys in unsaved targets are rejected.
