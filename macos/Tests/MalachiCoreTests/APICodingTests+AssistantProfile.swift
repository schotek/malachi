// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The assistant profile's wire types (docs/api.md §4.18,
/// backend/pkg/api/assistant.go): the result as the daemon writes it,
/// absent fields, and params that send only what they carry.
@Suite struct APICodingAssistantProfileTests {
    private func decode<T: Decodable>(_ type: T.Type, _ s: String) throws -> T {
        try JSONCoding.decoder().decode(type, from: json(s))
    }

    private func encodeObject<T: Encodable>(_ v: T) throws -> [String: Any] {
        let data = try JSONCoding.encoder().encode(v)
        return try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    @Test func getResultAsGoWritesIt() throws {
        // pkg/api assistant_test.go TestAssistantProfileJSON.
        let r = try decode(AssistantProfileGetResult.self, #"""
        {"profile":{"text":"Píšu stručně.","gender":"masculine",
         "accounts":[{"accountId":"acc_1","note":"Podepisuj jako studio."}],"shareWithMcp":true,
         "updatedAt":"2026-10-21T08:00:00Z"},
         "proposals":[{"id":"p_1","text":"Schůzky v pondělí.","source":"panel","createdAt":"2026-10-21T08:00:00Z"}],
         "limits":{"text":2000,"accountNote":500,"proposal":300}}
        """#)
        #expect(r.profile.text == "Píšu stručně." && r.profile.gender == .masculine && r.profile.shareWithMcp)
        #expect(r.profile.accounts == [ProfileAccountNote(accountId: "acc_1", note: "Podepisuj jako studio.")])
        #expect(r.profile.updatedAt != nil)
        #expect(r.proposals.count == 1 && r.proposals[0].source == .panel && r.proposals[0].accountId == nil)
        #expect(r.limits == ProfileLimits())
    }

    @Test func absentFieldsReadAsDefaults() throws {
        let p = try decode(AssistantProfile.self, #"{"accounts":null}"#)
        #expect(p == AssistantProfile())
        #expect(p.shareWithMcp, "sharing is on unless the daemon says otherwise")
        let r = try decode(AssistantProfileGetResult.self, #"{"profile":{},"proposals":null}"#)
        #expect(r.proposals.isEmpty && r.limits == nil)
        #expect(try decode(ProfileGender.self, #""other""#).rawValue == "other", "a newer value decodes as itself")
    }

    @Test func setSendsOnlyWhatItCarries() throws {
        #expect(try encodeObject(AssistantProfileSetParams()).isEmpty)
        let p = try encodeObject(AssistantProfileSetParams(gender: .feminine, accounts: [ProfileAccountNote(accountId: "acc_1", note: "")]))
        #expect(p.keys.sorted() == ["accounts", "gender"])
        #expect(p["gender"] as? String == "feminine")
        let r = try encodeObject(AssistantProfileResolveParams(id: "p_1", accept: false))
        #expect(r.keys.sorted() == ["accept", "id"])
        let q = try encodeObject(AssistantProfileProposeParams(text: "x", source: .mcp))
        #expect(q.keys.sorted() == ["source", "text"])
    }

    @Test func methodNames() {
        #expect(API.AssistantProfileGet.name == "assistant.profileGet")
        #expect(API.AssistantProfileSet.name == "assistant.profileSet")
        #expect(API.AssistantProfilePropose.name == "assistant.profilePropose")
        #expect(API.AssistantProfileResolve.name == "assistant.profileResolve")
        #expect(API.Notify.assistantProfileChanged == "notify.assistantProfileChanged")
    }
}
