// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Add CalDAV Calendar sheet (Go caldav.go): its flow (entering →
// discovering → listed → adding → added), which fields and buttons are
// enabled, the certificate-trust offer (the rules of `CertTrust`; the
// confirmation is the account assistant's), the account it adds, and its
// texts. A value type: the controller (M3) owns one and mutates it.

import Foundation

extension Cal {
    /// caldav.go `Wizard`. The password lives here only while the sheet is
    /// open and goes to the daemon's keyring with account.add.
    public struct CalDAVWizard: Sendable, Equatable {
        /// caldav.go `WizardPhase`.
        public enum Phase: Int, Sendable, CaseIterable {
            /// The fields are editable; Discover Calendars is next.
            case entering
            /// account.discoverCalDAV, then account.listCalDAVCalendars run.
            case discovering
            /// The calendars are listed with checkboxes; Add adds.
            case listed
            /// account.add runs.
            case adding
            /// The account exists; the sheet closes.
            case added
        }

        /// caldav.go `WizardStep`: the call of `.discovering`.
        public enum Step: Int, Sendable {
            case discover, list
        }

        /// caldav.go `CalDAVFieldKind`, in the sheet's order.
        public enum Field: Int, Sendable, CaseIterable {
            case name, server, username, password
        }

        public private(set) var phase: Phase = .entering
        public private(set) var step: Step = .discover
        public private(set) var name = ""
        public private(set) var server = ""
        public private(set) var username = ""
        public private(set) var password = ""
        /// account.discoverCalDAV's answer (nil before).
        public private(set) var discovered: AccountDiscoverCalDAVResult?
        /// account.listCalDAVCalendars's answer (nil before).
        public private(set) var listed: AccountListCalDAVCalendarsResult?
        /// The hrefs to synchronise.
        public private(set) var selected: [String: Bool] = [:]
        /// The mail account invitations leave from (client scheduling).
        public private(set) var mailAccount: AccountID?
        /// The certificate the user trusted ("" none).
        public private(set) var pin = ""
        /// The last call's error.
        public private(set) var error: RPCError?
        /// The added account.
        public private(set) var accountId: AccountID?

        /// caldav.go `NewWizard`: `mailAccount` is the default mail account
        /// for invitations (the first compose account, nil none).
        public init(mailAccount: AccountID?) {
            self.mailAccount = mailAccount
        }

        /// Editable fields (not while a call runs).
        public var fieldsEnabled: Bool { phase == .entering || phase == .listed }

        /// caldav.go `Edit`: a change of the server, username or password
        /// drops what was found; the trusted certificate stays only while
        /// the server's host is the same. Ignored while a call runs.
        public mutating func edit(name: String, server: String, username: String, password: String) {
            guard fieldsEnabled else { return }
            let changed = Cal.trimSpace(server) != Cal.trimSpace(self.server) || username != self.username
                || password != self.password
            if CalDAVWizard.host(server).lowercased() != CalDAVWizard.host(self.server).lowercased() {
                pin = ""
            }
            self.name = name
            self.server = server
            self.username = username
            self.password = password
            if changed {
                phase = .entering
                discovered = nil
                listed = nil
                error = nil
                selected.removeAll()
            }
        }

        /// The host of a typed server ("cloud.example.org",
        /// "https://cloud.example.org/dav"); "" when it is not one.
        static func host(_ s: String) -> String {
            var s = Cal.trimSpace(s)
            if !s.contains("://") {
                s = "https://" + s
            }
            guard let c = URLComponents(string: s), var h = c.host else { return "" }
            if h.hasPrefix("[") && h.hasSuffix("]") {
                h = String(h.dropFirst().dropLast())
            }
            return h
        }

        /// The typed server as a URL (https assumed).
        static func typedURL(_ s: String) -> String {
            var s = Cal.trimSpace(s)
            while s.hasSuffix("/") {
                s.removeLast()
            }
            return s.contains("://") ? s : "https://" + s
        }

        /// Discover Calendars enabled.
        public var canDiscover: Bool {
            fieldsEnabled && !CalDAVWizard.host(server).isEmpty && !Cal.trimSpace(username).isEmpty && !password.isEmpty
        }

        /// caldav.go `BeginDiscover`: false when it cannot start.
        @discardableResult
        public mutating func beginDiscover() -> Bool {
            guard canDiscover else { return false }
            phase = .discovering
            step = .discover
            error = nil
            discovered = nil
            listed = nil
            return true
        }

        /// account.discoverCalDAV's params, with the trusted certificate.
        public var discoverParams: AccountDiscoverCalDAVParams {
            AccountDiscoverCalDAVParams(url: Cal.trimSpace(server), certificateSha256: pin.isEmpty ? nil : pin)
        }

        /// The pin for the server the account uses: only while that server
        /// is on the typed host.
        private var pinForServer: String {
            guard !pin.isEmpty, CalDAVWizard.host(serverURL).lowercased() == CalDAVWizard.host(server).lowercased() else {
                return ""
            }
            return pin
        }

        /// caldav.go `DiscoverDone`.
        public mutating func discoverDone(_ res: AccountDiscoverCalDAVResult?, error err: RPCError?) {
            guard phase == .discovering, step == .discover else { return }
            guard err == nil, let res else {
                phase = .entering
                error = err
                return
            }
            discovered = res
            step = .list
        }

        /// The server the account uses: the discovered one, else the typed.
        public var serverURL: String {
            if let d = discovered, !d.serverUrl.isEmpty {
                return d.serverUrl
            }
            return CalDAVWizard.typedURL(server)
        }

        private var config: AccountConfig {
            let pin = pinForServer
            var cal = CalDAVConfig(serverUrl: serverURL, username: Cal.trimSpace(username), certificateSha256: pin.isEmpty ? nil : pin)
            var cfg = AccountConfig(name: Cal.trimSpace(name), email: "", kind: .caldav)
            if cfg.name.isEmpty {
                cfg.name = CalDAVWizard.host(server)
            }
            if let l = listed {
                cal.principalUrl = l.principalUrl
                cal.homeSetUrl = l.homeSetUrl
                if let first = l.addresses.first {
                    cfg.email = first
                }
                if l.scheduling == .client, let m = mailAccount, !m.rawValue.isEmpty {
                    cal.mailAccountId = m
                }
            }
            if cfg.email.isEmpty && cal.username.contains("@") {
                cfg.email = cal.username
            }
            cfg.caldav = cal
            return cfg
        }

        /// account.listCalDAVCalendars's params.
        public var listParams: AccountListCalDAVCalendarsParams {
            AccountListCalDAVCalendarsParams(config: config, credentials: Credentials(password: password))
        }

        /// caldav.go `ListDone`: listed with every calendar selected.
        public mutating func listDone(_ res: AccountListCalDAVCalendarsResult?, error err: RPCError?) {
            guard phase == .discovering, step == .list else { return }
            guard err == nil, let res else {
                phase = .entering
                error = err
                return
            }
            phase = .listed
            listed = res
            selected.removeAll()
            for c in res.calendars {
                selected[c.href] = true
            }
            if Cal.trimSpace(name).isEmpty {
                let n = Cal.cleanLine(res.displayName)
                if !n.isEmpty {
                    name = n
                }
            }
        }

        public func isSelected(_ href: String) -> Bool { selected[href] ?? false }

        /// Selects or deselects a listed calendar.
        public mutating func toggle(_ href: String, _ on: Bool) {
            guard phase == .listed, let l = listed, l.calendars.contains(where: { $0.href == href }) else { return }
            selected[href] = on
        }

        public mutating func setMailAccount(_ id: AccountID?) { mailAccount = id }

        /// The mail-account popup: the server leaves invitations to us.
        public var showsMailAccount: Bool { listed?.scheduling == .client }

        /// Add enabled: listed, a calendar selected.
        public var canAdd: Bool {
            guard phase == .listed, let l = listed else { return false }
            return l.calendars.contains { isSelected($0.href) }
        }

        @discardableResult
        public mutating func beginAdd() -> Bool {
            guard canAdd else { return false }
            phase = .adding
            error = nil
            return true
        }

        /// caldav.go `AddParams`: the deselected calendars disabled and the
        /// server's default (when selected) as the default calendar.
        public var addParams: AccountAddParams {
            var cfg = config
            var cal = CalendarConfig(sync: true)
            for c in listed?.calendars ?? [] {
                if !isSelected(c.href) {
                    cal.disabledCalendars = (cal.disabledCalendars ?? []) + [c.href]
                } else if c.isDefault {
                    cal.defaultCalendar = c.href
                }
            }
            cfg.calendar = cal
            return AccountAddParams(config: cfg, credentials: Credentials(password: password))
        }

        /// caldav.go `AddDone`: added, or back to listed with the error.
        public mutating func addDone(_ id: AccountID?, error err: RPCError?) {
            guard phase == .adding else { return }
            if let err {
                phase = .listed
                error = err
                return
            }
            phase = .added
            accountId = id
            password = ""
        }

        /// caldav.go `TrustOffer`: a certificate problem (not a connection
        /// one) with a valid fingerprint at an https server, not the pinned
        /// one.
        public var trustOffer: CertTrust.Problem? {
            guard phase == .entering, let e = error, e.code == .tlsError, serverURL.lowercased().hasPrefix("https://"),
                  let p = CertTrust.details(e), let cert = p.cert, p.category != .connection, cert.sha256 != pin else {
                return nil
            }
            return p
        }

        /// caldav.go `Trust`: pins the offered certificate after the user
        /// confirmed it and starts discovery again (the params then carry
        /// the pin). False when nothing was offered.
        @discardableResult
        public mutating func trust() -> Bool {
            guard let p = trustOffer, let cert = p.cert else { return false }
            pin = cert.sha256
            error = nil
            return beginDiscover()
        }

        // MARK: Texts

        public static var title: String { L10n.T("Add CalDAV Calendar") }
        public static var subtitle: String {
            L10n.T("Nextcloud, Fastmail, iCloud, Radicale and other servers that speak CalDAV.")
        }

        public static func fieldLabel(_ f: Field) -> String {
            switch f {
            case .server: L10n.T("Server Address")
            case .username: L10n.T("Username")
            case .password: L10n.T("Password")
            case .name: L10n.T("Account Name")
            }
        }

        /// The server field's example; not translated.
        public static let serverPlaceholder = "cloud.example.org"

        public static var passwordNote: String {
            L10n.T("The password goes to the system keyring. Malachi Mail never shows it again.")
        }

        public static var discoverButton: String { L10n.T("Discover Calendars") }

        /// The line while a call runs ("" otherwise).
        public var progress: String {
            switch phase {
            case .discovering: L10n.T("Looking for calendars…")
            case .adding: L10n.T("Adding the account…")
            default: ""
            }
        }

        public static func foundVia(_ v: CalDAVFoundVia) -> String {
            switch v {
            case .wellKnown: "/.well-known/caldav"
            case .srv: L10n.T("a DNS SRV record")
            default: L10n.T("the address you entered")
            }
        }

        /// "Found through /.well-known/caldav · 4 calendars"; "" before.
        public var foundLine: String {
            guard let l = listed else { return "" }
            let via = discovered?.foundVia ?? .url
            let n = l.calendars.count
            return L10n.N("Found through %s · %d calendar", "Found through %s · %d calendars", n, CalDAVWizard.foundVia(via), n)
        }

        /// Discovery ended at another host than the one typed.
        public var serverChanged: String {
            guard let d = discovered, CalDAVWizard.host(d.serverUrl).lowercased() != CalDAVWizard.host(server).lowercased() else {
                return ""
            }
            return L10n.T("The calendars are at %s.", CalDAVWizard.host(d.serverUrl))
        }

        public static var certificateLabel: String { L10n.T("Certificate") }

        /// "Verified (R11)", "Trusted by you", "" before discovery.
        public var certificateText: String {
            if !pin.isEmpty {
                return L10n.T("Trusted by you")
            }
            if let c = discovered?.certificate {
                let issuer = Cal.cleanLine(c.issuer)
                return issuer.isEmpty ? L10n.T("Verified") : L10n.T("Verified (%s)", issuer)
            }
            if discovered != nil && serverURL.lowercased().hasPrefix("http://") {
                return L10n.T("Not encrypted (local server)")
            }
            return ""
        }

        public static var trustButton: String { L10n.T("Trust Certificate…") }
        public static var calendarsHeading: String { L10n.T("Calendars") }

        /// The note after a found calendar's name.
        public static func listedNote(_ c: CalDAVCalendar) -> String {
            Prefs.calendarRowNote(isDefault: c.isDefault, access: c.access, owner: nil)
        }

        public static var footer: String { L10n.T("Not shown in Mail; in Calendar with the CALDAV capsule.") }
        public static var addButton: String { L10n.C("caldav", "Add") }

        /// The sentence of the sheet's error ("" none).
        public var errorText: String {
            guard let e = error else { return "" }
            switch e.code {
            case .authFailed, .authRequired: return L10n.T("The server did not accept the username or password.")
            case .tlsError: return L10n.T("The server’s certificate is not trusted.")
            case .networkError, .serverTimeout, .offline: return L10n.T("The server could not be reached.")
            case .serverError: return L10n.T("The server answered, but it is not a CalDAV server.")
            case .invalidArgument: return L10n.T("This is not a server address Malachi Mail can use.")
            case .keyringError: return L10n.T("The password could not be stored in the system keyring.")
            default: return L10n.T("The calendars could not be found.")
            }
        }
    }
}
