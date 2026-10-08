// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardView.swift (the view model's
// types: Filter, AccountFilter, ViewState, Row, CommitmentRow, SectionKind,
// Section, Column, NavItem, AccountItem, MessageCard, Detail, DueGroupKind,
// DueItem, DueGroup, TileKind, Tile, Today, View); GTK:
// ui/internal/board/view.go and text.go (Filter, DueGroup), the same
// types.
//
// Swift's enums with an associated value are Go's kind and value: a Filter
// is its kind and, for FilterKind.State, its state; a section and a tile
// carry their kind and state the same way. Swift's AccountFilter is an
// AccountId, null for every account (Go's ""). Swift's View is ViewModel
// (Go's name): the function that builds it is View. Go's Row.DueOverdue
// (GTK colours an overdue deadline) is here too.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>The cases the list shows (<see cref="Filter"/>).</summary>
    public enum FilterKind
    {
        /// <summary>Every case on the board (Overview).</summary>
        All,

        /// <summary>The cases of one state.</summary>
        State,

        /// <summary>The done cases.</summary>
        Done,

        /// <summary>The cases off the board until a reminder.</summary>
        Snoozed,
    }

    /// <summary>What a section of the list holds.</summary>
    public enum SectionKind
    {
        /// <summary>The live cases of <see cref="Section.State"/>.</summary>
        State,

        /// <summary>Under the Snoozed filter: the cases that come back later, the soonest first.</summary>
        Snoozed,

        /// <summary>The done cases.</summary>
        Done,
    }

    /// <summary>The Today page's deadline groups; the declaration order is the order.</summary>
    public enum DueGroupKind
    {
        /// <summary>Before today.</summary>
        Overdue,

        /// <summary>Today.</summary>
        Today,

        /// <summary>Tomorrow.</summary>
        Tomorrow,

        /// <summary>Two to seven days ahead ("Next 7 Days").</summary>
        ThisWeek,

        /// <summary>Later.</summary>
        Later,
    }

    /// <summary>What a count tile of the Today page counts.</summary>
    public enum TileKind
    {
        /// <summary>The live cases of <see cref="Tile.State"/>.</summary>
        State,

        /// <summary>The open commitments.</summary>
        Commitments,
    }

    /// <summary>Which cases the list shows. The default is <see cref="All"/>.</summary>
    public readonly record struct Filter
    {
        private Filter(FilterKind kind, State state)
        {
            Kind = kind;
            State = state;
        }

        /// <summary>Every case on the board.</summary>
        public static Filter All => default;

        /// <summary>The done cases.</summary>
        public static Filter Done => new(FilterKind.Done, default);

        /// <summary>The snoozed cases.</summary>
        public static Filter Snoozed => new(FilterKind.Snoozed, default);

        /// <summary>Which.</summary>
        public FilterKind Kind { get; }

        /// <summary>The state of <see cref="FilterKind.State"/>; <see cref="State.Hot"/> otherwise.</summary>
        public State State { get; }

        /// <summary>The live cases of <paramref name="state"/>.</summary>
        public static Filter Of(State state) => new(FilterKind.State, state);
    }

    /// <summary>What the user chose to look at. Not case data: the source keeps that.</summary>
    public sealed record ViewState
    {
        /// <summary>How the board lays the cases out.</summary>
        public BoardStyle Style { get; init; } = BoardStyle.List;

        /// <summary>Which cases the list shows.</summary>
        public Filter Filter { get; init; }

        /// <summary>Which account's cases the board shows; null for every account.</summary>
        public AccountId? Account { get; init; }

        /// <summary>The selected case; null for none.</summary>
        public BoardCaseId? Selection { get; init; }

        /// <summary>The detail's "Why is this here?" box is open.</summary>
        public bool RevealsWhy { get; init; }

        /// <summary>
        /// The list style has room for the detail beside it; without, the
        /// detail is the sliding panel.
        /// </summary>
        public bool InlineDetail { get; init; } = true;
    }

    /// <summary>
    /// A case in a list, a column or the Today page. No selected field on
    /// purpose: a selection change must not rebuild the rows.
    /// </summary>
    public sealed record Row
    {
        /// <summary>The case.</summary>
        public required BoardCaseId Id { get; init; }

        /// <summary>Its state.</summary>
        public required State State { get; init; }

        /// <summary>The other party.</summary>
        public required string Person { get; init; }

        /// <summary>The list's date of the latest activity.</summary>
        public required string Time { get; init; }

        /// <summary>The title.</summary>
        public required string Title { get; init; }

        /// <summary>
        /// <see cref="Title"/> is the assistant's (its annotation's title),
        /// not the subject: the views mark it (docs/api.md §4.13).
        /// </summary>
        public bool TitleIsAssistant { get; init; }

        /// <summary>The snippet.</summary>
        public required string Snippet { get; init; }

        /// <summary><see cref="Snippet"/> is the assistant's summary, not the message's text.</summary>
        public bool SnippetIsAssistant { get; init; }

        /// <summary>The account's name.</summary>
        public required string Account { get; init; }

        /// <summary>"" when the case is no issue.</summary>
        public string IssueKey { get; init; } = "";

        /// <summary>The issue's status.</summary>
        public string IssueStatus { get; init; } = "";

        /// <summary>The issue status's colour.</summary>
        public JiraStatusStyle IssueStyle { get; init; }

        /// <summary>"" without a due date.</summary>
        public string Due { get; init; } = "";

        /// <summary>The due date lies before today in the board's time zone (Go only).</summary>
        public bool DueOverdue { get; init; }

        /// <summary>When a snoozed case comes back ("Tomorrow at 09:00"); "" otherwise.</summary>
        public string Remind { get; init; } = "";

        /// <summary>The case is back from a reminder (<see cref="Case.Reminded"/>): listed first in its state, with the badge <see cref="Text.Reminded"/>.</summary>
        public bool Reminded { get; init; }

        /// <summary>Its sender is someone the user never wrote to (<c>you.newContact</c>): the badge <see cref="Text.NewContact"/>.</summary>
        public bool NewContact { get; init; }

        /// <summary>The badges' texts in order (Reminded, New contact); none when empty.</summary>
        public IReadOnlyList<string> Badges { get; init => field = value ?? []; } = [];

        /// <summary>Attachments in the case.</summary>
        public bool Attachments { get; init; }

        /// <summary>The message count from two on, "" below.</summary>
        public string CountText { get; init; } = "";

        /// <summary>Unread mail in the case.</summary>
        public bool Unread { get; init; }

        /// <summary>The row's accessible name ("Assistant:" before the assistant's title).</summary>
        public required string Spoken { get; init; }

        /// <summary>
        /// The row shows text the assistant wrote (its title or its summary
        /// as the snippet): the views put the assistant's mark in front of the
        /// title.
        /// </summary>
        public bool MarksAssistant => TitleIsAssistant || SnippetIsAssistant;

        /// <summary>Whether both show the same, the badges compared in order.</summary>
        public bool Equals(Row? other) =>
            other is not null && Id == other.Id && State == other.State && Person == other.Person && Time == other.Time
            && Title == other.Title && TitleIsAssistant == other.TitleIsAssistant && Snippet == other.Snippet
            && SnippetIsAssistant == other.SnippetIsAssistant && Account == other.Account && IssueKey == other.IssueKey
            && IssueStatus == other.IssueStatus && IssueStyle == other.IssueStyle && Due == other.Due
            && DueOverdue == other.DueOverdue && Remind == other.Remind && Reminded == other.Reminded
            && NewContact == other.NewContact && SameList(Badges, other.Badges) && Attachments == other.Attachments
            && CountText == other.CountText && Unread == other.Unread && Spoken == other.Spoken;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Id, State, Title, Time, Reminded);
    }

    /// <summary>A commitment of the user's, with the case it comes from.</summary>
    public sealed record CommitmentRow
    {
        /// <summary>The commitment.</summary>
        public required BoardCommitmentId Id { get; init; }

        /// <summary>The case it comes from.</summary>
        public required BoardCaseId CaseId { get; init; }

        /// <summary>What was promised.</summary>
        public required string Text { get; init; }

        /// <summary>The sentence it comes from.</summary>
        public required string Quote { get; init; }

        /// <summary>By when; "" without.</summary>
        public required string Due { get; init; }

        /// <summary>The case's title.</summary>
        public required string From { get; init; }

        /// <summary><see cref="From"/> is the assistant's title of the case, not its subject.</summary>
        public bool FromIsAssistant { get; init; }

        /// <summary><see cref="From"/> for the screen reader ("Assistant:" before the assistant's title).</summary>
        public required string SpokenFrom { get; init; }
    }

    /// <summary>A section of the list.</summary>
    public sealed record Section
    {
        /// <summary>What it holds.</summary>
        public required SectionKind Kind { get; init; }

        /// <summary>The state of <see cref="SectionKind.State"/>; <see cref="State.Hot"/> otherwise.</summary>
        public State State { get; init; }

        /// <summary>Its title.</summary>
        public required string Title { get; init; }

        /// <summary>Its rows.</summary>
        public IReadOnlyList<Row> Rows { get; init => field = value ?? []; } = [];

        /// <summary>Whether both are the same section with the same rows.</summary>
        public bool Equals(Section? other) =>
            other is not null && Kind == other.Kind && State == other.State && Title == other.Title
            && SameList(Rows, other.Rows);

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Kind, State, Rows.Count);
    }

    /// <summary>A column of the Columns style.</summary>
    public sealed record Column
    {
        /// <summary>Its state.</summary>
        public required State State { get; init; }

        /// <summary>Its title.</summary>
        public required string Title { get; init; }

        /// <summary>Its rows.</summary>
        public IReadOnlyList<Row> Rows { get; init => field = value ?? []; } = [];

        /// <summary>The placeholder without a row.</summary>
        public required string EmptyText { get; init; }

        /// <summary>Whether both are the same column with the same rows.</summary>
        public bool Equals(Column? other) =>
            other is not null && State == other.State && Title == other.Title && EmptyText == other.EmptyText
            && SameList(Rows, other.Rows);

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(State, Rows.Count);
    }

    /// <summary>A filter in the navigation column.</summary>
    /// <param name="Filter">The filter.</param>
    /// <param name="Title">Its name.</param>
    /// <param name="Dot">The state's colour dot; null for Overview, Snoozed and Done.</param>
    /// <param name="Count">The cases it lists.</param>
    /// <param name="Selected">It is the filter.</param>
    public sealed record NavItem(Filter Filter, string Title, State? Dot, int Count, bool Selected);

    /// <summary>An account in the navigation column and the account menu.</summary>
    /// <param name="Filter">The account; null for every account.</param>
    /// <param name="Title">Its name.</param>
    /// <param name="Badge">Its kind capsule; "" for every account.</param>
    /// <param name="Count">The cases not done.</param>
    /// <param name="Selected">It is the account filter.</param>
    public sealed record AccountItem(AccountId? Filter, string Title, string Badge, int Count, bool Selected)
    {
        /// <summary><see cref="Title"/> with <see cref="Badge"/> (<see cref="Text.TitleWithBadge"/>), for a one-line menu and its spoken name.</summary>
        public string Label { get; init; } = Title;
    }

    /// <summary>A message of the detail's conversation.</summary>
    /// <param name="Id">The message; null for the samples.</param>
    /// <param name="From">The sender, <see cref="Text.You"/> for the user.</param>
    /// <param name="When">The list's date.</param>
    /// <param name="Text">The plain text.</param>
    /// <param name="Mine">The user wrote it.</param>
    public sealed record MessageCard(MessageId? Id, string From, string When, string Text, bool Mine);

    /// <summary>The selected case in full. An empty string hides its block.</summary>
    public sealed record Detail
    {
        /// <summary>The case.</summary>
        public required BoardCaseId Id { get; init; }

        /// <summary>Its account.</summary>
        public required AccountId AccountId { get; init; }

        /// <summary>Its thread; null for the samples.</summary>
        public ThreadId? Thread { get; init; }

        /// <summary>What Reply answers and Show in Mail selects; null for the samples.</summary>
        public ReplyTarget? Reply { get; init; }

        /// <summary>The newest message that counts; null for the samples.</summary>
        public MessageId? LatestMessage { get; init; }

        /// <summary>Its state.</summary>
        public required State State { get; init; }

        /// <summary>The state's name.</summary>
        public required string StateTitle { get; init; }

        /// <summary>Who decided the state.</summary>
        public required StateSource Source { get; init; }

        /// <summary>Why the case is here.</summary>
        public required string Why { get; init; }

        /// <summary>
        /// Lines "Why is this here?" adds after <see cref="Why"/> and
        /// <see cref="SourceText"/>: <see cref="Text.ReasonReminded"/> for a
        /// case back from a reminder, <see cref="Text.ReasonUserKeeps"/> when
        /// the user chose its state.
        /// </summary>
        public IReadOnlyList<string> WhyNotes { get; init => field = value ?? []; } = [];

        /// <summary>
        /// <see cref="Why"/> is the assistant's reason, not the rules': the
        /// box leads it with the assistant's mark.
        /// </summary>
        public bool WhyIsAssistant { get; init; }

        /// <summary>Who decided the state, in words.</summary>
        public required string SourceText { get; init; }

        /// <summary>The account's name.</summary>
        public required string Account { get; init; }

        /// <summary>The issue, its strings cleaned.</summary>
        public IssueInfo? Issue { get; init; }

        /// <summary>The other party.</summary>
        public required string Person { get; init; }

        /// <summary>The full date of the latest activity.</summary>
        public required string Time { get; init; }

        /// <summary><see cref="Person"/> and <see cref="Time"/> as one line (<see cref="Text.PersonAndTime"/>).</summary>
        public string Byline { get; init; } = "";

        /// <summary>The row's (<see cref="Row.Reminded"/>).</summary>
        public bool Reminded { get; init; }

        /// <summary>The row's (<see cref="Row.NewContact"/>).</summary>
        public bool NewContact { get; init; }

        /// <summary>The row's (<see cref="Row.Badges"/>).</summary>
        public IReadOnlyList<string> Badges { get; init => field = value ?? []; } = [];

        /// <summary>The title.</summary>
        public required string Title { get; init; }

        /// <summary><see cref="Title"/> is the assistant's, not the subject: the detail marks it.</summary>
        public bool TitleIsAssistant { get; init; }

        /// <summary><see cref="Title"/> for the screen reader ("Assistant:" before the assistant's).</summary>
        public required string SpokenTitle { get; init; }

        /// <summary>"" = hidden: shown only when the assistant's title differs.</summary>
        public string Subject { get; init; } = "";

        /// <summary>The deadline's day.</summary>
        public string Due { get; init; } = "";

        /// <summary>The sentence the deadline comes from.</summary>
        public string DueQuote { get; init; } = "";

        /// <summary>The assistant's summary.</summary>
        public string Summary { get; init; } = "";

        /// <summary>The assistant's tasks.</summary>
        public IReadOnlyList<string> Tasks { get; init => field = value ?? []; } = [];

        /// <summary>
        /// The suggested reply's plain text: the samples' static block shows it
        /// (the daemon's board edits the draft inline instead).
        /// </summary>
        public string Draft { get; init; } = "";

        /// <summary>
        /// The suggested reply's draft, whatever its text (an empty draft is
        /// still edited inline); shown while the draft exists, also after the
        /// notes went stale.
        /// </summary>
        public DraftId? DraftId { get; init; }

        /// <summary>
        /// Unstar is offered: the case is on the board because of a star
        /// (<c>hot.flagged</c>) and not done (<see cref="IBoardSource.Unflag"/>).
        /// </summary>
        public bool CanUnstar { get; init; }

        /// <summary>"" or <see cref="Text.StaleNotes"/>: the assistant's notes no longer count.</summary>
        public string StaleNote { get; init; } = "";

        /// <summary>The case is done.</summary>
        public bool IsDone { get; init; }

        /// <summary>The case is snoozed.</summary>
        public bool IsSnoozed { get; init; }

        /// <summary>"" or "Back on the board Tomorrow 09:00".</summary>
        public string RemindText { get; init; } = "";

        /// <summary>Archive moves messages; without, it only marks the case done.</summary>
        public bool CanArchive { get; init; }

        /// <summary>"Conversation · 3 messages".</summary>
        public required string ConversationTitle { get; init; }

        /// <summary>Oldest first; the last loaded while a newer version loads.</summary>
        public IReadOnlyList<MessageCard> Messages { get; init => field = value ?? []; } = [];

        /// <summary>The conversation has not arrived yet (<see cref="MessagesNote"/> says so).</summary>
        public bool MessagesLoading { get; init; }

        /// <summary>"", <see cref="Text.MessagesLoading"/> or <see cref="Text.MessagesFailed"/>, shown in place of the cards.</summary>
        public string MessagesNote { get; init; } = "";

        /// <summary>The conversation could not be loaded: the note offers <see cref="Text.TryAgain"/>.</summary>
        public bool MessagesRetry { get; init; }

        /// <summary>Whether both show the same, the lists compared in order.</summary>
        public bool Equals(Detail? other) =>
            other is not null && Id == other.Id && AccountId == other.AccountId && Thread == other.Thread
            && Reply == other.Reply && LatestMessage == other.LatestMessage && State == other.State
            && StateTitle == other.StateTitle && Source == other.Source && Why == other.Why
            && SameList(WhyNotes, other.WhyNotes)
            && WhyIsAssistant == other.WhyIsAssistant && SourceText == other.SourceText && Account == other.Account
            && Issue == other.Issue && Person == other.Person && Time == other.Time && Byline == other.Byline
            && Reminded == other.Reminded && NewContact == other.NewContact && SameList(Badges, other.Badges)
            && Title == other.Title
            && TitleIsAssistant == other.TitleIsAssistant && SpokenTitle == other.SpokenTitle
            && Subject == other.Subject && Due == other.Due && DueQuote == other.DueQuote && Summary == other.Summary
            && SameList(Tasks, other.Tasks) && Draft == other.Draft && DraftId == other.DraftId
            && CanUnstar == other.CanUnstar && StaleNote == other.StaleNote && IsDone == other.IsDone
            && IsSnoozed == other.IsSnoozed && RemindText == other.RemindText && CanArchive == other.CanArchive
            && ConversationTitle == other.ConversationTitle && SameList(Messages, other.Messages)
            && MessagesLoading == other.MessagesLoading && MessagesNote == other.MessagesNote
            && MessagesRetry == other.MessagesRetry;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Id, State, Title, Messages.Count);
    }

    /// <summary>A deadline on the Today page.</summary>
    public sealed record DueItem
    {
        /// <summary>The case.</summary>
        public required BoardCaseId CaseId { get; init; }

        /// <summary>The deadline's day.</summary>
        public required string Label { get; init; }

        /// <summary>The case's title.</summary>
        public required string Title { get; init; }

        /// <summary><see cref="Title"/> is the assistant's, not the subject.</summary>
        public bool TitleIsAssistant { get; init; }

        /// <summary><see cref="Title"/> for the screen reader ("Assistant:" before the assistant's).</summary>
        public required string SpokenTitle { get; init; }

        /// <summary>The other party.</summary>
        public required string Person { get; init; }

        /// <summary>The sentence the deadline comes from.</summary>
        public required string Quote { get; init; }
    }

    /// <summary>A group of the Today page's deadlines.</summary>
    public sealed record DueGroup
    {
        /// <summary>Which.</summary>
        public required DueGroupKind Kind { get; init; }

        /// <summary>Its title.</summary>
        public required string Title { get; init; }

        /// <summary>Its deadlines, soonest first.</summary>
        public IReadOnlyList<DueItem> Items { get; init => field = value ?? []; } = [];

        /// <summary>Whether both are the same group with the same items.</summary>
        public bool Equals(DueGroup? other) =>
            other is not null && Kind == other.Kind && Title == other.Title && SameList(Items, other.Items);

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Kind, Items.Count);
    }

    /// <summary>A count tile of the Today page ("3 Hot").</summary>
    /// <param name="Kind">What it counts.</param>
    /// <param name="State">The state of <see cref="TileKind.State"/>; <see cref="State.Hot"/> otherwise.</param>
    /// <param name="Count">How many.</param>
    /// <param name="Title">Its title.</param>
    public sealed record Tile(TileKind Kind, State State, int Count, string Title)
    {
        /// <summary>Its count and title in one sentence (<see cref="Text.TileToolTip"/>), for the tooltip and the spoken name.</summary>
        public string ToolTip { get; init; } = "";
    }

    /// <summary>The Today page.</summary>
    public sealed record Today
    {
        /// <summary>Its title.</summary>
        public required string Title { get; init; }

        /// <summary>
        /// The sentence under the title: <see cref="Text.TodoPhrase"/> of the
        /// hot cases and those waiting for the user that need the user today
        /// (<see cref="NeedsYouToday"/>).
        /// </summary>
        public required string Phrase { get; init; }

        /// <summary>The four states, and the commitments when annotations count.</summary>
        public IReadOnlyList<Tile> Tiles { get; init => field = value ?? []; } = [];

        /// <summary>The hot cases.</summary>
        public IReadOnlyList<Row> Hot { get; init => field = value ?? []; } = [];

        /// <summary>The first <see cref="YouTopCount"/> cases waiting for the user.</summary>
        public IReadOnlyList<Row> You { get; init => field = value ?? []; } = [];

        /// <summary>The cases waiting for the user beyond <see cref="You"/>.</summary>
        public int YouMore { get; init; }

        /// <summary>The commitments.</summary>
        public IReadOnlyList<CommitmentRow> Commitments { get; init => field = value ?? []; } = [];

        /// <summary>The non-empty groups, in <see cref="DueGroupKind"/> order.</summary>
        public IReadOnlyList<DueGroup> DueGroups { get; init => field = value ?? []; } = [];

        /// <summary>The deadlines without one.</summary>
        public required string DueEmpty { get; init; }

        /// <summary>Whether both show the same, the lists compared in order.</summary>
        public bool Equals(Today? other) =>
            other is not null && Title == other.Title && Phrase == other.Phrase && SameList(Tiles, other.Tiles)
            && SameList(Hot, other.Hot) && SameList(You, other.You) && YouMore == other.YouMore
            && SameList(Commitments, other.Commitments) && SameList(DueGroups, other.DueGroups)
            && DueEmpty == other.DueEmpty;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Phrase, Hot.Count, You.Count, YouMore);
    }

    /// <summary>Everything the board shows (Swift <c>Board.View</c>).</summary>
    public sealed record ViewModel
    {
        /// <summary>How far the source's data is.</summary>
        public required Phase Phase { get; init; }

        /// <summary>The empty board's title for the phase (<see cref="IsEmpty"/>).</summary>
        public required string EmptyTitle { get; init; }

        /// <summary>The empty board's body for the phase.</summary>
        public required string EmptyBody { get; init; }

        /// <summary>A line above the cases while they are partial or old; "" when none.</summary>
        public required string Notice { get; init; }

        /// <summary>The triage's status.</summary>
        public required Triage Triage { get; init; }

        /// <summary>The assistant's last run.</summary>
        public Run? Run { get; init; }

        /// <summary>The navigation column's filters.</summary>
        public IReadOnlyList<NavItem> Nav { get; init => field = value ?? []; } = [];

        /// <summary>The navigation column's accounts.</summary>
        public IReadOnlyList<AccountItem> Accounts { get; init => field = value ?? []; } = [];

        /// <summary>The account filter's name.</summary>
        public required string AccountTitle { get; init; }

        /// <summary>The window's subtitle: "All Accounts · 23 cases".</summary>
        public required string Subtitle { get; init; }

        /// <summary>No case at all in the account scope (live, snoozed or done).</summary>
        public bool IsEmpty { get; init; }

        /// <summary>The list's non-empty sections.</summary>
        public IReadOnlyList<Section> Sections { get; init => field = value ?? []; } = [];

        /// <summary>The list without a row.</summary>
        public required string SectionsEmptyText { get; init; }

        /// <summary>The commitments shown.</summary>
        public IReadOnlyList<CommitmentRow> Commitments { get; init => field = value ?? []; } = [];

        /// <summary>The list shows the commitments (under Overview).</summary>
        public bool ShowsCommitmentsInList { get; init; }

        /// <summary>Always the four states, whatever the filter.</summary>
        public IReadOnlyList<Column> Columns { get; init => field = value ?? []; } = [];

        /// <summary>The Today page.</summary>
        public required Today Today { get; init; }

        /// <summary>The selected case.</summary>
        public Detail? Detail { get; init; }

        /// <summary>The detail is the sliding panel.</summary>
        public bool ShowsPanel { get; init; }

        /// <summary>The status bar's line.</summary>
        public required string StatusLine { get; init; }

        /// <summary>Whether the assistant is on (the snapshot's <see cref="Snapshot.Annotated"/>).</summary>
        public bool AssistantOn { get; init; }

        /// <summary>The selection, resolved (<see cref="ResolveSelection"/>).</summary>
        public BoardCaseId? Selection { get; init; }

        /// <summary>Whether both show the same, the lists compared in order.</summary>
        public bool Equals(ViewModel? other) =>
            other is not null && Phase == other.Phase && EmptyTitle == other.EmptyTitle && EmptyBody == other.EmptyBody
            && Notice == other.Notice && Triage == other.Triage && Run == other.Run && SameList(Nav, other.Nav)
            && SameList(Accounts, other.Accounts) && AccountTitle == other.AccountTitle && Subtitle == other.Subtitle
            && IsEmpty == other.IsEmpty && SameList(Sections, other.Sections)
            && SectionsEmptyText == other.SectionsEmptyText && SameList(Commitments, other.Commitments)
            && ShowsCommitmentsInList == other.ShowsCommitmentsInList && SameList(Columns, other.Columns)
            && Today == other.Today && Detail == other.Detail && ShowsPanel == other.ShowsPanel
            && StatusLine == other.StatusLine && AssistantOn == other.AssistantOn && Selection == other.Selection;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Phase, Sections.Count, Selection, Subtitle);
    }

    /// <summary>The cases waiting for the user the Today page lists.</summary>
    public const int YouTopCount = 5;
}
