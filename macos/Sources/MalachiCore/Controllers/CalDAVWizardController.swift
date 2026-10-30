// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Add CalDAV Calendar sheet (docs/calendar.md §5.8): owns one
// `Cal.CalDAVWizard` value and runs its calls — Discover Calendars
// (account.discoverCalDAV, then account.listCalDAVCalendars), Add
// (account.add) — and the certificate trust: on a `tlsError` the wizard
// offers Trust Certificate…, the confirmation is the account assistant's
// (`trustPrompt`, presented by the sheet through `onConfirmTrust`), and a
// confirmed certificate runs discovery again with the pin. The mail
// account popup lists the accounts that can send (`compose`), for a
// server that leaves the invitations to the client. Nothing typed is
// logged; the password leaves only in the calls' `Credentials`.

import Foundation

@MainActor
public final class CalDAVWizardController {
    /// A choice of the mail account popup.
    public struct MailAccount: Sendable, Equatable {
        public var id: AccountID
        public var name: String
    }

    public let source: any CalendarDataSource

    // MARK: Outputs

    public var onChange: (@MainActor () -> Void)?
    /// The certificate confirmation; nil or false pins nothing.
    public var onConfirmTrust: (@MainActor (TrustPrompt) async -> Bool)?
    /// The account was added; the sheet closes.
    public var onDone: (@MainActor (AccountID) -> Void)?

    // MARK: State

    public private(set) var wizard: Cal.CalDAVWizard
    /// The last call failed with the preview's error.
    public private(set) var previewRefused = false
    /// Bumped per call and by every edit so stale answers bail out.
    private var op = 0
    private var closed = false
    private var observer: CalendarObserverToken?
    /// The user chose in the mail-account popup (None too).
    private var mailAccountChosen = false

    public init(source: any CalendarDataSource) {
        self.source = source
        let first = Self.mailAccounts(source.snapshot).first?.id
        wizard = Cal.CalDAVWizard(mailAccount: first)
    }

    /// The accounts invitations can leave from, in the accounts' order.
    static func mailAccounts(_ s: Cal.Snapshot) -> [MailAccount] {
        s.accounts.filter { ($0.capabilities ?? API.mailCapabilities).contains(.compose) && $0.config.kind != .caldav }
            .map { MailAccount(id: $0.id, name: Cal.cleanLine($0.config.name)) }
    }

    public var mailAccounts: [MailAccount] { Self.mailAccounts(source.snapshot) }

    /// The sheet's error line ("" none).
    public var errorText: String { previewRefused ? Cal.Text.previewOnly : wizard.errorText }

    /// Follows the source: the mail accounts arrive with its snapshot, and
    /// the first one becomes the default until the user chooses.
    public func start() {
        guard observer == nil, !closed else { return }
        observer = source.observe { [weak self] c in
            guard let self, c == .snapshot else { return }
            self.snapshotChanged()
        }
        snapshotChanged()
    }

    private func snapshotChanged() {
        if !mailAccountChosen {
            let first = mailAccounts.first?.id
            if wizard.mailAccount != first {
                wizard.setMailAccount(first)
            }
        }
        changed()
    }

    /// The sheet closed (Cancel): nothing is taken any more.
    public func cancel() {
        closed = true
        op += 1
        observer?.cancel()
        observer = nil
    }

    /// A found calendar as the sheet lists it.
    public struct ListedCalendar: Sendable, Equatable {
        public var href: String
        public var name: String
        /// `#rrggbb`, "" none.
        public var color: String
        /// "default" / "read only".
        public var note: String
        public var selected: Bool
    }

    /// The found calendars, their names cleaned.
    public var listedCalendars: [ListedCalendar] {
        (wizard.listed?.calendars ?? []).map { c in
            ListedCalendar(
                href: c.href, name: Cal.cleanLine(c.name), color: c.color ?? "",
                note: Cal.CalDAVWizard.listedNote(c), selected: wizard.isSelected(c.href))
        }
    }

    /// A found address book as the sheet lists it: its name cleaned.
    public var listedAddressBooks: [String] {
        wizard.addressBooks.map { Cal.cleanLine($0.name) }
    }

    // MARK: Fields

    public func edit(name: String, server: String, username: String, password: String) {
        let before = wizard
        wizard.edit(name: name, server: server, username: username, password: password)
        if wizard != before {
            previewRefused = false
            changed()
        }
    }

    public func toggle(_ href: String, _ on: Bool) {
        wizard.toggle(href, on)
        changed()
    }

    /// Sync Contacts of the Address Books section.
    public func setContacts(_ on: Bool) {
        wizard.setContacts(on)
        changed()
    }

    public func setMailAccount(_ id: AccountID?) {
        mailAccountChosen = true
        wizard.setMailAccount(id)
        changed()
    }

    // MARK: Calls

    /// Discover Calendars.
    public func discover() {
        guard wizard.beginDiscover() else { return }
        previewRefused = false
        changed()
        runDiscovery()
    }

    /// The calls of the discovering phase: discovery, then the list.
    private func runDiscovery() {
        op += 1
        let current = op
        let params = wizard.discoverParams
        let source = source
        Task { [weak self] in
            var res: AccountDiscoverCalDAVResult?
            var err: RPCError?
            do {
                res = try await source.discoverCalDAV(params)
            } catch {
                err = self?.rpcError(error)
            }
            guard let self, !self.closed, current == self.op else { return }
            self.wizard.discoverDone(res, error: err)
            self.changed()
            guard err == nil, self.wizard.phase == .discovering, self.wizard.step == .list else { return }
            let list = self.wizard.listParams
            var listed: AccountListCalDAVCalendarsResult?
            var listErr: RPCError?
            do {
                listed = try await source.listCalDAVCalendars(list)
            } catch {
                listErr = self.rpcError(error)
            }
            guard !self.closed, current == self.op else { return }
            self.wizard.listDone(listed, error: listErr)
            self.changed()
        }
    }

    /// Trust Certificate…: the confirmation, then discovery with the pin.
    public func trust() {
        guard let offer = wizard.trustOffer, let cert = offer.cert, let confirm = onConfirmTrust else { return }
        let prompt = trustPrompt(
            cert, servers: [Cal.CalDAVWizard.host(wizard.serverURL)], changed: offer.category == .changed,
            previous: offer.expected)
        let current = op
        Task { [weak self] in
            let ok = await confirm(prompt)
            guard ok, let self, !self.closed, current == self.op, self.wizard.trust() else { return }
            self.changed()
            self.runDiscovery()
        }
    }

    /// Add.
    public func add() {
        guard wizard.beginAdd() else { return }
        previewRefused = false
        changed()
        op += 1
        let current = op
        let params = wizard.addParams
        let source = source
        Task { [weak self] in
            var id: AccountID?
            var err: RPCError?
            do {
                id = try await source.addAccount(params).accountId
            } catch {
                err = self?.rpcError(error)
            }
            guard let self, !self.closed, current == self.op else { return }
            self.wizard.addDone(id, error: err)
            self.changed()
            if let id, err == nil {
                self.onDone?(id)
            }
        }
    }

    /// Technical English (`RPCError.message` is never shown).
    private static let previewCode = "preview"
    private static let timeoutCode = "timeout"

    /// Any error as the wizard takes it (a timeout as serverTimeout).
    private func rpcError(_ error: any Error) -> RPCError {
        if let e = error as? RPCError {
            return e
        }
        if let e = error as? Cal.SourceError, e == .preview {
            previewRefused = true
            return RPCError(code: .notImplemented, message: Self.previewCode)
        }
        if let e = error as? RPCClient.ClientError, case .timeout = e {
            return RPCError(code: .serverTimeout, message: Self.timeoutCode)
        }
        return RPCError(code: .networkError, message: "\(error)")
    }

    private func changed() {
        guard !closed else { return }
        onChange?()
    }
}
