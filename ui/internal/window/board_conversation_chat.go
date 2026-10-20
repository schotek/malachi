// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"math"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/htmlview"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
)

// The chat layout of the board's conversation block (board_conversation.go;
// the reference is ui/internal/board/conversation_chat.go): the toggle in
// the block's heading row, left of Show in Mail, and a stack of the cards'
// column and the chat's. The toggle shares Mail's key
// conversation-chat-layout: it writes the setting, and the layout follows
// the setting (applyLayout), so a switch in Mail switches the board too.
// The bubbles are Mail's chat rows (conversation_chat_row.go) showing the
// text the case carries (MessageCard.Text): no message.body, no web view,
// no placeholder; the samples read the same. The detail is never scrolled
// to the chat's end: it reads down from the oldest bubble to the newest.
// While the chat shows, the block is not live (boardConversation.live), so
// every card's web view is let go and no body is fetched; the cards' state
// (board.ConversationCards) stays, and switching back shows them again.
//
// With the preference conversation-chat-html (Mail's Render HTML in Chat,
// htmlOn, observed), the bubbles near the visible part of the detail ask
// message.body through Mail's cache as the open cards do (fetchBodyOnly,
// the trimQuoted variant) and show its HTML as Mail's chat does
// (bubbleHTML, conversation.BubbleRendersHTML) in the bubble's HTML slot
// (conversation_chat_row.go: transparent view, htmlview.ChatDocument,
// frozen height once released), at most board.ConversationMaxLiveWebViews
// of them, measured from the end (chatLiveRows); the case's text stays
// the fallback. The views are the board cards' kind (their own, released
// when let go), the links go through the window's question, a height
// change above the viewport keeps the reader's place as the cards do
// (board.ConversationCompensatedTop). Far, over the cap, the chat hidden,
// the preference off or the block closed, a bubble lets its view go;
// with the preference off nothing is fetched.

const (
	boardChatCards = "cards"
	boardChatPage  = "chat"
)

// boardChat is the chat layout of a boardConversation.
type boardChat struct {
	b             *boardConversation
	toggle        *gtk.ToggleButton
	toggleContent *adw.ButtonContent
	stack         *gtk.Stack
	column        *gtk.Box

	// shown: the chat is the layout shown (the setting is on); started:
	// applyLayout ran once.
	shown, started bool
	unbind, remove func()

	// account and msgs are the last detail's (apply); rows the bubbles
	// shown, in order, cards what each shows, byID the rows of members
	// with an id.
	account api.AccountID
	msgs    []board.MessageCard
	visible bool
	rows    []*chatRow
	cards   map[*chatRow]board.MessageCard
	byID    map[api.MessageID]*chatRow

	// columnWidth is the width the bubbles were sized for (followSize),
	// compact whether they show the short date.
	columnWidth float64
	compact     bool
	sizeQueued  bool
	closed      bool

	// The HTML bubbles: htmlOn the preference as last read, entries the
	// Mail cache entries that answered, asked the members asked for,
	// heightPending the frames whose height change waits to be
	// compensated; the watchers go with close.
	htmlOn        bool
	mineOn        bool
	othersOn      bool
	expanded      conversation.Expanded
	expandedScope string
	entries       map[api.MessageID]*loadedMessage
	asked         map[api.MessageID]bool
	heightPending map[*gtk.Frame]bool
	updateQueued  bool
	layoutTries   int
	watchers      []func()
}

func newBoardChat(b *boardConversation) *boardChat {
	installConvCSS()
	c := &boardChat{b: b, cards: map[*chatRow]board.MessageCard{}, byID: map[api.MessageID]*chatRow{},
		entries: map[api.MessageID]*loadedMessage{}, asked: map[api.MessageID]bool{}, heightPending: map[*gtk.Frame]bool{}}
	// As conversation_view.blp's conversation_chat_toggle: checked while
	// the chat shows.
	c.toggle = gtk.NewToggleButton()
	c.toggle.SetVAlign(gtk.AlignCenter)
	c.toggleContent = adw.NewButtonContent()
	c.toggleContent.SetIconName("malachi-chat-symbolic")
	c.toggleContent.SetCanShrink(true)
	c.toggle.SetChild(c.toggleContent)
	c.column = gtk.NewBox(gtk.OrientationVertical, 0)
	c.column.ConnectMap(c.queueSize)
	return c
}

// layouts is the stack of the cards' column and the chat's, which the
// block puts where the cards' column stood.
func (c *boardChat) layouts(cards *gtk.Box) *gtk.Stack {
	c.stack = gtk.NewStack()
	c.stack.SetHhomogeneous(false)
	c.stack.SetVhomogeneous(false)
	c.stack.SetTransitionType(gtk.StackTransitionTypeNone)
	c.stack.AddNamed(cards, boardChatCards)
	c.stack.AddNamed(c.column, boardChatPage)
	c.stack.SetVisibleChildName(boardChatCards)
	c.stack.SetVisible(false)
	return c.stack
}

// bind ties the toggle to the setting; without a settings store the
// toggle is hidden and the cards stay.
func (c *boardChat) bind() {
	s := c.b.p.w.settings
	if s == nil {
		c.toggle.SetVisible(false)
		return
	}
	c.unbind = s.Bind(settings.KeyConversationChatLayout, c.toggle.Object, "active")
	c.remove = s.OnChanged(settings.KeyConversationChatLayout, c.applyLayout)
	c.htmlOn = s.ConversationChatHTML()
	c.mineOn, c.othersOn = s.ConversationChatTruncateMine(), s.ConversationChatTruncateOthers()
	truncChanged := func() {
		mine, others := s.ConversationChatTruncateMine(), s.ConversationChatTruncateOthers()
		if (mine == c.mineOn && others == c.othersOn) || c.closed {
			return
		}
		c.mineOn, c.othersOn = mine, others
		for _, r := range c.rows {
			c.shorten(r)
		}
		c.queueUpdate()
	}
	c.watchers = append(c.watchers,
		s.OnChanged(settings.KeyConversationChatHTML, c.htmlChanged),
		s.OnChanged(settings.KeyConversationChatTruncateMine, truncChanged),
		s.OnChanged(settings.KeyConversationChatTruncateOthers, truncChanged),
		s.OnChanged(settings.KeyTextZoom, func() {
			for _, r := range c.rows {
				r.bubble.setZoom(s.TextZoom())
			}
		}))
	if scroller := c.b.p.boardReplyScroller(); scroller != nil {
		adj := scroller.VAdjustment()
		value := adj.ConnectValueChanged(c.queueUpdate)
		changed := adj.ConnectChanged(c.queueUpdate)
		c.watchers = append(c.watchers, func() {
			adj.HandlerDisconnect(value)
			adj.HandlerDisconnect(changed)
		})
	}
	box := c.b.p.conversationBox
	mapped := box.ConnectMap(c.queueUpdate)
	unmapped := box.ConnectUnmap(c.queueUpdate)
	sm := adw.StyleManagerGetDefault()
	dark := sm.NotifyProperty("dark", c.styleChanged)
	accent := sm.NotifyProperty("accent-color", c.styleChanged)
	c.watchers = append(c.watchers, func() {
		box.HandlerDisconnect(mapped)
		box.HandlerDisconnect(unmapped)
		sm.HandlerDisconnect(dark)
		sm.HandlerDisconnect(accent)
	})
	c.applyLayout()
}

// applyLayout shows the layout the setting asks for, and the toggle's
// label, tooltip and accessible name and description name what a click
// shows (board.ChatToggleLabel, ChatToggleTooltip), as Mail's
// conversationHost.applyLayout does.
func (c *boardChat) applyLayout() {
	if c.closed {
		return
	}
	s := c.b.p.w.settings
	on := s != nil && s.ConversationChatLayout()
	label := board.ChatToggleLabel(on, i18n.Tr)
	tooltip := board.ChatToggleTooltip(on, i18n.Tr)
	c.toggleContent.SetLabel(label)
	c.toggle.SetTooltipText(tooltip)
	c.toggle.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel, gtk.AccessiblePropertyDescription},
		[]coreglib.Value{*coreglib.NewValue(label), *coreglib.NewValue(tooltip)})
	if on == c.shown && c.started {
		return
	}
	c.started = true
	c.shown = on
	if on {
		c.stack.SetVisibleChildName(boardChatPage)
		c.render()
		c.queueUpdate()
	} else {
		c.stack.SetVisibleChildName(boardChatCards)
		c.releaseAll()
	}
	// The cards let go of their web views while the chat shows (live),
	// and open them again when they show.
	c.b.refresh()
	c.b.fetchBodies()
}

// apply follows the block's detail: nil hides the layouts (no case, or the
// block's loading, failure or empty note stands in their place).
func (c *boardChat) apply(d *board.Detail) {
	if c.closed {
		return
	}
	if d == nil {
		c.visible = false
		c.stack.SetVisible(false)
		c.msgs = nil
		c.clearRows()
		return
	}
	c.visible = true
	c.stack.SetVisible(true)
	chatExpandedScope(&c.expanded, &c.expandedScope, string(d.ID))
	if d.AccountID != c.account {
		c.entries = map[api.MessageID]*loadedMessage{}
		c.asked = map[api.MessageID]bool{}
	}
	c.account = d.AccountID
	c.msgs = d.Messages
	c.render()
}

// render shows the bubbles of the last detail while the chat shows: rows
// of members with an id are kept across refreshes (an inline autosave
// refreshes the detail), the samples' rebuilt when they changed.
func (c *boardChat) render() {
	if !c.shown || !c.visible || c.closed {
		return
	}
	keyed := true
	for _, m := range c.msgs {
		if m.ID == "" {
			keyed = false
			break
		}
	}
	if !keyed {
		if len(c.rows) == len(c.msgs) && len(c.byID) == 0 {
			same := true
			for i, r := range c.rows {
				if c.cards[r] != c.msgs[i] {
					same = false
					break
				}
			}
			if same {
				return
			}
		}
		c.clearRows()
	}
	max := c.bubbleMax()
	next := make([]*chatRow, 0, len(c.msgs))
	nextByID := map[api.MessageID]*chatRow{}
	for _, cb := range board.ChatRows(c.msgs) {
		m := c.msgs[cb.Index]
		var row *chatRow
		if keyed {
			if old, ok := c.byID[m.ID]; ok && old.right == cb.Right {
				row = old
				delete(c.byID, m.ID)
			}
		}
		if row == nil {
			row = newChatRow(cb.Right, c.activate)
			row.bubble.chat = c
		}
		row.setMax(max)
		row.setGap(c.bubbleGap())
		c.show(row, m)
		c.shorten(row)
		c.renderHTML(row)
		next = append(next, row)
		if keyed {
			nextByID[m.ID] = row
		}
	}
	// Every row shown before that the next set does not keep goes: a
	// member gone, a side changed, or a sample's row (samples have no id,
	// so they are never in byID) when the detail now has ids.
	kept := make(map[*chatRow]bool, len(next))
	for _, row := range next {
		kept[row] = true
	}
	for _, old := range c.rows {
		if !kept[old] {
			old.bubble.setLive(false)
			c.column.Remove(old)
			delete(c.cards, old)
		}
	}
	var previous gtk.Widgetter
	for _, row := range next {
		if row.Parent() == nil {
			c.column.InsertChildAfter(row, previous)
		} else {
			c.column.ReorderChildAfter(row, previous)
		}
		previous = row
	}
	c.rows, c.byID = next, nextByID
	c.followSize()
	c.queueUpdate()
}

// show shows member m in row: the header names From ("You" for the
// user's own), the avatar's initials come from the real sender (Sender),
// the date is formatted as Mail's chat formats it, and the text is the
// case's own (never a placeholder, never the monospace setting's).
func (c *boardChat) show(row *chatRow, m board.MessageCard) {
	if old, ok := c.cards[row]; ok && old == m && row.bubble.compact == c.compact {
		return
	}
	c.cards[row] = m
	side := conversation.ChatLeft
	if m.Mine {
		side = conversation.ChatRight
	}
	item := conversation.Item{
		Kind:    conversation.ItemMessage,
		Message: api.MessageSummary{ID: m.ID, AccountID: c.account, Date: m.Date, Snippet: m.Text},
		Sender:  m.From,
		Mine:    m.Mine,
	}
	row.row = conversation.ChatRow{Kind: conversation.ChatBubble, Side: side, Item: item}
	row.showMessage(item, bubbleAttachments{}, c.compact)
	row.avatar.SetText(m.Sender)
	row.bubble.showBody(conversation.BubbleText(m.Text), false, false)
}

// activate is a double-click on a bubble: the message in its own window
// (a sample opens nothing and says so, as Show in Mail does).
func (c *boardChat) activate(row *chatRow) {
	m, ok := c.cards[row]
	if !ok || c.closed {
		return
	}
	p := c.b.p
	w := p.w
	if p.daemon == nil || !board.ChatOpens(m) {
		w.Toast(board.Later(i18n.Tr))
		return
	}
	w.withMessage(c.account, m.ID, w.openMessage, func(err error) {
		w.Toast(boardChatMissingText(err))
	})
}

// boardChatMissingText is the toast of a bubble whose message could not
// be opened: gone from the daemon, or not loaded (Show in Mail's texts).
func boardChatMissingText(err error) string {
	if err != nil && revealGone(err) {
		return board.ShowInMailGone(i18n.Tr)
	}
	return board.ShowInMailFailed(i18n.Tr)
}

func (c *boardChat) clearRows() {
	for _, r := range c.rows {
		r.bubble.setLive(false)
		c.column.Remove(r)
	}
	c.rows = nil
	c.cards = map[*chatRow]board.MessageCard{}
	c.byID = map[api.MessageID]*chatRow{}
}

// bubbleMax is the widest a bubble gets in the column as last sized
// (convChatBubbleMax), in whole pixels; 0 before the column has a width.
func (c *boardChat) bubbleMax() int {
	if c.columnWidth <= 0 {
		return 0
	}
	return int(math.Floor(convChatBubbleMax(c.columnWidth)))
}

// widthChanged is the detail's width change (the block's scroller): the
// column is allocated after it, so the size is followed now and once more
// when idle.
func (c *boardChat) widthChanged() {
	c.followSize()
	c.queueSize()
	c.queueUpdate()
}

func (c *boardChat) queueSize() {
	if c.sizeQueued || c.closed {
		return
	}
	c.sizeQueued = true
	glib.IdleAdd(func() {
		c.sizeQueued = false
		c.followSize()
	})
}

// followSize follows the column's width as Mail's chat does
// (conversationChat.followSize): the bubbles' cap (convChatBubbleMax) and
// the short dates of a narrow column (convChatCompactDates); before the
// column's first allocation the width comes from the detail's scroller
// (convChatColumn).
func (c *boardChat) followSize() {
	if c.closed || !c.shown {
		return
	}
	column := float64(c.column.Width())
	if column <= 0 {
		if scroller := c.b.p.boardReplyScroller(); scroller != nil {
			column = convChatColumn(float64(scroller.Width()))
		}
	}
	if column <= 0 || math.Abs(column-c.columnWidth) < 0.5 {
		return
	}
	c.columnWidth = column
	max := c.bubbleMax()
	gap := c.bubbleGap()
	compact := convChatCompactDates(column)
	compactChanged := compact != c.compact
	c.compact = compact
	for _, r := range c.rows {
		r.setMax(max)
		r.setGap(gap)
		if compactChanged {
			r.bubble.setCompact(compact)
		}
	}
}

// close unbinds the toggle and lets the rows go (the block's close).
func (c *boardChat) close() {
	if c.closed {
		return
	}
	c.clearRows()
	if c.unbind != nil {
		c.unbind()
		c.unbind = nil
	}
	if c.remove != nil {
		c.remove()
		c.remove = nil
	}
	for _, f := range c.watchers {
		f()
	}
	c.watchers = nil
	c.closed = true
}

// bubbleGap is the empty side of a row whose bubble fills its maximum (an
// HTML body), as Mail's chat's.
func (c *boardChat) bubbleGap() int {
	if c.columnWidth <= 0 {
		return 0
	}
	return max(0, int(math.Floor(c.columnWidth))-convAvatar-convGutterGap-c.bubbleMax())
}

// The HTML bubbles.

// htmlLive says whether bubbles may show HTML now: the preference on, the
// chat shown with a case, the board page shown with a client.
func (c *boardChat) htmlLive() bool {
	p := c.b.p
	return !c.closed && c.htmlOn && c.shown && c.visible && !p.replyClosed && p.w.mode == board.ModeBoard &&
		p.w.client != nil && p.conversationBox.Mapped() && c.column.Mapped()
}

// htmlChanged follows the preference conversation-chat-html: off lets
// every view and body go (nothing is fetched any more), on renders the
// bubbles near the viewport anew.
func (c *boardChat) htmlChanged() {
	s := c.b.p.w.settings
	on := s != nil && s.ConversationChatHTML()
	if on == c.htmlOn || c.closed {
		return
	}
	c.htmlOn = on
	if !on {
		c.releaseAll()
		c.entries = map[api.MessageID]*loadedMessage{}
		c.asked = map[api.MessageID]bool{}
	}
	for _, r := range c.rows {
		c.renderHTML(r)
	}
	c.queueUpdate()
}

// shorten shows row shortened or whole as the preference and the user's
// expanded bubbles in this case say, as Mail's chat (a sample's bubble,
// without an id, only shows whole).
func (c *boardChat) shorten(row *chatRow) {
	row.bubble.onExpand = func() {
		chatExpandRow(&c.expanded, row.row)
		row.bubble.setShortened(false)
		c.queueUpdate()
	}
	row.bubble.setShortened(chatRowShortened(c.mineOn, c.othersOn, &c.expanded, row.row))
}

// releaseAll lets every bubble's view go (its text shows).
func (c *boardChat) releaseAll() {
	for _, r := range c.rows {
		r.bubble.setLive(false)
	}
}

// renderHTML gives the bubble of row the HTML of its member's entry
// (bubbleHTML; nil: the case's text).
func (c *boardChat) renderHTML(row *chatRow) {
	m, ok := c.cards[row]
	if !ok || m.ID == "" {
		row.bubble.setHTML(nil)
		return
	}
	row.bubble.setHTML(bubbleHTML(c.entries[m.ID], c.htmlOn))
}

// loaded is the news of a member's entry (its body arrived, or Mail's
// cache changed it: the remote images, the quoted text).
func (c *boardChat) loaded(id api.MessageID, lm *loadedMessage) {
	if c.closed || !c.htmlOn || lm == nil || !lm.bodySettled() || lm.fetching {
		return
	}
	row := c.byID[id]
	if row == nil {
		return
	}
	c.entries[id] = lm
	c.renderHTML(row)
	c.queueUpdate()
}

// styleChanged loads the shown documents again in the style's colours.
func (c *boardChat) styleChanged() {
	for _, r := range c.rows {
		if r.bubble.web != nil {
			r.bubble.loadWeb()
		}
	}
}

func (c *boardChat) queueUpdate() {
	if c.updateQueued || c.closed {
		return
	}
	c.updateQueued = true
	glib.IdleAdd(func() {
		c.updateQueued = false
		c.update()
	})
}

// update asks for the bodies of the bubbles near the visible part of the
// detail and gives views to the nearest HTML bubbles (chatLiveRows, the
// board's cap), taking them from the rest.
func (c *boardChat) update() {
	if !c.htmlLive() {
		c.releaseAll()
		return
	}
	scroller := c.b.p.boardReplyScroller()
	if scroller == nil {
		c.releaseAll()
		return
	}
	adj := scroller.VAdjustment()
	visible := convSpanOf(0, adj.PageSize())
	frames := make([]convSpan, 0, len(c.rows))
	html := make([]bool, 0, len(c.rows))
	placed := make([]*chatRow, 0, len(c.rows))
	unplaced := false
	for _, r := range c.rows {
		b, ok := r.ComputeBounds(scroller)
		if !ok || (r.Width() == 0 && r.Height() == 0) {
			unplaced = true
			continue
		}
		frames = append(frames, convSpanOf(float64(b.Y()), float64(b.Y()+b.Height())))
		html = append(html, r.bubble.wantsHTML())
		placed = append(placed, r)
	}
	live := chatLiveRows(frames, html, visible, board.ConversationMaxLiveWebViews)
	for i, r := range placed {
		if live.Near[i] {
			c.fetch(c.cards[r].ID)
		}
		r.bubble.setLive(live.Web[i])
	}
	if unplaced && c.layoutTries < convLayoutTries {
		c.layoutTries++
		c.column.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
			c.queueUpdate()
			return false
		})
	} else if !unplaced {
		c.layoutTries = 0
	}
}

// fetch asks Mail's cache for the body of member id once (the default
// trimQuoted variant, as the board's cards).
func (c *boardChat) fetch(id api.MessageID) {
	if id == "" || c.asked[id] {
		return
	}
	c.asked[id] = true
	account := c.account
	c.b.p.w.fetchBodyOnly(account, id, func(lm *loadedMessage) {
		if c.closed || c.account != account {
			return
		}
		c.loaded(id, lm)
	})
}

// The bubbles' web views (chatWebHost): a view of their own each, as the
// board's cards, released when let go.
func (c *boardChat) chatTakeWeb(zoom int) *htmlview.Card {
	w := c.b.p.w
	return htmlview.NewCard(w.log, w.fetchPart, zoom)
}
func (c *boardChat) chatGiveWeb(web *htmlview.Card) { web.Release() }
func (c *boardChat) chatZoom() int {
	if s := c.b.p.w.settings; s != nil {
		return s.TextZoom()
	}
	return 100
}
func (c *boardChat) chatHover(uri string) { c.b.showHover(uri) }
func (c *boardChat) chatLink(uri string, links []api.Link) {
	w := c.b.p.w
	w.openLink(&w.Window, uri, links)
}
func (c *boardChat) chatScrollBy(dy float64, unit gdk.ScrollUnit) {
	scroller := c.b.p.boardReplyScroller()
	if scroller == nil {
		return
	}
	adj := scroller.VAdjustment()
	if unit == gdk.ScrollUnitWheel {
		dy *= adj.StepIncrement()
	}
	adj.SetValue(adj.Value() + dy)
}

// chatHeight runs a bubble's height change keeping the reader's place:
// a bubble above the viewport moves the viewport by its change, as the
// board's cards do (boardConversationCard.changingHeight).
func (c *boardChat) chatHeight(frame *gtk.Frame, change func()) {
	scroller := c.b.p.boardReplyScroller()
	if c.closed || scroller == nil || !frame.Mapped() || c.heightPending[frame] {
		change()
		return
	}
	bounds, ok := frame.ComputeBounds(scroller)
	if !ok {
		change()
		return
	}
	adj := scroller.VAdjustment()
	top := adj.Value()
	maxY := float64(bounds.Y()+bounds.Height()) + top
	before := frame.Height()
	c.heightPending[frame] = true
	change()
	frame.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		glib.IdleAdd(func() {
			delete(c.heightPending, frame)
			if c.closed || !frame.Mapped() {
				return
			}
			delta := float64(frame.Height() - before)
			compensated := board.ConversationCompensatedTop(top, maxY, delta, adj.Upper(), adj.PageSize())
			adj.SetValue(adj.Value() + compensated - top)
		})
		return false
	})
}

// loadedWithChat is the board block's share of Mail's cache fan-out
// (Window.showLoaded): the cards' and the chat's.
func (b *boardConversation) loadedWithChat(id api.MessageID, lm *loadedMessage) {
	b.loaded(id, lm)
	if b.chat != nil {
		b.chat.loaded(id, lm)
	}
}
