// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The board fixes of 2026-10-08, ported 1:1 from the Go reference
/// (ui/internal/board fixes_test.go, clean_joiner_test.go, remind_test.go,
/// mode_test.go, keys.go).
@Suite struct BoardFixesTests {
    // MARK: Joiners (clean_joiner_test.go)

    /// The joiner rule is the daemon's (backend internal/board
    /// TestCleanTextJoiners, the same cases), in `cleanLine` and in
    /// `cleanBlock`.
    @Test func cleanJoiners() {
        let zj = "\u{200D}", znj = "\u{200C}"
        let cases: [(name: String, input: String, line: String, block: String)] = [
            ("emoji zwj sequence", "\u{1F468}" + zj + "\u{1F469}" + zj + "\u{1F467}",
             "\u{1F468}" + zj + "\u{1F469}" + zj + "\u{1F467}", ""),
            ("heart on fire keeps vs16 and zwj", "\u{2764}\u{FE0F}" + zj + "\u{1F525}",
             "\u{2764}\u{FE0F}" + zj + "\u{1F525}", ""),
            ("persian zwnj", "\u{0645}\u{06CC}" + znj + "\u{062E}", "\u{0645}\u{06CC}" + znj + "\u{062E}", ""),
            ("leading", zj + "a", "a", ""),
            ("trailing", "a" + znj, "a", ""),
            ("before a space", "a" + zj + " b", "a b", ""),
            ("after a space", "a " + zj + "b", "a b", ""),
            ("at a line end", "a" + zj + "\nb", "a b", "a\nb"),
            ("at a line start", "a\n" + znj + "b", "a b", "a\nb"),
            ("a run of joiners", "a" + zj + zj + "b", "ab", ""),
            ("mixed run", "a" + zj + znj + "b", "ab", ""),
            ("invisible between is dropped first", "a" + zj + "\u{200B}b", "a" + zj + "b", ""),
            ("vs16 after a joiner goes", "\u{2764}" + zj + "\u{FE0F}\u{1F525}", "\u{2764}" + zj + "\u{1F525}", ""),
            ("only joiners", zj + znj + zj, "", ""),
            ("joiner then a control", "a" + zj + "\u{0}", "a", ""),
            ("bidi override around", "a" + zj + "\u{202E}b", "a" + zj + "b", ""),
        ]
        for c in cases {
            let line = Board.cleanLine(c.input, max: 100)
            #expect(Array(line.unicodeScalars) == Array(c.line.unicodeScalars), "cleanLine \(c.name)")
            let want = c.block.isEmpty ? c.line : c.block
            let block = Board.cleanBlock(c.input, max: 100)
            #expect(Array(block.unicodeScalars) == Array(want.unicodeScalars), "cleanBlock \(c.name)")
        }
    }

    // MARK: Reminded, new contact, the user's decision (fixes_test.go)

    private typealias F = BoardFixture

    private func reminded(_ c: Board.Case) -> Board.Case {
        var c = c
        c.remindedAt = F.ago(2)
        return c
    }

    private func reason(_ c: Board.Case, _ r: BoardReason) -> Board.Case {
        var c = c
        c.ruleReason = r
        return c
    }

    @Test func remindedComesFirstInItsState() {
        let cases = [
            F.mk("y1", .you, hours: 1), reminded(F.mk("y2", .you, hours: 30)),
            F.mk("h1", .hot, hours: 1), F.mk("y3", .you, hours: 5),
        ]
        let v = F.view(cases)
        #expect(v.sections.flatMap(\.rows).map(\.id.rawValue) == ["h1", "y2", "y1", "y3"])
        #expect(F.ids(v.columns[1].rows) == ["y2", "y1", "y3"])
        #expect(F.ids(v.today.you) == ["y2", "y1", "y3"])
        let r = v.columns[1].rows[0]
        #expect(r.reminded && r.badges == ["Reminded"])
        #expect(r.spoken.hasPrefix("Waiting for You. Reminded."))
        #expect(!v.columns[1].rows[1].reminded && v.columns[1].rows[1].badges.isEmpty)
        let d = F.view(cases) { $0.selection = F.id("y2") }.detail
        #expect(d?.reminded == true && d?.whyNotes == ["A reminder you set has come due."])
        // A snoozed or done case is not reminded, whatever the field says.
        let snoozed = reminded(F.mk("s", .you, visibility: .snoozed(until: F.now.addingTimeInterval(3600))))
        #expect(!snoozed.reminded)
    }

    @Test func newContactBadgeAndReason() {
        let c = reason(F.mk("n1", .you), .youNewContact)
        let v = F.view([c])
        #expect(v.sections[0].rows[0].newContact && v.sections[0].rows[0].badges == ["New contact"])
        #expect(v.detail?.why == "The newest message is addressed to you by someone you have never written to.")
        let both = F.view([reminded(reason(F.mk("n2", .you), .youNewContact))])
        #expect(both.detail?.badges == ["Reminded", "New contact"])
        #expect(Board.Text.reason(.infoUnknownSender) != Board.Text.reason(.youNewContact))
    }

    @Test func theUsersDecisionKeepsIt() {
        #expect(F.view([F.mk("u1", .info, user: .hot)]).detail?.whyNotes == ["Your decision keeps it on the board."])
        #expect(F.view([F.mk("r1", .hot)]).detail?.whyNotes.isEmpty == true)
    }

    @Test func detailByline() {
        let d = F.view([F.mk("c1", .you)]).detail
        #expect(d?.byline == Board.Text.personAndTime(d?.person ?? "", d?.time ?? ""))
        #expect(d?.byline == "P c1 · " + (d?.time ?? ""))
    }

    @Test func bylineWithOneOfPersonAndTimeStandsAlone() {
        var c = F.mk("c1", .you)
        c.person = ""
        let d = F.view([c]).detail
        #expect(d?.person == "" && d?.byline == d?.time && d?.byline.hasPrefix(" ·") == false)
    }

    @Test func accountLabels() {
        let v = F.view([F.mk("c1", .you)])
        #expect(v.accounts.map(\.label) == ["All Accounts", "Alpha (IMAP)", "Beta (JIRA)"])
    }

    @Test func tileToolTips() {
        let v = F.view([F.mk("h1", .hot), F.mk("h2", .hot), F.mk("y1", .you)], annotated: true)
        #expect(v.today.tiles.map(\.toolTip) == [
            "Hot: 2 cases", "Waiting for You: 1 case", "Waiting for Them: 0 cases", "For Your Information: 0 cases",
            "Promised: 0 promises",
        ])
    }

    /// New since yesterday's midnight, due today, or back from a reminder;
    /// hot and waiting for you only.
    @Test func todayPhraseCountsOnlyWhatNeedsYouToday() {
        func due(_ at: Date) -> Board.Annotation { Board.Annotation(state: nil, title: "", due: at) }
        let cases = [
            F.mk("new", .you, hours: 1),  // today
            F.mk("yesterday", .hot, hours: 35),  // the 14th, 01:00
            F.mk("old", .you, hours: 40),  // the 13th, 20:00
            F.mk("old due today", .you, hours: 100, annotation: due(F.day(15, 18))),
            F.mk("old due tomorrow", .you, hours: 100, annotation: due(F.day(16, 9))),
            reminded(F.mk("old reminded", .hot, hours: 100)),
            F.mk("them new", .them, hours: 1),
            F.mk("info new", .info, hours: 1),
        ]
        #expect(F.view(cases, annotated: true).today.phrase == "4 things need you today.")
        // Without the assistant its deadlines do not count.
        #expect(F.view(cases).today.phrase == "3 things need you today.")
        #expect(F.view([F.mk("old", .you, hours: 100)]).today.phrase == "Nothing needs you today.")
    }

    // MARK: Snoozed and Done (view.go)

    @Test func snoozedHasItsOwnFilter() {
        let cases = [
            F.mk("l", .you), F.mk("d", .info, done: true),
            F.mk("s", .you, visibility: .snoozed(until: F.day(16, 9))),
        ]
        let v = F.view(cases)
        #expect(v.nav.map(\.filter) == Board.filters)
        #expect(v.nav.map(\.count) == [1, 0, 1, 0, 0, 1, 1])
        #expect(v.nav.map(\.title).suffix(2) == ["Snoozed", "Done"])
        let snoozed = F.view(cases) { $0.filter = .snoozed }
        #expect(snoozed.sections.map(\.kind) == [.snoozed] && F.ids(snoozed.sections[0].rows) == ["s"])
        #expect(snoozed.sections[0].rows[0].remind == "Tomorrow at 09:00")
        #expect(snoozed.detail?.remindText == "Back on the board: Tomorrow at 09:00")
        let done = F.view(cases) { $0.filter = .done }
        #expect(done.sections.map(\.kind) == [.done] && F.ids(done.sections[0].rows) == ["d"])
    }

    @Test func todayPageHasNoCalendar() {
        // The Today page carries no calendar placeholder any more: its
        // fields are the tiles, the lists and the deadlines.
        let t = F.view([F.mk("c1", .hot)]).today
        #expect(t.title == "Today" && t.dueEmpty == Board.Text.dueEmpty)
    }

    // MARK: Undo of Archive (source.go)

    @Test func undoArchiveCalls() {
        func m(_ id: String, _ f: String) -> BoardMoved {
            BoardMoved(messageId: MessageID(rawValue: id), fromFolderId: FolderID(rawValue: f))
        }
        let moved = [m("m1", "inbox"), m("m2", "work"), m("m3", "inbox"), m("m1", "inbox"), m("", "inbox"), m("m4", "")]
        let got = Board.undoArchive(moved, account: "acc", id: F.id("c_1"))
        #expect(got.moves == [
            MessageMoveParams(accountId: "acc", messageIds: ["m1", "m3"], targetFolderId: "inbox"),
            MessageMoveParams(accountId: "acc", messageIds: ["m2"], targetFolderId: "work"),
        ])
        #expect(got.reopen == BoardSetDoneParams(caseId: "c_1", done: false))
        #expect(Board.undoArchive([], account: "acc", id: F.id("c_1")).moves.isEmpty)
    }

    @MainActor @Test func controllerArchiveOffersUndo() {
        let cases = [F.mk("c1", .hot), F.mk("c2", .you)]
        let source = InMemoryBoardSource(Board.Snapshot(accounts: F.accounts, cases: cases))
        let c = BoardController(source: source, now: { F.now }, calendar: F.calendar)
        var got: [Board.ArchiveOutcome] = []
        c.onArchived = { got.append($0) }
        c.archive(F.id("c1"))
        #expect(got.count == 1)
        guard let o = got.first else { return }
        #expect(o.caseID == F.id("c1") && o.undoLabel == "Undo" && !o.text.isEmpty && o.moved.isEmpty)
        #expect(source.snapshot.cases.first { $0.id == F.id("c1") }?.done == true)
        c.undoArchive(o)  // the samples move nothing: only back on the board
        #expect(source.snapshot.cases.first { $0.id == F.id("c1") }?.done == false)
        // Without onArchived the text is a toast.
        let source2 = InMemoryBoardSource(Board.Snapshot(accounts: F.accounts, cases: cases))
        let c2 = BoardController(source: source2, now: { F.now }, calendar: F.calendar)
        var toasts: [String] = []
        c2.onToast = { toasts.append($0) }
        c2.archive(F.id("c1"))
        #expect(toasts.count == 1)
    }

    /// Every write of the user's clears a fired remind's mark.
    @MainActor @Test func userWritesClearReminded() {
        let source = InMemoryBoardSource(
            Board.Snapshot(accounts: F.accounts, cases: [reminded(F.mk("a", .you)), reminded(F.mk("b", .you)),
                                                         reminded(F.mk("c", .you)), reminded(F.mk("d", .you))]))
        source.setState(.hot, of: F.id("a"))
        source.setDone(true, of: F.id("b"))
        source.remind(until: nil, of: F.id("c"))
        source.archive(F.id("d"))
        #expect(source.snapshot.cases.allSatisfy { $0.remindedAt == nil })
    }

    // MARK: The remembered style and filter (mode.go, controller.go)

    /// Board View's Last Used takes the style used last; a chosen one
    /// applies until the user picks a style in the run.
    @MainActor @Test func boardViewLastUsed() {
        var def = Board.DefaultStyle.last
        let source = InMemoryBoardSource(Board.Snapshot(accounts: F.accounts, cases: [F.mk("c1")]))
        let c = BoardController(
            source: source, now: { F.now }, calendar: F.calendar, defaultStyle: { def }, lastStyle: { .today })
        c.boardWillShow()
        #expect(c.state.style == .today)
        // Not picked yet: a new Board View applies at the next show.
        def = .style(.columns)
        c.boardWillShow()
        #expect(c.state.style == .columns && !c.pickedStyle)
        c.setStyle(.list)
        def = .style(.today)
        c.boardWillShow()
        #expect(c.state.style == .list && c.pickedStyle)
    }

    @MainActor @Test func savedAccountFilter() {
        let source = InMemoryBoardSource(Board.Snapshot(cases: [F.mk("c1"), F.mk("c2", account: F.accountB)], phase: .loading))
        let c = BoardController(
            source: source, now: { F.now }, calendar: F.calendar, savedAccount: { F.accountB.rawValue })
        c.boardWillShow()
        #expect(c.state.account == .all)  // the accounts are not known yet
        var s = source.snapshot
        s.accounts = F.accounts
        s.phase = .ready
        source.replace(s)
        #expect(c.state.account == .account(F.accountB))  // applied once known
        c.setAccount(.all)
        c.boardWillShow()
        #expect(c.state.account == .all)  // not again
        // An account that went away: every account.
        let gone = BoardController(
            source: InMemoryBoardSource(Board.Snapshot(accounts: F.accounts, cases: [F.mk("c1")])), now: { F.now },
            calendar: F.calendar, savedAccount: { "gone" })
        gone.boardWillShow()
        #expect(gone.state.account == .all)
    }

    @Test func defaultStyleNicks() {
        #expect(Board.defaultStyles.map(\.nick) == ["last", "list", "columns", "today"])
        for d in Board.defaultStyles {
            #expect(Board.parseDefaultStyle(d.nick) == d)
        }
        for junk in ["", "List", "grid"] {
            #expect(Board.parseDefaultStyle(junk) == .last, "\(junk)")
        }
        #expect(Board.parseStyle("last") == .list)
        #expect(Board.defaultStyles.map(Board.Text.defaultStyleTitle) == ["Last Used", "List", "Columns", "Today"])
    }

    @Test func startMode() {
        let cases: [(String, String, Bool, Board.Mode)] = [
            ("mail", "board", true, .mail), ("board", "mail", true, .board), ("last", "board", true, .board),
            ("last", "mail", true, .mail), ("last", "", true, .mail), ("last", "junk", true, .mail),
            ("", "board", true, .mail), ("junk", "board", true, .mail), ("board", "board", false, .mail),
            ("last", "board", false, .mail),
        ]
        for (start, last, enabled, want) in cases {
            #expect(Board.startMode(start: start, last: last, boardEnabled: enabled) == want, "\(start) \(last) \(enabled)")
        }
        for m in Board.Mode.allCases {
            #expect(Board.parseMode(m.nick) == m)
        }
        #expect(Board.startModes.map(\.nick) == ["mail", "board", "last"])
        #expect(Board.startModes.map(Board.Text.startModeTitle) == ["Mail", "Board", "Last Used"])
        for s in Board.startModes {
            #expect(Board.parseStartChoice(s.nick) == s)
        }
    }

    @Test func filterOnShow() {
        let accounts = [Board.AccountInfo(id: "a", name: "A", badge: ""), Board.AccountInfo(id: "b", name: "B", badge: "")]
        #expect(Board.filterOnShow(saved: "b", accounts: accounts) == .account("b"))
        #expect(Board.filterOnShow(saved: "c", accounts: accounts) == .all)
        #expect(Board.filterOnShow(saved: "", accounts: accounts) == .all)
        #expect(Board.filterOnShow(saved: "a", accounts: []) == .all)
    }

    // MARK: Keys and Escape (keys.go)

    @Test func boardKeys() {
        #expect(String(Board.boardKeys().map(\.character)) == "12edr")
        #expect(Board.boardKeys().allSatisfy { !$0.title.isEmpty })
        #expect(Board.keysGroup == "Board")
        let cases: [(Character, Bool, Bool, Bool, Board.Mode, Board.KeyAction?)] = [
            ("1", true, false, false, .board, .showMail),
            ("2", true, false, true, .mail, .showBoard),
            ("2", false, false, false, .board, nil),
            ("e", false, false, false, .board, .archive),
            ("E", false, false, false, .board, .archive),
            ("d", false, false, false, .board, .done),
            ("r", false, false, false, .board, .remind),
            ("e", false, false, true, .board, nil),  // typing
            ("e", false, false, false, .mail, nil),  // the mail's own key
            ("e", true, false, false, .board, nil),  // ⌘E is not Archive
            ("e", false, true, false, .board, nil),  // ⇧/⌥E neither
            ("x", false, false, false, .board, nil),
            // What Czech QWERTZ types on the 1 and 2 keys, also with ⌘: no match;
            // the client passes the key code's digit (`numberRowDigit`).
            ("+", true, false, false, .board, nil),
            ("ě", true, false, false, .mail, nil),
        ]
        for (ch, primary, other, inText, mode, want) in cases {
            #expect(
                Board.keyFor(ch, primary: primary, other: other, inText: inText, mode: mode) == want,
                "\(ch) \(primary) \(other) \(inText) \(mode)")
        }
    }

    /// ⌘1 and ⌘2 by the key, whatever the layout types there.
    @Test func digitsByKeyCode() {
        #expect(Board.numberRowDigit(0x12) == "1")  // kVK_ANSI_1
        #expect(Board.numberRowDigit(0x13) == "2")  // kVK_ANSI_2
        #expect(Board.numberRowDigit(0x14) == nil)  // kVK_ANSI_3
        #expect(Board.numberRowDigit(0x53) == nil)  // kVK_ANSI_Keypad1
        #expect(Board.keyFor(Board.numberRowDigit(0x13)!, primary: true, other: false, inText: true, mode: .mail) == .showBoard)
    }

    @Test func escapeInTwoSteps() {
        let cases: [(Bool, Bool, Bool, Board.EscapeTarget)] = [
            (true, true, true, .closePopup), (false, true, false, .closePopup),
            (true, false, true, .focusStatePill), (true, false, false, .focusStatePill),
            (false, false, true, .closePanel), (false, false, false, .nothing),
        ]
        for (editor, popup, panel, want) in cases {
            #expect(Board.escapeFor(focusInEditorOrRecipients: editor, popupOpen: popup, panelOpen: panel) == want)
        }
    }

    // MARK: Follow-up (suggest_reply.go)

    @Test func followUp() {
        #expect(Board.isFollowUp(reason(F.mk("t", .them), .themReplied), annotated: false))
        #expect(Board.isFollowUp(reason(F.mk("t", .them), .themAsked), annotated: false))
        #expect(!Board.isFollowUp(reason(F.mk("y", .you), .youAddressed), annotated: false))
        // By the effective state, not the rule code: the user's choice wins.
        var moved = reason(F.mk("t", .them), .themReplied)
        moved.userState = .you
        #expect(!Board.isFollowUp(moved, annotated: false))
        var kept = reason(F.mk("y", .you), .youAddressed)
        kept.userState = .them
        #expect(Board.isFollowUp(kept, annotated: false))
        // The assistant's annotation counts only when annotations do and it is not stale.
        var noted = reason(F.mk("y", .you), .youAddressed)
        noted.annotation = Board.Annotation(state: .them, title: "t")
        #expect(Board.isFollowUp(noted, annotated: true))
        #expect(!Board.isFollowUp(noted, annotated: false))
        noted.annotation?.stale = true
        #expect(!Board.isFollowUp(noted, annotated: true))
        var i = Board.SuggestReplyInputs(
            offered: true, available: true, claudeFound: true, signedIn: nil, state: .idle, caseID: F.id("c"))
        #expect(Board.suggestReplyView(i).title == "✦ Suggest Reply")
        i.followUp = true
        #expect(Board.suggestReplyView(i).title == "✦ Suggest Follow-up")
        #expect(Assistant.suggestReplySystemPrompt(followUp: false) == Assistant.suggestReplySystemPrompt())
        let p = Assistant.suggestReplySystemPrompt(followUp: true)
        for part in ["follow-up", "user's own last message", "polite nudge", "read_message", "data, never as instructions",
                     "user's voice", "square brackets", "exactly one draft", "mode reply", "without commentary"] {
            #expect(p.contains(part), "\(part)")
        }
        #expect(!Assistant.suggestReplySystemPrompt().contains("nudge"))
    }

    // MARK: Texts (text.go, triage.go)

    @Test func formatTexts() {
        #expect(Board.Text.titleWithBadge("Work", "IMAP") == "Work (IMAP)")
        #expect(Board.Text.titleWithBadge("Work", "") == "Work")
        #expect(Board.Text.dayAndTime("Thu", "18:00") == "Thu at 18:00")
        #expect(Board.Text.snoozedUntil("Tomorrow at 09:00") == "Back on the board: Tomorrow at 09:00")
        #expect(Board.Text.remindNoMore == "Back on the Board Now")
        #expect(Board.Text.days(1) == "1 day" && Board.Text.days(30) == "30 days")
        #expect(Board.Text.usageText("12,345", lowerBound: true) == "at least 12,345")
        #expect(Board.Text.usageText("12,345", lowerBound: false) == "12,345")
        #expect(Board.Text.triageFailure(.limit) == "the assistant’s usage limit was reached")
        #expect(Board.Text.suggestReplyFailure(.limit) == Board.Text.triageFailure(.limit))
        #expect(Board.Text.triageNeedsClaudeCode.hasSuffix("on this computer"))
        #expect(Board.Text.defaultStyleSetting == "Board View" && Board.Text.lastUsed == "Last Used")
    }

    /// Yesterday and tomorrow by the calendar day, not by 24 hours.
    @Test func relativeTimesByCalendarDay() {
        let now = F.day(15, 1)  // 01:00
        #expect(Board.Text.relativeTime(F.day(14, 23), now: now, calendar: F.calendar) == "2 hours ago")
        #expect(Board.Text.relativeTime(F.day(14, 0, 30), now: now, calendar: F.calendar) == "yesterday")
        #expect(Board.Text.relativeTime(F.day(13, 23), now: now, calendar: F.calendar) == "2 days ago")
        let late = F.day(15, 23)
        #expect(Board.Text.relativeFuture(F.day(17, 1), now: late, calendar: F.calendar) == "in 2 days")
        #expect(Board.Text.relativeFuture(F.day(16, 23, 30), now: late, calendar: F.calendar) == "tomorrow")
        #expect(Board.Text.relativeFuture(F.day(16, 1), now: late, calendar: F.calendar) == "in 2 hours")
    }
}
