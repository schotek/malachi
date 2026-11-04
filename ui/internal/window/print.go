// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/htmlview"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/printing"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Printing (docs/plans/toolbar-customization.md §8, P1–P8; the macOS
// client's PrintController is the model): a message, a conversation,
// several messages, a calendar event or a day. The document is a
// printing.Fragment: the application's escaped header rows around the
// sanitised bodies of message.body asked without trimQuoted (the whole
// message, quoted history included), straight from the client, never
// through the cache's variant switch, which would change what the reader
// shows. htmlview.Print loads it into a viewer of its own (same CSP, no
// script of the document, no network, malachi-cid:) and prints it with
// WebKit's print operation.
//
// win.print (Ctrl+P, PrintAccel) prints in Mail the selection (one
// message; a conversation row as its folder members, oldest first, a
// rule between them; several rows each on a new page; at most
// printing.MaxMessages) and in the Calendar the selected event, else the
// day the view stands on (P8); on the Board it is off. msg.print of a
// message window prints its message. Both are in their More Actions menu
// (GTK has no File menu, and the main menu stays as PR #20 settled it).
// The toolbar item of the toolbar customization binds to win.print.

// PrintAccel is win.print's and msg.print's shortcut.
const PrintAccel = "<Control>p"

// printLayout is how the rows print: several rows page by page (P5), a
// row that stands for several messages (a conversation) as one
// conversation, else one message.
func printLayout(rows, messages int) printing.Layout {
	switch {
	case rows > 1:
		return printing.Pages
	case messages > 1:
		return printing.Conversation
	}
	return printing.Single
}

// printOrder is what a print takes of list: at most printing.MaxMessages,
// a conversation oldest first (by date, then id).
func printOrder(list []api.MessageSummary, layout printing.Layout) []api.MessageSummary {
	out := append([]api.MessageSummary(nil), list...)
	if len(out) > printing.MaxMessages {
		out = out[:printing.MaxMessages]
	}
	if layout == printing.Conversation {
		sort.SliceStable(out, func(i, j int) bool {
			if !out[i].Date.Equal(out[j].Date) {
				return out[i].Date.Before(out[j].Date)
			}
			return out[i].ID < out[j].ID
		})
	}
	return out
}

// ownRole reports whether a message in a folder of role r is the user's
// own, whose Bcc the print shows (P6).
func ownRole(r api.FolderRole) bool {
	return r == api.RoleSent || r == api.RoleDrafts || r == api.RoleOutbox
}

// printHeader is a message's header for the print from its summary s, the
// message.get answer m (nil: not known) and its body b (nil: not known),
// formatted as the reader formats them; Bcc only with own.
func printHeader(s api.MessageSummary, m *api.Message, b *api.MessageBodyResult, own bool) printing.Header {
	if m != nil {
		s = m.MessageSummary
	}
	h := printing.Header{
		Subject: subjectText(s.Subject),
		From:    widget.FormatParticipants(s.From),
		To:      widget.FormatParticipants(s.To),
	}
	if !s.Date.IsZero() {
		h.Date = widget.FormatDateTime(s.Date)
	}
	if m != nil {
		h.Cc = widget.FormatParticipants(m.CC)
		if own {
			h.Bcc = widget.FormatParticipants(m.BCC)
		}
		for _, a := range chipAttachments(m.Attachments, b, nil) {
			h.Attachments = append(h.Attachments, chipName(a)+" ("+widget.FormatSize(a.Size)+")")
		}
	}
	return h
}

// printBody is the body the print shows: the sanitiser's HTML when the
// reader would show it (showsHTML, not withheld), else the plain text,
// else the summary's snippet.
func printBody(s api.MessageSummary, b *api.MessageBodyResult) printing.Body {
	if showsHTML(b) && !b.HTMLWithheld {
		return printing.Body{HTML: b.HTML}
	}
	if b != nil && strings.TrimSpace(b.Text) != "" {
		return printing.Body{Text: b.Text}
	}
	return printing.Body{Text: s.Snippet}
}

// printEvent is an event's print from its detail (calendar.DetailOf).
func printEvent(d calendar.Detail) printing.Event {
	e := printing.Event{
		Title: d.Title,
		Lines: []string{d.TimeLine, d.Recurrence, d.Location, d.CalendarLine, d.PeopleLine},
		Notes: d.Notes,
	}
	for _, a := range d.Attendees {
		who := a.Name
		if who == "" {
			who = a.Address
		}
		if a.ResponseText != "" {
			who += " — " + a.ResponseText
		}
		e.Attendees = append(e.Attendees, who)
	}
	return e
}

// printDay is a day's print (P7): the day's heading and its events as the
// agenda lists them (all-day first).
func printDay(occ []api.Occurrence, day, now time.Time, env calendar.Env, showDeclined bool) string {
	var items []printing.DayItem
	if days := calendar.Agenda(occ, day, 1, now, env, showDeclined); len(days) > 0 {
		for _, it := range days[0].Items {
			items = append(items, printing.DayItem{
				Time: calendar.ItemTime(it, env), Title: it.Occurrence.Title, Location: it.Occurrence.Location,
			})
		}
	}
	return printing.DaySection(calendar.DayHeading(day, now, env), items, calendar.AgendaEmpty(env.Tr))
}

// setupPrint adds win.print and its item to the main window's More
// Actions menu (b is window.blp's builder).
func (w *Window) setupPrint(b *gtk.Builder) {
	w.addAction("print", false, w.printCurrent)
	if m, ok := b.GetObject("message_menu_model").Cast().(*gio.Menu); ok {
		m.AppendSection("", printMenuSection("win.print"))
	}
}

// printMenuSection is the More Actions section with Print… for action.
func printMenuSection(action string) *gio.Menu {
	section := gio.NewMenu()
	section.Append(printing.MenuTitle(i18n.Tr), action)
	return section
}

// refreshPrint enables win.print: in Mail while 1 to MaxMessages rows are
// selected (mailRows, from setMessageActionsSensitive), always in the
// Calendar, never on the Board.
func (w *Window) refreshPrint() {
	a := w.actions["print"]
	if a == nil {
		return
	}
	switch w.mode {
	case board.ModeCalendar:
		a.SetEnabled(true)
	case board.ModeMail:
		a.SetEnabled(w.printRows > 0 && w.printRows <= printing.MaxMessages)
	default:
		a.SetEnabled(false)
	}
}

// printCurrent is win.print.
func (w *Window) printCurrent() {
	switch w.mode {
	case board.ModeMail:
		w.printSelection()
	case board.ModeCalendar:
		w.printCalendar()
	}
}

// printSelection prints the selected rows' messages: a conversation row's
// folder members, else the row's message.
func (w *Window) printSelection() {
	w.selectedIDs(func(rows []listRow, ids []api.MessageID) {
		var list []api.MessageSummary
		for _, r := range rows {
			for _, id := range w.model.rowIDs(r) {
				if s, _, ok := w.model.message(id); ok {
					list = append(list, s)
				} else if id == r.Message.ID {
					list = append(list, r.Message)
				}
			}
		}
		w.printMessages(list, printLayout(len(rows), len(list)), &w.ApplicationWindow.Window)
	})
}

// printMessages prints list in layout from parent: each message's section
// in order once its header and body arrived.
func (w *Window) printMessages(list []api.MessageSummary, layout printing.Layout, parent *gtk.Window) {
	list = printOrder(list, layout)
	if len(list) == 0 {
		return
	}
	title := subjectText(list[0].Subject)
	sections := make([]string, len(list))
	var next func(i int)
	next = func(i int) {
		if i == len(list) {
			htmlview.Print(w.log, w.fetchPart, printing.Fragment(sections, layout), title, parent, nil)
			return
		}
		s := list[i]
		own := ownRole(w.model.folderRole(folderKey{Account: s.AccountID, Folder: s.FolderID}))
		w.printSection(s, own, func(section string) {
			sections[i] = section
			next(i + 1)
		})
	}
	next(0)
}

// printSection builds one message's section and calls done with it on
// the main loop: the header from message.get through the cache (which
// calls back after each half and on later updates: the first answer with
// nothing in flight is taken, once), the whole body from message.body
// without trimQuoted, asked directly; a body that fails falls back to the
// cache's, then to the snippet.
func (w *Window) printSection(s api.MessageSummary, own bool, done func(string)) {
	taken := false
	w.fetchMessage(s.AccountID, s.ID, func(lm *loadedMessage) {
		if taken || lm.getting || lm.fetching {
			return
		}
		taken = true
		m, cached := lm.msg, lm.body
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
			defer cancel()
			var res api.MessageBodyResult
			err := w.client.Call(ctx, api.MethodMessageBody, bodyParams(s.AccountID, s.ID, true, ""), &res)
			glib.IdleAdd(func() {
				b := &res
				if err != nil {
					w.log.Warn("print: message.body", "err", err)
					b = cached
				}
				done(printing.Message(printing.HeaderRows(i18n.Tr, printHeader(s, m, b, own)), printBody(s, b)))
			})
		}()
	})
}

// printCalendar prints the Calendar's selected event, else the day its
// view stands on (P8: in the week and month the anchor's day).
func (w *Window) printCalendar() {
	p := w.cal.page
	if p == nil {
		return
	}
	parent := &w.ApplicationWindow.Window
	env := w.calendarEnv()
	if o, ok := p.ctl.SelectedOccurrence(); ok && o.EventID != "" {
		src := p.ctl.Source()
		src.Event(o.EventID, o.RecurrenceID, false, func(e *api.Event, err *api.Error) {
			if err != nil || e == nil {
				w.log.Warn("print: event", "err", err)
				return
			}
			snap := src.Snapshot()
			cal := ""
			if e.CalendarID != "" {
				cal = snap.CalendarName(e.CalendarID)
			}
			d := calendar.DetailOf(*e, snap.AccountName(e.AccountID), cal, env)
			frag := printing.Fragment([]string{printing.EventSection(printEvent(d))}, printing.Single)
			htmlview.Print(w.log, w.fetchPart, frag, d.Title, parent, nil)
		})
		return
	}
	now := time.Now()
	day := p.ctl.Anchor()
	frag := printing.Fragment([]string{printDay(p.ctl.Occurrences(), day, now, env, p.ctl.ShowDeclined())}, printing.Single)
	htmlview.Print(w.log, w.fetchPart, frag, calendar.DayHeading(day, now, env), parent, nil)
}
