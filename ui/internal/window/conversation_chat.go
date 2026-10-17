// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"math"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/conversation"
)

// The chat layout of the conversation in the reading pane
// (docs/plans/chat-view.md, conversation.ChatRows): the conversation as a
// messaging app shows it, oldest first from the top, open at the end. The
// others' messages are bubbles on the left, the user's own (and the user's
// replies in Sent, whichever identity sent them) on the right, the drafts
// last on the right; an issue's events and the row of older members left
// out are centred rows; a Jira conversation has its issue card on top
// across the column. The column is the timeline's (conversation_view.blp:
// no wider than 900, the same insets), without its gutter and line: the
// avatars stand beside the bubbles (conversation_chat_row.go).
//
// A bubble is plain text, never a web view: the message's text as the
// daemon derived it, without its quoted history (bubbleBody), the
// summary's snippet dimmed until the body is there. The bodies are asked
// for only for the bubbles near the viewport (chatNear: liveCards over the
// rows' frames, two heights of the view), from the same controller and
// cache of bodies as the timeline, so switching layouts asks thread.get
// nothing. A double click anywhere in a bubble opens the message in its
// own window, a draft in the compose window (chatActivate); nothing else
// is interactive, and the text cannot be selected (D15).
//
// The chat opens at its end. While the viewport shows the end (and until
// the user scrolls away from it), the end stays in view as bodies arrive,
// members come and the pane changes its size; otherwise the row under the
// viewport's top keeps its place (convChatScroll). The vertical
// adjustment's "changed" comes after the column's allocation, so the
// position is put back within the same frame, from what was measured at
// rest before the change. Space and Shift+Space from the list page through
// it (pageBy, through the host). The host is the controller's only
// subscriber (conversation_host.go): it hands the changes on (changed,
// loaded) and trims the controller's bodies with the near set this layout
// reports only while it is the layout shown. A port of the macOS client's
// ConversationChatViewController.swift.

// conversationChat is the chat layout of the conversation page.
type conversationChat struct {
	w    *Window
	host *conversationHost
	ctrl *conversationController

	scroller *gtk.ScrolledWindow
	clamp    *adw.Clamp
	column   *gtk.Box
	// issue is the issue card of a Jira conversation, across the column.
	issue convIssueSlot

	// The rows: the bubbles of the members and of the drafts, by id, the
	// centred rows of the events, and the row of older members, made once.
	bubbles   map[api.MessageID]*chatRow
	drafts    map[api.DraftID]*chatRow
	events    map[api.MessageID]*chatCenterRow
	truncated *chatCenterRow
	// order are the rows in the order shown, under the issue card; members
	// the message rows among them, for chatNear.
	order   []gtk.Widgetter
	members []*chatRow

	// shown: the host shows this layout.
	shown bool
	// scroll is where the viewport stays (convChatScroll); last the row
	// under the viewport's top as last measured at rest.
	scroll       convChatScroll
	last         *convAnchor
	settingValue bool
	// columnWidth is the column's inner width the rows were last sized
	// for (followSize; 0 before the first allocation), compact whether the
	// bubbles show the short date (convChatCompactDates).
	columnWidth float64
	compact     bool

	nearScheduled bool
	// layoutTick, layoutTries: as the timeline's afterLayout.
	layoutTick  uint
	layoutTries int
}

// newConversationChat is the chat layout over the host's controller, from
// conversation_view.ui (b).
func newConversationChat(h *conversationHost, b *gtk.Builder) *conversationChat {
	cv := &conversationChat{
		w:        h.w,
		host:     h,
		ctrl:     h.ctrl,
		scroller: b.GetObject("conversation_chat_scroller").Cast().(*gtk.ScrolledWindow),
		clamp:    b.GetObject("conversation_chat_clamp").Cast().(*adw.Clamp),
		column:   b.GetObject("conversation_chat_column").Cast().(*gtk.Box),
		issue:    convIssueSlot{box: b.GetObject("conversation_chat_issue").Cast().(*gtk.Box)},
		bubbles:  map[api.MessageID]*chatRow{},
		drafts:   map[api.DraftID]*chatRow{},
		events:   map[api.MessageID]*chatCenterRow{},
	}
	cv.scroller.ConnectMap(cv.scheduleNear)
	adj := cv.scroller.VAdjustment()
	adj.ConnectChanged(func() {
		if !cv.shown || cv.ctrl.model == nil {
			return
		}
		// After the column's allocation (a body arrived, a member came,
		// the pane changed its size): the end stays in view when it was in
		// view before, else the row the user read keeps its place.
		cv.followSize()
		cv.settle()
		cv.scheduleNear()
	})
	adj.ConnectValueChanged(func() {
		if !cv.shown {
			return
		}
		if !cv.settingValue {
			// The user scrolled (or GTK kept the view in the document):
			// away from the end, the end is no longer pinned.
			if !cv.atEnd() {
				cv.scroll.scrolled()
			}
			cv.measure()
		}
		cv.scheduleNear()
	})
	// A scroll by the user (the wheel, the touchpad) lets go of the end.
	sc := gtk.NewEventControllerScroll(gtk.EventControllerScrollVertical)
	sc.SetPropagationPhase(gtk.PhaseCapture)
	sc.ConnectScroll(func(float64, float64) bool {
		cv.scroll.scrolled()
		return false
	})
	cv.scroller.AddController(sc)
	return cv
}

// changed follows the controller (through the host). While hidden the rows
// are only taken out when the conversation goes; setShown(true) builds the
// rest.
func (cv *conversationChat) changed(ch convChange) {
	switch ch {
	case convLoading, convCleared:
		cv.removeAll()
	case convOpened:
		if cv.shown {
			cv.open()
		} else {
			cv.removeAll()
		}
	case convUpdated:
		if cv.shown {
			cv.update()
		}
	}
}

// loaded: a bubble's entry has news (through the host while this layout is
// shown): its text and its paperclip.
func (cv *conversationChat) loaded(id api.MessageID, lm *loadedMessage) {
	if row := cv.bubbles[id]; row != nil {
		cv.render(row, lm)
	}
}

// setShown shows the chat (on) or hides it for the timeline. Shown, the
// chat catches up with the controller (the rows reconciled with the model,
// every bubble rendered from the entries held) and opens at its end.
func (cv *conversationChat) setShown(on bool) {
	if on == cv.shown {
		return
	}
	cv.shown = on
	if on {
		cv.open()
	}
}

// open shows the conversation anew (a new one, or this layout shown): the
// rows as the model has them, the viewport at the end, the bubbles near it
// asked for their bodies.
func (cv *conversationChat) open() {
	m := cv.ctrl.model
	if m == nil {
		cv.removeAll()
		return
	}
	cv.followSize()
	cv.apply(m)
	for id, row := range cv.bubbles {
		cv.render(row, cv.ctrl.loaded[id])
	}
	cv.layoutTries = 0
	cv.scroll.open()
	cv.scrollToEnd()
	cv.scheduleNear()
}

// update: the shown conversation changed; the rows are reconciled by id.
// The end stays in view when it was (the adjustment's "changed" follows
// the new heights), else the row the user reads keeps its place.
func (cv *conversationChat) update() {
	m := cv.ctrl.model
	if m == nil {
		return
	}
	cv.apply(m)
	cv.settle()
	cv.scheduleNear()
}

// apply puts the rows of m (conversation.ChatRows: the row of older
// members, the items oldest first, the drafts last) into the column after
// the issue card, reusing the rows of the members, events and drafts shown
// already; a member whose side changed gets a new row.
func (cv *conversationChat) apply(m *conversation.Model) {
	cv.issue.show(cv.w, cv.ctrl, m)
	rows := conversation.ChatRows(*m, cv.ctrl.drafts)
	desired := make([]gtk.Widgetter, 0, len(rows))
	members := make([]*chatRow, 0, len(rows))
	bubbleIDs := map[api.MessageID]bool{}
	eventIDs := map[api.MessageID]bool{}
	draftIDs := map[api.DraftID]bool{}
	bubbleMax := cv.bubbleMax()
	for _, r := range rows {
		switch r.Kind {
		case conversation.ChatTruncated:
			if cv.truncated == nil {
				cv.truncated = newChatCenterRow()
			}
			cv.truncated.showText(r.Item.Text)
			cv.truncated.setMax(bubbleMax)
			desired = append(desired, cv.truncated)
		case conversation.ChatBubble:
			id := r.Item.Message.ID
			bubbleIDs[id] = true
			right := r.Side == conversation.ChatRight
			row := cv.bubbles[id]
			if row == nil || row.right != right {
				if row != nil {
					cv.detach(row)
				}
				row = newChatRow(right, cv.activate)
				cv.bubbles[id] = row
			}
			row.row = r
			row.setMax(bubbleMax)
			row.showMessage(r.Item, bubbleAttachmentBadge(r.Item.Message, cv.ctrl.loaded[id]), cv.compact)
			if !row.bubble.showsText {
				lm := cv.ctrl.loaded[id]
				text, placeholder := bubbleBody(lm, r.Item.Message.Snippet)
				row.bubble.showBody(text, placeholder, bubbleMonospace(lm, true))
			}
			desired = append(desired, row)
			members = append(members, row)
		case conversation.ChatEvent:
			id := r.Item.Message.ID
			eventIDs[id] = true
			e := cv.events[id]
			if e == nil {
				e = newChatCenterRow()
				cv.events[id] = e
			}
			e.showEvent(r.Item, cv.compact)
			e.setMax(bubbleMax)
			desired = append(desired, e)
		case conversation.ChatDraft:
			id := r.Draft.Draft.ID
			draftIDs[id] = true
			row := cv.drafts[id]
			if row == nil {
				row = newChatRow(true, cv.activate)
				cv.drafts[id] = row
			}
			row.row = r
			row.setMax(bubbleMax)
			row.showDraft(r.Draft)
			desired = append(desired, row)
		}
	}
	for id, row := range cv.bubbles {
		if !bubbleIDs[id] {
			cv.detach(row)
			delete(cv.bubbles, id)
		}
	}
	for id, e := range cv.events {
		if !eventIDs[id] {
			cv.detach(e)
			delete(cv.events, id)
		}
	}
	for id, row := range cv.drafts {
		if !draftIDs[id] {
			cv.detach(row)
			delete(cv.drafts, id)
		}
	}
	keepTruncated := false
	for _, w := range desired {
		keepTruncated = keepTruncated || (cv.truncated != nil && w == gtk.Widgetter(cv.truncated))
	}
	if !keepTruncated && cv.truncated != nil {
		cv.detach(cv.truncated)
	}
	// The column after the issue box, in the order of the rows.
	var prev gtk.Widgetter = cv.issue.box
	for _, r := range desired {
		base := gtk.BaseWidget(r)
		if base.Parent() == nil {
			cv.column.InsertChildAfter(r, prev)
		} else if p := base.PrevSibling(); p == nil || !gtk.BaseWidget(p).Eq(prev) {
			cv.column.ReorderChildAfter(r, prev)
		}
		prev = r
	}
	cv.order = desired
	cv.members = members
}

// render shows what the entry lm of the member of row holds (nil: nothing
// asked for yet, or let go): the text (bubbleBody; a text shown already
// stays when lm has none) and the paperclip (bubbleAttachmentBadge).
func (cv *conversationChat) render(row *chatRow, lm *loadedMessage) {
	if row.row.Kind != conversation.ChatBubble || cv.ctrl.model == nil {
		return
	}
	id := row.row.Item.Message.ID
	i := cv.ctrl.model.Index(id)
	if i < 0 {
		return
	}
	item := cv.ctrl.model.Items[i]
	row.showMessage(item, bubbleAttachmentBadge(item.Message, lm), cv.compact)
	text, placeholder := bubbleBody(lm, item.Message.Snippet)
	row.bubble.showBody(text, placeholder, bubbleMonospace(lm, true))
}

// activate is a double click on a bubble (chatActivate).
func (cv *conversationChat) activate(row *chatRow) {
	chatActivate(cv.ctrl, row.row)
}

// detach takes a row out of the column.
func (cv *conversationChat) detach(w gtk.Widgetter) {
	base := gtk.BaseWidget(w)
	if base.Parent() != nil {
		cv.column.Remove(w)
	}
}

// removeAll takes everything out (another conversation, or none).
func (cv *conversationChat) removeAll() {
	for _, row := range cv.bubbles {
		cv.detach(row)
	}
	for _, row := range cv.drafts {
		cv.detach(row)
	}
	for _, e := range cv.events {
		cv.detach(e)
	}
	if cv.truncated != nil {
		cv.detach(cv.truncated)
	}
	cv.bubbles = map[api.MessageID]*chatRow{}
	cv.drafts = map[api.DraftID]*chatRow{}
	cv.events = map[api.MessageID]*chatCenterRow{}
	cv.order = nil
	cv.members = nil
	cv.scroll.clear()
	cv.last = nil
	cv.layoutTries = 0
	cv.issue.clear()
}

// Width.

// bubbleMax is the widest a bubble gets in the column as last sized
// (convChatBubbleMax), in whole pixels; 0 while the column has no width
// yet (the rows keep no cap until it has).
func (cv *conversationChat) bubbleMax() int {
	if cv.columnWidth <= 0 {
		return 0
	}
	return int(math.Floor(convChatBubbleMax(cv.columnWidth)))
}

// followSize follows the column's width: a change sizes the bubbles anew
// (convChatBubbleMax) and may switch the short dates
// (convChatCompactDates). The column is the clamp's child as allocated
// (its inner width, the margins left out, which the bubbles do not change),
// so that classic scrollbars and a text scale other than 1 (the clamp's
// 900 is in scale-independent pixels) leave the bubbles their empty side;
// before its first allocation it is worked out from the scrolled window's
// width (convChatColumn). The column is allocated after the adjustment's
// "changed", so the idle updateNear picks a new width up and the layout it
// causes settles the position again. Whether the end stays in view is
// settle's (convChatScroll.toEnd).
func (cv *conversationChat) followSize() {
	column := float64(cv.column.Width())
	if column <= 0 {
		column = convChatColumn(float64(cv.scroller.Width()))
	}
	if column <= 0 || math.Abs(column-cv.columnWidth) < 0.5 {
		return
	}
	cv.columnWidth = column
	max := cv.bubbleMax()
	compact := convChatCompactDates(column)
	compactChanged := compact != cv.compact
	cv.compact = compact
	for _, row := range cv.bubbles {
		row.setMax(max)
		if compactChanged {
			row.bubble.setCompact(compact)
		}
	}
	for _, row := range cv.drafts {
		row.setMax(max)
	}
	for _, e := range cv.events {
		e.setMax(max)
		if compactChanged {
			e.setCompact(compact)
		}
	}
	if cv.truncated != nil {
		cv.truncated.setMax(max)
	}
}

// Scroll position.

// settle puts the viewport where the change being laid out leaves it: at
// the end when it was there before (or the end is pinned), else the row
// the user read in its place.
func (cv *conversationChat) settle() {
	if cv.scroll.toEnd() {
		cv.scrollToEnd()
		return
	}
	cv.restore(cv.last)
}

// atEnd reports whether the viewport shows the end of the chat now.
func (cv *conversationChat) atEnd() bool {
	adj := cv.scroller.VAdjustment()
	return convChatAtEnd(adj.Value(), adj.PageSize(), adj.Upper(), convChatEndSlack)
}

// measure records, at rest, whether the viewport shows the end and the row
// under its top: what a change that follows keeps.
func (cv *conversationChat) measure() {
	adj := cv.scroller.VAdjustment()
	cv.scroll.measure(adj.Value(), adj.PageSize(), adj.Upper())
	cv.last = cv.currentAnchor()
}

// scrollToEnd shows the end of the chat (convChatEndTop).
func (cv *conversationChat) scrollToEnd() {
	adj := cv.scroller.VAdjustment()
	top := convChatEndTop(adj.PageSize(), adj.Upper())
	if math.Abs(top-adj.Value()) < 0.5 {
		return
	}
	cv.settingValue = true
	adj.SetValue(top)
	cv.settingValue = false
}

// spanOf is w's stretch of the document; false while w is not in the
// column or not laid out yet.
func (cv *conversationChat) spanOf(w gtk.Widgetter) (convSpan, bool) {
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

// currentAnchor is the first row reaching below the viewport's top (the
// issue card counts), and how far below its top the viewport's top is.
func (cv *conversationChat) currentAnchor() *convAnchor {
	top := cv.scroller.VAdjustment().Value()
	candidates := make([]gtk.Widgetter, 0, len(cv.order)+1)
	if cv.issue.box.IsVisible() {
		candidates = append(candidates, cv.issue.box)
	}
	candidates = append(candidates, cv.order...)
	for _, w := range candidates {
		s, ok := cv.spanOf(w)
		if ok && s.Max > top+0.5 {
			return &convAnchor{widget: w, offset: top - s.Min}
		}
	}
	return nil
}

// restore brings the anchored row back to where it was on screen.
func (cv *conversationChat) restore(a *convAnchor) {
	if a == nil {
		return
	}
	s, ok := cv.spanOf(a.widget)
	if !ok {
		return
	}
	adj := cv.scroller.VAdjustment()
	top := anchoredTop(s.Min, a.offset, adj.Upper(), adj.PageSize())
	if math.Abs(top-adj.Value()) < 0.5 {
		return
	}
	cv.settingValue = true
	adj.SetValue(top)
	cv.settingValue = false
}

// pageBy scrolls one page down (or up) for Space (Shift+Space) in the list
// (through the host). True: the key was used.
func (cv *conversationChat) pageBy(up bool) bool {
	if cv.ctrl.model == nil || !cv.shown {
		return false
	}
	adj := cv.scroller.VAdjustment()
	overlap := adj.PageSize() - adj.PageIncrement()
	cv.scroll.scrolled()
	adj.SetValue(pageTop(adj.Value(), up, adj.PageSize(), adj.Upper(), overlap))
	return true
}

// Bodies.

// scheduleNear runs updateNear once the main loop is idle.
func (cv *conversationChat) scheduleNear() {
	if cv.nearScheduled {
		return
	}
	cv.nearScheduled = true
	glib.IdleAdd(func() {
		cv.nearScheduled = false
		cv.updateNear()
	})
}

// updateNear asks for the bodies of the bubbles near the viewport (within
// two of its heights, chatNear), message.get as well for a message with
// attachments, tells the host which they are, and measures the position
// at rest. Only while this layout is shown.
func (cv *conversationChat) updateNear() {
	if !cv.shown || cv.ctrl.model == nil {
		return
	}
	cv.followSize()
	if cv.scroll.pinned {
		cv.scrollToEnd()
	}
	adj := cv.scroller.VAdjustment()
	visible := convSpanOf(adj.Value(), adj.Value()+adj.PageSize())
	ids := make([]api.MessageID, 0, len(cv.members))
	frames := make([]convSpan, 0, len(cv.members))
	unplaced := false
	for _, row := range cv.members {
		s, ok := cv.spanOf(row)
		if !ok {
			unplaced = true
			continue
		}
		ids = append(ids, row.row.Item.Message.ID)
		frames = append(frames, s)
	}
	near := chatNear(cv.ctrl, ids, frames, visible)
	cv.host.near(cv, near)
	cv.measure()
	if unplaced {
		cv.afterLayout()
	} else {
		cv.layoutTries = 0
	}
}

// afterLayout runs updateNear again after the next frame's layout: rows
// that had no allocation yet were skipped (the timeline's afterLayout: a
// short chat that fits the viewport fires no "changed").
func (cv *conversationChat) afterLayout() {
	if cv.layoutTick != 0 || cv.layoutTries >= convLayoutTries {
		return
	}
	cv.layoutTries++
	cv.layoutTick = cv.scroller.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		cv.layoutTick = 0
		cv.scheduleNear()
		return false
	})
}
