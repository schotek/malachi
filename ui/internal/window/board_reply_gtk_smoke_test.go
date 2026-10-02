// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/boardreply"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/compose"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
)

// The optional WebKit part of TestBoardGTKSmoke loads an invented draft
// through a fake caller. Compose's own client remains disconnected, so no
// test action can read or write a real mailbox.
type smokeReplyCaller struct{ gets atomic.Int32 }

func (c *smokeReplyCaller) Call(_ context.Context, method string, params, result any) error {
	if method != api.MethodDraftGet {
		return fmt.Errorf("unexpected smoke RPC %s", method)
	}
	c.gets.Add(1)
	p := params.(api.DraftGetParams)
	*result.(*api.DraftGetResult) = api.DraftGetResult{Draft: api.Draft{
		ID: p.DraftID, AccountID: p.AccountID, Version: 1, Local: true,
		To:      []api.Address{{Address: "smoke@example.invalid"}},
		Subject: "Smoke reply", TextBody: "An invented reply", HTMLBody: "<p>An invented reply</p>",
		InReplyTo: "smoke-message",
	}}
	return nil
}

func smokeBoardReplyEditor(t *testing.T, p *boardPage, src *board.InMemorySource, flush func()) {
	t.Helper()
	rpc := client.New(filepath.Join(t.TempDir(), "absent.sock"))
	defer rpc.Close()
	p.w.compose = compose.NewManager(nil, rpc, p.w.log, settings.NewMemory())
	fake := &smokeReplyCaller{}
	p.replyEditor = boardreply.NewEditor(fake, glibLoop{}, p.w.log)
	panes := boardreply.NewPanes[*compose.Pane](p.replyEditor, boardreply.Timing{}, glibLoop{}, i18n.Tr)
	p.replyPanes = panes
	panes.Make, panes.Detach, panes.OnChange = p.makeBoardReply, p.releaseBoardReply, p.boardReplySlotChanged
	defer p.w.CloseBoardReplies()
	snapshot := src.Snapshot()
	var selected board.CaseID
	for i := range snapshot.Cases {
		c := &snapshot.Cases[i]
		c.Draft = nil
		if selected == "" && !c.Done() {
			selected = c.ID
			c.Reply = &board.ReplyTarget{Message: "smoke-message", Folder: "smoke-inbox"}
			c.Draft = &board.DraftLink{ID: "smoke-draft"}
		}
	}
	if selected == "" {
		t.Fatal("no replyable sample case")
	}
	src.Replace(snapshot)
	p.ctl.Select(selected)
	p.applyAll()
	deadline := time.Now().Add(10 * time.Second)
	for p.replyShown == nil && time.Now().Before(deadline) {
		flush()
		time.Sleep(10 * time.Millisecond)
	}
	pane := p.replyShown
	if pane == nil {
		t.Fatal("linked draft never became an inline compose pane")
	}
	if !p.replySlot.Visible() {
		t.Fatal("inline pane slot is hidden")
	}
	ready := pane.FocusEditorStart()
	for !ready && time.Now().Before(deadline) {
		flush()
		time.Sleep(10 * time.Millisecond)
		ready = pane.FocusEditorStart()
	}
	if !ready {
		t.Fatal("inline WebKit editor never became ready")
	}
	// Refreshing the same linked draft must keep the existing editor and
	// must not fetch or replace the text that the user may be editing.
	for i := range snapshot.Cases {
		snapshot.Cases[i].Version++
	}
	src.Replace(snapshot)
	p.applyAll()
	flush()
	if p.replyShown != pane || fake.gets.Load() != 1 {
		t.Fatal("source refresh rebuilt or reloaded the editor")
	}
	for _, style := range []board.Style{board.StyleColumns, board.StyleToday, board.StyleList} {
		p.ctl.SetStyle(style)
		p.ctl.Select(selected)
		p.applyAll()
		flush()
		if p.replyShown != pane {
			t.Fatalf("style %s replaced the same draft's editor", style.Nick())
		}
	}
	// Keyboard routing from Reply reaches the existing pane.
	p.focusBoardReply(selected)
	flush()
	if p.replyShown != pane {
		t.Fatal("Reply replaced its own inline editor")
	}
	finished, saved := false, false
	p.w.FinishBoardReplies(func(ok bool) { finished, saved = true, ok })
	for !finished && time.Now().Before(deadline) {
		flush()
		time.Sleep(10 * time.Millisecond)
	}
	if !finished || !saved {
		t.Fatal("untouched reply did not settle without writing to the disconnected daemon")
	}
	// Teardown detaches every pane and invalidates pending callbacks, even
	// when the editor's web process is still starting.
	p.w.CloseBoardReplies()
	if len(panes.All()) != 0 || p.replyShown != nil {
		t.Fatal("closed board retained an inline pane")
	}
	flush()
}
