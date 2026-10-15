// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/conversation/drafts_test.go (TestIsConversationRowCountsDrafts,
// TestDrafts, TestDraftsTextPathological, TestSameDrafts, TestDraftsFirst,
// TestDraftTexts); macOS: ConversationDraftsTests.swift.
// Added: the Draft badge of a conversation row and what thread.list tells
// of a conversation with drafts (thread_model.go threadDraftBadge,
// membersFromListing, sameShape), and the timeline through the draft cards
// (conversation_layout.go convWithDrafts). The fixtures are
// ConversationTests'.

using System.Collections.Generic;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.ConversationTests;

namespace Malachi.Core.Tests.Model;

public sealed class ConversationDraftsTests
{
    private static readonly IssueInfo Request = new()
    {
        Key = "ITSD-42",
        Url = "https://acme.atlassian.net/browse/ITSD-42",
        Summary = "Printer on the third floor jams",
        Status = "In Progress",
    };

    // A draft of Petr's mailbox replying to m1, saved at min.
    private static Draft MailDraft(string id, int min, string text) =>
        new() { Id = id, AccountId = "a1", Version = 1, UpdatedAt = At(min), TextBody = text, InReplyTo = "m1" };

    private static string[] Ids(IEnumerable<ConversationDraftCard> cards) =>
        [.. cards.Select(c => c.Draft.Id?.Value ?? "")];

    [Theory]
    [InlineData(1, 0, 0, false)]
    [InlineData(1, 0, 1, true)]
    [InlineData(1, 1, 1, true)]
    [InlineData(0, 0, 2, true)]
    [InlineData(1, 0, -3, false)]
    [InlineData(2, 0, 0, true)]
    public void IsConversationRowCountsDrafts(int count, int sent, int drafts, bool want) =>
        Assert.Equal(want, Conversation.IsConversationRow(MailThread(count) with { SentCount = sent, DraftCount = drafts }));

    [Fact]
    public void Drafts()
    {
        Draft[] input =
        [
            MailDraft("d1", 10, "first"),
            MailDraft("d3", 30, "third"),
            MailDraft("d2", 30, "second"), // a tie: the greater id first
            MailDraft("d1", 40, "repeated"),
            new Draft { AccountId = "a1", UpdatedAt = At(50) },
            new Draft { Id = "dx", AccountId = "a2", UpdatedAt = At(60) }, // another account's
        ];
        var got = Conversation.Drafts(input, MailAccount);
        Assert.Equal(["d3", "d2", "d1"], Ids(got));
        Assert.True(
            got[2].Text == "first" && got[0].Sender == "petr@acme.example" && !got[0].Comment && !got[0].Internal,
            $"card {got[2]}");
        var named = MailAccount with { Config = MailAccount.Config with { DisplayName = "  Petr‮ Svoboda\n" } };
        Assert.Equal("Petr Svoboda", Conversation.Drafts(input[..1], named)[0].Sender);

        // A comment draft: internal shows the badge, public none.
        Draft[] comments =
        [
            new() { Id = "c1", AccountId = "j1", UpdatedAt = At(1), Comment = new DraftComment { Issue = Request, Visibility = CommentVisibility.Internal } },
            new() { Id = "c2", AccountId = "j1", UpdatedAt = At(0), Comment = new DraftComment { Issue = Request, Visibility = CommentVisibility.Public } },
        ];
        var cc = Conversation.Drafts(comments, JiraAccount);
        Assert.Equal(2, cc.Count);
        Assert.True(cc[0].Comment && cc[0].Internal && cc[0].InternalLabel == "Internal", $"internal {cc[0]}");
        Assert.True(cc[1].Comment && !cc[1].Internal && cc[1].InternalLabel.Length == 0, $"public {cc[1]}");

        // The cap: the newest API.Limits.MaxThreadDrafts.
        var many = Enumerable.Range(0, API.Limits.MaxThreadDrafts + 5).Select(i => MailDraft($"d{i:D2}", i, "x")).ToArray();
        var capped = Conversation.Drafts(many, MailAccount);
        Assert.Equal(API.Limits.MaxThreadDrafts, capped.Count);
        Assert.Equal($"d{API.Limits.MaxThreadDrafts + 4:D2}", capped[0].Draft.Id?.Value);
        Assert.Empty(Conversation.Drafts(null, MailAccount));
        Assert.Empty(Conversation.Drafts([], MailAccount));
    }

    // The text is plain and cleaned: control and format characters go, line
    // breaks stay, at most DraftTextBytes.
    [Fact]
    public void DraftsTextPathological()
    {
        static Draft Text(string text) => new() { Id = "d", AccountId = "a1", UpdatedAt = At(0), TextBody = text };
        const string text = "Hi‮ there\0\r\n\r\n\r\n\r\nBye ​<b>not markup</b>\u2028end";
        Assert.Equal("Hi there\n\nBye <b>not markup</b>\nend", Conversation.Drafts([Text(text)], MailAccount)[0].Text);
        var longText = Conversation.Drafts([Text(new string('é', Conversation.DraftTextBytes))], MailAccount)[0].Text;
        var bytes = Encoding.UTF8.GetByteCount(longText);
        Assert.True(bytes > 0 && bytes <= Conversation.DraftTextBytes, $"long text: {bytes} bytes");
        // A lone surrogate is Go's invalid UTF-8: dropped.
        Assert.Equal("ok", Conversation.Drafts([Text("ok\ud800")], MailAccount)[0].Text);
    }

    [Fact]
    public void SameDrafts()
    {
        var a = Conversation.Drafts([new Draft { Id = "d1", AccountId = "a1", Version = 1, UpdatedAt = At(0), TextBody = "x" }], MailAccount);
        var b = Conversation.Drafts([new Draft { Id = "d1", AccountId = "a1", Version = 1, UpdatedAt = At(0), TextBody = "x" }], MailAccount);
        Assert.True(Conversation.SameDrafts(a, b) && Conversation.SameDrafts(null, null) && Conversation.SameDrafts([], null), "the same drafts differ");
        var c = Conversation.Drafts([new Draft { Id = "d1", AccountId = "a1", Version = 2, UpdatedAt = At(1), TextBody = "y" }], MailAccount);
        Assert.False(Conversation.SameDrafts(a, c) || Conversation.SameDrafts(a, null), "other drafts are the same");
    }

    [Fact]
    public void DraftsFirst() => Assert.True(Conversation.DraftsFirst(0) == 1 && Conversation.DraftsFirst(-1) == 0);

    [Fact]
    public void DraftTexts()
    {
        string[] texts =
        [
            Conversation.DraftBadge(), Conversation.DraftEditLabel(), Conversation.DraftDiscardLabel(),
            Conversation.DiscardQuestion(), Conversation.DraftSaved("10:00"), Conversation.DraftGone(),
        ];
        Assert.Equal(["Draft", "Edit", "_Discard", "Discard this message?", "Draft saved 10:00", "The draft is no longer there"], texts);
    }

    // thread_model.go: a conversation with a draft shows the Draft badge on
    // its row, is not complete before thread.get answered (only it returns
    // the drafts), and a change of its draft count drops the fetched members.
    [Fact]
    public void ARowWithDraftsWaitsForThreadGet()
    {
        var plain = MailThread(1);
        var withDraft = plain with { DraftCount = 2 };
        Assert.Equal("", MailModel.ThreadDraftBadge(plain));
        Assert.Equal("Draft", MailModel.ThreadDraftBadge(withDraft));
        Assert.Equal("Draft", MailModel.SummaryThread(withDraft, false, false).Draft);
        Assert.Equal("", MailModel.SummaryThread(plain with { DraftCount = -1 }, false, false).Draft);
        Assert.True(MailModel.MembersFromListing(plain).Complete);
        Assert.False(MailModel.MembersFromListing(withDraft).Complete);
        Assert.True(MailModel.SameShape(plain, plain with { }));
        Assert.False(MailModel.SameShape(plain, withDraft));
    }

    // conversation_layout.go convWithDrafts: the draft cards are the user's
    // own message cards in the timeline, put where the pane shows them.
    [Fact]
    public void TheTimelineRunsThroughTheDrafts()
    {
        var m = Conversation.Build(MailThread(2), [Mail("m1", 0, true), Mail("m2", 10, true)], MailAccount);
        var display = ConversationLayout.DisplayOrder(m.Items);
        var at = Conversation.DraftsFirst(display.Root);
        Assert.Equal(1, at); // right under the card that opened the conversation
        var items = ConversationLayout.WithDrafts(display.Items, at, 2);
        Assert.Equal(4, items.Count);
        Assert.Equal(["m1", "", "", "m2"], items.Select(it => it.Message?.Id.Value ?? ""));
        Assert.True(items[1].Mine && items[2].Mine && items[1].Kind == ConversationItemKind.Message);
        var rails = ConversationLayout.Rails(items);
        Assert.True(rails[1].Accent && rails[2].Accent && rails[1].Above && rails[2].Below);
        // Clamped to the items; none adds nothing.
        Assert.Equal(3, ConversationLayout.WithDrafts(display.Items, 9, 1).Count);
        Assert.Equal(2, ConversationLayout.WithDrafts(display.Items, -4, 0).Count);
        // Nothing opened it on show: the drafts come first.
        Assert.Equal(0, Conversation.DraftsFirst(ConversationLayout.DisplayOrder([]).Root));
    }
}
