// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The event detail popover's controller (EventDetailController) over the
// in-memory samples: its rows, the answer with and without the series
// question and Decline Without Reply, the remove questions of an attendee
// and an organiser and the confirm-delete preference, Edit's scope, the
// conflict reload and the hand-offs.

@MainActor
private func detail(
    _ src: InMemoryCalendarSource, _ id: String, recurrenceId: DateOrTime? = nil, confirmDelete: Bool = true
) async throws -> EventDetailController {
    let d = EventDetailController(
        source: src, eventId: EventID(id), recurrenceId: recurrenceId, env: calEnv, confirmDelete: { confirmDelete })
    d.start()
    try await calWaitUntil { d.event != nil || !d.error.isEmpty }
    return d
}

@MainActor
private func waitIdle(_ d: EventDetailController) async throws {
    try await Task.sleep(for: .milliseconds(5))
    try await calWaitUntil { !d.busy && !d.loading }
}

/// The samples with the design review repeating weekly.
@MainActor
private func withRecurringReview() -> InMemoryCalendarSource {
    var data = Cal.sampleData(now: calNow, zone: calZone)
    if let i = data.events.firstIndex(where: { $0.eventId == "ev_sample_review" }) {
        data.events[i].recurring = true
        data.events[i].recurrence = Recurrence(frequency: .weekly, interval: 1, byDay: ["TU"])
        data.events[i].recurrenceId = .time(data.events[i].start!)
    }
    let s = InMemoryCalendarSource(data)
    s.emulatesWrites = true
    return s
}

@MainActor
@Suite(.serialized) struct CalendarEventDetailControllerTests {
    @Test func rowsOfAnInvitedEvent() async throws {
        let d = try await detail(calSamples(), "ev_sample_planning")
        let r = try #require(d.detail)
        #expect(r.calendarLine == "Work · Calendar")
        #expect(r.title == "Q4 planning")
        #expect(r.canRespond && !r.canEdit && r.showInvitation)
        #expect(r.removeTitle == Cal.removeFromCalendar)
        #expect(r.videoHost == "teams.microsoft.com")
        #expect(d.shownAttendees.count == 4 && d.showAllAttendees.isEmpty)
        var links: [String] = []
        var shown: [MessageRef] = []
        d.onOpenLink = { links.append($0) }
        d.onShowInvitation = { shown.append($0) }
        d.joinVideoCall()
        d.openLink("https://example.org/q4-slides")
        d.openLink("https://evil.example/")
        d.showInvitation()
        #expect(links == ["https://teams.microsoft.com/l/meetup-join/sample", "https://example.org/q4-slides"])
        #expect(shown == [MessageRef(accountId: Cal.sampleWork, messageId: "msg_sample_planning")])
    }

    @Test func answerOfASingleEvent() async throws {
        let src = calSamples()
        let d = try await detail(src, "ev_sample_planning")
        var asked = 0
        d.onScope = { _ in asked += 1; return .this }
        d.respond(.tentative)
        try await waitIdle(d)
        #expect(asked == 0)
        guard case .respond(let p) = src.writes.last else {
            Issue.record("no respond")
            return
        }
        #expect(p.scope == .all && p.response == .tentative && p.etag == "\"1\"" && p.sendResponse == nil)
        #expect(p.mail?.subject == "Tentative: Q4 planning")
        #expect(d.event?.myResponse == .tentative)
        // Decline Without Reply.
        d.respond(.declined, withoutReply: true)
        try await waitIdle(d)
        guard case .respond(let q) = src.writes.last else {
            Issue.record("no respond")
            return
        }
        #expect(q.sendResponse == false && q.mail == nil && q.response == .declined)
    }

    @Test func answerOfASeriesAsksTheScope() async throws {
        let src = withRecurringReview()
        let d = try await detail(src, "ev_sample_review")
        var questions: [Cal.ScopeQuestion] = []
        d.onScope = { q in questions.append(q); return nil }
        d.respond(.accepted)
        try await waitIdle(d)
        #expect(questions.first?.choices.map(\.scope) == [.this, .all])
        #expect(src.writes.isEmpty)
        d.onScope = { _ in .all }
        d.respond(.accepted)
        try await waitIdle(d)
        guard case .respond(let p) = src.writes.last else {
            Issue.record("no respond")
            return
        }
        #expect(p.scope == .all)
    }

    @Test func attendeeRemoveAsksAboutDeclining() async throws {
        let src = calSamples()
        let d = try await detail(src, "ev_sample_planning")
        var questions: [Cal.Question] = []
        var answer = Cal.Answer.cancel
        d.onQuestion = { q in questions.append(q); return answer }
        var closed = 0
        d.onClose = { closed += 1 }
        d.remove()
        try await waitIdle(d)
        #expect(questions.first?.heading == "Also tell the organiser you won’t attend?")
        #expect(src.writes.isEmpty && closed == 0)
        answer = .alternative
        d.remove()
        try await waitIdle(d)
        guard case .delete(let p) = src.writes.last else {
            Issue.record("no delete")
            return
        }
        #expect(p.notify == false && p.mail == nil && p.scope == .all)
        #expect(closed == 1)
    }

    @Test func attendeeRemoveAndDecline() async throws {
        let src = calSamples()
        let d = try await detail(src, "ev_sample_planning")
        d.onQuestion = { _ in .confirm }
        d.remove()
        try await waitIdle(d)
        guard case .delete(let p) = src.writes.last else {
            Issue.record("no delete")
            return
        }
        #expect(p.notify == true && p.mail?.subject == "Declined: Q4 planning")
    }

    @Test func organiserDeleteFollowsConfirmDelete() async throws {
        let src = calSamples()
        var questions: [Cal.Question] = []
        let d = try await detail(src, "ev_sample_offsite")
        d.onQuestion = { q in questions.append(q); return .confirm }
        d.remove()
        try await waitIdle(d)
        #expect(questions.map(\.heading) == ["Remove “Team offsite”?"])
        guard case .delete(let p) = src.writes.last else {
            Issue.record("no delete")
            return
        }
        #expect(p.notify == nil)
        // Without the preference nothing is asked.
        let src2 = calSamples()
        let quiet = try await detail(src2, "ev_sample_offsite", confirmDelete: false)
        var asked = 0
        quiet.onQuestion = { _ in asked += 1; return .cancel }
        quiet.remove()
        try await waitIdle(quiet)
        #expect(asked == 0 && src2.writes.count == 1)
    }

    @Test func seriesDeleteAsksTheScope() async throws {
        let src = calSamples()
        let rid = src.data.events.first { $0.eventId == "ev_sample_standup" }!.recurrenceId
        let d = try await detail(src, "ev_sample_standup", recurrenceId: rid, confirmDelete: false)
        var scopes: [Cal.ScopeQuestion] = []
        d.onScope = { q in scopes.append(q); return .this }
        d.remove()
        try await waitIdle(d)
        #expect(scopes.first?.choices.map(\.scope) == [.this, .thisAndFuture, .all])
        guard case .delete(let p) = src.writes.last else {
            Issue.record("no delete")
            return
        }
        #expect(p.scope == .this && p.recurrenceId == rid)
    }

    @Test func editHandsTheScopeOver() async throws {
        let src = calSamples()
        let rid = src.data.events.first { $0.eventId == "ev_sample_standup" }!.recurrenceId
        let d = try await detail(src, "ev_sample_standup", recurrenceId: rid)
        var edits: [EditScope] = []
        d.onScope = { _ in .thisAndFuture }
        d.onEdit = { _, s in edits.append(s) }
        d.edit()
        try await calWaitUntil { !edits.isEmpty }
        #expect(edits == [.thisAndFuture])
        // A single event goes to the editor without a question.
        let single = try await detail(src, "ev_sample_offsite")
        var asked = 0
        single.onScope = { _ in asked += 1; return nil }
        single.onEdit = { _, s in edits.append(s) }
        single.edit()
        try await calWaitUntil { edits.count == 2 }
        #expect(edits.last == .all && asked == 0)
    }

    @Test func conflictReloadsTheEvent() async throws {
        let src = calSamples()
        var newer = src.data.events.first { $0.eventId == "ev_sample_planning" }!
        newer.etag = "\"9\""
        newer.title = "Q4 planning (moved)"
        let data = try JSONCoding.encoder().encode(ConflictEventData(event: newer))
        let value = try JSONCoding.decoder().decode(JSONValue.self, from: data)
        src.failures[API.EventRespond.name] = RPCError(code: .conflict, message: "etag", data: value)
        let d = try await detail(src, "ev_sample_planning")
        d.respond(.accepted)
        try await waitIdle(d)
        #expect(d.error == Cal.Text.errorText(.conflict))
        #expect(d.event?.etag == "\"9\"" && d.detail?.title == "Q4 planning (moved)")
    }

    @Test func unknownEventSaysSo() async throws {
        let d = try await detail(calSamples(), "ev_nothing")
        #expect(d.event == nil && d.error == Cal.Text.errorText(.eventNotFound))
        #expect(EventDetailController(source: calSamples(), occurrence: Cal.sampleData(now: calNow, zone: calZone).occurrences.last!) == nil)
    }
}
