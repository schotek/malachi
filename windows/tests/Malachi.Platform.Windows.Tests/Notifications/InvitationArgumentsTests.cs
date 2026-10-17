// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of NotificationArguments.ForInvitation and the invitation half of
// Parse: what the Accept button of an invitation's toast and the Remove
// from Calendar button of a cancellation's carry back when clicked, the
// counterpart of NotificationService.swift's INVITATION and CANCELLATION
// (docs/calendar.md §5.9). Each button names its message and its action;
// Show and the toast's own click carry the plain pairs (For), which open
// the message; a button without both ids is an ordinary click.

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Notifications;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Notifications;

public sealed class InvitationArgumentsTests
{
    [Theory]
    [InlineData(InvitationNotificationResponse.Accept, "accept")]
    [InlineData(InvitationNotificationResponse.RemoveFromCalendar, "removeFromCalendar")]
    public void AButtonCarriesItsActionTheAccountAndTheMessage(InvitationNotificationResponse response, string action)
    {
        var pairs = NotificationArguments.ForInvitation(Toast(), response);
        Assert.Equal(
            [new("action", action), new("account", "acc_1"), new KeyValuePair<string, string>("message", "m_1")],
            pairs);
        var a = NotificationArguments.Parse(pairs);
        Assert.Null(a.AccountId);
        Assert.Null(a.MessageId);
        Assert.Null(a.Alert);
        Assert.Equal(new InvitationActivation(response, new AccountId("acc_1"), new MessageId("m_1")), a.Invitation);
    }

    [Fact]
    public void ShowIsTheToastsOwnClick()
    {
        var a = NotificationArguments.Parse(NotificationArguments.For(Toast()));
        Assert.Null(a.Invitation);
        Assert.Equal(new NotificationActivation(new AccountId("acc_1"), new MessageId("m_1")), a);
    }

    [Fact]
    public void TheArgumentStringOfAButtonIsSplitAndUnescaped()
    {
        var a = NotificationArguments.Parse("action=removeFromCalendar;account=acc%3B1;message=m%3D1");
        Assert.Equal(
            new InvitationActivation(InvitationNotificationResponse.RemoveFromCalendar, new AccountId("acc;1"), new MessageId("m=1")),
            a.Invitation);
    }

    [Fact]
    public void AButtonWithoutItsIdsIsAnOrdinaryClick()
    {
        Assert.Equal(
            new NotificationActivation(new AccountId("a"), null),
            NotificationArguments.Parse([new("action", "accept"), new("account", "a")]));
        Assert.Equal(
            new NotificationActivation(null, new MessageId("m")),
            NotificationArguments.Parse([new("action", "removeFromCalendar"), new("account", ""), new("message", "m")]));
        Assert.Null(NotificationArguments.Parse([new("action", "decline"), new("account", "a"), new("message", "m")]).Invitation);
    }

    private static DesktopNotification Toast() => new()
    {
        Title = "New invitation from Jana Nováková",
        Body = "Q4 planning · Wednesday 14 October 2026 · 10:00–11:00 · 1 h",
        Group = "acc_1",
        Tag = "message-m_1",
        AccountId = new AccountId("acc_1"),
        MessageId = new MessageId("m_1"),
        Actions = DesktopNotificationActions.Invitation,
    };
}
