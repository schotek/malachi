// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/conversation/chat_test.go (TestChatRowsMail,
// TestChatRowsDraftOrder, TestChatRowsJira, TestChatRowsEmptyAndEventsOnly,
// TestBubbleText, TestBubbleTextCapAndParity, TestChatTexts) and of
// macos/Tests/MalachiCoreTests/ConversationChatTests.swift
// (ConversationChatTests; its other suite, ConversationChatBodyTests, is
// ConversationChatBodyTests.cs). The English catalogue: the msgids come
// back verbatim. The fixtures are ConversationTests'; Go's zero item and
// draft of a row are null here. The chat's measures
// (ConversationLayout.ChatGap, ChatBubbleMax) are in ConversationLayoutTests.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Tests.I18n;
using Xunit;
using static Malachi.Core.Tests.Model.ConversationTests;

namespace Malachi.Core.Tests.Model;

public sealed class ConversationChatTests
{
    // Characters the cleaning must drop, as escapes: the source stays free
    // of invisible characters.
    private const string Rlo = "\u202E"; // RIGHT-TO-LEFT OVERRIDE
    private const string Zwsp = "\u200B"; // ZERO WIDTH SPACE

    private static readonly IssueInfo Request = new()
    {
        Key = "ITSD-42",
        Url = "https://acme.atlassian.net/browse/ITSD-42",
        Summary = "Printer on the third floor jams",
        Status = "In Progress",
    };

    // Petr's reply in his Sent folder fs (thread.get's sent).
    private static MessageSummary Reply(string id, int min) =>
        Mail(id, min, false) with { FolderId = "fs", From = [Petr], To = [Jana], Snippet = "Thanks" };

    // chatShape: a row of the chat as "kind:id@side" ("more@center" for the
    // row of older messages, "draft:<id>@right" for a draft).
    private static string[] ChatShape(IEnumerable<ChatRow> rows) =>
        [.. rows.Select(r => (r.Kind switch
        {
            ChatRowKind.Bubble => "msg:" + (r.Item?.Id?.Value ?? ""),
            ChatRowKind.Event => "event:" + (r.Item?.Id?.Value ?? ""),
            ChatRowKind.Truncated => "more",
            _ => "draft:" + (r.Draft?.Draft.Id?.Value ?? ""),
        }) + "@" + (r.Side switch
        {
            ChatSide.Left => "left",
            ChatSide.Right => "right",
            _ => "center",
        }))];

    private static string[] DraftIds(IEnumerable<ConversationDraftCard> cards) =>
        [.. cards.Select(c => c.Draft.Id?.Value ?? "")];

    // A draft card as Go's composite literal makes it: only the draft set.
    private static ConversationDraftCard BareCard(string id, int min) =>
        new() { Draft = new Draft { Id = id, AccountId = "", UpdatedAt = At(min) } };

    [Fact]
    public void ChatRowsMail()
    {
        var own = Mail("m2", 10, true) with { From = [Petr] };
        // The user's reply in Sent from another identity: not the account's
        // address, so not Mine, and still on the right.
        var other = Reply("r1", 15) with { From = [new Address { Name = "Petr", Email = "petr@home.example" }] };
        MessageSummary[] members = [Mail("m3", 20, false), own, Mail("m1", 0, true)];
        MessageSummary[] sent = [Reply("r2", 30), other];
        var m = Conversation.Build(MailThread(5), members, MailAccount, sent);
        Assert.Equal(2, m.Earlier);
        var r1 = m.Items[m.Index("r1")];
        Assert.True(!r1.Mine && r1.Sent, $"the reply from another identity: Mine {r1.Mine}, Sent {r1.Sent}");
        var drafts = Conversation.Drafts(
            [
                new Draft { Id = "d2", AccountId = "", UpdatedAt = At(50), TextBody = "newer" },
                new Draft { Id = "d1", AccountId = "", UpdatedAt = At(40), TextBody = "older" },
                new Draft { Id = "d3", AccountId = "", UpdatedAt = At(60), TextBody = "newest" },
            ], MailAccount);
        var rows = Conversation.ChatRows(m, drafts);
        Assert.Equal(
            [
                "more@center",
                "msg:m1@left", "msg:m2@right", "msg:r1@right", "msg:m3@left", "msg:r2@right",
                "draft:d1@right", "draft:d2@right", "draft:d3@right",
            ],
            ChatShape(rows));
        Assert.Equal("2 earlier messages are not shown", rows[0].Item?.Text);
        Assert.Equal(m.Items[m.Index("m2")], rows[2].Item);
        Assert.Equal("older", rows[6].Draft?.Text);
        Assert.True(rows[7].Item is null && rows[1].Draft is null, "a draft row carries an item, or a bubble a draft");
        var to = Conversation.ChatScrollTo(rows);
        Assert.Equal(rows.Count - 1, to);
        Assert.Equal("d3", rows[to].Draft?.Draft.Id?.Value);

        // Without drafts the chat opens at the newest message.
        var plain = Conversation.ChatRows(m, []);
        var plainTo = Conversation.ChatScrollTo(plain);
        Assert.Equal(plain.Count - 1, plainTo);
        Assert.Equal("r2", plain[plainTo].Item?.Id?.Value);
        Assert.Equal(ChatShape(plain), ChatShape(Conversation.ChatRows(m, null)));
        // The model is not changed.
        Assert.Equal(["more", "msg:m1", "msg:m2", "msg:r1", "msg:m3", "msg:r2"], Shape(m));
    }

    // Drafts saved at the same time keep the order given; the list given is
    // not reordered.
    [Fact]
    public void ChatRowsDraftOrder()
    {
        var m = Conversation.Build(MailThread(1), [Mail("m1", 0, true)], MailAccount);
        ConversationDraftCard[] drafts = [BareCard("c", 20), BareCard("b", 10), BareCard("a", 10), BareCard("z", 5)];
        var given = DraftIds(drafts);
        var rows = Conversation.ChatRows(m, drafts);
        Assert.Equal(["msg:m1@left", "draft:z@right", "draft:b@right", "draft:a@right", "draft:c@right"], ChatShape(rows));
        Assert.Equal(given, DraftIds(drafts));
    }

    [Fact]
    public void ChatRowsJira()
    {
        MessageSummary[] members =
        [
            IssueMsg("d", 0, true, Item(IssueItemKind.Description) with { Mine = true }),
            IssueMsg("c1", 10, true, Item(IssueItemKind.Comment)),
            StatusEvent("e1", 20, "To Do", "In Progress"),
            IssueMsg("c2", 30, true, Item(IssueItemKind.Comment) with { Mine = true }),
            // The site says it is the user's, but an integration posted it.
            IssueMsg("c3", 40, false, Item(IssueItemKind.Comment) with { Mine = true, Via = "Issue Sync" }),
            StatusEvent("e2", 50, "In Progress", "Done"),
        ];
        var m = Conversation.Build(JiraThread(7), members, JiraAccount);
        var drafts = Conversation.Drafts(
            [
                new Draft
                {
                    Id = "k1", AccountId = "j1", UpdatedAt = At(60), TextBody = "On it",
                    Comment = new DraftComment { Issue = Request, Visibility = CommentVisibility.Internal },
                },
            ], JiraAccount);
        var rows = Conversation.ChatRows(m, drafts);
        Assert.Equal(
            [
                "more@center",
                "msg:d@right", "msg:c1@left", "event:e1@center", "msg:c2@right", "msg:c3@left", "event:e2@center",
                "draft:k1@right",
            ],
            ChatShape(rows));
        Assert.True(rows[5].Item is { Via.Length: > 0, Mine: false }, $"the relayed comment: {rows[5].Item}");
        Assert.True(rows[7].Draft is { Internal: true, Comment: true }, $"the comment draft: {rows[7].Draft}");
        Assert.Equal(7, Conversation.ChatScrollTo(rows));
    }

    [Fact]
    public void ChatRowsEmptyAndEventsOnly()
    {
        var empty = Conversation.ChatRows(new ConversationModel { Thread = "t1" }, []);
        Assert.Empty(empty);
        Assert.Equal(-1, Conversation.ChatScrollTo(empty));
        Assert.Equal(-1, Conversation.ChatScrollTo([]));
        Assert.Equal(-1, Conversation.ChatScrollTo(null));
        // An empty model with older members left out (Remove took the last
        // shown one) has no rows either: the pane loads it again.
        var gone = Conversation.Remove(Conversation.Build(MailThread(3), [Mail("m1", 0, true)], MailAccount), "m1");
        Assert.Empty(Conversation.ChatRows(gone, []));
        Assert.Equal(2, gone.Earlier);

        var events = Conversation.Build(
            JiraThread(2),
            [StatusEvent("e2", 10, "In Progress", "Done"), StatusEvent("e1", 0, "To Do", "In Progress")],
            JiraAccount);
        var rows = Conversation.ChatRows(events, []);
        Assert.Equal(["event:e1@center", "event:e2@center"], ChatShape(rows));
        Assert.Equal(1, Conversation.ChatScrollTo(rows));
        Assert.NotEmpty(rows[0].Item!.EventLines);
    }

    // "ok", two bytes that are no UTF-8, "!".
    private static readonly byte[] InvalidUtf8 = [0x6F, 0x6B, 0xFF, 0xFE, 0x21];

    // The text of a bubble is cleaned as a draft card's is; these follow
    // Board.CleanBlock. Go's invalid UTF-8 reaches C# as U+FFFD (the
    // daemon's JSON and the decoder replace it), which goes, as in Swift.
    public static TheoryData<string, string, string> BubbleTextCases => new()
    {
        { "empty", "", "" },
        { "plain", "Hello", "Hello" },
        { "CRLF", "one\r\ntwo\r\n", "one\ntwo" },
        { "lone CR", "one\rtwo", "one\ntwo" },
        { "NEL and line separator", "one\u0085two\u2028three\u2029four", "one\ntwo\nthree\nfour" },
        { "control characters", "a\0b\u0007c\u001Bd\u007Fe", "abcde" },
        { "bidi and invisible", "Hi" + Rlo + " there" + Zwsp, "Hi there" },
        { "blank lines", "a\n\n\n\n\nb", "a\n\nb" },
        { "blank lines with spaces", "a\n \t\n  \n\nb  \n", "a\n\nb" },
        { "only whitespace", " \n\t\r\n   \n\n", "" },
        { "markup stays text", "<b>bold</b> & <script>x</script>", "<b>bold</b> & <script>x</script>" },
        { "invalid UTF-8", Encoding.UTF8.GetString(InvalidUtf8), "ok!" },
    };

    [Theory]
    [MemberData(nameof(BubbleTextCases))]
    public void BubbleText(string name, string input, string want) =>
        Assert.True(Conversation.BubbleText(input) == want, $"{name}: BubbleText = \"{Conversation.BubbleText(input)}\", want \"{want}\"");

    // Windows: no text is "", and a lone surrogate (Go's invalid UTF-8 in
    // a UTF-16 string, kept out of the theory's serialised data) goes.
    [Fact]
    public void BubbleTextOfNothingAndLoneSurrogates()
    {
        Assert.Equal("", Conversation.BubbleText(null));
        Assert.Equal("ok!", Conversation.BubbleText("ok\uDFFF\uD800!"));
    }

    [Fact]
    public void BubbleTextCapAndParity()
    {
        var huge = string.Concat(Enumerable.Repeat("Řádek dlouhé zprávy.\r\n", (1 << 20) / 24 + 1));
        var size = Encoding.UTF8.GetByteCount(huge);
        Assert.True(size >= 1 << 20, $"the input is only {size} bytes");
        var got = Conversation.BubbleText(huge);
        var bytes = Encoding.UTF8.GetByteCount(got);
        Assert.True(bytes > 0 && bytes <= Conversation.DraftTextBytes, $"1 MiB in: {bytes} bytes out");
        Assert.StartsWith("Řádek dlouhé zprávy.\nŘádek", got, StringComparison.Ordinal);
        Assert.DoesNotContain("\uFFFD", got, StringComparison.Ordinal);
        Assert.False(char.IsHighSurrogate(got[^1]), "the cut split a pair");

        // A message bubble and a draft bubble read alike.
        foreach (var s in new[] { huge, "Hi" + Rlo + "\r\n\r\n\r\nBye\0", " \n ", "plain" })
        {
            var card = Conversation.Drafts([new Draft { Id = "d", AccountId = "", UpdatedAt = At(0), TextBody = s }], MailAccount)[0];
            Assert.True(Conversation.BubbleText(s) == card.Text, $"bubble and draft differ for \"{s[..Math.Min(s.Length, 20)]}…\"");
        }
    }

    [Fact]
    public void ChatTexts()
    {
        Assert.Equal("Show as Chat", Conversation.ChatToggleLabel(on: false));
        Assert.Equal("Others on the left, your messages on the right; double-click opens the message", Conversation.ChatToggleTooltip(on: false));
        Assert.Equal("Show as Thread", Conversation.ChatToggleLabel(on: true));
        Assert.Equal("Back to the timeline: the messages as cards, newest first", Conversation.ChatToggleTooltip(on: true));
    }

    // po_test.go TestMsgidsInTemplate for chat.go: the toggle's four msgids
    // are the template's, and the port translates them as such.
    [Fact]
    public void TheTogglesTextsAreTheTemplatesMsgids()
    {
        var pot = PoFile.Parse(File.ReadAllText(RepositoryPo.Pot), "malachi.pot");
        string[] msgids =
        [
            "Show as Chat",
            "Show as Thread",
            "Others on the left, your messages on the right; double-click opens the message",
            "Back to the timeline: the messages as cards, newest first",
        ];
        foreach (var msgid in msgids)
        {
            var entry = pot.Entries.Single(e => e.Msgid == msgid && e.Msgctxt is null);
            Assert.Null(entry.MsgidPlural);
        }
        Assert.Equal(L10n.T("Show as Chat"), Conversation.ChatToggleLabel(on: false));
        Assert.Equal(L10n.T("Show as Thread"), Conversation.ChatToggleLabel(on: true));
        Assert.Equal(L10n.T(msgids[2]), Conversation.ChatToggleTooltip(on: false));
        Assert.Equal(L10n.T(msgids[3]), Conversation.ChatToggleTooltip(on: true));
        // The bubble's error reuses the cards' msgid.
        Assert.Single(pot.Entries, e => e.Msgid == "Loading the message" && e.Msgctxt is null);
    }

    // chat_test.go TestBubbleRendersHTML: the HTML only with the setting, a
    // body with HTML, and that HTML not withheld.
    [Theory]
    [InlineData(false, false, false, false)]
    [InlineData(false, false, true, false)]
    [InlineData(false, true, false, false)]
    [InlineData(false, true, true, false)]
    [InlineData(true, false, false, false)]
    [InlineData(true, false, true, false)]
    [InlineData(true, true, false, true)]
    [InlineData(true, true, true, false)]
    public void BubbleRendersHtml(bool setting, bool hasHtml, bool withheld, bool want) =>
        Assert.Equal(want, Conversation.BubbleRendersHtml(setting, hasHtml, withheld));

    // chat_test.go TestBubbleShortened: the setting of the bubble's side
    // (a draft counts as mine) and not expanded; all 16 rows.
    [Theory]
    [InlineData(false, false, false, false, false)]
    [InlineData(false, false, false, true, false)]
    [InlineData(false, false, true, false, false)]
    [InlineData(false, false, true, true, false)]
    [InlineData(false, true, false, false, true)]
    [InlineData(false, true, false, true, false)]
    [InlineData(false, true, true, false, false)]
    [InlineData(false, true, true, true, false)]
    [InlineData(true, false, false, false, false)]
    [InlineData(true, false, false, true, false)]
    [InlineData(true, false, true, false, true)]
    [InlineData(true, false, true, true, false)]
    [InlineData(true, true, false, false, true)]
    [InlineData(true, true, false, true, false)]
    [InlineData(true, true, true, false, true)]
    [InlineData(true, true, true, true, false)]
    public void BubbleShortened(bool mineOn, bool othersOn, bool mine, bool expanded, bool want) =>
        Assert.Equal(want, Conversation.BubbleShortened(mineOn, othersOn, mine, expanded));

    [Fact]
    public void ExpandedRemembersUntilReset()
    {
        var e = new ConversationExpanded();
        Assert.False(e.Is(new MessageId("m1")));
        Assert.False(e.Is(null));
        e.Expand(new MessageId("m1"));
        e.Expand(new MessageId(""));
        Assert.True(e.Is(new MessageId("m1")));
        Assert.False(e.Is(new MessageId("m2")));
        Assert.False(e.Is(new MessageId("")));
        e.Reset();
        Assert.False(e.Is(new MessageId("m1")));
    }

    [Fact]
    public void ChatTruncateTexts()
    {
        Assert.Equal(3, Conversation.ChatPreviewLines);
        Assert.Equal("Show More", Conversation.ChatShowMore());
        Assert.Equal("Shorten My Messages in Chat", Conversation.ChatTruncateMineRowTitle());
        Assert.Equal("Shorten Others' Messages in Chat", Conversation.ChatTruncateOthersRowTitle());
        Assert.Equal("About three lines of each message in its bubble; a click shows the whole message", Conversation.ChatTruncateRowSubtitle());
        Assert.Equal("Conversations", Conversation.ChatPreferencesGroupTitle());
        var pot = PoFile.Parse(File.ReadAllText(RepositoryPo.Pot), "malachi.pot");
        foreach (var msgid in new[] { "Show More", "Shorten My Messages in Chat", "Shorten Others' Messages in Chat", "Conversations", "About three lines of each message in its bubble; a click shows the whole message" })
        {
            Assert.Single(pot.Entries, entry => entry.Msgid == msgid && entry.Msgctxt is null);
        }
    }
}
