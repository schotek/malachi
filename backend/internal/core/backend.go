// Package core composes the daemon's internal packages into the api.Backend
// that malachid serves. It embeds rpc.StubBackend and overrides one service
// at a time as they become real; anything not overridden still answers
// notImplemented.
package core

import (
	"github.com/schotek/malachi/backend/internal/config"
	"github.com/schotek/malachi/backend/internal/rpc"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Backend is the production api.Backend.
type Backend struct {
	rpc.StubBackend

	store    *store.Store
	defaults config.Config // config.toml as loaded at startup (bootstrap defaults)
}

var _ api.Backend = (*Backend)(nil)

// New builds the backend over an open store. cfg supplies the defaults for
// preferences that have not been set through config.set.
func New(version string, st *store.Store, cfg config.Config) *Backend {
	return &Backend{
		StubBackend: rpc.StubBackend{Version: version, StorePath: st.Path()},
		store:       st,
		defaults:    cfg,
	}
}

func (b *Backend) Config() api.ConfigService  { return &configService{b} }
func (b *Backend) Senders() api.SenderService { return &senderService{b} }
