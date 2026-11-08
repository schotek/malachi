// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/assistant/profile.go: the user's profile for the
// assistant (docs/api.md §4.18, docs/plans/assistant-profile.md). The
// daemon appends the profile to every system prompt it builds
// (assistant.prompt; the note of the account a one-shot request names),
// so nothing of it is composed here any more: this file keeps the
// proposal a propose_profile_note result names and the texts of Settings →
// AI → About You.

import Foundation

extension Assistant {
    /// The tool whose result names a proposal (`parseProposalResult`),
    /// without the bridge's prefix, as `Event.tool` names it.
    public static let proposeProfileTool = "propose_profile_note"

    private static let proposalHead = Array("profile note proposal ".utf8)
    private static let proposalTail = Array(" is pending;".utf8)

    /// assistant.ParseProposalResult: the proposal a propose_profile_note
    /// result names. Only the result's first line counts, and only when it
    /// starts exactly with the head the bridge writes, "profile note
    /// proposal <id> is pending;", followed by the end of the line or a
    /// space; the id holds no white space or control characters. The
    /// application reads the proposal's text from the daemon, never from
    /// the result.
    public static func parseProposalResult(_ text: String) -> String? {
        let b = Array(text.utf8)
        let lineEnd = b.firstIndex(of: 0x0A) ?? b.count
        guard lineEnd >= proposalHead.count, b[0..<proposalHead.count].elementsEqual(proposalHead) else { return nil }
        let idStart = proposalHead.count
        guard let idEnd = firstIndex(of: proposalTail, in: b, from: idStart, to: lineEnd) else { return nil }
        let rest = idEnd + proposalTail.count
        guard validID(b, idStart, idEnd), rest == lineEnd || b[rest] == 0x20 else { return nil }
        return String(decoding: b[idStart..<idEnd], as: UTF8.self)
    }

    /// assistant.ProfileStrings: the fixed texts of the profile.
    public struct ProfileStrings: Sendable, Equatable {
        public var group, groupDescription, genderRow: String
        public var genderUnset, genderMasculine, genderFeminine, genderNeutral: String
        public var textPlaceholder, notePlaceholder, noNote: String
        public var shareRow, shareSubtitle, pending: String
        public var editProfile, proposalTitle: String
        public var save, edit, dismiss: String
        public var fromPanel, fromOtherApp: String
        public var accountProposal, proposalOK: String

        /// The label of a gender in Settings.
        public func genderLabel(_ g: ProfileGender) -> String {
            switch g {
            case .masculine: return genderMasculine
            case .feminine: return genderFeminine
            case .neutral: return genderNeutral
            default: return genderUnset
            }
        }
    }

    /// assistant.ProfileTexts: the fixed texts of the profile, translated.
    public static func profileTexts() -> ProfileStrings {
        ProfileStrings(
            group: L10n.T("About You"),
            groupDescription: L10n.T(
                "The assistant gets this in the app (Claude Code, ChatGPT) and, if you allow it below, in apps connected over MCP; only with your consent."),
            genderRow: L10n.T("I Write As"),
            genderUnset: L10n.T("Not Specified"),
            genderMasculine: L10n.T("A Man"),
            genderFeminine: L10n.T("A Woman"),
            genderNeutral: L10n.T("Gender-Neutral"),
            textPlaceholder: L10n.T("For example: I write briefly · suggest meetings on Mondays and Wednesdays"),
            notePlaceholder: L10n.T("For example: sign as the studio"),
            noNote: L10n.T("No Note"),
            shareRow: L10n.T("Send the Profile to Apps over MCP"),
            shareSubtitle: L10n.T("Claude Desktop and other apps registered over MCP; a change applies after they restart."),
            pending: L10n.T("Suggested by the Assistant"),
            editProfile: L10n.T("Edit What the Assistant Knows About You…"),
            proposalTitle: L10n.T("The assistant suggests remembering:"),
            save: L10n.T("Save"),
            edit: L10n.T("Edit…"),
            dismiss: L10n.T("Dismiss"),
            fromPanel: L10n.T("From the assistant panel"),
            fromOtherApp: L10n.T("From another app over MCP"),
            accountProposal: L10n.T("For the account %s"),
            proposalOK: L10n.T("Saved to your profile")
        )
    }

    /// assistant.ProfileCounter: "12 / 2000".
    public static func profileCounter(_ n: Int, limit: Int) -> String {
        L10n.T("%d / %d", n, limit)
    }

    /// assistant.ProfileProposalCount: the panel menu's item with pending
    /// proposals; the plain item for 0.
    public static func profileProposalCount(_ n: Int) -> String {
        let item = profileTexts().editProfile
        guard n > 0 else { return item }
        return L10n.T("%s (%d)", item, n)
    }
}
