// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/assistant, Claude Code's sign-in from the application (the
// In App target): claude.go `SignInArgs` and assistant.go `SignInStrings`,
// `SignInTexts`, `SignInFailedText` and `InstallURL`. Its environment,
// `signInEnv`, is beside `childEnv` in AssistantPanel.swift, as in claude.go.
//
// Claude Code has a sign-in of its own, and the panel cannot answer without
// it. The application runs Claude Code's own `claude auth login`, which
// opens the browser at claude.ai, waits for its answer on a local port and
// stores the sign-in itself; the application only waits for the process to
// end (status 0: signed in). It waits with its stdin closed too. What it
// prints is never logged or shown: the address to open by hand names the
// sign-in's session. Measured with Claude Code 2.1.285. The run is
// `ClaudeCodeLocator.startSignIn`.

import Foundation

extension Assistant {
    /// assistant.SignInArgs: the arguments of Claude Code's own sign-in.
    public static let signInArgs: [String] = ["auth", "login"]

    /// assistant.InstallURL: where "Get Claude Code…" leads: Anthropic's
    /// page with the installers for every system. The application downloads
    /// and runs nothing itself.
    public static let installURL = "https://code.claude.com/docs/en/setup"

    /// assistant.SignInStrings: the fixed texts of Claude Code's sign-in
    /// from the application (target App): the application runs Claude
    /// Code's own `claude auth login`, which opens the browser, and waits
    /// for it. It never sees a credential. A failure is `signInFailedText`.
    public struct SignInStrings: Sendable, Equatable {
        /// The button beside the panel's "Claude Code is not signed in" and
        /// on the settings' Claude Code row; `waiting` what both show while
        /// the browser is open (the panel as an activity line).
        public var signIn: String
        public var waiting: String
        /// The error line when the browser brought no answer in time.
        public var timedOut: String
        /// The line of a request that has no button of its own (the
        /// compose window's rewrite, the search in the user's own words).
        public var hint: String
        /// The button beside "Claude Code was not found on this computer":
        /// it opens `installURL` in the browser.
        public var getClaudeCode: String

        public init(signIn: String, waiting: String, timedOut: String, hint: String, getClaudeCode: String) {
            self.signIn = signIn
            self.waiting = waiting
            self.timedOut = timedOut
            self.hint = hint
            self.getClaudeCode = getClaudeCode
        }
    }

    /// assistant.SignInTexts: the fixed texts of Claude Code's sign-in,
    /// translated.
    public static func signInTexts() -> SignInStrings {
        SignInStrings(
            signIn: L10n.T("Sign In…"),
            waiting: L10n.T("Waiting for the sign-in in your browser…"),
            timedOut: L10n.T("The sign-in took too long; try again"),
            // TRANSLATORS: AI is the page of the preferences with the Claude Code row.
            hint: L10n.T("Claude Code is not signed in. Sign in under AI in the preferences."),
            // TRANSLATORS: A button that opens the web page with Claude Code's installers.
            getClaudeCode: L10n.T("Get Claude Code…")
        )
    }

    /// assistant.SignInFailedText: the error line when Claude Code's
    /// sign-in ended badly. `reason` is technical (its stderr, or its exit
    /// status) and shown as data, as `stoppedText` shows it: its first
    /// non-empty line without control characters, at most 400 bytes;
    /// "unknown" when nothing is left.
    public static func signInFailedText(_ reason: String) -> String {
        // TRANSLATORS: %s is a technical reason.
        L10n.T("The sign-in failed: %s", firstLine(reason, limit: maxReason))
    }
}
