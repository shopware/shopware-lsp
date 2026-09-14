# Shopware Language Server for VS Code

Supercharge your Shopware development with intelligent code completion, instant navigation, real-time validation, and more — right inside VS Code.

## Highlights

- **Smart Completions** — Context-aware suggestions for services, routes, snippets, config keys, Twig templates, admin components, and more
- **Click-to-Navigate** — Jump to any definition: service classes, route controllers, snippet files, template paths, component sources
- **Inline Diagnostics** — Catch missing snippets, invalid icons, outdated block overrides, and broken component references before runtime
- **8 File Types** — Full support across PHP, Twig, XML, YAML, JSON, JavaScript, TypeScript, and SCSS

## Features

### Project activation

The extension may activate when a supported file type opens, but in the
default `shopwareLSP.activationMode: "auto"` it starts no language-server or
MCP process until the workspace root is recognized as Shopware or Symfony.
Detection uses only root metadata such as `composer.json`, `composer.lock`, a
Shopware app `manifest.xml`, `config/bundles.php`, or the explicit
`.config/shopware/lsp.yaml` opt-in. Unrelated PHP projects therefore do
not get indexed and produce no inactive-project notification.

Use `"always"` for an unusual project layout that should deliberately run the
server, or `"never"` to disable both LSP and MCP for a workspace folder. Marker
changes are re-evaluated automatically, so adding a framework dependency or a
Shopware LSP configuration starts the server without reloading VS Code.

### Multi-root workspaces

Each supported VS Code workspace folder gets an isolated language-server
process, index, configuration, output channel, and MCP definition. Files and
file-based commands are routed to the process that owns their path. Commands
without a file use the active editor, then the only running server; when more
than one server remains possible, the extension asks which workspace to use.
Restart and force-reindex operations affect only the selected workspace.

Overlapping workspace folders do not start duplicate indexers. When both a
parent folder and one of its descendants are supported, the outermost folder
owns the complete tree and the descendant is suppressed. If the parent is
disabled or unsupported, the supported descendant starts normally. The same
selection rule applies to MCP definitions.

### Symfony Services

Autocomplete service IDs, tags, parameters, and class names in XML and YAML service definitions. Navigate to any service definition with a single click. PHP files show a code lens with service usage counts.

### Twig Templates

Complete template paths in `extends`, `include`, `sw_extends`, and `sw_include` tags. Autocomplete Twig filters and functions. Navigate to any template file from Twig or PHP (`renderStorefront`). See block usage at a glance with code lens indicators.

### Twig Block Versioning

Keep template overrides in sync across Storefront, themes, vendor packages, and
custom extensions. The LSP follows the complete `sw_extends` chain, detects
changed, deprecated, and removed upstream blocks, and shows their source,
package version, and status on hover. Quick actions add or update portable
version comments, show historical Shopware core diffs, or scaffold a new block
override in your extension. Missing version comments are intentionally not
reported by default; enable `twig.versioning.comment_missing` in the project
configuration when your project enforces them.

### Icons

Get autocompletion for icon names and packs in `sw_icon` tags. Hover over an icon to see an inline SVG preview. Missing icons are flagged as errors instantly.

### Snippets (Translations)

Autocomplete translation keys everywhere they're used:

- **Storefront:** `{{ 'key'|trans }}` in Twig, `$this->trans('key')` in PHP
- **Administration:** `{{ $t('key') }}` in Twig, `this.$t('key')` in JS/TS

Hover over any key to see all available translations across locales. Missing keys are flagged as errors with a quick fix to create them — or select text and create a snippet directly from your selection.

### Routes

Autocomplete route names in PHP (`redirectToRoute`) and Twig (`url`, `path`, `seoUrl`). Jump to the controller method that handles any route. Find all references to see everywhere a route is used.

### Feature Flags

Autocomplete and navigate to feature flag definitions from PHP (`Feature::isActive()`), Twig (`feature()`), and SCSS.

### System Configuration

Autocomplete system config keys in PHP when calling `SystemConfigService` methods (`get`, `getInt`, `getString`, `getFloat`, `getBool`, `set`, `getDomain`) and in Twig with the `config()` function. Navigate to the config XML definition.

### Theme Configuration

Autocomplete theme config variables in SCSS (as `$variables`) and in Twig (`theme_config()` function). Navigate to the field definition in `theme.json`.

### Administration Components

Full intelligence for the Shopware Admin built with Vue.js:

- **Tag completion** — Type `<sw-` and get a list of all registered components
- **Prop completion** — See each prop's type, whether it's required, and its default value
- **Slot completion** — Autocomplete available slots in `<template #slot-name>`
- **Event completion** — Autocomplete emitted events with `@event`
- **Hover** — See the full component API (props, events, methods, computed properties, slots)
- **Diagnostics** — Warnings for missing required props, errors for invalid blocks or non-existent parent components
- **Quick fixes** — Add missing props with type-appropriate default values in one click

### AI and MCP

On VS Code 1.101 or newer, the extension automatically contributes a Shopware
MCP server for every effective supported workspace folder. VS Code starts the bundled
`shopware-lsp` executable lazily when an AI agent uses a Shopware tool, so no
manual `mcp.json` setup is required. Diagnostics, code actions, hover,
definitions, references, workspace-symbol search, Shopware/Symfony scaffolds,
and the typed DAL entity-schema workflow use the same production services as
the editor. Scaffolds preview their diff unless the caller explicitly requests
a write; entity changes retain the preview-revision and destructive-change
confirmation checks used by the visual designer.

Set `shopwareLSP.mcp.enabled` to `false` for a workspace folder to hide that
server from VS Code AI clients. A custom `shopwareLSP.serverPath` and the
`shopwareLSP.memoryLimitMiB` setting apply to both the editor server and the
MCP process. Use `shopwareLSP.mcp.tools` to disable exact MCP tool names only
for the current user/workspace, or commit `mcp.tools` in
`.config/shopware/lsp.yaml` so every MCP client uses the same policy.

### Diagnostic configuration

Use `Shopware: Open Configuration…` to create or open
`.config/shopware/lsp.yaml`. Extensions nested inside a Shopware
workspace can keep the same file in their own repository; its diagnostic rules
and path overrides apply only below that extension directory. Override paths
are relative to the configuration-owning directory and support `*`, `?`, `**`,
brace alternatives, and character classes.

The quick-fix menu for a Shopware diagnostic can suppress that rule or all
Shopware diagnostics for the current file, directory, extension, or workspace.
Suppressions may be committed to the root or extension configuration, or kept
locally in VS Code workspace/user settings. Changes apply without restarting
the language server, and VS Code's Shopware MCP process receives the same local
diagnostic settings.

### Diagnostics Overview

| What's checked | Severity | Where |
|---|---|---|
| Missing snippet keys | Error | Twig, JS/TS |
| Missing icons | Error | Twig |
| Missing required component props | Warning | Twig (admin) |
| Invalid block references | Error | Twig (admin) |
| Non-existent parent component | Error | JS/TS (admin) |
| Outdated block version | Warning | Twig |
| Missing block version comment (opt-in) | Warning | Twig |

## Resource usage

`shopwareLSP.memoryLimitMiB` applies a soft memory limit to the language-server
and MCP processes. Its default value of `0` uses the server's balanced GC
policy. For large Shopware workspaces, `512` is an opt-in low-memory starting
point when lower peak memory matters more than indexing CPU. Very low limits
can cause frequent garbage collection and substantially increase CPU use.
Restart the language server after changing this setting.
