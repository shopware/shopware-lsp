# Store review rules

Besides the [phpstan-shopware rules](phpstan-shopware-parity.md), the
`shopware.php.semantic` inspection has rules of its own for findings that
recur in Shopware Store reviews. Each one matches a pattern that a single
file proves; what needs context (ownership, permissions, cleanup, payments)
is left to tests and reviewers. The identifiers in the last column are those
of the Store preflight rules in `FriendsOfShopware/agent-skills`.

| Code | Reports | Left alone | Preflight |
| --- | --- | --- | --- |
| `shopware.php.repository.unbounded_search` | `search()` with a `new Criteria()` that has no IDs and that nothing restricted: inline, or a variable used nowhere else | `searchIds()`, a criteria that was passed on or had any method called on it, a criteria received as a parameter | RUN-04 |
| `shopware.php.system_config.constructor` | a `SystemConfigService` read in `__construct()` | reads in any other method | RUN-01, INT-01 |
| `shopware.php.state.translated_name` | `getName()` of a state machine state compared with a string literal | comparing `getTechnicalName()`, printing the name, comparing two names | COM-04 |
| `shopware.php.message.entity_payload` | a property or constructor parameter of an asynchronous message typed as an entity, an entity collection or a `SalesChannelContext` | classes that are not `AsyncMessageInterface` messages, other methods of the message | RUN-05 |
| `shopware.php.route.get_mutation` | `update()`, `upsert()` or `delete()` on an entity repository inside a route that answers only `GET` or `HEAD` | `create()` (logging a visit), routes that also accept another method, helper methods | SEC-09 |
| `shopware.php.route.acl_missing` | an Admin API route (`_routeScope` `api`) accepting `POST`, `PUT`, `PATCH` or `DELETE` with no `_acl` default on the method or the class | routes with `_acl`, including the empty list; routes without `methods`; other scopes | SEC-01 |
| `shopware.php.filesystem.getcwd` | `getcwd()` | — | INT-04 |
| `shopware.php.function.debug_output` | `print_r()` and `var_export()` that print, `debug_print_backtrace()`, `debug_zval_dump()`, `phpinfo()`, `xdebug_break()`, `xdebug_var_dump()` | `print_r($value, true)` and `var_export($value, true)` | REL-07 |
| `shopware.php.catch.empty` | an empty `catch` of `\Throwable` or `\Exception` without a comment | a catch of a specific exception, a block with a comment | UI-03, INT-01 |

They are warnings and on by default; switch one off with
`diagnostics.rules.<code>`, or suppress one place with
`@phpstan-ignore <code>` or `@noinspection <code>`.

## Opting out of the privilege check

A missing `_acl` does not prove a missing check: when the route writes
through a repository with the caller's context, the DAL checks the
privileges. A route that needs no privilege of its own says so with an
empty list, which Shopware treats like no `_acl` at all:

```php
#[Route(path: '/api/_action/acme/preview', defaults: ['_acl' => []], methods: ['POST'])]
```

The declaration can also sit on the class, for all its routes.

## Considered and not added

- **Raw SQL, broad catches, inline styles, build artifacts by file name.**
  The review history has valid counterexamples for each.
- **Twig `raw` on configuration values, consent, cache variation, cleanup,
  payment callbacks.** They need the rendered page, a running shop or the
  intent of the extension.
