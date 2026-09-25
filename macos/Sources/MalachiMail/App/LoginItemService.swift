// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import ServiceManagement

/// The "Launch at Login" switch's backend, the counterpart of
/// ui/internal/background (the Background portal): the login item of the
/// main application through `SMAppService`. The service, not the
/// `launch-at-login` key, is authoritative; the key mirrors `isEnabled`.
///
/// An ad-hoc signed or unbundled build may be refused (`register` throws,
/// or the status stays `.notFound`); the caller shows the error and leaves
/// the switch where the service says it is. Nothing here crashes.
@MainActor
final class LoginItemService {
    /// The result of `set`: what the service says afterwards.
    enum Outcome: Equatable {
        /// The item is now in the wanted state.
        case granted
        /// macOS wants the user to allow the item in System Settings →
        /// General → Login Items (`SMAppService.Status.requiresApproval`).
        case requiresApproval
        /// The call went through but the status is not the wanted one.
        case notGranted
    }

    private let service = SMAppService.mainApp

    init() {}

    var status: SMAppService.Status {
        service.status
    }

    /// Whether the application is registered and enabled as a login item.
    var isEnabled: Bool {
        status == .enabled
    }

    /// Registers or unregisters the login item. Throws what the service
    /// throws (the caller shows `localizedDescription`); otherwise reports
    /// whether the wanted state is in effect.
    func set(_ on: Bool) throws -> Outcome {
        if on {
            try service.register()
        } else {
            try service.unregister()
        }
        let now = status
        if now == .requiresApproval {
            return .requiresApproval
        }
        return (now == .enabled) == on ? .granted : .notGranted
    }

    /// Opens System Settings → General → Login Items.
    func openSystemSettings() {
        SMAppService.openSystemSettingsLoginItems()
    }

    // MARK: Start hidden

    /// Whether this process was started by the login item mechanism: the
    /// open-application Apple event that launched it carries
    /// `keyAELaunchedAsLogInItem`. Read it early, before another Apple
    /// event becomes the current one (`applicationDidFinishLaunching`
    /// is in time). A process started from a terminal has no such event
    /// and answers false.
    static var launchedAsLoginItem: Bool {
        guard let event = NSAppleEventManager.shared().currentAppleEvent else { return false }
        guard event.eventClass == AEEventClass(kCoreEventClass), event.eventID == AEEventID(kAEOpenApplication) else {
            return false
        }
        guard let reason = event.paramDescriptor(forKeyword: AEKeyword(keyAEPropData)) else { return false }
        return reason.enumCodeValue == OSType(keyAELaunchedAsLogInItem)
    }
}
