// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

@Suite struct MailDateGroupsTests {
    private var calendar: Calendar {
        var value = Calendar(identifier: .gregorian)
        value.timeZone = TimeZone(identifier: "Europe/Prague")!
        value.firstWeekday = 2
        value.minimumDaysInFirstWeek = 4
        return value
    }

    private func date(_ value: String) -> Date {
        ISO8601DateFormatter().date(from: value)!
    }

    @Test func intervalsAreExclusiveAndNewestFirst() {
        let now = date("2026-10-22T10:00:00Z")
        let cases: [(String, MailDateGroup)] = [
            ("2026-10-22T00:00:00Z", .today),
            ("2026-10-21T10:00:00Z", .yesterday),
            ("2026-10-19T10:00:00Z", .thisWeek),
            ("2026-10-12T10:00:00Z", .lastWeek),
            ("2026-10-01T10:00:00Z", .thisMonth),
            ("2026-09-01T10:00:00Z", .lastMonth),
            ("2026-01-01T10:00:00Z", .thisYear),
            ("2025-12-01T10:00:00Z", .year(2025)),
            ("2024-12-01T10:00:00Z", .year(2024)),
        ]
        for (value, expected) in cases {
            #expect(MailDateGroup.containing(date(value), now: now, calendar: calendar) == expected)
        }
    }

    @Test func localMidnightAndDaylightSaving() {
        let now = date("2026-03-30T10:00:00Z")
        #expect(MailDateGroup.containing(date("2026-03-29T22:00:00Z"), now: now, calendar: calendar) == .today)
        #expect(MailDateGroup.containing(date("2026-03-29T21:59:59Z"), now: now, calendar: calendar) == .yesterday)
        #expect(MailDateGroup.containing(date("2026-03-28T23:00:00Z"), now: now, calendar: calendar) == .yesterday)
        #expect(MailDateGroup.containing(date("2026-03-28T22:59:59Z"), now: now, calendar: calendar) == .lastWeek)
    }

    @Test func weekUsesUsersCalendarAndCrossesYear() {
        let now = date("2027-01-04T10:00:00Z")
        #expect(MailDateGroup.containing(date("2026-12-31T10:00:00Z"), now: now, calendar: calendar) == .lastWeek)
        var sunday = calendar
        sunday.firstWeekday = 1
        let wednesday = date("2026-10-21T10:00:00Z")
        let previousSunday = date("2026-10-18T10:00:00Z")
        #expect(MailDateGroup.containing(previousSunday, now: wednesday, calendar: calendar) == .lastWeek)
        #expect(MailDateGroup.containing(previousSunday, now: wednesday, calendar: sunday) == .thisWeek)
    }

    private func row(_ id: String, _ timestamp: String, member isMember: Bool = false) -> ListRow {
        var message = member(id, "thread", 0, "sender")
        message.date = date(timestamp)
        return ListRow(key: ListKey(message: message.id), member: isMember, message: message)
    }

    @Test func collapseKeepsHeaderAndWholeConversationTogether() {
        let now = date("2026-10-22T10:00:00Z")
        let parent = row("parent", "2026-10-22T08:00:00Z")
        let child = row("child", "2025-01-01T10:00:00Z", member: true)
        let yesterday = row("yesterday", "2026-10-21T10:00:00Z")
        let rows = [parent, child, yesterday]
        #expect(MailDateGroups.items(rows: rows, now: now, calendar: calendar) == [
            .heading(.today), .message(parent.key), .message(child.key),
            .heading(.yesterday), .message(yesterday.key),
        ])
        #expect(MailDateGroups.items(rows: rows, collapsed: [.today], now: now, calendar: calendar) == [
            .heading(.today), .heading(.yesterday), .message(yesterday.key),
        ])
        #expect(MailDateGroups.items(rows: [], now: now, calendar: calendar).isEmpty)
    }

    @Test func paginationDoesNotRepeatHeadersAndMidnightMovesRows() {
        let now = date("2026-10-22T10:00:00Z")
        let rows = [row("a", "2026-10-22T08:00:00Z"), row("b", "2026-10-22T07:00:00Z")]
        #expect(MailDateGroups.items(rows: rows, now: now, calendar: calendar) == [
            .heading(.today), .message(rows[0].key), .message(rows[1].key),
        ])
        #expect(MailDateGroups.items(rows: rows, now: date("2026-10-23T10:00:00Z"), calendar: calendar).first == .heading(.yesterday))
    }

    @Test func flaggedRowsComeFirstWithoutDuplicatesAndMoveBackWhenUnflagged() {
        let now = date("2026-10-22T10:00:00Z")
        let today = row("today", "2026-10-22T08:00:00Z")
        var old = row("old", "2025-01-01T10:00:00Z")
        old.message.flags = [.flagged]
        #expect(MailDateGroups.items(rows: [today, old], now: now, calendar: calendar) == [
            .heading(.flagged), .message(old.key), .heading(.today), .message(today.key),
        ])
        #expect(MailDateGroups.items(rows: [today, old], collapsed: [.flagged], now: now, calendar: calendar) == [
            .heading(.flagged), .heading(.today), .message(today.key),
        ])
        old.message.flags = []
        #expect(MailDateGroups.items(rows: [today, old], now: now, calendar: calendar) == [
            .heading(.today), .message(today.key), .heading(.year(2025)), .message(old.key),
        ])
    }

    @Test func flaggedConversationUsesAggregateAndKeepsItsMembers() {
        let now = date("2026-10-22T10:00:00Z")
        let today = row("today", "2026-10-22T08:00:00Z")
        var parent = row("parent", "2026-10-21T10:00:00Z")
        parent.thread = true
        parent.summary = thr("conversation", 2, 0, parent.message, .flagged)
        let child = row("child", "2025-01-01T10:00:00Z", member: true)
        #expect(MailDateGroups.items(rows: [today, parent, child], now: now, calendar: calendar) == [
            .heading(.flagged), .message(parent.key), .message(child.key),
            .heading(.today), .message(today.key),
        ])
    }

    @Test func olderFlaggedPagesJoinTheFirstSectionInDateOrder() {
        let now = date("2026-10-22T10:00:00Z")
        var recent = row("recent", "2026-10-22T08:00:00Z")
        recent.message.flags = [.flagged]
        let today = row("today", "2026-10-22T07:00:00Z")
        var old = row("old", "2025-01-01T10:00:00Z")
        old.message.flags = [.flagged]
        #expect(MailDateGroups.items(rows: [recent, today, old], now: now, calendar: calendar) == [
            .heading(.flagged), .message(recent.key), .message(old.key),
            .heading(.today), .message(today.key),
        ])
    }

}
