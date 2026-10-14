// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ComposeBodyLayoutTests.swift (the
// layout's three tests; its two prompt tests are AssistantWriteTests), the
// counterpart of ui/internal/composebody/layout_test.go
// (TestPreparedOriginalIsKeptWhole, TestExistingBodyIsNeverMistakenForAnOriginal,
// TestUnavailableOriginalDoesNotCreateAnEmptySection). Go's cases over a mode
// and Swift's over ComposeParams both run: the two constructors.

using Malachi.Core.Api;
using Malachi.Core.Compose;
using Xunit;

namespace Malachi.Core.Tests.Compose;

public sealed class ComposeBodyLayoutTests
{
    private const string Original = "<p><br></p><div>A wrote:</div><blockquote type=\"cite\"><p>Original &amp; text</p><img src=\"cid:image\"></blockquote>";

    /// <summary>
    /// A new reply or forward keeps the prepared original whole, apart from
    /// the editor, whether included or not.
    /// </summary>
    [Theory]
    [InlineData(ComposeMode.Reply)]
    [InlineData(ComposeMode.ReplyAll)]
    [InlineData(ComposeMode.Forward)]
    public void PreparedOriginalIsKeptWhole(string mode) => KeptWhole(new ComposeBodyLayout(mode, false, Original));

    /// <summary>The same, from what the window was opened with (Swift's arguments).</summary>
    [Theory]
    [InlineData(ComposeKind.Reply)]
    [InlineData(ComposeKind.ReplyAll)]
    [InlineData(ComposeKind.Forward)]
    public void PreparedOriginalOfParamsIsKeptWhole(ComposeKind kind) =>
        KeptWhole(new ComposeBodyLayout(new ComposeParams { Kind = kind, BodyHtml = Original }));

    /// <summary>
    /// A body that may hold the user's edits, a comment and an unknown mode
    /// stay whole in the editor.
    /// </summary>
    [Theory]
    [InlineData(ComposeMode.New, false)]
    [InlineData(ComposeMode.Reply, true)]
    [InlineData("", false)]
    [InlineData("draft", false)]
    public void ExistingBodyIsNeverMistakenForAnOriginal(string mode, bool comment)
    {
        const string body = "<p>User edits</p>" + Original;
        var l = new ComposeBodyLayout(mode, comment, body);
        Assert.Equal(body, l.EditorHtml);
        Assert.False(l.HasQuote);
        Assert.Equal(body, l.Html(body, true));
        Assert.Equal("x", l.Text("x", "y", true));
    }

    /// <summary>Swift's cases: a new message and a draft opened from Drafts.</summary>
    [Theory]
    [InlineData(ComposeKind.New)]
    [InlineData(ComposeKind.Edit)]
    public void ExistingBodyOfParamsIsNeverMistakenForAnOriginal(ComposeKind kind)
    {
        const string body = "<p>User edits</p>" + Original;
        var l = new ComposeBodyLayout(new ComposeParams { Kind = kind, BodyHtml = body });
        Assert.Equal(body, l.EditorHtml);
        Assert.Equal("", l.QuotedHtml);
        Assert.Equal(body, l.Html(body, true));
    }

    /// <summary>Without an original (draft.create brought none) there is no panel.</summary>
    [Fact]
    public void UnavailableOriginalDoesNotCreateAnEmptySection()
    {
        var l = new ComposeBodyLayout(ComposeMode.Reply, false, "");
        Assert.False(l.HasQuote);
        Assert.Equal("", l.EditorHtml);
        Assert.Equal("Answer", l.Text("Answer", "", true));
        Assert.Equal("<p>A</p>", l.Html("<p>A</p>", true));
        Assert.Equal("", new ComposeBodyLayout(new ComposeParams { Kind = ComposeKind.Reply }).QuotedHtml);
    }

    private static void KeptWhole(ComposeBodyLayout l)
    {
        Assert.Equal(ComposeBodyLayout.EmptyBody, l.EditorHtml);
        Assert.Equal("<p><br></p>", l.EditorHtml);
        Assert.Equal(Original, l.QuotedHtml);
        Assert.True(l.HasQuote);
        Assert.Equal("<p>Answer</p>\n" + Original, l.Html("<p>Answer</p>", true));
        Assert.Equal("<p>Answer</p>", l.Html("<p>Answer</p>", false));
        Assert.Equal("Answer\n\nA wrote:\nOriginal & text", l.Text("Answer", "A wrote:\nOriginal & text", true));
        Assert.Equal("Answer", l.Text("Answer", "Original", false));
        // Toggling inclusion never modifies or loses the prepared original.
        Assert.Equal(Original, l.QuotedHtml);
    }
}
