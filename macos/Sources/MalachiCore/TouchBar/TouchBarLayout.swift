// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The Touch Bar of the main window (docs/plans/touch-bar.md, T12): which
/// items a bar holds, in which order, and how the mail's items read for the
/// selection. Swift-first: the Touch Bar exists on the Mac only, so no Go
/// reference exists. AppKit builds the items (MalachiMail `MainTouchBar`)
/// and asks the window's validator whether each is enabled, as the
/// toolbar does; nothing here decides what an action may do.
public enum TouchBarLayout {
    /// The bars of the main window, one per mode (plan §4.1, §4.3, §4.4).
    public enum Bar: String, Sendable, CaseIterable {
        case mail, board, calendar

        /// AppKit keeps the user's arrangement of the bar under it.
        public var customizationIdentifier: String {
            "io.github.schotek.Malachi.touchbar.\(rawValue)"
        }

        /// The bar of a window mode.
        public static func of(_ mode: Board.Mode) -> Bar {
            switch mode {
            case .mail: .mail
            case .board: .board
            case .calendar: .calendar
            }
        }
    }

    /// The items (plan §4.1). The raw value ends the item's identifier.
    public enum Item: String, Sendable, CaseIterable {
        /// Mail · Board · Calendar (`setWindowMode:`).
        case mode
        case newMessage
        case checkMail
        /// Reply (Comment on an issue) · Reply All · Forward, those the
        /// account offers.
        case replies
        /// Archive · Move to Trash, those the account offers.
        case moves
        case star
        /// Move To: a popover with the account's folders (`moveToFolder:`,
        /// plan §4.2).
        case moveTo
        /// The issue's status, a popover with its transitions (Change
        /// Status), on an account that changes statuses.
        case status
        /// The Assistant: a popover with the toolbar menu's message
        /// actions, or the panel with In App; while the Assistant is shown.
        case assistant
        /// "3 messages selected" while several rows are; no action.
        case count
        // The palette's items, not in the default bar.
        case junk
        /// Mark as Read or Mark as Unread, whichever the selection allows,
        /// as the toolbar's one item.
        case markRead
        case loadImages
        case print
        // The Board's (plan §4.3).
        /// Done, or Move Back to Board for a done case (`boardDone:`).
        case boardDone
        /// Remind…: a popover with the presets (`BoardActions.remindMenu`).
        case boardRemind
        /// Archive · Delete, Delete while the account deletes mail.
        case boardMoves
        case boardReply
        /// The case's state: a popover with the four states.
        case caseState
        /// List · Columns · Today while no case is selected (T14).
        case boardStyle
        /// Triage (or Stop, Sign In…): while the board offers it.
        case triage
        // The Calendar's (plan §4.4).
        case calToday
        /// Previous · Next period (`Cal.step`).
        case calStep
        /// Day · Week · Month · Agenda, while no event is selected.
        case calView
        /// Join Video Call, for a selected event with a call.
        case calJoin
        /// Edit, for a selected event the user may change.
        case calEdit
        /// Accept · Tentative · Decline, for an unanswered invitation.
        case calRespond
        /// Show Invitation in Mail, for an unanswered invitation.
        case calShowInMail
        case newEvent
        /// The assistant panel of the Calendar (the toolbar's ✦).
        case calAssistant

        public var identifier: String {
            "io.github.schotek.Malachi.touchbar.item.\(rawValue)"
        }

        /// The item of an identifier, nil for AppKit's own.
        public init?(identifier: String) {
            let prefix = "io.github.schotek.Malachi.touchbar.item."
            guard identifier.hasPrefix(prefix) else { return nil }
            self.init(rawValue: String(identifier.dropFirst(prefix.count)))
        }
    }

    /// A place in a bar: an item, or one of AppKit's spaces, or the proxy
    /// where the responder with the keyboard puts its own items (a text
    /// field's word suggestions, plan T7).
    public enum Slot: Sendable, Equatable {
        case item(Item)
        case smallSpace
        case flexibleSpace
        case otherItems
    }

    /// The bar as it starts (and as Customize Touch Bar… resets it to).
    /// At most eight targets (T5): the mode switch counts as one.
    public static func defaultSlots(_ bar: Bar) -> [Slot] {
        switch bar {
        case .mail:
            return [
                .item(.mode), .item(.newMessage), .item(.checkMail), .smallSpace,
                .item(.replies), .smallSpace, .item(.moves), .item(.status), .item(.star), .item(.moveTo),
                .item(.count), .flexibleSpace, .item(.assistant), .otherItems,
            ]
        case .board:
            return [
                .item(.mode), .smallSpace, .item(.boardDone), .item(.boardRemind), .item(.boardMoves), .item(.boardReply),
                .flexibleSpace, .item(.caseState), .item(.boardStyle), .item(.triage), .otherItems,
            ]
        case .calendar:
            return [
                .item(.mode), .smallSpace, .item(.calToday), .item(.calStep), .smallSpace, .item(.calView), .item(.calJoin),
                .item(.calEdit), .item(.calRespond), .flexibleSpace, .item(.calShowInMail), .item(.newEvent),
                .item(.calAssistant), .otherItems,
            ]
        }
    }

    /// What Customize Touch Bar… offers for the bar.
    public static func allowedItems(_ bar: Bar) -> [Item] {
        switch bar {
        case .mail:
            return [
                .mode, .newMessage, .checkMail, .replies, .moves, .status, .star, .moveTo, .count, .assistant,
                .junk, .markRead, .loadImages, .print,
            ]
        case .board:
            return [.mode, .boardDone, .boardRemind, .boardMoves, .boardReply, .caseState, .boardStyle, .triage]
        case .calendar:
            return [
                .mode, .calToday, .calStep, .calView, .calJoin, .calEdit, .calRespond, .calShowInMail, .newEvent,
                .calAssistant,
            ]
        }
    }

    /// How the mail's items show for the selection (plan §4.1, T4): an
    /// action the account does not offer at all leaves the bar, as it
    /// leaves the toolbar (`ActionFlags.unsupported`); one the selection
    /// only does not allow now stays, disabled by the validator.
    public struct Mail: Sendable, Equatable {
        /// The replies group's actions, in order: `.reply`, `.replyAll`,
        /// `.forward`.
        public var replies: [MessageActionKind]
        /// The moves group's actions: `.archive`, `.trash`.
        public var moves: [MessageActionKind]
        /// Mark as Junk is offered (the palette's item).
        public var junk: Bool
        /// Move To is offered: the account moves mail (plan §4.2).
        public var moveTo: Bool
        /// The Assistant item is there (`AssistantController.shown`).
        public var assistant: Bool
        /// The issue's status while its account changes statuses (Change
        /// Status is offered), cut to the pill's length; "" when the row
        /// names none; nil without the item.
        public var status: String?
        /// Reply is "Comment" (`Jira.replyLabel`).
        public var comment: Bool
        /// Trash is "Cancel Sending": the row is queued in the outbox.
        public var outbox: Bool
        /// The star shows filled: the row (any member of a conversation)
        /// is starred.
        public var flagged: Bool
        /// The read toggle marks as read (else as unread), as the
        /// toolbar's item: while any selected row is unread.
        public var marksRead: Bool
        /// The count label's number: several rows selected, else nil.
        public var count: Int?

        /// What only a new bar can change (the items an account offers):
        /// AppKit cannot take an item out of a bar it shows.
        public var structure: Structure {
            Structure(replies: replies, moves: moves, junk: junk, moveTo: moveTo, assistant: assistant, status: status != nil)
        }

        public struct Structure: Sendable, Equatable {
            public var replies: [MessageActionKind]
            public var moves: [MessageActionKind]
            public var junk: Bool
            public var moveTo: Bool
            public var assistant: Bool
            public var status: Bool
        }
    }

    /// The mail's items for the selection's flags (`ActionFlags`, several
    /// rows already combined by `Selection.combineAccounts`), with the
    /// Assistant shown or not and the issue's `status` while Change Status
    /// is offered for the selection (nil otherwise), cut to `maxStatus`
    /// characters as the status pill cuts it.
    public static func mail(_ f: ActionFlags, assistant: Bool = false, status: String? = nil, maxStatus: Int = 24) -> Mail {
        let offered = { (k: MessageActionKind) in !f.unsupported.contains(k) }
        return Mail(
            replies: [MessageActionKind.reply, .replyAll, .forward].filter(offered),
            moves: [MessageActionKind.archive, .trash].filter(offered),
            junk: offered(.junk),
            moveTo: offered(.move),
            assistant: assistant,
            status: status.map { cut($0, maxStatus) },
            comment: f.comment,
            outbox: f.outbox,
            flagged: f.flagged,
            marksRead: f.markRead || !f.markUnread,
            count: f.count > 1 ? f.count : nil
        )
    }

    /// `text` cut to `max` characters with an ellipsis, as the pill does.
    static func cut(_ text: String, _ max: Int) -> String {
        guard max > 1, text.count > max else { return text }
        return String(text.prefix(max - 1)) + "…"
    }

    /// How the Board's items show (plan §4.3): with a case selected its
    /// actions and its state, without one the actions disabled (by the
    /// validator) and the style switch in the state's place.
    public struct BoardBar: Sendable, Equatable {
        /// A case is selected (the detail shows).
        public var hasCase: Bool
        /// The case is done: Done reads Move Back to Board.
        public var done: Bool
        /// Delete is offered: the case's account deletes mail; with no
        /// case it stays (disabled).
        public var delete: Bool
        /// The case's state as its pill names it, "" without a case.
        public var stateTitle: String
        /// The board's style, for the style switch.
        public var style: Board.Style
        /// Triage's title while the board offers it, nil otherwise.
        public var triage: String?

        /// What only a new bar can change.
        public var structure: Structure {
            Structure(hasCase: hasCase, delete: delete, triage: triage != nil)
        }

        public struct Structure: Sendable, Equatable {
            public var hasCase: Bool
            public var delete: Bool
            public var triage: Bool
        }
    }

    /// The Board's items for the selected case's `detail` (nil: none), the
    /// board's `style` and Triage's title while it is offered.
    public static func board(_ detail: Board.Detail?, style: Board.Style, triage: String?) -> BoardBar {
        BoardBar(
            hasCase: detail != nil,
            done: detail?.isDone ?? false,
            delete: detail?.canDelete ?? true,
            stateTitle: detail?.stateTitle ?? "",
            style: style,
            triage: triage
        )
    }

    /// What is selected in the Calendar, as the bar shows it.
    public enum CalendarSelection: Sendable, Equatable {
        case none
        /// An event; `join` while it has a video call, `edit` while the
        /// user may change it (not read-only, and they organise it or
        /// nobody is invited).
        case event(join: Bool, edit: Bool)
        /// An invitation the user has not answered; `inMail` while its
        /// message is known.
        case invitation(join: Bool, inMail: Bool)
    }

    /// How the Calendar's items show (plan §4.4).
    public struct CalendarBar: Sendable, Equatable {
        public var selection: CalendarSelection
        /// The view the view switch shows.
        public var view: Cal.View
        /// The assistant panel's button is there (`panelShown`).
        public var assistant: Bool

        /// What only a new bar can change: the selection's kind and its
        /// buttons, the assistant.
        public var structure: Structure {
            Structure(selection: selection, assistant: assistant)
        }

        public struct Structure: Sendable, Equatable {
            public var selection: CalendarSelection
            public var assistant: Bool
        }

        /// The items the selection leaves on the bar (the others' builders
        /// return nothing).
        public func shows(_ item: Item) -> Bool {
            switch item {
            case .calView:
                return selection == .none
            case .calJoin:
                switch selection {
                case .event(join: true, _), .invitation(join: true, _): return true
                default: return false
                }
            case .calEdit:
                if case .event(_, edit: true) = selection { return true }
                return false
            case .calRespond:
                if case .invitation = selection { return true }
                return false
            case .calShowInMail:
                if case .invitation(_, inMail: true) = selection { return true }
                return false
            case .calAssistant:
                return assistant
            default:
                return true
            }
        }
    }

    /// The Calendar's items for the selected occurrence (nil: none), the
    /// view and whether the assistant panel is offered.
    public static func calendar(_ selected: Occurrence?, view: Cal.View, assistant: Bool) -> CalendarBar {
        CalendarBar(selection: selection(of: selected), view: view, assistant: assistant)
    }

    /// An occurrence's kind for the bar: unanswered while the user is
    /// invited, does not organise it, has not answered and it is not
    /// cancelled (`Cal.canRespond` once the event is loaded).
    static func selection(of o: Occurrence?) -> CalendarSelection {
        guard let o, o.eventId != nil else { return .none }
        if !o.organizer, o.myResponse == .needsAction, o.status != .cancelled {
            return .invitation(join: o.videoCall, inMail: o.invitation != nil)
        }
        return .event(join: o.videoCall, edit: !o.readOnly && (o.organizer || o.attendeeCount == 0))
    }
}
