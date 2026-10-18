// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/crm crm_test.go, ported. Fictional accounts and items only.

func crmAccount(_ id: String, kind: AccountKind? = nil, enabled: Bool = true, serviceURL: String? = nil) -> Account {
    var cfg = AccountConfig(name: "", email: id == "m1" ? "jana@acme.example" : "", kind: kind)
    if let serviceURL {
        cfg.eway = EwayConfig(serviceUrl: serviceURL, username: "jana")
    }
    return Account(id: AccountID(id), config: cfg, enabled: enabled, state: SyncState(accountId: AccountID(id), status: .idle))
}

let crmImap = crmAccount("m1")
let crmGraph = crmAccount("g1", kind: .graph)
let crmJira = crmAccount("j1", kind: .jira)
let crmEwayOff = crmAccount("e0", kind: .eway, enabled: false, serviceURL: "https://hosting.eway-crm.com/old")
let crmEwayA = crmAccount("e1", kind: .eway, serviceURL: "https://hosting.eway-crm.com/acme")
let crmEwayB = crmAccount("e2", kind: .eway, serviceURL: "https://crm.example.org")

struct CRMTests {
    /// The first enabled eway account in list order (D11, §5 Q12).
    @Test func available() {
        let all = [crmImap, crmEwayOff, crmJira, crmEwayA, crmEwayB]
        #expect(CRM.accounts(all).map(\.id) == ["e1", "e2"])
        #expect(CRM.available(all)?.id == "e1")
        #expect(CRM.available([crmImap, crmEwayOff]) == nil, "a paused account is available")
        #expect(CRM.available([]) == nil)
    }

    /// D6: mail accounts only, never Outbox, Drafts or a local draft; Junk
    /// is allowed.
    @Test func canSave() {
        let cases: [(String, Account, FolderRole, Bool, Bool)] = [
            ("inbox of imap", crmImap, .inbox, false, true),
            ("folder of graph", crmGraph, .none, false, true),
            ("sent", crmImap, .sent, false, true),
            ("junk", crmImap, .junk, false, true),
            ("archive", crmImap, .archive, false, true),
            ("outbox", crmImap, .outbox, false, false),
            ("drafts", crmImap, .drafts, false, false),
            ("local draft", crmImap, .inbox, true, false),
            ("jira", crmJira, .none, false, false),
            ("eway itself", crmEwayA, .none, false, false),
            ("caldav", crmAccount("c", kind: .caldav), .none, false, false),
        ]
        for (name, acc, role, local, want) in cases {
            #expect(CRM.canSave(acc, role: role, localDraft: local) == want, "\(name)")
        }
    }

    /// One rule for the button, the context menu and the multi-selection.
    @Test func offerFor() {
        let accs = [crmImap, crmEwayA]
        let inbox = CRM.Row(account: crmImap, role: .inbox)
        var saved = inbox
        saved.saved = true
        let outbox = CRM.Row(account: crmImap, role: .outbox)
        let rows: (Int, CRM.Row) -> [CRM.Row] = { n, r in Array(repeating: r, count: n) }
        let cases: [(String, [Account], [CRM.Row], CRM.Offer)] = [
            ("no eway account", [crmImap], [inbox], CRM.Offer()),
            ("nothing selected", accs, [], CRM.Offer()),
            ("one message", accs, [inbox], CRM.Offer(shown: true, enabled: true, count: 1)),
            ("one saved", accs, [saved], CRM.Offer(shown: true, enabled: true, count: 1, saved: true)),
            ("one in the outbox", accs, [outbox], CRM.Offer()),
            ("one of jira", accs, [CRM.Row(account: crmJira, role: .none)], CRM.Offer()),
            ("several", accs, [inbox, saved], CRM.Offer(shown: true, enabled: true, count: 2)),
            ("several, one a draft", accs, [inbox, CRM.Row(account: crmImap, role: .drafts)], CRM.Offer(shown: true, count: 2)),
            // A conversation's row stands for its members, not for one message (owner, 2026-10-17).
            ("a conversation's row", accs, [CRM.Row(account: crmImap, role: .inbox, conversation: true)], CRM.Offer()),
            ("a conversation's row among others", accs, [inbox, CRM.Row(account: crmImap, role: .inbox, conversation: true)], CRM.Offer()),
            ("the most", accs, rows(CRM.maxMessages, inbox), CRM.Offer(shown: true, enabled: true, count: CRM.maxMessages)),
            ("too many", accs, rows(CRM.maxMessages + 1, inbox), CRM.Offer(shown: true, count: CRM.maxMessages + 1)),
            ("several of jira", accs, rows(2, CRM.Row(account: crmJira, role: .none)), CRM.Offer()),
        ]
        for (name, a, r, want) in cases {
            #expect(CRM.offer(for: a, rows: r) == want, "\(name)")
        }
    }

    @Test func button() {
        let accs = [crmImap, crmEwayA]
        var inbox = CRM.Row(account: crmImap, role: .inbox)
        #expect(CRM.buttonState(accs, inbox, busy: false) == .notSaved)
        #expect(CRM.buttonState(accs, inbox, busy: true) == .saving)
        inbox.saved = true
        #expect(CRM.buttonState(accs, inbox, busy: false) == .saved)
        #expect(CRM.buttonState([crmImap], inbox, busy: false) == .hidden)
        #expect(CRM.buttonState(accs, CRM.Row(account: crmImap, role: .outbox), busy: true) == .hidden)
        let want: [CRM.ButtonState: CRM.Button] = [
            .hidden: CRM.Button(state: .hidden),
            .notSaved: CRM.Button(state: .notSaved, label: "Save to eWay-CRM", tooltip: "Save to eWay-CRM", enabled: true),
            .saving: CRM.Button(state: .saving, label: "Save to eWay-CRM", tooltip: "Saving to eWay-CRM…"),
            .saved: CRM.Button(state: .saved, label: "In eWay-CRM", tooltip: "Saved in eWay-CRM", enabled: true, menu: true),
        ]
        for (s, w) in want {
            #expect(CRM.buttonFor(s) == w, "\(s)")
        }
        let menu = CRM.savedMenu()
        #expect(menu.map(\.id) == [.open, .editLinks, .remove])
        #expect(menu[2].destructive && !menu[0].destructive)
        #expect(menu.map(\.label) == ["Open in eWay-CRM", "Edit Links…", "Remove from eWay-CRM"])
    }

    @Test func isSaved() {
        let s = MessageSummary(
            id: "x", accountId: "m1", folderId: "f", from: [], subject: "", date: .goZero, snippet: "", flags: [],
            hasAttachments: false, size: 0, crmSaved: true)
        #expect(CRM.isSaved(s, nil, crm: "e1"), "summary says saved")
        var plain = s
        plain.crmSaved = false
        var m = Message(summary: plain)
        #expect(!CRM.isSaved(s, m, crm: "e1"), "the loaded message has no record")
        m.crm = [CrmRecord(accountId: "e2", itemGuid: "a"), CrmRecord(accountId: "e1", itemGuid: "b")]
        #expect(CRM.isSaved(plain, m, crm: "e1"), "the loaded message has a record")
        #expect(CRM.recordOf(m, crm: "e1")?.itemGuid == "b")
        #expect(CRM.recordOf(m, crm: nil)?.itemGuid == "a")
        #expect(CRM.recordOf(m, crm: "")?.itemGuid == "a")
        #expect(CRM.recordOf(m, crm: "e9") == nil && CRM.recordOf(nil, crm: nil) == nil)
        var other = Message(summary: plain)
        other.summary.id = "y"
        #expect(CRM.isSaved(s, other, crm: "e1"), "another message's record decides")
    }

    @Test func inFlight() {
        var f = CRM.InFlight()
        #expect(!f.has("m1", "a") && !f.any("m1", ["a"]) && f.isEmpty)
        f.begin("m1", ["a", "b"])
        f.begin("m1", ["a"])
        #expect(f.has("m1", "a") && f.has("m1", "b") && !f.has("m2", "a") && f.any("m1", ["z", "b"]))
        f.end("m1", ["a", "b"])
        #expect(f.has("m1", "a") && !f.has("m1", "b"), "nested end")
        f.end("m1", ["a", "a"])
        #expect(!f.has("m1", "a") && f.isEmpty)
    }

    @Test func errorData() {
        // As the RPC client decodes it.
        let nd = RPCError(code: .partNotDownloaded, message: "x", data: .object([
            "messageIds": .array([.string("m_1"), .string("m_2"), .string("m_1"), .string("")]),
        ]))
        #expect(CRM.notDownloaded(nd) == ["m_1", "m_2"])
        let none: [(any Error)?] = [
            nil, RPCError(code: .partNotDownloaded, message: ""),
            RPCError(code: .serverError, message: "", data: nd.data),
            RPCError(code: .partNotDownloaded, message: "", data: .object(["messageIds": .array([])])),
        ]
        for e in none {
            #expect(CRM.notDownloaded(e) == nil, "\(String(describing: e))")
        }
        let ms = RPCError(code: .crmSignInUnsupported, message: "", data: .object(["reason": .string("microsoft")]))
        #expect(CRM.signInReason(ms) == .microsoft)
        let plan = RPCError(code: .crmSignInUnsupported, message: "", data: .object(["reason": .string("plan")]))
        #expect(CRM.signInReason(plan) == .plan)
        #expect(CRM.signInReason(RPCError(code: .crmSignInUnsupported, message: "", data: .object(["reason": .string("other")]))) == nil)
        #expect(CRM.signInReason(RPCError(code: .crmSignInUnsupported, message: "")) == nil)
    }

    @Test func urlsAndNames() {
        let open: [(String, String)] = [
            ("https://open.eway-crm.com/?ws=aA&l=bB", "https://open.eway-crm.com/?ws=aA&l=bB"),
            ("HTTPS://open.eway-crm.com/", "HTTPS://open.eway-crm.com/"),
            ("http://open.eway-crm.com/", ""),
            ("javascript:alert(1)", ""),
            ("https://user@open.eway-crm.com/", ""),
            ("https:///nohost", ""),
            ("", ""),
        ]
        for (u, want) in open {
            #expect(CRM.openURL(u) == want, "\(u)")
        }
        let names: [(String, String)] = [
            ("https://hosting.eway-crm.com/acme", "acme"),
            ("https://hosting.eway-crm.com/a/b%20c/", "b c"),
            ("https://CRM.Example.org", "crm.example.org"),
            ("https://crm.example.org/\u{202E}evil", "evil"),
            ("not a url", ""),
        ]
        for (u, want) in names {
            #expect(CRM.defaultName(u) == want, "\(u)")
        }
        #expect(CRM.serviceHost(crmEwayA) == "hosting.eway-crm.com/acme")
        #expect(CRM.serviceHost(crmImap) == "")
    }
}
