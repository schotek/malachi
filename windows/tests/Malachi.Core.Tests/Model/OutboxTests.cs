// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/OutboxTests.swift
// (outboxBannerTextTest, trashTooltipTest, outboxTracker), the counterpart
// of ui/internal/window/outbox_test.go (TestOutboxBannerText,
// TestTrashTooltip). inOutboxTest (TestInOutbox) tests MailModel.InOutbox
// and is ported with the model's suite (MailModelTests).

using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class OutboxTests
{
    public static TheoryData<string, OutboxInfo?, string, string, bool> BannerCases => new()
    {
        { "not in the outbox", null, "", "", false },
        { "sent", Info(OutboxState.Sent, 0), "", "", false },
        { "unknown state", Info(new OutboxState("bogus"), 0), "", "", false },
        { "queued", Info(OutboxState.Queued, 0), "Queued for sending", "", true },
        {
            "queued after a failure keeps the error quiet",
            Info(OutboxState.Queued, 2, new RpcError { Code = ErrorCode.NetworkError, Message = "dial" }),
            "Queued for sending", "", true
        },
        { "sending", Info(OutboxState.Sending, 1), "Sending…", "", true },
        {
            "failed with a known code",
            Info(OutboxState.Failed, 0, new RpcError { Code = ErrorCode.AuthFailed, Message = "535" }),
            "Sending the message failed: the server rejected the user name or password", "Retry", true
        },
        {
            "failed with an unknown code",
            Info(OutboxState.Failed, 0, new RpcError { Code = ErrorCode.InternalError, Message = "boom" }),
            "Sending the message failed", "Retry", true
        },
        { "failed without an error", Info(OutboxState.Failed, 0), "Sending the message failed", "Retry", true },
    };

    [Theory]
    [MemberData(nameof(BannerCases))]
    public void OutboxBannerTextTest(string name, OutboxInfo? info, string title, string button, bool shown)
    {
        var got = Outbox.OutboxBannerText(info);
        Assert.True(got.Title == title && got.Button == button && got.Shown == shown,
            $"{name}: got {got.Title}/{got.Button}/{got.Shown}");
    }

    [Fact]
    public void TrashTooltipTest()
    {
        Assert.Equal("Cancel Sending", Outbox.TrashTooltip(outbox: true));
        Assert.Equal("Move to Trash", Outbox.TrashTooltip(outbox: false));
    }

    // The delivery bookkeeping of trackOutbox (no Go counterpart; it is
    // bound to the window there).
    [Fact]
    public void OutboxTracker()
    {
        var a = new AccountId("a");
        var t = new OutboxTracker();
        Assert.Equal(0, t.Track(a, total: 3)); // the first look is a baseline
        Assert.Equal(0, t.Track(a, total: 3));
        Assert.Equal(2, t.Track(a, total: 1)); // two delivered
        Assert.Equal(0, t.Track(a, total: 4)); // growth is not a delivery
        t.NoteCancelled(a);
        Assert.Equal(0, t.Track(a, total: 3)); // a cancelled message is not a delivery
        Assert.Equal(1, t.Track(a, total: 2)); // the cancellation counts once
        var b = new AccountId("b");
        Assert.Equal(0, t.Track(b, total: 0));
        Assert.Equal(0, t.Track(b, total: 0));

        // A cancel is used up only by a shrink it explains: a reload that
        // lands before the daemon's delete keeps it for the one that sees
        // the drop.
        t.NoteCancelled(a);
        Assert.Equal(0, t.Track(a, total: 2)); // nothing left yet
        Assert.Equal(0, t.Track(a, total: 1)); // the drop is the cancel, not a delivery
        Assert.Equal(1, t.Track(a, total: 0)); // the cancel was used up by the drop it explained

        // A delivery and a cancel in one shrink.
        var c = new AccountId("c");
        _ = t.Track(c, total: 3);
        t.NoteCancelled(c);
        Assert.Equal(1, t.Track(c, total: 1)); // one of the two was ours

        // A refused cancel is taken back, never below zero.
        var d = new AccountId("d");
        _ = t.Track(d, total: 2);
        t.NoteCancelled(d);
        t.NoteCancelFailed(d);
        t.NoteCancelFailed(d);
        Assert.Equal(1, t.Track(d, total: 1)); // the refused cancel explains nothing
        t.NoteCancelled(d);
        Assert.Equal(0, t.Track(d, total: 0)); // a note after the refusals still counts once
    }

    private static OutboxInfo Info(OutboxState state, int attempts, RpcError? error = null) =>
        new() { State = state, Attempts = attempts, Error = error };
}
