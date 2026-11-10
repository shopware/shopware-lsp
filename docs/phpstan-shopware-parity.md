# phpstan-shopware parity

The `shopware.php.local`, `shopware.php.semantic`, `shopware.dal.definition`,
`shopware.criteria` and `shopware.migration.announced` inspections are the
native LSP counterpart of the rules in `shopware/phpstan-shopware`. The matrix
was audited against commit `c54c45b` (2026-10-23).

The LSP reports them per document, in the editor, in `check` and through MCP,
without a PHPStan run. Suppression comments written for PHPStan
(`@phpstan-ignore <code>`) keep working with the LSP codes.

## Default rules (`rules.neon`)

| PHPStan rule | LSP code |
| --- | --- |
| `ClassExtendUsesAbstractClassWhenExisting` | `shopware.php.decorator.concrete_extension` |
| `DisallowDefaultContextCreation` | `shopware.php.context.default` |
| `DisallowFunctionsRule` | `shopware.php.function.disallowed` |
| `DisallowSessionFunctionsRule` | `shopware.php.session.function` |
| `ForbidGlobBraceRule` | `shopware.php.glob_brace` |
| `InternalClassExtendsRule` | `shopware.php.internal.class_extension` |
| `InternalFunctionCallRule` | `shopware.php.internal.function_call` |
| `InternalMethodCallRule` | `shopware.php.internal.method_call` |
| `MethodBecomesAbstractRule` | `shopware.migration.announced.abstract` |
| `NoDALFilterByID` | `shopware.criteria.id-filter`, with a quick fix |
| `NoSessionInPaymentHandlerAndStoreApiRule` | `shopware.php.session.payment_handler`, `shopware.php.session.store_api` |
| `NoSymfonySessionInConstructorRule` | `shopware.php.session.constructor` |
| `NoUserEntityGetStoreTokenRule` | `shopware.php.user.store_token` |
| `ScheduledTaskTooLowIntervalRule` | `shopware.php.scheduled_task.interval` |
| `SetForeignKeyRule` | `shopware.php.foreign_key_checks.disabled` |
| `NoEntityRepositoryInLoopRule` | `shopware.php.repository.in_loop` |
| `NoSuperglobalsRule` | `shopware.php.superglobal` |
| `ForbidLocalDiskWriteRule` | `shopware.php.filesystem.local_write` |
| `BestPractise\PreferRouteEventRule` | `shopware.php.listener.route_condition` |
| `BestPractise\DALDefinitionRule` | `shopware.dal.entity_property.*` |
| `ForwardSalesChannelContextToSystemConfigServiceRule` | `shopware.php.system_config.sales_channel` |
| `ForbidPredictableSaltRule` | `shopware.php.crypto.predictable_salt` |
| `ForbidWeakCryptoKeyRule` | `shopware.php.crypto.weak_key` |
| `ForbidInsecureCookieRule` | `shopware.php.cookie.insecure` |
| `ForbidInsecureSymfonyCookieRule` | `shopware.php.cookie.symfony_insecure` |
| `ForbidDisabledSslVerificationRule` | `shopware.php.tls.verification_disabled` |
| `NoEmptyResponseRule` | `shopware.php.response.empty` |

### Deliberate differences

- **Internal API.** PHPStan reports any `@internal` parent class. The LSP
  reports Shopware's, from outside the package that declares it, as it does
  for internal methods and functions.
- **Foreign key checks.** PHPStan reports every string mentioning
  `FOREIGN_KEY_CHECKS`; the LSP reports the ones that set it to `0`.
- **Repositories in loops.** The LSP also looks into `while` and `do … while`
  loops and accepts subclasses of `EntityRepository`.
- **`@abstract` docblocks.** `MethodBecomesAbstractRule` reads `@abstract`
  in the PHPDoc of a parent method. The index keeps that tag, and
  `shopware.migration.announced.abstract` reports a concrete subclass
  without an implementation, as it does for `#[BecomesAbstract]`. Abstract
  subclasses are left alone; they do not have to implement it yet.
- **Entity definitions.** PHPStan collects definitions and entities over the
  whole run and reports in the entity file. The LSP checks a definition
  against the indexed entity class when the definition is analyzed, and
  reports on the field, where the name can be corrected. Only definitions
  with their own `getEntityClass()` are checked; the default `ArrayEntity`
  needs no properties. Fields the DAL index gives no property name
  (`CustomFields`, `ReferenceVersionField`, `TranslationsAssociationField`
  and the other fields without a name argument) are skipped.
- **Local disk writes.** A path counts as temporary when it mentions
  `sys_get_temp_dir()`, `tempnam()`, or something named `temp` or `tmp`, or
  is a standard stream. PHPStan's check is the same heuristic over variable
  names and concatenations.
- **Empty responses.** The index keeps no default values, so
  `new Response()` is recognized by the optional string body parameter of
  the constructor rather than by its `''` default.
- **Route listeners.** Event classes (`RequestEvent::class`) are accepted as
  subscribed event names, and a method-level `#[AsEventListener]` without an
  event takes it from the parameter type, as Symfony does.
- **Symfony cookies.** PHPStan looks inside class methods only; the LSP
  checks every `new Cookie`, `Cookie::create()` and `withSecure()` call.

## Future compatibility (`future-compatibility.neon`)

PHPStan keeps these rules opt-in. The LSP reports them with the other
migration diagnostics (`shopware.migrations` domain): the attributes describe
changes that are already decided, and the diagnostics disappear with them.
Code inside a class or function marked `@deprecated tag:<version>` for the
same version is exempt, as in PHPStan, because it is removed with the change.
Shopware's own packages may keep using what becomes internal.

| Attribute | PHPStan identifier suffix | LSP code |
| --- | --- | --- |
| `BecomesInternal` | `methodBecomesInternal`, `classBecomesInternal`, `extendsClassBecomingInternal` | `shopware.migration.announced.internal` |
| `BecomesFinal` | `extendsClassBecomingFinal` | `shopware.migration.announced.final` |
| `BecomesAbstract` | `missingBecomesAbstractMethodImplementation` | `shopware.migration.announced.abstract` |
| `VisibilityChange` | `methodVisibilityChange`, `propertyVisibilityChange` | `shopware.migration.announced.visibility` |
| `ParameterRemoval` | `parameterRemoval` | `shopware.migration.announced.parameter.removed` |
| `ParameterNameChange` | `parameterNameChange` | `shopware.migration.announced.parameter.renamed` |
| `NewRequiredParameter` | `newRequiredParameter` | `shopware.migration.announced.parameter.required` |
| `NewRequiredParameter`, `NewOptionalParameter`, `ParameterTypeNarrowing` | `newParameterType`, `parameterTypeNarrowing` | `shopware.migration.announced.parameter.type` |
| `BecomesReadonly` | `propertyBecomesReadonly` | `shopware.migration.announced.property.readonly` |
| `PropertyTypeNarrowing` | `propertyTypeNarrowing` | `shopware.migration.announced.property.type` |
| `BecomesReadonly`, `PropertyTypeNarrowing`, `PropertyTypeWidening`, private `VisibilityChange` | `propertyRedeclaration` | `shopware.migration.announced.property.redeclared` |
| `ReturnTypeWidening`, `PropertyTypeWidening` | none; see type extensions | `shopware.migration.announced.type.widened` |
| `NewOptionalParameter` | `missingNewOptionalParameterInOverride` | `shopware.migration.declaration.parameter.add`, with a quick fix |
| `ParameterTypeWidening` | `parameterTypeWideningInOverride` | `shopware.migration.declaration.type`, with a quick fix |
| `ReturnTypeNarrowing` | `returnTypeNarrowingInOverride` | `shopware.migration.declaration.type`, with a quick fix |
| `ParameterDefaultValueChange` | `parameterDefaultValueChange` | `shopware.migration.arguments.add_default`, with a quick fix |
| `ClassMoved` | `classMoved` | `shopware.migration.api.class.rename`, with a quick fix |

The last five rows are shared with
[Shopware Rector](shopware-rector-parity.md#announced-changes-bcchange-attributes),
which rewrites what PHPStan only reports.

A type check reports only what can never fit: `'three'` for a parameter
narrowed to `int`, not an `int|string` value. Arguments behind a spread and
types naming classes the index does not know are left alone. The two
attributes announcing parameters are also what lets the PHP analysis accept
the extra argument today (PHPStan's
`AnnouncedParameterCountIgnoreExtension`).

`ParameterDefaultValueChange` is checked for constructor calls, the only
place Shopware uses it.

## Type extensions

phpstan-shopware also registers three type extensions. They report nothing
themselves; they change the types PHPStan's own rules see.

- `FutureReturnTypeExtension` and `FuturePropertyTypeExtension` make PHPStan
  analyze calls and property reads with the type announced by
  `ReturnTypeWidening` and `PropertyTypeWidening`. The LSP does the same in
  a second analysis of the document, run only when it uses such a
  declaration: the announced types replace the declared ones, and what the
  PHP analysis then reports in addition (an argument or a returned value
  that no longer fits, also after the value went through a variable and
  with narrowing such as `!== null` respected) is reported as
  `shopware.migration.announced.type.widened`. The same code marks a
  value that is dereferenced with `->` and may be null only with the
  announced types, directly or after it went through a variable.
  Hover, completion and the other diagnostics keep the types of the
  installed version.
- `CollectionHasSpecifyingExtension` narrows `$collection->get($key)` to
  non-null after `$collection->has($key)`. The PHP inference does this for
  every `has()`/`get()` and `hasX()`/`getX()` pair, Shopware collections
  included.
