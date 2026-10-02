// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

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
	IssueKey    string
	IssueStatus string
	IssueStyle  jira.StatusStyle
	Attachments bool
	Unread      bool
	CountText   string
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
		r.person.AddCSSClass("heading")
		r.title.AddCSSClass("heading")
	} else {
		r.person.RemoveCSSClass("heading")
		r.title.RemoveCSSClass("heading")
	}
	r.title.SetText(d.Title)
	r.snippet.SetText(d.Snippet)
	r.snippet.SetVisible(d.Snippet != "")
	r.date.SetText(d.Date)
	r.due.SetText(d.Due)
	r.due.SetVisible(d.Due != "")
	r.issueKey.SetText(d.IssueKey)
	r.issueKey.SetVisible(d.IssueKey != "")
	SetStatusPill(r.statusPill, d.IssueStatus, d.IssueStyle)
	r.attachment.SetVisible(d.Attachments)
	r.badge.SetText(d.CountText)
	r.badge.SetVisible(d.CountText != "")
	r.unreadDot.SetVisible(d.Unread)
}
