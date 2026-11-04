// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"slices"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/jira"
)

// BoardRow is one row of the board's List style, built from
// data/ui/board_row.blp (a projection of ui/internal/board.Row onto
// widgets). Each row parses its own copy of the builder XML, as MessageRow
// does; the board's offline window keeps the list bounded, so this is fine
// for now (message_row.go's comment on a future GtkListView applies here
// too).
type BoardRow struct {
	*gtk.ListBoxRow

	stateDot   *gtk.Box
	person     *gtk.Label
	issueKey   *gtk.Label
	statusPill *gtk.Label
	badges     *gtk.Box
	// badgeTexts are the badges shown, so SetRow rebuilds the pills only
	// when they change.
	badgeTexts []string
	attachment *gtk.Image
	badge      *gtk.Label
	date       *gtk.Label
	unreadDot  *gtk.Box
	title      *gtk.Label
	snippet    *gtk.Label
	due        *gtk.Label
}

// NewBoardRow builds an empty row; call SetRow to fill it.
func NewBoardRow() *BoardRow {
	b := data.Builder("board_row.ui")
	return &BoardRow{
		ListBoxRow: b.GetObject("board_row").Cast().(*gtk.ListBoxRow),
		stateDot:   b.GetObject("state_dot").Cast().(*gtk.Box),
		person:     b.GetObject("person_label").Cast().(*gtk.Label),
		issueKey:   b.GetObject("issue_key").Cast().(*gtk.Label),
		statusPill: b.GetObject("status_pill").Cast().(*gtk.Label),
		badges:     b.GetObject("badges_box").Cast().(*gtk.Box),
		attachment: b.GetObject("attachment_icon").Cast().(*gtk.Image),
		badge:      b.GetObject("count_badge").Cast().(*gtk.Label),
		date:       b.GetObject("date_label").Cast().(*gtk.Label),
		unreadDot:  b.GetObject("unread_dot").Cast().(*gtk.Box),
		title:      b.GetObject("title_label").Cast().(*gtk.Label),
		snippet:    b.GetObject("snippet_label").Cast().(*gtk.Label),
		due:        b.GetObject("due_label").Cast().(*gtk.Label),
	}
}

// stateDotClasses are every board-state-dot-* class a row's dot may carry
// (internal/style), removed before the current one (if any) is added.
var stateDotClasses = []string{
	"board-state-dot-hot", "board-state-dot-you", "board-state-dot-them", "board-state-dot-info",
}

// BoardRowData is what a BoardRow displays: a projection of
// ui/internal/board.Row. The caller passes plain fields rather than the
// board package's type, so this widget stays free of it, as MessageRow
// stays free of api.MessageSummary's own quirks.
type BoardRowData struct {
	// StateClass is one of stateDotClasses, "" for none.
	StateClass  string
	Person      string
	Title       string
	Snippet     string
	Date        string
	Due         string
	DueOverdue  bool
	IssueKey    string
	IssueStatus string
	IssueStyle  jira.StatusStyle
	Attachments bool
	Unread      bool
	CountText   string
	// Badges are the case's small pills (Reminded, New contact), in
	// order; plain text.
	Badges []string
}

// SetRow displays d. Every string is untrusted (mail, or an assistant that
// read mail) and shown as plain text only (CLAUDE.md rule 3).
func (r *BoardRow) SetRow(d BoardRowData) {
	for _, c := range stateDotClasses {
		r.stateDot.RemoveCSSClass(c)
	}
	if d.StateClass != "" {
		r.stateDot.AddCSSClass(d.StateClass)
	}
	r.person.SetText(d.Person)
	if d.Unread {
		r.title.AddCSSClass("heading")
	} else {
		r.title.RemoveCSSClass("heading")
	}
	r.title.SetText(d.Title)
	r.snippet.SetText(d.Snippet)
	r.snippet.SetVisible(d.Snippet != "")
	r.date.SetText(d.Date)
	r.due.SetText(d.Due)
	r.due.SetVisible(d.Due != "")
	if d.DueOverdue {
		r.due.AddCSSClass("board-due-overdue")
	} else {
		r.due.RemoveCSSClass("board-due-overdue")
	}
	r.issueKey.SetText(d.IssueKey)
	r.issueKey.SetVisible(d.IssueKey != "")
	SetStatusPill(r.statusPill, d.IssueStatus, d.IssueStyle)
	r.attachment.SetVisible(d.Attachments)
	r.badge.SetText(d.CountText)
	r.badge.SetVisible(d.CountText != "")
	r.unreadDot.SetVisible(d.Unread)
	r.setBadges(d.Badges)
}

// setBadges shows texts as neutral pills, rebuilt only when they change.
func (r *BoardRow) setBadges(texts []string) {
	r.badges.SetVisible(len(texts) > 0)
	if slices.Equal(texts, r.badgeTexts) {
		return
	}
	r.badgeTexts = slices.Clone(texts)
	for c := r.badges.FirstChild(); c != nil; c = r.badges.FirstChild() {
		r.badges.Remove(c)
	}
	for _, t := range texts {
		r.badges.Append(NewBadgePill(t))
	}
}

// NewBadgePill is one of a board case's badges (Reminded, New contact): a
// neutral pill of plain text, its text as its tooltip since a long one is
// cut.
func NewBadgePill(text string) *gtk.Label {
	l := NewPill()
	l.AddCSSClass("board-badge")
	l.SetText(text)
	l.SetTooltipText(text)
	l.SetVisible(text != "")
	return l
}

// SetCardPresentation gives a column's card up to two lines for its title
// and summary; the other board lists retain compact, single-line labels.
func (r *BoardRow) SetCardPresentation() {
	for _, label := range []*gtk.Label{r.title, r.snippet} {
		label.SetWrap(true)
		label.SetWrapMode(pango.WrapWordChar)
		label.SetLines(2)
	}
}
