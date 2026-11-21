<p align="center">
  <img src="editors/vscode/icon.png" alt="Shopware Language Server" width="112" height="112">
</p>

<h1 align="center">Shopware Language Server</h1>

<p align="center">
  <strong>Your editor finally understands Shopware and Symfony.</strong><br>
  Completion, navigation, diagnostics and refactoring across PHP, Twig, XML, YAML, JavaScript and SCSS.
</p>

<p align="center">
  <a href="https://marketplace.visualstudio.com/items?itemName=shopware.shopware-lsp"><img src="https://img.shields.io/visual-studio-marketplace/v/shopware.shopware-lsp?label=VS%20Code%20Marketplace" alt="Visual Studio Marketplace"></a>
  <a href="https://open-vsx.org/extension/shopware/shopware-lsp"><img src="https://img.shields.io/open-vsx/v/shopware/shopware-lsp?label=Open%20VSX" alt="Open VSX"></a>
  <a href="https://github.com/shopware/shopware-lsp/releases"><img src="https://img.shields.io/github/v/release/shopware/shopware-lsp" alt="GitHub release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/shopware/shopware-lsp" alt="License"></a>
  <a href="https://app.codspeed.io/shopware/shopware-lsp?utm_source=badge"><img src="https://img.shields.io/endpoint?url=https://codspeed.io/badge.json" alt="CodSpeed"></a>
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#what-you-get">Features</a> ·
  <a href="#command-line-and-ci">CLI and CI</a> ·
  <a href="#mcp-and-ai-clients">AI clients</a> ·
  <a href="docs/reference.md">Reference</a>
</p>

---

A Shopware project is held together by strings: service IDs, route names,
template paths, snippet keys, config keys, entity names, component names.
Ordinary PHP tooling sees a string. Shopware Language Server sees what it
points to.

It indexes the whole application, across every language, and keeps that
picture current while you type. The same analysis then runs in your editor,
on the command line, in CI, and inside your AI coding agent.

- **One click from name to code.** Jump from a route name to its controller,
  from a snippet key to every translation, from a Twig block to the template
  it overrides.
- **Mistakes found while you type.** Missing services, unknown snippets,
  outdated Twig blocks, deprecated APIs and wrong arguments, each with a
  quick fix where one is safe.
- **Upgrades without surprises.** Version-aware checks for Shopware and
  Symfony report what will break before you run the code.
- **Fast and local.** Written in Rust, with its own parsers for every
  language. Nothing leaves your machine; the index lives in a local SQLite
  cache.
- **Open source.** MIT licensed.

> [!WARNING]
> Shopware Language Server is experimental. Features and editor integrations
> may change, and you may encounter bugs or incomplete results. This
> repository may also be ahead of the latest release: install a preview build
> to try what has not reached the stable channel yet.

## Install

| Editor | How |
|---|---|
| **VS Code** | Install the pre-release of [Shopware Language Server](https://marketplace.visualstudio.com/items?itemName=shopware.shopware-lsp) from the Marketplace, or from [Open VSX](https://open-vsx.org/extension/shopware/shopware-lsp) for VSCodium. The server is bundled. |
| **PhpStorm** | Add the Shopware plugin's EAP repository and update the plugin. [Setup steps](docs/installation.md#phpstorm) |
| **Zed** | Install `editors/zed` as a dev extension. [Setup steps](docs/installation.md#zed) |
| **Neovim and others** | Point any LSP client at the standalone binary. [Setup steps](docs/installation.md#other-editors-and-standalone-use) |

Open a Shopware or Symfony project and let the first index finish. No
configuration is needed.

## Try it in five minutes

1. Put the cursor on a route name in Twig `path()` or PHP `generateUrl()` and
   use **Go to Definition**.
2. Open a service configuration file and complete a service ID, tag, class,
   parameter or constructor argument.
3. Open a Twig template and complete a template path, snippet key, component,
   route, asset or the members of a typed variable.
4. Run **Symfony: Browse Routes...** or **Symfony: Locate Service...** from
   the command palette.
5. Run **Shopware: New File...** to preview a framework-aware scaffold.

## What you get

### Shopware

- **Snippets**: completion, hover, navigation and missing-key diagnostics for
  Storefront and Administration, with actions to create a key or extract a
  text into one.
- **Twig**: templates, blocks, routes, icons, theme configuration, system
  configuration and feature flags.
- **Twig block versioning** that follows inheritance across Shopware core,
  vendor packages, themes and your extensions, and tells you when an
  overridden block changed upstream.
- **Administration**: components, props, slots, events, methods, computed
  properties and extension blocks.
- **Data Abstraction Layer**: completion, navigation, type inference,
  diagnostics and migrations, plus a visual entity designer for definitions,
  mappings and extensions.
- **Extensions**: App Scripts, migrations and opt-in Store review checks for
  the metadata an extension ships.

### Symfony

- **Services**: IDs, aliases, parameters, tags, autowiring, decorators and
  factories across PHP, XML and YAML.
- **Routes**: controllers, parameters, imports and URLs across PHP, Twig,
  XML, YAML, JavaScript and TypeScript.
- **Doctrine DBAL**: tables and columns backed by the DAL definitions, join
  aliases and custom types.
- **The rest of the framework**: console commands, Messenger, events and
  listeners, validation, Serializer, assets, environment variables and bundle
  configuration.
- **Symfony UX**: Twig and Live Component props, actions, events, listeners
  and blocks.

### PHP and Twig

- **A full PHP engine**: symbols, inheritance, traits, PHPDoc generics and
  flow-sensitive type inference behind completion, hover, definitions,
  references, signature help, rename and diagnostics.
- **Refactoring**: Extract Variable, Extract Method, Organize Imports and
  unused-import fixes.
- **Typed Twig variables** from controllers, components, globals and
  annotations, with member completion and navigation back to PHP. Opt-in
  strict rules report access on null and undefined variables.
- **Formatting**: PHP to
  [PER Coding Style 2.0](https://www.php-fig.org/per/coding-style/), Twig with
  Shopware's block conventions, and JSON, JavaScript, TypeScript and SCSS.
  Whole files, selections, or a whole project with `shopware-lsp format`.

### Project tools

- **Browsers** for routes, services, commands, Twig extensions, components,
  template usages and profiler requests.
- **Scaffolds** for controllers, commands, Twig extensions, compiler passes,
  tests, service files and DAL models, each previewed before it is written.
- **Administration modules from an entity**: describe a module in three
  lines of YAML and get a list page, a detail page, roles and snippets that
  stay in step with the entity. See [the guide](docs/admin-modules.md).

### Supported files

| File type | Examples of what is understood |
|---|---|
| PHP | Types, completion, navigation, references, rename, diagnostics, code actions, formatting |
| Twig | Templates, blocks, routes, translations, components, assets, typed variables, formatting |
| XML and YAML | Services, routes, DBAL type registrations, configuration, translations, validation |
| JavaScript and TypeScript | Administration components, snippets, routes, assets, formatting |
| Vue | Administration component templates, scripts, styles, props, slots, events and blocks |
| SCSS | Theme variables, feature flags, classes, colors, formatting |
| JSON | Snippets, theme configuration, Composer and Shopware metadata, entity snapshots, formatting |
| Dotenv, Dockerfile and Compose | Environment declarations, definitions, references and hover |

The [feature reference](docs/reference.md) has the complete catalog, and
[every inspection](docs/inspections.md) is listed with its codes.

## Command line and CI

The `shopware-lsp` binary runs the editor's analysis without an editor.

```bash
# Check a project the way the editor does, and fail the build on findings.
shopware-lsp --root /path/to/project check src tests

# Format, as a diff by default.
shopware-lsp --root /path/to/project format src

# Ask where something is defined or used.
shopware-lsp --root /path/to/project lsp definition src/Controller.php:24:18
shopware-lsp --root /path/to/project lsp references src/Controller.php:24:18
```

In GitHub Actions, findings appear as annotations on the pull request:

```yaml
- run: shopware-lsp check src
```

Commands that change files preview a diff and write only when asked. Run
`shopware-lsp help` for every command, and see the
[CLI reference](docs/reference.md#command-line-interface) for what `check`
needs from a project (installed dependencies and a compiled container).

## MCP and AI clients

Give your AI coding agent the same knowledge of the project that you have in
the editor: diagnostics, definitions, references, symbol search, code actions
and scaffolds, over the Model Context Protocol.

VS Code and Zed register the server for you. Any other MCP client can start
it:

```json
{
  "mcpServers": {
    "shopware": {
      "command": "/absolute/path/to/shopware-lsp",
      "args": ["--root", "/absolute/path/to/project", "mcp"]
    }
  }
}
```

Read and preview tools never modify files. Applying a code action requires its
exact title, and every edit is validated against the workspace root.

## Configuration

Projects work without a configuration file. To share settings with your team,
commit `.config/shopware-lsp.yaml`:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/shopware/shopware-lsp/main/crates/projectconfig/schema.json
version: 1
shopware:
  targetVersion: "6.7"
indexing:
  exclude:
    - "**/generated/**"
diagnostics:
  overrides:
    - files: [src/Generated/**]
      enabled: false
```

Diagnostics can be tuned or switched off per file, directory, extension or
workspace. In VS Code, **Shopware: Configure Language Server...** opens a
searchable settings UI. See the
[configuration reference](docs/reference.md#project-configuration) and the
[JSON schema](crates/projectconfig/schema.json) for every option.

## Documentation

- [Installation](docs/installation.md)
- [Feature, command and configuration reference](docs/reference.md)
- [Inspection catalog](docs/inspections.md)
- [Symfony feature coverage](docs/symfony-plugin-roadmap.md)
- [Architecture and contributor documentation](docs/README.md)

## Contributing

The server is a Rust workspace under `crates/`; `rust-toolchain.toml` pins the
toolchain and rustup installs it on first use. SQLite is bundled, so a C
compiler is the only other requirement.

```bash
cargo build --locked --release -p swlsp-cli   # target/release/shopware-lsp
make check                                    # server, VS Code and Zed clients
```

`make check` needs Node.js 24 and Python 3 for the editor clients. Read
[AGENTS.md](AGENTS.md) for the rules of the codebase and
[docs/architecture.md](docs/architecture.md) for how it works.

## License

[MIT](LICENSE)
