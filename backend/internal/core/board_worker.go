// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/schotek/malachi/backend/internal/board"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The board's recomputation (docs/api.md §4.13): the store's triggers
// mark threads dirty as messages are stored, moved, flagged, deleted or
// merged; one worker drains the dirty set in small batches (each one write
// transaction within the store's budgets, with a pause after it so that the
// syncers' writes keep flowing), judging each thread with the rules
// (board_adapter.go). It wakes when the notifier sees new mail, changed
// messages or a sync pass ending, when a board method changed something,
// on a ticker, and when the earliest remind comes due. What changed is told
// to the clients by notify.boardChanged, at most once per boardNotifyEvery,
// naming the accounts (absent: any).

// The pace and the bounds; variables so that tests can change them.
var (
	boardBatch       = 50
	boardPause       = 20 * time.Millisecond
	boardTick        = 30 * time.Second
	boardNotifyEvery = time.Second
	// boardIdentityTTL is how long an account's own addresses (the
	// senders of its sent folders) and the user's known correspondents are
	// used before they are read again.
	boardIdentityTTL = time.Hour
	// boardOwnAddresses is how many senders of the sent folders count as
	// the user's.
	boardOwnAddresses = 10
	// boardRunOpenLimit: a run still open after this is ended as failed.
	boardRunOpenLimit = 2 * time.Hour
	// boardRunKeep: runs are kept this long (docs/api.md §4.13).
	boardRunKeep = 90 * 24 * time.Hour
	// boardDoneKeep: a done case stays listed (and stored) this long.
	boardDoneKeep = 30 * 24 * time.Hour
)

// metaBoardRoles records the folder roles of every account the hourly
// upkeep last saw (JSON: account id → fingerprint), so that a change the
// syncer made while the daemon was not running is noticed too.
const metaBoardRoles = "board.roles"

// boardState is the board's part of the backend.
type boardState struct {
	wake chan struct{} // capacity 1

	mu sync.Mutex
	// What notify.boardChanged has not told yet: the accounts, or any
	// (all).
	pending map[string]bool
	all     bool
	timer   *time.Timer
	sent    time.Time
	stopped bool
	// ctx is the worker's (StartSync), for work a method starts in the
	// background (the backfill after a change of the preferences); nil
	// before. bg counts that work; the worker waits for it when it stops.
	ctx context.Context
	bg  sync.WaitGroup

	// The accounts' own addresses and identities, read at; self: the
	// addresses of every account last used (selfSet once recorded).
	idMu    sync.Mutex
	ids     map[string]boardIdentityEntry
	self    []string
	selfSet bool

	// The user's known correspondents across the enabled mail accounts
	// (knownKey: their ids), read at knownAt; knownGen counts the reads so
	// that the identities built over an older set are built again.
	knownMu  sync.Mutex
	known    []string
	knownKey string
	knownAt  time.Time
	knownGen int

	// The own texts derived from the HTML of the user's messages
	// (board_owntext.go).
	own boardOwnCache

	// ready: every thread was evaluated since the board was enabled or its
	// rules changed (BoardListResult.Ready).
	ready atomic.Bool
	// backfillMu runs one pass of backfillBoard at a time; backfillGen
	// counts the restarts (restartBoardBackfill), and backfillMetaMu makes
	// a pass's record of its cursor and a restart's reset of it exclusive,
	// so that a pass never overwrites a restart (board_backfill.go).
	backfillMu     sync.Mutex
	backfillMetaMu sync.Mutex
	backfillGen    atomic.Int64

	// now is the clock; tests substitute one.
	now func() time.Time
}

type boardIdentityEntry struct {
	addresses []string
	at        time.Time
	// identity is built over addresses, self (every account's) and the
	// known correspondents of knownGen (hasIdentity).
	self        []string
	identity    board.Identity
	knownGen    int
	hasIdentity bool
}

func (bs *boardState) init() {
	bs.wake = make(chan struct{}, 1)
	bs.pending = map[string]bool{}
	bs.ids = map[string]boardIdentityEntry{}
	bs.own.init()
	bs.now = time.Now
}

// boardNow is the board's clock, in UTC.
func (b *Backend) boardNow() time.Time { return b.board.now().UTC() }

// wakeBoard asks the worker to look at the dirty set; it never blocks.
func (b *Backend) wakeBoard() {
	select {
	case b.board.wake <- struct{}{}:
	default:
	}
}

// startBoard starts the worker; done is closed when it has stopped, with
// the background work it started. It first ends the triage runs a client
// left open before this start.
func (b *Backend) startBoard(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	bs := &b.board
	bs.mu.Lock()
	bs.ctx = ctx
	bs.stopped = false
	bs.mu.Unlock()
	now := b.boardNow()
	if n, err := b.store.CloseOpenBoardRuns(ctx, now, now); err != nil {
		b.log.Warn("board: end the runs left open", "err", err)
	} else if n > 0 {
		b.notifyBoard(true)
	}
	if v, _, err := b.store.GetMeta(ctx, metaBoardRules); err == nil {
		bs.ready.Store(v == boardRulesDone())
	}
	go func() {
		defer close(done)
		tick := time.NewTicker(boardTick)
		defer tick.Stop()
		for ctx.Err() == nil {
			if err := b.drainBoard(ctx); err != nil && !isCancelled(err) {
				b.log.Warn("board: evaluate threads", "err", err)
			}
			var remind <-chan time.Time
			var rt *time.Timer
			if at, ok, err := b.store.NextBoardRemind(ctx); err == nil && ok {
				rt = time.NewTimer(max(at.Sub(b.boardNow()), 0) + 10*time.Millisecond)
				remind = rt.C
			}
			select {
			case <-ctx.Done():
			case <-bs.wake:
			case <-tick.C:
			case <-remind:
				b.clearDueReminds(ctx)
			}
			if rt != nil {
				rt.Stop()
			}
		}
		b.stopBoard()
		bs.bg.Wait()
	}()
	return done
}

// stopBoard drops what is not told yet; nothing is told after it, and no
// background work starts.
func (b *Backend) stopBoard() {
	bs := &b.board
	bs.mu.Lock()
	defer bs.mu.Unlock()
	bs.stopped = true
	if bs.timer != nil {
		bs.timer.Stop()
		bs.timer = nil
	}
	bs.pending, bs.all = map[string]bool{}, false
}

// goBoard runs f in the background of the worker (joined when it stops);
// false when the worker is not running.
func (b *Backend) goBoard(f func(ctx context.Context)) bool {
	bs := &b.board
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.ctx == nil || bs.ctx.Err() != nil || bs.stopped {
		return false
	}
	ctx := bs.ctx
	bs.bg.Add(1)
	go func() {
		defer bs.bg.Done()
		f(ctx)
	}()
	return true
}

// drainBoard evaluates the dirty threads until none is left (or the
// board is disabled, or ctx ends), batch by batch, and tells the clients
// what changed. It is what the worker runs; tests and the dry run call it
// directly.
func (b *Backend) drainBoard(ctx context.Context) error {
	prefs, err := b.boardPrefs(ctx)
	if err != nil {
		return err
	}
	if !prefs.Enabled {
		return nil
	}
	for {
		// Read for every batch: an account added meanwhile must not be
		// judged as gone.
		accounts, err := b.boardAccounts(ctx)
		if err != nil {
			return err
		}
		now := b.boardNow()
		wants := &boardWants{}
		d, err := b.store.DrainBoard(ctx, store.BoardDrainOptions{
			Limit: boardBatch, Now: now, Since: boardSinceOf(now, prefs.Windows), Assistant: prefs.Assistant,
		}, b.boardDecider(accounts, now, wants))
		if err != nil {
			return err
		}
		for _, f := range d.Failed {
			// Ids only: the error is the store's (a decode, a read), never
			// mail text.
			b.log.Warn("board: a thread could not be evaluated; it is evaluated again when it changes",
				"account", f.AccountID, "thread", f.ThreadID, "err", f.Err)
		}
		if len(d.Accounts) > 0 {
			b.notifyBoard(false, d.Accounts...)
		}
		more := d.More
		if !wants.empty() {
			// Threads the rules need the own text of HTML messages for:
			// derived now, outside the transaction, and judged again.
			if err := b.loadBoardOwnTexts(ctx, wants); err != nil {
				return err
			}
			more = true
		}
		if !more {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(boardPause):
		}
	}
	if !b.board.ready.Load() {
		if v, _, err := b.store.GetMeta(ctx, metaBoardRules); err == nil && v == boardRulesDone() {
			if n, err := b.store.CountBoardDirty(ctx); err == nil && n == 0 {
				b.board.ready.Store(true)
				b.notifyBoard(true)
			}
		}
	}
	return nil
}

// boardSinceOf is when the longest window of the preferences starts.
func boardSinceOf(now time.Time, w api.BoardWindows) time.Time {
	return now.AddDate(0, 0, -maxBoardWindow(w))
}

// boardSince is when the longest window of the stored preferences starts
// (the defaults when they cannot be read).
func (b *Backend) boardSince(ctx context.Context) time.Time {
	prefs, err := b.boardPrefs(ctx)
	if err != nil {
		prefs = api.DefaultBoardPreferences()
	}
	return boardSinceOf(b.boardNow(), prefs.Windows)
}

// boardAccounts reads what the decider needs of every account.
func (b *Backend) boardAccounts(ctx context.Context) (map[string]*boardAccount, error) {
	list, err := b.store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	known, gen, changed, err := b.boardKnown(ctx, list)
	if err != nil {
		return nil, err
	}
	if len(changed) > 0 {
		// Someone became known (or stopped being): the cases with mail
		// from them are judged again — an unknown sender's mail is always
		// a case, so marking those cases' threads reaches every verdict
		// that changes; no other thread's does.
		var mail []string
		for _, a := range list {
			if a.Enabled && !isIssueAccount(a) {
				mail = append(mail, a.ID)
			}
		}
		if err := b.store.MarkBoardCasesFrom(ctx, mail, changed); err != nil {
			return nil, err
		}
	}
	// The user's addresses on every account, for notes to self.
	own := make([][]string, len(list))
	var self []string
	seen := map[string]bool{}
	for i, a := range list {
		if own[i], err = b.boardOwn(ctx, a); err != nil {
			return nil, err
		}
		for _, s := range own[i] {
			if !seen[s] && len(self) < board.MaxSelfAddresses {
				seen[s] = true
				self = append(self, s)
			}
		}
	}
	if b.boardSelfChanged(self) {
		// Another address of the user's (an account added or removed):
		// a note to self may be one no longer, or newly, and a thread that
		// is no case may become one, so the stored mail of the longest
		// window is judged again (rare: the accounts changed).
		since := b.boardSince(ctx)
		for _, a := range list {
			if a.Enabled && !isIssueAccount(a) {
				if err := b.store.MarkBoardAccountDirty(ctx, a.ID, since); err != nil {
					return nil, err
				}
			}
		}
	}
	out := make(map[string]*boardAccount, len(list))
	for i, a := range list {
		ba := &boardAccount{jira: isIssueAccount(a)}
		ba.addresses = own[i]
		ba.identity = b.boardIdentity(a, own[i], self, known, gen)
		if ba.jira && a.Config.Jira != nil {
			ba.closed = map[string]bool{}
			for _, st := range a.Config.Jira.ClosedStatuses {
				ba.closed[st.ID] = true
			}
		}
		if can(a.Config, api.CapabilityMove) {
			f, err := b.store.FolderByRole(ctx, a.ID, api.RoleArchive)
			switch {
			case err == nil:
				ba.canArchive = f.Selectable
			case !errors.Is(err, store.ErrNotFound):
				return nil, err
			}
		}
		out[a.ID] = ba
	}
	return out, nil
}

// boardKnown returns the user's known correspondents (the addresses the
// user has written to in any of the enabled mail accounts, most recent
// first, at most board.MaxKnownCorrespondents) and the generation of that
// read, read again after boardIdentityTTL, when the set of enabled mail
// accounts changed, or after boardAccountChanged; changed lists the
// addresses a read after the first added or dropped (none: the same set).
func (b *Backend) boardKnown(ctx context.Context, list []store.Account) (known []string, gen int, changed []string, err error) {
	var ids []string
	for _, a := range list {
		if a.Enabled && !isIssueAccount(a) {
			ids = append(ids, a.ID)
		}
	}
	sort.Strings(ids)
	key := strings.Join(ids, ",")
	bs := &b.board
	now := b.boardNow()
	bs.knownMu.Lock()
	defer bs.knownMu.Unlock()
	if !bs.knownAt.IsZero() && bs.knownKey == key && now.Sub(bs.knownAt) < boardIdentityTTL {
		return bs.known, bs.knownGen, nil, nil
	}
	known, err = b.store.BoardKnownCorrespondents(ctx, ids, board.MaxKnownCorrespondents)
	if err != nil {
		return nil, 0, nil, err
	}
	if bs.knownGen > 0 {
		changed = addressesDiff(bs.known, known)
	}
	bs.known, bs.knownKey, bs.knownAt = known, key, now
	bs.knownGen++
	return bs.known, bs.knownGen, changed, nil
}

// addressesDiff lists the addresses in one of two lists but not in the
// other, sorted; none when they hold the same addresses.
func addressesDiff(a, b []string) []string {
	in := make(map[string]int, len(a)+len(b))
	for _, x := range a {
		in[x] |= 1
	}
	for _, x := range b {
		in[x] |= 2
	}
	var out []string
	for x, m := range in {
		if m != 3 {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// sameAddresses reports whether two address lists hold the same
// addresses, in whatever order.
func sameAddresses(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		if !set[x] {
			return false
		}
	}
	return true
}

// boardIdentity returns the identity for the rules of an account over its
// addresses (boardOwn), the addresses of every account of the user (self)
// and the known correspondents of generation gen (a jira account's
// identity takes the user's id per thread: none here).
func (b *Backend) boardIdentity(a store.Account, addresses, self, known []string, gen int) board.Identity {
	if isIssueAccount(a) {
		return board.Identity{}
	}
	bs := &b.board
	bs.idMu.Lock()
	defer bs.idMu.Unlock()
	e := bs.ids[a.ID]
	if e.hasIdentity && e.knownGen == gen && slices.Equal(e.addresses, addresses) && slices.Equal(e.self, self) {
		return e.identity
	}
	e.addresses, e.self = addresses, self
	e.identity, e.knownGen, e.hasIdentity = board.NewIdentity("", addresses, known).WithSelf(self), gen, true
	bs.ids[a.ID] = e
	return e.identity
}

// boardSelfChanged records the addresses of every account of the user and
// reports whether they differ from the ones recorded before (false the
// first time).
func (b *Backend) boardSelfChanged(self []string) bool {
	bs := &b.board
	bs.idMu.Lock()
	defer bs.idMu.Unlock()
	changed := bs.selfSet && !sameAddresses(bs.self, self)
	bs.self, bs.selfSet = self, true
	return changed
}

// boardOwn returns the user's addresses on an account: its own and the
// senders of its sent folders, lower case, read again after
// boardIdentityTTL or a change of the account.
func (b *Backend) boardOwn(ctx context.Context, a store.Account) ([]string, error) {
	bs := &b.board
	now := b.boardNow()
	bs.idMu.Lock()
	e, ok := bs.ids[a.ID]
	bs.idMu.Unlock()
	if ok && e.addresses != nil && now.Sub(e.at) < boardIdentityTTL {
		return e.addresses, nil
	}
	seen := map[string]bool{}
	out := []string{}
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" && !seen[s] && len(out) < board.MaxIdentityAddresses {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(a.Email)
	add(a.Config.Email)
	if !isIssueAccount(a) {
		own, err := b.store.BoardOwnAddresses(ctx, a.ID, boardOwnAddresses)
		if err != nil {
			return nil, err
		}
		for _, s := range own {
			add(s)
		}
	}
	bs.idMu.Lock()
	bs.ids[a.ID] = boardIdentityEntry{addresses: out, at: now}
	bs.idMu.Unlock()
	return out, nil
}

// notifyBoard gathers notify.boardChanged for the accounts (all: any
// account) and sends it at most once per boardNotifyEvery, from a timer
// goroutine: never from the caller's.
func (b *Backend) notifyBoard(all bool, accounts ...string) {
	bs := &b.board
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.stopped {
		return
	}
	if all {
		bs.all = true
	}
	for _, a := range accounts {
		if a != "" {
			bs.pending[a] = true
		}
	}
	if bs.timer != nil || (!bs.all && len(bs.pending) == 0) {
		return
	}
	wait := max(boardNotifyEvery-time.Since(bs.sent), 0)
	bs.timer = time.AfterFunc(wait, b.flushBoard)
}

// flushBoard sends what notifyBoard gathered.
func (b *Backend) flushBoard() {
	bs := &b.board
	bs.mu.Lock()
	pending, all, stopped := bs.pending, bs.all, bs.stopped
	bs.pending, bs.all, bs.timer = map[string]bool{}, false, nil
	bs.sent = time.Now()
	bs.mu.Unlock()
	if stopped || (!all && len(pending) == 0) {
		return
	}
	var ev api.BoardChangedNotification
	if !all {
		ids := make([]string, 0, len(pending))
		for a := range pending {
			ids = append(ids, a)
		}
		sort.Strings(ids)
		for _, a := range ids {
			ev.AccountIDs = append(ev.AccountIDs, api.AccountID(a))
		}
	}
	if n, ok := b.getNotifier().(api.BoardNotifier); ok {
		n.BoardChanged(ev)
	}
}

// boardWritten is told that a board method changed a case of the account:
// the thread is evaluated again (what keeps a case may have changed) and
// the clients told.
func (b *Backend) boardWritten(ctx context.Context, c store.BoardCase) {
	if err := b.store.MarkBoardThreadsDirty(ctx, c.AccountID, []string{c.ThreadID}); err != nil {
		b.log.Warn("board: mark a thread", "err", err)
	}
	b.notifyBoard(false, c.AccountID)
	b.wakeBoard()
}

// clearDueReminds marks the reminds that came due (their cases live
// again, and kept until done or reminded again) and tells the clients.
func (b *Backend) clearDueReminds(ctx context.Context) {
	accounts, err := b.store.ClearDueBoardReminds(ctx, b.boardNow())
	if err != nil {
		b.log.Warn("board: end due reminds", "err", err)
		return
	}
	if len(accounts) > 0 {
		b.notifyBoard(false, accounts...)
	}
}

// boardAccountChanged is told that an account was changed, paused or
// resumed: its own addresses and the known correspondents are read again,
// and when what the rules read of it changed (its configuration: kind,
// closedStatuses and the like), its threads within the longest window are
// evaluated again.
func (b *Backend) boardAccountChanged(ctx context.Context, before, after store.Account) {
	b.board.idMu.Lock()
	delete(b.board.ids, after.ID)
	b.board.idMu.Unlock()
	b.board.knownMu.Lock()
	b.board.knownAt = time.Time{}
	b.board.knownMu.Unlock()
	if fmt.Sprint(boardConfigKey(before)) != fmt.Sprint(boardConfigKey(after)) {
		if err := b.store.MarkBoardAccountDirty(ctx, after.ID, b.boardSince(ctx)); err != nil {
			b.log.Warn("board: mark an account", "account", after.ID, "err", err)
		}
	}
	b.notifyBoard(false, after.ID)
	b.wakeBoard()
}

// boardConfigKey is what of an account's configuration the board reads.
func boardConfigKey(a store.Account) any {
	var closed []string
	if a.Config.Jira != nil {
		for _, st := range a.Config.Jira.ClosedStatuses {
			closed = append(closed, st.ID)
		}
	}
	sort.Strings(closed)
	return []any{a.Email, a.Config.Protocol(), closed}
}

// boardDraftTouched is told that a draft was saved, sent or deleted: a
// case linking it shows another draft now (the store's triggers raised its
// version), and the clients are told.
func (b *Backend) boardDraftTouched(ctx context.Context, accountID, draftID string) {
	linked, err := b.store.BoardDraftLinked(ctx, accountID, draftID)
	if err != nil {
		b.log.Warn("board: cases of a draft", "err", err)
		return
	}
	if linked {
		b.notifyBoard(false, accountID)
	}
}

// boardDraftsDropped is told the drafts a move, trash or delete of their
// copies deleted (the store's draft sync): when a case links one of them,
// it shows no draft now, and the clients are told once.
func (b *Backend) boardDraftsDropped(ctx context.Context, accountID string, draftIDs []string) {
	for _, id := range draftIDs {
		linked, err := b.store.BoardDraftLinked(ctx, accountID, id)
		if err != nil {
			b.log.Warn("board: cases of a draft", "err", err)
			return
		}
		if linked {
			b.notifyBoard(false, accountID)
			return
		}
	}
}

// boardCopyLinked reports whether m, a message of the Drafts folder, is
// the copy of a draft a case links.
func (b *Backend) boardCopyLinked(ctx context.Context, accountID string, m store.Message) bool {
	d, err := b.store.DraftForMessage(ctx, accountID, m)
	if err != nil {
		return false
	}
	linked, err := b.store.BoardDraftLinked(ctx, accountID, d.ID)
	return err == nil && linked
}

// boardUpkeep is the hourly housekeeping of the board (Maintain): it ends
// runs left open for long, deletes old runs, ends due reminds, deletes the
// server copies no draft holds any more, deletes the cases off the board
// for good (and those whose thread has had no member for a day), logs the
// suggested replies that lost their case, deletes the local drafts no case
// links, and evaluates again the accounts whose folder roles changed (the
// syncer changes them; the rules read them).
func (b *Backend) boardUpkeep(ctx context.Context) {
	now := b.boardNow()
	if n, err := b.store.CloseOpenBoardRuns(ctx, now.Add(-boardRunOpenLimit), now); err != nil {
		b.log.Warn("board: end the runs left open", "err", err)
	} else if n > 0 {
		b.notifyBoard(true)
	}
	if _, err := b.store.PruneBoardRuns(ctx, now.Add(-boardRunKeep)); err != nil {
		b.log.Warn("board: delete old runs", "err", err)
	}
	b.clearDueReminds(ctx)
	b.dedupBoardCommitments(ctx)
	if accounts, err := b.store.DropStrayDraftCopies(ctx, "", nil, now); err != nil {
		b.log.Warn("board: delete the Drafts folder copies no draft holds", "err", err)
	} else {
		for _, a := range accounts {
			b.log.Info("board: deleting the Drafts folder copy of a suggested reply", "account", a)
			b.triggerDrafts(a)
		}
	}
	enabled := b.boardPrune(ctx, now)
	b.logUnlinkedDrafts(ctx)
	if accounts, err := b.store.ReleaseEditedLocalDrafts(ctx); err != nil {
		b.log.Warn("board: release edited local drafts", "err", err)
	} else {
		for _, a := range accounts {
			b.log.Warn("board: an edited suggested reply had no case; it is an ordinary draft again", "account", a)
			b.scheduleDraftSync(a)
		}
	}
	b.sweepLocalDrafts(ctx, now)
	if enabled {
		b.boardRolesChanged(ctx)
	}
}

// metaBoardCommitmentsDedup records that the commitments recorded more
// than once before board.commit returned the existing one were merged.
const metaBoardCommitmentsDedup = "board.commitments.dedup"

// dedupBoardCommitments merges, once, the commitments recorded twice for
// the same message and quote (store.DedupBoardCommitments with the rule
// of board.commit) and tells the clients about the cases it changed.
func (b *Backend) dedupBoardCommitments(ctx context.Context) {
	if v, _, err := b.store.GetMeta(ctx, metaBoardCommitmentsDedup); err != nil {
		b.log.Warn("board: read the commitments' merge mark", "err", err)
		return
	} else if v == "1" {
		return
	}
	changed, err := b.store.DedupBoardCommitments(ctx, board.SameCommitment)
	if err != nil {
		b.log.Warn("board: merge the commitments recorded twice", "err", err)
		return
	}
	accounts := make([]string, 0, len(changed))
	for account, threads := range changed {
		if err := b.store.MarkBoardThreadsDirty(ctx, account, threads); err != nil {
			b.log.Warn("board: mark a thread", "err", err)
		}
		accounts = append(accounts, account)
	}
	if len(accounts) > 0 {
		b.log.Info("board: merged the commitments recorded twice", "accounts", len(accounts))
		b.notifyBoard(false, accounts...)
		b.wakeBoard()
	}
	if err := b.store.SetMeta(ctx, metaBoardCommitmentsDedup, "1"); err != nil {
		b.log.Warn("board: record the commitments' merge mark", "err", err)
	}
}

// boardPrune deletes the cases off the board for good, unless the board is
// off (the user's decisions are kept meanwhile); it says whether the board
// is on.
func (b *Backend) boardPrune(ctx context.Context, now time.Time) bool {
	prefs, err := b.boardPrefs(ctx)
	if err != nil {
		b.log.Warn("board: read the preferences", "err", err)
		return false
	}
	if !prefs.Enabled {
		return false
	}
	accounts, err := b.store.PruneBoardCases(ctx, store.BoardPrune{
		Before:     boardSinceOf(now, prefs.Windows),
		DoneBefore: now.Add(-boardDoneKeep),
		Now:        now,
		Assistant:  prefs.Assistant,
	})
	if err != nil {
		b.log.Warn("board: delete old cases", "err", err)
	} else if len(accounts) > 0 {
		b.notifyBoard(false, accounts...)
	}
	return true
}

// logUnlinkedDrafts logs the suggested replies that lost their case since
// the last upkeep (a thread merge, the prune) and wakes the uploads of
// those that became ordinary drafts.
func (b *Backend) logUnlinkedDrafts(ctx context.Context) {
	drafts, err := b.store.TakeBoardUnlinkedDrafts(ctx)
	if err != nil {
		b.log.Warn("board: suggested replies that lost their case", "err", err)
		return
	}
	released := map[string]bool{}
	for _, d := range drafts {
		switch d.Outcome {
		case store.ReleaseOrdinary:
			b.log.Info("board: an edited suggested reply lost its case; it is an ordinary draft now, uploaded to the Drafts folder",
				"account", d.AccountID, "draft", d.DraftID, "case", d.CaseID, "reason", d.Reason)
			released[d.AccountID] = true
		default:
			b.log.Info("board: an untouched suggested reply lost its case and was deleted",
				"account", d.AccountID, "draft", d.DraftID, "case", d.CaseID, "reason", d.Reason)
		}
	}
	for a := range released {
		b.scheduleDraftSync(a)
	}
}

// localDraftKeep is how long a local draft no case links is kept before
// sweepLocalDrafts deletes it: longer than a triage run may stay open
// (boardRunOpenLimit), so a draft whose annotation is still to come is
// never taken.
const localDraftKeep = 6 * time.Hour

// sweepLocalDrafts deletes the local drafts that no case links, that were
// never edited and that were not saved for localDraftKeep: a triage run's
// draft whose link was refused or never asked for, or a Suggest Reply
// whose link never came. A draft that lost its case was dealt with then
// (store.PruneBoardCases, the thread merge), and an edited draft is never
// taken. Nothing shows such a draft — it is not in the Drafts folder — so
// it would otherwise stay forever. They go as draft.delete deletes,
// attachment files included.
func (b *Backend) sweepLocalDrafts(ctx context.Context, now time.Time) {
	ds := &draftService{b}
	for range 10 { // at most 1000 an hour
		list, err := b.store.UnlinkedLocalDrafts(ctx, now.Add(-localDraftKeep), 100)
		if err != nil {
			b.log.Warn("board: local drafts no case links", "err", err)
			return
		}
		for _, d := range list {
			if _, err := ds.Delete(ctx, api.DraftDeleteParams{AccountID: api.AccountID(d[0]), DraftID: api.DraftID(d[1])}); err != nil {
				b.log.Warn("board: delete a local draft no case links", "account", d[0], "draft", d[1], "err", err)
				return
			}
			b.log.Info("board: deleted a suggested reply no case links", "account", d[0], "draft", d[1])
		}
		if len(list) < 100 {
			return
		}
	}
}

// boardRolesChanged marks dirty (within the longest window) the threads of
// the accounts whose folder roles changed since the last look, which is
// recorded in meta (metaBoardRoles) so that it survives a restart. An
// account seen for the first time is only recorded.
func (b *Backend) boardRolesChanged(ctx context.Context) {
	list, err := b.store.ListAccounts(ctx)
	if err != nil {
		b.log.Warn("board: list accounts", "err", err)
		return
	}
	seen := map[string]string{}
	if raw, found, err := b.store.GetMeta(ctx, metaBoardRoles); err != nil {
		b.log.Warn("board: read the folder roles", "err", err)
		return
	} else if found {
		if err := json.Unmarshal([]byte(raw), &seen); err != nil {
			seen = map[string]string{}
		}
	}
	next := make(map[string]string, len(list))
	changed := false
	for _, a := range list {
		folders, err := b.store.ListFolders(ctx, a.ID)
		if err != nil {
			b.log.Warn("board: list folders", "account", a.ID, "err", err)
			if old, ok := seen[a.ID]; ok {
				next[a.ID] = old
			}
			continue
		}
		roles := make([]string, 0, len(folders))
		for _, f := range folders {
			if f.Role != "" {
				roles = append(roles, f.ID+"="+string(f.Role))
			}
		}
		sort.Strings(roles)
		key := strings.Join(roles, ",")
		next[a.ID] = key
		old, ok := seen[a.ID]
		if ok && old == key {
			continue
		}
		changed = true
		if !ok {
			continue
		}
		if err := b.store.MarkBoardAccountDirty(ctx, a.ID, b.boardSince(ctx)); err != nil {
			b.log.Warn("board: mark an account", "account", a.ID, "err", err)
			next[a.ID] = old // try again at the next look
			continue
		}
		b.wakeBoard()
	}
	if !changed && len(next) == len(seen) {
		return
	}
	raw, err := json.Marshal(next)
	if err == nil {
		err = b.store.SetMeta(ctx, metaBoardRoles, string(raw))
	}
	if err != nil {
		b.log.Warn("board: record the folder roles", "err", err)
	}
}

// maxBoardWindow is the longest window of the preferences, in days.
func maxBoardWindow(w api.BoardWindows) int {
	n := 0
	for _, s := range api.BoardStates {
		n = max(n, board.WindowDays(s, w))
	}
	return n
}
