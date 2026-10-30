// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// What the contacts controllers share (ui/internal/contactsctl client.go):
// the daemon they call (`ContactsCaller`: the `RPCClient`, or a fake in the
// tests), the strftime writer of birthdays, the clock, the debounce timer
// and the sentence of a failed call. The controllers
// (`ContactsListController`, `ContactsDetailController`,
// `ContactsLookupController`, `ContactsPreferencesController`) are 1:1 ports
// of the Go reference's `WindowController`, `ContactController`, `Lookup`
// and `PreferencesController`: `@MainActor` classes that keep the state of
// the Contacts window, of the contact shown and edited, of the chips'
// lookups and of Settings → Contacts, build what the views show from the
// pure `ContactBook` and report through callbacks. Each call runs in a task
// and its answer comes back on the main actor; a debounce goes through
// `after`, so the tests drive their own timer.

import Foundation
import os

private let contactsLog = Logger(subsystem: "io.github.schotek.Malachi", category: "contacts")

/// The daemon as the contacts controllers call it: `RPCClient`, or a fake
/// in the tests.
public protocol ContactsCaller: Sendable {
    func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params, timeout: Duration?) async throws -> M.Result
}

extension RPCClient: ContactsCaller {}

/// The calls the controllers have running (the tests wait for none).
@MainActor
public final class ContactsCalls {
    /// Calls and questions without their answer.
    public internal(set) var pending = 0

    public init() {}
}

/// contactsctl.Options: what every contacts controller runs with. The
/// window builds one and hands it to each controller.
@MainActor
public struct ContactsOptions {
    /// Runs `f` after the duration on the main actor (a debounce).
    public typealias After = @MainActor (Duration, @escaping @MainActor @Sendable () -> Void) -> Void

    /// contactsctl.WriteTimeout: what a write to the server is given (a
    /// call without a timeout takes its method's own, `RPCMethod.timeout`).
    public static let writeTimeout: Duration = .seconds(60)

    public var caller: any ContactsCaller
    /// Writes birthdays (`ContactBook.strftime(locale:)`).
    public var format: ContactBook.Strftime
    public var now: @Sendable () -> Date
    public var after: After
    /// The usual sentence of a failed call with the action `what`
    /// (`rpcErrorText`) for an error the contacts have no sentence of; nil
    /// is `what` alone.
    public var rpcText: (@MainActor (String, any Error) -> String)?
    public let calls: ContactsCalls

    public init(
        caller: any ContactsCaller,
        format: @escaping ContactBook.Strftime,
        now: @escaping @Sendable () -> Date = { Date() },
        after: @escaping After = ContactsOptions.taskAfter,
        rpcText: (@MainActor (String, any Error) -> String)? = nil,
        calls: ContactsCalls = ContactsCalls()
    ) {
        self.caller = caller
        self.format = format
        self.now = now
        self.after = after
        self.rpcText = rpcText
        self.calls = calls
    }

    /// The application's timer: a task that sleeps.
    public static func taskAfter(_ d: Duration, _ f: @escaping @MainActor @Sendable () -> Void) {
        Task { @MainActor in
            try? await Task.sleep(for: d)
            f()
        }
    }

    /// contactsctl.call: runs the method in a task and hands `done` the
    /// result on the main actor; `timeout` nil is the method's own. Only
    /// the method and the error code are logged, never a contact's text.
    func call<M: RPCMethod>(
        _ method: M.Type, _ params: M.Params, timeout: Duration? = nil,
        _ done: @escaping @MainActor @Sendable (Result<M.Result, any Error>) -> Void
    ) {
        let caller = caller
        let calls = calls
        calls.pending += 1
        Task { @MainActor in
            let outcome: Result<M.Result, any Error>
            do {
                outcome = .success(try await caller.call(M.self, params, timeout: timeout))
            } catch {
                let code = ContactsOptions.asRPCError(error).code.rawValue
                contactsLog.info("contacts call \(M.name, privacy: .public) failed: \(code, privacy: .public)")
                outcome = .failure(error)
            }
            done(outcome)
            calls.pending -= 1
        }
    }

    /// Runs a question (or any other wait) counted with the calls.
    func spawn(_ body: @escaping @MainActor @Sendable () async -> Void) {
        let calls = calls
        calls.pending += 1
        Task { @MainActor in
            await body()
            calls.pending -= 1
        }
    }

    /// contactsctl.Options.ErrorMessage: the contacts' own sentence by code
    /// (`account` is the `accountLabel` for consent), else `rpcText` with
    /// `what`; "" for nil. Never the daemon's message.
    public func errorMessage(_ error: (any Error)?, account: String = "", what: String = "") -> String {
        guard let error else { return "" }
        if let t = ContactBook.errorText(Self.asRPCError(error).code, account: account) {
            return t
        }
        let w = what.isEmpty ? ContactBook.windowTitle : what
        guard let rpcText else { return w }
        return rpcText(w, error)
    }

    /// contactsctl.AsError: the daemon's error as it is, a call without a
    /// connection `unavailable`, one that ran out of time or was cancelled
    /// `cancelled`, anything else `internalError` (only the code counts;
    /// the message stays empty).
    public nonisolated static func asRPCError(_ error: any Error) -> RPCError {
        if let e = error as? RPCError {
            return e
        }
        if let e = error as? RPCClient.ClientError {
            switch e {
            case .notConnected, .disconnected:
                return RPCError(code: .unavailable, message: "")
            case .timeout:
                return RPCError(code: .cancelled, message: "")
            case .transport:
                break
            }
        }
        if error is CancellationError {
            return RPCError(code: .cancelled, message: "")
        }
        return RPCError(code: .internalError, message: "")
    }

    /// contactsctl.ConflictCard: the server's version in a contactConflict.
    public nonisolated static func conflictCard(_ error: any Error) -> ContactCard? {
        guard let s = (error as? RPCError)?.contactConflict?.server, !s.id.rawValue.isEmpty else { return nil }
        return s
    }

    /// contactsctl.ConsentData: a contactsConsentRequired's data.
    public nonisolated static func consentData(_ error: any Error) -> ContactsConsentRequiredData? {
        guard let d = (error as? RPCError)?.contactsConsentRequired, !d.accountId.rawValue.isEmpty else { return nil }
        return d
    }

    /// The error's code; nil for a success.
    nonisolated static func code<T>(_ r: Result<T, any Error>) -> ErrorCode? {
        if case .failure(let e) = r {
            return asRPCError(e).code
        }
        return nil
    }
}

/// contactsctl.ConsentNeed: an account whose contacts wait for Microsoft's
/// consent: the banner `ContactBook.consentBanner(account)` with Allow…
/// (`signIn`) or the Online Accounts advice.
public struct ContactsConsentNeed: Sendable, Equatable {
    public var accountId: AccountID
    public var account: String
    public var signIn: Bool

    public init(accountId: AccountID, account: String, signIn: Bool) {
        self.accountId = accountId
        self.account = account
        self.signIn = signIn
    }
}

extension ContactBook {
    /// contactsctl.consentCandidates: the Microsoft accounts with contacts
    /// on and no listed book (addressBook.list left them out, perhaps for
    /// the consent).
    static func consentCandidates(_ accounts: [Account], books: [AddressBook]) -> [Account] {
        let have = Set(books.compactMap(\.accountId))
        return accounts.filter { a in
            a.enabled && a.config.protocolKind == .graph && a.can(.contacts) && a.config.graph?.contacts?.sync == true
                && !have.contains(a.id)
        }
    }

    /// contactsctl.SyncOn: an account's contacts switch,
    /// graph.contacts.sync or caldav.contacts.
    public static func syncOn(_ c: AccountConfig) -> Bool {
        switch c.protocolKind {
        case .graph: return c.graph?.contacts?.sync ?? false
        case .caldav: return c.caldav?.contacts ?? false
        default: return false
        }
    }

    /// contactsctl.withSync: `c` with the contacts switch set.
    public static func withSync(_ c: AccountConfig, _ on: Bool) -> AccountConfig {
        var c = c
        switch c.protocolKind {
        case .graph:
            var g = c.graph ?? GraphConfig(source: "")
            g.contacts = ContactsConfig(sync: on)
            c.graph = g
        case .caldav:
            var d = c.caldav ?? CalDAVConfig(serverUrl: "", username: "")
            d.contacts = on
            c.caldav = d
        default:
            break
        }
        return c
    }
}
