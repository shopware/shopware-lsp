# Troubleshooting

Symptoms first. Most of these look like the extension is broken and are
not -- the cause is usually upstream, or a second PHP server, or a stale
index.

## Diagnostics that are plainly wrong, on code that is fine

A merged fix that has not been published yet. Open VSX is the extension's only
source, and it lags `main`. On 2026-09-09 the published 0.3.53 read
every PHPStan `@template T` as an empty Symfony `@Template`, so
`Collection.php` in Shopware core showed five "Template not found" errors that
did not exist. The fix had merged six hours earlier.

Check whether the branch is ahead of what you are running:

```bash
./build-server.sh          # prints both, builds main into ~/.local/bin
```

Then point Zed at it, in `settings.json`:

```jsonc
"lsp": { "shopware-lsp": { "binary": { "path": "/Users/you/.local/bin/shopware-lsp" } } },
"context_servers": { "shopware-lsp": { "command": "/Users/you/.local/bin/shopware-lsp" } }
```

Set both. With only the first, the Agent Panel keeps answering from the
downloaded build and quietly disagrees with the editor.

Releases land every few days, so treat this as temporary. Delete the `binary`
block afterwards: the extension re-checks Open VSX each session and prunes the
old download, so it upgrades on the next Zed start without help.

## "failed to spawn command ... No such file or directory (os error 2)"

The message names the server binary, but the missing path is often the
`current_dir` at the end of the same line. `spawn` reports ENOENT for a
missing working directory exactly as it does for a missing program, so the two
are indistinguishable from the text.

Check the binary first. If it runs, the working directory is the problem:

```bash
"$HOME/Library/Application Support/Zed/extensions/work/shopware-lsp"/*/extension/shopware-lsp version
```

A worktree whose directory has been deleted underneath Zed produces this. A
temporary project is the usual way in: open a file under `/tmp` or
`/var/folders`, the OS reclaims it, and Zed keeps trying to start a server
there. Such worktrees are not persisted, so restarting Zed clears them;
otherwise close that window or remove the folder from the project.

The extension cannot prevent it. Zed sets the working directory from the
worktree, and `zed::Command` has no field for it.

## Thousands of "Service ... not found" or "Parameter ... not found"

The server checks service and parameter references against Symfony's **dev
debug container dump**, `var/cache/dev*/Shopware_Core_KernelDevDebugContainer.xml`.
Without that file it has no service list, so every reference looks missing. On
a shopware/shopware checkout that was 2018 diagnostics, 70% of everything
reported.

Two separate things can hide it.

**The app runs in prod.** Shopware's `.env` ships `APP_ENV=prod`, and only a
dev-with-debug build writes that XML. Either switch your `.env` to
`APP_ENV=dev`, which is the normal development setup and keeps it current, or
warm dev explicitly:

```bash
bin/console cache:warmup --env=dev
```

Note that a plain `cache:warmup` warms **prod** and produces nothing useful
here.

**The app runs in a container and `var/` is not synced to the host.** The
extension runs the language server on your machine, so a cache written inside
the container is invisible to it. Copy the dump out:

```bash
D=$(docker compose exec -T web sh -c 'ls -d /var/www/html/var/cache/dev_*' | tr -d "\r")
mkdir -p "var/cache/$(basename "$D")"
docker compose exec -T web cat "$D/Shopware_Core_KernelDevDebugContainer.xml" \
  > "var/cache/$(basename "$D")/Shopware_Core_KernelDevDebugContainer.xml"
```

Roughly 3 MB. `var/` is gitignored, and the server's glob is `dev*`, so the
hashed directory name is fine. Repeat after changing service definitions.

## Code actions and completions appear twice

Two PHP language servers. See
[Running alongside another PHP server](internals.md#running-alongside-another-php-server).

## Nothing happens in `.twig` files

Install the **Twig** extension from Zed's registry. Without it those files have
no language id and the server is never attached. The **Dockerfile** extension
is optional and enables env-var hover in Dockerfiles.

## Twig syntax errors on core templates, or duplicate Twig diagnostics

Not this extension. Zed's **Twig** extension ships its own language server,
`twiggy-language-server`, so `.twig` files have two servers attached and you
see the union of what both report.

The tell is the source suffix. Diagnostics from here are tagged, for example
`Route 'x' not found (symfony symfony.route.missing)`. Twiggy's carry no
source, so a bare `Unexpected syntax` is not ours.

Twiggy's Twig grammar lags the language. `===`, valid since Twig 3 as the
alias for `same as`, is not in its parser, so it reports `Unexpected syntax`
on templates that render fine — including core ones such as
`storefront/page/product-detail/meta.html.twig`.

Confirm which server produced a diagnostic by asking this one directly. It
takes the editor out of the loop:

```bash
shopware-lsp -root . check -severity hint path/to/template.html.twig
```

Anything the CLI does not report came from the other server.

To silence twiggy while keeping the Twig extension — which you still need,
since it provides the language and grammar without which nothing attaches to
`.twig` at all:

```json
{
  "languages": {
    "Twig": {
      "language_servers": ["shopware-lsp", "!twiggy-language-server"]
    }
  }
}
```

That trades twiggy's general Twig intelligence for silence. In a Shopware
project this extension already covers templates, blocks, filters and
functions, so the loss is small; outside one, twiggy is the only Twig server
you have.

## Snippets or new behaviour missing after a `git pull`

Zed compiles a dev extension only when you install it. Re-run
`zed: install dev extension` on the folder.

## `failed to spawn command ... No such file or directory`

A stale `lsp.shopware-lsp.binary.path`. Recent versions skip a configured path
that does not exist, but older ones spawn it anyway. Remove the setting and let
the extension resolve the server itself.

## PHP 8.3+ syntax reported as unsupported

```
Typed class constants require PHP 8.3; the project is configured for PHP 8.2 [php.version]
```

The version comes from `composer.json`: `config.platform.php` wins, then the
floor of the `require.php` constraint, then 8.2. shopware/shopware declares
`~8.2.0 || ~8.3.0 || ~8.4.0 || ~8.5.0`, so the floor is 8.2 regardless of the
PHP your container runs. That is correct for core, which must support 8.2.
Silence it per project in `.config/shopware/lsp.yaml`:

```yaml
version: 1
diagnostics:
  rules:
    php.version: off
```

## Auditing a whole project

`check` takes a directory, which is a quick way to find systematic problems:

```bash
shopware-lsp -root . -json check src/ > /tmp/check.json
```

Aggregate by the `code` field rather than reading it. A rule firing in the
hundreds against known-good code is a false positive worth reporting upstream,
which is how the `@template` bug in
[shopware/shopware-lsp#62](https://github.com/shopware/shopware-lsp/pull/62)
was found.

---

[Back to the README](../README.md)
