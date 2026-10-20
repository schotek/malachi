// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/conversation/reply_test.go (TestQuickReplyTarget,
// TestQuickReplyTargetOfABuiltModel, TestQuickReplyModes, TestQuickReplyHTML,
// TestQuickReplyBlank, TestQuickReplyBody, TestQuickReplyTexts,
// TestSingleModel) and of
// macos/Tests/MalachiCoreTests/ConversationReplyTests.swift (the same, its
// composeKind cases, the window's fromDraft layout in quickReplyBody and
// quickReplyIconOnly), with po_test.go TestMsgidsInTemplate for reply.go.
// The English catalogue: the msgids come back verbatim. The fixtures are
// ConversationTests'; Go's (Item, bool) is a nullable item.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Tests.I18n;
using Xunit;
using static Malachi.Core.Tests.Model.ConversationTests;

namespace Malachi.Core.Tests.Model;

public sealed class ConversationReplyTests
{
    // Characters the text keeps as typed, as escapes: the source stays free
    // of invisible characters.
    private const string Rlo = "\u202E"; // RIGHT-TO-LEFT OVERRIDE
    private const string Lri = "\u2066"; // LEFT-TO-RIGHT ISOLATE
    private const string Zwsp = "\u200B"; // ZERO WIDTH SPACE
    private const string Lrm = "\u200E"; // LEFT-TO-RIGHT MARK
    private const string Bom = "\uFEFF"; // ZERO WIDTH NO-BREAK SPACE
    private const string Nbsp = "\u00A0";
    private const string IdeographicSpace = "\u3000";

    private const string Original = "<p><br></p><div>A wrote:</div><blockquote type=\"cite\"><p>Hi &amp; bye</p><img src=\"cid:i1\"></blockquote>";

    // Petr's reply in his Sent folder fs (thread.get's sent).
    private static MessageSummary Reply(string id, int min) =>
        Mail(id, min, false) with { FolderId = "fs", From = [Petr], To = [Jana], Snippet = "Thanks" };

    // reply_test.go msgItem.
    private static ConversationItem MsgItem(string id, bool mine, bool sent) =>
        new() { Kind = ConversationItemKind.Message, Message = Mail(id, 0, true), Mine = mine, Sent = sent };

    private static ConversationModel ModelOf(params ConversationItem[] items) => new() { Thread = "t1", Items = items };

    [Fact]
    public void QuickReplyTarget()
    {
        var evt = new ConversationItem { Kind = ConversationItemKind.Event, Message = Mail("e", 0, true) };
        var more = new ConversationItem { Kind = ConversationItemKind.Truncated, Text = "2 earlier messages" };
        (string Name, ConversationItem[] Items, string? Want)[] cases =
        [
            ("empty", [], null),
            ("events and the row of older messages only", [more, evt], null),
            ("the newest of someone else", [more, MsgItem("a", false, false), MsgItem("b", false, false), MsgItem("c", true, false)], "b"),
            ("a sent card is the user's", [MsgItem("a", false, false), MsgItem("s", false, true)], "a"),
            ("an event after the message is skipped", [MsgItem("a", false, false), evt], "a"),
            ("all the user's: the newest", [MsgItem("a", true, false), MsgItem("s", false, true), MsgItem("c", true, false), evt], "c"),
            ("a sent card as the newest of the user's", [MsgItem("a", true, false), MsgItem("s", false, true)], "s"),
        ];
        foreach (var c in cases)
        {
            var got = Conversation.QuickReplyTarget(ModelOf(c.Items));
            Assert.True(got?.Id?.Value == c.Want, $"{c.Name}: {got?.Id?.Value ?? "null"}, want {c.Want ?? "null"}");
            if (got is not null)
            {
                Assert.True(got.Kind == ConversationItemKind.Message, $"{c.Name}: kind {got.Kind}");
            }
        }
    }

    // The target of a model Build made: the user's reply in Sent and their
    // own member are passed over for the newest message of someone else.
    [Fact]
    public void QuickReplyTargetOfABuiltModel()
    {
        var own = Mail("m3", 20, true) with { From = [Petr] };
        MessageSummary[] members = [Mail("m1", 0, true), Mail("m2", 10, false), own];
        var m = Conversation.Build(MailThread(3), members, MailAccount, [Reply("s1", 30)]);
        Assert.Equal("m2", Conversation.QuickReplyTarget(m)?.Id?.Value);
    }

    [Fact]
    public void QuickReplyModes()
    {
        (string Name, CapabilityActions A, QuickReplyMode[] Want)[] cases =
        [
            ("none", default, []),
            ("mail", new CapabilityActions { Reply = true, ReplyAll = true, Forward = true, Trash = true }, [QuickReplyMode.Reply, QuickReplyMode.ReplyAll, QuickReplyMode.Forward]),
            ("reply only", new CapabilityActions { Reply = true }, [QuickReplyMode.Reply]),
            ("forward only", new CapabilityActions { Forward = true }, [QuickReplyMode.Forward]),
            ("comment", new CapabilityActions { Reply = true, Forward = true, Comment = true }, [QuickReplyMode.Reply]),
        ];
        foreach (var c in cases)
        {
            var got = Conversation.QuickReplyModes(c.A);
            Assert.True(c.Want.SequenceEqual(got), $"{c.Name}: [{string.Join(", ", got)}], want [{string.Join(", ", c.Want)}]");
        }
        Assert.Equal(ComposeMode.Reply, QuickReplyMode.Reply.ComposeMode.Value);
        Assert.Equal(ComposeMode.ReplyAll, QuickReplyMode.ReplyAll.ComposeMode.Value);
        Assert.Equal(ComposeMode.Forward, QuickReplyMode.Forward.ComposeMode.Value);
        Assert.Equal(ComposeKind.Reply, QuickReplyMode.Reply.ComposeKind);
        Assert.Equal(ComposeKind.ReplyAll, QuickReplyMode.ReplyAll.ComposeKind);
        Assert.Equal(ComposeKind.Forward, QuickReplyMode.Forward.ComposeKind);
    }

    public static TheoryData<string, string, string> HtmlCases => new()
    {
        { "empty", "", "" },
        { "blank", " \n\t\n\r\n", "" },
        { "one line", "Thanks", "<p>Thanks</p>" },
        { "lines", "Hi,\nsee you", "<p>Hi,<br>see you</p>" },
        { "paragraphs", "One\n\n\n\nTwo\nthree", "<p>One</p><p>Two<br>three</p>" },
        { "blank lines around", "\n\n  \nOne\n\n", "<p>One</p>" },
        { "a line of spaces splits", "One\n   \nTwo", "<p>One</p><p>Two</p>" },
        { "CRLF and CR", "a\r\nb\rc\r\n\r\nd", "<p>a<br>b<br>c</p><p>d</p>" },
        { "escaped", "<script>alert(\"x\")</script> & 'y'", "<p>&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt; &amp; &#39;y&#39;</p>" },
        { "markup stays text", "<p>a</p><br><img src=x onerror=y>", "<p>&lt;p&gt;a&lt;/p&gt;&lt;br&gt;&lt;img src=x onerror=y&gt;</p>" },
        { "markdown is not read", "**bold** _x_\n- item", "<p>**bold** _x_<br>- item</p>" },
        // The user's own text: bidi and invisible characters pass as typed
        // (the daemon's sanitiser and the plain-text rules see them).
        { "bidi", "a" + Rlo + "b" + Lri + "c" + Zwsp, "<p>a" + Rlo + "b" + Lri + "c" + Zwsp + "</p>" },
        { "leading spaces kept", "  indented", "<p>  indented</p>" },
    };

    [Theory]
    [MemberData(nameof(HtmlCases))]
    public void QuickReplyHtml(string name, string input, string want) =>
        Assert.True(Conversation.QuickReplyHtml(input) == want, $"{name}: \"{Conversation.QuickReplyHtml(input)}\", want \"{want}\"");

    // Never cut, even over the daemon's cap.
    [Fact]
    public void QuickReplyHtmlNeverCuts()
    {
        var big = new string('x', API.Limits.MaxDraftBodyBytes + 10);
        var got = Conversation.QuickReplyHtml(big);
        Assert.True(got == "<p>" + big + "</p>", $"1 MiB: {got.Length} chars");
        var many = string.Concat(Enumerable.Repeat("<\n", 1 << 16));
        var lines = string.Concat(Enumerable.Repeat("&lt;<br>", 1 << 16));
        var want = "<p>" + lines[..^"<br>".Length] + "</p>";
        var gotMany = Conversation.QuickReplyHtml(many);
        Assert.True(gotMany == want, $"many lines: {gotMany.Length} chars, want {want.Length}");
    }

    public static TheoryData<string, string, bool> BlankCases => new()
    {
        { "empty", "", true },
        { "white space", " \t\r\n", true },
        { "wide spaces", Nbsp + IdeographicSpace, true },
        { "format characters", Zwsp + Lrm + Bom, true },
        { "control characters", "\0\a", true },
        { "bidi and a space", Rlo + " " + Lri, true },
        { "a letter", "x", false },
        { "a dot", "  .  ", false },
        { "a letter after a format character", Zwsp + "x", false },
    };

    [Theory]
    [MemberData(nameof(BlankCases))]
    public void QuickReplyBlank(string name, string text, bool want) =>
        Assert.True(Conversation.QuickReplyBlank(text) == want, $"{name}: {!want}, want {want}");

    // The body is byte for byte what the compose window saves with "Include"
    // checked for the same own HTML.
    [Fact]
    public void QuickReplyBody()
    {
        var d = new Draft { Id = "d1", AccountId = "", TextBody = "A wrote: Hi & bye", HtmlBody = Original };
        const string own = "Thanks!\n\nP.";
        foreach (var (mode, kind) in new[] { (ComposeMode.Reply, ComposeKind.Reply), (ComposeMode.ReplyAll, ComposeKind.ReplyAll), (ComposeMode.Forward, ComposeKind.Forward) })
        {
            var got = Conversation.QuickReplyBody(mode, own, d);
            Assert.True(Bytes("<p>Thanks!</p><p>P.</p>\n" + Original).SequenceEqual(Bytes(got)), $"{mode}: {got}");
            var window = new ComposeBodyLayout(mode, false, ComposeBodyLayout.DraftBody(d)).Html(Conversation.QuickReplyHtml(own), true);
            Assert.True(Bytes(window).SequenceEqual(Bytes(got)), $"{mode}: differs from the window's {window}");
            // The window's own layout of FromDraft's params, too.
            var fromWindow = new ComposeBodyLayout(Prefill.FromDraft(kind, d, new BlockedContent())).Html(Conversation.QuickReplyHtml(own), true);
            Assert.True(Bytes(fromWindow).SequenceEqual(Bytes(got)), $"{mode}: differs from FromDraft's window {fromWindow}");
        }
        // A draft without HTML quotes its text, escaped.
        var text = new Draft { AccountId = "", TextBody = "> <x>\r\n> y" };
        Assert.Equal("<p>ok</p>\n&gt; &lt;x&gt;<br>&gt; y", Conversation.QuickReplyBody(ComposeMode.Reply, "ok", text));
        // A comment has no original apart: the own HTML alone.
        var comment = new Draft
        {
            AccountId = "",
            HtmlBody = "<p>ignored</p>",
            Comment = new DraftComment { Issue = new IssueInfo { Key = "", Url = "", Summary = "", Status = "" }, Visibility = CommentVisibility.Public },
        };
        Assert.Equal("<p>a &lt;b&gt;</p>", Conversation.QuickReplyBody(ComposeMode.Reply, "a <b>", comment));
        // No original at all.
        Assert.Equal("<p>ok</p>", Conversation.QuickReplyBody(ComposeMode.Reply, "ok", new Draft { AccountId = "" }));
    }

    [Fact]
    public void QuickReplyTexts()
    {
        Assert.Equal("Write a reply…", Conversation.QuickReplyPlaceholder(comment: false));
        Assert.Equal("Write a comment…", Conversation.QuickReplyPlaceholder(comment: true));
        Assert.Equal("Reply to: Jana Nováková", Conversation.QuickReplyTargetTooltip(QuickReplyMode.Reply, "Jana Nováková"));
        Assert.Equal("Reply all to: Jana", Conversation.QuickReplyTargetTooltip(QuickReplyMode.ReplyAll, "Jana"));
        // A sender with a percent sign stays as it is (a format argument).
        Assert.Equal("Reply to: 100%s", Conversation.QuickReplyTargetTooltip(QuickReplyMode.Reply, "100%s"));
        foreach (var (mode, sender) in new[] { (QuickReplyMode.Forward, "Jana"), (QuickReplyMode.Reply, ""), (QuickReplyMode.ReplyAll, "") })
        {
            Assert.True(Conversation.QuickReplyTargetTooltip(mode, sender).Length == 0, $"{mode} \"{sender}\"");
        }
        Assert.Equal("Open in a Window", Conversation.QuickReplyOpenWindowLabel());
        Assert.Equal("Forward…", Conversation.QuickReplyForwardLabel());
    }

    [Fact]
    public void QuickReplyIconOnly()
    {
        // The bubbles' short-date rule of the pane's column (pane - 32 up to
        // 900): compact below a column of 610.
        Assert.False(ConversationLayout.QuickReplyIconOnly(1200));
        Assert.False(ConversationLayout.QuickReplyIconOnly(642));
        Assert.True(ConversationLayout.QuickReplyIconOnly(641));
        Assert.True(ConversationLayout.QuickReplyIconOnly(400));
        Assert.True(ConversationLayout.QuickReplyIconOnly(0));
    }

    // po_test.go TestMsgidsInTemplate for reply.go: the bar's six msgids are
    // the template's (the two tooltips c-format), and the port asks for each
    // literally, in Go's order (so that the strings check sees them used).
    [Fact]
    public void TheTextsAreTheTemplatesMsgids()
    {
        var pot = PoFile.Parse(File.ReadAllText(RepositoryPo.Pot), "malachi.pot");
        string[] plain = ["Write a comment…", "Write a reply…", "Open in a Window", "Forward…"];
        string[] formats = ["Reply to: %s", "Reply all to: %s"];
        foreach (var msgid in plain.Concat(formats))
        {
            var entry = pot.Entries.Single(e => e.Msgid == msgid && e.Msgctxt is null);
            Assert.Null(entry.MsgidPlural);
        }
        var template = File.ReadAllText(RepositoryPo.Pot).Replace("\r\n", "\n", StringComparison.Ordinal);
        foreach (var msgid in formats)
        {
            Assert.Contains("#, c-format\nmsgid \"" + msgid + "\"\nmsgstr", template, StringComparison.Ordinal);
        }
        Assert.Equal(L10n.T("Write a comment…"), Conversation.QuickReplyPlaceholder(comment: true));
        Assert.Equal(L10n.T("Write a reply…"), Conversation.QuickReplyPlaceholder(comment: false));
        Assert.Equal(L10n.T("Reply to: %s", "Jana"), Conversation.QuickReplyTargetTooltip(QuickReplyMode.Reply, "Jana"));
        Assert.Equal(L10n.T("Reply all to: %s", "Jana"), Conversation.QuickReplyTargetTooltip(QuickReplyMode.ReplyAll, "Jana"));
        Assert.Equal(L10n.T("Open in a Window"), Conversation.QuickReplyOpenWindowLabel());
        Assert.Equal(L10n.T("Forward…"), Conversation.QuickReplyForwardLabel());

        var port = File.ReadAllText(Path.Combine(RepositoryPo.Root, "windows", "src", "Malachi.Core", "Model", "Conversation.Reply.cs"));
        var calls = port.Split("L10n.").Skip(1).Select(c => c[..c.IndexOf(')', StringComparison.Ordinal)]).ToArray();
        Assert.Equal(
            [
                "T(\"Write a comment…\"",
                "T(\"Write a reply…\"",
                "T(\"Reply to: %s\", sender",
                "T(\"Reply all to: %s\", sender",
                "T(\"Open in a Window\"",
                "T(\"Forward…\"",
            ],
            calls);
    }

    // TestSingleModel (and Swift singleModel): one message shown alone is a
    // model of one message card, which is the quick reply's target whoever
    // wrote it; an issue's event has none.
    [Fact]
    public void SingleModel()
    {
        var s = Mail("m1", 0, false) with { From = [new Address { Name = "Jana" + Rlo + " Dvořáková\n", Email = "jana@acme.example" }] };
        var m = Conversation.SingleModel(s, false);
        Assert.True(m.Thread == "t1" && m.Items.Count == 1 && m.ScrollTo == 0 && m.MarkRead is null && m.Issue is null && m.Earlier == 0, m.ToString());
        var it = m.Items[0];
        Assert.True(it.Kind == ConversationItemKind.Message && it.Id?.Value == "m1" && !it.Mine && it.Unread && !it.Sent, it.ToString());
        Assert.Equal(Malachi.Core.IssueTrackers.Jira.Clean("Jana" + Rlo + " Dvořáková\n"), it.Sender);
        Assert.DoesNotContain(Rlo, it.Sender, StringComparison.Ordinal);
        Assert.DoesNotContain("\n", it.Sender, StringComparison.Ordinal);
        Assert.Equal("m1", Conversation.QuickReplyTarget(m)?.Id?.Value);

        // The user's own message (Sent): still the target.
        var own = Mail("m2", 5, true) with { From = [Petr] };
        m = Conversation.SingleModel(own, true);
        var target = Conversation.QuickReplyTarget(m);
        Assert.True(target is { Mine: true, Unread: false } && target.Id?.Value == "m2", target?.ToString());
        // IsMine is the rule of ConversationItem.Mine (the account's own address).
        Assert.True(Conversation.IsMine(own, MailAccount));
        Assert.False(Conversation.IsMine(s, MailAccount));
        Assert.False(Conversation.IsMine(own, null));

        // An issue's description is a message; an event is none.
        var desc = IssueMsg("d", 0, true, Item(IssueItemKind.Description));
        Assert.Equal("d", Conversation.QuickReplyTarget(Conversation.SingleModel(desc, false))?.Id?.Value);
        var ev = Conversation.SingleModel(StatusEvent("e", 1, "Open", "Done"), false);
        Assert.True(ev.Items.Count == 1 && ev.Items[0].Kind == ConversationItemKind.Event && !ev.Items[0].Unread && ev.Thread == "tj", ev.ToString());
        Assert.True(Conversation.QuickReplyTarget(ev) is null, "an event is a target");

        // No id: an empty model, no target.
        var empty = Conversation.SingleModel(Mail("", 0, true) with { ThreadId = "t9" }, false);
        Assert.True(empty.Items.Count == 0 && empty.Thread == "t9" && empty.ScrollTo == -1, empty.ToString());
        Assert.True(Conversation.QuickReplyTarget(empty) is null, "an empty model has a target");
        // No thread: the model's thread is "".
        Assert.Equal("", Conversation.SingleModel(Mail("m3", 0, true) with { ThreadId = null }, false).Thread.Value);
    }

    private static byte[] Bytes(string s) => Encoding.UTF8.GetBytes(s);
}
