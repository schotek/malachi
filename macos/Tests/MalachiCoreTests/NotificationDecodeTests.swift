// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// `DaemonNotification` over the four notifications of docs/api.md §5, as
/// the transport hands them over (the whole line, params decoded on demand).
@Suite struct NotificationDecodeTests {
    private func raw(_ method: String, _ params: String) -> RPCNotification {
        RPCNotification(method: method, line: json(#"{"jsonrpc":"2.0","method":"\#(method)","params":\#(params)}"#))
    }

    @Test func newMessage() throws {
        let n = try DaemonNotification(raw("notify.newMessage", #"""
        {"accountId":"acc_1","folderId":"f_inbox","message":\#(APICodingTests.summaryJSON)}
        """#))
        guard case .newMessage(let p) = n else {
            Issue.record("expected newMessage, got \(n)")
            return
        }
        #expect(p.accountId == "acc_1" && p.folderId == "f_inbox")
        #expect(p.message.id == "m_123" && p.message.threadId == "t_9" && p.message.snippet.hasPrefix("plain text"))
    }

    @Test func syncState() throws {
        let n = try DaemonNotification(raw("notify.syncState", #"""
        {"state":{"accountId":"acc_1","status":"syncing","folderId":"f_inbox","progress":42,"lastSync":"2026-09-02T10:00:00Z","pendingOutbox":1}}
        """#))
        #expect(n == .syncState(SyncState(accountId: "acc_1", status: .syncing, folderId: "f_inbox", progress: 42,
                                          lastSync: RFC3339.parse("2026-09-02T10:00:00Z"), pendingOutbox: 1)))
        let failed = try DaemonNotification(raw("notify.syncState", #"""
        {"state":{"accountId":"acc_1","status":"error","progress":-1,"error":{"code":1400,"message":"disk full"},"pendingOutbox":0}}
        """#))
        guard case .syncState(let s) = failed else {
            Issue.record("expected syncState")
            return
        }
        #expect(s.status == .error && s.error?.code == .storageError)
    }

    @Test func authRequired() throws {
        let n = try DaemonNotification(raw("notify.authRequired", #"{"accountId":"acc_1","reason":1201,"message":"535 rejected"}"#))
        #expect(n == .authRequired(AuthRequiredNotification(accountId: "acc_1", reason: .authFailed, message: "535 rejected")))
        let oauth = try DaemonNotification(raw("notify.authRequired", #"{"accountId":"acc_2","reason":1200,"message":"token expired","authUrl":"https://login.example/x"}"#))
        guard case .authRequired(let p) = oauth else {
            Issue.record("expected authRequired")
            return
        }
        #expect(p.reason == .authRequired && p.reason.name == "authRequired" && p.authUrl == "https://login.example/x")
        let keyring = try DaemonNotification(raw("notify.authRequired", #"{"accountId":"acc_3","reason":1202,"message":"no keyring"}"#))
        if case .authRequired(let k) = keyring { #expect(k.reason == .keyringError) } else { Issue.record("expected authRequired") }
    }

    @Test func accountsChanged() throws {
        #expect(try DaemonNotification(raw("notify.accountsChanged", "{}")) == .accountsChanged)
        // The transport may hand over a line without params at all.
        let bare = RPCNotification(method: "notify.accountsChanged", line: json(#"{"jsonrpc":"2.0","method":"notify.accountsChanged"}"#))
        #expect(try DaemonNotification(bare) == .accountsChanged)
    }

    @Test func unknownMethodIsKeptByName() throws {
        let n = try DaemonNotification(raw("notify.somethingNewer", #"{"x":1}"#))
        #expect(n == .unknown(method: "notify.somethingNewer"))
    }

    @Test func malformedKnownNotificationThrows() {
        #expect(throws: DecodingError.self) {
            try DaemonNotification(raw("notify.syncState", "{}"))
        }
        #expect(throws: DecodingError.self) {
            try DaemonNotification(raw("notify.newMessage", #"{"accountId":"a","folderId":"f","message":{"id":"m"}}"#))
        }
    }

    @Test func everyDocumentedNotificationDecodes() throws {
        let samples: [(String, String)] = [
            (API.Notify.newMessage, #"{"accountId":"acc_1","folderId":"f_inbox","message":\#(APICodingTests.summaryJSON)}"#),
            (API.Notify.syncState, #"{"state":{"accountId":"acc_1","status":"idle","progress":-1,"pendingOutbox":0}}"#),
            (API.Notify.authRequired, #"{"accountId":"acc_1","reason":1200,"message":"m"}"#),
            (API.Notify.accountsChanged, "{}"),
        ]
        #expect(samples.map(\.0) == API.allNotifications)
        for (method, params) in samples {
            let n = try DaemonNotification(raw(method, params))
            if case .unknown = n {
                Issue.record("\(method) decoded as unknown")
            }
        }
    }
}
