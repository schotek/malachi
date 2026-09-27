// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/scripts/po2strings.py (parse_po, unquote, parse_header,
// parse_nplurals, the Catalogue dataclass). GTK: msgfmt reads the same files.
//
// po/ is the single source of truth of every client (docs/windows-port.md
// §9): Windows parses po/<lang>.po at run time instead of converting it at
// build time. One deliberate difference from the script: a comment that
// opens an entry stays with it when a msgctxt follows (the script closes the
// entry at every msgctxt, so the flags of a context entry, fuzzy included,
// were lost with it; nothing in po/ depends on that).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;
using System.Text.RegularExpressions;

namespace Malachi.Core.I18n;

/// <summary>A parsed PO or POT file.</summary>
public sealed partial class PoFile
{
    private PoFile(IReadOnlyList<PoEntry> entries, int? nplurals)
    {
        Entries = entries;
        NPlurals = nplurals;
    }

    /// <summary>Every entry in file order, the header included.</summary>
    public IReadOnlyList<PoEntry> Entries { get; }

    /// <summary>The header's <c>nplurals</c>; null when it has no usable Plural-Forms.</summary>
    public int? NPlurals { get; }

    /// <summary>The header's fields ("Language", "Plural-Forms", …); empty without a header.</summary>
    public IReadOnlyDictionary<string, string> Header()
    {
        foreach (var e in Entries)
        {
            if (e.IsHeader)
            {
                return ParseHeader(e.Msgstr);
            }
        }
        return new Dictionary<string, string>(StringComparer.Ordinal);
    }

    /// <summary>
    /// Parses a PO or POT file. Comments are kept only as far as they
    /// matter (flags, the obsolete marker); the header's Plural-Forms is
    /// decoded. Throws <see cref="PoFormatException"/> naming
    /// <paramref name="name"/> and the line.
    /// </summary>
    public static PoFile Parse(string text, string name = "<po>")
    {
        ArgumentNullException.ThrowIfNull(text);
        var parser = new Parser(name);
        var lineNumber = 0;
        foreach (var raw in SplitLines(text))
        {
            lineNumber++;
            parser.Line(raw, lineNumber);
        }
        var entries = parser.Finish();

        int? nplurals = null;
        foreach (var e in entries)
        {
            if (e.IsHeader)
            {
                nplurals = ParseNPlurals(ParseHeader(e.Msgstr).GetValueOrDefault("Plural-Forms", ""));
                break;
            }
        }
        return new PoFile(entries, nplurals);
    }

    /// <summary>The <c>Key: value</c> lines of a header msgstr.</summary>
    public static IReadOnlyDictionary<string, string> ParseHeader(string msgstr)
    {
        ArgumentNullException.ThrowIfNull(msgstr);
        var result = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (var line in msgstr.Split('\n'))
        {
            var colon = line.IndexOf(':', StringComparison.Ordinal);
            if (colon >= 0)
            {
                result[line[..colon].Trim()] = line[(colon + 1)..].Trim();
            }
        }
        return result;
    }

    /// <summary>The <c>nplurals</c> of a Plural-Forms value, or null.</summary>
    public static int? ParseNPlurals(string pluralForms)
    {
        ArgumentNullException.ThrowIfNull(pluralForms);
        var m = NPluralsPattern().Match(pluralForms);
        if (!m.Success)
        {
            return null;
        }
        // A number too large for int cannot match any table.
        return int.TryParse(m.Groups[1].ValueSpan, NumberStyles.None, CultureInfo.InvariantCulture, out var n) ? n : -1;
    }

    // Python's str.splitlines(): every line boundary it knows, \r\n as one.
    private static IEnumerable<string> SplitLines(string text)
    {
        var start = 0;
        var i = 0;
        while (i < text.Length)
        {
            var c = text[i];
            if (IsLineBreak(c))
            {
                yield return text[start..i];
                i += c == '\r' && i + 1 < text.Length && text[i + 1] == '\n' ? 2 : 1;
                start = i;
                continue;
            }
            i++;
        }
        if (start < text.Length)
        {
            yield return text[start..];
        }
    }

    // \n, \r, \v, \f, the file, group and record separators, NEL, and the
    // Unicode line and paragraph separators.
    private static bool IsLineBreak(char c) =>
        c is (char)0x0A or (char)0x0D or (char)0x0B or (char)0x0C or (char)0x1C or (char)0x1D or (char)0x1E
            or (char)0x85 or (char)0x2028 or (char)0x2029;

    // One gettext string token: the part between the quotes, unescaped.
    private static string Unquote(string s, string where)
    {
        if (s.Length < 2 || s[0] != '"' || s[^1] != '"')
        {
            throw new PoFormatException($"{where}: expected a quoted string, got {s}");
        }
        var body = s.AsSpan(1, s.Length - 2);
        var sb = new StringBuilder(body.Length);
        var i = 0;
        while (i < body.Length)
        {
            var c = body[i];
            if (c != '\\')
            {
                sb.Append(c);
                i++;
                continue;
            }
            i++;
            if (i >= body.Length)
            {
                throw new PoFormatException($"{where}: trailing backslash");
            }
            var e = body[i];
            switch (e)
            {
                case 'n': sb.Append('\n'); i++; continue;
                case 't': sb.Append('\t'); i++; continue;
                case 'r': sb.Append('\r'); i++; continue;
                case '"': sb.Append('"'); i++; continue;
                case '\\': sb.Append('\\'); i++; continue;
                case 'a': sb.Append('\a'); i++; continue;
                case 'b': sb.Append('\b'); i++; continue;
                case 'f': sb.Append('\f'); i++; continue;
                case 'v': sb.Append('\v'); i++; continue;
            }
            if (e is >= '0' and <= '7')
            {
                var j = i;
                var value = 0;
                while (j < body.Length && j < i + 3 && body[j] is >= '0' and <= '7')
                {
                    value = value * 8 + (body[j] - '0');
                    j++;
                }
                sb.Append((char)value);
                i = j;
            }
            else if (e == 'x')
            {
                var j = i + 1;
                var value = 0;
                while (j < body.Length && j < i + 3 && char.IsAsciiHexDigit(body[j]))
                {
                    var h = body[j];
                    value = value * 16 + (h <= '9' ? h - '0' : (h | 0x20) - 'a' + 10);
                    j++;
                }
                if (j == i + 1)
                {
                    throw new PoFormatException($"{where}: bad \\x escape");
                }
                sb.Append((char)value);
                i = j;
            }
            else
            {
                throw new PoFormatException($"{where}: unknown escape \\{e}");
            }
        }
        return sb.ToString();
    }

    [GeneratedRegex("^(msgctxt|msgid_plural|msgid|msgstr\\[([0-9]+)\\]|msgstr)\\s+(\".*\")\\s*$", RegexOptions.CultureInvariant)]
    private static partial Regex KeywordPattern();

    [GeneratedRegex("^(\".*\")\\s*$", RegexOptions.CultureInvariant)]
    private static partial Regex ContinuationPattern();

    [GeneratedRegex("nplurals\\s*=\\s*([0-9]+)", RegexOptions.CultureInvariant)]
    private static partial Regex NPluralsPattern();

    // The state machine of parse_po.
    private sealed class Parser(string name)
    {
        private readonly List<PoEntry> entries = [];
        private PoEntry? current;
        private string? field; // which string the next continuation extends
        private int fieldIndex;
        private int lineNumber;

        public List<PoEntry> Finish()
        {
            Close();
            return entries;
        }

        public void Line(string raw, int number)
        {
            lineNumber = number;
            var where = $"{name}:{number}";
            var line = raw.Trim();
            if (line.Length == 0)
            {
                Close();
                return;
            }
            var obsolete = false;
            if (line.StartsWith("#~", StringComparison.Ordinal))
            {
                obsolete = true;
                line = line[2..].Trim();
                if (line.Length == 0)
                {
                    return;
                }
                if (line[0] is '|' or ',' or '.' or ':' or '#')
                {
                    // A comment on an obsolete entry: msgmerge writes the
                    // previous msgid as "#~| msgid" and flags as "#~, fuzzy".
                    line = "#" + line;
                }
            }
            if (line.StartsWith('#'))
            {
                // PO files separate entries with blank lines; be lenient and
                // let a comment after a msgstr start the next entry too.
                if (current is not null && field is "msgstr" or "msgstr[]")
                {
                    Close();
                }
                var e = Target();
                if (obsolete)
                {
                    e.Obsolete = true;
                }
                if (line.StartsWith("#,", StringComparison.Ordinal))
                {
                    foreach (var part in line[2..].Split(','))
                    {
                        var flag = part.Trim();
                        if (flag.Length > 0)
                        {
                            e.AddFlag(flag);
                        }
                    }
                }
                return;
            }
            var m = KeywordPattern().Match(line);
            if (m.Success)
            {
                var keyword = m.Groups[1].Value;
                // A new entry without a separating blank line. (The script
                // also closes an entry that has only seen comments here.)
                if ((keyword == "msgctxt" && field is not null) || (keyword == "msgid" && field is "msgstr" or "msgstr[]"))
                {
                    Close();
                }
                if (keyword == "msgid" && current is not null && field == "msgid")
                {
                    throw new PoFormatException($"{where}: duplicate msgid in one entry");
                }
                var e = Target();
                if (obsolete)
                {
                    e.Obsolete = true;
                }
                if (keyword.StartsWith("msgstr[", StringComparison.Ordinal))
                {
                    field = "msgstr[]";
                    if (!int.TryParse(m.Groups[2].ValueSpan, NumberStyles.None, CultureInfo.InvariantCulture, out fieldIndex))
                    {
                        throw new PoFormatException($"{where}: bad plural index {m.Groups[2].Value}");
                    }
                }
                else
                {
                    field = keyword;
                }
                Append(e, Unquote(m.Groups[3].Value, where));
                return;
            }
            m = ContinuationPattern().Match(line);
            if (m.Success)
            {
                if (current is null || field is null)
                {
                    throw new PoFormatException($"{where}: continuation string without a keyword");
                }
                if (obsolete)
                {
                    current.Obsolete = true;
                }
                Append(current, Unquote(m.Groups[1].Value, where));
                return;
            }
            throw new PoFormatException($"{where}: cannot parse {raw}");
        }

        private PoEntry Target() => current ??= new PoEntry(lineNumber);

        private void Close()
        {
            if (current is not null)
            {
                entries.Add(current);
            }
            current = null;
            field = null;
        }

        private void Append(PoEntry e, string value)
        {
            switch (field)
            {
                case "msgctxt":
                    e.Msgctxt = (e.Msgctxt ?? "") + value;
                    break;
                case "msgid":
                    e.Msgid += value;
                    break;
                case "msgid_plural":
                    e.MsgidPlural = (e.MsgidPlural ?? "") + value;
                    break;
                case "msgstr":
                    e.Msgstr += value;
                    break;
                case "msgstr[]":
                    e.AppendPlural(fieldIndex, value);
                    break;
            }
        }
    }
}
