// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of DesktopNotification, what NotificationService.swift (post) and
// notify.go (notifyNewMessage) hand the platform: the texts of
// NotificationText (tested in NotificationTextTests, the port of
// notify_test.go TestNotificationText), the account as the group,
// "message-<id>" as the identity. Windows-only: the 64-character cap of a
// toast's Tag and Group.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Xunit;

namespace Malachi.Core.Tests.Presentation;

public sealed class DesktopNotificationTests
{
    [Fact]
    public void CarriesTheTextsTheAccountAndTheMessage()
    {
        var d = DesktopNotification.For(New("acc_1", "m_123", "Alice Example", "  Lunch?  "));
        Assert.Equal("Alice Example", d.Title);
        Assert.Equal("Lunch?", d.Body);
        Assert.Equal("acc_1", d.Group);
        Assert.Equal("message-m_123", d.Tag);
        Assert.Equal(new AccountId("acc_1"), d.AccountId);
        Assert.Equal(new MessageId("m_123"), d.MessageId);

        var anonymous = DesktopNotification.For(New("acc_1", "m_1", null, ""));
        Assert.Equal("New message", anonymous.Title);
        Assert.Equal("(No subject)", anonymous.Body);
    }

    [Fact]
    public void TheDaemonsIdsFitAsTheyAre()
    {
        // store/ids.go: a prefix and 32 hex digits.
        var id = "msg_" + new string('f', 32);
        Assert.Equal("message-" + id, DesktopNotification.For(New("acc_" + new string('0', 32), id, null, "s")).Tag);
        Assert.Equal(64, DesktopNotification.Identifier(new string('x', 64)).Length);
        Assert.Equal(new string('x', 64), DesktopNotification.Identifier(new string('x', 64)));
        Assert.Equal("", DesktopNotification.Identifier(""));
    }

    [Fact]
    public void ALongerIdentifierKeepsItsStartAndEndsInAHash()
    {
        var graphId = "AAMkAGI2TG93AAA=" + new string('A', 140);
        var tag = DesktopNotification.Identifier("message-" + graphId);
        Assert.Equal(DesktopNotification.MaxIdentifierLength, tag.Length);
        Assert.StartsWith("message-AAMkAGI2TG93", tag, StringComparison.Ordinal);
        Assert.Equal('~', tag[^17]);
        Assert.True(tag[^16..].All(Uri.IsHexDigit));
        // Stable, and distinct for ids that share the kept start.
        Assert.Equal(tag, DesktopNotification.Identifier("message-" + graphId));
        Assert.NotEqual(tag, DesktopNotification.Identifier("message-" + graphId + "B"));
        Assert.Equal(DesktopNotification.MaxIdentifierLength, DesktopNotification.For(New(new string('a', 100), graphId, null, "s")).Group.Length);
    }

    [Fact]
    public void TheCutNeverSplitsASurrogatePair()
    {
        // The kept start is 47 characters; put a pair across its end.
        var s = new string('a', 46) + "😀" + new string('b', 40);
        var id = DesktopNotification.Identifier(s);
        Assert.True(id.Length <= DesktopNotification.MaxIdentifierLength);
        Assert.Equal(new string('a', 46) + "~", id[..47]);
        Assert.DoesNotContain(id, char.IsSurrogate);
    }

    private static NewMessageNotification New(string account, string message, string? from, string subject) => new()
    {
        AccountId = new AccountId(account),
        FolderId = new FolderId("f"),
        Message = new MessageSummary
        {
            Id = new MessageId(message),
            AccountId = new AccountId(account),
            FolderId = new FolderId("f"),
            From = from is null ? [] : [new Address { Name = from, Email = "alice@example.invalid" }],
            Subject = subject,
            Date = DateTimeOffset.MinValue,
            Snippet = "",
            HasAttachments = false,
            Size = 0,
        },
    };
}
