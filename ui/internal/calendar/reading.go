// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendar

import (
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// What Mail shows of a message with a calendar part (docs/calendar.md
// §5.7): the list row's pill, whether the reading pane, a message window
// or a conversation card shows the invitation card, the card's clock line
// with the repetition, and the geometry of its mini day strip. Port of
// macos MalachiCore/Model/InvitationReading.swift.

// InvitationPillOf is the pill of a list row: that of the message the row
// shows (on a conversation row its newest folder member), whatever its
// state. Swift: invitationPill.
func InvitationPillOf(s api.MessageSummary) InvitationPill { return PillKind(s.Invitation) }

// InvitationReading is the invitation card a message on display shows:
// the card of message.get when CardModeOf gives one (cardOn: the
// account's "Show the Invitation Card"); nil before message.get answered,
// for a message without a calendar part, for an unreadable part (the
// message reads as usual) and for kind other. Swift: invitationReading.
func InvitationReading(m *api.Message, cardOn bool) *api.InvitationCard {
	if m == nil || CardModeOf(m.Invitation, cardOn) == CardNone {
		return nil
	}
	return m.Invitation
}

// ShowsInvitationCard reports whether the account a shows invitation
// cards (an account without a calendar part, or not known, shows them).
// Swift: showsInvitationCard.
func ShowsInvitationCard(a *api.Account) bool {
	if a == nil || a.Config.Calendar == nil {
		return true
	}
	return a.Config.Calendar.ShowsInvitationCard()
}

// CardClockLine is the card's clock line with the repetition: "Tuesday 14
// October 2026 · 10:00–11:30 · 1 h 30 min · every 2 weeks". Swift:
// Cal.cardClockLine.
func CardClockLine(c api.InvitationCard, env Env) string {
	line := CardTimeLine(c, env)
	start, ok := cardStart(c.InvitationSummary, env.loc())
	if !ok {
		return line
	}
	return dots(env.Tr, line, RecurrenceText(c.Recurrence, start, env))
}

// The limits of the day strip.
const (
	// MaxStripBusy: at most this many busy blocks are drawn (the daemon
	// caps them too; a hostile part must not make the card draw thousands).
	MaxStripBusy = 48
	// MaxStripTicks: no more ticks than this; a longer axis labels every
	// second hour, and so on.
	MaxStripTicks = 13
)

// StripSpan is a span of the strip's axis, as fractions of it (0 at the
// start, 1 at the end).
type StripSpan struct{ From, To float64 }

// StripTick is a whole hour on the axis.
type StripTick struct {
	At float64
	// Label is "10:00" (TimeText).
	Label string
}

// DayStrip is the geometry of the card's mini day strip
// (InvitationCard.Day): the axis from Day.From to Day.To, its whole hours,
// the busy blocks and the invitation's own slot, clipped to the axis.
// Swift: Cal.DayStrip.
type DayStrip struct {
	// Ticks are the whole hours on the axis, its ends included when whole.
	Ticks []StripTick
	// Busy are the busy spans of the visible calendars, in time order.
	Busy []StripSpan
	// Proposed is the invitation's slot (nil: an all-day invitation, or
	// outside the axis).
	Proposed *StripSpan
	// ProposedLabel is "10:00–11:30" for the slot ("" without one).
	ProposedLabel string
}

// DayStripOf is the strip of c; nil when Day spans nothing (an unreadable
// part, or a span longer than two days, which is not a day). Swift:
// Cal.dayStripOf.
func DayStripOf(c api.InvitationCard, env Env) *DayStrip {
	from, to := c.Day.From, c.Day.To
	length := to.Sub(from).Seconds()
	if length < 3600 || length > 2*86400 {
		return nil
	}
	frac := func(t time.Time) float64 { return min(1, max(0, t.Sub(from).Seconds()/length)) }
	span := func(s, e time.Time) (StripSpan, bool) {
		if !e.After(s) || !e.After(from) || !s.Before(to) {
			return StripSpan{}, false
		}
		return StripSpan{frac(s), frac(e)}, true
	}

	// The hours are those of the zone (its clock hour starts).
	f := from.In(env.loc())
	t := time.Date(f.Year(), f.Month(), f.Day(), f.Hour(), 0, 0, 0, env.loc())
	if t.Before(from) {
		t = t.Add(time.Hour)
	}
	var hours []time.Time
	for !t.After(to) && len(hours) <= 48 {
		hours = append(hours, t)
		t = t.Add(time.Hour)
	}
	step := max(1, (len(hours)+MaxStripTicks-1)/MaxStripTicks)
	s := &DayStrip{Ticks: []StripTick{}, Busy: []StripSpan{}}
	for i, h := range hours {
		if i%step == 0 {
			s.Ticks = append(s.Ticks, StripTick{At: frac(h), Label: TimeText(h, env)})
		}
	}

	busy := slices.Clone(c.Day.Busy)
	slices.SortStableFunc(busy, func(a, b api.DayBusy) int { return a.Start.Compare(b.Start) })
	if len(busy) > MaxStripBusy {
		busy = busy[:MaxStripBusy]
	}
	for _, b := range busy {
		if sp, ok := span(b.Start, b.End); ok {
			s.Busy = append(s.Busy, sp)
		}
	}
	if !c.AllDay && c.Start != nil && c.End != nil {
		if sp, ok := span(*c.Start, *c.End); ok {
			s.Proposed = &sp
			s.ProposedLabel = TimeRangeText(*c.Start, *c.End, env)
		}
	}
	return s
}
