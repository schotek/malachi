# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
"""Tests for check-strings.py.

Run from the repository root:   python3 -m unittest macos/scripts/test_check_strings.py
or all script tests:            python3 -m unittest discover -s macos/scripts -p 'test_*.py'
"""

import contextlib
import importlib.util
import io
import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

_spec = importlib.util.spec_from_file_location("check_strings", os.path.join(HERE, "check-strings.py"))
cs = importlib.util.module_from_spec(_spec)
sys.modules["check_strings"] = cs  # dataclasses resolve annotations through sys.modules
_spec.loader.exec_module(cs)

REPO = os.path.abspath(os.path.join(HERE, "..", ".."))
POT_PATH = os.path.join(REPO, "po", "malachi.pot")

POT = '''msgid ""
msgstr ""
"Content-Type: text/plain; charset=UTF-8\\n"
"Plural-Forms: nplurals=INTEGER; plural=EXPRESSION;\\n"

msgid "Connected"
msgstr ""

msgid "Connected to malachid %s (pid %d)"
msgstr ""

msgid "Line\\nbreak \\"quoted\\" back\\\\slash"
msgstr ""

msgid "Sending…"
msgstr ""

msgid "_Next"
msgstr ""

msgid "Trash"
msgstr ""

msgctxt "folder"
msgid "Inbox"
msgstr ""

msgid "%d message"
msgid_plural "%d messages"
msgstr[0] ""
msgstr[1] ""

msgid "%d unsafe element was removed from the message"
msgid_plural "%d unsafe elements were removed from the message"
msgstr[0] ""
msgstr[1] ""

#~ msgid "Gone"
#~ msgstr ""
'''


class Fixture:
    """A temporary tree with a .pot and Swift files, scanned with run()."""

    def __init__(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = self.tmp.name
        self.pot = os.path.join(self.root, "x.pot")
        with open(self.pot, "w", encoding="utf-8") as f:
            f.write(POT)
        self.src = os.path.join(self.root, "Sources")
        os.makedirs(self.src)

    def add(self, name, text):
        path = os.path.join(self.src, name)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def run(self, **kw):
        return cs.run(self.root, self.pot, [self.src], **kw)

    def lines(self, kind, **kw):
        return [(os.path.basename(f.path), f.line, f.message) for f in self.run(**kw).findings if f.kind == kind]

    def close(self):
        self.tmp.cleanup()


class LexerTests(unittest.TestCase):
    def strings(self, src):
        toks, _ = cs.lex(src)
        return [(t.text, t.line, t.interpolated) for t in toks if t.kind == "str"]

    def test_escapes(self):
        self.assertEqual(self.strings(r'let s = "a\"b\\c\nd\te\u{2026}f\u{4}g"'), [('a"b\\c\nd\te…f\u0004g', 1, False)])

    def test_interpolation_is_flagged(self):
        self.assertEqual(self.strings(r'"Hi \(name.uppercased(")")) there"'), [("Hi \\(…) there", 1, True)])

    def test_multiline_string(self):
        src = 'let s = """\n    line one\n      indented \\\n    joined "q"\n    """\nlet t = "after"\n'
        self.assertEqual(self.strings(src), [('line one\n  indented joined "q"', 1, False), ("after", 6, False)])

    def test_raw_string(self):
        self.assertEqual(self.strings(r'let r = #"back\slash "quoted" \#n"#'), [('back\\slash "quoted" \n', 1, False)])

    def test_comments_are_not_code(self):
        toks, comments = cs.lex('// "not a string"\n/* "nor\n this" */ let x = "yes" // trailing\n')
        self.assertEqual([t.text for t in toks if t.kind == "str"], ["yes"])
        self.assertEqual([(c.line, c.code_before) for c in comments], [(1, False), (2, False), (3, True)])

    def test_operators_and_idents(self):
        toks, _ = cs.lex('x?.title == "a"; `default` = #selector(f(_:))')
        kinds = [(t.kind, t.text) for t in toks[:6]]
        self.assertEqual(kinds, [("ident", "x"), ("op", "?"), ("punct", "."), ("ident", "title"), ("op", "=="), ("str", "a")])
        self.assertIn(("ident", "default"), [(t.kind, t.text) for t in toks])
        self.assertIn(("ident", "#selector"), [(t.kind, t.text) for t in toks])


class ShimCallTests(unittest.TestCase):
    def setUp(self):
        self.fx = Fixture()
        self.addCleanup(self.fx.close)

    def test_known_msgids_pass(self):
        self.fx.add("A.swift", '''import MalachiCore
let a = L10n.T("Connected")
let b = L10n.T("Connected to malachid %s (pid %d)", v, pid)
let c = L10n.T("Line\\nbreak \\"quoted\\" back\\\\slash")
let d = L10n.T("Sending\\u{2026}")
let e = L10n.N("%d message", "%d messages", n)
let f = L10n.N("%d unsafe element was removed from the message",
               "%d unsafe elements were removed from the message", n)
let g = L10n.C("folder", "Inbox")
''')
        rep = self.fx.run()
        self.assertEqual([str(f) for f in rep.findings], [])
        self.assertEqual(rep.calls, 7)

    def test_missing_msgids_are_reported(self):
        self.fx.add("B.swift", '''
let a = L10n.T("Nope")
let b = L10n.T("Gone")
let c = L10n.N("%d widget", "%d widgets", n)
let d = L10n.N("%d message", "%d messages!", n)
let e = L10n.N("Trash", "Trashes", n)
let f = L10n.C("folder", "Nope")
let g = L10n.C("menu", "Trash")
let h = L10n.T("Hi \\(name)")
''')
        self.assertEqual(self.fx.lines("missing"), [
            ("B.swift", 2, 'missing msgid "Nope"'),
            ("B.swift", 3, 'missing msgid "Gone"'),
            ("B.swift", 4, 'missing msgid "%d widget" (plural)'),
            ("B.swift", 5, 'plural form of "%d message" differs from the .pot: code has "%d messages!", the .pot has "%d messages"'),
            ("B.swift", 6, 'missing plural msgid "Trash" (the .pot has it as a singular entry)'),
            ("B.swift", 7, 'missing msgid "Nope" with context "folder"'),
            ("B.swift", 8, 'missing msgid "Trash" with context "menu" (the .pot has it without a context)'),
            ("B.swift", 9, "msgid of L10n.T is an interpolated literal"),
        ])

    def test_plural_used_with_t_is_a_warning(self):
        self.fx.add("C.swift", 'let a = L10n.T("%d message")\n')
        self.assertEqual(self.fx.lines("warning"), [("C.swift", 1, 'msgid "%d message" is a plural entry in the .pot; use L10n.N')])
        self.assertEqual(self.fx.lines("missing"), [])

    def test_bare_calls_only_when_defined(self):
        # A file-local alias enables bare calls in that file only; a free
        # function forwarding to L10n.T is a wrapper for the whole module.
        self.fx.add("D.swift", 'let T = L10n.T\nlet a = T("Nope")\nlet b = other.T("Ignored")\n')
        self.fx.add("E.swift", 'let a = T("Not the shim")\n')
        self.assertEqual(self.fx.lines("missing"), [("D.swift", 2, 'missing msgid "Nope"')])
        self.fx.add("F.swift", 'func T(_ s: String) -> String { L10n.T(s) }\n')
        self.assertEqual(self.fx.lines("missing"), [
            ("D.swift", 2, 'missing msgid "Nope"'),
            ("E.swift", 1, 'missing msgid "Not the shim" (via T)'),
        ])

    def test_wrapper_is_detected(self):
        self.fx.add("W.swift", '''func wizardLabel(_ msgid: String) -> String {
    let s = L10n.T(msgid)
    return s.replacingOccurrences(of: "_", with: "")
}
button.title = wizardLabel("_Next")
button.title = wizardLabel("_Back")
''')
        rep = self.fx.run()
        self.assertEqual([(f.kind, f.line, f.message) for f in rep.findings], [("missing", 6, 'missing msgid "_Back" (via wizardLabel)')])
        self.assertEqual(rep.non_literal, 0)
        self.assertEqual(rep.calls, 3)  # the forwarding call inside the wrapper counts too

    def test_explicit_wrapper(self):
        self.fx.add("X.swift", 'let a = tr("Nope")\n')
        self.assertEqual(self.fx.lines("missing"), [])
        self.assertEqual(self.fx.lines("missing", extra_wrappers=["tr"]), [("X.swift", 1, 'missing msgid "Nope" (via tr)')])

    def test_non_literal_msgid_is_a_note(self):
        self.fx.add("N.swift", 'let a = L10n.T(key)\nlet b = L10n.T(flag ? "Connected" : "Trash")\n')
        rep = self.fx.run()
        self.assertEqual(rep.non_literal, 2)
        self.assertEqual([f.kind for f in rep.findings], ["note", "note"])

    def test_literal_argument_of_t_is_untranslated(self):
        self.fx.add("F.swift", 'toast(L10n.T("Connected to malachid %s (pid %d)", "dev build", 1))\n')
        self.assertEqual(self.fx.lines("warning"), [
            ("F.swift", 1, 'literal argument "dev build" of L10n.T("Connected to malachid %s (pid %d)", …) is untranslated text'),
        ])


class SinkTests(unittest.TestCase):
    def setUp(self):
        self.fx = Fixture()
        self.addCleanup(self.fx.close)

    def test_unwrapped_literals_in_sinks(self):
        self.fx.add("S.swift", '''
let i = NSMenuItem(title: "Open", action: nil, keyEquivalent: "o")
let b = NSButton(title: "Cancel", target: nil, action: nil)
menu.addItem(withTitle: "Quit", action: nil, keyEquivalent: "q")
field.stringValue = "Hello"
field.placeholderString = "Name"
view.toolTip = "Tip"
alert.messageText = "Sure?"
alert.informativeText = "Really."
alert.addButton(withTitle: "OK")
let l = NSTextField(labelWithString: "Label")
group = Group(title: "Keyboard", confirmLabel: "Remove")
popup.addItems(withTitles: ["One", "Two"])
static let windowTitle = "Settings"
''')
        got = self.fx.lines("warning")
        self.assertEqual([m for _, _, m in got], [
            'unwrapped literal "Open" passed to title:',
            'unwrapped literal "Cancel" passed to title:',
            'unwrapped literal "Quit" passed to withTitle:',
            'unwrapped literal "Hello" passed to stringValue =',
            'unwrapped literal "Name" passed to placeholderString =',
            'unwrapped literal "Tip" passed to toolTip =',
            'unwrapped literal "Sure?" passed to messageText =',
            'unwrapped literal "Really." passed to informativeText =',
            'unwrapped literal "OK" passed to withTitle:',
            'unwrapped literal "Label" passed to labelWithString:',
            'unwrapped literal "Keyboard" passed to title:',
            'unwrapped literal "Remove" passed to confirmLabel:',
            'unwrapped literal "One" passed to withTitles: [',
            'unwrapped literal "Two" passed to withTitles: [',
            'unwrapped literal "Settings" passed to windowTitle =',
        ])
        self.assertEqual([ln for _, ln, _ in got], [2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 12, 13, 13, 14])

    def test_wrapped_and_non_prose_literals_are_fine(self):
        self.fx.add("G.swift", '''
let i = NSMenuItem(title: L10n.T("Connected"), action: nil, keyEquivalent: "o")
field.stringValue = mn(L10n.T("Trash"))
view.toolTip = ""
label.title = "%d"
queue = DispatchQueue(label: "io.github.schotek.Malachi.rpc")
key.title = "group-by-conversation"
static let newMessage = "notify.newMessage"
let e = DecodingError.Context(codingPath: [], debugDescription: "not a JSON value")
if x == "Connected" { }
''')
        self.assertEqual(self.fx.lines("warning"), [])
        self.assertEqual(self.fx.lines("missing"), [])

    def test_helper_with_title_parameter_is_a_sink(self):
        self.fx.add("H.swift", '''
private static func item(
    _ title: String, _ action: Selector?, key: String = ""
) -> NSMenuItem { NSMenuItem(title: title, action: action, keyEquivalent: key) }
m.addItem(item("Undo", nil, key: "z"))
m.addItem(item(L10n.T("Connected"), nil))
m.addItem(other.item("Not ours", nil))
''')
        self.assertEqual(self.fx.lines("warning"), [("H.swift", 5, 'unwrapped literal "Undo" passed to item(')])

    def test_no_sinks_option(self):
        self.fx.add("O.swift", 'view.toolTip = "Tip"\nlet a = L10n.T("Nope")\n')
        self.assertEqual(self.fx.lines("warning", check_sinks=False), [])
        self.assertEqual(len(self.fx.lines("missing", check_sinks=False)), 1)


class MarkerTests(unittest.TestCase):
    def setUp(self):
        self.fx = Fixture()
        self.addCleanup(self.fx.close)

    def test_same_line_marker(self):
        self.fx.add("M.swift", '''
view.toolTip = "Tip" // macOS-only string
let a = L10n.T("Nope") // macOS-only string (an English key)
view.toolTip = "Unmarked"
''')
        self.assertEqual(self.fx.lines("warning"), [("M.swift", 4, 'unwrapped literal "Unmarked" passed to toolTip =')])
        self.assertEqual(self.fx.lines("missing"), [])
        self.assertEqual(self.fx.lines("macos-only"), [
            ("M.swift", 2, 'unwrapped literal "Tip" passed to toolTip ='),
            ("M.swift", 3, 'missing msgid "Nope"'),
        ])

    def test_marked_literal_with_an_existing_msgid(self):
        self.fx.add("E.swift", '''
view.toolTip = "Trash" // macOS-only string
button.title = "Next" // macOS-only string
view.toolTip = "Nowhere" // macOS-only string
''')
        self.assertEqual(self.fx.lines("warning"), [
            ("E.swift", 2, 'macOS-only literal "Trash" has a msgid in the .pot; use L10n.T("Trash")'),
            ("E.swift", 3, 'macOS-only literal "Next" has a msgid in the .pot; use mn(L10n.T("_Next"))'),
        ])
        self.assertEqual([ln for _, ln, _ in self.fx.lines("macos-only")], [2, 3, 4])

    def test_previous_line_marker_covers_the_next_statement(self):
        self.fx.add("P.swift", '''
// macOS-only string
let page = Page(
    title: "Sign-in not available",
    description: "Not on macOS yet.")
view.toolTip = "Unmarked"
''')
        self.assertEqual([ln for _, ln, _ in self.fx.lines("macos-only")], [4, 5])
        self.assertEqual([ln for _, ln, _ in self.fx.lines("warning")], [6])

    def test_same_line_marker_at_the_head_of_a_call(self):
        self.fx.add("Q.swift", '''
popup.addItems(withTitles: [ // macOS-only strings
    "One",
    "Two",
])
view.toolTip = "Unmarked"
''')
        self.assertEqual([ln for _, ln, _ in self.fx.lines("macos-only")], [3, 4])
        self.assertEqual([ln for _, ln, _ in self.fx.lines("warning")], [6])

    def test_plural_marker_covers_the_enclosing_block(self):
        self.fx.add("R.swift", '''
func editMenu() -> NSMenu {
    // All macOS-only strings: the standard Edit menu.
    let m = NSMenu(title: "Edit")
    m.addItem(NSMenuItem(title: "Undo", action: nil, keyEquivalent: "z"))

    let sub = NSMenu(title: "Spelling")
    return m
}
func other() {
    view.toolTip = "Unmarked"
}
''')
        self.assertEqual([ln for _, ln, _ in self.fx.lines("macos-only")], [4, 5, 7])
        self.assertEqual([ln for _, ln, _ in self.fx.lines("warning")], [11])


class CliTests(unittest.TestCase):
    def setUp(self):
        self.fx = Fixture()
        self.addCleanup(self.fx.close)

    def run_cli(self, *extra):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = cs.main(["--root", self.fx.root, "--pot", self.fx.pot, *extra, self.fx.src])
        return code, out.getvalue().splitlines(), err.getvalue().strip()

    def test_exit_codes_and_output(self):
        self.fx.add("Sub/A.swift", 'let a = L10n.T("Connected")\nview.toolTip = "Tip" // macOS-only string\nview.toolTip = "Warn"\nlet k = L10n.T(key)\n')
        code, out, err = self.run_cli()
        self.assertEqual(code, 0)
        self.assertEqual(out, ['Sources/Sub/A.swift:3: warning: unwrapped literal "Warn" passed to toolTip ='])
        self.assertEqual(err, "check-strings: 1 files, 2 shim calls, 0 missing, 1 warnings, 1 macOS-only, 1 non-literal msgids")
        code, out, _ = self.run_cli("--strict")
        self.assertEqual(code, 1)
        code, out, _ = self.run_cli("--allow-macos-only", "--notes")
        self.assertEqual(code, 0)
        self.assertEqual(out, [
            'Sources/Sub/A.swift:2: macOS-only: unwrapped literal "Tip" passed to toolTip =',
            'Sources/Sub/A.swift:3: warning: unwrapped literal "Warn" passed to toolTip =',
            "Sources/Sub/A.swift:4: note: msgid of L10n.T is not a literal",
        ])
        code, out, err = self.run_cli("--no-sinks")
        self.assertEqual((code, out), (0, []))
        self.assertIn("0 warnings, 0 macOS-only", err)

    def test_missing_msgid_fails(self):
        self.fx.add("B.swift", 'let a = L10n.T("Nope")\n')
        code, out, _ = self.run_cli()
        self.assertEqual(code, 1)
        self.assertEqual(out, ['Sources/B.swift:1: missing msgid "Nope"'])

    def test_bad_pot_is_an_error(self):
        with open(self.fx.pot, "w", encoding="utf-8") as f:
            f.write('msgid "bad \\q"\nmsgstr ""\n')
        code, _, err = self.run_cli()
        self.assertEqual(code, 2)
        self.assertTrue(err.startswith("check-strings: "))


@unittest.skipUnless(os.path.exists(POT_PATH), "po/ not found")
class RealTreeTests(unittest.TestCase):
    def test_tree_has_no_missing_msgids(self):
        paths = [os.path.join(REPO, p) for p in cs.DEFAULT_SOURCES if os.path.isdir(os.path.join(REPO, p))]
        if not paths:
            self.skipTest("macos/Sources not found")
        rep = cs.run(REPO, POT_PATH, paths)
        self.assertEqual([str(f) for f in rep.findings if f.kind == "missing"], [])
        self.assertGreater(rep.calls, 0)


if __name__ == "__main__":
    unittest.main()
