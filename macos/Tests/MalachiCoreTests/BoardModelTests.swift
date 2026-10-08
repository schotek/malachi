// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The board's model (MalachiCore/Board): states, the view model, the
// selection rules, the cleaning of hostile strings and the invented
// samples. Pure and deterministic: a fixed `now`, a UTC Gregorian calendar
// and en_US_POSIX. The catalogue is English in tests, so msgids come back
// verbatim. Swift-first; the GTK port brings ui/internal/board/*_test.go of
// the same shape.

/// Fixtures shared with BoardControllerTests.
enum BoardFixture {
    static let calendar: Calendar = {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = TimeZone(identifier: "UTC")!
        c.locale = Locale(identifier: "en_US_POSIX")
        return c
    }()

    /// A day of October 2026 (the 15th is a Thursday).
    static func day(_ d: Int, _ h: Int = 12, _ m: Int = 0) -> Date {
        calendar.date(from: DateComponents(year: 2026, month: 10, day: d, hour: h, minute: m))!
    }

    static let now = day(15, 12)

    static func ago(_ hours: Double) -> Date {
        now.addingTimeInterval(-hours * 3600)
    }

    static let accountA = AccountID(rawValue: "a")
    static let accountB = AccountID(rawValue: "b")

    static let accounts = [
        Board.AccountInfo(id: accountA, name: "Alpha", badge: "IMAP"),
        Board.AccountInfo(id: accountB, name: "Beta", badge: "JIRA"),
    ]

    static func id(_ s: String) -> Board.CaseID { Board.CaseID(rawValue: s) }

    static func mk(
        _ n: String, _ state: Board.State = .you, account: AccountID = accountA, hours: Double = 1,
        subject: String? = nil, snippet: String = "", count: Int = 1, annotation: Board.Annotation? = nil,
        user: Board.State? = nil, done: Bool = false, messages: [Board.CaseMessage]? = [],
        issue: Board.IssueInfo? = nil, visibility: Board.Visibility? = nil, draft: String? = nil,
        version: Int64 = 0
    ) -> Board.Case {
        Board.Case(
            id: id(n), account: account, person: "P \(n)", date: ago(hours), subject: subject ?? "Subject \(n)",
            snippet: snippet, messageCount: count, issue: issue, ruleState: state,
            ruleReason: BoardReason(rawValue: "rule \(n)"), annotation: annotation, userState: user,
            visibility: visibility ?? (done ? .done(at: nil) : .live),
            draft: draft.map { Board.DraftLink(id: DraftID(rawValue: "d_\(n)"), text: $0) }, messages: messages,
            version: version)
    }

    static func view(
        _ cases: [Board.Case], annotated: Bool = false, commitments: [Board.Commitment] = [],
        configure: (inout Board.ViewState) -> Void = { _ in }
    ) -> Board.View {
        var v = Board.ViewState()
        configure(&v)
        let s = Board.Snapshot(
            accounts: accounts, cases: cases, commitments: commitments, annotated: annotated,
            run: Board.Run(model: "M", date: now))
        return Board.view(s, v, now: now, calendar: calendar)
    }

    static func ids(_ rows: [Board.Row]) -> [String] { rows.map(\.id.rawValue) }
}

private typealias F = BoardFixture

@Suite struct BoardStateTests {
    @Test func stateOf() {
        let ann = Board.Annotation(state: .hot, title: "t")
        // (user, annotation, annotated, want)
        let cases: [(Board.State?, Board.State?, Bool, Board.State)] = [
            (nil, nil, false, .info),
            (nil, nil, true, .info),
            (nil, .hot, true, .hot),
            (nil, .info, true, .info),  // the annotation agrees with the rules
            (nil, .hot, false, .info),  // annotations switched off
            (.them, nil, true, .them),
            (.them, .hot, true, .them),  // the user wins over the assistant
            (.them, .hot, false, .them),
            (.info, .info, true, .info),
        ]
        for (user, annotation, annotated, want) in cases {
            var c = F.mk("x", .info, user: user)
            c.annotation = annotation.map { Board.Annotation(state: $0, title: ann.title) }
            #expect(Board.state(of: c, annotated: annotated) == want, "\(String(describing: user)) \(String(describing: annotation)) \(annotated)")
        }
    }

    @Test func stateSource() {
        let cases: [(Board.State?, Board.State?, Bool, Board.StateSource)] = [
            (nil, nil, false, .assistantOff),
            (nil, .hot, false, .assistantOff),
            (nil, nil, true, .rules),
            (nil, .you, true, .assistantKept),
            (nil, .hot, true, .assistantChanged(from: .you)),
            (.them, nil, true, .user),
            (.them, .hot, true, .user),
            (.them, .hot, false, .user),
            (.them, nil, false, .user),
        ]
        for (user, annotation, annotated, want) in cases {
            var c = F.mk("x", .you, user: user)
            c.annotation = annotation.map { Board.Annotation(state: $0, title: "t") }
            #expect(
                Board.stateSource(of: c, annotated: annotated) == want,
                "\(String(describing: user)) \(String(describing: annotation)) \(annotated)")
        }
    }
}

@Suite struct BoardViewTests {
    // MARK: Ordering and sections

    @Test func orderIsStateThenNewestThenID() {
        let cases = [
            F.mk("c1", .you, hours: 5), F.mk("c2", .hot, hours: 9), F.mk("c3", .you, hours: 1),
            F.mk("c4", .info, hours: 3), F.mk("c6", .them, hours: 2), F.mk("c5", .them, hours: 2),
        ]
        let v = F.view(cases)
        #expect(v.sections.map(\.kind) == [.state(.hot), .state(.you), .state(.them), .state(.info)])
        #expect(v.sections.map { F.ids($0.rows) } == [["c2"], ["c3", "c1"], ["c5", "c6"], ["c4"]])
        #expect(v.sections.map(\.title) == ["Hot", "Waiting for You", "Waiting for Them", "For Your Information"])
    }

    @Test func sectionsPerFilter() {
        let cases = [
            F.mk("c1", .you), F.mk("c2", .hot), F.mk("c3", .info, hours: 2, done: true),
            F.mk("c4", .them, hours: 4, done: true), F.mk("c5", .them, hours: 3),
        ]
        let all = F.view(cases)
        #expect(all.sections.map(\.kind) == [.state(.hot), .state(.you), .state(.them)])  // info is done: no empty section
        let you = F.view(cases) { $0.filter = .state(.you) }
        #expect(you.sections.map(\.kind) == [.state(.you)])
        #expect(F.ids(you.sections[0].rows) == ["c1"])
        let none = F.view(cases) { $0.filter = .state(.info) }
        #expect(none.sections.isEmpty)
        #expect(none.sectionsEmptyText == "Nothing here.")
        let done = F.view(cases) { $0.filter = .done }
        #expect(done.sections.map(\.kind) == [.done])
        #expect(done.sections[0].title == "Done")
        #expect(F.ids(done.sections[0].rows) == ["c3", "c4"])  // newest first
        let emptyDone = F.view([F.mk("c1")]) { $0.filter = .done }
        #expect(emptyDone.sections.isEmpty)
    }

    @Test func annotationDecidesTheSectionOnlyWhenAnnotated() {
        let ann = Board.Annotation(state: .hot, title: "Burning")
        let cases = [F.mk("c1", .info, annotation: ann)]
        #expect(F.view(cases, annotated: true).sections.map(\.kind) == [.state(.hot)])
        #expect(F.view(cases, annotated: false).sections.map(\.kind) == [.state(.info)])
    }

    @Test func columnsAreAlwaysFourWhateverTheFilter() {
        let cases = [F.mk("c1", .you), F.mk("c2", .hot, done: true)]
        for filter: Board.Filter in [.all, .state(.hot), .done] {
            let v = F.view(cases) { $0.filter = filter; $0.style = .columns }
            #expect(v.columns.map(\.state) == Board.State.allCases)
            #expect(v.columns.map { F.ids($0.rows) } == [[], ["c1"], [], []])
            #expect(v.columns.map(\.emptyText) == ["Nothing burning.", "Empty.", "Empty.", "Empty."])
            #expect(v.columns.map(\.title) == Board.State.allCases.map(Board.Text.stateName))
        }
    }

    // MARK: Scope, counts

    @Test func accountScopeAndCounts() {
        let cases = [
            F.mk("c1", .hot), F.mk("c2", .you, account: F.accountB), F.mk("c3", .you, account: F.accountB, hours: 2),
            F.mk("c4", .info, account: F.accountB, done: true), F.mk("c5", .them, done: true),
        ]
        let all = F.view(cases)
        #expect(all.nav.map(\.count) == [3, 1, 2, 0, 0, 0, 2])
        #expect(all.nav.map(\.title) == ["Overview", "Hot", "Waiting for You", "Waiting for Them", "For Your Information", "Snoozed", "Done"])
        #expect(all.nav.map(\.dot) == [nil, .hot, .you, .them, .info, nil, nil])
        #expect(all.nav.map(\.selected) == [true, false, false, false, false, false, false])
        #expect(all.accounts.map(\.title) == ["All Accounts", "Alpha", "Beta"])
        #expect(all.accounts.map(\.badge) == ["", "IMAP", "JIRA"])
        #expect(all.accounts.map(\.count) == [3, 1, 2])
        #expect(all.accounts.map(\.selected) == [true, false, false])
        #expect(all.subtitle == "All Accounts · 3 cases")

        // The account filter narrows the cases; the account list keeps its own counts.
        let b = F.view(cases) { $0.account = .account(F.accountB); $0.filter = .done }
        #expect(b.nav.map(\.count) == [2, 0, 2, 0, 0, 0, 1])
        #expect(b.nav.map(\.selected) == [false, false, false, false, false, false, true])
        #expect(b.accounts.map(\.count) == [3, 1, 2])
        #expect(b.accounts.map(\.selected) == [false, false, true])
        #expect(b.accountTitle == "Beta")
        #expect(b.subtitle == "Beta · 2 cases")
        #expect(F.ids(b.columns[1].rows) == ["c2", "c3"])
        #expect(F.ids(b.sections[0].rows) == ["c4"])
        #expect(b.today.tiles.map(\.count)[..<4] == [0, 2, 0, 0])
    }

    @Test func isEmpty() {
        #expect(F.view([]).isEmpty)
        #expect(!F.view([F.mk("c1")]).isEmpty)
        // Done cases still count: the board is not empty, the list is.
        #expect(!F.view([F.mk("c1", done: true)]).isEmpty)
        // In the account scope.
        #expect(F.view([F.mk("c1")]) { $0.account = .account(F.accountB) }.isEmpty)
        #expect(!F.view([F.mk("c1", done: true)]) { $0.account = .account(F.accountA) }.isEmpty)
        // Whatever the filter.
        #expect(!F.view([F.mk("c1")]) { $0.filter = .done }.isEmpty)
    }

    @Test func pluralTexts() {
        #expect(F.view([]).subtitle == "All Accounts · 0 cases")
        #expect(F.view([F.mk("c1")]).subtitle == "All Accounts · 1 case")
        #expect(F.view([F.mk("c1"), F.mk("c2")]).subtitle == "All Accounts · 2 cases")
        #expect(Board.Text.messageCount(1) == "1 message")
        #expect(Board.Text.messageCount(2) == "2 messages")
        #expect(Board.Text.messageCount(0) == "0 messages")
        let d = F.view([F.mk("c1", count: 1), F.mk("c2", hours: 2, count: 7)]) { $0.selection = F.id("c2") }.detail
        #expect(d?.conversationTitle == "Conversation · 7 messages")
        let one = F.view([F.mk("c1", count: 1)]).detail
        #expect(one?.conversationTitle == "Conversation · 1 message")
        // A count below one still means one message.
        #expect(F.view([F.mk("c1", count: 0)]).detail?.conversationTitle == "Conversation · 1 message")
        #expect(Board.Text.todoPhrase(0) == "Nothing needs you today.")
        #expect(Board.Text.todoPhrase(1) == "1 thing needs you today.")
        #expect(Board.Text.todoPhrase(2) == "2 things need you today.")
        #expect(Board.Text.andMore(2) == "and 2 more")
    }

    // MARK: Rows

    @Test func rowFields() {
        let c = Board.Case(
            id: F.id("c1"), account: F.accountB, person: "Ada", date: F.ago(2), subject: "DEMO-1: Subject",
            snippet: "snip", unread: true, hasAttachments: true, messageCount: 3,
            issue: Board.IssueInfo(key: "DEMO-1", status: "To Do", style: .todo), ruleState: .you)
        let r = F.view([c]).sections[0].rows[0]
        #expect(r.id == F.id("c1") && r.state == .you && r.person == "Ada")
        #expect(r.title == "DEMO-1: Subject" && r.snippet == "snip" && r.account == "Beta")
        #expect(r.issueKey == "DEMO-1" && r.issueStatus == "To Do" && r.issueStyle == .todo)
        #expect(r.attachments && r.unread && r.countText == "3" && r.due == "")
        #expect(!r.time.isEmpty)
        #expect(r.spoken == "Waiting for You. Ada. DEMO-1: Subject. DEMO-1, To Do. 3 messages. Has attachments. Unread.")
        let plain = F.view([F.mk("c2")]).sections[0].rows[0]
        #expect(plain.issueKey == "" && plain.issueStyle == .plain && plain.countText == "" && !plain.unread && !plain.attachments)
        #expect(!plain.spoken.contains("message"))
    }

    // MARK: Annotated and not

    private func annotatedCase() -> Board.Case {
        F.mk(
            "c1", .you, subject: "Raw subject", snippet: "raw snippet",
            annotation: Board.Annotation(
                state: .hot, title: "Assistant title", summary: "Assistant summary", why: "Assistant why",
                due: F.day(16, 10), dueQuote: "by tomorrow", tasks: ["t1", "  ", "t2"]),
            draft: "Hi,\n\nreply")
    }

    @Test func withAnnotations() {
        let v = F.view([annotatedCase()], annotated: true)
        let r = v.sections[0].rows[0]
        #expect(r.title == "Assistant title")
        #expect(r.snippet == "Assistant summary")
        #expect(r.due == "Tomorrow")
        let d = try! #require(v.detail)
        #expect(d.state == .hot && d.source == .assistantChanged(from: .you))
        #expect(d.title == "Assistant title" && d.subject == "Raw subject")
        #expect(d.summary == "Assistant summary" && d.why == "Assistant why")
        #expect(d.due == "Tomorrow" && d.dueQuote == "by tomorrow")
        #expect(d.tasks == ["t1", "t2"])
        #expect(d.draft == "Hi,\n\nreply")
        #expect(d.stateTitle == "Hot")
        #expect(d.sourceText.contains("The rules suggested: Waiting for You."))
    }

    @Test func withoutAnnotations() {
        let v = F.view([annotatedCase()], annotated: false)
        let r = v.sections[0].rows[0]
        #expect(r.state == .you)
        #expect(r.title == "Raw subject")
        #expect(r.snippet == "raw snippet")
        #expect(r.due == "")
        let d = try! #require(v.detail)
        #expect(d.state == .you && d.source == .assistantOff)
        #expect(d.title == "Raw subject")
        #expect(d.subject == "")
        #expect(d.summary == "")
        #expect(d.due == "" && d.dueQuote == "")
        #expect(d.tasks.isEmpty)
        // The draft is a real draft of the account: it stays without the assistant.
        #expect(d.draft == "Hi,\n\nreply" && d.draftID == DraftID(rawValue: "d_c1"))
        #expect(d.why == Board.Text.reasonUnknown)  // "rule c1" is no code this client knows
        #expect(v.today.dueGroups.isEmpty)
        #expect(v.today.tiles.count == 4)
        #expect(v.statusLine == "Sorted by the daemon’s rules · assistant off")
    }

    @Test func subjectShownOnlyWhenTheTitleDiffers() {
        func detail(title: String, subject: String) -> Board.Detail? {
            F.view([F.mk("c1", subject: subject, annotation: Board.Annotation(state: .you, title: title))], annotated: true).detail
        }
        #expect(detail(title: "Same", subject: "Same")?.subject == "")
        #expect(detail(title: "Same", subject: "  Same \n")?.subject == "")  // cleaned first
        #expect(detail(title: "Other", subject: "Same")?.subject == "Same")
        // An empty title falls back to the subject, and then the two agree.
        #expect(detail(title: "", subject: "Same")?.title == "Same")
        #expect(detail(title: "", subject: "Same")?.subject == "")
        #expect(detail(title: "\u{202E}", subject: "Same")?.title == "Same")
    }

    @Test func emptySubjectAndSnippetFallbacks() {
        let v = F.view([F.mk("c1", subject: " \n ", snippet: "snip")])
        #expect(v.sections[0].rows[0].title == "(No subject)")
        // An annotation with an empty summary leaves the case snippet in the row.
        let a = Board.Annotation(state: .you, title: "T", summary: " ")
        let r = F.view([F.mk("c1", snippet: "snip", annotation: a)], annotated: true).sections[0].rows[0]
        #expect(r.snippet == "snip")
        #expect(F.view([F.mk("c1", snippet: "snip", annotation: a)], annotated: true).detail?.summary == "")
    }

    @Test func whyFallsBackToTheRulesReason() {
        let a = Board.Annotation(state: .you, title: "T", why: "")
        #expect(F.view([F.mk("c1", annotation: a)], annotated: true).detail?.why == Board.Text.reasonUnknown)
        var known = F.mk("c1", annotation: a)
        known.ruleReason = .youAddressed
        #expect(F.view([known], annotated: true).detail?.why == Board.Text.reason(.youAddressed))
        let b = Board.Annotation(state: .you, title: "T", why: "Mine")
        #expect(F.view([F.mk("c1", annotation: b)], annotated: true).detail?.why == "Mine")
    }

    @Test func dueQuoteOnlyWithADueDate() {
        let a = Board.Annotation(state: .you, title: "T", dueQuote: "quote")
        #expect(F.view([F.mk("c1", annotation: a)], annotated: true).detail?.dueQuote == "")
    }

    @Test func detailMessagesAndIssue() {
        let m = [
            Board.CaseMessage(from: "Bob", date: F.ago(2), text: "second"),
            Board.CaseMessage(from: "Ann", date: F.ago(5), text: "first", mine: false),
            Board.CaseMessage(from: "Me", date: F.ago(1), text: "third", mine: true),
        ]
        let c = F.mk(
            "c1", count: 3, messages: m, issue: Board.IssueInfo(key: "DEMO-2", status: "Done", style: .done))
        let d = try! #require(F.view([c]).detail)
        #expect(d.messages.map(\.text) == ["first", "second", "third"])  // oldest first
        #expect(d.messages.map(\.from) == ["Ann", "Bob", "You"])  // the user's own are "You"
        #expect(d.messages.map(\.mine) == [false, false, true])
        #expect(d.issue == Board.IssueInfo(key: "DEMO-2", status: "Done", style: .done))
        #expect(d.account == "Alpha" && d.person == "P c1" && !d.time.isEmpty && !d.isDone)
        #expect(F.view([F.mk("c1", done: true)]) { $0.filter = .done }.detail?.isDone == true)
    }

    // MARK: Commitments

    @Test func commitments() {
        let cases = [
            F.mk("c1", .hot, annotation: Board.Annotation(state: .hot, title: "Titled")),
            F.mk("c2", .you, account: F.accountB), F.mk("c3", .info, done: true),
        ]
        let ks = [
            Board.Commitment(id: "k1", caseID: F.id("c1"), text: "Do it", quote: "I will", due: F.day(20, 9)),
            Board.Commitment(id: "k2", caseID: F.id("c2"), text: "Other account"),
            Board.Commitment(id: "k3", caseID: F.id("c3"), text: "On a done case"),
            Board.Commitment(id: "k4", caseID: F.id("missing"), text: "Unknown case"),
            Board.Commitment(id: "k5", caseID: F.id("c1"), text: "Next year", due: F.calendar.date(from: DateComponents(year: 2027, month: 1, day: 3, hour: 9))),
        ]
        let on = F.view(cases, annotated: true, commitments: ks)
        #expect(on.commitments.map(\.id) == ["k1", "k2", "k5"])
        #expect(on.commitments[0].from == "Titled" && on.commitments[0].caseID == F.id("c1"))
        #expect(on.commitments[0].text == "Do it" && on.commitments[0].quote == "I will")
        #expect(on.commitments[0].due == "20 Oct")
        #expect(on.commitments[1].due == "" && on.commitments[1].from == "Subject c2")
        #expect(on.commitments[2].due == "2027-01-03")
        #expect(on.showsCommitmentsInList)
        #expect(on.today.commitments == on.commitments)
        #expect(on.today.tiles.last == Board.Tile(kind: .commitments, count: 3, title: "Promised"))

        // In the account scope.
        let b = F.view(cases, annotated: true, commitments: ks) { $0.account = .account(F.accountB) }
        #expect(b.commitments.map(\.id) == ["k2"])

        // The list shows them under Overview only.
        let you = F.view(cases, annotated: true, commitments: ks) { $0.filter = .state(.you) }
        #expect(!you.showsCommitmentsInList)
        #expect(!you.commitments.isEmpty)

        // Not annotated: none, and no tile.
        let off = F.view(cases, annotated: false, commitments: ks)
        #expect(off.commitments.isEmpty && off.today.commitments.isEmpty && !off.showsCommitmentsInList)
        #expect(off.today.tiles.map(\.kind) == Board.State.allCases.map { .state($0) })

        // No commitment, nothing to show.
        let none = F.view(cases, annotated: true)
        #expect(none.commitments.isEmpty && !none.showsCommitmentsInList)
        #expect(none.today.tiles.last?.count == 0)
    }

    // MARK: Due groups

    @Test func dueGroups() {
        func ann(_ due: Date?, quote: String = "") -> Board.Annotation {
            Board.Annotation(state: .you, title: "T", due: due, dueQuote: quote)
        }
        let cases = [
            F.mk("c1", annotation: ann(F.day(13, 9), quote: "q1")),
            F.mk("c2", annotation: ann(F.day(15, 23, 59))),
            F.mk("c3", annotation: ann(F.day(16, 0, 30))),
            F.mk("c4", annotation: ann(F.day(22, 23))),  // +7 days
            F.mk("c5", annotation: ann(F.day(23, 0))),  // +8 days
            F.mk("c6", annotation: ann(F.day(20, 9))),
            F.mk("c7", annotation: ann(F.day(18, 9))),
            F.mk("c8", annotation: ann(F.day(15, 9)), done: true),  // done: hidden
            F.mk("c9", account: F.accountB, annotation: ann(F.day(15, 8))),  // other account
            F.mk("c10", annotation: ann(nil)),
        ]
        let v = F.view(cases, annotated: true) { $0.account = .account(F.accountA) }
        let groups = v.today.dueGroups
        #expect(groups.map(\.kind) == Board.DueGroupKind.allCases)
        #expect(groups.map(\.title) == ["Overdue", "Today", "Tomorrow", "Next 7 Days", "Later"])
        #expect(groups.map { $0.items.map(\.caseID.rawValue) } == [["c1"], ["c2"], ["c3"], ["c7", "c6", "c4"], ["c5"]])
        #expect(groups[0].items[0].quote == "q1" && groups[0].items[0].label == "13 Oct")
        #expect(groups[1].items[0].label == "Today" && groups[2].items[0].label == "Tomorrow")
        #expect(groups[0].items[0].person == "P c1" && groups[0].items[0].title == "T")

        // Without the account filter the other account's case joins its group, sorted by date.
        let all = F.view(cases, annotated: true).today.dueGroups
        #expect(all[1].items.map(\.caseID.rawValue) == ["c9", "c2"])

        // Empty groups are left out.
        let one = F.view([F.mk("c1", annotation: ann(F.day(15, 10)))], annotated: true).today.dueGroups
        #expect(one.map(\.kind) == [.today])
        #expect(F.view([F.mk("c1")], annotated: true).today.dueGroups.isEmpty)

        // Switched off: none, even with the annotation there.
        #expect(F.view(cases, annotated: false).today.dueGroups.isEmpty)
        #expect(F.view(cases, annotated: true).today.dueEmpty == Board.Text.dueEmpty)
    }

    @Test func dueGroupBoundaries() {
        let cal = F.calendar
        func d(_ day: Int, _ h: Int, _ m: Int = 0, _ s: Int = 0) -> Date {
            cal.date(from: DateComponents(year: 2026, month: 10, day: day, hour: h, minute: m, second: s))!
        }
        // (now, due, group)
        let cases: [(Date, Date, Board.DueGroupKind)] = [
            (d(15, 0, 0, 10), d(14, 23, 59, 59), .overdue),  // just before midnight
            (d(15, 0, 0, 10), d(15, 0, 0, 0), .today),  // earlier today still counts as today
            (d(15, 23, 59, 30), d(15, 23, 59, 59), .today),
            (d(15, 23, 59, 30), d(16, 0, 0, 10), .tomorrow),  // seconds away, yet tomorrow
            (d(15, 12), d(16, 23, 59, 59), .tomorrow),
            (d(15, 12), d(17, 0, 0, 0), .thisWeek),
            (d(15, 12), d(22, 23, 59, 59), .thisWeek),  // +7 days
            (d(15, 12), d(23, 0, 0, 0), .later),  // +8 days
            (d(15, 12), d(14, 12), .overdue),
        ]
        for (i, (now, due, want)) in cases.enumerated() {
            #expect(Board.dueGroup(due, now: now, calendar: cal) == want, "case \(i)")
        }
        // The month end: 31 Oct to 1 Nov is one calendar day.
        let nov1 = cal.date(from: DateComponents(year: 2026, month: 11, day: 1, hour: 9))!
        #expect(Board.dueGroup(nov1, now: d(31, 23), calendar: cal) == .tomorrow)
        // A time zone moves midnight: 23:30 UTC is already tomorrow in Prague.
        var prague = cal
        prague.timeZone = TimeZone(identifier: "Europe/Prague")!
        let late = d(15, 23, 30)
        let due = d(16, 10)
        #expect(Board.dueGroup(due, now: late, calendar: cal) == .tomorrow)
        #expect(Board.dueGroup(due, now: late, calendar: prague) == .today)
    }

    // MARK: Panel and Today

    @Test func showsPanel() {
        let cases = [F.mk("c1"), F.mk("c2", .hot)]
        // (style, inlineDetail, selection, want)
        let table: [(Board.Style, Bool, String?, Bool)] = [
            (.list, true, "c1", false),
            (.list, true, nil, false),
            (.list, false, "c1", true),
            (.list, false, nil, false),
            (.columns, true, "c1", true),
            (.columns, false, "c1", true),
            (.columns, true, nil, false),
            (.today, true, "c1", true),
            (.today, true, nil, false),
        ]
        for (style, inline, sel, want) in table {
            let v = F.view(cases) {
                $0.style = style; $0.inlineDetail = inline; $0.selection = sel.map(F.id)
            }
            #expect(v.showsPanel == want, "\(style) \(inline) \(String(describing: sel))")
        }
    }

    @Test func todayPage() {
        var cases = [F.mk("h1", .hot, hours: 1), F.mk("h2", .hot, hours: 2)]
        for i in 1...7 {
            cases.append(F.mk("y\(i)", .you, hours: Double(i)))
        }
        cases.append(F.mk("t1", .them))
        cases.append(F.mk("i1", .info, done: true))
        let t = F.view(cases).today
        #expect(t.title == "Today")
        #expect(t.tiles.map(\.count) == [2, 7, 1, 0])
        #expect(t.tiles.map(\.title) == ["Hot", "Waiting for You", "Waiting for Them", "For Your Information"])
        #expect(F.ids(t.hot) == ["h1", "h2"])
        #expect(F.ids(t.you) == ["y1", "y2", "y3", "y4", "y5"])
        #expect(t.youMore == 2)
        #expect(t.phrase == "9 things need you today.")

        let few = F.view([F.mk("y1"), F.mk("y2")]).today
        #expect(few.youMore == 0 && few.you.count == 2)
        #expect(few.phrase == "2 things need you today.")

        let exactly = F.view((1...5).map { F.mk("y\($0)", hours: Double($0)) }).today
        #expect(exactly.youMore == 0 && exactly.you.count == 5)

        #expect(F.view([F.mk("y1")]).today.phrase == "1 thing needs you today.")
        #expect(F.view([F.mk("t1", .them)]).today.phrase == "Nothing needs you today.")
        #expect(F.view([]).today.phrase == "Nothing needs you today.")

        // The state decides, so an annotation can move a case into the top list.
        let a = Board.Annotation(state: .hot, title: "t")
        let moved = F.view([F.mk("y1", .you, annotation: a)], annotated: true).today
        #expect(F.ids(moved.hot) == ["y1"] && moved.you.isEmpty)
        #expect(moved.phrase == "1 thing needs you today.")
    }

    @Test func viewSaysWhetherTheAssistantIsOn() {
        #expect(F.view([F.mk("c1")], annotated: true).assistantOn)
        #expect(!F.view([F.mk("c1")], annotated: false).assistantOn)
    }

    @Test func snoozedRowSpeaksItsRemindTime() {
        let at = F.now.addingTimeInterval(20 * 3600)
        let c = F.mk("c1", visibility: .snoozed(until: at))
        let v = F.view([c]) { $0.filter = .snoozed }
        let r = v.sections.flatMap(\.rows).first { $0.id == F.id("c1") }!
        #expect(!r.remind.isEmpty)
        #expect(r.spoken.contains(Board.Text.spokenRemind(r.remind) + "."))
    }

    @Test func statusLine() {
        let s = Board.Snapshot(
            cases: [F.mk("c1")], annotated: true, run: Board.Run(model: "Claude", date: F.now, note: "3 sorted"))
        let v = Board.view(s, Board.ViewState(), now: F.now, calendar: F.calendar)
        #expect(v.statusLine == "Sorted by rules · refined by the assistant (Claude) · 3 sorted")
        var bare = s
        bare.run = nil
        #expect(Board.view(bare, Board.ViewState(), now: F.now, calendar: F.calendar).statusLine == "Sorted by rules · refined by the assistant")
    }

    // MARK: Cleaning in the view

    @Test func hostileStringsAreCleanedInTheView() {
        let evil = "\u{202E}ev\0il\r\n\u{2028}text\u{200B}"
        let ann = Board.Annotation(
            state: .you, title: evil, summary: evil, why: evil, due: F.day(15), dueQuote: evil, tasks: [evil])
        let c = Board.Case(
            id: F.id("c1"), account: F.accountA, person: evil, date: F.ago(1), subject: evil, snippet: evil,
            issue: Board.IssueInfo(key: evil, status: evil, style: .plain), ruleState: .you,
            ruleReason: BoardReason(rawValue: evil), annotation: ann,
            draft: Board.DraftLink(id: "d_1", text: evil),
            messages: [Board.CaseMessage(from: evil, date: F.ago(2), text: evil)])
        let k = Board.Commitment(id: "k", caseID: F.id("c1"), text: evil, quote: evil)
        let v = F.view([c], annotated: true, commitments: [k])
        var strings: [String] = []
        let r = v.sections[0].rows[0]
        strings += [r.person, r.title, r.snippet, r.issueKey, r.issueStatus, r.spoken]
        let d = v.detail!
        strings += [d.why, d.person, d.title, d.dueQuote, d.summary, d.draft, d.issue!.key, d.issue!.status]
        strings += d.tasks + d.messages.map(\.from) + d.messages.map(\.text)
        strings += v.commitments.flatMap { [$0.text, $0.quote, $0.from] }
        strings += v.today.dueGroups.flatMap { $0.items.flatMap { [$0.title, $0.person, $0.quote] } }
        for s in strings {
            #expect(!s.isEmpty)
            #expect(s.unicodeScalars.allSatisfy { $0.value != 0x202E && $0.value != 0 && $0.value != 0x200B && $0.value != 0x2028 })
        }
        // NUL and the override vanish; CR LF and U+2028 are two line breaks in a block, spaces in a line.
        #expect(r.title == "evil text")
        #expect(d.summary == "evil\n\ntext")
        #expect(d.subject == "")  // cleaned alike, so the title does not differ
    }

    @Test func capsAreHonouredInTheView() {
        let long = String(repeating: "é", count: 5000)  // 10 000 bytes
        let tasks = (0..<30).map { "task \($0)" } + ["   ", "\0"]
        let messages = (0..<60).map { i in
            Board.CaseMessage(from: "F", date: F.ago(Double(100 - i)), text: "m\(i)")
        }.reversed()
        let ann = Board.Annotation(state: .you, title: long, summary: long, tasks: tasks)
        let c = F.mk("c1", subject: long, snippet: long, count: 60, annotation: ann, messages: Array(messages), draft: long)
        let v = F.view([c], annotated: true)
        let r = v.sections[0].rows[0]
        #expect(r.title.utf8.count <= 300 && r.title.utf8.count > 250)
        #expect(r.snippet.utf8.count <= 400)
        let d = v.detail!
        #expect(d.title.utf8.count <= 300)
        #expect(d.subject == "")  // cleaned alike: no differing subject to show
        #expect(d.summary.utf8.count <= 2000)
        #expect(d.draft.utf8.count <= 4000)
        #expect(d.tasks.count == 20)
        #expect(d.tasks.first == "task 0" && d.tasks.last == "task 19")
        #expect(d.messages.count == 50)
        #expect(d.messages.first?.text == "m10" && d.messages.last?.text == "m59")  // the newest 50, oldest first
        #expect(d.conversationTitle == "Conversation · 60 messages")

        let bigMessage = F.mk("c2", messages: [Board.CaseMessage(from: String(repeating: "x", count: 1000), date: F.ago(1), text: String(repeating: "é", count: 10000))])
        let m = F.view([bigMessage]).detail!.messages[0]
        // board.get's cap (8000 bytes): a whole ordinary mail, no more.
        #expect(m.text.utf8.count <= 8000 && m.text.utf8.count > 7990 && m.from.utf8.count <= 200)
    }

    @Test func bigAccountAndBadgeAreCapped() {
        let s = Board.Snapshot(
            accounts: [Board.AccountInfo(id: F.accountA, name: String(repeating: "n", count: 1000), badge: String(repeating: "b", count: 1000))],
            cases: [F.mk("c1")])
        let v = Board.view(s, Board.ViewState(), now: F.now, calendar: F.calendar)
        #expect(v.accounts[1].title.utf8.count == 120 && v.accounts[1].badge.utf8.count == 64)
    }

    @Test func commitmentsShownAreCapped() {
        let ks = (0..<250).map { Board.Commitment(id: "k\($0)", caseID: F.id("c1"), text: "promise \($0)") }
        let v = F.view([F.mk("c1")], annotated: true, commitments: ks)
        #expect(v.commitments.count == 100 && v.today.commitments.count == 100)
        #expect(v.commitments.first?.id == "k0" && v.commitments.last?.id == "k99")
        // The tile counts them all.
        #expect(v.today.tiles.last?.kind == .commitments && v.today.tiles.last?.count == 250)
    }

    @Test func newestMessagesWithoutSortingThemAll() {
        // Shuffled dates with ties: the same as a stable sort's last 50.
        var rng = SystemRandomNumberGenerator()
        for n in [0, 1, 49, 50, 51, 120] {
            let ms = (0..<n).map { i in
                Board.CaseMessage(from: "F", date: F.ago(Double(Int.random(in: 0..<20, using: &rng))), text: "m\(i)")
            }
            let want = ms.enumerated().sorted { a, b in
                a.element.date != b.element.date ? a.element.date < b.element.date : a.offset < b.offset
            }.suffix(50).map(\.element.text)
            let got = F.view([F.mk("c1", messages: ms)]).detail!.messages.map(\.text)
            #expect(got == Array(want), "n \(n)")
        }
        // A huge conversation, newest first as some sources send it.
        let huge = (0..<200_000).map { i in Board.CaseMessage(from: "F", date: F.ago(Double(i) / 60), text: "m\(i)") }
        let d = F.view([F.mk("c1", messages: huge)]).detail!
        #expect(d.messages.count == 50)
        #expect(d.messages.first?.text == "m49" && d.messages.last?.text == "m0")
    }

    @Test func tasksScanIsBounded() {
        let blanks = { (n: Int) in [String](repeating: " \u{200B} ", count: n) }
        func tasks(_ t: [String]) -> [String] {
            F.view([F.mk("c1", annotation: Board.Annotation(state: .you, title: "T", tasks: t))], annotated: true).detail!.tasks
        }
        #expect(tasks(blanks(199) + ["x"]) == ["x"])  // the 200th is still looked at
        #expect(tasks(blanks(200) + ["x"]) == [])  // the 201st is not
        #expect(tasks(blanks(150) + (0..<30).map { "t\($0)" }) == (0..<20).map { "t\($0)" })
        #expect(tasks(blanks(100_000)) == [])
    }

    @Test func aRepeatedCaseIDShowsTheFirstCaseOnly() {
        let first = F.mk("c1", .hot, subject: "first")
        let again = F.mk("c1", .them, hours: 0.5, subject: "again")
        let v = F.view([F.mk("c2", hours: 2), first, again, F.mk("c1", done: true)])
        let ids = v.sections.flatMap(\.rows).map(\.id.rawValue)
        #expect(ids == ["c1", "c2"] && Set(ids).count == ids.count)
        #expect(v.sections[0].rows[0].title == "first")
        #expect(v.columns.flatMap(\.rows).map(\.id.rawValue) == ["c1", "c2"])
        #expect(v.nav.map(\.count) == [2, 1, 1, 0, 0, 0, 0])
        #expect(v.accounts.map(\.count) == [2, 2, 0])
        #expect(v.detail?.id == F.id("c1") && v.detail?.title == "first")
        let selected = F.view([first, again]) { $0.style = .columns; $0.selection = F.id("c1") }
        #expect(selected.detail?.title == "first" && selected.columns[2].rows.isEmpty)
    }

    // MARK: Selection

    private func snapshot(_ cases: [Board.Case]) -> Board.Snapshot {
        Board.Snapshot(accounts: F.accounts, cases: cases)
    }

    private var sample: [Board.Case] {
        [
            F.mk("c1", .hot, hours: 4), F.mk("c2", .you, hours: 3), F.mk("c3", .you, hours: 2),
            F.mk("c4", .them, hours: 1), F.mk("d1", .info, hours: 6, done: true),
            F.mk("d2", .info, hours: 5, done: true), F.mk("b1", .you, account: F.accountB, hours: 7),
        ]
    }

    @Test func resolveSelectionInTheList() {
        let s = snapshot(sample)
        func resolve(_ configure: (inout Board.ViewState) -> Void) -> String? {
            var v = Board.ViewState()
            configure(&v)
            return Board.resolveSelection(s, v)?.rawValue
        }
        // Inline detail, nothing selected: the first row.
        #expect(resolve { _ in } == "c1")
        // A shown case stays.
        #expect(resolve { $0.selection = F.id("c3") } == "c3")
        // Outside the filter: the first row of the filter.
        #expect(resolve { $0.filter = .state(.you); $0.selection = F.id("c1") } == "c3")
        #expect(resolve { $0.filter = .state(.you); $0.selection = F.id("c3") } == "c3")
        // Done is its own list.
        #expect(resolve { $0.filter = .done } == "d2")
        #expect(resolve { $0.filter = .done; $0.selection = F.id("d1") } == "d1")
        #expect(resolve { $0.selection = F.id("d1") } == "c1")
        // Outside the account scope.
        #expect(resolve { $0.account = .account(F.accountB); $0.selection = F.id("c1") } == "b1")
        #expect(resolve { $0.account = .account(F.accountB); $0.filter = .state(.hot) } == nil)
        // Unknown.
        #expect(resolve { $0.selection = F.id("zz") } == "c1")
        // No inline detail: nothing selects itself, a valid selection stays.
        #expect(resolve { $0.inlineDetail = false } == nil)
        #expect(resolve { $0.inlineDetail = false; $0.selection = F.id("c2") } == "c2")
        #expect(resolve { $0.inlineDetail = false; $0.selection = F.id("zz") } == nil)
        // Empty list.
        #expect(Board.resolveSelection(.empty, Board.ViewState()) == nil)
    }

    @Test func resolveSelectionInColumnsAndToday() {
        let s = snapshot(sample)
        for style in [Board.Style.columns, .today] {
            func resolve(_ configure: (inout Board.ViewState) -> Void) -> String? {
                var v = Board.ViewState()
                v.style = style
                configure(&v)
                return Board.resolveSelection(s, v)?.rawValue
            }
            // Never selects by itself, with or without inline detail.
            #expect(resolve { _ in } == nil)
            #expect(resolve { $0.inlineDetail = false } == nil)
            // Any live case, whatever the filter.
            #expect(resolve { $0.selection = F.id("c4") } == "c4")
            #expect(resolve { $0.filter = .state(.hot); $0.selection = F.id("c4") } == "c4")
            #expect(resolve { $0.filter = .done; $0.selection = F.id("c4") } == "c4")
            // A done case is not on the board here.
            #expect(resolve { $0.selection = F.id("d1") } == nil)
            #expect(resolve { $0.filter = .done; $0.selection = F.id("d1") } == nil)
            // The account scope.
            #expect(resolve { $0.account = .account(F.accountB); $0.selection = F.id("c4") } == nil)
            #expect(resolve { $0.account = .account(F.accountB); $0.selection = F.id("b1") } == "b1")
        }
    }

    @Test func viewSelectionIsTheResolvedOne() {
        let v = F.view(sample) { $0.selection = F.id("zz") }
        #expect(v.selection == F.id("c1") && v.detail?.id == F.id("c1"))
        let col = F.view(sample) { $0.style = .columns; $0.selection = F.id("c4") }
        #expect(col.selection == F.id("c4") && col.detail?.id == F.id("c4") && col.showsPanel)
    }

    @Test func selectionAfterDone() {
        let s = snapshot(sample)
        func after(_ id: String, _ configure: (inout Board.ViewState) -> Void = { _ in }) -> String? {
            var v = Board.ViewState()
            configure(&v)
            return Board.selectionAfterDone(F.id(id), s, v)?.rawValue
        }
        // List rows: c1 hot, c2 c3 you (c3 newer first? c3 is 2 h ago, c2 3 h: c3 first), c4 them, b1 you.
        let shown = Board.view(s, Board.ViewState(), now: F.now, calendar: F.calendar).sections.flatMap(\.rows).map(\.id.rawValue)
        #expect(shown == ["c1", "c3", "c2", "b1", "c4"])
        #expect(after("c1") == "c3")  // the first: the next
        #expect(after("c3") == "c2")  // the middle: the next
        #expect(after("c4") == "b1")  // the last: the previous
        #expect(after("zz") == nil)
        #expect(after("d1") == nil)  // not in the list
        // Under a filter the filter's rows count.
        #expect(after("c2") { $0.filter = .state(.you) } == "b1")
        #expect(after("b1") { $0.filter = .state(.you) } == "c2")
        #expect(after("c1") { $0.filter = .state(.you) } == nil)
        // Done list: reopening.
        #expect(after("d2") { $0.filter = .done } == "d1")
        #expect(after("d1") { $0.filter = .done } == "d2")
        // Account scope.
        #expect(after("b1") { $0.account = .account(F.accountB) } == nil)  // the only row
        #expect(after("c1") { $0.account = .account(F.accountB) } == nil)
        // Columns and Today select nothing.
        for style in [Board.Style.columns, .today] {
            #expect(after("c1") { $0.style = style } == nil)
            #expect(after("c3") { $0.style = style } == nil)
        }
    }
}

@Suite struct BoardCleaningTests {
    @Test func cleanLine() {
        let table: [(String, String)] = [
            ("plain", "plain"),
            ("  a   b  ", "a b"),
            ("a\tb", "a b"),
            ("a\r\nb", "a b"),
            ("a\nb\rc", "a b c"),
            ("a\u{2028}b\u{2029}c", "a b c"),
            ("a\u{85}b", "a b"),
            ("a\0b", "ab"),
            ("a\u{202E}b", "ab"),  // right-to-left override
            ("\u{202E}a\u{202C}", "a"),
            ("a\u{200B}b\u{FEFF}d", "abd"),  // zero width, BOM
            ("a\u{2066}b\u{2069}", "ab"),  // isolates
            ("a\u{7}b\u{1B}c", "abc"),  // bell, escape
            ("\u{202E}\u{200B}\0\u{FEFF}", ""),  // only format characters
            ("", ""),
            ("   \n\t ", ""),
            ("čeština ✓ 🙂", "čeština ✓ 🙂"),
        ]
        for (input, want) in table {
            #expect(Board.cleanLine(input, max: 100) == want, "\(input.debugDescription)")
        }
        #expect(Board.cleanLine("abc", max: 0) == "")
    }

    @Test func cleanLineCaps() {
        #expect(Board.cleanLine("abcdef", max: 3) == "abc")
        #expect(Board.cleanLine("ab cd", max: 3) == "ab")  // the space at the cut goes
        #expect(Board.cleanLine("éé", max: 3) == "é")  // a scalar is never cut
        #expect(Board.cleanLine("€€€", max: 8) == "€€")
        #expect(Board.cleanLine("🙂🙂", max: 7) == "🙂")
        #expect(Board.cleanLine("🙂", max: 3) == "")
    }

    @Test func cleanLineBigInput() {
        let mb = String(repeating: "é", count: 500_000)  // 1 MB
        for max in [1, 2, 99, 100, 300] {
            let out = Board.cleanLine(mb, max: max)
            #expect(out.utf8.count == max - max % 2, "max \(max)")
            #expect(out.unicodeScalars.allSatisfy { $0 == "é" })
        }
        let three = String(repeating: "€", count: 400_000)
        let out = Board.cleanLine(three, max: 100)
        #expect(out.utf8.count == 99)  // 33 scalars of 3 bytes, cut on the boundary
        // A megabyte of nothing but format characters and whitespace.
        let nothing = String(repeating: "\u{202E} \u{200B}\n", count: 250_000)
        #expect(Board.cleanLine(nothing, max: 300) == "")
        #expect(Board.cleanBlock(nothing, max: 300) == "")
        // A megabyte of spaces between two words: the scan gives up long
        // before the second word, so the text ends with the first.
        let gap = "a" + String(repeating: " ", count: 1_000_000) + "b"
        #expect(Board.cleanLine(gap, max: 300) == "a")
        #expect(Board.cleanBlock(gap, max: 300) == "a")
        // A shorter gap is read through.
        let short = "a" + String(repeating: " ", count: 1000) + "b"
        #expect(Board.cleanLine(short, max: 300) == "a b")
    }

    /// A megabyte of what the cleaners drop costs no more than a short
    /// input: they read a bounded number of scalars (8 × the cap).
    @Test func cleaningIsBoundedForDroppedInput() {
        let zw = String(repeating: "\u{200B}", count: 1_000_000)
        let spaces = String(repeating: " ", count: 1_000_000)
        let clock = ContinuousClock()
        let took = clock.measure {
            for _ in 0..<20 {
                #expect(Board.cleanLine(zw, max: 300) == "")
                #expect(Board.cleanBlock(zw, max: 4000) == "")
                #expect(Board.cleanLine(spaces, max: 300) == "")
                #expect(Board.cleanBlock(spaces, max: 4000) == "")
            }
        }
        // Eighty full scans of a megabyte would take seconds even optimised.
        #expect(took < .seconds(2), "\(took)")
        // Past the budget nothing more is read, even what would be kept.
        #expect(Board.cleanLine(String(repeating: "\u{200B}", count: 24) + "x", max: 3) == "")
        #expect(Board.cleanLine(String(repeating: "\u{200B}", count: 23) + "x", max: 3) == "x")
        #expect(Board.cleanBlock(String(repeating: "\u{200B}", count: 24) + "x", max: 3) == "")

        // A whole view over such a case.
        let c = F.mk("c1", subject: zw, snippet: spaces, messages: [Board.CaseMessage(from: zw, date: F.ago(1), text: spaces)])
        let v = F.view([c, F.mk("c2", hours: 2)])
        let r = v.sections[0].rows[0]
        #expect(r.id == F.id("c1") && r.title == subjectText("") && r.snippet == "" && r.person == "P c1")
        let d = v.detail!
        #expect(d.id == F.id("c1") && d.title == subjectText("") && d.subject == "")
        #expect(d.messages.map(\.from) == [""] && d.messages.map(\.text) == [""])
        #expect(F.ids(v.sections[0].rows) == ["c1", "c2"])
    }

    @Test func theCutNeverBreaksACharacter() {
        let e = "e\u{301}"  // é, decomposed: 3 bytes
        #expect(Board.cleanLine("ab" + e, max: 3) == "ab")  // the e would lose its accent
        #expect(Board.cleanLine("ab" + e, max: 4) == "ab")
        #expect(Board.cleanLine("ab" + e, max: 5) == "ab" + e)  // it fits
        #expect(Board.cleanLine("ab " + e + "cd", max: 4) == "ab")  // the space before goes too
        #expect(Board.cleanBlock("ab\n" + e, max: 4) == "ab")
        #expect(Board.cleanBlock("ab" + e, max: 3) == "ab")
        // Several marks on one letter: the cluster goes whole.
        #expect(Board.cleanLine("a" + "e\u{301}\u{302}\u{303}", max: 5) == "a")
        let cz = "\u{1F1E8}\u{1F1FF}"  // a flag: two regional indicators, 8 bytes
        #expect(Board.cleanLine(cz + cz, max: 12) == cz)  // not a lone indicator
        #expect(Board.cleanLine(cz + cz, max: 16) == cz + cz)
        #expect(Board.cleanLine(cz, max: 4) == "")
        #expect(Board.cleanBlock(cz + cz, max: 15) == cz)
        // Not cut at all: a trailing cluster stays as it is.
        #expect(Board.cleanLine("x\u{1F1E8}", max: 100) == "x\u{1F1E8}")
        // A joiner between two kept characters stays (the daemon's rule);
        // one the cut leaves last goes.
        #expect(Board.cleanLine("a\u{200D}b\u{200C}c", max: 100) == "a\u{200D}b\u{200C}c")
        #expect(Array(Board.cleanLine("ab\u{200D}c", max: 5).unicodeScalars) == Array("ab".unicodeScalars))
    }

    /// A cut at the cap never leaves a joiner last (`capped`), in a line
    /// and in a block, whichever joiner and wherever the cap falls in a
    /// joined sequence.
    @Test func capLeavesNoTrailingJoiner() {
        let inputs = ["ab\u{200D}c", "ab\u{200C}c", "x \u{1F469}\u{200D}\u{1F469}", "a\u{200D}b\u{200D}c\u{200D}d"]
        for input in inputs {
            for max in 1...input.utf8.count {
                for out in [Board.cleanLine(input, max: max), Board.cleanBlock(input, max: max)] {
                    #expect(out.utf8.count <= max, "\(input.debugDescription) \(max)")
                    if let last = out.unicodeScalars.last {
                        #expect(!Board.JoinerState.isJoiner(last), "\(input.debugDescription) \(max)")
                    }
                }
            }
        }
        #expect(Array(Board.cleanBlock("ab\u{200D}c", max: 5).unicodeScalars) == Array("ab".unicodeScalars))
        #expect(Array(Board.cleanLine("ab\u{200C}c", max: 5).unicodeScalars) == Array("ab".unicodeScalars))
    }

    @Test func cleanBlock() {
        let table: [(String, String)] = [
            ("plain", "plain"),
            ("a\nb", "a\nb"),
            ("a\r\nb", "a\nb"),
            ("a\rb", "a\nb"),
            ("a\r\rb", "a\n\nb"),
            ("a\n\rb", "a\n\nb"),
            ("a\u{2028}b", "a\nb"),
            ("a\u{2029}b", "a\nb"),
            ("a\u{85}b\u{B}c\u{C}d", "a\nb\nc\nd"),
            ("a\n\n\n\n\nb", "a\n\nb"),  // at most one empty line
            ("a  \nb", "a\nb"),  // no spaces at the end of a line
            ("a\n  b", "a\n  b"),  // indentation stays
            ("a\tb", "a b"),
            ("\n\n a\n\n", "a"),
            ("a\0b", "ab"),
            ("a\u{202E}b\u{200B}c", "abc"),
            ("\u{202E}\u{200B}\0", ""),
            ("", ""),
        ]
        for (input, want) in table {
            #expect(Board.cleanBlock(input, max: 100) == want, "\(input.debugDescription)")
        }
        #expect(Board.cleanBlock("abc", max: 0) == "")
        #expect(Board.cleanBlock("ab\n\ncd", max: 3) == "ab")
        #expect(Board.cleanBlock("éé", max: 3) == "é")
    }

    @Test func cleanBlockBigInput() {
        let mb = String(repeating: "é", count: 500_000)
        let out = Board.cleanBlock(mb, max: 4000)
        #expect(out.utf8.count == 4000)
        let lines = String(repeating: "line\r\n", count: 200_000)
        let blk = Board.cleanBlock(lines, max: 4000)
        #expect(blk.utf8.count <= 4000 && !blk.contains("\r") && blk.hasPrefix("line\nline"))
        #expect(!blk.hasSuffix("\n"))
    }
}

@Suite struct BoardSampleTests {
    private let snap = Board.sampleSnapshot(now: BoardFixture.now, calendar: BoardFixture.calendar)

    private var strings: [String] {
        var out: [String] = []
        for a in snap.accounts { out += [a.name, a.badge] }
        for c in snap.cases {
            out += [c.person, c.subject, c.snippet, Board.Text.reason(c.ruleReason), c.draft?.text ?? ""]
            out += [c.issue?.key ?? "", c.issue?.status ?? ""]
            if let a = c.annotation { out += [a.title, a.summary, a.why, a.dueQuote] + a.tasks }
            for m in c.messages ?? [] { out += [m.from, m.text] }
        }
        for k in snap.commitments { out += [k.text, k.quote] }
        out += [snap.run?.model ?? "", snap.run?.note ?? ""]
        return out
    }

    @Test func everyAddressEndsInInvalid() {
        var found = 0
        for s in strings {
            for token in s.split(whereSeparator: { $0.isWhitespace }) where token.contains("@") {
                found += 1
                let t = token.trimmingCharacters(in: CharacterSet.alphanumerics.inverted)
                #expect(t.hasSuffix(".invalid"), "\(token)")
            }
        }
        #expect(found >= 1)  // the samples do use an address, so the check has teeth
        // No web address either.
        #expect(!strings.contains { $0.contains("http") || $0.contains("www.") })
    }

    @Test func coversTheStatesAndShapes() {
        let st = Set(snap.cases.map { Board.state(of: $0, annotated: true) })
        #expect(st == Set(Board.State.allCases))
        #expect(snap.annotated)
        #expect(snap.accounts.count == 4)
        #expect(Set(snap.accounts.map(\.id)).count == 4)
        #expect(Set(snap.cases.map(\.id)).count == snap.cases.count)
        let ids = Set(snap.accounts.map(\.id))
        #expect(snap.cases.allSatisfy { ids.contains($0.account) })
        #expect(snap.cases.filter(\.done).count == 2)
        let jira = snap.cases.filter { $0.issue != nil }
        #expect(!jira.isEmpty && jira.allSatisfy { $0.issue!.key.hasPrefix("DEMO-") })
        #expect(snap.cases.contains { $0.annotation == nil })  // one the assistant has not looked at
        #expect(snap.cases.contains { $0.draft != nil })
        // Every reason is a code this client knows.
        #expect(snap.cases.allSatisfy { BoardReason.known.contains($0.ruleReason) })
        #expect(snap.cases.allSatisfy { $0.messages != nil })  // loaded: the samples need no daemon
        #expect(snap.cases.contains { !($0.annotation?.tasks.isEmpty ?? true) })
        #expect(snap.cases.contains { $0.userState != nil })
        #expect(snap.cases.contains { Board.stateSource(of: $0, annotated: true) == .assistantKept })
        #expect(snap.cases.contains {
            if case .assistantChanged = Board.stateSource(of: $0, annotated: true) { return true }
            return false
        })
        #expect(snap.cases.contains(where: \.unread) && snap.cases.contains(where: \.hasAttachments))
        #expect(snap.cases.allSatisfy { $0.date <= BoardFixture.now })
    }

    @Test func viewShowsEverything() {
        let v = Board.view(snap, Board.ViewState(), now: BoardFixture.now, calendar: BoardFixture.calendar)
        #expect(!v.isEmpty)
        #expect(v.today.dueGroups.map(\.kind) == Board.DueGroupKind.allCases)
        #expect(v.commitments.count >= 2)
        #expect(!v.commitments.contains { $0.caseID.rawValue == "sample-21" })  // on a done case
        #expect(v.today.youMore >= 1 && v.today.you.count == Board.youTopCount)
        #expect(v.columns.allSatisfy { !$0.rows.isEmpty })
        #expect(v.sections.count == 4)
        #expect(v.detail != nil)
        let done = Board.view(snap, { var s = Board.ViewState(); s.filter = .done; return s }(), now: BoardFixture.now, calendar: BoardFixture.calendar)
        #expect(done.sections.first?.rows.count == 2)
        #expect(v.statusLine.contains("Claude"))
    }

    /// A due quote cites the conversation word for word: it is a part of
    /// one of the case's messages.
    @Test func dueQuotesCiteTheMessages() {
        var checked = 0
        for c in snap.cases {
            guard let a = c.annotation, !a.dueQuote.isEmpty, let ms = c.messages, !ms.isEmpty else { continue }
            #expect(ms.contains { $0.text.contains(a.dueQuote) }, "\(c.id.rawValue): \(a.dueQuote)")
            checked += 1
        }
        #expect(checked >= 4)
        // No weekday names in what has a deadline: the dates are relative.
        let weekdays = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"]
        for c in snap.cases where c.annotation?.due != nil {
            let a = c.annotation!
            let texts = [c.snippet, a.summary, a.why, a.dueQuote, c.draft?.text ?? ""] + (c.messages ?? []).map(\.text)
            #expect(!texts.contains { t in weekdays.contains { t.contains($0) } }, "\(c.id.rawValue)")
        }
    }

    @Test func stableForAFixedNow() {
        let again = Board.sampleSnapshot(now: BoardFixture.now, calendar: BoardFixture.calendar)
        #expect(again == snap)
        // Relative to now: a day later, the same shape with later dates.
        let later = Board.sampleSnapshot(now: BoardFixture.now.addingTimeInterval(86400), calendar: BoardFixture.calendar)
        #expect(later != snap)
        #expect(later.cases.map(\.id) == snap.cases.map(\.id))
    }

    /// The samples show a reminder that came due and a new contact (Go
    /// samples 22 and 23), so their marks can be seen in the preview.
    @Test func remindedAndNewContact() {
        #expect(snap.cases.contains { $0.reminded && $0.id.rawValue.hasSuffix("22") })
        #expect(snap.cases.contains { $0.newContact && $0.id.rawValue.hasSuffix("23") })
    }
}

/// The inline suggested reply and Unstar in the detail.
@Suite struct BoardInlineReplyDetailTests {
    @Test func draftIDWithoutText() {
        // An empty draft is still the case's suggested reply: edited inline.
        let d = try! #require(F.view([F.mk("c1", draft: "")]).detail)
        #expect(d.draftID == DraftID(rawValue: "d_c1") && d.draft == "")
        let none = try! #require(F.view([F.mk("c1")]).detail)
        #expect(none.draftID == nil)
    }

    @Test func unstarOnlyForTheStar() {
        func detail(_ reason: BoardReason, done: Bool = false, user: Board.State? = nil) -> Board.Detail {
            var c = F.mk("c1", .hot, user: user, done: done)
            c.ruleReason = reason
            return try! #require(F.view([c]) { $0.filter = done ? .done : $0.filter }.detail)
        }
        #expect(detail(.hotFlagged).canUnstar)
        #expect(detail(.hotFlagged, user: .info).canUnstar, "whatever the user's state")
        #expect(!detail(.hotFlagged, done: true).canUnstar)
        #expect(!detail(.hotImportant).canUnstar)
        #expect(!detail(.youAddressed).canUnstar)
    }

    @Test func texts() {
        #expect(Board.Text.draftNote == "Only here on the board until you send it")
        #expect(Board.Text.replyLoading == "Loading the suggested reply…")
        #expect(Board.Text.replyLoadFailed == "The suggested reply could not be opened.")
        #expect(Board.Text.replyRemoved == "The suggested reply was removed elsewhere.")
        #expect(Board.Text.unstar == "Unstar")
        #expect(Board.Text.failed(.unflag, RPCError(code: .caseNotFound, message: "x"))
            == "Removing the star failed: the case is no longer on the board.")
    }
}

/// The inline reply editor's height: the content's, clamped to
/// [160, min(480, 0.6 × visible)].
@Suite struct EditorHeightTests {
    @Test func clamps() {
        let tall: CGFloat = 1000
        // Short content: the minimum, no scrolling.
        #expect(EditorHeight(content: 40, visible: tall) == EditorHeight(content: 0, visible: tall))
        #expect(EditorHeight(content: 40, visible: tall).height == 160 && !EditorHeight(content: 40, visible: tall).scrolls)
        // In between: the content's.
        #expect(EditorHeight(content: 300, visible: tall).height == 300)
        #expect(!EditorHeight(content: 300, visible: tall).scrolls)
        // 0.6 × 1000 = 600 > 480: the maximum caps it.
        #expect(EditorHeight(content: 700, visible: tall).height == 480 && EditorHeight(content: 700, visible: tall).scrolls)
        // A small detail: 0.6 × 500 = 300.
        #expect(EditorHeight(content: 700, visible: 500).height == 300)
        #expect(EditorHeight(content: 300, visible: 500).height == 300 && !EditorHeight(content: 300, visible: 500).scrolls)
        // Tiny: never below the minimum.
        #expect(EditorHeight(content: 700, visible: 100).height == 160 && EditorHeight(content: 700, visible: 100).scrolls)
        #expect(EditorHeight.cap(visible: 100) == 160 && EditorHeight.cap(visible: 700) == 420)
    }

    @Test func oddInput() {
        #expect(EditorHeight(content: .nan, visible: 1000).height == 160)
        #expect(EditorHeight(content: .infinity, visible: 1000).height == 160)
        #expect(EditorHeight(content: -5, visible: 1000).height == 160)
        #expect(EditorHeight.cap(visible: 0) == 480 && EditorHeight.cap(visible: .nan) == 480)
    }
}
