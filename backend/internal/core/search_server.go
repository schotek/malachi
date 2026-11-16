// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/graph"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/search"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// search.server (docs/api.md §4.6, docs/plans/server-search.md): the mail
// older than the offlineDays window is on the servers only, so the query
// goes to them. What they find is stored as rows with their headers
// (the sync engines download the bodies) and kept outside the window for
// api.ServerSearchKeepDays; the results are those rows.

// serverSearchTimeout bounds one search.server over all its accounts.
const serverSearchTimeout = 30 * time.Second

// serverSearchNow is the clock of the window's start and of the keep;
// tests pin it.
var serverSearchNow = time.Now

// serverSearchFn searches one account's folders on its server and returns
// the rows found, newest first within a folder; tests replace it.
type serverSearchFn func(ctx context.Context, a store.Account, folders []store.Folder, q search.Query, before time.Time, whole bool) (rows []*store.Message, truncated bool, err error)

// serverSearches lets a new search.server end the one still running: the
// user typed on, and the servers need not finish the older query.
type serverSearches struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	gen    uint64
}

// begin ends the running search and returns the context of a new one and
// what ends it.
func (r *serverSearches) begin(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(ctx, serverSearchTimeout)
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	r.gen++
	gen := r.gen
	r.cancel = cancel
	r.mu.Unlock()
	return ctx, func() {
		cancel()
		r.mu.Lock()
		if r.gen == gen {
			r.cancel = nil
		}
		r.mu.Unlock()
	}
}

// Server searches the mail servers of the accounts in scope (docs/api.md,
// search.server). The query is what the user typed and is never logged.
func (s *searchService) Server(ctx context.Context, p api.SearchServerParams) (*api.SearchServerResult, error) {
	text := strings.TrimSpace(p.Query)
	switch {
	case text == "":
		return nil, api.NewError(api.CodeInvalidArgument, "query is required")
	case len(p.Query) > api.MaxSearchQueryBytes:
		return nil, api.NewError(api.CodeInvalidArgument, "query is longer than %d bytes", api.MaxSearchQueryBytes)
	case p.FolderID != "" && p.AccountID == "":
		return nil, api.NewError(api.CodeInvalidArgument, "folderId needs accountId")
	}
	q := search.Parse(text)
	if q.Size() > api.MaxSearchTerms {
		return nil, api.NewError(api.CodeInvalidArgument, "query has more than %d terms and filters", api.MaxSearchTerms)
	}
	out := &api.SearchServerResult{Results: []api.SearchResult{}}

	var accounts []store.Account
	if p.AccountID != "" {
		a, err := s.b.requireAccount(ctx, string(p.AccountID))
		if err != nil {
			return nil, err
		}
		if p.FolderID != "" {
			if _, err := s.b.store.GetFolder(ctx, a.ID, string(p.FolderID)); errors.Is(err, store.ErrNotFound) {
				return nil, api.NewError(api.CodeFolderNotFound, "unknown folder %q", p.FolderID)
			} else if err != nil {
				return nil, api.NewError(api.CodeStorageError, "%v", err)
			}
		}
		accounts = []store.Account{a}
	} else {
		all, err := s.b.store.ListAccounts(ctx)
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		for _, a := range all {
			if a.Enabled {
				accounts = append(accounts, a)
			}
		}
	}
	// Only mail accounts have a server to search; an issue tracker has a
	// window and a query language of its own.
	accounts = slices.DeleteFunc(accounts, func(a store.Account) bool { return !isMailAccount(a) })

	_, days := s.b.SyncPrefs()
	if q.Empty() || days <= 0 || len(accounts) == 0 {
		return out, nil // nothing to search by, or nothing is older than the store
	}
	now := serverSearchNow()
	before := time.Date(now.Year(), now.Month(), now.Day()-days, 0, 0, 0, 0, time.UTC)

	var inIDs []string
	if len(q.In) > 0 {
		ids, err := s.resolveIn(ctx, accounts, q.In)
		if err != nil {
			return nil, err
		}
		inIDs = ids
	}
	// The whole account (no folder, no in:) is searched as one mailbox where
	// the service offers it (graphServerSearch).
	whole := p.FolderID == "" && len(q.In) == 0
	type job struct {
		account store.Account
		folders []store.Folder
	}
	var jobs []job
	for _, a := range accounts {
		all, err := s.b.store.ListFolders(ctx, a.ID)
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		var folders []store.Folder
		for _, f := range all {
			switch {
			case !f.Selectable || f.Unsynced || f.Virtual != "" || f.Role == api.RoleOutbox:
				continue
			case p.FolderID != "" && f.ID != string(p.FolderID):
				continue
			case len(q.In) > 0 && !slices.Contains(inIDs, f.ID):
				continue
			}
			folders = append(folders, f)
		}
		if len(folders) > 0 {
			jobs = append(jobs, job{a, folders})
		}
	}
	if len(jobs) == 0 {
		return out, nil
	}

	sctx, done := s.b.serverSearches.begin(ctx)
	defer done()
	fn := s.b.serverSearch
	if fn == nil {
		fn = s.b.searchAccountServer
	}
	type answer struct {
		rows      []*store.Message
		truncated bool
		err       error
	}
	answers := make([]answer, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, truncated, err := fn(sctx, j.account, j.folders, q, before, whole)
			answers[i] = answer{rows, truncated, err}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if errors.Is(sctx.Err(), context.Canceled) {
		// A newer search took over; this one's results are not wanted.
		return nil, api.NewError(api.CodeUnavailable, "superseded by a newer search")
	}

	until := now.AddDate(0, 0, api.ServerSearchKeepDays)
	var found []store.Message
	for i, j := range jobs {
		ans := answers[i]
		if ans.err != nil {
			code := codeOf(ans.err)
			if code == 0 {
				code = api.CodeServerError
				if errors.Is(ans.err, context.DeadlineExceeded) {
					code = api.CodeServerTimeout
				}
			}
			s.b.log.Info("search on the server failed", "account", j.account.ID, "code", code)
			out.Failed = append(out.Failed, api.SearchServerFailure{AccountID: api.AccountID(j.account.ID), Code: code})
		}
		rows := ans.rows
		sort.SliceStable(rows, func(a, b int) bool { return foundAt(rows[a]).After(foundAt(rows[b])) })
		if len(rows) > api.MaxServerSearchResults {
			rows, ans.truncated = rows[:api.MaxServerSearchResults], true
		}
		out.Truncated = out.Truncated || ans.truncated
		if len(rows) == 0 {
			continue
		}
		stored, err := s.b.storeFound(ctx, j.account, rows, until)
		if err != nil {
			return nil, err
		}
		found = append(found, stored...)
	}
	sort.SliceStable(found, func(a, b int) bool { return found[a].Date.After(found[b].Date) })
	for _, m := range found {
		sum := toAPISummary(m)
		out.Results = append(out.Results, api.SearchResult{Message: sum, Snippet: sum.Snippet})
	}
	return out, nil
}

// foundAt is when the server received a row, its Date when unknown.
func foundAt(m *store.Message) time.Time {
	if m.InternalDate.IsZero() {
		return m.Date
	}
	return m.InternalDate
}

// storeFound stores the rows a server search found in an account, keeps
// them outside the window until until and returns them as stored, visible
// rows. A row the store already has keeps what it has. The folders'
// syncers are asked for a pass, which downloads the bodies and counts the
// folders.
func (b *Backend) storeFound(ctx context.Context, a store.Account, rows []*store.Message, until time.Time) ([]store.Message, error) {
	if err := b.store.UpsertMessages(ctx, rows); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	ids := make([]string, 0, len(rows))
	folders := map[string]bool{}
	for _, m := range rows {
		ids = append(ids, m.ID)
		folders[m.FolderID] = true
	}
	if err := b.store.KeepMessages(ctx, ids, until); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := make([]store.Message, 0, len(ids))
	for _, id := range ids {
		m, err := b.store.GetMessage(ctx, a.ID, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			continue // deleted meanwhile
		case err != nil:
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		if !m.Hidden {
			out = append(out, m)
		}
	}
	if b.Supervisor != nil && a.Enabled {
		for id := range folders {
			b.Supervisor.Trigger(a.ID, api.FolderID(id), true)
		}
	}
	return out, nil
}

// searchAccountServer searches the folders of one mail account on its
// server: IMAP on a connection of its own with the account's credential
// (asked for again once after the server refused an OAuth2 token),
// Microsoft Graph with the account's token and limiter.
func (b *Backend) searchAccountServer(ctx context.Context, a store.Account, folders []store.Folder, q search.Query, before time.Time, whole bool) ([]*store.Message, bool, error) {
	var rows []*store.Message
	truncated := false
	if a.Config.Protocol() == api.AccountGraph {
		id := a.ID
		client := graph.NewClient(graph.Options{
			Token:      func(ctx context.Context) (string, error) { return b.GraphTokenFor(ctx, id) },
			Invalidate: func() { b.invalidateGraphTokenFor(id) },
			Log:        b.log.With("component", "graph", "account", id),
			Limiter:    b.graphLimiter(id),
		})
		found, err := b.graphServerSearch(ctx, client, a.ID, folders, graph.ServerSearch{
			Query: q, Zone: searchTimeZone, Before: before, Limit: api.MaxServerSearchResults,
		}, whole)
		for _, f := range found {
			// Rows an earlier search stored under the service's REST ids
			// are re-keyed to the immutable ids these rows carry, so that
			// a message stays one row (graph.Found.Renamed).
			if len(f.Renamed) > 0 {
				if rerr := b.store.RenameRemoteIDs(ctx, f.Folder.ID, f.Renamed); rerr != nil {
					return nil, false, api.NewError(api.CodeStorageError, "%v", rerr)
				}
			}
			rows = append(rows, f.Messages...)
			truncated = truncated || f.Truncated
		}
		if err != nil {
			if e := graph.ToAPIError(err); e != nil {
				return rows, truncated, e
			}
		}
		return rows, truncated, err
	}
	if a.Config.IMAP == nil {
		return nil, false, api.NewError(api.CodeInvalidArgument, "account %s has no IMAP endpoint", a.ID)
	}
	for retried := false; ; retried = true {
		secret, err := b.credentialFor(ctx, a.ID)
		if err != nil {
			return nil, false, err
		}
		found, err := imap.SearchServer(ctx, *a.Config.IMAP, secret, a.ID, folders, imap.ServerSearch{
			Query: q, Zone: searchTimeZone, Before: before, Limit: api.MaxServerSearchResults,
		})
		if !retried && codeOf(err) == api.CodeAuthFailed && usesAuth(a.Config, api.AuthOAuth2) {
			b.invalidateCredentialsFor(a.ID) // a token that expired early
			continue
		}
		for _, f := range found {
			rows = append(rows, f.Messages...)
			truncated = truncated || f.Truncated
		}
		return rows, truncated, err
	}
}
