// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraActionsTests.swift, the
// counterpart of ui/internal/window/jira_actions_test.go: the message
// actions on a Jira account that comments and forwards (capabilities
// ["comment", "forward"]) over MailFixture. Reply writes a comment
// (draft.create reply on the Jira account, never the mail prefill), Forward
// is written in a mail account (draft.create with messageAccountId), the
// compose window's From lists only accounts that write mail, New Message
// needs one, and a queued comment in the Jira outbox can be cancelled and
// retried like a message.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Tests.Api;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;
using static Malachi.Core.Tests.Model.MailModelTests;

namespace Malachi.Core.Tests.Controllers;

public sealed class JiraActionsTests
{
    private static readonly AccountId MailA = "a";
    private static readonly AccountId MailB = "b";
    private static readonly AccountId Jira = "j";
    private static readonly FolderKey InboxA = new("a", "in");
    private static readonly FolderKey InboxB = new("b", "binb");
    private static readonly FolderKey Web = new("j", "web");
    private static readonly FolderKey JiraOutbox = new("j", "out");

    // 2026-09-01T10:00:00Z.
    private static readonly DateTimeOffset Base = DateTimeOffset.FromUnixTimeSeconds(1_788_256_800);

    private static readonly IssueInfo WebIssue = new()
    {
        Key = "WEB-1",
        Url = "https://acme.atlassian.net/browse/WEB-1",
        Summary = "Logo on the front page",
        Status = "To Do",
        StatusCategory = IssueStatusCategory.Todo,
    };

    [Fact]
    public async Task CommentAsksDraftCreateReplyOnTheJiraAccount()
    {
        await using var j = await Harness.StartAsync();
        var h = j.H;
        await j.ShowIssueAsync();
        var flags = await h.Ui.RunAsync(() => h.Actions.FlagsFor(h.Summary("w1c")));
        Assert.True(flags.Reply && flags.Comment && flags.Forward && !flags.ReplyAll && !flags.Trash);

        var comment = new DraftComment { Issue = WebIssue };
        var template = new Draft { AccountId = Jira, Subject = "WEB-1: Logo on the front page", InReplyTo = "w1c", Comment = comment };
        j.OnDraftCreate(new DraftCreateResult { Draft = template, Quoted = QuoteForm.None });
        await h.Run(() => h.Actions.OpenCompose(ComposeKind.Reply, "w1c"));
        await h.IdleAsync();
        var asked = Assert.Single(j.Drafts);
        Assert.Equal((Jira, ComposeMode.Reply, new MessageId("w1c"), (AccountId?)null), (asked.AccountId, asked.Mode, asked.MessageId!.Value, asked.MessageAccountId));
        Assert.Null(asked.Attribution);
        var p = Assert.Single(h.Log.Composed);
        Assert.Equal(ComposeKind.Reply, p.Kind);
        Assert.Equal(Jira, p.AccountId); // pinned to the Jira account
        ApiJson.AssertSameValue(comment, p.Comment);
        Assert.Equal("w1c", p.InReplyTo?.Value);
        Assert.Empty(p.To);
        Assert.Empty(h.Log.Toasts);
        Assert.Equal(0, h.Fixture.CallCount(API.MessageDownload.Name)); // nothing is quoted, nothing downloaded
    }

    // compose_open.go openComposeFrom: the comment goes before the download
    // a reply makes for pictures kept on the server.
    [Fact]
    public async Task ACommentDownloadsNoPictures()
    {
        await using var j = await Harness.StartAsync();
        var h = j.H;
        await j.ShowIssueAsync();
        h.Fixture.SetBody("w1c", new MessageBodyResult
        {
            MessageId = "w1c",
            BodyState = BodyState.Fetched,
            HasHtml = true,
            Html = "<p><img src=\"malachi-cid:j/w1c/2\"></p>",
            Text = "",
            Blocked = new BlockedContent { RemoteImages = 0 },
            RemoteContent = RemoteContentPolicy.Block,
            SanitizerVersion = "1",
            RemotePictures = 1,
        });
        var s = h.Summary("w1c");
        await h.Run(() => h.Cache.FetchBody(s, _ => { }));
        await h.IdleAsync();
        Assert.True(ComposeSources.ReplyNeedsDownload(h.Cache.Loaded("w1c")));

        j.OnDraftCreate(new DraftCreateResult { Draft = new Draft { AccountId = Jira, InReplyTo = "w1c", Comment = new DraftComment { Issue = WebIssue } }, Quoted = QuoteForm.None });
        await h.Run(() => h.Actions.OpenCompose(ComposeKind.Reply, "w1c"));
        await h.IdleAsync();
        Assert.Single(h.Log.Composed);
        Assert.Equal(0, h.Fixture.CallCount(API.MessageDownload.Name));
        Assert.Empty(j.Downloads);
    }

    [Fact]
    public async Task AFailedCommentOnlyToasts()
    {
        await using var j = await Harness.StartAsync();
        var h = j.H;
        await j.ShowIssueAsync();

        j.OnDraftCreate(error: DaemonHarness.Daemon(ErrorCode.ServerError, "500"));
        await h.Run(() => h.Actions.OpenCompose(ComposeKind.Reply, "w1c"));
        await h.IdleAsync();
        Assert.Equal(["Preparing the reply failed: the server returned an error"], h.Log.Toasts);
        // Where a mail reply falls back to the pane's own quote silently, a
        // comment has nothing to fall back on: said, and nothing opens.
        j.OnDraftCreate(error: DaemonHarness.Daemon(ErrorCode.NotImplemented, "no"));
        await h.Run(() => h.Actions.OpenCompose(ComposeKind.Reply, "w1c"));
        await h.IdleAsync();
        Assert.Equal("Preparing the reply is not available yet", h.Log.Toasts[^1]);
        Assert.Empty(h.Log.Composed); // never the mail prefill
        Assert.Equal(2, j.Drafts.Count);
    }

    [Fact]
    public async Task ForwardIsWrittenInAMailAccount()
    {
        await using var j = await Harness.StartAsync();
        var h = j.H;
        await j.ShowIssueAsync();
        var forward = new Draft { AccountId = MailA, Subject = "Fwd: WEB-1: Logo on the front page", TextBody = "q", HtmlBody = "<p>q</p>", Forwarding = "w1c" };
        j.OnDraftCreate(new DraftCreateResult { Draft = forward, Quoted = QuoteForm.Html });

        // Browsing the Jira account: the first account that writes mail.
        await h.Run(() => h.Actions.OpenCompose(ComposeKind.Forward, "w1c"));
        await h.IdleAsync();
        var req = Assert.Single(j.Drafts);
        Assert.Equal((MailA, ComposeMode.Forward, "w1c", (AccountId?)Jira), (req.AccountId, req.Mode, req.MessageId?.Value, req.MessageAccountId));
        Assert.StartsWith("---------- Forwarded message ----------", req.Attribution, StringComparison.Ordinal);
        var download = Assert.Single(j.Downloads); // the Jira message is downloaded from its own account
        Assert.Equal((Jira, "w1c"), (download.AccountId, download.MessageId.Value));
        var p = Assert.Single(h.Log.Composed);
        Assert.Equal((ComposeKind.Forward, MailA, "w1c"), (p.Kind, p.AccountId, p.Forwarding?.Value));
        Assert.Null(p.Comment);

        // A mail folder selected (an issue a search found from there): that
        // account.
        await h.Run(() =>
        {
            h.Mailbox.Model.Selected = InboxB;
            h.Actions.OpenCompose(ComposeKind.Forward, "w1c");
        });
        await h.IdleAsync();
        Assert.Equal((MailB, (AccountId?)Jira), (j.Drafts[^1].AccountId, j.Drafts[^1].MessageAccountId));
        Assert.Equal(MailB, h.Log.Composed[1].AccountId);

        // The fallback without draft.create is written there too.
        j.OnDraftCreate(error: DaemonHarness.Daemon(ErrorCode.NotImplemented, "no"));
        await h.Run(() => h.Actions.OpenCompose(ComposeKind.Forward, "w1c"));
        await h.IdleAsync();
        Assert.Equal(3, h.Log.Composed.Count);
        Assert.Equal((MailB, "w1c"), (h.Log.Composed[2].AccountId, h.Log.Composed[2].Forwarding?.Value));
    }

    [Fact]
    public async Task AMailForwardNamesNoMessageAccount()
    {
        await using var j = await Harness.StartAsync();
        var h = j.H;
        h.Fixture.SetMessages(
        [
            new MessageSummary
            {
                Id = "m1",
                AccountId = MailA,
                FolderId = InboxA.Folder,
                From = [new Address { Email = "x@example.invalid" }],
                Subject = "s",
                Date = Base,
                Snippet = "",
                Flags = [Flag.Seen],
                HasAttachments = false,
                Size = 0,
            },
        ],
        MailA,
        InboxA.Folder);
        // Listed again with the message.
        await h.SelectAsync(Web);
        await h.SelectAsync(InboxA);
        j.OnDraftCreate(new DraftCreateResult { Draft = new Draft { AccountId = MailA, Subject = "Fwd: s", Forwarding = "m1" }, Quoted = QuoteForm.Html });
        await h.Run(() =>
        {
            h.Mailbox.Model.Selected = InboxB;
            h.Actions.OpenCompose(ComposeKind.Forward, "m1");
        });
        await h.IdleAsync();
        var req = Assert.Single(j.Drafts);
        Assert.Equal(MailA, req.AccountId); // a mail message goes out from its own account
        Assert.Null(req.MessageAccountId);
    }

    [Fact]
    public async Task ForwardNeedsAnAccountThatWritesMail()
    {
        await using var j = await Harness.StartAsync([JiraAccount()]);
        var h = j.H;
        await j.ShowIssueAsync();
        var flags = await h.Ui.RunAsync(() => h.Actions.FlagsFor(h.Summary("w1c")));
        Assert.True(flags.Reply && flags.Comment);
        Assert.False(flags.Forward);
        Assert.True(flags.Unsupported.HasFlag(MessageActionKind.Forward));

        // An accelerator bypassing the disabled action creates nothing.
        j.OnDraftCreate(error: DaemonHarness.Daemon(ErrorCode.ServerError, "not to be asked"));
        await h.Run(() => h.Actions.OpenCompose(ComposeKind.Forward, "w1c"));
        await h.IdleAsync();
        Assert.Empty(j.Drafts);
        Assert.True(h.Log.Composed.Count == 0 && h.Log.Toasts.Count == 0);
    }

    [Fact]
    public async Task NewMessageNeedsAnAccountThatWritesMail()
    {
        await using (var only = await Harness.StartAsync([JiraAccount()]))
        {
            Assert.False(Capabilities.CanComposeNew(only.H.Mailbox.Model.Accounts));
        }
        await using var both = await Harness.StartAsync();
        Assert.True(Capabilities.CanComposeNew(both.H.Mailbox.Model.Accounts));
    }

    [Fact]
    public void TheConversationCardsOfferCommentAndForward()
    {
        var s = Item("w1c", 2);
        var a = Conversation.CardActions(JiraAccount(), s, composeAccount: true);
        Assert.True(a.Reply && a.Comment && a.Forward && !a.ReplyAll);
        Assert.False(Conversation.CardActions(JiraAccount(), s, composeAccount: false).Forward);
    }

    [Fact]
    public async Task AQueuedCommentIsRetriedAndCancelledInTheJiraOutbox()
    {
        await using var j = await Harness.StartAsync();
        var h = j.H;
        await h.SelectAsync(JiraOutbox);
        Assert.False(h.Mailbox.Model.Grouped); // the outbox is flat
        var queued = h.Summary("o1");
        var flags = await h.Ui.RunAsync(() => h.Actions.FlagsFor(queued));
        Assert.True(flags.Outbox && flags.Trash && !flags.Unsupported.HasFlag(MessageActionKind.Trash)); // Delete is there to cancel

        await h.Run(() =>
        {
            h.Actions.RetryOutbox("o1");
            Assert.Equal(OutboxState.Queued, h.Mailbox.Model.Message("o1")?.Summary.Outbox?.State);
        });
        await h.IdleAsync();
        var retry = Assert.Single(j.Retries);
        Assert.Equal((Jira, "o1"), (retry.AccountId, retry.MessageId.Value));

        await h.Run(() => h.Actions.Trash(["o1"], "WEB-1: Logo on the front page"));
        await h.IdleAsync();
        Assert.Equal(["Cancel sending this message?"], h.Log.Confirmations.Select(c => c.Heading));
        var delete = Assert.Single(h.Fixture.DeleteRequests);
        Assert.Equal((Jira, "o1"), (delete.AccountId, Assert.Single(delete.MessageIds).Value));
        Assert.Empty(h.List.Rows);
        Assert.Empty(h.Log.Toasts);
    }

    [Fact]
    public async Task TheFromListHasOnlyAccountsThatWriteMail()
    {
        await using var h = await ComposeAsync([JiraAccount(), TestAccount("a", email: "petr@example.invalid")]);
        Assert.Equal([MailA], h.Compose.Accounts.Select(a => a.Id));
        Assert.False(h.Compose.Placeholder);
        Assert.Equal([Jira, MailA], h.Compose.KnownAccounts.Select(a => a.Id)); // a comment window finds its account
        Assert.Equal("petr@example.invalid", h.Compose.SelfAddress.Email);
    }

    [Fact]
    public async Task IssueTrackersAloneWriteFromThePlaceholder()
    {
        await using var h = await ComposeAsync([JiraAccount()]);
        Assert.True(h.Compose.Placeholder);
        Assert.Same(ComposeController.PlaceholderAccounts, h.Compose.Accounts);
    }

    private static Account JiraAccount(Capability[]? capabilities = null) => new()
    {
        Id = Jira,
        Config = new AccountConfig
        {
            Name = "Acme Jira",
            Email = "jana@acme.example",
            Kind = AccountKind.Jira,
            Jira = new JiraConfig { SiteUrl = "https://acme.atlassian.net", Deployment = JiraDeployment.Cloud },
        },
        Enabled = true,
        State = new SyncState { AccountId = Jira, Status = SyncStatus.Idle },
        Capabilities = capabilities ?? [Capability.Comment, Capability.Forward],
    };

    // A member of WEB-1 dated hours after Base.
    private static MessageSummary Item(string id, int hours, IssueItemKind? kind = null, params Flag[] flags) => new()
    {
        Id = id,
        AccountId = Jira,
        FolderId = Web.Folder,
        ThreadId = "issue-WEB-1",
        From = [new Address { Name = "Jana Dvořáková", Email = "" }],
        Subject = "WEB-1: " + WebIssue.Summary,
        Date = Base.AddHours(hours),
        Snippet = "p-" + id,
        Flags = flags,
        HasAttachments = false,
        Size = 0,
        Issue = MessageIssue.Of(WebIssue, kind ?? IssueItemKind.Comment),
    };

    // A ComposeController over a fake daemon that lists accounts.
    private static async Task<ComposeHarness> ComposeAsync(Account[] accounts)
    {
        var d = await DaemonHarness.StartAsync();
        d.On(API.AccountList.Name, _ => JsonCoding.EncodeToString(new AccountListResult { Accounts = accounts }));
        var client = await d.ConnectAsync();
        var settings = Malachi.Core.Tests.Model.CollapseStateTests.Scratch();
        var compose = await d.Ui.RunAsync(() => new ComposeController(client, settings, d.Pending));
        await d.Ui.RunAsync(compose.RefreshAccounts);
        await d.IdleAsync();
        Assert.NotEmpty(compose.KnownAccounts);
        return new ComposeHarness(d, compose);
    }

    private sealed class ComposeHarness(DaemonHarness daemon, ComposeController compose) : IAsyncDisposable
    {
        public ComposeController Compose => compose;

        public ValueTask DisposeAsync() => daemon.DisposeAsync();
    }

    // Two mail accounts (the first one's Inbox is the initial folder) and a
    // Jira account with the space WEB and an outbox holding a failed
    // comment, over the actions harness; what the scripted handlers
    // received.
    private sealed class Harness(ActionsControllerHarness h) : IAsyncDisposable
    {
        private readonly List<DraftCreateParams> drafts = [];
        private readonly List<MessageDownloadParams> downloads = [];
        private readonly List<OutboxRetryParams> retries = [];

        public ActionsControllerHarness H => h;

        public IReadOnlyList<DraftCreateParams> Drafts => Locked(drafts);

        public IReadOnlyList<MessageDownloadParams> Downloads => Locked(downloads);

        public IReadOnlyList<OutboxRetryParams> Retries => Locked(retries);

        public static async Task<Harness> StartAsync(Account[]? accounts = null)
        {
            accounts ??=
            [
                TestAccount("a", email: "petr@example.invalid", displayName: "Petr"),
                TestAccount("b", email: "petr@work.example", displayName: "Petr"),
                JiraAccount(),
            ];
            Harness? j = null;
            var downloaded = JsonCoding.EncodeToString(new MessageDownloadResult { Message = new Message { Summary = Item("w1c", 2, flags: Flag.Seen) } });
            var h = await ActionsControllerHarness.StartAsync(
                folders: accounts[0].Id == MailA ? [TestFolder("in", "INBOX", FolderRole.Inbox)] : [TestFolder("web", "WEB", name: "Web"), TestFolder("out", "Outbox", FolderRole.Outbox)],
                accounts: accounts,
                script: f =>
                {
                    f.SetFolders([TestFolder("binb", "INBOX", FolderRole.Inbox)], MailB);
                    f.SetFolders([TestFolder("web", "WEB", name: "Web"), TestFolder("out", "Outbox", FolderRole.Outbox, total: 1)], Jira);
                    f.SetMessages([Item("w1d", 1, IssueItemKind.Description, Flag.Seen), Item("w1c", 2, flags: Flag.Seen)], Jira, Web.Folder);
                    var queued = Item("o1", 3, flags: Flag.Seen) with
                    {
                        FolderId = JiraOutbox.Folder,
                        Outbox = new OutboxInfo { State = OutboxState.Failed, Attempts = 2, Error = new RpcError { Code = ErrorCode.ServerError, Message = "500" } },
                    };
                    f.SetMessages([queued], Jira, JiraOutbox.Folder);
                    f.On(API.MessageDownload.Name, p =>
                    {
                        Record(j!.downloads, JsonCoding.Decode<MessageDownloadParams>(p));
                        return Task.FromResult(downloaded);
                    });
                    f.On(API.OutboxRetry.Name, p =>
                    {
                        Record(j!.retries, JsonCoding.Decode<OutboxRetryParams>(p));
                        return Task.FromResult("{}");
                    });
                });
            j = new Harness(h);
            return j;
        }

        // Lists the space and makes the members of WEB-1 known.
        public async Task ShowIssueAsync()
        {
            await h.SelectAsync(Web);
            var done = false;
            await h.Run(() => h.List.EnsureMembers("issue-WEB-1", () => done = true));
            await h.IdleAsync();
            Assert.True(done);
            Assert.NotNull(h.Mailbox.Model.Message("w1c"));
        }

        // Answers draft.create with result, or fails with error.
        public void OnDraftCreate(DraftCreateResult? result = null, RpcException? error = null)
        {
            var answer = result is null ? "{}" : JsonCoding.EncodeToString(result);
            h.Fixture.On(API.DraftCreate.Name, p =>
            {
                Record(drafts, JsonCoding.Decode<DraftCreateParams>(p));
                return error is not null ? Task.FromException<string>(error) : Task.FromResult(answer);
            });
        }

        public ValueTask DisposeAsync() => h.DisposeAsync();

        private static IReadOnlyList<T> Locked<T>(List<T> list)
        {
            lock (list)
            {
                return [.. list];
            }
        }

        private static void Record<T>(List<T> list, T value)
        {
            lock (list)
            {
                list.Add(value);
            }
        }
    }
}
