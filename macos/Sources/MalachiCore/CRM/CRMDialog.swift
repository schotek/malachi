// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// ui/internal/crm dialog.go: the model of the Save to eWay-CRM dialog.

extension CRM {
    /// crm.Kinds: the kinds in the order the dialog groups them.
    public static let kinds: [CrmItemKind] = [.contact, .company, .deal, .project]

    /// crm.CanBeSuperior: a deal or a project (eWay-CRM's OUTLOOKPROJECT).
    public static func canBeSuperior(_ k: CrmItemKind) -> Bool {
        k == .deal || k == .project
    }

    /// The shape of an eWay-CRM GUID, lower case (eway.ValidGUID).
    static func isGUID(_ s: String) -> Bool {
        let b = Array(s.utf8)
        guard b.count == 36 else { return false }
        for (i, c) in b.enumerated() {
            switch i {
            case 8, 13, 18, 23:
                guard c == UInt8(ascii: "-") else { return false }
            default:
                let digit = c >= UInt8(ascii: "0") && c <= UInt8(ascii: "9")
                let hex = c >= UInt8(ascii: "a") && c <= UInt8(ascii: "f")
                guard digit || hex else { return false }
            }
        }
        return true
    }

    /// refOf: the item's key (its kind and lower-case GUID); nil for an
    /// unknown kind or a GUID of another shape.
    static func refOf(_ kind: CrmItemKind, _ guid: String) -> CrmItemRef? {
        let g = Jira.trimSpace(guid).lowercased()
        guard kinds.contains(kind), isGUID(g) else { return nil }
        return CrmItemRef(kind: kind, guid: g)
    }

    /// cleanItem: the display texts cleaned and capped again, the URL
    /// dropped unless https.
    static func cleanItem(_ it: CrmItem, _ ref: CrmItemRef) -> CrmItem {
        let detail = capBytes(Jira.clean(it.detail ?? ""), API.Limits.maxCrmDetailBytes)
        return CrmItem(
            kind: ref.kind, guid: ref.guid, name: capBytes(Jira.clean(it.name), API.Limits.maxCrmNameBytes),
            detail: detail, reason: it.reason, superior: it.superior, url: openURL(it.url))
    }

    /// crm.DialogEntry: a row of the dialog.
    public struct DialogEntry: Sendable, Equatable {
        public var item: CrmItem
        public var checked: Bool
        public var superior: Bool

        public var ref: CrmItemRef { CrmItemRef(kind: item.kind, guid: item.guid) }
        public var canBeSuperior: Bool { CRM.canBeSuperior(item.kind) }
    }

    /// crm.DialogGroup: the entries of one kind.
    public struct DialogGroup: Sendable, Equatable {
        public var kind: CrmItemKind
        public var entries: [DialogEntry]
    }

    /// crm.Dialog: the model of the dialog over the answer of crm.lookup
    /// (and Edit Links of one saved message), with search results merged
    /// in. Prefill: contacts and companies found by an address or a
    /// tracked domain are checked, the one deal or project the daemon flags
    /// superior is checked as the superior item; over one saved message the
    /// record's items are checked instead. At most one superior item: the
    /// first deal or project checked while none is takes it, `setSuperior`
    /// moves it, unchecking it leaves none.
    public final class Dialog {
        public let accountID: AccountID
        public let messages: [MessageID]
        public let editing: Bool
        public let partial: Bool

        private var items: [CrmItemRef: CrmItem] = [:]
        private var order: [CrmItemKind: [CrmItemRef]] = [:]
        private var checked: Set<CrmItemRef> = []
        private var superiorRef: CrmItemRef?
        public var isPrivate: Bool

        private var recorded: Set<CrmItemRef> = []
        private var recordedSuperior: CrmItemRef?
        private let recordedPrivate: Bool

        /// crm.NewDialog: it edits when `res` is about one saved message.
        public init(_ res: CrmLookupResult) {
            accountID = res.accountId
            partial = res.partial
            messages = res.saved.map(\.messageId)
            let record = res.saved.count == 1 ? res.saved[0].record : nil
            editing = record != nil
            isPrivate = record?.private ?? false
            recordedPrivate = isPrivate
            if let record {
                for it in record.items {
                    guard let ref = add(it) else { continue }
                    checked.insert(ref)
                    recorded.insert(ref)
                    if it.superior, CRM.canBeSuperior(ref.kind), superiorRef == nil {
                        superiorRef = ref
                        recordedSuperior = ref
                    }
                }
            }
            for it in res.suggestions {
                guard let ref = add(it), !editing else { continue }
                if ref.kind == .contact || ref.kind == .company, it.reason == .address || it.reason == .domain {
                    checked.insert(ref)
                } else if it.superior, CRM.canBeSuperior(ref.kind), superiorRef == nil {
                    checked.insert(ref)
                    superiorRef = ref
                }
            }
        }

        /// Lists `it` unless listed already or invalid; its ref when valid.
        @discardableResult
        private func add(_ it: CrmItem) -> CrmItemRef? {
            guard let ref = CRM.refOf(it.kind, it.guid) else { return nil }
            if items[ref] == nil {
                items[ref] = CRM.cleanItem(it, ref)
                order[ref.kind, default: []].append(ref)
            }
            return ref
        }

        /// Nothing is listed (the dialog shows `DialogStrings.empty`).
        public var isEmpty: Bool { items.isEmpty }

        /// crm.Dialog.OffersSearchHint: something is listed, but no deal or
        /// project — the quiet line says they are found by searching.
        public var offersSearchHint: Bool {
            !items.isEmpty && !items.keys.contains { CRM.canBeSuperior($0.kind) }
        }

        /// crm.Dialog.SetPrivate.
        public func setPrivate(_ on: Bool) {
            isPrivate = on
        }

        /// crm.Dialog.Superior: nil when there is none.
        public var superior: CrmItemRef? { superiorRef }

        /// crm.Dialog.Checked.
        public func isChecked(_ ref: CrmItemRef) -> Bool {
            guard let r = CRM.refOf(ref.kind, ref.guid) else { return false }
            return checked.contains(r)
        }

        /// crm.Dialog.Groups: by kind in `kinds`' order, each in the order
        /// listed; a kind without items is left out.
        public var groups: [DialogGroup] {
            CRM.kinds.compactMap { k in
                guard let refs = order[k], !refs.isEmpty else { return nil }
                return DialogGroup(kind: k, entries: refs.map { ref in
                    DialogEntry(item: items[ref]!, checked: checked.contains(ref), superior: ref == superiorRef)
                })
            }
        }

        /// crm.Dialog.Toggle: whether anything changed. A check over
        /// `API.Limits.maxCrmItems` items to change is refused.
        @discardableResult
        public func toggle(_ ref: CrmItemRef, _ on: Bool) -> Bool {
            guard let r = CRM.refOf(ref.kind, ref.guid), items[r] != nil, checked.contains(r) != on else { return false }
            if !on {
                checked.remove(r)
                if superiorRef == r {
                    superiorRef = nil
                }
                return true
            }
            let prev = superiorRef
            checked.insert(r)
            if CRM.canBeSuperior(r.kind), superiorRef == nil {
                superiorRef = r
            }
            if changeCount > API.Limits.maxCrmItems {
                checked.remove(r)
                superiorRef = prev
                return false
            }
            return true
        }

        /// crm.Dialog.SetSuperior: checks it when it is not.
        @discardableResult
        public func setSuperior(_ ref: CrmItemRef) -> Bool {
            guard let r = CRM.refOf(ref.kind, ref.guid), items[r] != nil, CRM.canBeSuperior(r.kind), superiorRef != r else {
                return false
            }
            if !checked.contains(r), !toggle(r, true) {
                return false
            }
            let prev = superiorRef
            superiorRef = r
            if changeCount > API.Limits.maxCrmItems {
                superiorRef = prev
                return false
            }
            return true
        }

        /// crm.Dialog.Merge: the items of a crm.search answer not listed
        /// yet, unchecked, after the others of their kind; how many.
        @discardableResult
        public func merge(_ res: CrmSearchResult) -> Int {
            var n = 0
            for var it in res.items {
                guard let ref = CRM.refOf(it.kind, it.guid), items[ref] == nil else { continue }
                it.superior = false
                add(it)
                n += 1
            }
            return n
        }

        /// What the save changes, in the dialog's order.
        private func diff() -> (link: [CrmItemRef], unlink: [CrmItemRef], superior: CrmItemRef?) {
            var link: [CrmItemRef] = []
            var unlink: [CrmItemRef] = []
            for k in CRM.kinds {
                for ref in order[k] ?? [] {
                    let cur = checked.contains(ref)
                    let was = recorded.contains(ref)
                    if cur, !was, ref != superiorRef {
                        link.append(ref)
                    } else if !cur, was {
                        unlink.append(ref)
                    }
                }
            }
            var sup: CrmItemRef?
            if let s = superiorRef, s != recordedSuperior {
                sup = s
                // The superior item it replaces stays a plain link while
                // it stays checked.
                if let old = recordedSuperior, checked.contains(old) {
                    link.append(old)
                }
            }
            return (link, unlink, sup)
        }

        private var changeCount: Int {
            let d = diff()
            return d.link.count + d.unlink.count + (d.superior == nil ? 0 : 1)
        }

        /// crm.Dialog.Changed: a new save always; an edit when the links
        /// or Private differ from the record.
        public var changed: Bool {
            !editing || changeCount > 0 || isPrivate != recordedPrivate
        }

        /// crm.Dialog.CanSave: the Save button.
        public var canSave: Bool {
            changed && changeCount <= API.Limits.maxCrmItems
        }

        /// crm.Dialog.SaveParams: nil when the call would break a limit of
        /// docs/api.md §4.17 (1 to 20 messages, exactly one when editing;
        /// at most 50 items).
        public func saveParams(messageAccount: AccountID, messageIDs: [MessageID]) -> CrmSaveParams? {
            let n = messageIDs.count
            guard n > 0, n <= API.Limits.maxCrmMessages, !editing || n == 1 else { return nil }
            var d = diff()
            guard d.link.count + d.unlink.count + (d.superior == nil ? 0 : 1) <= API.Limits.maxCrmItems else { return nil }
            if !editing {
                d.unlink = []
            }
            return CrmSaveParams(
                accountId: accountID, messageAccountId: messageAccount, messageIds: messageIDs,
                link: d.link.isEmpty ? nil : d.link, unlink: d.unlink.isEmpty ? nil : d.unlink,
                superior: d.superior, private: isPrivate ? true : nil)
        }

        /// crm.Dialog.SearchParams: nil while the trimmed query is shorter
        /// than 2 bytes or longer than 200.
        public func searchParams(_ raw: String) -> CrmSearchParams? {
            let q = Jira.trimSpace(raw)
            let n = q.utf8.count
            guard n >= API.Limits.minCrmQueryBytes, n <= API.Limits.maxCrmQueryBytes else { return nil }
            return CrmSearchParams(accountId: accountID, query: q)
        }
    }
}
