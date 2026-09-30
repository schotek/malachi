// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of MailboxController.Notifications.cs: which desktop notifications
// the mailbox withdraws, and when (ui/internal/window/notify.go
// withdrawNotifications and the functions after it; Swift
// MailboxController+Notifications.swift), exercised against MailFixture.
// GTK and macOS test only the set itself (NotifiedMessagesTests here); the
// flows are Windows-only tests: after a sync pass the daemon is asked about
// each notified message of the account, an answer that tells nothing ends
// the check, a pass during a check gets one more, a folder viewed in the
// active main window and an account removed or paused take theirs along.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;

namespace Malachi.Core.Tests.Controllers;

public sealed class MailboxControllerNotificationsTests
{
    private static readonly FolderKey Inbox1 = new("acc1", "inbox");
    private static readonly FolderKey Trash1 = new("acc1", "trash");
    private static readonly FolderKey In2 = new("acc2", "in2");

    [Fact]
    public async Task ASyncPassWithdrawsWhatWasReadMovedOrDeletedElsewhere()
    {
        var w = new Watch();
        await using var h = await StartAsync(w, Msg("m1"), Msg("m2"), Msg("m3"));
        await h.On(() =>
        {
            h.Mailbox.RecordNotification(New(Inbox1, "m1"));
            h.Mailbox.RecordNotification(New(Inbox1, "m2"));
            h.Mailbox.RecordNotification(New(Inbox1, "m3"));
            h.Mailbox.RecordNotification(New(In2, "n1"));
        });
        Assert.Empty(w.Withdrawn);

        // Read on another device, and moved to Trash by a server rule.
        h.Fixture.SetMessages([Msg("m1", Flag.Seen), Msg("m3")], "acc1", "inbox");
        h.Fixture.SetMessages([Msg("m2")], "acc1", "trash");
        var gets = h.Fixture.CallCount(API.MessageGet.Name);
        await Pass(h, "acc1");
        Assert.Equal(["m1", "m2"], w.Withdrawn);
        // One message.get per notified message of the account, no more.
        Assert.Equal(gets + 3, h.Fixture.CallCount(API.MessageGet.Name));

        // Deleted: the daemon no longer has it. The other account's
        // notification is not asked about.
        h.Fixture.SetMessages([Msg("m1", Flag.Seen)], "acc1", "inbox");
        await Pass(h, "acc1");
        Assert.Equal(["m1", "m2", "m3"], w.Withdrawn);
        Assert.Equal(gets + 4, h.Fixture.CallCount(API.MessageGet.Name));

        // Nothing left of the account: a pass asks nothing.
        await Pass(h, "acc1");
        Assert.Equal(gets + 4, h.Fixture.CallCount(API.MessageGet.Name));
    }

    [Fact]
    public async Task AnAnswerThatTellsNothingEndsTheCheckUntilTheNextPass()
    {
        var w = new Watch();
        await using var h = await StartAsync(w, Msg("m1"), Msg("m2"));
        await h.On(() =>
        {
            h.Mailbox.RecordNotification(New(Inbox1, "m1"));
            h.Mailbox.RecordNotification(New(Inbox1, "m2"));
        });
        h.Fixture.SetMessages([Msg("m1", Flag.Seen), Msg("m2", Flag.Seen)], "acc1", "inbox");
        h.Fixture.Fail(API.MessageGet.Name, Error(ErrorCode.StorageError, "disk"));
        var gets = h.Fixture.CallCount(API.MessageGet.Name);
        await Pass(h, "acc1");
        Assert.Empty(w.Withdrawn);
        Assert.Equal(gets + 1, h.Fixture.CallCount(API.MessageGet.Name));

        h.Fixture.Succeed(API.MessageGet.Name);
        await Pass(h, "acc1");
        Assert.Equal(["m1", "m2"], w.Withdrawn);
    }

    [Fact]
    public async Task APassDuringACheckGetsOneMoreCheck()
    {
        var w = new Watch();
        await using var h = await StartAsync(w, Msg("m1"));
        await h.On(() => h.Mailbox.RecordNotification(New(Inbox1, "m1")));
        h.Fixture.Delay(API.MessageGet.Name, TimeSpan.FromMilliseconds(100));
        var gets = h.Fixture.CallCount(API.MessageGet.Name);
        await h.On(() => SyncPass(h, "acc1"));
        await h.DelayedAsync(1);
        // Two more passes while the answer is held: one more check, not two.
        await h.On(() =>
        {
            SyncPass(h, "acc1");
            SyncPass(h, "acc1");
        });
        // The first check finds it unread; the one more asks again (held
        // back as well, so the daemon stays busy) and finds it read in the
        // meantime.
        h.Time.Advance(TimeSpan.FromMilliseconds(100));
        await h.DelayedAsync(2);
        Assert.Empty(w.Withdrawn);
        h.Fixture.SetMessages([Msg("m1", Flag.Seen)], "acc1", "inbox");
        await h.AdvanceAsync(TimeSpan.FromMilliseconds(100));
        await h.IdleAsync();
        Assert.Equal(["m1"], w.Withdrawn);
        Assert.Equal(gets + 2, h.Fixture.CallCount(API.MessageGet.Name));
    }

    [Fact]
    public async Task AFolderViewedInTheActiveMainWindowTakesItsNotifications()
    {
        var w = new Watch();
        await using var h = await StartAsync(w, Msg("m1"));
        await h.On(() =>
        {
            h.Mailbox.RecordNotification(New(Inbox1, "m1"));
            h.Mailbox.RecordNotification(New(Trash1, "t1"));
            h.Mailbox.RecordNotification(New(In2, "n1"));
        });
        Assert.Equal(Inbox1, h.Mailbox.Model.Selected);

        // The window is not active: nothing counts as viewed.
        await h.On(h.Mailbox.WithdrawViewedNotifications);
        Assert.Empty(w.Withdrawn);

        // It became active: the selected folder's go, the others stay.
        w.Active = true;
        await h.On(h.Mailbox.WithdrawViewedNotifications);
        Assert.Equal(["m1"], w.Withdrawn);
        await h.On(h.Mailbox.WithdrawViewedNotifications);
        Assert.Equal(["m1"], w.Withdrawn);

        // A folder selected in the active window.
        await h.On(() => h.Mailbox.SelectFolder(Trash1, fav: false));
        Assert.Equal(["m1", "t1"], w.Withdrawn);

        // Selected while the window is not active: it stays.
        w.Active = false;
        await h.On(() => h.Mailbox.SelectFolder(In2, fav: false));
        Assert.Equal(["m1", "t1"], w.Withdrawn);
    }

    [Fact]
    public async Task AnAccountRemovedOrPausedTakesItsNotifications()
    {
        var w = new Watch();
        await using var h = await StartAsync(w, Msg("m1"));
        await h.On(() =>
        {
            h.Mailbox.RecordNotification(New(Inbox1, "m1"));
            h.Mailbox.RecordNotification(New(In2, "n1"));
        });
        var (accounts, _) = TestAccounts();
        h.Fixture.SetAccounts([accounts[0], accounts[1] with { Enabled = false }, accounts[2]]);
        await h.On(h.Mailbox.LoadAccounts);
        await h.IdleAsync();
        Assert.Equal(["n1"], w.Withdrawn);
    }

    [Fact]
    public async Task ANotificationPushedOutIsWithdrawn()
    {
        var w = new Watch();
        await using var h = await StartAsync(w);
        await h.On(() =>
        {
            for (var i = 0; i <= NotifiedMessages.Max; i++)
            {
                h.Mailbox.RecordNotification(New(Inbox1, $"m{i}"));
            }
        });
        // Once forgotten it could never be withdrawn: it goes now.
        Assert.Equal(["m0"], w.Withdrawn);
    }

    private static async Task<MailboxControllerHarness> StartAsync(Watch w, params MessageSummary[] inbox)
    {
        var (accounts, folders) = TestAccounts();
        var h = await MailboxControllerHarness.StartAsync(
            f =>
            {
                f.SetAccounts(accounts);
                f.SetFolders(folders);
                f.SetMessages(inbox, "acc1", "inbox");
            },
            wire: h =>
            {
                h.Mailbox.OnWithdrawNotifications = ids => w.Withdrawn.AddRange(ids.Select(id => id.Value));
                h.Mailbox.IsMainWindowActive = () => w.Active;
            });
        await h.IdleAsync();
        return h;
    }

    // One sync pass of the account: syncing, then idle (folders.go
    // onSyncFinished runs on the second).
    private static async Task Pass(MailboxControllerHarness h, AccountId acc)
    {
        await h.On(() => SyncPass(h, acc));
        await h.IdleAsync();
    }

    private static void SyncPass(MailboxControllerHarness h, AccountId acc)
    {
        h.Mailbox.HandleSyncState(new SyncState { AccountId = acc, Status = SyncStatus.Syncing });
        h.Mailbox.HandleSyncState(new SyncState { AccountId = acc, Status = SyncStatus.Idle });
    }

    private static MessageSummary Msg(string id, params Flag[] flags) => Summary(id, flags);

    private static NewMessageNotification New(FolderKey k, string id) => new()
    {
        AccountId = k.Account,
        FolderId = k.Folder,
        Message = Summary(id) with { AccountId = k.Account, FolderId = k.Folder },
    };

    private static RpcError Error(int code, string message) => new() { Code = code, Message = message };

    // What the mailbox asked the platform for.
    private sealed class Watch
    {
        public List<string> Withdrawn { get; } = [];

        public bool Active { get; set; }
    }
}
