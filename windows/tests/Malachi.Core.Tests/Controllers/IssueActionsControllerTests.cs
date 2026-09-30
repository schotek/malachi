// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/IssueActionsControllerTests.swift,
// the counterpart of ui/internal/window/issue_actions_test.go: the Change
// Status menu's controller over a fake daemon. The items of
// issue.transitions (a transition that needs fields in Jira disabled with
// the hint), issue.transition with its toast and the refreshed issue for
// the cards, the failure toasts, the stale-reply discipline of the loads,
// one transition per issue at a time, and nothing at all for an account
// without the capability. Swift's delayed answers are held here
// (HeldAnswer). Added: a transition chosen as the window closes is still
// sent, and its reply reaches nobody.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Xunit;
using Subject = Malachi.Core.Controllers.IssueActionsController.Subject;

namespace Malachi.Core.Tests.Controllers;

public sealed class IssueActionsControllerTests
{
    private static readonly AccountId JiraAccount = "j";
    private static readonly AccountId PlainAccount = "p";
    private static readonly AccountId MailAccount = "m";

    private static readonly IssueInfo Issue = new()
    {
        Key = "ITSD-42",
        Url = "https://acme.atlassian.net/browse/ITSD-42",
        Summary = "The printer on the third floor",
        Status = "To Do",
        StatusCategory = IssueStatusCategory.Todo,
        Assignee = "Jana Dvořáková",
    };

    private static readonly IssueInfo InProgress = Issue with { Status = "In Progress", StatusCategory = IssueStatusCategory.InProgress };

    // The transition items of the script's answer.
    private static readonly JiraTransitionItem Start = new() { Id = "11", Title = "Start Progress", Target = "In Progress", Subtitle = "In Progress", Enabled = true };
    private static readonly JiraTransitionItem Resolve = new() { Id = "31", Title = "Resolve", Target = "Resolved", Subtitle = "Resolved", Hint = "Needs fields in Jira" };
    private static readonly JiraTransitionItem Done = new() { Id = "21", Title = "Done", Target = "Done", Enabled = true };

    [Fact]
    public async Task LoadListsTheTransitions()
    {
        await using var h = await Harness.StartAsync();
        Assert.True(h.Controller.CanTransition(JiraAccount));
        Assert.True(await h.LoadAsync());
        await h.IdleAsync();
        var got = Assert.Single(h.Loaded).Value!;
        Assert.Equal(Issue, got.Issue);
        Assert.Equal([Start, Resolve, Done], got.Items);
        Assert.True(!got.Items[1].Enabled && got.Items[1].Hint == "Needs fields in Jira"); // a transition that needs input is listed disabled
        var asked = Assert.Single(h.Daemon.Params.All<IssueTransitionsParams>(API.IssueTransitions.Name));
        Assert.Equal((JiraAccount, new MessageId("m1")), (asked.AccountId, asked.MessageId));
        Assert.Equal(["issue.transitions"], h.Daemon.Fake.Calls);
    }

    [Fact]
    public async Task NothingWithoutTheCapability()
    {
        await using var h = await Harness.StartAsync();
        Assert.False(h.Controller.CanTransition(PlainAccount)); // comment and forward alone do not change statuses
        Assert.False(h.Controller.CanTransition(MailAccount)); // a mail account never does
        Assert.False(h.Controller.CanTransition("unknown"));
        Assert.False(await h.LoadAsync(PlainAccount));
        Assert.False(await h.LoadAsync(MailAccount));
        Assert.False(await h.Daemon.Ui.RunAsync(() => h.Controller.Perform(new Subject(PlainAccount, "m1"), Start, Issue)));
        await h.IdleAsync();
        Assert.True(h.Loaded.Count == 0 && h.Toasts.Count == 0 && h.Busy.Count == 0);
        Assert.Empty(h.Daemon.Fake.Calls); // the daemon is never asked
    }

    [Fact]
    public async Task LoadFailureReachesTheMenu()
    {
        await using var h = await Harness.StartAsync();
        h.ListError = DaemonHarness.Daemon(ErrorCode.NetworkError, "dial tcp: refused");
        Assert.True(await h.LoadAsync());
        await h.IdleAsync();
        var error = Assert.Single(h.Loaded).Error;
        Assert.Equal(ErrorCode.NetworkError, Assert.IsType<RpcException>(error).Code);
        Assert.Equal("Loading the status changes failed: the server could not be reached", RpcErrorText.Text(Jira.LoadTransitionsAction(), error));
        Assert.Empty(h.Toasts); // the menu shows the failure; no toast
    }

    [Fact]
    public async Task OnlyTheNewestLoadAnswers()
    {
        await using var h = await Harness.StartAsync();
        var first = h.Daemon.Hold();
        h.ListHold = first;
        Assert.True(await h.LoadAsync(JiraAccount, "m1"));
        await first.ArrivedAsync();
        h.ListHold = null;
        Assert.True(await h.LoadAsync(JiraAccount, "m2"));
        await h.Conditions.WhenAsync(h.Daemon.Ui, () => h.Loaded.Count == 1, "the second load answered");
        first.Release();
        await h.IdleAsync();
        Assert.Single(h.Loaded); // the first load's reply was dropped
        Assert.Equal(["m1", "m2"], h.Daemon.Params.All<IssueTransitionsParams>(API.IssueTransitions.Name).Select(p => p.MessageId.Value)); // both were asked

        // CancelLoad: the menu closed before the answer.
        await h.Daemon.Ui.RunAsync(() =>
        {
            h.Load(JiraAccount, "m3");
            h.Controller.CancelLoad();
        });
        await h.IdleAsync();
        Assert.Single(h.Loaded); // a cancelled load answers nobody
        Assert.Equal(3, h.Daemon.Params.Count(API.IssueTransitions.Name));
    }

    [Fact]
    public async Task PerformChangesTheStatus()
    {
        await using var h = await Harness.StartAsync();
        var subject = new Subject(JiraAccount, "m1");
        await h.Daemon.Ui.RunAsync(() =>
        {
            Assert.True(h.Controller.Perform(subject, Start, Issue));
            Assert.True(h.Controller.IsBusy(JiraAccount, "ITSD-42"));
            Assert.Equal([(JiraAccount, "ITSD-42", true)], h.Busy);
        });
        await h.IdleAsync();
        Assert.Equal(["Status changed to In Progress"], h.Toasts);
        Assert.Equal([(JiraAccount, InProgress)], h.Changed); // the cards get the refreshed issue
        Assert.Equal([(JiraAccount, "ITSD-42", true), (JiraAccount, "ITSD-42", false)], h.Busy);
        Assert.False(h.Controller.IsBusy(JiraAccount, "ITSD-42"));
        var sent = Assert.Single(h.Daemon.Params.All<IssueTransitionParams>(API.IssueTransition.Name));
        Assert.Equal((JiraAccount, new MessageId("m1"), "11"), (sent.AccountId, sent.MessageId, sent.TransitionId));
        Assert.Equal(["issue.transition"], h.Daemon.Fake.Calls);
    }

    [Fact]
    public async Task TheToastNamesTheTargetEvenWhenTheRefreshWasLate()
    {
        await using var h = await Harness.StartAsync();
        // The daemon's refresh timed out: the result still shows To Do.
        h.Refreshed = Issue;
        Assert.True(await h.Daemon.Ui.RunAsync(() => h.Controller.Perform(new Subject(JiraAccount, "m1"), Start, Issue)));
        await h.IdleAsync();
        Assert.Equal(["Status changed to In Progress"], h.Toasts);
        Assert.Equal(Issue, Assert.Single(h.Changed).Issue); // the card shows what the daemon knows; the sync brings the rest
    }

    [Fact]
    public async Task FailuresToast()
    {
        await using var h = await Harness.StartAsync();
        var subject = new Subject(JiraAccount, "m1");
        h.TransitionError = DaemonHarness.Daemon(ErrorCode.ServerError, "Transition is not allowed by the workflow");
        Assert.True(await h.Daemon.Ui.RunAsync(() => h.Controller.Perform(subject, Start, Issue)));
        await h.IdleAsync();
        Assert.Equal("The status could not be changed: Transition is not allowed by the workflow", Assert.Single(h.Toasts));
        Assert.Empty(h.Changed); // no issue to apply
        Assert.Equal([true, false], h.Busy.Select(b => b.Busy));

        h.TransitionError = DaemonHarness.Daemon(ErrorCode.NetworkError, "refused");
        Assert.True(await h.Daemon.Ui.RunAsync(() => h.Controller.Perform(subject, Done, Issue)));
        await h.IdleAsync();
        Assert.Equal("Changing the status failed: the server could not be reached", h.Toasts[1]);

        h.TransitionError = DaemonHarness.Daemon(ErrorCode.InvalidArgument, "transition needs input");
        Assert.True(await h.Daemon.Ui.RunAsync(() => h.Controller.Perform(subject, Done, Issue)));
        await h.IdleAsync();
        Assert.Equal("Changing the status was rejected: transition needs input", h.Toasts[2]);
    }

    [Fact]
    public async Task ADisabledItemIsNeverPerformed()
    {
        await using var h = await Harness.StartAsync();
        var subject = new Subject(JiraAccount, "m1");
        await h.Daemon.Ui.RunAsync(() =>
        {
            Assert.False(h.Controller.Perform(subject, Resolve, Issue)); // needs fields in Jira
            Assert.False(h.Controller.Perform(subject, new JiraTransitionItem { Title = "x", Enabled = true }, Issue)); // no id
        });
        await h.IdleAsync();
        Assert.True(h.Busy.Count == 0 && h.Toasts.Count == 0);
        Assert.Empty(h.Daemon.Fake.Calls);
    }

    [Fact]
    public async Task OneTransitionPerIssueAtATime()
    {
        await using var h = await Harness.StartAsync();
        var hold = h.Daemon.Hold();
        h.TransitionHold = hold;
        var subject = new Subject(JiraAccount, "m1");
        await h.Daemon.Ui.RunAsync(() =>
        {
            Assert.True(h.Controller.Perform(subject, Start, Issue));
            Assert.False(h.Controller.Perform(subject, Done, Issue)); // the issue is busy
            // Another issue of the account is not.
            Assert.True(h.Controller.Perform(new Subject(JiraAccount, "w1"), Done, Issue with { Key = "WEB-7" }));
        });
        await hold.ArrivedAsync();
        hold.Release();
        await h.IdleAsync();
        Assert.Equal(2, h.Toasts.Count);
        Assert.Equal(["11", "21"], h.Daemon.Params.All<IssueTransitionParams>(API.IssueTransition.Name).Select(p => p.TransitionId).Order(StringComparer.Ordinal));
        Assert.True(!h.Controller.IsBusy(JiraAccount, "ITSD-42") && !h.Controller.IsBusy(JiraAccount, "WEB-7"));
        // Free again: the next choice goes through.
        Assert.True(await h.Daemon.Ui.RunAsync(() => h.Controller.Perform(subject, Done, Issue)));
        await h.IdleAsync();
        Assert.Equal(3, h.Toasts.Count);
    }

    // Windows-only: a status chosen as the window closes is still changed;
    // the reply reaches nobody.
    [Fact]
    public async Task ATransitionChosenAsTheWindowClosesIsSent()
    {
        await using var h = await Harness.StartAsync();
        await h.Daemon.Ui.RunAsync(() =>
        {
            Assert.True(h.Controller.Perform(new Subject(JiraAccount, "m1"), Start, Issue));
            h.Controller.Close();
        });
        await h.IdleAsync();
        Assert.Equal(["issue.transition"], h.Daemon.Fake.Calls);
        Assert.Empty(h.Toasts);
        Assert.Empty(h.Changed);
    }

    private static Account JiraAccountOf(AccountId id, Capability[]? caps) => new()
    {
        Id = id,
        Config = new AccountConfig { Name = "Acme Jira", Email = "jana@acme.example", Kind = AccountKind.Jira },
        Enabled = true,
        State = new SyncState { AccountId = id, Status = SyncStatus.Idle },
        Capabilities = caps,
    };

    // The daemon's answers (Swift's Script actor), the controller over it and
    // what it reported.
    private sealed class Harness : IAsyncDisposable
    {
        private readonly Dictionary<AccountId, Account> accounts = new()
        {
            [JiraAccount] = JiraAccountOf(JiraAccount, [Capability.Comment, Capability.Forward, Capability.Transition]),
            [PlainAccount] = JiraAccountOf(PlainAccount, [Capability.Comment, Capability.Forward]),
            [MailAccount] = new Account
            {
                Id = MailAccount,
                Config = new AccountConfig { Name = "Mail", Email = "me@example.invalid" },
                Enabled = true,
                State = new SyncState { AccountId = MailAccount, Status = SyncStatus.Idle },
            },
        };

        private Harness(DaemonHarness daemon) => Daemon = daemon;

        public DaemonHarness Daemon { get; }

        public IssueActionsController Controller { get; private set; } = null!;

        public RpcException? ListError { get; set; }

        public HeldAnswer? ListHold { get; set; }

        public RpcException? TransitionError { get; set; }

        public HeldAnswer? TransitionHold { get; set; }

        public IssueInfo Refreshed { get; set; } = InProgress;

        public List<string> Toasts { get; } = [];

        public List<(AccountId Account, string Key, bool Busy)> Busy { get; } = [];

        public List<(AccountId Account, IssueInfo Issue)> Changed { get; } = [];

        public List<Outcome<IssueActionsController.Loaded>> Loaded { get; } = [];

        public UiConditions Conditions { get; } = new();

        public static async Task<Harness> StartAsync()
        {
            var h = new Harness(await DaemonHarness.StartAsync());
            h.Daemon.On(API.IssueTransitions.Name, async _ =>
            {
                if (h.ListHold is { } hold)
                {
                    await hold.WaitAsync();
                }
                if (h.ListError is { } error)
                {
                    throw error;
                }
                return JsonCoding.EncodeToString(new IssueTransitionsResult
                {
                    Issue = Issue,
                    Transitions =
                    [
                        new IssueTransition { Id = "11", Name = "Start Progress", To = "In Progress", ToCategory = IssueStatusCategory.InProgress },
                        new IssueTransition { Id = "31", Name = "Resolve", To = "Resolved", ToCategory = IssueStatusCategory.Done, NeedsInput = true },
                        new IssueTransition { Id = "21", Name = "Done", To = "Done", ToCategory = IssueStatusCategory.Done },
                    ],
                });
            });
            h.Daemon.On(API.IssueTransition.Name, async _ =>
            {
                if (h.TransitionHold is { } hold)
                {
                    await hold.WaitAsync();
                }
                if (h.TransitionError is { } error)
                {
                    throw error;
                }
                return JsonCoding.EncodeToString(new IssueTransitionResult { Issue = h.Refreshed });
            });
            var client = await h.Daemon.ConnectAsync();
            await h.Daemon.Ui.RunAsync(() =>
            {
                var c = new IssueActionsController(client, id => h.accounts.GetValueOrDefault(id), pending: h.Daemon.Pending);
                c.ToastRequested += (_, text) => h.Note(() => h.Toasts.Add(text));
                c.BusyChanged += (_, e) => h.Note(() => h.Busy.Add(e));
                c.IssueChanged += (_, e) => h.Note(() => h.Changed.Add(e));
                h.Daemon.CloseAtEnd(c.Close);
                h.Controller = c;
            });
            return h;
        }

        // On the UI thread.
        public bool Load(AccountId account, MessageId message) =>
            Controller.LoadTransitions(new Subject(account, message), outcome => Note(() => Loaded.Add(outcome)));

        public Task<bool> LoadAsync(AccountId? account = null, string message = "m1") =>
            Daemon.Ui.RunAsync(() => Load(account ?? JiraAccount, message));

        public Task IdleAsync() => Daemon.IdleAsync();

        public ValueTask DisposeAsync() => Daemon.DisposeAsync();

        private void Note(Action record)
        {
            record();
            Conditions.Changed();
        }
    }
}
