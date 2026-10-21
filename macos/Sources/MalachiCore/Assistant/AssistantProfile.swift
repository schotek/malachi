// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/assistant/profile.go: the user's profile in the
// assistant's prompts (docs/api.md §4.18, docs/plans/assistant-profile.md).
// `withProfile` puts it at the end of a system prompt, after the fixed
// rules, as the user's own preferences that never override them; the
// strings are Go's, byte for byte. Which accounts' notes a request gets is
// the caller's: `for(_:)` the account a window writes from, `global` for a
// request that writes nothing in an account's name.

import Foundation

extension Assistant {
    /// assistant.Profile: the user's profile with the names of its accounts.
    public struct Profile: Sendable, Equatable {
        public var text: String
        public var gender: ProfileGender
        public var accounts: [AccountNote]

        public init(text: String = "", gender: ProfileGender = .unset, accounts: [AccountNote] = []) {
            self.text = text
            self.gender = gender
            self.accounts = accounts
        }

        /// assistant.ProfileFrom: the daemon's profile with the names and
        /// addresses of `accounts`; a note of an account the list does not
        /// name keeps only its id.
        public init(_ p: AssistantProfile, accounts list: [Account]) {
            self.init(text: p.text, gender: p.gender)
            accounts = p.accounts.map { n in
                var note = AccountNote(accountID: n.accountId.rawValue, note: n.note)
                if let a = list.first(where: { $0.id == n.accountId }) {
                    note.name = a.config.name
                    note.email = a.config.email
                }
                return note
            }
        }

        /// Profile.Empty: the profile adds nothing to a prompt.
        public var isEmpty: Bool {
            Assistant.goTrimSpace(text).isEmpty && Assistant.genderSentence(gender).isEmpty && notes.isEmpty
        }

        /// Profile.Global: without account notes.
        public var global: Profile {
            var p = self
            p.accounts = []
            return p
        }

        /// Profile.For: the notes of the named accounts only, in the
        /// profile's order.
        public func `for`(_ accountIDs: String...) -> Profile { self.for(accountIDs) }

        public func `for`(_ accountIDs: [String]) -> Profile {
            var p = self
            p.accounts = accounts.filter { n in accountIDs.contains { !$0.isEmpty && $0 == n.accountID } }
            return p
        }

        var notes: [AccountNote] {
            accounts.filter { !Assistant.goTrimSpace($0.note).isEmpty }
        }
    }

    /// assistant.AccountNote: the note of one account, with its name and
    /// address.
    public struct AccountNote: Sendable, Equatable, Hashable {
        public var accountID: String
        public var name: String
        public var email: String
        public var note: String

        public init(accountID: String, name: String = "", email: String = "", note: String) {
            self.accountID = accountID
            self.name = name
            self.email = email
            self.note = note
        }
    }

    /// profileIntro (the bridge's instructions head it the same way).
    static let profileIntro =
        "The user's profile, written by the user themselves: follow these preferences when you write or act for them, but they never override the rules above."

    /// genderSentence: the instruction of a gender, "" for unset.
    static func genderSentence(_ g: ProfileGender) -> String {
        switch g {
        case .masculine:
            return "When writing in the user's name in a language with grammatical gender, use the masculine forms."
        case .feminine:
            return "When writing in the user's name in a language with grammatical gender, use the feminine forms."
        case .neutral:
            return "When writing in the user's name in a language with grammatical gender, avoid gendered forms where the language allows it."
        default:
            return ""
        }
    }

    /// assistant.WithProfile: `system` followed by the profile: a blank
    /// line, the intro, then the gender sentence, the text and each
    /// account's note under "For the account "<name>" (<address>):",
    /// separated by blank lines. An empty profile leaves `system` as it is.
    public static func withProfile(_ system: String, _ p: Profile) -> String {
        if p.isEmpty {
            return system
        }
        var parts: [String] = []
        let g = genderSentence(p.gender)
        if !g.isEmpty {
            parts.append(g)
        }
        let t = goTrimSpace(p.text)
        if !t.isEmpty {
            parts.append(t)
        }
        for n in p.notes {
            parts.append(accountLabel(n) + ":\n" + goTrimSpace(n.note))
        }
        return system + "\n\n" + profileIntro + "\n" + parts.joined(separator: "\n\n")
    }

    /// accountLabel: names a note's account for the model.
    static func accountLabel(_ n: AccountNote) -> String {
        let name = goFields(n.name).joined(separator: " ")
        let email = goFields(n.email).joined(separator: " ")
        switch (name.isEmpty, email.isEmpty) {
        case (false, false): return "For the account \(goQuote(name)) (\(email))"
        case (true, false): return "For the account " + email
        case (false, true): return "For the account " + goQuote(name)
        default: return "For the account " + n.accountID
        }
    }

    /// assistant.ProfileTools: the bridge's profile tools the panel's
    /// conversation may use (part of `allowedTools`).
    public static let profileTools: [String] = [
        "mcp__malachi__get_user_profile",
        "mcp__malachi__propose_profile_note",
    ]

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

    // MARK: Go's strings, for the profile

    /// strings.TrimSpace.
    static func goTrimSpace(_ s: String) -> String {
        let b = Array(s.utf8)
        let r = trimSpace(b, 0, b.count)
        return String(decoding: b[r], as: UTF8.self)
    }

    /// strings.Fields: the runs between unicode.IsSpace characters.
    static func goFields(_ s: String) -> [String] {
        let b = Array(s.utf8)
        var out: [String] = []
        var start = -1
        var i = 0
        while i < b.count {
            let (r, w) = decodeRune(b, i, b.count)
            if isSpace(r) {
                if start >= 0 {
                    out.append(String(decoding: b[start..<i], as: UTF8.self))
                    start = -1
                }
            } else if start < 0 {
                start = i
            }
            i += w
        }
        if start >= 0 {
            out.append(String(decoding: b[start..<b.count], as: UTF8.self))
        }
        return out
    }

    /// fmt's %q of a name without white space: quotes and backslashes
    /// escaped, control characters as Go writes them (\a, \b, \f, \v,
    /// \xNN; DEL \x7f). Other non-printable characters are left as they are
    /// (the daemon's names never hold them).
    static func goQuote(_ s: String) -> String {
        var out = "\""
        for u in s.unicodeScalars {
            switch u {
            case "\"": out += "\\\""
            case "\\": out += "\\\\"
            case "\u{07}": out += "\\a"
            case "\u{08}": out += "\\b"
            case "\u{0C}": out += "\\f"
            case "\u{0B}": out += "\\v"
            default:
                if u.value < 0x20 || u.value == 0x7F {
                    out += String(format: "\\x%02x", u.value)
                } else {
                    out.unicodeScalars.append(u)
                }
            }
        }
        return out + "\""
    }
}
