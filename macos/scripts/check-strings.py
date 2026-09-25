#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Vladislav Janeček
# SPDX-License-Identifier: GPL-3.0-or-later
"""Check the strings of the macOS client against the gettext template.

The Swift client keys its strings by the GTK msgids (L10n.T / N / C in
macos/Sources/MalachiCore/I18n/Localization.swift) and its .lproj
catalogues are generated from po/ by po2strings.py, so a msgid that is
not in po/malachi.pot can never be translated. This script lexes every
Swift file under macos/Sources/MalachiMail and macos/Sources/MalachiCore,
collects each string literal handed to the shim (also through a wrapper
that forwards its String parameter to L10n.T, such as wizardLabel) and
reports the ones the template does not know:

    <file>:<line>: missing msgid "..."

It also warns about string literals handed straight to AppKit sinks
(`title:`, `stringValue =`, `toolTip`, `NSMenuItem(title:`,
`addItem(withTitle:`, ...) without going through the shim, and about
literal arguments of L10n.T (a literal formatted into a translated
sentence is untranslated text). Strings that exist only on macOS (the
standard menus, Keychain wording) are marked in the source and accepted:

    m.addItem(item("Hide Others", ...))   // macOS-only string
    // macOS-only string                  (marks the next statement)
    // macOS-only strings                 (marks the rest of the enclosing block)

`--allow-macos-only` lists the marked strings as well. The exit status
is 1 when a msgid is missing or a plural form differs from the template
(with --strict also when a warning was printed). Only the standard
library is used; the PO parser is po2strings.py's.

Usage: check-strings.py [--pot po/malachi.pot] [--allow-macos-only] [--strict] [PATH...]
"""

from __future__ import annotations

import argparse
import os
import re
import sys
from dataclasses import dataclass, field

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import po2strings  # noqa: E402

SHIM = "L10n"
SHIM_FUNCS = ("T", "N", "C")
DEFAULT_SOURCES = ("macos/Sources/MalachiMail", "macos/Sources/MalachiCore")
EOT = po2strings.CONTEXT_SEPARATOR

MARKER = re.compile(r"macOS-only\s+string(s?)\b", re.IGNORECASE)

# Argument labels and properties whose string literal is shown to the user.
# A label or property also counts when it ends with one of the suffixes
# ("confirmLabel:", "windowTitle =").
SINK_LABELS = {
    "title", "withTitle", "subtitle", "label", "text", "message", "messageText",
    "informativeText", "placeholderString", "placeholder", "toolTip", "prompt",
    "heading", "description", "labelWithString", "wrappingLabelWithString",
    "checkboxWithTitle", "radioButtonWithTitle", "body", "caption", "hint",
}
SINK_PROPERTIES = {
    "stringValue", "placeholderString", "toolTip", "messageText", "informativeText",
    "title", "subtitle", "prompt", "message", "label", "text", "body", "caption", "hint",
}
SINK_SUFFIXES = (
    "title", "label", "text", "message", "placeholder", "description", "heading",
    "prompt", "tooltip", "subtitle", "hint", "caption",
)
# Names the suffix rule would catch that never reach the screen.
NOT_SINKS = {"debugDescription", "localizedDescription"}

# Free functions whose first parameter is a String: a wrapper when the body
# hands that parameter to L10n.T, a sink when the parameter's name says it
# is shown ("_ title: String").
FUNC_SIG = re.compile(r"\bfunc\s+(\w+)\s*\(\s*(?:_\s+)?(\w+)\s*:\s*String\b")
# A file may call T/N/C without the "L10n." prefix only when it defines them.
BARE_DEFINITION = re.compile(r"\b(?:func|let|var)\s+([TNC])\s*[(:=<]")
IDENT = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")
NUMBER = re.compile(r"[0-9][0-9A-Za-z_]*(?:\.[0-9][0-9A-Za-z_]*)?")
OPERATOR_CHARS = "/=-+!*%<>&|^~?"
PRINTF = re.compile(r"%(?:\d+\$)?[-+ 0#']*\d*(?:\.\d+)?(?:hh|h|ll|l|q|L|z|j|t)?[@a-zA-Z%]")
KEY_LIKE = re.compile(r"^[A-Za-z0-9]+(?:[-._/][A-Za-z0-9]+)+$")


# ---------------------------------------------------------------- lexing


@dataclass
class Tok:
    kind: str  # "str", "ident", "num", "op", "punct"
    text: str  # decoded value for "str"
    line: int
    interpolated: bool = False


@dataclass
class Comment:
    line: int
    text: str
    code_before: bool  # a token starts on the same line before the comment


_ESCAPES = {"n": "\n", "t": "\t", "r": "\r", "0": "\0", "\\": "\\", '"': '"', "'": "'"}


def _decode_escape(src: str, i: int) -> tuple[int, str]:
    """`i` is the index of the character after the backslash."""
    if i >= len(src):
        return i, "\\"
    c = src[i]
    if c in _ESCAPES:
        return i + 1, _ESCAPES[c]
    if c == "u" and i + 1 < len(src) and src[i + 1] == "{":
        end = src.find("}", i + 2)
        if end > 0:
            try:
                return end + 1, chr(int(src[i + 2:end], 16))
            except ValueError:
                pass
    return i + 1, "\\" + c


def _skip_interpolation(src: str, i: int) -> int:
    """`i` is the index of the "(" of `\\(`; returns the index after ")"."""
    depth = 0
    n = len(src)
    while i < n:
        c = src[i]
        if c == "(":
            depth += 1
        elif c == ")":
            depth -= 1
            if depth == 0:
                return i + 1
        elif c == '"':
            _, _, i = _lex_string(src, i + 1, 0)
            continue
        elif c == "\n":
            return i
        i += 1
    return n


def _lex_string(src: str, i: int, hashes: int) -> tuple[str, bool, int]:
    """A single-line string; `i` is the index after the opening quote.
    Returns (value, interpolated, index after the closing quote)."""
    close = '"' + "#" * hashes
    esc = "\\" + "#" * hashes
    out: list[str] = []
    interpolated = False
    n = len(src)
    while i < n:
        if src.startswith(close, i):
            return "".join(out), interpolated, i + len(close)
        c = src[i]
        if c == "\n":  # unterminated: stop at the line end
            return "".join(out), interpolated, i
        if src.startswith(esc, i):
            j = i + len(esc)
            if j < n and src[j] == "(":
                interpolated = True
                i = _skip_interpolation(src, j)
                out.append("\\(…)")
                continue
            i, ch = _decode_escape(src, j)
            out.append(ch)
            continue
        out.append(c)
        i += 1
    return "".join(out), interpolated, n


def _lex_multiline(src: str, i: int, hashes: int) -> tuple[str, bool, int]:
    """A multi-line string; `i` is the index after the opening triple quote.
    The closing delimiter's indentation is stripped from every line, a
    backslash at a line end joins lines."""
    close = '"""' + "#" * hashes
    esc = "\\" + "#" * hashes
    n = len(src)
    first_nl = src.find("\n", i)
    if first_nl < 0:
        return src[i:], False, n
    m = re.compile(r"\n([ \t]*)" + re.escape(close)).search(src, first_nl)
    if m is None:
        return src[first_nl + 1:], False, n
    indent = m.group(1)
    raw_lines = src[first_nl + 1:m.start()].split("\n")
    stripped = [ln[len(indent):] if ln.startswith(indent) else ln for ln in raw_lines]
    body = "\n".join(stripped)
    out: list[str] = []
    interpolated = False
    j = 0
    while j < len(body):
        if body.startswith(esc, j):
            k = j + len(esc)
            if k < len(body) and body[k] == "\n":
                j = k + 1
                continue
            if k < len(body) and body[k] == "(":
                interpolated = True
                j = _skip_interpolation(body, k)
                out.append("\\(…)")
                continue
            j, ch = _decode_escape(body, k)
            out.append(ch)
            continue
        out.append(body[j])
        j += 1
    return "".join(out), interpolated, m.end()


def lex(src: str) -> tuple[list[Tok], list[Comment]]:
    toks: list[Tok] = []
    comments: list[Comment] = []
    i, n, line = 0, len(src), 1
    code_on_line = False
    while i < n:
        c = src[i]
        if c == "\n":
            line += 1
            code_on_line = False
            i += 1
            continue
        if c in " \t\r\f":
            i += 1
            continue
        if src.startswith("//", i):
            j = src.find("\n", i)
            j = n if j < 0 else j
            comments.append(Comment(line, src[i + 2:j], code_on_line))
            i = j
            continue
        if src.startswith("/*", i):
            depth, j = 1, i + 2
            while j < n and depth:
                if src.startswith("/*", j):
                    depth += 1
                    j += 2
                elif src.startswith("*/", j):
                    depth -= 1
                    j += 2
                else:
                    j += 1
            comments.append(Comment(line, src[i + 2:j], code_on_line))
            line += src.count("\n", i, j)
            i = j
            continue
        hashes = 0
        if c == "#":
            while i + hashes < n and src[i + hashes] == "#":
                hashes += 1
            if not (i + hashes < n and src[i + hashes] == '"'):
                hashes = 0
        if c == '"' or hashes:
            start = i + hashes
            if src.startswith('"""', start):
                value, interp, j = _lex_multiline(src, start + 3, hashes)
            else:
                value, interp, j = _lex_string(src, start + 1, hashes)
            toks.append(Tok("str", value, line, interp))
            code_on_line = True
            line += src.count("\n", i, j)
            i = j
            continue
        code_on_line = True
        if c == "`":
            j = src.find("`", i + 1)
            j = n if j < 0 else j
            toks.append(Tok("ident", src[i + 1:j], line))
            i = j + 1
            continue
        if c == "#" and i + 1 < n and (src[i + 1].isalpha() or src[i + 1] == "_"):
            m = IDENT.match(src, i + 1)
            toks.append(Tok("ident", "#" + m.group(0), line))
            i = m.end()
            continue
        m = IDENT.match(src, i)
        if m:
            toks.append(Tok("ident", m.group(0), line))
            i = m.end()
            continue
        m = NUMBER.match(src, i)
        if m:
            toks.append(Tok("num", m.group(0), line))
            i = m.end()
            continue
        if c in OPERATOR_CHARS:
            j = i
            while j < n and src[j] in OPERATOR_CHARS and not src.startswith("//", j) and not src.startswith("/*", j):
                j += 1
            if j == i:
                j = i + 1
            toks.append(Tok("op", src[i:j], line))
            i = j
            continue
        toks.append(Tok("punct", c, line))
        i += 1
    return toks, comments


# ------------------------------------------------------------- template


def strip_mnemonic(s: str) -> str:
    """"_Next" -> "Next", "__" -> "_" (the GTK mnemonic convention)."""
    out: list[str] = []
    i = 0
    while i < len(s):
        if s[i] == "_" and i + 1 < len(s):
            out.append(s[i + 1])
            i += 2
            continue
        out.append(s[i])
        i += 1
    return "".join(out)


@dataclass
class Template:
    singular: set[str] = field(default_factory=set)  # keys (context-aware)
    plural: dict[str, str] = field(default_factory=dict)  # key -> msgid_plural
    by_text: dict[str, str] = field(default_factory=dict)  # mnemonic-free text -> msgid

    @classmethod
    def load(cls, path: str) -> "Template":
        with open(path, encoding="utf-8") as f:
            cat = po2strings.parse_po(f.read(), path)
        t = cls()
        for e in cat.entries:
            if e.is_header or e.obsolete:
                continue
            if e.msgid_plural is None:
                t.singular.add(e.key)
                if e.msgctxt is None:
                    t.by_text.setdefault(strip_mnemonic(e.msgid), e.msgid)
            else:
                t.plural[e.key] = e.msgid_plural
        return t

    def existing_msgid(self, text: str) -> str | None:
        """The msgid a marked literal could use instead, if the template has one."""
        if text in self.singular:
            return text
        return self.by_text.get(text)


# --------------------------------------------------------------- scanning


@dataclass
class Finding:
    kind: str  # "missing", "warning", "macos-only", "note"
    path: str
    line: int
    message: str

    def __str__(self) -> str:
        prefix = {"warning": "warning: ", "macos-only": "macOS-only: ", "note": "note: "}.get(self.kind, "")
        return f"{self.path}:{self.line}: {prefix}{self.message}"


def _function_body(src: str, i: int) -> str:
    j = src.find("{", i)
    if j < 0:
        return ""
    depth, k = 0, j
    while k < len(src):
        ch = src[k]
        if ch == "{":
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                return src[j:k]
        elif ch == '"':
            _, _, k = _lex_string(src, k + 1, 0)
            continue
        k += 1
    return src[j:]


def detect_helpers(src: str) -> tuple[dict[str, str], set[str]]:
    """(wrappers: name -> parameter, sink helpers) defined in `src`."""
    wrappers: dict[str, str] = {}
    sinks: set[str] = set()
    for m in FUNC_SIG.finditer(src):
        name, param = m.group(1), m.group(2)
        body = _function_body(src, m.end())
        if re.search(r"\b" + SHIM + r"\.T\(\s*" + re.escape(param) + r"\s*[,)]", body):
            wrappers[name] = param
        elif param.lower().endswith(SINK_SUFFIXES):
            sinks.add(name)
    return wrappers, sinks


def _has_sink_suffix(name: str) -> bool:
    lower = name.lower()
    return lower.endswith(SINK_SUFFIXES) or lower.endswith(tuple(s + "s" for s in SINK_SUFFIXES))


def is_sink_label(label: str) -> bool:
    if label in NOT_SINKS:
        return False
    return label in SINK_LABELS or _has_sink_suffix(label)


def is_sink_property(prop: str) -> bool:
    if prop in NOT_SINKS:
        return False
    return prop in SINK_PROPERTIES or _has_sink_suffix(prop)


def enclosing_array_label(toks: list[Tok], k: int) -> str | None:
    """The sink label of `label: [ ... ]` when the string at `k` is an
    element of that array literal ("titles:", "withTitles:")."""
    depth = 0
    j = k - 1
    limit = max(0, k - 400)
    while j >= limit:
        t = toks[j]
        if t.kind == "punct":
            if t.text in ")]}":
                depth += 1
            elif t.text in "([{":
                if depth == 0:
                    if t.text == "[" and j >= 3 and toks[j - 1].kind == "punct" and toks[j - 1].text == ":" \
                            and toks[j - 2].kind == "ident" and toks[j - 3].kind == "punct" and toks[j - 3].text in "(,[" \
                            and is_sink_label(toks[j - 2].text):
                        return toks[j - 2].text + ": ["
                    return None
                depth -= 1
        j -= 1
    return None


def looks_visible(value: str) -> bool:
    """False for literals that cannot be prose: no letters once printf
    directives are removed, or a key-like token such as "group-by-conversation"."""
    if KEY_LIKE.match(value):
        return False
    return re.search(r"[^\W\d_]", PRINTF.sub("", value)) is not None


def quote(s: str) -> str:
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"').replace("\n", "\\n").replace("\t", "\\t").replace(EOT, "\\u{4}") + '"'


def marked_lines(comments: list[Comment], toks: list[Tok], nlines: int) -> dict[int, str]:
    """Line -> marker text for the lines a `macOS-only string(s)` comment covers."""
    brace_end = [0] * (nlines + 2)
    paren_end = [0] * (nlines + 2)
    has_code = [False] * (nlines + 2)
    brace = paren = 0
    cur = 1
    for t in toks:
        while cur < t.line:
            brace_end[cur], paren_end[cur] = brace, paren
            cur += 1
        has_code[t.line] = True
        if t.kind == "punct":
            if t.text == "{":
                brace += 1
            elif t.text == "}":
                brace -= 1
            elif t.text in "([":
                paren += 1
            elif t.text in ")]":
                paren -= 1
    while cur <= nlines + 1:
        brace_end[cur], paren_end[cur] = brace, paren
        cur += 1

    def statement(first: int) -> list[int]:
        base = paren_end[first - 1] if first > 1 else 0
        lines = [first]
        k = first
        while k <= nlines and paren_end[k] > base:
            k += 1
            lines.append(k)
        return lines

    marked: dict[int, str] = {}
    for c in comments:
        m = MARKER.search(c.text)
        if not m:
            continue
        text = c.text.strip()
        if c.code_before:
            for ln in statement(c.line):
                marked.setdefault(ln, text)
        elif m.group(1):  # plural: the rest of the enclosing block
            depth = brace_end[c.line]
            ln = c.line + 1
            while ln <= nlines and (brace_end[ln - 1] if ln > 1 else 0) >= depth:
                marked.setdefault(ln, text)
                ln += 1
        else:  # singular: the next statement
            ln = c.line + 1
            while ln <= nlines and not has_code[ln]:
                ln += 1
            if ln <= nlines:
                for k in statement(ln):
                    marked.setdefault(k, text)
    return marked


def split_args(toks: list[Tok], open_index: int) -> tuple[list[list[Tok]], int]:
    """The top-level arguments of the call whose "(" is at `open_index`."""
    args: list[list[Tok]] = []
    cur: list[Tok] = []
    depth = 0
    j = open_index
    while j < len(toks):
        t = toks[j]
        if t.kind == "punct":
            if t.text in "([{":
                depth += 1
                if depth == 1:
                    j += 1
                    continue
            elif t.text in ")]}":
                depth -= 1
                if depth == 0:
                    if cur or args:
                        args.append(cur)
                    return args, j
            elif t.text == "," and depth == 1:
                args.append(cur)
                cur = []
                j += 1
                continue
        cur.append(t)
        j += 1
    if cur:
        args.append(cur)
    return args, j


def unlabel(arg: list[Tok]) -> list[Tok]:
    if len(arg) >= 2 and arg[0].kind == "ident" and arg[1].kind == "punct" and arg[1].text == ":":
        return arg[2:]
    return arg


def literal(arg: list[Tok]) -> Tok | None:
    """The argument's string token when it is one plain literal."""
    arg = unlabel(arg)
    if len(arg) == 1 and arg[0].kind == "str" and not arg[0].interpolated:
        return arg[0]
    return None


@dataclass
class FileReport:
    findings: list[Finding] = field(default_factory=list)
    calls: int = 0
    non_literal: int = 0


def scan_source(path: str, src: str, template: Template, wrappers: dict[str, str],
                sink_helpers: set[str], check_sinks: bool = True) -> FileReport:
    rep = FileReport()
    toks, comments = lex(src)
    nlines = src.count("\n") + 1
    marked = marked_lines(comments, toks, nlines)
    local_wrappers, _ = detect_helpers(src)
    wrapper_params = set(local_wrappers.values())
    bare_ok = {m.group(1) for m in BARE_DEFINITION.finditer(src)}

    def add(kind: str, line: int, message: str) -> None:
        rep.findings.append(Finding(kind, path, line, message))

    def missing(line: int, message: str) -> None:
        if line in marked:
            add("macos-only", line, message)
        else:
            add("missing", line, message)

    def warn(line: int, message: str) -> None:
        if line in marked:
            add("macos-only", line, message)
        else:
            add("warning", line, message)

    def check_call(func: str, via: str, args: list[list[Tok]], line: int) -> None:
        rep.calls += 1
        label = f"{SHIM}.{func}" if via == SHIM else via
        first = literal(args[0]) if args else None
        if first is None:
            expr = unlabel(args[0]) if args else []
            if any(t.kind == "str" and t.interpolated for t in expr):
                add("missing", line, f"msgid of {label} is an interpolated literal")
            elif not (len(expr) == 1 and expr[0].kind == "ident" and expr[0].text in wrapper_params):
                rep.non_literal += 1
                add("note", line, f"msgid of {label} is not a literal")
            return
        if func == "N":
            second = literal(args[1]) if len(args) > 1 else None
            if second is None:
                add("missing", line, f"plural of {label}({quote(first.text)}) is not a literal")
                return
            want = template.plural.get(first.text)
            if want is None:
                if first.text in template.singular:
                    missing(line, f"missing plural msgid {quote(first.text)} (the .pot has it as a singular entry)")
                else:
                    missing(line, f"missing msgid {quote(first.text)} (plural)")
            elif want != second.text:
                add("missing", line, f"plural form of {quote(first.text)} differs from the .pot: code has {quote(second.text)}, the .pot has {quote(want)}")
            return
        if func == "C":
            second = literal(args[1]) if len(args) > 1 else None
            if second is None:
                add("missing", line, f"msgid of {label}({quote(first.text)}, …) is not a literal")
                return
            key = first.text + EOT + second.text
            if key not in template.singular:
                hint = " (the .pot has it without a context)" if second.text in template.singular else ""
                missing(line, f"missing msgid {quote(second.text)} with context {quote(first.text)}{hint}")
            return
        # T and wrappers.
        if first.text not in template.singular:
            if first.text in template.plural:
                warn(line, f"msgid {quote(first.text)} is a plural entry in the .pot; use {SHIM}.N")
            else:
                suffix = "" if via == SHIM else f" (via {via})"
                missing(line, f"missing msgid {quote(first.text)}{suffix}")
        if check_sinks and via == SHIM:
            for arg in args[1:]:
                lit = literal(arg)
                if lit is not None and looks_visible(lit.text):
                    warn(lit.line, f"literal argument {quote(lit.text)} of {label}({quote(first.text)}, …) is untranslated text")

    for k, t in enumerate(toks):
        if t.kind == "ident":
            nxt = toks[k + 1] if k + 1 < len(toks) else None
            prev = toks[k - 1] if k > 0 else None
            # A member access (x.T) or a definition (func T) is not a call.
            after_dot = prev is not None and ((prev.kind == "punct" and prev.text == ".")
                                              or (prev.kind == "ident" and prev.text == "func"))
            if (t.text == SHIM and nxt is not None and nxt.kind == "punct" and nxt.text == "."
                    and k + 3 < len(toks) and toks[k + 2].kind == "ident" and toks[k + 2].text in SHIM_FUNCS
                    and toks[k + 3].kind == "punct" and toks[k + 3].text == "("):
                args, _ = split_args(toks, k + 3)
                check_call(toks[k + 2].text, SHIM, args, toks[k + 2].line)
            elif (not after_dot and nxt is not None and nxt.kind == "punct" and nxt.text == "("
                    and (t.text in bare_ok and t.text in SHIM_FUNCS)):
                args, _ = split_args(toks, k + 1)
                check_call(t.text, SHIM, args, t.line)
            elif not after_dot and t.text in wrappers and nxt is not None and nxt.kind == "punct" and nxt.text == "(":
                args, _ = split_args(toks, k + 1)
                check_call("T", t.text, args, t.line)
            continue
        if t.kind != "str" or not check_sinks:
            continue
        if not looks_visible(t.text):
            continue
        p1 = toks[k - 1] if k >= 1 else None
        p2 = toks[k - 2] if k >= 2 else None
        p3 = toks[k - 3] if k >= 3 else None
        sink: str | None = None
        if p1 is not None and p1.kind == "punct" and p1.text == ":" and p2 is not None and p2.kind == "ident" \
                and p3 is not None and p3.kind == "punct" and p3.text in "(,[" and is_sink_label(p2.text):
            sink = p2.text + ":"
        elif p1 is not None and p1.kind == "op" and p1.text == "=" and p2 is not None and p2.kind == "ident" \
                and is_sink_property(p2.text):
            sink = p2.text + " ="
        elif p1 is not None and p1.kind == "punct" and p1.text == "(" and p2 is not None and p2.kind == "ident" \
                and p2.text in sink_helpers and not (p3 is not None and p3.kind == "punct" and p3.text == "."):
            sink = p2.text + "("
        elif p1 is not None and p1.kind == "punct" and p1.text in "[,":
            sink = enclosing_array_label(toks, k)
        if sink is None:
            continue
        what = "interpolated literal" if t.interpolated else "literal"
        warn(t.line, f"unwrapped {what} {quote(t.text)} passed to {sink}")
        if t.line in marked and (existing := template.existing_msgid(t.text)) is not None:
            how = f"{SHIM}.T({quote(existing)})" if existing == t.text else f"mn({SHIM}.T({quote(existing)}))"
            add("warning", t.line, f"macOS-only literal {quote(t.text)} has a msgid in the .pot; use {how}")
    return rep


# ------------------------------------------------------------------ main


def swift_files(paths: list[str]) -> list[str]:
    out: list[str] = []
    for p in paths:
        if os.path.isdir(p):
            for root, _, files in os.walk(p):
                out.extend(os.path.join(root, f) for f in files if f.endswith(".swift"))
        elif p.endswith(".swift"):
            out.append(p)
    return sorted(set(out))


@dataclass
class Report:
    findings: list[Finding]
    files: int
    calls: int
    non_literal: int

    def count(self, kind: str) -> int:
        return sum(1 for f in self.findings if f.kind == kind)


def run(root: str, pot: str, paths: list[str], extra_wrappers: list[str] | None = None,
        check_sinks: bool = True) -> Report:
    template = Template.load(pot)
    files = swift_files(paths)
    sources = {}
    for f in files:
        with open(f, encoding="utf-8") as fh:
            sources[f] = fh.read()
    wrappers: dict[str, str] = {w: "" for w in (extra_wrappers or [])}
    sink_helpers: set[str] = set()
    for src in sources.values():
        w, s = detect_helpers(src)
        wrappers.update(w)
        sink_helpers |= s
    sink_helpers -= set(wrappers)
    findings: list[Finding] = []
    calls = non_literal = 0
    for f in files:
        rel = os.path.relpath(f, root)
        rep = scan_source(rel, sources[f], template, wrappers, sink_helpers, check_sinks)
        findings.extend(rep.findings)
        calls += rep.calls
        non_literal += rep.non_literal
    findings.sort(key=lambda x: (x.path, x.line, x.kind))
    return Report(findings, len(files), calls, non_literal)


def main(argv: list[str] | None = None) -> int:
    root_default = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--root", default=root_default, help="repository root (paths in the output are relative to it)")
    p.add_argument("--pot", help="the gettext template (default: <root>/po/malachi.pot)")
    p.add_argument("--allow-macos-only", action="store_true", help="also list the strings marked // macOS-only string(s)")
    p.add_argument("--strict", action="store_true", help="exit 1 on warnings too")
    p.add_argument("--notes", action="store_true", help="also list the calls whose msgid is not a literal")
    p.add_argument("--no-sinks", action="store_true", help="skip the warnings about unwrapped literals")
    p.add_argument("--wrapper", action="append", default=[], help="a function that forwards its first argument to L10n.T (auto-detected too)")
    p.add_argument("paths", nargs="*", help="Swift files or directories (default: macos/Sources/MalachiMail and MalachiCore)")
    args = p.parse_args(argv)
    root = os.path.abspath(args.root)
    pot = args.pot or os.path.join(root, "po", "malachi.pot")
    paths = [os.path.join(root, s) for s in DEFAULT_SOURCES] if not args.paths else args.paths
    try:
        report = run(root, pot, paths, args.wrapper, not args.no_sinks)
    except (po2strings.POError, OSError) as e:
        print(f"check-strings: {e}", file=sys.stderr)
        return 2
    for f in report.findings:
        if f.kind == "macos-only" and not args.allow_macos_only:
            continue
        if f.kind == "note" and not args.notes:
            continue
        print(f)
    missing = report.count("missing")
    warnings = report.count("warning")
    macos_only = report.count("macos-only")
    print(f"check-strings: {report.files} files, {report.calls} shim calls, {missing} missing, "
          f"{warnings} warnings, {macos_only} macOS-only, {report.non_literal} non-literal msgids", file=sys.stderr)
    if missing or (args.strict and warnings):
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
