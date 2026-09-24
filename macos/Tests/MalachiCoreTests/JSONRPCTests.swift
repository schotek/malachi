// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

@Suite struct JSONRPCTests {
    @Test func requestEncodesAsTheContractExpects() throws {
        let data = try JSONCoding.encoder().encode(Request(id: 7, method: API.systemInfo, params: EmptyParams()))
        let obj = try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
        #expect(obj["jsonrpc"] as? String == "2.0")
        #expect(obj["id"] as? Int == 7)
        #expect(obj["method"] as? String == "system.info")
        #expect((obj["params"] as? [String: Any])?.isEmpty == true)
        #expect(!data.contains(0x0A), "a frame must not contain raw newlines")
    }

    @Test func envelopeClassifiesLines() throws {
        let response = try JSONCoding.decoder().decode(Envelope.self, from: json(#"{"jsonrpc":"2.0","id":3,"result":{"x":1}}"#))
        #expect(response.isResponse && !response.isNotification && response.error == nil)

        let failure = try JSONCoding.decoder().decode(Envelope.self, from: json(#"{"jsonrpc":"2.0","id":4,"error":{"code":1102,"message":"gone"}}"#))
        #expect(failure.error == RPCError(code: 1102, message: "gone"))

        let notification = try JSONCoding.decoder().decode(Envelope.self, from: json(#"{"jsonrpc":"2.0","method":"notify.accountsChanged","params":{}}"#))
        #expect(notification.isNotification && !notification.isResponse)
    }

    @Test func notificationParamsDecodeOnDemand() throws {
        struct P: Decodable, Equatable { let accountId: String }
        let n = RPCNotification(method: "notify.syncState", line: json(#"{"jsonrpc":"2.0","method":"notify.syncState","params":{"accountId":"a1"}}"#))
        #expect(try n.params(P.self) == P(accountId: "a1"))
    }

    @Test func systemInfoDecodes() throws {
        let raw = json(#"{"result":{"version":"0.1.0","protocolVersion":1,"pid":42,"storePath":"/x/store.db"}}"#)
        let info = try JSONCoding.decoder().decode(ResultEnvelope<SystemInfo>.self, from: raw).result
        #expect(info == SystemInfo(version: "0.1.0", protocolVersion: 1, pid: 42, storePath: "/x/store.db"))
    }
}
