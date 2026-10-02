// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/client"
)

// Optional WebKit card integration within TestBoardGTKSmoke. Every body is
// invented safe markup in the cache; the RPC client is never connected.
func smokeBoardConversationCards(t *testing.T, p *boardPage, flush func()) {
	t.Helper()
	rpc := client.New(filepath.Join(t.TempDir(), "absent.sock"))
	defer rpc.Close()
	p.w.client = rpc
	p.w.loaded = make(map[api.MessageID]*loadedMessage)
	defer func() { p.w.client = nil }()
	d := board.Detail{ID: "smoke-html-case", AccountID: "smoke-account", ConversationTitle: "Invented conversation"}
	for i := 0; i < 6; i++ {
		id := api.MessageID(fmt.Sprintf("smoke-card-%d", i))
		d.Messages = append(d.Messages, board.MessageCard{ID: id, From: "Example sender", Text: fmt.Sprintf("Excerpt %d", i)})
		p.w.loaded[id] = &loadedMessage{account: d.AccountID, body: &api.MessageBodyResult{
			MessageID: id, BodyState: api.BodyFetched, HasHTML: true,
			HTML: fmt.Sprintf("<p>Invented safe card %d</p>", i),
		}}
	}
	p.renderDetailConversation(d)
	b := p.conversation
	defer func() {
		b.close()
		removeAllChildren(p.conversationBox)
		p.conversation = nil
	}()
	p.detailStack.SetVisibleChildName("detail")
	deadline := time.Now().Add(10 * time.Second)
	for !p.conversationBox.Mapped() && time.Now().Before(deadline) {
		flush()
		time.Sleep(10 * time.Millisecond)
	}
	b.refresh()
	flush()
	if len(b.cards) != len(d.Messages) || b.cards[5].web == nil {
		t.Fatal("newest open card did not display its cached HTML")
	}
	first, newest, web := b.cards[0], b.cards[5], b.cards[5].web
	for i := 0; i < 3; i++ {
		b.apply(&d)
		flush()
	}
	if b.cards[0] != first || b.cards[5] != newest || newest.web != web {
		t.Fatal("unchanged board refresh replaced a card or its web view")
	}
	oldMember := board.ConversationMember{ID: d.Messages[5].ID, Text: d.Messages[5].Text}
	d.Messages[5].Text = "Updated excerpt for the same message"
	p.w.loaded[d.Messages[5].ID] = &loadedMessage{account: d.AccountID, body: &api.MessageBodyResult{
		MessageID: d.Messages[5].ID, BodyState: api.BodyFetched, HasHTML: true,
		HTML: "<p>Updated invented safe card</p>",
	}}
	b.apply(&d)
	flush()
	if b.cards[5] != newest || newest.web != web {
		t.Fatal("changed excerpt rebuilt its card instead of reloading in place")
	}
	// A late error for the old excerpt must not displace the newer HTML.
	b.bodyArrived(oldMember, &loadedMessage{err: fmt.Errorf("old request failed")})
	if newest.web != web {
		t.Fatal("stale body answer replaced the current member's view")
	}
	for i := 0; i < len(b.cards)-1; i++ {
		b.userFold(b.cards[i], false)
		flush()
		live := 0
		for _, c := range b.cards {
			if c.web != nil {
				live++
			}
		}
		if live > board.ConversationMaxLiveWebViews || newest.web == nil {
			t.Fatalf("HTML card limit violated: %d live, newest live=%v", live, newest.web != nil)
		}
	}
	// A refused body replaces only that card with its native excerpt.
	b.bodyArrived(board.ConversationMember{ID: d.Messages[5].ID, Text: d.Messages[5].Text}, &loadedMessage{body: &api.MessageBodyResult{
		MessageID: d.Messages[5].ID, BodyState: api.BodyFetched, HasHTML: true, HTMLWithheld: true,
	}})
	if newest.web != nil {
		t.Fatal("withheld HTML retained its old web view")
	}
	p.w.mode = board.ModeMail
	b.refresh()
	for _, c := range b.cards {
		if c.web != nil {
			t.Fatal("leaving Board retained a card web view")
		}
	}
	p.w.mode = board.ModeBoard
	b.close()
	flush()
}
