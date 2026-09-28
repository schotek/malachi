// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ComposeSourceTests.swift, the
// counterpart of ui/internal/window/compose_open_test.go
// (TestComposeSource, TestComposeFallbackText, TestForwardNeedsDownload,
// TestReplyNeedsDownload, TestAskForwardWithout).

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

    /// <summary>
    /// compose_open.go <c>forwardNeedsDownload</c>: a forward downloads first
    /// when any part is on the server (an attachment, or a picture the HTML
    /// shows), or the body is not downloaded yet, or the cache cannot tell
    /// (message.download answers at once when nothing is missing).
    /// </summary>
    [Fact]
    public void ForwardNeedsDownloadTest()
    {
        static Attachment Att(string id, bool inline = false, bool? remote = null) => new()
        {
            PartId = id,
            Filename = id + ".bin",
            ContentType = "application/octet-stream",
            Size = 200_000,
            Inline = inline,
            ContentId = inline ? id + "@x" : null,
            Remote = remote,
        };
        var s = Summary();
        Assert.True(ComposeSources.ForwardNeedsDownload(null), "not in the cache");
        Assert.True(ComposeSources.ForwardNeedsDownload(new LoadedMessage { Body = Body(BodyState.Pending, "") }), "nothing known before message.get");
        Assert.True(ComposeSources.ForwardNeedsDownload(new LoadedMessage { Body = Body(BodyState.Fetched, "") }), "message.get failed");
        var local = new Message { Summary = s, Attachments = [Att("2"), Att("3", remote: false)] };
        Assert.False(ComposeSources.ForwardNeedsDownload(new LoadedMessage { Msg = local, Body = Body(BodyState.Fetched, "") }));
        Assert.False(ComposeSources.ForwardNeedsDownload(new LoadedMessage { Msg = local }), "body not asked for yet");
        Assert.True(ComposeSources.ForwardNeedsDownload(new LoadedMessage { Msg = local, Body = Body(BodyState.Pending, "") }), "a body not downloaded yet");
        var remote = new Message { Summary = s, Attachments = [Att("2"), Att("3", remote: true)] };
        Assert.True(ComposeSources.ForwardNeedsDownload(new LoadedMessage { Msg = remote, Body = Body(BodyState.Fetched, "") }));
        Assert.True(ComposeSources.ForwardNeedsDownload(new LoadedMessage { Msg = remote }), "on the server, body not loaded here");
        Assert.False(ComposeSources.ForwardNeedsDownload(new LoadedMessage { Msg = local, Body = Body(BodyState.TooBig, "") }), "too big to download anyway");
        var picture = new Message { Summary = s, Attachments = [Att("2", inline: true, remote: true)] };
        Assert.True(ComposeSources.ForwardNeedsDownload(new LoadedMessage { Msg = picture, Body = Body(BodyState.Fetched, "") }), "a picture the HTML shows, on the server");
        var storedPicture = new Message { Summary = s, Attachments = [Att("2", inline: true)] };
        Assert.False(ComposeSources.ForwardNeedsDownload(new LoadedMessage { Msg = storedPicture, Body = Body(BodyState.Fetched, "") }), "a stored picture");
        Assert.False(ComposeSources.ForwardNeedsDownload(new LoadedMessage { Msg = new Message { Summary = s }, Body = Body(BodyState.Failed, "") }));
    }

    /// <summary>
    /// compose_open.go <c>replyNeedsDownload</c>: a reply downloads first only
    /// when the body on display counts pictures kept on the mail server only
    /// (the daemon's <c>remotePictures</c>); an attachment there, even one
    /// with a Content-ID, or a message the cache has no body of, is no reason
    /// to.
    /// </summary>
    [Fact]
    public void ReplyNeedsDownloadTest()
    {
        static Attachment Att(string id, string? cid = null, bool inline = false, bool? remote = null) => new()
        {
            PartId = id,
            Filename = id + ".png",
            ContentType = "image/png",
            Size = 200_000,
            Inline = inline,
            ContentId = cid,
            Remote = remote,
        };
        static MessageBodyResult Html(string? html = "<p>x</p>", int? remotePictures = null) => new()
        {
            MessageId = new MessageId("m_1"),
            BodyState = BodyState.Fetched,
            HasHtml = html is not null,
            Html = html,
            Text = "x",
            RemotePictures = remotePictures,
            RemoteContent = RemoteContentPolicy.Block,
            SanitizerVersion = "1",
        };
        var s = Summary();
        LoadedMessage Loaded(Attachment[] atts, MessageBodyResult? b) => new() { Msg = new Message { Summary = s, Attachments = atts }, Body = b };
        var picture = Att("2", cid: "p@x", inline: true, remote: true);
        Assert.False(ComposeSources.ReplyNeedsDownload(null), "not in the cache");
        Assert.False(ComposeSources.ReplyNeedsDownload(new LoadedMessage()), "nothing known yet");
        Assert.False(ComposeSources.ReplyNeedsDownload(Loaded([picture], null)), "body not loaded yet");
        Assert.False(ComposeSources.ReplyNeedsDownload(Loaded([], Html())));
        Assert.False(ComposeSources.ReplyNeedsDownload(Loaded([Att("2", cid: "p@x", remote: true)], Html())), "a Content-ID is no reason");
        Assert.False(ComposeSources.ReplyNeedsDownload(Loaded([Att("2", remote: true)], Html())), "an attachment is not quoted");
        Assert.False(ComposeSources.ReplyNeedsDownload(Loaded([picture], Html())), "a picture the daemon holds is not counted");
        Assert.True(ComposeSources.ReplyNeedsDownload(Loaded([Att("2", remote: true), picture], Html(remotePictures: 2))));
        Assert.True(ComposeSources.ReplyNeedsDownload(new LoadedMessage { Body = Html(remotePictures: 1) }), "counted, message.get failed");
        Assert.False(ComposeSources.ReplyNeedsDownload(Loaded([picture], Html(html: null, remotePictures: 2))), "text only");
    }

    /// <summary>
    /// A failed download asks, unless asking would change nothing: no daemon,
    /// one without message.download, or a message it can never download.
    /// </summary>
    [Fact]
    public void AskForwardWithoutTest()
    {
        Assert.False(ComposeSources.AskForwardWithout(null), "no error, no question");
        Exception[] atOnce =
        [
            Daemon(ErrorCode.MethodNotFound, "x"),
            Daemon(ErrorCode.NotImplemented, "x"),
            new RpcClientException(ClientError.NotConnected),
            new RpcClientException(ClientError.Disconnected),
            Daemon(ErrorCode.AttachmentTooBig, "over the cap"),
        ];
        foreach (var err in atOnce)
        {
            Assert.False(ComposeSources.AskForwardWithout(err), $"{err}");
        }
        Exception[] ask =
        [
            Daemon(ErrorCode.Offline, "x"),
            Daemon(ErrorCode.MessageGone, "x"),
            Daemon(ErrorCode.Unavailable, "x"),
            Daemon(ErrorCode.ServerTimeout, "x"),
            Daemon(ErrorCode.Cancelled, "x"),
            new RpcClientException(ClientError.Timeout("message.download")),
            new RpcClientException(ClientError.Transport("reset")),
            new OperationCanceledException(),
            new InvalidOperationException("boom"),
        ];
        foreach (var err in ask)
        {
            Assert.True(ComposeSources.AskForwardWithout(err), $"{err}");
        }
    }

    private static RpcException Daemon(int code, string message) => new(new RpcError { Code = code, Message = message });

    private static MessageSummary Summary() => new()
    {
        Id = new MessageId("m_1"),
        AccountId = new AccountId("acc"),
        FolderId = new FolderId("f"),
        From = [],
        Subject = "s",
        Date = DateTimeOffset.UnixEpoch,
        Snippet = "",
        HasAttachments = true,
        Size = 0,
    };

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
