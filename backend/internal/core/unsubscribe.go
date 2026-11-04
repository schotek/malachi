// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-msgauth/dkim"

	"github.com/schotek/malachi/backend/internal/bulk"
	"github.com/schotek/malachi/backend/internal/ingest"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/oneclick"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

const (
	// dkimTimeout bounds the DNS lookups of one verification.
	dkimTimeout = 10 * time.Second
	// dkimMaxSignatures is how many signatures of a message are checked.
	dkimMaxSignatures = 5
	// headBytes is how much of a stored message is read to learn its
	// headers (mime.DefaultLimits().MaxHeaderBytes).
	headBytes = 256 << 10

	unsubscribeDefaultSubject = "unsubscribe"

	// unsubscribeRepeat is how long an unsubscription is answered again
	// without a new request.
	unsubscribeRepeat = 60 * time.Second
)

// claimUnsubscribe marks an unsubscription of key in flight; false when
// one already is.
func (b *Backend) claimUnsubscribe(accountID, key string) bool {
	b.unsubMu.Lock()
	defer b.unsubMu.Unlock()
	k := accountID + "\x00" + key
	if b.unsubBusy[k] {
		return false
	}
	if b.unsubBusy == nil {
		b.unsubBusy = map[string]bool{}
	}
	b.unsubBusy[k] = true
	return true
}

func (b *Backend) releaseUnsubscribe(accountID, key string) {
	b.unsubMu.Lock()
	delete(b.unsubBusy, accountID+"\x00"+key)
	b.unsubMu.Unlock()
}

// unsubscribeOffer is Message.unsubscribe for the stored message m: the
// method internal/bulk chooses from its headers, absent for an
// issue-tracker account, for a message in the junk folder or flagged
// junk, and when the headers offer nothing usable; unsubscribedAt is the
// remembered unsubscription of its list or sender.
func (b *Backend) unsubscribeOffer(ctx context.Context, a store.Account, m store.Message) (*api.UnsubscribeOffer, error) {
	if isIssueAccount(a) {
		return nil, nil
	}
	o := bulk.Choose(m.Headers, api.BulkKind(m.Bulk))
	if o == nil {
		return nil, nil
	}
	junk, err := b.isJunk(ctx, m)
	if err != nil || junk {
		return nil, err
	}
	out := &api.UnsubscribeOffer{Method: o.Method, Target: o.Target}
	if o.Method == api.UnsubscribeURL {
		out.URL = o.URI
	}
	u, ok, err := b.store.GetUnsubscription(ctx, a.ID, rememberKey(m))
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if ok && !u.At.IsZero() {
		at := u.At
		out.UnsubscribedAt = &at
	}
	return out, nil
}

// rememberKey is the key under which the unsubscription from m's list or
// sender is remembered.
func rememberKey(m store.Message) string {
	var from string
	if len(m.From) > 0 {
		from = m.From[0].Address
	}
	return bulk.RememberKey(m.ListID, from)
}

// isJunk says whether m is in a junk-role folder or flagged junk.
func (b *Backend) isJunk(ctx context.Context, m store.Message) (bool, error) {
	if slices.Contains(m.Flags, api.FlagJunk) {
		return true, nil
	}
	f, err := b.store.GetFolder(ctx, m.AccountID, m.FolderID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return false, nil
	case err != nil:
		return false, api.NewError(api.CodeStorageError, "%v", err)
	}
	return f.Role == api.RoleJunk, nil
}

// Unsubscribe acts on the unsubscribe offer of a message (docs/api.md
// §4.3, message.unsubscribe). The client names only the message: the
// method, the URL and the address are read again from the stored message,
// never taken from the caller. A page (method url) is only handed back; a
// one-click request is sent only when it is verified (below), else the
// outcome is unverified and nothing is sent, the one-click URL is never
// handed back; a mailto: request is queued in the outbox of the account
// the message arrived in. p.Method mailto asks for the message's mailto:
// alternative (after the user confirmed an unverified outcome).
//
// Verified: for an IMAP account a DKIM signature of the sender's own
// organisation, checked here, covers List-Unsubscribe and
// List-Unsubscribe-Post; for a Graph account, whose MIME Exchange rebuilt,
// the topmost Authentication-Results field (Exchange's own) says dkim=pass
// for such a domain and the message has a DKIM-Signature of it signing
// both fields.
func (s *messageService) Unsubscribe(ctx context.Context, p api.MessageUnsubscribeParams) (*api.MessageUnsubscribeResult, error) {
	if p.AccountID == "" || p.MessageID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and messageId are required")
	}
	if p.Method != "" && p.Method != api.UnsubscribeMailto {
		return nil, api.NewError(api.CodeInvalidArgument, "method must be empty or mailto")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	if isIssueAccount(a) {
		return nil, api.NewError(api.CodeInvalidArgument, "unsubscribing is not available for this account")
	}
	m, err := s.b.getMessage(ctx, a.ID, string(p.MessageID))
	if err != nil {
		return nil, err
	}
	if junk, err := s.b.isJunk(ctx, m); err != nil {
		return nil, err
	} else if junk {
		return nil, api.NewError(api.CodeInvalidArgument, "the message is junk; unsubscribing would confirm the address")
	}
	kind := api.BulkKind(m.Bulk)
	stored := bulk.Choose(m.Headers, kind)
	if stored == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "the message offers no way to unsubscribe")
	}

	wantMailto := p.Method == api.UnsubscribeMailto
	if wantMailto && bulk.MailtoAlternative(m.Headers) == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "the message has no mailto: address to unsubscribe at")
	}
	if wantMailto || stored.Method != api.UnsubscribeURL {
		// One request at a time per list or sender, and a repeat within
		// unsubscribeRepeat (a double click, a retrying agent) is answered
		// with what the first one did.
		key := rememberKey(m)
		if !s.b.claimUnsubscribe(a.ID, key) {
			return nil, api.NewError(api.CodeConflict, "an unsubscribe request for this sender is already running")
		}
		defer s.b.releaseUnsubscribe(a.ID, key)
		u, ok, err := s.b.store.GetUnsubscription(ctx, a.ID, key)
		if err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
		if ok && !u.At.IsZero() && time.Since(u.At) < unsubscribeRepeat {
			at := u.At
			outcome := api.UnsubscribeDone
			if u.Method == "mailto" {
				outcome = api.UnsubscribeQueued
			}
			return &api.MessageUnsubscribeResult{Outcome: outcome, UnsubscribedAt: &at}, nil
		}
	}

	choose := func(h map[string]string) *bulk.Offer {
		if wantMailto {
			return bulk.MailtoAlternative(h)
		}
		return bulk.Choose(h, kind)
	}
	src, err := s.b.unsubscribeSource(ctx, a, m, !wantMailto && stored.Method == api.UnsubscribeOneClick)
	if err != nil {
		return nil, err
	}
	offer := choose(src.headers)
	if offer != nil && offer.Method == api.UnsubscribeOneClick && !src.triedWhole {
		if src, err = s.b.unsubscribeSource(ctx, a, m, true); err != nil {
			return nil, err
		}
		offer = choose(src.headers)
	}
	if offer == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "the message offers no way to unsubscribe")
	}

	switch offer.Method {
	case api.UnsubscribeURL:
		return &api.MessageUnsubscribeResult{Outcome: api.UnsubscribeOpenURL, URL: offer.URI}, nil
	case api.UnsubscribeOneClick:
		return s.b.unsubscribeOneClick(ctx, a, m, offer, src)
	default:
		return s.b.unsubscribeMailto(ctx, a, m, offer)
	}
}

// unsubSource is what the stored message says about its own headers.
type unsubSource struct {
	// headers are the curated headers read from the message itself (the
	// stored copy of the row when neither the file nor a held copy is
	// there).
	headers map[string]string
	// repeated is set when the message has more than one From,
	// List-Unsubscribe or List-Unsubscribe-Post field: the one shown is
	// then not necessarily the one a signature covers.
	repeated bool
	// whole is the complete message (nil when not asked for or not
	// available); triedWhole says it was asked for.
	whole      []byte
	triedWhole bool
	// head is the beginning of the message (its header block), nil when
	// the message is not stored.
	head []byte
}

var guardedHeaders = []string{"From", "List-Unsubscribe", "List-Unsubscribe-Post"}

// unsubscribeSource reads the headers of m again from the message as
// stored. With needWhole it makes the message whole first (the way
// message.download does, so under neverStoreAttachments the whole copy is
// held in memory only) and keeps the complete bytes for the DKIM check;
// a message too big to hold, or one the daemon cannot make whole, leaves
// whole nil and the check fails closed.
func (b *Backend) unsubscribeSource(ctx context.Context, a store.Account, m store.Message, verify bool) (unsubSource, error) {
	src := unsubSource{triedWhole: verify}
	var head []byte
	graph := a.Config.Protocol() == api.AccountGraph
	if verify && graph {
		// Exchange's verdict is in the headers: the stored file's head
		// will do, and the whole message is fetched only when there is
		// none yet.
		head = b.localHead(ctx, m)
	}
	if verify && head == nil {
		whole, err := b.wholeMessage(ctx, a, m)
		if err != nil {
			return src, err
		}
		if !graph {
			src.whole = whole
		}
		head = whole[:min(len(whole), headBytes)]
		if whole == nil {
			head = nil
		}
	}
	if head == nil {
		head = b.localHead(ctx, m)
	}
	src.head = head
	if head == nil {
		src.headers = m.Headers
		return src, nil
	}
	_, src.headers = mime.ParseHeaderFields(bytes.NewReader(head), mime.DefaultLimits())
	for _, n := range mime.CountHeaderFields(bytes.NewReader(head), mime.DefaultLimits(), guardedHeaders...) {
		if n > 1 {
			src.repeated = true
		}
	}
	return src, nil
}

// localHead returns the beginning of the stored message, without the
// network; nil when there is none.
func (b *Backend) localHead(ctx context.Context, m store.Message) []byte {
	if held, ok := b.mem.get(m.AccountID, m.ID); ok {
		return held.raw[:min(len(held.raw), headBytes)]
	}
	f, err := b.store.OpenMessageRaw(ctx, m.AccountID, m.ID)
	if err != nil {
		return nil
	}
	defer f.Close()
	head, err := io.ReadAll(io.LimitReader(f, headBytes))
	if err != nil || len(head) == 0 {
		return nil
	}
	return head
}

// wholeMessage returns the complete message as the server sent it: the
// copy held in memory, else the stored file when it is not a reduced
// skeleton. nil when that is not possible (too big, reduced and not held);
// a failed download is an error.
func (b *Backend) wholeMessage(ctx context.Context, a store.Account, m store.Message) ([]byte, error) {
	if err := b.ensureWhole(ctx, a, m); err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == api.CodeAttachmentTooBig {
			return nil, nil
		}
		return nil, err
	}
	if held, ok := b.mem.get(m.AccountID, m.ID); ok {
		return held.raw, nil
	}
	now, err := b.getMessage(ctx, a.ID, m.ID)
	if err != nil {
		return nil, err
	}
	if now.RawState == store.RawPartial {
		return nil, nil
	}
	f, err := b.store.OpenMessageRaw(ctx, a.ID, m.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, ingest.MaxMessageBytes+1))
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if int64(len(raw)) > ingest.MaxMessageBytes {
		return nil, nil
	}
	return raw, nil
}

// unsubscribeOneClick verifies the message and sends the RFC 8058
// request.
func (b *Backend) unsubscribeOneClick(ctx context.Context, a store.Account, m store.Message, offer *bulk.Offer, src unsubSource) (*api.MessageUnsubscribeResult, error) {
	if !b.oneClickVerified(ctx, a, m, src) {
		// Nothing is sent and the one-click URL is never handed back (a
		// one-click endpoint need not answer a GET); the message's mailto:
		// address, if it has one, is what the client may offer instead.
		res := &api.MessageUnsubscribeResult{Outcome: api.UnsubscribeUnverified}
		if alt := bulk.MailtoAlternative(src.headers); alt != nil {
			res.Mailto = alt.Target
		}
		return res, nil
	}
	post := b.OneClickPost
	if post == nil {
		post = oneclick.New().Post
	}
	if err := post(ctx, offer.URI); err != nil {
		var se *oneclick.StatusError
		switch {
		case errors.As(err, &se):
			b.log.Info("unsubscribe request refused", "account", a.ID, "status", se.Code)
			return nil, api.NewError(api.CodeUnsubscribeFailed, "the sender's server answered %d", se.Code)
		case ctx.Err() != nil:
			return nil, api.NewError(api.CodeCancelled, "the call ended")
		case errors.Is(err, oneclick.ErrBlockedAddress):
			b.log.Info("unsubscribe request refused", "account", a.ID, "reason", "non-public address")
			return nil, api.NewError(api.CodeUnsubscribeFailed, "the sender's server is not on a public address")
		}
		// Never the error itself: a DNS or TLS error names the host.
		b.log.Info("unsubscribe request failed", "account", a.ID, "class", oneclick.Class(err))
		return nil, api.NewError(api.CodeNetworkError, "the sender's server could not be reached")
	}
	at, err := b.rememberUnsubscription(ctx, a, m, "oneClick")
	if err != nil {
		return nil, err
	}
	b.log.Info("unsubscribe request accepted", "account", a.ID)
	return &api.MessageUnsubscribeResult{Outcome: api.UnsubscribeDone, UnsubscribedAt: &at}, nil
}

// rememberUnsubscription records the unsubscription from m's list or
// sender and returns its time.
func (b *Backend) rememberUnsubscription(ctx context.Context, a store.Account, m store.Message, method string) (time.Time, error) {
	at := time.Now().UTC().Truncate(time.Second)
	if err := b.store.RememberUnsubscription(ctx, a.ID, rememberKey(m), method, at); err != nil {
		return time.Time{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	return at, nil
}

// unsubscribeMailto queues the unsubscribe request of a mailto: offer in
// the account's outbox, as a plain-text message from the account's own
// address (so it appears in Sent), and remembers it. Only the first
// address of the URI is used, with its subject and body.
func (b *Backend) unsubscribeMailto(ctx context.Context, a store.Account, m store.Message, offer *bulk.Offer) (*api.MessageUnsubscribeResult, error) {
	f, err := parseMailto(offer.URI)
	if err != nil || len(f.To) == 0 {
		return nil, api.NewError(api.CodeInvalidArgument, "the unsubscribe address is not usable")
	}
	to := f.To[:1]
	if err := validateAddress(to[0]); err != nil {
		return nil, api.NewError(api.CodeInvalidArgument, "the unsubscribe address is not usable")
	}
	subject := f.Subject
	if subject == "" {
		subject = unsubscribeDefaultSubject
	}
	now := time.Now()
	from := api.Address{Name: a.Config.DisplayName, Address: a.Config.Email}
	in := smtp.BuildInput{
		From:      from,
		To:        to,
		Subject:   subject,
		Text:      f.Body,
		Date:      now,
		MessageID: smtp.NewMessageID(a.Config.Email),
	}
	queued, err := b.store.EnqueueOutbox(ctx, store.EnqueueInput{
		Message: store.Message{
			AccountID:    a.ID,
			From:         []api.Address{from},
			To:           to,
			Subject:      subject,
			Date:         now,
			InternalDate: now,
			RFCMessageID: in.MessageID,
			Snippet:      mime.Snippet(f.Body, snippetRunes),
		},
		Text:         f.Body,
		EnvelopeFrom: a.Config.Email,
		Recipients:   []string{to[0].Address},
		Build:        func(w io.Writer) error { return smtp.BuildMessage(w, in) },
		Limit:        outgoingLimit,
	})
	if err != nil {
		return nil, enqueueError(err, "", store.Draft{})
	}
	b.Delivery.Wake(a.ID)
	b.outboxChanged(a.ID)
	at, err := b.rememberUnsubscription(ctx, a, m, "mailto")
	if err != nil {
		return nil, err
	}
	b.log.Info("unsubscribe request queued", "account", a.ID, "message", queued.ID)
	return &api.MessageUnsubscribeResult{Outcome: api.UnsubscribeQueued, UnsubscribedAt: &at}, nil
}

// oneClickVerified applies the verification rule of the account's kind.
func (b *Backend) oneClickVerified(ctx context.Context, a store.Account, m store.Message, src unsubSource) bool {
	if a.Config.Protocol() != api.AccountGraph {
		return b.dkimVerified(ctx, m, src)
	}
	if src.head == nil || src.repeated || len(m.From) == 0 {
		return false
	}
	fromDomain := bulk.Domain(m.From[0].Address)
	v := mime.HeaderValues(bytes.NewReader(src.head), mime.DefaultLimits(), "Authentication-Results", "DKIM-Signature")
	return bulk.ExchangeVerified(v["Authentication-Results"], v["DKIM-Signature"], fromDomain)
}

// dkimVerified says whether the complete message carries a valid DKIM
// signature of the sender's own organisation that covers the
// List-Unsubscribe and List-Unsubscribe-Post fields (RFC 8058 §3.2), so
// that the one-click URL is the sender's and not something added on the
// way. Anything short of that, a message that is not at hand whole, a
// repeated From or unsubscribe field and every DNS or parse failure
// included, is not verified.
func (b *Backend) dkimVerified(ctx context.Context, m store.Message, src unsubSource) bool {
	if src.whole == nil || src.repeated || len(m.From) == 0 {
		return false
	}
	fromDomain := bulk.Domain(m.From[0].Address)
	if fromDomain == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, dkimTimeout)
	defer cancel()
	lookup := b.LookupTXT
	if lookup == nil {
		lookup = defaultLookupTXT
	}
	verifs, err := dkim.VerifyWithOptions(bytes.NewReader(src.whole), &dkim.VerifyOptions{
		LookupTXT:        func(domain string) ([]string, error) { return lookup(ctx, domain) },
		MaxVerifications: dkimMaxSignatures,
	})
	if err != nil && !errors.Is(err, dkim.ErrTooManySignatures) {
		return false
	}
	for _, v := range verifs {
		if v == nil || v.Err != nil || !bulk.Aligned(v.Domain, fromDomain) {
			continue
		}
		if signs(v.HeaderKeys, "from") && signs(v.HeaderKeys, bulk.HeaderListUnsubscribe) &&
			signs(v.HeaderKeys, bulk.HeaderListUnsubscribePost) {
			return true
		}
	}
	return false
}

// signs reports whether the signed header list names the field.
func signs(keys []string, name string) bool {
	return slices.ContainsFunc(keys, func(k string) bool { return strings.EqualFold(strings.TrimSpace(k), name) })
}

// defaultLookupTXT is the system resolver.
func defaultLookupTXT(ctx context.Context, domain string) ([]string, error) {
	return net.DefaultResolver.LookupTXT(ctx, domain)
}
