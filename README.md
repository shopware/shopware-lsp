# Shopware Language Server

A Language Server Protocol (LSP) implementation for Shopware and Symfony development, providing IDE features across PHP, Twig, XML, YAML, JavaScript/TypeScript, SCSS, and JSON files.

Editor authors can use the versioned integration contract and framework-only
presentation profile documented in the
[PhpStorm LSP integration guide](docs/phpstorm-integration.md). The profile
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
shopware-lsp -root /path/to/project -json project-info
shopware-lsp -root /path/to/project index
shopware-lsp -root /path/to/project index -force
shopware-lsp -root /path/to/project stats
shopware-lsp -root /path/to/project check src tests
shopware-lsp -root /path/to/project check -fail-on error src/Controller.php
shopware-lsp -root /path/to/project definition src/Controller.php:24:18
shopware-lsp -root /path/to/project references src/Controller.php:24:18
shopware-lsp -root /path/to/project workspace-symbol customer.detail
shopware-lsp -root /path/to/project workspace-symbol --fresh customer.detail
shopware-lsp -root /path/to/project codeaction -kind quickfix src/Controller.php:24:18
shopware-lsp -root /path/to/project rename -d src/Controller.php:24:18 NewName
shopware-lsp -root /path/to/project mcp
```

Workspace commands run only for detected Shopware or Symfony roots. Detection
is intentionally bounded to root metadata: Shopware Composer metadata or app
manifest files, `symfony/framework-bundle` or `config/bundles.php`, and the
committed `.config/shopware/lsp.json` opt-in. Use `project-info` to see
the detected kind and its evidence without creating a workspace cache. For an
unusual but intentional root, either add the project configuration or pass the
global `-allow-unsupported-project` flag before the command. The same guard is
enforced by the language-server and MCP entry points.

Positions are one-based `file:line:column` values. Structured feature commands
emit JSON. `check` and `stats` default to readable text; put the global `-json`
flag before the command for machine-readable output. `check` recursively
expands directory targets and accepts multiple, overlapping files or
directories. It uses up to four diagnostic workers by default; pass
`-workers 1` for serial analysis or another positive limit for a particular
machine. `codeaction -exec` and `rename` preview a unified diff unless `-w` is
supplied, so commands do not modify source files by default.

`workspace-symbol` normally reads the populated SQLite FTS catalog directly,
without restoring the PHP semantic graph or rescanning the workspace. Use
`--fresh` when a one-shot command must validate the filesystem through the
full production LSP session first. Textual relevance is ranked before explicit
symbol tiers; equally relevant PHP types rank above PHP globals, framework
objects, PHP members, and framework members.

The CLI exposes completion, definition, implementation, references, hover,
signature help, call/type hierarchy, document and workspace symbols,
highlights, folding ranges, links, semantic tokens, inlay hints, code lenses,
selection ranges, linked editing, colors, code actions, and rename. Use
`shopware-lsp execute` to list the registered `shopware/...` analytics and
generator requests, or `shopware-lsp request` as a generic JSON-RPC escape
hatch.

Operational flags include `-profile.cpu`, `-profile.mem`, and
`-profile.trace`. `serve` additionally supports `-logfile`, `-rpc.trace`, a
runtime profiling endpoint through `-debug`, and a single-client TCP or Unix
socket through `-listen`.

### MCP server

The `mcp` command exposes the production workspace index, diagnostics, and
refactoring engine to AI clients over MCP's stdin/stdout transport. For
example, an MCP client configuration can launch one server per workspace:

```json
{
  "mcpServers": {
    "shopware": {
      "command": "/absolute/path/to/shopware-lsp",
      "args": ["-root", "/absolute/path/to/project", "mcp"]
    }
  }
}
```

The server advertises diagnostics, code actions, hover, definitions,
references, and workspace-symbol search. It also exposes the production
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
  `!php/const`, and Composer-version-aware service argument tags such as
  `!tagged_iterator`, `!tagged_locator`, and `!service_locator`
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
- Hint diagnostics for legacy `factory_class` / `factory_method` /
  `factory_service` and XML factory-attribute forms
- XML/YAML service-tag contract diagnostics for conventional tags such as
  `twig.extension`, `form.type`, `security.voter`, and
  `kernel.event_subscriber`, including modern/legacy interface alternatives
- Symfony XML/YAML container-constant completion, PHP go-to-definition, and
  missing-reference diagnostics for global, inherited class, and enum
  constants, including modern and legacy `!php/const` YAML syntax
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
  templates/blocks/macros/extensions, Doctrine entities/tables, Twig/Live
  Components, and translation domains/keys
- A structured route analytics request correlates route sources, normalized
  methods and URLs, resolved controller locations, and rendered Twig
  templates, with route/controller/URL/Ant-glob filters; `Symfony: Browse
  Routes…` exposes the catalog as a searchable command-palette navigator
- Local Symfony profiler analytics discover modern `var/cache/*/profiler` and
  legacy `app/cache/*/profiler` indexes, read bounded raw or gzip profile data,
  and correlate recent requests with routes, controller locations, static and
  runtime Twig templates, root form types, rendered Symfony UX Twig
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

### Symfony Security

- Persistent user-provider and firewall symbols from SecurityBundle XML,
  YAML, and typed `Symfony\Config\SecurityConfig` PHP configuration
- Cross-format provider completion, navigation, references, hover, missing-name
  diagnostics, cache restore, and unsaved-document overlays
- Chain, firewall, nested authenticator, and switch-user provider references,
  including fluent PHP configurator aliases
- Source-aligned nested SecurityBundle YAML key completion and hover for form,
  JSON, LDAP, Basic, login-link, throttling, remember-me, remote-user, X.509,
  logout, switch-user, and access-token/OIDC configuration, including
  `when@environment` sections

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
  context menu for Console commands, controllers, form types, Twig extensions,
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
- PHPDoc `#Class`, `#Interface`, and `#ClassInterface` parameter contracts
  provide kind-filtered, exact-edit class-name completion and declaration
  navigation plus kind-aware missing-reference diagnostics and typo fixes at
  function, constructor, and inherited method call arguments
- Context-aware `#` / `#[…]` attribute completion with conflict-safe imports
  for controllers, Console commands, Twig extensions/components, and Doctrine
  entities; lifecycle callbacks also add the required class marker
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
- Template path completion in Twig files (`extends`, `include`, `sw_extends`, `sw_include` tags)
- Template path completion in PHP files (`renderStorefront` method calls)
- Go-to-definition for template paths in Twig and PHP files
- Exact standalone PHP strings ending in `.twig` also navigate when their
  logical template exists; they remain excluded from usage indexing and rename
  so ordinary configuration strings do not create false semantic edges
- PHPDoc `#Template` parameter contracts provide exact template-name
  completion, multi-target definition navigation, missing-reference
  diagnostics, and typo fixes at matching call arguments
- Missing-template diagnostics for typed PHP render calls and explicit or
  convention-guessed `#[Template]` / `@Template` declarations, with typo
  replacements and safe standard `templates/` file-creation quick fixes
- Find References across static Twig tags/functions, PHP render calls, and
  explicit `#[Template]`/`@Template` mappings
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
- Standard semantic coloration for both Twig `{# @var variable Type #}` and
  `{# @var Type variable #}` annotations, including unions, array types,
  multiple declarations, and PHPDoc-style `$variable` names
- Twig `{# @see … #}` completion/navigation for PHP classes and methods,
  controller service actions, logical templates, and safe workspace-relative
  files, including the legacy single-target comment syntax
- Persistent custom-operator indexing and context-aware completion for legacy
  `getOperators()` and Twig 3.21+/4 `getExpressionParsers()`, including named
  and positional aliases
- Controller `createForm(...)->createView()` provenance with Twig form-field
  completion, PHP definition navigation, typed hover, typo diagnostics, and an
  interactive generator for selected `{{ form_row(variable.field) }}` calls;
  `form.vars.*` keys written by type/core/extension `buildView()` and
  `finishView()` methods provide the same completion/navigation/hover checks
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
- Doctrine entity and table symbols, with ranked results and exact source
  ranges where available

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
- Deprecated controller class/method diagnostics on XML/YAML/Twig controller
  references and PHP/Twig route-name usages
- Hint diagnostics for legacy route `pattern` and `_method` / `_scheme`
  requirement settings in YAML and XML
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

### Symfony Form Support
- PHP form types/extensions and XML, YAML, and PHP service aliases
- PHPDoc `#FormType` parameter contracts provide exact alias/class completion
  and PHP declaration navigation plus missing-reference diagnostics and typo
  fixes at matching call arguments
- Inherited type options, builder fields, `data_class` properties, completion,
  navigation, hover, diagnostics, and typo quick fixes
- Provenance-gated Twig `FormView` child completion, definition, hover, and
  missing-field diagnostics from controller-rendered form types, plus
  persisted `FormView.vars` metadata from form types, extensions, and the core
  `FormType`
- Semantic form-factory recognition for `createForm`, `create`,
  `createBuilder`, `createNamed`, and `createNamedBuilder`
- Symfony 2.8+ diagnostics for deprecated builder string aliases, with a
  conflict-safe quick fix that imports and inserts the form type `::class`
- Interactive `buildForm()` field generation from writable `data_class`
  properties and setters, with inherited-field discovery, existing-field
  exclusion, scalar/date/enum/Doctrine type guessing, options, and
  conflict-safe imports
- Related form-type code lenses on public PHP methods, with class-constant,
  legacy-alias, and named-argument resolution
- FormType navigation code lenses on controller-backed Twig `form_start()`,
  `form()`, `form_end()`, and `form_rest()` calls
- Bidirectional form-type ↔ `data_class` code lenses, including multiple forms
  for one model, exact class targets, and unsaved form configuration overlays
- Structured form-type and effective-option analytics include aliases, parent
  chains, data classes, field/view-variable counts, defaults, allowed types,
  declaration kinds, and exact source provenance; `Symfony: Browse Form
  Types…` provides the searchable type-to-option navigator

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

### Doctrine Support
- Unified ORM/ODM metadata from PHP attributes and annotations plus external
  XML and YAML mappings
- Completion can bootstrap empty XML mapping attributes for model,
  repository, property, relation, embedded, enum, lifecycle, and type values;
  root ODM embedded-document mappings participate as embeddable models
- MongoDB ODM PHP `ReferenceOne/Many` and `EmbedOne/Many` mappings resolve
  `targetDocument` in both attributes and legacy annotations, including
  untyped properties
- Entity, repository, relation, field, lifecycle, mapping-type, Criteria,
  DQL, QueryBuilder, and magic-finder intelligence
- Legacy ORM/ODM `Bundle:Model` namespace shortcuts from the compiled Symfony
  container, with convention-based bundle fallbacks, completion, navigation,
  hover/diagnostics, DQL/QueryBuilder resolution, and repository result typing
- PHP inference for direct object-manager `find()` calls and mapped custom
  repository classes, preserving entity result types while exposing custom
  repository methods
- Doctrine field analytics preserve resolved PHP nullable, union,
  intersection, and parenthesized DNF property types instead of flattening
  intersections or dropping `null`
- Structured Doctrine analytics requests expose model kind/source, table,
  repository, inheritance, source locations, inherited/embedded field paths,
  mapping/PHP/enum/relation types, declaring classes, and table
  index/unique-constraint counts; `Symfony: Browse Doctrine Entities…`
  provides a searchable entity-to-field source navigator
- PHPDoc `#Entity` parameter contracts provide exact mapped-model completion
  and navigation to PHP or external mapping declarations, with
  missing-reference diagnostics and typo fixes
- Scope-aware ORM class/property/lifecycle attribute completion that preserves
  existing `Doctrine\ORM\Mapping` aliases
- Inheritance/discriminator metadata from PHP attributes, legacy annotations,
  XML, and YAML, with subtype-filtered discriminator-class completion,
  definition, hover, missing/invalid-target diagnostics, typo fixes, and
  reference navigation
- Table indexes and unique constraints from PHP attributes/legacy annotations,
  XML, and YAML retain exact `fields`/`columns` members; completion,
  property navigation, hover, typo diagnostics/fixes, Find References, cache
  restore, and entity-catalog counts share the normalized mapping model
- Built-in and custom DBAL/ODM mapping-type completion, navigation, hover, and
  validation; custom type names resolve literal and class-constant-backed
  `getName()` returns, static DoctrineBundle YAML/XML aliases and PHP
  `extension('doctrine', …)` `dbal.types` aliases, and the conventional
  `FooBarType` → `foo_bar` fallback. PHP configuration supports imported
  `::class`, string, and expanded `['class' => …]` values, including
  incomplete editing states. Static runtime `Type::addType()` /
  `Type::overrideType()` and `Type::getTypeRegistry()->register()` calls are
  indexed as well, with class-string/class-constant and object-instance-aware
  completion. ORM, MongoDB, CouchDB, and generic ODM mapping filenames receive
  their matching type families; registration class values provide
  subtype-filtered completion, PHP navigation, hover, missing/invalid-class
  diagnostics, and typo fixes. Find References links registration keys and
  implementation declarations with PHP attributes plus XML/YAML mapping
  usages
- Native standalone DQL strings in `$dql` assignments and typed
  `EntityManager::createQuery()`/`Query::setDQL()` calls, including entity and
  relation-alias fields, completion, navigation, hover, diagnostics, typo
  actions, persistent Find References, and cache restore
- Cached built-in DQL function discovery from Doctrine ORM's parser registry,
  with function completion, navigation to the implementation class, and hover
- Assigned and fluent QueryBuilder chains, including relation/class joins,
  nested `Expr` methods, `indexBy` fields, and inferred query parameters
- Typed DBAL QueryBuilder/Connection table, column, and join-alias completion,
  navigation, hover, diagnostics, and typo actions backed by ORM table metadata
- Bidirectional related-navigation code lenses between PHP models and external
  mappings
- Public-method code lenses from typed manager-registry, object-manager,
  QueryBuilder, and Doctrine cache calls to PHP and external model declarations
- Contextual, type-checked migration of string entity names in repository and
  object-manager calls (including `find`) to conflict-safe `::class` references

### Stimulus Support
- Persistent controller discovery from conventional `_controller`/
  `-controller` JavaScript and TypeScript files, explicit
  `startStimulusApp()` registrations, and enabled `controllers.json` entries
- Controller completion and navigation in Twig `stimulus_controller()` calls
  and `data-controller` attributes in Twig or plain HTML
- Cross-template references, hover, missing-controller diagnostics, typo quick
  fixes, stale removal, and cache restore; plain HTML is parsed on demand

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
| Non-existent parent component | Error | JS/TS (admin) |
| Outdated block version hash | Warning | Twig |
| Missing block version comment | Warning | Twig |
| Undefined PHP classes, variables, and members | Warning | PHP |
| PHP argument, constructor, and return mismatch | Error | PHP |
| Invalid PHP visibility or override | Error | PHP |
| Missing abstract/interface implementation | Error | PHP |
| Missing Stimulus controller | Warning | Twig, HTML |
| Missing Messenger subscriber handler method | Warning | PHP |
| Missing Messenger message or configured handler method | Warning | PHP, XML, YAML |
| Invokable Symfony command should declare `int` | Hint | PHP |
| Invokable Symfony command returns a non-integer exit code | Warning | PHP |
| Deprecated Symfony form-type string alias | Hint | PHP |
| Deprecated invalid escape or unquoted YAML indicator | Hint | YAML |
| Deprecated colon in an unquoted YAML mapping value | Hint | YAML |
| Deprecated Symfony service or service class | Hint | PHP, XML, YAML |
| Service class violates a conventional tag contract | Warning | XML, YAML |
| Missing Symfony container constant | Error | XML, YAML |
| Unknown YAML service named argument | Warning | YAML |
| Missing configured service or event-listener method | Warning | PHP, XML, YAML |
| Incompatible service supplied to a constructor/method argument | Warning | XML, YAML, PHP |
| Deprecated controller action or legacy route/DI setting | Hint | PHP, Twig, XML, YAML |
| Deprecated Twig function, filter, custom tag, or typed PHP member | Hint | Twig |
| Missing or non-enum class in `enum()` / `enum_cases()` | Warning | Twig |
| Invalid `_self` macro import inside a Twig component | Error | Twig |

### Commands
- `shopware/forceReindex` - Trigger a full re-index of the workspace

## Supported File Types

| File Type | Features |
|---|---|
| PHP (.php) | Completion, hover, go-to-definition, references, signature help, rename, diagnostics, code actions, code lens |
| Twig (.twig) | Completion, go-to-definition, hover, diagnostics, code actions, code lens |
| HTML (.html) | Stimulus completion, go-to-definition, references, hover, diagnostics, code actions |
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
selects the workspace; only then does `internal/app` construct the scanner,
shared index store, domain indexes, and feature providers. One server process
supports one workspace root. Multi-root clients should start one process per
workspace.

Language parsing is registered centrally in `internal/language`. PHP,
Twig/HTML, XML, YAML, JSON, JavaScript/TypeScript, SCSS, and embedded XPath use
the native lossless CST frontends in `internal/parser`; protocol DTOs do not contain parser-specific
state. A file is parsed once and the immutable syntax context is shared across
indexers and LSP providers.

Workspace index data lives in one namespaced SQLite store. File replacement,
deletion, and force-clear operations are transactional across repositories;
file fingerprints are committed only after all indexers succeed. Filesystem
changes have one source of truth: `fsnotify` on Linux/Windows and native
FSEvents on macOS. FSEvents avoids kqueue's one-descriptor-per-file cost on
large Shopware workspaces. All workspace resources are closed by the
application lifecycle.

The PHP semantic pipeline and its extension contracts are documented in
[`docs/php-semantic-engine.md`](docs/php-semantic-engine.md).
The staged feature mapping from the PhpStorm Symfony plugin to LSP capabilities
is tracked in [`docs/symfony-plugin-roadmap.md`](docs/symfony-plugin-roadmap.md).

### Requirements

- Go 1.26.5 or higher
- A C compiler with CGO enabled (the index uses native SQLite)
- Node.js 24 for VSCode extension development
- Docker for cross-compiling release binaries

The repository includes a `mise.toml` for reproducible tool versions and
common development tasks:

```bash
mise install
mise run setup
mise run check
```

Run `mise tasks` to list the individual backend and VSCode extension tasks.
`mise run release` uses the pinned GoReleaser cross-build container to create
platform-specific VSIX packages and `SHA256SUMS` in `out/`. It keeps CGO
enabled for every server binary and covers macOS, Linux, Alpine, and Windows.
Pass `--pre-release` through mise to mark every VSIX as a VSCode pre-release:

```bash
mise run release -- --pre-release
```

Every commit pushed to `feat/next-gen` also runs the `VSIX Preview` GitHub
Actions workflow. The workflow assigns a unique odd-minor preview version,
builds all platform-specific pre-release packages, and stores each target's
VSIX plus a separate `SHA256SUMS` file as commit-associated artifacts for 14
days. It does not publish to the VSCode Marketplace, Open VSX, GitHub Releases,
or Homebrew. Open the workflow check for a commit, download the artifact for
your platform, and install it with VSCode's **Extensions: Install from VSIX...**
command.

### Building

```bash
go build
```

### Testing

Run the tests with:

```bash
go test ./...
```

Or run tests with race condition detection:

```bash
go test -race ./...
```

The production indexing composition also has an opt-in real-world test. It
uses `~/Developer/sw-trunk` by default, or a checkout selected through
`SHOPWARE_LSP_REAL_WORLD_ROOT`, and reports cold indexing, warm no-op, cache
restore, class count, heap measurements, and p50/p95 end-to-end JSON-RPC
latency for hover, code-action, diagnostic, document-link, and document-symbol
requests. The latency census executes through an in-memory LSP transport with
the production provider composition and enforces broad interactive budgets:

```bash
go test -tags=integration ./internal/app \
  -run '^TestShopwareTrunkIndexing$' -count=1 -v
```

The integration build tag keeps ordinary tests and CI independent of a
developer-local Shopware checkout.

Set `SHOPWARE_LSP_TRACE_DIAGNOSTICS=1` when launching the server to log the
elapsed time and finding count of every inspection plus normalization and
total diagnostic-request time. This is intended for profiling a slow document;
leave it unset during ordinary editing to avoid verbose output.

The real-world integration benchmark uses 21 request samples by default. Set
`SHOPWARE_LSP_LATENCY_SAMPLES` to a value of at least 2 for a shorter profiling
run; the first request remains an unmeasured warm-up.

One representative August 2026 run against a 60,811-file `sw-trunk` checkout
reported the following warm request latency after indexing. These are local
reference measurements rather than portable timing guarantees:

| LSP request | p50 | p95 |
|---|---:|---:|
| PHP hover | 0.45 ms | 0.50 ms |
| Administration Twig hover | 0.13 ms | 0.15 ms |
| PHP code actions, all kinds | 1.09 ms | 1.37 ms |
| PHP quick fixes only | 0.03 ms | 0.07 ms |
| Administration Twig code actions, all kinds | 0.09 ms | 0.11 ms |
| Administration Twig diagnostics | 0.09 ms | 0.10 ms |
| Administration diagnostic quick fixes | 0.49 ms | 0.51 ms |
| Code-action resolve | 0.56 ms | 0.61 ms |
| Twig document links | 0.06 ms | 0.06 ms |
| Administration TypeScript document symbols | 0.46 ms | 0.48 ms |
| Administration Twig document symbols | 0.10 ms | 0.10 ms |

The diagnostics row is an unchanged-document pull after the scheduled publish
pass, so it reuses the versioned diagnostic result. On the same fresh index,
the uncached Administration Twig analysis after opening the document took
30.8 ms end to end, including 24.4 ms in the Administration inspection.

### Updating PHP runtime stubs

Runtime declarations are generated from a pinned
[JetBrains phpstorm-stubs](https://github.com/JetBrains/phpstorm-stubs)
checkout. The server embeds the compact generated catalog and does not parse
the upstream PHP files at startup. Hand-written declarations in
`internal/php/stubs/stubs.go` are a semantic overlay for generics and inference
contracts that are intentionally more precise than native signatures.

To update the catalog, check out the revision recorded in
`internal/php/stubs/phpstorm-stubs.lock.json` and run:

```bash
go run ./cmd/generate_phpstorm_stubs -source /path/to/phpstorm-stubs
go test ./internal/php/stubs/...
```

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
`.config/shopware/lsp.json`. The same configuration is used by editor
sessions and CLI commands, so diagnostic policy does not need to be duplicated
in CI. VS Code user and workspace settings are local overrides; explicit CLI
flags such as `check -severity` and `check -fail-on` take final precedence.

```json
{
  "$schema": "https://raw.githubusercontent.com/shopware/shopware-lsp/main/internal/projectconfig/schema.json",
  "version": 1,
  "php": {
    "extensions": ["redis"],
    "disabledExtensions": []
  },
  "shopware": {
    "targetVersion": "6.7"
  },
  "features": {
    "codeLens": false
  },
  "mcp": {
    "tools": {
      "shopware_apply_code_action": false,
      "shopware_entity_schema_apply": false
    }
  },
  "domains": {
    "symfony.doctrine": false
  },
  "diagnostics": {
    "enabled": true,
    "inspections": {
      "shopware.admin": false
    },
    "rules": {
      "php.arguments": "error",
      "admin.component.unknown-instance-member": "off"
    },
    "overrides": [
      {
        "files": ["src/Generated/**"],
        "enabled": false
      },
      {
        "files": ["custom/plugins/FroshTools/**"],
        "rules": {"php.arguments": "off"}
      }
    ]
  },
  "check": {
    "severity": "warning",
    "failOn": "error"
  }
}
```

Unspecified values inherit the built-in defaults, which keep all current
features, MCP tools, domains, indexing, and diagnostics enabled. Diagnostic rule values
are `off`, `hint`, `information`, `warning`, or `error`. Disabling a complete
inspection skips its analyzer; disabling every rule in an inspection has the
same optimization. Domain dependencies cascade off, so disabling PHP also
disables PHP-backed Symfony and Twig domains.

Shopware extensions may commit their own
`.config/shopware/lsp.json`, for example
`custom/plugins/FroshTools/.config/shopware/lsp.json`. Nested files are
diagnostics-only and apply to their containing directory. Their `files`
patterns are relative to the extension root, so the same file works when the
extension is installed in a project or opened as its own repository. Scoped
configuration is applied from the workspace root toward the nearest extension;
VS Code-local overrides apply last. Patterns support `*`, `?`, and `**`.

The VS Code command `Shopware: Configure Language Server…` provides searchable
feature, domain, inspection, and rule controls and can write either the shared
project file or a local editor override. Diagnostic and request-time feature
changes apply live. Indexing, domain, PHP extension, and target-version changes
prompt for a language-server restart and invalidate structurally incompatible
workspace caches. `Shopware: Open Configuration…` opens or creates the root or
extension-local file. Every Shopware diagnostic also offers a configuration
quick fix for suppressing its rule—or all diagnostics—for a file, directory,
extension, or workspace.

For CI, `shopware-lsp config` validates and prints the effective configuration.
`shopware-lsp check` fails before indexing when the committed configuration is
invalid. Its command-line severity flags override the `check` defaults from the
file; use `-fail-on off` to explicitly suppress the configured failure threshold
for one invocation.

For a resource-only measurement without the feature assertions, build the
profiling harness once and run the binary through the operating-system resource
counter:

```bash
go test -c -tags=integration ./internal/app \
  -o /tmp/shopware-lsp-index-profile.test
/usr/bin/time -l /tmp/shopware-lsp-index-profile.test \
  -test.run '^TestShopwareTrunkIndexingProfile$' -test.count=1 -test.v
```

The profile reports construction, cold and warm scan time, retained Go heap,
current and peak RSS, cumulative allocations, GC activity, and cache size.
Set `SHOPWARE_LSP_HEAP_PROFILE=/tmp/shopware-lsp-live-heap.pprof` to capture
the live post-index heap for `go tool pprof`. Set
`SHOPWARE_LSP_PROFILE_REVERSE_REFERENCES=1` to additionally report the one-time
cost of constructing the lazy PHP reverse-reference index. Set
`SHOPWARE_LSP_PROFILE_SEMANTIC_STORAGE=1` to report compact PHP graph
cardinality, or set `SHOPWARE_LSP_PROFILE_SEMANTIC_DOCUMENT` to an indexed PHP
path to measure the on-demand reconstruction of one detailed semantic document.
`SHOPWARE_LSP_PROFILE_WORKERS` overrides indexing concurrency for comparative
resource measurements. The production scanner defaults to at most four
workers; callers can still override that balance when throughput matters more
than peak memory.

One representative July 2026 run on a 16-core Apple development machine
indexed the current 52,440-file `sw-trunk` checkout with its compiled Symfony
cache present. These are diagnostic reference numbers, not timing assertions:

| Measurement | Empty cache | Cached restart |
|---|---:|---:|
| Workspace construction | 0.13 s | 1.28 s |
| Index/no-op scan | 6.12 s | 0.26 s |
| Warm no-op scan | 0.27 s | 0.23 s |
| Retained Go heap | 134.6 MiB | 135.8 MiB |
| Retained RSS | 547.7 MiB | 315.6 MiB |
| Peak RSS | 555.8 MiB | 316.6 MiB |
| Go allocation volume | 5.14 GiB | 475.1 MiB |
| Process CPU (user + system) | 21.1 s | 3.5 s |
| On-disk cache | 185.0 MiB before close | 164.8 MiB |

An earlier concurrency census showed that reducing indexing concurrency only
modestly changes the memory high-water mark on this corpus. Fewer simultaneous
parser slabs lower cumulative allocation, but the live semantic graph and Go's
GC/heap pacing dominate RSS; a single worker is not reliably lower than two
because collection timing also matters. The main effect is therefore longer
wall time:

| Workers | Cold index | Process CPU | Peak RSS | Go allocation |
|---:|---:|---:|---:|---:|
| 4 (default) | 10.56 s | 32.9 s | 830 MiB | 5.9 GiB |
| 2 | 14.59 s | 32.0 s | 800 MiB | 5.8 GiB |
| 1 | 25.76 s | 32.0 s | 817 MiB | 5.7 GiB |

Go's soft memory limit provides an optional peak-memory/CPU tradeoff. The
following earlier consecutive empty-cache runs used the same binary and
checkout. They remain useful as a relative limit comparison; the default row
predates the allocation improvements reflected in the table above:

| `GOMEMLIMIT` | Cold index | Process CPU | Peak RSS | Retained RSS | GC cycles |
|---|---:|---:|---:|---:|---:|
| Runtime default | 16.85 s | 44.1 s | 1,080 MiB | 996 MiB | 83 |
| 768 MiB | 15.66 s | 45.4 s | 878 MiB | 823 MiB | 90 |
| 640 MiB | 16.01 s | 49.4 s | 819 MiB | 778 MiB | 114 |
| 512 MiB | 20.04 s | 127.7 s | 739 MiB | 702 MiB | 427 |

The VS Code client exposes this as `shopwareLSP.memoryLimitMiB`; other clients
can set `GOMEMLIMIT` in the server environment. The limit is soft and should
remain comfortably above a workspace's live heap. Wall time varies with
filesystem cache and machine load; process CPU and memory are the more useful
comparisons in this table.

The server now selects `GOGC=75` when neither `GOGC` nor `GOMEMLIMIT` is
present in its environment. Explicit runtime settings are never overwritten.
On the later 61,032-file source-only checkout, this balanced default removed
about 93 MiB of peak and retained RSS compared with Go's `GOGC=100` default,
with a 0.02-second cold-index difference. A 512 MiB soft limit is the useful
low-memory knee on this corpus; tighter limits spend disproportionate CPU on
collection:

| Runtime policy | Cold index | Process CPU | Peak RSS | Retained RSS | GC cycles |
|---|---:|---:|---:|---:|---:|
| `GOGC=100` | 9.07 s | 31.0 s | 807 MiB | 805 MiB | 47 |
| Server default (`GOGC=75`) | 9.09 s | 32.4 s | 714 MiB | 713 MiB | 62 |
| `GOGC=50` | 9.05 s | 34.4 s | 665 MiB | 657 MiB | 98 |
| `GOMEMLIMIT=512MiB` | 9.09 s | 35.7 s | 651 MiB | 650 MiB | 75 |
| `GOMEMLIMIT=448MiB` | 10.51 s | 60.8 s | 612 MiB | 611 MiB | 197 |
| `GOMEMLIMIT=384MiB` | 12.73 s | 98.1 s | 563 MiB | 562 MiB | 399 |

The VS Code `memoryLimitMiB` setting is therefore an opt-in low-memory mode.
Leaving it at zero uses the server's balanced GC target; `512` is a reasonable
starting point for a machine where another roughly 60 MiB RSS reduction is
worth additional indexing CPU.

On an earlier 61,033-file source-only profile, the cumulative work before the
shared symbol-string compaction had reduced cold indexing from 93.9 to about
9.0 seconds, retained heap from 1.6 GiB to 178.3 MiB, peak RSS from about
3.25 GiB to about 687 MiB, cache size from 2.2 GiB to 219.1 MiB, and cached
construction from 16.8 to about 2.15 seconds. That historical source-only
profile should not be compared directly with the compiled-cache corpus in the
current table. Detailed PHP
semantic documents are reconstructed from source only when an editor feature
requests a closed file; the persistent cache retains their compact workspace
graphs instead. CST nodes share the source pointer stored by their root, keeping
each node to 32 bytes without changing the public parent or text APIs.
Tokens pack their 32-bit start with a 15-bit common length and an explicit
token flag, reducing the token record from 24 to 16 bytes. Ranges of 32,767
bytes or more retain their full public range in a rare root-owned side table;
that table occupied only about 0.5 MiB across the reference corpus. This saves
roughly 390 MiB of cold allocation without a measurable CPU regression.
Direct-child capacity exists only while building; the uncommon sibling lookup
is recovered from the compact parent child list on demand. PHP
lexer buffers use a corpus-measured one-token-per-four-source-byte reservation;
the opt-in `TestShopwareTrunkParserBufferProfile` integration test reports
token, event, node, and marker utilization when retuning the frontend.
Scanner-owned syntax trees return their cleared node, token, and child slabs at
the end of each persistence batch. Preparation batches now stop at either 50
files or 128 KiB of source, so tiny files still share an efficient SQLite
transaction while a generated container cannot keep dozens of unrelated trees
alive. Three best-fit CST pools retain at most 4 MiB each, reject individual
slabs above 2 MiB, and are emptied after every scanner run; editor/request trees
keep ordinary immutable garbage-collected lifetime.
On the reference cold index this reduced allocation from 8.29 to 7.15 GB
(about 1.1 GiB), lowered GC work and retired instructions, and kept CPU flat
while peak RSS moved from about 839 to 832 MiB. The bounded lexer-token and
parser-event pools also select the smallest sufficient idle buffer instead of
an arbitrary one. Token buffers are cleared before pooling so they cannot
retain source strings. Medium generated files may reuse storage, but aggregate
token and event storage is capped at 8 MiB per pool and the extreme
generated-file outliers are discarded. Against the immediately preceding
50-file/6-MiB-CST-pool build, the current 43,708-file checkout allocates 3.8
instead of 4.3 GiB (about 423 MiB less) and executes about 250 instead of 254
billion retired instructions. Process CPU remains about 21 seconds; cold wall
time moves from 6.12 to 6.28 seconds. Strictly flushing before a source-weight
overflow keeps the measured 565 MiB peak inside the baseline's 564-591 MiB
run-to-run range. Parsed-file line
indexes remain lazy during cold scanning. Exact PHP route and Twig extension
candidate guards run before requesting one, while indexers that actually need
line/column conversion still share the same file-lifetime instance. This
removed roughly 45-95 MiB, about 545,000 allocations, and around eight billion
retired instructions on consecutive reference runs. Twig PHP-access indexing
prepares `@var` and `{% types %}` declarations once per immutable document
resolver instead of rescanning the entire comment/tag set for every attribute
access. This removed another 120-150 MiB, about 760,000 allocations, and around
eight billion retired instructions; the annotation path fell from roughly
140 MiB to 2.5 MiB in the sampled allocation profile. The PHP type graph
similarly stores immutable arguments and cached text in compact exact-length
pointer views. Named/literal kinds use one string slot for their
semantic value while composite kinds reuse it for cached canonical text. A
GC-visible tagged payload stores either the exact argument view or the
callable/shape details record because those representations are mutually
exclusive. This reduces every type node from 48 to 32 bytes and removed about
60 MiB of cold allocation on the reference checkout with flat allocation count
and CPU. Multi-value type folds use an eight-member inline union accumulator
and materialize only the final immutable type. Array literals, inferred return
types, shape projections, and helper folds therefore no longer allocate and
render a succession of immediately discarded union nodes. Two consecutive
reference runs removed another 70-100 MiB of cold allocation and about 110,000
mallocs with flat process CPU and the same retained heap. Scalar literal facts
are derived directly from their lossless syntax nodes instead of occupying the
per-file type-fact table;
`Document.TypeOf` still reports their exact literal type, source, and range.
The immutable types for `""`, `0`, and `1` are canonical singletons; the
integration-only `TestShopwareTrunkLiteralProfile` reports scalar frequency
without introducing an unbounded runtime interner.
PHPDoc parsing streams normalized logical lines instead of materializing a
one-use line slice, and creates assistant-tag maps only when a `#Tag` is
present. Together these changes removed about 18 MiB and 260,000 allocations
from the reference cold index without changing its CPU or retained heap.
Console commands, Twig globals, and Symfony service configuration keep the
small set of paths currently represented in their repositories in memory.
Candidate rejection therefore does not issue three to five SQLite existence
queries for every ordinary PHP file. The sets load from the persistent cache
at startup and publish mutation changes only after commit. On the reference
cold index this removed about 175 MiB, 3.9 million allocations, 16 billion
retired instructions, and roughly 2.5 seconds of process CPU.
Native declaration reference binding classifies scalar and composite builtin
names directly instead of constructing a full semantic type merely to discard
it. Besides preventing the broad `object` type from becoming a bogus
`App\object` class reference, this removed about 2.8 million tiny allocations
and four to five billion retired instructions; consecutive total-allocation
measurements ranged from flat to about 60 MiB lower.
Native type trees are also traversed directly rather than materialized into a
temporary query slice. Each class type is now recorded exactly once; simple
types were previously emitted twice. On the reference checkout this removed
about 1.1 million allocations, 53 MiB of cold allocation, 3.4 MiB of retained
heap, and about 1 MiB of persisted duplicate reference data.
Stored syntax type facts use lossless 64-bit start/length/kind keys, with a
full-identity overflow path for unusually large ranges. Ordinary assignment
facts keep only the type because their confidence, source, and empty reason are
implicit. Workspace projections borrow immutable semantic values until their
database transaction commits, then one batch-wide detacher copies and
canonicalizes them; failed transactions never enter that pool. The binder's
structural census also reserves the final member-reference slots and a
per-file non-literal fact estimate, avoiding repeated slice and map growth
after type inference.
Function, closure, and synthetic PHPDoc parameters use exact-length storage,
while stable symbol-ID builders reserve only the named or anonymous branch they
actually encode. `foreach` target variables are counted and visited in place,
including destructuring, and inference reads only the key/value targets rather
than scanning the iterable and loop body. These changes remove roughly another
half-million binder allocations plus about 92,000 inference allocations, and
also prevent a one-value `foreach` from accidentally replacing the iterable's
flow type with its key type.
Retained PHP signatures keep their parameter slice inline while templates,
throws, and literal/constant-return metadata share one optional side record;
on the reference checkout only 34,516 of 125,598 signatures need that record.
Their 88-byte parameter records derive the effective type from the native or
PHPDoc type and retain an explicit fallback only for the three exceptions in
208,392 parameters. Assistant tags use the same optional record and occur on
only 92 parameters. A versioned positional cache encoding avoids reflective
per-parameter work while continuing to decode the former public-struct map.
Published snapshots answer per-file symbol queries from the immutable
document graph directly instead of retaining a second 39,416-entry path map
and another copy of all 314,038 symbol IDs.
Every compact workspace symbol also borrows the one path owned by its enclosing
document instead of retaining another string header. This keeps the core symbol
record at 160 bytes and saves about 2.2 MiB of live heap on the reference
checkout. The direct cache decoder consumes the former 20-field wire layout,
discards its redundant serialized path, and binds the document-owned pointer;
cached restore therefore remains compatible while also avoiding about 65 MiB
of transient allocation compared with the former reflective symbol decode.
Raw-DEFLATE cache restore reuses its dictionary, bounded reader, buffered
reader, and MessagePack decoder across documents, then releases that short-lived
state when startup completes. On the reference checkout this removes the
1.23 GiB of per-document DEFLATE dictionaries and the repeated decoder buffers:
cached allocation volume fell from 3.0 to 1.3 GiB, GC cycles from 49 to 23, and
process CPU from about 7.7 to 6.1 seconds without changing retained heap or the
cache format.
That decoder also owns a restore-scoped parsed-type cache. The reference graph
contains 2,271,440 serialized type slots but only 43,792 distinct canonical
types, so each immutable type is now parsed once across the restore rather than
once per occurrence. The cache is cleared with the decompressor after startup
and does not become process-global state. This further reduced cached
allocation from 1.3 to 1.2 GiB, removed about 2.84 million allocations, lowered
GC cycles from 23 to 21, and cut retired instructions by roughly 11%; cached
construction fell from about 3.0 to 2.6 seconds while the existing wire format
remained compatible.
Bulk repository restore reads the outer MessagePack value through
`sql.RawBytes`, whose lifetime is restricted to one synchronous visitor call.
The PHP repository decodes its two-field envelope with bounds checks and
borrows the compressed payload until DEFLATE finishes, rather than copying it
once in `database/sql` and again in the generic MessagePack decoder. The
existing SQLite and graph formats remain unchanged. This removed another
roughly 146 MiB of cached allocation and lowered GC cycles from 21 to 18;
cached allocation volume is now about 1.1 GiB.
The repository row count now also sizes the temporary maps used by cache
restore. A storage census of the reference checkout found 2,947,594 interned
string slots but only 700,249 distinct strings, and 2,271,440 type slots but
only 43,792 distinct types. Reserving the document map exactly, the string map
at 18 entries per document, and the type map at five entries per four documents
replaced roughly 82 MiB of incremental map growth with one 43 MiB allocation.
That saved about 43 MiB of cached allocation, reduced GC cycles from 18 to 13,
and retired about 3% fewer instructions. The maps remain restore-scoped and
are released after publication, so retained heap and the cache format do not
change; underestimated hints only allow ordinary map growth.
The compact graph decoder now shares those same temporary maps instead of
first allocating a distinct Go string and parsed type for every wire value.
It reads string bytes through one reusable scratch buffer, probes the
already-reserved string map without allocating on hits, and allocates only the
roughly 700,000 distinct retained strings. Reference/value arrays and
signature, hierarchy, and metadata records are decoded directly into their
final slices instead of passing through reflection and intermediate wire
objects. Existing MessagePack and compression bytes remain compatible.
Against the reserved-map baseline, cached allocation fell from 1.05 GiB to
845 MiB, allocations from 11.87 million to 6.24 million, and GC cycles from 13
to 11; cached construction fell to about 2.45 seconds and retired instructions
by roughly another 6%, while retained heap stayed at 266 MiB. Declared
collection and string lengths are bounded before allocation so a corrupt cache
cannot turn a short payload into an unbounded allocation request.
No-op discovery now checks excluded path components in place instead of
splitting every relative path. The asset supplemental index also precomputes
its public/web roots and uses component-boundary prefix checks instead of
rebuilding roots and calling `filepath.Rel` for every candidate (and a second
time for accepted public files). Both predicates allocate zero bytes for clean
paths while retaining sibling-prefix, relative-root, case-insensitive dynamic
directory, and bundle-resource behavior. This removed another roughly 59 MiB
from a cached run and brought representative no-op scans to 0.68–0.75 seconds.
Workspace discovery now uses a bounded four-worker pure-Go directory traversal
and sorts its concurrent results before publication. Scanner-owned paths bypass
the redundant public-call filtering pass; stale paths and persisted
size/mtime rows are matched against that sorted list with linear merges.
`sql.RawBytes` comparisons avoid materializing a second copy of every stored
path, while an aligned state slice replaces the former 60,000-entry lookup
map. On the reference checkout these changes reduce cached allocation by
another roughly 43 MiB and bring representative no-op scans to 0.33–0.39
seconds without increasing retained heap.
The frequently used `ClassSymbols` API likewise filters lightweight immutable
views before materialization, preallocates the exact public result, and sorts
ordinary ASCII PHP names without lowercase copies. On the reference snapshot,
one call fell from about 91 MiB of allocation to 10 MiB; that query happens
after the resource harness takes its table measurements and therefore is not
included in the table above.
Snapshot publication now builds the final unique-symbol table first and indexes
only the winning declaration for each stable ID. Class, function, constant, and
member indexes store one primary ID inline and allocate an alternatives slice
only for a real normalized-name collision. On the reference checkout, 314,038
declarations collapse to 184,188 winning IDs; 272,629 former member-index
entries collapse to 158,452 names plus only 2,543 distinct alternatives.
Exact per-container capacities are derived from the winning symbols before the
immutable indexes are filled. The discarded original-to-lowercase construction
map and hierarchy-only normalization pass are gone; uncommon runtime spellings
continue to use the shared lazy concurrent cache. Together these publication
changes removed about 67 MiB from cached allocation, more than 1.4 million
allocations, and about 7 MiB of retained heap while also preventing stale
lookup keys from shadowed duplicate declarations.
Class, function, constant, and named-member lookups now walk the compact
overlay/base indexes directly. They deduplicate the usually one-element result
in place and materialize the final view slice once instead of constructing an
ID slice, a deduplication map, and a second view slice. Consecutive reference
runs removed about 3.25 million allocations, roughly 120 MiB of allocation,
and nearly three billion retired instructions with flat process CPU and no
change to lookup ordering or duplicate-declaration behavior.
Hot binder, inference, and member-resolution paths now consume those indexes
through non-materializing visitors. The visitor deduplicates the usual small
overlay/base result in four inline ID slots and allocates a map only for an
unusually ambiguous name. On the 60,472-file reference corpus, this removed
another roughly 1.60 million allocations with retained heap and process CPU
effectively flat.
Linked PHP references store a unique target directly in `Resolved` and reserve
the `Candidates` slice for genuine ambiguity. Local binding likewise keeps the
first matching symbol inline and allocates the slice only when a second match
appears; definition providers merge both representations when exposing
targets. A later consecutive A/B run on the 61,032-file source checkout, with
its generated Symfony cache absent in both runs, removed about 907,000 mallocs,
1.6 MiB of retained heap, and 2.1 billion retired instructions while leaving
CPU flat to slightly improved and trimming several MiB from the cache.
Member-property and class-constant inference now collects its final
`types.Type` values directly instead of first materializing private
`(symbol ID, type)` records and copying them into a second slice. Union and
intersection receivers retain ID-based deduplication without imposing that
storage on ordinary object receivers. A focused generic-property lookup fell
from 552 bytes and seven allocations to 528 bytes and six allocations; the
61,032-file corpus consistently performed about 203,000 fewer mallocs with
flat retained heap and process CPU.
Fixed-kind member traversals no longer prefix property names with `$` merely
to distinguish them from methods that cannot occur in that traversal. Mixed
`All` lookups retain the discriminator. The focused property lookup falls
again to 520 bytes and five allocations, and consecutive corpus runs remove
about 356,000–358,000 mallocs with flat-to-lower CPU and retained heap.
Member-ID resolution exposes matching method, property, constant, and enum-case
visitors while preserving the existing slice-returning APIs. PHP linking and
abstract-method validation consume the visitors directly, and `Reference`
keeps the first target inline while traversal is still in progress instead of
allocating a singleton ID slice. A focused named-method lookup takes about
111 ns with zero allocation through the visitor versus 130 ns, 16 bytes, and
one allocation through the compatibility API. Consecutive real-corpus runs
removed another roughly 476,000 mallocs and 0.2–0.5 billion retired
instructions with flat retained heap and slightly lower process CPU.
Each PHP semantic analysis now owns a small environment-handle arena. Its first
two copy-on-write frames are inline, and additional stable handles come from
geometrically growing blocks capped at 128 entries; handles are never recycled
while an analysis can still reference them. This preserves branch isolation
and ordinary environment alias semantics while amortizing heap objects. The
focused semantic benchmark removes two allocations with flat latency and only
48 extra transient bytes, while consecutive real-corpus runs remove about
896,000 mallocs with the same 240.3 MiB retained heap and CPU/instruction
counts within roughly 0.2%.
Recursive call inference now allocates argument records from an
analysis-local bump arena: four records are inline, ordinary overflow uses
eight-entry blocks, and unusually large calls retain exact allocations.
Returned slices are capacity-limited so downstream code cannot overwrite the
next arena region. The representative semantic benchmark falls from 10,448
bytes and 74 allocations to 10,040 bytes and 61 allocations. Paired corpus
runs remove about 475,000 mallocs and roughly 30 MiB of allocation volume with
flat process CPU and retained heap.
PHP query consumers that only need to walk syntax nodes no longer materialize
temporary result slices. Namespace discovery stops after its first match, name
resolution visits imports in source order, and inference builds its
exact-capacity function index directly during traversal. The representative
semantic benchmark removes another allocation, while paired 61,032-file corpus
runs remove about 127,000 mallocs and 18 MiB of allocation traffic with the
same 240.3 MiB retained heap and flat-to-slightly-lower CPU and retired
instructions.
Function-parameter queries now also expose a zero-allocation direct-child
iterator while preserving the slice API for random-access callers. Binding
uses its exact count to allocate the retained signature once, and closure
inference fills its exact-capacity callable parameter list directly. Paired
corpus runs remove another 130,000–131,000 mallocs with unchanged retained heap,
CPU, and retired instructions.
The shared CST now provides a value cursor for direct child nodes alongside its
range-over-function API. Hot JSON, YAML, JavaScript, and typed Twig accessors
use the cursor, and exact property lookups scan the children directly instead
of materializing a sibling collection. The cursor is allocation-free in the
focused test; paired corpus runs remove about 1.69 million mallocs and 23–26 MiB
of allocation traffic with unchanged 240.3 MiB retained heap and flat CPU and
retired instructions.
The matching direct-token cursor is likewise allocation-free. Twig typed
accessors, YAML scalar lookup, and PHP modifier lookup use it in their hot
paths, removing another roughly 824,000 corpus mallocs; total allocation volume,
retained heap, CPU, and retired instructions remain effectively flat.
Doctrine candidate screening now uses immutable ASCII multi-pattern automata
instead of scanning each file independently for as many as 21 markers. Exact
and folded automata preserve the former per-pattern case sensitivity, and a
2,000-input randomized equivalence test guards that boundary while the scanner
itself allocates zero bytes. Paired 61,032-file runs reduce cold indexing from
9.90 to 8.94 seconds, process CPU from 31.67 to 30.56 seconds, and retired
instructions from 384.6 to 363.6 billion. Allocation count and the roughly
240.4 MiB retained heap remain effectively unchanged.
PHP name contexts now allocate class, function, and constant import maps lazily
by kind. When the first map of a kind is created, the binder republishes its
header to scopes already created in the same namespace block; sibling namespace
blocks remain isolated. Escaped empty-context construction falls from 144
bytes and three allocations to zero, and the real corpus performs about
536,000 fewer mallocs with flat retained heap and CPU. Import-free resolution
also skips lowercase alias construction entirely. A local-class lookup falls
from roughly 66 ns, 40 bytes, and two allocations to 32 ns, 24 bytes, and one
allocation, removing another 27,000–30,000 corpus mallocs.
Resolved function and constant candidates also have a two-name inline value
and non-materializing visitors while the existing slice APIs remain intact.
Inference and interactive lookups consume the visitor; binder references still
materialize their persistent qualified-name list. An import-free function
lookup drops from about 54 ns, 64 bytes, and two allocations to 40 ns, 32
bytes, and one allocation, and the real corpus performs about 187,000 fewer
mallocs with unchanged resolution and fallback order.
Retained reference records pack their two value counts into bytes and combine
the target kind with the two reference flags, reducing the record from 32 to
28 bytes without changing the MessagePack layout. The reference corpus uses a
maximum qualified count of two and candidate count of one; new pathological
inputs are bounded to 255 values. This saved another 5.4 MiB of retained heap.
The document-local string and receiver tables are already bounded below
2^21 entries, so the next layout stores both counts, the name kind, target
kind, and flags in the otherwise unused upper index bits. The wire layout and
full 32-bit value offset remain unchanged, while 1,321,490 current-corpus
records fall from 28 to 24 bytes and remove another 5.4 MiB of live heap.
Boundary and MessagePack round-trip tests cover every packed field.
The restore decoder stores newly interned immutable strings in 64 KiB arena
chunks rather than making roughly 700,000 separate backing allocations. The
strings remain ordinary immutable Go strings and the arena builder is released
after publication. CPU profiles remain effectively flat, while cached restore
uses about 724,000 fewer allocations, 4 MiB less retained heap, and materially
less reserved heap/RSS.
Cold scanner batches now use the same immutable arena ownership for canonical
workspace strings. Cross-document interning and ordinary string APIs are
unchanged, but the 61,032-file run performs about 719,000 fewer mallocs and
retains roughly 4.6 MiB less live heap with flat process CPU.
Retained symbols now keep one owning-document pointer and three one-based
side-table indexes packed into a `uint64`, replacing separate signature,
hierarchy, and metadata pointers. The 21-bit indexes cover the bounded
per-document wire format, and decoding stages the old pointer-shaped data only
until it can publish exact immutable tables. This reduces each of the 314,038
symbol cores from 160 to 144 bytes and removes another 5.1 MiB from the
real-world oracle's retained heap. Cold indexing, warm scans, and the
1.86-second cached restore remain flat.
The optional symbol side records now use immutable pointer-and-length views
instead of retaining a full three-word Go slice header for every possible
collection. The corpus census found that 63,782 of 80,098 metadata records are
documentation-only, only 1,735 hierarchy type entries exist across 26,237
hierarchies, and 34,516 signatures carry any of their four extra collections.
Metadata cores fall from 64 to 24 bytes with a 24-byte extras record only for
attributes or constant arrays; hierarchies fall from 144 to 40 bytes with a
32-byte type record only when needed; and signature extras fall from 96 to
48 bytes. Parameter source ranges similarly store one absolute offset and
bounded deltas in 16 rather than 24 bytes, with an exact full-range fallback;
all 208,392 parameters in the reference checkout use the compact form. The
wire/cache format and public slices remain unchanged, and strict `checkptr`
tests cover the immutable views. Together these layouts lower the focused
retained heap from 225.3 to 216.1 MiB and the full feature oracle from 297.8 to
288.7 MiB. A 61,033-file run remains at 8.87 seconds cold, 318 milliseconds
warm, and 1.83 seconds for a cached restart.
Published PHP snapshots now store member names in sorted per-container spans
with contiguous symbol-pointer values instead of retaining a nested Go map for
every class-like declaration. Single-document analysis overlays keep their
small mutable maps, so replacement and fixed-point behavior are unchanged.
The reference census contains 20,242 member containers, 158,452 distinct
names, and only 2,542 names with alternates; 15,554 containers have eight or
fewer names. The immutable index is built directly in document precedence
order and discards no intermediate map tree. Typical named visits remain
allocation-free at roughly 32–36 ns, while enumerating a 64-member class falls
from about 9.4 µs, 4 allocations, and 4,648 bytes to 1.26 µs, 1 allocation, and
1,152 bytes. The real checkout performs about 53,000 fewer cold-index mallocs,
retains 209.2 rather than 216.1 MiB in the focused harness, and retains 281.7
rather than 288.7 MiB in the full feature oracle. Its latest cold, warm, and
cached runs complete in 8.90 seconds, 358 milliseconds, and 1.89 seconds.
The semantic store and its current published snapshot now share one immutable
path-to-document map instead of retaining identical 39,416-entry maps. Every
replace/remove operation clones before its first write, so older lock-free
readers keep their exact generation; bulk batches reserve the destination map
once, while a one-file update uses Go's optimized map clone. On a synthetic
10,000-document workspace a one-file replacement improves from roughly
1.88 to 1.60 milliseconds with the same allocations. The real checkout removes
another 1.7 MiB from both harnesses, reaching 207.6 MiB focused and 280.0 MiB
with the full feature catalog. Its cold, warm, and cached passes remain flat at
8.80 seconds, 300 milliseconds, and 1.82 seconds.
Symbol source locations now retain one absolute start and five 16-bit deltas
instead of three complete ranges. Missing selection/body ranges use a reserved
delta, while declarations with larger spans keep an exact document-local
fallback. The trunk census found 313,878 compact symbols and only 160
fallbacks among 314,038 declarations. This reduces the symbol core from 144 to
136 bytes, keeps the cache wire format unchanged, and lowers retained heap to
204.7 MiB in the focused harness and 277.3 MiB in the full oracle. Compact
`Range` access remains allocation-free at roughly 1.6 ns; the latest cold,
warm, and cached passes complete in 8.80 seconds, 302 milliseconds, and
1.81 seconds.
Retained PHP references likewise combine their document-local value offset and
source-range length into one 32-bit word. A reserved length selects an exact
sparse fallback containing the original range and offset. All 1,321,490
references in the trunk fit the inline form: the longest range is 86 bytes and
the largest per-document value offset is 2,451. The record therefore falls
from 24 to 20 bytes and removes a further 5.3 MiB, reaching 199.4 MiB in the
focused harness and 271.9 MiB with the full feature catalog. Reading both
values remains allocation-free at roughly 1.07 ns. The unchanged cache wire
format restores in 1.78 seconds, while cold and warm passes remain flat at
8.77 seconds and 306 milliseconds.
Batch-published and cache-restored PHP documents now share one immutable
reference-string header table while retaining compact per-document `uint32`
IDs. The wire format still writes each document's local list, and standalone
incremental updates keep their cheaper local table. The trunk's 1,047,627
document-local entries collapse to 176,518 shared headers; lookup remains
allocation-free at roughly 1.14 ns instead of 1.07 ns. This removes another
9.7 MiB from the focused harness and 9.8 MiB from the full catalog, reaching
189.7 MiB and 262.1 MiB respectively. A cached process retains 190.9 MiB and
constructs in 1.87 seconds; cold and warm scans remain at 8.80 seconds and
303 milliseconds.
Parameter symbol IDs are now reconstructed from the owning declaration ID and
parameter name instead of retaining a string header and backing string for
every signature entry. The trunk census derives 206,254 of 208,392 IDs; the
remaining 2,138 noncanonical IDs use an exact optional fallback. This reduces
the parameter core from 80 to 64 bytes and also removes the canonical ID
strings from the workspace interner, lowering focused retained heap from 189.7
to 178.3 MiB and the full feature oracle from 262.1 to 250.8 MiB. Parameter
wire layout v2 omits derivable IDs while its decoder continues to accept v1
and legacy map records, including mixed-version parameter arrays. The reference
cache is 1.8 MiB smaller and a cached process retains 179.5 MiB. Reconstructing
an ID on demand takes roughly 64 ns, 32 bytes, and one allocation; an exact
fallback remains allocation-free at roughly 1.1 ns. Cached retired instructions
remain effectively flat at about 48 billion; the latest cold, warm, and cached
construction passes complete in 9.04 seconds, 390 milliseconds, and 2.15
seconds.
Published symbols now keep document-local `uint32` indexes for name,
fully-qualified name, and container instead of three retained string headers.
Cold batches and cache restores remap those indexes directly into shared
canonical tables; standalone incremental documents retain a small local table.
The symbol core falls from 136 to 104 bytes. The current 43,708-file checkout
stores 186,339 declarations. Symbol and reference indexes use the same
canonical header table: 285,756 symbol and 177,116 reference values collapse
to 379,560 retained headers, eliminating 83,312 duplicates. Canonical data
pointers key the construction-only index, so unification removes about 34 MiB
of matched cold allocation and 10 MiB from cached restore versus maintaining
two maps. The immutable 184,814-entry ID lookup likewise stores only symbol
pointers in a seeded open-addressed table, because every target symbol already
owns the corresponding ID; later declarations still replace earlier duplicate
IDs exactly. The current focused harness retains 128.8 MiB, the full feature
oracle 188.9 MiB, and a cached restart 128.4 MiB. Cached construction remains
at about 1.36 seconds with effectively flat process CPU, and the cache wire
format is unchanged. Symbol-string reads take roughly 0.91 ns. Average
symbol-ID hits take about 14 ns instead of 10 ns for a Go map, while misses
take about 5 ns; both remain allocation-free.
Raw-DEFLATE reads use the API-compatible lower-allocation decoder from
`klauspost/compress`, while writes deliberately retain Go's standard encoder
so existing compression output, cold-index behavior, and the versioned cache
format remain stable. Reusing that decoder across rows removes about 42 MiB of
per-block Huffman-table allocation and nearly one million allocations from the
reference cached restart. The restore-scoped canonical-string lookup is also a
seeded open-addressed table with one string header per slot instead of a
`map[string]string` with two. Its capacity hint is bounded and allocated only
when the first value arrives. The same table backs cold batch detachment; the
scanner supplies its filtered paths so PHP sizes it from actual `.php`
candidates without penalizing a warm no-op batch. Together these changes lower
cached allocation from 666 MiB to about 601 MiB, cached construction from about
2.5 to 2.2 seconds, and cold allocation by roughly another 49 MiB without
changing the 250 MiB retained cached heap.
Generic repository writes now borrow one bounded MessagePack encoder buffer
across sequential SQLite inserts instead of growing a fresh `bytes.Buffer` for
every value. Buffers larger than 1 MiB are discarded, and all idle buffers are
released at the scanner batch boundary. The encoded bytes and database schema
are unchanged. On the reference cold index this removed about 221 MiB of
allocation (8.3 to 8.1 GiB) while retired instructions and CPU remained flat;
cached restarts do not rewrite values and are therefore unchanged.
The bounded pool now keeps the encoder and its small wrapper together with that
buffer instead of allocating a new wrapper for every repository/file batch.
Oversized entries still discard the complete unit, and the batch boundary still
drains every idle encoder. Paired 61,032-file runs remove about 534,000 mallocs
and 23 MiB of allocation traffic with unchanged retained heap and effectively
flat CPU and retired instructions.
Per-document reference packers assign stable indexes in their temporary
uniqueness maps and materialize exact string and receiver-type tables once
packing is complete. They no longer build provisional reserved slices, grow
them, and copy them again during compaction. The reference corpus packs
1,427,786 records and 1,263,408 qualified/candidate values into 1,047,843
document-local string slots and 321,549 type slots. This removed about 41 MiB
of cold allocation (8.1 to 8.0 GiB) with flat CPU, retained heap, cache format,
and cached-restart cost.
The binder's existing structural census now also reserves its scope slice,
removing about 31 MiB of repeated growth without another syntax-tree walk.
Scope declaration indexes use compact insertion-ordered name/ID lists, which
naturally represent duplicate normalized names without a map bucket and
per-name alternatives slice for every non-empty scope. Across the two
scope-index layout steps this removes about 74 MiB and roughly 715,000
allocations while preserving lookup order and clone isolation.
Reference reservations use 90% of the syntax-derived upper bound while
retaining the complete 4,096-entry reservation for generated-file outliers.
The integration-only binder capacity census covers more than 37,000 PHP
documents and verifies the resulting growth frequency before that ratio is
changed. Top-level declarations, class members, trait names, property
variables, constant-array items, attributes, and body declarations are also
visited directly instead of first materializing one-use node slices. Against
the immediately preceding weighted-batch build, these binder changes removed
about 61 MiB of cold allocation with flat process CPU and retained heap; RSS
remained inside its normal run-to-run range. Semantic results, cache encoding,
and query APIs are unchanged.
Generic repository rows now own their source path directly alongside
namespace, key, and value. The previous `data`/`files` join was strictly
one-to-one on the reference cache (127,327 rows in each table), so it doubled
every insert/delete and retained three association indexes without expressing
additional semantics. The flattened schema removes the second statement and
`LastInsertId` round trip, cuts cold allocation by about 124 MiB and 4.6
million allocations, reduces retired instructions by 5.1%, and lowers the
checkpointed cache from 246 to 223 MiB. Cold indexing improved from about 14.1
to 12.1 seconds in the measured run while cached allocation remained flat.
The disposable cache version is bumped, and standalone legacy databases are
detected by schema shape and rebuilt automatically.
Repository-independent route lookup ordering now ranks exact reverse-path
matches before partial matches, source declarations before generated catalogs,
and remaining ties by stable route fields. This keeps navigation results
deterministic across SQLite query plans and concurrent indexing completion.
Line indexes start from a bounded source-size estimate instead of growing from
one entry, and PHP type-alias maps remain unallocated until first use.
Parsed-file memoization checks its shared result before allocating
first-computation synchronization. Semantic member queries reserve their known
result cardinality, while document overlays census their already-built symbol
slice before constructing member and global indexes. Together these small
hot-path changes removed about 47 MiB and 520,000 allocations from the
reference cold index with effectively flat retired instructions and no
retained-heap change.
Per-document inference snapshots keep their one replacement path inline, use
32-bit indexes into the already-owned symbol slice, and omit reference storage
for declaration-only overlays instead of allocating general workspace maps.
The PHP
reverse-reference index is built on first use instead of startup; on this
checkout that lookup took 0.56 seconds and added 27 MiB of retained heap. Peak
RSS is particularly sensitive to operating-system allocation and GC timing;
retained Go heap and cumulative Go allocation are the more repeatable memory
metrics.

Transient PHP references now keep their unique target in `Resolved` and move
qualified-name and genuinely ambiguous target collections behind one lazy
side record. The common record is 64 rather than 112 bytes. Flow-environment
keys borrow variable tokens and already-normalized member expressions directly;
only internal whitespace or nullsafe access needs a normalized copy. On the
same 44,288-file checkout this key change reduced cold allocation by about
145 MiB and 3.90 million allocations, with process CPU falling by roughly
0.55 seconds.

All built-in type-fact reasons now use compact byte codes and round-trip to
their original analytics strings. The real-world census contains 2.82 million
non-literal facts: 33.8% use the type-only assignment table and the remainder
use the annotated compact table, so reservations follow the same one-third /
two-thirds split. This moved roughly 953,000 flow facts out of the heavyweight
detailed map; a paired 45,214-file run removed about 71 MiB of allocation and
retired roughly 1% fewer instructions. JSON recovery sets are immutable grammar
tables rather than per-property slices, removing another 407,000 allocations.
Case-folded import probes, path classifiers, Twig sort comparisons, and
framework type-extension method gates avoid temporary lowercase strings in
their ASCII hot paths while retaining Unicode fallbacks.

First-class-callable detection and route metadata now enumerate PHP arguments
through the allocation-free CST iterator. Native type parsing only allocates a
union or intersection collection after seeing the corresponding operator.
PHP/JavaScript name compaction shares a borrowed fast path, and commented PHP
expressions reserve their final builder once. Twig PHP-context discovery walks
matching nodes directly instead of materializing recursive result slices.
Finally, scanner fingerprints are written in 128-row SQLite statements instead
of one statement execution per file. Paired 52,059-file runs removed about
1.87 million mallocs and 43 MiB of cumulative Go allocation across this group;
the fingerprint batching alone removed 17 MiB of allocation and roughly
1.47 billion retired instructions. Incremental file removal now uses the same
bounded batching for repository rows and scanner fingerprints, shares prepared
statements and path arguments across namespaces, and skips insert setup for
delete-only replacements. A 46-repository, 512-path removal benchmark dropped
from about 48.0 ms, 6.78 MiB, and 235,534 allocations to 3.1 ms, 1.05 MiB, and
1,700 allocations. Doctrine candidate screening advances its exact and
case-folded automata in one source pass; the focused scanner benchmark is about
47% faster, and paired full-corpus runs retired 0.8–1.3 billion fewer
instructions without adding allocations. Twig-context and PHP route-usage
candidate gates share a fixed-pattern ASCII automaton as well, replacing one
source scan per keyword with one scan per feature. The worst-case Twig gate
benchmark fell from roughly 13.6 ms to 0.27 ms for a 114 KiB source string;
paired full-corpus runs retired 3.2–5.0 billion fewer instructions. PHP
workspace graphs now use the pooled Klauspost best-speed DEFLATE encoder (the
same implementation already used for decoding), removing roughly another
5 billion instructions. Its 23,542 compressed graph payloads grew by only
690,818 bytes, or 1.7%, while retained heap and cached restore time stayed
flat; a standard-library decoder compatibility test protects the raw-DEFLATE
cache format.

PHP array inference now keeps the field-index map lazy for small shapes and
transfers completed field storage into immutable types instead of copying it
again. The focused small-shape collector fell from about 350 ns, 736 bytes, and
six allocations to about 212 ns, 480 bytes, and four allocations; a paired full
index removed 58.8 MiB of cumulative allocation. Implicit list literals bypass
shape collection entirely, while keyed literals reserve the exact number of
candidate fields and spread arrays skip candidates they cannot preserve. A
controlled one-worker corpus comparison removed another 35.3 MiB and roughly
313,000 allocations with flat wall time. Allocation-free indexed accessors for
immutable type arguments, callable parameters, and shape fields now cover
read-only inference, resolution, Twig, analytics, and semantic-graph traversal.
The field/parameter portion removed a further 3.7 MiB and about 62,000
allocations; the broader argument traversal was neutral at full-corpus scale.

Canonical PHP type rendering now reserves its final output once and writes
literal and generic children directly, avoiding temporary child strings. The
focused eight-field shape benchmark fell from about 341 ns, 968 bytes, and nine
allocations to about 260 ns, 576 bytes, and four allocations. PHP use
declarations likewise scan fields and top-level imports without temporary
slices, and the binder/name resolver consume imports through an allocation-free
visitor. Together these changes removed 78.4 MiB of cumulative allocation and
1.38 million allocations in a controlled one-worker 52,059-file comparison;
retained heap stayed flat.

Parsed files now keep their common single derived analysis inline and allocate
a small overflow map only if a second component uses a distinct memoization
key. Concurrent callers for the same key still share one computation, and the
scanner clears all derived analysis at the end of the preparation phase. The
single-key lifecycle benchmark fell from about 220 ns, 712 bytes, and six
allocations to 84 ns, 320 bytes, and two allocations. A controlled one-worker
52,059-file comparison removed 5.4 MiB of cumulative allocation and about
76,800 allocations; retained heap stayed flat.

The latest empty-cache resource run covered a growing 52,440-file checkout:
6.12 seconds cold, 267 milliseconds warm, 134.6 MiB retained Go heap,
547.7 MiB retained RSS, 555.8 MiB maximum RSS, 5.14 GiB cumulative Go
allocation, and 21.1 seconds of process CPU. Of the 505.6 MiB Go heap
reservation, 271.4 MiB was in use, 191.6 MiB had already been released to the
operating system, and only 42.6 MiB was idle but resident; the remaining
process RSS is therefore not simply unreclaimed Go heap. Bounding each
explicitly configured SQLite page cache at 8 MiB reduced retained and peak RSS
by about 46 MiB on an identical corpus, with effectively unchanged CPU and
retired instructions. The assertion-heavy oracle indexed the same 52,440 files
in 6.05 seconds, retained 203.4 MiB with the complete feature catalog, and
restored its cache in 1.27 seconds.

### CI/CD

This project uses GitHub Actions for continuous integration:

- Tests are run on every push and pull request
- Code linting is performed using golangci-lint
- Builds are created for verification

## License

[MIT License](LICENSE)
