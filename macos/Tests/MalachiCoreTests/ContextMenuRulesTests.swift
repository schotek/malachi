// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/contextmenu rules_test.go, ported: Create Rule from
/// Message… and Apply Rules….
struct ContextMenuRulesTests {
    private func show(_ gs: [[ContextMenu.Item]]) -> String {
        gs.map { g in g.map { $0.id.rawValue + ($0.enabled ? "" : "-") }.joined(separator: " ") }
            .joined(separator: " | ")
    }

    /// Create Rule from Message… is a group of its own before the
    /// Assistant, for one message outside the outbox and the drafts only.
    @Test func messageItemsRule() {
        var base = ContextMenu.MessageState()
        base.offerRule = true
        base.toggleFlag = true
        base.assistant = true
        #expect(show(ContextMenu.messageItems(base)) == "openMessage | toggleFlag | createRule | assistant", "one message")
        var several = base
        several.count = 2
        several.open = true
        #expect(show(ContextMenu.messageItems(several)) == "openMessage | toggleFlag | assistant", "several")
        var draft = base
        draft.draft = true
        #expect(show(ContextMenu.messageItems(draft)) == "editDraft | toggleFlag | assistant", "draft")
        var outbox = base
        outbox.outbox = true
        #expect(show(ContextMenu.messageItems(outbox)) == "openMessage | toggleFlag | trash- | assistant", "outbox")
        var off = base
        off.offerRule = false
        #expect(show(ContextMenu.messageItems(off)) == "openMessage | toggleFlag | assistant", "not offered")
    }

    /// Apply Rules… is in the acting group of a mail folder, disabled
    /// without a rule for the account or without messages.
    @Test func folderItemsRules() {
        let cases: [(String, ContextMenu.FolderState, String)] = [
            ("inbox with rules", .init(selectable: true, role: .inbox, total: 5, offerRules: true, rules: true),
             "checkMail | markAllRead- applyRules | favourite"),
            ("no rule for the account", .init(selectable: true, role: .inbox, total: 5, offerRules: true),
             "checkMail | markAllRead- applyRules- | favourite"),
            ("empty folder", .init(selectable: true, role: .none, offerRules: true, rules: true),
             "checkMail | markAllRead- applyRules- | favourite"),
            ("outbox", .init(selectable: true, role: .outbox, total: 1, offerRules: true, rules: true), "favourite"),
            ("drafts", .init(selectable: true, role: .drafts, total: 1, offerRules: true, rules: true),
             "checkMail | markAllRead- | favourite"),
            ("issue tracker", .init(selectable: true, role: .none, total: 1, rules: true),
             "checkMail | markAllRead- | favourite"),
        ]
        for (name, f, want) in cases {
            #expect(show(ContextMenu.folderItems(f)) == want, "\(name)")
        }
    }

    @Test func rulesLabels() {
        #expect(ContextMenu.label(.createRule) == "Create Rule from Message…")
        #expect(ContextMenu.label(.applyRules) == "Apply Rules…")
    }
}
