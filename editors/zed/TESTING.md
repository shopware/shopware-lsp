# Test plan

Four layers, because the interesting behaviour lives in four places: the
extension's logic, the task scripts, upstream's artifacts, and Zed's runtime.

## 1. Unit tests, `cargo test`

Runs on the **native** target. `zed_extension_api` compiles for the host even
though it only *runs* inside Zed, so anything that does not call a host import
is testable normally.

Host imports (`current_platform`, `download_file`, `HttpRequest::fetch`,
`LspSettings::for_worktree`) cannot be invoked without Zed. The logic around
them is therefore kept in free functions that take plain values, and the
`Extension` impl stays a thin wrapper. Keep new logic on the pure side of that
line and it stays testable.

Covered today:

| Function | Why it is worth a test |
|---|---|
| `target_for` | The 5 target strings are a wire contract with Open VSX. A typo is a 404 on someone else's machine, never ours. |
| `target_for` (errors) | Unsupported platforms must fail loudly and name the `binary.path` escape hatch, not silently pick a wrong build. |
| `binary_path_for` | Encodes the `extension/` prefix inside the vsix, plus the `.exe` suffix on Windows. |
| `parse_latest_release` | Payload shape, and that each failure says which field was missing. |
| `mcp_args` | Argument order. `mcp -root x` is rejected by the binary; only `-root x mcp` works. |
| `is_superseded_download` | Pruning deletes 31 MB directories. It must not match a binary someone dropped in the work dir. |

These were mutation-checked: breaking the linux-x64 mapping, reversing the MCP
argument order, and loosening the prune prefix each fail exactly one test.

### Coverage

```bash
cargo llvm-cov --summary-only
```

Around 71% of lines. The hooks are now "collect, plan, execute": each one
gathers what it needs from the host into a plain struct, hands it to a pure
`plan_*` function, and carries out the result. So the decisions are testable
without a wasm host, and what stays uncovered is the gathering and the host
calls themselves.

The number is not the goal. What matters is that no *decision* sits in the
uncovered half. Every bug found here so far was a decision hiding inside a
hook:

- the resolution order, duplicated per hook until it drifted and the MCP server
  ran a different binary from the editor, now `resolve_server`;
- the settings shape, forwarded as the editor's `shopwareLSP` wrapper that the
  server does not read, now `normalized_configuration`;
- the update hook building a smaller configuration than initialize, which the
  server treats as an instruction to unset the difference;
- the download directory, the MCP root, and the override rule, now
  `download_layout`, `mcp_root` and `command_override`.

One limitation to keep in mind: the hooks are call sites that must pass the
right facts to the right planner, and nothing tests that wiring. The tests pin
the planners and the differences that make a wrong call visible, but a hook
that hands over the wrong struct still compiles.

That is not hypothetical. The configuration hooks each built the object and
only initialize cached it, so after a settings change the language server had
the new configuration while a later MCP restart still used the old one. No unit
test could see it, and reintroducing it now still compiles and still passes.
The guard is structural: both hooks call `remember_configuration`, so there is
one place that builds and caches. Prefer collapsing call sites over adding a
test that cannot reach them.


## 1b. Script tests, `scripts/test-sw-action.py`

Offline, no server binary. Covers the pure text helpers in `sw-action.py` and
treats `examples/` as code, because it is what people copy:

* `uri_to_path` / `path_to_uri` round-trip, including `#` and `?` in a path.
  `urlparse` splits on both and would truncate the path, which is why the
  script does not use it.
* Both column conversions across an astral character, and the fact that they
  disagree. LSP positions are UTF-16; Zed's `$ZED_COLUMN` is a UTF-8 byte
  offset. An emoji earlier in the line shifts every insertion after it, and
  reading one encoding as the other lands two positions short. An offset
  inside a multi-byte sequence snaps forward rather than splitting it.
* Every `keymap.json` `task_name` resolves to a label in `tasks.json`. Zed
  silently does nothing on a mismatch, with no error in any log.
* Every task passing `$ZED_FILE` declares a `save` strategy, and every task
  names an action the script actually has.
* `inventory/parity.json` is self-consistent: every entry either names an
  action the script has or explains the gap, every gap appears in the table in
  `docs/vscode-parity.md`, and that doc's counts are the ones the map implies. That last check
  is the one that would have caught "23 palette commands, 13 equivalents",
  where the numbers came from two different lists.
* The picker resolves a selection by position, under both fzf and the
  numbered fallback. Labels are built from server data and repeat, so
  `options.index(chosen)` returned the first match and acted on an entry the
  user did not pick.
* `run_uuid` against real files in a temporary directory: first line, mid
  line, empty file, no trailing newline, row and column past the end, and the
  final blank line after a trailing newline. That last one is a real
  regression: `splitlines` drops the empty segment, so the row clamp walked
  the insertion back onto the previous line.

### Mutation testing, on demand

Not part of any run: `mutmut` takes about two minutes and its output needs
reading, not gating. It exists to answer "do these tests actually test
anything", which the hand-written break-it-and-restore check only answers for
the line you happened to think of.

```bash
python3 -m venv .venv && .venv/bin/pip install mutmut pytest
.venv/bin/mutmut run --max-children 8
.venv/bin/mutmut results | grep survived
.venv/bin/mutmut show <mutant-name>      # the diff that survived
```

Configured in `setup.cfg`. Two things about it are load-bearing:

* `also_copy` lists the test file and everything it reads. mutmut copies only
  the mutated source into `mutants/`, so without it every mutant is "killed"
  by a collection error and the run looks perfect.
* the test file imports `sw-action.py` under the module name
  `scripts.sw-action`, because that is how mutmut keys a mutant to its module.
  Rename it and the run aborts with "trampoline hits but none match any
  mutant key".

Last run: 3038 mutants, 77 survived, 2701 untouched by any test. That third
number is the server-dependent code, which has no offline coverage by design.

Reading the output is most of the work. Most survivors are noise: `"utf-8"`
becoming `"UTF-8"`, an fzf flag changing case, error-message wording. Real
findings from the first run were the BMP and astral boundaries in the column
helpers, the `max(line, 1)` clamp, the fzf ordinal bound, and the Windows and
URI-authority branches, which had no coverage at all. Five tests for the last
of those killed 30 mutants.

## 2. Contract checks, `scripts/contract-check.py`

Guards the seams we do not own. Unit tests cannot notice when Open VSX changes
a payload, the vsix moves the binary, or the server changes its CLI.

```bash
scripts/contract-check.py                      # uses shopware-lsp from PATH
scripts/contract-check.py --binary /path/to/shopware-lsp
scripts/contract-check.py --offline --binary /path/to/shopware-lsp
```

Checks:

- an MCP `tools/call` returns indexed project data, not just a tool list; the
  fixture ships a PHP class and the call has to find it
- all 5 published targets resolve and expose `version` + `files.download`
- the artifact is a zip containing `extension/shopware-lsp`
- `.config/shopware/lsp.yaml` works as a project marker (it needs `version: 1`)
- a bare invocation speaks stdio LSP
- `legend.tokenModifiers` is an array, not `null` — the
  [#59](https://github.com/shopware/shopware-lsp/pull/59) regression that stops
  Zed from initializing at all
- `mcp` rejects trailing global flags, so `mcp_args` ordering stays correct
- `-root ... mcp` speaks MCP and lists `shopware_*` tools

The normal CI job builds this checkout's server and uses `--offline` to check
LSP, MCP, and project detection without network access. From the repository
root, `mise run zed:contracts` runs the same checks and the inventory gate with
isolated caches. Open VSX payload and archive checks run separately on a
weekly schedule against the published server.

`--expect-fail NAME` tolerates a named check that is known to fail upstream,
while still failing the run on anything else. No allowance is currently needed;
reach for it only when upstream breaks something and you want the rest of the
suite to keep guarding.

## 3. Upstream surface drift, `scripts/inventory.py`

Also gates parity: every palette command (read from the VS Code manifest) and
every client command must have an entry in `inventory/parity.json`. A new one
fails as new surface until somebody records the action that covers it or why
none does. Local CI passes `--extension-dir ../vscode` to read the sibling
manifest. The argument also accepts an unzipped VSIX's `extension/` directory;
without it the published manifest is fetched from Open VSX.

Layers 1 and 2 guard what we already know about. Neither notices *new* upstream
surface, which is the other half of the problem: shopware-lsp gains commands,
scaffolds and tools continuously, and some of that is work for us.

```bash
scripts/inventory.py --check    # diff against inventory/snapshot.json
scripts/inventory.py --write    # accept the current surface as the baseline
```

`inventory/snapshot.json` is a golden file recording the protocol version,
capability keys, code-action kinds, server commands, client commands,
scaffold kinds, MCP tool names, feature flags and CLI commands.

Most new surface needs no work, by design:

| Category | Absorbed automatically because |
|---|---|
| scaffolds | `sw-action.py scaffold` reads the catalog live |
| MCP tools | the context server passes through whatever the server offers |
| client commands | our `supportedCommands` allow-list filters out unimplemented generators |

Two categories are not, and those are what the gate is for:

- a **new server command** may be a generator worth wiring into `sw-action.py`;
- a **removed or renamed** command breaks an action we already ship.

Exit codes: `0` unchanged, `1` breakage, `2` new surface only. CI fails on
either non-zero. Clear an addition by reviewing it, then `--write` and commit
the snapshot; that makes accepting new surface a deliberate, reviewable act.

The list of commands we depend on is **derived from `sw-action.py` itself**
rather than hand-kept, so the two cannot drift apart. It also checks the
client protocol version, since a bump there makes `initialize` fail outright.

The surface is project-independent, so CI runs this against a fixture holding
nothing but `.config/shopware/lsp.yaml`.

## 4. Manual checks in Zed

Nothing here is automatable: it needs a running Zed with a UI.

Run after touching `extension.toml`, the resolution order, or the context
server. Re-run `zed: install dev extension` first.

- [x] Open a PHP file in a Shopware project. `debug: open language server logs`
      shows `shopware-lsp` running, no initialize error.
- [x] Completion, hover, and go-to-definition respond. Test completion
      separately from the other two: hover and go-to-definition passed while
      every completion was being discarded client-side, so a working hover
      says nothing about completion. Cross-check against the server's own CLI,
      which takes the editor out of the loop:
      `shopware-lsp -root <project> completion <file>:<line>:<col>`, with the
      cursor inside the string of `Feature::isActive('')`. If that returns
      items and Zed shows none, the response is being rejected, not missed --
      `~/Library/Logs/Zed/Zed.log` names the field. Confirmed 2026-09-09, but
      only against a locally patched server: 0.3.57 emits
      `documentation:{"kind":""}` on every undocumented item, which is not a
      valid `MarkupKind`, so Zed drops the whole list. Fixed upstream in
      shopware/shopware-lsp#65.
- [x] Diagnostics appear (unused imports are a reliable source). Expect no
      squiggle: `inspections/php_imports.go` sends unused imports as severity
      Hint tagged Unnecessary, which Zed renders as faded text, so the import
      just looks dimmer than its neighbours. Hover names the rule
      (`shopware-php php.unusedImport`). Turn on `diagnostics.inline` if that
      is too subtle. Confirmed 2026-09-09.
- [x] `editor: toggle code actions` on a PHP file offers `Organize Imports`,
      and applying it removes the unused imports. Check the menu holds no
      duplicates while you are there; two entries means a second PHP server is
      enabled via the `"..."` wildcard. Confirmed 2026-09-09.
- [x] On an unused-import diagnostic, the `Remove unused import '...'` quickfix
      applies. This exercises the `codeAction/resolve` round trip, which only
      works while Zed preserves the diagnostic `data` field. Confirmed
      2026-09-09; the server's `data` carries a `validated-workspace-edit-v1`
      digest, so a silent no-op here shows up as a `codeAction/resolve` error
      in `~/Library/Logs/Zed/Zed.log` rather than a missing menu entry.
- [x] Open a `.twig` file with the Twig extension installed and confirm the
      server attaches to it. Check the status bar reads exactly `Twig`:
      `extension.toml` keys on Zed's language *name*, so a different name
      means the server silently never attaches and no log says why. Stronger
      than attachment is that it indexed: `{% sw_include '' %}` should offer
      bundle-namespaced template paths (`@Storefront/...`, `@Framework/...`)
      from the template index. Confirmed 2026-09-09. Hover on a block in a
      core template reports `No resolvable upstream block`, which is correct
      -- there is no upstream to resolve unless the file is an override.
- [x] With no `shopware-lsp` on `PATH` and no `binary.path`, the download runs
      and Zed shows the install status. Delete the extension work dir to retest.
      Confirmed 2026-09-09 on 0.3.57: the log's `Binary:` line named the work
      directory, so the order did fall through, and only the new version dir
      was left behind.
- [x] Agent Panel lists the `shopware-lsp` context server and a Shopware tool
      call returns real results. Confirmed 2026-09-09:
      `shopware_workspace_symbols` found `ProductEntity` at
      `src/Core/Content/Product/ProductEntity.php:44` with the right
      namespace, and `shopware_diagnostics` answered for the same file. Note
      that Zed's Agent Panel passes extension-contributed MCP servers to
      external agents too, so a Claude Code thread in the panel is a valid
      client for this check. Each thread spawns its own server, each with its
      own index and no sharing: eight of them on a shopware/shopware checkout
      measured 2.0 GB resident in total, several at 300-400 MB each.
- [x] The Agent Panel and the editor agree on the binary. `ps | grep shopware-lsp`
      should show both the language server and the `mcp` process on the same
      path. They diverged once, with the editor on `PATH` and the agent on the
      managed download, so the agent was answering from a different build.
      Confirmed 2026-09-09, but only after two failures worth reproducing:

      1. `lsp.shopware-lsp.binary.path` alone is not enough. The carry-over
         through `cached_binary_path` needs the language server to have
         started first, and it had not, so the agent stayed on the managed
         download while the editor ran the local build.
      2. `context_servers.shopware-lsp.command.path` **without** `args` is
         worse. Zed treats a present `command` as a full custom-server
         definition and never calls `context_server_command`, so `mcp_args`
         never runs and the binary is spawned bare -- a stdio LSP process that
         never answers an MCP `initialize`, failing with a 30s timeout and no
         useful error. It also makes the process indistinguishable from the
         language server in `ps`, which defeats this check. Pass
         `args: ["-root", "<project>", "mcp"]`, with `mcp` last.

      Distinguish the two lanes by parent process: the language server's
      parent is `zed`, an agent's MCP server is parented by the agent.
- [x] Snippets load: typing `sw-config-` in an XML buffer offers all six
      `sw-config-*` entries with their descriptions, and accepting one expands
      it. Confirmed 2026-09-07.
- [x] Choice placeholders work. `sw-config-element-text` uses the VS Code
      syntax `${1|text,textarea,password,url|}` and Zed offers all four as a
      pick-list, sorted alphabetically rather than in snippet order. Confirmed
      2026-09-07, so the snippets need no rewriting.
- [x] Multi-root workspace: with `context_servers.shopware-lsp.settings.root`
      set, the MCP server targets that root. Confirmed 2026-09-09, and read it
      from Zed rather than inferred: the `--mcp-config` Zed hands the agent
      (visible in `ps` on the agent process) carried
      `args: ["-root", "<project>", "mcp"]`.

      Tested with a *single*-root workspace opened on an unrelated Rust repo,
      which is stronger than the multi-root case for this mechanism: with no
      matching worktree, no open PHP file and so no language server, both other
      sources in `mcp_root` were empty, and the working-directory fallback
      would have made `shopware-lsp mcp` refuse to start outside a Shopware or
      Symfony project. So the negative control is built in. What that does not
      cover is whether a genuine multi-root workspace spawns one context server
      per project or per worktree.

      `command` must be absent for any of this to matter -- see the note under
      the Agent Panel binary check.
- [x] `Shopware: insert UUID` puts 32 hex characters at the cursor, not at the
      start of the line. Test on a line with an emoji before the cursor:
      `$ZED_COLUMN` is a UTF-8 byte offset, so a character-based or UTF-16
      conversion lands in the wrong place. Confirmed 2026-09-09 on `🎉ab`
      with the cursor between `a` and `b`: Zed passes `ZED_COLUMN=6`, one-based
      UTF-8 bytes exactly as documented, giving `🎉a<32 hex>b` with the
      emoji still four bytes.

      Measure the column before believing a mis-insertion. Clicking beside a
      wide emoji lands a character early easily, and column 5 -- the cursor
      before the `a` -- produces a wrong-looking but correct result that reads
      as an encoding bug. A throwaway task running a script that prints
      `$ZED_COLUMN` next to the insertion point each candidate reading implies
      settles it in one run.

      Read the reported path, not just the position. It should be relative
      (`.zed/scratch.txt`). A `~/...` path means the write went to a phantom
      tree: before the `expanduser` fix, the six
      `os.makedirs(os.path.dirname(path), exist_ok=True)` call sites in
      `sw-action.py` happily created `<worktree>/~/Users/...` and wrote there,
      so a task could report success having touched nothing you can see. Check
      that `<worktree>/~` does not exist after a run. Verifying from the buffer
      hides this entirely, because Zed shows the file it thinks is open.

      Zed also joins a task's `command` and `args` into a single string and
      runs it through `/bin/zsh -i -c`, so arguments are word-split and
      globbed: an inline `python3 -c` snippet dies on its parentheses, and a
      `$ZED_FILE` containing a space would break every task in
      `examples/tasks.json`.

## Fixtures worth knowing about

Two actions cannot be exercised against Shopware itself, because Shopware does
not use Symfony forms:

- `form-fields` and `twig-form-fields` need a throwaway project with
  `symfony/framework-bundle` and `symfony/form` installed, plus a `FormType`, a
  data class, a controller calling `createForm(...)->createView()`, and a
  template.
- **Real vendor code is required.** With hand-written stubs the variable
  resolves to `FormView` but never to a `FormType`, so `candidates` returns
  nothing and the feature looks broken when the fixture is at fault.

## Known gaps

- The `Extension` trait impl itself is only verified by compiling. There is no
  harness that drives the wasm component with a mock host.
- The download path is exercised end to end only by the manual check. The URL
  it builds and the archive layout it expects are covered by the contract
  check, but `download_file` itself is Zed's.
- `cached_worktree_root` depends on the language server starting before the
  Agent Panel. Ordering is not tested; the `root` setting exists for when that
  assumption does not hold.
