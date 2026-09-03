// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
)

// Message is what a MessageRow displays; a projection of api.MessageSummary.
type Message struct {
	From    api.Address
	Subject string
	Snippet string
	Date    time.Time
	Unread  bool
}

// Row geometry for the two list densities.
const (
	avatarSizeComfortable = 40
	avatarSizeCompact     = 28
	rowMarginComfortable  = 8
	rowMarginCompact      = 3
)

// MessageRow is one row of the message list, built from
// data/ui/message_row.blp.
//
// Each row parses its own copy of the builder XML. That is fine for the
// current placeholder list; the real list will move to Gtk.ListView with a
// list-item factory (TODO(phase-1)).
type MessageRow struct {
	*gtk.ListBoxRow

	box       *gtk.Box
	avatar    *adw.Avatar
	from      *gtk.Label
	date      *gtk.Label
	subject   *gtk.Label
	preview   *gtk.Label
	unreadDot *gtk.Box
}

// NewMessageRow builds an empty row; call SetMessage to fill it.
func NewMessageRow() *MessageRow {
	b := gtk.NewBuilderFromString(data.MustUI("message_row.ui"))
	return &MessageRow{
		ListBoxRow: b.GetObject("message_row").Cast().(*gtk.ListBoxRow),
		box:        b.GetObject("content_box").Cast().(*gtk.Box),
		avatar:     b.GetObject("avatar").Cast().(*adw.Avatar),
		from:       b.GetObject("from_label").Cast().(*gtk.Label),
		date:       b.GetObject("date_label").Cast().(*gtk.Label),
		subject:    b.GetObject("subject_label").Cast().(*gtk.Label),
		preview:    b.GetObject("preview_label").Cast().(*gtk.Label),
		unreadDot:  b.GetObject("unread_dot").Cast().(*gtk.Box),
	}
}

// SetMessage displays m. All strings are untrusted and shown as plain text.
func (r *MessageRow) SetMessage(m Message) {
	name := DisplayName(m.From)
	r.avatar.SetText(name)
	r.from.SetText(name)
	r.from.SetTooltipText(FormatAddress(m.From))
	r.date.SetText(FormatDate(m.Date, time.Now()))
	r.subject.SetText(m.Subject)
	r.preview.SetText(m.Snippet)

	r.unreadDot.SetVisible(m.Unread)
	for _, l := range []*gtk.Label{r.from, r.subject} {
		if m.Unread {
			l.AddCSSClass("heading")
		} else {
			l.RemoveCSSClass("heading")
		}
	}
}

// SetCompact switches between the comfortable and compact densities.
func (r *MessageRow) SetCompact(compact bool) {
	margin, size := rowMarginComfortable, avatarSizeComfortable
	if compact {
		margin, size = rowMarginCompact, avatarSizeCompact
		r.AddCSSClass("compact")
	} else {
		r.RemoveCSSClass("compact")
	}
	r.box.SetMarginTop(margin)
	r.box.SetMarginBottom(margin)
	r.avatar.SetSize(size)
}

// SetShowPreview shows or hides the snippet line.
func (r *MessageRow) SetShowPreview(show bool) { r.preview.SetVisible(show) }

// SetShowAvatar shows or hides the sender avatar.
func (r *MessageRow) SetShowAvatar(show bool) { r.avatar.SetVisible(show) }
