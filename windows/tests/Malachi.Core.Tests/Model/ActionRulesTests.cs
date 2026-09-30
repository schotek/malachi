// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraActionRulesTests.swift, the
// counterpart of ui/internal/window/action_rules_test.go
// (TestActionRulesMailAccountsOfferEverything,
// TestActionRulesJiraWithoutCapabilities, TestActionRulesJiraThatComments,
// TestActionRulesForwardNeedsAnAccountThatComposes,
// TestActionRulesNothingSelectedFollowsTheListedFolder,
// TestActionRulesTheRowsAccountDecidesInASearch,
// TestActionRulesAQueuedJiraCommentCanBeCancelled,
// TestActionRulesChangeStatus).
//
// The per-message actions of a Jira account (ActionRules.MessageActionState
// over Capabilities): what the account does not offer is off and
// Unsupported, Reply of a commenting account is Comment, Forward needs an
// account to send from, and with nothing selected the listed folder's
// account decides. Go's supported set is the complement of Unsupported
// plus Comment.

using System;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;

namespace Malachi.Core.Tests.Model;

public sealed class ActionRulesTests
{
    private static Account Acc(string id, string kind, Capability[]? capabilities, bool enabled = true) => new()
    {
        Id = id,
        Config = new AccountConfig { Name = "", Email = "jana@acme.example", Kind = kind.Length == 0 ? null : (AccountKind?)new AccountKind(kind) },
        Enabled = enabled,
        State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
        Capabilities = capabilities,
    };

    // A mail account of a daemon from before capabilities.
    private static readonly Account OldMail = Acc("a", "", null);
    private static readonly Account Mail = Acc("m", "", [.. API.MailCapabilities]);

    // Reads and flags only.
    private static readonly Account JiraReadOnly = Acc("j", AccountKind.Jira, []);

    // Comments and forwards into a mail account.
    private static readonly Account JiraM2 = Acc("j", AccountKind.Jira, [Capability.Comment, Capability.Forward]);

    // Every capability-bound action off (Go's everythingJiraLacks).
    private const MessageActionKind EverythingJiraLacks =
        MessageActionKind.Reply | MessageActionKind.ReplyAll | MessageActionKind.Forward | MessageActionKind.Trash
        | MessageActionKind.Move | MessageActionKind.Archive | MessageActionKind.Junk;

    // Go's Actions{Reply, Forward, Comment}: all but these are unsupported.
    private const MessageActionKind CommentAndForward =
        MessageActionKind.ReplyAll | MessageActionKind.Trash | MessageActionKind.Move | MessageActionKind.Archive | MessageActionKind.Junk;

    private static readonly IssueInfo Issue = new() { Key = "ITSD-42", Url = "", Summary = "", Status = "" };

    private static Folder F(Account a, string id, string path, FolderRole role, int total = 0) => new()
    {
        Id = id,
        AccountId = a.Id,
        Name = path,
        Path = path,
        Role = role,
        Subscribed = true,
        Selectable = true,
        Synced = true,
        Unread = 0,
        Total = total,
    };

    private static MessageSummary Summary(string id, AccountId account, params Flag[] flags) => new()
    {
        Id = id,
        AccountId = account,
        FolderId = "in",
        Subject = "",
        Date = DateTimeOffset.UnixEpoch,
        Snippet = "",
        Flags = flags,
        HasAttachments = false,
        Size = 0,
    };

    // ruleModel: lists folder "in" of account acc with one message "1" in it;
    // every account's folders include Archive, Junk and Outbox so that only
    // the capabilities can switch those off.
    private static MailModel Model(Account[] accounts, AccountId acc, params Flag[] flags)
    {
        var m = new MailModel(accounts);
        foreach (var a in accounts)
        {
            m.Folders[a.Id] =
            [
                F(a, "in", "INBOX", FolderRole.Inbox),
                F(a, "arch", "Archive", FolderRole.Archive),
                F(a, "junk", "Junk", FolderRole.Junk),
                F(a, "out", "Outbox", FolderRole.Outbox, total: 1),
            ];
        }
        m.ListFolder = new FolderKey(acc, "in");
        m.SetMessages([Summary("1", acc, flags)], new PageInfo { Total = 1 });
        return m;
    }

    // The model's only message replaced by change (Go changes it in place).
    private static void Change(MailModel m, Func<MessageSummary, MessageSummary> change) =>
        m.SetMessages([change(m.Messages[0])], new PageInfo { Total = 1 });

    private static ListRow FirstRow(MailModel m) => new() { Key = new ListKey(Message: m.Messages[0].Id), Message = m.Messages[0] };

    private static ActionFlags State(MailModel m, ListRow? row, bool on = true) => ActionRules.MessageActionState(row, m, on);

    [Fact]
    public void MailAccountsOfferEverything()
    {
        foreach (var acc in new[] { OldMail, Mail })
        {
            var m = Model([acc], acc.Id, Flag.Seen);
            var st = State(m, FirstRow(m));
            Assert.True(st.Reply && st.ReplyAll && st.Forward && st.Trash && st.Archive && st.Junk, $"{acc.Id}: {st}");
            Assert.True(st.Unsupported == MessageActionKind.None && !st.Comment, $"{acc.Id}: {st}");
            var idle = State(m, null, on: false);
            Assert.True(!idle.On && idle.Unsupported == MessageActionKind.None && !idle.Comment, $"{acc.Id}: nothing selected in a mail folder: {idle}");
        }
    }

    [Fact]
    public void JiraWithoutCapabilities()
    {
        var m = Model([Mail, JiraReadOnly], "j");
        var st = State(m, FirstRow(m));
        Assert.True(st.On);
        Assert.False(st.Reply || st.ReplyAll || st.Forward || st.Trash || st.Archive || st.Junk, $"an action the account lacks is on: {st}");
        Assert.Equal(EverythingJiraLacks, st.Unsupported);
        Assert.False(st.Comment);
        // Flags are local and always allowed.
        Assert.True(st.Star && st.ToggleFlag && st.MarkRead && !st.MarkUnread && st.LoadImages && st.TrustSender, $"flags {st}");
        Assert.False(st.ChangeStatus, "Change Status without the capability (and without an issue)");
    }

    [Fact]
    public void JiraThatComments()
    {
        var m = Model([Mail, JiraM2], "j", Flag.Seen);
        var st = State(m, FirstRow(m));
        Assert.True(st.Reply && st.Comment, "Reply writes a comment");
        Assert.True(st.Forward, "forwarded from the mail account");
        Assert.False(st.ReplyAll || st.Trash || st.Archive || st.Junk, st.ToString());
        Assert.Equal(CommentAndForward, st.Unsupported);
        Assert.True(st.MarkUnread && !st.MarkRead, $"seen flags {st}");
    }

    [Fact]
    public void ForwardNeedsAnAccountThatComposes()
    {
        // Only the Jira account: nothing to forward from.
        var m = Model([JiraM2], "j");
        var st = State(m, FirstRow(m));
        Assert.True(!st.Forward && st.Unsupported.HasFlag(MessageActionKind.Forward) && st.Reply && st.Comment, $"only Jira: {st}");
        // A paused mail account does not count.
        m = Model([Mail with { Enabled = false }, JiraM2], "j");
        st = State(m, FirstRow(m));
        Assert.True(!st.Forward && st.Unsupported.HasFlag(MessageActionKind.Forward), $"paused mail: {st}");
        // An enabled one does, a mail account of an old daemon too.
        m = Model([OldMail, JiraM2], "j");
        st = State(m, FirstRow(m));
        Assert.True(st.Forward && !st.Unsupported.HasFlag(MessageActionKind.Forward), $"old mail: {st}");
    }

    [Fact]
    public void NothingSelectedFollowsTheListedFolder()
    {
        var m = Model([Mail, JiraReadOnly], "j");
        var st = State(m, null, on: false);
        Assert.True(!st.On && !st.Reply && !st.Trash && st.Unsupported == EverythingJiraLacks, $"the toolbar of a Jira folder has no Reply: {st}");
        Assert.Equal(EverythingJiraLacks, State(m, FirstRow(m), on: false).Unsupported);

        m = Model([Mail, JiraM2], "j");
        st = State(m, null, on: false);
        Assert.True(st.Comment, "Reply is not labelled Comment before a row is chosen");
        Assert.Equal(CommentAndForward, st.Unsupported);
        // The Jira account's outbox: Trash cancels a queued comment.
        m.ListFolder = new FolderKey("j", "out");
        Assert.False(State(m, null, on: false).Unsupported.HasFlag(MessageActionKind.Trash), "Trash does not cancel a queued comment");
        // A mail folder, or none (a search over every account).
        m.ListFolder = new FolderKey("m", "in");
        st = State(m, null, on: false);
        Assert.True(st.Unsupported == MessageActionKind.None && !st.Comment, $"mail folder: {st}");
        m.ListFolder = null;
        st = State(m, null, on: false);
        Assert.True(st.Unsupported == MessageActionKind.None && !st.Comment, $"no folder: {st}");
    }

    [Fact]
    public void TheRowsAccountDecidesInASearch()
    {
        // A search result of the Jira account while nothing is listed.
        var m = Model([Mail, JiraReadOnly], "j");
        m.ListFolder = null;
        Assert.Equal(EverythingJiraLacks, State(m, FirstRow(m)).Unsupported);
        // An account the window does not know (a message window outliving it)
        // has the mail default.
        var s = Summary("2", "gone", Flag.Seen);
        var gone = State(m, new ListRow { Key = new ListKey(Message: s.Id), Message = s });
        Assert.True(gone.Reply && gone.Trash && !gone.Unsupported.HasFlag(MessageActionKind.Trash), $"unknown account: {gone}");
    }

    [Fact]
    public void AQueuedJiraCommentCanBeCancelled()
    {
        var m = Model([Mail, JiraM2], "j");
        Change(m, s => s with { FolderId = "out", Outbox = new OutboxInfo { State = OutboxState.Queued, Attempts = 0 } });
        var st = State(m, FirstRow(m));
        Assert.True(st.Outbox && st.Trash && !st.Unsupported.HasFlag(MessageActionKind.Trash), $"Trash cancels the send: {st}");
        Assert.False(st.Star || st.Archive || st.Junk, st.ToString());
    }

    [Fact]
    public void ChangeStatus()
    {
        var jira = Acc("j", AccountKind.Jira, [Capability.Comment, Capability.Transition]);
        var m = Model([Mail, jira], "j");
        Assert.False(State(m, FirstRow(m)).ChangeStatus, "Change Status for a message without an issue");
        Change(m, s => s with { Issue = MessageIssue.Of(Issue, IssueItemKind.Comment) });
        Assert.True(State(m, FirstRow(m)).ChangeStatus, "no Change Status on an issue of an account that changes statuses");
        Assert.False(State(m, FirstRow(m), on: false).ChangeStatus, "Change Status with nothing selected");
        m = Model([Mail, JiraM2], "j");
        Change(m, s => s with { Issue = MessageIssue.Of(Issue, IssueItemKind.Comment) });
        Assert.False(State(m, FirstRow(m)).ChangeStatus, "Change Status without the capability");
    }
}
