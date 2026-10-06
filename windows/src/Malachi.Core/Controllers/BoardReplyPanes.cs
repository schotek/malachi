// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/BoardReplyPanes.swift
// (BoardReplyPanes: Slot, End, Timing, keptLimit, make, detach, onToast,
// onChange, onAdopt, live, panes, slot, show, suspend, resume, retire,
// settle, settled, scheduleRetry, trimKept, discarding, sendFailed, ended,
// finishAll); GTK: ui/internal/boardreply/panes.go (Panes, Slot, End,
// Timing, KeptLimit, Live, LiveKey, All, Slot, Show, Suspend, Resume,
// Discarding, SendFailed, Ended, QuitWait, HasUnsavedOrUnsent, Close).
//
// The rules behind the board's inline reply editor for one main window:
// when a pane is made, shown, kept, settled, closed or abandoned. The loader
// (BoardReplyEditorController) says which draft the selected case links;
// this type decides what happens to the panes that edit such drafts. The
// panes themselves are reached through IBoardReplyPane; the window only
// makes them, puts their views into the detail and forwards their ends
// here. The theme of every rule: what the user typed, and the outcome of a
// Send, are never lost silently.
//
// Windows differences: not generic (Swift's Pane parameter, Go's
// ComparablePane): the panes are IBoardReplyPane, told apart by reference,
// and the host casts what it made. Swift's Slot.pane is Slot.Editor (a
// nested type cannot share the name of the member it would declare) and
// its End is PaneEnd (End is a reserved word elsewhere, CA1716). A
// pane is made from the loader's ComposeParams as Swift's (Go hands the
// api.Draft and the case's reply target over). The back-off and the quit's
// bound run on the injected TimeProvider; a settle that throws counts as
// failed and is reported (docs/windows-port.md §7.5). FinishAllAsync is
// Swift's finishAll (Go splits it into QuitWait and HasUnsavedOrUnsent);
// HasUnsavedOrUnsent and LiveKey are Go's. Dispose is Go's Close.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Boards;
using Malachi.Core.Compose;
using Malachi.Core.Controllers.Infrastructure;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>The inline reply panes of one window over the loader.</summary>
/// <remarks>
/// <para>The rules:</para>
/// <list type="bullet">
/// <item><b>One live pane</b>, the selected case's, made when the loader has
/// the draft ready, or a parked pane of the same case and draft taken
/// back.</item>
/// <item><b>Retired, never dropped</b>: when its case is no longer the one
/// shown (another selection, the case gone, Mail mode, a closed window, quit)
/// or the board shows its case linking another draft or none (not proof the
/// draft went), the live pane is parked and <b>settled</b> (saved). Only the
/// draft controller's draftNotFound (<see cref="PaneEnd.Lost"/>) abandons it.</item>
/// <item><b>Sending</b> panes are never settled, closed or released before
/// the send answered; the outcome is always delivered: success → the queued
/// toast and <see cref="BoardReplyEditorController.Ended"/> wherever the user
/// is; failure → the draft controller's own toast and, out of sight,
/// <see cref="Board.Text.ReplyNotSent"/>, and the pane is kept so the user
/// can come back to it.</item>
/// <item><b>Unsaved text is kept</b>: a parked pane whose settle failed
/// stays, whatever their number, and settles again with back-off
/// (<see cref="Timing"/>); its case shows it again with
/// <see cref="Slot.Editor"/> and <c>Unsaved</c>. Only panes with nothing at
/// stake (kept after a failed send, saved) are bounded
/// (<see cref="KeptLimit"/>).</item>
/// <item><b>A pane taken back while it settles stays</b>: a settle that ends
/// while the user is typing in the pane again does not close it.</item>
/// </list>
/// <para>Create it, and call it, on the UI thread.</para>
/// </remarks>
public sealed partial class BoardReplyPanes : IDisposable
{
    /// <summary>
    /// At most this many panes with nothing at stake are kept (those kept
    /// after a failed send); the oldest is closed beyond that. Panes with
    /// unsaved text are never counted nor closed.
    /// </summary>
    public const int KeptLimit = 3;

    private readonly TimeProvider time;
    private readonly ILogger logger;
    private readonly ControllerScope scope;
    private readonly BoardObserverToken loaderToken;

    // Retired panes, oldest first: settling, waiting for a send, kept.
    private readonly List<Entry> parked = [];

    private Entry? liveEntry;
    private Board.Case? selected;
    private bool suspended;

    // The quit's wait (FinishAllAsync).
    private TaskCompletionSource? quitWait;

    /// <summary>The panes of one window over <paramref name="loader"/>.</summary>
    /// <param name="loader">The window's loader.</param>
    /// <param name="timing">The back-off of a kept pane's settles (<see cref="Timing.Default"/> when null).</param>
    /// <param name="time">The clock of the back-off and of the quit's bound; the system's when null.</param>
    /// <param name="logger">Receives the count of a pane's failed saves.</param>
    /// <param name="pending">Counts the settles; one of its own when null.</param>
    public BoardReplyPanes(
        BoardReplyEditorController loader,
        Timing? timing = null,
        TimeProvider? time = null,
        ILogger<BoardReplyPanes>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(loader);
        Loader = loader;
        Backoff = timing ?? Timing.Default;
        this.time = time ?? TimeProvider.System;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
        loaderToken = loader.Observe(PhaseChanged);
    }

    /// <summary>The window's loader.</summary>
    public BoardReplyEditorController Loader { get; }

    /// <summary>The back-off of a kept pane's settles.</summary>
    public Timing Backoff { get; }

    /// <summary>Makes the pane for a draft the loader has ready (null: none made).</summary>
    public Func<BoardReplyEditorController.Key, ComposeParams, IBoardReplyPane?>? Make { get; set; }

    /// <summary>The pane is gone from here for good: take its view away.</summary>
    public Action<IBoardReplyPane>? Detach { get; set; }

    /// <summary>A short message over the page.</summary>
    public Action<string>? OnToast { get; set; }

    /// <summary>What a slot shows changed.</summary>
    public Action? OnChange { get; set; }

    /// <summary>A pane became the live one (made, or taken back).</summary>
    public Action<IBoardReplyPane>? OnAdopt { get; set; }

    // What is shown

    /// <summary>The live pane.</summary>
    public IBoardReplyPane? Live => liveEntry?.Pane;

    /// <summary>The live pane's key (Go <c>LiveKey</c>).</summary>
    public BoardReplyEditorController.Key? LiveKey => liveEntry?.Key;

    /// <summary>Every pane this type holds, live and parked.</summary>
    public IReadOnlyList<IBoardReplyPane> Panes =>
        [.. (liveEntry is null ? [] : new[] { liveEntry.Pane }), .. parked.Select(e => e.Pane)];

    /// <summary>
    /// Whether a parked pane still holds unsaved text or waits for a send, a
    /// discard or a save to answer (Go's, after <see cref="FinishAllAsync"/>).
    /// </summary>
    public bool HasUnsavedOrUnsent => parked.Any(e => e.Unsaved || e.Pending);

    /// <summary>What the reply slot of case <paramref name="id"/> shows.</summary>
    public Slot SlotFor(Api.BoardCaseId id)
    {
        if (liveEntry is { } e && e.Key.CaseId == id)
        {
            return new Slot.Editor(e.Pane, e.Unsaved);
        }
        return Loader.Current switch
        {
            BoardReplyEditorController.Phase.Loading l when l.Of.CaseId == id => new Slot.Loading(),
            BoardReplyEditorController.Phase.Ready r when r.Of.CaseId == id => new Slot.Loading(),
            BoardReplyEditorController.Phase.Failed f when f.Of.CaseId == id =>
                new Slot.Failed(f.Why == BoardReplyEditorController.Failure.Backend),
            _ => new Slot.None(),
        };
    }

    // The key a case's link gives.
    private static BoardReplyEditorController.Key? KeyOf(Board.Case c) =>
        c.Draft is { } d ? new BoardReplyEditorController.Key(c.Id, c.Account, d.Id) : null;

    // Following the board

    /// <summary>
    /// The selected case (null: none), after every change of the board: the
    /// live pane stays for its case and draft, and is retired otherwise; a
    /// parked pane of the case's draft is taken back; the loader is told.
    /// </summary>
    public void Show(Board.Case? c)
    {
        scope.VerifyAccess();
        selected = c;
        if (suspended)
        {
            return;
        }
        if (liveEntry is { } e)
        {
            if (c is not null && c.Id == e.Key.CaseId)
            {
                if (!e.Ending && !e.Pane.IsSending && c.Draft?.Id != e.Key.Draft)
                {
                    // Another link, or none: saved first; only the daemon's
                    // draftNotFound makes it lost.
                    Retire(e);
                }
            }
            else
            {
                Retire(e);
            }
        }
        if (liveEntry is null && c is not null && KeyOf(c) is { } k && parked.FindIndex(p => p.Key == k) is var i and >= 0)
        {
            Adopt(TakeParked(i));
        }
        Loader.Show(c);
        PhaseChanged();
    }

    /// <summary>
    /// The page left the window (Mail mode, a closed window): the live pane
    /// is retired and nothing loads until <see cref="Resume"/>.
    /// </summary>
    public void Suspend()
    {
        scope.VerifyAccess();
        if (suspended)
        {
            return;
        }
        if (liveEntry is { } e)
        {
            Retire(e);
        }
        suspended = true;
        Loader.Show(null);
    }

    /// <summary>The page is back; <see cref="Show"/> the selected case next.</summary>
    public void Resume() => suspended = false;

    /// <summary>Stops following the loader (the window is closing); the panes still held are left as they are.</summary>
    public void Dispose()
    {
        loaderToken.Cancel();
        scope.Close();
    }

    // The loader's phase changed: a ready draft of the selected case gets its
    // pane (a parked one, or a new one).
    private void PhaseChanged()
    {
        try
        {
            if (suspended || liveEntry is not null || Loader.Current is not BoardReplyEditorController.Phase.Ready ready
                || selected?.Id != ready.Of.CaseId)
            {
                return;
            }
            if (parked.FindIndex(p => p.Key == ready.Of) is var i and >= 0)
            {
                Adopt(TakeParked(i));
                return;
            }
            if (Make?.Invoke(ready.Of, ready.Params) is { } pane)
            {
                Adopt(new Entry(ready.Of, pane));
            }
        }
        finally
        {
            Changed();
        }
    }

    private void Adopt(Entry e)
    {
        // The user's again: retired later, it is settled and closed like any
        // other.
        e.Keep = false;
        liveEntry = e;
        if (OnAdopt is { } onAdopt)
        {
            scope.Guard(() => onAdopt(e.Pane));
        }
    }

    private Entry TakeParked(int i)
    {
        var e = parked[i];
        parked.RemoveAt(i);
        return e;
    }

    // Retiring and settling

    // The live pane's case is no longer the one shown: parked, and saved
    // unless it is sending (then it waits for the send) or discarding.
    private void Retire(Entry e)
    {
        if (liveEntry == e)
        {
            liveEntry = null;
        }
        parked.Add(e);
        if (e.Pane.IsSending)
        {
            e.AwaitingSend = true;
        }
        else if (!e.Ending)
        {
            Settle(e);
        }
        Changed();
    }

    private void Settle(Entry e)
    {
        if (e.Settling)
        {
            return;
        }
        e.CancelRetry();
        e.Settling = true;
        scope.Run(async _ =>
        {
            bool ok;
            try
            {
                ok = await e.Pane.SettleAsync();
            }
#pragma warning disable CA1031 // A settle that fails saved nothing; reported, the pane kept.
            catch (Exception error)
#pragma warning restore CA1031
            {
                scope.Pending.Report(error);
                ok = false;
            }
            if (!scope.IsClosed)
            {
                Settled(e, ok);
            }
        });
    }

    private bool Tracked(Entry e) => liveEntry == e || parked.Contains(e);

    private void Settled(Entry e, bool ok)
    {
        e.Settling = false;
        try
        {
            if (!Tracked(e))
            {
                return;
            }
            if (ok)
            {
                e.Unsaved = false;
                e.Failures = 0;
                if (liveEntry == e)
                {
                    // Taken back while it settled: it stays the user's.
                    Changed();
                    return;
                }
                if (e.Keep)
                {
                    TrimKept();
                    Changed();
                    return;
                }
                parked.Remove(e);
                e.Pane.Close();
                DetachPane(e.Pane);
                Changed();
                return;
            }
            // Lost: its end (End.Lost) has come or is coming.
            if (e.Pane.IsLost)
            {
                return;
            }
            e.Unsaved = true;
            e.Failures++;
            LogKept(logger, e.Failures);
            ScheduleRetry(e);
            Changed();
        }
        finally
        {
            CheckQuit();
        }
    }

    // The next settle of a pane with unsaved text, later each time.
    private void ScheduleRetry(Entry e)
    {
        e.CancelRetry();
        var delay = Backoff.RetryFirst;
        for (var i = 1; i < Math.Max(e.Failures, 1); i++)
        {
            if (delay < Backoff.RetryMax)
            {
                delay *= 2;
            }
        }
        if (delay > Backoff.RetryMax)
        {
            delay = Backoff.RetryMax;
        }
        var stop = CancellationTokenSource.CreateLinkedTokenSource(scope.Lifetime);
        e.Retry = stop;
        // Made now, on the clock, so that a fake clock's next step sees it.
        var wait = Task.Delay(delay, time, stop.Token);
        scope.RunDetached(async _ =>
        {
            try
            {
                await wait;
            }
            catch (OperationCanceledException)
            {
                return;
            }
            if (e.Retry != stop || !Tracked(e) || !e.Unsaved)
            {
                return;
            }
            e.CancelRetry();
            Settle(e);
        });
    }

    // Beyond KeptLimit panes with nothing at stake the oldest is closed
    // (settled once more first: should it hold text after all, it stays).
    private void TrimKept()
    {
        var clean = parked.Where(e => e.Keep && !e.Unsaved && !e.Pending && !e.Pane.HasUnsavedText).ToList();
        if (clean.Count <= KeptLimit)
        {
            return;
        }
        var oldest = clean[0];
        oldest.Keep = false;
        Settle(oldest);
    }

    // What the panes report

    /// <summary>
    /// Discard is under way (true) or failed (false) for
    /// <paramref name="pane"/>: while it runs, a dropped link is its doing and
    /// the pane is not saved.
    /// </summary>
    public void Discarding(IBoardReplyPane pane, bool on)
    {
        scope.VerifyAccess();
        if (EntryOf(pane) is not { } e)
        {
            return;
        }
        e.Ending = on;
        if (!on && liveEntry != e)
        {
            // Retired while it ran: what was typed is saved after all.
            Settle(e);
        }
        CheckQuit();
    }

    /// <summary>
    /// The send of <paramref name="pane"/> failed (the draft controller said
    /// why in its own toast and gave Send back). Out of sight, the user hears
    /// which reply, and the pane is kept for when they come back.
    /// </summary>
    public void SendFailed(IBoardReplyPane pane)
    {
        scope.VerifyAccess();
        if (EntryOf(pane) is not { } e)
        {
            return;
        }
        e.AwaitingSend = false;
        if (liveEntry != e)
        {
            Toast(Board.Text.ReplyNotSent(pane.ReplyTitle));
            e.Keep = true;
            Settle(e);
        }
        Changed();
        CheckQuit();
    }

    /// <summary><paramref name="pane"/> ended (the compose pane's end).</summary>
    public void Ended(IBoardReplyPane pane, PaneEnd end)
    {
        ArgumentNullException.ThrowIfNull(end);
        scope.VerifyAccess();
        if (EntryOf(pane) is not { } e)
        {
            return;
        }
        if (liveEntry == e)
        {
            liveEntry = null;
        }
        parked.Remove(e);
        e.CancelRetry();
        var reload = false;
        switch (end)
        {
            case PaneEnd.Sent sent:
                // "Message queued for sending", or the comment's.
                Toast(sent.Text);
                Loader.Ended(e.Key);
                break;
            case PaneEnd.Lost:
                Toast(Board.Text.ReplyRemoved);
                Loader.Ended(e.Key);
                break;
            default:
                // The board dropped the link already; should the discard
                // fail, the link comes back and the draft loads again.
                reload = Loader.Current.Key == e.Key;
                break;
        }
        e.Pane.Close();
        DetachPane(e.Pane);
        if (reload && !suspended)
        {
            Loader.Show(null);
            Loader.Show(selected);
        }
        Changed();
        CheckQuit();
    }

    private Entry? EntryOf(IBoardReplyPane pane)
    {
        if (liveEntry is { } e && ReferenceEquals(e.Pane, pane))
        {
            return e;
        }
        return parked.FirstOrDefault(p => ReferenceEquals(p.Pane, pane));
    }

    // Quitting

    /// <summary>
    /// The application quits: the live pane is retired, every pane with
    /// something at stake settles again (or its send or discard answers), at
    /// most <paramref name="wait"/> on the clock. True when nothing typed is
    /// left unsaved and no send is unanswered; false: the caller asks before
    /// quitting (and calls <see cref="Resume"/> and <see cref="Show"/> when
    /// the user stays).
    /// </summary>
    public async Task<bool> FinishAllAsync(TimeSpan wait)
    {
        scope.VerifyAccess();
        if (liveEntry is { } live)
        {
            Retire(live);
        }
        suspended = true;
        Loader.Show(null);
        foreach (var e in parked.Where(e => e.Unsaved && !e.Settling).ToList())
        {
            Settle(e);
        }
        if (parked.Any(e => e.Pending))
        {
            var done = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            quitWait = done;
            using var stop = new CancellationTokenSource();
            var bound = Task.Delay(wait, time, stop.Token);
            CheckQuit();
            await Task.WhenAny(done.Task, bound);
            await stop.CancelAsync();
            if (quitWait == done)
            {
                quitWait = null;
            }
        }
        return !parked.Any(e => e.Unsaved || e.Pending);
    }

    private void CheckQuit()
    {
        if (quitWait is { } q && !parked.Any(e => e.Pending))
        {
            quitWait = null;
            q.TrySetResult();
        }
    }

    private void Toast(string text)
    {
        if (OnToast is { } onToast)
        {
            scope.Guard(() => onToast(text));
        }
    }

    private void DetachPane(IBoardReplyPane pane)
    {
        if (Detach is { } detach)
        {
            scope.Guard(() => detach(pane));
        }
    }

    private void Changed()
    {
        if (OnChange is { } onChange)
        {
            scope.Guard(onChange);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "board reply: a suggested reply could not be saved ({Failures}×); kept")]
    private static partial void LogKept(ILogger logger, int failures);

    /// <summary>What a detail's reply slot shows for a case.</summary>
    public abstract record Slot
    {
        // Only the cases below derive from it.
        private Slot()
        {
        }

        /// <summary>Nothing of the panes': Suggest Reply, or the samples' block.</summary>
        public sealed record None : Slot;

        /// <summary>The draft is loading.</summary>
        public sealed record Loading : Slot;

        /// <summary>The draft could not be opened; <paramref name="Retry"/> offers Try Again.</summary>
        /// <param name="Retry">Try Again is offered (a backend failure, not a gone draft).</param>
        public sealed record Failed(bool Retry) : Slot;

        /// <summary>The case's pane (Swift <c>.pane</c>).</summary>
        /// <param name="Pane">The pane.</param>
        /// <param name="Unsaved">What was typed could not be saved yet (the pane keeps trying).</param>
        public sealed record Editor(IBoardReplyPane Pane, bool Unsaved) : Slot;
    }

    /// <summary>How a pane ended (the compose pane's end without the view).</summary>
    public abstract record PaneEnd
    {
        // Only the cases below derive from it.
        private PaneEnd()
        {
        }

        /// <summary>Queued; the text is the confirmation.</summary>
        /// <param name="Text">"Message queued for sending", or the comment's.</param>
        public sealed record Sent(string Text) : PaneEnd;

        /// <summary>Discarded.</summary>
        public sealed record Discarded : PaneEnd;

        /// <summary>Closed.</summary>
        public sealed record Closed : PaneEnd;

        /// <summary>The draft was deleted elsewhere.</summary>
        public sealed record Lost : PaneEnd;
    }

    /// <summary>The back-off of a kept pane's settles (tests shorten it).</summary>
    /// <param name="RetryFirst">The wait before the first retry; doubled after every failure.</param>
    /// <param name="RetryMax">The longest wait between retries.</param>
    public readonly record struct Timing(TimeSpan RetryFirst, TimeSpan RetryMax)
    {
        /// <summary>5 s first, 2 min at most.</summary>
        public static Timing Default => new(TimeSpan.FromSeconds(5), TimeSpan.FromSeconds(120));
    }

    // One pane this type holds, live or parked.
    private sealed class Entry(BoardReplyEditorController.Key key, IBoardReplyPane pane)
    {
        public BoardReplyEditorController.Key Key { get; } = key;

        public IBoardReplyPane Pane { get; } = pane;

        // A settle is under way.
        public bool Settling { get; set; }

        // The next settle of a kept pane with unsaved text.
        public CancellationTokenSource? Retry { get; set; }

        // Settles that failed in a row.
        public int Failures { get; set; }

        // Its last settle failed: it holds unsaved text.
        public bool Unsaved { get; set; }

        // Retired while sending: waits for the outcome.
        public bool AwaitingSend { get; set; }

        // Kept after a failed send: shown again when its case is.
        public bool Keep { get; set; }

        // Discard is under way: a dropped link is its doing.
        public bool Ending { get; set; }

        // Something is still to come before it can be judged.
        public bool Pending => Settling || AwaitingSend || Ending;

        public void CancelRetry()
        {
            if (Retry is { } r)
            {
                Retry = null;
                r.Cancel();
                r.Dispose();
            }
        }
    }
}
