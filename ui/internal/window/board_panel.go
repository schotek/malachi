// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// The sliding detail panel over Columns and Today
// (Adw.OverlaySplitView board_panel_split, board_page.blp): shows the
// selected case's detail — the ONE detail widget tree board_detail.go
// fills (board_detail_tree), reparented here while the style is not List.
// The List style keeps its own collapse/push (board_list_split's own
// Adw.Breakpoint); boardPanelShown never applies to it — List's own
// inline/collapsed detail is board_list.go's applyInlineDetail, not this
// file. Reference: macOS BoardPageViewController.swift (the panel),
// BoardDetailViewController.swift.

// boardPanel owns the split view's chrome (title, Close) and moves
// board_detail_tree between board_detail_page (the List style) and here.
type boardPanel struct {
	p *boardPage

	split   *adw.OverlaySplitView
	content *adw.ToolbarView
	title   *adw.WindowTitle
	close   *gtk.Button

	// inPanel: board_detail_tree currently lives in board_panel_content;
	// false while it is in board_detail_page (the List style).
	inPanel bool
}

// bindPanel fetches the panel's own widgets; called from ensureBoard
// after p.bind (which fetches p.detailTree, board_detail_tree).
func (p *boardPage) bindPanel(b *gtk.Builder) {
	pn := &boardPanel{p: p}
	pn.split = b.GetObject("board_panel_split").Cast().(*adw.OverlaySplitView)
	pn.content = b.GetObject("board_panel_content").Cast().(*adw.ToolbarView)
	pn.title = b.GetObject("board_panel_title").Cast().(*adw.WindowTitle)
	pn.close = b.GetObject("board_panel_close").Cast().(*gtk.Button)
	p.panel = pn
}

// wirePanel sets the Close button's text and wires it to close the panel
// (clearing the selection, like board_actions.go's actions do for the rest
// of the detail). Escape is the whole page's (board_keys.go
// wireBoardKeys), not only the panel's.
func (p *boardPage) wirePanel() {
	pn := p.panel
	pn.close.SetLabel(board.Close(i18n.Tr))
	pn.close.SetUseUnderline(true)
	pn.close.ConnectClicked(func() { p.ctl.Select("") })
}

// renderPanel shows the panel exactly while boardPanelShown and moves the
// ONE detail widget tree to wherever it belongs for the current style;
// called from board.go's applyAll and onChange on every style, content or
// selection change.
func (p *boardPage) renderPanel(vm board.ViewModel) {
	pn := p.panel
	style := p.ctl.State().Style

	wantsTreeInPanel := boardDetailBelongsToPanel(style)
	if wantsTreeInPanel != pn.inPanel {
		pn.move(wantsTreeInPanel)
	}

	title := board.BoardName(i18n.Tr)
	if d := vm.Detail; d != nil {
		title = d.StateTitle
	}
	pn.title.SetTitle(title)

	pn.split.SetShowSidebar(boardPanelShown(style, vm.Selection != "", vm.IsEmpty))
}

// move relocates board_detail_tree: out of its current home first (a
// GtkWidget has only one parent at a time; both AdwNavigationPage.SetChild
// and AdwToolbarView.SetContent accept nil to let go of it), then into
// the new one.
func (pn *boardPanel) move(toPanel bool) {
	tree := pn.p.detailTree
	if toPanel {
		pn.p.detailPage.SetChild(nil)
		pn.content.SetContent(tree)
	} else {
		pn.content.SetContent(nil)
		pn.p.detailPage.SetChild(tree)
	}
	pn.inPanel = toPanel
}

// boardPanelShown is whether the sliding panel is shown: a case selected,
// the board not empty (the empty page replaces everything) and the style
// not List (which keeps its own inline/collapsed detail instead).
func boardPanelShown(style board.Style, hasSelection, isEmpty bool) bool {
	return !isEmpty && hasSelection && style != board.StyleList
}

// boardDetailBelongsToPanel is where the ONE detail widget tree
// (board_detail_tree) should live for style: the panel for every style
// but List.
func boardDetailBelongsToPanel(style board.Style) bool {
	return style != board.StyleList
}
