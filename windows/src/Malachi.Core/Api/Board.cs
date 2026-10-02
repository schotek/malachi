// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows addition, ahead of a Swift port (MalachiCore/API has no board
// types yet); Go: backend/pkg/api/board.go (the identifiers, string types
// and structs of the board, QuoteNotFoundData, BoardChangedNotification);
// contract: docs/api.md §2 (caseNotFound, quoteNotFound), §4.13, §5
// (notify.boardChanged).
//
// The board sorts the user's conversations and issues into four states by
// what is owed. Every string of a case comes from mail or from an
// assistant that read mail: untrusted plain text, cleaned by the daemon,
// never interpreted as markup. Only the contract is here; the board's
// limits (api.MaxBoard*) are not in API.Limits, which follows types.go.

using System;
using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.BoardCaseID: a case, <c>c_</c> and 32 lowercase hex digits. Stable
/// across thread merges (unlike <see cref="ThreadId"/>) and restarts; per
/// account.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<BoardCaseId>))]
public readonly record struct BoardCaseId(string Value) : IOpaqueId<BoardCaseId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator BoardCaseId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.BoardCommitmentID: a commitment of a case. Opaque.</summary>
[JsonConverter(typeof(StringWireValueConverter<BoardCommitmentId>))]
public readonly record struct BoardCommitmentId(string Value) : IOpaqueId<BoardCommitmentId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator BoardCommitmentId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.BoardRunID: a triage run (<c>board.runStart</c>). Opaque.</summary>
[JsonConverter(typeof(StringWireValueConverter<BoardRunId>))]
public readonly record struct BoardRunId(string Value) : IOpaqueId<BoardRunId>
{
    /// <summary>The identifier of a wire string.</summary>
    public static implicit operator BoardRunId(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.BoardState: where a case stands. Go's api.BoardStates is the display order.</summary>
[JsonConverter(typeof(StringWireValueConverter<BoardState>))]
public readonly record struct BoardState(string Value) : IWireEnumeration<BoardState>
{
    /// <summary>Needs the user now: marked important, or flagged by the user.</summary>
    public const string Hot = "hot";

    /// <summary>Waits for the user's answer.</summary>
    public const string You = "you";

    /// <summary>The user waits for someone else.</summary>
    public const string Them = "them";

    /// <summary>Nothing to do; for reading.</summary>
    public const string Info = "info";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator BoardState(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.BoardReason: the code of the rule that gave a case its rule state.
/// An open enumeration: a client shows a generic text for a code it does
/// not know; a code is never reused for another meaning.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<BoardReason>))]
public readonly record struct BoardReason(string Value) : IWireEnumeration<BoardReason>
{
    /// <summary>Inbound, the user in its To, and its own header says Importance: high or X-Priority 1 or 2.</summary>
    public const string HotImportant = "hot.important";

    /// <summary>The user flagged a member and the newest relevant member is inbound.</summary>
    public const string HotFlagged = "hot.flagged";

    /// <summary>The newest relevant member is inbound and the user is in its To.</summary>
    public const string YouAddressed = "you.addressed";

    /// <summary>The newest relevant member is inbound and answers one of the user's messages.</summary>
    public const string YouRepliedToYou = "you.repliedToYou";

    /// <summary>The user replied to an inbound member, to one of its senders.</summary>
    public const string ThemReplied = "them.replied";

    /// <summary>The user started the thread with a question to someone else.</summary>
    public const string ThemAsked = "them.asked";

    /// <summary>Inbound; the user is only in Cc.</summary>
    public const string InfoCcOnly = "info.ccOnly";

    /// <summary>Inbound; the user is not among the To or Cc recipients (a list, a Bcc).</summary>
    public const string InfoNotAddressed = "info.notAddressed";

    /// <summary>Inbound and the user in its To, but from a sender the user has never written to.</summary>
    public const string InfoUnknownSender = "info.unknownSender";

    /// <summary>A note to oneself: every recipient is one of the user's addresses.</summary>
    public const string InfoYourNote = "info.yourNote";

    /// <summary>Jira: the last item that is not an event is the user's comment.</summary>
    public const string JiraYourComment = "jira.yourComment";

    /// <summary>Jira: someone else's item on an issue assigned to the user.</summary>
    public const string JiraAssigned = "jira.assigned";

    /// <summary>Jira: someone else's item on an issue the user reported.</summary>
    public const string JiraReporter = "jira.reporter";

    /// <summary>Jira: someone else's item on an issue the user commented on before.</summary>
    public const string JiraCommented = "jira.commented";

    /// <summary>Jira: an issue the user only watches.</summary>
    public const string JiraWatching = "jira.watching";

    /// <summary>
    /// The rules no longer make the thread a case, but a user state, a
    /// future remind, a future deadline or an open commitment keeps it; the
    /// rule state is the one the rules gave last.
    /// </summary>
    public const string Kept = "kept";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator BoardReason(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.BoardVisibility: where a case is listed. Derived by the daemon.</summary>
[JsonConverter(typeof(StringWireValueConverter<BoardVisibility>))]
public readonly record struct BoardVisibility(string Value) : IWireEnumeration<BoardVisibility>
{
    /// <summary>On the board (Go <c>BoardLive</c>).</summary>
    public const string Live = "live";

    /// <summary>The user marked it done; a later inbound message reopens it (Go <c>BoardDone</c>).</summary>
    public const string Done = "done";

    /// <summary>Hidden until <see cref="BoardCase.RemindAt"/>, then live again (Go <c>BoardSnoozed</c>).</summary>
    public const string Snoozed = "snoozed";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator BoardVisibility(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.BoardCommitmentState: where a commitment stands.</summary>
[JsonConverter(typeof(StringWireValueConverter<BoardCommitmentState>))]
public readonly record struct BoardCommitmentState(string Value) : IWireEnumeration<BoardCommitmentState>
{
    /// <summary>Not kept yet (Go <c>CommitmentOpen</c>).</summary>
    public const string Open = "open";

    /// <summary>The user ticked it off (Go <c>CommitmentDone</c>).</summary>
    public const string Done = "done";

    /// <summary>Closed by the daemon, see <see cref="BoardCommitment.ClosedReason"/> (Go <c>CommitmentClosed</c>).</summary>
    public const string Closed = "closed";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator BoardCommitmentState(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.BoardTrigger: what started a triage run.</summary>
[JsonConverter(typeof(StringWireValueConverter<BoardTrigger>))]
public readonly record struct BoardTrigger(string Value) : IWireEnumeration<BoardTrigger>
{
    /// <summary>The user pressed Triage.</summary>
    public const string Manual = "manual";

    /// <summary>The client's automatic schedule.</summary>
    public const string Auto = "auto";

    /// <summary>Annotations without a run id (Claude Desktop, Claude Code); never passed to <c>board.runStart</c>.</summary>
    public const string External = "external";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator BoardTrigger(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.BoardRunError: the class of a failed triage run, never free text.
/// <c>board.runEnd</c> stores a value the daemon does not know as failed.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<BoardRunError>))]
public readonly record struct BoardRunError(string Value) : IWireEnumeration<BoardRunError>
{
    /// <summary>The user stopped it.</summary>
    public const string Cancelled = "cancelled";

    /// <summary>It took too long.</summary>
    public const string Timeout = "timeout";

    /// <summary>The assistant was not signed in.</summary>
    public const string SignedOut = "signedOut";

    /// <summary>Any other failure.</summary>
    public const string Failed = "failed";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator BoardRunError(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>api.QuoteField: what a quoteNotFound error is about (<see cref="QuoteNotFoundData"/>).</summary>
[JsonConverter(typeof(StringWireValueConverter<QuoteField>))]
public readonly record struct QuoteField(string Value) : IWireEnumeration<QuoteField>
{
    /// <summary>The quote of <see cref="BoardAnnotateParams.Due"/>.</summary>
    public const string Due = "due";

    /// <summary><see cref="BoardCommitParams.Quote"/>.</summary>
    public const string Commitment = "commitment";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator QuoteField(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.BoardCase: a conversation (a mail thread) or an issue (a jira
/// account's thread) on the board. The state a client shows is
/// <see cref="UserState"/> when set, else the annotation's state when the
/// assistant is on (<see cref="BoardListResult.Assistant"/>), the
/// annotation is not stale and carries a state, else <see cref="RuleState"/>.
/// </summary>
public sealed record BoardCase
{
    /// <summary>The case.</summary>
    [JsonPropertyName("id")]
    public required BoardCaseId Id { get; init; }

    /// <summary>Its account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The case's thread now; a merge of threads can change it, the case id stays.</summary>
    [JsonPropertyName("threadId")]
    public required ThreadId ThreadId { get; init; }

    /// <summary>The state the rules gave.</summary>
    [JsonPropertyName("ruleState")]
    public required BoardState RuleState { get; init; }

    /// <summary>The rule that gave it.</summary>
    [JsonPropertyName("ruleReason")]
    public required BoardReason RuleReason { get; init; }

    /// <summary>The user's own choice (<c>board.setState</c>); null = automatic.</summary>
    [JsonPropertyName("userState")]
    public BoardState? UserState { get; init; }

    /// <summary>The assistant's, null when there is none; present also when stale.</summary>
    [JsonPropertyName("annotation")]
    public BoardAnnotation? Annotation { get; init; }

    /// <summary>live, done or snoozed.</summary>
    [JsonPropertyName("visibility")]
    public required BoardVisibility Visibility { get; init; }

    /// <summary>Set when <see cref="Visibility"/> is done.</summary>
    [JsonPropertyName("doneAt")]
    public DateTimeOffset? DoneAt { get; init; }

    /// <summary>Set while snoozed, always in the future.</summary>
    [JsonPropertyName("remindAt")]
    public DateTimeOffset? RemindAt { get; init; }

    /// <summary>The newest relevant member's, Re:/Fwd: stripped; for an issue "KEY: Summary".</summary>
    [JsonPropertyName("subject")]
    public required string Subject { get; init; }

    /// <summary>The other party.</summary>
    [JsonPropertyName("person")]
    public required Address Person { get; init; }

    /// <summary>When the newest relevant member arrived.</summary>
    [JsonPropertyName("date")]
    public required DateTimeOffset Date { get; init; }

    /// <summary>Of the newest relevant member.</summary>
    [JsonPropertyName("snippet")]
    public required string Snippet { get; init; }

    /// <summary>A relevant member is unread.</summary>
    [JsonPropertyName("unread")]
    public required bool Unread { get; init; }

    /// <summary>A relevant member carries attachments.</summary>
    [JsonPropertyName("hasAttachments")]
    public required bool HasAttachments { get; init; }

    /// <summary>The relevant members.</summary>
    [JsonPropertyName("messageCount")]
    public required int MessageCount { get; init; }

    /// <summary>The member a reply answers (<c>draft.create</c> reply); on a jira account a comment on the issue.</summary>
    [JsonPropertyName("replyMessageId")]
    public required MessageId ReplyMessageId { get; init; }

    /// <summary>The folder of <see cref="ReplyMessageId"/>.</summary>
    [JsonPropertyName("replyFolderId")]
    public required FolderId ReplyFolderId { get; init; }

    /// <summary>The newest relevant member.</summary>
    [JsonPropertyName("latestMessageId")]
    public required MessageId LatestMessageId { get; init; }

    /// <summary>Set for a case of a jira account.</summary>
    [JsonPropertyName("issue")]
    public BoardIssue? Issue { get; init; }

    /// <summary><c>board.archive</c> would move messages.</summary>
    [JsonPropertyName("canArchive")]
    public required bool CanArchive { get; init; }

    /// <summary>The suggested reply linked to the case (by the user with <c>board.setDraft</c>, or by an annotation), while that draft exists; the case's, not the annotation's.</summary>
    [JsonPropertyName("draft")]
    public BoardDraft? Draft { get; init; }

    /// <summary>Changes whenever anything above changes; clients cache <c>board.get</c> by (id, version).</summary>
    [JsonPropertyName("version")]
    public required long Version { get; init; }
}

/// <summary>api.BoardIssue: the issue behind a case of a jira account. Status is untrusted text from the site.</summary>
public sealed record BoardIssue
{
    /// <summary>"ITSD-42".</summary>
    [JsonPropertyName("key")]
    public required string Key { get; init; }

    /// <summary>The status's name on the site.</summary>
    [JsonPropertyName("status")]
    public required string Status { get; init; }

    /// <summary>Its category; null when the site does not say.</summary>
    [JsonPropertyName("statusCategory")]
    public IssueStatusCategory? StatusCategory { get; init; }
}

/// <summary>api.BoardDraft: a draft linked to a case.</summary>
public sealed record BoardDraft
{
    /// <summary>The draft.</summary>
    [JsonPropertyName("draftId")]
    public required DraftId DraftId { get; init; }

    /// <summary>Its plain text, cut at a character boundary.</summary>
    [JsonPropertyName("text")]
    public required string Text { get; init; }

    /// <summary>When it was last saved.</summary>
    [JsonPropertyName("updated")]
    public required DateTimeOffset Updated { get; init; }
}

/// <summary>
/// api.BoardAnnotation: what an assistant made of a case
/// (<c>board.annotate</c>). Every string is text an assistant wrote after
/// reading mail: shown only as plain text, never as the daemon's or the
/// user's own words. A stale annotation is used for nothing.
/// </summary>
public sealed record BoardAnnotation
{
    /// <summary>Null: the assistant left the state to the rules.</summary>
    [JsonPropertyName("state")]
    public BoardState? State { get; init; }

    /// <summary>One line; "" = use the subject.</summary>
    [JsonPropertyName("title")]
    public required string Title { get; init; }

    /// <summary>A block, line breaks kept.</summary>
    [JsonPropertyName("summary")]
    public required string Summary { get; init; }

    /// <summary>One line: why the case is in its state.</summary>
    [JsonPropertyName("why")]
    public required string Why { get; init; }

    /// <summary>One line each.</summary>
    [JsonPropertyName("tasks")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Tasks { get; init => field = value ?? []; } = [];

    /// <summary>A deadline the daemon verified against a verbatim quote of a member; null when there is none.</summary>
    [JsonPropertyName("due")]
    public BoardDue? Due { get; init; }

    /// <summary>Names the assistant (a model name), untrusted text from the bridge.</summary>
    [JsonPropertyName("source")]
    public required string Source { get; init; }

    /// <summary>When it was made.</summary>
    [JsonPropertyName("at")]
    public required DateTimeOffset At { get; init; }

    /// <summary>A member was added, removed or got its body since; left out when false.</summary>
    [JsonPropertyName("stale")]
    public bool? Stale { get; init; }
}

/// <summary>api.BoardDue: a deadline with the quote it comes from; a client always shows the quote next to the date.</summary>
public sealed record BoardDue
{
    /// <summary>The deadline.</summary>
    [JsonPropertyName("at")]
    public required DateTimeOffset At { get; init; }

    /// <summary>The sentence of the message the deadline comes from, as found in it.</summary>
    [JsonPropertyName("quote")]
    public required string Quote { get; init; }

    /// <summary>A member of the case's thread.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }
}

/// <summary>
/// api.BoardCommitment: something the user promised in one of their own
/// messages, as an assistant found it (<c>board.commit</c>).
/// <see cref="Text"/> is the assistant's wording; <see cref="Quote"/> is
/// verbatim from the user's own text of <see cref="MessageId"/>.
/// </summary>
public sealed record BoardCommitment
{
    /// <summary>The commitment.</summary>
    [JsonPropertyName("id")]
    public required BoardCommitmentId Id { get; init; }

    /// <summary>Its case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }

    /// <summary>The case's account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The user's message it is in.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>One line.</summary>
    [JsonPropertyName("text")]
    public required string Text { get; init; }

    /// <summary>Verbatim from the message.</summary>
    [JsonPropertyName("quote")]
    public required string Quote { get; init; }

    /// <summary>The deadline, when there is one.</summary>
    [JsonPropertyName("due")]
    public DateTimeOffset? Due { get; init; }

    /// <summary>open, done or closed.</summary>
    [JsonPropertyName("state")]
    public required BoardCommitmentState State { get; init; }

    /// <summary>
    /// For a closed one: "replied" (a newer message of the user's arrived in
    /// the thread) or "done" (the case was marked done). An open enumeration.
    /// </summary>
    [JsonPropertyName("closedReason")]
    public string? ClosedReason { get; init; }

    /// <summary>When it was recorded.</summary>
    [JsonPropertyName("at")]
    public required DateTimeOffset At { get; init; }
}

/// <summary>api.BoardMessage: one member of a case as <c>board.get</c> shows it, plain text only.</summary>
public sealed record BoardMessage
{
    /// <summary>The message.</summary>
    [JsonPropertyName("id")]
    public required MessageId Id { get; init; }

    /// <summary>Its folder.</summary>
    [JsonPropertyName("folderId")]
    public required FolderId FolderId { get; init; }

    /// <summary>Its sender.</summary>
    [JsonPropertyName("from")]
    public required Address From { get; init; }

    /// <summary>Its date.</summary>
    [JsonPropertyName("date")]
    public required DateTimeOffset Date { get; init; }

    /// <summary>In a folder of role sent or outbox.</summary>
    [JsonPropertyName("mine")]
    public required bool Mine { get; init; }

    /// <summary>The stored plain text without the quoted history and the signature, cleaned.</summary>
    [JsonPropertyName("text")]
    public required string Text { get; init; }

    /// <summary>Something was cut off <see cref="Text"/>; left out when false.</summary>
    [JsonPropertyName("trimmed")]
    public bool? Trimmed { get; init; }
}

/// <summary>api.BoardRun: a triage run as <c>board.list</c> reports it.</summary>
public sealed record BoardRun
{
    /// <summary>When it started.</summary>
    [JsonPropertyName("at")]
    public required DateTimeOffset At { get; init; }

    /// <summary>Null while it runs.</summary>
    [JsonPropertyName("endedAt")]
    public DateTimeOffset? EndedAt { get; init; }

    /// <summary>manual, auto or external.</summary>
    [JsonPropertyName("trigger")]
    public required BoardTrigger Trigger { get; init; }

    /// <summary>Names the assistant.</summary>
    [JsonPropertyName("source")]
    public required string Source { get; init; }

    /// <summary>Cases annotated in it.</summary>
    [JsonPropertyName("annotated")]
    public required int Annotated { get; init; }

    /// <summary>Null after a success.</summary>
    [JsonPropertyName("error")]
    public BoardRunError? Error { get; init; }
}

/// <summary>api.BoardTriage: the state of triage the client's status line and automatic schedule need.</summary>
public sealed record BoardTriage
{
    /// <summary>The newest run by start, of any trigger; null before the first.</summary>
    [JsonPropertyName("lastRun")]
    public BoardRun? LastRun { get; init; }

    /// <summary>Cases annotated by runs with trigger auto that started today (the daemon's local day).</summary>
    [JsonPropertyName("annotatedTodayAuto")]
    public required int AnnotatedTodayAuto { get; init; }

    /// <summary>The live cases <c>board.queue</c> would offer; 0 while the assistant is off.</summary>
    [JsonPropertyName("queue")]
    public required int Queue { get; init; }

    /// <summary>
    /// The token usage of the runs that ended within the 24 hours before
    /// <c>board.list</c> answered and carry usage; null when none does.
    /// Computed when the daemon answers: it shrinks without a notification.
    /// </summary>
    [JsonPropertyName("usage24h")]
    public BoardUsageTotal? Usage24h { get; init; }
}

/// <summary>
/// api.BoardUsage: the token usage of a triage run as the client's
/// assistant reported it (<c>board.runEnd</c>), each counter
/// 0..api.MaxBoardUsageTokens (10^12; the daemon clamps larger values).
/// </summary>
public sealed record BoardUsage
{
    /// <summary>Input tokens.</summary>
    [JsonPropertyName("inputTokens")]
    public required long InputTokens { get; init; }

    /// <summary>Output tokens.</summary>
    [JsonPropertyName("outputTokens")]
    public required long OutputTokens { get; init; }

    /// <summary>Input tokens written to the prompt cache.</summary>
    [JsonPropertyName("cacheCreationInputTokens")]
    public required long CacheCreationInputTokens { get; init; }

    /// <summary>Input tokens read from the prompt cache.</summary>
    [JsonPropertyName("cacheReadInputTokens")]
    public required long CacheReadInputTokens { get; init; }
}

/// <summary>
/// api.BoardUsageTotal (<see cref="BoardTriage.Usage24h"/>): <see cref="BoardUsage"/>
/// summed over <see cref="Runs"/> runs. Go embeds BoardUsage, so the wire
/// object is flat.
/// </summary>
public sealed record BoardUsageTotal
{
    /// <summary>Input tokens.</summary>
    [JsonPropertyName("inputTokens")]
    public required long InputTokens { get; init; }

    /// <summary>Output tokens.</summary>
    [JsonPropertyName("outputTokens")]
    public required long OutputTokens { get; init; }

    /// <summary>Input tokens written to the prompt cache.</summary>
    [JsonPropertyName("cacheCreationInputTokens")]
    public required long CacheCreationInputTokens { get; init; }

    /// <summary>Input tokens read from the prompt cache.</summary>
    [JsonPropertyName("cacheReadInputTokens")]
    public required long CacheReadInputTokens { get; init; }

    /// <summary>The runs that contributed, at least 1.</summary>
    [JsonPropertyName("runs")]
    public required int Runs { get; init; }
}

/// <summary>
/// api.BoardWindows: how long cases of each state stay on the board, in
/// days from their date.
/// </summary>
public sealed record BoardWindows
{
    /// <summary>Days for hot.</summary>
    [JsonPropertyName("hot")]
    public required int Hot { get; init; }

    /// <summary>Days for you.</summary>
    [JsonPropertyName("you")]
    public required int You { get; init; }

    /// <summary>Days for them.</summary>
    [JsonPropertyName("them")]
    public required int Them { get; init; }

    /// <summary>Days for info.</summary>
    [JsonPropertyName("info")]
    public required int Info { get; init; }
}

/// <summary>
/// api.BoardPreferences: the board's daemon-side preferences
/// (<c>board.preferences</c>, <c>board.setPreferences</c>; not part of
/// <see cref="Preferences"/>). <c>board.setPreferences</c> replaces every
/// field: a client sends back what it was given with its changes.
/// </summary>
public sealed record BoardPreferences
{
    /// <summary>The daemon computes the board.</summary>
    [JsonPropertyName("enabled")]
    public required bool Enabled { get; init; }

    /// <summary>Annotations count and <c>board.queue</c> hands out mail text; on only after the user's consent.</summary>
    [JsonPropertyName("assistant")]
    public required bool Assistant { get; init; }

    /// <summary>How long cases stay, per state.</summary>
    [JsonPropertyName("windows")]
    public required BoardWindows Windows { get; init; }

    /// <summary>Limits triage to these accounts; empty = every enabled mail account. Always written.</summary>
    [JsonPropertyName("triageAccounts")]
    [JsonConverter(typeof(NullAsEmptyListConverter<AccountId>))]
    public IReadOnlyList<AccountId> TriageAccounts { get; init => field = value ?? []; } = [];

    /// <summary>The client runs triage on its own schedule; the daemon only stores it.</summary>
    [JsonPropertyName("autoTriage")]
    public required bool AutoTriage { get; init; }

    /// <summary>The least time between automatic runs.</summary>
    [JsonPropertyName("autoTriageMinutes")]
    public required int AutoTriageMinutes { get; init; }

    /// <summary>The cases automatic runs annotate per local day at most; 0 = none.</summary>
    [JsonPropertyName("autoTriageDailyCases")]
    public required int AutoTriageDailyCases { get; init; }
}

/// <summary>api.QuoteNotFoundData: the <c>data</c> of a quoteNotFound error.</summary>
public sealed record QuoteNotFoundData
{
    /// <summary>due or commitment.</summary>
    [JsonPropertyName("field")]
    public required QuoteField Field { get; init; }
}

/// <summary>api.BoardListParams. Null or empty <see cref="AccountIds"/> = every enabled account.</summary>
public sealed record BoardListParams
{
    /// <summary>The accounts.</summary>
    [JsonPropertyName("accountIds")]
    public IReadOnlyList<AccountId>? AccountIds { get; init; }
}

/// <summary>api.BoardListResult.</summary>
public sealed record BoardListResult
{
    /// <summary>Live, done and snoozed, newest date first.</summary>
    [JsonPropertyName("cases")]
    [JsonConverter(typeof(NullAsEmptyListConverter<BoardCase>))]
    public IReadOnlyList<BoardCase> Cases { get; init => field = value ?? []; } = [];

    /// <summary>The open commitments of the live cases listed.</summary>
    [JsonPropertyName("commitments")]
    [JsonConverter(typeof(NullAsEmptyListConverter<BoardCommitment>))]
    public IReadOnlyList<BoardCommitment> Commitments { get; init => field = value ?? []; } = [];

    /// <summary><see cref="BoardPreferences.Enabled"/>.</summary>
    [JsonPropertyName("enabled")]
    public required bool Enabled { get; init; }

    /// <summary><see cref="BoardPreferences.Assistant"/>.</summary>
    [JsonPropertyName("assistant")]
    public required bool Assistant { get; init; }

    /// <summary>The state of triage.</summary>
    [JsonPropertyName("triage")]
    public required BoardTriage Triage { get; init; }

    /// <summary>Every thread has been evaluated once; until then <see cref="Cases"/> may be partial.</summary>
    [JsonPropertyName("ready")]
    public required bool Ready { get; init; }

    /// <summary>More cases than the daemon lists; left out when false.</summary>
    [JsonPropertyName("truncated")]
    public bool? Truncated { get; init; }
}

/// <summary>api.BoardGetParams.</summary>
public sealed record BoardGetParams
{
    /// <summary>The case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }
}

/// <summary>api.BoardGetResult.</summary>
public sealed record BoardGetResult
{
    /// <summary>The case.</summary>
    [JsonPropertyName("case")]
    public required BoardCase Case { get; init; }

    /// <summary>The members that count, the newest, oldest first.</summary>
    [JsonPropertyName("messages")]
    [JsonConverter(typeof(NullAsEmptyListConverter<BoardMessage>))]
    public IReadOnlyList<BoardMessage> Messages { get; init => field = value ?? []; } = [];
}

/// <summary>api.BoardSetStateParams. A null <see cref="State"/> (left out) returns the case to automatic.</summary>
public sealed record BoardSetStateParams
{
    /// <summary>The case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }

    /// <summary>The user's state, or null.</summary>
    [JsonPropertyName("state")]
    public BoardState? State { get; init; }
}

/// <summary>api.BoardSetStateResult.</summary>
public sealed record BoardSetStateResult
{
    /// <summary>The case after the change.</summary>
    [JsonPropertyName("case")]
    public required BoardCase Case { get; init; }
}

/// <summary>api.BoardSetDoneParams.</summary>
public sealed record BoardSetDoneParams
{
    /// <summary>The case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }

    /// <summary>Done, or live again.</summary>
    [JsonPropertyName("done")]
    public required bool Done { get; init; }
}

/// <summary>api.BoardSetDoneResult.</summary>
public sealed record BoardSetDoneResult
{
    /// <summary>The case after the change.</summary>
    [JsonPropertyName("case")]
    public required BoardCase Case { get; init; }
}

/// <summary>api.BoardRemindParams. A null <see cref="Until"/> (left out) ends the remind.</summary>
public sealed record BoardRemindParams
{
    /// <summary>The case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }

    /// <summary>In the future and at most a year ahead.</summary>
    [JsonPropertyName("until")]
    public DateTimeOffset? Until { get; init; }
}

/// <summary>api.BoardRemindResult.</summary>
public sealed record BoardRemindResult
{
    /// <summary>The case after the change.</summary>
    [JsonPropertyName("case")]
    public required BoardCase Case { get; init; }
}

/// <summary>api.BoardArchiveParams.</summary>
public sealed record BoardArchiveParams
{
    /// <summary>The case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }
}

/// <summary>api.BoardArchiveResult.</summary>
public sealed record BoardArchiveResult
{
    /// <summary>Messages moved to the archive folder.</summary>
    [JsonPropertyName("archived")]
    public required int Archived { get; init; }

    /// <summary>The account cannot archive: the case was only marked done; left out when false.</summary>
    [JsonPropertyName("noArchive")]
    public bool? NoArchive { get; init; }

    /// <summary>The case after the change.</summary>
    [JsonPropertyName("case")]
    public required BoardCase Case { get; init; }
}

/// <summary>api.BoardUnflagParams: clears the flags behind <c>hot.flagged</c>.</summary>
public sealed record BoardUnflagParams
{
    /// <summary>The case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }
}

/// <summary>api.BoardUnflagResult.</summary>
public sealed record BoardUnflagResult
{
    /// <summary>The case as stored; the rules judge it again afterwards.</summary>
    [JsonPropertyName("case")]
    public required BoardCase Case { get; init; }

    /// <summary>Rows whose flag was cleared; 0 is no error.</summary>
    [JsonPropertyName("unflagged")]
    public required int Unflagged { get; init; }
}

/// <summary>api.BoardDiscardDraftParams.</summary>
public sealed record BoardDiscardDraftParams
{
    /// <summary>The case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }
}

/// <summary>api.BoardDiscardDraftResult.</summary>
public sealed record BoardDiscardDraftResult
{
    /// <summary>The case, without its draft.</summary>
    [JsonPropertyName("case")]
    public required BoardCase Case { get; init; }
}

/// <summary>api.BoardSetDraftParams: links a draft to a case as its suggested reply.</summary>
public sealed record BoardSetDraftParams
{
    /// <summary>The case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }

    /// <summary>A draft of the case's account replying to a member of the case.</summary>
    [JsonPropertyName("draftId")]
    public required DraftId DraftId { get; init; }
}

/// <summary>api.BoardSetDraftResult.</summary>
public sealed record BoardSetDraftResult
{
    /// <summary>The case, with its draft.</summary>
    [JsonPropertyName("case")]
    public required BoardCase Case { get; init; }
}

/// <summary>api.BoardQueueParams (the MCP bridge's; a client has no use for it but the tests).</summary>
public sealed record BoardQueueParams
{
    /// <summary>Null or empty = every triage account.</summary>
    [JsonPropertyName("accountIds")]
    public IReadOnlyList<AccountId>? AccountIds { get; init; }

    /// <summary>Null or empty = any case.</summary>
    [JsonPropertyName("caseIds")]
    public IReadOnlyList<BoardCaseId>? CaseIds { get; init; }

    /// <summary>Null or 0 = the daemon's default.</summary>
    [JsonPropertyName("limit")]
    public int? Limit { get; init; }
}

/// <summary>api.BoardQueueResult.</summary>
public sealed record BoardQueueResult
{
    /// <summary>Newest date first.</summary>
    [JsonPropertyName("items")]
    [JsonConverter(typeof(NullAsEmptyListConverter<BoardQueueItem>))]
    public IReadOnlyList<BoardQueueItem> Items { get; init => field = value ?? []; } = [];

    /// <summary>Further cases the queue would offer.</summary>
    [JsonPropertyName("remaining")]
    public required int Remaining { get; init; }
}

/// <summary>
/// api.BoardQueueItem: a case handed to an assistant. Every string but the
/// ids and <see cref="InputKey"/> comes from mail.
/// </summary>
public sealed record BoardQueueItem
{
    /// <summary>The case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }

    /// <summary>Its account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Names the members the item was built from; passed back to <c>board.annotate</c> and <c>board.commit</c>.</summary>
    [JsonPropertyName("inputKey")]
    public required string InputKey { get; init; }

    /// <summary>The state the rules gave.</summary>
    [JsonPropertyName("ruleState")]
    public required BoardState RuleState { get; init; }

    /// <summary>The rule that gave it.</summary>
    [JsonPropertyName("ruleReason")]
    public required BoardReason RuleReason { get; init; }

    /// <summary>The user's own choice; null = automatic.</summary>
    [JsonPropertyName("userState")]
    public BoardState? UserState { get; init; }

    /// <summary>The case's subject.</summary>
    [JsonPropertyName("subject")]
    public required string Subject { get; init; }

    /// <summary>For create_draft mode reply.</summary>
    [JsonPropertyName("replyMessageId")]
    public required MessageId ReplyMessageId { get; init; }

    /// <summary>Set for a case of a jira account.</summary>
    [JsonPropertyName("issue")]
    public BoardIssue? Issue { get; init; }

    /// <summary>The user's addresses on the account, lower case.</summary>
    [JsonPropertyName("own")]
    [JsonConverter(typeof(NullAsEmptyListConverter<string>))]
    public IReadOnlyList<string> Own { get; init => field = value ?? []; } = [];

    /// <summary>The newest members, oldest first.</summary>
    [JsonPropertyName("messages")]
    [JsonConverter(typeof(NullAsEmptyListConverter<BoardQueueMessage>))]
    public IReadOnlyList<BoardQueueMessage> Messages { get; init => field = value ?? []; } = [];

    /// <summary>The case already links a draft that exists; left out when false.</summary>
    [JsonPropertyName("hasDraft")]
    public bool? HasDraft { get; init; }
}

/// <summary>api.BoardQueueMessage: one member of a <see cref="BoardQueueItem"/>.</summary>
public sealed record BoardQueueMessage
{
    /// <summary>The message.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>Its sender.</summary>
    [JsonPropertyName("from")]
    public required Address From { get; init; }

    /// <summary>Its To; null when empty.</summary>
    [JsonPropertyName("to")]
    public IReadOnlyList<Address>? To { get; init; }

    /// <summary>Its Cc; null when empty.</summary>
    [JsonPropertyName("cc")]
    public IReadOnlyList<Address>? Cc { get; init; }

    /// <summary>Its date.</summary>
    [JsonPropertyName("date")]
    public required DateTimeOffset Date { get; init; }

    /// <summary>The user's own.</summary>
    [JsonPropertyName("mine")]
    public required bool Mine { get; init; }

    /// <summary>As <see cref="BoardMessage.Text"/>, shorter.</summary>
    [JsonPropertyName("text")]
    public required string Text { get; init; }

    /// <summary>Something was cut off <see cref="Text"/>; left out when false.</summary>
    [JsonPropertyName("truncated")]
    public bool? Truncated { get; init; }
}

/// <summary>
/// api.BoardAnnotateParams: replaces the case's annotation as a whole
/// (members left out are empty).
/// </summary>
public sealed record BoardAnnotateParams
{
    /// <summary>The case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }

    /// <summary>From the <see cref="BoardQueueItem"/>.</summary>
    [JsonPropertyName("inputKey")]
    public required string InputKey { get; init; }

    /// <summary>The run the call counts in; null = the implicit external run.</summary>
    [JsonPropertyName("runId")]
    public BoardRunId? RunId { get; init; }

    /// <summary>Null: the state stays the rules'.</summary>
    [JsonPropertyName("state")]
    public BoardState? State { get; init; }

    /// <summary>One line.</summary>
    [JsonPropertyName("title")]
    public string? Title { get; init; }

    /// <summary>A block.</summary>
    [JsonPropertyName("summary")]
    public string? Summary { get; init; }

    /// <summary>One line.</summary>
    [JsonPropertyName("why")]
    public string? Why { get; init; }

    /// <summary>One line each.</summary>
    [JsonPropertyName("tasks")]
    public IReadOnlyList<string>? Tasks { get; init; }

    /// <summary>A deadline with its verbatim quote.</summary>
    [JsonPropertyName("due")]
    public BoardDue? Due { get; init; }

    /// <summary>A draft of the case's account replying to a member of the case; not linked over another live draft; null leaves the link.</summary>
    [JsonPropertyName("draftId")]
    public DraftId? DraftId { get; init; }

    /// <summary>Names the assistant.</summary>
    [JsonPropertyName("source")]
    public required string Source { get; init; }
}

/// <summary>api.BoardAnnotateResult.</summary>
public sealed record BoardAnnotateResult
{
    /// <summary>The case with its new annotation.</summary>
    [JsonPropertyName("case")]
    public required BoardCase Case { get; init; }

    /// <summary>The draft passed was not linked: the case links another that exists; left out when false.</summary>
    [JsonPropertyName("draftNotLinked")]
    public bool? DraftNotLinked { get; init; }
}

/// <summary>api.BoardCommitParams.</summary>
public sealed record BoardCommitParams
{
    /// <summary>The case.</summary>
    [JsonPropertyName("caseId")]
    public required BoardCaseId CaseId { get; init; }

    /// <summary>From the <see cref="BoardQueueItem"/>.</summary>
    [JsonPropertyName("inputKey")]
    public required string InputKey { get; init; }

    /// <summary>The run the call counts in; null = the implicit external run.</summary>
    [JsonPropertyName("runId")]
    public BoardRunId? RunId { get; init; }

    /// <summary>A member of the case that is the user's own.</summary>
    [JsonPropertyName("messageId")]
    public required MessageId MessageId { get; init; }

    /// <summary>One line.</summary>
    [JsonPropertyName("text")]
    public required string Text { get; init; }

    /// <summary>Verbatim from the user's own text of the message.</summary>
    [JsonPropertyName("quote")]
    public required string Quote { get; init; }

    /// <summary>The deadline, when there is one.</summary>
    [JsonPropertyName("due")]
    public DateTimeOffset? Due { get; init; }

    /// <summary>Names the assistant.</summary>
    [JsonPropertyName("source")]
    public required string Source { get; init; }
}

/// <summary>api.BoardCommitResult.</summary>
public sealed record BoardCommitResult
{
    /// <summary>The commitment recorded.</summary>
    [JsonPropertyName("commitment")]
    public required BoardCommitment Commitment { get; init; }
}

/// <summary>api.BoardSetCommitmentParams.</summary>
public sealed record BoardSetCommitmentParams
{
    /// <summary>The commitment.</summary>
    [JsonPropertyName("commitmentId")]
    public required BoardCommitmentId CommitmentId { get; init; }

    /// <summary>Done; false reopens a done or closed one.</summary>
    [JsonPropertyName("done")]
    public required bool Done { get; init; }
}

/// <summary>api.BoardSetCommitmentResult.</summary>
public sealed record BoardSetCommitmentResult
{
    /// <summary>The commitment after the change.</summary>
    [JsonPropertyName("commitment")]
    public required BoardCommitment Commitment { get; init; }
}

/// <summary>api.BoardPreferencesResult (<c>board.preferences</c>, whose params are <see cref="EmptyParams"/>).</summary>
public sealed record BoardPreferencesResult
{
    /// <summary>The preferences.</summary>
    [JsonPropertyName("preferences")]
    public required BoardPreferences Preferences { get; init; }
}

/// <summary>api.BoardSetPreferencesParams: every preference.</summary>
public sealed record BoardSetPreferencesParams
{
    /// <summary>The preferences.</summary>
    [JsonPropertyName("preferences")]
    public required BoardPreferences Preferences { get; init; }
}

/// <summary>api.BoardSetPreferencesResult.</summary>
public sealed record BoardSetPreferencesResult
{
    /// <summary>The preferences as stored.</summary>
    [JsonPropertyName("preferences")]
    public required BoardPreferences Preferences { get; init; }
}

/// <summary>api.BoardRunStartParams.</summary>
public sealed record BoardRunStartParams
{
    /// <summary>manual or auto.</summary>
    [JsonPropertyName("trigger")]
    public required BoardTrigger Trigger { get; init; }

    /// <summary>Names the assistant.</summary>
    [JsonPropertyName("source")]
    public required string Source { get; init; }
}

/// <summary>api.BoardRunStartResult.</summary>
public sealed record BoardRunStartResult
{
    /// <summary>The run.</summary>
    [JsonPropertyName("runId")]
    public required BoardRunId RunId { get; init; }
}

/// <summary>api.BoardRunEndParams (<c>board.runEnd</c>, whose result is <see cref="EmptyResult"/>).</summary>
public sealed record BoardRunEndParams
{
    /// <summary>The run.</summary>
    [JsonPropertyName("runId")]
    public required BoardRunId RunId { get; init; }

    /// <summary>Null = success.</summary>
    [JsonPropertyName("error")]
    public BoardRunError? Error { get; init; }

    /// <summary>
    /// The run's token usage; null = unknown. Stored only when this call
    /// ends the run (ignored like the rest on a run that ended).
    /// </summary>
    [JsonPropertyName("usage")]
    public BoardUsage? Usage { get; init; }
}

/// <summary>
/// api.BoardChangedNotification (<c>notify.boardChanged</c>): what
/// <c>board.list</c> returns changed for these accounts; clients list
/// again. <see cref="AccountIds"/> empty = any account.
/// </summary>
public sealed record BoardChangedNotification
{
    /// <summary>The accounts; empty = any.</summary>
    [JsonPropertyName("accountIds")]
    [JsonConverter(typeof(NullAsEmptyListConverter<AccountId>))]
    public IReadOnlyList<AccountId> AccountIds { get; init => field = value ?? []; } = [];
}
