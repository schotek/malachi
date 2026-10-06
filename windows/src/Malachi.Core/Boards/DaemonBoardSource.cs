// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/DaemonBoardSource.swift; Go:
// ui/internal/board/daemon_source.go (DaemonSource).
//
// The board from the daemon (docs/api.md §4.13): board.list for the cases,
// account.list for the accounts' names, board.get for a case's
// conversation when it is selected, and the user's decisions written back
// with board.setState, setDone, remind, archive, unflag, discardDraft and
// setCommitment.
//
// It subscribes to nothing itself: the application's notification fan-out
// calls BoardChanged on notify.boardChanged, AccountsChanged on
// notify.accountsChanged and ConnectionChanged when the connection comes
// and goes; Start loads the first time. A notification is answered after
// the debounce, several in that time with one board.list. One board.list
// is on its way at a time: asked for again meanwhile, it runs once more
// after the reply is applied, so a slow list under a stream of
// notifications still lands. A lost connection drops the reply on its way
// (generation counter). While the daemon cannot be asked the last snapshot
// stays, in phase Unavailable; a list the daemon could not answer is phase
// Failed and is asked again after a back-off (a few seconds, growing to a
// minute), and a daemon without the board is phase Unsupported.
//
// Writes are optimistic: the change is laid over the daemon's data at once
// (one report: the board controller's selection logic takes it as the
// result of its write), the call follows, and its answer replaces the case;
// a refused write is taken back and Failed says why. A list asked for
// before a write's answer never undoes it: a case keeps the newer version,
// a promise the answered state until a list asked for later arrives. The
// conversations are cached by case and version, the ConversationsKept most
// recently used: a case that did not change is not asked for again.
//
// Windows: Swift's onChange, onError, onNotice and onSnapshot are the Action
// properties of the same names, as IBoardSource's; its sleep closure is the
// injected TimeProvider (docs/windows-port.md §3.1), the waits run detached
// as StorageUsageController's. As in Go, times from the wire are taken in
// UTC, and the inline editor's draft.delete counts as a write under way.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Boards;

/// <summary>
/// The board's cases from the daemon (Swift <c>DaemonBoardSource</c>).
/// Create it, and call it, on the UI thread.
/// </summary>
public sealed partial class DaemonBoardSource : IBoardSource, IDisposable
{
    /// <summary>How long a notification waits for others before the board is listed again.</summary>
    public static readonly TimeSpan DefaultDebounce = TimeSpan.FromMilliseconds(300);

    /// <summary>The conversations kept (<c>board.get</c>), the most recently used.</summary>
    public const int ConversationsKept = 50;

    private readonly RpcClient client;
    private readonly ControllerScope scope;
    private readonly ILogger logger;
    private readonly TimeProvider time;
    private readonly TimeSpan debounce;
    private readonly Func<int, TimeSpan> retryDelay;

    // The writes under way, laid over the daemon's data in order.
    private readonly List<(int Token, Func<Board.Snapshot, Board.Snapshot> Apply)> overlays = [];

    // The conversations, by case: the version they are of; and the cases
    // of them, the least recently used first.
    private readonly Dictionary<BoardCaseId, (long Version, IReadOnlyList<Board.CaseMessage> List)> messages = [];
    private readonly List<BoardCaseId> messageOrder = [];

    // The version of a case whose conversation is being loaded, or whose
    // load failed.
    private readonly Dictionary<BoardCaseId, long> loading = [];
    private readonly Dictionary<BoardCaseId, long> failed = [];

    // Promises as board.setCommitment answered them, with the list
    // generation of that moment: a list asked for no later is older than
    // the answer and does not replace them.
    private readonly Dictionary<BoardCommitmentId, (Board.Commitment Commitment, int After)> commitmentPins = [];

    // The daemon's data as last answered (board.list, then each write's and
    // board.get's case), without the writes under way.
    private Base data = new();
    private IReadOnlyList<Board.AccountInfo> accounts = [];
    private Board.Phase phase = Board.Phase.Loading;
    private int nextToken;

    // The latest board.list and account.list asked for; older replies are
    // dropped. The generation on its way, null for none.
    private int listGeneration;
    private int accountGeneration;
    private int? listInFlight;
    private int? accountsInFlight;

    // Another board.list was asked for while one was on its way.
    private bool listAgain;
    private CancellationTokenSource? debounceTimer;

    // The next try after a failed board.list, and how many failed in a row.
    private CancellationTokenSource? retryTimer;
    private int failures;
    private bool stopped;

    // Between Start and Stop: nothing loads on its own outside it.
    private bool started;

    // Writes whose answer has not arrived.
    private int writesInFlight;

    /// <summary>A source over <paramref name="client"/>, on the calling (UI) thread, in phase Loading; it asks for nothing before <see cref="Start"/>.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="debounce">How long a notification waits; <see cref="DefaultDebounce"/> when null.</param>
    /// <param name="retryDelay">The back-off of a failed board.list; <see cref="RetryDelay"/> when null.</param>
    /// <param name="time">The clock of the debounce and the back-off; the system's when null.</param>
    /// <param name="logger">Receives method names and error classes, never a case's text.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    public DaemonBoardSource(
        RpcClient client, TimeSpan? debounce = null, Func<int, TimeSpan>? retryDelay = null, TimeProvider? time = null,
        ILogger<DaemonBoardSource>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        this.client = client;
        this.debounce = debounce ?? DefaultDebounce;
        this.retryDelay = retryDelay ?? RetryDelay;
        this.time = time ?? TimeProvider.System;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>Called after <see cref="Snapshot"/> changed (Swift <c>onChange</c>).</summary>
    public Action? OnChange { get; set; }

    /// <summary>Called with a sentence for a toast when a write was refused (Swift <c>onError</c>).</summary>
    public Action<string>? OnError { get; set; }

    /// <summary>Called with a sentence for a toast about what a write did: Archive (Swift <c>onNotice</c>).</summary>
    public Action<string>? OnNotice { get; set; }

    /// <summary>
    /// Called after <see cref="OnChange"/> with the new snapshot, for the
    /// application's board triage, which is not the board controller of this
    /// window (Swift <c>onSnapshot</c>).
    /// </summary>
    public Action<Board.Snapshot>? OnSnapshot { get; set; }

    /// <summary>What the source knows now.</summary>
    public Board.Snapshot Snapshot { get; private set; } = new() { Phase = Board.Phase.Loading };

    /// <summary>How far the data is.</summary>
    public Board.Phase Phase => Snapshot.Phase;

    /// <summary>
    /// Nothing is waiting or on its way (a debounce, a retry, board.list,
    /// account.list, a write, board.get of a case on the board).
    /// </summary>
    public bool IsIdle =>
        debounceTimer is null && retryTimer is null && listInFlight is null && accountsInFlight is null
        && writesInFlight == 0 && loading.Count == 0;

    /// <summary>The back-off before the <paramref name="attempt"/>th retry of a failed board.list (from 1): 2, 4, 8, 16, 32 seconds, then a minute.</summary>
    public static TimeSpan RetryDelay(int attempt) => TimeSpan.FromSeconds(Math.Min(60, 1 << Math.Min(Math.Max(attempt, 1), 6)));

    // What the application calls

    /// <summary>Loads the accounts and the board.</summary>
    public void Start()
    {
        scope.VerifyAccess();
        stopped = false;
        started = true;
        Refresh();
    }

    /// <summary>Stops: no call is made and no reply of a list or a conversation is taken any more (a write's answer still is).</summary>
    public void Stop()
    {
        scope.VerifyAccess();
        stopped = true;
        started = false;
        CancelTimers();
    }

    /// <summary>notify.boardChanged: lists the board again after the debounce. The accounts it names are not used: one board.list covers them all.</summary>
    public void BoardChanged(BoardChangedNotification? notification = null)
    {
        scope.VerifyAccess();
        if (!started || stopped || debounceTimer is not null)
        {
            return;
        }
        debounceTimer = After(debounce, () => debounceTimer, () =>
        {
            debounceTimer = null;
            LoadList();
        });
    }

    /// <summary>notify.accountsChanged: the accounts' names and the board again.</summary>
    public void AccountsChanged()
    {
        scope.VerifyAccess();
        if (started)
        {
            Refresh();
        }
    }

    /// <summary>The connection came (everything is loaded again) or went (the snapshot stays, in phase Unavailable).</summary>
    public void ConnectionChanged(bool connected)
    {
        scope.VerifyAccess();
        if (!started)
        {
            return;
        }
        if (connected)
        {
            Refresh();
            return;
        }
        CancelTimers();
        // Replies on the way are dropped: they come from the old connection,
        // or fail.
        listGeneration++;
        accountGeneration++;
        listInFlight = null;
        accountsInFlight = null;
        listAgain = false;
        failures = 0;
        SetPhase(Board.Phase.Unavailable);
    }

    /// <summary>Asks for the accounts and the board now (a retry waiting is brought forward).</summary>
    public void Refresh()
    {
        scope.VerifyAccess();
        if (!started || stopped)
        {
            return;
        }
        LoadAccounts();
        LoadList();
    }

    /// <summary>Stops for good: the calls under way are dropped.</summary>
    public void Dispose()
    {
        Stop();
        scope.Close();
    }

    // Conversations

    /// <summary>Loads the case's conversation unless it is there for the case's current version already.</summary>
    public void LoadMessages(BoardCaseId id)
    {
        scope.VerifyAccess();
        if (stopped || data.Cases.FirstOrDefault(c => c.Id == id) is not { } c)
        {
            return;
        }
        if (messages.TryGetValue(id, out var m) && m.Version == c.Version)
        {
            Touch(id);
            return;
        }
        if (loading.TryGetValue(id, out var v) && v == c.Version)
        {
            return;
        }
        loading[id] = c.Version;
        failed.Remove(id);
        Publish();
        var version = c.Version;
        scope.Perform(client, API.BoardGet, new BoardGetParams { CaseId = id }, outcome =>
        {
            if (stopped)
            {
                return;
            }
            if (loading.TryGetValue(id, out var now) && now == version)
            {
                loading.Remove(id);
            }
            if (!outcome.TryGetValue(out var r, out var error))
            {
                LogCallFailed(API.BoardGet.Name, error);
                failed[id] = version;
                Publish();
                return;
            }
            // A case that left the board meanwhile keeps nothing.
            if (!data.Cases.Any(b => b.Id == id))
            {
                return;
            }
            var got = Convert(r.Case);
            messages[id] = (got.Version, [.. r.Messages.Select(Convert)]);
            Touch(id);
            Store(got);
            Publish();
            // The case changed meanwhile: its conversation may have too.
            if (data.Cases.FirstOrDefault(b => b.Id == id) is { } current && current.Version != got.Version)
            {
                LoadMessages(id);
            }
        });
    }

    // The user's decisions

    /// <summary>Moves the case to <paramref name="state"/>; null = back to automatic.</summary>
    public void SetState(Board.State? state, BoardCaseId id) =>
        Write(
            API.BoardSetState, new BoardSetStateParams { CaseId = id, State = state is { } s ? WireState(s) : (BoardState?)null },
            Board.Text.Action.Move, id, c => c with { UserState = state }, r => r.Case);

    /// <summary>Done takes the case off the board (and ends a remind); not done puts it back.</summary>
    public void SetDone(bool done, BoardCaseId id) =>
        Write(
            API.BoardSetDone, new BoardSetDoneParams { CaseId = id, Done = done },
            done ? Board.Text.Action.Done : Board.Text.Action.Reopen, id, c => c.WithDone(done), r => r.Case);

    /// <summary>Hides the case until <paramref name="until"/>; null puts a snoozed case back on the board.</summary>
    public void Remind(DateTimeOffset? until, BoardCaseId id) =>
        Write(
            API.BoardRemind, new BoardRemindParams { CaseId = id, Until = until }, Board.Text.Action.Remind, id,
            c => until is { } u
                ? c with { Visibility = Board.Visibility.Snoozed(u) }
                : c.Visibility.RemindAt is not null ? c with { Visibility = Board.Visibility.Live } : c,
            r => r.Case);

    /// <summary>Moves the case's inbox messages to the archive (where the account can) and marks it done; <see cref="OnNotice"/> says what it did.</summary>
    public void Archive(BoardCaseId id) =>
        Write(
            API.BoardArchive, new BoardArchiveParams { CaseId = id }, Board.Text.Action.Archive, id,
            c => c with { Visibility = Board.Visibility.Done() },
            r =>
            {
                var notice = Board.Text.Archived(r.Archived, r.NoArchive ?? false);
                scope.Guard(() => OnNotice?.Invoke(notice));
                return r.Case;
            });

    /// <summary>Drops the suggested reply (the draft itself, too).</summary>
    public void DiscardDraft(BoardCaseId id) =>
        Write(
            API.BoardDiscardDraft, new BoardDiscardDraftParams { CaseId = id }, Board.Text.Action.DiscardDraft, id,
            c => c with { Draft = null }, r => r.Case);

    /// <summary>
    /// The inline editor's Discard: board.discardDraft while the case links
    /// <paramref name="draft"/> (optimistic, taken back when refused), else
    /// draft.delete of that draft alone. The caller reports a failure (no
    /// <see cref="OnError"/>); after <see cref="Stop"/> it is cancelled at once.
    /// </summary>
    public async Task DiscardDraftAsync(DraftId draft, AccountId account, BoardCaseId id)
    {
        scope.VerifyAccess();
        if (stopped)
        {
            throw new OperationCanceledException();
        }
        writesInFlight++;
        try
        {
            if (Snapshot.Cases.FirstOrDefault(c => c.Id == id)?.Draft?.Id != draft)
            {
                await client.CallAsync(API.DraftDelete, new DraftDeleteParams { AccountId = account, DraftId = draft }, scope.Lifetime);
                return;
            }
            var token = Lay(s => WithCase(s, id, c => c with { Draft = null }));
            try
            {
                var r = await client.CallAsync(API.BoardDiscardDraft, new BoardDiscardDraftParams { CaseId = id }, scope.Lifetime);
                Store(Convert(r.Case));
                Lift(token);
            }
            catch (Exception e) when (e is RpcException or RpcClientException or OperationCanceledException)
            {
                LogCallFailed(API.BoardDiscardDraft.Name, e);
                Lift(token);
                throw;
            }
        }
        finally
        {
            writesInFlight--;
        }
    }

    /// <summary>
    /// board.unflag. Nothing changes optimistically (the rules decide what
    /// the case becomes); the board is listed again once the stars are gone,
    /// so the case moves without waiting for the notification.
    /// </summary>
    public void Unflag(BoardCaseId id) =>
        Write(
            API.BoardUnflag, new BoardUnflagParams { CaseId = id }, Board.Text.Action.Unflag, id, c => c,
            r =>
            {
                Refresh();
                return r.Case;
            });

    /// <summary>Ticks a promise off (or reopens it).</summary>
    public void SetCommitmentDone(bool done, BoardCommitmentId id)
    {
        scope.VerifyAccess();
        if (stopped || !Snapshot.Commitments.Any(k => k.Id == id))
        {
            return;
        }
        var state = done ? Board.CommitmentState.Done : Board.CommitmentState.Open;
        var token = Lay(s => s with
        {
            Commitments = [.. s.Commitments.Select(k => k.Id == id ? k with { State = state } : k)],
        });
        writesInFlight++;
        scope.Perform(
            client, API.BoardSetCommitment, new BoardSetCommitmentParams { CommitmentId = id, Done = done }, outcome =>
            {
                writesInFlight--;
                if (!outcome.TryGetValue(out var r, out var error))
                {
                    LogCallFailed(API.BoardSetCommitment.Name, error);
                    Lift(token);
                    var text = Board.Text.Failed(Board.Text.Action.Commitment, error);
                    scope.Guard(() => OnError?.Invoke(text));
                    return;
                }
                var k = Convert(r.Commitment);
                data = data with { Commitments = [.. data.Commitments.Select(b => b.Id == k.Id ? k : b)] };
                // A list on its way, or asked for before now, is older.
                commitmentPins[k.Id] = (k, listGeneration);
                Lift(token);
            });
    }

    // From the wire

    /// <summary>The wire state of a state.</summary>
    public static BoardState WireState(Board.State s) => s switch
    {
        Board.State.Hot => BoardState.Hot,
        Board.State.You => BoardState.You,
        Board.State.Them => BoardState.Them,
        _ => BoardState.Info,
    };

    /// <summary>The state of a wire state; null for none or one this client does not know.</summary>
    public static Board.State? StateOf(BoardState? s) => s?.Value switch
    {
        BoardState.Hot => Board.State.Hot,
        BoardState.You => Board.State.You,
        BoardState.Them => Board.State.Them,
        BoardState.Info => Board.State.Info,
        _ => null,
    };

    /// <summary>A case from the wire.</summary>
    public static Board.Case Convert(BoardCase c)
    {
        ArgumentNullException.ThrowIfNull(c);
        Board.Visibility visibility = c.Visibility.Value switch
        {
            BoardVisibility.Done => Board.Visibility.Done(c.DoneAt?.ToUniversalTime()),
            BoardVisibility.Snoozed when c.RemindAt is { } at => Board.Visibility.Snoozed(at.ToUniversalTime()),
            _ => Board.Visibility.Live,
        };
        return new Board.Case
        {
            Id = c.Id,
            Account = c.AccountId,
            Thread = c.ThreadId,
            Person = Format.DisplayName(c.Person),
            Date = c.Date.ToUniversalTime(),
            Subject = c.Subject,
            Snippet = c.Snippet,
            Unread = c.Unread,
            HasAttachments = c.HasAttachments,
            MessageCount = c.MessageCount,
            Issue = c.Issue is { } issue ? new Board.IssueInfo(issue.Key, issue.Status, Jira.StyleOf(issue.StatusCategory)) : null,
            // A state this client does not know reads as for reading.
            RuleState = StateOf(c.RuleState) ?? Board.State.Info,
            RuleReason = c.RuleReason,
            Annotation = c.Annotation is { } a ? Convert(a) : null,
            UserState = StateOf(c.UserState),
            Visibility = visibility,
            Reply = string.IsNullOrEmpty(c.ReplyMessageId.Value) ? null : new Board.ReplyTarget(c.ReplyMessageId, c.ReplyFolderId),
            LatestMessage = string.IsNullOrEmpty(c.LatestMessageId.Value) ? null : (MessageId?)c.LatestMessageId,
            CanArchive = c.CanArchive,
            Draft = c.Draft is { } d ? new Board.DraftLink(d.DraftId, d.Text) : null,
            Version = c.Version,
        };
    }

    /// <summary>An annotation from the wire.</summary>
    public static Board.Annotation Convert(BoardAnnotation a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return new Board.Annotation
        {
            State = StateOf(a.State),
            Title = a.Title,
            Summary = a.Summary,
            Why = a.Why,
            Due = a.Due?.At.ToUniversalTime(),
            DueQuote = a.Due?.Quote ?? "",
            DueMessage = a.Due?.MessageId,
            Tasks = a.Tasks,
            Source = a.Source,
            At = a.At.ToUniversalTime(),
            Stale = a.Stale ?? false,
        };
    }

    /// <summary>A commitment from the wire; closed, or a state this client does not know, is not shown.</summary>
    public static Board.Commitment Convert(BoardCommitment k)
    {
        ArgumentNullException.ThrowIfNull(k);
        return new Board.Commitment
        {
            Id = k.Id,
            CaseId = k.CaseId,
            Text = k.Text,
            Quote = k.Quote,
            Due = k.Due?.ToUniversalTime(),
            MessageId = k.MessageId,
            State = k.State.Value switch
            {
                BoardCommitmentState.Open => Board.CommitmentState.Open,
                BoardCommitmentState.Done => Board.CommitmentState.Done,
                _ => Board.CommitmentState.Closed,
            },
        };
    }

    /// <summary>A member of a conversation from the wire.</summary>
    public static Board.CaseMessage Convert(BoardMessage m)
    {
        ArgumentNullException.ThrowIfNull(m);
        return new Board.CaseMessage
        {
            Id = m.Id,
            Folder = m.FolderId,
            From = Format.DisplayName(m.From),
            Date = m.Date.ToUniversalTime(),
            Text = m.Text,
            Mine = m.Mine,
            Trimmed = m.Trimmed ?? false,
        };
    }

    /// <summary>A run from the wire.</summary>
    public static Board.Run Convert(BoardRun r)
    {
        ArgumentNullException.ThrowIfNull(r);
        return new Board.Run
        {
            Model = r.Source,
            Date = (r.EndedAt ?? r.At).ToUniversalTime(),
            Annotated = r.Annotated,
            Running = r.EndedAt is null,
            Error = r.Error?.Value,
            Trigger = r.Trigger.Value ?? "",
            Started = r.At.ToUniversalTime(),
        };
    }

    /// <summary>An account as the board names it: the sidebar's label and kind capsule, and whether a reply can be written in it.</summary>
    public static Board.AccountInfo Convert(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return new Board.AccountInfo(
            a.Id, FolderTree.AccountLabel(a), FolderTree.AccountHeaderBadge(a), a.Can(Capability.Reply) || a.Can(Capability.Comment));
    }

    // Loading

    private void LoadList()
    {
        if (!started || stopped)
        {
            return;
        }
        if (listInFlight is not null)
        {
            listAgain = true;
            return;
        }
        CancelTimer(ref retryTimer);
        listGeneration++;
        var generation = listGeneration;
        listInFlight = generation;
        scope.Perform(client, API.BoardList, new BoardListParams(), outcome =>
        {
            if (listInFlight == generation)
            {
                listInFlight = null;
            }
            if (stopped || generation != listGeneration)
            {
                return;
            }
            if (outcome.TryGetValue(out var r, out var error))
            {
                failures = 0;
                Apply(r, generation);
            }
            else
            {
                LogCallFailed(API.BoardList.Name, error);
                ListFailed(error!);
            }
            if (listAgain)
            {
                listAgain = false;
                LoadList();
            }
        });
    }

    // Why board.list failed decides the phase: not connected (the reconnect
    // lists again), a daemon without the board, or anything else, which is
    // asked again after the back-off.
    private void ListFailed(Exception error)
    {
        if (error is RpcClientException { Error.Kind: ClientErrorKind.NotConnected or ClientErrorKind.Disconnected })
        {
            SetPhase(Board.Phase.Unavailable);
            return;
        }
        if (error is RpcException { Code.Value: ErrorCode.MethodNotFound or ErrorCode.NotImplemented })
        {
            SetPhase(Board.Phase.Unsupported);
            return;
        }
        SetPhase(Board.Phase.Failed);
        failures++;
        CancelTimer(ref retryTimer);
        retryTimer = After(retryDelay(failures), () => retryTimer, () =>
        {
            retryTimer = null;
            LoadList();
        });
    }

    private void LoadAccounts()
    {
        if (!started || stopped)
        {
            return;
        }
        accountGeneration++;
        var generation = accountGeneration;
        accountsInFlight = generation;
        scope.Perform(client, API.AccountList, new EmptyParams(), outcome =>
        {
            if (accountsInFlight == generation)
            {
                accountsInFlight = null;
            }
            if (stopped || generation != accountGeneration || !outcome.TryGetValue(out var r, out _))
            {
                return;
            }
            accounts = [.. r.Accounts.Where(a => a.Enabled).Select(Convert)];
            Publish();
        });
    }

    // Takes board.list's answer, asked for as generation.
    private void Apply(BoardListResult r, int generation)
    {
        var known = new Dictionary<BoardCaseId, Board.Case>();
        foreach (var c in data.Cases)
        {
            known.TryAdd(c.Id, c);
        }
        var cases = new List<Board.Case>(r.Cases.Count);
        foreach (var wire in r.Cases)
        {
            var c = Convert(wire);
            // A list asked for before a write answered can be older than the
            // write's case: the newer version stays.
            cases.Add(known.TryGetValue(c.Id, out var k) && k.Version > c.Version ? k : c);
        }
        // Likewise a promise board.setCommitment answered after this list was
        // asked for; a list asked for later has the last word.
        var commitments = r.Commitments.Select(Convert).ToList();
        foreach (var (id, pin) in commitmentPins.ToList())
        {
            if (generation > pin.After)
            {
                commitmentPins.Remove(id);
                continue;
            }
            var i = commitments.FindIndex(k => k.Id == id);
            if (i >= 0)
            {
                commitments[i] = pin.Commitment;
            }
        }
        data = new Base
        {
            Cases = cases,
            Commitments = commitments,
            Annotated = r.Assistant,
            Run = r.Triage.LastRun is { } run ? Convert(run) : null,
            Triage = new Board.Triage
            {
                Queue = r.Triage.Queue,
                AnnotatedToday = r.Triage.AnnotatedTodayAuto,
                Usage24h = r.Triage.Usage24h,
            },
            Truncated = r.Truncated ?? false,
        };
        var ids = cases.Select(c => c.Id).ToHashSet();
        foreach (var id in messages.Keys.Where(id => !ids.Contains(id)).ToList())
        {
            messages.Remove(id);
        }
        messageOrder.RemoveAll(id => !ids.Contains(id));
        foreach (var id in loading.Keys.Where(id => !ids.Contains(id)).ToList())
        {
            loading.Remove(id);
        }
        foreach (var id in failed.Keys.Where(id => !ids.Contains(id)).ToList())
        {
            failed.Remove(id);
        }
        phase = !r.Enabled ? Board.Phase.Off : r.Ready ? Board.Phase.Ready : Board.Phase.Preparing;
        Publish();
    }

    private void SetPhase(Board.Phase p)
    {
        if (phase == p)
        {
            return;
        }
        phase = p;
        Publish();
    }

    // Builds the snapshot from the daemon's data, the conversations and the
    // writes under way, and reports it when it changed.
    private void Publish()
    {
        var s = new Board.Snapshot
        {
            Accounts = accounts,
            Cases = [.. data.Cases.Select(c =>
                messages.TryGetValue(c.Id, out var m) ? c with { Messages = m.List }
                : !loading.ContainsKey(c.Id) && failed.ContainsKey(c.Id) ? c with { MessagesFailed = true }
                : c)],
            Commitments = data.Commitments,
            Annotated = data.Annotated,
            Run = data.Run,
            Phase = phase,
            Triage = data.Triage,
            Truncated = data.Truncated,
        };
        foreach (var o in overlays)
        {
            s = o.Apply(s);
        }
        if (s.Equals(Snapshot))
        {
            return;
        }
        Snapshot = s;
        scope.Guard(() => OnChange?.Invoke());
        scope.Guard(() => OnSnapshot?.Invoke(s));
    }

    // Replaces the daemon's case with c unless a newer one is there.
    private void Store(Board.Case c)
    {
        var i = data.Cases.FindIndex(b => b.Id == c.Id);
        if (i < 0 || c.Version < data.Cases[i].Version)
        {
            return;
        }
        data = data with { Cases = [.. data.Cases.Select((b, j) => j == i ? c : b)] };
    }

    // Marks id's conversation as the most recently used and forgets the
    // least recently used beyond ConversationsKept.
    private void Touch(BoardCaseId id)
    {
        messageOrder.Remove(id);
        messageOrder.Add(id);
        while (messageOrder.Count > ConversationsKept)
        {
            messages.Remove(messageOrder[0]);
            messageOrder.RemoveAt(0);
        }
    }

    // Lays change over case id at once, calls method, and then puts the case
    // the daemon answered (done) in place of the change, or takes the change
    // back and reports the error. An unknown case is left alone.
    private void Write<TParams, TResult>(
        RpcMethod<TParams, TResult> method, TParams parameters, Board.Text.Action action, BoardCaseId id,
        Func<Board.Case, Board.Case> change, Func<TResult, BoardCase> done)
    {
        scope.VerifyAccess();
        if (stopped || !Snapshot.Cases.Any(c => c.Id == id))
        {
            return;
        }
        var token = Lay(s => WithCase(s, id, change));
        writesInFlight++;
        scope.Perform(client, method, parameters, outcome =>
        {
            writesInFlight--;
            if (outcome.TryGetValue(out var r, out var error))
            {
                Store(Convert(done(r)));
                Lift(token);
                return;
            }
            LogCallFailed(method.Name, error);
            Lift(token);
            var text = Board.Text.Failed(action, error);
            scope.Guard(() => OnError?.Invoke(text));
        });
    }

    private static Board.Snapshot WithCase(Board.Snapshot s, BoardCaseId id, Func<Board.Case, Board.Case> change) =>
        s.Cases.Any(c => c.Id == id) ? s with { Cases = [.. s.Cases.Select(c => c.Id == id ? change(c) : c)] } : s;

    // Adds an overlay and reports the snapshot with it.
    private int Lay(Func<Board.Snapshot, Board.Snapshot> apply)
    {
        nextToken++;
        overlays.Add((nextToken, apply));
        Publish();
        return nextToken;
    }

    // Removes an overlay and reports what is left.
    private void Lift(int token)
    {
        overlays.RemoveAll(o => o.Token == token);
        Publish();
    }

    private void CancelTimers()
    {
        CancelTimer(ref debounceTimer);
        CancelTimer(ref retryTimer);
    }

    private static void CancelTimer(ref CancellationTokenSource? timer)
    {
        timer?.Cancel();
        timer = null;
    }

    // Runs fire after delay on the UI thread unless the timer was cancelled
    // or replaced meanwhile (current reads the field that holds it).
    private CancellationTokenSource After(TimeSpan delay, Func<CancellationTokenSource?> current, Action fire)
    {
        var cts = new CancellationTokenSource();
        scope.RunDetached(async lifetime =>
        {
            try
            {
                using var linked = CancellationTokenSource.CreateLinkedTokenSource(lifetime, cts.Token);
                await Task.Delay(delay, time, linked.Token);
            }
            catch (OperationCanceledException) when (cts.IsCancellationRequested)
            {
                // Cancelled: a reconnect, Stop, or a list asked for at once.
                cts.Dispose();
                return;
            }
            if (current() != cts)
            {
                cts.Dispose();
                return;
            }
            cts.Dispose();
            fire();
        });
        return cts;
    }

    private void LogCallFailed(string method, Exception? error)
    {
        if (logger.IsEnabled(LogLevel.Information))
        {
            var (kind, daemonError) = RpcErrorText.Classify(error);
            LogFailed(logger, method, kind, daemonError?.Code.Value ?? 0);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "{Method} failed: {Kind} {Code}")]
    private static partial void LogFailed(ILogger logger, string method, RpcErrorText.FailureKind kind, int code);

    // The daemon's side of the snapshot.
    private sealed record Base
    {
        public List<Board.Case> Cases { get; init; } = [];

        public List<Board.Commitment> Commitments { get; init; } = [];

        public bool Annotated { get; init; }

        public Board.Run? Run { get; init; }

        public Board.Triage Triage { get; init; } = new();

        public bool Truncated { get; init; }
    }
}
