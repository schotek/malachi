// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/assistant"
	"github.com/schotek/malachi/ui/internal/assistantpanel"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The assistant panel of the main window (ui/internal/assistant, the In App
// target; macOS AssistantPanelHost and AssistantPanelViewController): the
// right-hand side of window.blp's assistant_split, built from
// assistant_panel.blp, which renders an assistantpanel.Controller and
// sends the clicks back. It exists while the Assistant is shown and In App
// chosen (Assistant.panelShown); the message pane's toggle opens and folds
// it, and a message action of the Assistant menu opens it too.
//
// Everything the model or mail wrote reaches the screen as plain text
// (labels without markup) or as the text buffer the answers are drawn in
// from assistant.Markdown with tags only: never markup, never HTML. A link
// in an answer is opened only after "Open This Link?" has shown where it
// leads.
//
// The list's selection goes to the controller (followSelection, from
// onMessageRowSelected): one message, or a conversation row's folder
// members newest first (only its newest message until the members are
// known; the controller asks resolve). Never an Outbox message: it is not
// on the server yet.

// quickActions are the panel's buttons, in order.
var quickActions = []assistant.Action{assistant.Summarize, assistant.DraftReply, assistant.Tasks}

// glibLoop runs the panel's callbacks on the GTK main loop.
type glibLoop struct{}

func (glibLoop) Post(f func()) { glib.IdleAdd(f) }

func (glibLoop) After(d time.Duration, f func()) {
	glib.TimeoutAdd(uint(d.Milliseconds()), func() bool {
		f()
		return false
	})
}

// assistantDirectory is Claude Code's working directory: empty and private
// (created 0700 on demand).
func assistantDirectory() string {
	return filepath.Join(glib.GetUserCacheDir(), "malachi", "assistant")
}

// catalogLanguages are the English names of the languages po/LINGUAS
// ships, for the system prompt; a language added there belongs here too.
var catalogLanguages = map[string]string{"cs": "Czech"}

// uiLanguage is the English name of the language the UI shows: the first
// of the user's languages with a catalog, "" (English) otherwise.
func uiLanguage() string {
	for _, l := range glib.GetLanguageNames() {
		if name, ok := catalogLanguages[l]; ok {
			return name
		}
		if l == "C" || l == "en" || strings.HasPrefix(l, "en_") {
			return ""
		}
	}
	return ""
}

// transcriptRow is the widget of one item and how it follows its content.
type transcriptRow struct {
	kind   assistantpanel.ContentKind
	root   gtk.Widgetter
	update func(assistantpanel.Content)
}

// assistantPanel is the panel's widgets and its controller.
type assistantPanel struct {
	w      *Window
	ctl    *assistantpanel.Controller
	split  *adw.OverlaySplitView
	toggle *gtk.ToggleButton

	title        *adw.WindowTitle
	newButton    *gtk.Button
	chipIcon     *gtk.Image
	chipLabel    *gtk.Label
	chipRemove   *gtk.Button
	actions      []*gtk.Button
	bar          *gtk.Box
	scroller     *gtk.ScrolledWindow
	transcript   *gtk.Box
	rows         []*transcriptRow
	waiting      *adw.Spinner
	pending      *gtk.Box
	pendingLabel *gtk.Label
	input        *gtk.TextView
	placeholder  *gtk.Label
	send         *gtk.Button

	// followsEnd: the transcript was at its end, and stays there as items
	// arrive; a user who scrolled up to read is left where they are.
	followsEnd bool
}

// newAssistantPanel builds the panel into the main window's
// assistant_panel_bin (b is the main window's builder).
func newAssistantPanel(w *Window, b *gtk.Builder) *assistantPanel {
	pb := data.Builder("assistant_panel.ui")
	p := &assistantPanel{
		w:            w,
		split:        b.GetObject("assistant_split").Cast().(*adw.OverlaySplitView),
		toggle:       b.GetObject("assistant_panel_button").Cast().(*gtk.ToggleButton),
		title:        pb.GetObject("assistant_title").Cast().(*adw.WindowTitle),
		newButton:    pb.GetObject("assistant_new").Cast().(*gtk.Button),
		chipIcon:     pb.GetObject("assistant_chip_icon").Cast().(*gtk.Image),
		chipLabel:    pb.GetObject("assistant_chip_label").Cast().(*gtk.Label),
		chipRemove:   pb.GetObject("assistant_chip_remove").Cast().(*gtk.Button),
		bar:          pb.GetObject("assistant_bar").Cast().(*gtk.Box),
		scroller:     pb.GetObject("assistant_scroller").Cast().(*gtk.ScrolledWindow),
		transcript:   pb.GetObject("assistant_transcript").Cast().(*gtk.Box),
		waiting:      pb.GetObject("assistant_waiting").Cast().(*adw.Spinner),
		pending:      pb.GetObject("assistant_pending").Cast().(*gtk.Box),
		pendingLabel: pb.GetObject("assistant_pending_label").Cast().(*gtk.Label),
		input:        pb.GetObject("assistant_input").Cast().(*gtk.TextView),
		placeholder:  pb.GetObject("assistant_placeholder").Cast().(*gtk.Label),
		send:         pb.GetObject("assistant_send").Cast().(*gtk.Button),
		followsEnd:   true,
	}
	b.GetObject("assistant_panel_bin").Cast().(*adw.Bin).SetChild(pb.GetObject("assistant_panel").Cast().(*adw.ToolbarView))

	as := w.assist
	p.ctl = assistantpanel.New(assistantpanel.Config{
		Translator: tr, Settings: w.settings, Locator: as.locator, Loop: glibLoop{}, Log: w.log,
		Bridge: as.bridge, Socket: w.client.Socket, Directory: assistantDirectory(), Env: os.Environ(),
	})
	p.ctl.Language = uiLanguage
	p.ctl.Consent = p.askConsent
	p.ctl.ResolveContext = p.resolve
	p.ctl.OpenDraft = w.openSavedDraft
	p.ctl.OnChange = p.applyChange
	p.ctl.OnState = p.updateState
	p.ctl.OnFocusInput = func() {
		// After the panel has unfolded (a message action opened it).
		glib.IdleAdd(func() { p.input.GrabFocus() })
	}
	p.ctl.OnRestoreInput = func(text string) {
		if p.text() == "" {
			p.input.Buffer().SetText(text)
		}
	}

	texts, panel := assistant.Texts(tr), assistant.PanelTexts(tr)
	p.title.SetTitle(texts.Assistant)
	p.newButton.SetTooltipText(panel.NewConversation)
	p.newButton.ConnectClicked(p.ctl.NewConversation)
	p.chipRemove.SetTooltipText(i18n.T("Remove"))
	p.chipRemove.ConnectClicked(p.ctl.RemoveContext)
	actions := pb.GetObject("assistant_actions").Cast().(*adw.WrapBox)
	for _, a := range quickActions {
		a := a
		btn := gtk.NewButtonWithLabel(assistant.Label(tr, a))
		btn.AddCSSClass("chip-action")
		btn.ConnectClicked(func() { p.ctl.Run(a) })
		actions.Append(btn)
		p.actions = append(p.actions, btn)
	}
	pb.GetObject("assistant_bar_label").Cast().(*gtk.Label).SetText(panel.AnotherSelected)
	barNew := pb.GetObject("assistant_bar_new").Cast().(*gtk.Button)
	barNew.SetLabel(panel.NewConversation)
	barNew.ConnectClicked(p.ctl.NewConversation)
	barAdd := pb.GetObject("assistant_bar_add").Cast().(*gtk.Button)
	barAdd.SetLabel(panel.AddToConversation)
	barAdd.ConnectClicked(p.ctl.AddSelection)
	cancel := pb.GetObject("assistant_pending_cancel").Cast().(*gtk.Button)
	cancel.SetTooltipText(strings.ReplaceAll(i18n.T("_Cancel"), "_", ""))
	cancel.ConnectClicked(p.ctl.CancelPending)
	pb.GetObject("assistant_footer").Cast().(*gtk.Label).SetText(panel.Footer)
	p.send.ConnectClicked(p.sendOrStop)
	p.bindInput()
	p.bindScrolling()

	w.settings.OnChanged(settings.KeyAssistantModel, p.updateState)
	p.toggle.NotifyProperty("active", p.updateToggle)
	// The main window lives as long as the application: no unbinding.
	as.OnChange(p.syncShown)
	p.syncShown()
	p.updateToggle()
	p.updateState()
	return p
}

// close ends a running Claude Code for good (the application quits).
func (p *assistantPanel) close() {
	p.ctl.Close()
}

// syncShown shows the toggle while the panel may be shown, and folds the
// panel when it may not.
func (p *assistantPanel) syncShown() {
	shown := p.w.assist.panelShown()
	p.toggle.SetVisible(shown)
	if !shown {
		p.split.SetShowSidebar(false)
	}
}

func (p *assistantPanel) updateToggle() {
	t := assistant.PanelTexts(tr)
	if p.toggle.Active() {
		p.toggle.SetTooltipText(t.Hide)
	} else {
		p.toggle.SetTooltipText(t.Show)
	}
}

// reveal unfolds the panel, the main window brought forward.
func (p *assistantPanel) reveal() {
	p.w.Present()
	p.split.SetShowSidebar(true)
}

// The context

// messageContext is one message as the panel's context.
func messageContext(s api.MessageSummary) assistantpanel.Context {
	return assistantpanel.NewContext(assistant.Selection{AccountID: string(s.AccountID), MessageIDs: []string{string(s.ID)}},
		1, false, s.Subject, string(s.ThreadID))
}

// selectionContext is the list's selection as the panel's context; nil for
// none or an Outbox message.
func (p *assistantPanel) selectionContext() *assistantpanel.Context {
	row, ok := p.w.selectedRow()
	if !ok || p.w.model.inOutbox(row.Message) {
		return nil
	}
	if !row.Thread {
		c := messageContext(row.Message)
		return &c
	}
	// The conversation's subject is its newest member's without Re: and
	// Fwd:, as its row shows it.
	subject := row.Summary.Subject
	if subject == "" {
		subject = row.Message.Subject
	}
	thread := string(row.Summary.ID)
	if thread == "" {
		thread = string(row.Key.Thread)
	}
	acc := string(row.Message.AccountID)
	if ids := p.w.model.rowIDs(row); ids != nil {
		members := newestFirst(ids, true)
		c := assistantpanel.NewContext(assistant.Selection{AccountID: acc, MessageIDs: members}, len(members), false, subject, thread)
		return &c
	}
	// A folded conversation whose members are not known yet: its newest
	// message stands for it until they are asked for.
	c := assistantpanel.NewContext(assistant.Selection{AccountID: acc, MessageIDs: []string{string(row.Message.ID)}},
		max(row.Summary.MessageCount, 2), true, subject, thread)
	return &c
}

// followSelection is the list's selection changing: the panel follows it
// (or, once the conversation keeps its context, compares it with what the
// conversation is about).
func (p *assistantPanel) followSelection() {
	p.ctl.SetContext(p.selectionContext())
}

// resolve completes a folded conversation's members (newest first) when
// the list still shows it selected; otherwise the context stays what it
// was.
func (p *assistantPanel) resolve(c assistantpanel.Context, done func(assistant.Selection)) {
	row, ok := p.w.selectedRow()
	if !ok || !row.Thread || string(row.Message.AccountID) != c.Selection.AccountID ||
		len(c.Selection.MessageIDs) == 0 || c.Selection.MessageIDs[0] != string(row.Message.ID) {
		done(c.Selection)
		return
	}
	p.w.selectedIDs(func(r listRow, ids []api.MessageID) {
		done(assistant.Selection{AccountID: string(r.Message.AccountID), MessageIDs: newestFirst(ids, true)})
	})
}

// Running from the menus

// run is a message action of the Assistant menu on the list's selection.
func (p *assistantPanel) run(a assistant.Action) {
	c := p.selectionContext()
	if c == nil {
		return
	}
	p.reveal()
	p.ctl.RunOn(a, *c)
}

// runAbout is a message action from a message window: the main window
// comes forward and the action runs on the window's message; the list's
// selection stays.
func (p *assistantPanel) runAbout(a assistant.Action, s api.MessageSummary) {
	if p.w.model.inOutbox(s) {
		return
	}
	p.reveal()
	p.ctl.RunOn(a, messageContext(s))
}

// summarizeUnread is Summarize Unread in This Folder for folder k.
func (p *assistantPanel) summarizeUnread(k folderKey) {
	p.reveal()
	p.ctl.SummarizeUnread(string(k.Account), string(k.Folder))
}

// askAttachment is an attachment's question: the panel waits for the
// user's words.
func (p *assistantPanel) askAttachment(acc api.AccountID, id api.MessageID, a api.Attachment) {
	subject, thread := "", ""
	if s, ok := p.w.summary(id); ok {
		subject, thread = s.Subject, string(s.ThreadID)
	}
	p.reveal()
	p.ctl.AskAttachment(string(acc), string(id), a.PartID, subject, thread)
}

// askConsent asks "Send Mail to Claude?" before the first question ever.
func (p *assistantPanel) askConsent(done func(bool)) {
	widget.AskAssistantConsent(p.w, done)
}

// State

// updateState brings everything but the transcript's rows in line with the
// controller, the spinner below them among it.
func (p *assistantPanel) updateState() {
	c := p.ctl
	p.title.SetSubtitle(c.Subtitle())
	p.chipLabel.SetText(c.ContextLabel())
	p.chipLabel.SetTooltipText(c.ContextLabel())
	if c.IsPinned() {
		icon := "view-list-symbolic"
		if pinned := c.Pinned(); len(pinned) == 1 {
			icon = contextIcon(pinned[0].Context)
		}
		p.chipIcon.SetFromIconName(icon)
		p.chipRemove.SetVisible(false)
	} else {
		p.chipIcon.SetFromIconName(contextIcon(c.EffectiveContext()))
		p.chipRemove.SetVisible(c.EffectiveContext() != nil)
	}
	p.bar.SetVisible(c.AnotherSelected())
	for _, b := range p.actions {
		b.SetSensitive(c.CanRunActions())
	}
	label := c.PendingLabel()
	p.pendingLabel.SetText(label)
	p.pending.SetVisible(label != "")
	p.placeholder.SetText(c.Placeholder())
	p.newButton.SetSensitive(!c.Closed() && (len(c.Items()) > 0 || c.Running() || c.Pending().Kind != assistantpanel.PendingNone || c.IsPinned()))
	p.waiting.SetVisible(c.Waiting())
	p.updateSend()
}

// contextIcon is the chip's icon of one context: all mail, a conversation,
// a message.
func contextIcon(c *assistantpanel.Context) string {
	switch {
	case c == nil:
		return "mail-inbox-symbolic"
	case c.Conversation():
		return "mail-message-new-symbolic"
	}
	return "mail-unread-symbolic"
}

// updateSend: Send, or Stop while an answer comes.
func (p *assistantPanel) updateSend() {
	t := assistant.PanelTexts(tr)
	c := p.ctl
	if c.Running() {
		p.send.SetLabel(t.Stop)
		p.send.SetSensitive(!c.Closed())
		p.send.RemoveCSSClass("suggested-action")
		return
	}
	p.send.SetLabel(i18n.T("_Send"))
	p.send.AddCSSClass("suggested-action")
	draft := c.Pending() == assistantpanel.Pending{Kind: assistantpanel.PendingAction, Action: assistant.DraftReply}
	p.send.SetSensitive(!c.Closed() && (strings.TrimSpace(p.text()) != "" || draft))
}

// The question field

func (p *assistantPanel) text() string {
	buf := p.input.Buffer()
	return buf.Text(buf.StartIter(), buf.EndIter(), false)
}

// bindInput: Return sends, Shift-Return starts a new line, Escape drops a
// waiting message action; the placeholder shows while the field is empty.
func (p *assistantPanel) bindInput() {
	buf := p.input.Buffer()
	buf.ConnectChanged(func() {
		p.placeholder.SetVisible(buf.CharCount() == 0)
		p.updateSend()
	})
	keys := gtk.NewEventControllerKey()
	keys.SetPropagationPhase(gtk.PhaseCapture)
	keys.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		switch keyval {
		case gdk.KEY_Return, gdk.KEY_KP_Enter, gdk.KEY_ISO_Enter:
			if state&(gdk.ShiftMask|gdk.AltMask) != 0 {
				return false
			}
			p.submit()
			return true
		case gdk.KEY_Escape:
			if p.ctl.Pending().Kind != assistantpanel.PendingNone {
				p.ctl.CancelPending()
				return true
			}
		}
		return false
	})
	p.input.AddController(keys)
}

// submit: the controller takes the text (the field empties) or leaves it
// (a question under way, nothing typed).
func (p *assistantPanel) submit() {
	if p.ctl.Submit(p.text()) {
		p.input.Buffer().SetText("")
	}
}

func (p *assistantPanel) sendOrStop() {
	if p.ctl.Running() {
		p.ctl.Stop()
		return
	}
	p.submit()
}

// The transcript

// applyChange brings the rows in line with the controller's items.
func (p *assistantPanel) applyChange(ch assistantpanel.Change) {
	items := p.ctl.Items()
	switch ch.Kind {
	case assistantpanel.ChangeReset:
		p.rebuild(items)
		p.followsEnd = true
	case assistantpanel.ChangeAppended:
		if ch.Index == len(p.rows) && ch.Index < len(items) {
			p.appendRow(items[ch.Index])
		} else {
			p.rebuild(items)
		}
	case assistantpanel.ChangeUpdated:
		if ch.Index >= len(items) || len(p.rows) != len(items) {
			p.rebuild(items)
			break
		}
		it := items[ch.Index]
		if r := p.rows[ch.Index]; r.kind == it.Content.Kind {
			r.update(it.Content)
		} else {
			p.replaceRow(ch.Index, it)
		}
	}
	p.updateState()
	p.stickToEnd()
}

func (p *assistantPanel) rebuild(items []assistantpanel.Item) {
	for _, r := range p.rows {
		p.transcript.Remove(r.root)
	}
	p.rows = nil
	for _, it := range items {
		p.appendRow(it)
	}
}

func (p *assistantPanel) appendRow(it assistantpanel.Item) {
	r := p.makeRow(it)
	p.transcript.Append(r.root)
	p.rows = append(p.rows, r)
}

func (p *assistantPanel) replaceRow(i int, it assistantpanel.Item) {
	r := p.makeRow(it)
	var before gtk.Widgetter
	if i > 0 {
		before = p.rows[i-1].root
	}
	p.transcript.Remove(p.rows[i].root)
	p.transcript.InsertChildAfter(r.root, before)
	p.rows[i] = r
}

// bindScrolling: the transcript follows its end while the user has not
// scrolled up.
func (p *assistantPanel) bindScrolling() {
	adj := p.scroller.VAdjustment()
	adj.ConnectValueChanged(func() {
		p.followsEnd = adj.Upper()-(adj.Value()+adj.PageSize()) <= 24
	})
	adj.NotifyProperty("upper", func() {
		if p.followsEnd {
			adj.SetValue(adj.Upper() - adj.PageSize())
		}
	})
}

// stickToEnd scrolls to the end after the layout, while following it.
func (p *assistantPanel) stickToEnd() {
	if !p.followsEnd {
		return
	}
	adj := p.scroller.VAdjustment()
	adj.SetValue(adj.Upper() - adj.PageSize())
}

// makeRow is the widget of an item.
func (p *assistantPanel) makeRow(it assistantpanel.Item) *transcriptRow {
	id := it.ID
	var r *transcriptRow
	switch it.Content.Kind {
	case assistantpanel.ContentUser:
		r = userRow()
	case assistantpanel.ContentAssistant:
		r = p.answerRow()
	case assistantpanel.ContentActivity:
		r = activityRow()
	case assistantpanel.ContentDraft:
		r = draftRow(func() { p.ctl.OpenDraftItem(id) })
	default: // ContentError, ContentNote
		r = lineRow(func() { p.ctl.Retry(id) }, func(o assistantpanel.Offer) { p.offered(id, o) })
	}
	r.kind = it.Content.Kind
	r.update(it.Content)
	return r
}

// plainLabel is a wrapping label for model or mail text: never markup.
func plainLabel(classes ...string) *gtk.Label {
	l := gtk.NewLabel("")
	l.SetUseMarkup(false)
	l.SetWrap(true)
	l.SetWrapMode(pango.WrapWordChar)
	l.SetXAlign(0)
	for _, c := range classes {
		l.AddCSSClass(c)
	}
	return l
}

// userRow is the user's question: a tinted bubble with the action's label
// over the typed text, indented from the leading edge.
func userRow() *transcriptRow {
	box := gtk.NewBox(gtk.OrientationVertical, 2)
	box.AddCSSClass("assistant-user")
	box.SetMarginStart(28)
	label := plainLabel("caption", "dim-label")
	text := plainLabel()
	text.SetSelectable(true)
	box.Append(label)
	box.Append(text)
	return &transcriptRow{root: box, update: func(c assistantpanel.Content) {
		label.SetText(c.Label)
		label.SetVisible(c.Label != "")
		text.SetText(c.Text)
		text.SetVisible(c.Text != "")
	}}
}

// activityRow is a tool at work: a spinner, then a check mark, beside its
// label.
func activityRow() *transcriptRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	spinner := adw.NewSpinner()
	check := gtk.NewImageFromIconName("object-select-symbolic")
	check.AddCSSClass("dim-label")
	label := plainLabel("caption", "dim-label")
	label.SetHExpand(true)
	box.Append(spinner)
	box.Append(check)
	box.Append(label)
	return &transcriptRow{root: box, update: func(c assistantpanel.Content) {
		label.SetText(c.Label)
		spinner.SetVisible(!c.Done)
		check.SetVisible(c.Done)
	}}
}

// draftRow is a draft the bridge saved: "A draft is ready" with Open Draft.
func draftRow(open func()) *transcriptRow {
	t := assistant.PanelTexts(tr)
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.AddCSSClass("assistant-card")
	icon := gtk.NewImageFromIconName("document-edit-symbolic")
	label := plainLabel()
	label.SetText(t.DraftReady)
	label.SetHExpand(true)
	button := gtk.NewButtonWithLabel(t.OpenDraft)
	button.AddCSSClass("chip-action")
	button.SetVAlign(gtk.AlignCenter)
	button.ConnectClicked(open)
	box.Append(icon)
	box.Append(label)
	box.Append(button)
	return &transcriptRow{root: box, update: func(assistantpanel.Content) {}}
}

// lineRow is an error (with Try Again when the question can be sent once
// more, and the button of what it offers: Sign In… or Get Claude Code…) or
// a note.
func lineRow(retry func(), offer func(assistantpanel.Offer)) *transcriptRow {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	icon := gtk.NewImageFromIconName("dialog-warning-symbolic")
	icon.SetVAlign(gtk.AlignStart)
	icon.AddCSSClass("error")
	column := gtk.NewBox(gtk.OrientationVertical, 4)
	column.SetHExpand(true)
	label := plainLabel()
	button := gtk.NewButtonWithLabel(i18n.T("Try Again"))
	button.AddCSSClass("chip-action")
	button.ConnectClicked(retry)
	offered := assistantpanel.OfferNone
	other := gtk.NewButtonWithLabel("")
	other.AddCSSClass("chip-action")
	other.ConnectClicked(func() { offer(offered) })
	buttons := gtk.NewBox(gtk.OrientationHorizontal, 6)
	buttons.SetHAlign(gtk.AlignStart)
	buttons.Append(other)
	buttons.Append(button)
	column.Append(label)
	column.Append(buttons)
	box.Append(icon)
	box.Append(column)
	return &transcriptRow{root: box, update: func(c assistantpanel.Content) {
		label.SetText(c.Text)
		if c.Kind == assistantpanel.ContentError {
			label.RemoveCSSClass("dim-label")
			label.AddCSSClass("error")
			icon.SetVisible(true)
			offered = c.Offer
			other.SetLabel(offerLabel(c.Offer))
			other.SetVisible(c.Offer != assistantpanel.OfferNone)
			button.SetVisible(c.Retry)
			buttons.SetVisible(c.Retry || c.Offer != assistantpanel.OfferNone)
			return
		}
		label.RemoveCSSClass("error")
		label.AddCSSClass("dim-label")
		icon.SetVisible(false)
		buttons.SetVisible(false)
	}}
}

// offerLabel is the button of what an error line offers.
func offerLabel(o assistantpanel.Offer) string {
	t := assistant.SignInTexts(tr)
	switch o {
	case assistantpanel.OfferSignIn:
		return t.SignIn
	case assistantpanel.OfferInstall:
		return t.GetClaudeCode
	}
	return ""
}

// offered is the other button of an error line: Sign In… runs Claude
// Code's own sign-in and sends the question again (the controller's),
// Get Claude Code… opens Anthropic's page with the installers.
func (p *assistantPanel) offered(id int, o assistantpanel.Offer) {
	switch o {
	case assistantpanel.OfferSignIn:
		p.ctl.SignIn(id)
	case assistantpanel.OfferInstall:
		p.w.launchURI(&p.w.ApplicationWindow.Window, assistant.InstallURL)
	}
}

// answerRow is an answer: a read-only text view that draws its Markdown
// with tags. A click on a link goes through the confirmation.
func (p *assistantPanel) answerRow() *transcriptRow {
	tv := gtk.NewTextView()
	tv.SetEditable(false)
	tv.SetCursorVisible(false)
	tv.SetWrapMode(gtk.WrapWordChar)
	tv.AddCSSClass("assistant-answer")
	a := newAnswerView(tv)
	click := gtk.NewGestureClick()
	click.ConnectReleased(func(n int, x, y float64) {
		if n != 1 || tv.Buffer().HasSelection() {
			return
		}
		if href := a.linkAt(x, y); href != "" {
			p.w.openAssistantAnswerLink(href)
		}
	})
	tv.AddController(click)
	motion := gtk.NewEventControllerMotion()
	motion.ConnectMotion(func(x, y float64) {
		href := a.linkAt(x, y)
		if href == a.hovered {
			return
		}
		a.hovered = href
		if href != "" {
			// The destination before any click, as plain text.
			tv.SetCursorFromName("pointer")
			tv.SetTooltipText(href)
		} else {
			tv.SetCursorFromName("text")
			tv.SetTooltipText("")
		}
	})
	tv.AddController(motion)
	shown := ""
	return &transcriptRow{root: tv, update: func(c assistantpanel.Content) {
		if c.Text == shown {
			return
		}
		shown = c.Text
		a.render(c.Text)
	}}
}

// answerView draws the Markdown subset of an answer into a text buffer:
// tags for the block kinds and the spans (fonts, sizes, margins, a tint
// for code, the accent colour for links), never markup. Each link has a
// tag of its own, whose name leads to its URL.
type answerView struct {
	tv      *gtk.TextView
	buf     *gtk.TextBuffer
	tags    map[string]*gtk.TextTag
	links   map[string]string
	linkTag []*gtk.TextTag
	hovered string
}

// listIndent is the indentation of one list level, in pixels.
const listIndent = 14

// lineSeparator joins the lines of one block without starting a new
// paragraph (macOS AssistantMarkdownRenderer.lineSeparator): a list
// item's next line, a table row's "header: cell" among them, starts under
// its text, not under its marker, and the block's spacing comes once, after
// it. A code block keeps "\n", so that copied code has plain newlines.
const lineSeparator = "\u2028"

func newAnswerView(tv *gtk.TextView) *answerView {
	a := &answerView{tv: tv, buf: tv.Buffer(), tags: map[string]*gtk.TextTag{}, links: map[string]string{}}
	table := a.buf.TagTable()
	add := func(name string, props map[string]any) {
		t := gtk.NewTextTag(name)
		for k, v := range props {
			t.SetObjectProperty(k, v)
		}
		table.Add(t)
		a.tags[name] = t
	}
	add("para", map[string]any{"pixels-below-lines": int32(6)})
	add("h1", map[string]any{"weight": int32(700), "scale": 1.3, "pixels-above-lines": int32(4), "pixels-below-lines": int32(4)})
	add("h2", map[string]any{"weight": int32(700), "scale": 1.15, "pixels-above-lines": int32(4), "pixels-below-lines": int32(4)})
	add("h3", map[string]any{"weight": int32(700), "pixels-above-lines": int32(4), "pixels-below-lines": int32(4)})
	for level := range 4 {
		add(fmt.Sprintf("list%d", level), map[string]any{
			"left-margin":        int32((level + 1) * listIndent),
			"indent":             int32(-listIndent + 2),
			"pixels-below-lines": int32(3),
		})
	}
	add("codeblock", map[string]any{"family": "monospace", "left-margin": int32(6), "paragraph-background": "rgba(127,127,127,0.12)"})
	add("bold", map[string]any{"weight": int32(700)})
	add("italic", map[string]any{"font": "Italic"})
	add("code", map[string]any{"family": "monospace", "background": "rgba(127,127,127,0.15)"})
	return a
}

// linkAt is the URL of the link under (x, y) of the view, "" for none.
func (a *answerView) linkAt(x, y float64) string {
	bx, by := a.tv.WindowToBufferCoords(gtk.TextWindowWidget, int(x), int(y))
	iter, ok := a.tv.IterAtLocation(bx, by)
	if !ok {
		return ""
	}
	for _, t := range iter.Tags() {
		if name, ok := t.ObjectProperty("name").(string); ok {
			if href, ok := a.links[name]; ok {
				return href
			}
		}
	}
	return ""
}

// render draws markdown anew.
func (a *answerView) render(markdown string) {
	table := a.buf.TagTable()
	for _, t := range a.linkTag {
		table.Remove(t)
	}
	a.linkTag = nil
	a.links = map[string]string{}
	a.hovered = ""
	a.buf.SetText("")
	accent := linkColor()
	for i, b := range assistant.Markdown(markdown) {
		if i > 0 {
			a.insert("\n")
		}
		var block []*gtk.TextTag
		prefix := ""
		switch b.Kind {
		case assistant.BlockHeading:
			block = append(block, a.tags[fmt.Sprintf("h%d", min(max(b.Level, 1), 3))])
		case assistant.BlockBullet:
			block = append(block, a.tags[fmt.Sprintf("list%d", min(b.Level, 3))])
			prefix = "•  "
		case assistant.BlockNumbered:
			block = append(block, a.tags[fmt.Sprintf("list%d", min(b.Level, 3))])
			prefix = fmt.Sprintf("%d. ", b.Number)
		case assistant.BlockCode:
			block = append(block, a.tags["codeblock"])
		default:
			block = append(block, a.tags["para"])
		}
		a.insert(prefix, block...)
		for _, s := range b.Spans {
			tags := append([]*gtk.TextTag(nil), block...)
			if s.Bold {
				tags = append(tags, a.tags["bold"])
			}
			if s.Italic {
				tags = append(tags, a.tags["italic"])
			}
			if s.Code && b.Kind != assistant.BlockCode {
				tags = append(tags, a.tags["code"])
			}
			if s.Link != "" {
				name := fmt.Sprintf("link%d", len(a.linkTag))
				t := gtk.NewTextTag(name)
				if accent != "" {
					t.SetObjectProperty("foreground", accent)
				}
				table.Add(t)
				a.linkTag = append(a.linkTag, t)
				a.links[name] = s.Link
				tags = append(tags, t)
			}
			text := s.Text
			if b.Kind != assistant.BlockCode {
				text = strings.ReplaceAll(text, "\n", lineSeparator)
			}
			a.insert(text, tags...)
		}
	}
}

// insert appends text with tags.
func (a *answerView) insert(text string, tags ...*gtk.TextTag) {
	if text == "" {
		return
	}
	start := a.buf.CharCount()
	a.buf.Insert(a.buf.EndIter(), text)
	s, e := a.buf.IterAtOffset(start), a.buf.EndIter()
	for _, t := range tags {
		if t != nil {
			a.buf.ApplyTag(t, s, e)
		}
	}
}

// linkColor is the accent colour links are drawn in, "" when libadwaita
// does not say.
func linkColor() string {
	if c := adw.StyleManagerGetDefault().AccentColorRGBA(); c != nil {
		return c.String()
	}
	return ""
}

// Opening a saved draft

// draftListPageLimit and draftListMaxPages bound one Open Draft's
// draft.list.
const (
	draftListPageLimit = 500
	draftListMaxPages  = 20
)

// openSavedDraft is a draft card's Open Draft (macOS
// ActionsController.openSavedDraft): the draft Claude Code saved through
// the bridge's create_draft opens in the compose window, or the window
// already editing it comes to the front. The ids come from the bridge's own
// line of the tool result, so the draft is looked up with draft.list first
// (pages of 500, the cursor followed), and only a draft the daemon lists
// is opened; one that is gone (sent, deleted, or never there) says so. A
// second request while one runs does nothing.
func (w *Window) openSavedDraft(ref assistant.DraftRef) {
	id := api.DraftID(ref.DraftID)
	if id == "" || w.findingDrafts[id] {
		return
	}
	w.findingDrafts[id] = true
	acc := api.AccountID(ref.AccountID)
	go func() {
		d, err := w.findDraft(acc, id)
		glib.IdleAdd(func() {
			delete(w.findingDrafts, id)
			switch {
			case err != nil:
				w.log.Warn("draft.list", "err", err)
				w.Toast(widget.RPCErrorText(i18n.T("Opening the draft"), err))
			case d == nil:
				w.Toast(assistant.PanelTexts(tr).DraftGone)
			default:
				if cw := w.compose.FindDraft(*d); cw != nil {
					cw.Present()
					return
				}
				w.compose.Open(compose.FromDraft(compose.KindEdit, *d, api.BlockedContent{}))
			}
		})
	}()
}

// findDraft pages draft.list of acc for draft id: the draft, nil when the
// last page (or the page limit) came without it, or the error. Off the
// main loop.
func (w *Window) findDraft(acc api.AccountID, id api.DraftID) (*api.Draft, error) {
	cursor := ""
	for range draftListMaxPages {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		var res api.DraftListResult
		err := w.client.Call(ctx, api.MethodDraftList, api.DraftListParams{AccountID: acc, Page: api.Page{Cursor: cursor, Limit: draftListPageLimit}}, &res)
		cancel()
		if err != nil {
			return nil, err
		}
		for i := range res.Drafts {
			if res.Drafts[i].ID == id {
				return &res.Drafts[i], nil
			}
		}
		next := res.Page.NextCursor
		if next == "" || next == cursor {
			return nil, nil
		}
		cursor = next
	}
	return nil, nil
}

// The settings store is what the panel reads its preferences from.
var _ assistantpanel.Settings = (*settings.Store)(nil)
