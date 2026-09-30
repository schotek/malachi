// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/jira"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Notification mail of an issue-tracker site in the user's mail accounts
// (docs/api.md §4.1 notificationMail, docs/security.md §4.1).
//
// A Jira site tells its users of every change by e-mail. When such a
// message is stored in a mail account (the syncers' Stored hook, before
// the message is announced), noteIssueMail asks every enabled jira
// account whose notificationMail is not "ignore", in the accounts' order,
// whether the message is one of its site about an issue of a selected
// space (jira.MatchNotification); the first that says so gets
//
//   - a link from the message to the issue (store.LinkIssueMail), which
//     also keeps the issue under onlyMine;
//   - a refresh of the issue: waited for, briefly, when the message is
//     fresh and the issue not stored, so that what follows applies before
//     the message is announced; else asked for (TriggerIssue), unless
//     nothing can come of it: the message is older than the account's
//     window, the stored issue is newer than the message, or the issue
//     was asked for a moment ago and the site did not give it;
//   - with notificationMail "hide", and only when the issue is stored in
//     the account: the message hidden (messages.hidden, a display filter:
//     nothing changes on the mail server, no flag is touched).
//
// What is hidden follows the accounts from then on. Whenever the
// issue-tracker syncer stores something, the linked mail of the account
// is hidden or shown as its issues are stored or not (applyIssueMail).
// When the account's configuration changes, it is enabled or paused, its
// first pass with a configuration ends, and hourly (Maintain), the links
// are judged again (settleIssueMail): with anything but "hide" on an
// enabled account the linked mail is shown; else links the matcher no
// longer agrees with go, and their mail is shown. After a change, the
// mail stored within the account's window from its senders is looked
// through for notifications that arrived before (scanIssueMail). An
// account removed shows its mail again (account.remove).
//
// Every message hidden or shown is told to the clients by
// notify.messagesChanged, gathered per mail account; a hidden message is
// never announced (notify.newMessage).

// The pace and the bounds; variables so that tests can change them.
var (
	// issueMailFresh: a message that arrived within this is news, and its
	// issue refreshed before the message is announced.
	issueMailFresh = 15 * time.Minute
	// issueMailWait is how long that refresh may hold the mail syncer.
	issueMailWait = 5 * time.Second
	// issueMailWaits is how many such waits a minute may bring, and
	// issueMailAskAgain how long an issue that was asked for (or waited
	// for) and is still not stored is left alone: mail about an issue the
	// site does not give arrives no slower than other mail, and costs the
	// site one request, not one a message. issueMailAsked bounds what is
	// remembered for that.
	issueMailWaits    = 6
	issueMailAskAgain = 10 * time.Minute
	issueMailAsked    = 4096
	// issueMailDebounce gathers what an issue-tracker pass stores into
	// one evaluation of the account's links.
	issueMailDebounce = 500 * time.Millisecond
	// issueMailCoalesce gathers notify.messagesChanged per account.
	issueMailCoalesce = 250 * time.Millisecond
	// issueMailBatch is how many messages or links one step of a scan
	// reads, issueMailPause the rest after it, issueMailScanLimit how many
	// messages one scan reads at most and issueMailTriggerLimit how many
	// issues it asks the syncer for.
	issueMailBatch        = 200
	issueMailPause        = 20 * time.Millisecond
	issueMailScanLimit    = 50000
	issueMailTriggerLimit = 200
	// issueTrackerTTL is how long the accounts read for the matcher are
	// used (an account change ends it at once).
	issueTrackerTTL = 30 * time.Second
)

// metaIssueMailSettled + account id records the configuration the
// account's links were last judged for (issueMailState).
const metaIssueMailSettled = store.MetaIssueMailPrefix

// issueTracker is a jira account as the matcher needs it.
type issueTracker struct {
	id     string
	cfg    api.JiraConfig
	spaces []store.IssueSpace
	hide   bool
	days   int
}

// issueMailJob is work queued for an issue-tracker account.
type issueMailJob struct {
	settle bool // judge the links again (else only apply the stored issues)
	scan   bool // and look through the stored mail
	due    time.Time
}

// issueMail is the state of the notification-mail feature, under mu.
type issueMail struct {
	mu sync.Mutex

	// The accounts read for the matcher.
	trackers   []issueTracker
	trackerIDs map[string]bool // every jira account, enabled or not
	loaded     bool
	loadedAt   time.Time
	// settled are the accounts known to be judged for their
	// configuration, scanning those a job with a scan is queued or runs
	// for (how many).
	settled  map[string]bool
	scanning map[string]int

	// Waits for a refresh (when), and the issues that were asked for
	// while they were not stored (account and key → when).
	waits []time.Time
	asked map[string]time.Time

	// notify.messagesChanged not yet sent: mail account → folders ("" =
	// any).
	changed map[string]map[string]bool
	timer   *time.Timer

	// The worker and its queue; without a worker (before StartSync) a job
	// runs where it is asked for.
	running bool
	stopped bool
	jobs    map[string]issueMailJob
	wake    chan struct{}
}

func (im *issueMail) init() {
	im.settled = map[string]bool{}
	im.scanning = map[string]int{}
	im.asked = map[string]time.Time{}
	im.changed = map[string]map[string]bool{}
	im.jobs = map[string]issueMailJob{}
	im.wake = make(chan struct{}, 1)
}

// afterMailStored is the Stored hook of the mail syncers (IMAP, Graph).
func (b *Backend) afterMailStored(ctx context.Context, accountID, messageID string, pol ingest.Policy) {
	b.storedUnder(ctx, messageID, pol)
	b.noteIssueMail(ctx, accountID, messageID)
}

// afterIssueStored is the Stored hook of the issue-tracker syncer: the
// account's linked mail is to follow what is stored now.
func (b *Backend) afterIssueStored(ctx context.Context, accountID, messageID string, pol ingest.Policy) {
	b.storedUnder(ctx, messageID, pol)
	for _, tr := range b.issueTrackers(ctx) {
		if tr.id == accountID && tr.hide {
			b.kickIssueMail(ctx, accountID, issueMailJob{}, issueMailDebounce)
			return
		}
	}
}

// invalidateIssueTrackers forgets the accounts read for the matcher: an
// account changed.
func (b *Backend) invalidateIssueTrackers() {
	b.im.mu.Lock()
	b.im.loaded = false
	b.im.settled = map[string]bool{}
	b.im.mu.Unlock()
}

// issueTrackers are the enabled jira accounts that take notification
// mail, in the accounts' order, read from the store at most every
// issueTrackerTTL.
func (b *Backend) issueTrackers(ctx context.Context) []issueTracker {
	now := time.Now()
	b.im.mu.Lock()
	if b.im.loaded && now.Sub(b.im.loadedAt) < issueTrackerTTL {
		out := b.im.trackers
		b.im.mu.Unlock()
		return out
	}
	b.im.mu.Unlock()
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		if !isCancelled(err) {
			b.log.Warn("read the accounts for notification mail", "err", err)
		}
		return nil
	}
	var trackers []issueTracker
	ids := map[string]bool{}
	for _, a := range accounts {
		if !isIssueAccount(a) || a.Config.Jira == nil {
			continue
		}
		ids[a.ID] = true
		tr, ok := b.issueTrackerOf(ctx, a)
		if ok {
			trackers = append(trackers, tr)
		}
	}
	b.im.mu.Lock()
	b.im.trackers, b.im.trackerIDs, b.im.loaded, b.im.loadedAt = trackers, ids, true, now
	b.im.mu.Unlock()
	return trackers
}

// issueTrackerOf reads what the matcher needs of a jira account; false
// for one that takes no notification mail (paused, "ignore", no sender to
// know it by).
func (b *Backend) issueTrackerOf(ctx context.Context, a store.Account) (issueTracker, bool) {
	if !isIssueAccount(a) || a.Config.Jira == nil || !a.Enabled {
		return issueTracker{}, false
	}
	cfg := *a.Config.Jira
	if cfg.NotificationMail == api.NotificationMailIgnore || len(jira.NotificationSenders(cfg)) == 0 {
		return issueTracker{}, false
	}
	spaces, err := b.store.IssueSpaces(ctx, a.ID)
	if err != nil {
		if !isCancelled(err) {
			b.log.Warn("read the spaces for notification mail", "account", a.ID, "err", err)
		}
		return issueTracker{}, false
	}
	return issueTracker{id: a.ID, cfg: cfg, spaces: spaces,
		hide: cfg.NotificationMail == api.NotificationMailHide, days: jira.OfflineDays(cfg)}, true
}

// noteIssueMail judges a message just stored in a mail account (see the
// file comment). It never fails: a message that cannot be judged is an
// ordinary message.
func (b *Backend) noteIssueMail(ctx context.Context, mailAccountID, messageID string) {
	trackers := b.issueTrackers(ctx)
	if len(trackers) == 0 {
		return
	}
	b.im.mu.Lock()
	tracker := b.im.trackerIDs[mailAccountID]
	b.im.mu.Unlock()
	if tracker {
		return // an issue tracker's own messages are nobody's notifications
	}
	m, err := b.store.GetMessage(ctx, mailAccountID, messageID)
	if err != nil {
		return
	}
	b.matchIssueMail(ctx, trackers, m, true, nil)
}

// matchIssueMail asks the trackers in order about the message and links
// it to the first that knows it (linkIssueMail). arriving says that the
// message is being received, so that a fresh one may wait for its issue;
// asked, when not nil, collects the issues the syncers were asked for and
// bounds them (issueMailTriggerLimit).
func (b *Backend) matchIssueMail(ctx context.Context, trackers []issueTracker, m store.Message, arriving bool, asked map[string]bool) bool {
	for _, tr := range trackers {
		key, ok := jira.MatchNotification(tr.cfg, tr.spaces, m.From, m.Subject)
		if !ok {
			continue
		}
		b.linkIssueMail(ctx, tr, m, key, arriving, asked)
		return true
	}
	return false
}

// mailTime is when a message arrived: the server's date of arrival, else
// its Date header.
func mailTime(m store.Message) time.Time {
	if !m.InternalDate.IsZero() {
		return m.InternalDate
	}
	return m.Date
}

// linkIssueMail links a notification to its issue, refreshes the issue
// and hides the message when the account hides its notifications and the
// issue is stored; a message hidden before (for another issue, by
// another account) that is not to be hidden now is shown.
func (b *Backend) linkIssueMail(ctx context.Context, tr issueTracker, m store.Message, key string, arriving bool, asked map[string]bool) {
	issue, stored, err := b.storedIssue(ctx, tr.id, key)
	if err != nil {
		return
	}
	if err := b.store.LinkIssueMail(ctx, m.ID, tr.id, key, issue.IssueID); err != nil {
		if !errors.Is(err, store.ErrNotFound) && !isCancelled(err) {
			b.log.Warn("link a notification mail", "account", tr.id, "message", m.ID, "err", err)
		}
		return
	}
	when, now := mailTime(m), time.Now()
	age := now.Sub(when)
	if sup, ok := b.Supervisor.(IssueSupervisor); ok && !when.IsZero() {
		window := time.Duration(tr.days) * 24 * time.Hour
		switch {
		case age > window:
			// The issue left the account's window with the message.
		case stored && !issue.SyncedUpdated.Before(when):
			// What is stored is newer than the message.
		case !stored && !b.allowIssueAsk(tr.id, key, now):
			// Asked for a moment ago, and the site did not give it.
		case !stored && arriving && age <= issueMailFresh && age >= -issueMailFresh && b.allowIssueWait(now):
			if err := sup.RefreshIssueWait(ctx, tr.id, key, issueMailWait); err != nil {
				// Out of time, the refresh stays asked for, and what it
				// brings is applied when it is stored; refused (no syncer
				// runs), the next message asks again.
				if code := codeOf(err); code != api.CodeServerTimeout {
					b.forgetIssueAsk(tr.id, key)
				}
				b.log.Debug("the issue of a notification mail did not arrive in time", "account", tr.id, "code", codeOf(err))
			}
			if issue, stored, err = b.storedIssue(ctx, tr.id, key); err != nil {
				return
			}
			if stored {
				// The link learns the issue, and the issue that a mail
				// named it, whatever the syncer made of it.
				if err := b.store.LinkIssueMail(ctx, m.ID, tr.id, key, issue.IssueID); err != nil && !errors.Is(err, store.ErrNotFound) && !isCancelled(err) {
					b.log.Warn("link a notification mail", "account", tr.id, "message", m.ID, "err", err)
				}
			}
		case asked != nil && (asked[key] || len(asked) >= issueMailTriggerLimit):
		default:
			switch {
			case !sup.TriggerIssue(tr.id, key):
				b.forgetIssueAsk(tr.id, key) // no syncer runs: the next message asks again
			case asked != nil:
				asked[key] = true
			}
		}
	}
	hide := tr.hide && stored
	if !hide && !m.Hidden {
		return
	}
	folder, _, err := b.store.SetMessageHidden(ctx, m.ID, hide)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && !isCancelled(err) {
			b.log.Warn("hide a notification mail", "account", m.AccountID, "message", m.ID, "err", err)
		}
		return
	}
	if hide != m.Hidden {
		b.messagesChanged(map[string][]string{m.AccountID: {folder}})
	}
}

// storedIssue reads the account's issue with the key; stored false when
// there is none.
func (b *Backend) storedIssue(ctx context.Context, accountID, key string) (store.Issue, bool, error) {
	is, err := b.store.IssueByKey(ctx, accountID, key)
	switch {
	case err == nil:
		return is, true, nil
	case errors.Is(err, store.ErrNotFound):
		return store.Issue{}, false, nil
	}
	if !isCancelled(err) {
		b.log.Warn("read the issue of a notification mail", "account", accountID, "err", err)
	}
	return store.Issue{}, false, err
}

// allowIssueAsk reports whether an issue that is not stored may be asked
// for: not when it was within issueMailAskAgain, and remembers that it is.
func (b *Backend) allowIssueAsk(accountID, key string, now time.Time) bool {
	im := &b.im
	im.mu.Lock()
	defer im.mu.Unlock()
	id := accountID + "\x00" + key
	if t, ok := im.asked[id]; ok && now.Sub(t) < issueMailAskAgain {
		return false
	}
	if len(im.asked) >= issueMailAsked {
		for k, t := range im.asked {
			if now.Sub(t) >= issueMailAskAgain {
				delete(im.asked, k)
			}
		}
		if len(im.asked) >= issueMailAsked {
			im.asked = map[string]time.Time{} // a flood: start over
		}
	}
	im.asked[id] = now
	return true
}

// forgetIssueAsk forgets that the issue was asked for: nobody heard.
func (b *Backend) forgetIssueAsk(accountID, key string) {
	b.im.mu.Lock()
	delete(b.im.asked, accountID+"\x00"+key)
	b.im.mu.Unlock()
}

// allowIssueWait reports whether a mail syncer may wait for an issue, and
// counts the wait (issueMailWaits a minute).
func (b *Backend) allowIssueWait(now time.Time) bool {
	im := &b.im
	im.mu.Lock()
	defer im.mu.Unlock()
	keep := im.waits[:0]
	for _, t := range im.waits {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	im.waits = keep
	if len(im.waits) >= issueMailWaits {
		return false
	}
	im.waits = append(im.waits, now)
	return true
}

// messagesChanged queues notify.messagesChanged for the folders of each
// mail account (an empty list: any folder); what gathers within
// issueMailCoalesce goes out as one notification an account.
func (b *Backend) messagesChanged(touched map[string][]string) {
	if len(touched) == 0 {
		return
	}
	im := &b.im
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.stopped {
		return
	}
	for acc, folders := range touched {
		if acc == "" {
			continue
		}
		set := im.changed[acc]
		if set == nil {
			set = map[string]bool{}
			im.changed[acc] = set
		}
		if len(folders) == 0 {
			set[""] = true
		}
		for _, f := range folders {
			set[f] = true
		}
	}
	if im.timer == nil && len(im.changed) > 0 {
		im.timer = time.AfterFunc(issueMailCoalesce, b.flushMessagesChanged)
	}
}

// flushMessagesChanged sends what messagesChanged gathered, the accounts
// and the folders of each in order.
func (b *Backend) flushMessagesChanged() {
	im := &b.im
	im.mu.Lock()
	changed := im.changed
	im.changed = map[string]map[string]bool{}
	im.timer = nil
	stopped := im.stopped
	im.mu.Unlock()
	if stopped {
		return
	}
	accounts := make([]string, 0, len(changed))
	for acc := range changed {
		accounts = append(accounts, acc)
	}
	sort.Strings(accounts)
	for _, acc := range accounts {
		ev := api.MessagesChangedNotification{AccountID: api.AccountID(acc)}
		if !changed[acc][""] {
			folders := make([]string, 0, len(changed[acc]))
			for f := range changed[acc] {
				folders = append(folders, f)
			}
			sort.Strings(folders)
			for _, f := range folders {
				ev.FolderIDs = append(ev.FolderIDs, api.FolderID(f))
			}
		}
		b.syncNotifier.MessagesChanged(ev)
	}
}

// hiddenMail reports a message that is not to be announced: a mail
// message under the display filter.
func (b *Backend) hiddenMail(ev api.NewMessageNotification) bool {
	if ev.Message.ID == "" || strings.HasPrefix(string(ev.Message.ThreadID), store.IssueThreadPrefix) {
		return false
	}
	hidden, err := b.store.MessageHidden(context.Background(), string(ev.Message.ID))
	return err == nil && hidden
}

// issuePassed is told that a syncer reports a finished pass: the first of
// an issue-tracker account with its configuration judges the account's
// links and looks through the stored mail.
func (b *Backend) issuePassed(st api.SyncState) {
	if st.Status != api.SyncIdle || st.LastSync == nil || st.Error != nil {
		return
	}
	id := string(st.AccountID)
	ctx := context.Background()
	b.issueTrackers(ctx) // the accounts, read again when they are old
	im := &b.im
	im.mu.Lock()
	other := im.loaded && !im.trackerIDs[id]
	settled := im.settled[id] || im.scanning[id] > 0
	im.mu.Unlock()
	if other || settled {
		return
	}
	a, err := b.store.GetAccount(ctx, id)
	if err != nil || !isIssueAccount(a) || a.Config.Jira == nil {
		return
	}
	if v, _, err := b.store.GetMeta(ctx, metaIssueMailSettled+id); err == nil && v == issueMailState(a) {
		im.mu.Lock()
		im.settled[id] = true
		im.mu.Unlock()
		return
	}
	b.kickIssueMail(ctx, id, issueMailJob{settle: true, scan: true}, 0)
}

// issueMailState names what an account's links were judged for: whether
// it is enabled, what it does with notification mail, whom it knows it
// by, its spaces and its window.
func issueMailState(a store.Account) string {
	cfg := a.Config.Jira
	if cfg == nil {
		return ""
	}
	mode := cfg.NotificationMail
	if mode == "" {
		mode = api.NotificationMailSync
	}
	type space struct{ ID, Key string }
	spaces := make([]space, 0, len(cfg.Spaces))
	for _, sp := range cfg.Spaces {
		spaces = append(spaces, space{sp.ID, sp.Key})
	}
	raw, err := json.Marshal(struct {
		Enabled bool
		Mode    api.NotificationMailMode
		Senders []string
		Spaces  []space
		Days    int
	}{a.Enabled, mode, jira.NotificationSenders(*cfg), spaces, jira.OfflineDays(*cfg)})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "1:" + hex.EncodeToString(sum[:16])
}

// issueAccountChanged is told that an account was added, changed, paused
// or resumed: the matcher reads the accounts again, and the links of an
// issue-tracker account (before or now) are judged for what it is now.
func (b *Backend) issueAccountChanged(ctx context.Context, before, after store.Account) {
	b.invalidateIssueTrackers()
	if !isIssueAccount(before) && !isIssueAccount(after) {
		return
	}
	b.kickIssueMail(context.WithoutCancel(ctx), after.ID, issueMailJob{settle: true, scan: true}, 0)
}

// showIssueMail shows the mail an account hid, before the account goes
// (account.remove).
func (b *Backend) showIssueMail(ctx context.Context, accountID string) {
	touched, err := b.store.SetIssueMailHidden(ctx, accountID, false, nil)
	if err != nil {
		b.log.Warn("show the notification mail of a removed account", "account", accountID, "err", err)
		return
	}
	b.messagesChanged(touched)
}

// kickIssueMail queues a job for the account, to run after delay; jobs
// of one account merge. Without a worker (StartSync has not run) the job
// runs here.
func (b *Backend) kickIssueMail(ctx context.Context, accountID string, job issueMailJob, delay time.Duration) {
	im := &b.im
	im.mu.Lock()
	if im.stopped {
		im.mu.Unlock()
		return
	}
	if !im.running {
		if job.scan {
			im.scanning[accountID]++
		}
		im.mu.Unlock()
		b.runIssueMailJob(ctx, accountID, job)
		return
	}
	job.due = time.Now().Add(delay)
	old, queued := im.jobs[accountID]
	if queued {
		job.settle, job.scan = job.settle || old.settle, job.scan || old.scan
		if old.due.Before(job.due) {
			job.due = old.due
		}
	}
	if job.scan && !(queued && old.scan) {
		im.scanning[accountID]++
	}
	im.jobs[accountID] = job
	im.mu.Unlock()
	select {
	case im.wake <- struct{}{}:
	default:
	}
}

// startIssueMail starts the worker, which runs the queued jobs one at a
// time until ctx ends; done is closed when it has stopped.
func (b *Backend) startIssueMail(ctx context.Context) (done <-chan struct{}) {
	ch := make(chan struct{})
	im := &b.im
	im.mu.Lock()
	if im.running || im.stopped {
		im.mu.Unlock()
		close(ch)
		return ch
	}
	im.running = true
	im.mu.Unlock()
	go func() {
		defer close(ch)
		defer func() {
			im.mu.Lock()
			im.running, im.stopped = false, true
			im.jobs, im.scanning = map[string]issueMailJob{}, map[string]int{}
			im.mu.Unlock()
		}()
		for ctx.Err() == nil {
			id, job, wait, ok := b.nextIssueMailJob(time.Now())
			if ok {
				b.runIssueMailJob(ctx, id, job)
				continue
			}
			var timer <-chan time.Time
			var t *time.Timer
			if wait > 0 {
				t = time.NewTimer(wait)
				timer = t.C
			}
			select {
			case <-ctx.Done():
			case <-im.wake:
			case <-timer:
			}
			if t != nil {
				t.Stop()
			}
		}
	}()
	return ch
}

// nextIssueMailJob takes the job that is due (the account first in
// order, when several are), or says how long the next one is away (0:
// none is queued).
func (b *Backend) nextIssueMailJob(now time.Time) (id string, job issueMailJob, wait time.Duration, ok bool) {
	im := &b.im
	im.mu.Lock()
	defer im.mu.Unlock()
	for acc, j := range im.jobs {
		if !j.due.After(now) {
			if !ok || acc < id {
				id, job, ok = acc, j, true
			}
			continue
		}
		if d := j.due.Sub(now); wait == 0 || d < wait {
			wait = d
		}
	}
	if ok {
		delete(im.jobs, id)
		return id, job, 0, true
	}
	return "", issueMailJob{}, wait, false
}

// runIssueMailJob runs one job; its failure is logged, and the next
// evaluation (the hourly one at the latest) does what it left.
func (b *Backend) runIssueMailJob(ctx context.Context, accountID string, job issueMailJob) {
	if job.scan {
		defer func() {
			b.im.mu.Lock()
			if b.im.scanning[accountID]--; b.im.scanning[accountID] <= 0 {
				delete(b.im.scanning, accountID)
			}
			b.im.mu.Unlock()
		}()
	}
	var err error
	if job.settle {
		err = b.settleIssueMail(ctx, accountID, job.scan)
	} else {
		err = b.applyIssueMail(ctx, accountID)
	}
	if err != nil && !isCancelled(err) {
		b.log.Warn("notification mail of an issue-tracker account", "account", accountID, "err", err)
	}
}

// hidesIssueMail reads the account and reports whether it hides its
// notification mail now: an enabled jira account with notificationMail
// "hide". An account that is gone hides nothing.
func (b *Backend) hidesIssueMail(ctx context.Context, accountID string) (store.Account, bool, error) {
	a, err := b.store.GetAccount(ctx, accountID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Account{ID: accountID}, false, nil
	case err != nil:
		return store.Account{}, false, err
	}
	hide := isIssueAccount(a) && a.Config.Jira != nil && a.Enabled && a.Config.Jira.NotificationMail == api.NotificationMailHide
	return a, hide, nil
}

// applyIssueMail makes the account's linked mail follow its stored
// issues: hidden when the issue is stored, shown when it is not; all of
// it shown for an account that hides nothing.
func (b *Backend) applyIssueMail(ctx context.Context, accountID string) error {
	a, hide, err := b.hidesIssueMail(ctx, accountID)
	if err != nil {
		return err
	}
	touched, err := b.store.SetIssueMailHidden(ctx, a.ID, hide, nil)
	b.messagesChanged(touched)
	return err
}

// settleIssueMail judges the links of an issue-tracker account for what
// the account is now (see the file comment), with scan after looking
// through the stored mail for notifications not linked yet, and records
// what it judged them for.
func (b *Backend) settleIssueMail(ctx context.Context, accountID string, scan bool) error {
	a, hide, err := b.hidesIssueMail(ctx, accountID)
	if err != nil {
		return err
	}
	if !hide {
		touched, err := b.store.SetIssueMailHidden(ctx, a.ID, false, nil)
		b.messagesChanged(touched)
		if err != nil {
			return err
		}
		return b.issueMailSettled(ctx, a, scan)
	}
	tr, ok := b.issueTrackerOf(ctx, a)
	if !ok {
		// Nobody to know its mail by: nothing is its notification.
		tr = issueTracker{id: a.ID, cfg: *a.Config.Jira, hide: true, days: jira.OfflineDays(*a.Config.Jira)}
	}
	linked, err := b.judgeIssueLinks(ctx, tr)
	if err != nil {
		return err
	}
	if scan && ok {
		if err := b.scanIssueMail(ctx, tr, linked); err != nil {
			return err
		}
	}
	touched, err := b.store.SetIssueMailHidden(ctx, a.ID, true, nil)
	b.messagesChanged(touched)
	if err != nil {
		return err
	}
	return b.issueMailSettled(ctx, a, scan)
}

// issueMailSettled records what the account's links were judged for (as
// the account was when the evaluation began), after a scan only: an
// evaluation without one leaves the stored mail to the next first pass.
// The next finished pass that finds the record right remembers it
// (issuePassed).
func (b *Backend) issueMailSettled(ctx context.Context, a store.Account, scanned bool) error {
	if !scanned || !isIssueAccount(a) || a.Config.Jira == nil {
		return nil
	}
	return b.store.SetMeta(ctx, metaIssueMailSettled+a.ID, issueMailState(a))
}

// judgeIssueLinks asks the matcher about every link of the account
// again: a link it no longer agrees with goes and its message is shown,
// one it reads another issue from is linked to that. It returns the keys
// the messages are linked by now.
func (b *Backend) judgeIssueLinks(ctx context.Context, tr issueTracker) (map[string]string, error) {
	linked := map[string]string{}
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		links, err := b.store.IssueMailLinks(ctx, tr.id, after, issueMailBatch)
		if err != nil {
			return nil, err
		}
		var stale []string
		moved := map[string]string{}
		for _, l := range links {
			after = l.MessageID
			key, ok := jira.MatchNotification(tr.cfg, tr.spaces, l.From, l.Subject)
			switch {
			case !ok:
				stale = append(stale, l.MessageID)
			case key != l.IssueKey:
				stale = append(stale, l.MessageID)
				moved[l.MessageID] = key
			default:
				linked[l.MessageID] = key
			}
		}
		touched, err := b.store.UnlinkIssueMail(ctx, stale)
		b.messagesChanged(touched)
		if err != nil {
			return nil, err
		}
		for id, key := range moved {
			if err := b.store.LinkIssueMail(ctx, id, tr.id, key, ""); err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
			linked[id] = key
		}
		if len(links) < issueMailBatch {
			return linked, nil
		}
		if err := pause(ctx, issueMailPause); err != nil {
			return nil, err
		}
	}
}

// scanIssueMail looks through the mail stored within the account's
// window from its senders, in every mail account, for notifications that
// are not linked (linked: message → key, the account's links): mail that
// arrived before the account did, or before it took notification mail.
// Each is judged as an arriving one is, by every tracker in order, but
// for the wait; at most issueMailScanLimit messages are read and
// issueMailTriggerLimit issues asked for.
func (b *Backend) scanIssueMail(ctx context.Context, tr issueTracker, linked map[string]string) error {
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		return err
	}
	var mail []string
	var trackers []issueTracker
	for _, a := range accounts {
		switch {
		case a.Config.Protocol() == api.AccountIMAP, a.Config.Protocol() == api.AccountGraph:
			mail = append(mail, a.ID)
		case a.ID == tr.id:
			trackers = append(trackers, tr)
		default:
			if other, ok := b.issueTrackerOf(ctx, a); ok {
				trackers = append(trackers, other)
			}
		}
	}
	senders := jira.NotificationSenders(tr.cfg)
	since := time.Now().Add(-time.Duration(tr.days) * 24 * time.Hour)
	asked := map[string]bool{}
	after, read := "", 0
	for read < issueMailScanLimit {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := b.store.MailFromSenders(ctx, mail, senders, since, after, issueMailBatch)
		if err != nil {
			return err
		}
		for _, m := range batch {
			after = m.ID
			if _, ok := linked[m.ID]; ok {
				continue // judged a moment ago (judgeIssueLinks)
			}
			b.matchIssueMail(ctx, trackers, m, false, asked)
		}
		read += len(batch)
		if len(batch) < issueMailBatch {
			return nil
		}
		if err := pause(ctx, issueMailPause); err != nil {
			return err
		}
	}
	b.log.Info("notification mail: the scan of the stored mail stopped at its limit", "account", tr.id, "messages", read)
	return nil
}

// reevaluateIssueMail judges the links of every issue-tracker account
// again, without looking through the stored mail: an issue that went
// shows its mail, one that is stored hides it (Maintain, hourly).
func (b *Backend) reevaluateIssueMail(ctx context.Context) {
	accounts, err := b.store.ListAccounts(ctx)
	if err != nil {
		if !isCancelled(err) {
			b.log.Warn("list the accounts for notification mail", "err", err)
		}
		return
	}
	for _, a := range accounts {
		if !isIssueAccount(a) {
			continue
		}
		if err := b.settleIssueMail(ctx, a.ID, false); err != nil {
			if !isCancelled(err) {
				b.log.Warn("notification mail of an issue-tracker account", "account", a.ID, "err", err)
			}
			return
		}
	}
}

// stopIssueMail ends the feature's timers; nothing is announced after it.
func (b *Backend) stopIssueMail() {
	im := &b.im
	im.mu.Lock()
	defer im.mu.Unlock()
	im.stopped = true
	if im.timer != nil {
		im.timer.Stop()
		im.timer = nil
	}
	im.jobs = map[string]issueMailJob{}
}

// pause waits d, or less when ctx ends.
func pause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
