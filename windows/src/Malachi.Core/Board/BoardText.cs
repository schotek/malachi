// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardText.swift (Board.Text); GTK:
// ui/internal/board/text.go, which holds the msgids, and reply.go
// (ReplyNotSaved, ReplyNotSent, QuitUnsavedHeading, QuitUnsavedBody,
// QuitAnyway).
//
// The board's texts: every text goes through L10n with the GTK msgid as
// the key, formats with the same placeholders. The strings that come from a
// case are cleaned before they are formatted in. The triage's and the
// suggested reply's texts extend this class elsewhere (BoardTriageText.swift,
// BoardSuggestReplyText.swift). Labels with a GTK mnemonic (_Discard,
// _Close, _Quit Anyway) keep it, as the other C# texts do: the view strips
// it (Mnemonic.Strip) or turns it into an access key. Failed takes the
// exception a call threw and reads it through RpcErrorText.Classify (Swift
// casts the error, Go's client maps it to a Why first).

using System;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Text;

namespace Malachi.Core.Board;

public static partial class Board
{
    /// <summary>The board's texts (Swift <c>Board.Text</c>).</summary>
    public static partial class Text
    {
        /// <summary>The separator of a line's parts ("All Accounts · 23 cases").</summary>
        internal const string Separator = " · ";

        /// <summary>
        /// The mode switch's segment and menu item of the board, the
        /// window's title in it, the navigation's caption.
        /// </summary>
        public static string BoardName =>
            // TRANSLATORS: the window's second mode beside Mail: the conversations
            // that need something, sorted into four states.
            L10n.T("Board");

        /// <summary>A state's name: its section, column, tile and pill.</summary>
        public static string StateName(State s) => s switch
        {
            // TRANSLATORS: a state of the board, its column and filter: the
            // cases that need the user now (a deadline, an escalation).
            State.Hot => L10n.T("Hot"),
            // TRANSLATORS: a state of the board, its column and filter: the
            // cases that wait for the user's answer.
            State.You => L10n.T("Waiting for You"),
            // TRANSLATORS: a state of the board, its column and filter: the
            // cases where the user waits for someone else's answer.
            State.Them => L10n.T("Waiting for Them"),
            // TRANSLATORS: a state of the board, its column and filter: the
            // cases with nothing to do, only for reading.
            State.Info => L10n.T("For Your Information"),
            _ => "",
        };

        /// <summary>A filter's name in the navigation column and the Show menu.</summary>
        public static string FilterTitle(Filter f) => f.Kind switch
        {
            FilterKind.State => StateName(f.State),
            FilterKind.Done => Done,
            // TRANSLATORS: the board's filter that shows every case still on it.
            _ => L10n.T("Overview"),
        };

        /// <summary>The account filter that shows every account.</summary>
        public static string AllAccounts => L10n.C("account filter", "All Accounts");

        /// <summary>The Done filter, its section, and the detail's button.</summary>
        public static string Done =>
            // TRANSLATORS: the board's filter and section of the finished cases,
            // and the button that marks a case finished.
            L10n.T("Done");

        /// <summary>"1 case", "23 cases".</summary>
        public static string CaseCount(int n) => L10n.N("%d case", "%d cases", n);

        /// <summary>"1 message", "3 messages".</summary>
        public static string MessageCount(int n) => L10n.N("%d message", "%d messages", n);

        /// <summary>The Today page's sentence under its title, for the hot cases and the ones waiting for the user.</summary>
        public static string TodoPhrase(int n) =>
            n < 1 ? L10n.T("Nothing needs you today.") : L10n.N("%d thing needs you today.", "%d things need you today.", n);

        /// <summary>The row under the top cases waiting for the user.</summary>
        public static string AndMore(int n) =>
            // TRANSLATORS: the row under the first few cases waiting for the user;
            // %d is how many more there are.
            L10n.N("and %d more", "and %d more", n);

        /// <summary>A deadline group's title.</summary>
        public static string DueGroupTitle(DueGroupKind k) => k switch
        {
            DueGroupKind.Overdue => L10n.T("Overdue"),
            DueGroupKind.Today => Today,
            DueGroupKind.Tomorrow => L10n.T("Tomorrow"),
            DueGroupKind.ThisWeek => L10n.T("Next 7 Days"),
            DueGroupKind.Later => L10n.C("deadline", "Later"),
            _ => "",
        };

        /// <summary>The style Today, a group of deadlines and a deadline's day.</summary>
        private static string Today =>
            // TRANSLATORS: the board's style that shows the day (what needs the
            // user, deadlines), a group of deadlines and a deadline's day.
            L10n.T("Today");

        /// <summary>
        /// Who decided a case's state, under "Why is this here?".
        /// <paramref name="run"/> names the assistant's model; it is cleaned here.
        /// </summary>
        public static string SourceText(StateSource source, Run? run)
        {
            var model = ModelName(run);
            switch (source.Kind)
            {
                case StateSourceKind.AssistantKept:
                    return model.Length == 0
                        ? L10n.T("The daemon’s rules set the state and the assistant kept it.")
                        // TRANSLATORS: %s is the name of the assistant's model, such as
                        // "claude-sonnet-4-5".
                        : L10n.T("The daemon’s rules set the state and the assistant (%s) kept it.", model);
                case StateSourceKind.AssistantChanged:
                    var from = StateName(source.From ?? State.Info);
                    return model.Length == 0
                        // TRANSLATORS: %s is the state the rules chose, such as "Waiting for You".
                        ? L10n.T("The assistant refined the state. The rules suggested: %s.", from)
                        // TRANSLATORS: the first %s is the name of the assistant's model,
                        // the second the state the rules chose, such as "Waiting for You".
                        : L10n.T("The assistant (%s) refined the state. The rules suggested: %s.", model, from);
                case StateSourceKind.User:
                    return L10n.T("You moved this case yourself.");
                case StateSourceKind.AssistantOff:
                    return L10n.T("The daemon’s rules set the state; the assistant is off.");
                default:
                    return L10n.T("The daemon’s rules set the state. The assistant has not looked at this case yet.");
            }
        }

        /// <summary>The status bar's line: who sorted the board.</summary>
        public static string StatusLine(bool annotated, Run? run)
        {
            if (!annotated)
            {
                return AssistantOffLine;
            }
            var model = ModelName(run);
            var line = model.Length == 0
                ? L10n.T("Sorted by rules · refined by the assistant")
                // TRANSLATORS: %s is the name of the assistant's model.
                : L10n.T("Sorted by rules · refined by the assistant (%s)", model);
            var note = CleanLine(run?.Note, Cap.Note);
            return note.Length == 0 ? line : line + Separator + note;
        }

        /// <summary>The status line when the assistant does not refine the board.</summary>
        internal static string AssistantOffLine => L10n.T("Sorted by the daemon’s rules · assistant off");

        /// <summary>The detail without a selected case: its title.</summary>
        public static string NoSelectionTitle => L10n.T("No Case Selected");

        /// <summary>The detail without a selected case: its body.</summary>
        public static string NoSelectionBody =>
            L10n.T("Select a case to see its summary, a suggested reply and the conversation.");

        /// <summary>The ready board without a case: its title.</summary>
        public static string EmptyTitle => L10n.T("Nothing on the Board");

        /// <summary>The ready board without a case: its body.</summary>
        public static string EmptyBody => L10n.T("Cases from your accounts show up here as they arrive.");

        /// <summary>A list with no row.</summary>
        public static string SectionEmpty => L10n.T("Nothing here.");

        /// <summary>The placeholder of an empty column.</summary>
        public static string ColumnEmpty(State s) => s == State.Hot ? L10n.T("Nothing burning.") : L10n.T("Empty.");

        /// <summary>The heading of the commitments.</summary>
        public static string FromAssistant => L10n.T("✦ From the Assistant");

        /// <summary>The heading of the assistant's summary.</summary>
        public static string SummaryHeading => L10n.T("✦ Summary from the Assistant");

        /// <summary>The heading of the assistant's tasks.</summary>
        public static string TasksHeading => L10n.T("✦ Tasks and Questions");

        /// <summary>The heading of the suggested reply.</summary>
        public static string DraftHeading => L10n.T("Suggested Reply");

        /// <summary>
        /// The note under the suggested reply: it stays on the board (a local
        /// draft, never in the Drafts folder) until it is sent.
        /// </summary>
        public static string DraftNote => L10n.T("Only here on the board until you send it");

        /// <summary>The detail's reply block while the suggested reply loads.</summary>
        public static string ReplyLoading => L10n.T("Loading the suggested reply…");

        /// <summary>The detail's reply block when draft.get failed (<see cref="TryAgain"/> beside it unless the draft is gone).</summary>
        public static string ReplyLoadFailed => L10n.T("The suggested reply could not be opened.");

        /// <summary>The toast when the draft the inline editor edits was deleted elsewhere.</summary>
        public static string ReplyRemoved => L10n.T("The suggested reply was removed elsewhere.");

        /// <summary>The note in the reply block while what was typed in the suggested reply could not be saved.</summary>
        public static string ReplyNotSaved => L10n.T("This reply could not be saved yet; Malachi Mail keeps trying.");

        /// <summary>The toast when a reply sent and then left was not sent; <paramref name="title"/> is its subject.</summary>
        public static string ReplyNotSent(string? title) =>
            L10n.T("Your reply “%s” was not sent; it is still on the board.", CleanLine(title, 80));

        /// <summary>The question before quitting while a reply on the board could not be saved or sent: its heading.</summary>
        public static string QuitUnsavedHeading => L10n.T("Quit without saving a reply?");

        /// <summary>The question before quitting while a reply on the board could not be saved or sent: its body.</summary>
        public static string QuitUnsavedBody =>
            L10n.T("A reply on the board could not be saved or sent yet. If you quit now, what you typed in it may be lost.");

        /// <summary>The question's button that quits all the same (with its mnemonic).</summary>
        public static string QuitAnyway => L10n.T("_Quit Anyway");

        /// <summary>The detail's and the context menu's Unstar: the app's own wording for removing the flag.</summary>
        public static string Unstar => L10n.T("Unstar");

        /// <summary>Opens the suggested reply's draft.</summary>
        public static string OpenDraft => L10n.T("Open Draft");

        /// <summary>Discards the suggested reply (with its mnemonic).</summary>
        public static string Discard => L10n.T("_Discard");

        /// <summary>The link that opens the reasons of the state.</summary>
        public static string WhyLink => L10n.T("Why is this here?");

        /// <summary>Moves a done case back to the board (the detail's button, the context menu).</summary>
        public static string NotDone => L10n.T("Move Back to Board");

        /// <summary>The toolbar's and the detail's button that picks a reminder.</summary>
        public static string Remind =>
            // TRANSLATORS: a button that takes a case off the board until a
            // chosen time.
            L10n.T("Remind…");

        /// <summary>Answers a case's newest message.</summary>
        public static string Reply => L10n.T("Reply");

        /// <summary>Closes the sliding detail panel (with its mnemonic).</summary>
        public static string Close => L10n.T("_Close");

        /// <summary>The toolbar's button that starts the assistant's triage.</summary>
        public static string Triage =>
            // TRANSLATORS: a button: the assistant reads the conversations on the
            // board and adds notes (titles, summaries, tasks, deadlines).
            L10n.T("✦ Triage");

        /// <summary>The toast of an action this preview does not have yet.</summary>
        public static string Later => L10n.T("Not in this preview yet.");

        /// <summary>The style switch's segments.</summary>
        public static string StyleTitle(BoardStyle s) => s switch
        {
            // TRANSLATORS: the board's style: a navigation column, the list of
            // cases and the detail.
            BoardStyle.List => L10n.C("board style", "List"),
            // TRANSLATORS: the board's style: one column of cards per state.
            BoardStyle.Columns => L10n.C("board style", "Columns"),
            BoardStyle.Today => Today,
            _ => "",
        };

        /// <summary>The menu's items of the styles.</summary>
        public static string StyleMenuTitle(BoardStyle s) => s switch
        {
            BoardStyle.List => L10n.T("As List"),
            BoardStyle.Columns => L10n.T("As Columns"),
            _ => StyleTitle(s),
        };

        /// <summary>Settings → General → Board: the style of the first show after launch; its choices are <see cref="StyleTitle"/>.</summary>
        public static string DefaultStyleSetting =>
            // TRANSLATORS: Settings → General → Board: which style (List, Columns,
            // Today) the board opens in after launch.
            L10n.T("Default View");

        /// <summary>The sender of the user's own messages in the conversation.</summary>
        public static string You =>
            // TRANSLATORS: the sender of the user's own messages in a conversation.
            L10n.T("You");

        /// <summary>"Conversation · 3 messages".</summary>
        public static string Conversation(int n) =>
            // TRANSLATORS: %s is a number of messages, such as "3 messages".
            L10n.T("Conversation · %s", MessageCount(n));

        /// <summary>The heading of the Today page's deadlines.</summary>
        public static string Deadlines => L10n.T("Deadlines");

        /// <summary>The Today page's deadlines without one.</summary>
        public static string DueEmpty =>
            L10n.T("No deadlines. The assistant finds deadlines in the text of messages and keeps the sentence each one comes from.");

        /// <summary>The title of the Today page's calendar placeholder.</summary>
        public static string CalendarTitle => L10n.T("Calendar and Reminders");

        /// <summary>The body of the Today page's calendar placeholder.</summary>
        public static string CalendarBody => L10n.T("Later. Read-only; where the data comes from differs on each platform.");

        /// <summary>The commitments' tile.</summary>
        public static string Commitments =>
            // TRANSLATORS: a tile of the Today page: the promises the assistant
            // found in the user's replies.
            L10n.T("Promised");

        /// <summary>A part of a row's spoken label: "Due Tomorrow".</summary>
        public static string SpokenDue(string label) =>
            // TRANSLATORS: spoken by the screen reader; %s is a deadline's day,
            // such as "Tomorrow" or "20 Oct".
            L10n.T("Due %s", label);

        /// <summary>A part of a row's spoken label: "Back on the board Tomorrow 09:00".</summary>
        public static string SpokenRemind(string label) => SnoozedUntil(label);

        /// <summary>A part of a row's spoken label.</summary>
        public static string SpokenAttachments => L10n.T("Has attachments");

        /// <summary>A part of a row's spoken label.</summary>
        public static string SpokenUnread =>
            // TRANSLATORS: spoken by the screen reader for a case with unread mail.
            L10n.C("board row", "Unread");

        /// <summary>
        /// Why the rules put a case where it is, by the rule's code. The codes
        /// are an open set: one this client does not know gets
        /// <see cref="ReasonUnknown"/>.
        /// </summary>
        public static string Reason(BoardReason code) => code.Value switch
        {
            BoardReason.HotImportant => L10n.T("The newest message is marked as important, addressed to you and from a sender you have written to."),
            BoardReason.HotFlagged => L10n.T("You flagged a message in this conversation and the newest one is not yours."),
            BoardReason.YouAddressed => L10n.T("The newest message is addressed to you by a sender you have written to."),
            BoardReason.YouRepliedToYou => L10n.T("The newest message answers one of yours."),
            BoardReason.ThemReplied => L10n.T("You replied last; the next step is theirs."),
            BoardReason.ThemAsked => L10n.T("You asked a question and wait for the answer."),
            BoardReason.InfoCcOnly => L10n.T("You are only in Cc on the newest message."),
            BoardReason.InfoNotAddressed => L10n.T("The message is not addressed to you (a mailing list or a Bcc)."),
            BoardReason.InfoYourNote => L10n.T("A note to yourself."),
            BoardReason.InfoUnknownSender => L10n.T("The message is addressed to you, but its sender is one you have never written to, so it waits under For Your Information. Its importance does not count."),
            BoardReason.JiraYourComment => L10n.T("Your comment is the latest in the issue; the next step is theirs."),
            BoardReason.JiraAssigned => L10n.T("Someone wrote in an issue assigned to you."),
            BoardReason.JiraReporter => L10n.T("Someone wrote in an issue you reported."),
            BoardReason.JiraCommented => L10n.T("Someone wrote in an issue you commented on."),
            BoardReason.JiraWatching => L10n.T("You only watch this issue."),
            BoardReason.Kept => L10n.T("The rules would no longer list it; your choice, a reminder, a deadline or a promise keeps it here."),
            _ => ReasonUnknown,
        };

        /// <summary>The reason of a rule this client does not know.</summary>
        public static string ReasonUnknown => L10n.T("The daemon’s rules put the case here.");

        /// <summary>The empty board's title, by how far the data is.</summary>
        public static string EmptyTitleOf(Phase phase) => phase switch
        {
            Phase.Loading => L10n.T("Loading the Board…"),
            Phase.Preparing => L10n.T("Preparing the Board…"),
            Phase.Unavailable or Phase.Failed or Phase.Unsupported => L10n.T("Board Unavailable"),
            Phase.Off => L10n.T("The Board Is Off"),
            _ => EmptyTitle,
        };

        /// <summary>The empty board's body, by how far the data is.</summary>
        public static string EmptyBodyOf(Phase phase) => phase switch
        {
            Phase.Loading => "",
            Phase.Preparing => Preparing,
            Phase.Unavailable => L10n.T("The board needs a running mail backend."),
            Phase.Failed => L10n.T("The board could not be loaded. Malachi Mail tries again shortly."),
            Phase.Unsupported => Unsupported,
            Phase.Off => L10n.T("The board is turned off in the settings. Your decisions are kept for when it is on again."),
            _ => EmptyBody,
        };

        /// <summary>The line above the cases while they are not the whole truth; "" when they are.</summary>
        public static string Notice(Phase phase, bool truncated) => phase switch
        {
            Phase.Preparing => Preparing,
            Phase.Unavailable => L10n.T("The mail backend is not running: the board shows what it knew last."),
            Phase.Failed => L10n.T("The board could not be loaded: it shows what it knew last. Malachi Mail tries again shortly."),
            Phase.Unsupported => Unsupported,
            _ => truncated ? L10n.T("Only the newest 1,000 cases are on the board.") : "",
        };

        private static string Unsupported =>
            L10n.T("This mail backend has no board. A newer Malachi Mail backend brings it.");

        private static string Preparing =>
            L10n.T("Malachi Mail is sorting your mail for the first time. This takes a minute or two.");

        /// <summary>The detail's note when the assistant's notes no longer count.</summary>
        public static string StaleNotes => L10n.T("The assistant’s notes are out of date: the conversation changed since.");

        /// <summary>The conversation of the detail while it loads.</summary>
        public static string MessagesLoading => L10n.T("Loading the conversation…");

        /// <summary>The conversation of the detail when it cannot load.</summary>
        public static string MessagesFailed => L10n.T("The conversation could not be loaded.");

        /// <summary>The link beside <see cref="MessagesFailed"/> that asks again.</summary>
        public static string TryAgain => L10n.T("Try Again");

        /// <summary>
        /// The mark in front of text the assistant wrote (a title, a "why"):
        /// the glyph of the summary's heading, in the assistant's colour. Not
        /// translated (board.AssistantMark).
        /// </summary>
        public const string AssistantMark = "✦";

        /// <summary>What the screen reader says for text the assistant wrote: "Assistant: Lunch on Friday".</summary>
        public static string SpokenAssistant(string text) => L10n.T("Assistant: %s", text);

        /// <summary>Shows the case's message in Mail.</summary>
        public static string ShowInMail => L10n.T("Show in Mail");

        /// <summary>Show in Mail could not find the message: the daemon said it is gone.</summary>
        public static string ShowInMailGone => L10n.T("Show in Mail failed: the message is no longer on the server.");

        /// <summary>Show in Mail could not find the message: the daemon could not be asked.</summary>
        public static string ShowInMailFailed => L10n.T("Show in Mail failed: the message could not be loaded.");

        /// <summary>Archives a case's messages.</summary>
        public static string Archive => L10n.T("Archive");

        /// <summary>The Done filter's section of the cases that come back later.</summary>
        public static string Snoozed =>
            // TRANSLATORS: a section of the board's Done filter: the cases off the
            // board until a reminder.
            L10n.T("Snoozed");

        /// <summary>"Back on the board Tomorrow 09:00", for a snoozed case.</summary>
        public static string SnoozedUntil(string label) =>
            // TRANSLATORS: %s is a day and a time, such as "Tomorrow 09:00" or
            // "20 Oct 09:00".
            L10n.T("Back on the board %s", label);

        /// <summary>The remind presets (<see cref="RemindPresets"/>).</summary>
        public static string RemindPreset(RemindPresetKind k) => k switch
        {
            // TRANSLATORS: a reminder preset: in about three hours, or at 18:00.
            RemindPresetKind.LaterToday => L10n.T("Later Today"),
            RemindPresetKind.Tomorrow => L10n.T("Tomorrow"),
            // TRANSLATORS: a reminder preset: next Monday at 09:00.
            RemindPresetKind.NextWeek => L10n.T("Next Week"),
            _ => "",
        };

        /// <summary>Ends a reminder.</summary>
        public static string RemindNoMore => L10n.T("Don’t Remind Me");

        /// <summary>What Archive did: messages moved, or only marked done.</summary>
        public static string Archived(int n, bool noArchive)
        {
            if (noArchive)
            {
                return L10n.T("Marked as done. This account has no archive.");
            }
            return n < 1
                ? L10n.T("Marked as done. No message was in the inbox.")
                : L10n.N("Archived %d message.", "Archived %d messages.", n);
        }

        /// <summary>What a write of the board did, for the toast of its failure (Swift <c>Board.Text.Action</c>).</summary>
        public enum Action
        {
            /// <summary>board.setState.</summary>
            Move,

            /// <summary>board.setDone, done.</summary>
            Done,

            /// <summary>board.setDone, not done.</summary>
            Reopen,

            /// <summary>board.remind.</summary>
            Remind,

            /// <summary>board.archive.</summary>
            Archive,

            /// <summary>board.setCommitment.</summary>
            Commitment,

            /// <summary>board.discardDraft.</summary>
            DiscardDraft,

            /// <summary>Unstar (board.unflag).</summary>
            Unflag,

            /// <summary>A change of the board's preferences (board.setPreferences).</summary>
            Preferences,
        }

        /// <summary>
        /// The toast of a failed write or load: what failed and, when the
        /// error says, why. Never the daemon's own message (it is not for the
        /// user).
        /// </summary>
        public static string Failed(Action action, Exception? error)
        {
            var what = action switch
            {
                Action.Move => L10n.T("Moving the case"),
                Action.Done => L10n.T("Marking the case done"),
                Action.Reopen => L10n.T("Moving the case back to the board"),
                Action.Remind => L10n.T("Setting the reminder"),
                Action.Archive => L10n.T("Archiving"),
                Action.Commitment => L10n.T("Changing the promise"),
                Action.DiscardDraft => L10n.T("Discarding the draft"),
                Action.Unflag => L10n.T("Removing the star"),
                Action.Preferences => L10n.T("Changing the board’s settings"),
                _ => "",
            };
            if (FailureReason(error, action) is not { } why)
            {
                // TRANSLATORS: a toast; %s is an action such as "Moving the case".
                return L10n.T("%s failed.", what);
            }
            // TRANSLATORS: a toast; the first %s is an action such as "Moving the
            // case", the second the reason, such as "the case is no longer on the
            // board".
            return L10n.T("%s failed: %s.", what, why);
        }

        private static string? FailureReason(Exception? error, Action action)
        {
            var (kind, e) = RpcErrorText.Classify(error);
            switch (kind)
            {
                case RpcErrorText.FailureKind.NoBackend:
                    return L10n.T("the mail backend is not running");
                case RpcErrorText.FailureKind.TimedOut:
                    return L10n.T("the mail backend did not answer in time");
                case RpcErrorText.FailureKind.Daemon when e is not null:
                    break;
                default:
                    return null;
            }
            // board.setCommitment's caseNotFound names the promise, not the
            // case (docs/api.md §2).
            if (action == Action.Commitment && e.Code.Value == ErrorCode.CaseNotFound)
            {
                return L10n.T("the promise no longer exists");
            }
            return e.Code.Value switch
            {
                ErrorCode.CaseNotFound => L10n.T("the case is no longer on the board"),
                ErrorCode.InvalidArgument => L10n.T("the board did not accept it"),
                ErrorCode.MethodNotFound or ErrorCode.NotImplemented => L10n.T("this mail backend has no board"),
                ErrorCode.StorageError => L10n.T("the mail backend could not save it"),
                ErrorCode.DraftNotFound => L10n.T("the draft no longer exists"),
                _ => null,
            };
        }

        // The run's model, cleaned; "" when unknown (the texts then say only
        // "the assistant").
        private static string ModelName(Run? run) => CleanLine(run?.Model, Cap.Model);

        /// <summary>The context menu's submenu of the states.</summary>
        public static string MoveTo =>
            // TRANSLATORS: a menu item whose submenu lists the board's states.
            L10n.T("Move To");

        /// <summary>The context menu's item that marks a case done.</summary>
        public static string MarkAsDone => L10n.T("Mark as Done");

        /// <summary>The context menu's submenu of the remind presets.</summary>
        public static string RemindMe => L10n.T("Remind Me");

        /// <summary>The accessibility name of the detail's state pill.</summary>
        public static string StateLabel => L10n.T("Status");

        /// <summary>The navigation column's second caption.</summary>
        public static string AccountsCaption => L10n.T("Accounts");

        /// <summary>The tooltip and accessibility name of a promise's tick.</summary>
        public static string MarkPromiseDone =>
            // TRANSLATORS: a button beside a promise the assistant found in the
            // user's reply.
            L10n.T("Mark Promise as Done");

        /// <summary>"1 promise", "3 promises" (the commitments' spoken count).</summary>
        public static string PromiseCount(int n) => L10n.N("%d promise", "%d promises", n);

        /// <summary>The sentence a deadline comes from, in quotation marks.</summary>
        public static string Quoted(string s) =>
            // TRANSLATORS: quotation marks around a sentence quoted from a message;
            // use your language's quotation marks.
            L10n.T("“%s”", s);
    }
}
