// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/SearchModel.swift (the free
// functions: searchMinScalars, searchReady, searchRetentionText,
// searchTotalText, searchEmptyText, maxHighlights, highlightRanges; the
// MailModel extension is MailModel.Search.cs); GTK:
// ui/internal/window/search_model.go (searchMinRunes, searchReady,
// searchRetentionText, searchTotalText, searchEmptyText) and
// ui/internal/widget/highlight.go (maxHighlights, validRanges).
//
// The search side of the message list, without WinUI: while a search is on
// the flat list holds search.query results instead of the selected folder's
// messages. The results are summaries like any listing, each carrying its
// own account and folder, so every action on a row works unchanged.
// searchReady counts runes as Go does; highlightRanges checks the daemon's
// UTF-8 byte ranges on the bytes and hands out UTF-16 ranges, as Swift hands
// out NSRanges.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Settings;

namespace Malachi.Core.Model;

/// <summary>The texts and checks of the search.</summary>
public static class SearchModel
{
    /// <summary>
    /// How much has to be typed before a search runs (search_model.go
    /// <c>searchMinRunes</c>), in Unicode scalars as Go counts runes.
    /// </summary>
    public const int SearchMinScalars = 2;

    /// <summary>The most bold ranges one excerpt gets (widget/highlight.go <c>maxHighlights</c>).</summary>
    public const int MaxHighlights = 32;

    /// <summary>Whether enough was typed to search (search_model.go <c>searchReady</c>).</summary>
    public static bool SearchReady(string text) => (text ?? "").Trim().EnumerateRunes().Count() >= SearchMinScalars;

    /// <summary>
    /// How far back the local store, and so search, reaches: the offlineDays
    /// window, everything, or (before config.get answered) just that it is
    /// the mail on this computer (search_model.go <c>searchRetentionText</c>).
    /// </summary>
    public static string SearchRetentionText(int days, bool known)
    {
        if (!known)
        {
            return L10n.T("Searches the mail stored on this computer.");
        }
        if (days <= 0)
        {
            return L10n.T("Searches all mail stored on this computer.");
        }
        return L10n.N(
            "Searches the mail of the last %d day stored on this computer.",
            "Searches the mail of the last %d days stored on this computer.",
            days);
    }

    /// <summary>
    /// The subtitle while searching: how many results there are, "more than"
    /// beyond what the daemon counts, nothing while the count is not known
    /// (search_model.go <c>searchTotalText</c>).
    /// </summary>
    public static string SearchTotalText(int total, bool shown)
    {
        if (!shown)
        {
            return "";
        }
        if (total < 0)
        {
            return L10n.T("More than %d results", API.Limits.MaxSearchTotal);
        }
        return L10n.N("%d result", "%d results", total);
    }

    /// <summary>
    /// An empty result explained in its scope; the wider ones say that Trash
    /// and Junk were left out (search_model.go <c>searchEmptyText</c>).
    /// </summary>
    public static string SearchEmptyText(SearchScope scope) => scope switch
    {
        SearchScope.Folder => L10n.T("Nothing in this folder matches."),
        SearchScope.Account => L10n.T("Nothing in this account matches. Trash and Junk are searched only when chosen as the folder."),
        _ => L10n.T("Nothing in any account matches. Trash and Junk are searched only when chosen as the folder."),
    };

    /// <summary>
    /// The match ranges of an excerpt as ranges of the string
    /// (widget/highlight.go <c>validRanges</c>): the daemon's are UTF-8 byte
    /// ranges, a C# string and WinUI count UTF-16 units. Only ranges that can
    /// be applied as they are survive: inside the text, not empty, starting
    /// and ending on a character boundary, in order and not overlapping, at
    /// most <see cref="MaxHighlights"/>. The daemon promises all of that; a
    /// range that breaks it is dropped rather than trusted.
    /// </summary>
    public static IReadOnlyList<Utf16Range> HighlightRanges(string text, IReadOnlyList<MatchRange> ranges)
    {
        ArgumentNullException.ThrowIfNull(ranges);
        var utf8 = Encoding.UTF8.GetBytes(text ?? "");
        bool Boundary(int i) => i == utf8.Length || (utf8[i] & 0xC0) != 0x80;
        var valid = ranges
            .Where(r => r is not null && r.Start >= 0 && r.Start < r.End && r.End <= utf8.Length && Boundary(r.Start) && Boundary(r.End))
            .OrderBy(r => r.Start);
        var output = new List<Utf16Range>();
        var end = -1;
        foreach (var r in valid)
        {
            if (r.Start < end)
            {
                continue;
            }
            // Both ends sit on character boundaries, so the UTF-16 lengths of
            // the byte spans are exact.
            var from = Encoding.UTF8.GetCharCount(utf8, 0, r.Start);
            var length = Encoding.UTF8.GetCharCount(utf8, r.Start, r.End - r.Start);
            output.Add(new Utf16Range(from, length));
            end = r.End;
            if (output.Count == MaxHighlights)
            {
                break;
            }
        }
        return output;
    }
}
