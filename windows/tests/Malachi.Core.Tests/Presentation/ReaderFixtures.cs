// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only test fixtures of the reader's presentation classes: the
// records a message view renders.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.Tests.Presentation;

internal static class ReaderFixtures
{
    public static readonly AccountId Account = "acc";
    public static readonly FolderId Folder = "inbox";

    public static MessageSummary Summary(string id, string subject = "Hello", Address? from = null, IReadOnlyList<Address>? to = null) => new()
    {
        Id = id,
        AccountId = Account,
        FolderId = Folder,
        From = [from ?? new Address { Name = "Alice", Email = "alice@example.invalid" }],
        To = to,
        Subject = subject,
        Date = new DateTimeOffset(2026, 9, 2, 13, 4, 0, TimeSpan.Zero),
        Snippet = "",
        HasAttachments = false,
        Size = 1,
    };

    public static Attachment Attachment(string part, string name, string type = "application/pdf", long size = 2048, string? cid = null) => new()
    {
        PartId = part,
        Filename = name,
        ContentType = type,
        Size = size,
        Inline = cid is not null,
        ContentId = cid,
    };

    public static Message Message(MessageSummary s, IReadOnlyList<Attachment>? attachments = null, IReadOnlyList<Address>? cc = null) => new()
    {
        Summary = s,
        Cc = cc,
        Attachments = attachments ?? [],
    };

    public static MessageBodyResult TextBody(string id, string text = "plain body") => new()
    {
        MessageId = id,
        BodyState = BodyState.Fetched,
        HasHtml = false,
        Text = text,
        RemoteContent = RemoteContentPolicy.Block,
        SanitizerVersion = "1",
    };

    public static MessageBodyResult HtmlBody(
        string id, string html = "<p>hi</p>", int remoteImages = 0, string remote = RemoteContentPolicy.Block,
        IReadOnlyList<Link>? links = null, IReadOnlyDictionary<string, string>? inline = null) => new()
        {
            MessageId = id,
            BodyState = BodyState.Fetched,
            HasHtml = true,
            Html = html,
            Text = "plain of " + id,
            Blocked = new BlockedContent { RemoteImages = remoteImages },
            Links = links ?? [],
            InlineParts = inline,
            RemoteContent = remote,
            SanitizerVersion = "1",
        };
}
