// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/signature"
	"github.com/schotek/malachi/ui/internal/widget"
)

// Preferences → Security (plan §6 and §11.3 SecurityPageTexts; the macOS
// Security pane, the Windows Security page): the stored OpenPGP keys
// (pgp.keyList) with their trust and state, Confirm Key… or Remove
// Confirmation (pgp.keyVerify) and Remove Key… (pgp.keyRemove, asked
// first), and Import Key… with a pasted key or a file (pgp.keyImport).
// The page is built from Go, after the AI page, so preferences.blp
// carries none of its texts; every text is the reference's, every string
// of a key plain text. notify.signaturesChanged lists the keys again.

// securityPage is the page while the dialog is open.
type securityPage struct {
	d     *PreferencesDialog
	c     *client.Client
	texts signature.SecurityPage

	page         *adw.PreferencesPage
	group        *adw.PreferencesGroup
	importButton *gtk.Button
	// status is the row in place of the keys: loading (empty title),
	// none, or the load failed.
	status *adw.ActionRow
	rows   []*gtk.ListBoxRow

	// gen numbers the pgp.keyList requests (only the last answer counts);
	// closed is set once the dialog closed.
	gen    int
	closed bool
}

// bindSecurity adds the Security page and lists the keys.
func (d *PreferencesDialog) bindSecurity(c *client.Client) (unbind func()) {
	p := newSecurityPage(d, c)
	cancel := listenSignatures(p.load)
	p.load()
	return func() {
		p.closed = true
		cancel()
	}
}

// newSecurityPage builds the page and adds it to d (after the AI page);
// load lists the keys.
func newSecurityPage(d *PreferencesDialog, c *client.Client) *securityPage {
	p := &securityPage{d: d, c: c, texts: signature.SecurityPageTexts(i18n.Tr)}
	p.page = adw.NewPreferencesPage()
	p.page.SetName("security")
	p.page.SetTitle(p.texts.Title)
	p.page.SetIconName(signature.IconPage)
	p.group = adw.NewPreferencesGroup()
	p.group.SetTitle(p.texts.Group)
	p.group.SetDescription(p.texts.Description)
	p.importButton = gtk.NewButtonWithLabel(p.texts.Import)
	p.importButton.AddCSSClass("flat")
	p.importButton.SetVAlign(gtk.AlignCenter)
	p.importButton.SetSensitive(c != nil)
	p.importButton.ConnectClicked(p.openImport)
	p.group.SetHeaderSuffix(p.importButton)
	p.status = adw.NewActionRow()
	p.status.SetUseMarkup(false)
	p.status.SetVisible(false) // until pgp.keyList answered
	p.group.Add(p.status)
	p.page.Add(p.group)
	d.Add(p.page)
	return p
}

// load asks pgp.keyList and shows the answer.
func (p *securityPage) load() {
	if p.closed {
		return
	}
	if p.c == nil {
		p.show(nil, true)
		return
	}
	p.gen++
	gen := p.gen
	c := p.c
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.PGPKeyListResult
		err := c.Call(ctx, api.MethodPGPKeyList, api.PGPKeyListParams{}, &res)
		glib.IdleAdd(func() {
			if p.closed || gen != p.gen {
				return
			}
			if err != nil {
				p.d.log.Info("pgp.keyList failed", "err", err)
			}
			p.show(res.Keys, err != nil)
		})
	}()
}

// show replaces the rows by those of keys (signature.KeyRows), or the
// status row: No OpenPGP Keys, or Could not load the keys.
func (p *securityPage) show(keys []api.PGPKey, failed bool) {
	for _, r := range p.rows {
		p.group.Remove(r)
	}
	p.rows = nil
	switch {
	case failed:
		p.status.SetTitle(p.texts.LoadFailed)
		p.status.SetVisible(true)
		return
	case len(keys) == 0:
		p.status.SetTitle(p.texts.Empty)
		p.status.SetVisible(true)
		return
	}
	p.status.SetVisible(false)
	for _, kr := range signature.KeyRows(keys, time.Now(), widget.FormatDateTime, i18n.Tr) {
		r := p.keyRow(kr)
		p.rows = append(p.rows, r)
		p.group.Add(r)
	}
}

// keyRow is the row of one key: its title, the fingerprint in a monospace
// face, where it came from, the trust pill (green when confirmed), the
// state pill (red: revoked or expired) and the menu of its actions.
func (p *securityPage) keyRow(kr signature.KeyRow) *gtk.ListBoxRow {
	row := gtk.NewListBoxRow()
	row.SetActivatable(false)
	box := gtk.NewBox(gtk.OrientationHorizontal, 8)
	box.SetMarginTop(8)
	box.SetMarginBottom(8)
	box.SetMarginStart(12)
	box.SetMarginEnd(8)

	text := gtk.NewBox(gtk.OrientationVertical, 2)
	text.SetHExpand(true)
	text.SetVAlign(gtk.AlignCenter)
	title := plainKeyLabel(kr.Title)
	title.SetEllipsize(pango.EllipsizeEnd)
	title.SetTooltipText(kr.Title)
	text.Append(title)
	if kr.Subtitle != "" {
		fp := plainKeyLabel(kr.Subtitle, "caption", "monospace")
		fp.SetWrap(true)
		fp.SetWrapMode(pango.WrapWordChar)
		fp.SetSelectable(true)
		fp.SetFocusable(false)
		text.Append(fp)
	}
	origin := plainKeyLabel(kr.Origin, "caption", "dim-label")
	origin.SetWrap(true)
	origin.SetWrapMode(pango.WrapWordChar)
	text.Append(origin)
	box.Append(text)

	trust := widget.NewPill()
	trust.SetText(kr.TrustPill)
	trust.SetVisible(kr.TrustPill != "")
	if kr.Verified {
		trust.AddCSSClass("key-verified")
	}
	box.Append(trust)
	state := widget.NewPill()
	state.SetText(kr.StatePill)
	state.SetVisible(kr.StatePill != "")
	state.AddCSSClass("key-state")
	box.Append(state)

	menu := gtk.NewMenuButton()
	menu.SetIconName("view-more-symbolic")
	menu.AddCSSClass("flat")
	menu.SetVAlign(gtk.AlignCenter)
	// The menu names the key it acts on for a screen reader.
	menu.SetTooltipText(kr.Title)
	popover := gtk.NewPopover()
	items := gtk.NewBox(gtk.OrientationVertical, 0)
	for _, b := range kr.Actions {
		item := gtk.NewButtonWithMnemonic(b.Label)
		item.AddCSSClass("flat")
		if child, ok := item.Child().(*gtk.Label); ok {
			child.SetXAlign(0)
			child.SetUseMarkup(false)
		}
		item.SetSensitive(b.Enabled)
		action, fingerprint := b.Action, kr.Fingerprint
		item.ConnectClicked(func() {
			popover.Popdown()
			p.act(action, fingerprint)
		})
		items.Append(item)
	}
	popover.SetChild(items)
	menu.SetPopover(popover)
	menu.SetVisible(len(kr.Actions) > 0)
	box.Append(menu)

	row.SetChild(box)
	return row
}

// plainKeyLabel is a plain-text label, left aligned, with classes.
func plainKeyLabel(text string, classes ...string) *gtk.Label {
	l := gtk.NewLabel(text)
	l.SetUseMarkup(false)
	l.SetXAlign(0)
	for _, c := range classes {
		l.AddCSSClass(c)
	}
	return l
}

// act runs a key row's action: Confirm Key… and Remove Key… ask first.
func (p *securityPage) act(action signature.Action, fingerprint string) {
	if p.closed || p.c == nil {
		return
	}
	switch action {
	case signature.ActionConfirmKey:
		askSignatureQuestion(p.d, signature.ConfirmKeyQuestion(i18n.Tr), signature.FormatFingerprint(fingerprint), func() {
			p.call(action, api.MethodPGPKeyVerify, api.PGPKeyVerifyParams{Fingerprint: fingerprint, Verified: true}, signature.KeyConfirmedText(i18n.Tr))
		})
	case signature.ActionUnconfirmKey:
		p.call(action, api.MethodPGPKeyVerify, api.PGPKeyVerifyParams{Fingerprint: fingerprint, Verified: false}, signature.ConfirmationRemovedText(i18n.Tr))
	case signature.ActionRemoveKey:
		askSignatureQuestion(p.d, signature.RemoveKeyQuestion(i18n.Tr), "", func() {
			p.call(action, api.MethodPGPKeyRemove, api.PGPKeyRemoveParams{Fingerprint: fingerprint}, signature.KeyRemovedText(i18n.Tr))
		})
	}
}

// call runs one key method; done is the toast after it. The list is asked
// again either way (the daemon's notify.signaturesChanged does it too).
func (p *securityPage) call(action signature.Action, method string, params any, done string) {
	c := p.c
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		err := c.Call(ctx, method, params, nil)
		glib.IdleAdd(func() {
			if p.closed {
				return
			}
			if err != nil {
				p.d.log.Info(method+" failed", "err", err)
				p.d.AddToast(widget.PlainToast(widget.RPCErrorText(actionWhat(action), err)))
			} else {
				p.d.AddToast(widget.PlainToast(done))
			}
			p.load()
		})
	}()
}

// openImport is Import Key…: a dialog with a text area for a pasted key
// (the placeholder ArmorPlaceholder over it while empty), Choose File…,
// Cancel and Import, active only with something to import
// (signature.PastedParams).
func (p *securityPage) openImport() {
	if p.closed || p.c == nil {
		return
	}
	t := signature.ImportDialogTexts(i18n.Tr)
	dlg := adw.NewAlertDialog(t.Title, t.Body)
	dlg.SetHeadingUseMarkup(false)
	dlg.SetBodyUseMarkup(false)
	dlg.AddResponse("cancel", t.Cancel)
	dlg.AddResponse("import", t.Import)
	dlg.SetResponseAppearance("import", adw.ResponseSuggested)
	dlg.SetResponseEnabled("import", false)
	dlg.SetDefaultResponse("import")
	dlg.SetCloseResponse("cancel")

	view := gtk.NewTextView()
	view.SetMonospace(true)
	view.SetWrapMode(gtk.WrapChar)
	view.SetAcceptsTab(false)
	view.SetTopMargin(6)
	view.SetBottomMargin(6)
	view.SetLeftMargin(6)
	view.SetRightMargin(6)
	placeholder := plainKeyLabel(signature.ArmorPlaceholder, "monospace", "dim-label")
	placeholder.SetVAlign(gtk.AlignStart)
	placeholder.SetMarginTop(6)
	placeholder.SetMarginStart(6)
	placeholder.SetCanTarget(false)
	placeholder.SetEllipsize(pango.EllipsizeEnd)
	scroller := gtk.NewScrolledWindow()
	scroller.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
	scroller.SetMinContentHeight(160)
	scroller.SetChild(view)
	scroller.AddCSSClass("card")
	overlay := gtk.NewOverlay()
	overlay.SetChild(scroller)
	overlay.AddOverlay(placeholder)
	buffer := view.Buffer()
	pasted := func() string {
		start, end := buffer.Bounds()
		return buffer.Text(start, end, false)
	}
	buffer.ConnectChanged(func() {
		text := pasted()
		placeholder.SetVisible(text == "")
		_, ok := signature.PastedParams(text)
		dlg.SetResponseEnabled("import", ok)
	})

	choose := gtk.NewButtonWithMnemonic(t.ChooseFile)
	choose.SetHAlign(gtk.AlignStart)
	choose.ConnectClicked(func() {
		dlg.Close()
		p.chooseFile(t)
	})

	extra := gtk.NewBox(gtk.OrientationVertical, 12)
	extra.Append(overlay)
	extra.Append(choose)
	dlg.SetExtraChild(extra)
	dlg.ConnectResponse(func(r string) {
		if r != "import" {
			return
		}
		if params, ok := signature.PastedParams(pasted()); ok {
			p.importKey(params)
		}
	})
	dlg.Present(p.d)
	view.GrabFocus()
}

// chooseFile picks a key file (the OpenPGP keys filter: *.asc, *.gpg,
// *.pgp, *.key) and imports it: at most api.MaxPGPKeyBytes + 1 bytes are
// read, more is ImportFailedText(tooLarge) without asking the daemon.
func (p *securityPage) chooseFile(t signature.ImportDialog) {
	dlg := gtk.NewFileDialog()
	dlg.SetTitle(t.Title)
	filter := gtk.NewFileFilter()
	filter.SetName(t.FileFilter)
	for _, pat := range []string{"*.asc", "*.gpg", "*.pgp", "*.key"} {
		filter.AddPattern(pat)
	}
	filters := gio.NewListStore(gtk.GTypeFileFilter)
	filters.Append(filter.Object)
	dlg.SetFilters(filters)
	dlg.SetDefaultFilter(filter)
	// The preferences are a dialog, not a window: no parent.
	dlg.Open(context.Background(), nil, func(r gio.AsyncResulter) {
		f, err := dlg.OpenFinish(r)
		if err != nil || p.closed {
			return // dismissed
		}
		path := f.Path()
		if path == "" {
			p.d.AddToast(widget.PlainToast(signature.ImportFailedText("", i18n.Tr)))
			return
		}
		go func() {
			data, readErr := readKeyFile(path)
			glib.IdleAdd(func() {
				if p.closed {
					return
				}
				if readErr != nil {
					p.d.log.Info("reading a key file failed", "err", readErr)
					p.d.AddToast(widget.PlainToast(signature.ImportFailedText("", i18n.Tr)))
					return
				}
				if len(data) > api.MaxPGPKeyBytes {
					p.d.AddToast(widget.PlainToast(signature.ImportFailedText(api.PGPImportTooLarge, i18n.Tr)))
					return
				}
				params, ok := signature.FileParams(data)
				if !ok {
					p.d.AddToast(widget.PlainToast(signature.ImportFailedText(api.PGPImportNoKey, i18n.Tr)))
					return
				}
				p.importKey(params)
			})
		}()
	})
}

// readKeyFile reads at most api.MaxPGPKeyBytes + 1 bytes of path: one
// more than the limit says it is too large.
func readKeyFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, api.MaxPGPKeyBytes+1))
}

// importKey runs pgp.keyImport: the toast says what was imported
// (ImportedText) or why not (ImportFailedText of its class, the general
// sentence for any other error); the list is asked again.
func (p *securityPage) importKey(params api.PGPKeyImportParams) {
	c := p.c
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		var res api.PGPKeyImportResult
		err := c.Call(ctx, api.MethodPGPKeyImport, params, &res)
		glib.IdleAdd(func() {
			if p.closed {
				return
			}
			if err != nil {
				p.d.log.Info("pgp.keyImport failed", "reason", string(signature.ImportFailureOf(err)))
				p.d.AddToast(widget.PlainToast(signature.ImportFailedText(signature.ImportFailureOf(err), i18n.Tr)))
			} else {
				p.d.AddToast(widget.PlainToast(signature.ImportedText(res, i18n.Tr)))
			}
			p.load()
		})
	}()
}

// keyTitles are the key rows' titles as a test reads them.
func (p *securityPage) keyTitles() []string {
	var out []string
	for _, r := range p.rows {
		box, ok := r.Child().(*gtk.Box)
		if !ok {
			continue
		}
		text, ok := box.FirstChild().(*gtk.Box)
		if !ok {
			continue
		}
		if l, ok := text.FirstChild().(*gtk.Label); ok {
			out = append(out, strings.TrimSpace(l.Text()))
		}
	}
	return out
}
