// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/graph"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// message.download (docs/api.md §4.3): the one way the daemon fetches a
// stored message from its server again, when the user asks for content
// this device does not hold (attachments left on the server under
// attachmentOfflineDays, a body not downloaded yet). It runs on a
// connection of its own, apart from the syncer and its IDLE, only reads
// the server, and stores the whole message through ingest.Store, which
// checks that it is the stored one. Under neverStoreAttachments it stores
// nothing of a message whose body is stored: the whole message is received
// into memory (ingest.Hold) and held there (memCache), and the parts stay
// remote; a body not downloaded yet is stored without its attachments and
// held in memory whole too.

// downloadBudget bounds one download from the connection to the commit;
// a client waits longer (docs/api.md asks for 5 minutes). A variable so a
// test can run out of it.
var downloadBudget = 4 * time.Minute

const (
	// downloadsPerAccount is how many downloads of one account run at a
	// time; more wait for a slot.
	downloadsPerAccount = 2
	// downloadStopWait bounds how long shutdown waits for the downloads
	// it cancelled.
	downloadStopWait = 10 * time.Second
)

// rawFetcher downloads a stored message from its server into fn, with the
// size the server announced (-1 when it announces none).
type rawFetcher func(ctx context.Context, a store.Account, loc store.ServerLocation, fn func(r io.Reader, size int64) error) error

// errNoServerCopy: the store knows no place on the server to fetch the
// message from (a local move not pushed yet, without a snapshot).
var errNoServerCopy = errors.New("core: the message has no known place on the server")

// downloadState is what message.download keeps between calls: the
// downloads running, one per message and joined by every call for it, the
// slots of each account, and the lifetime they all run under, which ends
// only with the daemon (close), not with a caller. Its zero value is ready
// to use.
type downloadState struct {
	// fetchRaw fetches a message from its account's server; nil =
	// Backend.fetchRaw. A field so tests substitute a fake server.
	fetchRaw rawFetcher

	mu      sync.Mutex
	life    context.Context
	end     context.CancelFunc
	closed  bool
	flights map[string]*download
	slots   map[string]chan struct{}
	wg      sync.WaitGroup
}

// download is one running download, shared by the calls for its message.
type download struct {
	account string
	cancel  context.CancelFunc
	done    chan struct{}
	err     error // set before done is closed
}

// initLocked makes the zero value usable; d.mu is held.
func (d *downloadState) initLocked() {
	if d.flights == nil {
		d.flights = map[string]*download{}
		d.slots = map[string]chan struct{}{}
		d.life, d.end = context.WithCancel(context.Background())
	}
}

// start returns the running download of the message, or starts run as it,
// under the downloads' lifetime and the budget.
func (d *downloadState) start(accountID, id string, run func(ctx context.Context) error) *download {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()
	key := accountID + "/" + id
	if f := d.flights[key]; f != nil {
		return f
	}
	f := &download{account: accountID, done: make(chan struct{})}
	if d.closed {
		f.err = api.NewError(api.CodeCancelled, "the daemon is shutting down")
		close(f.done)
		return f
	}
	ctx, cancel := context.WithTimeout(d.life, downloadBudget)
	f.cancel = cancel
	d.flights[key] = f
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		err := run(ctx)
		cancel()
		d.mu.Lock()
		delete(d.flights, key)
		d.mu.Unlock()
		f.err = err
		close(f.done)
	}()
	return f
}

// slot is the account's semaphore of downloadsPerAccount places.
func (d *downloadState) slot(accountID string) chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()
	s := d.slots[accountID]
	if s == nil {
		s = make(chan struct{}, downloadsPerAccount)
		d.slots[accountID] = s
	}
	return s
}

// stopAccount cancels the account's downloads and waits until they have
// stopped, so that none writes into an account being removed or paused.
func (d *downloadState) stopAccount(accountID string) {
	d.mu.Lock()
	var running []*download
	for _, f := range d.flights {
		if f.account == accountID {
			f.cancel()
			running = append(running, f)
		}
	}
	delete(d.slots, accountID)
	d.mu.Unlock()
	for _, f := range running {
		<-f.done
	}
}

// close cancels every download, refuses new ones and waits for the
// cancelled ones within downloadStopWait. malachid calls it (through
// Backend.Close) before it closes the store.
func (d *downloadState) close(log *slog.Logger) {
	d.mu.Lock()
	d.closed = true
	if d.end != nil {
		d.end()
	}
	d.mu.Unlock()
	stopped := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(downloadStopWait):
		log.Warn("downloads did not stop in time", "timeout", downloadStopWait)
	}
}

// Download makes a stored message whole (docs/api.md §4.3,
// message.download): a message with nothing missing, one whose whole copy
// is held in memory, or one that failed to parse, answers at once; one
// over the raw cap is attachmentTooBig; a paused account is unavailable.
// Otherwise the call joins the message's download, or starts it, and
// waits; a caller that gives up gets cancelled while the download goes on.
func (s *messageService) Download(ctx context.Context, p api.MessageDownloadParams) (*api.MessageDownloadResult, error) {
	if p.AccountID == "" || p.MessageID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and messageId are required")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	m, err := s.b.getMessage(ctx, a.ID, string(p.MessageID))
	if err != nil {
		return nil, err
	}
	need, err := s.b.needsFetch(m)
	switch {
	case err != nil:
		return nil, err
	case need && !a.Enabled:
		return nil, api.NewError(api.CodeUnavailable, "account %s is paused", a.ID)
	case need:
		accountID, id := a.ID, m.ID
		f := s.b.dl.start(accountID, id, func(ctx context.Context) error { return s.b.runDownload(ctx, accountID, id) })
		select {
		case <-f.done:
		case <-ctx.Done():
			return nil, api.NewError(api.CodeCancelled, "the call ended; the download goes on")
		}
		if f.err != nil {
			return nil, f.err
		}
	}
	got, err := s.Get(ctx, api.MessageGetParams{AccountID: p.AccountID, MessageID: p.MessageID})
	if err != nil {
		return nil, err
	}
	return &api.MessageDownloadResult{Message: got.Message}, nil
}

// needsDownload says whether message.download has anything to fetch for a
// message: a body not downloaded yet, or attachments kept on the server.
// A message over the raw cap is attachmentTooBig.
func needsDownload(m store.Message) (bool, error) {
	switch m.BodyState {
	case store.BodyFetched:
		return m.RawState == store.RawPartial, nil
	case store.BodyFailed:
		return false, nil
	case store.BodyTooBig:
		return false, messageTooBig(m.Size)
	}
	return true, nil
}

// needsFetch is needsDownload for the message as the daemon has it: one
// whose attachments are on the server while message.download holds a
// whole copy in memory (neverStoreAttachments) that has every one of them
// (heldMissing) needs nothing, and is marked used there. A copy that
// lacks one does not count: a download fetches the message again.
func (b *Backend) needsFetch(m store.Message) (bool, error) {
	need, err := needsDownload(m)
	if err != nil || !need || m.BodyState != store.BodyFetched {
		return need, err
	}
	held, ok := b.mem.get(m.AccountID, m.ID)
	return !ok || len(heldMissing(m, held.attachments)) > 0, nil
}

// messageTooBig is the error for a message over the raw-message cap.
func messageTooBig(size int64) *api.Error {
	e := api.NewError(api.CodeAttachmentTooBig, "the message is over the %d-byte cap", int64(ingest.MaxMessageBytes))
	e.Data = map[string]int64{"limit": ingest.MaxMessageBytes, "size": size}
	return e
}

// runDownload is one download: a slot of the account, the account and the
// message read again (a paused account, a deleted message, a message made
// whole meanwhile all end it), then the transfer and the commit. A commit
// that finds the row changed (the syncer stored the body meanwhile) reads
// it once more and, when something is still missing, tries again.
func (b *Backend) runDownload(ctx context.Context, accountID, id string) error {
	slot := b.dl.slot(accountID)
	select {
	case slot <- struct{}{}:
	case <-ctx.Done():
		return b.downloadStopped(ctx, accountID)
	}
	defer func() { <-slot }()
	for attempt := 0; ; attempt++ {
		a, err := b.store.GetAccount(ctx, accountID)
		switch {
		case ctx.Err() != nil:
			return b.downloadStopped(ctx, accountID)
		case errors.Is(err, store.ErrNotFound):
			return api.NewError(api.CodeAccountNotFound, "unknown account %q", accountID)
		case err != nil:
			return api.NewError(api.CodeStorageError, "%v", err)
		case !a.Enabled:
			return api.NewError(api.CodeUnavailable, "account %s is paused", accountID)
		}
		m, err := b.getMessage(ctx, accountID, id)
		switch {
		case ctx.Err() != nil:
			return b.downloadStopped(ctx, accountID)
		case err != nil:
			return err
		}
		need, err := b.needsFetch(m)
		if err != nil || !need {
			return err
		}
		loc, err := b.store.MessageServerLocation(ctx, accountID, id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			err = errNoServerCopy
		case err == nil:
			err = b.fetchInto(ctx, a, m, loc)
		}
		if errors.Is(err, store.ErrConflict) && attempt == 0 {
			continue
		}
		return b.downloadError(ctx, a, m, loc, err)
	}
}

// fetchInto downloads the message from loc and stores it whole
// (ingest.Store, on demand): the commit expects the row as m describes it,
// and the download must be m (its Message-ID; for IMAP, where a UID names
// the same bytes for good, also its parts when the body was stored
// before). A message the server announces over the cap is refused before a
// byte of it is read (the fetcher then gives the connection up rather than
// drain it); one that turns out longer than announced still stops at the
// cap (ingest.Store). The attachment preferences apply as they are when
// the message arrives. Under neverStoreAttachments a message whose body is
// stored is only held in memory (hold), whatever its folder or kind: such
// a message is downloaded only for parts its row keeps on the server, and
// one in Drafts has those when it was reduced before it was moved there.
// A body stored for the first time keeps its attachments on the server and
// the whole message is held in memory too, unless the store keeps it whole
// anyway (Drafts, signed, ...), which is then not held. A message stored
// whole because the preference was switched on while it was being
// received is judged again by the attachment pass (storedUnder).
func (b *Backend) fetchInto(ctx context.Context, a store.Account, m store.Message, loc store.ServerLocation) error {
	role := loc.Folder.Role
	if f, err := b.store.GetFolder(ctx, a.ID, m.FolderID); err == nil {
		role = f.Role
	}
	hydrated := m.HydratedAt
	req := ingest.Request{
		Target: ingest.Target{
			AccountID: a.ID, MessageID: m.ID, Role: role, HasServerCopy: true,
			InternalDate: m.InternalDate, Date: m.Date, HydratedAt: m.HydratedAt,
		},
		Limit:    ingest.MaxMessageBytes,
		OnDemand: true,
		Expect:   store.RawExpect{BodyState: m.BodyState, RawState: m.RawState, HydratedAt: &hydrated},
		Verify:   &m,
		Strict:   a.Config.Protocol() != api.AccountGraph,
	}
	fetch := b.dl.fetchRaw
	if fetch == nil {
		fetch = b.fetchRaw
	}
	return fetch(ctx, a, loc, func(r io.Reader, size int64) error {
		if size > req.Limit {
			return ingest.ErrTooBig
		}
		// The generation before the policy: a switch-off in between
		// clears the cache, and what this download holds then goes too
		// (memCache.put).
		gen := b.mem.generation()
		req.Policy = b.attachmentPolicy()
		req.Body, req.Size = r, size
		if req.Policy.NeverStore && m.BodyState == store.BodyFetched {
			return b.hold(ctx, gen, m, req)
		}
		res, err := ingest.Store(ctx, b.store, req, b.log)
		if err != nil {
			return err
		}
		if len(res.RemoteParts) > 0 && res.Whole != nil {
			b.mem.put(gen, a.ID, m.ID, res.Whole, res.Attachments)
		}
		b.storedUnder(ctx, m.ID, req.Policy)
		return nil
	})
}

// hold receives a stored message downloaded again into memory
// (ingest.Hold, checked against m) and holds it there (memCache) for the
// parts m keeps on the server. A copy that lacks any of them
// (heldMissing: Microsoft 365 rebuilds a message, and may name or type a
// part anew) cannot stand in for the stored message, so it is not held
// (a held one would answer message.download at once, and the part stay
// partNotDownloaded for good): serverError, as for a download that is
// not the stored message; the log names the ids only.
func (b *Backend) hold(ctx context.Context, gen uint64, m store.Message, req ingest.Request) error {
	held, err := ingest.Hold(ctx, b.store, req, b.log)
	if err != nil {
		return err
	}
	if missing := heldMissing(m, held.Attachments); len(missing) > 0 {
		b.mem.remove(m.AccountID, m.ID)
		b.log.Warn("downloaded message lacks parts the stored one keeps on the server; not held",
			"account", m.AccountID, "message", m.ID, "parts", missing)
		return api.NewError(api.CodeServerError, "the server's copy of message %s lacks parts the stored one lists", m.ID)
	}
	b.mem.put(gen, m.AccountID, m.ID, held.Raw, held.Attachments)
	return nil
}

// fetchRaw downloads a stored message from its account's server: IMAP on
// a connection of its own (imap.FetchMessage) with the account's
// credential, asked for again once after the server refused an OAuth2
// token; Graph through the message's $value with the account's token.
func (b *Backend) fetchRaw(ctx context.Context, a store.Account, loc store.ServerLocation, fn func(io.Reader, int64) error) error {
	if a.Config.Protocol() == api.AccountGraph {
		id := a.ID
		client := graph.NewClient(graph.Options{
			Token:      func(ctx context.Context) (string, error) { return b.GraphTokenFor(ctx, id) },
			Invalidate: func() { b.invalidateGraphTokenFor(id) },
			Log:        b.log,
		})
		return graph.FetchMessage(ctx, client, loc.RemoteID, fn)
	}
	if a.Config.IMAP == nil {
		return api.NewError(api.CodeInvalidArgument, "account %s has no IMAP endpoint", a.ID)
	}
	at := imap.Location{Mailbox: loc.Folder.Mailbox, UIDValidity: loc.Folder.UIDValidity, UID: loc.UID}
	for retried := false; ; retried = true {
		secret, err := b.credentialFor(ctx, a.ID)
		if err != nil {
			return err
		}
		err = imap.FetchMessage(ctx, *a.Config.IMAP, secret, at, fn)
		if !retried && codeOf(err) == api.CodeAuthFailed && usesAuth(a.Config, api.AuthOAuth2) {
			// A token that expired early: the source refreshes it.
			b.invalidateCredentialsFor(a.ID)
			continue
		}
		return err
	}
}

// downloadError maps the failure of a download to the contract
// (docs/api.md, message.download). A message the server no longer has
// triggers a pass of its folder, which removes the local copy; a new body
// over the cap or unparsable is settled as the syncer would settle it.
func (b *Backend) downloadError(ctx context.Context, a store.Account, m store.Message, loc store.ServerLocation, err error) error {
	var apiErr *api.Error
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return b.downloadStopped(ctx, a.ID)
	case errors.Is(err, errNoServerCopy):
		b.Supervisor.Trigger(a.ID, "", false)
		return api.NewError(api.CodeUnavailable, "message %s has no place on the server yet; retry after the next sync", m.ID)
	case errors.Is(err, imap.ErrGone), errors.Is(err, graph.ErrGone):
		folder := loc.Folder.ID
		if folder == "" {
			folder = m.FolderID
		}
		b.Supervisor.Trigger(a.ID, api.FolderID(folder), false)
		return api.NewError(api.CodeMessageGone, "the server no longer has message %s", m.ID)
	case errors.Is(err, ingest.ErrTooBig):
		if m.BodyState == store.BodyNone {
			b.settleDownloaded(m.ID, store.BodyTooBig)
		}
		return messageTooBig(m.Size)
	case errors.Is(err, ingest.ErrUnparsable):
		if m.BodyState == store.BodyNone {
			b.settleDownloaded(m.ID, store.BodyFailed)
		}
		return api.NewError(api.CodeMalformedMessage, "message %s cannot be parsed", m.ID)
	case errors.Is(err, ingest.ErrMismatch):
		b.log.Warn("downloaded message is not the stored one", "account", a.ID, "message", m.ID)
		return api.NewError(api.CodeServerError, "the server's copy of message %s is not the stored one", m.ID)
	case errors.Is(err, store.ErrConflict):
		return api.NewError(api.CodeUnavailable, "message %s changed during the download; try again", m.ID)
	case errors.Is(err, store.ErrBusy):
		// A reader of the daemon kept the stored file open for longer than
		// the store waits to replace it (Windows): the file and its row are
		// as they were. Not retried here, which would fetch the whole
		// message again at once while that reader is likely still at it.
		return api.NewError(api.CodeUnavailable, "the stored file of message %s is in use; try again", m.ID)
	case errors.Is(err, store.ErrNotFound):
		return api.NewError(api.CodeMessageNotFound, "unknown message %q", m.ID)
	case errors.As(err, &apiErr):
		return apiErr
	}
	return api.NewError(api.CodeStorageError, "%v", err)
}

// settleDownloaded records the terminal state of a body downloaded for the
// first time, as the syncer does for one it downloads.
func (b *Backend) settleDownloaded(id string, state store.BodyState) {
	if err := b.store.MarkBodyState(context.Background(), id, state); err != nil && !errors.Is(err, store.ErrNotFound) {
		b.log.Warn("mark body state after a download", "message", id, "state", state, "err", err)
	}
}

// downloadStopped is the error of a download whose context ended: the
// budget ran out (serverTimeout), the account was removed or paused
// meanwhile (stopAccount: accountNotFound, unavailable), or the daemon is
// shutting down (cancelled).
func (b *Backend) downloadStopped(ctx context.Context, accountID string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return api.NewError(api.CodeServerTimeout, "the download did not finish within %s", downloadBudget)
	}
	a, err := b.store.GetAccount(context.WithoutCancel(ctx), accountID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return api.NewError(api.CodeAccountNotFound, "unknown account %q", accountID)
	case err == nil && !a.Enabled:
		return api.NewError(api.CodeUnavailable, "account %s is paused", accountID)
	}
	return api.NewError(api.CodeCancelled, "the download was cancelled")
}

// codeOf is the contract code of an error, 0 when it carries none.
func codeOf(err error) api.ErrorCode {
	var e *api.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}
