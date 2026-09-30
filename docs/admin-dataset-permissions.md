# Admin SDK dataset privileges

Shopware apps that read Administration data with the Meteor Admin SDK must
declare a `read` privilege for every entity in that data. Without it, the
Administration fails the call at runtime:

```text
Error: Your app is missing the privileges read:language, ... for action "datasetSubscribe".
```

`shopware.admin.dataset` checks this while editing an app's Administration
sources. It runs on JavaScript, TypeScript, and Vue files under
`Resources/app/administration/` that belong to an app (`manifest.xml`). The
same diagnostics are published to the editor, MCP, and `shopware-lsp check`
(plain text or `-json`).

## Diagnostics

| Rule | Severity | When |
|---|---|---|
| `admin.dataset.permission-missing` | Error | A literal `data.subscribe` or `data.get` selector needs a `read` privilege the manifest does not grant. |
| `admin.dataset.unscoped-subscription` | Warning | `data.subscribe` has no `selectors`, so the whole dataset is sent and later core or plugin associations can add required privileges. |
| `admin.dataset.unresolved` | Hint | The dataset id or selectors are dynamic, or the indexed schema cannot resolve them. The check does not report a missing privilege. |

`<read>` and `<crud>` both grant read access. `<read>*</read>` grants every
entity. The error offers a quick fix that inserts the missing `<read>`
elements into `manifest.xml`.

`data.get` without selectors is not warned about. Only subscriptions send the
dataset again whenever it changes.

Calls are recognized as `data.subscribe` / `data.get`, as an alias of the
`data` export (`import { data as sdkData } from '@shopware-ag/meteor-admin-sdk'`),
or as `SDK.data.subscribe` when the module namespace is imported from
`@shopware-ag/meteor-admin-sdk`, `@shopware-ag/admin-sdk`, or
`admin-extension-sdk`.

## Dataset to entity

A dataset id such as `sw-order-detail-base__order` is not itself an entity
name. Resolution uses the workspace, in this order:

1. **Indexed `publishData` path.** Administration sources are scanned for
   literal `publishData({ id, path })` calls. The path is a Vue property path.
   Its first segment is matched to an indexed entity (`salesChannel` matches
   `sales_channel`). Later segments are association fields, so
   `product.manufacturer` resolves to `product_manufacturer` rather than
   `product`.
2. **Id suffix.** When the workspace has no literal path, the text after `__`
   is matched the same way. `sw-product-detail__product` resolves to
   `product`. `sw-dashboard-detail__todayOrderData` does not match an entity
   and stays unresolved.

There is no hand-maintained catalog. The mapping follows the Shopware version
and plugins in the open workspace. Changing Shopware, or adding a plugin that
publishes a dataset, updates the index on the next scan. The index version
bumps when this extraction changes, so existing caches are rebuilt.

The check does not switch schemas from `shopware.targetVersion`. It uses the
entity definitions indexed from the workspace, which is the code the app is
being checked against.

## Selectors and the entity schema

Selectors are walked through DAL definitions indexed from `EntityDefinition`
classes (`internal/shopware/dal`). For each segment the check records:

- the entity that owns the field;
- the association target, when the field is an association.

`*`, `[0]`, and other numeric indexes step into the association's entity
instead of naming a field. A selector that stops on a plain value such as
`customFields.foo` does not invent further entities.

Examples for dataset `sw-order-detail-base__order` (`order`):

| Selector | Required read privileges |
|---|---|
| `orderNumber` | `read:order` |
| `language` | `read:order`, `read:language` |
| `language.name` | `read:order`, `read:language` |
| `deliveries.*.shippingMethod.name` | `read:order`, `read:order_delivery`, `read:shipping_method` |

`name` on the last row is a scalar, so `shipping_method` is included because
it owns that field. The dataset entity and each association passed through are
included as well: those are the entities reached by the selector.

Associations contributed only by `EntityExtension` classes are not part of the
DAL field index. A selector that depends on one of those fields is left
unresolved instead of reported as a missing privilege.

## What is skipped

- Dataset ids or selector lists that are not literals, including template
  strings and shorthand properties.
- Dataset ids that do not resolve to an indexed entity.
- Files that are not part of an app's Administration sources.
- `data.get` calls that do not pass selectors.

Turn a rule off in `.config/shopware/lsp.yaml` when a project intentionally
keeps one of these patterns:

```yaml
diagnostics:
  rules:
    admin.dataset.unresolved: off
```
