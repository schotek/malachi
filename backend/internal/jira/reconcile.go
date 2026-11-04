// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Reconciliation. The incremental search sees what changed; it cannot see
// what went: an issue deleted, moved out of the selected spaces, or (with
// OnlyMine) no longer the user's, nor a change that leaves an issue's
// updated time alone (watching it). Hourly, and on a full pass, the
// syncer therefore enumerates the ids in scope — the window, the open
// issues assigned to the user whatever their age, the watched ones — and
// compares them with the store:
//
//   - an id in scope the store lacks, or whose watching differs, is
//     refreshed;
//   - a stored issue the window's enumeration should have named and did
//     not (or an old open one assigned to the user that its enumeration
//     did not name) is fetched directly: gone or no longer visible, in a
//     space no longer selected, or (OnlyMine, unless a notification mail
//     named it) no longer the user's, it is deleted; else (the index
//     lags) it is refreshed.
//
// Nothing is concluded from an enumeration that stopped at its cap
// (complete false): a missing id there proves nothing.
//
// Retention, on every pass: an issue last updated before the window (and
// a day's grace) is deleted, unless it is open and assigned to the user —
// at most maxOpenAssigned of those, the most recently updated — which
// stay whatever their age.

func (s *Syncer) reconcile(ctx context.Context, p *pass) error {
	if len(p.spaceFolders) == 0 {
		return nil
	}
	spaces := make([]string, 0, len(p.spaceFolders))
	for id := range p.spaceFolders {
		spaces = append(spaces, id)
	}
	window := `updated >= "-` + strconv.Itoa(p.days) + `d"`
	inWindow, completeWindow, err := AllIDs(ctx, s.deps.Remote, p.scope(spaces)+" AND "+window, 0)
	if err != nil {
		return err
	}
	openMine, completeOpen, err := AllIDs(ctx, s.deps.Remote, p.spaces(spaces)+" AND assignee = currentUser() AND statusCategory != Done", maxOpenAssigned)
	if err != nil {
		return err
	}
	watched, completeWatched, err := AllIDs(ctx, s.deps.Remote, p.scope(spaces)+" AND watcher = currentUser() AND "+window, 0)
	if err != nil {
		return err
	}
	stamps, err := s.deps.Store.ListIssueStamps(ctx, s.account.ID)
	if err != nil {
		return storageError(err)
	}
	ids := make([]string, 0, len(stamps))
	for id := range stamps {
		ids = append(ids, id)
	}
	stored, err := s.deps.Store.IssuesByID(ctx, s.account.ID, ids)
	if err != nil {
		return storageError(err)
	}

	present := map[string]bool{}
	for _, id := range inWindow {
		present[id] = true
	}
	for _, id := range openMine {
		present[id] = true
	}
	isWatched := map[string]bool{}
	for _, id := range watched {
		isWatched[id] = true
	}
	windowStart := p.start.Add(-time.Duration(p.days) * 24 * time.Hour)

	var refresh, suspects []string
	for id := range present {
		if _, ok := stored[id]; !ok && !p.isClaimed(id) {
			refresh = append(refresh, id)
		}
	}
	for id, is := range stored {
		if p.isClaimed(id) {
			continue
		}
		if _, visible := p.spaceFolders[is.SpaceID]; !visible {
			continue // a selected space the site does not list now
		}
		if present[id] {
			if completeWatched && !is.Updated.Before(windowStart) && is.Watching != isWatched[id] {
				refresh = append(refresh, id)
			}
			continue
		}
		switch {
		case !is.Updated.Before(windowStart):
			if completeWindow {
				suspects = append(suspects, id)
			}
		case s.openAssigned(is):
			if completeOpen {
				suspects = append(suspects, id)
			}
		}
	}

	if len(suspects) > 0 {
		sort.Slice(suspects, func(i, j int) bool { return idLess(suspects[i], suspects[j]) })
		var drop []string
		for len(suspects) > 0 {
			chunk := suspects[:min(bulkChunk, len(suspects))]
			suspects = suspects[len(chunk):]
			got, err := s.deps.Remote.BulkIssues(ctx, chunk, IssueOptions{Fields: FieldsAll})
			if err != nil {
				return err
			}
			byID := map[string]Issue{}
			for _, is := range got {
				byID[is.ID] = is
			}
			for _, id := range chunk {
				is, ok := byID[id]
				switch {
				case !ok:
					drop = append(drop, id)
				case !p.selected(is.SpaceID):
					drop = append(drop, id)
				case p.cfg.OnlyMine && !stored[id].ViaMail && !s.stillMine(ctx, p, is):
					drop = append(drop, id)
				default:
					refresh = append(refresh, id)
				}
			}
		}
		if len(drop) > 0 {
			s.log.Info("jira issues gone from scope", "count", len(drop))
			if err := s.forget(ctx, p, drop...); err != nil {
				return err
			}
		}
	}
	sort.Slice(refresh, func(i, j int) bool { return idLess(refresh[i], refresh[j]) })
	return s.fetchAndMaterialise(ctx, p, refresh, nil)
}

// selected reports a space of the configuration.
func (p *pass) selected(spaceID string) bool {
	_, ok := p.allSpaceFolders[spaceID]
	return ok
}

// stillMine is OnlyMine's test for an issue the enumeration no longer
// names: the user reports it, is assigned or watches it, or a stored
// comment or event of the user's is within the window.
func (s *Syncer) stillMine(ctx context.Context, p *pass, is Issue) bool {
	me := s.me.ID
	if me == "" || is.Reporter.ID == me || is.Assignee.ID == me || is.Watching {
		return true
	}
	items, err := s.deps.Store.IssueItems(ctx, s.account.ID, is.ID)
	if err != nil {
		return true // in doubt, keep it
	}
	since := p.start.Add(-time.Duration(p.days) * 24 * time.Hour)
	for _, it := range items {
		if it.Kind != api.IssueItemDescription && it.AuthorID == me && !it.Updated.Before(since) {
			return true
		}
	}
	return false
}

// openAssigned reports a stored issue assigned to the user and not done.
func (s *Syncer) openAssigned(is store.Issue) bool {
	return s.me.ID != "" && is.AssigneeID == s.me.ID && is.StatusCategory != api.StatusCategoryDone
}

// retain deletes the issues older than the window, but for the open ones
// assigned to the user (the newest maxOpenAssigned of them).
func (s *Syncer) retain(ctx context.Context, p *pass) error {
	cutoff := p.start.Add(-time.Duration(p.days)*24*time.Hour - retentionGrace)
	old, err := s.deps.Store.IssuesUpdatedBefore(ctx, s.account.ID, cutoff)
	if err != nil {
		return storageError(err)
	}
	if len(old) == 0 {
		return nil
	}
	stored, err := s.deps.Store.IssuesByID(ctx, s.account.ID, old)
	if err != nil {
		return storageError(err)
	}
	var keep []store.Issue
	for _, is := range stored {
		if s.openAssigned(is) {
			keep = append(keep, is)
		}
	}
	// The newest open ones stay, within the cap (counting those in the
	// window too, which the backfill fetched under the same cap).
	sort.Slice(keep, func(i, j int) bool {
		if !keep[i].Updated.Equal(keep[j].Updated) {
			return keep[i].Updated.After(keep[j].Updated)
		}
		return idLess(keep[j].IssueID, keep[i].IssueID)
	})
	kept := map[string]bool{}
	for i, is := range keep {
		if i == maxOpenAssigned {
			break
		}
		kept[is.IssueID] = true
	}
	var drop []string
	for _, id := range old {
		if !kept[id] && !p.isClaimed(id) {
			drop = append(drop, id)
		}
	}
	if len(drop) == 0 {
		return nil
	}
	s.log.Info("jira issues left the window", "count", len(drop))
	return s.forget(ctx, p, drop...)
}
