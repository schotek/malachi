// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/MailboxControllerListTests.swift
// (its second suite, ListSearchTests, every test): the search over the
// message list (ui/internal/window/search.go) against MailFixture, which
// answers search.query through a handler of the test's. Two tests are
// Windows-only: the search box's pause, which macOS keeps in AppKit
// (MainToolbar.swift) and Windows in Core, and the search-scope setting a
// search starts in (search.go onSearchModeChanged).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Settings;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Controllers.MailboxControllerListTests;

namespace Malachi.Core.Tests.Controllers;

public sealed class ListSearchTests
{
    [Fact]
    public async Task SearchReplacesTheListAndEndsBackInTheFolder()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = [Msg("m1", 1), Msg("m2", 2)] });
        var calls = new SearchCalls();
        var hit = new MatchRange { Start = 3, End = 11 };
        ServeSearch(h, [new SearchResult { Message = Msg("m2", 2), Snippet = "…nalezeno", Ranges = [hit], Score = 0 }], calls);
        await h.On(() => h.Mailbox.SelectFolder(Inbox, fav: false));
        await h.IdleAsync();

        // One character: the prompt, no request.
        var (active, listed, title) = await h.On(() =>
        {
            h.List.SetSearchText("n");
            return (h.List.SearchActive, h.Mailbox.Model.ListFolder, StatusTitle(h.List.ListState));
        });
        Assert.True(active);
        Assert.Null(listed);
        Assert.Equal("Search Mail", title);

        await h.On(() => h.List.SetSearchText("nalez"));
        await h.IdleAsync();
        Assert.Equal(["m2"], Ids(h.List.Rows));
        Assert.Equal(new ListState.Messages(), h.List.ListState);
        var first = calls.Last;
        Assert.True(first?.AccountId == Acc && first.FolderId == Inbox.Folder && first.Query == "nalez");
        Assert.Equal([hit], h.List.RowMessage(h.List.Rows[0].Message).Highlights);
        Assert.Equal("…nalezeno", h.List.RowMessage(h.List.Rows[0].Message).Snippet);

        // The same text again asks nothing; a new mail leaves the results be.
        await h.On(() =>
        {
            h.List.SetSearchText("nalez");
            h.List.ApplyNewMessage(New(Msg("m9", 9)));
        });
        await h.IdleAsync();
        Assert.Equal(1, calls.Count);
        Assert.Equal(["m2"], Ids(h.List.Rows));

        // Another scope asks again and is remembered.
        await h.On(() => h.List.SetSearchScope(SearchScope.All));
        await h.IdleAsync();
        Assert.Equal(2, calls.Count);
        Assert.Null(calls.Last?.AccountId);
        Assert.Equal(SearchScope.All, h.Settings.SearchScope);

        // Emptying the box lists the folder again.
        Assert.False(await h.On(() =>
        {
            h.List.SetSearchText("");
            return h.List.SearchActive;
        }));
        await h.IdleAsync();
        Assert.Equal(Inbox, h.Mailbox.Model.ListFolder);
        Assert.Equal(["m2", "m1"], Ids(h.List.Rows));
    }

    [Fact]
    public async Task AFailedSearchOffersARetry()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = [Msg("m1", 1)] });
        h.Fixture.On(API.SearchQuery.Name, _ => Task.FromException<string>(new RpcException(new RpcError { Code = ErrorCode.InvalidArgument, Message = "no" })));
        await h.On(() => h.Mailbox.SelectFolder(Inbox, fav: false));
        await h.IdleAsync();
        await h.On(() => h.List.SetSearchText("abc"));
        await h.IdleAsync();
        Assert.Equal("Search Failed", StatusTitle(h.List.ListState));
        Assert.True(Assert.IsType<ListState.Status>(h.List.ListState).Retry);
        var calls = new SearchCalls();
        ServeSearch(h, [new SearchResult { Message = Msg("m1", 1), Snippet = "abc", Score = 0 }], calls);
        await h.On(h.List.Retry);
        await h.IdleAsync();
        Assert.Equal(["m1"], Ids(h.List.Rows));
    }

    [Fact]
    public async Task NoResultsAndPaging()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = [Msg("m1", 1), Msg("m2", 2)] });
        var calls = new SearchCalls();
        ServeSearch(h, [], calls);
        await h.On(() => h.Mailbox.SelectFolder(Inbox, fav: false));
        await h.IdleAsync();
        await h.On(() => h.List.SetSearchText("nothing"));
        await h.IdleAsync();
        Assert.Equal("No Results", StatusTitle(h.List.ListState));

        var r1 = new SearchResult { Message = Msg("m2", 2), Snippet = "x", Score = 0 };
        var r2 = new SearchResult { Message = Msg("m1", 1), Snippet = "y", Score = 0 };
        h.Fixture.On(API.SearchQuery.Name, json =>
        {
            var p = JsonCoding.Decode<SearchQueryParams>(json);
            var page = p.Page.Cursor is null
                ? new SearchQueryResult { Results = [r1], Page = new PageInfo { NextCursor = "c", Total = 2 } }
                : new SearchQueryResult { Results = [r2], Page = new PageInfo { Total = 2 } };
            return Task.FromResult(JsonCoding.EncodeToString(page));
        });
        await h.On(() => h.List.SetSearchText("something"));
        await h.IdleAsync();
        Assert.Equal(["m2"], Ids(h.List.Rows));
        Assert.True(h.List.LoadMoreState.Button && h.List.LoadMoreState.Note.Length == 0);
        await h.On(h.List.LoadMore);
        await h.IdleAsync();
        Assert.Equal(["m2", "m1"], Ids(h.List.Rows));
        // The last page carries the note on how far back search reaches.
        Assert.Equal("Searches the mail of the last 30 days stored on this computer.", h.List.LoadMoreState.Note);
    }

    [Fact]
    public async Task ReturnFocusesTheFirstResult()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = [Msg("m1", 1)] });
        var calls = new SearchCalls();
        ServeSearch(h, [new SearchResult { Message = Msg("m1", 1), Snippet = "abc", Score = 0 }], calls);
        var focus = new List<ListKey>();
        await h.On(() => h.List.FocusRow += (_, k) => focus.Add(k));
        await h.On(() => h.Mailbox.SelectFolder(Inbox, fav: false));
        await h.IdleAsync();
        await h.On(() => h.List.ActivateSearch("abc"));
        await h.IdleAsync();
        Assert.Equal([new ListKey(Message: "m1")], focus);
        // Results on show already: at once, without asking again.
        Assert.Equal(2, await h.On(() =>
        {
            h.List.ActivateSearch("abc");
            return focus.Count;
        }));
        await h.IdleAsync();
        Assert.Equal(1, calls.Count);
    }

    /// <summary>
    /// Windows-only (MainToolbar.swift <c>searchFieldChanged</c> and its
    /// Return and end-of-search handlers; window.blp <c>search-delay: 300</c>):
    /// the text is searched once typing pauses for 300 ms, Enter searches
    /// at once and selects the first result, and an emptied box ends the
    /// search at once, with no pause left to fire.
    /// </summary>
    [Fact]
    public async Task SearchBoxWaitsForTypingToPause()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = [Msg("m1", 1)] });
        var calls = new SearchCalls();
        ServeSearch(h, [new SearchResult { Message = Msg("m1", 1), Snippet = "abc", Score = 0 }], calls);
        var focus = new List<ListKey>();
        await h.On(() => h.List.FocusRow += (_, k) => focus.Add(k));
        await h.IdleAsync();
        var justUnder = ListController.SearchDelay - TimeSpan.FromMilliseconds(1);

        await h.On(() => h.List.SearchFieldChanged("ab"));
        await h.AdvanceAsync(justUnder);
        Assert.Equal(0, calls.Count);
        Assert.False(h.List.SearchActive);
        // Typing on starts the pause over.
        await h.On(() => h.List.SearchFieldChanged("abc"));
        await h.AdvanceAsync(justUnder);
        Assert.Equal(0, calls.Count);
        await h.AdvanceAsync(TimeSpan.FromMilliseconds(1));
        Assert.True(h.List.SearchActive);
        Assert.Equal("abc", Assert.Single(calls.All).Query);
        Assert.Empty(focus);

        // Enter does not wait, and the pause it cut short does not fire.
        await h.On(() =>
        {
            h.List.SearchFieldChanged("abcd");
            h.List.SearchFieldReturn("abcd");
        });
        await h.IdleAsync();
        Assert.Equal(2, calls.Count);
        Assert.Equal("abcd", calls.Last?.Query);
        Assert.Equal([new ListKey(Message: "m1")], focus);
        await h.AdvanceAsync(ListController.SearchDelay);
        Assert.Equal(2, calls.Count);

        // An emptied box ends the search at once; so does Escape.
        Assert.False(await h.On(() =>
        {
            h.List.SearchFieldChanged("abc");
            h.List.SearchFieldChanged("");
            return h.List.SearchActive;
        }));
        await h.AdvanceAsync(ListController.SearchDelay);
        Assert.False(h.List.SearchActive);
        await h.On(() => h.List.SearchFieldChanged("xyz"));
        await h.AdvanceAsync(ListController.SearchDelay);
        Assert.True(h.List.SearchActive);
        Assert.False(await h.On(() =>
        {
            h.List.SearchFieldChanged("xyzw");
            h.List.SearchFieldEnded();
            return h.List.SearchActive;
        }));
        await h.AdvanceAsync(ListController.SearchDelay);
        Assert.False(h.List.SearchActive);
        Assert.Equal(["abc", "abcd", "xyz"], calls.All.Select(p => p.Query));
        Assert.Equal(["m1"], Ids(h.List.Rows));
    }

    /// <summary>
    /// Windows-only (search.go <c>onSearchModeChanged</c>): a search starts
    /// in the scope the search-scope setting remembers, the scope bar says
    /// which, and it goes when the search ends.
    /// </summary>
    [Fact]
    public async Task SearchStartsInTheRememberedScope()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = [Msg("m1", 1)] });
        var calls = new SearchCalls();
        ServeSearch(h, [new SearchResult { Message = Msg("m1", 1), Snippet = "abc", Score = 0 }], calls);
        var bars = new List<SearchBarState?>();
        await h.On(() =>
        {
            h.Settings.SearchScope = SearchScope.Account;
            h.List.SearchBarChanged += (_, b) => bars.Add(b);
        });
        await h.IdleAsync();

        await h.On(() => h.List.SetSearchText("abc"));
        await h.IdleAsync();
        Assert.Equal(new SearchQueryParams { AccountId = Acc, Query = "abc", Page = new Page { Limit = 50 } }, calls.Last);
        Assert.Equal(SearchScope.Account, h.List.SearchBar?.Scope);
        Assert.True(h.List.SearchBar?.NarrowEnabled);
        Assert.Equal(new ListHeading("Search", "1 result"), h.Mailbox.ListHeading);

        await h.On(() => h.List.SetSearchText(""));
        await h.IdleAsync();
        Assert.Null(h.List.SearchBar);
        Assert.Null(bars[^1]);
        Assert.Equal(new ListHeading("Inbox", "1 unread of 1"), h.Mailbox.ListHeading);
    }

    private static string? StatusTitle(ListState s) => s is ListState.Status status ? status.Title : null;

    // Serves search.query with results whatever is asked, recording the params.
    private static void ServeSearch(MailboxControllerHarness h, SearchResult[] results, SearchCalls calls)
    {
        var data = JsonCoding.EncodeToString(new SearchQueryResult { Results = results, Page = new PageInfo { Total = results.Length } });
        h.Fixture.On(API.SearchQuery.Name, json =>
        {
            calls.Add(JsonCoding.Decode<SearchQueryParams>(json));
            return Task.FromResult(data);
        });
    }

    /// <summary>What the list sent as search.query (the daemon's thread adds, the test reads).</summary>
    private sealed class SearchCalls
    {
        private readonly System.Threading.Lock gate = new();
        private readonly List<SearchQueryParams> all = [];

        public IReadOnlyList<SearchQueryParams> All
        {
            get
            {
                lock (gate)
                {
                    return [.. all];
                }
            }
        }

        public int Count => All.Count;

        public SearchQueryParams? Last
        {
            get
            {
                lock (gate)
                {
                    return all.Count > 0 ? all[^1] : null;
                }
            }
        }

        public void Add(SearchQueryParams p)
        {
            lock (gate)
            {
                all.Add(p);
            }
        }
    }
}
