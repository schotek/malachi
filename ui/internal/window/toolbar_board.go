// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/toolbar"
)

// The board page's customizable header bars (toolbar_host.go): board_header
// draws the List's sidebar and list sections (board-list) or, in Columns
// and Today, the one section of board-flow; the detail's own header bar,
// one widget tree moved between the List and the sliding panel
// (board_panel.go), always draws board-list's detail section.

// boardToolbar is the board page's toolbar state.
type boardToolbar struct {
	set          *toolbarSet
	header       *tbHeader
	detail       *tbHeader
	faces        []*tbFace
	contacts     *gtk.Button
	flow         bool // the header shows board-flow now
	applied      bool
	removeChange []func()
}

// boardToolbarBar is the bar board_header shows in style st.
func boardToolbarBar(st board.Style) toolbar.Bar {
	if st == board.StyleList {
		return toolbar.BarBoardList
	}
	return toolbar.BarBoardFlow
}

// wireToolbar registers the board page's items and header bars from its
// builder b and packs the stored arrangements.
func (p *boardPage) wireToolbar(b *gtk.Builder) {
	ib := toolbarItemsBuilder()
	t := &boardToolbar{set: newToolbarSet(), contacts: ib.GetObject("toolbar_contacts").Cast().(*gtk.Button)}
	p.toolbar = t
	s := t.set
	s.add(toolbar.Mode, p.modeSwitch)
	s.add(toolbar.BoardStyle, p.styleSwitch)
	s.add(toolbar.Contacts, t.contacts)
	t.contacts.SetTooltipText(toolbar.ItemLabel(i18n.Tr, toolbar.BarBoardList, toolbar.Contacts))
	t.faces = append(t.faces, addFace(t.contacts, toolbar.ItemLabel(i18n.Tr, toolbar.BarBoardList, toolbar.Contacts)))
	s.add(toolbar.BoardAccount, p.accountFilter)
	s.add(toolbar.BoardTriage, p.triageButton, p.triageActivity)
	// The Blueprint's 18 px of Done are the detail's inset: they go to
	// whichever item stands first (leadInset).
	s.add(toolbar.BoardDone, p.doneButton).baseStart = 0
	s.add(toolbar.BoardRemind, p.remindButton)
	s.add(toolbar.BoardArchive, p.archiveButton)
	s.add(toolbar.BoardTrash, p.trashButton)
	s.add(toolbar.BoardReply, p.replyButton)
	t.header = s.addHeader(b.GetObject("board_header").Cast().(*adw.HeaderBar)).
		on(toolbar.BarBoardList, toolbar.SectionSidebar, toolbar.SectionList).
		on(toolbar.BarBoardFlow, toolbar.SectionMain)
	t.detail = s.addHeader(b.GetObject("board_detail_header").Cast().(*adw.HeaderBar)).
		on(toolbar.BarBoardList, toolbar.SectionDetail)
	t.detail.leadInset = 18
	w := p.w
	t.header.column = func() int { return w.Width() }
	s.watchFit(t.header)
	wireCustomizeMenu(t.header.header, func() toolbar.Bar { return boardToolbarBar(p.ctl.State().Style) }, w.openToolbarEditor)
	wireCustomizeMenu(t.detail.header, func() toolbar.Bar { return toolbar.BarBoardList }, w.openToolbarEditor)
	if st := w.settings; st != nil {
		for _, bar := range []toolbar.Bar{toolbar.BarBoardList, toolbar.BarBoardFlow} {
			t.removeChange = append(t.removeChange, st.OnChanged(toolbar.Key(bar), p.applyToolbar))
		}
	}
	p.applyToolbar()
	applyFaces(t.faces, w.toolbarDisplayMode())
}

// applyToolbar packs the board's arrangements for the style shown.
func (p *boardPage) applyToolbar() {
	t := p.toolbar
	if t == nil {
		return
	}
	w := p.w
	f := w.toolbarFeatures()
	list := w.toolbarLayout(toolbar.BarBoardList)
	t.flow = p.ctl.State().Style != board.StyleList
	t.applied = true
	if !t.flow {
		t.set.apply(toolbar.BarBoardList, list, f)
		return
	}
	t.set.apply(toolbar.BarBoardFlow, w.toolbarLayout(toolbar.BarBoardFlow), f)
	t.set.apply(toolbar.BarBoardList, list, f, t.detail)
}

// syncToolbar packs the header bar again when the style moved between the
// List and Columns/Today (renderHeader).
func (p *boardPage) syncToolbar() {
	t := p.toolbar
	if t == nil {
		return
	}
	if flow := p.ctl.State().Style != board.StyleList; !t.applied || flow != t.flow {
		p.applyToolbar()
	}
}
