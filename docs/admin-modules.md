# Generated Administration modules

A plugin describes the Administration modules it wants in one file. The
server derives a list page, a detail page, roles and snippets from the
entity, and keeps them in step with the description and the entity.

```yaml
# src/Resources/shopware-lsp/admin-modules.yaml
modules:
  acme-review:
    entity: acme_review
```

That is a complete description. The diagnostic `shopware.admin_module.outdated`
appears on the module name, and its quick fix `Shopware: Generate
Administration modules` writes the files as one workspace edit:

```text
src/Resources/app/administration/src/
  main.js                                   import of the module, added once
  module/acme-review/generated/
    index.js                                module, routes, navigation
    acl.js                                  viewer, editor, creator, deleter
    page/acme-review-list/                  list page
    page/acme-review-detail/                detail and create page
    snippet/en-GB.json, de-DE.json          every text of the module
```

Change the entity or the description and the diagnostic comes back; the
same fix updates the files. `shopware-lsp check` reports it too, so a build
can fail on a module that was not regenerated.

The entity must be defined by the plugin itself, as a class-based
`EntityDefinition`.

The code action `Shopware: Add Administration module <name> for <entity>` on the class
of an entity definition writes these lines for you. It creates the
description when the plugin has none, and adds the module to an existing
one with the indentation the file uses. The module is named after the
entity (`acme_review` gives `acme-review`), so the entity needs a prefix.
The action is not offered for an entity that has a module, for mapping and
translation definitions, or when the description cannot be read.
It needs a client that runs `shopware.addAdminModule` (VS Code does);
other clients call `shopware/admin/module/describe` (see
[custom commands](custom-commands.md)).

## What is derived

| Field of the entity | Input in the form | Column in the list |
|---|---|---|
| `StringField` | text field | yes |
| `StringField` with choices | select | yes |
| `LongTextField` | textarea | no |
| `LongTextField` with `AllowHtml` | text editor | no |
| `IntField`, `FloatField` | number field | yes, right-aligned |
| `BoolField` | switch | yes |
| `DateField`, `DateTimeField` | date picker | yes |
| `ManyToOneAssociationField` | entity select, written through its foreign key | no |
| `createdAt` | none | yes |
| identifiers, versions, JSON, blobs, to-many associations | none | no |
| write protected, runtime and computed fields | none | no |

- A required field is required in the form.
- A translated field gets the placeholder of the default language, and the
  pages get the language switch.
- The field named `name`, `title`, `label` or `technicalName` (else the
  first string field) names a record: it links to the detail page and is
  the title there.
- Labels are derived from the property name: `publishedAt` becomes
  `Published at`.
- The component set follows the oldest Shopware version the plugin
  supports: Meteor components (`mt-*`) from 6.7, the classic ones (`sw-*`)
  below.

## The format

Everything with a name is a key of a mapping, and every value is a mapping.
An absent key means "as derived", `enable: false` removes something, and
`position` orders (derived entries are 10 apart). An unknown key is an
error. That is what lets the format grow without breaking a description.

```yaml
version: 1

modules:
  acme-review:
    entity: acme_review
    title: { en-GB: Reviews, de-DE: Bewertungen }
    description: { en-GB: Product reviews }
    icon: regular-star
    color: "#ff68b4"
    labelField: title

    navigation:
      enable: true
      parent: sw-catalogue
      position: 50

    acl:
      enable: true
      parent: catalogues

    locales: { en-GB: {}, de-DE: {} }

    listing:
      auto: true
      limit: 25
      sorting: { field: createdAt, direction: DESC }
      columns:
        rating: { position: 5, inlineEdit: true, label: { de-DE: Sterne } }
        status: { sortable: false, align: center }
        createdAt: { enable: false }

    detail:
      auto: true
      cards:
        general:
          title: { en-GB: Basics }
          fields:
            rating:
              position: 1
              required: true
              helpText: { en-GB: One to five }
              component: { type: number, props: { min: 1, max: 5 } }
        content:
          position: 20
          fields:
            text:
              placeholder: { en-GB: Write something }
              component: { type: textarea }
            product:
              component: { type: entity-select, labelProperty: productNumber }
```

| Key | Meaning |
|---|---|
| `version` | The format version, `1`. The rules that derive a module belong to it: the same entity always gives the same module. |
| `modules.<name>` | The module name and the prefix of its components: lower-case words joined by dashes, with a vendor prefix. |
| `entity` | The technical name of the entity. |
| `title`, `description` | Texts per locale; derived from the entity name. |
| `labelField` | The property that names a record. |
| `navigation` | The menu entry: `parent` is the id of the entry above it (`sw-extension` when absent). |
| `acl` | The roles of the module; `parent` is the group of the permission grid, derived from the navigation parent. |
| `locales` | The locales snippet files are written for: `en-GB` and `de-DE` when absent. A locale without a text takes the one of `en-GB`. |
| `listing.auto`, `detail.auto` | `false` generates only what is named. |
| `listing.columns.<field>` | `enable`, `position`, `label`, `sortable`, `align` (`left`, `center`, `right`), `inlineEdit`. |
| `detail.cards.<name>` | `enable`, `position`, `title`, `fields`. Derived fields go into `general`; a field named in another card moves there. |
| `detail.cards.<name>.fields.<field>` | `enable`, `position`, `label`, `helpText`, `placeholder`, `required`, `component`. |

### Components

`component` picks the input by its `type`. Every type takes `props`:
attributes written onto the element as they are (`min: 1` becomes
`:min="1"`, `suffix: stars` becomes `suffix="stars"`).

| `type` | Further keys |
|---|---|
| `text`, `textarea`, `text-editor`, `switch`, `date`, `datetime` | none |
| `number` | `numberType`: `int` or `float` |
| `select` | `options.<value>`: `label`, `position`; the choices of the field when absent |
| `entity-select` | `entity`, `labelProperty` |
| `custom` | `name`: a component of the plugin; it gets the value, the label and the error of the field |

## Adjusting a generated module

From the lightest to the heaviest:

1. **The description.** Labels, order, hiding, another input, default
   sorting.
2. **`props`.** An option of a component the format does not name.
3. **A component of your own in a field.** `component: { type: custom,
   name: acme-rating }`.
4. **An override in a file of your own.** `Shopware.Component.override`
   with Twig blocks, outside `generated/`: regenerating never touches it.
   Every card and every field is a block
   (`acme_review_detail_card_general_field_title`), and the components
   keep these methods to replace one at a time:

   | Component | Methods |
   |---|---|
   | `<name>-list` | `getListCriteria()`, `getColumns()`, `getList()` |
   | `<name>-detail` | `getDetailCriteria()`, `loadEntity()`, `onSave()`, `onCancel()` |

   ```js
   // module/acme-review/index.js, imported from main.js
   Shopware.Component.override('acme-review-list', {
       methods: {
           getListCriteria() {
               const criteria = this.$super('getListCriteria');
               criteria.addAssociation('product');
               return criteria;
           },
       },
   });
   ```

5. **Taking a page over.** `listing: { managed: false }` or `detail: {
   managed: false }` moves the page to `module/<name>/page/` as it is on
   disk, edits included. It belongs to the plugin from then on and is
   never written again; the other page stays generated.

Component names, block names and the methods above belong to the format
version, like the keys of the description.

A generated file that was edited by hand counts as a difference: the fix
would discard the edit. Move the change into an override or take the page
over first.

## Removing a module

Remove it from the description. The fix then deletes its `generated/`
directory and its import; files of your own beside it are kept.

## Limits

- The entity must be a class-based definition of the same plugin.
- To-many associations, media, enum cases and custom fields get no input.
- Tabs on the detail page are not part of version 1.
- The generated pages are checked by the parsers of this server and against
  the pages Shopware itself ships. They are not run in a browser.
