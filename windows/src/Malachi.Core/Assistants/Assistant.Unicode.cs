// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantUnicode.swift; GTK:
// the rules of Go's unicode, unicode/utf8 and encoding/json packages that
// ui/internal/assistant relies on, so that the port reads hostile text
// exactly as the Go package does: byte offsets into UTF-8, Go's classes
// (unicode.IsSpace is the White_Space property, unicode.IsControl only the
// C0 and C1 controls, never .NET's char.IsControl or char.IsWhiteSpace,
// which differ from them), U+FFFD for bad UTF-8 one byte at a time as
// utf8.DecodeRune reads it (.NET's Rune.DecodeFromUtf8 takes a maximal
// subpart instead), and JSON strings written as encoding/json writes them.
// A C# string holds no invalid UTF-8; a lone surrogate becomes U+FFFD when
// it is encoded (Utf8). No texts here.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Text;

namespace Malachi.Core.Assistants;

/// <summary>
/// ui/internal/assistant: the Assistant menu's hand-off to Claude Desktop and
/// Claude Code, the assistant panel's command line, stream and Markdown
/// subset, and the one-shot requests' prompts. A namespace of functions, so
/// that the Go and Swift names map 1:1 (<c>assistant.Prompt</c>, Swift
/// <c>Assistant.prompt</c>, here <c>Assistant.Prompt</c>).
/// </summary>
public static partial class Assistant
{
    // UTF-8

    /// <summary>The UTF-8 form of <paramref name="s"/>; a lone surrogate is U+FFFD.</summary>
    internal static byte[] Utf8(string s) => Encoding.UTF8.GetBytes(s);

    /// <summary>
    /// <paramref name="b"/> as a string. Every byte buffer this class builds
    /// is valid UTF-8; for anything else each maximal invalid subpart is one
    /// U+FFFD, as Swift's <c>String(decoding:as:)</c> reads it.
    /// </summary>
    internal static string FromUtf8(ReadOnlySpan<byte> b) => Encoding.UTF8.GetString(b);

    /// <summary>
    /// utf8.DecodeRune of <paramref name="b"/>[<paramref name="i"/>..<paramref name="end"/>):
    /// the scalar and its length in bytes; (U+FFFD, 1) for an invalid,
    /// overlong, surrogate or short sequence. <paramref name="i"/> is below
    /// <paramref name="end"/>.
    /// </summary>
    internal static (int Rune, int Size) DecodeRune(ReadOnlySpan<byte> b, int i, int end)
    {
        var c0 = b[i];
        if (c0 < 0x80)
        {
            return (c0, 1);
        }
        var n = end - i;
        if (c0 < 0xC2 || c0 > 0xF4)
        {
            return (0xFFFD, 1);
        }
        if (c0 < 0xE0)
        {
            if (n < 2 || (b[i + 1] & 0xC0) != 0x80)
            {
                return (0xFFFD, 1);
            }
            return (((c0 & 0x1F) << 6) | (b[i + 1] & 0x3F), 2);
        }
        if (c0 < 0xF0)
        {
            var lo3 = c0 == 0xE0 ? 0xA0 : 0x80;
            var hi3 = c0 == 0xED ? 0x9F : 0xBF;
            if (n < 3 || b[i + 1] < lo3 || b[i + 1] > hi3 || (b[i + 2] & 0xC0) != 0x80)
            {
                return (0xFFFD, 1);
            }
            return (((c0 & 0x0F) << 12) | ((b[i + 1] & 0x3F) << 6) | (b[i + 2] & 0x3F), 3);
        }
        var lo4 = c0 == 0xF0 ? 0x90 : 0x80;
        var hi4 = c0 == 0xF4 ? 0x8F : 0xBF;
        if (n < 4 || b[i + 1] < lo4 || b[i + 1] > hi4 || (b[i + 2] & 0xC0) != 0x80 || (b[i + 3] & 0xC0) != 0x80)
        {
            return (0xFFFD, 1);
        }
        return (((c0 & 0x07) << 18) | ((b[i + 1] & 0x3F) << 12) | ((b[i + 2] & 0x3F) << 6) | (b[i + 3] & 0x3F), 4);
    }

    /// <summary>
    /// utf8.DecodeLastRune of <paramref name="b"/>[<paramref name="lo"/>..<paramref name="end"/>):
    /// the last scalar and its length; (U+FFFD, 0) when the range is empty,
    /// (U+FFFD, 1) when it does not end with a whole character.
    /// </summary>
    internal static (int Rune, int Size) DecodeLastRune(ReadOnlySpan<byte> b, int lo, int end)
    {
        if (end <= lo)
        {
            return (0xFFFD, 0);
        }
        var start = end - 1;
        if (b[start] < 0x80)
        {
            return (b[start], 1);
        }
        var lim = Math.Max(end - 4, lo);
        start--;
        while (start >= lim)
        {
            if ((b[start] & 0xC0) != 0x80)
            {
                break;
            }
            start--;
        }
        if (start < lo)
        {
            start = lo;
        }
        var (r, size) = DecodeRune(b, start, end);
        if (start + size != end)
        {
            return (0xFFFD, 1);
        }
        return (r, size);
    }

    /// <summary>utf8.AppendRune: a scalar's UTF-8; U+FFFD for a surrogate or a value past U+10FFFF.</summary>
    internal static void AppendUtf8(int r, List<byte> output)
    {
        switch (r)
        {
            case >= 0 and < 0x80:
                output.Add((byte)r);
                break;
            case >= 0x80 and < 0x800:
                output.Add((byte)(0xC0 | (r >> 6)));
                output.Add((byte)(0x80 | (r & 0x3F)));
                break;
            case (>= 0xD800 and <= 0xDFFF) or < 0 or >= 0x110000:
                output.Add(0xEF);
                output.Add(0xBF);
                output.Add(0xBD);
                break;
            case < 0x10000:
                output.Add((byte)(0xE0 | (r >> 12)));
                output.Add((byte)(0x80 | ((r >> 6) & 0x3F)));
                output.Add((byte)(0x80 | (r & 0x3F)));
                break;
            default:
                output.Add((byte)(0xF0 | (r >> 18)));
                output.Add((byte)(0x80 | ((r >> 12) & 0x3F)));
                output.Add((byte)(0x80 | ((r >> 6) & 0x3F)));
                output.Add((byte)(0x80 | (r & 0x3F)));
                break;
        }
    }

    /// <summary>Whether byte <paramref name="c"/> starts a character (utf8.RuneStart).</summary>
    internal static bool RuneStart(byte c) => (c & 0xC0) != 0x80;

    // Character classes

    /// <summary>unicode.IsSpace: the White_Space property.</summary>
    internal static bool IsSpace(int r) => r switch
    {
        (>= 0x09 and <= 0x0D) or 0x20 or 0x85 or 0xA0 or 0x1680 or (>= 0x2000 and <= 0x200A)
            or 0x2028 or 0x2029 or 0x202F or 0x205F or 0x3000 => true,
        _ => false,
    };

    /// <summary>unicode.IsControl: the C0 and C1 controls and DEL (category Cc), nothing else.</summary>
    internal static bool IsControl(int r) => r is (>= 0 and < 0x20) or (>= 0x7F and <= 0x9F);

    /// <summary>unicode.IsLetter: category L.</summary>
    internal static bool IsLetter(int r)
    {
        if (r < 0x80)
        {
            return r is (>= 0x41 and <= 0x5A) or (>= 0x61 and <= 0x7A);
        }
        if (!Rune.IsValid(r))
        {
            return false;
        }
        return Rune.GetUnicodeCategory(new Rune(r)) is UnicodeCategory.UppercaseLetter or UnicodeCategory.LowercaseLetter
            or UnicodeCategory.TitlecaseLetter or UnicodeCategory.ModifierLetter or UnicodeCategory.OtherLetter;
    }

    /// <summary>unicode.IsDigit: category Nd.</summary>
    internal static bool IsDigit(int r)
    {
        if (r < 0x80)
        {
            return r is >= 0x30 and <= 0x39;
        }
        return Rune.IsValid(r) && Rune.GetUnicodeCategory(new Rune(r)) == UnicodeCategory.DecimalDigitNumber;
    }

    /// <summary>
    /// Go's bidiControl: the Arabic letter mark, the left-to-right and
    /// right-to-left marks, the embeddings and overrides and the isolates.
    /// </summary>
    internal static bool IsBidiControl(int r) =>
        r is 0x061C or 0x200E or 0x200F or (>= 0x202A and <= 0x202E) or (>= 0x2066 and <= 0x2069);

    /// <summary>
    /// Lower-cases the ASCII letters and nothing else, so no other
    /// character can turn into one of them (the KELVIN SIGN stays).
    /// </summary>
    internal static byte[] AsciiLower(ReadOnlySpan<byte> b)
    {
        var output = b.ToArray();
        for (var i = 0; i < output.Length; i++)
        {
            if (output[i] is >= 0x41 and <= 0x5A)
            {
                output[i] += 0x20;
            }
        }
        return output;
    }

    /// <summary>
    /// strings.TrimSpace of <paramref name="b"/>[<paramref name="lo"/>..<paramref name="hi"/>):
    /// the range without the leading and trailing White_Space characters.
    /// </summary>
    internal static (int Lo, int Hi) TrimSpace(ReadOnlySpan<byte> b, int lo, int hi)
    {
        while (lo < hi)
        {
            var (r, w) = DecodeRune(b, lo, hi);
            if (!IsSpace(r))
            {
                break;
            }
            lo += w;
        }
        while (hi > lo)
        {
            var (r, w) = DecodeLastRune(b, lo, hi);
            if (!IsSpace(r))
            {
                break;
            }
            hi -= w;
        }
        return (lo, hi);
    }

    /// <summary>
    /// Go's firstLine (assistant.go): the first line of <paramref name="s"/>
    /// that has more than spaces, without control characters (tabs
    /// included) and trimmed, cut to at most <paramref name="limit"/> bytes
    /// at a character boundary; "unknown" when there is none. Lines end at
    /// "\n" only (a "\r" is a control character).
    /// </summary>
    internal static string FirstLine(string s, int limit)
    {
        var b = Utf8(s);
        var start = 0;
        while (start <= b.Length)
        {
            var end = start;
            while (end < b.Length && b[end] != 0x0A)
            {
                end++;
            }
            var kept = new List<byte>(end - start);
            var i = start;
            while (i < end)
            {
                var (r, w) = DecodeRune(b, i, end);
                if (!IsControl(r))
                {
                    for (var k = i; k < i + w; k++)
                    {
                        kept.Add(b[k]);
                    }
                }
                i += w;
            }
            var t = kept.ToArray();
            var (lo, hi) = TrimSpace(t, 0, t.Length);
            if (hi > lo)
            {
                t = t[lo..hi];
                if (t.Length > limit)
                {
                    var cut = limit;
                    while (cut > 0 && !RuneStart(t[cut]))
                    {
                        cut--;
                    }
                    (lo, hi) = TrimSpace(t, 0, cut);
                    t = t[lo..hi];
                }
                return FromUtf8(t);
            }
            start = end + 1;
        }
        return "unknown";
    }

    /// <summary>
    /// Go's oneLine (assistant.go): mail text (a subject, written by a third
    /// party) as one line of a label: every run of White_Space (line breaks,
    /// tabs, U+2028 and U+2029 among them) one space, the other control
    /// characters and the bidirectional formatting characters (which would
    /// reorder what follows them) left out, trimmed and cut to at most
    /// <paramref name="limit"/> bytes at a character boundary.
    /// </summary>
    internal static string OneLine(string s, int limit)
    {
        var b = Utf8(s);
        var output = new List<byte>(b.Length);
        var space = false;
        var i = 0;
        while (i < b.Length)
        {
            var (r, w) = DecodeRune(b, i, b.Length);
            i += w;
            if (IsSpace(r))
            {
                space = output.Count > 0;
                continue;
            }
            if (IsControl(r) || IsBidiControl(r))
            {
                continue;
            }
            if (space)
            {
                output.Add(0x20);
                space = false;
            }
            AppendUtf8(r, output);
        }
        if (output.Count > limit)
        {
            var cut = limit;
            while (cut > 0 && !RuneStart(output[cut]))
            {
                cut--;
            }
            output.RemoveRange(cut, output.Count - cut);
            while (output.Count > 0 && output[^1] == 0x20)
            {
                output.RemoveAt(output.Count - 1);
            }
        }
        return FromUtf8(output.ToArray());
    }

    /// <summary>
    /// The first position of <paramref name="needle"/> in
    /// <paramref name="hay"/>[<paramref name="from"/>..<paramref name="to"/>),
    /// -1 when there is none; <paramref name="from"/> for an empty needle.
    /// </summary>
    internal static int IndexOf(ReadOnlySpan<byte> hay, ReadOnlySpan<byte> needle, int from, int to)
    {
        if (needle.IsEmpty)
        {
            return from;
        }
        if (to - from < needle.Length)
        {
            return -1;
        }
        var at = hay[from..to].IndexOf(needle);
        return at < 0 ? -1 : from + at;
    }

    // JSON strings

    /// <summary>
    /// Appends <paramref name="s"/> as encoding/json writes a string: quoted,
    /// <c>"</c> and <c>\</c> escaped, the control characters as \b \f \n \r
    /// \t or \u00XX, and, as Go escapes them for HTML, <c>&lt;</c>
    /// <c>&gt;</c> <c>&amp;</c> as backslash-u escapes (u003c, u003e, u0026)
    /// and U+2028 U+2029 as u2028 and u2029 escapes. The result is one line.
    /// </summary>
    internal static void AppendJsonString(string s, List<byte> output)
    {
        const string hex = "0123456789abcdef";
        output.Add((byte)'"');
        var b = Utf8(s);
        var i = 0;
        while (i < b.Length)
        {
            var c = b[i];
            if (c < 0x80)
            {
                switch (c)
                {
                    case 0x22 or 0x5C:
                        output.Add(0x5C);
                        output.Add(c);
                        break;
                    case 0x08:
                        output.Add(0x5C);
                        output.Add((byte)'b');
                        break;
                    case 0x0C:
                        output.Add(0x5C);
                        output.Add((byte)'f');
                        break;
                    case 0x0A:
                        output.Add(0x5C);
                        output.Add((byte)'n');
                        break;
                    case 0x0D:
                        output.Add(0x5C);
                        output.Add((byte)'r');
                        break;
                    case 0x09:
                        output.Add(0x5C);
                        output.Add((byte)'t');
                        break;
                    case 0x3C or 0x3E or 0x26 or < 0x20:
                        output.Add(0x5C);
                        output.Add((byte)'u');
                        output.Add((byte)'0');
                        output.Add((byte)'0');
                        output.Add((byte)hex[c >> 4]);
                        output.Add((byte)hex[c & 0x0F]);
                        break;
                    default:
                        output.Add(c);
                        break;
                }
                i++;
                continue;
            }
            var (r, w) = DecodeRune(b, i, b.Length);
            if (r is 0x2028 or 0x2029)
            {
                foreach (var ch in "\\u202")
                {
                    output.Add((byte)ch);
                }
                output.Add((byte)hex[r & 0x0F]);
            }
            else if (r == 0xFFFD && w == 1)
            {
                foreach (var ch in "\\ufffd")
                {
                    output.Add((byte)ch);
                }
            }
            else
            {
                for (var k = i; k < i + w; k++)
                {
                    output.Add(b[k]);
                }
            }
            i += w;
        }
        output.Add((byte)'"');
    }
}
