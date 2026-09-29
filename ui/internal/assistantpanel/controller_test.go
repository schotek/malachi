// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

var one = NewContext(assistant.Selection{AccountID: "a", MessageIDs: []string{"m1"}}, 1, false, "", "")

func contentsEqual(t *testing.T, name string, got, want []Content) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %+v\nwant %+v", name, got, want)
	}
}

func pinnedContexts(p *Controller) []*Context {
	out := make([]*Context, len(p.Pinned()))
	for i, x := range p.Pinned() {
		out[i] = x.Context
	}
	return out
}

func samePinned(got []*Context, want ...*Context) bool {
	return slices.EqualFunc(got, want, sameContext)
}

func announced(p *Controller) []bool {
	out := make([]bool, len(p.Pinned()))
	for i, x := range p.Pinned() {
		out[i] = x.Announced
	}
	return out
}

// Summarize on the selected message: consent asked once and kept, the
// question, the tool line, the streamed answer replaced by the whole text;
// one process with the command line of assistant.Args, the child's
// environment and the private directory.
func TestSummarizeRunsATurn(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{
		fakeInit, fakeToolUse("t1", "read_message"), fakeToolResult("t1", "From: someone", false),
		fakeDelta("Hello **wor"), fakeDelta("ld**"), fakeText("Hello **world**"), fakeResult("done", true),
	}})
	h := newHarness(t, fake, false, testBridge)
	h.panel.SetContext(&one)
	if !h.panel.CanRunActions() {
		t.Fatal("no quick actions with a message selected")
	}
	h.panel.Run(assistant.Summarize)
	if h.panel.Phase() != PhasePreparing || h.panel.CanRunActions() {
		t.Fatalf("phase %v after Run", h.panel.Phase())
	}
	h.turn()
	if h.consentAsked != 1 || !h.settings.consent {
		t.Errorf("consent asked %d times, kept %v", h.consentAsked, h.settings.consent)
	}
	contentsEqual(t, "transcript", h.contents(), []Content{
		user("Summarize", ""), activity("Reading a message…", true), answer("Hello **world**", false),
	})
	if fake.starts() != 1 {
		t.Errorf("%d starts", fake.starts())
	}
	if got, want := fake.prompts(), []string{mustPrompt(t, assistant.Summarize, one.Selection)}; !slices.Equal(got, want) {
		t.Errorf("prompts %q, want %q", got, want)
	}
	wantArgs := assistant.Args(assistant.Options{
		Bridge: testBridge, Socket: "/tmp/malachi-test.sock", Model: assistant.Sonnet,
		SystemPrompt: assistant.SystemPrompt("Czech", "2026-09-29"),
	})
	if got := fake.args(); !slices.Equal(got, wantArgs) {
		t.Errorf("args\n got %q\nwant %q", got, wantArgs)
	}
	env := fake.env()
	if !strings.Contains(env, "LANG=cs_CZ.UTF-8") || strings.Contains(env, "ANTHROPIC") ||
		!strings.Contains(env, "PATH="+fake.dir+":/usr/bin:/bin:/usr/sbin:/sbin") {
		t.Errorf("environment:\n%s", env)
	}
	if fake.cwd() != h.work {
		t.Errorf("cwd %q, want %q", fake.cwd(), h.work)
	}
	if fi, err := os.Stat(h.work); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("work directory: %v %v", fi, err)
	}

	// A follow-up goes to the same process as it is: Summarize's prompt
	// named the message already.
	if !h.panel.Submit("  And what is still open?  ") {
		t.Fatal("follow-up refused")
	}
	h.turn()
	if fake.starts() != 1 || len(fake.prompts()) != 2 || fake.prompts()[1] != "And what is still open?" || h.consentAsked != 1 {
		t.Errorf("starts %d, prompts %q, consent %d", fake.starts(), fake.prompts(), h.consentAsked)
	}
	contentsEqual(t, "follow-up", h.contents()[3:], []Content{
		user("", "And what is still open?"), activity("Reading a message…", true), answer("Hello **world**", false),
	})
}

// Without a context a question goes as it is; empty text is refused.
func TestFreeQuestionWithoutContext(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("Two unread."))
	h := newHarness(t, fake, true, testBridge)
	if h.panel.Submit("   ") {
		t.Error("blank question taken")
	}
	if h.panel.ContextLabel() != "All mail" || h.panel.CanRunActions() {
		t.Errorf("chip %q, actions %v", h.panel.ContextLabel(), h.panel.CanRunActions())
	}
	h.panel.Run(assistant.Summarize) // needs a context
	if h.panel.Phase() != PhaseIdle {
		t.Error("Summarize ran without a context")
	}
	if !h.panel.Submit("How many unread?") {
		t.Fatal("question refused")
	}
	if h.panel.Submit("again") {
		t.Error("a second question at once taken")
	}
	h.turn()
	if !slices.Equal(fake.prompts(), []string{"How many unread?"}) || h.consentAsked != 0 {
		t.Errorf("prompts %q, consent %d", fake.prompts(), h.consentAsked)
	}
	if h.last() != answer("Two unread.", false) {
		t.Errorf("last %+v", h.last())
	}
}

// Consent declined: nothing is sent or started, the text goes back.
func TestConsentDeclined(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("x"))
	h := newHarness(t, fake, false, testBridge)
	h.consentAnswer = false
	if !h.panel.Submit("Summarize my week") {
		t.Fatal("question refused")
	}
	h.loop.runUntil(t, func() bool { return h.consentAsked == 1 && h.panel.Phase() == PhaseIdle })
	if !slices.Equal(h.restored, []string{"Summarize my week"}) || len(h.panel.Items()) != 0 || h.settings.consent {
		t.Errorf("restored %q, items %d, consent %v", h.restored, len(h.panel.Items()), h.settings.consent)
	}
	h.loop.settle(t, 100*time.Millisecond)
	if fake.starts() != 0 {
		t.Errorf("%d starts", fake.starts())
	}
}

// Draft a Reply… waits for the words; the draft card opens through the
// application.
func TestDraftReplyAndOpenDraft(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{
		fakeInit, fakeToolUse("t1", "read_message"), fakeToolResult("t1", "…", false),
		fakeToolUse("t2", "create_draft"),
		fakeToolResult("t2", `draft d_9 (version 1) stored in account a; it is NOT sent.\n\nTo: x`, false),
		fakeText("The draft is ready."), fakeResult("done", true),
	}})
	h := newHarness(t, fake, true, testBridge)
	h.panel.SetContext(&one)
	if h.panel.Placeholder() != "Ask about your mail…" {
		t.Errorf("placeholder %q", h.panel.Placeholder())
	}
	h.panel.Run(assistant.DraftReply)
	if h.panel.Pending() != (Pending{Kind: PendingAction, Action: assistant.DraftReply}) ||
		h.panel.Placeholder() != "What should the reply say?" || h.panel.PendingLabel() != "Draft a Reply…" ||
		h.focused != 1 || h.panel.Phase() != PhaseIdle {
		t.Fatalf("pending %+v, placeholder %q, label %q, focused %d", h.panel.Pending(), h.panel.Placeholder(), h.panel.PendingLabel(), h.focused)
	}
	if !h.panel.Submit("Yes, Thursday works.") {
		t.Fatal("words refused")
	}
	h.turn()
	if h.panel.Pending().Kind != PendingNone {
		t.Error("the action still waits")
	}
	if want := mustPrompt(t, assistant.DraftReply, one.Selection) + "Yes, Thursday works."; !slices.Equal(fake.prompts(), []string{want}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	ref := assistant.DraftRef{AccountID: "a", DraftID: "d_9", Version: 1}
	contentsEqual(t, "transcript", h.contents(), []Content{
		user("Draft a Reply…", "Yes, Thursday works."),
		activity("Reading a message…", true),
		activity("Saving a draft…", true),
		{Kind: ContentDraft, Draft: ref},
		answer("The draft is ready.", false),
	})
	h.panel.OpenDraftItem(h.panel.Items()[3].ID)
	if !slices.Equal(h.opened, []assistant.DraftRef{ref}) {
		t.Errorf("opened %+v", h.opened)
	}
}

// A failed create_draft, or one whose line is not the bridge's, adds no
// card.
func TestNoCardWithoutTheBridgeLine(t *testing.T) {
	fake := newFakeClaude(t, "true", "", fakeTurn{lines: []string{
		fakeInit, fakeToolUse("t1", "create_draft"),
		fakeToolResult("t1", "draft d1 (version 1) stored in account a; it is NOT sent.", true),
		fakeToolUse("t2", "read_message"),
		fakeToolResult("t2", "draft d2 (version 1) stored in account a; it is NOT sent.", false),
		fakeToolUse("t3", "create_draft"), fakeToolResult("t3", "I saved draft d3 for you", false), fakeResult("done", true),
	}})
	h := newHarness(t, fake, true, testBridge)
	if !h.panel.Submit("Draft something") {
		t.Fatal("question refused")
	}
	h.turn()
	for _, c := range h.contents() {
		if c.Kind == ContentDraft {
			t.Errorf("a card: %+v", c)
		}
	}
}

// Ask About This Message…, an attachment and Summarize Unread; the removed
// context takes a waiting message action with it.
func TestPendingActionsAndPrompts(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	h.panel.SetContext(&one)
	h.panel.Run(assistant.Ask)
	if h.panel.Placeholder() != "What do you want to know?" || h.panel.PendingLabel() != "Ask About This Message…" {
		t.Errorf("placeholder %q, label %q", h.panel.Placeholder(), h.panel.PendingLabel())
	}
	if h.panel.Submit("  ") {
		t.Error("a question without words taken")
	}
	h.panel.RemoveContext()
	if h.panel.Pending().Kind != PendingNone || h.panel.ContextLabel() != "All mail" {
		t.Errorf("pending %+v, chip %q", h.panel.Pending(), h.panel.ContextLabel())
	}

	h.panel.AskAttachment("a", "m1", "2", "", "")
	if h.panel.Placeholder() != "What do you want to know?" || h.panel.PendingLabel() != "Ask the Assistant…" {
		t.Errorf("placeholder %q, label %q", h.panel.Placeholder(), h.panel.PendingLabel())
	}
	h.panel.CancelPending()
	if h.panel.Pending().Kind != PendingNone {
		t.Error("CancelPending kept the action")
	}
	h.panel.AskAttachment("a", "m1", "2", "", "")
	if !h.panel.Submit("What is the total?") {
		t.Fatal("words refused")
	}
	h.turn()
	h.panel.SummarizeUnread("a", "in")
	h.turn()
	attachment, _ := assistant.AttachmentPrompt(tr, "a", "m1", "2")
	unread, _ := assistant.UnreadPrompt(tr, "a", "in")
	if want := []string{attachment + "What is the total?", unread}; !slices.Equal(fake.prompts(), want) {
		t.Errorf("prompts\n got %q\nwant %q", fake.prompts(), want)
	}
	var users []Content
	for _, c := range h.contents() {
		if c.Kind == ContentUser {
			users = append(users, c)
		}
	}
	contentsEqual(t, "questions", users, []Content{
		user("Ask the Assistant…", "What is the total?"), user("Summarize Unread in This Folder", ""),
	})
}

// The context follows the selection; a removed context comes back with the
// next selection; the chip's text.
func TestContextFollowsTheSelection(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	states := 0
	h.panel.OnState = func() { states++ }
	if h.panel.ContextLabel() != "All mail" {
		t.Errorf("chip %q", h.panel.ContextLabel())
	}
	h.panel.SetContext(&one)
	if h.panel.ContextLabel() != "Selected message" {
		t.Errorf("chip %q", h.panel.ContextLabel())
	}
	conv := folded([]string{"m3"}, 3, true, "", "")
	h.panel.SetContext(&conv)
	if h.panel.ContextLabel() != "Selected conversation (3 messages)" {
		t.Errorf("chip %q", h.panel.ContextLabel())
	}
	h.panel.RemoveContext()
	if h.panel.EffectiveContext() != nil || h.panel.ContextLabel() != "All mail" {
		t.Errorf("removed: %v %q", h.panel.EffectiveContext(), h.panel.ContextLabel())
	}
	h.panel.SetContext(ptr(conv))
	if !sameContext(h.panel.EffectiveContext(), &conv) {
		t.Errorf("effective %v", h.panel.EffectiveContext())
	}
	h.panel.SetContext(nil)
	if h.panel.ContextLabel() != "All mail" || states != 5 {
		t.Errorf("chip %q, %d state changes", h.panel.ContextLabel(), states)
	}
}

// A folded conversation's members are asked for when the question is
// sent, once; without an answer the newest message alone goes.
func TestPartialContextIsResolved(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	conv := folded([]string{"m3"}, 3, true, "", "")
	members := assistant.Selection{AccountID: "a", MessageIDs: []string{"m3", "m2", "m1"}}
	var asked []Context
	h.panel.ResolveContext = func(c Context, done func(assistant.Selection)) {
		asked = append(asked, c)
		done(members)
	}
	h.panel.SetContext(&conv)
	h.panel.Run(assistant.Tasks)
	h.turn()
	if len(asked) != 1 || !asked[0].equal(conv) {
		t.Errorf("asked %+v", asked)
	}
	if want := mustPrompt(t, assistant.Tasks, members); !slices.Equal(fake.prompts(), []string{want}) {
		t.Errorf("prompts %q", fake.prompts())
	}
	// The pinned conversation keeps its members.
	if p := h.panel.Pinned()[0].Context; !slices.Equal(p.Selection.MessageIDs, members.MessageIDs) || p.Partial {
		t.Errorf("pinned %+v", p)
	}
	h.panel.Run(assistant.Summarize)
	h.turn()
	if fake.lastPrompt() != mustPrompt(t, assistant.Summarize, members) || len(asked) != 1 {
		t.Errorf("prompt %q, asked %d times", fake.lastPrompt(), len(asked))
	}

	h.panel.NewConversation()
	h.panel.ResolveTimeout = 100 * time.Millisecond
	h.panel.ResolveContext = func(Context, func(assistant.Selection)) {} // never answers
	h.panel.Run(assistant.Summarize)
	h.turn()
	if fake.lastPrompt() != mustPrompt(t, assistant.Summarize, conv.Selection) {
		t.Errorf("prompt %q", fake.lastPrompt())
	}
}

// No Claude Code: an error with Try Again, nothing started.
func TestClaudeNotFound(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	if err := os.Remove(fake.path); err != nil {
		t.Fatal(err)
	}
	if !h.panel.Submit("Hello") {
		t.Fatal("question refused")
	}
	h.turn()
	contentsEqual(t, "transcript", h.contents(), []Content{
		user("", "Hello"), failure("Claude Code was not found on this computer", true),
	})
}

// Signed out: an error, and Try Again after signing in works.
func TestNotSignedIn(t *testing.T) {
	fake := newFakeClaude(t, "false", "", answerTurn("Hi there"))
	h := newHarness(t, fake, true, testBridge)
	if !h.panel.Submit("Hello") {
		t.Fatal("question refused")
	}
	h.turn()
	signedOut := "Claude Code is not signed in. Run claude in Terminal and sign in."
	contentsEqual(t, "transcript", h.contents(), []Content{user("", "Hello"), failure(signedOut, true)})
	if fake.starts() != 0 {
		t.Errorf("%d starts", fake.starts())
	}
	// Signed in meanwhile: Try Again sends the same question, without a
	// second bubble.
	script := strings.Replace(readFile(t, fake.path), `{"loggedIn": false}`, `{"loggedIn": true}`, 1)
	if err := os.WriteFile(fake.path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	h.panel.Retry(h.panel.Items()[1].ID)
	h.turn()
	contentsEqual(t, "after Try Again", h.contents(), []Content{
		user("", "Hello"), failure(signedOut, false), answer("Hi there", false),
	})
	if !slices.Equal(fake.prompts(), []string{"Hello"}) {
		t.Errorf("prompts %q", fake.prompts())
	}
}

// No bridge beside the application: the tools are missing.
func TestNoBridge(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, "")
	if !h.panel.Submit("Hello") {
		t.Fatal("question refused")
	}
	h.turn()
	if h.last() != failure("The Malachi Mail tools are not available to the assistant", false) || fake.starts() != 0 {
		t.Errorf("last %+v, starts %d", h.last(), fake.starts())
	}
}

// The bridge not connected in Claude Code: the conversation ends, the next
// question starts a new process.
func TestBridgeNotConnected(t *testing.T) {
	fake := newFakeClaude(t, "true", "",
		fakeTurn{lines: []string{fakeInitFailed, fakeText("I cannot"), fakeResult("done", true)}}, answerTurn("fine"))
	h := newHarness(t, fake, true, testBridge)
	if !h.panel.Submit("Hello") {
		t.Fatal("question refused")
	}
	h.turn()
	contentsEqual(t, "transcript", h.contents(), []Content{
		user("", "Hello"), failure("The Malachi Mail tools are not available to the assistant", false),
	})
	if h.panel.process != nil {
		t.Error("the process stays")
	}
	h.loop.runUntil(t, func() bool { return fake.starts() == 1 })
	if !h.panel.Submit("Again") {
		t.Fatal("question refused")
	}
	h.turn()
	if fake.starts() != 2 {
		t.Errorf("%d starts", fake.starts())
	}
}

// Stop during a turn: the note, the streamed text kept and closed, the
// process gone; the next question starts a new one.
func TestStopEndsTheTurn(t *testing.T) {
	fake := newFakeClaude(t, "true", "",
		fakeTurn{lines: []string{fakeInit, fakeToolUse("t1", "search_messages"), fakeDelta("Looking")}, shell: "sleep 5"},
		answerTurn("Fresh start"))
	h := newHarness(t, fake, true, testBridge)
	if !h.panel.Submit("Find the invoice") {
		t.Fatal("question refused")
	}
	h.loop.runUntil(t, func() bool { return h.last() == answer("Looking", true) })
	if h.panel.Phase() != PhaseRunning {
		t.Errorf("phase %v", h.panel.Phase())
	}
	h.panel.Stop()
	if h.panel.Phase() != PhaseIdle || h.panel.process != nil {
		t.Errorf("phase %v, process %v", h.panel.Phase(), h.panel.process)
	}
	contentsEqual(t, "transcript", h.contents(), []Content{
		user("", "Find the invoice"), activity("Searching mail…", true), answer("Looking", false),
		note("The conversation was stopped"),
	})
	if !h.panel.Submit("Once more") {
		t.Fatal("question refused")
	}
	h.turn()
	if fake.starts() != 2 || h.last() != answer("Fresh start", false) {
		t.Errorf("starts %d, last %+v", fake.starts(), h.last())
	}
}

// New Conversation: the transcript empties, the process ends, the next
// question starts a new one.
func TestNewConversation(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("One"), answerTurn("Two"))
	h := newHarness(t, fake, true, testBridge)
	if !h.panel.Submit("First") {
		t.Fatal("question refused")
	}
	h.turn()
	p := h.panel.process
	if p == nil || !h.panel.IsPinned() {
		t.Fatal("no process or not pinned after the first question")
	}
	h.panel.NewConversation()
	if len(h.panel.Items()) != 0 || h.panel.IsPinned() || h.changes[len(h.changes)-1] != (Change{Kind: ChangeReset}) || h.panel.process != nil {
		t.Errorf("items %d, pinned %v, last change %+v", len(h.panel.Items()), h.panel.IsPinned(), h.changes[len(h.changes)-1])
	}
	h.loop.runUntil(t, func() bool { return !p.Running() })
	if !h.panel.Submit("Second") {
		t.Fatal("question refused")
	}
	h.turn()
	if fake.starts() != 2 {
		t.Errorf("%d starts", fake.starts())
	}
	contentsEqual(t, "transcript", h.contents(), []Content{user("", "Second"), answer("Two", false)})
}

// The process ending during a turn: an error with its stderr, Try Again
// starts a new process.
func TestExitDuringATurn(t *testing.T) {
	fake := newFakeClaude(t, "true", `if [ "$n" = 1 ]; then read -r line; echo 'Error: boom' >&2; exit 1; fi`, answerTurn("Recovered"))
	h := newHarness(t, fake, true, testBridge)
	if !h.panel.Submit("Hello") {
		t.Fatal("question refused")
	}
	h.turn()
	contentsEqual(t, "transcript", h.contents(), []Content{user("", "Hello"), failure("The assistant stopped: Error: boom", true)})
	if h.panel.process != nil {
		t.Error("the process stays")
	}
	h.panel.Retry(h.panel.Items()[1].ID)
	h.turn()
	if fake.starts() != 2 || h.last() != answer("Recovered", false) {
		t.Errorf("starts %d, last %+v", fake.starts(), h.last())
	}
}

// A result that is not a success is said; the process stays for the next
// question.
func TestFailedResult(t *testing.T) {
	fake := newFakeClaude(t, "true", "",
		fakeTurn{lines: []string{fakeInit, fakeResult("API Error: 529 Overloaded", false)}}, answerTurn("Better"))
	h := newHarness(t, fake, true, testBridge)
	if !h.panel.Submit("Hello") {
		t.Fatal("question refused")
	}
	h.turn()
	if h.last() != failure("The assistant stopped: API Error: 529 Overloaded", true) || h.panel.process == nil || !h.panel.process.Running() {
		t.Errorf("last %+v", h.last())
	}
	if !h.panel.Submit("Hello again") {
		t.Fatal("question refused")
	}
	h.turn()
	if fake.starts() != 1 {
		t.Errorf("%d starts", fake.starts())
	}
	// The older error no longer offers Try Again.
	if h.contents()[1] != failure("The assistant stopped: API Error: 529 Overloaded", false) {
		t.Errorf("older error %+v", h.contents()[1])
	}
}

// The model setting reaches the command line of the next conversation.
func TestModelSetting(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	h.settings.model = assistant.Opus
	if h.panel.Subtitle() != "Claude Code · Opus" {
		t.Errorf("subtitle %q", h.panel.Subtitle())
	}
	if !h.panel.Submit("Hello") {
		t.Fatal("question refused")
	}
	h.turn()
	args := fake.args()
	i := slices.Index(args, "--model")
	if i < 0 || args[i+1] != "opus" {
		t.Errorf("args %q", args)
	}
}

// The conversation's first question pins the chip's context, whatever
// kind of question it is. Later selections change neither the chip nor
// what the conversation is about.
func TestFirstQuestionPinsTheContext(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	m1 := message("m1", "Invoice 42", "t1")
	m2 := message("m2", "Lunch", "t2")
	unread, _ := assistant.UnreadPrompt(tr, "a", "in")
	sends := []struct {
		name   string
		send   func()
		prompt string
	}{
		{"quick action", func() { h.panel.Run(assistant.Summarize) }, mustPrompt(t, assistant.Summarize, m1.Selection)},
		{"waiting action", func() {
			h.panel.Run(assistant.DraftReply)
			h.panel.Submit("Yes.")
		}, mustPrompt(t, assistant.DraftReply, m1.Selection) + "Yes."},
		{"free question", func() { h.panel.Submit("What now?") }, "Context: the user has selected message m1 in account a.\n\nWhat now?"},
		{"menu action", func() { h.panel.RunOn(assistant.Tasks, m1) }, mustPrompt(t, assistant.Tasks, m1.Selection)},
		{"unread", func() { h.panel.SummarizeUnread("a", "in") }, unread},
	}
	for _, s := range sends {
		h.panel.NewConversation()
		// The menu's action takes its own message, whatever the chip shows.
		if s.name == "menu action" {
			h.panel.SetContext(ptr(m2))
		} else {
			h.panel.SetContext(ptr(m1))
		}
		if h.panel.IsPinned() || h.panel.ContextLabel() != "Selected message" {
			t.Errorf("%s: pinned %v, chip %q before", s.name, h.panel.IsPinned(), h.panel.ContextLabel())
		}
		s.send()
		h.turn()
		if fake.lastPrompt() != s.prompt {
			t.Errorf("%s: prompt %q, want %q", s.name, fake.lastPrompt(), s.prompt)
		}
		if !samePinned(pinnedContexts(h.panel), &m1) || h.panel.ContextLabel() != "Conversation about: Invoice 42" || h.panel.AnotherSelected() {
			t.Errorf("%s: pinned %+v, chip %q", s.name, pinnedContexts(h.panel), h.panel.ContextLabel())
		}
		h.panel.SetContext(ptr(m2))
		if h.panel.ContextLabel() != "Conversation about: Invoice 42" || !h.panel.AnotherSelected() || !samePinned(pinnedContexts(h.panel), &m1) {
			t.Errorf("%s: after another selection chip %q, bar %v", s.name, h.panel.ContextLabel(), h.panel.AnotherSelected())
		}
		h.panel.RemoveContext() // no remove button while pinned
		if h.panel.contextRemoved {
			t.Errorf("%s: context removed while pinned", s.name)
		}
	}
}

// The chip once a conversation keeps its context.
func TestPinnedChipLabels(t *testing.T) {
	m1 := message("m1", "Invoice 42", "")
	cases := []struct {
		name     string
		contexts []*Context
		want     string
	}{
		{"one message", []*Context{&m1}, "Conversation about: Invoice 42"},
		{"no subject", []*Context{ptr(message("m1", "", ""))}, "Selected message"},
		{"a blank subject", []*Context{ptr(message("m1", " \n\t", ""))}, "Selected message"},
		{"a subject on two lines", []*Context{ptr(message("m1", "Invoice\r\n42", ""))}, "Conversation about: Invoice 42"},
		{"all mail", []*Context{nil}, "All mail"},
		{"a conversation", []*Context{ptr(folded([]string{"m3", "m2", "m1"}, 3, false, "Trip", ""))}, "Conversation about: Trip"},
		{"a conversation without a subject", []*Context{ptr(folded([]string{"m3", "m2", "m1"}, 3, false, "", ""))}, "Conversation about 3 messages"},
		{"a folded conversation", []*Context{ptr(folded([]string{"m3"}, 3, true, "Trip", ""))}, "Conversation about: Trip"},
		{"two messages", []*Context{&m1, ptr(message("m2", "Lunch", ""))}, "Conversation about 2 messages"},
		{"a conversation and a message", []*Context{ptr(folded([]string{"m3", "m2"}, 2, false, "", "")), ptr(message("m4", "", ""))}, "Conversation about 3 messages"},
		{"members not known yet", []*Context{ptr(folded([]string{"m3"}, 3, true, "", "")), ptr(message("m9", "", ""))}, "Conversation about 4 messages"},
		{"a message counted once", []*Context{ptr(folded([]string{"m2", "m1"}, 2, false, "", "")), &m1}, "Conversation about 2 messages"},
		{"all mail and a message", []*Context{nil, &m1}, "Conversation about: Invoice 42"},
		{"all mail and two messages", []*Context{nil, &m1, ptr(message("m2", "", ""))}, "Conversation about 2 messages"},
		{"the same id in two accounts", []*Context{&m1, ptr(NewContext(assistant.Selection{AccountID: "b", MessageIDs: []string{"m1"}}, 1, false, "", ""))}, "Conversation about 2 messages"},
	}
	for _, c := range cases {
		if got := pinnedLabel(tr, c.contexts); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The bar "Another message is selected": only while the conversation
// keeps its context and the selection is part of none of it.
func TestAnotherSelectedBar(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	m1 := message("m1", "Invoice", "t1")
	h.panel.SetContext(ptr(message("m2", "", "t2")))
	if h.panel.AnotherSelected() {
		t.Error("the bar before the first question")
	}
	h.panel.SetContext(&m1)
	if !h.panel.Submit("Who sent it?") {
		t.Fatal("question refused")
	}
	h.turn()
	states := 0
	h.panel.OnState = func() { states++ }
	cases := []struct {
		name string
		c    *Context
		want bool
	}{
		{"another message", ptr(message("m2", "", "t2")), true},
		{"the pinned message", &m1, false},
		{"no selection", nil, false},
		{"another message of the same thread", ptr(message("m6", "", "t1")), true},
		{"a folded conversation of the thread", ptr(folded([]string{"m5"}, 3, true, "", "t1")), false},
		{"a conversation with the message", ptr(folded([]string{"m7", "m1"}, 2, false, "", "")), false},
		{"another folded conversation", ptr(folded([]string{"m8"}, 2, true, "", "t3")), true},
		{"the same id in another account", ptr(NewContext(assistant.Selection{AccountID: "b", MessageIDs: []string{"m1"}}, 1, false, "", "")), true},
	}
	for _, c := range cases {
		h.panel.SetContext(c.c)
		if h.panel.AnotherSelected() != c.want || h.panel.ContextLabel() != "Conversation about: Invoice" {
			t.Errorf("%s: bar %v, chip %q", c.name, h.panel.AnotherSelected(), h.panel.ContextLabel())
		}
	}
	if states != len(cases) {
		t.Errorf("%d state changes, want %d", states, len(cases))
	}
	h.panel.NewConversation()
	if h.panel.AnotherSelected() || h.panel.ContextLabel() != "Selected message" {
		t.Errorf("after New Conversation: bar %v, chip %q", h.panel.AnotherSelected(), h.panel.ContextLabel())
	}
}

// Add to Conversation: the selection joins the conversation, the bar goes,
// and the next free question tells the model once.
func TestAddToConversation(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	m1 := message("m1", "Invoice", "t1")
	m2 := message("m2", "Lunch", "t2")
	h.panel.SetContext(&m1)
	h.panel.Run(assistant.Summarize)
	h.turn()
	h.panel.AddSelection() // nothing: the selection is what the conversation is about
	if len(h.panel.Pinned()) != 1 {
		t.Fatalf("%d pinned", len(h.panel.Pinned()))
	}
	h.panel.SetContext(&m2)
	if !h.panel.AnotherSelected() {
		t.Error("no bar for another message")
	}
	h.panel.AddSelection()
	if h.panel.AnotherSelected() || !samePinned(pinnedContexts(h.panel), &m1, &m2) ||
		!slices.Equal(announced(h.panel), []bool{true, false}) || h.panel.ContextLabel() != "Conversation about 2 messages" {
		t.Errorf("after Add: bar %v, announced %v, chip %q", h.panel.AnotherSelected(), announced(h.panel), h.panel.ContextLabel())
	}
	if !h.panel.Submit("Which is older?") {
		t.Fatal("question refused")
	}
	h.turn()
	if want := "Context: the user has also selected message m2 in account a; questions from now on may be about it too.\n\nWhich is older?"; fake.lastPrompt() != want {
		t.Errorf("prompt %q", fake.lastPrompt())
	}
	if !slices.Equal(announced(h.panel), []bool{true, true}) {
		t.Errorf("announced %v", announced(h.panel))
	}
	if !h.panel.Submit("And the total?") {
		t.Fatal("question refused")
	}
	h.turn()
	if fake.lastPrompt() != "And the total?" || fake.starts() != 1 {
		t.Errorf("prompt %q, starts %d", fake.lastPrompt(), fake.starts())
	}
	h.panel.SetContext(&m1)
	if h.panel.AnotherSelected() {
		t.Error("a bar for a pinned message")
	}
}

// An added folded conversation's members are asked for at once, and the
// model hears of all of them.
func TestAddedConversationIsResolved(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	var asked []Context
	h.panel.ResolveContext = func(c Context, done func(assistant.Selection)) {
		asked = append(asked, c)
		h.loop.Post(func() { done(assistant.Selection{AccountID: "a", MessageIDs: []string{"m9", "m8", "m7"}}) })
	}
	h.panel.SetContext(ptr(message("m1", "Invoice", "")))
	if !h.panel.Submit("Who sent it?") {
		t.Fatal("question refused")
	}
	h.turn()
	conv := folded([]string{"m9"}, 3, true, "Trip", "t9")
	h.panel.SetContext(&conv)
	if !h.panel.AnotherSelected() {
		t.Error("no bar")
	}
	h.panel.AddSelection()
	if h.panel.ContextLabel() != "Conversation about 4 messages" {
		t.Errorf("chip %q", h.panel.ContextLabel())
	}
	h.loop.runUntil(t, func() bool { return !h.panel.Pinned()[1].Context.Partial })
	if len(asked) != 1 || !asked[0].equal(conv) || h.panel.ContextLabel() != "Conversation about 4 messages" || h.panel.AnotherSelected() {
		t.Errorf("asked %+v, chip %q, bar %v", asked, h.panel.ContextLabel(), h.panel.AnotherSelected())
	}
	if !h.panel.Submit("When do we leave?") {
		t.Fatal("question refused")
	}
	h.turn()
	if want := "Context: the user has also selected a conversation with messages m9, m8, m7 (newest first) in account a; questions from now on may be about it too.\n\nWhen do we leave?"; fake.lastPrompt() != want {
		t.Errorf("prompt %q", fake.lastPrompt())
	}
	if len(asked) != 1 {
		t.Errorf("asked %d times", len(asked))
	}
}

// The bar's New Conversation: nothing is pinned any more, the chip follows
// the selection, and the next question starts over.
func TestNewConversationFromTheBar(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	m2 := message("m2", "Lunch", "")
	h.panel.SetContext(ptr(message("m1", "Invoice", "")))
	if !h.panel.Submit("First") {
		t.Fatal("question refused")
	}
	h.turn()
	h.panel.SetContext(&m2)
	if !h.panel.AnotherSelected() {
		t.Error("no bar")
	}
	h.panel.NewConversation()
	if h.panel.IsPinned() || len(h.panel.Items()) != 0 || h.panel.AnotherSelected() ||
		h.panel.ContextLabel() != "Selected message" || !h.panel.CanRunActions() {
		t.Errorf("after New Conversation: chip %q", h.panel.ContextLabel())
	}
	if !h.panel.Submit("Second") {
		t.Fatal("question refused")
	}
	h.turn()
	if fake.starts() != 2 || fake.lastPrompt() != "Context: the user has selected message m2 in account a.\n\nSecond" ||
		!samePinned(pinnedContexts(h.panel), &m2) || h.panel.ContextLabel() != "Conversation about: Lunch" {
		t.Errorf("starts %d, prompt %q, chip %q", fake.starts(), fake.lastPrompt(), h.panel.ContextLabel())
	}
}

// A menu's action on a message that is part of no pinned context adds it
// and names it in its own prompt; on a pinned message it only runs. An
// attachment's question does the same for its message.
func TestMenuActionOnAnotherMessageAddsIt(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	m1 := message("m1", "Invoice", "t1")
	m2 := message("m2", "Lunch", "t2")
	m4 := message("m4", "Visit", "t4")
	h.panel.SetContext(&m1)
	if !h.panel.Submit("Who sent it?") {
		t.Fatal("question refused")
	}
	h.turn()
	if fake.lastPrompt() != "Context: the user has selected message m1 in account a.\n\nWho sent it?" {
		t.Errorf("prompt %q", fake.lastPrompt())
	}

	h.panel.SetContext(&m2)
	h.panel.RunOn(assistant.Summarize, m2)
	h.turn()
	if fake.lastPrompt() != mustPrompt(t, assistant.Summarize, m2.Selection) || !samePinned(pinnedContexts(h.panel), &m1, &m2) ||
		!slices.Equal(announced(h.panel), []bool{true, true}) || h.panel.AnotherSelected() {
		t.Errorf("prompt %q, announced %v", fake.lastPrompt(), announced(h.panel))
	}
	if !h.panel.Submit("Anything urgent?") {
		t.Fatal("question refused")
	}
	h.turn()
	if fake.lastPrompt() != "Anything urgent?" {
		t.Errorf("prompt %q", fake.lastPrompt())
	}

	h.panel.RunOn(assistant.Tasks, m1)
	h.turn()
	if fake.lastPrompt() != mustPrompt(t, assistant.Tasks, m1.Selection) || len(h.panel.Pinned()) != 2 {
		t.Errorf("prompt %q, %d pinned", fake.lastPrompt(), len(h.panel.Pinned()))
	}

	h.panel.AskAttachment("a", "m3", "2", "Scan", "t3")
	if len(h.panel.Pinned()) != 3 || h.panel.ContextLabel() != "Conversation about 3 messages" {
		t.Errorf("%d pinned, chip %q", len(h.panel.Pinned()), h.panel.ContextLabel())
	}
	if !h.panel.Submit("What is it?") {
		t.Fatal("question refused")
	}
	h.turn()
	attachment, _ := assistant.AttachmentPrompt(tr, "a", "m3", "2")
	if fake.lastPrompt() != attachment+"What is it?" {
		t.Errorf("prompt %q", fake.lastPrompt())
	}
	if !h.panel.Submit("And the date?") {
		t.Fatal("question refused")
	}
	h.turn()
	if fake.lastPrompt() != "And the date?" {
		t.Errorf("prompt %q", fake.lastPrompt())
	}

	// A waiting action stays on its message when the selection moves on.
	h.panel.SetContext(&m4)
	h.panel.RunOn(assistant.DraftReply, m4)
	if h.panel.Pending() != (Pending{Kind: PendingAction, Action: assistant.DraftReply}) || len(h.panel.Pinned()) != 4 || h.panel.AnotherSelected() {
		t.Errorf("pending %+v, %d pinned", h.panel.Pending(), len(h.panel.Pinned()))
	}
	h.panel.SetContext(&m1)
	h.panel.SetContext(nil)
	if h.panel.Pending().Kind != PendingAction {
		t.Error("the waiting action went with the selection")
	}
	if !h.panel.Submit("Fine") {
		t.Fatal("words refused")
	}
	h.turn()
	if fake.lastPrompt() != mustPrompt(t, assistant.DraftReply, m4.Selection)+"Fine" || fake.starts() != 1 {
		t.Errorf("prompt %q, starts %d", fake.lastPrompt(), fake.starts())
	}
}

// The quick actions act on the newest pinned context, not on the
// selection; with only all mail pinned there is nothing to act on.
func TestQuickActionsUseTheNewestPinnedContext(t *testing.T) {
	fake := newFakeClaude(t, "true", "", answerTurn("ok"))
	h := newHarness(t, fake, true, testBridge)
	m1 := message("m1", "Invoice", "t1")
	m2 := message("m2", "Lunch", "t2")
	h.panel.SetContext(&m1)
	if !h.panel.Submit("Who sent it?") {
		t.Fatal("question refused")
	}
	h.turn()
	h.panel.SetContext(&m2)
	h.panel.AddSelection()
	h.panel.SetContext(ptr(message("m3", "", "t3")))
	if !h.panel.AnotherSelected() || !h.panel.CanRunActions() {
		t.Error("no bar or no actions")
	}
	h.panel.Run(assistant.Tasks)
	h.turn()
	if fake.lastPrompt() != mustPrompt(t, assistant.Tasks, m2.Selection) {
		t.Errorf("prompt %q", fake.lastPrompt())
	}
	h.panel.Run(assistant.DraftReply)
	h.panel.SetContext(nil)
	if h.panel.Pending().Kind != PendingAction {
		t.Error("the waiting action went with the selection")
	}
	if !h.panel.Submit("ok") {
		t.Fatal("words refused")
	}
	h.turn()
	if fake.lastPrompt() != mustPrompt(t, assistant.DraftReply, m2.Selection)+"ok" {
		t.Errorf("prompt %q", fake.lastPrompt())
	}

	h.panel.NewConversation()
	if !h.panel.Submit("How many unread?") {
		t.Fatal("question refused")
	}
	h.turn()
	if fake.lastPrompt() != "How many unread?" || !samePinned(pinnedContexts(h.panel), nil) ||
		h.panel.ContextLabel() != "All mail" || h.panel.CanRunActions() {
		t.Errorf("prompt %q, chip %q, actions %v", fake.lastPrompt(), h.panel.ContextLabel(), h.panel.CanRunActions())
	}
	h.panel.SetContext(&m1)
	if !h.panel.AnotherSelected() {
		t.Error("no bar")
	}
	h.panel.AddSelection()
	if !h.panel.CanRunActions() || h.panel.ContextLabel() != "Conversation about: Invoice" {
		t.Errorf("actions %v, chip %q", h.panel.CanRunActions(), h.panel.ContextLabel())
	}
	if !h.panel.Submit("From whom?") {
		t.Fatal("question refused")
	}
	h.turn()
	if want := "Context: the user has also selected message m1 in account a; questions from now on may be about it too.\n\nFrom whom?"; fake.lastPrompt() != want {
		t.Errorf("prompt %q", fake.lastPrompt())
	}
}

// After Stop the next question starts a new Claude Code, which knows
// nothing of the conversation: its contexts are told again.
func TestANewProcessIsToldTheContextAgain(t *testing.T) {
	fake := newFakeClaude(t, "true", "",
		answerTurn("Summary"),
		fakeTurn{lines: []string{fakeInit, fakeDelta("Thinking")}, shell: "sleep 5"},
		answerTurn("Again"))
	h := newHarness(t, fake, true, testBridge)
	h.panel.SetContext(ptr(message("m1", "Invoice", "")))
	h.panel.Run(assistant.Summarize)
	h.turn()
	h.panel.SetContext(ptr(message("m2", "Lunch", "")))
	h.panel.AddSelection()
	if !h.panel.Submit("Which is older?") {
		t.Fatal("question refused")
	}
	h.loop.runUntil(t, func() bool { return h.last() == answer("Thinking", true) })
	if want := "Context: the user has also selected message m2 in account a; questions from now on may be about it too.\n\nWhich is older?"; fake.lastPrompt() != want {
		t.Errorf("prompt %q", fake.lastPrompt())
	}
	h.panel.Stop()
	if len(h.panel.Pinned()) != 2 || h.panel.ContextLabel() != "Conversation about 2 messages" {
		t.Errorf("%d pinned, chip %q", len(h.panel.Pinned()), h.panel.ContextLabel())
	}
	if !h.panel.Submit("Once more") {
		t.Fatal("question refused")
	}
	h.turn()
	want := "Context: the user has selected message m1 in account a.\n" +
		"Context: the user has also selected message m2 in account a; questions from now on may be about it too.\n\nOnce more"
	if fake.starts() != 2 || fake.lastPrompt() != want {
		t.Errorf("starts %d, prompt %q", fake.starts(), fake.lastPrompt())
	}
}
