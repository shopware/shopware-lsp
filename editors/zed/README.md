# Shopware LSP for Zed

Shopware 6 intelligence in [Zed](https://zed.dev): completion that knows your
feature flags and service ids, navigation that follows a Twig block to the
template it overrides, and diagnostics from a server that understands Shopware
rather than just PHP.

It runs [shopware-lsp](https://github.com/shopware/shopware-lsp), the same
language server behind Shopware's official VS Code extension. Zed cannot load
VS Code extensions and has no settings-only way to declare a language server,
so this small WASM extension is the route.

![Feature flag completion inside Feature::isActive](docs/images/completion-feature-flags.png)

## What you can do with it

- **Complete what actually exists in your project** — feature flags, service
  ids, route names, system config keys, snippet keys, entity and field names,
  Twig templates, blocks, filters and functions. Read from your code, not a
  bundled list.
- **Jump anywhere** — a service id in XML to the class behind it, a Twig
  template path to the file, a block name to where it is defined upstream.
- **See what a symbol is** without leaving the file, docblock included.
- **Fix problems as you type** — unused imports, unresolved services, wrong
  Twig block references, each with the rule name so you can silence it.
- **Organize imports and apply quick fixes** from the code action menu.
- **Run Shopware's generators** — scaffolds, entity schemas, Twig overrides,
  snippet creation. Ask the Agent Panel, or run them as Zed tasks.
- **Give the Agent Panel real tools** — 16 MCP tools over the same index, so
  an agent can look things up instead of guessing.
- **Run the same checks in CI** — the server has a headless `check` mode, so a
  job can fail on what the editor would have underlined.

Each of these, with screenshots and the caveats that matter, is in
**[docs/features.md](docs/features.md)**.

![Hover on the AndRule class](docs/images/hover-class-signature.png)

## Requirements

- **Zed** (tested on 1.18).
- **Rust via rustup**, with the `wasm32-wasip2` target. Zed compiles the
  extension itself, so the toolchain has to be present. If Rust came from
  Homebrew or Nix rather than rustup, add the target yourself. This is only
  needed because it installs as a dev extension; a published one ships
  prebuilt.
- **git**.
- **Python 3** for the optional generator tasks and task installer.
- Optional: **fzf**, which the task scripts use for nicer pickers. Without it
  they fall back to a numbered prompt.

No PHP, Go or Node needed. The language server is a prebuilt binary the
extension downloads.

## Install

Not in Zed's extension registry yet, so it installs from source. Once it is
published this collapses to searching **Shopware** on the Extensions page,
and steps 1 and 2 below go away along with the Rust requirement.

**1. Clone it somewhere permanent.**

Zed loads a dev extension *from the directory you point it at* and keeps
reading it from there, so this is not a throwaway checkout. Do not clone into
`/tmp`, and do not delete or move it afterwards.

```bash
git clone https://github.com/shopware/shopware-lsp.git \
  ~/Documents/Projects/shopware-lsp
cd ~/Documents/Projects/shopware-lsp/editors/zed
rustup show
```

**2. Install it into Zed.**

Command palette (`cmd-shift-p` / `ctrl-shift-p`) → `zed: install dev extension`
→ select **`editors/zed`**, the folder holding `extension.toml`. Zed
compiles it; the first build takes about half a minute.

**3. Install the Twig extension.**

From Zed's Extensions page, search `Twig` and install it. Without it, `.twig`
files get no language id and the server never attaches to them. It also brings
its own Twig language server, which reports separately from this one — see
[troubleshooting](docs/troubleshooting.md#twig-syntax-errors-on-core-templates-or-duplicate-twig-diagnostics)
if a Twig diagnostic looks wrong.

**4. Open a Shopware project.**

The server downloads on first use, with progress in the status bar. No manual
binary install, and no settings are required.

**5. Optional — install the generator tasks.**

```bash
./install-tasks.sh
```

One command, once, and the tasks work in every project you open. See
[docs/tasks.md](docs/tasks.md).

This step exists because Zed only lets an extension ship tasks for languages
it *defines*, and this one defines none — it attaches to PHP, Twig and the
rest, all owned by other extensions
([zed#64012](https://github.com/zed-industries/zed/issues/64012)). The
generators themselves need no setup: they are also MCP tools, so the Agent
Panel can run them with nothing installed. The tasks are the
keyboard-and-terminal path to the same thing.

## Verify it works

- `debug: open language server logs` lists `shopware-lsp` with no initialize
  error.
- Hover a Shopware class in a PHP file; you should get its signature.
- `editor: toggle code actions` on a PHP file with unused imports offers
  `Organize Imports`.

If nothing happens, check that the project root is a Shopware or Symfony
project. The server refuses to start otherwise, and says so in the log.
Anything else, start at [docs/troubleshooting.md](docs/troubleshooting.md).

## What is supported, and what is not

The server advertises these to Zed, and Zed uses all of them: completion,
hover, go-to-definition, go-to-implementation, references, document and
workspace symbols, code actions with resolve, code lenses, formatting, rename,
document links, folding, selection ranges, call hierarchy, type hierarchy,
semantic tokens, inlay hints, linked editing, document colors, signature help
and diagnostics.

Languages: PHP, Twig, XML, YAML, JSON, JavaScript, TypeScript, SCSS and Vue.

Not available, and why:

| | |
|---|---|
| **Generator code actions** | Upstream exposes scaffolding and Twig/snippet generators through VS Code-specific UI that Zed's code-action menu cannot host. Reachable as [tasks](docs/tasks.md) instead. |
| **`activationMode` and `phpExecutable` settings** | Meaningless in Zed. It starts the server on its own terms, and nothing here shells out to PHP. |
| **Multi-root in one process** | The server takes one workspace folder per process. Zed's extension API gives the MCP server no worktree paths, so a multi-root workspace needs [`settings.root`](docs/agent-panel.md). |
| **musl / Alpine** | Not detectable through the extension API. Point `binary.path` at an `alpine-*` build yourself. |
| **General PHP intelligence** | This is Shopware intelligence. For everything a normal PHP server does, see [running alongside another one](docs/internals.md#running-alongside-another-php-server). |

A full command-by-command comparison against the VS Code extension, including
every gap and the reason for it, is in
[docs/vscode-parity.md](docs/vscode-parity.md).

The extension and language server are maintained together in
[shopware/shopware-lsp](https://github.com/shopware/shopware-lsp). Include the
editor and server versions when reporting an issue.

## Using it in CI

The same binary runs headlessly, so the diagnostics you see while typing can
also gate a pull request — for a project or for a single plugin:

```bash
shopware-lsp -root . check -severity warning -fail-on warning src/
```

It prints `path:line:col: severity: message [rule]` and exits non-zero when
anything at or above `-fail-on` is reported.

Three things that are easy to get wrong:

- **`-fail-on` alone does nothing** if the severity floor excludes the
  diagnostics you care about. `-severity` filters first, so `-fail-on hint`
  reports nothing while the floor is `warning`. Set both.
- **`check` needs a path.** A bare `check` errors rather than scanning the
  project; pass `src/`, `custom/plugins/YourPlugin`, or `.`.
- **Service and parameter diagnostics need Symfony's dev debug container
  dump.** Without `var/cache/dev*/*DevDebugContainer.xml` every reference
  looks unresolvable, so a job that wants those has to warm the cache first.
  Everything else works on a bare checkout.

`update-server.sh` here installs the binary on macOS and Linux from Open VSX,
which is one way to get it onto a runner.

This is upstream's CLI rather than something this extension adds, so the full
command set, output formats and any behaviour you want changed live in
[shopware/shopware-lsp](https://github.com/shopware/shopware-lsp).

## Documentation

| | |
|---|---|
| [Features](docs/features.md) | Every feature, how to use it, where it stops |
| [Tasks](docs/tasks.md) | The generator actions, and `install-tasks.sh` |
| [Agent Panel](docs/agent-panel.md) | MCP tools, and pinning the binary correctly |
| [Settings](docs/settings.md) | Every setting, and the environment variables |
| [Troubleshooting](docs/troubleshooting.md) | Symptoms, causes, fixes |
| [VS Code parity](docs/vscode-parity.md) | Command-by-command comparison |
| [Internals](docs/internals.md) | Server resolution, repository layout, limitations |
| [AGENTS.md](AGENTS.md) | Architecture and conventions for contributors |
| [TESTING.md](TESTING.md) | The four-layer test plan |

## Updating and removing

This is not in Zed's extension registry yet, so it installs as a dev
extension and updates by hand:

```bash
git -C /path/to/shopware-lsp pull
```

Then re-run `zed: install dev extension` on `editors/zed`. Zed only
recompiles when you ask it to, so a pull on its own changes nothing.

Tasks are separate. They point at the script inside the checkout, so a pull
updates them in every project without help; re-run `./install-tasks.sh` only
when the task list itself changes.

To remove the extension, uninstall **Shopware LSP** from the Extensions page.
The downloaded server lives in the extension's work directory and goes with
it. The tasks do not: delete the `Shopware: ...` entries from
`~/.config/zed/tasks.json`, or the whole file if it holds nothing else.

Once it is published to the registry, most of this goes away — install and
update from the Extensions page, with Zed handling the build, and no rustup
requirement.

## License

MIT. See [LICENSE](LICENSE).

Ported from [BrocksiNet/zed-shopware-lsp](https://github.com/BrocksiNet/zed-shopware-lsp)
at commit `5d4e9f0a068ebea19ea102e2cf5b71391dec29e5`. The original author
credits and MIT license are preserved. Screenshots and historical manual
test observations were imported from that project; they are not new tests of
this port.

The snippets under `snippets/` are ported from
[shopware/shopware-lsp](https://github.com/shopware/shopware-lsp)
(`editors/vscode/snippets`), which is MIT licensed and copyright Shopware
Contributors. The language server itself is downloaded at runtime and is not
bundled here, so its license is upstream's.
