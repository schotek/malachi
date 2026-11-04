// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The view logic of saving mail to eWay-CRM (account kind `eway`,
/// capability `crm`; docs/eway.md, docs/api.md §4.17), a 1:1 port of the
/// pure Go package ui/internal/crm (crm.go here, dialog.go in
/// CRMDialog.swift, text.go in CRMText.swift), tests included: which
/// eWay-CRM account the client uses (`available`), which messages can be
/// saved (`canSave`, `offer(for:rows:)`), the state of the message's
/// button and its menu, the model of the dialog, the bar under a saved
/// message's header and the list pill, the confirmation before removal,
/// the toasts and error texts, and the texts of the account assistant
/// (the flow is `EWayWizardController`).
///
/// The daemon does every CRM call; this only turns its API values into
/// small view models and texts. Every name and detail of a CRM item is
/// untrusted display text, cleaned and capped here again (`Jira.clean`)
/// and shown as plain text only. Every URL (`CrmItem.url`,
/// `CrmRecord.url`) is built by the daemon from the account's stored
/// service URL and a GUID of fixed shape, never taken from the CRM or from
/// mail, so the client opens it in the browser without the "Open This
/// Link?" question (docs/eway.md D15); `openURL` only refuses anything
/// that is not https.
///
/// One eWay-CRM account per installation (docs/eway.md D11, §5 Q12): the
/// first enabled one in account.list order; the dialog has no account
/// popup.
public enum CRM {
    /// crm.Brand: the service's name in texts; a brand, never translated.
    public static let brand = "eWay-CRM"
    /// crm.PillLabel: the text of the list pill; a brand, never translated.
    public static let pillBrand = "eWay"

    /// crm.KindBadge: the capsule of an eWay-CRM account's kind beside its
    /// name (Settings → Integrations, `accountHeaderBadge`), as
    /// `Jira.kindBadge` is "JIRA"; a brand, never translated.
    public static let kindBadge = "EWAY"
    /// crm.MaxMessages: the most messages one save takes.
    public static let maxMessages = API.Limits.maxCrmMessages
    /// crm.Icon: the GTK symbolic icon; the AppKit side maps it to an SF
    /// Symbol (`Icon.crm`).
    public static let icon = "x-office-address-book-symbolic"

    // MARK: Accounts and rules

    /// crm.Accounts: the enabled eWay-CRM accounts, in account.list order.
    public static func accounts(_ accounts: [Account]) -> [Account] {
        accounts.filter { $0.enabled && $0.config.protocolKind == .eway }
    }

    /// crm.Available: the eWay-CRM account the client saves to, the first
    /// enabled one; nil when there is none, and then every CRM control is
    /// hidden.
    public static func available(_ accounts: [Account]) -> Account? {
        Self.accounts(accounts).first
    }

    /// crm.IsMailAccount: an account whose messages can be saved (imap,
    /// the empty kind, graph).
    public static func isMailAccount(_ a: Account) -> Bool {
        let k = a.config.protocolKind
        return k == .imap || k == .graph
    }

    /// crm.CanSave (docs/eway.md D6): a message of a mail account, not in
    /// the Outbox or a Drafts folder, not a local draft. Junk is allowed.
    public static func canSave(_ messageAccount: Account, role: FolderRole, localDraft: Bool) -> Bool {
        guard isMailAccount(messageAccount), !localDraft else { return false }
        return role != .outbox && role != .drafts
    }

    /// crm.Row: what the rules need of one selected row.
    public struct Row: Sendable, Equatable {
        public var account: Account
        public var role: FolderRole
        /// A local draft (a draft card), not a message.
        public var localDraft: Bool
        /// Saved in eWay-CRM (`isSaved`).
        public var saved: Bool
        /// A conversation's row (folded or not), which stands for its
        /// members in the folder rather than for one message: it offers
        /// nothing (owner, 2026-10-17); the user unfolds it and saves the
        /// members.
        public var conversation: Bool

        public init(account: Account, role: FolderRole, localDraft: Bool = false, saved: Bool = false, conversation: Bool = false) {
            self.account = account
            self.role = role
            self.localDraft = localDraft
            self.saved = saved
            self.conversation = conversation
        }
    }

    /// crm.Offer: what the button, the context menu and the
    /// multi-selection offer.
    public struct Offer: Sendable, Equatable {
        public var shown = false
        public var enabled = false
        public var count = 0
        /// One row, saved: the saved-state items replace Save.
        public var saved = false

        public init(shown: Bool = false, enabled: Bool = false, count: Int = 0, saved: Bool = false) {
            self.shown = shown
            self.enabled = enabled
            self.count = count
            self.saved = saved
        }
    }

    /// crm.OfferFor: nothing without an enabled eWay-CRM account or a row.
    /// One row: shown and enabled when it `canSave`. Several: shown when
    /// their account is a mail account, enabled when every row `canSave`
    /// and there are at most `maxMessages`; several rows are always Save.
    public static func offer(for accounts: [Account], rows: [Row]) -> Offer {
        guard available(accounts) != nil, let first = rows.first else { return Offer() }
        if rows.contains(where: \.conversation) {
            return Offer()
        }
        if rows.count == 1 {
            guard canSave(first.account, role: first.role, localDraft: first.localDraft) else { return Offer() }
            return Offer(shown: true, enabled: true, count: 1, saved: first.saved)
        }
        guard isMailAccount(first.account) else { return Offer() }
        var o = Offer(shown: true, enabled: rows.count <= maxMessages, count: rows.count)
        for r in rows where !canSave(r.account, role: r.role, localDraft: r.localDraft) {
            o.enabled = false
        }
        return o
    }

    /// crm.RecordOf: the message's record in eWay-CRM account `crm`
    /// (`Message.crm`); nil or "" takes the first.
    public static func recordOf(_ m: Message?, crm: AccountID?) -> CrmRecord? {
        guard let m else { return nil }
        let want = crm?.rawValue ?? ""
        return (m.crm ?? []).first { want.isEmpty || $0.accountId.rawValue == want }
    }

    /// crm.IsSaved: the loaded message's record when `m` is the message
    /// `s` (message.get is the newer word), else `s.crmSaved`.
    public static func isSaved(_ s: MessageSummary, _ m: Message?, crm: AccountID?) -> Bool {
        if let m, m.summary.id == s.id, m.summary.accountId == s.accountId {
            return recordOf(m, crm: crm) != nil
        }
        return s.crmSaved
    }

    /// crm.Key: a message of a mail account, for `InFlight`.
    public struct Key: Sendable, Hashable {
        public var account: AccountID
        public var message: MessageID

        public init(_ account: AccountID, _ message: MessageID) {
            self.account = account
            self.message = message
        }
    }

    /// crm.InFlight: the messages a crm.save or crm.remove runs for, so
    /// their button reads Saving and a second save is not started. The
    /// client touches it on the main actor only.
    public struct InFlight: Sendable, Equatable {
        private var keys: [Key: Int] = [:]

        public init() {}

        /// Marks the messages busy; `end` releases them (counted, so
        /// overlapping calls nest).
        public mutating func begin(_ account: AccountID, _ ids: [MessageID]) {
            for id in ids {
                keys[Key(account, id), default: 0] += 1
            }
        }

        public mutating func end(_ account: AccountID, _ ids: [MessageID]) {
            for id in ids {
                let k = Key(account, id)
                if (keys[k] ?? 0) <= 1 {
                    keys.removeValue(forKey: k)
                } else {
                    keys[k]! -= 1
                }
            }
        }

        public func has(_ account: AccountID, _ id: MessageID) -> Bool {
            (keys[Key(account, id)] ?? 0) > 0
        }

        public func any(_ account: AccountID, _ ids: [MessageID]) -> Bool {
            ids.contains { has(account, $0) }
        }

        public var isEmpty: Bool { keys.isEmpty }
    }

    /// crm.ButtonState: the CRM button of an open message.
    public enum ButtonState: Int, Sendable, CaseIterable {
        /// No eWay-CRM account, or the message cannot be saved.
        case hidden
        /// "Save to eWay-CRM", which opens the dialog.
        case notSaved
        /// A save or removal runs; the button is off.
        case saving
        /// "In eWay-CRM", which opens `savedMenu()`.
        case saved
    }

    /// crm.ButtonStateFor: hidden unless the message can be saved, Saving
    /// while busy, else Saved or Not Saved by `row.saved`.
    public static func buttonState(_ accounts: [Account], _ row: Row, busy: Bool) -> ButtonState {
        let o = offer(for: accounts, rows: [row])
        if !o.shown {
            return .hidden
        }
        if busy {
            return .saving
        }
        return o.saved ? .saved : .notSaved
    }

    /// crm.MenuID: an item of the saved message's menu, in this order.
    public enum MenuID: String, Sendable, CaseIterable {
        /// Opens the saved e-mail item (`CrmRecord.url`).
        case open
        /// The dialog over the saved message (`Dialog.editing`).
        case editLinks
        /// `removeQuestion`, then crm.remove.
        case remove
    }

    /// crm.MenuItem.
    public struct MenuItem: Sendable, Equatable {
        public var id: MenuID
        public var label: String
        /// It deletes the item from the CRM for every user, after a question.
        public var destructive: Bool
    }

    /// crm.NotDownloaded: the messages crm.save needs downloaded first (the
    /// data of its partNotDownloaded), each once; nil for any other error.
    public static func notDownloaded(_ error: (any Error)?) -> [MessageID]? {
        guard let e = error as? RPCError, let d = e.messagesNotDownloaded else { return nil }
        var seen = Set<MessageID>()
        var out: [MessageID] = []
        for id in d.messageIds where !id.rawValue.isEmpty && !seen.contains(id) {
            seen.insert(id)
            out.append(id)
        }
        return out.isEmpty ? nil : out
    }

    /// crm.SignInReason: why the instance cannot be signed in to (1205);
    /// nil for any other error or an unknown reason.
    public static func signInReason(_ error: (any Error)?) -> CrmSignInReason? {
        guard let e = error as? RPCError, let d = e.crmSignInUnsupported else { return nil }
        return d.reason == .microsoft || d.reason == .plan ? d.reason : nil
    }

    /// crm.OpenURL: `u` when it is an https URL with a host and no user
    /// info, else "" (nothing to open). No question is asked.
    public static func openURL(_ u: String) -> String {
        guard let p = URLParts(u), p.scheme == "https", !p.host.isEmpty, !p.hasUser else { return "" }
        return u
    }

    /// crm.DefaultName: the last segment of the service URL's path, else
    /// its host; "" for an address that is not a URL.
    public static func defaultName(_ serviceURL: String) -> String {
        guard let p = URLParts(Jira.trimSpace(serviceURL)), !p.host.isEmpty else { return "" }
        var path = Substring(p.path).trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        if let i = path.lastIndex(of: "/") {
            path = String(path[path.index(after: i)...])
        }
        if let s = path.removingPercentEncoding {
            path = s
        }
        let name = Jira.clean(path)
        if !name.isEmpty {
            return name
        }
        return Jira.clean(p.hostname.lowercased())
    }

    /// crm.ServiceHost: the host and path of an account's service URL, the
    /// subtitle of its row; "" without one.
    public static func serviceHost(_ a: Account) -> String {
        guard let e = a.config.eway, let p = URLParts(Jira.trimSpace(e.serviceUrl)), !p.host.isEmpty else { return "" }
        var path = p.path
        while path.hasSuffix("/") {
            path.removeLast()
        }
        return Jira.clean(p.host.lowercased() + path)
    }

    // MARK: Helpers

    /// What the rules read of a URL, as Go's url.Parse has it: the lower-
    /// cased scheme, the host (with its port), the hostname, the decoded
    /// path and whether it has user info; nil where url.Parse fails.
    struct URLParts {
        var scheme: String
        var host: String
        var hostname: String
        var path: String
        var hasUser: Bool

        init?(_ raw: String) {
            guard let u = Jira.parseURL(raw) else { return nil }
            scheme = u.scheme
            host = u.host
            hostname = u.hostname
            hasUser = u.hasUser
            path = ""
            guard !u.opaque else { return }
            var rest = Substring(raw)
            if let hash = rest.firstIndex(of: "#") {
                rest = rest[..<hash]
            }
            if let q = rest.firstIndex(of: "?") {
                rest = rest[..<q]
            }
            if let colon = rest.range(of: "://"), !u.scheme.isEmpty {
                rest = rest[colon.upperBound...]
                if let slash = rest.firstIndex(of: "/") {
                    rest = rest[slash...]
                } else {
                    rest = ""
                }
            } else if !u.scheme.isEmpty, let colon = rest.firstIndex(of: ":") {
                rest = rest[rest.index(after: colon)...]
            }
            path = String(rest).removingPercentEncoding ?? String(rest)
        }
    }

    /// The ellipsis that ends a text cut by `capBytes`.
    static let ellipsis = "…"

    /// capBytes: `s` capped at `n` UTF-8 bytes on a character boundary,
    /// with an ellipsis when cut.
    static func capBytes(_ s: String, _ n: Int) -> String {
        guard s.utf8.count > n else { return s }
        let limit = n - ellipsis.utf8.count
        var out = String.UnicodeScalarView()
        var bytes = 0
        for r in s.unicodeScalars {
            let w = String(r).utf8.count
            if bytes + w > limit {
                break
            }
            bytes += w
            out.append(r)
        }
        while out.last == " " {
            out.removeLast()
        }
        return String(out) + ellipsis
    }
}
