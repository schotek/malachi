// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The editor's availability rows (Go availability.go; calendar.freeBusy,
// Graph only): per person the busy spans on the editor's day axis,
// "availability unknown", the summary line of the proposed time and Next
// Free Slot (calendar.findFreeTime).

import Foundation

extension Cal {
    /// availability.go `AxisSpan`: fractions 0–1 of the axis.
    public struct AxisSpan: Sendable, Equatable {
        public var from: Double
        public var to: Double
        public var showAs: ShowAs

        public init(from: Double, to: Double, showAs: ShowAs) {
            self.from = from
            self.to = to
            self.showAs = showAs
        }
    }

    /// availability.go `AvailabilityRow`.
    public struct AvailabilityRow: Sendable, Equatable {
        /// "You" or the attendee's name (else address).
        public var label: String
        public var address: String
        public var known: Bool
        public var busy: [AxisSpan]
    }

    /// availability.go `Busy`: busy, out of office, tentative and unknown
    /// block a meeting; free and working elsewhere do not.
    public static func busy(_ s: ShowAs) -> Bool {
        s != .free && s != .workingElsewhere
    }

    static func axisSpans(_ busy: [BusyInterval], from: Date, to: Date) -> [AxisSpan] {
        let total = to.timeIntervalSince(from)
        guard total > 0 else { return [] }
        var out: [AxisSpan] = []
        for b in busy {
            guard Cal.busy(b.showAs), b.end > from, b.start < to, b.end > b.start else { continue }
            let s = max(b.start, from)
            let e = min(b.end, to)
            out.append(AxisSpan(
                from: s.timeIntervalSince(from) / total, to: e.timeIntervalSince(from) / total, showAs: b.showAs))
        }
        return stableSorted(out) { compare($0.from, $1.from) }
    }

    /// availability.go `AvailabilityRows`: the user first, then the
    /// attendees in the order asked; `names` maps an address to the name
    /// the recipient field shows.
    public static func availabilityRows(
        _ res: CalendarFreeBusyResult, names: [String: String], from: Date, to: Date
    ) -> [AvailabilityRow] {
        var rows = [AvailabilityRow(label: you, address: res.me.address, known: true, busy: axisSpans(res.me.busy, from: from, to: to))]
        for a in res.attendees {
            var label = cleanLine(names[a.address])
            if label.isEmpty {
                label = cleanLine(a.address)
            }
            rows.append(AvailabilityRow(
                label: label, address: a.address, known: a.known, busy: a.known ? axisSpans(a.busy, from: from, to: to) : []))
        }
        return rows
    }

    /// The user's own row.
    public static var you: String { L10n.T("You") }

    /// The row text of a person without free/busy.
    public static var unknown: String { L10n.T("availability unknown") }

    static func busyDuring(_ busy: [BusyInterval], _ start: Date, _ end: Date) -> Bool {
        busy.contains { Cal.busy($0.showAs) && $0.start < end && $0.end > start }
    }

    /// availability.go `Summary`: "10:00–11:00: all known attendees are
    /// free" or "2 of 3 attendees are busy", then the unknown count; empty
    /// without attendees.
    public static func summary(_ res: CalendarFreeBusyResult, start: Date, end: Date, env: Env) -> [String] {
        var known = 0
        var busyCount = 0
        var unknownCount = 0
        for a in res.attendees {
            if !a.known {
                unknownCount += 1
                continue
            }
            known += 1
            if busyDuring(a.busy, start, end) {
                busyCount += 1
            }
        }
        var lines: [String] = []
        if known > 0 && busyCount == 0 {
            lines.append(L10n.T("%s: all known attendees are free", Text.timeRange(start, end, env)))
        } else if known > 0 {
            lines.append(L10n.N("%d of %d attendees is busy", "%d of %d attendees are busy", busyCount, busyCount, known))
        }
        if unknownCount > 0 {
            lines.append(L10n.N(
                "Availability of %d attendee is unknown", "Availability of %d attendees is unknown", unknownCount))
        }
        return lines
    }

    /// availability.go `NextFreeSlot`: the first slot that starts at or
    /// after `after`.
    public static func nextFreeSlot(_ res: CalendarFindFreeTimeResult, after: Date) -> TimeSlot? {
        var best: TimeSlot?
        for s in res.slots where s.start >= after && s.end > s.start {
            if best == nil || s.start < best!.start {
                best = s
            }
        }
        return best
    }

    public static var nextFreeSlotButton: String { L10n.T("Next Free Slot") }

    /// "Next free slot: Wednesday 15 October 2026 · 14:00–15:00".
    public static func nextFreeSlotText(_ s: TimeSlot, env: Env) -> String {
        L10n.T("Next free slot: %s", Text.dots(Text.longDate(s.start, env), Text.timeRange(s.start, s.end, env)))
    }

    public static var noFreeSlot: String { L10n.T("No free slot was found in the next two weeks.") }
}
