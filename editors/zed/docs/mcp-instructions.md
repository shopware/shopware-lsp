# Shopware MCP tools

Exposes the Shopware Language Server's 16 MCP tools to the Agent Panel:
diagnostics, hover, definitions, references, workspace symbols, code actions,
scaffolding, and the DAL entity-schema workflow.

No separate install. The extension reuses the same `shopware-lsp` binary as the
language server and downloads it if needed.

The server refuses to start outside a Shopware or Symfony project. It normally
picks up the root from the language server automatically. Set `root` explicitly
for a multi-root workspace, or when the Agent Panel starts before you have
opened a PHP or Twig file:

```json
{
  "context_servers": {
    "shopware-lsp": {
      "settings": { "root": "/path/to/shopware" }
    }
  }
}
```
