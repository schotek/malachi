// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/NotifiedMessagesTests.swift, the
// counterpart of ui/internal/window/notified_test.go (every test).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class NotifiedMessagesTests
{
    private static readonly FolderKey InboxA = new("a", "inbox");
    private static readonly FolderKey ArchiveA = new("a", "archive");
    private static readonly FolderKey InboxB = new("b", "inbox");

    [Fact]
    public void AddRemove()
    {
        var s = new NotifiedMessages();
        s.Add("m1", InboxA);
        s.Add("m2", ArchiveA);
        s.Add("m3", InboxB);
        // Delivered twice: one entry, now the newest.
        Assert.Empty(s.Add("m1", InboxA));
        Assert.Equal(Ids("m2", "m3", "m1"), Ids(s.Entries));

        // Only what the set holds comes back: nothing is withdrawn blindly.
        Assert.Equal(Ids("m3"), s.Remove(Ids("m9", "m3", "m3")));
        Assert.Empty(s.Remove(Ids("m3")));
        Assert.Empty(s.Remove([]));
        Assert.Equal(Ids("m2", "m1"), Ids(s.Entries));

        Assert.Empty(new NotifiedMessages().Remove(Ids("m1")));
    }

    [Fact]
    public void Bounded()
    {
        var s = new NotifiedMessages();
        for (var i = 0; i < NotifiedMessages.Max; i++)
        {
            Assert.Empty(s.Add(new MessageId($"m{i}"), InboxA));
        }
        // The oldest goes, and the caller is told to withdraw it.
        Assert.Equal(Ids("m0"), s.Add("new", InboxA));
        Assert.Equal(NotifiedMessages.Max, s.Entries.Count);
        Assert.Equal("m1", s.Entries[0].Id.Value);
        Assert.Equal("new", s.Entries[^1].Id.Value);
        // Re-adding a held message evicts nothing.
        Assert.Empty(s.Add("m1", InboxA));
    }

    [Fact]
    public void FolderAndAccount()
    {
        var s = new NotifiedMessages();
        s.Add("m1", InboxA);
        s.Add("m2", ArchiveA);
        s.Add("m3", InboxA);
        s.Add("m4", InboxB);

        var got = s.OfAccount("a");
        Assert.Equal(Ids("m1", "m2", "m3"), Ids(got));
        Assert.Equal(ArchiveA, got[1].Key);
        Assert.Empty(s.OfAccount("zzz"));

        // Viewing the inbox withdraws the inbox's notifications, not the
        // archive's or the other account's inbox.
        Assert.Equal(Ids("m1", "m3"), s.RemoveFolder(InboxA));
        Assert.Empty(s.RemoveFolder(InboxA));

        // Account b was removed or paused.
        Assert.Equal(Ids("m4"), s.RemoveAccountsExcept(new HashSet<AccountId> { "a" }));
        Assert.Equal(Ids("m2"), s.RemoveAccountsExcept(new HashSet<AccountId>()));
        Assert.Empty(s.Entries);
    }

    [Fact]
    public void Outdated()
    {
        var e = new NotifiedEntry("m1", InboxA);
        var unread = Summary(Flag.Flagged);
        var read = unread with { Flags = [Flag.Flagged, Flag.Seen] };
        var moved = unread with { FolderId = "archive" };
        var noFolder = unread with { FolderId = "" }; // an older daemon: nothing to compare

        Assert.False(NotifiedMessages.Outdated(e, unread, null));
        Assert.True(NotifiedMessages.Outdated(e, read, null));
        Assert.True(NotifiedMessages.Outdated(e, moved, null));
        Assert.False(NotifiedMessages.Outdated(e, noFolder, null));
        Assert.True(NotifiedMessages.Outdated(e, null, Rpc(ErrorCode.MessageNotFound)));
        Assert.True(NotifiedMessages.Outdated(e, null, Rpc(ErrorCode.AccountNotFound)));
        Assert.Null(NotifiedMessages.Outdated(e, null, Rpc(ErrorCode.StorageError)));
        Assert.Null(NotifiedMessages.Outdated(e, null, new RpcClientException(ClientError.Disconnected)));
        Assert.Null(NotifiedMessages.Outdated(e, null, new RpcClientException(ClientError.Timeout("message.get"))));
        Assert.Null(NotifiedMessages.Outdated(e, null, new TaskCanceledException()));
    }

    [Fact]
    public void Identifier() => Assert.Equal("message-m_42", NotifiedMessages.NotificationId("m_42"));

    private static MessageId[] Ids(params string[] ids) => [.. ids.Select(id => new MessageId(id))];

    private static MessageId[] Ids(IEnumerable<NotifiedEntry> entries) => [.. entries.Select(e => e.Id)];

    private static RpcException Rpc(int code) => new(new RpcError { Code = code, Message = "x" });

    private static MessageSummary Summary(params Flag[] flags) => new()
    {
        Id = "m1",
        AccountId = "a",
        FolderId = "inbox",
        From = [],
        Subject = "s",
        Date = DateTimeOffset.MinValue,
        Snippet = "",
        Flags = flags,
        HasAttachments = false,
        Size = 0,
    };
}
