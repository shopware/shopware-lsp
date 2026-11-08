#!/usr/bin/env python3
"""Compare two lsp_bench.py result files (baseline first, candidate second).

Prints index time, peak RSS, per (category, method) totals and p95, the
slowest candidate requests relative to the baseline, and response-hash
mismatches. `--fail-slower RATIO` exits non-zero when any (category, method)
total in the candidate exceeds RATIO times the baseline (ignoring groups
whose baseline total is under `--min-ms`).
"""

from __future__ import annotations

import argparse
import json
import sys
from collections import defaultdict


def percentile(values: list[float], fraction: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    return ordered[min(len(ordered) - 1, int(len(ordered) * fraction))]


def load(path: str) -> dict:
    with open(path) as handle:
        return json.load(handle)


def key(record: dict) -> tuple:
    return (record["file"], record["method"], tuple(record["pos"]) if record["pos"] else None)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("baseline")
    parser.add_argument("candidate")
    parser.add_argument("--top", type=int, default=15)
    parser.add_argument("--fail-slower", type=float)
    parser.add_argument("--min-ms", type=float, default=50)
    parser.add_argument("--show-diffs", type=int, default=10)
    args = parser.parse_args()

    base, cand = load(args.baseline), load(args.candidate)
    print(f"index: {base['index_seconds']:.1f}s -> {cand['index_seconds']:.1f}s")
    print(f"RSS after index: {base['rss_after_index_kib'] / 1024:.0f} -> {cand['rss_after_index_kib'] / 1024:.0f} MiB")
    print(f"peak RSS: {base['peak_rss_kib'] / 1024:.0f} -> {cand['peak_rss_kib'] / 1024:.0f} MiB")

    groups: dict[tuple, list[list[float]]] = defaultdict(lambda: [[], []])
    base_by_key = {key(r): r for r in base["records"]}
    for record in base["records"]:
        groups[(record["category"], record["method"].removeprefix("textDocument/"))][0].append(record["ms"] or 0)
    for record in cand["records"]:
        groups[(record["category"], record["method"].removeprefix("textDocument/"))][1].append(record["ms"] or 0)

    print(f"\n{'category':16} {'method':22} {'base total':>11} {'cand total':>11} {'ratio':>6} {'base p95':>9} {'cand p95':>9}")
    failures = []
    grand = [0.0, 0.0]
    for group in sorted(groups):
        b, c = groups[group]
        bt, ct = sum(b), sum(c)
        grand[0] += bt
        grand[1] += ct
        ratio = ct / bt if bt else float("inf") if ct else 1.0
        flag = ""
        if args.fail_slower and bt >= args.min_ms and ratio > args.fail_slower:
            failures.append(group)
            flag = "  <-- slower"
        print(f"{group[0]:16} {group[1]:22} {bt:10.0f}ms {ct:10.0f}ms {ratio:6.2f} {percentile(b, .95):8.1f} {percentile(c, .95):8.1f}{flag}")
    print(f"{'TOTAL':39} {grand[0]:10.0f}ms {grand[1]:10.0f}ms {grand[1] / max(grand[0], 1e-9):6.2f}")

    slow = []
    diffs = []
    for record in cand["records"]:
        other = base_by_key.get(key(record))
        if other is None:
            continue
        if record["hash"] != other["hash"]:
            diffs.append((record, other))
        slow.append(((record["ms"] or 1e9) - (other["ms"] or 0), record, other))
    slow.sort(key=lambda item: item[0], reverse=True)
    print(f"\nlargest candidate regressions (top {args.top}):")
    for delta, record, other in slow[: args.top]:
        if delta <= 0:
            break
        print(f"  +{delta:8.1f}ms  {record['method'].removeprefix('textDocument/'):18} {record['file']} {record['pos'] or ''}"
              f"  ({other['ms'] or 0:.1f} -> {record['ms'] or 0:.1f})")
    print(f"\nresponse mismatches: {len(diffs)} of {len(cand['records'])}")
    by_method = defaultdict(int)
    for record, _ in diffs:
        by_method[record["method"]] += 1
    for method, count in sorted(by_method.items(), key=lambda item: -item[1]):
        print(f"  {count:5}  {method}")
    for record, _ in diffs[: args.show_diffs]:
        print(f"    {record['method']} {record['file']} {record['pos'] or ''}")
    if failures:
        print(f"\n{len(failures)} group(s) slower than {args.fail_slower}x baseline", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
