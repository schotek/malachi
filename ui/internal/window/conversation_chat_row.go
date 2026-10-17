// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"strconv"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/conversation"
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
	if right {
		r.Append(spacer)
		r.Append(r.clamp)
		r.Append(r.avatar)
	} else {
		r.Append(r.avatar)
		r.Append(r.clamp)
		r.Append(spacer)
	}

	click := gtk.NewGestureClick()
	click.SetButton(gdk.BUTTON_PRIMARY)
	click.ConnectReleased(func(n int, _, _ float64) {
		if n == 2 {
			activate(r)
		}
	})
	r.bubble.frame.AddController(click)
	return r
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

	column := gtk.NewBox(gtk.OrientationVertical, 6)
	column.Append(header)
	column.Append(b.text)
	b.frame.SetChild(column)
	return b
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
	b.text.SetVisible(text != "")
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
