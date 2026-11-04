// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/security.md §4, docs/windows-port.md §3): the
// rule for mail text the app shows in its own chrome, which GTK and macOS
// do not apply yet (proposed for them, docs/windows-port.md §14). A display
// name or a subject is the sender's text, and Unicode lets that text
// reorder what is drawn after it: U+202E (RIGHT-TO-LEFT OVERRIDE) in a
// From name draws everything after it backwards, the "<address>" that
// follows the name in the chip's tooltip included, and in a subject turns
// "gnp.exe" into what reads as "exe.png" in the list, the reader, the
// window's caption and a toast. A control character is no better: a BEL
// makes the toast's XML invalid, and Windows then drops the notification,
// which the sender may have wanted.
//
// So the text is cleaned before it is shown (Clean) and isolated where it
// is composed with other text (Isolate): "Name <address>" keeps the name
// between U+2068 (FIRST STRONG ISOLATE) and U+2069 (POP DIRECTIONAL
// ISOLATE), so that a right-to-left name stays readable and cannot move the
// address. Only the explicit embeddings, overrides and isolates go; the
// marks (U+200E, U+200F, U+061C), the joiners and every letter stay, so
// Hebrew, Arabic and Persian names read as they were written.
// CertTrust.CleanText (Wizard) is stricter because it ports certtrust's
// rule for a certificate's fields: it drops every format character, the
// zero-width joiners that Persian, the Indic scripts and emoji sequences
// need included. WindowsFileNames drops the marks as well, because a file
// name has no use for them. A kept character can still draw nothing, so a
// subject or a name of nothing but such characters (an RLM, a ZWSP, a BOM)
// counts as empty (CleanTrimmed, IsInvisible) and takes the fallback an
// empty one takes ("(No subject)", the address) rather than a blank line.
//
// Display only: what goes to the daemon (a reply's recipients, a draft's
// subject) and what Copy Address copies stay the text as received.

using System;
using System.Globalization;
using System.Text;

namespace Malachi.Core.Text;

/// <summary>Mail text made safe to show in the app's chrome.</summary>
public static class DisplayText
{
    /// <summary>U+2068 FIRST STRONG ISOLATE: opens an isolate whose direction is its first strong character's.</summary>
    public const char FirstStrongIsolate = '\u2068';

    /// <summary>U+2069 POP DIRECTIONAL ISOLATE: closes it.</summary>
    public const char PopDirectionalIsolate = '\u2069';

    /// <summary>
    /// <paramref name="s"/> as the app shows it: the explicit bidirectional
    /// formatting characters (U+202A to U+202E: LRE, RLE, PDF, LRO, RLO;
    /// U+2066 to U+2069: LRI, RLI, FSI, PDI) removed; every control
    /// character (C0, DEL, C1, which includes the line breaks) and the line
    /// and paragraph separators (U+2028, U+2029) each replaced by a space;
    /// a lone surrogate and the non-characters U+FFFE and U+FFFF replaced
    /// by U+FFFD. The result is valid XML character data and cannot reorder
    /// text around it. Not trimmed: the texts shown trimmed take
    /// <see cref="CleanTrimmed"/>, which trims after cleaning, so a subject
    /// of only controls counts as empty. Applying it again changes nothing;
    /// "" for null.
    /// </summary>
    public static string Clean(string? s)
    {
        if (string.IsNullOrEmpty(s))
        {
            return "";
        }
        if (IsClean(s))
        {
            return s;
        }
        var b = new StringBuilder(s.Length);
        foreach (var r in s.EnumerateRunes())
        {
            // A lone surrogate comes as U+FFFD.
            var c = r.Value;
            if (IsExplicitBidiControl(c))
            {
                continue;
            }
            if (Rune.IsControl(r) || c is 0x2028 or 0x2029)
            {
                b.Append(' ');
            }
            else if (c is 0xFFFE or 0xFFFF)
            {
                b.Append('\uFFFD');
            }
            else
            {
                b.Append(r.ToString());
            }
        }
        return b.ToString();
    }

    /// <summary>
    /// <paramref name="s"/> cleaned (<see cref="Clean"/>) and without the
    /// white space at its ends, or "" when nothing is left but white space
    /// and characters that draw nothing (<see cref="IsInvisible"/>): what a
    /// subject, a name or an address shows, "" telling the caller to show
    /// its fallback ("(No subject)", the address) instead of a blank.
    /// </summary>
    public static string CleanTrimmed(string? s)
    {
        var c = Clean(s).Trim();
        return DrawsNothing(c, whiteSpaceToo: true) ? "" : c;
    }

    /// <summary>
    /// Whether <paramref name="s"/> draws nothing: it is empty, or each of
    /// its characters is one that <see cref="Clean"/> keeps and a renderer
    /// does not draw, Unicode's Default_Ignorable_Code_Point (the marks
    /// U+200E, U+200F and U+061C, U+200B ZERO WIDTH SPACE, the joiners,
    /// U+2060 WORD JOINER, U+FEFF, the variation selectors, the Hangul
    /// fillers). A subject or name of nothing else counts as none, as ""
    /// does, since a sender can pick one to make a message or a sender
    /// look nameless without taking the fallback. White space is not
    /// invisible here: GTK counts a name of spaces as a name
    /// (<c>c.Name != ""</c>); <see cref="CleanTrimmed"/> is for the texts
    /// that are trimmed.
    /// </summary>
    public static bool IsInvisible(string? s) => DrawsNothing(s ?? "", whiteSpaceToo: false);

    /// <summary>
    /// <paramref name="s"/>, cleaned (<see cref="Clean"/>), between
    /// <see cref="FirstStrongIsolate"/> and <see cref="PopDirectionalIsolate"/>,
    /// for text composed with other text ("Name &lt;address&gt;", a list of
    /// names, a sentence): its direction is its own and nothing in it
    /// reorders what surrounds it. Cleaning first means no PDI of the text
    /// can close the isolate early. "" stays "".
    /// </summary>
    public static string Isolate(string? s)
    {
        var c = Clean(s);
        return c.Length == 0 ? "" : FirstStrongIsolate + c + PopDirectionalIsolate;
    }

    /// <summary>
    /// The explicit bidirectional formatting characters: the embeddings and
    /// overrides with their pop (U+202A to U+202E) and the isolates with
    /// theirs (U+2066 to U+2069). Unicode's other bidi characters, the
    /// marks, only act on the neutral characters next to them.
    /// </summary>
    internal static bool IsExplicitBidiControl(int c) => c is (>= 0x202A and <= 0x202E) or (>= 0x2066 and <= 0x2069);

    // Whether every character of s is default ignorable, or white space
    // where whiteSpaceToo says so.
    private static bool DrawsNothing(string s, bool whiteSpaceToo)
    {
        foreach (var r in s.EnumerateRunes())
        {
            if (!IsDefaultIgnorable(r) && !(whiteSpaceToo && Rune.IsWhiteSpace(r)))
            {
                return false;
            }
        }
        return true;
    }

    // Unicode's Default_Ignorable_Code_Point, derived as DerivedCoreProperties
    // derives it (.NET has no API for it): the format characters (Cf) but
    // the interlinear annotation characters, the Egyptian hieroglyph format
    // controls and the prepended concatenation marks (the Arabic number
    // signs and their kin, which draw a sign), plus the variation
    // selectors and Other_Default_Ignorable_Code_Point (the combining
    // grapheme joiner, the Hangul fillers, the Khmer inherent vowels, the
    // reserved ranges set aside for such characters).
    private static bool IsDefaultIgnorable(Rune r)
    {
        var c = r.Value;
        if (Rune.GetUnicodeCategory(r) == UnicodeCategory.Format)
        {
            return c is not ((>= 0xFFF9 and <= 0xFFFB) or (>= 0x13430 and <= 0x1343F)
                or (>= 0x0600 and <= 0x0605) or 0x06DD or 0x070F or 0x0890 or 0x0891 or 0x08E2 or 0x110BD or 0x110CD);
        }
        return c is 0x034F or 0x115F or 0x1160 or 0x17B4 or 0x17B5 or (>= 0x180B and <= 0x180F) or 0x2065
            or 0x3164 or (>= 0xFE00 and <= 0xFE0F) or 0xFFA0 or (>= 0xFFF0 and <= 0xFFF8) or (>= 0xE0000 and <= 0xE0FFF);
    }

    // Whether Clean would change nothing: the common case, answered without
    // an allocation.
    private static bool IsClean(string s)
    {
        for (var i = 0; i < s.Length; i++)
        {
            var ch = s[i];
            if (char.IsHighSurrogate(ch) && i + 1 < s.Length && char.IsLowSurrogate(s[i + 1]))
            {
                i++;
                continue;
            }
            if (ch < 0x20 || ch is (>= '\u007F' and <= '\u009F') || char.IsSurrogate(ch)
                || IsExplicitBidiControl(ch) || ch is '\u2028' or '\u2029' or '\uFFFE' or '\uFFFF')
            {
                return false;
            }
        }
        return true;
    }
}
