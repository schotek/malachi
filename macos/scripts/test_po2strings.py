# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
"""Tests for po2strings.py.

Run from the repository root:   python3 -m unittest macos/scripts/test_po2strings.py
or from macos/:                 python3 -m unittest scripts/test_po2strings.py
"""

import contextlib
import io
import os
import plistlib
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import po2strings  # noqa: E402

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
POT = os.path.join(REPO, "po", "malachi.pot")
CS_PO = os.path.join(REPO, "po", "cs.po")

HEADER = '''msgid ""
msgstr ""
"Content-Type: text/plain; charset=UTF-8\\n"
"Plural-Forms: nplurals=3; plural=(n==1) ? 0 : (n>=2 && n<=4) ? 1 : 2;\\n"

'''


def parse(text):
    return po2strings.parse_po(text, "<test>")


def entries(text):
    return [e for e in parse(text).entries if not e.is_header]


class ParserTests(unittest.TestCase):
    def test_multiline_and_escapes(self):
        text = HEADER + '''#: a.go:1
msgid ""
"Line one\\n"
"Line \\"two\\" \\\\ back\\ttab"
msgstr ""
"Řádek jedna\\n"
"Řádek dva"
'''
        (e,) = entries(text)
        self.assertEqual(e.msgid, 'Line one\nLine "two" \\ back\ttab')
        self.assertEqual(e.msgstr, "Řádek jedna\nŘádek dva")

    def test_header_plural_forms(self):
        self.assertEqual(parse(HEADER).nplurals, 3)
        self.assertIsNone(parse('msgid ""\nmsgstr "Plural-Forms: nplurals=INTEGER; plural=EXPRESSION;\\n"\n').nplurals)

    def test_fuzzy_and_obsolete_are_skipped(self):
        text = HEADER + '''#, fuzzy
msgid "Fuzzy"
msgstr "Nejistý"

#~ msgid "Old"
#~ msgstr "Starý"

msgid "Kept"
msgstr "Zachován"
'''
        strings, plurals = po2strings.collect(parse(text), ["one", "few", "other"], False, "<test>")
        self.assertEqual(strings, {"Kept": "Zachován"})
        self.assertEqual(plurals, {})
        kinds = {e.msgid: (e.fuzzy, e.obsolete) for e in entries(text)}
        self.assertEqual(kinds, {"Fuzzy": (True, False), "Old": (False, True), "Kept": (False, False)})

    def test_obsolete_comment_forms_are_skipped(self):
        # msgmerge keeps the previous msgid and the flags of obsolete entries
        # as "#~|" and "#~," comments; an obsolete plural has "#~ msgstr[i]".
        text = HEADER + '''#~| msgid "Older"
#~ msgid "Old"
#~ msgstr "Starý"

#~, fuzzy
#~ msgid "Old fuzzy"
#~ msgstr "Starý nejistý"

#, fuzzy
#~ msgid "Old fuzzy too"
#~ msgstr "Také starý"

#~ msgid "%d old"
#~ msgid_plural "%d olds"
#~ msgstr[0] "a"
#~ msgstr[1] "b"
#~ msgstr[2] "c"

#, fuzzy
#| msgid "Older kept"
msgid "Kept fuzzy"
msgstr "Nejistý"

msgid "Kept"
msgstr "Zachován"
'''
        cat = parse(text)
        strings, plurals = po2strings.collect(cat, ["one", "few", "other"], False, "<test>")
        self.assertEqual(strings, {"Kept": "Zachován"})
        self.assertEqual(plurals, {})
        kinds = {e.msgid: (e.fuzzy, e.obsolete) for e in entries(text)}
        self.assertEqual(kinds, {
            "Old": (False, True), "Old fuzzy": (True, True), "Old fuzzy too": (True, True),
            "%d old": (False, True), "Kept fuzzy": (True, False), "Kept": (False, False),
        })

    def test_untranslated_is_skipped(self):
        text = HEADER + 'msgid "Missing"\nmsgstr ""\n\nmsgid "%d thing"\nmsgid_plural "%d things"\nmsgstr[0] "%d věc"\nmsgstr[1] ""\nmsgstr[2] ""\n'
        strings, plurals = po2strings.collect(parse(text), ["one", "few", "other"], False, "<test>")
        self.assertEqual(strings, {})
        self.assertEqual(plurals, {})

    def test_context_key_uses_eot(self):
        text = HEADER + 'msgctxt "folder"\nmsgid "Inbox"\nmsgstr "Doručená pošta"\n'
        strings, _ = po2strings.collect(parse(text), ["one", "few", "other"], False, "<test>")
        self.assertEqual(strings, {"folder\u0004Inbox": "Doručená pošta"})
        rendered = po2strings.render_strings(strings, "x.po")
        self.assertIn('"folder\\U0004Inbox" = "Doručená pošta";', rendered)

    def test_template_uses_msgid_as_value(self):
        text = 'msgid ""\nmsgstr "Plural-Forms: nplurals=INTEGER; plural=EXPRESSION;\\n"\n\nmsgctxt "folder"\nmsgid "Inbox"\nmsgstr ""\n\nmsgid "Hi %s"\nmsgstr ""\n'
        strings, _ = po2strings.collect(parse(text), ["one", "other"], True, "<test>")
        self.assertEqual(strings, {"folder\u0004Inbox": "Inbox", "Hi %s": "Hi %@"})

    def test_plural_mapping_cs_and_en(self):
        text = HEADER + '''#, c-format
msgid "%d message"
msgid_plural "%d messages"
msgstr[0] "%d zpráva"
msgstr[1] "%d zprávy"
msgstr[2] "%d zpráv"
'''
        _, cs = po2strings.collect(parse(text), po2strings.PLURAL_CATEGORIES["cs"], False, "<test>")
        self.assertEqual(cs, {"%d message": {"one": "%ld zpráva", "few": "%ld zprávy", "other": "%ld zpráv"}})
        _, en = po2strings.collect(parse(text), po2strings.PLURAL_CATEGORIES["en"], True, "<test>")
        self.assertEqual(en, {"%d message": {"one": "%ld message", "other": "%ld messages"}})

    def test_no_c_format_is_left_alone(self):
        text = HEADER + '#, no-c-format\nmsgid "%-d %b"\nmsgstr "%-d. %-m."\n\n#, c-format\nmsgid "%d B"\nmsgstr "%d B"\n'
        strings, _ = po2strings.collect(parse(text), ["one", "few", "other"], False, "<test>")
        self.assertEqual(strings, {"%-d %b": "%-d. %-m.", "%d B": "%ld B"})

    def test_bad_escape_is_an_error(self):
        with self.assertRaises(po2strings.POError):
            parse('msgid "bad \\q"\nmsgstr ""\n')

    def test_unknown_language_is_an_error(self):
        with self.assertRaises(po2strings.POError):
            po2strings.categories_for("tlh")
        self.assertEqual(po2strings.categories_for("pt_BR"), ["one", "other"])
        self.assertEqual(po2strings.categories_for("cs"), ["one", "few", "other"])


class FormatTests(unittest.TestCase):
    def test_conversions(self):
        cases = {
            "%s": "%@",
            "%d": "%ld",
            "%1$s and %2$d": "%1$@ and %2$ld",
            "Connected to malachid %s (pid %d)": "Connected to malachid %@ (pid %ld)",
            "%.1f MiB": "%.1f MiB",
            "%.0f KiB": "%.0f KiB",
            "%f": "%f",
            "100%%": "100%%",
            "%@": "%@",
            "%ld": "%ld",
            "%5d|%-3s": "%5ld|%-3@",
            "no directives": "no directives",
        }
        for src, want in cases.items():
            with self.subTest(src=src):
                self.assertEqual(po2strings.to_foundation(src), want)

    def test_idempotent(self):
        once = po2strings.to_foundation("%s %d %1$s %2$d %.1f %% %@ %ld")
        self.assertEqual(po2strings.to_foundation(once), once)


class OutputTests(unittest.TestCase):
    def test_strings_escaping(self):
        self.assertEqual(po2strings.strings_escape('a"b\\c\nd\te\u0004f\u0001'), 'a\\"b\\\\c\\nd\\te\\U0004f\\U0001')

    def test_stringsdict_layout(self):
        data = po2strings.render_stringsdict({"%d message": {"one": "%ld zpráva", "few": "%ld zprávy", "other": "%ld zpráv"}})
        root = plistlib.loads(data)
        self.assertEqual(root["%d message"]["NSStringLocalizedFormatKey"], "%#@n@")
        n = root["%d message"]["n"]
        self.assertEqual(n["NSStringFormatSpecTypeKey"], "NSStringPluralRuleType")
        self.assertEqual(n["NSStringFormatValueTypeKey"], "ld")
        self.assertEqual({k: n[k] for k in ("one", "few", "other")}, {"one": "%ld zpráva", "few": "%ld zprávy", "other": "%ld zpráv"})

    def test_end_to_end_cli(self):
        with tempfile.TemporaryDirectory() as tmp:
            pot = os.path.join(tmp, "x.pot")
            po = os.path.join(tmp, "cs.po")
            with open(pot, "w", encoding="utf-8") as f:
                f.write('msgid ""\nmsgstr "Plural-Forms: nplurals=INTEGER; plural=EXPRESSION;\\n"\n\nmsgid "Hello %s"\nmsgstr ""\n\nmsgid "%d cat"\nmsgid_plural "%d cats"\nmsgstr[0] ""\nmsgstr[1] ""\n')
            with open(po, "w", encoding="utf-8") as f:
                f.write(HEADER + 'msgid "Hello %s"\nmsgstr "Ahoj %s"\n\nmsgid "%d cat"\nmsgid_plural "%d cats"\nmsgstr[0] "%d kočka"\nmsgstr[1] "%d kočky"\nmsgstr[2] "%d koček"\n')
            out = os.path.join(tmp, "locale")
            printed = io.StringIO()
            with contextlib.redirect_stdout(printed):
                self.assertEqual(po2strings.main(["--pot", pot, "--out", out, po]), 0)
            self.assertEqual(printed.getvalue().splitlines(), [os.path.join(out, "en.lproj"), os.path.join(out, "cs.lproj")])
            with open(os.path.join(out, "en.lproj", "Localizable.strings"), encoding="utf-8") as f:
                self.assertIn('"Hello %s" = "Hello %@";', f.read())
            with open(os.path.join(out, "cs.lproj", "Localizable.strings"), encoding="utf-8") as f:
                self.assertIn('"Hello %s" = "Ahoj %@";', f.read())
            with open(os.path.join(out, "cs.lproj", "Localizable.stringsdict"), "rb") as f:
                self.assertEqual(plistlib.load(f)["%d cat"]["n"]["few"], "%ld kočky")
            with open(os.path.join(out, "en.lproj", "Localizable.stringsdict"), "rb") as f:
                self.assertEqual(plistlib.load(f)["%d cat"]["n"], {
                    "NSStringFormatSpecTypeKey": "NSStringPluralRuleType",
                    "NSStringFormatValueTypeKey": "ld",
                    "one": "%ld cat",
                    "other": "%ld cats",
                })

    def test_nplurals_mismatch_is_an_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            po = os.path.join(tmp, "en.po")
            with open(po, "w", encoding="utf-8") as f:
                f.write(HEADER + 'msgid "x"\nmsgstr "y"\n')
            with self.assertRaises(po2strings.POError):
                po2strings.convert_po(po, tmp)


@unittest.skipUnless(os.path.exists(POT) and os.path.exists(CS_PO), "po/ not found")
class RealCatalogueTests(unittest.TestCase):
    def test_pot_parses_and_has_plurals(self):
        with open(POT, encoding="utf-8") as f:
            cat = po2strings.parse_po(f.read(), POT)
        strings, plurals = po2strings.collect(cat, po2strings.PLURAL_CATEGORIES["en"], True, POT)
        self.assertIn("%d message", plurals)
        self.assertEqual(plurals["%d message"], {"one": "%ld message", "other": "%ld messages"})
        self.assertEqual(strings["folder\u0004Inbox"], "Inbox")
        self.assertEqual(strings["Connected to malachid %s (pid %d)"], "Connected to malachid %@ (pid %ld)")
        self.assertEqual(strings["%-d %b"], "%-d %b")
        self.assertEqual(strings["%a, %-d %b %Y at %H:%M"], "%a, %-d %b %Y at %H:%M")

    def test_cs_round_trip(self):
        with open(CS_PO, encoding="utf-8") as f:
            cat = po2strings.parse_po(f.read(), CS_PO)
        self.assertEqual(cat.nplurals, 3)
        strings, plurals = po2strings.collect(cat, po2strings.PLURAL_CATEGORIES["cs"], False, CS_PO)
        self.assertEqual(plurals["%d message"], {"one": "%ld zpráva", "few": "%ld zprávy", "other": "%ld zpráv"})
        self.assertEqual(strings["folder\u0004Inbox"], "Doručená pošta")
        self.assertEqual(strings["Connected to malachid %s (pid %d)"], "Připojeno k malachid %@ (pid %ld)")
        self.assertEqual(strings["%a, %-d %b %Y at %H:%M"], "%a %-d. %-m. %Y v %H:%M")
        self.assertNotIn("Project bootstrap. Nothing works yet.", strings)  # obsolete


if __name__ == "__main__":
    unittest.main()
