// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/signin"
)

// The board from the daemon (docs/api.md §4.13): board.list for the cases,
// account.list for the accounts' names, board.get for a case's
// conversation when it is selected, and the user's decisions written back
// with board.setState, setDone, remind, archive, unflag, discardDraft and
// setCommitment.
//
// It subscribes to nothing itself: the window's notification fan-out calls
// BoardChanged on notify.boardChanged, AccountsChanged on
// notify.accountsChanged and ConnectionChanged when the connection comes
// and goes; Start loads the first time. A notification is answered after
// the debounce, several in that time with one board.list. One board.list
// is on its way at a time: asked for again meanwhile, it runs once more
// after the reply is applied, so a slow list under a stream of
// notifications still lands. A lost connection drops the reply on its way
// (generation counter). While the daemon cannot be asked the last snapshot
// stays, in PhaseUnavailable; a list the daemon could not answer is
// PhaseFailed and is asked again after a back-off (a few seconds, growing
// to a minute), and a daemon without the board is PhaseUnsupported.
//
// Writes are optimistic: the change is laid over the daemon's data at once
// (one report: the controller's selection logic takes it as the result of
// its write), the call follows, and its answer replaces the case; a refused
// write is taken back and Handlers.Error says why. A list asked for before
// a write's answer never undoes it: a case keeps the newer version, a
// promise the answered state until a list asked for later arrives. The
// conversations are cached by case and version, the ConversationsKept most
// recently used: a case that did not change is not asked for again.
//
// The macOS client leads (MalachiCore/Board/DaemonBoardSource.swift); this
// is its port. Every method runs on the main loop; the calls run on their
// own goroutines and come back through the Loop, whose timers also carry
// the debounce and the back-off (the tests drive both).

// Caller is the one thing the source needs of the daemon: a call. It
// blocks and is called off the main loop. *client.Client is one; the tests
// pass a fake.
type Caller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

// Loop runs callbacks on the application's main loop, where the source and
// the controller live (the method set of assistantpanel.Loop, so the
// window passes the same one).
type Loop interface {
	// Post runs f on the main loop, after what was posted before; it may be
	// called from any goroutine.
	Post(f func())
	// After runs f on the main loop once d has passed.
	After(d time.Duration, f func())
}

// The source's defaults.
const (
	// DefaultDebounce is how long a notification waits for others before
	// the board is listed again.
	DefaultDebounce = 300 * time.Millisecond
	// ConversationsKept is how many conversations (board.get) the source
	// keeps, the most recently used.
	ConversationsKept = 50
	// DefaultCallTimeout bounds every call (the macOS client's default).
	DefaultCallTimeout = 30 * time.Second
)

// RetryDelay is the back-off before the attempt-th retry of a failed
// board.list (from 1): 2, 4, 8, 16, 32 seconds, then a minute.
func RetryDelay(attempt int) time.Duration {
	return time.Duration(min(60, 1<<min(max(attempt, 1), 6))) * time.Second
}

// DaemonOptions tune a DaemonSource; the zero value is the defaults.
type DaemonOptions struct {
	// Debounce is DefaultDebounce when 0.
	Debounce time.Duration
	// RetryDelay is RetryDelay when nil.
	RetryDelay func(attempt int) time.Duration
	// CallTimeout is DefaultCallTimeout when 0.
	CallTimeout time.Duration
	// Log may be nil. Only method names and errors are logged, never a
	// case's text.
	Log *slog.Logger
}

// DaemonSource is the board from the daemon (DaemonBoardSource). Make it
// with NewDaemonSource, install the controller (NewController), then
// Start.
type DaemonSource struct {
	// OnSnapshot is called after Handlers.Change with the new snapshot, for
	// the application's board triage, which is not the board controller of
	// this window.
	OnSnapshot func(Snapshot)

	caller      Caller
	loop        Loop
	tr          Translator
	log         *slog.Logger
	debounce    time.Duration
	retryDelay  func(int) time.Duration
	callTimeout time.Duration

	snapshot Snapshot
	h        Handlers

	// base is the daemon's data as last answered (board.list, then each
	// write's and board.get's case), without the writes under way.
	base     boardBase
	accounts []AccountInfo
	phase    Phase
	// overlays are the writes under way, laid over base in order.
	overlays  []overlay
	nextToken int
	// messages are the conversations by case, with the version they are
	// of; messageOrder the cases of messages, the least recently used
	// first.
	messages     map[CaseID]conversation
	messageOrder []CaseID
	// loading and failed are the version of a case whose conversation is
	// being loaded, or whose load failed.
	loading map[CaseID]int64
	failed  map[CaseID]int64
	// commitmentPins are promises as board.setCommitment answered them,
	// with the list generation of that moment: a list asked for no later
	// is older than the answer and does not replace them.
	commitmentPins map[api.BoardCommitmentID]commitmentPin
	// listGeneration and accountGeneration are the latest board.list and
	// account.list asked for; older replies are dropped. listInFlight and
	// accountsInFlight are the generation on its way, 0 for none.
	listGeneration, accountGeneration int
	listInFlight, accountsInFlight    int
	// listAgain: another board.list was asked for while one was on its
	// way.
	listAgain bool
	// The debounce and the next try after a failed board.list: armed, and
	// the token that cancels a timer by changing.
	debounceArmed, retryArmed bool
	debounceToken, retryToken int
	// failures counts the board.lists that failed in a row.
	failures int
	stopped  bool
	// started is between Start and Stop: nothing loads on its own outside
	// it.
	started bool
	// writesInFlight counts the writes whose answer has not arrived.
	writesInFlight int
}

// boardBase is the daemon's side of the snapshot.
type boardBase struct {
	cases       []Case
	commitments []Commitment
	annotated   bool
	run         *Run
	triage      TriageInfo
	truncated   bool
}

type overlay struct {
	token int
	apply func(*Snapshot)
}

type conversation struct {
	version int64
	list    []CaseMessage
}

type commitmentPin struct {
	commitment Commitment
	after      int
}

// NewDaemonSource makes the daemon's source; tr writes its toasts. It is in
// PhaseLoading and asks for nothing before Start.
func NewDaemonSource(c Caller, loop Loop, tr Translator, o DaemonOptions) *DaemonSource {
	d := &DaemonSource{
		caller: c, loop: loop, tr: tr, log: o.Log, debounce: o.Debounce, retryDelay: o.RetryDelay,
		callTimeout: o.CallTimeout, snapshot: Snapshot{Phase: PhaseLoading}, phase: PhaseLoading,
		messages: map[CaseID]conversation{}, loading: map[CaseID]int64{}, failed: map[CaseID]int64{},
		commitmentPins: map[api.BoardCommitmentID]commitmentPin{},
	}
	if d.log == nil {
		d.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.debounce <= 0 {
		d.debounce = DefaultDebounce
	}
	if d.retryDelay == nil {
		d.retryDelay = RetryDelay
	}
	if d.callTimeout <= 0 {
		d.callTimeout = DefaultCallTimeout
	}
	return d
}

// Snapshot implements DataSource.
func (d *DaemonSource) Snapshot() Snapshot { return d.snapshot }

// SetHandlers implements DataSource.
func (d *DaemonSource) SetHandlers(h Handlers) { d.h = h }

// Phase is how far the data is (Snapshot().Phase).
func (d *DaemonSource) Phase() Phase { return d.snapshot.Phase }

// idle reports that nothing is waiting or on its way (a debounce, a retry,
// board.list, account.list, a write, board.get of a case on the board):
// what the tests wait for before they count calls.
func (d *DaemonSource) idle() bool {
	return !d.debounceArmed && !d.retryArmed && d.listInFlight == 0 && d.accountsInFlight == 0 &&
		d.writesInFlight == 0 && len(d.loading) == 0
}

// Start loads the accounts and the board.
func (d *DaemonSource) Start() {
	d.stopped = false
	d.started = true
	d.Refresh()
}

// Stop stops: no call is made and no reply of a list or a conversation is
// taken any more (a write's answer still is: it ends what the user did).
func (d *DaemonSource) Stop() {
	d.stopped = true
	d.started = false
	d.cancelTimers()
}

// BoardChanged is notify.boardChanged: the board is listed again after the
// debounce. The accounts it names are not used: one board.list covers them
// all.
func (d *DaemonSource) BoardChanged(api.BoardChangedNotification) {
	if !d.started || d.stopped || d.debounceArmed {
		return
	}
	d.debounceArmed = true
	d.debounceToken++
	token := d.debounceToken
	d.loop.After(d.debounce, func() {
		if !d.debounceArmed || token != d.debounceToken {
			return
		}
		d.debounceArmed = false
		d.loadList()
	})
}

// AccountsChanged is notify.accountsChanged: the accounts' names and the
// board again.
func (d *DaemonSource) AccountsChanged() {
	if !d.started {
		return
	}
	d.Refresh()
}

// ConnectionChanged is the connection coming (everything is loaded again)
// or going (the snapshot stays, in PhaseUnavailable).
func (d *DaemonSource) ConnectionChanged(connected bool) {
	if !d.started {
		return
	}
	if connected {
		d.Refresh()
		return
	}
	d.cancelTimers()
	// Replies on the way are dropped: they come from the old connection,
	// or fail.
	d.listGeneration++
	d.accountGeneration++
	d.listInFlight = 0
	d.accountsInFlight = 0
	d.listAgain = false
	d.failures = 0
	d.setPhase(PhaseUnavailable)
}

// Refresh implements DataSource: it asks for the accounts and the board
// now (a retry waiting is brought forward).
func (d *DaemonSource) Refresh() {
	if !d.started || d.stopped {
		return
	}
	d.loadAccounts()
	d.loadList()
}

func (d *DaemonSource) cancelTimers() {
	d.debounceArmed = false
	d.debounceToken++
	d.cancelRetry()
}

func (d *DaemonSource) cancelRetry() {
	d.retryArmed = false
	d.retryToken++
}

// call runs one call on its own goroutine and hands its error to done on
// the main loop; result is filled by then.
func (d *DaemonSource) call(method string, params, result any, done func(error)) {
	timeout := d.callTimeout
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := d.caller.Call(ctx, method, params, result)
		d.loop.Post(func() { done(err) })
	}()
}

func (d *DaemonSource) loadList() {
	if !d.started || d.stopped {
		return
	}
	if d.listInFlight != 0 {
		d.listAgain = true
		return
	}
	d.cancelRetry()
	d.listGeneration++
	generation := d.listGeneration
	d.listInFlight = generation
	r := new(api.BoardListResult)
	d.call(api.MethodBoardList, api.BoardListParams{}, r, func(err error) {
		if d.listInFlight == generation {
			d.listInFlight = 0
		}
		if d.stopped || generation != d.listGeneration {
			return
		}
		if err != nil {
			d.log.Info("board.list failed", "err", err)
			d.listFailed(err)
		} else {
			d.failures = 0
			d.apply(r, generation)
		}
		if d.listAgain {
			d.listAgain = false
			d.loadList()
		}
	})
}

// listFailed decides the phase by why board.list failed: not connected
// (the reconnect lists again), a daemon without the board, or anything
// else, which is asked again after the back-off.
func (d *DaemonSource) listFailed(err error) {
	if isDisconnected(err) {
		d.setPhase(PhaseUnavailable)
		return
	}
	if isUnsupported(err) {
		d.setPhase(PhaseUnsupported)
		return
	}
	d.setPhase(PhaseFailed)
	d.failures++
	delay := d.retryDelay(d.failures)
	d.cancelRetry()
	d.retryArmed = true
	token := d.retryToken
	d.loop.After(delay, func() {
		if !d.retryArmed || token != d.retryToken {
			return
		}
		d.retryArmed = false
		d.loadList()
	})
}

func (d *DaemonSource) loadAccounts() {
	if !d.started || d.stopped {
		return
	}
	d.accountGeneration++
	generation := d.accountGeneration
	d.accountsInFlight = generation
	r := new(api.AccountListResult)
	d.call(api.MethodAccountList, api.AccountListParams{}, r, func(err error) {
		if d.accountsInFlight == generation {
			d.accountsInFlight = 0
		}
		if d.stopped || generation != d.accountGeneration || err != nil {
			return
		}
		var accounts []AccountInfo
		for _, a := range r.Accounts {
			if !a.Enabled {
				continue
			}
			accounts = append(accounts, AccountInfo{
				ID: a.ID, Name: accountLabel(a), Badge: accountBadge(a),
				CanReply: a.Can(api.CapabilityReply) || a.Can(api.CapabilityComment),
			})
		}
		d.accounts = accounts
		d.publish()
	})
}

// apply takes board.list's answer, asked for as generation.
func (d *DaemonSource) apply(r *api.BoardListResult, generation int) {
	known := make(map[CaseID]Case, len(d.base.cases))
	for _, c := range d.base.cases {
		if _, ok := known[c.ID]; !ok {
			known[c.ID] = c
		}
	}
	cases := make([]Case, 0, len(r.Cases))
	for _, w := range r.Cases {
		c := convertCase(w)
		// A list asked for before a write answered can be older than the
		// write's case: the newer version stays.
		if k, ok := known[c.ID]; ok && k.Version > c.Version {
			cases = append(cases, k)
		} else {
			cases = append(cases, c)
		}
	}
	// Likewise a promise board.setCommitment answered after this list was
	// asked for; a list asked for later has the last word.
	commitments := make([]Commitment, 0, len(r.Commitments))
	for _, k := range r.Commitments {
		commitments = append(commitments, convertCommitment(k))
	}
	for id, pin := range d.commitmentPins {
		if generation > pin.after {
			delete(d.commitmentPins, id)
			continue
		}
		if i := slices.IndexFunc(commitments, func(k Commitment) bool { return k.ID == id }); i >= 0 {
			commitments[i] = pin.commitment
		}
	}
	d.base = boardBase{
		cases: cases, commitments: commitments, annotated: r.Assistant, run: convertRun(r.Triage.LastRun),
		triage: TriageInfo{
			Queue: r.Triage.Queue, AnnotatedToday: r.Triage.AnnotatedTodayAuto, Usage24h: r.Triage.Usage24h,
		},
		truncated: r.Truncated,
	}
	ids := make(map[CaseID]bool, len(cases))
	for _, c := range cases {
		ids[c.ID] = true
	}
	for id := range d.messages {
		if !ids[id] {
			delete(d.messages, id)
		}
	}
	d.messageOrder = slices.DeleteFunc(d.messageOrder, func(id CaseID) bool { return !ids[id] })
	for id := range d.loading {
		if !ids[id] {
			delete(d.loading, id)
		}
	}
	for id := range d.failed {
		if !ids[id] {
			delete(d.failed, id)
		}
	}
	switch {
	case !r.Enabled:
		d.phase = PhaseOff
	case r.Ready:
		d.phase = PhaseReady
	default:
		d.phase = PhasePreparing
	}
	d.publish()
}

func (d *DaemonSource) setPhase(p Phase) {
	if d.phase == p {
		return
	}
	d.phase = p
	d.publish()
}

// publish builds the snapshot from the daemon's data, the conversations
// and the writes under way, and reports it when it changed.
func (d *DaemonSource) publish() {
	cases := make([]Case, len(d.base.cases))
	for i, c := range d.base.cases {
		if m, ok := d.messages[c.ID]; ok {
			c.Messages = m.list
			c.MessagesLoaded = true
		} else if _, loading := d.loading[c.ID]; !loading {
			if _, failed := d.failed[c.ID]; failed {
				c.MessagesFailed = true
			}
		}
		cases[i] = c
	}
	s := Snapshot{
		Accounts: d.accounts, Cases: cases, Commitments: d.base.commitments, Annotated: d.base.annotated,
		Run: d.base.run, Phase: d.phase, Triage: d.base.triage, Truncated: d.base.truncated,
	}
	for _, o := range d.overlays {
		o.apply(&s)
	}
	if s.Equal(d.snapshot) {
		return
	}
	d.snapshot = s
	if d.h.Change != nil {
		d.h.Change()
	}
	if d.OnSnapshot != nil {
		d.OnSnapshot(d.snapshot)
	}
}

// store replaces the daemon's case with c unless a newer one is there.
func (d *DaemonSource) store(c Case) {
	i := slices.IndexFunc(d.base.cases, func(b Case) bool { return b.ID == c.ID })
	if i < 0 || c.Version < d.base.cases[i].Version {
		return
	}
	d.base.cases[i] = c
}

// baseCase is the daemon's case with id.
func (d *DaemonSource) baseCase(id CaseID) (Case, bool) {
	for _, c := range d.base.cases {
		if c.ID == id {
			return c, true
		}
	}
	return Case{}, false
}

// touch marks id's conversation as the most recently used and forgets the
// least recently used beyond ConversationsKept.
func (d *DaemonSource) touch(id CaseID) {
	d.messageOrder = slices.DeleteFunc(d.messageOrder, func(o CaseID) bool { return o == id })
	d.messageOrder = append(d.messageOrder, id)
	for len(d.messageOrder) > ConversationsKept {
		delete(d.messages, d.messageOrder[0])
		d.messageOrder = d.messageOrder[1:]
	}
}

// LoadMessages implements DataSource.
func (d *DaemonSource) LoadMessages(id CaseID) {
	if d.stopped {
		return
	}
	c, ok := d.baseCase(id)
	if !ok {
		return
	}
	if m, ok := d.messages[id]; ok && m.version == c.Version {
		d.touch(id)
		return
	}
	if v, ok := d.loading[id]; ok && v == c.Version {
		return
	}
	d.loading[id] = c.Version
	delete(d.failed, id)
	d.publish()
	version := c.Version
	r := new(api.BoardGetResult)
	d.call(api.MethodBoardGet, api.BoardGetParams{CaseID: id}, r, func(err error) {
		if d.stopped {
			return
		}
		if v, ok := d.loading[id]; ok && v == version {
			delete(d.loading, id)
		}
		if err != nil {
			d.log.Info("board.get failed", "err", err)
			d.failed[id] = version
			d.publish()
			return
		}
		// A case that left the board meanwhile keeps nothing.
		if _, ok := d.baseCase(id); !ok {
			return
		}
		got := convertCase(r.Case)
		list := make([]CaseMessage, 0, len(r.Messages))
		for _, m := range r.Messages {
			list = append(list, convertMessage(m))
		}
		d.messages[id] = conversation{version: got.Version, list: list}
		d.touch(id)
		d.store(got)
		d.publish()
		// The case changed meanwhile: its conversation may have too.
		if now, ok := d.baseCase(id); ok && now.Version != got.Version {
			d.LoadMessages(id)
		}
	})
}

// The user's decisions.

// SetState implements DataSource.
func (d *DaemonSource) SetState(id CaseID, state *State) {
	var wire *api.BoardState
	var local *State
	if state != nil {
		wire = api.Ptr(state.API())
		local = statePtr(*state)
	}
	writeCase(d, api.MethodBoardSetState, api.BoardSetStateParams{CaseID: id, State: wire}, ActionMove, id,
		func(c *Case) { c.UserState = local },
		func(r *api.BoardSetStateResult) api.BoardCase { return r.Case })
}

// SetDone implements DataSource.
func (d *DaemonSource) SetDone(id CaseID, done bool) {
	action := ActionReopen
	if done {
		action = ActionDone
	}
	writeCase(d, api.MethodBoardSetDone, api.BoardSetDoneParams{CaseID: id, Done: done}, action, id,
		func(c *Case) { c.SetDone(done) },
		func(r *api.BoardSetDoneResult) api.BoardCase { return r.Case })
}

// Remind implements DataSource.
func (d *DaemonSource) Remind(id CaseID, until *time.Time) {
	writeCase(d, api.MethodBoardRemind, api.BoardRemindParams{CaseID: id, Until: until}, ActionRemind, id,
		func(c *Case) { remindCase(c, until) },
		func(r *api.BoardRemindResult) api.BoardCase { return r.Case })
}

// Archive implements DataSource.
func (d *DaemonSource) Archive(id CaseID) {
	writeCase(d, api.MethodBoardArchive, api.BoardArchiveParams{CaseID: id}, ActionArchive, id,
		func(c *Case) { c.Visibility = Visibility{Kind: VisibleDone} },
		func(r *api.BoardArchiveResult) api.BoardCase {
			if d.h.Notice != nil {
				d.h.Notice(Archived(r.Archived, r.NoArchive, d.tr))
			}
			return r.Case
		})
}

// DiscardDraft implements DataSource.
func (d *DaemonSource) DiscardDraft(id CaseID) {
	writeCase(d, api.MethodBoardDiscardDraft, api.BoardDiscardDraftParams{CaseID: id}, ActionDiscardDraft, id,
		func(c *Case) { c.Draft = nil },
		func(r *api.BoardDiscardDraftResult) api.BoardCase { return r.Case })
}

// DiscardStoredDraft implements DataSource: board.discardDraft while the
// case links draft (optimistic, taken back when refused), else
// draft.delete of that draft alone. The caller reports a failure (no
// Handlers.Error); after Stop done gets context.Canceled at once.
func (d *DaemonSource) DiscardStoredDraft(id CaseID, draft api.DraftID, account api.AccountID, done func(error)) {
	finish := func(err error) {
		if done != nil {
			done(err)
		}
	}
	if d.stopped {
		finish(context.Canceled)
		return
	}
	d.writesInFlight++
	if c, ok := d.snapshot.Case(id); !ok || c.Draft == nil || c.Draft.ID != draft {
		params := api.DraftDeleteParams{AccountID: account, DraftID: draft}
		d.call(api.MethodDraftDelete, params, new(api.DraftDeleteResult), func(err error) {
			d.writesInFlight--
			finish(err)
		})
		return
	}
	token := d.lay(func(s *Snapshot) {
		if i := caseIndex(s.Cases, id); i >= 0 {
			s.Cases[i].Draft = nil
		}
	})
	r := new(api.BoardDiscardDraftResult)
	d.call(api.MethodBoardDiscardDraft, api.BoardDiscardDraftParams{CaseID: id}, r, func(err error) {
		d.writesInFlight--
		if err != nil {
			d.log.Info("board.discardDraft failed", "err", err)
			d.lift(token)
			finish(err)
			return
		}
		d.store(convertCase(r.Case))
		d.lift(token)
		finish(nil)
	})
}

// Unflag implements DataSource: board.unflag. Nothing changes
// optimistically (the rules decide what the case becomes); the board is
// listed again once the stars are gone, so the case moves without waiting
// for the notification.
func (d *DaemonSource) Unflag(id CaseID) {
	writeCase(d, api.MethodBoardUnflag, api.BoardUnflagParams{CaseID: id}, ActionUnflag, id,
		func(*Case) {},
		func(r *api.BoardUnflagResult) api.BoardCase {
			d.Refresh()
			return r.Case
		})
}

// SetCommitmentDone implements DataSource.
func (d *DaemonSource) SetCommitmentDone(id api.BoardCommitmentID, done bool) {
	if d.stopped || !slices.ContainsFunc(d.snapshot.Commitments, func(k Commitment) bool { return k.ID == id }) {
		return
	}
	state := CommitmentOpen
	if done {
		state = CommitmentDone
	}
	token := d.lay(func(s *Snapshot) {
		if i := slices.IndexFunc(s.Commitments, func(k Commitment) bool { return k.ID == id }); i >= 0 {
			ks := slices.Clone(s.Commitments)
			ks[i].State = state
			s.Commitments = ks
		}
	})
	d.writesInFlight++
	r := new(api.BoardSetCommitmentResult)
	d.call(api.MethodBoardSetCommitment, api.BoardSetCommitmentParams{CommitmentID: id, Done: done}, r, func(err error) {
		d.writesInFlight--
		if err != nil {
			d.log.Info("board.setCommitment failed", "err", err)
			d.lift(token)
			d.toast(FailedText(ActionCommitment, err, d.tr))
			return
		}
		k := convertCommitment(r.Commitment)
		if i := slices.IndexFunc(d.base.commitments, func(b Commitment) bool { return b.ID == k.ID }); i >= 0 {
			ks := slices.Clone(d.base.commitments)
			ks[i] = k
			d.base.commitments = ks
		}
		// A list on its way, or asked for before now, is older.
		d.commitmentPins[k.ID] = commitmentPin{commitment: k, after: d.listGeneration}
		d.lift(token)
	})
}

// writeCase lays change over case id at once, calls method, and then puts
// the case the daemon answered (done) in place of the change, or takes the
// change back and reports the error. An unknown case is left alone.
func writeCase[R any](d *DaemonSource, method string, params any, action Action, id CaseID,
	change func(*Case), done func(*R) api.BoardCase,
) {
	if d.stopped {
		return
	}
	if _, ok := d.snapshot.Case(id); !ok {
		return
	}
	token := d.lay(func(s *Snapshot) {
		if i := caseIndex(s.Cases, id); i >= 0 {
			change(&s.Cases[i])
		}
	})
	d.writesInFlight++
	r := new(R)
	d.call(method, params, r, func(err error) {
		d.writesInFlight--
		if err != nil {
			d.log.Info(method+" failed", "err", err)
			d.lift(token)
			d.toast(FailedText(action, err, d.tr))
			return
		}
		d.store(convertCase(done(r)))
		d.lift(token)
	})
}

func (d *DaemonSource) toast(text string) {
	if d.h.Error != nil {
		d.h.Error(text)
	}
}

// lay adds an overlay and reports the snapshot with it.
func (d *DaemonSource) lay(apply func(*Snapshot)) int {
	d.nextToken++
	d.overlays = append(d.overlays, overlay{token: d.nextToken, apply: apply})
	d.publish()
	return d.nextToken
}

// lift removes an overlay and reports what is left.
func (d *DaemonSource) lift(token int) {
	d.overlays = slices.DeleteFunc(d.overlays, func(o overlay) bool { return o.token == token })
	d.publish()
}

func caseIndex(cases []Case, id CaseID) int {
	return slices.IndexFunc(cases, func(c Case) bool { return c.ID == id })
}

// From the wire. Times are taken in UTC, so that two lists of the same
// data are equal snapshots.

func convertCase(w api.BoardCase) Case {
	var vis Visibility
	switch w.Visibility {
	case api.BoardDone:
		vis = Visibility{Kind: VisibleDone}
		if w.DoneAt != nil {
			vis.At = w.DoneAt.UTC()
		}
	case api.BoardSnoozed:
		if w.RemindAt != nil {
			vis = Visibility{Kind: VisibleSnoozed, At: w.RemindAt.UTC()}
		}
	}
	var reply *ReplyTarget
	if w.ReplyMessageID != "" {
		reply = &ReplyTarget{Message: w.ReplyMessageID, Folder: w.ReplyFolderID}
	}
	var issue *IssueInfo
	if w.Issue != nil {
		issue = &IssueInfo{Key: w.Issue.Key, Status: w.Issue.Status, Style: jira.StyleOf(w.Issue.StatusCategory)}
	}
	// A state this client does not know reads as for reading.
	rule, _ := StateFromAPI(w.RuleState)
	var user *State
	if w.UserState != nil {
		if s, ok := StateFromAPI(*w.UserState); ok {
			user = statePtr(s)
		}
	}
	var draft *DraftLink
	if w.Draft != nil {
		draft = &DraftLink{ID: w.Draft.DraftID, Text: w.Draft.Text}
	}
	return Case{
		ID: w.ID, Account: w.AccountID, Thread: w.ThreadID, Person: displayName(w.Person), Date: w.Date.UTC(),
		Subject: w.Subject, Snippet: w.Snippet, Unread: w.Unread, HasAttachments: w.HasAttachments,
		MessageCount: w.MessageCount, Issue: issue, RuleState: rule, RuleReason: w.RuleReason,
		Annotation: convertAnnotation(w.Annotation), UserState: user, Visibility: vis, Reply: reply,
		LatestMessage: w.LatestMessageID, CanArchive: w.CanArchive, Draft: draft, Version: w.Version,
	}
}

func convertAnnotation(a *api.BoardAnnotation) *Annotation {
	if a == nil {
		return nil
	}
	out := &Annotation{
		Title: a.Title, Summary: a.Summary, Why: a.Why, Tasks: a.Tasks, Source: a.Source, At: a.At.UTC(),
		Stale: a.Stale,
	}
	if a.State != nil {
		if s, ok := StateFromAPI(*a.State); ok {
			out.State = statePtr(s)
		}
	}
	if a.Due != nil {
		due := a.Due.At.UTC()
		out.Due = &due
		out.DueQuote = a.Due.Quote
		out.DueMessage = a.Due.MessageID
	}
	return out
}

func convertCommitment(k api.BoardCommitment) Commitment {
	// Closed, or a state this client does not know: not shown.
	state := CommitmentClosed
	switch k.State {
	case api.CommitmentOpen:
		state = CommitmentOpen
	case api.CommitmentDone:
		state = CommitmentDone
	}
	var due *time.Time
	if k.Due != nil {
		t := k.Due.UTC()
		due = &t
	}
	return Commitment{
		ID: k.ID, CaseID: k.CaseID, Text: k.Text, Quote: k.Quote, Due: due, Message: k.MessageID, State: state,
	}
}

func convertMessage(m api.BoardMessage) CaseMessage {
	return CaseMessage{
		ID: m.ID, Folder: m.FolderID, From: displayName(m.From), Date: m.Date.UTC(), Text: m.Text, Mine: m.Mine,
		Trimmed: m.Trimmed,
	}
}

func convertRun(r *api.BoardRun) *Run {
	if r == nil {
		return nil
	}
	date := r.At
	if r.EndedAt != nil {
		date = *r.EndedAt
	}
	return &Run{
		Model: r.Source, Date: date.UTC(), Annotated: r.Annotated, Running: r.EndedAt == nil, Error: string(r.Error),
		Trigger: string(r.Trigger), Started: r.At.UTC(),
	}
}

// displayName is the short form of an address: the name if the daemon
// parsed one, otherwise the bare address (widget.DisplayName). Hostile
// text; the view model cleans it.
func displayName(a api.Address) string {
	if name := strings.TrimSpace(a.Name); name != "" {
		return name
	}
	return strings.TrimSpace(a.Address)
}

// accountLabel is an account's name as the sidebar shows it: its name; for
// a Jira account without one the site's host; else its address (the
// window's accountLabel).
func accountLabel(a api.Account) string {
	if jira.IsJira(a.Config) {
		return jira.AccountLabel(a.Config)
	}
	if name := strings.TrimSpace(a.Config.Name); name != "" {
		return name
	}
	return strings.TrimSpace(a.Config.Email)
}

// accountBadge is the capsule of an account's kind (the window's
// accountHeaderBadge): "JIRA", the provider a mail account signs in with
// ("GOOGLE", "M365"), else "IMAP". Brand and protocol names, never
// translated.
func accountBadge(a api.Account) string {
	if jira.IsJira(a.Config) {
		return jira.KindBadge
	}
	switch signin.Provider(a.Config) {
	case signin.ProviderGoogle:
		return googleBadge
	case signin.ProviderMicrosoft365:
		return microsoftBadge
	}
	return imapBadge
}

// The capsules of mail accounts (accountBadge).
const (
	googleBadge    = "GOOGLE"
	microsoftBadge = "M365"
	imapBadge      = "IMAP"
)
