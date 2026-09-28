// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/NotificationDecodeTests.swift.
//
// DaemonNotification over the four notifications of docs/api.md §5, as the
// transport hands them over (the whole line, params decoded on demand).

using System;
using System.Linq;
using System.Text;
using System.Text.Json;
using Malachi.Core.Api;
using Xunit;

namespace Malachi.Core.Tests.Api;

public sealed class NotificationDecodeTests
{
    // Swift's raw(_:_:): the line a daemon writes for the notification.
    private static DaemonNotification Raw(string method, string parameters) =>
        DaemonNotification.Decode(method, Encoding.UTF8.GetBytes($$$"""{"jsonrpc":"2.0","method":"{{{method}}}","params":{{{parameters}}}}"""));

    [Fact]
    public void NewMessage()
    {
        var n = Raw("notify.newMessage", $$$"""
            {"accountId":"acc_1","folderId":"f_inbox","message":{{{ApiCodingTests.SummaryJson}}}}
            """);
        var p = Assert.IsType<DaemonNotification.NewMessage>(n).Payload;
        Assert.Equal("acc_1", p.AccountId);
        Assert.Equal("f_inbox", p.FolderId);
        Assert.Equal("m_123", p.Message.Id);
        Assert.Equal("t_9", p.Message.ThreadId);
        Assert.StartsWith("plain text", p.Message.Snippet, StringComparison.Ordinal);
    }

    [Fact]
    public void SyncState()
    {
        var n = Raw("notify.syncState", """
            {"state":{"accountId":"acc_1","status":"syncing","folderId":"f_inbox","progress":42,"lastSync":"2026-09-02T10:00:00Z","pendingOutbox":1}}
            """);
        Assert.Equal(
            new DaemonNotification.SyncState(new SyncState
            {
                AccountId = "acc_1",
                Status = SyncStatus.Syncing,
                FolderId = "f_inbox",
                Progress = 42,
                LastSync = Rfc3339.Parse("2026-09-02T10:00:00Z"),
                PendingOutbox = 1,
            }),
            n);
        var failed = Raw("notify.syncState", """
            {"state":{"accountId":"acc_1","status":"error","progress":-1,"error":{"code":1400,"message":"disk full"},"pendingOutbox":0}}
            """);
        var s = Assert.IsType<DaemonNotification.SyncState>(failed).State;
        Assert.Equal(SyncStatus.Error, s.Status);
        Assert.Equal(ErrorCode.StorageError, s.Error!.Code);
    }

    [Fact]
    public void AuthRequired()
    {
        var n = Raw("notify.authRequired", """{"accountId":"acc_1","reason":1201,"message":"535 rejected"}""");
        Assert.Equal(
            new DaemonNotification.AuthRequired(new AuthRequiredNotification { AccountId = "acc_1", Reason = ErrorCode.AuthFailed, Message = "535 rejected" }),
            n);
        var oauth = Raw("notify.authRequired", """{"accountId":"acc_2","reason":1200,"message":"token expired","authUrl":"https://login.example/x"}""");
        var p = Assert.IsType<DaemonNotification.AuthRequired>(oauth).Payload;
        Assert.Equal(ErrorCode.AuthRequired, p.Reason);
        Assert.Equal("authRequired", p.Reason.Name);
        Assert.Equal("https://login.example/x", p.AuthUrl);
        var keyring = Raw("notify.authRequired", """{"accountId":"acc_3","reason":1202,"message":"no keyring"}""");
        Assert.Equal(ErrorCode.KeyringError, Assert.IsType<DaemonNotification.AuthRequired>(keyring).Payload.Reason);
    }

    [Fact]
    public void AccountsChanged()
    {
        Assert.Equal(new DaemonNotification.AccountsChanged(), Raw("notify.accountsChanged", "{}"));
        // The transport may hand over a line without params at all.
        var bare = DaemonNotification.Decode("notify.accountsChanged", """{"jsonrpc":"2.0","method":"notify.accountsChanged"}"""u8);
        Assert.Equal(new DaemonNotification.AccountsChanged(), bare);
    }

    [Fact]
    public void UnknownMethodIsKeptByName()
    {
        var n = Raw("notify.somethingNewer", """{"x":1}""");
        Assert.Equal(new DaemonNotification.Unknown("notify.somethingNewer"), n);
    }

    [Fact]
    public void MalformedKnownNotificationThrows()
    {
        Assert.Throws<JsonException>(() => Raw("notify.syncState", "{}"));
        Assert.Throws<JsonException>(() => Raw("notify.newMessage", """{"accountId":"a","folderId":"f","message":{"id":"m"}}"""));
    }

    [Fact]
    public void EveryDocumentedNotificationDecodes()
    {
        (string Method, string Params)[] samples =
        [
            (API.Notify.NewMessage, $$$"""{"accountId":"acc_1","folderId":"f_inbox","message":{{{ApiCodingTests.SummaryJson}}}}"""),
            (API.Notify.SyncState, """{"state":{"accountId":"acc_1","status":"idle","progress":-1,"pendingOutbox":0}}"""),
            (API.Notify.AuthRequired, """{"accountId":"acc_1","reason":1200,"message":"m"}"""),
            (API.Notify.AccountsChanged, "{}"),
        ];
        Assert.Equal(API.AllNotifications, samples.Select(s => s.Method));
        foreach (var (method, parameters) in samples)
        {
            var n = Raw(method, parameters);
            Assert.False(n is DaemonNotification.Unknown, $"{method} decoded as unknown");
        }
    }

    /// <summary>
    /// Windows addition: a transport that has taken the line apart hands over
    /// the params alone; absent or null params of a method that has them are
    /// an error, as a line without them is.
    /// </summary>
    [Fact]
    public void ParamsDecodeWithoutTheLine()
    {
        var parameters = ApiJson.Parse("""{"state":{"accountId":"a","status":"idle","progress":-1,"pendingOutbox":0}}""");
        var n = DaemonNotification.Decode(API.Notify.SyncState, parameters);
        Assert.Equal("a", Assert.IsType<DaemonNotification.SyncState>(n).State.AccountId);
        Assert.Equal(new DaemonNotification.AccountsChanged(), DaemonNotification.Decode(API.Notify.AccountsChanged, (JsonElement?)null));
        Assert.Throws<JsonException>(() => DaemonNotification.Decode(API.Notify.SyncState, (JsonElement?)null));
        Assert.Throws<JsonException>(() => DaemonNotification.Decode(API.Notify.AuthRequired, ApiJson.Parse("null")));
        Assert.Throws<JsonException>(() => DaemonNotification.Decode(API.Notify.NewMessage, """{"jsonrpc":"2.0","method":"notify.newMessage"}"""u8));
        Assert.Throws<JsonException>(() => DaemonNotification.Decode(API.Notify.NewMessage, "[]"u8));
    }
}
