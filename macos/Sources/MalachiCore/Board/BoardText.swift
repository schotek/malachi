// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The board's texts: one to one with the Go reference ui/internal/board
// (text.go), which holds the msgids; every text goes through L10n with the
// msgid as the key, formats with the same placeholders. The strings that
// come from a case are cleaned before they are formatted in.
//
// Swift-first: the board was written here first; the Go package holds only
// its texts until the GTK client ports the rest.

import Foundation

extension Board {
    public enum Text {
        /// board.BoardName: the mode switch's segment and View menu item of
        /// the board, the window's title in it, the navigation's caption.
        public static var boardName: String { L10n.T("Board") }

        /// A state's name: its section, column, tile and pill.
        public static func stateName(_ s: State) -> String {
            switch s {
            case .hot: return L10n.T("Hot")
            case .you: return L10n.T("Waiting for You")
            case .them: return L10n.T("Waiting for Them")
            case .info: return L10n.T("For Your Information")
            }
        }

        /// A filter's name in the navigation column and the Show menu.
        public static func filterTitle(_ f: Filter) -> String {
            switch f {
            case .all: return L10n.T("Overview")
            case .state(let s): return stateName(s)
            case .done: return done
            }
        }

        public static var allAccounts: String { L10n.C("account filter", "All Accounts") }
        /// The Done filter, its section, and the detail's button.
        public static var done: String { L10n.T("Done") }

        /// "1 case", "23 cases".
        public static func caseCount(_ n: Int) -> String {
            L10n.N("%d case", "%d cases", n)
        }

        /// "1 message", "3 messages".
        public static func messageCount(_ n: Int) -> String {
            L10n.N("%d message", "%d messages", n)
        }

        /// The Today page's sentence under its title, for the hot cases and
        /// the ones waiting for the user.
        public static func todoPhrase(_ n: Int) -> String {
            guard n >= 1 else { return L10n.T("Nothing needs you today.") }
            return L10n.N("%d thing needs you today.", "%d things need you today.", n)
        }

        /// The row under the top cases waiting for the user.
        public static func andMore(_ n: Int) -> String {
            L10n.N("and %d more", "and %d more", n)
        }

        public static func dueGroupTitle(_ k: DueGroupKind) -> String {
            switch k {
            case .overdue: return L10n.T("Overdue")
            case .today: return L10n.T("Today")
            case .tomorrow: return L10n.T("Tomorrow")
            case .thisWeek: return L10n.T("Next 7 Days")
            case .later: return L10n.C("deadline", "Later")
            }
        }

        /// Who decided a case's state, under "Why is this here?". `run`
        /// names the assistant's model; it is cleaned here.
        public static func sourceText(_ source: StateSource, run: Run?) -> String {
            let model = modelName(run)
            switch source {
            case .rules:
                return L10n.T("The daemon’s rules set the state. The assistant has not looked at this case yet.")
            case .assistantKept:
                guard !model.isEmpty else {
                    return L10n.T("The daemon’s rules set the state and the assistant kept it.")
                }
                return L10n.T("The daemon’s rules set the state and the assistant (%s) kept it.", model)
            case .assistantChanged(let from):
                guard !model.isEmpty else {
                    return L10n.T("The assistant refined the state. The rules suggested: %s.", stateName(from))
                }
                return L10n.T("The assistant (%s) refined the state. The rules suggested: %s.", model, stateName(from))
            case .user:
                return L10n.T("You moved this case yourself.")
            case .assistantOff:
                return L10n.T("The daemon’s rules set the state; the assistant is off.")
            }
        }

        /// The status bar's line: who sorted the board.
        public static func statusLine(annotated: Bool, run: Run?) -> String {
            guard annotated else {
                return assistantOffLine
            }
            let model = modelName(run)
            var line = model.isEmpty
                ? L10n.T("Sorted by rules · refined by the assistant")
                : L10n.T("Sorted by rules · refined by the assistant (%s)", model)
            let note = cleanLine(run?.note ?? "", max: Cap.note)
            if !note.isEmpty {
                line += " · " + note
            }
            return line
        }

        /// The status line when the assistant does not refine the board.
        static var assistantOffLine: String { L10n.T("Sorted by the daemon’s rules · assistant off") }

        public static var noSelectionTitle: String { L10n.T("No Case Selected") }
        public static var noSelectionBody: String {
            L10n.T("Select a case to see its summary, a suggested reply and the conversation.")
        }
        public static var emptyTitle: String { L10n.T("Nothing on the Board") }
        public static var emptyBody: String { L10n.T("Cases from your accounts show up here as they arrive.") }
        /// A list with no row.
        public static var sectionEmpty: String { L10n.T("Nothing here.") }

        /// The placeholder of an empty column.
        public static func columnEmpty(_ s: State) -> String {
            s == .hot ? L10n.T("Nothing burning.") : L10n.T("Empty.")
        }

        /// The heading of the commitments.
        public static var fromAssistant: String { L10n.T("✦ From the Assistant") }
        public static var summaryHeading: String { L10n.T("✦ Summary from the Assistant") }
        public static var tasksHeading: String { L10n.T("✦ Tasks and Questions") }
        public static var draftHeading: String { L10n.T("Suggested Reply") }
        /// The note under the suggested reply: it stays on the board (a
        /// local draft, never in the Drafts folder) until it is sent.
        public static var draftNote: String { L10n.T("Only here on the board until you send it") }
        /// The detail's reply block while the suggested reply loads.
        public static var replyLoading: String { L10n.T("Loading the suggested reply…") }
        /// The detail's reply block when draft.get failed (`Text.tryAgain`
        /// beside it unless the draft is gone).
        public static var replyLoadFailed: String { L10n.T("The suggested reply could not be opened.") }
        /// The toast when the draft the inline editor edits was deleted
        /// elsewhere (`ComposeDraftController.onLost`).
        public static var replyRemoved: String { L10n.T("The suggested reply was removed elsewhere.") }
        /// The note in the reply block while what was typed in the suggested
        /// reply could not be saved (`BoardReplyPanes.Slot.pane(_, unsaved:)`).
        public static var replyNotSaved: String {
            L10n.T("This reply could not be saved yet; Malachi Mail keeps trying.")
        }
        /// The toast when a reply sent and then left was not sent
        /// (`BoardReplyPanes.sendFailed`); `title` is its subject.
        public static func replyNotSent(_ title: String) -> String {
            L10n.T("Your reply “%s” was not sent; it is still on the board.", Board.cleanLine(title, max: 80))
        }
        /// The question before quitting while a reply on the board could
        /// not be saved or sent (`BoardReplyPanes.finishAll`).
        public static var quitUnsavedHeading: String { L10n.T("Quit without saving a reply?") }
        public static var quitUnsavedBody: String {
            L10n.T("A reply on the board could not be saved or sent yet. If you quit now, what you typed in it may be lost.")
        }
        public static var quitAnyway: String { L10n.T("_Quit Anyway") }
        /// The detail's and the context menu's Unstar (`Detail.canUnstar`):
        /// the app's own wording for removing the flag.
        public static var unstar: String { L10n.T("Unstar") }
        public static var openDraft: String { L10n.T("Open Draft") }
        /// The GTK label without its mnemonic.
        public static var discard: String { withoutMnemonic(L10n.T("_Discard")) }
        public static var whyLink: String { L10n.T("Why is this here?") }
        public static var notDone: String { L10n.T("Move Back to Board") }
        public static var remind: String { L10n.T("Remind…") }
        public static var reply: String { L10n.T("Reply") }
        /// The GTK label without its mnemonic.
        public static var close: String { withoutMnemonic(L10n.T("_Close")) }
        public static var triage: String { L10n.T("✦ Triage") }
        /// The toast of an action this preview does not have yet.
        public static var later: String { L10n.T("Not in this preview yet.") }

        /// The style switch's segments.
        public static func styleTitle(_ s: Style) -> String {
            switch s {
            case .list: return L10n.C("board style", "List")
            case .columns: return L10n.C("board style", "Columns")
            case .today: return L10n.T("Today")
            }
        }

        /// The View menu's items of the styles.
        public static func styleMenuTitle(_ s: Style) -> String {
            switch s {
            case .list: return L10n.T("As List")
            case .columns: return L10n.T("As Columns")
            case .today: return styleTitle(.today)
            }
        }

        /// Settings → General → Board: the style of the first show after
        /// launch; its choices are `styleTitle`.
        public static var defaultStyleSetting: String { L10n.T("Default View") }

        // Beyond the plan's list: texts the view model needs.

        /// The sender of the user's own messages in the conversation.
        public static var you: String { L10n.T("You") }

        /// "Conversation · 3 messages".
        public static func conversation(_ n: Int) -> String {
            L10n.T("Conversation · %s", messageCount(n))
        }

        /// The Today page's deadlines and its calendar placeholder.
        public static var deadlines: String { L10n.T("Deadlines") }
        public static var dueEmpty: String {
            L10n.T("No deadlines. The assistant finds deadlines in the text of messages and keeps the sentence each one comes from.")
        }
        public static var calendarTitle: String { L10n.T("Calendar and Reminders") }
        public static var calendarBody: String {
            L10n.T("Later. Read-only; where the data comes from differs on each platform.")
        }
        /// The commitments' tile.
        public static var commitments: String { L10n.T("Promised") }

        /// Parts of a row's spoken label.
        public static func spokenDue(_ label: String) -> String { L10n.T("Due %s", label) }
        public static func spokenRemind(_ label: String) -> String { snoozedUntil(label) }
        public static var spokenAttachments: String { L10n.T("Has attachments") }
        public static var spokenUnread: String { L10n.C("board row", "Unread") }

        // The daemon's board (docs/api.md §4.13).

        /// Why the rules put a case where it is, by the rule's code. The
        /// codes are an open set: one this client does not know gets
        /// `reasonUnknown`.
        public static func reason(_ code: BoardReason) -> String {
            switch code.rawValue {
            case "hot.important": return L10n.T("The newest message is marked as important, addressed to you and from a sender you have written to.")
            case "hot.flagged": return L10n.T("You flagged a message in this conversation and the newest one is not yours.")
            case "you.addressed": return L10n.T("The newest message is addressed to you by a sender you have written to.")
            case "you.repliedToYou": return L10n.T("The newest message answers one of yours.")
            case "them.replied": return L10n.T("You replied last; the next step is theirs.")
            case "them.asked": return L10n.T("You asked a question and wait for the answer.")
            case "info.ccOnly": return L10n.T("You are only in Cc on the newest message.")
            case "info.notAddressed": return L10n.T("The message is not addressed to you (a mailing list or a Bcc).")
            case "info.yourNote": return L10n.T("A note to yourself.")
            case "info.unknownSender": return L10n.T("The message is addressed to you, but its sender is one you have never written to, so it waits under For Your Information. Its importance does not count.")
            case "jira.yourComment": return L10n.T("Your comment is the latest in the issue; the next step is theirs.")
            case "jira.assigned": return L10n.T("Someone wrote in an issue assigned to you.")
            case "jira.reporter": return L10n.T("Someone wrote in an issue you reported.")
            case "jira.commented": return L10n.T("Someone wrote in an issue you commented on.")
            case "jira.watching": return L10n.T("You only watch this issue.")
            case "kept": return L10n.T("The rules would no longer list it; your choice, a reminder, a deadline or a promise keeps it here.")
            default: return reasonUnknown
            }
        }

        /// The reason of a rule this client does not know.
        public static var reasonUnknown: String { L10n.T("The daemon’s rules put the case here.") }

        /// The empty board's title and body, by how far the data is.
        public static func emptyTitle(_ phase: Phase) -> String {
            switch phase {
            case .loading: return L10n.T("Loading the Board…")
            case .preparing: return L10n.T("Preparing the Board…")
            case .ready: return emptyTitle
            case .unavailable, .failed, .unsupported: return L10n.T("Board Unavailable")
            case .off: return L10n.T("The Board Is Off")
            }
        }

        public static func emptyBody(_ phase: Phase) -> String {
            switch phase {
            case .loading: return ""
            case .preparing: return preparing
            case .ready: return emptyBody
            case .unavailable: return L10n.T("The board needs a running mail backend.")
            case .failed: return L10n.T("The board could not be loaded. Malachi Mail tries again shortly.")
            case .unsupported: return unsupported
            case .off:
                return L10n.T("The board is turned off in the settings. Your decisions are kept for when it is on again.")
            }
        }

        /// The line above the cases while they are not the whole truth; ""
        /// when they are.
        public static func notice(_ phase: Phase, truncated: Bool) -> String {
            switch phase {
            case .preparing: return preparing
            case .unavailable: return L10n.T("The mail backend is not running: the board shows what it knew last.")
            case .failed:
                return L10n.T("The board could not be loaded: it shows what it knew last. Malachi Mail tries again shortly.")
            case .unsupported: return unsupported
            case .loading, .ready, .off: return truncated ? L10n.T("Only the newest 1,000 cases are on the board.") : ""
            }
        }

        private static var unsupported: String {
            L10n.T("This mail backend has no board. A newer Malachi Mail backend brings it.")
        }

        private static var preparing: String {
            L10n.T("Malachi Mail is sorting your mail for the first time. This takes a minute or two.")
        }

        /// The detail's note when the assistant's notes no longer count.
        public static var staleNotes: String {
            L10n.T("The assistant’s notes are out of date: the conversation changed since.")
        }

        /// The conversation of the detail while it loads, and when it
        /// cannot.
        public static var messagesLoading: String { L10n.T("Loading the conversation…") }
        public static var messagesFailed: String { L10n.T("The conversation could not be loaded.") }
        /// The link beside `messagesFailed` that asks again.
        public static var tryAgain: String { L10n.T("Try Again") }

        /// The mark in front of text the assistant wrote (a title, a "why"):
        /// the glyph of the summary's heading, in the assistant's colour. Not
        /// translated (board.AssistantMark).
        public static var assistantMark: String { "✦" }

        /// What VoiceOver says for text the assistant wrote: "Assistant:
        /// Lunch on Friday".
        public static func spokenAssistant(_ text: String) -> String {
            L10n.T("Assistant: %s", text)
        }

        public static var showInMail: String { L10n.T("Show in Mail") }

        /// Show in Mail could not find the message: the daemon said it is
        /// gone, or it could not be asked.
        public static var showInMailGone: String {
            L10n.T("Show in Mail failed: the message is no longer on the server.")
        }
        public static var showInMailFailed: String { L10n.T("Show in Mail failed: the message could not be loaded.") }
        public static var archive: String { L10n.T("Archive") }

        /// The Done filter's section of the cases that come back later.
        public static var snoozed: String { L10n.T("Snoozed") }

        /// "Back on the board Tomorrow 09:00", for a snoozed case.
        public static func snoozedUntil(_ label: String) -> String {
            L10n.T("Back on the board %s", label)
        }

        /// The remind presets (`Board.remindPresets`) and ending a remind.
        public static func remindPreset(_ k: RemindPreset.Kind) -> String {
            switch k {
            case .laterToday: return L10n.T("Later Today")
            case .tomorrow: return L10n.T("Tomorrow")
            case .nextWeek: return L10n.T("Next Week")
            }
        }

        public static var remindNoMore: String { L10n.T("Don’t Remind Me") }

        /// What Archive did: messages moved, or only marked done.
        public static func archived(_ n: Int, noArchive: Bool) -> String {
            if noArchive {
                return L10n.T("Marked as done. This account has no archive.")
            }
            guard n >= 1 else { return L10n.T("Marked as done. No message was in the inbox.") }
            return L10n.N("Archived %d message.", "Archived %d messages.", n)
        }

        /// What a write of the board did, for the toast of its failure.
        public enum Action: Sendable, CaseIterable {
            case move, done, reopen, remind, archive, commitment, discardDraft
            /// Unstar (`board.unflag`).
            case unflag
            /// A change of the board's preferences (`board.setPreferences`).
            case preferences
        }

        /// The toast of a failed write or load: what failed and, when the
        /// error says, why. Never the daemon's own message (it is not for
        /// the user).
        public static func failed(_ action: Action, _ error: any Error) -> String {
            let what: String
            switch action {
            case .move: what = L10n.T("Moving the case")
            case .done: what = L10n.T("Marking the case done")
            case .reopen: what = L10n.T("Moving the case back to the board")
            case .remind: what = L10n.T("Setting the reminder")
            case .archive: what = L10n.T("Archiving")
            case .commitment: what = L10n.T("Changing the promise")
            case .discardDraft: what = L10n.T("Discarding the draft")
            case .unflag: what = L10n.T("Removing the star")
            case .preferences: what = L10n.T("Changing the board’s settings")
            }
            guard let why = failureReason(error, action) else { return L10n.T("%s failed.", what) }
            return L10n.T("%s failed: %s.", what, why)
        }

        private static func failureReason(_ error: any Error, _ action: Action) -> String? {
            // board.setCommitment's caseNotFound names the promise, not the
            // case (docs/api.md §2).
            if action == .commitment, let e = error as? RPCError, e.code == .caseNotFound {
                return L10n.T("the promise no longer exists")
            }
            if let e = error as? RPCClient.ClientError {
                switch e {
                case .notConnected, .disconnected: return L10n.T("the mail backend is not running")
                case .timeout: return L10n.T("the mail backend did not answer in time")
                case .transport: return nil
                }
            }
            if error is CancellationError {
                return L10n.T("the mail backend did not answer in time")
            }
            guard let e = error as? RPCError else { return nil }
            switch e.code {
            case .caseNotFound: return L10n.T("the case is no longer on the board")
            case .invalidArgument: return L10n.T("the board did not accept it")
            case .methodNotFound, .notImplemented: return L10n.T("this mail backend has no board")
            case .storageError: return L10n.T("the mail backend could not save it")
            case .draftNotFound: return L10n.T("the draft no longer exists")
            default: return nil
            }
        }

        /// The run's model, cleaned; "" when unknown (the texts then say
        /// only "the assistant").
        private static func modelName(_ run: Run?) -> String {
            cleanLine(run?.model ?? "", max: Cap.model)
        }

        // The board's view literals (MalachiMail/Board, the View menu).

        /// The context menu's submenu of the states.
        public static var moveTo: String { L10n.T("Move To") }
        /// The context menu's item that marks a case done.
        public static var markAsDone: String { L10n.T("Mark as Done") }
        /// The context menu's submenu of the remind presets.
        public static var remindMe: String { L10n.T("Remind Me") }
        /// The accessibility name of the detail's state pill.
        public static var stateLabel: String { L10n.T("Status") }
        /// The navigation column's second caption.
        public static var accountsCaption: String { L10n.T("Accounts") }
        /// The tooltip and accessibility name of a promise's tick.
        public static var markPromiseDone: String { L10n.T("Mark Promise as Done") }

        /// "1 promise", "3 promises" (the commitments' spoken count).
        public static func promiseCount(_ n: Int) -> String {
            L10n.N("%d promise", "%d promises", n)
        }

        /// The sentence a deadline comes from, in quotation marks.
        public static func quoted(_ s: String) -> String {
            L10n.T("“%s”", s)
        }
    }
}
