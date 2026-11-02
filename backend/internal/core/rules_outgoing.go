// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/bulk"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/safename"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The outgoing actions of the mail rules (forward, autoReply; docs/api.md
// §4.21, R7–R8, docs/security.md §7.5). The Stored hook judges a message
// against the fixed protections (ruleOutgoingRefusal) and queues what
// passes; a worker per mail account takes the queue serially, reserves
// the rule's hourly slot and, for a reply, the sender's interval in one
// store transaction (ReserveRuleOutgoing), builds the message and queues
// it in the account's outbox like unsubscribeMailto. Nothing here runs
// under rules.apply.

const (
	// ruleOutQueueMax bounds one account's queue; what does not fit is
	// dropped (and logged): a burst that large is what the limits stop
	// anyway.
	ruleOutQueueMax = 200
	// ruleOutJobTimeout bounds one job (a forward may download the
	// original).
	ruleOutJobTimeout = 3 * time.Minute
	// ruleAutoForwarded and ruleAutoReplied are the Auto-Submitted
	// values of RFC 3834 the outgoing messages carry.
	ruleAutoForwarded = "auto-forwarded"
	ruleAutoReplied   = "auto-replied"
)

// ruleOutJob is one outgoing action of a rule on a message: the message
// as it was when the rule ran, and for a forward its original when it
// was at hand whole then.
type ruleOutJob struct {
	ruleID    string
	action    api.RuleAction
	accountID string
	msg       store.Message
	raw       []byte
}

type ruleOutQueue struct {
	jobs    []ruleOutJob
	running bool
}

// ruleEnqueue queues a job and starts the account's worker when none
// runs.
func (b *Backend) ruleEnqueue(j ruleOutJob) {
	r := &b.rules
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.queues == nil {
		r.queues = map[string]*ruleOutQueue{}
	}
	q := r.queues[j.accountID]
	if q == nil {
		q = &ruleOutQueue{}
		r.queues[j.accountID] = q
	}
	if len(q.jobs) >= ruleOutQueueMax {
		b.log.Warn("rules: outgoing queue full, action dropped", "rule", j.ruleID, "account", j.accountID)
		return
	}
	r.pending.Add(1)
	q.jobs = append(q.jobs, j)
	if !q.running {
		q.running = true
		go b.ruleOutWorker(j.accountID)
	}
}

// ruleOutNext takes the account's next job; false ends the worker.
func (b *Backend) ruleOutNext(accountID string) (ruleOutJob, bool) {
	r := &b.rules
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.queues[accountID]
	if q == nil || len(q.jobs) == 0 {
		if q != nil {
			q.running = false
		}
		return ruleOutJob{}, false
	}
	j := q.jobs[0]
	q.jobs = q.jobs[1:]
	return j, true
}

func (b *Backend) ruleOutWorker(accountID string) {
	for {
		j, ok := b.ruleOutNext(accountID)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), ruleOutJobTimeout)
		b.ruleOutOne(ctx, j)
		cancel()
		b.rules.pending.Done()
	}
}

// waitRuleOutgoing blocks until every queued outgoing job is done (tests).
func (b *Backend) waitRuleOutgoing() { b.rules.pending.Wait() }

// ruleOutOne carries one job out: the rule must still be enabled with the
// same action, the account an enabled mail account; then the limits, the
// message and the outbox. A refusal counts in the rule's skipped; a
// message that cannot be queued disables the rule (sendFailed).
func (b *Backend) ruleOutOne(ctx context.Context, j ruleOutJob) {
	r, err := b.store.GetRule(ctx, j.ruleID)
	if err != nil || !r.Enabled || !slices.Contains(r.Actions, j.action) {
		return // changed or gone meanwhile: the user's newer word wins
	}
	a, err := b.store.GetAccount(ctx, j.accountID)
	if err != nil || !isMailKind(a) || !a.Enabled {
		return
	}
	now := b.rulesNow()
	sender := ""
	var to api.Address
	if j.action.Kind == api.RuleActionAutoReply {
		var ok bool
		if to, ok = ruleReplyTarget(j.msg); !ok {
			b.ruleOutSkipped(ctx, j, "no usable sender")
			return
		}
		sender = store.NormalizeAddress(to.Address)
	}
	verdict, err := b.store.ReserveRuleOutgoing(ctx, j.ruleID, sender, now, api.RuleOutgoingPerHour, api.RuleReplyInterval)
	switch {
	case err != nil:
		b.log.Warn("rules: reserve an outgoing message", "rule", j.ruleID, "err", err)
		return
	case verdict == store.OutgoingHourly:
		b.ruleOutSkipped(ctx, j, "hourly limit")
		return
	case verdict == store.OutgoingReplied:
		b.ruleOutSkipped(ctx, j, "sender answered recently")
		return
	}
	switch j.action.Kind {
	case api.RuleActionForward:
		err = b.ruleForward(ctx, a, j, now)
	case api.RuleActionAutoReply:
		err = b.ruleAutoReply(ctx, a, j, to, now)
	}
	var skip *ruleSkip
	switch {
	case err == nil:
		b.Delivery.Wake(a.ID)
		b.outboxChanged(a.ID)
		b.log.Info("rules: message queued", "rule", j.ruleID, "kind", j.action.Kind, "account", a.ID)
	case errors.As(err, &skip):
		b.ruleOutSkipped(ctx, j, skip.reason)
	default:
		b.log.Warn("rules: outgoing message not queued", "rule", j.ruleID, "kind", j.action.Kind, "err", err)
		b.disableRule(ctx, j.ruleID, api.RuleProblemSendFailed)
	}
}

// ruleSkip is a job left out for a reason that is not the rule's fault
// (the original is not at hand, it is too big to send).
type ruleSkip struct{ reason string }

func (e *ruleSkip) Error() string { return e.reason }

func (b *Backend) ruleOutSkipped(ctx context.Context, j ruleOutJob, reason string) {
	b.log.Info("rules: outgoing action skipped", "rule", j.ruleID, "kind", j.action.Kind, "reason", reason)
	if err := b.store.NoteRuleSkipped(ctx, j.ruleID, 1); err != nil {
		b.log.Warn("rules: count", "err", err)
	}
	b.rulesChanged()
}

// ruleHeaders are the extra header fields of a rule's message: the
// Auto-Submitted of RFC 3834 and an X-Loop naming the account.
func ruleHeaders(a store.Account, auto string) []smtp.Header {
	h := []smtp.Header{{Name: "Auto-Submitted", Value: auto}}
	if addr := strings.TrimSpace(a.Config.Email); addr != "" && printableASCII(addr) {
		h = append(h, smtp.Header{Name: "X-Loop", Value: addr})
	}
	return h
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// ruleForward queues the message, attached whole as message/rfc822, to
// the action's address.
func (b *Backend) ruleForward(ctx context.Context, a store.Account, j ruleOutJob, now time.Time) error {
	to := api.Address{Address: j.action.To}
	if err := validateAddress(to); err != nil {
		return err
	}
	raw := j.raw
	if raw == nil {
		var err error
		if raw, err = b.wholeMessage(ctx, a, j.msg); err != nil || raw == nil {
			return &ruleSkip{reason: "the original is not at hand"}
		}
	}
	if int64(len(raw)) > outgoingLimit {
		return &ruleSkip{reason: "the original is too big to forward"}
	}
	from := api.Address{Name: a.Config.DisplayName, Address: a.Config.Email}
	subject := forwardSubject(j.msg.Subject)
	name := safename.Filename(cleanSubject(j.msg.Subject) + ".eml")
	in := smtp.BuildInput{
		From:      from,
		To:        []api.Address{to},
		Subject:   subject,
		Date:      now,
		MessageID: smtp.NewMessageID(a.Config.Email),
		Headers:   ruleHeaders(a, ruleAutoForwarded),
		Attachments: []smtp.Attachment{{
			Filename: name, ContentType: "message/rfc822", Size: int64(len(raw)),
			Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil },
		}},
	}
	ids := smtp.PartIDs(in)
	_, err := b.store.EnqueueOutbox(ctx, store.EnqueueInput{
		Message: store.Message{
			AccountID: a.ID, From: []api.Address{from}, To: in.To, Subject: subject,
			Date: now, InternalDate: now, RFCMessageID: in.MessageID, HasAttachments: true,
			Attachments: []api.Attachment{{PartID: ids[0], Filename: name, ContentType: "message/rfc822", Size: int64(len(raw))}},
		},
		EnvelopeFrom: a.Config.Email,
		Recipients:   []string{to.Address},
		Build:        func(w io.Writer) error { return smtp.BuildMessage(w, in) },
		Limit:        outgoingLimit,
	})
	if errors.Is(err, store.ErrTooBig) {
		return &ruleSkip{reason: "the original is too big to forward"}
	}
	return err
}

// ruleAutoReply queues the action's text to the message's sender.
func (b *Backend) ruleAutoReply(ctx context.Context, a store.Account, j ruleOutJob, to api.Address, now time.Time) error {
	if err := validateAddress(to); err != nil {
		return &ruleSkip{reason: "the sender's address is not usable"}
	}
	subject := capSubject(cleanSubject(j.action.Subject))
	if strings.TrimSpace(subject) == "" {
		subject = replySubject(j.msg.Subject)
	}
	var refs []string
	irt := j.msg.RFCMessageID
	if irt != "" {
		for _, r := range j.msg.References {
			if r != "" && r != irt {
				refs = append(refs, r)
			}
		}
		refs = append(refs, irt)
		if len(refs) > maxOutgoingReferences {
			refs = refs[len(refs)-maxOutgoingReferences:]
		}
	}
	from := api.Address{Name: a.Config.DisplayName, Address: a.Config.Email}
	in := smtp.BuildInput{
		From:       from,
		To:         []api.Address{to},
		Subject:    subject,
		Text:       j.action.Text,
		InReplyTo:  irt,
		References: refs,
		Date:       now,
		MessageID:  smtp.NewMessageID(a.Config.Email),
		Headers:    ruleHeaders(a, ruleAutoReplied),
	}
	_, err := b.store.EnqueueOutbox(ctx, store.EnqueueInput{
		Message: store.Message{
			AccountID: a.ID, From: []api.Address{from}, To: in.To, Subject: subject,
			Date: now, InternalDate: now, RFCMessageID: in.MessageID, InReplyTo: irt, References: refs,
			Snippet: mime.Snippet(j.action.Text, snippetRunes),
		},
		Text:         j.action.Text,
		EnvelopeFrom: a.Config.Email,
		Recipients:   []string{to.Address},
		Build:        func(w io.Writer) error { return smtp.BuildMessage(w, in) },
		Limit:        outgoingLimit,
	})
	return err
}

// ruleReplyTarget is whom an automatic reply answers: the first Reply-To
// address, else the first From address.
func ruleReplyTarget(m store.Message) (api.Address, bool) {
	for _, list := range [][]api.Address{m.ReplyTo, m.From} {
		if len(list) > 0 {
			a := api.Address{Name: list[0].Name, Address: strings.TrimSpace(list[0].Address)}
			if a.Address != "" {
				if c, ok := cleanAddress(a); ok {
					return c, true
				}
				return api.Address{}, false
			}
		}
	}
	return api.Address{}, false
}

// allSelfAddresses are the addresses of every account of the user: a
// rule never answers or forwards mail of the user's own.
func (b *Backend) allSelfAddresses(ctx context.Context, a store.Account) map[string]bool {
	self := selfAddresses(a)
	if list, err := b.store.ListAccounts(ctx); err == nil {
		for _, other := range list {
			for k := range selfAddresses(other) {
				self[k] = true
			}
		}
	}
	return self
}

// ruleOutgoingRefusal is why an outgoing action must not run for m ("" =
// it may; R8): bulk or automatic mail, a loop marker, mail of the user's
// own, of a mailer daemon or a no-reply address, mail in junk or on its
// way there. The limits are the worker's.
func ruleOutgoingRefusal(m store.Message, role, target api.FolderRole, act api.RuleAction, self map[string]bool) string {
	switch {
	case role == api.RoleJunk || target == api.RoleJunk:
		return "junk"
	}
	kind := m.Bulk
	if kind == "" {
		kind = bulk.Classify(m.Headers).Stored()
	}
	if kind != "" && kind != bulk.StoredNone {
		return "bulk"
	}
	if v := headerValue(m.Headers, "Auto-Submitted"); v != "" {
		token := strings.ToLower(strings.TrimSpace(strings.SplitN(v, ";", 2)[0]))
		if token != "no" {
			return "auto-submitted"
		}
	}
	if strings.TrimSpace(headerValue(m.Headers, "X-Loop")) != "" {
		return "loop"
	}
	if len(m.From) == 0 {
		return "no sender"
	}
	addrs := append(append([]api.Address(nil), m.From...), m.ReplyTo...)
	if act.Kind == api.RuleActionForward {
		addrs = m.From
	}
	for _, x := range addrs {
		key := store.NormalizeAddress(x.Address)
		if key == "" {
			continue
		}
		if self[key] {
			return "own address"
		}
		if automaticSender(key) {
			return "automatic sender"
		}
	}
	return ""
}

// automaticSender reports whether a (normalised) address is a mailer
// daemon's, a postmaster's or a no-reply one.
func automaticSender(addr string) bool {
	local := addr
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		local = addr[:i]
	}
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}
	switch local {
	case "mailer-daemon", "postmaster", "mailerdaemon":
		return true
	}
	squeezed := strings.NewReplacer("-", "", "_", "", ".", "").Replace(local)
	return strings.Contains(squeezed, "noreply") || strings.Contains(squeezed, "donotreply")
}
