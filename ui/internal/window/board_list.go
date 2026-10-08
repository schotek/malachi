// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"slices"
	"strconv"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The board page's header (the mode and style switches, the title, the
// account filter), the empty/list/placeholder pages of board_stack, the
// List style's navigation column (the state and Done filters, the
// accounts) and its case list. board_detail.go renders the selected case;
// board_actions.go the actions, the Remind and context menus;
// board_triage_button.go the Triage control.

// bind fetches every widget board.go and the other board_*.go files need,
// from board_page.blp's builder.
func (p *boardPage) bind(b *gtk.Builder) {
	p.root = b.GetObject("board_page_root").Cast().(*adw.BreakpointBin)
	p.modeSwitch = b.GetObject("board_mode_switch").Cast().(*adw.ToggleGroup)
	p.styleSwitch = b.GetObject("board_style_switch").Cast().(*adw.ToggleGroup)
	p.title = b.GetObject("board_title").Cast().(*adw.WindowTitle)
	p.accountFilter = b.GetObject("board_account_filter").Cast().(*gtk.DropDown)
	p.triageButton = b.GetObject("board_triage_button").Cast().(*gtk.Button)
	p.menuButton = b.GetObject("board_menu_button").Cast().(*gtk.MenuButton)
	p.triageActivity = b.GetObject("board_triage_activity").Cast().(*gtk.Box)
	p.triageProgress = b.GetObject("board_triage_progress").Cast().(*gtk.Label)

	p.stack = b.GetObject("board_stack").Cast().(*gtk.Stack)
	p.emptyPage = b.GetObject("board_empty_page").Cast().(*adw.StatusPage)
	p.notice = b.GetObject("board_notice").Cast().(*adw.Banner)
	p.panelNotice = b.GetObject("board_panel_notice").Cast().(*adw.Banner)

	p.navSplit = b.GetObject("board_nav_split").Cast().(*adw.NavigationSplitView)
	p.navList = b.GetObject("board_nav_list").Cast().(*gtk.ListBox)

	p.statusButton = b.GetObject("board_status_button").Cast().(*gtk.MenuButton)
	p.syncSpinner = b.GetObject("board_sync_spinner").Cast().(*adw.Spinner)
	p.connIcon = b.GetObject("board_connection_icon").Cast().(*gtk.Image)
	p.syncLabel = b.GetObject("board_sync_label").Cast().(*gtk.Label)

	p.listSplit = b.GetObject("board_list_split").Cast().(*adw.NavigationSplitView)
	p.listStack = b.GetObject("board_list_stack").Cast().(*gtk.Stack)
	p.listEmptyPage = b.GetObject("board_list_empty_page").Cast().(*adw.StatusPage)
	p.caseList = b.GetObject("board_case_list").Cast().(*gtk.ListBox)

	p.detailPage = b.GetObject("board_detail_page").Cast().(*adw.NavigationPage)
	p.detailTree = b.GetObject("board_detail_tree").Cast().(*adw.ToolbarView)
	p.doneButton = b.GetObject("board_done_button").Cast().(*gtk.Button)
	p.remindButton = b.GetObject("board_remind_button").Cast().(*gtk.MenuButton)
	p.archiveButton = b.GetObject("board_archive_button").Cast().(*gtk.Button)
	p.detailMenu = b.GetObject("board_detail_menu").Cast().(*gtk.MenuButton)
	p.replyButton = b.GetObject("board_reply_button").Cast().(*gtk.Button)
	p.detailStack = b.GetObject("board_detail_stack").Cast().(*gtk.Stack)
	p.noSelectionPage = b.GetObject("board_no_selection_page").Cast().(*adw.StatusPage)
	p.detailTop = b.GetObject("board_detail_top").Cast().(*gtk.Box)
	p.dueBox = b.GetObject("board_detail_due_box").Cast().(*gtk.Box)
	p.summaryBox = b.GetObject("board_detail_summary_box").Cast().(*gtk.Box)
	p.replySlot = b.GetObject("reply_slot").Cast().(*gtk.Box)
	p.conversationBox = b.GetObject("board_detail_conversation_box").Cast().(*gtk.Box)
	p.commitmentsBox = b.GetObject("board_detail_commitments_box").Cast().(*gtk.Box)
}

// wire connects every signal of the board page that is not specific to the
// detail or the actions (board_detail.go, board_actions.go do their own).
func (p *boardPage) wire() {
	w := p.w
	for _, m := range board.Modes {
		if t := p.modeSwitch.ToggleByName(modeNick(m)); t != nil {
			t.SetTooltip(modeTitle(m))
		}
	}
	p.modeSwitch.SetActiveName(modeNick(w.mode))
	p.modeSwitch.NotifyProperty("active-name", func() {
		if w.settingModeSwitch {
			return
		}
		w.setMode(parseModeNick(p.modeSwitch.ActiveName()))
	})

	for _, s := range board.Styles {
		if t := p.styleSwitch.ToggleByName(styleNick(s)); t != nil {
			t.SetLabel(board.StyleTitle(s, i18n.Tr))
		}
	}
	p.styleSwitch.NotifyProperty("active-name", func() {
		if p.settingStyle {
			return
		}
		p.ctl.SetStyle(parseStyleNick(p.styleSwitch.ActiveName()))
		p.applyInlineDetail()
	})

	p.accountFilter.SetFactory(&boardAccountFactory().ListItemFactory)
	p.accountFilter.NotifyProperty("selected", func() {
		if p.settingAccount {
			return
		}
		i := int(p.accountFilter.Selected())
		if i < 0 || i >= len(p.accountFilterIDs) {
			return
		}
		p.ctl.SetAccount(p.accountFilterIDs[i])
	})

	p.navList.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if row == nil || p.reselecting {
			return
		}
		p.onNavRowSelected(row.Index())
	})
	p.caseList.ConnectRowSelected(func(row *gtk.ListBoxRow) {
		if p.reselecting {
			return
		}
		if row == nil {
			p.ctl.Select("")
			return
		}
		if id, ok := p.caseIDAt(row.Index()); ok {
			p.ctl.Select(id)
			if !p.ctl.State().InlineDetail {
				p.listSplit.SetShowContent(true)
			}
		}
	})
	p.caseList.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		// A collapsed List style pushes the detail (board_page.blp's own
		// Adw.Breakpoint objects fold board_list_split below 640sp); with
		// room beside the list, activation is the same as selection
		// (handled by row-selected already).
		if !p.ctl.State().InlineDetail {
			p.listSplit.SetShowContent(true)
		}
	})

	// The List style has room for the detail beside it exactly while it
	// shows and board_list_split is not collapsed (its own Adw.Breakpoint,
	// set in board_page.blp); every other style has no inline detail of
	// its own (board_panel.go's sliding panel instead, applyInlineDetail).
	p.listSplit.NotifyProperty("collapsed", p.applyInlineDetail)
	p.applyInlineDetail()
}

// wireMenuButton gives the board's header the window's main menu: the
// model of Mail's own main menu button (window.blp's primary_menu, kept
// from the window's builder in New) and its tooltip.
func (p *boardPage) wireMenuButton() {
	p.menuButton.SetTooltipText(mainMenuTooltip())
	if m := p.w.mainMenu; m != nil {
		p.menuButton.SetMenuModel(m)
	}
}

// applyInlineDetail keeps Controller.State().InlineDetail following
// whether the List style currently has room for the detail beside it:
// false whenever another style shows, so a style switch away from List
// never leaves a stale true behind for ResolveSelection/ShowsPanel to read
// (board/view.go) the next time List shows; called here, by
// board_list_split's own "collapsed" notify and by the style switch above.
func (p *boardPage) applyInlineDetail() {
	if p.ctl.State().Style != board.StyleList {
		p.ctl.SetInlineDetail(false)
		return
	}
	p.ctl.SetInlineDetail(!p.listSplit.Collapsed())
}

// styleNick and parseStyleNick are the style switch's "active-name" values
// (board.Style.Nick, already the settings' own nicks).
func styleNick(s board.Style) string      { return s.Nick() }
func parseStyleNick(s string) board.Style { return board.ParseStyle(s) }

// renderHeader shows the title, subtitle, style switch and account filter.
func (p *boardPage) renderHeader(vm board.ViewModel) {
	p.title.SetTitle(board.BoardName(i18n.Tr))
	p.title.SetSubtitle(vm.Subtitle)

	p.settingStyle = true
	p.styleSwitch.SetActiveName(styleNick(p.ctl.State().Style))
	p.settingStyle = false

	p.settingAccount = true
	ids := make([]api.AccountID, 0, len(vm.Accounts))
	labels := make([]string, 0, len(vm.Accounts))
	selected := uint(0)
	for i, a := range vm.Accounts {
		ids = append(ids, a.Filter)
		labels = append(labels, a.Label)
		if a.Selected {
			selected = uint(i)
		}
	}
	// The same entries as last time keep the model: a fresh one on every
	// change of the board would close an open dropdown under the pointer.
	if !slices.Equal(labels, p.accountFilterLabels) {
		p.accountFilterLabels = labels
		p.accountFilter.SetModel(gtk.NewStringList(labels))
	}
	p.accountFilterIDs = ids
	if p.accountFilter.Selected() != selected {
		p.accountFilter.SetSelected(selected)
	}
	p.settingAccount = false
}

// boardAccountFactory renders one entry of the account filter: an eliding
// label, as compose's From row does (compose.go fromFactory), never markup.
func boardAccountFactory() *gtk.SignalListItemFactory {
	f := gtk.NewSignalListItemFactory()
	f.ConnectSetup(func(obj *coreglib.Object) {
		item, ok := obj.Cast().(*gtk.ListItem)
		if !ok {
			return
		}
		l := gtk.NewLabel("")
		l.SetUseMarkup(false)
		l.SetXAlign(0)
		l.SetEllipsize(pango.EllipsizeEnd)
		l.SetMaxWidthChars(24)
		item.SetChild(l)
	})
	f.ConnectBind(func(obj *coreglib.Object) {
		item, ok := obj.Cast().(*gtk.ListItem)
		if !ok {
			return
		}
		l, ok := item.Child().(*gtk.Label)
		if !ok {
			return
		}
		if s, ok := item.Item().Cast().(*gtk.StringObject); ok {
			l.SetLabel(s.String())
		}
	})
	return f
}

// renderStack shows the page that belongs on screen: the empty state
// (ViewModel.IsEmpty, or a phase with nothing else to show), or the current
// List, Columns or Today style.
func (p *boardPage) renderStack(vm board.ViewModel) {
	setStatusPage(p.emptyPage, "view-grid-symbolic", vm.EmptyTitle, vm.EmptyBody)
	style := p.ctl.State().Style
	shown := vm.Notice != "" && !vm.IsEmpty
	p.notice.SetTitle(vm.Notice)
	p.notice.SetRevealed(shown && style == board.StyleList)
	p.panelNotice.SetTitle(vm.Notice)
	p.panelNotice.SetRevealed(shown && style != board.StyleList)

	switch {
	case vm.IsEmpty:
		p.stack.SetVisibleChildName("empty")
	case style == board.StyleColumns:
		p.stack.SetVisibleChildName("columns")
	case style == board.StyleToday:
		p.stack.SetVisibleChildName("today")
	default:
		p.stack.SetVisibleChildName("list")
	}
}

// renderNav shows the navigation column: the state filters with their
// colour dot and count, Overview, Snoozed and Done, then the accounts with
// their kind capsule, as ViewModel orders them. Rows of the same kinds as
// last time are updated in place (the counts move on every change of the
// board, and a rebuilt row would take the keyboard and an arrow key's
// position with it); a different shape rebuilds the column.
func (p *boardPage) renderNav(vm board.ViewModel) {
	p.reselecting = true
	defer func() { p.reselecting = false }()
	if len(p.navRows) != len(vm.Nav)+len(vm.Accounts) || p.navFilters != len(vm.Nav) {
		refocus := listHoldsFocus(p.w, p.navList)
		p.navList.RemoveAll()
		p.navRows = p.navRows[:0]
		for range vm.Nav {
			n := newBoardNavRow(false)
			p.navList.Append(n.row)
			p.navRows = append(p.navRows, n)
		}
		for range vm.Accounts {
			n := newBoardNavRow(true)
			p.navList.Append(n.row)
			p.navRows = append(p.navRows, n)
		}
		p.navFilters = len(vm.Nav)
		defer func() {
			if refocus {
				focusSelectedRow(p.navList)
			}
		}()
	}
	selected := -1
	for i, n := range vm.Nav {
		p.navRows[i].setNav(n)
		if n.Selected {
			selected = i
		}
	}
	for j, a := range vm.Accounts {
		i := len(vm.Nav) + j
		p.navRows[i].setAccount(a)
		if a.Selected {
			selected = i
		}
	}
	if selected < 0 {
		p.navList.UnselectAll()
		return
	}
	if row := p.navList.RowAtIndex(selected); row != nil {
		if cur := p.navList.SelectedRow(); cur == nil || cur.Index() != selected {
			p.navList.SelectRow(row)
		}
	}
}

// boardNavRow is one row of the navigation column, kept across changes: a
// filter (the state's colour dot, or none for Overview, Snoozed and Done;
// the title; the count) or an account (its name, kind capsule and count).
type boardNavRow struct {
	row      *gtk.ListBoxRow
	dot      *gtk.Box
	title    *gtk.Label
	badge    *gtk.Label
	count    *gtk.Label
	dotClass string
}

// newBoardNavRow builds an empty row; account: the account kind (a kind
// capsule instead of a dot).
func newBoardNavRow(account bool) *boardNavRow {
	n := &boardNavRow{}
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.SetMarginStart(6)
	box.SetMarginEnd(6)
	box.SetMarginTop(4)
	box.SetMarginBottom(4)
	if !account {
		n.dot = gtk.NewBox(gtk.OrientationHorizontal, 0)
		n.dot.SetSizeRequest(10, 10)
		n.dot.SetVAlign(gtk.AlignCenter)
		n.dot.AddCSSClass("board-state-dot")
		box.Append(n.dot)
	}
	n.title = gtk.NewLabel("")
	n.title.SetUseMarkup(false)
	n.title.SetXAlign(0)
	n.title.SetHExpand(true)
	n.title.SetEllipsize(pango.EllipsizeEnd)
	box.Append(n.title)
	if account {
		n.badge = widget.NewKindBadge("")
		box.Append(n.badge)
	}
	n.count = gtk.NewLabel("")
	n.count.SetUseMarkup(false)
	n.count.AddCSSClass("caption")
	n.count.AddCSSClass("numeric")
	n.count.AddCSSClass("board-nav-count")
	box.Append(n.count)
	n.row = gtk.NewListBoxRow()
	n.row.SetChild(box)
	n.row.AddCSSClass("board-nav-row")
	return n
}

// setNav shows filter item it.
func (n *boardNavRow) setNav(it board.NavItem) {
	if n.dot != nil {
		class := ""
		if it.HasDot {
			class = boardStateDotClass(it.Dot)
		}
		if class != n.dotClass {
			if n.dotClass != "" {
				n.dot.RemoveCSSClass(n.dotClass)
			}
			if class != "" {
				n.dot.AddCSSClass(class)
			}
			n.dotClass = class
		}
		if it.HasDot {
			n.dot.SetOpacity(1)
		} else {
			n.dot.SetOpacity(0)
		}
	}
	n.title.SetText(it.Title)
	n.setCount(it.Count)
}

// setAccount shows account item a.
func (n *boardNavRow) setAccount(a board.AccountItem) {
	n.title.SetText(a.Title)
	if n.badge != nil {
		n.badge.SetText(a.Badge)
		n.badge.SetVisible(a.Badge != "")
	}
	n.setCount(a.Count)
}

func (n *boardNavRow) setCount(c int) {
	n.count.SetText(boardCountText(c))
	n.count.SetVisible(c > 0)
}

// boardCountText is a nav row's count badge: nothing below one.
func boardCountText(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// onNavRowSelected applies the filter or account the user picked in the
// navigation column: the first len(vm.Nav) rows are the state filters and
// Overview/Done, the rest the accounts, in the order renderNav built them.
func (p *boardPage) onNavRowSelected(index int) {
	vm := p.ctl.View()
	if index < len(vm.Nav) {
		p.ctl.SetFilter(vm.Nav[index].Filter)
		return
	}
	j := index - len(vm.Nav)
	if j >= 0 && j < len(vm.Accounts) {
		p.ctl.SetAccount(vm.Accounts[j].Filter)
	}
}

// renderList shows the case list: the sections with their headers, or the
// empty page. Rows are keyed by case id (headers by their section) and
// kept across changes: an existing row is updated in place, only rows that
// came or went are inserted or removed, and a row that moved is moved
// alone. ChangeContent fires on every autosave, annotation and boardChanged,
// so a rebuild would take the keyboard and the arrow-key position from
// under the user; if the focused row had to move anyway, the keyboard goes
// back to the selected row.
func (p *boardPage) renderList(vm board.ViewModel) {
	p.listEmptyPage.SetTitle(board.SectionEmpty(i18n.Tr))
	if len(vm.Sections) == 0 {
		p.listStack.SetVisibleChildName("empty")
		return
	}
	p.listStack.SetVisibleChildName("rows")
	p.reselecting = true
	defer func() { p.reselecting = false }()
	refocus := listHoldsFocus(p.w, p.caseList)
	if p.caseRows == nil {
		p.caseRows = make(map[board.CaseID]*widget.BoardRow)
		p.headerRows = make(map[boardSectionKey]*boardHeaderRow)
	}

	var want []*gtk.ListBoxRow
	order := make([]board.CaseID, 0, len(p.caseOrder))
	keepCases := make(map[board.CaseID]bool)
	keepHeaders := make(map[boardSectionKey]bool)
	for _, sec := range vm.Sections {
		key := boardSectionKey{kind: sec.Kind, state: sec.State}
		h := p.headerRows[key]
		if h == nil {
			h = newBoardHeaderRow()
			p.headerRows[key] = h
		}
		h.label.SetText(sec.Title)
		keepHeaders[key] = true
		want = append(want, h.row)
		order = append(order, "")
		for _, row := range sec.Rows {
			r := p.caseRows[row.ID]
			if r == nil {
				r = p.newCaseRow(row.ID)
				p.caseRows[row.ID] = r
			}
			r.SetRow(boardRowData(row))
			keepCases[row.ID] = true
			want = append(want, r.ListBoxRow)
			order = append(order, row.ID)
		}
	}
	for id, r := range p.caseRows {
		if !keepCases[id] {
			if r.Parent() != nil {
				p.caseList.Remove(r.ListBoxRow)
			}
			delete(p.caseRows, id)
		}
	}
	for key, h := range p.headerRows {
		if !keepHeaders[key] {
			if h.row.Parent() != nil {
				p.caseList.Remove(h.row)
			}
			delete(p.headerRows, key)
		}
	}
	for i, row := range want {
		if cur := p.caseList.RowAtIndex(i); cur != nil && sameObject(cur, row) {
			continue
		}
		if row.Parent() != nil {
			p.caseList.Remove(row)
		}
		p.caseList.Insert(row, i)
	}
	p.caseOrder = order
	p.syncListSelection(vm)
	if refocus && !listHoldsFocus(p.w, p.caseList) {
		focusSelectedRow(p.caseList)
	}
}

// boardSectionKey names a section of the case list across changes.
type boardSectionKey struct {
	kind  board.SectionKind
	state board.State
}

// boardHeaderRow is a section header kept across changes.
type boardHeaderRow struct {
	row   *gtk.ListBoxRow
	label *gtk.Label
}

func newBoardHeaderRow() *boardHeaderRow {
	row, label := boardSectionHeaderParts("")
	return &boardHeaderRow{row: row, label: label}
}

// newCaseRow is case id's row with its context menu (right-click, long
// press); filled by renderList.
func (p *boardPage) newCaseRow(id board.CaseID) *widget.BoardRow {
	r := widget.NewBoardRow()
	anchor := &r.ListBoxRow.Widget
	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_SECONDARY)
	click.ConnectPressed(func(n int, x, y float64) {
		p.ctl.Select(id)
		p.showCaseContextMenu(anchor, id, x, y)
	})
	r.ListBoxRow.AddController(click)
	long := gtk.NewGestureLongPress()
	long.ConnectPressed(func(x, y float64) {
		p.ctl.Select(id)
		p.showCaseContextMenu(anchor, id, x, y)
	})
	r.ListBoxRow.AddController(long)
	return r
}

// sameObject reports whether a and b wrap the same GObject (gotk4 may hand
// out a fresh Go wrapper for an object it already wrapped).
func sameObject(a, b coreglib.Objector) bool {
	return coreglib.InternObject(a).Native() == coreglib.InternObject(b).Native()
}

// boardSectionHeader is a non-selectable header row, as the Jira sidebar's
// account headers are built (plain, bold, dim).
func boardSectionHeader(title string) *gtk.ListBoxRow {
	row, _ := boardSectionHeaderParts(title)
	return row
}

// boardSectionHeaderParts is boardSectionHeader and its label.
func boardSectionHeaderParts(title string) (*gtk.ListBoxRow, *gtk.Label) {
	l := gtk.NewLabel(title)
	l.SetUseMarkup(false)
	l.SetXAlign(0)
	l.SetMarginStart(10)
	l.SetMarginEnd(10)
	l.SetMarginTop(10)
	l.SetMarginBottom(4)
	l.AddCSSClass("caption-heading")
	l.AddCSSClass("dim-label")
	row := gtk.NewListBoxRow()
	row.SetChild(l)
	row.SetSelectable(false)
	row.SetActivatable(false)
	return row, l
}

// boardRowData projects a board.Row onto widget.BoardRowData.
func boardRowData(r board.Row) widget.BoardRowData {
	return widget.BoardRowData{
		StateClass:  boardStateDotClass(r.State),
		Person:      r.Person,
		Title:       boardTitleText(r.Title, r.TitleIsAssistant),
		Snippet:     boardSnippetText(r.Snippet, r.SnippetIsAssistant),
		Date:        r.Time,
		Due:         r.Due,
		DueOverdue:  r.DueOverdue,
		IssueKey:    r.IssueKey,
		IssueStatus: r.IssueStatus,
		IssueStyle:  r.IssueStyle,
		Attachments: r.Attachments,
		Unread:      r.Unread,
		CountText:   r.CountText,
		Badges:      r.Badges,
	}
}

// boardTitleText and boardSnippetText mark text the assistant wrote with
// its glyph in front, as plain text (never markup, CLAUDE.md rule 3): the
// board's own board.AssistantMark, the same one the detail uses.
func boardTitleText(s string, assistant bool) string {
	if assistant && s != "" {
		return board.AssistantMark + " " + s
	}
	return s
}

func boardSnippetText(s string, assistant bool) string {
	if assistant && s != "" {
		return board.AssistantMark + " " + s
	}
	return s
}

// caseIDAt is the case id of the list's row at index, "" for a header.
func (p *boardPage) caseIDAt(index int) (board.CaseID, bool) {
	if index < 0 || index >= len(p.caseOrder) {
		return "", false
	}
	id := p.caseOrder[index]
	return id, id != ""
}

// syncListSelection highlights the row of the controller's selection
// without re-entering the row-selected handler.
func (p *boardPage) syncListSelection(vm board.ViewModel) {
	p.reselecting = true
	defer func() { p.reselecting = false }()
	if vm.Selection == "" {
		p.caseList.UnselectAll()
		return
	}
	for i, id := range p.caseOrder {
		if id == vm.Selection {
			if row := p.caseList.RowAtIndex(i); row != nil {
				p.caseList.SelectRow(row)
			}
			return
		}
	}
	p.caseList.UnselectAll()
}

// boardStateDotClass is a state's CSS class (internal/style).
func boardStateDotClass(s board.State) string {
	switch s {
	case board.StateHot:
		return "board-state-dot-hot"
	case board.StateYou:
		return "board-state-dot-you"
	case board.StateThem:
		return "board-state-dot-them"
	}
	return "board-state-dot-info"
}
