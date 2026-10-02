// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/schotek/malachi/backend/internal/board"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Board serves the board.* methods (docs/api.md §4.13). The cases are
// computed in the background (board_worker.go, board_adapter.go); the
// methods read them, record the user's decisions and what an assistant
// found, and tell the worker and the clients what changed. No method waits
// for the worker or sends a notification itself.
func (b *Backend) Board() api.BoardService { return &boardService{b} }

type boardService struct{ b *Backend }

var _ api.BoardService = (*boardService)(nil)

// metaBoardPrefs holds the board's preferences as JSON (api.BoardPreferences).
const metaBoardPrefs = "board.prefs"

// boardQueueAddresses caps the To and Cc addresses of a queue message.
const boardQueueAddresses = 50

// boardPrefs reads the preferences: the stored ones over the defaults.
func (b *Backend) boardPrefs(ctx context.Context) (api.BoardPreferences, error) {
	p := api.DefaultBoardPreferences()
	raw, found, err := b.store.GetMeta(ctx, metaBoardPrefs)
	if err != nil {
		return p, api.NewError(api.CodeStorageError, "%v", err)
	}
	if found {
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			b.log.Warn("board: unreadable preferences, using the defaults", "err", err)
			p = api.DefaultBoardPreferences()
		}
	}
	if p.TriageAccounts == nil {
		p.TriageAccounts = []api.AccountID{}
	}
	return p, nil
}

// requireBoard returns the preferences of an enabled board, else
// invalidArgument (every method but the preferences).
func (s *boardService) requireBoard(ctx context.Context) (api.BoardPreferences, error) {
	p, err := s.b.boardPrefs(ctx)
	if err != nil {
		return p, err
	}
	if !p.Enabled {
		return p, api.NewError(api.CodeInvalidArgument, "the board is disabled")
	}
	return p, nil
}

// boardErr maps a store failure of a board method.
func boardErr(err error) error {
	var apiErr *api.Error
	switch {
	case errors.As(err, &apiErr):
		return apiErr
	case errors.Is(err, store.ErrNotFound):
		return api.NewError(api.CodeCaseNotFound, "no such case")
	case errors.Is(err, store.ErrBoardConflict):
		return api.NewError(api.CodeConflict, "the case changed since its inputKey was read")
	case errors.Is(err, store.ErrBoardNotMine):
		return api.NewError(api.CodeInvalidArgument, "messageId is not one of the user's messages in the case")
	case isCancelled(err):
		return api.NewError(api.CodeCancelled, "%v", err)
	}
	return api.NewError(api.CodeStorageError, "%v", err)
}

// caseOf reads a case; invalidArgument for an empty id, caseNotFound for
// an unknown one.
func (s *boardService) caseOf(ctx context.Context, id api.BoardCaseID) (store.BoardCase, error) {
	if id == "" {
		return store.BoardCase{}, api.NewError(api.CodeInvalidArgument, "caseId is required")
	}
	c, err := s.b.store.GetBoardCase(ctx, string(id))
	if err != nil {
		return store.BoardCase{}, boardErr(err)
	}
	return c, nil
}

// toAPIBoardCase projects a stored case at now.
func toAPIBoardCase(c store.BoardCase, now time.Time) api.BoardCase {
	out := api.BoardCase{
		ID: api.BoardCaseID(c.ID), AccountID: api.AccountID(c.AccountID), ThreadID: api.ThreadID(c.ThreadID),
		RuleState: c.RuleState, RuleReason: c.RuleReason,
		Visibility: c.Visibility(now),
		Subject:    c.Subject, Person: c.Person, Date: c.Date, Snippet: c.Snippet,
		Unread: c.Unread, HasAttachments: c.HasAttachments, MessageCount: c.MessageCount,
		ReplyMessageID: api.MessageID(c.ReplyMessageID), ReplyFolderID: api.FolderID(c.ReplyFolderID),
		LatestMessageID: api.MessageID(c.LatestMessageID),
		CanArchive:      c.CanArchive,
		Version:         c.Version,
	}
	if c.UserState != "" {
		st := c.UserState
		out.UserState = &st
	}
	switch out.Visibility {
	case api.BoardDone:
		t := c.DoneAt
		out.DoneAt = &t
	case api.BoardSnoozed:
		t := c.RemindAt
		out.RemindAt = &t
	}
	if c.Issue != nil {
		out.Issue = &api.BoardIssue{Key: c.Issue.Key, Status: c.Issue.Status, StatusCategory: c.Issue.StatusCategory}
	}
	if a := c.Annotation; a != nil {
		an := &api.BoardAnnotation{Title: a.Title, Summary: a.Summary, Why: a.Why, Tasks: a.Tasks,
			Source: a.Source, At: a.At, Stale: a.Stale}
		if an.Tasks == nil {
			an.Tasks = []string{}
		}
		if a.State != "" {
			st := a.State
			an.State = &st
		}
		if a.Due != nil {
			an.Due = &api.BoardDue{At: a.Due.At, Quote: a.Due.Quote, MessageID: api.MessageID(a.Due.MessageID)}
		}
		out.Annotation = an
	}
	if d := c.Draft; d != nil {
		out.Draft = &api.BoardDraft{DraftID: api.DraftID(d.DraftID), Text: d.Text, Updated: d.Updated}
	}
	return out
}

func toAPICommitment(k store.BoardCommitment) api.BoardCommitment {
	out := api.BoardCommitment{
		ID: api.BoardCommitmentID(k.ID), CaseID: api.BoardCaseID(k.CaseID), AccountID: api.AccountID(k.AccountID),
		MessageID: api.MessageID(k.MessageID), Text: k.Text, Quote: k.Quote, State: k.State,
		ClosedReason: k.ClosedReason, At: k.At,
	}
	if !k.Due.IsZero() {
		d := k.Due
		out.Due = &d
	}
	return out
}

func storeWindows(w api.BoardWindows) store.BoardWindowDays {
	return store.BoardWindowDays{
		Hot: board.WindowDays(api.BoardHot, w), You: board.WindowDays(api.BoardYou, w),
		Them: board.WindowDays(api.BoardThem, w), Info: board.WindowDays(api.BoardInfo, w),
	}
}

// localDay is the daemon's local day of t ("YYYY-MM-DD") and its start.
func localDay(t time.Time) (string, time.Time) {
	l := t.In(time.Local)
	start := time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.Local)
	return l.Format(time.DateOnly), start.UTC()
}

// triageAccounts are the accounts triage may read and annotate: the
// enabled accounts the preferences name, or with none named every enabled
// mail account.
func (b *Backend) triageAccounts(ctx context.Context, p api.BoardPreferences) (map[string]store.Account, error) {
	list, err := b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	named := map[string]bool{}
	for _, id := range p.TriageAccounts {
		named[string(id)] = true
	}
	out := map[string]store.Account{}
	for _, a := range list {
		if !a.Enabled {
			continue
		}
		if len(named) == 0 && isIssueAccount(a) {
			continue
		}
		if len(named) > 0 && !named[a.ID] {
			continue
		}
		out[a.ID] = a
	}
	return out, nil
}

func sortedAccountIDs(m map[string]store.Account) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// --- board.list ------------------------------------------------------------

func (s *boardService) List(ctx context.Context, p api.BoardListParams) (*api.BoardListResult, error) {
	prefs, err := s.b.boardPrefs(ctx)
	if err != nil {
		return nil, err
	}
	out := &api.BoardListResult{Cases: []api.BoardCase{}, Commitments: []api.BoardCommitment{},
		Enabled: prefs.Enabled, Assistant: prefs.Assistant, Ready: s.b.board.ready.Load()}
	all, err := s.b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	enabled := map[string]bool{}
	known := map[string]bool{}
	for _, a := range all {
		known[a.ID] = true
		enabled[a.ID] = a.Enabled
	}
	var accounts []string
	if len(p.AccountIDs) == 0 {
		for _, a := range all {
			if a.Enabled {
				accounts = append(accounts, a.ID)
			}
		}
	} else {
		for _, id := range p.AccountIDs {
			if !known[string(id)] {
				return nil, api.NewError(api.CodeAccountNotFound, "unknown account %q", id)
			}
			if enabled[string(id)] && !slices.Contains(accounts, string(id)) {
				accounts = append(accounts, string(id))
			}
		}
	}
	if !prefs.Enabled {
		return out, nil
	}
	now := s.b.boardNow()
	listing, err := s.b.store.ListBoard(ctx, store.BoardListQuery{AccountIDs: accounts, Now: now,
		Windows: storeWindows(prefs.Windows), DoneDays: int(boardDoneKeep / (24 * time.Hour)), Assistant: prefs.Assistant})
	if err != nil {
		return nil, boardErr(err)
	}
	for _, c := range listing.Cases {
		out.Cases = append(out.Cases, toAPIBoardCase(c, now))
	}
	for _, k := range listing.Commitments {
		out.Commitments = append(out.Commitments, toAPICommitment(k))
	}
	out.Truncated = listing.Truncated
	_, dayStart := localDay(now)
	stats, err := s.b.store.BoardRunStats(ctx, dayStart)
	if err != nil {
		return nil, boardErr(err)
	}
	if r := stats.LastRun; r != nil {
		lr := &api.BoardRun{At: r.StartedAt, Trigger: r.Trigger, Source: r.Source, Annotated: r.Annotated, Error: r.Error}
		if !r.EndedAt.IsZero() {
			t := r.EndedAt
			lr.EndedAt = &t
		}
		out.Triage.LastRun = lr
	}
	out.Triage.AnnotatedTodayAuto = stats.AnnotatedAuto
	usage, err := s.b.store.BoardRunUsage(ctx, now.Add(-boardUsageWindow))
	if err != nil {
		return nil, boardErr(err)
	}
	if usage.Runs > 0 {
		out.Triage.Usage24h = &usage
	}
	if prefs.Assistant {
		triage, err := s.b.triageAccounts(ctx, prefs)
		if err != nil {
			return nil, err
		}
		n, err := s.b.store.CountBoardQueue(ctx, store.BoardQueueQuery{AccountIDs: sortedAccountIDs(triage), Now: now,
			Windows: storeWindows(prefs.Windows)})
		if err != nil {
			return nil, boardErr(err)
		}
		out.Triage.Queue = n
	}
	return out, nil
}

// --- board.get -------------------------------------------------------------

func (s *boardService) Get(ctx context.Context, p api.BoardGetParams) (*api.BoardGetResult, error) {
	if _, err := s.requireBoard(ctx); err != nil {
		return nil, err
	}
	c, err := s.caseOf(ctx, p.CaseID)
	if err != nil {
		return nil, err
	}
	msgs, err := s.b.store.BoardMessages(ctx, c.ID, api.MaxBoardMessages)
	if err != nil {
		return nil, boardErr(err)
	}
	now := s.b.boardNow()
	out := &api.BoardGetResult{Case: toAPIBoardCase(c, now), Messages: make([]api.BoardMessage, 0, len(msgs))}
	for _, m := range msgs {
		// The member's own text: its quoted history cut off as
		// message.body with trimQuoted does (boardExcerptSource).
		text, cut := s.b.boardExcerptSource(ctx, c.AccountID, m)
		ex := board.BoardMessageExcerpt(text)
		bm := api.BoardMessage{ID: api.MessageID(m.ID), FolderID: api.FolderID(m.FolderID), Date: boardMessageDate(m, now),
			Mine: m.Mine, Text: ex.Text, Trimmed: ex.Trimmed || cut || m.TextCut}
		if len(m.From) > 0 {
			bm.From = board.CleanAddress(m.From[0])
		}
		out.Messages = append(out.Messages, bm)
	}
	return out, nil
}

// boardMessageDate is when a member arrived, as the rules and the case's
// date see it (board.Arrival): its internal date, else its Date header,
// else when the daemon stored it, never later than that nor than now.
func boardMessageDate(m store.BoardMessage, now time.Time) time.Time {
	return board.Arrival(board.Member{Date: m.Date, InternalDate: m.InternalDate, StoredAt: m.StoredAt}, now)
}

// --- the user's decisions ----------------------------------------------------

func (s *boardService) SetState(ctx context.Context, p api.BoardSetStateParams) (*api.BoardSetStateResult, error) {
	if _, err := s.requireBoard(ctx); err != nil {
		return nil, err
	}
	var st api.BoardState
	if p.State != nil {
		st = *p.State
		if !st.Valid() {
			return nil, api.NewError(api.CodeInvalidArgument, "unknown state %q", st)
		}
	}
	if _, err := s.caseOf(ctx, p.CaseID); err != nil {
		return nil, err
	}
	c, err := s.b.store.SetBoardUserState(ctx, string(p.CaseID), st, s.b.boardNow())
	if err != nil {
		return nil, boardErr(err)
	}
	s.b.boardWritten(ctx, c)
	return &api.BoardSetStateResult{Case: toAPIBoardCase(c, s.b.boardNow())}, nil
}

func (s *boardService) SetDone(ctx context.Context, p api.BoardSetDoneParams) (*api.BoardSetDoneResult, error) {
	if _, err := s.requireBoard(ctx); err != nil {
		return nil, err
	}
	if _, err := s.caseOf(ctx, p.CaseID); err != nil {
		return nil, err
	}
	c, err := s.b.store.SetBoardDone(ctx, string(p.CaseID), p.Done, s.b.boardNow())
	if err != nil {
		return nil, boardErr(err)
	}
	s.b.boardWritten(ctx, c)
	return &api.BoardSetDoneResult{Case: toAPIBoardCase(c, s.b.boardNow())}, nil
}

func (s *boardService) Remind(ctx context.Context, p api.BoardRemindParams) (*api.BoardRemindResult, error) {
	if _, err := s.requireBoard(ctx); err != nil {
		return nil, err
	}
	now := s.b.boardNow()
	var until time.Time
	if p.Until != nil {
		until = p.Until.UTC()
		if !until.After(now) || until.After(now.Add(api.MaxBoardRemind)) {
			return nil, api.NewError(api.CodeInvalidArgument, "until must lie in the future and within a year")
		}
	}
	if _, err := s.caseOf(ctx, p.CaseID); err != nil {
		return nil, err
	}
	c, err := s.b.store.SetBoardRemind(ctx, string(p.CaseID), until)
	if err != nil {
		return nil, boardErr(err)
	}
	s.b.boardWritten(ctx, c) // also re-arms the worker's remind timer
	return &api.BoardRemindResult{Case: toAPIBoardCase(c, s.b.boardNow())}, nil
}

func (s *boardService) Archive(ctx context.Context, p api.BoardArchiveParams) (*api.BoardArchiveResult, error) {
	if _, err := s.requireBoard(ctx); err != nil {
		return nil, err
	}
	c, err := s.caseOf(ctx, p.CaseID)
	if err != nil {
		return nil, err
	}
	a, err := s.b.store.GetAccount(ctx, c.AccountID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeCaseNotFound, "the case's account is gone")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := &api.BoardArchiveResult{NoArchive: true}
	archive, aerr := s.b.store.FolderByRole(ctx, a.ID, api.RoleArchive)
	if aerr != nil && !errors.Is(aerr, store.ErrNotFound) {
		return nil, api.NewError(api.CodeStorageError, "%v", aerr)
	}
	// As message.move: only into a selectable folder. One that is not
	// counts as no archive folder (the case is only marked done).
	if can(a.Config, api.CapabilityMove) && aerr == nil && archive.Selectable {
		out.NoArchive = false
		ids, err := s.inboxMembers(ctx, a.ID, c.ThreadID)
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			// As message.move does: local first, the server through the
			// operation log.
			if _, err := s.b.store.MoveMessages(ctx, a.ID, ids, archive.ID); err != nil {
				return nil, mutationError(err)
			}
			s.b.Supervisor.Trigger(a.ID, "", false)
			out.Archived = len(ids)
		}
	}
	c, err = s.b.store.SetBoardDone(ctx, c.ID, true, s.b.boardNow())
	if err != nil {
		return nil, boardErr(err)
	}
	s.b.boardWritten(ctx, c)
	out.Case = toAPIBoardCase(c, s.b.boardNow())
	return out, nil
}

// Unflag clears the flag (the star) of every copy of the case's members
// whose flag the rules read for hot.flagged (board.FlaggedCopies over the
// thread exactly as the drain judges it): locally and, as message.flag
// does, through the operation log for the server, or on an issue-tracker
// account on the item's local copies. A flagged copy in the trash, or of
// a message that does not count, keeps its flag. Returns the case as
// stored: the rules judge the thread again (the flag change marks it
// dirty) and notify.boardChanged follows. Nothing flagged is no error.
func (s *boardService) Unflag(ctx context.Context, p api.BoardUnflagParams) (*api.BoardUnflagResult, error) {
	if _, err := s.requireBoard(ctx); err != nil {
		return nil, err
	}
	c, err := s.caseOf(ctx, p.CaseID)
	if err != nil {
		return nil, err
	}
	a, err := s.b.store.GetAccount(ctx, c.AccountID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeCaseNotFound, "the case's account is gone")
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	t, err := s.b.store.BoardCaseThread(ctx, c.ID)
	if err != nil {
		return nil, boardErr(err)
	}
	accounts, err := s.b.boardAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	acc := accounts[a.ID]
	if acc == nil {
		return nil, api.NewError(api.CodeCaseNotFound, "the case's account is gone")
	}
	id := acc.identity
	if acc.jira {
		id = board.NewIdentity(t.Me, acc.addresses, nil)
	}
	flagged := board.FlaggedCopies(boardThreadOf(t, acc), id, s.b.boardNow())
	out := &api.BoardUnflagResult{Case: toAPIBoardCase(c, s.b.boardNow())}
	outbox := map[string]bool{}
	for _, m := range t.Members {
		if m.Role == api.RoleOutbox {
			outbox[m.ID] = true // message.flag refuses the outbox's rows
		}
	}
	ids := make([]string, 0, len(flagged))
	for _, m := range flagged {
		if !outbox[string(m)] {
			ids = append(ids, string(m))
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	if err := s.b.store.FlagMessages(ctx, a.ID, ids, nil, []api.Flag{api.FlagFlagged}); err != nil {
		return nil, mutationError(err)
	}
	if isIssueAccount(a) {
		if err := s.b.flagCopies(ctx, a.ID, ids); err != nil {
			return nil, err
		}
	} else {
		s.b.Supervisor.Trigger(a.ID, "", false)
	}
	out.Unflagged = len(ids)
	if c, err = s.b.store.GetBoardCase(ctx, c.ID); err != nil {
		return nil, boardErr(err)
	}
	s.b.boardWritten(ctx, c)
	out.Case = toAPIBoardCase(c, s.b.boardNow())
	return out, nil
}

// inboxMembers lists the thread's messages in the folder of role inbox.
func (s *boardService) inboxMembers(ctx context.Context, accountID, threadID string) ([]string, error) {
	inbox, err := s.b.store.FolderByRole(ctx, accountID, api.RoleInbox)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	msgs, err := s.b.store.ThreadMessages(ctx, accountID, threadID, inbox.ID, api.MaxThreadMessages)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if !m.Hidden {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

func (s *boardService) DiscardDraft(ctx context.Context, p api.BoardDiscardDraftParams) (*api.BoardDiscardDraftResult, error) {
	if _, err := s.requireBoard(ctx); err != nil {
		return nil, err
	}
	c, err := s.caseOf(ctx, p.CaseID)
	if err != nil {
		return nil, err
	}
	if c.DraftID == "" {
		return &api.BoardDiscardDraftResult{Case: toAPIBoardCase(c, s.b.boardNow())}, nil
	}
	if c.Draft != nil {
		// As draft.delete: the copy on the server goes too.
		if _, err := (&draftService{s.b}).Delete(ctx, api.DraftDeleteParams{
			AccountID: api.AccountID(c.AccountID), DraftID: api.DraftID(c.DraftID)}); err != nil {
			return nil, err
		}
	}
	c, err = s.b.store.SetBoardDraft(ctx, c.ID, "")
	if err != nil {
		return nil, boardErr(err)
	}
	s.b.boardWritten(ctx, c)
	return &api.BoardDiscardDraftResult{Case: toAPIBoardCase(c, s.b.boardNow())}, nil
}

// SetDraft links a draft to a case as its suggested reply on the user's
// request (board.setDraft): the same draft checks as board.annotate's
// draftId, but neither an annotation nor the assistant preference is
// needed. A case that links another draft that still exists is refused
// with conflict (the user discards it first); linking the draft already
// linked changes nothing. A linked draft is local (store.SetBoardDraft):
// a copy it had in the Drafts folder is deleted on the server.
func (s *boardService) SetDraft(ctx context.Context, p api.BoardSetDraftParams) (*api.BoardSetDraftResult, error) {
	if _, err := s.requireBoard(ctx); err != nil {
		return nil, err
	}
	if p.DraftID == "" {
		return nil, boardInvalid("draftId is required")
	}
	c, err := s.caseOf(ctx, p.CaseID)
	if err != nil {
		return nil, err
	}
	if err := s.checkDraft(ctx, c, string(p.DraftID)); err != nil {
		return nil, err
	}
	wasLocal := s.b.draftLocal(ctx, c.AccountID, string(p.DraftID))
	stored, err := s.b.store.SetBoardDraft(ctx, c.ID, string(p.DraftID))
	switch {
	case errors.Is(err, store.ErrBoardDraftLinked):
		return nil, api.NewError(api.CodeConflict, "the case already links another draft; discard it first")
	case err != nil:
		return nil, boardErr(err)
	}
	if !wasLocal {
		s.b.draftMadeLocal(c.AccountID)
	}
	if stored.Version != c.Version {
		s.b.boardWritten(ctx, stored)
	}
	return &api.BoardSetDraftResult{Case: toAPIBoardCase(stored, s.b.boardNow())}, nil
}

// --- triage ------------------------------------------------------------------

// requireAssistant returns the preferences when triage may run: the board
// enabled and the assistant preference on.
func (s *boardService) requireAssistant(ctx context.Context) (api.BoardPreferences, error) {
	prefs, err := s.requireBoard(ctx)
	if err != nil {
		return prefs, err
	}
	if !prefs.Assistant {
		return prefs, api.NewError(api.CodeInvalidArgument, "the board's assistant preference is off")
	}
	return prefs, nil
}

func (s *boardService) Queue(ctx context.Context, p api.BoardQueueParams) (*api.BoardQueueResult, error) {
	prefs, err := s.requireAssistant(ctx)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	switch {
	case limit == 0:
		limit = api.DefaultBoardQueueLimit
	case limit < 0 || limit > api.MaxBoardQueueLimit:
		return nil, api.NewError(api.CodeInvalidArgument, "limit must be 1..%d", api.MaxBoardQueueLimit)
	}
	triage, err := s.b.triageAccounts(ctx, prefs)
	if err != nil {
		return nil, err
	}
	accounts := sortedAccountIDs(triage)
	if len(p.AccountIDs) > 0 {
		var asked []string
		for _, id := range p.AccountIDs {
			if _, ok := triage[string(id)]; ok && !slices.Contains(asked, string(id)) {
				asked = append(asked, string(id))
			}
		}
		accounts = asked
	}
	caseIDs := make([]string, 0, len(p.CaseIDs))
	for _, id := range p.CaseIDs {
		caseIDs = append(caseIDs, string(id))
	}
	out := &api.BoardQueueResult{Items: []api.BoardQueueItem{}}
	if len(accounts) == 0 {
		return out, nil
	}
	items, remaining, err := s.b.store.BoardQueue(ctx, store.BoardQueueQuery{AccountIDs: accounts, CaseIDs: caseIDs,
		Now: s.b.boardNow(), Windows: storeWindows(prefs.Windows), Limit: limit, Messages: api.MaxBoardQueueMessages})
	if err != nil {
		return nil, boardErr(err)
	}
	out.Remaining = remaining
	for _, it := range items {
		c := it.Case
		own, err := s.b.boardOwn(ctx, triage[c.AccountID])
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		own = slices.Clone(own)
		sort.Strings(own)
		ac := toAPIBoardCase(c, s.b.boardNow())
		item := apiQueueItem(ac, c.InputKey, own)
		texts := make([]string, len(it.Messages))
		for i, m := range it.Messages {
			texts[i] = m.Text
		}
		ex := board.QueueExcerpts(texts)
		for i, m := range it.Messages {
			qm := queueMessage(m, s.b.boardNow())
			qm.Text, qm.Truncated = ex[i].Text, ex[i].Trimmed || m.TextCut
			item.Messages = append(item.Messages, qm)
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func apiQueueItem(c api.BoardCase, inputKey string, own []string) api.BoardQueueItem {
	return api.BoardQueueItem{
		CaseID: c.ID, AccountID: c.AccountID, InputKey: inputKey,
		RuleState: c.RuleState, RuleReason: c.RuleReason, UserState: c.UserState,
		Subject: c.Subject, ReplyMessageID: c.ReplyMessageID, Issue: c.Issue,
		Own: own, Messages: []api.BoardQueueMessage{}, HasDraft: c.Draft != nil,
	}
}

func queueMessage(m store.BoardMessage, now time.Time) api.BoardQueueMessage {
	qm := api.BoardQueueMessage{MessageID: api.MessageID(m.ID), Date: boardMessageDate(m, now), Mine: m.Mine}
	if len(m.From) > 0 {
		qm.From = board.CleanAddress(m.From[0])
	}
	for _, list := range []struct {
		in  []api.Address
		out *[]api.Address
	}{{m.To, &qm.To}, {m.CC, &qm.Cc}} {
		for i, a := range list.in {
			if i >= boardQueueAddresses {
				break
			}
			*list.out = append(*list.out, board.CleanAddress(a))
		}
	}
	return qm
}

// triageCall is what annotate and commit share: the checks before the
// fields, and the counting of a refusal in the call's run.
type triageCall struct {
	s   *boardService
	c   store.BoardCase
	ref store.BoardRunRef
	// counted: a refusal can be counted in a run — the source is valid
	// (its implicit external run, or the run it names), or the call names
	// an open run.
	counted bool
}

// beginTriage checks the board and the assistant preference (refusals not
// counted: there is no triage to count in), then the case, the account and
// the source, and returns the call; every invalidArgument refusal from
// there on is counted (reject) where a run can be attributed. An unknown
// case is caseNotFound and not counted.
func (s *boardService) beginTriage(ctx context.Context, caseID api.BoardCaseID, runID api.BoardRunID, source string) (*triageCall, error) {
	prefs, err := s.requireAssistant(ctx)
	if err != nil {
		return nil, err
	}
	src, fits := board.CleanLine(source, api.MaxBoardSourceBytes)
	validSource := src != "" && fits
	day, _ := localDay(s.b.boardNow())
	t := &triageCall{s: s, ref: store.BoardRunRef{RunID: string(runID), Source: src, Day: day}, counted: validSource}
	if !validSource && runID != "" {
		// Without a source only the run the call names can count it.
		if r, err := s.b.store.GetBoardRun(ctx, string(runID)); err == nil && r.EndedAt.IsZero() && r.Trigger != api.TriggerExternal {
			t.counted = true
		}
	}
	c, err := s.caseOf(ctx, caseID)
	if err != nil {
		if apiErr := (*api.Error)(nil); errors.As(err, &apiErr) && apiErr.Code == api.CodeInvalidArgument {
			return nil, t.reject(ctx, err)
		}
		return nil, err
	}
	t.c = c
	triage, err := s.b.triageAccounts(ctx, prefs)
	if err != nil {
		return nil, err
	}
	if _, ok := triage[c.AccountID]; !ok {
		return nil, t.reject(ctx, boardInvalid("the case's account is not a triage account"))
	}
	if !validSource {
		return nil, t.reject(ctx, boardInvalid("source must be one line of 1..%d bytes", api.MaxBoardSourceBytes))
	}
	return t, nil
}

// reject counts the refusal in the call's run (when one can be
// attributed) and returns err.
func (t *triageCall) reject(ctx context.Context, err error) error {
	if !t.counted {
		return err
	}
	if _, cerr := t.s.b.store.CountBoardRejected(ctx, t.ref, t.s.b.boardNow()); cerr != nil {
		t.s.b.log.Warn("board: count a refused call", "err", cerr)
	} else {
		t.s.b.notifyBoard(true)
	}
	return err
}

func boardInvalid(format string, args ...any) error {
	return api.NewError(api.CodeInvalidArgument, format, args...)
}

func boardQuoteNotFound(field api.QuoteField) error {
	e := api.NewError(api.CodeQuoteNotFound, "the quote is not in the message's text")
	e.Data = api.QuoteNotFoundData{Field: field}
	return e
}

// memberArrival is the time a deadline found in a member is measured
// from: when it arrived as the rules see it (board.Arrival: the server's
// arrival, else its Date header, never later than when the daemon stored
// it nor than now), so that a forged Date cannot widen the range.
func (s *boardService) memberArrival(ctx context.Context, accountID, id string) (time.Time, error) {
	m, err := s.b.store.GetMessage(ctx, accountID, id)
	if err != nil {
		return time.Time{}, boardErr(err)
	}
	return board.Arrival(board.Member{Date: m.Date, InternalDate: m.InternalDate, StoredAt: m.CreatedAt}, s.b.boardNow()), nil
}

func (s *boardService) Annotate(ctx context.Context, p api.BoardAnnotateParams) (*api.BoardAnnotateResult, error) {
	t, err := s.beginTriage(ctx, p.CaseID, p.RunID, p.Source)
	if err != nil {
		return nil, err
	}
	c := t.c
	if p.InputKey == "" || p.InputKey != c.InputKey {
		return nil, t.reject(ctx, api.NewError(api.CodeConflict, "the case changed since its inputKey was read"))
	}
	in := store.BoardAnnotationInput{CaseID: c.ID, InputKey: p.InputKey, Source: t.ref.Source, Run: t.ref, Now: s.b.boardNow()}
	if p.State != nil {
		if !p.State.Valid() {
			return nil, t.reject(ctx, boardInvalid("unknown state %q", *p.State))
		}
		in.State = *p.State
	}
	var fits [4]bool
	in.Title, fits[0] = board.CleanLine(p.Title, api.MaxBoardTitleBytes)
	in.Why, fits[1] = board.CleanLine(p.Why, api.MaxBoardWhyBytes)
	in.Summary, fits[2] = board.CleanBlock(p.Summary, api.MaxBoardSummaryBytes)
	in.Tasks, fits[3] = board.CleanTasks(p.Tasks)
	for i, f := range fits {
		if !f {
			return nil, t.reject(ctx, boardInvalid("%s is over its limit", [...]string{"title", "why", "summary", "tasks"}[i]))
		}
	}
	if p.Due != nil {
		quote, ok := board.CleanQuote(p.Due.Quote)
		if !ok {
			return nil, t.reject(ctx, boardInvalid("due.quote must be %d..%d bytes with %d characters that are not spaces",
				api.MinBoardQuoteBytes, api.MaxBoardQuoteBytes, api.MinBoardQuoteChars))
		}
		msg := string(p.Due.MessageID)
		counts, _, err := s.b.store.BoardMemberOf(ctx, c.ID, msg)
		if err != nil {
			return nil, boardErr(err)
		}
		if msg == "" || !counts {
			return nil, t.reject(ctx, boardInvalid("due.messageId is not a member of the case"))
		}
		arrived, err := s.memberArrival(ctx, c.AccountID, msg)
		if err != nil {
			return nil, err
		}
		if !board.DueInRange(p.Due.At, arrived) {
			return nil, t.reject(ctx, boardInvalid("due.at is out of range of its message's date"))
		}
		text, _, _, err := s.b.store.GetMessageText(ctx, c.AccountID, msg)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, boardErr(err)
		}
		// The quote as sent (a URL in it matches the same URL in the text);
		// the cleaned one is what the case stores.
		if !board.QuoteIn(text, p.Due.Quote) {
			return nil, t.reject(ctx, boardQuoteNotFound(api.QuoteFieldDue))
		}
		in.Due = &store.BoardDue{At: p.Due.At.UTC(), Quote: quote, MessageID: msg}
	}
	if p.DraftID != "" {
		if err := s.checkDraft(ctx, c, string(p.DraftID)); err != nil {
			return nil, t.reject(ctx, err)
		}
		in.DraftID = string(p.DraftID)
	}
	wasLocal := in.DraftID == "" || s.b.draftLocal(ctx, c.AccountID, in.DraftID)
	stored, _, err := s.b.store.AnnotateBoardCase(ctx, in)
	switch {
	case errors.Is(err, store.ErrBoardConflict):
		return nil, t.reject(ctx, boardErr(err))
	case err != nil:
		return nil, boardErr(err)
	}
	if !wasLocal && stored.DraftID == in.DraftID {
		s.b.draftMadeLocal(c.AccountID)
	}
	s.b.boardWritten(ctx, stored)
	s.b.notifyBoard(true) // the run's counts
	// The case keeps a link to another draft that exists (the user's, or
	// an earlier annotation's): the annotation stands, the draft passed is
	// not linked, and the caller is told.
	return &api.BoardAnnotateResult{Case: toAPIBoardCase(stored, s.b.boardNow()),
		DraftNotLinked: in.DraftID != "" && stored.DraftID != in.DraftID}, nil
}

// draftLocal says whether the account's draft is local already (an
// unknown draft or a failed read: no).
func (b *Backend) draftLocal(ctx context.Context, accountID, draftID string) bool {
	d, err := b.store.GetDraft(ctx, accountID, draftID)
	return err == nil && d.Local
}

// draftMadeLocal is told that a link made a draft of the account local:
// a copy it had in the Drafts folder is a queued delete or a stray copy
// now (a Graph copy waits for the syncer's edit check), and an upload
// that was under way deletes its own copy when it ends; the syncer is
// woken for it, and the wake-up armed for the draft's upload is
// recomputed without it.
func (b *Backend) draftMadeLocal(accountID string) {
	b.triggerDrafts(accountID)
	b.scheduleDraftSync(accountID)
}

// checkDraft: the draft is one of the case's account and replies to a
// message of the case's thread.
func (s *boardService) checkDraft(ctx context.Context, c store.BoardCase, draftID string) error {
	d, err := s.b.store.GetDraft(ctx, c.AccountID, draftID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return boardInvalid("draftId is not a draft of the case's account")
	case err != nil:
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	if d.InReplyTo == "" {
		return boardInvalid("the draft does not reply to a message of the case")
	}
	m, err := s.b.store.GetMessage(ctx, c.AccountID, d.InReplyTo)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return boardInvalid("the draft does not reply to a message of the case")
	case err != nil:
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	if m.ThreadID != c.ThreadID || m.Hidden {
		return boardInvalid("the draft does not reply to a message of the case")
	}
	return nil
}

func (s *boardService) Commit(ctx context.Context, p api.BoardCommitParams) (*api.BoardCommitResult, error) {
	t, err := s.beginTriage(ctx, p.CaseID, p.RunID, p.Source)
	if err != nil {
		return nil, err
	}
	c := t.c
	if p.InputKey == "" || p.InputKey != c.InputKey {
		return nil, t.reject(ctx, api.NewError(api.CodeConflict, "the case changed since its inputKey was read"))
	}
	text, fits := board.CleanLine(p.Text, api.MaxBoardCommitmentTextBytes)
	if text == "" || !fits {
		return nil, t.reject(ctx, boardInvalid("text must be one line of 1..%d bytes", api.MaxBoardCommitmentTextBytes))
	}
	quote, ok := board.CleanQuote(p.Quote)
	if !ok {
		return nil, t.reject(ctx, boardInvalid("quote must be %d..%d bytes with %d characters that are not spaces",
			api.MinBoardQuoteBytes, api.MaxBoardQuoteBytes, api.MinBoardQuoteChars))
	}
	msg := string(p.MessageID)
	counts, mine, err := s.b.store.BoardMemberOf(ctx, c.ID, msg)
	if err != nil {
		return nil, boardErr(err)
	}
	if msg == "" || !counts || !mine {
		return nil, t.reject(ctx, boardInvalid("messageId is not one of the user's messages in the case"))
	}
	body, _, _, err := s.b.store.GetMessageText(ctx, c.AccountID, msg)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, boardErr(err)
	}
	// The user's own text as the rules read it: for a message with HTML,
	// the text of its HTML with the quoted history cut off.
	member := board.Member{Text: body}
	member.OwnText, member.OwnTextSet = s.b.boardOwnText(ctx, c.AccountID, msg)
	if !board.QuoteInOwnText(member, p.Quote) {
		return nil, t.reject(ctx, boardQuoteNotFound(api.QuoteFieldCommitment))
	}
	in := store.BoardCommitmentInput{CaseID: c.ID, InputKey: p.InputKey, MessageID: msg, Text: text, Quote: quote,
		Source: t.ref.Source, Run: t.ref, Now: s.b.boardNow()}
	if p.Due != nil {
		arrived, err := s.memberArrival(ctx, c.AccountID, msg)
		if err != nil {
			return nil, err
		}
		if !board.DueInRange(*p.Due, arrived) {
			return nil, t.reject(ctx, boardInvalid("due is out of range of its message's date"))
		}
		in.Due = p.Due.UTC()
	}
	k, _, err := s.b.store.AddBoardCommitment(ctx, in)
	switch {
	case errors.Is(err, store.ErrBoardConflict), errors.Is(err, store.ErrBoardNotMine):
		return nil, t.reject(ctx, boardErr(err))
	case err != nil:
		return nil, boardErr(err)
	}
	s.b.boardWritten(ctx, c)
	s.b.notifyBoard(true) // the run's counts
	return &api.BoardCommitResult{Commitment: toAPICommitment(k)}, nil
}

func (s *boardService) SetCommitment(ctx context.Context, p api.BoardSetCommitmentParams) (*api.BoardSetCommitmentResult, error) {
	if _, err := s.requireBoard(ctx); err != nil {
		return nil, err
	}
	if p.CommitmentID == "" {
		return nil, boardInvalid("commitmentId is required")
	}
	k, err := s.b.store.SetBoardCommitment(ctx, string(p.CommitmentID), p.Done, s.b.boardNow())
	if err != nil {
		return nil, boardErr(err)
	}
	if c, err := s.b.store.GetBoardCase(ctx, k.CaseID); err == nil {
		s.b.boardWritten(ctx, c)
	} else {
		s.b.notifyBoard(false, k.AccountID)
	}
	return &api.BoardSetCommitmentResult{Commitment: toAPICommitment(k)}, nil
}

// --- preferences -------------------------------------------------------------

func (s *boardService) Preferences(ctx context.Context, _ api.BoardPreferencesParams) (*api.BoardPreferencesResult, error) {
	p, err := s.b.boardPrefs(ctx)
	if err != nil {
		return nil, err
	}
	return &api.BoardPreferencesResult{Preferences: p}, nil
}

func (s *boardService) SetPreferences(ctx context.Context, p api.BoardSetPreferencesParams) (*api.BoardSetPreferencesResult, error) {
	next := p.Preferences
	w := next.Windows
	for _, d := range []int{w.Hot, w.You, w.Them, w.Info} {
		if d < 1 || d > api.MaxBoardWindowDays {
			return nil, boardInvalid("a window must be 1..%d days", api.MaxBoardWindowDays)
		}
	}
	if next.AutoTriageMinutes < api.MinBoardAutoTriageMinutes || next.AutoTriageMinutes > api.MaxBoardAutoTriageMinutes {
		return nil, boardInvalid("autoTriageMinutes must be %d..%d", api.MinBoardAutoTriageMinutes, api.MaxBoardAutoTriageMinutes)
	}
	if next.AutoTriageDailyCases < 0 || next.AutoTriageDailyCases > api.MaxBoardAutoTriageDailyCases {
		return nil, boardInvalid("autoTriageDailyCases must be 0..%d", api.MaxBoardAutoTriageDailyCases)
	}
	known := map[string]bool{}
	list, err := s.b.store.ListAccounts(ctx)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	for _, a := range list {
		known[a.ID] = true
	}
	triage := []api.AccountID{}
	for _, id := range next.TriageAccounts {
		if !known[string(id)] {
			return nil, boardInvalid("unknown account %q in triageAccounts", id)
		}
		if !slices.Contains(triage, id) {
			triage = append(triage, id)
		}
	}
	next.TriageAccounts = triage
	prev, err := s.b.boardPrefs(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return nil, api.NewError(api.CodeInternalError, "%v", err)
	}
	if err := s.b.store.SetMeta(ctx, metaBoardPrefs, string(raw)); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if next.Enabled && (!prev.Enabled || maxBoardWindow(next.Windows) > maxBoardWindow(prev.Windows)) {
		// Turned on, or a window reaches further back: the stored mail is
		// evaluated again.
		s.b.restartBoardBackfill(ctx)
	}
	s.b.notifyBoard(true)
	s.b.wakeBoard()
	return &api.BoardSetPreferencesResult{Preferences: next}, nil
}

// --- runs --------------------------------------------------------------------

// boardUsageWindow is the window of BoardTriage.Usage24h.
const boardUsageWindow = 24 * time.Hour

func (s *boardService) RunStart(ctx context.Context, p api.BoardRunStartParams) (*api.BoardRunStartResult, error) {
	if _, err := s.requireBoard(ctx); err != nil {
		return nil, err
	}
	if p.Trigger != api.TriggerManual && p.Trigger != api.TriggerAuto {
		return nil, boardInvalid("trigger must be manual or auto")
	}
	src, fits := board.CleanLine(p.Source, api.MaxBoardSourceBytes)
	if src == "" || !fits {
		return nil, boardInvalid("source must be one line of 1..%d bytes", api.MaxBoardSourceBytes)
	}
	id, err := s.b.store.StartBoardRun(ctx, p.Trigger, src, s.b.boardNow())
	if err != nil {
		return nil, boardErr(err)
	}
	s.b.notifyBoard(true)
	return &api.BoardRunStartResult{RunID: api.BoardRunID(id)}, nil
}

func (s *boardService) RunEnd(ctx context.Context, p api.BoardRunEndParams) (*api.BoardRunEndResult, error) {
	if _, err := s.requireBoard(ctx); err != nil {
		return nil, err
	}
	if p.RunID == "" {
		return nil, boardInvalid("runId is required")
	}
	if p.Usage != nil && !p.Usage.Valid() {
		return nil, boardInvalid("usage counters must not be negative")
	}
	err := s.b.store.EndBoardRun(ctx, string(p.RunID), p.Error, p.Usage, s.b.boardNow())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, boardInvalid("unknown run %q", p.RunID)
	case err != nil:
		return nil, boardErr(err)
	}
	s.b.notifyBoard(true)
	return &api.BoardRunEndResult{}, nil
}
