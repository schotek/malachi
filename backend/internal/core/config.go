// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

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
	prefOfflineDays   = "sync.offline_days"
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

// Set validates and stores the whole preference set, then wakes the sync
// supervisor so intervals and the retention window take effect.
func (s *configService) Set(ctx context.Context, p api.ConfigSetParams) (*api.ConfigSetResult, error) {
	if err := ValidatePreferences(p.Preferences); err != nil {
		return nil, err
	}
	st := s.b.store
	for _, kv := range []struct{ key, value string }{
		{prefSyncInterval, strconv.Itoa(p.Preferences.SyncIntervalSeconds)},
		{prefRemoteContent, string(p.Preferences.RemoteContent)},
		{prefOfflineDays, strconv.Itoa(p.Preferences.OfflineDays)},
	} {
		if err := st.SetPreference(ctx, kv.key, kv.value); err != nil {
			return nil, api.NewError(api.CodeStorageError, "%v", err)
		}
	}
	s.b.preferencesChanged()
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
	if p.OfflineDays < 0 || p.OfflineDays > api.OfflineDaysMax {
		return api.NewError(api.CodeInvalidArgument,
			"offlineDays must be 0 (everything) or 1–%d", api.OfflineDaysMax)
	}
	return nil
}

// preferences resolves the effective values.
func (b *Backend) preferences(ctx context.Context) (api.Preferences, error) {
	p := api.Preferences{
		SyncIntervalSeconds: b.defaults.Sync.IntervalSeconds,
		RemoteContent:       api.RemoteBlock,
		OfflineDays:         b.defaults.Sync.OfflineDays,
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
	if v, ok, err := b.store.GetPreference(ctx, prefOfflineDays); err != nil {
		return p, api.NewError(api.CodeStorageError, "%v", err)
	} else if ok {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= api.OfflineDaysMax {
			p.OfflineDays = n
		}
	}
	return p, nil
}

// preferencesChanged wakes every syncer so a new interval or retention
// window takes effect without waiting for the next pass.
func (b *Backend) preferencesChanged() { b.Supervisor.Reload() }
