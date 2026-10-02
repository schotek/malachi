// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package assistantpanel is the assistant panel of the main window (the In
// App target of ui/internal/assistant) without its widgets: the
// conversation with the user's own Claude Code, restricted to the
// malachi-mcp tools, as a list of items the panel shows (Controller), the
// Claude Code process (Process) and where claude is (Locator). It knows no
// GTK: the window runs it on the main loop through a Loop and renders it.
// The macOS client leads (MalachiCore AssistantPanelController,
// ClaudeCodeProcess, ClaudeCodeLocator); this is its port.
package assistantpanel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// ContentKind is what an Item shows.
type ContentKind int

// The kinds of items.
const (
	// ContentUser is the user's question: the action's label (may be
	// empty) and the typed text (may be empty).
	ContentUser ContentKind = iota
	// ContentAssistant is the answer as Markdown source (assistant.Markdown),
	// still arriving while Streaming.
	ContentAssistant
	// ContentActivity is a tool at work (assistant.ActivityLabel), Done at
	// its result.
	ContentActivity
	// ContentDraft is a draft the bridge saved, with Open Draft.
	ContentDraft
	// ContentError is what went wrong; Retry offers Try Again, Offer one
	// more button.
	ContentError
	// ContentNote is a remark of the panel's own ("The conversation was
	// stopped").
	ContentNote
)

// Offer is the button of an error item beyond Try Again.
type Offer int

// The offers.
const (
	OfferNone Offer = iota
	// OfferSignIn: "Sign In…" beside "Claude Code is not signed in"
	// (Controller.SignIn).
	OfferSignIn
	// OfferInstall: "Get Claude Code…" beside "Claude Code was not found on
	// this computer"; the view opens assistant.InstallURL in the browser.
	OfferInstall
)

// Content is what one item of the transcript shows; only the fields of its
// Kind are set.
type Content struct {
	Kind ContentKind
	// Label is the user item's action label and the activity's line.
	Label string
	// Text is the user item's words, the answer's Markdown, and the error's
	// or note's line.
	Text                   string
	Streaming, Done, Retry bool
	Draft                  assistant.DraftRef
	// Offer is the error's other button.
	Offer Offer
}

// Item is one entry of the transcript; ID stays with it.
type Item struct {
	ID      int
	Content Content
}

// ChangeKind is what changed in the items.
type ChangeKind int

// The kinds of changes.
const (
	ChangeReset ChangeKind = iota
	ChangeAppended
	ChangeUpdated
)

// Change is what changed in Items, for the view: everything, or the item at
// Index.
type Change struct {
	Kind  ChangeKind
	Index int
}

// Phase is where a question is.
type Phase int

// The phases.
const (
	PhaseIdle Phase = iota
	// PhasePreparing: consent, the context, locating and checking Claude
	// Code, the process starting.
	PhasePreparing
	// PhaseRunning: a turn is under way.
	PhaseRunning
)

// PendingKind is what waits for the user's words.
type PendingKind int

// The kinds of waiting actions.
const (
	PendingNone PendingKind = iota
	// PendingAction: Draft a Reply… or Ask About This Message… on the
	// context.
	PendingAction
	// PendingAttachment: a question about an attachment.
	PendingAttachment
)

// Pending is a message action that waits for the user's words.
type Pending struct {
	Kind                         PendingKind
	Action                       assistant.Action
	AccountID, MessageID, PartID string
}

// Context is what the panel works on: the selected message, or a
// conversation of Count messages (Selection newest first; with Partial
// only its newest message is known yet).
type Context struct {
	Selection assistant.Selection
	Count     int
	Partial   bool
	// Subject is the message's subject, or the conversation's: what the
	// chip names once a conversation is about it. Mail text, shown as one
	// line (assistant.ConversationLabel), never sent to the model.
	Subject string
	// ThreadID is the conversation (thread) it belongs to; "" when not
	// known.
	ThreadID string
}

// NewContext is a context whose count is at least its ids.
func NewContext(sel assistant.Selection, count int, partial bool, subject, threadID string) Context {
	return Context{Selection: sel, Count: max(count, len(sel.MessageIDs)), Partial: partial, Subject: subject, ThreadID: threadID}
}

// Conversation says whether the context is a conversation.
func (c Context) Conversation() bool { return c.Count > 1 }

// Overlaps says whether o is part of c, or c of o: the same account and a
// message in common, or, when either is a conversation, the same thread (a
// folded conversation knows only its newest message until its members are
// resolved).
func (c Context) Overlaps(o Context) bool {
	if c.Selection.AccountID == "" || c.Selection.AccountID != o.Selection.AccountID {
		return false
	}
	for _, id := range c.Selection.MessageIDs {
		if id != "" && slices.Contains(o.Selection.MessageIDs, id) {
			return true
		}
	}
	return (c.Conversation() || o.Conversation()) && c.ThreadID != "" && c.ThreadID == o.ThreadID
}

// equal says whether two contexts say the same.
func (c Context) equal(o Context) bool {
	return c.Selection.AccountID == o.Selection.AccountID && slices.Equal(c.Selection.MessageIDs, o.Selection.MessageIDs) &&
		c.Count == o.Count && c.Partial == o.Partial && c.Subject == o.Subject && c.ThreadID == o.ThreadID
}

func sameContext(a, b *Context) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.equal(*b)
}

// Pinned is one context of a conversation that keeps its context: what the
// chip showed at its first question, or a selection added since.
type Pinned struct {
	// Context is nil for all mail (nothing was selected, or the chip's
	// context was removed, when the conversation began).
	Context *Context
	// Announced: the model was told about it since its Claude Code started.
	Announced bool
	// key is its identity while its members are resolved; never reused.
	key int
}

// target is what a message action or an attachment's question is about: a
// pinned context (by key), or one the question pins (or adds) when sent.
type target struct {
	pinned bool
	key    int
	ctx    Context
}

type requestKind int

const (
	requestFree requestKind = iota
	requestAction
	requestUnread
	requestAttachment
)

// request is one question, as sent and as retried.
type request struct {
	kind                                   requestKind
	action                                 assistant.Action
	accountID, folderID, messageID, partID string
	label, text                            string
	// inEffect is what the chip showed when it was asked: what the
	// conversation is about when this question is its first.
	inEffect *Context
	// target is what a message action or an attachment's question is
	// about.
	target *target
	// signIn: Sign In… sent it; Claude Code signs in before it starts.
	signIn bool
}

// DefaultResolveTimeout is how long the members of a folded conversation
// are waited for.
const DefaultResolveTimeout = 10 * time.Second

// errNoMessages is a message action without the ids of its context.
var errNoMessages = errors.New("assistant: no message ids")

// Controller is the panel's conversation (macOS: AssistantPanelController).
// A question goes through these steps, each of which may end it:
//
//  1. Idle → preparing. The first question ever asks for consent (Consent,
//     "Send Mail to Claude?"; the answer is kept in assistant-consent).
//     Declined: nothing happens, and the typed text goes back into the
//     field (OnRestoreInput).
//  2. The question appears in the transcript (a user item: the action's
//     label and the typed text) and a waiting message action is used up.
//     The first question of a conversation pins its context (see below).
//  3. The ids of the contexts the prompt names are completed: a folded
//     conversation stands for its newest message until its members are
//     known (ResolveContext; after ResolveTimeout the newest message alone
//     is used).
//  4. Without a running process: Claude Code is located (none: "Claude Code
//     was not found on this computer"), the bridge must be there (none: the
//     tools are not available), and Claude Code must not say it is signed
//     out (Locator.SignedIn, asked afresh; not known counts as signed in,
//     and the process then says what is wrong): "Claude Code is not signed
//     in" with Sign In…, see below. Then the process starts (assistant.Args,
//     assistant.ChildEnv, the private directory) and is kept for the
//     follow-up questions of the conversation.
//  5. Running: the turn is written to stdin. Its system/init must report
//     the bridge connected, or the conversation ends with "The Malachi Mail
//     tools are not available to the assistant". Text deltas stream into
//     the current answer, a whole text block replaces it; a tool call is an
//     activity line until its result; a create_draft result whose bridge
//     line names a draft (assistant.ParseDraftResult) adds a draft card,
//     whose Open Draft the application checks with draft.list (OpenDraft).
//     The result ends the turn (idle); one that is not a success adds "The
//     assistant stopped: …". The process ending during a turn does the same
//     with its stderr's first line. A message Claude Code wrote itself
//     because the API refused the turn (assistant.EventFailure) is no
//     answer: the result repeats it. When the API refused the sign-in
//     (expired or revoked, whatever claude auth status says), the turn ends
//     with "Claude Code is not signed in" and Sign In…, and the process
//     ends, for a new sign-in takes a new one. When Claude Code could not
//     refresh its sign-in (assistant.Event.RefreshFailed: another Claude
//     Code was refreshing it, or ended in the middle of that), the turn
//     ends with the result's "The assistant stopped: …" and both Try Again
//     (Claude Code takes the refresh over after about a minute) and Sign
//     In…, and the process ends the same way.
//
// Sign In… (SignIn) sends the same question once more, with Claude Code's
// own sign-in in front of step 4's start: the line "Waiting for the sign-in
// in your browser…" is an activity while claude auth login runs
// (Locator.SignIn: the browser opens, the application sees no credential),
// then the process starts and the question is asked. A sign-in that fails,
// takes too long or is taken over by the settings' ends the turn with its
// reason and Sign In… again; Stop ends it like any turn. "Claude Code was
// not found on this computer" offers Get Claude Code… (OfferInstall), which
// the view opens in the browser, beside Try Again.
//
// Stop ends the process and the turn with the note "The conversation was
// stopped"; the next question starts a new process (a new conversation
// for Claude). NewConversation ends the process and empties the
// transcript. Errors of steps 4 and 5 offer Try Again, which sends the same
// question once more. Nothing is kept on disk: Claude Code runs with
// --no-session-persistence in an empty directory, and the transcript
// lives in memory. Costs are logged as numbers, never mail or model text.
//
// A conversation keeps its context. Before its first question the chip
// follows the list's selection (SetContext) unless its remove button
// (RemoveContext) leaves it at all mail until the next selection change.
// The first question pins what the chip showed then: Pinned starts with it,
// the chip names it (assistant.ConversationLabel), and from then on the
// selection only decides whether the bar "Another message is selected"
// shows (AnotherSelected). Its Add to Conversation (AddSelection) appends
// the selection; its New Conversation ends the conversation, and the chip
// follows the selection again. The quick actions act on the newest pinned
// context; a menu's action (RunOn) or an attachment's question on a
// message that is part of no pinned context adds it first.
//
// The model hears of each pinned context once per Claude Code process: a
// free question carries a model-facing line in front of it for every
// context not yet told (assistant.ContextPreamble for the first,
// assistant.AddedContextPreamble for one added later), and an action's own
// prompt, which names its ids, tells the model of its context. A new
// process knows nothing, so the contexts are told again.
//
// Main loop only: every method, hook and callback runs on it.
type Controller struct {
	// Consent asks the user before the first question ever and calls done
	// with the answer; without it nothing is ever sent.
	Consent func(done func(allowed bool))
	// ResolveContext completes a partial context (a folded conversation's
	// members, newest first); done should be called once, and the
	// context's own selection is used when it is not called within
	// ResolveTimeout.
	ResolveContext func(c Context, done func(assistant.Selection))
	// OpenDraft is a draft card's Open Draft.
	OpenDraft func(assistant.DraftRef)
	// OnChange: Items changed.
	OnChange func(Change)
	// OnState: the phase, the context, the waiting action or the like
	// changed.
	OnState func()
	// OnFocusInput: the question field should take the keyboard (a message
	// action waits for the user's words).
	OnFocusInput func()
	// OnRestoreInput: a question that was not sent (consent declined); its
	// text goes back into the field.
	OnRestoreInput func(text string)
	// Today is the date for the system prompt, YYYY-MM-DD.
	Today func() string
	// Language is the UI language's English name for the system prompt.
	Language func() string
	// ResolveTimeout is how long ResolveContext is waited for.
	ResolveTimeout time.Duration

	tr        assistant.Translator
	settings  Settings
	locator   *Locator
	loop      Loop
	log       *slog.Logger
	bridge    string
	socket    string
	directory string
	env       []string
	killGrace time.Duration

	items   []Item
	phase   Phase
	context *Context
	// contextRemoved: the chip's remove button, all mail until the
	// selection changes.
	contextRemoved bool
	pinned         []Pinned
	pending        Pending
	// pendingTarget is what the waiting action acts on; nil for the chip's
	// context when its words are sent (before the first question).
	pendingTarget    *target
	process          *Process
	providerFactory  func() Provider
	selectedProvider Provider
	providerSession  Session
	cancelProvider   context.CancelFunc
	closed           bool

	// gen is bumped by every question, Stop, NewConversation and Close:
	// the steps of an older question stop at their next callback.
	gen    int
	nextID int
	// streaming is the index of the answer deltas stream into, -1 none.
	streaming int
	// activities are the activity items by tool call id, toolNames each
	// call's tool.
	activities map[string]int
	toolNames  map[string]string
	// lastRequest is the question of the turn under way (or the last that
	// failed), for Try Again.
	lastRequest *request
	// authFailed: the API refused the sign-in in the turn under way;
	// refreshFailed: Claude Code could not refresh it.
	authFailed, refreshFailed bool
	// signingIn is the index of the sign-in's activity line, -1 none;
	// cancelSignIn ends the sign-in this question started.
	signingIn    int
	cancelSignIn func()
	nextKey      int
	// resolving are the pinned folded conversations whose members are
	// being asked for, and who waits for them.
	resolving map[int]*future[struct{}]
}

// Config is what a Controller needs from the application.
type Config struct {
	// Provider selects the in-app runtime; nil retains Claude.
	Provider   func() Provider
	Translator assistant.Translator
	Settings   Settings
	Locator    *Locator
	Loop       Loop
	Log        *slog.Logger
	// Bridge is malachi-mcp beside the application, "" without one;
	// Socket the daemon's socket, for the bridge.
	Bridge, Socket string
	// Directory is Claude Code's working directory (empty, private).
	Directory string
	// Env is the application's environment, filtered by
	// assistant.ChildEnv.
	Env []string
	// KillGrace is the time from SIGTERM to SIGKILL; 0 is
	// DefaultKillGrace.
	KillGrace time.Duration
}

// New is a controller with nothing asked yet.
func New(cfg Config) *Controller {
	grace := cfg.KillGrace
	if grace == 0 {
		grace = DefaultKillGrace
	}
	return &Controller{
		Today:           func() string { return time.Now().Format("2006-01-02") },
		Language:        func() string { return "" },
		ResolveTimeout:  DefaultResolveTimeout,
		tr:              cfg.Translator,
		providerFactory: cfg.Provider,
		settings:        cfg.Settings,
		locator:         cfg.Locator,
		loop:            cfg.Loop,
		log:             cfg.Log,
		bridge:          cfg.Bridge,
		socket:          cfg.Socket,
		directory:       cfg.Directory,
		env:             cfg.Env,
		killGrace:       grace,
		streaming:       -1,
		signingIn:       -1,
		activities:      make(map[string]int),
		toolNames:       make(map[string]string),
		resolving:       make(map[int]*future[struct{}]),
	}
}

// Close ends the conversation for good (the application quits).
func (c *Controller) Close() {
	if c.closed {
		return
	}
	c.closed = true
	c.gen++
	c.endSignIn()
	c.endProcess()
}

// Reading

// Items are the transcript.
func (c *Controller) Items() []Item { return c.items }

// Phase is where the question under way is.
func (c *Controller) Phase() Phase { return c.phase }

// Running says whether a question is under way.
func (c *Controller) Running() bool { return c.phase != PhaseIdle }

// Waiting says whether a question is under way and nothing in the
// transcript shows it: no answer streams, no tool and no sign-in is at
// work. The view shows that it waits (a spinner at the end of the
// transcript): from the question until Claude Code started and answers,
// and between a tool's result and what comes next. It is read from the
// items, so it holds at every Change and OnState.
func (c *Controller) Waiting() bool {
	if c.phase == PhaseIdle {
		return false
	}
	for _, it := range c.items {
		if it.Content.Streaming || it.Content.Kind == ContentActivity && !it.Content.Done {
			return false
		}
	}
	return true
}

// Closed says whether nothing runs any more.
func (c *Controller) Closed() bool { return c.closed }

// Pending is the message action that waits for the user's words.
func (c *Controller) Pending() Pending { return c.pending }

// Pinned is what the conversation is about, in the order it was pinned;
// empty until its first question.
func (c *Controller) Pinned() []Pinned { return c.pinned }

// IsPinned says whether the conversation keeps its context: its first
// question was asked.
func (c *Controller) IsPinned() bool { return len(c.pinned) > 0 }

// Context is the list's selection as the application last set it.
func (c *Controller) Context() *Context { return c.context }

// EffectiveContext is the chip's context before the conversation's first
// question: the list's, unless removed.
func (c *Controller) EffectiveContext() *Context {
	if c.contextRemoved {
		return nil
	}
	return c.context
}

// ContextLabel is the chip's text: the selection it follows
// (assistant.ContextLabel) until the conversation's first question, then
// what the conversation is about.
func (c *Controller) ContextLabel() string {
	if c.IsPinned() {
		contexts := make([]*Context, len(c.pinned))
		for i, p := range c.pinned {
			contexts[i] = p.Context
		}
		return pinnedLabel(c.tr, contexts)
	}
	n := 0
	if e := c.EffectiveContext(); e != nil {
		n = e.Count
	}
	return assistant.ContextLabel(c.tr, n)
}

// AnotherSelected says whether the bar "Another message is selected"
// shows: the conversation keeps its context and the list's selection is
// part of none of it.
func (c *Controller) AnotherSelected() bool {
	if !c.IsPinned() || c.closed || c.context == nil {
		return false
	}
	for _, p := range c.pinned {
		if p.Context != nil && p.Context.Overlaps(*c.context) {
			return false
		}
	}
	return true
}

// CanRunActions says whether the quick actions (Summarize, Draft a Reply…,
// Tasks and Deadlines) can run: nothing under way, and something to act
// on.
func (c *Controller) CanRunActions() bool {
	return c.phase == PhaseIdle && !c.closed && c.quickTarget() != nil
}

// CanSummarizeUnread says whether the quick action Summarize Unread in This
// Folder can run: folder says whether the window has a folder it can be
// summarised for (the Assistant menu item's condition: one is selected,
// not an Outbox, no search replaces it), and nothing is under way. It
// needs no selected message.
func (c *Controller) CanSummarizeUnread(folder bool) bool {
	return folder && c.phase == PhaseIdle && !c.closed
}

// quickTarget is what the quick actions act on: the chip's context before
// the conversation's first question, the newest pinned context after it
// (none when that is all mail).
func (c *Controller) quickTarget() *target {
	if !c.IsPinned() {
		if e := c.EffectiveContext(); e != nil {
			return &target{ctx: *e}
		}
		return nil
	}
	p := c.pinned[len(c.pinned)-1]
	if p.Context == nil || len(p.Context.Selection.MessageIDs) == 0 {
		return nil
	}
	return &target{pinned: true, key: p.key}
}

// pinnedLabel is the chip's text for what a conversation is about (the
// contexts in pinned order, nil for all mail): one context its subject
// (without one, the message or the conversation's count), all mail "All
// mail", several the number of their messages, each counted once.
func pinnedLabel(tr assistant.Translator, contexts []*Context) string {
	var mail []Context
	for _, c := range contexts {
		if c != nil && len(c.Selection.MessageIDs) > 0 {
			mail = append(mail, *c)
		}
	}
	if len(contexts) == 1 {
		if len(mail) == 0 {
			return assistant.ContextLabel(tr, 0)
		}
		return subjectLabel(tr, mail[0])
	}
	switch n := messageCount(mail); n {
	case 0:
		return assistant.ContextLabel(tr, 0)
	case 1:
		return subjectLabel(tr, mail[0])
	default:
		return assistant.ConversationLabel(tr, "", n)
	}
}

// subjectLabel is one context's chip: its subject; without one, the
// message or the conversation's count.
func subjectLabel(tr assistant.Translator, c Context) string {
	n := c.Count
	if assistant.SubjectLine(c.Subject) != "" {
		n = 1
	}
	return assistant.ConversationLabel(tr, c.Subject, n)
}

// messageCount is the messages of contexts, each counted once; a folded
// conversation's members that are not known yet count as its count says.
func messageCount(contexts []Context) int {
	seen := map[[2]string]bool{}
	unknown := 0
	for _, c := range contexts {
		known := 0
		for _, id := range c.Selection.MessageIDs {
			if id != "" {
				seen[[2]string{c.Selection.AccountID, id}] = true
				known++
			}
		}
		if c.Partial {
			unknown += max(0, c.Count-known)
		}
	}
	return len(seen) + unknown
}

// Placeholder is the question field's placeholder for the waiting action.
func (c *Controller) Placeholder() string {
	t := assistant.PanelTexts(c.tr)
	switch {
	case c.pending.Kind == PendingAction && c.pending.Action == assistant.DraftReply:
		return t.ReplyPlaceholder
	case c.pending.Kind != PendingNone:
		return t.AskPlaceholder
	}
	return t.Placeholder
}

// PendingLabel is the label over the question field while an action
// waits.
func (c *Controller) PendingLabel() string {
	switch c.pending.Kind {
	case PendingAction:
		return assistant.Label(c.tr, c.pending.Action)
	case PendingAttachment:
		return assistant.Texts(c.tr).AskFile
	}
	return ""
}

// Subtitle is the panel's subtitle: "Claude Code · Sonnet".
func (c *Controller) Subtitle() string {
	if c.providerFactory != nil {
		if provider := c.providerFactory(); provider != nil {
			labels := assistant.ChatGPTText(c.tr)
			model := provider.Model()
			if model == "" {
				model = labels.DefaultModel
			}
			return labels.Name + " · " + model
		}
	}
	return assistant.TargetName(c.tr, assistant.Code) + " · " + assistant.ModelName(c.tr, c.settings.AssistantModel())
}

// The context

// SetContext is the list's selection: before the conversation's first
// question the chip follows it (and a removed context comes back); after
// it, the bar "Another message is selected" may come or go.
func (c *Controller) SetContext(ctx *Context) {
	if sameContext(ctx, c.context) && !c.contextRemoved {
		return
	}
	c.context = ctx
	c.contextRemoved = false
	if !c.IsPinned() && ctx == nil && c.pending.Kind == PendingAction {
		c.pending, c.pendingTarget = Pending{}, nil
	}
	c.state()
}

// RemoveContext is the chip's remove button: all mail until the next
// selection. Only before the conversation's first question.
func (c *Controller) RemoveContext() {
	if c.IsPinned() || c.contextRemoved || c.context == nil {
		return
	}
	c.contextRemoved = true
	if c.pending.Kind == PendingAction {
		c.pending, c.pendingTarget = Pending{}, nil
	}
	c.state()
}

// AddSelection is the bar's Add to Conversation: the list's selection
// joins what the conversation is about; the next free question tells the
// model (assistant.AddedContextPreamble).
func (c *Controller) AddSelection() {
	if c.closed || !c.AnotherSelected() {
		return
	}
	c.add(*c.context)
	c.state()
}

// add appends ctx to what the conversation is about; a folded
// conversation's members are asked for at once, while the list still shows
// it selected.
func (c *Controller) add(ctx Context) int {
	key := c.newKey()
	cp := ctx
	c.pinned = append(c.pinned, Pinned{Context: &cp, key: key})
	if ctx.Partial {
		c.resolvePinned(key)
	}
	return key
}

// pinnedKey is the newest pinned context ctx is part of.
func (c *Controller) pinnedKey(ctx Context) (int, bool) {
	for i := len(c.pinned) - 1; i >= 0; i-- {
		if p := c.pinned[i]; p.Context != nil && p.Context.Overlaps(ctx) {
			return p.key, true
		}
	}
	return 0, false
}

func (c *Controller) pinnedContext(key int) *Context {
	for _, p := range c.pinned {
		if p.key == key {
			return p.Context
		}
	}
	return nil
}

func (c *Controller) newKey() int {
	k := c.nextKey
	c.nextKey++
	return k
}

// pinOrAdd is the key of the pinned context ctx is part of, ctx added
// first when it is part of none.
func (c *Controller) pinOrAdd(ctx Context) int {
	if key, ok := c.pinnedKey(ctx); ok {
		return key
	}
	return c.add(ctx)
}

// Questions

// Run is a quick action (the panel's buttons) on the chip's context, or,
// once the conversation keeps its context, on its newest pinned context:
// Summarize and Tasks and Deadlines send at once, Draft a Reply… and Ask
// About This Message… wait for the user's words. Nothing while a question
// is under way or without anything to act on.
func (c *Controller) Run(a assistant.Action) {
	if c.closed || c.phase != PhaseIdle || !slices.Contains(assistant.MessageActions, a) {
		return
	}
	t := c.quickTarget()
	if t == nil {
		return
	}
	c.perform(a, *t)
}

// RunOn is a message action of a menu (the Assistant menu, a message
// window) on ctx, the selection or the window's message. Before the
// conversation's first question the chip takes ctx and the action runs on
// it; after it, the action runs on the pinned context ctx is part of, and a
// ctx that is part of none is added first (the action's prompt names its
// ids, so the model needs no other word of it).
func (c *Controller) RunOn(a assistant.Action, ctx Context) {
	if c.closed || c.phase != PhaseIdle || !slices.Contains(assistant.MessageActions, a) {
		return
	}
	if !c.IsPinned() {
		c.SetContext(&ctx)
		c.Run(a)
		return
	}
	c.perform(a, target{pinned: true, key: c.pinOrAdd(ctx)})
}

func (c *Controller) perform(a assistant.Action, t target) {
	switch a {
	case assistant.Summarize, assistant.Tasks:
		c.start(request{kind: requestAction, action: a, label: assistant.Label(c.tr, a), inEffect: c.EffectiveContext(), target: &t}, true)
	default:
		c.pending = Pending{Kind: PendingAction, Action: a}
		c.pendingTarget = nil
		if c.IsPinned() {
			c.pendingTarget = &t
		}
		c.state()
		if c.OnFocusInput != nil {
			c.OnFocusInput()
		}
	}
}

// SummarizeUnread is Summarize Unread in This Folder, for a folder: sent at
// once.
func (c *Controller) SummarizeUnread(accountID, folderID string) {
	if c.closed || c.phase != PhaseIdle || accountID == "" || folderID == "" {
		return
	}
	c.start(request{
		kind: requestUnread, accountID: accountID, folderID: folderID,
		label: assistant.Label(c.tr, assistant.Unread), inEffect: c.EffectiveContext(),
	}, true)
}

// AskAttachment is an attachment's question: it waits for the user's
// words. Once the conversation keeps its context, the attachment's message
// is added to it when it is part of none of it (subject and threadID are
// the message's).
func (c *Controller) AskAttachment(accountID, messageID, partID, subject, threadID string) {
	if c.closed || c.phase != PhaseIdle || accountID == "" || messageID == "" || partID == "" {
		return
	}
	ctx := NewContext(assistant.Selection{AccountID: accountID, MessageIDs: []string{messageID}}, 1, false, subject, threadID)
	c.pending = Pending{Kind: PendingAttachment, AccountID: accountID, MessageID: messageID, PartID: partID}
	if c.IsPinned() {
		c.pendingTarget = &target{pinned: true, key: c.pinOrAdd(ctx)}
	} else {
		c.pendingTarget = &target{ctx: ctx}
	}
	c.state()
	if c.OnFocusInput != nil {
		c.OnFocusInput()
	}
}

// CancelPending drops the waiting action (a context it added stays).
func (c *Controller) CancelPending() {
	if c.pending.Kind == PendingNone {
		return
	}
	c.pending, c.pendingTarget = Pending{}, nil
	c.state()
}

// Submit is the question field's Send: with a waiting action its prompt and
// the words, otherwise a free question. False when nothing was taken
// (empty, or a question under way); the field keeps its text then.
func (c *Controller) Submit(text string) bool {
	if c.closed || c.phase != PhaseIdle {
		return false
	}
	words := strings.TrimSpace(text)
	switch c.pending.Kind {
	case PendingAction:
		a := c.pending.Action
		// A reply may be drafted without instructions.
		if words == "" && a != assistant.DraftReply {
			return false
		}
		t := c.pendingTarget
		if t == nil {
			e := c.EffectiveContext()
			if e == nil {
				return false
			}
			t = &target{ctx: *e}
		}
		c.start(request{kind: requestAction, action: a, label: assistant.Label(c.tr, a), text: words, inEffect: c.EffectiveContext(), target: t}, true)
	case PendingAttachment:
		if words == "" {
			return false
		}
		p := c.pending
		c.start(request{
			kind: requestAttachment, accountID: p.AccountID, messageID: p.MessageID, partID: p.PartID,
			label: assistant.Texts(c.tr).AskFile, text: words, inEffect: c.EffectiveContext(), target: c.pendingTarget,
		}, true)
	default:
		if words == "" {
			return false
		}
		c.start(request{kind: requestFree, text: words, inEffect: c.EffectiveContext()}, true)
	}
	return true
}

// Retry is Try Again on an error item: the same question once more.
func (c *Controller) Retry(itemID int) {
	if c.closed || c.phase != PhaseIdle || c.lastRequest == nil {
		return
	}
	idx := c.indexOf(itemID)
	if idx < 0 || c.items[idx].Content.Kind != ContentError || !c.items[idx].Content.Retry {
		return
	}
	c.items[idx].Content.Retry = false
	c.changed(ChangeUpdated, idx)
	c.start(*c.lastRequest, false)
}

// SignIn is Sign In… on an error item: Claude Code's own sign-in in the
// browser, then the same question once more.
func (c *Controller) SignIn(itemID int) {
	if c.closed || c.phase != PhaseIdle || c.lastRequest == nil {
		return
	}
	idx := c.indexOf(itemID)
	if idx < 0 || c.items[idx].Content.Kind != ContentError || c.items[idx].Content.Offer != OfferSignIn {
		return
	}
	c.items[idx].Content.Offer = OfferNone
	c.changed(ChangeUpdated, idx)
	req := *c.lastRequest
	req.signIn = true
	c.start(req, false)
}

// OpenDraftItem is a draft card's Open Draft.
func (c *Controller) OpenDraftItem(itemID int) {
	idx := c.indexOf(itemID)
	if idx < 0 || c.items[idx].Content.Kind != ContentDraft || c.OpenDraft == nil {
		return
	}
	c.OpenDraft(c.items[idx].Content.Draft)
}

func (c *Controller) indexOf(itemID int) int {
	for i, it := range c.items {
		if it.ID == itemID {
			return i
		}
	}
	return -1
}

// Stop ends the process and the turn under way. The conversation keeps its
// context; the next question starts a new process, which is told the
// context again.
func (c *Controller) Stop() {
	if c.phase == PhaseIdle {
		return
	}
	c.gen++
	c.endSignIn()
	c.endProcess()
	c.closeTurn()
	c.phase = PhaseIdle
	c.append(Content{Kind: ContentNote, Text: assistant.PanelTexts(c.tr).Stopped})
	c.state()
}

// NewConversation (the header's button and the bar's) ends the process,
// empties the transcript and forgets what the conversation was about; the
// chip follows the selection again.
func (c *Controller) NewConversation() {
	c.gen++
	c.endSignIn()
	c.endProcess()
	c.items = nil
	c.streaming = -1
	c.signingIn = -1
	c.activities = make(map[string]int)
	c.toolNames = make(map[string]string)
	c.pending, c.pendingTarget = Pending{}, nil
	c.lastRequest = nil
	c.pinned = nil
	c.resolving = make(map[int]*future[struct{}])
	c.contextRemoved = false
	c.phase = PhaseIdle
	c.changed(ChangeReset, 0)
	c.state()
}

// Sending

func (c *Controller) start(req request, echo bool) {
	if c.closed || c.phase != PhaseIdle {
		return
	}
	c.gen++
	my := c.gen
	if c.providerFactory != nil {
		c.selectedProvider = c.providerFactory()
	}
	c.phase = PhasePreparing
	c.state()
	c.loop.Post(func() { c.askConsent(req, my, echo) })
}

// askConsent is step 1: consent, once ever.
func (c *Controller) askConsent(req request, my int, echo bool) {
	if my != c.gen {
		return
	}
	if (c.selectedProvider != nil && c.selectedProvider.HasConsent()) || (c.selectedProvider == nil && c.settings.AssistantConsent()) {
		c.pin(req, my, echo)
		return
	}
	answer := func(allowed bool) {
		if my != c.gen {
			return
		}
		if !allowed {
			c.phase = PhaseIdle
			c.state()
			if req.text != "" && c.OnRestoreInput != nil {
				c.OnRestoreInput(req.text)
			}
			return
		}
		if c.selectedProvider != nil {
			c.selectedProvider.AcceptConsent()
		} else {
			c.settings.SetAssistantConsent(true)
		}
		c.pin(req, my, echo)
	}
	if c.Consent == nil {
		answer(false)
		return
	}
	c.Consent(answer)
}

// pin is step 2: the question in the transcript; the conversation's first
// question pins what the chip showed, and what the question is about joins
// it when it is part of none of it; a waiting action is used up. Then step
// 3 waits for the members the prompt needs.
func (c *Controller) pin(req request, my int, echo bool) {
	if len(c.pinned) == 0 {
		key := c.newKey()
		var ctx *Context
		if req.inEffect != nil {
			cp := *req.inEffect
			ctx = &cp
		}
		c.pinned = []Pinned{{Context: ctx, key: key}}
		if ctx != nil && ctx.Partial {
			c.resolvePinned(key)
		}
	}
	if req.target != nil && !req.target.pinned {
		req.target = &target{pinned: true, key: c.pinOrAdd(req.target.ctx)}
	}
	last := req
	last.signIn = false
	c.lastRequest = &last
	c.clearRetries()
	if echo {
		c.append(Content{Kind: ContentUser, Label: req.label, Text: req.text})
	}
	c.pending, c.pendingTarget = Pending{}, nil
	c.state()
	// A new Claude Code knows nothing of the conversation yet.
	if !c.hasSession() {
		for i := range c.pinned {
			c.pinned[i].Announced = false
		}
	}
	c.awaitMembers(req, my, c.unresolved(req))
}

// awaitMembers waits, one after the other, for the members of the pinned
// folded conversations in keys, then builds the prompt.
func (c *Controller) awaitMembers(req request, my int, keys []int) {
	if my != c.gen {
		return
	}
	if len(keys) == 0 {
		c.promptReady(req, my)
		return
	}
	c.resolvePinned(keys[0]).then(func(struct{}) { c.awaitMembers(req, my, keys[1:]) })
}

// promptReady builds the prompt and goes on to step 4.
func (c *Controller) promptReady(req request, my int) {
	prompt, told, err := c.prompt(req)
	if err != nil {
		c.log.Warn("assistant prompt", "err", err)
		c.fail(assistant.StoppedText(c.tr, err.Error()), false)
		return
	}
	if c.selectedProvider != nil {
		c.providerPrompt(my, prompt, told)
		return
	}
	if c.process != nil && c.process.Running() {
		c.send(prompt, told)
		return
	}
	// 4. Claude Code, started when the conversation has none.
	c.process = nil
	p := c.locator.Locate()
	if p == "" {
		c.offer(assistant.PanelTexts(c.tr).NotFound, true, OfferInstall)
		return
	}
	if c.bridge == "" {
		c.fail(assistant.PanelTexts(c.tr).ToolsMissing, false)
		return
	}
	launch := func() {
		proc, err := c.launch(p)
		if err != nil {
			c.log.Warn("assistant", "err", err)
			c.fail(assistant.StoppedText(c.tr, err.Error()), true)
			return
		}
		c.process = proc
		c.send(prompt, told)
	}
	if req.signIn {
		c.signInFirst(my, launch)
		return
	}
	c.locator.Refresh()
	c.locator.SignedIn(func(s SignIn) {
		if my != c.gen {
			return
		}
		if s.Known && !s.SignedIn {
			c.offer(assistant.PanelTexts(c.tr).NotSignedIn, false, OfferSignIn)
			return
		}
		launch()
	})
}

// signInFirst runs Claude Code's sign-in, shown as an activity line, and
// goes on with then once it is signed in; anything else ends the turn with
// the reason and Sign In… again.
func (c *Controller) signInFirst(my int, then func()) {
	texts := assistant.SignInTexts(c.tr)
	c.signingIn = c.append(Content{Kind: ContentActivity, Label: texts.Waiting})
	c.cancelSignIn = c.locator.SignIn(func(r SignInResult) {
		if my != c.gen {
			return
		}
		c.cancelSignIn = nil
		c.closeSignIn()
		switch r.Outcome {
		case SignInDone:
			then()
		case SignInFailed:
			c.offer(assistant.SignInFailedText(c.tr, r.Reason), false, OfferSignIn)
		case SignInTimedOut:
			c.offer(texts.TimedOut, false, OfferSignIn)
		case SignInNotFound:
			c.offer(assistant.PanelTexts(c.tr).NotFound, true, OfferInstall)
		default: // SignInCancelled: the settings' sign-in took its place
			c.offer(assistant.PanelTexts(c.tr).NotSignedIn, false, OfferSignIn)
		}
	})
}

// endSignIn ends the sign-in the question under way started; its end is
// not reported.
func (c *Controller) endSignIn() {
	cancel := c.cancelSignIn
	c.cancelSignIn = nil
	if cancel != nil {
		cancel()
	}
}

// closeSignIn: the sign-in's activity line is over.
func (c *Controller) closeSignIn() {
	idx := c.signingIn
	c.signingIn = -1
	if idx < 0 || idx >= len(c.items) {
		return
	}
	if it := &c.items[idx].Content; it.Kind == ContentActivity && !it.Done {
		it.Done = true
		c.changed(ChangeUpdated, idx)
	}
}

// send is step 5: the turn.
func (c *Controller) send(prompt string, told []int) {
	if c.process == nil || !c.process.Send(assistant.UserMessage(prompt)) {
		c.fail(assistant.StoppedText(c.tr, "claude is not running"), true)
		return
	}
	for i := range c.pinned {
		if slices.Contains(told, c.pinned[i].key) {
			c.pinned[i].Announced = true
		}
	}
	c.authFailed, c.refreshFailed = false, false
	c.phase = PhaseRunning
	c.state()
}

// prompt is the prompt of a question and the keys of the pinned contexts
// it tells the model about. A free question carries a line for every
// context not told yet (assistant.ContextPreamble for the conversation's
// first, assistant.AddedContextPreamble for one added later); a message
// action names its context's ids, and an attachment's question its
// message's.
func (c *Controller) prompt(req request) (string, []int, error) {
	switch req.kind {
	case requestFree:
		var lines []string
		var told []int
		for i, p := range c.pinned {
			if p.Announced {
				continue
			}
			told = append(told, p.key)
			if p.Context == nil {
				continue
			}
			line := assistant.AddedContextPreamble(p.Context.Selection)
			if i == 0 {
				line = assistant.ContextPreamble(p.Context.Selection)
			}
			if line != "" {
				lines = append(lines, line)
			}
		}
		if preamble := strings.Join(lines, "\n"); preamble != "" {
			return preamble + "\n\n" + req.text, told, nil
		}
		return req.text, told, nil
	case requestAction:
		if req.target == nil || !req.target.pinned {
			return "", nil, errNoMessages
		}
		ctx := c.pinnedContext(req.target.key)
		if ctx == nil {
			return "", nil, errNoMessages
		}
		p, err := assistant.Prompt(c.tr, assistant.App, req.action, ctx.Selection)
		if err != nil {
			return "", nil, err
		}
		return p + req.text, []int{req.target.key}, nil
	case requestUnread:
		p, err := assistant.UnreadPrompt(c.tr, req.accountID, req.folderID)
		return p, nil, err
	default: // requestAttachment
		p, err := assistant.AttachmentPrompt(c.tr, req.accountID, req.messageID, req.partID)
		if err != nil {
			return "", nil, err
		}
		p += req.text
		if req.target != nil && req.target.pinned {
			if ctx := c.pinnedContext(req.target.key); ctx != nil && ctx.Selection.AccountID == req.accountID &&
				slices.Equal(ctx.Selection.MessageIDs, []string{req.messageID}) {
				return p, []int{req.target.key}, nil
			}
		}
		return p, nil, nil
	}
}

// unresolved are the pinned folded conversations whose members the prompt
// of req needs: every one a free question tells the model about, a message
// action's own.
func (c *Controller) unresolved(req request) []int {
	var keys []int
	switch req.kind {
	case requestFree:
		for _, p := range c.pinned {
			if !p.Announced {
				keys = append(keys, p.key)
			}
		}
	case requestAction:
		if req.target != nil && req.target.pinned {
			keys = []int{req.target.key}
		}
	}
	var out []int
	for _, k := range keys {
		if ctx := c.pinnedContext(k); ctx != nil && ctx.Partial {
			out = append(out, k)
		}
	}
	return out
}

// resolvePinned asks once for the members of a pinned folded conversation
// (the application answers while the list still shows it selected, and
// with its own selection otherwise); a question that needs them waits for
// the same answer.
func (c *Controller) resolvePinned(key int) *future[struct{}] {
	if f, ok := c.resolving[key]; ok {
		return f
	}
	f := &future[struct{}]{}
	ctx := c.pinnedContext(key)
	if ctx == nil || !ctx.Partial {
		f.resolve(struct{}{})
		return f
	}
	c.resolving[key] = f
	resolving := c.resolving
	c.resolve(*ctx, func(sel assistant.Selection) {
		// A new conversation began meanwhile: its map is another.
		if c.resolving[key] == f {
			delete(resolving, key)
			c.settle(key, sel)
		}
		f.resolve(struct{}{})
	})
	return f
}

// settle takes the members of a pinned folded conversation.
func (c *Controller) settle(key int, sel assistant.Selection) {
	for i := range c.pinned {
		ctx := c.pinned[i].Context
		if c.pinned[i].key != key || ctx == nil || !ctx.Partial {
			continue
		}
		if len(sel.MessageIDs) == 0 || (sel.AccountID == ctx.Selection.AccountID && slices.Equal(sel.MessageIDs, ctx.Selection.MessageIDs)) {
			return
		}
		cp := *ctx
		cp.Selection = sel
		cp.Count = len(sel.MessageIDs)
		cp.Partial = false
		c.pinned[i].Context = &cp
		c.state()
		return
	}
}

// resolve calls done with the context's selection, its members asked for
// when partial; once, whatever answers first: the application or the
// timeout.
func (c *Controller) resolve(ctx Context, done func(assistant.Selection)) {
	if !ctx.Partial || c.ResolveContext == nil {
		done(ctx.Selection)
		return
	}
	fired := false
	once := func(sel assistant.Selection) {
		if fired {
			return
		}
		fired = true
		if len(sel.MessageIDs) == 0 {
			sel = ctx.Selection
		}
		done(sel)
	}
	c.ResolveContext(ctx, once)
	c.loop.After(c.ResolveTimeout, func() { once(ctx.Selection) })
}

// launch starts Claude Code for a new conversation.
func (c *Controller) launch(path string) (*Process, error) {
	if err := ensureDirectory(c.directory); err != nil {
		return nil, fmt.Errorf("the assistant's directory: %w", err)
	}
	args := assistant.Args(assistant.Options{
		Bridge: c.bridge, Socket: c.socket, Model: c.settings.AssistantModel(),
		SystemPrompt: assistant.SystemPrompt(c.Language(), c.Today()),
	})
	p := NewProcess(c.loop, c.log, path, args, assistant.ChildEnv(c.env, path), c.directory, c.killGrace)
	p.OnEvents = func(events []assistant.Event) {
		if p == c.process {
			c.handle(events)
		}
	}
	p.OnExit = func(e Exit) {
		if p == c.process {
			c.exited(e)
		}
	}
	if err := p.Start(); err != nil {
		return nil, err
	}
	return p, nil
}

// The stream

func (c *Controller) handle(events []assistant.Event) {
	for _, e := range events {
		if c.process == nil && c.providerSession == nil {
			return
		}
		switch e.Kind {
		case assistant.EventInit:
			if !e.BridgeConnected {
				c.log.Warn("assistant: the malachi MCP server is not connected")
				c.endProcess()
				c.fail(assistant.PanelTexts(c.tr).ToolsMissing, false)
				return
			}
		case assistant.EventTextDelta:
			if c.streaming >= 0 {
				c.items[c.streaming].Content.Text += e.Text
				c.changed(ChangeUpdated, c.streaming)
			} else if e.Text != "" {
				c.streaming = c.append(Content{Kind: ContentAssistant, Text: e.Text, Streaming: true})
			}
		case assistant.EventText:
			if c.streaming >= 0 {
				idx := c.streaming
				if e.Text != "" {
					c.items[idx].Content.Text = e.Text
				}
				c.items[idx].Content.Streaming = false
				c.streaming = -1
				c.changed(ChangeUpdated, idx)
			} else if e.Text != "" {
				c.append(Content{Kind: ContentAssistant, Text: e.Text})
			}
		case assistant.EventToolUse:
			c.closeStreaming()
			idx := c.append(Content{Kind: ContentActivity, Label: assistant.ActivityLabel(c.tr, e.Tool)})
			if e.ToolUseID != "" {
				c.activities[e.ToolUseID] = idx
				c.toolNames[e.ToolUseID] = e.Tool
			}
		case assistant.EventToolResult:
			if idx, ok := c.activities[e.ToolUseID]; ok {
				delete(c.activities, e.ToolUseID)
				if it := &c.items[idx].Content; it.Kind == ContentActivity && !it.Done {
					it.Done = true
					c.changed(ChangeUpdated, idx)
				}
			}
			tool := c.toolNames[e.ToolUseID]
			delete(c.toolNames, e.ToolUseID)
			if tool == "create_draft" && !e.IsError {
				if ref, ok := assistant.ParseDraftResult(e.ResultText); ok {
					c.append(Content{Kind: ContentDraft, Draft: ref})
				}
			}
		case assistant.EventFailure:
			// Claude Code's own words for a turn the API refused: the
			// result repeats them.
			c.log.Info("assistant: the API refused the turn", "failure", e.Failure)
			switch {
			case e.NotSignedIn():
				c.authFailed = true
			case e.RefreshFailed():
				c.refreshFailed = true
			}
		case assistant.EventResult:
			c.log.Info("assistant turn", "success", e.Success, "costUSD", e.CostUSD, "denied", len(e.Denied))
			for _, tool := range e.Denied {
				c.log.Info("assistant: denied", "tool", tool)
			}
			c.closeTurn()
			c.phase = PhaseIdle
			switch {
			case e.Success:
				c.lastRequest = nil
			case c.authFailed:
				// A new sign-in takes a new Claude Code.
				c.endProcess()
				c.append(Content{Kind: ContentError, Text: assistant.PanelTexts(c.tr).NotSignedIn, Offer: OfferSignIn})
			case c.refreshFailed:
				// Its words say what happened and what helps: Try Again
				// in a minute, or a new sign-in now; either way a new
				// Claude Code.
				c.endProcess()
				c.append(Content{Kind: ContentError, Text: assistant.StoppedText(c.tr, e.ResultText), Retry: true, Offer: OfferSignIn})
			default:
				c.append(Content{Kind: ContentError, Text: assistant.StoppedText(c.tr, e.ResultText), Retry: true})
			}
			c.authFailed, c.refreshFailed = false, false
			c.state()
		}
	}
}

// exited is the end of the process: during a turn that is an error with its
// reason; between turns (or while the next question is being prepared) the
// next question starts a new one.
func (c *Controller) exited(e Exit) {
	c.process = nil
	c.log.Info("assistant: claude ended", "status", e.Status)
	if c.phase != PhaseRunning {
		return
	}
	c.closeTurn()
	c.phase = PhaseIdle
	c.append(Content{Kind: ContentError, Text: assistant.StoppedText(c.tr, e.Description()), Retry: true})
	c.state()
}

// fail ends the turn with an error line.
func (c *Controller) fail(text string, retry bool) {
	c.offer(text, retry, OfferNone)
}

// offer ends the turn with an error line and its buttons.
func (c *Controller) offer(text string, retry bool, offer Offer) {
	c.closeTurn()
	c.phase = PhaseIdle
	c.append(Content{Kind: ContentError, Text: text, Retry: retry, Offer: offer})
	c.state()
}

// clearRetries: Try Again and the offers belong to the last question only.
func (c *Controller) clearRetries() {
	for i := range c.items {
		if it := &c.items[i].Content; it.Kind == ContentError && (it.Retry || it.Offer != OfferNone) {
			it.Retry, it.Offer = false, OfferNone
			c.changed(ChangeUpdated, i)
		}
	}
}

// endProcess terminates the conversation's process; its end is not
// reported.
func (c *Controller) endProcess() {
	if c.cancelProvider != nil {
		c.cancelProvider()
		c.cancelProvider = nil
	}
	session := c.providerSession
	c.providerSession = nil
	if session != nil {
		session.SetHandlers(nil, nil)
		session.Terminate()
	}
	p := c.process
	c.process = nil
	if p != nil {
		p.Terminate()
	}
}

// closeTurn: nothing streams any more and every activity is over.
func (c *Controller) closeTurn() {
	c.closeStreaming()
	c.closeSignIn()
	idxs := make([]int, 0, len(c.activities))
	for _, idx := range c.activities {
		idxs = append(idxs, idx)
	}
	slices.Sort(idxs)
	for _, idx := range idxs {
		if it := &c.items[idx].Content; it.Kind == ContentActivity && !it.Done {
			it.Done = true
			c.changed(ChangeUpdated, idx)
		}
	}
	c.activities = make(map[string]int)
	c.toolNames = make(map[string]string)
}

func (c *Controller) closeStreaming() {
	idx := c.streaming
	if idx < 0 {
		return
	}
	c.streaming = -1
	if it := &c.items[idx].Content; it.Kind == ContentAssistant && it.Streaming {
		it.Streaming = false
		c.changed(ChangeUpdated, idx)
	}
}

func (c *Controller) append(content Content) int {
	c.items = append(c.items, Item{ID: c.nextID, Content: content})
	c.nextID++
	idx := len(c.items) - 1
	c.changed(ChangeAppended, idx)
	return idx
}

func (c *Controller) changed(kind ChangeKind, idx int) {
	if c.OnChange != nil {
		c.OnChange(Change{Kind: kind, Index: idx})
	}
}

func (c *Controller) state() {
	if c.OnState != nil {
		c.OnState()
	}
}
