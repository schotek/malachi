// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The alerts (CalendarAlertsController): due presents, dismissed from
// elsewhere withdraws, Snooze and a dismissal write and withdraw, the
// notification preference.

private func alert(_ state: EventAlertState, video: Bool = true) -> EventAlert {
    EventAlert(
        alertId: "al_1", state: state, eventId: "ev_sample_planning", accountId: Cal.sampleWork,
        calendarId: Cal.sampleWorkCalendar, title: "Q4 planning", start: calNow.addingTimeInterval(600),
        end: calNow.addingTimeInterval(3600), allDay: false, location: "Room A", videoCall: video, minutesBefore: 10)
}

@MainActor
@Suite(.serialized) struct CalendarAlertsControllerTests {
    @Test func dueDismissAndSnooze() async throws {
        let src = calSamples()
        let a = CalendarAlertsController(source: src, env: calEnv, now: { calNow })
        var presented: [CalendarAlertsController.Request] = []
        var withdrawn: [AlertID] = []
        a.onPresent = { presented.append($0) }
        a.onWithdraw = { withdrawn.append($0) }
        a.handle(.alert(alert(.due)))
        #expect(presented.count == 1 && presented[0].join && presented[0].title == "Q4 planning")
        #expect(presented[0].body == Cal.alertNotification(alert(.due), now: calNow, env: calEnv).body)
        #expect(a.shown.keys.contains("al_1"))
        // Another client dismissed it.
        a.handle(.alert(alert(.dismissed)))
        #expect(withdrawn == ["al_1"] && a.shown.isEmpty)
        // A dismissal of an alert not shown withdraws nothing.
        a.handle(.alert(alert(.dismissed)))
        #expect(withdrawn.count == 1)
        a.handle(.alert(alert(.due)))
        a.snooze("al_1")
        #expect(withdrawn.count == 2)
        try await calWaitUntil { !src.writes.isEmpty }
        #expect(src.writes == [.snooze(EventSnoozeAlertParams(alertId: "al_1", minutes: 5))])
        a.handle(.alert(alert(.due)))
        a.dismiss("al_1")
        try await calWaitUntil { src.writes.count == 2 }
        #expect(src.writes.last == .dismiss(EventDismissAlertParams(alertId: "al_1")))
        // Other notifications are not this controller's.
        a.handle(.invitationsChanged(InvitationsChangedNotification()))
        #expect(presented.count == 3)
    }

    @Test func notificationsOffPresentNothing() {
        let a = CalendarAlertsController(source: calSamples(), env: calEnv, now: { calNow }, enabled: { false })
        var presented = 0
        a.onPresent = { _ in presented += 1 }
        a.handle(.alert(alert(.due, video: false)))
        #expect(presented == 0 && a.shown.isEmpty)
    }
}
