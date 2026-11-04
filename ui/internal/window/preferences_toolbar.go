// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/toolbar"
)

// Preferences → General → Toolbar (T13): the display mode, one for every
// toolbar (toolbar-display-mode), and Customize Toolbar…, which opens the
// editor (toolbar_editor.go) on the bar the main window shows. Built in Go
// at the end of the page, so preferences.blp holds no row of its own;
// every text is ui/internal/toolbar's.

// toolbarHome is what the editor opened from Preferences starts with: the
// main window's bar and its features (set by the main window's toolbar,
// toolbar_mail.go); nil before there is a main window.
var toolbarHome func() (toolbar.Bar, func() toolbar.Features)

// bindToolbar adds the Toolbar group to the General page of b.
func (d *PreferencesDialog) bindToolbar(b *gtk.Builder, s *settings.Store) (unbind func()) {
	tr := i18n.Tr
	page := b.GetObject("general_page").Cast().(*adw.PreferencesPage)
	g := adw.NewPreferencesGroup()
	g.SetTitle(toolbar.GroupTitle(tr))

	mode := adw.NewComboRow()
	mode.SetUseMarkup(false)
	mode.SetTitle(toolbar.DisplayModeTitle(tr))
	labels := make([]string, len(toolbar.DisplayModes))
	for i, m := range toolbar.DisplayModes {
		labels[i] = toolbar.DisplayModeLabel(tr, m)
	}
	mode.SetModel(gtk.NewStringList(labels))
	g.Add(mode)

	customize := adw.NewButtonRow()
	customize.SetTitle(toolbar.CustomizeTitle(tr))
	customize.SetEndIconName("go-next-symbolic")
	customize.ConnectActivated(func() {
		bar, features := toolbar.BarMail, func() toolbar.Features { return toolbar.Features{} }
		if toolbarHome != nil {
			bar, features = toolbarHome()
		}
		newToolbarEditor(s, features, bar).dialog.Present(d)
	})
	g.Add(customize)
	page.Add(g)
	d.toolbarGroup, d.toolbarMode = g, mode

	return bindChoice(s, toolbar.DisplayModeKey, mode, toolbar.DisplayModes, s.ToolbarDisplayMode, s.SetToolbarDisplayMode)
}
