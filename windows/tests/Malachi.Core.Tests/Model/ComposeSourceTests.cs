// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ComposeSourceTests.swift, the
// counterpart of ui/internal/window/compose_open_test.go
// (TestComposeSource, TestComposeFallbackText).

using System;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Model;
using Malachi.Core.Transport;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class ComposeSourceTests
{
    // The source of a reply is the summary until the full message and the
    // body are loaded; a body that is not fetched contributes no text.
    [Fact]
    public void ComposeSourceTest()
    {
        var date = DateTimeOffset.FromUnixTimeSeconds(1_788_775_200); // 2026-09-07T10:00:00Z
        var s = new MessageSummary
        {
            Id = new MessageId("m_1"),
            AccountId = new AccountId("acc"),
            FolderId = new FolderId("f"),
            From = [new Address { Email = "a@example.invalid" }],
            To = [new Address { Email = "me@example.invalid" }],
            Subject = "s",
            Date = date,
            Snippet = "",
            HasAttachments = false,
            Size = 0,
        };

        var src = ComposeSources.ComposeSource(s, lm: null);
        Assert.Equal(new MessageId("m_1"), src.Id);
        Assert.Equal(new AccountId("acc"), src.AccountId);
        Assert.Single(src.From);
        Assert.Single(src.To);
        Assert.Equal("s", src.Subject);
        Assert.Equal(date, src.Date);
        Assert.Equal("", src.Text);
        Assert.Empty(src.ReplyTo);

        var headers = s with { From = [new Address { Name = "A", Email = "a@example.invalid" }], Subject = "full" };
        var full = new Message
        {
            Summary = headers,
            Cc = [new Address { Email = "c@example.invalid" }],
            ReplyTo = [new Address { Email = "r@example.invalid" }],
        };
        var lm = new LoadedMessage { Msg = full, Body = Body(BodyState.Pending, "not yet") };
        src = ComposeSources.ComposeSource(s, lm);
        Assert.Equal("A", src.From[0].Name);
        Assert.Single(src.ReplyTo);
        Assert.Single(src.Cc);
        Assert.Equal("full", src.Subject);
        Assert.Equal("", src.Text);
        lm.Body = Body(BodyState.Fetched, "hello");
        src = ComposeSources.ComposeSource(s, lm);
        Assert.Equal("hello", src.Text);

        // Go's zero date is no date.
        s = s with { Date = DateTimeOffset.MinValue };
        Assert.Null(ComposeSources.ComposeSource(s, lm: null).Date);
        Assert.Equal("Preparing the forwarded message", ComposeSources.ComposeWhat(ComposeKind.Forward));
        Assert.Equal("Preparing the reply", ComposeSources.ComposeWhat(ComposeKind.Reply));
        Assert.Equal("Preparing the reply", ComposeSources.ComposeWhat(ComposeKind.ReplyAll));
    }

    // No toast when there is no backend to ask or it lacks the call; a
    // sentence for everything else.
    [Fact]
    public void ComposeFallbackTextTest()
    {
        Assert.Equal("", ComposeSources.ComposeFallbackText("Preparing the reply", new RpcClientException(ClientError.Disconnected)));
        Assert.Equal("", ComposeSources.ComposeFallbackText("Preparing the reply", new RpcClientException(ClientError.NotConnected)));
        Assert.Equal("", ComposeSources.ComposeFallbackText("Preparing the reply",
            new RpcException(new RpcError { Code = ErrorCode.NotImplemented, Message = "x" })));
        Exception[] others =
        [
            new RpcClientException(ClientError.Timeout("draft.create")),
            new OperationCanceledException(),
            new RpcException(new RpcError { Code = ErrorCode.MessageNotFound, Message = "gone" }),
            new InvalidOperationException("boom"),
        ];
        foreach (var err in others)
        {
            Assert.True(ComposeSources.ComposeFallbackText("Preparing the reply", err).Length > 0, $"{err}: no toast");
        }
    }

    private static MessageBodyResult Body(BodyState state, string text) => new()
    {
        MessageId = new MessageId("m_1"),
        BodyState = state,
        HasHtml = false,
        Text = text,
        RemoteContent = RemoteContentPolicy.Block,
        SanitizerVersion = "1",
    };
}
