// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Run in a dedicated test process with a display (xvfb-run, Broadway):
// MALACHI_GTK_SMOKE=1 go test ./internal/window -run
// '^TestInvitationCardGTKSmoke$' -count=1 -v. Only the invented sample
// calendar (calendar.SampleData through calendardata.NewSampleSource) is
// used: no daemon, settings or account. It builds the invitation card of
// every card state — unanswered, answered, outdated, cancelled, a reply,
// the user's own event, an update with its former values, unreadable —
// checks what each shows, answers the unanswered one, and logs LAYOUT:
// lines with the allocations and the texts.
func TestInvitationCardGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	wb := data.Builder("window.ui")
	w := &Window{
		ApplicationWindow: wb.GetObject("main_window").Cast().(*adw.ApplicationWindow),
		modeSwitchMail:    wb.GetObject("mode_switch").Cast().(*adw.ToggleGroup),
		log:               slog.Default(), mode: board.ModeMail,
		actions:       make(map[string]*gio.SimpleAction),
		toasts:        adw.NewToastOverlay(),
		dateWeekStart: time.Monday,
	}
	defer w.Destroy()
	now := time.Now()
	src := calendardata.NewSampleSource(now, time.Local, glibLoop{})
	w.cal.source = src
	samples := calendar.SampleData(now, time.Local)
	cards := map[api.InvitationState]api.InvitationCard{}
	for i, st := range calendar.InvitationStates {
		cards[st] = samples.Cards[i]
	}
	own := cards[api.InvitationUnanswered]
	own.OwnEvent, own.State, own.CanRespond, own.EventID = true, api.InvitationProcessed, false, "ev_sample_demo"
	update := cards[api.InvitationUnanswered]
	update.Kind, update.Sequence = api.InvitationKindUpdate, 1
	prev := *cards[api.InvitationOutdated].Previous
	prev.Location = "Room B"
	update.Previous = &prev

	host := gtk.NewBox(gtk.OrientationVertical, 12)
	host.SetMarginStart(24)
	host.SetMarginEnd(24)
	host.SetMarginTop(24)
	scroll := gtk.NewScrolledWindow()
	scroll.SetChild(host)
	w.SetContent(scroll)
	w.SetDefaultSize(900, 1100)
	w.Present()
	flush := func() {
		ctx := glib.MainContextDefault()
		for i := 0; i < 200 && ctx.Pending(); i++ {
			ctx.Iteration(false)
		}
	}
	wait := func(d time.Duration) {
		end := time.Now().Add(d)
		ctx := glib.MainContextDefault()
		for time.Now().Before(end) {
			ctx.Iteration(false)
			time.Sleep(5 * time.Millisecond)
		}
	}

	type step struct {
		name string
		card api.InvitationCard
		// check looks at the built card.
		check func(t *testing.T, c *invitationCard)
	}
	tr := i18n.Tr
	steps := []step{
		{"unanswered", cards[api.InvitationUnanswered], func(t *testing.T, c *invitationCard) {
			if !c.actions.Visible() || !c.accept.Visible() || c.stripRow.Visible() {
				t.Error("an unanswered invitation shows no Accept / Tentative / Decline row")
			}
			if !c.target.Visible() || len(c.targetIDs) != 2 {
				t.Errorf("Add to: shows %d targets in a dropdown (visible %v), want 2", len(c.targetIDs), c.target.Visible())
			}
			if c.dayStrip == nil || !c.strip.Visible() || c.dayStrip.Proposed == nil {
				t.Error("no day strip with the proposed slot")
			}
			if !c.conflictRow.Visible() || !strings.Contains(c.conflict.Text(), "Standup") {
				t.Errorf("conflict line %q", c.conflict.Text())
			}
			if !c.notes.Visible() {
				t.Error("no reply note")
			}
			if !c.tile.Visible() || c.day.Text() == "" {
				t.Error("no date tile")
			}
		}},
		{"answered", cards[api.InvitationAnswered], func(t *testing.T, c *invitationCard) {
			if c.actions.Visible() || !c.stripRow.Visible() || !c.show.Visible() || !c.change.Visible() {
				t.Error("an answered invitation shows no state strip with Change Response and Show in Calendar")
			}
			if !strings.HasPrefix(c.stripLabel.Text(), calendar.ResponseLabel(api.ResponseAccepted, tr)) {
				t.Errorf("state strip %q", c.stripLabel.Text())
			}
		}},
		{"outdated", cards[api.InvitationOutdated], func(t *testing.T, c *invitationCard) {
			if c.actions.Visible() || !c.state.Visible() || !c.previous.Visible() {
				t.Error("an outdated invitation shows answers, or no state line, or no former time")
			}
		}},
		{"cancelled", cards[api.InvitationCancelled], func(t *testing.T, c *invitationCard) {
			if a := c.title.Attributes(); a == nil || len(a.Attributes()) == 0 {
				t.Error("the cancelled title is not struck through")
			}
			if !c.cancelRow.Visible() || c.remove.Label() != calendar.RemoveFromCalendar(tr) {
				t.Error("no Remove from Calendar")
			}
			if c.state.Text() != calendar.CancelledLine(tr) {
				t.Errorf("state line %q", c.state.Text())
			}
		}},
		{"reply", cards[api.InvitationProcessed], func(t *testing.T, c *invitationCard) {
			if c.tile.Visible() || c.clock.Visible() || c.actions.Visible() {
				t.Error("a reply's card is more than its line")
			}
			if !strings.Contains(c.title.Text(), "Jana Nováková") {
				t.Errorf("reply line %q", c.title.Text())
			}
		}},
		{"own", own, func(t *testing.T, c *invitationCard) {
			if want := calendar.StateLine(own, tr); c.state.Text() != want || want == "" {
				t.Errorf("own event line %q", c.state.Text())
			}
			if !c.stateShow.Visible() || c.actions.Visible() {
				t.Error("the user's own event shows no Show in Calendar, or answers")
			}
		}},
		{"update", update, func(t *testing.T, c *invitationCard) {
			if !c.actions.Visible() || len(c.previousLines) != 2 {
				t.Errorf("an update shows %d former values (%q), want 2", len(c.previousLines), c.previousLines)
			}
			if !c.propose.Visible() || !c.proposeRow.Visible() {
				t.Log("no Propose New Time… (the sample does not allow it)")
			}
		}},
		{"invalid", cards[api.InvitationInvalid], func(t *testing.T, c *invitationCard) {
			if c.root.Visible() {
				t.Error("an unreadable invitation shows a card")
			}
		}},
	}
	built := map[string]*invitationCard{}
	for i, s := range steps {
		c := newInvitationCard(w, &w.ApplicationWindow.Window)
		host.Append(c.widget())
		sum := api.MessageSummary{ID: api.MessageID("msg_" + s.name), AccountID: calendar.SampleGmail}
		card := s.card
		c.showCard(sum, &api.Message{MessageSummary: sum, Invitation: &card}, true)
		built[s.name] = c
		if i == 0 {
			wait(300 * time.Millisecond)
		}
		flush()
	}
	wait(300 * time.Millisecond)
	flush()
	for _, s := range steps {
		c := built[s.name]
		t.Run(s.name, func(t *testing.T) { s.check(t, c) })
		t.Logf("LAYOUT: %s card=%dx%d visible=%v tile=%v(%q %q %q) pill=%q title=%q clock=%q previous=%q location=%q join=%v people=%q conflict=%q strip=%dx%d actions=%v(target %v label %q) state=%q stateShow=%v answered=%q notes=%q remove=%v propose=%v",
			s.name, c.root.Width(), c.root.Height(), c.root.Visible(), c.tile.Visible(), c.weekday.Text(), c.day.Text(), c.month.Text(),
			visibleText(c.pill), visibleText(c.title), visibleText(c.clock), c.previousLines, visibleText(c.location), c.join.Visible(),
			visibleText(c.people), visibleRow(c.conflictRow, c.conflict), c.strip.Width(), c.strip.Height(),
			c.actions.Visible(), c.target.Visible(), visibleText(c.targetLabel), visibleText(c.state), c.stateShow.Visible(),
			visibleRow(c.stripRow, c.stripLabel), c.noteText, c.cancelRow.Visible(), c.proposeRow.Visible())
	}

	// Answering: Accept on the unanswered card shows the answered one.
	answered := cards[api.InvitationAnswered]
	src.EmulatesWrites = true
	src.RespondedCard = &answered
	u := built["unanswered"]
	if !u.accept.Activate() {
		t.Log("Accept did not activate; answering through the card")
		u.respond(api.ResponseAccepted, false)
	}
	// A button's activate clicks after its 250 ms press animation.
	wait(600 * time.Millisecond)
	flush()
	if !u.stripRow.Visible() || u.actions.Visible() {
		t.Errorf("after Accept the card shows no state strip (actions %v, strip %v, error %q)", u.actions.Visible(),
			u.stripRow.Visible(), u.errLabel.Text())
		t.Logf("busy %v writes %d mode %v", u.ctl.Busy(), len(src.Writes()), u.ctl.Mode())
	}
	t.Logf("LAYOUT: accepted card=%dx%d strip=%q", u.root.Width(), u.root.Height(), u.stripLabel.Text())

	// A card let go hides and stops following the source.
	u.clear()
	if u.root.Visible() || u.token != nil {
		t.Error("a cleared card still shows or observes")
	}
}

// visibleText is a label's text when it shows, else "-".
func visibleText(l *gtk.Label) string {
	if !l.Visible() {
		return "-"
	}
	return l.Text()
}

// visibleRow is a label's text when its row shows, else "-".
func visibleRow(row *gtk.Box, l *gtk.Label) string {
	if !row.Visible() {
		return "-"
	}
	return l.Text()
}
