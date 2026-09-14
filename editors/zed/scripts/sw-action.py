#!/usr/bin/env python3
"""Run shopware-lsp generator features that Zed cannot offer as code actions.

Some of the server's code actions carry a client-side `shopware.*` command
instead of an edit, because the flow is picker-then-insert: VS Code opens a
picker and inserts whatever the server returns. Zed's extension API can neither
register such a command nor filter the dead menu entry out.

The features themselves are still reachable. Each is backed by ordinary server
commands, so this script does the picker in a terminal and the writing on disk,
driven from a Zed task.

Usage:
    sw-action.py list                          every action with a one-liner
    sw-action.py twig-extends        <file> [row]
    sw-action.py twig-blocks         <file> [row]
    sw-action.py twig-form-fields    <file> [row]
    sw-action.py form-fields         <file>
    sw-action.py snippet             <file>        storefront translation
    sw-action.py snippet-admin       <file>        administration translation
    sw-action.py twig-extend-block   <file> [row]
    sw-action.py admin-twig-override <file> [row]
    sw-action.py twig-block-diff     <file> [row]  read-only
    sw-action.py scaffold            [directory]

`row` is 1-based and defaults to the top of the file; Zed passes $ZED_ROW. It
selects which Twig block the pickers offer first.

`--print` shows what would happen instead of writing. Prompts can be skipped
with --key, --value, --block, --extension, --name, --class and --option.
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
from urllib.parse import quote, unquote

# Generators shaped as: candidates -> pick -> generate -> text snippet.
#
# `pick` extracts choosable labels from the candidates response and `payload`
# turns the picked labels back into generate arguments, so each generator's
# quirks stay in one table entry.
SNIPPET_ACTIONS = {
    "twig-extends": {
        "label": "Add Twig extends",
        "candidates": "shopware/symfony/twig/extends/candidates",
        "generate": "shopware/symfony/twig/extends/generate",
        "prompt": "parent template",
        "multi": False,
        "pick": lambda data: data.get("templates") or [],
        "payload": lambda picked, data: {"template": picked[0]},
    },
    "twig-blocks": {
        "label": "Add parent Twig blocks",
        "candidates": "shopware/symfony/twig/blocks/candidates",
        "generate": "shopware/symfony/twig/blocks/generate",
        "prompt": "blocks to override",
        "multi": True,
        "pick": lambda data: data.get("blocks") or [],
        "payload": lambda picked, data: {"selectedBlocks": picked},
    },
    "form-fields": {
        "label": "Generate form fields from a data class",
        "candidates": "shopware/symfony/form/fields/candidates",
        "generate": "shopware/symfony/form/fields/generate",
        "prompt": "fields",
        "multi": True,
        # The server needs the FormType class in this document, and it will
        # not infer it. Resolved from the document outline.
        "needs_class": True,
        # `generate` answers with the whole rewritten FormType, not a snippet.
        "mode": "replace",
        # Candidates are objects; show the inferred type next to the name.
        "pick": lambda data: [
            "{}  ({})".format(
                field["name"], field.get("suggestedType") or field.get("phpType", "?")
            )
            for field in (data.get("fields") or [])
        ],
        "payload": lambda picked, data: {
            "selectedFields": [line.split("  (")[0] for line in picked],
        },
    },
}


def resolve_class(binary, root, path):
    """Fully qualified class name declared in a PHP file.

    Uses the server's own document outline rather than a regex. Namespaces come
    back as kind 3 with classes as kind 5 children.
    """
    result = subprocess.run(
        [binary, "-root", root, "-json", "symbols", path],
        capture_output=True,
        text=True,
    )
    try:
        outline = json.loads(result.stdout or "[]")
    except ValueError:
        return None

    found = []

    def walk(nodes, prefix=""):
        for node in nodes:
            name = node.get("name", "")
            if node.get("kind") == 3:  # namespace
                walk(node.get("children") or [], name)
            elif node.get("kind") == 5:  # class
                found.append(f"{prefix}\\{name}" if prefix else name)
            else:
                walk(node.get("children") or [], prefix)

    walk(outline)
    if not found:
        return None
    if len(found) == 1:
        return found[0]
    return choose(found, "class", False)[0]


# Actions with a bespoke flow rather than the candidates/generate shape.
OTHER_ACTIONS = {
    "twig-form-fields": "Generate Twig form rows",
    "scaffold": "Create any of the server's scaffolds",
    "snippet": "Create a storefront snippet",
    "snippet-admin": "Create an Administration snippet",
    "twig-extend-block": "Override a storefront block in an extension",
    "admin-twig-override": "Override an Administration Twig block",
    "twig-block-diff": "Show an override against its upstream block",
    "service-definition": "Render a Symfony service definition for a class",
    "compiler-pass": "Create a compiler pass and register it in a bundle",
    "translation-extract": "Extract selected Twig text into a translation",
    "run": "Run the resolved server binary with the given arguments",
    "routes": "Browse Symfony routes and open the controller",
    "locate-service": "Find where a service id or class is defined",
    "template-usages": "Find the templates that extend or include this one",
    "config": "Show the effective configuration and where it comes from",
    "config-open": "Open the project configuration, creating a valid stub",
    "reindex": "Rebuild the workspace index from scratch",
    "uuid": "Insert a Shopware-style UUID at the cursor",
}


def action_names():
    return sorted(SNIPPET_ACTIONS) + sorted(OTHER_ACTIONS)


def zed_managed_servers():
    """Servers the Zed extension downloaded for itself.

    Following the documented install leaves no binary on PATH at all, because
    the extension keeps its download inside its own work directory. Newest
    first, so a fresh download wins.
    """
    import glob

    roots = [
        "~/Library/Application Support/Zed/extensions/work/shopware-lsp",
        "~/.local/share/zed/extensions/work/shopware-lsp",
        "~/AppData/Local/Zed/extensions/work/shopware-lsp",
    ]
    found = []
    for root in roots:
        pattern = os.path.join(
            os.path.expanduser(root), "shopware-lsp-*", "extension", "shopware-lsp*"
        )
        found.extend(
            entry
            for entry in glob.glob(pattern)
            if os.path.basename(entry) in ("shopware-lsp", "shopware-lsp.exe")
        )
    return sorted(found, key=download_version, reverse=True)


def download_version(path):
    """Version of a managed download, as numbers, from its directory name.

    Sorting these as strings puts 0.3.9 above 0.3.57, and upstream is well
    into the 0.3.50s. Unparseable names sort last rather than raising.
    """
    directory = os.path.basename(os.path.dirname(os.path.dirname(path)))
    match = re.search(r"shopware-lsp-(\d+(?:\.\d+)*)", directory)
    return tuple(int(part) for part in match.group(1).split(".")) if match else (-1,)


def server_binary():
    candidates = [
        os.environ.get("SHOPWARE_LSP_BIN"),
        shutil.which("shopware-lsp"),
        shutil.which("shopware-lsp.exe"),
        os.path.expanduser("~/.local/bin/shopware-lsp"),
        *zed_managed_servers(),
    ]
    for candidate in candidates:
        if candidate and os.path.isfile(candidate):
            return candidate
    sys.exit(
        "shopware-lsp not found. Set SHOPWARE_LSP_BIN, or install a server with "
        "the extension's update-server.sh."
    )


def execute(binary, root, method, payload):
    result = subprocess.run(
        [binary, "-root", root, "execute", method, json.dumps(payload)],
        capture_output=True,
        text=True,
    )
    output = (result.stdout or "").strip()
    if result.returncode != 0 or not output:
        sys.exit(f"{method} failed: {(result.stderr or result.stdout).strip()[:400]}")
    try:
        return json.loads(output)
    except ValueError:
        sys.exit(f"{method} returned non-JSON: {output[:200]}")


def choose_indexes(options, prompt, multi=False):
    """Pick from `options` and return the positions chosen.

    Positions rather than strings, because labels are built from server data
    and are not unique: two service definitions can share one, and a template
    can appear twice in its own usages. Resolving a pick with
    `options.index(chosen)` then silently returns the first match, so the
    caller acts on an entry the user did not select.
    """
    if not options:
        sys.exit(f"no {prompt} available here")

    if shutil.which("fzf"):
        # A tab-separated ordinal carries the position through fzf. It echoes
        # the whole line back, while --with-nth keeps field 1 out of the list
        # and out of matching.
        tagged = [f"{index}\t{option}" for index, option in enumerate(options)]
        args = [
            "fzf",
            "--prompt",
            f"{prompt}> ",
            "--height",
            "40%",
            "--delimiter",
            "\t",
            "--with-nth",
            "2..",
        ]
        if multi:
            args.append("--multi")
        picked = subprocess.run(
            args, input="\n".join(tagged), capture_output=True, text=True
        ).stdout.splitlines()
        indexes = []
        for line in picked:
            ordinal = line.split("\t", 1)[0].strip()
            if ordinal.isdigit() and int(ordinal) < len(options):
                indexes.append(int(ordinal))
    else:
        for index, option in enumerate(options, 1):
            print(f"  {index:4}  {option}")
        raw = input(
            prompt + (" (numbers, comma separated): " if multi else " (number): ")
        )
        try:
            indexes = [int(part) - 1 for part in raw.replace(",", " ").split()]
        except ValueError:
            sys.exit("invalid selection")
        # Python would read a negative index as counting from the end, so 0
        # used to pick the last entry.
        if any(index < 0 or index >= len(options) for index in indexes):
            sys.exit("invalid selection")

    if not indexes:
        sys.exit("nothing selected")
    return indexes if multi else indexes[:1]


def choose(options, prompt, multi):
    """Pick with fzf when available, otherwise a numbered prompt.

    For callers that only need the text back. Anything that maps the pick to a
    structure must use `choose_indexes`, since labels can repeat.
    """
    return [options[index] for index in choose_indexes(options, prompt, multi)]


def checked_write_path(path, root, action="write"):
    """Resolve a path for writing, refusing two shapes that are always wrong.

    A `~` path component means a tilde was never expanded. That is not
    hypothetical: these paths arrive from WorkspaceEdit URIs the server echoes
    back from what we sent it, so a bad path we produced comes straight back as
    an instruction. Before this check, `makedirs` cheerfully built
    `<root>/~/Users/...` and wrote there, and the task reported success having
    touched nothing the user could see.

    Anything resolving outside the project root is refused too. Those URIs are
    the server's word for where a file belongs, and nothing it can legitimately
    ask for lives outside the worktree.
    """
    if "~" in path.replace("\\", "/").split("/"):
        sys.exit(f"refusing to {action} a path with an unexpanded '~': {path}")
    resolved = os.path.realpath(path)
    base = os.path.realpath(root)
    if resolved != base and os.path.commonpath([resolved, base]) != base:
        sys.exit(
            f"refusing to {action} outside the project root:\n"
            f"  path: {resolved}\n  root: {base}"
        )
    return resolved


def prepare_write(path, root, action="write"):
    """`checked_write_path`, plus the parent directories it is allowed to make."""
    target = checked_write_path(path, root, action)
    os.makedirs(os.path.dirname(target) or ".", exist_ok=True)
    return target


def relative(path, root):
    try:
        return os.path.relpath(path, root)
    except ValueError:
        return path


def insert_snippet(path, row, snippet, dry_run, root):
    with open(path, encoding="utf-8") as handle:
        lines = handle.read().splitlines(keepends=True)
    index = max(0, min(row - 1, len(lines)))
    if not snippet.endswith("\n"):
        snippet += "\n"

    verb = "would insert" if dry_run else "inserted"
    if not dry_run:
        lines.insert(index, snippet)
        with open(path, "w", encoding="utf-8") as handle:
            handle.write("".join(lines))
    print(f"{verb} at {relative(path, root)}:{index + 1}")
    print(snippet, end="")


def replace_file(path, content, dry_run, root):
    verb = "would rewrite" if dry_run else "rewrote"
    if not dry_run:
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(content)
    print(f"{verb} {relative(path, root)} ({len(content)} bytes)")
    if dry_run:
        print(content[:400] + ("..." if len(content) > 400 else ""))


def run_snippet_action(spec, args, binary):
    path = os.path.abspath(args.target)
    if not os.path.isfile(path):
        sys.exit(f"not a file: {path}")
    with open(path, encoding="utf-8") as handle:
        source = handle.read()

    request = {"fileUri": path_to_uri(path), "source": source, "version": 1}
    if spec.get("needs_class"):
        class_name = args.class_name or resolve_class(binary, args.root, path)
        if not class_name:
            sys.exit(f"could not find a class in {relative(path, args.root)}")
        request["className"] = class_name

    data = execute(binary, args.root, spec["candidates"], request)
    picked = choose(spec["pick"](data), spec["prompt"], spec["multi"])

    generate = dict(request)
    generate.update(spec["payload"](picked, data))
    content = execute(binary, args.root, spec["generate"], generate).get("content", "")
    if not content:
        sys.exit("the server returned nothing")

    if spec.get("mode") == "replace":
        replace_file(path, content, args.print_only, args.root)
    else:
        insert_snippet(path, args.row, content, args.print_only, args.root)


def run_twig_form_fields(args, binary):
    """Two-level picker: choose a form in the template, then its fields."""
    path = os.path.abspath(args.target)
    if not os.path.isfile(path):
        sys.exit(f"not a file: {path}")

    request = {"fileUri": path_to_uri(path)}
    data = execute(
        binary, args.root, "shopware/symfony/twig/form/fields/candidates", request
    )
    forms = data.get("forms") or []
    if not forms:
        sys.exit("no form variables found in this template")

    labels = [
        "{}  ({})".format(form["variable"], form.get("formType", "?")) for form in forms
    ]
    form = forms[choose_indexes(labels, "form variable")[0]]

    generate = dict(request)
    generate.update(
        {
            "variable": form["variable"],
            "formType": form.get("formType", ""),
            "selectedFields": choose(form.get("fields") or [], "fields", True),
        }
    )
    snippet = execute(
        binary, args.root, "shopware/symfony/twig/form/fields/generate", generate
    ).get("content", "")
    if not snippet:
        sys.exit("the server returned an empty snippet")

    insert_snippet(path, args.row, snippet, args.print_only, args.root)


def utf16_to_index(text, units):
    """Convert a UTF-16 code-unit offset into a Python string index."""
    if units <= 0:
        return 0
    consumed = 0
    for index, char in enumerate(text):
        if consumed >= units:
            return index
        consumed += 2 if ord(char) > 0xFFFF else 1
    return len(text)


def index_to_utf16(text, index):
    """Convert a Python string index into a UTF-16 code-unit offset."""
    return sum(2 if ord(char) > 0xFFFF else 1 for char in text[:index])


def byte_column_to_index(text, offset):
    """Convert a UTF-8 byte offset within a line into a string index.

    Zed's `$ZED_COLUMN` is a byte offset, not a UTF-16 one: its `Point.column`
    advances by `c.len_utf8()` (`crates/rope/src/rope.rs`, `TextSummary::from`).
    LSP positions in this script are UTF-16 and use `utf16_to_index` instead;
    the two disagree on every non-ASCII line, so do not swap them.

    An offset landing inside a multi-byte sequence snaps forward to the next
    character boundary, which cannot corrupt the text.
    """
    encoded = text.encode("utf-8")
    offset = max(0, min(offset, len(encoded)))
    while offset < len(encoded) and (encoded[offset] & 0xC0) == 0x80:
        offset += 1
    return len(encoded[:offset].decode("utf-8"))


def uri_to_path(uri):
    """Filesystem path for a `file://` URI.

    Deliberately not `urlparse`: it splits on `#` and `?`, so `a#b.twig` comes
    back as `a`. A file URI has no query or fragment, so everything after the
    authority is path and only percent-decoding is needed.

    A Windows URI carries the drive as `/C:/...`; the leading slash is dropped.
    """
    if not uri.startswith("file://"):
        return uri
    rest = uri[len("file://") :]
    # Skip an authority component, which is empty for local files.
    if not rest.startswith("/"):
        rest = "/" + rest.split("/", 1)[-1] if "/" in rest else "/"
    path = unquote(rest)
    if re.match(r"^/[A-Za-z]:", path):
        path = path[1:]
    return path


def path_to_uri(path):
    """`file://` URI for a filesystem path.

    Outgoing paths need encoding for the same reason incoming ones need
    decoding: a space or a `#` in a template name otherwise produces a URI the
    server reads as a different file.
    """
    path = os.path.abspath(path)
    if re.match(r"^[A-Za-z]:", path):
        path = "/" + path.replace("\\", "/")
    return "file://" + quote(path, safe="/")


def twig_blocks(path, row):
    """Block names declared in a Twig file, nearest to `row` first.

    Twig has no document symbols, so this reads the declarations directly.
    Ordering by proximity puts the block the cursor sits in at the top, which
    is what the VS Code code action would have acted on.
    """
    import re

    found = []
    with open(path, encoding="utf-8") as handle:
        for number, line in enumerate(handle, 1):
            for match in re.finditer(r"{%-?\s*block\s+([A-Za-z0-9_]+)", line):
                found.append((number, match.group(1)))
    if not found:
        sys.exit(f"no Twig blocks found in {os.path.basename(path)}")

    before = [entry for entry in found if entry[0] <= row]
    ordered = list(reversed(before)) + [entry for entry in found if entry[0] > row]
    return [f"{name}  (line {number})" for number, name in ordered]


def pick_block(args, path):
    if args.block:
        return args.block
    chosen = choose(twig_blocks(path, args.row), "block", False)[0]
    return chosen.split("  (line")[0]


def pick_extension(args, binary, root):
    if args.extension:
        return args.extension
    listed = execute(binary, root, "shopware/extension/all", {})
    if not isinstance(listed, list) or not listed:
        sys.exit("no Shopware extensions found in this workspace")
    # Type 0 is a plugin or bundle, 1 an app. Only the former has storefront
    # views, so show it and let the user judge.
    labels = [
        "{}  ({})".format(
            entry.get("Name", "?"), "app" if entry.get("Type") == 1 else "plugin"
        )
        for entry in listed
    ]
    return choose(labels, "extension", False)[0].split("  (")[0]


def run_snippet_create(args, binary, domain):
    """Create a translation snippet in one or more snippet files."""
    path = os.path.abspath(args.target)
    if not os.path.isfile(path):
        sys.exit(f"not a file: {path}")

    key = args.key or input("snippet key: ").strip()
    if not key:
        sys.exit("a snippet key is required")

    listed = execute(
        binary,
        args.root,
        f"shopware/snippet/{domain}/getPossibleSnippetFiles",
        {"fileUri": path_to_uri(path)},
    )
    paths = (listed or {}).get("paths") or []
    if not paths:
        sys.exit(f"no {domain} snippet files found or creatable for this file")

    labels = [
        "{}  ({})".format(entry.get("name", "?"), entry.get("path", "")) for entry in paths
    ]
    chosen = [paths[index] for index in choose_indexes(labels, "snippet files", True)]

    value = args.value if args.value is not None else input(f"value for {key}: ")

    # `create` writes nothing itself: it answers with a WorkspaceEdit for the
    # client to apply. LSP.md still documents this as returning null.
    result = execute(
        binary,
        args.root,
        f"shopware/snippet/{domain}/create",
        {
            "fileUri": path_to_uri(path),
            "snippetKey": key,
            "snippets": [
                {"path": entry["path"], "name": entry.get("name", ""), "value": value}
                for entry in chosen
            ],
        },
    )
    edit = (result or {}).get("edit")
    if not edit:
        sys.exit(f"the server returned no edit for {key!r}: {json.dumps(result)[:200]}")

    print(f"{key!r} = {value!r}")
    apply_workspace_edit(edit, args.print_only, args.root)


def run_twig_extend_block(args, binary):
    """Create a storefront block override in a chosen extension."""
    path = os.path.abspath(args.target)
    if not os.path.isfile(path):
        sys.exit(f"not a file: {path}")

    block = pick_block(args, path)
    extension = pick_extension(args, binary, args.root)

    # Returns {uri, line, edit}. LSP.md documents only {uri, line}, but the
    # edit is the part that actually changes anything, so it must be applied.
    result = execute(
        binary,
        args.root,
        "shopware/twig/extendBlock",
        {"textUri": path_to_uri(path), "blockName": block, "extension": extension},
    )
    if not isinstance(result, dict) or result.get("message"):
        sys.exit(f"server refused: {(result or {}).get('message', result)}")

    print(f"override for {block!r} in {extension}:")
    edit = result.get("edit")
    if edit:
        apply_workspace_edit(edit, args.print_only, args.root)
    target = uri_to_path(result.get("uri", ""))
    if target:
        print(f"  block at {relative(target, args.root)}:{result.get('line', 1)}")


def run_admin_twig_override(args, binary):
    """Administration Twig block override; the server answers with an edit."""
    path = os.path.abspath(args.target)
    if not os.path.isfile(path):
        sys.exit(f"not a file: {path}")

    block = pick_block(args, path)
    extension = pick_extension(args, binary, args.root)

    result = execute(
        binary,
        args.root,
        "shopware/admin/twig/override",
        {"textUri": path_to_uri(path), "blockName": block, "extension": extension},
    )
    if not isinstance(result, dict) or result.get("message"):
        sys.exit(f"server refused: {(result or {}).get('message', result)}")

    print(f"admin override for {block!r} in {extension}:")
    edit = result.get("edit")
    if edit:
        apply_workspace_edit(edit, args.print_only, args.root)
    component = result.get("component")
    if component:
        print(f"  component: {component}")


def run_twig_block_diff(args, binary):
    """Read-only: show how an override differs from its upstream block."""
    path = os.path.abspath(args.target)
    if not os.path.isfile(path):
        sys.exit(f"not a file: {path}")

    block = pick_block(args, path)
    result = execute(
        binary,
        args.root,
        "shopware/twig/getBlockDiff",
        {"textUri": path_to_uri(path), "blockName": block},
    )
    if isinstance(result, dict) and result.get("message"):
        sys.exit(f"server refused: {result['message']}")
    print(json.dumps(result, indent=2) if not isinstance(result, str) else result)


def apply_workspace_edit(edit, dry_run, root):
    """Apply an LSP WorkspaceEdit to disk.

    Handles `documentChanges` (text edits plus create/rename/delete) and the
    legacy `changes` map. Text edits are applied bottom-up so that earlier
    ranges stay valid while later ones are rewritten.
    """
    touched = []

    def offset(text, position):
        """Python string index for an LSP position.

        LSP columns count UTF-16 code units, Python indexes count code points.
        Anything outside the BMP, an emoji in a Twig label for instance, makes
        the two diverge and edits land mid-string.
        """
        lines = text.splitlines(keepends=True)
        line = min(position["line"], len(lines))
        base = sum(len(entry) for entry in lines[:line])
        if line >= len(lines):
            return base
        return base + utf16_to_index(lines[line], position["character"])

    def apply_text_edits(path, edits):
        existing = ""
        if os.path.isfile(path):
            with open(path, encoding="utf-8") as handle:
                existing = handle.read()
        ordered = sorted(
            edits,
            key=lambda entry: (
                entry["range"]["start"]["line"],
                entry["range"]["start"]["character"],
            ),
            reverse=True,
        )
        updated = existing
        for entry in ordered:
            start = offset(updated, entry["range"]["start"])
            end = offset(updated, entry["range"]["end"])
            updated = updated[:start] + entry.get("newText", "") + updated[end:]
        if not dry_run:
            path = prepare_write(path, root)
            with open(path, "w", encoding="utf-8") as handle:
                handle.write(updated)
        touched.append((path, f"{len(ordered)} edit(s), {len(updated) - len(existing):+d} bytes"))

    changes = edit.get("documentChanges")
    if changes:
        for change in changes:
            if "textDocument" in change:
                apply_text_edits(
                    uri_to_path(change["textDocument"]["uri"]), change.get("edits", [])
                )
                continue
            kind = change.get("kind")
            path = uri_to_path(change.get("uri", ""))
            if kind == "create":
                if not dry_run:
                    path = prepare_write(path, root, "create")
                    if not os.path.exists(path):
                        open(path, "w", encoding="utf-8").close()
                touched.append((path, "create"))
            elif kind == "delete":
                if not dry_run and os.path.exists(path):
                    os.remove(checked_write_path(path, root, "delete"))
                touched.append((path, "delete"))
            elif kind == "rename":
                target = uri_to_path(change.get("newUri", ""))
                if not dry_run and os.path.exists(path):
                    checked_write_path(path, root, "rename")
                    os.rename(path, prepare_write(target, root, "rename"))
                touched.append((target, "rename"))
    else:
        for uri, edits in (edit.get("changes") or {}).items():
            apply_text_edits(uri_to_path(uri), edits)

    verb = "would write" if dry_run else "wrote"
    for path, detail in touched:
        print(f"  {verb} {relative(path, root)}  ({detail})")
    if not touched:
        print("  the server returned an empty edit")
    return touched


def run_scaffold(args, binary):
    catalog = execute(binary, args.root, "shopware/integration/catalog", {})
    scaffolds = catalog.get("scaffolds") or []
    if not scaffolds:
        sys.exit("the server exposed no scaffolds")

    labels = [
        "{:9} {:24} {}".format(
            entry.get("family", "?"), entry.get("kind", ""), entry.get("label", "")
        )
        for entry in scaffolds
    ]
    entry = scaffolds[choose_indexes(labels, "scaffold")[0]]

    if entry.get("workflow") == "entity-schema":
        print(
            "The DAL entity scaffold is a multi-step workflow (bootstrap, "
            "preview, apply) rather than a single command. Drive it with the "
            "shopware_entity_schema_* MCP tools in the Agent Panel."
        )
        return

    placeholder = entry.get("namePlaceholder") or "Example"
    name = args.name or input(f"name [{placeholder}]: ").strip() or placeholder
    directory = os.path.abspath(args.target or args.root)
    request = {
        "kind": entry["kind"],
        "directoryUri": path_to_uri(directory),
        "name": name,
    }

    options = {}
    for pair in args.option:
        key, separator, value = pair.partition("=")
        if not separator:
            sys.exit(f"--option needs KEY=VALUE, got {pair!r}")
        options[key.strip()] = value
    if options:
        request["options"] = options

    print(f"{entry.get('kind')} '{name}':")

    # The symfony family returns one file; the shopware family a WorkspaceEdit.
    if entry.get("family") == "symfony":
        response = execute(
            binary, args.root, "shopware/symfony/scaffold/create", request
        )
        path = uri_to_path(response.get("fileUri", ""))
        content = response.get("content", "")
        if not path or not content:
            sys.exit("the scaffold returned nothing")
        if args.print_only:
            print(f"  would write {relative(path, args.root)}")
            print(content[:400] + ("..." if len(content) > 400 else ""))
            return
        path = prepare_write(path, args.root)
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(content)
        print(f"  wrote {relative(path, args.root)}")
        return

    response = execute(binary, args.root, "shopware/scaffold/create", request)
    edit = response.get("edit")
    if not edit:
        sys.exit("the scaffold returned no workspace edit")
    apply_workspace_edit(edit, args.print_only, args.root)
    primary = uri_to_path(response.get("primaryFileUri", ""))
    if primary:
        print(f"  primary file: {relative(primary, args.root)}")


SERVICE_FORMATS = ["yaml", "xml", "fluent", "php-array"]


def run_service_definition(args, binary):
    """Render a Symfony service definition for the class in this file.

    The result belongs in a services config file, not in the PHP file, so it
    is printed rather than inserted. Redirect it or copy it from the terminal.
    """
    path = os.path.abspath(args.target)
    if not os.path.isfile(path):
        sys.exit(f"not a file: {path}")

    class_name = args.class_name or resolve_class(binary, args.root, path)
    if not class_name:
        sys.exit(f"could not find a class in {relative(path, args.root)}")

    # `output` is a format, not a destination.
    fmt = args.format or choose(SERVICE_FORMATS, "output format", False)[0]
    if fmt not in SERVICE_FORMATS:
        sys.exit(f"unsupported format {fmt!r}; expected one of {SERVICE_FORMATS}")

    with open(path, encoding="utf-8") as handle:
        source = handle.read()

    result = execute(
        binary,
        args.root,
        "shopware/symfony/service/generate",
        {
            "fileUri": path_to_uri(path),
            "source": source,
            "version": 1,
            "className": class_name,
            "output": fmt,
            "classAsId": bool(args.class_as_id),
            "serviceId": args.service_id or "",
        },
    )
    content = (result or {}).get("content", "")
    if not content:
        sys.exit(f"the server returned no definition: {json.dumps(result)[:200]}")

    print(f"# {class_name} as {fmt}")
    print(content, end="" if content.endswith("\n") else "\n")


def run_compiler_pass(args, binary):
    """Create a compiler pass and register it in the chosen bundle."""
    extension = pick_extension(args, binary, args.root)
    listed = execute(binary, args.root, "shopware/extension/all", {})
    entry = next(
        (item for item in listed if item.get("Name") == extension), None
    )
    if not entry or not entry.get("Path"):
        sys.exit(f"no bundle class path known for extension {extension!r}")

    bundle_path = entry["Path"]
    bundle_class = resolve_class(binary, args.root, bundle_path)
    if not bundle_class:
        sys.exit(f"could not resolve the bundle class in {bundle_path}")

    name = args.name or input("compiler pass class name [CollectServicesPass]: ").strip()
    name = name or "CollectServicesPass"

    with open(bundle_path, encoding="utf-8") as handle:
        bundle_source = handle.read()

    result = execute(
        binary,
        args.root,
        "shopware/symfony/compilerPass/create",
        {
            "bundleUri": path_to_uri(bundle_path),
            "bundleClass": bundle_class,
            "className": name,
            "source": bundle_source,
            "version": 1,
        },
    )
    pass_path = uri_to_path((result or {}).get("fileUri", ""))
    pass_content = (result or {}).get("fileContent", "")
    bundle_content = (result or {}).get("bundleContent", "")
    if not pass_path or not pass_content:
        sys.exit(f"the server returned no compiler pass: {json.dumps(result)[:200]}")

    writes = [(pass_path, pass_content)]
    if bundle_content:
        writes.append((bundle_path, bundle_content))

    verb = "would write" if args.print_only else "wrote"
    for target, content in writes:
        if not args.print_only:
            target = prepare_write(target, args.root)
            with open(target, "w", encoding="utf-8") as handle:
                handle.write(content)
        print(f"  {verb} {relative(target, args.root)}  ({len(content)} bytes)")


def locate_range(path, text, row):
    """LSP range covering `text`, searching at or after `row`.

    Zed hands a task `$ZED_SELECTED_TEXT`, `$ZED_ROW` and `$ZED_COLUMN`, but
    the column sits at whichever end of the selection the cursor is on, so
    reconstructing the range from it is guesswork. Searching for the text is
    deterministic instead.

    Characters are UTF-16 code units per the LSP spec, so text outside the BMP
    would need conversion; Twig literals in practice are not.
    """
    with open(path, encoding="utf-8") as handle:
        lines = handle.read().splitlines()

    order = list(range(max(row - 1, 0), len(lines))) + list(range(0, max(row - 1, 0)))
    for index in order:
        column = lines[index].find(text)
        if column != -1:
            # The server expects UTF-16 columns, so convert rather than send
            # code-point indexes.
            start = index_to_utf16(lines[index], column)
            end = index_to_utf16(lines[index], column + len(text))
            return {
                "start": {"line": index, "character": start},
                "end": {"line": index, "character": end},
            }
    sys.exit(f"could not find {text!r} in {os.path.basename(path)}")


def run_translation_extract(args, binary):
    """Extract selected Twig text into a translation key."""
    path = os.path.abspath(args.target)
    if not os.path.isfile(path):
        sys.exit(f"not a file: {path}")

    text = args.text or os.environ.get("ZED_SELECTED_TEXT") or ""
    text = text.strip()
    if not text:
        sys.exit("nothing to extract; pass --text or select something first")

    with open(path, encoding="utf-8") as handle:
        source = handle.read()

    selection = locate_range(path, text, args.row)
    # No `version` here: this command validates the document version against
    # its own snapshot and rejects any value with "syntax element handle is
    # stale". Omitting it lets the server use the snapshot it already has.
    request = {
        "fileUri": path_to_uri(path),
        "source": source,
        "range": selection,
    }

    prepared = execute(
        binary, args.root, "shopware/symfony/translation/extract/prepare", request
    ) or {}
    domains = prepared.get("domains") or []
    default_domain = prepared.get("defaultDomain") or ""
    default_key = prepared.get("defaultKey") or ""

    domain = args.domain or (
        choose(domains, "domain", False)[0] if domains else default_domain
    )
    key = args.key or input(f"key [{default_key}]: ").strip() or default_key
    if not key:
        sys.exit("a translation key is required")

    generate = dict(request)
    generate.update({"key": key, "domain": domain})
    result = execute(
        binary, args.root, "shopware/symfony/translation/extract/generate", generate
    ) or {}

    print(f"{key!r} in domain {domain!r} replaces {text!r}")

    edit = result.get("edit")
    if not edit:
        # `edit` is optional. The response always carries the pieces though:
        # `range`/`replacement` for the template, and one target per
        # translation file with an insertion point and its text. Assemble the
        # same WorkspaceEdit from those rather than depending on the optional
        # field being populated.
        replacement = result.get("replacement")
        if replacement is None:
            sys.exit(f"the server returned nothing usable: {json.dumps(result)[:200]}")

        changes = [
            {
                "textDocument": {"uri": path_to_uri(path), "version": None},
                "edits": [
                    {"range": result.get("range", selection), "newText": replacement}
                ],
            }
        ]
        for target in result.get("targets") or []:
            uri = target.get("fileUri") or path_to_uri(target.get("file", ""))
            position = {
                "line": target.get("line", 0),
                "character": target.get("character", 0),
            }
            changes.append(
                {
                    "textDocument": {"uri": uri, "version": None},
                    "edits": [
                        {
                            "range": {"start": position, "end": position},
                            "newText": target.get("newText", ""),
                        }
                    ],
                }
            )
        edit = {"documentChanges": changes}

    apply_workspace_edit(edit, args.print_only, args.root)


def zed_cli():
    """The Zed CLI, which opens a file at `path:line:column`.

    `zed` is only on PATH if the user ran `cli: install`, so the app bundle is
    checked too. Returning None is not fatal; the caller prints the location
    instead of opening it.
    """
    found = shutil.which("zed")
    if found:
        return found
    for candidate in (
        "/Applications/Zed.app/Contents/MacOS/cli",
        os.path.expanduser("~/Applications/Zed.app/Contents/MacOS/cli"),
        "/usr/local/bin/zed",
        os.path.expanduser("~/.local/bin/zed"),
    ):
        if os.path.isfile(candidate):
            return candidate
    return None


def open_location(path, line, root, print_only):
    """Open a source location in Zed, or print it when the CLI is missing."""
    target = f"{relative(path, root)}:{max(line, 1)}"
    cli = zed_cli()
    if print_only or not cli:
        print(f"  {target}")
        if not cli and not print_only:
            print("  (no Zed CLI found; run `cli: install` from Zed to enable opening)")
        return
    subprocess.run([cli, f"{path}:{max(line, 1)}"], check=False)
    print(f"  opened {target}")


def pick_location(entries, prompt, args):
    """Show `entries` as `(label, path, line)` and open the chosen one."""
    if not entries:
        sys.exit(f"no {prompt} found")
    labels = [
        "{}  {}".format(
            label,
            f"{relative(path, args.root)}:{max(line, 1)}" if path else "(no location)",
        )
        for label, path, line in entries
    ]
    index = choose_indexes(labels, prompt)[0]
    label, path, line = entries[index]
    if not path:
        print(f"  {label}")
        print("  (no source location reported for this entry)")
        return
    open_location(path, line, args.root, args.print_only)


def run_routes(args, binary):
    """Browse Symfony routes and jump to the controller."""
    rows = execute(binary, args.root, "shopware/symfony/analytics/routes", {})
    entries = []
    for row in rows if isinstance(rows, list) else []:
        # sourceUri is the #[Route] attribute; controllerUri the method body.
        uri = row.get("sourceUri") or row.get("controllerUri") or ""
        line = row.get("sourceLine") or row.get("controllerLine") or 1
        entries.append(
            (
                "{:<52} {}".format(row.get("name", "?"), row.get("path", "")),
                uri_to_path(uri) if uri else "",
                line,
            )
        )
    pick_location(sorted(entries), "route", args)


def run_locate_service(args, binary):
    """Find where a service id or class is defined."""
    identifier = (
        args.service
        or (args.target if args.target else None)
        or os.environ.get("ZED_SELECTED_TEXT")
        or input("service id or class: ")
    ).strip()
    if not identifier:
        sys.exit("a service identifier is required")

    rows = execute(
        binary,
        args.root,
        "shopware/symfony/analytics/services/locate",
        {"identifier": identifier},
    )
    entries = []
    for row in rows if isinstance(rows, list) else []:
        class_uri = row.get("classFileUri") or ""
        if class_uri:
            entries.append(
                (
                    "class    {}".format(row.get("className", identifier)),
                    uri_to_path(class_uri),
                    row.get("classLine") or 1,
                )
            )
        for definition in row.get("definitions") or []:
            uri = definition.get("fileUri") or ""
            entries.append(
                (
                    "def      {}".format(definition.get("source", "")),
                    uri_to_path(uri) if uri else "",
                    definition.get("sourceLine") or 1,
                )
            )
    pick_location(entries, f"location for {identifier}", args)


def run_template_usages(args, binary):
    """Find the templates that extend or include this one."""
    path = os.path.abspath(args.target)
    if not os.path.isfile(path):
        sys.exit(f"not a file: {path}")

    rows = execute(
        binary,
        args.root,
        "shopware/symfony/analytics/twig/templateUsages",
        {"fileGlob": relative(path, args.root)},
    )
    entries = []
    for row in rows if isinstance(rows, list) else []:
        for kind in ("extends", "includes", "embeds", "controllers"):
            for usage in row.get(kind) or []:
                uri = usage.get("fileUri") or ""
                label = usage.get("controller") or relative(
                    uri_to_path(uri) if uri else "?", args.root
                )
                entries.append(
                    (
                        "{:<9} {}".format(kind, label),
                        uri_to_path(uri) if uri else "",
                        usage.get("line") or 1,
                    )
                )
    pick_location(sorted(entries), "usage", args)


CONFIG_TEMPLATE = """# Shopware LSP project configuration.
# Schema: https://raw.githubusercontent.com/shopware/shopware-lsp/main/internal/projectconfig/schema.json
#
# `version` is mandatory. Without it the server refuses the whole file with
# "configuration version is required".
version: 1
# features:
#   semanticTokens: true
# diagnostics:
#   rules:
#     php.version: off
"""


def run_config(args, binary):
    """Show the effective configuration and where it comes from."""
    result = subprocess.run(
        [binary, "-root", args.root, "config"],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        sys.exit((result.stderr or result.stdout).strip()[:400])
    try:
        report = json.loads(result.stdout)
    except ValueError:
        sys.exit(f"unexpected config output: {result.stdout[:200]}")

    path = report.get("path") or ""
    exists = bool(path) and os.path.isfile(path)
    print(f"config file : {relative(path, args.root) if path else '(none)'}"
          f"{'' if exists else '  (not created yet; defaults in use)'}")
    if report.get("scopes"):
        print(f"scopes      : {json.dumps(report['scopes'])}")
    print()
    print(json.dumps(report.get("effective", {}), indent=2, sort_keys=True))


def run_config_open(args, binary):
    """Open the project configuration, creating a valid stub when absent."""
    result = subprocess.run(
        [binary, "-root", args.root, "config"], capture_output=True, text=True
    )
    path = ""
    try:
        path = (json.loads(result.stdout) or {}).get("path") or ""
    except ValueError:
        pass
    if not path:
        path = os.path.join(args.root, ".config", "shopware", "lsp.yaml")

    if os.path.isfile(path):
        print(f"exists  {relative(path, args.root)}")
    elif args.print_only:
        print(f"would create {relative(path, args.root)}")
        print(CONFIG_TEMPLATE, end="")
        return
    else:
        path = prepare_write(path, args.root, "create")
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(CONFIG_TEMPLATE)
        print(f"created {relative(path, args.root)}")

    open_location(path, 1, args.root, args.print_only)


def run_reindex(args, binary):
    """Rebuild the workspace index from scratch."""
    if args.print_only:
        print("would run: shopware-lsp index -force")
        return
    result = subprocess.run(
        [binary, "-root", args.root, "index", "-force"],
        capture_output=True,
        text=True,
    )
    print((result.stdout or result.stderr).strip()[:400])
    if result.returncode != 0:
        sys.exit(result.returncode)


def run_uuid(args, binary):
    """Insert a Shopware-style UUID at the cursor, or print one.

    32 lowercase hex characters: a v4 UUID with the dashes removed, which is
    what `Uuid::randomHex()` produces and what the VS Code command inserts.
    """
    import uuid as uuid_module

    value = uuid_module.uuid4().hex

    if not args.target:
        print(value)
        return

    path = os.path.abspath(args.target)
    if not os.path.isfile(path):
        sys.exit(f"not a file: {path}")

    with open(path, encoding="utf-8") as handle:
        text = handle.read()
    lines = text.splitlines(keepends=True)
    # splitlines drops the empty segment after a trailing newline, but that is
    # a real line to the editor and is where the cursor sits on a file ending
    # in a blank line. Without it the clamp below silently walks the insertion
    # back onto the previous line.
    if not lines or text.endswith("\n"):
        lines.append("")

    index = max(0, min(args.row - 1, len(lines) - 1))
    line = lines[index]
    # Zed passes a one-based UTF-8 byte column, and the trailing newline is
    # not a valid insertion point.
    body = line.rstrip("\n")
    column = byte_column_to_index(body, max(args.column - 1, 0))
    lines[index] = body[:column] + value + body[column:] + line[len(body) :]

    if args.print_only:
        print(f"would insert {value} at {relative(path, args.root)}:{index + 1}:{column + 1}")
        return

    with open(path, "w", encoding="utf-8") as handle:
        handle.write("".join(lines))
    print(f"inserted {value} at {relative(path, args.root)}:{index + 1}:{column + 1}")


def main():
    # `run` is handled before argparse: the positional `target` and `row` would
    # otherwise swallow the server's own arguments. A passthrough matters
    # because tasks must not hardcode a path; the only copy of the binary is
    # often the one the Zed extension downloaded for itself.
    if len(sys.argv) > 1 and sys.argv[1] == "run":
        forwarded = sys.argv[2:]
        if forwarded and forwarded[0] == "--":
            forwarded = forwarded[1:]
        binary = server_binary()
        os.execv(binary, [binary, *forwarded])

    parser = argparse.ArgumentParser(
        description="Run shopware-lsp generators that cannot be Zed code actions."
    )
    parser.add_argument("action", choices=action_names() + ["list"])
    parser.add_argument("target", nargs="?", help="file, or directory for scaffold")
    parser.add_argument("row", nargs="?", type=int, default=1)
    parser.add_argument(
        "--root", default=os.environ.get("ZED_WORKTREE_ROOT") or os.getcwd()
    )
    parser.add_argument("--name", help="scaffold name, skips the prompt")
    parser.add_argument("--key", help="snippet key, skips the prompt")
    parser.add_argument("--value", help="snippet value, skips the prompt")
    parser.add_argument("--block", help="Twig block name, skips the picker")
    parser.add_argument(
        "--format", help="service definition format: yaml, xml, fluent or php-array"
    )
    parser.add_argument("--service-id", help="explicit service id")
    parser.add_argument(
        "--class-as-id", action="store_true", help="use the class name as the service id"
    )
    parser.add_argument("--text", help="text to extract, defaults to $ZED_SELECTED_TEXT")
    parser.add_argument("--domain", help="translation domain, skips the picker")
    parser.add_argument("--service", help="service id or class, skips the prompt")
    parser.add_argument(
        "--column",
        type=int,
        default=1,
        help="one-based UTF-8 byte column; Zed passes $ZED_COLUMN",
    )
    parser.add_argument("--extension", help="extension name, skips the picker")
    parser.add_argument(
        "--class",
        dest="class_name",
        help="fully qualified class to act on, instead of resolving it from the file",
    )
    parser.add_argument(
        "--option",
        action="append",
        default=[],
        metavar="KEY=VALUE",
        help=(
            "extra scaffold option, repeatable. Some kinds require one, e.g. "
            "event-listener needs event=<FQCN>. Known keys: author, category, "
            "color, description, event, hook, icon, label, license, method, "
            "methodGroup, mode, namespace, package, parameters, target, "
            "taskName, timestamp, type"
        ),
    )
    parser.add_argument(
        "--print",
        dest="print_only",
        action="store_true",
        help="show what would change instead of writing",
    )
    args = parser.parse_args()

    # Zed hands over a tilde-abbreviated path in `$ZED_FILE`, and
    # `$ZED_WORKTREE_ROOT` can be one too. `os.path.abspath` does not expand
    # `~`, so it treats the whole value as relative and joins it onto the
    # working directory, yielding `<worktree>/~/Users/...` and "not a file".
    # Normalising here covers every action, since they all reach the
    # filesystem through `args.target` and `args.root`.
    if args.target:
        args.target = os.path.expanduser(args.target)
    args.root = os.path.expanduser(args.root)

    if args.action == "list":
        for name in sorted(SNIPPET_ACTIONS):
            print(f"  {name:20} {SNIPPET_ACTIONS[name]['label']}")
        for name in sorted(OTHER_ACTIONS):
            print(f"  {name:20} {OTHER_ACTIONS[name]}")
        return

    binary = server_binary()

    standalone = {
        "scaffold": run_scaffold,
        "routes": run_routes,
        "locate-service": run_locate_service,
        "config": run_config,
        "config-open": run_config_open,
        "reindex": run_reindex,
    }
    if args.action in standalone:
        standalone[args.action](args, binary)
        return

    if args.action == "compiler-pass":
        run_compiler_pass(args, binary)
        return

    if args.action == "uuid":
        run_uuid(args, binary)
        return

    if not args.target:
        sys.exit(f"{args.action} needs a file")

    handlers = {
        "twig-form-fields": run_twig_form_fields,
        "twig-extend-block": run_twig_extend_block,
        "admin-twig-override": run_admin_twig_override,
        "twig-block-diff": run_twig_block_diff,
        "template-usages": run_template_usages,
        "uuid": run_uuid,
        "snippet": lambda a, b: run_snippet_create(a, b, "storefront"),
        "snippet-admin": lambda a, b: run_snippet_create(a, b, "admin"),
        "service-definition": run_service_definition,
        "translation-extract": run_translation_extract,
    }
    handler = handlers.get(args.action)
    if handler:
        handler(args, binary)
        return

    run_snippet_action(SNIPPET_ACTIONS[args.action], args, binary)


if __name__ == "__main__":
    main()
