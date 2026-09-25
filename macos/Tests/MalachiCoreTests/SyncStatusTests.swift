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
    }
}
