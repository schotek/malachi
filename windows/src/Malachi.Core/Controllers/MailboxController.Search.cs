// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MailboxController+Search.swift
// (the ListController extension), with the search field's pause of
// macos/Sources/MalachiMail/MainWindow/MainToolbar.swift (searchFieldChanged,
// the Return and end-of-search handlers, searchDelay), which macOS keeps in
// AppKit and Windows in Core (docs/windows-port.md §7.4); GTK:
// ui/internal/window/search.go (onSearchModeChanged, onSearchChanged,
// onSearchActivate, onSearchScopeChanged, runSearch, loadMoreSearch,
// selectFirstResult, loadOfflineDays, showSearchState, refreshSearchScope)
// and ui/data/ui/window.blp (search_entry, search-delay: 300).
//
// Windows keeps the search box in the title bar, as macOS keeps it in the
// toolbar (a deviation row of windows/README.md): a search is on while the
// box holds text, Enter selects the first result, Escape empties the box,
// and the scope bar over the list sets the scope, which the search-scope
// setting remembers.

using System;
using System.Threading;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Settings;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;

namespace Malachi.Core.Controllers;

/// <summary>
/// The search over the message list: while a search is on the list shows
/// search.query results for the text, in the chosen scope, instead of the
/// selected folder. The daemon searches its local store; the list only
/// sends the text and shows the answer. The results are a snapshot: they are
/// asked for again when the text, the scope or (for the Folder and Account
/// scopes) the selected folder changes, not when mail arrives, so nothing
/// moves under the pointer.
/// </summary>
public sealed partial class ListController
{
    /// <summary>
    /// How long typing pauses before the text is searched (window.blp
    /// <c>search-delay: 300</c>; MainToolbar.swift <c>searchDelay</c>).
    /// </summary>
    public static readonly TimeSpan SearchDelay = TimeSpan.FromMilliseconds(300);

    private CancellationTokenSource? searchWork;

    /// <summary>Whether the list shows search results.</summary>
    public bool SearchActive => Model.Search.Active;

    /// <summary>
    /// The scope bar while a search is on, null otherwise (Swift
    /// <c>onSearchBar</c>'s last value; <see cref="SearchBarChanged"/>).
    /// </summary>
    [ObservableProperty]
    public partial SearchBarState? SearchBar { get; private set; }

    /// <summary>What a message row displays: the summary, and in search the excerpt and where the message lies.</summary>
    public RowMessage RowMessage(MessageSummary s) => Model.RowMessage(s);

    // The search box (MainToolbar.swift)

    /// <summary>
    /// Every change of the search box's text (MainToolbar.swift
    /// <c>searchFieldChanged</c>, GTK's search-changed after the entry's
    /// search-delay): an emptied box ends the search at once, anything else
    /// waits for typing to pause (<see cref="SearchDelay"/>, on the
    /// <see cref="TimeProvider"/>) and then goes to
    /// <see cref="SetSearchText"/>.
    /// </summary>
    public void SearchFieldChanged(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        Scope.VerifyAccess();
        CancelSearchWork();
        if (text.Length == 0)
        {
            SetSearchText("");
            return;
        }
        var work = CancellationTokenSource.CreateLinkedTokenSource(Scope.Lifetime);
        searchWork = work;
        // Taken now: the next key may cancel and dispose the source before
        // the pause has even started.
        var cancelled = work.Token;
        Scope.RunDetached(async _ =>
        {
            try
            {
                await Task.Delay(SearchDelay, time, cancelled);
            }
            catch (OperationCanceledException)
            {
                return;
            }
            // Typed on or cleared meanwhile: the field no longer holds this pause.
            if (!ReferenceEquals(searchWork, work) || closed)
            {
                return;
            }
            CancelSearchWork();
            SetSearchText(text);
        });
    }

    /// <summary>
    /// Enter in the search box selects the first result, without waiting
    /// for the pause (MainToolbar.swift, the <c>insertNewline</c> command;
    /// search.go <c>onSearchActivate</c>).
    /// </summary>
    public void SearchFieldReturn(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        Scope.VerifyAccess();
        CancelSearchWork();
        ActivateSearch(text);
    }

    /// <summary>
    /// The box was emptied by its clear button or Escape (MainToolbar.swift
    /// <c>searchFieldDidEndSearching</c>): the search ends at once.
    /// </summary>
    public void SearchFieldEnded()
    {
        Scope.VerifyAccess();
        CancelSearchWork();
        SetSearchText("");
    }

    // The search

    /// <summary>
    /// The search box's text changed (after the pause the box waits for):
    /// text starts a search, or narrows the one on; an emptied box ends it
    /// and the folder is listed again (search.go <c>onSearchChanged</c>).
    /// </summary>
    public void SetSearchText(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        Scope.VerifyAccess();
        if (text.Length == 0)
        {
            SetSearchActive(false);
            return;
        }
        SetSearchActive(true);
        Model.Search.Text = text.Trim();
        RunSearch(force: false);
    }

    /// <summary>
    /// Enter in the search box: the first result is selected, now when the
    /// results for the text are on show, otherwise as soon as they arrive
    /// (search.go <c>onSearchActivate</c>).
    /// </summary>
    public void ActivateSearch(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        Scope.VerifyAccess();
        if (text.Length == 0)
        {
            return;
        }
        SetSearchActive(true);
        var trimmed = text.Trim();
        Model.Search.Text = trimmed;
        var (parameters, _) = Model.SearchRequest(trimmed, Model.Search.Scope);
        if (Model.Search.Shown && parameters == Model.Search.Params)
        {
            FocusFirstResult();
            return;
        }
        Model.Search.FocusFirst = true;
        RunSearch(force: false);
    }

    /// <summary>
    /// The scope bar: the choice is kept for the next search (search.go
    /// <c>onSearchScopeChanged</c>, the search-scope setting).
    /// </summary>
    public void SetSearchScope(SearchScope scope)
    {
        Scope.VerifyAccess();
        if (scope == Model.Search.Scope)
        {
            return;
        }
        Model.Search.Scope = scope;
        Settings.SearchScope = scope;
        PublishSearchBar();
        RunSearch(force: false);
        Mailbox.RefreshListTitle();
    }

    /// <summary>
    /// Switches the list between the folder and search (search.go
    /// <c>onSearchModeChanged</c>): starting empties it and shows the
    /// prompt, ending loads the folder again.
    /// </summary>
    public void SetSearchActive(bool on)
    {
        Scope.VerifyAccess();
        if (on == Model.Search.Active)
        {
            return;
        }
        Model.Search.Active = on;
        Model.Search.Shown = false;
        Model.Search.FocusFirst = false;
        Model.Search.Params = new SearchQueryParams { Query = "" };
        Model.Search.Text = "";
        Model.BumpList();
        Model.Loading = false;
        Model.LoadingMore = false;
        Model.ListErr = null;
        Model.ClearSearchResults();
        OnPropertyChanged(nameof(SearchActive));
        if (on)
        {
            // No folder is listed: new mail and outbox changes leave the
            // results alone (ApplyNewMessage, RefreshOutboxViews).
            Model.ListFolder = null;
            Model.Grouped = false;
            Model.Search.Scope = Settings.SearchScope;
            OnPropertyChanged(nameof(FolderRole));
            OnPropertyChanged(nameof(InOutbox));
            LoadOfflineDays();
            Reconcile(SelectionHint.Clear);
            PublishSearchBar();
        }
        else
        {
            Reconcile(SelectionHint.Clear);
            SearchBar = null;
            SearchBarChanged?.Invoke(this, null);
            LoadMessages();
        }
        Mailbox.RefreshListTitle();
        ShowLoadMore();
    }

    /// <summary>
    /// Asks for the first page of results for the text in the chosen scope
    /// (search.go <c>runSearch</c>). With too little typed the prompt is
    /// shown instead; the same request as the results on show (or the one
    /// on its way) is not sent again unless <paramref name="force"/>.
    /// </summary>
    internal void RunSearch(bool force)
    {
        if (!Model.Search.Active)
        {
            return;
        }
        var text = Model.Search.Text;
        if (!SearchModel.SearchReady(text))
        {
            Model.BumpList();
            Model.Search.Params = new SearchQueryParams { Query = "" };
            Model.Search.Shown = false;
            Model.Search.FocusFirst = false;
            Model.Loading = false;
            Model.ListErr = null;
            if (Model.RowCount > 0)
            {
                Model.ClearSearchResults();
                Reconcile(SelectionHint.Clear);
            }
            else
            {
                ShowListState();
            }
            Mailbox.RefreshListTitle();
            return;
        }
        var (parameters, effective) = Model.SearchRequest(text, Model.Search.Scope);
        if (!force && parameters == Model.Search.Params && (Model.Search.Shown || Model.Loading))
        {
            return;
        }
        Model.Search.Params = parameters;
        Model.Search.Effective = effective;
        Model.Search.Shown = false;
        var gen = Model.BumpList();
        Model.Loading = true;
        Model.LoadingMore = false;
        Model.ListErr = null;
        ShowListState();
        Mailbox.RefreshListTitle();
        Mailbox.Perform(API.SearchQuery, parameters, outcome =>
        {
            if (gen != Model.ListGen || !Model.Search.Active)
            {
                return;
            }
            Model.Loading = false;
            if (!outcome.TryGetValue(out var res, out var err))
            {
                // The query is what the user typed: never logged, nor a
                // daemon message that could quote it (only its code).
                LogSearchFailed(logger, FailureOf(err!));
                Model.ClearSearchResults();
                Model.ListErr = err;
                Reconcile(SelectionHint.Clear);
                Mailbox.RefreshListTitle();
                return;
            }
            Model.Search.Shown = true;
            Model.SetSearchResults(res);
            Reconcile(SelectionHint.Keep);
            // A message listed for the text before keeps its row, but its
            // excerpt belongs to the new text: every row redraws.
            ListKey[] keys = [.. System.Linq.Enumerable.Select(Rows, r => r.Key)];
            RowsRefreshed?.Invoke(this, keys);
            Mailbox.RefreshListTitle();
            if (Model.Search.FocusFirst)
            {
                Model.Search.FocusFirst = false;
                FocusFirstResult();
            }
        });
    }

    /// <summary>
    /// Fetches the next page of results (<see cref="LoadMore"/> while
    /// searching; search.go <c>loadMoreSearch</c>).
    /// </summary>
    internal void LoadMoreSearch(ulong gen, string cursor)
    {
        var parameters = Model.Search.Params with { Page = new Page { Cursor = cursor, Limit = API.Limits.DefaultPageLimit } };
        Mailbox.Perform(API.SearchQuery, parameters, outcome =>
        {
            if (gen != Model.ListGen || !Model.Search.Active)
            {
                return;
            }
            Model.LoadingMore = false;
            if (!outcome.TryGetValue(out var res, out var err))
            {
                LogSearchMoreFailed(logger, FailureOf(err!));
                Mailbox.Toast(RpcErrorText.Text(L10n.T("Loading more results"), err));
                ShowLoadMore(); // the cursor is still there; the list offers a retry
                return;
            }
            Model.AppendSearchResults(res);
            Reconcile(SelectionHint.Keep);
        });
    }

    /// <summary>
    /// What the list pane shows while searching and no result is listed
    /// (search.go <c>showSearchState</c>): the prompt, the search under way,
    /// its failure, or no results.
    /// </summary>
    internal ListState SearchListState()
    {
        var st = Model.Search;
        if (!SearchModel.SearchReady(st.Text))
        {
            return new ListState.Status(
                "edit-find-symbolic", L10n.T("Search Mail"), SearchModel.SearchRetentionText(st.OfflineDays, st.OfflineKnown), false);
        }
        if (Model.ListErr is { } err)
        {
            return new ListState.Status(
                "dialog-warning-symbolic", L10n.T("Search Failed"), RpcErrorText.Text(L10n.T("Searching"), err), true);
        }
        if (Model.Loading || !st.Shown)
        {
            return new ListState.Status("", L10n.T("Searching…"), "", false);
        }
        return new ListState.Status("edit-find-symbolic", L10n.T("No Results"), SearchModel.SearchEmptyText(st.Effective), false);
    }

    /// <summary>Announces the scope bar for the current selection while searching.</summary>
    internal void PublishSearchBar()
    {
        if (!Model.Search.Active)
        {
            return;
        }
        var bar = Model.SearchBar();
        SearchBar = bar;
        SearchBarChanged?.Invoke(this, bar);
    }

    /// <summary>Hands the first result to the view to select and focus.</summary>
    private void FocusFirstResult()
    {
        if (Rows.Count == 0)
        {
            return;
        }
        FocusRow?.Invoke(this, Rows[0].Key);
    }

    /// <summary>Learns the retention window the retention note names.</summary>
    private void LoadOfflineDays()
    {
        Mailbox.Perform(API.ConfigGet, new EmptyParams(), outcome =>
        {
            if (!outcome.TryGetValue(out var got, out var err))
            {
                LogConfigGetFailed(logger, err!.Message);
                return;
            }
            Model.Search.OfflineDays = got.Preferences.OfflineDays;
            Model.Search.OfflineKnown = true;
            if (Model.Search.Active)
            {
                ShowListState();
                ShowLoadMore();
            }
        });
    }

    // What a failed search.query is logged as: the daemon's error code, or
    // the transport's failure, whose text never carries the query.
    private static string FailureOf(Exception e) => e is RpcException r ? r.Code.Name : e.Message;

    private void CancelSearchWork()
    {
        var work = searchWork;
        searchWork = null;
        if (work is not null)
        {
            work.Cancel();
            work.Dispose();
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "search.query failed: {Reason}")]
    private static partial void LogSearchFailed(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Warning, Message = "search.query (more) failed: {Reason}")]
    private static partial void LogSearchMoreFailed(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Debug, Message = "config.get for the search note failed: {Reason}")]
    private static partial void LogConfigGetFailed(ILogger logger, string reason);
}
