// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package core composes the daemon's internal packages into the api.Backend
// that malachid serves. It embeds rpc.StubBackend and overrides one service
// at a time as they become real; anything not overridden still answers
// notImplemented.
package core

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/schotek/malachi/backend/internal/auth"
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/discover"
	"github.com/schotek/malachi/backend/internal/imap"
	"github.com/schotek/malachi/backend/internal/rpc"
	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// attachmentSweepAge is how long an imported attachment may stay unbound
// (never listed by a draft.save) before the sweep deletes it.
const attachmentSweepAge = 24 * time.Hour

// Backend is the production api.Backend.
type Backend struct {
	rpc.StubBackend

	store    *store.Store
	defaults config.Config // config.toml as loaded at startup (bootstrap defaults)
	log      *slog.Logger

	// Sanitize is the HTML sanitiser used for composed bodies. It defaults
	// to sanitize.Sanitize and is a field so tests can substitute a fake.
	Sanitize func(sanitize.Input) (sanitize.Output, error)

	// Keyring stores account secrets. It defaults to the not-implemented
	// placeholder and is a field so tests can substitute an in-memory one.
	Keyring auth.Keyring

	// ProbeIMAP and ProbeSMTP back account.test. They default to the real
	// probes and are fields so tests can substitute fakes.
	ProbeIMAP func(ctx context.Context, cfg api.ServerConfig, password string) (imap.ProbeResult, error)
	ProbeSMTP func(ctx context.Context, cfg api.ServerConfig, password string) (smtp.ProbeResult, error)
	// Discover backs account.discover; a field for the same reason.
	Discover func(ctx context.Context, email string) (discover.Result, error)

	mu       sync.RWMutex
	notifier api.Notifier // nil until SetNotifier
}

var _ api.Backend = (*Backend)(nil)

// New builds the backend over an open store. cfg supplies the defaults for
// preferences that have not been set through config.set.
func New(version string, st *store.Store, cfg config.Config, log *slog.Logger) *Backend {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Backend{
		StubBackend: rpc.StubBackend{Version: version, StorePath: st.Path()},
		store:       st,
		defaults:    cfg,
		log:         log.With("component", "core"),
		Sanitize:    sanitize.Sanitize,
		Keyring:     auth.NotImplementedKeyring{},
		ProbeIMAP:   imap.Probe,
		ProbeSMTP:   smtp.Probe,
		Discover:    discover.New(log).Discover,
	}
}

// SetNotifier wires the RPC server so that services can push notifications.
// malachid calls it right after rpc.NewServer, before Listen. Without a
// notifier, notifications are dropped.
func (b *Backend) SetNotifier(n api.Notifier) {
	b.mu.Lock()
	b.notifier = n
	b.mu.Unlock()
}

func (b *Backend) getNotifier() api.Notifier {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.notifier
}

func (b *Backend) Accounts() api.AccountService       { return &accountService{b} }
func (b *Backend) Config() api.ConfigService          { return &configService{b} }
func (b *Backend) Senders() api.SenderService         { return &senderService{b} }
func (b *Backend) Drafts() api.DraftService           { return &draftService{b} }
func (b *Backend) Attachments() api.AttachmentService { return &attachmentService{b} }

// Maintain runs periodic housekeeping until ctx is cancelled: the orphan
// attachment sweep at start and then hourly.
func (b *Backend) Maintain(ctx context.Context) {
	sweep := func() {
		n, err := b.store.SweepAttachments(ctx, attachmentSweepAge)
		if err != nil {
			b.log.Warn("attachment sweep", "err", err)
		} else if n > 0 {
			b.log.Info("attachment sweep", "removed", n)
		}
	}
	sweep()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}
