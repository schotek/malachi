// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The pills of an issue-tracker account (kind jira): an issue's status in
// the message list, on the issue card and in the conversation view, the
// Internal badge of a service-desk comment, and the kind capsule after a
// Jira account's name in the sidebar. The colours are CSS of
// internal/style (issue-pill with the status style's class, the
// jira.StatusStyle values; internal-pill; kind-badge). Every text is
// plain: it comes from the site.

// statusClasses are the colour classes a status pill may carry.
var statusClasses = []jira.StatusStyle{jira.StatusTodo, jira.StatusInProgress, jira.StatusDone}

// NewPill is an empty, hidden pill label: one line, ellipsised at 24
// characters, plain text.
func NewPill() *gtk.Label {
	l := gtk.NewLabel("")
	l.SetUseMarkup(false)
	l.SetEllipsize(pango.EllipsizeEnd)
	l.SetMaxWidthChars(24)
	l.SetVAlign(gtk.AlignCenter)
	l.AddCSSClass("issue-pill")
	l.SetVisible(false)
	return l
}

// SetStatusPill shows text in pill l in the colour of style; an empty text
// hides the pill. The status is its own tooltip, since a long one is cut.
func SetStatusPill(l *gtk.Label, text string, style jira.StatusStyle) {
	for _, c := range statusClasses {
		l.RemoveCSSClass(string(c))
	}
	l.RemoveCSSClass("internal-pill")
	if style != jira.StatusPlain {
		l.AddCSSClass(string(style))
	}
	l.SetText(text)
	l.SetTooltipText(text)
	l.SetVisible(text != "")
}

// SetInternalPill shows the Internal badge of a service-desk comment in
// pill l; an empty text hides it.
func SetInternalPill(l *gtk.Label, text string) {
	for _, c := range statusClasses {
		l.RemoveCSSClass(string(c))
	}
	l.AddCSSClass("internal-pill")
	l.SetText(text)
	l.SetTooltipText("")
	l.SetVisible(text != "")
}

// NewKindBadge is the capsule after an account's name in the sidebar
// ("JIRA", jira.KindBadge; a brand name, never translated); hidden while
// text is empty.
func NewKindBadge(text string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetUseMarkup(false)
	l.SetVAlign(gtk.AlignCenter)
	l.AddCSSClass("kind-badge")
	l.SetVisible(text != "")
	return l
}

// SetBulkPill shows tag in the neutral pill of a bulk message (the text is
// its own tooltip, as a long one is cut); an empty tag, or show false (a
// row of an issue), hides it.
func SetBulkPill(l *gtk.Label, tag string, show bool) {
	l.SetText(tag)
	l.SetTooltipText(tag)
	l.SetVisible(show && tag != "")
}

// invitationClasses are the colour classes of an invitation's pill
// (style/calendar_card.go), by calendar.InvitationPill.
var invitationClasses = map[calendar.InvitationPill]string{
	calendar.PillInvitation: "invitation-pill",
	calendar.PillUpdate:     "invitation-update",
	calendar.PillCancelled:  "invitation-cancelled",
	calendar.PillReply:      "invitation-reply",
}

// InvitationClass is the colour class of pill p ("" for none): the list
// pill's and the invitation card's tile.
func InvitationClass(p calendar.InvitationPill) string { return invitationClasses[p] }

// SetInvitationClass puts the colour class of p on w, taking the others
// off.
func SetInvitationClass(w interface {
	AddCSSClass(string)
	RemoveCSSClass(string)
}, p calendar.InvitationPill) {
	for _, c := range invitationClasses {
		w.RemoveCSSClass(c)
	}
	if c := invitationClasses[p]; c != "" {
		w.AddCSSClass(c)
	}
}

// SetInvitationPill shows the invitation pill p in l (docs/calendar.md
// §5.7: Invitation blue, Update orange, Cancelled red, Reply grey); none
// hides it. The text is the app's own (calendar.PillTitle).
func SetInvitationPill(l *gtk.Label, p calendar.InvitationPill) {
	SetInvitationClass(l, p)
	title := calendar.PillTitle(p, i18n.Tr)
	l.SetText(title)
	l.SetVisible(title != "")
}

// StrikeAttrs is the Pango strike-through over a whole label's text, for
// the subject of a cancelled event (never markup); nil when off.
func StrikeAttrs(on bool) *pango.AttrList {
	if !on {
		return nil
	}
	list := pango.NewAttrList()
	list.Insert(pango.NewAttrStrikethrough(true))
	return list
}
