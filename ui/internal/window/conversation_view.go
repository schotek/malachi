// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"math"
	"reflect"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/bulkmail"
	"github.com/schotek/malachi/ui/internal/capabilities"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/htmlview"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/settings"
)

// The whole conversation in the reading pane (ui/internal/conversation,
// conversation_controller.go): selecting a folded conversation row of the
// grouped list stacks every member the folder holds as Jira shows an
// issue: what opened the conversation first, folded to its header while
// more follows, then the rest newest first (convDisplayOrder; the model and
// the macOS client keep the oldest first), the pane opened at its top; a
// member row and a single-message row keep the single-message view. The
// user's replies in Sent that the folder lacks are cards among them by
// date, folded. Every card folds and opens with its arrow
// (conversation.Folds), and one button above the conversation folds or
// opens them all (Collapse All / Expand All, conversation.FoldAllOffer).
// Under an open card's body a "•••" shows the quoted history the daemon
// cut from it (quoted.go). A Jira conversation has its issue card once on
// top; its description and
// comments are cards (conversation_card.go), its status and assignee
// changes compact rows, and older members left out by thread.get's cap are
// one row at the bottom (conversation_rows.go). Only the newest member that
// is not an event is marked read. A port of the macOS client's
// ConversationViewController.swift and ReadingPaneViewController.swift.
//
// The page (conversation_view.blp) is slightly grey and the messages are
// cards on it, in one column no wider than 900, a gutter at its leading
// edge carrying the timeline (convRails). A stack of native cards in one
// scrolled window, never one composed document: each card's body is its
// own locked view with one sanitiser output in it (htmlview.Card,
// docs/security.md §3.2), so a message's CSS cannot restyle or forge
// another message's headers, which are plain text. The cards are cheap: a
// body is fetched only near the viewport and a web view exists only for
// the nearest few HTML cards (liveCards); the others keep the height they
// last had.
//
// While heights settle (bodies arriving, web views measuring their
// documents) the item the user reads stays in place: until the user
// scrolls that is the top of the conversation (pinned: the issue card, or
// the newest item), afterwards the first item reaching below the
// viewport's top (last). The vertical adjustment's
// "changed" comes after the column's allocation, so the anchor is put
// back within the same frame. The link under the pointer of any card
// shows in one status label at the bottom of the pane. The list keeps the
// keyboard: Space and Shift+Space page through the conversation.

// convPageName is the page of message_stack the conversation view is.
const convPageName = "conversation"

// convStatusMaxRunes caps the link shown in the status label.
const convStatusMaxRunes = 512

// convFriction slows the pane down after a flick over a card whose
// document fits, per second (GTK's kinetic scrolling uses 4).
const convFriction = 4.0

// convAnchor keeps an item where it is on screen: offset is how far the
// viewport's top was below the item's top.
type convAnchor struct {
	widget gtk.Widgetter
	offset float64
}

// conversationView is the conversation page of the main window's reading
// pane.
type conversationView struct {
	w    *Window
	ctrl *conversationController

	page     *gtk.Overlay
	scroller *gtk.ScrolledWindow
	clamp    *adw.Clamp
	column   *gtk.Box
	issueBox *gtk.Box
	status   *gtk.Label
	spinner  *adw.Spinner
	// foldAllButton is Collapse All / Expand All above the conversation,
	// hidden while fewer than two cards fold; foldAll what it offers now.
	foldAllButton *gtk.Button
	foldAll       conversation.FoldAll

	// The issue card on show and what it was built from.
	issueCard  *issueCard
	issueShown *convIssueKey

	cards  map[api.MessageID]*convCard
	events map[api.MessageID]*convEventRow
	rows   map[api.MessageID]*convRow
	// truncated is the row of the older members, made once.
	truncated    *convTruncatedRow
	truncatedRow *convRow
	// items are the rows of the model's items, in order.
	items []*convRow
	// compact: the pane is narrow, the items show the short date.
	compact bool
	width   int

	pinned, last  *convAnchor
	settingValue  bool
	liveScheduled bool
	// webPool are the card views no card holds now, kept for the next
	// card that goes live (takeWebView, returnWebView).
	webPool []*htmlview.Card

	// folds are the user's folds and unfolds of the cards of the
	// conversation on show (setCardFolded, Collapse All / Expand All),
	// which win over the default (conversation.DefaultFolds) until another
	// conversation is shown; foldState the cards' fold state as last
	// applied (conversation.Folds.State).
	folds     conversation.Folds
	foldState map[api.MessageID]bool

	// layoutTick is the frame callback that runs updateLive again once the
	// rows are laid out (afterLayout), 0 when none is pending; layoutTries
	// counts the frames it waited without every card laid out.
	layoutTick   uint
	layoutTries  int
	spinnerTimer glib.SourceHandle
	kineticID    uint
}

// convIssueKey is what the issue card on show was built from.
type convIssueKey struct {
	card    jira.Card
	account api.AccountID
	issue   api.IssueInfo
}

// conversationPane is the conversation view, made the first time a
// conversation is shown: the page joins message_stack, the text zoom and
// Space in the list reach it.
func (w *Window) conversationPane() *conversationView {
	if w.conv != nil {
		return w.conv
	}
	installConvCSS()
	b := data.Builder("conversation_view.ui")
	cv := &conversationView{
		w:             w,
		page:          b.GetObject("conversation_page").Cast().(*gtk.Overlay),
		scroller:      b.GetObject("conversation_scroller").Cast().(*gtk.ScrolledWindow),
		clamp:         b.GetObject("conversation_clamp").Cast().(*adw.Clamp),
		column:        b.GetObject("conversation_column").Cast().(*gtk.Box),
		issueBox:      b.GetObject("conversation_issue").Cast().(*gtk.Box),
		status:        b.GetObject("conversation_status").Cast().(*gtk.Label),
		spinner:       b.GetObject("conversation_spinner").Cast().(*adw.Spinner),
		foldAllButton: b.GetObject("conversation_fold_all").Cast().(*gtk.Button),
		cards:         map[api.MessageID]*convCard{},
		events:        map[api.MessageID]*convEventRow{},
		rows:          map[api.MessageID]*convRow{},
	}
	cv.status.SetUseMarkup(false)
	cv.foldAllButton.ConnectClicked(cv.foldAllClicked)
	cv.ctrl = newConversationController(convWindowHost{w}, i18n.Tr)
	cv.ctrl.onChange = cv.modelChanged
	cv.ctrl.onLoaded = func(id api.MessageID, lm *loadedMessage) {
		if c := cv.cards[id]; c != nil {
			c.render(lm)
		}
	}

	// Shown again (another page of the stack was on top): the cards near
	// the viewport are looked at anew.
	cv.page.ConnectMap(cv.scheduleLiveUpdate)
	adj := cv.scroller.VAdjustment()
	adj.ConnectChanged(func() {
		// After the column's allocation: what the user reads goes back
		// where it was, in the same frame.
		cv.restore(cv.anchor())
		cv.scheduleLiveUpdate()
	})
	adj.ConnectValueChanged(func() {
		if !cv.settingValue {
			// The user scrolled (or GTK kept the view in the document):
			// from now on the item under the viewport's top stays in place,
			// no longer the top, unless the view is still there.
			if !cv.atStart() {
				cv.pinned = nil
			}
			cv.last = cv.currentAnchor()
		}
		cv.scheduleLiveUpdate()
	})
	// A new scroll by the user stops a flick of ours.
	sc := gtk.NewEventControllerScroll(gtk.EventControllerScrollVertical)
	sc.SetPropagationPhase(gtk.PhaseCapture)
	sc.ConnectScrollBegin(cv.stopKinetic)
	sc.ConnectScroll(func(float64, float64) bool {
		cv.stopKinetic()
		return false
	})
	cv.scroller.AddController(sc)

	w.messageStack.AddNamed(cv.page, convPageName)
	w.settings.OnChanged(settings.KeyTextZoom, func() { cv.setZoom(w.settings.TextZoom()) })
	w.addConversationPaging()
	w.conv = cv
	return cv
}

// showConversation shows the list's selected row r in the conversation
// view when it is a folded conversation row (rowShowsConversation), and
// reports whether it did; any other row clears the conversation and is the
// single-message view's (false).
func (w *Window) showConversation(r listRow) bool {
	if !rowShowsConversation(r) || w.model.search.active {
		w.hideConversation()
		return false
	}
	cv := w.conversationPane()
	if cv.ctrl.thread != r.Key.Thread {
		// The single-message view drops what it showed; a late answer for
		// it is not rendered (bodyGen), and nothing of it is marked read.
		w.model.bumpBody()
		w.scheduleMarkRead("")
		w.pane.leaveForConversation()
		w.paneQuoted.Clear()
	}
	w.outboxBanner.SetRevealed(false)
	w.draftBanner.SetRevealed(false)
	w.messageStack.SetVisibleChildName(convPageName)
	cv.ctrl.show(r)
	return true
}

// hideConversation takes the conversation out of the reading pane (another
// row, or none, is selected); its web views go.
func (w *Window) hideConversation() {
	if w.conv != nil {
		w.conv.ctrl.clear()
	}
}

// conversationShown reports whether the reading pane shows a conversation.
func (w *Window) conversationShown() bool {
	return w.conv != nil && w.conv.ctrl.thread != ""
}

// conversationListChanged is called after every change of the grouped
// list (syncRows): the shown conversation follows its members as they
// arrive, change or go.
func (w *Window) conversationListChanged() {
	if w.conv != nil {
		w.conv.ctrl.membersChanged()
	}
}

// conversationMessagesChanged: the daemon rebuilt messages of account acc
// in place (notify.messagesChanged). When the conversation shown is the
// account's, its held bodies go and the cards near the viewport ask for
// theirs again: call it after the window's message cache let go of the
// account's entries, and have the list forget the folder's members and
// list it again, so that the members come back through thread.get and
// merge.
func (w *Window) conversationMessagesChanged(acc api.AccountID) {
	if w.conv == nil {
		return
	}
	if c := w.conv.ctrl; c.thread != "" && c.summary.AccountID == acc {
		c.refresh(c.thread)
	}
}

// The window's fan-out of message news (remote.go, download.go) to the
// card of the message, when one shows it.

// conversationShowLoaded re-renders the card of message id.
func (w *Window) conversationShowLoaded(id api.MessageID, lm *loadedMessage) {
	if w.conv == nil {
		return
	}
	if c := w.conv.cards[id]; c != nil {
		w.conv.ctrl.adopt(id, lm)
		c.render(lm)
	}
}

// conversationRefreshBars redraws the bars of the card of message id.
func (w *Window) conversationRefreshBars(id api.MessageID, lm *loadedMessage) {
	if w.conv == nil {
		return
	}
	if c := w.conv.cards[id]; c != nil {
		c.renderBars(lm)
	}
}

// conversationRefreshChips redraws the chips of the card of message id.
func (w *Window) conversationRefreshChips(id api.MessageID, lm *loadedMessage) {
	if w.conv == nil {
		return
	}
	if c := w.conv.cards[id]; c != nil {
		if lm == nil {
			lm = w.conv.ctrl.loaded[id]
		}
		c.refreshChips(lm)
	}
}

// leaveForConversation empties the single-message view when the pane
// shows a conversation instead: its body and pictures, its bars and
// chips; nothing of it is redrawn for the message it showed.
func (v *messageView) leaveForConversation() {
	v.cancelSpinner()
	v.links = nil
	v.hint.SetVisible(false)
	v.setBarVisible(false)
	v.showPicturesBar(picturesBarState{})
	v.showBulk(bulkmail.Strip{}, false)
	v.showText("")
	v.renderAttachments(api.MessageSummary{}, nil)
	v.showQuotedButton(conversation.QuotedNone)
	v.shown, v.shownLoaded = api.MessageSummary{}, nil
}

// addConversationPaging lets Space and Shift+Space in the message list
// page through the conversation shown (the list keeps the keyboard);
// otherwise the keys do what they did.
func (w *Window) addConversationPaging() {
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		if keyval != gdk.KEY_space && keyval != gdk.KEY_KP_Space {
			return false
		}
		if state&(gdk.ControlMask|gdk.AltMask|gdk.SuperMask) != 0 || !w.conversationShown() {
			return false
		}
		return w.conv.pageBy(state&gdk.ShiftMask != 0)
	})
	w.messageList.AddController(keys)
}

// convWindowHost is the window as the controller's convHost.
type convWindowHost struct{ w *Window }

func (h convWindowHost) convMembers(tid api.ThreadID) *threadMembers {
	return h.w.model.members[tid]
}

func (h convWindowHost) convSummary(tid api.ThreadID) (api.ThreadSummary, bool) {
	i, ok := h.w.model.tindex[tid]
	if !ok || i >= len(h.w.model.threads) {
		return api.ThreadSummary{}, false
	}
	return h.w.model.threads[i], true
}

func (h convWindowHost) convEnsureMembers(tid api.ThreadID, then func()) {
	h.w.ensureMembers(tid, then)
}

func (h convWindowHost) convAccount(id api.AccountID) (api.Account, bool) {
	return h.w.model.account(id)
}

func (h convWindowHost) convComposeAccount() bool {
	return len(capabilities.ForwardAccounts(h.w.model.accounts)) > 0
}

func (h convWindowHost) convFetch(s api.MessageSummary, full, quoted bool, then func(*loadedMessage)) {
	lm := h.w.loadedFor(s.ID)
	if lm.account == "" {
		lm.account = s.AccountID
	}
	h.w.switchQuoted(s.ID, lm, quoted)
	if full {
		h.w.fetchMessage(s.AccountID, s.ID, then)
		return
	}
	h.w.fetchBodyOnly(s.AccountID, s.ID, then)
}

// fetchBodyOnly is fetchMessage without message.get: the body half alone,
// for a card that needs nothing the summary does not have. then runs on
// the main loop after every answer for the entry, and at once when the
// body is there.
func (w *Window) fetchBodyOnly(acc api.AccountID, id api.MessageID, then func(*loadedMessage)) {
	lm := w.loadedFor(id)
	if lm.body != nil {
		then(lm)
		return
	}
	lm.waiters = append(lm.waiters, then)
	if lm.fetching {
		return
	}
	w.startBody(acc, id, lm)
}

// modelChanged follows the controller.
func (cv *conversationView) modelChanged(ch convChange) {
	switch ch {
	case convLoading:
		cv.removeAll()
		cv.startSpinner()
	case convOpened:
		cv.stopSpinner()
		cv.open()
		// The newest member that is not an event, after the usual delay;
		// once per selection.
		if id := cv.ctrl.takeMarkRead(); id != "" {
			cv.w.scheduleMarkRead(id)
		}
	case convUpdated:
		cv.stopSpinner()
		cv.update()
	case convCleared:
		cv.stopSpinner()
		cv.removeAll()
		// The pane shows something else: only a few views wait for the
		// next conversation, the others' processes end.
		cv.trimWebPool(convIdleWebViews)
	}
}

// convIdleWebViews is how many card views the pool keeps while no
// conversation is shown.
const convIdleWebViews = 2

// takeWebView is a card view for a card going live: one from the pool,
// its zoom brought up to date, or a new one. Every view has a web process
// of its own; reusing them keeps their number at the live window's
// (convMaxLiveWebViews) however the cards come and go, where a new view
// per card started a process each time and the sandbox ran out of
// namespaces for them.
func (cv *conversationView) takeWebView(zoom int) *htmlview.Card {
	if n := len(cv.webPool); n > 0 {
		web := cv.webPool[n-1]
		cv.webPool = cv.webPool[:n-1]
		web.SetZoom(zoom)
		return web
	}
	w := cv.w
	return htmlview.NewCard(w.log, w.fetchPart, zoom)
}

// returnWebView takes a card view back (its widget already out of the
// card): reset for the next card, or released (its process ended) when
// the pool is full.
func (cv *conversationView) returnWebView(web *htmlview.Card) {
	if len(cv.webPool) >= convMaxLiveWebViews {
		web.Release()
		return
	}
	web.Reset()
	cv.webPool = append(cv.webPool, web)
}

// trimWebPool releases the pooled views beyond keep.
func (cv *conversationView) trimWebPool(keep int) {
	for len(cv.webPool) > keep {
		n := len(cv.webPool)
		cv.webPool[n-1].Release()
		cv.webPool = cv.webPool[:n-1]
	}
}

// open lays a conversation built anew out: what opened it first (folded
// while more follows), then the rest newest first (convDisplayOrder), the
// top of the conversation pinned at the viewport's top, and the cards near
// it asked for their bodies. Nothing below can push the newest message out
// of view while the heights settle.
func (cv *conversationView) open() {
	cv.removeAll()
	m := cv.ctrl.model
	if m == nil {
		return
	}
	cv.compact = convCompactDates(float64(cv.scroller.Width()))
	cv.apply(m)
	cv.pinned = cv.topAnchor()
	cv.restore(cv.pinned)
	// The first layout puts the pinned item in place ("changed").
	cv.scheduleLiveUpdate()
}

// update: the shown conversation changed. The cards are reconciled by id
// and what the user reads stays in place; while the pane shows its top, it
// stays there, so an arrival (under the opening card, newest first) is in
// view.
func (cv *conversationView) update() {
	m := cv.ctrl.model
	if m == nil {
		return
	}
	atStart := cv.atStart()
	cv.apply(m)
	if atStart {
		cv.pinned = cv.topAnchor()
		cv.restore(cv.pinned)
	}
	cv.scheduleLiveUpdate()
}

// setCardFolded folds or opens a card at the user's request (its arrow, a
// click on its preview); the choice holds while the conversation is shown.
// An opened card asks for its body. Until the user scrolls the top of the
// conversation stays in view; otherwise the card keeps its header where it
// was (in view). The Collapse All / Expand All button follows.
func (cv *conversationView) setCardFolded(c *convCard, folded bool) {
	cv.folds.Set(c.id, folded)
	if cv.foldState == nil {
		cv.foldState = map[api.MessageID]bool{}
	}
	cv.foldState[c.id] = folded
	if cv.pinned == nil {
		if row := cv.rows[c.id]; row != nil {
			if top, ok := cv.topOf(row); ok {
				cv.last = convHeaderAnchor(&convAnchor{widget: row, offset: cv.scroller.VAdjustment().Value() - top})
			}
		}
	}
	c.setFolded(folded)
	cv.showFoldAll()
	cv.scheduleLiveUpdate()
}

// foldAllClicked is Collapse All or Expand All: every card of the
// conversation shown folds or opens (conversation.Folds.SetAll); a card
// that arrives later starts as its default. Until the user scrolls the top
// of the conversation stays in view; otherwise the item under the
// viewport's top keeps its place, its header in view.
func (cv *conversationView) foldAllClicked() {
	m := cv.ctrl.model
	if m == nil || cv.foldAll == conversation.FoldAllNone {
		return
	}
	display := convDisplayOrder(m.Items)
	cv.folds.SetAll(display.items, cv.foldAll.Folded())
	cv.foldState = cv.folds.State(display.items, display.opening)
	if cv.pinned == nil {
		cv.last = convHeaderAnchor(cv.currentAnchor())
	}
	for id, c := range cv.cards {
		if c.foldable {
			c.setFolded(cv.foldState[id])
		}
	}
	cv.showFoldAll()
	cv.scheduleLiveUpdate()
}

// showFoldAll shows the Collapse All / Expand All button for the cards'
// fold state (conversation.FoldAllOffer); hidden while fewer than two
// fold.
func (cv *conversationView) showFoldAll() {
	cv.foldAll = conversation.FoldAllOffer(cv.foldState)
	if cv.foldAll == conversation.FoldAllNone {
		if cv.foldAllButton.HasFocus() {
			cv.scroller.GrabFocus()
		}
		cv.foldAllButton.SetVisible(false)
		return
	}
	cv.foldAllButton.SetLabel(cv.foldAll.Label(i18n.Tr))
	cv.foldAllButton.SetVisible(true)
}

// convHeaderAnchor is a with the item's top kept in view: a viewport
// inside the item (its top above the viewport's) moves to the item's top,
// so a card that folds keeps its header where the user can see it; nil for
// nil.
func convHeaderAnchor(a *convAnchor) *convAnchor {
	if a == nil {
		return nil
	}
	return &convAnchor{widget: a.widget, offset: min(a.offset, 0)}
}

// topAnchor is the top of the conversation: the Collapse All / Expand All
// button when it shows, else the issue card of a Jira conversation, else
// the first item (the card that opened it, or the newest); nil for none.
func (cv *conversationView) topAnchor() *convAnchor {
	if cv.foldAllButton.IsVisible() {
		return &convAnchor{widget: cv.foldAllButton}
	}
	if cv.issueBox.IsVisible() {
		return &convAnchor{widget: cv.issueBox}
	}
	if len(cv.items) > 0 {
		return &convAnchor{widget: cv.items[0]}
	}
	return nil
}

// apply puts the model's items into the column in the order shown
// (convDisplayOrder: what opened the conversation, then the rest newest
// first), reusing the widgets of the members shown already, each in a row
// with its piece of the timeline; every card folds as the user or the
// default says (conversation.Folds.State), and the Collapse All / Expand
// All button follows.
func (cv *conversationView) apply(m *conversation.Model) {
	cv.showIssue(m)
	display := convDisplayOrder(m.Items)
	items := display.items
	cv.folds.Show(m.Thread)
	cv.foldState = cv.folds.State(items, display.opening)
	rails := convRails(items)
	desired := make([]*convRow, 0, len(items))
	cardIDs := map[api.MessageID]bool{}
	eventIDs := map[api.MessageID]bool{}
	for i, it := range items {
		var row *convRow
		switch it.Kind {
		case conversation.ItemTruncated:
			if cv.truncated == nil {
				cv.truncated = newConvTruncatedRow()
				cv.truncatedRow = newConvRow(cv.truncated, markDot)
			}
			cv.truncated.SetText(it.Text)
			row = cv.truncatedRow
		case conversation.ItemMessage:
			id := it.Message.ID
			cardIDs[id] = true
			if c, ok := cv.cards[id]; ok && cv.rows[id] != nil {
				c.update(it)
				row = cv.rows[id]
			} else {
				c := newConvCard(cv, it, cv.compact)
				cv.cards[id] = c
				delete(cv.events, id)
				row = newConvRow(c.widget(), markAvatar)
				row.card = c
				cv.replaceRow(id, row)
				c.render(cv.ctrl.loaded[id])
			}
		case conversation.ItemEvent:
			id := it.Message.ID
			eventIDs[id] = true
			if e, ok := cv.events[id]; ok && cv.rows[id] != nil {
				e.update(it, cv.compact)
				row = cv.rows[id]
			} else {
				e := newConvEventRow(it, cv.compact)
				cv.events[id] = e
				if c := cv.cards[id]; c != nil {
					c.setLive(false)
					delete(cv.cards, id)
				}
				row = newConvRow(e, markDot)
				cv.replaceRow(id, row)
			}
		default:
			continue
		}
		row.show(rails[i], it.Sender)
		if row.card != nil {
			row.card.setFold(conversation.Foldable(it), cv.foldState[it.Message.ID])
		}
		desired = append(desired, row)
	}
	for id, c := range cv.cards {
		if !cardIDs[id] {
			c.setLive(false)
			delete(cv.cards, id)
		}
	}
	for id := range cv.events {
		if !eventIDs[id] {
			delete(cv.events, id)
		}
	}
	for id, row := range cv.rows {
		if !cardIDs[id] && !eventIDs[id] {
			cv.detach(row)
			delete(cv.rows, id)
		}
	}
	keepTruncated := false
	for _, r := range desired {
		keepTruncated = keepTruncated || r == cv.truncatedRow
	}
	if !keepTruncated && cv.truncatedRow != nil {
		cv.detach(cv.truncatedRow)
	}
	// The column after the issue box, in the model's order.
	var prev gtk.Widgetter = cv.issueBox
	for _, r := range desired {
		if r.Parent() == nil {
			cv.column.InsertChildAfter(r, prev)
		} else if p := r.PrevSibling(); p == nil || !gtk.BaseWidget(p).Eq(prev) {
			cv.column.ReorderChildAfter(r, prev)
		}
		prev = r
	}
	cv.items = desired
	cv.showFoldAll()
}

// replaceRow makes row the row of member id; the one it had before (the
// member was a card and is an event now, or the other way round) goes.
func (cv *conversationView) replaceRow(id api.MessageID, row *convRow) {
	if old := cv.rows[id]; old != nil {
		cv.detach(old)
	}
	cv.rows[id] = row
}

// detach takes a row out of the column.
func (cv *conversationView) detach(r *convRow) {
	if r != nil && r.Parent() != nil {
		cv.column.Remove(r)
	}
}

// removeAll takes everything out (another conversation, or none).
func (cv *conversationView) removeAll() {
	cv.stopKinetic()
	cv.folds = conversation.Folds{}
	cv.foldState = nil
	cv.foldAll = conversation.FoldAllNone
	cv.foldAllButton.SetVisible(false)
	// A conversation laid out anew waits for its own layout.
	cv.layoutTries = 0
	for _, c := range cv.cards {
		c.setLive(false)
	}
	for _, r := range cv.rows {
		cv.detach(r)
	}
	cv.detach(cv.truncatedRow)
	cv.cards = map[api.MessageID]*convCard{}
	cv.events = map[api.MessageID]*convEventRow{}
	cv.rows = map[api.MessageID]*convRow{}
	cv.items = nil
	cv.pinned, cv.last = nil, nil
	cv.clearIssue()
	cv.hover("")
}

// showIssue shows the issue card of a Jira conversation once on top
// (built by the window, issue_card.go); a mail conversation has none.
func (cv *conversationView) showIssue(m *conversation.Model) {
	info := cv.ctrl.issue
	first, ok := convFirstMember(m)
	if m.Issue == nil || info == nil || !ok {
		cv.clearIssue()
		return
	}
	key := &convIssueKey{card: *m.Issue, account: first.AccountID, issue: *info}
	if cv.issueShown != nil && reflect.DeepEqual(*cv.issueShown, *key) {
		return
	}
	account, _ := cv.w.model.account(first.AccountID)
	cv.clearIssue()
	card := cv.w.conversationIssueCard(*m.Issue, account, *info)
	cv.issueCard = card
	cv.issueShown = key
	cv.issueBox.Append(card)
	cv.issueBox.SetVisible(true)
}

// clearIssue takes the issue card out.
func (cv *conversationView) clearIssue() {
	if cv.issueCard != nil {
		cv.issueBox.Remove(cv.issueCard)
		cv.issueCard = nil
	}
	cv.issueShown = nil
	cv.issueBox.SetVisible(false)
}

// convFirstMember is the first member of m, which names the conversation's
// account.
func convFirstMember(m *conversation.Model) (api.MessageSummary, bool) {
	for _, it := range m.Items {
		if it.Kind != conversation.ItemTruncated {
			return it.Message, true
		}
	}
	return api.MessageSummary{}, false
}

// Scroll position.

// anchor is what stays in place while heights change: the pinned item
// until the user scrolls, then the one under the viewport's top.
func (cv *conversationView) anchor() *convAnchor {
	if cv.pinned != nil {
		return cv.pinned
	}
	return cv.last
}

// topOf is the top of w in the column's document (the clamp the viewport
// scrolls).
func (cv *conversationView) topOf(w gtk.Widgetter) (float64, bool) {
	s, ok := cv.spanOf(w)
	return s.Min, ok
}

// spanOf is w's stretch of the document; false while w is not in the
// column or not laid out yet (a row added since the last frame), so that
// nothing is decided from a place it does not have.
func (cv *conversationView) spanOf(w gtk.Widgetter) (convSpan, bool) {
	base := gtk.BaseWidget(w)
	if base.Parent() == nil || !base.IsVisible() || (base.Width() == 0 && base.Height() == 0) {
		return convSpan{}, false
	}
	b, ok := base.ComputeBounds(cv.clamp)
	if !ok {
		return convSpan{}, false
	}
	return convSpanOf(float64(b.Y()), float64(b.Y()+b.Height())), true
}

// currentAnchor is the first item reaching below the viewport's top, and
// how far below its top the viewport's top is.
func (cv *conversationView) currentAnchor() *convAnchor {
	top := cv.scroller.VAdjustment().Value()
	candidates := make([]gtk.Widgetter, 0, len(cv.items)+2)
	if cv.foldAllButton.IsVisible() {
		candidates = append(candidates, cv.foldAllButton)
	}
	if cv.issueBox.IsVisible() {
		candidates = append(candidates, cv.issueBox)
	}
	for _, r := range cv.items {
		candidates = append(candidates, r)
	}
	for _, w := range candidates {
		s, ok := cv.spanOf(w)
		if ok && s.Max > top+0.5 {
			return &convAnchor{widget: w, offset: top - s.Min}
		}
	}
	return nil
}

// restore brings the anchored item back to where it was on screen.
func (cv *conversationView) restore(a *convAnchor) {
	if a == nil {
		return
	}
	itemTop, ok := cv.topOf(a.widget)
	if !ok {
		return
	}
	adj := cv.scroller.VAdjustment()
	top := anchoredTop(itemTop, a.offset, adj.Upper(), adj.PageSize())
	if math.Abs(top-adj.Value()) < 0.5 {
		return
	}
	cv.settingValue = true
	adj.SetValue(top)
	cv.settingValue = false
}

// atStart reports a viewport at the top of the conversation, where the
// newest item is.
func (cv *conversationView) atStart() bool {
	return cv.scroller.VAdjustment().Value() <= 1
}

// pageBy scrolls one page down (or up) for Space (Shift+Space) in the list.
// True: the key was used.
func (cv *conversationView) pageBy(up bool) bool {
	if cv.ctrl.model == nil {
		return false
	}
	cv.stopKinetic()
	adj := cv.scroller.VAdjustment()
	overlap := adj.PageSize() - adj.PageIncrement()
	cv.pinned = nil
	adj.SetValue(pageTop(adj.Value(), up, adj.PageSize(), adj.Upper(), overlap))
	return true
}

// wheelStep is how far one step of the wheel scrolls the pane (GTK's own
// step for a scrolled window).
func (cv *conversationView) wheelStep() float64 {
	return math.Pow(cv.scroller.VAdjustment().PageSize(), 2.0/3.0)
}

// scrollBy scrolls the pane by what a card whose document fits handed
// on: dy steps of the wheel, or dy pixels of a touchpad.
func (cv *conversationView) scrollBy(dy float64, unit gdk.ScrollUnit) {
	cv.stopKinetic()
	if unit == gdk.ScrollUnitWheel {
		dy *= cv.wheelStep()
	}
	adj := cv.scroller.VAdjustment()
	adj.SetValue(adj.Value() + dy)
}

// kinetic lets the pane glide on after a flick of v pixels a second
// handed on by a card, slowing down as GTK's own scrolling does.
func (cv *conversationView) kinetic(v float64) {
	cv.stopKinetic()
	if math.Abs(v) < 1 {
		return
	}
	var last int64
	cv.kineticID = cv.scroller.AddTickCallback(func(_ gtk.Widgetter, clock gdk.FrameClocker) bool {
		now := gdk.BaseFrameClock(clock).FrameTime()
		if last == 0 {
			last = now
			return true
		}
		dt := float64(now-last) / 1e6
		last = now
		adj := cv.scroller.VAdjustment()
		before := adj.Value()
		adj.SetValue(before + v*dt)
		v *= math.Exp(-convFriction * dt)
		if math.Abs(v) < 10 || adj.Value() == before {
			cv.kineticID = 0
			return false
		}
		return true
	})
}

// stopKinetic ends a glide.
func (cv *conversationView) stopKinetic() {
	if cv.kineticID != 0 {
		cv.scroller.RemoveTickCallback(cv.kineticID)
		cv.kineticID = 0
	}
}

// Live cards.

// scheduleLiveUpdate runs updateLive once the main loop is idle.
func (cv *conversationView) scheduleLiveUpdate() {
	if cv.liveScheduled {
		return
	}
	cv.liveScheduled = true
	glib.IdleAdd(func() {
		cv.liveScheduled = false
		cv.updateLive()
	})
}

// updateLive asks for the bodies of the cards near the viewport, gives web
// views to the nearest HTML cards and takes them from the rest
// (liveCards), lets go of far entries beyond the controller's budget, and
// follows the pane's width (the short dates, the documents' reflow).
func (cv *conversationView) updateLive() {
	if cv.ctrl.model == nil {
		return
	}
	// Should no layout have put the pinned item in place yet.
	cv.restore(cv.pinned)
	if w := cv.scroller.Width(); w != cv.width {
		cv.width = w
		if compact := convCompactDates(float64(w)); compact != cv.compact {
			cv.compact = compact
			for _, c := range cv.cards {
				c.setCompact(compact)
			}
			for _, e := range cv.events {
				e.setCompact(compact)
			}
		}
	}
	adj := cv.scroller.VAdjustment()
	visible := convSpanOf(adj.Value(), adj.Value()+adj.PageSize())
	var ordered []*convCard
	var frames []convSpan
	var html []bool
	unplaced := false
	for _, r := range cv.items {
		card := r.card
		if card == nil {
			continue
		}
		if card.folded {
			continue // no body, no view while folded (setFolded)
		}
		s, ok := cv.spanOf(card.root)
		if !ok {
			unplaced = true
			continue
		}
		ordered = append(ordered, card)
		frames = append(frames, s)
		html = append(html, card.isHTML)
	}
	live := liveCards(frames, html, visible, convLiveScreens, convMaxLiveWebViews)
	near := map[api.MessageID]bool{}
	for i, c := range ordered {
		c.setLive(live.Web[i])
		if live.Near[i] {
			near[c.id] = true
			cv.ctrl.needsBody(c.id, c.detailsOpen)
		}
	}
	cv.ctrl.trim(near, convHeldBytes)
	if cv.pinned == nil {
		cv.last = cv.currentAnchor()
	}
	if unplaced {
		cv.afterLayout()
	} else {
		cv.layoutTries = 0
	}
}

// convLayoutTries bounds the frames afterLayout waits for the rows to be
// laid out, so that a card that never gets an allocation (a hidden page)
// does not keep a callback on every frame.
const convLayoutTries = 30

// afterLayout runs updateLive again after the next frame's layout: cards
// that had no allocation yet were skipped, and nothing else may come to
// run it. The adjustment's "changed" does not fire when the column still
// fits the viewport (the upper bound stays the page size), which is the
// case of a short conversation whose bodies have not been asked for, so
// without this they would never be. A frame callback runs before that
// frame's layout, the idle it schedules after it.
func (cv *conversationView) afterLayout() {
	if cv.layoutTick != 0 || cv.layoutTries >= convLayoutTries {
		return
	}
	cv.layoutTries++
	cv.layoutTick = cv.scroller.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		cv.layoutTick = 0
		cv.scheduleLiveUpdate()
		return false
	})
}

// Other.

// hover shows the link under the pointer of any card ("" hides it): plain
// text, capped, the middle elided.
func (cv *conversationView) hover(uri string) {
	text := clampText(uri, convStatusMaxRunes)
	cv.status.SetText(text)
	cv.status.SetVisible(text != "")
}

// setZoom follows the text-zoom setting: the web views and their heights
// at once (the plain text follows internal/style).
func (cv *conversationView) setZoom(percent int) {
	for _, c := range cv.cards {
		c.setZoom(percent)
	}
}

// startSpinner shows the spinner once the members take long enough to
// notice (bodySpinnerDelay).
func (cv *conversationView) startSpinner() {
	cv.stopSpinner()
	cv.spinnerTimer = glib.TimeoutAdd(bodySpinnerDelay, func() bool {
		cv.spinnerTimer = 0
		cv.spinner.SetVisible(true)
		return false
	})
}

// stopSpinner hides the spinner and disarms its timer.
func (cv *conversationView) stopSpinner() {
	if cv.spinnerTimer != 0 {
		glib.SourceRemove(cv.spinnerTimer)
		cv.spinnerTimer = 0
	}
	cv.spinner.SetVisible(false)
}
