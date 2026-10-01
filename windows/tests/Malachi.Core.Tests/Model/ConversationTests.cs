// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/ConversationTests.swift, the
// counterpart of ui/internal/conversation/conversation_test.go
// (TestIsConversationRow, TestBuild, TestBuildItems,
// TestBuildIssueFallsBackToNewestMember, TestBuildTruncated,
// TestBuildCapsMembers, TestMine, TestSenderIsCleaned, TestMerge,
// TestRemove, TestCardActions) and po_test.go; ConversationSentTests and
// ConversationFoldTests use its fixtures. The process-wide catalogue
// is English here; the Czech plural of the truncated row is read from
// po/cs.po, and the model's one msgid is checked against the template.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using System.Text;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Tests.I18n;
using Malachi.Core.Tests.IssueTrackers;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class ConversationTests
{
    private static readonly DateTimeOffset T0 = new(2026, 9, 1, 9, 0, 0, TimeSpan.Zero);

    internal static DateTimeOffset At(int min) => T0.AddMinutes(min);

    internal static readonly Address Jana = new() { Name = "Jana Dvořáková", Email = "jana@acme.example" };
    internal static readonly Address Petr = new() { Name = "Petr Svoboda", Email = "petr@acme.example" };

    // The accounts of the two conversations: Petr's mailbox (the mail of the
    // tests is Jana's, written to him) and his account on the Jira site.
    internal static readonly Account MailAccount = new()
    {
        Id = "a1",
        Config = new AccountConfig { Name = "Work", Email = "petr@acme.example" },
        Enabled = true,
        State = new SyncState { AccountId = "a1", Status = SyncStatus.Idle },
    };

    internal static readonly Account JiraAccount = new()
    {
        Id = "j1",
        Config = new AccountConfig { Name = "Acme Jira", Email = "petr@acme.example", Kind = AccountKind.Jira },
        Enabled = true,
        State = new SyncState { AccountId = "j1", Status = SyncStatus.Idle },
        Capabilities = [Capability.Comment, Capability.Forward],
    };

    private static Account AccountOf(ThreadSummary t) => t.AccountId == JiraAccount.Id ? JiraAccount : MailAccount;

    // A member of the mail conversation t1 in folder f1.
    internal static MessageSummary Mail(string id, int min, bool seen) => new()
    {
        Id = id,
        AccountId = "a1",
        FolderId = "f1",
        ThreadId = "t1",
        From = [Jana],
        To = [Petr],
        Subject = "Re: Quarterly report",
        Date = At(min),
        Snippet = "See the figures",
        Flags = seen ? [Flag.Seen] : [],
        HasAttachments = false,
        Size = 0,
    };

    private static readonly IssueInfo Issue = new()
    {
        Key = "ITSD-42",
        Url = "https://acme.atlassian.net/browse/ITSD-42",
        Summary = "Printer on the third floor jams",
        Status = "In Progress",
        StatusCategory = IssueStatusCategory.InProgress,
        Type = "Incident",
        Priority = "High",
        Assignee = "Petr Svoboda",
        Reporter = "Jana Dvořáková",
        CommentVisibilities = [CommentVisibility.Public, CommentVisibility.Internal],
    };

    // A member of the issue ITSD-42 (thread tj of account j1); item is the
    // message's part of it, its issue members replaced by Issue's.
    internal static MessageSummary IssueMsg(string id, int min, bool seen, MessageIssue item) => new()
    {
        Id = id,
        AccountId = "j1",
        FolderId = "space-itsd",
        ThreadId = "tj",
        From = [new Address { Name = "Jana Dvořáková", Email = "" }],
        Subject = "ITSD-42: Printer on the third floor jams",
        Date = At(min),
        Snippet = "",
        Flags = seen ? [Flag.Seen] : [],
        HasAttachments = false,
        Size = 0,
        Issue = MessageIssue.Of(Issue, item.Item) with
        {
            Visibility = item.Visibility,
            Changes = item.Changes,
            Via = item.Via,
            Edited = item.Edited,
            Mine = item.Mine,
        },
    };

    internal static MessageIssue Item(string kind) => MessageIssue.Of(Issue, kind);

    internal static MessageSummary StatusEvent(string id, int min, string from, string to) =>
        IssueMsg(id, min, false, Item(IssueItemKind.Event) with { Changes = [new IssueChange { Field = IssueField.Status, From = from, To = to }] });

    internal static ThreadSummary MailThread(int count) => new()
    {
        Id = "t1",
        AccountId = "a1",
        Subject = "Quarterly report",
        MessageCount = count,
        UnreadCount = 0,
        LatestDate = T0,
        Latest = Mail("x", 0, true),
        Snippet = "",
        HasAttachments = false,
    };

    internal static ThreadSummary JiraThread(int count) => MailThread(count) with { Id = "tj", AccountId = "j1", Subject = "", Issue = Issue };

    // shape: an item as "kind:id" ("more" for the truncated row).
    internal static string[] Shape(ConversationModel m) =>
        [.. m.Items.Select(it => it.Kind switch
        {
            ConversationItemKind.Message => "msg:" + it.Id!.Value.Value,
            ConversationItemKind.Event => "event:" + it.Id!.Value.Value,
            _ => "more",
        })];

    [Theory]
    [InlineData(0, false)]
    [InlineData(1, false)]
    [InlineData(2, true)]
    [InlineData(3, true)]
    [InlineData(612, true)]
    public void IsConversationRow(int count, bool want) => Assert.Equal(want, Conversation.IsConversationRow(MailThread(count)));

    // sent_test.go TestIsConversationRowCountsSent.
    [Theory]
    [InlineData(1, 0, false)]
    [InlineData(1, 1, true)]
    [InlineData(0, 2, true)]
    [InlineData(2, 0, true)]
    [InlineData(0, 1, false)]
    [InlineData(1, -5, false)]
    public void IsConversationRowCountsSent(int count, int sent, bool want) =>
        Assert.Equal(want, Conversation.IsConversationRow(MailThread(count) with { SentCount = sent }));

    private static readonly MessageSummary Queued = Mail("m4", 40, false) with { Outbox = new OutboxInfo { State = OutboxState.Queued, Attempts = 0 } };

    private static readonly MessageSummary UnknownEvent =
        IssueMsg("e9", 50, false, Item(IssueItemKind.Event) with { Changes = [new IssueChange { Field = "priority", From = "Low", To = "High" }] });

    public static TheoryData<string, ThreadSummary, MessageSummary[], string[], string, int, string> BuildCases => new()
    {
        { "mail thread, newest unread", MailThread(3), [Mail("m1", 0, true), Mail("m2", 10, false), Mail("m3", 20, false)], ["msg:m1", "msg:m2", "msg:m3"], "m3", 0, "" },
        { "all read", MailThread(2), [Mail("m1", 0, true), Mail("m2", 10, true)], ["msg:m1", "msg:m2"], "", 0, "" },
        {
            "newest read, an older one unread stays unread", MailThread(3),
            [Mail("m1", 0, false), Mail("m2", 10, false), Mail("m3", 20, true)], ["msg:m1", "msg:m2", "msg:m3"], "", 0, ""
        },
        {
            "jira: description, comments, newest an event", JiraThread(4),
            [
                IssueMsg("d", 0, true, Item(IssueItemKind.Description)),
                IssueMsg("c1", 10, false, Item(IssueItemKind.Comment) with { Visibility = CommentVisibility.Internal }),
                IssueMsg("c2", 20, false, Item(IssueItemKind.Comment) with { Visibility = CommentVisibility.Public }),
                StatusEvent("e1", 30, "To Do", "In Progress"),
            ],
            ["msg:d", "msg:c1", "msg:c2", "event:e1"], "c2", 0, "ITSD-42"
        },
        {
            "jira: the newest comment read, events never count", JiraThread(3),
            [
                IssueMsg("c1", 10, false, Item(IssueItemKind.Comment)),
                IssueMsg("c2", 20, true, Item(IssueItemKind.Comment)),
                StatusEvent("e1", 30, "To Do", "Done"),
            ],
            ["msg:c1", "msg:c2", "event:e1"], "", 0, "ITSD-42"
        },
        { "jira: only events", JiraThread(2), [StatusEvent("e1", 0, "", "To Do"), StatusEvent("e2", 10, "To Do", "Done")], ["event:e1", "event:e2"], "", 0, "ITSD-42" },
        {
            "jira: an event of an unknown field is left out", JiraThread(2),
            [IssueMsg("d", 0, false, Item(IssueItemKind.Description)), UnknownEvent], ["msg:d"], "d", 0, "ITSD-42"
        },
        {
            "a queued outbox member is shown but never marked", MailThread(4),
            [Mail("m1", 0, true), Mail("m2", 10, false), Queued], ["msg:m1", "msg:m2", "msg:m4"], "m2", 1, ""
        },
        {
            "duplicates keep the first, empty ids are dropped", MailThread(2),
            [Mail("m1", 0, true), Mail("m2", 10, false), Mail("m1", 30, false), Mail("", 40, false), Mail("m2", 50, true)], ["msg:m1", "msg:m2"], "m2", 0, ""
        },
        {
            "unsorted input, equal dates by id", MailThread(4),
            [Mail("m3", 20, false), Mail("b", 10, true), Mail("m1", 0, true), Mail("a", 10, true)], ["msg:m1", "msg:a", "msg:b", "msg:m3"], "m3", 0, ""
        },
        { "a stale count below the members adds no row", MailThread(1), [Mail("m1", 0, true), Mail("m2", 10, true)], ["msg:m1", "msg:m2"], "", 0, "" },
        { "nothing to show: empty, no older members", JiraThread(3), [UnknownEvent], [], "", 0, "" },
        { "no members: empty, even with an issue", JiraThread(3), [], [], "", 0, "" },
    };

    [Theory]
    [MemberData(nameof(BuildCases))]
    public void Build(string name, ThreadSummary thread, MessageSummary[] members, string[] shape, string markRead, int earlier, string issueKey)
    {
        var m = Conversation.Build(thread, members, AccountOf(thread));
        string[] want = earlier > 0 ? ["more", .. shape] : shape;
        Assert.True(want.SequenceEqual(Shape(m)), $"{name}: items = [{string.Join(", ", Shape(m))}]");
        Assert.Equal(markRead, m.MarkRead?.Value ?? "");
        Assert.Equal(earlier, m.Earlier);
        Assert.Equal(want.Length - 1, m.ScrollTo);
        Assert.Equal(issueKey, m.Issue?.Key ?? "");
        Assert.Equal(thread.Id, m.Thread);
        foreach (var it in m.Items)
        {
            if (it.Kind == ConversationItemKind.Event)
            {
                Assert.False(it.Unread, $"{name}: an event is unread");
            }
            if (it.Kind == ConversationItemKind.Message)
            {
                Assert.Equal(!it.Message!.Flags.Contains(Flag.Seen), it.Unread);
            }
        }
    }

    [Fact]
    public void BuildItems()
    {
        MessageSummary[] members =
        [
            IssueMsg("d", 0, true, Item(IssueItemKind.Description)),
            IssueMsg("c1", 10, false, Item(IssueItemKind.Comment) with { Visibility = CommentVisibility.Internal, Via = "Issue Sync", Edited = true }),
            IssueMsg("e1", 20, false, Item(IssueItemKind.Event) with
            {
                Changes =
                [
                    new IssueChange { Field = IssueField.Status, From = "To Do", To = "In Progress" },
                    new IssueChange { Field = "priority", From = "Low", To = "High" },
                    new IssueChange { Field = IssueField.Assignee, To = "Petr Svoboda" },
                ],
            }),
        ];
        var m = Conversation.Build(JiraThread(3), members, JiraAccount);
        var d = m.Items[0];
        Assert.Equal((ConversationItemKind.Message, members[0], "Jana Dvořáková", false, false, "", "", ""), (d.Kind, d.Message, d.Sender, d.Unread, d.Internal, d.InternalLabel, d.Via, d.Edited));
        var c1 = m.Items[1];
        Assert.Equal((ConversationItemKind.Message, members[1], "Jana Dvořáková", true, true, "Internal", "via Issue Sync", "Edited"), (c1.Kind, c1.Message, c1.Sender, c1.Unread, c1.Internal, c1.InternalLabel, c1.Via, c1.Edited));
        var e1 = m.Items[2];
        Assert.Equal(ConversationItemKind.Event, e1.Kind);
        Assert.Same(members[2], e1.Message);
        Assert.Equal("Jana Dvořáková", e1.Sender);
        Assert.Equal(["Status: To Do → In Progress", "Assignee: Unassigned → Petr Svoboda"], e1.EventLines);
        Assert.Equal("Status: To Do → In Progress; Assignee: Unassigned → Petr Svoboda", e1.EventText);
        Assert.Empty(d.EventLines);
        var c = m.Issue!;
        Assert.Equal(("ITSD-42", "Printer on the third floor jams", "In Progress", "https://acme.atlassian.net/browse/ITSD-42"), (c.Key, c.Summary, c.Status, c.Url));
        Assert.False(c.Internal); // the issue card carries no item's badges
        Assert.Equal("", c.Via);
        Assert.Equal("", c.Edited);
    }

    [Fact]
    public void BuildIssueFallsBackToNewestMember()
    {
        var older = IssueMsg("c1", 0, true, Item(IssueItemKind.Comment));
        var newer = IssueMsg("c2", 10, true, Item(IssueItemKind.Comment));
        newer = newer with { Issue = newer.Issue! with { Status = "Done" } };
        var m = Conversation.Build(JiraThread(2) with { Issue = null }, [newer, older], JiraAccount);
        Assert.Equal("Done", m.Issue?.Status); // the newest member's
        Assert.Null(Conversation.Build(MailThread(2), [Mail("m1", 0, true), Mail("m2", 1, true)], MailAccount).Issue);
    }

    private static string Id(int i) => "m" + i.ToString("D3", CultureInfo.InvariantCulture);

    [Fact]
    public void BuildTruncated()
    {
        var members = Enumerable.Range(0, API.Limits.MaxThreadMessages).Select(i => Mail(Id(i), i, i != API.Limits.MaxThreadMessages - 1)).ToArray();
        var m = Conversation.Build(MailThread(612), members, MailAccount);
        Assert.Equal(501, m.Items.Count);
        Assert.Equal(ConversationItemKind.Truncated, m.Items[0].Kind);
        Assert.Equal("112 earlier messages are not shown", m.Items[0].Text);
        Assert.Null(m.Items[0].Message); // the truncated row carries no message
        Assert.Equal("", m.Items[0].Sender);
        Assert.Equal((112, 500, "m499"), (m.Earlier, m.ScrollTo, m.MarkRead?.Value));
        Assert.Equal(1, m.Index("m000"));
        Assert.Equal(500, m.Index("m499"));
        Assert.Equal(-1, m.Index(""));
        Assert.Equal(-1, m.Index(null));
        Assert.Equal(-1, m.Index("m999"));
        Assert.Equal("1 earlier message is not shown", Conversation.Build(MailThread(3), members[..2], MailAccount).Items[0].Text);
    }

    [Fact]
    public void BuildTruncatedInCzech()
    {
        var cs = Catalogue.Load(RepositoryPo.Directory, ["cs"]);
        Assert.Equal("1 starší zpráva není zobrazena", cs.Plural("%d earlier message is not shown", "%d earlier messages are not shown", 1));
        Assert.Equal("3 starší zprávy nejsou zobrazeny", cs.Plural("%d earlier message is not shown", "%d earlier messages are not shown", 3));
        Assert.Equal("7 starších zpráv není zobrazeno", cs.Plural("%d earlier message is not shown", "%d earlier messages are not shown", 7));
    }

    [Fact]
    public void BuildCapsMembers()
    {
        var n = API.Limits.MaxThreadMessages + 10;
        var m = Conversation.Build(MailThread(n), [.. Enumerable.Range(0, n).Select(i => Mail(Id(i), i, true))], MailAccount);
        Assert.Equal(API.Limits.MaxThreadMessages + 1, m.Items.Count);
        Assert.Equal(10, m.Earlier);
        Assert.Equal("m010", m.Items[1].Id?.Value); // the oldest shown
        Assert.Equal("10 earlier messages are not shown", m.Items[0].Text);
    }

    private static MessageSummary From(string id, int min, params Address[] list) => Mail(id, min, true) with { From = list };

    private static Address Addr(string name, string email) => new() { Name = name, Email = email };

    public static TheoryData<string, bool, MessageSummary, bool> MineCases => new()
    {
        { "mail of the account's address", false, From("m1", 0, Petr), true },
        { "mail of another sender", false, From("m2", 1, Jana), false },
        { "case and surrounding space do not count", false, From("m3", 2, Addr("Petr", " \tPetr@ACME.Example\n")), true },
        { "the address decides, not the name", false, From("m4", 3, Addr("Petr Svoboda", "petr@other.example")), false },
        { "a name that is the address does not count", false, From("m5", 4, Addr("petr@acme.example", "jana@acme.example")), false },
        { "only the first sender counts", false, From("m6", 5, Jana, Petr), false },
        { "the first sender without an address", false, From("m7", 6, Addr("Petr Svoboda", ""), Petr), false },
        { "a longer address that holds the account's", false, From("m8", 7, Addr("", "petr@acme.example.invalid")), false },
        { "an invisible character makes another address", false, From("m9", 8, Addr("", "petr" + JiraTests.Zwsp + "@acme.example")), false },
        { "missing From", false, From("m10", 9), false },
        { "jira: the user's comment", true, IssueMsg("c1", 12, true, Item(IssueItemKind.Comment) with { Mine = true }), true },
        { "jira: the user's description", true, IssueMsg("d", 13, true, Item(IssueItemKind.Description) with { Mine = true }), true },
        { "jira: someone else's comment", true, IssueMsg("c2", 14, true, Item(IssueItemKind.Comment)), false },
        {
            "jira: a comment relayed by an integration is never the user's", true,
            IssueMsg("c3", 15, true, Item(IssueItemKind.Comment) with { Via = "Issue Sync", Mine = true }), false
        },
        { "jira: the site decides, not the sender's address", true, IssueMsg("c4", 16, true, Item(IssueItemKind.Comment)) with { From = [Petr] }, false },
        {
            "jira: a change the user made", true,
            IssueMsg("e1", 17, true, Item(IssueItemKind.Event) with { Changes = [new IssueChange { Field = IssueField.Status, From = "To Do", To = "Done" }], Mine = true }),
            true
        },
    };

    [Theory]
    [MemberData(nameof(MineCases))]
    public void Mine(string name, bool jira, MessageSummary msg, bool want)
    {
        var account = jira ? JiraAccount : MailAccount;
        var other = msg.Issue is not null ? IssueMsg("c0", -10, true, Item(IssueItemKind.Comment)) : Mail("m0", -10, true);
        var thread = MailThread(2) with { Id = msg.ThreadId!.Value, AccountId = account.Id };
        var m = Conversation.Build(thread, [other, msg], account);
        var at = m.Index(msg.Id);
        Assert.True(at >= 0, $"{name}: the member is not shown");
        Assert.True(want == m.Items[at].Mine, $"{name}: Build: Mine");
        Assert.False(m.Items[m.Index(other.Id)].Mine, $"{name}: the other member is the user's");
        // The same member arriving while the conversation is shown.
        var merged = Conversation.Merge(Conversation.Remove(m, msg.Id), msg, account);
        Assert.True(want == merged.Items[merged.Index(msg.Id)].Mine, $"{name}: Merge: Mine");
    }

    [Fact]
    public void MineOfAnAccountWithoutAnAddress()
    {
        var noAddress = MailAccount with { Config = MailAccount.Config with { Email = "  " } };
        foreach (var msg in new[] { From("m11", 10, Addr("", "  ")), From("m12", 11, Petr) })
        {
            var m = Conversation.Build(MailThread(2), [Mail("m0", -10, true), msg], noAddress);
            Assert.False(m.Items[m.Index(msg.Id)].Mine);
        }
        // The truncated row is nobody's.
        var cut = Conversation.Build(MailThread(5), [From("m1", 0, Petr), From("m2", 1, Petr)], MailAccount);
        Assert.Equal(ConversationItemKind.Truncated, cut.Items[0].Kind);
        Assert.False(cut.Items[0].Mine);
        Assert.True(cut.Items[1].Mine);
    }

    [Fact]
    public void SenderIsCleaned()
    {
        var hostile = From("m1", 0, Addr(" " + JiraTests.Zwsp + " ", ""), Addr("Jana" + JiraTests.Rlo + "\nDvořáková" + JiraTests.Zwsp, "jana@acme.example"));
        var longName = From("m2", 1, Addr(new string('ř', 400), ""));
        var bare = From("m3", 2, Addr("", " petr@acme.example "));
        var none = From("m4", 3);
        var m = Conversation.Build(MailThread(4), [hostile, longName, bare, none], MailAccount);
        Assert.Equal("Jana Dvořáková", m.Items[0].Sender);
        var got = m.Items[1].Sender;
        Assert.True(Encoding.UTF8.GetByteCount(got) <= 512 && got.StartsWith("řř", StringComparison.Ordinal) && !got.Contains('\uFFFD', StringComparison.Ordinal), $"long sender: {got.Length}");
        Assert.Equal("petr@acme.example", m.Items[2].Sender);
        Assert.Equal("", m.Items[3].Sender);
    }

    private static ConversationModel MailBase() =>
        Conversation.Build(MailThread(3), [Mail("m1", 0, true), Mail("m2", 10, true), Mail("m3", 20, true)], MailAccount);

    [Fact]
    public void MergeNewestArrival()
    {
        var m = Conversation.Merge(MailBase(), Mail("m4", 30, false), MailAccount);
        Assert.Equal(["msg:m1", "msg:m2", "msg:m3", "msg:m4"], Shape(m));
        Assert.Equal(("m4", 3, 3), (m.MarkRead?.Value, m.ScrollTo, m.Index("m4")));
    }

    [Fact]
    public void MergeOlderArrivalGoesInItsPlace()
    {
        var m = Conversation.Merge(MailBase(), Mail("m2b", 10, false), MailAccount);
        Assert.Equal(["msg:m1", "msg:m2", "msg:m2b", "msg:m3"], Shape(m));
        Assert.Null(m.MarkRead); // the newest is read
    }

    [Fact]
    public void MergeReplacesAShownMember()
    {
        var baseModel = MailBase();
        var unread = Conversation.Merge(baseModel, Mail("m3", 20, false), MailAccount);
        Assert.Equal(Shape(baseModel), Shape(unread));
        Assert.True(unread.Items[2].Unread);
        Assert.Equal("m3", unread.MarkRead?.Value);
        Assert.Equal(["msg:m2", "msg:m3", "msg:m1"], Shape(Conversation.Merge(baseModel, Mail("m1", 25, true), MailAccount)));
        // Merge does not modify the model it was given.
        Assert.Equal(["msg:m1", "msg:m2", "msg:m3"], Shape(baseModel));
        Assert.False(baseModel.Items[2].Unread);
    }

    [Fact]
    public void MergeIgnoresAnotherConversationOrNoId()
    {
        var baseModel = MailBase();
        Assert.Same(baseModel, Conversation.Merge(baseModel, Mail("x1", 30, false) with { ThreadId = "t2" }, MailAccount));
        Assert.Same(baseModel, Conversation.Merge(baseModel, Mail("", 30, false), MailAccount));
        Assert.Equal(3, Conversation.Merge(baseModel, Mail("x2", 30, false) with { ThreadId = null }, MailAccount).Index("x2")); // no thread id is taken
    }

    [Fact]
    public void MergeKeepsTheTruncatedRowOnTop()
    {
        var cut = Conversation.Build(MailThread(5), [Mail("m2", 10, true), Mail("m3", 20, true)], MailAccount);
        var m = Conversation.Merge(cut, Mail("m0", -10, false), MailAccount);
        Assert.Equal(["more", "msg:m0", "msg:m2", "msg:m3"], Shape(m));
        Assert.Equal("3 earlier messages are not shown", m.Items[0].Text);
        Assert.Equal(3, m.Earlier);
    }

    [Fact]
    public void MergeOfAnEventRefreshesTheIssueCard()
    {
        var j = Conversation.Build(JiraThread(2), [IssueMsg("d", 0, true, Item(IssueItemKind.Description)), IssueMsg("c1", 10, false, Item(IssueItemKind.Comment))], JiraAccount);
        var ev = StatusEvent("e1", 20, "In Progress", "Done");
        ev = ev with { Issue = ev.Issue! with { Status = "Done", StatusCategory = IssueStatusCategory.Done } };
        var m = Conversation.Merge(j, ev, JiraAccount);
        Assert.Equal("Done", m.Issue?.Status);
        Assert.Equal("In Progress", j.Issue?.Status);
        Assert.Equal(("c1", 2, ConversationItemKind.Event), (m.MarkRead?.Value, m.ScrollTo, m.Items[2].Kind));
        Assert.Equal("In Progress", Conversation.Merge(j, Mail("m9", 30, true), JiraAccount).Issue?.Status); // a member without an issue keeps the card
    }

    [Fact]
    public void MergeIntoAnEmptyModel()
    {
        var m = Conversation.Merge(Conversation.Build(MailThread(0), [], MailAccount), Mail("m1", 0, false), MailAccount);
        Assert.Equal(["msg:m1"], Shape(m));
        Assert.Equal(("m1", 0), (m.MarkRead?.Value, m.ScrollTo));
    }

    [Fact]
    public void Remove()
    {
        var baseModel = Conversation.Build(MailThread(5), [Mail("m1", 0, false), Mail("m2", 10, false), Mail("m3", 20, false)], MailAccount);
        var m = Conversation.Remove(baseModel, "m3");
        Assert.Equal(["more", "msg:m1", "msg:m2"], Shape(m));
        Assert.Equal(("m2", 2, 2), (m.MarkRead?.Value, m.ScrollTo, m.Earlier));
        Assert.Equal(["more", "msg:m1", "msg:m3"], Shape(Conversation.Remove(baseModel, "m2")));
        Assert.Same(baseModel, Conversation.Remove(baseModel, "nope"));
        Assert.Same(baseModel, Conversation.Remove(baseModel, ""));
        var last = Conversation.Remove(Conversation.Remove(m, "m1"), "m2");
        Assert.Empty(last.Items);
        Assert.Null(last.Issue);
        Assert.Null(last.MarkRead);
        Assert.Equal(-1, last.ScrollTo);
        Assert.Equal((2, "t1"), (last.Earlier, last.Thread.Value));
        Assert.Equal(["more", "msg:m1", "msg:m2", "msg:m3"], Shape(baseModel)); // not modified

        var j = Conversation.Build(JiraThread(2), [IssueMsg("c1", 0, false, Item(IssueItemKind.Comment)), StatusEvent("e1", 10, "To Do", "Done")], JiraAccount);
        var withoutEvent = Conversation.Remove(j, "e1");
        Assert.NotNull(withoutEvent.Issue);
        Assert.Equal(("c1", 0), (withoutEvent.MarkRead?.Value, withoutEvent.ScrollTo));
        var withoutComment = Conversation.Remove(j, "c1");
        Assert.Null(withoutComment.MarkRead);
        Assert.Equal(["event:e1"], Shape(withoutComment));
    }

    public static TheoryData<string, Account, MessageSummary, bool, CapabilityActions> CardCases
    {
        get
        {
            var oldMail = MailAccount with { Id = "a1", Capabilities = null };
            var jiraM1 = JiraAccount with { Capabilities = [] };
            var jiraM2 = JiraAccount;
            var queued = Mail("m2", 10, true) with { Outbox = new OutboxInfo { State = OutboxState.Failed, Attempts = 3 } };
            var comment = IssueMsg("c1", 0, true, Item(IssueItemKind.Comment));
            var ev = StatusEvent("e1", 10, "To Do", "Done");
            return new()
            {
                { "mail", oldMail, Mail("m1", 0, true), false, new() { Reply = true, ReplyAll = true, Forward = true } },
                { "mail with the full list", MailAccount with { Id = "a2", Capabilities = API.MailCapabilities }, Mail("m1", 0, true), true, new() { Reply = true, ReplyAll = true, Forward = true } },
                { "queued mail keeps reply and forward", oldMail, queued, false, new() { Reply = true, ReplyAll = true, Forward = true } },
                { "jira without capabilities", jiraM1, comment, true, new() },
                { "jira comment and forward", jiraM2, comment, true, new() { Reply = true, Forward = true, Comment = true } },
                { "jira forward needs a mail account", jiraM2, comment, false, new() { Reply = true, Comment = true } },
                { "an event offers nothing", jiraM2, ev, true, new() },
            };
        }
    }

    [Theory]
    [MemberData(nameof(CardCases))]
    public void CardActions(string name, Account account, MessageSummary msg, bool compose, CapabilityActions want) =>
        Assert.True(want == Conversation.CardActions(account, msg, compose), $"{name}: {Conversation.CardActions(account, msg, compose)}");

    // po_test.go: the model's one msgid is the template's, with its plural.
    [Fact]
    public void TheTruncatedRowIsTheTemplatesMsgid()
    {
        var pot = PoFile.Parse(System.IO.File.ReadAllText(RepositoryPo.Pot), "malachi.pot");
        var entry = pot.Entries.Single(e => e.Msgid == "%d earlier message is not shown");
        Assert.Equal("%d earlier messages are not shown", entry.MsgidPlural);
        Assert.Null(entry.Msgctxt);
    }
}
