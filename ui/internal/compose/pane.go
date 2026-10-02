// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/editor"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Layout is a pane's visual layout (Options.Layout, ComposePane.Options.Layout).
type Layout int

const (
	// LayoutWindow: the compose window's content as it always was. The
	// window keeps its toolbar, its title, the close question, Escape and
	// the assistant's rewrite; Send and Ctrl+S reach the pane from the
	// window's own ShortcutController and action-name buttons.
	LayoutWindow Layout = iota
	// LayoutInline: a reply edited in place in the board's case detail.
	// No From row (the reply is from-locked), the editor in its sized
	// mode (it takes the height of its document between a minimum and a
	// cap, then scrolls inside), and a footer row of its own with Attach,
	// the status line, Discard and Send. Ctrl+Enter and Ctrl+S reach the
	// pane only while the keyboard focus is inside it.
	LayoutInline
)

// PaneOptions is how a pane is built (ComposePane.Options).
type PaneOptions struct {
	Layout Layout
	// Owner is who keeps the draft (draftController.owner); see Owner.
	Owner Owner
}

// EndKind is how a pane ended (ComposePane.End without the widgets).
type EndKind int

// The kinds.
const (
	// EndSent: Text is the confirmation ("Message queued for sending").
	EndSent EndKind = iota
	EndDiscarded
	EndClosed
)

// End is how a pane ended (OnEnd).
type End struct {
	Kind EndKind
	// Text is set for EndSent.
	Text string
}

// Pane is the content of a compose window (compose_pane.blp): the card of
// header fields (or the comment's), the formatting bar, the editor, the
// attachment chips, the status line, the draft behind them
// (draftController over composeForm, below) and the compose and Format
// actions. compose.Window embeds one for LayoutWindow/OwnerWindow; the
// board embeds one for LayoutInline/OwnerBoard in its case detail.
//
// Hooks (OnHeight, OnTitle, OnSendEnabled, OnToast, OnEnd, OnLost,
// DiscardStored) are how the pane talks back to whatever hosts it,
// mirroring the macOS ComposePaneHost protocol and
// ComposeDraftController's own fields: a host never reaches into the
// pane's widgets directly.
type Pane struct {
	root *gtk.Box // pane_root

	m       *Manager
	log     *slog.Logger
	params  Params
	options PaneOptions

	fromBox       *gtk.Box
	fromSeparator *gtk.Separator
	from          *gtk.DropDown
	to, cc, bcc   *recipientField
	subjectEntry  *gtk.Entry

	// The Cc and Bcc lines start hidden; the button reveals them, and
	// each carries the separator above it.
	ccBcc         *gtk.Button
	ccBox, bccBox *gtk.Box
	ccSep, bccSep *gtk.Separator

	// The comment mode (comment.go) shows commentHeader in place of
	// headerRows: the issue's key and summary and, on a service-desk
	// request, the choice of who reads the comment (visibilityGroup, one
	// toggle per commentOptions).
	headerRows      *gtk.Box
	commentHeader   *gtk.Box
	commentTitle    *gtk.Label
	commentSummary  *gtk.Label
	visibilityGroup *adw.ToggleGroup
	commentOptions  []jira.VisibilityOption

	editorSlot *gtk.Box
	attBox     *gtk.FlowBox
	status     *gtk.Label
	toolbar    *gtk.Box
	plainHint  *gtk.Label

	bold, italic, underline *gtk.ToggleButton
	ul, ol, quote           *gtk.ToggleButton
	blockButton             *gtk.MenuButton
	alignButton             *gtk.MenuButton
	linkPopover             *gtk.Popover
	linkEntry               *gtk.Entry
	linkApply               *gtk.Button
	colorButton             *gtk.ColorDialogButton
	clearButton             *gtk.Button

	// LayoutInline only: the footer's own Attach, Discard and Send
	// buttons (footerRow).
	footerAttach  *gtk.Button
	footerDiscard *gtk.Button
	footerSend    *gtk.Button

	editor      *editor.Editor
	group       *gio.SimpleActionGroup
	actions     map[string]*gio.SimpleAction
	blockAction *gio.SimpleAction
	alignAction *gio.SimpleAction
	syncing     bool // toolbar being updated from the page, not by the user

	accounts []api.Account
	// chosenAccount is the identity the user picked in From; until they
	// do, params.AccountID is what From shows (also after the account
	// list arrives, replacing the placeholder). settingFrom marks the
	// pane's own changes of the row, which are no choice.
	chosenAccount  api.AccountID
	settingFrom    bool
	attachmentList []api.DraftAttachment
	chips          map[string]gtk.Widgetter
	// suggest is the recipient completion of the To, Cc and Bcc rows.
	suggest []*suggestions

	dc *draftController

	// flushed remembers the content flushEditor last reported, for
	// editorChanged's echo check.
	flushed flushEcho
	// sentText is the draft controller's onSent confirmation, for
	// closeForm to tell EndSent apart from EndDiscarded/EndClosed.
	sentText string

	// titleText is the pane's title (the subject, or "New Message"; a
	// comment names its issue): ReplyTitle, and OnTitle when it changes.
	titleText string

	// LayoutInline only: the sized editor (pane_layout.go).
	visibleHeight     float64
	contentHeight     float64
	haveContentHeight bool
	editorHeightSet   bool
	appliedHeight     float64

	ended bool // OnEnd fired once (End)

	// Hooks: how the pane talks back to its host. Set them before Present
	// (window) or before the pane is shown (board).
	//
	// OnHeight: LayoutInline only, the editor's clamped height changed.
	OnHeight func(height float64)
	// OnTitle: the pane's title changed (the subject was edited, or the
	// comment's issue is now known).
	OnTitle func(title string)
	// OnSendEnabled: the draft controller enabled or disabled Send.
	OnSendEnabled func(enabled bool)
	// OnToast: a short message for the user.
	OnToast func(text string)
	// OnEnd: the draft controller decided the pane goes (sent, discarded,
	// or closed after the close question, window owner only).
	OnEnd func(End)
	// OnLost: OwnerBoard only, the draft was deleted elsewhere.
	OnLost func()
	// OnSendFailed reports a completed, failed send to the board host.
	OnSendFailed func()
	// DiscardStored: OwnerBoard only, Discard deletes the stored draft
	// through this instead of a plain draft.delete (the board's
	// board.discardDraft, which also unlinks it from the case).
	DiscardStored func(accountID api.AccountID, draftID api.DraftID, done func(err error))

	// dialogParent is the window for confirmation and file dialogs,
	// supplied by either host before showing the pane.
	dialogParent *gtk.Window

	tornDown bool
}

// recipientFieldFrom binds the recipient row id ("to", "cc", "bcc") of the
// builder: its scrolled wrap box and label (compose_pane.blp).
func recipientFieldFrom(b *gtk.Builder, id string) *recipientField {
	return newRecipientField(
		b.GetObject(id+"_scroll").Cast().(*gtk.ScrolledWindow),
		b.GetObject(id+"_row").Cast().(*adw.WrapBox),
		b.GetObject(id+"_label").Cast().(*gtk.Label),
		i18n.T("Remove"))
}

// NewPane builds and prefills a pane from compose_pane.blp. The window
// (compose.go) presents it inside its own chrome; the board
// puts its widget into the case detail and feeds it a visible height
// (SetVisibleHeight) for the sized editor.
func NewPane(m *Manager, p Params, opts PaneOptions) *Pane {
	b := data.Builder("compose_pane.ui")
	pn := &Pane{
		m:             m,
		log:           m.log,
		params:        p,
		options:       opts,
		root:          b.GetObject("pane_root").Cast().(*gtk.Box),
		fromBox:       b.GetObject("from_box").Cast().(*gtk.Box),
		fromSeparator: b.GetObject("from_separator").Cast().(*gtk.Separator),
		from:          b.GetObject("from_row").Cast().(*gtk.DropDown),
		to:            recipientFieldFrom(b, "to"),
		cc:            recipientFieldFrom(b, "cc"),
		bcc:           recipientFieldFrom(b, "bcc"),
		subjectEntry:  b.GetObject("subject_row").Cast().(*gtk.Entry),
		ccBcc:         b.GetObject("cc_bcc_button").Cast().(*gtk.Button),
		ccBox:         b.GetObject("cc_box").Cast().(*gtk.Box),
		bccBox:        b.GetObject("bcc_box").Cast().(*gtk.Box),
		ccSep:         b.GetObject("cc_separator").Cast().(*gtk.Separator),
		bccSep:        b.GetObject("bcc_separator").Cast().(*gtk.Separator),
		headerRows:    b.GetObject("header_rows").Cast().(*gtk.Box),
		editorSlot:    b.GetObject("editor_slot").Cast().(*gtk.Box),
		attBox:        b.GetObject("attachments_box").Cast().(*gtk.FlowBox),
		status:        b.GetObject("draft_status").Cast().(*gtk.Label),
		toolbar:       b.GetObject("format_toolbar").Cast().(*gtk.Box),
		plainHint:     b.GetObject("plain_text_hint").Cast().(*gtk.Label),
		bold:          b.GetObject("bold_button").Cast().(*gtk.ToggleButton),
		italic:        b.GetObject("italic_button").Cast().(*gtk.ToggleButton),
		underline:     b.GetObject("underline_button").Cast().(*gtk.ToggleButton),
		ul:            b.GetObject("ul_button").Cast().(*gtk.ToggleButton),
		ol:            b.GetObject("ol_button").Cast().(*gtk.ToggleButton),
		quote:         b.GetObject("quote_button").Cast().(*gtk.ToggleButton),
		blockButton:   b.GetObject("block_button").Cast().(*gtk.MenuButton),
		alignButton:   b.GetObject("align_button").Cast().(*gtk.MenuButton),
		linkPopover:   b.GetObject("link_popover").Cast().(*gtk.Popover),
		linkEntry:     b.GetObject("link_entry").Cast().(*gtk.Entry),
		linkApply:     b.GetObject("link_apply").Cast().(*gtk.Button),
		colorButton:   b.GetObject("color_button").Cast().(*gtk.ColorDialogButton),
		clearButton:   b.GetObject("clear_button").Cast().(*gtk.Button),
		actions:       make(map[string]*gio.SimpleAction),
		chips:         make(map[string]gtk.Widgetter),
	}
	pn.commentHeader = b.GetObject("comment_header").Cast().(*gtk.Box)
	pn.commentTitle = b.GetObject("comment_title").Cast().(*gtk.Label)
	pn.commentSummary = b.GetObject("comment_summary").Cast().(*gtk.Label)
	pn.visibilityGroup = b.GetObject("comment_visibility").Cast().(*adw.ToggleGroup)

	pn.dc = newDraftController(opts.Owner, &realCaller{m: m}, pn.log)
	pn.dc.form = pn
	pn.dc.loop = glibLoop{}
	pn.dc.confirmDelete = func() bool { return m.settings.ConfirmDelete() }
	pn.dc.placeholder = m.Placeholder
	pn.dc.ctxFn = pn.ctx
	pn.dc.unregisterCID = editor.UnregisterCID
	pn.dc.onSent = pn.handleSent
	pn.dc.onSendFailed = func() {
		if pn.OnSendFailed != nil {
			pn.OnSendFailed()
		}
	}
	pn.dc.onLost = func() {
		if pn.OnLost != nil {
			pn.OnLost()
		}
	}
	pn.dc.discardStored = func(accountID api.AccountID, draftID api.DraftID, done func(err error)) {
		if pn.DiscardStored != nil {
			pn.DiscardStored(accountID, draftID, done)
			return
		}
		done(errNoDiscardHost)
	}
	pn.dc.confirmDiscard = func(heading, body, label string, proceed func()) {
		widget.ConfirmDestructive(pn.dialogParent, heading, body, label, proceed)
	}
	if opts.Layout == LayoutWindow {
		pn.dc.saveDraftQuestion = pn.showSaveDraftQuestion
	}

	// Editor.
	pn.editor = editor.New(m.log)
	pn.editor.SetDebug(m.log.Enabled(nil, slog.LevelDebug))
	pn.editorSlot.Append(pn.editor)
	pn.editor.OnState = pn.applyState
	pn.editor.OnChanged = pn.editorChanged
	pn.editor.OnDropFiles, pn.editor.OnPaste = pn.attachGioFiles, pn.pasteMarkdown
	pn.editor.OnReady = func() {
		if p.Kind != KindNew && p.Kind != KindEdit {
			pn.editor.FocusStart()
		}
		if opts.Owner == OwnerBoard {
			pn.dc.editorReady()
		}
	}
	pn.editor.OnCrashed = func() {
		if pn.dc.draft.closed {
			return
		}
		pn.toast(i18n.T("The editor crashed; your last text was restored"))
		pn.editor.Load(pn.editor.HTML())
	}
	if opts.Layout == LayoutInline {
		pn.wireSizedEditor()
	}

	// Prefill before connecting change handlers so it does not count as
	// an edit.
	pn.to.SetAddresses(p.To)
	pn.cc.SetAddresses(p.CC)
	pn.bcc.SetAddresses(p.BCC)
	pn.subjectEntry.SetText(p.Subject)
	pn.setCcBccVisible(len(p.CC) > 0, len(p.BCC) > 0)
	pn.updateTitle()
	pn.dc.setOriginal(p.InReplyTo, p.Forwarding, p.Comment)
	// A draft opened from the Drafts folder is the user's already: its
	// id and version make the saves updates, and closing never deletes it.
	pn.dc.setOpened(p.DraftID, p.Version, p.Replaces, p.Kind == KindEdit)
	pn.editor.Load(p.BodyHTML)
	pn.setAccounts(m.Accounts(), m.Placeholder())
	// What the backend imported for the template (a quoted original's
	// pictures, a forwarded message's files): listed and shown now, bound
	// by the first save.
	pn.setAttachments(p.Attachments)

	pn.wireActions()
	pn.wireToolbar()
	pn.wireRows()
	pn.wireRecipientSuggestions()
	if !richText {
		// Text-only phase (see richText): no formatting to offer, no
		// inline images, and the user is told what will go out.
		pn.toolbar.SetVisible(false)
		pn.actions["insert-image"].SetEnabled(false)
		pn.plainHint.SetVisible(true)
	}
	pn.applyCommentMode()

	if opts.Layout == LayoutInline {
		pn.fromBox.SetVisible(false)
		pn.fromSeparator.SetVisible(false)
		pn.buildInlineFooter()
		pn.wireInlineShortcuts()
	} else {
		pn.root.Append(pn.status)
		pn.status.SetMarginStart(12)
		pn.status.SetMarginEnd(12)
		pn.status.SetMarginTop(4)
		pn.status.SetMarginBottom(4)
	}
	pn.root.InsertActionGroup("compose", pn.group)
	return pn
}

var errNoDiscardHost = fmt.Errorf("compose: no discard host wired")

// Widget is the pane's root, for a host to place.
func (p *Pane) Widget() gtk.Widgetter { return p.root }

// ActionGroup is the pane's "compose.*" actions (Send, Attach, Save,
// Discard, Insert Image, Format): the pane always inserts it on its own
// root (compose.insert-image and the Format controls live in the
// pane's toolbar in both layouts), and compose.Window additionally
// inserts it on the window itself so its header bar's Send, Attach and
// menu button (outside the pane's widget tree) resolve it too.
func (p *Pane) ActionGroup() *gio.SimpleActionGroup { return p.group }

// IsComment reports the comment mode, for a host deciding whether to show
// its own Attach control.
func (p *Pane) IsComment() bool { return p.isComment() }

// FocusEditor gives the editor the keyboard (editor.GrabFocus).
func (p *Pane) FocusEditor() { p.editor.GrabFocus() }

// InitialFocus gives the keyboard to whatever should have it first: the
// editor for a comment, To otherwise.
func (p *Pane) InitialFocus() gtk.Widgetter {
	if p.isComment() {
		return p.editor
	}
	return p.to.entry
}

// ---------------------------------------------------------------------------
// composeForm
// ---------------------------------------------------------------------------

// fromFactory renders one identity in the From drop-down. GtkDropDown's
// built-in factory uses a label that never elides, so a long
// "Name <address>" would become the compose window's minimum width; this
// one elides and, as everywhere, shows the account's own text as plain
// text rather than markup.
func fromFactory() *gtk.SignalListItemFactory {
	f := gtk.NewSignalListItemFactory()
	f.ConnectSetup(func(obj *coreglib.Object) {
		item, ok := obj.Cast().(*gtk.ListItem)
		if !ok {
			return
		}
		l := gtk.NewLabel("")
		l.SetUseMarkup(false)
		l.SetXAlign(0)
		l.SetEllipsize(pango.EllipsizeEnd)
		l.SetMaxWidthChars(30)
		item.SetChild(l)
	})
	f.ConnectBind(func(obj *coreglib.Object) {
		item, ok := obj.Cast().(*gtk.ListItem)
		if !ok {
			return
		}
		l, ok := item.Child().(*gtk.Label)
		if !ok {
			return
		}
		if s, ok := item.Item().Cast().(*gtk.StringObject); ok {
			l.SetLabel(s.String())
		}
	})
	return f
}

// setAccounts fills the From row, keeping the selected identity when it is
// still listed; before any choice was made the account the pane was opened
// for (Params.AccountID) is preselected. The row is only sensitive with a
// choice. compose.Manager calls this on every open pane when the account
// list changes.
func (p *Pane) setAccounts(accounts []api.Account, placeholder bool) {
	selectedID := p.params.AccountID
	if p.chosenAccount != "" {
		selectedID = p.chosenAccount
	}
	p.accounts = accounts
	labels := make([]string, 0, len(accounts))
	selected := uint(0)
	found := false
	for i, a := range accounts {
		name := a.Config.DisplayName
		if name == "" {
			name = a.Config.Name
		}
		labels = append(labels, widget.FormatAddress(api.Address{Name: name, Address: a.Config.Email}))
		if a.ID == selectedID {
			selected = uint(i)
			found = true
		}
	}
	p.settingFrom = true
	p.from.SetFactory(&fromFactory().ListItemFactory)
	p.from.SetModel(gtk.NewStringList(labels))
	p.from.SetSelected(selected)
	p.settingFrom = false
	// A reply or a forward goes out from the account the original is in:
	// its quoted pictures and forwarded files were copied into that
	// account, and the reply belongs to that mailbox's conversation.
	p.from.SetSensitive(len(accounts) > 1 && !(p.fromLocked() && found))
	if placeholder && !p.isComment() {
		p.setStatus(i18n.T("Using placeholder account"))
	}
}

// fromLocked reports whether From is fixed to params.AccountID: for a
// reply or a forward (a draft reopened from Drafts included).
func (p *Pane) fromLocked() bool {
	return p.params.InReplyTo != "" || p.params.Forwarding != ""
}

// account is the selected identity; a comment's is the issue's account
// (Manager.commentAccount). The inline layout (the board's own draft) has
// no From row: the account is always params.AccountID, never the
// placeholder or the first entry (ui/internal/boardreply.Params.Account).
func (p *Pane) account() api.Account {
	if p.isComment() {
		return p.m.commentAccount(p.params.AccountID)
	}
	if p.options.Layout == LayoutInline && p.params.AccountID != "" {
		for _, a := range p.accounts {
			if a.ID == p.params.AccountID {
				return a
			}
		}
		if a, ok := p.m.knownAccount(p.params.AccountID); ok {
			return a
		}
		return api.Account{ID: p.params.AccountID, Enabled: true}
	}
	if i := p.from.Selected(); i < uint(len(p.accounts)) {
		return p.accounts[i]
	}
	if len(p.accounts) > 0 {
		return p.accounts[0]
	}
	return dummyAccounts[0]
}

func (p *Pane) self() api.Address {
	a := p.account()
	return api.Address{Name: a.Config.DisplayName, Address: a.Config.Email}
}

func (p *Pane) wireRows() {
	p.subjectEntry.ConnectChanged(func() {
		p.updateTitle()
		p.dc.markDirty()
	})
	p.from.NotifyProperty("selected", func() {
		if p.settingFrom {
			return
		}
		p.chosenAccount = p.account().ID
		p.dc.markDirty()
		// Another identity means other address books: what is shown was
		// asked on behalf of the previous one.
		for _, s := range p.suggest {
			s.hide()
		}
	})
	p.ccBcc.ConnectClicked(p.showCcBcc)
}

// wireRecipientSuggestions wires the To/Cc/Bcc recipient completion
// (suggest.go) and the rows' own dirty tracking.
func (p *Pane) wireRecipientSuggestions() {
	for _, f := range []*recipientField{p.to, p.cc, p.bcc} {
		f := f
		s := newSuggestions(p, f)
		p.suggest = append(p.suggest, s)
		// A focus loss that is only the window going to the background, or
		// a click on a suggestion, is not the end of the half-typed address.
		f.skipCommit = func() bool { return !p.isActive() || s.hover }
		f.changed = func() {
			if !p.dc.draft.closed {
				p.dc.markDirty()
			}
		}
		f.typed = s.onChanged
		f.wireKeys() // after the completion's keys: they come first
	}
}

// isActive reports whether the host window is active, for the recipient
// rows' skipCommit (a focus loss while the window is in the background is
// not the end of a half-typed address). The board embeds the pane inline
// with the main window as dialogParent, so switching to another
// application preserves a half-typed address there as well.
func (p *Pane) isActive() bool {
	if p.dialogParent == nil {
		return true
	}
	return p.dialogParent.IsActive()
}

// setCcBccVisible reveals the lines asked for and keeps the Cc/Bcc button
// only while one of them is still hidden. A reply carrying only a Cc
// therefore does not open an empty Bcc line as well.
func (p *Pane) setCcBccVisible(cc, bcc bool) {
	if cc {
		p.ccBox.SetVisible(true)
		p.ccSep.SetVisible(true)
	}
	if bcc {
		p.bccBox.SetVisible(true)
		p.bccSep.SetVisible(true)
	}
	p.ccBcc.SetVisible(!p.ccBox.Visible() || !p.bccBox.Visible())
}

// showCcBcc is the Cc/Bcc button: both lines at once.
func (p *Pane) showCcBcc() { p.setCcBccVisible(true, true) }

// updateTitle computes the pane's title (the subject, or "New Message"; a
// comment names its issue, jira.CommentTitle) and tells the host
// (OnTitle) when it changed.
func (p *Pane) updateTitle() {
	var title string
	if c := p.params.Comment; c != nil {
		title = jira.CommentTitle(c.Issue.Key, i18n.Tr)
	} else if s := strings.TrimSpace(p.subjectEntry.Text()); s != "" {
		title = s
	} else {
		title = i18n.T("New Message")
	}
	if title == p.titleText {
		return
	}
	p.titleText = title
	if p.OnTitle != nil {
		p.OnTitle(title)
	}
}

// ReplyTitle is the pane's title (boardreply.Pane).
func (p *Pane) ReplyTitle() string { return p.titleText }

// recipients reads the three rows as they would be with the typed text
// committed; ok is false when any entry is not an address. It goes through
// the fields' models, not through their text: parsing that again would
// join or split entries differently.
func (p *Pane) recipients() (to, cc, bcc []api.Address, ok bool) {
	ok = true
	read := func(f *recipientField) []api.Address {
		addrs, invalid := f.resolved()
		if len(invalid) > 0 {
			ok = false
		}
		return addrs
	}
	return read(p.to), read(p.cc), read(p.bcc), ok
}

func (p *Pane) subject() string { return p.subjectEntry.Text() }

func (p *Pane) attachments() []api.DraftAttachment { return p.attachmentList }

func (p *Pane) editorHTML() string { return p.editor.HTML() }
func (p *Pane) editorText() string { return p.editor.Text() }

// flushEditor asks the editor for its current content; its own debounced
// "changed" that reports the same content is the flush's own report, not
// an edit (editorChanged).
func (p *Pane) flushEditor(done func()) {
	p.editor.Flush(func() {
		p.flushed.record(p.editor.HTML())
		done()
	})
}

// setSendEnabled toggles the Send action: compose.blp's header bar button
// (window layout) and the footer's own Send button (inline layout) are
// both bound to it by action-name, so either follows automatically; the
// inline footer button's own sensitivity is also set directly (belt and
// braces: it has no accelerator of its own to race, but its action-name
// resolves through two different inserted action groups, pane.go
// ActionGroup).
func (p *Pane) setSendEnabled(enabled bool) {
	if a := p.actions["send"]; a != nil {
		a.SetEnabled(enabled)
	}
	if p.footerSend != nil {
		p.footerSend.SetSensitive(enabled)
	}
	if p.OnSendEnabled != nil {
		p.OnSendEnabled(enabled)
	}
}

// closeForm is composeForm's hook: the draft controller decided the pane
// goes. The window owner's host (compose.Window) turns OnEnd into an
// actual window close; the board's host (future work) retires the pane.
func (p *Pane) closeForm() {
	if p.ended {
		return
	}
	p.ended = true
	end := End{Kind: EndClosed}
	switch {
	case p.sentText != "":
		end = End{Kind: EndSent, Text: p.sentText}
	case p.dc.draft.discard:
		end = End{Kind: EndDiscarded}
	}
	if p.OnEnd != nil {
		p.OnEnd(end)
	}
}

// showSaveDraftQuestion is the window layout's "Save changes to this
// draft?" dialog (draftController.saveDraftQuestion).
func (p *Pane) showSaveDraftQuestion(done func(answer draftCloseAnswer)) {
	dlg := adw.NewAlertDialog(i18n.T("Save changes to this draft?"), "")
	dlg.AddResponse("cancel", i18n.T("_Cancel"))
	dlg.AddResponse("discard", i18n.T("_Discard"))
	dlg.AddResponse("save", i18n.T("_Save Draft"))
	dlg.SetResponseAppearance("discard", adw.ResponseDestructive)
	dlg.SetResponseAppearance("save", adw.ResponseSuggested)
	dlg.SetDefaultResponse("save")
	dlg.SetCloseResponse("cancel")
	dlg.ConnectResponse(func(response string) {
		switch response {
		case "discard":
			done(answerDiscard)
		case "save":
			done(answerSave)
		default:
			done(answerCancel)
		}
	})
	dlg.Present(p.dialogParent)
}

func (p *Pane) handleSent(text string) {
	p.sentText = text
	if p.options.Layout == LayoutWindow && p.m.OnSent != nil {
		p.m.OnSent(text)
	}
}

func (p *Pane) isComment() bool { return p.params.Comment != nil }

func (p *Pane) commentVisibility() api.CommentVisibility {
	if len(p.commentOptions) == 0 {
		return api.CommentPublic
	}
	return chosenVisibility(p.commentOptions, p.visibilityGroup.ActiveName())
}

// editorChanged is the editor's "changed": the one a flush produces
// reports what is being saved; only other content is an edit.
func (p *Pane) editorChanged() {
	if p.flushed.echo(p.editor.HTML()) {
		return
	}
	p.dc.markDirty()
}

// flushEcho remembers the content a flush's own report carried: the
// editor's "changed" with the same content is that report, not an edit.
// Without it every flush (a save's, settle's, editorReady's) marked the
// draft dirty again.
type flushEcho struct {
	html string
	set  bool
}

// record notes the HTML the flush reported.
func (f *flushEcho) record(html string) { f.html, f.set = html, true }

// echo reports whether html is what the last flush reported; any other
// content is an edit and forgets the record.
func (f *flushEcho) echo(html string) bool {
	if f.set && f.html == html {
		return true
	}
	f.html, f.set = "", false
	return false
}

// ---------------------------------------------------------------------------
// Helpers shared with suggest.go, markdown.go, comment.go, rewrite.go
// ---------------------------------------------------------------------------

func (p *Pane) ctx() context.Context {
	// Callers run the call in a goroutine bounded by RPCTimeout; the parent
	// is background so a closed pane does not cancel a save in flight.
	ctx, cancel := context.WithTimeout(context.Background(), widget.RPCTimeout)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}

// rpc runs call off the main loop and then on it, unless the pane closed.
func (p *Pane) rpc(call func() (any, error), then func(v any, err error)) {
	p.rpcOr(call, then, nil)
}

// rpcOr is rpc with gone (optional) run on the main loop in place of then
// when the pane closed meanwhile.
func (p *Pane) rpcOr(call func() (any, error), then, gone func(v any, err error)) {
	go func() {
		v, err := call()
		glib.IdleAdd(func() {
			if p.dc.draft.closed {
				if gone != nil {
					gone(v, err)
				}
				return
			}
			then(v, err)
		})
	}()
}

func (p *Pane) toast(text string) {
	if p.OnToast != nil {
		p.OnToast(text)
	}
}

func (p *Pane) setStatus(text string) {
	p.status.SetLabel(text)
	p.status.SetTooltipText(text)
}

// ---------------------------------------------------------------------------
// Actions and toolbar
// ---------------------------------------------------------------------------

// wireActions registers the "compose." action group. ActionGroup places
// it; NewPane always inserts it on the pane's own root.
func (p *Pane) wireActions() {
	g := gio.NewSimpleActionGroup()
	add := func(name string, f func()) {
		a := gio.NewSimpleAction(name, nil)
		a.ConnectActivate(func(*glib.Variant) { f() })
		g.AddAction(a)
		p.actions[name] = a
	}
	add("send", p.dc.send)
	add("save", func() { p.dc.save(saveExplicit, nil) })
	add("attach", p.attachFiles)
	add("insert-image", p.insertImage)
	add("discard", p.dc.discard)

	p.blockAction = gio.NewSimpleActionStateful("block", glib.NewVariantType("s"), glib.NewVariantString("p"))
	p.blockAction.ConnectActivate(func(v *glib.Variant) {
		p.editor.Exec("FormatBlock", v.String())
		p.editor.GrabFocus()
	})
	g.AddAction(p.blockAction)

	p.alignAction = gio.NewSimpleActionStateful("align", glib.NewVariantType("s"), glib.NewVariantString("left"))
	p.alignAction.ConnectActivate(func(v *glib.Variant) {
		switch v.String() {
		case "center":
			p.editor.Exec("JustifyCenter", "")
		case "right":
			p.editor.Exec("JustifyRight", "")
		default:
			p.editor.Exec("JustifyLeft", "")
		}
		p.editor.GrabFocus()
	})
	g.AddAction(p.alignAction)

	p.group = g
}

func (p *Pane) wireToolbar() {
	toggle := func(b *gtk.ToggleButton, cmd string) {
		b.ConnectToggled(func() {
			if p.syncing {
				return
			}
			p.editor.Exec(cmd, "")
		})
	}
	toggle(p.bold, "Bold")
	toggle(p.italic, "Italic")
	toggle(p.underline, "Underline")
	toggle(p.ul, "InsertUnorderedList")
	toggle(p.ol, "InsertOrderedList")
	p.quote.ConnectToggled(func() {
		if p.syncing {
			return
		}
		if p.quote.Active() {
			p.editor.Exec("FormatBlock", "blockquote")
		} else {
			p.editor.Exec("Outdent", "")
		}
	})
	p.clearButton.ConnectClicked(func() {
		p.editor.Exec("RemoveFormat", "")
		p.editor.Exec("Unlink", "")
	})
	p.colorButton.NotifyProperty("rgba", func() {
		p.editor.Exec("ForeColor", p.colorButton.RGBA().String())
	})

	insertLink := func() {
		raw := strings.TrimSpace(p.linkEntry.Text())
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") {
			p.linkEntry.AddCSSClass("error")
			return
		}
		p.linkEntry.RemoveCSSClass("error")
		p.editor.Exec("CreateLink", u.String())
		p.linkEntry.SetText("")
		p.linkPopover.Popdown()
	}
	p.linkApply.ConnectClicked(insertLink)
	p.linkEntry.ConnectActivate(insertLink)
}

// applyState mirrors the formatting at the caret onto the toolbar.
func (p *Pane) applyState(st editor.State) {
	p.syncing = true
	defer func() { p.syncing = false }()
	p.bold.SetActive(st.Bold)
	p.italic.SetActive(st.Italic)
	p.underline.SetActive(st.Underline)
	p.ul.SetActive(st.UL)
	p.ol.SetActive(st.OL)
	p.quote.SetActive(st.Block == "blockquote")

	block := st.Block
	label := i18n.T("Paragraph")
	switch block {
	case "h1", "h2", "h3":
		label = fmt.Sprintf(i18n.T("Heading %s"), block[1:])
	default:
		block = "p"
	}
	p.blockAction.SetState(glib.NewVariantString(block))
	p.blockButton.SetLabel(label)

	align := st.Align
	if align != "center" && align != "right" {
		align = "left"
	}
	p.alignAction.SetState(glib.NewVariantString(align))
	p.alignButton.SetIconName("format-justify-" + align + "-symbolic")
}

// ---------------------------------------------------------------------------
// Attachments
// ---------------------------------------------------------------------------

func (p *Pane) attachFiles() {
	dlg := gtk.NewFileDialog()
	dlg.SetTitle(i18n.T("Attach Files"))
	dlg.OpenMultiple(p.ctx(), p.topLevelWindow(), func(res gio.AsyncResulter) {
		files, err := dlg.OpenMultipleFinish(res)
		if err != nil || p.dc.draft.closed {
			return // cancelled
		}
		list := make([]*gio.File, 0, files.NItems())
		for i := uint(0); i < files.NItems(); i++ {
			list = append(list, files.Item(i).Cast().(*gio.File))
		}
		p.attachGioFiles(list)
	})
}

// topLevelWindow is the *gtk.Window a file dialog presents over: the
// compose window or the board's main window.
func (p *Pane) topLevelWindow() *gtk.Window { return p.dialogParent }

// attachGioFiles imports files chosen in the dialog or dropped onto the
// editor as attachments; only local files can be.
func (p *Pane) attachGioFiles(files []*gio.File) {
	if p.dc.draft.closed {
		return
	}
	for _, f := range files {
		path := f.Path()
		if path == "" {
			p.toast(i18n.T("Only local files can be attached"))
			continue
		}
		p.importFile(path, f.Basename(), false, nil)
	}
}

func (p *Pane) insertImage() {
	dlg := gtk.NewFileDialog()
	dlg.SetTitle(i18n.T("Insert Image"))
	filter := gtk.NewFileFilter()
	filter.SetName(i18n.T("Images"))
	for _, pat := range []string{"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp"} {
		filter.AddPattern(pat)
	}
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(filter.Object)
	dlg.SetFilters(filters)
	dlg.Open(p.ctx(), p.topLevelWindow(), func(res gio.AsyncResulter) {
		f, err := dlg.OpenFinish(res)
		if err != nil || p.dc.draft.closed {
			return
		}
		path := f.Path()
		if path == "" {
			p.toast(i18n.T("Only local images can be inserted"))
			return
		}
		p.importFile(path, f.Basename(), true, func(att api.DraftAttachment) {
			editor.RegisterCID(att.ContentID, path, att.ContentType)
			p.editor.Exec("InsertImage", "cid:"+att.ContentID)
			p.editor.GrabFocus()
		})
	})
}

// importFile hands the path to the backend and adds the attachment on
// success; then (optional) runs afterwards on the main loop.
func (p *Pane) importFile(path, name string, inline bool, then func(api.DraftAttachment)) {
	p.setStatus(fmt.Sprintf(i18n.T("Attaching %s…"), name))
	p.rpc(func() (any, error) {
		var res api.AttachmentImportResult
		err := p.m.client.Call(p.ctx(), api.MethodAttachmentImport, api.AttachmentImportParams{
			AccountID: p.account().ID, Path: path, Filename: name, Inline: inline,
		}, &res)
		return res, err
	}, func(v any, err error) {
		if err != nil {
			p.toast(widget.RPCErrorText(fmt.Sprintf(i18n.T("Attaching %s"), name), err))
			p.dc.refreshStatus()
			return
		}
		att := v.(api.AttachmentImportResult).Attachment
		p.attachmentList = append(p.attachmentList, att)
		p.addChip(att)
		p.dc.markDirty()
		if then != nil {
			then(att)
		}
	})
}

func (p *Pane) addChip(att api.DraftAttachment) {
	box := gtk.NewBox(gtk.OrientationHorizontal, 6)
	box.AddCSSClass("card")
	box.SetMarginTop(2)
	box.SetMarginBottom(2)
	icon := gtk.NewImageFromIconName("mail-attachment-symbolic")
	if att.Inline {
		icon.SetFromIconName("image-x-generic-symbolic")
	}
	icon.SetMarginStart(8)
	name := gtk.NewLabel(att.Filename) // backend-sanitised, still plain text
	name.SetEllipsize(3)               // PANGO_ELLIPSIZE_END
	name.SetMaxWidthChars(24)
	size := gtk.NewLabel(widget.FormatSize(att.Size))
	size.AddCSSClass("caption")
	size.AddCSSClass("dim-label")
	remove := gtk.NewButtonFromIconName("window-close-symbolic")
	remove.AddCSSClass("flat")
	remove.SetTooltipText(i18n.T("Remove"))
	remove.ConnectClicked(func() { p.removeAttachment(att.ID) })
	box.Append(icon)
	box.Append(name)
	box.Append(size)
	box.Append(remove)
	p.attBox.Insert(box, -1)
	p.chips[att.ID] = box
	p.attBox.SetVisible(true)
}

func (p *Pane) removeAttachment(id string) {
	var kept []api.DraftAttachment
	var removed *api.DraftAttachment
	for i := range p.attachmentList {
		if p.attachmentList[i].ID == id {
			removed = &p.attachmentList[i]
			continue
		}
		kept = append(kept, p.attachmentList[i])
	}
	if removed == nil {
		return
	}
	if removed.Inline {
		editor.UnregisterCID(removed.ContentID)
	}
	p.attachmentList = kept
	if chip, ok := p.chips[id]; ok {
		p.attBox.Remove(chip)
		delete(p.chips, id)
	}
	p.attBox.SetVisible(len(p.attachmentList) > 0)
	p.dc.markDirty()
	accountID := p.account().ID
	p.rpc(func() (any, error) {
		return nil, p.m.client.Call(p.ctx(), api.MethodAttachmentRemove,
			api.AttachmentRemoveParams{AccountID: accountID, AttachmentID: id}, &api.AttachmentRemoveResult{})
	}, func(_ any, err error) {
		if err != nil {
			p.log.Debug("attachment.remove", "err", err)
		}
	})
}

// setAttachments replaces the list and chips with what the backend kept.
// An inline picture the pane did not insert itself (the backend copied it
// out of a quoted original) is served to the editor from the backend; one
// that is gone from the list is forgotten.
func (p *Pane) setAttachments(atts []api.DraftAttachment) {
	for id, chip := range p.chips {
		p.attBox.Remove(chip)
		delete(p.chips, id)
	}
	kept := make(map[string]bool, len(atts))
	for _, a := range atts {
		if a.Inline {
			kept[a.ContentID] = true
		}
	}
	for _, a := range p.attachmentList {
		if a.Inline && !kept[a.ContentID] {
			editor.UnregisterCID(a.ContentID)
		}
	}
	p.attachmentList = nil
	for _, a := range atts {
		p.attachmentList = append(p.attachmentList, a)
		p.addChip(a)
		if a.Inline && !editor.CIDRegistered(a.ContentID) {
			p.registerInline(a)
		}
	}
	p.attBox.SetVisible(len(p.attachmentList) > 0)
}

// registerInline makes the editor fetch the picture behind cid:<contentId>
// from the backend (attachment.get), for a copy the backend made.
func (p *Pane) registerInline(a api.DraftAttachment) {
	c, accountID, id := p.m.client, p.account().ID, a.ID
	editor.RegisterCIDFetcher(a.ContentID, func(ctx context.Context) ([]byte, string, error) {
		var res api.AttachmentGetResult
		if err := c.Call(ctx, api.MethodAttachmentGet, api.AttachmentGetParams{AccountID: accountID, AttachmentID: id}, &res); err != nil {
			return nil, "", err
		}
		return res.Data, res.ContentType, nil
	})
}

// ---------------------------------------------------------------------------
// The daemon client, for the draft controller.
// ---------------------------------------------------------------------------

// realCaller adapts Manager's client to the draft controller's caller
// interface.
type realCaller struct{ m *Manager }

func (c *realCaller) Call(ctx context.Context, method string, params, result any) error {
	return c.m.client.Call(ctx, method, params, result)
}

// glibLoop runs the draft controller's continuations and timers on GTK's
// main loop (assistantpanel.Loop's shape).
type glibLoop struct{}

func (glibLoop) Post(f func()) { glib.IdleAdd(f) }
func (glibLoop) After(d time.Duration, f func()) {
	glib.TimeoutAdd(uint(d.Milliseconds()), func() bool {
		f()
		return false
	})
}
