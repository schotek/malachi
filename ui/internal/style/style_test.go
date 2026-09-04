// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package style

import (
	"strings"
	"testing"
)

func TestCSS(t *testing.T) {
	css := CSS(120, false, false)
	if !strings.Contains(css, "row.folder-row") {
		t.Error("folder row density rule missing")
	}
	if !strings.Contains(css, ".message-body { font-size: 120%; }") {
		t.Errorf("zoom rule missing:\n%s", css)
	}
	if strings.Contains(css, "monospace") {
		t.Errorf("monospace rule present when disabled:\n%s", css)
	}
	if strings.Contains(css, "avatar") {
		t.Errorf("avatar rule present when monochrome disabled:\n%s", css)
	}
	if !strings.Contains(CSS(100, true, false), "font-family: monospace") {
		t.Error("monospace rule missing when enabled")
	}
	if !strings.Contains(CSS(100, false, true), "avatar { background-image: none;") {
		t.Error("monochrome avatar rule missing when enabled")
	}
	// The message separator and account reordering rules do not depend on any
	// setting.
	for _, want := range []string{
		"list.message-list > row { border-radius: 0; margin: 0; }",
		"list.message-list > row:last-child { border-bottom: none; }",
		"row.account-row.drop-above", "row.account-row.drop-below", "@accent_bg_color", ".drag-handle"} {
		for _, got := range []string{CSS(100, false, false), CSS(150, true, true)} {
			if !strings.Contains(got, want) {
				t.Errorf("reorder rule %q missing:\n%s", want, got)
			}
		}
	}
}
