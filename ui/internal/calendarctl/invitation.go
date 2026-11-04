// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package calendarctl

import (
	"reflect"
	"slices"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
)

// The invitation card of one message in Mail (docs/calendar.md §5.7), the
// port of InvitationController.swift: the card of Message.Invitation
// (message.get), which card it is (calendar.CardModeOf with the account's
// switch), its lines, the target calendar ("Add to:"), and its actions:
// Accept / Tentative / Decline (and Decline Without Reply), Change
// Response, Propose New Time…, Remove from Calendar of a cancellation
// (invitation.applyCancel), Show in Calendar and Join Video Call
// (hand-offs). The answer's card replaces the shown one; an error shows
// inline. An answer given in another client (D29) and the user's own
// event (D32) come as the card's state; an update's former values are
// one line each (PreviousLines, D30).

// TargetChoice is a choice of the "Add to:" popup.
type TargetChoice struct {
	ID api.CalendarID
	// Title is "Account · Calendar".
	Title string
	// Color is the calendar's colour as the source has it (the CSS checks
	// it).
	Color     string
	IsDefault bool
}

// InvitationController is the controller of one invitation card. Every
// method runs on the main loop.
type InvitationController struct {
	source    calendardata.DataSource
	opts      Options
	accountID api.AccountID
	messageID api.MessageID
	cardOn    bool

	// OnChange: anything shown changed.
	OnChange func()
	// OnShowInCalendar: Calendar mode, the Day view of day with the event
	// selected; day nil when the card has no start.
	OnShowInCalendar func(eventID api.EventID, rec api.DateOrTime, day *time.Time)
	// OnOpenLink: Join Video Call, through the link confirmation.
	OnOpenLink func(url string)

	card          api.InvitationCard
	target        api.CalendarID
	busy          bool
	err           string
	cancelApplied bool
	movedTo       api.FolderID
	closed        bool
}

// NewInvitationController is the card of message messageID of account
// accountID; card is message.get's Invitation, cardOn the account's "Show
// the Invitation Card above the Message in Mail"
// (calendar.ShowsInvitationCard).
func NewInvitationController(src calendardata.DataSource, opts Options, accountID api.AccountID, messageID api.MessageID,
	card api.InvitationCard, cardOn bool) *InvitationController {
	return &InvitationController{source: src, opts: opts, accountID: accountID, messageID: messageID, cardOn: cardOn,
		card: card, target: DefaultInvitationTarget(card)}
}

// DefaultInvitationTarget is the default "Add to:" calendar: the card's linked
// calendar, else the default of the targets, else the first; "" none.
func DefaultInvitationTarget(c api.InvitationCard) api.CalendarID {
	if c.CalendarID != "" && (c.FixedTarget || slices.ContainsFunc(c.Targets, func(t api.InvitationTarget) bool {
		return t.CalendarID == c.CalendarID
	})) {
		return c.CalendarID
	}
	for _, t := range c.Targets {
		if t.IsDefault {
			return t.CalendarID
		}
	}
	if len(c.Targets) > 0 {
		return c.Targets[0].CalendarID
	}
	return ""
}

// Stop: the card went away; no callback runs any more.
func (c *InvitationController) Stop() { c.closed = true }

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

// AccountID is the message's account.
func (c *InvitationController) AccountID() api.AccountID { return c.accountID }

// MessageID is the message.
func (c *InvitationController) MessageID() api.MessageID { return c.messageID }

// Card is the card shown.
func (c *InvitationController) Card() api.InvitationCard { return c.card }

// Target is the "Add to:" calendar ("" the answer only).
func (c *InvitationController) Target() api.CalendarID { return c.target }

// Busy: an answer is on its way; the buttons wait.
func (c *InvitationController) Busy() bool { return c.busy }

// Error is the inline error ("" none).
func (c *InvitationController) Error() string { return c.err }

// CancelApplied: invitation.applyCancel removed the event.
func (c *InvitationController) CancelApplied() bool { return c.cancelApplied }

// MovedTo is the folder invitation.respond moved the message to (After
// Answering); "" none.
func (c *InvitationController) MovedTo() api.FolderID { return c.movedTo }

// ---------------------------------------------------------------------------
// Derived
// ---------------------------------------------------------------------------

func (c *InvitationController) tr() calendar.Translator { return c.opts.Env.Tr }

// Mode is which card shows.
func (c *InvitationController) Mode() calendar.CardMode {
	return calendar.CardModeOf(&c.card, c.cardOn)
}

// Pill is the card's pill.
func (c *InvitationController) Pill() calendar.InvitationPill {
	s := c.card.InvitationSummary
	return calendar.PillKind(&s)
}

// PillTitle is the pill's text.
func (c *InvitationController) PillTitle() string { return calendar.PillTitle(c.Pill(), c.tr()) }

// Title is the bold title.
func (c *InvitationController) Title() string {
	return calendar.CardTitle(c.card.InvitationSummary, c.tr())
}

// DateTile is the date tile; false without a start.
func (c *InvitationController) DateTile() (calendar.DateTile, bool) {
	return calendar.DateTileOf(c.card.InvitationSummary, c.opts.Env)
}

// TimeLine is the clock line.
func (c *InvitationController) TimeLine() string { return calendar.CardTimeLine(c.card, c.opts.Env) }

// ClockLine is the clock line with the repetition.
func (c *InvitationController) ClockLine() string { return calendar.CardClockLine(c.card, c.opts.Env) }

// Location is the place, one clean line.
func (c *InvitationController) Location() string { return cardLine(c.card.Location) }

// VideoHost is the video call's host ("" none); Join Video Call opens it.
func (c *InvitationController) VideoHost() string {
	if c.card.VideoCall == nil {
		return ""
	}
	return cardLine(c.card.VideoCall.Host)
}

// PeopleLine is "Organiser Jana Nováková · 4 attendees".
func (c *InvitationController) PeopleLine() string { return calendar.CardPeopleLine(c.card, c.tr()) }

// PreviousLines are an update's former values, one line each (D30).
func (c *InvitationController) PreviousLines() []string {
	return calendar.PreviousLines(c.card, c.opts.Env)
}

// ConflictLine is the conflict line, else "No conflicts in your
// calendars".
func (c *InvitationController) ConflictLine() string {
	s := c.source.Snapshot()
	if line := calendar.ConflictLine(c.card.Conflicts, s.CalendarName, c.opts.Env); line != "" {
		return line
	}
	return calendar.NoConflict(c.tr())
}

// Day is the day strip's span and busy blocks.
func (c *InvitationController) Day() api.InvitationDay { return c.card.Day }

// DayStrip is the day strip's geometry (nil none).
func (c *InvitationController) DayStrip() *calendar.DayStrip {
	return calendar.DayStripOf(c.card, c.opts.Env)
}

// ShowsResponses reports Accept / Tentative / Decline.
func (c *InvitationController) ShowsResponses() bool { return calendar.ShowsResponses(c.card) }

// ShowsStateStrip reports the strip after answering (here or, D29,
// elsewhere).
func (c *InvitationController) ShowsStateStrip() bool { return calendar.ShowsStateStrip(c.card) }

// ShowsProposeNewTime reports Propose New Time….
func (c *InvitationController) ShowsProposeNewTime() bool {
	return calendar.ShowsProposeNewTime(c.card)
}

// ShowsInCalendar reports Show in Calendar (an answered card or the
// user's own event, linked to a stored event).
func (c *InvitationController) ShowsInCalendar() bool { return calendar.ShowsInCalendar(c.card) }

// TargetIsPopup: "Add to:" is a popup (false: a label with the fixed
// calendar, or nothing without targets).
func (c *InvitationController) TargetIsPopup() bool {
	return !c.card.FixedTarget && len(c.card.Targets) > 1
}

// Targets are the "Add to:" choices.
func (c *InvitationController) Targets() []TargetChoice {
	s := c.source.Snapshot()
	out := make([]TargetChoice, 0, len(c.card.Targets))
	for _, t := range c.card.Targets {
		k, ok := s.Calendar(t.CalendarID)
		account, name, color := "", "", ""
		if ok {
			account, name, color = s.AccountName(k.AccountID), cardLine(k.Name), k.Color
		}
		out = append(out, TargetChoice{ID: t.CalendarID, Title: calendar.Dot(account, name, c.tr()), Color: color,
			IsDefault: t.IsDefault})
	}
	return out
}

// ReplyNote is the note under the buttons.
func (c *InvitationController) ReplyNote() []string {
	return calendar.ReplyNote(c.card, c.source.Snapshot().AccountName(c.replyAccount()), c.tr())
}

// StateStrip is "Accepted · in calendar Work · Calendar".
func (c *InvitationController) StateStrip() string {
	name := ""
	if id := c.card.CalendarID; id != "" {
		s := c.source.Snapshot()
		acc := c.accountID
		if k, ok := s.Calendar(id); ok {
			acc = k.AccountID
		}
		name = calendar.Dot(s.AccountName(acc), s.CalendarName(id), c.tr())
	}
	return calendar.StateStrip(c.card, name, c.tr())
}

// StateLine is the line of an invitation that cannot be answered, "You
// organise this event." first (D32); "" none.
func (c *InvitationController) StateLine() string { return calendar.StateLine(c.card, c.tr()) }

// ReplyCard is a reply's one-line card.
func (c *InvitationController) ReplyCard() string { return calendar.ReplyCard(c.card, c.tr()) }

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

// SetTarget chooses the "Add to:" calendar (one of the targets).
func (c *InvitationController) SetTarget(id api.CalendarID) {
	if c.card.FixedTarget || id == c.target || !slices.ContainsFunc(c.card.Targets, func(t api.InvitationTarget) bool {
		return t.CalendarID == id
	}) {
		return
	}
	c.target = id
	c.changed()
}

// Respond is Accept / Tentative / Decline and Change Response;
// withoutReply is Decline Without Reply.
func (c *InvitationController) Respond(r api.EventResponse, withoutReply bool) {
	if c.busy || !c.card.CanRespond {
		return
	}
	c.run(r, withoutReply, nil, nil)
}

// ProposeNewTime is Propose New Time…: the answer Tentative with the
// proposed start and end.
func (c *InvitationController) ProposeNewTime(start, end time.Time) {
	if c.busy || !c.card.CanProposeNewTime || !end.After(start) {
		return
	}
	s, e := start, end
	c.run(api.ResponseTentative, false, &s, &e)
}

func (c *InvitationController) run(r api.EventResponse, withoutReply bool, start, end *time.Time) {
	p := api.InvitationRespondParams{AccountID: c.accountID, MessageID: c.messageID, Response: r,
		ProposedStart: start, ProposedEnd: end}
	if withoutReply {
		f := false
		p.SendResponse = &f
	} else {
		m := calendar.MailTexts(calendar.MailKindOf(r), cardOccurrence(c.card), c.source.Snapshot().MyName(c.replyAccount()),
			c.opts.Env)
		p.Mail = &m
	}
	if r != api.ResponseDeclined && !c.card.FixedTarget {
		p.CalendarID = c.target
	}
	c.busy = true
	c.err = ""
	c.changed()
	c.source.RespondToInvitation(p, func(res *api.InvitationRespondResult, err *api.Error) {
		switch {
		case err != nil:
			c.err = c.opts.ErrorMessage(err, "")
		case res != nil:
			c.card = res.Invitation
			c.movedTo = res.MovedTo
			if res.Invitation.CalendarID != "" {
				c.target = res.Invitation.CalendarID
			}
		}
		c.busy = false
		c.changed()
	})
}

// ApplyCancel is Remove from Calendar of a cancellation.
func (c *InvitationController) ApplyCancel() {
	if c.busy || c.Mode() != calendar.CardCancelled || c.cancelApplied {
		return
	}
	c.busy = true
	c.err = ""
	c.changed()
	p := api.InvitationApplyCancelParams{AccountID: c.accountID, MessageID: c.messageID}
	c.source.ApplyCancel(p, func(_ *api.InvitationApplyCancelResult, err *api.Error) {
		if err != nil {
			c.err = c.opts.ErrorMessage(err, "")
		} else {
			c.cancelApplied = true
		}
		c.busy = false
		c.changed()
	})
}

// ShowInCalendar hands the linked event and its day to Calendar mode.
func (c *InvitationController) ShowInCalendar() {
	if c.OnShowInCalendar == nil {
		return
	}
	var day *time.Time
	if c.card.Start != nil {
		t := *c.card.Start
		day = &t
	} else if t, ok := calendar.ParseDate(c.card.StartDate, c.opts.Loc()); ok {
		day = &t
	}
	c.OnShowInCalendar(c.card.EventID, c.card.RecurrenceID, day)
}

// JoinVideoCall opens the call's link (through OnOpenLink).
func (c *InvitationController) JoinVideoCall() {
	if c.card.VideoCall == nil || c.card.VideoCall.URL == "" || c.OnOpenLink == nil {
		return
	}
	c.OnOpenLink(c.card.VideoCall.URL)
}

// Replace shows the card of a newer message.get (the message changed);
// the target goes back to the new card's default.
func (c *InvitationController) Replace(card api.InvitationCard) {
	if reflect.DeepEqual(card, c.card) {
		return
	}
	c.card = card
	c.target = DefaultInvitationTarget(card)
	c.changed()
}

// replyAccount is the account the answer leaves from.
func (c *InvitationController) replyAccount() api.AccountID {
	if c.card.ReplyAccountID != "" {
		return c.card.ReplyAccountID
	}
	return c.accountID
}

func (c *InvitationController) changed() {
	if !c.closed && c.OnChange != nil {
		c.OnChange()
	}
}

// cardOccurrence is the card's time as an occurrence (Cal.occurrenceOf),
// for the mail's texts.
func cardOccurrence(c api.InvitationCard) api.Occurrence {
	return api.Occurrence{Title: c.Title, Start: c.Start, End: c.End, StartDate: c.StartDate, EndDate: c.EndDate,
		AllDay: c.AllDay, Location: c.Location}
}

// cardLine is one line of server text, as calendar cleans it.
func cardLine(s string) string { return board.CleanLine(s, api.MaxEventLocationBytes) }
