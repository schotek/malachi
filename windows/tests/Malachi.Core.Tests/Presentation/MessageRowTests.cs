// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The tests of MessageRow: ui/internal/widget/message_row.go's SetMessage,
// SetThread, fill and applyLead (the fold arrow, the spinner, the kept
// place of a grouped list's plain row, a member's indent, the densities),
// and the matched words of a search result as UTF-16 ranges.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Text;
using Xunit;
using static Malachi.Core.Tests.Model.ThreadModelTests;

namespace Malachi.Core.Tests.Presentation;

public sealed class MessageRowTests
{
    private static readonly RowAppearance Comfortable = new(Compact: false, ShowPreview: true, ShowAvatars: true, Monochrome: false, Grouped: false);

    private static MessageRow Row(ListRow r, RowMessage? m = null, RowAppearance? look = null)
    {
        var row = new MessageRow(r.Key);
        row.Update(r, m ?? MailModel.SummaryMessage(r.Message), look ?? Comfortable, r.Message.Date);
        return row;
    }

    private static ListRow Plain(MessageSummary s, bool member = false) =>
        new() { Key = new ListKey(Message: s.Id), Message = s, Member = member };

    [Fact]
    public void AMessageRowShowsTheFirstSender()
    {
        var s = Member("m1", null, 1, "Alice") with
        {
            From = [new Address { Name = "Alice", Email = "alice@example.invalid" }, new Address { Email = "bob@example.invalid" }],
            Subject = "  Hello  ",
            HasAttachments = true,
            Flags = [Flag.Flagged],
        };
        var row = Row(Plain(s));
        Assert.Equal("Alice", row.AvatarText);
        Assert.Equal("Alice", row.Sender);
        Assert.Equal("Alice <alice@example.invalid>", row.SenderTooltip);
        Assert.Equal("Hello", row.Subject);
        Assert.Equal("p-m1", row.Preview);
        Assert.True(row.Unread);
        Assert.True(row.Flagged);
        Assert.True(row.HasAttachments);
        Assert.Equal(Format.FormatDate(s.Date, s.Date), row.DateText);
        Assert.False(row.IsThread);
        Assert.Equal("", row.CountText);
        Assert.False(row.ExpanderShown);
        Assert.False(row.Loading);
        Assert.True(row.AvatarVisible);
        Assert.Equal(MessageRow.AvatarComfortable, row.AvatarSize);
        Assert.Equal(MessageRow.MarginStartPlain, row.MarginStart);
        Assert.Equal(MessageRow.MarginComfortable, row.MarginTop);
    }

    [Fact]
    public void AMessageWithoutSenderOrSubject()
    {
        var s = Member("m1", null, 1, "x", Flag.Seen) with { From = [], Subject = " " };
        var row = Row(Plain(s));
        Assert.Equal("", row.Sender);
        Assert.Equal("", row.AvatarText);
        Assert.Equal("(No subject)", row.Subject);
        Assert.False(row.Unread);
    }

    [Fact]
    public void AConversationRowShowsItsParticipantsAndCount()
    {
        var latest = Member("a2", "t_a", 2, "bob");
        var summary = Thr("t_a", 3, 1, latest) with
        {
            Participants = [new Address { Name = "Bob", Email = "bob@example.invalid" }, new Address { Name = "Carol", Email = "carol@example.invalid" }],
        };
        var r = new ListRow { Key = new ListKey(Thread: "t_a"), Thread = true, Message = latest, Summary = summary };
        var row = Row(r);
        Assert.True(row.IsThread);
        Assert.Equal("Bob", row.AvatarText);
        Assert.Equal("Bob, Carol", row.Sender);
        Assert.Equal("Bob <bob@example.invalid>\nCarol <carol@example.invalid>", row.SenderTooltip);
        Assert.Equal("3", row.CountText);
        Assert.Equal("3 messages", row.CountTooltip);
        Assert.True(row.Unread);
        // The live arrow, folded.
        Assert.True(row.ExpanderShown);
        Assert.True(row.ExpanderLive);
        Assert.Equal("pan-end-symbolic", row.ExpanderIcon);
        Assert.Equal("Expand", row.ExpanderTooltip);
        Assert.Equal(MessageRow.MarginStartGrouped, row.MarginStart);

        var open = Row(r with { Expanded = true });
        Assert.Equal("pan-down-symbolic", open.ExpanderIcon);
        Assert.Equal("Collapse", open.ExpanderTooltip);
    }

    [Fact]
    public void ALoadingConversationShowsTheSpinnerInsteadOfTheArrow()
    {
        var latest = Member("a2", "t_a", 2, "bob");
        var r = new ListRow { Key = new ListKey(Thread: "t_a"), Thread = true, Message = latest, Summary = Thr("t_a", 2, 0, latest), Expanded = true, Loading = true };
        var row = Row(r);
        Assert.True(row.Loading);
        Assert.False(row.ExpanderShown);
        Assert.False(row.ExpanderLive);
        Assert.Equal("", row.ExpanderTooltip);
    }

    [Fact]
    public void APlainRowOfAGroupedListKeepsTheArrowsPlace()
    {
        var s = Member("b1", "t_b", 1, "carol");
        var row = Row(new ListRow { Key = new ListKey(Thread: "t_b", Message: s.Id), Message = s }, look: Comfortable with { Grouped = true });
        Assert.True(row.ExpanderShown);
        Assert.False(row.ExpanderLive);
        Assert.Equal("", row.ExpanderTooltip);
        Assert.Equal(MessageRow.MarginStartGrouped, row.MarginStart);
    }

    [Fact]
    public void AMemberRowIsIndentedWithoutAnAvatar()
    {
        var s = Member("a1", "t_a", 1, "alice");
        var r = new ListRow { Key = new ListKey(Thread: "t_a", Message: s.Id), Message = s, Member = true };
        var grouped = Comfortable with { Grouped = true };
        var row = Row(r, look: grouped);
        Assert.True(row.IsMember);
        Assert.False(row.AvatarVisible);
        // Lines up with its conversation's text: the avatar and the gap.
        Assert.Equal(MessageRow.MarginStartGrouped + MessageRow.AvatarComfortable + MessageRow.LeadSpacing, row.MarginStart);
        var compact = Row(r, look: grouped with { Compact = true });
        Assert.Equal(MessageRow.MarginStartGrouped + MessageRow.AvatarCompact + MessageRow.LeadSpacing, compact.MarginStart);
        var noAvatars = Row(r, look: grouped with { ShowAvatars = false });
        Assert.Equal(MessageRow.MarginStartGrouped + MessageRow.MemberIndent, noAvatars.MarginStart);
    }

    [Fact]
    public void TheCompactDensity()
    {
        var s = Member("m1", null, 1, "a");
        var row = Row(Plain(s), look: Comfortable with { Compact = true, ShowPreview = false, ShowAvatars = false, Monochrome = true });
        Assert.Equal(MessageRow.MarginCompact, row.MarginTop);
        Assert.Equal(MessageRow.MarginCompact, row.MarginBottom);
        Assert.Equal(MessageRow.AvatarCompact, row.AvatarSize);
        Assert.False(row.ShowPreview);
        Assert.False(row.AvatarVisible);
        Assert.True(row.Monochrome);
    }

    [Fact]
    public void ASearchResultShowsItsOriginAndMatchedWords()
    {
        var s = Member("m1", null, 1, "a");
        var excerpt = "Výlet na Sněžku o víkendu";
        // "Sněžku" is bytes 10 to 18 of the UTF-8 excerpt (ý and ě ž take two).
        var m = MailModel.SummaryMessage(s) with
        {
            Snippet = excerpt,
            Highlights = [new MatchRange { Start = 10, End = 18 }],
            Origin = "Inbox",
            OriginTooltip = "INBOX — one@example.invalid",
        };
        var row = Row(Plain(s), m);
        Assert.Equal(excerpt, row.Preview);
        Assert.Equal([new Utf16Range(9, 6)], row.Highlights);
        Assert.Equal("Sněžku", excerpt.Substring(9, 6));
        Assert.Equal("Inbox", row.Origin);
        Assert.Equal("INBOX — one@example.invalid", row.OriginTooltip);
    }

    [Fact]
    public void AnUpdateRaisesOnlyWhatChanged()
    {
        var s = Member("m1", null, 1, "a");
        var row = Row(Plain(s));
        var changed = new List<string?>();
        row.PropertyChanged += (_, e) => changed.Add(e.PropertyName);
        row.Update(Plain(s with { Flags = [Flag.Seen] }), MailModel.SummaryMessage(s with { Flags = [Flag.Seen] }), Comfortable, s.Date);
        Assert.Equal(["Unread"], changed);
        changed.Clear();
        row.IsLast = true;
        Assert.Equal(["IsLast"], changed);
    }

    [Fact]
    public void TheAccessibleNameIsTheShownTexts()
    {
        var s = Member("m1", null, 1, "Alice") with { Subject = "Hello", Flags = [Flag.Seen] };
        var row = Row(Plain(s));
        Assert.Equal(string.Join(", ", "Alice", "Hello", row.DateText, "p-m1"), row.ToString());
    }

    [Fact]
    public void TheAccessibleNameSaysWhatTheIconsShow()
    {
        var s = Member("m1", null, 1, "Alice") with { Subject = "Hello", Flags = [Flag.Flagged], HasAttachments = true };
        var row = Row(Plain(s));
        Assert.True(row.Unread);
        Assert.Equal(string.Join(", ", "Alice", "Hello", "Unread", "Flagged", "Attachment", row.DateText, "p-m1"), row.ToString());
        row.Update(Plain(s with { Flags = [Flag.Seen] }), MailModel.SummaryMessage(s with { Flags = [Flag.Seen] }), Comfortable, s.Date);
        Assert.Equal(string.Join(", ", "Alice", "Hello", "Attachment", row.DateText, "p-m1"), row.ToString());
    }
}
