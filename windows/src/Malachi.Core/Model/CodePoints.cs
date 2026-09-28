// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// No Swift counterpart: the string order and case folding of Go that
// ui/internal/window/model.go (sortSiblings, strings.ToLower) and
// collapse.go / favourites.go (sort.Strings) rely on. Go compares strings
// byte by byte, which for UTF-8 is the order of the code points; C#'s
// ordinal comparison orders UTF-16 units, which differs from it for the
// characters above U+FFFF against those from U+E000 to U+FFFF. Go lowers
// rune by rune (unicode.ToLower); so does ToLower here.

using System;
using System.Collections.Generic;
using System.Text;

namespace Malachi.Core.Model;

/// <summary>Go's string order and case folding over code points.</summary>
internal static class CodePoints
{
    /// <summary>Orders strings as Go's <c>&lt;</c> and <c>sort.Strings</c> do.</summary>
    public static IComparer<string> Comparer { get; } = Comparer<string>.Create(Compare);

    /// <summary>
    /// Compares <paramref name="a"/> and <paramref name="b"/> by code points,
    /// which is the order of their UTF-8 bytes.
    /// </summary>
    public static int Compare(string a, string b)
    {
        var ea = a.EnumerateRunes();
        var eb = b.EnumerateRunes();
        while (true)
        {
            var hasA = ea.MoveNext();
            var hasB = eb.MoveNext();
            if (!hasA || !hasB)
            {
                return hasA ? 1 : hasB ? -1 : 0;
            }
            var c = ea.Current.Value.CompareTo(eb.Current.Value);
            if (c != 0)
            {
                return c;
            }
        }
    }

    /// <summary>
    /// <paramref name="s"/> with every rune lowered on its own, as Go's
    /// <c>strings.ToLower</c> does (no culture, no special casing).
    /// </summary>
    public static string ToLower(string s)
    {
        var b = new StringBuilder(s.Length);
        Span<char> units = stackalloc char[2];
        foreach (var r in s.EnumerateRunes())
        {
            var n = Rune.ToLowerInvariant(r).EncodeToUtf16(units);
            b.Append(units[..n]);
        }
        return b.ToString();
    }
}
