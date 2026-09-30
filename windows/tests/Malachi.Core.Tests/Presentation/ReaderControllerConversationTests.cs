// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of ReaderController.LeaveForConversation: conversation_view.go
// (leaveForConversation, showConversation's page), which GTK and macOS check
// by hand. The pane shows the conversation page; the message it showed
// goes, a late answer for it is not rendered, and the placeholders do not
// take the page while the conversation is there.

using System;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using static Malachi.Core.Tests.Presentation.ReaderFixtures;

namespace Malachi.Core.Tests.Presentation;

public sealed class ReaderControllerConversationTests
{
    private readonly FakeTimeProvider clock = new();
    private readonly FakeReaderCache cache = new();

    [Fact]
    public void TheMessageGoesAndTheConversationPageShows()
    {
        var r = new ReaderController(ReaderMode.Pane, cache, timeProvider: clock);
        var s = Summary("m1", "Plans");
        r.Show(s);
        cache.Entry("m1").Body = HtmlBody("m1");
        cache.Settle("m1");
        Assert.NotNull(r.Html);

        r.LeaveForConversation();
        Assert.Equal(ReaderPage.Conversation, r.Page);
        Assert.Null(r.Current);
        Assert.Null(r.Html); // its pictures go with it
        Assert.Empty(r.Chips);
        Assert.False(r.RemoteBarVisible);

        // No spinner and no late answer for the message it showed.
        r.Show(Summary("m2"));
        r.LeaveForConversation();
        clock.Advance(TimeSpan.FromSeconds(5));
        cache.Entry("m2").Body = TextBody("m2", "late");
        cache.Settle("m2");
        Assert.Equal(ReaderPage.Conversation, r.Page);
        Assert.NotEqual("late", r.BodyText);

        // The accounts' answer leaves the conversation where it is.
        r.SetHasAccounts(false);
        Assert.Equal(ReaderPage.Conversation, r.Page);

        // Another row: the message, or the placeholder, again.
        r.Show(Summary("m3"));
        Assert.Equal(ReaderPage.Message, r.Page);
        r.LeaveForConversation();
        r.Clear();
        Assert.Equal(ReaderPage.NoAccounts, r.Page);
    }

    [Fact]
    public void OnlyThePaneShowsConversations()
    {
        var w = new ReaderController(ReaderMode.Window, cache, timeProvider: clock);
        w.Show(Summary("m1"));
        w.LeaveForConversation();
        Assert.Equal(ReaderPage.Message, w.Page);
    }
}
