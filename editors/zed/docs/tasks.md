# Generator actions as tasks

> Zed only lets an extension ship tasks for languages it *defines*, and this
> extension defines none — it attaches to languages other extensions own. So
> these tasks need one install step rather than arriving with the extension.
> Tracked upstream as
> [zed#64012](https://github.com/zed-industries/zed/issues/64012).
>
> The generators are also MCP tools, so the Agent Panel reaches them with no
> setup at all. These tasks are the alternative for people not working through
> an agent.


The generator actions cannot work from Zed's code-action menu. These
tasks are how you reach them instead.

The generator code actions cannot work from Zed's code-action menu, but the
features behind them are not lost. Each is backed by ordinary server commands,
reachable over `workspace/executeCommand` and the CLI. `scripts/sw-action.py`
runs the picker in a terminal and writes the result, so a Zed task gives you
the same outcome:

```bash
./install-tasks.sh            # tasks into ~/.config/zed/tasks.json
./install-tasks.sh --keymap   # also merge the key bindings
./install-tasks.sh --print    # show what would change, write nothing
```

Reload the window, then `task: spawn` and type *sho*. It uses `fzf` when
installed and falls back to a numbered prompt.

The tasks install at user level, not per project, because none of them needs
anything project-specific: Zed sets `$ZED_WORKTREE_ROOT` per window and
`sw-action.py` defaults its root to that, so one definition serves every
project you open. They point at `scripts/sw-action.py` inside this checkout,
so `git pull` here updates every project at once.

That means **moving or deleting this checkout breaks all 22 tasks**, with the
same symptom as any other task misconfiguration: `task: spawn` runs and
nothing happens. The clone already has to stay put for the dev extension, so
this is not a new constraint, just a second thing depending on it.

Re-running updates in place. Entries labelled `Shopware: ...` are replaced;
anything else in your `tasks.json` is kept, and the previous file is backed up.

The trade-off is that all 22 appear in `task: spawn` for every project, Rust
and Node included. They filter out by typing *sho*, and outside a Shopware or
Symfony project they fail cleanly because the server refuses to start. To scope
them to one project instead, use the old layout -- copy `scripts/sw-action.py`
and `examples/tasks.json` into `<project>/.zed/`, where the tasks' original
`$ZED_WORKTREE_ROOT/.zed/sw-action.py` path applies. Note that `.zed/` is
gitignored in `shopware/shopware`, so such a copy goes stale invisibly.

Task labels are verb-first, so `task: spawn` narrows by intent: type *go* for
navigation, *insert* or *create* for the generators, *show* for the read-only
ones.

| Action | What it does | Status |
|---|---|---|
| `twig-extends` | Pick a parent template, insert `{% extends %}` | verified |
| `twig-blocks` | Pick parent blocks, insert overrides | verified |
| `form-fields` | Pick fields off the data class, rewrite the FormType | verified |
| `scaffold` | Any of the server's 24 scaffolds | verified, 23 kinds |
| `snippet` | Create a storefront translation in chosen snippet files | verified |
| `snippet-admin` | Same for Administration snippets | verified |
| `twig-extend-block` | Override a storefront block in an extension | verified |
| `admin-twig-override` | Override an admin block, and register it in `main.js` | verified |
| `twig-block-diff` | Show an override against its upstream block | read-only |
| `service-definition` | Render a service definition, arguments resolved from the index | verified |
| `compiler-pass` | Create a compiler pass and register it in the bundle | verified |
| `translation-extract` | Replace Twig text with a key, add it to every locale file | verified |
| `twig-form-fields` | Pick a form variable and its fields, insert `form_row` calls | verified |
| `routes` | Browse every Symfony route and open its controller | verified |
| `locate-service` | Find where a service id or class is defined | verified |
| `template-usages` | Find the templates that extend or include this one | verified |
| `run` | Passthrough to the resolved server binary | verified |
| `config` | Show the effective configuration and where it comes from | verified |
| `config-open` | Open the project configuration, creating a valid stub | verified |
| `reindex` | Rebuild the workspace index from scratch | verified |
| `uuid` | Insert a 32-character hex UUID at the cursor, as `Uuid::randomHex()` produces | verified |

`config-open` writes a stub containing `version: 1`, because the server
rejects the file outright without it, and points `$schema` at the server's own
schema so `yaml-language-server` can validate as you type.

To restart the server after a change, use Zed's own
`editor: restart language server`. There is no task for it: the extension
cannot register editor commands, and restarting the process from outside would
leave Zed's client attached to a dead one.

The navigation actions open the chosen result in Zed through its CLI, which
accepts `path:line:column`. That CLI is only on `PATH` after running
`cli: install` from Zed's command palette, so the app bundle is checked as
well; with neither available the location is printed instead of opened.

Two upstream analytics commands are deliberately not wired up, because both
return nothing for Shopware: `doctrine/entities` is empty, since Shopware uses
the DAL rather than Doctrine ORM, and `forms/types` is empty for the same
reason `twig-form-fields` cannot be exercised here.

`scaffold` covers both families: the `symfony` kinds return a single file, the
`shopware` kinds a `WorkspaceEdit` that the script applies (including
multi-file output such as `scheduled-task`, which writes a task and its
handler). Use `--print` to preview.

`service-definition` prints to the terminal rather than editing, because its
output belongs in a services config file, not the PHP file it was generated
from. `--format` takes `yaml`, `xml`, `fluent` or `php-array`.

`translation-extract` needs the text, which the example task passes as
`$ZED_SELECTED_TEXT`; Zed only offers the task when something is selected. It
locates the text in the file rather than trusting `$ZED_COLUMN`, since the
column sits at whichever end of the selection the cursor is on.

Some kinds need an extra option, for example
`--option 'event=Shopware\Core\...\EntityWrittenEvent'` for
`event-listener`. Known keys: `author`, `category`, `color`, `description`,
`event`, `hook`, `icon`, `label`, `license`, `method`, `methodGroup`, `mode`,
`namespace`, `package`, `parameters`, `target`, `taskName`, `timestamp`,
`type`.

`twig-block-diff` only answers for an override that carries a version
comment; on a core template the server replies "No version comment found for
block", which the script surfaces as-is.

Two deliberate gaps. The `entity-definition` scaffold is a multi-step
bootstrap/preview/apply workflow, so the script points you at the
`shopware_entity_schema_*` MCP tools instead.

`twig-form-fields` targets **Symfony form rendering**,
`{{ form_row(form.name) }}`, which Shopware does not use anywhere: no
`form_row`, `form_widget` or `form_start` appears under `src/`. Administration
templates such as `sw-bulk-edit-customer.html.twig` are Vue components written
in Twig syntax, not Symfony forms, so an empty result there is correct.

It is verified against a real Symfony 7 application, and the fixture has to be
real: with hand-written stubs for `AbstractController` and `FormInterface` the
server resolves the variable to `FormView` but never links it to a `FormType`,
and `candidates` comes back empty. Install `symfony/framework-bundle` and
`symfony/form` for real and it resolves:

```json
{"forms": [{"variable": "form", "formType": "App\\Form\\ProductType",
            "fields": ["active", "name", "price", "stock"]}]}
```

Note that the `twig/templateVariables` analytics command still reports
`formTypes: None` for that variable; it does not expose the field, and
`candidates` resolves the link internally. Do not use it to diagnose this.

---

[Back to the README](../README.md)
