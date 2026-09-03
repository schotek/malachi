// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package data embeds compiled UI definitions.
//
// Source of truth are the Blueprint files in data/ui/*.blp. They are
// compiled to GtkBuilder XML (*.ui) by blueprint-compiler at build time
// (`make blueprint` or `go generate ./...`); the generated *.ui files are
// git-ignored and embedded here.
package data

import (
	"embed"
	"fmt"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/i18n"
)

//go:generate blueprint-compiler batch-compile ui ui ui/window.blp ui/message_window.blp ui/message_row.blp ui/preferences.blp ui/editor.blp ui/compose.blp

//go:embed ui/*.ui
var ui embed.FS

// UI returns the GtkBuilder XML for the named definition (e.g. "window.ui").
func UI(name string) (string, error) {
	b, err := ui.ReadFile("ui/" + name)
	if err != nil {
		return "", fmt.Errorf("ui definition %q not embedded (run `make blueprint`): %w", name, err)
	}
	return string(b), nil
}

// MustUI is UI for definitions that ship with the binary.
func MustUI(name string) string {
	s, err := UI(name)
	if err != nil {
		panic(err)
	}
	return s
}

// Builder parses the named definition with the application's translation
// domain, so _("…") strings in Blueprint come out translated. Every window
// and widget must be built through it, never with NewBuilderFromString.
func Builder(name string) *gtk.Builder {
	b := gtk.NewBuilder()
	b.SetTranslationDomain(i18n.Domain)
	if err := b.AddFromString(MustUI(name)); err != nil {
		panic(fmt.Errorf("ui definition %q: %w", name, err))
	}
	return b
}
