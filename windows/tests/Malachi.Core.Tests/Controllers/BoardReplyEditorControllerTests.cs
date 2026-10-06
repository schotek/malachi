// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardReplyEditorControllerTests.swift;
// Go: ui/internal/boardreply/editor_test.go. The board's inline reply
// editor state (BoardReplyEditorController) against a fake daemon: a new
// key loads the draft with draft.get, the same key never reloads (an
// autosave bumps the case's version), a stale answer is dropped, a draft
// the pane ended stays hidden until the board drops the link, retry, and
// the failures. The fake holds draft.get answers until the test releases
// them, so no outcome depends on timing; "nothing more happens" is checked
// once everything is idle (Quiescence), never after a sleep. Swift's
// samplesUnflagIsAPlaceholder (InMemoryBoardSource) is ported with the
// source, InMemoryBoardSourceTests.UnflagIsAPlaceholder.

using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Xunit;
using Key = Malachi.Core.Controllers.BoardReplyEditorController.Key;
using Phase = Malachi.Core.Controllers.BoardReplyEditorController.Phase;

namespace Malachi.Core.Tests.Controllers;

public sealed class BoardReplyEditorControllerTests
{
    private static readonly DateTimeOffset T0 = DateTimeOffset.FromUnixTimeSeconds(1_790_000_000);

    internal static Board.Case BoardCase(string n, string? draft = null, long version = 1) => new()
    {
        Id = new BoardCaseId("c_" + n),
        Account = new AccountId("acc_1"),
        Thread = new ThreadId("t_" + n),
        Person = "Ann",
        Date = T0,
        Subject = "Offer",
        RuleState = Board.State.You,
        Reply = new Board.ReplyTarget(new MessageId("m_2"), new FolderId("f_inbox")),
        Draft = draft is null ? null : new Board.DraftLink(new DraftId(draft), ""),
        Version = version,
    };

    private static Key K(string n, string draft) => new(new BoardCaseId("c_" + n), new AccountId("acc_1"), new DraftId(draft));

    [Fact]
    public async Task ACaseWithoutADraftShowsNothing()
    {
        await using var h = await Harness.StartAsync();
        await h.Ui.RunAsync(() =>
        {
            h.Editor.Show(BoardCase("1"));
            h.Editor.Show(null);
        });
        await h.IdleAsync();
        Assert.Equal(new Phase.None(), await h.PhaseAsync());
        Assert.Equal(0, h.Changes);
        Assert.Empty(h.Drafts.Asked);
    }

    [Fact]
    public async Task ANewKeyLoadsTheDraft()
    {
        await using var h = await Harness.StartAsync();
        h.Drafts.Hold(true);
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("1", "d_1")));
        Assert.Equal(new Phase.Loading(K("1", "d_1")), await h.PhaseAsync());
        await Eventually.Holds(() => h.Drafts.Held == 1);
        h.Drafts.Hold(false);
        await h.IdleAsync();
        var ready = Assert.IsType<Phase.Ready>(await h.PhaseAsync());
        Assert.Equal(K("1", "d_1"), ready.Of);
        var p = ready.Params;
        Assert.True(p.Kind == ComposeKind.Edit && p.DraftId == new DraftId("d_1") && p.Version == 5 && p.AccountId == new AccountId("acc_1"));
        Assert.True(p.BodyHtml == "<p>Text of d_1</p>" && p.InReplyTo == new MessageId("m_2") && p.Subject == "Re: Offer");
        Assert.Equal([new DraftGetParams { AccountId = "acc_1", DraftId = "d_1" }], h.Drafts.Asked);
        // Loading, ready.
        Assert.Equal(2, h.Changes);
    }

    [Fact]
    public async Task TheSameKeyNeverReloads()
    {
        await using var h = await Harness.StartAsync();
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("1", "d_1", version: 1)));
        await h.IdleAsync();
        Assert.IsType<Phase.Ready>(await h.PhaseAsync());
        var changes = h.Changes;
        // Autosaves bump the case's version; the board lists it again.
        await h.Ui.RunAsync(() =>
        {
            for (var v = 2; v <= 6; v++)
            {
                h.Editor.Show(BoardCase("1", "d_1", version: v));
            }
        });
        await h.IdleAsync();
        Assert.Single(h.Drafts.Asked);
        Assert.Equal(changes, h.Changes);
        Assert.IsType<Phase.Ready>(await h.PhaseAsync());
        // Also while loading.
        h.Drafts.Hold(true);
        await h.Ui.RunAsync(() =>
        {
            h.Editor.Show(BoardCase("2", "d_2"));
            h.Editor.Show(BoardCase("2", "d_2", version: 9));
        });
        await Eventually.Holds(() => h.Drafts.Held == 1);
        Assert.Equal(2, h.Drafts.Asked.Count);
    }

    [Fact]
    public async Task AStaleAnswerIsDropped()
    {
        await using var h = await Harness.StartAsync();
        h.Drafts.Hold(true);
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("1", "d_1")));
        await Eventually.Holds(() => h.Drafts.Held == 1);
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("2", "d_2")));
        await Eventually.Holds(() => h.Drafts.Held == 2);
        // The first case's answer arrives after the second was selected.
        h.Drafts.ReleaseOne();
        await Eventually.Holds(() => h.Pending.Count == 1, what: "the first answer handled");
        Assert.Equal(new Phase.Loading(K("2", "d_2")), await h.PhaseAsync());
        h.Drafts.ReleaseOne();
        await h.IdleAsync();
        Assert.Equal(K("2", "d_2"), Assert.IsType<Phase.Ready>(await h.PhaseAsync()).Of);
        // Deselected while loading: the answer is dropped too.
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("3", "d_3")));
        await Eventually.Holds(() => h.Drafts.Held == 1);
        await h.Ui.RunAsync(() => h.Editor.Show(null));
        h.Drafts.Hold(false);
        await h.IdleAsync();
        Assert.Equal(new Phase.None(), await h.PhaseAsync());
    }

    [Fact]
    public async Task AnotherDraftOfTheSameCaseLoads()
    {
        await using var h = await Harness.StartAsync();
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("1", "d_1")));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("1", "d_9")));
        await h.IdleAsync();
        Assert.Equal(K("1", "d_9"), Assert.IsType<Phase.Ready>(await h.PhaseAsync()).Of);
        Assert.Equal(2, h.Drafts.Asked.Count);
        // The link dropped: nothing.
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("1")));
        Assert.Equal(new Phase.None(), await h.PhaseAsync());
    }

    [Fact]
    public async Task EndedHidesUntilTheLinkGoes()
    {
        await using var h = await Harness.StartAsync();
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("1", "d_1")));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => h.Editor.Ended(K("1", "d_1")));
        Assert.Equal(new Phase.None(), await h.PhaseAsync());
        // The board still links it for a moment (the refresh is on its way).
        await h.Ui.RunAsync(() =>
        {
            h.Editor.Show(BoardCase("1", "d_1", version: 2));
            h.Editor.Show(BoardCase("2"));
            h.Editor.Show(BoardCase("1", "d_1", version: 3));
        });
        Assert.Equal(new Phase.None(), await h.PhaseAsync());
        await h.IdleAsync();
        Assert.Single(h.Drafts.Asked);
        // The link went; a later suggested reply, even under the same id, is
        // edited again.
        await h.Ui.RunAsync(() =>
        {
            h.Editor.Show(BoardCase("1"));
            h.Editor.Show(BoardCase("1", "d_1", version: 4));
            // Asked in the same turn: the answer may come at once.
            Assert.Equal(new Phase.Loading(K("1", "d_1")), h.Editor.Current);
        });
        await h.IdleAsync();
        Assert.Equal(2, h.Drafts.Asked.Count);
        // Ending another key changes nothing shown.
        Assert.IsType<Phase.Ready>(await h.PhaseAsync());
        await h.Ui.RunAsync(() => h.Editor.Ended(K("7", "d_7")));
        Assert.IsType<Phase.Ready>(await h.PhaseAsync());
    }

    [Fact]
    public async Task FailuresAndRetry()
    {
        await using var h = await Harness.StartAsync();
        h.Drafts.Failure = new RpcError { Code = ErrorCode.StorageError, Message = "disk" };
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("1", "d_1")));
        await h.IdleAsync();
        Assert.Equal(new Phase.Failed(K("1", "d_1"), BoardReplyEditorController.Failure.Backend), await h.PhaseAsync());
        // The same key while failed: no new request (Try Again asks).
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("1", "d_1", version: 2)));
        await h.IdleAsync();
        Assert.Single(h.Drafts.Asked);
        h.Drafts.Failure = null;
        await h.Ui.RunAsync(() =>
        {
            h.Editor.Retry();
            Assert.Equal(new Phase.Loading(K("1", "d_1")), h.Editor.Current);
        });
        await h.IdleAsync();
        Assert.IsType<Phase.Ready>(await h.PhaseAsync());
        Assert.Equal(2, h.Drafts.Asked.Count);
        // Retry outside a failure does nothing.
        await h.Ui.RunAsync(h.Editor.Retry);
        await h.IdleAsync();
        Assert.Equal(2, h.Drafts.Asked.Count);

        h.Drafts.Failure = new RpcError { Code = ErrorCode.DraftNotFound, Message = "gone" };
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("2", "d_2")));
        await h.IdleAsync();
        Assert.Equal(new Phase.Failed(K("2", "d_2"), BoardReplyEditorController.Failure.Gone), await h.PhaseAsync());
    }

    [Fact]
    public async Task ALostConnectionIsABackendFailure()
    {
        await using var h = await Harness.StartAsync();
        h.Drafts.Hold(true);
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("1", "d_1")));
        await Eventually.Holds(() => h.Drafts.Held == 1);
        h.Fake.CloseAll();
        await h.WhenAsync(() => h.Editor.Current is Phase.Failed, "the failure");
        Assert.Equal(new Phase.Failed(K("1", "d_1"), BoardReplyEditorController.Failure.Backend), await h.PhaseAsync());
    }

    [Fact]
    public async Task ObserversCanBeRemoved()
    {
        await using var h = await Harness.StartAsync();
        await h.Ui.RunAsync(h.Token.Cancel);
        await h.Ui.RunAsync(() => h.Editor.Show(BoardCase("1", "d_1")));
        await h.IdleAsync();
        Assert.IsType<Phase.Ready>(await h.PhaseAsync());
        Assert.Equal(0, h.Changes);
    }

    /// <summary>draft.get: what it was asked, the answers held until released.</summary>
    private sealed class DraftGets
    {
        private readonly Lock gate = new();
        private readonly List<DraftGetParams> asked = [];
        private readonly Queue<TaskCompletionSource> waiting = new();
        private bool holding;
        private RpcError? failure;

        public RpcError? Failure
        {
            get
            {
                lock (gate)
                {
                    return failure;
                }
            }
            set
            {
                lock (gate)
                {
                    failure = value;
                }
            }
        }

        public IReadOnlyList<DraftGetParams> Asked
        {
            get
            {
                lock (gate)
                {
                    return [.. asked];
                }
            }
        }

        public int Held
        {
            get
            {
                lock (gate)
                {
                    return waiting.Count;
                }
            }
        }

        public void Hold(bool on)
        {
            List<TaskCompletionSource> release = [];
            lock (gate)
            {
                holding = on;
                if (!on)
                {
                    release.AddRange(waiting);
                    waiting.Clear();
                }
            }
            release.ForEach(w => w.TrySetResult());
        }

        /// <summary>Releases the oldest held answer only.</summary>
        public void ReleaseOne()
        {
            TaskCompletionSource? w = null;
            lock (gate)
            {
                if (waiting.Count > 0)
                {
                    w = waiting.Dequeue();
                }
            }
            w?.TrySetResult();
        }

        public async Task<string> GetAsync(string json)
        {
            var p = JsonCoding.Decode<DraftGetParams>(json);
            Task wait = Task.CompletedTask;
            RpcError? f;
            lock (gate)
            {
                asked.Add(p);
                f = failure;
                if (holding)
                {
                    var w = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                    waiting.Enqueue(w);
                    wait = w.Task;
                }
            }
            await wait;
            if (f is not null)
            {
                throw new RpcException(f);
            }
            return JsonCoding.EncodeToString(new DraftGetResult
            {
                Draft = new Draft
                {
                    Id = p.DraftId,
                    AccountId = p.AccountId,
                    Version = 5,
                    To = [new Address { Name = "Ann", Email = "ann@example.org" }],
                    Subject = "Re: Offer",
                    TextBody = "Text of " + p.DraftId.Value,
                    HtmlBody = "<p>Text of " + p.DraftId.Value + "</p>",
                    InReplyTo = "m_2",
                    Local = true,
                },
            });
        }
    }

    private sealed class Harness : IAsyncDisposable
    {
        private readonly UiConditions conditions = new();
        private int changes;

        private Harness()
        {
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeDaemon Fake { get; } = new();

        public DraftGets Drafts { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public BoardReplyEditorController Editor { get; private set; } = null!;

        public BoardObserverToken Token { get; private set; } = null!;

        public int Changes => Volatile.Read(ref changes);

        public static async Task<Harness> StartAsync()
        {
            var h = new Harness();
            h.Fake.On(API.DraftGet.Name, (FakeDaemon.MethodHandler)h.Drafts.GetAsync);
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            await h.Ui.RunAsync(() =>
            {
                h.Editor = new BoardReplyEditorController(h.Client, pending: h.Pending);
                h.Token = h.Editor.Observe(() =>
                {
                    Interlocked.Increment(ref h.changes);
                    h.conditions.Changed();
                });
            });
            return h;
        }

        public Task<Phase> PhaseAsync() => Ui.RunAsync(() => Editor.Current);

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        public Task WhenAsync(Func<bool> condition, string what) => conditions.WhenAsync(Ui, condition, what);

        public async ValueTask DisposeAsync()
        {
            Drafts.Hold(false);
            await Ui.RunAsync(Editor.Dispose);
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
