// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/HTML/CIDRegistry.swift (CIDEntry, an
// enum with associated values: a closed record hierarchy here); GTK:
// ui/internal/editor/cid.go (cidFile).

namespace Malachi.Core.Html;

/// <summary>editor.cidFile: one registered id: a local file, or a fetcher.</summary>
public abstract record CidEntry
{
    private CidEntry()
    {
    }

    /// <summary>A file the window itself picked (Insert Image…).</summary>
    /// <param name="Path">The local file.</param>
    /// <param name="ContentType">Its media type, as detected when it was picked.</param>
    public sealed record File(string Path, string ContentType) : CidEntry;

    /// <summary>A copy the daemon holds (a quoted original's picture), fetched on demand.</summary>
    /// <param name="Fetch">Produces the picture.</param>
    public sealed record Fetcher(CidFetcher Fetch) : CidEntry;
}
