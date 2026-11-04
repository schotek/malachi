// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"slices"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/diamondburned/gotk4/pkg/pango"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/updates"
	"github.com/schotek/malachi/ui/internal/updatesctl"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The update check (docs/api.md §4.20, docs/plans/update-check.md): the
// daemon checks, ui/internal/updates turns its status into texts, and
// ui/internal/updatesctl keeps the one status of the application. The
// main window shows the banner across its top, above the header bars of
// every mode and of the No Accounts page (as the macOS card), asks a
// security release's question once, and the first-run note sits under the
// No Accounts page's buttons. Preferences → General → Updates is
// preferences_updates.go.
//
// This file has no translatable text and is not in po/POTFILES: every
// text is ui/internal/updates'.

var appUpdates *updatesctl.Store

// updateStore is the application's update status over c, made on first
// use; the main window feeds it the connection and the notifications.
func updateStore(c *client.Client, log *slog.Logger) *updatesctl.Store {
	if appUpdates == nil {
		locale := updatesctl.Locale(glib.GetLanguageNames(), catalogLocales())
		appUpdates = updatesctl.NewStore(c, glibLoop{}, locale, log.With("part", "updates"))
	}
	return appUpdates
}

// catalogLocales are the languages po/LINGUAS ships (catalogLanguages).
func catalogLocales() []string {
	out := make([]string, 0, len(catalogLanguages))
	for l := range catalogLanguages {
		out = append(out, l)
	}
	return out
}

// updateDate formats a release date ("14 October 2026").
func updateDate(t time.Time) string {
	return calendar.DayMonthYearText(t, calendar.Env{Tr: i18n.Tr, Format: widget.Strftime, Loc: time.Local})
}

// updateCheckedAt formats the time of the last check.
func updateCheckedAt(t time.Time) string { return widget.FormatDateTime(t) }

// updateView is the main window's part of the update check.
type updateView struct {
	w     *Window
	store *updatesctl.Store
	bars  []*updateBar
	// banner is what the bars show.
	banner updates.Banner
}

// setupUpdates builds the bar and the first-run note from the window's
// builder and follows the store. The bar is window.blp's update_bar, across
// the top of the whole window above the header bars (the owner's choice,
// 2026-11-02, as the macOS card): one bar serves Mail, the Board, the
// Calendar and the no-accounts page.
func (w *Window) setupUpdates(b *gtk.Builder) {
	v := &updateView{w: w, store: updateStore(w.client, w.log)}
	w.updates = v
	if slot, ok := b.GetObject("update_bar").Cast().(*adw.Bin); ok {
		bar := v.newBar()
		slot.SetChild(bar.root)
	}
	if note, ok := b.GetObject("no_accounts_update_note").Cast().(*gtk.Label); ok {
		note.SetUseMarkup(false)
		note.SetText(updates.WizardNote(i18n.Tr))
	}
	v.store.Observe(v.render)
	// A question waits while the window is hidden (Run in Background).
	w.NotifyProperty("visible", v.render)
	v.render()
}

// updatesConnectionChanged: the connection came or went
// (modeConnectionChanged).
func (w *Window) updatesConnectionChanged(connected bool) {
	if w.updates != nil {
		w.updates.store.ConnectionChanged(connected)
	}
}

// updatesNotification takes notify.updateChanged (otherNotification).
func (w *Window) updatesNotification(method string) bool {
	if w.updates == nil {
		return false
	}
	return w.updates.store.Handle(method)
}

// render shows the store's status: the bars, and the security question
// once the window is on screen.
func (v *updateView) render() {
	v.banner = updates.BannerFor(v.store.Status(), time.Now(), i18n.Tr)
	for _, b := range v.bars {
		b.show(v.banner)
	}
	if !v.w.IsVisible() {
		return
	}
	if p := v.store.TakePrompt(i18n.Tr); p != nil {
		v.ask(p)
	}
}

// ask is the security release's question: Download (the default) opens
// the installer and, with the check box on, quits through app.quit, the
// menu's Quit with its questions about unsaved work. Whatever the answer,
// update.dismiss {version, "prompted"} tells the other clients that it
// was asked; the security banner stays either way.
func (v *updateView) ask(p *updates.Prompt) {
	d := adw.NewAlertDialog(p.Title, p.Body)
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	quit := gtk.NewCheckButtonWithLabel(p.Quit)
	quit.SetActive(true)
	d.SetExtraChild(quit)
	d.AddResponse("cancel", p.Cancel)
	d.AddResponse("download", p.Download)
	d.SetResponseAppearance("download", adw.ResponseSuggested)
	d.SetDefaultResponse("download")
	d.SetCloseResponse("cancel")
	d.ConnectResponse(func(response string) {
		v.store.Dismiss(p.Version, api.UpdatePrompted)
		if response != "download" {
			return
		}
		thenQuit := quit.Active()
		v.open(p.URL, func() {
			if thenQuit {
				if app := v.w.Application(); app != nil {
					app.ActivateAction("quit", nil)
				}
			}
		})
	})
	d.Present(&v.w.ApplicationWindow.Window)
}

// open opens raw in the browser when updates.OpenableURL accepts it;
// opened runs once the browser took it.
func (v *updateView) open(raw string, opened func()) {
	uri, ok := updates.OpenableURL(raw)
	if !ok {
		return
	}
	widget.LaunchURI(&v.w.ApplicationWindow.Window, uri, func(err error) {
		if err != nil {
			v.w.log.Warn("open link", "err", err)
			v.w.Toast(widget.LaunchErrorText(err))
			return
		}
		if opened != nil {
			opened()
		}
	})
}

// act runs a banner button's action.
func (v *updateView) act(b updates.Banner, a updates.Action) {
	switch a {
	case updates.ActionDownload, updates.ActionMoreInfo:
		v.open(b.URL, nil)
	case updates.ActionWhatsNew:
		v.whatsNew()
	case updates.ActionLater:
		v.store.Dismiss(b.Version, api.UpdateLater)
	case updates.ActionSkip:
		v.store.Dismiss(b.Version, api.UpdateSkip)
	case updates.ActionClose:
		v.store.DismissNotice(b.NoticeID)
	}
}

// whatsNew is the What's New dialog: the release date under the title and
// the notes read as Markdown, with Download and More Information (the
// project's news) where they apply.
func (v *updateView) whatsNew() {
	n := updates.NotesFor(v.store.Status(), updateDate, i18n.Tr)
	if n == nil {
		return
	}
	d := adw.NewAlertDialog(n.Title, n.Subtitle)
	d.SetHeadingUseMarkup(false)
	d.SetBodyUseMarkup(false)
	if len(n.Blocks) > 0 {
		// The notes as Markdown, drawn like the assistant's answers
		// (text tags, never markup); links are plain text.
		body := gtk.NewTextView()
		body.SetEditable(false)
		body.SetCursorVisible(false)
		body.SetWrapMode(gtk.WrapWordChar)
		body.AddCSSClass("inline")
		newAnswerView(body).renderBlocks(n.Blocks)
		scroll := gtk.NewScrolledWindow()
		scroll.SetPolicy(gtk.PolicyNever, gtk.PolicyAutomatic)
		scroll.SetPropagateNaturalHeight(true)
		scroll.SetMaxContentHeight(360)
		scroll.SetChild(body)
		d.SetExtraChild(scroll)
	}
	d.AddResponse("close", n.Close)
	d.SetDefaultResponse("close")
	d.SetCloseResponse("close")
	if n.InfoURL != "" {
		d.AddResponse("info", n.MoreInfo)
	}
	if n.URL != "" {
		d.AddResponse("download", n.Download)
		d.SetResponseAppearance("download", adw.ResponseSuggested)
		d.SetDefaultResponse("download")
	}
	d.ConnectResponse(func(response string) {
		switch response {
		case "download":
			v.open(n.URL, nil)
		case "info":
			v.open(n.InfoURL, nil)
		}
	})
	d.Present(&v.w.ApplicationWindow.Window)
}

// updateBar is one banner: the title, which wraps, over the buttons in the
// banner's order, the first the suggested one. Adw.Banner has room for
// one button, hence a bar of our own (styled as remote_bar in window.blp).
// The buttons stand under the title, not beside it: beside a wrapping
// title the four buttons took their two-column width in the list's column
// and left the title a few words per line.
type updateBar struct {
	root    *gtk.Box
	title   *gtk.Label
	buttons *adw.WrapBox
	act     func(updates.Action)
	kind    updates.BannerKind
	shown   []updates.Button
	made    []*gtk.Button
}

func (v *updateView) newBar() *updateBar {
	b := &updateBar{
		root:    gtk.NewBox(gtk.OrientationVertical, 6),
		title:   gtk.NewLabel(""),
		buttons: adw.NewWrapBox(),
	}
	b.root.AddCSSClass("toolbar")
	b.root.AddCSSClass("update-bar")
	b.root.SetVisible(false)
	b.title.SetUseMarkup(false)
	b.title.SetWrap(true)
	b.title.SetWrapMode(pango.WrapWordChar)
	b.title.SetXAlign(0)
	b.title.SetHExpand(true)
	b.buttons.SetChildSpacing(6)
	b.buttons.SetLineSpacing(6)
	b.buttons.SetHAlign(gtk.AlignStart)
	b.root.Append(b.title)
	b.root.Append(b.buttons)
	b.act = func(a updates.Action) { v.act(v.banner, a) }
	v.bars = append(v.bars, b)
	return b
}

// show makes the bar show banner (hidden for none); the buttons are made
// again only when they change.
func (b *updateBar) show(banner updates.Banner) {
	b.root.SetVisible(banner.Shown())
	if !banner.Shown() {
		return
	}
	b.title.SetText(banner.Title)
	if banner.Kind != b.kind {
		if banner.Kind == updates.BannerSecurity {
			b.root.AddCSSClass("update-security")
		} else {
			b.root.RemoveCSSClass("update-security")
		}
		b.kind = banner.Kind
	}
	if slices.Equal(b.shown, banner.Buttons) {
		return
	}
	b.shown = slices.Clone(banner.Buttons)
	for _, old := range b.made {
		b.buttons.Remove(old)
	}
	b.made = b.made[:0]
	for i, x := range banner.Buttons {
		button := gtk.NewButtonWithLabel(x.Label)
		if i == 0 {
			button.AddCSSClass("suggested-action")
		}
		a := x.Action
		button.ConnectClicked(func() { b.act(a) })
		b.buttons.Append(button)
		b.made = append(b.made, button)
	}
}

// UpdateAboutLine is the update check's status line for the About dialog
// (updates.AboutLine), "" before the application has a status.
func UpdateAboutLine() string {
	if appUpdates == nil || appUpdates.Status() == nil {
		return ""
	}
	return updates.AboutLine(appUpdates.Status(), updateCheckedAt, i18n.Tr)
}
