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
}
