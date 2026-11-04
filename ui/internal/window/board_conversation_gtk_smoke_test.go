// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
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

// smokeBoardConversationChat switches the block to the chat layout through
// the shared setting (conversation-chat-layout) and back: one bubble per
// member on its side with the case's text, no card web view while the chat
// shows, the cards and (withCards: invented cached HTML, a client that is
// never connected) the newest card's web view back afterwards; a sample's
// bubble opens no window. A settings store of its own in memory, only for
// this block.
func smokeBoardConversationChat(t *testing.T, p *boardPage, flush func(), withCards bool) {
	t.Helper()
	w := p.w
	if p.conversation != nil {
		p.conversation.close()
		removeAllChildren(p.conversationBox)
		p.conversation = nil
	}
	saved := w.settings
	w.settings = settings.NewMemory()
	defer func() { w.settings = saved }()
	if withCards {
		rpc := client.New(filepath.Join(t.TempDir(), "absent.sock"))
		defer rpc.Close()
		w.client = rpc
		w.loaded = make(map[api.MessageID]*loadedMessage)
		defer func() { w.client = nil }()
	}
	now := time.Now()
	d := board.Detail{ID: "smoke-chat-case", AccountID: "smoke-account", ConversationTitle: "Invented chat"}
	for i := 0; i < 4; i++ {
		id := api.MessageID(fmt.Sprintf("smoke-chat-%d", i))
		mine := i%2 == 1
		m := board.MessageCard{ID: id, From: "Example sender", Sender: "Example sender",
			Date: now.Add(time.Duration(i-4) * time.Hour), When: "today", Text: fmt.Sprintf("Invented chat text %d\nsecond line", i), Mine: mine}
		if mine {
			m.From, m.Sender = board.You(i18n.Tr), "Smoke User"
		}
		d.Messages = append(d.Messages, m)
		if withCards {
			w.loaded[id] = &loadedMessage{account: d.AccountID, body: &api.MessageBodyResult{
				MessageID: id, BodyState: api.BodyFetched, HasHTML: true,
				HTML: fmt.Sprintf("<p>Invented safe chat card %d</p>", i),
			}}
		}
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
	if !b.chat.toggle.Visible() || b.chat.stack.VisibleChildName() != boardChatCards {
		t.Fatal("the block does not start with the cards and its chat toggle")
	}
	if got := b.chat.toggleContent.Label(); got != board.ChatToggleLabel(false, i18n.Tr) {
		t.Fatalf("toggle off says %q", got)
	}
	// A conversation still loading hides the cards' column; GtkStack then
	// shows the chat page and must be put back once the cards are there.
	loading := board.Detail{ID: d.ID, AccountID: d.AccountID, ConversationTitle: d.ConversationTitle, MessagesLoading: true}
	b.apply(&loading)
	flush()
	b.apply(&d)
	flush()
	if b.chat.stack.VisibleChildName() != boardChatCards || !b.column.Mapped() {
		t.Fatalf("after loading the block shows %q, the cards mapped %v", b.chat.stack.VisibleChildName(), b.column.Mapped())
	}
	cards := append([]*boardConversationCard(nil), b.cards...)
	if len(cards) != len(d.Messages) {
		t.Fatalf("%d cards for %d messages", len(cards), len(d.Messages))
	}
	if withCards && cards[len(cards)-1].web == nil {
		t.Fatal("newest card shows no web view before the chat")
	}

	w.settings.SetConversationChatLayout(true)
	flush()
	if b.chat.stack.VisibleChildName() != boardChatPage || !b.chat.toggle.Active() {
		t.Fatal("the setting did not switch the block to the chat")
	}
	if got := b.chat.toggleContent.Label(); got != board.ChatToggleLabel(true, i18n.Tr) {
		t.Fatalf("toggle on says %q", got)
	}
	if got := b.chat.toggle.TooltipText(); got != board.ChatToggleTooltip(true, i18n.Tr) {
		t.Fatalf("toggle on has the tooltip %q", got)
	}
	checkRows := func() {
		t.Helper()
		if len(b.chat.rows) != len(d.Messages) {
			t.Fatalf("%d bubbles for %d messages", len(b.chat.rows), len(d.Messages))
		}
		for i, r := range b.chat.rows {
			m := d.Messages[i]
			if r.right != m.Mine || r.Parent() == nil {
				t.Fatalf("bubble %d on the wrong side or not in the column", i)
			}
			if got := r.bubble.text.Text(); got != conversation.BubbleText(m.Text) {
				t.Fatalf("bubble %d shows %q", i, got)
			}
			if got := r.bubble.sender.Text(); got != m.From {
				t.Fatalf("bubble %d names %q", i, got)
			}
			if got := r.avatar.Text(); got != m.Sender {
				t.Fatalf("bubble %d's avatar is %q", i, got)
			}
		}
	}
	checkRows()
	for i, c := range b.cards {
		if c.web != nil {
			t.Fatalf("card %d kept its web view under the chat", i)
		}
	}
	rows := append([]*chatRow(nil), b.chat.rows...)
	b.apply(&d)
	flush()
	checkRows()
	for i, r := range b.chat.rows {
		if r != rows[i] {
			t.Fatal("an unchanged refresh rebuilt a bubble")
		}
	}
	t.Logf("chat: %d bubbles, bubble max %d px, compact %v", len(rows), b.chat.bubbleMax(), b.chat.compact)

	// The toggle writes the shared setting.
	b.chat.toggle.SetActive(false)
	flush()
	if w.settings.ConversationChatLayout() || b.chat.stack.VisibleChildName() != boardChatCards {
		t.Fatal("the toggle did not switch the setting and the block back to the cards")
	}
	for i, c := range b.cards {
		if c != cards[i] {
			t.Fatal("the chat replaced a card")
		}
	}
	if withCards && b.cards[len(b.cards)-1].web == nil {
		t.Fatal("the newest card did not show its web view again")
	}

	// A sample's bubble opens nothing.
	sample := board.Detail{ID: "smoke-chat-sample", ConversationTitle: "Sample chat",
		Messages: []board.MessageCard{{From: "Example sender", Sender: "Example sender", Text: "A sample"}}}
	b.chat.toggle.SetActive(true)
	b.apply(&sample)
	flush()
	if len(b.chat.rows) != 1 {
		t.Fatal("the sample shows no bubble")
	}
	before := len(w.openMessages)
	b.chat.activate(b.chat.rows[0])
	flush()
	if len(w.openMessages) != before {
		t.Fatal("a sample's bubble opened a window")
	}
	// Samples to ids and back: the column holds only the detail's bubbles.
	columnRows := func() int {
		n := 0
		for child := b.chat.column.FirstChild(); child != nil; child = gtk.BaseWidget(child).NextSibling() {
			n++
		}
		return n
	}
	for _, next := range []*board.Detail{&d, &sample} {
		b.apply(next)
		flush()
		if len(b.chat.rows) != len(next.Messages) || columnRows() != len(next.Messages) {
			t.Fatalf("%s: %d rows, %d in the column, for %d messages", next.ID, len(b.chat.rows), columnRows(), len(next.Messages))
		}
	}
	// Shortened bubbles: every bubble shortened, a click expands it, the
	// preference off shows them whole.
	b.apply(&d)
	flush()
	w.settings.SetConversationChatTruncateMine(true)
	w.settings.SetConversationChatTruncateOthers(true)
	flush()
	for i, r := range b.chat.rows {
		if !r.bubble.short || r.bubble.text.Lines() != conversation.ChatPreviewLines {
			t.Fatalf("bubble %d not shortened", i)
		}
	}
	first := b.chat.rows[0]
	first.bubble.onExpand()
	flush()
	if first.bubble.short || !b.chat.expanded.Is(d.Messages[0].ID) {
		t.Fatal("a click did not expand the bubble")
	}
	b.apply(&d)
	flush()
	if b.chat.rows[0].bubble.short {
		t.Fatal("a refresh of the case shortened the expanded bubble again")
	}
	// Mine off: only the others' bubbles (on the left) stay shortened.
	w.settings.SetConversationChatTruncateMine(false)
	flush()
	for i, r := range b.chat.rows {
		if i > 0 && r.bubble.short != !r.right {
			t.Fatalf("mine off: bubble %d (right %v) shortened %v", i, r.right, r.bubble.short)
		}
	}
	w.settings.SetConversationChatTruncateOthers(false)
	flush()
	for i, r := range b.chat.rows {
		if r.bubble.short {
			t.Fatalf("bubble %d shortened with both off", i)
		}
	}
	if withCards {
		smokeBoardChatHTML(t, b, &d, flush)
	}
	w.settings.SetConversationChatLayout(false)
	flush()
}

// smokeBoardChatHTML: with Render HTML in Chat on, the bubbles near the
// detail's visible part show their cached HTML in live views (at most the
// board's cap), and the preference off lets every view go, the case's
// text back. Real WebKit (the cards' environment).
func smokeBoardChatHTML(t *testing.T, b *boardConversation, d *board.Detail, flush func()) {
	t.Helper()
	w := b.p.w
	// Six members, all with cached HTML: more than the cap.
	d6 := *d
	d6.ID = "smoke-chat-html"
	d6.Messages = append([]board.MessageCard(nil), d.Messages...)
	for i := 4; i < 6; i++ {
		id := api.MessageID(fmt.Sprintf("smoke-chat-%d", i))
		d6.Messages = append(d6.Messages, board.MessageCard{ID: id, From: "Example sender", Sender: "Example sender",
			Date: time.Now(), When: "today", Text: fmt.Sprintf("Invented chat text %d", i)})
		w.loaded[id] = &loadedMessage{account: d6.AccountID, body: &api.MessageBodyResult{
			MessageID: id, BodyState: api.BodyFetched, HasHTML: true, HTML: fmt.Sprintf("<p>Invented safe chat card %d</p>", i)}}
	}
	w.settings.SetConversationChatLayout(true)
	b.apply(&d6)
	flush()
	countLive := func() int {
		n := 0
		for _, r := range b.chat.rows {
			if r.bubble.web != nil {
				n++
			}
		}
		return n
	}
	if n := countLive(); n != 0 {
		t.Fatalf("preference off: %d live views", n)
	}
	w.settings.SetConversationChatHTML(true)
	for i := 0; i < 5; i++ {
		flush()
		time.Sleep(50 * time.Millisecond)
	}
	n := countLive()
	t.Logf("chat html: %d live views of %d bubbles", n, len(b.chat.rows))
	if n == 0 || n > board.ConversationMaxLiveWebViews {
		t.Fatalf("preference on: %d live views (cap %d)", n, board.ConversationMaxLiveWebViews)
	}
	// The detail reads from the top, so the bubbles nearest the visible
	// part are not necessarily the newest; one of them shows its HTML.
	var shown *chatRow
	var shownIndex int
	for i, r := range b.chat.rows {
		if r.bubble.web != nil {
			shown, shownIndex = r, i
			break
		}
	}
	if shown == nil || shown.bubble.text.Visible() || !shown.fill || !shown.bubble.html.Visible() {
		t.Fatal("a live bubble does not show its HTML")
	}
	for i, c := range b.cards {
		if c.web != nil {
			t.Fatalf("card %d has a web view under the chat", i)
		}
	}
	w.settings.SetConversationChatHTML(false)
	flush()
	if n := countLive(); n != 0 {
		t.Fatalf("preference off again: %d live views", n)
	}
	if !shown.bubble.text.Visible() || shown.bubble.text.Text() != conversation.BubbleText(d6.Messages[shownIndex].Text) {
		t.Fatalf("the text did not come back: %q", shown.bubble.text.Text())
	}
	b.apply(d)
	flush()
}
