// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"reflect"
	"slices"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/capabilities"
	"github.com/schotek/malachi/ui/internal/conversation"
	"github.com/schotek/malachi/ui/internal/jira"
)

// The conversation view of the reading pane, the controller half: which
// conversation the pane shows, its model (ui/internal/conversation), the
// members' bodies as the cards ask for them, and the model kept in step
// with the list while it is shown. No GTK: the pane
// (conversation_view.go) lays the cards out and tells this controller
// which of them are near enough to need a body; the window
// (conversationHost in conversation_view.go) is its convHost. A port of
// the macOS client's MalachiCore/Controllers/ConversationController.swift.

// convHeldBytes is the held entries' bodies beyond which the entries of
// cards the pane does not keep near are let go (trim).
const convHeldBytes = 64 << 20

// convHost is what the controller needs of the window: the grouped list's
// folder members of a conversation (shared with its unfolded row and the
// toolbar's actions, so that thread.get runs once for all of them), the
// accounts, and the message cache.
type convHost interface {
	// convMembers is what the list knows of the folder members of tid,
	// nil when the conversation is not listed. The controller only reads
	// it; the list replaces it (a new pointer) when it lists the folder
	// again or thread.get answers.
	convMembers(tid api.ThreadID) *threadMembers
	// convSummary is the summary of the listed conversation row of tid.
	convSummary(tid api.ThreadID) (api.ThreadSummary, bool)
	// convEnsureMembers has the list ask for the folder members of tid
	// (thread.get scoped to the listed folder) unless it knows them, or
	// asks already, and run then once they are known: at once when they
	// are. A failure only changes the list, which calls membersChanged.
	convEnsureMembers(tid api.ThreadID, then func())
	// convAccount is the account id, if known.
	convAccount(id api.AccountID) (api.Account, bool)
	// convComposeAccount says some enabled account composes (the forward
	// of an issue goes out from a mail account).
	convComposeAccount() bool
	// convFetch has the message cache fetch message s: its body
	// (message.body) and, with full, the whole message (message.get) as
	// well, each only when the cache lacks it and no request is in flight.
	// quoted is the variant of the body the card shows: with its quoted
	// history (true) or without (message.body with trimQuoted); a variant
	// held already shows at once wherever the message is on display
	// (switchQuoted). then runs on the main loop after every answer with
	// the cache entry so far, and at once when the entry has what was
	// asked for.
	convFetch(s api.MessageSummary, full, quoted bool, then func(*loadedMessage))
}

// convChange is what changed, for the pane.
type convChange int

const (
	// convLoading: a conversation was selected; its members are on their
	// way (model is nil).
	convLoading convChange = iota
	// convOpened: the model was built anew; the pane lays the stack out,
	// newest first, and opens at its top (convDisplayOrder).
	convOpened
	// convUpdated: the shown conversation changed (a member arrived or
	// went, its flags or issue changed, the daemon rebuilt its messages);
	// the pane reconciles its cards by id and keeps what the user reads in
	// place.
	convUpdated
	// convCleared: nothing is shown any more.
	convCleared
)

// rowShowsConversation reports a folded conversation row of the grouped
// list whose selection shows the whole conversation in the reading pane: a
// conversation row (not a member row, not a single-message row) with two
// or more members in the folder, or one and the user's replies in Sent
// (conversation.IsConversationRow). Every other row shows its message
// alone. The outbox is never grouped.
func rowShowsConversation(r listRow) bool {
	return r.Thread && !r.Member && r.Key.Thread != "" && r.Key.Message == "" &&
		conversation.IsConversationRow(r.Summary)
}

// conversationController holds which conversation the pane shows and what
// of it is loaded. The members come from the list (convEnsureMembers),
// with the user's replies in Sent the folder lacks (threadMembers.sent,
// cards with conversation.Item.Sent: never marked read, never among the
// members the actions take), the bodies from the message cache, one card at a time and only for the cards
// the pane asks for (needsBody): message.body alone, and message.get too
// only for a card that needs what it adds to the summary (the attachment
// chips, the Cc of the recipients). The entries of the cards are held here
// (loaded), so a long conversation does not lose them to the cache's caps
// while it is shown.
//
// Marking read is the window's delayed mark-read (scheduleMarkRead): the
// pane arms it once per selection with takeMarkRead.
type conversationController struct {
	host convHost
	tr   conversation.Translator

	// thread is the conversation on display, "" when the pane shows
	// something else; model its model, nil while the members are on
	// their way.
	thread api.ThreadID
	model  *conversation.Model
	// issue is what the model's issue card was built from (the thread's
	// issue, or a member's): the widget of the card needs the whole of it.
	issue *api.IssueInfo
	// loaded are the cache entries of the cards whose body was asked for.
	loaded map[api.MessageID]*loadedMessage

	// onChange is called after every change of thread or model; onLoaded
	// when a card's entry has news (a half of it arrived through
	// needsBody). The window's own fan-out (showLoaded and the rest) tells
	// the pane about later news, such as remote images.
	onChange func(convChange)
	onLoaded func(api.MessageID, *loadedMessage)

	// members are the folder members the model was last built or merged
	// from; sent the user's replies in Sent likewise.
	members []api.MessageSummary
	sent    []api.MessageSummary
	// quoted are the cards whose quoted history the user revealed (Show
	// Quoted Text): kept through updates and a rebuild of the messages,
	// forgotten with the conversation.
	quoted conversation.QuotedReveal
	// pending are the members whose entry was asked for and has not
	// settled, and whether message.get was part of the request; noGet the
	// members whose message.get failed, not asked again for this
	// conversation.
	pending map[api.MessageID]bool
	noGet   map[api.MessageID]bool
	// summary is the conversation's summary as last known (the row's,
	// then the list's after thread.get).
	summary api.ThreadSummary
	// asked is the members object thread.get was asked for, failed the one
	// whose thread.get failed: an incomplete members object that is still
	// asked, and no longer fetching, is a failure; another one is the list
	// listing the folder again.
	asked, failed *threadMembers
	// listing: the model was built from what the listing knew, after
	// thread.get failed; the members, once they arrive, build it anew.
	listing bool
	// markTaken: the member to mark read was handed out for this
	// selection.
	markTaken bool
	// gen is bumped with every selection: a late answer for a conversation
	// left meanwhile is dropped. bodyGen is bumped by refresh: a body asked
	// for before the daemon rebuilt the conversation's messages is dropped
	// when it arrives, so that it cannot land beside the fresh one.
	gen, bodyGen uint64
}

// newConversationController is a controller over host; tr gives the texts
// of the model.
func newConversationController(host convHost, tr conversation.Translator) *conversationController {
	return &conversationController{
		host:    host,
		tr:      tr,
		loaded:  map[api.MessageID]*loadedMessage{},
		pending: map[api.MessageID]bool{},
		noGet:   map[api.MessageID]bool{},
	}
}

func (c *conversationController) emit(ch convChange) {
	if c.onChange != nil {
		c.onChange(ch)
	}
}

// show takes the list's selection: a conversation row
// (rowShowsConversation) is shown here, and true is returned; anything
// else clears the conversation and returns false (the pane shows the
// row's message, or its empty page). The same conversation announced
// again keeps what is shown.
func (c *conversationController) show(row listRow) bool {
	if !rowShowsConversation(row) {
		c.clear()
		return false
	}
	tid := row.Key.Thread
	if tid == c.thread {
		c.summary = row.Summary
		return true
	}
	c.reset()
	c.thread = tid
	c.quoted.Show(string(tid))
	c.summary = row.Summary
	c.emit(convLoading)
	c.requestMembers()
	return true
}

// clear shows nothing (another kind of row, or none, is selected).
func (c *conversationController) clear() {
	had := c.thread != ""
	c.reset()
	if had {
		c.emit(convCleared)
	}
}

// reset forgets the conversation on display.
func (c *conversationController) reset() {
	c.gen++
	c.thread = ""
	c.model = nil
	c.issue = nil
	c.members = nil
	c.sent = nil
	c.quoted.Clear()
	c.loaded = map[api.MessageID]*loadedMessage{}
	c.pending = map[api.MessageID]bool{}
	c.noGet = map[api.MessageID]bool{}
	c.summary = api.ThreadSummary{}
	c.asked, c.failed = nil, nil
	c.listing = false
	c.markTaken = false
}

// requestMembers asks the list for the folder members of the conversation
// on display; they arrive through membersChanged.
func (c *conversationController) requestMembers() {
	tid := c.thread
	mem := c.host.convMembers(tid)
	if mem == nil {
		return
	}
	c.asked = mem
	g := c.gen
	c.host.convEnsureMembers(tid, func() {
		if c.gen == g && c.thread == tid {
			c.membersChanged()
		}
	})
}

// membersChanged is called whenever the list changed (the window's syncRows)
// and when the asked members arrived: the model is built the first time,
// then kept in step by merging what changed and removing what went
// (conversation.Merge, Remove), and so are the user's replies in Sent
// (conversation.MergeSent; a refetch of the list brings them). Members the list lost to a reload are asked
// for again; when thread.get failed, the conversation is built from what
// the listing told (its newest member, with the row of the older ones on
// top), and nothing is marked read.
func (c *conversationController) membersChanged() {
	if c.thread == "" {
		return
	}
	tid := c.thread
	mem := c.host.convMembers(tid)
	if mem == nil {
		return // no longer listed: the selection moves, and clears this
	}
	if !mem.complete {
		switch {
		case mem.fetching:
		case mem == c.asked:
			if c.failed != mem {
				c.failed = mem
				if c.model == nil {
					c.buildFromListing(mem)
				}
			}
		default:
			c.requestMembers()
		}
		return
	}
	if t, ok := c.host.convSummary(tid); ok {
		c.summary = t
	}
	// The account tells the user's own mail (conversation.Item.Mine).
	own, _ := c.host.convAccount(c.summary.AccountID)
	if c.model == nil || c.listing {
		c.listing = false
		c.open(mem.list, mem.sent, own)
		return
	}
	current := *c.model
	m := current
	issue := c.issue
	before := make(map[api.MessageID]api.MessageSummary, len(c.members))
	for _, s := range c.members {
		if _, ok := before[s.ID]; !ok {
			before[s.ID] = s
		}
	}
	now := make(map[api.MessageID]bool, len(mem.list))
	for _, s := range mem.list {
		now[s.ID] = true
		if b, ok := before[s.ID]; ok && reflect.DeepEqual(b, s) {
			continue
		}
		m = conversation.Merge(m, s, own, c.tr)
		if s.Issue != nil {
			info := s.Issue.IssueInfo
			issue = &info
		}
	}
	for _, s := range c.members {
		if now[s.ID] {
			continue
		}
		m = conversation.Remove(m, s.ID)
		delete(c.loaded, s.ID)
		delete(c.pending, s.ID)
	}
	// The replies after the members: a member that took a reply's place
	// (Merge) is not merged back as a reply.
	sentBefore := make(map[api.MessageID]api.MessageSummary, len(c.sent))
	for _, s := range c.sent {
		if _, ok := sentBefore[s.ID]; !ok {
			sentBefore[s.ID] = s
		}
	}
	sentNow := make(map[api.MessageID]bool, len(mem.sent))
	for _, s := range mem.sent {
		sentNow[s.ID] = true
	}
	for _, s := range c.sent {
		if sentNow[s.ID] || now[s.ID] {
			continue
		}
		m = conversation.Remove(m, s.ID)
		delete(c.loaded, s.ID)
		delete(c.pending, s.ID)
	}
	for _, s := range mem.sent {
		if b, ok := sentBefore[s.ID]; ok && reflect.DeepEqual(b, s) {
			continue
		}
		m = conversation.MergeSent(m, s, own, c.tr)
	}
	c.members = append([]api.MessageSummary(nil), mem.list...)
	c.sent = append([]api.MessageSummary(nil), mem.sent...)
	if len(m.Items) == 0 && m.Earlier > 0 {
		// Every shown member went while older ones are left out: what the
		// list holds now is the conversation.
		c.open(mem.list, mem.sent, own)
		return
	}
	if reflect.DeepEqual(m, current) {
		return
	}
	c.model = &m
	c.issue = issue
	c.emit(convUpdated)
}

// open builds the model from members and the user's replies in Sent and
// announces it.
func (c *conversationController) open(members, sent []api.MessageSummary, own api.Account) {
	c.members = append([]api.MessageSummary(nil), members...)
	c.sent = append([]api.MessageSummary(nil), sent...)
	m := conversation.Build(c.summary, c.members, c.sent, own, c.tr)
	c.model = &m
	c.issue = convIssueOf(c.summary, c.members)
	c.emit(convOpened)
}

// buildFromListing is the conversation as the listing knows it, after
// thread.get failed (the list has said why).
func (c *conversationController) buildFromListing(mem *threadMembers) {
	known := mem.list
	if len(known) == 0 {
		known = []api.MessageSummary{c.summary.Latest}
	}
	own, _ := c.host.convAccount(c.summary.AccountID)
	c.listing = true
	c.open(known, mem.sent, own)
}

// convIssueOf is the issue a model's card is built from, as
// conversation.Build picks it: the thread's, else the newest member's.
func convIssueOf(t api.ThreadSummary, members []api.MessageSummary) *api.IssueInfo {
	if t.Issue != nil {
		info := *t.Issue
		return &info
	}
	for i := len(members) - 1; i >= 0; i-- {
		if members[i].Issue != nil {
			info := members[i].Issue.IssueInfo
			return &info
		}
	}
	return nil
}

// refresh: the daemon rebuilt the messages of conversation tid in place
// (notify.messagesChanged: a Jira pass with other rendering settings, a
// comment edited or re-attributed, the issue renamed; docs/api.md §5). The
// held entries are let go (the window's message cache lets go of its own)
// and the pane is told the conversation changed, so it asks for the bodies
// of the cards near the viewport again; the cards keep what they show
// until the fresh body arrives. The members come back through
// membersChanged once the list asked thread.get for them.
func (c *conversationController) refresh(tid api.ThreadID) {
	if tid == "" || tid != c.thread {
		return
	}
	c.loaded = map[api.MessageID]*loadedMessage{}
	c.pending = map[api.MessageID]bool{}
	c.noGet = map[api.MessageID]bool{}
	c.bodyGen++
	if c.model != nil {
		c.emit(convUpdated)
	}
}

// takeMarkRead is the member opening the conversation marks read
// (conversation.Model.MarkRead), once per selection: "" before the members
// arrived, after thread.get failed (nothing is marked without them), and
// every time after the first. A member the user marks unread afterwards
// stays unread.
func (c *conversationController) takeMarkRead() api.MessageID {
	if c.model == nil || c.listing || c.markTaken {
		return ""
	}
	c.markTaken = true
	return c.model.MarkRead
}

// needsBody: the card of member id is near the viewport, and its body is
// fetched unless held already (message.body; message.get as well when
// details, or when the message has attachments, whose chips need it). An
// event has no body. The entry is held in loaded and announced through
// onLoaded whenever a half of it arrives. The body is the variant the user
// chose for the card: without its quoted history unless revealed
// (setQuoted).
func (c *conversationController) needsBody(id api.MessageID, details bool) {
	s, ok := c.member(id)
	if !ok || jira.IsEvent(s.Issue) {
		return
	}
	full := (details || s.HasAttachments) && !c.noGet[id]
	reveal := c.quoted.IsRevealed(id)
	if lm := c.loaded[id]; lm != nil && lm.quotedShown == reveal && lm.bodySettled() && (!full || lm.msg != nil) {
		return
	}
	// Asked already (the pane asks on every scroll): the answer comes.
	if asked, ok := c.pending[id]; ok && (asked || !full) {
		return
	}
	c.pending[id] = full
	g, bg := c.gen, c.bodyGen
	c.host.convFetch(s, full, reveal, func(lm *loadedMessage) {
		if c.gen != g || c.bodyGen != bg {
			return
		}
		if _, ok := c.member(id); !ok {
			return
		}
		if !lm.getting && !lm.fetching {
			delete(c.pending, id)
			if full && lm.msg == nil {
				c.noGet[id] = true // logged by the cache; the summary serves
			}
		}
		c.loaded[id] = lm
		if c.onLoaded != nil {
			c.onLoaded(id, lm)
		}
	})
}

// quotedRevealed reports whether the quoted history of the card of member
// id shows.
func (c *conversationController) quotedRevealed(id api.MessageID) bool {
	return c.quoted.IsRevealed(id)
}

// setQuoted is the card's Show Quoted Text (on) or Hide Quoted Text: the
// choice holds while the conversation is shown, and the card's body
// switches to that variant (the one held, or fetched as needsBody does;
// details as there).
func (c *conversationController) setQuoted(id api.MessageID, on, details bool) {
	if _, ok := c.member(id); !ok {
		return
	}
	c.quoted.Set(id, on)
	// A request for the other variant is in flight: this one is asked for
	// beside it.
	delete(c.pending, id)
	c.needsBody(id, details)
}

// adopt: the window has news about member id (its fan-out: the remote
// images, a download), and the card's entry is lm from now on. An id that
// is not shown is ignored.
func (c *conversationController) adopt(id api.MessageID, lm *loadedMessage) {
	if _, ok := c.member(id); ok && lm != nil {
		c.loaded[id] = lm
	}
}

// trim lets go of the entries of the cards not in near while the held
// bodies are above budget bytes, the largest first: their cards keep what
// they show and ask again when they come near.
func (c *conversationController) trim(near map[api.MessageID]bool, budget int) {
	total := 0
	for _, lm := range c.loaded {
		total += lm.size()
	}
	if total <= budget {
		return
	}
	type entry struct {
		id   api.MessageID
		size int
	}
	var far []entry
	for id, lm := range c.loaded {
		if !near[id] {
			far = append(far, entry{id, lm.size()})
		}
	}
	// The largest first, then by id, so that the order is the same every
	// time.
	slices.SortFunc(far, func(a, b entry) int {
		if a.size != b.size {
			return b.size - a.size
		}
		return strings.Compare(string(a.id), string(b.id))
	})
	for _, e := range far {
		if total <= budget {
			break
		}
		total -= e.size
		delete(c.loaded, e.id)
	}
}

// member is the shown member id, as the model has it now.
func (c *conversationController) member(id api.MessageID) (api.MessageSummary, bool) {
	if c.model == nil {
		return api.MessageSummary{}, false
	}
	i := c.model.Index(id)
	if i < 0 {
		return api.MessageSummary{}, false
	}
	return c.model.Items[i].Message, true
}

// actions are the buttons the card of s offers (conversation.CardActions):
// none for an account the window does not know.
func (c *conversationController) actions(s api.MessageSummary) capabilities.Actions {
	a, ok := c.host.convAccount(s.AccountID)
	if !ok {
		return capabilities.Actions{}
	}
	return conversation.CardActions(a, s, c.host.convComposeAccount())
}
