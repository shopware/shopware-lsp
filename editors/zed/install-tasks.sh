#!/usr/bin/env bash
# Install the Shopware task definitions into Zed's user-level config.
#
# The tasks need nothing project-specific. Zed sets ZED_WORKTREE_ROOT per
# window and sw-action.py defaults its --root to that, so one definition in
# ~/.config/zed/tasks.json serves every project you open.
#
# The tasks point at sw-action.py *inside this checkout* rather than a copy, so
# `git pull` here updates every project at once. That relies on the checkout
# staying put, which the README already requires: Zed loads the dev extension
# from this directory and keeps reading it from there.
#
# The alternative is the per-project layout in the README, copying the script
# and tasks into <project>/.zed/. Scoped to one project, at the cost of a copy
# that silently goes stale -- .zed/ is gitignored in shopware/shopware, so
# nothing reminds you it drifted.
#
#   ./install-tasks.sh            # tasks into ~/.config/zed/tasks.json
#   ./install-tasks.sh --keymap   # also merge the key bindings
#   ./install-tasks.sh --print    # show what would change, write nothing
#   DEST=/somewhere/tasks.json ./install-tasks.sh
#
# Re-running updates in place: entries labelled "Shopware: ..." are replaced,
# anything else in the file is left alone.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG="${ZED_CONFIG_DIR:-$HOME/.config/zed}"
DEST="${DEST:-$CONFIG/tasks.json}"
KEYMAP_DEST="${KEYMAP_DEST:-$CONFIG/keymap.json}"
PRINT=0
WITH_KEYMAP=0

while [ $# -gt 0 ]; do
  case "$1" in
    --print) PRINT=1 ;;
    --keymap) WITH_KEYMAP=1 ;;
    # BSD sed has no \? , so keep the class portable.
    -h|--help) sed -n '2,24p' "${BASH_SOURCE[0]}" | sed 's/^#[[:space:]]*//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

command -v python3 >/dev/null || { echo "python3 not found" >&2; exit 1; }
[ -f "$HERE/scripts/sw-action.py" ] || { echo "no scripts/sw-action.py next to this script" >&2; exit 1; }
[ -f "$HERE/examples/tasks.json" ] || { echo "no examples/tasks.json next to this script" >&2; exit 1; }

HERE="$HERE" DEST="$DEST" KEYMAP_DEST="$KEYMAP_DEST" \
PRINT="$PRINT" WITH_KEYMAP="$WITH_KEYMAP" python3 - <<'PY'
import json
import os
import re
import shutil
import sys
import time

here = os.environ["HERE"]
dest = os.environ["DEST"]
keymap_dest = os.environ["KEYMAP_DEST"]
dry_run = os.environ["PRINT"] == "1"
with_keymap = os.environ["WITH_KEYMAP"] == "1"

script = os.path.join(here, "scripts", "sw-action.py")
PREFIX = "Shopware:"
PLACEHOLDER = "$ZED_WORKTREE_ROOT/.zed/sw-action.py"


def load_jsonc(path):
    """Zed config files allow comments and trailing commas; json does not."""
    raw = open(path, encoding="utf-8").read()
    # Only strip // when it starts a line or follows whitespace, so URLs survive.
    raw = re.sub(r"(^|\s)//[^\n]*", r"\1", raw)
    raw = re.sub(r",(\s*[}\]])", r"\1", raw)
    return json.loads(raw)


def read_existing(path, kind):
    if not os.path.exists(path):
        return []
    try:
        existing = load_jsonc(path)
    except ValueError as error:
        sys.exit(
            f"cannot parse the existing {kind} at {path}: {error}\n"
            "fix or move it, then re-run; refusing to overwrite something "
            "that might not be what it looks like"
        )
    if not isinstance(existing, list):
        sys.exit(f"expected a JSON array in {path}, found {type(existing).__name__}")
    return existing


def write(path, payload, banner):
    body = "\n".join(f"// {line}" for line in banner) + "\n"
    body += json.dumps(payload, indent=2, ensure_ascii=False) + "\n"
    if dry_run:
        print(f"--- would write {path} ---")
        print(body if len(body) < 1200 else body[:1200] + "\n... truncated")
        return
    os.makedirs(os.path.dirname(path) or ".", exist_ok=True)
    if os.path.exists(path):
        backup = f"{path}.bak-{time.strftime('%Y%m%d-%H%M%S')}"
        shutil.copy2(path, backup)
        print(f"  backed up {os.path.basename(path)} -> {os.path.basename(backup)}")
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(body)
    # Re-read, so a malformed result is caught here rather than by Zed silently
    # ignoring the whole file.
    load_jsonc(path)


# --- tasks -------------------------------------------------------------------

tasks = load_jsonc(os.path.join(here, "examples", "tasks.json"))
for task in tasks:
    args = task.get("args") or []
    if not args or args[0] != PLACEHOLDER:
        sys.exit(
            f"examples/tasks.json entry {task.get('label')!r} does not start its "
            f"args with {PLACEHOLDER!r}; this installer rewrites that path and "
            "cannot tell what to do otherwise"
        )
    args[0] = script

kept = [t for t in read_existing(dest, "tasks.json") if not str(t.get("label", "")).startswith(PREFIX)]
print(f"tasks: {len(tasks)} Shopware entries, {len(kept)} of your own kept")
write(dest, kept + tasks, [
    "Shopware LSP tasks, installed by install-tasks.sh. Re-run it to update.",
    f"Entries labelled \"{PREFIX} ...\" are managed; anything else you add here is kept.",
    f"They run {script}",
])

# --- keymap ------------------------------------------------------------------

if with_keymap:
    def is_ours(binding):
        return PREFIX in json.dumps(binding)

    source = load_jsonc(os.path.join(here, "examples", "keymap.json"))
    existing = read_existing(keymap_dest, "keymap.json")

    # Drop only our bindings, keeping each block's other keys and any block
    # that has nothing of ours in it.
    cleaned = []
    for block in existing:
        bindings = {k: v for k, v in (block.get("bindings") or {}).items() if not is_ours(v)}
        if bindings or not any(is_ours(v) for v in (block.get("bindings") or {}).values()):
            cleaned.append({**block, "bindings": bindings})

    by_context = {b.get("context"): b for b in cleaned}
    added = 0
    for block in source:
        target = by_context.get(block.get("context"))
        if target is None:
            cleaned.append(block)
        else:
            target["bindings"].update(block.get("bindings") or {})
        added += len(block.get("bindings") or {})

    print(f"keymap: {added} Shopware bindings across {len(source)} contexts")
    write(keymap_dest, cleaned, [
        "Shopware LSP key bindings, merged by install-tasks.sh --keymap.",
        "Bindings naming a \"Shopware: ...\" task are managed; your own are kept.",
    ])

print()
print("Reload the Zed window, then `task: spawn` and type \"sho\".")
if dry_run:
    print("(nothing was written: --print)")
PY
