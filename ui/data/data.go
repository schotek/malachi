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
)

//go:generate blueprint-compiler batch-compile ui ui ui/window.blp ui/message_window.blp ui/message_row.blp ui/preferences.blp

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
