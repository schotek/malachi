// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Message is what a MessageRow displays; a projection of api.MessageSummary.
type Message struct {
	From           []api.Address
	Subject        string
	Snippet        string
	Date           time.Time
	Unread         bool
	Flagged        bool
	HasAttachments bool
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
// Each row parses its own copy of the builder XML. That is fine for lists
// bounded by the offline window; a Gtk.ListView with a list-item factory
// can replace it later.
type MessageRow struct {
	*gtk.ListBoxRow

	box        *gtk.Box
	avatar     *adw.Avatar
	from       *gtk.Label
	date       *gtk.Label
	subject    *gtk.Label
	preview    *gtk.Label
	unreadDot  *gtk.Box
	attachment *gtk.Image
	star       *gtk.Image
}

// NewMessageRow builds an empty row; call SetMessage to fill it.
func NewMessageRow() *MessageRow {
	b := data.Builder("message_row.ui")
	return &MessageRow{
		ListBoxRow: b.GetObject("message_row").Cast().(*gtk.ListBoxRow),
		box:        b.GetObject("content_box").Cast().(*gtk.Box),
		avatar:     b.GetObject("avatar").Cast().(*adw.Avatar),
		from:       b.GetObject("from_label").Cast().(*gtk.Label),
		date:       b.GetObject("date_label").Cast().(*gtk.Label),
		subject:    b.GetObject("subject_label").Cast().(*gtk.Label),
		preview:    b.GetObject("preview_label").Cast().(*gtk.Label),
		unreadDot:  b.GetObject("unread_dot").Cast().(*gtk.Box),
		attachment: b.GetObject("attachment_icon").Cast().(*gtk.Image),
		star:       b.GetObject("star_icon").Cast().(*gtk.Image),
	}
}

// SetMessage displays m. All strings are untrusted and shown as plain text.
// The first sender is shown; a message without one gets an empty name.
func (r *MessageRow) SetMessage(m Message) {
	var from api.Address
	if len(m.From) > 0 {
		from = m.From[0]
	}
	name := DisplayName(from)
	r.avatar.SetText(name)
	r.from.SetText(name)
	r.from.SetTooltipText(FormatAddress(from))
	r.date.SetText(FormatDate(m.Date, time.Now()))
	subject := strings.TrimSpace(m.Subject)
	if subject == "" {
		subject = i18n.T("(No subject)")
	}
	r.subject.SetText(subject)
	r.preview.SetText(m.Snippet)

	r.attachment.SetVisible(m.HasAttachments)
	r.star.SetVisible(m.Flagged)
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
