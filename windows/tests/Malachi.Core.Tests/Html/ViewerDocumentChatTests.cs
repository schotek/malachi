// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ViewerDocument.ChatDocument: the document of a chat bubble rendering HTML
// (the preference conversation-chat-html) neutralises every background of
// the mail and has no frame of its own; the card's document keeps its
// white paper; in the dark theme and high contrast the text and links take
// the colours passed in, and nothing but a colour reaches the stylesheet.

using System;
using Malachi.Core.Html;
using Xunit;

namespace Malachi.Core.Tests.Html;

public sealed class ViewerDocumentChatTests
{
    private const string Body = "<p style=\"background:#ccc;color:#333\">x</p>";

    [Fact]
    public void TheChatVariantNeutralisesBackgroundsAndTheCardsDoesNot()
    {
        var chat = ViewerDocument.ChatDocument(Body, palette: null);
        Assert.Contains(ViewerDocument.ChatNeutralCss, chat, StringComparison.Ordinal);
        Assert.Contains("background-color: transparent !important; background-image: none !important;", chat, StringComparison.Ordinal);
        Assert.Contains("padding: 0 !important;", chat, StringComparison.Ordinal);
        Assert.Contains(Body, chat, StringComparison.Ordinal);
        Assert.Contains("content=\"" + ViewerDocument.Csp + "\"", chat, StringComparison.Ordinal);
        // Light: the mail's own colours stay.
        Assert.DoesNotContain("color: rgb", chat, StringComparison.Ordinal);
        Assert.DoesNotContain("* { color:", chat, StringComparison.Ordinal);

        var card = ViewerDocument.CompactDocument(Body);
        Assert.DoesNotContain(ViewerDocument.ChatNeutralCss, card, StringComparison.Ordinal);
        Assert.DoesNotContain("transparent", card, StringComparison.Ordinal);
        Assert.Contains("background: #ffffff;", card, StringComparison.Ordinal);
    }

    // No scroll bar of the bubble's own: html and body never scroll and have
    // no margin, the column holds its children's margins (flow-root, after
    // the column's display: block, so it wins), and the card has neither.
    [Fact]
    public void TheChatVariantNeverScrollsAndHoldsItsMargins()
    {
        var chat = ViewerDocument.ChatDocument(Body, palette: null);
        Assert.Contains("html, body { overflow: hidden !important; margin: 0 !important; }", chat, StringComparison.Ordinal);
        var block = chat.IndexOf("display: block !important", StringComparison.Ordinal);
        var flow = chat.IndexOf("#malachi-column { display: flow-root !important; }", StringComparison.Ordinal);
        Assert.True(block >= 0 && flow > block, "flow-root after the column's display: block");
        var card = ViewerDocument.CompactDocument(Body);
        Assert.DoesNotContain("overflow: hidden", card, StringComparison.Ordinal);
        Assert.DoesNotContain("flow-root", card, StringComparison.Ordinal);
    }

    [Fact]
    public void ThePaletteForcesTheTextAndTheLinks()
    {
        var dark = ViewerDocument.ChatDocument(Body, new ChatPalette("rgba(255,255,255,1)", "#99ebff"));
        Assert.Contains("* { color: rgba(255,255,255,1) !important; }", dark, StringComparison.Ordinal);
        Assert.Contains("a, a * { color: #99ebff !important; }", dark, StringComparison.Ordinal);
        var contrast = ViewerDocument.ChatDocument(Body, new ChatPalette("CanvasText", "LinkText"));
        Assert.Contains("* { color: CanvasText !important; }", contrast, StringComparison.Ordinal);
        Assert.Contains("a, a * { color: LinkText !important; }", contrast, StringComparison.Ordinal);
    }

    [Theory]
    [InlineData("red; } body { display: none", "#fff")]
    [InlineData("#fff", "url(x)")]
    [InlineData("rgb(1,2,3)) } *{x:y", "#fff")]
    [InlineData("", "#fff")]
    [InlineData("#12345", "#fff")]
    public void NothingButAColourReachesTheStylesheet(string text, string link)
    {
        var doc = ViewerDocument.ChatDocument(Body, new ChatPalette(text, link));
        Assert.Equal(ViewerDocument.ChatDocument(Body, palette: null), doc);
    }
}
