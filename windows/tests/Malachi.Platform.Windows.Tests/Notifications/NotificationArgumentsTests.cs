// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Tests of NotificationArguments and NotificationActivation: what a toast
// carries back when it is clicked, the counterpart of the userInfo of
// NotificationService.swift (accountId, messageId). The escaping is that of
// AppNotificationBuilder.AddArgument (%25, %3B, %3D).

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Notifications;
using Xunit;

namespace Malachi.Platform.Windows.Tests.Notifications;

public sealed class NotificationArgumentsTests
{
    [Fact]
    public void AToastCarriesTheActionTheAccountAndTheMessage()
    {
        var pairs = NotificationArguments.For(Notification("acc_1", "m_123"));
        Assert.Equal(
            [new("action", "open"), new("account", "acc_1"), new KeyValuePair<string, string>("message", "m_123")],
            pairs);
    }

    [Fact]
    public void AClickNamesTheMessage()
    {
        var a = NotificationArguments.Parse(NotificationArguments.For(Notification("acc_1", "m_123")));
        Assert.Equal(new NotificationActivation(new AccountId("acc_1"), new MessageId("m_123")), a);
        Assert.Equal("acc_1", a.AccountId?.Value);
    }

    [Fact]
    public void EveryClickShowsTheWindowWhateverItCarries()
    {
        Assert.Equal(new NotificationActivation(null, null), NotificationArguments.Parse((IEnumerable<KeyValuePair<string, string>>?)null));
        Assert.Equal(new NotificationActivation(null, null), NotificationArguments.Parse([]));
        Assert.Equal(new NotificationActivation(null, null), NotificationArguments.Parse([new("action", "open"), new("account", ""), new("message", "")]));
        Assert.Equal(
            new NotificationActivation(new AccountId("a"), null),
            NotificationArguments.Parse([new("action", "something newer"), new("account", "a"), new("other", "x")]));
        Assert.Equal(new NotificationActivation(null, null), NotificationArguments.Parse((string?)null));
        Assert.Equal(new NotificationActivation(null, null), NotificationArguments.Parse(""));
    }

    [Theory]
    [InlineData("action=open;account=acc_1;message=m_123", "acc_1", "m_123")]
    [InlineData("message=a%3Bb%3Dc%25d;account=x%3b%3dy", "x;=y", "a;b=c%d")]
    [InlineData("account=%;message=%2", "%", "%2")]
    [InlineData("account=%41;message=100%", "%41", "100%")]
    [InlineData(";;account=a;;flag;message=m=n", "a", "m=n")]
    public void TheArgumentStringIsSplitAndUnescaped(string argument, string account, string message)
    {
        Assert.Equal(new NotificationActivation(new AccountId(account), new MessageId(message)), NotificationArguments.Parse(argument));
    }

    [Fact]
    public void APartWithoutAnEqualsSignHasAnEmptyValue()
    {
        Assert.Equal([new("flag", ""), new KeyValuePair<string, string>("k", "v")], NotificationArguments.Split("flag;k=v"));
        Assert.Empty(NotificationArguments.Split(null));
    }

    private static DesktopNotification Notification(string account, string message) => new()
    {
        Title = "t",
        Body = "b",
        Group = account,
        Tag = "message-" + message,
        AccountId = new AccountId(account),
        MessageId = new MessageId(message),
    };
}
