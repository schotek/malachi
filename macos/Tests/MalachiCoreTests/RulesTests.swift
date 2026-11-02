// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/rules rules_test.go, ported (the English catalogue: every
/// text is its msgid). Invented addresses only.
struct RulesTests {
    @Test func operatorsFor() {
        let cases: [(RuleField, String)] = [
            (.from, "contains notContains is isNot startsWith endsWith domainIs"),
            (.toOrCc, "contains notContains is isNot startsWith endsWith domainIs"),
            (.subject, "contains notContains is isNot startsWith endsWith"),
            (.listId, "contains notContains is isNot startsWith endsWith"),
            (.bulk, "is isNot"),
            (.hasAttachments, "is"),
        ]
        for (f, want) in cases {
            #expect(Rules.operators(for: f).map(\.rawValue).joined(separator: " ") == want, "\(f)")
        }
    }

    @Test func newCondition() {
        #expect(Rules.newCondition(.from) == RuleCondition(field: "from", op: "contains"))
        var old = RuleCondition(field: .from, op: .domainIs, value: "acme.cz")
        // Subject has no domainIs: the first operator, the text kept.
        #expect(Rules.newCondition(.subject, old: old) == RuleCondition(field: "subject", op: "contains", value: "acme.cz"))
        old.op = .isNot
        #expect(Rules.newCondition(.bulk, old: old) == RuleCondition(field: "bulk", op: "isNot", value: "any"))
        let bulk = RuleCondition(field: .bulk, op: .is, value: "list")
        #expect(Rules.newCondition(.to, old: bulk) == RuleCondition(field: "to", op: "is"))
        #expect(Rules.newCondition(.hasAttachments).value == Rules.valueTrue)
    }

    private func valid() -> Rules.Form {
        var f = Rules.newForm("acc")
        f.name = " News "
        f.conditions[0].value = " acme.cz "
        f.actions = [RuleAction(kind: .move, folderId: "f_news", to: "stale"), RuleAction(kind: .markRead)]
        return f
    }

    @Test func input() {
        let input = valid().trimmed
        #expect(input.name == "News" && input.conditions[0].value == "acme.cz", "trimmed")
        #expect(input.actions[0] == RuleAction(kind: .move, folderId: "f_news"), "other fields cleared")
        var f = valid()
        f.actions = [RuleAction(kind: .autoReply, folderId: "x", subject: " s ", text: "Hi\n\n")]
        #expect(f.trimmed.actions[0] == RuleAction(kind: .autoReply, subject: "s", text: "Hi"))
        // The form itself is not touched.
        f = valid()
        _ = f.trimmed
        #expect(f.name == " News " && f.conditions[0].value == " acme.cz ")
    }

    private func kinds(_ ps: [Rules.Problem]) -> String {
        ps.map { "\($0.field):\(String(repeating: "i", count: $0.index)):\(Rules.problemText($0))" }.joined(separator: " | ")
    }

    @Test func validate() {
        #expect(Rules.validate(valid()).isEmpty, "\(kinds(Rules.validate(valid())))")
        typealias P = Rules.Problem
        let cases: [(String, (inout Rules.Form) -> Void, [P])] = [
            ("no name", { $0.name = "  " }, [P("name", 0, .nameMissing)]),
            ("long name", { $0.name = String(repeating: "x", count: API.Limits.maxRuleNameBytes + 1) }, [P("name", 0, .tooLong)]),
            ("bidi in name", { $0.name = "a\u{202E}b" }, [P("name", 0, .badCharacters)]),
            ("newline in value", { $0.conditions[0].value = "a\nb" }, [P("conditions", 0, .badCharacters)]),
            ("no condition", { $0.conditions = [] }, [P("conditions", 0, .noCondition)]),
            ("empty value", { $0.conditions.append(RuleCondition(field: .subject, op: .is, value: " ")) },
             [P("conditions", 1, .valueMissing)]),
            ("domain of a subject", { $0.conditions[0].field = .subject; $0.conditions[0].op = .domainIs },
             [P("conditions", 0, .badOperator)]),
            ("bad bulk", { $0.conditions[0] = RuleCondition(field: .bulk, op: .is, value: "spam") },
             [P("conditions", 0, .valueMissing)]),
            ("no action", { $0.actions = [] }, [P("actions", 0, .noAction)]),
            ("twice", { $0.actions.append(RuleAction(kind: .markRead)) }, [P("actions", 2, .actionTwice)]),
            ("two places", { $0.actions.append(RuleAction(kind: .trash)) }, [P("actions", 2, .onePlace)]),
            ("move for every account", { $0.accountId = nil }, [P("actions", 0, .moveNeedsAccount)]),
            ("move nowhere", { $0.actions[0].folderId = nil }, [P("actions", 0, .folderMissing)]),
            ("forward to a list", { $0.actions = [RuleAction(kind: .forward, to: "a@b.cz, c@d.cz")] },
             [P("actions", 0, .addressInvalid)]),
            ("empty reply", { $0.actions = [RuleAction(kind: .autoReply, text: " \n")] }, [P("actions", 0, .replyMissing)]),
            ("reply with tabs and lines", { $0.actions = [RuleAction(kind: .autoReply, text: "Hi\r\n\tthere")] }, []),
            ("reply with a bidi override", { $0.actions = [RuleAction(kind: .autoReply, text: "Hi \u{202E}")] },
             [P("actions", 0, .badCharacters)]),
        ]
        for (name, edit, want) in cases {
            var f = valid()
            edit(&f)
            let got = Rules.validate(f)
            #expect(got == want, "\(name): got \(kinds(got)), want \(kinds(want))")
        }
    }

    @Test func validAddress() {
        let cases: [String: Bool] = [
            "a@b.cz": true, " a.b+c@mail.example.com ": true,
            "": false, "a": false, "a@b": false, "@b.cz": false, "a@": false, "a@@b.cz": false,
            "A <a@b.cz>": false, "a@b..cz": false, "a@.b.cz": false, "a b@c.cz": false, "a@b.cz\u{202E}": false,
        ]
        for (s, want) in cases {
            #expect(Rules.validAddress(s) == want, "\(s)")
        }
    }

    @Test func availableActions() {
        func join(_ ks: [RuleActionKind]) -> String { ks.map(\.rawValue).joined(separator: " ") }
        var f = Rules.newForm("acc")
        #expect(join(Rules.availableActions(f)) == "markRead flag forward autoReply move junk trash", "new")
        f.accountId = nil
        #expect(join(Rules.availableActions(f)) == "markRead flag forward autoReply junk trash", "every account")
        f.actions = [RuleAction(kind: .junk), RuleAction(kind: .flag)]
        #expect(join(Rules.availableActions(f)) == "markRead forward autoReply", "placed")
    }

    private func message(_ from: [Address], bulk: BulkInfo? = nil) -> MessageSummary {
        MessageSummary(id: "m", accountId: "acc", folderId: "f", from: from, subject: "", date: .goZero, snippet: "",
                       flags: [], hasAttachments: false, size: 0, bulk: bulk)
    }

    @Test func fromMessage() {
        var m = message([Address(name: "Acme News", address: "news@acme.cz")])
        var f = Rules.fromMessage(m, accountId: "acc")
        #expect(f.name == "Messages from Acme News" && f.accountId == "acc" && f.enabled)
        #expect(f.conditions == [RuleCondition(field: .from, op: .is, value: "news@acme.cz")])
        #expect(f.actions == [RuleAction(kind: .move)])
        m.bulk = BulkInfo(kind: .list, listId: "dev.lists.acme.cz")
        f = Rules.fromMessage(m, accountId: "acc")
        #expect(f.conditions == [RuleCondition(field: .listId, op: .is, value: "dev.lists.acme.cz")], "list")
        // A name with a bidi override is not used; the address is.
        m = message([Address(name: "evil\u{202E}", address: "x@y.cz")])
        f = Rules.fromMessage(m, accountId: nil)
        #expect(f.name == "Messages from x@y.cz" && f.actions.isEmpty, "bidi name")
        // Without a sender: the empty From condition of a new rule.
        f = Rules.fromMessage(message([]), accountId: "acc")
        #expect(f.name == "" && f.conditions[0].value == "", "no sender")
        // A long name is cut on a scalar boundary.
        m = message([Address(name: String(repeating: "ž", count: 200), address: "a@b.cz")])
        f = Rules.fromMessage(m, accountId: "acc")
        #expect(f.name.utf8.count <= API.Limits.maxRuleNameBytes && Rules.clean(f.name), "long name")
    }

    @Test func needsConfirm() {
        let fwd = RuleInput(enabled: true, actions: [RuleAction(kind: .forward, to: "a@b.cz")])
        let local = RuleInput(enabled: true, actions: [RuleAction(kind: .markRead)])
        #expect(!Rules.needsConfirm(nil, local) && Rules.needsConfirm(nil, fwd), "new rule")
        var off = fwd
        off.enabled = false
        #expect(!Rules.needsConfirm(nil, off), "disabled")
        var stored = Rule(id: "r", input: fwd)
        #expect(!Rules.needsConfirm(stored, fwd), "unchanged")
        var renamed = fwd
        renamed.name = "x"
        #expect(!Rules.needsConfirm(stored, renamed), "renamed")
        let moved = RuleInput(enabled: true, actions: [RuleAction(kind: .forward, to: "c@d.cz")])
        #expect(Rules.needsConfirm(stored, moved), "other address")
        #expect(Rules.needsConfirm(Rule(id: "r", input: off), fwd), "turned on")
        #expect(Rules.needsConfirm(Rule(id: "r", input: local), fwd), "became outgoing")
        stored.input.enabled = false
        #expect(Rules.needsConfirm(stored, Rules.toggled(stored)), "switch on")
    }

    @Test func moved() {
        let ids = ["a", "b", "c", "d"]
        let cases: [(Int, Int, String)] = [
            (0, 3, "b c d a"), (3, 0, "d a b c"), (1, 2, "a c b d"), (2, 2, "a b c d"), (-1, 0, "a b c d"), (0, 4, "a b c d"),
        ]
        for (from, to, want) in cases {
            #expect(Rules.moved(ids, from: from, to: to).joined(separator: " ") == want, "\(from)→\(to)")
        }
    }

    private func folder(_ id: String, selectable: Bool = true, role: FolderRole = .none, virtual: VirtualFolder? = nil) -> Folder {
        Folder(id: FolderID(id), accountId: "acc", name: id, path: id, role: role, subscribed: true, selectable: selectable,
               synced: true, unread: 0, total: 0, virtual: virtual)
    }

    @Test func appliesAndOffers() {
        var rules = [Rule(id: "1", input: RuleInput(accountId: "a")), Rule(id: "2", input: RuleInput(enabled: true, accountId: "b"))]
        #expect(!Rules.appliesToAccount(rules, "a") && Rules.appliesToAccount(rules, "b"), "own rules")
        rules.append(Rule(id: "3", input: RuleInput(enabled: true)))
        #expect(Rules.appliesToAccount(rules, "a"), "rule for every account")
        #expect(Rules.canApplyIn(folder("in", role: .inbox)))
        #expect(!Rules.canApplyIn(folder("out", role: .outbox)))
        #expect(!Rules.canApplyIn(folder("box", selectable: false)))
        #expect(!Rules.canApplyIn(folder("v", virtual: "open")))
        #expect(Rules.canCreateFrom(.inbox) && Rules.canCreateFrom(.junk) && !Rules.canCreateFrom(.sent) && !Rules.canCreateFrom(.drafts))
        let folders = [
            folder("in", role: .inbox), folder("box", selectable: false),
            folder("out", role: .outbox), folder("all", role: .all),
            folder("news"), folder("v", virtual: "open"),
        ]
        #expect(Rules.moveFolders(folders).map(\.id.rawValue) == ["in", "news"])
    }

    @Test func summaries() {
        let names = Rules.Names(folder: { _, id in id == "f_news" ? "News" : "" })
        var input = RuleInput(
            match: .any,
            conditions: [RuleCondition(field: .from, op: .domainIs, value: "acme.cz"),
                         RuleCondition(field: .bulk, op: .is, value: "newsletter")],
            actions: [RuleAction(kind: .move, folderId: "f_news"), RuleAction(kind: .markRead)], stop: true)
        #expect(Rules.conditionsLine(input) == "From is from domain “acme.cz” or Bulk mail is newsletter")
        #expect(Rules.actionsLine(input, account: "acc", names: names) == "mark as read, move to “News”, stop processing more rules")
        input.actions[0].folderId = "gone"
        #expect(Rules.actionsLine(input, account: "acc", names: names).contains("move to a missing folder"))
        let at = RFC3339.parse("2026-11-02T08:00:00Z")!
        let r = Rule(id: "r_1", input: input, outgoing: true, problem: .folderGone, applied: 2, lastAppliedAt: at, skipped: 1)
        let rows = Rules.rows([r], names: names, date: { _ in "today" })
        #expect(rows.count == 1)
        #expect(rows.first?.status == "Used on 2 messages, last today · 1 outgoing message held back")
        #expect(rows.first?.scope == "All Mail Accounts" && rows.first?.problem.isEmpty == false && rows.first?.outgoing == true)
        #expect(Rules.scopeText("acc", names: Rules.Names(account: { _ in "" })) == "Removed account")
        let q = Rules.outgoingQuestion(RuleInput(actions: [RuleAction(kind: .forward, to: "boss@acme.cz")]))
        #expect(q.body.contains("boss@acme.cz") && q.confirm == "Turn On")
        #expect(Rules.applyResultText(RulesApplyResult(changed: 1)) == "The rules changed 1 message.")
    }
}
