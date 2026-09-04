// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package outbox delivers queued messages over SMTP. One Worker per
// enabled account drains the store's outbox (docs/api.md §4.3
// message.send): it takes the oldest due entry, runs one SMTP session for
// it and records the outcome — delivered (the IMAP syncer then files the
// Sent copy), retry later with backoff, or failed for good. The Supervisor
// owns the workers' lifecycle the way imap.Supervisor owns the syncers.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// backoffMin and backoffMax bound the retry delay after a transient
	// failure: 1 min, 2 min, 4 min, … up to 4 h, each ± backoffJitter.
	backoffMin    = time.Minute
	backoffMax    = 4 * time.Hour
	backoffJitter = 0.2
	// authDefer is how long every queued message waits after a refused
	// password or a missing keyring secret; account.update retries at once.
	authDefer = 24 * time.Hour
	// deliverTimeout bounds one SMTP session.
	deliverTimeout = 10 * time.Minute
	// storeRetry is the pause after a store failure in the loop itself, so
	// a broken database does not spin the worker.
	storeRetry = time.Minute
)

// DeliverFunc is smtp.Deliver's signature; Deps.Deliver substitutes it.
type DeliverFunc func(ctx context.Context, cfg api.ServerConfig, password, from string, rcpts []string, r io.Reader, size int64) error

// Deps is what a Worker needs. Store and Password are required; the rest
// is optional (nil = default or no-op).
type Deps struct {
	Store *store.Store
	// Password fetches the account's SMTP password from the keyring. Its
	// error codes 1200/1201/1202 take the authentication path.
	Password func(ctx context.Context) (string, error)
	// Notifier receives notify.authRequired; notify.syncState is core's job
	// (see Changed).
	Notifier api.Notifier
	// Deliver runs one SMTP session; nil means smtp.Deliver.
	Deliver DeliverFunc
	// FilesSentCopy says the delivery path stores the Sent copy on the
	// server itself (Microsoft Graph's sendMail does): after a delivery the
	// local copy is dropped and the Sent folder re-synchronised, instead of
	// being kept for the syncer to upload.
	FilesSentCopy bool
	// Trigger asks the account's syncer for a pass of one folder after a
	// delivery, so the Sent copy is appended (or fetched) at once.
	Trigger func(accountID string, folder api.FolderID, full bool) bool
	// Changed is called after every change of an outbox row; core re-emits
	// the account's SyncState with the new pendingOutbox count.
	Changed func(accountID string)

	Log *slog.Logger
	// Now and Backoff are the clock and the retry schedule; tests inject
	// deterministic ones. Backoff receives the number of failed attempts so
	// far, this one included (1 for the first failure).
	Now     func() time.Time
	Backoff func(attempt int) time.Duration
}

// Worker drains one account's outbox.
type Worker struct {
	account store.Account
	deps    Deps
	log     *slog.Logger
	wake    chan struct{}

	mu           sync.Mutex
	notifiedAuth api.ErrorCode // auth code already reported since the last delivery
}

// NewWorker prepares a worker for the account; Run starts it.
func NewWorker(a store.Account, deps Deps) *Worker {
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Backoff == nil {
		deps.Backoff = DefaultBackoff
	}
	if deps.Deliver == nil {
		deps.Deliver = smtp.Deliver
	}
	return &Worker{
		account: a,
		deps:    deps,
		log:     deps.Log.With("component", "outbox", "account", a.ID),
		wake:    make(chan struct{}, 1),
	}
}

// DefaultBackoff is the production retry schedule: backoffMin doubled per
// failed attempt, capped at backoffMax, with ± backoffJitter of random
// spread so retries of many messages do not line up.
func DefaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := backoffMin
	for i := 1; i < attempt && d < backoffMax; i++ {
		d *= 2
	}
	if d > backoffMax {
		d = backoffMax
	}
	spread := float64(d) * backoffJitter
	return time.Duration(float64(d) + (rand.Float64()*2-1)*spread)
}

// Wake asks the worker to look at the queue now. It never blocks: a wake
// that is already pending covers this one.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run drains the outbox until ctx ends and returns ctx's error. It starts
// with the store's recovery sweep (an attempt interrupted by a crash goes
// back to queued, every queued entry becomes due) and then alternates
// between sending the oldest due entry and sleeping until the next retry
// time, a Wake, or the end of ctx.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.deps.Store.ResetOutbox(ctx, w.account.ID); err != nil && ctx.Err() == nil {
		w.log.Warn("outbox reset", "err", err)
	}
	w.changed()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entry, ok, err := w.deps.Store.NextOutbox(ctx, w.account.ID, w.deps.Now())
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.log.Warn("outbox lookup", "err", err)
			w.sleep(ctx, storeRetry)
			continue
		case ok:
			w.sendOne(ctx, entry)
			continue
		}
		due, has, err := w.deps.Store.NextOutboxDue(ctx, w.account.ID)
		wait := time.Duration(-1) // no timer
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			w.log.Warn("outbox schedule", "err", err)
			wait = storeRetry
		case has:
			wait = due.Sub(w.deps.Now())
			if wait < 0 {
				wait = 0
			}
		}
		w.sleep(ctx, wait)
	}
}

// sleep waits for d (negative: forever), a Wake, or the end of ctx.
func (w *Worker) sleep(ctx context.Context, d time.Duration) {
	var timer <-chan time.Time
	if d >= 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ctx.Done():
	case <-w.wake:
	case <-timer:
	}
}

// sendOne runs one delivery attempt for a queued entry and records the
// outcome. When ctx ends mid-attempt the row is left as it is (sending);
// the next Run's ResetOutbox re-queues it.
func (w *Worker) sendOne(ctx context.Context, e store.OutboxEntry) {
	id := e.MessageID
	if err := w.deps.Store.MarkOutboxSending(ctx, id); err != nil {
		if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
			w.log.Warn("outbox mark sending", "message", id, "err", err)
		}
		return
	}
	w.changed()
	defer w.changed()

	password, err := w.deps.Password(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.fail(ctx, e, err, false, "")
		return
	}

	if _, err := w.deps.Store.GetMessage(ctx, w.account.ID, id); err != nil {
		w.missing(ctx, id, "message row", err)
		return
	}
	f, err := w.deps.Store.OpenMessageRaw(ctx, w.account.ID, id)
	if err != nil {
		w.missing(ctx, id, "raw message", err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		w.missing(ctx, id, "raw message", err)
		return
	}

	var smtpCfg api.ServerConfig
	if w.account.Config.SMTP != nil {
		smtpCfg = *w.account.Config.SMTP
	}
	dctx, cancel := context.WithTimeout(ctx, deliverTimeout)
	err = w.deps.Deliver(dctx, smtpCfg, password, e.EnvelopeFrom, e.Recipients, f, info.Size())
	cancel()
	if err == nil {
		w.succeed(ctx, e)
		return
	}
	if ctx.Err() != nil {
		return
	}
	var se *smtp.SendError
	permanent := errors.As(err, &se) && se.Permanent
	w.fail(ctx, e, err, permanent, password)
}

// missing records a message whose row or file is gone: nothing can be
// sent, and the store is the only place the reason can be recorded.
func (w *Worker) missing(ctx context.Context, id, what string, err error) {
	if ctx.Err() != nil {
		return
	}
	msg := fmt.Sprintf("%s missing: %s", what, transport.CleanMessage(err.Error()))
	w.log.Error("outbox message unusable", "message", id, "err", msg)
	if err := w.deps.Store.MarkOutboxFailed(ctx, id, api.CodeStorageError, msg); err != nil && !errors.Is(err, store.ErrNotFound) {
		w.log.Warn("outbox mark failed", "message", id, "err", err)
	}
}

// succeed records a delivery. The bookkeeping runs detached from ctx: the
// server has the message, so a shutdown right now must not lead to a
// second delivery on the next start.
func (w *Worker) succeed(ctx context.Context, e store.OutboxEntry) {
	ctx = context.WithoutCancel(ctx)
	id := e.MessageID
	w.mu.Lock()
	w.notifiedAuth = 0
	w.mu.Unlock()

	for _, rcpt := range e.Recipients {
		if err := w.deps.Store.AddKnownSender(ctx, rcpt, api.KnownSenderSourceSent); err != nil {
			w.log.Warn("record recipient as known sender", "err", err)
		}
	}
	sent, err := w.deps.Store.FolderByRole(ctx, w.account.ID, api.RoleSent)
	switch {
	case errors.Is(err, store.ErrNotFound), w.deps.FilesSentCopy:
		// No Sent folder, or the server files its own copy: the local copy
		// has served its purpose. When the server filed one, the Sent
		// folder is re-synchronised so the copy shows up under its identity.
		if err := w.deps.Store.DeleteOutboxMessage(ctx, w.account.ID, id); err != nil && !errors.Is(err, store.ErrNotFound) {
			w.log.Warn("drop delivered outbox message", "message", id, "err", err)
		}
		w.log.Info("outbox delivered", "message", id, "attempts", e.Attempts+1, "sentCopy", w.deps.FilesSentCopy)
		if w.deps.FilesSentCopy && err == nil && sent.ID != "" && w.deps.Trigger != nil {
			w.deps.Trigger(w.account.ID, api.FolderID(sent.ID), false)
		}
		return
	case err != nil:
		w.log.Warn("look up sent folder", "err", err)
		// Fall through: keep the message as sent; the syncer files it once
		// the folder can be read again.
	}
	if err := w.deps.Store.MarkOutboxSent(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		w.log.Warn("outbox mark sent", "message", id, "err", err)
	}
	w.log.Info("outbox delivered", "message", id, "attempts", e.Attempts+1, "sentCopy", true)
	if sent.ID != "" && w.deps.Trigger != nil {
		w.deps.Trigger(w.account.ID, api.FolderID(sent.ID), false)
	}
}

// fail records a failed attempt: credentials problems defer the whole
// queue and raise notify.authRequired, permanent failures park the message
// as failed, everything else is retried with backoff. A timeout of this
// worker's own per-attempt deadline arrives as cancelled (1003) and is a
// transient serverTimeout here. The recorded text never contains the
// password (smtp.Deliver redacts it already; this is the second net).
func (w *Worker) fail(ctx context.Context, e store.OutboxEntry, err error, permanent bool, password string) {
	id := e.MessageID
	var ae *api.Error
	if !errors.As(err, &ae) {
		ae = api.NewError(api.CodeServerError, "%s", transport.CleanMessage(err.Error()))
	}
	code, msg := ae.Code, transport.CleanMessage(ae.Message)
	if password != "" {
		msg = strings.ReplaceAll(msg, password, "***")
	}
	now := w.deps.Now()

	switch {
	case code == api.CodeAuthRequired || code == api.CodeAuthFailed || code == api.CodeKeyringError:
		until := now.Add(authDefer)
		w.log.Warn("outbox delivery needs credentials", "message", id, "code", code, "err", msg)
		if err := w.deps.Store.MarkOutboxRetry(ctx, id, code, msg, until); err != nil && !errors.Is(err, store.ErrNotFound) {
			w.log.Warn("outbox mark retry", "message", id, "err", err)
		}
		if _, err := w.deps.Store.DeferOutbox(ctx, w.account.ID, until, code, msg); err != nil {
			w.log.Warn("outbox defer", "err", err)
		}
		w.notifyAuth(code, msg)
	case permanent:
		w.log.Warn("outbox delivery failed permanently", "message", id, "code", code, "err", msg)
		if err := w.deps.Store.MarkOutboxFailed(ctx, id, code, msg); err != nil && !errors.Is(err, store.ErrNotFound) {
			w.log.Warn("outbox mark failed", "message", id, "err", err)
		}
	default:
		if code == api.CodeCancelled {
			code, msg = api.CodeServerTimeout, fmt.Sprintf("delivery did not finish within %s", deliverTimeout)
		}
		attempt := e.Attempts + 1
		delay := w.deps.Backoff(attempt)
		w.log.Warn("outbox delivery failed, will retry", "message", id, "code", code, "attempt", attempt, "retryIn", delay, "err", msg)
		if err := w.deps.Store.MarkOutboxRetry(ctx, id, code, msg, now.Add(delay)); err != nil && !errors.Is(err, store.ErrNotFound) {
			w.log.Warn("outbox mark retry", "message", id, "err", err)
		}
	}
}

// notifyAuth emits notify.authRequired once per distinct code until a
// delivery succeeds (the same dedupe the IMAP syncer applies).
func (w *Worker) notifyAuth(code api.ErrorCode, msg string) {
	w.mu.Lock()
	already := w.notifiedAuth == code
	w.notifiedAuth = code
	w.mu.Unlock()
	if already || w.deps.Notifier == nil {
		return
	}
	w.deps.Notifier.AuthRequired(api.AuthRequiredNotification{
		AccountID: api.AccountID(w.account.ID),
		Reason:    code,
		Message:   msg,
	})
}

func (w *Worker) changed() {
	if w.deps.Changed != nil {
		w.deps.Changed(w.account.ID)
	}
}
