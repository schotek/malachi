// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// How this copy of the app was distributed, baked into Info.plist by
/// macos/Makefile (`MalachiDistribution`, `DISTRIBUTION=`). The website's
/// DMG (Developer ID) and the Mac App Store build come from the same code;
/// what the App Sandbox of the second does not allow is decided here at
/// run time, never by a build flag (docs/plans/mac-app-store.md).
public enum Distribution: String, Sendable {
    /// The DMG from the website, and every development build.
    case direct
    /// The Mac App Store build: App Sandbox, the socket in the app group.
    case appStore = "appstore"

    /// The app group the App Store build shares with malachi-mcp: its
    /// container holds the daemon's socket and key, which the bridge,
    /// started by Claude in a sandbox of its own, can reach only there.
    /// Team-prefixed, so it needs no registration in the developer portal.
    public static let appGroup = "PG9F7XU6X8.io.github.schotek.Malachi"

    /// Info.plist `MalachiDistribution`; anything else, or no bundle, is
    /// `direct`.
    public static let current: Distribution = from(
        Bundle.main.object(forInfoDictionaryKey: "MalachiDistribution") as? String)

    static func from(_ value: String?) -> Distribution {
        value.flatMap(Distribution.init(rawValue:)) ?? .direct
    }

    /// Whether the app may run the user's own Claude Code or Codex: the In
    /// App assistant and its panel, the compose window's rewrite and
    /// writing bar, the search in the user's own words, the board's triage
    /// and Suggest Reply, the ChatGPT provider and the Claude Code sign-in.
    /// Not in the App Store build: a process it starts inherits the App
    /// Sandbox and reaches neither `~/.claude` nor its keychain item, and
    /// running a program the user picked does not pass App Review (2.5.2).
    /// The hand-off to Claude Desktop and to Claude Code in the terminal by
    /// link stays.
    public var allowsLocalAgents: Bool { self == .direct }

    /// Whether an attachment can go to Claude as a file (the attachment's
    /// "Ask the Assistant…": a Cowork session's file, Claude Code's working
    /// directory). Not in the App Store build: the file would lie in the
    /// app's container, which macOS keeps from other apps; there Claude
    /// reads an attachment's text through the bridge (`get_attachment`).
    /// Decided by the owner 2026-11-05 (docs/plans/mac-app-store.md Q12).
    public var handsFilesToClaude: Bool { self == .direct }

    /// The app group's container for the App Store build, nil for `direct`.
    public var groupContainer: URL? {
        guard self == .appStore else { return nil }
        return FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: Self.appGroup)
    }
}
