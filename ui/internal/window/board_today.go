// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"reflect"
	"slices"
	"strconv"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The board's Today style: a greeting, the count tiles, what is hot, the
// top of what waits for the user ("and N more"), the user's commitments —
// one ListBox, board_today_list, board_page.blp — and beside it (under it
// below 900sp, the root Adw.Breakpoint) the deadlines
// (board_today_due_box) and a calendar placeholder. Every row reuses List
// style's building blocks (widget.BoardRow, boardSectionHeader, the
// context menu) so Today reads as a filtered view of the same board, not
// a fourth kind of row. Reference: macOS BoardTodayViewController.swift.

// boardToday owns board_today_title/_phrase/_tiles/_list and the side
// column's widgets.
type boardToday struct {
	p *boardPage

	title  *gtk.Label
	phrase *gtk.Label
	tiles  *gtk.Box

	list        *gtk.ListBox
	items       []todayItem
	reselecting bool

	dueBox        *gtk.Box
	calendarTitle *gtk.Label
	calendarBody  *gtk.Label
	dueGroups     []board.DueSection
}

// bindToday fetches Today's static widgets; called from ensureBoard after
// p.bind.
func (p *boardPage) bindToday(b *gtk.Builder) {
	t := &boardToday{p: p}
	t.title = b.GetObject("board_today_title").Cast().(*gtk.Label)
	t.phrase = b.GetObject("board_today_phrase").Cast().(*gtk.Label)
	t.tiles = b.GetObject("board_today_tiles").Cast().(*gtk.Box)
	t.list = b.GetObject("board_today_list").Cast().(*gtk.ListBox)
	t.dueBox = b.GetObject("board_today_due_box").Cast().(*gtk.Box)
	t.calendarTitle = b.GetObject("board_today_calendar_title").Cast().(*gtk.Label)
	t.calendarBody = b.GetObject("board_today_calendar_body").Cast().(*gtk.Label)
	p.today = t
}

// wireToday connects board_today_list's selection and the "and N more"
// row's action.
func (p *boardPage) wireToday() {
	t := p.today
	t.list.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if t.reselecting {
			return
		}
		if row == nil {
			p.ctl.Select("")
			return
		}
		idx := row.Index()
		if idx < 0 || idx >= len(t.items) {
			return
		}
		if id, ok := t.items[idx].caseID(); ok {
			p.ctl.Select(id)
			return
		}
		if t.items[idx].kind == todayItemMore {
			p.ctl.ShowWaitingForYou()
		}
	})
}

// apply is board.go's applyAll/onChange hook: idempotent, and cheap when
// nothing in the list, the tiles or the deadlines changed.
func (t *boardToday) apply(vm board.ViewModel) {
	if t == nil {
		return
	}
	t.title.SetText(vm.Today.Title)
	t.phrase.SetText(vm.Today.Phrase)
	renderTodayTiles(t.tiles, vm.Today.Tiles)

	items := todayItems(vm)
	if !slices.Equal(items, t.items) {
		t.items = items
		t.rebuildList(items)
	}
	t.reflectSelection(vm.Selection)

	t.calendarTitle.SetText(vm.Today.CalendarTitle)
	t.calendarBody.SetText(vm.Today.CalendarBody)
	if !reflect.DeepEqual(vm.Today.DueGroups, t.dueGroups) {
		t.dueGroups = vm.Today.DueGroups
		t.renderDue(vm.Today)
	}
}

// rebuildList replaces board_today_list's rows with items, in order.
func (t *boardToday) rebuildList(items []todayItem) {
	t.reselecting = true
	defer func() { t.reselecting = false }()
	t.list.RemoveAll()
	for _, it := range items {
		t.list.Append(t.rowFor(it))
	}
}

// rowFor builds one row of board_today_list from it.
func (t *boardToday) rowFor(it todayItem) *gtk.ListBoxRow {
	switch it.kind {
	case todayItemSection:
		return boardTodaySectionHeader(board.StateName(it.state, i18n.Tr), it.count, boardStateDotClass(it.state))
	case todayItemEmptyText:
		return boardPlainTextRow(it.emptyText)
	case todayItemCase:
		r := widget.NewBoardRow()
		r.SetRow(boardRowData(it.row))
		id := it.row.ID
		wireRowMenu(t.p, r.ListBoxRow, id)
		return r.ListBoxRow
	case todayItemMore:
		return boardMoreRow(it.more)
	case todayItemCommitmentsHeading:
		return boardSectionHeader(board.FromAssistant(i18n.Tr))
	case todayItemCommitment:
		row := boardCommitmentRow(t.p, it.commitment)
		wireRowMenu(t.p, row, it.commitment.CaseID)
		return row
	}
	return gtk.NewListBoxRow()
}

// reflectSelection shows selection in board_today_list without reloading,
// as boardColumns.reflectSelection does for one pane: already correct
// (including a selected commitment of the same case) keeps it.
func (t *boardToday) reflectSelection(selection board.CaseID) {
	t.reselecting = true
	defer func() { t.reselecting = false }()
	if selection == "" {
		t.list.UnselectAll()
		return
	}
	if row := t.list.SelectedRow(); row != nil {
		idx := row.Index()
		if idx >= 0 && idx < len(t.items) {
			if id, ok := t.items[idx].caseID(); ok && id == selection {
				return
			}
		}
	}
	for i, it := range t.items {
		if id, ok := it.caseID(); ok && id == selection {
			if row := t.list.RowAtIndex(i); row != nil {
				t.list.SelectRow(row)
			}
			return
		}
	}
	t.list.UnselectAll()
}

// renderDue rebuilds board_today_due_box: a heading and either DueEmpty
// or, per group, a caption and its due items (as board_detail.go's
// renderDetailDue/renderDetailSummary rebuild their own boxes whole).
func (t *boardToday) renderDue(today board.TodayPage) {
	removeAllChildren(t.dueBox)
	head := gtk.NewLabel(board.Deadlines(i18n.Tr))
	head.SetUseMarkup(false)
	head.SetXAlign(0)
	head.AddCSSClass("heading")
	t.dueBox.Append(head)
	if len(today.DueGroups) == 0 {
		empty := gtk.NewLabel(today.DueEmpty)
		empty.SetUseMarkup(false)
		empty.SetXAlign(0)
		empty.SetWrap(true)
		empty.AddCSSClass("dim-label")
		t.dueBox.Append(empty)
		return
	}
	for _, group := range today.DueGroups {
		caption := gtk.NewLabel(group.Title)
		caption.SetUseMarkup(false)
		caption.SetXAlign(0)
		caption.AddCSSClass("caption-heading")
		caption.AddCSSClass("dim-label")
		t.dueBox.Append(caption)
		for _, item := range group.Items {
			t.dueBox.Append(t.dueItemButton(item))
		}
	}
}

// dueItemButton is one deadline of the side card: the case's title, the
// quote that gave the date and the person; a click selects the case.
func (t *boardToday) dueItemButton(item board.DueItem) *gtk.Button {
	btn := gtk.NewButton()
	btn.AddCSSClass("flat")
	btn.AddCSSClass("board-due-item")
	id := item.CaseID
	ctl := t.p.ctl
	btn.ConnectClicked(func() { ctl.Select(id) })

	box := gtk.NewBox(gtk.OrientationVertical, 2)
	title := gtk.NewLabel(boardTitleText(item.Title, item.TitleIsAssistant))
	title.SetUseMarkup(false)
	title.SetXAlign(0)
	title.SetWrap(true)
	box.Append(title)
	if item.Quote != "" {
		quote := gtk.NewLabel(board.Quoted(item.Quote, i18n.Tr))
		quote.SetUseMarkup(false)
		quote.SetXAlign(0)
		quote.SetWrap(true)
		quote.AddCSSClass("caption")
		quote.AddCSSClass("dim-label")
		box.Append(quote)
	}
	if item.Person != "" {
		person := gtk.NewLabel(item.Person)
		person.SetUseMarkup(false)
		person.SetXAlign(0)
		person.AddCSSClass("caption")
		person.AddCSSClass("dim-label")
		box.Append(person)
	}
	btn.SetChild(box)
	return btn
}

// wireRowMenu is a case's or a commitment's right-click and long-press in
// Today's list, as boardColumns.wireCardMenu wires Columns' cards.
func wireRowMenu(p *boardPage, row *gtk.ListBoxRow, id board.CaseID) {
	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_SECONDARY)
	click.ConnectPressed(func(n int, x, y float64) {
		p.ctl.Select(id)
		p.showCaseContextMenu(&row.Widget, id, x, y)
	})
	row.AddController(click)
	long := gtk.NewGestureLongPress()
	long.ConnectPressed(func(x, y float64) {
		p.ctl.Select(id)
		p.showCaseContextMenu(&row.Widget, id, x, y)
	})
	row.AddController(long)
}

// renderTodayTiles rebuilds the count tiles row.
func renderTodayTiles(box *gtk.Box, tiles []board.Tile) {
	removeAllChildren(box)
	for _, tile := range tiles {
		box.Append(boardTileView(tile))
	}
}

// boardTileView is one count tile ("3 Hot"): its count and title, a
// warmer fill while it is the Hot tile.
func boardTileView(tile board.Tile) *gtk.Box {
	b := gtk.NewBox(gtk.OrientationVertical, 2)
	b.AddCSSClass("board-today-tile")
	if tile.Kind == board.TileState && tile.State == board.StateHot {
		b.AddCSSClass("board-today-tile-hot")
	}
	count := gtk.NewLabel(strconv.Itoa(tile.Count))
	count.SetUseMarkup(false)
	count.SetXAlign(0)
	count.AddCSSClass("board-today-tile-count")
	b.Append(count)
	title := gtk.NewLabel(tile.Title)
	title.SetUseMarkup(false)
	title.SetXAlign(0)
	title.SetWrap(true)
	title.AddCSSClass("caption")
	title.AddCSSClass("dim-label")
	b.Append(title)
	b.SetTooltipText(strconv.Itoa(tile.Count) + " " + tile.Title)
	return b
}

// todayItemKind is what one row of board_today_list shows.
type todayItemKind int

// The kinds.
const (
	// todayItemSection is a coloured section header (Hot, Waiting for
	// You) with its count.
	todayItemSection todayItemKind = iota
	// todayItemEmptyText is a section's empty text (no rows in it).
	todayItemEmptyText
	todayItemCase
	// todayItemMore is "and N more" under the top of what waits for the
	// user.
	todayItemMore
	// todayItemCommitmentsHeading is "From the Assistant", before the
	// commitments.
	todayItemCommitmentsHeading
	todayItemCommitment
)

// todayItem is one row: comparable (board.Row and board.CommitmentRow
// included), so apply can skip the rebuild when nothing changed.
type todayItem struct {
	kind       todayItemKind
	state      board.State
	count      int
	emptyText  string
	row        board.Row
	more       int
	commitment board.CommitmentRow
}

// caseID is the case a row selects; "" and false for a header, empty text
// or the "and N more" row.
func (it todayItem) caseID() (board.CaseID, bool) {
	switch it.kind {
	case todayItemCase:
		return it.row.ID, true
	case todayItemCommitment:
		return it.commitment.CaseID, true
	}
	return "", false
}

// todayItems is the ordered rows of board_today_list: Hot (its rows, or
// its empty text), Waiting for You (its rows, "and N more"), then the
// commitments under "From the Assistant". Pure: every string it uses is
// already in vm (ViewModel.Columns' EmptyText, ViewModel.SectionsEmptyText
// as the fallback), so it needs no translator.
func todayItems(vm board.ViewModel) []todayItem {
	t := vm.Today
	var items []todayItem
	items = append(items, todayItem{kind: todayItemSection, state: board.StateHot, count: len(t.Hot)})
	if len(t.Hot) == 0 {
		items = append(items, todayItem{kind: todayItemEmptyText, emptyText: boardColumnEmptyText(vm, board.StateHot)})
	} else {
		for _, r := range t.Hot {
			items = append(items, todayItem{kind: todayItemCase, row: r})
		}
	}
	items = append(items, todayItem{kind: todayItemSection, state: board.StateYou, count: len(t.You) + t.YouMore})
	if len(t.You) == 0 {
		items = append(items, todayItem{kind: todayItemEmptyText, emptyText: boardColumnEmptyText(vm, board.StateYou)})
	} else {
		for _, r := range t.You {
			items = append(items, todayItem{kind: todayItemCase, row: r})
		}
	}
	if t.YouMore > 0 {
		items = append(items, todayItem{kind: todayItemMore, more: t.YouMore})
	}
	if len(t.Commitments) > 0 {
		items = append(items, todayItem{kind: todayItemCommitmentsHeading, count: len(t.Commitments)})
		for _, k := range t.Commitments {
			items = append(items, todayItem{kind: todayItemCommitment, commitment: k})
		}
	}
	return items
}

// boardColumnEmptyText is state's empty-column text from vm.Columns, the
// same text Columns would show (SectionsEmptyText as the fallback, as
// macOS's own Board.Text.sectionEmpty is).
func boardColumnEmptyText(vm board.ViewModel, state board.State) string {
	for _, col := range vm.Columns {
		if col.State == state {
			return col.EmptyText
		}
	}
	return vm.SectionsEmptyText
}

// boardTodaySectionHeader is a coloured section title and its count,
// never selectable.
func boardTodaySectionHeader(title string, count int, stateClass string) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.SetMarginTop(10)
	box.SetMarginBottom(4)
	t := gtk.NewLabel(title)
	t.SetUseMarkup(false)
	t.SetXAlign(0)
	t.SetHExpand(true)
	t.AddCSSClass("heading")
	t.AddCSSClass("board-column-title")
	if stateClass != "" {
		t.AddCSSClass(stateClass)
	}
	box.Append(t)
	c := gtk.NewLabel(strconv.Itoa(count))
	c.SetUseMarkup(false)
	c.AddCSSClass("caption")
	c.AddCSSClass("dim-label")
	box.Append(c)
	row := gtk.NewListBoxRow()
	row.SetChild(box)
	row.SetSelectable(false)
	row.SetActivatable(false)
	return row
}

// boardPlainTextRow is a section's empty text, never selectable.
func boardPlainTextRow(text string) *gtk.ListBoxRow {
	l := gtk.NewLabel(text)
	l.SetUseMarkup(false)
	l.SetXAlign(0)
	l.SetWrap(true)
	l.AddCSSClass("dim-label")
	row := gtk.NewListBoxRow()
	row.SetChild(l)
	row.SetSelectable(false)
	row.SetActivatable(false)
	return row
}

// boardMoreRow is "and N more" under the top of what waits for the user
// (Controller.ShowWaitingForYou, wireToday).
func boardMoreRow(n int) *gtk.ListBoxRow {
	l := gtk.NewLabel(board.AndMore(n, i18n.Tr))
	l.SetUseMarkup(false)
	l.SetXAlign(0)
	l.AddCSSClass("link")
	row := gtk.NewListBoxRow()
	row.SetChild(l)
	return row
}
