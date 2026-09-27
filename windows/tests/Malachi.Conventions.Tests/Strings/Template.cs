// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// po/malachi.pot as the strings check reads it (docs/windows-port.md §9),
// with Core's own PO parser: the msgids without a context, the pairs of
// context and msgid, and the plural msgid of every plural entry. The
// counterpart of po2strings.py's parsing in macos/scripts/check-strings.py.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using Malachi.Core.I18n;

namespace Malachi.Conventions.Tests.Strings;

/// <summary>The gettext template of the GTK UI.</summary>
internal sealed class Template
{
    private static readonly Lazy<Template> Loaded = new(() => Load(Path.Combine(RepositoryTree.Root, "po", "malachi.pot")));

    private Template(IReadOnlyList<PoEntry> entries)
    {
        Entries = entries;
        Plain = entries.Where(e => e.Msgctxt is null).Select(e => e.Msgid).ToHashSet(StringComparer.Ordinal);
        Contexts = entries.Where(e => e.Msgctxt is not null).Select(e => (e.Msgctxt!, e.Msgid)).ToHashSet();
        Plurals = entries.Where(e => e.MsgidPlural is not null).ToDictionary(e => e.Key, e => e.MsgidPlural!, StringComparer.Ordinal);
    }

    /// <summary>The template of the repository.</summary>
    public static Template Pot => Loaded.Value;

    /// <summary>Every entry but the header.</summary>
    public IReadOnlyList<PoEntry> Entries { get; }

    /// <summary>The msgids without a context (_(), T).</summary>
    public IReadOnlySet<string> Plain { get; }

    /// <summary>The (context, msgid) pairs (C_(), C).</summary>
    public IReadOnlySet<(string Context, string Msgid)> Contexts { get; }

    /// <summary>The plural msgid of every plural entry, by its key (N).</summary>
    public IReadOnlyDictionary<string, string> Plurals { get; }

    /// <summary>The key of an entry as the exclusion list writes it: the msgid, or context, U+0004, msgid.</summary>
    public static string KeyOf(string? context, string msgid) =>
        context is null ? msgid : context + PoEntry.ContextSeparator + msgid;

    private static Template Load(string path)
    {
        var pot = PoFile.Parse(File.ReadAllText(path), path);
        return new Template([.. pot.Entries.Where(e => !e.IsHeader && !e.Obsolete)]);
    }
}
