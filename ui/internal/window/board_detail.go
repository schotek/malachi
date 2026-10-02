// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The selected case in full: the state pill, title, person/date, the "Why
// is this here?" box, the deadline, the assistant's summary and tasks, the
// reply slot (ui/internal/boardreply), conversation cards and open
// commitments. Case metadata and excerpts are hostile input shown as
// plain text; board_conversation.go alone renders message.body's sanitised
// HTML in Mail's locked card views (CLAUDE.md rule 3).
//
// The reply slot and conversation block stay stable across rebuilds, so
// autosaves preserve the editor and each message's document. The other
// boxes are rebuilt from the current detail.

// renderDetail shows the selected case, or the empty placeholder.
func (p *boardPage) renderDetail(vm board.ViewModel) {
	d := vm.Detail
	if d == nil {
		if p.conversation != nil {
			p.conversation.apply(nil)
		}
		p.noSelectionPage.SetIconName("view-grid-symbolic")
		p.noSelectionPage.SetTitle(board.NoSelectionTitle(i18n.Tr))
		p.noSelectionPage.SetDescription(board.NoSelectionBody(i18n.Tr))
		p.detailStack.SetVisibleChildName("empty")
		return
	}
	p.detailStack.SetVisibleChildName("detail")
	p.renderDetailHeader(*d)
	p.renderDetailTop(*d)
	p.renderDetailDue(*d)
	p.renderDetailSummary(*d)
	p.renderDetailReplySlot(*d)
	p.renderDetailConversation(*d)
	p.renderDetailCommitments(*d)
}

// renderDetailHeader is the detail pane's own header bar: Done / Move Back
// to Board, Remind…, Archive, Reply (or Comment), Show in Mail.
func (p *boardPage) renderDetailHeader(d board.Detail) {
	if d.IsDone {
		p.doneButton.SetLabel(board.NotDone(i18n.Tr))
		p.doneButton.SetActionName("win.board-reopen")
	} else {
		p.doneButton.SetLabel(board.Done(i18n.Tr))
		p.doneButton.SetActionName("win.board-mark-done")
	}
	p.remindButton.SetLabel(board.Remind(i18n.Tr))
	p.archiveButton.SetLabel(board.Archive(i18n.Tr))
	p.archiveButton.SetSensitive(!d.IsDone)
	comment := p.w.boardCaseComments(d)
	p.replyButton.SetLabel(jira.ReplyLabel(comment, i18n.Tr))
	p.replyButton.SetActionName("win.board-reply")
	p.detailMenu.SetMenuModel(boardDetailMenu())
}

// boardCaseComments reports whether the case's reply is a comment (a Jira
// account's capability), by its account (capabilities.Supported over the
// account the case belongs to, as action_rules.go reads it for mail).
func (w *Window) boardCaseComments(d board.Detail) bool {
	acc, ok := w.model.account(d.AccountID)
	return ok && acc.Can(api.CapabilityComment)
}

// renderDetailTop rebuilds the state pill, title, person/date and the Why /
// Unstar links.
func (p *boardPage) renderDetailTop(d board.Detail) {
	removeAllChildren(p.detailTop)

	pillRow := gtk.NewBox(gtk.OrientationHorizontal, 8)
	pill := gtk.NewMenuButton()
	p.statePill = pill
	pill.AddCSSClass("board-state-pill")
	if c := boardStatePillClass(d.State); c != "" {
		pill.AddCSSClass(c)
	}
	pill.SetLabel(d.StateTitle)
	pill.SetTooltipText(board.StateLabel(i18n.Tr))
	pill.SetMenuModel(p.w.boardStateMenu())
	pillRow.Append(pill)
	if d.Issue != nil {
		status := widget.NewPill()
		widget.SetStatusPill(status, d.Issue.Status, d.Issue.Style)
		pillRow.Append(status)
	}
	p.detailTop.Append(pillRow)

	title := gtk.NewLabel(boardTitleText(d.Title, d.TitleIsAssistant))
	title.SetUseMarkup(false)
	title.SetXAlign(0)
	title.SetWrap(true)
	title.SetSelectable(true)
	title.AddCSSClass("title-2")
	p.detailTop.Append(title)
	if d.Subject != "" {
		subj := gtk.NewLabel(d.Subject)
		subj.SetUseMarkup(false)
		subj.SetXAlign(0)
		subj.SetWrap(true)
		subj.AddCSSClass("dim-label")
		p.detailTop.Append(subj)
	}

	person := gtk.NewLabel(d.Person + " · " + d.Time)
	person.SetUseMarkup(false)
	person.SetXAlign(0)
	person.AddCSSClass("dim-label")
	p.detailTop.Append(person)

	if d.RemindText != "" {
		snoozed := gtk.NewLabel(d.RemindText)
		snoozed.SetUseMarkup(false)
		snoozed.SetXAlign(0)
		snoozed.AddCSSClass("caption")
		snoozed.AddCSSClass("dim-label")
		p.detailTop.Append(snoozed)
	}

	links := gtk.NewBox(gtk.OrientationHorizontal, 8)
	why := gtk.NewButtonWithLabel(board.WhyLink(i18n.Tr))
	why.AddCSSClass("flat")
	why.AddCSSClass("link")
	why.ConnectClicked(p.ctl.ToggleWhy)
	links.Append(why)
	if d.CanUnstar {
		unstar := gtk.NewButtonWithLabel(board.Unstar(i18n.Tr))
		unstar.AddCSSClass("flat")
		unstar.AddCSSClass("link")
		id := d.ID
		unstar.ConnectClicked(func() { p.ctl.Unflag(id) })
		links.Append(unstar)
	}
	p.detailTop.Append(links)

	if p.ctl.State().RevealsWhy {
		box := gtk.NewBox(gtk.OrientationVertical, 2)
		box.AddCSSClass("board-assistant-box")
		why := gtk.NewLabel(d.Why)
		why.SetUseMarkup(false)
		why.SetXAlign(0)
		why.SetWrap(true)
		if d.WhyIsAssistant {
			why.SetText(board.AssistantMark + " " + d.Why)
		}
		box.Append(why)
		if d.StaleNote != "" {
			note := gtk.NewLabel(d.StaleNote)
			note.SetUseMarkup(false)
			note.SetXAlign(0)
			note.SetWrap(true)
			note.AddCSSClass("caption")
			note.AddCSSClass("dim-label")
			box.Append(note)
		}
		p.detailTop.Append(box)
	}
}

// renderDetailDue shows the deadline with its quote.
func (p *boardPage) renderDetailDue(d board.Detail) {
	removeAllChildren(p.dueBox)
	p.dueBox.SetVisible(d.Due != "")
	if d.Due == "" {
		return
	}
	head := gtk.NewLabel(board.AssistantMark + " " + d.Due)
	head.SetUseMarkup(false)
	head.SetXAlign(0)
	head.AddCSSClass("heading")
	head.AddCSSClass("board-assistant-mark")
	p.dueBox.Append(head)
	if d.DueQuote != "" {
		quote := gtk.NewLabel(board.Quoted(d.DueQuote, i18n.Tr))
		quote.SetUseMarkup(false)
		quote.SetXAlign(0)
		quote.SetWrap(true)
		quote.AddCSSClass("caption")
		quote.AddCSSClass("dim-label")
		p.dueBox.Append(quote)
	}
}

// renderDetailSummary shows the assistant's summary and tasks.
func (p *boardPage) renderDetailSummary(d board.Detail) {
	removeAllChildren(p.summaryBox)
	has := d.Summary != "" || len(d.Tasks) > 0
	p.summaryBox.SetVisible(has)
	if !has {
		return
	}
	box := gtk.NewBox(gtk.OrientationVertical, 8)
	box.AddCSSClass("board-assistant-box")
	if d.Summary != "" {
		head := gtk.NewLabel(board.SummaryHeading(i18n.Tr))
		head.SetUseMarkup(false)
		head.SetXAlign(0)
		head.AddCSSClass("heading")
		box.Append(head)
		body := gtk.NewLabel(d.Summary)
		body.SetUseMarkup(false)
		body.SetXAlign(0)
		body.SetWrap(true)
		body.SetSelectable(true)
		box.Append(body)
	}
	if len(d.Tasks) > 0 {
		head := gtk.NewLabel(board.TasksHeading(i18n.Tr))
		head.SetUseMarkup(false)
		head.SetXAlign(0)
		head.AddCSSClass("heading")
		box.Append(head)
		for _, t := range d.Tasks {
			row := gtk.NewLabel("• " + t)
			row.SetUseMarkup(false)
			row.SetXAlign(0)
			row.SetWrap(true)
			box.Append(row)
		}
	}
	p.summaryBox.Append(box)
}

// renderDetailCommitments shows "From the Assistant": the case's open
// commitments, each with a tick that marks it done.
func (p *boardPage) renderDetailCommitments(d board.Detail) {
	removeAllChildren(p.commitmentsBox)
	own := p.ctl.View().Commitments
	var mine []board.CommitmentRow
	for _, k := range own {
		if k.CaseID == d.ID {
			mine = append(mine, k)
		}
	}
	p.commitmentsBox.SetVisible(len(mine) > 0)
	if len(mine) == 0 {
		return
	}
	head := gtk.NewLabel(board.FromAssistant(i18n.Tr))
	head.SetUseMarkup(false)
	head.SetXAlign(0)
	head.AddCSSClass("heading")
	p.commitmentsBox.Append(head)
	for _, k := range mine {
		row := gtk.NewBox(gtk.OrientationHorizontal, 8)
		check := gtk.NewCheckButton()
		check.SetTooltipText(board.MarkPromiseDone(i18n.Tr))
		id := k.ID
		check.ConnectToggled(func() { p.ctl.SetCommitmentDone(id, check.Active()) })
		row.Append(check)
		text := gtk.NewLabel(k.Text)
		text.SetUseMarkup(false)
		text.SetXAlign(0)
		text.SetWrap(true)
		text.SetHExpand(true)
		row.Append(text)
		if k.Due != "" {
			due := gtk.NewLabel(k.Due)
			due.SetUseMarkup(false)
			due.AddCSSClass("caption")
			due.AddCSSClass("dim-label")
			row.Append(due)
		}
		p.commitmentsBox.Append(row)
	}
}

// removeAllChildren empties a container Box built from Blueprint
// (Gtk.Box has no RemoveAll, unlike Gtk.ListBox).
func removeAllChildren(box *gtk.Box) {
	for {
		c := box.FirstChild()
		if c == nil {
			return
		}
		box.Remove(c)
	}
}
