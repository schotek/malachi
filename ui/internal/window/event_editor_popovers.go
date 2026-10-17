// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"bufio"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendardata"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The event editor's popovers (port of EventEditorPopovers.swift and the
// Add Video Call menu of EventEditorViewController.swift): the time-zone
// search, Custom… repeat ("Every [n] [unit ▾]", the week's days, Done)
// and Add Video Call ▾ (the calendar's providers, Paste Link…, Remove).

// editorRemoveText is the tooltip of an attendee badge's and an
// attachment chip's × and the video menu's Remove (the compose window's
// msgid).
func editorRemoveText() string { return i18n.T("Remove") }

// editorZones is the IANA zone list, read once.
var editorZones = sync.OnceValue(readZoneNames)

// readZoneNames reads the system's IANA zone names (zone1970.tab and
// zone.tab of tzdata), UTC and the local zone, sorted; without tzdata at
// least those two.
func readZoneNames() []string {
	seen := map[string]bool{"UTC": true}
	if z := string(calendardata.ZoneName(time.Local)); z != "" {
		seen[z] = true
	}
	for _, p := range []string{"/usr/share/zoneinfo/zone1970.tab", "/usr/share/zoneinfo/zone.tab"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if line == "" || line[0] == '#' {
				continue
			}
			if fields := strings.Split(line, "\t"); len(fields) >= 3 && fields[2] != "" {
				seen[fields[2]] = true
			}
		}
		_ = f.Close()
	}
	out := make([]string, 0, len(seen))
	for z := range seen {
		out = append(out, z)
	}
	slices.Sort(out)
	return out
}

// wirePopovers fills the time-zone popover's list and its search.
func (ed *eventEditor) wirePopovers() {
	names := editorZones()
	for _, z := range names {
		l := gtk.NewLabel(z)
		l.SetUseMarkup(false)
		l.SetXAlign(0)
		l.SetMarginTop(4)
		l.SetMarginBottom(4)
		l.SetMarginStart(8)
		l.SetMarginEnd(8)
		ed.zoneList.Append(l)
	}
	ed.zoneList.SetFilterFunc(func(row *gtk.ListBoxRow) bool {
		q := strings.ToLower(strings.TrimSpace(ed.zoneSearch.Text()))
		i := row.Index()
		return q == "" || (i >= 0 && i < len(names) && strings.Contains(strings.ToLower(names[i]), q))
	})
	ed.zoneSearch.ConnectSearchChanged(func() { ed.zoneList.InvalidateFilter() })
	pick := func(i int) {
		if i < 0 || i >= len(names) {
			return
		}
		if l, err := time.LoadLocation(names[i]); err == nil {
			ed.ctl.SetZone(l)
		}
		ed.zonePopover.Popdown()
	}
	ed.zoneList.ConnectRowActivated(func(row *gtk.ListBoxRow) { pick(row.Index()) })
	ed.zoneSearch.ConnectActivate(func() {
		for i := 0; ; i++ {
			row := ed.zoneList.RowAtIndex(i)
			if row == nil {
				return
			}
			if row.ChildVisible() && row.Mapped() {
				pick(i)
				return
			}
		}
	})
	ed.zonePopover.ConnectShow(func() {
		ed.zoneSearch.SetText("")
		cur := string(calendardata.ZoneName(ed.ctl.Zone()))
		if i := slices.Index(names, cur); i >= 0 {
			if row := ed.zoneList.RowAtIndex(i); row != nil {
				ed.zoneList.SelectRow(row)
			}
		}
		ed.zoneSearch.GrabFocus()
	})
}

// customFrequencies are the Custom… popover's units.
var customFrequencies = []api.RecurrenceFrequency{api.FrequencyDaily, api.FrequencyWeekly, api.FrequencyMonthly,
	api.FrequencyYearly}

// showCustomRepeat is Repeat's Custom…: "Every [n] [unit ▾]", the week's
// days for a weekly rule, Done. The popup shows the rule in force until
// Done applies a new one.
func (ed *eventEditor) showCustomRepeat() {
	ed.render()
	if ed.repeatPopover != nil {
		ed.repeatPopover.Popup()
		return
	}
	tr := i18n.Tr
	in, z := ed.ctl.Input(), ed.ctl.Zone()
	start := in.Start.In(z)
	cur := ed.ctl.Recurrence()
	freq, n := api.FrequencyWeekly, 1
	var codes []string
	if cur != nil {
		if slices.Contains(customFrequencies, cur.Frequency) {
			freq = cur.Frequency
		}
		n = min(max(1, cur.Interval), api.MaxRecurrenceInterval)
		codes = cur.ByDay
	}

	box := gtk.NewBox(gtk.OrientationVertical, 10)
	box.SetMarginTop(12)
	box.SetMarginBottom(12)
	box.SetMarginStart(14)
	box.SetMarginEnd(14)
	line := gtk.NewBox(gtk.OrientationHorizontal, 6)
	every := gtk.NewLabel(calendar.EveryLabel(tr))
	every.SetUseMarkup(false)
	interval := gtk.NewSpinButtonWithRange(1, float64(api.MaxRecurrenceInterval), 1)
	interval.SetValue(float64(n))
	interval.SetNumeric(true)
	unit := gtk.NewDropDown(nil, nil)
	units := func(sel int) {
		k := max(1, interval.ValueAsInt())
		titles := make([]string, 0, len(customFrequencies))
		for _, f := range customFrequencies {
			titles = append(titles, calendar.IntervalUnit(f, k, tr))
		}
		unit.SetModel(gtk.NewStringList(titles))
		unit.SetSelected(uint(max(0, sel)))
	}
	units(slices.Index(customFrequencies, freq))
	line.Append(every)
	line.Append(interval)
	line.Append(unit)
	box.Append(line)

	days := gtk.NewBox(gtk.OrientationHorizontal, 6)
	type dayCheck struct {
		day   time.Weekday
		check *gtk.CheckButton
	}
	var checks []dayCheck
	first := calendar.StartOfDay(start, z)
	var week []time.Time
	for k := range 7 {
		week = append(week, calendar.AddDays(first, k))
	}
	slices.SortFunc(week, func(a, b time.Time) int { return (int(a.Weekday())+6)%7 - (int(b.Weekday())+6)%7 })
	env := ed.zoneEnv()
	for _, d := range week {
		c := gtk.NewCheckButtonWithLabel(calendar.ShortWeekdayText(d, env))
		on := d.Weekday() == start.Weekday()
		if len(codes) > 0 {
			on = slices.Contains(codes, calendar.DayCode(d.Weekday()))
		}
		c.SetActive(on)
		days.Append(c)
		checks = append(checks, dayCheck{d.Weekday(), c})
	}
	days.SetVisible(freq == api.FrequencyWeekly)
	box.Append(days)

	frequency := func() api.RecurrenceFrequency {
		i := int(unit.Selected())
		if i < 0 || i >= len(customFrequencies) {
			return api.FrequencyWeekly
		}
		return customFrequencies[i]
	}
	interval.ConnectValueChanged(func() { units(int(unit.Selected())) })
	unit.NotifyProperty("selected", func() { days.SetVisible(frequency() == api.FrequencyWeekly) })

	done := gtk.NewButtonWithLabel(board.Done(tr))
	done.AddCSSClass("suggested-action")
	done.SetHAlign(gtk.AlignStart)
	box.Append(done)

	p := gtk.NewPopover()
	p.SetChild(box)
	p.SetParent(ed.repeat)
	p.SetPosition(gtk.PosBottom)
	p.SetDefaultWidget(done)
	done.ConnectClicked(func() {
		var on []time.Weekday
		for _, c := range checks {
			if c.check.Active() {
				on = append(on, c.day)
			}
		}
		in, z := ed.ctl.Input(), ed.ctl.Zone()
		ed.ctl.SetRecurrence(calendar.CustomRecurrence(frequency(), interval.ValueAsInt(), on, in.Start.In(z)))
		p.Popdown()
	})
	p.ConnectClosed(func() {
		glib.IdleAdd(func() {
			if ed.repeatPopover == p {
				ed.repeatPopover = nil
				p.Unparent()
			}
			ed.render()
		})
	})
	ed.repeatPopover = p
	p.Popup()
}

// wireVideoActions adds Add Video Call's actions: a provider (create),
// Paste Link… (link) and Remove.
func (ed *eventEditor) wireVideoActions() {
	provider := gio.NewSimpleAction("video-provider", glib.NewVariantType("s"))
	provider.ConnectActivate(func(v *glib.Variant) {
		if v == nil {
			return
		}
		ed.ctl.SetVideoCall(&api.VideoCallInput{Mode: api.VideoCallCreate, Provider: v.String()})
		ed.videoKey = ""
		ed.renderVideo()
	})
	ed.actions.AddAction(provider)
	paste := gio.NewSimpleAction("paste-link", nil)
	paste.ConnectActivate(func(*glib.Variant) { ed.askLink() })
	ed.actions.AddAction(paste)
	remove := gio.NewSimpleAction("remove-video", nil)
	remove.ConnectActivate(func(*glib.Variant) {
		ed.ctl.SetVideoCall(nil)
		ed.videoKey = ""
		ed.renderVideo()
	})
	ed.actions.AddAction(remove)
}

// renderVideo is Add Video Call ▾: its title the chosen provider or the
// link's host, the menu the calendar's providers, Paste Link… and Remove
// once there is one.
func (ed *eventEditor) renderVideo() {
	tr := i18n.Tr
	providers := ed.ctl.VideoProviders()
	v := ed.ctl.VideoCall()
	key := strings.Join(providers, "\x00")
	if v != nil {
		key += "|" + string(v.Mode) + "|" + v.Provider + "|" + v.URL
	}
	if key == ed.videoKey && ed.video.MenuModel() != nil {
		return
	}
	ed.videoKey = key
	title := calendar.AddVideoCall(tr)
	tip := ""
	if v != nil {
		switch v.Mode {
		case api.VideoCallCreate:
			title = calendar.VideoProviderName(v.Provider, tr)
		case api.VideoCallLink:
			if u, err := url.Parse(v.URL); err == nil && u.Host != "" {
				title = board.CleanLine(u.Host, 200)
			}
			tip = editorClip(v.URL, 1000)
		}
	}
	ed.video.SetLabel(title)
	ed.video.SetTooltipText(tip)
	if c, ok := ed.video.Child().(*gtk.Box); ok {
		for w := c.FirstChild(); w != nil; w = gtk.BaseWidget(w).NextSibling() {
			if l, ok := w.(*gtk.Label); ok {
				l.SetUseMarkup(false)
				l.SetEllipsize(pango.EllipsizeEnd)
			}
		}
	}
	m := gio.NewMenu()
	if len(providers) > 0 {
		sec := gio.NewMenu()
		for _, p := range providers {
			it := gio.NewMenuItem(calendar.VideoProviderName(p, tr), "")
			it.SetActionAndTargetValue("editor.video-provider", glib.NewVariantString(p))
			sec.AppendItem(it)
		}
		m.AppendSection("", sec)
	}
	link := gio.NewMenu()
	link.Append(calendar.PasteLink(tr), "editor.paste-link")
	m.AppendSection("", link)
	if v != nil {
		rm := gio.NewMenu()
		rm.Append(editorRemoveText(), "editor.remove-video")
		m.AppendSection("", rm)
	}
	ed.video.SetMenuModel(m)
}

// askLink is Paste Link…: an https link (the current one prefilled).
func (ed *eventEditor) askLink() {
	tr := i18n.Tr
	entry := gtk.NewEntry()
	// The scheme the link must have; not a sentence.
	entry.SetPlaceholderText("https://")
	entry.SetActivatesDefault(true)
	if v := ed.ctl.VideoCall(); v != nil && v.Mode == api.VideoCallLink {
		entry.SetText(v.URL)
	}
	d := adw.NewAlertDialog(calendar.AddVideoCall(tr), "")
	d.SetExtraChild(entry)
	d.AddResponse("cancel", calendar.Cancel(tr))
	d.AddResponse("done", board.Done(tr))
	d.SetResponseAppearance("done", adw.ResponseSuggested)
	d.SetDefaultResponse("done")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(r string) {
		if r != "done" || ed.closing {
			return
		}
		if !ed.ctl.SetVideoCall(&api.VideoCallInput{Mode: api.VideoCallLink, URL: entry.Text()}) {
			ed.win.ErrorBell()
		}
		ed.videoKey = ""
		ed.renderVideo()
	})
	d.Present(ed.win)
	entry.GrabFocus()
}
