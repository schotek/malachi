// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of DesktopNotification, what NotificationService.swift (post) and
// notify.go (notifyNewMessage) hand the platform: the texts of
// NotificationText (tested in NotificationTextTests, the port of
// notify_test.go TestNotificationText), the account as the group,
// "message-<id>" as the identity. Windows-only: the 64-character cap of a
// toast's Tag and Group. A message with a calendar part (macOS post): an
// invitation's or a cancellation's texts and buttons (docs/calendar.md §5.9).

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

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

    [Fact]
    public void AMessageWithoutACalendarPartHasNoButtons() =>
        Assert.Equal(DesktopNotificationActions.None, DesktopNotification.For(New("a", "m", "Alice Example", "Lunch?")).Actions);

    [Theory]
    [InlineData(InvitationKind.Invitation, InvitationState.Unanswered, "New invitation from Jana Nováková", DesktopNotificationActions.Invitation)]
    [InlineData(InvitationKind.Update, InvitationState.Unanswered, "Jana Nováková updated the event", DesktopNotificationActions.Invitation)]
    [InlineData(InvitationKind.Invitation, InvitationState.Answered, "New invitation from Jana Nováková", DesktopNotificationActions.None)]
    [InlineData(InvitationKind.Cancelled, InvitationState.Cancelled, "Jana Nováková cancelled the event", DesktopNotificationActions.Cancellation)]
    [InlineData(InvitationKind.Cancelled, InvitationState.Processed, "Jana Nováková cancelled the event", DesktopNotificationActions.None)]
    [InlineData(InvitationKind.Reply, InvitationState.Processed, "Alice Example", DesktopNotificationActions.None)]
    public void AnInvitationSaysSoWithItsButtons(string kind, string state, string title, DesktopNotificationActions actions)
    {
        var start = F.At(2026, 10, 14, 10).ToUniversalTime();
        var n = New("acc_1", "m_1", "Alice Example", "Invitation: Q4 planning");
        n = n with
        {
            Message = n.Message with
            {
                Invitation = new InvitationSummary
                {
                    Method = new InvitationMethod("REQUEST"),
                    Kind = new InvitationKind(kind),
                    State = new InvitationState(state),
                    Title = "Q4 planning",
                    Start = start,
                    End = start.AddHours(1),
                    Organizer = new Address { Name = "Jana Nováková", Email = "jana@example.org" },
                },
            },
        };
        var d = DesktopNotification.For(n, F.Env());
        Assert.Equal(title, d.Title);
        Assert.Equal(kind == InvitationKind.Reply ? "Invitation: Q4 planning" : "Q4 planning · Wednesday 14 October 2026 · 10:00–11:00 · 1 h", d.Body);
        Assert.Equal(actions, d.Actions);
        // The click still names the message, as any mail's does.
        Assert.Equal("message-m_1", d.Tag);
        Assert.Equal(new MessageId("m_1"), d.MessageId);
    }

    [Fact]
    public void AnInvitationsTextsAreCappedAsAMessagesAre()
    {
        var n = New("a", "m", null, "s");
        n = n with
        {
            Message = n.Message with
            {
                Invitation = new InvitationSummary
                {
                    Method = new InvitationMethod("REQUEST"),
                    Kind = InvitationKind.Invitation,
                    State = InvitationState.Unanswered,
                    Title = new string('x', 1000),
                    Organizer = new Address { Name = new string('y', 1000), Email = "y@example.org" },
                },
            },
        };
        var d = DesktopNotification.For(n, F.Env());
        Assert.True(System.Text.Encoding.UTF8.GetByteCount(d.Title) <= NotificationTextMax + 3);
        Assert.True(System.Text.Encoding.UTF8.GetByteCount(d.Body) <= NotificationTextMax + 3);
    }

    private const int NotificationTextMax = Malachi.Core.Model.NotificationText.NotificationBodyMax;

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
