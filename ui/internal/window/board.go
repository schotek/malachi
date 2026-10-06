// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"os"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/boardreply"
	"github.com/schotek/malachi/ui/internal/boardtriage"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The window's second mode, Board (docs/api.md §4.13, docs/architecture.md
// §3.7; CLAUDE.md "Nástěnka"): cases over mail threads and Jira issues,
// sorted by what is owed, with the assistant's notes and triage. This file
// is the mode switch and the board page's lifecycle; board_list.go renders
// the List style's navigation column and case list, board_detail.go the
// selected case, board_actions.go the win.board-* actions and the Remind
// and context menus, board_triage_button.go the Triage control,
// board_show_in_mail.go the reveal that switches back to Mail.
//
// The macOS client leads (MalachiCore/Board, MalachiMail/Board); this is
// the GTK port of its model (ui/internal/board, ported and tested this
// session) and view logic, including the three styles and inline replies.

// boardPage is the board's own page (board_page.blp), its controller and
// data source, built once by ensureBoard.
type boardPage struct {
	w   *Window
	ctl *board.Controller
	src board.DataSource
	// daemon is src when it is the daemon's: Start and the notification
	// hooks (notify.go) act on it. nil for the in-memory sample/dummy
	// source, which needs neither.
	daemon *board.DaemonSource

	// root is board_page_root (Adw.BreakpointBin): what is inserted into
	// window.blp's board_page_bin.
	root *adw.BreakpointBin

	modeSwitch       *adw.ToggleGroup
	styleSwitch      *adw.ToggleGroup
	title            *adw.WindowTitle
	accountFilter    *gtk.DropDown
	accountFilterIDs []api.AccountID // accountFilter's model, in order; "" (All Accounts) first
	settingAccount   bool
	settingStyle     bool
	triageButton     *gtk.Button
	triageActivity   *gtk.Box
	triageProgress   *gtk.Label

	stack     *gtk.Stack
	emptyPage *adw.StatusPage
	notice    *adw.Banner

	navSplit *adw.NavigationSplitView
	navList  *gtk.ListBox

	statusButton *gtk.MenuButton
	syncSpinner  *adw.Spinner
	connIcon     *gtk.Image
	syncLabel    *gtk.Label

	listSplit     *adw.NavigationSplitView
	listStack     *gtk.Stack
	listEmptyPage *adw.StatusPage
	caseList      *gtk.ListBox
	// caseOrder mirrors the case list's rows in order, a header's slot "".
	caseOrder   []board.CaseID
	reselecting bool

	detailPage      *adw.NavigationPage
	doneButton      *gtk.Button
	remindButton    *gtk.MenuButton
	remindPopover   *gtk.Popover
	archiveButton   *gtk.Button
	detailMenu      *gtk.MenuButton
	replyButton     *gtk.Button
	detailStack     *gtk.Stack
	noSelectionPage *adw.StatusPage
	detailTop       *gtk.Box
	dueBox          *gtk.Box
	summaryBox      *gtk.Box
	replySlot       *gtk.Box
	suggest         *boardSuggestReplyControl
	conversationBox *gtk.Box
	conversation    *boardConversation
	commitmentsBox  *gtk.Box
	statePill       *gtk.MenuButton

	columns *boardColumns
	today   *boardToday
	panel   *boardPanel
	// detailTree is the ONE detail widget tree board_detail.go fills
	// (board_detail_tree, board_page.blp): the actions header
	// (Done/Remind/Archive/Menu/Reply) and board_detail_stack.
	// board_panel.go alone moves it between board_detail_page (the List
	// style) and the panel on every style switch; board_detail.go only
	// ever fills it, board_list.go only ever fetches it.
	detailTree *adw.ToolbarView
	// panelNotice is ViewModel.Notice above Columns and Today (board_notice
	// above the List is unaffected); board_list.go's renderStack reveals
	// whichever one applies.
	panelNotice *adw.Banner

	// reveal is Show in Mail's pending request (board_show_in_mail.go).
	// win.board-move-to (the state pill's and the context menu's "Move
	// To") is registered and owned by the Window (boardMoveToAction),
	// not here.
	reveal revealState

	replyEditor           *boardreply.Editor
	replyPanes            *boardreply.Panes[*compose.Pane]
	replyShown            *compose.Pane
	replyUnsaved          *gtk.Label
	replyFocusPending     board.CaseID
	replySuspended        bool
	replyClosed           bool
	replyHeightDisconnect map[*compose.Pane]func()

	triageRemoveObserve func()
	triageRemoveEnded   func()
}

// boardAPIState is api.BoardState's value on the wire: board_actions.go
// casts win.board-move-to's string parameter through it without its own
// import of api (this file already has one).
type boardAPIState = api.BoardState

// boardEnv implements board.Dates over widget's formatters, so the pure
// board package never imports GLib itself.
type boardEnv struct{}

func (boardEnv) Date(t, now time.Time) string { return widget.FormatDate(t, now) }
func (boardEnv) Time(t time.Time) string      { return widget.FormatTime(t) }
func (boardEnv) DateTime(t time.Time) string  { return widget.FormatDateTime(t) }
func (boardEnv) Weekday(t time.Time) string   { return widget.FormatWeekday(t) }

// boardSamplesEnv is MALACHI_BOARD_SAMPLES, read once by ensureBoard.
const boardSamplesEnv = "MALACHI_BOARD_SAMPLES"

// setupBoardMode wires the mode switch (the folders header bar's; the board
// page's own mirrors it once built) and builds the board eagerly when the
// application's triage wants its data running in the background
// (boardtriage.Controller.WantsBoardData); otherwise the board page and its
// source are made on the first switch to Board (ensureBoard). Call once
// from New, after the client and the Assistant's board triage exist.
func (w *Window) setupBoardMode() {
	w.registerBoardActions()
	for _, m := range board.Modes {
		if t := w.modeSwitchMail.ToggleByName(modeNick(m)); t != nil {
			t.SetTooltip(modeTitle(m))
		}
	}
	w.modeSwitchMail.SetActiveName(modeNick(board.InitialMode))
	w.modeSwitchMail.NotifyProperty("active-name", func() {
		if w.settingModeSwitch {
			return
		}
		w.setMode(parseModeNick(w.modeSwitchMail.ActiveName()))
	})
	w.watchBoardAutoStart()
}

// modeNick and parseModeNick are the mode switch's "active-name" values.
func modeNick(m board.Mode) string {
	if m == board.ModeBoard {
		return "board"
	}
	return "mail"
}

func parseModeNick(s string) board.Mode {
	if s == "board" {
		return board.ModeBoard
	}
	return board.ModeMail
}

// modeTitle is a mode's tooltip and segment title (board.Mail / BoardName).
func modeTitle(m board.Mode) string {
	if m == board.ModeBoard {
		return board.BoardName(i18n.Tr)
	}
	return board.Mail(i18n.Tr)
}

// setMode switches between Mail and Board (board.Mode): gates the mail-only
// actions and shortcuts (applyMessageAccels), moves the status strip's
// popover (status.go moveStatusStripToBoard/Mail) and tells the board
// controller it is about to show or has left
// (BoardWillShow/BoardShown; board.StyleOnShow). Requests from elsewhere
// (the status strip's Outbox, the assistant panel, a notification) decide
// their target mode with board.ModeFor before calling this.
func (w *Window) setMode(m board.Mode) {
	if m == w.mode {
		return
	}
	leaving := w.mode
	w.mode = m
	w.settingModeSwitch = true
	w.modeSwitchMail.SetActiveName(modeNick(m))
	if w.boardPage != nil {
		w.boardPage.modeSwitch.SetActiveName(modeNick(m))
	}
	w.settingModeSwitch = false
	w.applyMessageAccels()
	if m == board.ModeBoard {
		// Leaving Mail: a wait for a listing Show in Mail started no
		// longer leads anywhere the user can see why.
		if w.boardPage != nil {
			w.boardPage.reveal.cancel()
		}
		w.ensureBoard()
		w.moveStatusStripToBoard()
		w.ResumeBoardReplies()
		w.boardPage.ctl.BoardWillShow()
		w.modeStack.SetVisibleChildName("board")
		w.boardPage.applyAll()
		w.boardPage.ctl.BoardShown()
		return
	}
	if w.boardPage != nil {
		w.boardPage.suspendBoardReplies()
	}
	w.moveStatusStripToMail()
	w.modeStack.SetVisibleChildName("mail")
	if w.boardPage != nil {
		// win.board-* act on a selection only Board shows.
		w.boardPage.applyActionsSensitivity(w.boardPage.ctl.View())
	}
	if leaving == board.ModeBoard {
		w.refreshMessageActions()
	}
}

// applyMessageAccels keeps the single-key list shortcuts (MessageAccels)
// installed exactly when a key may act on the selected message: in Mail,
// and with the keyboard away from a text field (typingAllowsAccels,
// search.go's gate — a letter typed in the search entry must not archive
// the selected message). Board hides the list and the reader those
// shortcuts act on (board.Allows CommandMessageAction), so they never fire
// there, however the typing gate stands; called by setMode and by
// search.go wherever typingAllowsAccels changes.
func (w *Window) applyMessageAccels() {
	w.installMessageAccels(messageAccelsAllowed(w.mode, w.typingAllowsAccels))
}

// messageAccelsAllowed is applyMessageAccels' decision, pure and tested
// without GTK.
func messageAccelsAllowed(m board.Mode, typingAllows bool) bool {
	return board.Allows(board.CommandMessageAction, m) && typingAllows
}

// ensureBoard builds the board page and its controller the first time it is
// needed. A second call does nothing.
func (w *Window) ensureBoard() {
	if w.boardPage != nil {
		return
	}
	p := &boardPage{w: w}
	p.newDataSource()
	p.ctl = board.NewController(p.src, board.ControllerOptions{
		Env:          board.Env{Tr: i18n.Tr, Dates: boardEnv{}, Loc: time.Local},
		DefaultStyle: func() string { return string(w.settings.BoardDefaultStyle()) },
	})
	p.ctl.OnChange = p.onChange
	p.ctl.OnToast = w.Toast
	pb := data.Builder("board_page.ui")
	p.bind(pb)
	p.bindColumns(pb)
	p.bindToday(pb)
	p.bindPanel(pb)
	w.boardPageBin.SetChild(p.root)
	p.wire()
	p.wireToday()
	p.wireKeys()
	p.wirePanel()
	p.wireTriage()
	if w.assist != nil {
		if bt := w.assist.BoardTriage(); bt != nil {
			bt.SetOnRefresh(func() { p.src.Refresh() })
		}
	}
	if p.daemon != nil {
		p.daemon.Start()
	}
	w.boardPage = p
	p.initBoardReplies()
	p.initBoardSuggestReply()
	p.applyAll()
	// The board's own status button keeps up with sync.go's (whose own
	// timer and immediate refreshes this agent's files do not touch) by
	// asking for the current line every few seconds; the main window
	// stops this timer when CloseBoardReplies releases its page.
	glib.TimeoutSecondsAdd(boardStatusRefreshSeconds, func() bool {
		if p.replyClosed {
			return false
		}
		w.refreshBoardStatusLabel()
		return true // keep the timer
	})
}

// boardStatusRefreshSeconds is how often refreshBoardStatusLabel polls the
// status line on its own (board_triage.go's connection poll uses the same
// cadence): short enough to feel immediate, cheap enough to run forever.
const boardStatusRefreshSeconds = 1

// newDataSource is the board's source: the daemon's, unless
// MALACHI_BOARD_SAMPLES=1 asks for the invented sample board (the owner's
// development aid, read once here; never the daemon's past this point, as
// CLAUDE.md's "Stav a priority" describes it).
func (p *boardPage) newDataSource() {
	if os.Getenv(boardSamplesEnv) == "1" {
		p.src = board.NewDummySource(true, time.Now(), time.Local, i18n.Tr)
		return
	}
	d := board.NewDaemonSource(p.w.client, glibLoop{}, i18n.Tr, board.DaemonOptions{Log: p.w.log})
	if bt := p.w.assist.BoardTriage(); bt != nil {
		d.OnSnapshot = bt.BoardChanged
	}
	p.daemon = d
	p.src = d
}

// BoardChanged is notify.boardChanged (notify.go): the board is told to
// list again, debounced inside the source itself.
func (w *Window) boardChanged(n api.BoardChangedNotification) {
	if w.boardPage != nil && w.boardPage.daemon != nil {
		w.boardPage.daemon.BoardChanged(n)
	}
}

// boardAccountsChanged is notify.accountsChanged's effect on the board: the
// source re-lists the accounts (account names, kind badges).
func (w *Window) boardAccountsChanged() {
	if w.boardPage != nil && w.boardPage.daemon != nil {
		w.boardPage.daemon.AccountsChanged()
	}
}

// boardConnectionChanged mirrors the daemon connection's state into the
// board's source (showConnectionState) and, since it moves the status
// line's text too (statusLineFor), the board's own status button.
func (w *Window) boardConnectionChanged(connected bool) {
	if w.boardPage != nil && w.boardPage.daemon != nil {
		w.boardPage.daemon.ConnectionChanged(connected)
	}
	w.refreshBoardStatusLabel()
}

// refreshBoardStatusLabel is the board's half of refreshSyncLabel
// (sync.go): the same status line (currentStatusLine, status.go),
// replaced by a non-empty boardtriage.StripText while there is one to
// show (the triage's note takes the strip over, as it does on macOS and
// in the GTK panel). Called whenever something that moves the line
// changes: the board's own notify hooks above, wireTriage's Observe
// (board_triage_button.go) and a short timer (ensureBoard) that catches
// the rest (sync.go's own refreshSyncLabel, which this agent does not
// own and so cannot hook directly).
func (w *Window) refreshBoardStatusLabel() {
	p := w.boardPage
	if p == nil {
		return
	}
	line := w.currentStatusLine()
	text := line.Text
	if bt := w.boardTriageOrNil(); bt != nil {
		triageText := boardtriage.StripText(bt.Controller().View(), true, p.ctl.Source().Snapshot().Phase, i18n.Tr)
		text = boardStatusText(line.Text, triageText)
	}
	p.syncLabel.SetUseMarkup(false)
	p.syncLabel.SetLabel(text)
	p.syncSpinner.SetVisible(line.Spinning)
	if line.Icon != "" {
		p.connIcon.SetFromIconName(line.Icon)
	}
	p.connIcon.SetVisible(line.Icon != "")
	if !line.Active {
		p.statusButton.Popdown()
	}
	p.statusButton.SetSensitive(line.Active)
}

// boardStatusText is refreshBoardStatusLabel's text decision, pure and
// tested without GTK: the triage's note when it has one, the base sync
// line otherwise.
func boardStatusText(base, triageNote string) string {
	if triageNote != "" {
		return triageNote
	}
	return base
}

// refresh re-renders everything from the controller's current view model
// (after a change that does not warrant picking apart which Changes bits
// fired, e.g. BoardShown or the account list changing underneath).
func (p *boardPage) refresh() { p.applyAll() }

// applyAll renders every part of the page from the controller's view model:
// the header (title, style switch, account filter, triage button), the
// page's stack, the notice banner, the nav column, the case list and the
// detail. Called once after the board is built and whenever a full redraw
// is simplest (BoardWillShow/BoardShown, the account list changing).
func (p *boardPage) applyAll() {
	p.updateBoardReplies()
	vm := p.ctl.View()
	p.renderHeader(vm)
	p.renderStack(vm)
	p.renderNav(vm)
	p.renderList(vm)
	p.renderDetail(vm)
	p.columns.apply(vm)
	p.today.apply(vm)
	p.renderPanel(vm)
	p.renderTriageButton()
	p.applyActionsSensitivity(vm)
}

// onChange is the controller's OnChange: redraw only what changed.
func (p *boardPage) onChange(c board.Changes) {
	p.updateBoardReplies()
	vm := p.ctl.View()
	p.renderStack(vm)
	if c.Has(board.ChangeStyle) {
		p.renderHeader(vm)
		p.renderStack(vm)
	}
	if c.Has(board.ChangeFilters) {
		p.renderHeader(vm)
	}
	if c.Has(board.ChangeContent) {
		p.renderNav(vm)
		p.renderList(vm)
	}
	if c.Has(board.ChangeContent) || c.Has(board.ChangeSelection) {
		p.renderDetail(vm)
		p.applyActionsSensitivity(vm)
		if c.Has(board.ChangeSelection) {
			p.syncListSelection(vm)
		}
	}
	if c.Has(board.ChangeContent) || c.Has(board.ChangeSelection) || c.Has(board.ChangeStyle) {
		p.columns.apply(vm)
		p.today.apply(vm)
		p.renderPanel(vm)
	}
}
