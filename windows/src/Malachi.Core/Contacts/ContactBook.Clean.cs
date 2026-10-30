// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Contacts/ContactBookDetail.swift (clean,
// cleanBlock, isBidiOrInvisible, cut, trimSpace, fields, firstLetter,
// isLetter, upper); GTK: ui/internal/contacts detail.go (Clean, CleanBlock,
// isBidiOrInvisible, cut) and Go's strings.TrimSpace, strings.Fields and
// unicode.ToUpper.
//
// A string of a server as display text: control, bidi and invisible format
// characters dropped, white space runs as one space, cut on a character
// boundary to a number of UTF-8 bytes. Go's rules, on runes: White_Space is
// unicode.IsSpace, Cc unicode.IsControl, Cf unicode.Is(unicode.Cf); a lone
// surrogate reads as U+FFFD and is dropped, as Go drops utf8.RuneError.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;

namespace Malachi.Core.Contacts;

public static partial class ContactBook
{
    /// <summary>
    /// contacts.Clean: a server's string as one line of display text:
    /// control, bidi and invisible format characters dropped, white space
    /// runs as one space, trimmed, cut to <paramref name="max"/> UTF-8 bytes
    /// on a character boundary (<paramref name="max"/> ≤ 0: no cut). Null
    /// reads as "".
    /// </summary>
    public static string Clean(string? s, int max)
    {
        var b = new StringBuilder((s ?? "").Length);
        var space = false;
        foreach (var r in (s ?? "").EnumerateRunes())
        {
            if (r == Rune.ReplacementChar || IsBidiOrInvisible(r))
            {
                continue;
            }
            if (IsGoSpace(r.Value) || Rune.GetUnicodeCategory(r) == UnicodeCategory.Control)
            {
                space = true;
                continue;
            }
            if (space && b.Length > 0)
            {
                b.Append(' ');
            }
            space = false;
            b.Append(r.ToString());
        }
        return Cut(b.ToString(), max);
    }

    /// <summary>contacts.CleanBlock: <see cref="Clean"/> that keeps line breaks (a note): every line cleaned, at most two empty lines in a row, trimmed.</summary>
    public static string CleanBlock(string? s, int max)
    {
        var unified = (s ?? "").Replace("\r\n", "\n", StringComparison.Ordinal).Replace('\r', '\n');
        var output = new List<string>();
        var empty = 0;
        foreach (var line in unified.Split('\n'))
        {
            var l = Clean(line, 0);
            if (l.Length == 0)
            {
                empty++;
                if (empty > 2)
                {
                    continue;
                }
            }
            else
            {
                empty = 0;
            }
            output.Add(l);
        }
        return Cut(TrimSpace(string.Join('\n', output)), max);
    }

    private static bool IsBidiOrInvisible(Rune r) => r.Value switch
    {
        (>= 0x202A and <= 0x202E) or (>= 0x2066 and <= 0x2069) or 0x200E or 0x200F or 0x061C => true,
        0xFEFF or (>= 0x200B and <= 0x200D) or 0x2060 or 0x00AD => true,
        _ => Rune.GetUnicodeCategory(r) == UnicodeCategory.Format,
    };

    // contacts.cut: at most max UTF-8 bytes on a character boundary, trimmed;
    // max ≤ 0 or a shorter string as it is.
    private static string Cut(string s, int max)
    {
        if (max <= 0 || Encoding.UTF8.GetByteCount(s) <= max)
        {
            return s;
        }
        var b = new StringBuilder();
        var bytes = 0;
        foreach (var r in s.EnumerateRunes())
        {
            if (bytes + r.Utf8SequenceLength > max)
            {
                break;
            }
            bytes += r.Utf8SequenceLength;
            b.Append(r.ToString());
        }
        return TrimSpace(b.ToString());
    }

    // unicode.IsSpace: the White_Space property.
    internal static bool IsGoSpace(int r) => r switch
    {
        (>= 0x09 and <= 0x0D) or 0x20 or 0x85 or 0xA0 or 0x1680 or (>= 0x2000 and <= 0x200A)
            or 0x2028 or 0x2029 or 0x202F or 0x205F or 0x3000 => true,
        _ => false,
    };

    /// <summary>strings.TrimSpace: White_Space at both ends goes.</summary>
    internal static string TrimSpace(string? s)
    {
        s ??= "";
        var start = 0;
        var end = s.Length;
        while (start < end && Rune.DecodeFromUtf16(s.AsSpan(start), out var r, out var n) == System.Buffers.OperationStatus.Done && IsGoSpace(r.Value))
        {
            start += n;
        }
        while (end > start && Rune.DecodeLastFromUtf16(s.AsSpan(start, end - start), out var r, out var n) == System.Buffers.OperationStatus.Done && IsGoSpace(r.Value))
        {
            end -= n;
        }
        return s[start..end];
    }

    /// <summary>strings.Fields: the runs between White_Space.</summary>
    internal static List<string> Fields(string? s)
    {
        var output = new List<string>();
        var cur = new StringBuilder();
        foreach (var r in (s ?? "").EnumerateRunes())
        {
            if (IsGoSpace(r.Value))
            {
                if (cur.Length > 0)
                {
                    output.Add(cur.ToString());
                    cur.Clear();
                }
            }
            else
            {
                cur.Append(r.ToString());
            }
        }
        if (cur.Length > 0)
        {
            output.Add(cur.ToString());
        }
        return output;
    }

    // The first letter or decimal digit of s.
    private static Rune? FirstLetter(string? s)
    {
        foreach (var r in (s ?? "").EnumerateRunes())
        {
            if (Rune.IsLetter(r) || Rune.GetUnicodeCategory(r) == UnicodeCategory.DecimalDigitNumber)
            {
                return r;
            }
        }
        return null;
    }

    // unicode.ToUpper of one character: the simple mapping.
    private static string Upper(Rune r) => Rune.ToUpperInvariant(r).ToString();
}
