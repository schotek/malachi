// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// One event detail popover (docs/calendar.md §5.5): loads the event
// (event.get, cached by the source), its rows (`Cal.detailOf`), and runs
// the flows of its buttons: the answer (with the series question and
// Decline Without Reply), Remove from Calendar / Delete (with
// `Cal.removeQuestion`, else the Mail preference confirm-delete and
// `Cal.deleteQuestion`, then the series question), Edit (the series
// question, then the editor), Show Invitation in Mail and Join Video Call
// (hand-offs). Questions are data the popover presents (`onQuestion`,
// `onScope`); a write's error shows inline under the footer (`error`), a
// conflict reloads the event and says so.

import Foundation

@MainActor
public final class EventDetailController {
    public let source: any CalendarDataSource
    public let env: Cal.Env
    public let eventId: EventID
    public let recurrenceId: DateOrTime?
    private let confirmDelete: @MainActor () -> Bool

    // MARK: Outputs

    public var onChange: (@MainActor () -> Void)?
    /// Asks a question (Cancel always there); nil answers Cancel.
    public var onQuestion: (@MainActor (Cal.Question) async -> Cal.Answer)?
    /// Asks which occurrences of a series (`Cal.ScopeQuestion`); nil is
    /// Cancel.
    public var onScope: (@MainActor (Cal.ScopeQuestion) async -> EditScope?)?
    /// Edit: the editor for this event and scope (`EventEditorManager`).
    public var onEdit: (@MainActor (Event, EditScope) -> Void)?
    /// Show Invitation in Mail: Mail mode, that message revealed.
    public var onShowInvitation: (@MainActor (MessageRef) -> Void)?
    /// Join Video Call, a link of the notes: through the link
    /// confirmation (`MessageActionDelegate.openLink`).
    public var onOpenLink: (@MainActor (String) -> Void)?
    /// The event is gone (removed here): the popover closes.
    public var onClose: (@MainActor () -> Void)?
    /// ✦ Find Another Time: the editor with Next Free Slot.
    public var onFindAnotherTime: (@MainActor (Event) -> Void)?
    /// ✦ Ask about This Event: the assistant panel.
    public var onAsk: (@MainActor (Event) -> Void)?

    // MARK: State

    /// The occurrence the popover was opened for (shown until the event
    /// arrives).
    public private(set) var occurrence: Occurrence?
    public private(set) var event: Event?
    public private(set) var loading = false
    /// A write is on its way: the buttons wait.
    public private(set) var busy = false
    /// The inline error under the footer ("" none).
    public private(set) var error = ""
    /// The attendee list beyond `Cal.maxAttendeesShown` is open.
    public private(set) var attendeesExpanded = false

    private var observer: CalendarObserverToken?
    private var generation = 0
    private var closed = false

    /// `confirmDelete` reads the Mail preference "Ask before moving to the
    /// Trash" (`Settings.confirmDelete`).
    public init(
        source: any CalendarDataSource, eventId: EventID, recurrenceId: DateOrTime?, occurrence: Occurrence? = nil,
        env: Cal.Env = Cal.Env(zone: .current, locale: .current), confirmDelete: @escaping @MainActor () -> Bool = { true }
    ) {
        self.source = source
        self.eventId = eventId
        self.recurrenceId = recurrenceId
        self.occurrence = occurrence
        self.env = env
        self.confirmDelete = confirmDelete
    }

    /// For an occurrence of a view; nil for one without an event (an
    /// invitation not in a calendar: its card is in Mail).
    public convenience init?(
        source: any CalendarDataSource, occurrence o: Occurrence, env: Cal.Env = Cal.Env(zone: .current, locale: .current),
        confirmDelete: @escaping @MainActor () -> Bool = { true }
    ) {
        guard let id = o.eventId, !id.rawValue.isEmpty else { return nil }
        self.init(source: source, eventId: id, recurrenceId: o.recurrenceId, occurrence: o, env: env, confirmDelete: confirmDelete)
    }

    public func start() {
        guard observer == nil else { return }
        observer = source.observe { [weak self] c in
            self?.sourceChanged(c)
        }
        load()
    }

    /// The popover closed: nothing is taken any more.
    public func stop() {
        closed = true
        observer?.cancel()
        observer = nil
        generation += 1
    }

    // MARK: Derived

    /// The popover's rows (nil until the event arrived).
    public var detail: Cal.Detail? {
        guard let e = event else { return nil }
        let s = source.snapshot
        return Cal.detailOf(
            e, accountName: s.accountName(e.accountId), calendarName: e.calendarId.map(s.calendarName) ?? "", env: env)
    }

    /// The attendee rows to show (collapsed beyond `Cal.maxAttendeesShown`).
    public var shownAttendees: [Cal.AttendeeRow] {
        guard let d = detail else { return [] }
        return d.collapsed && !attendeesExpanded ? Array(d.attendees.prefix(Cal.maxAttendeesShown)) : d.attendees
    }

    /// "Show All %d Attendees" ("" when nothing is hidden).
    public var showAllAttendees: String {
        guard let d = detail, d.collapsed, !attendeesExpanded else { return "" }
        return Cal.showAllAttendees(d.attendees.count)
    }

    /// The "You: …" popup's choices: Accept, Tentative, Decline (the menu
    /// of Decline adds Decline Without Reply).
    public var responseChoices: [EventResponse] { Cal.responseChoices }

    public func expandAttendees() {
        attendeesExpanded = true
        changed()
    }

    // MARK: Loading

    public func load(reload: Bool = false) {
        generation += 1
        let g = generation
        loading = true
        changed()
        let source = source
        let id = eventId
        let rid = recurrenceId
        Task { [weak self] in
            let outcome: Result<Event, any Error>
            do {
                outcome = .success(try await source.event(id, recurrenceId: rid, reload: reload))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, g == self.generation else { return }
            self.loading = false
            switch outcome {
            case .success(let e):
                self.event = e
                self.occurrence = e.occurrence
            case .failure(let err):
                self.error = Cal.errorMessage(err)
            }
            self.changed()
        }
    }

    private func sourceChanged(_ c: Cal.SourceChange) {
        switch c {
        case .event(let id) where id == eventId:
            load()
        case .occurrences(let from, let to):
            if let o = occurrence ?? event?.occurrence, let (s, e) = Cal.span(o, env.zone),
               !Cal.rangesOverlap(s, e, from: from, to: to) {
                return
            }
            load()
        default:
            break
        }
    }

    // MARK: Respond

    /// The "You: …" popup: answers with `response`; with a series asks
    /// This Event / All Events first. `withoutReply` is Decline Without
    /// Reply (`sendResponse: false`).
    public func respond(_ response: EventResponse, withoutReply: Bool = false) {
        guard let e = event, Cal.canRespond(e), !busy else { return }
        Task { [weak self] in
            await self?.runRespond(e, response, withoutReply: withoutReply)
        }
    }

    func runRespond(_ e: Event, _ response: EventResponse, withoutReply: Bool) async {
        guard let scope = await scope(for: .respond, e) else { return }
        let s = source.snapshot
        let mail = withoutReply ? nil : Cal.mailTexts(Cal.mailKindOf(response), e.occurrence, me: s.myName(e.accountId), env: env)
        let params = EventRespondParams(
            eventId: eventId, recurrenceId: e.recurrenceId ?? recurrenceId, scope: scope, etag: e.etag, response: response,
            sendResponse: withoutReply ? false : nil, mail: mail)
        await write { source in
            let r = try await source.respond(params)
            self.event = r.event
        }
    }

    // MARK: Remove

    /// Remove from Calendar / Delete, ⌫: the questions, then event.delete.
    public func remove() {
        guard let e = event, e.canDelete, !busy else { return }
        Task { [weak self] in
            await self?.runRemove(e)
        }
    }

    func runRemove(_ e: Event) async {
        var notify: Bool?
        if let q = Cal.removeQuestion(e) {
            switch await ask(q) {
            case .cancel: return
            case .confirm: notify = true
            case .alternative: notify = false
            }
        } else if confirmDelete() {
            guard await ask(Cal.deleteQuestion(e)) == .confirm else { return }
        }
        guard let scope = await scope(for: .delete, e) else { return }
        let s = source.snapshot
        var mail: MailText?
        if notify == true {
            let kind: Cal.MailKind = e.removeIsDelete ? .cancel : .declined
            mail = Cal.mailTexts(kind, e.occurrence, me: s.myName(e.accountId), env: env)
        }
        let params = EventDeleteParams(
            eventId: eventId, recurrenceId: e.recurrenceId ?? recurrenceId, scope: scope, etag: e.etag, notify: notify, mail: mail)
        let ok = await write { source in
            _ = try await source.deleteEvent(params)
        }
        if ok {
            onClose?()
        }
    }

    // MARK: Hand-offs

    /// Edit: with a series the scope first, then the editor.
    public func edit() {
        guard let e = event, e.canEdit, !busy else { return }
        Task { [weak self] in
            guard let self, let scope = await self.scope(for: .edit, e) else { return }
            self.onEdit?(e, scope)
        }
    }

    public func showInvitation() {
        guard let m = event?.invitationMessages?.first else { return }
        onShowInvitation?(m)
    }

    public func joinVideoCall() {
        guard let url = event?.videoCallLink?.url, !url.isEmpty else { return }
        onOpenLink?(url)
    }

    /// A link of the notes (`Detail.links`).
    public func openLink(_ url: String) {
        guard event?.links?.contains(url) == true else { return }
        onOpenLink?(url)
    }

    public func findAnotherTime() {
        guard let e = event else { return }
        onFindAnotherTime?(e)
    }

    public func askAssistant() {
        guard let e = event else { return }
        onAsk?(e)
    }

    // MARK: Helpers

    private func ask(_ q: Cal.Question) async -> Cal.Answer {
        guard let onQuestion else { return .cancel }
        return await onQuestion(q)
    }

    /// The scope of a write: `all` for a single event, else the series
    /// question's answer (nil: cancelled).
    private func scope(for action: Cal.ScopeAction, _ e: Event) async -> EditScope? {
        guard e.recurring || e.recurrence != nil else { return .all }
        let q = Cal.scopeQuestionFor(action, complex: e.recurrence?.complex ?? false)
        if q.choices.count == 1 {
            return q.choices[0].scope
        }
        guard let onScope else { return nil }
        return await onScope(q)
    }

    /// Runs a write with the buttons waiting; its error goes inline (a
    /// conflict reloads the event the source stored). True when it went
    /// through.
    @discardableResult
    private func write(_ body: @MainActor (any CalendarDataSource) async throws -> Void) async -> Bool {
        busy = true
        error = ""
        changed()
        defer {
            busy = false
            changed()
        }
        do {
            try await body(source)
            return true
        } catch {
            guard !closed else { return false }
            self.error = Cal.errorMessage(error)
            if let e = error as? RPCError, e.code == .conflict {
                if let c = e.conflictEvent {
                    event = c.event
                } else {
                    load(reload: true)
                }
            }
            return false
        }
    }

    private func changed() {
        guard !closed else { return }
        onChange?()
    }
}
