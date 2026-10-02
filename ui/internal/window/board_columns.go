// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"slices"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The board's Columns style: the four states side by side on the board's
// tinted surface (board_columns_scroller/board_columns_row,
// board_page.blp), each a header (the state's title in its colour and the
// count) over a ListBox of cards; the first column (Hot) ends with the
// commitments under "From the Assistant". The columns share the width
// equally (homogeneous in the blueprint), none narrower than
// boardColumnMinWidth; when the page is narrower, board_columns_scroller
// scrolls sideways instead of widening the window.
//
// A card is widget.BoardRow styled as a floating card (the "board-card-row"
// CSS class, internal/style), not a separate widget: same fields, same
// plain-text rule (CLAUDE.md rule 3), reused as the List style's row is.
//
// One selection across the four lists: the list holding the selected case
// selects its row, the others none (reflectSelection); a right-click or
// long press reuses board_actions.go's context menu. Reference: macOS
// BoardColumnsViewController.swift.

// boardColumnMinWidth is a column's minimum width (board_columns.blp's
// "homogeneous: true" shares the rest equally); matches macOS's
// minColumnWidth closely enough for a first pass.
const boardColumnMinWidth = 240

// boardColumnPane is one column: its header labels, its list and the rows
// it currently shows (for the no-op-when-unchanged redraw).
type boardColumnPane struct {
	state     board.State
	container *gtk.Box
	title     *gtk.Label
	count     *gtk.Label
	list      *gtk.ListBox
	items     []columnItem
}

// boardColumns owns the four panes, built once into board_columns_row.
type boardColumns struct {
	p           *boardPage
	row         *gtk.Box
	panes       [4]*boardColumnPane
	reselecting bool
}

// bindColumns builds the four panes into board_columns_row (board.States
// order), once; called from ensureBoard after p.bind.
func (p *boardPage) bindColumns(b *gtk.Builder) {
	c := &boardColumns{p: p, row: b.GetObject("board_columns_row").Cast().(*gtk.Box)}
	for i, st := range board.States {
		c.panes[i] = c.newPane(st)
	}
	p.columns = c
}

// newPane builds one column's header and list, appends it to the row and
// wires its selection.
func (c *boardColumns) newPane(state board.State) *boardColumnPane {
	container := gtk.NewBox(gtk.OrientationVertical, 0)
	container.SetSizeRequest(boardColumnMinWidth, -1)
	container.SetHExpand(true)

	header := gtk.NewBox(gtk.OrientationHorizontal, 8)
	header.SetMarginStart(2)
	header.SetMarginEnd(2)
	header.SetMarginBottom(8)
	title := gtk.NewLabel("")
	title.SetUseMarkup(false)
	title.SetXAlign(0)
	title.SetHExpand(true)
	title.SetEllipsize(pango.EllipsizeEnd)
	title.AddCSSClass("heading")
	title.AddCSSClass("board-column-title")
	if cl := boardStateDotClass(state); cl != "" {
		title.AddCSSClass(cl)
	}
	header.Append(title)
	count := gtk.NewLabel("")
	count.SetUseMarkup(false)
	count.AddCSSClass("caption")
	count.AddCSSClass("dim-label")
	header.Append(count)
	container.Append(header)

	scroller := gtk.NewScrolledWindow()
	scroller.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroller.SetVExpand(true)
	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionSingle)
	list.AddCSSClass("navigation-sidebar")
	list.AddCSSClass("board-columns-list")
	scroller.SetChild(list)
	container.Append(scroller)

	c.row.Append(container)

	pane := &boardColumnPane{state: state, container: container, title: title, count: count, list: list}
	list.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if c.reselecting || row == nil {
			return
		}
		c.choose(pane, row.Index())
	})
	return pane
}

// choose is pane's row index selected by the user (a click or an arrow
// key): the controller selects its case; reflectSelection (via the
// controller's own change notification) then shows it in every pane.
// Header and placeholder rows are never selectable (SetSelectable(false)),
// so index always names a case or a commitment here.
func (c *boardColumns) choose(pane *boardColumnPane, index int) {
	if index < 0 || index >= len(pane.items) {
		return
	}
	if id, ok := pane.items[index].caseID(); ok {
		c.p.ctl.Select(id)
	}
}

// apply is board.go's applyAll/onChange hook: idempotent, and cheap when
// none of the four panes' rows or the selection changed (board.go calls
// it on ChangeContent, ChangeSelection and ChangeStyle alike, since a
// style switch alone does not already imply ChangeContent).
func (c *boardColumns) apply(vm board.ViewModel) {
	if c == nil {
		return
	}
	for i, col := range vm.Columns {
		if i >= len(c.panes) {
			break
		}
		pane := c.panes[i]
		pane.title.SetText(col.Title)
		pane.count.SetText(boardCountText(len(col.Rows)))
		pane.count.SetVisible(len(col.Rows) > 0)

		items := columnItems(col, vm.Commitments, col.State == board.StateHot)
		if slices.Equal(items, pane.items) {
			continue
		}
		pane.items = items
		c.rebuildPane(pane, items)
	}
	c.reflectSelection(vm.Selection)
}

// rebuildPane replaces pane's rows with items, in order.
func (c *boardColumns) rebuildPane(pane *boardColumnPane, items []columnItem) {
	c.reselecting = true
	defer func() { c.reselecting = false }()
	pane.list.RemoveAll()
	for _, it := range items {
		pane.list.Append(c.rowFor(it))
	}
}

// rowFor builds one row of a column's list from it.
func (c *boardColumns) rowFor(it columnItem) *gtk.ListBoxRow {
	switch it.kind {
	case columnItemCase:
		r := widget.NewBoardRow()
		r.SetRow(boardRowData(it.row))
		r.AddCSSClass("board-card-row")
		if it.row.State == board.StateHot {
			r.AddCSSClass("board-card-row-hot")
		}
		id := it.row.ID
		c.wireCardMenu(r.ListBoxRow, id)
		return r.ListBoxRow
	case columnItemPlaceholder:
		return boardColumnPlaceholderRow(it.placeholder)
	case columnItemCommitmentsHeading:
		return boardSectionHeader(board.FromAssistant(i18n.Tr))
	case columnItemCommitment:
		row := boardCommitmentRow(c.p, it.commitment)
		c.wireCardMenu(row, it.commitment.CaseID)
		return row
	}
	return gtk.NewListBoxRow()
}

// wireCardMenu is a card's or a commitment's right-click and long-press:
// selects id first (the menu then acts on "the selection", like
// board_list.go's renderList).
func (c *boardColumns) wireCardMenu(row *gtk.ListBoxRow, id board.CaseID) {
	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_SECONDARY)
	click.ConnectPressed(func(n int, x, y float64) {
		c.p.ctl.Select(id)
		c.p.showCaseContextMenu(&row.Widget, id, x, y)
	})
	row.AddController(click)
	long := gtk.NewGestureLongPress()
	long.ConnectPressed(func(x, y float64) {
		c.p.ctl.Select(id)
		c.p.showCaseContextMenu(&row.Widget, id, x, y)
	})
	row.AddController(long)
}

// reflectSelection shows selection in whichever pane already holds it
// (its card, or — when the user picked it there — a commitment of the
// same case, which stays the selected row instead of jumping to the
// card); every other pane selects nothing.
func (c *boardColumns) reflectSelection(selection board.CaseID) {
	c.reselecting = true
	defer func() { c.reselecting = false }()
	for _, pane := range c.panes {
		if pane == nil {
			continue
		}
		if selection == "" {
			pane.list.UnselectAll()
			continue
		}
		if id, ok := selectedCaseOf(pane); ok && id == selection {
			continue
		}
		if i, ok := indexOfCase(pane.items, selection); ok {
			if row := pane.list.RowAtIndex(i); row != nil {
				pane.list.SelectRow(row)
				continue
			}
		}
		pane.list.UnselectAll()
	}
}

// selectedCaseOf is the case of pane's currently selected row, if any.
func selectedCaseOf(pane *boardColumnPane) (board.CaseID, bool) {
	row := pane.list.SelectedRow()
	if row == nil {
		return "", false
	}
	idx := row.Index()
	if idx < 0 || idx >= len(pane.items) {
		return "", false
	}
	return pane.items[idx].caseID()
}

// indexOfCase is the first row of items showing case id: its card, else a
// commitment of it (so a card is preferred when both exist).
func indexOfCase(items []columnItem, id board.CaseID) (int, bool) {
	for i, it := range items {
		if it.kind == columnItemCase && it.row.ID == id {
			return i, true
		}
	}
	for i, it := range items {
		if it.kind == columnItemCommitment && it.commitment.CaseID == id {
			return i, true
		}
	}
	return -1, false
}

// columnItemKind is what one row of a column's list shows.
type columnItemKind int

// The kinds.
const (
	columnItemCase columnItemKind = iota
	// columnItemPlaceholder is the empty column's dashed card.
	columnItemPlaceholder
	// columnItemCommitmentsHeading is "From the Assistant", before the
	// commitments (the first column only).
	columnItemCommitmentsHeading
	columnItemCommitment
)

// columnItem is one row of a column's list: comparable (every field is a
// plain value, board.Row and board.CommitmentRow included), so render can
// skip a pane whose rows did not change (slices.Equal).
type columnItem struct {
	kind        columnItemKind
	row         board.Row
	placeholder string
	commitment  board.CommitmentRow
}

// caseID is the case a row selects; "" and false for a header or a
// placeholder.
func (it columnItem) caseID() (board.CaseID, bool) {
	switch it.kind {
	case columnItemCase:
		return it.row.ID, true
	case columnItemCommitment:
		return it.commitment.CaseID, true
	}
	return "", false
}

// columnItems is the rows one column's list shows: the cases, or the
// column's placeholder when there are none; first (the Hot column) also
// gets the commitments under "From the Assistant".
func columnItems(col board.Column, commitments []board.CommitmentRow, first bool) []columnItem {
	var items []columnItem
	if len(col.Rows) == 0 {
		items = append(items, columnItem{kind: columnItemPlaceholder, placeholder: col.EmptyText})
	} else {
		for _, r := range col.Rows {
			items = append(items, columnItem{kind: columnItemCase, row: r})
		}
	}
	if first && len(commitments) > 0 {
		items = append(items, columnItem{kind: columnItemCommitmentsHeading})
		for _, k := range commitments {
			items = append(items, columnItem{kind: columnItemCommitment, commitment: k})
		}
	}
	return items
}

// boardColumnPlaceholderRow is an empty column's dashed card, centred
// text, never selectable (as board_list.go's boardSectionHeader is not).
func boardColumnPlaceholderRow(text string) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	box.AddCSSClass("board-column-placeholder")
	box.SetMarginStart(4)
	box.SetMarginEnd(4)
	box.SetMarginTop(2)
	box.SetMarginBottom(2)
	label := gtk.NewLabel(text)
	label.SetUseMarkup(false)
	label.SetXAlign(0.5)
	label.SetJustify(gtk.JustifyCenter)
	label.SetMarginTop(18)
	label.SetMarginBottom(18)
	label.SetWrap(true)
	label.AddCSSClass("dim-label")
	label.AddCSSClass("caption")
	box.Append(label)
	row := gtk.NewListBoxRow()
	row.SetChild(box)
	row.SetSelectable(false)
	row.SetActivatable(false)
	return row
}

// boardCommitmentRow is one commitment: a tick that marks it done, its
// text and case, and its due date — shared by Columns (the first column)
// and Today. A click selects its case, as a card does.
func boardCommitmentRow(p *boardPage, k board.CommitmentRow) *gtk.ListBoxRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.SetMarginStart(4)
	box.SetMarginEnd(4)
	box.SetMarginTop(6)
	box.SetMarginBottom(6)
	check := gtk.NewCheckButton()
	check.SetTooltipText(board.MarkPromiseDone(i18n.Tr))
	check.SetVAlign(gtk.AlignStart)
	id := k.ID
	ctl := p.ctl
	check.ConnectToggled(func() { ctl.SetCommitmentDone(id, check.Active()) })
	box.Append(check)

	text := gtk.NewBox(gtk.OrientationVertical, 2)
	text.SetHExpand(true)
	main := gtk.NewLabel(k.Text)
	main.SetUseMarkup(false)
	main.SetXAlign(0)
	main.SetWrap(true)
	text.Append(main)
	from := gtk.NewLabel(boardTitleText(k.From, k.FromIsAssistant))
	from.SetUseMarkup(false)
	from.SetXAlign(0)
	from.SetWrap(true)
	from.AddCSSClass("caption")
	from.AddCSSClass("dim-label")
	text.Append(from)
	if k.Due != "" {
		due := gtk.NewLabel(k.Due)
		due.SetUseMarkup(false)
		due.SetXAlign(0)
		due.AddCSSClass("caption")
		text.Append(due)
	}
	box.Append(text)

	row := gtk.NewListBoxRow()
	row.SetChild(box)
	return row
}
