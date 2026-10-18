// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The CRM's wire types (docs/api.md §4.1 account.detectEway, §4.17,
/// backend/pkg/api/crm.go) with invented data: the account block, the
/// documents with optional fields absent and present, params encoding, the
/// error data and the message fields.
@Suite struct APICodingCRMTests {
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

    private let guid = "0b9e3d2c-1a2b-4c3d-8e9f-001122334455"

    @Test func ewayAccountRoundTrips() throws {
        let a = try decode(Account.self, #"""
        {"id":"acc_3","config":{"name":"Acme CRM","email":"jana@acme.example","kind":"eway",
         "eway":{"serviceUrl":"https://hosting.eway-crm.com/acme","username":"jana","userGuid":"0b9e3d2c-1a2b-4c3d-8e9f-001122334455"}},
         "enabled":true,"state":{"accountId":"acc_3","status":"idle","progress":-1,"pendingOutbox":0},"capabilities":["crm"]}
        """#)
        #expect(a.config.protocolKind == .eway && a.can(.crm) && !a.can(.reply))
        #expect(a.config.eway == EwayConfig(serviceUrl: "https://hosting.eway-crm.com/acme", username: "jana", userGuid: guid))
        #expect(try roundTrip(a) == a)
        let cfg = try encodeObject(EwayConfig(serviceUrl: "https://crm.example.org", username: "petr"))
        #expect(cfg.keys.sorted() == ["followThreads", "serviceUrl", "username"], "an empty user GUID is left out")
        #expect(cfg["followThreads"] as? Bool == false, "always sent (D22)")
        let follows = EwayConfig(serviceUrl: "https://crm.example.org", username: "petr", followThreads: true)
        #expect(try roundTrip(follows) == follows)
        #expect(try decode(EwayConfig.self, #"{"serviceUrl":"https://x.example","username":"u","followThreads":true}"#).followThreads)
        #expect(!(try decode(EwayConfig.self, #"{"serviceUrl":"https://x.example","username":"u"}"#).followThreads), "absent is off")
        let test = try decode(AccountTestResult.self, #"{"eway":{"ok":false,"latencyMs":0,"error":{"code":1201,"message":"x"}}}"#)
        #expect(test.eway?.ok == false && test.eway?.error?.code == .authFailed && test.imap == nil)
    }

    @Test func detectEway() throws {
        let p = try encodeObject(AccountDetectEwayParams(serviceUrl: "hosting.eway-crm.com/acme"))
        #expect(p.keys.sorted() == ["serviceUrl"])
        let r = try decode(AccountDetectEwayResult.self, #"{"serviceUrl":"https://hosting.eway-crm.com/acme","microsoftSignIn":true,"version":"10.0"}"#)
        #expect(r == AccountDetectEwayResult(serviceUrl: "https://hosting.eway-crm.com/acme", microsoftSignIn: true, version: "10.0"))
        #expect(try decode(AccountDetectEwayResult.self, #"{"serviceUrl":"https://x.example","microsoftSignIn":false}"#).version == nil)
        #expect(API.AccountDetectEway.name == "account.detectEway")
    }

    @Test func itemsAndRecords() throws {
        let it = try decode(CrmItem.self, #"{"kind":"deal","guid":"\#(guid)","name":"Fleet","url":"https://open.eway-crm.com/?l=x"}"#)
        #expect(it == CrmItem(kind: .deal, guid: guid, name: "Fleet", url: "https://open.eway-crm.com/?l=x"))
        #expect(!it.superior && it.detail == nil && it.reason == nil)
        let o = try encodeObject(it)
        #expect(o.keys.sorted() == ["guid", "kind", "name", "url"], "omitempty fields left out")
        let full = CrmItem(kind: .contact, guid: guid, name: "Novák, Jan", detail: "jan@acme.example", reason: .address,
                           superior: true, url: "https://open.eway-crm.com/?l=y")
        #expect(try roundTrip(full) == full)
        #expect(try decode(CrmItemKind.self, #""marketing""#).rawValue == "marketing", "an open enum")
        let rec = try decode(CrmRecord.self, #"""
        {"accountId":"acc_3","itemGuid":"\#(guid)","savedAt":"2026-10-17T10:00:00Z","checkedAt":"2026-10-17T10:05:00Z",
         "private":true,"items":null,"url":"https://open.eway-crm.com/?l=e"}
        """#)
        #expect(rec.private && rec.items.isEmpty && rec.accountId == "acc_3" && !rec.auto)
        #expect(try roundTrip(rec) == rec)
        #expect(try encodeObject(rec)["auto"] == nil, "omitempty")
        let auto = CrmRecord(accountId: "acc_3", itemGuid: guid, auto: true, url: "u")
        #expect(try roundTrip(auto).auto && (try encodeObject(auto)["auto"] as? Bool) == true)
    }

    @Test func messageFields() throws {
        let s = try decode(MessageSummary.self, #"""
        {"id":"m_1","accountId":"acc_1","folderId":"f","from":[],"subject":"s","date":"2026-10-17T10:00:00Z","snippet":"",
         "flags":[],"hasAttachments":false,"size":1,"crmSaved":true}
        """#)
        #expect(s.crmSaved)
        let plain = try decode(MessageSummary.self, #"""
        {"id":"m_1","accountId":"acc_1","folderId":"f","from":[],"subject":"s","date":"2026-10-17T10:00:00Z","snippet":"",
         "flags":[],"hasAttachments":false,"size":1}
        """#)
        #expect(!plain.crmSaved)
        #expect(try encodeObject(plain)["crmSaved"] == nil, "omitempty")
        let m = try decode(Message.self, #"""
        {"id":"m_1","accountId":"acc_1","folderId":"f","from":[],"subject":"s","date":"2026-10-17T10:00:00Z","snippet":"",
         "flags":[],"hasAttachments":false,"size":1,"crmSaved":true,"attachments":[],
         "crm":[{"accountId":"acc_3","itemGuid":"\#(guid)","savedAt":"2026-10-17T10:00:00Z","checkedAt":"2026-10-17T10:00:00Z",
                 "private":false,"items":[],"url":"https://open.eway-crm.com/?l=e"}]}
        """#)
        #expect(m.summary.crmSaved && m.crm?.first?.itemGuid == guid)
        #expect(try roundTrip(m) == m)
        #expect(try decode(Message.self, #"{"id":"m","accountId":"a","folderId":"f","from":[],"subject":"","date":"2026-10-17T10:00:00Z","snippet":"","flags":[],"hasAttachments":false,"size":0,"attachments":[]}"#).crm == nil)
    }

    @Test func crmMethods() throws {
        let lookup = try encodeObject(CrmLookupParams(messageAccountId: "acc_1", messageIds: ["m_1"]))
        #expect(lookup.keys.sorted() == ["messageAccountId", "messageIds"], "no accountId: the first eway account")
        let res = try decode(CrmLookupResult.self, #"""
        {"accountId":"acc_3","saved":[{"messageId":"m_1","record":null}],"suggestions":[],"partial":true}
        """#)
        #expect(res.saved == [CrmSavedMessage(messageId: "m_1")] && res.partial && res.suggestions.isEmpty)
        let wire = try encodeObject(CrmSavedMessage(messageId: "m_1"))
        #expect(wire.keys.sorted() == ["messageId", "record"] && wire["record"] is NSNull, "record is always sent")
        #expect(try roundTrip(res) == res)
        #expect(try decode(CrmLookupResult.self, #"{"accountId":"a","saved":null,"suggestions":null}"#).saved.isEmpty)

        let search = try encodeObject(CrmSearchParams(accountId: "acc_3", query: "acme"))
        #expect(search.keys.sorted() == ["accountId", "query"])
        let found = try decode(CrmSearchResult.self, #"{"items":[{"kind":"company","guid":"\#(guid)","name":"Acme","reason":"search","url":""}]}"#)
        #expect(found.items.count == 1 && !found.truncated && found.items[0].reason == .search)

        let save = try encodeObject(CrmSaveParams(
            accountId: "acc_3", messageAccountId: "acc_1", messageIds: ["m_1"],
            link: [CrmItemRef(kind: .contact, guid: guid)], superior: CrmItemRef(kind: .deal, guid: guid), private: true))
        #expect(save.keys.sorted() == ["accountId", "link", "messageAccountId", "messageIds", "private", "superior"])
        #expect((save["superior"] as? [String: Any])?["kind"] as? String == "deal")
        let bare = try encodeObject(CrmSaveParams(messageAccountId: "acc_1", messageIds: ["m_1"]))
        #expect(bare.keys.sorted() == ["messageAccountId", "messageIds"])
        let saved = try decode(CrmSaveResult.self, #"""
        {"results":[{"messageId":"m_1","created":true,"record":{"accountId":"acc_3","itemGuid":"\#(guid)","savedAt":"2026-10-17T10:00:00Z",
          "checkedAt":"2026-10-17T10:00:00Z","private":false,"items":[],"url":"u"}},
         {"messageId":"m_2","error":{"code":1307,"message":"duplicate"}}]}
        """#)
        #expect(saved.results[0].created && saved.results[0].record?.itemGuid == guid && saved.results[0].error == nil)
        #expect(saved.results[1].error?.code == .crmDuplicate && !saved.results[1].created)
        #expect(try roundTrip(saved) == saved)

        let remove = try encodeObject(CrmRemoveParams(messageAccountId: "acc_1", messageId: "m_1"))
        #expect(remove.keys.sorted() == ["messageAccountId", "messageId"])
        #expect([API.CrmLookup.name, API.CrmSearch.name, API.CrmSave.name, API.CrmRemove.name]
            == ["crm.lookup", "crm.search", "crm.save", "crm.remove"])
        #expect(API.CrmLookup.timeout == .seconds(30) && API.CrmSave.timeout == .seconds(900) && API.CrmRemove.timeout == .seconds(30))
    }

    @Test func errorData() throws {
        let e = try decode(RPCError.self, #"{"code":1205,"message":"x","data":{"reason":"microsoft"}}"#)
        #expect(e.code == .crmSignInUnsupported && e.crmSignInUnsupported?.reason == .microsoft)
        #expect(RPCError(code: .serverError, message: "", data: e.data).crmSignInUnsupported == nil)
        let nd = try decode(RPCError.self, #"{"code":1504,"message":"x","data":{"messageIds":["m_1","m_2"]}}"#)
        #expect(nd.messagesNotDownloaded == MessagesNotDownloadedData(messageIds: ["m_1", "m_2"]))
        #expect(RPCError(code: .partNotDownloaded, message: "").messagesNotDownloaded == nil)
        #expect(ErrorCode.crmItemNotFound.rawValue == 1109 && ErrorCode.crmSignInUnsupported.rawValue == 1205)
        #expect(ErrorCode.crmDuplicate.rawValue == 1307 && ErrorCode(rawValue: 1307).name == "crmDuplicate")
    }

    @Test func limitsMatchGo() {
        #expect(API.Limits.maxCrmMessages == 20 && API.Limits.maxCrmItems == 50 && API.Limits.maxCrmSuggestions == 50)
        #expect(API.Limits.maxCrmSearchResults == 50 && API.Limits.minCrmQueryBytes == 2 && API.Limits.maxCrmQueryBytes == 200)
        #expect(API.Limits.maxCrmLookupAddresses == 20 && API.Limits.maxCrmNoteBytes == 32 << 10)
        #expect(API.Limits.maxCrmNameBytes == 256 && API.Limits.maxCrmDetailBytes == 512)
        #expect(AccountKind.eway.rawValue == "eway" && Capability.crm.rawValue == "crm")
    }
}
