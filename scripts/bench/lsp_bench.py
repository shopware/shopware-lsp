#!/usr/bin/env python3
"""Drive a shopware-lsp binary over stdio and measure request latency.

Runs the same deterministic request mix against any server binary (for
example two builds of different revisions), so two result files can be
compared with `compare.py`:

    lsp_bench.py --server target/base/shopware-lsp --root ~/shopware \
        --out target/bench/base.json
    lsp_bench.py --server target/release/shopware-lsp --root ~/shopware \
        --out target/bench/new.json
    compare.py target/bench/base.json target/bench/new.json

Each run uses its own cache directory (`--cache`); pass `--cold` to wipe it
first. Results record wall time per request, the indexing time, peak RSS
(VmHWM) and a hash of every response so behavior differences show up next to
timing differences.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import threading
import time
from pathlib import Path
from urllib.parse import quote

CATEGORIES = {
    # name: (glob roots relative to workspace, extensions, language id)
    "admin-js": (["src/Administration/Resources/app/administration/src"], (".js",), "javascript"),
    "admin-ts": (["src/Administration/Resources/app/administration/src"], (".ts",), "typescript"),
    "admin-twig": (["src/Administration/Resources/app/administration/src"], (".html.twig",), "twig"),
    "storefront-twig": (["src/Storefront/Resources/views"], (".html.twig",), "twig"),
    "php": (["src/Core", "src/Storefront", "src/Administration"], (".php",), "php"),
    "migration": (["src/Core/Migration"], (".php",), "php"),
    "xml": (["src/Core", "src/Storefront"], (".xml",), "xml"),
    "yaml": (["src/Core", "src/Storefront", "config"], (".yaml", ".yml"), "yaml"),
    "scss": (["src/Storefront/Resources/app/storefront/src/scss"], (".scss",), "scss"),
}

# Focused categories place positions inside specific constructs (route names,
# translation keys, ...) instead of at sampled identifiers. Each entry is
# (directories, extensions, language id, regex whose group 1 is the target).
FOCUS = {
    "route-twig": (["src/Storefront/Resources/views"], (".html.twig",), "twig",
                   r"""(?:path|url|seoUrl)\(\s*'([a-z][a-z0-9_.]+)'"""),
    "route-php": (["src/Storefront/Controller", "src/Core"], (".php",), "php",
                  r"""(?:name:\s*|redirectToRoute\(\s*|generateUrl\(\s*)'([a-z][a-z0-9_.]+\.[a-z0-9_.]+)'"""),
    "trans-twig": (["src/Storefront/Resources/views"], (".html.twig",), "twig",
                   r"""'([a-zA-Z][a-zA-Z0-9_.-]+)'\s*\|\s*trans"""),
    "trans-php": (["src/Storefront", "src/Core"], (".php",), "php",
                  r"""->trans\(\s*'([a-zA-Z][a-zA-Z0-9_.-]+)'"""),
    "event-php": (["src/Core", "src/Storefront"], (".php",), "php",
                  r"""(?:([A-Z][A-Za-z0-9]+Event)::class)"""),
    "service-xml": (["src/Core", "src/Storefront"], (".xml",), "xml",
                    r'<argument type="service" id="([^"]+)"'),
    "repository-php": (["src/Core", "src/Storefront"], (".php",), "php",
                       r"""\$this->([a-zA-Z]+Repository)->"""),
    # Symfony UX projects (see ux_fixture.py); absent from the Shopware corpus.
    "ux-tag-twig": (["templates"], (".html.twig",), "twig", r"<twig:([A-Za-z0-9:]+)"),
    "ux-function-twig": (["templates"], (".html.twig",), "twig", r"component\('([A-Za-z0-9:]+)'"),
    "ux-event-twig": (["templates"], (".html.twig",), "twig", r'data-live-event-param="([A-Za-z0-9]+)"'),
    "ux-action-twig": (["templates"], (".html.twig",), "twig", r'data-live-action-param="([A-Za-z0-9]+)"'),
    "ux-listener-php": (["src"], (".php",), "php", r"LiveListener\('([A-Za-z0-9]+)'"),
    "ux-emit-php": (["src"], (".php",), "php", r"emit(?:Up|Self)?\('([A-Za-z0-9]+)'"),
}

DOCUMENT_METHODS = [
    "textDocument/diagnostic",
    "textDocument/documentSymbol",
    "textDocument/documentLink",
    "textDocument/codeLens",
    "textDocument/foldingRange",
    "textDocument/semanticTokens/full",
    "textDocument/inlayHint",
]
POSITION_METHODS = [
    "textDocument/hover",
    "textDocument/definition",
    "textDocument/references",
    "textDocument/completion",
    "textDocument/documentHighlight",
    "textDocument/signatureHelp",
]

IDENT = re.compile(r"[A-Za-z_$][A-Za-z0-9_$\\.:-]*")


def file_uri(path: Path) -> str:
    return "file://" + quote(str(path))


def select_files(root: Path, per_category: int, only: list[str] | None) -> list[tuple[str, Path, str]]:
    selected = []
    for name, (dirs, exts, language) in CATEGORIES.items():
        if only and name not in only:
            continue
        candidates = []
        for rel in dirs:
            base = root / rel
            if not base.is_dir():
                continue
            for path in base.rglob("*"):
                text = str(path)
                if not path.is_file() or not text.endswith(exts):
                    continue
                if "/node_modules/" in text or "/Test/" in text or "/test/" in text or "/fixtures/" in text:
                    continue
                if name == "php" and "/Migration/" in text:
                    continue
                if name == "migration" and not re.search(r"/Migration/V6_\d+/", text):
                    continue
                if name == "admin-js" and text.endswith(".spec.js"):
                    continue
                if name == "admin-ts" and (text.endswith(".spec.ts") or text.endswith(".d.ts")):
                    continue
                candidates.append(path)
        candidates.sort()
        if not candidates:
            continue
        step = max(1, len(candidates) // per_category)
        for path in candidates[::step][:per_category]:
            selected.append((name, path, language))
    return selected


def select_focus_files(root: Path, per_category: int, only: list[str] | None) -> list[tuple[str, Path, str]]:
    selected = []
    for name, (dirs, exts, language, pattern) in FOCUS.items():
        if not only or name not in only:
            continue
        regex = re.compile(pattern)
        candidates = []
        for rel in dirs:
            base = root / rel
            if not base.is_dir():
                continue
            for path in base.rglob("*"):
                text = str(path)
                if not path.is_file() or not text.endswith(exts) or "/Test/" in text or "/test/" in text:
                    continue
                try:
                    if regex.search(path.read_text(encoding="utf-8", errors="replace")):
                        candidates.append(path)
                except OSError:
                    continue
        candidates.sort()
        step = max(1, len(candidates) // per_category) if candidates else 1
        for path in candidates[::step][:per_category]:
            selected.append((name, path, language))
    return selected


def focus_positions(source: str, pattern: str, count: int) -> list[tuple[int, int]]:
    """Positions inside group 1 of each `pattern` match, evenly sampled."""
    import bisect

    matches = list(re.finditer(pattern, source))
    if not matches or count <= 0:
        return []
    step = max(1, len(matches) // count)
    line_starts = [0]
    for index, char in enumerate(source):
        if char == "\n":
            line_starts.append(index + 1)
    result = []
    for match in matches[::step][:count]:
        offset = match.start(1) + max(1, (match.end(1) - match.start(1)) // 2)
        line = bisect.bisect_right(line_starts, offset) - 1
        character = len(source[line_starts[line] : offset].encode("utf-16-le")) // 2
        result.append((line, character))
    return result


def positions(source: str, count: int) -> list[tuple[int, int]]:
    """Evenly spaced identifier positions as (line, utf16 character)."""
    matches = [m for m in IDENT.finditer(source) if len(m.group(0)) > 2]
    if not matches or count <= 0:
        return []
    step = max(1, len(matches) // count)
    picked = matches[step // 2 :: step][:count]
    line_starts = [0]
    for index, char in enumerate(source):
        if char == "\n":
            line_starts.append(index + 1)
    result = []
    import bisect

    for match in picked:
        offset = match.start() + len(match.group(0)) // 2
        line = bisect.bisect_right(line_starts, offset) - 1
        prefix = source[line_starts[line] : offset]
        character = len(prefix.encode("utf-16-le")) // 2
        result.append((line, character))
    return result


class Client:
    def __init__(self, argv: list[str], env: dict[str, str]):
        self.process = subprocess.Popen(
            argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=env
        )
        self.next_id = 0
        self.lock = threading.Lock()
        self.pending: dict[int, dict] = {}
        self.events: dict[int, threading.Event] = {}
        self.notifications: list[tuple[float, str, dict]] = []
        self.notified = threading.Condition()
        threading.Thread(target=self._read, daemon=True).start()

    def _write(self, message: dict) -> None:
        body = json.dumps(message).encode()
        with self.lock:
            self.process.stdin.write(b"Content-Length: %d\r\n\r\n" % len(body) + body)
            self.process.stdin.flush()

    def _read(self) -> None:
        stream = self.process.stdout
        while True:
            length = 0
            while True:
                line = stream.readline()
                if not line:
                    with self.notified:
                        self.notified.notify_all()
                    for event in self.events.values():
                        event.set()
                    return
                line = line.strip()
                if not line:
                    break
                if line.lower().startswith(b"content-length:"):
                    length = int(line.split(b":")[1])
            message = json.loads(stream.read(length))
            if "id" in message and "method" in message:
                self._answer(message)
            elif "id" in message:
                self.pending[message["id"]] = message
                event = self.events.get(message["id"])
                if event:
                    event.set()
            else:
                with self.notified:
                    self.notifications.append((time.monotonic(), message["method"], message.get("params")))
                    self.notified.notify_all()

    def _answer(self, message: dict) -> None:
        method = message["method"]
        result = None
        if method == "workspace/configuration":
            result = [None for _ in message.get("params", {}).get("items", [])]
        self._write({"jsonrpc": "2.0", "id": message["id"], "result": result})

    def request(self, method: str, params: dict, timeout: float = 600) -> tuple[float, dict]:
        self.next_id += 1
        request_id = self.next_id
        event = threading.Event()
        self.events[request_id] = event
        started = time.perf_counter()
        self._write({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params})
        if not event.wait(timeout):
            raise TimeoutError(f"{method} timed out")
        elapsed = time.perf_counter() - started
        del self.events[request_id]
        response = self.pending.pop(request_id, None)
        if response is None:
            raise RuntimeError("server exited")
        return elapsed, response

    def notify(self, method: str, params: dict) -> None:
        self._write({"jsonrpc": "2.0", "method": method, "params": params})

    def wait_notification(self, method: str, timeout: float) -> float:
        deadline = time.monotonic() + timeout
        with self.notified:
            while True:
                for stamp, name, _ in self.notifications:
                    if name == method:
                        return stamp
                remaining = deadline - time.monotonic()
                if remaining <= 0 or self.process.poll() is not None:
                    raise TimeoutError(f"no {method}")
                self.notified.wait(remaining)

    def peak_rss_kib(self) -> int:
        try:
            for line in Path(f"/proc/{self.process.pid}/status").read_text().splitlines():
                if line.startswith("VmHWM:"):
                    return int(line.split()[1])
        except OSError:
            pass
        return 0


KEEP_RESPONSES = False
CACHE_PATH = ""


def response_hash(response: dict) -> str:
    payload = response.get("result") if "error" not in response else {"error": response["error"].get("message")}
    if isinstance(payload, dict) and "items" in payload and payload["items"] is None:
        # Go encodes an empty completion list as `items: null`.
        payload = dict(payload, items=[])
    if isinstance(payload, dict) and "resultId" in payload:
        payload = {k: v for k, v in payload.items() if k != "resultId"}
    encoded = json.dumps(payload, sort_keys=True, separators=(",", ":"))
    if CACHE_PATH:
        # Phar sources live under the cache directory, which differs per run.
        encoded = encoded.replace(CACHE_PATH, "<cache>")
        payload = json.loads(encoded)
    encoded = encoded.encode()
    digest = hashlib.sha1(encoded).hexdigest()[:16]
    if KEEP_RESPONSES:
        RESPONSES[digest] = payload
    return digest


RESPONSES: dict[str, object] = {}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--server", required=True)
    parser.add_argument("--root", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--cache", help="cache directory (default: <out>.cache)")
    parser.add_argument("--cold", action="store_true", help="wipe the cache before starting")
    parser.add_argument("--files-per-category", type=int, default=20)
    parser.add_argument("--positions-per-file", type=int, default=6)
    parser.add_argument(
        "--categories",
        help="comma-separated subset of " + ",".join(CATEGORIES)
        + "; focused categories (" + ",".join(FOCUS) + ") run only when named",
    )
    parser.add_argument("--methods", help="comma-separated subset of methods (short names, e.g. hover,diagnostic)")
    parser.add_argument("--index-timeout", type=float, default=1800)
    parser.add_argument("--responses", action="store_true", help="store full response payloads (large)")
    args = parser.parse_args()

    global KEEP_RESPONSES, CACHE_PATH
    KEEP_RESPONSES = args.responses
    root = Path(args.root).expanduser().resolve()
    cache = Path(args.cache or args.out + ".cache").resolve()
    if args.cold and cache.exists():
        shutil.rmtree(cache)
    cache.mkdir(parents=True, exist_ok=True)
    CACHE_PATH = json.dumps(str(cache))[1:-1]
    env = dict(os.environ, SHOPWARE_LSP_CACHE_DIR=str(cache))
    only = args.categories.split(",") if args.categories else None
    wanted = set(args.methods.split(",")) if args.methods else None

    def enabled(method: str) -> bool:
        return wanted is None or method.rsplit("/", 1)[-1] in wanted or method in wanted

    files = select_files(root, args.files_per_category, only) + select_focus_files(
        root, args.files_per_category, only
    )
    client = Client([str(Path(args.server).resolve()), "serve"], env)
    started = time.monotonic()
    _, init = client.request(
        "initialize",
        {
            "processId": os.getpid(),
            "rootUri": file_uri(root),
            "rootPath": str(root),
            "workspaceFolders": [{"uri": file_uri(root), "name": root.name}],
            "capabilities": {
                "textDocument": {
                    "diagnostic": {"dynamicRegistration": False},
                    "hover": {"contentFormat": ["markdown", "plaintext"]},
                    "completion": {"completionItem": {"snippetSupport": True}},
                    "documentSymbol": {"hierarchicalDocumentSymbolSupport": True},
                },
                "workspace": {"configuration": True, "workspaceFolders": True},
                "window": {"workDoneProgress": True},
            },
            "initializationOptions": {"allowUnsupportedProject": True, "omitExecuteCommandProvider": True},
        },
    )
    if "error" in init:
        print("initialize failed:", init["error"], file=sys.stderr)
        return 1
    client.notify("initialized", {})
    indexed_at = client.wait_notification("shopware/indexingCompleted", args.index_timeout)
    index_seconds = indexed_at - started
    rss_after_index = client.peak_rss_kib()
    print(f"indexed in {index_seconds:.1f}s, peak RSS {rss_after_index / 1024:.0f} MiB", file=sys.stderr)

    records = []
    for number, (category, path, language) in enumerate(files, 1):
        source = path.read_text(encoding="utf-8", errors="replace")
        uri = file_uri(path)
        rel = str(path.relative_to(root))
        client.notify(
            "textDocument/didOpen",
            {"textDocument": {"uri": uri, "languageId": language, "version": 1, "text": source}},
        )
        document = {"textDocument": {"uri": uri}}
        line_count = source.count("\n") + 1
        for method in DOCUMENT_METHODS:
            if not enabled(method):
                continue
            params = dict(document)
            if method == "textDocument/inlayHint":
                params["range"] = {"start": {"line": 0, "character": 0}, "end": {"line": line_count, "character": 0}}
            try:
                elapsed, response = client.request(method, params)
                records.append({"category": category, "file": rel, "method": method, "pos": None,
                                "ms": elapsed * 1000, "hash": response_hash(response)})
            except TimeoutError:
                records.append({"category": category, "file": rel, "method": method, "pos": None,
                                "ms": None, "hash": "timeout"})
        if category in FOCUS:
            sampled = focus_positions(source, FOCUS[category][3], args.positions_per_file)
        else:
            sampled = positions(source, args.positions_per_file)
        for line, character in sampled:
            for method in POSITION_METHODS:
                if not enabled(method):
                    continue
                params = dict(document, position={"line": line, "character": character})
                if method == "textDocument/references":
                    params["context"] = {"includeDeclaration": True}
                try:
                    elapsed, response = client.request(method, params)
                    records.append({"category": category, "file": rel, "method": method,
                                    "pos": [line, character], "ms": elapsed * 1000,
                                    "hash": response_hash(response)})
                except TimeoutError:
                    records.append({"category": category, "file": rel, "method": method,
                                    "pos": [line, character], "ms": None, "hash": "timeout"})
        client.notify("textDocument/didClose", document)
        if number % 10 == 0:
            print(f"  {number}/{len(files)} files", file=sys.stderr)

    peak = client.peak_rss_kib()
    try:
        client.request("shutdown", {}, timeout=30)
        client.notify("exit", {})
        client.process.wait(timeout=30)
    except Exception:
        client.process.kill()
    Path(args.out).parent.mkdir(parents=True, exist_ok=True)
    Path(args.out).write_text(json.dumps({
        "server": args.server,
        "index_seconds": index_seconds,
        "rss_after_index_kib": rss_after_index,
        "peak_rss_kib": peak,
        "records": records,
        "responses": RESPONSES,
    }))
    total = sum(record["ms"] or 0 for record in records)
    print(f"{len(records)} requests, {total / 1000:.1f}s total, peak RSS {peak / 1024:.0f} MiB", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
