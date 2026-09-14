#!/usr/bin/env python3
"""Unit tests for the pure helpers in sw-action.py, and for examples/.

Everything here runs offline: no server binary, no network. The server-backed
actions are covered by contract-check.py instead.

    python3 scripts/test-sw-action.py
"""

import argparse
import contextlib
import importlib.util
import io
import json
import os
import pathlib
import re
import shutil
import sys
import tempfile
import unittest
from unittest import mock

ROOT = pathlib.Path(__file__).resolve().parent.parent

# The module name is the dotted file path on purpose: mutmut derives a
# mutant's key the same way, and a mismatch aborts the run with "tests
# recorded trampoline hits but none match any mutant key".
MODULE = "scripts.sw-action"
spec = importlib.util.spec_from_file_location(MODULE, ROOT / "scripts" / "sw-action.py")
sw = importlib.util.module_from_spec(spec)
sys.modules[MODULE] = sw
spec.loader.exec_module(sw)


def load_jsonc(path):
    """Zed accepts // comments in its config files; json does not."""
    return json.loads(re.sub(r"^\s*//.*$", "", (ROOT / path).read_text(), flags=re.M))


class UriRoundTrip(unittest.TestCase):
    """urlparse is wrong here: it splits on # and ?, which are legal in paths."""

    def test_path_with_fragment_and_query_characters(self):
        path = "/tmp/a b/c#d?e/Resources/views/base.html.twig"
        self.assertEqual(sw.uri_to_path(sw.path_to_uri(path)), path)

    def test_non_ascii_path(self):
        path = "/tmp/größe/übersicht.twig"
        self.assertEqual(sw.uri_to_path(sw.path_to_uri(path)), path)

    def test_slashes_stay_unescaped(self):
        self.assertEqual(sw.path_to_uri("/a/b"), "file:///a/b")

    def test_something_that_is_not_a_file_uri_passes_through(self):
        self.assertEqual(sw.uri_to_path("/plain/path"), "/plain/path")

    def test_a_windows_drive_loses_the_uri_leading_slash(self):
        self.assertEqual(sw.uri_to_path("file:///C:/Users/x/a.twig"), "C:/Users/x/a.twig")
        self.assertEqual(sw.uri_to_path("file:///C%3A/Users/x/a.twig"), "C:/Users/x/a.twig")

    def test_a_windows_path_becomes_a_drive_uri(self):
        # abspath is mocked because this branch is unreachable off Windows.
        # The colon percent-encodes, which is what VS Code sends too.
        with mock.patch.object(sw.os.path, "abspath", return_value="C:\\Users\\x\\a.twig"):
            self.assertEqual(sw.path_to_uri("ignored"), "file:///C%3A/Users/x/a.twig")

    def test_an_authority_component_is_skipped(self):
        # Local file URIs have an empty authority, but a UNC-style one would
        # otherwise be read as part of the path.
        self.assertEqual(sw.uri_to_path("file://server/share/a.twig"), "/share/a.twig")

    def test_an_authority_with_no_path_yields_the_root(self):
        self.assertEqual(sw.uri_to_path("file://host"), "/")


class Utf16Columns(unittest.TestCase):
    """LSP counts UTF-16 code units; Python counts characters. Emoji differ."""

    LINE = '<div data-id="😀"></div>'

    def test_ascii_prefix_is_one_to_one(self):
        self.assertEqual(sw.utf16_to_index(self.LINE, 5), 5)
        self.assertEqual(sw.index_to_utf16(self.LINE, 5), 5)

    def test_astral_character_counts_as_two_units(self):
        emoji = self.LINE.index("😀")
        self.assertEqual(sw.index_to_utf16(self.LINE, emoji + 1), emoji + 2)

    def test_round_trip_across_the_astral_character(self):
        for index in range(len(self.LINE) + 1):
            units = sw.index_to_utf16(self.LINE, index)
            self.assertEqual(sw.utf16_to_index(self.LINE, units), index)

    def test_the_last_bmp_character_counts_as_one_unit(self):
        # U+FFFF is the boundary: still one UTF-16 unit, so `>= 0xFFFF` is wrong.
        text = "a\uffffb"
        self.assertEqual(sw.index_to_utf16(text, 2), 2)
        # Offset 3, not 2: at 2 both the correct and the off-by-one version
        # answer 2, so it proves nothing.
        self.assertEqual(sw.utf16_to_index(text, 3), 3)

    def test_the_first_astral_character_counts_as_two_units(self):
        # U+10000 is the other side of it, so `> 65536` is wrong too.
        text = "a\U00010000b"
        self.assertEqual(sw.index_to_utf16(text, 2), 3)
        self.assertEqual(sw.utf16_to_index(text, 3), 2)

    def test_a_unit_inside_the_surrogate_pair_does_not_split_it(self):
        emoji = self.LINE.index("😀")
        inside = sw.index_to_utf16(self.LINE, emoji) + 1
        # Snapping forward keeps the pair intact; splitting it would write a
        # lone surrogate into the file.
        self.assertEqual(sw.utf16_to_index(self.LINE, inside), emoji + 1)


class ByteColumns(unittest.TestCase):
    """$ZED_COLUMN is a UTF-8 byte offset, which is not the UTF-16 one above."""

    LINE = '<div data-id="😀"></div>'

    def test_ascii_prefix_is_one_to_one(self):
        self.assertEqual(sw.byte_column_to_index(self.LINE, 5), 5)

    def test_the_two_encodings_disagree_after_an_astral_character(self):
        emoji = self.LINE.index("😀")
        after = emoji + 1
        # 4 bytes but 2 UTF-16 units: reading one as the other lands 2 short.
        self.assertEqual(sw.byte_column_to_index(self.LINE, emoji + 4), after)
        self.assertEqual(sw.utf16_to_index(self.LINE, emoji + 4), after + 2)

    def test_round_trip_over_every_character_boundary(self):
        for index in range(len(self.LINE) + 1):
            offset = len(self.LINE[:index].encode("utf-8"))
            self.assertEqual(sw.byte_column_to_index(self.LINE, offset), index)

    def test_offset_inside_a_sequence_snaps_forward(self):
        emoji = self.LINE.index("😀")
        start = len(self.LINE[:emoji].encode("utf-8"))
        for inside in range(start + 1, start + 4):
            self.assertEqual(sw.byte_column_to_index(self.LINE, inside), emoji + 1)

    def test_the_first_astral_character_is_four_bytes(self):
        self.assertEqual(sw.byte_column_to_index("a\U00010000b", 5), 2)

    def test_the_last_bmp_character_is_three_bytes(self):
        self.assertEqual(sw.byte_column_to_index("a\uffffb", 4), 2)

    def test_offsets_out_of_range_clamp(self):
        self.assertEqual(sw.byte_column_to_index(self.LINE, -5), 0)
        self.assertEqual(sw.byte_column_to_index(self.LINE, 9999), len(self.LINE))


class Parity(unittest.TestCase):
    """inventory/parity.json against the script, and against the README.

    inventory.py covers the other direction, that upstream has not added a
    command the map ignores. That needs the server and the vsix; this does not.
    """

    def setUp(self):
        self.parity = json.loads((ROOT / "inventory" / "parity.json").read_text())
        self.groups = {key: self.parity[key] for key in ("palette", "clientCommands")}
        self.standalone = self.parity["standalone"]
        # The parity tables moved out of the README into their own doc when the
        # README was made user-facing. These assertions follow the tables, not
        # the filename: pointing them back at README.md would make every one of
        # them pass without checking anything.
        self.readme = (ROOT / "docs" / "vscode-parity.md").read_text()
        self.tasks_doc = (ROOT / "docs" / "tasks.md").read_text()
        # Prose wraps, so compare against a single-spaced form.
        self.prose = re.sub(r"\s+", " ", self.readme)
        self.tasks_prose = re.sub(r"\s+", " ", self.tasks_doc)

    def entries(self):
        for group, commands in self.groups.items():
            for command, entry in commands.items():
                yield group, command, entry

    def counts(self, group):
        """Full equivalents only. A partial one is not parity."""
        commands = self.groups[group]
        full = sum(1 for e in commands.values() if "action" in e and "partial" not in e)
        return full, len(commands)

    def full_actions(self):
        return {
            e["action"]
            for _, _, e in self.entries()
            if "action" in e and "partial" not in e
        }

    def test_every_entry_is_covered_partial_or_explained(self):
        allowed = [{"action"}, {"action", "partial"}, {"gap"}]
        for group, command, entry in self.entries():
            self.assertIn(set(entry), allowed, f"{group}: {command} -> {entry}")

    def test_every_named_action_exists(self):
        known = set(sw.action_names())
        for group, command, entry in self.entries():
            if "action" in entry:
                self.assertIn(entry["action"], known, f"{group}: {command}")
        for action in self.standalone:
            self.assertIn(action, known, f"standalone: {action}")

    def test_every_action_is_mapped_or_standalone(self):
        # An action does not have to correspond to an upstream command. It does
        # have to be accounted for, so that nothing is quietly unlisted and
        # nothing gets mapped to a loose match just to appear in the count.
        mapped = {e["action"] for _, _, e in self.entries() if "action" in e}
        for action in sw.action_names():
            self.assertTrue(
                action in mapped or action in self.standalone,
                f"{action} is neither mapped to an upstream command nor listed "
                "as standalone in parity.json",
            )

    def test_no_reason_is_empty(self):
        for group, command, entry in self.entries():
            for key in ("gap", "partial"):
                if key in entry:
                    self.assertTrue(entry[key].strip(), f"{group}: {command}")
        for action, reason in self.standalone.items():
            self.assertTrue(reason.strip(), action)

    def test_readme_states_the_counts_the_map_implies(self):
        # The stale-number bug this whole gate exists for.
        palette_covered, palette_total = self.counts("palette")
        client_covered, client_total = self.counts("clientCommands")
        sentence = (
            f"{palette_covered} of {palette_total} palette commands and "
            f"{client_covered} of {client_total} client commands have a full "
            "task equivalent"
        )
        self.assertIn(sentence, self.prose)

    def test_readme_comparison_rows_agree_with_the_map(self):
        palette_covered, palette_total = self.counts("palette")
        client_covered, client_total = self.counts("clientCommands")
        self.assertIn(
            f"| {palette_total} palette commands | no palette; "
            f"{palette_covered} full equivalents as tasks",
            self.readme,
        )
        self.assertIn(
            f"| {client_total} client commands behind code actions | "
            f"{client_covered} full equivalents as tasks",
            self.readme,
        )

    def test_the_tables_list_every_gap_and_every_partial(self):
        # Recorded but undocumented is invisible to a reader.
        for group, command, entry in self.entries():
            if "gap" not in entry and "partial" not in entry:
                continue
            short = command.rsplit(".", 1)[-1]
            self.assertIn(f"`{short}`", self.readme, f"{group}: {command}")

    def action_table(self):
        """Rows of the action table, which is not the only table of actions."""
        # This table lives with the tasks, not with the parity comparison.
        lines = self.tasks_doc.splitlines()
        header = lines.index("| Action | What it does | Status |")
        rows = []
        for line in lines[header + 2 :]:
            if not line.startswith("|"):
                break
            rows.append(line.split("|")[1].strip().strip("`"))
        return rows

    def test_every_mapped_action_is_reachable_from_a_task(self):
        # An action nobody can run is not an equivalent of anything.
        tasks = {task["args"][1] for task in load_jsonc("examples/tasks.json")}
        for group, command, entry in self.entries():
            if "action" in entry:
                self.assertIn(entry["action"], tasks, f"{group}: {command}")

    def test_a_mapped_action_is_not_also_standalone(self):
        for group, command, entry in self.entries():
            if "action" in entry:
                self.assertNotIn(entry["action"], self.standalone, f"{group}: {command}")

    def test_the_readme_carries_each_reason_verbatim(self):
        # The short command name alone would let the reason drift silently.
        for group, command, entry in self.entries():
            for key in ("gap", "partial"):
                if key in entry:
                    self.assertIn(entry[key], self.readme, f"{group}: {command}")
        for action, reason in self.standalone.items():
            self.assertIn(reason, self.readme, action)

    def test_the_action_table_lists_exactly_the_scripts_actions(self):
        self.assertCountEqual(self.action_table(), sw.action_names())

    def test_the_readme_states_how_many_tasks_expose_them(self):
        tasks = load_jsonc("examples/tasks.json")
        # Asserted against the tasks doc, which is where the claim belongs.
        # The parity doc happens to repeat the number, so checking there
        # would pass for the wrong reason.
        self.assertIn(f"{len(tasks)} tasks", self.tasks_prose)

    def test_distinct_actions_reconcile(self):
        # Three actions serve both lists; the README explains the arithmetic,
        # so it has to hold.
        palette_covered, _ = self.counts("palette")
        client_covered, _ = self.counts("clientCommands")
        shared = palette_covered + client_covered - len(self.full_actions())
        self.assertIn(f"{shared} actions serve both lists", self.prose)
        self.assertIn(
            f"{len(self.full_actions())} distinct actions", self.prose
        )
        self.assertIn(f"{len(sw.action_names())} in the action table", self.prose)


class ManagedDownloads(unittest.TestCase):
    """Picking the newest binary the extension downloaded for itself."""

    def path(self, version):
        return f"/x/shopware-lsp-{version}-darwin-arm64/extension/shopware-lsp"

    def test_version_is_parsed_into_numbers(self):
        self.assertEqual(sw.download_version(self.path("0.3.57")), (0, 3, 57))

    def test_newest_wins_by_number_not_by_string(self):
        names = [self.path(v) for v in ("0.3.9", "0.3.57", "0.3.100")]
        self.assertEqual(max(names, key=sw.download_version), self.path("0.3.100"))
        # What the old lexicographic sort did, and why this test exists:
        # upstream is past 0.3.57, so "0.3.9" would have shadowed every
        # release after it.
        self.assertEqual(sorted(names, reverse=True)[0], self.path("0.3.9"))

    def test_zed_managed_servers_returns_newest_first(self):
        # Exercises the sort where it is actually used. Asserting on
        # download_version alone passed even with the lexicographic sort
        # restored, so it pinned nothing.
        names = [self.path(v) for v in ("0.3.9", "0.3.100", "0.3.57")]
        with mock.patch("glob.glob", side_effect=[names, [], []]):
            self.assertEqual(sw.zed_managed_servers()[0], self.path("0.3.100"))

    def test_zed_managed_servers_ignores_files_that_are_not_the_binary(self):
        keep = self.path("0.1.0")
        noise = "/x/shopware-lsp-0.9.0-darwin-arm64/extension/shopware-lsp.sha256"
        with mock.patch("glob.glob", side_effect=[[noise, keep], [], []]):
            self.assertEqual(sw.zed_managed_servers(), [keep])

    def test_an_unparseable_directory_sorts_last_instead_of_raising(self):
        self.assertEqual(sw.download_version("/x/odd/extension/shopware-lsp"), (-1,))
        names = ["/x/odd/extension/shopware-lsp", self.path("0.0.1")]
        self.assertEqual(max(names, key=sw.download_version), self.path("0.0.1"))


class Picker(unittest.TestCase):
    """Labels come from server data and repeat, so picks resolve by position."""

    DUPLICATES = ["Service", "Service", "Other"]

    def numbered(self, options, answer, multi=False):
        with (
            mock.patch.object(sw.shutil, "which", return_value=None),
            mock.patch("builtins.input", return_value=answer),
            contextlib.redirect_stdout(io.StringIO()) as out,
        ):
            result = sw.choose_indexes(options, "thing", multi)
        return result, out.getvalue()

    def fzf(self, options, stdout, multi=False):
        recorded = {}

        def fake_run(args, **kwargs):
            recorded["args"] = args
            recorded["input"] = kwargs.get("input", "")
            return argparse.Namespace(stdout=stdout)

        with (
            mock.patch.object(sw.shutil, "which", return_value="/usr/bin/fzf"),
            mock.patch.object(sw.subprocess, "run", fake_run),
        ):
            return sw.choose_indexes(options, "thing", multi), recorded

    def test_numbered_prompt_resolves_the_second_duplicate(self):
        self.assertEqual(self.numbered(self.DUPLICATES, "2")[0], [1])

    def test_numbered_prompt_multi_keeps_both_duplicates(self):
        self.assertEqual(self.numbered(self.DUPLICATES, "1,2", multi=True)[0], [0, 1])

    def test_zero_is_rejected_rather_than_read_as_the_last_entry(self):
        with self.assertRaises(SystemExit):
            self.numbered(self.DUPLICATES, "0")

    def test_out_of_range_is_rejected(self):
        with self.assertRaises(SystemExit):
            self.numbered(self.DUPLICATES, "4")

    def test_fzf_resolves_by_ordinal_not_by_matching_text(self):
        # fzf echoes back the line it was given, ordinal included.
        indexes, _ = self.fzf(self.DUPLICATES, "1\tService\n")
        self.assertEqual(indexes, [1])

    def test_fzf_is_given_ordinals_and_told_to_hide_them(self):
        _, recorded = self.fzf(self.DUPLICATES, "0\tService\n")
        self.assertEqual(recorded["input"].splitlines()[1], "1\tService")
        self.assertIn("--with-nth", recorded["args"])
        self.assertIn("2..", recorded["args"])

    def test_fzf_multi_returns_every_position(self):
        indexes, _ = self.fzf(self.DUPLICATES, "0\tService\n2\tOther\n", multi=True)
        self.assertEqual(indexes, [0, 2])

    def test_an_out_of_range_ordinal_from_fzf_is_ignored(self):
        # fzf should never echo one, but accepting it would hand the caller an
        # index past the end and raise IndexError instead of a clean message.
        with self.assertRaises(SystemExit):
            self.fzf(["a", "b"], "2\tc\n")

    def test_empty_fzf_output_exits(self):
        with self.assertRaises(SystemExit):
            self.fzf(self.DUPLICATES, "\n")

    def test_choose_still_returns_the_text(self):
        with (
            mock.patch.object(sw.shutil, "which", return_value=None),
            mock.patch("builtins.input", return_value="3"),
            contextlib.redirect_stdout(io.StringIO()),
        ):
            self.assertEqual(sw.choose(self.DUPLICATES, "thing", False), ["Other"])


class PickLocation(unittest.TestCase):
    """Note what these do not cover: since the label now ends in `path:line`,
    it is unique whenever the entry is, so resolving by text would pass here
    too. The position-based resolution is pinned by `Picker` above, and it is
    what protects the three call sites whose labels are not self-identifying:
    form variables, snippet files and scaffolds."""

    ENTRIES = [("Service", "/root/a/One.php", 10), ("Service", "/root/b/Two.php", 20)]

    def pick(self, answer):
        opened = {}
        args = argparse.Namespace(root="/root", print_only=True)
        with (
            mock.patch.object(sw.shutil, "which", return_value=None),
            mock.patch("builtins.input", return_value=answer),
            mock.patch.object(
                sw,
                "open_location",
                lambda path, line, root, print_only: opened.update(path=path, line=line),
            ),
            contextlib.redirect_stdout(io.StringIO()) as out,
        ):
            sw.pick_location(list(self.ENTRIES), "service", args)
        return opened, out.getvalue()

    def test_identical_labels_open_the_one_that_was_chosen(self):
        opened, _ = self.pick("2")
        self.assertEqual(opened, {"path": "/root/b/Two.php", "line": 20})

    def test_the_list_shows_path_and_line_so_duplicates_are_distinguishable(self):
        _, listing = self.pick("1")
        self.assertIn("a/One.php:10", listing)
        self.assertIn("b/Two.php:20", listing)

    def test_a_zero_line_is_shown_as_line_one(self):
        opened = {}
        args = argparse.Namespace(root="/root", print_only=True)
        with (
            mock.patch.object(sw.shutil, "which", return_value=None),
            mock.patch("builtins.input", return_value="1"),
            mock.patch.object(sw, "open_location", lambda *a: opened.update(hit=True)),
            contextlib.redirect_stdout(io.StringIO()) as out,
        ):
            sw.pick_location([("Service", "/root/a.php", 0)], "service", args)
        self.assertIn("a.php:1", out.getvalue())

    def test_an_entry_without_a_location_is_reported_not_opened(self):
        opened = {}
        args = argparse.Namespace(root="/root", print_only=True)
        with (
            mock.patch.object(sw.shutil, "which", return_value=None),
            mock.patch("builtins.input", return_value="1"),
            mock.patch.object(
                sw, "open_location", lambda *a: opened.update(called=True)
            ),
            contextlib.redirect_stdout(io.StringIO()) as out,
        ):
            sw.pick_location([("Orphan", "", 0)], "service", args)
        self.assertEqual(opened, {})
        self.assertIn("no source location", out.getvalue())

    def test_no_entries_exits(self):
        args = argparse.Namespace(root="/root", print_only=True)
        with self.assertRaises(SystemExit):
            sw.pick_location([], "service", args)


class WriteGuard(unittest.TestCase):
    """checked_write_path / prepare_write refuse the two always-wrong shapes."""

    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.root)

    def test_rejects_an_unexpanded_tilde_component(self):
        # The shape that actually happened: a task passed $ZED_FILE unexpanded,
        # the server echoed the path back in a WorkspaceEdit, and makedirs
        # built <root>/~/Users/... and wrote there. Note this path is *inside*
        # the root, so the root check alone would let it through.
        phantom = os.path.join(self.root, "~", "Users", "me", "buffer.twig")
        with self.assertRaises(SystemExit) as caught:
            sw.prepare_write(phantom, self.root)
        self.assertIn("unexpanded", str(caught.exception))
        self.assertFalse(os.path.exists(os.path.join(self.root, "~")),
                         "must not create the phantom tree before refusing")

    def test_rejects_a_path_outside_the_root(self):
        outside = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, outside)
        with self.assertRaises(SystemExit) as caught:
            sw.prepare_write(os.path.join(outside, "escaped.php"), self.root)
        self.assertIn("outside the project root", str(caught.exception))

    def test_rejects_traversal_back_out_of_the_root(self):
        with self.assertRaises(SystemExit):
            sw.prepare_write(os.path.join(self.root, "..", "escaped.php"), self.root)

    def test_allows_a_path_inside_the_root_and_creates_parents(self):
        target = os.path.join(self.root, "src", "Core", "Thing.php")
        resolved = sw.prepare_write(target, self.root)
        self.assertEqual(resolved, os.path.realpath(target))
        self.assertTrue(os.path.isdir(os.path.dirname(resolved)))

    def test_a_sibling_prefix_is_not_inside_the_root(self):
        # os.path.commonpath, not startswith: "<root>-other" shares a string
        # prefix with "<root>" but is a different directory.
        sibling = self.root + "-other"
        os.makedirs(sibling)
        self.addCleanup(shutil.rmtree, sibling)
        with self.assertRaises(SystemExit):
            sw.prepare_write(os.path.join(sibling, "x.php"), self.root)


class InsertUuid(unittest.TestCase):
    """run_uuid end to end, against real files in a temporary directory."""

    HEX = r"[0-9a-f]{32}"

    def insert(self, contents, row, column=1, print_only=False):
        directory = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, directory)
        path = pathlib.Path(directory) / "buffer.twig"
        path.write_text(contents, encoding="utf-8")
        args = argparse.Namespace(
            target=str(path), row=row, column=column,
            print_only=print_only, root=directory,
        )
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            sw.run_uuid(args, None)
        return path.read_text(encoding="utf-8"), stdout.getvalue()

    def run_main(self, argv, home):
        """Drive main() rather than run_uuid, so argument handling is covered.

        The tilde bug lived in main(): every other test here builds a
        Namespace by hand and so could never see it. server_binary() runs
        before the uuid dispatch, hence SHOPWARE_LSP_BIN.
        """
        stdout = io.StringIO()
        with mock.patch.dict(
            os.environ, {"HOME": home, "SHOPWARE_LSP_BIN": sys.executable}
        ), mock.patch.object(sys, "argv", ["sw-action.py", *argv]):
            with contextlib.redirect_stdout(stdout):
                sw.main()
        return stdout.getvalue()

    def test_expands_a_tilde_in_the_target_path(self):
        # Zed passes $ZED_FILE as ~/... . os.path.abspath does not expand `~`,
        # so it joined the value onto the working directory and every task
        # taking $ZED_FILE died with "not a file: <worktree>/~/Users/...".
        directory = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, directory)
        target = pathlib.Path(directory) / "buffer.twig"
        target.write_text("hello\n", encoding="utf-8")

        output = self.run_main(["uuid", "~/buffer.twig", "1"], directory)

        self.assertRegex(target.read_text(encoding="utf-8"), r"^" + self.HEX + r"hello\n$")
        self.assertIn("inserted", output)

    def test_expands_a_tilde_in_the_root_path(self):
        # $ZED_WORKTREE_ROOT can be abbreviated the same way, and --root is
        # what every relative() call reports against.
        directory = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, directory)
        target = pathlib.Path(directory) / "buffer.twig"
        target.write_text("hello\n", encoding="utf-8")

        output = self.run_main(
            ["uuid", str(target), "1", "--root", "~"], directory
        )

        # relpath against an unexpanded "~" does not raise, it just walks out
        # of <cwd>/~ with a pile of "..", so assert the reported path is the
        # clean relative one. Merely checking for the absence of "~" passes
        # either way, which is how the first version of this test was useless.
        self.assertIn(" at buffer.twig:", output)
        self.assertNotIn("..", output)

    def test_inserts_on_the_final_blank_line(self):
        # splitlines drops the empty segment after a trailing newline. Without
        # it the row clamp walks back a line and appends to "hello" instead.
        result, _ = self.insert("hello\n", row=2)
        self.assertRegex(result, r"^hello\n" + self.HEX + r"$")

    def test_inserts_at_the_start_of_the_first_line(self):
        result, _ = self.insert("hello\n", row=1)
        self.assertRegex(result, r"^" + self.HEX + r"hello\n$")

    def test_inserts_mid_line_without_disturbing_the_rest(self):
        result, _ = self.insert("<span></span>\n", row=1, column=7)
        self.assertRegex(result, r"^<span>" + self.HEX + r"</span>\n$")

    def test_byte_column_lands_after_an_astral_character(self):
        # `<div data-id="` is 14 bytes, the emoji is 4, so Zed reports 19.
        result, _ = self.insert('<div data-id="😀"></div>\n', row=1, column=19)
        self.assertRegex(result, r'^<div data-id="😀' + self.HEX + r'"></div>\n$')

    def test_empty_file(self):
        result, _ = self.insert("", row=1)
        self.assertRegex(result, r"^" + self.HEX + r"$")

    def test_row_past_the_end_clamps_to_the_last_line(self):
        result, _ = self.insert("a\nb\n", row=99)
        self.assertRegex(result, r"^a\nb\n" + self.HEX + r"$")

    def test_column_past_the_end_clamps_to_the_line_length(self):
        result, _ = self.insert("ab\n", row=1, column=99)
        self.assertRegex(result, r"^ab" + self.HEX + r"\n$")

    def test_a_file_without_a_trailing_newline_keeps_its_last_line(self):
        result, _ = self.insert("hello", row=1, column=6)
        self.assertRegex(result, r"^hello" + self.HEX + r"$")

    def test_print_only_leaves_the_file_alone(self):
        result, output = self.insert("hello\n", row=2, print_only=True)
        self.assertEqual(result, "hello\n")
        self.assertIn("would insert", output)
        self.assertIn("buffer.twig:2:1", output)

    def test_reported_position_matches_where_it_landed(self):
        _, output = self.insert("hello\n", row=2)
        self.assertIn("buffer.twig:2:1", output)

    def test_without_a_file_it_prints_a_uuid(self):
        args = argparse.Namespace(
            target=None, row=1, column=1, print_only=False, root=os.getcwd()
        )
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            sw.run_uuid(args, None)
        self.assertRegex(stdout.getvalue().strip(), r"^" + self.HEX + r"$")

    def test_values_do_not_repeat(self):
        seen = {self.insert("\n", row=1)[0].strip() for _ in range(10)}
        self.assertEqual(len(seen), 10)


class Examples(unittest.TestCase):
    """The example config is the product here, so it is checked like code."""

    def setUp(self):
        self.tasks = load_jsonc("examples/tasks.json")
        self.keymap = load_jsonc("examples/keymap.json")
        self.labels = {task["label"] for task in self.tasks}

    def test_every_binding_names_a_task_that_exists(self):
        # Zed does nothing at all when a task_name does not match, with no
        # error anywhere, so a rename can break bindings invisibly.
        for group in self.keymap:
            for key, binding in group["bindings"].items():
                name = binding[1]["task_name"]
                self.assertIn(name, self.labels, f"{key} in {group['context']}")

    def test_labels_are_unique(self):
        self.assertEqual(len(self.labels), len(self.tasks))

    def test_labels_are_verb_first(self):
        verbs = {"go", "insert", "create", "show", "open", "rebuild"}
        for label in self.labels:
            self.assertTrue(label.startswith("Shopware: "), label)
            self.assertIn(label.split(": ", 1)[1].split()[0], verbs, label)

    def test_every_task_names_a_known_action(self):
        known = set(sw.action_names())
        for task in self.tasks:
            action = task["args"][1]
            self.assertIn(action, known, task["label"])

    def test_every_task_starts_its_args_with_the_script_placeholder(self):
        # install-tasks.sh rewrites args[0] to an absolute path in the
        # checkout, and refuses to guess if it finds anything else there. It
        # would fail at someone's install; fail here instead. args[1] being the
        # action, asserted above, depends on the same position.
        for task in self.tasks:
            self.assertEqual(
                task["args"][0], "$ZED_WORKTREE_ROOT/.zed/sw-action.py", task["label"]
            )

    def test_tasks_touching_a_file_declare_a_save_strategy(self):
        # sw-action.py reads the file from disk, so an unsaved buffer is stale.
        # Whether a task needs the flush depends on the action, which the test
        # cannot infer, so require the choice to be explicit and reviewable.
        for task in self.tasks:
            if "$ZED_FILE" in task["args"]:
                self.assertIn(task.get("save"), ("current", "none"), task["label"])

    def test_no_binding_is_used_twice_in_one_context(self):
        for group in self.keymap:
            keys = list(group["bindings"])
            self.assertEqual(len(keys), len(set(keys)), group["context"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
