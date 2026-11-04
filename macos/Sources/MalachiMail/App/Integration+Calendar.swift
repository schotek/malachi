// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The application's calendar (docs/calendar.md §5.2, §5.3): one calendar
/// source per main window (`source(for:)`: the daemon's over the
/// application's client, or the samples with MALACHI_CALENDAR_SAMPLES=1),
/// the application's `CalendarAlertsController`, and the notification
/// routing: the hub decodes `CalendarNotification` next to
/// `DaemonNotification` and every one reaches each window's source
/// (`handle(_:)`) and the alerts controller; notify.accountsChanged and the
/// connection reach the daemon's sources (`accountsChanged()`,
/// `connectionChanged(connected:)`), as for the board. A source not started
/// yet ignores them. The alerts are presented as desktop notifications by
/// `Integration.wireCalendarNotifications` (M10).
@MainActor
final class AppCalendar {
    /// MALACHI_CALENDAR_SAMPLES=1 (read once).
    let samples = InMemoryCalendarSource.samplesRequested
    /// The events' alerts for the whole application (presented by
    /// `Integration.wireCalendarNotifications`).
    let alerts: CalendarAlertsController
    private let client: RPCClient
    private struct Entry {
        weak var window: NSWindowController?
        let source: any CalendarDataSource
    }
    private var entries: [ObjectIdentifier: Entry] = [:]
    private var tokens: [NotificationHub.Token] = []

    init(client: RPCClient, settings: Settings, hub: NotificationHub) {
        self.client = client
        let source: any CalendarDataSource = samples ? InMemoryCalendarSource.samples() : DaemonCalendarSource(client: client)
        alerts = CalendarAlertsController(source: source, enabled: { settings.desktopNotifications })
        tokens = [
            hub.addCalendar { [weak self] n in
                self?.handle(n)
            },
            hub.addAccountsChanged { [weak self] in
                self?.daemonSources.forEach { $0.accountsChanged() }
            },
            hub.addConnectionState { [weak self] s in
                let connected: Bool
                switch s {
                case .connected, .infoFailed: connected = true
                case .connecting, .protocolMismatch, .unavailable, .stopping: connected = false
                }
                self?.daemonSources.forEach { $0.connectionChanged(connected: connected) }
            },
        ]
        NotificationCenter.default.addObserver(
            forName: NSApplication.willTerminateNotification, object: nil, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.daemonSources.forEach { $0.stop() }
            }
        }
    }

    /// The calendar source of `window`, made on first ask.
    func source(for window: NSWindowController) -> any CalendarDataSource {
        let key = ObjectIdentifier(window)
        if let e = entries[key], e.window != nil {
            return e.source
        }
        entries = entries.filter { $0.value.window != nil }
        let source: any CalendarDataSource = samples ? InMemoryCalendarSource.samples() : DaemonCalendarSource(client: client)
        entries[key] = Entry(window: window, source: source)
        return source
    }

    private var sources: [any CalendarDataSource] {
        entries.values.filter { $0.window != nil }.map(\.source)
    }

    private var daemonSources: [DaemonCalendarSource] {
        sources.compactMap { $0 as? DaemonCalendarSource }
    }

    /// A decoded calendar notification: every window's source, and the
    /// alerts once for the application.
    func handle(_ n: CalendarNotification) {
        for s in sources {
            s.handle(n)
        }
        alerts.handle(n)
    }
}

extension AppState {
    /// The calendar source of a main window (`MainWindowController
    /// .calendarController` is made over it).
    func calendarSource(for window: NSWindowController) -> any CalendarDataSource {
        calendar.source(for: window)
    }

    /// The application's alerts controller.
    var calendarAlerts: CalendarAlertsController {
        calendar.alerts
    }
}

/// The calendar's desktop notifications (docs/calendar.md §5.9): the
/// alerts controller presents and withdraws them through the notification
/// service; an alert's click opens the Calendar with the event's detail,
/// Join its video call through the link confirmation, Snooze puts it off 5
/// minutes, its close button dismisses it for every client. A new
/// invitation's Accept answers it with the card's default calendar and a
/// cancellation's Remove from Calendar applies it, both through an
/// `InvitationController` over the window's calendar source and the
/// message's card (message.get); Show and the click open the message, as
/// for any mail (`onActivate`).
extension Integration {
    func wireCalendarNotifications() {
        let alerts = state.calendarAlerts
        alerts.onPresent = { [weak self] r in
            self?.notifications.presentAlert(r)
        }
        alerts.onWithdraw = { [weak self] id in
            self?.notifications.withdrawAlert(id)
        }
        notifications.onAlert = { [weak self] response, target in
            self?.alertAnswered(response, target)
        }
        notifications.onAcceptInvitation = { [weak self] target in
            self?.answerNotified(target, cancel: false)
        }
        notifications.onApplyCancel = { [weak self] target in
            self?.answerNotified(target, cancel: true)
        }
    }

    private func alertAnswered(_ response: NotificationService.AlertResponse, _ t: NotificationService.AlertTarget) {
        let alerts = state.calendarAlerts
        switch response {
        case .open:
            alerts.dismiss(t.alertId)
            NSApp.activate()
            mainWindow?.showInCalendar(eventID: t.eventId, recurrenceID: t.recurrenceId, day: t.day)
        case .join:
            alerts.dismiss(t.alertId)
            joinVideoCall(t)
        case .snooze:
            alerts.snooze(t.alertId)
        case .dismiss:
            alerts.dismiss(t.alertId)
        }
    }

    /// Join: the event's video call (event.get: the link is never in the
    /// notification) through the link confirmation of the main window.
    private func joinVideoCall(_ t: NotificationService.AlertTarget) {
        guard let mainWindow else { return }
        mainWindow.startCalendar()
        let source = state.calendarSource(for: mainWindow)
        let toast = mainToast
        Task { [weak self] in
            do {
                let e = try await source.event(t.eventId, recurrenceId: t.recurrenceId)
                guard let self, let url = e.videoCallLink?.url, !url.isEmpty else { return }
                NSApp.activate()
                self.messageActions.openLink(url, links: [], from: self.mainWindow?.window)
            } catch {
                toast(Cal.errorMessage(error))
            }
        }
    }

    /// Accept (`cancel` false) or Remove from Calendar (`cancel` true) of a
    /// notification: the message's invitation card, then the card's own
    /// action; a failure or a card that cannot do it is a toast.
    private func answerNotified(_ t: (account: AccountID, message: MessageID), cancel: Bool) {
        mailbox.withdrawNotifications([t.message])
        guard let mainWindow else { return }
        mainWindow.startCalendar()
        let source = state.calendarSource(for: mainWindow)
        let client = state.client
        let toast = mainToast
        Task { [weak self] in
            let card: InvitationCard
            do {
                let res = try await client.call(
                    API.MessageGet.self, MessageGetParams(accountId: t.account, messageId: t.message),
                    timeout: RPCTimeouts.default)
                guard let c = res.message.invitation else {
                    toast(Cal.invalidLine)
                    return
                }
                card = c
            } catch {
                toast(Cal.errorMessage(error))
                return
            }
            guard let self else { return }
            let c = InvitationController(source: source, accountId: t.account, messageId: t.message, card: card)
            c.onChange = { [weak self, weak c] in
                guard let self, let c, !c.busy else { return }
                if !c.error.isEmpty {
                    toast(c.error)
                }
                self.notifiedAnswers.removeAll { $0 === c }
            }
            self.notifiedAnswers.append(c)
            if cancel {
                c.applyCancel()
            } else {
                c.respond(.accepted)
            }
            if !c.busy {
                // The card cannot do it (answered elsewhere, not a
                // cancellation any more): its message says why.
                self.notifiedAnswers.removeAll { $0 === c }
                toast(c.stateLine.isEmpty ? Cal.invalidLine : c.stateLine)
            }
        }
    }
}
