// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// One New Event / Edit Event window (docs/calendar.md §5.6): the input
// (`Cal.Input`) with its validation, the writable calendars, the Repeat
// and Alert choices, the attendees through the compose window's
// `RecipientTokens` model, the attendees' availability (calendar.freeBusy
// 500 ms after the last change, only when the calendar's account has
// `features.freeBusy`), Next Free Slot (calendar.findFreeTime), the
// attachments (attachment.import), the primary button and the footer
// (`Cal.primaryLabel`, `Cal.footer`), and the save paths: event.create, or
// event.update with the scope the detail asked. Nothing is stored before
// Save (D6): closing with changes asks Save / Discard / Cancel, and
// Discard just closes.

import Foundation

@MainActor
public final class EventEditorController {
    public enum Mode: Sendable, Equatable {
        case new
        /// The event and the scope the detail's series question chose.
        case edit(Event, EditScope)
    }

    /// A choice of the Calendar popup.
    public struct CalendarChoice: Sendable, Equatable {
        public var id: CalendarID
        public var accountId: AccountID
        /// "Account · Calendar".
        public var title: String
        public var color: String
    }

    /// How long the availability waits after the last change.
    public static let availabilityDebounce: Duration = .milliseconds(500)
    /// How far Next Free Slot looks.
    public static let freeSlotDays = 14

    public let source: any CalendarDataSource
    public let env: Cal.Env
    public let mode: Mode
    private let now: @MainActor () -> Date
    private let sleep: Cal.Sleep

    // MARK: Outputs

    public var onChange: (@MainActor () -> Void)?
    /// Asks a question (the close question); nil answers Cancel.
    public var onQuestion: (@MainActor (Cal.Question) async -> Cal.Answer)?
    /// Saved: the window closes (`EventEditorManager` forgets it).
    public var onSaved: (@MainActor (Event) -> Void)?
    /// The window may close.
    public var onClose: (@MainActor () -> Void)?
    /// Asks a question of Delete (Cancel the default, the confirmation
    /// destructive); nil asks through `onQuestion`.
    public var onDeleteQuestion: (@MainActor (Cal.Question) async -> Cal.Answer)?
    /// The Mail preference "Ask before moving to the Trash"
    /// (`Settings.confirmDelete`), as the detail reads it.
    public var confirmDelete: @MainActor () -> Bool = { true }

    // MARK: State

    public private(set) var input: Cal.Input
    public private(set) var repeatChoice: Cal.Repeat
    /// The rule sent (the choice's, a custom one, the event's own).
    public private(set) var recurrence: Recurrence?
    /// The Alert popup's minutes before the start (-1 None).
    public private(set) var alert: Int
    /// The attendee field's model; `editAttendees` changes it.
    public private(set) var attendees = RecipientTokens()
    public private(set) var attachments: [DraftAttachment] = []
    /// The video call (Add Video Call ▾: a provider of `videoProviders`,
    /// or Paste Link… with an https URL); nil none.
    public private(set) var videoCall: VideoCallInput?
    /// The problems of the last Save (cleared by the next change).
    public private(set) var problems: [Cal.Problem] = []
    /// calendar.freeBusy's answer for the current input (nil none).
    public private(set) var availability: CalendarFreeBusyResult?
    /// The Next Free Slot line ("" none).
    public private(set) var freeSlotText = ""
    /// Something was changed since the window opened.
    public private(set) var dirty = false
    public private(set) var saving = false
    /// The inline error of Save or Attach ("" none).
    public private(set) var error = ""

    private let original: Recurrence?
    private var availabilityTask: Task<Void, Never>?
    private var availabilityGeneration = 0
    private var closed = false

    /// `defaults` (New Event) from `Cal.newEventDefaults`; `calendarId` a
    /// preset calendar (else the first writable, default first).
    public init(
        source: any CalendarDataSource, mode: Mode = .new, defaults: Cal.Defaults? = nil, calendarId: CalendarID? = nil,
        env: Cal.Env = Cal.Env(zone: .current, locale: .current), now: @escaping @MainActor () -> Date = { Date() },
        sleep: @escaping Cal.Sleep = Cal.taskSleep
    ) {
        self.source = source
        self.mode = mode
        self.env = env
        self.now = now
        self.sleep = sleep
        switch mode {
        case .new:
            let d = defaults ?? Cal.newEventDefaults(at: now(), origin: .now, preferences: source.snapshot.preferences, zone: env.zone)
            let cal = calendarId ?? source.snapshot.writableCalendars.first?.id
            input = Cal.Input(allDay: d.allDay, start: d.start, end: d.end, zone: env.zone, calendarId: cal)
            alert = d.alerts.first ?? -1
            repeatChoice = .never
            recurrence = nil
            original = nil
        case .edit(let e, _):
            let zone = e.timeZone.flatMap { TimeZone(identifier: $0.rawValue) } ?? env.zone
            let span = Cal.span(e.occurrence, zone) ?? (start: now(), end: now())
            input = Cal.Input(
                title: e.title, allDay: e.allDay, start: span.start, end: e.allDay ? span.end : span.end, zone: zone,
                location: e.location ?? "", notes: e.notes ?? "", calendarId: e.calendarId)
            alert = e.alerts?.first ?? -1
            recurrence = e.recurrence
            original = e.recurrence
            repeatChoice = Cal.repeatOf(e.recurrence, start: span.start, zone: zone)
            if let v = e.videoCallLink, !v.url.isEmpty {
                videoCall = VideoCallInput(mode: .link, url: v.url)
            }
            for a in e.attendees ?? [] where !a.me && !a.organizer {
                attendees.add(Address(name: a.name, address: a.address))
            }
        }
        input.alerts = alert >= 0 ? [alert] : []
        syncAttendees()
    }

    /// Starts the availability for the first input.
    public func start() {
        scheduleAvailability()
    }

    /// The window closed: nothing is taken any more.
    public func stop() {
        closed = true
        availabilityTask?.cancel()
        availabilityTask = nil
    }

    // MARK: Derived

    public var windowTitle: String {
        if case .edit = mode { return Cal.Text.editEvent }
        return Cal.Text.newEvent
    }

    /// The Calendar popup: the writable calendars (Edit: the event's own).
    public var calendars: [CalendarChoice] {
        let s = source.snapshot
        let records: [CalendarRecord]
        if case .edit(let e, _) = mode {
            records = e.calendarId.flatMap(s.calendar).map { [$0] } ?? []
        } else {
            records = s.writableCalendars
        }
        return records.map {
            CalendarChoice(
                id: $0.id, accountId: $0.accountId, title: Cal.Text.dot(s.accountName($0.accountId), Cal.cleanLine($0.name)),
                color: $0.color)
        }
    }

    private var calendarAccount: CalendarAccount? {
        let s = source.snapshot
        guard let id = input.calendarId, let c = s.calendar(id) else { return nil }
        return s.calendarAccount(c.accountId)
    }

    public var repeatChoices: [Cal.Repeat] { Cal.repeats }
    /// The Alert popup's values: day-based at 9:00 for an all-day event.
    public var alertChoices: [Int] { Cal.alertChoices(allDay: input.allDay) }

    /// The Alert popup's title of `minutes` for the current All Day state.
    public func alertTitle(_ minutes: Int) -> String { Cal.alertTitle(minutes, allDay: input.allDay) }

    /// Save / Save and Send Invitations.
    public var primaryLabel: String {
        guard let a = calendarAccount else { return Cal.primaryLabel(attendees: 0, source: .caldav, features: noFeatures) }
        return Cal.primaryLabel(attendees: input.attendees.count, source: a.source, features: a.features)
    }

    /// ⌘S saves only without attendees (⌘↩ is the primary).
    public var saveKeyAllowed: Bool { input.attendees.isEmpty }

    /// The footer's lines: who sends the invitations, the attachments.
    public var footer: [String] {
        let f = calendarAccount?.features ?? noFeatures
        let s = source.snapshot
        let mail = f.mailAccountId ?? calendarAccount?.accountId
        return Cal.footer(
            attendees: input.attendees.count, features: f, mailAccount: mail.map(s.accountName) ?? "",
            mailAddress: mail.map(s.myAddress) ?? "", attachments: attachments.map(\.filename))
    }

    private var noFeatures: CalendarFeatures {
        CalendarFeatures(freeBusy: false, proposeNewTime: false, invitations: .none, onlineMeetings: false)
    }

    /// The problem lines of the last Save.
    public var problemTexts: [String] { problems.map(Cal.problemText) }

    /// The availability rows are shown (the account answers for others).
    public var showsAvailability: Bool {
        (calendarAccount?.features.freeBusy ?? false) && !input.attendees.isEmpty
    }

    /// The day the availability rows span.
    public var availabilityDay: (from: Date, to: Date) {
        let d = Cal.startOfDay(input.start, env.zone)
        return (d, Cal.addDays(d, 1, env.zone))
    }

    public var availabilityRows: [Cal.AvailabilityRow] {
        guard showsAvailability, let a = availability else { return [] }
        var names: [String: String] = [:]
        for t in attendees.tokens {
            if let ad = t.address { names[ad.address] = t.label }
        }
        let day = availabilityDay
        return Cal.availabilityRows(a, names: names, from: day.from, to: day.to)
    }

    public var availabilitySummary: [String] {
        guard showsAvailability, let a = availability else { return [] }
        return Cal.summary(a, start: input.start, end: input.end, env: env)
    }

    /// The providers Add Video Call offers: the chosen calendar's
    /// `onlineMeetingProviders` when its account can create meetings.
    public var videoProviders: [String] {
        let s = source.snapshot
        guard calendarAccount?.features.onlineMeetings == true, let id = input.calendarId, let c = s.calendar(id) else {
            return []
        }
        return c.onlineMeetingProviders ?? []
    }

    /// The time-zone link's text.
    public var timeZoneLink: String { Cal.timeZoneLink(input.zone.identifier) }

    // MARK: Editing

    private func edited(availability again: Bool = false) {
        dirty = true
        problems = []
        error = ""
        if again {
            scheduleAvailability()
        }
        changed()
    }

    public func setTitle(_ s: String) {
        guard s != input.title else { return }
        input.title = s
        edited()
    }

    public func setAllDay(_ on: Bool) {
        guard on != input.allDay else { return }
        input.allDay = on
        if on {
            input.start = Cal.startOfDay(input.start, input.zone)
            let end = Cal.startOfDay(input.end, input.zone)
            input.end = end > input.start ? end : Cal.addDays(input.start, 1, input.zone)
        } else {
            let p = Cal.parts(input.start, input.zone)
            input.start = Cal.date(p.year, p.month, p.day, 9, 0, in: input.zone)
            input.end = input.start.addingTimeInterval(Cal.defaultDuration(source.snapshot.preferences))
        }
        // The alert follows: 15 minutes → 1 day before (9:00) and back to
        // the preference's default; None stays None.
        alert = Cal.alertOnAllDayToggle(alert, on: on, preferences: source.snapshot.preferences)
        input.alerts = alert >= 0 ? [alert] : []
        edited(availability: true)
    }

    /// The start; the end moves with it (the duration stays).
    public func setStart(_ t: Date) {
        guard t != input.start else { return }
        let d = input.end.timeIntervalSince(input.start)
        input.start = t
        input.end = t.addingTimeInterval(max(d, 0))
        edited(availability: true)
    }

    public func setEnd(_ t: Date) {
        guard t != input.end else { return }
        input.end = t
        edited(availability: true)
    }

    /// The time-zone popover: the wall-clock times stay, the zone changes.
    public func setZone(_ z: TimeZone) {
        guard z.identifier != input.zone.identifier else { return }
        let s = Cal.parts(input.start, input.zone)
        let e = Cal.parts(input.end, input.zone)
        input.zone = z
        input.start = Cal.date(s.year, s.month, s.day, s.hour, s.minute, in: z)
        input.end = Cal.date(e.year, e.month, e.day, e.hour, e.minute, in: z)
        edited(availability: true)
    }

    /// Add Video Call ▾ / Paste Link… / remove (nil or mode none). A link
    /// must be an https URL with a host, a meeting one of `videoProviders`;
    /// false (nothing changed) otherwise.
    @discardableResult
    public func setVideoCall(_ v: VideoCallInput?) -> Bool {
        var next = v
        switch v?.mode {
        case nil, .none?:
            next = nil
        case .link?:
            let url = Cal.trimSpace(v?.url ?? "")
            guard let c = URLComponents(string: url), c.scheme?.lowercased() == "https", !(c.host ?? "").isEmpty else {
                return false
            }
            next = VideoCallInput(mode: .link, url: url)
        case .create?:
            guard let p = v?.provider, videoProviders.contains(p) else { return false }
            next = VideoCallInput(mode: .create, provider: p)
        default:
            return false
        }
        guard next != videoCall else { return true }
        videoCall = next
        edited()
        return true
    }

    public func setLocation(_ s: String) {
        guard s != input.location else { return }
        input.location = s
        edited()
    }

    public func setNotes(_ s: String) {
        guard s != input.notes else { return }
        input.notes = s
        edited()
    }

    public func setCalendar(_ id: CalendarID) {
        guard id != input.calendarId, calendars.contains(where: { $0.id == id }) else { return }
        input.calendarId = id
        // A meeting of a provider the new calendar does not offer goes.
        if let v = videoCall, v.mode == .create, !videoProviders.contains(v.provider ?? "") {
            videoCall = nil
        }
        edited(availability: true)
    }

    /// The Repeat popup (Custom… opens the popover: `setRecurrence`).
    public func setRepeat(_ c: Cal.Repeat) {
        guard c != .custom else { return }
        repeatChoice = c
        recurrence = Cal.repeatRecurrence(c, start: input.start, zone: input.zone)
        edited()
    }

    /// The Custom… popover's rule.
    public func setRecurrence(_ r: Recurrence) {
        recurrence = r
        repeatChoice = Cal.repeatOf(r, start: input.start, zone: input.zone)
        edited()
    }

    public func setAlert(_ minutes: Int) {
        guard minutes != alert else { return }
        alert = minutes
        input.alerts = minutes >= 0 ? [minutes] : []
        edited()
    }

    /// The attendee field: the view applies the keystroke, paste or token
    /// change to the model here.
    public func editAttendees(_ change: (inout RecipientTokens) -> Void) {
        let before = attendees
        change(&attendees)
        guard attendees != before else { return }
        let resolvedBefore = input.attendees
        syncAttendees()
        edited(availability: input.attendees != resolvedBefore)
    }

    private func syncAttendees() {
        input.attendees = attendees.resolved().addresses.map { a in
            EventInputAttendee(name: Cal.cleanLine(a.name).isEmpty ? nil : a.name, address: a.address)
        }
    }

    // MARK: Availability

    private func scheduleAvailability() {
        availabilityTask?.cancel()
        availabilityTask = nil
        availabilityGeneration += 1
        guard showsAvailability, let account = calendarAccount?.accountId else {
            if availability != nil {
                availability = nil
            }
            return
        }
        let g = availabilityGeneration
        let day = availabilityDay
        let params = CalendarFreeBusyParams(
            accountId: account, attendees: Array(input.attendees.prefix(API.Limits.maxFreeBusyAttendees).map(\.address)),
            from: day.from, to: day.to)
        let sleep = sleep
        let source = source
        availabilityTask = Task { [weak self] in
            await sleep(Self.availabilityDebounce)
            guard !Task.isCancelled, let self, g == self.availabilityGeneration else { return }
            let r = try? await source.freeBusy(params)
            guard !self.closed, g == self.availabilityGeneration else { return }
            self.availabilityTask = nil
            self.availability = r
            self.changed()
        }
    }

    /// A request is waiting or on its way (the tests).
    var availabilityPending: Bool { availabilityTask != nil }

    /// Next Free Slot: the first free slot of the event's length after its
    /// start, in the next two weeks; moves the event there.
    public func nextFreeSlot() {
        guard let account = calendarAccount?.accountId, !input.allDay else { return }
        let minutes = max(15, Int(input.end.timeIntervalSince(input.start) / 60))
        let from = max(input.start, now())
        let params = CalendarFindFreeTimeParams(
            accountId: account, attendees: input.attendees.map(\.address), durationMinutes: minutes, from: from,
            to: Cal.addDays(from, Self.freeSlotDays, env.zone), limit: 5)
        let source = source
        Task { [weak self] in
            do {
                let r = try await source.findFreeTime(params)
                guard let self, !self.closed else { return }
                if let slot = Cal.nextFreeSlot(r, after: from) {
                    self.input.start = slot.start
                    self.input.end = slot.end
                    self.freeSlotText = Cal.nextFreeSlotText(slot, env: self.env)
                    self.edited(availability: true)
                } else {
                    self.freeSlotText = Cal.noFreeSlot
                    self.changed()
                }
            } catch {
                guard let self, !self.closed else { return }
                self.error = Cal.errorMessage(error)
                self.changed()
            }
        }
    }

    // MARK: Attachments

    /// Attach: attachment.import of a file the user picked, under the
    /// calendar's account.
    public func attach(path: String) {
        guard let account = calendarAccount?.accountId ?? calendars.first?.accountId else { return }
        guard attachments.count < API.Limits.maxEventAttachments else { return }
        let source = source
        Task { [weak self] in
            do {
                let r = try await source.importAttachment(AttachmentImportParams(accountId: account, path: path))
                guard let self, !self.closed else { return }
                self.attachments.append(r.attachment)
                self.edited()
            } catch {
                guard let self, !self.closed else { return }
                self.error = Cal.errorMessage(error, what: L10n.T("Saving the attachment"))
                self.changed()
            }
        }
    }

    public func removeAttachment(_ id: String) {
        let n = attachments.count
        attachments.removeAll { $0.id == id }
        if attachments.count != n {
            edited()
        }
    }

    // MARK: Save and close

    /// What Save would send (the tests, and nothing else).
    var eventInput: EventInput {
        var e = Cal.eventInput(input, zone: input.zone)
        if case .edit(_, let scope) = mode {
            if scope == .this {
                e.recurrence = nil
            } else if let o = original, o.complex, recurrence == o {
                e.keepRecurrence = true
            } else {
                e.recurrence = recurrence
            }
        } else {
            e.recurrence = recurrence
        }
        e.videoCall = videoCall
        if !attachments.isEmpty {
            e.attachments = attachments.map(\.id)
        }
        return e
    }

    /// The primary button: validates, then event.create or event.update.
    /// True when it was stored (the window then closes).
    @discardableResult
    public func save() async -> Bool {
        guard !saving else { return false }
        var p = Cal.validate(input)
        if attendees.hasInvalid && !p.contains(.invalidAttendee) {
            p.append(.invalidAttendee)
        }
        problems = p
        guard p.isEmpty, let cal = input.calendarId else {
            changed()
            return false
        }
        saving = true
        error = ""
        changed()
        defer {
            saving = false
            changed()
        }
        let s = source.snapshot
        let account = s.calendar(cal)?.accountId ?? AccountID("")
        let hasAttendees = !input.attendees.isEmpty
        let o = Cal.bareOccurrence(
            title: input.title, start: input.allDay ? nil : input.start, end: input.allDay ? nil : input.end,
            startDate: input.allDay ? Cal.dateOf(input.start, input.zone) : nil,
            endDate: input.allDay ? Cal.dateOf(input.end, input.zone) : nil, allDay: input.allDay,
            location: input.location.isEmpty ? nil : input.location)
        do {
            let saved: Event
            switch mode {
            case .new:
                let mail = hasAttendees ? Cal.mailTexts(.invitation, o, me: s.myName(account), env: env) : nil
                saved = try await source.createEvent(EventCreateParams(
                    calendarId: cal, event: eventInput, sendInvitations: hasAttendees ? true : nil, mail: mail)).event
            case .edit(let e, let scope):
                let mail = hasAttendees ? Cal.mailTexts(.update, o, me: s.myName(account), env: env) : nil
                guard let id = e.eventId else { return false }
                saved = try await source.updateEvent(EventUpdateParams(
                    eventId: id, recurrenceId: scope == .all ? nil : e.recurrenceId, scope: scope, etag: e.etag,
                    event: eventInput, sendUpdates: hasAttendees ? true : nil, mail: mail)).event
            }
            guard !closed else { return true }
            dirty = false
            onSaved?(saved)
            return true
        } catch {
            guard !closed else { return false }
            self.error = Cal.errorMessage(error)
            return false
        }
    }

    /// Edit mode's footer button at the left ("Delete" / "Remove from
    /// Calendar"); nil without one (New Event, an event that cannot go).
    public var deleteTitle: String? {
        guard case .edit(let e, _) = mode else { return nil }
        return Cal.editorDelete(editing: true, e)
    }

    /// The footer's Delete: the detail's flow (`EventDetailController
    /// .runRemove`) with the scope the editor was opened with — the remove
    /// question (confirm = tell the organiser / attendees, alternative =
    /// remove only), else the delete question when the preference asks,
    /// then event.delete. Success closes the window without the unsaved
    /// question; a refusal shows inline. True when it was deleted.
    @discardableResult
    public func delete() async -> Bool {
        guard case .edit(let e, let scope) = mode, e.canDelete, let id = e.eventId, !saving else { return false }
        let ask: (Cal.Question) async -> Cal.Answer = { [weak self] q in
            guard let self else { return .cancel }
            if let f = self.onDeleteQuestion { return await f(q) }
            return await self.onQuestion?(q) ?? .cancel
        }
        var notify: Bool?
        if let q = Cal.removeQuestion(e) {
            switch await ask(q) {
            case .cancel: return false
            case .confirm: notify = true
            case .alternative: notify = false
            }
        } else if confirmDelete() {
            guard await ask(Cal.deleteQuestion(e)) == .confirm else { return false }
        }
        guard !closed else { return false }
        let s = source.snapshot
        var mail: MailText?
        if notify == true {
            let kind: Cal.MailKind = e.removeIsDelete ? .cancel : .declined
            mail = Cal.mailTexts(kind, e.occurrence, me: s.myName(e.accountId), env: env)
        }
        let params = EventDeleteParams(
            eventId: id, recurrenceId: e.recurrenceId, scope: scope, etag: e.etag, notify: notify, mail: mail)
        saving = true
        error = ""
        changed()
        defer {
            saving = false
            changed()
        }
        do {
            _ = try await source.deleteEvent(params)
            guard !closed else { return true }
            dirty = false
            onClose?()
            return true
        } catch {
            guard !closed else { return false }
            self.error = Cal.errorMessage(error)
            return false
        }
    }

    /// The window's close button: with changes asks Save / Discard /
    /// Cancel. True when the window may close.
    @discardableResult
    public func close() async -> Bool {
        guard dirty else {
            onClose?()
            return true
        }
        let answer = await onQuestion?(Cal.closeQuestion) ?? .cancel
        switch answer {
        case .cancel:
            return false
        case .alternative:
            dirty = false
            onClose?()
            return true
        case .confirm:
            guard await save() else { return false }
            onClose?()
            return true
        }
    }

    private func changed() {
        guard !closed else { return }
        onChange?()
    }
}
