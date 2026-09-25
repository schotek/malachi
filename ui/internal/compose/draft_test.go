// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package compose

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

func TestBlockedSummary(t *testing.T) {
	if got := blockedSummary(api.BlockedContent{}); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := blockedSummary(api.BlockedContent{RemoteImages: 2, Scripts: 1}); got != "3 unsafe elements were removed from the message" {
		t.Errorf("plural: got %q", got)
	}
	if got := blockedSummary(api.BlockedContent{Forms: 1}); got != "1 unsafe element was removed from the message" {
		t.Errorf("singular: got %q", got)
	}
}

func TestFlushEcho(t *testing.T) {
	var f flushEcho
	if f.echo("") {
		t.Fatal("nothing recorded: an empty body is an edit, not an echo")
	}

	f.record("")
	if !f.echo("") {
		t.Error("an empty body flushed: its changed is the echo")
	}

	f.record("<p>a</p>")
	if !f.echo("<p>a</p>") {
		t.Error("the flush's own changed must be an echo")
	}
	if !f.echo("<p>a</p>") {
		t.Error("a late debounced changed with the same content must stay an echo")
	}
	if f.echo("<p>ab</p>") {
		t.Error("new content is an edit")
	}
	if f.echo("<p>a</p>") {
		t.Error("an edit forgets the record: going back to the saved text is an edit too")
	}
}
