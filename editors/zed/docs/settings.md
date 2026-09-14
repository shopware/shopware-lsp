# Settings

Every setting the extension reads, what it does, and the ones that
look like they should exist but do not.

**None are required.** Everything below is optional; `examples/settings.json`
has the same content ready to merge into your own settings.

Do not set `lsp.shopware-lsp.binary.path` unless you have a specific server you
want to pin. The extension finds one on its own, and a path that later stops
existing is a common way to end up with no language server at all.

```json
{
  "lsp": {
    "shopware-lsp": {
      "settings": {
        "shopwareLSP": { "activationMode": "auto", "memoryLimitMiB": 512 }
      }
    }
  }
}
```

`settings` is translated into the server's shape, not forwarded as written, and
`initialization_options` on `initialize`. Project-level configuration lives in
`.config/shopware/lsp.yaml` in the workspace root.

Validate `.config/shopware/lsp.yaml` against the server's own schema, the
equivalent of the VS Code extension's `yamlValidation`:

```json
{
  "lsp": {
    "yaml-language-server": {
      "settings": {
        "yaml": {
          "schemas": {
            "https://raw.githubusercontent.com/shopware/shopware-lsp/main/internal/projectconfig/schema.json": [
              ".config/shopware/lsp.yaml"
            ]
          }
        }
      }
    }
  }
}
```

Organize-imports on save, which is one of the few code actions that works in a
generic client:

```json
{
  "languages": {
    "PHP": {
      "formatter": [{ "code_action": "source.organizeImports" }],
      "format_on_save": "on"
    }
  }
}
```

`formatter` replaces rather than merges, so add your existing formatter as a
second array element if you have one.


## Every setting, in one place

None are required. `examples/settings.json` has these ready to merge.

| Setting | What it does |
|---|---|
| `lsp.shopware-lsp.settings.shopwareLSP.*` | The VS Code setting names. Translated into the shape the server reads, see below |
| `lsp.shopware-lsp.binary.path` | Pin the language server binary. Taken as given; the extension cannot verify it exists |
| `lsp.shopware-lsp.binary.arguments` | Extra arguments for the language server |
| `lsp.shopware-lsp.initialization_options` | Deep-merged over the defaults, so you can override one key. Use `shopwareClient.supportedCommands` to change code-lens filtering |
| `context_servers.shopware-lsp.command.path` | Pin the **MCP** binary. Optional: without it the extension downloads its own copy, same as for the language server. Set it only to force a specific build, and note it has to be set here as well as under `lsp`, since the MCP hook has no `PATH` and no `Worktree::which` to inherit one |
| `context_servers.shopware-lsp.settings.root` | Project root for the MCP server, for multi-root workspaces |
| `languages.PHP.language_servers` | Pick one PHP server. See below |
| `languages.PHP.formatter` | `{"code_action": "source.organizeImports"}` to organize imports on save |
| `lsp.yaml-language-server.settings.yaml.schemas` | Validate `.config/shopware/lsp.yaml` against the server's schema |

`shopwareLSP.*` uses the VS Code setting names, but the server has no such
configuration namespace, so they are translated rather than forwarded:

| Setting | Where it goes |
|---|---|
| `phpExtensions`, `disabledPhpExtensions`, `shopwareTargetVersion` | named `initializationOptions` fields |
| `features`, `domains`, `indexing.*`, `diagnostics.*`, `mcp.tools` | `initializationOptions.configuration`, the `lsp.yaml` shape |
| `memoryLimitMiB` | `GOMEMLIMIT` on both the language server and the MCP process |
| `activationMode` | **not supported.** VS Code decides whether to start the server at all; Zed's extension API cannot. The server refuses to start outside a Shopware or Symfony project anyway |
| `serverPath` | use `lsp.shopware-lsp.binary.path` |
| `phpExecutable` | **not supported.** Only used by VS Code's Symfony console code lenses, which Zed cannot run |
| `mcp.enabled` | drop the `context_servers.shopware-lsp` entry instead |

The same object goes to `initialize`, to `didChangeConfiguration` and to the
MCP process as `SHOPWARE_LSP_EDITOR_CONFIGURATION`. That matters because the
server *replaces* the editor overlay on an update rather than merging it, so a
smaller object sent later would silently unset whatever it omitted.

Anything the translation misses can still be set by hand through
`lsp.shopware-lsp.initialization_options`, which is merged last and wins.

The two binary settings are independent on purpose. `lsp.…binary.path` covers
the editor, `context_servers.…command.path` covers the Agent Panel, and
setting only one leaves the two answering from different builds.

Project-level server configuration lives in `.config/shopware/lsp.yaml` in the
workspace root, not in Zed settings, and needs `version: 1`.

## Environment variables

The server indexes `.env` files and offers hover on Symfony environment
variables, showing where each is declared and how often it is used. That works
in PHP, Twig and Dockerfiles once the relevant language is registered.

It does **not** work inside `.env` files themselves, though the server supports
it: hovering a variable there returns a real answer when asked over the CLI.
Zed has no dotenv language at all, so those files get no language id and no
server attaches. Fixing it means shipping a dotenv language from this
extension, grammar included, and taking on a language other extensions may
later provide. Not done.

Worth knowing that the server dispatches on file path rather than language id,
so the id an editor reports does not matter; only whether anything attaches
does.

---

[Back to the README](../README.md)
