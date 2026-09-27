// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AttachmentChips.swift
// (safeFileName, uniqueName, fileName), itself the mirror of
// backend/internal/safename (Filename); GTK: ui/internal/window/
// attachments.go (uniqueName, fileName). What Windows adds is its own
// naming rules (docs/windows-port.md §10): reserved characters and what the
// best-fit conversion into an ANSI code page turns into them, alternate
// data streams, trailing dots and spaces, device names, the length of a
// path.

using System;
using System.Collections.Frozen;
using System.Collections.Generic;
using System.Globalization;
using System.Text;

namespace Malachi.Core.Platform;

/// <summary>
/// Names for the files the client writes out of a message. They are server
/// data: the daemon sanitises them already (safename), this is defence in
/// depth, and it makes them names Windows stores as given.
/// </summary>
public static class WindowsFileNames
{
    /// <summary>
    /// The longest name produced, in UTF-8 bytes, as safename
    /// <c>MaxBytes</c> (and never more than 255 UTF-16 code units, the NTFS
    /// limit of one path component).
    /// </summary>
    public const int MaxBytes = 255;

    /// <summary>The longest name produced, in UTF-16 code units (NTFS).</summary>
    public const int MaxLength = 255;

    /// <summary>
    /// The shortest cap <see cref="Sanitize(string, int)"/> takes: room for
    /// the fallback and an extension of 16 bytes.
    /// </summary>
    public const int MinLength = 32;

    /// <summary>What is left when nothing usable remains (safename <c>Fallback</c>).</summary>
    public const string Fallback = "attachment";

    // The names Windows keeps for devices, with or without an extension:
    // "NUL.txt" and "nul .tar.gz" are NUL. The superscript digits count as
    // digits for COM and LPT; CLOCK$ as Chromium keeps it.
    private static readonly FrozenSet<string> DeviceNames = new[]
    {
        "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "CLOCK$",
        "COM0", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
        "COM¹", "COM²", "COM³",
        "LPT0", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
        "LPT¹", "LPT²", "LPT³",
    }.ToFrozenSet(StringComparer.OrdinalIgnoreCase);

    // Names the shell reads as settings of the folder they lie in (its
    // icon, its class): a desktop.ini saved into Downloads would restyle
    // it, and can make Explorer fetch a picture from another host.
    private static readonly FrozenSet<string> ShellNames = new[]
    {
        "desktop.ini", "thumbs.db",
    }.ToFrozenSet(StringComparer.OrdinalIgnoreCase);

    // The characters Windows keeps out of names ('/' and '\' never get
    // here: only the last path component is left), and their look-alikes.
    // A program that reads its command line in the ANSI code page gets the
    // path of the file it opens through Windows' best-fit conversion, which
    // turns these back into reserved ones; a '"' that comes back splits the
    // command line and passes the rest of the name as arguments (WorstFit).
    // Measured with WideCharToMultiByte on Windows 11 26100: everything
    // code page 1252 turns into < > : " / \ | ? *, and everything any ANSI
    // code page turns into '"' except „ (U+201E: a '"' only in code page
    // 874, and the opening quote of Czech and German). What the other code
    // pages add (¥ in 932, ¦ in 932–950, ₩ in 949, ´ in 1253, „ in 874,
    // ← → in 1251 and 1253, ↕ ↨ in 1250 and 1254, ► ◄ ♂ in 1250, 1251,
    // 1253 and 1254) are ordinary characters of names everywhere else: the
    // caller passes those of this machine's code page (the lookAlikes of
    // Sanitize, from Malachi.Platform.Windows.Files.AnsiLookAlikes).
    private static readonly FrozenSet<char> ReservedCharacters = new[]
    {
        '<', '>', ':', '"', '|', '?', '*',
        // The full-width forms.
        '＂', '＊', '／', '：', '＜', '＞', '？', '＼', '｜',
        // Code page 1252: ǀ ʺ ̎ ։ ‟ ″ ‶ ⁄ ∕ ∖ ∗ ∣ ∶ 〈 〉 ❘ 〈 〉.
        'ǀ', 'ʺ', '̎', '։', '‟', '″', '‶', '⁄', '∕',
        '∖', '∗', '∣', '∶', '〈', '〉', '❘', '〈', '〉',
        // The '"' of code page 1254 (〝 〞), and the ditto mark 〃, which
        // looks like one.
        '〝', '〞', '〃',
    }.ToFrozenSet();

    /// <summary>
    /// <see cref="Sanitize(string, int)"/> with the longest name Windows
    /// allows.
    /// </summary>
    public static string Sanitize(string? raw) => Sanitize(raw, MaxLength, null);

    /// <summary>
    /// <see cref="Sanitize(string, int, IReadOnlySet{char})"/> without
    /// look-alikes of this machine's own.
    /// </summary>
    public static string Sanitize(string? raw, int maxLength) => Sanitize(raw, maxLength, null);

    /// <summary>
    /// A file name Windows stores exactly as returned (AttachmentChips.swift
    /// <c>safeFileName</c>, safename <c>Filename</c>): the last path
    /// component only (both separators), no control characters, no
    /// bidirectional formatting characters (a U+202E before "gnp.exe" makes
    /// "photo.exe.png" appear on screen), no U+FFFD or broken surrogates,
    /// surrounding white space and leading dots removed. Then Windows' own
    /// rules: <c>&lt; &gt; : " | ? *</c> and their look-alikes become
    /// <c>_</c> (<c>:</c> would write an alternate data stream, and the
    /// look-alikes turn back into the reserved ones when a program reads
    /// its command line in an ANSI code page: the full-width forms, what
    /// code page 1252 maps to them, every look-alike of <c>"</c>, and
    /// <paramref name="lookAlikes"/>, those of this machine's code page),
    /// trailing dots and spaces go (Windows would drop them silently), a
    /// device name gets a leading <c>_</c> ("CON.txt" is the console), and
    /// the result has at most <see cref="MaxBytes"/> bytes and
    /// <paramref name="maxLength"/> characters, a short extension kept.
    /// Whatever the cap, the result's extension is the one the name has
    /// after those rules, or none: a cut through a long one turns the dots
    /// it leaves into <c>_</c>. Never empty; applying it again changes
    /// nothing.
    /// </summary>
    public static string Sanitize(string? raw, int maxLength, IReadOnlySet<char>? lookAlikes)
    {
        ArgumentOutOfRangeException.ThrowIfLessThan(maxLength, MinLength);
        ArgumentOutOfRangeException.ThrowIfGreaterThan(maxLength, MaxLength);
        var s = raw ?? "";
        var separator = s.LastIndexOfAny(['/', '\\']);
        if (separator >= 0)
        {
            s = s[(separator + 1)..];
        }
        var kept = new StringBuilder(s.Length);
        foreach (var r in s.EnumerateRunes())
        {
            // A lone surrogate comes out as U+FFFD, and goes with it.
            if (Rune.IsControl(r) || BidiControl(r.Value) || r.Value == 0xFFFD)
            {
                continue;
            }
            if (r.IsBmp && (ReservedCharacters.Contains((char)r.Value) || lookAlikes?.Contains((char)r.Value) == true))
            {
                kept.Append('_');
                continue;
            }
            kept.Append(r.ToString());
        }
        s = kept.ToString().Trim().TrimStart('.').Trim();
        s = TrimEnd(s);
        if (s.Length == 0)
        {
            return Fallback;
        }
        // Bounded, then renamed off a device name; the leading "_" can take
        // the name over the bound once, and the second pass cuts it back.
        while (true)
        {
            s = Fit(s, maxLength);
            if (!IsReservedName(s))
            {
                return s;
            }
            s = "_" + s;
        }
    }

    /// <summary>
    /// Whether Windows reads <paramref name="name"/> as a device (CON, NUL,
    /// COM1, LPT¹, CONIN$, with or without an extension, in any case, with
    /// spaces before the dot) or the shell reads it as the settings of its
    /// folder (desktop.ini, thumbs.db).
    /// </summary>
    public static bool IsReservedName(string? name)
    {
        var s = name ?? "";
        if (ShellNames.Contains(s))
        {
            return true;
        }
        var dot = s.IndexOf('.', StringComparison.Ordinal);
        var stem = (dot < 0 ? s : s[..dot]).TrimEnd(' ');
        return DeviceNames.Contains(stem);
    }

    /// <summary>
    /// <paramref name="name"/>, or <paramref name="name"/> with " (2)",
    /// " (3)", … before the extension until <paramref name="taken"/> says no
    /// (AttachmentChips.swift <c>uniqueName</c>, attachments.go
    /// <c>uniqueName</c>). It gives up after 1000 tries and returns
    /// <paramref name="name"/>. A candidate that would outgrow
    /// <paramref name="maxLength"/> characters or <see cref="MaxBytes"/>
    /// bytes is shortened before the number, so it stays a name Windows
    /// can create. <paramref name="taken"/> should compare as the file
    /// system does, without regard to case.
    /// </summary>
    public static string UniqueName(string name, Func<string, bool> taken, int maxLength = MaxLength)
    {
        ArgumentNullException.ThrowIfNull(name);
        ArgumentNullException.ThrowIfNull(taken);
        ArgumentOutOfRangeException.ThrowIfLessThan(maxLength, MinLength);
        ArgumentOutOfRangeException.ThrowIfGreaterThan(maxLength, MaxLength);
        if (!taken(name))
        {
            return name;
        }
        var stem = name;
        var ext = "";
        var dot = name.LastIndexOf('.');
        // An "extension" that would leave no room for the number and a
        // character before it counts as part of the name.
        if (dot > 0 && maxLength - (name.Length - dot) >= 8 && MaxBytes - Utf8(name[dot..]) >= 8)
        {
            stem = name[..dot];
            ext = name[dot..];
        }
        for (var n = 2; n < 1000; n++)
        {
            var suffix = " (" + n.ToString(CultureInfo.InvariantCulture) + ")" + ext;
            var candidate = Shorten(stem, maxLength - suffix.Length, MaxBytes - Utf8(suffix)) + suffix;
            if (!taken(candidate))
            {
                return candidate;
            }
        }
        return name;
    }

    /// <summary>
    /// The name a fetched part is written under (AttachmentChips.swift
    /// <c>fileName</c>, attachments.go <c>fileName</c>): what message.part
    /// reported (<paramref name="served"/>), else the listed name, else
    /// <see cref="Fallback"/>; each through
    /// <see cref="Sanitize(string, int, IReadOnlySet{char})"/> with this
    /// machine's <paramref name="lookAlikes"/>.
    /// </summary>
    public static string FileName(string? served, string? listed, IReadOnlySet<char>? lookAlikes = null)
    {
        foreach (var candidate in new[] { served ?? "", listed ?? "" })
        {
            var n = Sanitize(candidate, MaxLength, lookAlikes);
            if (n != Fallback)
            {
                return n;
            }
        }
        return Fallback;
    }

    // safename bidiControl: the Unicode bidirectional formatting
    // characters, which are not control characters to the category test.
    private static bool BidiControl(int c) =>
        c == 0x061C // ARABIC LETTER MARK
        || c is 0x200E or 0x200F // LRM, RLM
        || c is >= 0x202A and <= 0x202E // LRE, RLE, PDF, LRO, RLO
        || c is >= 0x2066 and <= 0x2069; // LRI, RLI, FSI, PDI

    // Windows drops trailing dots and spaces from a name; so does this, and
    // any other white space they uncover.
    private static string TrimEnd(string s)
    {
        var end = s.Length;
        while (end > 0 && (s[end - 1] == '.' || char.IsWhiteSpace(s[end - 1])))
        {
            end--;
        }
        return s[..end];
    }

    // safename truncate: at most MaxBytes bytes and maxLength characters,
    // cut on a scalar boundary, keeping an extension of at most 16 bytes;
    // the fallback before the extension when nothing else fits. A longer
    // "extension" is cut with the rest, and that cut must not leave a
    // shorter one in its place: "x.exeabcdefghijklmnopq" cut after "exe"
    // would be a program no check saw, the cap depends on the length of
    // the profile path, and that can be guessed from an address.
    private static string Fit(string s, int maxLength)
    {
        if (s.Length <= maxLength && Utf8(s) <= MaxBytes)
        {
            return s;
        }
        var ext = "";
        var dot = s.LastIndexOf('.');
        if (dot > 0 && Utf8(s[dot..]) <= 16)
        {
            ext = s[dot..];
        }
        var stem = Shorten(s[..^ext.Length], maxLength - ext.Length, MaxBytes - Utf8(ext));
        if (ext.Length > 0)
        {
            return stem.Length == 0 ? Fallback + ext : stem + ext;
        }
        stem = WithoutNewExtension(TrimEnd(stem), Extension(s));
        return stem.Length == 0 ? Fallback : stem;
    }

    // A cut that dropped the extension: its dots become "_", from the last
    // one, until what follows the last dot is the uncut name's extension
    // or there is no dot, so a cut never gives the file a type of its own.
    // Nothing the lists name contains "_" (nor does a class ID), so the
    // joined parts are no type either.
    private static string WithoutNewExtension(string cut, string extension)
    {
        while (true)
        {
            var dot = cut.LastIndexOf('.');
            if (dot < 0 || string.Equals(cut[(dot + 1)..], extension, StringComparison.OrdinalIgnoreCase))
            {
                return cut;
            }
            cut = string.Concat(cut.AsSpan(0, dot), "_", cut.AsSpan(dot + 1));
        }
    }

    // Go's filepath.Ext without the dot, of a name without separators.
    private static string Extension(string name)
    {
        var dot = name.LastIndexOf('.');
        return dot < 0 ? "" : name[(dot + 1)..];
    }

    // Drops scalars from the end of s until it has at most chars characters
    // and bytes UTF-8 bytes.
    private static string Shorten(string s, int chars, int bytes)
    {
        while (s.Length > 0 && (s.Length > chars || Utf8(s) > bytes))
        {
            var cut = char.IsLowSurrogate(s[^1]) && s.Length > 1 && char.IsHighSurrogate(s[^2]) ? 2 : 1;
            s = s[..^cut];
        }
        return s;
    }

    private static int Utf8(string s) => Encoding.UTF8.GetByteCount(s);
}
