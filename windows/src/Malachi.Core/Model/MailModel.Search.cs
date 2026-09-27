// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/SearchModel.swift (the MailModel
// extension; the free functions are SearchModel.cs); GTK:
// ui/internal/window/search_model.go (searchRequest, setSearchResults,
// appendSearchResults, takeHits, clearSearchResults, rowMessage,
// searchOrigin) and search.go (refreshSearchScope).
//
// Swift's switches over Settings.SearchScope are exhaustive; a C# enum can
// hold any number, which is read as All, as Go's default branch reads an
// unknown scope.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Settings;

namespace Malachi.Core.Model;

public sealed partial class MailModel
{
    /// <summary>
    /// The first-page search.query for <paramref name="text"/> in
    /// <paramref name="scope"/>, and the scope it actually covers: without a
    /// selected folder there is nothing to narrow Folder or Account to, so
    /// every account is searched (search_model.go <c>searchRequest</c>).
    /// </summary>
    public (SearchQueryParams Params, SearchScope Effective) SearchRequest(string text, SearchScope scope)
    {
        var query = (text ?? "").Trim();
        var page = new Page { Limit = API.Limits.DefaultPageLimit };
        if (Selected is not { } k)
        {
            return (new SearchQueryParams { Query = query, Page = page }, SearchScope.All);
        }
        return scope switch
        {
            SearchScope.Folder => (new SearchQueryParams { AccountId = k.Account, FolderId = k.Folder, Query = query, Page = page }, SearchScope.Folder),
            SearchScope.Account => (new SearchQueryParams { AccountId = k.Account, Query = query, Page = page }, SearchScope.Account),
            _ => (new SearchQueryParams { Query = query, Page = page }, SearchScope.All),
        };
    }

    /// <summary>Replaces the list with the first page of results.</summary>
    public void SetSearchResults(SearchQueryResult res)
    {
        ArgumentNullException.ThrowIfNull(res);
        Search.Hits.Clear();
        SetMessages(TakeHits(res.Results), res.Page);
    }

    /// <summary>
    /// Adds a further page and returns how many results were new. A result
    /// listed already keeps the excerpt it came with.
    /// </summary>
    public int AppendSearchResults(SearchQueryResult res)
    {
        ArgumentNullException.ThrowIfNull(res);
        SearchResult[] fresh = [.. res.Results.Where(r => !index.ContainsKey(r.Message.Id))];
        return AppendMessages(TakeHits(fresh), res.Page);
    }

    /// <summary>Empties the list of results.</summary>
    public void ClearSearchResults()
    {
        ClearMessages();
        Search.Hits.Clear();
    }

    /// <summary>
    /// What the list row of <paramref name="s"/> displays: its summary, and
    /// in search the excerpt with the matched words and where the message
    /// lies (search_model.go <c>rowMessage</c>).
    /// </summary>
    public RowMessage RowMessage(MessageSummary s)
    {
        var m = SummaryMessage(s);
        if (!Search.Active)
        {
            return m;
        }
        if (Search.Hits.TryGetValue(s.Id, out var hit))
        {
            m = m with { Snippet = hit.Snippet, Highlights = hit.Ranges };
        }
        var (label, tooltip) = SearchOrigin(s);
        return m with { Origin = label, OriginTooltip = tooltip };
    }

    /// <summary>
    /// Where a result lies when the search spans more than one folder: the
    /// folder, and its account as well when every account is searched and
    /// there is more than one. The tooltip has the folder's path and the
    /// account (search_model.go <c>searchOrigin</c>).
    /// </summary>
    public (string Label, string Tooltip) SearchOrigin(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (Search.Effective == SearchScope.Folder || Folder(new FolderKey(s.AccountId, s.FolderId)) is not { } f)
        {
            return ("", "");
        }
        var label = FolderTree.FolderTitle(f);
        var tooltip = (f.Path ?? "").Trim();
        if (tooltip.Length == 0)
        {
            tooltip = label;
        }
        if (Account(s.AccountId) is { } a)
        {
            tooltip += "\n" + FolderTree.AccountLabel(a);
            if (Search.Effective == SearchScope.All && EnabledAccounts.Count > 1)
            {
                // TRANSLATORS: where a search result lies, shown in its row:
                // the folder, then the account.
                label = L10n.Format(L10n.C("search result origin", "%s · %s"), label, FolderTree.AccountLabel(a));
            }
        }
        return (label, tooltip);
    }

    /// <summary>The scope bar for the current selection (search.go <c>refreshSearchScope</c>).</summary>
    public SearchBarState SearchBar()
    {
        var f = Selected is { } k ? Folder(k) : null;
        var folderTip = f is not null
            ? L10n.T("Search in %s", FolderTree.FolderTitle(f))
            : L10n.T("Select a folder to search in it");
        var accountTip = Selected is { } sk && Account(sk.Account) is { } a
            ? L10n.T("Search every folder of %s except Trash and Junk", FolderTree.AccountLabel(a))
            : "";
        return new SearchBarState
        {
            Scope = Search.Scope,
            NarrowEnabled = f is not null,
            FolderTooltip = folderTip,
            AccountTooltip = accountTip,
            AllTooltip = L10n.T("Search every account except Trash and Junk"),
        };
    }

    // Remembers the excerpts of results and returns their summaries.
    private MessageSummary[] TakeHits(IReadOnlyList<SearchResult> results)
    {
        foreach (var r in results)
        {
            Search.Hits[r.Message.Id] = new SearchHit(r.Snippet, r.Ranges ?? []);
        }
        return [.. results.Select(r => r.Message)];
    }
}
