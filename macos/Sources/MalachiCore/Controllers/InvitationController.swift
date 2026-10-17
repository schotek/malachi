// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The invitation card of one message in Mail (docs/calendar.md §5.7): the
// card of `Message.invitation` (`message.get`), which card it is
// (`Cal.cardModeOf` with the account's switch), its lines, the target
// calendar ("Add to:"), and its actions: Accept / Tentative / Decline (and
// Decline Without Reply), Change Response, Propose New Time…, Remove from
// Calendar of a cancellation (invitation.applyCancel), Show in Calendar
// and Join Video Call (hand-offs). The answer's card replaces the shown
// one; an error shows inline.

import Foundation

@MainActor
public final class InvitationController {
    /// A choice of the "Add to:" popup.
    public struct TargetChoice: Sendable, Equatable {
        public var id: CalendarID
        /// "Account · Calendar".
        public var title: String
        public var color: String
        public var isDefault: Bool
    }

    public let source: any CalendarDataSource
    public let env: Cal.Env
    public let accountId: AccountID
    public let messageId: MessageID
    /// The account's "Show the Invitation Card above the Message in Mail".
    public let cardOn: Bool

    // MARK: Outputs

    public var onChange: (@MainActor () -> Void)?
    /// Show in Calendar: Calendar mode, the Day view of `day` with the
    /// event selected (`CalendarController.show(day:select:)`).
    public var onShowInCalendar: (@MainActor (_ eventId: EventID?, _ day: Date?) -> Void)?
    /// Join Video Call: through the link confirmation.
    public var onOpenLink: (@MainActor (String) -> Void)?

    // MARK: State

    public private(set) var card: InvitationCard
    /// The "Add to:" calendar (nil: the answer only).
    public private(set) var target: CalendarID?
    public private(set) var busy = false
    /// The inline error ("" none).
    public private(set) var error = ""
    /// invitation.applyCancel removed the event.
    public private(set) var cancelApplied = false
    /// The folder invitation.respond moved the message to (After Answering).
    public private(set) var movedTo: FolderID?

    public init(
        source: any CalendarDataSource, accountId: AccountID, messageId: MessageID, card: InvitationCard, cardOn: Bool = true,
        env: Cal.Env = Cal.Env(zone: .current, locale: .current)
    ) {
        self.source = source
        self.accountId = accountId
        self.messageId = messageId
        self.card = card
        self.cardOn = cardOn
        self.env = env
        target = Self.defaultTarget(card)
    }

    /// The default target: the card's linked calendar, else the default of
    /// `targets`, else the first.
    static func defaultTarget(_ c: InvitationCard) -> CalendarID? {
        if let id = c.calendarId, c.targets.contains(where: { $0.calendarId == id }) || c.fixedTarget {
            return id
        }
        return (c.targets.first(where: \.isDefault) ?? c.targets.first)?.calendarId
    }

    // MARK: Derived

    public var mode: Cal.CardMode { Cal.cardModeOf(card, cardOn: cardOn) }
    public var pill: Cal.InvitationPill { Cal.pillKind(card.summary) }
    public var pillTitle: String { Cal.pillTitle(pill) }
    public var title: String { Cal.cardTitle(card.summary) }
    public var dateTile: Cal.DateTile? { Cal.dateTileOf(card.summary, env: env) }
    public var timeLine: String { Cal.cardTimeLine(card, env: env) }
    public var location: String { Cal.cleanLine(card.location) }
    /// The video call's host ("" none); Join Video Call opens it.
    public var videoHost: String { Cal.cleanLine(card.videoCall?.host) }
    public var peopleLine: String { Cal.cardPeopleLine(card) }
    public var previousLine: String { Cal.previousLine(card, env: env) }
    public var conflictLine: String {
        let s = source.snapshot
        let line = Cal.conflictLine(card.conflicts, calendarName: s.calendarName, env: env)
        return line.isEmpty ? Cal.noConflict : line
    }

    /// The day strip's span and busy blocks (`card.day`).
    public var day: InvitationDay { card.day }

    public var showsResponses: Bool { Cal.showsResponses(card) }
    public var showsStateStrip: Bool { Cal.showsStateStrip(card) }
    public var showsProposeNewTime: Bool { Cal.showsProposeNewTime(card) }

    /// "Add to:" is a popup (false: a label with the fixed calendar, or
    /// nothing without targets).
    public var targetIsPopup: Bool { !card.fixedTarget && card.targets.count > 1 }

    public var targets: [TargetChoice] {
        let s = source.snapshot
        return card.targets.map { t in
            let c = s.calendar(t.calendarId)
            return TargetChoice(
                id: t.calendarId,
                title: Cal.Text.dot(c.map { s.accountName($0.accountId) } ?? "", Cal.cleanLine(c?.name)),
                color: c?.color ?? "", isDefault: t.isDefault)
        }
    }

    public var replyNote: [String] {
        Cal.replyNote(card, accountName: source.snapshot.accountName(card.replyAccountId ?? accountId))
    }

    /// "✓ Accepted · in calendar Work · Calendar".
    public var stateStrip: String {
        let s = source.snapshot
        let name = card.calendarId.map { id in Cal.Text.dot(s.accountName(s.calendar(id)?.accountId ?? accountId), s.calendarName(id)) }
        return Cal.stateStrip(card, calendarName: name ?? "")
    }

    /// The line of an invitation that cannot be answered ("" none).
    public var stateLine: String { Cal.stateLine(card) }
    /// A reply's one-line card.
    public var replyCard: String { Cal.replyCard(card) }

    // MARK: Actions

    public func setTarget(_ id: CalendarID) {
        guard !card.fixedTarget, card.targets.contains(where: { $0.calendarId == id }), id != target else { return }
        target = id
        changed()
    }

    /// Accept / Tentative / Decline, Change Response, Decline Without Reply
    /// (`withoutReply`).
    public func respond(_ response: EventResponse, withoutReply: Bool = false) {
        guard !busy, card.canRespond else { return }
        run(response, withoutReply: withoutReply, proposed: nil)
    }

    /// Propose New Time…: the answer with the proposed start and end
    /// (Tentative unless `response` says otherwise).
    public func proposeNewTime(start: Date, end: Date, response: EventResponse = .tentative) {
        guard !busy, card.canProposeNewTime, end > start else { return }
        run(response, withoutReply: false, proposed: TimeSlot(start: start, end: end))
    }

    private func run(_ response: EventResponse, withoutReply: Bool, proposed: TimeSlot?) {
        let s = source.snapshot
        let replyAccount = card.replyAccountId ?? accountId
        let mail = withoutReply
            ? nil : Cal.mailTexts(Cal.mailKindOf(response), Cal.occurrenceOf(card), me: s.myName(replyAccount), env: env)
        let calendarId = response == .declined || card.fixedTarget ? nil : target
        let params = InvitationRespondParams(
            accountId: accountId, messageId: messageId, response: response, calendarId: calendarId,
            sendResponse: withoutReply ? false : nil, proposedStart: proposed?.start, proposedEnd: proposed?.end, mail: mail)
        busy = true
        error = ""
        changed()
        let source = source
        Task { [weak self] in
            do {
                let r = try await source.respondToInvitation(params)
                guard let self else { return }
                self.card = r.invitation
                self.movedTo = r.movedTo
                if let id = r.invitation.calendarId {
                    self.target = id
                }
            } catch {
                self?.error = Cal.errorMessage(error)
            }
            self?.busy = false
            self?.changed()
        }
    }

    /// Remove from Calendar of a cancellation.
    public func applyCancel() {
        guard !busy, mode == .cancelled, !cancelApplied else { return }
        busy = true
        error = ""
        changed()
        let source = source
        let params = InvitationApplyCancelParams(accountId: accountId, messageId: messageId)
        Task { [weak self] in
            do {
                let r = try await source.applyCancel(params)
                self?.cancelApplied = true
                _ = r
            } catch {
                self?.error = Cal.errorMessage(error)
            }
            self?.busy = false
            self?.changed()
        }
    }

    public func showInCalendar() {
        onShowInCalendar?(card.eventId, card.start ?? Cal.parseDate(card.startDate, env.zone))
    }

    public func joinVideoCall() {
        guard let url = card.videoCall?.url, !url.isEmpty else { return }
        onOpenLink?(url)
    }

    /// The card of a newer message.get (the message changed).
    public func replace(_ c: InvitationCard) {
        guard c != card else { return }
        card = c
        target = Self.defaultTarget(c)
        changed()
    }

    private func changed() {
        onChange?()
    }
}
