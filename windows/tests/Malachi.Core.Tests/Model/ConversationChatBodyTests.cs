// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ConversationChatTests.swift
// (ConversationChatBodyTests: the variant of a body a bubble reads, its
// text in every state, the attachments pill, the end of the chat, the
// short dates, a resize, the monospaced font). Swift-first:
// ui/internal/conversation/chat.go leaves these rules to the clients
// (Conversation.ChatBody.cs, ConversationLayout.Chat.cs). The English
// catalogue: the msgids come back verbatim. The fixtures are
// ConversationTests'.

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Model.ConversationTests;

namespace Malachi.Core.Tests.Model;

public sealed class ConversationChatBodyTests
{
    private static MessageBodyResult Body(string text, string state = BodyState.Fetched, bool trimmed = false, bool html = false) => new()
    {
        MessageId = "m1",
        BodyState = state,
        HasHtml = html,
        Text = text,
        RemoteContent = RemoteContentPolicy.Block,
        SanitizerVersion = "1",
        QuotedTrimmed = trimmed,
    };

    private static RpcClientException Timeout() => new(ClientError.Timeout("message.body"));

    [Fact]
    public void ChatBodyReadsTheTrimmedVariant()
    {
        // Trimmed and shown: the body.
        var lm = new LoadedMessage();
        lm.Store(Body("Hi", trimmed: true), quoted: false);
        Assert.Equal("Hi", Conversation.ChatBody(lm, lm.QuotedShown)?.Text);

        // The user revealed the quoted history: the trimmed variant kept
        // aside, not the whole body shown.
        lm.ShowQuoted(true);
        lm.Store(Body("Hi\n> quoted"), quoted: true);
        Assert.True(lm.QuotedShown);
        Assert.Equal("Hi", Conversation.ChatBody(lm, lm.QuotedShown)?.Text);
        Assert.Equal("Hi\n> quoted", Conversation.ChatBody(lm, quotedRevealed: false)?.Text);

        // Revealed before the trimmed variant was ever fetched: the whole
        // body is all there is.
        var whole = new LoadedMessage();
        whole.ShowQuoted(true);
        whole.Store(Body("Hi\n> quoted"), quoted: true);
        Assert.Equal("Hi\n> quoted", Conversation.ChatBody(whole, quotedRevealed: true)?.Text);

        // Nothing yet.
        Assert.Null(Conversation.ChatBody(new LoadedMessage(), quotedRevealed: false));
    }

    [Fact]
    public void BubbleBodyStates()
    {
        const string snippet = "  See the\r\nfigures  ";
        Assert.Equal(new BubbleBody.Placeholder("See the\nfigures"), Conversation.BubbleBodyFor(null, snippet));
        Assert.Equal(new BubbleBody.Placeholder("See the\nfigures"), Conversation.BubbleBodyFor(new LoadedMessage(), snippet));
        Assert.Equal(new BubbleBody.Placeholder(""), Conversation.BubbleBodyFor(null, null));

        var lm = new LoadedMessage();
        lm.Store(Body("Hello\r\n\r\n\r\nBye\r\n"), quoted: false);
        Assert.Equal(new BubbleBody.Text("Hello\n\nBye"), Conversation.BubbleBodyFor(lm, snippet));

        lm.Store(Body(" \n\t "), quoted: false);
        Assert.Equal(new BubbleBody.Text("(Empty message)"), Conversation.BubbleBodyFor(lm, snippet));
        lm.Store(Body("", BodyState.Pending), quoted: false);
        Assert.Equal(new BubbleBody.Text("Downloading…"), Conversation.BubbleBodyFor(lm, snippet));
        lm.Store(Body("", BodyState.TooBig), quoted: false);
        Assert.Equal(new BubbleBody.Text("This message is too large to download."), Conversation.BubbleBodyFor(lm, snippet));
        lm.Store(Body("", BodyState.Failed), quoted: false);
        Assert.Equal(new BubbleBody.Text("This message could not be read."), Conversation.BubbleBodyFor(lm, snippet));

        // The call failed: the cards' sentence.
        var failed = new LoadedMessage { Err = Timeout() };
        Assert.Equal(
            new BubbleBody.Text(RpcErrorText.Text("Loading the message", Timeout())),
            Conversation.BubbleBodyFor(failed, snippet));

        // The whole body failed after a reveal: the trimmed variant kept
        // aside still reads.
        var aside = new LoadedMessage();
        aside.Store(Body("Short"), quoted: false);
        aside.ShowQuoted(true);
        aside.Err = Timeout();
        Assert.Equal(new BubbleBody.Text("Short"), Conversation.BubbleBodyFor(aside, snippet));
    }

    [Fact]
    public void BubbleAttachmentBadge()
    {
        static MessageSummary Summary(bool has) => Mail("m1", 0, true) with { HasAttachments = has };
        static Attachment Pdf(string part, string? cid = null) => new()
        {
            PartId = part,
            Filename = part + ".pdf",
            ContentType = "application/pdf",
            Size = 1000,
            Inline = cid is not null,
            ContentId = cid,
        };
        BubbleAttachments none = new BubbleAttachments.None();
        BubbleAttachments some = new BubbleAttachments.Some();

        // Nothing known but the summary: the pill alone, or none.
        Assert.Equal(some, Conversation.BubbleAttachmentBadge(Summary(true), null));
        Assert.Equal(none, Conversation.BubbleAttachmentBadge(Summary(false), null));
        Assert.Equal(none, Conversation.BubbleAttachmentBadge(null, null));
        // The body alone (message.get not answered, or failed): still the
        // summary's word.
        var bodyOnly = new LoadedMessage();
        bodyOnly.Store(Body("Hi"), quoted: false);
        Assert.Equal(some, Conversation.BubbleAttachmentBadge(Summary(true), bodyOnly));
        Assert.Equal(none, Conversation.BubbleAttachmentBadge(Summary(false), bodyOnly));

        // message.get answered: the number of the timeline's chips.
        var two = new LoadedMessage();
        two.Store(Body("Hi"), quoted: false);
        two.Msg = new Message { Summary = Summary(true), Attachments = [Pdf("2"), Pdf("3")] };
        Assert.Equal(new BubbleAttachments.Count(2), Conversation.BubbleAttachmentBadge(Summary(true), two));
        Assert.Equal(
            new BubbleAttachments.Count(AttachmentChips.ChipAttachments(two.Msg!.Attachments, two.Body).Count),
            Conversation.BubbleAttachmentBadge(Summary(true), two));

        // A part the daemon calls inline (a Content-ID) leaves the summary's
        // flag false, and the timeline still shows its chip (a plain-text
        // body shows no part itself): so does the bubble.
        var inline = new LoadedMessage();
        inline.Store(Body("See the attached"), quoted: false);
        inline.Msg = new Message { Summary = Summary(false), Attachments = [Pdf("2", cid: "scan@x")] };
        Assert.Equal(new BubbleAttachments.Count(1), Conversation.BubbleAttachmentBadge(Summary(false), inline));

        // An image the HTML body shows through cid: no chip, no pill.
        var shown = new LoadedMessage();
        shown.Store(
            new MessageBodyResult
            {
                MessageId = "m1",
                BodyState = BodyState.Fetched,
                HasHtml = true,
                Html = "<img src=\"cid:logo@x\">",
                Text = "",
                InlineParts = new Dictionary<string, string> { ["logo@x"] = "2" },
                RemoteContent = RemoteContentPolicy.Block,
                SanitizerVersion = "1",
            },
            quoted: false);
        shown.Msg = new Message { Summary = Summary(false), Attachments = [Pdf("2", cid: "logo@x")] };
        Assert.Equal(none, Conversation.BubbleAttachmentBadge(Summary(true), shown));
    }

    [Fact]
    public void ChatEnd()
    {
        Assert.True(ConversationLayout.ChatAtEnd(top: 600, viewportHeight: 400, documentHeight: 1000));
        Assert.True(ConversationLayout.ChatAtEnd(top: 597, viewportHeight: 400, documentHeight: 1000));
        Assert.False(ConversationLayout.ChatAtEnd(top: 590, viewportHeight: 400, documentHeight: 1000));
        Assert.True(ConversationLayout.ChatAtEnd(top: 0, viewportHeight: 400, documentHeight: 300), "shorter than the view");
        Assert.False(ConversationLayout.ChatAtEnd(top: 597, viewportHeight: 400, documentHeight: 1000, slack: 0));
        Assert.False(ConversationLayout.ChatAtEnd(top: 595.9, viewportHeight: 400, documentHeight: 1000), "beyond the slack of 4");
        Assert.Equal(600, ConversationLayout.ChatEndTop(viewportHeight: 400, documentHeight: 1000));
        Assert.Equal(0, ConversationLayout.ChatEndTop(viewportHeight: 400, documentHeight: 300));
        Assert.Equal(868, ConversationLayout.ChatColumn(1200));
        Assert.Equal(468, ConversationLayout.ChatColumn(500));
        Assert.Equal(0, ConversationLayout.ChatColumn(20));
    }

    [Fact]
    public void ChatCompactDates()
    {
        // The bubble's widest is column - 40 - max(24, column / 5).
        var wide = ConversationLayout.ChatColumn(1200); // 868: bubble 654.4
        Assert.False(ConversationLayout.ChatCompactDates(wide));
        // 600: bubble 440, less 28 of padding: 412 < 420.
        Assert.True(ConversationLayout.ChatCompactDates(600));
        // The cards of the same pane (632) still show the full date.
        Assert.False(ConversationLayout.CompactDates(632));
        // The boundary: 0.8 * c - 40 - 28 == 420 at c = 610.
        Assert.False(ConversationLayout.ChatCompactDates(610));
        Assert.True(ConversationLayout.ChatCompactDates(609));
        Assert.True(ConversationLayout.ChatCompactDates(0));
    }

    [Fact]
    public void ChatResize()
    {
        Assert.Null(ConversationLayout.ChatResizeFor((500, 400), (500.2, 400.3), pinned: true, wasAtEnd: true));
        // Only the height: no reflow, the end stays in view when it was.
        Assert.Equal(
            new ConversationLayout.ChatResize(Width: false, ToEnd: true),
            ConversationLayout.ChatResizeFor((500, 400), (500, 300), pinned: false, wasAtEnd: true));
        Assert.Equal(
            new ConversationLayout.ChatResize(Width: false, ToEnd: false),
            ConversationLayout.ChatResizeFor((500, 400), (500, 300), pinned: false, wasAtEnd: false));
        Assert.Equal(
            new ConversationLayout.ChatResize(Width: false, ToEnd: true),
            ConversationLayout.ChatResizeFor((500, 400), (500, 500), pinned: true, wasAtEnd: false));
        // The width: a reflow.
        Assert.Equal(
            new ConversationLayout.ChatResize(Width: true, ToEnd: false),
            ConversationLayout.ChatResizeFor((500, 400), (700, 400), pinned: false, wasAtEnd: false));
    }

    [Fact]
    public void BubbleMonospace()
    {
        var plain = new LoadedMessage();
        plain.Store(Body("x"), quoted: false);
        Assert.True(Conversation.BubbleMonospace(plain, monospace: true));
        Assert.False(Conversation.BubbleMonospace(plain, monospace: false));
        var html = new LoadedMessage();
        html.Store(Body("x", html: true), quoted: false);
        Assert.False(Conversation.BubbleMonospace(html, monospace: true), "text derived from HTML");
        var pending = new LoadedMessage();
        pending.Store(Body("x", BodyState.Pending), quoted: false);
        Assert.False(Conversation.BubbleMonospace(pending, monospace: true));
        Assert.False(Conversation.BubbleMonospace(null, monospace: true), "the placeholder");
        Assert.False(Conversation.BubbleMonospace(new LoadedMessage(), monospace: true));
        var failed = new LoadedMessage { Err = Timeout() };
        Assert.False(Conversation.BubbleMonospace(failed, monospace: true));
        // The whole body failed after a reveal: the trimmed variant kept
        // aside still reads, in the plain-text font.
        var aside = new LoadedMessage();
        aside.Store(Body("Short"), quoted: false);
        aside.ShowQuoted(true);
        aside.Err = Timeout();
        Assert.True(Conversation.BubbleMonospace(aside, monospace: true));
    }
}
