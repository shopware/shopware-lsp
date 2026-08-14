# PhpStorm LSP integration guide

This document is the integration contract for replacing the Shopware-specific
analysis in `shopware6-phpstorm-plugin` with Shopware LSP. The PhpStorm plugin
should become a thin process and UI adapter. Indexing, parsing, framework
analysis, diagnostics, navigation, rewriting, and generators remain in the Go
server shared by VS Code, the CLI, and MCP.

The implementation described here targets PhpStorm 2026.2. JetBrains documents
the current API under
[`LspIntegrationProvider` and `LspClientDescriptor`](https://plugins.jetbrains.com/docs/intellij/language-server-protocol.html).
The plugin must depend on `com.intellij.modules.lsp` and
`com.intellij.modules.ultimate` and register
`com.intellij.platform.lsp.integrationProvider`.

## Ownership boundary

PhpStorm continues to own base PHP, JavaScript/TypeScript, Twig, XML, YAML,
JSON, Vue, and SCSS editing. Shopware LSP owns Shopware and Symfony knowledge
that depends on workspace indexes or cross-language analysis.

The adapter should retain only:

- executable download/discovery, settings, process status, logs, restart, and
  unsupported-project UI;
- editor prompts, pickers, diff presentation, and navigation requested by a
  server-returned client command;
- application of validated `WorkspaceEdit` responses, including file creates;
- the entity-schema designer and scaffold dialogs, driven entirely by server
  catalogs and requests;
- IDE-only project wizard, live-template, and file-template features until a
  separate product decision removes them.

Do not retain an independent Shopware index, parser, inspection, completion,
navigation, symbol, or rewrite path in Kotlin.

## Process and binary lifecycle

Start one `shopware-lsp` process per PhpStorm project root and communicate over
stdin/stdout. Do not pass `-stdio`; the default command starts the LSP
transport. Multi-root support requires one process per root.

Before starting, use the same bounded project markers as the server: Shopware
Composer metadata, Shopware app manifests, Symfony FrameworkBundle evidence,
or committed `.config/shopware/lsp.json`. The server still performs its own
check. An unsupported editor project returns a successful initialize response
with:

```json
{
  "capabilities": {
    "experimental": {
      "shopwareLSP": {
        "active": false,
        "reason": "unsupportedProject",
        "protocolVersion": 1,
        "presentationProfile": "framework"
      }
    }
  }
}
```

Treat that state as inactive rather than a crashed server. Do not repeatedly
restart it when unrelated PHP files are opened.

Use a user-configured executable path when present. Otherwise download the
pinned Shopware LSP release asset matching the current OS and architecture,
verify it against the release `checksums.txt`, store it in an IDE-managed
versioned cache, and launch only after verification. JetBrains Marketplace
cannot publish different plugin distributions per OS, so downloading the
platform binary is preferable to bundling every CGO build in one plugin
([JetBrains plugin content documentation](https://plugins.jetbrains.com/docs/intellij/plugin-content.html)).
If no release asset exists for a platform, show a clear unsupported-target
message and allow selecting a custom executable.

## Initialization contract

Send the ordinary root URI and client capabilities plus the following
Shopware options:

```json
{
  "initializationOptions": {
    "configuration": {},
    "allowUnsupportedProject": false,
    "shopwareClient": {
      "protocolVersion": 1,
      "presentationProfile": "framework",
      "supportedCommands": [
        "shopware.openReferences",
        "shopware.admin.extendComponent",
        "shopware.admin.overrideMethod",
        "shopware.admin.overrideTwigBlock",
        "shopware.twig.extendBlock",
        "shopware.twig.showBlockDiff"
      ]
    }
  }
}
```

`protocolVersion` must match the version returned in
`capabilities.experimental.shopwareLSP`. Reject a mismatched server before
using custom methods. `presentationProfile` must be `framework`; the default
`full` profile is intended for editors without their own PHP intelligence.

`supportedCommands` is an exact allow-list of editor-side commands implemented
by the adapter. The server removes command-backed presentations that the
client cannot execute. Fetch `shopware/integration/catalog` after initialize
and compare its `clientCommands` with the adapter registrations. The same
response contains the authoritative scaffold catalog.

Forward editor-local configuration in `initializationOptions.configuration`
and through `workspace/didChangeConfiguration`. Committed configuration stays
in `.config/shopware/lsp.json`; nested extension configuration remains
diagnostics-only. Use `shopware/configuration/catalog`, `/effective`, and
`/reload` for the settings UI.

## Supported files and lifecycle

The descriptor should accept project files with these extensions:

- `.php`, `.twig`, `.html`, `.js`, `.ts`, `.vue`, `.scss`, and `.json`;
- `.xml`, `.xlf`, `.xliff`, `.yaml`, and `.yml`.

Only start the process after a supported project marker is found. Forward
full-text `didOpen` and `didChange` snapshots with monotonically increasing
versions. Send `didClose`; the server then clears diagnostics and discards the
in-memory overlay. Do not add an IDE file watcher for indexing—the server's
filesystem watcher is authoritative.

Advertise `window.workDoneProgress` and present the server's standard
`$/progress` indexing state. The custom `shopware/indexingStarted`,
`shopware/indexingCompleted`, and `shopware/indexingFailed` notifications are
legacy-compatible fallbacks. On project disposal, send `shutdown`, then
`exit`, and terminate a process that does not exit within a bounded grace
period.

## Standard features and duplicate prevention

Register only the capabilities returned by initialize. The server derives
them from the providers enabled for the current project and presentation
profile; the adapter must not assume a fixed capability set.

The `framework` profile keeps internal PHP semantic snapshots but suppresses
their generic presentation. PhpStorm therefore remains responsible for plain
PHP completion, definitions, implementation and type hierarchy, references,
hover, signature help, core PHP diagnostics, embedded-language validation,
PHP-origin rename, built-in Twig test/operator completion, and SCSS color
previews. Shopware LSP still provides indexed and type-aware
Shopware/Symfony/Twig/Administration/DAL results returned by its remaining
providers.

An independently installed Symfony plugin may still produce overlapping
framework results. Shopware LSP cannot suppress another plugin. Prefer the LSP
and disable or remove overlapping legacy Shopware/Symfony bridge registrations
in the adapter.

## Commands and workspace edits

Use standard `workspace/executeCommand` for server-side commands advertised in
`executeCommandProvider.commands`. Shopware commands accept zero or one JSON
object in `arguments`. Direct custom requests remain available for older
clients and for typed LSP4J interfaces. `shopware/commands` returns the current
server-side command IDs.

Editor-side dotted commands such as `shopware.twig.extendBlock` are different:
the server may place them in code actions, code lenses, completion items, or
inlay hints, and the adapter implements their prompts and UI. Their exact
positional arguments and surfaces come from `shopware/integration/catalog`.
After collecting user input, call the slash-style server methods named in this
document or discoverable through `shopware/commands`.

Workspace edits can contain both `changes` and ordered `documentChanges`.
Support text-document edits and `create` operations, preserving optional
document versions. Apply the complete edit through the IDE write-command API;
never write returned content directly. If the edit is rejected or stale,
request a fresh action or preview instead of partially applying it.

Code actions with an `edit` can be applied directly. A command-only action is
shown only when its command was declared in `supportedCommands`. For lazy code
actions, call `codeAction/resolve` immediately before presentation or apply;
honor a returned `disabled` reason.

## Scaffolds and entity schemas

Build the "New Shopware File" UI from the `scaffolds` array returned by
`shopware/integration/catalog`. Do not duplicate the scaffold list in Kotlin.

- `workflow: "workspace-edit"` uses `shopware/scaffold/create` for Shopware
  artifacts or `shopware/symfony/scaffold/create` for Symfony artifacts. Pass
  `kind`, `directoryUri`, `name`, and catalog-described options. Apply the
  returned workspace edit and navigate to `primaryFileUri`.
- `workflow: "entity-schema"` uses the typed entity workflow below.

The entity workflow exposes schema-owning class-based definitions only.
Attribute definitions, attribute-only `SerializedField`, dynamic custom-entity
templates, and non-owning derived definition views are intentionally not
editable entity-schema targets.

Entity creation or editing must follow this sequence:

1. `shopware/entity-schema/bootstrap` for the selected plugin directory.
2. Optionally `search` for association targets and `load` for an existing
   definition.
3. Edit the typed specification in the designer. When a returned field type
   has a `template`, clone that template to add the Shopware-specific field;
   do not reconstruct its implementation metadata.
   Use `definitionKind: "mapping"` for `MappingEntityDefinition`; mapping mode
   has no entity/collection classes or implicit timestamp fields and may use
   standalone foreign keys as a composite primary key.
   Use `definitionKind: "extension"` for `EntityExtension`, select an indexed
   `extendedDefinitionClass`, and retain its matching technical `entityName`.
   Preserve the selected target's returned `fields` as `extendedFields` so
   index controls can combine an extension-owned column with verified target
   columns. The server rehydrates this metadata from the index before
   validation; an extension index must contain at least one owned column.
   Shopware accepts only associations, reference-version fields, runtime
   fields, and a foreign key paired with its association from the same
   extension. Model persisted extension columns as a `many-to-one` or
   `one-to-one` row; standalone persisted scalars and foreign keys are invalid.
   Use `definitionKind: "bulk-extension"` for `BulkEntityExtension`. Keep
   top-level `fields`, `indexes`, `extendedDefinitionClass`, and `entityName`
   empty; create one `bulkExtensions` entry per indexed target with its own
   `entityName`, `extendedDefinitionClass`, rehydrated `extendedFields`,
   `fields`, and `indexes`. Apply the same EntityExtension field and index
   validity rules independently to every target.
   Represent a parent/children tree as one field with `kind: "hierarchy"`.
   Do not add separate parent FK or reference-version fields: the server owns
   the native three-field DAL bundle and derives version pairing automatically.
   For product-style variants set `inheritanceAware: true` and keep that
   hierarchy. Use the typed `inherited`, `associationInherited`,
   `translationInherited`, and `reverseInheritedProperty` members; do not add
   raw `Inherited` or `ReverseInherited` expressions to preserved flags.
   Use `definitionBehavior.parentDefinitionClass` for aggregate definitions,
   the optional `versionAware` boolean only when the class explicitly
   overrides framework inference, and `overrideDefaultFields` when the class
   owns `defaultFields()`. An empty `defaultFields` list disables the normal
   implicit timestamps. `overrideBaseFields` and `baseFields` represent a
   literal `getBaseFields()` override; these fields participate in generated
   entity accessors, indexed relation metadata, snapshots, and migrations.
   `restrictDeleteMetaProperties` represents the literal metadata-property
   filter.
   Use `definitionMetadata` for `since()`, `getDefaults()`,
   `getChildDefaults()`, and `getHydratorClass()`; translation-specific values
   live below `translation.definitionBehavior` and
   `translation.definitionMetadata`.
   Retain loaded `conditionalAssociation`, `apiAwareSources`, behavior,
   metadata, and JSON mapping/default members. They are semantic source data,
   not disposable presentation hints.
   For a returned `kind: "enum"` field retain `enumClass`, `enumCase`, and
   `enumBackingType`; the server verifies the indexed backed enum and owns its
   PHP and migration representation.
   Retain every loaded property ending in `MethodRaw` byte-for-byte. It marks
   a non-literal PHP method that the server preserves but intentionally does
   not allow clients to synthesize or edit.
4. Call `preview`; show validation, rename, drift, and destructive-change
   questions and repeat preview only after the specification or decisions
   change. Answer `entityRenameQuestions` with `entityRename` plus the exact
   old/new table names, or `entityCreate` when the table is intentionally new;
   never silently translate a technical-name change into drop-and-create.
5. When preview returns `ready`, call `apply` once with the identical spec and
   exact opaque revision. Do not refresh the preview timestamp or revision.
6. Use `reconcile` only for divergent committed snapshot history, then restart
   at bootstrap.

The adapter must not generate entity PHP, services, migration, or snapshot
files itself. Mapping preview/apply returns only the definition, service,
migration, and snapshot changes. A field with `translated: true` is still part
of the parent specification: retain the returned `translation` companion
metadata and let the server preview/apply the complete six-class DAL bundle
and both database tables atomically. Extension preview/apply returns the
extension class, `shopware.entity.extension` service registration, and a
snapshot. Paired to-one foreign keys and reference-version fields alter the
target table; scalar extension fields must be runtime, and association-only
extensions intentionally produce no migration. Bulk-extension preview/apply
returns one class registered with `shopware.bulk.entity.extension` and combines
all of its target-table contributions in the same migration and snapshot.

## Native plugin migration checklist

Replace and then remove these Shopware-specific native registrations:

| Native plugin area | LSP replacement |
|---|---|
| Snippet, feature flag, bundle, DAL, app, script, hook, theme, system-config, Twig block, and Administration indexes | Shared server indexes and workspace symbols |
| PHP, Twig, and Administration completion contributors | `textDocument/completion` in framework profile |
| Shopware/Symfony goto declaration and symbol handlers | Definition, references, workspace/document symbols, links, and hierarchy capabilities |
| Shopware inspections and intentions | Typed diagnostics, code actions, and lazy quick fixes |
| Administration component/override/module intelligence | Administration completion, navigation, diagnostics, references, symbols, hierarchy, folding, semantic tokens, and client commands |
| Twig block version/hash inspection and diff actions | Twig versioning diagnostics, hover, code actions, and diff command |
| PHPUnit/Shopware inlays, line markers, and related navigation | Inlay hints and code lenses |
| Plugin, configuration, task, migration, app, Administration, CMS, Symfony, and DAL generators | Integration catalog, scaffold requests, and entity-schema workflow |

Remove the old FileBasedIndex implementations and provider extension points
only after representative results have been verified through the LSP. Keep no
fallback that silently re-enables native analysis, because it recreates the
duplicate-result problem and allows the two implementations to drift.

## Adapter acceptance tests

The PhpStorm adapter should verify:

- supported and unsupported project startup, one process per project, restart,
  shutdown, and binary checksum failure;
- framework initialization and protocol mismatch handling;
- unsaved document diagnostics and clearing on close;
- completion, definition, references, hover, symbols, code actions, lazy
  resolution, inlays, code lenses, and indexing progress;
- UTF-16 positions around multibyte source text;
- `changes`, `documentChanges`, and create-file workspace edits;
- every declared client command and its cancellation path;
- scaffold generation and the complete entity preview/apply workflow;
- absence of generic PHP diagnostics, symbols, implementation, hierarchy, and
  SCSS color duplication when using the framework profile.

Run these against a small fixture in CI and an opt-in `sw-trunk` project before
removing the corresponding native extension points.
