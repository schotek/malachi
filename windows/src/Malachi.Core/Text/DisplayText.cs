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
// name has no use for them.
//
// Display only: what goes to the daemon (a reply's recipients, a draft's
// subject) and what Copy Address copies stay the text as received.

using System;
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
    /// text around it. Not trimmed: callers trim what they show, after
    /// cleaning, so a subject of only controls counts as empty. Applying it
    /// again changes nothing; "" for null.
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
