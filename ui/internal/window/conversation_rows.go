// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"sync"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The rows of the conversation view: every item of the stack sits in a
// row with its piece of the timeline (convRow), and the items that are
// not cards are compact rows of their own: a status or assignee change of
// an issue (convEventRow) and the row of the older members left out
// (convTruncatedRow). A port of the macOS client's ConversationRow.swift
// and ConversationEventRow.swift. Every text is plain: it comes from the
// message or the site.

// convCaptionMiddle is how far below the top of a caption's first line its
// middle is: where the dot of an event sits.
const convCaptionMiddle = 8

// convCSS is the look of the conversation view: the pane slightly grey
// behind the cards, a card with a hairline border and rounded corners that
// clip its body, the white page under an HTML body while it has no view,
// the timeline's line and dots, and the avatar of the user's own message
// in the accent colour (over the list's monochrome setting).
const convCSS = `.conversation-pane { background-color: @window_bg_color; }
.conversation-card { background-color: @card_bg_color; border: 1px solid alpha(currentColor, 0.15); border-radius: 10px; }
.conversation-paper { background-color: #ffffff; }
.conversation-line { background-color: alpha(currentColor, 0.3); min-width: 1px; }
.conversation-dot { background-color: alpha(currentColor, 0.3); min-width: 7px; min-height: 7px; border-radius: 4px; }
.conversation-sender { font-weight: 600; }
.conversation-card-button { min-width: 24px; min-height: 20px; padding: 0 2px; }
avatar.conversation-own { background-image: none; background-color: @accent_bg_color; color: @accent_fg_color; }
`

var convCSSOnce sync.Once

// installConvCSS puts convCSS on the display, once; it could live in
// internal/style with the rest of the application's sheet.
func installConvCSS() {
	convCSSOnce.Do(func() {
		p := gtk.NewCSSProvider()
		p.LoadFromString(convCSS)
		gtk.StyleContextAddProviderForDisplay(gdk.DisplayGetDefault(), p, uint(gtk.STYLE_PROVIDER_PRIORITY_APPLICATION))
	})
}

// plainLabel is a label for text from a message or a site: no markup, no
// mnemonics, at the start of its line.
func plainLabel(text string, classes ...string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetUseMarkup(false)
	l.SetUseUnderline(false)
	l.SetXAlign(0)
	for _, c := range classes {
		l.AddCSSClass(c)
	}
	return l
}

// convRow is one item of the stack with its piece of the timeline
// (convRails): the gap above the item, in the gutter the marker (the
// sender's avatar at the top of a card, a dot beside the first line of an
// event or of the row of older messages) with the line above and below it,
// and beside the gutter the item itself. The rows touch, so the pieces of
// the line join: each runs through its own row from edge to edge, the gap
// included, and stops short of the marker. The gutter is decoration: the
// card and the event row name their sender themselves.
type convRow struct {
	*gtk.Box
	content gtk.Widgetter
	// card is the card the row holds, nil for an event and the row of the
	// older members.
	card         *convCard
	avatar       *adw.Avatar // nil for a dot
	above, below *gtk.Box
}

// newConvRow is a row of content marked by marker.
func newConvRow(content gtk.Widgetter, marker convMarker) *convRow {
	r := &convRow{Box: gtk.NewBox(gtk.OrientationHorizontal, 0), content: content}
	gutter := gtk.NewBox(gtk.OrientationVertical, 0)
	gutter.SetSizeRequest(convAvatar, -1)
	markerTop := convItemGap
	var mark gtk.Widgetter
	if marker == markAvatar {
		r.avatar = adw.NewAvatar(convAvatar, "", true)
		r.avatar.SetHAlign(gtk.AlignCenter)
		mark = r.avatar
	} else {
		dot := gtk.NewBox(gtk.OrientationHorizontal, 0)
		dot.AddCSSClass("conversation-dot")
		dot.SetHAlign(gtk.AlignCenter)
		dot.SetSizeRequest(convDot, convDot)
		mark = dot
		markerTop = convItemGap + convCaptionMiddle - convDot/2
	}
	// The line from the row's top to a break above the marker, whose top
	// is then markerTop (the avatar's at the card's top), and from a break
	// under the marker to the row's bottom.
	r.above = gtk.NewBox(gtk.OrientationVertical, 0)
	r.above.AddCSSClass("conversation-line")
	r.above.SetHAlign(gtk.AlignCenter)
	r.above.SetSizeRequest(convLine, markerTop-convLineBreak)
	r.above.SetMarginBottom(convLineBreak)
	r.below = gtk.NewBox(gtk.OrientationVertical, 0)
	r.below.AddCSSClass("conversation-line")
	r.below.SetHAlign(gtk.AlignCenter)
	r.below.SetSizeRequest(convLine, -1)
	r.below.SetVExpand(true)
	r.below.SetMarginTop(convLineBreak)
	gutter.Append(r.above)
	gutter.Append(mark)
	gutter.Append(r.below)

	cw := gtk.BaseWidget(content)
	cw.SetMarginTop(convItemGap)
	cw.SetMarginStart(convGutterGap)
	cw.SetHExpand(true)
	r.Append(gutter)
	r.Append(content)
	return r
}

// show shows the row's piece of the timeline; name is the sender the
// avatar stands for (hostile text, only ever drawn as initials).
func (r *convRow) show(rail convRail, name string) {
	// A piece that is not drawn keeps its place, so the marker stays put.
	r.above.SetOpacity(boolOpacity(rail.Above))
	r.below.SetOpacity(boolOpacity(rail.Below))
	if r.avatar == nil {
		return
	}
	r.avatar.SetText(name)
	if rail.Accent {
		r.avatar.AddCSSClass("conversation-own")
	} else {
		r.avatar.RemoveCSSClass("conversation-own")
	}
}

func boolOpacity(on bool) float64 {
	if on {
		return 1
	}
	return 0
}

// convEventRow is a status or assignee change of an issue
// (conversation.ItemEvent): not a card but a compact native row, never a
// web view and never unread: one line per change in small secondary text,
// who made it, and the time at the trailing edge, in the column of the
// cards' texts. The name gives way (it is cut) before the lines wrap.
type convEventRow struct {
	*gtk.Box
	item   conversation.Item
	lines  *gtk.Box
	sender *gtk.Label
	date   *gtk.Label
}

func newConvEventRow(item conversation.Item, compact bool) *convEventRow {
	e := &convEventRow{Box: gtk.NewBox(gtk.OrientationHorizontal, 8)}
	e.SetMarginStart(convCardPaddingH)
	e.SetMarginEnd(convCardPaddingH)
	e.lines = gtk.NewBox(gtk.OrientationVertical, 2)
	e.lines.SetHExpand(true)
	e.sender = plainLabel("", "caption", "dim-label")
	e.sender.SetEllipsize(pango.EllipsizeEnd)
	e.sender.SetMaxWidthChars(28)
	e.sender.SetVAlign(gtk.AlignStart)
	e.date = plainLabel("", "caption", "dim-label")
	e.date.SetVAlign(gtk.AlignStart)
	e.Append(e.lines)
	e.Append(e.sender)
	e.Append(e.date)
	e.update(item, compact)
	return e
}

// update shows item (the same member, as the model has it now).
func (e *convEventRow) update(item conversation.Item, compact bool) {
	e.item = item
	for c := e.lines.FirstChild(); c != nil; c = e.lines.FirstChild() {
		e.lines.Remove(c)
	}
	for _, text := range item.EventLines {
		l := plainLabel(text, "caption", "dim-label")
		l.SetWrap(true)
		l.SetWrapMode(pango.WrapWordChar)
		e.lines.Append(l)
	}
	e.sender.SetText(item.Sender)
	e.sender.SetTooltipText(item.Sender)
	e.sender.SetVisible(item.Sender != "")
	e.setCompact(compact)
}

// setCompact shows the date in full, or the list's short form in a narrow
// pane with the full one in the tooltip.
func (e *convEventRow) setCompact(compact bool) {
	setConvDate(e.date, e.item.Message.Date, compact)
}

// setConvDate shows date on l: in full, or in the list's short form with
// the full one as the tooltip; hidden without a date.
func setConvDate(l *gtk.Label, date time.Time, compact bool) {
	if date.IsZero() {
		l.SetVisible(false)
		return
	}
	full := widget.FormatDateTime(date)
	if compact {
		l.SetText(widget.FormatDate(date, time.Now()))
		l.SetTooltipText(full)
	} else {
		l.SetText(full)
		l.SetTooltipText("")
	}
	l.SetVisible(true)
}

// convTruncatedRow is the row at the bottom of a conversation (after its
// oldest member shown, convDisplayOrder) whose older members are left out (conversation.ItemTruncated): its sentence in small
// secondary text, in the column of the cards' texts.
type convTruncatedRow struct {
	*gtk.Label
}

func newConvTruncatedRow() *convTruncatedRow {
	l := plainLabel("", "caption", "dim-label")
	l.SetWrap(true)
	l.SetMarginStart(convCardPaddingH)
	l.SetMarginEnd(convCardPaddingH)
	return &convTruncatedRow{Label: l}
}

// clampText cuts s to n runes, for the status label: a link is content of
// the mail.
func clampText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
