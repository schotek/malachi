// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// What Mail shows of a message with a calendar part (docs/calendar.md §5.7):
// the list row's pill (`MessageSummary.invitation`), whether the reading
// pane, a message window or a conversation card shows the invitation card
// (`Message.invitation` of message.get, the account's `invitationCard`),
// the card's clock line with the repetition, and the geometry of its mini
// day strip. Swift-first beside `Cal` (CalendarInvitation.swift, the port of
// invitation.go): the GTK and Windows clients follow.

/// The pill of a list row: that of the message the row shows (on a
/// conversation row its newest folder member), whatever its state
/// (`Cal.pillKind`, Go `PillKind`).
public func invitationPill(_ s: MessageSummary) -> Cal.InvitationPill {
    Cal.pillKind(s.invitation)
}

/// The invitation card a message on display shows: the card of message.get
/// when `Cal.cardModeOf` gives one (`cardOn`: the account's "Show the
/// Invitation Card"); nil before message.get answered, for a message
/// without a calendar part, for an unreadable part (`invalid`: the message
/// reads as usual) and for kind other.
public func invitationReading(_ m: Message?, cardOn: Bool) -> InvitationCard? {
    guard let c = m?.invitation, Cal.cardModeOf(c, cardOn: cardOn) != .none else { return nil }
    return c
}

/// Whether the account `a` shows invitation cards (`CalendarConfig
/// .showsInvitationCard`; an account without a calendar part, or not known,
/// shows them).
public func showsInvitationCard(_ a: Account?) -> Bool {
    a?.config.calendar?.showsInvitationCard ?? true
}

extension Cal {
    /// The card's clock line with the repetition: "Tuesday 14 October 2026
    /// · 10:00–11:30 · 1 h 30 min · every 2 weeks".
    public static func cardClockLine(_ c: InvitationCard, env: Env) -> String {
        let line = cardTimeLine(c, env: env)
        guard let start = cardStart(c.summary, env.zone) else { return line }
        return Text.dots(line, recurrenceText(c.recurrence, start: start, env: env))
    }

    /// The geometry of the card's mini day strip (`InvitationCard.day`): the
    /// axis from `day.from` to `day.to`, its whole hours, the busy blocks
    /// and the invitation's own slot, as fractions of the axis (0 at the
    /// start, 1 at the end), clipped to it.
    public struct DayStrip: Sendable, Equatable {
        public struct Span: Sendable, Equatable {
            public var from: Double
            public var to: Double

            public init(from: Double, to: Double) {
                self.from = from
                self.to = to
            }
        }

        public struct Tick: Sendable, Equatable {
            public var at: Double
            /// "10:00" (`Text.time`).
            public var label: String

            public init(at: Double, label: String) {
                self.at = at
                self.label = label
            }
        }

        /// The whole hours on the axis, its ends included when whole.
        public var ticks: [Tick]
        /// The busy spans of the visible calendars, in time order.
        public var busy: [Span]
        /// The invitation's slot (nil: an all-day invitation, or outside the
        /// axis).
        public var proposed: Span?
        /// "10:00–11:30" for the slot ("" without one).
        public var proposedLabel: String

        /// At most this many busy blocks are drawn (the daemon caps them
        /// too; a hostile part must not make the card draw thousands).
        public static let maxBusy = 48
        /// No more ticks than this; a longer axis labels every second hour,
        /// and so on.
        public static let maxTicks = 13

        public init(ticks: [Tick], busy: [Span], proposed: Span?, proposedLabel: String) {
            self.ticks = ticks
            self.busy = busy
            self.proposed = proposed
            self.proposedLabel = proposedLabel
        }
    }

    /// The strip of `c`; nil when `day` spans nothing (an unreadable part,
    /// or a span longer than two days, which is not a day).
    public static func dayStripOf(_ c: InvitationCard, env: Env) -> DayStrip? {
        let from = c.day.from
        let to = c.day.to
        let length = to.timeIntervalSince(from)
        guard length >= 3600, length <= 2 * 86400 else { return nil }
        func frac(_ t: Date) -> Double { min(1, max(0, t.timeIntervalSince(from) / length)) }
        func span(_ s: Date, _ e: Date) -> DayStrip.Span? {
            guard e > s, e > from, s < to else { return nil }
            return DayStrip.Span(from: frac(s), to: frac(e))
        }

        var cal = Foundation.Calendar(identifier: .gregorian)
        cal.timeZone = env.zone
        var hours: [Date] = []
        var t = cal.dateInterval(of: .hour, for: from)?.start ?? from
        if t < from {
            t = t.addingTimeInterval(3600)
        }
        while t <= to, hours.count <= 48 {
            hours.append(t)
            t = t.addingTimeInterval(3600)
        }
        let step = max(1, (hours.count + DayStrip.maxTicks - 1) / DayStrip.maxTicks)
        let ticks = hours.enumerated().filter { $0.offset % step == 0 }.map {
            DayStrip.Tick(at: frac($0.element), label: Text.time($0.element, env))
        }

        let busy = c.day.busy.sorted { $0.start < $1.start }.prefix(DayStrip.maxBusy).compactMap { span($0.start, $0.end) }
        var proposed: DayStrip.Span?
        var label = ""
        if !c.allDay, let s = c.start, let e = c.end, let p = span(s, e) {
            proposed = p
            label = Text.timeRange(s, e, env)
        }
        return DayStrip(ticks: ticks, busy: busy, proposed: proposed, proposedLabel: label)
    }
}
