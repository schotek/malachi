// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/crm dialog_test.go, ported.

/// A GUID of the right shape for item n.
func crmGUID(_ n: Int) -> String {
    "00000000-0000-4000-8000-" + String(format: "%012d", n)
}

func crmItem(_ kind: CrmItemKind, _ n: Int, _ name: String, _ reason: CrmReason) -> CrmItem {
    CrmItem(kind: kind, guid: crmGUID(n), name: name, reason: reason, url: "https://open.eway-crm.com/?l=" + crmGUID(n))
}

func crmRef(_ kind: CrmItemKind, _ n: Int) -> CrmItemRef {
    CrmItemRef(kind: kind, guid: crmGUID(n))
}

struct CRMDialogTests {
    /// The groups as "kind: name[x][*] …", x checked, * superior.
    private func show(_ d: CRM.Dialog) -> String {
        d.groups.map { g in
            g.kind.rawValue + ": " + g.entries.map { e in
                e.item.name + (e.checked ? "x" : "") + (e.superior ? "*" : "")
            }.joined(separator: " ")
        }.joined(separator: " | ")
    }

    /// A lookup of one message that is not saved.
    private func lookup() -> CrmLookupResult {
        CrmLookupResult(
            accountId: "e1", saved: [CrmSavedMessage(messageId: "m_1")],
            suggestions: [
                crmItem(.deal, 5, "Fleet renewal", .contact),
                crmItem(.contact, 1, "Novák, Jan", .address),
                crmItem(.company, 2, "Acme", .domain),
                crmItem(.company, 3, "Acme Holding", .contact),
                crmItem(.project, 6, "Rollout", .company),
                // Invalid: an unknown kind, a GUID of another shape, a repeat.
                CrmItem(kind: "lead", guid: crmGUID(9), name: "x"),
                CrmItem(kind: .contact, guid: "not-a-guid", name: "y"),
                crmItem(.contact, 1, "Novák again", .address),
            ])
    }

    @Test func prefill() {
        var d = CRM.Dialog(lookup())
        #expect(!d.editing && !d.isEmpty && !d.partial && !d.isPrivate && d.accountID == "e1")
        #expect(show(d) == "contact: Novák, Janx | company: Acmex Acme Holding | deal: Fleet renewal | project: Rollout")
        #expect(d.superior == nil)
        let g = d.groups
        #expect(!g[0].entries[0].item.url.isEmpty && !g[0].entries[0].canBeSuperior && g[2].entries[0].canBeSuperior)

        // Exactly one open deal: the daemon flags it, the dialog checks it
        // as superior.
        var res = lookup()
        res.suggestions[0].superior = true
        res.partial = true
        d = CRM.Dialog(res)
        #expect(show(d).contains("deal: Fleet renewalx*") && d.partial)
        #expect(CRM.Dialog(CrmLookupResult(accountId: "e1")).isEmpty)
    }

    @Test func superior() {
        let d = CRM.Dialog(lookup())
        // The first deal or project checked becomes superior, another a link.
        #expect(d.toggle(crmRef(.project, 6), true) && d.toggle(crmRef(.deal, 5), true))
        #expect(show(d).contains("deal: Fleet renewalx |") && show(d).contains("project: Rolloutx*"))
        #expect(!d.toggle(crmRef(.deal, 5), true), "toggle to the same state changed something")
        // The marker moves.
        #expect(d.setSuperior(crmRef(.deal, 5)))
        #expect(show(d).contains("deal: Fleet renewalx*") && show(d).contains("project: Rolloutx"))
        // Unchecking the superior leaves none; the next one checked takes it.
        d.toggle(crmRef(.deal, 5), false)
        #expect(d.superior == nil)
        d.toggle(crmRef(.project, 6), false)
        d.toggle(crmRef(.project, 6), true)
        #expect(d.superior == crmRef(.project, 6))
        // setSuperior checks an unchecked one; contacts cannot be superior;
        // unknown items are ignored.
        d.toggle(crmRef(.deal, 5), false)
        #expect(d.setSuperior(crmRef(.deal, 5)) && d.isChecked(crmRef(.deal, 5)))
        #expect(!d.setSuperior(crmRef(.contact, 1)) && !d.setSuperior(crmRef(.deal, 77)) && !d.toggle(crmRef(.deal, 77), true))
        // Upper-case GUIDs name the same item.
        #expect(d.isChecked(CrmItemRef(kind: .deal, guid: crmGUID(5).uppercased())))
    }

    @Test func saveNew() throws {
        let d = CRM.Dialog(lookup())
        d.toggle(crmRef(.project, 6), true)
        d.toggle(crmRef(.deal, 5), true)
        d.setPrivate(true)
        #expect(d.canSave && d.changed, "a new save can always go")
        let p = try #require(d.saveParams(messageAccount: "m1", messageIDs: ["m_1", "m_2"]))
        #expect(p == CrmSaveParams(
            accountId: "e1", messageAccountId: "m1", messageIds: ["m_1", "m_2"],
            link: [crmRef(.contact, 1), crmRef(.company, 2), crmRef(.deal, 5)],
            superior: crmRef(.project, 6), private: true))
        // Nothing checked: the message is saved without links.
        let empty = CRM.Dialog(CrmLookupResult(accountId: "e1", saved: [CrmSavedMessage(messageId: "m_1")]))
        let e = try #require(empty.saveParams(messageAccount: "m1", messageIDs: ["m_1"]))
        #expect(e.link == nil && e.superior == nil && e.private == nil && empty.canSave)
        for ids in [[MessageID](), Array(repeating: MessageID("m"), count: API.Limits.maxCrmMessages + 1)] {
            #expect(d.saveParams(messageAccount: "m1", messageIDs: ids) == nil, "\(ids.count) messages")
        }
    }

    /// One saved message: the record checked, the result a diff.
    private func savedLookup() -> CrmLookupResult {
        var res = lookup()
        res.saved[0].record = CrmRecord(
            accountId: "e1", itemGuid: crmGUID(100), private: true, items: [
                crmItem(.contact, 1, "Novák, Jan", .saved),
                crmItem(.company, 3, "Acme Holding", .saved),
                CrmItem(kind: .deal, guid: crmGUID(5), name: "Fleet renewal", reason: .saved, superior: true),
            ], url: "https://open.eway-crm.com/?l=e")
        return res
    }

    @Test func edit() throws {
        var d = CRM.Dialog(savedLookup())
        try #require(d.editing && d.isPrivate)
        #expect(show(d) == "contact: Novák, Janx | company: Acme Holdingx Acme | deal: Fleet renewalx* | project: Rollout")
        #expect(!d.changed && !d.canSave, "nothing changed yet")
        d.setPrivate(false)
        #expect(d.canSave, "privacy changed")
        var p = try #require(d.saveParams(messageAccount: "m1", messageIDs: ["m_1"]))
        #expect(p.link == nil && p.unlink == nil && p.superior == nil && p.private == nil)
        d.setPrivate(true)

        // Uncheck a company, check another, make the project superior while
        // the old deal stays checked (as a plain link).
        d.toggle(crmRef(.company, 3), false)
        d.toggle(crmRef(.company, 2), true)
        d.setSuperior(crmRef(.project, 6))
        p = try #require(d.saveParams(messageAccount: "m1", messageIDs: ["m_1"]))
        #expect(p == CrmSaveParams(
            accountId: "e1", messageAccountId: "m1", messageIds: ["m_1"],
            link: [crmRef(.company, 2), crmRef(.deal, 5)], unlink: [crmRef(.company, 3)],
            superior: crmRef(.project, 6), private: true))
        // Unchecking the recorded superior unlinks it.
        d = CRM.Dialog(savedLookup())
        d.toggle(crmRef(.deal, 5), false)
        p = try #require(d.saveParams(messageAccount: "m1", messageIDs: ["m_1"]))
        #expect(p.unlink == [crmRef(.deal, 5)] && p.superior == nil && p.link == nil)
        // Unchecked and checked again: no change.
        d.toggle(crmRef(.deal, 5), true)
        #expect(!d.changed, "rechecked superior is a change: \(show(d))")
        // Editing saves exactly one message.
        #expect(d.saveParams(messageAccount: "m1", messageIDs: ["m_1", "m_2"]) == nil)
    }

    @Test func merge() {
        let d = CRM.Dialog(lookup())
        d.toggle(crmRef(.deal, 5), true)
        let n = d.merge(CrmSearchResult(items: [
            crmItem(.deal, 5, "Fleet renewal (search)", .search),
            CrmItem(kind: .project, guid: crmGUID(7), name: "Pilot", reason: .search, superior: true),
            crmItem(.contact, 8, "Dvořák, Petr", .search),
            CrmItem(kind: .contact, guid: "bad", name: "z"),
        ]))
        #expect(n == 2)
        #expect(show(d) == "contact: Novák, Janx Dvořák, Petr | company: Acmex Acme Holding | deal: Fleet renewalx* | project: Rollout Pilot")
        let p = d.searchParams("  ac  ")
        #expect(p?.query == "ac" && p?.accountId == "e1")
        for q in ["", " a ", String(repeating: "x", count: API.Limits.maxCrmQueryBytes + 1)] {
            #expect(d.searchParams(q) == nil, "\(q)")
        }
    }

    /// Hostile names are cleaned and capped; a URL that is not https dropped.
    @Test func cleansItems() {
        let res = CrmLookupResult(accountId: "e1", suggestions: [CrmItem(
            kind: .contact, guid: crmGUID(1).uppercased(),
            name: "Eve\u{202E}\u{0} \n Evil" + String(repeating: "ž", count: 400), detail: "a\tb", reason: .address,
            url: "javascript:alert(1)")])
        let e = CRM.Dialog(res).groups[0].entries[0]
        #expect(e.item.name.hasPrefix("Eve Evil") && e.item.name.utf8.count <= API.Limits.maxCrmNameBytes && e.item.name.hasSuffix("…"))
        #expect(e.item.detail == "a b" && e.item.url.isEmpty && e.item.guid == crmGUID(1))
    }

    /// A save names at most 50 items: the check over it is refused.
    @Test func limit() {
        var res = CrmLookupResult(accountId: "e1", saved: [CrmSavedMessage(messageId: "m_1")])
        for i in 0...API.Limits.maxCrmItems {
            res.suggestions.append(crmItem(.contact, i, "c\(i)", .contact))
        }
        let d = CRM.Dialog(res)
        for i in 0..<API.Limits.maxCrmItems {
            #expect(d.toggle(crmRef(.contact, i), true), "check \(i)")
        }
        #expect(!d.toggle(crmRef(.contact, API.Limits.maxCrmItems), true) && !d.isChecked(crmRef(.contact, API.Limits.maxCrmItems)))
        let p = d.saveParams(messageAccount: "m1", messageIDs: ["m_1"])
        #expect(p?.link?.count == API.Limits.maxCrmItems && d.canSave)
    }

    /// The quiet line under the groups: shown with contacts or companies
    /// listed but no deal or project, gone once a search adds one, never on
    /// an empty dialog (owner, 2026-10-17).
    @Test func searchHint() {
        var res = lookup()
        res.suggestions = res.suggestions.filter { !CRM.canBeSuperior($0.kind) }
        var d = CRM.Dialog(res)
        #expect(d.offersSearchHint)
        _ = d.merge(CrmSearchResult(items: [crmItem(.deal, 9, "Found deal", .search)]))
        #expect(!d.offersSearchHint)
        #expect(!CRM.Dialog(CrmLookupResult(accountId: "e1", saved: [], suggestions: [])).offersSearchHint)
        #expect(!CRM.Dialog(lookup()).offersSearchHint)
    }
}
