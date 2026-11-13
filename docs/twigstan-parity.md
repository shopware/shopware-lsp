# TwigStan parity

[TwigStan](https://github.com/twigstan/twigstan) compiles every template to
PHP, flattens the inheritance chain and runs PHPStan over the result. The LSP
keeps its own Twig tree and resolves types against the PHP semantic index, so
the checks work per document, in the editor, in `check` and through MCP,
without a Twig environment or a PHPStan run. The comparison was made against
TwigStan commit `49be00a`.

## What is covered

| TwigStan | LSP |
| --- | --- |
| Template context from controller `render()` calls, `#[Template]` and `Twig\Environment::render` | The PHP render context index (already present) |
| `{% types %}` and optional variables | Already present; `{# @var name type #}` as well |
| `getAttribute.notFound` | `twig.member.missing`, `twig.member.arguments`, `twig.member.not-callable` |
| `getAttribute.null`, with the `defined` and `empty` type specifiers | `twig.member.null` (opt-in), narrowed by `if`/`elseif`/`else`, ternaries, `and`/`or`, `is null`, `is empty`, `is defined`, `==`/`!=` null, `\|length`, and boolean variables set from such a condition (`{% set hasPrice = price.listPrice is not null %}`) |
| Variables set in a template | `{% set %}`, captures, `{% with %}`, loop variables, arrow function parameters |
| Filter return types (`first`, `last`, `keys`, `merge`, `filter`, `map`, `reduce`, `find`, `slice`, `sort`, `reverse`, `shuffle`, `column`, `batch`, `default`, `split`, `round`, `number_format`) | Typed from the input: `items\|first` is an element of `items` |
| Function return types (`constant`, `cycle`, `random`, `date`) | `cycle`, `random`, `range`, `max`, `min`, `attribute`; `constant` and `date` through the indexed extension |
| Include and embed context, `only` | The types of a literal `with` hash and of the including context reach the included template |
| Template inheritance (flattening) | A child's context and top-level assignments reach the parent; a parent's top-level assignments reach the child |
| Macro argument context | Macro parameters take the union of the argument types at their call sites |
| Variables that do not exist | `twig.variable.undefined` (opt-in) |
| `app.request` not null in controller templates | Yes |
| `app.user` not null behind `#[IsGranted]` and `access_control`, `appUserClass` | Yes: `#[IsGranted]` with `ROLE_*` or `IS_AUTHENTICATED*` on the action or its class, or `security.access_control` rules that require a user for every route of the action; the user class is the project's single implementation of `UserInterface` |
| Strict-rules findings on the compiled PHP (non-boolean conditions, loose comparisons, short ternaries) | Not reported, as in TwigStan, which filters them out |
| Form view variables (`form.vars.id`) | The same sixteen variables |
| `{# @twigstan-abstract #}` | `twig.template.abstract` for PHP render calls |
| `{% dump_type %}` | `twig.type.dumped`, shown as information where the tag stands |

Shopware specifics: the globals `TemplateDataExtension::getGlobals()` builds
in a local array (`context`, `shopware`, `activeRoute`, …) and the variables
subscribers add with `StorefrontRenderEvent::setParameter()` are known in
every template.

The null-safe accessor `?.` (Twig 3.23) is parsed and understood: it
tolerates a null receiver for the rest of its chain and makes the result
nullable.

## Deliberate differences

- **Opt-in strictness.** `twig.member.null` and `twig.variable.undefined` are
  disabled by default. Twig yields null for an attribute of null and for an
  undefined variable unless `strict_variables` is on, and Shopware never
  enables it. Enable them in `.config/shopware-lsp.yaml` for a project that
  runs strict.
- **`is defined` is a null guard.** Templates use
  `{% if product.manufacturer is defined %}` to guard an access; strictly it
  only says the attribute exists.
- **Undefined variables need a known context and no provider at all.** A
  variable is reported when no render call, including or extending template,
  assignment, declaration or global provides it, and at least one render call
  for the template family passes variables. TwigStan analyzes each render
  point separately; the LSP reports only what no render point provides.
  Nothing is reported for a template whose providers cannot all be seen: a
  render call that passes a parameter, a spread or a context built by code
  the analysis does not follow; a template some `include` may name at
  runtime (`'…/cms-element-' ~ element.type`); and the template `sw_icon`
  renders.
- **`access_control` is matched statically.** Route placeholders are filled
  with `1`, only the top-level `security` key is read (not `when@env`
  overrides), and a rule with a host, IP, method or expression condition is
  taken as not requiring a user.
- **Bounded cross-template search.** A template included or extended from
  more than eight places gets no type from its users, and a type is
  followed through at most two templates.
- **Other templates are read from disk.** Unsaved edits in another template
  do not change the types that flow from it until it is saved.
