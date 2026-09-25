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
/// key (the GTK `IsActive` check). Clicking one calls `onActivate`.
@MainActor
final class NotificationService: NSObject, UNUserNotificationCenterDelegate {
    private let settings: Settings
    private let isMainWindowKey: @MainActor () -> Bool
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "notifications")
    private var warnedUnbundled = false
    private var center: UNUserNotificationCenter?

    /// The user clicked a notification: activate and show the main window.
    var onActivate: (@MainActor () -> Void)?

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
        }
    }

    /// `UNUserNotificationCenter.current()` traps without a bundle
    /// identifier and an .app wrapper.
    static var isBundled: Bool {
        Bundle.main.bundleIdentifier != nil && Bundle.main.bundleURL.pathExtension == "app"
    }

    /// Shows a desktop notification (and plays the sound) for a new
    /// message unless the user is looking at the main window right now.
    func deliver(_ n: NewMessageNotification) {
        if isMainWindowKey() {
            return
        }
        if settings.desktopNotifications {
            post(n)
        }
        if settings.notificationSound {
            playSound()
        }
    }

    private func post(_ n: NewMessageNotification) {
        guard let center else {
            if !warnedUnbundled {
                warnedUnbundled = true
                log.notice("desktop notifications need the .app bundle; running unbundled")
            }
            return
        }
        let (title, body) = notificationText(n)
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.threadIdentifier = n.accountId.rawValue
        content.userInfo = ["accountId": n.accountId.rawValue, "messageId": n.message.id.rawValue]
        // The sound is the app's own (`playSound`), gated by its own setting.
        let request = UNNotificationRequest(identifier: "message-" + n.message.id.rawValue, content: content, trigger: nil)
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
        guard action == UNNotificationDefaultActionIdentifier else { return }
        await MainActor.run {
            self.onActivate?()
        }
    }
}
