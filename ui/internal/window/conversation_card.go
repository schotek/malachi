// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/htmlview"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// One message of the conversation view (conversation.ItemMessage), a card:
// a header (an unread dot, the sender, a disclosure for the recipients,
// the Jira badges via, Internal and Edited, the date, and on hover Reply —
// Comment on an issue —, Reply All and Forward as the account allows
// them), the recipients, the attachment chips, the remote-image and
// pictures bars, the body, and under it the "•••" that shows the quoted
// history the daemon cut from it (quoted.go). The body is: plain text in a label, HTML in a view of
// its own sized to its document (htmlview.Card; never one document for
// the conversation: a message's CSS must not reach another's headers).
// Headers are plain text; the body's HTML is the sanitiser's output only.
// The sender's avatar is not the card's: it sits on the timeline beside it
// (convRow). A port of the macOS client's ConversationCardView.swift and
// ConversationCardHeader.swift.
//
// Every card folds (setFold, conversation.Folds): an arrow at the start of
// its header, and while folded only the header and a preview of its text
// show. How it starts is the pane's: the card that opened the conversation
// and the user's replies in Sent folded, the rest open.
//
// A card is cheap: the recipients, the chips, the bars, the hint and the
// "•••" are made when first needed, and the web view exists only while the pane
// keeps the card live (setLive, near the viewport); otherwise the body
// keeps the height it last had, on the white of the page.
//
// The recipients, the chips and the bars are the single-message pane's own
// code (addresses.go, attachments.go, remote.go) over a messageView of the
// card's (mv) that holds only those widgets: the chips' Open, Save As,
// Save All and View, the download spinner and the bars' buttons act on
// this card's message exactly as they do in the pane.

// The top part's slots, in order: the header, the recipients, the chips,
// the hint.
const (
	cardSlotHeader = iota
	cardSlotDetails
	cardSlotChips
	cardSlotHint
	cardSlots
)

// convCard is one card of the conversation view.
type convCard struct {
	cv   *conversationView
	id   api.MessageID
	item conversation.Item
	s    api.MessageSummary

	root  *gtk.Box
	top   *gtk.Box
	slots [cardSlots]gtk.Widgetter

	// The header.
	unread                   *gtk.Box
	sender                   *gtk.Label
	disclosure               *gtk.ToggleButton
	via, internal, edited    *gtk.Label
	buttons                  *gtk.Box
	reply, replyAll, forward *gtk.Button
	date                     *gtk.Label
	hovering, focused        bool
	compact                  bool

	// mv is the card's share of a message display: the recipients, the
	// chips and the bars (made when first needed), with the body stack as
	// the place the focus goes when one of them goes away.
	mv          *messageView
	detailsOpen bool
	hint        *gtk.Label
	bars        *gtk.Box

	// The fold of the card (setFold): the arrow in the header, the preview
	// under it while folded, and the parts of the top the fold hid, shown
	// again when it opens.
	foldable, folded bool
	fold             *gtk.Button
	preview          *gtk.Label
	foldHidden       []gtk.Widgetter

	// The "•••" under the body (renderQuoted), made when first offered,
	// and what it offers now.
	quotedBox    *gtk.Box
	quotedButton *gtk.Button
	quotedNow    conversation.QuotedOffer

	// The body: "wait", "text" or "html". The web view is an overlay of
	// host, which is as tall as its size request and nothing else: WebKit
	// asks for the height of whatever it last laid out, never less than the
	// view's own height, so as a child of a box a card could grow but never
	// shrink (a body shown again without its quoted history, or after a
	// taller document in a reused view, kept the taller height). An overlay
	// counts none of its overlays' sizes.
	body *gtk.Stack
	text *gtk.Label
	host *gtk.Overlay

	// The web view while the card is live, the height the HTML body last
	// had (kept while no view is live), and what governs it.
	web       *htmlview.Card
	gov       webHeightGovernor
	webHeight int
	webDoc    string
	live      bool
	isHTML    bool
	html      string
	reload    bool
	links     []api.Link

	// What the body shows, so that a render with the same body leaves it
	// alone.
	rendered    *api.MessageBodyResult
	renderedErr bool
	renderedAny bool
}

// newConvCard builds the card of item.
func newConvCard(cv *conversationView, item conversation.Item, compact bool) *convCard {
	w := cv.w
	c := &convCard{cv: cv, id: item.Message.ID, item: item, s: item.Message, compact: compact, webHeight: convInitialWebHeight}

	c.root = gtk.NewBox(gtk.OrientationVertical, 0)
	c.root.AddCSSClass("conversation-card")
	// The corners clip the body (an HTML body reaches the edges).
	c.root.SetOverflow(gtk.OverflowHidden)

	// The native parts keep the card's padding, from the hairline on.
	c.top = gtk.NewBox(gtk.OrientationVertical, 8)
	c.top.SetMarginStart(convCardPaddingH - 1)
	c.top.SetMarginEnd(convCardPaddingH - 1)
	c.top.SetMarginTop(convCardPaddingV - 1)
	c.top.SetMarginBottom(8)
	c.buildHeader()

	c.bars = gtk.NewBox(gtk.OrientationVertical, 0)

	c.body = gtk.NewStack()
	c.body.SetVhomogeneous(false)
	c.body.SetHhomogeneous(false)
	wait := plainLabel(i18n.T("Loading…"), "caption", "dim-label")
	wait.SetYAlign(0)
	wait.SetVAlign(gtk.AlignStart)
	wait.SetMarginStart(convCardPaddingH - 1)
	waitBox := gtk.NewBox(gtk.OrientationVertical, 0)
	waitBox.SetSizeRequest(-1, convEstimatedBodyHeight)
	waitBox.Append(wait)
	c.body.AddNamed(waitBox, "wait")
	c.text = plainLabel("", "message-body")
	c.text.SetYAlign(0)
	c.text.SetWrap(true)
	c.text.SetWrapMode(pango.WrapWordChar)
	c.text.SetSelectable(true)
	c.text.SetMarginStart(convCardPaddingH - 1)
	c.text.SetMarginEnd(convCardPaddingH - 1)
	c.text.SetMarginTop(2)
	c.text.SetMarginBottom(convCardPaddingV - 1)
	c.body.AddNamed(c.text, "text")
	c.host = gtk.NewOverlay()
	c.host.AddCSSClass("conversation-paper")
	c.host.SetSizeRequest(-1, c.webHeight)
	c.body.AddNamed(c.host, "html")
	c.body.SetVisibleChildName("wait")
	c.forwardScroll()

	// The folded card's preview: the summary's snippet, two lines at most;
	// a click opens the card.
	c.preview = plainLabel("", "dim-label")
	c.preview.SetWrap(true)
	c.preview.SetWrapMode(pango.WrapWordChar)
	c.preview.SetLines(2)
	c.preview.SetEllipsize(pango.EllipsizeEnd)
	c.preview.SetMarginStart(convCardPaddingH - 1)
	c.preview.SetMarginEnd(convCardPaddingH - 1)
	c.preview.SetMarginBottom(convCardPaddingV - 1)
	c.preview.SetVisible(false)
	open := gtk.NewGestureClick()
	open.ConnectReleased(func(int, float64, float64) {
		if c.folded {
			c.cv.setCardFolded(c, false)
		}
	})
	c.preview.AddController(open)

	c.root.Append(c.top)
	c.root.Append(c.preview)
	c.root.Append(c.bars)
	c.root.Append(c.body)

	c.mv = &messageView{win: w, parent: &w.ApplicationWindow.Window, stack: c.body, toast: w.Toast, shown: item.Message}
	c.mv.bulk = func() { w.unsubscribe(c.mv.parent, c.id, w.Toast) }
	c.mv.load = func() { w.loadRemoteImages(c.id) }
	c.mv.trust = func() { w.trustSender(c.id) }
	c.mv.pictures = func() { w.downloadPictures(c.id, w.Toast) }

	c.update(item)
	return c
}

// buildHeader makes the header's line: the sender's group and the badges,
// which go to a second line where they do not fit, then the hover buttons
// and the date.
func (c *convCard) buildHeader() {
	line := gtk.NewBox(gtk.OrientationHorizontal, 8)
	flow := adw.NewWrapBox()
	flow.SetChildSpacing(8)
	flow.SetLineSpacing(4)
	flow.SetHExpand(true)

	group := gtk.NewBox(gtk.OrientationHorizontal, 6)
	// The fold arrow (setFold); hidden on a card that does not fold.
	c.fold = gtk.NewButtonFromIconName("pan-down-symbolic")
	c.fold.AddCSSClass("flat")
	c.fold.AddCSSClass("conversation-card-button")
	c.fold.SetVAlign(gtk.AlignCenter)
	c.fold.SetVisible(false)
	c.fold.ConnectClicked(func() { c.cv.setCardFolded(c, !c.folded) })
	group.Append(c.fold)
	c.unread = gtk.NewBox(gtk.OrientationHorizontal, 0)
	c.unread.AddCSSClass("unread-dot")
	c.unread.SetVAlign(gtk.AlignCenter)
	c.sender = plainLabel("", "conversation-sender")
	c.sender.SetEllipsize(pango.EllipsizeEnd)
	c.sender.SetWidthChars(4)
	c.sender.SetVAlign(gtk.AlignCenter)
	c.disclosure = gtk.NewToggleButton()
	c.disclosure.SetIconName("pan-end-symbolic")
	c.disclosure.AddCSSClass("flat")
	c.disclosure.AddCSSClass("conversation-card-button")
	c.disclosure.SetVAlign(gtk.AlignCenter)
	c.disclosure.ConnectToggled(c.toggleDetails)
	group.Append(c.unread)
	group.Append(c.sender)
	group.Append(c.disclosure)
	flow.Append(group)

	c.via = plainLabel("", "caption", "dim-label")
	c.via.SetEllipsize(pango.EllipsizeEnd)
	c.via.SetVAlign(gtk.AlignCenter)
	c.internal = widget.NewPill()
	c.edited = plainLabel("", "caption", "dim-label")
	c.edited.SetVAlign(gtk.AlignCenter)
	flow.Append(c.via)
	flow.Append(c.internal)
	flow.Append(c.edited)

	// The buttons show while the pointer is over the card or one of them
	// has the focus; hidden, they keep their room (the header's height
	// never changes) and take no clicks, but Tab still reaches them.
	c.buttons = gtk.NewBox(gtk.OrientationHorizontal, 2)
	c.buttons.SetVAlign(gtk.AlignStart)
	mk := func(icon, tooltip string, kind compose.Kind) *gtk.Button {
		b := gtk.NewButtonFromIconName(icon)
		b.AddCSSClass("flat")
		b.AddCSSClass("conversation-card-button")
		b.SetTooltipText(tooltip)
		b.ConnectClicked(func() { c.cv.w.openCompose(kind, c.id) })
		c.buttons.Append(b)
		return b
	}
	c.reply = mk("mail-reply-sender-symbolic", i18n.T("Reply"), compose.KindReply)
	c.replyAll = mk("mail-reply-all-symbolic", i18n.T("Reply All"), compose.KindReplyAll)
	c.forward = mk("mail-forward-symbolic", i18n.T("Forward"), compose.KindForward)
	c.setButtonsShown(false)

	c.date = plainLabel("", "caption", "dim-label")
	c.date.SetVAlign(gtk.AlignStart)
	c.date.SetMarginTop(3)

	line.Append(flow)
	line.Append(c.buttons)
	line.Append(c.date)
	c.install(line, cardSlotHeader)

	motion := gtk.NewEventControllerMotion()
	motion.ConnectEnter(func(float64, float64) {
		c.hovering = true
		c.setButtonsShown(true)
	})
	motion.ConnectLeave(func() {
		c.hovering = false
		c.setButtonsShown(c.focused)
	})
	c.root.AddController(motion)
	focus := gtk.NewEventControllerFocus()
	focus.ConnectEnter(func() {
		c.focused = true
		c.setButtonsShown(true)
	})
	focus.ConnectLeave(func() {
		c.focused = false
		c.setButtonsShown(c.hovering)
	})
	c.buttons.AddController(focus)
}

// setButtonsShown shows or hides the hover buttons.
func (c *convCard) setButtonsShown(on bool) {
	c.buttons.SetOpacity(boolOpacity(on))
	c.buttons.SetCanTarget(on)
}

// install puts w into the top part at slot's place.
func (c *convCard) install(w gtk.Widgetter, slot int) {
	c.slots[slot] = w
	var after gtk.Widgetter
	for i := slot - 1; i >= 0; i-- {
		if c.slots[i] != nil {
			after = c.slots[i]
			break
		}
	}
	if after == nil {
		c.top.Prepend(w)
		return
	}
	c.top.InsertChildAfter(w, after)
}

// widget is the card, to put in a row.
func (c *convCard) widget() gtk.Widgetter { return c.root }

// update shows item: the same member, as the model has it now (flags,
// the issue's badges).
func (c *convCard) update(item conversation.Item) {
	c.item = item
	c.s = item.Message
	c.mv.shown = item.Message
	c.sender.SetText(item.Sender)
	c.sender.SetTooltipText(item.Sender)
	c.sender.SetVisible(item.Sender != "")
	// The disclosure names whose recipients it shows.
	c.disclosure.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(item.Sender)})
	c.unread.SetVisible(item.Unread)
	internal := ""
	if item.Internal {
		internal = item.InternalLabel
	}
	widget.SetInternalPill(c.internal, internal)
	c.via.SetText(item.Via)
	c.via.SetTooltipText(item.Via)
	c.via.SetVisible(item.Via != "")
	c.edited.SetText(item.Edited)
	c.edited.SetVisible(item.Edited != "")
	setConvDate(c.date, item.Message.Date, c.compact)
	c.refreshActions()
	if c.foldable {
		c.showFoldState() // the snippet may have changed
	}
}

// setFold makes the card one that folds (on, conversation.Foldable) to its
// header and a preview of its text, and folds or opens it; off: a card
// without the arrow, open.
func (c *convCard) setFold(on, folded bool) {
	c.foldable = on
	c.fold.SetVisible(on)
	c.setFolded(on && folded)
}

// setFolded folds the card to its header and the preview (the summary's
// snippet), or opens it: the recipients, the chips, the hint, the bars and
// the body come back as they were, and what arrived meanwhile is shown. A
// folded card holds no web view and asks for no body (updateLive).
func (c *convCard) setFolded(folded bool) {
	if folded == c.folded {
		c.showFoldState()
		return
	}
	c.folded = folded
	if folded {
		if c.disclosure.Active() {
			c.disclosure.SetActive(false) // the recipients close with it
		}
		for _, slot := range []int{cardSlotDetails, cardSlotChips, cardSlotHint} {
			if w := c.slots[slot]; w != nil && gtk.BaseWidget(w).Visible() {
				gtk.BaseWidget(w).SetVisible(false)
				c.foldHidden = append(c.foldHidden, w)
			}
		}
		c.setLive(false)
	} else {
		for _, w := range c.foldHidden {
			gtk.BaseWidget(w).SetVisible(true)
		}
		c.foldHidden = nil
	}
	c.showFoldState()
	if !folded {
		c.render(c.cv.ctrl.loaded[c.id])
	}
}

// showFoldState shows the fold's arrow, the preview and the body as the
// card is folded or not.
func (c *convCard) showFoldState() {
	icon, tip := "pan-down-symbolic", i18n.T("Collapse")
	if c.folded {
		icon, tip = "pan-end-symbolic", i18n.T("Expand")
	}
	c.fold.SetIconName(icon)
	c.fold.SetTooltipText(tip)
	snippet := strings.TrimSpace(c.s.Snippet)
	c.preview.SetText(snippet)
	c.preview.SetVisible(c.folded && snippet != "")
	c.disclosure.SetVisible(!c.folded)
	c.bars.SetVisible(!c.folded)
	c.body.SetVisible(!c.folded)
	if c.quotedBox != nil {
		c.quotedBox.SetVisible(!c.folded && c.quotedNow != conversation.QuotedNone)
	}
}

// setCompact shows the short date of a narrow pane, or the full one.
func (c *convCard) setCompact(compact bool) {
	if compact == c.compact {
		return
	}
	c.compact = compact
	setConvDate(c.date, c.s.Date, compact)
}

// refreshActions shows the hover buttons the account allows for this
// member; Reply is Comment on an issue (jira.ReplyLabel).
func (c *convCard) refreshActions() {
	a := c.cv.ctrl.actions(c.s)
	c.reply.SetTooltipText(jira.ReplyLabel(a.Comment, i18n.Tr))
	if a.Comment {
		c.reply.SetIconName("chat-message-new-symbolic")
	} else {
		c.reply.SetIconName("mail-reply-sender-symbolic")
	}
	c.reply.SetVisible(a.Reply)
	c.replyAll.SetVisible(a.ReplyAll)
	c.forward.SetVisible(a.Forward)
}

// toggleDetails opens or closes the recipients: the summary's, then the
// full message's (Cc) once message.get answered.
func (c *convCard) toggleDetails() {
	c.detailsOpen = c.disclosure.Active()
	if c.detailsOpen {
		c.disclosure.SetIconName("pan-down-symbolic")
	} else {
		c.disclosure.SetIconName("pan-end-symbolic")
	}
	if !c.detailsOpen {
		if d := c.slots[cardSlotDetails]; d != nil {
			gtk.BaseWidget(d).SetVisible(false)
		}
		return
	}
	if c.slots[cardSlotDetails] == nil {
		c.makeDetails()
	}
	gtk.BaseWidget(c.slots[cardSlotDetails]).SetVisible(true)
	var m *api.Message
	if lm := c.cv.ctrl.loaded[c.id]; lm != nil {
		m = lm.msg
	} else if lm := c.cv.w.loaded[c.id]; lm != nil {
		m = lm.msg
	}
	c.renderDetails(m)
	c.cv.ctrl.needsBody(c.id, true)
}

// makeDetails builds the From, To and Cc rows as the message pane has them
// (window.blp's message_addresses), for addresses.go to fill.
func (c *convCard) makeDetails() {
	grid := gtk.NewGrid()
	grid.SetColumnSpacing(12)
	grid.SetRowSpacing(6)
	row := func(title string, at int) *addressRow {
		l := gtk.NewLabel(title)
		l.SetXAlign(0)
		l.SetVAlign(gtk.AlignStart)
		l.AddCSSClass("dim-label")
		l.AddCSSClass("address-label")
		box := adw.NewWrapBox()
		box.SetChildSpacing(4)
		box.SetLineSpacing(4)
		box.SetHExpand(true)
		grid.Attach(l, 0, at, 1, 1)
		grid.Attach(box, 1, at, 1, 1)
		return &addressRow{label: l, box: box}
	}
	c.mv.addresses = &addressHeader{v: c.mv, from: row(i18n.T("From"), 0), to: row(i18n.T("To"), 1), cc: row(i18n.T("Cc"), 2)}
	c.install(grid, cardSlotDetails)
}

// renderDetails fills the recipients while they are open: from m, the
// full message, when there is one, else from the summary.
func (c *convCard) renderDetails(m *api.Message) {
	if !c.detailsOpen || c.mv.addresses == nil {
		return
	}
	from, to := c.s.From, c.s.To
	var cc []api.Address
	if m != nil {
		from, to, cc = m.From, m.To, m.CC
	}
	c.mv.addresses.show(c.id, c.s.AccountID, from, to, cc)
}

// render shows whatever lm holds (nil: nothing asked for yet, or let go by
// the pane): the recipients, the body, the bars and the chips. A body
// already shown stays when lm has none. A folded card shows none of it
// (setFolded renders once it opens).
func (c *convCard) render(lm *loadedMessage) {
	if c.folded {
		return
	}
	var m *api.Message
	if lm != nil {
		m = lm.msg
	}
	c.renderDetails(m)
	c.renderBody(lm)
	c.renderBars(lm)
	c.renderChips(lm)
	c.renderQuoted(lm)
}

// renderQuoted shows the "•••" under the body for what lm holds: Show
// Quoted Text when the daemon cut the quoted history, Hide Quoted Text
// while it shows (quotedOffer). An entry let go (nil) keeps the button as
// it was.
func (c *convCard) renderQuoted(lm *loadedMessage) {
	if lm == nil {
		return
	}
	o := quotedOffer(lm)
	if c.quotedBox == nil {
		if o == conversation.QuotedNone {
			return
		}
		c.quotedButton = newQuotedButton()
		c.quotedButton.SetFocusOnClick(false)
		c.quotedButton.ConnectClicked(func() {
			if c.quotedNow != conversation.QuotedNone {
				c.cv.ctrl.setQuoted(c.id, c.quotedNow == conversation.QuotedShow, c.detailsOpen)
			}
		})
		c.quotedBox = gtk.NewBox(gtk.OrientationHorizontal, 0)
		c.quotedBox.SetMarginStart(convCardPaddingH - 1)
		c.quotedBox.SetMarginEnd(convCardPaddingH - 1)
		c.quotedBox.SetMarginTop(6)
		c.quotedBox.SetMarginBottom(convCardPaddingV - 1)
		c.quotedBox.Append(c.quotedButton)
		c.root.Append(c.quotedBox)
	}
	c.quotedNow = o
	if o != conversation.QuotedNone {
		setQuotedLabel(c.quotedButton, o)
	} else if c.quotedButton.HasFocus() {
		c.body.GrabFocus()
	}
	c.quotedBox.SetVisible(!c.folded && o != conversation.QuotedNone)
}

// renderBody shows the body, its error, or the wait; the same body again
// changes nothing (a render must not reload the web view or lay a long
// text out anew).
func (c *convCard) renderBody(lm *loadedMessage) {
	if lm == nil || !lm.bodySettled() {
		if !c.renderedAny {
			c.body.SetVisibleChildName("wait")
		}
		return
	}
	before := c.rendered
	if c.renderedAny && lm.body == c.rendered && (lm.err != nil) == c.renderedErr {
		return
	}
	c.rendered, c.renderedErr, c.renderedAny = lm.body, lm.err != nil, true
	if lm.err != nil {
		c.links = nil
		c.setHint(false)
		c.showText(widget.RPCErrorText(i18n.T("Loading the message"), lm.err))
		return
	}
	b := lm.body
	if showsHTML(b) {
		c.links = b.Links
		c.setHint(false)
		c.showHTML(b.HTML, picturesArrived(before, b))
		return
	}
	c.links = nil
	c.setHint(b != nil && b.HTMLWithheld)
	c.showText(bodyText(b))
}

// picturesArrived reports the body shown again after its pictures kept on
// the mail server were downloaded: the same HTML, whose malachi-cid:
// pictures load now.
func picturesArrived(before, now *api.MessageBodyResult) bool {
	return before != nil && now != nil && before != now && before.MessageID == now.MessageID && before.RemotePictures > 0
}

// showText shows plain text in the body; a web view goes.
func (c *convCard) showText(text string) {
	wasHTML := c.isHTML
	c.releaseWebView()
	c.isHTML = false
	c.html = ""
	c.text.SetText(text)
	c.body.SetVisibleChildName("text")
	if wasHTML {
		c.cv.scheduleLiveUpdate()
	}
}

// showHTML shows the sanitised body in the card's web view while the card
// is live, and its last height on white otherwise.
func (c *convCard) showHTML(body string, reload bool) {
	was := c.isHTML
	c.isHTML = true
	c.html = body
	c.reload = c.reload || reload
	c.host.SetSizeRequest(-1, c.webHeight)
	c.body.SetVisibleChildName("html")
	if c.live {
		c.loadWebView()
	}
	if !was {
		c.cv.scheduleLiveUpdate() // an HTML card wants a view
	}
}

// setHint shows or hides the note that only the plain text is shown.
func (c *convCard) setHint(on bool) {
	if on && c.hint == nil {
		c.hint = plainLabel(i18n.T("The formatted version of this message could not be shown safely; this is its plain text."), "caption", "dim-label")
		c.hint.SetWrap(true)
		c.install(c.hint, cardSlotHint)
	}
	if c.hint != nil {
		c.hint.SetVisible(on)
	}
}

// renderBars redraws the remote-image and pictures bars of an HTML body
// and leaves the body alone; the bars are made when first shown.
func (c *convCard) renderBars(lm *loadedMessage) {
	c.renderBulk(lm)
	if !c.isHTML || lm == nil {
		if c.mv.bar != nil {
			c.mv.setBarVisible(false)
		}
		c.mv.showPicturesBar(picturesBarState{})
		return
	}
	if st := remoteBarStateFor(lm); st.visible || c.mv.bar != nil {
		c.makeRemoteBar()
		c.mv.showRemoteBar(st)
	}
	if st := picturesBarStateFor(lm); st.visible || c.mv.picturesBar != nil {
		c.makePicturesBar()
		c.mv.showPicturesBar(st)
	}
}

// makeRemoteBar builds the remote-image bar as window.blp's remote_bar,
// once.
func (c *convCard) makeRemoteBar() {
	if c.mv.bar != nil {
		return
	}
	v := c.mv
	v.bar, v.barLabel, v.barSpinner = convBar()
	v.loadButton = gtk.NewButtonWithMnemonic(i18n.T("Load _Images"))
	v.trustButton = gtk.NewButtonWithMnemonic(i18n.T("Always From This _Sender"))
	v.trustButton.SetTooltipText(i18n.T("Load remote images now and whenever this sender writes"))
	v.loadButton.ConnectClicked(func() { v.load() })
	v.trustButton.ConnectClicked(func() { v.trust() })
	// As in the pane: the bar goes when the images are in; a click leaves
	// the focus where it was (newMessageView).
	v.loadButton.SetFocusOnClick(false)
	v.trustButton.SetFocusOnClick(false)
	v.bar.Append(v.loadButton)
	v.bar.Append(v.trustButton)
	c.bars.Prepend(v.bar)
}

// makePicturesBar builds the bar of the pictures kept on the mail server
// only, as window.blp's pictures_bar, once.
func (c *convCard) makePicturesBar() {
	if c.mv.picturesBar != nil {
		return
	}
	v := c.mv
	v.picturesBar, v.picturesLabel, v.picturesSpinner = convBar()
	v.picturesDownload = gtk.NewButtonWithMnemonic(i18n.T("_Download Pictures"))
	v.picturesDownload.ConnectClicked(func() { v.pictures() })
	v.picturesDownload.SetFocusOnClick(false)
	v.picturesBar.Append(v.picturesDownload)
	c.bars.Append(v.picturesBar)
}

// convBar is a bar of a card: hidden, the spinner, the label.
func convBar() (*gtk.Box, *gtk.Label, *adw.Spinner) {
	bar := gtk.NewBox(gtk.OrientationHorizontal, 12)
	bar.AddCSSClass("toolbar")
	bar.AddCSSClass("remote-bar")
	bar.SetVisible(false)
	spinner := adw.NewSpinner()
	spinner.SetVisible(false)
	spinner.SetVAlign(gtk.AlignCenter)
	label := plainLabel("")
	label.SetWrap(true)
	label.SetHExpand(true)
	bar.Append(spinner)
	bar.Append(label)
	return bar, label, spinner
}

// renderChips rebuilds the attachment chips for what lm holds (the
// pane's renderAttachments); nothing until message.get answered.
func (c *convCard) renderChips(lm *loadedMessage) {
	if c.mv.attachments == nil {
		if lm == nil || lm.msg == nil || len(chipAttachments(lm.msg.Attachments, lm.body)) == 0 {
			return
		}
		box := adw.NewWrapBox()
		box.SetChildSpacing(6)
		box.SetLineSpacing(6)
		box.SetVisible(false)
		c.mv.attachments = box
		c.install(box, cardSlotChips)
	}
	c.mv.shown, c.mv.shownLoaded = c.s, lm
	c.mv.renderAttachments(c.s, lm)
}

// refreshChips redraws the chips (a download started or ended, Save All
// runs): from lm, or from the entry they last showed.
func (c *convCard) refreshChips(lm *loadedMessage) {
	if c.folded {
		return // shown once the card opens (setFolded renders)
	}
	if c.mv.attachments == nil && lm == nil {
		return
	}
	if lm == nil {
		lm = c.mv.shownLoaded
	}
	c.renderChips(lm)
}

// setLive gives the card a web view (the pane's live window,
// convMaxLiveWebViews at most) or takes it; the body keeps its last
// height meanwhile.
func (c *convCard) setLive(on bool) {
	if on == c.live {
		return
	}
	c.live = on
	if !on {
		c.releaseWebView()
		return
	}
	if c.isHTML {
		c.loadWebView()
	}
}

// loadWebView takes a view from the pane's pool (once while live) and
// loads the body.
func (c *convCard) loadWebView() {
	w := c.cv.w
	if c.web == nil {
		zoom := w.settings.TextZoom()
		c.web = c.cv.takeWebView(zoom)
		c.gov = newWebHeightGovernor(float64(zoomPercent(zoom)) / 100)
		c.web.OnLink = func(uri string) { w.openLink(&w.ApplicationWindow.Window, uri, c.links) }
		c.web.OnHover = c.cv.hover
		c.web.OnSize = c.sizeReported
		c.web.SetHeight(c.webHeight)
		c.host.AddOverlay(c.web.Widget())
		c.webDoc = ""
	}
	if c.webDoc != c.html || c.reload {
		c.gov.reset()
	}
	c.web.Load(c.html, c.reload)
	c.webDoc = c.html
	c.reload = false
}

// releaseWebView hands the view back to the pane's pool; the body keeps
// its height.
func (c *convCard) releaseWebView() {
	if c.web == nil {
		return
	}
	web := c.web
	c.web = nil
	c.host.RemoveOverlay(web.Widget())
	c.cv.returnWebView(web)
	c.cv.hover("")
}

// sizeReported is the document's height from the card's script: the
// governor decides the view's height (capped, frozen for a document that
// grows with the view), which the body keeps when the view goes.
func (c *convCard) sizeReported(css float64, viewport, widthChanged bool) {
	if c.web == nil {
		return
	}
	if widthChanged {
		c.gov.widthChanged()
	}
	if h, ok := c.gov.report(css, viewport); ok {
		c.setWebHeight(int(h))
	}
}

// setWebHeight gives the HTML body its height.
func (c *convCard) setWebHeight(h int) {
	if h <= 0 || h == c.webHeight {
		return
	}
	c.webHeight = h
	c.host.SetSizeRequest(-1, h)
	if c.web != nil {
		c.web.SetHeight(h)
	}
}

// setZoom scales the web view's document and its height at once.
func (c *convCard) setZoom(percent int) {
	if c.web == nil {
		return
	}
	c.web.SetZoom(percent)
	if h, ok := c.gov.setZoom(float64(zoomPercent(percent)) / 100); ok {
		c.setWebHeight(int(h))
	}
}

// zoomPercent is the text-zoom setting as the views take it: 100 for
// nothing sensible.
func zoomPercent(p int) int {
	if p <= 0 {
		return 100
	}
	return p
}

// forwardScroll hands the scroll wheel over the web view of a document
// that fits (nothing to scroll inside) on to the conversation, before
// WebKit takes it: otherwise the view would swallow what the pane should
// scroll.
func (c *convCard) forwardScroll() {
	sc := gtk.NewEventControllerScroll(gtk.EventControllerScrollVertical | gtk.EventControllerScrollKinetic)
	sc.SetPropagationPhase(gtk.PhaseCapture)
	sc.ConnectScroll(func(_, dy float64) bool {
		if c.web == nil || !c.gov.fits() {
			return false
		}
		c.cv.scrollBy(dy, sc.Unit())
		return true
	})
	sc.ConnectDecelerate(func(_, vy float64) {
		if c.web == nil || !c.gov.fits() {
			return
		}
		if sc.Unit() == gdk.ScrollUnitWheel {
			vy *= c.cv.wheelStep()
		}
		c.cv.kinetic(vy)
	})
	c.host.AddController(sc)
}
