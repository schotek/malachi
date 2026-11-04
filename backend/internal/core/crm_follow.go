// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/eway"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Following a saved conversation (docs/eway.md D22): once the user saved a
// message of a conversation to an eWay-CRM account whose followThreads is
// on, the later messages of that conversation in the same mail account
// are saved by the daemon with the same links, superior item and privacy.
// Three parts: the Stored hook of the mail syncers only enqueues (one
// store query, never network); a worker per CRM account drains its queue
// serially through crm.save's path; an hourly sweep catches up what the
// hook missed (offline time, failures). It is the only automatic save.

const (
	// crmFollowTTL is how long the list of following accounts is reused
	// (invalidated on account changes too).
	crmFollowTTL = 30 * time.Second
	// crmFollowQueueMax bounds one account's queue; what does not fit is
	// left to the sweep.
	crmFollowQueueMax = 1000
	// crmFollowJobTimeout bounds one automatic save.
	crmFollowJobTimeout = 3 * time.Minute
	// crmFollowSweepSaves bounds the saves of one sweep, crmFollowWindow
	// how far back the sweep looks, crmFollowThreads how many threads.
	crmFollowSweepSaves = 50
	crmFollowWindow     = 90 * 24 * time.Hour
	crmFollowThreads    = 500
	// crmFollowNotWholeMax bounds the keys logged once as not whole.
	crmFollowNotWholeMax = 10000
)

type crmFollowJob struct{ mail, msg string }

type crmFollowQueue struct {
	jobs    []crmFollowJob
	queued  map[crmFollowJob]bool
	running bool
}

// crmFollow is the state of following: the cached list of following CRM
// accounts and the queue of each.
type crmFollow struct {
	mu       sync.Mutex
	accounts []string
	loadedAt time.Time
	loaded   bool
	queues   map[string]*crmFollowQueue
	notWhole map[string]bool
	// idle is signalled whenever a worker finishes a job (tests).
	idle chan struct{}
}

func (f *crmFollow) invalidate() {
	f.mu.Lock()
	f.loaded = false
	f.mu.Unlock()
}

// following reports the enabled eway accounts with followThreads on.
func (b *Backend) crmFollowing(ctx context.Context) []string {
	f := &b.crm.follow
	f.mu.Lock()
	if f.loaded && time.Since(f.loadedAt) < crmFollowTTL {
		out := f.accounts
		f.mu.Unlock()
		return out
	}
	f.mu.Unlock()
	list, err := b.store.ListAccounts(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range list {
		if crmFollows(a) {
			out = append(out, a.ID)
		}
	}
	f.mu.Lock()
	f.accounts, f.loadedAt, f.loaded = out, time.Now(), true
	f.mu.Unlock()
	return out
}

func crmFollows(a store.Account) bool {
	return isCrmAccount(a) && a.Enabled && a.Config.Eway != nil && a.Config.Eway.FollowThreads
}

// noteCrmFollow is the Stored hook's part: when the message's conversation
// is followed by a CRM account, the message is queued for it. Without a
// following account it costs nothing; with one, one store query.
func (b *Backend) noteCrmFollow(ctx context.Context, accountID, messageID string) {
	crms := b.crmFollowing(ctx)
	if len(crms) == 0 {
		return
	}
	by, err := b.store.CrmFollowedBy(ctx, accountID, messageID, crms)
	if err != nil {
		if !isCancelled(err) {
			b.log.Warn("crm follow: thread lookup", "err", err)
		}
		return
	}
	for _, crm := range by {
		b.crmEnqueue(crm, crmFollowJob{mail: accountID, msg: messageID})
	}
}

// crmEnqueue queues a job and starts the account's worker when none runs.
func (b *Backend) crmEnqueue(crmID string, job crmFollowJob) {
	f := &b.crm.follow
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.queues == nil {
		f.queues = map[string]*crmFollowQueue{}
	}
	q := f.queues[crmID]
	if q == nil {
		q = &crmFollowQueue{queued: map[crmFollowJob]bool{}}
		f.queues[crmID] = q
	}
	if q.queued[job] || len(q.jobs) >= crmFollowQueueMax {
		return
	}
	q.queued[job] = true
	q.jobs = append(q.jobs, job)
	if !q.running {
		q.running = true
		go b.crmFollowWorker(crmID)
	}
}

// crmFollowNext takes the next job, or ends the worker (running false)
// when the queue is empty or drop is set.
func (b *Backend) crmFollowNext(crmID string, drop bool) (crmFollowJob, bool) {
	f := &b.crm.follow
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.queues[crmID]
	if q == nil {
		return crmFollowJob{}, false
	}
	if drop || len(q.jobs) == 0 {
		q.jobs, q.queued, q.running = nil, map[crmFollowJob]bool{}, false
		return crmFollowJob{}, false
	}
	j := q.jobs[0]
	q.jobs = q.jobs[1:]
	delete(q.queued, j)
	return j, true
}

func (b *Backend) crmFollowSignal() {
	f := &b.crm.follow
	f.mu.Lock()
	ch := f.idle
	f.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// crmFollowWorker drains one CRM account's queue serially. It ends when
// the queue is empty, and drops it when the account no longer follows
// (paused, removed, switched off) or its client was closed.
func (b *Backend) crmFollowWorker(crmID string) {
	drop := false
	for {
		job, ok := b.crmFollowNext(crmID, drop)
		if !ok {
			b.crmFollowSignal()
			return
		}
		crm, err := b.store.GetAccount(context.Background(), crmID)
		if err != nil || !crmFollows(crm) {
			drop = true
			continue
		}
		c, err := b.crmClient(crm)
		if err != nil || c.Context().Err() != nil {
			drop = true
			continue
		}
		ctx, cancel := context.WithTimeout(c.Context(), crmFollowJobTimeout)
		_, err = b.crmFollowOne(ctx, crm, c, job.mail, job.msg)
		cancel()
		if err != nil {
			b.crmFollowFailed(err)
		}
		if c.Context().Err() != nil {
			drop = true
		}
		b.crmFollowSignal()
	}
}

func (b *Backend) crmFollowFailed(err error) {
	code := api.CodeServerError
	var ae *api.Error
	if errors.As(err, &ae) {
		code = ae.Code
	}
	b.log.Info("crm follow: message not saved, the sweep retries", "code", code.String())
}

// crmFollowOne saves one message of a followed conversation, when it still
// qualifies (D22): a mail account's message, the conversation followed by
// the CRM account, stored after the first save, in a folder that counts,
// not saved yet, and whole on this device (else it is skipped and logged
// once). The template's links, superior item and privacy are copied; an
// item of the template gone from the CRM is left out. true when saved.
func (b *Backend) crmFollowOne(ctx context.Context, crm store.Account, c *eway.Client, mailID, msgID string) (bool, error) {
	mail, err := b.store.GetAccount(ctx, mailID)
	if err != nil {
		return false, nil // removed meanwhile
	}
	if k := mail.Config.Protocol(); (k != api.AccountIMAP && k != api.AccountGraph) || !mail.Enabled {
		return false, nil
	}
	m, err := b.store.GetMessage(ctx, mailID, msgID)
	if err != nil {
		return false, nil
	}
	tpl, ok, err := b.store.CrmFollowedThreadTemplate(ctx, mailID, m.ThreadID, crm.ID)
	if err != nil || !ok {
		return false, err
	}
	if ok, err := b.store.CrmFollowable(ctx, mailID, m.ID, crm.ID, tpl.FirstSaved); err != nil || !ok {
		return false, err
	}
	key := store.CrmMessageKey(m)
	if _, whole := b.crmWholeSize(ctx, mailID, m); !whole {
		b.crmFollowNotWhole(crm.ID + "\x00" + mailID + "\x00" + key)
		return false, nil
	}
	busy := []string{crmBusyKey(crm.ID, mailID, key)}
	if !b.crm.claim(busy) {
		return false, nil // a manual save or a check runs; the sweep comes back
	}
	defer b.crm.release(busy)
	links, err := b.crmTemplateLinks(ctx, c, tpl.Link)
	if err != nil {
		return false, err
	}
	if _, _, aerr := b.crmSaveOne(ctx, crm, c, mail, m, nil, links, tpl.Link.Private, true); aerr != nil {
		return false, aerr
	}
	c.ForgetCache()
	b.crmChanged(ctx, mailID, []string{key})
	b.log.Info("crm follow: message saved")
	return true, nil
}

// crmTemplateLinks are the template's relations as links: the superior
// item as superior, the rest as plain links, an item gone from the CRM left
// out.
func (b *Backend) crmTemplateLinks(ctx context.Context, c *eway.Client, tpl store.CrmLink) (eway.Links, error) {
	byKind := map[api.CrmItemKind][]string{}
	for _, r := range tpl.Relations {
		if eway.FolderOf(r.Kind) != "" && eway.ValidGUID(r.GUID) {
			byKind[r.Kind] = append(byKind[r.Kind], r.GUID)
		}
	}
	exists := map[string]bool{}
	for kind, guids := range byKind {
		items, err := c.Items(ctx, kind, guids)
		if err != nil {
			return eway.Links{}, eway.ToAPIError(err)
		}
		for _, it := range items {
			exists[it.GUID] = true
		}
	}
	var l eway.Links
	for _, r := range tpl.Relations {
		ref := eway.Ref{Kind: r.Kind, GUID: r.GUID}
		if !exists[r.GUID] {
			continue
		}
		if r.Superior && (r.Kind == api.CrmDeal || r.Kind == api.CrmProject) && l.Superior == nil {
			l.Superior = &ref
			continue
		}
		l.Link = append(l.Link, ref)
	}
	return l, nil
}

func (b *Backend) crmFollowNotWhole(key string) {
	f := &b.crm.follow
	f.mu.Lock()
	if f.notWhole == nil || len(f.notWhole) >= crmFollowNotWholeMax {
		f.notWhole = map[string]bool{}
	}
	first := !f.notWhole[key]
	f.notWhole[key] = true
	f.mu.Unlock()
	if first {
		b.log.Info("crm follow: a message is not whole on this device; it is saved by hand or after its download")
	}
}

// crmFollowSweep is the hourly catch-up (core.Maintain): for every enabled
// eway account with followThreads on, the conversations it followed in the
// last 90 days, at most crmFollowSweepSaves saves per sweep, serially, on
// the account's client (stopped with it).
func (b *Backend) crmFollowSweep(ctx context.Context) {
	b.crm.follow.invalidate()
	saves := 0
	for _, crmID := range b.crmFollowing(ctx) {
		if saves >= crmFollowSweepSaves || ctx.Err() != nil {
			return
		}
		crm, err := b.store.GetAccount(ctx, crmID)
		if err != nil || !crmFollows(crm) {
			continue
		}
		c, err := b.crmClient(crm)
		if err != nil {
			continue
		}
		saves += b.crmFollowSweepAccount(ctx, crm, c, crmFollowSweepSaves-saves)
	}
}

func (b *Backend) crmFollowSweepAccount(ctx context.Context, crm store.Account, c *eway.Client, budget int) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(c.Context(), cancel)
	defer stop()
	threads, err := b.store.CrmFollowedThreads(ctx, crm.ID, time.Now().Add(-crmFollowWindow), crmFollowThreads)
	if err != nil {
		if !isCancelled(err) {
			b.log.Warn("crm follow sweep", "err", err)
		}
		return 0
	}
	saves := 0
	for _, t := range threads {
		if saves >= budget || ctx.Err() != nil {
			break
		}
		tpl, ok, err := b.store.CrmFollowedThreadTemplate(ctx, t.MailAccountID, t.ThreadID, crm.ID)
		if err != nil || !ok {
			continue
		}
		ids, err := b.store.CrmUnsavedMembers(ctx, t.MailAccountID, t.ThreadID, crm.ID, tpl.FirstSaved, budget-saves)
		if err != nil {
			continue
		}
		for _, id := range ids {
			jctx, jcancel := context.WithTimeout(ctx, crmFollowJobTimeout)
			ok, err := b.crmFollowOne(jctx, crm, c, t.MailAccountID, id)
			jcancel()
			if err != nil {
				b.crmFollowFailed(err)
				continue
			}
			if ok {
				saves++
			}
		}
	}
	if saves > 0 {
		b.log.Info("crm follow sweep", "saved", saves)
	}
	return saves
}
