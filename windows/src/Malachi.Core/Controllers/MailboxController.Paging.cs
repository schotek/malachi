// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the paging of
// macos/Sources/MalachiMail/MessageList/MessageListViewController.swift
// (requestedAt, atBottom, requestMore, fillPane, showLoadMore's retry,
// loadMoreClicked, clipBoundsChanged, and apply(rows:hint:)'s reset on
// .clear), which macOS keeps in AppKit and Windows in Core
// (docs/windows-port.md §7.4); GTK: ui/internal/window/messages.go
// (loadMore from the Load More button and ConnectEdgeReached).
//
// The list pages itself (a deviation row of windows/README.md, macOS's
// M6): reaching the end of the rows, or rows too few to fill the pane,
// asks for the next page, with the footer's spinner while it loads; the
// Load More button shows only to retry a page that failed. The view knows
// the layout and reports it through ViewportChanged after every layout pass
// that changes the rows' extent or the pane's size, and whenever the user
// scrolls; Core decides.

using CommunityToolkit.Mvvm.ComponentModel;

namespace Malachi.Core.Controllers;

public sealed partial class ListController
{
    // The number of rows when this list asked for a page, until the footer
    // says the page is back (MessageListViewController.requestedAt).
    private int? requestedAt;

    // Whether the pane was scrolled to the end at the last report.
    private bool atBottom;

    /// <summary>
    /// The Load More button, which shows only to retry a page that failed
    /// (the footer said a further page exists, yet the rows did not grow).
    /// </summary>
    [ObservableProperty]
    public partial bool LoadMoreRetry { get; private set; }

    /// <summary>
    /// The view's layout changed or the user scrolled
    /// (MessageListViewController <c>clipBoundsChanged</c>, window.go
    /// <c>ConnectEdgeReached</c>): arriving at the end of rows that overflow
    /// the pane asks for the next page, once per arrival; rows that do not
    /// fill the pane have no end to reach and ask by themselves
    /// (<c>fillPane</c>), unless a request is open or the last one failed.
    /// </summary>
    /// <param name="scrollable">The rows are taller than the pane.</param>
    /// <param name="atEnd">The pane shows the last row's bottom edge.</param>
    public void ViewportChanged(bool scrollable, bool atEnd)
    {
        Scope.VerifyAccess();
        var bottom = scrollable && atEnd;
        if (bottom && !atBottom && !LoadMoreRetry)
        {
            RequestMore();
        }
        atBottom = bottom;
        if (!scrollable)
        {
            FillPane();
        }
    }

    /// <summary>The Load More button (MessageListViewController <c>loadMoreClicked</c>): the failed page is asked for again.</summary>
    public void RetryLoadMore()
    {
        Scope.VerifyAccess();
        LoadMoreRetry = false;
        RequestMore();
    }

    /// <summary>Asks for the next page, noting the rows it had then (<c>requestMore</c>).</summary>
    private void RequestMore()
    {
        requestedAt = Rows.Count;
        LoadMore();
    }

    /// <summary>
    /// Rows too few to fill the pane have no edge to scroll to: the list
    /// asks for the next page itself (GTK shows its Load More button then),
    /// unless a request is open or the last one failed (<c>fillPane</c>).
    /// </summary>
    private void FillPane()
    {
        if (!LoadMoreState.Button || requestedAt is not null || LoadMoreRetry)
        {
            return;
        }
        RequestMore();
    }

    /// <summary>
    /// The footer changed (MessageListViewController <c>showLoadMore</c>): a
    /// page this list asked for is back, as rows, or failed (the controller
    /// toasted why) and the button offers the retry.
    /// </summary>
    private void FollowLoadMore(LoadMoreState state)
    {
        var failed = false;
        if (state.Button && requestedAt is { } at)
        {
            requestedAt = null;
            failed = Rows.Count == at;
        }
        LoadMoreRetry = failed;
    }

    /// <summary>Another listing: a page asked for the one before is moot (<c>apply(rows:hint:)</c> on <c>.clear</c>).</summary>
    private void ResetPaging()
    {
        requestedAt = null;
        LoadMoreRetry = false;
    }
}
