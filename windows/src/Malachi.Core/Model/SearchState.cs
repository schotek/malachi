// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/SearchModel.swift (SearchState);
// GTK: ui/internal/window/search_model.go (searchState). Swift's struct is a
// class here: the model owns one and the controller changes it in place, as
// Swift changes model.search; Clone takes the copy a Swift assignment
// would make.

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Settings;

namespace Malachi.Core.Model;

/// <summary>What the list knows of the search.</summary>
public sealed class SearchState
{
    /// <summary>A search is on.</summary>
    public bool Active { get; set; }

    /// <summary>The field's text, trimmed.</summary>
    public string Text { get; set; } = "";

    /// <summary>As chosen in the scope bar.</summary>
    public SearchScope Scope { get; set; } = SearchScope.Folder;

    /// <summary>
    /// The scope of <see cref="Params"/>: Folder and Account fall back to All
    /// while no folder is selected.
    /// </summary>
    public SearchScope Effective { get; set; } = SearchScope.Folder;

    /// <summary>Of the results on show, or on their way.</summary>
    public SearchQueryParams Params { get; set; } = new() { Query = "" };

    /// <summary>The results of <see cref="Params"/> are on show.</summary>
    public bool Shown { get; set; }

    /// <summary>The excerpts of the results listed.</summary>
    public Dictionary<MessageId, SearchHit> Hits { get; } = new();

    /// <summary>
    /// Return was pressed before the results came: the first one is selected
    /// when they do.
    /// </summary>
    public bool FocusFirst { get; set; }

    /// <summary>
    /// The retention window from config.get, which the retention note names;
    /// unknown until that answered.
    /// </summary>
    public int OfflineDays { get; set; }

    /// <summary>Whether <see cref="OfflineDays"/> is known.</summary>
    public bool OfflineKnown { get; set; }

    /// <summary>An independent copy (what a Swift assignment of the struct makes).</summary>
    public SearchState Clone()
    {
        var c = new SearchState
        {
            Active = Active,
            Text = Text,
            Scope = Scope,
            Effective = Effective,
            Params = Params,
            Shown = Shown,
            FocusFirst = FocusFirst,
            OfflineDays = OfflineDays,
            OfflineKnown = OfflineKnown,
        };
        foreach (var (id, hit) in Hits)
        {
            c.Hits[id] = hit;
        }
        return c;
    }
}
