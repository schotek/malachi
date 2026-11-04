// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/toolbar toolbar_test.go, ported.
struct ToolbarTests {
    private func ids(_ s: String) -> Toolbar.Layout {
        s.split(separator: " ").map { Toolbar.ID(String($0)) }
    }

    @Test func defaultsAreNormal() {
        for bar in Toolbar.Bar.allCases {
            let d = Toolbar.defaultLayout(bar)
            #expect(Toolbar.normalize(bar, Toolbar.tokens(d)) == d, "\(bar)")
            for id in d where !id.isSpace && !id.isSeparator {
                #expect(Toolbar.lookup(bar, id) != nil, "\(bar): \(id)")
            }
        }
    }

    @Test func defaultsAreTheToolbarsBeforeCustomization() {
        #expect(Toolbar.defaultLayout(.mail) == ids(
            "mode flexibleSpace |list toggleSidebar newMessage contacts filter refresh flexibleSpace "
                + "|message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions search"))
        #expect(Toolbar.defaultLayout(.boardFlow) == ids("mode boardStyle contacts flexibleSpace boardAccount boardTriage"))
    }

    @Test func newItemsAreOnlyInThePalette() {
        let d = Toolbar.defaultLayout(.mail)
        for id: Toolbar.ID in [.markRead, .junk, .moveTo, .print, .undo, .loadPictures] {
            #expect(!d.contains(id), "\(id)")
            #expect(Toolbar.lookup(.mail, id) != nil, "\(id)")
        }
    }

    @Test func emptyIsTheDefault() {
        #expect(Toolbar.normalize(.mail, []) == Toolbar.defaultLayout(.mail))
        #expect(Toolbar.stored(.mail, Toolbar.defaultLayout(.mail)).isEmpty)
    }

    @Test(arguments: [
        // Unknown and duplicate items go.
        (Toolbar.Bar.mail, "mode |list refresh bogus refresh |message reply reply junk",
         "mode |list refresh |message reply junk"),
        // A missing fixed item and missing separators come back.
        (.mail, "reply", "mode |list |message reply"),
        // A message action in the list section goes to its home.
        (.mail, "mode |list archive refresh |message reply", "mode |list refresh |message reply archive"),
        // A list tool may stand in the sidebar section.
        (.mail, "mode refresh |list |message", "mode refresh |list |message"),
        // A fixed item moved elsewhere goes back to the start.
        (.mail, "flexibleSpace |list newMessage mode |message", "mode flexibleSpace |list newMessage |message"),
        // Separators out of order and twice are ignored; refresh in the
        // message section goes home to the list.
        (.mail, "mode |message reply |list refresh |message forward", "mode |list refresh |message reply forward"),
        // Spaces stand any number of times.
        (.mail, "mode |list space space |message flexibleSpace flexibleSpace",
         "mode |list space space |message flexibleSpace flexibleSpace"),
        // A board detail action goes to the detail section.
        (.boardList, "mode |list boardDone |detail boardReply", "mode |list |detail boardReply boardDone"),
        // A separator of another toolbar is unknown.
        (.boardFlow, "|list mode contacts |detail boardStyle", "mode contacts boardStyle"),
        (.calendar, "mode |main calendarToday print", "mode |main calendarToday print"),
        // A mail item in the calendar is unknown.
        (.calendar, "mode |main reply calendarToday", "mode |main calendarToday"),
    ])
    func normalize(bar: Toolbar.Bar, stored: String, want: String) {
        let got = Toolbar.normalize(bar, Toolbar.tokens(ids(stored)))
        #expect(got == ids(want), "\(stored)")
        #expect(Toolbar.normalize(bar, Toolbar.tokens(got)) == got, "idempotent")
    }

    @Test func normalizeIsBounded() {
        let stored = [String](repeating: "space", count: 10 * Toolbar.maxTokens)
        #expect(Toolbar.normalize(.mail, stored).count <= Toolbar.maxTokens + Toolbar.sections(.mail).count)
    }

    @Test func canInsert() {
        let cases: [(Toolbar.Bar, Toolbar.ID, Toolbar.Section, Bool)] = [
            (.mail, .reply, .message, true),
            (.mail, .reply, .list, false),
            (.mail, .refresh, .sidebar, true),
            (.mail, .refresh, .message, false),
            (.mail, .space, .sidebar, true),
            (.mail, .flexibleSpace, .list, true),
            (.mail, .mode, .sidebar, false),
            (.mail, Toolbar.separator(.list), .list, false),
            (.mail, .boardDone, .message, false),
            (.boardList, .boardDone, .detail, true),
            (.boardList, .boardDone, .list, false),
            (.calendar, .print, .main, true),
        ]
        for (bar, id, s, want) in cases {
            #expect(Toolbar.canInsert(bar, id, s) == want, "\(bar) \(id) \(s)")
        }
    }

    @Test func immovable() {
        #expect(Toolbar.immovable(.mail) == [.mode, Toolbar.separator(.list), Toolbar.separator(.message)])
        #expect(Toolbar.immovable(.boardFlow) == [.mode])
    }

    @Test func sectionAt() {
        let l = Toolbar.defaultLayout(.mail)
        let want: [Toolbar.ID: Toolbar.Section] = [
            .mode: .sidebar, Toolbar.separator(.list): .list, .refresh: .list, .reply: .message, .search: .message,
        ]
        for (i, id) in l.enumerated() {
            if let s = want[id] {
                #expect(Toolbar.sectionAt(.mail, l, i) == s, "\(id)")
            }
        }
        #expect(Toolbar.sectionAt(.boardFlow, Toolbar.defaultLayout(.boardFlow), 3) == .main)
    }

    @Test func allowedLeavesOutMissingFeatures() {
        let got = Set(Toolbar.allowed(.mail, Toolbar.Features(have: [.assistant])))
        #expect(!got.contains(.crm) && !got.contains(.print))
        #expect(got.contains(.assistant) && got.contains(.reply) && got.contains(.mode))
    }

    @Test func presentHidesAndKeepsPlaces() {
        let l = Toolbar.defaultLayout(.mail)
        let got = Toolbar.present(.mail, l, Toolbar.Features(have: [.assistant]))
        #expect(got.map(\.id) == l)
        for e in got {
            #expect(e.hidden == (e.id == .crm), "\(e.id)")
        }
    }

    @Test func presentLeavesOutSeparatorsAndSections() {
        // The list pane collapsed: its separator goes, its items stay.
        #expect(Toolbar.present(.mail, ids("mode |list refresh |message reply"),
                                Toolbar.Features(hiddenSeparators: [Toolbar.separator(.list)]))
            == [Toolbar.Shown(.mode), Toolbar.Shown(.refresh), Toolbar.Shown(Toolbar.separator(.message)),
                Toolbar.Shown(.reply)])
        // The Board List without a case: the detail section goes whole.
        #expect(Toolbar.present(.boardList, ids("mode |list boardStyle |detail boardDone boardReply"),
                                Toolbar.Features(hiddenSections: [.detail]))
            == [Toolbar.Shown(.mode), Toolbar.Shown(Toolbar.separator(.list)), Toolbar.Shown(.boardStyle)])
    }

    @Test func missingActions() {
        let l = ids("mode |list |message reply archive crm")
        #expect(Toolbar.missingActions(.mail, l, Toolbar.Features(have: [.crm]))
            == [.replyAll, .forward, .trash, .star])
        #expect(!Toolbar.missingActions(.mail, l, Toolbar.Features()).contains(.crm))
        #expect(Toolbar.missingActions(.boardList, Toolbar.defaultLayout(.boardList), Toolbar.Features()).isEmpty)
    }

    @Test func searchIndex() {
        #expect(Toolbar.searchIndex(.mail, Toolbar.defaultLayout(.mail)) == nil)
        let mail = ids("mode |list refresh |message reply")
        #expect(Toolbar.searchIndex(.mail, mail) == mail.count)
        let cal = ids("mode |main calendarToday")
        #expect(Toolbar.searchIndex(.calendar, cal) == cal.count)
        #expect(Toolbar.searchIndex(.boardFlow, Toolbar.defaultLayout(.boardFlow)) == nil)
    }

    @Test func displayModeAndKeys() {
        let cases: [(String, Toolbar.DisplayMode)] = [
            ("icon", .icon), ("icon-and-label", .iconAndLabel), ("label", .label), ("", .icon), ("LABEL", .icon),
        ]
        for (s, want) in cases {
            #expect(Toolbar.parseDisplayMode(s) == want, "\(s)")
        }
        #expect(Toolbar.Bar.allCases.map(Toolbar.key)
            == ["toolbar-mail", "toolbar-board-list", "toolbar-board-flow", "toolbar-calendar"])
    }

    @Test func catalogsAreConsistent() {
        for bar in Toolbar.Bar.allCases {
            var seen = Set<Toolbar.ID>()
            let secs = Set(Toolbar.sections(bar))
            for it in Toolbar.catalog(bar) {
                #expect(seen.insert(it.id).inserted, "\(bar): \(it.id) twice")
                #expect(!it.sections.isEmpty)
                #expect(Set(it.sections).isSubset(of: secs), "\(bar): \(it.id)")
                #expect(!it.id.isSpace && !it.id.isSeparator)
            }
        }
    }
}
