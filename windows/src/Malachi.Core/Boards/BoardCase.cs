// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardCase.swift (the types and
// the rules of the state; the cleaning is Board.Clean.cs); GTK:
// ui/internal/board/case.go (KnownReasons, AccountInfo, IssueInfo,
// ReplyTarget, Visibility, CaseMessage, Annotation, DraftLink, Case,
// Commitment, Run, TriageInfo, Phase, Snapshot, AnnotationOf, StateOf,
// StateSourceOf) and text.go (Source).
//
// The board's input: the cases a source hands over (a conversation or an
// issue with the state it is in, what the assistant made of it and what the
// user decided), the user's commitments the assistant found. A case is
// mail, so every string here is hostile input: the view model shows only
// what CleanLine and CleanBlock let through.
//
// Swift's value types are records with init members; a change is a with
// expression. A record compares an IReadOnlyList by reference, so the
// records holding lists compare them by value (SameList), as Swift's arrays
// and Go's reflect.DeepEqual do. The case id is the wire's BoardCaseId (Go
// aliases api.BoardCaseID; Swift's Board.CaseID wraps the same string), a
// commitment's id the wire's BoardCommitmentId (Go; Swift a String).

using System;
using System.Collections.Generic;
using System.Diagnostics.CodeAnalysis;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>
    /// Where a case stands. The declaration order is the display order: the
    /// sections, the columns and the tiles follow it (<see cref="States"/>).
    /// </summary>
    public enum State
    {
        /// <summary>Needs the user now (a deadline, an escalation).</summary>
        Hot,

        /// <summary>Waits for the user's answer.</summary>
        You,

        /// <summary>The user waits for someone else.</summary>
        Them,

        /// <summary>Nothing to do; for reading.</summary>
        Info,
    }

    /// <summary>Who decided a case's state (<see cref="StateSource"/>).</summary>
    public enum StateSourceKind
    {
        /// <summary>The daemon's rules; the assistant has not looked at the case.</summary>
        Rules,

        /// <summary>The rules, and the assistant agreed.</summary>
        AssistantKept,

        /// <summary>The assistant changed the rules' state (<see cref="StateSource.From"/>).</summary>
        AssistantChanged,

        /// <summary>The user moved the case.</summary>
        User,

        /// <summary>The rules; the assistant is off.</summary>
        AssistantOff,
    }

    /// <summary>Where a case is listed (<see cref="Visibility"/>).</summary>
    public enum VisibilityKind
    {
        /// <summary>On the board.</summary>
        Live,

        /// <summary>The user marked it done.</summary>
        Done,

        /// <summary>Hidden until <see cref="Visibility.At"/>, then live again (the source reports that).</summary>
        Snoozed,
    }

    /// <summary>Where a commitment stands.</summary>
    public enum CommitmentState
    {
        /// <summary>Open: the only ones shown.</summary>
        Open,

        /// <summary>The user ticked it off.</summary>
        Done,

        /// <summary>The daemon closed it (the user replied, the case was done).</summary>
        Closed,
    }

    /// <summary>How far the source's data is.</summary>
    public enum Phase
    {
        /// <summary>Nothing has arrived yet.</summary>
        Loading,

        /// <summary>The daemon still evaluates the mail for the first time: the cases may be partial.</summary>
        Preparing,

        /// <summary>The cases are whole.</summary>
        Ready,

        /// <summary>The daemon cannot be asked; the cases are the last ones known.</summary>
        Unavailable,

        /// <summary>
        /// The daemon was asked and could not list the board (a timeout, a
        /// storage error); the cases are the last ones known and the source
        /// asks again after a while.
        /// </summary>
        Failed,

        /// <summary>The daemon has no board (<c>methodNotFound</c>: an older backend).</summary>
        Unsupported,

        /// <summary>The board is turned off in the daemon: no cases.</summary>
        Off,
    }

    /// <summary>The states in their display order (Swift <c>State.allCases</c>, Go <c>States</c>).</summary>
    public static IReadOnlyList<State> States { get; } = [State.Hot, State.You, State.Them, State.Info];

    /// <summary>
    /// The rule codes of docs/api.md §4.13 this client has a text of its own
    /// for (<see cref="Text.Reason"/>); any other code reads
    /// <see cref="Text.ReasonUnknown"/> (Go <c>KnownReasons</c>, Swift
    /// <c>BoardReason.known</c>).
    /// </summary>
    public static IReadOnlyList<BoardReason> KnownReasons { get; } =
    [
        BoardReason.HotImportant, BoardReason.HotFlagged, BoardReason.YouAddressed, BoardReason.YouRepliedToYou,
        BoardReason.ThemReplied, BoardReason.ThemAsked, BoardReason.InfoCcOnly, BoardReason.InfoNotAddressed,
        BoardReason.InfoUnknownSender, BoardReason.InfoYourNote, BoardReason.JiraYourComment, BoardReason.JiraAssigned,
        BoardReason.JiraReporter, BoardReason.JiraCommented, BoardReason.JiraWatching, BoardReason.Kept,
    ];

    /// <summary>Who decided a case's state (<see cref="StateSourceOf"/>).</summary>
    /// <param name="Kind">Who.</param>
    /// <param name="From">The rules' state the assistant changed; null but for <see cref="StateSourceKind.AssistantChanged"/>.</param>
    public readonly record struct StateSource(StateSourceKind Kind, State? From = null)
    {
        /// <summary>The daemon's rules; the assistant has not looked at the case.</summary>
        public static StateSource Rules => new(StateSourceKind.Rules);

        /// <summary>The rules, and the assistant agreed.</summary>
        public static StateSource AssistantKept => new(StateSourceKind.AssistantKept);

        /// <summary>The user moved the case.</summary>
        public static StateSource User => new(StateSourceKind.User);

        /// <summary>The rules; the assistant is off.</summary>
        public static StateSource AssistantOff => new(StateSourceKind.AssistantOff);

        /// <summary>The assistant changed the rules' state <paramref name="from"/>.</summary>
        public static StateSource AssistantChanged(State from) => new(StateSourceKind.AssistantChanged, from);
    }

    /// <summary>An account a case belongs to, as the board names it (Swift's init order).</summary>
    /// <param name="Id">The account.</param>
    /// <param name="Name">Its name.</param>
    /// <param name="Badge">The kind capsule ("JIRA", "M365"; <c>FolderTree.AccountHeaderBadge</c>).</param>
    /// <param name="CanReply">
    /// A reply can be written in it: a mail account's reply, an issue
    /// tracker's comment (the reply or comment capability).
    /// </param>
    public sealed record AccountInfo(AccountId Id, string Name, string Badge = "", bool CanReply = true);

    /// <summary>The issue behind a case of a Jira account.</summary>
    /// <param name="Key">The issue's key ("DEMO-14").</param>
    /// <param name="Status">Its status's name.</param>
    /// <param name="Style">The status category's colour.</param>
    public sealed record IssueInfo(string Key, string Status, JiraStatusStyle Style);

    /// <summary>
    /// The message a reply to a case answers and the folder it is in
    /// (<c>replyMessageId</c>, <c>replyFolderId</c>): what Reply opens and
    /// Show in Mail selects. On a Jira account the reply is a comment.
    /// </summary>
    /// <param name="Message">The message.</param>
    /// <param name="Folder">Its folder.</param>
    public sealed record ReplyTarget(MessageId Message, FolderId Folder);

    /// <summary>Where a case is listed. The default is live.</summary>
    public readonly record struct Visibility
    {
        private Visibility(VisibilityKind kind, DateTimeOffset? at)
        {
            Kind = kind;
            At = at;
        }

        /// <summary>On the board.</summary>
        public static Visibility Live => default;

        /// <summary>Where.</summary>
        public VisibilityKind Kind { get; }

        /// <summary>
        /// When the case was marked done (null when the source does not know)
        /// or when it comes back; null while live.
        /// </summary>
        public DateTimeOffset? At { get; }

        /// <summary>On the board.</summary>
        public bool IsLive => Kind == VisibilityKind.Live;

        /// <summary>Marked done.</summary>
        public bool IsDone => Kind == VisibilityKind.Done;

        /// <summary>The remind date while snoozed; null otherwise.</summary>
        public DateTimeOffset? RemindAt => Kind == VisibilityKind.Snoozed ? At : null;

        /// <summary>The user marked it done, <paramref name="at"/> when the source knows.</summary>
        public static Visibility Done(DateTimeOffset? at = null) => new(VisibilityKind.Done, at);

        /// <summary>Hidden until <paramref name="until"/>.</summary>
        public static Visibility Snoozed(DateTimeOffset until) => new(VisibilityKind.Snoozed, until);
    }

    /// <summary>One message of a case's conversation (<c>board.get</c>).</summary>
    public sealed record CaseMessage
    {
        /// <summary>The message; null for the invented samples.</summary>
        public MessageId? Id { get; init; }

        /// <summary>Its folder; null for the samples.</summary>
        public FolderId? Folder { get; init; }

        /// <summary>The sender's name.</summary>
        public required string From { get; init; }

        /// <summary>When it was sent.</summary>
        public required DateTimeOffset Date { get; init; }

        /// <summary>Plain text, never markup.</summary>
        public required string Text { get; init; }

        /// <summary>The user wrote it.</summary>
        public bool Mine { get; init; }

        /// <summary>The quoted history, the signature or the rest past the cap was cut off <see cref="Text"/>.</summary>
        public bool Trimmed { get; init; }
    }

    /// <summary>
    /// What the assistant made of a case. Text an assistant wrote: shown
    /// only as plain text and always as the assistant's.
    /// </summary>
    public sealed record Annotation
    {
        /// <summary>The assistant's state; null leaves the state to the rules.</summary>
        public State? State { get; init; }

        /// <summary>A short title of its own; the subject otherwise.</summary>
        public required string Title { get; init; }

        /// <summary>The summary.</summary>
        public string Summary { get; init; } = "";

        /// <summary>Why the case is where it is.</summary>
        public string Why { get; init; } = "";

        /// <summary>The deadline it found.</summary>
        public DateTimeOffset? Due { get; init; }

        /// <summary>The sentence the due date comes from.</summary>
        public string DueQuote { get; init; } = "";

        /// <summary>The message the sentence is in.</summary>
        public MessageId? DueMessage { get; init; }

        /// <summary>The tasks and questions.</summary>
        public IReadOnlyList<string> Tasks { get; init => field = value ?? []; } = [];

        /// <summary>Names the assistant (a model name); "" when unknown.</summary>
        public string Source { get; init; } = "";

        /// <summary>When it was made; null for the samples.</summary>
        public DateTimeOffset? At { get; init; }

        /// <summary>
        /// A message was added, removed or got its body since: none of it
        /// counts (<see cref="AnnotationOf"/>), only the case's draft stays.
        /// </summary>
        public bool Stale { get; init; }

        /// <summary>Whether both say the same, the tasks compared in order.</summary>
        public bool Equals(Annotation? other) =>
            other is not null && State == other.State && Title == other.Title && Summary == other.Summary
            && Why == other.Why && Due == other.Due && DueQuote == other.DueQuote && DueMessage == other.DueMessage
            && SameList(Tasks, other.Tasks) && Source == other.Source && At == other.At && Stale == other.Stale;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(State, Title, Due, Tasks.Count, Stale);
    }

    /// <summary>
    /// The suggested reply an annotation linked to a case: a real draft of
    /// the case's account, never sent by itself. It outlives a stale
    /// annotation.
    /// </summary>
    /// <param name="Id">The draft.</param>
    /// <param name="Text">Its plain text.</param>
    public sealed record DraftLink(DraftId Id, string Text);

    /// <summary>A case as the source has it.</summary>
    [SuppressMessage("Naming", "CA1716", Justification = "Swift's and Go's name (Board.Case, board.Case); no Visual Basic code uses the board.")]
    public sealed record Case
    {
        /// <summary>The case.</summary>
        public required BoardCaseId Id { get; init; }

        /// <summary>Its account.</summary>
        public required AccountId Account { get; init; }

        /// <summary>The case's thread now (a merge can change it, <see cref="Id"/> stays); null for the samples.</summary>
        public ThreadId? Thread { get; init; }

        /// <summary>The other party (the sender, the reporter).</summary>
        public required string Person { get; init; }

        /// <summary>The latest activity.</summary>
        public required DateTimeOffset Date { get; init; }

        /// <summary>The subject.</summary>
        public required string Subject { get; init; }

        /// <summary>The newest message's text, short.</summary>
        public string Snippet { get; init; } = "";

        /// <summary>Unread mail in it.</summary>
        public bool Unread { get; init; }

        /// <summary>Attachments in it.</summary>
        public bool HasAttachments { get; init; }

        /// <summary>The messages that count.</summary>
        public int MessageCount { get; init; } = 1;

        /// <summary>The issue of a Jira account's case.</summary>
        public IssueInfo? Issue { get; init; }

        /// <summary>The daemon's rules' state.</summary>
        public required State RuleState { get; init; }

        /// <summary>The code of the rule (<see cref="Text.Reason"/>; an open set).</summary>
        public BoardReason RuleReason { get; init; } = new("");

        /// <summary>What the assistant made of it.</summary>
        public Annotation? Annotation { get; init; }

        /// <summary>The user's own choice; null = automatic.</summary>
        public State? UserState { get; init; }

        /// <summary>Where it is listed.</summary>
        public Visibility Visibility { get; init; }

        /// <summary>What a reply answers; null for the samples.</summary>
        public ReplyTarget? Reply { get; init; }

        /// <summary>The newest message that counts; null for the samples.</summary>
        public MessageId? LatestMessage { get; init; }

        /// <summary>Archive would move messages (else it only marks the case done).</summary>
        public bool CanArchive { get; init; }

        /// <summary>The suggested reply.</summary>
        public DraftLink? Draft { get; init; }

        /// <summary>The conversation, oldest first; null until loaded (<see cref="IBoardSource.LoadMessages"/>).</summary>
        public IReadOnlyList<CaseMessage>? Messages { get; init; }

        /// <summary>Loading the conversation failed (and nothing was loaded before).</summary>
        public bool MessagesFailed { get; init; }

        /// <summary>Changes whenever the case does (the daemon's <c>version</c>).</summary>
        public long Version { get; init; }

        /// <summary>Marked done.</summary>
        public bool Done => Visibility.IsDone;

        /// <summary>
        /// The case moved to done (when unknown) or back on the board; the
        /// same case when it already is (Swift's setter of <c>done</c>).
        /// </summary>
        public Case WithDone(bool done) =>
            done == Visibility.IsDone ? this : this with { Visibility = done ? Visibility.Done() : Visibility.Live };

        /// <summary>Whether both are the same case in the same shape, the messages compared in order.</summary>
        public bool Equals(Case? other) =>
            other is not null && Id == other.Id && Account == other.Account && Thread == other.Thread
            && Person == other.Person && Date == other.Date && Subject == other.Subject && Snippet == other.Snippet
            && Unread == other.Unread && HasAttachments == other.HasAttachments && MessageCount == other.MessageCount
            && Issue == other.Issue && RuleState == other.RuleState && RuleReason == other.RuleReason
            && Annotation == other.Annotation && UserState == other.UserState && Visibility == other.Visibility
            && Reply == other.Reply && LatestMessage == other.LatestMessage && CanArchive == other.CanArchive
            && Draft == other.Draft && SameList(Messages, other.Messages) && MessagesFailed == other.MessagesFailed
            && Version == other.Version;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Id, Account, Date, Version, Visibility);
    }

    /// <summary>Something the user promised, as the assistant found it in a case.</summary>
    public sealed record Commitment
    {
        /// <summary>The commitment.</summary>
        public required BoardCommitmentId Id { get; init; }

        /// <summary>The case it comes from.</summary>
        public required BoardCaseId CaseId { get; init; }

        /// <summary>What was promised.</summary>
        public required string Text { get; init; }

        /// <summary>The sentence it comes from.</summary>
        public string Quote { get; init; } = "";

        /// <summary>By when.</summary>
        public DateTimeOffset? Due { get; init; }

        /// <summary>The user's message the sentence is in; null for the samples.</summary>
        public MessageId? MessageId { get; init; }

        /// <summary>Only open ones are shown.</summary>
        public CommitmentState State { get; init; }
    }

    /// <summary>The assistant's last pass over the board.</summary>
    public sealed record Run
    {
        /// <summary>Names the assistant (the run's source).</summary>
        public required string Model { get; init; }

        /// <summary>When it ended, or started while it runs.</summary>
        public required DateTimeOffset Date { get; init; }

        /// <summary>A short note.</summary>
        public string Note { get; init; } = "";

        /// <summary>Cases it annotated.</summary>
        public int Annotated { get; init; }

        /// <summary>It has not ended yet.</summary>
        public bool Running { get; init; }

        /// <summary>The class of its failure (<see cref="BoardRunError"/>); null after a success.</summary>
        public string? Error { get; init; }

        /// <summary>Who started it (<see cref="BoardTrigger"/>: manual, auto, external); "" when not known.</summary>
        public string Trigger { get; init; } = "";

        /// <summary>When it started; null when not known.</summary>
        public DateTimeOffset? Started { get; init; }
    }

    /// <summary>What the triage needs to know (<c>board.list</c> <c>triage</c>; Go <c>TriageInfo</c>).</summary>
    public sealed record Triage
    {
        /// <summary>Live cases waiting for the assistant (0 while it is off).</summary>
        public int Queue { get; init; }

        /// <summary>Cases automatic runs annotated today.</summary>
        public int AnnotatedToday { get; init; }

        /// <summary>
        /// The tokens triage runs used in the last 24 hours, as the daemon
        /// summed them when it listed; null when no run reported any.
        /// </summary>
        public BoardUsageTotal? Usage24h { get; init; }
    }

    /// <summary>Everything a source knows at one moment.</summary>
    public sealed record Snapshot
    {
        /// <summary>No account, no case, the assistant off.</summary>
        public static Snapshot Empty { get; } = new();

        /// <summary>The accounts, in the order the board lists them.</summary>
        public IReadOnlyList<AccountInfo> Accounts { get; init => field = value ?? []; } = [];

        /// <summary>The cases.</summary>
        public IReadOnlyList<Case> Cases { get; init => field = value ?? []; } = [];

        /// <summary>The user's commitments.</summary>
        public IReadOnlyList<Commitment> Commitments { get; init => field = value ?? []; } = [];

        /// <summary>The assistant is on and its annotations count.</summary>
        public bool Annotated { get; init; }

        /// <summary>The assistant's last run.</summary>
        public Run? Run { get; init; }

        /// <summary>How far the data is.</summary>
        public Phase Phase { get; init; } = Phase.Ready;

        /// <summary>What the triage needs to know.</summary>
        public Triage Triage { get; init => field = value ?? new(); } = new();

        /// <summary>More cases than the daemon lists (the oldest left out).</summary>
        public bool Truncated { get; init; }

        /// <summary>The first case of id <paramref name="id"/>; null when none (Go <c>Snapshot.Case</c>; the name Case is the type's).</summary>
        public Case? FindCase(BoardCaseId id) => Cases.FirstOrDefault(c => c.Id == id);

        /// <summary>Whether both hold the same, the lists compared in order.</summary>
        public bool Equals(Snapshot? other) =>
            other is not null && SameList(Accounts, other.Accounts) && SameList(Cases, other.Cases)
            && SameList(Commitments, other.Commitments) && Annotated == other.Annotated && Run == other.Run
            && Phase == other.Phase && Triage == other.Triage && Truncated == other.Truncated;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Cases.Count, Commitments.Count, Annotated, Phase);
    }

    extension(Phase phase)
    {
        /// <summary>The board could not be listed: the cases shown are old or none.</summary>
        public bool IsFailure => phase is Phase.Unavailable or Phase.Failed or Phase.Unsupported;
    }

    /// <summary>
    /// The annotation that counts (Swift <c>annotation(of:annotated:)</c>):
    /// none while the assistant is off or when it is stale.
    /// </summary>
    public static Annotation? AnnotationOf(Case c, bool annotated)
    {
        ArgumentNullException.ThrowIfNull(c);
        return annotated && c.Annotation is { Stale: false } a ? a : null;
    }

    /// <summary>
    /// The state a case shows (docs/api.md §4.13; Swift
    /// <c>state(of:annotated:)</c>): the user's choice, else the
    /// assistant's (when annotations count, the annotation is not stale and
    /// has one), else the rules'.
    /// </summary>
    public static State StateOf(Case c, bool annotated)
    {
        ArgumentNullException.ThrowIfNull(c);
        return c.UserState ?? AnnotationOf(c, annotated)?.State ?? c.RuleState;
    }

    /// <summary>
    /// Who decided <see cref="StateOf"/> (Swift
    /// <c>stateSource(of:annotated:)</c>). An annotation that leaves the
    /// state to the rules kept it; a stale one counts as none.
    /// </summary>
    public static StateSource StateSourceOf(Case c, bool annotated)
    {
        ArgumentNullException.ThrowIfNull(c);
        if (c.UserState is not null)
        {
            return StateSource.User;
        }
        if (!annotated)
        {
            return StateSource.AssistantOff;
        }
        if (AnnotationOf(c, annotated) is not { } a)
        {
            return StateSource.Rules;
        }
        if (a.State is not { } st || st == c.RuleState)
        {
            return StateSource.AssistantKept;
        }
        return StateSource.AssistantChanged(c.RuleState);
    }

    // Two lists of the same items in the same order; two nulls are the same.
    internal static bool SameList<T>(IReadOnlyList<T>? a, IReadOnlyList<T>? b)
    {
        if (ReferenceEquals(a, b))
        {
            return true;
        }
        if (a is null || b is null || a.Count != b.Count)
        {
            return false;
        }
        var eq = EqualityComparer<T>.Default;
        for (var i = 0; i < a.Count; i++)
        {
            if (!eq.Equals(a[i], b[i]))
            {
                return false;
            }
        }
        return true;
    }
}
