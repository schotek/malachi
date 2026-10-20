// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"math"
	"strconv"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/htmlview"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The rows of the chat layout (conversation_chat.go,
// docs/plans/chat-view.md D1, D3–D6, D14, D15): a bubble across the column
// (chatRow: the gap above it, the sender's avatar on the bubble's outer
// side, the bubble at that edge, the rest of the row the empty side) and a
// centred row without a bubble (chatCenterRow: an issue's status or
// assignee change, or the row of older messages left out). A port of the
// macOS client's ConversationChatRow.swift (ConversationChatRow,
// ConversationChatCenterRow) and ConversationBubbleView.swift
// (ConversationBubbleView, ConversationClipPill). No new text: the badges
// and sentences are those of package conversation, the timeline's cards
// (bodyText) and "Has attachments" of the board (board.SpokenAttachments).
//
// How a bubble hugs its text: the bubble is the child of an Adw.Clamp in
// pixels (setMax: convChatBubbleMax of the column) that does not expand in
// the row's horizontal box, beside an expanding spacer. A clamp's natural
// width is its child's natural width up to the maximum, and the box gives
// a child that does not expand its natural width when there is room, so a
// short message is as wide as its text, never narrower than its header
// (the sender and the date, whose natural widths count), and a long one
// stops at the maximum and wraps inside it (height for width); in a narrow
// column the header's sender is ellipsised first. The body label's natural
// width is its text unwrapped (natural-wrap-mode none), so that the clamp,
// not the label, decides where it wraps. The empty side, at least
// convChatGap, is what the maximum leaves. No constraint layout and no
// max-width-chars (which counts characters, not pixels).
//
// Every text is plain (SetUseMarkup(false)) and none can be selected, so
// that a double click anywhere in a bubble opens the message (chatRow's
// gesture, conversation_chat_layout.go chatActivate). The bubble is a
// Gtk.Frame, whose accessible role is a group, named by its sender and
// date as a card is. Its look is convCSS: .chat-bubble is a card on the
// pane's grey, .chat-bubble-mine the user's own tinted with the accent
// colour and bordered with it, .chat-placeholder the dimmed snippet. The
// body is a .message-body label: the text zoom scales it as it scales the
// timeline's plain-text bodies (internal/style), and the monospace setting
// reaches it too, except where .chat-proportional says the text is no
// plain-text body (the text the daemon derived from HTML, a placeholder,
// a sentence for the state of the body: bubbleMonospace).
//
// With the preference conversation-chat-html (bubbleHTML), a bubble near
// the viewport whose body has HTML shows that sanitised HTML instead of
// its text, in the locked web view of the timeline's cards (htmlview.Card
// from the timeline's pool, sized to its document by webHeightGovernor,
// capped at 4000 px; the same CompactDocument, CSP and malachi-cid:
// parts, links through the "Open this link?" question): the view stands
// where the text label stands, inside the bubble's background with its
// padding, transparent on the bubble's own background with every
// background of the document neutralised and, in the dark style, the
// theme's text and link colours (htmlview.ChatDocument, chatStyle), and the
// bubble takes its maximum width (chatRow.setFill: the clamp expands, the
// spacer keeps only the empty side). No bars and no fold in a bubble (the
// message's window has them). The chat gives at most convMaxLiveWebViews
// bubbles a view (chatLive); a bubble that loses it (far from the
// viewport, beyond the cap, the preference off, the chat hidden) shows its
// text again. WebKit takes the clicks inside the document, so in an HTML
// bubble a double click on the header (the sender, the date) opens the
// message; one inside the document is the document's (a word selected).

// convChatNoCap is a clamp's maximum before the column has a width.
const convChatNoCap = 100000

// chatRow is a bubble of the chat across the column: a message's or a
// draft's.
type chatRow struct {
	*gtk.Box
	// right: the bubble stands on the right (the user's own).
	right bool
	// row is the chat's row shown (conversation.ChatRows), for the double
	// click.
	row    conversation.ChatRow
	avatar *adw.Avatar
	clamp  *adw.Clamp
	bubble *chatBubble
	max    int
	// spacer is the row's empty side; fill: the bubble takes its maximum
	// (an HTML body), the spacer only gap pixels.
	spacer *gtk.Box
	fill   bool
	gap    int
}

// newChatRow is a row with its bubble on the right (right) or the left;
// activate runs on a double click anywhere in the bubble.
func newChatRow(right bool, activate func(*chatRow)) *chatRow {
	r := &chatRow{Box: gtk.NewBox(gtk.OrientationHorizontal, 0), right: right}
	r.SetMarginTop(convItemGap)
	r.SetVExpand(false)

	r.avatar = adw.NewAvatar(convAvatar, "", true)
	r.avatar.SetVAlign(gtk.AlignStart)
	if right {
		// The user's own, tinted with the accent colour over the list's
		// monochrome setting (convCSS).
		r.avatar.AddCSSClass("conversation-own")
		r.avatar.SetMarginStart(convGutterGap)
	} else {
		r.avatar.SetMarginEnd(convGutterGap)
	}

	r.bubble = newChatBubble(right)
	r.clamp = adw.NewClamp()
	r.clamp.SetUnit(adw.LengthUnitPx)
	r.clamp.SetMaximumSize(convChatNoCap)
	r.clamp.SetTighteningThreshold(convChatNoCap)
	r.clamp.SetHExpand(false)
	r.clamp.SetVAlign(gtk.AlignStart)
	r.clamp.SetChild(r.bubble.frame)

	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	r.spacer = spacer
	if right {
		r.Append(spacer)
		r.Append(r.clamp)
		r.Append(r.avatar)
	} else {
		r.Append(r.avatar)
		r.Append(r.clamp)
		r.Append(spacer)
	}
	r.bubble.fill = r.setFill

	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_PRIMARY)
	click.ConnectReleased(func(n int, x, y float64) {
		// In an HTML bubble only the header opens the message: the
		// document is WebKit's.
		if n == 2 && !r.bubble.inDocument(x, y) {
			activate(r)
		}
	})
	r.bubble.frame.AddController(click)
	return r
}

// setGap is the row's empty side in pixels while the bubble fills its
// maximum (the column less the avatar's gutter and the bubble's maximum).
func (r *chatRow) setGap(gap int) {
	if gap < 0 {
		gap = 0
	}
	if gap == r.gap {
		return
	}
	r.gap = gap
	if r.fill {
		r.spacer.SetSizeRequest(gap, -1)
	}
}

// setFill has the bubble take its maximum width (on: an HTML body, whose
// view has no natural width of its own) or hug its text: the clamp
// expands and the spacer keeps the empty side alone, or the spacer
// expands.
func (r *chatRow) setFill(on bool) {
	if on == r.fill {
		return
	}
	r.fill = on
	r.clamp.SetHExpand(on)
	r.spacer.SetHExpand(!on)
	if on {
		r.spacer.SetSizeRequest(r.gap, -1)
	} else {
		r.spacer.SetSizeRequest(-1, -1)
	}
}

// setMax caps the bubble at max pixels (convChatBubbleMax of the column);
// 0 or less: the column has no width yet, the cap stays as it was.
func (r *chatRow) setMax(max int) {
	if max <= 0 || max == r.max {
		return
	}
	r.max = max
	r.clamp.SetMaximumSize(max)
	r.clamp.SetTighteningThreshold(max)
}

// showMessage shows item (the member as the model has it now) with its
// attachments pill; the avatar stands for the sender (hostile text, only
// ever drawn as initials).
func (r *chatRow) showMessage(item conversation.Item, att bubbleAttachments, compact bool) {
	r.avatar.SetText(item.Sender)
	r.bubble.showMessage(item, att, compact)
}

// showDraft shows the user's draft card.
func (r *chatRow) showDraft(card conversation.DraftCard) {
	r.avatar.SetText(card.Sender)
	r.bubble.showDraft(card)
}

// chatBubble is the bubble of a message or a draft: a header (the unread
// dot of someone else's message, the sender, the paperclip pill, the Draft
// and Internal badges, who relayed a comment and Edited, the date or when
// a draft was saved) over the text.
type chatBubble struct {
	frame *gtk.Frame
	mine  bool

	unread    *gtk.Box
	sender    *gtk.Label
	clip      *gtk.Box
	clipCount *gtk.Label
	draftPill *gtk.Label
	internal  *gtk.Label
	via       *gtk.Label
	edited    *gtk.Label
	date      *gtk.Label
	text      *gtk.Label
	// html is the host of the web view of an HTML body (bubbleHTML): an
	// overlay as tall as its size request, as the card's host.
	html *gtk.Overlay

	// What the header names and the date it shows (compact: the list's
	// short form); saved is a draft's "Draft saved …" in the date's place.
	senderName string
	when       time.Time
	saved      string
	draft      bool
	compact    bool

	// showsText: the text shows a body (not the placeholder), which a
	// let-go entry does not take away (showBody); shown* is what the label
	// shows, so that the same text again lays nothing out anew.
	showsText        bool
	shownAny         bool
	shownText        string
	shownPlaceholder bool

	// The HTML body (setHTML): chat lends the views (nil: none ever; Mail's
	// conversationChat or the board's boardChat),
	// fill widens the row; doc is the body whose HTML the bubble shows
	// while live (nil: text), web the view while it shows, gov its
	// height's governor, webHeight the height last reported.
	chat      chatWebHost
	fill      func(bool)
	doc       *api.MessageBodyResult
	reload    bool
	live      bool
	web       *htmlview.Card
	gov       webHeightGovernor
	webHeight int
	webDoc    string
	// reported: the document reported its height (webHeight is real),
	// which the text keeps as its minimum height once the view goes
	// (bubbleFrozenHeight).
	reported bool
}

// newChatBubble is an empty bubble, the user's own (mine) or not.
func newChatBubble(mine bool) *chatBubble {
	b := &chatBubble{frame: gtk.NewFrame(""), mine: mine}
	b.frame.AddCSSClass("chat-bubble")
	if mine {
		b.frame.AddCSSClass("chat-bubble-mine")
	}

	header := gtk.NewBox(gtk.OrientationHorizontal, 6)
	b.unread = gtk.NewBox(gtk.OrientationHorizontal, 0)
	b.unread.AddCSSClass("unread-dot")
	b.unread.SetVAlign(gtk.AlignCenter)
	b.unread.SetVisible(false)
	b.sender = plainLabel("", "conversation-sender")
	b.sender.SetEllipsize(pango.EllipsizeEnd)
	b.sender.SetWidthChars(4)
	b.sender.SetVAlign(gtk.AlignCenter)

	// The paperclip: a pill, so that it reads at a glance on someone
	// else's bubble and on the user's tinted one; the number once
	// message.get told it.
	b.clip = gtk.NewBox(gtk.OrientationHorizontal, 2)
	b.clip.AddCSSClass("chat-clip-pill")
	b.clip.SetVAlign(gtk.AlignCenter)
	// The image carries the name ("Has attachments", its tooltip too): a
	// box's role is generic, which screen readers pass over, and cannot
	// change after construction; the number is a visible label of its own.
	clipIcon := gtk.NewImageFromIconName("mail-attachment-symbolic")
	spoken := board.SpokenAttachments(i18n.Tr)
	clipIcon.SetTooltipText(spoken)
	clipIcon.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(spoken)})
	b.clip.Append(clipIcon)
	b.clipCount = plainLabel("")
	b.clipCount.SetVisible(false)
	b.clip.Append(b.clipCount)
	b.clip.SetVisible(false)

	b.draftPill = widget.NewPill()
	b.draftPill.AddCSSClass("draft-pill")
	b.draftPill.SetText(conversation.DraftBadge(i18n.Tr))
	b.internal = widget.NewPill()
	b.via = plainLabel("", "caption", "dim-label")
	b.via.SetEllipsize(pango.EllipsizeEnd)
	b.via.SetVAlign(gtk.AlignCenter)
	b.via.SetVisible(false)
	b.edited = plainLabel("", "caption", "dim-label")
	b.edited.SetVAlign(gtk.AlignCenter)
	b.edited.SetVisible(false)
	spacer := gtk.NewBox(gtk.OrientationHorizontal, 0)
	spacer.SetHExpand(true)
	b.date = plainLabel("", "caption", "dim-label")
	b.date.SetVAlign(gtk.AlignCenter)
	b.date.SetMarginStart(6)

	for _, w := range []gtk.Widgetter{b.unread, b.sender, b.clip, b.draftPill, b.internal, b.via, b.edited, spacer, b.date} {
		header.Append(w)
	}

	b.text = plainLabel("", "message-body")
	b.text.SetWrap(true)
	b.text.SetWrapMode(pango.WrapWordChar)
	// The natural width is the text unwrapped (its longest line), which
	// the clamp caps: a short message hugs its text, a long one fills the
	// maximum and wraps.
	b.text.SetNaturalWrapMode(gtk.NaturalWrapNone)
	b.text.SetSelectable(false)
	b.text.SetYAlign(0)
	b.text.SetVisible(false)

	b.html = gtk.NewOverlay()
	b.html.AddCSSClass("chat-bubble-html")
	b.html.SetOverflow(gtk.OverflowHidden)
	b.html.SetVisible(false)
	b.webHeight = convInitialWebHeight
	b.forwardScroll()

	column := gtk.NewBox(gtk.OrientationVertical, 6)
	column.Append(header)
	column.Append(b.text)
	column.Append(b.html)
	b.frame.SetChild(column)
	return b
}

// chatWebHost is what a chat layout lends its bubbles' web views: Mail's
// conversationChat (the timeline's pool, its scroller) and the board's
// boardChat (views of their own as the board's cards, the detail's
// scroller).
type chatWebHost interface {
	// chatTakeWeb is a view at the text-zoom percent zoom, chatGiveWeb takes
	// one back (its widget already out of the bubble).
	chatTakeWeb(zoom int) *htmlview.Card
	chatGiveWeb(web *htmlview.Card)
	// chatZoom is the text-zoom setting.
	chatZoom() int
	// chatLink opens a link the user activated (the "Open this link?"
	// question of the cards); chatHover shows the link under the pointer.
	chatLink(uri string, links []api.Link)
	chatHover(uri string)
	// chatScrollBy scrolls the layout by what a document that fits handed
	// on.
	chatScrollBy(dy float64, unit gdk.ScrollUnit)
	// chatHeight runs change, a change of the height of the bubble frame,
	// keeping the reader's place as the layout keeps it.
	chatHeight(frame *gtk.Frame, change func())
}

// showsHTML reports whether the bubble shows its body in a web view now.
func (b *chatBubble) showsHTML() bool { return b.web != nil }

// wantsHTML reports whether the bubble has an HTML body to show while it
// is live (chatLive counts it among the HTML bubbles).
func (b *chatBubble) wantsHTML() bool { return b.doc != nil }

// setHTML is the body whose HTML the bubble shows while live (nil: its
// text); the same body again changes nothing, the same message's body
// with its pictures now downloaded reloads the document.
func (b *chatBubble) setHTML(doc *api.MessageBodyResult) {
	if doc == b.doc {
		return
	}
	b.reload = b.reload || picturesArrived(b.doc, doc)
	b.doc = doc
	if doc == nil {
		// No HTML any more (the preference off, a body that cannot render
		// it): the frozen height goes.
		b.reported = false
		b.webHeight = convInitialWebHeight
	}
	b.sync()
}

// setLive gives the bubble a web view (on, chatLive) or takes it.
func (b *chatBubble) setLive(on bool) {
	if on == b.live {
		return
	}
	b.live = on
	b.sync()
}

// sync shows the HTML body in a web view while the bubble is live and has
// one, else the text; a view let go goes back to the timeline's pool.
func (b *chatBubble) sync() {
	if b.live && b.doc != nil && b.chat != nil {
		b.loadWeb()
	} else {
		b.releaseWeb()
	}
	b.applyFrozen()
}

// applyFrozen keeps the text of an HTML bubble whose view went (far, or
// beyond the cap) as tall as the document last was, as a released card
// keeps its paper's height: the rows keep their allocations, so that the
// live window does not flip the bubble between the view and the text.
func (b *chatBubble) applyFrozen() {
	if h := bubbleFrozenHeight(b.doc != nil, b.web != nil, b.reported, b.webHeight); h > 0 {
		b.text.SetSizeRequest(-1, h)
	} else {
		b.text.SetSizeRequest(-1, -1)
	}
}

// loadWeb takes a view (once while it shows) and loads the document.
func (b *chatBubble) loadWeb() {
	host := b.chat
	if b.web == nil {
		zoom := host.chatZoom()
		b.web = host.chatTakeWeb(zoom)
		b.gov = newWebHeightGovernor(float64(zoomPercent(zoom)) / 100)
		b.web.OnLink = func(uri string) {
			links := []api.Link(nil)
			if b.doc != nil {
				links = b.doc.Links
			}
			host.chatLink(uri, links)
		}
		b.web.OnHover = host.chatHover
		b.web.OnSize = b.sizeReported
		b.web.SetHeight(b.webHeight)
		b.html.AddOverlay(b.web.Widget())
		b.webDoc = ""
		b.html.SetSizeRequest(-1, b.webHeight)
		b.html.SetVisible(true)
		b.text.SetVisible(false)
		if b.fill != nil {
			b.fill(true)
		}
	}
	if b.webDoc != b.doc.HTML || b.reload {
		b.gov.reset()
	}
	// Transparent and the chat's sheet before the document loads.
	style := b.chatStyle()
	b.web.SetChat(&style)
	b.web.Load(b.doc.HTML, b.reload)
	b.webDoc = b.doc.HTML
	b.reload = false
}

// chatStyle is how the bubble's document is drawn now: the dark style's
// text in the bubble's own text colour, its links in the accent's
// standalone colour, as libadwaita draws links.
func (b *chatBubble) chatStyle() htmlview.ChatStyle {
	sm := adw.StyleManagerGetDefault()
	s := htmlview.ChatStyle{Dark: sm.Dark()}
	if !s.Dark {
		return s
	}
	s.Text = htmlColour(b.text.Color())
	s.Link = htmlColour(adw.AccentColorToStandaloneRGBA(sm.AccentColor(), true))
	return s
}

// htmlColour is c without its alpha (nil: white).
func htmlColour(c *gdk.RGBA) htmlview.Colour {
	if c == nil {
		return htmlview.Colour{R: 255, G: 255, B: 255}
	}
	ch := func(v float32) uint8 {
		return uint8(math.Round(math.Min(math.Max(float64(v), 0), 1) * 255))
	}
	return htmlview.Colour{R: ch(c.Red()), G: ch(c.Green()), B: ch(c.Blue())}
}

// releaseWeb hands the view back to the timeline's pool and shows the
// text again; the next view starts from the height last reported.
func (b *chatBubble) releaseWeb() {
	if b.web == nil {
		return
	}
	web := b.web
	b.web = nil
	b.html.RemoveOverlay(web.Widget())
	b.html.SetVisible(false)
	b.text.SetVisible(b.shownAny && b.shownText != "")
	if b.fill != nil {
		b.fill(false)
	}
	b.chat.chatGiveWeb(web)
	b.chat.chatHover("")
}

// sizeReported is the document's height from the view's script, as the
// card's (convCard.sizeReported).
func (b *chatBubble) sizeReported(css float64, viewport, widthChanged bool) {
	if b.web == nil {
		return
	}
	if widthChanged {
		b.gov.widthChanged()
	}
	if h, ok := b.gov.report(css, viewport); ok {
		b.setWebHeight(int(h))
	}
}

// setWebHeight gives the view its height; the chat's adjustment follows
// ("changed": the end stays in view, or the row read keeps its place).
func (b *chatBubble) setWebHeight(h int) {
	if h <= 0 {
		return
	}
	b.reported = true
	if h == b.webHeight {
		return
	}
	change := func() {
		b.webHeight = h
		b.html.SetSizeRequest(-1, h)
		if b.web != nil {
			b.web.SetHeight(h)
		}
	}
	if b.chat == nil {
		change()
		return
	}
	b.chat.chatHeight(b.frame, change)
}

// setZoom scales the document and its height at once (the text zoom).
func (b *chatBubble) setZoom(percent int) {
	if b.web == nil {
		return
	}
	b.web.SetZoom(percent)
	if h, ok := b.gov.setZoom(float64(zoomPercent(percent)) / 100); ok {
		b.setWebHeight(int(h))
	}
}

// inDocument reports whether (x, y) in the bubble's frame is on the web
// view of an HTML body (below the header).
func (b *chatBubble) inDocument(x, y float64) bool {
	if b.web == nil || !b.html.IsVisible() {
		return false
	}
	r, ok := b.html.ComputeBounds(b.frame)
	if !ok {
		return false
	}
	return y >= float64(r.Y()) && y < float64(r.Y()+r.Height()) && x >= float64(r.X()) && x < float64(r.X()+r.Width())
}

// forwardScroll hands the wheel over the view of a document that fits on
// to the chat, before WebKit takes it (the card's forwardScroll, without
// the glide of a flick).
func (b *chatBubble) forwardScroll() {
	sc := gtk.NewEventControllerScroll(gtk.EventControllerScrollVertical)
	sc.SetPropagationPhase(gtk.PhaseCapture)
	sc.ConnectScroll(func(_, dy float64) bool {
		if b.web == nil || b.chat == nil || !b.gov.fits() {
			return false
		}
		b.chat.chatScrollBy(dy, sc.Unit())
		return true
	})
	b.html.AddController(sc)
}

// showMessage shows the header of a message (item, as the model has it
// now) and its attachments pill.
func (b *chatBubble) showMessage(item conversation.Item, att bubbleAttachments, compact bool) {
	b.draft = false
	b.senderName = item.Sender
	b.sender.SetText(item.Sender)
	b.sender.SetTooltipText(item.Sender)
	b.sender.SetVisible(item.Sender != "")
	b.unread.SetVisible(item.Unread && !b.mine)
	b.showClip(att)
	b.draftPill.SetVisible(false)
	internal := ""
	if item.Internal {
		internal = item.InternalLabel
	}
	widget.SetInternalPill(b.internal, internal)
	b.via.SetText(item.Via)
	b.via.SetTooltipText(item.Via)
	b.via.SetVisible(item.Via != "")
	b.edited.SetText(item.Edited)
	b.edited.SetVisible(item.Edited != "")
	b.when = item.Message.Date
	b.saved = ""
	b.compact = compact
	b.showDate()
}

// showDraft shows the user's draft (D14): the header with the Draft badge,
// the Internal one of an internal comment and when it was saved, and its
// text, which follows the monospace setting as the timeline's draft card
// does.
func (b *chatBubble) showDraft(card conversation.DraftCard) {
	b.draft = true
	b.senderName = card.Sender
	b.sender.SetText(card.Sender)
	b.sender.SetTooltipText(card.Sender)
	b.sender.SetVisible(card.Sender != "")
	b.unread.SetVisible(false)
	b.showClip(bubbleAttachments{})
	b.draftPill.SetVisible(true)
	widget.SetInternalPill(b.internal, card.InternalLabel)
	b.internal.SetVisible(card.Internal)
	b.via.SetVisible(false)
	b.edited.SetVisible(false)
	b.when = time.Time{}
	b.saved = ""
	if !card.Draft.UpdatedAt.IsZero() {
		b.saved = conversation.DraftSaved(widget.FormatTime(card.Draft.UpdatedAt), i18n.Tr)
	}
	b.showDate()
	b.showBody(card.Text, false, true)
}

// showClip shows the paperclip pill: hidden, alone, or with the number.
func (b *chatBubble) showClip(att bubbleAttachments) {
	if !att.Shown {
		b.clip.SetVisible(false)
		return
	}
	if att.Count > 0 {
		n := strconv.Itoa(att.Count)
		b.clipCount.SetText(n)
		b.clipCount.SetVisible(true)
	} else {
		b.clipCount.SetVisible(false)
	}
	b.clip.SetVisible(true)
}

// setCompact shows the short date of a narrow column, or the full one.
func (b *chatBubble) setCompact(compact bool) {
	if compact == b.compact {
		return
	}
	b.compact = compact
	b.showDate()
}

// showDate shows the date (or a draft's saved time) at the header's end,
// and names the bubble for accessibility: the sender and the full date
// (a draft: the sender, the Draft badge and when it was saved).
func (b *chatBubble) showDate() {
	if b.draft {
		b.date.SetText(b.saved)
		b.date.SetTooltipText("")
		b.date.SetVisible(b.saved != "")
		b.setName(chatAccessibleName(b.senderName, conversation.DraftBadge(i18n.Tr), b.saved))
		return
	}
	full := ""
	short := ""
	if !b.when.IsZero() {
		full = widget.FormatDateTime(b.when)
		short = widget.FormatDate(b.when, time.Now())
	}
	text, tooltip := chatDateText(full, short, b.compact)
	b.date.SetText(text)
	b.date.SetTooltipText(tooltip)
	b.date.SetVisible(text != "")
	b.setName(chatAccessibleName(b.senderName, full))
}

func (b *chatBubble) setName(name string) {
	b.frame.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(name)})
}

// showBody shows the text under the header: the placeholder (dimmed) or
// the text; an empty one hides the label. A placeholder does not replace a
// text shown already (the controller let go of the body, or fetches it
// again: the bubble keeps what it read). mono: the text is a plain-text
// body, which the monospace setting may reach (bubbleMonospace); never a
// placeholder.
func (b *chatBubble) showBody(text string, placeholder, mono bool) {
	if placeholder && b.showsText {
		return
	}
	if placeholder {
		mono = false
	}
	if mono {
		b.text.RemoveCSSClass("chat-proportional")
	} else {
		b.text.AddCSSClass("chat-proportional")
	}
	if b.shownAny && text == b.shownText && placeholder == b.shownPlaceholder {
		return
	}
	b.shownAny, b.shownText, b.shownPlaceholder = true, text, placeholder
	b.showsText = !placeholder
	b.text.SetText(text)
	if placeholder {
		b.text.AddCSSClass("chat-placeholder")
	} else {
		b.text.RemoveCSSClass("chat-placeholder")
	}
	b.text.SetVisible(text != "" && b.web == nil)
}

// chatCenterRow is a row of the chat without a bubble (D1): an issue's
// status or assignee change, or the row of older messages left out,
// centred in the column no wider than a bubble, in small secondary text:
// the lines of the change and under them who made it and when (the list's
// short date in a narrow column, the full one in the tooltip), as the
// timeline's convEventRow composes them; the row of older messages its
// sentence. The centred variant of convEventRow and convTruncatedRow.
type chatCenterRow struct {
	*adw.Clamp
	lines *gtk.Label
	meta  *gtk.Label
	// item is the event shown; event false for the row of older messages.
	item    conversation.Item
	event   bool
	compact bool
	max     int
}

func newChatCenterRow() *chatCenterRow {
	e := &chatCenterRow{Clamp: adw.NewClamp()}
	e.SetUnit(adw.LengthUnitPx)
	e.SetMaximumSize(convChatNoCap)
	e.SetTighteningThreshold(convChatNoCap)
	e.SetMarginTop(convItemGap)
	box := gtk.NewBox(gtk.OrientationVertical, 2)
	e.lines = plainLabel("", "caption", "dim-label")
	e.lines.SetWrap(true)
	e.lines.SetWrapMode(pango.WrapWordChar)
	e.lines.SetJustify(gtk.JustifyCenter)
	e.lines.SetXAlign(0.5)
	e.meta = plainLabel("", "caption", "dim-label")
	e.meta.SetEllipsize(pango.EllipsizeMiddle)
	e.meta.SetXAlign(0.5)
	e.meta.SetVisible(false)
	box.Append(e.lines)
	box.Append(e.meta)
	e.SetChild(box)
	return e
}

// setMax keeps the row's text no wider than a bubble (as chatRow.setMax).
func (e *chatCenterRow) setMax(max int) {
	if max <= 0 || max == e.max {
		return
	}
	e.max = max
	e.SetMaximumSize(max)
	e.SetTighteningThreshold(max)
}

// showEvent shows an issue event (item, as the model has it now).
func (e *chatCenterRow) showEvent(item conversation.Item, compact bool) {
	e.item = item
	e.event = true
	e.compact = compact
	text := ""
	for i, l := range item.EventLines {
		if i > 0 {
			text += "\n"
		}
		text += l
	}
	e.lines.SetText(text)
	e.showMeta()
}

// showMeta shows who made the change and when.
func (e *chatCenterRow) showMeta() {
	full, short := "", ""
	if d := e.item.Message.Date; !d.IsZero() {
		full = widget.FormatDateTime(d)
		short = widget.FormatDate(d, time.Now())
	}
	when, tooltip := chatDateText(full, short, e.compact)
	meta := chatEventMeta(e.item.Sender, when)
	e.meta.SetText(meta)
	e.meta.SetTooltipText(tooltip)
	e.meta.SetVisible(meta != "")
	e.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(chatAccessibleName(e.item.EventText, e.item.Sender, full))})
}

// showText shows the row of older messages: its sentence.
func (e *chatCenterRow) showText(text string) {
	e.item = conversation.Item{}
	e.event = false
	e.lines.SetText(text)
	e.meta.SetVisible(false)
	e.UpdateProperty([]gtk.AccessibleProperty{gtk.AccessiblePropertyLabel},
		[]coreglib.Value{*coreglib.NewValue(text)})
}

// setCompact shows the short date of a narrow column, or the full one.
func (e *chatCenterRow) setCompact(compact bool) {
	if compact == e.compact {
		return
	}
	e.compact = compact
	if e.event {
		e.showMeta()
	}
}
