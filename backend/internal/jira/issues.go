// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// A pass (Syncer.cycle):
//
//  1. the queued local operations (flags → every copy of the item);
//  2. the user (/myself, once a day; meta issues.me.<account>) and the
//     site's spaces (every six hours; issue_spaces holds the selected ones
//     the site lists), then the folders from the configuration;
//  3. the changed issues: a space folder that has never been enumerated
//     for the account's window (a new account, a new space, a window that
//     grew, a full pass) gets its window enumerated ("updated >= -Nd") and
//     its open issues assigned to the user whatever their age (at most
//     500); the other spaces are searched for what changed since the last
//     pass started, lagMargin earlier ("updated >= -Nm": relative JQL
//     sidesteps the profile's time zone). Issues the store owes a refresh
//     (an interrupted pass, other rendering settings, queued triggers) are
//     fetched by id. Every issue that changed is materialised (below),
//     issueConcurrency at a time;
//  4. hourly, or on a full pass, the reconciliation (reconcile.go), and on
//     every pass the retention of the account's window;
//  5. the folder counts, and the space folders' cursors: last_sync_at =
//     the pass's start, delta_link = "window:<days>"; and, when the pass
//     changed stored rows in place (a body rebuilt with other settings or
//     after an edit, a comment re-attributed, an issue renamed), one
//     notify.messagesChanged naming their folders, so that clients drop
//     what they cached of them (announceChanged).
//
// Materialising an issue brings its rows in step with the site: the items
// that vanished and the copies in folders the issue left are deleted; each
// item gets a row in the space's folder and in every view the issue
// belongs to, a new row only where none is (UpsertMessages would reset
// the flags of an existing one: local flags are the only state), with the
// flags of an existing copy, else read when it is the user's own or an
// event, or — the first time a space is enumerated — when it was created
// more than three days before the pass, or — later — before the last
// pass (it is not news: the issue only came into scope). Envelopes follow
// renames and re-attributions, bodies are built (synth.go) and stored
// through ingest for new rows and for items that changed, and the item
// rows and then, last, the issue row are written: an interrupted
// materialisation leaves an issue whose synced_updated still differs,
// which the next pass completes. A new item that is not the user's own and
// not an event is announced (notify.newMessage, on its space folder's
// row), except when the space is enumerated for the first time.
//
// With OnlyMine an issue that is not the user's is not materialised
// (and a stored one forgotten), unless a notification mail in one of the
// user's mail accounts named it (store.IssueMailLinked, Issue.ViaMail):
// the site found it worth telling the user of.

const (
	// issueConcurrency is how many issues are materialised at once.
	issueConcurrency = 4
	// firstBackfillFresh: the first time a space is enumerated, an item
	// created within this before the pass is unread, older ones read.
	firstBackfillFresh = 72 * time.Hour
	// unreadMargin: later, an item the store did not have is news when it
	// was created after the last pass started, less this.
	unreadMargin = time.Hour
	// retentionGrace keeps an issue this much longer than the window says,
	// so that clock differences with the site cannot make one flap.
	retentionGrace = 24 * time.Hour
	// maxOpenAssigned caps the open issues assigned to the user kept
	// beyond the window.
	maxOpenAssigned = 500
	// bulkChunk is how many issues one bulk fetch names.
	bulkChunk = 50
	// maxHeaderScan bounds what is read of a stored message to find its
	// revision.
	maxHeaderScan = 64 << 10
)

// windowDays is the account's window: JiraConfig.OfflineDays, 0 meaning
// api.DefaultJiraOfflineDays, at most api.MaxJiraOfflineDays.
func windowDays(cfg api.JiraConfig) int {
	d := cfg.OfflineDays
	if d <= 0 {
		d = api.DefaultJiraOfflineDays
	}
	return min(d, api.MaxJiraOfflineDays)
}

// normKey is an issue key as the syncer queues it.
func normKey(k string) string { return strings.ToUpper(k) }

// pass is the state of one synchronisation.
type pass struct {
	start     time.Time
	cfg       api.JiraConfig
	days      int
	full      bool
	y         *synth
	renderKey string

	listed          map[string]Space        // the site's spaces by id
	spaceFolders    map[string]store.Folder // the selected spaces the site lists, by space id
	allSpaceFolders map[string]store.Folder // every selected space's folder
	views           map[api.VirtualFolder]store.Folder
	firstBackfill   map[string]bool      // space id → its folder was never enumerated
	lastSync        map[string]time.Time // space id → start of its last pass
	stamps          map[string]store.IssueStamp
	forced          map[string]bool // issue ids refreshed whatever their stamp

	mu        sync.Mutex
	claimed   map[string]bool // issue ids materialised (or forgotten) in this pass
	touched   map[string]bool // folder ids whose rows changed
	changed   map[string]bool // folder ids whose existing rows changed in place
	processed int
}

func (p *pass) claim(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.claimed[id] {
		return false
	}
	p.claimed[id] = true
	return true
}

func (p *pass) isClaimed(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.claimed[id]
}

func (p *pass) touch(folderIDs ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range folderIDs {
		p.touched[id] = true
	}
}

func (p *pass) takeTouched() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.touched))
	for id := range p.touched {
		out = append(out, id)
	}
	p.touched = map[string]bool{}
	sort.Strings(out)
	return out
}

// mark records folders whose existing rows changed in place (a body
// rebuilt, an envelope re-attributed, a thread retitled): what a client
// cached of them is stale, unlike a row that arrived or left.
func (p *pass) mark(folderIDs ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range folderIDs {
		p.changed[id] = true
	}
}

func (p *pass) takeChanged() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.changed))
	for id := range p.changed {
		out = append(out, id)
	}
	p.changed = map[string]bool{}
	sort.Strings(out)
	return out
}

// cycle runs one pass. keys are issue keys queued by TriggerIssue.
func (s *Syncer) cycle(ctx context.Context, req passRequest, keys []string) error {
	cfg := *s.account.Config.Jira
	p := &pass{
		start: s.now(), cfg: cfg, days: windowDays(cfg), full: req.full,
		forced: map[string]bool{}, claimed: map[string]bool{}, touched: map[string]bool{}, changed: map[string]bool{},
	}
	// Whatever the pass's outcome: what it rebuilt in place stays rebuilt,
	// and the clients must drop what they cached of it.
	defer s.announceChanged(p)
	s.setState(func(st *api.SyncState) { st.Status, st.FolderID, st.Progress = api.SyncSyncing, "", 0 })

	// Local first: flags reach every copy even while the site is away.
	if err := s.pushOps(ctx); err != nil {
		return err
	}
	if err := s.refreshMe(ctx, p); err != nil {
		return err
	}
	if err := s.refreshSpaces(ctx, p); err != nil {
		return err
	}
	rules := compileRules(cfg, s.log)
	y, err := newSynth(cfg, rules, s.deps.Remote, s.deps.Client, s.me, s.log)
	if err != nil {
		return err
	}
	p.y, p.renderKey = y, renderKey(rules, cfg.HideEvents)
	if err := s.upsertFolders(ctx, p); err != nil {
		return err
	}
	p.firstBackfill, p.lastSync = map[string]bool{}, map[string]time.Time{}
	for id, f := range p.spaceFolders {
		p.firstBackfill[id] = f.LastSyncAt.IsZero()
		p.lastSync[id] = f.LastSyncAt
	}
	if p.stamps, err = s.deps.Store.ListIssueStamps(ctx, s.account.ID); err != nil {
		return storageError(err)
	}
	if s.passes == 0 {
		// The configuration only changes with a new syncer: what it
		// changed is settled on its first pass.
		if err := s.settleConfig(ctx, p); err != nil {
			return err
		}
	}
	s.setProgress(5)
	s.log.Info("jira pass started", "full", p.full, "first", s.passes == 0,
		"spaces", len(p.spaceFolders), "windowDays", p.days, "storedIssues", len(p.stamps))

	// Queued keys: known issues by id, others by key.
	var byKey []string
	for _, k := range keys {
		is, err := s.deps.Store.IssueByKey(ctx, s.account.ID, k)
		switch {
		case err == nil:
			p.forced[is.IssueID] = true
		case errors.Is(err, store.ErrNotFound):
			byKey = append(byKey, k)
		default:
			return storageError(err)
		}
	}

	if err := s.enumerate(ctx, p); err != nil {
		return err
	}
	if err := s.refreshOwed(ctx, p, byKey); err != nil {
		return err
	}
	s.setProgress(90)
	if p.full || s.lastReconcile.IsZero() || p.start.Sub(s.lastReconcile) >= reconcileEvery {
		if err := s.reconcile(ctx, p); err != nil {
			return err
		}
		s.lastReconcile = p.start
	}
	if err := s.retain(ctx, p); err != nil {
		return err
	}
	if err := s.recount(ctx, p); err != nil {
		return err
	}
	for _, f := range p.spaceFolders {
		if err := s.deps.Store.SetFolderSyncState(ctx, f.ID, store.FolderSyncState{
			DeltaLink: windowPrefix + strconv.Itoa(p.days), LastSyncAt: p.start,
		}); err != nil && !errors.Is(err, store.ErrNotFound) {
			return storageError(err)
		}
	}
	for _, f := range p.views {
		if err := s.deps.Store.SetFolderSyncState(ctx, f.ID, store.FolderSyncState{LastSyncAt: p.start}); err != nil && !errors.Is(err, store.ErrNotFound) {
			return storageError(err)
		}
	}
	s.passes++
	p.mu.Lock()
	processed := p.processed
	p.mu.Unlock()
	s.log.Info("jira pass finished", "issuesRefreshed", processed,
		"took", s.now().Sub(p.start).Round(time.Millisecond))
	return nil
}

// meMeta is the meta record of the user (issues.me.<account>).
type meMeta struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	TimeZone string `json:"timeZone"`
}

// refreshMe asks the site who the token's user is, once a day, and keeps
// it in meta for core (IssueInfo.AssignedToMe).
func (s *Syncer) refreshMe(ctx context.Context, p *pass) error {
	if s.me.ID != "" && p.start.Sub(s.meAt) < meRefresh {
		return nil
	}
	u, err := s.deps.Remote.Myself(ctx)
	if err != nil {
		return err
	}
	s.me, s.meAt = u, p.start
	data, err := json.Marshal(meMeta{ID: u.ID, Name: u.Name, Email: u.Email, TimeZone: u.TimeZone})
	if err != nil {
		return fmt.Errorf("jira: encode the user: %w", err)
	}
	key := store.MetaIssueMePrefix + s.account.ID
	if old, ok, err := s.deps.Store.GetMeta(ctx, key); err != nil {
		return storageError(err)
	} else if ok && old == string(data) {
		return nil
	}
	if err := s.deps.Store.SetMeta(ctx, key, string(data)); err != nil {
		return storageError(err)
	}
	return nil
}

// refreshSpaces lists the site's spaces every spacesRefresh (and on a full
// pass) and stores the selected ones it lists (issue_spaces). A selected
// space the site does not list (taken away, or deleted) keeps its folder
// and its issues, unsynchronised.
func (s *Syncer) refreshSpaces(ctx context.Context, p *pass) error {
	if s.listed == nil || p.full || p.start.Sub(s.spacesAt) >= spacesRefresh {
		spaces, err := s.deps.Remote.Spaces(ctx)
		if err != nil {
			return err
		}
		listed := make(map[string]Space, len(spaces))
		for _, sp := range spaces {
			listed[sp.ID] = sp
		}
		var rows []store.IssueSpace
		for _, ref := range p.cfg.Spaces {
			sp, ok := listed[ref.ID]
			if !ok {
				s.log.Warn("selected jira space not listed by the site", "space", ref.ID)
				continue
			}
			if !containsSpace(rows, sp.ID) {
				rows = append(rows, store.IssueSpace{SpaceID: sp.ID, Key: sp.Key, Name: sp.Name, ServiceDesk: sp.ServiceDesk})
			}
		}
		if err := s.deps.Store.SetIssueSpaces(ctx, s.account.ID, rows); err != nil {
			return storageError(err)
		}
		s.listed, s.spacesAt = listed, p.start
	}
	p.listed = s.listed
	return nil
}

func containsSpace(rows []store.IssueSpace, id string) bool {
	for _, r := range rows {
		if r.SpaceID == id {
			return true
		}
	}
	return false
}

// settleConfig applies what a new configuration changed to the stored
// issues: those of spaces no longer selected are forgotten, and those
// whose views differ from what the enabled views make of them (a view
// switched on or off) are refreshed.
func (s *Syncer) settleConfig(ctx context.Context, p *pass) error {
	if len(p.stamps) == 0 {
		return nil
	}
	ids := make([]string, 0, len(p.stamps))
	for id := range p.stamps {
		ids = append(ids, id)
	}
	stored, err := s.deps.Store.IssuesByID(ctx, s.account.ID, ids)
	if err != nil {
		return storageError(err)
	}
	selected := map[string]bool{}
	for _, sp := range p.cfg.Spaces {
		selected[sp.ID] = true
	}
	var drop []string
	for id, is := range stored {
		if !selected[is.SpaceID] {
			drop = append(drop, id)
			continue
		}
		want := p.membership(is.AssigneeID, is.Watching, is.StatusID, is.StatusCategory)
		if !sameViews(want, is.Views) {
			p.forced[id] = true
		}
	}
	if len(drop) > 0 {
		s.log.Info("forgetting issues of spaces no longer selected", "count", len(drop))
		return s.forget(ctx, p, drop...)
	}
	return nil
}

func sameViews(a, b []api.VirtualFolder) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[api.VirtualFolder]bool{}
	for _, v := range a {
		set[v] = true
	}
	for _, v := range b {
		if !set[v] {
			return false
		}
	}
	return true
}

// membership lists the enabled views an issue belongs to, in viewOrder.
func (p *pass) membership(assigneeID string, watching bool, statusID string, cat api.IssueStatusCategory) []api.VirtualFolder {
	var out []api.VirtualFolder
	for _, v := range viewOrder {
		if _, on := p.views[v]; !on {
			continue
		}
		var in bool
		switch v {
		case api.VirtualAssignedToMe:
			in = assigneeID != "" && assigneeID == p.y.me.ID
		case api.VirtualWatching:
			in = watching
		case api.VirtualOpen:
			in = !p.closed(statusID, cat)
		}
		if in {
			out = append(out, v)
		}
	}
	return out
}

// closed reports a status the configuration counts as closed: one of
// ClosedStatuses, else any of the category done.
func (p *pass) closed(statusID string, cat api.IssueStatusCategory) bool {
	if len(p.cfg.ClosedStatuses) > 0 {
		for _, st := range p.cfg.ClosedStatuses {
			if st.ID == statusID {
				return true
			}
		}
		return false
	}
	return cat == api.StatusCategoryDone
}

// scope is the JQL of the issues of the spaces the account keeps: all of
// them, or with OnlyMine those the user reports, is assigned, watches or
// (cloud: Data Center may lack the function) changed within the window.
func (p *pass) scope(spaceIDs []string) string {
	s := p.spaces(spaceIDs)
	if !p.cfg.OnlyMine {
		return s
	}
	mine := "reporter = currentUser() OR assignee = currentUser() OR watcher = currentUser()"
	if p.cfg.Deployment == api.JiraCloud {
		mine += ` OR issuekey in updatedBy(currentUser(), "-` + strconv.Itoa(p.days) + `d")`
	}
	return s + " AND (" + mine + ")"
}

// spaces is the JQL of the spaces, ids sorted.
func (p *pass) spaces(spaceIDs []string) string {
	ids := append([]string(nil), spaceIDs...)
	sort.Slice(ids, func(i, j int) bool { return idLess(ids[i], ids[j]) })
	vals := make([]string, len(ids))
	for i, id := range ids {
		vals[i] = JQLValue(id)
	}
	return "project in (" + strings.Join(vals, ", ") + ")"
}

// enumerate searches the spaces for what changed (see the file comment)
// and materialises it page by page.
func (s *Syncer) enumerate(ctx context.Context, p *pass) error {
	var backfill, incremental []string
	oldest := time.Time{}
	for id, f := range p.spaceFolders {
		if p.full || f.LastSyncAt.IsZero() || windowOf(f) < p.days {
			backfill = append(backfill, id)
			continue
		}
		incremental = append(incremental, id)
		if oldest.IsZero() || f.LastSyncAt.Before(oldest) {
			oldest = f.LastSyncAt
		}
	}
	minutes := 0
	if len(incremental) > 0 {
		minutes = int((p.start.Sub(oldest)+lagMargin+time.Minute-1)/time.Minute) + 1
		minutes = max(minutes, int(lagMargin/time.Minute))
		if minutes > p.days*24*60 {
			// The account was away longer than its window.
			backfill, incremental = append(backfill, incremental...), nil
		}
	}
	opts := IssueOptions{Fields: FieldsAll, Rendered: true}
	if len(backfill) > 0 {
		if err := s.searchInto(ctx, p, SearchRequest{
			JQL:          p.scope(backfill) + ` AND updated >= "-` + strconv.Itoa(p.days) + `d" ORDER BY updated DESC`,
			IssueOptions: opts,
		}, 0); err != nil {
			return err
		}
		if err := s.searchInto(ctx, p, SearchRequest{
			JQL:          p.spaces(backfill) + " AND assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC",
			IssueOptions: opts,
		}, maxOpenAssigned); err != nil {
			return err
		}
	}
	if len(incremental) > 0 {
		var reconcile []string
		for id := range p.forced {
			reconcile = append(reconcile, id)
		}
		sort.Strings(reconcile)
		if err := s.searchInto(ctx, p, SearchRequest{
			JQL:          p.scope(incremental) + ` AND updated >= "-` + strconv.Itoa(minutes) + `m" ORDER BY updated DESC`,
			IssueOptions: opts, Reconcile: reconcile,
		}, 0); err != nil {
			return err
		}
	}
	return nil
}

// searchInto pages a search and materialises the issues that owe it.
func (s *Syncer) searchInto(ctx context.Context, p *pass, req SearchRequest, limit int) error {
	_, err := SearchAll(ctx, s.deps.Remote, req, limit, func(page []Issue) error {
		return s.materialiseBatch(ctx, p, p.owing(page))
	})
	if statusOf(err) == http.StatusBadRequest {
		// Most likely a space the site no longer knows: list the spaces
		// again before the next attempt.
		s.spacesAt = time.Time{}
	}
	return err
}

// owing keeps the issues a pass must materialise: new, changed since
// synchronised, rendered with other settings, or forced.
func (p *pass) owing(issues []Issue) []Issue {
	var out []Issue
	for _, is := range issues {
		if p.isClaimed(is.ID) {
			continue
		}
		st, known := p.stamps[is.ID]
		if !known || p.forced[is.ID] || !st.SyncedUpdated.Equal(is.Updated) || st.RenderKey != p.renderKey {
			out = append(out, is)
		}
	}
	return out
}

// refreshOwed fetches by id the stored issues that owe a refresh and were
// not in a search of this pass (an interrupted materialisation, other
// rendering settings, forced), and the issues queued by key the store does
// not know. A stored one the site no longer shows is forgotten.
func (s *Syncer) refreshOwed(ctx context.Context, p *pass, byKey []string) error {
	var ids []string
	for id, st := range p.stamps {
		if p.isClaimed(id) {
			continue
		}
		if p.forced[id] || !st.SyncedUpdated.Equal(st.Updated) || st.RenderKey != p.renderKey {
			ids = append(ids, id)
		}
	}
	for id := range p.forced {
		if _, known := p.stamps[id]; !known && !p.isClaimed(id) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return idLess(ids[i], ids[j]) })
	return s.fetchAndMaterialise(ctx, p, ids, byKey)
}

// fetchAndMaterialise fetches issues by id (and by key) and materialises
// them whatever their stamps say; a stored issue named by id that the site
// does not return is forgotten.
func (s *Syncer) fetchAndMaterialise(ctx context.Context, p *pass, ids, keys []string) error {
	refs := append(append([]string(nil), ids...), keys...)
	for len(refs) > 0 {
		chunk := refs[:min(bulkChunk, len(refs))]
		refs = refs[len(chunk):]
		got, err := s.deps.Remote.BulkIssues(ctx, chunk, IssueOptions{Fields: FieldsAll, Rendered: true})
		if err != nil {
			return err
		}
		found := map[string]bool{}
		for _, is := range got {
			found[is.ID] = true
		}
		var gone []string
		for _, ref := range chunk {
			if _, stored := p.stamps[ref]; stored && !found[ref] && !p.isClaimed(ref) {
				gone = append(gone, ref)
			}
		}
		if len(gone) > 0 {
			if err := s.forget(ctx, p, gone...); err != nil {
				return err
			}
		}
		if err := s.materialiseBatch(ctx, p, got); err != nil {
			return err
		}
	}
	return nil
}

// materialiseBatch materialises issues, issueConcurrency at a time, with
// their changelogs fetched for the batch in one go. An issue that is gone
// by the time its comments are asked for is forgotten, one the user may
// not read (403) skipped; any other failure ends the pass.
func (s *Syncer) materialiseBatch(ctx context.Context, p *pass, issues []Issue) error {
	var todo []Issue
	for _, is := range issues {
		if p.claim(is.ID) {
			todo = append(todo, is)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	var hist map[string][]History
	if !p.cfg.HideEvents {
		ids := make([]string, len(todo))
		for i, is := range todo {
			ids[i] = is.ID
		}
		var err error
		if hist, err = s.deps.Remote.Changelogs(ctx, ids); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	sem := make(chan struct{}, issueConcurrency)
	for _, is := range todo {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(is Issue) {
			defer wg.Done()
			defer func() { <-sem }()
			err := s.materialiseOne(ctx, p, is, hist[is.ID])
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
			}
		}(is)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.recount(ctx, p)
}

// materialiseOne fetches an issue's comments and materialises it.
func (s *Syncer) materialiseOne(ctx context.Context, p *pass, is Issue, hist []History) error {
	cl, err := s.deps.Remote.Comments(ctx, is.ID, 0)
	switch {
	case IsNotFound(err):
		return s.forget(ctx, p, is.ID)
	case IsForbidden(err):
		s.log.Warn("jira issue not readable, skipped", "issue", is.ID)
		return nil
	case err != nil:
		return err
	}
	if err := s.materialise(ctx, p, is, cl, hist); err != nil {
		return err
	}
	p.mu.Lock()
	p.processed++
	n := p.processed
	p.mu.Unlock()
	s.setProgress(5 + 85*n/(n+50))
	if n%25 == 0 {
		// A backfill of a whole window is minutes of silence otherwise.
		s.log.Info("jira pass progress", "issuesRefreshed", n, "took", s.now().Sub(p.start).Round(time.Second))
	}
	return nil
}

// forget deletes issues with every row, and tells the clients about mail
// of other accounts that showed again (hidden because of them).
func (s *Syncer) forget(ctx context.Context, p *pass, ids ...string) error {
	for _, id := range ids {
		p.claim(id)
	}
	shown, err := s.deps.Store.DeleteIssues(ctx, s.account.ID, ids)
	if err != nil {
		return storageError(err)
	}
	if s.deps.Notifier == nil {
		return nil
	}
	accounts := make([]string, 0, len(shown))
	for acc := range shown {
		accounts = append(accounts, acc)
	}
	sort.Strings(accounts)
	for _, acc := range accounts {
		folders := make([]api.FolderID, 0, len(shown[acc]))
		for _, f := range shown[acc] {
			folders = append(folders, api.FolderID(f))
		}
		s.deps.Notifier.MessagesChanged(api.MessagesChangedNotification{AccountID: api.AccountID(acc), FolderIDs: folders})
	}
	return nil
}

// recount recomputes the counts of the folders whose rows changed.
func (s *Syncer) recount(ctx context.Context, p *pass) error {
	for _, id := range p.takeTouched() {
		if _, _, err := s.deps.Store.RecountFolder(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
			return storageError(err)
		}
	}
	return nil
}

// announceChanged emits one notify.messagesChanged for the folders whose
// existing rows the pass changed in place (a comment re-attributed, edited
// or cleaned, an issue renamed), on the jira account itself: a client
// drops what it cached of them and lists again. A pass that only added or
// deleted rows emits nothing (clients reload on notify.syncState).
func (s *Syncer) announceChanged(p *pass) {
	changed := p.takeChanged()
	if len(changed) == 0 || s.deps.Notifier == nil {
		return
	}
	folders := make([]api.FolderID, len(changed))
	for i, id := range changed {
		folders[i] = api.FolderID(id)
	}
	s.deps.Notifier.MessagesChanged(api.MessagesChangedNotification{AccountID: api.AccountID(s.account.ID), FolderIDs: folders})
}

// materialise brings the stored rows of one issue in step with the site
// (see the file comment).
func (s *Syncer) materialise(ctx context.Context, p *pass, is Issue, cl CommentList, hist []History) error {
	acc := s.account.ID
	var prev *store.Issue
	switch stored, err := s.deps.Store.GetIssue(ctx, acc, is.ID); {
	case err == nil:
		prev = &stored
	case !errors.Is(err, store.ErrNotFound):
		return storageError(err)
	}
	if _, selected := p.allSpaceFolders[is.SpaceID]; !selected {
		// It moved to a space that is not selected.
		if prev != nil {
			return s.forget(ctx, p, is.ID)
		}
		return nil
	}
	spaceFolder, ok := p.spaceFolders[is.SpaceID]
	if !ok {
		// A selected space the site does not list now: what is stored
		// stays as it is.
		return nil
	}
	serviceDesk := p.listed[is.SpaceID].ServiceDesk
	items := p.y.items(is, cl.Comments, hist, serviceDesk, p.cfg.HideEvents)
	// An issue a notification mail named is kept whatever OnlyMine says:
	// the stored mark, or a mail that named it since (or before the issue
	// was ever stored).
	viaMail := prev != nil && prev.ViaMail
	if !viaMail {
		linked, err := s.deps.Store.IssueMailLinked(ctx, acc, is.Key)
		if err != nil {
			return storageError(err)
		}
		viaMail = linked
	}
	if p.cfg.OnlyMine && !viaMail && !p.mine(is, items) {
		if prev != nil {
			return s.forget(ctx, p, is.ID)
		}
		return nil
	}
	views := p.membership(is.Assignee.ID, is.Watching, is.Status.ID, is.Status.Category)
	targets := []store.Folder{spaceFolder}
	for _, v := range views {
		targets = append(targets, p.views[v])
	}
	inTarget := map[string]bool{}
	for _, f := range targets {
		inTarget[f.ID] = true
	}

	rows, err := s.deps.Store.IssueRows(ctx, acc, is.ID)
	if err != nil {
		return storageError(err)
	}
	storedItems := map[string]store.IssueItem{}
	if list, err := s.deps.Store.IssueItems(ctx, acc, is.ID); err != nil {
		return storageError(err)
	} else {
		for _, it := range list {
			storedItems[it.RemoteID] = it
		}
	}
	envelopes := map[string]store.Message{}
	threadID := store.IssueThreadID(is.ID)
	if msgs, err := s.deps.Store.ThreadMessages(ctx, acc, threadID, "", 1<<20); err != nil && !errors.Is(err, store.ErrNotFound) {
		return storageError(err)
	} else {
		for _, m := range msgs {
			if _, dup := envelopes[m.RemoteID]; !dup {
				envelopes[m.RemoteID] = m
			}
		}
	}
	want := map[string]*item{}
	for _, it := range items {
		want[it.remoteID] = it
	}
	byItem := map[string][]store.IssueRow{}
	for _, r := range rows {
		byItem[r.RemoteID] = append(byItem[r.RemoteID], r)
	}

	// Items the site no longer has.
	var gone []string
	for rid, rs := range byItem {
		if want[rid] == nil && vanished(rid, cl, hist, p.cfg.HideEvents) {
			gone = append(gone, rid)
			for _, r := range rs {
				p.touch(r.FolderID)
			}
		}
	}
	if len(gone) > 0 {
		sort.Strings(gone)
		if err := s.deps.Store.DeleteIssueItems(ctx, acc, gone); err != nil {
			return storageError(err)
		}
		for _, rid := range gone {
			delete(byItem, rid)
		}
	}

	// The flags an item's new copies inherit: its space folder's copy's,
	// else any copy's — read before the copies in folders the issue left
	// go.
	inherit := map[string][]api.Flag{}
	for rid, rs := range byItem {
		best := rs[0]
		for _, r := range rs {
			if r.FolderID == spaceFolder.ID {
				best = r
			}
		}
		inherit[rid] = best.Flags
	}
	left := map[string][]string{}
	for rid, rs := range byItem {
		kept := rs[:0:0]
		for _, r := range rs {
			if inTarget[r.FolderID] {
				kept = append(kept, r)
			} else {
				left[r.FolderID] = append(left[r.FolderID], rid)
			}
		}
		byItem[rid] = kept
	}
	for _, folderID := range sortedKeys(left) {
		if err := s.deps.Store.DeleteMessagesByRemoteID(ctx, folderID, left[folderID]); err != nil {
			return storageError(err)
		}
		p.touch(folderID)
	}

	// The folders holding each item's copies before this pass adds any: a
	// change of their envelope or body is a change in place (pass.mark).
	existing := map[string][]string{}
	for rid, rs := range byItem {
		for _, r := range rs {
			existing[rid] = append(existing[rid], r.FolderID)
		}
	}

	// New copies.
	subj := subject(is)
	announce := map[string]bool{}
	var fresh []*store.Message
	for _, it := range items {
		flags, known := inherit[it.remoteID]
		if !known {
			flags, announce[it.remoteID] = p.initialFlags(it, is.SpaceID)
		}
		have := map[string]bool{}
		for _, r := range byItem[it.remoteID] {
			have[r.FolderID] = true
		}
		for _, f := range targets {
			if have[f.ID] {
				continue
			}
			fresh = append(fresh, &store.Message{
				AccountID: acc, FolderID: f.ID, RemoteID: it.remoteID, Flags: flags,
				From: []api.Address{it.from}, Subject: subj, Date: it.date, InternalDate: it.date,
				RFCMessageID: it.msgID, InReplyTo: it.inReplyTo, References: refsOf(it.inReplyTo),
				ThreadID: threadID, BodyState: store.BodyNone,
			})
		}
	}
	if len(fresh) > 0 {
		if err := s.deps.Store.UpsertMessages(ctx, fresh); err != nil {
			return storageError(err)
		}
		for _, m := range fresh {
			byItem[m.RemoteID] = append(byItem[m.RemoteID], store.IssueRow{
				ID: m.ID, FolderID: m.FolderID, RemoteID: m.RemoteID, Flags: m.Flags, BodyState: store.BodyNone,
			})
			p.touch(m.FolderID)
		}
	}

	// Envelopes: a renamed or moved issue retitles its thread, a
	// re-attributed comment gets its author and date.
	retitle := prev != nil && subject(Issue{Key: prev.Key, Summary: prev.Summary}) != subj
	for _, it := range items {
		env, ok := envelopes[it.remoteID]
		if !ok {
			continue
		}
		if env.Subject != subj {
			retitle = true
		}
		if !sameSender(env.From, it.from) || !env.Date.Equal(it.date) {
			if err := s.deps.Store.UpdateEnvelopeByRemoteID(ctx, acc, it.remoteID, subj, []api.Address{it.from}, it.date); err != nil && !errors.Is(err, store.ErrNotFound) {
				return storageError(err)
			}
			p.mark(existing[it.remoteID]...)
		}
	}
	if retitle {
		if err := s.deps.Store.RetitleThread(ctx, acc, threadID, subj); err != nil && !errors.Is(err, store.ErrNotFound) {
			return storageError(err)
		}
		for _, folders := range existing {
			p.mark(folders...)
		}
	}

	// Bodies. The new items are announced once the issue is stored (below):
	// core reads the item and the issue rows into the notification.
	conflicted := map[string]bool{}
	var newItems []string
	for _, it := range items {
		copies := byItem[it.remoteID]
		sort.SliceStable(copies, func(i, j int) bool {
			return copies[i].FolderID == spaceFolder.ID && copies[j].FolderID != spaceFolder.ID
		})
		changed := s.itemChanged(ctx, p, it, storedItems, prev, copies)
		var need []store.IssueRow
		for _, r := range copies {
			if r.BodyState == store.BodyNone || changed {
				need = append(need, r)
			}
		}
		if len(need) == 0 {
			continue
		}
		var raw []byte
		if !changed {
			// A new copy of an item that did not change (a view the
			// issue joined) takes a whole stored copy's bytes.
			raw = s.wholeCopy(ctx, copies)
		}
		if raw == nil {
			if raw, err = p.y.build(ctx, is, it); err != nil {
				return err
			}
		}
		for _, r := range need {
			conflict, err := s.storeBody(ctx, r, raw, it)
			if err != nil {
				return err
			}
			if conflict {
				conflicted[it.remoteID] = true
				continue
			}
			if r.BodyState != store.BodyNone {
				// A stored body rebuilt: a client showing it must read it again.
				p.mark(r.FolderID)
			}
			if r.FolderID == spaceFolder.ID && announce[it.remoteID] && r.BodyState == store.BodyNone {
				newItems = append(newItems, r.ID)
			}
		}
	}

	// The items, then the issue: last, so that an interruption leaves it
	// owing a refresh.
	list := make([]store.IssueItem, 0, len(items))
	for _, it := range items {
		si := it.storeItem()
		if conflicted[it.remoteID] {
			si.Updated = time.Time{} // compares as changed next time
		}
		list = append(list, si)
	}
	if err := s.deps.Store.PutIssueItems(ctx, acc, list); err != nil {
		return storageError(err)
	}
	row := store.Issue{
		AccountID: acc, IssueID: is.ID, Key: is.Key, SpaceID: is.SpaceID, Summary: is.Summary,
		StatusID: is.Status.ID, Status: is.Status.Name, StatusCategory: is.Status.Category,
		Type: is.Type, Priority: is.Priority,
		AssigneeID: is.Assignee.ID, AssigneeName: is.Assignee.Name,
		ReporterID: is.Reporter.ID, ReporterName: is.Reporter.Name,
		Watching: is.Watching, ServiceDesk: serviceDesk, ViaMail: viaMail,
		Created: is.Created, Updated: is.Updated, SyncedUpdated: is.Updated,
		RenderKey: p.renderKey, Views: views,
	}
	if len(conflicted) > 0 {
		row.SyncedUpdated = time.Time{}
	}
	if err := s.deps.Store.PutIssue(ctx, row); err != nil {
		return storageError(err)
	}
	for _, id := range newItems {
		s.notifyNew(ctx, spaceFolder, id)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func refsOf(id string) []string {
	if id == "" {
		return nil
	}
	return []string{id}
}

func sameSender(a []api.Address, b api.Address) bool {
	return len(a) == 1 && a[0].Name == b.Name && strings.EqualFold(a[0].Address, b.Address)
}

// vanished reports whether a stored item the site did not return is gone.
// A comment older than the oldest one returned was left out by the cap,
// and so was a changelog entry older than the oldest returned: those
// stay.
func vanished(rid string, cl CommentList, hist []History, hideEvents bool) bool {
	kind, id, _ := strings.Cut(rid, ":")
	switch kind {
	case "c":
		if !cl.Truncated || len(cl.Comments) == 0 {
			return true
		}
		return !idLess(id, cl.Comments[0].ID)
	case "h":
		if hideEvents || len(hist) == 0 {
			return true
		}
		return !idLess(id, hist[0].ID)
	}
	return true
}

// mine reports an issue OnlyMine keeps: the user reports it, is assigned,
// watches it, or commented or changed it within the window.
func (p *pass) mine(is Issue, items []*item) bool {
	me := p.y.me.ID
	if me == "" || is.Reporter.ID == me || is.Assignee.ID == me || is.Watching {
		return true
	}
	since := p.start.Add(-time.Duration(p.days) * 24 * time.Hour)
	for _, it := range items {
		if it.kind != api.IssueItemDescription && it.authorID == me && !it.created.Before(since) {
			return true
		}
	}
	return false
}

// initialFlags are the flags of an item new to the store, and whether it
// is announced.
func (p *pass) initialFlags(it *item, spaceID string) ([]api.Flag, bool) {
	seen := it.kind == api.IssueItemEvent || it.mine
	first := p.firstBackfill[spaceID]
	if !seen {
		created := it.created
		if created.IsZero() {
			created = it.date
		}
		if first {
			seen = created.Before(p.start.Add(-firstBackfillFresh))
		} else {
			seen = created.Before(p.lastSync[spaceID].Add(-unreadMargin))
		}
	}
	if seen {
		return []api.Flag{api.FlagSeen}, false
	}
	return nil, !first
}

// itemChanged reports whether a stored item's message must be built
// again: it is new to the store, the issue was built with other settings,
// a comment was edited or re-attributed, or a description's revision (in
// its stored message) differs. Events never change.
func (s *Syncer) itemChanged(ctx context.Context, p *pass, it *item, stored map[string]store.IssueItem, prev *store.Issue, copies []store.IssueRow) bool {
	st, ok := stored[it.remoteID]
	if !ok || prev == nil || prev.RenderKey != p.renderKey {
		return true
	}
	switch it.kind {
	case api.IssueItemComment:
		return !st.Updated.Equal(it.updated) || st.Via != it.via || st.AuthorID != it.authorID || st.Visibility != it.visibility
	case api.IssueItemEvent:
		return false
	}
	for _, r := range copies {
		if r.BodyState == store.BodyFetched {
			return s.storedRevision(ctx, r.ID) != it.revision
		}
	}
	return true
}

// wholeCopy is the message of the first copy stored whole, nil when
// none is (or it cannot be read).
func (s *Syncer) wholeCopy(ctx context.Context, copies []store.IssueRow) []byte {
	for _, r := range copies {
		if r.BodyState != store.BodyFetched || r.RawState != store.RawFull {
			continue
		}
		raw, err := s.deps.Store.OpenMessageRaw(ctx, s.account.ID, r.ID)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(raw, ingest.MaxMessageBytes+1))
		raw.Close()
		if err == nil && len(data) <= ingest.MaxMessageBytes {
			return data
		}
	}
	return nil
}

// storedRevision reads the revision header of a stored message; "" when
// it cannot.
func (s *Syncer) storedRevision(ctx context.Context, id string) string {
	raw, err := s.deps.Store.OpenMessageRaw(ctx, s.account.ID, id)
	if err != nil {
		return ""
	}
	defer raw.Close()
	h, err := textproto.NewReader(bufio.NewReader(io.LimitReader(raw, maxHeaderScan))).ReadMIMEHeader()
	if err != nil && len(h) == 0 {
		return ""
	}
	return strings.TrimSpace(h.Get(revisionHeader))
}

// storeBody stores a built message as the body of one copy: a new copy
// as received, a stored one replaced only if it is still what it was read
// as (a download meanwhile wins: conflict). The copy's age for the
// attachment policy is the item's date.
func (s *Syncer) storeBody(ctx context.Context, r store.IssueRow, raw []byte, it *item) (conflict bool, err error) {
	prefs := s.prefs()
	pol := ingest.Policy{AttachmentOfflineDays: prefs.AttachmentOfflineDays, NeverStore: prefs.NeverStoreAttachments}
	req := ingest.Request{
		Target: ingest.Target{
			AccountID: s.account.ID, MessageID: r.ID, Role: api.RoleNone, HasServerCopy: true,
			InternalDate: it.date, Date: it.date,
		},
		Body:   bytes.NewReader(raw),
		Size:   int64(len(raw)),
		Limit:  ingest.MaxMessageBytes,
		Policy: pol,
		Now:    s.now(),
		Expect: store.RawExpect{BodyState: r.BodyState},
	}
	if r.BodyState != store.BodyNone {
		req.Expect.RawState = r.RawState
		req.Verify = &store.Message{RFCMessageID: it.msgID, BodyState: r.BodyState}
	}
	_, err = ingest.Store(ctx, s.deps.Store, req, s.log)
	switch {
	case err == nil:
		if s.deps.Stored != nil {
			s.deps.Stored(ctx, r.ID, pol)
		}
		return false, nil
	case errors.Is(err, ingest.ErrTooBig):
		return false, s.settleBody(ctx, r.ID, store.BodyTooBig)
	case errors.Is(err, ingest.ErrUnparsable):
		s.log.Warn("jira message unparsable", "message", r.ID)
		if r.BodyState == store.BodyNone {
			return false, s.settleBody(ctx, r.ID, store.BodyFailed)
		}
		return true, nil
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrNotFound), errors.Is(err, ingest.ErrMismatch):
		return true, nil
	case ctx.Err() != nil:
		return false, ctx.Err()
	}
	return false, storageError(err)
}

func (s *Syncer) settleBody(ctx context.Context, id string, state store.BodyState) error {
	if err := s.deps.Store.MarkBodyState(ctx, id, state); err != nil && !errors.Is(err, store.ErrNotFound) {
		return storageError(err)
	}
	return nil
}

// notifyNew emits notify.newMessage for a new item's space-folder copy.
func (s *Syncer) notifyNew(ctx context.Context, f store.Folder, id string) {
	if s.deps.Notifier == nil {
		return
	}
	m, err := s.deps.Store.GetMessage(ctx, s.account.ID, id)
	if err != nil {
		return
	}
	s.deps.Notifier.NewMessage(api.NewMessageNotification{
		AccountID: api.AccountID(s.account.ID),
		FolderID:  api.FolderID(f.ID),
		Message:   summaryOf(m),
	})
}

// summaryOf is the list-view projection of a stored message (core adds
// the issue).
func summaryOf(m store.Message) api.MessageSummary {
	sum := api.MessageSummary{
		ID:             api.MessageID(m.ID),
		AccountID:      api.AccountID(m.AccountID),
		FolderID:       api.FolderID(m.FolderID),
		ThreadID:       api.ThreadID(m.ThreadID),
		From:           m.From,
		To:             m.To,
		Subject:        m.Subject,
		Date:           m.Date,
		Snippet:        m.Snippet,
		Flags:          m.Flags,
		HasAttachments: m.HasAttachments,
		Size:           m.Size,
	}
	if sum.From == nil {
		sum.From = []api.Address{}
	}
	if sum.Flags == nil {
		sum.Flags = []api.Flag{}
	}
	if sum.Date.IsZero() {
		sum.Date = m.InternalDate
	}
	return sum
}
