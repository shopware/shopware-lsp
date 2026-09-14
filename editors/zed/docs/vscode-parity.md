# Compared with the VS Code extension

What the VS Code extension contributes, and what you get here instead.

The tables below are checked by `scripts/test-sw-action.py` against
`inventory/parity.json`: the counts, every gap, every partial and each
stated reason. Editing prose here without updating the map fails the
suite, which is the point -- the numbers came from two different lists
once, and said so for months.

| VS Code contribution | Here |
|---|---|
| Language server | yes |
| 18 `shopwareLSP.*` settings | translated; `activationMode` and `phpExecutable` cannot work in Zed |
| MCP server | yes, as a context server |
| Snippets (1 PHP, 6 config XML) | yes, ported and confirmed working |
| `yamlValidation` for `lsp.yaml` | via `examples/settings.json`, pointing at the server's own schema |
| 22 palette commands | no palette; 10 full equivalents as tasks. See [Command parity](#command-parity) |
| 23 client commands behind code actions | 12 full equivalents as tasks; the menu entries are filtered out |
| Explorer and editor context menus | no, Zed has no extension menu API |
| Code lenses | yes, text renders; clicking does nothing. See [Code lenses](#code-lenses) |
| Entity designer, Twig block diff viewer | no, needs custom UI |

## Command parity

Two lists, counted separately because they are easy to conflate. The
**palette** is what `contributes.commands` declares in the VS Code manifest.
The **client commands** are the ones the server asks a client to run, almost
all attached to code actions rather than to the palette.

10 of 22 palette commands and 12 of 23 client commands have a full task
equivalent. 3 actions serve both lists, so those are 19 distinct
actions, and with the partial and standalone ones below that is the
21 in the action table above. The examples expose them through 22 tasks;
tasks and upstream commands are not a one-to-one mapping.

Partial, and counted as a gap rather than as parity:

| Upstream | Action | What is missing |
|---|---|---|
| `configure` | `config` | configure is an interactive editor that writes feature toggles and picks a scope; config only prints the effective configuration |

Standalone, with no upstream counterpart:

| Action | What it is |
|---|---|
| `run` | passthrough to the resolved server binary; no upstream counterpart |

Not covered at all, and why:

| Missing | Reason |
|---|---|
| `analyzeTwigTemplateVariables`, `browseFormTypes`, `browseTwigComponents`, `browseTwigExtensions`, `twigVariables` | analytics browser, no equivalent yet |
| `createSnippetFromSelection`, `createAdminSnippetFromSelection` | worth adding; needs the selection plus a snippet-file picker |
| `insertSnippet`, `insertSnippetAtPosition` | worth adding; needs a picker over existing snippets |
| `runConsoleCommandPicker`, `runConsoleCommand` | a task runs bin/console directly; only the picker is missing |
| `extendComponent`, `overrideMethod` | picker-then-insert over Vue components, no equivalent yet |
| `browseDoctrineEntities` | no browser yet; useful for Symfony projects using Doctrine |
| `browseProfilerRequests` | no profiler browser yet |
| `restart` | Zed's own `language server: restart` covers it |
| `copySnippetUsage` | clipboard convenience not implemented; would need platform-specific integration |
| `createEventListener` | worth adding; the server command exists |
| `openReferences` | declared in supportedCommands so the lenses render; Zed cannot execute it |

The counts come from `inventory/parity.json`, which records a decision for
every upstream command and for every action. `scripts/test-sw-action.py`
fails when this section disagrees with it, and `scripts/inventory.py` fails
when upstream adds a command the map does not mention.

## Code lenses

Zed renders code lenses, and the extension declares `shopware.openReferences`
so they show. On a Store-API controller that is four:

```
Open Service Definition
Open 2 routing imports
POST|GET /store-api/product/{productId} · store-api.product.detail
Open route definition
```

The third is pure information and worth reading without ever clicking, which
is why the command is declared even though Zed cannot execute it. **Clicking a
lens does nothing useful**; the value is the text.

`supportedCommands` is matched per command name, so this leaves all ~20
generator code actions filtered. To hide the lenses instead:

```json
{
  "lsp": {
    "shopware-lsp": {
      "initialization_options": {
        "shopwareClient": { "supportedCommands": [] }
      }
    }
  }
}
```


Everything the language server itself provides — completion, hover,
definitions, references, diagnostics, quickfixes, organize-imports, semantic
tokens, inlay hints — is identical, because it is the same binary answering.
The gaps are all editor-surface, not intelligence.

---

[Back to the README](../README.md)
