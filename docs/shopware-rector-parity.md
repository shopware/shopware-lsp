# Shopware Rector parity

The `shopware.migration` inspection is the native LSP counterpart of the
Shopware-specific transformations in `frosh/shopware-rector`. The current
matrix was audited against commit
`8cae3156e421142bcd0dc75fe3ae904c080d6cb7`.

The LSP treats migration sets cumulatively: when a project targets Shopware
6.8, diagnostics from the 6.5, 6.6, 6.7, and 6.8 sets can all apply. An
unknown Shopware version disables the inspection rather than guessing.

## Covered configuration

| Rector configuration | Native diagnostic coverage |
| --- | --- |
| `v6.5/flysystem-v3.php` | 3 class renames, 48 method renames across the four configured filesystem types, and 8 visibility constant moves |
| `v6.5/renaming.php` | 14 class renames, 3 method renames, removed call/constructor arguments, three route-annotation migrations, thumbnail batching, FlowState property migration, three interface-to-abstract-class migrations, and Faker property calls |
| `v6.5/typehints.php` | 10 parameter types, 4 return types, 2 required method parameters, the ThumbnailService strict argument, and reverse-proxy `banAll()` |
| `v6.5.0` | EntityExtension compatibility method and AbstractMessageHandler migration |
| `v6.6/renaming.php` | 2 new class renames, 2 method renames, 1 static method move, and 6 constant moves |
| `v6.6/exceptions.php` | 12 exception-constructor to static-factory migrations |
| `v6.7/renaming.php` | 9 constant moves |
| `v6.7.0` | EntityExtension cleanup, ScheduledTaskHandler exception logger, and Elasticsearch return type |
| `v6.8/renaming.php` | EntitySearchResult delegation, ProductStream criteria migration, ProductStreamBuilder abstract class, and 12 constant moves |

`ContextMetadataExtensionToStateRector` is also supported as a 6.5 migration,
although the audited upstream set does not currently register that local rule.

The generic `MakeClassConstructorArgumentRequiredRector` has no Shopware set
configuration in the audited revision, so there is no versioned class and
argument mapping to activate in the LSP.

## Quick-fix safety

Every covered transformation produces a diagnostic. A quick-fix is bound only
when the lossless CST and semantic index prove that the edit is mechanical.
Examples that intentionally remain diagnostic-only include conflicting parent
classes, a Faker property used as an assignment target, named-argument layouts
whose position cannot be changed safely, and EntityExtension implementations
where an entity-name constant cannot be derived.

ProductStream patterns recognized by Rector are rewritten directly. Other
typed `buildFilters()` uses receive Rector's migration TODO as a quick-fix,
without repeatedly adding the same comment. Grouped imports whose target moves
outside the group namespace remain diagnostic-only.

## External Rector sets

The Shopware set files also import Rector-owned Symfony 5.4 through 7.2 and PHP
7.4 through 8.2 sets. Those rules are not defined or version-pinned by
`frosh/shopware-rector` (the audited checkout has neither a lock file nor a
vendored Rector tree), so they are not duplicated in `shopware.migration`.
Language-level PHP diagnostics and Symfony compatibility inspections remain
owned by their existing LSP subsystems.

The patch-only Shopware 6.6.4 and 6.6.10 files contain only those external
Symfony set imports and therefore add no Shopware-owned migration table entry.

## Verification

Mapping-count tests guard the explicit configuration in
`shopware_migration_api_test.go` and
`shopware_migration_declarations_test.go`. Diagnostic and inspection tests
cover version boundaries, semantic type matching, safe/unsafe edits, and
parse-valid output for every custom rule family.
