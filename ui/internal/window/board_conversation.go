// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/htmlview"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
)

// BoardConversationBlock and BoardMessageCardView's GTK counterpart. The
// model owns folding and the four-view budget. Bodies and links use Mail's
// cache and locked htmlview.Card; card headers are always native plain text.
// The block lives across detail refreshes (including inline autosaves).
type boardConversation struct {
	p            *boardPage
	state        *board.ConversationCards
	cards        []*boardConversationCard
	heading      *gtk.Label
	status       *gtk.Box
	column       *gtk.Box
	hover        *gtk.Label
	account      api.AccountID
	statusKey    boardConversationStatus
	pending      bool
	closed       bool
	userChanging bool
	zoomRemove   func()
}

type boardConversationStatus struct {
	loading bool
	note    string
	retry   bool
}

type boardConversationCard struct {
	block            *boardConversation
	m                board.MessageCard
	root, head       *gtk.Box
	from, when, text *gtk.Label
	fold             *gtk.Button
	host             *gtk.Overlay
	web              *htmlview.Card
	html             string
	links            []api.Link
	webDoc           string
	lastBody         *api.MessageBodyResult
	reload           bool
	folded           bool
	long             *bool
	measuredWidth    int
	measureTick      uint
	gov              webHeightGovernor
	height           int
	// pendingHeight captures the first geometry before a group of late
	// changes. The next frame settles GTK's allocation before compensating.
	pendingHeight bool
	heightTick    uint
}

func (p *boardPage) renderDetailConversation(d board.Detail) {
	if p.conversation == nil {
		p.conversation = newBoardConversation(p)
	}
	p.conversation.apply(&d)
}

func newBoardConversation(p *boardPage) *boardConversation {
	b := &boardConversation{p: p, state: board.NewConversationCards(0)}
	top := gtk.NewBox(gtk.OrientationHorizontal, 8)
	b.heading = plainLabel("", "heading")
	b.heading.SetHExpand(true)
	top.Append(b.heading)
	show := gtk.NewButtonWithLabel(board.ShowInMail(i18n.Tr))
	show.AddCSSClass("flat")
	show.SetActionName("win.board-show-in-mail")
	top.Append(show)
	b.status = gtk.NewBox(gtk.OrientationVertical, 4)
	b.column = gtk.NewBox(gtk.OrientationVertical, 8)
	b.hover = plainLabel("", "caption", "dim-label")
	b.hover.SetEllipsize(pango.EllipsizeMiddle)
	b.hover.SetVisible(false)
	p.conversationBox.Append(top)
	p.conversationBox.Append(b.status)
	p.conversationBox.Append(b.column)
	p.conversationBox.Append(b.hover)
	// Moving the detail between List and the panel unmaps it briefly: defer
	// the decision, so a view that stays visible keeps its document.
	p.conversationBox.ConnectMap(b.scheduleRefresh)
	p.conversationBox.ConnectUnmap(b.scheduleRefresh)
	if p.w.settings != nil {
		b.zoomRemove = p.w.settings.OnChanged(settings.KeyTextZoom, func() { b.setZoom(p.w.settings.TextZoom()) })
	}
	return b
}

func (b *boardConversation) apply(d *board.Detail) {
	if b.closed {
		return
	}
	if d == nil {
		b.removeAll()
		b.state = board.NewConversationCards(0)
		b.account = ""
		return
	}
	b.account = d.AccountID
	b.heading.SetText(d.ConversationTitle)
	status := boardConversationStatus{d.MessagesLoading, d.MessagesNote, d.MessagesRetry}
	if status != b.statusKey {
		b.statusKey = status
		removeAllChildren(b.status)
		if status.loading || status.note != "" {
			row := gtk.NewBox(gtk.OrientationHorizontal, 6)
			if status.loading {
				spin := gtk.NewSpinner()
				spin.Start()
				row.Append(spin)
			}
			note := plainLabel(status.note, "dim-label")
			note.SetWrap(true)
			row.Append(note)
			b.status.Append(row)
			if status.retry {
				retry := gtk.NewButtonWithLabel(board.TryAgain(i18n.Tr))
				retry.AddCSSClass("flat")
				retry.SetHAlign(gtk.AlignStart)
				retry.ConnectClicked(b.p.ctl.RetryMessages)
				b.status.Append(retry)
			}
		}
	}
	b.status.SetVisible(status.loading || status.note != "")
	if status.loading || status.note != "" {
		if b.state.CaseID() != d.ID {
			b.removeAll()
			b.state = board.NewConversationCards(0)
		}
		b.column.SetVisible(false)
		b.refresh()
		return
	}
	b.column.SetVisible(true)
	changes := b.state.Apply(board.ConversationKeyOf(*d))
	switch changes.Kind {
	case board.ConversationChangeReset:
		b.removeAll()
		for _, m := range d.Messages {
			c := b.makeCard(m)
			b.cards = append(b.cards, c)
			b.column.Append(c.root)
		}
	case board.ConversationChangeMembers:
		next := make([]*boardConversationCard, 0, len(d.Messages))
		kept := make(map[*boardConversationCard]bool)
		for i, m := range d.Messages {
			if old, ok := changes.Kept[i]; ok {
				c := b.cards[old]
				c.update(m)
				next = append(next, c)
				kept[c] = true
			} else {
				next = append(next, b.makeCard(m))
			}
		}
		for _, c := range b.cards {
			if !kept[c] {
				c.close()
				b.column.Remove(c.root)
			}
		}
		var previous gtk.Widgetter
		for _, c := range next {
			if c.root.Parent() == nil {
				b.column.InsertChildAfter(c.root, previous)
			} else {
				b.column.ReorderChildAfter(c.root, previous)
			}
			previous = c.root
		}
		b.cards = next
	case board.ConversationChangeNone:
		// Metadata (sender/date/own-message tint) can change without the key.
		for i, m := range d.Messages {
			b.cards[i].update(m)
		}
	}
	b.refresh()
	b.fetchBodies()
	for _, i := range changes.Reload {
		b.fetchBody(i)
	}
}

func (b *boardConversation) live() bool {
	return !b.closed && !b.p.replyClosed && b.p.w.mode == board.ModeBoard && b.p.w.client != nil && b.p.conversationBox.Mapped() && b.column.Visible()
}
func (b *boardConversation) scheduleRefresh() {
	if b.pending || b.closed {
		return
	}
	b.pending = true
	glib.IdleAdd(func() {
		b.pending = false
		if !b.closed {
			b.refresh()
			b.fetchBodies()
		}
	})
}
func (b *boardConversation) refresh() {
	if b.closed {
		return
	}
	live := b.live()
	// Retire every budget loser before making a newly opened view.
	for i, c := range b.cards {
		if b.state.Shows(i, live) != board.ConversationWeb {
			c.releaseWeb()
		}
	}
	for i, c := range b.cards {
		c.apply(b.state.Shows(i, live), b.state.IsFolded(i), b.state.Foldable(i), b.state.Arrow(i, c.long, b.p.w.client != nil))
	}
}
func (b *boardConversation) fetchBodies() {
	if !b.live() {
		return
	}
	for i := range b.cards {
		if b.state.NeedsBody(i) {
			b.state.Asked(i)
			b.fetchBody(i)
		}
	}
}
func (b *boardConversation) fetchBody(i int) {
	members := b.state.Members()
	if !b.live() || i < 0 || i >= len(members) || members[i].ID == "" {
		return
	}
	member := members[i]
	account := b.account
	caseID := b.state.CaseID()
	// The default cache variant has trimQuoted=true; a variant explicitly
	// revealed in Mail is shared, exactly as Mail's other displays share it.
	b.p.w.fetchBodyOnly(account, member.ID, func(lm *loadedMessage) {
		if b.closed || b.account != account || b.state.CaseID() != caseID {
			return
		}
		b.bodyArrived(member, lm)
	})
}
func (b *boardConversation) bodyArrived(member board.ConversationMember, lm *loadedMessage) {
	if b.closed || lm == nil || !lm.bodySettled() || lm.fetching {
		return
	}
	for i, m := range b.state.Members() {
		if m != member {
			continue
		}
		c := b.cards[i]
		c.reload = c.reload || picturesArrived(c.lastBody, lm.body)
		c.lastBody = lm.body
		c.html, c.links = "", nil
		if lm.err == nil && showsHTML(lm.body) && !lm.body.HTMLWithheld {
			c.html = lm.body.HTML
			c.links = lm.body.Links
		}
		b.state.Answered(i, c.html != "")
		b.refresh()
		return
	}
}

// loaded is Mail's cache fan-out (remote image/quoted-body changes).
func (b *boardConversation) loaded(id api.MessageID, lm *loadedMessage) {
	if b.closed {
		return
	}
	if i, ok := b.state.Index(id); ok && b.state.Body(i) != board.ConversationBodyUnknown {
		b.bodyArrived(b.state.Members()[i], lm)
	}
}
func (b *boardConversation) userFold(c *boardConversationCard, folded bool) {
	for i, card := range b.cards {
		if card == c {
			// A user fold can move the viewport naturally. Late HTML/height changes
			// and folds forced by the budget compensate separately.
			b.userChanging = true
			b.state.SetFolded(i, folded)
			c.apply(b.state.Shows(i, b.live()), b.state.IsFolded(i), b.state.Foldable(i), b.state.Arrow(i, c.long, b.p.w.client != nil))
			b.userChanging = false
			b.refresh()
			b.fetchBodies()
			return
		}
	}
}
func (b *boardConversation) removeAll() {
	for _, c := range b.cards {
		c.close()
		b.column.Remove(c.root)
	}
	b.cards = nil
	b.showHover("")
}
func (b *boardConversation) close() {
	if !b.closed {
		b.closed = true
		b.removeAll()
		if b.zoomRemove != nil {
			b.zoomRemove()
			b.zoomRemove = nil
		}
	}
}
func (b *boardConversation) showHover(uri string) {
	b.hover.SetText(clampText(uri, convStatusMaxRunes))
	b.hover.SetVisible(uri != "")
}
func (b *boardConversation) setZoom(percent int) {
	for _, c := range b.cards {
		if c.web != nil {
			c.web.SetZoom(percent)
			if h, ok := c.gov.setZoom(float64(zoomPercent(percent)) / 100); ok {
				c.setHeight(int(h))
			}
		}
	}
}

func (b *boardConversation) makeCard(m board.MessageCard) *boardConversationCard {
	c := &boardConversationCard{block: b, height: convInitialWebHeight / 2, measuredWidth: -1}
	c.root = gtk.NewBox(gtk.OrientationVertical, 0)
	c.root.AddCSSClass("conversation-card")
	c.root.AddCSSClass("board-conversation-card")
	c.root.SetOverflow(gtk.OverflowHidden)
	c.head = gtk.NewBox(gtk.OrientationHorizontal, 8)
	c.head.SetMarginStart(convCardPaddingH)
	c.head.SetMarginEnd(convCardPaddingH)
	c.head.SetMarginTop(convCardPaddingV)
	c.head.SetMarginBottom(4)
	c.fold = gtk.NewButtonFromIconName("pan-end-symbolic")
	c.fold.AddCSSClass("flat")
	c.fold.SetFocusOnClick(false)
	c.fold.ConnectClicked(func() { b.userFold(c, !c.folded) })
	c.from = plainLabel("", "heading")
	c.from.SetHExpand(true)
	c.from.SetEllipsize(pango.EllipsizeEnd)
	c.from.SetSelectable(true)
	c.when = plainLabel("", "caption", "dim-label")
	c.head.Append(c.fold)
	c.head.Append(c.from)
	c.head.Append(c.when)
	c.root.Append(c.head)
	c.text = plainLabel("")
	c.text.SetWrap(true)
	c.text.SetWrapMode(pango.WrapWordChar)
	c.text.SetMarginStart(convCardPaddingH)
	c.text.SetMarginEnd(convCardPaddingH)
	c.text.SetMarginBottom(convCardPaddingV)
	c.root.Append(c.text)
	c.host = gtk.NewOverlay()
	c.host.AddCSSClass("conversation-paper")
	c.host.SetVisible(false)
	c.host.SetSizeRequest(-1, c.height)
	c.root.Append(c.host)
	c.update(m)
	c.measureTick = c.root.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		width := c.text.Width()
		if width > 0 && width != c.measuredWidth {
			c.measuredWidth = width
			layout := c.text.CreatePangoLayout(c.m.Text)
			layout.SetWidth(width * pango.SCALE)
			layout.SetWrap(pango.WrapWordChar)
			long := layout.LineCount() > 3
			if c.long == nil || *c.long != long {
				c.long = &long
				b.refresh()
			}
		}
		return !b.closed
	})
	c.forwardScroll()
	return c
}
func (c *boardConversationCard) update(m board.MessageCard) {
	if c.m == m {
		return
	}
	c.changingHeight(func() {
		if c.m.Text != m.Text {
			c.long = nil
			c.measuredWidth = -1
		}
		c.m = m
		c.from.SetText(m.From)
		c.when.SetText(m.When)
		if m.Mine {
			c.root.AddCSSClass("board-card-mine")
		} else {
			c.root.RemoveCSSClass("board-card-mine")
		}
		if c.web == nil {
			c.showText()
		}
	})
}
func (c *boardConversationCard) apply(shows board.ConversationShows, folded, foldable, arrow bool) {
	c.fold.SetVisible(foldable && arrow)
	if folded {
		c.fold.SetIconName("pan-end-symbolic")
		c.fold.SetTooltipText(i18n.T("Expand"))
	} else {
		c.fold.SetIconName("pan-down-symbolic")
		c.fold.SetTooltipText(i18n.T("Collapse"))
	}
	wantsWeb := shows == board.ConversationWeb && c.html != ""
	if wantsWeb && c.web != nil && c.folded == folded {
		c.loadWeb()
		return
	}
	if !wantsWeb && c.web == nil && c.folded == folded {
		c.showText()
		return
	}
	c.changingHeight(func() {
		c.folded = folded
		if wantsWeb {
			c.text.SetVisible(false)
			c.host.SetVisible(true)
			c.loadWeb()
		} else {
			c.releaseWeb()
			c.showText()
		}
	})
}
func (c *boardConversationCard) showText() {
	cut := c.folded && (c.long == nil || *c.long)
	text := c.m.Text
	if cut {
		text = strings.Join(strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }), " ")
		c.text.SetLines(3)
		c.text.SetEllipsize(pango.EllipsizeEnd)
	} else {
		c.text.SetLines(-1)
		c.text.SetEllipsize(pango.EllipsizeNone)
	}
	c.text.SetText(text)
	c.text.SetSelectable(!cut)
	c.text.SetVisible(true)
	c.host.SetVisible(false)
}
func (c *boardConversationCard) loadWeb() {
	w := c.block.p.w
	if c.web == nil {
		zoom := 100
		if w.settings != nil {
			zoom = w.settings.TextZoom()
		}
		c.web = htmlview.NewCard(w.log, w.fetchPart, zoom)
		c.gov = newWebHeightGovernor(float64(zoomPercent(zoom)) / 100)
		c.web.OnLink = func(uri string) { w.openLink(&w.Window, uri, c.links) }
		c.web.OnHover = c.block.showHover
		c.web.OnSize = func(css float64, viewport, widthChanged bool) {
			if c.web == nil {
				return
			}
			if widthChanged {
				c.gov.widthChanged()
			}
			if h, ok := c.gov.report(css, viewport); ok {
				c.setHeight(int(h))
			}
		}
		c.web.SetHeight(c.height)
		c.host.AddOverlay(c.web.Widget())
	}
	if c.webDoc != c.html || c.reload {
		c.gov.reset()
	}
	c.web.Load(c.html, c.reload)
	c.webDoc, c.reload = c.html, false
}
func (c *boardConversationCard) releaseWeb() {
	if c.web == nil {
		return
	}
	web := c.web
	c.web = nil
	c.webDoc = ""
	c.host.RemoveOverlay(web.Widget())
	web.Release()
	c.block.showHover("")
}
func (c *boardConversationCard) setHeight(height int) {
	if height <= 0 || c.height == height {
		return
	}
	c.changingHeight(func() {
		c.height = height
		c.host.SetSizeRequest(-1, height)
		if c.web != nil {
			c.web.SetHeight(height)
		}
	})
}
func (c *boardConversationCard) close() {
	if c.measureTick != 0 {
		c.root.RemoveTickCallback(c.measureTick)
		c.measureTick = 0
	}
	if c.heightTick != 0 {
		c.root.RemoveTickCallback(c.heightTick)
		c.heightTick = 0
	}
	c.pendingHeight = false
	c.releaseWeb()
}
func (c *boardConversationCard) changingHeight(change func()) {
	b := c.block
	scroller := b.p.boardReplyScroller()
	if b.userChanging || b.closed || c.pendingHeight || scroller == nil || !c.root.Mapped() {
		change()
		return
	}
	bounds, ok := c.root.ComputeBounds(scroller)
	if !ok {
		change()
		return
	}
	adj := scroller.VAdjustment()
	top := adj.Value()
	maxY := float64(bounds.Y()+bounds.Height()) + top
	before := c.root.Height()
	c.pendingHeight = true
	change()
	c.heightTick = c.root.AddTickCallback(func(gtk.Widgetter, gdk.FrameClocker) bool {
		c.heightTick = 0
		glib.IdleAdd(func() {
			if !c.pendingHeight {
				return
			}
			c.pendingHeight = false
			if b.closed || !c.root.Mapped() {
				return
			}
			// Preserve any scrolling the user did while layout was pending.
			delta := float64(c.root.Height() - before)
			compensated := board.ConversationCompensatedTop(top, maxY, delta, adj.Upper(), adj.PageSize())
			adj.SetValue(adj.Value() + compensated - top)
		})
		return false
	})
}
func (c *boardConversationCard) forwardScroll() {
	sc := gtk.NewEventControllerScroll(gtk.EventControllerScrollVertical)
	sc.SetPropagationPhase(gtk.PhaseCapture)
	sc.ConnectScroll(func(_, dy float64) bool {
		if c.web == nil || !c.gov.fits() {
			return false
		}
		host := c.block.p.boardReplyScroller()
		if host == nil {
			return false
		}
		adj := host.VAdjustment()
		if sc.Unit() == gdk.ScrollUnitWheel {
			dy *= adj.StepIncrement()
		}
		adj.SetValue(adj.Value() + dy)
		return true
	})
	c.host.AddController(sc)
}
