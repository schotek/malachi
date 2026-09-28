// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/scripts/po2strings.py (Entry). GTK: msgfmt compiles the same
// entries into the .mo file.

using System.Collections.Generic;

namespace Malachi.Core.I18n;

/// <summary>One entry of a PO or POT file, as <see cref="PoFile.Parse"/> read it.</summary>
public sealed class PoEntry
{
    /// <summary>gettext's EOT separator between the context and the msgid of a key.</summary>
    public const char ContextSeparator = '\u0004';

    private readonly Dictionary<int, string> msgstrPlural = [];
    private readonly HashSet<string> flags = new(System.StringComparer.Ordinal);

    internal PoEntry(int line)
    {
        Line = line;
    }

    /// <summary>The context (<c>msgctxt</c>), or null.</summary>
    public string? Msgctxt { get; internal set; }

    /// <summary>The msgid; empty for the header.</summary>
    public string Msgid { get; internal set; } = "";

    /// <summary>The plural msgid, or null for a singular entry.</summary>
    public string? MsgidPlural { get; internal set; }

    /// <summary>The translation of a singular entry; empty when untranslated.</summary>
    public string Msgstr { get; internal set; } = "";

    /// <summary>The translations of a plural entry by <c>msgstr[i]</c> index.</summary>
    public IReadOnlyDictionary<int, string> MsgstrPlural => msgstrPlural;

    /// <summary>The flags of the <c>#,</c> comments (fuzzy, c-format, no-c-format, …).</summary>
    public IReadOnlySet<string> Flags => flags;

    /// <summary>Whether the entry is obsolete (<c>#~</c>).</summary>
    public bool Obsolete { get; internal set; }

    /// <summary>The line the entry starts on, 1-based.</summary>
    public int Line { get; }

    /// <summary>The catalogue key: the msgid, or <c>msgctxt + U+0004 + msgid</c>.</summary>
    public string Key => Msgctxt is null ? Msgid : Msgctxt + ContextSeparator + Msgid;

    /// <summary>Whether this is the header (empty msgid, no context).</summary>
    public bool IsHeader => Msgid.Length == 0 && Msgctxt is null;

    /// <summary>Whether the entry is flagged fuzzy.</summary>
    public bool Fuzzy => flags.Contains("fuzzy");

    /// <summary>
    /// Whether the strings are printf formats: not flagged <c>no-c-format</c>
    /// (the strftime msgids are). po2strings.py <c>converts_format</c>.
    /// </summary>
    public bool IsPrintfFormat => !flags.Contains("no-c-format");

    internal void AddFlag(string flag) => flags.Add(flag);

    internal void AppendPlural(int index, string value) =>
        msgstrPlural[index] = msgstrPlural.TryGetValue(index, out var old) ? old + value : value;
}
