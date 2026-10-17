// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// An account's Calendar and Invitations groups in Preferences → Accounts
// (docs/calendar.md §5.8) over account.update and calendar.list: Sync
// This Account's Calendars (a Graph account; switching on when the grant
// lacks the calendar scopes ends in state `consentRequired`, and the
// controller then asks with `Prefs.syncConsentNote` and hands the account
// to the existing re-sign-in flow with `calendar: true`), the calendars
// with their checkboxes (`disabledCalendars`), the default calendar for
// new events, Keep Events, and the invitation switches with After
// Answering. Each change is one account.update of the whole
// configuration with empty credentials (the keyring keeps the secret).
//
// The calendars' checkboxes and the default are keyed by the calendars'
// remote ids (`CalendarRecord.remoteId`; calendar.list lists the disabled
// calendars too, with `disabled`), for Graph and CalDAV alike; a CalDAV
// account also lists the server's calendars (account.listCalDAVCalendars
// with `accountId`) so that one the daemon does not store yet can be
// switched on.

import Foundation

@MainActor
public final class CalendarAccountController {
    /// A calendar row of the Calendar group.
    public struct CalendarRow: Sendable, Equatable {
        /// The remote id (CalDAV href); nil when the contract has none.
        public var remoteId: String?
        public var name: String
        public var color: String
        /// Synchronised (not in `disabledCalendars`).
        public var enabled: Bool
        /// "default" / "read only" / "shared · %s".
        public var note: String
        public var writable: Bool
        /// New events go here (the configured default, else the server's).
        public var isDefault = false
        /// The checkbox works (a remote id is known).
        public var canToggle: Bool { remoteId != nil }
    }

    public let source: any CalendarDataSource
    public let accountId: AccountID

    // MARK: Outputs

    public var onChange: (@MainActor () -> Void)?
    /// Asks a question (the consent explanation); nil answers Cancel.
    public var onQuestion: (@MainActor (Cal.Question) async -> Cal.Answer)?
    /// The re-sign-in of the account with `calendar: true`
    /// (`account.oauthStart`), the existing Upravit účet flow.
    public var onSignIn: (@MainActor (AccountID) -> Void)?
    /// Add CalDAV Calendar… of an account without a calendar.
    public var onAddCalDAV: (@MainActor () -> Void)?

    // MARK: State

    public private(set) var saving = false
    /// The last refusal ("" none).
    public private(set) var error = ""
    /// The CalDAV calendars as account.listCalDAVCalendars listed them.
    public private(set) var listed: [CalDAVCalendar]?
    /// The account's configuration as shown (the written change before its
    /// answer).
    public private(set) var config: AccountConfig?

    private var observer: CalendarObserverToken?
    private var askConsent = false
    private var closed = false

    public init(source: any CalendarDataSource, accountId: AccountID) {
        self.source = source
        self.accountId = accountId
        config = source.snapshot.account(accountId)?.config
    }

    public func start() {
        guard observer == nil else { return }
        observer = source.observe { [weak self] c in
            guard let self, c == .snapshot else { return }
            self.snapshotChanged()
        }
        if isCalDAV {
            loadCalDAV()
        }
        snapshotChanged()
    }

    public func stop() {
        closed = true
        observer?.cancel()
        observer = nil
    }

    // MARK: Derived

    public var account: Account? { source.snapshot.account(accountId) }
    public var calendarAccount: CalendarAccount? { source.snapshot.calendarAccount(accountId) }
    public var isCalDAV: Bool { (config?.kind ?? account?.config.kind) == .caldav }
    public var isGraph: Bool { (config?.kind ?? account?.config.kind) == .graph }

    /// The account can have calendars (capability `calendar`).
    public var hasCalendarCapability: Bool { account?.capabilities?.contains(.calendar) ?? false }

    /// An account without the capability (IMAP): "This account has no
    /// calendar." with Add CalDAV Calendar….
    public var showsNoCalendar: Bool { !hasCalendarCapability }

    public var calendarConfig: CalendarConfig {
        config?.calendar ?? CalendarConfig(sync: isCalDAV)
    }

    /// The Sync switch (a CalDAV account always synchronises: no switch).
    public var showsSyncSwitch: Bool { isGraph }
    public var syncOn: Bool { isCalDAV || calendarConfig.sync }

    /// The account's grant lacks the calendar scopes.
    public var consentRequired: Bool { calendarAccount?.state.status == .consentRequired }

    /// The calendars of calendar.list (the disabled ones too, by their
    /// `remoteId`), the checkboxes as the shown configuration says; for a
    /// CalDAV account also the server's calendars the daemon does not
    /// store yet (account.listCalDAVCalendars).
    public var rows: [CalendarRow] {
        let disabled = Set(calendarConfig.disabledCalendars ?? [])
        let def = calendarConfig.defaultCalendar
        var out = (calendarAccount?.calendars ?? []).map { c in
            let remote = c.remoteId.isEmpty ? nil : c.remoteId
            let isDefault = remote.flatMap { r in def.map { $0 == r } } ?? c.isDefault
            return CalendarRow(
                remoteId: remote, name: Cal.cleanLine(c.name), color: c.color,
                enabled: remote.map { !disabled.contains($0) } ?? !c.disabled,
                note: Cal.Prefs.calendarRowNote(isDefault: isDefault, access: c.access, owner: c.owner),
                writable: Cal.Prefs.writable(c), isDefault: isDefault)
        }
        let known = Set(out.compactMap(\.remoteId))
        for c in listed ?? [] where !known.contains(c.href) {
            let isDefault = def.map { $0 == c.href } ?? c.isDefault
            out.append(CalendarRow(
                remoteId: c.href, name: Cal.cleanLine(c.name), color: c.color ?? "", enabled: !disabled.contains(c.href),
                note: Cal.Prefs.calendarRowNote(isDefault: isDefault, access: c.access, owner: nil),
                writable: c.access == .owner || c.access == .write, isDefault: isDefault))
        }
        return out
    }

    /// The Default Calendar popup's choices (the enabled writable rows
    /// with a remote id).
    public var defaultChoices: [CalendarRow] { rows.filter { $0.enabled && $0.writable && $0.canToggle } }

    public var keepChoices: [Int] { Cal.Prefs.keepChoices }
    public var keepDays: Int {
        let k = calendarConfig.keepDays ?? 0
        return k == 0 ? API.Limits.defaultCalendarKeepDays : k
    }

    public var invitationCard: Bool { calendarConfig.showsInvitationCard }
    public var showUnanswered: Bool { calendarConfig.showsUnanswered }
    public var afterAnswer: AfterAnswer { calendarConfig.afterAnswer ?? .keep }

    // MARK: Changes

    /// Sync This Account's Calendars.
    public func setSync(_ on: Bool) {
        guard showsSyncSwitch, on != calendarConfig.sync else { return }
        askConsent = on
        change { $0.sync = on }
    }

    /// A calendar's checkbox.
    public func setEnabled(_ on: Bool, remoteId: String) {
        guard rows.contains(where: { $0.remoteId == remoteId }) else { return }
        change { c in
            var d = c.disabledCalendars ?? []
            d.removeAll { $0 == remoteId }
            if !on {
                d.append(remoteId)
            }
            c.disabledCalendars = d.isEmpty ? nil : d
            if !on && c.defaultCalendar == remoteId {
                c.defaultCalendar = nil
            }
        }
    }

    public func setDefaultCalendar(_ remoteId: String?) {
        guard remoteId != calendarConfig.defaultCalendar else { return }
        change { $0.defaultCalendar = remoteId }
    }

    public func setKeepDays(_ days: Int) {
        guard days != keepDays, Cal.Prefs.keepChoices.contains(days) else { return }
        change { $0.keepDays = days }
    }

    public func setInvitationCard(_ on: Bool) {
        guard on != invitationCard else { return }
        change { $0.invitationCard = on }
    }

    public func setShowUnanswered(_ on: Bool) {
        guard on != showUnanswered else { return }
        change { $0.showUnanswered = on }
    }

    public func setAfterAnswer(_ a: AfterAnswer) {
        guard a != afterAnswer else { return }
        change { $0.afterAnswer = a }
    }

    /// The consent row's Sign In….
    public func signIn() {
        onSignIn?(accountId)
    }

    public func addCalDAV() {
        onAddCalDAV?()
    }

    private func change(_ f: (inout CalendarConfig) -> Void) {
        var cal = calendarConfig
        f(&cal)
        changeConfig { $0.calendar = cal }
    }

    /// One account.update of the whole configuration changed by `f`;
    /// `credentials` empty keeps the stored secret.
    private func changeConfig(_ f: (inout AccountConfig) -> Void, credentials: Credentials = Credentials()) {
        guard var cfg = config else { return }
        let before = cfg
        f(&cfg)
        guard cfg != before || credentials != Credentials() else { return }
        config = cfg
        saving = true
        error = ""
        onChange?()
        let params = AccountUpdateParams(accountId: accountId, config: cfg, credentials: credentials)
        let source = source
        Task { [weak self] in
            var failure: (any Error)?
            do {
                try await source.updateAccount(params)
            } catch {
                failure = error
            }
            guard let self, !self.closed else { return }
            self.saving = false
            if let failure {
                self.config = before
                self.askConsent = false
                self.error = Cal.errorMessage(failure, what: L10n.T("Saving the account"))
            }
            self.onChange?()
            self.snapshotChanged()
        }
    }

    // MARK: The CalDAV Account group

    /// The CalDAV connection as shown (nil: not a CalDAV account).
    public var caldav: CalDAVConfig? { isCalDAV ? config?.caldav : nil }

    /// The account's name as typed.
    public var name: String { config?.name ?? "" }

    /// The server's host (the Server row).
    public var serverHost: String {
        guard let u = caldav?.serverUrl else { return "" }
        let h = Cal.CalDAVWizard.host(u)
        return h.isEmpty ? Cal.cleanLine(u) : h
    }

    public var username: String { Cal.cleanLine(caldav?.username) }

    /// The accounts invitations can leave from (the popup after None).
    public var mailAccounts: [CalDAVWizardController.MailAccount] {
        CalDAVWizardController.mailAccounts(source.snapshot)
    }

    public var mailAccountId: AccountID? {
        guard let m = caldav?.mailAccountId, !m.rawValue.isEmpty else { return nil }
        return m
    }

    /// The server leaves invitations to the client (account.listCalDAVCalendars).
    public private(set) var scheduling: CalDAVScheduling?

    /// A certificate is pinned (the row offers Forget).
    public var certificatePinned: Bool { !(caldav?.certificateSha256 ?? "").isEmpty }

    /// The Certificate row: "Trusted by you", "Verified", "Not encrypted
    /// (local server)".
    public var certificateText: String {
        if certificatePinned {
            return L10n.T("Trusted by you")
        }
        if (caldav?.serverUrl ?? "").lowercased().hasPrefix("http://") {
            return L10n.T("Not encrypted (local server)")
        }
        return L10n.T("Verified")
    }

    /// The Account Name field; an empty name is not written.
    public func setName(_ n: String) {
        let n = Cal.trimSpace(n)
        guard isCalDAV, !n.isEmpty, n != name else { return }
        changeConfig { $0.name = n }
    }

    public func setMailAccount(_ id: AccountID?) {
        guard isCalDAV, id != mailAccountId else { return }
        changeConfig { $0.caldav?.mailAccountId = id }
    }

    /// Forget: the pin goes; the server's certificate must verify again.
    public func forgetCertificate() {
        guard certificatePinned else { return }
        changeConfig { $0.caldav?.certificateSha256 = nil }
    }

    /// Replace Password…: the new password goes to the keyring with
    /// account.update; it is never kept here.
    public func replacePassword(_ password: String) {
        guard isCalDAV, !password.isEmpty else { return }
        changeConfig({ _ in }, credentials: Credentials(password: password))
    }

    // MARK: Loading

    private func loadCalDAV() {
        guard let cfg = config else { return }
        let params = AccountListCalDAVCalendarsParams(accountId: accountId, config: cfg, credentials: Credentials())
        let source = source
        Task { [weak self] in
            let r = try? await source.listCalDAVCalendars(params)
            guard let self, !self.closed, let r else { return }
            self.listed = r.calendars
            self.scheduling = r.scheduling
            self.onChange?()
        }
    }

    private func snapshotChanged() {
        if !saving, let c = source.snapshot.account(accountId)?.config, c != config {
            config = c
        }
        if askConsent && consentRequired {
            askConsent = false
            Task { [weak self] in
                guard let self else { return }
                let q = Cal.Question(heading: Cal.Text.consentLine, body: Cal.Prefs.syncConsentNote, confirm: Cal.Text.signIn)
                if await self.onQuestion?(q) == .confirm {
                    self.signIn()
                }
            }
        }
        onChange?()
    }
}
