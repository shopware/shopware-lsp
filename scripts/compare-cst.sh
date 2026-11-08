#!/usr/bin/env bash
# Differential test: compare Go and Rust parser output on a corpus.
#
#   scripts/compare-cst.sh <lang> <corpus-dir> <ext> [<ext>...]
#
# The Rust side is the `dump-cst-<lang>` binary of crate `swlsp-parser-<lang>`
# (override the crate suffix with RUST_LANG, e.g. RUST_LANG=js for javascript).
#
# Prints the number of differing files and writes per-file diffs to
# $OUT (default: a temp dir).
set -euo pipefail
lang=$1; corpus=$2; shift 2
root=$(cd "$(dirname "$0")/.." && pwd)
out=${OUT:-$(mktemp -d)}
mkdir -p "$out"
(cd "$root" && go build -o "$out/dump_cst_go" ./cmd/dump_cst)
rust_lang=${RUST_LANG:-$lang}
(cd "$root" && cargo build -q --release -p "swlsp-parser-$rust_lang" --bin "dump-cst-$rust_lang")
name_args=()
for ext in "$@"; do
  [[ ${#name_args[@]} -gt 0 ]] && name_args+=(-o)
  name_args+=(-name "*.$ext")
done
# Only valid UTF-8 files: Rust sources are always UTF-8.
find "$corpus" -type f \( "${name_args[@]}" \) -not -path '*/node_modules/*' | sort |
  python3 -c '
import sys
for line in sys.stdin:
    path = line.rstrip("\n")
    try:
        open(path, "rb").read().decode("utf-8")
        print(path)
    except Exception:
        pass' > "$out/files.txt"
"$out/dump_cst_go" -lang "$lang" < "$out/files.txt" > "$out/go.txt"
"${CARGO_TARGET_DIR:-$root/target}/release/dump-cst-$rust_lang" < "$out/files.txt" > "$out/rust.txt"
python3 - "$out" <<'PY'
import sys, os
out = sys.argv[1]
import re
TOKEN = re.compile(r'^(\s*)(\S+)@(\d+)\.\.(\d+) "(.*)"$')
def normalize(body):
    # Intentional difference: Go may split one multi-byte character into
    # byte-sized tokens rendered as U+FFFD; Rust keeps characters whole.
    # Merge such runs and blank the text of non-ASCII-only tokens.
    lines, outl = body.split('\n'), []
    for line in lines:
        m = TOKEN.match(line)
        if m and (m.group(5).startswith('\\u{fffd}') or (m.group(5) and all(ord(c) > 127 for c in m.group(5)))):
            ind, kind, a, b = m.group(1), m.group(2), m.group(3), m.group(4)
            if outl and isinstance(outl[-1], tuple) and outl[-1][0] == ind and outl[-1][1] == kind and outl[-1][3] == a:
                outl[-1] = (ind, kind, outl[-1][2], b)
                continue
            outl.append((ind, kind, a, b))
            continue
        outl.append(line)
    return '\n'.join(f'{x[0]}{x[1]}@{x[2]}..{x[3]} "<non-ascii>"' if isinstance(x, tuple) else x for x in outl)
def stream(path):
    # Yields (name, body) records without loading the whole dump.
    name, body = None, []
    with open(path, encoding='utf-8', errors='replace') as fh:
        for line in fh:
            if line.startswith('=== '):
                if name is not None:
                    yield name, ''.join(body).rstrip('\n')
                name, body = line[4:].rstrip('\n'), []
            else:
                body.append(line)
    if name is not None:
        yield name, ''.join(body).rstrip('\n')
def clean(v):
    # Go's debug printer escapes a genuine U+FFFD as \u{fffd}; Rust prints it.
    # Go only approximates Rust's {:?} printable table, so decode \u{..}
    # escapes on both sides; tree shape and ranges are still compared.
    return UESC.sub(lambda m: chr(int(m.group(1), 16)), normalize(v).replace('\\u{fffd}', '\ufffd'))
UESC = re.compile(r'\\u\{([0-9a-f]+)\}')
os.makedirs(f'{out}/diffs', exist_ok=True)
import difflib, itertools
total, diff = 0, []
for (gn, gb), (rn, rb) in itertools.zip_longest(stream(f'{out}/go.txt'), stream(f'{out}/rust.txt'), fillvalue=(None, '')):
    total += 1
    gb, rb = clean(gb), clean(rb)
    if gn != rn or gb != rb:
        diff.append(gn or rn)
        if len(diff) <= 50:
            with open(f'{out}/diffs/' + (gn or rn).strip('/').replace('/', '__') + '.diff', 'w') as fh:
                fh.writelines(difflib.unified_diff(gb.splitlines(True), rb.splitlines(True), 'go', 'rust', n=3))
go = range(total)
print(f'{len(go)} files, {len(diff)} differ; diffs in {out}/diffs')
for n in diff[:10]: print('  ', n)
sys.exit(1 if diff else 0)
PY
