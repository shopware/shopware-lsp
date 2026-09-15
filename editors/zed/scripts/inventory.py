#!/usr/bin/env python3
"""Detect upstream surface this extension may need to adapt to.

`contract-check.py` guards assumptions we already know about. It is blind to
*new* surface, which is the other half of the problem: shopware-lsp gains
commands, scaffolds and tools continuously, and some of that is work for us.

Most categories are absorbed automatically by design:

* new **scaffolds** appear in `sw-action.py scaffold`, which reads the catalog
  live rather than hardcoding kinds;
* new **MCP tools** reach Zed's Agent Panel, since the context server passes
  through whatever the server offers;
* new **client commands** are filtered out by our `supportedCommands`, so they
  never become dead menu entries, though they still count against parity.

Three categories are not:

* a new **server command** may be a generator worth wiring into `sw-action.py`,
  and nothing will tell us unless we look;
* a **removed or renamed** command breaks an action we already ship;
* a new **palette or client command** changes how complete this extension is,
  and the parity counts in docs/vscode-parity.md silently go stale. They
  did: the table
  compared the client-command count against the palette's coverage for
  several releases.

So this compares a snapshot of the server's own inventories against
`inventory/snapshot.json`, and every palette and client command against a
decision recorded in `inventory/parity.json`.

    scripts/inventory.py --check    # diff, and verify what we depend on
    scripts/inventory.py --write    # accept the current surface as the baseline

Exit codes: 0 unchanged, 1 breakage (something we use disappeared), 2 new
surface only. CI fails on either non-zero; clear an addition by reviewing it,
then re-running with --write and committing the snapshot.
"""

import argparse
import json
import os
import pathlib
import re
import subprocess
import sys

SNAPSHOT = pathlib.Path(__file__).resolve().parent.parent / "inventory" / "snapshot.json"
PARITY = SNAPSHOT.parent / "parity.json"
ACTION_SCRIPT = pathlib.Path(__file__).resolve().parent / "sw-action.py"
MANIFEST_URL = "https://open-vsx.org/api/shopware/shopware-lsp/linux-x64/latest"


def zed_managed_server():
    """Newest server the Zed extension downloaded for itself, if any.

    Following the documented install leaves nothing on PATH: the extension
    keeps its download inside its own work directory. sw-action.py has the
    same lookup, deliberately duplicated so each script stays runnable on its
    own. Sorted by parsed version, since 0.3.9 beats 0.3.57 as a string.
    """
    import glob
    import re as _re

    roots = (
        "~/Library/Application Support/Zed/extensions/work/shopware-lsp",
        "~/.local/share/zed/extensions/work/shopware-lsp",
        "~/AppData/Local/Zed/extensions/work/shopware-lsp",
    )
    found = []
    for root in roots:
        pattern = os.path.join(
            os.path.expanduser(root), "shopware-lsp-*", "extension", "shopware-lsp*"
        )
        found += [
            entry
            for entry in glob.glob(pattern)
            if os.path.basename(entry) in ("shopware-lsp", "shopware-lsp.exe")
        ]

    def version(path):
        directory = os.path.basename(os.path.dirname(os.path.dirname(path)))
        match = _re.search(r"shopware-lsp-(\d+(?:\.\d+)*)", directory)
        return tuple(int(p) for p in match.group(1).split(".")) if match else (-1,)

    return max(found, key=version) if found else None


def server_binary():
    import shutil

    for candidate in (
        os.environ.get("SHOPWARE_LSP_BIN"),
        shutil.which("shopware-lsp"),
        os.path.expanduser("~/.local/bin/shopware-lsp"),
        zed_managed_server(),
    ):
        if candidate and os.path.isfile(candidate):
            return candidate
    sys.exit(
        "shopware-lsp not found. Set SHOPWARE_LSP_BIN, run update-server.sh, "
        "or install the extension in Zed so it downloads one."
    )


def execute(binary, root, method, payload=None):
    args = [binary, "-root", root, "execute", method]
    if payload is not None:
        args.append(json.dumps(payload))
    result = subprocess.run(args, capture_output=True, text=True, timeout=300)
    output = (result.stdout or "").strip()
    if result.returncode != 0 or not output:
        sys.exit(f"{method} failed: {(result.stderr or result.stdout).strip()[:300]}")
    return json.loads(output)


def read_lsp_message(stream):
    while True:
        line = stream.readline()
        if not line:
            return None
        if line.lower().startswith(b"content-length"):
            length = int(line.split(b":")[1])
            while True:
                header = stream.readline()
                if not header or header in (b"\r\n", b"\n"):
                    break
            return json.loads(stream.read(length))


def initialize(binary, root):
    """Capability surface, as negotiated the way the extension negotiates it."""
    process = subprocess.Popen(
        [binary],
        cwd=root,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
    )
    request = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": {
            "processId": os.getpid(),
            "rootUri": "file://" + root,
            "workspaceFolders": [{"uri": "file://" + root, "name": "inventory"}],
            "capabilities": {
                "textDocument": {
                    "codeAction": {
                        "dataSupport": True,
                        "resolveSupport": {"properties": ["edit"]},
                    },
                    "publishDiagnostics": {"dataSupport": True},
                }
            },
            "initializationOptions": {
                "shopwareClient": {
                    "protocolVersion": 1,
                    "presentationProfile": "full",
                    "supportedCommands": [],
                }
            },
        },
    }
    body = json.dumps(request).encode()
    process.stdin.write(b"Content-Length: %d\r\n\r\n" % len(body) + body)
    process.stdin.flush()
    response = None
    for _ in range(12):
        message = read_lsp_message(process.stdout)
        if message is None:
            break
        if message.get("id") == 1:
            response = message
            break
    process.kill()
    if not response or "result" not in response:
        sys.exit("initialize failed while taking the inventory")
    return response["result"]


def mcp_tools(binary, root):
    process = subprocess.Popen(
        [binary, "-root", root, "mcp"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        bufsize=0,
    )

    def send(message):
        process.stdin.write((json.dumps(message) + "\n").encode())
        process.stdin.flush()

    send(
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "initialize",
            "params": {
                "protocolVersion": "2025-06-18",
                "capabilities": {},
                "clientInfo": {"name": "inventory", "version": "1"},
            },
        }
    )
    send({"jsonrpc": "2.0", "method": "notifications/initialized"})
    send({"jsonrpc": "2.0", "id": 2, "method": "tools/list"})

    tools = []
    for _ in range(80):
        line = process.stdout.readline()
        if not line:
            break
        line = line.strip()
        if not line:
            continue
        try:
            message = json.loads(line)
        except ValueError:
            continue
        if message.get("id") == 2:
            tools = [tool["name"] for tool in message["result"]["tools"]]
            break
    process.kill()
    return sorted(tools)


def palette_commands(extension_dir):
    """Command ids the VS Code extension puts in the palette.

    Not the same list as the server's clientCommands, which are almost all
    attached to code actions. Conflating the two is how the docs came to
    compare one list's size against the other's coverage.

    Read from a local unzipped vsix when given one, since CI already downloads
    it for contract-check.py, and fetched from Open VSX otherwise. There is no
    skip path: a parity gate that quietly does nothing is worse than none.
    """
    if extension_dir:
        manifest = json.loads((pathlib.Path(extension_dir) / "package.json").read_text())
    else:
        import io
        import urllib.request
        import zipfile

        with urllib.request.urlopen(MANIFEST_URL, timeout=60) as response:
            url = json.load(response)["files"]["download"]
        with urllib.request.urlopen(url, timeout=180) as response:
            archive = zipfile.ZipFile(io.BytesIO(response.read()))
        manifest = json.loads(archive.read("extension/package.json"))

    commands = manifest.get("contributes", {}).get("commands") or []
    return sorted(entry["command"] for entry in commands)


def check_parity(current):
    """Every upstream command must have a decision recorded in parity.json."""
    if not PARITY.is_file():
        return [f"no parity map at {PARITY}"], []
    parity = json.loads(PARITY.read_text())

    breakage, additions = [], []
    for key, group in (("palette", "paletteCommands"), ("clientCommands", "clientCommands")):
        recorded = parity.get(key) or {}
        upstream = set(current[group])
        for command in sorted(upstream - set(recorded)):
            additions.append(
                f"{key}: {command} is new and has no entry in parity.json; "
                "record the action that covers it, or why none does"
            )
        for command in sorted(set(recorded) - upstream):
            breakage.append(
                f"{key}: parity.json still maps {command}, which upstream removed"
            )
    return breakage, additions


def collect(binary, root, extension_dir=None):
    result = initialize(binary, root)
    capabilities = result.get("capabilities", {})
    experimental = capabilities.get("experimental", {}).get("shopwareLSP", {})
    catalog = execute(binary, root, "shopware/integration/catalog", {})
    config = json.loads(
        subprocess.run(
            [binary, "-root", root, "config"], capture_output=True, text=True, timeout=300
        ).stdout
    )["effective"]
    api = json.loads(
        subprocess.run(
            [binary, "api-json"], capture_output=True, text=True, timeout=300
        ).stdout
    )

    code_action = capabilities.get("codeActionProvider") or {}

    return {
        "protocolVersion": experimental.get("protocolVersion"),
        "capabilities": sorted(capabilities.keys()),
        "codeActionKinds": sorted(code_action.get("codeActionKinds") or []),
        "serverCommands": sorted(execute(binary, root, "shopware/commands", {})),
        "clientCommands": sorted(
            entry["id"] for entry in catalog.get("clientCommands") or []
        ),
        "scaffolds": sorted(
            f"{entry.get('family', '?')}/{entry.get('kind', '?')}"
            for entry in catalog.get("scaffolds") or []
        ),
        "paletteCommands": palette_commands(extension_dir),
        "mcpTools": mcp_tools(binary, root),
        "features": sorted(config.get("features", {}).keys()),
        "cliCommands": sorted(
            entry["name"] for entry in api.get("commands") or [] if entry.get("name")
        ),
    }


def commands_we_depend_on():
    """Server commands referenced by sw-action.py.

    Derived from the script rather than a hand-kept list, so the two cannot
    drift apart.
    """
    source = ACTION_SCRIPT.read_text()
    # Note the uppercase range: extendBlock, getBlockDiff and compilerPass
    # are camelCase and a lowercase-only pattern silently skips them.
    found = set(re.findall(r'"(shopware/[A-Za-z0-9/\-]+)"', source))
    # f-string domains, e.g. f"shopware/snippet/{domain}/create"
    for template in re.findall(r'f"(shopware/snippet/\{domain\}/[a-zA-Z]+)"', source):
        for domain in ("storefront", "admin"):
            found.add(template.replace("{domain}", domain))
    return sorted(found)


def diff_lists(previous, current):
    added = [item for item in current if item not in previous]
    removed = [item for item in previous if item not in current]
    return added, removed


def main():
    parser = argparse.ArgumentParser()
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--check", action="store_true")
    group.add_argument("--write", action="store_true")
    parser.add_argument("--root", default=os.getcwd())
    parser.add_argument("--binary")
    parser.add_argument(
        "--extension-dir",
        help="unzipped vsix `extension/` directory; fetched from Open VSX when omitted",
    )
    args = parser.parse_args()

    binary = args.binary or server_binary()
    root = os.path.abspath(args.root)
    current = collect(binary, root, args.extension_dir)

    if args.write:
        SNAPSHOT.parent.mkdir(parents=True, exist_ok=True)
        SNAPSHOT.write_text(json.dumps(current, indent=2, sort_keys=True) + "\n")
        print(f"wrote {SNAPSHOT.relative_to(SNAPSHOT.parent.parent)}")
        for key, value in current.items():
            size = len(value) if isinstance(value, list) else value
            print(f"  {key}: {size}")
        return 0

    if not SNAPSHOT.is_file():
        sys.exit(f"no snapshot at {SNAPSHOT}; run --write first")
    previous = json.loads(SNAPSHOT.read_text())

    breakage, additions = check_parity(current)

    # Anything sw-action.py calls must still exist.
    available = set(current["serverCommands"])
    for command in commands_we_depend_on():
        if command not in available:
            breakage.append(f"sw-action.py uses {command}, which the server no longer has")

    if current["protocolVersion"] != previous.get("protocolVersion"):
        breakage.append(
            "client protocol version moved from "
            f"{previous.get('protocolVersion')} to {current['protocolVersion']}; "
            "CLIENT_PROTOCOL_VERSION in src/lib.rs must match or initialize fails"
        )

    for key in sorted(k for k, v in current.items() if isinstance(v, list)):
        added, removed = diff_lists(previous.get(key) or [], current[key])
        for item in removed:
            breakage.append(f"{key}: removed {item}")
        for item in added:
            additions.append(f"{key}: new {item}")

    if breakage:
        print("BREAKAGE")
        for line in breakage:
            print(f"  - {line}")
        print()
    if additions:
        print("NEW SURFACE")
        for line in additions:
            print(f"  - {line}")
        print()
        print(
            "Scaffolds and MCP tools are absorbed automatically.\n"
            "A new server command may be a generator worth adding to sw-action.py.\n"
            "A new palette or client command needs an inventory/parity.json entry,\n"
            "and docs/vscode-parity.md moves with it; test-sw-action.py checks.\n"
            "Once reviewed, re-run with --write and commit the snapshot."
        )

    if breakage:
        return 1
    if additions:
        return 2
    print("upstream surface unchanged")
    return 0


if __name__ == "__main__":
    sys.exit(main())
