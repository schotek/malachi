// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"slices"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

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
		// Adw.StatusPage's description is markup: escaped (setStatusPage).
		setStatusPage(p.noSelectionPage, "view-grid-symbolic", board.NoSelectionTitle(i18n.Tr), board.NoSelectionBody(i18n.Tr))
		p.detailStack.SetVisibleChildName("empty")
		return
	}
	p.detailStack.SetVisibleChildName("detail")
	// The deadline, summary and commitment boxes are still rebuilt whole:
	// the keyboard on a commitment's tick goes to the state pill rather
	// than nowhere.
	refocus := p.keyboardIn(p.dueBox, p.summaryBox, p.commitmentsBox)
	p.renderDetailHeader(*d)
	p.renderDetailTop(*d)
	p.renderDetailDue(*d)
	p.renderDetailSummary(*d)
	p.renderDetailReplySlot(*d)
	p.renderDetailConversation(*d)
	p.renderDetailCommitments(*d)
	if refocus && !p.keyboardIn(p.dueBox, p.summaryBox, p.commitmentsBox) && p.statePill != nil {
		p.statePill.GrabFocus()
	}
}

// keyboardIn reports the window's focus inside one of boxes.
func (p *boardPage) keyboardIn(boxes ...*gtk.Box) bool {
	f := p.w.Focus()
	if f == nil {
		return false
	}
	for _, b := range boxes {
		if gtk.BaseWidget(f).IsAncestor(b) {
			return true
		}
	}
	return false
}

// renderDetailHeader is the detail pane's own header bar: Done / Move Back
// to Board, Remind…, Archive, Reply (or Comment), Show in Mail. The
// buttons act through the win.board-* actions, whose enabled state
// (applyActionsSensitivity) is the buttons' sensitivity; the menus are set
// once (wireDetailHeader), so a refresh never closes an open one.
func (p *boardPage) renderDetailHeader(d board.Detail) {
	if d.IsDone {
		p.doneButton.SetLabel(board.NotDone(i18n.Tr))
		p.doneButton.SetActionName("win.board-reopen")
	} else {
		p.doneButton.SetLabel(board.Done(i18n.Tr))
		p.doneButton.SetActionName("win.board-mark-done")
	}
	comment := p.w.boardCaseComments(d)
	p.replyButton.SetLabel(jira.ReplyLabel(comment, i18n.Tr))
}

// wireDetailHeader sets what the detail header's buttons never change:
// their texts and actions, the "…" menu and the Remind popover.
func (p *boardPage) wireDetailHeader() {
	p.remindButton.SetLabel(board.Remind(i18n.Tr))
	p.archiveButton.SetLabel(board.Archive(i18n.Tr))
	p.archiveButton.SetActionName("win.board-archive")
	p.replyButton.SetActionName("win.board-reply")
	p.detailMenu.SetMenuModel(boardDetailMenu())
	p.wireRemindButton()
}

// boardCaseComments reports whether the case's reply is a comment (a Jira
// account's capability), by its account (capabilities.Supported over the
// account the case belongs to, as action_rules.go reads it for mail).
func (w *Window) boardCaseComments(d board.Detail) bool {
	acc, ok := w.model.account(d.AccountID)
	return ok && acc.Can(api.CapabilityComment)
}

// boardDetailTop is the top of the detail — the state pill, the issue's
// status, the badges, title, subject, byline, remind line, the Why and
// Unstar links and the "Why is this here?" box — built once and updated in
// place: ChangeContent fires on every autosave and annotation, and a
// rebuilt pill or link would take the keyboard with it.
type boardDetailTop struct {
	pill      *gtk.MenuButton
	pillClass string
	issue     *gtk.Label
	badges    *gtk.Box
	shown     []string // the badges shown
	title     *gtk.Label
	subject   *gtk.Label
	byline    *gtk.Label
	remind    *gtk.Label
	why       *gtk.Button
	unstar    *gtk.Button
	whyBox    *gtk.Box
	whyText   *gtk.Label
	source    *gtk.Label
	notes     *gtk.Box
	noteTexts []string
	stale     *gtk.Label
}

// detailLabel is a plain-text label of the detail's top.
func detailLabel(classes ...string) *gtk.Label {
	l := gtk.NewLabel("")
	l.SetUseMarkup(false)
	l.SetXAlign(0)
	l.SetWrap(true)
	for _, c := range classes {
		l.AddCSSClass(c)
	}
	return l
}

// buildDetailTop makes p.top's widgets into board_detail_top, once.
func (p *boardPage) buildDetailTop() *boardDetailTop {
	t := &boardDetailTop{}
	pillRow := gtk.NewBox(gtk.OrientationHorizontal, 8)
	t.pill = gtk.NewMenuButton()
	t.pill.AddCSSClass("board-state-pill")
	t.pill.SetTooltipText(board.StateLabel(i18n.Tr))
	t.pill.SetMenuModel(p.w.boardStateMenu())
	pillRow.Append(t.pill)
	t.issue = widget.NewPill()
	pillRow.Append(t.issue)
	t.badges = gtk.NewBox(gtk.OrientationHorizontal, 4)
	t.badges.SetVAlign(gtk.AlignCenter)
	t.badges.SetVisible(false)
	pillRow.Append(t.badges)
	p.detailTop.Append(pillRow)
	p.statePill = t.pill

	t.title = detailLabel("title-2")
	t.title.SetSelectable(true)
	p.detailTop.Append(t.title)
	t.subject = detailLabel("dim-label")
	p.detailTop.Append(t.subject)
	t.byline = detailLabel("dim-label")
	t.byline.SetWrap(false)
	t.byline.SetEllipsize(pango.EllipsizeEnd)
	p.detailTop.Append(t.byline)
	t.remind = detailLabel("caption", "dim-label")
	p.detailTop.Append(t.remind)

	links := gtk.NewBox(gtk.OrientationHorizontal, 8)
	t.why = gtk.NewButtonWithLabel(board.WhyLink(i18n.Tr))
	t.why.AddCSSClass("flat")
	t.why.AddCSSClass("link")
	t.why.ConnectClicked(p.ctl.ToggleWhy)
	links.Append(t.why)
	t.unstar = gtk.NewButtonWithLabel(board.Unstar(i18n.Tr))
	t.unstar.AddCSSClass("flat")
	t.unstar.AddCSSClass("link")
	t.unstar.ConnectClicked(func() {
		if d := p.ctl.View().Detail; d != nil && d.CanUnstar {
			p.ctl.Unflag(d.ID)
		}
	})
	links.Append(t.unstar)
	p.detailTop.Append(links)

	t.whyBox = gtk.NewBox(gtk.OrientationVertical, 4)
	t.whyBox.AddCSSClass("board-assistant-box")
	t.whyText = detailLabel()
	t.whyBox.Append(t.whyText)
	t.source = detailLabel("caption", "dim-label")
	t.whyBox.Append(t.source)
	t.notes = gtk.NewBox(gtk.OrientationVertical, 2)
	t.whyBox.Append(t.notes)
	t.stale = detailLabel("caption", "dim-label")
	t.whyBox.Append(t.stale)
	p.detailTop.Append(t.whyBox)
	return t
}

// setText shows text in l, hiding l while it is empty.
func setShownText(l *gtk.Label, text string) {
	l.SetText(text)
	l.SetVisible(text != "")
}

// renderDetailTop shows the state pill, the badges, title, byline and the
// Why / Unstar links of d, in place.
func (p *boardPage) renderDetailTop(d board.Detail) {
	if p.top == nil {
		p.top = p.buildDetailTop()
	}
	t := p.top
	if c := boardStatePillClass(d.State); c != t.pillClass {
		if t.pillClass != "" {
			t.pill.RemoveCSSClass(t.pillClass)
		}
		if c != "" {
			t.pill.AddCSSClass(c)
		}
		t.pillClass = c
	}
	t.pill.SetLabel(d.StateTitle)
	if d.Issue != nil {
		widget.SetStatusPill(t.issue, d.Issue.Status, d.Issue.Style)
	} else {
		widget.SetStatusPill(t.issue, "", jira.StatusPlain)
	}
	t.badges.SetVisible(len(d.Badges) > 0)
	if !slices.Equal(d.Badges, t.shown) {
		t.shown = slices.Clone(d.Badges)
		removeAllChildren(t.badges)
		for _, b := range d.Badges {
			t.badges.Append(widget.NewBadgePill(b))
		}
	}

	t.title.SetText(boardTitleText(d.Title, d.TitleIsAssistant))
	setShownText(t.subject, d.Subject)
	setShownText(t.byline, d.Byline)
	setShownText(t.remind, d.RemindText)
	t.unstar.SetVisible(d.CanUnstar)

	reveals := p.ctl.State().RevealsWhy
	t.whyBox.SetVisible(reveals)
	if !reveals {
		return
	}
	why := d.Why
	if d.WhyIsAssistant && why != "" {
		why = board.AssistantMark + " " + why
	}
	t.whyText.SetText(why)
	setShownText(t.source, d.SourceText)
	if !slices.Equal(d.WhyNotes, t.noteTexts) {
		t.noteTexts = slices.Clone(d.WhyNotes)
		removeAllChildren(t.notes)
		for _, n := range d.WhyNotes {
			l := detailLabel()
			l.SetText(n)
			t.notes.Append(l)
		}
	}
	t.notes.SetVisible(len(d.WhyNotes) > 0)
	setShownText(t.stale, d.StaleNote)
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
	own := p.ctl.View().Commitments
	var mine []board.CommitmentRow
	for _, k := range own {
		if k.CaseID == d.ID {
			mine = append(mine, k)
		}
	}
	p.commitmentsBox.SetVisible(len(mine) > 0)
	// The same commitments keep their rows (and a focused tick).
	if p.commitmentsCase == d.ID && slices.Equal(mine, p.commitmentsShown) {
		return
	}
	p.commitmentsCase, p.commitmentsShown = d.ID, mine
	if len(mine) == 0 {
		removeAllChildren(p.commitmentsBox)
		return
	}
	removeAllChildren(p.commitmentsBox)
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
