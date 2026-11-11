# Twig markup rules

The `twig.markup` inspection reports mistakes in Storefront templates that
one template proves. It grew out of a scan of the 328 templates in
`src/Storefront/Resources/views`; the last column is what it finds there.
Administration templates (below `app/administration`) are Vue templates and
are not checked.

| Code | Reports | Left alone | Shopware Storefront |
| --- | --- | --- | --- |
| `twig.raw.attribute` | `raw` as the last filter of a value printed inside an attribute | `raw` in element content | 11 |
| `twig.sanitize.attribute` | `sw_sanitize` as the last filter inside an attribute: it cleans HTML and does not escape quotes | `sw_sanitize` in element content | 25 |
| `twig.raw.translation` | `trans` followed by `raw` in element content | `trans\|sw_sanitize`, `json_encode\|raw`, `raw` on a variable | 19 |
| `twig.html.target_blank` | `target="_blank"` on a link without `rel="noopener"` or `noreferrer` | a `rel` or `target` built with Twig | 6 |
| `twig.html.button_type` | `<button>` without `type` | a tag with Twig between its attributes, which may add one | 36 |
| `twig.html.img_alt` | `<img>` without `alt` | `alt=""`, a tag with Twig between its attributes | 1 |
| `twig.html.duplicate_attribute` | an attribute written twice on one tag | attributes in different branches of an `if` | 0 |

They are warnings and on by default; switch one off with
`diagnostics.rules.<code>`.

## From the scan, not added

- **`raw` on a plain variable** (47 in the Storefront). The template cannot
  tell whether the value was sanitized before it was passed in.
- **Unused snippet keys.** That is a finding in a snippet file about every
  template, PHP and JavaScript file of the project, not about one template.
- **Blocks the parent chain does not define, unknown snippets, routes,
  includes and icons.** Other inspections resolve these against the indexes
  (`twig.template`, `shopware.snippet`, `symfony.routing`, `shopware.theme`).
- **Style and complexity** (`== true`, `|length > 0`, nesting depth, file
  length), inline scripts and styles, heading order, untranslated text. They
  are conventions or need a baseline, not defects one line proves.
