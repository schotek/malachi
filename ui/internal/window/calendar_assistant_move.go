// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Moving the assistant panel between Mail and the Calendar (D28): one
// panel per window, its widget (assistant_panel.blp's ToolbarView) in
// Mail's assistant_panel_bin, or borrowed into calendar_page.blp's
// calendar_assistant_bin while the Calendar shows it. assistantPanel.split
// is the split it is in now, so its Hide button, reveal and syncShown act
// where it is.

// calendarAssistantHome is a place of the panel: a split and its sidebar
// bin.
type calendarAssistantHome struct {
	split *adw.OverlaySplitView
	bin   *adw.Bin
}

// assistantSlot is the Calendar page's place for the panel: the split
// around calendar_split (calendar_assistant_split) and its sidebar
// (calendar_assistant_bin).
func (p *calendarPage) assistantSlot() (calendarAssistantHome, bool) {
	if p == nil || p.split == nil {
		return calendarAssistantHome{}, false
	}
	s, ok := gtk.BaseWidget(p.split).Parent().(*adw.OverlaySplitView)
	if !ok || s == nil {
		return calendarAssistantHome{}, false
	}
	b, ok := s.Sidebar().(*adw.Bin)
	if !ok || b == nil {
		return calendarAssistantHome{}, false
	}
	return calendarAssistantHome{split: s, bin: b}, true
}

// borrowCalendarAssistant moves the panel into the Calendar page; true
// when it is there.
func (w *Window) borrowCalendarAssistant() bool {
	ap := w.assistantPanel
	if ap == nil {
		return false
	}
	if ap.borrowed {
		return true
	}
	slot, ok := w.cal.page.assistantSlot()
	if !ok {
		return false
	}
	ap.home.split.SetShowSidebar(false)
	ap.home.bin.SetChild(nil)
	slot.bin.SetChild(ap.root)
	ap.split = slot.split
	ap.borrowed = true
	return true
}

// returnCalendarAssistant gives the panel back to Mail as the Calendar is
// left (leaveCalendar), open there when it was open in the Calendar.
func (w *Window) returnCalendarAssistant() {
	ap := w.assistantPanel
	if ap == nil || !ap.borrowed {
		return
	}
	open := ap.split.ShowSidebar()
	ap.split.SetShowSidebar(false)
	if b, ok := ap.split.Sidebar().(*adw.Bin); ok && b != nil {
		b.SetChild(nil)
	}
	ap.home.bin.SetChild(ap.root)
	ap.split = ap.home.split
	ap.borrowed = false
	ap.split.SetShowSidebar(open && w.assist != nil && w.assist.panelShown())
	ap.updateToggle()
}

// quickActionLabel is a quick action's button label.
func quickActionLabel(a assistant.Action) string {
	if assistant.IsCalendarAction(a) {
		return assistant.CalendarLabel(tr, a)
	}
	return assistant.Label(tr, a)
}

// syncQuickActions shows the calendar's quick actions while the window's
// calendar is ready and enables each button by what it acts on.
func (p *assistantPanel) syncQuickActions() {
	cal := p.w.calendarActionsShown()
	for i, b := range p.actions {
		a := quickActions[i]
		b.SetVisible(!assistant.IsCalendarAction(a) || cal)
		b.SetSensitive(p.canRunQuick(a))
	}
}

// calendarCardRow is the event an answer named (assistantpanel's calendar
// card): its day and Show in Calendar. The day is the bridge's, never text
// of an event.
func calendarCardRow(open func()) *transcriptRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.AddCSSClass("assistant-card")
	icon := gtk.NewImageFromIconName("x-office-calendar-symbolic")
	label := transcriptLabel()
	label.SetHExpand(true)
	button := gtk.NewButtonWithLabel(calendar.ShowInCalendar(i18n.Tr))
	button.AddCSSClass("chip-action")
	button.SetVAlign(gtk.AlignCenter)
	button.ConnectClicked(open)
	box.Append(icon)
	box.Append(label)
	box.Append(button)
	return &transcriptRow{root: box, update: func(c assistantpanel.Content) {
		label.SetText(calendarCardDay(c.Calendar))
	}}
}

// calendarCardDay is the card's day ("" when the bridge named none).
func calendarCardDay(ref assistant.CalendarRef) string {
	d, ok := ref.Day(time.Local)
	if !ok {
		return ""
	}
	return calendar.WeekdayDateText(d, calendar.Env{Tr: i18n.Tr, Format: widget.Strftime, Loc: time.Local})
}
