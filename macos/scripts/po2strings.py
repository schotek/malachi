#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
"""Convert the gettext catalogues in po/ into Foundation .lproj directories.

The macOS client keys its strings by the GTK msgid (docs: the plan's i18n
pipeline), so the catalogues the daemon's GTK UI is translated with are the
source of truth here too. This script needs only the Python standard
library and writes, per language:

    <out>/<lang>.lproj/Localizable.strings      singular entries
    <out>/<lang>.lproj/Localizable.stringsdict  plural entries

Keys are msgids verbatim. Entries with a msgctxt are keyed
"<ctxt>\\u0004<msgid>" (the gettext EOT convention), written into the
.strings file with the escape \\U0004. Plural entries are keyed by the
singular msgid. Values are converted from gettext/printf to Foundation
format (%s -> %@, %d -> %ld, %N$s -> %N$@, %N$d -> %N$ld) unless the entry is
flagged no-c-format (strftime patterns). Fuzzy and obsolete entries, and
untranslated ones, are left out so the runtime falls back to the msgid.

The template (--pot) produces en.lproj with the msgid as the value; that is
needed because context keys must resolve to the plain msgid in English too.

Usage: po2strings.py --pot ../po/malachi.pot --out <dir> [PO...]
"""

from __future__ import annotations

import argparse
import os
import plistlib
import re
import sys
from dataclasses import dataclass, field

# CLDR plural categories per language, in the order of the gettext plural
# index (msgstr[0], msgstr[1], ...). The Swift side (PluralRules.swift)
# computes the same index and looks the category up by name, so the two
# tables must agree. An unknown language is an error, not a guess.
PLURAL_CATEGORIES: dict[str, list[str]] = {
    "en": ["one", "other"],
    "de": ["one", "other"],
    "nl": ["one", "other"],
    "sv": ["one", "other"],
    "da": ["one", "other"],
    "nb": ["one", "other"],
    "nn": ["one", "other"],
    "fi": ["one", "other"],
    "it": ["one", "other"],
    "es": ["one", "other"],
    "pt": ["one", "other"],
    "fr": ["one", "other"],
    "cs": ["one", "few", "other"],
    "sk": ["one", "few", "other"],
    "pl": ["one", "few", "many"],
    "ru": ["one", "few", "many"],
    "uk": ["one", "few", "many"],
    "ro": ["one", "few", "other"],
}

# gettext's EOT separator between context and msgid.
CONTEXT_SEPARATOR = "\u0004"


class POError(Exception):
    """A malformed catalogue; the message names the file and line."""


@dataclass
class Entry:
    msgctxt: str | None = None
    msgid: str = ""
    msgid_plural: str | None = None
    msgstr: str = ""
    msgstr_plural: dict[int, str] = field(default_factory=dict)
    flags: set[str] = field(default_factory=set)
    obsolete: bool = False
    line: int = 0

    @property
    def key(self) -> str:
        if self.msgctxt is not None:
            return self.msgctxt + CONTEXT_SEPARATOR + self.msgid
        return self.msgid

    @property
    def is_header(self) -> bool:
        return self.msgid == "" and self.msgctxt is None

    @property
    def fuzzy(self) -> bool:
        return "fuzzy" in self.flags

    @property
    def converts_format(self) -> bool:
        return "no-c-format" not in self.flags


@dataclass
class Catalogue:
    entries: list[Entry]
    nplurals: int | None  # None when the header has no usable Plural-Forms

    def header(self) -> dict[str, str]:
        for e in self.entries:
            if e.is_header:
                return parse_header(e.msgstr)
        return {}


# ---------------------------------------------------------------- parsing

_ESCAPES = {
    "n": "\n",
    "t": "\t",
    "r": "\r",
    '"': '"',
    "\\": "\\",
    "a": "\a",
    "b": "\b",
    "f": "\f",
    "v": "\v",
}


def unquote(s: str, where: str) -> str:
    """Decode one gettext string token: the part between the quotes."""
    if len(s) < 2 or s[0] != '"' or s[-1] != '"':
        raise POError(f"{where}: expected a quoted string, got {s!r}")
    body = s[1:-1]
    out: list[str] = []
    i = 0
    while i < len(body):
        c = body[i]
        if c != "\\":
            out.append(c)
            i += 1
            continue
        i += 1
        if i >= len(body):
            raise POError(f"{where}: trailing backslash")
        e = body[i]
        if e in _ESCAPES:
            out.append(_ESCAPES[e])
            i += 1
        elif e in "01234567":
            j = i
            while j < len(body) and j < i + 3 and body[j] in "01234567":
                j += 1
            out.append(chr(int(body[i:j], 8)))
            i = j
        elif e == "x":
            j = i + 1
            while j < len(body) and j < i + 3 and body[j] in "0123456789abcdefABCDEF":
                j += 1
            if j == i + 1:
                raise POError(f"{where}: bad \\x escape")
            out.append(chr(int(body[i + 1:j], 16)))
            i = j
        else:
            raise POError(f"{where}: unknown escape \\{e}")
    return "".join(out)


_KEYWORD = re.compile(r'^(msgctxt|msgid_plural|msgid|msgstr\[(\d+)\]|msgstr)\s+(".*")\s*$')
_CONTINUATION = re.compile(r'^(".*")\s*$')


def parse_po(text: str, name: str = "<po>") -> Catalogue:
    """Parse a PO/POT file. Comments are kept only as far as they matter
    (flags, obsolete marker); the header's Plural-Forms is decoded."""
    entries: list[Entry] = []
    cur: Entry | None = None
    field_name: str | None = None  # which string the next continuation extends
    field_index = 0

    def finish() -> None:
        nonlocal cur, field_name
        if cur is not None:
            entries.append(cur)
        cur = None
        field_name = None

    def target() -> Entry:
        nonlocal cur
        if cur is None:
            cur = Entry(line=lineno)
        return cur

    def append(e: Entry, value: str) -> None:
        if field_name == "msgctxt":
            e.msgctxt = (e.msgctxt or "") + value
        elif field_name == "msgid":
            e.msgid += value
        elif field_name == "msgid_plural":
            e.msgid_plural = (e.msgid_plural or "") + value
        elif field_name == "msgstr":
            e.msgstr += value
        elif field_name == "msgstr[]":
            e.msgstr_plural[field_index] = e.msgstr_plural.get(field_index, "") + value

    for lineno, raw in enumerate(text.splitlines(), start=1):
        where = f"{name}:{lineno}"
        line = raw.strip()
        if not line:
            finish()
            continue
        obsolete = False
        if line.startswith("#~"):
            obsolete = True
            line = line[2:].strip()
            if not line:
                continue
            if line[0] in "|,.:#":
                # A comment on an obsolete entry: msgmerge writes the
                # previous msgid as "#~| msgid" and flags as "#~, fuzzy".
                line = "#" + line
        if line.startswith("#"):
            # PO files separate entries with blank lines; be lenient and
            # let a comment after a msgstr start the next entry too.
            if cur is not None and field_name in ("msgstr", "msgstr[]"):
                finish()
            e = target()
            if obsolete:
                e.obsolete = True
            if line.startswith("#,"):
                for flag in line[2:].split(","):
                    flag = flag.strip()
                    if flag:
                        e.flags.add(flag)
            continue
        m = _KEYWORD.match(line)
        if m:
            kw, idx, quoted = m.group(1), m.group(2), m.group(3)
            if kw == "msgctxt" or (kw == "msgid" and field_name in ("msgstr", "msgstr[]")):
                # A new entry without a separating blank line.
                finish()
            if kw == "msgid" and cur is not None and field_name == "msgid":
                raise POError(f"{where}: duplicate msgid in one entry")
            e = target()
            if obsolete:
                e.obsolete = True
            if kw.startswith("msgstr["):
                field_name = "msgstr[]"
                field_index = int(idx)
            else:
                field_name = kw
            append(e, unquote(quoted, where))
            continue
        m = _CONTINUATION.match(line)
        if m:
            if cur is None or field_name is None:
                raise POError(f"{where}: continuation string without a keyword")
            if obsolete:
                cur.obsolete = True
            append(cur, unquote(m.group(1), where))
            continue
        raise POError(f"{where}: cannot parse {raw!r}")
    finish()

    nplurals: int | None = None
    for e in entries:
        if e.is_header:
            nplurals = parse_nplurals(parse_header(e.msgstr).get("Plural-Forms", ""))
            break
    return Catalogue(entries=entries, nplurals=nplurals)


def parse_header(msgstr: str) -> dict[str, str]:
    out: dict[str, str] = {}
    for line in msgstr.split("\n"):
        if ":" in line:
            k, v = line.split(":", 1)
            out[k.strip()] = v.strip()
    return out


def parse_nplurals(plural_forms: str) -> int | None:
    m = re.search(r"nplurals\s*=\s*(\d+)", plural_forms)
    return int(m.group(1)) if m else None


# ------------------------------------------------------- format conversion

# One printf directive: %[N$][flags][width][.precision][length]conversion.
_DIRECTIVE = re.compile(
    r"%(?P<pos>\d+\$)?(?P<flags>[-+ 0#']*)(?P<width>\d+|\*)?(?P<prec>\.\d*)?"
    r"(?P<len>hh|h|ll|l|q|L|z|j|t)?(?P<conv>[a-zA-Z@%])"
)


def to_foundation(s: str) -> str:
    """gettext/printf format -> Foundation format. Idempotent: %@ and %ld
    are left alone, as is everything but a bare %s / %d."""

    def sub(m: re.Match[str]) -> str:
        if m.group("len"):
            return m.group(0)
        conv = m.group("conv")
        if conv == "s":
            conv = "@"
        elif conv == "d":
            conv = "ld"
        else:
            return m.group(0)
        return "%" + (m.group("pos") or "") + m.group("flags") + (m.group("width") or "") + (m.group("prec") or "") + conv

    return _DIRECTIVE.sub(sub, s)


# ---------------------------------------------------------------- output


def strings_escape(s: str) -> str:
    """Escape for an old-style (OpenStep) .strings quoted string. Control
    characters become \\Uxxxx, which NSDictionary(contentsOf:) decodes; the
    context separator U+0004 relies on that."""
    out: list[str] = []
    for ch in s:
        code = ord(ch)
        if ch == "\\":
            out.append("\\\\")
        elif ch == '"':
            out.append('\\"')
        elif ch == "\n":
            out.append("\\n")
        elif ch == "\t":
            out.append("\\t")
        elif ch == "\r":
            out.append("\\r")
        elif code < 0x20 or code == 0x7F:
            out.append("\\U%04X" % code)
        else:
            out.append(ch)
    return "".join(out)


def render_strings(pairs: dict[str, str], source: str) -> str:
    lines = [f"/* Generated by po2strings.py from {source}; do not edit. */", ""]
    for key in sorted(pairs):
        lines.append(f'"{strings_escape(key)}" = "{strings_escape(pairs[key])}";')
    lines.append("")
    return "\n".join(lines)


def render_stringsdict(plurals: dict[str, dict[str, str]]) -> bytes:
    """plurals: singular msgid -> {category: format}. The layout is the
    standard one Foundation expects (%#@n@ with a plural-rule variable), so
    the files also work with bundle-based lookup, though the client selects
    the form itself (Catalogue in Localization.swift)."""
    root: dict[str, object] = {}
    for key in sorted(plurals):
        variable: dict[str, str] = {
            "NSStringFormatSpecTypeKey": "NSStringPluralRuleType",
            "NSStringFormatValueTypeKey": "ld",
        }
        variable.update(plurals[key])
        root[key] = {"NSStringLocalizedFormatKey": "%#@n@", "n": variable}
    return plistlib.dumps(root, fmt=plistlib.FMT_XML, sort_keys=True)


def collect(cat: Catalogue, categories: list[str], source_is_template: bool, name: str) -> tuple[dict[str, str], dict[str, dict[str, str]]]:
    """Split a catalogue into the .strings pairs and the .stringsdict
    plurals. For the template the msgid itself is the value."""
    strings: dict[str, str] = {}
    plurals: dict[str, dict[str, str]] = {}
    for e in cat.entries:
        if e.is_header or e.obsolete or e.fuzzy:
            continue
        convert = to_foundation if e.converts_format else (lambda s: s)
        if e.msgid_plural is None:
            value = e.msgid if source_is_template else e.msgstr
            if value == "":
                continue
            if e.key in strings:
                raise POError(f"{name}: duplicate entry {e.key!r} (line {e.line})")
            strings[e.key] = convert(value)
            continue
        if e.msgctxt is not None:
            raise POError(f"{name}: plural entries with msgctxt are not supported ({e.msgid!r}, line {e.line})")
        if source_is_template:
            forms = [e.msgid, e.msgid_plural]
        else:
            forms = [e.msgstr_plural.get(i, "") for i in range(len(categories))]
            if any(f == "" for f in forms) or len(e.msgstr_plural) != len(categories):
                # Partially translated: msgfmt would reject it; fall back.
                continue
        if e.msgid in plurals:
            raise POError(f"{name}: duplicate plural entry {e.msgid!r} (line {e.line})")
        plurals[e.msgid] = {cat_name: convert(f) for cat_name, f in zip(categories, forms)}
    return strings, plurals


def language_of(po_path: str) -> str:
    base = os.path.basename(po_path)
    if not base.endswith(".po"):
        raise POError(f"{po_path}: expected a .po file")
    return base[:-3]


def categories_for(lang: str) -> list[str]:
    base = lang.split("_")[0].split("-")[0].lower()
    if base not in PLURAL_CATEGORIES:
        raise POError(f"no plural table for language {lang!r}; add it to PLURAL_CATEGORIES")
    return PLURAL_CATEGORIES[base]


def write_lproj(out_dir: str, lang: str, strings: dict[str, str], plurals: dict[str, dict[str, str]], source: str) -> str:
    lproj = os.path.join(out_dir, lang.replace("_", "-") + ".lproj")
    os.makedirs(lproj, exist_ok=True)
    with open(os.path.join(lproj, "Localizable.strings"), "w", encoding="utf-8", newline="\n") as f:
        f.write(render_strings(strings, source))
    with open(os.path.join(lproj, "Localizable.stringsdict"), "wb") as f:
        f.write(render_stringsdict(plurals))
    return lproj


def convert_template(pot_path: str, out_dir: str) -> str:
    with open(pot_path, encoding="utf-8") as f:
        cat = parse_po(f.read(), pot_path)
    strings, plurals = collect(cat, PLURAL_CATEGORIES["en"], True, pot_path)
    return write_lproj(out_dir, "en", strings, plurals, os.path.basename(pot_path))


def convert_po(po_path: str, out_dir: str) -> str:
    lang = language_of(po_path)
    categories = categories_for(lang)
    with open(po_path, encoding="utf-8") as f:
        cat = parse_po(f.read(), po_path)
    if cat.nplurals is not None and cat.nplurals != len(categories):
        raise POError(f"{po_path}: header says nplurals={cat.nplurals}, the table for {lang!r} has {len(categories)} forms")
    strings, plurals = collect(cat, categories, False, po_path)
    return write_lproj(out_dir, lang, strings, plurals, os.path.basename(po_path))


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--pot", required=True, help="the template; produces en.lproj")
    p.add_argument("--out", required=True, help="output directory for the .lproj directories")
    p.add_argument("po", nargs="*", help="translations (<lang>.po); each produces <lang>.lproj")
    args = p.parse_args(argv)
    try:
        written = [convert_template(args.pot, args.out)]
        for po in args.po:
            written.append(convert_po(po, args.out))
    except (POError, OSError) as e:
        print(f"po2strings: {e}", file=sys.stderr)
        return 1
    for d in written:
        print(d)
    return 0


if __name__ == "__main__":
    sys.exit(main())
