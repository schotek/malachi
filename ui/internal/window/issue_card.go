// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// issueCard is the issue card over the headers of a Jira message
// (jira.IssueCard): the key as a link to the issue, the status pill, the
// Internal badge of an internal service-desk comment, who relayed the
// comment ("via …") and whether it was edited, then Assignee, Priority,
// Type and Reporter. The issue's summary is the header's subject. Hidden
// for a mail message. Everything but the labels comes from the Jira site
// and is plain text; the key opens its URL only when jira.IsIssueURL
// accepts it for the account's site (issueReading.openable), otherwise it
// is plain text. On an account that changes statuses
// (api.CapabilityTransition) the pill is a menu button with the Change
// Status menu (transitionMenu); while a transition runs it shows a spinner
// (setBusy), and the result's issue comes back through show. The same card
// sits on top of a Jira conversation in the conversation view. macOS has it
// as IssueCardView and IssueStatusPill.
type issueCard struct {
	*gtk.Box

	win    *Window
	parent *gtk.Window

	keyButton *gtk.Button
	keyText   *gtk.Label // the button's label
	keyLabel  *gtk.Label
	// status is the pill as a menu button (statusText, statusSpinner in
	// it); plainStatus the pill where the account cannot change statuses.
	status        *gtk.MenuButton
	statusText    *gtk.Label
	statusSpinner *adw.Spinner
	plainStatus   *gtk.Label
	internal      *gtk.Label
	via, edited   *gtk.Label
	fieldPairs    []*gtk.Box
	fieldLabels   []*gtk.Label
	fieldValues   []*gtk.Label

	menu *transitionMenu

	// url is what the key opens ("" when it opens nothing); key the
	// cleaned key of the issue on show, account its account.
	url     string
	key     string
	account api.AccountID
}

// issueCardFields is how many fields the card's grid has
// (jira.Card.Rows: Assignee, Priority, Type, Reporter).
const issueCardFields = 4

// newIssueCard builds a hidden card of main window w, shown in parent (the
// main window or a message window). subject names the message the Change
// Status menu acts on when it opens (the message on display).
func newIssueCard(w *Window, parent *gtk.Window, subject func() (issueSubject, bool)) *issueCard {
	c := &issueCard{Box: gtk.NewBox(gtk.OrientationVertical, 6), win: w, parent: parent}
	c.AddCSSClass("card")
	c.AddCSSClass("issue-card")
	c.SetVisible(false)

	top := gtk.NewBox(gtk.OrientationHorizontal, 8)
	c.keyLabel = gtk.NewLabel("")
	c.keyLabel.SetUseMarkup(false)
	c.keyLabel.SetSelectable(true)
	c.keyLabel.AddCSSClass("title-4")
	c.keyLabel.AddCSSClass("numeric")
	c.keyText = gtk.NewLabel("")
	c.keyText.SetUseMarkup(false)
	c.keyText.AddCSSClass("title-4")
	c.keyText.AddCSSClass("numeric")
	c.keyButton = gtk.NewButton()
	c.keyButton.SetChild(c.keyText)
	c.keyButton.AddCSSClass("flat")
	c.keyButton.AddCSSClass("issue-key")
	c.keyButton.ConnectClicked(c.openKey)
	top.Append(c.keyButton)
	top.Append(c.keyLabel)

	c.statusText = gtk.NewLabel("")
	c.statusText.SetUseMarkup(false)
	c.statusText.SetEllipsize(pango.EllipsizeEnd)
	c.statusText.SetMaxWidthChars(24)
	c.statusSpinner = adw.NewSpinner()
	c.statusSpinner.SetVisible(false)
	arrow := gtk.NewImageFromIconName("pan-down-symbolic")
	inner := gtk.NewBox(gtk.OrientationHorizontal, 3)
	inner.Append(c.statusText)
	inner.Append(arrow)
	inner.Append(c.statusSpinner)
	c.status = gtk.NewMenuButton()
	c.status.SetChild(inner)
	c.status.SetVAlign(gtk.AlignCenter)
	c.status.AddCSSClass("issue-status-button")
	c.menu = newTransitionMenu(w, subject)
	c.status.SetPopover(c.menu.Popover)
	top.Append(c.status)

	c.plainStatus = widget.NewPill()
	top.Append(c.plainStatus)
	c.internal = widget.NewPill()
	top.Append(c.internal)
	c.via = dimCaption()
	c.edited = dimCaption()
	top.Append(c.via)
	top.Append(c.edited)
	c.Append(top)

	fields := adw.NewWrapBox()
	fields.SetChildSpacing(16)
	fields.SetLineSpacing(4)
	for range issueCardFields {
		label := dimCaption()
		label.SetVisible(true)
		value := gtk.NewLabel("")
		value.SetUseMarkup(false)
		value.AddCSSClass("caption")
		value.SetEllipsize(pango.EllipsizeEnd)
		value.SetMaxWidthChars(28)
		pair := gtk.NewBox(gtk.OrientationHorizontal, 4)
		pair.Append(label)
		pair.Append(value)
		fields.Append(pair)
		c.fieldPairs = append(c.fieldPairs, pair)
		c.fieldLabels = append(c.fieldLabels, label)
		c.fieldValues = append(c.fieldValues, value)
	}
	c.Append(fields)
	return c
}

// dimCaption is a hidden plain-text caption in the dimmed colour.
func dimCaption() *gtk.Label {
	l := gtk.NewLabel("")
	l.SetUseMarkup(false)
	l.SetEllipsize(pango.EllipsizeEnd)
	l.AddCSSClass("caption")
	l.AddCSSClass("dim-label")
	l.SetVisible(false)
	return l
}

// show displays the card of r for a message of account acc, or hides the
// card for none (a mail message). transitions: the account changes
// statuses, so the pill is the Change Status menu.
func (c *issueCard) show(r *issueReading, acc api.AccountID, transitions bool) {
	if r == nil {
		c.SetVisible(false)
		c.url, c.key, c.account = "", "", ""
		c.setBusy(false, "")
		return
	}
	card := r.card
	c.SetVisible(true)
	if c.key != card.Key || c.account != acc {
		// Another issue: its own transition, if any, is reported below.
		c.statusSpinner.SetVisible(false)
		c.status.SetSensitive(true)
	}
	c.key, c.account = card.Key, acc
	c.url = ""
	if r.openable {
		c.url = card.URL
	}
	c.keyText.SetText(card.Key)
	c.keyButton.SetTooltipText(card.OpenTooltip)
	c.keyButton.SetVisible(card.Key != "" && r.openable)
	c.keyLabel.SetText(card.Key)
	c.keyLabel.SetVisible(card.Key != "" && !r.openable)

	menu := transitions && card.Status != ""
	c.status.SetVisible(menu)
	c.statusText.SetText(card.Status)
	c.statusText.SetTooltipText(card.Status)
	c.status.SetTooltipText(jira.ChangeStatusLabel(i18n.Tr))
	setStatusButtonStyle(c.status, card.StatusStyle)
	widget.SetStatusPill(c.plainStatus, card.Status, card.StatusStyle)
	c.plainStatus.SetVisible(!menu && card.Status != "")

	widget.SetInternalPill(c.internal, card.InternalLabel)
	c.internal.SetVisible(card.Internal)
	c.via.SetText(card.Via)
	c.via.SetVisible(card.Via != "")
	c.edited.SetText(card.Edited)
	c.edited.SetVisible(card.Edited != "")

	for i, pair := range c.fieldPairs {
		if i >= len(card.Rows) {
			pair.SetVisible(false)
			continue
		}
		row := card.Rows[i]
		pair.SetVisible(true)
		c.fieldLabels[i].SetText(row.Label)
		c.fieldValues[i].SetText(row.Value)
		if row.Missing {
			c.fieldValues[i].AddCSSClass("dim-label")
			c.fieldValues[i].SetTooltipText("")
		} else {
			c.fieldValues[i].RemoveCSSClass("dim-label")
			c.fieldValues[i].SetTooltipText(row.Value)
		}
	}
	c.setBusy(c.win.issues.busy(acc, card.Key), card.Key)
}

// setStatusButtonStyle colours the pill's menu button like a status pill
// of style (the classes of widget/pill.go on the button).
func setStatusButtonStyle(b *gtk.MenuButton, style jira.StatusStyle) {
	for _, s := range []jira.StatusStyle{jira.StatusTodo, jira.StatusInProgress, jira.StatusDone} {
		b.RemoveCSSClass(string(s))
	}
	if style != jira.StatusPlain {
		b.AddCSSClass(string(style))
	}
}

// setBusy shows the spinner in the pill while a transition runs on the
// issue key (as the daemon names it) of the card's account; another
// issue's is ignored. The pill takes no click meanwhile.
func (c *issueCard) setBusy(busy bool, key string) {
	if c.key == "" || jira.Clean(key) != c.key {
		c.statusSpinner.SetVisible(false)
		c.status.SetSensitive(true)
		return
	}
	c.statusSpinner.SetVisible(busy)
	c.status.SetSensitive(!busy)
}

// popupStatus opens the Change Status menu (win.change-status), when the
// card offers it.
func (c *issueCard) popupStatus() {
	if c.Visible() && c.status.Visible() && c.status.Sensitive() {
		c.status.Popup()
	}
}

// openKey opens the issue in the browser: only a URL of the account's own
// site (show set url after jira.IsIssueURL), since it comes from the site
// and is hostile input like mail.
func (c *issueCard) openKey() {
	if c.url == "" {
		return
	}
	widget.LaunchURI(c.parent, c.url, func(err error) {
		if err != nil {
			c.win.log.Warn("open issue", "err", err)
			c.win.Toast(widget.LaunchErrorText(err))
		}
	})
}

// transitionMenu is the Change Status menu (jira.ChangeStatusLabel; the
// items and texts are ui/internal/jira/transitions.go): the popover of
// the issue card's status pill, which win.change-status and msg.change-
// status pop up too. The items are built when it opens: "Loading…" while
// issue.transitions runs (issueActions.load), then one item per transition,
// a transition that needs fields in Jira disabled with the hint under it,
// the failure as one disabled line; choosing an item performs it
// (issueActions.perform, which toasts and refreshes the cards). Closing the
// menu drops a late answer. Every title is text from the site, plain.
type transitionMenu struct {
	*gtk.Popover

	win     *Window
	subject func() (issueSubject, bool)
	list    *gtk.Box

	// loadedFor and loadedIssue are what the open menu shows: the subject
	// it was loaded for and the issue issue.transitions named (the
	// spinner's key when an item is chosen).
	loadedFor   issueSubject
	loadedIssue api.IssueInfo
}

func newTransitionMenu(w *Window, subject func() (issueSubject, bool)) *transitionMenu {
	m := &transitionMenu{Popover: gtk.NewPopover(), win: w, subject: subject}
	m.AddCSSClass("menu")
	m.AddCSSClass("transition-menu")
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	title := gtk.NewLabel(jira.ChangeStatusLabel(i18n.Tr))
	title.SetXAlign(0)
	title.AddCSSClass("heading")
	title.SetMarginStart(10)
	title.SetMarginEnd(10)
	title.SetMarginTop(4)
	title.SetMarginBottom(4)
	box.Append(title)
	m.list = gtk.NewBox(gtk.OrientationVertical, 0)
	box.Append(m.list)
	m.SetChild(box)
	m.ConnectShow(m.load)
	m.ConnectClosed(func() { w.issues.cancelLoad() })
	return m
}

// clear empties the items.
func (m *transitionMenu) clear() {
	for child := m.list.FirstChild(); child != nil; child = m.list.FirstChild() {
		m.list.Remove(child)
	}
}

// line adds one disabled line of text (loading, none, the failure).
func (m *transitionMenu) line(text string, spinner bool) {
	row := gtk.NewBox(gtk.OrientationHorizontal, 6)
	row.SetMarginStart(10)
	row.SetMarginEnd(10)
	row.SetMarginTop(6)
	row.SetMarginBottom(6)
	if spinner {
		row.Append(adw.NewSpinner())
	}
	l := gtk.NewLabel(text)
	l.SetUseMarkup(false)
	l.SetWrap(true)
	l.SetMaxWidthChars(40)
	l.SetXAlign(0)
	l.AddCSSClass("dim-label")
	row.Append(l)
	m.list.Append(row)
}

// load fills the menu for the message the subject names now.
func (m *transitionMenu) load() {
	m.clear()
	subject, ok := m.subject()
	if !ok {
		m.line(jira.NoTransitions(i18n.Tr), false)
		return
	}
	m.line(jira.TransitionsLoading(i18n.Tr), true)
	started := m.win.issues.load(subject, func(l loadedTransitions, err error) {
		m.clear()
		if err != nil {
			m.line(widget.RPCErrorText(jira.LoadTransitionsAction(i18n.Tr), err), false)
			return
		}
		m.loadedFor, m.loadedIssue = subject, l.Issue
		if len(l.Items) == 0 {
			m.line(jira.NoTransitions(i18n.Tr), false)
			return
		}
		for _, item := range l.Items {
			m.list.Append(m.itemButton(item))
		}
	})
	if !started {
		m.clear()
		m.line(jira.NoTransitions(i18n.Tr), false)
	}
}

// itemButton is the button of one transition: its name, and under it the
// status it leads to or why it is disabled.
func (m *transitionMenu) itemButton(item jira.TransitionItem) *gtk.Button {
	box := gtk.NewBox(gtk.OrientationVertical, 0)
	title := gtk.NewLabel(item.Title)
	title.SetUseMarkup(false)
	title.SetXAlign(0)
	title.SetEllipsize(pango.EllipsizeEnd)
	title.SetMaxWidthChars(40)
	box.Append(title)
	sub := item.Subtitle
	if !item.Enabled {
		sub = item.Hint
	}
	if sub != "" {
		l := gtk.NewLabel(sub)
		l.SetUseMarkup(false)
		l.SetXAlign(0)
		l.SetEllipsize(pango.EllipsizeEnd)
		l.SetMaxWidthChars(40)
		l.AddCSSClass("caption")
		l.AddCSSClass("dim-label")
		box.Append(l)
	}
	b := gtk.NewButton()
	b.SetChild(box)
	b.AddCSSClass("flat")
	b.SetSensitive(item.Enabled)
	if !item.Enabled {
		b.SetTooltipText(item.Hint)
	}
	b.ConnectClicked(func() {
		m.Popdown()
		// After the popover closed: its closing drops nothing a perform
		// needs, and the toast is not hidden behind it.
		glib.IdleAdd(func() {
			m.win.issues.perform(m.loadedFor, item, m.loadedIssue)
		})
	})
	return b
}
