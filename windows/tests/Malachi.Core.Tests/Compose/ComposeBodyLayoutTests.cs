// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ComposeBodyLayoutTests.swift (the
// layout's three tests; its two prompt tests are AssistantWriteTests), the
// counterpart of ui/internal/composebody/layout_test.go
// (TestPreparedOriginalIsKeptWhole, TestExistingBodyIsNeverMistakenForAnOriginal,
// TestUnavailableOriginalDoesNotCreateAnEmptySection, TestDraftBody,
// TestWithOwn) and of ui/internal/compose/prefill_test.go
// (TestBodyLayoutStartsWithTheOwnText); Swift's draftBody, withOwn,
// bodyLayoutStartsWithTheOwnText and ownTextOfAComment. Go's cases over a
// mode and Swift's over ComposeParams both run: the two constructors.

using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Model;
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

    /// <summary>
    /// composebody TestDraftBody: a draft's body is its HTML when it has one;
    /// otherwise its text, escaped, with its line breaks (CRLF too) as
    /// &lt;br&gt;.
    /// </summary>
    [Fact]
    public void DraftBody()
    {
        Assert.Equal(Original, ComposeBodyLayout.DraftBody(new Draft { AccountId = "", TextBody = "ignored", HtmlBody = Original }));
        Assert.Equal("a &lt;b&gt;&amp;<br>c<br>d", ComposeBodyLayout.DraftBody(new Draft { AccountId = "", TextBody = "a <b>&\r\nc\nd" }));
        // An empty HTML body is no HTML (Go's "").
        Assert.Equal("t", ComposeBodyLayout.DraftBody(new Draft { AccountId = "", TextBody = "t", HtmlBody = "" }));
        Assert.Equal("", ComposeBodyLayout.DraftBody(new Draft { AccountId = "" }));
        Assert.Equal("&lt;script&gt;&#34;x&#34;&lt;/script&gt;", Prefill.EscapeText("<script>\"x\"</script>"));
    }

    /// <summary>
    /// composebody TestWithOwn: the user's own text replaces the editor's
    /// start, the original apart stays, and the saved body is the own text
    /// above it; "" changes nothing.
    /// </summary>
    [Fact]
    public void WithOwn()
    {
        const string own = "<p>Thanks</p>";
        var l = new ComposeBodyLayout(ComposeMode.Reply, false, Original).WithOwn(own);
        Assert.Equal(own, l.EditorHtml);
        Assert.Equal(Original, l.QuotedHtml);
        Assert.Equal(own + "\n" + Original, l.Html(own, true));
        var none = new ComposeBodyLayout(ComposeMode.Forward, false, Original).WithOwn("");
        Assert.Equal(ComposeBodyLayout.EmptyBody, none.EditorHtml);
        Assert.Equal(Original, none.QuotedHtml);
        // Without an original the own text is the whole editor.
        var bare = new ComposeBodyLayout(ComposeMode.Reply, false, "").WithOwn(own);
        Assert.Equal(own, bare.EditorHtml);
        Assert.Equal("", bare.QuotedHtml);
        // A comment has no original apart: the own text is the body.
        var comment = new ComposeBodyLayout(ComposeMode.Reply, true, "").WithOwn(own);
        Assert.Equal(own, comment.EditorHtml);
        Assert.Equal("", comment.QuotedHtml);
        Assert.Equal(own, comment.Html(own, true));
        // The receiver is not changed.
        var original = new ComposeBodyLayout(ComposeMode.Reply, false, Original);
        _ = original.WithOwn(own);
        Assert.Equal(ComposeBodyLayout.EmptyBody, original.EditorHtml);
    }

    /// <summary>
    /// prefill_test.go TestBodyLayoutStartsWithTheOwnText: the user's own
    /// text (<see cref="ComposeParams.OwnHtml"/>, from the chat's quick-reply
    /// bar) starts the window's editor in place of the empty body, above the
    /// original apart, for a reply, a forward and a comment; the inline
    /// layout (params of its own, Edit) does not get it, and a window without
    /// it is unchanged. FromDraft never sets it.
    /// </summary>
    [Theory]
    [InlineData(ComposeKind.Reply)]
    [InlineData(ComposeKind.ReplyAll)]
    [InlineData(ComposeKind.Forward)]
    public void BodyLayoutStartsWithTheOwnText(ComposeKind kind)
    {
        const string original = "<p><br></p><blockquote type=\"cite\">hi</blockquote>";
        const string own = "<p>Thanks</p>";
        var p = Prefill.FromDraft(kind, new Draft { AccountId = "", HtmlBody = original }, new BlockedContent());
        Assert.Equal("", p.OwnHtml);
        var plain = new ComposeBodyLayout(p);
        Assert.Equal(ComposeBodyLayout.EmptyBody, plain.EditorHtml);
        Assert.Equal(original, plain.QuotedHtml);
        p = p with { OwnHtml = own };
        var window = new ComposeBodyLayout(p);
        Assert.Equal(own, window.EditorHtml);
        Assert.Equal(original, window.QuotedHtml);
        Assert.Equal(new ComposeBodyLayout(kind.Mode, false, original).WithOwn(own), window);
        // ComposePane's inline layout.
        var inline = new ComposeBodyLayout(new ComposeParams { Kind = ComposeKind.Edit, BodyHtml = p.BodyHtml });
        Assert.Equal(original, inline.EditorHtml);
        Assert.Equal("", inline.QuotedHtml);
        // Params differ by their own text.
        Assert.NotEqual(p with { OwnHtml = "" }, p);
    }

    [Fact]
    public void OwnTextOfAComment()
    {
        var issue = new IssueInfo { Key = "PRJ-1", Url = "", Summary = "", Status = "" };
        var comment = Prefill.FromDraft(
            ComposeKind.Reply,
            new Draft { AccountId = "", Comment = new DraftComment { Issue = issue, Visibility = CommentVisibility.Public } },
            new BlockedContent()) with { OwnHtml = "<p>Thanks</p>" };
        var l = new ComposeBodyLayout(comment);
        Assert.Equal("<p>Thanks</p>", l.EditorHtml);
        Assert.Equal("", l.QuotedHtml);
    }

    /// <summary>
    /// The quick reply's body is what this layout saves (Conversation's
    /// QuickReplyBody over DraftBody).
    /// </summary>
    [Fact]
    public void TheQuickReplysBodyIsTheWindows()
    {
        var d = new Draft { AccountId = "", HtmlBody = Original };
        var own = Conversation.QuickReplyHtml("Thanks");
        var window = new ComposeBodyLayout(Prefill.FromDraft(ComposeKind.Reply, d, new BlockedContent()) with { OwnHtml = own });
        Assert.Equal(window.Html(window.EditorHtml, true), Conversation.QuickReplyBody(ComposeMode.Reply, "Thanks", d));
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
