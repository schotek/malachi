// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The event editor's controller (EventEditorController) over the
// in-memory samples: the defaults of a new event, the calendars, the
// validation, the attendees through RecipientTokens, the primary label and
// the footer, the availability's debounce and its account rule, Next Free
// Slot, Attach, the save paths with their params and the close question.

@MainActor
private func editor(
    _ src: InMemoryCalendarSource = calSamples(), mode: EventEditorController.Mode = .new, calendarId: CalendarID? = nil,
    clock: CalManualClock? = nil
) -> EventEditorController {
    var sleep: Cal.Sleep = Cal.taskSleep
    if let clock {
        sleep = { d in await clock.sleep(d) }
    }
    let d = Cal.newEventDefaults(at: calNow, origin: .now, preferences: src.snapshot.preferences, zone: calZone)
    let e = EventEditorController(
        source: src, mode: mode, defaults: d, calendarId: calendarId, env: calEnv, now: { calNow }, sleep: sleep)
    e.start()
    return e
}

private func event(_ src: InMemoryCalendarSource, _ id: String) -> Event {
    MainActor.assumeIsolated { src.data.events.first { $0.eventId?.rawValue == id }! }
}

@MainActor
@Suite(.serialized) struct CalendarEventEditorControllerTests {
    @Test func newEventDefaults() {
        let e = editor()
        #expect(e.windowTitle == Cal.Text.newEvent)
        #expect(e.input.start == Cal.date(2026, 10, 14, 10, 30, in: calZone))
        #expect(e.input.end == Cal.date(2026, 10, 14, 11, 30, in: calZone))
        #expect(e.alert == 15 && e.input.alerts == [15])
        #expect(e.input.calendarId == Cal.sampleWorkCalendar)
        // Writable calendars only: no birthdays or holidays.
        #expect(e.calendars.map(\.id) == [Cal.sampleWorkCalendar, Cal.samplePersonal, Cal.sampleFamily, Cal.sampleSport])
        #expect(e.calendars.first?.title == "Work · Calendar")
        #expect(e.primaryLabel == "Save" && e.footer.isEmpty && !e.dirty)
        #expect(e.saveKeyAllowed)
    }

    @Test func allDayRemapsTheAlert() {
        let e = editor()
        #expect(e.alert == 15 && e.alertChoices == Cal.alertChoices)
        e.setAllDay(true)
        // 15 minutes → 1 day before (9:00), the day-based choices.
        #expect(e.alert == Cal.allDayAlertDefault && e.input.alerts == [Cal.allDayAlertDefault])
        #expect(e.alertChoices == Cal.allDayAlertChoices)
        #expect(e.alertTitle(e.alert) == Cal.alertTitle(Cal.allDayAlertDefault, allDay: true))
        // Off: back to the preference's default.
        e.setAllDay(false)
        #expect(e.alert == 15 && e.input.alerts == [15])
        #expect(e.alertChoices == Cal.alertChoices)
        // None stays None both ways.
        e.setAlert(-1)
        e.setAllDay(true)
        #expect(e.alert == -1 && e.input.alerts == [])
        e.setAllDay(false)
        #expect(e.alert == -1 && e.input.alerts == [])
    }

    @Test func validationBlocksSave() async {
        let src = calSamples()
        let e = editor(src)
        e.setEnd(e.input.start.addingTimeInterval(-60))
        #expect(e.dirty)
        #expect(await e.save() == false)
        #expect(e.problems == [.endBeforeStart])
        #expect(e.problemTexts.count == 1)
        #expect(src.writes.isEmpty)
        // A change clears the problems.
        e.setTitle("Lunch")
        #expect(e.problems.isEmpty)
    }

    @Test func startMovesTheEnd() {
        let e = editor()
        e.setStart(Cal.date(2026, 10, 15, 14, 0, in: calZone))
        #expect(e.input.end == Cal.date(2026, 10, 15, 15, 0, in: calZone))
        e.setAllDay(true)
        #expect(e.input.start == Cal.date(2026, 10, 15, 0, 0, in: calZone))
        #expect(e.input.end == Cal.date(2026, 10, 16, 0, 0, in: calZone))
        e.setZone(TimeZone(identifier: "America/New_York")!)
        #expect(e.timeZoneLink == "Time zone: America/New_York")
    }

    @Test func attendeesLabelFooterAndInvalid() async {
        let src = calSamples()
        let e = editor(src)
        e.editAttendees { $0.paste("Jana <jana@example.org>, nonsense") }
        #expect(e.input.attendees.map(\.address) == ["jana@example.org"])
        #expect(e.primaryLabel == "Save and Send Invitations")
        #expect(e.footer == ["The server sends the invitations."])
        #expect(!e.saveKeyAllowed)
        #expect(await e.save() == false)
        #expect(e.problems.contains(.invalidAttendee))
        // The CalDAV calendar sends by mail through the Gmail account.
        e.setCalendar(Cal.sampleSport)
        #expect(e.footer == ["Invitations leave by e-mail from account Gmail (me@example.com) through the Outbox."])
    }

    @Test func availabilityIsDebouncedAndOnlyWithFreeBusy() async throws {
        let clock = CalManualClock()
        let src = calSamples()
        let e = editor(src, clock: clock)
        #expect(!e.showsAvailability && !e.availabilityPending)
        e.editAttendees { $0.paste("jana@example.org, petr@example.org,") }
        e.editAttendees { $0.paste("eva@example.org,") }
        #expect(e.showsAvailability)
        try await calWaitUntil { await clock.pending.count == 2 }
        #expect(await clock.pending.allSatisfy { $0 == .milliseconds(500) })
        await clock.fire()
        try await calWaitUntil { !e.availabilityPending }
        #expect(src.reads.filter { $0 == API.CalendarFreeBusy.name }.count == 1)
        #expect(e.availabilityRows.map(\.label) == ["You", "jana@example.org", "petr@example.org", "eva@example.org"])
        #expect(e.availabilityRows.dropFirst().allSatisfy { !$0.known })
        #expect(!e.availabilitySummary.isEmpty)
        // Gmail's account cannot answer for others: no request.
        e.setCalendar(Cal.samplePersonal)
        #expect(!e.showsAvailability && !e.availabilityPending)
        #expect(e.availabilityRows.isEmpty)
    }

    @Test func nextFreeSlotMovesTheEvent() async throws {
        let src = calSamples()
        let slot = TimeSlot(start: Cal.date(2026, 10, 15, 13, 0, in: calZone), end: Cal.date(2026, 10, 15, 14, 0, in: calZone))
        src.findFreeTimeResult = CalendarFindFreeTimeResult(slots: [slot])
        let e = editor(src)
        e.nextFreeSlot()
        try await calWaitUntil { !e.freeSlotText.isEmpty }
        #expect(e.input.start == slot.start && e.input.end == slot.end)
        #expect(e.freeSlotText.hasPrefix("Next free slot: "))
        src.findFreeTimeResult = CalendarFindFreeTimeResult()
        let none = editor(src)
        none.nextFreeSlot()
        try await calWaitUntil { !none.freeSlotText.isEmpty }
        #expect(none.freeSlotText == Cal.noFreeSlot)
    }

    @Test func saveCreatesWithInvitations() async throws {
        let src = calSamples()
        let e = editor(src)
        var saved: [Event] = []
        e.onSaved = { saved.append($0) }
        e.setTitle("  Roadmap  ")
        e.setLocation("Room D")
        e.setRepeat(.weekly)
        e.setAlert(-1)
        e.editAttendees { $0.paste("jana@example.org,") }
        e.attach(path: "/tmp/agenda.pdf")
        try await calWaitUntil { !e.attachments.isEmpty }
        #expect(e.footer.contains("Attachment: agenda.pdf"))
        #expect(await e.save())
        guard case .create(let p) = src.writes.last else {
            Issue.record("no create")
            return
        }
        #expect(p.calendarId == Cal.sampleWorkCalendar && p.sendInvitations == true)
        #expect(p.event.title == "Roadmap" && p.event.location == "Room D" && p.event.alerts == [])
        #expect(p.event.start == LocalDateTime("2026-10-14T10:30:00") && p.event.timeZone == "Europe/Prague")
        #expect(p.event.recurrence == Recurrence(frequency: .weekly, interval: 1, byDay: ["WE"]))
        #expect(p.event.attachments == [e.attachments[0].id])
        #expect(p.mail?.subject == "Invitation: Roadmap")
        #expect(saved.count == 1 && !e.dirty)
        if case .importAttachment(let a) = src.writes.first {
            #expect(a.accountId == Cal.sampleWork && a.path == "/tmp/agenda.pdf")
        } else {
            Issue.record("no import")
        }
    }

    @Test func editUpdatesWithTheScope() async {
        let src = calSamples()
        let offsite = event(src, "ev_sample_offsite")
        let e = editor(src, mode: .edit(offsite, .all))
        #expect(e.windowTitle == Cal.Text.editEvent)
        #expect(e.input.allDay && e.input.title == "Team offsite")
        #expect(e.calendars.map(\.id) == [Cal.sampleWorkCalendar])
        e.setTitle("Team offsite 2026")
        #expect(await e.save())
        guard case .update(let p) = src.writes.last else {
            Issue.record("no update")
            return
        }
        #expect(p.scope == .all && p.etag == "\"1\"" && p.recurrenceId == nil && p.mail == nil)
        #expect(p.event.allDay && p.event.startDate == offsite.startDate && p.event.endDate == offsite.endDate)
        // One occurrence of the standup: no rule in the exception.
        let standup = event(src, "ev_sample_standup")
        let one = editor(src, mode: .edit(standup, .this))
        #expect(one.repeatChoice == .custom || one.repeatChoice == .weekly)
        one.setTitle("Standup (moved)")
        #expect(await one.save())
        guard case .update(let q) = src.writes.last else {
            Issue.record("no update")
            return
        }
        #expect(q.scope == .this && q.recurrenceId == standup.recurrenceId && q.event.recurrence == nil)
    }

    @Test func deleteRunsTheDetailsFlow() async {
        // New Event has no Delete.
        #expect(editor().deleteTitle == nil)
        // An invitation: Cancel, Remove Only, Remove and Decline.
        let src = calSamples()
        let planning = event(src, "ev_sample_planning")
        let e = editor(src, mode: .edit(planning, .all))
        #expect(e.deleteTitle == Cal.removeTitle(planning))
        var answer = Cal.Answer.cancel
        var asked: [Cal.Question] = []
        var saveQuestions = 0
        e.onQuestion = { _ in saveQuestions += 1; return .cancel }
        e.onDeleteQuestion = { q in asked.append(q); return answer }
        var closed = 0
        e.onClose = { closed += 1 }
        e.setTitle("changed")
        #expect(await e.delete() == false)
        #expect(asked.first?.heading == "Also tell the organiser you won’t attend?")
        #expect(src.writes.isEmpty && closed == 0)
        answer = .alternative
        #expect(await e.delete())
        guard case .delete(let p) = src.writes.last else {
            Issue.record("no delete")
            return
        }
        #expect(p.eventId == planning.eventId && p.scope == .all && p.notify == false && p.mail == nil)
        // Closes without the unsaved-changes question.
        #expect(closed == 1 && saveQuestions == 0 && !e.dirty)

        let src2 = calSamples()
        let d = editor(src2, mode: .edit(event(src2, "ev_sample_planning"), .all))
        d.onDeleteQuestion = { _ in .confirm }
        #expect(await d.delete())
        guard case .delete(let q) = src2.writes.last else {
            Issue.record("no delete")
            return
        }
        #expect(q.notify == true && q.mail?.subject == "Declined: Q4 planning")

        // The organiser's event: the delete question follows the preference.
        let src3 = calSamples()
        let offsite = event(src3, "ev_sample_offsite")
        let o = editor(src3, mode: .edit(offsite, .all))
        var headings: [String] = []
        o.onDeleteQuestion = { q in headings.append(q.heading); return .cancel }
        #expect(await o.delete() == false)
        #expect(headings == ["Remove “Team offsite”?"] && src3.writes.isEmpty)
        o.confirmDelete = { false }
        #expect(await o.delete())
        guard case .delete(let r) = src3.writes.last else {
            Issue.record("no delete")
            return
        }
        #expect(r.notify == nil && r.etag == offsite.etag && headings.count == 1)
    }

    @Test func closeAsksWithChanges() async {
        let src = calSamples()
        let e = editor(src)
        var closed = 0
        e.onClose = { closed += 1 }
        #expect(await e.close())
        #expect(closed == 1)
        e.setTitle("Draft")
        var answer = Cal.Answer.cancel
        var questions: [Cal.Question] = []
        e.onQuestion = { q in questions.append(q); return answer }
        #expect(await e.close() == false)
        #expect(questions.first?.heading == "Save the Changes to This Event?")
        answer = .alternative
        #expect(await e.close())
        #expect(closed == 2 && src.writes.isEmpty)
        let s = editor(src)
        s.setTitle("Kept")
        s.onQuestion = { _ in .confirm }
        #expect(await s.close())
        guard case .create = src.writes.last else {
            Issue.record("no create")
            return
        }
    }

    @Test func refusedSaveStaysOpenWithTheError() async {
        let src = calSamples()
        src.failures[API.EventCreate.name] = RPCError(code: .readOnly, message: "no")
        let e = editor(src)
        e.setTitle("X")
        #expect(await e.save() == false)
        #expect(e.error == Cal.Text.errorText(.readOnly) && e.dirty)
    }

    @Test func videoProvidersFollowTheCalendar() async {
        let src = calSamples()
        let e = editor(src)
        #expect(e.videoProviders == ["teamsForBusiness"])
        #expect(e.setVideoCall(VideoCallInput(mode: .create, provider: "teamsForBusiness")))
        #expect(e.dirty && e.eventInput.videoCall == VideoCallInput(mode: .create, provider: "teamsForBusiness"))
        #expect(!e.setVideoCall(VideoCallInput(mode: .create, provider: "zoom")))
        // Gmail's account cannot create meetings: none, and the meeting goes.
        e.setCalendar(Cal.samplePersonal)
        #expect(e.videoProviders.isEmpty && e.videoCall == nil)
    }

    @Test func videoLinkMustBeHTTPS() {
        let e = editor()
        #expect(!e.setVideoCall(VideoCallInput(mode: .link, url: "http://meet.example.org/x")))
        #expect(!e.setVideoCall(VideoCallInput(mode: .link, url: "javascript:alert(1)")))
        #expect(!e.setVideoCall(VideoCallInput(mode: .link, url: "https://")))
        #expect(e.videoCall == nil && !e.dirty)
        #expect(e.setVideoCall(VideoCallInput(mode: .link, url: " https://meet.example.org/abc ")))
        #expect(e.videoCall == VideoCallInput(mode: .link, url: "https://meet.example.org/abc"))
        #expect(e.setVideoCall(VideoCallInput(mode: .none)))
        #expect(e.videoCall == nil && e.eventInput.videoCall == nil)
    }

    @Test func editStartsWithTheEventsLink() {
        let src = calSamples()
        let e = editor(src, mode: .edit(event(src, "ev_sample_planning"), .all))
        #expect(e.videoCall == VideoCallInput(mode: .link, url: "https://teams.microsoft.com/l/meetup-join/sample"))
        #expect(e.eventInput.videoCall == e.videoCall && !e.dirty)
    }
}
