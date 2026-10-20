// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"math"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/conversation"
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
}

func newBoardChat(b *boardConversation) *boardChat {
	installConvCSS()
	c := &boardChat{b: b, cards: map[*chatRow]board.MessageCard{}, byID: map[api.MessageID]*chatRow{}}
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
	} else {
		c.stack.SetVisibleChildName(boardChatCards)
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
		}
		row.setMax(max)
		c.show(row, m)
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
	compact := convChatCompactDates(column)
	compactChanged := compact != c.compact
	c.compact = compact
	for _, r := range c.rows {
		r.setMax(max)
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
	c.closed = true
}
