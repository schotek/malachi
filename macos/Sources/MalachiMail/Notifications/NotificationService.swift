// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import UserNotifications
import os

/// Desktop notifications and the new-mail sound, the counterpart of
/// ui/internal/window/notify.go `notifyNewMessage` and ui/internal/sound.
///
/// `UNUserNotificationCenter` needs a bundle: run as a bare executable
/// (`swift run`) the service logs once and skips the notification; the
/// sound still plays. A notification is skipped while the main window is
/// key (the GTK `IsActive` check) and for a message read elsewhere before it
/// arrived. Clicking one calls `onActivate` with the message it announced.
/// Which notifications may still
/// show, and when they are outdated, the mailbox controller keeps
/// (`MailboxController+Notifications.swift`); `withdraw` removes them.
@MainActor
final class NotificationService: NSObject, UNUserNotificationCenterDelegate {
    private let settings: Settings
    private let isMainWindowKey: @MainActor () -> Bool
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "notifications")
    private var warnedUnbundled = false
    private var center: UNUserNotificationCenter?

    /// The user clicked a notification: open its message, given by its
    /// account and id (nil for a notification without them: show the main
    /// window). notify.go's default action `app.open-message`.
    var onActivate: (@MainActor ((account: AccountID, message: MessageID)?) -> Void)?

    /// What the user did with an event's alert (docs/calendar.md §5.9).
    enum AlertResponse: Sendable, Equatable {
        /// A click: the Calendar with the event's detail.
        case open
        /// Join: the video call through the link confirmation.
        case join
        /// Snooze: again in 5 minutes.
        case snooze
        /// The close button.
        case dismiss
    }

    /// The event alert a notification was posted for (its userInfo).
    struct AlertTarget: Sendable, Equatable {
        var alertId: AlertID
        var eventId: EventID
        var recurrenceId: DateOrTime?
        var accountId: AccountID
        var day: Date?
    }

    /// An event alert's notification was answered.
    var onAlert: (@MainActor (AlertResponse, AlertTarget) -> Void)?
    /// Accept of a new invitation's notification.
    var onAcceptInvitation: (@MainActor ((account: AccountID, message: MessageID)) -> Void)?
    /// Remove from Calendar of a cancellation's notification.
    var onApplyCancel: (@MainActor ((account: AccountID, message: MessageID)) -> Void)?

    /// The categories of the calendar's notifications. An alert without a
    /// video call has no Join, so it has a category of its own.
    enum Category {
        static let eventAlert = "EVENT_ALERT"
        static let eventAlertJoin = "EVENT_ALERT_JOIN"
        static let invitation = "INVITATION"
        static let cancellation = "CANCELLATION"
    }

    enum ActionID {
        static let join = "JOIN"
        static let snooze = "SNOOZE"
        static let accept = "ACCEPT"
        static let show = "SHOW"
        static let removeFromCalendar = "REMOVE_FROM_CALENDAR"
    }

    /// - Parameter isMainWindowKey: whether the user is looking at the main
    ///   window right now; nothing is shown then.
    init(settings: Settings, isMainWindowKey: @escaping @MainActor () -> Bool) {
        self.settings = settings
        self.isMainWindowKey = isMainWindowKey
        super.init()
        if NotificationService.isBundled {
            let c = UNUserNotificationCenter.current()
            c.delegate = self
            center = c
            c.setNotificationCategories(Self.categories())
        }
    }

    /// EVENT_ALERT (Snooze), EVENT_ALERT_JOIN (Join, Snooze), INVITATION
    /// (Accept, Show), CANCELLATION (Remove from Calendar); an alert's
    /// close button is reported (`customDismissAction`), so that the alert
    /// is dismissed for every client.
    private static func categories() -> Set<UNNotificationCategory> {
        let join = UNNotificationAction(identifier: ActionID.join, title: Cal.join, options: [.foreground])
        let snooze = UNNotificationAction(identifier: ActionID.snooze, title: Cal.snooze, options: [])
        let accept = UNNotificationAction(identifier: ActionID.accept, title: Cal.responseAction(.accepted), options: [])
        let show = UNNotificationAction(identifier: ActionID.show, title: Cal.show, options: [.foreground])
        let remove = UNNotificationAction(
            identifier: ActionID.removeFromCalendar, title: Cal.removeFromCalendar, options: [.destructive])
        return [
            UNNotificationCategory(
                identifier: Category.eventAlert, actions: [snooze], intentIdentifiers: [], options: [.customDismissAction]),
            UNNotificationCategory(
                identifier: Category.eventAlertJoin, actions: [join, snooze], intentIdentifiers: [],
                options: [.customDismissAction]),
            UNNotificationCategory(identifier: Category.invitation, actions: [accept, show], intentIdentifiers: []),
            UNNotificationCategory(identifier: Category.cancellation, actions: [remove], intentIdentifiers: []),
        ]
    }

    /// The identifier of an event alert's notification.
    static func alertNotificationID(_ id: AlertID) -> String {
        "alert-" + id.rawValue
    }

    /// Presents (or replaces) an event's alert (`CalendarAlertsController
    /// .onPresent`, which already read Settings → Notifications), with the
    /// new-mail sound under its own setting. Shown even while the main
    /// window is key: the alert is about the time, not about a folder.
    func presentAlert(_ r: CalendarAlertsController.Request) {
        if settings.notificationSound {
            playSound()
        }
        let content = UNMutableNotificationContent()
        content.title = r.title
        content.body = r.body
        content.categoryIdentifier = r.join ? Category.eventAlertJoin : Category.eventAlert
        content.threadIdentifier = "calendar"
        var info: [String: Any] = [
            "kind": "alert", "alertId": r.alertId.rawValue, "eventId": r.eventId.rawValue, "accountId": r.accountId.rawValue,
        ]
        if let rec = r.recurrenceId {
            info["recurrenceId"] = rec.wire
        }
        if let day = r.day {
            info["day"] = day.timeIntervalSince1970
        }
        content.userInfo = info
        add(UNNotificationRequest(identifier: Self.alertNotificationID(r.alertId), content: content, trigger: nil))
    }

    /// Withdraws an event alert's notification (dismissed by any client,
    /// snoozed).
    func withdrawAlert(_ id: AlertID) {
        guard let center else { return }
        let ids = [Self.alertNotificationID(id)]
        center.removeDeliveredNotifications(withIdentifiers: ids)
        center.removePendingNotificationRequests(withIdentifiers: ids)
    }

    /// `UNUserNotificationCenter.current()` traps without a bundle
    /// identifier and an .app wrapper.
    static var isBundled: Bool {
        Bundle.main.bundleIdentifier != nil && Bundle.main.bundleURL.pathExtension == "app"
    }

    /// Shows a desktop notification (and plays the sound) for a new
    /// message unless the user is looking at the main window right now, or
    /// has read the message elsewhere before it arrived here. True when a
    /// notification was requested, for the caller to remember
    /// (`MailboxController.recordNotification`).
    @discardableResult
    func deliver(_ n: NewMessageNotification) -> Bool {
        if isMainWindowKey() || hasFlag(n.message.flags, .seen) {
            return false
        }
        let posted = settings.desktopNotifications
        if posted {
            post(n)
        }
        if settings.notificationSound {
            playSound()
        }
        return posted
    }

    /// Removes the delivered notifications with the given identifiers
    /// (`notificationID`; the GTK `WithdrawNotification`). An identifier
    /// with nothing delivered under it is ignored.
    func withdraw(_ identifiers: [String]) {
        guard let center, !identifiers.isEmpty else { return }
        center.removeDeliveredNotifications(withIdentifiers: identifiers)
    }

    private func post(_ n: NewMessageNotification) {
        var (title, body) = notificationText(n)
        let content = UNMutableNotificationContent()
        // A message with an invitation or a cancellation says so, with its
        // actions (docs/calendar.md §5.9); the click opens the message as
        // for any mail.
        if let inv = n.message.invitation {
            let env = Cal.Env(zone: .current, locale: .current)
            switch inv.kind {
            case .invitation, .update:
                let text = Cal.invitationNotification(inv, env: env)
                (title, body) = (text.title, text.body)
                if inv.state == .unanswered {
                    content.categoryIdentifier = Category.invitation
                }
            case .cancelled:
                let text = Cal.cancellationNotification(inv, env: env)
                (title, body) = (text.title, text.body)
                if inv.state != .processed {
                    content.categoryIdentifier = Category.cancellation
                }
            default:
                break
            }
        }
        content.title = title
        content.body = body
        content.threadIdentifier = n.accountId.rawValue
        content.userInfo = ["accountId": n.accountId.rawValue, "messageId": n.message.id.rawValue]
        // The sound is the app's own (`playSound`), gated by its own setting.
        add(UNNotificationRequest(identifier: notificationID(n.message.id), content: content, trigger: nil))
    }

    /// Asks for the permission the first time, then adds `request`.
    private func add(_ request: UNNotificationRequest) {
        guard let center else {
            if !warnedUnbundled {
                warnedUnbundled = true
                log.notice("desktop notifications need the .app bundle; running unbundled")
            }
            return
        }
        let log = log
        Task {
            var status = await center.notificationSettings().authorizationStatus
            if status == .notDetermined {
                let granted = (try? await center.requestAuthorization(options: [.alert, .sound])) ?? false
                status = granted ? .authorized : .denied
            }
            guard status == .authorized || status == .provisional else {
                log.debug("notification not shown: authorization \(status.rawValue)")
                return
            }
            do {
                try await center.add(request)
            } catch {
                log.error("notification: \(String(describing: error), privacy: .public)")
            }
        }
    }

    /// The system "Glass" sound stands in for the sound theme's
    /// message-new-email event, which macOS has no equivalent of.
    private func playSound() {
        guard let sound = NSSound(named: "Glass") else {
            log.debug("new-mail sound: Glass not found")
            return
        }
        if !sound.play() {
            log.debug("new-mail sound: playback refused")
        }
    }

    // MARK: UNUserNotificationCenterDelegate (called off the main actor)

    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter, willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        // `deliver` already skipped the case of the main window being key,
        // so whatever reaches here is meant to be seen.
        [.banner, .list]
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        let action = response.actionIdentifier
        let info = response.notification.request.content.userInfo
        if let alert = NotificationService.alertTarget(info) {
            let r: AlertResponse
            switch action {
            case UNNotificationDefaultActionIdentifier: r = .open
            case ActionID.join: r = .join
            case ActionID.snooze: r = .snooze
            case UNNotificationDismissActionIdentifier: r = .dismiss
            default: return
            }
            await MainActor.run {
                self.onAlert?(r, alert)
            }
            return
        }
        let target = NotificationService.target(info)
        switch action {
        case UNNotificationDefaultActionIdentifier, ActionID.show:
            await MainActor.run {
                self.onActivate?(target)
            }
        case ActionID.accept:
            guard let target else { return }
            await MainActor.run {
                self.onAcceptInvitation?(target)
            }
        case ActionID.removeFromCalendar:
            guard let target else { return }
            await MainActor.run {
                self.onApplyCancel?(target)
            }
        default:
            break
        }
    }

    /// The event alert `presentAlert` put into a notification's userInfo;
    /// nil for another notification.
    nonisolated static func alertTarget(_ userInfo: [AnyHashable: Any]) -> AlertTarget? {
        guard userInfo["kind"] as? String == "alert",
              let alert = userInfo["alertId"] as? String, !alert.isEmpty,
              let event = userInfo["eventId"] as? String, !event.isEmpty,
              let account = userInfo["accountId"] as? String
        else { return nil }
        var recurrence: DateOrTime?
        if let r = userInfo["recurrenceId"] as? String {
            if let t = RFC3339.parse(r) {
                recurrence = .time(t)
            } else if CalendarDay(r).parts != nil {
                recurrence = .date(CalendarDay(r))
            }
        }
        let day = (userInfo["day"] as? Double).map { Date(timeIntervalSince1970: $0) }
        return AlertTarget(
            alertId: AlertID(rawValue: alert), eventId: EventID(rawValue: event), recurrenceId: recurrence,
            accountId: AccountID(rawValue: account), day: day)
    }

    /// The account and message `post` put into a notification's userInfo;
    /// nil unless both are non-empty strings (window/notify_open.go
    /// `ParseNotificationTarget`).
    nonisolated static func target(_ userInfo: [AnyHashable: Any]) -> (account: AccountID, message: MessageID)? {
        guard let account = userInfo["accountId"] as? String, !account.isEmpty,
              let message = userInfo["messageId"] as? String, !message.isEmpty
        else { return nil }
        return (AccountID(rawValue: account), MessageID(rawValue: message))
    }
}
