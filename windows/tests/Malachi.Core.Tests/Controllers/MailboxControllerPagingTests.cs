// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests of MailboxController.Paging.cs: the list pages itself
// (windows/README.md, macOS's M6), the logic macOS keeps untested in
// macos/Sources/MalachiMail/MessageList/MessageListViewController.swift
// (requestMore, fillPane, showLoadMore's retry, clipBoundsChanged, the
// reset of apply(rows:hint:) on .clear). The view reports its layout
// through ViewportChanged; the rows and the footer come from MailFixture.

using System;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Xunit;
using static Malachi.Core.Tests.Controllers.MailboxControllerListTests;

namespace Malachi.Core.Tests.Controllers;

public sealed class MailboxControllerPagingTests
{
    /// <summary>
    /// Arriving at the end of rows taller than the pane asks for the next
    /// page, once per arrival; the footer spins meanwhile and no retry is
    /// offered for a page that brought rows.
    /// </summary>
    [Fact]
    public async Task PagesItselfAtTheEnd()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = Many(120) });
        await h.IdleAsync();
        Assert.Equal(50, h.List.Rows.Count);
        Assert.True(h.List.LoadMoreState.Button);

        // Scrolled, not to the end: nothing.
        await h.On(() => h.List.ViewportChanged(scrollable: true, atEnd: false));
        await h.IdleAsync();
        Assert.Single(h.Fixture.ListRequests);

        // The end: the next page, with the spinner.
        Assert.True(await h.On(() =>
        {
            h.List.ViewportChanged(scrollable: true, atEnd: true);
            return h.List.LoadMoreState.Spinner;
        }));
        await h.IdleAsync();
        Assert.Equal(100, h.List.Rows.Count);
        Assert.Equal("50", h.Fixture.ListRequests[^1].Page.Cursor);
        Assert.False(h.List.LoadMoreRetry);

        // Still at the end (the rows grew below): no second arrival.
        await h.On(() => h.List.ViewportChanged(scrollable: true, atEnd: true));
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.ListRequests.Count);

        // Away and back: the last page; then there is nothing more to ask.
        await h.On(() =>
        {
            h.List.ViewportChanged(scrollable: true, atEnd: false);
            h.List.ViewportChanged(scrollable: true, atEnd: true);
        });
        await h.IdleAsync();
        Assert.Equal(120, h.List.Rows.Count);
        Assert.Equal(new LoadMoreState(), h.List.LoadMoreState);
        await h.On(() =>
        {
            h.List.ViewportChanged(scrollable: true, atEnd: false);
            h.List.ViewportChanged(scrollable: true, atEnd: true);
        });
        await h.IdleAsync();
        Assert.Equal(3, h.Fixture.ListRequests.Count);
        Assert.False(h.List.LoadMoreRetry);
        Assert.Empty(h.Toasts);
    }

    /// <summary>
    /// Rows too few to fill the pane have no end to scroll to: the list asks
    /// for the next page by itself, one request at a time, until the rows
    /// fill the pane or the folder ends.
    /// </summary>
    [Fact]
    public async Task FillsAPaneTheRowsDoNotFill()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = Many(120) });
        await h.IdleAsync();

        // Two reports in one turn: one request.
        await h.On(() =>
        {
            h.List.ViewportChanged(scrollable: false, atEnd: true);
            h.List.ViewportChanged(scrollable: false, atEnd: true);
        });
        await h.IdleAsync();
        Assert.Equal(2, h.Fixture.ListRequests.Count);
        Assert.Equal(100, h.List.Rows.Count);

        // The pane is still not filled after the layout: the next page.
        await h.On(() => h.List.ViewportChanged(scrollable: false, atEnd: true));
        await h.IdleAsync();
        Assert.Equal(120, h.List.Rows.Count);

        // The folder ended: nothing more to ask for.
        await h.On(() => h.List.ViewportChanged(scrollable: false, atEnd: true));
        await h.IdleAsync();
        Assert.Equal(3, h.Fixture.ListRequests.Count);

        // Rows that fill the pane ask for nothing by themselves.
        await using var tall = await StartAsync(new ListLog(), messages: new() { [Inbox] = Many(120) });
        await tall.IdleAsync();
        await tall.On(() => tall.List.ViewportChanged(scrollable: true, atEnd: false));
        await tall.IdleAsync();
        Assert.Single(tall.Fixture.ListRequests);
    }

    /// <summary>
    /// A page that failed (the footer says a further page exists, yet the
    /// rows did not grow) offers Load More, and neither the end of the rows
    /// nor an unfilled pane asks again until it is clicked.
    /// </summary>
    [Fact]
    public async Task OffersARetryOnlyAfterAFailedPage()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = Many(120) });
        await h.IdleAsync();
        Assert.False(h.List.LoadMoreRetry);

        h.Fixture.Fail(API.MessageList.Name, new RpcError { Code = ErrorCode.ServerError, Message = "500" });
        await h.On(() => h.List.ViewportChanged(scrollable: false, atEnd: true));
        await h.IdleAsync();
        Assert.Equal(["Loading more messages failed: the server returned an error"], h.Toasts);
        Assert.True(h.List.LoadMoreState.Button);
        Assert.True(h.List.LoadMoreRetry);
        Assert.Equal(50, h.List.Rows.Count);
        var served = h.Fixture.CallCount(API.MessageList.Name);

        await h.On(() =>
        {
            h.List.ViewportChanged(scrollable: false, atEnd: true);
            h.List.ViewportChanged(scrollable: true, atEnd: false);
            h.List.ViewportChanged(scrollable: true, atEnd: true);
        });
        await h.IdleAsync();
        Assert.Equal(served, h.Fixture.CallCount(API.MessageList.Name));
        Assert.True(h.List.LoadMoreRetry);

        // Load More: asked again; the page comes and the button goes.
        h.Fixture.Succeed(API.MessageList.Name);
        Assert.False(await h.On(() =>
        {
            h.List.RetryLoadMore();
            return h.List.LoadMoreRetry;
        }));
        await h.IdleAsync();
        Assert.Equal(100, h.List.Rows.Count);
        Assert.False(h.List.LoadMoreRetry);
    }

    /// <summary>
    /// Another listing (a filter, a folder) forgets a request of the one
    /// before and its failure: the new listing pages itself afresh.
    /// </summary>
    [Fact]
    public async Task AnotherListingForgetsTheRequest()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = Many(120) });
        await h.IdleAsync();
        h.Fixture.Fail(API.MessageList.Name, new RpcError { Code = ErrorCode.ServerError, Message = "500" });
        await h.On(() => h.List.ViewportChanged(scrollable: false, atEnd: true));
        await h.IdleAsync();
        Assert.True(h.List.LoadMoreRetry);

        h.Fixture.Succeed(API.MessageList.Name);
        Assert.False(await h.On(() =>
        {
            h.List.SetListFilter(MessageFilter.Unread);
            return h.List.LoadMoreRetry;
        }));
        await h.IdleAsync();
        Assert.Equal(50, h.List.Rows.Count);
        Assert.Equal(MessageFilter.Unread, h.Fixture.ListRequests[^1].Filter?.Value);

        await h.On(() => h.List.ViewportChanged(scrollable: false, atEnd: true));
        await h.IdleAsync();
        Assert.Equal(100, h.List.Rows.Count);
        Assert.Equal("50", h.Fixture.ListRequests[^1].Page.Cursor);
        Assert.False(h.List.LoadMoreRetry);
    }

    /// <summary>
    /// The view reports the emptied list of another listing (a filter, a
    /// folder) before its first page is back: the footer is the new
    /// listing's by then, with nothing to ask for, so the report asks
    /// nothing, and once the page is back an unfilled pane pages on.
    /// </summary>
    [Fact]
    public async Task AnEmptiedListingReportedBeforeItsPagePagesOn()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = Many(120), [Trash] = Many(120, "t") });
        await h.IdleAsync();
        Assert.True(h.List.LoadMoreState.Button);

        // A filter: the footer of the listing before goes with its rows.
        var footer = await h.On(() =>
        {
            h.List.SetListFilter(MessageFilter.Unread);
            h.List.ViewportChanged(scrollable: false, atEnd: false);
            return h.List.LoadMoreState;
        });
        Assert.Equal(new LoadMoreState(), footer);
        await h.IdleAsync();
        Assert.Equal(50, h.List.Rows.Count);
        Assert.True(h.List.LoadMoreState.Button);
        await h.On(() => h.List.ViewportChanged(scrollable: false, atEnd: true));
        await h.IdleAsync();
        Assert.Equal(100, h.List.Rows.Count);
        Assert.Equal("50", h.Fixture.ListRequests[^1].Page.Cursor);
        Assert.False(h.List.LoadMoreRetry);

        // A folder: the same.
        footer = await h.On(() =>
        {
            h.Mailbox.SelectFolder(Trash, fav: false);
            h.List.ViewportChanged(scrollable: false, atEnd: false);
            return h.List.LoadMoreState;
        });
        Assert.Equal(new LoadMoreState(), footer);
        await h.IdleAsync();
        Assert.Equal(50, h.List.Rows.Count);
        await h.On(() => h.List.ViewportChanged(scrollable: false, atEnd: true));
        await h.IdleAsync();
        Assert.Equal(100, h.List.Rows.Count);
        Assert.Equal(new MessageListParams
        {
            AccountId = "a",
            FolderId = Trash.Folder,
            Page = new Page { Cursor = "50", Limit = 50 },
            Sort = SortOrder.DateDesc,
            Filter = MessageFilter.Unread,
        }, h.Fixture.ListRequests[^1]);
        Assert.False(h.List.LoadMoreRetry);
        Assert.Empty(h.Toasts);
    }

    /// <summary>
    /// A report while nothing can be asked for (the folder reloading after
    /// a sync, rows kept, the footer still offering the next page) starts
    /// no page and so is no request: once the reload is back, an unfilled
    /// pane pages on.
    /// </summary>
    [Fact]
    public async Task AReportThatStartsNoPageIsNoRequest()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = Many(120) });
        await h.IdleAsync();
        var served = h.Fixture.CallCount(API.MessageList.Name);

        await h.On(() =>
        {
            h.List.LoadMessages();
            h.List.ViewportChanged(scrollable: false, atEnd: true);
        });
        await h.IdleAsync();
        Assert.Equal(served + 1, h.Fixture.CallCount(API.MessageList.Name));
        Assert.Equal(50, h.List.Rows.Count);
        Assert.True(h.List.LoadMoreState.Button);
        Assert.False(h.List.LoadMoreRetry);

        await h.On(() => h.List.ViewportChanged(scrollable: false, atEnd: true));
        await h.IdleAsync();
        Assert.Equal(100, h.List.Rows.Count);
        Assert.Equal("50", h.Fixture.ListRequests[^1].Page.Cursor);
    }

    /// <summary>
    /// A request answered by the folder's last page is over: a reload after
    /// it (a sync, back to the first page and its cursor) is not taken for
    /// that request failing, offers no retry and pages on.
    /// </summary>
    [Fact]
    public async Task AReloadAfterTheLastPageOffersNoRetry()
    {
        await using var h = await StartAsync(new ListLog(), messages: new() { [Inbox] = Many(70) });
        await h.IdleAsync();
        await h.On(() => h.List.ViewportChanged(scrollable: false, atEnd: true));
        await h.IdleAsync();
        Assert.Equal(70, h.List.Rows.Count);
        Assert.Equal(new LoadMoreState(), h.List.LoadMoreState);

        await h.On(h.List.LoadMessages);
        await h.IdleAsync();
        Assert.Equal(50, h.List.Rows.Count);
        Assert.True(h.List.LoadMoreState.Button);
        Assert.False(h.List.LoadMoreRetry);

        await h.On(() => h.List.ViewportChanged(scrollable: false, atEnd: true));
        await h.IdleAsync();
        Assert.Equal(70, h.List.Rows.Count);
        Assert.False(h.List.LoadMoreRetry);
        Assert.Empty(h.Toasts);
    }

    private static MessageSummary[] Many(int n, string prefix = "m") => [.. Enumerable.Range(1, n).Select(i => Msg($"{prefix}{i}", i))];
}
