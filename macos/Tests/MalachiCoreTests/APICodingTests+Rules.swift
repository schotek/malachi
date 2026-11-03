// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The mail rules' wire types (docs/api.md §4.21, backend/pkg/api/rules.go)
/// with invented data: the embedded input of a stored rule, Go's omitempty
/// on the way out, the open enums, the error data and the methods' names.
@Suite struct APICodingRulesTests {
    private func decode<T: Decodable>(_ type: T.Type, _ s: String) throws -> T {
        try JSONCoding.decoder().decode(type, from: json(s))
    }

    private func encodeObject<T: Encodable>(_ v: T) throws -> [String: Any] {
        let data = try JSONCoding.encoder().encode(v)
        return try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    private func roundTrip<T: Codable & Equatable>(_ v: T) throws -> T {
        try JSONCoding.decoder().decode(T.self, from: JSONCoding.encoder().encode(v))
    }

    @Test func storedRules() throws {
        let r = try decode(RulesListResult.self, #"""
        {"rules":[
          {"id":"r_1","name":"News","enabled":true,"accountId":"acc_1","match":"any",
           "conditions":[{"field":"from","op":"domainIs","value":"acme.example"},{"field":"bulk","op":"is","value":"newsletter"}],
           "actions":[{"kind":"markRead"},{"kind":"move","folderId":"f_news"}],"stop":true,
           "outgoing":false,"createdAt":"2026-11-01T10:00:00Z","updatedAt":"2026-11-02T10:00:00Z",
           "applied":12,"lastAppliedAt":"2026-11-02T08:00:00Z","skipped":0},
          {"id":"r_2","name":"Away","enabled":false,"match":"all","conditions":null,"actions":[
             {"kind":"autoReply","subject":"Away","text":"Back on Monday."},{"kind":"forward","to":"a@example.org"},
             {"kind":"label","folderId":""}],
           "stop":false,"outgoing":true,"problem":"sendFailed","createdAt":"2026-11-01T10:00:00Z",
           "updatedAt":"2026-11-01T10:00:00Z","applied":0,"skipped":3}]}
        """#)
        #expect(r.rules.count == 2)
        let a = r.rules[0]
        #expect(a.id == "r_1" && a.name == "News" && a.enabled && a.accountId == "acc_1" && a.match == .any && a.stop)
        #expect(a.conditions[0] == RuleCondition(field: .from, op: .domainIs, value: "acme.example"))
        #expect(a.actions == [RuleAction(kind: .markRead), RuleAction(kind: .move, folderId: "f_news")])
        #expect(a.applied == 12 && a.lastAppliedAt == RFC3339.parse("2026-11-02T08:00:00Z") && a.problem == nil)
        let b = r.rules[1]
        #expect(b.accountId == nil && b.conditions.isEmpty && b.outgoing && b.problem == .sendFailed && b.skipped == 3)
        #expect(b.lastAppliedAt == nil)
        #expect(b.actions[0] == RuleAction(kind: .autoReply, subject: "Away", text: "Back on Monday."))
        #expect(b.actions[2].kind.rawValue == "label" && b.actions[2].folderId == nil, "an open enum, an empty id")
        #expect(RuleActionKind.forward.isOutgoing && RuleActionKind.autoReply.isOutgoing && !RuleActionKind.move.isOutgoing)
        #expect(try roundTrip(r) == r)

        let wire = try encodeObject(a)
        #expect(wire["id"] as? String == "r_1" && wire["name"] as? String == "News" && wire["accountId"] as? String == "acc_1",
                "the input's fields next to the rule's own")
        #expect(wire["problem"] == nil && wire["skipped"] as? Int == 0, "omitempty problem, a plain count")
        let wireB = try encodeObject(b)
        #expect(wireB["accountId"] == nil && wireB["lastAppliedAt"] == nil && wireB["conditions"] is [Any])
        let actions = try #require(wireB["actions"] as? [[String: Any]])
        #expect(actions[0].keys.sorted() == ["kind", "subject", "text"] && actions[1].keys.sorted() == ["kind", "to"])
        #expect(actions[2].keys.sorted() == ["kind"])
    }

    @Test func params() throws {
        let input = RuleInput(name: "N", enabled: true, conditions: [RuleCondition(field: .subject, op: .contains, value: "x")],
                              actions: [RuleAction(kind: .flag)])
        let create = try encodeObject(RulesCreateParams(rule: input))
        #expect(create["confirmOutgoing"] == nil, "omitempty")
        let rule = try #require(create["rule"] as? [String: Any])
        #expect(rule.keys.sorted() == ["actions", "conditions", "enabled", "match", "name", "stop"], "no accountId")
        #expect(rule["match"] as? String == "all" && rule["stop"] as? Bool == false)
        let update = try encodeObject(RulesUpdateParams(id: "r_1", rule: input, confirmOutgoing: true))
        #expect(update["id"] as? String == "r_1" && update["confirmOutgoing"] as? Bool == true)
        #expect(try encodeObject(RulesDeleteParams(id: "r_1")).keys.sorted() == ["id"])
        #expect(try encodeObject(RulesReorderParams(ids: ["r_2", "r_1"]))["ids"] as? [String] == ["r_2", "r_1"])
        let test = try encodeObject(RulesTestParams(rule: input, accountId: "acc_1", folderId: "f_in"))
        #expect(test.keys.sorted() == ["accountId", "folderId", "rule"])
        let apply = try encodeObject(RulesApplyParams(accountId: "acc_1", folderId: "f_in"))
        #expect(apply.keys.sorted() == ["accountId", "folderId"], "messageIds and ruleIds left out")
        let some = try encodeObject(RulesApplyParams(accountId: "acc_1", messageIds: ["m_1"], ruleIds: ["r_1"]))
        #expect(some.keys.sorted() == ["accountId", "messageIds", "ruleIds"])
        #expect(try encodeObject(EmptyParams()).isEmpty)
    }

    @Test func results() throws {
        let t = try decode(RulesTestResult.self, #"{"count":3,"messages":[\#(APICodingTests.summaryJSON)]}"#)
        #expect(t.count == 3 && t.messages.count == 1)
        #expect(try decode(RulesTestResult.self, #"{"count":0,"messages":null}"#).messages.isEmpty)
        let a = try decode(RulesApplyResult.self, #"{"examined":120,"matched":14,"changed":12,"skippedOutgoing":2}"#)
        #expect(a == RulesApplyResult(examined: 120, matched: 14, changed: 12, skippedOutgoing: 2))
        let one = try decode(RuleResult.self, #"{"rule":{"id":"r_1","name":"N","enabled":true,"match":"all","conditions":[],"actions":[],"stop":false,"outgoing":false,"createdAt":"2026-11-01T10:00:00Z","updatedAt":"2026-11-01T10:00:00Z","applied":0,"skipped":0}}"#)
        #expect(one.rule.id == "r_1" && one.rule.input.name == "N")
    }

    @Test func errorData() {
        let e = RPCError(code: .invalidArgument, message: "m", data: .object(["field": .string("conditions"), "index": .number(2)]))
        #expect(e.ruleInvalid == RuleInvalidData(field: "conditions", index: 2))
        let name = RPCError(code: .invalidArgument, message: "m", data: .object(["field": .string("name")]))
        #expect(name.ruleInvalid == RuleInvalidData(field: "name"))
        #expect(RPCError(code: .invalidArgument, message: "m").ruleInvalid == nil)
        #expect(RPCError(code: .ruleNotFound, message: "m", data: .object(["field": .string("name")])).ruleInvalid == nil)
        #expect(ErrorCode.ruleNotFound.rawValue == 1112 && ErrorCode.ruleNotFound.name == "ruleNotFound")
    }

    @Test func methods() {
        #expect(API.rulesMethods.map { $0.name } == [
            "rules.list", "rules.create", "rules.update", "rules.delete", "rules.reorder", "rules.test", "rules.apply",
        ])
        #expect(Array(API.allMethods.dropLast(API.signatureMethods.count).suffix(7)) == API.rulesMethods.map { $0.name })
        #expect(API.RulesList.timeout == RPCTimeouts.default && API.RulesTest.timeout == RPCTimeouts.rulesTest)
        #expect(API.RulesApply.timeout == RPCTimeouts.rulesApply)
        #expect(API.Notify.rulesChanged == "notify.rulesChanged")
        #expect(API.Limits.maxRules == 200 && API.Limits.maxRuleConditions == 20 && API.Limits.maxRuleActions == 10)
        #expect(API.Limits.maxRuleNameBytes == 200 && API.Limits.maxRuleValueBytes == 500 && API.Limits.maxRuleReplyBytes == 8192)
        #expect(API.Limits.maxRuleBodyBytes == 65536 && API.Limits.maxRuleTestMessages == 50 && API.Limits.ruleOutgoingPerHour == 20)
        #expect(API.Limits.ruleReplyInterval == 4 * 24 * 3600)
    }
}
