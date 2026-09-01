<p align="center">
  <img src="vscode-extension/icon.png" alt="Shopware Language Server" width="112" height="112">
</p>

# Shopware Language Server

Framework-aware code intelligence for Shopware and Symfony projects.

[![Visual Studio Marketplace](https://img.shields.io/visual-studio-marketplace/v/shopware.shopware-lsp?label=VS%20Code%20Marketplace)](https://marketplace.visualstudio.com/items?itemName=shopware.shopware-lsp)
[![Open VSX](https://img.shields.io/open-vsx/v/shopware/shopware-lsp?label=Open%20VSX)](https://open-vsx.org/extension/shopware/shopware-lsp)
[![GitHub release](https://img.shields.io/github/v/release/shopware/shopware-lsp)](https://github.com/shopware/shopware-lsp/releases)
[![License](https://img.shields.io/github/license/shopware/shopware-lsp)](LICENSE)

Shopware Language Server understands how PHP classes, Twig templates, routes,
services, configuration, translations, Administration components, and DAL
definitions connect. It brings completion, navigation, diagnostics,
refactoring, and project tooling to those connections instead of treating
every file as an isolated document.

Use it to find framework mistakes while editing, move through a large project
without searching for string identifiers by hand, and run the same analysis in
VS Code, CI, the command line, or an AI client.

> [!NOTE]
> This repository may be ahead of the latest tagged release. Install a preview
> build to evaluate features that have not reached the Marketplace or Open VSX
> stable channel yet.

## Why use it?

Most PHP tooling stops at classes, methods, and types. Shopware and Symfony
applications also depend on relationships expressed through service IDs,
route names, Twig strings, XML/YAML configuration, translation keys, entity
metadata, and JavaScript component registrations.

Shopware Language Server indexes that application graph and keeps it available
while you work.

| When you are... | Shopware Language Server helps by... |
|---|---|
| Following a service, route, template, snippet, or config key | Completing the identifier and navigating directly to its declaration |
| Editing PHP, Twig, XML, YAML, Vue, JavaScript, TypeScript, or SCSS | Connecting references across languages instead of analyzing one file at a time |
| Upgrading Shopware or Symfony | Reporting deprecated APIs, unsafe inheritance, changed Twig blocks, and version-specific behavior before runtime |
| Building a plugin or application | Generating validated scaffolds, service definitions, snippets, and DAL entity changes |
| Reviewing or running CI | Reusing the editor's diagnostics through the `shopware-lsp check` command |
| Working with an AI coding agent | Giving it the same definitions, references, diagnostics, code actions, and generators through MCP |

The server is open source, runs locally, and stores its workspace index in a
project-specific SQLite cache. Unsaved editor documents take precedence over
the on-disk index, so navigation and diagnostics follow what you are actually
editing.

## What you get

### Shopware intelligence

- Storefront and Administration snippet completion, hover, navigation,
  diagnostics, creation actions, and translation extraction.
- Twig template, block, route, icon, theme configuration, system configuration,
  and feature-flag intelligence.
- Twig block versioning that follows inheritance across Shopware core, vendor
  packages, themes, and custom extensions.
- Administration component completion for components, props, slots, events,
  methods, computed properties, and extension blocks.
- Shopware DAL completion, navigation, type inference, diagnostics, migrations,
  and a visual entity designer for definitions, mappings, and extensions.
- App Script, migration, Store metadata, and extension-aware project support.

### Symfony intelligence

- Service IDs, aliases, parameters, tags, autowiring, decorators, factories,
  and service configuration across PHP, XML, and YAML.
- Routes, controllers, route parameters, imports, URLs, and references across
  PHP, Twig, XML, YAML, JavaScript, and TypeScript.
- Doctrine ORM/ODM metadata, repositories, QueryBuilder, DQL, DBAL tables, and
  custom types.
- Console commands, Messenger messages and handlers, events and listeners,
  forms, validation, security, Serializer targets, Stimulus controllers,
  assets, environment variables, and bundle configuration.
- Symfony UX Twig and Live Component props, actions, events, listeners, blocks,
  templates, and cross-language references.

### PHP and Twig semantics

- Native, lossless PHP and Twig parsers designed for incomplete editor input.
- Native Twig/HTML document formatting with separate Shopware Administration
  and Storefront block-indentation behavior and editor-provided tab settings.
- Workspace-wide PHP symbols, inheritance, traits, types, PHPDoc generics,
  flow-sensitive inference, completion, hover, definitions, references,
  signature help, rename, and diagnostics.
- Typed Twig variables from controllers, components, globals, forms, and
  annotations, including Twig 3.29 documentation comments, member completion,
  hover documentation, and navigation back to PHP.
- Framework-aware diagnostics for missing symbols, incompatible arguments,
  invalid configuration, deprecated APIs, `@final` inheritance, and more.

### Project tools

- Searchable route, service, command, Doctrine entity, form type, Twig
  extension, component, template usage, and profiler request browsers.
- Safe scaffolds for controllers, commands, form types, Twig extensions,
  compiler passes, tests, service files, and Shopware DAL models.
- One production analysis path shared by the editor, CLI, MCP server, and
  refactoring engine.

The [full feature and configuration reference](docs/reference.md) contains the
complete catalog. The [Symfony capability map](docs/symfony-plugin-roadmap.md)
tracks detailed framework coverage.

## Install

### VS Code

Shopware Language Server requires VS Code 1.101 or newer.

1. Install **Shopware Language Server** from the
   [Visual Studio Marketplace](https://marketplace.visualstudio.com/items?itemName=shopware.shopware-lsp).
2. Open a Shopware or Symfony workspace folder.
3. Start editing. The platform-specific extension package includes the server
   executable and starts it automatically for supported projects.

VSCodium and other Open VSX clients can install the extension from
[Open VSX](https://open-vsx.org/extension/shopware/shopware-lsp).

The default activation mode is conservative. The extension starts only when it
recognizes Shopware Composer metadata, a Shopware app manifest, Symfony's
`config/bundles.php`, or an explicit `.config/shopware/lsp.yaml` file. It does
not index unrelated PHP projects.

### Preview builds

Successful `feat/next-gen` workflow runs produce platform-specific preview VSIX
packages for macOS, Linux, Alpine, and Windows.

1. Open the latest successful
   [VSIX Preview workflow](https://github.com/shopware/shopware-lsp/actions/workflows/vsix-preview.yml?query=branch%3Afeat%2Fnext-gen).
2. Download the artifact for your operating system and architecture.
3. Run **Extensions: Install from VSIX...** in VS Code.

Preview artifacts are tied to a commit and expire after 14 days. Use the
Marketplace or Open VSX release for the stable channel.

### Other editors and standalone use

Download a server archive from
[GitHub Releases](https://github.com/shopware/shopware-lsp/releases), place the
`shopware-lsp` executable on your `PATH`, and configure your editor to start it
over stdin/stdout. Running the binary without a subcommand starts the language
server.

See [LSP.md](LSP.md) for custom protocol commands and a Neovim example. Editor
integrators can use the versioned
[PhpStorm integration guide](docs/phpstorm-integration.md).

## Try it in five minutes

After the initial workspace index completes:

1. Put the cursor on a route name in Twig `path()` or PHP `generateUrl()` and
   use **Go to Definition**.
2. Open a service configuration file and complete a service ID, tag, class,
   parameter, constructor argument, or configured method.
3. Open a Twig template and complete a template path, snippet key, component,
   route, asset, form field, or typed variable member.
4. Run **Symfony: Browse Routes...** or **Symfony: Locate Service...** from the
   command palette.
5. Run **Shopware: New File...** to preview a framework-aware scaffold.

Diagnostics include quick fixes where a deterministic edit is available. They
can also be configured or suppressed for a file, directory, extension, or the
whole workspace.

## Supported files

| File type | Examples of framework support |
|---|---|
| PHP | Semantic types, completion, navigation, references, rename, diagnostics, code actions, code lenses |
| Twig and HTML | Formatting, templates, blocks, routes, translations, components, forms, assets, Stimulus, typed variables |
| XML and YAML | Services, routes, Doctrine mappings, configuration, translations, validation, security |
| JavaScript and TypeScript | Administration components, snippets, routes, assets, Stimulus |
| Vue | Shopware Administration component templates, scripts, styles, props, slots, events, and blocks |
| SCSS | Theme variables, feature flags, classes, colors, completion, navigation, diagnostics |
| JSON | Snippets, theme configuration, Composer and Shopware metadata, entity snapshots |
| Dotenv, Dockerfile, and Compose | Environment declarations, definitions, references, and hover |

## Configuration

No configuration file is required for normal projects. Use **Shopware:
Configure Language Server...** for a searchable settings UI, or commit
`.config/shopware/lsp.yaml` when the team should share the same policy.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/shopware/shopware-lsp/main/internal/projectconfig/schema.json
version: 1
shopware:
  targetVersion: "6.7"
indexing:
  # Files above this size are skipped before reading and parsing.
  maxFileSizeMiB: 8
  exclude:
    - "**/generated/**"
diagnostics:
  overrides:
    - files: [src/Generated/**]
      enabled: false
```

Root configuration controls features, domains, indexing, PHP extensions, the
Shopware target version, diagnostics, MCP tools, and CLI defaults. A plugin or
extension may contain its own `.config/shopware/lsp.yaml` for diagnostics below
that directory. VS Code user and workspace settings remain local overrides.

See the [project configuration reference](docs/reference.md#project-configuration)
and the bundled [JSON schema](internal/projectconfig/schema.json) for every
option.

Large workspaces can set `shopwareLSP.memoryLimitMiB` to trade indexing CPU for
a lower soft memory limit. The default `0` uses the server's balanced runtime
policy; `512` MiB is a practical opt-in starting point when memory pressure is
more important than indexing speed.

## Command line and CI

The standalone binary exposes the same workspace, indexes, diagnostics, and
refactoring engine used by the editor.

```bash
# Inspect project detection without creating a workspace cache.
shopware-lsp -root /path/to/project -json project-info

# Index once, then run configured diagnostics over source and tests.
shopware-lsp -root /path/to/project index
shopware-lsp -root /path/to/project check src tests

# Query framework-aware navigation from scripts or CI.
shopware-lsp -root /path/to/project definition src/Controller.php:24:18
shopware-lsp -root /path/to/project references src/Controller.php:24:18
shopware-lsp -root /path/to/project workspace-symbol customer.detail
```

CLI positions are one-based `file:line:column` values. `check` can fail on a
configured severity threshold, while refactoring commands preview a diff unless
write mode is explicitly requested. Run `shopware-lsp help` for the full
command catalog.

## MCP and AI clients

VS Code automatically exposes one Shopware MCP server per supported workspace
folder. AI clients receive the same diagnostics, code actions, hover,
definitions, references, symbol search, scaffolds, and DAL entity workflow as
the editor.

Other MCP clients can start the standalone server over stdin/stdout:

```json
{
  "mcpServers": {
    "shopware": {
      "command": "/absolute/path/to/shopware-lsp",
      "args": ["-root", "/absolute/path/to/project", "mcp"]
    }
  }
}
```

Read and preview tools do not modify files. Applying a code action requires an
exact action title, scaffolds preview their diff by default, and all workspace
edits are validated against the workspace root. MCP tools can be disabled in
VS Code settings or in committed project configuration.

## Documentation

- [Documentation index](docs/README.md) — start here for architecture
- [Full feature, command, configuration, and performance reference](docs/reference.md)
- [Symfony feature coverage](docs/symfony-plugin-roadmap.md)

Architecture, for contributors:

- [System architecture](docs/architecture.md)
- [Parser, CST, and query architecture](docs/parser-architecture.md)
- [Indexing and persistence](docs/indexing.md)
- [LSP server, documents, and providers](docs/lsp-server.md)
- [Diagnostics and quick-fix pipeline](docs/diagnostics-pipeline.md)
- [Refactoring engine](docs/refactoring-engine.md)
- [PHP semantic engine](docs/php-semantic-engine.md)
- [PhpStorm and editor integration](docs/phpstorm-integration.md)
- [Custom LSP commands and Neovim example](LSP.md)
- [Maintainability guide](docs/maintainability.md)

## Contributing

The repository pins Go, Node.js, golangci-lint, and VSIX tooling in
`mise.toml`.

```bash
mise install
mise run setup
mise run check
```

Read [AGENTS.md](AGENTS.md) for the architecture, contributor workflow, and
testing expectations.

## License

[MIT](LICENSE)
