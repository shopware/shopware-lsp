# Shopware Language Server Reference

This document preserves the exhaustive feature, command, configuration,
architecture, and performance reference for Shopware Language Server. For the
user-focused introduction and installation guide, start with the
[project README](../README.md).

Editor authors can use the versioned integration contract and framework-only
presentation profile documented in the
[PhpStorm LSP integration guide](phpstorm-integration.md). The profile
keeps Shopware and Symfony intelligence in the server while leaving generic
PHP presentation to the host IDE.

## Command-line interface

Running `shopware-lsp` without a command keeps the editor-compatible behavior
and starts the language server on stdin/stdout. The same production workspace,
indexes, providers, diagnostics, and refactoring engine are also available from
the terminal:

```bash
shopware-lsp help
shopware-lsp version
shopware-lsp --root /path/to/project --json info
shopware-lsp --root /path/to/project index
shopware-lsp --root /path/to/project index --force
shopware-lsp --root /path/to/project info --stats
shopware-lsp cache
shopware-lsp --root /path/to/project cache clear
shopware-lsp cache clear --stale
shopware-lsp --root /path/to/project check src tests
shopware-lsp --root /path/to/project check --fail-on error src/Controller.php
shopware-lsp --root /path/to/project lsp definition src/Controller.php:24:18
shopware-lsp --root /path/to/project lsp references src/Controller.php:24:18
shopware-lsp --root /path/to/project lsp workspace-symbol customer.detail
shopware-lsp --root /path/to/project lsp workspace-symbol --fresh customer.detail
shopware-lsp --root /path/to/project lsp codeaction --kind quickfix src/Controller.php:24:18
shopware-lsp --root /path/to/project lsp codeaction --kind refactor.extract --end-line 30 --end-column 1 src/Controller.php:24:1
shopware-lsp --root /path/to/project lsp rename -d src/Controller.php:24:18 NewName
shopware-lsp --root /path/to/project mcp
```

Workspace commands run only for detected Shopware or Symfony roots. Detection
is intentionally bounded to root metadata: Shopware Composer metadata or app
manifest files, `symfony/framework-bundle` or `config/bundles.php`, and the
committed `.config/shopware-lsp.yaml` opt-in. Use `info` to see
the detected kind, its evidence and the configuration in effect; `info --stats`
adds index, cache and memory statistics, and `info --json` prints the complete
effective configuration. For an
unusual but intentional root, either add the project configuration or pass the
global `--allow-unsupported-project` flag. The language server applies
the same guard.

The MCP server always starts, because clients launch it for every project
they open and show an exit as a failed server. In a root without those
markers, or with a project or editor configuration that cannot be read, it
is inactive: it completes the handshake, offers no tools, gives the reason in
its instructions and on stderr (`Shopware MCP inactive: …`), and never indexes
the root. Only a root that does not exist, or a failing project detection,
still ends it.

The commands people and CI run are at the top level: `check`, `index`, `info`,
`cache`, `serve` and `mcp`. Everything that asks the language server what an editor
would ask (hover, definition, rename, code actions, raw requests) is below
`lsp`; `shopware-lsp lsp --help` lists the requests by what they take.
`info --project-only` only detects the project, without opening the workspace:
editors use it to decide whether to start the server.

`cache` lists the index caches of every project on this machine with their
size, when they were last written and their state; it opens no workspace, so
it also works when a cache does not. `cache clear` deletes the cache of the
current project, `cache clear --stale` those whose project is gone or that are
outdated or unidentified and were not written for a week, and
`cache clear --all` all of them; `--dry-run` shows what would go. A cache that
a running server or command has open is kept: stop it first, or rebuild the
index in place with `index --force`. Editors offer the same through
`shopware/cache/list` and `shopware/cache/clear` (VS Code: "Shopware: Clear
Index Cache…").

Positions are one-based `file:line:column` values. Structured feature commands
emit JSON. `check` and `info` default to readable text; pass the global `--json` flag
for machine-readable output. Global flags work before or after the command,
and `shopware-lsp help <command>` (or `<command> --help`) lists every flag.
Flags are GNU-style (`--root DIR`, `--root=DIR`, `-w`); the single-dash
spelling of earlier releases (`-root`) is rejected with a hint. `check` recursively
expands directory targets and accepts multiple, overlapping files or
directories. It uses up to four diagnostic workers by default; pass
`--workers 1` for serial analysis or another positive limit for a particular
machine.

On a terminal `check` groups the findings below their file and shows a live
status line while the workspace indexes and the files are checked; elsewhere
(a pipe, a CI log) it prints one `path:line:column: severity: message [code]`
line per finding and no status. Choose the layout explicitly with
`--format pretty` or `--format concise`: the concise lines are what terminals
and editors link to a position. Either way:

- paths are relative to the current directory when the file is below it,
  otherwise to the workspace root (`--json` keeps full URIs);
- findings are written to standard output as each file finishes, in path
  order, and the summary (counts per severity, files, duration) goes to
  standard error;
- the exit code is 1 when a finding reaches `--fail-on`, and the summary is
  the only message.

In a GitHub Actions job (`GITHUB_ACTIONS=true`) `check` prints
`--format github` unless a format is named: one workflow command per finding,
which GitHub shows as an annotation on the file and line in the pull request.

```yaml
- run: shopware-lsp check src
```

Errors and warnings keep their level, info and hint findings become notices,
the rule ID is the annotation title, and paths are relative to
`GITHUB_WORKSPACE`. The summary still goes to standard error and the exit code
is unchanged. GitHub shows a limited number of annotations per step (10 per
level) and per job (50); the job log lists every finding.

A finding that `check` can fix on its own ends in `[fixable]` (`"fixable": true`
in `--json`), and the summary counts them. `check --fix` applies those fixes to
the files and reports what is left; the exit code then reflects the remaining
findings only. A finding is fixable when the server offers exactly one
preferred quick fix for it and that fix edits nothing but the checked file.
Fixes that need a choice between alternatives, an editor command, or a change
to another file (creating a template, adding a service argument to
`services.xml`) are not applied: use `lsp codeaction` for them. The fixes of one
file are applied one after another, checking the file again after each, so
fixes that affect each other (two renamed references sharing an import) come
out right. Review the result with `git diff`.

`check --only RULE` narrows a run to particular rules, so one kind of problem
can be reviewed or fixed at a time:

```bash
shopware-lsp check --only php.unusedImport --fix src
shopware-lsp check --only 'twig.html.*' --only php.deprecated src
```

A value is a rule ID or a prefix ending in `.*`; the flag repeats and takes
comma-separated values. Only the selected rules are reported, count for the
exit code and are fixed by `--fix`; every other finding of the file stays
untouched. A value that matches no rule is an error. A selected rule is
reported whatever its severity unless `--severity` is given, and a rule that
is off unless configured is turned on for the run. The project configuration
still decides about a rule it mentions: one it sets to `off` stays off, and
the run says so. All rules are still analyzed, so a narrowed run is not
faster.

Colors follow the global `--color auto|always|never` flag: `auto` colors
terminals only, `NO_COLOR` turns colors off and `FORCE_COLOR` or
`CLICOLOR_FORCE` turn them on. The status line also appears for `index` and
for the other workspace commands while they wait for the index; `-v` replaces
it with plain progress lines.

`lsp codeaction --exec` and `lsp rename` preview a unified diff unless `-w` is
supplied, so commands do not modify source files by default.

`lsp workspace-symbol` normally reads the populated SQLite FTS catalog directly,
without restoring the PHP semantic graph or rescanning the workspace. Use
`--fresh` when a one-shot command must validate the filesystem through the
full production LSP session first. Textual relevance is ranked before explicit
symbol tiers; equally relevant PHP types rank above PHP globals, framework
objects, PHP members, and framework members.

The CLI exposes completion, definition, implementation, references, hover,
signature help, call/type hierarchy, document and workspace symbols,
highlights, folding ranges, links, semantic tokens, inlay hints, code lenses,
selection ranges, linked editing, colors, code actions, and rename. Use
`shopware-lsp lsp execute` to list the registered `shopware/...` analytics and
generator requests, or `shopware-lsp lsp request` as a generic JSON-RPC escape
hatch.

Operational flags include `--profile-cpu`, `--profile-mem`, and
`--profile-trace`. `serve` additionally supports `--logfile`, `--rpc-trace`, a
runtime profiling endpoint through `--debug`, and a single-client TCP or Unix
socket through `--listen`.

### MCP server

The `mcp` command exposes the production workspace index, diagnostics, and
refactoring engine to AI clients over MCP's stdin/stdout transport. For
example, an MCP client configuration can launch one server per workspace:

```json
{
  "mcpServers": {
    "shopware": {
      "command": "/absolute/path/to/shopware-lsp",
      "args": ["--root", "/absolute/path/to/project", "mcp"]
    }
  }
}
```

The server advertises diagnostics, code actions, hover, definitions,
references, and workspace-symbol search. `shopware_explain_diagnostic` takes a
diagnostic code or an inspection ID and returns what the rule reports, how to
fix a finding, what the rule deliberately leaves alone, how to opt out in the
code, and how to configure or suppress it: the explanations of the
[inspection reference](inspections.md), so an agent does not have to guess
from a one-line message. It also exposes the production
Shopware/Symfony scaffold generators through `shopware_scaffold_catalog` and
`shopware_scaffold`. A scaffold call previews a unified diff by default; pass
`write: true` to create its validated files. The typed DAL entity workflow is
available through `shopware_entity_schema_bootstrap`, `_field_types`,
`_search`, `_load`, `_preview`, `_apply`, and `_reconcile`. Bootstrap keeps its
result inline by returning compact field summaries and omitting relation rows.
Call `_field_types` without an ID to filter or list available kinds, or with an
exact returned ID to obtain one copyable template; agents must not inspect
editor-generated `content.json` payloads. Apply requires the exact preview
revision, and destructive migrations additionally require
`allowDestructive: true`. Loaded definitions retain typed behavior
and metadata flags, scoped `ApiAware` sources, `JsonField` property mappings
and defaults, and version-gated to-one association shapes rather than
flattening them into raw PHP.

Indexing is lazy on the first analysis or scaffold call and the resulting
workspace session is reused. Tool paths may be absolute or workspace-relative;
positions and returned ranges are one-based. Every returned workspace edit is
validated against the workspace root. Read and preview tools never modify the
workspace. Applying a code action requires an exact title returned by
`shopware_code_actions`, writes the affected files, and returns a unified diff.

The bundled VS Code extension runs one language-server and registers one MCP
definition for each effective supported workspace folder; manual JSON
configuration is only needed for other MCP clients. In overlapping multi-root
workspaces, the outermost supported folder owns its descendants so the same
files are never indexed twice. A supported descendant becomes active when its
parent is disabled or unsupported. Set
`shopwareLSP.mcp.enabled` to `false` to opt a workspace folder out. Individual
tools can be disabled locally with `shopwareLSP.mcp.tools`, or for every client
through the committed `mcp.tools` project configuration. Tool keys are the
exact public names shown by MCP `tools/list`; unknown names are rejected.

## Features

### Symfony Service Support
- Service ID completion in PHP, XML, and YAML files
- Navigation to service definitions from PHP, XML, and YAML
- PHP class code lenses for service definitions and public-constructor code
  lenses for effectively autowired services, including defaults, parent
  inheritance, and PSR-4 prototypes
- Parameter reference completion and navigation in XML files
- Service tag completion in XML files
- Service class completion in XML and YAML files
- Tag-based service lookup and navigation
- YAML service configuration support with `@service` reference completion
- Exact-edit YAML service-option completion in block/flow definitions and
  `_instanceof` rules, excluding already configured keys and marking legacy
  factory/scope/autowiring settings deprecated
- YAML scalar keyword and standard-tag completion, config-aware
  `!php/const`, Symfony 6.2+ `!php/enum`, and Composer-version-aware service
  argument tags such as `!tagged_iterator`, `!tagged_locator`, and
  `!service_locator`
- Native PHP configurator indexing for `set`, `alias`, `remove`, `load` /
  `exclude` prototypes, parameters, defaults, and inheritance-aware
  `instanceof` tags
- PHP array service `arguments` and nested `calls` provide percent-wrapped
  parameter completion, definition navigation, and missing-reference
  diagnostics; array `decorates`, `parent`, service, and tag values share the
  normal container intelligence, while configured call/factory methods resolve
  through explicit classes or service IDs
- PHP configurator completion/navigation for `service()`, `param()`,
  `tagged_iterator()`, `tagged_locator()`, and `::class` references
- PHPDoc `#Service` and `#Parameter` assistant contracts on function,
  constructor, or method parameters provide exact container-name completion and
  definition navigation plus missing-reference diagnostics and typo fixes at
  matching call arguments, including inherited interface declarations
- Service completion, navigation, and missing-reference diagnostics for
  `AutowireLocator`, `AutowireServiceClosure`, `AutowireMethodOf`, and
  `AutowireCallable`; callable methods resolve service aliases or `::class`
  receivers and expose only public instance methods
- Tag completion and navigation for PHP `tagged_iterator()` /
  `tagged_locator()` helpers and `TaggedIterator`, `TaggedLocator`,
  `Autoconfigure`, and `AutoconfigureTag` attributes
- Deployment-environment completion for the Symfony `When` attribute
- Persistent PSR-4 prototype expansion from PHP, XML, and YAML resource/exclude
  declarations, with prototype-to-class navigation
- Deprecated service metadata from XML/YAML/PHP definitions and compiled
  containers, with deprecated completion items and hint diagnostics for
  services or their PHP classes
- XML/YAML service-tag contract diagnostics for conventional tags such as
  `twig.extension`, `security.voter`, and
  `kernel.event_subscriber`, including modern/legacy interface alternatives
- Symfony XML/YAML container-constant and enum completion, PHP
  go-to-definition, and missing-reference diagnostics for global, inherited
  class, and enum constants/cases, including modern and legacy `!php/const`,
  `!php/enum`, and backed-enum `->value` YAML syntax
- Typed PHP `ParameterBagInterface` and `ContainerBagInterface` `get()` /
  `has()` calls use container-parameter completion, navigation, and missing
  diagnostics without being misclassified as service-container access
- Typed `ContainerInterface::getParameter()` / `hasParameter()` reads and
  `ParameterBagInterface` / `ParametersConfigurator::set()` calls provide
  parameter completion and navigation; missing diagnostics remain read-only
- YAML named constructor-argument completion and PHP declaration navigation,
  with inherited-constructor support, exact invalid-key diagnostics, and typo
  replacement actions; name-only and typed `_defaults.bind` keys navigate to
  matching constructor parameters across explicit and prototype services
- XML/YAML positional constructor and configured-method argument inlay hints,
  showing the mapped PHP parameter name or object type across inherited methods
- Clickable resolved route-controller inlay hints for YAML/XML controller
  literals and PHP route declarations, including service aliases and invokable
  controllers
- YAML `calls`, tag callbacks, modern tuple/string factories, and legacy
  factory fields plus XML `<call>`, `<factory>`, and legacy factory attributes
  complete inherited public PHP methods with exact edits and navigate to their
  declarations
- Constructor and configured-method service-reference type validation across
  XML, YAML, and PHP configurator/legacy-array definitions, with interface,
  inheritance, nullable/union support, unsaved local definitions, and
  syntax-preserving compatible-service replacement actions
- Missing configured XML/YAML call, callback, and factory-method diagnostics
  resolve the actual receiver and offer typo replacement or cross-file
  create-method actions
- Interactive Symfony service-definition generation from PHP classes in YAML,
  XML, PHP fluent-configurator, and PHP-array formats, with inferred
  constructor/setter service arguments and conventional tags
- Multi-class service-definition analytics reuse the same four renderers and
  report up to 15 compatible service IDs for every ambiguous constructor or
  setter parameter, both as structured data and format-correct comments;
  `Symfony: Generate Service Definitions…` opens the combined result
- Interactive compiler-pass creation for Symfony bundle classes, including an
  import-aware `build()` registration edit and a new
  `DependencyInjection/Compiler/*Pass.php` implementation
- Forward and reverse service code lenses for `decorates` and `parent`
  relationships across PHP, XML, and YAML definitions
- Structured service location by ID or PHP class correlates aliases, resolved
  classes, decorators, parents, tags, deprecations, explicit definitions,
  compiled metadata, and PSR-4 prototype origins. It returns bounded source
  previews with exact locations; `Symfony: Locate Service…` exposes the
  multi-definition navigator

### Routing
- Exact-edit YAML route-option completion in block and flow declarations,
  excluding already configured keys and marking legacy `pattern` deprecated;
  `requirements` keys are completed from sibling path placeholders, while
  `path` values offer indexed route patterns with route-name details
- Indexed route-pattern completion in positional or named PHP `#[Route]`
  paths and legacy `@Route` docblock paths, preserving the surrounding quotes
- Conventional route-name suggestions in native and legacy PHP route metadata,
  derived from controller namespaces, class names, and action methods
- PHPDoc `#Route` assistant contracts on function, constructor, or method
  parameters provide exact route-name completion, definition navigation,
  missing-reference diagnostics, and typo fixes at matching call arguments,
  including inherited interface declarations
- Contextual PHP refactor action to add an import-aware `#[Route]` attribute to
  public controller methods, with a generated path and route name
- Go-to-definition from intermediate segments of positional or named PHP
  `#[Route]` paths to indexed parent and descendant routes sharing that prefix
- Route completion, navigation, hover, diagnostics, typo fixes, and Find
  References in exact Twig `_route` equality, `same as`, and membership
  comparisons; prefix checks remain intentionally unvalidated
- Reverse route lookup from static Twig anchor/form URLs and project symbols,
  including absolute/protocol-relative URLs, query/fragment normalization,
  concrete placeholder segments, and useful partial request paths
- Route-path completion and reverse navigation in JavaScript/TypeScript
  `fetch()`, Axios, `Request()`, `url`, and Axios `baseURL` contexts
- Portable Symfony Endpoints support: route workspace symbols include the
  normalized HTTP method, path, and controller, while PHP, YAML, and XML route
  declarations show inline endpoint code lenses that navigate to the resolved
  controller action; PHP `Request::METHOD_*` constants are evaluated natively
- Workspace symbols include Symfony services and container parameters, routes
  and concrete route URLs, controller actions, commands, Twig
  templates/blocks/macros/extensions, Twig/Live
  Components, and translation domains/keys
- A structured route analytics request correlates route sources, normalized
  methods and URLs, resolved controller locations, and rendered Twig
  templates, with route/controller/URL/Ant-glob filters; `Symfony: Browse
  Routes…` exposes the catalog as a searchable command-palette navigator
- Local Symfony profiler analytics discover modern `var/cache/*/profiler` and
  legacy `app/cache/*/profiler` indexes, read bounded raw or gzip profile data,
  and correlate recent requests with routes, controller locations, static and
  runtime Twig templates, rendered Symfony UX Twig
  Components, and sent-mail subjects. Runtime components navigate to their PHP
  class or anonymous template; mail results open the matching profiler mail
  panel when an absolute profiler URL is available. URL/hash/controller/route
  filters are available through the structured request; `Symfony: Browse
  Profiler Requests…` provides a searchable, filesystem-only editor surface
- Routing-resource navigation and code lenses for scalar or
  `{ path, namespace }` YAML imports, XML `<import>` elements, and typed PHP
  `RoutingConfigurator::import()` calls; direct files, recursive
  attribute/annotation directories, `**` globs, and brace alternatives resolve
  to exact matching files, while imported controller/route files link back to
  every persisted import declaration; legacy `@Bundle/...` paths resolve
  through the cached PHP `BundleInterface` subtype graph
- Resource-value completion in block/flow YAML, nested YAML `resource.path`,
  XML imports, and typed PHP configurator imports; suggestions include files
  and folders relative to the current config plus files under each bundle's
  legacy/modern config and controller directories

### Symfony Configuration

- Persistent configuration-root discovery from modern and legacy
  `getConfigTreeBuilder()` implementations
- Root-key completion, exact PHP declaration navigation, and related code
  lenses in top-level and `when@environment` PHP arrays and YAML mappings
- Bundle and directory-scoped `resource` completion for YAML/XML configuration
  imports, preserving quoted and incomplete values with exact replacement edits
- Go-to-definition and related code lenses for relative or globbed PHP
  `imports[].resource` and YAML `resource:` files

### Symfony Project Scaffolding
- `Symfony: New File…` is available from the command palette and folder
  context menu for Console commands, controllers, Twig extensions,
  compiler passes, kernel tests, web tests, and YAML/XML/PHP service
  configuration files
- PHP namespaces are derived from the most-specific Composer PSR-4 mapping;
  command names reuse indexed project prefixes, and Symfony/Twig feature
  detection selects invokable, attribute, property, or legacy templates
- Generated PHP, YAML, and XML is checked by the native parsers, target
  collisions and workspace escapes are rejected, and the editor applies each
  file as an atomic workspace edit

### Shopware DAL Entity Designer

- `Shopware: New File…` → `DAL Entity / Mapping / Extensions` opens a visual
  designer for new and indexed plugin entities, mapping definitions, entity
  extensions, bulk entity extensions, ordered fields, relations, and indexes
- One preview drives the definition, entity, collection, service registration,
  Shopware migration, and a full-plugin schema snapshot committed below
  `src/Resources/shopware-lsp/schema/`
- Existing definitions are imported through the native PHP CST. Unknown field
  expressions are locked and preserved; custom definition/entity members are
  retained, and customized managed accessors stop the rewrite safely
- The designer targets schema-owning class-based definitions. Shopware's
  attribute-generated definitions, attribute-only `SerializedField`, dynamic
  custom-entity templates, and non-owning derived sales-channel definition
  views are deliberately outside this workflow
- Class-level definition behavior is typed as well: aggregate
  `getParentDefinitionClass()` links, explicit `isVersionAware()` overrides,
  literal `defaultFields()` and `getBaseFields()` overrides, and
  restrict-delete metadata property sets round-trip through the designer.
  Base fields feed entity accessors, relation metadata, snapshots, and
  migrations; an empty default-field override intentionally disables the
  framework's implicit timestamp fields
- Definition `since()`, `getDefaults()`, `getChildDefaults()`, and
  `getHydratorClass()` hooks are editable for parent and applicable translation
  definitions. Non-literal implementations are shown as locked methods and
  must be preserved exactly by VS Code, MCP, and other clients
- Mapping mode generates a `MappingEntityDefinition`, service registration,
  join table migration, and snapshot without creating entity or collection
  classes. Standalone or associated foreign keys can form composite primary
  keys. Unlike normal definitions, mapping definitions have no implicit DAL
  timestamps, but explicit created/updated timestamp fields remain available
- Extension mode targets an indexed definition and generates an
  `EntityExtension` registered with `shopware.entity.extension`. Shopware only
  accepts associations, reference-version fields, runtime fields, and foreign
  keys paired with an association from the same extension. To-one relations
  therefore migrate their paired foreign-key columns with `ALTER TABLE`, while
  scalar additions are runtime and association-only extensions generate no SQL. The
  generated compatibility methods follow the plugin's Shopware target: 6.7+
  uses `getEntityName()`, while older targets also receive
  `getDefinitionClass()`. Extension indexes can combine contributed columns
  with verified physical columns from the indexed target; each index must own
  at least one contributed foreign-key/reference-version column so multiple
  extensions remain separable
- Bulk-extension mode generates one `BulkEntityExtension` registered with
  `shopware.bulk.entity.extension`. It manages any number of indexed targets;
  every target keeps an independent field list, verified target metadata, and
  owned indexes. The preview merges all target-table changes into one atomic
  migration and snapshot, while association-only targets intentionally produce
  no SQL
- Existing `EntityExtension` classes round-trip structured `extendFields()`,
  all supported literal `modifyFields()` flag additions/removals, and
  `extendProtections()`. Custom method bodies are preserved as locked source
  instead of being partially rewritten
- The field model covers scalar, backed PHP enums, JSON/list/object/blob, automatically
  persisted DAL timestamps, auto-increment and version fields, primary flags,
  reference versions, and owning or inverse to-one/to-many associations.
  Version-aware relations emit composite foreign keys and indexes automatically
- Shopware 6.6.10+ `EnumField` declarations retain the constructor enum case,
  resolve the enum's `string` or `int` backing type through the shared PHP
  semantic index, generate enum-typed entity accessors, and use the matching
  physical SQL type. Unbacked, missing, or mismatched enum declarations are
  rejected before preview can be applied
- A hierarchy field atomically generates Shopware's `ParentFkField`,
  `ParentAssociationField`, and `ChildrenAssociationField` bundle. When the
  entity is version-aware it also owns `parent_version_id`, the composite
  self-reference, and its index; deleting a parent cascades to its children
- Inheritance-aware definitions build on that hierarchy and generate
  `isInheritanceAware()`. Stored fields, associations, and translated facades
  expose typed `Inherited` metadata, while inverse associations support
  `ReverseInherited`; these flags do not create database migrations
- Many-to-one and one-to-one associations preserve and expose Shopware's
  `autoload` option, including the different native constructor defaults
- Stored fields, translated facades, associations, and hierarchy components
  expose typed `WriteProtected` metadata with optional allowed write scopes;
  dynamic custom scope expressions remain preserved as raw flags
- Scalar fields can be marked as translated. The same atomic preview then
  creates or updates the parent `TranslatedField` facade and translations
  association, the companion translation definition/entity/collection, both
  service registrations, and the translation table. Version-aware parents use
  the required parent-ID/version-ID/language-ID composite key and cascading
  foreign keys. Existing translation bundles are imported and rewritten with
  custom members preserved. Named normal or unique indexes can target either
  the parent table or generated translation table independently
- A selected-field inspector exposes type-specific limits, integer ranges,
  search ranking, relation delete behavior, mapping columns, and backfill SQL.
  Relation and index columns use indexed choices, and validation is attached to
  the affected field instead of only appearing in the file preview
- Required columns use an explicit existing-row backfill before becoming
  `NOT NULL`; version fields use Shopware's live-version ID. Primary-key and
  named JSON-check changes are rebuilt explicitly during schema edits
- Snapshot history is a content-addressed DAG. The designer handles baseline
  import, explicit branch reconciliation, manual-drift adoption or migration,
  explicit column create-versus-rename decisions, and technical entity/table
  rename decisions. Accepted table renames use `RENAME TABLE`, preserve data,
  and rebuild generated index, foreign-key, and JSON-check names safely
- Snapshot format v2 stores only physical database state: technical entity and
  storage names, columns, indexes, primary keys, and foreign keys. PHP classes,
  namespaces, properties, flags, associations without storage, and opaque PHP
  expressions stay in live indexed source, so moving a definition does not
  create schema drift
- Unapplied drafts survive webview reloads, unsaved PHP buffers participate in
  import and rewrite analysis, and a changed snapshot head forces a reload
  instead of silently rebasing a pending schema edit
- Destructive changes require confirmation. All generated DDL—including drops
  and narrowing changes—runs from `MigrationStep::update()`; the generator does
  not create or use `updateDestructive()`
- JSON snapshot diagnostics validate IDs, ancestry, migration hashes, branch
  reconciliation, and PHP-definition drift, so the same checks run in the
  editor and through the CLI `check` command

### PHP Semantic Intelligence

**Extract Method / Function** (`refactor.extract`) moves selected consecutive
statements into a private class method or a function in the same namespace.
A caret selects its containing statement, including statements inside nested
blocks. The action derives input parameters, preserves writable inputs through
reference parameters, and restores multiple outputs with destructuring.
Conditionals, loops, switch statements and early returns are supported when
their control-flow targets remain valid after extraction. Static methods use a
private static helper and a `self::` call. Generated parameters remain untyped
to avoid introducing additional coercions.

Existing source, inherited methods, trait methods and namespace functions
participate in name collision checks. Unknown ancestors withhold the action.
Simple string interpolation supplies variable inputs, and multiline string
contents are preserved exactly during reindentation. Locals that may retain
objects are kept in the caller so their lifetime is not shortened.

**Extract Variable** replaces a selected expression with a fresh `$extracted`
local. An empty selection chooses the smallest supported expression at the
caret. When a preceding assignment is safe, it is inserted before the statement.
In arguments, conditional branches, loop headers and other order-sensitive
positions, the assignment stays at the original evaluation point as
`($extracted = expression)`. This preserves short-circuiting and evaluation
frequency. Calls used directly as arguments require a resolved non-reference
return; unresolved or reference-returning calls remain unavailable there.

A separate **Extract variable '$extracted' (all N occurrences)** action shares
pure expressions across consecutive statements or within one argument list.
It stops at writes to an input, calls, control-flow boundaries or other
uncertain effects. Comments in later occurrences prevent their replacement.

Both actions add numeric suffixes to avoid name collisions; use ordinary
rename afterward to choose another name. Edits preserve comments, indentation,
literal contents, line endings and the current document version. LSP, CLI and
MCP share the same analysis and validated rewrite plans. CLI `lsp codeaction`
accepts `--end-line` and `--end-column`; MCP code-action tools accept `endLine`
and `endColumn`. Endpoints are exclusive, one-based UTF-16 positions; omitted
endpoints retain caret behavior. List actions first and use their exact titles
when applying through MCP.

Cases requiring unsupported scope or reference analysis still withhold the
action: dynamic symbol-table operations, generators, moving closures or
anonymous classes, executable string interpolation, reference returns,
exception-handling regions, and jumps targeting an outer loop outside the
selection. Extract Method does not extract trait or anonymous-class methods.
A newly introduced variable that would be undefined on a continuing path
cannot be exported. Early-return selections that create locals potentially
holding objects are withheld when their lifetime cannot be preserved.
Disabling the PHP domain or selecting the framework
presentation profile disables both PHP refactorings.

Unused namespace imports produce `php.unusedImport` hints with an unnecessary
tag and a lazy **Remove unused import** quick fix. The `php.imports`
inspection accounts for aliases, separate class/function/constant namespaces,
PHPDoc types, and legacy annotations. It uses the current unsaved snapshot and
skips documents with syntax errors.

**Organize Imports** (`source.organizeImports`) removes unused imports and
sorts contiguous import blocks by class, function, and constant, then name.
Comment-free groups expand to one import per line; comments remain in place
and form sorting boundaries. Suppressed imports are retained. The action is
available through LSP and the production CLI/MCP code-action tools, including
when the diagnostic rule is disabled. Disabling the PHP domain or using the
framework presentation profile disables both features.

Configure the hint with `diagnostics.rules.php.unusedImport` (for example
`warning` or `off`), or disable the inspection with
`diagnostics.inspections.php.imports: false`. Inline
`@noinspection php.unusedImport`, `PhpUnusedAliasInspection`, and
`PhpUnusedImportInspection` suppressions are also recognized. MCP diagnostics
use the project severity threshold by default; request `severity: hint` to
include these hints.

- Native lossless PHP 8.x CST, including PHP 8.4 asymmetric visibility and
  property hooks, heredoc/nowdoc, attributes, enums, traits, closures, match,
  and alternative control-flow syntax
- Workspace-wide symbols, lexical scopes, imports, references, inheritance,
  trait members, promoted properties, and unsaved-document overlays
- Native and PHPDoc types: unions, intersections, generics with variance and
  bounds, array/list/object shapes, callable signatures, literals,
  `class-string`, `self`, `static`, and `parent`
- Flow-sensitive local inference for assignments, branches, loops,
  `instanceof`, null checks, assertions, arrays, calls, and chained members
- Generic constructor/method inference, named and variadic argument checking,
  return checking, override compatibility, and abstract-method validation
- PHP completion, hover, go-to-definition, references, signature help, rename,
  and diagnostics
- PHP document outlines group namespaces, classes, interfaces, traits, enums,
  functions, methods, properties (including constructor promotion), and constants
- Same-document PHP occurrence highlighting uses resolved symbol identities and
  distinguishes reads from syntactic writes
- PHP folding covers bodies, arrays, multiline comments/strings, import groups,
  and alternative control syntax; selection expansion follows the lossless CST
- These editor features use unsaved documents, respect their existing feature
  toggles, and defer to the host IDE in the framework-only presentation profile
- PHPDoc `#Class`, `#Interface`, and `#ClassInterface` parameter contracts
  provide kind-filtered, exact-edit class-name completion and declaration
  navigation plus kind-aware missing-reference diagnostics and typo fixes at
  function, constructor, and inherited method call arguments
- Context-aware `#` / `#[…]` attribute completion with conflict-safe imports
  for controllers, Console commands, and Twig extensions/components
- Context-aware Symfony `Response::HTTP_*` completion for
  `setStatusCode()` arguments and `getStatusCode()` comparisons
- Clickable PHP/Twig route-name inlays expose the resolved path beside static
  route usages, providing a portable equivalent of inline route folding;
  PHP calls are receiver-typed across controller helpers and
  `UrlGeneratorInterface::generate()`, and repeated same-file usages remain
  distinct Find References targets
- Typed Symfony HttpClient option completion, exact definition navigation, and
  hover for `request()` / `withOptions()` arrays, derived from persisted
  `HttpClientInterface::OPTIONS_DEFAULTS` constant metadata
- Type-aware Symfony Console helper-name completion, class navigation, and
  hover for `HelperSet::get()` / `has()` and `Command::getHelper()`, derived
  from persisted literal `getName()` returns on concrete helper classes
- Symfony 7.3 `#[AsCommand]` invokable-command diagnostics for missing/wrong
  `int` return types and non-integer exit codes, with a return-type quick fix
- Console command aliases from named or positional `#[AsCommand]` arrays,
  including literal `self::` / `static::` class constants, participate in
  completion, navigation, hover, diagnostics, and project symbols
- Run code lenses on class-level, method-level, and legacy PHP command
  declarations launch the workspace `bin/console` through the configured PHP
  executable; names come from the current unsaved native parse tree
- `Symfony: Run Console Command…` provides a searchable command-palette
  catalog with aliases, implementation targets, descriptions, arguments, and
  options from the persistent index, then reuses the same safe runner. The
  structured catalog also accepts Ant-style project-relative source-file
  filters and returns both exact file URIs and portable relative paths
- Contextual import-aware actions for adding supported Console input/output,
  cursor, style, and application parameters to invokable commands
- Composer PHP-version/autoload discovery, versioned runtime stubs, and
  framework inference hooks for Symfony and Shopware
- Native JSON validation and semantic coloring inside static, typed
  `JsonResponse::fromJsonString()` / `setJson()` PHP arguments, including
  imported aliases, subclasses, escaped double-quoted strings, and exact
  decoded-to-host diagnostic ranges
- Native structural validation and CSS-aware semantic coloring for static,
  typed DomCrawler `filter()` / `children()` and
  `CssSelectorConverter::toXPath()` selector arguments
- A native lossless XPath frontend structurally validates and semantically colors static,
  typed DomCrawler `filterXPath()` / `evaluate()` expressions, including
  axes, functions, element/attribute names, variables, strings, numbers, and
  operators with exact PHP host ranges

### Twig Template Support
- Native whole-document Twig/HTML formatting through
  `textDocument/formatting`, using the current unsaved CST and the editor's
  `tabSize` / `insertSpaces` options. Templates below
  `Resources/app/administration` keep Shopware's flat Twig-block children;
  Storefront and ordinary Twig templates indent block children. Twig whitespace
  controls (`-` and `~`), comments, and whitespace-sensitive `pre` / `textarea`
  bodies are preserved.
- Template path completion in Twig files (`extends`, `include`, `sw_extends`, `sw_include` tags)
- Template path completion in PHP files (`renderStorefront` method calls)
- Go-to-definition for template paths in Twig and PHP files
- `sw_include` and `sw_extends` diagnostics and navigation resolve the shared
  template path across indexed bundle views, independent of the requested
  `@BundleName` or plugin folder name, matching Shopware's template finder.
  Ordinary Twig tags and functions keep exact namespace lookup.
- Exact standalone PHP strings ending in `.twig` also navigate when their
  logical template exists; they remain excluded from usage indexing and rename
  so ordinary configuration strings do not create false semantic edges
- PHPDoc `#Template` parameter contracts provide exact template-name
  completion, multi-target definition navigation, missing-reference
  diagnostics, and typo fixes at matching call arguments
- Missing-template diagnostics for typed PHP render calls and explicit or
  convention-guessed `#[Template]` declarations, with typo
  replacements and safe standard `templates/` file-creation quick fixes
- Find References across static Twig tags/functions, PHP render calls, and
  explicit `#[Template]` mappings
- PHPDoc `@Template` annotations are ignored for Twig templates; generic
  `@template` tags remain available to PHP type inference
- Structured template-usage analytics correlate logical/physical template
  names with PHP controller actions and routes, include/embed/extends/import/
  use/form-theme edges, and Twig component composition; partial-name,
  project-relative path, and Ant-style target-file filters are supported, and
  `Symfony: Analyze Twig Template Usages…` provides an aggregate navigator
- Structured Twig component analytics aggregate attribute, service,
  compiled-container, and anonymous-template declarations with templates,
  typed/live/writable props, computed values, blocks, usages, exact locations,
  and ready-to-use HTML/function/composition syntax; `Symfony: Browse Twig
  Components…` provides the interactive navigator
- Static Symfony UX Live Actions in `live_action()` or
  `data-live-action-param` complete and navigate to inherited
  `#[LiveAction]` methods, expose typed signatures, participate in Find
  References, and report typos. Helper-hash and `data-live-*-param` arguments
  resolve `#[LiveArg]` aliases with duplicate-aware completion, exact PHP
  parameter navigation, typed hover, diagnostics, and syntax-correct fixes
- Symfony UX Live events emitted through PHP `emit()`, `emitUp()`, or
  `emitSelf()` and Twig `live#emit`/`live#emitSelf` attributes complete and
  navigate to inherited, repeatable `#[LiveListener]` declarations, expose
  typed listener signatures, participate in cross-language Find References,
  and report unknown events. Static payload keys resolve `#[LiveArg]` aliases
  with duplicate-aware completion, exact parameter navigation, typed hover,
  diagnostics, and typo fixes
- Structured template-variable analytics merge controller render contexts,
  inherited template input contracts, native `@var` annotations, workspace
  globals, and component props. Results include canonical PHP types, source
  provenance, and first-level Twig-accessible public fields, getter aliases,
  and methods with exact declarations; `Symfony: Analyze Twig Template
  Variables…` provides the interactive type/member navigator
- Bidirectional related-navigation code lenses from controller render calls to
  templates and route declarations, and from templates back to rendering PHP
  locations and their routes
- Native clickable document links for unambiguous Twig template references,
  routing imports, Symfony configuration resources, and configuration-root
  declarations; ambiguous/globbed targets remain available through
  multi-target definition navigation and code lenses
- Static Twig `controller()` class/service-action completion, definition,
  hover, diagnostics, Find References, cache restore, and bidirectional
  PHP-method/Twig-usage code lenses
- Clickable top-of-template `Variables (N)` inlays for controller-rendered
  Twig files. The picker exposes inferred PHP types and accessible members,
  navigates to their declarations, and inserts print, conditional, or
  collection-loop snippets at the current line
- Scope-aware `loop.*` completion with Twig 3's fixed keys and dynamic Twig 4
  members resolved from `Twig\Runtime\LoopContext`
- Native `{% types { variable: 'PHP\\Type' } %}` declarations with optional
  keys, class completion/navigation, persistent PHP references, typed member
  intelligence, loop-element inference, and `if`/`for` statement suggestions
- Twig 3.29 documentation comments on output expressions, tags, blocks,
  macros, type declarations, assignments, loop targets, and macro arguments.
  `{## ... #}` / `{## ... ##}` construct documentation is available on hover;
  `## ...` binding documentation enriches `types` completion and hover plus
  persisted block and macro completion, hover, and signature help
- Standard semantic coloration for both Twig `{# @var variable Type #}` and
  `{# @var Type variable #}` annotations, including unions, array types,
  multiple declarations, and PHPDoc-style `$variable` names
- Twig `{# @see … #}` completion/navigation for PHP classes and methods,
  controller service actions, logical templates, and safe workspace-relative
  files, including the legacy single-target comment syntax
- Persistent custom-operator indexing and context-aware completion for legacy
  `getOperators()` and Twig 3.21+/4 `getExpressionParsers()`, including named
  and positional aliases
- Interactive Twig parent-template selection and inherited block-override
  generation, with unsaved override exclusion and cursor-aware snippets
- Native indexing for `#[AsTwigFunction]` and `#[AsTwigFilter]` methods,
  including functions and filters on classes that no longer extend
  `AbstractExtension`; exact persistent Find References works from either the
  Twig symbol or its PHP callback, including node-class-backed symbols
- Persistent Twig tests from `getTests()` / `TwigTest`, legacy
  `Twig_SimpleTest` / `Twig_Test`, `node_class` registrations, and
  `#[AsTwigTest]`, with
  completion, PHP navigation, deprecation diagnostics, and exact persistent
  Find References in ordinary `is` / `is not` expressions, plus type and
  callable completion/navigation in `{% guard function|filter|test … %}` tags
- A structured Twig extension analytics request lists functions, filters,
  tests, and token-parser tags with callback classes/methods, typed optional
  parameters, usage signatures, deprecations, and source locations;
  `Symfony: Browse Twig Extensions…` provides an interactive source navigator
- Safe whole-document migration from local `TwigFunction`, `TwigFilter`, and
  `TwigTest` registries to method attributes, preserving options and partial
  dynamic registries
- Safe template file move/rename edits through `workspace/willRenameFiles`,
  preserving plain, `@Namespace`, and legacy `Bundle:dir:file` name styles
- Symfony `form_theme` and `block(..., template)` template references
- Inherited Twig block completion, multi-target go-to-definition, hover,
  diagnostics, and typo quick fixes for typed PHP `renderBlock()` and
  `renderBlockView()` calls, including named arguments
- Twig related-item code lenses for reverse includes/imports, extending
  templates, same-name overrides, ancestor blocks, and transitive block
  implementations, with open-document inheritance/block overlays
- Twig filter and function completion with snippet support
- Deprecated Twig function/filter metadata from callable options and callback
  deprecation triggers, plus typed PHP member deprecations from PHPDoc or
  `#[Deprecated]`, with deprecated completion items, definition/hover, and hint
  diagnostics
- Low-noise missing-member diagnostics for typed Twig property/method chains,
  including getter aliases, constants, unions, and function/filter return
  types, while dynamic, collection-like, array, and magic-access receivers stay
  silent
- Call diagnostics for typed Twig variables: `product.price(context)` and the
  implicit call behind `product.price` are checked against the method Twig
  calls (`price()`, `getPrice()`, `isPrice()`, `hasPrice()`, in that order)
  for missing arguments and for arguments the parameter cannot accept, and
  `product.name()` is reported when `name` is only a property. Nothing is
  reported for receivers of unknown type or with a magic `__call`, for
  arguments of unknown type, or for spread and named arguments; scalars are
  only rejected by parameters that accept no scalar, as compiled templates
  do not use strict types
- Persistent modern and legacy Twig token-parser tags, with custom-tag
  completion, PHP go-to-definition, and opening/closing deprecation diagnostics
  from PHPDoc, `#[Deprecated]`, or `trigger_deprecation()`
- PHP enum completion/navigation/hover for Twig `enum()` and `enum_cases()`,
  including enum-kind validation, missing-enum diagnostics, and typo fixes
- Twig `constant()` completion, PHP navigation, typed/deprecated hover, and
  persistent cross-language references for namespaced global constants,
  class constants, enum cases, and object-relative constants
- PHP Find References and rename include persisted, type-resolved Twig
  property/method accesses, getter shortcuts, direct constants, `constant()`
  usages, enum cases, `enum()` / `enum_cases()`, `@var` class annotations, and
  `{% types %}` declarations
- Symfony UX Twig Component completion/navigation and prop/block intelligence,
  including semantic coloration for `{# @prop … #}` / `{# @block … #}`
  annotations plus mixed-syntax and invalid `_self` macro-import diagnostics
- Twig Component namespace, name-prefix, template-directory, and anonymous
  directory discovery from YAML and PHP root-array, `App::config()`, and
  `ContainerConfigurator::extension()` configuration; compiled-container
  component metadata overrides configured guesses, supports runtime-only
  components and dynamic template methods, and refreshes after cache warmup
- Icon name completion for `sw_icon` tags with pack selection
- Icon preview on hover for `sw_icon` tags (shows SVG preview inline)
- Diagnostics for missing icons in `sw_icon` tags

### Twig Block Versioning

- Tracks compatible SHA-256 block hashes across the complete `sw_extends`
  chain, including Storefront, vendor packages, themes, and custom extensions
- Detects outdated, deprecated, and safely resolvable removed upstream blocks;
  unresolved or missing dependencies do not produce false removal warnings
- Detects exact non-empty copies of the nearest resolved parent block and
  offers a lazy quick fix that delegates through `{{ parent() }}` while
  removing an obsolete version comment
- Adds or updates portable `{# shopware-block: hash@version #}` comments through
  typed, lazily resolved quick fixes that also work through MCP
- Offers the same add/update action contextually even when the optional missing
  comment diagnostic is disabled
- Shows historical diffs for changed Shopware core blocks and exposes upstream
  path, package version, hash candidates, and current status on hover
- Scaffolds a Storefront block override into a selected extension

The `twig.versioning.comment_missing` diagnostic is opt-in to avoid noisy
warnings in projects that do not use block comments. Enable it explicitly:

```json
{
  "version": 1,
  "diagnostics": {
    "rules": {
      "twig.versioning.comment_missing": "warning"
    }
  }
}
```

Set `domains.shopware.twigVersioning` to `false` to disable Twig block
versioning diagnostics, hover, actions, and commands as one feature domain.

### Assets, AssetMapper, Webpack Encore, and Vite
- Static `public/`/`web/` files, manifests, entrypoints, and Encore entries
- Twig `asset()`/Encore and typed PHP Asset `Packages` intelligence
- Named package discovery from Symfony YAML and XML/PHP service tags, plus
  inferred Shopware `@Bundle` packages
- Package-aware completion, navigation, references, hover, diagnostics, and
  typo quick fixes, including Shopware bundle and dynamic theme paths
- Persistent `importmap.php` and `assets/vendor/installed.php` module metadata;
  Twig `importmap()` entrypoint completion, target/declaration navigation,
  references, hover, diagnostics, and typo quick fixes
- Legacy Assetic `{% stylesheets %}` / `{% javascripts %}` blocks with native
  lossless parsing, direct file/bundle/directory/glob completion and
  navigation, missing-asset diagnostics, references, hover, and lazily
  refreshed compiled `@named_asset` formulas
- Persistent `vite.config.js`/`vite.config.ts` Rollup input discovery, including
  variable and spread-based entry maps; Twig `vite_entry_link_tags()` and
  `vite_entry_script_tags()` completion, target/config navigation, references,
  hover, diagnostics, typo quick fixes, and target-file related code lenses
- Tag-aware asset completion in Twig `<link rel="stylesheet">`, `<script>`,
  and `<img>` attributes, with extension filtering and automatic `asset()`
  insertion; static raw paths also support navigation, hover, references, and
  actionable typo diagnostics

### Project Symbol Search
- `workspace/symbol` navigation for Symfony services, routes and route URL
  matches, console commands, and translation keys
- Project-wide Twig template, block, macro, function, filter, and
  Twig/Live Component symbols

### Snippet Support
- Snippet completion in Twig, PHP, and JavaScript/TypeScript files
- Frontend snippets: `{{ 'key'|trans }}` (Twig), `$this->trans('key')` (PHP)
- Admin snippets in Twig expressions and executable Vue bindings such as
  `:label="$t('key')"`, plus JavaScript/TypeScript calls through component,
  root, injected-translator, and `Shopware.Snippet.t/tc` receivers; static
  module `title`, `description`, and navigation `label` keys share the same
  completion, navigation, hover, diagnostics, and preview support
- Go-to-definition for snippet keys (shows all locale variants)
- Hover support showing all available translations for a snippet key
- Diagnostics for missing snippets in Twig and JavaScript/TypeScript files
- Code actions to create snippets from diagnostics or text selections

### Symfony Translation Support
- Catalog-backed key/domain completion, navigation, hover, diagnostics, typo
  actions, and placeholder completion in typed PHP and Twig translation calls
- `refactor.extract` support for static Twig text and HTML attribute values;
  the editor prompts for the key, active/default domain, and locale files while
  the server safely updates YAML or XLIFF catalogs
- PHPDoc `#TranslationKey` and `#TranslationDomain` parameter contracts provide
  exact completion/navigation, missing-reference diagnostics, and typo fixes;
  key contracts read a sibling domain contract, including named arguments, and
  otherwise default to `messages`

### Route Support
- Route indexing from PHP attributes, fluent PHP `RoutingConfigurator`
  definitions, YAML, and Symfony XML; fluent routes include local aliases,
  inherited path/name prefixes, controller/default targets, and HTTP methods
- Bidirectional related navigation and go-to-definition for YAML, XML, and PHP
  routing imports, including nested YAML resources, recursive controller
  directories, deterministic glob or brace-pattern matches, multiple reverse
  import targets, legacy `@Bundle` resources, cache restore, and stale-edge
  removal
- Reload-aware fallback routes from Symfony's generated route catalogs under
  `var/cache` or legacy `app/cache`, including modern
  `url_generating_routes.php`, legacy `*UrlGenerator.php` static-property and
  constructor formats, reconstructed placeholder paths, controller defaults,
  canonical aliases, localized route-name normalization, and Assetic-route
  filtering; source declarations remain the preferred navigation target
- Route name completion in PHP (`redirectToRoute`, `generateUrl`) and Twig
  (`seoUrl`, `url`, `path` functions), including path and placeholder details
- Route parameter completion and go-to-definition for placeholders such as
  `{id}` in PHP arrays and quoted, unquoted, or shorthand Twig hashes
- Go-to-definition and hover details for route names
- Find all references for routes
- Diagnostics for missing static route names
- Diagnostics for route names defined in more than one project file
  (`symfony.route.redefined`; the definition Symfony loads last replaces the
  others). Definitions in `vendor/` and test directories are ignored, since
  redefining a bundle route by name is how it is overridden
- Deprecated controller class/method diagnostics on XML/YAML/Twig controller
  references and PHP/Twig route-name usages
- Route completion in Twig anchor `href` and form `action` attributes with
  automatic `path()` insertion and placeholder arguments; concrete static URLs
  support normalized absolute/partial reverse navigation, hover, and
  cross-style references
- JavaScript/TypeScript request URL completion and reverse navigation for
  `fetch()`, Axios, `Request()`, `url`, and Axios `baseURL` contexts
- Contextual import-aware actions for adding `Request` and `UserInterface`
  parameters to public attribute- or annotation-based route actions

### Feature Flag Support
- Feature flag completion in PHP (`Feature::isActive()`), Twig (`feature()`), and SCSS files
- Go-to-definition for feature flags

### System Config Support
- System config key completion in PHP (`SystemConfigService::get()`, `getInt()`, `getString()`, `getFloat()`, `getBool()`, `set()`, `getDomain()`)
- System config key completion in Twig (`config()` function)
- Go-to-definition for system config keys

### Symfony Validator Support
- Inherited constraint-option completion, definition, hover, diagnostics, and
  typo actions for PHP constraint arrays and attributes
- `validators`-domain translation intelligence for constraint properties,
  object/attribute options, and violation builders
- Constraint-to-validator and constraint-message-to-catalog code lenses with
  exact, one-based navigation targets

### Symfony Messenger Support
- Persistent message graphs from class/method `#[AsMessageHandler]`
  attributes, legacy subscribers, and PHP, YAML, or XML
  `messenger.message_handler` service tags
- Typed `MessageBusInterface::dispatch()` site indexing, including messages
  held in variables and union-typed handler parameters
- Cross-language message/handler completion, navigation, Find References,
  hover, diagnostics, typo actions, and message/handler code lenses
- Native CST recognition of legacy
  `MessageSubscriberInterface::getHandledMessages()` return and yield forms
- Persistent cache restore and stale-file removal

### Symfony Environment Support
- Persistent environment declarations from `.env`, `.env.*`, `*.env`,
  Dockerfiles, and Compose `environment` mappings or sequences
- `%env(...)%` variable completion in PHP, YAML, and XML plus direct PHP
  `env('…')` and `#[Autowire(env: '…')]` support, including incomplete
  expressions and chained processors such as `resolve:int:VARIABLE`
- Go-to-definition across every declaration, workspace Find References, and
  hover with declaration sources and processor details
- Secret-like values are redacted from hover output
- Persistent cache restore and stale-file removal

### Doctrine DBAL Support

- Typed DBAL QueryBuilder/Connection table, column, and join-alias completion,
  navigation, and hover backed by Shopware DAL definitions.
- Table and column typo diagnostics and quick fixes use Shopware DAL storage
  names, excluding association-only fields. Unknown tables without a nearby
  indexed match are left alone.
- Custom DBAL types are discovered from literal/class-constant `getName()`
  returns, conventional class names, DoctrineBundle YAML/XML and PHP
  `extension('doctrine', …)` registrations, and static runtime `addType`,
  `overrideType`, and type-registry registrations.
- Type registration classes have subtype-filtered completion, navigation,
  hover, missing/invalid-class diagnostics, typo fixes, and references between
  registration keys and implementation declarations.
- The existing `symfony.doctrine` domain controls DBAL support. ORM/ODM
  mappings, repository inference, DQL, mapping actions/code lenses, and the
  Doctrine entity browser are no longer provided. General PHP intelligence
  remains available for installed library classes.

### Theme Config Support
- SCSS variable completion from theme configuration (prefixed with `$`), plus
  conservative unknown-variable diagnostics backed by workspace declarations
  and SCSS-enabled `theme.json` fields
- Twig `theme_config()` function key completion
- Go-to-definition for theme config fields

### Admin Component Support
- Native `.vue` single-file component support preserves one lossless tree with
  full-file ranges for embedded template, JavaScript/TypeScript, and SCSS
  sections. Administration language features are routed by the active section.
- Component contracts are indexed directly from both Options API definitions
  and `<script setup>` macros, including typed/runtime props, emits, models,
  template-visible bindings, imported local components, slots, and
  extensibility blocks. The same contracts drive markup completion, hover,
  navigation, references, rename, semantic tokens, and diagnostics.
- Type-only `<script setup>` contracts resolve lazily across imported
  interfaces and aliases, including intersections, generics, `Partial`,
  `Required`, `Pick`, `Omit`, `withDefaults`, event maps, and callable emit
  overloads. Prop/event navigation retains the external declaration owner and
  type-file edits do not require reindexing the consuming Vue component.
- Typed `defineSlots` declarations contribute named slots and scoped payload
  members, including imported payload interfaces. Reactive `defineProps`
  destructuring preserves aliases, defaults, exact local declaration ranges,
  and external type ownership. Open Vue documents use a non-mutating live
  contract overlay, so completion, hover, navigation, diagnostics, and semantic
  classification reflect unsaved component edits immediately, including newly
  added type imports, local interfaces, nested object members, and `v-for`
  element contracts without first updating the persistent workspace index.
- Template-local PascalCase or kebab-case component imports resolve directly to
  their indexed `.vue` definition even when they are not globally registered.
  Unsaved imports immediately drive tag/prop/slot completion, navigation,
  hover, semantic tokens, diagnostics, dynamic-component contracts, references,
  and safe slot refactors without leaking the open buffer into SQLite.
- Open Administration buffers form one workspace-scoped live contract overlay.
  A parent component therefore observes unsaved prop, event, slot, and scoped
  payload changes in an imported `.vue` child, while unsaved imported `.ts`
  interfaces are resolved lazily without reparsing that child. Closing either
  buffer removes only its in-memory overlay and falls back to the current
  persisted generation.
- The same overlay composes Shopware's legacy three-file components: live
  `Component.register` loaders replace registrations by source file, live
  Options API `.js`/`.ts` exports replace props, events, and template members,
  and live imported Twig files replace slots and extensibility blocks. Changes
  drive completion, diagnostics, and navigation across open files, and trigger
  a debounced refresh of dependent Administration diagnostics.
- Administration runtime registries use the same source-file replacement
  semantics for unsaved `.js`, `.ts`, and `.vue` buffers. Mixins, modules and
  routes, services, Pinia stores and imported setup factories, directives,
  filters, CMS elements/blocks, and privileges therefore update every provider immediately;
  deleting an entry in an open file hides its stale SQLite value until close.
- Effective component models across register, extend, override, imported
  definitions, and registered mixins
- Component tag and inherited prop completion in administration Twig templates,
  with lossless runtime/TypeScript `PropType` information, requirements,
  defaults, allowed-value completion from Shopware `validValues` and Meteor
  literal unions, plus closed literal domains from runtime validators such as
  `['small', 'large'].includes(value)` and validator-local immutable literal
  arrays, hover, source navigation, and conservative invalid-value diagnostics
  with typo fixes. Imported, mutable, dynamic, or compound validators remain
  open. A closed string validator also supplies the proven string type when a
  legacy component omits the redundant `type: String`.
  Exact `if (!value.length) return true` guards on proven string props retain
  the empty string alongside the subsequent closed literal domain. The same
  value completion and validation applies to direct attributes and literal
  values forwarded through component-level `v-bind="{ ... }"` objects.
  Source-owned prop JSDoc descriptions from Options API declarations and
  local or imported TypeScript `defineProps` contracts are persisted through
  inheritance and shown in both markup completion and hover.
  Every effective prop retains an exact declaration range for navigation and
  hover; legacy `props: ['name']` entries target the string content while
  remaining deliberately non-identifier declarations for rename safety.
- Component events from Options API `emits`, inferred `$emit` calls,
  `defineEmits` signatures, imported TypeScript contracts, `defineModel`, and
  generated Meteor declarations retain exact source ranges and source-owned
  documentation. Legacy Shopware `@event` annotations supply public payload
  contracts when `$emit` uses an imported constant; an explicit `emits`
  declaration remains authoritative over stale annotations. Event completion,
  hover, `$event` typing, model navigation, and reference declarations therefore
  use the documented public contract and land on its exact event token.
- Component slots from Twig/Vue `<slot>` tags, dynamic `:name` families,
  `defineSlots` contracts, imported TypeScript types, and generated Meteor
  declarations retain exact source ranges. Definition and reference requests
  select the literal, type member, implicit default tag, or dynamic name
  expression that owns the effective slot contract. Scoped payload members
  retain their own exact ranges from direct attributes, `v-bind` object keys,
  imported type members, and Meteor payload declarations.
- Source-owned `@deprecated` metadata for Administration components and props,
  including inherited props, is persisted and exposed through struck-through
  completion items, hover and document outlines, plus hint diagnostics on
  static or safely closed dynamic markup contracts
- Source-owned `@deprecated` metadata for Administration Options API and
  setup-return members survives inheritance and marks `this.*` and component
  template completion, hover, live outlines, and diagnostics
- Administration Twig extensibility blocks retain exact source ranges and
  `@deprecated` metadata across component inheritance and overrides. Parent
  block completion, hover, definition navigation, deprecated-use hints, and
  typo diagnostics with replacement actions share the same effective contract.
  Extend/override templates may introduce their own blocks; only a close
  misspelling of a parent block is diagnosed.
  Lexical inputs captured at the parent block declaration, such as data-grid
  `item` and `column` variables, remain typed and navigable inside overriding
  templates and survive persistent-cache restoration
- Administration `<sw-block extends="…">` declarations are diagnosed when
  nested or placed under conditional/repeated Vue or Twig control flow;
  conditional and repeated `<sw-block-parent>` rendering is rejected as well
- Vue `<component is>`, `:is`, and `v-bind:is` selectors are component-aware:
  static literal candidates participate in completion, hover, navigation,
  semantic highlighting, references, rename, and missing-component fixes.
  Complete ternary selectors expose the intersection of safe prop/event/model
  completions while hover, navigation, and diagnostics retain every possible
  component contract. Closed literal unions returned by the owning component's
  computed values or methods are resolved through the effective inherited and
  mixed-in member model as well; open props, services, mutable state, and
  unresolved return branches remain conservative. Vue Router's scoped
  `Component` value is joined with the indexed Administration module route
  tree, so direct child-route views expose the same finite dynamic contracts,
  including destructured aliases and whole-slot-object access
- Component props are source-owned symbols across Options API declarations,
  runtime and typed `defineProps`, imported TypeScript contracts, generated
  Meteor declarations, `this.prop` JavaScript access, inherited consumers, and
  Twig attributes. Their exact declaration ranges survive the persistent index,
  so markup navigation selects the prop token rather than only its source line;
  Find References and conflict-checked rename preserve camelCase declarations
  and kebab-case markup
- Statically named fields in component-level object bindings such as
  `v-bind="{ title, disabled: isDisabled }"` use the same prop contracts for
  completion, typed hover and diagnostics, navigation, semantic highlighting,
  Find References, and rename. Shorthand renames expand to explicit key/value
  pairs so public props and private component members retain separate identities;
  computed keys and runtime spreads remain conservative
- Default and named `v-model` directives are modeled as compound prop/update-
  event contracts, including legacy Options API `model` declarations and
  Meteor `defineModel` declarations. They participate in completion, typed
  hover, navigation to both contract sides, semantic highlighting, Find
  References, required-prop checks, writable-expression validation, and
  conservative value-type diagnostics. Compound model renames remain
  read-only until both declaration identities can be rewritten atomically.
  A named model is also recognized as supplying its argument prop when legacy
  components omit the matching update event from their indexed metadata, while
  event/type features remain conservative until the complete pair is known
- Template-scoped Options API `components` aliases participate in tag/prop/slot
  completion, hover, navigation, semantic highlighting, references, safe
  rename, and diagnostics without leaking into the global component registry.
  Static Vue application component maps registered through Shopware's
  kebab-case adapter are indexed as globals, including compound Meteor exports
- Catalog-backed unknown `sw-*` / `mt-*` tag diagnostics stay conservative for
  plugin custom elements and offer a paired opening/closing-tag typo fix
- Semantic highlighting for registered component tags, props, events, and
  named slots in Administration Twig templates
- Meteor `mt-*` component props, typed events, and typed scoped-slot payloads
  from the installed `@shopware-ag/meteor-component-library` TypeScript
  declarations. Runtime-compatible `mt-link` route-location objects and numeric
  `mt-icon` sizes augment the narrower generated declarations used by Meteor
- Source-aware component events from `emits`, static `$emit` calls, and Meteor
  listener declarations, with canonical `@event-name` completion, typed hover,
  navigation to the declaration, and source-owned Find References across
  inherited component listeners; conflict-checked rename preserves camelCase
  JavaScript declarations and kebab-case Twig listeners
- Custom Vue directives registered through `Shopware.Directive.register()` or
  `Directive.register()` are indexed across JavaScript/TypeScript and
  Administration Twig. Options API `directives: { name: ... }` declarations
  are indexed separately, resolve only in their owning component template, and
  correctly shadow an equally named global directive. `Directive.getByName()`
  and `v-name` markup share
  completion, hover, navigation, semantic highlighting, Find References, and
  conflict-checked rename; directive arguments and modifiers are preserved.
  Missing-directive diagnostics are deliberately limited to close typos of an
  indexed name and provide a replacement action, so third-party directives
  installed outside Shopware's registry remain conservative
- Administration filters registered through `Shopware.Filter.register()` or
  destructured `Filter.register()` are indexed with their TypeScript callable
  signatures. Static `Filter.getByName()` calls support completion, typed
  hover, navigation, Find References, conflict-checked rename, workspace
  symbols, and conservative missing-filter diagnostics with typo fixes. Open
  JavaScript, TypeScript, and Vue buffers replace persisted filter definitions
  by source file until the document closes
- Slot name completion in `<template #slot-name>` syntax, hover, and navigation
  to the owning template or declaration, including inherited slots and
  source-owned Find References and conflict-checked rename. Named slots below
  closed dynamic `<component :is>` owners resolve every candidate contract:
  completion and semantic highlighting use their safe intersection, hover,
  navigation, and references retain matching declarations, and rename proceeds
  only when all matches share one source identity; runtime-open owners remain
  conservative. Scoped payloads below those dynamic owners use the same policy:
  common fields complete and highlight safely, differing field types surface as
  unions or callable overloads, definitions/references retain every candidate
  declaration, and destructured aliases support shadow-aware lexical rename.
  Native `<slot
  :value>` and Meteor payload members provide destructuring completion, typed
  lexical locals, hover, and contract navigation in Twig interpolation and Vue
  directive expressions. Safely resolvable dynamic families such as
  ``column-${property}`` provide the same contract support to concrete
  `#column-name` consumers, while arbitrary runtime forwarding remains
  conservative. Whole-object bindings such as `#content="props"` expose
  contract-backed `props.member` completion, typed hover, declaration and
  reference navigation, semantic highlighting, and typo diagnostics when the
  payload is provably complete. Nested scoped slots retain every visible outer
  binding while preserving normal innermost-shadowing semantics
- Parent component name completion in `Component.extend()` calls, plus
  component registry completion, hover, and navigation for
  `getComponentRegistry().get(...)` and `.has(...)`
- Go-to-definition for component tags, props, events, slots, and parent components
- Hover showing full component details (props, events, methods, computed properties, slots)
- Template and JavaScript `this.*` scope for props, data, computed values,
  methods, injected services, inherited members, and Vue instance built-ins;
  statically returned Composition API `setup()` bindings participate in the
  same model, including aliased returns, unwrapped `ref`/`computed` types, and
  callable completion. Non-returned setup locals remain private. Public
  data/computed/method/setup-return names retain exact source identity through
  inheritance and participate in cross-file Find References and rename from
  either JavaScript or markup; setup/data shorthand returns are expanded to an
  alias during rename so private local bindings keep their original name.
  Signature help exposes parameter names, optional/rest markers, nested
  TypeScript types, and return types for component methods, typed markup member
  calls, Vue instance builtins, and Pinia actions in JavaScript/TypeScript and
  Administration Twig expressions. Range-aware parameter-name inlay hints
  reuse those exact contracts at call sites, suppress same-named arguments and
  ambiguous overloads, and remain conservative for unresolved/dynamic calls.
  Administration Twig interpolation and Vue directive expressions share the
  same template-member completion, hover, and navigation, including Vue
  instance built-ins and safe JavaScript template globals. Close root-member
  misspellings produce conservative diagnostics and replacement actions while
  runtime-spread component scopes remain open. Lexical `v-for`
  aliases support nested shadowing, completion, hover, definition, document
  references, safe rename, and semantic highlighting. Their properties are
  resolved through indexed TypeScript interfaces and type aliases, including
  imports, inheritance, generics, nullable unions, inline object types, nested
  member chains, and nested-loop element propagation. Complete type shapes
  keep generic container members distinct from their element members, so an
  `Array<{...}>` exposes `length`/`map` while indexed and loop values expose the
  object fields. Closed type shapes
  enable conservative unknown-property diagnostics and typo fixes; unresolved
  shapes fall back to document-local observations without false positives.
  Component-root chains use the same resolver, including generated Shopware
  `Entity<'name'>` schemas, extension interface augmentation, and
  `EntityCollection<'name'>` loop element types. Statically named calls with
  declared signatures propagate their return types through optional and nested
  chains, while computed or dynamically named access remains conservative.
  Data/computed values and method signatures retain declared or safely inferred
  types. Legacy JavaScript return expressions are retained until the effective
  component is assembled, allowing inherited computed chains,
  `repositoryFactory.create('entity')`, repository results, and typed
  `Shopware.Store.get(...)` members to resolve through generated entity schemas
  without requiring handwritten TypeScript annotations. Direct instance writes
  also refine weak `null`/`[]` data seeds after mixins, inheritance, and
  overrides are composed, so repository-loaded collections provide typed
  `v-for` bindings. Immutable function-local `const` results are propagated
  through lexically scoped instance writes, covering the common
  `const result = await repository.search(); this.items = result` form.
  Comma- and semicolon-separated TypeScript object fields are equivalent;
  branch-specific union properties remain available as optional values.
  Runtime-inferred object literals stay open for diagnostics while retaining
  their known members for completion and navigation. Component-local element
  writes such as `this.rows.forEach(row => row.selected = true)` augment that
  collection precisely without weakening Shopware entity contracts globally.
  First-parameter values from statically named Promise `.then(...)` arrow
  callbacks are propagated from the awaited receiver as well, while error
  callbacks and dynamic/unbound functions remain conservative.
  `EntityCollection` also exposes its inherited typed Array surface, and safe
  call chains such as `items.filter(...)` or `items.slice(...)` preserve their
  entity element type in computed values and direct `v-for` expressions.
  Statically bound `map` callbacks propagate primitive, nested-member, and
  object-projection result types through optional/nullish collection chains;
  dynamic or branch-dependent callbacks remain conservatively unknown.
  Static array/object literals, typed array/tuple/`Record` index access, and
  `Object.keys`/`values`/`entries`/`fromEntries` retain their key and value
  types when used by computed properties or directly in component markup.
  Markup member chains continue through numeric literals, typed `v-for`
  indices and arithmetic indices, or dynamic `Record` keys, so expressions
  such as `items[index].name` participate in completion, hover, navigation,
  references, and typo diagnostics without treating arbitrary objects as maps.
  Vue object and `Record<K, V>` loops distinguish value, key, and numeric-index
  bindings, while typed `$slots`, `$attrs`, `$refs`, translation helpers, and
  other core instance globals flow through legacy computed/method chains.
  Event handlers expose
  `$event` with native or indexed component payload types
- Indexed Administration services with completion, hover, and navigation from
  `Shopware.Service(...)`, component `inject`, middleware, and decorators;
  imported service factories navigate directly to their implementation
- `Application.getContainer(...)` and `Shopware.Application.getContainer(...)`
  expose all five Bottle.js containers with name completion and members resolved
  from the indexed `FactoryContainer`, `ServiceContainer`, `InitContainer`,
  `InitPreContainer`, and `InitPostContainer` TypeScript contracts. Ambient
  `ServiceContainer` declarations from core, modules, and plugins are merged;
  lexically scoped `const` aliases retain typed member completion, hover, and
  exact definition navigation. Service-container members also participate in
  Find References and safe service rename. Closed factory/initializer contracts
  report only close misspellings and provide replacement actions, while the
  runtime-extensible service container remains diagnostic-open.
- `Shopware.Context` is resolved from the indexed `ContextState` TypeScript
  contract rather than a hardcoded catalog. Nested `app`, `api`, and
  `app.config` fields provide typed completion, hover, exact definition
  navigation, computed-property inference for component markup, and safe typo
  replacements. Inline object-type members retain their recursive structure
  and exact source ranges in the persistent type index; the Context root stays
  diagnostic-open because `useContext()` also exposes runtime helpers.
- `Shopware.Utils` is resolved from the Administration's indexed
  `util.service` default export and its imported utility modules rather than a
  duplicated catalog. Root and nested utility members provide typed completion,
  hover, exact export navigation, signature help, component-expression
  inference, and conservative typo fixes. Lexically visible `const` aliases,
  destructured bindings, and renamed destructuring retain the same contracts
  across nested component methods; mutable or shadowed bindings remain
  unresolved. `Shopware.Utils.EventBus.on`, `off`, and `emit` provide known
  event-name completion, typed payload hover, exact declaration navigation,
  cached references, safe declaration-and-usage rename, and event-specific
  signature help, including lexical EventBus aliases. Known events receive conservative missing-payload,
  payload-type, and handler diagnostics when the mismatch is statically
  provable. External utility functions and the open EventBus event map do not
  produce unsafe missing-member or unknown-event diagnostics, and open event
  names are not offered as rename-safe symbols.
- Indexed Pinia stores with completion, hover, and navigation for
  `Shopware.Store.get(...)` and `Shopware.Store.unregister(...)`, including
  references and safe registry rename plus inline and imported setup-store state,
  getter, and action members; TypeScript state return types, ref/computed
  generics, getter results, and action signatures are preserved for markup
  inference and displayed by completion and hover
- Shopware DAL entity completion, PHP-definition navigation, and entity hover
  in Administration repository creation and static
  `Shopware.EntityDefinition.get()` / `.has()` calls, including embedded Vue
  scripts. Technical entity and field names participate in workspace symbol
  search. Close entity-name misspellings receive conservative diagnostics and
  replacement actions while runtime custom entities and existence guards stay
  unvalidated. Criteria fields and associations retain completion,
  segment-aware navigation, and owning-entity/type hover across filters,
  sorting, aggregations, field lists, and association paths
- Indexed Administration modules with completion, hover, navigation, cached
  references, and conservative missing-module diagnostics for
  `getModuleRegistry().get(...)`/`.has(...)`; module configuration objects may
  be inline or lexically bound to a `const`. Indexed routes include nested
  routes, local child-route factories, and statically named routes appended by
  inline or referenced `routeMiddleware` functions, with completion, hover,
  navigation, cached references, missing-route diagnostics, and typo fixes from JavaScript redirects,
  navigation/settings metadata, Vue Router `push`/`replace`/`resolve` locations,
  and Twig `:to`/`:route`/`:router-link` bindings
- Indexed Administration ACL role keys and concrete permissions, plus the
  source-less built-in `admin` administrator privilege, with
  completion, hover, and navigation in `acl.can(...)`, privilege-service calls,
  route/menu `privilege` fields, and dependency arrays; Vue expressions inside
  Administration Twig attributes participate in the same completion,
  navigation, hover, cached-reference, and missing-privilege typo-fix flow
- Indexed Shopware CMS element and block registrations, including labels,
  categories, render/configuration/preview components, and block-slot element
  references. Registration names support completion, hover, navigation,
  references, workspace symbols, safe rename, missing-name diagnostics, and
  typo fixes. The component/configuration/preview component links themselves
  support component completion, hover, navigation, references, safe rename,
  missing-component diagnostics, and typo fixes. CMS registry provenance is
  retained through computed values so
  dynamic markup such as `elementConfig.component` and
  `cmsElements[element.type].configComponent` exposes the concrete indexed
  component prop contracts.
- Workspace symbol search for Administration components and their public
  props, events, models, and slots (including markup-form queries such as
  `help-text`, `@save`, and `#actions`), plus mixins, global and component-local
  custom Vue directives, filters, modules and routes, CMS elements and blocks, services,
  Pinia stores and members, and ACL roles and permissions
- Bidirectional component code lenses link JavaScript/TypeScript registrations
  and imported definitions to their Twig templates and extensions, and link an
  Administration component template back to its owning definition and
  extension registrations
- Live document outlines and breadcrumbs for Administration component
  registrations/definitions, including props, events, injected services, data,
  computed values, methods, local components/directives, Twig blocks, slots,
  and scoped-slot payload fields; unsaved edits are reflected from the open CST
- Same-document semantic occurrence highlights for Administration components,
  props, events, models, slots, instance members, scoped-slot and `v-for`
  bindings, services, stores, routes, ACL keys, and other indexed registry
  identities; cached occurrences from the open file are replaced by the live
  unsaved CST before highlighting or Find References results are returned
- Linked editing for paired Administration component tags keeps opening and
  closing names synchronized from the live Twig CST, including nested custom
  components and Vue's dynamic `<component>` element; native HTML tags and
  incomplete/self-closing pairs are left untouched
- Live folding ranges for Administration JavaScript/TypeScript import groups,
  configuration objects, arrays, method bodies, and block comments, plus Twig
  blocks, comments, component markup bodies, and multiline self-closing tags;
  line-based ranges come directly from the open CST and retain no index state
- Smart selection expansion for Administration JavaScript/TypeScript and Twig
  starts at the exact token and walks strict native-CST parents through member
  expressions, methods, option objects, Vue bindings, attributes, tags,
  component bodies, Twig blocks, and the complete live document; multi-cursor
  requests preserve input order and UTF-16 coordinates
- Cross-language call hierarchy for source-owned Administration component
  methods and Pinia actions. Incoming callers are grouped by component method,
  store action, or Twig template; outgoing calls are verified from the native
  JavaScript/TypeScript or Twig CST, with open unsaved documents taking
  precedence over the persisted usage index
- Cached cross-file references for component and module registry names, Twig
  tags, source-owned component instance members, component events and slots
  (including inherited consumers), component model prop/update-event pairs,
  services (including injected `this.*` members), stores and members, mixins,
  ACL privileges, module routes, CMS elements, and CMS blocks
- Conflict-checked cross-language rename for local component names, component
  instance members, events and slots, services, stores, and mixins; generated
  ACL/module identities, ambiguous runtime/mixin members, and external Meteor
  components remain read-only
- Diagnostics for missing required props, statically unbound non-string props,
  provably incompatible typed `:prop` and `v-model` bindings, non-writable
  model expressions, close misspellings of indexed component props, events,
  named models, and slots (including object `v-bind` keys and dynamic slot
  families), plus unknown complete scoped-slot payload fields in both
  `props.member` and destructured `{ member: local }` forms, with
  prefix/modifier-preserving typo fixes,
  unknown component instance members, close misspellings on closed
  `Application.getContainer(...)` contracts, nested `Shopware.Context` fields,
  and closed `Shopware.Utils` utility objects, invalid block
  references, unknown
  Administration Twig privileges, unknown registry `get(...)` targets, and
  unknown module routes with suggested replacements
- Diagnostics for non-existent parent components
- Diagnostics and suggested replacements for unknown Shopware CMS element and
  block names in registry lookups and block-slot declarations
- Code action to add missing required props with type-appropriate defaults

### Diagnostics

| Diagnostic | Severity | File Types |
|---|---|---|
| Missing snippet keys | Error | Twig, JS/TS |
| Missing Symfony route | Error | PHP, Twig |
| Missing icons in `sw_icon` | Error | Twig |
| Missing required component props | Warning | Twig (admin) |
| Misspelled component props, events, models, or slots | Warning | Twig (admin) |
| Incompatible typed component prop binding | Warning | Twig (admin) |
| Invalid static component prop value | Warning | Twig (admin) |
| Missing custom Administration Vue directive typo | Warning | Twig, JS/TS (admin) |
| Missing Administration filter registration | Warning | JS/TS (admin) |
| Misspelled Application container member | Warning | JS/TS/Vue (admin) |
| Misspelled Shopware DAL entity name | Warning | JS/TS/Vue (admin) |
| Missing Shopware CMS element or block registration | Warning | JS/TS (admin) |
| Non-writable component model binding | Warning | Twig (admin) |
| Incompatible typed component model binding | Warning | Twig (admin) |
| Invalid block references in component overrides | Error | Twig (admin) |
| Nested or runtime-controlled Administration block override | Error | Twig, Vue (admin) |
| Non-existent parent component | Error | JS/TS (admin) |
| Outdated block version hash | Warning | Twig |
| Missing block version comment | Warning | Twig |
| Redundant Twig block override | Hint | Twig |
| Undefined PHP classes, variables, and members | Warning | PHP |
| PHP argument, constructor, and return mismatch | Error | PHP |
| Invalid PHP visibility or override | Error | PHP |
| Missing abstract/interface implementation | Error | PHP |
| Missing Messenger subscriber handler method | Warning | PHP |
| Missing Messenger message or configured handler method | Warning | PHP, XML, YAML |
| Invokable Symfony command should declare `int` | Hint | PHP |
| Invokable Symfony command returns a non-integer exit code | Warning | PHP |
| Deprecated Symfony service or service class | Hint | PHP, XML, YAML |
| Service class violates a conventional tag contract | Warning | XML, YAML |
| Missing Symfony container constant or enum | Error | XML, YAML |
| Unknown YAML service named argument | Warning | YAML |
| Missing configured service or event-listener method | Warning | PHP, XML, YAML |
| Incompatible service supplied to a constructor/method argument | Warning | XML, YAML, PHP |
| Deprecated controller action | Hint | PHP, Twig, XML, YAML |
| Deprecated Twig function, filter, custom tag, or typed PHP member | Hint | Twig |
| PHP method call from Twig with missing or incompatible arguments | Warning | Twig |
| Missing or non-enum class in `enum()` / `enum_cases()` | Warning | Twig |
| Invalid `_self` macro import inside a Twig component | Error | Twig |

In PHP files, every diagnostic honors PHPStan and PhpStorm suppression
comments. `@phpstan-ignore <code>`, `@phpstan-ignore-line` and
`@phpstan-ignore-next-line` accept Shopware LSP codes such as
`shopware.php.superglobal` or `symfony.service.missing`; the forms without a
code suppress every diagnostic on the targeted line. `@noinspection` accepts
codes and the matching PhpStorm inspection names for core PHP diagnostics.

Twig, YAML, XML, JavaScript/TypeScript and SCSS files honor `@noinspection`
in their comments (`{# @noinspection twig.macro.missing #}`,
`# @noinspection symfony.service.missing`, `<!-- @noinspection … -->`,
`// @noinspection …`). A comment sharing a line with code suppresses that
line; a comment on its own line suppresses the next line with code. Several
codes may be listed, separated by spaces or commas; without a code, `*` or
`all`, every diagnostic on the line is suppressed.

When a file has several problems of one code with the same quick fix, the
quick-fix menu also offers *Fix all N '<code>' problems in this file*. It
applies the fix to one problem after another, re-checking the file between
steps, and returns one edit, so fixes that share changes (such as an import
several occurrences need) combine correctly.

[`inspections.md`](inspections.md) lists every inspection with its
description, languages, domain and diagnostic codes, generated from the
server.

Clients supporting workspace diagnostics (VS Code's problems panel) also
show problems that rules about the whole project, such as
`symfony.route.redefined`, find in files that are not open. Diagnostics of a
document do not wait for slow inspections: the results of the fast ones are
published first. An inspection taking longer than 200 ms is logged once as a
warning; `SHOPWARE_LSP_TRACE_DIAGNOSTICS=1` logs every inspection's time.

The former `shopware.admin` inspection is split into
`shopware.admin.component`, `shopware.admin.component_api`,
`shopware.admin.member`, `shopware.admin.module`,
`shopware.admin.block_override` and `shopware.admin.migration`, and
`shopware.migration` into `shopware.migration.api`,
`shopware.migration.signature` and `shopware.migration.behavior`. Diagnostic
codes are unchanged; `diagnostics.inspections` entries naming the old IDs fail
validation and must use the new ones.

### Commands
- `shopware/forceReindex` - Trigger a full re-index of the workspace
- `shopware/cache/list` - List the workspace caches of this machine
- `shopware/cache/clear` - Delete the stale or all caches of other projects

## Supported File Types

| File Type | Features |
|---|---|
| PHP (.php) | Completion, hover, go-to-definition, references, signature help, rename, diagnostics, code actions, code lens |
| Twig (.twig) | Completion, go-to-definition, hover, diagnostics, code actions, code lens |
| XML (.xml) | Completion, go-to-definition, diagnostics, code actions |
| YAML (.yaml, .yml) | Completion, go-to-definition, diagnostics, code actions |
| JSON (.json) | Indexed for snippets and theme config |
| JavaScript (.js) | Completion, go-to-definition, hover, diagnostics (admin) |
| TypeScript (.ts) | Completion, go-to-definition, hover, diagnostics (admin) |
| Vue SFC (.vue) | Administration component indexing and template/script/style language features |
| SCSS (.scss) | Completion, go-to-definition, unknown-variable diagnostics, color swatches and color picker |
| Dotenv (.env, .env.*, *.env) | Go-to-definition, references, hover |
| Dockerfile / Compose | Environment declaration navigation, references, hover |

## Development

### Architecture

The executable is a small composition root. The LSP `initialize` request
selects the workspace; only then does `swlsp-app` (`crates/app`) construct the scanner,
shared index store, domain indexes, and feature providers. One server process
supports one workspace root. Multi-root clients should start one process per
workspace.

Language parsing is registered centrally in `swlsp-language`. PHP,
Twig/HTML, XML, YAML, JSON, JavaScript/TypeScript, SCSS, Vue, and embedded
XPath use the native lossless CST frontends in the `crates/parser-*` crates,
built on the shared `swlsp-syntax` tree; protocol DTOs do not contain
parser-specific state. A file is parsed once and the immutable syntax context is shared across
indexers and LSP providers.

Workspace index data lives in one namespaced SQLite store. File replacement,
deletion, and force-clear operations are transactional across repositories;
file fingerprints are committed only after all indexers succeed. Filesystem
changes have one source of truth: the scanner's watcher, built on the `notify`
crate (inotify on Linux, `ReadDirectoryChangesW` on Windows, and native
FSEvents on macOS, which avoids kqueue's one-descriptor-per-file cost on large
Shopware workspaces). All workspace resources are closed by the
application lifecycle.

The contributor-facing architecture documentation is indexed in
[`docs/README.md`](README.md). Start with the
[system architecture overview](architecture.md), then the subsystem you need:
[parser, CST, and queries](parser-architecture.md),
[indexing and persistence](indexing.md),
[LSP server and providers](lsp-server.md),
[diagnostics and quick fixes](diagnostics-pipeline.md), or the
[refactoring engine](refactoring-engine.md).
The PHP semantic pipeline and its extension contracts are documented in
[`php-semantic-engine.md`](php-semantic-engine.md).
The staged feature mapping from the PhpStorm Symfony plugin to LSP capabilities
is tracked in [`symfony-plugin-roadmap.md`](symfony-plugin-roadmap.md).

### Requirements

- Rust via rustup; `rust-toolchain.toml` pins the toolchain (1.98.0, with
  clippy and rustfmt) and rustup installs it on first use
- A C compiler (SQLite is compiled into the binary through `rusqlite`)
- Node.js 24 for VSCode extension development

Common tasks live in the `Makefile`:

```bash
make build        # target/release/shopware-lsp
make rust-check   # cargo fmt --check, clippy -D warnings, cargo test,
                  # and compilation of the real-world integration suite
make check        # Rust, VS Code and Zed validation
```

Release binaries are compiled natively per target in CI
(`.github/workflows/build-binaries.yml`) for macOS, Linux (a glibc build and
a static musl build for Alpine), and Windows, then packaged by GoReleaser
(`.goreleaser.yaml`: archives and `deb`/`rpm`/`apk` packages) and
`scripts/build-vsix-release.mjs`. `make release` builds the host binary and
packages only the host VSIX plus `SHA256SUMS` into `out/`; pass
`--pre-release` to the script to mark it as a VSCode pre-release:

```bash
node scripts/build-vsix-release.mjs --local --pre-release
```

See [RELEASING.md](../RELEASING.md) for the full pipeline.

The VSCode Marketplace and Open VSX only accept numeric `major.minor.patch`
extension versions; semver pre-release identifiers such as `0.3.0-rc.1` are
rejected at publish time. The repository therefore follows the VS Code
versioning convention: odd minors are pre-releases, even minors are stable.

Pushing an odd-minor tag (for example `0.3.0`) runs the `Pre-release` GitHub
Actions workflow. The workflow runs the test suite, builds all
platform-specific pre-release packages through the shared `build-vsix`
workflow, verifies their `SHA256SUMS`, and attaches every VSIX to a GitHub
pre-release. A separate publish job, gated by the `preview` deployment
environment, then publishes the verified packages to the VSCode Marketplace
and Open VSX pre-release channels. Even-minor tags (for example `0.4.0`) run
the `Release` workflow, which reuses the same build and verification steps,
attaches the packages to the stable GitHub release, and publishes through the
`release` deployment environment.

### Building

```bash
cargo build --locked --release -p swlsp-cli
```

Set `SHOPWARE_LSP_VERSION=<version>` to embed a version; otherwise the binary
reports `dev`.

### Testing

Run the tests with:

```bash
cargo test --locked --workspace
```

Parser and PHP-engine benchmarks live in `crates/bench` and run in CI through
CodSpeed:

```bash
cargo bench -p swlsp-bench
```

The production indexing composition also has an opt-in real-world suite in
`crates/app/src/tests/real_world`, behind the `integration` feature of
`swlsp-app`. It uses `~/Developer/sw-trunk` by default, or a checkout selected
through `SHOPWARE_LSP_REAL_WORLD_ROOT`, and reports cold indexing, warm no-op,
cache restore, class count, memory, and first/p50/p95 end-to-end JSON-RPC
latency for hover, definition, references, code-action, diagnostic,
document-link, and document-symbol requests. The latency census executes
through an in-memory LSP transport with the production provider composition
and enforces broad interactive budgets:

```bash
SHOPWARE_LSP_REAL_WORLD_ROOT=/path/to/sw-trunk \
  cargo test --release -p swlsp-app --features integration --lib \
  tests::real_world::indexing_main::shopware_trunk_indexing -- --exact --nocapture
```

The same request census can run independently of the broader moving-fixture
feature assertions:

```bash
cargo test --release -p swlsp-app --features integration --lib \
  shopware_trunk_request_latency -- --nocapture
```

The feature keeps ordinary tests and CI independent of a developer-local
Shopware checkout; `make rust-test-integration-compile` only compiles it.

Set `SHOPWARE_LSP_TRACE_DIAGNOSTICS=1` when launching the server to log the
elapsed time and finding count of every inspection plus normalization and
total diagnostic-request time. This is intended for profiling a slow document;
leave it unset during ordinary editing to avoid verbose output.

Set `SHOPWARE_LSP_TRACE_PROVIDERS=1` to log per-provider and total timings for
hover, definition, and references requests. This isolates cold or path-specific
framework providers without adding timing overhead during normal requests.

The real-world integration benchmark uses 21 request samples by default. Set
`SHOPWARE_LSP_LATENCY_SAMPLES` to a value of at least 2 for a shorter profiling
run; the first request is reported separately and excluded from p50/p95.

### Updating PHP runtime stubs

Runtime declarations are generated from a pinned
[JetBrains phpstorm-stubs](https://github.com/JetBrains/phpstorm-stubs)
checkout. The server embeds the compact generated catalog and does not parse
the upstream PHP files at startup. Hand-written declarations in
`crates/php-stubs/src/overlay.rs` are a semantic overlay for generics and inference
contracts that are intentionally more precise than native signatures.

To update the catalog, check out the revision recorded in
`crates/php-stubs/data/phpstorm-stubs.lock.json` and run from the repository
root:

```bash
cargo run --release -p swlsp-php-stubs-generate --bin generate-phpstorm-stubs -- \
  -source /path/to/phpstorm-stubs
cargo test -p swlsp-php-stubs -p swlsp-php-stubs-generate
```

The generator writes `crates/php-stubs/data/phpstorm-stubs.msgpack`.

Change the locked revision deliberately in the same commit as the generated
catalog. Generation verifies the checkout revision and is byte-for-byte
deterministic.

At runtime the catalog is materialized by extension bundle. Core PHP bundles
are always loaded, while Composer `require` and `require-dev` entries named
`ext-*` select optional bundles. VS Code settings
`shopwareLSP.phpExtensions` and `shopwareLSP.disabledPhpExtensions` can add
undeclared runtime extensions or record extensions known to be unavailable.
Composer `config.platform` entries set to `false` are also treated as explicit
disables. The server only emits `php.extension` when it has that negative
runtime evidence; an optional extension omitted from Composer is not assumed
to be unavailable.

### Project configuration

Shopware LSP reads the committed project configuration from
`.config/shopware-lsp.yaml`. The same configuration is used by editor
sessions and CLI commands, so diagnostic policy does not need to be duplicated
in CI. VS Code user and workspace settings are local overrides; explicit CLI
flags such as `check --severity` and `check --fail-on` take final precedence.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/shopware/shopware-lsp/main/crates/projectconfig/schema.json
version: 1
php:
  extensions: [redis]
  disabledExtensions: []
shopware:
  targetVersion: "6.7"
features:
  codeLens: false
indexing:
  # Zero disables this configurable ceiling; the parser limit remains.
  maxFileSizeMiB: 8
  exclude:
    - "**/generated/**"
    - custom/plugins/LegacyPlugin/**
mcp:
  tools:
    shopware_apply_code_action: false
    shopware_entity_schema_apply: false
domains:
  symfony.doctrine: false
diagnostics:
  enabled: true
  inspections:
    shopware.admin.migration: false
  rules:
    php.arguments: error
    admin.component.unknown-instance-member: off
  overrides:
    - files: [src/Generated/**]
      enabled: false
    - files: [custom/plugins/FroshTools/**]
      rules:
        php.arguments: off
check:
  severity: warning
  failOn: error
```

Unspecified values inherit the built-in defaults, which keep all current
features, MCP tools, domains, indexing, and diagnostics enabled. Diagnostic rule values
are `off`, `hint`, `information`, `warning`, or `error`. Disabling a complete
inspection skips its analyzer; disabling every rule in an inspection has the
same optimization. Domain dependencies cascade off, so disabling PHP also
disables PHP-backed Symfony and Twig domains.

`indexing.exclude` contains workspace-relative glob patterns. Excluded files do
not enter persistent indexes or workspace-wide results, while explicitly open
documents remain available for document-local language features. Patterns use
forward-slash paths and support `*`, `?`, `**`, brace alternatives such as
`*.{php,twig}`, and character classes such as `[0-9]`. Absolute paths and `..`
components are rejected. Changing exclusions requires a server restart and
invalidates the structurally incompatible workspace cache.

`indexing.maxFileSizeMiB` defaults to `8`. Larger source files are skipped
before reading or parsing and are listed by `shopware-lsp info --stats`. Set it to `0`
to disable the configurable ceiling; files outside the parser's 32-bit source
range are always rejected. The corresponding local VS Code override is
`shopwareLSP.indexing.maxFileSizeMiB`. Changing the limit requires a server
restart.

Shopware extensions may commit their own
`.config/shopware-lsp.yaml`, for example
`custom/plugins/FroshTools/.config/shopware-lsp.yaml`. Nested files are
diagnostics-only and apply to their containing directory. Their `files`
patterns are relative to the extension root, so the same file works when the
extension is installed in a project or opened as its own repository. Scoped
configuration is applied from the workspace root toward the nearest extension;
VS Code-local overrides apply last. Patterns use the same glob syntax described
above.

The VS Code command `Shopware: Configure Language Server…` provides searchable
feature, domain, inspection, and rule controls and can write either the shared
project file or a local editor override. Diagnostic and request-time feature
changes apply live. Indexing, domain, PHP extension, and target-version changes
prompt for a language-server restart and invalidate structurally incompatible
workspace caches. `Shopware: Open Configuration…` opens or creates the root or
extension-local file. Every Shopware diagnostic also offers a configuration
quick fix for suppressing its rule—or all diagnostics—for a file, directory,
extension, or workspace.

For CI, `shopware-lsp info --json` validates and prints the effective configuration.
`shopware-lsp check` fails before indexing when the committed configuration is
invalid. Its command-line severity flags override the `check` defaults from the
file; use `--fail-on off` to explicitly suppress the configured failure threshold
for one invocation.

For a resource-only measurement without the feature assertions, run the
index profile harness
(`crates/app/src/tests/real_world/index_profile.rs`) through the operating
system's resource counter:

```bash
SHOPWARE_LSP_REAL_WORLD_ROOT=/path/to/sw-trunk /usr/bin/time -v \
  cargo test --release -p swlsp-app --features integration --lib \
  shopware_trunk_indexing_profile -- --nocapture
```

Build the test binary first (`cargo test --no-run ...`) so compilation does not
count toward the measured resources. The profile reports construction, cold
and warm scan time, current and peak RSS, and cache size. Set
`SHOPWARE_LSP_PROFILE_CACHE_DIR` to keep the cache in a chosen directory,
`SHOPWARE_LSP_PROFILE_REVERSE_REFERENCES=1` to additionally report the
one-time cost of constructing the lazy PHP reverse-reference index, and
`SHOPWARE_LSP_PROFILE_SEMANTIC_STORAGE=1` to report compact PHP graph
cardinality. `SHOPWARE_LSP_PROFILE_WORKERS` overrides indexing concurrency for
comparative resource measurements. The production scanner defaults to three
quarters of the cores, at most eight workers, plus one writer thread.

For end-to-end comparisons of two server binaries, `scripts/bench/lsp_bench.py`
drives a server over stdio with a fixed request mix on a real checkout and
records index time, RSS, per-request latency, and a response hash;
`scripts/bench/compare.py` diffs two runs. Run the servers one after the
other, never concurrently, and use isolated cache directories.

On a Shopware checkout, the November 2026 run of that benchmark measured
21.5 s cold index, 333 MiB RSS after indexing, 759 MiB peak, and 125.7 s for
the full request mix. These are local reference numbers, not timing
guarantees.

### CI/CD

This project uses GitHub Actions for continuous integration:

- Tests (`cargo test` on Linux, macOS, and Windows, plus the VS Code and Zed
  suites) run on every push and pull request
- Formatting and linting use `cargo fmt --check` and
  `cargo clippy -D warnings`; the third-party notices must be up to date
- The real-world integration suite is compiled and the release server is
  built for verification
- CodSpeed tracks the `crates/bench` benchmarks

## License

[MIT License](../LICENSE)
