// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// A test build of the macOS app (`make macos` and `make run-macos`:
/// macos/Makefile `VARIANT=dev`) beside the stable Malachi Mail installed
/// on the same Mac. It has a bundle identifier of
/// its own (`io.github.schotek.Malachi.Dev`), so its preferences domain,
/// notifications, login item and `mailto:` are its own, and it shares by
/// name what the stable app has: the data directory with the store, the
/// daemon's socket and the keychain items. Info.plist
/// `MalachiStableBundleIdentifier` names the stable app; it is empty in
/// every other build (the DMG, the App Store, CI), which then behave as
/// before.
///
/// What a test build does differently:
/// - its preferences are an overlay over the stable app's
///   (`overlayPreferences`): it reads its own, then the stable app's,
///   and writes only its own;
/// - it never changes the bridge's registration in Claude Desktop and
///   Claude Code; a registration of another copy (the stable app) counts
///   as its own (`MCPRegistrationController.readOnly`);
/// - it uses only a daemon of its own version: one of another version on
///   the socket (the stable app's) is not used, and the status line says
///   to quit the other Malachi Mail first
///   (`ConnectionController.requiredDaemonVersion`).
public enum TestBuild {
    /// The stable app's bundle identifier in a test build, nil otherwise.
    /// Settable for tests.
    public nonisolated(unsafe) static var stableIdentifier: String? = from(
        Bundle.main.object(forInfoDictionaryKey: "MalachiStableBundleIdentifier") as? String,
        own: Bundle.main.bundleIdentifier)

    /// The stable identifier `value` names, unless it is empty or this
    /// bundle's own.
    static func from(_ value: String?, own: String?) -> String? {
        guard let v = value?.trimmingCharacters(in: .whitespacesAndNewlines), !v.isEmpty, v != own else { return nil }
        return v
    }

    /// This is a test build.
    public static var isActive: Bool { stableIdentifier != nil }

    /// Makes `defaults` read the stable app's preferences after its own
    /// (`UserDefaults.addSuite(named:)`: searched after the application's
    /// domain and before the registered defaults), so a test build starts
    /// with the stable app's settings and a change in it shows here
    /// until this build sets that key itself. Writes stay in this build's
    /// domain. Nothing outside a test build. Called once at launch, before
    /// anything reads a preference.
    public static func overlayPreferences(_ defaults: UserDefaults = .standard, stable: String? = stableIdentifier) {
        guard let id = stable else { return }
        defaults.addSuite(named: id)
    }

    /// macOS-only string: the status line while the stable app's daemon
    /// (of another version) answers on the socket.
    public static func otherDaemonText(_ version: String) -> String {
        // macOS-only string
        "Another Malachi Mail is running (malachid \(version)); quit it to use this test build"
    }

    /// macOS-only string: why the test build does not register the bridge.
    public static let registrationNote =
        // macOS-only string
        "This test build uses the registration of the stable Malachi Mail; change it there"
}
