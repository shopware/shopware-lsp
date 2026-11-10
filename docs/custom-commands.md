# Shopware LSP Custom Commands

This document lists the custom LSP commands and notifications provided by the Shopware Language Server. Each entry shows the method name, expected parameters and a short description of the action that is executed.

For the versioned editor contract, framework-only presentation profile,
client-side command catalog, scaffolding workflows, and a complete PhpStorm
adoption guide, see [`phpstorm-integration.md`](phpstorm-integration.md).

## Client integration

Smart host IDEs can send `initializationOptions.shopwareClient` with protocol
version `1`, presentation profile `framework`, and the exact editor-side
commands they implement. Legacy clients that omit this object retain the
existing `full` presentation.

The initialize result reports the negotiated state at
`capabilities.experimental.shopwareLSP`. Server-side commands are advertised
through standard `executeCommandProvider` and can be called with
`workspace/executeCommand`; direct custom requests remain supported.

The server also advertises standard `textDocument/formatting` support when the
`formatting` feature is enabled. Twig files are formatted from the current open
document snapshot; no custom request is required.

### `shopware/integration/catalog`

* **Parameters:** none
* **Action:** Returns the protocol version, every client-side command the
  server may emit, and the authoritative Shopware/Symfony scaffold catalog.
* **Returns:** `{ protocolVersion, clientCommands, scaffolds }`.

### `shopware/commands`

* **Parameters:** none
* **Action:** Lists registered server-side `shopware/...` commands.
* **Returns:** sorted command IDs.

## Commands

### `shopware/forceReindex`
* **Parameters:** none
* **Action:** Forces a rebuild of all indexes by invoking `indexAll` with `forceReindex` set to `true`.
* **Returns:** `{ "message": "Force reindexing started" }`

### `shopware/cache/list`
* **Parameters:** none
* **Action:** Lists the workspace caches of this machine without opening them.
* **Returns:** `{ directory, caches }`; each cache has `path`, `root` (the project it belongs to, `null` when the cache does not record one), `state` (`current`, `outdated`, `projectGone`, `unknown`), `inUse`, `bytes`, `files`, `idleSeconds` and `stale`.

### `shopware/cache/clear`
* **Parameters:** `{ scope?: "stale" | "all", dryRun?: boolean }`; the scope defaults to `stale`.
* **Action:** Deletes the caches of other projects: the stale ones (the project is gone, or the cache is outdated or unidentified and was not written for a week) or all of them. A cache a workspace has open, including the server's own, is never deleted; `shopware/forceReindex` rebuilds the server's own cache in place.
* **Returns:** `{ cleared, inUse, freedBytes, dryRun }`, with `cleared` and `inUse` holding cache entries as in `shopware/cache/list`.

### `shopware/extension/all`
* **Parameters:** none
* **Action:** Returns all detected Shopware extensions via the `ExtensionIndexer`.
* **Returns:** array of objects with `Name`, `Type` and `Path` fields.

### `shopware/snippet/storefront/getPossibleSnippetFiles`
* **Parameters:** `{ "fileUri": string }`
* **Action:** Searches the snippet directory for JSON files or creates a default `storefront.en-GB.json` if none exist.
* **Returns:** `{ "paths": [ { "path": string, "name": string, "value": string } ] }`

### `shopware/snippet/storefront/create`
* **Parameters:**
  ```json
  {
    "fileUri": string,
    "snippetKey": string,
    "snippets": [ { "path": string, "name": string, "value": string } ]
  }
  ```
* **Action:** Builds the edit that adds the provided snippet value to the given JSON files. The server writes nothing itself; the client must apply the returned edit.
* **Returns:** `{ "edit": WorkspaceEdit }`

### `shopware/snippet/storefront/all`
* **Parameters:** none
* **Action:** Collects all storefront snippet keys from the indexed snippet files.
* **Returns:** array of objects `{ key, text, file }` sorted alphabetically.

### `shopware/snippet/admin/getPossibleSnippetFiles`
* **Parameters:** `{ "fileUri": string }`
* **Action:** Searches the administration snippet directory for JSON files or creates a default structure if none exist.
* **Returns:** `{ "paths": [ { "path": string, "name": string, "value": string } ] }`

### `shopware/snippet/admin/create`
* **Parameters:**
  ```json
  {
    "fileUri": string,
    "snippetKey": string,
    "snippets": [ { "path": string, "name": string, "value": string } ]
  }
  ```
* **Action:** Builds the edit that adds the provided snippet value to the given admin JSON files. The server writes nothing itself; the client must apply the returned edit.
* **Returns:** `{ "edit": WorkspaceEdit }`

### `shopware/snippet/admin/all`
* **Parameters:** none
* **Action:** Collects all admin snippet keys from the indexed snippet files.
* **Returns:** array of objects `{ key, text, file }` sorted alphabetically.

### `shopware/twig/extendBlock`
* **Parameters:**
  ```json
  { "textUri": string, "blockName": string, "extension": string }
  ```
* **Action:** Builds the edit that creates or updates a Twig template in the selected extension so that it extends the given block, creating the file if necessary. The server writes nothing itself; the client must apply the returned edit.
* **Returns:** on success `{ "uri": string, "line": number, "edit": WorkspaceEdit }`, where `uri` and `line` locate the block once the edit has been applied; otherwise an error object with `code` and `message`.

## Notifications

### `$/progress` (indexing)
For clients that advertise `window.workDoneProgress`, a full workspace scan
is a standard work-done progress: `begin`, then `report` values with the
files looked at (`"message": "1,200/40,721 files"`, `percentage`) a few times
per second, `"message": "finishing"` after the last file, and `end`.

### `shopware/indexingStarted`
Sent when the server begins indexing. No parameters are required.

### `shopware/indexingCompleted`
Sent when indexing finishes. Parameters:
```json
{ "message": string, "timeInSeconds": number }
```

## Using with Neovim

The server is a single binary. Build it with:

```bash
cargo build --release -p swlsp-cli   # writes target/release/shopware-lsp
```

In Neovim, configure the language server using `lspconfig`:

```lua
require('lspconfig').shopware_lsp = {
  default_config = {
    cmd = { '/path/to/shopware-lsp' },
    filetypes = { 'php', 'twig', 'xml', 'yaml' },
    root_dir = vim.loop.cwd,
  },
}

require('lspconfig')['shopware_lsp'].setup{}
```

This registers the binary with Neovim’s built‑in LSP client and enables the custom commands above via `vim.lsp.buf.execute_command` or `client.request`.
