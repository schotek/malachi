// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// An account state after a tlsError with the given reason, as the client
/// decodes it (sync_test.go `tlsState`).
func tlsState(_ acc: AccountID, _ status: SyncStatus, _ reason: TLSErrorReason) throws -> SyncState {
    SyncState(accountId: acc, status: status, error: try tlsError(TLSErrorData(reason: reason)))
}

/// 2026-09-02 15:30 in the current time zone (sync_test.go and
/// status_test.go `now`).
func statusNow() throws -> Date {
    try #require(Calendar.current.date(from: DateComponents(year: 2026, month: 9, day: 2, hour: 15, minute: 30)))
}

/// The counterpart of ui/internal/window/sync_test.go and status_test.go.
/// A date before today is rendered with the machine's locale (its month
/// names), so the cases that show one expect `formatDate`'s own output
/// where Go spells out "1 Sep"; times of today are locale-free.
@Suite struct SyncStatusTests {
    @Test func syncStatusTextTest() throws {
        let accounts = [
            testAccount("a1", name: "Work", email: "w@example.invalid"),
            testAccount("a2", email: "home@example.invalid"),
            testAccount("a3", enabled: false, name: "Paused"),
        ]
        let folderName: (AccountID, FolderID) -> String = { acc, id in
            acc == "a1" && id == "f_inbox" ? "Inbox" : ""
        }
        func st(_ states: SyncState...) -> [AccountID: SyncState] {
            var m: [AccountID: SyncState] = [:]
            for s in states {
                m[s.accountId] = s
            }
            return m
        }
        func state(
            _ acc: AccountID, _ status: SyncStatus, folder: FolderID? = nil, progress: Int = -1, pending: Int = 0,
            failed: Int = 0, last: Date? = nil
        ) -> SyncState {
            SyncState(
                accountId: acc, status: status, folderId: folder, progress: progress, lastSync: last,
                pendingOutbox: pending, failedOutbox: failed)
        }
        let now = try statusNow()
        let yesterday = try #require(Calendar.current.date(byAdding: .day, value: -1, to: now))
        let twoHoursAgo = now.addingTimeInterval(-7200)
        let hourAgo = now.addingTimeInterval(-3600)
        let halfHourAgo = now.addingTimeInterval(-1800)
        let minuteAgo = now.addingTimeInterval(-60)
        // The date of yesterday in this machine's locale.
        let upToDateYesterday = "Up to date · " + formatDate(yesterday, now: now)
        func idle(_ acc: AccountID, _ last: Date) -> SyncState {
            state(acc, .idle, last: last)
        }
        // single is the first account alone: a state never names it.
        let single = Array(accounts.prefix(1))

        let cases: [(String, [AccountID: SyncState], [Account], String, Bool)] = [
            ("no accounts", st(), [], "", false),
            ("only disabled", st(state("a3", .syncing)), Array(accounts[2...]), "Paused", false),
            ("no states, idle from account.list", st(), accounts, "Up to date", false),
            ("idle", st(state("a1", .idle), state("a2", .idle)), accounts, "Up to date", false),
            ("syncing folder with progress", st(state("a1", .syncing, folder: "f_inbox", progress: 42)), accounts, "Syncing Inbox… 42 %", true),
            ("syncing folder without progress", st(state("a1", .syncing, folder: "f_inbox")), accounts, "Syncing Inbox…", true),
            ("syncing unknown folder falls back to the account", st(state("a1", .syncing, folder: "f_gone", progress: 0)), accounts, "Syncing Work… 0 %", true),
            ("syncing whole account, unnamed account uses the address", st(state("a2", .syncing)), accounts, "Syncing home@example.invalid…", true),
            ("syncing beats authRequired", st(state("a1", .authRequired), state("a2", .syncing)), accounts, "Syncing home@example.invalid…", true),
            ("account.list state used when uncached", st(), [testAccount("a9", state: state("a9", .offline))], "Offline, retrying", false),
            ("disabled account state is ignored", st(state("a3", .error), state("a1", .idle)), accounts, "Up to date", false),
            ("state of an unknown account is ignored", st(state("zzz", .error)), accounts, "Up to date", false),

            // Sign-in required, error and offline name the one account in that
            // state among several enabled ones, by name or else by address.
            // Precedence: syncing > authRequired > error > offline.
            ("authRequired beats error, one of two named", st(state("a1", .error), state("a2", .authRequired)), accounts, "Sign-in required: home@example.invalid", false),
            ("error beats offline, one of two named", st(state("a1", .offline), state("a2", .error)), accounts, "Sync error: home@example.invalid", false),
            ("offline beats idle, one of two named", st(state("a1", .idle), state("a2", .offline)), accounts, "Offline: home@example.invalid", false),
            ("offline named by account name", st(state("a1", .offline)), accounts, "Offline: Work", false),
            ("two signing in: no name", st(state("a1", .authRequired), state("a2", .authRequired)), accounts, "Sign-in required", false),
            ("two in error: no name", st(state("a1", .error), state("a2", .error)), accounts, "Sync error", false),
            ("two offline: no name", st(state("a1", .offline), state("a2", .offline)), accounts, "Offline, retrying", false),
            ("single account signing in: no name", st(state("a1", .authRequired)), single, "Sign-in required", false),
            ("single account in error: no name", st(state("a1", .error)), single, "Sync error", false),
            ("single account offline: no name", st(state("a1", .offline)), single, "Offline, retrying", false),
            // A paused account neither counts as a second one nor gets named.
            ("paused account does not count", st(state("a1", .error)), [accounts[0], accounts[2]], "Sync error", false),
            ("paused account in error is not named", st(state("a3", .error), state("a2", .error)), accounts, "Sync error: home@example.invalid", false),

            // Sending: the outbox count, summed over the enabled accounts, with
            // the spinner on. Precedence: syncing > authRequired > sending >
            // failed > error > offline.
            ("sending one", st(state("a1", .idle, pending: 1)), accounts, "Sending 1 message…", true),
            ("sending several", st(state("a1", .idle, pending: 3)), accounts, "Sending 3 messages…", true),
            ("sending summed over accounts", st(state("a1", .idle, pending: 1), state("a2", .idle, pending: 1)), accounts, "Sending 2 messages…", true),
            ("syncing beats sending", st(state("a1", .syncing, folder: "f_inbox", pending: 2)), accounts, "Syncing Inbox…", true),
            ("authRequired beats sending", st(state("a1", .authRequired), state("a2", .idle, pending: 1)), accounts, "Sign-in required: Work", false),
            ("sending beats error", st(state("a1", .error, pending: 1)), accounts, "Sending 1 message…", true),
            ("sending beats offline", st(state("a1", .offline), state("a2", .idle, pending: 1)), accounts, "Sending 1 message…", true),
            ("disabled account outbox is ignored", st(state("a3", .idle, pending: 4), state("a1", .idle)), accounts, "Up to date", false),
            ("uncached account outbox from account.list", st(), [testAccount("a9", state: state("a9", .idle, pending: 1))], "Sending 1 message…", true),

            // Not sent: the failed outbox messages, summed like the pending
            // ones, without the spinner.
            ("failed one", st(state("a1", .idle, failed: 1)), accounts, "1 message not sent", false),
            ("failed summed over accounts", st(state("a1", .idle, failed: 2), state("a2", .idle, failed: 1)), accounts, "3 messages not sent", false),
            ("sending beats failed", st(state("a1", .idle, pending: 1, failed: 2)), accounts, "Sending 1 message…", true),
            ("authRequired beats failed", st(state("a1", .idle, failed: 1), state("a2", .authRequired)), accounts, "Sign-in required: home@example.invalid", false),
            ("certificate beats failed", st(state("a1", .idle, failed: 1), try tlsState("a2", .offline, .untrusted)), accounts, "Certificate problem", false),
            ("failed beats error", st(state("a1", .error, failed: 1)), accounts, "1 message not sent", false),
            ("failed beats offline", st(state("a1", .offline), state("a2", .idle, failed: 1)), accounts, "1 message not sent", false),
            ("disabled account failed is ignored", st(state("a3", .disabled, failed: 4), state("a1", .idle)), accounts, "Up to date", false),
            ("uncached account failed from account.list", st(), [testAccount("a9", state: state("a9", .idle, failed: 2))], "2 messages not sent", false),

            // Idle names the newest last check of the enabled accounts, as a
            // time today and as a date before.
            ("last check today", st(idle("a1", twoHoursAgo)), accounts, "Up to date · 13:30", false),
            ("newest last check wins", st(idle("a1", twoHoursAgo), idle("a2", hourAgo)), accounts, "Up to date · 14:30", false),
            ("last check yesterday", st(idle("a1", yesterday)), accounts, upToDateYesterday, false),
            ("disabled account last check is ignored", st(idle("a1", twoHoursAgo), idle("a3", minuteAgo)), accounts, "Up to date · 13:30", false),
            ("last check from account.list", st(), [testAccount("a9", state: idle("a9", halfHourAgo))], "Up to date · 15:00", false),
            ("a last check is no excuse for offline", st(idle("a1", hourAgo), state("a2", .offline, last: now)), accounts, "Offline: home@example.invalid", false),
            // Go's zero time is no last check.
            ("a zero last check is none", st(idle("a1", .goZero)), accounts, "Up to date", false),

            // Certificates: a refused or changed certificate is named instead
            // of offline, right after authRequired; a handshake failure stays
            // offline. The account's name is the certificate banner's to say.
            // Precedence: authRequired > changed > problem > sending.
            ("refused certificate", st(try tlsState("a1", .offline, .untrusted), state("a2", .idle)), accounts, "Certificate problem", false),
            ("changed certificate", st(try tlsState("a2", .offline, .pinMismatch)), accounts, "Certificate changed", false),
            ("changed beats refused", st(try tlsState("a1", .offline, .expired), try tlsState("a2", .offline, .pinMismatch)), accounts, "Certificate changed", false),
            ("authRequired beats a certificate", st(try tlsState("a1", .offline, .untrusted), state("a2", .authRequired)), accounts, "Sign-in required: home@example.invalid", false),
            ("certificate beats sending", st(try tlsState("a1", .offline, .untrusted), state("a2", .idle, pending: 1)), accounts, "Certificate problem", false),
            ("certificate beats error", st(try tlsState("a1", .offline, .untrusted), state("a2", .error)), accounts, "Certificate problem", false),
            ("single account certificate", st(try tlsState("a1", .error, .expired)), single, "Certificate problem", false),
            ("handshake stays offline", st(try tlsState("a1", .offline, .handshake)), accounts, "Offline: Work", false),
            ("tlsError without details stays offline", st(SyncState(accountId: "a1", status: .offline, error: tlsError(nil))), accounts, "Offline: Work", false),
            ("disabled account certificate is ignored", st(try tlsState("a3", .offline, .untrusted)), accounts, "Up to date", false),
        ]
        for (name, states, accounts, want, spinning) in cases {
            let got = syncStatusText(states, accounts, folderName: folderName, now: now)
            #expect(got.text == want && got.spinning == spinning, "\(name): got \(got.text)/\(got.spinning)")
        }

        // A nil folderName falls back to the account name.
        let noNames = syncStatusText(st(state("a1", .syncing, folder: "f_inbox")), accounts, folderName: nil, now: now)
        #expect(noNames.text == "Syncing Work…")
    }

    /// More refused certificates (certtrust.FromSyncState) than the Go
    /// table has: syncing > authRequired > certificate changed >
    /// certificate problem > sending > error > offline.
    @Test func certificateStatuses() throws {
        let accounts = [
            testAccount("a1", name: "Work", email: "w@example.invalid"),
            testAccount("a2", email: "home@example.invalid"),
            testAccount("a3", enabled: false, name: "Paused"),
        ]
        func st(_ states: SyncState...) -> [AccountID: SyncState] {
            Dictionary(uniqueKeysWithValues: states.map { ($0.accountId, $0) })
        }
        let idle = SyncState(accountId: "a2", status: .idle)
        let cases: [(String, [AccountID: SyncState], String, Bool)] = [
            ("untrusted instead of offline", st(try tlsState("a1", .offline, .untrusted), idle), "Certificate problem", false),
            ("error status too", st(try tlsState("a1", .error, .expired), idle), "Certificate problem", false),
            ("changed", st(try tlsState("a1", .offline, .pinMismatch), idle), "Certificate changed", false),
            ("changed beats problem", st(try tlsState("a1", .offline, .untrusted), try tlsState("a2", .offline, .pinMismatch)), "Certificate changed", false),
            ("handshake stays offline", st(try tlsState("a1", .offline, .handshake), idle), "Offline: Work", false),
            ("starttls stays offline", st(try tlsState("a1", .offline, .starttlsUnavailable), idle), "Offline: Work", false),
            ("no details stays offline", st(SyncState(accountId: "a1", status: .offline, error: tlsError(nil)), idle), "Offline: Work", false),
            ("authRequired beats changed", st(try tlsState("a1", .offline, .pinMismatch), SyncState(accountId: "a2", status: .authRequired)), "Sign-in required: home@example.invalid", false),
            ("syncing beats the certificate", st(try tlsState("a1", .offline, .untrusted), SyncState(accountId: "a2", status: .syncing)), "Syncing home@example.invalid…", true),
            ("certificate beats sending", st(try tlsState("a1", .offline, .untrusted), SyncState(accountId: "a2", status: .idle, pendingOutbox: 1)), "Certificate problem", false),
            ("certificate beats error", st(try tlsState("a1", .offline, .untrusted), SyncState(accountId: "a2", status: .error)), "Certificate problem", false),
            ("disabled account ignored", st(try tlsState("a3", .offline, .pinMismatch), idle), "Up to date", false),
        ]
        for (name, states, want, spinning) in cases {
            let got = syncStatusText(states, accounts, folderName: nil, now: try statusNow())
            #expect(got.text == want && got.spinning == spinning, "\(name): got \(got.text)/\(got.spinning)")
        }
    }

    @Test func certTextsTest() {
        #expect(certBannerText(.certificate, "Work") == "The certificate of Work is not trusted")
        #expect(certBannerText(.changed, "Work") == "The certificate of Work has changed")
        #expect(certStatusText(.certificate) == "Certificate problem")
        #expect(certStatusText(.changed) == "Certificate changed")
    }

    @Test func certProblemAccountTest() throws {
        let bad = SyncState(accountId: "a2", status: .offline, error: try tlsError(TLSErrorData(reason: .pinMismatch)))
        let accounts = [
            testAccount("a0", enabled: false, state: SyncState(accountId: "a0", status: .offline, error: bad.error)),
            testAccount("a1"),
            testAccount("a2"),
            testAccount("a3", state: SyncState(accountId: "a3", status: .offline, error: try tlsError(TLSErrorData(reason: .expired)))),
        ]
        // The first enabled account in account order; the cached state
        // wins over account.list's.
        let got = certProblemAccount(["a2": bad], accounts)
        #expect(got?.account.id == "a2" && got?.problem.category == .changed)
        #expect(certProblemAccount([:], accounts)?.account.id == "a3", "account.list state when uncached")
        #expect(certProblemAccount(["a3": SyncState(accountId: "a3", status: .idle)], accounts) == nil)
    }

    @Test func authBannerTextTest() {
        let cases: [ErrorCode: String] = [
            .authRequired: "Sign in to Work again",
            .authFailed: "Sign in to Work again",
            .keyringError: "The system keyring is unavailable; Work cannot sign in",
            .networkError: "Work needs attention",
            0: "Work needs attention",
        ]
        for (reason, want) in cases {
            #expect(authBannerText(reason, "Work") == want, "authBannerText(\(reason))")
        }
        #expect(goaAuthBannerText(.unavailable, "Work") == "GNOME Online Accounts is not available; Work cannot sign in")
        #expect(goaAuthBannerText(.authRequired, "Work") == "Sign in to Work again in Settings → Online Accounts")
        #expect(oauthAuthBannerText(.authRequired, "Work") == "Sign in to Work again in your browser")
        #expect(oauthAuthBannerText(.authFailed, "Work") == "Sign in to Work again in your browser")
        #expect(oauthAuthBannerText(.keyringError, "Work") == "The system keyring is unavailable; Work cannot sign in")
        #expect(authBannerButton(.password) == "Open Preferences")
        #expect(authBannerButton(.goa) == "Open Online Accounts")
        #expect(authBannerButton(.oauth) == "Sign In")
    }

    // MARK: status_test.go

    @Test func accountStatusesTest() throws {
        let now = try statusNow()
        let yesterday = try #require(Calendar.current.date(byAdding: .day, value: -1, to: now))
        let twoHoursAgo = now.addingTimeInterval(-7200)
        // The date of yesterday in this machine's locale.
        let lastSyncedYesterday = "Last synced " + formatDate(yesterday, now: now)
        let folderName: (AccountID, FolderID) -> String = { acc, id in
            acc == "a1" && id == "f_inbox" ? "Inbox" : ""
        }
        let work = testAccount("a1", name: "Work", email: "w@example.invalid")
        func state(
            _ status: SyncStatus, folder: FolderID? = nil, progress: Int = -1, pending: Int = 0, failed: Int = 0,
            error: RPCError? = nil, last: Date? = nil
        ) -> SyncState {
            SyncState(
                accountId: "a1", status: status, folderId: folder, progress: progress, lastSync: last, error: error,
                pendingOutbox: pending, failedOutbox: failed)
        }
        func withOutbox(_ s: SyncState, _ pending: Int, _ failed: Int) -> SyncState {
            var s = s
            s.pendingOutbox = pending
            s.failedOutbox = failed
            return s
        }
        let cases: [(String, SyncState, String, StatusAction, Int)] = [
            // Syncing names the folder and its progress, never the account,
            // whose name is the row's title.
            ("syncing folder with progress", state(.syncing, folder: "f_inbox", progress: 42), "Syncing Inbox… 42 %", .check, 0),
            ("syncing folder without progress", state(.syncing, folder: "f_inbox"), "Syncing Inbox…", .check, 0),
            ("syncing the whole account", state(.syncing, progress: 30), "Syncing…", .check, 0),
            ("syncing an unknown folder", state(.syncing, folder: "f_gone", progress: 30), "Syncing…", .check, 0),
            ("syncing beats sign-in", state(.syncing, pending: 1), "Syncing…", .check, 0),
            ("sign-in required", state(.authRequired, pending: 1), "Sign-in required", .signIn, 0),
            // A refused or changed certificate says why and leads to the
            // account's settings, where it can be trusted.
            ("refused certificate", try tlsState("a1", .offline, .untrusted), "The server's certificate is not from a trusted authority", .edit, 0),
            ("changed certificate", try tlsState("a1", .error, .pinMismatch), "The server presented a different certificate than the one you trust", .edit, 0),
            ("certificate beats sending", withOutbox(try tlsState("a1", .offline, .expired), 1, 0), "The server's certificate has expired", .edit, 0),
            ("sending one", state(.idle, pending: 1), "Sending 1 message…", .check, 0),
            ("sending beats error", state(.error, pending: 2), "Sending 2 messages…", .check, 0),
            // Error and offline give the reason when there is one, and a retry.
            ("error with reason", state(.error, error: RPCError(code: .serverError, message: "x")), "The server returned an error", .retry, 0),
            ("error without reason", state(.error), "Sync error", .retry, 0),
            ("offline with reason", state(.offline, error: RPCError(code: .networkError, message: "dial")), "The server could not be reached", .retry, 0),
            ("offline without reason", state(.offline), "Offline, retrying", .retry, 0),
            ("handshake failure is offline", try tlsState("a1", .offline, .handshake), "The secure connection could not be established", .retry, 0),
            ("backend detail in a reason", state(.error, error: RPCError(code: .storageError, message: "<b>boom</b>")), "Failed: <b>boom</b>", .retry, 0),
            // Idle names the last check, as a time today and a date before.
            ("idle, checked today", state(.idle, last: twoHoursAgo), "Last synced 13:30", .check, 0),
            ("idle, checked yesterday", state(.idle, last: yesterday), lastSyncedYesterday, .check, 0),
            ("idle, never checked", state(.idle), "Up to date", .check, 0),
            // Unsent messages are counted whatever the state.
            ("failed while idle", state(.idle, failed: 2), "Up to date", .check, 2),
            ("failed while offline", state(.offline, failed: 1), "Offline, retrying", .retry, 1),
            ("failed while sending", state(.idle, pending: 1, failed: 1), "Sending 1 message…", .check, 1),
        ]
        for (name, s, detail, action, failed) in cases {
            let got = accountStatuses(["a1": s], [work], folderName: folderName, now: now)
            #expect(got.count == 1, "\(name): \(got.count) rows")
            guard let g = got.first else { continue }
            #expect(g.account == "a1" && g.title == "Work", "\(name): \(g)")
            #expect(g.detail == detail && g.action == action && g.failed == failed,
                    "\(name): got \(g.detail) \(g.action) \(g.failed), want \(detail) \(action) \(failed)")
        }
        // A nil folderName must not crash.
        let noNames = accountStatuses(["a1": state(.syncing, folder: "f_inbox", progress: 5)], [work], folderName: nil, now: now)
        #expect(noNames.first?.detail == "Syncing…")
    }

    @Test func accountStatusesAccounts() throws {
        let now = try statusNow()
        var graph = testAccount("a4", name: "Graph")
        graph.config.kind = .graph
        var goa = testAccount("a5", name: "GOA")
        goa.config.kind = .graph
        goa.config.graph = GraphConfig(source: .goa)
        let accounts = [
            testAccount("a1", name: "Work", state: SyncState(accountId: "a1", status: .offline, failedOutbox: 3)),
            testAccount("a2", enabled: false, email: "home@example.invalid",
                        state: SyncState(accountId: "a2", status: .disabled, failedOutbox: 1)),
            testAccount("a3", enabled: false, name: "Paused, fresh",
                        state: SyncState(accountId: "a3", status: .disabled, failedOutbox: 5)),
            graph,
            goa,
        ]
        let states: [AccountID: SyncState] = [
            // a1 has no cached state: account.list's is used.
            // a2 was paused while in error; the state from before the pause
            // is stale and must not show.
            "a2": SyncState(accountId: "a2", status: .error, failedOutbox: 7),
            // a3 changed its outbox after the pause: that state is newer.
            "a3": SyncState(accountId: "a3", status: .disabled, failedOutbox: 0),
            "a4": SyncState(accountId: "a4", status: .authRequired),
            "a5": SyncState(accountId: "a5", status: .authRequired),
            // A state for an account that is not listed makes no row.
            "zzz": SyncState(accountId: "zzz", status: .error),
        ]
        let got = accountStatuses(states, accounts, folderName: nil, now: now)
        let want = [
            AccountStatus(account: "a1", title: "Work", detail: "Offline, retrying", action: .retry, signIn: .password, failed: 3),
            AccountStatus(account: "a2", title: "home@example.invalid", detail: "Paused", action: .noAction, signIn: .password, failed: 1),
            AccountStatus(account: "a3", title: "Paused, fresh", detail: "Paused", action: .noAction, signIn: .password, failed: 0),
            AccountStatus(account: "a4", title: "Graph", detail: "Sign-in required", action: .signIn, signIn: .oauth),
            AccountStatus(account: "a5", title: "GOA", detail: "Sign-in required", action: .signIn, signIn: .goa),
        ]
        #expect(got == want)
        #expect(accountStatuses(states, [], folderName: nil, now: now).isEmpty, "no accounts")
    }

    @Test func statusButtonLabelTest() {
        let cases: [(AccountStatus, String)] = [
            (AccountStatus(account: "a", title: "", detail: "", action: .noAction), ""),
            (AccountStatus(account: "a", title: "", detail: "", action: .check), ""), // an icon of its own
            (AccountStatus(account: "a", title: "", detail: "", action: .retry), "Try Again"),
            (AccountStatus(account: "a", title: "", detail: "", action: .edit), "_Edit Account…"),
            (AccountStatus(account: "a", title: "", detail: "", action: .signIn, signIn: .password), "Open Preferences"),
            (AccountStatus(account: "a", title: "", detail: "", action: .signIn, signIn: .goa), "Open Online Accounts"),
            (AccountStatus(account: "a", title: "", detail: "", action: .signIn, signIn: .oauth), "Sign In"),
        ]
        for (st, want) in cases {
            #expect(statusButtonLabel(st) == want, "statusButtonLabel(\(st))")
        }
    }

    @Test func sameAccountsTest() {
        let list = [
            AccountStatus(account: "a1", title: "", detail: ""),
            AccountStatus(account: "a2", title: "", detail: ""),
        ]
        let cases: [([AccountID], Bool)] = [
            (["a1", "a2"], true),
            (["a2", "a1"], false), // reordered
            (["a1"], false), // added
            (["a1", "a2", "a3"], false),
            ([], false),
        ]
        for (order, want) in cases {
            #expect(sameAccounts(order, list) == want, "sameAccounts(\(order))")
        }
        #expect(sameAccounts([], []), "no accounts, no rows: nothing to rebuild")
    }

    @Test func outboxTexts() {
        #expect(sendingText(1) == "Sending 1 message…")
        #expect(sendingText(4) == "Sending 4 messages…")
        #expect(notSentText(1) == "1 message not sent")
        #expect(notSentText(3) == "3 messages not sent")
    }
}
