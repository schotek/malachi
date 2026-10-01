// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ConversationTests.swift
// (ConversationSentTests), the counterpart of
// ui/internal/conversation/sent_test.go: the user's replies in Sent that the
// folder lacks (thread.get's sent) as cards among the members. The fixtures
// are ConversationTests'.

using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.ConversationTests;

namespace Malachi.Core.Tests.Model;

public sealed class ConversationSentTests
{
    // Petr's reply in his Sent folder fs (thread.get's sent). Unread on
    // purpose: a sent card is never unread.
    private static MessageSummary Reply(string id, int min) => new()
    {
        Id = id,
        AccountId = "a1",
        FolderId = "fs",
        ThreadId = "t1",
        From = [Petr],
        To = [Jana],
        Subject = "Re: Quarterly report",
        Date = At(min),
        Snippet = "Thanks",
        Flags = [],
        HasAttachments = false,
        Size = 0,
    };

    // Shape with the sent cards marked "sent:".
    private static string[] SentShape(ConversationModel m) =>
        [.. Shape(m).Zip(m.Items, (s, it) => it.Sent ? "sent:" + (it.Id?.Value ?? "") : s)];

    [Fact]
    public void BuildWithSent()
    {
        var thread = MailThread(2) with { SentCount = 2 };
        MessageSummary[] members = [Mail("m2", 20, false), Mail("m1", 0, true)];
        MessageSummary[] sent = [Reply("r2", 30), Reply("r1", 10), Reply("r1", 10), Reply("", 5)];
        var m = Conversation.Build(thread, members, MailAccount, sent);
        Assert.Equal(["msg:m1", "sent:r1", "msg:m2", "sent:r2"], SentShape(m));
        // The newest folder member is marked read, not the newer reply; a
        // sent card is never unread and is the user's own.
        Assert.Equal(("m2", 3, 0), (m.MarkRead?.Value, m.ScrollTo, m.Earlier));
        var r = m.Items[m.Index("r2")];
        Assert.Equal((false, true, "Petr Svoboda", ConversationItemKind.Message, true), (r.Unread, r.Mine, r.Sender, r.Kind, r.Sent));

        // Only replies, all of them unread: the newest member is marked.
        var only = Conversation.Build(MailThread(1), [Mail("m1", 0, false)], MailAccount, [Reply("r1", 10)]);
        Assert.Equal(["msg:m1", "sent:r1"], SentShape(only));
        Assert.Equal("m1", only.MarkRead?.Value);
    }

    [Fact]
    public void BuildWithSentPathological()
    {
        // A sent message with a member's id is the member.
        var dup = Conversation.Build(MailThread(1), [Mail("m1", 0, true)], MailAccount, [Reply("m1", 5)]);
        Assert.Equal(["msg:m1"], SentShape(dup));
        // Sent alone is no conversation of the folder.
        foreach (var (name, members) in new (string, MessageSummary[])[] { ("none", System.Array.Empty<MessageSummary>()), ("no id", new[] { Mail("", 0, true) }) })
        {
            var m = Conversation.Build(MailThread(0), members, MailAccount, [Reply("r1", 0), Reply("r2", 1)]);
            Assert.True(
                m.Items.Count == 0 && m.Earlier == 0 && m.ScrollTo == -1 && m.MarkRead is null && m.Thread.Value == "t1",
                $"{name}: [{string.Join(", ", SentShape(m))}]");
        }
        // A sent event (never from a mail account) is left out.
        var j = Conversation.Build(
            JiraThread(1), [IssueMsg("d", 0, true, Item(IssueItemKind.Description))], JiraAccount, [StatusEvent("e1", 5, "To Do", "Done")]);
        Assert.Equal(["msg:d"], SentShape(j));
        // Equal dates order by id, as members do.
        var same = Conversation.Build(MailThread(1), [Mail("m", 10, true)], MailAccount, [Reply("a", 10), Reply("z", 10)]);
        Assert.Equal(["sent:a", "msg:m", "sent:z"], SentShape(same));
        // Beyond the cap only the newest replies stay.
        var many = Enumerable.Range(0, API.Limits.MaxThreadMessages + 3)
            .Select(i => Reply("r" + i.ToString("D4", System.Globalization.CultureInfo.InvariantCulture), i + 1)).ToArray();
        var capped = Conversation.Build(MailThread(1), [Mail("m", 0, true)], MailAccount, many);
        Assert.Equal(API.Limits.MaxThreadMessages + 1, capped.Items.Count);
        Assert.Equal((-1, 1), (capped.Index("r0002"), capped.Index("r0003")));
    }

    // When older members are left out, a reply older than the oldest member
    // shown is too: it would sit among the members the row says are missing.
    [Fact]
    public void BuildWithSentCut()
    {
        var m = Conversation.Build(MailThread(5), [Mail("m4", 40, true), Mail("m5", 50, true)], MailAccount, [Reply("r1", 10), Reply("r4", 45)]);
        Assert.Equal(["more", "msg:m4", "sent:r4", "msg:m5"], SentShape(m));
        Assert.Equal(3, m.Earlier);
        var merged = Conversation.MergeSent(m, Reply("r0", 5), MailAccount);
        Assert.Equal(["more", "msg:m4", "sent:r4", "msg:m5"], SentShape(merged)); // an older reply merged into a cut conversation
    }

    [Fact]
    public void MergeSent()
    {
        var baseModel = Conversation.Build(MailThread(2), [Mail("m1", 0, true), Mail("m2", 20, false)], MailAccount);
        var m = Conversation.MergeSent(baseModel, Reply("r1", 10), MailAccount);
        Assert.Equal(["msg:m1", "sent:r1", "msg:m2"], SentShape(m));
        Assert.Equal(("m2", 2), (m.MarkRead?.Value, m.ScrollTo));
        Assert.Equal(2, baseModel.Items.Count); // MergeSent changed the model given
        // A changed reply moves to its date; a newer one is last, and still
        // not marked.
        var moved = Reply("r1", 30) with { Flags = [Flag.Flagged] };
        var m2 = Conversation.MergeSent(m, moved, MailAccount);
        Assert.Equal(["msg:m1", "msg:m2", "sent:r1"], SentShape(m2));
        Assert.Equal(("m2", 2), (m2.MarkRead?.Value, m2.ScrollTo));
        Assert.Single(m2.Items[2].Message!.Flags); // the reply was replaced
        // Left alone: no id, another conversation, a member's id, an event.
        var other = Reply("r9", 5) with { ThreadId = "t2" };
        var ev = StatusEvent("e", 5, "A", "B") with { ThreadId = "t1" };
        foreach (var (name, s) in new[] { ("no id", Reply("", 5)), ("other thread", other), ("member id", Reply("m1", 5)), ("event", ev) })
        {
            Assert.True(SentShape(m).SequenceEqual(SentShape(Conversation.MergeSent(m, s, MailAccount))), name);
        }
        // An empty model stays empty.
        var empty = Conversation.Build(MailThread(0), [], MailAccount);
        Assert.Empty(Conversation.MergeSent(empty, Reply("r1", 0), MailAccount).Items);
        // A member arriving with a reply's id takes its place.
        var m3 = Conversation.Merge(m, Mail("r1", 10, false), MailAccount);
        Assert.Equal(["msg:m1", "msg:r1", "msg:m2"], SentShape(m3));
        Assert.False(m3.Items[1].Sent);
    }

    [Fact]
    public void RemoveWithSent()
    {
        var m = Conversation.Build(MailThread(2), [Mail("m1", 0, true), Mail("m2", 20, false)], MailAccount, [Reply("r1", 10), Reply("r2", 30)]);
        var r = Conversation.Remove(m, "r2");
        Assert.Equal(["msg:m1", "sent:r1", "msg:m2"], SentShape(r));
        Assert.Equal((2, "m2"), (r.ScrollTo, r.MarkRead?.Value));
        // The members go, the replies stay behind: no conversation of the
        // folder is left.
        var gone = Conversation.Remove(Conversation.Remove(m, "m1"), "m2");
        Assert.Empty(gone.Items);
        Assert.Equal((-1, (MessageId?)null), (gone.ScrollTo, gone.MarkRead));
        var cut = Conversation.Build(MailThread(4), [Mail("m3", 20, true)], MailAccount, [Reply("r3", 30)]);
        var cutGone = Conversation.Remove(cut, "m3");
        Assert.Empty(cutGone.Items);
        Assert.Equal(3, cutGone.Earlier);
    }
}
