// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiKeychain

@Suite struct RequestTests {
    private func parse(_ op: String, _ json: String) -> Result<Request, HelperExit> {
        Request.parse(op: op, data: Data(json.utf8))
    }

    @Test func parsesEveryOperation() throws {
        let get = try parse("get", #"{"account":"acc_1","key":"password"}"#).get()
        #expect(get == Request(account: "acc_1", key: "password"))
        let set = try parse("set", #"{"account":"acc_1","key":"password","value":"hunter2"}"#).get()
        #expect(set.value == "hunter2")
        let del = try parse("delete", #"{"account":"acc_1","key":"oauth2.refresh_token"}"#).get()
        #expect(del.key == "oauth2.refresh_token")
        // A trailing newline and unknown keys are fine: the daemon sends one line.
        #expect((try? parse("get", "{\"account\":\"acc_1\",\"key\":\"password\",\"extra\":1}\n").get()) != nil)
    }

    @Test func rejectsBadInput() {
        #expect(parse("fetch", #"{"account":"acc_1","key":"password"}"#) == .failure(.badRequest))
        #expect(parse("get", "not json") == .failure(.badRequest))
        #expect(parse("get", "") == .failure(.badRequest))
        #expect(parse("get", #"{"key":"password"}"#) == .failure(.badRequest))
        #expect(parse("get", #"{"account":"acc_1"}"#) == .failure(.badRequest))
        #expect(parse("set", #"{"account":"acc_1","key":"password"}"#) == .failure(.badRequest), "set needs a value")
        #expect(parse("get", #"{"account":"acc_1","key":"password","value":null}"#).isSuccess)
    }

    @Test func rejectsIdentifiersThatCouldReachAnAttribute() {
        for bad in ["", "acc/1", "acc 1", "acc\n1", "ünïcode", "a\u{0}b", String(repeating: "a", count: 129)] {
            #expect(!Request.isIdentifier(bad), "\(bad.debugDescription) must not pass")
            let json = "{\"account\":\(quoted(bad)),\"key\":\"password\"}"
            #expect(parse("get", json) == .failure(.badRequest), "account \(bad.debugDescription)")
            let keyJSON = "{\"account\":\"acc_1\",\"key\":\(quoted(bad))}"
            #expect(parse("get", keyJSON) == .failure(.badRequest), "key \(bad.debugDescription)")
        }
        for good in ["acc_1", "password", "oauth2.refresh_token", "A-Z.0", String(repeating: "a", count: 128)] {
            #expect(Request.isIdentifier(good), "\(good) must pass")
        }
    }

    @Test func rejectsOversizedInput() {
        let padding = String(repeating: " ", count: Request.maxInput)
        let json = #"{"account":"acc_1","key":"password"}"# + padding
        #expect(parse("get", json) == .failure(.badRequest))
    }

    @Test func itemAccountAndLabelNameTheIdentifiersOnly() {
        let r = Request(account: "acc_1", key: "password", value: "hunter2")
        #expect(r.itemAccount == "acc_1/password")
        #expect(r.label == "Malachi Mail: acc_1 (password)")
        #expect(!r.label.contains("hunter2"))
        #expect(Request.service == "io.github.schotek.Malachi")
    }

    @Test func exitCodesFollowTheProtocol() {
        #expect(HelperExit.ok.rawValue == 0)
        #expect(HelperExit.failure.rawValue == 1)
        #expect(HelperExit.notFound.rawValue == 2)
        #expect(HelperExit.badRequest.rawValue == 3)
    }

    @Test func valueLineIsOneJSONLine() throws {
        #expect(Request.valueLine("hunter2") == "{\"value\":\"hunter2\"}\n")
        let tricky = "a\"b\\c\nd\u{1}e"
        let line = Request.valueLine(tricky)
        #expect(line.hasSuffix("\n"))
        #expect(line.dropLast().contains("\n") == false)
        let back = try JSONDecoder().decode([String: String].self, from: Data(line.utf8))
        #expect(back["value"] == tricky)
    }

    private func quoted(_ s: String) -> String {
        let data = (try? JSONEncoder().encode([s])) ?? Data("[\"\"]".utf8)
        let array = String(decoding: data, as: UTF8.self)
        return String(array.dropFirst().dropLast())
    }
}

/// A real round trip through the login keychain. It can prompt and leaves
/// nothing behind; it runs only on request.
@Suite struct KeychainRoundTripTests {
    @Test(.enabled(if: ProcessInfo.processInfo.environment["MALACHI_KEYCHAIN_TEST"] == "1"))
    func storesReadsReplacesAndDeletes() throws {
        let store = KeychainStore()
        let request = Request(account: "test_" + UUID().uuidString.lowercased(), key: "password")
        defer { _ = store.delete(request) }

        #expect(store.get(request) == .failure(.notFound))
        #expect(throws: Never.self) { try store.set(request, value: "first").get() }
        #expect(store.get(request) == .success("first"))
        #expect(throws: Never.self) { try store.set(request, value: "second").get() }
        #expect(store.get(request) == .success("second"))
        #expect(throws: Never.self) { try store.delete(request).get() }
        #expect(store.get(request) == .failure(.notFound))
        #expect(throws: Never.self, "deleting a missing item is success") { try store.delete(request).get() }
    }
}

private extension Result {
    var isSuccess: Bool {
        if case .success = self { return true }
        return false
    }
}
