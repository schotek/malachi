// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tests of StatusPopover and StatusPopoverRow: ui/internal/window/
// status.go refreshStatusPopover (rows rebuilt only when the accounts
// change) and statusRow.apply (the buttons touched only when the action
// changes, the unsent messages' row).

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class StatusPopoverTests
{
    private static AccountStatus St(string acc, StatusAction action = StatusAction.Check, string detail = "Up to date", int failed = 0) =>
        new() { Account = acc, Title = "Account " + acc, Detail = detail, Action = action, Failed = failed };

    [Fact]
    public void TheRowsFollowTheAccounts()
    {
        var p = new StatusPopover();
        Assert.False(p.HasRows);
        p.Update([St("a"), St("b")], _ => true);
        Assert.True(p.HasRows);
        Assert.Equal(["a", "b"], new[] { p.Rows[0].Account.Value, p.Rows[1].Account.Value });
        Assert.True(p.Rows[0].IsFirst);
        Assert.False(p.Rows[1].IsFirst);
        var first = p.Rows[0];
        // The same accounts: the rows stay, updated in place.
        p.Update([St("a", detail: "Syncing…"), St("b")], _ => true);
        Assert.Same(first, p.Rows[0]);
        Assert.Equal("Syncing…", first.Detail);
        // Another set: rebuilt.
        p.Update([St("b")], _ => true);
        Assert.Single(p.Rows);
        Assert.Equal("b", p.Rows[0].Account.Value);
        p.Update([], _ => true);
        Assert.Empty(p.Rows);
        Assert.False(p.HasRows);
    }

    [Fact]
    public void TheCheckIsAnIconAndTheRepairsAreLabelled()
    {
        var row = new StatusPopoverRow("a");
        row.Apply(St("a"), true);
        Assert.Equal("Account a", row.Title);
        Assert.True(row.CheckVisible);
        Assert.False(row.ButtonVisible);

        row.Apply(St("a", StatusAction.Retry), true);
        Assert.False(row.CheckVisible);
        Assert.True(row.ButtonVisible);
        Assert.Equal("Try Again", row.ButtonLabel);
        Assert.False(row.ButtonMnemonic);

        row.Apply(St("a", StatusAction.Edit), true);
        Assert.Equal("_Edit Account…", row.ButtonLabel);
        Assert.True(row.ButtonMnemonic);

        row.Apply(St("a", StatusAction.NoAction, "Paused"), false);
        Assert.False(row.CheckVisible);
        Assert.False(row.ButtonVisible);
        Assert.Equal("Paused", row.Detail);
    }

    [Fact]
    public void TheButtonsStayWhileOnlyTheDetailMoves()
    {
        var row = new StatusPopoverRow("a");
        row.Apply(St("a"), true);
        var changed = new List<string?>();
        row.PropertyChanged += (_, e) => changed.Add(e.PropertyName);
        row.Apply(St("a", detail: "Syncing Inbox… 40 %"), true);
        Assert.Equal(["Detail"], changed);
    }

    [Fact]
    public void UnsentMessagesLeadToTheOutbox()
    {
        var row = new StatusPopoverRow("a");
        row.Apply(St("a", failed: 2), true);
        Assert.True(row.FailedVisible);
        Assert.Equal("2 messages not sent", row.FailedText);
        Assert.True(row.FailedActivatable);
        // A paused account's outbox cannot be shown: text only.
        row.Apply(St("a", StatusAction.NoAction, "Paused", failed: 1), false);
        Assert.Equal("1 message not sent", row.FailedText);
        Assert.False(row.FailedActivatable);
        row.Apply(St("a"), true);
        Assert.False(row.FailedVisible);
    }

    [Fact]
    public void TheFootIsTheDaemon()
    {
        var p = new StatusPopover { Daemon = "Connected to malachid 1.0 (pid 42)" };
        Assert.Equal("Connected to malachid 1.0 (pid 42)", p.Daemon);
    }
}
