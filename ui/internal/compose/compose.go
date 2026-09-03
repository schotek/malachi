// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/editor"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Window is one compose window, built from data/ui/compose.blp.
type Window struct {
	*adw.Window

	m      *Manager
	log    *slog.Logger
	params Params

	title      *adw.WindowTitle
	from       *adw.ComboRow
	to         *adw.EntryRow
	cc         *adw.EntryRow
	bcc        *adw.EntryRow
	subject    *adw.EntryRow
	ccBcc      *gtk.Button
	toasts     *adw.ToastOverlay
	editorSlot *gtk.Box
	attBox     *gtk.FlowBox
	status     *gtk.Label
	sendButton *gtk.Button

	bold, italic, underline *gtk.ToggleButton
	ul, ol, quote           *gtk.ToggleButton
	blockButton             *gtk.MenuButton
	alignButton             *gtk.MenuButton
	linkPopover             *gtk.Popover
	linkEntry               *gtk.Entry
	linkApply               *gtk.Button
	colorButton             *gtk.ColorDialogButton
	clearButton             *gtk.Button

	editor      *editor.Editor
	actions     map[string]*gio.SimpleAction
	blockAction *gio.SimpleAction
	alignAction *gio.SimpleAction
	syncing     bool // toolbar being updated from the page, not by the user

	accounts    []api.Account
	attachments []api.DraftAttachment
	chips       map[string]gtk.Widgetter

	draft draftState
}

// newWindow builds and prefills a window; Manager.Open presents it.
func newWindow(m *Manager, p Params) *Window {
	b := data.Builder("compose.ui")
	w := &Window{
		Window:      b.GetObject("compose_window").Cast().(*adw.Window),
		m:           m,
		log:         m.log,
		params:      p,
		title:       b.GetObject("window_title").Cast().(*adw.WindowTitle),
		from:        b.GetObject("from_row").Cast().(*adw.ComboRow),
		to:          b.GetObject("to_row").Cast().(*adw.EntryRow),
		cc:          b.GetObject("cc_row").Cast().(*adw.EntryRow),
		bcc:         b.GetObject("bcc_row").Cast().(*adw.EntryRow),
		subject:     b.GetObject("subject_row").Cast().(*adw.EntryRow),
		ccBcc:       b.GetObject("cc_bcc_button").Cast().(*gtk.Button),
		toasts:      b.GetObject("toast_overlay").Cast().(*adw.ToastOverlay),
		editorSlot:  b.GetObject("editor_slot").Cast().(*gtk.Box),
		attBox:      b.GetObject("attachments_box").Cast().(*gtk.FlowBox),
		status:      b.GetObject("draft_status").Cast().(*gtk.Label),
		sendButton:  b.GetObject("send_button").Cast().(*gtk.Button),
		bold:        b.GetObject("bold_button").Cast().(*gtk.ToggleButton),
		italic:      b.GetObject("italic_button").Cast().(*gtk.ToggleButton),
		underline:   b.GetObject("underline_button").Cast().(*gtk.ToggleButton),
		ul:          b.GetObject("ul_button").Cast().(*gtk.ToggleButton),
		ol:          b.GetObject("ol_button").Cast().(*gtk.ToggleButton),
		quote:       b.GetObject("quote_button").Cast().(*gtk.ToggleButton),
		blockButton: b.GetObject("block_button").Cast().(*gtk.MenuButton),
		alignButton: b.GetObject("align_button").Cast().(*gtk.MenuButton),
		linkPopover: b.GetObject("link_popover").Cast().(*gtk.Popover),
		linkEntry:   b.GetObject("link_entry").Cast().(*gtk.Entry),
		linkApply:   b.GetObject("link_apply").Cast().(*gtk.Button),
		colorButton: b.GetObject("color_button").Cast().(*gtk.ColorDialogButton),
		clearButton: b.GetObject("clear_button").Cast().(*gtk.Button),
		actions:     make(map[string]*gio.SimpleAction),
		chips:       make(map[string]gtk.Widgetter),
	}

	// Editor.
	w.editor = editor.New(m.log)
	w.editor.SetDebug(m.log.Enabled(nil, slog.LevelDebug))
	w.editorSlot.Append(w.editor)
	w.editor.OnState = w.applyState
	w.editor.OnChanged = w.markDirty
	w.editor.OnReady = func() {
		if p.Kind != KindNew {
			w.editor.FocusStart()
		}
	}
	w.editor.OnCrashed = func() {
		if w.draft.closed {
			return
		}
		w.toast(i18n.T("The editor crashed; your last text was restored"))
		w.editor.Load(w.editor.HTML())
	}

	// Prefill before connecting change handlers so it does not count as
	// an edit.
	w.to.SetText(FormatAddressList(p.To))
	w.cc.SetText(FormatAddressList(p.CC))
	w.bcc.SetText(FormatAddressList(p.BCC))
	w.subject.SetText(p.Subject)
	if len(p.CC) > 0 || len(p.BCC) > 0 {
		w.showCcBcc()
	}
	w.updateTitle()
	w.draft.inReplyTo, w.draft.forwarding = p.InReplyTo, p.Forwarding
	w.editor.Load(p.BodyHTML)
	w.setAccounts(m.Accounts(), m.Placeholder())

	w.wireActions()
	w.wireToolbar()
	w.wireRows()
	w.ConnectCloseRequest(w.closeRequest)
	return w
}

// setAccounts fills the From row, keeping the selected identity when it is
// still listed. The row is only sensitive with a choice.
func (w *Window) setAccounts(accounts []api.Account, placeholder bool) {
	var selectedID api.AccountID
	if len(w.accounts) > 0 {
		selectedID = w.account().ID
	}
	w.accounts = accounts
	labels := make([]string, 0, len(accounts))
	selected := uint(0)
	for i, a := range accounts {
		name := a.Config.DisplayName
		if name == "" {
			name = a.Config.Name
		}
		labels = append(labels, widget.FormatAddress(api.Address{Name: name, Address: a.Config.Email}))
		if a.ID == selectedID {
			selected = uint(i)
		}
	}
	w.from.SetModel(gtk.NewStringList(labels))
	w.from.SetSelected(selected)
	w.from.SetSensitive(len(accounts) > 1)
	if placeholder {
		w.setStatus(i18n.T("Using placeholder account"))
	}
}

// account is the selected identity.
func (w *Window) account() api.Account {
	if i := w.from.Selected(); i < uint(len(w.accounts)) {
		return w.accounts[i]
	}
	return w.accounts[0]
}

func (w *Window) self() api.Address {
	a := w.account()
	return api.Address{Name: a.Config.DisplayName, Address: a.Config.Email}
}

func (w *Window) wireRows() {
	for _, row := range []*adw.EntryRow{w.to, w.cc, w.bcc} {
		row := row
		row.ConnectChanged(func() {
			w.validateRow(row)
			w.markDirty()
		})
	}
	w.subject.ConnectChanged(func() {
		w.updateTitle()
		w.markDirty()
	})
	w.from.NotifyProperty("selected", w.markDirty)
	w.ccBcc.ConnectClicked(w.showCcBcc)
}

func (w *Window) showCcBcc() {
	w.cc.SetVisible(true)
	w.bcc.SetVisible(true)
	w.ccBcc.SetVisible(false)
}

func (w *Window) updateTitle() {
	if s := strings.TrimSpace(w.subject.Text()); s != "" {
		w.title.SetTitle(s)
	} else {
		w.title.SetTitle(i18n.T("New Message"))
	}
}

// validateRow flags a recipient row with unparsable tokens.
func (w *Window) validateRow(row *adw.EntryRow) bool {
	_, invalid := ParseAddressList(row.Text())
	if len(invalid) > 0 {
		row.AddCSSClass("error")
		return false
	}
	row.RemoveCSSClass("error")
	return true
}

// recipients parses the three rows; ok is false when any token is invalid.
func (w *Window) recipients() (to, cc, bcc []api.Address, ok bool) {
	ok = true
	parse := func(row *adw.EntryRow) []api.Address {
		addrs, invalid := ParseAddressList(row.Text())
		if len(invalid) > 0 {
			ok = false
		}
		return addrs
	}
	return parse(w.to), parse(w.cc), parse(w.bcc), ok
}

// wireActions registers the "compose." action group on the window.
func (w *Window) wireActions() {
	g := gio.NewSimpleActionGroup()
	add := func(name string, f func()) {
		a := gio.NewSimpleAction(name, nil)
		a.ConnectActivate(func(*glib.Variant) { f() })
		g.AddAction(a)
		w.actions[name] = a
	}
	add("send", w.send)
	add("save", func() { w.save(saveExplicit, nil) })
	add("attach", w.attachFiles)
	add("insert-image", w.insertImage)
	add("discard", w.discard)

	w.blockAction = gio.NewSimpleActionStateful("block", glib.NewVariantType("s"), glib.NewVariantString("p"))
	w.blockAction.ConnectActivate(func(p *glib.Variant) {
		w.editor.Exec("FormatBlock", p.String())
		w.editor.GrabFocus()
	})
	g.AddAction(w.blockAction)

	w.alignAction = gio.NewSimpleActionStateful("align", glib.NewVariantType("s"), glib.NewVariantString("left"))
	w.alignAction.ConnectActivate(func(p *glib.Variant) {
		switch p.String() {
		case "center":
			w.editor.Exec("JustifyCenter", "")
		case "right":
			w.editor.Exec("JustifyRight", "")
		default:
			w.editor.Exec("JustifyLeft", "")
		}
		w.editor.GrabFocus()
	})
	g.AddAction(w.alignAction)

	w.InsertActionGroup("compose", g)
}

func (w *Window) wireToolbar() {
	toggle := func(b *gtk.ToggleButton, cmd string) {
		b.ConnectToggled(func() {
			if w.syncing {
				return
			}
			w.editor.Exec(cmd, "")
		})
	}
	toggle(w.bold, "Bold")
	toggle(w.italic, "Italic")
	toggle(w.underline, "Underline")
	toggle(w.ul, "InsertUnorderedList")
	toggle(w.ol, "InsertOrderedList")
	w.quote.ConnectToggled(func() {
		if w.syncing {
			return
		}
		if w.quote.Active() {
			w.editor.Exec("FormatBlock", "blockquote")
		} else {
			w.editor.Exec("Outdent", "")
		}
	})
	w.clearButton.ConnectClicked(func() {
		w.editor.Exec("RemoveFormat", "")
		w.editor.Exec("Unlink", "")
	})
	w.colorButton.NotifyProperty("rgba", func() {
		w.editor.Exec("ForeColor", w.colorButton.RGBA().String())
	})

	insertLink := func() {
		raw := strings.TrimSpace(w.linkEntry.Text())
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "mailto") {
			w.linkEntry.AddCSSClass("error")
			return
		}
		w.linkEntry.RemoveCSSClass("error")
		w.editor.Exec("CreateLink", u.String())
		w.linkEntry.SetText("")
		w.linkPopover.Popdown()
	}
	w.linkApply.ConnectClicked(insertLink)
	w.linkEntry.ConnectActivate(insertLink)
}

// applyState mirrors the formatting at the caret onto the toolbar.
func (w *Window) applyState(st editor.State) {
	w.syncing = true
	defer func() { w.syncing = false }()
	w.bold.SetActive(st.Bold)
	w.italic.SetActive(st.Italic)
	w.underline.SetActive(st.Underline)
	w.ul.SetActive(st.UL)
	w.ol.SetActive(st.OL)
	w.quote.SetActive(st.Block == "blockquote")

	block := st.Block
	label := i18n.T("Paragraph")
	switch block {
	case "h1", "h2", "h3":
		label = fmt.Sprintf(i18n.T("Heading %s"), block[1:])
	default:
		block = "p"
	}
	w.blockAction.SetState(glib.NewVariantString(block))
	w.blockButton.SetLabel(label)

	align := st.Align
	if align != "center" && align != "right" {
		align = "left"
	}
	w.alignAction.SetState(glib.NewVariantString(align))
	w.alignButton.SetIconName("format-justify-" + align + "-symbolic")
}

func (w *Window) toast(text string) {
	w.toasts.AddToast(widget.PlainToast(text))
}

func (w *Window) setStatus(text string) {
	w.status.SetLabel(text)
}

// ---------------------------------------------------------------------------
// Attachments
// ---------------------------------------------------------------------------

func (w *Window) attachFiles() {
	dlg := gtk.NewFileDialog()
	dlg.SetTitle(i18n.T("Attach Files"))
	dlg.OpenMultiple(w.ctx(), &w.Window.Window, func(res gio.AsyncResulter) {
		files, err := dlg.OpenMultipleFinish(res)
		if err != nil || w.draft.closed {
			return // cancelled
		}
		for i := uint(0); i < files.NItems(); i++ {
			f := files.Item(i).Cast().(*gio.File)
			path := f.Path()
			if path == "" {
				w.toast(i18n.T("Only local files can be attached"))
				continue
			}
			w.importFile(path, f.Basename(), false, nil)
		}
	})
}

func (w *Window) insertImage() {
	dlg := gtk.NewFileDialog()
	dlg.SetTitle(i18n.T("Insert Image"))
	filter := gtk.NewFileFilter()
	filter.SetName(i18n.T("Images"))
	for _, p := range []string{"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp"} {
		filter.AddPattern(p)
	}
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(filter.Object)
	dlg.SetFilters(filters)
	dlg.Open(w.ctx(), &w.Window.Window, func(res gio.AsyncResulter) {
		f, err := dlg.OpenFinish(res)
		if err != nil || w.draft.closed {
			return
		}
		path := f.Path()
		if path == "" {
			w.toast(i18n.T("Only local images can be inserted"))
			return
		}
		w.importFile(path, f.Basename(), true, func(att api.DraftAttachment) {
			editor.RegisterCID(att.ContentID, path, att.ContentType)
			w.editor.Exec("InsertImage", "cid:"+att.ContentID)
			w.editor.GrabFocus()
		})
	})
}

// importFile hands the path to the backend and adds the attachment on
// success; then (optional) runs afterwards on the main loop.
func (w *Window) importFile(path, name string, inline bool, then func(api.DraftAttachment)) {
	w.setStatus(fmt.Sprintf(i18n.T("Attaching %s…"), name))
	w.rpc(func() (any, error) {
		var res api.AttachmentImportResult
		err := w.m.client.Call(w.ctx(), api.MethodAttachmentImport, api.AttachmentImportParams{
			AccountID: w.account().ID, Path: path, Filename: name, Inline: inline,
		}, &res)
		return res, err
	}, func(v any, err error) {
		if err != nil {
			w.toast(widget.RPCErrorText(fmt.Sprintf(i18n.T("Attaching %s"), name), err))
			w.refreshStatus()
			return
		}
		att := v.(api.AttachmentImportResult).Attachment
		w.attachments = append(w.attachments, att)
		w.addChip(att)
		w.markDirty()
		if then != nil {
			then(att)
		}
	})
}

func (w *Window) addChip(att api.DraftAttachment) {
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
	size := gtk.NewLabel(formatSize(att.Size))
	size.AddCSSClass("caption")
	size.AddCSSClass("dim-label")
	remove := gtk.NewButtonFromIconName("window-close-symbolic")
	remove.AddCSSClass("flat")
	remove.SetTooltipText(i18n.T("Remove"))
	remove.ConnectClicked(func() { w.removeAttachment(att.ID) })
	box.Append(icon)
	box.Append(name)
	box.Append(size)
	box.Append(remove)
	w.attBox.Insert(box, -1)
	w.chips[att.ID] = box
	w.attBox.SetVisible(true)
}

func (w *Window) removeAttachment(id string) {
	var kept []api.DraftAttachment
	var removed *api.DraftAttachment
	for i := range w.attachments {
		if w.attachments[i].ID == id {
			removed = &w.attachments[i]
			continue
		}
		kept = append(kept, w.attachments[i])
	}
	if removed == nil {
		return
	}
	if removed.Inline {
		editor.UnregisterCID(removed.ContentID)
	}
	w.attachments = kept
	if chip, ok := w.chips[id]; ok {
		w.attBox.Remove(chip)
		delete(w.chips, id)
	}
	w.attBox.SetVisible(len(w.attachments) > 0)
	w.markDirty()
	accountID := w.account().ID
	w.rpc(func() (any, error) {
		return nil, w.m.client.Call(w.ctx(), api.MethodAttachmentRemove,
			api.AttachmentRemoveParams{AccountID: accountID, AttachmentID: id}, &api.AttachmentRemoveResult{})
	}, func(_ any, err error) {
		if err != nil {
			w.log.Debug("attachment.remove", "err", err)
		}
	})
}

// setAttachments replaces the list and chips with what the backend kept.
func (w *Window) setAttachments(atts []api.DraftAttachment) {
	for id, chip := range w.chips {
		w.attBox.Remove(chip)
		delete(w.chips, id)
	}
	w.attachments = nil
	for _, a := range atts {
		w.attachments = append(w.attachments, a)
		w.addChip(a)
	}
	w.attBox.SetVisible(len(w.attachments) > 0)
}

func formatSize(n int64) string {
	switch {
	case n >= 1<<20:
		// TRANSLATORS: file size in mebibytes.
		return fmt.Sprintf(i18n.T("%.1f MiB"), float64(n)/(1<<20))
	case n >= 1<<10:
		// TRANSLATORS: file size in kibibytes.
		return fmt.Sprintf(i18n.T("%.0f KiB"), float64(n)/(1<<10))
	default:
		// TRANSLATORS: file size in bytes.
		return fmt.Sprintf(i18n.T("%d B"), n)
	}
}
