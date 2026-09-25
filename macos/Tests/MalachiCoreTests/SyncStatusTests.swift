// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The counterpart of ui/internal/window/sync_test.go.
@Suite struct SyncStatusTests {
    @Test func syncStatusTextTest() {
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
        func state(_ acc: AccountID, _ status: SyncStatus, folder: FolderID? = nil, progress: Int = -1, pending: Int = 0) -> SyncState {
            SyncState(accountId: acc, status: status, folderId: folder, progress: progress, pendingOutbox: pending)
        }

        let cases: [(String, [AccountID: SyncState], [Account], String, Bool)] = [
            ("no accounts", st(), [], "", false),
            ("only disabled", st(state("a3", .syncing)), Array(accounts[2...]), "", false),
            ("no states, idle from account.list", st(), accounts, "Up to date", false),
            ("idle", st(state("a1", .idle), state("a2", .idle)), accounts, "Up to date", false),
            ("syncing folder with progress", st(state("a1", .syncing, folder: "f_inbox", progress: 42)), accounts, "Syncing Inbox… 42 %", true),
            ("syncing folder without progress", st(state("a1", .syncing, folder: "f_inbox")), accounts, "Syncing Inbox…", true),
            ("syncing unknown folder falls back to the account", st(state("a1", .syncing, folder: "f_gone", progress: 0)), accounts, "Syncing Work… 0 %", true),
            ("syncing whole account, unnamed account uses the address", st(state("a2", .syncing)), accounts, "Syncing home@example.invalid…", true),
            ("syncing beats authRequired", st(state("a1", .authRequired), state("a2", .syncing)), accounts, "Syncing home@example.invalid…", true),
            ("authRequired beats error", st(state("a1", .error), state("a2", .authRequired)), accounts, "Sign-in required", false),
            ("error beats offline", st(state("a1", .offline), state("a2", .error)), accounts, "Sync error", false),
            ("offline beats idle", st(state("a1", .idle), state("a2", .offline)), accounts, "Offline, retrying", false),
            ("disabled account state is ignored", st(state("a3", .error), state("a1", .idle)), accounts, "Up to date", false),
            ("state of an unknown account is ignored", st(state("zzz", .error)), accounts, "Up to date", false),
            ("account.list state used when uncached", st(), [testAccount("a9", state: state("a9", .offline))], "Offline, retrying", false),
            // Sending: the outbox count, summed over the enabled accounts, with
            // the spinner on. Precedence: syncing > authRequired > sending >
            // error > offline.
            ("sending one", st(state("a1", .idle, pending: 1)), accounts, "Sending 1 message…", true),
            ("sending several", st(state("a1", .idle, pending: 3)), accounts, "Sending 3 messages…", true),
            ("sending summed over accounts", st(state("a1", .idle, pending: 1), state("a2", .idle, pending: 1)), accounts, "Sending 2 messages…", true),
            ("syncing beats sending", st(state("a1", .syncing, folder: "f_inbox", pending: 2)), accounts, "Syncing Inbox…", true),
            ("authRequired beats sending", st(state("a1", .authRequired), state("a2", .idle, pending: 1)), accounts, "Sign-in required", false),
            ("sending beats error", st(state("a1", .error, pending: 1)), accounts, "Sending 1 message…", true),
            ("sending beats offline", st(state("a1", .offline), state("a2", .idle, pending: 1)), accounts, "Sending 1 message…", true),
            ("disabled account outbox is ignored", st(state("a3", .idle, pending: 4), state("a1", .idle)), accounts, "Up to date", false),
            ("uncached account outbox from account.list", st(), [testAccount("a9", state: state("a9", .idle, pending: 1))], "Sending 1 message…", true),
        ]
        for (name, states, accounts, want, spinning) in cases {
            let got = syncStatusText(states, accounts, folderName: folderName)
            #expect(got.text == want && got.spinning == spinning, "\(name): got \(got.text)/\(got.spinning)")
        }

        // A nil folderName falls back to the account name.
        let noNames = syncStatusText(st(state("a1", .syncing, folder: "f_inbox")), accounts, folderName: nil)
        #expect(noNames.text == "Syncing Work…")
    }

    /// A refused certificate (certtrust.FromSyncState): syncing >
    /// authRequired > certificate changed > certificate problem > sending >
    /// error > offline.
    @Test func certificateStatuses() throws {
        let accounts = [
            testAccount("a1", name: "Work", email: "w@example.invalid"),
            testAccount("a2", email: "home@example.invalid"),
            testAccount("a3", enabled: false, name: "Paused"),
        ]
        func tls(_ acc: AccountID, _ reason: TLSErrorReason, status: SyncStatus = .offline) throws -> SyncState {
            SyncState(accountId: acc, status: status, error: try tlsError(TLSErrorData(reason: reason)))
        }
        func st(_ states: SyncState...) -> [AccountID: SyncState] {
            Dictionary(uniqueKeysWithValues: states.map { ($0.accountId, $0) })
        }
        let idle = SyncState(accountId: "a2", status: .idle)
        let cases: [(String, [AccountID: SyncState], String, Bool)] = [
            ("untrusted instead of offline", st(try tls("a1", .untrusted), idle), "Certificate problem", false),
            ("error status too", st(try tls("a1", .expired, status: .error), idle), "Certificate problem", false),
            ("changed", st(try tls("a1", .pinMismatch), idle), "Certificate changed", false),
            ("changed beats problem", st(try tls("a1", .untrusted), try tls("a2", .pinMismatch)), "Certificate changed", false),
            ("handshake stays offline", st(try tls("a1", .handshake), idle), "Offline, retrying", false),
            ("starttls stays offline", st(try tls("a1", .starttlsUnavailable), idle), "Offline, retrying", false),
            ("no details stays offline", st(SyncState(accountId: "a1", status: .offline, error: tlsError(nil)), idle), "Offline, retrying", false),
            ("authRequired beats changed", st(try tls("a1", .pinMismatch), SyncState(accountId: "a2", status: .authRequired)), "Sign-in required", false),
            ("syncing beats the certificate", st(try tls("a1", .untrusted), SyncState(accountId: "a2", status: .syncing)), "Syncing home@example.invalid…", true),
            ("certificate beats sending", st(try tls("a1", .untrusted), SyncState(accountId: "a2", status: .idle, pendingOutbox: 1)), "Certificate problem", false),
            ("certificate beats error", st(try tls("a1", .untrusted), SyncState(accountId: "a2", status: .error)), "Certificate problem", false),
            ("disabled account ignored", st(try tls("a3", .pinMismatch), idle), "Up to date", false),
        ]
        for (name, states, want, spinning) in cases {
            let got = syncStatusText(states, accounts, folderName: nil)
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
    }
}
