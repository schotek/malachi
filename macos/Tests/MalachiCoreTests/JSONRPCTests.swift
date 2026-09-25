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
        #expect(failure.error?.code == .messageNotFound)

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

    @Test func errorDataIsKeptAsJSON() throws {
        let err = try JSONCoding.decoder().decode(RPCError.self, from: json(#"{"code":1502,"message":"too big","data":{"limit":1024,"size":2048,"tags":["a",true,null,1.5],"nested":{"k":"v"}}}"#))
        #expect(err.code == .attachmentTooBig)
        #expect(err.data?["limit"]?.intValue == 1024 && err.data?["size"]?.intValue == 2048)
        #expect(err.data?["tags"]?.arrayValue == [.string("a"), .bool(true), .null, .number(1.5)])
        #expect(err.data?["nested"]?["k"]?.stringValue == "v")
        #expect(err.data?["missing"] == nil && err.data?["tags"]?.intValue == nil)
        #expect(JSONValue.number(1.5).intValue == nil && JSONValue.number(-3).intValue == -3)
        #expect(err.description == "too big (1502)")

        // Round trip, and a nil `data` stays absent (FakeDaemon encodes errors this way).
        let data = try JSONCoding.encoder().encode(err)
        #expect(try JSONCoding.decoder().decode(RPCError.self, from: data) == err)
        let plain = try JSONCoding.encoder().encode(RPCError(code: -32601, message: "nope"))
        let obj = try #require(JSONSerialization.jsonObject(with: plain) as? [String: Any])
        #expect(obj["code"] as? Int == -32601 && obj["data"] == nil)
    }
}
