// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/toolbar"
)

// The Calendar page's customizable header bar (toolbar_host.go):
// calendar_header draws both sections of the calendar bar.

// calendarToolbar is the Calendar page's toolbar state.
type calendarToolbar struct {
	set      *toolbarSet
	header   *tbHeader
	faces    []*tbFace
	contacts *gtk.Button
	print    *gtk.Button
}

// wireToolbar registers the page's items from its builder b and packs the
// stored arrangement.
func (p *calendarPage) wireToolbar(b *gtk.Builder) {
	ib := toolbarItemsBuilder()
	t := &calendarToolbar{
		set:      newToolbarSet(),
		contacts: ib.GetObject("toolbar_contacts").Cast().(*gtk.Button),
		print:    ib.GetObject("toolbar_print").Cast().(*gtk.Button),
	}
	p.toolbar = t
	s := t.set
	bar := toolbar.BarCalendar
	label := func(id toolbar.ID) string { return toolbar.ItemLabel(i18n.Tr, bar, id) }
	s.add(toolbar.Mode, p.modeSwitch)
	s.add(toolbar.ToggleSidebar, p.sidebarToggle)
	s.add(toolbar.CalendarToday, p.today)
	s.add(toolbar.CalendarStep, b.GetObject("calendar_step").Cast().(gtk.Widgetter))
	s.add(toolbar.CalendarView, p.viewSwitch, p.viewMenu)
	s.add(toolbar.CalendarSearch, p.searchToggle)
	s.add(toolbar.Contacts, t.contacts)
	s.add(toolbar.CalendarNewEvent, p.newEvent)
	s.add(toolbar.CalendarAssistant, p.assistantButton)
	s.add(toolbar.Print, t.print)
	t.contacts.SetTooltipText(label(toolbar.Contacts))
	t.print.SetTooltipText(label(toolbar.Print))
	if p.sidebarToggle.TooltipText() == "" {
		p.sidebarToggle.SetTooltipText(label(toolbar.ToggleSidebar))
	}
	for _, x := range []struct {
		id toolbar.ID
		w  gtk.Widgetter
	}{
		{toolbar.ToggleSidebar, p.sidebarToggle}, {toolbar.CalendarSearch, p.searchToggle},
		{toolbar.Contacts, t.contacts}, {toolbar.CalendarNewEvent, p.newEvent},
		{toolbar.CalendarAssistant, p.assistantButton}, {toolbar.Print, t.print},
	} {
		t.faces = append(t.faces, addFace(x.w, label(x.id)))
	}
	t.header = s.addHeader(p.header).on(bar, toolbar.SectionSidebar, toolbar.SectionMain)
	w := p.w
	t.header.column = func() int { return w.Width() }
	s.watchFit(t.header)
	wireCustomizeMenu(p.header, func() toolbar.Bar { return bar }, w.openToolbarEditor)
	if st := w.settings; st != nil {
		st.OnChanged(toolbar.Key(bar), p.applyToolbar)
	}
	p.applyToolbar()
	applyFaces(t.faces, w.toolbarDisplayMode())
}

// applyToolbar packs the stored arrangement.
func (p *calendarPage) applyToolbar() {
	if p.toolbar == nil {
		return
	}
	w := p.w
	p.toolbar.set.apply(toolbar.BarCalendar, w.toolbarLayout(toolbar.BarCalendar), w.toolbarFeatures())
}
