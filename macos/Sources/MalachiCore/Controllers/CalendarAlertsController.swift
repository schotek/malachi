// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The events' alerts for the whole application (docs/calendar.md §5.9),
// owned by the app state: `notify.eventAlert` `due` becomes a notification
// request the app presents (category EVENT_ALERT: Join only with a video
// call, Snooze), `dismissed` (from any client) withdraws it; Snooze is
// event.snoozeAlert for 5 minutes, a click or the close button dismisses
// (event.dismissAlert). The app routes the decoded `CalendarNotification`s
// here itself (`handle(_:)`), not through a window's source, so that an
// alert is shown once whatever the number of windows.

import Foundation

@MainActor
public final class CalendarAlertsController {
    /// What the app presents for one alert.
    public struct Request: Sendable, Equatable {
        public var alertId: AlertID
        public var title: String
        public var body: String
        /// Offer Join (the event has a video call; its link comes from
        /// event.get when the user clicks it).
        public var join: Bool
        public var eventId: EventID
        public var recurrenceId: DateOrTime?
        public var accountId: AccountID
        /// The day the click opens in the Calendar.
        public var day: Date?
    }

    public let source: any CalendarDataSource
    public let env: Cal.Env
    private let now: @MainActor () -> Date
    private let enabled: @MainActor () -> Bool

    /// Present (or replace) a notification.
    public var onPresent: (@MainActor (Request) -> Void)?
    /// Withdraw the notification of an alert.
    public var onWithdraw: (@MainActor (AlertID) -> Void)?
    /// A snooze or dismiss failed (a log line, not a toast).
    public var onError: (@MainActor (String) -> Void)?

    /// The alerts presented and not withdrawn.
    public private(set) var shown: [AlertID: Request] = [:]

    /// `enabled` reads Settings → Notifications (`desktopNotifications`):
    /// off presents nothing (a dismissal still withdraws).
    public init(
        source: any CalendarDataSource, env: Cal.Env = Cal.Env(zone: .current, locale: .current),
        now: @escaping @MainActor () -> Date = { Date() }, enabled: @escaping @MainActor () -> Bool = { true }
    ) {
        self.source = source
        self.env = env
        self.now = now
        self.enabled = enabled
    }

    /// A calendar notification; only alerts concern this controller.
    public func handle(_ n: CalendarNotification) {
        guard case .alert(let a) = n else { return }
        handle(a)
    }

    public func handle(_ a: EventAlert) {
        switch a.state {
        case .due:
            guard enabled() else { return }
            let text = Cal.alertNotification(a, now: now(), env: env)
            let r = Request(
                alertId: a.alertId, title: text.title, body: text.body, join: a.videoCall, eventId: a.eventId,
                recurrenceId: a.recurrenceId, accountId: a.accountId, day: a.start ?? Cal.parseDate(a.startDate, env.zone))
            shown[a.alertId] = r
            onPresent?(r)
        case .dismissed:
            withdraw(a.alertId)
        default:
            break
        }
    }

    /// Snooze: again in 5 minutes (the daemon sends `due` then).
    public func snooze(_ id: AlertID) {
        withdraw(id)
        let source = source
        Task { [weak self] in
            do {
                try await source.snoozeAlert(EventSnoozeAlertParams(alertId: id, minutes: Cal.snoozeMinutes))
            } catch {
                self?.onError?(Cal.errorMessage(error))
            }
        }
    }

    /// The notification was clicked or closed: dismissed for every client.
    public func dismiss(_ id: AlertID) {
        withdraw(id)
        let source = source
        Task { [weak self] in
            do {
                try await source.dismissAlert(EventDismissAlertParams(alertId: id))
            } catch {
                self?.onError?(Cal.errorMessage(error))
            }
        }
    }

    private func withdraw(_ id: AlertID) {
        guard shown.removeValue(forKey: id) != nil else { return }
        onWithdraw?(id)
    }
}
