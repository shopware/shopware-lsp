# Symfony Plugin Feature Roadmap

This roadmap maps the user-facing features in
`idea-php-symfony2-plugin` onto portable Language Server Protocol
capabilities. IntelliJ PSI, gutter, tool-window, and action implementations are
treated as design references; indexes and framework knowledge stay independent
of a particular editor.

## Current coverage

| Domain | LSP coverage |
|---|---|
| Dependency injection | XML, YAML, fluent/array PHP configurator, attributes, and typed-container references; persistent PHP-array parameters, explicit services, aliases, defaults, conditional tags, deprecations, effective autowire state, and PSR-4 prototypes plus structural completion/navigation/diagnostics for arguments, calls, factories, classes, tags, parents, and decorators; service, alias, parameter, tag, class, and container-constant completion/navigation; persistent PHPDoc `#Service` and `#Parameter` contracts provide exact container-name completion and definition navigation for matching function, constructor, and inherited method call arguments; exact YAML service-option completion for block/flow definitions and `_instanceof` rules with duplicate filtering, legacy marking, scalar keywords, standard/config tags, and Composer-version-aware DI argument tags; inherited public-method completion and declaration navigation for YAML calls/tag callbacks/modern or legacy factories and XML calls/factories, with exact edits and receiver-aware validation; `AutowireLocator`, `AutowireServiceClosure`, `AutowireMethodOf`, and `AutowireCallable` service intelligence plus public-instance callable-method completion/navigation through service aliases or `::class`; tag completion/navigation for PHP `tagged_iterator()` / `tagged_locator()` and `TaggedIterator`, `TaggedLocator`, `Autoconfigure`, and `AutoconfigureTag`; typed `ParameterBagInterface`/`ContainerBagInterface` `get()` and `has()` parameter completion/navigation/missing diagnostics with service-container disambiguation; missing-reference and local duplicate diagnostics; global/inherited class/enum constant resolution with modern/legacy YAML syntax and missing diagnostics; deprecated service/class completion marking and hint diagnostics from source or compiled-container metadata; legacy factory-setting diagnostics; conventional XML/YAML service-tag class/interface contract diagnostics with modern/legacy alternatives plus inheritance-aware actions that add missing inferred tags while preserving block/flow syntax; constructor-argument validation and insertion actions plus position/name-aware XML argument rewrites for every type-compatible explicit, prototype, or compiled service; XML/YAML positional constructor/configured-method argument inlay hints with inherited PHP parameter/type resolution; undefined `$this->property` service-injection actions with name/method-ranked type candidates, inherited-property suppression, import management, optional-parameter ordering, and promoted/traditional/readonly constructor-style preservation; interactive YAML/XML/PHP service-definition generation from PHP classes with inferred constructor/setter services and tags; interactive compiler-pass creation for bundle classes with import-aware `build()` registration and atomic new-file workspace edits; YAML named-argument completion, PHP parameter navigation, invalid-key diagnostics, and typo actions with inherited-constructor support plus name-only/typed `_defaults.bind` navigation across explicit and prototype services; configured XML/YAML service-method existence validation with typo and cross-file create-method actions; constructor and configured-method service-reference type validation across XML/YAML/PHP configurator and legacy-array syntax with interface/inheritance/union relations, unsaved local definitions, and syntax-preserving compatible-service fixes; compiled-container fallback; PHP type inference; persistent PHP/XML/YAML PSR-4 prototype expansion; PHP class-to-definition and effective-autowire constructor code lenses; cross-format forward/reverse decorator and parent code lenses plus prototype-to-class navigation; structured service location by exact ID or class with alias/class resolution, metadata, explicit/prototype/compiled provenance, bounded source previews, exact locations, cache restore, and a searchable editor navigator |
| Routing | PHP attribute, fluent PHP `RoutingConfigurator`, YAML, and XML indexing, including flow-sensitive local configurator/route aliases, inherited `namePrefix()`/`prefix()` chains, direct/default controller targets, and HTTP methods; exact-edit YAML route-option authoring in block/flow declarations with duplicate filtering, legacy `pattern` marking, `requirements` key completion from sibling path placeholders, and indexed route-pattern completion for `path` values; indexed route-pattern completion in positional/named PHP `#[Route]` and legacy `@Route` docblock paths plus conventional route-name suggestions derived from the containing controller/action; persistent PHPDoc `#Route` parameter contracts provide exact route-name completion and definition navigation for matching function, constructor, and inherited method call arguments; reload-aware generated-route fallback from modern `var/cache/*/url_generating_routes.php` and legacy `var/cache` or `app/cache` `*UrlGenerator.php` static-property/constructor formats, with reversed-token path reconstruction, controller defaults, canonical aliases, localized-name normalization, and Assetic filtering while source declarations retain navigation priority; receiver-typed PHP route-name intelligence covers controller `generateUrl()` / `redirectToRoute()` helpers and `UrlGeneratorInterface::generate()` without accepting unrelated same-name methods; name and placeholder completion plus route-parameter navigation from PHP arrays and quoted, unquoted, or shorthand Twig hashes; definition, references, hover, missing-route and local duplicate diagnostics, with every repeated same-file name/URL occurrence preserved across cache restore; exact Twig `_route` equality/inequality, `same as`, and `in`/`not in` comparison references across completion/navigation/hover/diagnostics/typo actions/persistent usages, with prefix checks deliberately excluded; clickable PHP/Twig route-name inlays expose resolved paths as the portable counterpart of inline route folding and navigate to source declarations; deprecated controller class/method diagnostics for direct targets and PHP/Twig route usages plus legacy pattern/requirement diagnostics; controller target/method validation and navigation across route configuration and static Twig `controller()` calls; cached class/service-action completion, Twig hover/diagnostics, cross-language Find References, create-controller-method actions, and bidirectional PHP↔Twig controller-usage code lenses; tag-aware Twig anchor/form completion with automatic `path()` insertion and reverse navigation from concrete, absolute, protocol-relative, or partial URLs after query/fragment normalization, including single-segment placeholder matches with dotted and percent-encoded values; JavaScript/TypeScript route-path completion and reverse navigation in `fetch()`, Axios, `Request()`, `url`, and Axios `baseURL` contexts; segment-aware navigation from positional/named PHP `#[Route]` path prefixes to matching indexed routes; context-aware class/method `Route`, `IsGranted`, `Cache`, and `AsController` attribute completion from lone or incomplete attribute groups with installed-class filtering and conflict-safe imports; contextual import-aware `#[Route]` insertion for public controller methods with generated paths and names; import-aware `Request`/`UserInterface` parameter insertion for public attribute- or annotation-based route actions, including class-level invokable routes; persistent bidirectional routing-resource definition navigation and code lenses between scalar/nested YAML, XML imports, or typed PHP configurator imports and exact direct, recursive, globbed, brace-expanded, or legacy `@Bundle` files, with bundle roots cached from the PHP subtype graph plus cache restore and stale removal; exact-edit resource completion across block/flow/nested YAML, XML, and typed PHP imports from conventional bundle config/controller files and current-directory files/folders |
| Twig templates | Shopware, standard Symfony, namespaced, and legacy bundle template aliases; completion/navigation for extends/include/embed/use/import/from/form_theme, `include()`/`source()`/`block()`, render APIs, and explicit `#[Template]`/`@Template` references; inherited Twig-block completion, multi-target navigation, hover, diagnostics, and typo actions for typed PHP `renderBlock()`/`renderBlockView()` calls, including positional and named arguments; interactive parent-template selection plus parent-chain block-override generation with unsaved override exclusion and cursor-aware snippets; persistent cross-language template usage indexing with Find References, unsaved overlays, cache restore, and ambiguity-guarded `workspace/willRenameFiles` edits that preserve logical-name style; persistent include/embed input contracts with inherited-template and context-flow analysis, key completion, definition, hover, PHP types, explicit-key subtraction, and unsaved overlays; modern colon and legacy equals named arguments; persistent macro declarations, defaulted signatures, namespace/direct/self imports, completion, definition, references, hover, signature help, diagnostics, and typo actions; PHP enum completion, definition, case-aware hover, kind/missing diagnostics, and typo actions for `enum()`/`enum_cases()`; `constant()` completion, PHP definition navigation, typed/deprecated hover, and persistent PHP↔Twig references for namespaced globals, class constants, enum cases, and statically typed object-relative constants; persistent type-resolved Twig usages participate in PHP Find References and rename for public properties, promoted properties, direct/getter-shortcut methods, direct and `constant()` constants, enum cases, enum helpers, and both `@var` orders; missing-template diagnostics for typed render calls plus explicit/empty convention-guessed `#[Template]` and `@Template` declarations, with typo and safe standard-template creation actions; modern and legacy extension functions/filters plus `#[AsTwigFunction]`/`#[AsTwigFilter]` methods, including attribute-only classes, callable-option/callback-trigger deprecation metadata, import-aware registry-to-attribute migration, PHPDoc/`#[Deprecated]` member inspection across typed variables and callable return values, deprecated completion marking, definition, hover, diagnostics, cache restore, and exact persistent Find References from either a Twig symbol or its PHP/node-class callback; persistent extension tests from `getTests()` / `TwigTest`, `Twig_SimpleTest`, `Twig_Test`, `node_class`, and `#[AsTwigTest]` with ordinary `is` / `is not` completion, PHP navigation, deprecation diagnostics, exact persistent Find References, and source-aware `{% guard function|filter|test … %}` type/callable completion and navigation; persistent legacy `getOperators()` and Twig 3.21+/4 `getExpressionParsers()` custom operators, including named/positional aliases and operand-aware completion that excludes Twig-test positions; conservative missing-member diagnostics across typed Twig chains, getter aliases, constants, unions, and callable returns, with unknown/mixed/array/collection/magic receivers excluded; scope-aware Twig 3 fixed and Twig 4 `Twig\Runtime\LoopContext`-derived `loop.*` completion; persistent modern/legacy token-parser tags with completion, PHP definition navigation, and opening/closing deprecation diagnostics; block versioning; typed controller variables from render contexts, local context builders, `@var` comments, and `#[Template]`/`@Template` returns, including clickable top-of-template variable catalogs with typed member navigation and print/conditional/loop insertion plus persistent `createForm(...)->createView()` FormType provenance; interactive Twig `form_row()` generation for selected fields on the exact controller-backed form variable; typed workspace globals from `twig.globals`, PHP extension `getGlobals()`, and compiled-container `addGlobal()` registrations with completion, member inference, definition, hover, shadowing, and cache restore |
| Symfony UX Twig Components | Persistent components from `#[AsTwigComponent]`/`#[AsLiveComponent]`, modern `ux.twig_component` and legacy service tags, configured namespace/name-prefix/template defaults and anonymous directories from YAML plus PHP root arrays, `App::config()`, and returned `ContainerConfigurator::extension()` closures, and anonymous templates including directory `index.html.twig`; compiled `ux.twig_component.component_factory` metadata with class maps, explicit-template precedence, runtime-only components, `template_from_method` suppression of guessed templates, reverse template context, and reload-aware catalog caching; `component()`, `{% component %}`, and `<twig:…>` completion, definition, references, hover, diagnostics, and typo actions; template-to-component-class/usage code lenses and component block-override navigation; public PHP, custom `#[ExposeInTemplate]` mappings, writable `#[LiveProp]`, and `{% props %}` type/default/doc intelligence in component templates and HTML attributes; standard LSP semantic coloration for Symfony UX Toolkit `{# @prop name type description #}` and `{# @block name description #}` annotations; contextual completion and safe imports for Twig/Live Component member and lifecycle attributes; inherited public `#[LiveAction]` methods provide completion, exact PHP definition navigation, typed hover, persistent Find References, missing-action diagnostics, and typo fixes from static `live_action()` calls or modifier-aware `data-live-action-param` values; action arguments in helper hashes and `data-live-*-param` attributes resolve aliased `#[LiveArg]` parameters with duplicate-aware completion, exact parameter navigation, typed hover, diagnostics, and syntax-correct fixes; static PHP `emit()`/`emitUp()`/`emitSelf()` and Twig `live#emit`/`live#emitSelf` emissions resolve inherited repeatable `#[LiveListener]` declarations across completion, exact definition, typed hover, persistent cross-language Find References, diagnostics, and typo actions, while payload keys resolve typed and aliased `#[LiveArg]` listener parameters; cached `computed.*` getter completion, definition, and hover; named-block completion/navigation/diagnostics; mixed block/HTML syntax and invalid `_self` macro-import inspections; unsaved Twig overlays and cache restore |
| YAML compatibility | Composer-version-gated Symfony 2.8 deprecation inspections for invalid backslash escapes, reserved leading indicators, and colons in unquoted block-mapping values, with native-CST recovery support and syntax-safe quote/escape actions |
| Translations | YAML, XLIFF/XML, PHP-array, and compiled-catalog indexing; Twig and type-aware PHP key/domain completion, definition, hover, diagnostics, typo replacement and catalog insertion actions; parameter placeholder completion; interactive extraction of static Twig text or HTML attribute values into selected YAML/XLIFF locale resources with active-domain inference |
| Console | Attribute, legacy class, method-command, `configure()`, modern parameter-attribute, and compiled-container indexing, including named/positional `AsCommand` alias arrays and literal local `self::` / `static::` command-name constants; context-aware `AsCommand` attribute completion for conventional, inherited, and typed-invokable command scopes; type-aware command/argument/option completion, definition, hover, missing-reference diagnostics, and typo actions; helper-name completion, class navigation, and hover for typed `HelperSet::get()` / `has()` and `Command::getHelper()` calls, discovered from persisted literal `getName()` returns; Symfony 7.3 `#[AsCommand]` `__invoke()` return-type/exit-code diagnostics with an `int` return-type action; current-document run code lenses for class-level, method-level, static-name, and `configure()->setName()` declarations execute the canonical command through the workspace `bin/console`; a searchable persistent command catalog exposes aliases, targets, descriptions, arguments, and options to the VS Code command-palette runner and future clients; contextual import-aware insertion of supported Console input/output, cursor, style, and application parameters into invokable commands while preserving optional/variadic parameter order; conservative whole-class migration of direct legacy `Command` subclasses with static `configure()` inputs to invokable commands, including Argument/Option attributes, input-read rewriting, import cleanup, parent-constructor removal, and exit-constant repair |
| Embedded PHP strings | Native lossless JSON validation and semantic coloring for static strings passed to typed `JsonResponse::fromJsonString()` / `setJson()` calls, with alias/subclass resolution, conservative interpolation exclusion, PHP escape decoding, and byte-exact host range mapping; typed DomCrawler `filter()` / `children()` and `CssSelectorConverter::toXPath()` arguments receive native selector-delimiter validation and CSS-aware class, ID, element, attribute, pseudo-function, operator, string, and number coloration; a dedicated lossless XPath frontend structurally validates and colors typed DomCrawler `filterXPath()` / `evaluate()` expressions including axes, functions, element/attribute names, variables, operators, strings, and numbers; the three injection families share one PHP CST scan and one semantic receiver analysis per request; native DQL intelligence is covered under Doctrine |
| HTTP client | Typed option-key completion, exact declaration navigation, and default/type hover for `HttpClientInterface::request()` and `withOptions()` arrays, backed by persisted semantic metadata for `OPTIONS_DEFAULTS` constant-array entries and unsaved receiver-type validation |
| Events | Subscriber arrays, `AsEventListener`, XML/YAML/PHP service tags, dispatch sites, and cross-file event constants; type-aware event/listener-method completion, definition, references, hover, diagnostics, typo actions, and cross-file missing-method creation with inferred event parameter unions |
| Messenger | Persistent message graphs from class/method `AsMessageHandler`, legacy `MessageSubscriberInterface`, and PHP/XML/YAML `messenger.message_handler` service tags; inferred and explicit message types including unions; typed `MessageBusInterface::dispatch()` sites; cross-language message/handler completion, definition, references, hover, diagnostics, typo actions, code lenses, cache restore, and stale removal |
| Environment variables | Persistent declarations from `.env`, `.env.*`, `*.env`, Dockerfiles, and Compose environment mappings/sequences; PHP/XML/YAML `%env(...)%`, PHP `env('…')`, and direct `#[Autowire(env: '…')]` references with chained-processor parsing, completion for incomplete expressions, multi-target definition, Find References, secret-safe hover, cache restore, and stale removal |
| Forms | PHP form types and extensions plus XML/YAML/PHP service aliases; inherited `getParent()` options, `configureOptions()` definitions, `buildForm()` fields, and `data_class` writable properties; type/option/field completion, definition, hover, diagnostics, typo actions, and unsaved-document overlays; persistent controller `createForm(...)->createView()` provenance drives Twig `FormView` child completion, PHP definition navigation, typed hover, missing-field diagnostics, and FormType code lenses on `form_start()`, `form()`, `form_end()`, and `form_rest()`; persisted type/core/extension `buildView()` and `finishView()` assignments provide `form.vars.*` completion, definition, typed/default hover, and typo diagnostics; semantic form-factory recognition and related form-type code lenses for public PHP methods; bidirectional form-type ↔ `data_class` code lenses with multiple reverse targets and unsaved overlays; Composer-version-gated legacy builder-alias inspection with import-aware `::class` migration; interactive generation of missing builder fields from inherited writable properties/setters with scalar/date/enum/Doctrine type guessing, generated options, and collision-safe imports; structured type/option analytics with aliases, parent/data-class relationships, field/view-variable counts, merged option kinds/defaults/allowed types, exact multi-source provenance, cache restore, and a searchable editor navigator |
| Security | Voter-supported attributes, YAML role hierarchy and access-control roles, PHP authorization calls, `IsGranted`/`Security` attributes and legacy annotations, and Twig authorization functions; source-aligned modern SecurityBundle YAML key completion/hover across nested form/JSON/LDAP/Basic/login-link/throttling/remember-me/remote-user/X.509/logout/switch-user/access-token/OIDC contexts and `when@environment` sections; persistent XML/YAML/typed-PHP-configurator user-provider and firewall symbols; cross-format provider-name completion, definition, references, diagnostics, typo actions, cache restore, nested authenticator providers, and unsaved-document overlays |
| Symfony configuration | Persistent root signatures from modern `new TreeBuilder('name')` and legacy `TreeBuilder->root('name')` `getConfigTreeBuilder()` methods; root-key completion, exact declaration navigation, and related code lenses from top-level and `when@environment` PHP arrays and YAML mappings; relative/globbed PHP `imports[].resource` and YAML `resource:` definition navigation and code lenses; bundle/current-directory resource completion in YAML and XML with conventional legacy/modern config and controller roots; cache restore and stale removal |
| Serializer | `deserialize()` target indexing for class constants, string class names, array targets, and the supported `ClassName::class . '[]'` concatenation; definition, references, hover, missing-class diagnostics, typo actions, and class code lenses |
| Validator | Inherited public constraint-option completion, definition, hover, diagnostics, and typo actions for option arrays; `validators`-domain messages in constraint objects/attributes/properties and violation builders; constraint-to-validator and constraint-message-to-catalog code lenses with unsaved PHP overlays |
| Doctrine | Unified ORM/ODM model metadata from PHP attributes, legacy annotations (including `ODM` aliases), XML, and YAML; inherited and embedded fields; typed object managers/repositories and magic finder methods; compiled-container ORM/ODM `Bundle:Model` namespaces plus convention fallbacks shared by completion, navigation, diagnostics, DQL, QueryBuilder, and repository result typing; assigned and unassigned/fluent ORM QueryBuilder alias/relation/field/parameter inference with nested `Expr`, class/right joins, and `indexBy`; typed DBAL QueryBuilder/Connection table, column, and join-alias completion, definition, hover, diagnostics, and typo actions; native standalone DQL in `$dql`, typed `createQuery()`, and `setDQL()` strings with entity/relation-field completion, definition, references, hover, diagnostics, typo actions, unsaved overlays, cache restore, and cached built-in function discovery/navigation; scope-aware ORM class/property/lifecycle attribute completion with namespace-alias reuse, installed-class filtering, and automatic `HasLifecycleCallbacks`; mapping class/property/lifecycle/type intelligence; inheritance/discriminator metadata plus subtype-aware discriminator-map completion, definition, hover, diagnostics, typo actions, and references across PHP attributes/annotations and XML/YAML; table indexes/unique constraints with normalized field/column intelligence across PHP attributes/annotations and XML/YAML; cached custom DBAL/ODM type discovery with literal or class-constant-backed `getName()` names, static DoctrineBundle YAML/XML and PHP `extension()` DBAL aliases, static runtime `addType`/`overrideType`/type-registry registrations, conventional class-name fallback, and mapping-filename-aware ORM/MongoDB/CouchDB/ODM scoping; type-registration class completion, definition, hover, missing/invalid diagnostics, typo actions, and cross-language Find References across registration keys, implementation classes, PHP attributes, and XML/YAML usages; bidirectional PHP model/external mapping code lenses and typed repository-call related navigation; type-checked, ambiguity-safe string entity migration to import-aware `::class` references across repository/object-manager APIs including `find`; structured entity and field analytics expose model kind/source, table, repository, inheritance, source locations, inherited/embedded field paths, mapping/PHP/enum/relation types, declaring classes, and constraint counts, with a searchable VS Code entity-to-field navigator |
| Assets, AssetMapper, Encore, Assetic, and Vite | Static `public`/`web` and bundle `Resources/public` resources with dynamic-media pruning; `manifest.json`, `entrypoints.json`, Webpack Encore, `importmap.php`, installed AssetMapper modules, and `vite.config.js`/`.ts` Rollup input indexing including variable and spread maps; named packages from Symfony YAML, XML/PHP service tags, and inferred Shopware bundle packages, with package-aware bundle/base-path/theme resolution; native legacy Assetic `stylesheets`/`javascripts` blocks with direct file, directory, glob, bundle, and lazily refreshed compiled named-formula resolution; Twig `asset()`, `importmap()`, Encore, and Vite entry helpers plus type-aware PHP `Packages` calls; path, package, and entrypoint completion, definition, references, hover, diagnostics, typo actions, target-file code lenses, and persistent cache restore |
| Stimulus | Persistent controllers from conventional JS/TS filenames, `startStimulusApp()` registrations, and enabled `controllers.json` package entries; normalized HTML and original Twig names; `stimulus_controller()` and multi-controller `data-controller` completion, definition, references, hover, diagnostics, typo actions, stale removal, unsaved overlays, and cached Twig usages, with plain HTML handled on demand to avoid scanning generated frontend trees |
| PHP | Workspace semantic graph, type inference, completion, definition, hover, references, signature help, rename, and diagnostics; persistent PHPDoc `#Class`, `#Interface`, and `#ClassInterface` parameter contracts with kind-filtered exact completion/navigation for function, constructor, and inherited method call arguments; context-aware Symfony `Response::HTTP_*` completion in status-code setters and comparisons |
| Project navigation | Ranked and capped `workspace/symbol` search for Symfony services, route names and normalized concrete/absolute/partial route-URL matches, console commands, Twig templates/blocks/macros/functions/filters, Doctrine entities/tables, Twig/Live Components, and translation keys, with exact declaration ranges where available; route symbols expose normalized endpoint method/path/controller metadata; bidirectional controller/template/route, form-type, and Doctrine model/mapping code lenses backed by indexed references; PHP, YAML, and XML route declarations expose portable endpoint code lenses that resolve direct, service, and invokable controller actions; structured route and Doctrine analytics catalogs power searchable editor browsers for endpoints/controllers/templates and entity fields |
| Project scaffolding | Portable command-palette and folder-context generators for Console commands, controllers, form types, Twig extensions, compiler passes, kernel/web tests, and YAML/XML/PHP service configurations; most-specific Composer PSR-4 namespace resolution, indexed command-prefix reuse, Symfony/Twig-version-aware templates, native-parser validation, symlink-aware workspace boundaries, collision rejection, and atomic editor workspace edits |
| Shopware | Snippets, feature flags, system config, theme config/icons, extensions, and Administration components |

PHPDoc assistant contracts from the plugin's `DocHashTagReferenceContributor`
are persisted in the PHP semantic graph. `#Route`, `#Service`, `#Parameter`,
`#Class`, `#Interface`, `#ClassInterface`, `#Entity`, `#FormType`, and
`#Template` provide exact completion and definition navigation at function,
constructor, and inherited method call arguments. `#TranslationKey` and
`#TranslationDomain` additionally correlate named or positional sibling
arguments, with `messages` as the default key domain. Every assistant contract
also participates in domain-aware missing-reference diagnostics and exact typo
replacement actions; class-family diagnostics preserve the tag's class versus
interface kind constraint.

## Planned ports

### 1. Symfony reference validation

- Missing asset, service, parameter, class, alias-target, and Twig template
  diagnostics are implemented.
- Deprecated services and their PHP classes are marked in completion and
  diagnosed across PHP, XML, and YAML references, including optional
  references and compiled-container `<deprecated>` metadata.
- Create-definition code actions. Route/template/service/parameter/class typo
  suggestions and replacement actions are implemented.
- Cross-file duplicate analysis. Document-local duplicate route, service, and
  parameter diagnostics are implemented.
- Controller target and method validation is implemented for YAML, XML, and
  static Twig `controller()` references, including class/route-service
  completion, definition, hover, persistent Find References, bidirectional
  PHP/Twig code lenses, and a create-method action (with route placeholders
  where available).
- Required constructor argument validation is implemented for XML and YAML
  services, including typed service suggestions and insertion actions. Existing
  XML argument slots also offer one rewrite per type-compatible service,
  preserving named-argument metadata.
- Undefined `$this->property` references inside indexed service classes offer
  ranked constructor-injection rewrites, preserving the class's promoted or
  traditional injection style and PHP language level.
- YAML named constructor arguments have completion, PHP parameter navigation,
  exact invalid-key diagnostics, and typo replacement actions, including
  inherited constructors and incomplete `$name` completion contexts.
- Service references supplied to constructors and configured method calls are
  validated against PHP parameter types across XML, YAML, PHP configurator
  `args()`/`arg()`, and legacy PHP `arguments` arrays, including interfaces,
  inheritance, nullable/unions, aliases, unsaved local definitions, and
  syntax-preserving compatible-service replacement actions.
- Conventional XML/YAML service tags are checked against their required class
  or interface contracts, including alternative modern/legacy Twig APIs.
  Contextual XML/YAML rewrite actions infer and insert absent conventional tags
  from the configured class hierarchy.
- Symfony container constants in XML and modern/legacy YAML syntax have
  completion, PHP definition navigation, and missing-reference diagnostics for
  global constants, inherited class constants, and enum cases.

### 2. Twig and translation intelligence

- Typed Twig globals from YAML configuration, PHP extensions, and the compiled
  container are implemented. Infer additional event-subscriber variables.
- Twig `{% types %}` declarations are implemented with optional variables,
  escaped PHP class completion/navigation, persistent PHP references, direct
  and loop-element member inference, and typed `if`/`for` suggestions shared
  with `@var` declarations.
- Twig `@var` comments expose semantic keyword, variable, and type tokens for
  both declaration orders, unions, array types, and multiple declarations.
- Twig `@see` documentation comments resolve PHP classes/methods, controller
  service actions, logical templates, and workspace-relative files, with
  class/method/template completion and legacy bare-target compatibility.
- Template usage references and safe file move/rename support are implemented
  for static Twig tags/functions, PHP render APIs, and explicit
  `#[Template]`/`@Template` mappings. Dynamic template expressions remain
  intentionally excluded.
- Standalone static PHP strings ending in `.twig` provide the reference
  plugin's exact-template definition fallback without becoming indexed usages
  or rename targets.
- Related-item code lenses cover reverse template references, direct extending
  templates, same-name overrides, ancestor blocks, and transitive block
  implementations, with unsaved `extends` and block declarations overlaid.
- PHP `renderBlock()` and `renderBlockView()` block-name intelligence is
  implemented for `AbstractController` subclasses, including inherited block
  declarations, named arguments, typo diagnostics, and quick fixes.
- Include/embed parameter intelligence, macro signatures/imported-call
  intelligence, and Symfony UX Twig Component names, props, variables, and
  blocks are implemented.
- Deprecated PHP fields/getter methods in Twig accessors are detected from
  PHPDoc and `#[Deprecated]`, including members reached through typed variables
  and Twig function/filter return types.
- Modern and legacy Twig token-parser tags are persisted with custom-tag
  completion and PHP navigation. PHPDoc, `#[Deprecated]`, and parse-time
  `trigger_deprecation()` metadata produces hint diagnostics on opening and
  conventional `end…` closing tags.
- Symfony UX lifecycle/PHP-attribute completion (`ExposeInTemplate`,
  `PreMount`, `PostMount`, `LiveProp`, `LiveAction`, `LiveListener`,
  `LiveArg`, and hydration/rerender hooks), custom exposed names, writable
  Live props, and the `computed.*` getter proxy are implemented.
- Live Action names and `#[LiveArg]` arguments are implemented across helper
  hashes and modifier-aware data attributes, including completion, navigation,
  hover, diagnostics, typo actions, and persistent action references.
- Static Live Component emitted events and `#[LiveListener]` payload arguments
  are implemented across PHP and Twig, including scoped emit variants,
  repeatable and inherited listeners, `#[LiveArg]` aliases, completion,
  navigation, hover, diagnostics, typo actions, and persistent cross-language
  event references.
- Controller-backed Twig `FormView` child completion, definition, hover, and
  missing-field diagnostics are implemented from persistent form-type
  provenance. `form.vars.*` metadata is also persisted from direct and
  `array_replace()` assignments in type/core/extension `buildView()` and
  `finishView()` methods.

### 3. Symfony framework APIs

- HttpClient `request()` / `withOptions()` option completion and declaration
  navigation are implemented from persisted typed constant-array metadata,
  with duplicate-key filtering and option default hover.
- Console helper completion, class navigation, and hover are implemented for
  typed helper-set/command calls from persisted literal helper-name returns.
- Console declaration run markers are implemented as current-document code
  lenses. The VS Code client launches the canonical command as a process task
  through the workspace `bin/console`, avoiding shell interpolation and
  supporting a configurable PHP executable.
- The persistent command catalog is exposed through a structured language
  server request and powers `Symfony: Run Console Command…`, the portable
  equivalent of the plugin's Run Anything provider. Catalog entries include
  aliases, implementation targets, arguments, options, exact file URIs, and
  project-relative paths for other clients; text and Ant-style source-file
  filters match the plugin's machine-readable collector.
- Dotenv/Docker/Compose declarations and `%env(...)%` completion, navigation,
  references, processor-aware hover, cache restore, and stale removal are
  implemented. Runtime-only variables intentionally do not produce missing
  diagnostics.
- Composer-version-aware selection between legacy and modern SecurityBundle
  schemas, plus enum/value completion. The implemented nested YAML schema
  follows SecurityBundle 7.4, while generic PHP config-root
  completion/navigation and imported-resource navigation come from persisted
  `ConfigurationInterface` tree signatures.
- Serializer metadata-group support and validation mapping-file schemas.

### 4. Doctrine

- Native SQL/native-query strings and dynamically assembled DQL expressions;
  static standalone DQL strings in `$dql`, typed `createQuery()`, and
  `setDQL()` contexts are implemented.
- Assigned and unassigned/fluent ORM QueryBuilder chains, nested `Expr`
  methods, right joins, and `indexBy` fields are implemented.
- Built-in DQL functions are discovered from Doctrine ORM's parser registry
  and provide completion, implementation navigation, and hover.
- Custom Doctrine type names from literal and class-constant-backed
  `getName()` returns, static DoctrineBundle YAML/XML `dbal.types` aliases,
  PHP `extension('doctrine', …)` registrations with string/imported
  `::class`/expanded class values, static `Type::addType()` /
  `Type::overrideType()` and type-registry registrations, mapping-manager
  scoping, and conventional class-name fallback are implemented.
  Configuration-schema completion, generated fluent configuration builders,
  and dynamically constructed/injected registry registrations remain.
- Legacy ORM/ODM namespace maps from compiled containers and convention-based
  bundle fallbacks resolve `Bundle:Model` consistently in repository APIs,
  DQL, QueryBuilder, completion, definitions, diagnostics, and PHP result
  inference.
- Typed object-manager `find()` calls return the requested mapped model, and
  `getRepository()` returns a configured custom repository class when present
  while retaining Doctrine result and magic-finder inference.
- Incomplete XML mappings recover semantic contexts from empty model,
  repository, property, target, embedded-class, enum, lifecycle, and type
  attributes, and root ODM `embedded-document`/`embedded` declarations are
  indexed as embeddable models.
- Mapped PHP property metadata preserves namespace-resolved nullable, union,
  intersection, and parenthesized DNF types; the native PHP parser recognizes
  DNF-typed property declarations losslessly.
- MongoDB ODM PHP reference/embed attributes and annotations resolve
  `targetDocument` alongside ORM `targetEntity`/`class`, so untyped document
  properties retain exact target ranges and metadata.
- Discriminator maps are implemented across PHP attributes, legacy
  annotations, XML, and YAML with subtype-aware completion, definition, hover,
  missing/invalid diagnostics, typo actions, and reference navigation. Table
  indexes and unique constraints are implemented across PHP attributes/legacy
  annotations, XML, and YAML with field/column completion, definition, hover,
  diagnostics, typo suggestions, Find References, and analytics counts. Named
  queries, second-level cache configuration, and advanced ODM mapping options
  remain.
- Portable related-file code lenses between PHP models and external mappings
  are implemented, including reverse mapping-to-class navigation and typed
  repository-call model targets.

### 5. Assets and frontend tooling

- Named Symfony/Shopware asset package discovery and package-aware resolution
  are implemented for Twig and typed PHP, including bundle and dynamic theme
  paths.
- AssetMapper/importmap entrypoint metadata and Twig references are implemented,
  including installed-module target resolution and cache restore.
- Static asset completion/navigation in ordinary Twig stylesheet, script, and
  image attributes is implemented, including automatic `asset()` insertion,
  hover, cross-style references, and low-noise typo diagnostics.
- Stimulus controller discovery and Twig/HTML intelligence are implemented for
  conventional JS/TS files, explicit registrations, and `controllers.json`.
- Symfony Vite entrypoint indexing and Twig helper intelligence are implemented
  for direct, variable, and spread-based Rollup inputs, including navigation
  between config declarations, target files, and persistent Twig usages.
- Legacy Assetic named assets and `{% stylesheets %}`/`{% javascripts %}` tags
  are implemented, including native parsing, direct bundle/directory/glob
  operands, compiled-container formula reloads, completion, definition,
  references, hover, diagnostics, and typo actions.

### 6. LSP-native navigation surfaces

- Workspace symbols are implemented for services, container parameters, routes
  and concrete route URL matches, conventional and route-backed controller
  actions, commands, templates, Twig
  blocks/macros/extensions, Doctrine entities/tables, Twig/Live Components,
  translation domains, and translation keys.
- The plugin's service-locator collector is implemented as a structured
  language-server request accepting an exact service ID or PHP class. It
  resolves aliases and inherited classes, returns matching service IDs with
  tags/autowire/deprecation/parent/decorator metadata, distinguishes explicit,
  PSR-4 prototype, and compiled origins, and includes bounded source previews
  with exact locations; `Symfony: Locate Service…` provides the editor surface.
- The plugin's generated service-definition collector is implemented as a
  structured multi-class request over the existing YAML/XML/PHP-fluent/
  PHP-array generator. It returns per-class errors without discarding valid
  definitions and exposes every ambiguous constructor/setter parameter with up
  to 15 type-compatible service IDs, also appending format-correct suggestion
  comments; `Symfony: Generate Service Definitions…` opens the combined output.
- The plugin's machine-readable route collector is implemented as a structured
  language-server analytics request with route-name, controller, partial/full
  URL, and Ant-style controller-file filters. It returns normalized methods,
  declaration/controller locations, resolved service actions, and templates;
  `Symfony: Browse Routes…` provides the interactive editor surface.
- The plugin's profiler-request collector is implemented for local Symfony
  profiler storage. It discovers modern and legacy cache indexes, safely reads
  bounded raw or gzip profile blobs, supports URL/hash/controller/route
  filters, and correlates request metadata with controller source, static and
  runtime Twig templates, entry views, root form types, rendered Symfony UX
  Twig Components, and sent-mail subjects. `Symfony: Browse Profiler
  Requests…` navigates runtime components to indexed PHP classes or anonymous
  templates and opens matching mail panels only after an explicit user
  selection; HTTP profiler fetching is intentionally excluded so analytics
  never perform workspace-triggered network requests.
- The plugin's Doctrine entity and field collectors are implemented as
  structured language-server analytics requests. Entity results support text,
  kind, and Ant-style source-file filters; field results include inherited and
  flattened embedded paths, mapping/PHP/enum/relation types, declaring classes,
  and exact source locations. `Symfony: Browse Doctrine Entities…` provides
  the interactive editor surface.
- The plugin's form-type and form-option collectors are implemented as
  structured language-server analytics requests. Type results expose every
  class and legacy/container alias with parent/data-class relationships and
  effective option, field, and view-variable counts; option results merge
  inherited and extension declarations while retaining every declaration kind,
  default, allowed type, source class, and exact source location. `Symfony:
  Browse Form Types…` provides the type-to-option editor navigator.
- The plugin's Twig extension collector is implemented as a structured
  language-server analytics request with partial-name and type filters. It
  returns functions, filters, tests, and token-parser tags with callback
  classes/methods, typed optional parameters, usage signatures, deprecations,
  and source locations; `Symfony: Browse Twig Extensions…` provides the
  interactive editor surface.
- The plugin's Twig template-usage collector is implemented as a structured
  language-server analytics request with partial logical-name, project-relative
  path, and Ant-style target-file filters. It correlates physical templates,
  controller actions and routes, include/embed/extends/import/use/form-theme
  edges, and component composition with exact source locations;
  `Symfony: Analyze Twig Template Usages…` provides the editor surface.
- The plugin's Twig component collector is implemented as a structured
  language-server analytics request that aggregates attribute, service,
  compiled-container, and anonymous-template declarations by canonical name.
  Results include templates, typed/live/writable props, computed values,
  blocks, usages, exact source locations, and ready-to-use
  HTML/function/composition syntax; `Symfony: Browse Twig Components…`
  provides the editor surface.
- The plugin's Twig template-variable collector is implemented as a structured
  language-server analytics request with comma-separated logical names,
  project-relative paths, and Ant-style target-file globs. It merges controller
  render contexts, inherited input contracts, native `@var` annotations,
  globals, and component props, then returns canonical PHP types, provenance,
  and exact first-level Twig-accessible property/getter/method declarations;
  `Symfony: Analyze Twig Template Variables…` provides the editor surface.
- Controller/template/route—including persistent Twig `controller()` usages—
  form-type, Doctrine model/mapping, and DI decorator/parent/prototype code
  lenses, plus PHP class-to-service and effective-autowire constructor lenses,
  PHP/YAML Symfony configuration root/resource lenses, and YAML/XML/PHP
  routing-resource lenses are implemented as portable equivalents for IntelliJ
  related-item gutter markers. Symfony Endpoints are mapped to enriched route
  workspace symbols and inline PHP/YAML/XML method/path lenses that navigate to
  direct, service, or invokable controllers. Unambiguous Twig templates,
  routing imports, Symfony configuration resources, and configuration-root
  declarations also expose native clickable document links; multi-target and
  globbed references remain on definition/code-lens pickers. Add remaining
  high-value gutter targets.
- Inlay hints for resolved XML/YAML service arguments, clickable YAML/XML/PHP
  route controllers—including service aliases and invokable controllers—and
  clickable Twig-variable catalogs with typed member navigation plus
  print/conditional/collection-loop insertion.
- Related-file navigation between controllers, templates, routes, form types,
  and Doctrine models/mappings is implemented.

IDE-only profiler panels, settings UI, and terminal integration are out of the
language-server core. The plugin's project-tree file generators are exposed
through one portable, version-aware scaffold command and thin editor-extension
prompts; contextual generators remain code actions where source position is
part of their semantics.
