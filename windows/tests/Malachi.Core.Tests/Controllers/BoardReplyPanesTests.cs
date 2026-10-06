// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardReplyPanesTests.swift (suite
// BoardReplyPanesTests; its second suite, over the real draft controller,
// is BoardReplyPanesDraftTests); Go: ui/internal/boardreply/panes_test.go.
// The rules of the board's inline reply panes (BoardReplyPanes) with fake
// panes over the real loader and a fake daemon: a retired pane is saved and
// closed; a sending pane is never settled and its outcome always arrives; a
// pane with unsaved text is kept (never trimmed), retried with back-off and
// shown again with its note; another link is not proof the draft went; a
// pane taken back while it settles stays; the quit says whether anything is
// at stake.
//
// Swift's back-off delays are milliseconds of real time and every wait a
// poll; here the back-off and the quit's bound run on a FakeTimeProvider the
// test advances, a settle the test holds is waited for as a condition met on
// the UI thread (UiConditions), and "nothing more happens" is checked once
// everything is idle (Quiescence), never after a sleep. The back-off's
// doubling (20, 40, 80 ms) is checked step by step, which Swift leaves to
// the clock.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using End = Malachi.Core.Controllers.BoardReplyPanes.PaneEnd;
using Key = Malachi.Core.Controllers.BoardReplyEditorController.Key;
using Slot = Malachi.Core.Controllers.BoardReplyPanes.Slot;

namespace Malachi.Core.Tests.Controllers;

public sealed class BoardReplyPanesTests
{
    private static readonly TimeSpan Ms = TimeSpan.FromMilliseconds(1);

    internal static Board.Case BoardCase(string n, string? draft = "d") => new()
    {
        Id = new BoardCaseId("c_" + n),
        Account = new AccountId("acc_1"),
        Thread = new ThreadId("t_" + n),
        Person = "Ann",
        Date = DateTimeOffset.FromUnixTimeSeconds(1_790_000_000),
        Subject = "Offer",
        RuleState = Board.State.You,
        Reply = new Board.ReplyTarget(new MessageId("m_2"), new FolderId("f_inbox")),
        Draft = draft is null ? null : new Board.DraftLink(new DraftId(draft + "_" + n), ""),
        Version = 1,
    };

    [Fact]
    public async Task AReadyDraftGetsAPaneAndTheSameKeyKeepsIt()
    {
        await using var h = await Harness.StartAsync();
        var c1 = BoardCase("1");
        var p = await h.OpenAsync(c1);
        Assert.True(h.Made.Count == 1 && h.Adopted.Count == 1 && p.Key.Draft == new DraftId("d_1"));
        // The board lists the case again (every autosave): nothing changes.
        await h.Run(() =>
        {
            h.Panes.Show(c1);
            h.Panes.Show(c1);
        });
        await h.IdleAsync();
        Assert.Same(p, await h.LiveAsync());
        Assert.True(h.Made.Count == 1 && p.Settles == 0);
    }

    [Fact]
    public async Task AnotherSelectionSavesThenClosesThePane()
    {
        await using var h = await Harness.StartAsync();
        var p = await h.OpenAsync(BoardCase("1"));
        p.Holding = true;
        await h.Run(() => h.Panes.Show(BoardCase("2")));
        await h.WhenAsync(() => p.Waiting == 1, "the save");
        // Not before its save answered.
        Assert.True(!p.Closed && h.Detached.Count == 0);
        await h.Run(p.Release);
        await h.IdleAsync();
        Assert.True(p.Closed);
        Assert.Same(p, h.Detached[0]);
        Assert.True(p.Settles == 1 && p.Abandons == 0);
    }

    // Finding 2: Send in flight, then another case.
    [Fact]
    public async Task ASendingPaneIsNeverSettledAndItsSuccessArrives()
    {
        await using var h = await Harness.StartAsync();
        var c1 = BoardCase("1");
        var p = await h.OpenAsync(c1);
        p.IsSending = true;
        await h.OpenAsync(BoardCase("2"));
        await h.IdleAsync();
        Assert.True(p.Settles == 0 && !p.Closed && h.Detached.Count == 0);
        // The send answers while the user is on case 2.
        await h.Run(() =>
        {
            p.IsSending = false;
            h.Panes.Ended(p, new End.Sent("Message queued for sending"));
        });
        Assert.Equal(["Message queued for sending"], h.Toasts);
        Assert.True(p.Closed && h.Detached.Contains(p));
        // The loader hides the sent draft while the board still links it.
        await h.Run(() => h.Panes.Show(c1));
        await h.IdleAsync();
        Assert.Null(await h.LiveAsync());
        Assert.Equal(2, h.Made.Count);
    }

    [Fact]
    public async Task AFailedSendOutOfSightSaysWhichReplyAndKeepsThePane()
    {
        await using var h = await Harness.StartAsync();
        var c1 = BoardCase("1");
        var p = await h.OpenAsync(c1);
        p.IsSending = true;
        p.ReplyTitle = "Re: Offer";
        await h.Run(() =>
        {
            h.Panes.Show(BoardCase("2"));
            p.IsSending = false;
            h.Panes.SendFailed(p);
        });
        Assert.Equal([Board.Text.ReplyNotSent("Re: Offer")], h.Toasts);
        await h.IdleAsync();
        Assert.Equal(1, p.Settles);
        // Kept for the user to come back.
        Assert.True(!p.Closed && h.Detached.Count == 0);
        // Back on case 1: the same pane, Send there again.
        await h.Run(() => h.Panes.Show(c1));
        Assert.Same(p, await h.LiveAsync());
        Assert.Equal(1, h.MadeFor(c1));
    }

    [Fact]
    public async Task AFailedSendInSightAddsNothing()
    {
        await using var h = await Harness.StartAsync();
        var p = await h.OpenAsync(BoardCase("1"));
        await h.Run(() => h.Panes.SendFailed(p));
        await h.IdleAsync();
        Assert.Empty(h.Toasts);
        Assert.Same(p, await h.LiveAsync());
        Assert.Equal(0, p.Settles);
    }

    // Finding 3: unsaved text is kept, retried, never trimmed.
    [Fact]
    public async Task AnUnsavedPaneIsKeptAndRetriedWithBackOff()
    {
        await using var h = await Harness.StartAsync();
        var p = await h.OpenAsync(BoardCase("1"));
        p.Results.AddRange([false, false, false]);
        await h.Run(() => h.Panes.Show(BoardCase("2")));
        await h.IdleAsync();
        Assert.Equal(1, p.Settles);
        // Two identical failures do not end the retries; each waits twice as
        // long as the one before, up to the most (20, 40, 80 ms).
        foreach (var (delay, settles) in new[] { (20, 2), (40, 3), (80, 4) })
        {
            await h.AdvanceAsync((delay - 1) * Ms);
            Assert.Equal(settles - 1, p.Settles);
            await h.AdvanceAsync(Ms);
            Assert.Equal(settles, p.Settles);
        }
        Assert.True(p.Closed && p.Abandons == 0);
        Assert.Same(p, h.Detached[0]);
    }

    [Fact]
    public async Task PanesWithUnsavedTextAreNeverTrimmed()
    {
        await using var h = await Harness.StartAsync(retryFirst: TimeSpan.FromSeconds(600), retryMax: TimeSpan.FromSeconds(600));
        h.SetUp = p => p.Fallback = false;
        for (var n = 1; n <= 6; n++)
        {
            await h.OpenAsync(BoardCase(n.ToString(System.Globalization.CultureInfo.InvariantCulture)));
        }
        await h.Run(() => h.Panes.Show(null));
        await h.IdleAsync();
        Assert.Equal(6, h.Made.Count);
        Assert.All(h.Made, p => Assert.True(p.Settles == 1 && !p.Closed && p.Abandons == 0));
        Assert.Empty(h.Detached);
    }

    [Fact]
    public async Task ComingBackShowsTheKeptPaneWithItsNote()
    {
        await using var h = await Harness.StartAsync(retryFirst: TimeSpan.FromSeconds(600), retryMax: TimeSpan.FromSeconds(600));
        var c1 = BoardCase("1");
        var p = await h.OpenAsync(c1);
        p.Fallback = false;
        await h.Run(() => h.Panes.Show(BoardCase("2")));
        await h.IdleAsync();
        Assert.Equal(1, p.Settles);
        await h.Run(() => h.Panes.Show(c1));
        Assert.Same(p, await h.LiveAsync());
        Assert.Equal(1, h.MadeFor(c1));
        Assert.True(await h.UnsavedAsync(c1));
    }

    [Fact]
    public async Task TheNoteGoesOnceAKeptPaneSaves()
    {
        await using var h = await Harness.StartAsync();
        var c1 = BoardCase("1");
        var p = await h.OpenAsync(c1);
        p.Results.Add(false);
        // The retry waits until the user is back.
        p.HoldFrom = 2;
        await h.Run(() => h.Panes.Show(BoardCase("2")));
        await h.IdleAsync();
        // The first failed; its retry begins and waits.
        await h.AdvanceClockAsync(TimeSpan.FromMilliseconds(20));
        await h.WhenAsync(() => p.Waiting == 1, "the retry");
        await h.Run(() => h.Panes.Show(c1));
        Assert.True(await h.UnsavedAsync(c1));
        // The retry saves while the user is back in it: the note goes, the
        // pane stays the live one.
        await h.Run(p.Release);
        await h.IdleAsync();
        Assert.False(await h.UnsavedAsync(c1));
        Assert.Same(p, await h.LiveAsync());
        Assert.False(p.Closed);
    }

    // Finding 4: another link, or none, is not proof the draft went.
    [Fact]
    public async Task AChangedLinkSavesThePaneInsteadOfAbandoningIt()
    {
        await using var h = await Harness.StartAsync();
        var p = await h.OpenAsync(BoardCase("1"));
        await h.Run(() => h.Panes.Show(BoardCase("1", draft: "other")));
        await h.IdleAsync();
        Assert.True(p.Closed && p.Settles == 1 && p.Abandons == 0);
        Assert.Empty(h.Toasts);
        // The new link gets its own pane.
        Assert.Equal(new DraftId("other_1"), (await h.Ui.RunAsync(() => h.Panes.LiveKey))?.Draft);
        var q = await h.OpenAsync(BoardCase("2"));
        await h.Run(() => h.Panes.Show(BoardCase("2", draft: null)));
        await h.IdleAsync();
        Assert.True(q.Closed && q.Abandons == 0);
        Assert.Empty(h.Toasts);
    }

    [Fact]
    public async Task OnlyTheDraftControllersLostAbandons()
    {
        await using var h = await Harness.StartAsync();
        var p = await h.OpenAsync(BoardCase("1"));
        p.Holding = true;
        await h.Run(() => h.Panes.Show(BoardCase("1", draft: null)));
        await h.WhenAsync(() => p.Waiting == 1, "the save");
        // The save said draftNotFound: the draft controller abandoned and
        // reported it.
        await h.Run(() =>
        {
            p.IsLost = true;
            p.Fallback = false;
            h.Panes.Ended(p, new End.Lost());
            p.Release();
        });
        await h.IdleAsync();
        Assert.Single(h.Detached);
        Assert.Equal([Board.Text.ReplyRemoved], h.Toasts);
        // No retry for a lost draft.
        await h.AdvanceAsync(TimeSpan.FromSeconds(1));
        Assert.Equal(1, p.Settles);
    }

    // Finding 6.
    [Fact]
    public async Task APaneTakenBackWhileItSavesIsNotReleasedUnderTheUser()
    {
        await using var h = await Harness.StartAsync();
        var c1 = BoardCase("1");
        var p = await h.OpenAsync(c1);
        p.Holding = true;
        await h.Run(() => h.Panes.Show(BoardCase("2")));
        await h.WhenAsync(() => p.Waiting == 1, "the save");
        // Back at once: the same pane, no loading row while it saves.
        await h.Run(() => h.Panes.Show(c1));
        Assert.Same(p, await h.LiveAsync());
        Assert.Equal(1, h.MadeFor(c1));
        await h.Run(p.Release);
        await h.IdleAsync();
        Assert.Same(p, await h.LiveAsync());
        Assert.True(!p.Closed && !h.Detached.Contains(p));
        // Left again later: saved and closed as usual.
        await h.Run(() => h.Panes.Show(null));
        await h.IdleAsync();
        Assert.True(p.Closed);
    }

    [Fact]
    public async Task PanesKeptWithNothingAtStakeAreBounded()
    {
        await using var h = await Harness.StartAsync();
        var kept = new List<FakePane>();
        for (var n = 1; n <= BoardReplyPanes.KeptLimit + 1; n++)
        {
            var p = await h.OpenAsync(BoardCase(n.ToString(System.Globalization.CultureInfo.InvariantCulture)));
            p.IsSending = true;
            await h.Run(() =>
            {
                h.Panes.Show(null);
                p.IsSending = false;
                h.Panes.SendFailed(p);
            });
            await h.IdleAsync();
            kept.Add(p);
        }
        // The oldest is settled once more and closed; the others stay.
        Assert.True(kept[0].Closed);
        Assert.Equal(2, kept[0].Settles);
        Assert.All(kept.Skip(1), p => Assert.True(!p.Closed && p.Settles == 1));
    }

    [Fact]
    public async Task ADiscardUnderWayIsNotSavedOver()
    {
        await using var h = await Harness.StartAsync();
        var p = await h.OpenAsync(BoardCase("1"));
        await h.Run(() =>
        {
            h.Panes.Discarding(p, true);
            // The board drops the link at once (optimistic).
            h.Panes.Show(BoardCase("1", draft: null));
        });
        await h.IdleAsync();
        Assert.Same(p, await h.LiveAsync());
        Assert.Equal(0, p.Settles);
        await h.Run(() => h.Panes.Ended(p, new End.Discarded()));
        Assert.True(p.Closed);
        Assert.Empty(h.Toasts);
    }

    [Fact]
    public async Task AFailedDiscardOfARetiredPaneSavesIt()
    {
        await using var h = await Harness.StartAsync();
        var p = await h.OpenAsync(BoardCase("1"));
        await h.Run(() =>
        {
            h.Panes.Discarding(p, true);
            h.Panes.Show(BoardCase("2"));
        });
        await h.IdleAsync();
        Assert.Equal(0, p.Settles);
        await h.Run(() => h.Panes.Discarding(p, false));
        await h.IdleAsync();
        Assert.True(p.Closed);
        Assert.Equal(1, p.Settles);
    }

    [Fact]
    public async Task ADiscardedLinkThatCameBackLoadsAgain()
    {
        await using var h = await Harness.StartAsync();
        var c1 = BoardCase("1");
        var p = await h.OpenAsync(c1);
        var asked = h.Asked;
        await h.Run(() => h.Panes.Ended(p, new End.Discarded()));
        Assert.True(p.Closed);
        await h.IdleAsync();
        var live = await h.LiveAsync();
        Assert.True(live is not null && !ReferenceEquals(live, p));
        Assert.Equal(asked + 1, h.Asked);
    }

    // The quit.
    [Fact]
    public async Task QuittingSavesEverything()
    {
        await using var h = await Harness.StartAsync();
        var p = await h.OpenAsync(BoardCase("1"));
        Assert.True(await h.FinishAllAsync(TimeSpan.FromSeconds(10)));
        Assert.True(p.Settles == 1 && p.Closed);
    }

    [Fact]
    public async Task QuittingWithAReplyThatCannotBeSavedSaysSo()
    {
        await using var h = await Harness.StartAsync(retryFirst: TimeSpan.FromSeconds(600), retryMax: TimeSpan.FromSeconds(600));
        var c1 = BoardCase("1");
        var p = await h.OpenAsync(c1);
        p.Fallback = false;
        Assert.False(await h.FinishAllAsync(TimeSpan.FromSeconds(10)));
        Assert.True(!p.Closed && p.Abandons == 0);
        Assert.True(await h.Ui.RunAsync(() => h.Panes.HasUnsavedOrUnsent));
        // The user stays: the pane is there again.
        await h.Run(() =>
        {
            h.Panes.Resume();
            h.Panes.Show(c1);
        });
        Assert.Same(p, await h.LiveAsync());
        Assert.True(await h.UnsavedAsync(c1));
    }

    [Fact]
    public async Task QuittingTriesAKeptPaneOnceMore()
    {
        await using var h = await Harness.StartAsync(retryFirst: TimeSpan.FromSeconds(600), retryMax: TimeSpan.FromSeconds(600));
        var p = await h.OpenAsync(BoardCase("1"));
        p.Results.Add(false);
        await h.Run(() => h.Panes.Show(null));
        await h.IdleAsync();
        Assert.Equal(1, p.Settles);
        Assert.True(await h.FinishAllAsync(TimeSpan.FromSeconds(10)));
        Assert.True(p.Settles == 2 && p.Closed);
    }

    [Fact]
    public async Task QuittingWaitsForASendAtMostTheBound()
    {
        await using var h = await Harness.StartAsync();
        var p = await h.OpenAsync(BoardCase("1"));
        p.IsSending = true;
        var finishing = await h.Ui.RunAsync(() => h.Panes.FinishAllAsync(TimeSpan.FromMilliseconds(100)));
        await h.IdleAsync();
        Assert.False(finishing.IsCompleted);
        h.Time.Advance(TimeSpan.FromMilliseconds(99));
        await h.IdleAsync();
        Assert.False(finishing.IsCompleted);
        h.Time.Advance(Ms);
        Assert.False(await finishing);
        Assert.True(p.Settles == 0 && !p.Closed);
    }

    [Fact]
    public async Task QuittingEndsWhenTheSendAnswers()
    {
        await using var h = await Harness.StartAsync();
        var p = await h.OpenAsync(BoardCase("1"));
        p.IsSending = true;
        var finishing = await h.Ui.RunAsync(() => h.Panes.FinishAllAsync(TimeSpan.FromSeconds(10)));
        await h.IdleAsync();
        Assert.False(finishing.IsCompleted);
        await h.Run(() =>
        {
            p.IsSending = false;
            h.Panes.Ended(p, new End.Sent("Message queued for sending"));
        });
        Assert.True(await finishing);
        Assert.Equal(["Message queued for sending"], h.Toasts);
    }

    /// <summary>A pane as the rules see it; used on the UI thread.</summary>
    internal sealed class FakePane(Key key, Action changed) : IBoardReplyPane
    {
        private readonly List<TaskCompletionSource> held = [];

        public Key Key { get; } = key;

        /// <summary>The outcomes of the next settles; past the list, <see cref="Fallback"/>.</summary>
        public List<bool> Results { get; } = [];

        public bool Fallback { get; set; } = true;

        /// <summary>Settles wait for <see cref="Release"/> while set, and so does every settle from the <see cref="HoldFrom"/>th on.</summary>
        public bool Holding { get; set; }

        public int HoldFrom { get; set; } = int.MaxValue;

        public int Settles { get; private set; }

        public int Closes { get; private set; }

        public int Abandons { get; private set; }

        public bool HasUnsavedText { get; set; }

        public bool IsSending { get; set; }

        public bool IsLost { get; set; }

        public string ReplyTitle { get; set; } = "Re: Offer";

        public int Waiting => held.Count;

        public bool Closed => Closes > 0;

        public async Task<bool> SettleAsync()
        {
            Settles++;
            if (Holding || Settles >= HoldFrom)
            {
                var w = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                held.Add(w);
                changed();
                await w.Task;
            }
            var ok = Fallback;
            if (Results.Count > 0)
            {
                ok = Results[0];
                Results.RemoveAt(0);
            }
            HasUnsavedText = !ok;
            changed();
            return ok;
        }

        /// <summary>Lets the held settles go on.</summary>
        public void Release()
        {
            Holding = false;
            HoldFrom = int.MaxValue;
            var h = held.ToList();
            held.Clear();
            h.ForEach(w => w.TrySetResult());
        }

        public void Close()
        {
            Closes++;
            changed();
        }

        public void Abandon() => Abandons++;
    }

    /// <summary>draft.get answers at once.</summary>
    private sealed class DraftGets
    {
        private int asked;

        public int Asked => System.Threading.Volatile.Read(ref asked);

        public string Get(string json)
        {
            System.Threading.Interlocked.Increment(ref asked);
            var p = JsonCoding.Decode<DraftGetParams>(json);
            return JsonCoding.EncodeToString(new DraftGetResult
            {
                Draft = new Draft { Id = p.DraftId, AccountId = p.AccountId, Version = 5, Subject = "Re: Offer", TextBody = "", Local = true },
            });
        }
    }

    private sealed class Harness : IAsyncDisposable
    {
        private readonly UiConditions conditions = new();
        private readonly DraftGets drafts = new();

        private Harness()
        {
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Time { get; } = new();

        public FakeDaemon Fake { get; } = new();

        public RpcClient Client { get; private set; } = null!;

        public BoardReplyEditorController Loader { get; private set; } = null!;

        public BoardReplyPanes Panes { get; private set; } = null!;

        public List<FakePane> Made { get; } = [];

        public List<IBoardReplyPane> Detached { get; } = [];

        public List<IBoardReplyPane> Adopted { get; } = [];

        public List<string> Toasts { get; } = [];

        /// <summary>What the next made pane is set up with.</summary>
        public Action<FakePane> SetUp { get; set; } = _ => { };

        public int Asked => drafts.Asked;

        public static async Task<Harness> StartAsync(TimeSpan? retryFirst = null, TimeSpan? retryMax = null)
        {
            var h = new Harness();
            h.Fake.On(API.DraftGet.Name, h.drafts.Get);
            await h.Fake.StartAsync();
            h.Client = new RpcClient(h.Fake.Path, PortableKeyFilePolicy.Instance);
            await h.Client.ConnectAsync(TestContext.Current.CancellationToken);
            await h.Ui.RunAsync(() =>
            {
                h.Loader = new BoardReplyEditorController(h.Client, pending: h.Pending);
                h.Panes = new BoardReplyPanes(
                    h.Loader,
                    new BoardReplyPanes.Timing(retryFirst ?? TimeSpan.FromMilliseconds(20), retryMax ?? TimeSpan.FromMilliseconds(80)),
                    h.Time,
                    pending: h.Pending)
                {
                    Make = (key, _) =>
                    {
                        var p = new FakePane(key, h.conditions.Changed);
                        h.SetUp(p);
                        h.Made.Add(p);
                        return p;
                    },
                    Detach = p =>
                    {
                        h.Detached.Add(p);
                        h.conditions.Changed();
                    },
                    OnAdopt = h.Adopted.Add,
                    OnToast = h.Toasts.Add,
                    OnChange = h.conditions.Changed,
                };
            });
            return h;
        }

        public Task Run(Action action) => Ui.RunAsync(action);

        /// <summary>Selects <paramref name="c"/> and waits until its pane is live.</summary>
        public async Task<FakePane> OpenAsync(Board.Case c)
        {
            await Run(() => Panes.Show(c));
            await WhenAsync(() => Panes.LiveKey?.CaseId == c.Id, "the pane");
            return (FakePane)(await LiveAsync())!;
        }

        public Task<IBoardReplyPane?> LiveAsync() => Ui.RunAsync(() => Panes.Live);

        /// <summary>Panes made for case <paramref name="c"/>.</summary>
        public int MadeFor(Board.Case c) => Made.Count(p => p.Key.CaseId == c.Id);

        public Task<bool?> UnsavedAsync(Board.Case c) =>
            Ui.RunAsync(() => Panes.SlotFor(c.Id) is Slot.Editor e ? e.Unsaved : (bool?)null);

        public async Task<bool> FinishAllAsync(TimeSpan wait)
        {
            var task = await Ui.RunAsync(() => Panes.FinishAllAsync(wait));
            return await task;
        }

        public Task WhenAsync(Func<bool> condition, string what) => conditions.WhenAsync(Ui, condition, what);

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, Fake);

        /// <summary>Moves the clock on, then waits until nothing is left to happen.</summary>
        public async Task AdvanceAsync(TimeSpan d)
        {
            await IdleAsync();
            Time.Advance(d);
            await IdleAsync();
        }

        /// <summary>Moves the clock on and lets the UI thread run what it posted (a held settle keeps the work busy).</summary>
        public async Task AdvanceClockAsync(TimeSpan d)
        {
            Time.Advance(d);
            await Ui.DrainAsync();
        }

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() =>
            {
                foreach (var p in Made)
                {
                    p.Release();
                }
                Panes.Dispose();
                Loader.Dispose();
            });
            Client.Dispose();
            await Fake.DisposeAsync();
            Ui.Dispose();
        }
    }
}
