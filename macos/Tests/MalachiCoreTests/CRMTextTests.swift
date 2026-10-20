// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/crm text_test.go, ported (the English catalogue: every text
// is its msgid).

struct CRMTextTests {
    @Test func contextMenuLabel() {
        #expect(CRM.saveMenuLabel(1) == "Save to eWay-CRM…")
        #expect(CRM.saveMenuLabel(0) == "Save to eWay-CRM…")
        #expect(CRM.saveMenuLabel(3) == "Save 3 Messages to eWay-CRM…")
    }

    @Test func dialogTexts() {
        let s = CRM.dialogTexts(editing: false)
        #expect(s == CRM.DialogStrings(
            title: "Save to eWay-CRM", search: "Search eWay-CRM", superiorItem: "Superior Item", private: "Private",
            privateHint: "Only you will see this message in eWay-CRM.",
            empty: "Nothing in eWay-CRM matches these addresses.",
            partial: "eWay-CRM did not answer in time; some suggestions may be missing.",
            searchHint: "Deals and projects are added by searching above",
            save: "_Save"))
        #expect(CRM.dialogTexts(editing: true).title == "Edit Links")
        #expect(CRM.kinds.map(CRM.groupTitle).joined(separator: " · ") == "Contacts · Companies · Deals · Projects")
        #expect(CRM.groupTitle("lead") == "")
    }

    @Test func bar() throws {
        #expect(CRM.barFor(nil) == nil)
        var rec = CrmRecord(
            accountId: "e1", itemGuid: crmGUID(100), items: [
                CrmItem(kind: .deal, guid: crmGUID(5), name: "Fleet renewal", superior: true, url: "https://open.eway-crm.com/?l=5"),
                CrmItem(kind: .contact, guid: crmGUID(1), name: "Novák,\u{200B} Jan", url: "http://evil.example/"),
                CrmItem(kind: .company, guid: crmGUID(2), name: "Acme"),
                CrmItem(kind: .company, guid: crmGUID(3), name: " \u{202E} "),
                CrmItem(kind: "lead", guid: crmGUID(4), name: "unknown"),
            ], url: "https://open.eway-crm.com/?l=e")
        var b = try #require(CRM.barFor(rec))
        #expect(b == CRM.Bar(
            title: "Saved in eWay-CRM",
            items: [
                CRM.BarItem(kind: .contact, name: "Novák, Jan"),
                CRM.BarItem(kind: .company, name: "Acme"),
                CRM.BarItem(kind: .deal, name: "Fleet renewal", superior: true, url: "https://open.eway-crm.com/?l=5"),
            ],
            names: "Novák, Jan, Acme, Fleet renewal", url: "https://open.eway-crm.com/?l=e", openLabel: "Open in eWay-CRM"))
        rec.private = true
        rec.items = (0..<40).map { CrmItem(kind: .contact, guid: crmGUID($0), name: String(repeating: "n", count: 30)) }
        b = try #require(CRM.barFor(rec))
        #expect(b.note == "Only you will see this message in eWay-CRM.")
        #expect(b.names.utf8.count <= API.Limits.maxCrmDetailBytes && b.names.hasSuffix("…") && b.items.count == 40)
    }

    @Test func pill() {
        var s = MessageSummary(
            id: "x", accountId: "m1", folderId: "f", from: [], subject: "", date: .goZero, snippet: "", flags: [],
            hasAttachments: false, size: 0)
        #expect(CRM.pillFor(s) == nil)
        s.crmSaved = true
        #expect(CRM.pillFor(s) == CRM.Pill(label: "eWay", tooltip: "Saved in eWay-CRM"))
    }

    @Test func saveToast() {
        let dup = RPCError(code: .crmDuplicate, message: "")
        let net = RPCError(code: .networkError, message: "")
        func res(_ errs: [RPCError?]) -> CrmSaveResult {
            CrmSaveResult(results: errs.enumerated().map { i, e in
                CrmSaveOutcome(
                    messageId: MessageID(String(UnicodeScalar(UInt8(97 + i)))),
                    record: e == nil ? CrmRecord(accountId: "e1", itemGuid: "") : nil, error: e)
            })
        }
        let cases: [(String, CrmSaveResult, String?, RPCError?)] = [
            ("one", res([nil]), "Saved to eWay-CRM", nil),
            ("three", res([nil, nil, nil]), "3 messages saved to eWay-CRM", nil),
            ("one duplicate", res([dup]), "eWay-CRM already has this message.", nil),
            ("one network error", res([net]), nil, net),
            ("some failed", res([nil, dup, net]), "2 messages were not saved to eWay-CRM.", nil),
            ("one of two failed", res([nil, net]), "1 message was not saved to eWay-CRM.", nil),
        ]
        for (name, r, text, err) in cases {
            let got = CRM.saveToast(CRM.summarize(r))
            #expect(got.text == text && got.error == err, "\(name)")
        }
        #expect(CRM.summarize(res([nil, dup, net])) == CRM.SaveSummary(saved: 1, failed: 2, error: dup))
        #expect(CRM.removedToast() == "Removed from eWay-CRM")
    }

    @Test func removeQuestion() {
        #expect(CRM.removeQuestion("Offer\u{202E}  for 2027") == CRM.Question(
            heading: "Remove from eWay-CRM?",
            body: "“Offer for 2027” will be deleted from eWay-CRM with its links, for everyone who uses it. The message in Malachi Mail stays.",
            confirm: "_Remove"))
        #expect(CRM.removeQuestion(" ").body.hasPrefix("“(No subject)”"))
        #expect(CRM.removeQuestion(String(repeating: "s", count: 500)).body.utf8.count <= 400)
    }

    @Test func errorText() {
        let cases: [((any Error)?, String)] = [
            (RPCError(code: .crmSignInUnsupported, message: "", data: .object(["reason": .string("microsoft")])),
             "This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet."),
            (RPCError(code: .crmSignInUnsupported, message: "", data: .object(["reason": .string("plan")])),
             "This eWay-CRM does not allow access through its API."),
            (RPCError(code: .crmSignInUnsupported, message: ""), ""),
            (RPCError(code: .crmDuplicate, message: ""), "eWay-CRM already has this message."),
            (RPCError(code: .crmItemNotFound, message: ""), "eWay-CRM no longer has this item."),
            (RPCError(code: .partNotDownloaded, message: ""), ""),
            (RPCError(code: .authFailed, message: ""), ""),
            (CancellationError(), ""),
            (nil, ""),
        ]
        for (e, want) in cases {
            #expect(CRM.errorText(e) == want, "\(String(describing: e))")
        }
        let acts: [CRM.Action: String] = [
            .save: "Saving to eWay-CRM", .lookup: "Contacting eWay-CRM", .search: "Searching eWay-CRM",
            .remove: "Removing from eWay-CRM", .download: "Downloading the message",
        ]
        for (a, w) in acts {
            #expect(CRM.actionText(a) == w, "\(a)")
        }
        #expect(CRM.downloadingText(1) == "Downloading the message first…")
        #expect(CRM.downloadingText(4) == "Downloading 4 messages first…")
    }

    @Test func checkServiceInput() {
        let cases: [(String, Bool, String)] = [
            ("", false, ""),
            ("   ", false, ""),
            ("hosting.eway-crm.com/acme", true, ""),
            ("https://hosting.eway-crm.com/acme/API.svc", true, ""),
            ("HTTPS://crm.example.org", true, ""),
            ("http://crm.example.org", false, "Malachi Mail connects to eWay-CRM over https only."),
            ("HTTP://crm.example.org", false, "Malachi Mail connects to eWay-CRM over https only."),
            ("ftp://crm.example.org", false, "This is not a web address"),
            ("crm example.org", false, "This is not a web address"),
            ("https://user@crm.example.org", false, "This is not a web address"),
            ("https://", false, "This is not a web address"),
            ("crm.example.org/\u{202E}", false, "This is not a web address"),
        ]
        for (input, ok, problem) in cases {
            let got = CRM.checkServiceInput(input)
            #expect(got.ok == ok && got.problem == problem, "\(input)")
        }
    }

    @Test func failureOf() {
        let ms = RPCError(code: .crmSignInUnsupported, message: "", data: .object(["reason": .string("microsoft")]))
        let plan = RPCError(code: .crmSignInUnsupported, message: "", data: .object(["reason": .string("plan")]))
        let microsoft = "This eWay-CRM signs in with a Microsoft account, which Malachi Mail cannot do yet."
        let cases: [(String, CRM.WizardStep, (any Error)?, Bool, CRM.Failure)] = [
            ("microsoft at detect", .detect, ms, false, CRM.Failure(page: .service, banner: microsoft, what: "Contacting eWay-CRM", field: .service)),
            ("microsoft at add", .save, ms, false, CRM.Failure(page: .service, banner: microsoft, what: "Adding the account", field: .service)),
            ("microsoft when editing", .test, ms, true, CRM.Failure(page: .credentials, banner: microsoft, what: "Signing in")),
            ("plan at test", .test, plan, false,
             CRM.Failure(page: .credentials, banner: "This eWay-CRM does not allow access through its API.", what: "Signing in")),
            ("wrong password", .test, RPCError(code: .authFailed, message: ""), false,
             CRM.Failure(page: .credentials, banner: "The server rejected the user name or password", what: "Signing in", field: .password)),
            ("no password", .test, RPCError(code: .authRequired, message: ""), true,
             CRM.Failure(page: .credentials, banner: "Enter the password for this account", what: "Signing in", field: .password)),
            ("not an address", .detect, RPCError(code: .invalidArgument, message: ""), false,
             CRM.Failure(page: .service, banner: "This is not a web address", what: "Contacting eWay-CRM", field: .service)),
            ("not eWay-CRM", .detect, RPCError(code: .serverError, message: ""), false,
             CRM.Failure(page: .service, what: "Contacting eWay-CRM", field: .service)),
            ("offline at detect", .detect, RPCError(code: .networkError, message: ""), false,
             CRM.Failure(page: .service, what: "Contacting eWay-CRM")),
            ("conflict at add", .save, RPCError(code: .conflict, message: ""), false,
             CRM.Failure(page: .credentials, what: "Adding the account")),
            ("timeout at update", .save, CancellationError(), true, CRM.Failure(page: .credentials, what: "Saving the account")),
        ]
        for (name, step, err, editing, want) in cases {
            #expect(CRM.failureOf(step, err, editing: editing) == want, "\(name)")
        }
        let w = CRM.wizardTexts()
        #expect(w.title == "Add eWay-CRM Account" && w.serviceLabel == "Web Service Address" && w.userLabel == "User Name")
        #expect(w.passwordLabel == "Password" && w.contacting == "Contacting eWay-CRM" && w.save == "_Save")
    }
}
