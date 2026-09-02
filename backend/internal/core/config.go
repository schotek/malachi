package core

import (
	"context"
	"strconv"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Preference keys in the store. Owned by this package.
const (
	prefSyncInterval  = "sync.interval_seconds"
	prefRemoteContent = "remote_content"
)

type configService struct{ b *Backend }

// Get returns the effective preferences: stored value, else config.toml,
// else the built-in default.
func (s *configService) Get(ctx context.Context, _ api.ConfigGetParams) (*api.ConfigGetResult, error) {
	p, err := s.b.preferences(ctx)
	if err != nil {
		return nil, err
	}
	return &api.ConfigGetResult{Preferences: p}, nil
}

// Set validates and stores the whole preference set.
//
// TODO(phase-1): the sync engine reads the interval from here and must be
// told about changes (restart its ticker).
func (s *configService) Set(ctx context.Context, p api.ConfigSetParams) (*api.ConfigSetResult, error) {
	if err := ValidatePreferences(p.Preferences); err != nil {
		return nil, err
	}
	st := s.b.store
	if err := st.SetPreference(ctx, prefSyncInterval, strconv.Itoa(p.Preferences.SyncIntervalSeconds)); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if err := st.SetPreference(ctx, prefRemoteContent, string(p.Preferences.RemoteContent)); err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return &api.ConfigSetResult{Preferences: p.Preferences}, nil
}

// ValidatePreferences returns invalidArgument for values the daemon does not
// accept.
func ValidatePreferences(p api.Preferences) error {
	if p.SyncIntervalSeconds != 0 && p.SyncIntervalSeconds < api.SyncIntervalMin {
		return api.NewError(api.CodeInvalidArgument,
			"syncIntervalSeconds must be 0 (manual) or at least %d", api.SyncIntervalMin)
	}
	switch p.RemoteContent {
	case api.RemoteBlock, api.RemoteKnownSenders, api.RemoteAllow:
	default:
		return api.NewError(api.CodeInvalidArgument, "remoteContent must be block, knownSenders or allow")
	}
	return nil
}

// preferences resolves the effective values.
func (b *Backend) preferences(ctx context.Context) (api.Preferences, error) {
	p := api.Preferences{
		SyncIntervalSeconds: b.defaults.Sync.IntervalSeconds,
		RemoteContent:       api.RemoteBlock,
	}
	if v, ok, err := b.store.GetPreference(ctx, prefSyncInterval); err != nil {
		return p, api.NewError(api.CodeStorageError, "%v", err)
	} else if ok {
		if n, err := strconv.Atoi(v); err == nil {
			p.SyncIntervalSeconds = n
		}
	}
	if v, ok, err := b.store.GetPreference(ctx, prefRemoteContent); err != nil {
		return p, api.NewError(api.CodeStorageError, "%v", err)
	} else if ok {
		// Unknown stored values (from a newer daemon, say) fall back to block.
		if pol := api.RemoteContentPolicy(v); ValidatePreferences(api.Preferences{RemoteContent: pol}) == nil {
			p.RemoteContent = pol
		}
	}
	return p, nil
}
